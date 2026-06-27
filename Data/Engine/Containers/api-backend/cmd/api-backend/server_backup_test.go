package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type engineBackupTestStore struct {
	profile  operatorProfile
	payload  engineBackupPayload
	restored *engineBackupPayload
}

func (s *engineBackupTestStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *engineBackupTestStore) exportEngineBackupPayload(_ context.Context) (engineBackupPayload, error) {
	return s.payload, nil
}

func (s *engineBackupTestStore) restoreEngineBackupPayload(_ context.Context, payload engineBackupPayload) (engineBackupRestoreResult, error) {
	copyPayload := payload
	s.restored = &copyPayload
	rows := 0
	for _, table := range payload.Tables {
		rows += len(table.Rows)
	}
	return engineBackupRestoreResult{TablesRestored: len(payload.Tables), RowsRestored: rows, FilesRestored: len(payload.Files), LogsCleared: 3}, nil
}

type engineBackupTestAegis struct {
	key        []byte
	state      aegisState
	clearCount int
	locked     bool
}

func (a *engineBackupTestAegis) status(_ context.Context) (map[string]any, error) {
	return map[string]any{"configured": true, "locked": a.locked}, nil
}

func (a *engineBackupTestAegis) setupWithCipher(_ context.Context, cipherText string) (map[string]any, error) {
	return map[string]any{"configured": true, "locked": false}, nil
}

func (a *engineBackupTestAegis) unlockWithCipher(_ context.Context, cipherText string) (map[string]any, error) {
	return map[string]any{"configured": true, "locked": false}, nil
}

func (a *engineBackupTestAegis) rotateWithCipher(_ context.Context, currentCipher string, newCipher string) (map[string]any, error) {
	return map[string]any{"configured": true, "locked": false}, nil
}

func (a *engineBackupTestAegis) forceReset(_ context.Context) (map[string]any, error) {
	return map[string]any{"configured": false, "locked": false}, nil
}

func (a *engineBackupTestAegis) decryptSecretText(_ context.Context, value any) (string, error) {
	return cleanText(value), nil
}

func (a *engineBackupTestAegis) encryptSecretText(_ context.Context, value string) (string, error) {
	return value, nil
}

func (a *engineBackupTestAegis) engineBackupExportKey(_ context.Context) ([]byte, aegisState, error) {
	if a.locked {
		return nil, aegisState{}, errAegisLocked
	}
	return append([]byte(nil), a.key...), a.state, nil
}

func (a *engineBackupTestAegis) engineBackupRestoreKey(_ context.Context, cipherText string, document encryptedEngineBackupDocument) ([]byte, error) {
	if strings.TrimSpace(cipherText) != "correct-cipher" {
		return nil, errAegisInvalidCipher
	}
	return append([]byte(nil), a.key...), nil
}

func (a *engineBackupTestAegis) engineBackupClearActiveKey() {
	a.clearCount++
}

func newEngineBackupTestAuth(store *engineBackupTestStore, aegis *engineBackupTestAegis, phase string) *authService {
	return &authService{
		verifier:      &tokenVerifier{secret: []byte("test-secret"), maxAge: time.Hour, now: time.Now},
		store:         store,
		bootstrapGate: &authLoginTestGate{state: map[string]any{"phase": phase, "configured": true, "locked": false}},
		aegis:         aegis,
		timeout:       time.Second,
	}
}

func addEngineBackupAuthCookie(t *testing.T, request *http.Request, auth *authService, role string) {
	t.Helper()
	token, err := auth.verifier.signPayload(map[string]any{"u": "operator", "r": role})
	if err != nil {
		t.Fatalf("sign auth token: %v", err)
	}
	request.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
}

