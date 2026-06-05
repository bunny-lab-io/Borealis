package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/lib/pq"
)

const (
	passkeyPendingTokenType = "passkey-ceremony"
	passkeyPendingMaxAge    = 5 * time.Minute
	passkeyFlowRegister     = "register"
	passkeyFlowAuthenticate = "authenticate_primary"
)

type passkeyCeremonyStore interface {
	loadPasskeyRegistrationUser(ctx context.Context, username string) (passkeyCeremonyUser, bool, error)
	insertUserPasskey(ctx context.Context, userID int64, label string, transports []string, lookupHMAC string, secretEncrypted string, now int64) (int, error)
	findPasskeyForAssertion(ctx context.Context, credentialID []byte, lookupHMACs []string, candidates []string) (passkeyCeremonyUser, passkeyStoredCredential, bool, error)
	updatePasskeyAssertion(ctx context.Context, passkeyID int64, lookupHMAC string, secretEncrypted string, now int64) (int, error)
}

type passkeyCeremonyUser struct {
	ID                int64
	Username          string
	DisplayName       string
	Role              string
	AuthSource        string
	DirectoryDisabled bool
	Passkeys          []passkeyStoredCredential
}

type passkeyStoredCredential struct {
	ID                   int64
	UserID               int64
	CredentialID         string
	PublicKey            string
	SignCount            int64
	Label                string
	TransportsJSON       string
	AAGUID               string
	CredentialLookupHMAC string
	SecretEncrypted      string
}

type passkeyWebUser struct {
	record      passkeyCeremonyUser
	credentials []webauthnlib.Credential
}

func passkeyRegisterOptionsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !bootstrapLoginReady(w, r, auth) {
			return
		}
		body, ok := readPasskeyJSON(w, r)
		if !ok {
			return
		}
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			writePasskeyAuthFailure(w, err)
			return
		}
		if strings.EqualFold(profile.AuthSource, directoryAuth) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "passkeys_local_users_only"})
			return
		}
		if profile.DirectoryDisabled {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "user_disabled"})
			return
		}
		store, ok := auth.store.(passkeyCeremonyStore)
		if !ok || auth.aegis == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "passkeys_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		userRecord, found, err := store.loadPasskeyRegistrationUser(ctx, profile.Username)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "user_not_found"})
			return
		}
		if strings.EqualFold(userRecord.AuthSource, directoryAuth) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "passkeys_local_users_only"})
			return
		}
		credentials := passkeyWebAuthnCredentials(ctx, auth.aegis, userRecord.Passkeys)
		webUser := passkeyWebUser{record: userRecord, credentials: credentials}
		exclusions := make([]protocol.CredentialDescriptor, 0, len(credentials))
		for _, credential := range credentials {
			exclusions = append(exclusions, credential.Descriptor())
		}
		webAuthn, err := newPasskeyWebAuthn(r)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		creation, sessionData, err := webAuthn.BeginRegistration(webUser, webauthnlib.WithExclusions(exclusions))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": firstText(err.Error(), "passkey_setup_unavailable")})
			return
		}
		requestID, err := signPasskeySession(auth, passkeyFlowRegister, sessionData, map[string]any{
			"username": userRecord.Username,
			"role":     firstText(userRecord.Role, defaultUserRole),
			"label":    cleanText(body["label"]),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "passkey_session_failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"request_id": requestID,
			"options":    creation.Response,
		})
	}
}

func passkeyRegisterVerifyHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !bootstrapLoginReady(w, r, auth) {
			return
		}
		raw, body, ok := readPasskeyJSONRaw(w, r)
		if !ok {
			return
		}
		pending, sessionData, ok := verifyPasskeySession(w, auth, body, passkeyFlowRegister)
		if !ok {
			return
		}
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			writePasskeyAuthFailure(w, err)
			return
		}
		username := cleanText(pending["username"])
		if username == "" || !strings.EqualFold(profile.Username, username) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_session"})
			return
		}
		if body, status, blocked := protectedSecretMutationBlock(r.Context(), auth); blocked {
			writeJSON(w, status, body)
			return
		}
		store, ok := auth.store.(passkeyCeremonyStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "passkeys_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		userRecord, found, err := store.loadPasskeyRegistrationUser(ctx, username)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "user_not_found"})
			return
		}
		if strings.EqualFold(userRecord.AuthSource, directoryAuth) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "passkeys_local_users_only"})
			return
		}
		webAuthn, err := newPasskeyWebAuthn(r)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		webUser := passkeyWebUser{record: userRecord, credentials: passkeyWebAuthnCredentials(ctx, auth.aegis, userRecord.Passkeys)}
		credentialRequest, err := passkeyCredentialHTTPRequest(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_passkey"})
			return
		}
		credential, err := webAuthn.FinishRegistration(webUser, *sessionData, credentialRequest)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		credentialID := normalizeWebAuthnStorageValue(credential.ID)
		publicKey := normalizeWebAuthnStorageValue(credential.PublicKey)
		if credentialID == "" || publicKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_passkey"})
			return
		}
		aaguid := normalizeWebAuthnStorageValue(credential.Authenticator.AAGUID)
		bundle, err := passkeySecretBundle(credentialID, publicKey, int64(credential.Authenticator.SignCount), aaguid)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		encrypted, err := auth.aegis.encryptSecretText(ctx, bundle)
		if err != nil {
			writeJSON(w, protectedSecretErrorStatus(err), protectedSecretErrorBody(err))
			return
		}
		label := firstText(cleanText(body["label"]), cleanText(pending["label"]), "Passkey")
		transports := passkeyTransportStrings(credential.Transport)
		count, err := store.insertUserPasskey(ctx, userRecord.ID, label, transports, passkeyLookupHMAC(auth, credentialID), encrypted, time.Now().Unix())
		if err != nil {
			if isUniqueViolation(err) {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "passkey_already_registered"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "ok",
			"username":      userRecord.Username,
			"passkey_count": count,
		})
	}
}

func passkeyAuthenticateOptionsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !bootstrapLoginReady(w, r, auth) {
			return
		}
		webAuthn, err := newPasskeyWebAuthn(r)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		assertion, sessionData, err := webAuthn.BeginDiscoverableLogin(webauthnlib.WithUserVerification(protocol.VerificationRequired))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": firstText(err.Error(), "passkey_auth_unavailable")})
			return
		}
		requestID, err := signPasskeySession(auth, passkeyFlowAuthenticate, sessionData, nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "passkey_session_failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"request_id": requestID,
			"options":    assertion.Response,
		})
	}
}

func passkeyAuthenticateVerifyHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		if !bootstrapLoginReady(w, r, auth) {
			return
		}
		raw, body, ok := readPasskeyJSONRaw(w, r)
		if !ok {
			return
		}
		_, sessionData, ok := verifyPasskeySession(w, auth, body, passkeyFlowAuthenticate)
		if !ok {
			return
		}
		if auth.aegis == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "passkeys_unavailable"})
			return
		}
		store, ok := auth.store.(passkeyCeremonyStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "passkeys_unavailable"})
			return
		}
		loginStore, ok := auth.store.(authLoginStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_store_unavailable"})
			return
		}
		webAuthn, err := newPasskeyWebAuthn(r)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		credentialRequest, err := passkeyCredentialHTTPRequest(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_passkey"})
			return
		}
		var verifiedCredential *webauthnlib.Credential
		var verifiedRecord passkeyStoredCredential
		var verifiedUser passkeyCeremonyUser
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		verifiedCredential, err = webAuthn.FinishDiscoverableLogin(func(rawID []byte, userHandle []byte) (webauthnlib.User, error) {
			candidates := passkeyCredentialCandidates(rawID)
			lookupHMACs := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				if value := passkeyLookupHMAC(auth, candidate); value != "" {
					lookupHMACs = append(lookupHMACs, value)
				}
			}
			userRecord, stored, found, err := store.findPasskeyForAssertion(ctx, rawID, lookupHMACs, candidates)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, errors.New("passkey_not_configured")
			}
			if strings.EqualFold(userRecord.AuthSource, directoryAuth) {
				return nil, errors.New("passkeys_local_users_only")
			}
			if userRecord.DirectoryDisabled {
				return nil, errors.New("user_disabled")
			}
			credential, err := passkeyStoredWebAuthnCredential(ctx, auth.aegis, stored)
			if err != nil {
				return nil, err
			}
			verifiedUser = userRecord
			verifiedRecord = stored
			return passkeyWebUser{record: userRecord, credentials: []webauthnlib.Credential{credential}}, nil
		}, *sessionData, credentialRequest)
		if err != nil {
			writeJSON(w, passkeyAssertionErrorStatus(err), map[string]any{"error": passkeyAssertionErrorKey(err)})
			return
		}
		credentialID := normalizeWebAuthnStorageValue(verifiedCredential.ID)
		publicKey := normalizeWebAuthnStorageValue(verifiedCredential.PublicKey)
		if credentialID == "" || publicKey == "" || verifiedRecord.ID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_passkey"})
			return
		}
		aaguid := normalizeWebAuthnStorageValue(verifiedCredential.Authenticator.AAGUID)
		bundle, err := passkeySecretBundle(credentialID, publicKey, int64(verifiedCredential.Authenticator.SignCount), aaguid)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		encrypted, err := auth.aegis.encryptSecretText(ctx, bundle)
		if err != nil {
			writeJSON(w, protectedSecretErrorStatus(err), protectedSecretErrorBody(err))
			return
		}
		if _, err := store.updatePasskeyAssertion(ctx, verifiedRecord.ID, passkeyLookupHMAC(auth, credentialID), encrypted, time.Now().Unix()); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		finalizeLogin(w, r, auth, loginStore, verifiedUser.Username, firstText(verifiedUser.Role, defaultUserRole))
	}
}

