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

	createProfile  operatorProfile
	createPayload  map[string]any
	createResponse map[string]any
	createStatus   int

	detailProfile operatorProfile
	detailID      int64
	detailPayload map[string]any
	detailStatus  int

	updateProfile  operatorProfile
	updateJobID    int64
	updatePayload  map[string]any
	updateResponse map[string]any
	updateStatus   int

	rerunProfile operatorProfile
	rerunJobID   int64
	rerunPayload map[string]any
	rerunStatus  int

	toggleProfile operatorProfile
	toggleJobID   int64
	toggleEnabled bool
	togglePayload map[string]any
	toggleStatus  int

	deleteProfile operatorProfile
	deleteJobID   int64
	deletePayload map[string]any
	deleteStatus  int

	runsProfile operatorProfile
	runsJobID   int64
	runsDays    int
	runsPayload map[string]any
	runsStatus  int

	clearRunsProfile operatorProfile
	clearRunsJobID   int64
	clearRunsPayload map[string]any
	clearRunsStatus  int

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

func (s *fakeScheduledJobStore) createScheduledJob(_ context.Context, profile operatorProfile, payload map[string]any) (map[string]any, int, error) {
	s.createProfile = profile
	s.createPayload = copyMap(payload)
	status := s.createStatus
	if status == 0 {
		status = http.StatusOK
	}
	if s.createResponse != nil {
		return s.createResponse, status, nil
	}
	return map[string]any{"job": map[string]any{"id": int64(99), "name": payload["name"]}}, status, nil
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

func (s *fakeScheduledJobStore) updateScheduledJob(_ context.Context, profile operatorProfile, jobID int64, payload map[string]any) (map[string]any, int, error) {
	s.updateProfile = profile
	s.updateJobID = jobID
	s.updatePayload = copyMap(payload)
	status := s.updateStatus
	if status == 0 {
		status = http.StatusOK
	}
	if s.updateResponse != nil {
		return s.updateResponse, status, nil
	}
	return map[string]any{"job": map[string]any{"id": jobID, "name": payload["name"]}}, status, nil
}

func (s *fakeScheduledJobStore) rerunScheduledJob(_ context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error) {
	s.rerunProfile = profile
	s.rerunJobID = jobID
	status := s.rerunStatus
	if status == 0 {
		status = http.StatusOK
	}
	if s.rerunPayload != nil {
		return s.rerunPayload, status, nil
	}
	return map[string]any{"status": "queued", "occurrence": int64(1700000100)}, status, nil
}

func (s *fakeScheduledJobStore) toggleScheduledJob(_ context.Context, profile operatorProfile, jobID int64, enabled bool) (map[string]any, int, error) {
	s.toggleProfile = profile
	s.toggleJobID = jobID
	s.toggleEnabled = enabled
	status := s.toggleStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.togglePayload, status, nil
}

func (s *fakeScheduledJobStore) deleteScheduledJob(_ context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error) {
	s.deleteProfile = profile
	s.deleteJobID = jobID
	status := s.deleteStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.deletePayload, status, nil
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

func (s *fakeScheduledJobStore) clearScheduledJobRuns(_ context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error) {
	s.clearRunsProfile = profile
	s.clearRunsJobID = jobID
	status := s.clearRunsStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.clearRunsPayload, status, nil
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

func scheduledJobJSONRequest(method string, path string, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	request.Header.Set("Content-Type", "application/json")
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

func TestScheduledJobToggleHandlerPassesEnabled(t *testing.T) {
	store := &fakeScheduledJobStore{togglePayload: map[string]any{"job": map[string]any{"id": int64(7), "enabled": false}}}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobJSONRequest(http.MethodPost, "/api/scheduled_jobs/7/toggle", `{"enabled":false}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.toggleJobID != 7 || store.toggleEnabled {
		t.Fatalf("unexpected toggle args job=%d enabled=%v", store.toggleJobID, store.toggleEnabled)
	}
}

func TestScheduledJobDeleteHandlerRoutesToStore(t *testing.T) {
	store := &fakeScheduledJobStore{deletePayload: map[string]any{"status": "ok"}}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobRequest(http.MethodDelete, "/api/scheduled_jobs/9"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.deleteJobID != 9 {
		t.Fatalf("unexpected delete job id %d", store.deleteJobID)
	}
}

func TestScheduledJobRunsClearHandlerRoutesToStore(t *testing.T) {
	store := &fakeScheduledJobStore{clearRunsPayload: map[string]any{"status": "ok", "cleared": int64(2), "kept_occurrence": int64(100)}}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobRequest(http.MethodDelete, "/api/scheduled_jobs/7/runs"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.clearRunsJobID != 7 {
		t.Fatalf("unexpected clear job id %d", store.clearRunsJobID)
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

func TestScheduledJobCreateHandlerRoutesToStore(t *testing.T) {
	store := &fakeScheduledJobStore{}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobJSONRequest(http.MethodPost, "/api/scheduled_jobs", `{"name":"Patch Tuesday"}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.createPayload["name"] != "Patch Tuesday" {
		t.Fatalf("unexpected create payload %#v", store.createPayload)
	}
}

func TestScheduledJobUpdateHandlerRoutesToStore(t *testing.T) {
	store := &fakeScheduledJobStore{}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobJSONRequest(http.MethodPut, "/api/scheduled_jobs/42", `{"name":"Updated"}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.updateJobID != 42 || store.updatePayload["name"] != "Updated" {
		t.Fatalf("unexpected update args id=%d payload=%#v", store.updateJobID, store.updatePayload)
	}
}

func TestScheduledJobRerunHandlerRoutesToStore(t *testing.T) {
	store := &fakeScheduledJobStore{}
	recorder := httptest.NewRecorder()
	scheduledJobTestMux(store).ServeHTTP(recorder, scheduledJobRequest(http.MethodPost, "/api/scheduled_jobs/42/rerun"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.rerunJobID != 42 {
		t.Fatalf("unexpected rerun job id %d", store.rerunJobID)
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
