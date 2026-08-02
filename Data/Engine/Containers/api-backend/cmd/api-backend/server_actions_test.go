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

type fakeServerServiceActionStore struct {
	profile    operatorProfile
	serviceKey string
	action     map[string]any
	workItemID int64
	err        error
}

func (s *fakeServerServiceActionStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeServerServiceActionStore) queueServerServiceAction(_ context.Context, serviceKey string, action map[string]any) (int64, error) {
	s.serviceKey = serviceKey
	s.action = action
	if s.err != nil {
		return 0, s.err
	}
	if s.workItemID == 0 {
		s.workItemID = 88
	}
	return s.workItemID, nil
}

func testServerActionAuth(store *fakeServerServiceActionStore) *authService {
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

func serverActionRequest(method string, path string, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestServerServiceActionQueuesTraefikReloadAction(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	store := &fakeServerServiceActionStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerServerActionRoutes(mux, testServerActionAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, serverActionRequest(http.MethodPost, "/api/server/services/traefik-edge/action", `{"action":"reload"}`))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("unexpected status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.serviceKey != "traefik-edge" || cleanText(store.action["action"]) != "reload" {
		t.Fatalf("unexpected queued action service=%q action=%+v", store.serviceKey, store.action)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["queued"] != true || payload["work_item_id"].(float64) != 88 {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

func TestServerServiceRestartQueuesRestartAction(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	store := &fakeServerServiceActionStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerServerActionRoutes(mux, testServerActionAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, serverActionRequest(http.MethodPost, "/api/server/services/api-backend/restart", `{}`))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("unexpected status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.serviceKey != "api-backend" || cleanText(store.action["action"]) != "restart" {
		t.Fatalf("unexpected queued action service=%q action=%+v", store.serviceKey, store.action)
	}
}

func TestServerServiceWebUIRestartQueuesRestartAction(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	store := &fakeServerServiceActionStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerServerActionRoutes(mux, testServerActionAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, serverActionRequest(http.MethodPost, "/api/server/services/webui-frontend/restart", `{}`))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("unexpected status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.serviceKey != "webui-frontend" || cleanText(store.action["action"]) != "restart" {
		t.Fatalf("unexpected queued action service=%q action=%+v", store.serviceKey, store.action)
	}
}

func TestServerServiceActionRejectsWebUIRebuild(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	store := &fakeServerServiceActionStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerServerActionRoutes(mux, testServerActionAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, serverActionRequest(http.MethodPost, "/api/server/services/webui-frontend/action", `{"action":"rebuild","mode":"prod"}`))
	if recorder.Code != http.StatusBadRequest || store.serviceKey != "" {
		t.Fatalf("unexpected status=%d service=%q", recorder.Code, store.serviceKey)
	}
}

func TestServerServiceActionRequiresAdmin(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	store := &fakeServerServiceActionStore{profile: operatorProfile{Username: "operator", Role: "User"}}
	mux := http.NewServeMux()
	registerServerActionRoutes(mux, testServerActionAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, serverActionRequest(http.MethodPost, "/api/server/services/api-backend/restart", `{}`))
	if recorder.Code != http.StatusForbidden || store.serviceKey != "" {
		t.Fatalf("unexpected status=%d service=%q", recorder.Code, store.serviceKey)
	}
}

func TestServerServiceRestartQueuesSystemdRestartAction(t *testing.T) {
	t.Setenv("BOREALIS_PROJECT_ROOT", t.TempDir())
	oldLookPath := systemdLookPath
	oldRunCommand := systemdRunCommand
	defer func() {
		systemdLookPath = oldLookPath
		systemdRunCommand = oldRunCommand
	}()
	systemdLookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}
	var commands [][]string
	systemdRunCommand = func(_ context.Context, args []string) systemCommandResult {
		commands = append(commands, append([]string(nil), args...))
		if len(args) > 1 && args[1] == "show" {
			return systemCommandResult{Stdout: "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nMainPID=123\n"}
		}
		if len(args) > 0 && strings.HasSuffix(args[0], "/systemd-run") {
			return systemCommandResult{Stdout: "queued\n"}
		}
		return systemCommandResult{}
	}

	store := &fakeServerServiceActionStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerServerActionRoutes(mux, testServerActionAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, serverActionRequest(http.MethodPost, "/api/server/services/borealis_engine/restart", `{}`))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("unexpected status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["unit_name"] != "borealis-engine.service" || payload["queued"] != true {
		t.Fatalf("unexpected payload %+v", payload)
	}
	if len(commands) < 2 || !strings.HasSuffix(commands[len(commands)-1][0], "/systemd-run") {
		t.Fatalf("expected systemd-run command, got %+v", commands)
	}
	queuedCommand := commands[len(commands)-1]
	if containsString(queuedCommand, "/bin/bash") || containsString(queuedCommand, "-lc") {
		t.Fatalf("expected shell-free systemd-run argv, got %+v", queuedCommand)
	}
	if !containsString(queuedCommand, "--on-active=2s") {
		t.Fatalf("expected delayed systemd-run timer, got %+v", queuedCommand)
	}
	if len(queuedCommand) < 3 || queuedCommand[len(queuedCommand)-3] != "/usr/bin/systemctl" || queuedCommand[len(queuedCommand)-2] != "restart" || queuedCommand[len(queuedCommand)-1] != "borealis-engine.service" {
		t.Fatalf("expected systemctl restart argv suffix, got %+v", queuedCommand)
	}
}

func TestServerServiceRestartRequiresPostgresqlInstance(t *testing.T) {
	t.Setenv("BOREALIS_PROJECT_ROOT", t.TempDir())
	store := &fakeServerServiceActionStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerServerActionRoutes(mux, testServerActionAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, serverActionRequest(http.MethodPost, "/api/server/services/postgresql_cluster/restart", `{}`))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
