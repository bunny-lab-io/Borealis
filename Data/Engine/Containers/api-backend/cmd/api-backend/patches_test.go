package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type patchInstallTestStore struct {
	profile operatorProfile
	targets []patchInstallTarget
	status  int
	err     error
	seen    patchInstallLookupRequest
}

type patchProgressEndpointStore struct {
	agentIngestTestStore
	seenDeviceCtx deviceBearerAuthContext
	seenPayload   map[string]any
	result        map[string]any
	status        int
	err           error
}

func (s *patchProgressEndpointStore) recordPatchInstallProgress(_ context.Context, deviceCtx deviceBearerAuthContext, payload map[string]any) (map[string]any, int, error) {
	s.seenDeviceCtx = deviceCtx
	s.seenPayload = payload
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	if s.err != nil {
		return nil, status, s.err
	}
	if s.result != nil {
		return s.result, status, nil
	}
	return normalizePatchInstallProgressPayload(payload), status, nil
}

func (s *patchInstallTestStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *patchInstallTestStore) listPatchInstallTargets(_ context.Context, _ operatorProfile, request patchInstallLookupRequest) ([]patchInstallTarget, int, error) {
	s.seen = request
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	if s.err != nil {
		return nil, status, s.err
	}
	return s.targets, status, nil
}

func patchInstallTestAuth(store *patchInstallTestStore) *authService {
	return &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		store:   store,
		timeout: time.Second,
	}
}

func patchInstallTargetForTest(hostname string, patchKey string, route *agentWorkerRoute) patchInstallTarget {
	return patchInstallTarget{
		Route: route,
		Row: patchInventoryRow{
			ID:              sql.NullInt64{Int64: 42, Valid: true},
			DeviceGUID:      sql.NullString{String: "DEVICE-GUID", Valid: true},
			PatchKey:        sql.NullString{String: patchKey, Valid: true},
			KB:              sql.NullString{String: "KB5000001", Valid: true},
			Title:           sql.NullString{String: "Security Update KB5000001", Valid: true},
			State:           sql.NullString{String: "pending", Valid: true},
			Source:          sql.NullString{String: "wua_pending", Valid: true},
			Classification:  sql.NullString{String: "Security Updates", Valid: true},
			Severity:        sql.NullString{String: "Important", Valid: true},
			CapturedAt:      sql.NullInt64{Int64: 1700000000, Valid: true},
			MetadataJSON:    sql.NullString{String: `{"update_id":"11111111-1111-1111-1111-111111111111","revision_number":4}`, Valid: true},
			GUID:            sql.NullString{String: "DEVICE-GUID", Valid: true},
			Hostname:        sql.NullString{String: hostname, Valid: true},
			AgentID:         sql.NullString{String: hostname + "_SYSTEM", Valid: true},
			OperatingSystem: sql.NullString{String: "Windows 11", Valid: true},
			SiteID:          sql.NullInt64{Int64: 7, Valid: true},
			SiteName:        sql.NullString{String: "Lab", Valid: true},
		},
	}
}

func TestNormalizeAgentPatchInventoryKBAndMetadata(t *testing.T) {
	rows := normalizeAgentPatchInventory([]any{
		map[string]any{
			"title":          "2026-06 Cumulative Update for Windows 11 KB5063060",
			"state":          "ready_to_install",
			"source":         "windows_update_agent",
			"classification": "Security Updates",
			"severity":       "Critical",
			"metadata": map[string]any{
				"is_downloaded":   true,
				"is_mandatory":    false,
				"requires_reboot": true,
				"update_id":       "UPDATE-GUID-1",
				"revision_number": float64(201),
			},
		},
	})

	if len(rows) != 1 {
		t.Fatalf("expected one normalized patch, got %d", len(rows))
	}
	row := rows[0]
	if row["kb"] != "KB5063060" || row["state"] != "pending" || row["source"] != "wua_pending" {
		t.Fatalf("unexpected normalized row %#v", row)
	}
	if row["patch_key"] != "kb:KB5063060:state:pending" {
		t.Fatalf("unexpected patch key %q", row["patch_key"])
	}
	metadata, _ := row["metadata"].(map[string]any)
	if metadata["is_downloaded"] != true || metadata["requires_reboot"] != true || metadata["update_id"] != "UPDATE-GUID-1" {
		t.Fatalf("unexpected metadata %#v", metadata)
	}
}

