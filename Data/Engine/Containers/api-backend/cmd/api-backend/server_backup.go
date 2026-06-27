package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	engineBackupKind             = "borealis.engine.config_backup"
	engineBackupSchemaVersion    = 1
	engineBackupCipher           = "AES-256-GCM"
	engineBackupConfirmationText = "RESTORE ENGINE CONFIG BACKUP"
	engineBackupBinaryMarker     = "__borealis_binary_b64"
	engineBackupTimeout          = 5 * time.Minute
)

type engineBackupTableSpec struct {
	Name         string
	OrderBy      []string
	Export       bool
	Restore      bool
	ResetSerials bool
}

type engineBackupColumn struct {
	Name     string `json:"name"`
	DataType string `json:"data_type,omitempty"`
	UDTName  string `json:"udt_name,omitempty"`
}

type engineBackupTable struct {
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
}

type engineBackupFile struct {
	ContentB64 string `json:"content_b64"`
	Mode       int    `json:"mode"`
}

type engineBackupPayload struct {
	Kind          string                       `json:"kind"`
	SchemaVersion int                          `json:"schema_version"`
	CreatedAt     string                       `json:"created_at"`
	Source        map[string]any               `json:"source"`
	Tables        map[string]engineBackupTable  `json:"tables"`
	Files         map[string]engineBackupFile   `json:"files"`
	Counts        map[string]map[string]int     `json:"counts"`
}

type encryptedEngineBackupDocument struct {
	Kind          string          `json:"kind"`
	SchemaVersion int             `json:"schema_version"`
	CreatedAt     string          `json:"created_at"`
	Source        map[string]any  `json:"source"`
	Encryption    string          `json:"encryption"`
	KDFName       string          `json:"kdf_name"`
	KDFParams     json.RawMessage `json:"kdf_params"`
	NonceB64      string          `json:"nonce_b64"`
	CiphertextB64 string          `json:"ciphertext_b64"`
}

type engineBackupRestoreRequest struct {
	Cipher       string
	Confirmation string
	Backup       encryptedEngineBackupDocument
}

type engineBackupRestoreResult struct {
	TablesRestored int `json:"tables_restored"`
	RowsRestored   int `json:"rows_restored"`
	FilesRestored  int `json:"files_restored"`
}

type engineBackupDataStore interface {
	exportEngineBackupPayload(ctx context.Context) (engineBackupPayload, error)
	restoreEngineBackupPayload(ctx context.Context, payload engineBackupPayload) (engineBackupRestoreResult, error)
}

type engineBackupAegisService interface {
	engineBackupExportKey(ctx context.Context) ([]byte, aegisState, error)
	engineBackupRestoreKey(ctx context.Context, cipherText string, document encryptedEngineBackupDocument) ([]byte, error)
	engineBackupClearActiveKey()
}

type engineBackupUserError struct {
	status  int
	code    string
	message string
}

func (e engineBackupUserError) Error() string {
	return e.code
}

func registerBackupRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("GET /api/server/backup/export", engineBackupExportHandler(auth))
	mux.HandleFunc("POST /api/server/backup/restore", engineBackupRestoreHandler(auth, false))
	mux.HandleFunc("POST /api/bootstrap/backup/restore", engineBackupRestoreHandler(auth, true))
}

func engineBackupExportHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		store, aegis, ok := engineBackupServices(auth)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "backup_restore_unavailable"})
			return
		}
		ctx, cancel := engineBackupRequestTimeout(r.Context(), auth)
		defer cancel()
		key, state, err := aegis.engineBackupExportKey(ctx)
		if err != nil {
			payload, status := aegisErrorBody(err)
			writeJSON(w, status, payload)
			return
		}
		backupPayload, err := store.exportEngineBackupPayload(ctx)
		if err != nil {
			engineBackupWriteError(w, err)
			return
		}
		document, err := encryptEngineBackupPayload(backupPayload, key, state)
		if err != nil {
			engineBackupWriteError(w, err)
			return
		}
		filename := "borealis-engine-backup-" + time.Now().UTC().Format("20060102-150405Z") + ".json"
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(document)
	}
}

