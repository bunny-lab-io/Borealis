package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeProcessStore struct {
	profile  operatorProfile
	snapshot deviceProcessContext
	status   int
	err      error
	seen     string
}

func (s *fakeProcessStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeProcessStore) loadDeviceProcessContext(_ context.Context, _ operatorProfile, hostname string) (deviceProcessContext, int, error) {
	s.seen = hostname
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	if s.err != nil {
		return deviceProcessContext{}, status, s.err
	}
	return s.snapshot, status, nil
}

func processTestAuth(store *fakeProcessStore) *authService {
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

func TestDeviceProcessListHandlerCallsWorkerAndReturnsProcesses(t *testing.T) {
	var sawStatus bool
	var sawCall bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("worker request body decode failed: %v", err)
		}
		switch r.URL.Path {
		case "/remote-ops/host-service/status":
			sawStatus = true
			if body["hostname"] != "LAB-OPERATOR-01" || body["service_mode"] != "system" {
				t.Fatalf("unexpected status body %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
		case "/remote-ops/host-service/call":
			sawCall = true
			if body["hostname"] != "LAB-OPERATOR-01" || body["service_mode"] != "system" || body["event_name"] != "process_management_request" {
				t.Fatalf("unexpected call body %#v", body)
			}
			payload, _ := body["payload"].(map[string]any)
			if payload["action"] != "list" || payload["max_age_seconds"].(float64) != 0.25 {
				t.Fatalf("unexpected call payload %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"called": true,
				"response": map[string]any{
					"ok":                  true,
					"reported_at":         1700000001,
					"refresh_interval_ms": 2500,
					"processes": []map[string]any{
						{"pid": 123, "name": "pwsh"},
					},
				},
			})
		default:
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
	}))
	defer worker.Close()
	route := routeForTestWorker(t, worker.URL)
	store := &fakeProcessStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: deviceProcessContext{
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Route:    route,
		},
	}
	mux := http.NewServeMux()
	registerProcessRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/processes/LAB-OPERATOR-01?max_age_seconds=0.01", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawStatus || !sawCall {
		t.Fatalf("expected status and call requests, sawStatus=%v sawCall=%v", sawStatus, sawCall)
	}
	if store.seen != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected hostname %q", store.seen)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if payload["agent_socket"] != true || payload["collection_state"] != "ready" || payload["count"].(float64) != 1 || payload["refresh_interval_ms"].(float64) != 5000 {
		t.Fatalf("unexpected process response %#v", payload)
	}
}

func TestDeviceProcessListHandlerMarksEmptySnapshotCollecting(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/remote-ops/host-service/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
		case "/remote-ops/host-service/call":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"called": true,
				"response": map[string]any{
					"ok":                  true,
					"reported_at":         1700000003,
					"refresh_interval_ms": 5000,
					"processes":           []map[string]any{},
				},
			})
		default:
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
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
	registerProcessRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/processes/LAB-OPERATOR-01", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if payload["collection_state"] != "collecting" || payload["count"].(float64) != 0 || payload["retry_after_ms"].(float64) != processCollectingRetryAfterMS {
		t.Fatalf("unexpected empty snapshot response %#v", payload)
	}
}

func TestDeviceProcessListHandlerReturnsUnavailableWhenSocketMissing(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote-ops/host-service/status" {
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"registered": false})
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
	registerProcessRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/processes/LAB-OPERATOR-01", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceProcessTerminateCallsWorkerAndReturnsSnapshot(t *testing.T) {
	var sawStatus bool
	var sawCall bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("worker request body decode failed: %v", err)
		}
		switch r.URL.Path {
		case "/remote-ops/host-service/status":
			sawStatus = true
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
		case "/remote-ops/host-service/call":
			sawCall = true
			if body["hostname"] != "LAB-OPERATOR-01" || body["service_mode"] != "system" || body["event_name"] != "process_management_request" {
				t.Fatalf("unexpected call body %#v", body)
			}
			payload, _ := body["payload"].(map[string]any)
			if payload["action"] != "terminate" || payload["pid"].(float64) != 1234 || payload["include_children"] != true || payload["requested_by"] != "operator" {
				t.Fatalf("unexpected terminate payload %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"called": true,
				"response": map[string]any{
					"ok":                  true,
					"reported_at":         1700000002,
					"refresh_interval_ms": 2500,
					"processes": []map[string]any{
						{"pid": 10, "name": "remaining"},
					},
				},
			})
		default:
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
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
	registerProcessRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/processes/LAB-OPERATOR-01/terminate", strings.NewReader(`{"pid":1234,"include_children":true}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawStatus || !sawCall {
		t.Fatalf("expected status and call requests, sawStatus=%v sawCall=%v", sawStatus, sawCall)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if payload["terminated_pid"].(float64) != 1234 || payload["count"].(float64) != 1 || payload["refresh_interval_ms"].(float64) != 5000 {
		t.Fatalf("unexpected terminate response %#v", payload)
	}
}

func TestDeviceProcessTerminateRequiresPID(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/remote-ops/host-service/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
			return
		}
		t.Fatalf("unexpected worker path %s", r.URL.Path)
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
	registerProcessRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/processes/LAB-OPERATOR-01/terminate", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceProcessListHandlerPropagatesStoreNotFound(t *testing.T) {
	store := &fakeProcessStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		status:  http.StatusNotFound,
		err:     errors.New("not found"),
	}
	mux := http.NewServeMux()
	registerProcessRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/processes/missing", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func routeForTestWorker(t *testing.T, rawURL string) *agentWorkerRoute {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("worker url parse failed: %v", err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("worker host split failed: %v", err)
	}
	port, err := strconv.ParseInt(portText, 10, 64)
	if err != nil {
		t.Fatalf("worker port parse failed: %v", err)
	}
	return &agentWorkerRoute{
		WorkerGUID:      "worker-1",
		SiteID:          1,
		RoutePathPrefix: "/_borealis/site-workers/worker-1",
		UpstreamScheme:  parsed.Scheme,
		UpstreamHost:    host,
		UpstreamPort:    port,
		Generation:      1,
	}
}