func engineBackupTestState(t *testing.T, key []byte) aegisState {
	t.Helper()
	token, err := aegisEncryptText(aegisVerificationPlaintext, key)
	if err != nil {
		t.Fatalf("encrypt verification token: %v", err)
	}
	return aegisState{
		Configured:        true,
		KDFName:           aegisKDFName,
		KDFParamsJSON:     `{"salt_b64":"dGVzdC1zYWx0","n":32768,"r":8,"p":1,"length":32}`,
		VerificationToken: token,
	}
}

func engineBackupTestPayload(t *testing.T, state aegisState) engineBackupPayload {
	t.Helper()
	return engineBackupPayload{
		Kind:          engineBackupKind,
		SchemaVersion: engineBackupSchemaVersion,
		Tables: map[string]engineBackupTable{
			"engine.aegis_cipher_state": {
				Columns: []string{"id", "kdf_name", "kdf_params_json", "verification_token"},
				Rows: []map[string]any{{
					"id":                 json.Number("1"),
					"kdf_name":           state.KDFName,
					"kdf_params_json":    state.KDFParamsJSON,
					"verification_token": state.VerificationToken,
				}},
			},
			"engine.directory_providers": {
				Columns: []string{"id", "name", "server_urls_json", "bind_password_encrypted"},
				Rows: []map[string]any{{
					"id":                      json.Number("7"),
					"name":                    "Lab LDAP",
					"server_urls_json":        `["ldaps://ldap.example.test"]`,
					"bind_password_encrypted": "aegis:v1:directory-secret",
				}},
			},
			"engine.sites": {
				Columns: []string{"id", "name", "enrollment_code"},
				Rows: []map[string]any{{
					"id":              json.Number("4"),
					"name":            "Bunny Lab",
					"enrollment_code": "SITE-CODE",
				}},
			},
			"engine.devices": {
				Columns: []string{"guid", "hostname", "agent_id"},
				Rows: []map[string]any{{
					"guid":     "agent-guid-1",
					"hostname": "LAB-AGENT-01",
					"agent_id": "agent-id-1",
				}},
			},
		},
		Files: map[string]engineBackupFile{
			"engine_secret": {ContentB64: "c2VjcmV0", Mode: 0o600},
		},
		Counts: map[string]map[string]int{},
	}
}

func encryptedEngineBackupTestDocument(t *testing.T, payload engineBackupPayload, key []byte, state aegisState) encryptedEngineBackupDocument {
	t.Helper()
	doc, err := encryptEngineBackupPayload(payload, key, state)
	if err != nil {
		t.Fatalf("encrypt backup: %v", err)
	}
	return doc
}

func engineBackupRestoreJSON(t *testing.T, doc encryptedEngineBackupDocument, cipher string, confirmation string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"cipher":       cipher,
		"confirmation": confirmation,
		"backup":       doc,
	})
	if err != nil {
		t.Fatalf("marshal restore request: %v", err)
	}
	return string(body)
}

func engineBackupAnalyzeJSON(t *testing.T, doc encryptedEngineBackupDocument, cipher string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"cipher": cipher,
		"backup": doc,
	})
	if err != nil {
		t.Fatalf("marshal analyze request: %v", err)
	}
	return string(body)
}

