package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type fakeSoftwareIconStore struct {
	profile operatorProfile
	asset   softwareIconAsset
	found   bool
	seen    string

	serviceSnapshot deviceServicesSnapshot
	serviceStatus   int
	serviceErr      error
	serviceHostname string

	auditRows    []map[string]any
	auditErr     error
	auditProfile operatorProfile

	overrideSnapshot softwareOverrideContext
	overrideStatus   int
	overrideErr      error
	overrideHostname string

	nextActivityID       int64
	insertedActivityHost string
	insertedActivityName string
	insertedMetadata     map[string]any
	failedActivityID     int64
	failedActivityText   string

	persistedServiceHost       string
	persistedServices          []map[string]any
	persistedServicesReported  int64
	persistedDeviceServicesErr error
}

func (s *fakeSoftwareIconStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeSoftwareIconStore) loadSoftwareIconAsset(_ context.Context, iconHash string) (softwareIconAsset, bool, error) {
	s.seen = iconHash
	return s.asset, s.found, nil
}

func (s *fakeSoftwareIconStore) loadDeviceServices(_ context.Context, _ operatorProfile, hostname string) (deviceServicesSnapshot, int, error) {
	s.serviceHostname = hostname
	status := s.serviceStatus
	if status == 0 {
		status = http.StatusOK
	}
	if s.serviceErr != nil {
		return deviceServicesSnapshot{}, status, s.serviceErr
	}
	return s.serviceSnapshot, status, nil
}

func (s *fakeSoftwareIconStore) listSoftwareAudit(_ context.Context, profile operatorProfile) ([]map[string]any, error) {
	s.auditProfile = profile
	return s.auditRows, s.auditErr
}

func (s *fakeSoftwareIconStore) loadDeviceSoftwareContext(_ context.Context, _ operatorProfile, hostname string) (softwareOverrideContext, int, error) {
	s.overrideHostname = hostname
	status := s.overrideStatus
	if status == 0 {
		status = http.StatusOK
	}
	if s.overrideErr != nil {
		return softwareOverrideContext{}, status, s.overrideErr
	}
	return s.overrideSnapshot, status, nil
}

func (s *fakeSoftwareIconStore) insertSoftwareUninstallActivity(_ context.Context, hostname string, scriptName string, metadata map[string]any) (int64, error) {
	s.insertedActivityHost = hostname
	s.insertedActivityName = scriptName
	s.insertedMetadata = metadata
	if s.nextActivityID <= 0 {
		s.nextActivityID = 900
	}
	return s.nextActivityID, nil
}

func (s *fakeSoftwareIconStore) markSoftwareUninstallActivityFailed(_ context.Context, activityID int64, failureText string) error {
	s.failedActivityID = activityID
	s.failedActivityText = failureText
	return nil
}

func (s *fakeSoftwareIconStore) persistDeviceServices(_ context.Context, hostname string, services []map[string]any, reportedAt int64) error {
	s.persistedServiceHost = hostname
	s.persistedServices = services
	s.persistedServicesReported = reportedAt
	return s.persistedDeviceServicesErr
}

func softwareIconTestAuth(store *fakeSoftwareIconStore) *authService {
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

func TestSoftwareIconHandlerReturnsAsset(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	store := &fakeSoftwareIconStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		asset: softwareIconAsset{
			Hash:     hash,
			MIMEType: "image/png",
			Bytes:    []byte{0x89, 0x50, 0x4e, 0x47},
		},
		found: true,
	}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/software/icon/"+hash, nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.seen != hash {
		t.Fatalf("expected normalized hash capture, got %q", store.seen)
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("unexpected content-type %q", got)
	}
	if got := recorder.Body.Bytes(); len(got) != 4 || got[1] != 0x50 {
		t.Fatalf("unexpected icon bytes %v", got)
	}
}

func TestSoftwareIconHandlerRejectsInvalidHash(t *testing.T) {
	store := &fakeSoftwareIconStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/software/icon/not-a-hash", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.seen != "" {
		t.Fatalf("invalid hash should not hit store, got %q", store.seen)
	}
}

