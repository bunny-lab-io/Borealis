package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Exercise the production reader and authenticated network broker. Only the
// artifact payload and database custody decision are fixture inputs.
func TestClusterDevelopmentCatalogRequiresCurrentAuthority(t *testing.T) {
	for _, resolute := range []bool{false, true} {
		name := "original Noble catalog"
		if resolute {
			name = "dual-suite catalog and fresh source baseline"
		}
		t.Run(name, func(t *testing.T) { testClusterDevelopmentCatalog(t, resolute) })
	}
}

func testClusterDevelopmentCatalog(t *testing.T, resolute bool) {
	parent := t.TempDir()
	t.Setenv("BOREALIS_ENGINE_DEVELOPMENT_CANDIDATES", parent)
	t.Setenv("BOREALIS_OPERATOR_SECRET", sshBrokerTestSecret)
	repo, sha, tree := clusterGitHubRepo(), strings.Repeat("c", 40), strings.Repeat("a", 40)
	families := map[string][]string{
		"bootstrap":    {clusterbootstrap.BundleName, clusterbootstrap.ManifestName},
		"host_package": {clusterbootstrap.HostPackageInventoryName, clusterbootstrap.HostPackageArchiveName},
		"k3s":          {clusterbootstrap.K3sInventoryName, clusterbootstrap.K3sBinaryName, clusterbootstrap.K3sInstallerName, clusterbootstrap.K3sArchiveName},
		"image":        {clusterbootstrap.ImageInventoryName}, "external": {clusterbootstrap.ExternalInventoryName},
	}
	for _, r := range clusterbootstrap.ImageRoles() {
		families["image"] = append(families["image"], clusterbootstrap.ImageAssetName(r))
	}
	for _, p := range clusterbootstrap.ExternalImagePins() {
		families["external"] = append(families["external"], clusterbootstrap.ExternalImageAssetName(p.Reference))
	}
	digest := func(b []byte) string { return strings.TrimPrefix(bootstrapDigest(string(b)), "sha256:") }
	payload := []byte("development fixture")
	objects := map[string]any{}
	inventories := map[string]string{}
	for family, names := range families {
		inventories[family] = digest(payload)
		for _, n := range names {
			objects[family+"/"+n] = map[string]any{"bytes": len(payload), "sha256": digest(payload)}
		}
	}
	if resolute {
		plan, _, err := clusterbootstrap.HostPackagePlanForSuite("resolute")
		if err != nil {
			t.Fatal(err)
		}
		inventories["host_package_resolute"] = digest([]byte("resolute inventory"))
		objects["host_package/"+plan.InventoryName()] = map[string]any{"bytes": len("resolute inventory"), "sha256": inventories["host_package_resolute"]}
		objects["host_package/"+plan.ArchiveName()] = map[string]any{"bytes": len(payload), "sha256": digest(payload)}
	}
	encode := func(v any) []byte {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	objects["upgrade/"+clusterbootstrap.DevelopmentHistoryName] = map[string]any{"bytes": len(payload), "sha256": digest(payload)}
	base := strings.Repeat("b", 40)
	compat := clusterReleaseManifest{SchemaVersion: 1, ClusterCompatible: true, AllowedReleaseChannels: []string{"development"}, MinimumRollingVersion: "2026.08.0", MaximumVersionSkewReleases: 1, DatabaseMigration: "expand-contract", RequiredK3sBaseline: "v1.36.3+k3s1", RequiredK3sConformance: "pod-restart-policy-liveness-delay-guard-v1"}
	manifest := encode(map[string]any{"schema": 1, "kind": "candidate", "channel": "development", "source_sha": sha, "source_tree": tree, "repository": repo, "platform": "linux-amd64", "artifacts": objects, "upgrade": map[string]any{"base_sha": base, "compatibility": compat}})
	binding := encode(map[string]any{"schema": 2, "channel": "development", "source_sha": sha, "source_tree": tree, "repository": repo, "platform": "linux-amd64", "inventories": inventories, "manifest_sha256": digest(manifest)})
	id := "dev-" + digest(binding)
	dir := filepath.Join(parent, id)
	if err := os.MkdirAll(filepath.Join(dir, "bootstrap"), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("development-manifest.json", manifest)
	write("development-identity.json", binding)
	bundle := "bootstrap/" + clusterbootstrap.BundleName
	write(bundle, payload)
	t.Run("local catalog to durable whole-cluster operation", func(t *testing.T) {
		external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected GitHub request") }))
		defer external.Close()
		t.Setenv("BOREALIS_GITHUB_API_BASE_URL", external.URL)
		t.Setenv("BOREALIS_GITHUB_RAW_BASE_URL", external.URL)
		store := &clusterTestStore{profile: operatorProfile{Username: "operator", Role: "Admin"}, snapshot: map[string]any{"baseline_release": "2026.11.1-rc.29", "baseline_sha": base, "active_size": int64(1), "config": map[string]any{"k3s_version": compat.RequiredK3sBaseline}}}
		if resolute {
			store.snapshot["baseline_release"] = "dev-" + base[:12]
			store.snapshot["config"].(map[string]any)["development_version_floor"] = "2026.11.1"
		}
		auth, token := clusterTestAuth(t, store)
		mux := http.NewServeMux()
		registerServerClusterRoutes(mux, auth)
		call := func(method, path, body string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, clusterTestRequest(t, method, path, body, token))
			return w
		}
		list := call("GET", "/api/server/cluster/releases?channel=development", "")
		if list.Code != 200 || !strings.Contains(list.Body.String(), id) || !strings.Contains(list.Body.String(), `"selectable":true`) {
			t.Fatalf("catalog %d %s", list.Code, list.Body.String())
		}
		body := map[string]any{"release_tag": id, "scope": "all", "node_ids": []string{}, "confirmation": "UPDATE CLUSTER", "maintenance_outage_acknowledgement": "ACCEPT OUTAGE"}
		post := func() *httptest.ResponseRecorder {
			return call("POST", "/api/server/cluster/updates", string(encode(body)))
		}
		if w := post(); w.Code < 400 {
			t.Fatal("casual confirmation accepted")
		}
		body["confirmation"] = "DEPLOY DEVELOPMENT"
		body["maintenance_outage_acknowledgement"] = ""
		if w := post(); w.Code < 400 {
			t.Fatal("outage acknowledgement omitted")
		}
		body["maintenance_outage_acknowledgement"] = "ACCEPT OUTAGE"
		if w := post(); w.Code != 202 {
			t.Fatalf("dispatch %d %s", w.Code, w.Body.String())
		}
		m := store.mutation
		if m.TargetSHA != sha || m.TargetRelease != id || m.Kind != "engine_update" {
			t.Fatal("queued target differs")
		}
		var persisted map[string]any
		if err := json.Unmarshal(encode(m.Payload), &persisted); err != nil {
			t.Fatal(err)
		}
		if err := validateClusterEngineUpdateIdentity(id, sha, persisted); err != nil {
			t.Fatal(err)
		}
		persisted["release_origin"] = "github"
		if validateClusterEngineUpdateIdentity(id, sha, persisted) == nil {
			t.Fatal("forged release origin accepted")
		}
		store.snapshot["baseline_sha"] = tree
		if resolute {
			store.snapshot["baseline_release"] = "dev-" + tree[:12]
		}
		if w := post(); w.Code != 422 {
			t.Fatalf("changed serving baseline %d", w.Code)
		}
	})
	expected := clusterBaselineExpected(id, sha)
	if expected.Development == "" || expected.Validate() != nil || clusterBaselineExpected(id, tree).Validate() == nil {
		t.Fatal("baseline source binding")
	}
	request, response, err := clusterPreparationReleaseCiphers()
	if err != nil {
		t.Fatal(err)
	}
	var current atomic.Bool
	current.Store(true)
	q := clusterPreparationReleaseRequest{Operation: newClusterUUID(), Attempt: 1, Holder: "owner", Confirmation: newClusterUUID()}
	broker := clusterPreparationReleaseBroker{request: request, response: response, slots: make(chan struct{}, 2), check: func(_ context.Context, got clusterPreparationReleaseRequest) bool {
		return current.Load() && got.Expected == expected && got.Operation == q.Operation && got.Holder == q.Holder && got.Confirmation == q.Confirmation
	}}
	server := httptest.NewServer(http.HandlerFunc(broker.handle))
	defer server.Close()
	reader := clusterPreparationReleaseReader{request: request, response: response, expected: expected, binding: q, client: server.Client(), endpoint: func(context.Context) (string, error) { return server.URL, nil }}
	ctx := context.WithValue(context.Background(), clusterPreparationReleaseKey{}, &reader)
	assets, err := resolveClusterBootstrapAssets(ctx, expected)
	if err != nil {
		t.Fatal(err)
	}
	asset := assets[clusterbootstrap.BundleName]
	read := func() ([]byte, error) {
		r, e := openClusterBootstrapAsset(ctx, asset)
		if e != nil {
			return nil, e
		}
		defer r.Body.Close()
		return io.ReadAll(r.Body)
	}
	if b, e := read(); e != nil || string(b) != string(payload) {
		t.Fatalf("authenticated development read: %v", e)
	}
	current.Store(false)
	if _, err := read(); err == nil {
		t.Fatal("stale custody opened local content")
	}
	current.Store(true)
	write(bundle, []byte(strings.Repeat("X", len(payload))))
	if _, err := read(); err == nil {
		t.Fatal("corrupt local stream completed")
	}
	write("development-manifest.json", append(manifest, ' '))
	if _, err := resolveClusterBootstrapAssets(ctx, expected); err == nil {
		t.Fatal("substituted manifest served")
	}
}