func TestNormalizeAgentPatchInventoryDedupesInstalledSources(t *testing.T) {
	rows := normalizeAgentPatchInventory([]map[string]any{
		{
			"kb":           "KB5010001",
			"title":        "Security Update for Windows",
			"state":        "installed",
			"source":       "wua_history",
			"installed_on": int64(1700000100),
		},
		{
			"hotfix_id":    "5010001",
			"title":        "Security Update for Windows 11 KB5010001",
			"state":        "succeeded",
			"source":       "quickfixengineering",
			"installed_on": int64(1700000200),
		},
	})

	if len(rows) != 1 {
		t.Fatalf("expected deduped installed row, got %#v", rows)
	}
	row := rows[0]
	if row["kb"] != "KB5010001" || row["state"] != "installed" || row["source"] != "quick_fix_engineering" {
		t.Fatalf("unexpected deduped row %#v", row)
	}
	if row["installed_on"] != int64(1700000100) {
		t.Fatalf("expected first installed timestamp preserved, got %#v", row["installed_on"])
	}
}

func TestPatchInventoryRowKeyFallsBackToUpdateIdentityAndTitleHash(t *testing.T) {
	updateRows := normalizeAgentPatchInventory([]map[string]any{
		{
			"title":  "Servicing Stack Update",
			"state":  "pending",
			"source": "wua_pending",
			"metadata": map[string]any{
				"update_id":       "ABCDEF",
				"revision_number": 42,
			},
		},
	})
	if len(updateRows) != 1 || updateRows[0]["patch_key"] != "update:abcdef:42:state:pending" {
		t.Fatalf("unexpected update identity fallback %#v", updateRows)
	}

	titleRows := normalizeAgentPatchInventory([]map[string]any{
		{"title": "Driver Update Without KB", "state": "pending", "source": "wua_pending"},
	})
	if len(titleRows) != 1 {
		t.Fatalf("expected title fallback row, got %#v", titleRows)
	}
	key := cleanText(titleRows[0]["patch_key"])
	if !strings.HasPrefix(key, "title:") || !strings.HasSuffix(key, ":state:pending") {
		t.Fatalf("unexpected title fallback key %q", key)
	}
}

func TestPatchInventorySignatureIgnoresCapturedAt(t *testing.T) {
	left := []map[string]any{
		{"kb": "KB5010001", "title": "Security Update", "state": "installed", "source": "wua_history", "captured_at": int64(1700000100)},
	}
	right := []map[string]any{
		{"kb": "5010001", "title": "Security Update", "state": "success", "source": "history", "captured_at": int64(1700000999)},
	}
	if patchInventorySignature(left) != patchInventorySignature(right) {
		t.Fatalf("expected signatures to ignore captured_at")
	}
}

func TestPatchActiveIdentityKeysIncludeWUAIdentityAndTitleKB(t *testing.T) {
	keys := patchActiveIdentityKeys(map[string]any{
		"patch_key": "update:abcdef:42:state:pending",
		"title":     "SQL Server 2017 RTM Azure Connect Pack KB5050533",
		"metadata": map[string]any{
			"update_id":       "ABCDEF",
			"revision_number": 42,
		},
	})
	seen := map[string]bool{}
	for _, key := range keys {
		seen[key] = true
	}
	for _, want := range []string{
		"patch:update:abcdef:42:state:pending",
		"kb:KB5050533",
		"update:abcdef:42",
		"update:abcdef",
		"title:sql server 2017 rtm azure connect pack kb5050533",
	} {
		if !seen[want] {
			t.Fatalf("missing active identity %q from %#v", want, keys)
		}
	}
}

