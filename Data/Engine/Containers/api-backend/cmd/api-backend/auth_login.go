package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	mfaPendingTokenType = "mfa-pending"
	mfaPendingMaxAge    = 5 * time.Minute
)

type bootstrapStateReader interface {
	bootstrapState(ctx context.Context) (map[string]any, error)
}

type authLoginStore interface {
	loadLoginRow(ctx context.Context, username string) (authLoginRow, bool, error)
	updateLastLogin(ctx context.Context, username string, now int64) error
	updateUserPasswordSecret(ctx context.Context, username string, encryptedSecret string, now int64) error
	updateUserMFASecret(ctx context.Context, username string, encryptedSecret string, now int64) error
}

type authLoginRow struct {
	ID                int64
	Username          string
	DisplayName       string
	PasswordSecret    string
	Role              string
	MFASecret         string
	MFADisabled       bool
	AuthResetRequired bool
	AuthSource        string
	DirectoryDisabled bool
}

func bootstrapStateHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		state, err := currentBootstrapState(r.Context(), auth)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":   "bootstrap_state_unavailable",
				"message": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, publicBootstrapState(state))
	}
}

func bootstrapAegisSetupHandler(auth *authService) http.HandlerFunc {
	return bootstrapAegisLifecycleHandler(auth, bootstrapPhaseAegisSetupRequired, func(ctx context.Context, cipherText string) (map[string]any, error) {
		return auth.aegis.setupWithCipher(ctx, cipherText)
	})
}

func bootstrapAegisUnlockHandler(auth *authService) http.HandlerFunc {
	return bootstrapAegisLifecycleHandler(auth, bootstrapPhaseAegisUnlockRequired, func(ctx context.Context, cipherText string) (map[string]any, error) {
		return auth.aegis.unlockWithCipher(ctx, cipherText)
	})
}

func bootstrapAegisLifecycleHandler(
	auth *authService,
	requiredPhase string,
	call func(context.Context, string) (map[string]any, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		body, ok := readAuthJSON(w, r)
		if !ok {
			return
		}
		cipherText := cleanText(body["cipher"])
		if cipherText == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":   "invalid_request",
				"message": "Aegis Cipher is required.",
			})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		state, err := currentBootstrapState(ctx, auth)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":   "bootstrap_state_unavailable",
				"message": err.Error(),
			})
			return
		}
		if cleanText(state["phase"]) != requiredPhase {
			payload := publicBootstrapState(state)
			payload["error"] = "invalid_phase"
			writeJSON(w, http.StatusConflict, payload)
			return
		}
		if auth.aegis == nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "aegis_store_unavailable"})
			return
		}
		_, err = call(ctx, cipherText)
		if err != nil {
			payload, status, _ := aegisErrorPayload(err)
			writeJSON(w, status, payload)
			return
		}
		nextState, err := currentBootstrapState(ctx, auth)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":   "bootstrap_state_unavailable",
				"message": err.Error(),
			})
			return
		}
		clearAuthCookies(w)
		payload := publicBootstrapState(nextState)
		payload["status"] = "ok"
		writeJSON(w, http.StatusOK, payload)
	}
}

func authLoginHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		_, body, ok := readAuthJSONRaw(w, r)
		if !ok {
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		state, err := currentBootstrapState(ctx, auth)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":   "bootstrap_state_unavailable",
				"message": err.Error(),
			})
			return
		}
		if cleanText(state["phase"]) != bootstrapPhaseLoginRequired {
			writeBootstrapRequired(w, state)
			return
		}

		username := cleanText(body["username"])
		password := cleanText(body["password"])
		credential, credentialOK := passwordCredentialFromBody(body, "password", "password_sha512")
		if username == "" || !credentialOK {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing credentials"})
			return
		}
		store, ok := auth.store.(authLoginStore)
		if !ok || auth.aegis == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "auth_store_unavailable"})
			return
		}
		row, found, err := store.loadLoginRow(ctx, username)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":   "auth_store_unavailable",
				"message": err.Error(),
			})
			return
		}
		if !found {
			directoryLoginAndRespond(w, r, auth, store, username, password)
			return
		}
		if strings.EqualFold(row.AuthSource, directoryAuth) {
			if row.DirectoryDisabled {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "directory_user_disabled"})
				return
			}
			directoryLoginAndRespond(w, r, auth, store, username, password)
			return
		}
		if row.DirectoryDisabled {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "directory_user_disabled"})
			return
		}
		if row.AuthResetRequired {
			writeJSON(w, http.StatusLocked, map[string]any{"error": "auth_reset_required"})
			return
		}
		storedSecret, err := auth.aegis.decryptSecretText(ctx, row.PasswordSecret)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": firstText(err.Error(), "auth_secret_unavailable")})
			return
		}
		existingSecret, err := auth.aegis.decryptSecretText(ctx, row.MFASecret)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": firstText(err.Error(), "auth_secret_unavailable")})
			return
		}
		passwordOK, needsUpgrade, err := verifyPasswordSecret(storedSecret, credential)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "auth_secret_unavailable"})
			return
		}
		if !passwordOK {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid username or password"})
			return
		}
		passwordVerifierUpgrade := ""
		if needsUpgrade {
			passwordVerifierUpgrade, err = newPasswordVerifierFromCredential(credential)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "auth_secret_unavailable"})
				return
			}
		}
		beginMFAOrFinalize(w, r, auth, store, row.Username, firstText(row.Role, defaultUserRole), strings.TrimSpace(existingSecret), row.MFADisabled, passwordVerifierUpgrade)
	}
}

func authMFAVerifyHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		_, body, ok := readAuthJSONRaw(w, r)
		if !ok {
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		state, err := currentBootstrapState(ctx, auth)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":   "bootstrap_state_unavailable",
				"message": err.Error(),
			})
			return
		}
		if cleanText(state["phase"]) != bootstrapPhaseLoginRequired {
			writeBootstrapRequired(w, state)
			return
		}
		pendingToken := cleanText(body["pending_token"])
		code := onlyDigits(cleanText(body["code"]))
		if pendingToken == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "mfa_pending"})
			return
		}
		pending, err := auth.verifier.signedPayload(pendingToken, mfaPendingMaxAge)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_session"})
			return
		}
		if cleanText(pending["typ"]) != mfaPendingTokenType {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_session"})
			return
		}
		if len(code) < 6 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_code"})
			return
		}
		store, ok := auth.store.(authLoginStore)
		if !ok || auth.aegis == nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_store_unavailable"})
			return
		}
		username := cleanText(pending["u"])
		role := firstText(cleanText(pending["r"]), defaultUserRole)
		stage := firstText(cleanText(pending["stage"]), "verify")
		if username == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_session"})
			return
		}

		switch stage {
		case "setup":
			secret := cleanText(pending["secret"])
			if secret == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_session"})
				return
			}
			if !verifyTOTPCode(secret, code, time.Now()) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_code"})
				return
			}
			encryptedSecret, err := auth.aegis.encryptSecretText(ctx, secret)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": firstText(err.Error(), "mfa_secret_store_failed")})
				return
			}
			if err := store.updateUserMFASecret(ctx, username, encryptedSecret, time.Now().Unix()); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]any{
					"error":   "auth_store_unavailable",
					"message": err.Error(),
				})
				return
			}
		case "verify":
			row, found, err := store.loadLoginRow(ctx, username)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]any{
					"error":   "auth_store_unavailable",
					"message": err.Error(),
				})
				return
			}
			if !found || row.DirectoryDisabled {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "user_disabled"})
				return
			}
			secret, err := auth.aegis.decryptSecretText(ctx, row.MFASecret)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": firstText(err.Error(), "mfa_secret_unavailable")})
				return
			}
			if strings.TrimSpace(secret) == "" {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "mfa_not_configured"})
				return
			}
			if !verifyTOTPCode(secret, code, time.Now()) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_code"})
				return
			}
			role = firstText(row.Role, role, defaultUserRole)
		default:
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_session"})
			return
		}

		if passwordVerifierUpgrade := cleanText(pending["password_verifier"]); passwordVerifierUpgrade != "" {
			if err := upgradeLoginPasswordIfNeeded(ctx, auth, store, username, passwordVerifierUpgrade); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_store_unavailable", "message": err.Error()})
				return
			}
		}

		finalizeLogin(w, r, auth, store, username, role)
	}
}

