package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const identityReleaseSHA = "0123456789abcdef0123456789abcdef01234567"

// HTTP fixtures keep publication changes separate from the cached picker and
// permit a tag to move immediately after its single resolution.
type releaseIdentityFixture struct {
	mu               sync.Mutex
	release          clusterGitHubRelease
	manifest         clusterReleaseManifest
	sha              string
	failure          int
	manifestFailure  bool
	moveAfterResolve bool
	paths            []string
}

func newReleaseIdentityFixture(t *testing.T) *releaseIdentityFixture {
	t.Helper()
	f := &releaseIdentityFixture{
		release: clusterGitHubRelease{TagName: "2026.09.2", Immutable: true}, sha: identityReleaseSHA,
		manifest: clusterReleaseManifest{SchemaVersion: 1, ClusterCompatible: true,
			AllowedReleaseChannels: []string{"stable", "qualification"}, MinimumRollingVersion: "2026.09.1",
			MaximumVersionSkewReleases: 1, DatabaseMigration: "expand-contract",
			RequiredK3sBaseline: "v1.36.4+k3s1", RequiredK3sConformance: "pod-restart-policy-liveness-delay-guard-v1"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.paths = append(f.paths, r.URL.Path)
		if f.failure != 0 {
			w.WriteHeader(f.failure)
			return
		}
		var value any
		switch {
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			value = f.release
		case strings.HasSuffix(r.URL.Path, "/releases"):
			if r.URL.Query().Get("page") != "1" {
				value = []clusterGitHubRelease{}
			} else {
				value = []clusterGitHubRelease{f.release}
			}
		case strings.Contains(r.URL.Path, "/git/ref/tags/"):
			value = map[string]any{"object": map[string]any{"sha": f.sha, "type": "commit"}}
			if f.moveAfterResolve {
				f.sha = strings.Repeat("b", 40)
			}
		case r.URL.Path == "/"+identityReleaseSHA+"/Data/Engine/release-manifest.json":
			if f.manifestFailure {
				http.NotFound(w, r)
				return
			}
			value = f.manifest
		case strings.Contains(r.URL.Path, "/compare/"):
			value = map[string]any{"status": "ahead"}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	t.Cleanup(server.Close)
	t.Setenv("BOREALIS_GITHUB_API_BASE_URL", server.URL)
	t.Setenv("BOREALIS_GITHUB_RAW_BASE_URL", server.URL)
	t.Setenv("BOREALIS_K3S_VERSION", "v1.36.3+k3s1") // Deliberately stale pod environment.
	serverClusterReleaseCache = clusterReleaseCache{}
	return f
}

func releaseIdentityStore() *clusterTestStore {
	return &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, snapshot: map[string]any{
		"baseline_release": "2026.09.1", "baseline_sha": strings.Repeat("a", 40), "active_size": int64(3),
		"config": map[string]any{"k3s_version": "v1.36.4+k3s1"},
		"nodes":  []map[string]any{{"node_name": "engine-1", "membership_state": "Active", "roles": map[string]any{"k3s_version": "v1.36.4+k3s1"}}},
	}}
}

func TestClusterReleaseQueueRejectsChangedPublicationDespiteCachedPicker(t *testing.T) {
	for _, name := range []string{"mutable", "draft", "channel mismatch", "wrong tag", "GitHub unavailable", "manifest missing", "manifest K3s mismatch"} {
		t.Run(name, func(t *testing.T) {
			f := newReleaseIdentityFixture(t)
			store := releaseIdentityStore()
			auth, token := clusterTestAuth(t, store)
			catalog, err := fetchClusterReleaseCatalog(context.Background(), "2026.09.1", strings.Repeat("a", 40), "v1.36.4+k3s1")
			if err != nil || len(catalog) != 1 || catalog[0]["selectable"] != true {
				t.Fatalf("initial picker: %v %v", catalog, err)
			}
			f.mu.Lock()
			switch name {
			case "mutable":
				f.release.Immutable = false
			case "draft":
				f.release.Draft = true
			case "channel mismatch":
				f.release.Prerelease = true
			case "wrong tag":
				f.release.TagName = "2026.09.3"
			case "GitHub unavailable":
				f.failure = http.StatusServiceUnavailable
			case "manifest missing":
				f.manifestFailure = true
			case "manifest K3s mismatch":
				f.manifest.RequiredK3sBaseline = "v1.36.3+k3s1"
			}
			f.mu.Unlock()
			recorder := httptest.NewRecorder()
			clusterUpdateHandler(auth).ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/updates", `{"scope":"all","release_tag":"2026.09.2","confirmation":"UPDATE CLUSTER"}`, token))
			if recorder.Code != http.StatusUnprocessableEntity || store.mutation.Kind != "" {
				t.Fatalf("cached picker authorized changed release: %d %s %+v", recorder.Code, recorder.Body.String(), store.mutation)
			}
		})
	}
}

func TestClusterReleaseHTTPRejectsRedirectAndAmbiguousJSON(t *testing.T) {
	for _, state := range []string{"redirect", "trailing JSON", "oversized JSON", "missing immutable"} {
		t.Run(state, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/publication" {
					t.Error("followed release redirect")
					return
				}
				switch state {
				case "redirect":
					http.Redirect(w, r, "/untrusted", http.StatusFound)
				case "trailing JSON":
					_, _ = w.Write([]byte(`{"immutable":true} {"immutable":false}`))
				case "oversized JSON":
					_, _ = w.Write([]byte(`{"name":"` + strings.Repeat("x", 2<<20) + `","immutable":true}`))
				case "missing immutable":
					_, _ = w.Write([]byte(`{"tag_name":"2026.09.2"}`))
				}
			}))
			defer server.Close()
			var release clusterGitHubRelease
			err := clusterGitHubJSON(context.Background(), server.URL+"/publication", &release)
			if state == "missing immutable" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := hydrateClusterRelease(context.Background(), release, "2026.09.1", identityReleaseSHA, "v1.36.4+k3s1"); err == nil {
					t.Fatal("missing immutable proof accepted")
				}
			} else if err == nil {
				t.Fatal("ambiguous release response accepted")
			}
		})
	}
}

