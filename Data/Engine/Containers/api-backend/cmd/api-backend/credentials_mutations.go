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
	"time"

	"github.com/lib/pq"
)

const (
	credentialTypeMachine = "machine"
	credentialTypeDomain  = "domain"
	credentialTypeToken   = "token"
	connectionTypeSSH     = "ssh"
	connectionTypeWindows = "windows"
	connectionTypeWinRM   = "winrm"
)

var (
	errCredentialSiteNotFound = errors.New("site not found")
	credentialSecretFieldList = []string{
		"password",
		"private_key",
		"private_key_passphrase",
		"become_password",
	}
	credentialResetMetadataKeys = []string{
		"aegis_secret_state",
		"aegis_lost_secret_fields",
		"aegis_reset_at",
	}
)

type credentialMutationValues struct {
	Name                    string
	Description             string
	SiteID                  sql.NullInt64
	CredentialType          string
	ConnectionType          string
	Username                string
	PasswordEncrypted       []byte
	PrivateKeyEncrypted     []byte
	PrivateKeyPassphrase    []byte
	BecomeMethod            string
	BecomeUsername          string
	BecomePasswordEncrypted []byte
	MetadataJSON            string
}

func (s *postgresOperatorStore) createCredential(ctx context.Context, secret authSecretService, payload map[string]any) (map[string]any, int, error) {
	values, body, status, err := buildCredentialCreateValues(ctx, secret, payload)
	if err != nil {
		return body, status, err
	}
	now := time.Now().Unix()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := ensureCredentialSiteExistsTx(ctx, tx, values.SiteID); err != nil {
		return credentialSQLFailure(err)
	}
	var credentialID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO engine.credentials(
			name,
			description,
			site_id,
			credential_type,
			connection_type,
			username,
			password_encrypted,
			private_key_encrypted,
			private_key_passphrase_encrypted,
			become_method,
			become_username,
			become_password_encrypted,
			metadata_json,
			created_at,
			updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id
	`,
		values.Name,
		values.Description,
		sqlNullIntArg(values.SiteID),
		values.CredentialType,
		values.ConnectionType,
		values.Username,
		credentialSecretArg(values.PasswordEncrypted),
		credentialSecretArg(values.PrivateKeyEncrypted),
		credentialSecretArg(values.PrivateKeyPassphrase),
		values.BecomeMethod,
		values.BecomeUsername,
		credentialSecretArg(values.BecomePasswordEncrypted),
		values.MetadataJSON,
		now,
		now,
	).Scan(&credentialID)
	if err != nil {
		return credentialSQLFailure(err)
	}
	row, found, err := queryCredentialRowTx(ctx, tx, credentialID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		return credentialMutationFailure("credential creation failed", http.StatusInternalServerError)
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	committed = true
	return map[string]any{"status": "ok", "credential": credentialPayload(row)}, http.StatusOK, nil
}

func (s *postgresOperatorStore) updateCredential(ctx context.Context, secret authSecretService, credentialID int64, payload map[string]any) (map[string]any, int, error) {
	existing, found, err := s.loadCredentialRowForMutation(ctx, credentialID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		return credentialMutationFailure("credential not found", http.StatusNotFound)
	}
	values, body, status, err := buildCredentialUpdateValues(ctx, secret, payload, existing)
	if err != nil {
		return body, status, err
	}
	now := time.Now().Unix()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := ensureCredentialSiteExistsTx(ctx, tx, values.SiteID); err != nil {
		return credentialSQLFailure(err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE engine.credentials
		   SET name=$1,
		       description=$2,
		       site_id=$3,
		       credential_type=$4,
		       connection_type=$5,
		       username=$6,
		       password_encrypted=$7,
		       private_key_encrypted=$8,
		       private_key_passphrase_encrypted=$9,
		       become_method=$10,
		       become_username=$11,
		       become_password_encrypted=$12,
		       metadata_json=$13,
		       updated_at=$14
		 WHERE id=$15
	`,
		values.Name,
		values.Description,
		sqlNullIntArg(values.SiteID),
		values.CredentialType,
		values.ConnectionType,
		values.Username,
		credentialSecretArg(values.PasswordEncrypted),
		credentialSecretArg(values.PrivateKeyEncrypted),
		credentialSecretArg(values.PrivateKeyPassphrase),
		values.BecomeMethod,
		values.BecomeUsername,
		credentialSecretArg(values.BecomePasswordEncrypted),
		values.MetadataJSON,
		now,
		credentialID,
	)
	if err != nil {
		return credentialSQLFailure(err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return credentialMutationFailure("credential not found", http.StatusNotFound)
	}
	row, found, err := queryCredentialRowTx(ctx, tx, credentialID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		return credentialMutationFailure("credential not found", http.StatusNotFound)
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	committed = true
	return map[string]any{"status": "ok", "credential": credentialPayload(row)}, http.StatusOK, nil
}

func (s *postgresOperatorStore) deleteCredential(ctx context.Context, secret authSecretService, credentialID int64) (map[string]any, int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	result, err := conn.ExecContext(ctx, `DELETE FROM engine.credentials WHERE id=$1`, credentialID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return credentialMutationFailure("credential not found", http.StatusNotFound)
	}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func (s *postgresOperatorStore) loadCredentialRowForMutation(ctx context.Context, credentialID int64) (credentialRow, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return credentialRow{}, false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, credentialSelectSQL()+" WHERE c.id=$1", credentialID)
	if err != nil {
		return credentialRow{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return credentialRow{}, false, err
		}
		return credentialRow{}, false, nil
	}
	row, err := scanCredentialRow(rows)
	if err != nil {
		return credentialRow{}, false, err
	}
	if err := rows.Err(); err != nil {
		return credentialRow{}, false, err
	}
	return row, true, nil
}

func buildCredentialCreateValues(ctx context.Context, secret authSecretService, payload map[string]any) (credentialMutationValues, map[string]any, int, error) {
	name := cleanText(payload["name"])
	if name == "" {
		return credentialMutationValues{}, map[string]any{"error": "name is required"}, http.StatusBadRequest, errors.New("name is required")
	}
	siteID, err := normalizeCredentialSiteID(payload["site_id"])
	if err != nil {
		return credentialMutationValues{}, map[string]any{"error": err.Error()}, http.StatusBadRequest, err
	}
	credentialType, err := normalizeCredentialType(payload["credential_type"])
	if err != nil {
		return credentialMutationValues{}, map[string]any{"error": err.Error()}, http.StatusBadRequest, err
	}
	connectionType, err := normalizeCredentialConnectionType(payload["connection_type"])
	if err != nil {
		return credentialMutationValues{}, map[string]any{"error": err.Error()}, http.StatusBadRequest, err
	}
	metadata, err := normalizeCredentialMutationMetadata(payload, nil)
	if err != nil {
		return credentialMutationValues{}, map[string]any{"error": err.Error()}, http.StatusBadRequest, err
	}
	metadata = mergeCredentialResetMetadata(metadata, nil, credentialSecretFieldList)
	metadataJSON, err := marshalCredentialMetadata(metadata)
	if err != nil {
		return credentialMutationValues{}, nil, http.StatusInternalServerError, err
	}
	password, err := encryptCredentialSecretBlob(ctx, secret, payload["password"])
	if err != nil {
		return credentialMutationValues{}, protectedSecretErrorBody(err), protectedSecretErrorStatus(err), err
	}
	privateKey, err := encryptCredentialSecretText(ctx, secret, normalizePrivateKeyText(payload["private_key"]))
	if err != nil {
		return credentialMutationValues{}, protectedSecretErrorBody(err), protectedSecretErrorStatus(err), err
	}
	passphrase, err := encryptCredentialSecretBlob(ctx, secret, payload["private_key_passphrase"])
	if err != nil {
		return credentialMutationValues{}, protectedSecretErrorBody(err), protectedSecretErrorStatus(err), err
	}
	becomePassword, err := encryptCredentialSecretBlob(ctx, secret, payload["become_password"])
	if err != nil {
		return credentialMutationValues{}, protectedSecretErrorBody(err), protectedSecretErrorStatus(err), err
	}
	return credentialMutationValues{
		Name:                    name,
		Description:             cleanText(payload["description"]),
		SiteID:                  siteID,
		CredentialType:          credentialType,
		ConnectionType:          connectionType,
		Username:                cleanText(payload["username"]),
		PasswordEncrypted:       password,
		PrivateKeyEncrypted:     privateKey,
		PrivateKeyPassphrase:    passphrase,
		BecomeMethod:            strings.ToLower(cleanText(payload["become_method"])),
		BecomeUsername:          cleanText(payload["become_username"]),
		BecomePasswordEncrypted: becomePassword,
		MetadataJSON:            metadataJSON,
	}, nil, http.StatusOK, nil
}

func buildCredentialUpdateValues(ctx context.Context, secret authSecretService, payload map[string]any, existing credentialRow) (credentialMutationValues, map[string]any, int, error) {
	name := credentialStringField(payload, "name", existing.Name)
	if name == "" {
		return credentialMutationValues{}, map[string]any{"error": "name is required"}, http.StatusBadRequest, errors.New("name is required")
	}
	siteID := existing.SiteID
	if _, ok := payload["site_id"]; ok {
		normalized, err := normalizeCredentialSiteID(payload["site_id"])
		if err != nil {
			return credentialMutationValues{}, map[string]any{"error": err.Error()}, http.StatusBadRequest, err
		}
		siteID = normalized
	}
	credentialType := strings.ToLower(nullString(existing.CredentialType))
	if credentialType == "" {
		credentialType = credentialTypeMachine
	}
	if _, ok := payload["credential_type"]; ok {
		normalized, err := normalizeCredentialType(payload["credential_type"])
		if err != nil {
			return credentialMutationValues{}, map[string]any{"error": err.Error()}, http.StatusBadRequest, err
		}
		credentialType = normalized
	}
	connectionType := strings.ToLower(nullString(existing.ConnectionType))
	if connectionType == "" {
		connectionType = connectionTypeSSH
	}
	if _, ok := payload["connection_type"]; ok {
		normalized, err := normalizeCredentialConnectionType(payload["connection_type"])
		if err != nil {
			return credentialMutationValues{}, map[string]any{"error": err.Error()}, http.StatusBadRequest, err
		}
		connectionType = normalized
	}

	existingMetadata := parseJSONObject(existing.MetadataJSON)
	metadata, err := normalizeCredentialMutationMetadata(payload, existingMetadata)
	if err != nil {
		return credentialMutationValues{}, map[string]any{"error": err.Error()}, http.StatusBadRequest, err
	}
	resolvedFields := []string{}
	password := existing.PasswordEncrypted
	if _, ok := payload["password"]; ok {
		password, err = encryptCredentialSecretBlob(ctx, secret, payload["password"])
		resolvedFields = append(resolvedFields, "password")
	} else if truthyPayload(payload["clear_password"]) {
		password = nil
		resolvedFields = append(resolvedFields, "password")
	}
	if err != nil {
		return credentialMutationValues{}, protectedSecretErrorBody(err), protectedSecretErrorStatus(err), err
	}
	privateKey := existing.PrivateKeyEncrypted
	if _, ok := payload["private_key"]; ok {
		privateKey, err = encryptCredentialSecretText(ctx, secret, normalizePrivateKeyText(payload["private_key"]))
		resolvedFields = append(resolvedFields, "private_key")
	} else if truthyPayload(payload["clear_private_key"]) {
		privateKey = nil
		resolvedFields = append(resolvedFields, "private_key")
	}
	if err != nil {
		return credentialMutationValues{}, protectedSecretErrorBody(err), protectedSecretErrorStatus(err), err
	}
	passphrase := existing.PrivateKeyPassphrase
	if _, ok := payload["private_key_passphrase"]; ok {
		passphrase, err = encryptCredentialSecretBlob(ctx, secret, payload["private_key_passphrase"])
		resolvedFields = append(resolvedFields, "private_key_passphrase")
	} else if truthyPayload(payload["clear_private_key_passphrase"]) {
		passphrase = nil
		resolvedFields = append(resolvedFields, "private_key_passphrase")
	}
	if err != nil {
		return credentialMutationValues{}, protectedSecretErrorBody(err), protectedSecretErrorStatus(err), err
	}
	becomePassword := existing.BecomePasswordEncrypted
	if _, ok := payload["become_password"]; ok {
		becomePassword, err = encryptCredentialSecretBlob(ctx, secret, payload["become_password"])
		resolvedFields = append(resolvedFields, "become_password")
	} else if truthyPayload(payload["clear_become_password"]) {
		becomePassword = nil
		resolvedFields = append(resolvedFields, "become_password")
	}
	if err != nil {
		return credentialMutationValues{}, protectedSecretErrorBody(err), protectedSecretErrorStatus(err), err
	}

	metadata = mergeCredentialResetMetadata(metadata, existingMetadata, resolvedFields)
	metadataJSON, err := marshalCredentialMetadata(metadata)
	if err != nil {
		return credentialMutationValues{}, nil, http.StatusInternalServerError, err
	}
	return credentialMutationValues{
		Name:                    name,
		Description:             credentialStringField(payload, "description", existing.Description),
		SiteID:                  siteID,
		CredentialType:          credentialType,
		ConnectionType:          connectionType,
		Username:                credentialStringField(payload, "username", existing.Username),
		PasswordEncrypted:       password,
		PrivateKeyEncrypted:     privateKey,
		PrivateKeyPassphrase:    passphrase,
		BecomeMethod:            strings.ToLower(credentialStringField(payload, "become_method", existing.BecomeMethod)),
		BecomeUsername:          credentialStringField(payload, "become_username", existing.BecomeUsername),
		BecomePasswordEncrypted: becomePassword,
		MetadataJSON:            metadataJSON,
	}, nil, http.StatusOK, nil
}

func normalizeCredentialSiteID(raw any) (sql.NullInt64, error) {
	if raw == nil {
		return sql.NullInt64{}, nil
	}
	switch typed := raw.(type) {
	case string:
		value := strings.TrimSpace(typed)
		if value == "" || strings.EqualFold(value, "null") {
			return sql.NullInt64{}, nil
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return sql.NullInt64{}, errors.New("invalid site_id")
		}
		if parsed <= 0 {
			return sql.NullInt64{}, nil
		}
		return sql.NullInt64{Int64: parsed, Valid: true}, nil
	case float64:
		parsed := int64(typed)
		if parsed <= 0 {
			return sql.NullInt64{}, nil
		}
		return sql.NullInt64{Int64: parsed, Valid: true}, nil
	case int:
		if typed <= 0 {
			return sql.NullInt64{}, nil
		}
		return sql.NullInt64{Int64: int64(typed), Valid: true}, nil
	case int64:
		if typed <= 0 {
			return sql.NullInt64{}, nil
		}
		return sql.NullInt64{Int64: typed, Valid: true}, nil
	default:
		parsed := parseInt64Any(raw)
		if parsed <= 0 {
			return sql.NullInt64{}, nil
		}
		return sql.NullInt64{Int64: parsed, Valid: true}, nil
	}
}

func normalizeCredentialType(raw any) (string, error) {
	value := strings.ToLower(cleanText(raw))
	if value == "" {
		value = credentialTypeMachine
	}
	switch value {
	case credentialTypeMachine, credentialTypeDomain, credentialTypeToken:
		return value, nil
	default:
		return "", errors.New("invalid credential_type")
	}
}

func normalizeCredentialConnectionType(raw any) (string, error) {
	value := strings.ToLower(cleanText(raw))
	if value == "" {
		value = connectionTypeSSH
	}
	switch value {
	case connectionTypeSSH, connectionTypeWindows, connectionTypeWinRM:
		return value, nil
	default:
		return "", errors.New("invalid connection_type")
	}
}

func normalizeCredentialMutationMetadata(payload map[string]any, existing map[string]any) (map[string]any, error) {
	metadata := copyMap(existing)
	if metadata == nil {
		metadata = map[string]any{}
	}
	if raw, ok := payload["metadata"]; ok {
		switch typed := raw.(type) {
		case nil:
			metadata = map[string]any{}
		case string:
			if strings.TrimSpace(typed) == "" {
				metadata = map[string]any{}
			} else {
				return nil, errors.New("metadata must be an object")
			}
		case map[string]any:
			metadata = copyMap(typed)
		default:
			return nil, errors.New("metadata must be an object")
		}
	} else if raw, ok := payload["metadata_json"]; ok {
		text := secretPlainText(raw)
		if strings.TrimSpace(text) == "" {
			metadata = map[string]any{}
		} else {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(text), &parsed); err != nil || parsed == nil {
				return nil, errors.New("metadata_json must be valid JSON")
			}
			metadata = parsed
		}
	}
	if raw, ok := payload["winrm_transport"]; ok {
		transport := strings.ToLower(cleanText(raw))
		if transport == "" {
			delete(metadata, "winrm_transport")
		} else {
			metadata["winrm_transport"] = transport
		}
	}
	return metadata, nil
}

func mergeCredentialResetMetadata(metadata map[string]any, existingMetadata map[string]any, resolvedFields []string) map[string]any {
	cleaned := stripCredentialResetMetadata(metadata)
	source := metadata
	if existingMetadata != nil {
		source = existingMetadata
	}
	resetRequired, lostFields, resetAt := credentialResetDetails(source)
	if !resetRequired {
		return cleaned
	}
	resolved := map[string]struct{}{}
	for _, field := range resolvedFields {
		field = strings.ToLower(strings.TrimSpace(field))
		if _, allowed := credentialSecretFields[field]; allowed {
			resolved[field] = struct{}{}
		}
	}
	remaining := make([]string, 0, len(lostFields))
	for _, field := range lostFields {
		if _, ok := resolved[field]; !ok {
			remaining = append(remaining, field)
		}
	}
	if len(remaining) == 0 {
		return cleaned
	}
	if resetAt <= 0 {
		resetAt = time.Now().Unix()
	}
	cleaned["aegis_secret_state"] = credentialSecretResetState
	cleaned["aegis_lost_secret_fields"] = remaining
	cleaned["aegis_reset_at"] = resetAt
	return cleaned
}

func stripCredentialResetMetadata(metadata map[string]any) map[string]any {
	cleaned := copyMap(metadata)
	if cleaned == nil {
		cleaned = map[string]any{}
	}
	for _, key := range credentialResetMetadataKeys {
		delete(cleaned, key)
	}
	return cleaned
}

func marshalCredentialMetadata(metadata map[string]any) (string, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func credentialStringField(payload map[string]any, key string, existing sql.NullString) string {
	if raw, ok := payload[key]; ok {
		return cleanText(raw)
	}
	return nullString(existing)
}

func normalizePrivateKeyText(value any) string {
	text := secretPlainText(value)
	if text == "" {
		return ""
	}
	text = strings.TrimLeft(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text
}

func encryptCredentialSecretBlob(ctx context.Context, secret authSecretService, value any) ([]byte, error) {
	return encryptCredentialSecretText(ctx, secret, secretPlainText(value))
}

func encryptCredentialSecretText(ctx context.Context, secret authSecretService, value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	if secret == nil {
		return nil, errAegisLocked
	}
	encrypted, err := secret.encryptSecretText(ctx, value)
	if err != nil {
		return nil, err
	}
	if encrypted == "" {
		return nil, nil
	}
	return []byte(encrypted), nil
}

func secretPlainText(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(value)
	}
}

func truthyPayload(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return parseTruthy(typed)
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return false
	}
}

func credentialSecretArg(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func sqlNullIntArg(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func ensureCredentialSiteExistsTx(ctx context.Context, tx *sql.Tx, siteID sql.NullInt64) error {
	if !siteID.Valid {
		return nil
	}
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM engine.sites WHERE id=$1`, siteID.Int64).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return errCredentialSiteNotFound
	}
	return err
}

func queryCredentialRowTx(ctx context.Context, tx *sql.Tx, credentialID int64) (credentialRow, bool, error) {
	rows, err := tx.QueryContext(ctx, credentialSelectSQL()+" WHERE c.id=$1", credentialID)
	if err != nil {
		return credentialRow{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return credentialRow{}, false, err
		}
		return credentialRow{}, false, nil
	}
	row, err := scanCredentialRow(rows)
	if err != nil {
		return credentialRow{}, false, err
	}
	if err := rows.Err(); err != nil {
		return credentialRow{}, false, err
	}
	return row, true, nil
}

func credentialSQLFailure(err error) (map[string]any, int, error) {
	if errors.Is(err, errCredentialSiteNotFound) {
		return credentialMutationFailure("site not found", http.StatusBadRequest)
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && string(pqErr.Code) == "23505" {
		return credentialMutationFailure("credential name already exists", http.StatusConflict)
	}
	return nil, http.StatusInternalServerError, err
}

func credentialMutationFailure(message string, status int) (map[string]any, int, error) {
	return map[string]any{"error": message}, status, errors.New(message)
}
