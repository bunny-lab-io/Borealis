package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	agentAccessTokenTTL        = 15 * time.Minute
	refreshTokenSlidingTTL     = 90 * 24 * time.Hour
	agentJWTKeyFilename        = "borealis-jwt-ed25519.key"
	dpopProofSkew              = 5 * time.Minute
	agentTokenRefreshRoutePath = "/api/agent/token/refresh"
)

var (
	errDPoPInvalid = errors.New("dpop_invalid")
	errDPoPReplay  = errors.New("dpop_replayed")
)

type tokenRefreshStore interface {
	refreshAgentToken(ctx context.Context, request agentTokenRefreshRequest) (agentTokenRefreshResult, int, error)
}

type agentTokenRefreshRequest struct {
	GUID         string
	RefreshToken string
	DPoPJKT      string
	HasDPoP      bool
	Now          time.Time
}

type agentTokenRefreshResult struct {
	GUID         string
	Fingerprint  string
	TokenVersion int
	SiteID       *int64
	Route        *agentWorkerRoute
	Reason       string
}

type agentWorkerRoute struct {
	WorkerGUID      string
	SiteID          int64
	RoutePathPrefix string
	Generation      int64
}

type agentJWTSigner struct {
	privateKey ed25519.PrivateKey
	keyID      string
	now        func() time.Time
}

type dpopVerifier struct {
	mu      sync.Mutex
	seenJTI map[string]time.Time
	now     func() time.Time
}

func registerAgentTokenRoutes(mux *http.ServeMux, auth *authService) error {
	signer, err := loadOrCreateAgentJWTSigner()
	if err != nil {
		return fmt.Errorf("failed to initialise agent JWT signer: %w", err)
	}
	scriptSigner, _ := loadOrCreateScriptSigner()
	verifier := &dpopVerifier{seenJTI: map[string]time.Time{}}
	mux.HandleFunc("POST "+agentTokenRefreshRoutePath, agentTokenRefreshHandler(auth, signer, verifier))
	registerAgentEnrollmentRoutes(mux, auth, signer, scriptSigner)
	registerAgentReadRoutes(mux, auth, signer, verifier)
	registerAgentHashRoutes(mux, auth, signer, verifier)
	return nil
}

func agentTokenRefreshHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		store, ok := auth.store.(tokenRefreshStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "token_refresh_unavailable"})
			return
		}

		var body struct {
			GUID         string `json:"guid"`
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		guid := normalizeCanonicalGUID(body.GUID)
		refreshToken := strings.TrimSpace(body.RefreshToken)
		if guid == "" || refreshToken == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}

		now := time.Now().UTC()
		proof := strings.TrimSpace(r.Header.Get("DPoP"))
		jkt := ""
		if proof != "" {
			verifiedJKT, err := dpop.verify(r.Method, absoluteRequestURL(r), proof, now, "")
			if err != nil {
				if errors.Is(err, errDPoPReplay) {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "dpop_replayed"})
					return
				}
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "dpop_invalid"})
				return
			}
			jkt = verifiedJKT
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		result, status, err := store.refreshAgentToken(ctx, agentTokenRefreshRequest{
			GUID:         guid,
			RefreshToken: refreshToken,
			DPoPJKT:      jkt,
			HasDPoP:      proof != "",
			Now:          now,
		})
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}

		accessToken, err := signer.issueAccessToken(result.GUID, result.Fingerprint, result.TokenVersion, agentAccessTokenTTL)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "token_issue_failed"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"access_token":     accessToken,
			"expires_in":       int(agentAccessTokenTTL / time.Second),
			"token_type":       "Bearer",
			"remote_ops_route": buildAgentRemoteOpsRoutePayload(r, result.SiteID, result.Route, result.Reason),
		})
	}
}