func engineBackupRestoreHandler(auth *authService, bootstrapOnly bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		ctx, cancel := engineBackupRequestTimeout(r.Context(), auth)
		defer cancel()
		if bootstrapOnly {
			state, err := currentBootstrapState(ctx, auth)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]any{"error": "bootstrap_state_unavailable", "message": err.Error()})
				return
			}
			if cleanText(state["phase"]) == bootstrapPhaseLoginRequired {
				payload := publicBootstrapState(state)
				payload["error"] = "bootstrap_restore_unavailable"
				payload["message"] = "Bootstrap restore is only available before normal login is enabled."
				writeJSON(w, http.StatusConflict, payload)
				return
			}
		} else if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		store, aegis, ok := engineBackupServices(auth)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "backup_restore_unavailable"})
			return
		}
		req, ok := readEngineBackupRestoreRequest(w, r)
		if !ok {
			return
		}
		key, err := aegis.engineBackupRestoreKey(ctx, req.Cipher, req.Backup)
		if err != nil {
			payload, status := aegisErrorBody(err)
			writeJSON(w, status, payload)
			return
		}
		payload, err := decryptEngineBackupPayload(req.Backup, key)
		if err != nil {
			engineBackupWriteError(w, err)
			return
		}
		if err := validateEngineBackupPayload(payload, key, req.Backup); err != nil {
			engineBackupWriteError(w, err)
			return
		}
		result, err := store.restoreEngineBackupPayload(ctx, payload)
		if err != nil {
			engineBackupWriteError(w, err)
			return
		}
		aegis.engineBackupClearActiveKey()
		clearAuthCookies(w)
		bootstrapPayload := map[string]any{}
		if nextState, err := currentBootstrapState(ctx, auth); err == nil {
			bootstrapPayload = publicBootstrapState(nextState)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":            "ok",
			"restart_required":  true,
			"unlock_required":   true,
			"confirmation":      req.Confirmation,
			"tables_restored":   result.TablesRestored,
			"rows_restored":     result.RowsRestored,
			"files_restored":    result.FilesRestored,
			"bootstrap_state":   bootstrapPayload,
		})
	}
}

func engineBackupServices(auth *authService) (engineBackupDataStore, engineBackupAegisService, bool) {
	if auth == nil {
		return nil, nil, false
	}
	store, storeOK := auth.store.(engineBackupDataStore)
	aegis, aegisOK := auth.aegis.(engineBackupAegisService)
	return store, aegis, storeOK && aegisOK
}

func engineBackupRequestTimeout(parent context.Context, auth *authService) (context.Context, context.CancelFunc) {
	timeout := engineBackupTimeout
	if auth != nil && auth.timeout > timeout {
		timeout = auth.timeout
	}
	return context.WithTimeout(parent, timeout)
}

func readEngineBackupRestoreRequest(w http.ResponseWriter, r *http.Request) (engineBackupRestoreRequest, bool) {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 128<<20))
	decoder.UseNumber()
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": "Request body must be valid JSON."})
		return engineBackupRestoreRequest{}, false
	}
	var req engineBackupRestoreRequest
	_ = json.Unmarshal(raw["cipher"], &req.Cipher)
	_ = json.Unmarshal(raw["confirmation"], &req.Confirmation)
	req.Cipher = strings.TrimSpace(req.Cipher)
	req.Confirmation = strings.TrimSpace(req.Confirmation)
	if req.Cipher == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cipher_required", "message": "Aegis Cipher is required."})
		return engineBackupRestoreRequest{}, false
	}
	if req.Confirmation != engineBackupConfirmationText {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "confirmation_required", "message": "Typed confirmation must match "+engineBackupConfirmationText+"."})
		return engineBackupRestoreRequest{}, false
	}
	backupRaw := raw["backup"]
	if len(backupRaw) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "backup_required", "message": "Encrypted backup JSON is required."})
		return engineBackupRestoreRequest{}, false
	}
	if len(backupRaw) > 0 && backupRaw[0] == '"' {
		var text string
		if err := json.Unmarshal(backupRaw, &text); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_backup", "message": "Backup field must contain encrypted backup JSON."})
			return engineBackupRestoreRequest{}, false
		}
		backupRaw = []byte(text)
	}
	if err := json.Unmarshal(backupRaw, &req.Backup); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_backup", "message": "Backup field must contain encrypted backup JSON."})
		return engineBackupRestoreRequest{}, false
	}
	return req, true
}

func (s *goAegisService) engineBackupExportKey(ctx context.Context) ([]byte, aegisState, error) {
	state, err := s.state(ctx)
	if err != nil {
		return nil, aegisState{}, err
	}
	if !state.Configured {
		return nil, aegisState{}, errAegisNotConfigured
	}
	key, err := s.activeKey()
	if err != nil {
		return nil, aegisState{}, err
	}
	return key, state, nil
}

func (s *goAegisService) engineBackupRestoreKey(_ context.Context, cipherText string, document encryptedEngineBackupDocument) ([]byte, error) {
	if strings.TrimSpace(cipherText) == "" {
		return nil, fmt.Errorf("%w: Aegis Cipher is required", errAegisInvalidRequest)
	}
	state, err := aegisStateFromBackupDocument(document)
	if err != nil {
		return nil, err
	}
	key, err := s.deriveKeyFromState(cipherText, state)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func (s *goAegisService) engineBackupClearActiveKey() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.key = nil
	s.mu.Unlock()
}