func (s *postgresOperatorStore) loadPasskeyRegistrationUser(ctx context.Context, username string) (passkeyCeremonyUser, bool, error) {
	user, found, err := s.loadPasskeyUserByUsername(ctx, username)
	if err != nil || !found {
		return user, found, err
	}
	passkeys, err := s.loadPasskeysForUser(ctx, user.ID)
	if err != nil {
		return passkeyCeremonyUser{}, false, err
	}
	user.Passkeys = passkeys
	return user, true, nil
}

func (s *postgresOperatorStore) insertUserPasskey(ctx context.Context, userID int64, label string, transports []string, lookupHMAC string, secretEncrypted string, now int64) (int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	transportsJSON, err := json.Marshal(transports)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO engine.user_passkeys(
			user_id,
			credential_id,
			public_key,
			sign_count,
			label,
			transports_json,
			aaguid,
			created_at,
			last_used_at,
			credential_lookup_hmac,
			secret_encrypted
		) VALUES ($1,'','',0,$2,$3,'',$4,$5,$6,$7)
	`, userID, firstText(strings.TrimSpace(label), "Passkey"), string(transportsJSON), now, now, lookupHMAC, secretEncrypted)
	if err != nil {
		return 0, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.user_passkeys WHERE user_id=$1`, userID).Scan(&count); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return count, nil
}

func (s *postgresOperatorStore) findPasskeyForAssertion(ctx context.Context, _ []byte, lookupHMACs []string, candidates []string) (passkeyCeremonyUser, passkeyStoredCredential, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return passkeyCeremonyUser{}, passkeyStoredCredential{}, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT
			up.id,
			up.user_id,
			COALESCE(up.credential_id, ''),
			COALESCE(up.public_key, ''),
			COALESCE(up.sign_count, 0),
			COALESCE(up.label, ''),
			COALESCE(up.transports_json, '[]'),
			COALESCE(up.aaguid, ''),
			COALESCE(up.credential_lookup_hmac, ''),
			COALESCE(up.secret_encrypted, ''),
			u.id,
			u.username,
			COALESCE(u.display_name, u.username),
			COALESCE(u.role, $1),
			COALESCE(u.auth_source, 'local'),
			COALESCE(u.directory_disabled, 0)
		  FROM engine.user_passkeys AS up
		  JOIN engine.users AS u ON u.id = up.user_id
		 WHERE up.credential_lookup_hmac = ANY($2)
		    OR up.credential_id = ANY($3)
		ORDER BY up.id ASC
	`, defaultUserRole, pq.Array(lookupHMACs), pq.Array(candidates))
	if err != nil {
		return passkeyCeremonyUser{}, passkeyStoredCredential{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		user, stored, err := scanPasskeyAssertionRow(rows)
		if err != nil {
			return passkeyCeremonyUser{}, passkeyStoredCredential{}, false, err
		}
		return user, stored, true, nil
	}
	if err := rows.Err(); err != nil {
		return passkeyCeremonyUser{}, passkeyStoredCredential{}, false, err
	}
	return passkeyCeremonyUser{}, passkeyStoredCredential{}, false, nil
}

func (s *postgresOperatorStore) updatePasskeyAssertion(ctx context.Context, passkeyID int64, lookupHMAC string, secretEncrypted string, now int64) (int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	var userID int64
	err = conn.QueryRowContext(ctx, `
		UPDATE engine.user_passkeys
		   SET credential_id='',
		       public_key='',
		       sign_count=0,
		       aaguid='',
		       credential_lookup_hmac=$1,
		       secret_encrypted=$2,
		       last_used_at=$3
		 WHERE id=$4
	 RETURNING user_id
	`, lookupHMAC, secretEncrypted, now, passkeyID).Scan(&userID)
	if err != nil {
		return 0, err
	}
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.user_passkeys WHERE user_id=$1`, userID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *postgresOperatorStore) loadPasskeyUserByUsername(ctx context.Context, username string) (passkeyCeremonyUser, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return passkeyCeremonyUser{}, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	var user passkeyCeremonyUser
	var directoryDisabled int
	err = conn.QueryRowContext(ctx, `
		SELECT
			id,
			username,
			COALESCE(display_name, username),
			COALESCE(role, $1),
			COALESCE(auth_source, 'local'),
			COALESCE(directory_disabled, 0)
		  FROM engine.users
		 WHERE LOWER(username)=LOWER($2)
		 LIMIT 1
	`, defaultUserRole, username).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &user.AuthSource, &directoryDisabled)
	if errors.Is(err, sql.ErrNoRows) {
		return passkeyCeremonyUser{}, false, nil
	}
	if err != nil {
		return passkeyCeremonyUser{}, false, err
	}
	user.DirectoryDisabled = directoryDisabled != 0
	user.Role = firstText(user.Role, defaultUserRole)
	user.AuthSource = strings.ToLower(firstText(user.AuthSource, "local"))
	return user, true, nil
}