func TestSoftwareIconHandlerNotFound(t *testing.T) {
	hash := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	store := &fakeSoftwareIconStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/software/icon/"+hash, nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceServicesHandlerReturnsCachedServicesAndWorkerSocket(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote-ops/host-service/status" {
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("worker request body decode failed: %v", err)
		}
		if body["hostname"] != "LAB-OPERATOR-01" || body["service_mode"] != "system" {
			t.Fatalf("unexpected worker request body %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
	}))
	defer worker.Close()
	workerURL, _ := url.Parse(worker.URL)
	host, portText, _ := net.SplitHostPort(workerURL.Host)
	port, _ := strconv.ParseInt(portText, 10, 64)

	store := &fakeSoftwareIconStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		serviceSnapshot: deviceServicesSnapshot{
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Reported: 1700000000,
			Route: &agentWorkerRoute{
				WorkerGUID:      "worker-1",
				SiteID:          1,
				RoutePathPrefix: "/_borealis/site-workers/worker-1",
				UpstreamScheme:  workerURL.Scheme,
				UpstreamHost:    host,
				UpstreamPort:    port,
				Generation:      1,
			},
			Services: []map[string]any{
				{
					"service_id":     "spooler",
					"name":           "Spooler",
					"display_name":   "Print Spooler",
					"status_code":    "running",
					"status":         "Running",
					"captured_at":    int64(1700000000),
					"pending_action": "",
				},
			},
		},
	}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/services/LAB-OPERATOR-01", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.serviceHostname != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected hostname %q", store.serviceHostname)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if payload["agent_socket"] != true || payload["count"].(float64) != 1 || payload["refresh_interval_seconds"].(float64) != serviceRefreshPeriod {
		t.Fatalf("unexpected response %#v", payload)
	}
}

func TestDeviceServiceActionMarksPendingAndEmitsWorkerEvent(t *testing.T) {
	var sawEvent bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("worker request body decode failed: %v", err)
		}
		switch r.URL.Path {
		case "/remote-ops/host-service/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
		case "/remote-ops/host-service/event":
			sawEvent = true
			if body["hostname"] != "LAB-OPERATOR-01" || body["service_mode"] != "system" || body["event_name"] != "service_control_action" {
				t.Fatalf("unexpected event body %#v", body)
			}
			payload, _ := body["payload"].(map[string]any)
			if payload["service_name"] != "Spooler" || payload["action"] != "restart" || payload["requested_by"] != "operator" {
				t.Fatalf("unexpected service event payload %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"emitted": true})
		default:
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
	}))
	defer worker.Close()
	store := &fakeSoftwareIconStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		serviceSnapshot: deviceServicesSnapshot{
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Reported: 1700000000,
			Route:    routeForTestWorker(t, worker.URL),
			Services: []map[string]any{
				{
					"service_id":     "spooler",
					"name":           "Spooler",
					"display_name":   "Print Spooler",
					"status_code":    "running",
					"status":         "Running",
					"captured_at":    int64(1700000000),
					"pending_action": "",
				},
			},
		},
	}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/services/LAB-OPERATOR-01/action", bytes.NewReader([]byte(`{"service_name":"Spooler","action":"restart"}`)))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawEvent {
		t.Fatalf("expected worker event")
	}
	if store.persistedServiceHost != "LAB-OPERATOR-01" || len(store.persistedServices) != 1 {
		t.Fatalf("unexpected persisted services host=%q rows=%#v", store.persistedServiceHost, store.persistedServices)
	}
	if store.persistedServices[0]["pending_action"] != "restart" || store.persistedServices[0]["desired_status"] != "running" {
		t.Fatalf("unexpected persisted service %#v", store.persistedServices[0])
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if payload["action"] != "restart" || payload["action_label"] != "Restarting..." || payload["count"].(float64) != 1 {
		t.Fatalf("unexpected response %#v", payload)
	}
}

