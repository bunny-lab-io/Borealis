package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

type fakeVNCStore struct {
	profile operatorProfile
}

func (s *fakeVNCStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func vncTestAuth(store *fakeVNCStore) *authService {
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

func TestVNCViewersHandlerReportsGuacdReady(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr failed: %v", err)
	}
	t.Setenv("BOREALIS_GUACAMOLE_ENABLED", "1")
	t.Setenv("BOREALIS_GUACD_HOST", "127.0.0.1")
	t.Setenv("BOREALIS_GUACD_PORT", portText)
	t.Setenv("BOREALIS_GUACAMOLE_VNC_WS_PATH", "remote-desktop/vnc/guacamole/")

	mux := http.NewServeMux()
	registerVNCRoutes(mux, vncTestAuth(&fakeVNCStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}), nil, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/vnc/viewers", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	guacamole := payload["guacamole"].(map[string]any)
	if guacamole["available"] != true || guacamole["reason"] != "ready" || guacamole["ws_path"] != "/remote-desktop/vnc/guacamole" {
		t.Fatalf("unexpected guacamole payload %#v", guacamole)
	}
	port, _ := strconv.ParseFloat(portText, 64)
	if guacamole["port"] != port {
		t.Fatalf("unexpected guacd port %#v", guacamole["port"])
	}
}

func TestVNCViewersHandlerReportsDisabled(t *testing.T) {
	t.Setenv("BOREALIS_GUACAMOLE_ENABLED", "0")
	mux := http.NewServeMux()
	registerVNCRoutes(mux, vncTestAuth(&fakeVNCStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}), nil, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/vnc/viewers", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	guacamole := payload["guacamole"].(map[string]any)
	if guacamole["enabled"] != false || guacamole["available"] != false || guacamole["reason"] != "disabled" {
		t.Fatalf("unexpected disabled payload %#v", guacamole)
	}
}

func TestVNCSessionRoutesUseGoHandler(t *testing.T) {
	mux := http.NewServeMux()
	registerVNCRoutes(mux, vncTestAuth(&fakeVNCStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}), nil, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/vnc/establish", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected Go validation 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRequestVNCServerCredentialConsumesWorkerCallEnvelope(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote-ops/host-service/call" {
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode worker request: %v", err)
		}
		if body["event_name"] != "vnc_credential_request" || body["hostname"] != "LAB-OPERATOR-01" {
			t.Fatalf("unexpected worker body %#v", body)
		}
		payload := body["payload"].(map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"called": true,
			"response": map[string]any{
				"request_id":             payload["request_id"],
				"controller_password":    "12345678",
				"credential_revision":    12345,
				"display_topology":       []map[string]any{{"id": "1", "width": 1024, "height": 768}},
				"display_virtual_bounds": map[string]any{"width": 1024, "height": 768},
			},
		})
	}))
	defer worker.Close()

	credential, err := requestVNCServerCredential(
		context.Background(),
		vncTestAuth(&fakeVNCStore{}),
		routeForTestWorker(t, worker.URL),
		"LAB-OPERATOR-01",
		"system",
		"LAB-OPERATOR-01_SYSTEM",
		"vnc_establish",
		1,
	)
	if err != nil {
		t.Fatalf("expected credential, got error %v", err)
	}
	if credential.ControllerPassword != "12345678" || credential.CredentialRevision != 12345 {
		t.Fatalf("unexpected credential %#v", credential)
	}
	if len(credential.DisplayTopology) != 1 || credential.DisplayVirtualBounds["width"] != float64(1024) {
		t.Fatalf("display data missing %#v", credential)
	}
}

func TestRequestVNCServerCredentialRejectsAgentNotReady(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode worker request: %v", err)
		}
		payload := body["payload"].(map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"called": true,
			"response": map[string]any{
				"status":         "error",
				"error":          "vnc_service_not_ready",
				"detail":         "UltraVNC service is STOP_PENDING",
				"request_id":     payload["request_id"],
				"ready":          false,
				"service_state":  "STOP_PENDING",
				"listener_state": "listening",
			},
		})
	}))
	defer worker.Close()

	_, err := requestVNCServerCredential(
		context.Background(),
		vncTestAuth(&fakeVNCStore{}),
		routeForTestWorker(t, worker.URL),
		"LAB-CA-01",
		"system",
		"LAB-CA-01_SYSTEM",
		"vnc_establish",
		1,
	)
	if err == nil || err.Error() != "UltraVNC service is STOP_PENDING" {
		t.Fatalf("expected agent readiness error, got %v", err)
	}
}

func TestRequestVNCStartReadyConsumesWorkerCallEnvelope(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote-ops/host-service/call" {
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode worker request: %v", err)
		}
		if body["event_name"] != "vnc_start" || body["hostname"] != "LAB-CAMERA-01" {
			t.Fatalf("unexpected worker body %#v", body)
		}
		if body["timeout_seconds"] != float64(12) {
			t.Fatalf("unexpected timeout %#v", body["timeout_seconds"])
		}
		payload := body["payload"].(map[string]any)
		if payload["session_id"] != "session-1" || payload["agent_id"] != "LAB-CAMERA-01_SYSTEM" {
			t.Fatalf("unexpected start payload %#v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"called": true,
			"response": map[string]any{
				"status": "ok",
				"ready":  true,
			},
		})
	}))
	defer worker.Close()

	response, status, workerErr := requestVNCStartReady(
		context.Background(),
		vncTestAuth(&fakeVNCStore{}),
		routeForTestWorker(t, worker.URL),
		"LAB-CAMERA-01",
		"system",
		map[string]any{
			"agent_id":   "LAB-CAMERA-01_SYSTEM",
			"session_id": "session-1",
			"reason":     "vnc_establish",
		},
		12,
	)
	if workerErr != nil || status != http.StatusOK {
		t.Fatalf("expected ready start, status=%d error=%#v", status, workerErr)
	}
	if response["ready"] != true {
		t.Fatalf("unexpected response %#v", response)
	}
}

