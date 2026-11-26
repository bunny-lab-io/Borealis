package main

import (
	b "borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevelopmentMemberFetchUsesOnlyRetainedFastForward(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	write := func(path string, data []byte) {
		t.Helper()
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		r, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %s: %v %s", args[0], e, r)
		}
		return strings.TrimSpace(string(r))
	}
	encode := func(v any) []byte {
		t.Helper()
		raw, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	hash := func(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
	repo := "bunny-lab-io/Borealis"
	seed, checkout := filepath.Join(root, "seed"), filepath.Join(root, "member")
	git(root, "init", "--quiet", seed)
	git(seed, "config", "user.name", "fixture")
	git(seed, "config", "user.email", "fixture@example.invalid")
	compatibility := map[string]any{"cluster_compatible": true}
	write(filepath.Join(seed, "Data/Engine/release-manifest.json"), encode(compatibility))
	git(seed, "add", ".")
	git(seed, "commit", "-qm", "base")
	base := git(seed, "rev-parse", "HEAD")
	git(root, "clone", "--quiet", "--no-local", seed, checkout)
	// A GitHub/network fallback would fail. The node retains its configured origin.
	t.Setenv("BOREALIS_ENGINE_REPOSITORY_URL", filepath.Join(root, "offline-origin.git"))
	git(checkout, "remote", "set-url", "origin", os.Getenv("BOREALIS_ENGINE_REPOSITORY_URL"))
	write(filepath.Join(seed, "fix"), []byte("new"))
	git(seed, "add", ".")
	git(seed, "commit", "-qm", "local fix")
	sha, tree := git(seed, "rev-parse", "HEAD"), git(seed, "rev-parse", "HEAD^{tree}")
	git(seed, "update-ref", developmentBundleRef, sha)
	file := filepath.Join(root, "range.bundle")
	git(seed, "bundle", "create", file, developmentBundleRef, "^"+base)
	history, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	names := map[string][]string{"bootstrap": {b.BundleName, b.ManifestName}, "k3s": {b.K3sBinaryName, b.K3sInstallerName, b.K3sArchiveName, b.K3sInventoryName}, "host_package": {b.HostPackageArchiveName, b.HostPackageInventoryName}, "image": {b.ImageInventoryName}, "external": {b.ExternalInventoryName}}
	for _, r := range b.ImageRoles() {
		names["image"] = append(names["image"], b.ImageAssetName(r))
	}
	for _, p := range b.ExternalImagePins() {
		names["external"] = append(names["external"], b.ExternalImageAssetName(p.Reference))
	}
	inventories := map[string]string{}
	objects := map[string]any{}
	for f, files := range names {
		inventories[f] = hash([]byte{0})
		for _, n := range files {
			objects[f+"/"+n] = map[string]any{"sha256": inventories[f], "bytes": 1}
		}
	}
	historyPath := "upgrade/" + b.DevelopmentHistoryName
	objects[historyPath] = map[string]any{"bytes": len(history), "sha256": hash(history)}
	manifest := encode(map[string]any{"schema": 1, "channel": "development", "kind": "candidate", "source_sha": sha, "source_tree": tree, "platform": "linux-amd64", "repository": repo, "artifacts": objects, "upgrade": map[string]any{"base_sha": base, "compatibility": compatibility}})
	binding := encode(map[string]any{"schema": 2, "channel": "development", "source_sha": sha, "source_tree": tree, "platform": "linux-amd64", "repository": repo, "inventories": inventories, "manifest_sha256": hash(manifest)})
	id := "dev-" + hash(binding)
	parent := filepath.Join(root, "retained")
	t.Setenv("BOREALIS_ENGINE_DEVELOPMENT_CANDIDATES", parent)
	install := func(n string, data []byte) { write(filepath.Join(parent, id, n), data) }
	install("development-identity.json", binding)
	install("development-manifest.json", manifest)
	bad := append([]byte(nil), history...)
	bad[len(bad)-1] ^= 1
	install(historyPath, bad)
	m := manager{repoRoot: checkout}
	if _, e := m.fetchRelease(ctx, id, sha); e == nil || git(checkout, "rev-parse", "HEAD") != base {
		t.Fatal("damaged history advanced checkout")
	}
	install(historyPath, history)
	if _, e := m.preflightRelease(ctx, id, sha); e != nil || git(checkout, "rev-parse", "HEAD") != base {
		t.Fatalf("preflight: %v", e)
	}
	for range 2 {
		if _, e := m.fetchRelease(ctx, id, sha); e != nil || git(checkout, "rev-parse", "HEAD") != sha {
			t.Fatalf("verified fast-forward: %v", e)
		}
	}
}

func TestDevelopmentPromotionRetainsRecoveryBeforeServing(t *testing.T) {
	deploy, config := t.TempDir(), t.TempDir()
	active, next := filepath.Join(deploy, "image-manifest.json"), filepath.Join(config, "image-manifest-dev.json")
	if err := os.WriteFile(active, []byte("prior"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(next, []byte("candidate"), 0600); err != nil {
		t.Fatal(err)
	}
	step := func() error { return context.Canceled }
	if err := promoteDevelopmentGeneration(context.Background(), deploy, next, step); err == nil {
		t.Fatal("failed rollout accepted")
	}
	if b, _ := os.ReadFile(active); string(b) != "prior" {
		t.Fatal("premature manifest swap")
	}
	backup, err := filepath.Glob(filepath.Join(config, "image-manifest-recovery-*.json"))
	if err != nil || len(backup) != 1 {
		t.Fatal("missing recovery manifest")
	}
	for range 2 {
		if err := promoteDevelopmentGeneration(context.Background(), deploy, next, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(active); string(b) != "candidate" {
		t.Fatal("serving manifest stale")
	}
	if b, _ := os.ReadFile(backup[0]); string(b) != "prior" {
		t.Fatal("lost recovery manifest")
	}
}

func TestDevelopmentPromotionRequiresActualPackagedHelper(t *testing.T) {
	root := t.TempDir()
	installed := filepath.Join(root, "installed")
	running := filepath.Join(root, "running")
	marker := filepath.Join(root, "marker")
	digest := sha256.Sum256([]byte("packaged helper"))
	sha := strings.Repeat("a", 40)
	for path, value := range map[string]string{installed: "packaged helper", marker: sha + "\n"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(installed, running); err != nil {
		t.Fatal(err)
	}
	want := hex.EncodeToString(digest[:])
	if err := verifyServingDevelopmentHelper(running, installed, marker, want, sha); err != nil {
		t.Fatal(err)
	}
	if verifyServingDevelopmentHelper(running, installed, marker, strings.Repeat("0", 64), sha) == nil {
		t.Fatal("accepted wrong packaged digest with matching revision")
	}
	if verifyServingDevelopmentHelper(running, installed, marker, want, strings.Repeat("b", 40)) == nil {
		t.Fatal("accepted stale revision marker")
	}
	if err := os.Remove(installed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("packaged helper"), 0600); err != nil {
		t.Fatal(err)
	}
	if verifyServingDevelopmentHelper(running, installed, marker, want, sha) == nil {
		t.Fatal("accepted replaced executable inode")
	}
}
