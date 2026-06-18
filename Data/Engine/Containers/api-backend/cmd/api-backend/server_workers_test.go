package main

import (
	"context"
	"database/sql"
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

func TestScheduledRunWorkPayloadCarriesSiteScopedTask(t *testing.T) {
	payload := scheduledRunWorkPayload(scheduledRunWorkRow{
		RunID:          sql.NullInt64{Int64: 1200, Valid: true},
		JobID:          sql.NullInt64{Int64: 140, Valid: true},
		SiteID:         sql.NullInt64{Int64: 1, Valid: true},
		TargetHostname: sql.NullString{String: "LAB-OPERATOR-01", Valid: true},
		Status:         sql.NullString{String: scheduledStatusRunning, Valid: true},
		StartedAt:      sql.NullInt64{Int64: 1700000000, Valid: true},
		UpdatedAt:      sql.NullInt64{Int64: 1700000005, Valid: true},
	})

	if payload["id"] != "scheduled-run:1200" || payload["kind"] != schedulerKindScheduledRun {
		t.Fatalf("unexpected identity payload %#v", payload)
	}
	if payload["site_id"].(int64) != 1 || payload["job_id"].(int64) != 140 || payload["run_id"].(int64) != 1200 {
		t.Fatalf("unexpected ids %#v", payload)
	}
	if payload["status"] != scheduledStatusRunning || payload["target_count"].(int64) != 1 {
		t.Fatalf("unexpected status/count %#v", payload)
	}
	link, ok := payload["task_link"].(map[string]any)
	if !ok || link["job_id"].(int64) != 140 || link["run_id"].(int64) != 1200 {
		t.Fatalf("unexpected task link %#v", payload["task_link"])
	}
}

func TestFilterScheduledDispatchWorkPrefersRunState(t *testing.T) {
	workItems := []map[string]any{
		{"id": int64(1026), "kind": schedulerKindScheduledRun, "run_id": int64(1204), "status": workStatusSucceeded},
		{"id": int64(9000), "kind": schedulerKindServiceAction, "run_id": int64(1204), "status": workStatusSucceeded},
		{"id": int64(1027), "kind": schedulerKindScheduledRun, "run_id": int64(1205), "status": workStatusQueued},
	}
	scheduledRuns := []map[string]any{
		{"id": "scheduled-run:1204", "kind": schedulerKindScheduledRun, "run_id": int64(1204), "status": scheduledStatusSuccess},
	}

	filtered := filterScheduledDispatchWork(workItems, scheduledRuns)
	if len(filtered) != 2 {
		t.Fatalf("expected dispatch duplicate removed, got %#v", filtered)
	}
	if filtered[0]["kind"] != schedulerKindServiceAction || filtered[1]["run_id"].(int64) != 1205 {
		t.Fatalf("unexpected filtered rows %#v", filtered)
	}
}
