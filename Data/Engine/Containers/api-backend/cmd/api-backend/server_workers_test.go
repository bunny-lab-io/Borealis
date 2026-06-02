package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeServerWorkerRecreateStore struct {
	profile    operatorProfile
	workerGUID string
	payload    map[string]any
	status     int
	err        error
}

func (s *fakeServerWorkerRecreateStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeServerWorkerRecreateStore) queueSiteWorkerRecreate(_ context.Context, workerGUID string) (map[string]any, int, error) {
	s.workerGUID = workerGUID
	if s.err != nil {
		return nil, 0, s.err
	}
	if s.payload != nil {
		return s.payload, s.status, nil
	}
	return map[string]any{"queued": true, "worker_guid": workerGUID, "work_item_id": 77}, http.StatusAccepted, nil
}

func testServerWorkerRecreateAuth(store *fakeServerWorkerRecreateStore) *authService {
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

func TestServerWorkerRecreateQueuesWorkItem(t *testing.T) {
	store := &fakeServerWorkerRecreateStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerServerWorkerRoutes(mux, testServerWorkerRecreateAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/server/workers/worker-1/recreate", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted || store.workerGUID != "worker-1" {
		t.Fatalf("unexpected status=%d worker_guid=%q", recorder.Code, store.workerGUID)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["queued"] != true || payload["work_item_id"].(float64) != 77 {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

func TestServerWorkerRecreateRequiresAdmin(t *testing.T) {
	store := &fakeServerWorkerRecreateStore{profile: operatorProfile{Username: "operator", Role: "User"}}
	mux := http.NewServeMux()
	registerServerWorkerRoutes(mux, testServerWorkerRecreateAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/server/workers/worker-1/recreate", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden || store.workerGUID != "" {
		t.Fatalf("unexpected status=%d worker_guid=%q", recorder.Code, store.workerGUID)
	}
}