func (s *postgresOperatorStore) loadPasskeysForUser(ctx context.Context, userID int64) ([]passkeyStoredCredential, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT
			id,
			user_id,
			COALESCE(credential_id, ''),
			COALESCE(public_key, ''),
			COALESCE(sign_count, 0),
			COALESCE(label, ''),
			COALESCE(transports_json, '[]'),
			COALESCE(aaguid, ''),
			COALESCE(credential_lookup_hmac, ''),
			COALESCE(secret_encrypted, '')
		  FROM engine.user_passkeys
		 WHERE user_id=$1
		 ORDER BY created_at ASC, id ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	passkeys := []passkeyStoredCredential{}
	for rows.Next() {
		var stored passkeyStoredCredential
		if err := rows.Scan(&stored.ID, &stored.UserID, &stored.CredentialID, &stored.PublicKey, &stored.SignCount, &stored.Label, &stored.TransportsJSON, &stored.AAGUID, &stored.CredentialLookupHMAC, &stored.SecretEncrypted); err != nil {
			return nil, err
		}
		passkeys = append(passkeys, stored)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return passkeys, nil
}

func scanPasskeyAssertionRow(scanner interface{ Scan(dest ...any) error }) (passkeyCeremonyUser, passkeyStoredCredential, error) {
	var stored passkeyStoredCredential
	var user passkeyCeremonyUser
	var directoryDisabled int
	err := scanner.Scan(
		&stored.ID,
		&stored.UserID,
		&stored.CredentialID,
		&stored.PublicKey,
		&stored.SignCount,
		&stored.Label,
		&stored.TransportsJSON,
		&stored.AAGUID,
		&stored.CredentialLookupHMAC,
		&stored.SecretEncrypted,
		&user.ID,
		&user.Username,
		&user.DisplayName,
		&user.Role,
		&user.AuthSource,
		&directoryDisabled,
	)
	user.DirectoryDisabled = directoryDisabled != 0
	user.Role = firstText(user.Role, defaultUserRole)
	user.AuthSource = strings.ToLower(firstText(user.AuthSource, "local"))
	return user, stored, err
}

func (u passkeyWebUser) WebAuthnID() []byte {
	sum := sha256.Sum256([]byte(strconvFormatPasskeyUser(u.record.ID, u.record.Username)))
	return sum[:32]
}

func (u passkeyWebUser) WebAuthnName() string {
	return u.record.Username
}

func (u passkeyWebUser) WebAuthnDisplayName() string {
	return firstText(u.record.DisplayName, u.record.Username)
}

func (u passkeyWebUser) WebAuthnIcon() string {
	return ""
}

func (u passkeyWebUser) WebAuthnCredentials() []webauthnlib.Credential {
	return u.credentials
}

func strconvFormatPasskeyUser(userID int64, username string) string {
	return strconv.FormatInt(userID, 10) + ":" + strings.ToLower(strings.TrimSpace(username))
}

func newPasskeyWebAuthn(r *http.Request) (*webauthnlib.WebAuthn, error) {
	origin := passkeyOrigin(r)
	parsed, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	rpID := parsed.Hostname()
	if rpID == "" {
		rpID = strings.SplitN(strings.TrimSpace(r.Host), ":", 2)[0]
	}
	return webauthnlib.New(&webauthnlib.Config{
		RPID:          rpID,
		RPDisplayName: firstText(strings.TrimSpace(os.Getenv("BOREALIS_PASSKEY_RP_NAME")), "Borealis"),
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts: webauthnlib.TimeoutsConfig{
			Login:        webauthnlib.TimeoutConfig{Enforce: true, Timeout: passkeyPendingMaxAge, TimeoutUVD: passkeyPendingMaxAge},
			Registration: webauthnlib.TimeoutConfig{Enforce: true, Timeout: passkeyPendingMaxAge, TimeoutUVD: passkeyPendingMaxAge},
		},
	})
}

func passkeyOrigin(r *http.Request) string {
	base := configuredPublicBaseURL(r)
	if !strings.Contains(base, "://") {
		base = "https://" + strings.TrimLeft(base, "/")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" {
		host := strings.TrimSpace(r.Host)
		if host == "" {
			host = "localhost"
		}
		return "https://" + host
	}
	scheme := firstText(parsed.Scheme, "https")
	host := parsed.Hostname()
	if port := parsed.Port(); port != "" && port != "80" && port != "443" {
		host += ":" + port
	}
	return strings.TrimRight(scheme+"://"+host, "/")
}

func signPasskeySession(auth *authService, flow string, session *webauthnlib.SessionData, fields map[string]any) (string, error) {
	rawSession, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"typ":     passkeyPendingTokenType,
		"flow":    flow,
		"session": string(rawSession),
	}
	for key, value := range fields {
		payload[key] = value
	}
	return auth.verifier.signPayload(payload)
}

