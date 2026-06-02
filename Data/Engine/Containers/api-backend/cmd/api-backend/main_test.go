package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testAuthToken = "eyJ1Ijoib3BlcmF0b3IiLCJyIjoiQWRtaW4iLCJ0cyI6MTcwMDAwMDAwMH0.ZVPxAA.T_nkD4f7np9iU74bxSttSuR_MoY"
const testCompressedAuthToken = ".eJyrVipVslJKySxKTS7JL6rUKy1OLVLSUSoCCoZCmCXFSlaG5gZQUAsAhqAOag.ZVPxAA.-Zu3AisDtRhgTd33co1kzyxIQqw"

type fakeOperatorStore struct {
	profiles         map[string]operatorProfile
	err              error
	search           []deviceSearchMatch
	searchErr        error
	searchProfile    operatorProfile
	searchQuery      string
	devices          []map[string]any
	deviceErr        error
	deviceProfile    operatorProfile
	deviceFilter     deviceListFilter
	sites            []map[string]any
	siteErr          error
	siteProfile      operatorProfile
	siteMap          map[string]map[string]any
	siteMapErr       error
	siteMapHostnames []string
	views            []map[string]any
	viewErr          error
	viewByID         map[int64]map[string]any
	viewIDSeen       int64
	metadataFields   []map[string]any
	metadataErr      error
	serverWorkers    map[string]any
	serverWorkerErr  error
	workerHistory    int
	workerContainers bool
}

func (s *fakeOperatorStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	if s.err != nil {
		return operatorProfile{}, s.err
	}
	profile, ok := s.profiles[strings.ToLower(username)]
	if !ok {
		return operatorProfile{}, errOperatorNotFound
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeOperatorStore) searchDevicesByHostname(_ context.Context, profile operatorProfile, query string) ([]deviceSearchMatch, error) {
	s.searchProfile = profile
	s.searchQuery = query
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	matches := append([]deviceSearchMatch(nil), s.search...)
	sortDeviceSearchMatches(matches, query)
	return matches, nil
}

func (s *fakeOperatorStore) listDevices(_ context.Context, profile operatorProfile, filter deviceListFilter) ([]map[string]any, error) {
	s.deviceProfile = profile
	s.deviceFilter = filter
	if s.deviceErr != nil {
		return nil, s.deviceErr
	}
	devices := make([]map[string]any, 0, len(s.devices))
	for _, device := range s.devices {
		copyDevice := make(map[string]any, len(device))
		for key, value := range device {
			copyDevice[key] = value
		}
		devices = append(devices, copyDevice)
	}
	return devices, nil
}

func (s *fakeOperatorStore) listSites(_ context.Context, profile operatorProfile) ([]map[string]any, error) {
	s.siteProfile = profile
	if s.siteErr != nil {
		return nil, s.siteErr
	}
	sites := make([]map[string]any, 0, len(s.sites))
	for _, site := range s.sites {
		copySite := make(map[string]any, len(site))
		for key, value := range site {
			copySite[key] = value
		}
		sites = append(sites, copySite)
	}
	return sites, nil
}

func (s *fakeOperatorStore) siteDeviceMap(_ context.Context, profile operatorProfile, hostnames []string) (map[string]map[string]any, error) {
	s.siteProfile = profile
	s.siteMapHostnames = append([]string(nil), hostnames...)
	if s.siteMapErr != nil {
		return nil, s.siteMapErr
	}
	mapping := make(map[string]map[string]any, len(s.siteMap))
	for hostname, site := range s.siteMap {
		copySite := make(map[string]any, len(site))
		for key, value := range site {
			copySite[key] = value
		}
		mapping[hostname] = copySite
	}
	return mapping, nil
}

func (s *fakeOperatorStore) listDeviceViews(_ context.Context) ([]map[string]any, error) {
	if s.viewErr != nil {
		return nil, s.viewErr
	}
	views := make([]map[string]any, 0, len(s.views))
	for _, view := range s.views {
		copyView := make(map[string]any, len(view))
		for key, value := range view {
			copyView[key] = value
		}
		views = append(views, copyView)
	}
	return views, nil
}

func (s *fakeOperatorStore) getDeviceView(_ context.Context, viewID int64) (map[string]any, bool, error) {
	s.viewIDSeen = viewID
	if s.viewErr != nil {
		return nil, false, s.viewErr
	}
	view, ok := s.viewByID[viewID]
	if !ok {
		return nil, false, nil
	}
	copyView := make(map[string]any, len(view))
	for key, value := range view {
		copyView[key] = value
	}
	return copyView, true, nil
}

func (s *fakeOperatorStore) listMetadataDefinitions(_ context.Context) ([]map[string]any, error) {
	if s.metadataErr != nil {
		return nil, s.metadataErr
	}
	fields := make([]map[string]any, 0, len(s.metadataFields))
	for _, field := range s.metadataFields {
		copyField := make(map[string]any, len(field))
		for key, value := range field {
			copyField[key] = value
		}
		fields = append(fields, copyField)
	}
	return fields, nil
}

func (s *fakeOperatorStore) serverWorkerPayload(_ context.Context, historySeconds int, includeContainerMetadata bool) (map[string]any, error) {
	s.workerHistory = historySeconds
	s.workerContainers = includeContainerMetadata
	if s.serverWorkerErr != nil {
		return nil, s.serverWorkerErr
	}
	payload := make(map[string]any, len(s.serverWorkers))
	for key, value := range s.serverWorkers {
		payload[key] = value
	}
	return payload, nil
}

func testAuthService(profile operatorProfile) *authService {
	auth, _ := testAuthServiceWithStore(profile)
	return auth
}

func testAuthServiceWithStore(profile operatorProfile) (*authService, *fakeOperatorStore) {
	if profile.Username == "" {
		profile.Username = "operator"
	}
	if profile.DisplayName == "" {
		profile.DisplayName = profile.Username
	}
	if profile.Role == "" {
		profile.Role = "Admin"
	}
	if profile.AuthSource == "" {
		profile.AuthSource = "local"
	}
	store := &fakeOperatorStore{
		profiles: map[string]operatorProfile{
			strings.ToLower(profile.Username): profile,
		},
	}
	return &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		store:   store,
		timeout: time.Second,
	}, store
}

