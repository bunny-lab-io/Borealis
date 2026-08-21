package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type agentMaintenanceTestStore struct {
	profile        operatorProfile
	request        agentMaintenanceRequest
	called         bool
	payload        map[string]any
	status         int
	err            error
	processContext deviceProcessContext
	processStatus  int
	processErr     error
}

func (s *agentMaintenanceTestStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.DisplayName == "" {
		profile.DisplayName = profile.Username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	if profile.AuthSource == "" {
		profile.AuthSource = "local"
	}
	return profile, nil
}

func (s *agentMaintenanceTestStore) queueAgentMaintenance(_ context.Context, profile operatorProfile, request agentMaintenanceRequest) (map[string]any, int, error) {
	s.called = true
	s.profile = profile
	s.request = request
	if s.err != nil {
		status := s.status
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return nil, status, s.err
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	return copyMap(s.payload), status, nil
}

func (s *agentMaintenanceTestStore) loadDeviceProcessContext(_ context.Context, _ operatorProfile, hostname string) (deviceProcessContext, int, error) {
	if s.processErr != nil {
		status := s.processStatus
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return deviceProcessContext{}, status, s.processErr
	}
	status := s.processStatus
	if status == 0 {
		status = http.StatusOK
	}
	snapshot := s.processContext
	if snapshot.Hostname == "" {
		snapshot.Hostname = hostname
	}
	return snapshot, status, nil
}

func TestAgentMaintenanceBulkHandlerQueuesRequest(t *testing.T) {
	store := &agentMaintenanceTestStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		payload: map[string]any{
			"status": "queued",
			"job_id": int64(123),
			"queued": []any{map[string]any{"hostname": "LAB-OPERATOR-01"}},
			"errors": []any{},
		},
	}
	auth := testAgentMaintenanceAuth(store)
	recorder := httptest.NewRecorder()
	body := `{"action":"update_agent","guids":["2540DA38-E2B1-45B9-9113-BF7CF0E1778A"]}`
	request := httptest.NewRequest(http.MethodPost, "/api/devices/agent-maintenance", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	agentMaintenanceBulkHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !store.called {
		t.Fatal("expected store call")
	}
	if store.request.Action != agentMaintenanceUpdateAction {
		t.Fatalf("unexpected request %+v", store.request)
	}
	if len(store.request.DeviceGUIDs) != 1 || store.request.DeviceGUIDs[0] != "2540DA38-E2B1-45B9-9113-BF7CF0E1778A" {
		t.Fatalf("unexpected guids %+v", store.request.DeviceGUIDs)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "queued" || payload["job_id"].(float64) != 123 {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

func TestAgentMaintenanceBulkHandlerRejectsRemovedSwitchAction(t *testing.T) {
	store := &agentMaintenanceTestStore{profile: operatorProfile{Username: "operator", Role: "User"}}
	auth := testAgentMaintenanceAuth(store)
	recorder := httptest.NewRecorder()
	body := `{"action":"switch_branch","guids":["2540DA38-E2B1-45B9-9113-BF7CF0E1778A"]}`
	request := httptest.NewRequest(http.MethodPost, "/api/devices/agent-maintenance", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	agentMaintenanceBulkHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.called {
		t.Fatal("store should not be called")
	}
}

func TestNormalizeAgentMaintenanceGUIDsAcceptsDeviceObjects(t *testing.T) {
	guids := normalizeAgentMaintenanceGUIDs([]any{
		map[string]any{"guid": "2540da38e2b145b99113bf7cf0e1778a"},
		map[string]any{"device_guid": "{2540DA38-E2B1-45B9-9113-BF7CF0E1778A}"},
		"not-a-guid",
	})
	if len(guids) != 1 || guids[0] != "2540DA38-E2B1-45B9-9113-BF7CF0E1778A" {
		t.Fatalf("unexpected guids %+v", guids)
	}
}

func TestAgentMaintenanceBulkHandlerPropagatesStoreErrors(t *testing.T) {
	store := &agentMaintenanceTestStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		status:  http.StatusConflict,
		err:     errors.New("queue_failed"),
	}
	auth := testAgentMaintenanceAuth(store)
	recorder := httptest.NewRecorder()
	body := `{"action":"update_now","guids":["2540DA38-E2B1-45B9-9113-BF7CF0E1778A"]}`
	request := httptest.NewRequest(http.MethodPost, "/api/devices/agent-maintenance", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	agentMaintenanceBulkHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceAgentUpdateHandlerQueuesSchedulerJob(t *testing.T) {
	store := &agentMaintenanceTestStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		processContext: deviceProcessContext{
			GUID:     "2540DA38-E2B1-45B9-9113-BF7CF0E1778A",
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
		},
		payload: map[string]any{
			"status": "queued",
			"job_id": int64(123),
			"queued": []any{map[string]any{"hostname": "LAB-OPERATOR-01", "operation_id": "op-42"}},
		},
	}
	auth := testAgentMaintenanceAuth(store)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/update-agent/LAB-OPERATOR-01", nil)
	request.SetPathValue("hostname", "LAB-OPERATOR-01")
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	deviceAgentUpdateHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "queued" || payload["job_id"].(float64) != 123 {
		t.Fatalf("unexpected payload %+v", payload)
	}
	if !store.called || len(store.request.DeviceGUIDs) != 1 || store.request.DeviceGUIDs[0] != "2540DA38-E2B1-45B9-9113-BF7CF0E1778A" {
		t.Fatalf("expected Scheduler-backed maintenance request, got %+v", store.request)
	}
}

func TestDeviceAgentUpdateHandlerQueuesWithoutActiveSocket(t *testing.T) {
	store := &agentMaintenanceTestStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		processContext: deviceProcessContext{
			GUID: "2540DA38-E2B1-45B9-9113-BF7CF0E1778A", Hostname: "LAB-OPERATOR-01", AgentID: "LAB-OPERATOR-01_SYSTEM",
		},
		payload: map[string]any{"status": "queued", "job_id": int64(124), "queued": []any{}},
	}
	auth := testAgentMaintenanceAuth(store)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/update-agent/LAB-OPERATOR-01", nil)
	request.SetPathValue("hostname", "LAB-OPERATOR-01")
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	deviceAgentUpdateHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || !store.called {
		t.Fatalf("expected Scheduler queue despite offline socket, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func testAgentMaintenanceAuth(store *agentMaintenanceTestStore) *authService {
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