func (s *postgresOperatorStore) refreshAgentToken(ctx context.Context, request agentTokenRefreshRequest) (agentTokenRefreshResult, int, error) {
	guid := normalizeCanonicalGUID(request.GUID)
	refreshToken := strings.TrimSpace(request.RefreshToken)
	if guid == "" || refreshToken == "" {
		return agentTokenRefreshResult{}, http.StatusBadRequest, errors.New("invalid_request")
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return agentTokenRefreshResult{}, http.StatusInternalServerError, err
	}
	defer conn.Close()

	barrierExists, err := relationExists(ctx, conn, "engine.device_purge_barriers")
	if err != nil {
		return agentTokenRefreshResult{}, http.StatusInternalServerError, err
	}
	if barrierExists {
		var purgeGUID sql.NullString
		err = conn.QueryRowContext(
			ctx,
			`SELECT guid FROM engine.device_purge_barriers WHERE UPPER(guid)=UPPER($1) LIMIT 1`,
			guid,
		).Scan(&purgeGUID)
		if err == nil {
			return agentTokenRefreshResult{}, http.StatusUnauthorized, errors.New("device_purged")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return agentTokenRefreshResult{}, http.StatusInternalServerError, err
		}
	}

	var recordID, rowGUID, tokenHash, createdAt, expiresAt sql.NullString
	var storedJKT, revokedAt sql.NullString
	err = conn.QueryRowContext(
		ctx,
		`
		SELECT id, guid, token_hash, dpop_jkt, created_at, expires_at, revoked_at
		  FROM engine.refresh_tokens
		 WHERE UPPER(guid)=UPPER($1)
		   AND token_hash=$2
		 LIMIT 1
		`,
		guid,
		hashRefreshToken(refreshToken),
	).Scan(&recordID, &rowGUID, &tokenHash, &storedJKT, &createdAt, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return agentTokenRefreshResult{}, http.StatusUnauthorized, errors.New("invalid_refresh_token")
	}
	if err != nil {
		return agentTokenRefreshResult{}, http.StatusInternalServerError, err
	}
	if normalizeCanonicalGUID(rowGUID.String) != guid {
		return agentTokenRefreshResult{}, http.StatusUnauthorized, errors.New("invalid_refresh_token")
	}
	if strings.TrimSpace(revokedAt.String) != "" {
		return agentTokenRefreshResult{}, http.StatusUnauthorized, errors.New("refresh_token_revoked")
	}
	if expiresAt.Valid && strings.TrimSpace(expiresAt.String) != "" {
		if expiry, ok := parseAgentTokenTime(expiresAt.String); ok && !expiry.After(request.Now.UTC()) {
			return agentTokenRefreshResult{}, http.StatusUnauthorized, errors.New("refresh_token_expired")
		}
	}

	var deviceGUID, fingerprint, status, hostname sql.NullString
	var tokenVersion sql.NullInt64
	var siteID sql.NullInt64
	err = conn.QueryRowContext(
		ctx,
		`
		SELECT d.guid, d.ssl_key_fingerprint, d.token_version, d.status, d.hostname, ds.site_id
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
		 WHERE UPPER(d.guid)=UPPER($1)
		 LIMIT 1
		`,
		guid,
	).Scan(&deviceGUID, &fingerprint, &tokenVersion, &status, &hostname, &siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return agentTokenRefreshResult{}, http.StatusNotFound, errors.New("device_not_found")
	}
	if err != nil {
		return agentTokenRefreshResult{}, http.StatusInternalServerError, err
	}
	statusNorm := strings.ToLower(strings.TrimSpace(status.String))
	if statusNorm == "revoked" || statusNorm == "decommissioned" {
		return agentTokenRefreshResult{}, http.StatusForbidden, errors.New("device_revoked")
	}

	routeReason := "site_worker_unavailable"
	var route *agentWorkerRoute
	var resultSiteID *int64
	if siteID.Valid {
		value := siteID.Int64
		resultSiteID = &value
		route, err = fetchAgentWorkerRoute(ctx, conn, value)
		if err != nil {
			return agentTokenRefreshResult{}, http.StatusInternalServerError, err
		}
	} else {
		routeReason = "device_site_unassigned"
	}

	nextExpiry := request.Now.UTC().Add(refreshTokenSlidingTTL).Format(time.RFC3339Nano)
	lastUsed := request.Now.UTC().Format(time.RFC3339Nano)
	_, err = conn.ExecContext(
		ctx,
		`
		UPDATE engine.refresh_tokens
		   SET last_used_at=$1,
		       expires_at=$2,
		       dpop_jkt=CASE
		           WHEN $3 THEN $4
		           WHEN $5 THEN NULL
		           ELSE dpop_jkt
		       END
		 WHERE id=$6
		`,
		lastUsed,
		nextExpiry,
		request.HasDPoP,
		request.DPoPJKT,
		!request.HasDPoP && storedJKT.Valid && strings.TrimSpace(storedJKT.String) != "",
		recordID.String,
	)
	if err != nil {
		return agentTokenRefreshResult{}, http.StatusInternalServerError, err
	}

	version := int(tokenVersion.Int64)
	if version <= 0 {
		version = 1
	}
	return agentTokenRefreshResult{
		GUID:         normalizeCanonicalGUID(deviceGUID.String),
		Fingerprint:  strings.ToLower(strings.TrimSpace(fingerprint.String)),
		TokenVersion: version,
		SiteID:       resultSiteID,
		Route:        route,
		Reason:       routeReason,
	}, http.StatusOK, nil
}