func TestEngineBackupExportRejectsNonAdmin(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	state := engineBackupTestState(t, key)
	store := &engineBackupTestStore{profile: operatorProfile{Username: "operator", Role: "User"}, payload: engineBackupTestPayload(t, state)}
	auth := newEngineBackupTestAuth(store, &engineBackupTestAegis{key: key, state: state}, bootstrapPhaseLoginRequired)
	request := httptest.NewRequest(http.MethodGet, "/api/server/backup/export", nil)
	addEngineBackupAuthCookie(t, request, auth, "User")
	recorder := httptest.NewRecorder()

	engineBackupExportHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden for non-admin, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestEngineBackupExportRejectsLockedAegis(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	state := engineBackupTestState(t, key)
	store := &engineBackupTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, payload: engineBackupTestPayload(t, state)}
	auth := newEngineBackupTestAuth(store, &engineBackupTestAegis{key: key, state: state, locked: true}, bootstrapPhaseLoginRequired)
	request := httptest.NewRequest(http.MethodGet, "/api/server/backup/export", nil)
	addEngineBackupAuthCookie(t, request, auth, "Admin")
	recorder := httptest.NewRecorder()

	engineBackupExportHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusLocked || !strings.Contains(recorder.Body.String(), "locked") {
		t.Fatalf("expected locked Aegis rejection, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestEngineBackupExportEncryptsPlaintext(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	state := engineBackupTestState(t, key)
	sourcePayload := engineBackupTestPayload(t, state)
	store := &engineBackupTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, payload: sourcePayload}
	auth := newEngineBackupTestAuth(store, &engineBackupTestAegis{key: key, state: state}, bootstrapPhaseLoginRequired)
	request := httptest.NewRequest(http.MethodGet, "/api/server/backup/export", nil)
	addEngineBackupAuthCookie(t, request, auth, "Admin")
	recorder := httptest.NewRecorder()

	engineBackupExportHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected export ok, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "\n  \"schema_version\":") || !strings.HasSuffix(body, "\n") {
		t.Fatalf("backup response should be indented JSON, got %s", body)
	}
	for _, plaintext := range []string{"ldaps://ldap.example.test", "LAB-AGENT-01", "SITE-CODE", "directory-secret"} {
		if strings.Contains(body, plaintext) {
			t.Fatalf("backup response leaked plaintext %q: %s", plaintext, body)
		}
	}
	for _, redundantField := range []string{"backup_scope", "excluded_scope", "generated_by", "api_version", "encryption", "kdf_name", "source", "created_at"} {
		if strings.Contains(body, `"`+redundantField+`"`) {
			t.Fatalf("backup response included redundant field %q: %s", redundantField, body)
		}
	}
	var doc encryptedEngineBackupDocument
	if err := json.Unmarshal(recorder.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode backup document: %v", err)
	}
	restored, err := decryptEngineBackupPayload(doc, key)
	if err != nil {
		t.Fatalf("decrypt exported backup: %v", err)
	}
	if restored.Tables["engine.devices"].Rows[0]["hostname"] != "LAB-AGENT-01" {
		t.Fatalf("decrypted payload missing device row: %#v", restored.Tables["engine.devices"].Rows)
	}
}

