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

type fakeWatchdogStore struct {
	profile operatorProfile

	listProfile operatorProfile
	listFilter  watchdogListFilter
	items       []map[string]any

	detailProfile operatorProfile
	detailID      int64
	detail        map[string]any

	incidentProfile operatorProfile
	incidentFilter  watchdogIncidentFilter
	incidentPayload map[string]any
}

func (s *fakeWatchdogStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	if !strings.EqualFold(username, s.profile.Username) {
		return operatorProfile{}, errOperatorNotFound
	}
	profile := s.profile
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeWatchdogStore) listWatchdogs(_ context.Context, profile operatorProfile, filter watchdogListFilter) ([]map[string]any, error) {
	s.listProfile = profile
	s.listFilter = filter
	return s.items, nil
}

func (s *fakeWatchdogStore) getWatchdog(_ context.Context, profile operatorProfile, watchdogID int64) (map[string]any, bool, error) {
	s.detailProfile = profile
	s.detailID = watchdogID
	return s.detail, s.detail != nil, nil
}

func (s *fakeWatchdogStore) listWatchdogIncidents(_ context.Context, profile operatorProfile, filter watchdogIncidentFilter) (map[string]any, error) {
	s.incidentProfile = profile
	s.incidentFilter = filter
	return s.incidentPayload, nil
}

func testWatchdogAuth(store *fakeWatchdogStore) *authService {
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

func watchdogTestMux(store *fakeWatchdogStore) *http.ServeMux {
	mux := http.NewServeMux()
	registerWatchdogRoutes(mux, testWatchdogAuth(store), http.NotFoundHandler())
	return mux
}

func watchdogRequest(method string, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	return request
}

func TestWatchdogListHandlerPassesFilters(t *testing.T) {
	siteID := int64(7)
	store := &fakeWatchdogStore{
		items: []map[string]any{{"id": int64(1), "name": "Watchdog"}},
	}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogRequest(http.MethodGet, "/api/watchdogs?archived=1&site_id=7"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !store.listFilter.ArchivedSet || !store.listFilter.Archived || store.listFilter.SiteID == nil || *store.listFilter.SiteID != siteID {
		t.Fatalf("unexpected filter %+v", store.listFilter)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload["items"].([]any)) != 1 {
		t.Fatalf("unexpected list payload %+v", payload)
	}
}

func TestWatchdogMetadataHandlerReturnsEditorOptions(t *testing.T) {
	store := &fakeWatchdogStore{}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogRequest(http.MethodGet, "/api/watchdogs/metadata"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload["rule_types"].([]any)) == 0 || len(payload["action_types"].([]any)) == 0 {
		t.Fatalf("unexpected metadata payload %+v", payload)
	}
}

func TestWatchdogDetailHandlerReturnsRecord(t *testing.T) {
	store := &fakeWatchdogStore{detail: map[string]any{"id": int64(9), "name": "Watchdog"}}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogRequest(http.MethodGet, "/api/watchdogs/9"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.detailID != 9 {
		t.Fatalf("unexpected detail id %d", store.detailID)
	}
}

func TestWatchdogIncidentsHandlerPassesStateAndSite(t *testing.T) {
	store := &fakeWatchdogStore{
		incidentPayload: map[string]any{
			"items":  []map[string]any{{"id": int64(2)}},
			"counts": map[string]int64{"open": 1, "suppressed": 0, "resolved": 0},
		},
	}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogRequest(http.MethodGet, "/api/watchdogs/incidents?state=all&site=3"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.incidentFilter.State != "all" || store.incidentFilter.SiteID == nil || *store.incidentFilter.SiteID != 3 {
		t.Fatalf("unexpected incident filter %+v", store.incidentFilter)
	}
}

func TestWatchdogMutationsFallBack(t *testing.T) {
	store := &fakeWatchdogStore{}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogRequest(http.MethodPost, "/api/watchdogs"))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected fallback 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