func TestSetEnvReplacesExistingValue(t *testing.T) {
	env := setEnv([]string{"PATH=/bin", "BOREALIS_ENGINE_PORT=5000"}, "BOREALIS_ENGINE_PORT", "5001")
	got := strings.Join(env, "\n")
	if strings.Count(got, "BOREALIS_ENGINE_PORT=") != 1 {
		t.Fatalf("expected one BOREALIS_ENGINE_PORT entry, got %q", got)
	}
	if !strings.Contains(got, "BOREALIS_ENGINE_PORT=5001") {
		t.Fatalf("expected replaced port, got %q", got)
	}
}

func TestEnvDurationSecondsRejectsInvalidValues(t *testing.T) {
	t.Setenv("BOREALIS_TEST_DURATION", "-1")
	if got := envDurationSeconds("BOREALIS_TEST_DURATION", 3*time.Second); got != 3*time.Second {
		t.Fatalf("expected fallback for negative duration, got %s", got)
	}
	t.Setenv("BOREALIS_TEST_DURATION", "1.5")
	if got := envDurationSeconds("BOREALIS_TEST_DURATION", 3*time.Second); got != 1500*time.Millisecond {
		t.Fatalf("expected parsed duration, got %s", got)
	}
}

func TestHealthHandlerReportsHealthyLegacyBackend(t *testing.T) {
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected legacy path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer legacy.Close()

	legacyURL, err := url.Parse(legacy.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := gatewayConfig{LegacyURL: legacyURL, HealthTimeout: time.Second}
	state := &legacyState{}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthHandler(cfg, state).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !state.snapshot().Healthy {
		t.Fatalf("expected state marked healthy")
	}
}

func TestHealthHandlerReportsUnhealthyLegacyBackend(t *testing.T) {
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer legacy.Close()

	legacyURL, err := url.Parse(legacy.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := gatewayConfig{LegacyURL: legacyURL, HealthTimeout: time.Second}
	state := &legacyState{}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthHandler(cfg, state).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if state.snapshot().Healthy {
		t.Fatalf("expected state marked unhealthy")
	}
}

func TestTokenVerifierAcceptsItsDangerousFixture(t *testing.T) {
	verifier := &tokenVerifier{
		secret: []byte("test-secret"),
		maxAge: time.Hour,
		now:    func() time.Time { return time.Unix(1700000010, 0) },
	}

	identity, err := verifier.verify(testAuthToken)
	if err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
	if identity.Username != "operator" || identity.Role != "Admin" {
		t.Fatalf("unexpected identity %+v", identity)
	}

	identity, err = verifier.verify(testCompressedAuthToken)
	if err != nil {
		t.Fatalf("expected compressed token valid, got %v", err)
	}
	if identity.Username != "directory.user" || identity.Role != "User" {
		t.Fatalf("unexpected compressed identity %+v", identity)
	}
}

func TestAuthMeHandlerReturnsOperatorProfile(t *testing.T) {
	auth := testAuthService(operatorProfile{
		Username:            "operator",
		DisplayName:         "Operator",
		Role:                "Admin",
		MFAEnabled:          true,
		PasskeyCount:        2,
		AuthSource:          "local",
		DirectoryProviderID: 0,
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	authMeHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["username"] != "operator" || payload["role"] != "Admin" || payload["passkey_count"].(float64) != 2 {
		t.Fatalf("unexpected /api/auth/me payload %+v", payload)
	}
}

func TestDeviceSearchHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/search?hostname=lab", nil)
	deviceSearchHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceSearchHandlerReturnsEmptyForShortQueryAfterAuth(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/search?hostname=la", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceSearchHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.searchQuery != "" {
		t.Fatalf("expected short query to skip DB search, got query %q", store.searchQuery)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["query"] != "la" || payload["count"].(float64) != 0 {
		t.Fatalf("unexpected short search payload %+v", payload)
	}
}

func TestDeviceSearchHandlerReturnsSortedMatches(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.search = []deviceSearchMatch{
		{AgentGUID: "CCCC", AgentID: "agent-c", Hostname: "z-lab", SiteName: "Zeta"},
		{AgentGUID: "BBBB", AgentID: "agent-b", Hostname: "lab-02", SiteName: "Beta"},
		{AgentGUID: "AAAA", AgentID: "agent-a", Hostname: "lab", SiteName: "Alpha"},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/search?hostname=lab", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceSearchHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Devices []deviceSearchMatch `json:"devices"`
		Query   string              `json:"query"`
		Count   int                 `json:"count"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Query != "lab" || payload.Count != 3 {
		t.Fatalf("unexpected search payload %+v", payload)
	}
	if got := payload.Devices[0].Hostname; got != "lab" {
		t.Fatalf("expected exact hostname first, got %q", got)
	}
	if store.searchProfile.Username != "operator" || store.searchQuery != "lab" {
		t.Fatalf("expected search called with operator profile/query, got %+v %q", store.searchProfile, store.searchQuery)
	}
}

func TestDeviceListHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	deviceListHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceListHandlerReturnsDevicesAndFilters(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.devices = []map[string]any{
		{
			"hostname":   "LAB-OPERATOR-01",
			"agent_guid": "2540DA38-E2B1-45B9-9113-BF7CF0E1778A",
			"status":     "online",
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices?only_agents=true&connection_type=ssh&hostname=lab-operator-01", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceListHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Devices) != 1 || payload.Devices[0]["hostname"] != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected device payload %+v", payload)
	}
	if !store.deviceFilter.OnlyAgents || store.deviceFilter.ConnectionType != "ssh" || store.deviceFilter.Hostname != "lab-operator-01" {
		t.Fatalf("unexpected device filters %+v", store.deviceFilter)
	}
	if store.deviceProfile.Username != "operator" {
		t.Fatalf("expected operator profile, got %+v", store.deviceProfile)
	}
}

func TestAgentListHandlerReturnsLegacyMapping(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.devices = []map[string]any{
		{
			"hostname":            "LAB-OPERATOR-01",
			"agent_guid":          "2540DA38E2B145B99113BF7CF0E1778A",
			"agent_id":            "LAB-OPERATOR-01-svc",
			"agent_hash":          "build-a",
			"last_seen":           int64(1700000000),
			"status":              "Online",
			"connection_type":     "",
			"connection_endpoint": "",
			"device_type":         "Windows",
			"domain":              "BUNNY",
			"external_ip":         "203.0.113.10",
			"internal_ip":         "10.0.0.5",
			"last_reboot":         "2026-06-01T00:00:00Z",
			"last_user":           "bunny",
			"operating_system":    "Windows 11",
			"uptime":              int64(42),
			"site_id":             int64(1),
			"site_name":           "Bunny Lab",
			"site_description":    "Lab site",
		},
		{
			"hostname":   "LAB-OPERATOR-01",
			"agent_guid": "2540DA38E2B145B99113BF7CF0E1778A",
			"agent_id":   "LAB-OPERATOR-01-svc",
			"last_seen":  int64(1699999999),
			"status":     "Offline",
		},
		{
			"hostname":   "LAB-OPERATOR-01",
			"agent_guid": "2540DA38E2B145B99113BF7CF0E1778A",
			"agent_id":   "LAB-OPERATOR-01-user",
			"last_seen":  int64(1699999900),
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	agentListHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !store.deviceFilter.OnlyAgents {
		t.Fatalf("expected only_agents filter captured")
	}
	var payload map[string]map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	systemAgent := payload["LAB-OPERATOR-01-svc"]
	if systemAgent == nil {
		t.Fatalf("expected system agent key in %+v", payload)
	}
	if got := systemAgent["service_mode"]; got != "system" {
		t.Fatalf("expected system mode, got %#v", got)
	}
	if got := systemAgent["agent_guid"]; got != "2540DA38-E2B1-45B9-9113-BF7CF0E1778A" {
		t.Fatalf("expected normalized guid, got %#v", got)
	}
	if got := systemAgent["status"]; got != "Online" {
		t.Fatalf("expected newest system row selected, got %#v", got)
	}
	if got := systemAgent["site_name"]; got != "Bunny Lab" {
		t.Fatalf("expected site copied, got %#v", got)
	}
	userAgent := payload["LAB-OPERATOR-01-user"]
	if userAgent == nil {
		t.Fatalf("expected current-user agent key in %+v", payload)
	}
	if got := userAgent["service_mode"]; got != "currentuser" {
		t.Fatalf("expected currentuser mode, got %#v", got)
	}
}

func TestSiteListHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	siteListHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSiteListHandlerReturnsSitesAndPublicMetadata(t *testing.T) {
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.example.test/")
	t.Setenv("BOREALIS_PUBLIC_HOSTNAME", "borealis.example.test")
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.sites = []map[string]any{
		{
			"id":                   1,
			"name":                 "Bunny Lab",
			"description":          "Lab site",
			"device_count":         21,
			"auto_approval_active": false,
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	siteListHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Sites          []map[string]any `json:"sites"`
		PublicBaseURL  string           `json:"public_base_url"`
		PublicHostname string           `json:"public_hostname"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Sites) != 1 || payload.Sites[0]["name"] != "Bunny Lab" {
		t.Fatalf("unexpected sites payload %+v", payload)
	}
	if payload.PublicBaseURL != "https://borealis.example.test" || payload.PublicHostname != "borealis.example.test" {
		t.Fatalf("unexpected public metadata %+v", payload)
	}
	if store.siteProfile.Username != "operator" {
		t.Fatalf("expected operator profile, got %+v", store.siteProfile)
	}
}

func TestSiteDeviceMapHandlerReturnsMapping(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.siteMap = map[string]map[string]any{
		"LAB-OPERATOR-01": {
			"site_id":   1,
			"site_name": "Bunny Lab",
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/sites/device_map?hostnames=LAB-OPERATOR-01,,LAB-OPERATOR-01", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	siteDeviceMapHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Mapping map[string]map[string]any `json:"mapping"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Mapping["LAB-OPERATOR-01"]["site_name"] != "Bunny Lab" {
		t.Fatalf("unexpected mapping %+v", payload.Mapping)
	}
	if len(store.siteMapHostnames) != 1 || store.siteMapHostnames[0] != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected host filter %+v", store.siteMapHostnames)
	}
}

func TestDeviceViewListHandlerReturnsViews(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.views = []map[string]any{
		{
			"id":         int64(7),
			"name":       "Lab View",
			"columns":    []any{"status", "hostname"},
			"filters":    map[string]any{"site": "Bunny Lab"},
			"created_at": int64(1700000000),
			"updated_at": int64(1700000100),
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device_list_views", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceViewListHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Views []map[string]any `json:"views"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Views) != 1 || payload.Views[0]["name"] != "Lab View" {
		t.Fatalf("unexpected views payload %+v", payload)
	}
}

func TestDeviceViewGetHandlerReturnsViewOrNotFound(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.viewByID = map[int64]map[string]any{
		7: {
			"id":      int64(7),
			"name":    "Lab View",
			"columns": []any{"status"},
			"filters": map[string]any{},
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device_list_views/7", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceViewGetHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.viewIDSeen != 7 {
		t.Fatalf("expected view id 7, got %d", store.viewIDSeen)
	}

	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodGet, "/api/device_list_views/8", nil)
	missingRequest.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceViewGetHandler(auth, nil).ServeHTTP(missing, missingRequest)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", missing.Code, missing.Body.String())
	}
}

func TestReadOnlyHandlersProxyNonNativeMethods(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	fallbackHits := 0
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.WriteHeader(http.StatusAccepted)
	})

	for _, entry := range []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
	}{
		{name: "site create", handler: siteListHandler(auth, fallback), method: http.MethodPost, path: "/api/sites"},
		{name: "view create", handler: deviceViewListHandler(auth, fallback), method: http.MethodPost, path: "/api/device_list_views"},
		{name: "view update", handler: deviceViewGetHandler(auth, fallback), method: http.MethodPut, path: "/api/device_list_views/7"},
		{name: "ansible runner update", handler: ansibleRunnerSettingsHandler(auth, fallback), method: http.MethodPut, path: "/api/server/ansible-runner-settings"},
		{name: "server workers update", handler: serverWorkersHandler(auth, fallback), method: http.MethodPost, path: "/api/server/workers"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(entry.method, entry.path, nil)
		entry.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("%s expected fallback 202, got %d", entry.name, recorder.Code)
		}
	}
	if fallbackHits != 5 {
		t.Fatalf("expected 5 fallback hits, got %d", fallbackHits)
	}
}

func TestServerWorkersHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/workers", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverWorkersHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestServerWorkersHandlerReturnsPayload(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.serverWorkers = map[string]any{
		"active_count": int64(1),
		"workers": []any{
			map[string]any{"worker_guid": "worker-1", "status": "running"},
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/workers?history_seconds=999999", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverWorkersHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.workerHistory != 86400 {
		t.Fatalf("expected clamped history 86400, got %d", store.workerHistory)
	}
	if !store.workerContainers {
		t.Fatalf("expected container metadata request")
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["active_count"]; got != float64(1) {
		t.Fatalf("expected active_count 1, got %#v", got)
	}
	workers, ok := payload["workers"].([]any)
	if !ok || len(workers) != 1 {
		t.Fatalf("expected one worker, got %#v", payload["workers"])
	}
}

func TestSiteWorkerSettingsHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/site-worker-settings", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	siteWorkerSettingsHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Administrator permissions") {
		t.Fatalf("expected admin auth message, got %s", recorder.Body.String())
	}
}