func fetchAgentWorkerRoute(ctx context.Context, conn *sql.Conn, siteID int64) (*agentWorkerRoute, error) {
	var route agentWorkerRoute
	err := conn.QueryRowContext(
		ctx,
		`
		SELECT worker_guid, site_id, route_path_prefix, generation
		  FROM engine.job_scheduler_worker_routes
		 WHERE site_id=$1
		   AND status='active'
		 ORDER BY updated_at DESC, generation DESC
		 LIMIT 1
		`,
		siteID,
	).Scan(&route.WorkerGUID, &route.SiteID, &route.RoutePathPrefix, &route.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &route, nil
}

func relationExists(ctx context.Context, conn *sql.Conn, name string) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, nil
	}
	var relation sql.NullString
	if err := conn.QueryRowContext(ctx, `SELECT to_regclass($1)`, name).Scan(&relation); err != nil {
		return false, err
	}
	return relation.Valid && strings.TrimSpace(relation.String) != "", nil
}

func hashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func parseAgentTokenTime(value string) (time.Time, bool) {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999-07:00",
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
	} {
		parsed, err := time.Parse(layout, cleaned)
		if err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func buildAgentRemoteOpsRoutePayload(r *http.Request, siteID *int64, route *agentWorkerRoute, reason string) map[string]any {
	var siteValue any
	if siteID != nil {
		siteValue = *siteID
	}
	if route == nil {
		if strings.TrimSpace(reason) == "" {
			reason = "site_worker_unavailable"
		}
		return map[string]any{
			"available": false,
			"site_id":   siteValue,
			"reason":    reason,
		}
	}
	routeSiteID := route.SiteID
	if routeSiteID == 0 && siteID != nil {
		routeSiteID = *siteID
	}
	workerBase := joinURL(publicBaseURLForRequest(r), route.RoutePathPrefix)
	urls := map[string]any{
		"base":      workerBase,
		"socket_io": joinURL(workerBase, "/socket.io/"),
	}
	return map[string]any{
		"available":         true,
		"site_id":           routeSiteID,
		"worker_guid":       strings.TrimSpace(route.WorkerGUID),
		"route_generation":  route.Generation,
		"route_path_prefix": strings.TrimSpace(route.RoutePathPrefix),
		"base_url":          urls["base"],
		"socket_url":        urls["socket_io"],
		"urls":              urls,
	}
}

func publicBaseURLForRequest(r *http.Request) string {
	for _, name := range []string{"BOREALIS_PUBLIC_BASE_URL", "PUBLIC_BASE_URL"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return strings.TrimRight(value, "/")
		}
	}
	scheme := firstHeaderValue(r, "X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "https"
		}
	}
	host := firstHeaderValue(r, "X-Forwarded-Host")
	if host == "" {
		host = firstHeaderValue(r, "X-Original-Host")
	}
	if host == "" {
		host = r.Host
	}
	return strings.TrimRight(scheme+"://"+host, "/")
}

func absoluteRequestURL(r *http.Request) string {
	return publicBaseURLForRequest(r) + r.URL.RequestURI()
}

func firstHeaderValue(r *http.Request, name string) string {
	value := strings.TrimSpace(r.Header.Get(name))
	if value == "" {
		return ""
	}
	return strings.TrimSpace(strings.Split(value, ",")[0])
}

