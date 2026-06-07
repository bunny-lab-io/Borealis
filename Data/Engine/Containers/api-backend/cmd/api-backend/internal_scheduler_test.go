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