func aegisStateFromBackupDocument(document encryptedEngineBackupDocument) (aegisState, error) {
	if document.Kind != engineBackupKind || document.SchemaVersion != engineBackupSchemaVersion {
		return aegisState{}, backupUserError(http.StatusBadRequest, "unsupported_backup", "Backup format is not supported by this Engine.")
	}
	if strings.TrimSpace(document.KDFName) == "" || len(document.KDFParams) == 0 {
		return aegisState{}, backupUserError(http.StatusBadRequest, "invalid_backup", "Backup is missing Aegis KDF metadata.")
	}
	params := strings.TrimSpace(string(document.KDFParams))
	if params == "" || params == "null" {
		params = "{}"
	}
	params = canonicalEngineBackupJSONText(params)
	return aegisState{
		Configured:    true,
		KDFName:       strings.TrimSpace(document.KDFName),
		KDFParamsJSON: params,
	}, nil
}

func encryptEngineBackupPayload(payload engineBackupPayload, key []byte, state aegisState) (encryptedEngineBackupDocument, error) {
	plain, err := json.Marshal(payload)
	if err != nil {
		return encryptedEngineBackupDocument{}, err
	}
	nonce, ciphertext, err := engineBackupSeal(plain, key)
	if err != nil {
		return encryptedEngineBackupDocument{}, err
	}
	params := json.RawMessage(strings.TrimSpace(state.KDFParamsJSON))
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	return encryptedEngineBackupDocument{
		Kind:          engineBackupKind,
		SchemaVersion: engineBackupSchemaVersion,
		CreatedAt:     payload.CreatedAt,
		Source:        payload.Source,
		Encryption:    engineBackupCipher,
		KDFName:       state.KDFName,
		KDFParams:     params,
		NonceB64:      base64.StdEncoding.EncodeToString(nonce),
		CiphertextB64: base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

func decryptEngineBackupPayload(document encryptedEngineBackupDocument, key []byte) (engineBackupPayload, error) {
	if document.Kind != engineBackupKind || document.SchemaVersion != engineBackupSchemaVersion {
		return engineBackupPayload{}, backupUserError(http.StatusBadRequest, "unsupported_backup", "Backup format is not supported by this Engine.")
	}
	if strings.TrimSpace(document.Encryption) != engineBackupCipher {
		return engineBackupPayload{}, backupUserError(http.StatusBadRequest, "unsupported_encryption", "Backup encryption is not supported by this Engine.")
	}
	nonce, err := base64.StdEncoding.DecodeString(strings.TrimSpace(document.NonceB64))
	if err != nil || len(nonce) != aegisNonceLength {
		return engineBackupPayload{}, backupUserError(http.StatusBadRequest, "invalid_backup", "Backup nonce is invalid.")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(document.CiphertextB64))
	if err != nil || len(ciphertext) == 0 {
		return engineBackupPayload{}, backupUserError(http.StatusBadRequest, "invalid_backup", "Backup ciphertext is invalid.")
	}
	plain, err := engineBackupOpen(nonce, ciphertext, key)
	if err != nil {
		return engineBackupPayload{}, errAegisInvalidCipher
	}
	var payload engineBackupPayload
	decoder := json.NewDecoder(strings.NewReader(string(plain)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return engineBackupPayload{}, backupUserError(http.StatusBadRequest, "invalid_backup_payload", "Encrypted backup payload is not valid JSON.")
	}
	return payload, nil
}

func engineBackupSeal(plain []byte, key []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, aegisNonceLength)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, plain, nil), nil
}

func engineBackupOpen(nonce []byte, ciphertext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func validateEngineBackupPayload(payload engineBackupPayload, key []byte, document encryptedEngineBackupDocument) error {
	if payload.Kind != engineBackupKind || payload.SchemaVersion != engineBackupSchemaVersion {
		return backupUserError(http.StatusBadRequest, "invalid_backup_payload", "Encrypted backup payload is not a Borealis Engine backup.")
	}
	tableNames := engineBackupRestoreTableSet()
	for name := range payload.Tables {
		if !tableNames[name] {
			return backupUserError(http.StatusBadRequest, "unknown_table_id", "Backup contains unsupported table "+name+".")
		}
	}
	fileIDs := engineBackupFileSet()
	for id := range payload.Files {
		if !fileIDs[id] {
			return backupUserError(http.StatusBadRequest, "unknown_file_id", "Backup contains unsupported file "+id+".")
		}
	}
	state, err := engineBackupPayloadAegisState(payload)
	if err != nil {
		return err
	}
	documentState, err := aegisStateFromBackupDocument(document)
	if err != nil {
		return err
	}
	if strings.TrimSpace(state.KDFName) != strings.TrimSpace(documentState.KDFName) || canonicalEngineBackupJSONText(state.KDFParamsJSON) != canonicalEngineBackupJSONText(documentState.KDFParamsJSON) {
		return backupUserError(http.StatusBadRequest, "aegis_state_mismatch", "Encrypted backup payload does not match backup Aegis metadata.")
	}
	plain, err := aegisDecryptText(state.VerificationToken, key)
	if err != nil || plain != aegisVerificationPlaintext {
		return errAegisInvalidCipher
	}
	return nil
}

func canonicalEngineBackupJSONText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "{}"
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return value
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return value
	}
	return string(encoded)
}

