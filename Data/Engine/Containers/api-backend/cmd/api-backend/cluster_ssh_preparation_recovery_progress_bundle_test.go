package main

import (
	"archive/tar"
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Same genuine Git/archive/compiled helper fixture used by native bootstrap
// tests. No fixture executable runs; Stage and ManagerProof verify owned bytes.
func recoveryProgressDigest(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func recoveryProgressManifestFixture() (clusterbootstrap.Expected, map[string]any) {
	e := clusterbootstrap.Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: strings.Repeat("a", 40), AllowQualification: true}
	return e, map[string]any{
		"schema_version": 1, "repository": e.Repository, "release": e.Release, "source_sha": e.SourceSHA,
		"source_tree": strings.Repeat("b", 40), "platform": "linux-amd64", "go_version": "go1.25.12",
		"node_manager": map[string]any{"path": clusterbootstrap.ManagerPath, "size": 2, "sha256": recoveryProgressDigest([]byte("nm"))},
		"asset":        map[string]any{"name": clusterbootstrap.BundleName, "size": 3, "sha256": recoveryProgressDigest([]byte("tar")), "url": clusterbootstrap.AssetURL(e, clusterbootstrap.BundleName)},
	}
}

func recoveryProgressMarshal(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func recoveryProgressPack(t *testing.T, members []recoveryProgressMember) []byte {
	t.Helper()
	return recoveryProgressPackWithCompressionLevel(t, members, gzip.DefaultCompression)
}

func recoveryProgressPackWithCompressionLevel(t *testing.T, members []recoveryProgressMember, level int) []byte {
	t.Helper()
	var out bytes.Buffer
	z, err := gzip.NewWriterLevel(&out, level)
	if err != nil {
		t.Fatal(err)
	}
	tarOut := tar.NewWriter(z)
	for _, m := range members {
		h := m.header
		if err := tarOut.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tarOut.Write(m.data); err != nil {
			t.Fatal(err)
		}
	}
	if tarOut.Close() != nil || z.Close() != nil {
		t.Fatal("fixture archive close failed")
	}
	return out.Bytes()
}

func recoveryProgressCommand(t *testing.T, root, binary string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, binary, args...)
	c.Dir = root
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "GOTOOLCHAIN=local", "GOWORK=off")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture recoveryProgressCommand failed: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func recoveryProgressArchiveFixture(t *testing.T) (clusterbootstrap.Expected, map[string]any, []recoveryProgressMember) {
	t.Helper()
	temporary := t.TempDir()
	original := filepath.Join(temporary, "original")
	root := filepath.Join(temporary, "bundle")
	manager := filepath.Join(original, "Data/Engine/Containers/api-backend/cmd/borealis-node-manager")
	if err := os.MkdirAll(manager, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(manager, "main.go"), "package main\nfunc main() {}\n")
	write(filepath.Join(original, "Data/Engine/Containers/api-backend/go.mod"), "module borealis/api-backend\n\ngo 1.25.0\n")
	write(filepath.Join(original, "Engine.sh"), "#!/bin/sh\nexit 0\n")
	write(filepath.Join(original, "Data/Engine/release-manifest.json"), `{"cluster_compatible":true,"required_k3s_baseline":"v1.36.3+k3s1","allowed_release_channels":["stable","qualification"]}`)
	git := func(args ...string) string { return recoveryProgressCommand(t, original, "git", args...) }
	git("init", "--quiet", "--template=")
	git("add", ".")
	git("-c", "user.name=Borealis Fixture", "-c", "user.email=tests@example.invalid", "commit", "--quiet", "-m", "fixture")
	e, value := recoveryProgressManifestFixture()
	e.SourceSHA = git("rev-parse", "HEAD")
	git("-c", "user.name=Borealis Fixture", "-c", "user.email=tests@example.invalid", "tag", "-a", e.Release, "-m", "fixture tag")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	git("clone", "--quiet", "--no-local", "--depth=1", "--single-branch", "--no-tags", "--template=", "--branch", e.Release, original, source)
	write(filepath.Join(source, ".git/config"), recoveryProgressSourceConfig(e.Repository))
	recoveryProgressCommand(t, source, "git", "repack", "-ad")
	for _, name := range []string{"logs", "hooks", "FETCH_HEAD", "ORIG_HEAD", "description", "index"} {
		if err := os.RemoveAll(filepath.Join(source, ".git", name)); err != nil {
			t.Fatal(err)
		}
	}
	recoveryProgressCommand(t, source, "git", "read-tree", e.SourceSHA)
	binary := filepath.Join(root, clusterbootstrap.ManagerPath)
	recoveryProgressCommand(t, filepath.Join(source, "Data/Engine/Containers/api-backend"), filepath.Join(runtime.GOROOT(), "bin/go"), "build", "-trimpath", "-buildvcs=false", "-o", binary, "./cmd/borealis-node-manager")
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	value["source_sha"] = e.SourceSHA
	value["source_tree"] = recoveryProgressCommand(t, source, "git", "rev-parse", "HEAD^{tree}")
	value["node_manager"] = map[string]any{"path": clusterbootstrap.ManagerPath, "size": len(data), "sha256": recoveryProgressDigest(data)}
	delete(value, "asset")
	write(filepath.Join(root, "identity.json"), string(recoveryProgressMarshal(t, value)))
	var members []recoveryProgressMember
	err = filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil || name == root {
			return err
		}
		relative, _ := filepath.Rel(root, name)
		h := tar.Header{Name: filepath.ToSlash(relative), Mode: 0o644, Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0)}
		var data []byte
		if entry.IsDir() {
			h.Typeflag, h.Mode = tar.TypeDir, 0o755
		} else {
			data, err = os.ReadFile(name)
			h.Size = int64(len(data))
			if relative == clusterbootstrap.ManagerPath {
				h.Mode = 0o755
			}
		}
		members = append(members, recoveryProgressMember{h, data})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return e, value, members
}

func recoveryProgressStageFixture(t *testing.T, ctx context.Context, e clusterbootstrap.Expected, value map[string]any, raw []byte, parent string) (*clusterbootstrap.Bundle, error) {
	t.Helper()
	value["asset"] = map[string]any{"name": clusterbootstrap.BundleName, "size": len(raw), "sha256": recoveryProgressDigest(raw), "url": clusterbootstrap.AssetURL(e, clusterbootstrap.BundleName)}
	m, err := clusterbootstrap.ParseManifest(recoveryProgressMarshal(t, value), e)
	if err != nil {
		t.Fatal(err)
	}
	return clusterbootstrap.Stage(ctx, parent, m, bytes.NewReader(raw), "")
}

type recoveryProgressMember struct {
	header tar.Header
	data   []byte
}

func recoveryProgressSourceConfig(repository string) string {
	return "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n" +
		"\tlogAllRefUpdates = false\n[remote \"origin\"]\n" +
		"\turl = https://github.com/" + repository + ".git\n" +
		"\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
}
