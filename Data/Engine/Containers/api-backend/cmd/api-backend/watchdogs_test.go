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

	saveProfile operatorProfile
	saveHasID   bool
	saveID      int64
	saveBody    map[string]any
	savePayload map[string]any
	saveErrors  []string

	incidentProfile operatorProfile
	incidentFilter  watchdogIncidentFilter
	incidentPayload map[string]any

	ackProfile  operatorProfile
	ackID       int64
	ackIncident map[string]any
	ackFound    bool

	stateProfile  operatorProfile
	stateID       int64
	stateValue    string
	stateReason   string
	stateIncident map[string]any
	stateErrors   []string

	deleteProfile operatorProfile
	deleteID      int64
	deleteHosts   []string
	deleteFound   bool

	previewProfile operatorProfile
	previewBody    map[string]any
	previewPayload map[string]any
	previewErrors  []string

	deviceWatchdogProfile operatorProfile
	deviceWatchdogID      string
	deviceWatchdogPayload map[string]any
	deviceWatchdogFound   bool

	overrideProfile  operatorProfile
	overrideDeviceID string
	overrideBody     map[string]any
	overridePayload  map[string]any
	overrideErrors   []string
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

func (s *fakeWatchdogStore) saveWatchdog(_ context.Context, profile operatorProfile, watchdogID *int64, body map[string]any) (map[string]any, []string, error) {
	s.saveProfile = profile
	s.saveBody = body
	if watchdogID != nil {
		s.saveHasID = true
		s.saveID = *watchdogID
	}
	return s.savePayload, s.saveErrors, nil
}

func (s *fakeWatchdogStore) listWatchdogIncidents(_ context.Context, profile operatorProfile, filter watchdogIncidentFilter) (map[string]any, error) {
	s.incidentProfile = profile
	s.incidentFilter = filter
	return s.incidentPayload, nil
}

func (s *fakeWatchdogStore) acknowledgeWatchdogIncident(_ context.Context, profile operatorProfile, incidentID int64) (map[string]any, bool, error) {
	s.ackProfile = profile
	s.ackID = incidentID
	return s.ackIncident, s.ackFound, nil
}

func (s *fakeWatchdogStore) updateWatchdogIncidentState(_ context.Context, profile operatorProfile, incidentID int64, state string, reason string) (map[string]any, []string, error) {
	s.stateProfile = profile
	s.stateID = incidentID
	s.stateValue = state
	s.stateReason = reason
	return s.stateIncident, s.stateErrors, nil
}

func (s *fakeWatchdogStore) deleteWatchdog(_ context.Context, profile operatorProfile, watchdogID int64) ([]string, bool, error) {
	s.deleteProfile = profile
	s.deleteID = watchdogID
	return s.deleteHosts, s.deleteFound, nil
}

func (s *fakeWatchdogStore) previewWatchdog(_ context.Context, profile operatorProfile, body map[string]any) (map[string]any, []string, error) {
	s.previewProfile = profile
	s.previewBody = body
	return s.previewPayload, s.previewErrors, nil
}

func (s *fakeWatchdogStore) getDeviceWatchdogs(_ context.Context, profile operatorProfile, deviceID string) (map[string]any, bool, error) {
	s.deviceWatchdogProfile = profile
	s.deviceWatchdogID = deviceID
	return s.deviceWatchdogPayload, s.deviceWatchdogFound, nil
}

func (s *fakeWatchdogStore) upsertDeviceWatchdogOverride(_ context.Context, profile operatorProfile, deviceID string, body map[string]any) (map[string]any, []string, error) {
	s.overrideProfile = profile
	s.overrideDeviceID = deviceID
	s.overrideBody = body
	return s.overridePayload, s.overrideErrors, nil
}

type fakeWatchdogIncidentBroadcaster struct {
	incidentPayloads []map[string]any
	devicePayloads   []map[string]any
}

func (b *fakeWatchdogIncidentBroadcaster) broadcastWatchdogIncidents(_ context.Context, payload map[string]any) error {
	b.incidentPayloads = append(b.incidentPayloads, copyMap(payload))
	return nil
}