func joinURL(base string, path string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	path = strings.TrimSpace(path)
	if path == "" {
		return base
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func loadOrCreateAgentJWTSigner() (*agentJWTSigner, error) {
	keyPath, err := agentJWTKeyPath()
	if err != nil {
		return nil, err
	}
	if existing, err := os.ReadFile(keyPath); err == nil && len(existing) > 0 {
		block, _ := pem.Decode(existing)
		if block == nil {
			return nil, fmt.Errorf("invalid PEM in %s", keyPath)
		}
		keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		key, ok := keyAny.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("agent JWT key is %T, expected Ed25519 private key", keyAny)
		}
		return newAgentJWTSignerFromKey(key)
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		return nil, err
	}
	_ = os.Chmod(keyPath, 0o600)
	return newAgentJWTSignerFromKey(privateKey)
}

func agentJWTKeyPath() (string, error) {
	root := strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_AUTH_TOKEN_ROOT"))
	if root == "" {
		engineRoot := strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_ROOT"))
		if engineRoot == "" {
			engineRoot = strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_RUNTIME"))
		}
		if engineRoot == "" {
			engineRoot = "/opt/Borealis/Engine"
		}
		root = filepath.Join(engineRoot, "Services", "api-backend", "secrets", "Auth_Tokens")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(absRoot, agentJWTKeyFilename), nil
}

func newAgentJWTSignerFromKey(privateKey ed25519.PrivateKey) (*agentJWTSigner, error) {
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("invalid Ed25519 public key")
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(der)
	return &agentJWTSigner{
		privateKey: privateKey,
		keyID:      hex.EncodeToString(sum[:])[:16],
	}, nil
}

func (s *agentJWTSigner) issueAccessToken(guid string, fingerprint string, tokenVersion int, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	if tokenVersion <= 0 {
		tokenVersion = 1
	}
	nowUnix := now.Unix()
	claims := map[string]any{
		"sub":                 "device:" + guid,
		"guid":                guid,
		"ssl_key_fingerprint": strings.ToLower(strings.TrimSpace(fingerprint)),
		"token_version":       tokenVersion,
		"iat":                 nowUnix,
		"nbf":                 nowUnix,
		"exp":                 now.Add(ttl).Unix(),
	}
	header := map[string]any{
		"alg": "EdDSA",
		"kid": s.keyID,
		"typ": "JWT",
	}
	return signJWT(header, claims, s.privateKey)
}

func signJWT(header map[string]any, claims map[string]any, key ed25519.PrivateKey) (string, error) {
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimJSON)
	signature := ed25519.Sign(key, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (v *dpopVerifier) verify(method string, htu string, proof string, now time.Time, accessToken string) (string, error) {
	if v == nil {
		return "", errDPoPInvalid
	}
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		return "", errDPoPInvalid
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errDPoPInvalid
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errDPoPInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errDPoPInvalid
	}

	var header struct {
		Alg string `json:"alg"`
		JWK struct {
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			X   string `json:"x"`
		} `json:"jwk"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return "", errDPoPInvalid
	}
	if header.Alg != "EdDSA" || header.JWK.Kty != "OKP" || header.JWK.Crv != "Ed25519" || strings.TrimSpace(header.JWK.X) == "" {
		return "", errDPoPInvalid
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(header.JWK.X)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return "", errDPoPInvalid
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), []byte(parts[0]+"."+parts[1]), signature) {
		return "", errDPoPInvalid
	}

	var claims struct {
		HTM string  `json:"htm"`
		HTU string  `json:"htu"`
		JTI string  `json:"jti"`
		IAT float64 `json:"iat"`
		ATH string  `json:"ath"`
	}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return "", errDPoPInvalid
	}
	if !strings.EqualFold(claims.HTM, method) || claims.HTU != htu || strings.TrimSpace(claims.JTI) == "" || claims.IAT == 0 {
		return "", errDPoPInvalid
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if v.now != nil {
		now = v.now().UTC()
	}
	iat := time.Unix(int64(claims.IAT), 0).UTC()
	if now.Sub(iat) > dpopProofSkew || iat.Sub(now) > dpopProofSkew {
		return "", errDPoPInvalid
	}
	if strings.TrimSpace(claims.ATH) != "" && strings.TrimSpace(accessToken) != "" {
		sum := sha256.Sum256([]byte(accessToken))
		if claims.ATH != base64.RawURLEncoding.EncodeToString(sum[:]) {
			return "", errDPoPInvalid
		}
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.seenJTI == nil {
		v.seenJTI = map[string]time.Time{}
	}
	if expiry, ok := v.seenJTI[claims.JTI]; ok && expiry.After(now) {
		return "", errDPoPReplay
	}
	v.seenJTI[claims.JTI] = now.Add(dpopProofSkew)
	for jti, expiry := range v.seenJTI {
		if !expiry.After(now) {
			delete(v.seenJTI, jti)
		}
	}
	return dpopJWKThumbprint(header.JWK.Crv, header.JWK.Kty, header.JWK.X)
}

func dpopJWKThumbprint(crv string, kty string, x string) (string, error) {
	canonical := `{"crv":` + strconv.Quote(crv) + `,"kty":` + strconv.Quote(kty) + `,"x":` + strconv.Quote(x) + `}`
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}
