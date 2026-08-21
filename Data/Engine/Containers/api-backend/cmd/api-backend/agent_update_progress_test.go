package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type agentUpdateProgressTestStore struct {
	deviceAuthRecord deviceBearerAuthRecord
	progressGUID     string
	progressRequest  agentUpdateProgressRequest
	historyProfile   operatorProfile
	historyGUID      string
	historyOperation string
	historyLimit     int
}

func (s *agentUpdateProgressTestStore) requiredDeviceTokenVersion(_ context.Context, _ string) (*int, error) {
	return nil, nil
}

func (s *agentUpdateProgressTestStore) deviceBearerAuthRecord(_ context.Context, _ string) (deviceBearerAuthRecord, bool, error) {
	return s.deviceAuthRecord, true, nil
}

func (s *agentUpdateProgressTestStore) lookupOperator(_ context.Context, username string, role string) (operatorProfile, error) {
	return operatorProfile{Username: username, DisplayName: username, Role: role, AuthSource: "local"}, nil
}

func (s *agentUpdateProgressTestStore) recordAgentUpdateProgress(_ context.Context, deviceCtx deviceBearerAuthContext, request agentUpdateProgressRequest) (map[string]any, int, error) {
	s.progressGUID = deviceCtx.GUID
	s.progressRequest = request
	return map[string]any{
		"status":                   "running",
		"operation_id":             request.OperationID,
		"scheduled_job_id":         int64(41),
		"scheduled_job_run_id":     int64(42),
		"engine_receive_timestamp": int64(1700000010),
	}, http.StatusOK, nil
}

func (s *agentUpdateProgressTestStore) agentUpdateHistory(_ context.Context, profile operatorProfile, deviceGUID string, operationID string, limit int) (map[string]any, int, error) {
	s.historyProfile = profile
	s.historyGUID = deviceGUID
	s.historyOperation = operationID
	s.historyLimit = limit
	return map[string]any{
		"status":      "ok",
		"device_guid": deviceGUID,
		"operations":  []any{},
	}, http.StatusOK, nil
}

func TestAgentUpdateProgressHandlerAuthenticatesNormalizesAndPersists(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }
	token, err := signer.issueAccessToken(guid, "fingerprint", 4, agentAccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	store := &agentUpdateProgressTestStore{deviceAuthRecord: deviceBearerAuthRecord{
		GUID: guid, Fingerprint: "fingerprint", TokenVersion: 4, Status: "active",
	}}
	auth := &authService{store: store, timeout: time.Second}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/update/progress", strings.NewReader(`{
		"operation_id":"op-42",
		"source":"operator_initiated",
		"requested_by":"operator",
		"event":{"event_id":"op-42:download:running:1","phase_id":"download","state":"running","agent_timestamp":1700000001,"detail":"access_token must never leave Agent"}
	}`))
	request.Header.Set("Authorization", "Bearer "+token)

	agentUpdateProgressHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.progressGUID != guid || store.progressRequest.OperationID != "op-42" || len(store.progressRequest.Events) != 1 {
		t.Fatalf("unexpected persisted request guid=%s request=%+v", store.progressGUID, store.progressRequest)
	}
	if detail := cleanText(store.progressRequest.Events[0]["detail"]); detail != "Sensitive diagnostic detail redacted." {
		t.Fatalf("expected sensitive detail redaction, got %q", detail)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if coerceInt64(payload["scheduled_job_run_id"]) != 42 {
		t.Fatalf("expected scheduler correlation, got %+v", payload)
	}
}

func TestAgentUpdateProgressHandlerRejectsInvalidProgressState(t *testing.T) {
	request, errs := normalizeAgentUpdateProgressRequest(map[string]any{
		"operation_id": "op-42",
		"source":       "operator_initiated",
		"event": map[string]any{
			"event_id": "op-42:download:unknown:1",
			"phase_id": "download",
			"state":    "unknown",
		},
	})
	if len(request.Events) != 1 || len(errs) == 0 {
		t.Fatalf("expected invalid state validation, request=%+v errors=%+v", request, errs)
	}
}

func TestAgentUpdateHistoryHandlerUsesOperatorScopeAndDeepLink(t *testing.T) {
	store := &agentUpdateProgressTestStore{}
	auth := &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		store: store, timeout: time.Second,
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/2540DA38-E2B1-45B9-9113-BF7CF0E1778A/agent-updates?operation_id=op-42&limit=20", nil)
	request.SetPathValue("device_guid", "2540DA38-E2B1-45B9-9113-BF7CF0E1778A")
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	agentUpdateHistoryHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.historyProfile.Username != "operator" || store.historyOperation != "op-42" || store.historyLimit != 20 {
		t.Fatalf("unexpected history request profile=%+v operation=%q limit=%d", store.historyProfile, store.historyOperation, store.historyLimit)
	}
}

func TestAgentUpdateStatusAndRunStatusRequireTerminalEvent(t *testing.T) {
	if got := agentUpdateStatusFromEvent("running", map[string]any{"state": "failed"}); got != "running" {
		t.Fatalf("non-terminal phase failure must keep operation running, got %q", got)
	}
	if got := agentUpdateStatusFromEvent("running", map[string]any{"state": "failed", "terminal_status": "failed"}); got != "failed" {
		t.Fatalf("terminal failure must fail operation, got %q", got)
	}
	if got := agentUpdateRunStatus("timed_out"); got != "Timed Out" {
		t.Fatalf("expected timed-out scheduler state, got %q", got)
	}
	if got := agentUpdateHistoryStatus("awaiting_health", "Timed Out"); got != "timed_out" {
		t.Fatalf("scheduler timeout must override stale Agent metadata, got %q", got)
	}
}
