package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type userListStore interface {
	listUsers(ctx context.Context) ([]map[string]any, error)
}

type userMutationStore interface {
	deleteUser(ctx context.Context, profile operatorProfile, username string) (map[string]any, int, error)
	updateUserRole(ctx context.Context, profile operatorProfile, username string, role string) (map[string]any, int, error)
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
	mux.HandleFunc("/api/users/", userSubtreeHandler(auth, fallback))
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

func userSubtreeHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, action, ok := parseUserSubtreePath(r.URL.Path)
		if !ok {
			if fallback != nil {
				fallback.ServeHTTP(w, r)
				return
			}
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}

		if action == "" && r.Method == http.MethodDelete {
			userDelete(w, r, auth, username)
			return
		}
		if action == "role" && r.Method == http.MethodPost {
			userRoleUpdate(w, r, auth, username)
			return
		}

		if fallback != nil {
			fallback.ServeHTTP(w, r)
			return
		}
		writeMethodNotAllowed(w, http.MethodDelete+", "+http.MethodPost)
	}
}

func parseUserSubtreePath(path string) (string, string, bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/api/users/"), "/")
	if rest == "" {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 2 {
		return "", "", false
	}
	username, err := url.PathUnescape(parts[0])
	if err != nil {
		username = parts[0]
	}
	action := ""
	if len(parts) == 2 {
		action = strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(username), action, strings.TrimSpace(username) != ""
}

func userDelete(w http.ResponseWriter, r *http.Request, auth *authService, username string) {
	profile, store, ok := userMutationRequestContext(w, r, auth)
	if !ok {
		return
	}
	ctx, cancel := userTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.deleteUser(ctx, profile, username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, payload)
}

func userRoleUpdate(w http.ResponseWriter, r *http.Request, auth *authService, username string) {
	profile, store, ok := userMutationRequestContext(w, r, auth)
	if !ok {
		return
	}

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return
	}
	role := normalizeUserRole(body["role"])
	if role != "User" && role != "Admin" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid role"})
		return
	}

	ctx, cancel := userTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.updateUserRole(ctx, profile, username, role)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, payload)
}

func userMutationRequestContext(w http.ResponseWriter, r *http.Request, auth *authService) (operatorProfile, userMutationStore, bool) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			unauthorizedAuthFailure().write(w)
			return operatorProfile{}, nil, false
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return operatorProfile{}, nil, false
	}
	if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		(&authFailure{
			status: http.StatusForbidden,
			body: map[string]any{
				"error":   "forbidden",
				"message": "Administrator permissions are required for this action.",
			},
		}).write(w)
		return operatorProfile{}, nil, false
	}
	store, ok := auth.store.(userMutationStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "users_unavailable"})
		return operatorProfile{}, nil, false
	}
	return profile, store, true
}

func userTimeoutContext(ctx context.Context, auth *authService) (context.Context, context.CancelFunc) {
	timeout := auth.timeout
	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func normalizeUserRole(value any) string {
	text := strings.ToLower(strings.TrimSpace(cleanText(value)))
	switch text {
	case "admin":
		return "Admin"
	case "user":
		return "User"
	default:
		return ""
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

func (s *postgresOperatorStore) deleteUser(ctx context.Context, profile operatorProfile, username string) (map[string]any, int, error) {
	usernameNorm := strings.TrimSpace(username)
	if usernameNorm == "" {
		return map[string]any{"error": "invalid username"}, http.StatusBadRequest, nil
	}
	source, found, err := s.userAuthSource(ctx, usernameNorm)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if found && source == "directory" {
		return map[string]any{"error": "directory_user_local_action_disabled"}, http.StatusForbidden, nil
	}
	if strings.EqualFold(profile.Username, usernameNorm) {
		return map[string]any{"error": "You cannot delete the user you are currently logged in as."}, http.StatusBadRequest, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer tx.Rollback()

	var totalUsers int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM engine.users").Scan(&totalUsers); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if totalUsers <= 1 {
		return map[string]any{"error": "There is only one user currently configured, you cannot delete this user until you have created another."}, http.StatusBadRequest, nil
	}

	var userID sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM engine.users WHERE LOWER(username)=LOWER($1)", usernameNorm).Scan(&userID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, http.StatusInternalServerError, err
	}
	if userID.Valid {
		if _, err := tx.ExecContext(ctx, "DELETE FROM engine.user_site_assignments WHERE user_id=$1", userID.Int64); err != nil {
			return nil, http.StatusInternalServerError, err
		}
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM engine.users WHERE LOWER(username)=LOWER($1)", usernameNorm)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if deleted == 0 {
		return map[string]any{"error": "user not found"}, http.StatusNotFound, nil
	}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func (s *postgresOperatorStore) updateUserRole(ctx context.Context, profile operatorProfile, username string, role string) (map[string]any, int, error) {
	usernameNorm := strings.TrimSpace(username)
	if usernameNorm == "" {
		return map[string]any{"error": "invalid username"}, http.StatusBadRequest, nil
	}
	source, found, err := s.userAuthSource(ctx, usernameNorm)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if found && source == "directory" {
		return map[string]any{"error": "directory_user_local_action_disabled"}, http.StatusForbidden, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer tx.Rollback()

	if role == "User" {
		var adminCount int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM engine.users WHERE LOWER(role)='admin'").Scan(&adminCount); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		var currentRole sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT LOWER(role) FROM engine.users WHERE LOWER(username)=LOWER($1)", usernameNorm).Scan(&currentRole); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, http.StatusInternalServerError, err
		}
		if strings.EqualFold(nullString(currentRole), "admin") && adminCount <= 1 {
			return map[string]any{"error": "cannot demote the last admin"}, http.StatusBadRequest, nil
		}
	}

	result, err := tx.ExecContext(ctx, "UPDATE engine.users SET role=$1, updated_at=$2 WHERE LOWER(username)=LOWER($3)", role, time.Now().Unix(), usernameNorm)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if updated == 0 {
		return map[string]any{"error": "user not found"}, http.StatusNotFound, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func (s *postgresOperatorStore) userAuthSource(ctx context.Context, username string) (string, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return "", false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	var source sql.NullString
	err = conn.QueryRowContext(ctx, "SELECT COALESCE(auth_source, 'local') FROM engine.users WHERE LOWER(username)=LOWER($1)", username).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	normalized := strings.ToLower(strings.TrimSpace(nullString(source)))
	if normalized == "" {
		normalized = "local"
	}
	return normalized, true, nil
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
