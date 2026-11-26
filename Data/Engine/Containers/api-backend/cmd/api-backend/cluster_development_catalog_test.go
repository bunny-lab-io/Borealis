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
	encode := func(v any) []byte {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	manifest := encode(map[string]any{"schema": 1, "kind": "candidate", "channel": "development", "source_sha": sha, "source_tree": tree, "repository": repo, "platform": "linux-amd64", "artifacts": objects})
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
