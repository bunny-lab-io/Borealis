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

func (s *clusterTestStore) clusterAdmissionStatus(_ context.Context, id string, claims map[string]any) (map[string]any, error) {
	s.admission = copyMap(claims)
	s.admission["id"] = id
	return map[string]any{"admission_id": id, "state": "Approved", "events": s.events}, s.mutationErr
}

func (s *clusterTestStore) approveClusterAdmission(_ context.Context, _ string, admissionID string) (map[string]any, error) {
	s.approvedID = admissionID
	return map[string]any{"state": "queued"}, s.mutationErr
}

func (s *clusterTestStore) cancelClusterAdmission(_ context.Context, _ string, admissionID string) (map[string]any, error) {
	s.cancelledID = admissionID
	return map[string]any{"admission_id": admissionID, "state": "Cancelled"}, s.mutationErr
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

func TestClusterMutationsAcceptValidAdminSessionWithoutFreshStepUp(t *testing.T) {
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
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected valid Admin session acceptance without step-up, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.mutation.Kind != "hmr_start" || store.mutation.TargetNodeID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected mutation: %+v", store.mutation)
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
	t.Setenv("BOREALIS_K3S_VERSION", "v1.36.3+k3s1")
	t.Setenv("BOREALIS_ENGINE_NODE_NAME", "engine-1")
	t.Setenv("BOREALIS_ENGINE_MANAGEMENT_IP", "10.20.30.12")
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	request := clusterTestRequest(t, http.MethodPost, "/api/server/cluster/enable", `{"cluster_vip":"10.20.30.10"}`, token)
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "probe_conformance") {
		t.Fatalf("expected conformance gate, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestClusterEnableDerivesAMD64NodeIdentityAndSynchronizesVIPStorage(t *testing.T) {
	t.Setenv("BOREALIS_K3S_PROBE_CONFORMANCE", "passed")
	t.Setenv("BOREALIS_ENGINE_RELEASE_VERSION", "dev-aaaaaaaaaaaa")
	t.Setenv("BOREALIS_ENGINE_SOURCE_SHA", strings.Repeat("a", 40))
	t.Setenv("BOREALIS_K3S_VERSION", "v1.36.3+k3s1")
	t.Setenv("BOREALIS_ENGINE_NODE_NAME", "ENGINE-1")
	t.Setenv("BOREALIS_ENGINE_MANAGEMENT_IP", "10.20.30.12")
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/enable", `{"cluster_vip":"10.20.30.10"}`, token))

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected cluster enable acceptance, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := store.mutation.Payload
	if store.mutation.Kind != "cluster_enable" || payload["cluster_vip"] != "10.20.30.10" || payload["control_plane_vip"] != "10.20.30.10" || payload["edge_vip"] != "10.20.30.10" {
		t.Fatalf("Cluster Virtual IP contract was not synchronized: %+v", store.mutation)
	}
	if payload["node_name"] != "engine-1" || payload["management_ip"] != "10.20.30.12" || payload["architecture"] != "amd64" {
		t.Fatalf("local node identity was not derived: %#v", payload)
	}
	if payload["baseline_release"] != "dev-aaaaaaaaaaaa" || payload["baseline_sha"] != strings.Repeat("a", 40) {
		t.Fatalf("development baseline was not pinned: %#v", payload)
	}
}

func TestClusterEnableRejectsRetiredTypedConfirmationField(t *testing.T) {
	t.Setenv("BOREALIS_K3S_PROBE_CONFORMANCE", "passed")
	t.Setenv("BOREALIS_ENGINE_RELEASE_VERSION", "dev-aaaaaaaaaaaa")
	t.Setenv("BOREALIS_ENGINE_SOURCE_SHA", strings.Repeat("a", 40))
	t.Setenv("BOREALIS_K3S_VERSION", "v1.36.3+k3s1")
	t.Setenv("BOREALIS_ENGINE_NODE_NAME", "engine-1")
	t.Setenv("BOREALIS_ENGINE_MANAGEMENT_IP", "10.20.30.12")
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/enable", `{"cluster_vip":"10.20.30.10","confirmation":"ENABLE CLUSTER"}`, token))

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"field":"confirmation"`) || !strings.Contains(recorder.Body.String(), "field is not allowed") {
		t.Fatalf("expected retired confirmation rejection, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestValidClusterBaselineReleaseRequiresDevelopmentNameToMatchSHA(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	for _, test := range []struct {
		release string
		sha     string
		want    bool
	}{
		{release: "2026.09.1", sha: sha, want: true},
		{release: "2026.09.1-rc.2", sha: sha, want: true},
		{release: "2026.09.1-rc.0", sha: sha, want: false},
		{release: "dev-0123456789ab", sha: sha, want: true},
		{release: "dev-fedcba987654", sha: sha, want: false},
		{release: "dev-0123456789ab", sha: "not-a-sha", want: false},
		{release: "main", sha: sha, want: false},
	} {
		if got := validClusterBaselineRelease(test.release, test.sha); got != test.want {
			t.Fatalf("validClusterBaselineRelease(%q, %q)=%v want %v", test.release, test.sha, got, test.want)
		}
	}
}

func TestClusterProbeConformancePayloadRequiresMultiTrialContract(t *testing.T) {
	t.Setenv("BOREALIS_K3S_PROBE_CONFORMANCE", "passed")
	payload := clusterProbeConformancePayload()
	if payload["id"] != "pod-restart-policy-liveness-delay-guard-v1" || payload["required_consecutive_trials"] != 10 || payload["cluster_activation_allowed"] != true {
		t.Fatalf("unexpected probe conformance contract: %#v", payload)
	}
}

func TestClusterStableReleaseCatalogStopsAtCurrentAndPinsCommit(t *testing.T) {
	const commitSHA = "0123456789abcdef0123456789abcdef01234567"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			_ = json.NewEncoder(w).Encode(clusterGitHubRelease{Immutable: true, TagName: "2026.08.9", Name: "2026.08.9 - Cluster Update"})
		case strings.Contains(r.URL.Path, "/releases"):
			_ = json.NewEncoder(w).Encode([]clusterGitHubRelease{
				{Immutable: true, TagName: "2026.08.12", Name: "Draft", Draft: true, PublishedAt: "2026-08-25T03:00:00Z"},
				{Immutable: true, TagName: "2026.08.11", Name: "Prerelease", Prerelease: true, PublishedAt: "2026-08-25T02:00:00Z"},
				{Immutable: true, TagName: "main", Name: "Branch Head", PublishedAt: "2026-08-25T01:00:00Z"},
				// GitHub release ordering is not semantic-version ordering. A
				// newly published backport must not hide newer rolling targets.
				{Immutable: true, TagName: "2026.08.6", Name: "Out-of-order Backport", PublishedAt: "2026-08-25T00:00:00Z"},
				{Immutable: true, TagName: "2026.08.9", Name: "2026.08.9 - Cluster Update", PublishedAt: "2026-08-24T00:00:00Z"},
				{Immutable: true, TagName: "2026.08.8", Name: "2026.08.8 - Probe Fix", PublishedAt: "2026-08-23T00:00:00Z"},
				{Immutable: true, TagName: "2026.08.7", Name: "2026.08.7 - Current", PublishedAt: "2026-08-21T00:00:00Z"},
			})
		case strings.Contains(r.URL.Path, "/git/ref/tags/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": commitSHA, "type": "commit"}})
		case strings.Contains(r.URL.Path, "/Data/Engine/release-manifest.json"):
			_ = json.NewEncoder(w).Encode(clusterReleaseManifest{SchemaVersion: 1, ClusterCompatible: true, AllowedReleaseChannels: []string{"stable", "qualification"}, MinimumRollingVersion: "2026.08.7", MaximumVersionSkewReleases: 1, DatabaseMigration: "expand-contract", RequiredK3sBaseline: "v1.36.3+k3s1", RequiredK3sConformance: "pod-restart-policy-liveness-delay-guard-v1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("BOREALIS_GITHUB_API_BASE_URL", server.URL)
	t.Setenv("BOREALIS_GITHUB_RAW_BASE_URL", server.URL)
	t.Setenv("BOREALIS_K3S_VERSION", "v1.36.3+k3s1")
	serverClusterReleaseCache = clusterReleaseCache{}
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, snapshot: map[string]any{"baseline_release": "2026.08.7", "baseline_sha": commitSHA, "active_size": int64(3), "config": map[string]any{"k3s_version": "v1.36.3+k3s1"}}}
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

func TestClusterDevelopmentBaselineCatalogStopsAfterFirstPageAndSelectsApprovedChannels(t *testing.T) {
	const baselineSHA = "fedcba9876543210fedcba9876543210fedcba98"
	const commitSHA = "0123456789abcdef0123456789abcdef01234567"
	releasePageRequests := 0
	compareRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases"):
			releasePageRequests++
			if r.URL.Query().Get("page") != "1" {
				http.Error(w, "unexpected historical release page", http.StatusGatewayTimeout)
				return
			}
			_ = json.NewEncoder(w).Encode([]clusterGitHubRelease{
				{Immutable: true, TagName: "2026.09.1", Name: "First Stable"},
				{Immutable: true, TagName: "2026.09.1-rc.1", Name: "Qualification", Prerelease: true},
			})
		case strings.Contains(r.URL.Path, "/git/ref/tags/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": commitSHA, "type": "commit"}})
		case strings.Contains(r.URL.Path, "/compare/"):
			compareRequests++
			if !strings.HasSuffix(r.URL.Path, "/compare/"+baselineSHA+"..."+commitSHA) {
				http.Error(w, "unexpected comparison", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ahead"})
		case strings.Contains(r.URL.Path, "/Data/Engine/release-manifest.json"):
			_ = json.NewEncoder(w).Encode(clusterReleaseManifest{SchemaVersion: 1, ClusterCompatible: true, AllowedReleaseChannels: []string{"stable", "qualification"}, MinimumRollingVersion: "2026.09.1", MaximumVersionSkewReleases: 1, DatabaseMigration: "expand-contract", RequiredK3sBaseline: "v1.36.3+k3s1", RequiredK3sConformance: "pod-restart-policy-liveness-delay-guard-v1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("BOREALIS_GITHUB_API_BASE_URL", server.URL)
	t.Setenv("BOREALIS_GITHUB_RAW_BASE_URL", server.URL)
	t.Setenv("BOREALIS_K3S_VERSION", "v1.36.3+k3s1")
	serverClusterReleaseCache = clusterReleaseCache{}

	entries, err := fetchClusterReleaseCatalog(context.Background(), "dev-fedcba987654", baselineSHA, "v1.36.3+k3s1")
	if err != nil {
		t.Fatal(err)
	}
	if releasePageRequests != 1 {
		t.Fatalf("development baseline requested %d release pages, want 1", releasePageRequests)
	}
	if compareRequests != 2 {
		t.Fatalf("development baseline requested %d ancestry comparisons, want 2", compareRequests)
	}
	if len(entries) != 2 || entries[0]["tag"] != "2026.09.1" || entries[0]["channel"] != "stable" || entries[1]["tag"] != "2026.09.1-rc.1" || entries[1]["channel"] != "qualification" || entries[0]["selectable"] != true || entries[1]["selectable"] != true {
		t.Fatalf("approved stable and qualification releases should be selectable from development baseline: %#v", entries)
	}
}

func TestClusterDevelopmentBaselineAcceptsApprovedQualificationPrerelease(t *testing.T) {
	const baselineSHA = "fedcba9876543210fedcba9876543210fedcba98"
	const targetSHA = "0123456789abcdef0123456789abcdef01234567"
	releasePageRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			_ = json.NewEncoder(w).Encode(clusterGitHubRelease{Immutable: true, TagName: "2026.09.1-rc.1", Name: "Cluster Qualification", Prerelease: true})
		case strings.Contains(r.URL.Path, "/releases"):
			releasePageRequests++
			if r.URL.Query().Get("page") != "1" {
				http.Error(w, "unexpected historical release page", http.StatusGatewayTimeout)
				return
			}
			_ = json.NewEncoder(w).Encode([]clusterGitHubRelease{
				{Immutable: true, TagName: "2026.09.1-rc.1", Name: "Cluster Qualification", Prerelease: true},
				{Immutable: true, TagName: "2026.09.2", Name: "Mismatched Stable", Prerelease: true},
				{Immutable: true, TagName: "main", Name: "Branch Head"},
			})
		case strings.Contains(r.URL.Path, "/git/ref/tags/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": targetSHA, "type": "commit"}})
		case strings.Contains(r.URL.Path, "/compare/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ahead"})
		case strings.Contains(r.URL.Path, "/Data/Engine/release-manifest.json"):
			_ = json.NewEncoder(w).Encode(clusterReleaseManifest{SchemaVersion: 1, ClusterCompatible: true, AllowedReleaseChannels: []string{"qualification"}, MinimumRollingVersion: "2026.09.1", MaximumVersionSkewReleases: 1, DatabaseMigration: "expand-contract", RequiredK3sBaseline: "v1.36.3+k3s1", RequiredK3sConformance: "pod-restart-policy-liveness-delay-guard-v1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("BOREALIS_GITHUB_API_BASE_URL", server.URL)
	t.Setenv("BOREALIS_GITHUB_RAW_BASE_URL", server.URL)
	t.Setenv("BOREALIS_K3S_VERSION", "v1.36.3+k3s1")
	serverClusterReleaseCache = clusterReleaseCache{}

	entries, err := fetchClusterReleaseCatalog(context.Background(), "dev-fedcba987654", baselineSHA, "v1.36.3+k3s1")
	if err != nil {
		t.Fatal(err)
	}
	if releasePageRequests != 1 {
		t.Fatalf("development baseline requested %d release pages, want 1", releasePageRequests)
	}
	if len(entries) != 1 || entries[0]["tag"] != "2026.09.1-rc.1" || entries[0]["channel"] != "qualification" || entries[0]["selectable"] != true {
		t.Fatalf("approved qualification prerelease should be selectable and mismatched metadata rejected: %#v", entries)
	}
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, snapshot: map[string]any{"baseline_release": "dev-fedcba987654", "baseline_sha": baselineSHA, "release_channel": "development", "active_size": int64(3), "config": map[string]any{"k3s_version": "v1.36.3+k3s1"}}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/updates", `{"scope":"all","node_ids":[],"release_tag":"2026.09.1-rc.1","confirmation":"DEPLOY QUALIFICATION"}`, token))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected qualification update acceptance, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.mutation.TargetRelease != "2026.09.1-rc.1" || store.mutation.TargetSHA != targetSHA || store.mutation.Payload["release_channel"] != "qualification" || store.mutation.Payload["source_sha"] != baselineSHA {
		t.Fatalf("qualification update did not preserve immutable channel metadata: %+v", store.mutation)
	}
}

func TestClusterDevelopmentBaselineRejectsStableReleaseOutsideAncestry(t *testing.T) {
	const baselineSHA = "fedcba9876543210fedcba9876543210fedcba98"
	const targetSHA = "0123456789abcdef0123456789abcdef01234567"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases"):
			_ = json.NewEncoder(w).Encode([]clusterGitHubRelease{{Immutable: true, TagName: "2026.09.1", Name: "Unrelated Stable"}})
		case strings.Contains(r.URL.Path, "/git/ref/tags/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": targetSHA, "type": "commit"}})
		case strings.Contains(r.URL.Path, "/compare/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "diverged"})
		case strings.Contains(r.URL.Path, "/Data/Engine/release-manifest.json"):
			_ = json.NewEncoder(w).Encode(clusterReleaseManifest{SchemaVersion: 1, ClusterCompatible: true, AllowedReleaseChannels: []string{"stable"}, MinimumRollingVersion: "2026.09.1", MaximumVersionSkewReleases: 1, DatabaseMigration: "expand-contract", RequiredK3sBaseline: "v1.36.3+k3s1", RequiredK3sConformance: "pod-restart-policy-liveness-delay-guard-v1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("BOREALIS_GITHUB_API_BASE_URL", server.URL)
	t.Setenv("BOREALIS_GITHUB_RAW_BASE_URL", server.URL)
	t.Setenv("BOREALIS_K3S_VERSION", "v1.36.3+k3s1")
	serverClusterReleaseCache = clusterReleaseCache{}

	entries, err := fetchClusterReleaseCatalog(context.Background(), "dev-fedcba987654", baselineSHA, "v1.36.3+k3s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0]["selectable"] != false || entries[0]["reason"] != "release does not contain current pinned baseline" {
		t.Fatalf("unrelated stable release should be filtered from development baseline: %#v", entries)
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

func TestClusterQualificationUpdateRequiresWholeClusterAndExplicitConfirmation(t *testing.T) {
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, snapshot: map[string]any{"baseline_release": "2026.09.1", "active_size": int64(3)}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)

	for _, body := range []string{
		`{"scope":"all","node_ids":[],"release_tag":"2026.09.2-rc.1","confirmation":"UPDATE CLUSTER"}`,
		`{"scope":"node","node_ids":["11111111-1111-4111-8111-111111111111"],"release_tag":"2026.09.2-rc.1","confirmation":"DEPLOY QUALIFICATION"}`,
	} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/updates", body, token))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("expected qualification validation failure, got %d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	store.snapshot = map[string]any{"baseline_release": "2026.09.2-rc.1", "release_channel": "qualification", "active_size": int64(3)}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/updates", `{"scope":"node","node_ids":["11111111-1111-4111-8111-111111111111"],"release_tag":"2026.09.2","confirmation":"UPDATE CLUSTER"}`, token))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "whole cluster") {
		t.Fatalf("expected qualification promotion scope failure, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.mutation.Kind != "" {
		t.Fatalf("invalid qualification update reached store: %+v", store.mutation)
	}
}

func TestCompareClusterReleasesOrdersQualificationBeforeStable(t *testing.T) {
	for _, test := range []struct {
		left  string
		right string
		want  int
	}{
		{left: "2026.09.1-rc.1", right: "2026.09.1-rc.2", want: -1},
		{left: "2026.09.1-rc.2", right: "2026.09.1", want: -1},
		{left: "2026.09.1", right: "2026.09.1-rc.2", want: 1},
		{left: "2026.09.2-rc.1", right: "2026.09.1", want: 1},
	} {
		if got := compareClusterReleases(test.left, test.right); got != test.want {
			t.Fatalf("compareClusterReleases(%q,%q)=%d want %d", test.left, test.right, got, test.want)
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

func TestClusterMembershipScaleRejectsFiveNodeExpansion(t *testing.T) {
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/membership/scale", `{"desired_size":5,"reason":"expand pair"}`, token))

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "future roadmap") {
		t.Fatalf("expected five-node expansion fence, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.mutation.Kind != "" {
		t.Fatalf("rejected five-node request reached store: %+v", store.mutation)
	}
}

func TestClusterMembershipScaleAcceptsThreeNodeTarget(t *testing.T) {
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/membership/scale", `{"desired_size":3,"reason":"form quorum"}`, token))

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected three-node expansion acceptance, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.mutation.Kind != "membership_scale" || coerceInt64(store.mutation.Payload["desired_size"]) != 3 {
		t.Fatalf("unexpected membership mutation: %+v", store.mutation)
	}
}

func TestClusterInvitationRejectsExpansionAfterThreeNodes(t *testing.T) {
	store := &clusterTestStore{
		profile:  operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: map[string]any{"enabled": true, "active_size": int64(3), "cluster_id": "11111111-1111-4111-8111-111111111111"},
	}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/invitations", `{"node_name":"engine-4"}`, token))

	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "membership_not_qualified") {
		t.Fatalf("expected invitation membership fence, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.invitation != nil {
		t.Fatalf("rejected invitation reached store: %+v", store.invitation)
	}
}

func TestClusterInvitationAllowsDegradedQuorumReplacement(t *testing.T) {
	store := &clusterTestStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: map[string]any{
			"enabled":      true,
			"active_size":  int64(2),
			"desired_size": int64(3),
			"status":       "Degraded Quorum",
			"cluster_id":   "11111111-1111-4111-8111-111111111111",
		},
	}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/invitations", `{"node_name":"engine-replacement"}`, token))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected degraded replacement invitation, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if cleanText(store.invitation["node_name"]) != "engine-replacement" {
		t.Fatalf("replacement invitation did not reach store: %+v", store.invitation)
	}
}

func TestCurrentReleaseClusterMembershipRules(t *testing.T) {
	if err := validateCurrentReleaseClusterExpansion(1, 3); err != nil {
		t.Fatalf("one-to-three expansion rejected: %v", err)
	}
	for _, sizes := range [][2]int64{{3, 5}, {1, 5}, {5, 3}} {
		if err := validateCurrentReleaseClusterExpansion(sizes[0], sizes[1]); err == nil {
			t.Fatalf("unsupported expansion accepted: %d to %d", sizes[0], sizes[1])
		}
	}
	if !currentReleaseClusterRemovalSupported(3) || currentReleaseClusterRemovalSupported(1) || currentReleaseClusterRemovalSupported(5) {
		t.Fatal("current release removal fence must allow only three-to-one membership change")
	}
}

func TestCurrentReleaseAdmissionBatchSize(t *testing.T) {
	for _, test := range []struct {
		active  int64
		desired int64
		status  string
		want    int
	}{
		{active: 1, desired: 1, status: "Healthy", want: 2},
		{active: 1, desired: 3, status: "Pending Quorum", want: 2},
		{active: 2, desired: 3, status: "Degraded Quorum", want: 1},
	} {
		got, err := currentReleaseAdmissionBatchSize(test.active, test.desired, test.status)
		if err != nil || got != test.want {
			t.Fatalf("admission state %d/%d %s: got=%d err=%v", test.active, test.desired, test.status, got, err)
		}
	}
	for _, test := range []struct {
		active  int64
		desired int64
		status  string
	}{
		{active: 2, desired: 2, status: "Degraded Quorum"},
		{active: 2, desired: 3, status: "Healthy"},
		{active: 3, desired: 3, status: "Healthy"},
		{active: 3, desired: 5, status: "Healthy"},
	} {
		if _, err := currentReleaseAdmissionBatchSize(test.active, test.desired, test.status); err == nil {
			t.Fatalf("unsupported admission state accepted: %d/%d %s", test.active, test.desired, test.status)
		}
	}
}

func TestClusterBannerIgnoresHistoricalFailures(t *testing.T) {
	store := &clusterTestStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: map[string]any{
			"enabled": true,
			"status":  "Healthy",
			"hmr":     map[string]any{"state": "inactive"},
			"operations": []map[string]any{
				{"kind": "membership_admit", "state": "succeeded", "current_step": "complete"},
				{"kind": "membership_admit", "state": "failed", "current_step": "apply_membership"},
			},
		},
	}
	auth, token := clusterTestAuth(t, store)
	recorder := httptest.NewRecorder()
	clusterBannerHandler(auth).ServeHTTP(recorder, clusterTestRequest(t, http.MethodGet, "/api/server/cluster/banner", "", token))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"active_operation":null`) {
		t.Fatalf("historical failure remained active banner: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	store.snapshot["operations"] = []map[string]any{{"kind": "engine_update", "state": "running", "current_step": "pre_change_snapshot"}}
	store.snapshot["release_channel"] = "qualification"
	store.snapshot["baseline_release"] = "2026.09.2-rc.1"
	store.snapshot["last_stable_release"] = "2026.09.1"
	store.snapshot["qualification_schema_finalize_pending"] = true
	recorder = httptest.NewRecorder()
	clusterBannerHandler(auth).ServeHTTP(recorder, clusterTestRequest(t, http.MethodGet, "/api/server/cluster/banner", "", token))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"state":"running"`) || !strings.Contains(recorder.Body.String(), `"release_channel":"qualification"`) || !strings.Contains(recorder.Body.String(), `"qualification_schema_finalize_pending":true`) {
		t.Fatalf("running operation missing from banner: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestClusterBannerIncludesIsolatedNodeName(t *testing.T) {
	const nodeID = "11111111-1111-4111-8111-111111111111"
	store := &clusterTestStore{
		profile: operatorProfile{Username: "operator", Role: "Admin"},
		snapshot: map[string]any{
			"enabled": true,
			"status":  "HMR Non-HA",
			"hmr":     map[string]any{"state": "active", "node_id": nodeID},
			"nodes":   []map[string]any{{"id": nodeID, "node_name": "engine-isolated"}},
		},
	}
	auth, token := clusterTestAuth(t, store)
	recorder := httptest.NewRecorder()
	clusterBannerHandler(auth).ServeHTTP(recorder, clusterTestRequest(t, http.MethodGet, "/api/server/cluster/banner", "", token))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"hmr_node_name":"engine-isolated"`) {
		t.Fatalf("isolated node missing from banner: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestClusterOperationHistoryMarksGlobalFailuresSuperseded(t *testing.T) {
	operations := []map[string]any{
		{"id": "new-success", "kind": "membership_admit", "state": "succeeded", "finished_at": int64(20)},
		{"id": "old-failure", "kind": "membership_admit", "state": "failed", "finished_at": int64(10)},
		{"id": "node-success", "kind": "node_maintenance", "state": "succeeded", "finished_at": int64(20)},
		{"id": "node-failure", "kind": "node_maintenance", "state": "failed", "finished_at": int64(10)},
		{"id": "old-cancellation", "kind": "membership_admit", "state": "cancelled", "finished_at": int64(10)},
	}
	annotateSupersededClusterOperations(operations)
	if operations[1]["superseded_by"] != "new-success" {
		t.Fatalf("membership failure not superseded: %+v", operations[1])
	}
	if operations[3]["superseded_by"] != nil {
		t.Fatalf("target-specific operation incorrectly superseded: %+v", operations[3])
	}
	if operations[4]["superseded_by"] != "new-success" {
		t.Fatalf("legacy admission cancellation not superseded: %+v", operations[4])
	}
}

func TestClusterDatabaseDegradationAllowsOnlyRecoveryMutations(t *testing.T) {
	allowed := []clusterMutation{
		{Kind: "hmr_exit"},
		{Kind: "postgres_emergency_failover"},
		{Kind: "node_maintenance", Payload: map[string]any{"action": "exit"}},
		{Kind: "node_remove", Payload: map[string]any{"emergency": true}},
	}
	for _, mutation := range allowed {
		if !clusterMutationSupportsDatabaseRecovery(mutation) {
			t.Fatalf("recovery mutation blocked: %+v", mutation)
		}
	}
	blocked := []clusterMutation{
		{Kind: "engine_update"},
		{Kind: "postgres_switchover"},
		{Kind: "node_maintenance", Payload: map[string]any{"action": "enter"}},
		{Kind: "node_remove", Payload: map[string]any{"emergency": false}},
	}
	for _, mutation := range blocked {
		if clusterMutationSupportsDatabaseRecovery(mutation) {
			t.Fatalf("normal mutation allowed during database degradation: %+v", mutation)
		}
	}
}

func TestClusterDatabaseRuntimeBlocksNormalMutationsAcrossOtherStatuses(t *testing.T) {
	degraded := `{"database_runtime":{"fully_ready":false,"durability_quorum":true}}`
	if !clusterDatabaseRuntimeRequiresRecovery(degraded) {
		t.Fatal("observed database degradation was ignored")
	}
	if clusterDatabaseRuntimeRequiresRecovery(`{"database_runtime":{"fully_ready":true,"durability_quorum":true}}`) {
		t.Fatal("healthy database runtime was marked degraded")
	}
	if clusterDatabaseRuntimeRequiresRecovery(`{}`) {
		t.Fatal("missing legacy database observation was marked degraded")
	}
}

func TestClusterDrainedNodeAllowsOnlyRecoveryMutations(t *testing.T) {
	allowed := []clusterMutation{
		{Kind: "hmr_exit"},
		{Kind: "postgres_emergency_failover"},
		{Kind: "node_maintenance", Payload: map[string]any{"action": "exit"}},
		{Kind: "node_remove", Payload: map[string]any{"emergency": true}},
	}
	for _, mutation := range allowed {
		if !clusterMutationSupportsDatabaseRecovery(mutation) {
			t.Fatalf("drained-node recovery mutation blocked: %+v", mutation)
		}
	}
	for _, mutation := range []clusterMutation{{Kind: "engine_update"}, {Kind: "hmr_start"}, {Kind: "membership_scale"}, {Kind: "node_maintenance", Payload: map[string]any{"action": "enter"}}} {
		if clusterMutationSupportsDatabaseRecovery(mutation) {
			t.Fatalf("normal mutation allowed while application node drained: %+v", mutation)
		}
	}
}

func TestClusterQuorumDegradationAllowsOnlyRecoveryMutations(t *testing.T) {
	for _, mutation := range []clusterMutation{
		{Kind: "postgres_emergency_failover"},
		{Kind: "node_maintenance", Payload: map[string]any{"action": "exit"}},
		{Kind: "node_remove", Payload: map[string]any{"emergency": true}},
	} {
		if !clusterMutationSupportsQuorumRecovery(mutation) {
			t.Fatalf("quorum recovery mutation blocked: %+v", mutation)
		}
	}
	for _, mutation := range []clusterMutation{
		{Kind: "engine_update"},
		{Kind: "k3s_update"},
		{Kind: "node_maintenance", Payload: map[string]any{"action": "enter"}},
		{Kind: "node_remove", Payload: map[string]any{"emergency": false}},
	} {
		if clusterMutationSupportsQuorumRecovery(mutation) {
			t.Fatalf("normal mutation allowed during quorum recovery: %+v", mutation)
		}
	}
}

func TestClusterSafeRemovalRequiresDistinctPair(t *testing.T) {
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	path := "/api/server/cluster/nodes/11111111-1111-4111-8111-111111111111/remove"
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, path, `{"emergency":false,"confirmation":"REMOVE NODE PAIR","reason":"retire pair"}`, token))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "paired_node_id") {
		t.Fatalf("expected paired target validation, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, path, `{"emergency":false,"paired_node_id":"22222222-2222-4222-8222-222222222222","confirmation":"REMOVE NODE PAIR","reason":"retire pair"}`, token))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected paired removal acceptance, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.mutation.Kind != "node_remove" || cleanText(store.mutation.Payload["paired_node_id"]) != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("paired removal mutation missing target: %+v", store.mutation)
	}
}

func TestClusterEmergencyRemovalRequiresExternalFenceConfirmation(t *testing.T) {
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	path := "/api/server/cluster/nodes/11111111-1111-4111-8111-111111111111/remove"
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, path, `{"emergency":true,"confirmation":"EMERGENCY REMOVE NODE","reason":"lost host"}`, token))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "TARGET IS POWERED OFF") {
		t.Fatalf("expected external fence acknowledgement, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, path, `{"emergency":true,"confirmation":"EMERGENCY REMOVE NODE","fencing_confirmation":"TARGET IS POWERED OFF","reason":"lost host"}`, token))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected fenced emergency removal acceptance, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestClusterK3sUpdateUsesDistinctQualifiedContract(t *testing.T) {
	t.Setenv("BOREALIS_K3S_VERSION", "v1.36.3+k3s1")
	t.Setenv("BOREALIS_K3S_PROBE_CONFORMANCE", "passed")
	t.Setenv("BOREALIS_K3S_UPGRADE_IMAGE", "docker.io/rancher/k3s-upgrade@sha256:"+strings.Repeat("a", 64))
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, snapshot: map[string]any{"active_size": int64(3), "config": map[string]any{"k3s_version": "v1.36.3+k3s1"}}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/updates", `{"update_type":"k3s","scope":"all","node_ids":[],"k3s_version":"v1.36.4+k3s1","confirmation":"UPDATE K3S"}`, token))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected K3s update acceptance, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.mutation.Kind != "k3s_update" || store.mutation.TargetRelease != "v1.36.4+k3s1" || cleanText(store.mutation.Payload["source_k3s_version"]) != "v1.36.3+k3s1" {
		t.Fatalf("K3s operation lost pinned contract: %+v", store.mutation)
	}
}