func TestSiteWorkerSettingsHandlerReturnsProfileManagedPayload(t *testing.T) {
	t.Setenv("BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY", "45")
	t.Setenv("BOREALIS_DEPLOYMENT_PROFILE", "MSP / Production")
	t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "4")
	t.Setenv("BOREALIS_DEPLOYMENT_CPU_RANK", "3")
	t.Setenv("BOREALIS_DEPLOYMENT_MEMORY_RANK", "2")
	t.Setenv("BOREALIS_DEPLOYMENT_HOST_VCPU", "16")
	t.Setenv("BOREALIS_DEPLOYMENT_HOST_MEMORY_MIB", "33075")
	t.Setenv("BOREALIS_DEPLOYMENT_HOST_MEMORY_GIB", "32.3")
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/site-worker-settings", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	siteWorkerSettingsHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["scheduled_task_concurrency_limit"]; got != float64(32) {
		t.Fatalf("expected clamped concurrency 32, got %#v", got)
	}
	if got := payload["max_scheduled_task_concurrency_limit"]; got != float64(32) {
		t.Fatalf("expected max concurrency 32, got %#v", got)
	}
	if got := payload["editable"]; got != false {
		t.Fatalf("expected editable false, got %#v", got)
	}
	if got := payload["managed_by"]; got != "deployment_profile" {
		t.Fatalf("expected deployment_profile manager, got %#v", got)
	}
	profile, ok := payload["deployment_profile"].(map[string]any)
	if !ok {
		t.Fatalf("expected deployment profile map, got %#v", payload["deployment_profile"])
	}
	if got := profile["name"]; got != "MSP / Production" {
		t.Fatalf("expected profile name, got %#v", got)
	}
	if got := profile["host_vcpu"]; got != float64(16) {
		t.Fatalf("expected host vcpu 16, got %#v", got)
	}
	if got := profile["host_memory_gib"]; got != "32.3" {
		t.Fatalf("expected host memory gib string, got %#v", got)
	}
}