func verifyPasskeySession(w http.ResponseWriter, auth *authService, body map[string]any, flow string) (map[string]any, *webauthnlib.SessionData, bool) {
	requestID := cleanText(body["request_id"])
	if requestID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "passkey_pending"})
		return nil, nil, false
	}
	pending, err := auth.verifier.signedPayload(requestID, passkeyPendingMaxAge)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": passkeyPendingErrorKey(err)})
		return nil, nil, false
	}
	if cleanText(pending["typ"]) != passkeyPendingTokenType || cleanText(pending["flow"]) != flow {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_session"})
		return nil, nil, false
	}
	var sessionData webauthnlib.SessionData
	if err := json.Unmarshal([]byte(cleanText(pending["session"])), &sessionData); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_session"})
		return nil, nil, false
	}
	return pending, &sessionData, true
}

func passkeyPendingErrorKey(err error) string {
	if errors.Is(err, errExpiredToken) {
		return "expired"
	}
	return "invalid_session"
}

func passkeyCredentialHTTPRequest(raw []byte) (*http.Request, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	credential, ok := payload["credential"]
	if !ok {
		return nil, errors.New("missing credential")
	}
	credentialRaw, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(credentialRaw))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	return request, nil
}

func passkeyWebAuthnCredentials(ctx context.Context, secret authSecretService, stored []passkeyStoredCredential) []webauthnlib.Credential {
	credentials := make([]webauthnlib.Credential, 0, len(stored))
	for _, item := range stored {
		credential, err := passkeyStoredWebAuthnCredential(ctx, secret, item)
		if err == nil && len(credential.ID) > 0 && len(credential.PublicKey) > 0 {
			credentials = append(credentials, credential)
		}
	}
	return credentials
}