func TestEngineBackupAnalyzeReturnsImportSummary(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	state := engineBackupTestState(t, key)
	payload := engineBackupTestPayload(t, state)
	payload.Tables["engine.device_filters"] = engineBackupTable{Columns: []string{"id", "name"}, Rows: []map[string]any{{"id": json.Number("8"), "name": "Servers"}}}
	payload.Tables["engine.watchdogs"] = engineBackupTable{Columns: []string{"id", "name"}, Rows: []map[string]any{{"id": json.Number("9"), "name": "CPU"}}}
	payload.Tables["engine.scheduled_jobs"] = engineBackupTable{Columns: []string{"id", "name"}, Rows: []map[string]any{{"id": json.Number("10"), "name": "Patch"}}}
	payload.Tables["assemblies.user_created_assemblies"] = engineBackupTable{Columns: []string{"assembly_guid", "name"}, Rows: []map[string]any{{"assembly_guid": "asm-1", "name": "Restart Service"}}}
	payload.Tables["engine.credentials"] = engineBackupTable{Columns: []string{"id", "name"}, Rows: []map[string]any{{"id": json.Number("11"), "name": "Domain Admin"}}}
	payload.Tables["engine.users"] = engineBackupTable{Columns: []string{"id", "username"}, Rows: []map[string]any{{"id": json.Number("12"), "username": "operator"}}}
	payload.Tables["engine.metadata_field_definitions"] = engineBackupTable{Columns: []string{"field_number", "display_name"}, Rows: []map[string]any{{"field_number": json.Number("1"), "display_name": "Asset Tag"}}}
	payload.Tables["engine.device_keys"] = engineBackupTable{Columns: []string{"id", "guid"}, Rows: []map[string]any{{"id": "key-1", "guid": "agent-guid-1"}}}
	payload.Tables["engine.refresh_tokens"] = engineBackupTable{Columns: []string{"id", "guid"}, Rows: []map[string]any{{"id": "token-1", "guid": "agent-guid-1"}}}
	payload.Tables["engine.device_approvals"] = engineBackupTable{Columns: []string{"id", "guid"}, Rows: []map[string]any{{"id": json.Number("14"), "guid": "agent-guid-1"}}}
	payload.Tables["engine.device_metadata_fields"] = engineBackupTable{Columns: []string{"device_guid", "field_number", "value"}, Rows: []map[string]any{{"device_guid": "agent-guid-1", "field_number": json.Number("1"), "value": "A-100"}}}
	doc := encryptedEngineBackupTestDocument(t, payload, key, state)
	store := &engineBackupTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth := newEngineBackupTestAuth(store, &engineBackupTestAegis{key: key, state: state}, bootstrapPhaseLoginRequired)
	request := httptest.NewRequest(http.MethodPost, "/api/server/backup/analyze", strings.NewReader(engineBackupAnalyzeJSON(t, doc, "correct-cipher")))
	addEngineBackupAuthCookie(t, request, auth, "Admin")
	recorder := httptest.NewRecorder()

	engineBackupAnalyzeHandler(auth, false).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected analyze ok, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode analyze response: %v", err)
	}
	analysis := asMap(response["analysis"])
	rows := jsonArray(analysis["summary"])
	counts := map[string]int{}
	for _, row := range rows {
		item := asMap(row)
		counts[cleanText(item["id"])] = int(coerceInt64(item["count"]))
	}
	for id, expected := range map[string]int{
		"devices":         1,
		"filters":         1,
		"watchdogs":       1,
		"scheduled_jobs":  1,
		"assemblies":      1,
		"sites":           1,
		"credentials":     1,
		"users":           1,
		"metadata_fields": 1,
	} {
		if counts[id] != expected {
			t.Fatalf("expected %s count %d, got %d in %#v", id, expected, counts[id], rows)
		}
	}
	for _, hiddenID := range []string{"device_keys", "refresh_tokens", "device_approvals", "metadata_values"} {
		if _, ok := counts[hiddenID]; ok {
			t.Fatalf("analysis should not display %s rows: %#v", hiddenID, rows)
		}
	}
	if counts["files"] != 1 {
		t.Fatalf("expected files count 1, got %d in %#v", counts["files"], rows)
	}
	foundFilesLabel := false
	for _, row := range rows {
		item := asMap(row)
		if cleanText(item["id"]) == "files" {
			foundFilesLabel = cleanText(item["name"]) == "Engine Settings and Secret Files"
			break
		}
	}
	if !foundFilesLabel {
		t.Fatalf("analysis should use human-friendly file label: %#v", rows)
	}
	if store.restored != nil {
		t.Fatalf("analyze must not restore payload")
	}
}

