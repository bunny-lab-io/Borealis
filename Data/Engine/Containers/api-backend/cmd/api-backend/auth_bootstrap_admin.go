package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	bootstrapAdminPendingTokenType = "bootstrap-admin-pending"
	bootstrapAdminPendingMaxAge    = 5 * time.Minute
)

type bootstrapAdminStore interface {
	authLoginStore
	bootstrapAdminRecoveryCandidate(ctx context.Context, username string) (displayName string, resetRequired bool, found bool, err error)
	createBootstrapAdmin(ctx context.Context, username string, displayName string, encryptedPassword string, encryptedMFASecret string, now int64) error
	recoverBootstrapAdmin(ctx context.Context, username string, encryptedPassword string, encryptedMFASecret string, now int64) error
}

func bootstrapAdminSetupHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		body, ok := readAuthJSON(w, r)
		if !ok {
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		state, err := currentBootstrapState(ctx, auth)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "bootstrap_state_unavailable", "message": err.Error()})
			return
		}
		if cleanText(state["phase"]) != bootstrapPhaseAdminSetupRequired {
			payload := publicBootstrapState(state)
			payload["error"] = "invalid_phase"
			writeJSON(w, http.StatusConflict, payload)
			return
		}
		username := cleanText(body["username"])
		displayName := firstText(cleanText(body["display_name"]), username)
		credential, credentialOK := passwordCredentialFromBody(body, "password", "password_sha512")
		if username == "" || !credentialOK {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "username and password are required"})
			return
		}
		writeBootstrapAdminPending(w, auth, "setup", username, displayName, "Admin", credential)
	}
}

func bootstrapAdminRecoverHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		body, ok := readAuthJSON(w, r)
		if !ok {
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		state, err := currentBootstrapState(ctx, auth)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "bootstrap_state_unavailable", "message": err.Error()})
			return
		}
		if cleanText(state["phase"]) != bootstrapPhaseAdminRecoveryRequired {
			payload := publicBootstrapState(state)
			payload["error"] = "invalid_phase"
			writeJSON(w, http.StatusConflict, payload)
			return
		}
		username := cleanText(body["username"])
		credential, credentialOK := passwordCredentialFromBody(body, "password", "password_sha512")
		if username == "" || !credentialOK {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "username and password are required"})
			return
		}
		store, ok := auth.store.(bootstrapAdminStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_store_unavailable"})
			return
		}
		displayName, resetRequired, found, err := store.bootstrapAdminRecoveryCandidate(ctx, username)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_store_unavailable", "message": err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "admin_not_found"})
			return
		}
		if !resetRequired {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "admin_recovery_not_required"})
			return
		}
		writeBootstrapAdminPending(w, auth, "recover", username, firstText(displayName, username), "Admin", credential)
	}
}

func bootstrapAdminMFAVerifyHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		body, ok := readAuthJSON(w, r)
		if !ok {
			return
		}
		pendingToken := cleanText(body["pending_token"])
		if pendingToken == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "bootstrap_pending"})
			return
		}
		pending, err := auth.verifier.signedPayload(pendingToken, bootstrapAdminPendingMaxAge)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_session"})
			return
		}
		if cleanText(pending["typ"]) != bootstrapAdminPendingTokenType {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_session"})
			return
		}
		code := onlyDigits(cleanText(body["code"]))
		if len(code) < 6 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_code"})
			return
		}
		secret := cleanText(pending["secret"])
		if secret == "" || !verifyTOTPCode(secret, code, time.Now()) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_code"})
			return
		}
		username := cleanText(pending["u"])
		displayName := firstText(cleanText(pending["display_name"]), username)
		role := firstText(cleanText(pending["r"]), "Admin")
		passwordVerifier := cleanText(pending["password_verifier"])
		flow := cleanText(pending["flow"])
		if username == "" || !passwordVerifierLooksValid(passwordVerifier) || (flow != "setup" && flow != "recover") {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_session"})
			return
		}
		if auth == nil || auth.aegis == nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "aegis_store_unavailable"})
			return
		}
		store, ok := auth.store.(bootstrapAdminStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_store_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		state, err := currentBootstrapState(ctx, auth)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "bootstrap_state_unavailable", "message": err.Error()})
			return
		}
		if flow == "setup" && cleanText(state["phase"]) != bootstrapPhaseAdminSetupRequired {
			payload := publicBootstrapState(state)
			payload["error"] = "invalid_phase"
			writeJSON(w, http.StatusConflict, payload)
			return
		}
		if flow == "recover" && cleanText(state["phase"]) != bootstrapPhaseAdminRecoveryRequired {
			payload := publicBootstrapState(state)
			payload["error"] = "invalid_phase"
			writeJSON(w, http.StatusConflict, payload)
			return
		}
		encryptedPassword, err := auth.aegis.encryptSecretText(ctx, passwordVerifier)
		if err != nil {
			writeBootstrapRequired(w, state)
			return
		}
		encryptedMFA, err := auth.aegis.encryptSecretText(ctx, secret)
		if err != nil {
			writeBootstrapRequired(w, state)
			return
		}
		now := time.Now().Unix()
		if flow == "setup" {
			err = store.createBootstrapAdmin(ctx, username, displayName, encryptedPassword, encryptedMFA, now)
		} else {
			err = store.recoverBootstrapAdmin(ctx, username, encryptedPassword, encryptedMFA, now)
		}
		if err != nil {
			if errors.Is(err, errOperatorNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "admin_not_found"})
				return
			}
			if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(strings.ToLower(err.Error()), "unique") {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "username already exists"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_store_unavailable", "message": err.Error()})
			return
		}
		finalizeLogin(w, r, auth, store, username, role)
	}
}

