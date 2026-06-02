package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type passkeyListStore interface {
	listUserPasskeys(ctx context.Context, username string) ([]map[string]any, error)
}

type passkeyRow struct {
	ID         sql.NullInt64
	Label      sql.NullString
	Transports sql.NullString
	CreatedAt  sql.NullInt64
	LastUsedAt sql.NullInt64
}

func authPasskeysHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
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
