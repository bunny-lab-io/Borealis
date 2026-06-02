package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(entry.method, entry.path, nil)
		entry.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("%s expected fallback 202, got %d", entry.name, recorder.Code)
		}
	}
	if fallbackHits != 3 {
		t.Fatalf("expected 3 fallback hits, got %d", fallbackHits)
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
