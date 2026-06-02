package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