func engineBackupPayloadAegisState(payload engineBackupPayload) (aegisState, error) {
	table, ok := payload.Tables["engine.aegis_cipher_state"]
	if !ok || len(table.Rows) == 0 {
		return aegisState{}, backupUserError(http.StatusBadRequest, "aegis_state_required", "Backup does not contain Aegis Cipher state.")
	}
	row := table.Rows[0]
	kdfName := cleanText(row["kdf_name"])
	params := cleanText(row["kdf_params_json"])
	token := cleanText(row["verification_token"])
	if kdfName == "" || params == "" || token == "" {
		return aegisState{}, backupUserError(http.StatusBadRequest, "aegis_state_invalid", "Backup Aegis Cipher state is incomplete.")
	}
	return aegisState{
		Configured:        true,
		KDFName:           kdfName,
		KDFParamsJSON:     params,
		VerificationToken: token,
	}, nil
}

func (s *postgresOperatorStore) exportEngineBackupPayload(ctx context.Context) (engineBackupPayload, error) {
	if s == nil || s.db == nil {
		return engineBackupPayload{}, errors.New("backup database store unavailable")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return engineBackupPayload{}, err
	}
	defer tx.Rollback()

	payload := engineBackupPayload{
		Kind:          engineBackupKind,
		SchemaVersion: engineBackupSchemaVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Source: map[string]any{
			"engine":          "Borealis",
			"api_version":     version,
			"generated_by":    "api-backend",
			"backup_scope":    "engine_configuration",
			"excluded_scope":  []string{"logs", "device_activity_history", "scheduled_job_history", "workflow_run_history", "watchdog_incident_history", "scheduler_runtime_state"},
		},
		Tables: map[string]engineBackupTable{},
		Files:  map[string]engineBackupFile{},
		Counts: map[string]map[string]int{},
	}
	for _, spec := range engineBackupTableSpecs() {
		if !spec.Export {
			continue
		}
		exists, err := relationExistsTx(ctx, tx, spec.Name)
		if err != nil {
			return engineBackupPayload{}, err
		}
		if !exists {
			continue
		}
		table, err := exportEngineBackupTable(ctx, tx, spec)
		if err != nil {
			return engineBackupPayload{}, err
		}
		payload.Tables[spec.Name] = table
		payload.Counts[spec.Name] = map[string]int{
			"rows":    len(table.Rows),
			"columns": len(table.Columns),
		}
	}
	if err := tx.Commit(); err != nil {
		return engineBackupPayload{}, err
	}
	files, err := exportEngineBackupFiles()
	if err != nil {
		return engineBackupPayload{}, err
	}
	payload.Files = files
	payload.Counts["files"] = map[string]int{"entries": len(files)}
	return payload, nil
}

func exportEngineBackupTable(ctx context.Context, tx *sql.Tx, spec engineBackupTableSpec) (engineBackupTable, error) {
	columns, err := engineBackupTableColumns(ctx, tx, spec.Name)
	if err != nil {
		return engineBackupTable{}, err
	}
	if len(columns) == 0 {
		return engineBackupTable{}, nil
	}
	selectColumns := make([]string, 0, len(columns))
	columnNames := make([]string, 0, len(columns))
	columnByName := map[string]engineBackupColumn{}
	for _, column := range columns {
		selectColumns = append(selectColumns, quoteBackupIdentifier(column.Name))
		columnNames = append(columnNames, column.Name)
		columnByName[column.Name] = column
	}
	orderBy := engineBackupOrderBy(spec, columnByName)
	query := fmt.Sprintf("SELECT %s FROM %s%s", strings.Join(selectColumns, ", "), quoteQualifiedBackupName(spec.Name), orderBy)
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return engineBackupTable{}, err
	}
	defer rows.Close()
	result := engineBackupTable{Columns: columnNames, Rows: []map[string]any{}}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return engineBackupTable{}, err
		}
		row := map[string]any{}
		for i, value := range values {
			row[columns[i].Name] = normalizeEngineBackupScannedValue(value, columns[i])
		}
		result.Rows = append(result.Rows, row)
	}
	return result, rows.Err()
}