func (s *postgresOperatorStore) loadLoginRow(ctx context.Context, username string) (authLoginRow, bool, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return authLoginRow{}, false, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return authLoginRow{}, false, err
	}
	defer conn.Close()
	var row authLoginRow
	var mfaDisabled int
	var authResetRequired int
	var directoryDisabled int
	err = conn.QueryRowContext(ctx, `
		SELECT
			id,
			username,
			COALESCE(display_name, username),
			COALESCE(password_sha512, ''),
			COALESCE(role, $1),
			COALESCE(mfa_secret, ''),
			COALESCE(mfa_disabled, 0),
			COALESCE(auth_reset_required, 0),
			COALESCE(auth_source, 'local'),
			COALESCE(directory_disabled, 0)
		  FROM engine.users
		 WHERE LOWER(username)=LOWER($2)
		 LIMIT 1
	`, defaultUserRole, username).Scan(
		&row.ID,
		&row.Username,
		&row.DisplayName,
		&row.PasswordSecret,
		&row.Role,
		&row.MFASecret,
		&mfaDisabled,
		&authResetRequired,
		&row.AuthSource,
		&directoryDisabled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return authLoginRow{}, false, nil
	}
	if err != nil {
		return authLoginRow{}, false, err
	}
	row.MFADisabled = mfaDisabled != 0
	row.AuthResetRequired = authResetRequired != 0
	row.DirectoryDisabled = directoryDisabled != 0
	row.AuthSource = strings.ToLower(firstText(strings.TrimSpace(row.AuthSource), "local"))
	row.Role = firstText(row.Role, defaultUserRole)
	row.DisplayName = firstText(row.DisplayName, row.Username)
	return row, true, nil
}

func (s *postgresOperatorStore) updateLastLogin(ctx context.Context, username string, now int64) error {
	if strings.TrimSpace(username) == "" {
		return nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, "UPDATE engine.users SET last_login=$1, updated_at=$1 WHERE LOWER(username)=LOWER($2)", now, username)
	return err
}

