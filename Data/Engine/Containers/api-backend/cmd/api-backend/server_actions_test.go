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

func TestServerServiceActionQueuesSelectedAction(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	store := &fakeServerServiceActionStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerServerActionRoutes(mux, testServerActionAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, serverActionRequest(http.MethodPost, "/api/server/services/webui-frontend/action", `{"id":"rebuild_prod"}`))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("unexpected status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.serviceKey != "webui-frontend" || cleanText(store.action["action"]) != "rebuild" || cleanText(store.action["mode"]) != "prod" {
		t.Fatalf("unexpected queued action service=%q action=%+v", store.serviceKey, store.action)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["queued"] != true || payload["mode"] != "prod" || payload["work_item_id"].(float64) != 88 {
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

func TestServerServiceActionRejectsInvalidAction(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CONTAINERIZED", "1")
	store := &fakeServerServiceActionStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerServerActionRoutes(mux, testServerActionAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, serverActionRequest(http.MethodPost, "/api/server/services/webui-frontend/action", `{"action":"rebuild"}`))
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