func engineBackupTableColumns(ctx context.Context, tx *sql.Tx, qualifiedName string) ([]engineBackupColumn, error) {
	schema, table, ok := splitQualifiedBackupName(qualifiedName)
	if !ok {
		return nil, backupUserError(http.StatusBadRequest, "invalid_table_id", "Backup table identifier is invalid.")
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT column_name, data_type, udt_name
		  FROM information_schema.columns
		 WHERE table_schema=$1
		   AND table_name=$2
		   AND COALESCE(is_generated, 'NEVER')='NEVER'
		 ORDER BY ordinal_position
	`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := []engineBackupColumn{}
	for rows.Next() {
		var column engineBackupColumn
		if err := rows.Scan(&column.Name, &column.DataType, &column.UDTName); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func engineBackupOrderBy(spec engineBackupTableSpec, columns map[string]engineBackupColumn) string {
	parts := []string{}
	for _, name := range spec.OrderBy {
		if _, ok := columns[name]; ok {
			parts = append(parts, quoteBackupIdentifier(name))
		}
	}
	if len(parts) == 0 {
		for name := range columns {
			parts = append(parts, quoteBackupIdentifier(name))
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " ORDER BY " + strings.Join(parts, ", ")
}

func normalizeEngineBackupScannedValue(value any, column engineBackupColumn) any {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []byte:
		if strings.EqualFold(column.UDTName, "bytea") {
			return map[string]any{engineBackupBinaryMarker: base64.StdEncoding.EncodeToString(typed)}
		}
		return string(typed)
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	default:
		return typed
	}
}

func (s *postgresOperatorStore) restoreEngineBackupPayload(ctx context.Context, payload engineBackupPayload) (engineBackupRestoreResult, error) {
	if s == nil || s.db == nil {
		return engineBackupRestoreResult{}, errors.New("backup database store unavailable")
	}
	stagedFiles, err := stageEngineBackupFiles(payload.Files)
	if err != nil {
		return engineBackupRestoreResult{}, err
	}
	committedFiles := false
	defer func() {
		if !committedFiles {
			cleanupEngineBackupStagedFiles(stagedFiles)
		}
	}()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return engineBackupRestoreResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := deleteEngineBackupTargetState(ctx, tx); err != nil {
		return engineBackupRestoreResult{}, err
	}
	result := engineBackupRestoreResult{}
	for _, spec := range engineBackupTableSpecs() {
		if !spec.Restore {
			continue
		}
		table, ok := payload.Tables[spec.Name]
		if !ok {
			continue
		}
		restored, err := importEngineBackupTable(ctx, tx, spec, table)
		if err != nil {
			return engineBackupRestoreResult{}, err
		}
		result.TablesRestored++
		result.RowsRestored += restored
	}
	if err := tx.Commit(); err != nil {
		return engineBackupRestoreResult{}, err
	}
	committed = true
	filesRestored, err := commitEngineBackupFiles(stagedFiles, payload.Files)
	if err != nil {
		return engineBackupRestoreResult{}, err
	}
	committedFiles = true
	result.FilesRestored = filesRestored
	return result, nil
}

func deleteEngineBackupTargetState(ctx context.Context, tx *sql.Tx) error {
	for _, name := range engineBackupDeleteOrder() {
		exists, err := relationExistsTx(ctx, tx, name)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+quoteQualifiedBackupName(name)); err != nil {
			return err
		}
	}
	return nil
}

func importEngineBackupTable(ctx context.Context, tx *sql.Tx, spec engineBackupTableSpec, table engineBackupTable) (int, error) {
	exists, err := relationExistsTx(ctx, tx, spec.Name)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, backupUserError(http.StatusBadRequest, "restore_target_schema_mismatch", "Restore target is missing table "+spec.Name+".")
	}
	targetColumns, err := engineBackupTableColumns(ctx, tx, spec.Name)
	if err != nil {
		return 0, err
	}
	targetSet := map[string]engineBackupColumn{}
	for _, column := range targetColumns {
		targetSet[column.Name] = column
	}
	for _, column := range table.Columns {
		if _, ok := targetSet[column]; !ok {
			return 0, backupUserError(http.StatusBadRequest, "invalid_columns", "Backup table "+spec.Name+" contains unsupported column "+column+".")
		}
	}
	if len(table.Rows) == 0 || len(table.Columns) == 0 {
		return 0, nil
	}
	insertColumns := make([]string, 0, len(table.Columns))
	placeholders := make([]string, 0, len(table.Columns))
	for i, column := range table.Columns {
		insertColumns = append(insertColumns, quoteBackupIdentifier(column))
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
	}
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", quoteQualifiedBackupName(spec.Name), strings.Join(insertColumns, ", "), strings.Join(placeholders, ", "))
	for _, row := range table.Rows {
		for column := range row {
			if _, ok := targetSet[column]; !ok {
				return 0, backupUserError(http.StatusBadRequest, "invalid_columns", "Backup table "+spec.Name+" contains unsupported column "+column+".")
			}
		}
		values := make([]any, 0, len(table.Columns))
		for _, column := range table.Columns {
			values = append(values, engineBackupDBValue(row[column]))
		}
		if _, err := tx.ExecContext(ctx, insertSQL, values...); err != nil {
			return 0, err
		}
	}
	if spec.ResetSerials {
		if err := resetEngineBackupSerial(ctx, tx, spec.Name); err != nil {
			return 0, err
		}
	}
	return len(table.Rows), nil
}

func resetEngineBackupSerial(ctx context.Context, tx *sql.Tx, qualifiedName string) error {
	var sequence sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT pg_get_serial_sequence($1, 'id')", qualifiedName).Scan(&sequence); err != nil {
		return err
	}
	if !sequence.Valid || strings.TrimSpace(sequence.String) == "" {
		return nil
	}
	var maxID sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT MAX(id) FROM "+quoteQualifiedBackupName(qualifiedName)).Scan(&maxID); err != nil {
		return err
	}
	if maxID.Valid && maxID.Int64 > 0 {
		_, err := tx.ExecContext(ctx, "SELECT setval($1, $2, true)", sequence.String, maxID.Int64)
		return err
	}
	_, err := tx.ExecContext(ctx, "SELECT setval($1, 1, false)", sequence.String)
	return err
}

func engineBackupDBValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case json.Number:
		text := typed.String()
		if strings.ContainsAny(text, ".eE") {
			if parsed, err := typed.Float64(); err == nil {
				return parsed
			}
		}
		if parsed, err := typed.Int64(); err == nil {
			return parsed
		}
		return text
	case map[string]any:
		if marker, ok := typed[engineBackupBinaryMarker]; ok && len(typed) == 1 {
			decoded, err := base64.StdEncoding.DecodeString(cleanText(marker))
			if err == nil {
				return decoded
			}
			return []byte{}
		}
		content, _ := json.Marshal(typed)
		return string(content)
	case []any:
		content, _ := json.Marshal(typed)
		return string(content)
	default:
		return typed
	}
}

func exportEngineBackupFiles() (map[string]engineBackupFile, error) {
	files := map[string]engineBackupFile{}
	for _, spec := range engineBackupFileSpecs() {
		path, err := spec.Path()
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(path) == "" {
			continue
		}
		content, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		mode := int(spec.Mode)
		if info, err := os.Stat(path); err == nil {
			mode = int(info.Mode().Perm())
		}
		files[spec.ID] = engineBackupFile{
			ContentB64: base64.StdEncoding.EncodeToString(content),
			Mode:       mode,
		}
	}
	return files, nil
}

type engineBackupFileSpec struct {
	ID   string
	Path func() (string, error)
	Mode fs.FileMode
}

type engineBackupStagedFile struct {
	ID      string
	Path    string
	TmpPath string
	Mode    fs.FileMode
}

func stageEngineBackupFiles(files map[string]engineBackupFile) ([]engineBackupStagedFile, error) {
	specs := engineBackupFileSpecMap()
	staged := []engineBackupStagedFile{}
	for id, file := range files {
		spec, ok := specs[id]
		if !ok {
			return nil, backupUserError(http.StatusBadRequest, "unknown_file_id", "Backup contains unsupported file "+id+".")
		}
		content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(file.ContentB64))
		if err != nil {
			return nil, backupUserError(http.StatusBadRequest, "invalid_file_content", "Backup file "+id+" is not valid Base64.")
		}
		path, err := spec.Path()
		if err != nil {
			return nil, err
		}
		mode := fs.FileMode(file.Mode).Perm()
		if mode == 0 {
			mode = spec.Mode
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".restore-*")
		if err != nil {
			return nil, err
		}
		tmpPath := tmp.Name()
		if _, err := tmp.Write(content); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return nil, err
		}
		if err := tmp.Chmod(mode); err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
			return nil, err
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmpPath)
			return nil, err
		}
		staged = append(staged, engineBackupStagedFile{ID: id, Path: path, TmpPath: tmpPath, Mode: mode})
	}
	return staged, nil
}

func commitEngineBackupFiles(staged []engineBackupStagedFile, files map[string]engineBackupFile) (int, error) {
	stagedByID := map[string]engineBackupStagedFile{}
	for _, file := range staged {
		stagedByID[file.ID] = file
	}
	count := 0
	for _, spec := range engineBackupFileSpecs() {
		if stagedFile, ok := stagedByID[spec.ID]; ok {
			if err := os.Rename(stagedFile.TmpPath, stagedFile.Path); err != nil {
				return count, err
			}
			_ = os.Chmod(stagedFile.Path, stagedFile.Mode)
			count++
			continue
		}
		if _, ok := files[spec.ID]; ok {
			continue
		}
		path, err := spec.Path()
		if err != nil {
			return count, err
		}
		if strings.TrimSpace(path) != "" {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return count, err
			}
		}
	}
	return count, nil
}

func cleanupEngineBackupStagedFiles(staged []engineBackupStagedFile) {
	for _, file := range staged {
		_ = os.Remove(file.TmpPath)
	}
}

func engineBackupTableSpecs() []engineBackupTableSpec {
	return []engineBackupTableSpec{
		{Name: "engine.aegis_cipher_state", OrderBy: []string{"id"}, Export: true, Restore: true},
		{Name: "engine.sites", OrderBy: []string{"id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.directory_providers", OrderBy: []string{"priority", "id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.users", OrderBy: []string{"id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.credentials", OrderBy: []string{"id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.github_token", OrderBy: []string{"id"}, Export: true, Restore: true},
		{Name: "engine.agent_service_account", OrderBy: []string{"agent_id"}, Export: true, Restore: true},
		{Name: "engine.devices", OrderBy: []string{"hostname", "guid"}, Export: true, Restore: true},
		{Name: "engine.device_keys", OrderBy: []string{"guid", "id"}, Export: true, Restore: true},
		{Name: "engine.refresh_tokens", OrderBy: []string{"guid", "id"}, Export: true, Restore: true},
		{Name: "engine.device_purge_barriers", OrderBy: []string{"guid"}, Export: true, Restore: true},
		{Name: "engine.device_vpn_config", OrderBy: []string{"agent_id"}, Export: true, Restore: true},
		{Name: "engine.device_vpn_ip_leases", OrderBy: []string{"agent_id"}, Export: true, Restore: true},
		{Name: "engine.device_vpn_key_leases", OrderBy: []string{"agent_id"}, Export: true, Restore: true},
		{Name: "engine.device_sites", OrderBy: []string{"site_id", "device_hostname"}, Export: true, Restore: true},
		{Name: "engine.device_approvals", OrderBy: []string{"created_at", "id"}, Export: true, Restore: true},
		{Name: "engine.user_passkeys", OrderBy: []string{"user_id", "id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.user_site_assignments", OrderBy: []string{"user_id", "site_id"}, Export: true, Restore: true},
		{Name: "engine.directory_provider_group_mappings", OrderBy: []string{"provider_id", "id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.directory_provider_site_mappings", OrderBy: []string{"provider_id", "position", "id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.device_filters", OrderBy: []string{"id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.device_filter_sites", OrderBy: []string{"filter_id", "site_id"}, Export: true, Restore: true},
		{Name: "engine.device_list_views", OrderBy: []string{"id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.metadata_field_definitions", OrderBy: []string{"field_number"}, Export: true, Restore: true},
		{Name: "engine.device_metadata_fields", OrderBy: []string{"device_guid", "field_number"}, Export: true, Restore: true},
		{Name: "engine.device_software_inventory", OrderBy: []string{"device_guid", "name_normalized", "id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.software_icon_assets", OrderBy: []string{"id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "assemblies.official_catalog_state", OrderBy: []string{"assembly_guid"}, Export: true, Restore: true},
		{Name: "assemblies.official_assemblies", OrderBy: []string{"assembly_guid"}, Export: true, Restore: true},
		{Name: "assemblies.community_assemblies", OrderBy: []string{"assembly_guid"}, Export: true, Restore: true},
		{Name: "assemblies.user_created_assemblies", OrderBy: []string{"assembly_guid"}, Export: true, Restore: true},
		{Name: "engine.scheduled_jobs", OrderBy: []string{"id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.workflow_webhooks", OrderBy: []string{"workflow_guid", "id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.watchdogs", OrderBy: []string{"id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.watchdog_sites", OrderBy: []string{"watchdog_id", "site_id"}, Export: true, Restore: true},
		{Name: "engine.watchdog_targets", OrderBy: []string{"watchdog_id", "id"}, Export: true, Restore: true, ResetSerials: true},
		{Name: "engine.watchdog_device_overrides", OrderBy: []string{"watchdog_id", "device_guid", "id"}, Export: true, Restore: true, ResetSerials: true},
	}
}

func engineBackupDeleteOrder() []string {
	names := []string{
		"engine.job_scheduler_service_snapshots",
		"engine.job_scheduler_worker_routes",
		"engine.job_scheduler_workers",
		"engine.job_scheduler_work_items",
		"engine.scheduled_job_onboarding_target_events",
		"engine.scheduled_job_onboarding_targets",
		"engine.scheduled_job_run_activity",
		"engine.scheduled_job_run_targets",
		"engine.scheduled_job_runs",
		"engine.workflow_child_jobs",
		"engine.workflow_node_runs",
		"engine.workflow_runs",
		"engine.watchdog_device_state",
		"engine.watchdog_incidents",
		"engine.ansible_play_recaps",
		"engine.activity_history",
		"engine.enrollment_code_failures",
		"engine.notifications",
	}
	specs := engineBackupTableSpecs()
	for i := len(specs) - 1; i >= 0; i-- {
		if specs[i].Restore {
			names = append(names, specs[i].Name)
		}
	}
	seen := map[string]bool{}
	ordered := []string{}
	for _, name := range names {
		if !seen[name] {
			ordered = append(ordered, name)
			seen[name] = true
		}
	}
	return ordered
}

func engineBackupRestoreTableSet() map[string]bool {
	result := map[string]bool{}
	for _, spec := range engineBackupTableSpecs() {
		if spec.Restore {
			result[spec.Name] = true
		}
	}
	return result
}

func engineBackupFileSpecs() []engineBackupFileSpec {
	return []engineBackupFileSpec{
		{ID: "engine_secret", Path: engineSecretBackupPath, Mode: 0o600},
		{ID: "agent_jwt_private_key", Path: agentJWTKeyPath, Mode: 0o600},
		{ID: "script_signing_private_key", Path: scriptSigningKeyPath, Mode: 0o600},
		{ID: "script_signing_public_key", Path: scriptSigningPublicKeyPath, Mode: 0o600},
		{ID: "wireguard_server_private_key", Path: wireGuardServerPrivateKeyPath, Mode: 0o600},
		{ID: "wireguard_server_public_key", Path: wireGuardServerPublicKeyPath, Mode: 0o600},
		{ID: "traefik_acme_state", Path: func() (string, error) { return overviewACMEStoragePath(), nil }, Mode: 0o600},
		{ID: "traefik_settings", Path: traefikSettingsBackupPath, Mode: 0o600},
		{ID: "agent_release_channels", Path: func() (string, error) { return agentReleaseChannelsPath(), nil }, Mode: 0o600},
		{ID: "ansible_runner_settings", Path: func() (string, error) { return ansibleRunnerSettingsPath(), nil }, Mode: 0o600},
		{ID: "site_worker_settings", Path: func() (string, error) { return siteWorkerSettingsPath(), nil }, Mode: 0o600},
		{ID: "software_icon_overrides", Path: func() (string, error) { return agentSoftwareIconOverridesPath(), nil }, Mode: 0o644},
		{ID: "software_uninstall_overrides", Path: func() (string, error) { return softwareUninstallOverridesPath(), nil }, Mode: 0o644},
		{ID: "software_uninstall_blocklist", Path: func() (string, error) { return softwareUninstallBlocklistPath(), nil }, Mode: 0o644},
	}
}

func engineBackupFileSpecMap() map[string]engineBackupFileSpec {
	result := map[string]engineBackupFileSpec{}
	for _, spec := range engineBackupFileSpecs() {
		result[spec.ID] = spec
	}
	return result
}

func engineBackupFileSet() map[string]bool {
	result := map[string]bool{}
	for _, spec := range engineBackupFileSpecs() {
		result[spec.ID] = true
	}
	return result
}

func engineSecretBackupPath() (string, error) {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_ENGINE_SECRET_PATH")); value != "" {
		return value, nil
	}
	return "/opt/Borealis/Engine/Services/api-backend/secrets/engine_secret.txt", nil
}

func scriptSigningPublicKeyPath() (string, error) {
	keyPath, err := scriptSigningKeyPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(keyPath), scriptSigningPubFilename), nil
}

func wireGuardServerPrivateKeyPath() (string, error) {
	root := cleanText(os.Getenv("BOREALIS_WIREGUARD_KEY_ROOT"))
	if root == "" {
		engineRoot := firstText(cleanText(os.Getenv("BOREALIS_ENGINE_ROOT")), "/opt/Borealis/Engine")
		root = filepath.Join(engineRoot, "Services", "wireguard-tunnel", "secrets")
	}
	return filepath.Join(root, "server_private.key"), nil
}

func wireGuardServerPublicKeyPath() (string, error) {
	root := cleanText(os.Getenv("BOREALIS_WIREGUARD_KEY_ROOT"))
	if root == "" {
		engineRoot := firstText(cleanText(os.Getenv("BOREALIS_ENGINE_ROOT")), "/opt/Borealis/Engine")
		root = filepath.Join(engineRoot, "Services", "wireguard-tunnel", "secrets")
	}
	return filepath.Join(root, "server_public.key"), nil
}

func traefikSettingsBackupPath() (string, error) {
	if value := strings.TrimSpace(os.Getenv("BOREALIS_TRAEFIK_SETTINGS_PATH")); value != "" {
		return value, nil
	}
	return filepath.Join(projectRoot(), "Engine", "Services", "traefik-edge", "state", "Settings.json"), nil
}

func quoteQualifiedBackupName(qualifiedName string) string {
	schema, table, ok := splitQualifiedBackupName(qualifiedName)
	if !ok {
		return `"".""`
	}
	return quoteBackupIdentifier(schema) + "." + quoteBackupIdentifier(table)
}

func quoteBackupIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func splitQualifiedBackupName(qualifiedName string) (string, string, bool) {
	parts := strings.Split(strings.TrimSpace(qualifiedName), ".")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func backupUserError(status int, code string, message string) error {
	return engineBackupUserError{status: status, code: code, message: message}
}

func engineBackupWriteError(w http.ResponseWriter, err error) {
	var userErr engineBackupUserError
	if errors.As(err, &userErr) {
		writeJSON(w, userErr.status, map[string]any{"error": userErr.code, "message": userErr.message})
		return
	}
	payload, status := aegisErrorBody(err)
	if status != http.StatusBadRequest || errors.Is(err, errAegisInvalidCipher) || errors.Is(err, errAegisNotConfigured) || errors.Is(err, errAegisLocked) {
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "backup_restore_failed", "message": err.Error()})
}