func (s *postgresOperatorStore) updateUserPasswordSecret(ctx context.Context, username string, encryptedSecret string, now int64) error {
	if strings.TrimSpace(username) == "" {
		return errors.New("username required")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	result, err := conn.ExecContext(ctx, `
		UPDATE engine.users
		   SET password_sha512=$1,
		       updated_at=$2
		 WHERE LOWER(username)=LOWER($3)
		   AND COALESCE(auth_source, 'local')='local'
	`, encryptedSecret, now, username)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected <= 0 {
		return errOperatorNotFound
	}
	return nil
}

func (s *postgresOperatorStore) updateUserMFASecret(ctx context.Context, username string, encryptedSecret string, now int64) error {
	if strings.TrimSpace(username) == "" {
		return errors.New("username required")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	result, err := conn.ExecContext(ctx, `
		UPDATE engine.users
		   SET mfa_enabled=1,
		       mfa_disabled=0,
		       mfa_secret=$1,
		       updated_at=$2
		 WHERE LOWER(username)=LOWER($3)
	`, encryptedSecret, now, username)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected <= 0 {
		return errOperatorNotFound
	}
	return nil
}

func beginMFAOrFinalize(w http.ResponseWriter, r *http.Request, auth *authService, store authLoginStore, username string, role string, existingSecret string, mfaDisabled bool, passwordVerifierUpgrade string) {
	if mfaDisabled {
		if err := upgradeLoginPasswordIfNeeded(r.Context(), auth, store, username, passwordVerifierUpgrade); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_store_unavailable", "message": err.Error()})
			return
		}
		finalizeLogin(w, r, auth, store, username, role)
		return
	}
	stage := "verify"
	secret := ""
	if strings.TrimSpace(existingSecret) == "" {
		stage = "setup"
		generated, err := randomBase32Secret()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "mfa_setup_unavailable"})
			return
		}
		secret = generated
	}
	pending := map[string]any{
		"typ":   mfaPendingTokenType,
		"u":     username,
		"r":     firstText(role, defaultUserRole),
		"stage": stage,
	}
	if stage == "setup" {
		pending["secret"] = secret
	}
	if strings.TrimSpace(passwordVerifierUpgrade) != "" {
		pending["password_verifier"] = passwordVerifierUpgrade
	}
	token, err := auth.verifier.signPayload(pending)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "mfa_session_failed"})
		return
	}
	payload := map[string]any{
		"status":            "mfa_required",
		"stage":             stage,
		"pending_token":     token,
		"username":          username,
		"role":              firstText(role, defaultUserRole),
		"preferred_method":  "totp",
		"available_methods": []string{"totp"},
	}
	if stage == "setup" {
		otpauth := totpProvisioningURI(secret, username)
		payload["secret"] = secret
		payload["otpauth_url"] = otpauth
		payload["qr_image"] = nil
	}
	writeJSON(w, http.StatusOK, payload)
}

func finalizeLogin(w http.ResponseWriter, r *http.Request, auth *authService, store authLoginStore, username string, role string) {
	profile, err := auth.store.lookupOperator(r.Context(), username, role)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "user_disabled"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "auth_store_unavailable",
			"message": err.Error(),
		})
		return
	}
	role = firstText(profile.Role, role, defaultUserRole)
	username = firstText(profile.Username, username)
	_ = store.updateLastLogin(r.Context(), username, time.Now().Unix())
	token, err := auth.verifier.signPayload(map[string]any{"u": username, "r": role, "ts": time.Now().Unix()})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "token_issue_failed"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"username":    username,
		"role":        role,
		"auth_source": firstText(profile.AuthSource, "local"),
	})
}

func currentBootstrapState(ctx context.Context, auth *authService) (map[string]any, error) {
	if auth == nil || auth.bootstrapGate == nil {
		return nil, errors.New("bootstrap gate unavailable")
	}
	provider, ok := auth.bootstrapGate.(bootstrapStateReader)
	if !ok || provider == nil {
		return nil, errors.New("bootstrap state provider unavailable")
	}
	return provider.bootstrapState(ctx)
}

func publicBootstrapState(state map[string]any) map[string]any {
	payload := map[string]any{
		"phase":      firstText(cleanText(state["phase"]), bootstrapPhaseLoginRequired),
		"configured": boolFromAny(state["configured"]),
		"locked":     boolFromAny(state["locked"]),
	}
	return payload
}

func writeBootstrapRequired(w http.ResponseWriter, state map[string]any) {
	payload := publicBootstrapState(state)
	payload["error"] = "bootstrap_required"
	writeJSON(w, http.StatusLocked, payload)
}

func readAuthJSON(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	_, body, ok := readAuthJSONRaw(w, r)
	return body, ok
}

func readAuthJSONRaw(w http.ResponseWriter, r *http.Request) ([]byte, map[string]any, bool) {
	if r.Body == nil {
		return nil, map[string]any{}, true
	}
	raw, err := readLimitedRequestBody(r, publicAuthJSONMaxBytes)
	if err != nil {
		invalidJSONOrValidation(w, err)
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
	if errs := sanitizeJSONInputMap(body); len(errs) > 0 {
		writePublicValidationErrors(w, errs)
		return nil, nil, false
	}
	return raw, body, true
}
