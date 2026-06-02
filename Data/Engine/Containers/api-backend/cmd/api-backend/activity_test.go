package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeActivityStore struct {
	profile       operatorProfile
	listProfile   operatorProfile
	listHost      string
	listPayload   map[string]any
	listStatus    int
	deleteProfile operatorProfile
	deleteHost    string
	deletePayload map[string]any
	deleteStatus  int
	jobProfile    operatorProfile
	jobID         int64
	jobPayload    map[string]any
	jobStatus     int
}

func (s *fakeActivityStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeActivityStore) listDeviceActivity(_ context.Context, profile operatorProfile, hostname string) (map[string]any, int, error) {
	s.listProfile = profile
	s.listHost = hostname
	if s.listPayload != nil {
		return s.listPayload, s.listStatus, nil
	}
	return map[string]any{"history": []map[string]any{{"id": 9, "status": "Success"}}}, http.StatusOK, nil
}

func (s *fakeActivityStore) deleteDeviceActivity(_ context.Context, profile operatorProfile, hostname string) (map[string]any, int, error) {
	s.deleteProfile = profile
	s.deleteHost = hostname
	if s.deletePayload != nil {
		return s.deletePayload, s.deleteStatus, nil
	}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func (s *fakeActivityStore) getActivityJob(_ context.Context, profile operatorProfile, activityID int64) (map[string]any, int, error) {
	s.jobProfile = profile
	s.jobID = activityID
	if s.jobPayload != nil {
		return s.jobPayload, s.jobStatus, nil
	}
	return map[string]any{"id": activityID, "status": "Running"}, http.StatusOK, nil
}

func activityTestAuth(store *fakeActivityStore) *authService {
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

func activityTestRequest(method string, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	return request
}

func TestActivityRoutesListAndDelete(t *testing.T) {
	store := &fakeActivityStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerActivityRoutes(mux, activityTestAuth(store))

	listRecorder := httptest.NewRecorder()
	mux.ServeHTTP(listRecorder, activityTestRequest(http.MethodGet, "/api/device/activity/LAB-OPERATOR-01"))
	if listRecorder.Code != http.StatusOK || store.listHost != "LAB-OPERATOR-01" || store.listProfile.Username != "operator" {
		t.Fatalf("unexpected list status=%d host=%q profile=%+v", listRecorder.Code, store.listHost, store.listProfile)
	}
	var listPayload map[string]any
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listPayload); err != nil {
		t.Fatal(err)
	}
	if history, ok := listPayload["history"].([]any); !ok || len(history) != 1 {
		t.Fatalf("unexpected history payload %+v", listPayload)
	}

	deleteRecorder := httptest.NewRecorder()
	mux.ServeHTTP(deleteRecorder, activityTestRequest(http.MethodDelete, "/api/device/activity/LAB-OPERATOR-01"))
	if deleteRecorder.Code != http.StatusOK || store.deleteHost != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected delete status=%d host=%q", deleteRecorder.Code, store.deleteHost)
	}
}

func TestActivityJobRoute(t *testing.T) {
	store := &fakeActivityStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerActivityRoutes(mux, activityTestAuth(store))

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, activityTestRequest(http.MethodGet, "/api/device/activity/job/42"))
	if recorder.Code != http.StatusOK || store.jobID != 42 {
		t.Fatalf("unexpected status=%d job_id=%d", recorder.Code, store.jobID)
	}
}

func TestActivityJobBadIDReturnsNotFound(t *testing.T) {
	store := &fakeActivityStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerActivityRoutes(mux, activityTestAuth(store))

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, activityTestRequest(http.MethodGet, "/api/device/activity/job/nope"))
	if recorder.Code != http.StatusNotFound || store.jobID != 0 {
		t.Fatalf("unexpected status=%d job_id=%d", recorder.Code, store.jobID)
	}
}

func TestActivityUnsupportedMethod(t *testing.T) {
	store := &fakeActivityStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerActivityRoutes(mux, activityTestAuth(store))

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, activityTestRequest(http.MethodPost, "/api/device/activity/LAB-OPERATOR-01"))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected status=%d", recorder.Code)
	}
	if allow := recorder.Header().Get("Allow"); allow != "GET, DELETE" {
		t.Fatalf("unexpected Allow header %q", allow)
	}
}

func TestActivityPayloadNormalizers(t *testing.T) {
	row := activityHistoryRow{
		ID:           sqlNullInt(7),
		Hostname:     sqlNullString("LAB"),
		ScriptName:   sqlNullString("Check"),
		Status:       sqlNullString("in-progress"),
		StdoutLen:    sqlNullInt(3),
		MetadataJSON: sqlNullString(`{"job":102}`),
	}
	payload := activityHistorySummaryPayload(row)
	if payload["status"] != "Running" || payload["has_stdout"] != true {
		t.Fatalf("unexpected payload %+v", payload)
	}
	metadata, ok := payload["metadata"].(map[string]any)
	if !ok || metadata["job"].(float64) != 102 {
		t.Fatalf("unexpected metadata %+v", payload["metadata"])
	}
}
