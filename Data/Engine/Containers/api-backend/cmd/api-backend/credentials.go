package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

const credentialSecretResetState = "reset_required"

var credentialSecretFields = map[string]struct{}{
	"password":               {},
	"private_key":            {},
	"private_key_passphrase": {},
	"become_password":        {},
}

type credentialReadStore interface {
	listCredentials(ctx context.Context) ([]map[string]any, error)
	getCredential(ctx context.Context, credentialID int64) (map[string]any, bool, error)
}

type credentialRow struct {
	ID                      sql.NullInt64
	Name                    sql.NullString
	Description             sql.NullString
	SiteID                  sql.NullInt64
	SiteName                sql.NullString
	CredentialType          sql.NullString
	ConnectionType          sql.NullString
	Username                sql.NullString
	PasswordEncrypted       []byte
	PrivateKeyEncrypted     []byte
	PrivateKeyPassphrase    []byte
	BecomeMethod            sql.NullString
	BecomeUsername          sql.NullString
	BecomePasswordEncrypted []byte
	MetadataJSON            sql.NullString
	CreatedAt               sql.NullInt64
	UpdatedAt               sql.NullInt64
}

func registerCredentialRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/credentials", credentialsHandler(auth, fallback))
	mux.HandleFunc("/api/credentials/", credentialByIDHandler(auth, fallback))
}

func credentialsHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
			return
		}
		if _, failure := requireUser(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(credentialReadStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "credentials_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		credentials, err := store.listCredentials(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"credentials": credentials})
	}
}

func credentialByIDHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, http.MethodGet)
			return
		}
		if _, failure := requireUser(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		raw := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/credentials/"), "/")
		credentialID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || credentialID <= 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "credential not found"})
			return
		}
		store, ok := auth.store.(credentialReadStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "credentials_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		credential, found, err := store.getCredential(ctx, credentialID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "credential not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"credential": credential})
	}
}

func (s *postgresOperatorStore) listCredentials(ctx context.Context) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, credentialSelectSQL()+" ORDER BY LOWER(c.name) ASC, c.id ASC")
	if err != nil {
		return nil, err
	}
	rawRows := make([]credentialRow, 0)
	for rows.Next() {
		row, err := scanCredentialRow(rows)
		if err != nil {
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

	credentials := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		credentials = append(credentials, credentialPayload(row))
	}
	return credentials, nil
}

func (s *postgresOperatorStore) getCredential(ctx context.Context, credentialID int64) (map[string]any, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, credentialSelectSQL()+" WHERE c.id=$1", credentialID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	row, err := scanCredentialRow(rows)
	if err != nil {
		return nil, false, err
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return credentialPayload(row), true, nil
}

func credentialSelectSQL() string {
	return `
		SELECT
			c.id,
			c.name,
			c.description,
			c.site_id,
			s.name,
			c.credential_type,
			c.connection_type,
			c.username,
			c.password_encrypted,
			c.private_key_encrypted,
			c.private_key_passphrase_encrypted,
			c.become_method,
			c.become_username,
			c.become_password_encrypted,
			c.metadata_json,
			c.created_at,
			c.updated_at
		  FROM engine.credentials AS c
	 LEFT JOIN engine.sites AS s ON s.id = c.site_id
	`
}

func scanCredentialRow(scanner interface {
	Scan(dest ...any) error
}) (credentialRow, error) {
	var row credentialRow
	err := scanner.Scan(
		&row.ID,
		&row.Name,
		&row.Description,
		&row.SiteID,
		&row.SiteName,
		&row.CredentialType,
		&row.ConnectionType,
		&row.Username,
		&row.PasswordEncrypted,
		&row.PrivateKeyEncrypted,
		&row.PrivateKeyPassphrase,
		&row.BecomeMethod,
		&row.BecomeUsername,
		&row.BecomePasswordEncrypted,
		&row.MetadataJSON,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	return row, err
}

func credentialPayload(row credentialRow) map[string]any {
	metadata := parseJSONObject(row.MetadataJSON)
	resetRequired, lostFields, resetAt := credentialResetDetails(metadata)
	credentialType := strings.ToLower(nullString(row.CredentialType))
	if credentialType == "" {
		credentialType = "machine"
	}
	connectionType := strings.ToLower(nullString(row.ConnectionType))
	if connectionType == "" {
		connectionType = "ssh"
	}
	return map[string]any{
		"id":                         nullInt(row.ID),
		"name":                       nullString(row.Name),
		"description":                nullString(row.Description),
		"site_id":                    nullableInt(row.SiteID),
		"site_name":                  nullString(row.SiteName),
		"credential_type":            credentialType,
		"connection_type":            connectionType,
		"username":                   nullString(row.Username),
		"has_password":               secretBlobPresent(row.PasswordEncrypted),
		"has_private_key":            secretBlobPresent(row.PrivateKeyEncrypted),
		"has_private_key_passphrase": secretBlobPresent(row.PrivateKeyPassphrase),
		"become_method":              nullString(row.BecomeMethod),
		"become_username":            nullString(row.BecomeUsername),
		"has_become_password":        secretBlobPresent(row.BecomePasswordEncrypted),
		"metadata":                   metadata,
		"secret_reset_required":      resetRequired,
		"lost_secret_fields":         lostFields,
		"reset_at":                   resetAt,
		"created_at":                 nullInt(row.CreatedAt),
		"updated_at":                 nullInt(row.UpdatedAt),
	}
}

func secretBlobPresent(blob []byte) bool {
	return len(blob) > 0
}

func credentialResetDetails(metadata map[string]any) (bool, []string, int64) {
	state := strings.ToLower(cleanText(metadata["aegis_secret_state"]))
	fields := normalizeLostCredentialFields(metadata["aegis_lost_secret_fields"])
	resetRequired := state == credentialSecretResetState && len(fields) > 0
	if !resetRequired {
		return false, []string{}, 0
	}
	return true, fields, parseInt64Any(metadata["aegis_reset_at"])
}

func normalizeLostCredentialFields(raw any) []string {
	values, ok := raw.([]any)
	if !ok {
		return []string{}
	}
	fields := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		field := strings.ToLower(cleanText(value))
		if _, allowed := credentialSecretFields[field]; !allowed {
			continue
		}
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		fields = append(fields, field)
	}
	return fields
}

func parseInt64Any(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}