func TestDeviceServicesHandlerPropagatesStoreNotFound(t *testing.T) {
	store := &fakeSoftwareIconStore{
		profile:       operatorProfile{Username: "operator", Role: "Admin"},
		serviceStatus: http.StatusNotFound,
		serviceErr:    errors.New("not found"),
	}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/services/missing", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSoftwareRefreshHandlerQueuesWorkerEvent(t *testing.T) {
	var sawEvent bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("worker request body decode failed: %v", err)
		}
		if r.URL.Path != "/remote-ops/host-service/event" {
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
		sawEvent = true
		if body["hostname"] != "LAB-OPERATOR-01" || body["service_mode"] != "system" || body["event_name"] != "software_inventory_refresh_request" {
			t.Fatalf("unexpected event body %#v", body)
		}
		if body["allow_pending"] != true || body["pending_ttl_seconds"].(float64) != 180 {
			t.Fatalf("unexpected pending flags %#v", body)
		}
		payload, _ := body["payload"].(map[string]any)
		if payload["requested_by"] != "operator" || payload["reason"] != "operator_query_software_updates" || payload["agent_id"] != "LAB-OPERATOR-01_SYSTEM" {
			t.Fatalf("unexpected refresh payload %#v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"emitted": false, "queued": true})
	}))
	defer worker.Close()

	store := &fakeProcessStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: deviceProcessContext{
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Route:    routeForTestWorker(t, worker.URL),
		},
	}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, processTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/software/LAB-OPERATOR-01/refresh", bytes.NewReader([]byte(`{}`)))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawEvent {
		t.Fatalf("expected worker event")
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if payload["status"] != "queued" || payload["hostname"] != "LAB-OPERATOR-01" || payload["agent_id"] != "LAB-OPERATOR-01_SYSTEM" {
		t.Fatalf("unexpected refresh response %#v", payload)
	}
}

