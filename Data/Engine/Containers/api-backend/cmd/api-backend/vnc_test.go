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