func TestClusterAnnotatedReleaseResolvesObjectFromPinnedRepository(t *testing.T) {
	objectSHA := strings.Repeat("c", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		object := map[string]any{"sha": objectSHA, "type": "tag", "url": "http://127.0.0.1:1/untrusted"}
		switch r.URL.Path {
		case "/repos/bunny-lab-io/Borealis/git/ref/tags/2026.09.2":
		case "/repos/bunny-lab-io/Borealis/git/tags/" + objectSHA:
			object = map[string]any{"sha": identityReleaseSHA, "type": "commit"}
		default:
			t.Errorf("unexpected object endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": object})
	}))
	defer server.Close()
	t.Setenv("BOREALIS_GITHUB_API_BASE_URL", server.URL)
	t.Setenv("BOREALIS_ENGINE_GITHUB_REPOSITORY", "bunny-lab-io/Borealis")
	sha, err := resolveClusterGitHubTagSHA(context.Background(), "2026.09.2")
	if err != nil || sha != identityReleaseSHA {
		t.Fatalf("annotated identity: %s %v", sha, err)
	}
}

func TestClusterReleasePinsManifestSHAAfterTagMoves(t *testing.T) {
	f := newReleaseIdentityFixture(t)
	f.mu.Lock()
	f.moveAfterResolve = true
	f.mu.Unlock()
	store := releaseIdentityStore()
	auth, token := clusterTestAuth(t, store)
	recorder := httptest.NewRecorder()
	clusterUpdateHandler(auth).ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/updates", `{"scope":"all","release_tag":"2026.09.2","confirmation":"UPDATE CLUSTER"}`, token))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("queue: %d %s", recorder.Code, recorder.Body.String())
	}
	if store.mutation.TargetSHA != identityReleaseSHA || store.mutation.Payload["release_immutable"] != true || store.mutation.Payload["source_k3s_version"] != "v1.36.4+k3s1" {
		t.Fatalf("lost verified identity: %+v", store.mutation)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	refs, manifests := 0, 0
	for _, path := range f.paths {
		if strings.Contains(path, "/git/ref/tags/") {
			refs++
		}
		if strings.HasSuffix(path, "/release-manifest.json") {
			manifests++
			if path != "/"+identityReleaseSHA+"/Data/Engine/release-manifest.json" {
				t.Fatalf("manifest followed mutable tag: %s", path)
			}
		}
	}
	if refs != 1 || manifests != 1 {
		t.Fatalf("identity resolved more than once: %v", f.paths)
	}
}

