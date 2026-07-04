package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testInternalSchedulerAuth() *authService {
	return &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		timeout: time.Second,
	}
}

func TestInternalSchedulerPublicBaseURLRequiresInternalToken(t *testing.T) {
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, testInternalSchedulerAuth(), nil, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/public-base-url", nil)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestInternalSchedulerPublicBaseURLReturnsConfiguredURL(t *testing.T) {
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.example.test/")
	auth := testInternalSchedulerAuth()
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, nil, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/public-base-url", nil)
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["public_base_url"] != "https://borealis.example.test" {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

type fakeInternalSchedulerCredentialStore struct {
	*fakeOperatorStore
	credential map[string]any
	found      bool
	err        error
	idSeen     int64
	secretSeen bool
}

func (s *fakeInternalSchedulerCredentialStore) loadDecryptedSchedulerCredential(_ context.Context, secret authSecretService, credentialID int64) (map[string]any, bool, error) {
	s.idSeen = credentialID
	s.secretSeen = secret != nil
	if s.err != nil {
		return nil, false, s.err
	}
	return copyMap(s.credential), s.found, nil
}

type fakeInternalSchedulerServiceAccountStore struct {
	*fakeOperatorStore
	serviceAccount map[string]any
	found          bool
	err            error
	agentSeen      string
}

func (s *fakeInternalSchedulerServiceAccountStore) loadSchedulerServiceAccount(_ context.Context, agentID string) (map[string]any, bool, error) {
	s.agentSeen = agentID
	if s.err != nil {
		return nil, false, s.err
	}
	return copyMap(s.serviceAccount), s.found, nil
}

type fakeInternalSchedulerOnlineHostStore struct {
	*fakeOperatorStore
	hostnames  []string
	err        error
	windowSeen int64
}

func (s *fakeInternalSchedulerOnlineHostStore) loadSchedulerOnlineHostnames(_ context.Context, windowSeconds int64) ([]string, error) {
	s.windowSeen = windowSeconds
	if s.err != nil {
		return nil, s.err
	}
	return append([]string{}, s.hostnames...), nil
}

type fakeInternalSchedulerOnlineSiteStore struct {
	*fakeOperatorStore
	counts     map[int64]int64
	err        error
	windowSeen int64
	siteIDs    []int64
}

func (s *fakeInternalSchedulerOnlineSiteStore) loadSchedulerOnlineSites(_ context.Context, windowSeconds int64, siteIDs []int64) (map[int64]int64, error) {
	s.windowSeen = windowSeconds
	s.siteIDs = append([]int64{}, siteIDs...)
	if s.err != nil {
		return nil, s.err
	}
	out := map[int64]int64{}
	for key, value := range s.counts {
		out[key] = value
	}
	return out, nil
}

func TestInternalSchedulerCredentialReturnsDecryptedPayload(t *testing.T) {
	auth, baseStore := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store := &fakeInternalSchedulerCredentialStore{
		fakeOperatorStore: baseStore,
		found:             true,
		credential: map[string]any{
			"id":              int64(42),
			"name":            "Lab SSH",
			"username":        "operator",
			"password":        "secret",
			"connection_type": "ssh",
		},
	}
	auth.store = store
	auth.aegis = &authLoginTestAegis{unlockedCipher: "ready"}
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, nil, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/credential/42", nil)
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.idSeen != 42 || !store.secretSeen {
		t.Fatalf("expected credential lookup with Aegis, id=%d secret=%v", store.idSeen, store.secretSeen)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	credential := payload["credential"].(map[string]any)
	if credential["password"] != "secret" || credential["username"] != "operator" {
		t.Fatalf("unexpected credential payload %#v", credential)
	}
}

func TestInternalSchedulerServiceAccountReturnsPayload(t *testing.T) {
	auth, baseStore := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store := &fakeInternalSchedulerServiceAccountStore{
		fakeOperatorStore: baseStore,
		found:             true,
		serviceAccount: map[string]any{
			"agent_id": "agent-01",
			"username": "svc-agent-01",
			"password": "secret",
		},
	}
	auth.store = store
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, nil, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/service-account/agent-01", nil)
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.agentSeen != "agent-01" {
		t.Fatalf("expected agent lookup, got %q", store.agentSeen)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	serviceAccount := payload["service_account"].(map[string]any)
	if serviceAccount["username"] != "svc-agent-01" || serviceAccount["password"] != "secret" {
		t.Fatalf("unexpected service account payload %#v", serviceAccount)
	}
}

func TestInternalSchedulerOnlineHostsReturnsVariants(t *testing.T) {
	auth, baseStore := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store := &fakeInternalSchedulerOnlineHostStore{
		fakeOperatorStore: baseStore,
		hostnames:         []string{"Lab-01"},
	}
	auth.store = store
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, nil, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/online-hosts?window_seconds=120", nil)
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.windowSeen != 120 {
		t.Fatalf("expected custom window, got %d", store.windowSeen)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	hostnames := payload["hostnames"].([]any)
	if len(hostnames) != 3 || hostnames[0] != "Lab-01" || hostnames[1] != "LAB-01" || hostnames[2] != "lab-01" {
		t.Fatalf("unexpected hostnames %#v", hostnames)
	}
}

func TestInternalSchedulerOnlineSitesReturnsCounts(t *testing.T) {
	auth, baseStore := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store := &fakeInternalSchedulerOnlineSiteStore{
		fakeOperatorStore: baseStore,
		counts:            map[int64]int64{3: 2},
	}
	auth.store = store
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, nil, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/online-sites?site_id=3&site_ids=5,6&window_seconds=90", nil)
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.windowSeen != 90 {
		t.Fatalf("expected custom window, got %d", store.windowSeen)
	}
	if len(store.siteIDs) != 3 || store.siteIDs[0] != 3 || store.siteIDs[1] != 5 || store.siteIDs[2] != 6 {
		t.Fatalf("unexpected site filter %#v", store.siteIDs)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	siteIDs := payload["site_ids"].([]any)
	counts := payload["site_online_device_counts"].(map[string]any)
	if len(siteIDs) != 1 || siteIDs[0] != float64(3) || counts["3"] != float64(2) {
		t.Fatalf("unexpected online site payload %#v", payload)
	}
}

func TestInternalSchedulerCredentialMapsResetRequired(t *testing.T) {
	auth, baseStore := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store := &fakeInternalSchedulerCredentialStore{
		fakeOperatorStore: baseStore,
		err:               errSchedulerCredentialResetRequired,
	}
	auth.store = store
	auth.aegis = &authLoginTestAegis{unlockedCipher: "ready"}
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, nil, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/credential/42", nil)
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["error"] != "credential_reset_required" {
		t.Fatalf("unexpected reset payload %#v", payload)
	}
}

type fakeInternalSchedulerWorkItemStore struct {
	*fakeOperatorStore
	kind    string
	payload map[string]any
	workID  int64
	err     error
}

func (s *fakeInternalSchedulerWorkItemStore) enqueueInternalSchedulerWorkItem(_ context.Context, kind string, payload map[string]any) (int64, error) {
	s.kind = kind
	s.payload = copyMap(payload)
	if s.err != nil {
		return 0, s.err
	}
	return s.workID, nil
}

func TestInternalSchedulerWorkItemsEnqueuesThroughStore(t *testing.T) {
	auth, baseStore := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store := &fakeInternalSchedulerWorkItemStore{
		fakeOperatorStore: baseStore,
		workID:            77,
	}
	auth.store = store
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, nil, http.NotFoundHandler())

	body := []byte(`{"kind":"scheduled_run","job_id":12,"run_id":34,"scheduled_ts":1700000300,"site_id":5}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/internal/job-scheduler/work-items", bytes.NewReader(body))
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.kind != schedulerKindScheduledRun || coerceInt64(store.payload["run_id"]) != 34 {
		t.Fatalf("unexpected store call kind=%s payload=%#v", store.kind, store.payload)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["work_id"] != float64(77) {
		t.Fatalf("unexpected response %#v", payload)
	}
}

func TestInternalSchedulerWorkItemsRequiresInternalToken(t *testing.T) {
	auth, baseStore := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	auth.store = &fakeInternalSchedulerWorkItemStore{fakeOperatorStore: baseStore, workID: 77}
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, nil, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/internal/job-scheduler/work-items", bytes.NewReader([]byte(`{"kind":"scheduled_run"}`)))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSchedulerWorkItemPayloadsMatchPythonQueue(t *testing.T) {
	item, updateRun, err := schedulerWorkItemFromPayload(schedulerKindOnboardingRun, map[string]any{
		"job_id":        float64(8),
		"run_row_id":    float64(9),
		"scheduled_ts":  float64(1700000400),
		"site_id":       float64(3),
		"components":    []any{map[string]any{"kind": "onboarding"}},
		"targets":       []any{map[string]any{"hostname": "LAB-01"}},
		"credential_id": float64(6),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updateRun || item.DedupeKey != "onboarding:9" || item.Kind != schedulerKindOnboardingRun || item.Lane != schedulerLaneOnboarding || item.Priority != 50 {
		t.Fatalf("unexpected onboarding item %#v update=%v", item, updateRun)
	}
	if item.SiteID.Int64 != 3 || item.JobID.Int64 != 8 || item.RunID.Int64 != 9 {
		t.Fatalf("unexpected ids %#v", item)
	}
	if item.Payload["credential_id"] != int64(6) {
		t.Fatalf("unexpected payload %#v", item.Payload)
	}

	item, updateRun, err = schedulerWorkItemFromPayload(schedulerKindScheduledRun, map[string]any{
		"job_id":              float64(10),
		"run_id":              float64(11),
		"scheduled_ts":        float64(1700000500),
		"site_id":             float64(4),
		"run_mode":            "ssh",
		"target_row_ids":      []any{float64(7), float64(8)},
		"use_service_account": true,
		"shared_execution":    true,
		"component_index":     float64(2),
		"task_link":           map[string]any{"kind": "ansible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updateRun || item.DedupeKey != "scheduled-run:11:7,8" || item.Kind != schedulerKindScheduledRun || item.Lane != schedulerLaneScheduledJob || item.Priority != 40 {
		t.Fatalf("unexpected scheduled item %#v update=%v", item, updateRun)
	}
	targets := item.Payload["target_row_ids"].([]int64)
	if len(targets) != 2 || targets[0] != 7 || item.Payload["run_mode"] != "ssh" || item.Payload["component_index"] != int64(2) {
		t.Fatalf("unexpected scheduled payload %#v", item.Payload)
	}

	item, updateRun, err = schedulerWorkItemFromPayload(schedulerKindScheduledWorkflowRun, map[string]any{
		"job_id":             float64(12),
		"run_id":             float64(13),
		"scheduled_ts":       float64(1700000600),
		"site_id":            float64(5),
		"workflow_component": map[string]any{"id": "workflow-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updateRun || item.DedupeKey != "scheduled-workflow:13:5" || item.Kind != schedulerKindScheduledWorkflowRun {
		t.Fatalf("unexpected workflow item %#v update=%v", item, updateRun)
	}
	scope := item.Payload["workflow_site_scope"].(map[string]any)
	if scope["site_id"] != int64(5) {
		t.Fatalf("unexpected workflow payload %#v", item.Payload)
	}

	item, updateRun, err = schedulerWorkItemFromPayload(schedulerKindPatchInstallRun, map[string]any{
		"job_id":          float64(14),
		"run_id":          float64(15),
		"scheduled_ts":    float64(1700000700),
		"site_id":         float64(7),
		"hostname":        "LAB-OPERATOR-01",
		"patch_component": map[string]any{"kind": "patch_install", "patch": map[string]any{"patch_key": "kb:KB5050533:state:pending"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updateRun || item.DedupeKey != "patch-install:15" || item.Kind != schedulerKindPatchInstallRun || item.Lane != schedulerLaneScheduledJob || item.Priority != 46 {
		t.Fatalf("unexpected patch install item %#v update=%v", item, updateRun)
	}
	if item.Payload["hostname"] != "LAB-OPERATOR-01" || schedulerAnyMap(item.Payload["patch_component"])["kind"] != "patch_install" {
		t.Fatalf("unexpected patch install payload %#v", item.Payload)
	}
}

func TestSchedulerDecryptedCredentialPayload(t *testing.T) {
	row := credentialRow{
		ID:                      sql.NullInt64{Int64: 7, Valid: true},
		Name:                    sql.NullString{String: "Lab SSH", Valid: true},
		SiteID:                  sql.NullInt64{Int64: 3, Valid: true},
		CredentialType:          sql.NullString{String: "Machine", Valid: true},
		ConnectionType:          sql.NullString{String: "SSH", Valid: true},
		Username:                sql.NullString{String: "operator", Valid: true},
		PasswordEncrypted:       []byte("enc:password"),
		PrivateKeyEncrypted:     []byte("enc:key"),
		PrivateKeyPassphrase:    []byte("enc:phrase"),
		BecomeMethod:            sql.NullString{String: "sudo", Valid: true},
		BecomeUsername:          sql.NullString{String: "root", Valid: true},
		BecomePasswordEncrypted: []byte("enc:become"),
		MetadataJSON:            sql.NullString{String: `{"scope":"lab"}`, Valid: true},
	}
	credential, err := schedulerDecryptedCredentialPayload(context.Background(), &authLoginTestAegis{unlockedCipher: "ready"}, row)
	if err != nil {
		t.Fatal(err)
	}
	if credential["password"] != "password" || credential["private_key"] != "key" || credential["become_password"] != "become" {
		t.Fatalf("unexpected decrypted credential %#v", credential)
	}
	if credential["credential_type"] != "machine" || credential["connection_type"] != "ssh" {
		t.Fatalf("unexpected normalized types %#v", credential)
	}
}

func TestSchedulerDecryptedCredentialPayloadBlocksResetRequired(t *testing.T) {
	row := credentialRow{
		ID:           sql.NullInt64{Int64: 7, Valid: true},
		MetadataJSON: sql.NullString{String: `{"aegis_secret_state":"reset_required","aegis_lost_secret_fields":["password"]}`, Valid: true},
	}
	_, err := schedulerDecryptedCredentialPayload(context.Background(), &authLoginTestAegis{unlockedCipher: "ready"}, row)
	if !errors.Is(err, errSchedulerCredentialResetRequired) {
		t.Fatalf("expected reset required, got %v", err)
	}
}

func TestInternalSchedulerHostServiceEventRoutesToWorker(t *testing.T) {
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
		if body["hostname"] != "LAB-OPERATOR-01" || body["service_mode"] != "system" || body["event_name"] != "quick_job_run" {
			t.Fatalf("unexpected event body %#v", body)
		}
		if body["allow_pending"] != true || body["pending_ttl_seconds"].(float64) != 240 {
			t.Fatalf("pending flags missing %#v", body)
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
	auth := processTestAuth(store)
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, nil, http.NotFoundHandler())

	body := []byte(`{"hostname":"LAB-OPERATOR-01","event_name":"quick_job_run","payload":{"job_id":1},"allow_pending":true,"pending_ttl_seconds":240}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/internal/job-scheduler/host-service-event", bytes.NewReader(body))
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !sawEvent {
		t.Fatalf("expected worker event")
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["queued"] != true || payload["emitted"] != false {
		t.Fatalf("unexpected response %#v", payload)
	}
}

type fakeSchedulerWireGuardBackend struct{}

func (fakeSchedulerWireGuardBackend) serverPublicKey() string { return "server-public" }

func (fakeSchedulerWireGuardBackend) buildPeerProfile(agentID string, virtualIP string, allowedPorts []int) map[string]any {
	return map[string]any{"agent_id": agentID, "virtual_ip": virtualIP, "allowed_ports": allowedPorts}
}

func (fakeSchedulerWireGuardBackend) upsertPeer(map[string]any) error { return nil }
func (fakeSchedulerWireGuardBackend) removePeer(string, string) error { return nil }
func (fakeSchedulerWireGuardBackend) reconcilePeers([]map[string]any) error {
	return nil
}
func (fakeSchedulerWireGuardBackend) checkListenerHealth(int) map[string]any {
	return map[string]any{"healthy": true, "reason": "listener_running", "peer_count": 1}
}
func (fakeSchedulerWireGuardBackend) checkPeerHealth(string) map[string]any {
	return map[string]any{"healthy": true, "peer_present": true, "reason": "peer_ready"}
}

func TestInternalSchedulerVPNSessionsReturnsRuntimeSnapshot(t *testing.T) {
	auth := testInternalSchedulerAuth()
	now := time.Unix(1700000100, 0).UTC()
	session := &vpnSession{
		TunnelID:         "tunnel-1",
		AgentID:          "LAB-01_SYSTEM",
		VirtualIP:        "10.255.0.2/32",
		ClientPublicKey:  "client-public",
		ClientPrivateKey: "client-private",
		AllowedPorts:     []int{22, 5900},
		CreatedAt:        now,
		ExpiresAt:        now.Add(5 * time.Minute),
		LastActivity:     now,
		Operators:        map[string]struct{}{},
	}
	vpnRuntime := &vpnTunnelService{
		auth:            auth,
		wg:              fakeSchedulerWireGuardBackend{},
		sessionsByAgent: map[string]*vpnSession{session.AgentID: session},
		sessionsByID:    map[string]*vpnSession{session.TunnelID: session},
	}
	mux := http.NewServeMux()
	registerInternalSchedulerRoutes(mux, auth, vpnRuntime, http.NotFoundHandler())

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/internal/job-scheduler/vpn-sessions", nil)
	request.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	sessions := payload["sessions"].(map[string]any)
	row := sessions["LAB-01_SYSTEM"].(map[string]any)
	if row["agent_id"] != "LAB-01_SYSTEM" || row["tunnel_id"] != "tunnel-1" || row["transport_ready"] != true {
		t.Fatalf("unexpected vpn session payload %#v", row)
	}
}