func TestSiteWorkerSettingsLoadsConfigFileWhenEnvUnset(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "site_worker_settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"scheduled_task_concurrency_limit": 12}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_SITE_WORKER_SETTINGS_PATH", settingsPath)
	t.Setenv("BOREALIS_DEPLOYMENT_PROFILE", "")

	payload := collectSiteWorkerSettingsPayload()
	if got := payload["scheduled_task_concurrency_limit"]; got != 12 {
		t.Fatalf("expected file-backed concurrency 12, got %#v", got)
	}
	profile := payload["deployment_profile"].(map[string]any)
	if got := profile["name"]; got != "Unprofiled" {
		t.Fatalf("expected unprofiled fallback, got %#v", got)
	}
}

func TestAnsibleRunnerSettingsHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/ansible-runner-settings", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	ansibleRunnerSettingsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAnsibleRunnerSettingsHandlerReturnsConfigPayload(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "ansible_runner_settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"job_concurrency_limit": 0, "global_concurrency_limit": 18}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH", settingsPath)
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/ansible-runner-settings", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	ansibleRunnerSettingsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["job_concurrency_limit"]; got != float64(1) {
		t.Fatalf("expected clamped job limit 1, got %#v", got)
	}
	if got := payload["global_concurrency_limit"]; got != float64(18) {
		t.Fatalf("expected global limit 18, got %#v", got)
	}
}