func TestClusterReleaseCatalogChangesWithAuthoritativeK3s(t *testing.T) {
	newReleaseIdentityFixture(t)
	for _, version := range []string{"v1.36.3+k3s1", "v1.36.4+k3s1"} {
		items, err := fetchClusterReleaseCatalog(context.Background(), "2026.09.1", strings.Repeat("a", 40), version)
		if err != nil || len(items) != 1 || items[0]["selectable"] != (version == "v1.36.4+k3s1") {
			t.Fatalf("stale compatibility cache for %s: %v %v", version, items, err)
		}
	}
	for _, state := range []string{"unknown config", "observed mismatch"} {
		t.Run(state, func(t *testing.T) {
			store := releaseIdentityStore()
			if state == "unknown config" {
				delete(store.snapshot, "config")
			} else {
				store.snapshot["config"] = map[string]any{"k3s_version": "v1.36.3+k3s1"}
			}
			auth, token := clusterTestAuth(t, store)
			recorder := httptest.NewRecorder()
			clusterReleasesHandler(auth).ServeHTTP(recorder, clusterTestRequest(t, http.MethodGet, "/api/server/cluster/releases", "", token))
			if recorder.Code != http.StatusConflict {
				t.Fatalf("unverified baseline authorized catalog: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestClusterImmutableQualificationPromotionKeepsSameCommit(t *testing.T) {
	newReleaseIdentityFixture(t)
	store := releaseIdentityStore()
	store.snapshot["baseline_release"] = "2026.09.2-rc.1"
	store.snapshot["baseline_sha"] = identityReleaseSHA
	auth, token := clusterTestAuth(t, store)
	recorder := httptest.NewRecorder()
	clusterUpdateHandler(auth).ServeHTTP(recorder, clusterTestRequest(t, http.MethodPost, "/api/server/cluster/updates", `{"scope":"all","release_tag":"2026.09.2","confirmation":"UPDATE CLUSTER"}`, token))
	if recorder.Code != http.StatusAccepted || store.mutation.TargetSHA != identityReleaseSHA {
		t.Fatalf("same-commit promotion rejected: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestClusterEngineUpdatePreflightFencesChangedK3sAndLegacyIdentity(t *testing.T) {
	for _, state := range []string{"changed version", "unknown version", "unavailable node", "legacy payload"} {
		t.Run(state, func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.Method+" "+r.URL.Path)
				mu.Unlock()
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/nodes/engine-1" || state == "unavailable node" {
					http.NotFound(w, r)
					return
				}
				version := "v1.36.3+k3s1"
				if state == "unknown version" {
					version = ""
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"nodeInfo": map[string]any{"kubeletVersion": version}}})
			}))
			defer server.Close()
			runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}}
			operation := clusterControllerOperation{Kind: "engine_update", TargetRelease: "2026.09.2", TargetSHA: identityReleaseSHA, Payload: map[string]any{"release_immutable": true, "source_k3s_version": "v1.36.4+k3s1", "compatibility": map[string]any{"required_k3s_baseline": "v1.36.4+k3s1"}}}
			if state == "legacy payload" {
				delete(operation.Payload, "release_immutable")
			}
			err := runner.Run(context.Background(), operation, clusterControllerStep{Name: "preflight"}, []clusterControllerNode{{Name: "engine-1"}})
			if err == nil {
				t.Fatal("unverified identity reached mutation")
			}
			mu.Lock()
			defer mu.Unlock()
			if state == "legacy payload" && len(paths) != 0 {
				t.Fatalf("legacy payload reached runtime: %v", paths)
			}
			for _, path := range paths {
				if path != "GET /api/v1/nodes/engine-1" {
					t.Fatalf("preflight reached mutation before K3s proof: %v", paths)
				}
			}
		})
	}
}
