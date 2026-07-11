package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

const (
	authCookieName     = "borealis_auth"
	authTokenSalt      = "borealis-auth"
	defaultUserRole    = "User"
	directoryAuth      = "directory"
	defaultAuthTimeout = 3 * time.Second
)

var (
	errMissingToken      = errors.New("missing auth token")
	errInvalidToken      = errors.New("invalid auth token")
	errExpiredToken      = errors.New("expired auth token")
	errOperatorNotFound  = errors.New("operator not found")
	errOperatorAuthBlock = errors.New("operator auth blocked")
	errOperatorStoreDown = errors.New("operator store unavailable")
)

type operatorIdentity struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type operatorProfile struct {
	ID                  int64
	Username            string
	DisplayName         string
	Role                string
	MFAEnabled          bool
	PasskeyCount        int
	AuthSource          string
	DirectoryProviderID int64
	DirectoryDisabled   bool
}

type operatorStore interface {
	lookupOperator(ctx context.Context, username string, fallbackRole string) (operatorProfile, error)
}

type postgresOperatorStore struct {
	db                  *sql.DB
	patchPolicySchemaMu sync.Mutex
	patchPolicySchemaOK bool
}

type authService struct {
	verifier      *tokenVerifier
	store         operatorStore
	bootstrapGate operatorAuthGate
	aegis         authSecretService
	devMode       *goDevModeManager
	timeout       time.Duration
}

type operatorAuthGate interface {
	operatorAuthAllowed(ctx context.Context) (bool, error)
}

type authSecretService interface {
	status(ctx context.Context) (map[string]any, error)
	setupWithCipher(ctx context.Context, cipherText string) (map[string]any, error)
	unlockWithCipher(ctx context.Context, cipherText string) (map[string]any, error)
	rotateWithCipher(ctx context.Context, currentCipher string, newCipher string) (map[string]any, error)
	forceReset(ctx context.Context) (map[string]any, error)
	decryptSecretText(ctx context.Context, value any) (string, error)
	encryptSecretText(ctx context.Context, value string) (string, error)
}

type tokenVerifier struct {
	secret []byte
	maxAge time.Duration
	now    func() time.Time
}

type authFailure struct {
	status int
	body   map[string]any
}

func (f authFailure) write(w http.ResponseWriter) {
	writeJSON(w, f.status, f.body)
}

func newAuthService(cfg gatewayConfig) (*authService, func(), error) {
	secret, err := loadOrCreateEngineSecret(cfg.EngineSecretPath)
	if err != nil {
		return nil, nil, err
	}

	store, closeStore, err := openOperatorStore(cfg)
	if err != nil {
		return nil, nil, err
	}

	var aegis *goAegisService
	if pgStore, ok := store.(*postgresOperatorStore); ok {
		aegis = newGoAegisService(pgStore.db, []byte(secret))
	}

	return &authService{
		verifier: &tokenVerifier{
			secret: []byte(secret),
			maxAge: cfg.AuthTokenTTL,
			now:    time.Now,
		},
		store: store,
		bootstrapGate: &goBootstrapGate{
			store: store,
			aegis: aegis,
		},
		aegis:   aegis,
		devMode: newGoDevModeManager(),
		timeout: cfg.AuthTimeout,
	}, closeStore, nil
}

func openOperatorStore(cfg gatewayConfig) (operatorStore, func(), error) {
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return nil, func() {}, fmt.Errorf("BOREALIS_DATABASE_URL is required")
	}
	db, err := sql.Open("postgres", normalizePostgresDriverURL(cfg))
	if err != nil {
		return nil, func() {}, err
	}
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxIdleTime(5 * time.Minute)
	store := &postgresOperatorStore{db: db}
	bootstrapTimeout := cfg.DBConnectTimeout + 15*time.Second
	if bootstrapTimeout < 15*time.Second {
		bootstrapTimeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), bootstrapTimeout)
	defer cancel()
	if err := store.ensureAssemblyTables(ctx); err != nil {
		_ = db.Close()
		return nil, func() {}, fmt.Errorf("failed to ensure assembly tables: %w", err)
	}
	return store, func() { _ = db.Close() }, nil
}