func TestRequestVNCStartReadyRejectsAgentNotReady(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"called": true,
			"response": map[string]any{
				"status": "ok",
				"ready":  false,
				"detail": "UltraVNC listener still settling",
			},
		})
	}))
	defer worker.Close()

	_, status, workerErr := requestVNCStartReady(
		context.Background(),
		vncTestAuth(&fakeVNCStore{}),
		routeForTestWorker(t, worker.URL),
		"LAB-CAMERA-01",
		"system",
		map[string]any{"agent_id": "LAB-CAMERA-01_SYSTEM", "session_id": "session-1"},
		1,
	)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", status)
	}
	if cleanText(workerErr["error"]) != "vnc_agent_not_ready" || cleanText(workerErr["detail"]) != "UltraVNC listener still settling" {
		t.Fatalf("unexpected error %#v", workerErr)
	}
}

func TestVNCRecordProxyFirstFrameUpdatesSessionSnapshot(t *testing.T) {
	runtime := newVNCRuntime(nil, nil)
	session, participant, created := runtime.ensureSession(
		"agent-1",
		"operator",
		vncCredential{ControllerPassword: "12345678", CredentialRevision: 1},
		true,
	)
	if !created {
		t.Fatalf("expected new session")
	}
	runtime.recordProxyFirstFrame(session.SessionID, participant.ParticipantID, "size")
	snapshot := runtime.sessionSnapshot(session, "operator")
	if snapshot["first_frame_at"] == nil {
		t.Fatalf("first frame was not recorded in session snapshot: %#v", snapshot)
	}
}

func TestVNCPendingStopCancelledOnEstablish(t *testing.T) {
	runtime := newVNCRuntime(nil, nil)
	runtime.scheduleVNCStop(nil, nil, "LAB-CAMERA-01", "system", "agent-1", "operator_disconnect")
	runtime.cancelPendingStop("agent-1", "vnc_establish")
	runtime.mu.Lock()
	_, exists := runtime.stops["agent-1"]
	runtime.mu.Unlock()
	if exists {
		t.Fatalf("pending stop was not cancelled")
	}
}

func TestVNCWorkerSessionNeedsAuthRetryOnlyForAuthFailure(t *testing.T) {
	if !vncWorkerSessionNeedsAuthRetry(map[string]any{"error": "vnc_auth_failed"}) {
		t.Fatalf("expected vnc_auth_failed to request auth retry")
	}
	if !vncErrorNeedsAuthRetry("too_many_auth_failures:Your connection has been rejected to many attempts.") {
		t.Fatalf("expected UltraVNC lockout reason to request auth retry")
	}
	if vncWorkerSessionNeedsAuthRetry(map[string]any{"error": "vnc_backend_unreachable"}) {
		t.Fatalf("backend reachability errors should not rotate credentials")
	}
	if vncWorkerSessionNeedsAuthRetry(nil) {
		t.Fatalf("nil worker error should not request auth retry")
	}
}

func TestVNCAgentNeedsAuthRetryFromPreviousProxyClose(t *testing.T) {
	runtime := newVNCRuntime(nil, nil)
	session, participant, _ := runtime.ensureSession(
		"agent-1",
		"operator",
		vncCredential{ControllerPassword: "secret", CredentialRevision: 1},
		true,
	)

	if runtime.agentNeedsAuthRetry("agent-1") {
		t.Fatalf("fresh session should not require auth retry")
	}
	runtime.recordProxyClose(session.SessionID, participant.ParticipantID, "vnc_auth_failed")
	if !runtime.agentNeedsAuthRetry("agent-1") {
		t.Fatalf("previous proxy auth failure should require auth retry")
	}
	runtime.recordProxyFirstFrame(session.SessionID, participant.ParticipantID, "size")
	if runtime.agentNeedsAuthRetry("agent-1") {
		t.Fatalf("first frame should clear auth retry state")
	}
}

func TestVNCDefaultReadinessWaitsCoverSlowAgents(t *testing.T) {
	cases := map[string]float64{
		"live_credentials":       defaultVNCLiveCredentialWaitSeconds,
		"start_ready":            defaultVNCStartReadyWaitSeconds,
		"recovery_ready":         defaultVNCRecoveryReadyWaitSeconds,
		"restart_ready":          defaultVNCRestartReadyWaitSeconds,
		"auth_retry_credentials": defaultVNCAuthRetryCredentialWaitSeconds,
		"auth_retry_ready":       defaultVNCAuthRetryReadyWaitSeconds,
	}
	minimums := map[string]float64{
		"live_credentials":       30,
		"start_ready":            30,
		"recovery_ready":         20,
		"restart_ready":          20,
		"auth_retry_credentials": 60,
		"auth_retry_ready":       20,
	}
	for name, got := range cases {
		if got < minimums[name] {
			t.Fatalf("%s wait too short for slow agents: got %.1fs want >= %.1fs", name, got, minimums[name])
		}
	}
}

func TestNormalizePerformancePreferencePreservesSpeedBias(t *testing.T) {
	cases := map[any]int{
		-4:    -2,
		-2:    -2,
		-1:    -1,
		0:     0,
		1:     1,
		2:     2,
		4:     2,
		" -2": -2,
		"2":   2,
	}
	for input, expected := range cases {
		if got := normalizePerformancePreference(input); got != expected {
			t.Fatalf("normalizePerformancePreference(%#v)=%d want %d", input, got, expected)
		}
	}
}
