package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeScheduledJobStore struct {
	profile operatorProfile

	listProfile operatorProfile
	listFilter  scheduledJobListFilter
	jobs        []map[string]any

	detailProfile operatorProfile
	detailID      int64
	detailPayload map[string]any
	detailStatus  int

	runsProfile operatorProfile
	runsJobID   int64
	runsDays    int
	runsPayload map[string]any
	runsStatus  int

	devicesProfile    operatorProfile
	devicesJobID      int64
	devicesOccurrence *int64
	devicesPayload    map[string]any
	devicesStatus     int

	onboardingProfile    operatorProfile
	onboardingJobID      int64
	onboardingOccurrence *int64
	onboardingPayload    map[string]any
	onboardingStatus     int
}

func (s *fakeScheduledJobStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	if !strings.EqualFold(username, s.profile.Username) {
		return operatorProfile{}, errOperatorNotFound
	}
	profile := s.profile
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeScheduledJobStore) listScheduledJobs(_ context.Context, profile operatorProfile, filter scheduledJobListFilter) ([]map[string]any, error) {
	s.listProfile = profile
	s.listFilter = filter
	return s.jobs, nil
}

func (s *fakeScheduledJobStore) getScheduledJob(_ context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error) {
	s.detailProfile = profile
	s.detailID = jobID
	status := s.detailStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.detailPayload, status, nil
}

func (s *fakeScheduledJobStore) listScheduledJobRuns(_ context.Context, profile operatorProfile, jobID int64, days int) (map[string]any, int, error) {
	s.runsProfile = profile
	s.runsJobID = jobID
	s.runsDays = days
	status := s.runsStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.runsPayload, status, nil
}

func (s *fakeScheduledJobStore) listScheduledJobDevices(_ context.Context, profile operatorProfile, jobID int64, occurrence *int64) (map[string]any, int, error) {
	s.devicesProfile = profile
	s.devicesJobID = jobID
	s.devicesOccurrence = occurrence
	status := s.devicesStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.devicesPayload, status, nil
}

func (s *fakeScheduledJobStore) listOnboardingJobTargets(_ context.Context, profile operatorProfile, jobID int64, occurrence *int64) (map[string]any, int, error) {
	s.onboardingProfile = profile
	s.onboardingJobID = jobID
	s.onboardingOccurrence = occurrence
	status := s.onboardingStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.onboardingPayload, status, nil
}

func testScheduledJobAuth(store *fakeScheduledJobStore) *authService {
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

func scheduledJobTestMux(store *fakeScheduledJobStore) *http.ServeMux {
	mux := http.NewServeMux()
	registerScheduledJobRoutes(mux, testScheduledJobAuth(store), http.NotFoundHandler())
	return mux
}

func scheduledJobRequest(method string, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	return request
}

func TestScheduledJobListHandlerPassesSiteFilter(t *testing.T) {
	store := &fakeScheduledJobStore{
		jobs: []map[string]any{{"id": int64(1), "name": "Job"}},
	}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobRequest(http.MethodGet, "/api/scheduled_jobs?site_id=4"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.listFilter.SiteID == nil || *store.listFilter.SiteID != 4 {
		t.Fatalf("unexpected filter %+v", store.listFilter)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload["jobs"].([]any)) != 1 {
		t.Fatalf("unexpected list payload %+v", payload)
	}
}

func TestScheduledJobDetailHandlerReturnsJob(t *testing.T) {
	store := &fakeScheduledJobStore{detailPayload: map[string]any{"job": map[string]any{"id": int64(42)}}}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobRequest(http.MethodGet, "/api/scheduled_jobs/42"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.detailID != 42 {
		t.Fatalf("unexpected detail id %d", store.detailID)
	}
}

func TestScheduledJobRunsHandlerUsesDaysQuery(t *testing.T) {
	store := &fakeScheduledJobStore{runsPayload: map[string]any{"runs": []map[string]any{{"id": int64(5)}}}}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobRequest(http.MethodGet, "/api/scheduled_jobs/7/runs?days=9"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.runsJobID != 7 || store.runsDays != 9 {
		t.Fatalf("unexpected runs args job=%d days=%d", store.runsJobID, store.runsDays)
	}
}

func TestScheduledJobDevicesHandlerPassesOccurrence(t *testing.T) {
	store := &fakeScheduledJobStore{devicesPayload: map[string]any{"occurrence": int64(100), "devices": []map[string]any{}}}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobRequest(http.MethodGet, "/api/scheduled_jobs/7/devices?occurrence=100"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.devicesJobID != 7 || store.devicesOccurrence == nil || *store.devicesOccurrence != 100 {
		t.Fatalf("unexpected devices args job=%d occurrence=%v", store.devicesJobID, store.devicesOccurrence)
	}
}

func TestOnboardingTargetsHandlerPassesOccurrence(t *testing.T) {
	store := &fakeScheduledJobStore{onboardingPayload: map[string]any{"occurrence": int64(200), "targets": []map[string]any{}}}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobRequest(http.MethodGet, "/api/onboarding/jobs/8/targets?occurrence=200"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.onboardingJobID != 8 || store.onboardingOccurrence == nil || *store.onboardingOccurrence != 200 {
		t.Fatalf("unexpected onboarding args job=%d occurrence=%v", store.onboardingJobID, store.onboardingOccurrence)
	}
}

func TestScheduledJobMutationsFallBack(t *testing.T) {
	store := &fakeScheduledJobStore{}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobRequest(http.MethodPost, "/api/scheduled_jobs"))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected fallback 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestScheduledJobAggregationPrefersFailedOverSuccess(t *testing.T) {
	devices := aggregateScheduledDevices([]scheduledRunRow{
		{ID: sqlNullInt(1), TargetHostname: sqlNullString("LAB-01"), Status: sqlNullString(scheduledStatusSuccess), FinishedTS: sqlNullInt(100)},
		{ID: sqlNullInt(2), TargetHostname: sqlNullString("LAB-01"), Status: sqlNullString(scheduledStatusFailed), FinishedTS: sqlNullInt(90)},
	}, nil)
	if len(devices) != 1 {
		t.Fatalf("expected one device, got %d", len(devices))
	}
	if got := cleanText(devices[0]["status"]); got != scheduledStatusFailed {
		t.Fatalf("expected failed status, got %q", got)
	}
}

func sqlNullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}

func sqlNullInt(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}