func normalizePostgresDriverURL(cfg gatewayConfig) string {
	raw := strings.TrimSpace(cfg.DatabaseURL)
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return raw
	}
	query := parsed.Query()
	if strings.TrimSpace(query.Get("sslmode")) == "" {
		query.Set("sslmode", strings.TrimSpace(cfg.DBSSLMode))
	}
	if strings.TrimSpace(query.Get("connect_timeout")) == "" && cfg.DBConnectTimeout > 0 {
		query.Set("connect_timeout", strconv.Itoa(int(cfg.DBConnectTimeout/time.Second)))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (s *postgresOperatorStore) lookupOperator(ctx context.Context, username string, fallbackRole string) (operatorProfile, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return operatorProfile{}, errOperatorNotFound
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return operatorProfile{}, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	roleFallback := strings.TrimSpace(fallbackRole)
	if roleFallback == "" {
		roleFallback = defaultUserRole
	}

	var profile operatorProfile
	var mfaEnabled int
	var directoryDisabled int
	err = conn.QueryRowContext(
		ctx,
		`
		SELECT
			id,
			username,
			COALESCE(display_name, ''),
			COALESCE(role, $1),
			CASE WHEN COALESCE(mfa_disabled, 0) = 1 THEN 0 ELSE 1 END AS mfa_enabled,
			COALESCE(auth_source, 'local'),
			COALESCE(directory_provider_id, 0),
			COALESCE(directory_disabled, 0)
		FROM engine.users
		WHERE LOWER(username)=LOWER($2)
		LIMIT 1
		`,
		roleFallback,
		username,
	).Scan(
		&profile.ID,
		&profile.Username,
		&profile.DisplayName,
		&profile.Role,
		&mfaEnabled,
		&profile.AuthSource,
		&profile.DirectoryProviderID,
		&directoryDisabled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return operatorProfile{}, errOperatorNotFound
	}
	if err != nil {
		return operatorProfile{}, errors.Join(errOperatorStoreDown, err)
	}

	profile.AuthSource = strings.ToLower(strings.TrimSpace(profile.AuthSource))
	if profile.AuthSource == "" {
		profile.AuthSource = "local"
	}
	profile.MFAEnabled = mfaEnabled != 0
	profile.DirectoryDisabled = directoryDisabled != 0
	if profile.DirectoryDisabled && profile.AuthSource == directoryAuth {
		return operatorProfile{}, errOperatorNotFound
	}
	if strings.TrimSpace(profile.DisplayName) == "" {
		profile.DisplayName = profile.Username
	}
	if strings.TrimSpace(profile.Role) == "" {
		profile.Role = roleFallback
	}
	if profile.AuthSource != directoryAuth {
		passkeyCount, err := countUserPasskeys(ctx, conn, profile.Username)
		if err != nil {
			return operatorProfile{}, errors.Join(errOperatorStoreDown, err)
		}
		profile.PasskeyCount = passkeyCount
	}
	return profile, nil
}

func countUserPasskeys(ctx context.Context, conn *sql.Conn, username string) (int, error) {
	var count int
	err := conn.QueryRowContext(
		ctx,
		`
		SELECT COUNT(*)
		FROM engine.user_passkeys up
		JOIN engine.users u ON u.id = up.user_id
		WHERE LOWER(u.username)=LOWER($1)
		`,
		username,
	).Scan(&count)
	return count, err
}

func registerAuthRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("GET /api/bootstrap/state", bootstrapStateHandler(auth))
	mux.HandleFunc("POST /api/bootstrap/aegis/setup", bootstrapAegisSetupHandler(auth))
	mux.HandleFunc("POST /api/bootstrap/aegis/unlock", bootstrapAegisUnlockHandler(auth))
	mux.HandleFunc("POST /api/bootstrap/admin/setup", bootstrapAdminSetupHandler(auth))
	mux.HandleFunc("POST /api/bootstrap/admin/recover", bootstrapAdminRecoverHandler(auth))
	mux.HandleFunc("POST /api/bootstrap/admin/mfa/verify", bootstrapAdminMFAVerifyHandler(auth))
	mux.HandleFunc("POST /api/auth/login", authLoginHandler(auth, fallback))
	mux.HandleFunc("POST /api/auth/logout", authLogoutHandler())
	mux.HandleFunc("POST /api/auth/mfa/verify", authMFAVerifyHandler(auth, fallback))
	mux.HandleFunc("/api/auth/me", authMeHandler(auth))
	mux.HandleFunc("/api/auth/mfa/reset", ownMFAResetHandler(auth))
	mux.HandleFunc("POST /api/auth/password/reset", ownPasswordResetHandler(auth))
	mux.HandleFunc("/api/auth/passkeys", authPasskeysHandler(auth, fallback))
	mux.HandleFunc("POST /api/auth/passkeys/register/options", passkeyRegisterOptionsHandler(auth))
	mux.HandleFunc("POST /api/auth/passkeys/register/verify", passkeyRegisterVerifyHandler(auth))
	mux.HandleFunc("POST /api/auth/passkeys/authenticate/options", passkeyAuthenticateOptionsHandler(auth))
	mux.HandleFunc("POST /api/auth/passkeys/authenticate/verify", passkeyAuthenticateVerifyHandler(auth))
	mux.HandleFunc("/api/auth/passkeys/", authPasskeyByIDHandler(auth, fallback))
}

func authLogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clearAuthCookies(w)
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	}
}

func clearAuthCookies(w http.ResponseWriter) {
	expired := time.Unix(0, 0)
	for _, name := range []string{authCookieName, "session"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			Expires:  expired,
			MaxAge:   -1,
			HttpOnly: name == "session",
			SameSite: http.SameSiteLaxMode,
		})
	}
}

func authMeHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":  "auth_unavailable",
				"detail": err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"username":              profile.Username,
			"display_name":          profile.DisplayName,
			"role":                  profile.Role,
			"mfa_enabled":           profile.MFAEnabled,
			"passkey_count":         profile.PasskeyCount,
			"auth_source":           profile.AuthSource,
			"directory_provider_id": profile.DirectoryProviderID,
			"directory_disabled":    profile.DirectoryDisabled,
		})
	}
}

func (a *authService) currentProfile(ctx context.Context, r *http.Request) (operatorProfile, error) {
	if a == nil || a.verifier == nil || a.store == nil {
		return operatorProfile{}, errOperatorStoreDown
	}
	token, err := extractAuthToken(r)
	if err != nil {
		return operatorProfile{}, err
	}
	identity, err := a.verifier.verify(token)
	if err != nil {
		return operatorProfile{}, err
	}

	timeout := a.timeout
	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if a.bootstrapGate != nil {
		allowed, err := a.bootstrapGate.operatorAuthAllowed(requestCtx)
		if err != nil {
			return operatorProfile{}, errors.Join(errOperatorStoreDown, err)
		}
		if !allowed {
			return operatorProfile{}, errOperatorAuthBlock
		}
	}
	return a.store.lookupOperator(requestCtx, identity.Username, identity.Role)
}

func requireUser(ctx context.Context, auth *authService, r *http.Request) (operatorIdentity, *authFailure) {
	profile, err := auth.currentProfile(ctx, r)
	if err == nil {
		return operatorIdentity{Username: profile.Username, Role: profile.Role}, nil
	}
	if isUnauthorizedAuthError(err) {
		return operatorIdentity{}, unauthorizedAuthFailure()
	}
	return operatorIdentity{}, &authFailure{
		status: http.StatusBadGateway,
		body: map[string]any{
			"error":  "auth_unavailable",
			"detail": err.Error(),
		},
	}
}

func requireAdmin(ctx context.Context, auth *authService, r *http.Request) (operatorIdentity, *authFailure) {
	identity, failure := requireUser(ctx, auth, r)
	if failure != nil {
		return operatorIdentity{}, failure
	}
	if strings.EqualFold(strings.TrimSpace(identity.Role), "admin") {
		return identity, nil
	}
	return operatorIdentity{}, &authFailure{
		status: http.StatusForbidden,
		body: map[string]any{
			"error":   "forbidden",
			"message": "Administrator permissions are required for this action.",
		},
	}
}

func unauthorizedAuthFailure() *authFailure {
	return &authFailure{
		status: http.StatusUnauthorized,
		body: map[string]any{
			"error":   "unauthorized",
			"message": "Authentication required. Please sign in and retry.",
		},
	}
}

func isUnauthorizedAuthError(err error) bool {
	return errors.Is(err, errMissingToken) ||
		errors.Is(err, errInvalidToken) ||
		errors.Is(err, errExpiredToken) ||
		errors.Is(err, errOperatorAuthBlock) ||
		errors.Is(err, errOperatorNotFound)
}

func extractAuthToken(r *http.Request) (string, error) {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authHeader) >= 7 && strings.EqualFold(authHeader[:6], "Bearer") && authHeader[6] == ' ' {
		if token := strings.TrimSpace(authHeader[7:]); token != "" {
			return token, nil
		}
	}
	if cookie, err := r.Cookie(authCookieName); err == nil {
		if token := strings.TrimSpace(cookie.Value); token != "" {
			return token, nil
		}
	}
	return "", errMissingToken
}

func (v *tokenVerifier) verify(token string) (operatorIdentity, error) {
	if v == nil || len(v.secret) == 0 {
		return operatorIdentity{}, errInvalidToken
	}
	payload, issuedAt, err := v.unsign(token)
	if err != nil {
		return operatorIdentity{}, err
	}

	now := time.Now()
	if v.now != nil {
		now = v.now()
	}
	age := now.Unix() - int64(issuedAt)
	if age < 0 {
		return operatorIdentity{}, errExpiredToken
	}
	if v.maxAge > 0 && age > int64(v.maxAge/time.Second) {
		return operatorIdentity{}, errExpiredToken
	}

	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return operatorIdentity{}, errInvalidToken
	}
	username := strings.TrimSpace(fmt.Sprint(data["u"]))
	if username == "" || username == "<nil>" {
		return operatorIdentity{}, errInvalidToken
	}
	role := strings.TrimSpace(fmt.Sprint(data["r"]))
	if role == "" || role == "<nil>" {
		role = defaultUserRole
	}
	return operatorIdentity{Username: username, Role: role}, nil
}

