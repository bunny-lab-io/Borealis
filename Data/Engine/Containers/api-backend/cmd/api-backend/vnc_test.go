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
	registerVNCRoutes(mux, vncTestAuth(&fakeVNCStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}), http.NotFoundHandler())

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
	registerVNCRoutes(mux, vncTestAuth(&fakeVNCStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}), http.NotFoundHandler())

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

func TestVNCSessionRoutesFallBack(t *testing.T) {
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/vnc/establish" {
			t.Fatalf("unexpected fallback request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux := http.NewServeMux()
	registerVNCRoutes(mux, vncTestAuth(&fakeVNCStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}), fallback)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/vnc/establish", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected fallback 202, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
