package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeQuickRunStore struct {
	profile operatorProfile

	items    []map[string]any
	getItem  map[string]any
	getFound bool

	targets map[string]deviceProcessContext

	inserted []map[string]any
	failed   []int64
}

func (s *fakeQuickRunStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	if !strings.EqualFold(username, s.profile.Username) {
		return operatorProfile{}, errOperatorNotFound
	}
	profile := s.profile
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeQuickRunStore) listAssemblies(_ context.Context, filter assemblyListFilter) ([]map[string]any, []map[string]any, error) {
	_ = filter
	return s.items, nil, nil
}

func (s *fakeQuickRunStore) getAssembly(_ context.Context, assemblyGUID string, includePayload bool) (map[string]any, bool, error) {
	_ = assemblyGUID
	_ = includePayload
	return s.getItem, s.getFound, nil
}

func (s *fakeQuickRunStore) createAssembly(_ context.Context, payload map[string]any) (map[string]any, int, error) {
	return payload, http.StatusCreated, nil
}

func (s *fakeQuickRunStore) updateAssembly(_ context.Context, _ string, payload map[string]any) (map[string]any, int, error) {
	return payload, http.StatusOK, nil
}

func (s *fakeQuickRunStore) deleteAssembly(_ context.Context, _ string) (map[string]any, int, error) {
	return map[string]any{"status": "queued"}, http.StatusAccepted, nil
}

func (s *fakeQuickRunStore) cloneAssembly(_ context.Context, _ string, payload map[string]any) (map[string]any, int, error) {
	return payload, http.StatusCreated, nil
}

func (s *fakeQuickRunStore) importAssembly(_ context.Context, payload map[string]any) (map[string]any, int, error) {
	return payload, http.StatusCreated, nil
}

func (s *fakeQuickRunStore) loadDeviceProcessContext(_ context.Context, _ operatorProfile, hostname string) (deviceProcessContext, int, error) {
	if s.targets == nil {
		return deviceProcessContext{}, http.StatusNotFound, errors.New("not found")
	}
	target, ok := s.targets[strings.ToLower(hostname)]
	if !ok {
		return deviceProcessContext{}, http.StatusNotFound, errors.New("not found")
	}
	return target, http.StatusOK, nil
}

func (s *fakeQuickRunStore) insertQuickRunActivity(_ context.Context, hostname string, scriptPath string, scriptName string, scriptType string, status string, metadata map[string]any) (int64, error) {
	id := int64(len(s.inserted) + 100)
	s.inserted = append(s.inserted, map[string]any{
		"id":          id,
		"hostname":    hostname,
		"script_path": scriptPath,
		"script_name": scriptName,
		"script_type": scriptType,
		"status":      status,
		"metadata":    metadata,
	})
	return id, nil
}

func (s *fakeQuickRunStore) markQuickRunActivityFailed(_ context.Context, activityID int64, _ string) error {
	s.failed = append(s.failed, activityID)
	return nil
}

func testQuickRunAuth(store *fakeQuickRunStore) *authService {
	if store.profile.Username == "" {
		store.profile.Username = "operator"
	}
	if store.profile.Role == "" {
		store.profile.Role = "Admin"
	}
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

func quickRunRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/scripts/quick_run", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestQuickRunRequiresHostnames(t *testing.T) {
	store := &fakeQuickRunStore{}
	recorder := httptest.NewRecorder()

	quickRunHandler(testQuickRunAuth(store), newOperatorRealtimeHub()).ServeHTTP(recorder, quickRunRequest(`{"assembly_guid":"asm-1"}`))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestQuickRunRejectsOutOfScopeHosts(t *testing.T) {
	store := &fakeQuickRunStore{}
	recorder := httptest.NewRecorder()

	quickRunHandler(testQuickRunAuth(store), newOperatorRealtimeHub()).ServeHTTP(recorder, quickRunRequest(`{"assembly_guid":"asm-1","hostnames":["blocked-host"]}`))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestQuickRunDispatchesSignedWorkerEvent(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CERT_ROOT", t.TempDir())
	var workerBody map[string]any
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote-ops/host-service/event" {
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
		if r.Header.Get(internalTokenHeader) != goInternalToken([]byte("test-secret")) {
			t.Fatalf("missing internal token")
		}
		if err := json.NewDecoder(r.Body).Decode(&workerBody); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"emitted": true})
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	port := int64(listener.Addr().(*net.TCPAddr).Port)

	store := &fakeQuickRunStore{
		getFound: true,
		getItem: map[string]any{
			"assembly_guid":    "asm-1",
			"display_name":     "Say Target",
			"assembly_type":    "script",
			"assembly_subtype": "powershell",
			"source":           "user",
			"source_path":      "Scripts/Test/Say_Target.ps1",
			"payload_json": map[string]any{
				"name":   "Say Target",
				"type":   "powershell",
				"script": "Write-Host $env:TARGET",
				"variables": []any{
					map[string]any{"name": "Target", "type": "string", "default": "Default"},
				},
			},
		},
		targets: map[string]deviceProcessContext{
			"lab-01": {
				Hostname: "lab-01",
				AgentID:  "agent-1-system",
				Route: &agentWorkerRoute{
					WorkerGUID:     "worker-1",
					UpstreamScheme: "http",
					UpstreamHost:   "127.0.0.1",
					UpstreamPort:   port,
				},
			},
		},
	}
	realtime := newOperatorRealtimeHub()
	events := realtime.subscribe()
	defer realtime.unsubscribe(events)
	recorder := httptest.NewRecorder()

	quickRunHandler(testQuickRunAuth(store), realtime).ServeHTTP(recorder, quickRunRequest(`{"assembly_guid":"asm-1","hostnames":["lab-01"],"variable_values":{"Target":"World"}}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(store.inserted) != 1 || store.inserted[0]["status"] != "Running" {
		t.Fatalf("expected running activity insert, got %#v", store.inserted)
	}
	metadata := store.inserted[0]["metadata"].(map[string]any)
	if metadata["assembly_guid"] != "asm-1" || metadata["requested_by"] != "operator" {
		t.Fatalf("unexpected metadata %#v", metadata)
	}
	if workerBody["event_name"] != "quick_job_run" || workerBody["service_mode"] != "system" {
		t.Fatalf("unexpected worker body %#v", workerBody)
	}
	payload := workerBody["payload"].(map[string]any)
	decoded, err := base64.StdEncoding.DecodeString(payload["script_content"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "Write-Host 'World'" {
		t.Fatalf("unexpected rewritten script %q", decoded)
	}
	if payload["signature"] == "" || payload["sig_alg"] != "ed25519" || payload["signing_key"] == "" {
		t.Fatalf("missing signing fields %#v", payload)
	}
	env := payload["environment"].(map[string]any)
	if env["TARGET"] != "World" {
		t.Fatalf("unexpected environment %#v", env)
	}
	expectRealtimeEvent(t, events, "device_activity_changed")
}