func (v *tokenVerifier) signPayload(payload map[string]any) (string, error) {
	if v == nil || len(v.secret) == 0 {
		return "", errInvalidToken
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	now := time.Now()
	if v.now != nil {
		now = v.now()
	}
	payloadSegment := base64.RawURLEncoding.EncodeToString(raw)
	timestampSegment := encodeTimestamp(uint64(now.Unix()))
	value := payloadSegment + "." + timestampSegment
	return value + "." + v.signatureSegment([]byte(value)), nil
}

func (v *tokenVerifier) signedPayload(token string, maxAge time.Duration) (map[string]any, error) {
	payload, issuedAt, err := v.unsign(token)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if v.now != nil {
		now = v.now()
	}
	age := now.Unix() - int64(issuedAt)
	if age < 0 || (maxAge > 0 && age > int64(maxAge/time.Second)) {
		return nil, errExpiredToken
	}
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, errInvalidToken
	}
	return data, nil
}

func (v *tokenVerifier) unsign(token string) ([]byte, uint64, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, 0, errMissingToken
	}

	lastSep := strings.LastIndex(token, ".")
	if lastSep < 0 {
		return nil, 0, errInvalidToken
	}
	value := token[:lastSep]
	signatureSegment := token[lastSep+1:]
	tsSep := strings.LastIndex(value, ".")
	if tsSep < 0 {
		return nil, 0, errInvalidToken
	}
	payloadSegment := value[:tsSep]
	timestampSegment := value[tsSep+1:]
	if payloadSegment == "" || timestampSegment == "" || signatureSegment == "" {
		return nil, 0, errInvalidToken
	}
	if !v.verifySignature([]byte(value), signatureSegment) {
		return nil, 0, errInvalidToken
	}
	issuedAt, err := decodeTimestamp(timestampSegment)
	if err != nil {
		return nil, 0, errInvalidToken
	}
	payload, err := decodePayload(payloadSegment)
	if err != nil {
		return nil, 0, errInvalidToken
	}
	return payload, issuedAt, nil
}

func (v *tokenVerifier) verifySignature(value []byte, signatureSegment string) bool {
	signature, err := base64RawURLDecode(signatureSegment)
	if err != nil {
		return false
	}
	expected := v.signingMAC(value)
	return subtle.ConstantTimeCompare(signature, expected) == 1
}

func (v *tokenVerifier) signatureSegment(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(v.signingMAC(value))
}

func (v *tokenVerifier) signingMAC(value []byte) []byte {
	derived := sha1.Sum(bytes.Join([][]byte{[]byte(authTokenSalt), []byte("signer"), v.secret}, nil))
	mac := hmac.New(sha1.New, derived[:])
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}

func decodeTimestamp(segment string) (uint64, error) {
	raw, err := base64RawURLDecode(segment)
	if err != nil || len(raw) > 8 {
		return 0, errInvalidToken
	}
	padded := make([]byte, 8)
	copy(padded[8-len(raw):], raw)
	return binary.BigEndian.Uint64(padded), nil
}

func encodeTimestamp(value uint64) string {
	raw := make([]byte, 8)
	binary.BigEndian.PutUint64(raw, value)
	for len(raw) > 1 && raw[0] == 0 {
		raw = raw[1:]
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodePayload(segment string) ([]byte, error) {
	compressed := strings.HasPrefix(segment, ".")
	if compressed {
		segment = strings.TrimPrefix(segment, ".")
	}
	raw, err := base64RawURLDecode(segment)
	if err != nil {
		return nil, err
	}
	if !compressed {
		return raw, nil
	}
	reader, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, 1<<20))
}

func base64RawURLDecode(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}

func loadOrCreateEngineSecret(secretPath string) (string, error) {
	envSecret := strings.TrimSpace(os.Getenv("SECRET_KEY"))
	if envSecret != "" {
		return envSecret, nil
	}

	path := strings.TrimSpace(secretPath)
	if path == "" {
		path = "/opt/Borealis/Engine/Services/api-backend/secrets/engine_secret.txt"
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if existing, err := os.ReadFile(path); err == nil {
		if secret := strings.TrimSpace(string(existing)); secret != "" {
			_ = os.Chmod(path, 0o600)
			return secret, nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	randomBytes := make([]byte, 64)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(randomBytes)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return "", err
	}
	return secret, nil
}
