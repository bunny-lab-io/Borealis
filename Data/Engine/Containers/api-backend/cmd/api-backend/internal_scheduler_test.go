package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testInternalSchedulerAuth() *authService {
	return &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		timeout: time.Second,
	}
}

func TestInternalSchedulerPublicBaseURLRequiresInternalToken(t *testing.T) {
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, testInternalSchedulerAuth(), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/public-base-url", nil)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestInternalSchedulerPublicBaseURLReturnsConfiguredURL(t *testing.T) {
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.example.test/")
	auth := testInternalSchedulerAuth()
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/public-base-url", nil)
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["public_base_url"] != "https://borealis.example.test" {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

func TestInternalSchedulerHostServiceEventRoutesToWorker(t *testing.T) {
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
		if body["hostname"] != "LAB-OPERATOR-01" || body["service_mode"] != "system" || body["event_name"] != "quick_job_run" {
			t.Fatalf("unexpected event body %#v", body)
		}
		if body["allow_pending"] != true || body["pending_ttl_seconds"].(float64) != 240 {
			t.Fatalf("pending flags missing %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"emitted": false, "queued": true})
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
	auth := processTestAuth(store)
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, http.NotFoundHandler())

	body := []byte(`{"hostname":"LAB-OPERATOR-01","event_name":"quick_job_run","payload":{"job_id":1},"allow_pending":true,"pending_ttl_seconds":240}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/internal/job-scheduler/host-service-event", bytes.NewReader(body))
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawEvent {
		t.Fatalf("expected worker event")
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["queued"] != true || payload["emitted"] != false {
		t.Fatalf("unexpected response %#v", payload)
	}
}
