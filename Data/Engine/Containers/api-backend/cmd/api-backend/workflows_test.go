package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeWorkflowStore struct {
	profile  operatorProfile
	authErr  error
	storeErr error

	listGUID  string
	listLimit int
	runs      []map[string]any

	runID int64
	run   map[string]any

	nodeRunID int64
	nodeID    string
	nodeRun   map[string]any

	webhookGUID string
	webhooks    []map[string]any

	createGUID    string
	createProfile operatorProfile
	created       map[string]any

	deleteGUID string
	deleteID   int64
	deleted    bool
}

func (s *fakeWorkflowStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	if s.authErr != nil {
		return operatorProfile{}, s.authErr
	}
	if !strings.EqualFold(username, s.profile.Username) {
		return operatorProfile{}, errOperatorNotFound
	}
	profile := s.profile
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeWorkflowStore) listWorkflowRuns(_ context.Context, workflowGUID string, limit int) ([]map[string]any, error) {
	s.listGUID = workflowGUID
	s.listLimit = limit
	if s.storeErr != nil {
		return nil, s.storeErr
	}
	return s.runs, nil
}

func (s *fakeWorkflowStore) getWorkflowRun(_ context.Context, runID int64) (map[string]any, bool, error) {
	s.runID = runID
	if s.storeErr != nil {
		return nil, false, s.storeErr
	}
	return s.run, s.run != nil, nil
}

func (s *fakeWorkflowStore) getWorkflowNodeRun(_ context.Context, runID int64, nodeID string) (map[string]any, bool, error) {
	s.nodeRunID = runID
	s.nodeID = nodeID
	if s.storeErr != nil {
		return nil, false, s.storeErr
	}
	return s.nodeRun, s.nodeRun != nil, nil
}

func (s *fakeWorkflowStore) listWorkflowWebhooks(_ context.Context, workflowGUID string) ([]map[string]any, error) {
	s.webhookGUID = workflowGUID
	if s.storeErr != nil {
		return nil, s.storeErr
	}
	return s.webhooks, nil
}

func (s *fakeWorkflowStore) createWorkflowWebhook(_ context.Context, workflowGUID string, profile operatorProfile) (map[string]any, error) {
	s.createGUID = workflowGUID
	s.createProfile = profile
	if s.storeErr != nil {
		return nil, s.storeErr
	}
	return s.created, nil
}

func (s *fakeWorkflowStore) deleteWorkflowWebhook(_ context.Context, workflowGUID string, webhookID int64) (bool, error) {
	s.deleteGUID = workflowGUID
	s.deleteID = webhookID
	if s.storeErr != nil {
		return false, s.storeErr
	}
	return s.deleted, nil
}

func testWorkflowAuth(store *fakeWorkflowStore) *authService {
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

func workflowTestMux(store *fakeWorkflowStore) *http.ServeMux {
	mux := http.NewServeMux()
	registerWorkflowRoutes(mux, testWorkflowAuth(store), http.NotFoundHandler())
	return mux
}

func workflowRequest(method string, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	return request
}

func TestWorkflowRunsHandlerListsRuns(t *testing.T) {
	store := &fakeWorkflowStore{
		runs: []map[string]any{{"id": int64(42), "status": "Succeeded"}},
	}
	recorder := httptest.NewRecorder()
	workflowTestMux(store).ServeHTTP(recorder, workflowRequest(http.MethodGet, "/api/workflows/flow-1/runs?limit=5"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.listGUID != "flow-1" || store.listLimit != 5 {
		t.Fatalf("unexpected list args guid=%q limit=%d", store.listGUID, store.listLimit)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	runs := payload["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["status"] != "Succeeded" {
		t.Fatalf("unexpected runs payload %+v", payload)
	}
}

func TestWorkflowRunHandlerReturnsRunWithNodeRuns(t *testing.T) {
	store := &fakeWorkflowStore{
		run: map[string]any{
			"id":             int64(42),
			"workflow_guid":  "flow-1",
			"graph_snapshot": map[string]any{"nodes": []any{}},
			"node_runs":      []map[string]any{{"node_id": "node-1"}},
		},
	}
	recorder := httptest.NewRecorder()
	workflowTestMux(store).ServeHTTP(recorder, workflowRequest(http.MethodGet, "/api/workflows/runs/42"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.runID != 42 {
		t.Fatalf("unexpected run id %d", store.runID)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["workflow_guid"] != "flow-1" || len(payload["node_runs"].([]any)) != 1 {
		t.Fatalf("unexpected run payload %+v", payload)
	}
}

func TestWorkflowNodeRunHandlerReturnsChildJobs(t *testing.T) {
	store := &fakeWorkflowStore{
		nodeRun: map[string]any{
			"id":         int64(7),
			"node_id":    "node-1",
			"child_jobs": []map[string]any{{"status": "Succeeded"}},
		},
	}
	recorder := httptest.NewRecorder()
	workflowTestMux(store).ServeHTTP(recorder, workflowRequest(http.MethodGet, "/api/workflows/runs/42/nodes/node-1"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.nodeRunID != 42 || store.nodeID != "node-1" {
		t.Fatalf("unexpected node args run=%d node=%q", store.nodeRunID, store.nodeID)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["node_id"] != "node-1" || len(payload["child_jobs"].([]any)) != 1 {
		t.Fatalf("unexpected node payload %+v", payload)
	}
}

func TestWorkflowWebhookHandlersAttachURLsAndMutate(t *testing.T) {
	store := &fakeWorkflowStore{
		webhooks: []map[string]any{{
			"id":               int64(3),
			"workflow_guid":    "flow-1",
			"opaque_token":     "token-1",
			"created_at":       int64(1700000000),
			"creator_username": "operator",
		}},
		created: map[string]any{
			"id":               int64(4),
			"workflow_guid":    "flow-1",
			"opaque_token":     "token-2",
			"created_at":       int64(1700000001),
			"creator_username": "operator",
		},
		deleted: true,
	}
	mux := workflowTestMux(store)

	listRecorder := httptest.NewRecorder()
	mux.ServeHTTP(listRecorder, workflowRequest(http.MethodGet, "https://borealis.test/api/workflows/flow-1/webhooks"))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var listPayload map[string]any
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatal(err)
	}
	webhook := listPayload["webhooks"].([]any)[0].(map[string]any)
	if webhook["webhook_url"] != "https://borealis.test/api/workflows/webhooks/token-1" || webhook["creator"] != "operator" {
		t.Fatalf("unexpected webhook payload %+v", webhook)
	}

	createRecorder := httptest.NewRecorder()
	mux.ServeHTTP(createRecorder, workflowRequest(http.MethodPost, "https://borealis.test/api/workflows/flow-1/webhooks"))
	if createRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	if store.createGUID != "flow-1" || store.createProfile.Username != "operator" {
		t.Fatalf("unexpected create args guid=%q profile=%+v", store.createGUID, store.createProfile)
	}

	deleteRecorder := httptest.NewRecorder()
	mux.ServeHTTP(deleteRecorder, workflowRequest(http.MethodDelete, "/api/workflows/flow-1/webhooks/4"))
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if store.deleteGUID != "flow-1" || store.deleteID != 4 {
		t.Fatalf("unexpected delete args guid=%q id=%d", store.deleteGUID, store.deleteID)
	}
}

func TestWorkflowHandlersReportStoreErrors(t *testing.T) {
	store := &fakeWorkflowStore{storeErr: errors.New("database offline")}
	recorder := httptest.NewRecorder()
	workflowTestMux(store).ServeHTTP(recorder, workflowRequest(http.MethodGet, "/api/workflows/flow-1/runs"))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected store error response, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
