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

type clusterTestStore struct {
	profile       operatorProfile
	snapshot      map[string]any
	events        []map[string]any
	mutation      clusterMutation
	mutationActor string
	operation     map[string]any
	invitation    map[string]any
	admission     map[string]any
	approvedID    string
	retriedID     string
	cancelledID   string
	mutationErr   error
}

func (s *clusterTestStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	profile := s.profile
	if profile.Username == "" {
		profile.Username = username
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *clusterTestStore) clusterSnapshot(_ context.Context) (map[string]any, error) {
	return copyMap(s.snapshot), nil
}

func (s *clusterTestStore) clusterEvents(_ context.Context, _ int64) ([]map[string]any, error) {
	return append([]map[string]any(nil), s.events...), nil
}

func (s *clusterTestStore) createClusterOperation(_ context.Context, actor string, mutation clusterMutation) (map[string]any, error) {
	s.mutationActor = actor
	s.mutation = mutation
	if s.mutationErr != nil {
		return nil, s.mutationErr
	}
	if s.operation != nil {
		return copyMap(s.operation), nil
	}
	return map[string]any{"operation_id": "11111111-1111-4111-8111-111111111111", "state": "queued"}, nil
}

func (s *clusterTestStore) createClusterInvitation(_ context.Context, _ string, invitation map[string]any) error {
	s.invitation = copyMap(invitation)
	return s.mutationErr
}

func (s *clusterTestStore) consumeClusterInvitation(_ context.Context, admission map[string]any) (map[string]any, error) {
	s.admission = copyMap(admission)
	return map[string]any{"admission_id": admission["id"], "state": "Pending Quorum"}, s.mutationErr
}

func (s *clusterTestStore) approveClusterAdmission(_ context.Context, _ string, admissionID string) (map[string]any, error) {
	s.approvedID = admissionID
	return map[string]any{"state": "queued"}, s.mutationErr
}

func (s *clusterTestStore) retryClusterOperation(_ context.Context, _ string, operationID string) (map[string]any, error) {
	s.retriedID = operationID
	return map[string]any{"state": "queued"}, s.mutationErr
}

func (s *clusterTestStore) cancelClusterOperation(_ context.Context, _ string, operationID string) (map[string]any, error) {
	s.cancelledID = operationID
	return map[string]any{"state": "cancelled"}, s.mutationErr
}

func clusterTestAuth(t *testing.T, store *clusterTestStore) (*authService, string) {
	t.Helper()
	verifier := &tokenVerifier{secret: []byte("cluster-test-secret"), maxAge: time.Hour, now: time.Now}
	token, err := verifier.signPayload(map[string]any{"u": "operator", "r": store.profile.Role})
	if err != nil {
		t.Fatalf("sign auth token: %v", err)
	}
	return &authService{verifier: verifier, store: store, timeout: time.Second}, token
}

func clusterTestRequest(t *testing.T, method, path, body, token string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

func TestClusterHMRStartRequiresAdminAndExactConfirmation(t *testing.T) {
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "User"}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	request := clusterTestRequest(t, http.MethodPost, "/api/server/cluster/hmr/start", `{"node_id":"11111111-1111-4111-8111-111111111111","confirmation":"ENABLE HMR"}`, token)
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected non-admin rejection, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	store.profile.Role = "Admin"
	auth, token = clusterTestAuth(t, store)
	mux = http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	request = clusterTestRequest(t, http.MethodPost, "/api/server/cluster/hmr/start", `{"node_id":"11111111-1111-4111-8111-111111111111","confirmation":"yes"}`, token)
	recorder = httptest.NewRecorder()

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "ENABLE HMR") {
		t.Fatalf("expected confirmation validation, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestClusterMutationsRequireRecentStepUpAuthentication(t *testing.T) {
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	verifier := &tokenVerifier{secret: []byte("cluster-test-secret"), maxAge: time.Hour, now: func() time.Time { return now }}
	token, err := verifier.signPayload(map[string]any{"u": "operator", "r": "Admin"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	auth := &authService{verifier: verifier, store: store, timeout: time.Second}
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/hmr/start", `{"node_id":"11111111-1111-4111-8111-111111111111","confirmation":"ENABLE HMR"}`, token))
	if recorder.Code != http.StatusPreconditionRequired || !strings.Contains(recorder.Body.String(), "step_up_required") {
		t.Fatalf("expected step-up rejection, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestClusterHMRStartQueuesValidatedTarget(t *testing.T) {
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	request := clusterTestRequest(t, http.MethodPost, "/api/server/cluster/hmr/start", `{"node_id":"11111111-1111-4111-8111-111111111111","confirmation":"ENABLE HMR"}`, token)
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected accepted HMR operation, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.mutation.Kind != "hmr_start" || store.mutation.TargetNodeID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected mutation: %+v", store.mutation)
	}
}

func TestClusterEnableRemainsProbeConformanceGated(t *testing.T) {
	t.Setenv("BOREALIS_K3S_PROBE_CONFORMANCE", "failed")
	t.Setenv("BOREALIS_ENGINE_RELEASE_VERSION", "2026.08.23")
	t.Setenv("BOREALIS_ENGINE_SOURCE_SHA", strings.Repeat("a", 40))
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	request := clusterTestRequest(t, http.MethodPost, "/api/server/cluster/enable", `{"control_plane_vip":"10.20.30.10","edge_vip":"10.20.30.11","management_ip":"10.20.30.12","architecture":"amd64","node_name":"engine-1","confirmation":"ENABLE CLUSTER"}`, token)
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "probe_conformance") {
		t.Fatalf("expected conformance gate, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestClusterStableReleaseCatalogStopsAtCurrentAndPinsCommit(t *testing.T) {
	const commitSHA = "0123456789abcdef0123456789abcdef01234567"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases"):
			_ = json.NewEncoder(w).Encode([]clusterGitHubRelease{
				{TagName: "2026.08.9", Name: "2026.08.9 - Cluster Update", PublishedAt: "2026-08-24T00:00:00Z"},
				{TagName: "2026.08.8", Name: "2026.08.8 - Probe Fix", PublishedAt: "2026-08-23T00:00:00Z"},
				{TagName: "2026.08.7", Name: "2026.08.7 - Current", PublishedAt: "2026-08-21T00:00:00Z"},
				{TagName: "2026.08.6", Name: "2026.08.6 - Older", PublishedAt: "2026-08-17T00:00:00Z"},
			})
		case strings.Contains(r.URL.Path, "/git/ref/tags/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": commitSHA, "type": "commit"}})
		case strings.Contains(r.URL.Path, "/Data/Engine/release-manifest.json"):
			_ = json.NewEncoder(w).Encode(clusterReleaseManifest{SchemaVersion: 1, ClusterCompatible: true, MinimumRollingVersion: "2026.08.7", MaximumVersionSkewReleases: 1, DatabaseMigration: "expand-contract", RequiredK3sBaseline: "v1.36.3+k3s1", RequiredK3sConformance: "pod-restart-policy-startup-probe-v1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("BOREALIS_GITHUB_API_BASE_URL", server.URL)
	t.Setenv("BOREALIS_GITHUB_RAW_BASE_URL", server.URL)
	t.Setenv("BOREALIS_K3S_VERSION", "v1.36.3+k3s1")
	serverClusterReleaseCache = clusterReleaseCache{}
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, snapshot: map[string]any{"baseline_release": "2026.08.7", "active_size": int64(3)}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	request := clusterTestRequest(t, http.MethodGet, "/api/server/cluster/releases", "", token)
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected release catalog, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	releases, _ := payload["releases"].([]any)
	if len(releases) != 3 {
		t.Fatalf("expected newest through current only, got %d: %s", len(releases), recorder.Body.String())
	}
	first, _ := releases[0].(map[string]any)
	if first["tag"] != "2026.08.9" || first["title"] != "2026.08.9 - Cluster Update" || first["commit_sha"] != commitSHA {
		t.Fatalf("release metadata not resolved: %#v", first)
	}

	request = clusterTestRequest(t, http.MethodPost, "/api/server/cluster/updates", `{"scope":"node","node_ids":["11111111-1111-4111-8111-111111111111"],"release_tag":"2026.08.9","confirmation":"UPDATE CLUSTER"}`, token)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected update accepted, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.mutation.TargetSHA != commitSHA || store.mutation.TargetRelease != "2026.08.9" || store.mutation.TargetNodeID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("update did not pin release SHA: %+v", store.mutation)
	}
}

func TestClusterGitHubJSONUsesTokenForConfiguredAPIOrigin(t *testing.T) {
	seen := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()
	t.Setenv("BOREALIS_GITHUB_API_BASE_URL", server.URL)
	ctx := context.WithValue(context.Background(), clusterGitHubTokenContextKey{}, "configured-token")
	var payload map[string]any
	if err := clusterGitHubJSON(ctx, server.URL+"/repos/bunny-lab-io/Borealis", &payload); err != nil {
		t.Fatal(err)
	}
	if seen != "Bearer configured-token" {
		t.Fatalf("expected configured token header, got %q", seen)
	}
}

func TestClusterUpdateRejectsUnexpectedFieldsAndInvalidUUID(t *testing.T) {
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, snapshot: map[string]any{"baseline_release": "2026.08.7", "active_size": int64(3)}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	request := clusterTestRequest(t, http.MethodPost, "/api/server/cluster/updates", `{"scope":"node","node_ids":["node-1"],"release_tag":"main","confirmation":"UPDATE CLUSTER","command":"rm"}`, token)
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected validation failure, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, field := range []string{"node_ids[0]", "release_tag", "command"} {
		if !strings.Contains(recorder.Body.String(), field) {
			t.Fatalf("validation response missing %s: %s", field, recorder.Body.String())
		}
	}
}

func TestClusterOneNodeUpdateRequiresOutageAcknowledgement(t *testing.T) {
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, snapshot: map[string]any{"baseline_release": "2026.08.7", "active_size": int64(1)}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	request := clusterTestRequest(t, http.MethodPost, "/api/server/cluster/updates", `{"scope":"node","node_ids":["11111111-1111-4111-8111-111111111111"],"release_tag":"2026.08.9","confirmation":"UPDATE CLUSTER"}`, token)
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "ACCEPT OUTAGE") {
		t.Fatalf("expected one-node outage acknowledgement, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestClusterInputClassesEnforceIPv4UbuntuAndOperationalLimits(t *testing.T) {
	if len(validateClusterIP("management_ip", "2001:db8::1")) == 0 || len(validateClusterIP("management_ip", "10.20.30.40")) != 0 {
		t.Fatal("cluster management addresses must be IPv4")
	}
	if !clusterSupportedUbuntu("Ubuntu 24.04") || !clusterSupportedUbuntu("Ubuntu 26.04 LTS") || clusterSupportedUbuntu("Ubuntu 22.04") || clusterSupportedUbuntu("Debian 13") {
		t.Fatal("unexpected Ubuntu baseline validation")
	}
	if len(validateClusterReason(strings.Repeat("x", clusterReasonMaxLength+1))) == 0 || len(validateClusterReason("line one\nline two")) == 0 {
		t.Fatal("cluster reason length/control validation missing")
	}
	if len(validateClusterRelease(strings.Repeat("1", clusterReleaseMaxLength+1))) == 0 || len(validateClusterNodeName("node_name", strings.Repeat("n", clusterNodeNameMaxLength+1))) == 0 {
		t.Fatal("release or node-name maximum missing")
	}
}

func TestClusterJoinRejectsOversizedInviteBeforeAuthentication(t *testing.T) {
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth, _ := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	body := `{"invite_bundle":"` + strings.Repeat("x", clusterInviteMaxBytes+1) + `","node_name":"engine-2","hostname":"engine-2","management_ip":"10.20.30.42","architecture":"amd64","os_version":"Ubuntu 24.04"}`
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/bootstrap/cluster/join", body, ""))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "16 KiB") {
		t.Fatalf("expected invite size rejection, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
