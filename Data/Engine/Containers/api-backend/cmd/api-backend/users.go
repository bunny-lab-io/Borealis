package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

type userListStore interface {
	listUsers(ctx context.Context) ([]map[string]any, error)
}

type userRow struct {
	ID                    sql.NullInt64
	Username              sql.NullString
	DisplayName           sql.NullString
	Role                  sql.NullString
	LastLogin             sql.NullInt64
	CreatedAt             sql.NullInt64
	UpdatedAt             sql.NullInt64
	MFAEnabled            sql.NullInt64
	AuthResetRequired     sql.NullInt64
	AuthResetAt           sql.NullInt64
	AuthSource            sql.NullString
	DirectoryProviderID   sql.NullInt64
	DirectoryProviderName sql.NullString
	DirectoryDomain       sql.NullString
	DirectoryDisabled     sql.NullInt64
}

func registerUserRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/users", usersHandler(auth, fallback))
}

func usersHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
			return
		}
		_, failure := requireAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(userListStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "users_unavailable"})
			return
		}

		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		users, err := store.listUsers(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
	}
}

func (s *postgresOperatorStore) listUsers(ctx context.Context) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`
		SELECT
			u.id,
			u.username,
			u.display_name,
			u.role,
			u.last_login,
			u.created_at,
			u.updated_at,
			CASE WHEN COALESCE(u.mfa_disabled, 0) = 1 THEN 0 ELSE 1 END AS mfa_enabled,
			COALESCE(u.auth_reset_required, 0) AS auth_reset_required,
			COALESCE(u.auth_reset_at, 0) AS auth_reset_at,
			COALESCE(u.auth_source, 'local') AS auth_source,
			u.directory_provider_id,
			COALESCE(dp.name, '') AS directory_provider_name,
			COALESCE(u.directory_domain, '') AS directory_domain,
			COALESCE(u.directory_disabled, 0) AS directory_disabled
		FROM engine.users AS u
		LEFT JOIN engine.directory_providers AS dp
		       ON dp.id = u.directory_provider_id
		ORDER BY LOWER(u.username) ASC
		`,
	)
	if err != nil {
		return nil, err
	}

	rawRows := make([]userRow, 0)
	for rows.Next() {
		var row userRow
		if err := rows.Scan(
			&row.ID,
			&row.Username,
			&row.DisplayName,
			&row.Role,
			&row.LastLogin,
			&row.CreatedAt,
			&row.UpdatedAt,
			&row.MFAEnabled,
			&row.AuthResetRequired,
			&row.AuthResetAt,
			&row.AuthSource,
			&row.DirectoryProviderID,
			&row.DirectoryProviderName,
			&row.DirectoryDomain,
			&row.DirectoryDisabled,
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

	users := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		users = append(users, userPayload(row))
	}
	return users, nil
}

func userPayload(row userRow) map[string]any {
	username := nullString(row.Username)
	displayName := nullString(row.DisplayName)
	if displayName == "" {
		displayName = username
	}
	role := nullString(row.Role)
	if role == "" {
		role = defaultUserRole
	}
	authSource := strings.ToLower(nullString(row.AuthSource))
	if authSource == "" {
		authSource = "local"
	}
	return map[string]any{
		"id":                      nullInt(row.ID),
		"username":                username,
		"display_name":            displayName,
		"role":                    role,
		"last_login":              nullInt(row.LastLogin),
		"created_at":              nullInt(row.CreatedAt),
		"updated_at":              nullInt(row.UpdatedAt),
		"mfa_enabled":             truthyInt(row.MFAEnabled),
		"auth_reset_required":     truthyInt(row.AuthResetRequired),
		"auth_reset_at":           nullInt(row.AuthResetAt),
		"auth_source":             authSource,
		"directory_provider_id":   nullableInt(row.DirectoryProviderID),
		"directory_provider_name": nullString(row.DirectoryProviderName),
		"directory_domain":        nullString(row.DirectoryDomain),
		"directory_disabled":      truthyInt(row.DirectoryDisabled),
	}
}

func truthyInt(value sql.NullInt64) int {
	if value.Valid && value.Int64 != 0 {
		return 1
	}
	return 0
}