func TestClusterK3sUpdateUsesPersistedBaselineAfterPriorRollingUpdate(t *testing.T) {
	t.Setenv("BOREALIS_K3S_VERSION", "v1.36.3+k3s1")
	t.Setenv("BOREALIS_K3S_PROBE_CONFORMANCE", "passed")
	t.Setenv("BOREALIS_K3S_UPGRADE_IMAGE", "docker.io/rancher/k3s-upgrade@sha256:"+strings.Repeat("a", 64))
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, snapshot: map[string]any{
		"active_size": int64(3),
		"config":      map[string]any{"k3s_version": "v1.36.4+k3s1"},
	}}
	auth, token := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/updates", `{"update_type":"k3s","scope":"all","node_ids":[],"k3s_version":"v1.36.5+k3s1","confirmation":"UPDATE K3S"}`, token))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected K3s update acceptance from persisted baseline, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if source := cleanText(store.mutation.Payload["source_k3s_version"]); source != "v1.36.4+k3s1" {
		t.Fatalf("expected persisted K3s source, got %q", source)
	}
}

func TestK3sUpgradePathRejectsDowngradeSameVersionAndMinorSkip(t *testing.T) {
	for _, target := range []string{"v1.36.2+k3s1", "v1.36.3+k3s1", "v1.38.0+k3s1", "v1.36.4-rc1+k3s1"} {
		if err := validateK3sUpgradePath("v1.36.3+k3s1", target); err == nil {
			t.Fatalf("unsafe K3s target accepted: %s", target)
		}
	}
	for _, target := range []string{"v1.36.3+k3s2", "v1.36.4+k3s1", "v1.37.0+k3s1"} {
		if err := validateK3sUpgradePath("v1.36.3+k3s1", target); err != nil {
			t.Fatalf("safe K3s target %s rejected: %v", target, err)
		}
	}
}

func TestClusterInputClassesEnforceIPv4UbuntuAndOperationalLimits(t *testing.T) {
	for _, address := range []string{"2001:db8::1", "8.8.8.8", "127.0.0.1", "169.254.1.1"} {
		if len(validateClusterIP("management_ip", address)) == 0 {
			t.Fatalf("unsafe cluster address accepted: %s", address)
		}
	}
	if len(validateClusterIP("management_ip", "10.20.30.40")) != 0 {
		t.Fatal("private unicast cluster IPv4 address rejected")
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

func TestClusterJoinRejectsNonAMD64ArchitectureBeforeAuthentication(t *testing.T) {
	store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}}
	auth, _ := clusterTestAuth(t, store)
	mux := http.NewServeMux()
	registerServerClusterRoutes(mux, auth)
	body := `{"invite_bundle":"invalid","node_name":"engine-2","hostname":"engine-2","management_ip":"10.20.30.42","architecture":"arm64","os_version":"Ubuntu 24.04"}`
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/bootstrap/cluster/join", body, ""))

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "must be amd64") {
		t.Fatalf("expected non-amd64 rejection, got %d body=%s", recorder.Code, recorder.Body.String())
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