func TestDeviceSoftwareIconOverridePersistsRuleAndQueuesRefresh(t *testing.T) {
	iconPath := filepath.Join(t.TempDir(), "software_icons_overrides.json")
	t.Setenv("BOREALIS_SOFTWARE_ICON_OVERRIDES_PATH", iconPath)
	if err := os.WriteFile(iconPath, []byte(`{"windows_icon_overrides":[]}`), 0o600); err != nil {
		t.Fatalf("seed icon overrides: %v", err)
	}
	var sawEvent bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote-ops/host-service/event" {
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("worker request body decode failed: %v", err)
		}
		sawEvent = true
		if body["event_name"] != "software_inventory_refresh_request" || body["allow_pending"] != true {
			t.Fatalf("unexpected event body %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"queued": true})
	}))
	defer worker.Close()
	store := &fakeSoftwareIconStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		overrideSnapshot: softwareOverrideContext{
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Route:    routeForTestWorker(t, worker.URL),
			Software: []map[string]any{
				{
					"name":    "Contoso Agent",
					"version": "1.0",
					"source":  "local_installed",
					"metadata": map[string]any{
						"publisher": "Contoso",
					},
				},
			},
		},
	}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	body := bytes.NewReader([]byte(`{"name":"Contoso Agent","version":"1.0","source":"local_installed","display_icon":"C:\\Program Files\\Contoso\\agent.exe,2"}`))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/software/LAB-OPERATOR-01/icon-override", body)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawEvent {
		t.Fatalf("expected refresh event")
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	rule := response["rule"].(map[string]any)
	if rule["rule_id"] != "icon_override_contoso_agent" || rule["display_icon"] != `C:\Program Files\Contoso\agent.exe,2` || response["refresh_requested"] != true {
		t.Fatalf("unexpected response %#v", response)
	}
	content, err := os.ReadFile(iconPath)
	if err != nil {
		t.Fatalf("read icon override file: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(content, &persisted); err != nil {
		t.Fatalf("persisted decode failed: %v", err)
	}
	rows := persisted["windows_icon_overrides"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["rule_id"] != "icon_override_contoso_agent" {
		t.Fatalf("unexpected persisted rows %#v", persisted)
	}
}

func TestDeviceSoftwareUninstallBlockAndUnblockPersistsRules(t *testing.T) {
	blockPath := filepath.Join(t.TempDir(), "software_uninstall_blocklist.json")
	t.Setenv("BOREALIS_SOFTWARE_UNINSTALL_BLOCKLIST_PATH", blockPath)
	if err := os.WriteFile(blockPath, []byte(`{"windows_quiet_uninstall_blocklist":[]}`), 0o600); err != nil {
		t.Fatalf("seed blocklist: %v", err)
	}
	store := &fakeSoftwareIconStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		overrideSnapshot: softwareOverrideContext{
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			Software: []map[string]any{
				{
					"name":    "Contoso Agent",
					"version": "1.0",
					"source":  "local_installed",
					"metadata": map[string]any{
						"publisher":              "Contoso",
						"quiet_uninstall_string": `"C:\Program Files\Contoso\uninstall.exe" /S`,
						"uninstall_string":       `"C:\Program Files\Contoso\uninstall.exe"`,
					},
				},
			},
		},
	}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	blockBody := []byte(`{"name":"Contoso Agent","version":"1.0","source":"local_installed","reason":"Needs manual review"}`)
	blockRecorder := httptest.NewRecorder()
	blockRequest := httptest.NewRequest(http.MethodPost, "/api/device/software/LAB-OPERATOR-01/uninstall-block", bytes.NewReader(blockBody))
	blockRequest.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(blockRecorder, blockRequest)
	if blockRecorder.Code != http.StatusOK {
		t.Fatalf("expected block 200, got %d body=%s", blockRecorder.Code, blockRecorder.Body.String())
	}
	content, err := os.ReadFile(blockPath)
	if err != nil {
		t.Fatalf("read blocklist: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(content, &persisted); err != nil {
		t.Fatalf("persisted decode failed: %v", err)
	}
	rows := persisted["windows_quiet_uninstall_blocklist"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["rule_id"] != "uninstall_block_contoso_agent_1_0" {
		t.Fatalf("unexpected blocklist rows %#v", persisted)
	}

	unblockRecorder := httptest.NewRecorder()
	unblockRequest := httptest.NewRequest(http.MethodPost, "/api/device/software/LAB-OPERATOR-01/uninstall-unblock", bytes.NewReader(blockBody))
	unblockRequest.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(unblockRecorder, unblockRequest)
	if unblockRecorder.Code != http.StatusOK {
		t.Fatalf("expected unblock 200, got %d body=%s", unblockRecorder.Code, unblockRecorder.Body.String())
	}
	if err := json.Unmarshal(unblockRecorder.Body.Bytes(), &persisted); err != nil {
		t.Fatalf("unblock response decode failed: %v", err)
	}
	removed := persisted["removed_rule_ids"].([]any)
	if len(removed) != 1 || removed[0] != "uninstall_block_contoso_agent_1_0" {
		t.Fatalf("unexpected unblock response %#v", persisted)
	}
	content, err = os.ReadFile(blockPath)
	if err != nil {
		t.Fatalf("read blocklist after unblock: %v", err)
	}
	if err := json.Unmarshal(content, &persisted); err != nil {
		t.Fatalf("persisted decode failed: %v", err)
	}
	rows = persisted["windows_quiet_uninstall_blocklist"].([]any)
	if len(rows) != 0 {
		t.Fatalf("expected empty blocklist, got %#v", persisted)
	}
}

func TestDeviceSoftwareUninstallQueuesSignedQuickJob(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_CERT_ROOT", t.TempDir())
	var sawEvent bool
	var eventBody map[string]any
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(internalTokenHeader); got != goInternalToken([]byte("test-secret")) {
			t.Fatalf("unexpected internal token %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("worker request body decode failed: %v", err)
		}
		switch r.URL.Path {
		case "/remote-ops/host-service/status":
			if body["hostname"] != "LAB-OPERATOR-01" || body["service_mode"] != "system" {
				t.Fatalf("unexpected status body %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"registered": true})
		case "/remote-ops/host-service/event":
			sawEvent = true
			eventBody = body
			_ = json.NewEncoder(w).Encode(map[string]any{"emitted": true})
		default:
			t.Fatalf("unexpected worker path %s", r.URL.Path)
		}
	}))
	defer worker.Close()
	store := &fakeSoftwareIconStore{
		profile:        operatorProfile{Username: "operator", Role: "Admin"},
		nextActivityID: 4242,
		overrideSnapshot: softwareOverrideContext{
			Hostname:        "LAB-OPERATOR-01",
			AgentID:         "LAB-OPERATOR-01_SYSTEM",
			OperatingSystem: "Windows 11 Pro",
			Route:           routeForTestWorker(t, worker.URL),
			Software: []map[string]any{
				{
					"name":    "Contoso Agent",
					"version": "1.0",
					"source":  "local_installed",
					"metadata": map[string]any{
						"quiet_uninstall_string": `"C:\Program Files\Contoso\uninstall.exe" /S`,
						"uninstall_string":       `"C:\Program Files\Contoso\uninstall.exe"`,
					},
				},
			},
		},
	}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	body := []byte(`{"name":"Contoso Agent","version":"1.0","source":"local_installed"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/software/LAB-OPERATOR-01/uninstall", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawEvent {
		t.Fatalf("expected quick job event")
	}
	if store.insertedActivityHost != "LAB-OPERATOR-01" || store.insertedActivityName != "Uninstall - Contoso Agent" {
		t.Fatalf("unexpected activity insert host=%q name=%q metadata=%#v", store.insertedActivityHost, store.insertedActivityName, store.insertedMetadata)
	}
	if store.insertedMetadata["queue_lane"] != nil {
		t.Fatalf("queue lane belongs on row, not metadata: %#v", store.insertedMetadata)
	}
	if eventBody["hostname"] != "LAB-OPERATOR-01" || eventBody["service_mode"] != "system" || eventBody["event_name"] != "quick_job_run" {
		t.Fatalf("unexpected event body %#v", eventBody)
	}
	payload := eventBody["payload"].(map[string]any)
	if payload["job_id"].(float64) != 4242 || payload["script_path"] != windowsSoftwareUninstallPath || payload["run_mode"] != "system" {
		t.Fatalf("unexpected quick job payload %#v", payload)
	}
	env := payload["environment"].(map[string]any)
	if env["SOFTWARE_NAME"] != "Contoso Agent" || env["SOFTWARE_SOURCE"] != "local_installed" || env["QUIET_UNINSTALL_STRING"] == "" {
		t.Fatalf("unexpected environment %#v", env)
	}
	contextBlock := payload["context"].(map[string]any)
	if contextBlock["queue_lane"] != softwareUninstallQueueLane || contextBlock["activity_kind"] != softwareUninstallActivity {
		t.Fatalf("unexpected context %#v", contextBlock)
	}
	scriptBytes, err := base64.StdEncoding.DecodeString(payload["script_content"].(string))
	if err != nil {
		t.Fatalf("script decode failed: %v", err)
	}
	signature, err := base64.StdEncoding.DecodeString(payload["signature"].(string))
	if err != nil {
		t.Fatalf("signature decode failed: %v", err)
	}
	publicDER, err := base64.StdEncoding.DecodeString(payload["signing_key"].(string))
	if err != nil {
		t.Fatalf("signing key decode failed: %v", err)
	}
	publicAny, err := x509.ParsePKIXPublicKey(publicDER)
	if err != nil {
		t.Fatalf("signing key parse failed: %v", err)
	}
	publicKey, ok := publicAny.(ed25519.PublicKey)
	if !ok || !ed25519.Verify(publicKey, scriptBytes, signature) {
		t.Fatalf("signature did not verify")
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if response["status"] != "queued" || response["job_id"].(float64) != 4242 || response["script_name"] != "Uninstall - Contoso Agent" {
		t.Fatalf("unexpected response %#v", response)
	}
}

func TestSoftwareAuditHandlerReturnsRows(t *testing.T) {
	store := &fakeSoftwareIconStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		auditRows: []map[string]any{
			{
				"id":       int64(10),
				"name":     "7-Zip",
				"hostname": "LAB-01",
				"uninstall": map[string]any{
					"supported": true,
				},
			},
		},
	}
	mux := http.NewServeMux()
	registerSoftwareRoutes(mux, softwareIconTestAuth(store), http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/software/audit", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.auditProfile.Username != "operator" {
		t.Fatalf("expected profile propagation, got %#v", store.auditProfile)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response decode failed: %v", err)
	}
	if payload["count"].(float64) != 1 {
		t.Fatalf("unexpected count payload %#v", payload)
	}
	rows := payload["software"].([]any)
	row := rows[0].(map[string]any)
	if row["name"] != "7-Zip" || row["hostname"] != "LAB-01" {
		t.Fatalf("unexpected row %#v", row)
	}
}

func TestNormalizeDeviceServicePayloadMatchesPendingShape(t *testing.T) {
	raw := sqlNullString(`{"reported_at":100,"services":[{"name":"Spooler","displayName":"Print Spooler","status":"active","captured_at":101},{"service_name":"Borealis Agent","state":"stop-pending","pending_action":"restart","pending_requested_at":102,"pending_requested_by":"operator"}]}`)
	services, reportedAt := normalizeDeviceServicePayload(raw)
	if reportedAt != 101 {
		t.Fatalf("expected reported_at 101, got %d", reportedAt)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	if services[0]["name"] != "Borealis Agent" || services[0]["status_code"] != "stopping" || services[0]["desired_status"] != "running" {
		t.Fatalf("unexpected first service %#v", services[0])
	}
	if services[1]["display_name"] != "Print Spooler" || services[1]["status_code"] != "running" {
		t.Fatalf("unexpected second service %#v", services[1])
	}
}
