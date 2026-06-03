package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type passkeyListStore interface {
	listUserPasskeys(ctx context.Context, username string) ([]map[string]any, error)
	updateUserPasskeyLabel(ctx context.Context, username string, passkeyID int64, label string) (map[string]any, int, error)
	deleteUserPasskey(ctx context.Context, username string, passkeyID int64) (map[string]any, int, error)
}

type passkeyRow struct {
	ID         sql.NullInt64
	Label      sql.NullString
	Transports sql.NullString
	CreatedAt  sql.NullInt64
	LastUsedAt sql.NullInt64
}

func authPasskeysHandler(auth *authService, _ http.Handler) http.HandlerFunc {
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
		username := strings.TrimSpace(profile.Username)
		if username == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		store, ok := auth.store.(passkeyListStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "passkeys_unavailable"})
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		passkeys, err := store.listUserPasskeys(ctx, username)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "ok",
			"passkeys":      passkeys,
			"passkey_count": len(passkeys),
		})
	}
}

func authPasskeyByIDHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/auth/passkeys/"), "/")
		passkeyID, err := strconv.ParseInt(rest, 10, 64)
		if err != nil || passkeyID <= 0 {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, "PATCH, DELETE")
			return
		}
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		username := strings.TrimSpace(profile.Username)
		if username == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		store, ok := auth.store.(passkeyListStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "passkeys_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		switch r.Method {
		case http.MethodPatch:
			var body map[string]any
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
				return
			}
			label := cleanText(body["label"])
			if len(label) > 80 {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_label"})
				return
			}
			payload, status, err := store.updateUserPasskeyLabel(ctx, username, passkeyID, label)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, status, payload)
		case http.MethodDelete:
			payload, status, err := store.deleteUserPasskey(ctx, username, passkeyID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, status, payload)
		default:
			writeMethodNotAllowed(w, "PATCH, DELETE")
		}
	}
}

func (s *postgresOperatorStore) listUserPasskeys(ctx context.Context, username string) ([]map[string]any, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return []map[string]any{}, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`
		SELECT
			up.id,
			COALESCE(up.label, ''),
			COALESCE(up.transports_json, '[]'),
			COALESCE(up.created_at, 0),
			COALESCE(up.last_used_at, 0)
		FROM engine.user_passkeys AS up
		JOIN engine.users AS u ON u.id = up.user_id
		WHERE LOWER(u.username)=LOWER($1)
		ORDER BY up.created_at ASC, up.id ASC
		`,
		username,
	)
	if err != nil {
		return nil, err
	}

	rawRows := make([]passkeyRow, 0)
	for rows.Next() {
		var row passkeyRow
		if err := rows.Scan(
			&row.ID,
			&row.Label,
			&row.Transports,
			&row.CreatedAt,
			&row.LastUsedAt,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		rawRows = append(rawRows, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	passkeys := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		passkeys = append(passkeys, passkeyPayload(row))
	}
	return passkeys, nil
}

func (s *postgresOperatorStore) updateUserPasskeyLabel(ctx context.Context, username string, passkeyID int64, label string) (map[string]any, int, error) {
	username = strings.TrimSpace(username)
	if username == "" || passkeyID <= 0 {
		return map[string]any{"error": "passkey_not_found"}, http.StatusNotFound, nil
	}
	normalizedLabel := strings.TrimSpace(label)
	if normalizedLabel == "" {
		normalizedLabel = "Passkey"
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var row passkeyRow
	err = tx.QueryRowContext(ctx, `
		UPDATE engine.user_passkeys AS up
		   SET label = $1
		 WHERE up.id = $2
		   AND up.user_id IN (
		       SELECT id FROM engine.users WHERE LOWER(username)=LOWER($3)
		   )
	 RETURNING up.id, COALESCE(up.label, ''), COALESCE(up.transports_json, '[]'), COALESCE(up.created_at, 0), COALESCE(up.last_used_at, 0)
	`, normalizedLabel, passkeyID, username).Scan(&row.ID, &row.Label, &row.Transports, &row.CreatedAt, &row.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"error": "passkey_not_found"}, http.StatusNotFound, nil
	}
	if err != nil {
		return nil, 0, err
	}
	count, err := countUserPasskeysTx(ctx, tx, username)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	committed = true
	return map[string]any{
		"status":        "ok",
		"passkey":       passkeyPayload(row),
		"passkey_count": count,
	}, http.StatusOK, nil
}

func (s *postgresOperatorStore) deleteUserPasskey(ctx context.Context, username string, passkeyID int64) (map[string]any, int, error) {
	username = strings.TrimSpace(username)
	if username == "" || passkeyID <= 0 {
		return map[string]any{"error": "passkey_not_found"}, http.StatusNotFound, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var removedID int64
	err = tx.QueryRowContext(ctx, `
		DELETE FROM engine.user_passkeys AS up
		 WHERE up.id = $1
		   AND up.user_id IN (
		       SELECT id FROM engine.users WHERE LOWER(username)=LOWER($2)
		   )
	 RETURNING up.id
	`, passkeyID, username).Scan(&removedID)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"error": "passkey_not_found"}, http.StatusNotFound, nil
	}
	if err != nil {
		return nil, 0, err
	}
	count, err := countUserPasskeysTx(ctx, tx, username)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	committed = true
	return map[string]any{
		"status":        "ok",
		"removed":       removedID > 0,
		"passkey_count": count,
	}, http.StatusOK, nil
}

func countUserPasskeysTx(ctx context.Context, tx *sql.Tx, username string) (int64, error) {
	var count int64
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM engine.user_passkeys AS up
		  JOIN engine.users AS u ON u.id = up.user_id
		 WHERE LOWER(u.username)=LOWER($1)
	`, username).Scan(&count)
	return count, err
}

func passkeyPayload(row passkeyRow) map[string]any {
	label := nullString(row.Label)
	if label == "" {
		label = "Passkey"
	}
	return map[string]any{
		"id":           nullInt(row.ID),
		"label":        label,
		"created_at":   nullInt(row.CreatedAt),
		"last_used_at": nullInt(row.LastUsedAt),
		"transports":   passkeyTransports(row.Transports),
	}
}

func passkeyTransports(raw sql.NullString) []string {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return []string{}
	}
	var values []any
	if err := json.Unmarshal([]byte(raw.String), &values); err != nil {
		return []string{}
	}
	transports := make([]string, 0, len(values))
	for _, value := range values {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			transports = append(transports, text)
		}
	}
	return transports
}