func TestEngineBackupBootstrapRestoreOnlyBeforeLoginRequired(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	state := engineBackupTestState(t, key)
	payload := engineBackupTestPayload(t, state)
	doc := encryptedEngineBackupTestDocument(t, payload, key, state)
	store := &engineBackupTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth := newEngineBackupTestAuth(store, &engineBackupTestAegis{key: key, state: state}, bootstrapPhaseLoginRequired)
	request := httptest.NewRequest(http.MethodPost, "/api/bootstrap/backup/restore", strings.NewReader(engineBackupRestoreJSON(t, doc, "correct-cipher", engineBackupConfirmationText)))
	recorder := httptest.NewRecorder()

	engineBackupRestoreHandler(auth, true).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "bootstrap_restore_unavailable") {
		t.Fatalf("expected bootstrap restore conflict after login enabled, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestEngineBackupRestoreRejectsWrongCipher(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	state := engineBackupTestState(t, key)
	payload := engineBackupTestPayload(t, state)
	doc := encryptedEngineBackupTestDocument(t, payload, key, state)
	store := &engineBackupTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth := newEngineBackupTestAuth(store, &engineBackupTestAegis{key: key, state: state}, bootstrapPhaseAegisSetupRequired)
	request := httptest.NewRequest(http.MethodPost, "/api/bootstrap/backup/restore", strings.NewReader(engineBackupRestoreJSON(t, doc, "wrong-cipher", engineBackupConfirmationText)))
	recorder := httptest.NewRecorder()

	engineBackupRestoreHandler(auth, true).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), "invalid_cipher") {
		t.Fatalf("expected invalid cipher, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.restored != nil {
		t.Fatalf("restore should not run after wrong cipher")
	}
}

func TestEngineBackupRestoreRejectsUnknownIDs(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	state := engineBackupTestState(t, key)
	payload := engineBackupTestPayload(t, state)
	payload.Tables["engine.unknown_config"] = engineBackupTable{Columns: []string{"id"}, Rows: []map[string]any{{"id": json.Number("1")}}}
	doc := encryptedEngineBackupTestDocument(t, payload, key, state)
	store := &engineBackupTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth := newEngineBackupTestAuth(store, &engineBackupTestAegis{key: key, state: state}, bootstrapPhaseAegisSetupRequired)
	request := httptest.NewRequest(http.MethodPost, "/api/bootstrap/backup/restore", strings.NewReader(engineBackupRestoreJSON(t, doc, "correct-cipher", engineBackupConfirmationText)))
	recorder := httptest.NewRecorder()

	engineBackupRestoreHandler(auth, true).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unknown_table_id") {
		t.Fatalf("expected unknown table rejection, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.restored != nil {
		t.Fatalf("restore should not run after unknown IDs")
	}
}