func TestSchedulerPatchInstallOutputShowsWUAResultDetails(t *testing.T) {
	stdout, stderr, errorText := schedulerPatchInstallOutput(
		"patch-job-266-run-6925",
		"LAB-CAMERA-01",
		"KB5087051 - 2026-05 .NET Framework Security Update (KB5087051)",
		map[string]any{"kb": "KB5087051", "patch_key": "kb:KB5087051:state:pending"},
		map[string]any{
			"ok":                             true,
			"status":                         "completed",
			"result_code":                    int64(2),
			"result_code_name":               "Succeeded",
			"reboot_required":                true,
			"reboot_required_before_install": false,
			"installed_count":                int64(1),
			"already_installed":              true,
		},
	)
	if stderr != "" || errorText != "" {
		t.Fatalf("unexpected stderr=%q error=%q", stderr, errorText)
	}
	for _, want := range []string{
		"WUA result_code=2 (Succeeded)",
		"Reboot required=true",
		"Reboot required before install=false",
		"Installed update count=1",
		"Already installed=true",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestPatchInstallProgressEndpointUsesDeviceAuth(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	token, err := signer.issueAccessToken(guid, "fingerprint", 4, agentAccessTokenTTL)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	store := &patchProgressEndpointStore{
		agentIngestTestStore: agentIngestTestStore{
			deviceAuthFound: true,
			deviceAuthRecord: deviceBearerAuthRecord{
				GUID:         guid,
				Fingerprint:  "fingerprint",
				TokenVersion: 4,
				Status:       "active",
			},
		},
	}
	auth := &authService{
		verifier: &tokenVerifier{secret: []byte("test-secret"), maxAge: time.Hour, now: time.Now},
		store:    store,
		timeout:  time.Second,
	}
	body := `{"scheduled_job_id":9,"scheduled_job_run_id":12,"phase":"install","percent":42,"kb":"KB5000001","captured_at":1783000999}`
	request := httptest.NewRequest(http.MethodPost, patchInstallProgressRoutePath, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()

	agentPatchInstallProgressHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected progress ok, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.seenDeviceCtx.GUID != normalizeCanonicalGUID(guid) {
		t.Fatalf("device auth context not passed: %#v", store.seenDeviceCtx)
	}
	if store.seenPayload["phase"] != "install" || coerceInt64(store.seenPayload["percent"]) != 42 {
		t.Fatalf("progress payload not passed: %#v", store.seenPayload)
	}
}

func TestPatchProgressFromActivitiesUsesLatestMetadata(t *testing.T) {
	activities := []map[string]any{
		{"metadata": map[string]any{"patch_progress": map[string]any{"phase": "download", "percent": int64(25), "captured_at": int64(100)}}},
		{"metadata": map[string]any{"patch_progress": map[string]any{"phase": "install", "percent": int64(42), "captured_at": int64(200)}}},
	}
	progress := patchProgressFromActivities(activities)
	if progress == nil {
		t.Fatalf("expected patch progress")
	}
	if progress["phase"] != "install" || progress["display_label"] != "Installing 42%" {
		t.Fatalf("unexpected patch progress %#v", progress)
	}
}

func TestPatchRefreshHandlerQueuesWorkerEvent(t *testing.T) {
	var sawEvent bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("worker request body decode failed: %v", err)
		}
		if r.URL.Path != "/remote-ops/host-service/event" {
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
		sawEvent = true
		if body["hostname"] != "LAB-OPERATOR-01" || body["service_mode"] != "system" || body["event_name"] != "patch_inventory_refresh_request" {
			t.Fatalf("unexpected event body %#v", body)
		}
		if body["allow_pending"] != true || body["pending_ttl_seconds"].(float64) != 180 {
			t.Fatalf("unexpected pending flags %#v", body)
		}
		payload, _ := body["payload"].(map[string]any)
		if payload["requested_by"] != "operator" || payload["reason"] != "operator_query_patch_inventory" || payload["agent_id"] != "LAB-OPERATOR-01_SYSTEM" {
			t.Fatalf("unexpected refresh payload %#v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"emitted": true})
	}))
	defer worker.Close()

	store := &fakeProcessStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: deviceProcessContext{
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Route:    routeForTestWorker(t, worker.URL),
		},
	}
	mux := http.NewServeMux()
	registerPatchRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/patches/LAB-OPERATOR-01/refresh", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawEvent {
		t.Fatalf("expected worker event")
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if payload["status"] != "queued" || payload["hostname"] != "LAB-OPERATOR-01" || payload["agent_id"] != "LAB-OPERATOR-01_SYSTEM" {
		t.Fatalf("unexpected refresh response %#v", payload)
	}
}

func TestPatchInstallPublicRoutesAreSchedulerOnly(t *testing.T) {
	store := &patchInstallTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerPatchRoutes(mux, patchInstallTestAuth(store), http.NotFoundHandler())

	for _, path := range []string{"/api/device/patches/LAB-OPERATOR-01/install", "/api/patches/install"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"patch_key":"kb:KB5000001:state:pending"}`))
		request.Header.Set("Authorization", "Bearer "+testAuthToken)
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("expected scheduler-only route %s to be unregistered, got %d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
	if store.seen.hasPatchIdentity() {
		t.Fatalf("direct install route should not resolve patch targets: %#v", store.seen)
	}
}

func TestEngineBackupIncludesPatchInventory(t *testing.T) {
	specs := engineBackupTableSpecs()
	found := false
	for _, spec := range specs {
		if spec.Name != "engine.device_patch_inventory" {
			continue
		}
		found = true
		if !spec.Export || !spec.Restore || !spec.ResetSerials {
			t.Fatalf("unexpected patch inventory backup spec %#v", spec)
		}
	}
	if !found {
		t.Fatalf("missing patch inventory backup spec")
	}

	payload := engineBackupPayload{
		Tables: map[string]engineBackupTable{
			"engine.device_patch_inventory": {
				Rows: []map[string]any{{"patch_key": "kb:KB5010001:state:installed"}},
			},
		},
	}
	analysis := analyzeEngineBackupPayload(payload)
	summaryRows, _ := analysis["summary"].([]engineBackupAnalysisRow)
	for _, row := range summaryRows {
		if row.ID == "patch_inventory" && row.Count == 1 {
			return
		}
	}
	t.Fatalf("missing patch inventory summary row %#v", analysis["summary"])
}