func TestAnsibleRunnerSettingsUsesEnvDefaultsWhenConfigMissing(t *testing.T) {
	t.Setenv("BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("BOREALIS_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT", "7")
	t.Setenv("BOREALIS_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT", "0")

	payload := collectAnsibleRunnerSettingsPayload()
	if got := payload["job_concurrency_limit"]; got != 7 {
		t.Fatalf("expected env-backed job limit 7, got %#v", got)
	}
	if got := payload["global_concurrency_limit"]; got != 1 {
		t.Fatalf("expected clamped env-backed global limit 1, got %#v", got)
	}
}

func TestMetadataFieldsHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/metadata_fields", nil)
	metadataFieldsHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMetadataFieldsHandlerReturnsDefinitions(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.metadataFields = []map[string]any{
		{
			"field_number":  1,
			"field_key":     "field_001",
			"default_label": "Field 001",
			"label":         "Rack",
			"description":   "Rack",
			"updated_at":    int64(1700000000),
			"updated_by":    "operator",
			"value_limit":   1024,
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/metadata_fields", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	metadataFieldsHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Fields     []map[string]any `json:"fields"`
		Count      int              `json:"count"`
		ValueLimit int              `json:"value_limit"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 1 || payload.ValueLimit != 1024 {
		t.Fatalf("unexpected metadata payload %+v", payload)
	}
	if got := payload.Fields[0]["label"]; got != "Rack" {
		t.Fatalf("expected custom label, got %#v", got)
	}
}

func TestBuildMetadataDefinitionsReturnsDefaultFiveHundredFields(t *testing.T) {
	fields := buildMetadataDefinitions(map[int]metadataDefinitionRow{
		7: {
			FieldNumber: sql.NullInt64{Int64: 7, Valid: true},
			Description: sql.NullString{String: "Location Code", Valid: true},
			UpdatedAt:   sql.NullInt64{Int64: 1700000000, Valid: true},
			UpdatedBy:   sql.NullString{String: "operator", Valid: true},
		},
	})
	if len(fields) != 500 {
		t.Fatalf("expected 500 fields, got %d", len(fields))
	}
	if fields[0]["field_key"] != "field_001" || fields[0]["label"] != "Field 001" {
		t.Fatalf("unexpected first field %+v", fields[0])
	}
	if fields[6]["field_key"] != "field_007" || fields[6]["label"] != "Location Code" {
		t.Fatalf("unexpected custom field %+v", fields[6])
	}
}

func TestServerTimeHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/time", nil)
	serverTimeHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Authentication required") {
		t.Fatalf("expected normalized auth message, got %s", recorder.Body.String())
	}
}

func TestServerTimezonesHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/timezones", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverTimezonesHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Administrator permissions") {
		t.Fatalf("expected admin auth message, got %s", recorder.Body.String())
	}
}

func TestServerTimeHandlerReturnsNativePayload(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/time", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverTimeHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"epoch", "iso", "utc", "timezone", "timezone_id", "display"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected payload key %q in %+v", key, payload)
		}
	}
}

func TestSerializeServerTimeMatchesPythonShape(t *testing.T) {
	location := time.FixedZone("MST", -7*60*60)
	nowLocal := time.Date(2026, 6, 2, 13, 4, 5, 123456789, location)
	nowUTC := nowLocal.UTC()

	payload := serializeServerTime(nowLocal, nowUTC, "America/Denver")

	if got := payload["iso"]; got != "2026-06-02T13:04:05.123456-07:00" {
		t.Fatalf("unexpected local iso %q", got)
	}
	if got := payload["utc"]; got != "2026-06-02T20:04:05.123456+00:00" {
		t.Fatalf("unexpected utc iso %q", got)
	}
	if got := payload["display"]; got != "2026-06-02 13:04:05 MST" {
		t.Fatalf("unexpected display %q", got)
	}
	if got := payload["timezone_id"]; got != "America/Denver" {
		t.Fatalf("unexpected timezone id %q", got)
	}
}

func TestCurrentTimezoneIDPrefersEngineHostTimezone(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_HOST_TIMEZONE", "America/Denver")
	t.Setenv("TZ", "Etc/UTC")

	if got := currentTimezoneID(); got != "America/Denver" {
		t.Fatalf("expected engine host timezone, got %q", got)
	}
}