func writeBootstrapAdminPending(w http.ResponseWriter, auth *authService, flow string, username string, displayName string, role string, credential passwordCredential) {
	secret, err := randomBase32Secret()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "mfa_setup_unavailable"})
		return
	}
	passwordVerifier, err := newPasswordVerifierFromCredential(credential)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "password_verifier_failed"})
		return
	}
	token, err := auth.verifier.signPayload(map[string]any{
		"typ":               bootstrapAdminPendingTokenType,
		"flow":              flow,
		"u":                 username,
		"display_name":      firstText(displayName, username),
		"r":                 firstText(role, "Admin"),
		"password_verifier": passwordVerifier,
		"secret":            secret,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "mfa_session_failed"})
		return
	}
	otpauth := totpProvisioningURI(secret, username)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":            "mfa_required",
		"pending_token":     token,
		"stage":             "setup",
		"username":          username,
		"role":              firstText(role, "Admin"),
		"preferred_method":  "totp",
		"available_methods": []string{"totp"},
		"secret":            secret,
		"otpauth_url":       otpauth,
		"qr_image":          nil,
	})
}

func (s *postgresOperatorStore) bootstrapAdminRecoveryCandidate(ctx context.Context, username string) (string, bool, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return "", false, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	var displayName sql.NullString
	var resetRequired sql.NullInt64
	err = conn.QueryRowContext(ctx, `
		SELECT COALESCE(display_name, username), COALESCE(auth_reset_required, 0)
		  FROM engine.users
		 WHERE LOWER(username)=LOWER($1)
		   AND LOWER(role)='admin'
		 LIMIT 1
	`, username).Scan(&displayName, &resetRequired)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	return firstText(nullString(displayName), username), resetRequired.Valid && resetRequired.Int64 != 0, true, nil
}

func (s *postgresOperatorStore) createBootstrapAdmin(ctx context.Context, username string, displayName string, encryptedPassword string, encryptedMFASecret string, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("bootstrap_already_initialized")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engine.users(
			username,
			display_name,
			password_sha512,
			role,
			created_at,
			updated_at,
			mfa_enabled,
			mfa_disabled,
			mfa_secret,
			auth_reset_required,
			auth_reset_at
		) VALUES ($1, $2, $3, 'Admin', $4, $4, 1, 0, $5, 0, NULL)
	`, username, firstText(displayName, username), encryptedPassword, now, encryptedMFASecret); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *postgresOperatorStore) recoverBootstrapAdmin(ctx context.Context, username string, encryptedPassword string, encryptedMFASecret string, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE engine.users
		   SET password_sha512=$1,
		       mfa_secret=$2,
		       mfa_enabled=1,
		       mfa_disabled=0,
		       auth_reset_required=0,
		       auth_reset_at=NULL,
		       updated_at=$3
		 WHERE LOWER(username)=LOWER($4)
		   AND LOWER(role)='admin'
	`, encryptedPassword, encryptedMFASecret, now, username)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected <= 0 {
		return errOperatorNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM engine.user_passkeys
		 WHERE user_id IN (
			SELECT id FROM engine.users WHERE LOWER(username)=LOWER($1)
		 )
	`, username); err != nil {
		return err
	}
	return tx.Commit()
}