func passkeyStoredWebAuthnCredential(ctx context.Context, secret authSecretService, stored passkeyStoredCredential) (webauthnlib.Credential, error) {
	credentialID := normalizeWebAuthnStorageValue(stored.CredentialID)
	publicKey := normalizeWebAuthnStorageValue(stored.PublicKey)
	signCount := stored.SignCount
	aaguid := normalizeWebAuthnStorageValue(stored.AAGUID)
	if strings.TrimSpace(stored.SecretEncrypted) != "" {
		if secret == nil {
			return webauthnlib.Credential{}, errAegisLocked
		}
		plain, err := secret.decryptSecretText(ctx, stored.SecretEncrypted)
		if err != nil {
			return webauthnlib.Credential{}, err
		}
		bundle := parsePasskeySecretBundle(plain)
		credentialID = normalizeWebAuthnStorageValue(bundle["credential_id"])
		publicKey = normalizeWebAuthnStorageValue(bundle["public_key"])
		signCount = parseInt64Any(bundle["sign_count"])
		aaguid = normalizeWebAuthnStorageValue(bundle["aaguid"])
	}
	idBytes, err := decodeWebAuthnStorageValue(credentialID)
	if err != nil {
		return webauthnlib.Credential{}, err
	}
	publicKeyBytes, err := decodeWebAuthnStorageValue(publicKey)
	if err != nil {
		return webauthnlib.Credential{}, err
	}
	aaguidBytes, _ := decodeWebAuthnStorageValue(aaguid)
	return webauthnlib.Credential{
		ID:        idBytes,
		PublicKey: publicKeyBytes,
		Transport: passkeyProtocolTransports(stored.TransportsJSON),
		Authenticator: webauthnlib.Authenticator{
			AAGUID:    aaguidBytes,
			SignCount: uint32(signCount),
		},
	}, nil
}

func decodeWebAuthnStorageValue(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.URLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return []byte(value), nil
}

func passkeyCredentialCandidates(rawID []byte) []string {
	seen := map[string]struct{}{}
	candidates := []string{}
	add := func(value string) {
		value = normalizeWebAuthnStorageValue(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		candidates = append(candidates, value)
	}
	encoded := normalizeWebAuthnStorageValue(rawID)
	add(encoded)
	if len(rawID) > 0 {
		add(string(rawID))
	}
	return candidates
}

func passkeyLookupHMAC(auth *authService, credentialID string) string {
	credentialID = normalizeWebAuthnStorageValue(credentialID)
	if credentialID == "" || auth == nil || auth.verifier == nil || len(auth.verifier.secret) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, auth.verifier.secret)
	_, _ = mac.Write([]byte(credentialID))
	return base64Hex(mac.Sum(nil))
}

func base64Hex(raw []byte) string {
	const alphabet = "0123456789abcdef"
	out := make([]byte, len(raw)*2)
	for i, value := range raw {
		out[i*2] = alphabet[value>>4]
		out[i*2+1] = alphabet[value&0x0f]
	}
	return string(out)
}

func passkeyTransportStrings(values []protocol.AuthenticatorTransport) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text := strings.TrimSpace(string(value))
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

func passkeyProtocolTransports(raw string) []protocol.AuthenticatorTransport {
	var values []string
	_ = json.Unmarshal([]byte(firstText(raw, "[]")), &values)
	out := make([]protocol.AuthenticatorTransport, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, protocol.AuthenticatorTransport(value))
		}
	}
	return out
}

func readPasskeyJSON(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	_, body, ok := readPasskeyJSONRaw(w, r)
	return body, ok
}

func readPasskeyJSONRaw(w http.ResponseWriter, r *http.Request) ([]byte, map[string]any, bool) {
	if r.Body == nil {
		return nil, map[string]any{}, true
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return nil, nil, false
	}
	if len(raw) == 0 {
		return raw, map[string]any{}, true
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return nil, nil, false
	}
	if body == nil {
		body = map[string]any{}
	}
	return raw, body, true
}

func bootstrapLoginReady(w http.ResponseWriter, r *http.Request, auth *authService) bool {
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	state, err := currentBootstrapState(ctx, auth)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "bootstrap_state_unavailable", "message": err.Error()})
		return false
	}
	if cleanText(state["phase"]) != bootstrapPhaseLoginRequired {
		writeBootstrapRequired(w, state)
		return false
	}
	return true
}

func writePasskeyAuthFailure(w http.ResponseWriter, err error) {
	if isUnauthorizedAuthError(err) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
}

func passkeyAssertionErrorKey(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "passkey_not_configured"):
		return "passkey_not_configured"
	case strings.Contains(text, "passkeys_local_users_only"):
		return "passkeys_local_users_only"
	case strings.Contains(text, "user_disabled"):
		return "user_disabled"
	default:
		return firstText(text, "invalid_passkey")
	}
}

func passkeyAssertionErrorStatus(err error) int {
	switch passkeyAssertionErrorKey(err) {
	case "passkey_not_configured":
		return http.StatusNotFound
	case "passkeys_local_users_only", "user_disabled":
		return http.StatusForbidden
	default:
		return http.StatusBadRequest
	}
}