func (b *fakeWatchdogIncidentBroadcaster) broadcastDeviceWatchdogs(_ context.Context, payload map[string]any) error {
	b.devicePayloads = append(b.devicePayloads, copyMap(payload))
	return nil
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
	registerWatchdogRoutes(mux, testWatchdogAuth(store), http.NotFoundHandler(), nil)
	return mux
}

func watchdogTestMuxWithBroadcaster(store *fakeWatchdogStore, broadcaster watchdogIncidentBroadcaster) *http.ServeMux {
	mux := http.NewServeMux()
	registerWatchdogRoutes(mux, testWatchdogAuth(store), http.NotFoundHandler(), broadcaster)
	return mux
}

func deviceWatchdogTestMux(store *fakeWatchdogStore) *http.ServeMux {
	return deviceWatchdogTestMuxWithBroadcaster(store, nil)
}

func deviceWatchdogTestMuxWithBroadcaster(store *fakeWatchdogStore, broadcaster watchdogIncidentBroadcaster) *http.ServeMux {
	mux := http.NewServeMux()
	registerDeviceRoutes(mux, testWatchdogAuth(store), devicePurgeRuntime{}, broadcaster)
	return mux
}

func watchdogRequest(method string, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	return request
}

func watchdogJSONRequest(method string, path string, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	request.Header.Set("Content-Type", "application/json")
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

func TestWatchdogPreviewHandlerUsesGoRoute(t *testing.T) {
	store := &fakeWatchdogStore{
		previewPayload: map[string]any{
			"devices":       []map[string]any{{"hostname": "LAB-OPERATOR-01"}},
			"device_count":  int64(1),
			"matched_count": int64(1),
		},
	}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPost, "/api/watchdogs/preview", `{"name":"Preview","targets":[{"kind":"all_devices"}]}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.previewProfile.Username != "operator" {
		t.Fatalf("expected operator profile, got %#v", store.previewProfile)
	}
	if store.previewBody["name"] != "Preview" {
		t.Fatalf("expected preview body to reach store, got %#v", store.previewBody)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["device_count"] != float64(1) || payload["matched_count"] != float64(1) {
		t.Fatalf("unexpected preview payload %#v", payload)
	}
}

func TestWatchdogPreviewHandlerReturnsValidationErrors(t *testing.T) {
	store := &fakeWatchdogStore{previewErrors: []string{"At least one watchdog rule is required."}}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPost, "/api/watchdogs/preview", `{}`))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWatchdogPreviewEvaluatesOfflineRule(t *testing.T) {
	record := map[string]any{
		"criteria": map[string]any{
			"match_mode": "all",
			"rules": []any{
				map[string]any{"id": "offline", "type": "device_offline", "offline_after_seconds": int64(60)},
			},
		},
		"match_mode":         "all",
		"boot_grace_seconds": int64(0),
	}
	device := map[string]any{
		"hostname":  "LAB-OPERATOR-01",
		"last_seen": time.Now().Unix() - 600,
	}

	evaluation := evaluateWatchdogPreviewDevice(record, device, nil)
	if !boolDefault(evaluation["matched"], false) || evaluation["state"] != "triggered" {
		t.Fatalf("expected triggered match, got %#v", evaluation)
	}
	results := anySlice(evaluation["rule_results"])
	if len(results) != 1 || !boolDefault(asStringAnyMap(results[0])["matched"], false) {
		t.Fatalf("expected matched rule result, got %#v", results)
	}
}

func TestNormalizeWatchdogSaveRecordPreservesExistingScopeAndActions(t *testing.T) {
	existing := map[string]any{
		"id":       int64(7),
		"name":     "Existing Watchdog",
		"site_ids": []int64{2, 3},
		"criteria": map[string]any{
			"match_mode": "all",
			"rules": []any{
				map[string]any{"id": "offline", "type": "device_offline", "offline_after_seconds": int64(120)},
			},
		},
		"actions": map[string]any{
			"actions": []any{
				map[string]any{"id": "svc", "type": "service_control", "action": "restart", "service_name": "Spooler"},
			},
		},
		"targets": []any{
			map[string]any{"kind": "device", "hostname": "LAB-OPERATOR-01", "site_id": int64(2)},
		},
		"created_at": int64(1700000000),
	}

	record := normalizeWatchdogSaveRecord(map[string]any{"description": "Updated"}, existing, "operator")

	siteIDs := coerceInt64Slice(record["site_ids"])
	if len(siteIDs) != 2 || siteIDs[0] != 2 || siteIDs[1] != 3 {
		t.Fatalf("expected existing site ids to survive update, got %#v", record["site_ids"])
	}
	actions := anySlice(asStringAnyMap(record["actions"])["actions"])
	if len(actions) != 1 || asStringAnyMap(actions[0])["service_name"] != "Spooler" {
		t.Fatalf("unexpected actions %#v", record["actions"])
	}
	targets := anySlice(record["targets"])
	if len(targets) != 1 || asStringAnyMap(targets[0])["kind"] != "device" {
		t.Fatalf("unexpected targets %#v", record["targets"])
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

func TestWatchdogCreateHandlerUsesGoRoute(t *testing.T) {
	store := &fakeWatchdogStore{
		savePayload: map[string]any{"id": int64(11), "name": "Created Watchdog"},
	}
	broadcaster := &fakeWatchdogIncidentBroadcaster{}
	recorder := httptest.NewRecorder()
	watchdogTestMuxWithBroadcaster(store, broadcaster).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPost, "/api/watchdogs", `{"name":"Created Watchdog"}`))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.saveHasID || store.saveProfile.Username != "operator" || store.saveBody["name"] != "Created Watchdog" {
		t.Fatalf("unexpected save call hasID=%v profile=%#v body=%#v", store.saveHasID, store.saveProfile, store.saveBody)
	}
	if len(broadcaster.incidentPayloads) != 1 || broadcaster.incidentPayloads[0]["watchdog_id"] != int64(11) {
		t.Fatalf("unexpected broadcasts %+v", broadcaster.incidentPayloads)
	}
}

func TestWatchdogUpdateHandlerUsesGoRoute(t *testing.T) {
	store := &fakeWatchdogStore{
		savePayload: map[string]any{"id": int64(9), "name": "Updated Watchdog"},
	}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPut, "/api/watchdogs/9", `{"name":"Updated Watchdog"}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !store.saveHasID || store.saveID != 9 || store.saveBody["name"] != "Updated Watchdog" {
		t.Fatalf("unexpected save call hasID=%v id=%d body=%#v", store.saveHasID, store.saveID, store.saveBody)
	}
}

func TestWatchdogSaveHandlerReturnsValidationErrors(t *testing.T) {
	store := &fakeWatchdogStore{saveErrors: []string{"At least one watchdog rule is required."}}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPost, "/api/watchdogs", `{}`))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWatchdogSaveHandlerMapsNotFound(t *testing.T) {
	store := &fakeWatchdogStore{saveErrors: []string{"Watchdog not found."}}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPut, "/api/watchdogs/99", `{"name":"Missing"}`))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWatchdogDeleteUsesGoRoute(t *testing.T) {
	store := &fakeWatchdogStore{
		deleteHosts: []string{"host-1", "host-2"},
		deleteFound: true,
	}
	broadcaster := &fakeWatchdogIncidentBroadcaster{}
	recorder := httptest.NewRecorder()
	watchdogTestMuxWithBroadcaster(store, broadcaster).ServeHTTP(recorder, watchdogRequest(http.MethodDelete, "/api/watchdogs/9"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.deleteID != 9 {
		t.Fatalf("unexpected delete id %d", store.deleteID)
	}
	if len(broadcaster.incidentPayloads) != 3 || len(broadcaster.devicePayloads) != 2 {
		t.Fatalf("unexpected broadcast counts incident=%d device=%d", len(broadcaster.incidentPayloads), len(broadcaster.devicePayloads))
	}
	if broadcaster.incidentPayloads[2]["hostname"] != "" || broadcaster.incidentPayloads[2]["watchdog_id"] != int64(9) {
		t.Fatalf("unexpected final incident broadcast %+v", broadcaster.incidentPayloads[2])
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

func TestDeviceWatchdogsHandlerUsesGoRoute(t *testing.T) {
	store := &fakeWatchdogStore{
		deviceWatchdogPayload: map[string]any{
			"device":      map[string]any{"hostname": "LAB-OPERATOR-01"},
			"assignments": []map[string]any{{"watchdog_id": int64(7)}},
			"incidents":   []map[string]any{},
			"overrides":   []map[string]any{},
		},
		deviceWatchdogFound: true,
	}
	recorder := httptest.NewRecorder()
	deviceWatchdogTestMux(store).ServeHTTP(recorder, watchdogRequest(http.MethodGet, "/api/devices/LAB-OPERATOR-01/watchdogs"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.deviceWatchdogID != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected device id %q", store.deviceWatchdogID)
	}
}

func TestDeviceWatchdogOverrideHandlerUsesGoRoute(t *testing.T) {
	store := &fakeWatchdogStore{
		overridePayload: map[string]any{
			"device":      map[string]any{"hostname": "LAB-OPERATOR-01"},
			"assignments": []map[string]any{{"watchdog_id": int64(7), "state": "suppressed"}},
			"incidents":   []map[string]any{},
			"overrides":   []map[string]any{{"watchdog_id": int64(7), "state": "suppressed"}},
		},
	}
	broadcaster := &fakeWatchdogIncidentBroadcaster{}
	recorder := httptest.NewRecorder()
	deviceWatchdogTestMuxWithBroadcaster(store, broadcaster).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPost, "/api/devices/LAB-OPERATOR-01/watchdogs/overrides", `{"watchdog_id":7,"state":"suppressed","reason":"maintenance"}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.overrideDeviceID != "LAB-OPERATOR-01" || store.overrideProfile.Username != "operator" {
		t.Fatalf("unexpected override call device=%q profile=%#v", store.overrideDeviceID, store.overrideProfile)
	}
	if coerceInt64(store.overrideBody["watchdog_id"]) != 7 || store.overrideBody["reason"] != "maintenance" {
		t.Fatalf("unexpected override body %#v", store.overrideBody)
	}
	if len(broadcaster.incidentPayloads) != 1 || broadcaster.incidentPayloads[0]["hostname"] != "LAB-OPERATOR-01" || broadcaster.incidentPayloads[0]["watchdog_id"] != int64(7) {
		t.Fatalf("unexpected incident broadcasts %+v", broadcaster.incidentPayloads)
	}
	if len(broadcaster.devicePayloads) != 1 || broadcaster.devicePayloads[0]["hostname"] != "LAB-OPERATOR-01" || broadcaster.devicePayloads[0]["watchdog_id"] != int64(7) {
		t.Fatalf("unexpected device broadcasts %+v", broadcaster.devicePayloads)
	}
}

func TestDeviceWatchdogOverrideHandlerReturnsValidationErrors(t *testing.T) {
	store := &fakeWatchdogStore{overrideErrors: []string{"Watchdog not found."}}
	recorder := httptest.NewRecorder()
	deviceWatchdogTestMux(store).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPost, "/api/devices/LAB-OPERATOR-01/watchdogs/overrides", `{"watchdog_id":99}`))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceWatchdogOverrideHandlerRequiresSuppressionReason(t *testing.T) {
	store := &fakeWatchdogStore{overrideErrors: []string{"Suppression reason is required."}}
	recorder := httptest.NewRecorder()
	deviceWatchdogTestMux(store).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPost, "/api/devices/LAB-OPERATOR-01/watchdogs/overrides", `{"watchdog_id":7,"state":"suppressed","reason":"  "}`))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWatchdogIncidentAcknowledgeUsesGoRoute(t *testing.T) {
	store := &fakeWatchdogStore{
		ackIncident: map[string]any{"id": int64(12), "watchdog_id": int64(4), "hostname": "host-1"},
		ackFound:    true,
	}
	broadcaster := &fakeWatchdogIncidentBroadcaster{}
	recorder := httptest.NewRecorder()
	watchdogTestMuxWithBroadcaster(store, broadcaster).ServeHTTP(recorder, watchdogRequest(http.MethodPost, "/api/watchdogs/incidents/12/acknowledge"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.ackID != 12 {
		t.Fatalf("unexpected ack id %d", store.ackID)
	}
	if len(broadcaster.incidentPayloads) != 1 || broadcaster.incidentPayloads[0]["hostname"] != "host-1" || broadcaster.incidentPayloads[0]["watchdog_id"] != int64(4) {
		t.Fatalf("unexpected incident broadcast payloads %+v", broadcaster.incidentPayloads)
	}
	if len(broadcaster.devicePayloads) != 1 || broadcaster.devicePayloads[0]["hostname"] != "host-1" || broadcaster.devicePayloads[0]["watchdog_id"] != int64(4) {
		t.Fatalf("unexpected device broadcast payloads %+v", broadcaster.devicePayloads)
	}
}

func TestWatchdogIncidentStateUpdatePassesPayload(t *testing.T) {
	store := &fakeWatchdogStore{
		stateIncident: map[string]any{"id": int64(12), "watchdog_id": int64(4), "hostname": "host-1", "state": "suppressed"},
	}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPost, "/api/watchdogs/incidents/12/state", `{"state":"suppressed","reason":"maintenance"}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.stateID != 12 || store.stateValue != "suppressed" || store.stateReason != "maintenance" {
		t.Fatalf("unexpected state update id=%d state=%q reason=%q", store.stateID, store.stateValue, store.stateReason)
	}
}

func TestWatchdogIncidentStateUpdateRequiresSuppressionReason(t *testing.T) {
	store := &fakeWatchdogStore{
		stateIncident: map[string]any{"id": int64(12), "watchdog_id": int64(4), "hostname": "host-1", "state": "suppressed"},
	}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPost, "/api/watchdogs/incidents/12/state", `{"state":"suppressed","reason":"  "}`))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.stateID != 0 {
		t.Fatalf("blank suppression reason should be rejected before store call, got id=%d", store.stateID)
	}
}

func TestWatchdogIncidentStateUpdateMapsNotFound(t *testing.T) {
	store := &fakeWatchdogStore{stateErrors: []string{"Incident not found."}}
	recorder := httptest.NewRecorder()
	watchdogTestMux(store).ServeHTTP(recorder, watchdogJSONRequest(http.MethodPost, "/api/watchdogs/incidents/99/state", `{"state":"open"}`))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWatchdogRuntimeRulesAreOfflineOnly(t *testing.T) {
	if !watchdogRuntimeRulesAreOfflineOnly(map[string]any{"rules": []any{
		map[string]any{"type": "device_offline"},
		map[string]any{"type": "device_offline"},
	}}) {
		t.Fatalf("expected offline-only rules to be detected")
	}
	if watchdogRuntimeRulesAreOfflineOnly(map[string]any{"rules": []any{
		map[string]any{"type": "device_offline"},
		map[string]any{"type": "cpu_usage_percent"},
	}}) {
		t.Fatalf("expected mixed rules to skip offline-only purge")
	}
	if watchdogRuntimeRulesAreOfflineOnly(map[string]any{"rules": []any{}}) {
		t.Fatalf("expected empty rules to skip offline-only purge")
	}
}

func TestWatchdogRuntimeCooldownGate(t *testing.T) {
	record := map[string]any{"cooldown_seconds": int64(300)}
	last := int64(1000)
	if watchdogRuntimeShouldRunActions(record, &last, 1200) {
		t.Fatalf("expected cooldown to block action dispatch")
	}
	if !watchdogRuntimeShouldRunActions(record, &last, 1300) {
		t.Fatalf("expected cooldown boundary to allow action dispatch")
	}
	if !watchdogRuntimeShouldRunActions(map[string]any{"cooldown_seconds": int64(0)}, &last, 1001) {
		t.Fatalf("expected zero cooldown to allow action dispatch")
	}
}