func TestEngineBackupBootstrapRestoreImportsAndClearsKey(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	state := engineBackupTestState(t, key)
	payload := engineBackupTestPayload(t, state)
	doc := encryptedEngineBackupTestDocument(t, payload, key, state)
	store := &engineBackupTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	aegis := &engineBackupTestAegis{key: key, state: state}
	auth := newEngineBackupTestAuth(store, aegis, bootstrapPhaseAegisSetupRequired)
	request := httptest.NewRequest(http.MethodPost, "/api/bootstrap/backup/restore", strings.NewReader(engineBackupRestoreJSON(t, doc, "correct-cipher", engineBackupConfirmationText)))
	recorder := httptest.NewRecorder()

	engineBackupRestoreHandler(auth, true).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected restore ok, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.restored == nil || store.restored.Tables["engine.sites"].Rows[0]["name"] != "Bunny Lab" {
		t.Fatalf("restore payload was not imported: %#v", store.restored)
	}
	if aegis.clearCount != 1 {
		t.Fatalf("expected active Aegis key clear after restore, got %d", aegis.clearCount)
	}
	if !strings.Contains(recorder.Body.String(), `"restart_required":true`) {
		t.Fatalf("restore response must require restart: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"logs_cleared":3`) {
		t.Fatalf("restore response must report cleared logs: %s", recorder.Body.String())
	}
}

func TestEngineBackupClearRuntimeLogsOnlyAllowsEngineServiceLogRoots(t *testing.T) {
	root := t.TempDir()
	logRoot := filepath.Join(root, "Engine", "Services", "api-backend", "logs")
	if err := os.MkdirAll(filepath.Join(logRoot, "VPN_Tunnel"), 0o755); err != nil {
		t.Fatalf("create log root: %v", err)
	}
	for _, path := range []string{
		filepath.Join(logRoot, "engine.log"),
		filepath.Join(logRoot, "engine.log.2026-06-01"),
		filepath.Join(logRoot, "VPN_Tunnel", "tunnel.log"),
	} {
		if err := os.WriteFile(path, []byte("log\n"), 0o600); err != nil {
			t.Fatalf("write log %s: %v", path, err)
		}
	}
	cleared, err := clearEngineBackupLogRoot(logRoot)
	if err != nil {
		t.Fatalf("clear engine log root: %v", err)
	}
	if cleared != 3 {
		t.Fatalf("expected 3 log entries cleared, got %d", cleared)
	}
	if entries, err := os.ReadDir(logRoot); err != nil || len(entries) != 0 {
		t.Fatalf("expected empty log root, entries=%v err=%v", entries, err)
	}

	unsafeRoot := filepath.Join(root, "logs")
	if err := os.MkdirAll(unsafeRoot, 0o755); err != nil {
		t.Fatalf("create unsafe log root: %v", err)
	}
	unsafeFile := filepath.Join(unsafeRoot, "keep.log")
	if err := os.WriteFile(unsafeFile, []byte("keep\n"), 0o600); err != nil {
		t.Fatalf("write unsafe log: %v", err)
	}
	cleared, err = clearEngineBackupLogRoot(unsafeRoot)
	if err != nil {
		t.Fatalf("unsafe log root should be ignored without error: %v", err)
	}
	if cleared != 0 {
		t.Fatalf("unsafe log root should not clear files, got %d", cleared)
	}
	if _, err := os.Stat(unsafeFile); err != nil {
		t.Fatalf("unsafe log file should remain: %v", err)
	}
}

func TestEngineBackupSoftwareIconAssetsUseIconHashKey(t *testing.T) {
	for _, spec := range engineBackupTableSpecs() {
		if spec.Name != "engine.software_icon_assets" {
			continue
		}
		if spec.ResetSerials {
			t.Fatalf("software_icon_assets has no id serial and must not reset serials")
		}
		if len(spec.OrderBy) != 1 || spec.OrderBy[0] != "icon_hash" {
			t.Fatalf("software_icon_assets must order by icon_hash, got %#v", spec.OrderBy)
		}
		return
	}
	t.Fatalf("software_icon_assets backup spec missing")
}

func TestEngineBackupTrustSpecsExcludeApprovalsAndLimitActiveRows(t *testing.T) {
	specs := map[string]engineBackupTableSpec{}
	for _, spec := range engineBackupTableSpecs() {
		specs[spec.Name] = spec
	}

	approvals := specs["engine.device_approvals"]
	if approvals.Export || approvals.Restore {
		t.Fatalf("device approvals should be cleanup-only, got export=%v restore=%v", approvals.Export, approvals.Restore)
	}
	if !engineBackupKnownTableSet()["engine.device_approvals"] {
		t.Fatalf("device approvals should remain known so older backups validate")
	}

	deviceKeys := specs["engine.device_keys"]
	if deviceKeys.LatestPartitionBy != "guid" || len(deviceKeys.ActiveNullColumns) != 1 || deviceKeys.ActiveNullColumns[0] != "retired_at" {
		t.Fatalf("device keys must export latest active row per guid, got %#v", deviceKeys)
	}

	refreshTokens := specs["engine.refresh_tokens"]
	if refreshTokens.LatestPartitionBy != "guid" || len(refreshTokens.ActiveNullColumns) != 1 || refreshTokens.ActiveNullColumns[0] != "revoked_at" {
		t.Fatalf("refresh tokens must export latest active row per guid, got %#v", refreshTokens)
	}
	if len(refreshTokens.ActiveAfterNow) != 1 || refreshTokens.ActiveAfterNow[0] != "expires_at" {
		t.Fatalf("refresh tokens must skip expired rows, got %#v", refreshTokens.ActiveAfterNow)
	}
}
