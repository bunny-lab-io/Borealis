package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func digest(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func manifestFixture() (Expected, map[string]any) {
	e := Expected{"bunny-lab-io/Borealis", "2026.09.999-rc.1", strings.Repeat("a", 40), true}
	return e, map[string]any{
		"schema_version": 1, "repository": e.Repository, "release": e.Release, "source_sha": e.SourceSHA,
		"source_tree": strings.Repeat("b", 40), "platform": "linux-amd64", "go_version": "go1.25.12",
		"node_manager": map[string]any{"path": ManagerPath, "size": 2, "sha256": digest([]byte("nm"))},
		"asset":        map[string]any{"name": BundleName, "size": 3, "sha256": digest([]byte("tar")), "url": AssetURL(e, BundleName)},
	}
}

func marshal(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestManifestRejectsAmbiguousOrUnboundIdentity(t *testing.T) {
	e, value := manifestFixture()
	raw := marshal(t, value)
	if _, err := ParseManifest(raw, e); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func([]byte) []byte{
		"duplicate":         func(b []byte) []byte { return append([]byte(`{"schema_version":1,`), b[1:]...) },
		"escaped duplicate": func(b []byte) []byte { return append([]byte(`{"\u0073chema_version":1,`), b[1:]...) },
		"case alias": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema_version"`), []byte(`"Schema_Version"`), 1)
		},
		"unknown":            func(b []byte) []byte { return append([]byte(`{"extra":true,`), b[1:]...) },
		"null nested":        func(b []byte) []byte { return bytes.Replace(b, []byte(`"size":2`), []byte(`"size":null`), 1) },
		"nested duplicate":   func(b []byte) []byte { return bytes.Replace(b, []byte(`"size":2`), []byte(`"size":2,"size":2`), 1) },
		"string number":      func(b []byte) []byte { return bytes.Replace(b, []byte(`"size":2`), []byte(`"size":"2"`), 1) },
		"fractional integer": func(b []byte) []byte { return bytes.Replace(b, []byte(`"size":2`), []byte(`"size":2.0`), 1) },
		"trailing":           func(b []byte) []byte { return append(b, []byte(`{}`)...) },
		"invalid utf8":       func(b []byte) []byte { return append(b, 0xff) },
		"oversized":          func(b []byte) []byte { return append(b, bytes.Repeat([]byte(" "), MaxManifestBytes)...) },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest(change(bytes.Clone(raw)), e); err == nil {
				t.Fatal("accepted ambiguous manifest")
			}
		})
	}
	for name, change := range map[string]func(map[string]any){
		"wrong schema":     func(v map[string]any) { v["schema_version"] = 2 },
		"wrong repository": func(v map[string]any) { v["repository"] = "unrelated/Borealis" },
		"wrong release":    func(v map[string]any) { v["release"] = "main" },
		"wrong commit":     func(v map[string]any) { v["source_sha"] = strings.Repeat("c", 40) },
		"invalid tree":     func(v map[string]any) { v["source_tree"] = strings.Repeat("0", 40) },
		"missing field":    func(v map[string]any) { delete(v, "platform") },
		"wrong platform":   func(v map[string]any) { v["platform"] = "linux-arm64" },
		"wrong Go":         func(v map[string]any) { v["go_version"] = "go1.25.0" },
		"wrong asset URL":  func(v map[string]any) { v["asset"].(map[string]any)["url"] = "https://example.invalid/bundle" },
		"oversized asset":  func(v map[string]any) { v["asset"].(map[string]any)["size"] = MaxBundleBytes + 1 },
		"invalid digest":   func(v map[string]any) { v["asset"].(map[string]any)["sha256"] = strings.Repeat("A", 64) },
		"binary escape":    func(v map[string]any) { v["node_manager"].(map[string]any)["path"] = "../node-manager" },
	} {
		t.Run(name, func(t *testing.T) {
			_, changed := manifestFixture()
			change(changed)
			if _, err := ParseManifest(marshal(t, changed), e); err == nil {
				t.Fatal("accepted mismatched identity")
			}
		})
	}
	e.AllowQualification = false
	if _, err := ParseManifest(raw, e); err == nil {
		t.Fatal("qualification accepted without opt-in")
	}
	for _, e := range []Expected{{"../outside", "2026.09.1", strings.Repeat("a", 40), false}, {"org/repo", "main", strings.Repeat("a", 40), true}, {"org/repo", "2026.09.1", "HEAD", false}} {
		if e.Validate() == nil {
			t.Fatal("invalid expected identity accepted")
		}
	}
}

type member struct {
	header tar.Header
	data   []byte
}

func pack(t *testing.T, members []member) []byte {
	t.Helper()
	var out bytes.Buffer
	z := gzip.NewWriter(&out)
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

func command(t *testing.T, root, binary string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, binary, args...)
	c.Dir = root
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "GOTOOLCHAIN=local", "GOWORK=off")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture command failed: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// Real Git objects and compiled Go executable exercise the complete receiver.
func archiveFixture(t *testing.T) (Expected, map[string]any, []member) {
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
	git := func(args ...string) string { return command(t, original, "git", args...) }
	git("init", "--quiet", "--template=")
	git("add", ".")
	git("-c", "user.name=Borealis Fixture", "-c", "user.email=tests@example.invalid", "commit", "--quiet", "-m", "fixture")
	e, value := manifestFixture()
	e.SourceSHA = git("rev-parse", "HEAD")
	git("-c", "user.name=Borealis Fixture", "-c", "user.email=tests@example.invalid", "tag", "-a", e.Release, "-m", "fixture tag")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	git("clone", "--quiet", "--no-local", "--depth=1", "--single-branch", "--no-tags", "--template=", "--branch", e.Release, original, source)
	write(filepath.Join(source, ".git/config"), sourceConfig(e.Repository))
	command(t, source, "git", "repack", "-ad")
	for _, name := range []string{"logs", "hooks", "FETCH_HEAD", "ORIG_HEAD", "description", "index"} {
		if err := os.RemoveAll(filepath.Join(source, ".git", name)); err != nil {
			t.Fatal(err)
		}
	}
	command(t, source, "git", "read-tree", e.SourceSHA)
	binary := filepath.Join(root, ManagerPath)
	command(t, filepath.Join(source, "Data/Engine/Containers/api-backend"), filepath.Join(runtime.GOROOT(), "bin/go"), "build", "-trimpath", "-buildvcs=false", "-o", binary, "./cmd/borealis-node-manager")
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	value["source_sha"] = e.SourceSHA
	value["source_tree"] = command(t, source, "git", "rev-parse", "HEAD^{tree}")
	value["node_manager"] = map[string]any{"path": ManagerPath, "size": len(data), "sha256": digest(data)}
	delete(value, "asset")
	write(filepath.Join(root, "identity.json"), string(marshal(t, value)))
	var members []member
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
			if relative == ManagerPath {
				h.Mode = 0o755
			}
		}
		members = append(members, member{h, data})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return e, value, members
}

func stageFixture(t *testing.T, ctx context.Context, e Expected, value map[string]any, raw []byte, parent string) (*Bundle, error) {
	t.Helper()
	value["asset"] = map[string]any{"name": BundleName, "size": len(raw), "sha256": digest(raw), "url": AssetURL(e, BundleName)}
	m, err := ParseManifest(marshal(t, value), e)
	if err != nil {
		t.Fatal(err)
	}
	return Stage(ctx, parent, m, bytes.NewReader(raw), "")
}

func TestStageVerifiesRealSourceAndRejectsTamperedArchives(t *testing.T) {
	e, value, members := archiveFixture(t)
	raw := pack(t, members)
	parent := t.TempDir()
	b, err := stageFixture(t, context.Background(), e, value, raw, parent)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(b.root); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("scratch mode: %v %v", info, err)
	}
	if got := command(t, b.SourcePath(), "git", "status", "--porcelain"); got != "" {
		t.Fatalf("source dirty: %s", got)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	mutateMember := func(m []member, name string, change func(*member)) []member {
		for index := range m {
			if m[index].header.Name == name {
				change(&m[index])
				return m
			}
		}
		t.Fatalf("missing fixture member %s", name)
		return nil
	}
	for name, mutate := range map[string]func([]member) []member{
		"traversal": func(m []member) []member { m[len(m)-1].header.Name = "../escaped"; return m },
		"absolute":  func(m []member) []member { m[len(m)-1].header.Name = "/escaped"; return m },
		"duplicate": func(m []member) []member { return append(m, m[len(m)-1]) },
		"symlink": func(m []member) []member {
			return append(m, member{tar.Header{Name: "source/link", Typeflag: tar.TypeSymlink, Linkname: "../../escaped", Mode: 0o755, ModTime: time.Unix(0, 0)}, nil})
		},
		"hardlink": func(m []member) []member {
			return append(m, member{tar.Header{Name: "source/link", Typeflag: tar.TypeLink, Linkname: ManagerPath, Mode: 0o644, ModTime: time.Unix(0, 0)}, nil})
		},
		"special file": func(m []member) []member {
			return append(m, member{tar.Header{Name: "source/fifo", Typeflag: tar.TypeFifo, Mode: 0o644, ModTime: time.Unix(0, 0)}, nil})
		},
		"missing parent": func(m []member) []member { m[len(m)-1].header.Name = "source/missing/member"; return m },
		"suid": func(m []member) []member {
			return mutateMember(m, ManagerPath, func(m *member) { m.header.Mode = 0o4755 })
		},
		"binary changed": func(m []member) []member {
			return mutateMember(m, ManagerPath, func(m *member) { m.data = bytes.Clone(m.data); m.data[100] ^= 1 })
		},
		"source changed": func(m []member) []member {
			return mutateMember(m, "source/Engine.sh", func(m *member) { m.data = []byte("changed\n"); m.header.Size = int64(len(m.data)) })
		},
		"source omitted": func(m []member) []member {
			for n := range m {
				if m[n].header.Name == "source/Engine.sh" {
					return append(m[:n], m[n+1:]...)
				}
			}
			return m
		},
		"source extra": func(m []member) []member {
			return append(m, member{tar.Header{Name: "source/operator-file", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: time.Unix(0, 0)}, nil})
		},
		"git hook": func(m []member) []member {
			return append(m, member{tar.Header{Name: "source/.git/hooks", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: time.Unix(0, 0)}, nil})
		},
		"git alternate": func(m []member) []member {
			return append(m, member{tar.Header{Name: "source/.git/objects/info/alternates", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: time.Unix(0, 0)}, nil})
		},
		"git config": func(m []member) []member {
			return mutateMember(m, "source/.git/config", func(m *member) {
				m.data = append(bytes.Clone(m.data), []byte("[core]\nfsmonitor = unwanted-command\n")...)
				m.header.Size = int64(len(m.data))
			})
		},
		"wrong inner": func(m []member) []member {
			return mutateMember(m, "identity.json", func(m *member) {
				m.data = bytes.ReplaceAll(m.data, []byte(e.SourceSHA), []byte(strings.Repeat("c", 40)))
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := append([]member{}, members...)
			if b, err := stageFixture(t, context.Background(), e, value, pack(t, mutate(changed)), parent); err == nil {
				_ = b.Close()
				t.Fatal("accepted unsafe or mismatched archive")
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed scratch retained: %v %v", entries, err)
			}
		})
	}
	for name, changed := range map[string][]byte{
		"truncated gzip": raw[:len(raw)-4], "gzip checksum": append(bytes.Clone(raw[:len(raw)-8]), bytes.Repeat([]byte{0}, 8)...),
		"second gzip": append(bytes.Clone(raw), raw...), "compressed junk": append(bytes.Clone(raw), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if b, err := stageFixture(t, context.Background(), e, value, changed, parent); err == nil {
				_ = b.Close()
				t.Fatal("accepted corrupt gzip")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b, err := stageFixture(t, ctx, e, value, raw, parent); err == nil || b != nil {
		t.Fatal("cancelled staging succeeded")
	}
	value["asset"] = map[string]any{"name": BundleName, "size": len(raw), "sha256": digest(raw), "url": AssetURL(e, BundleName)}
	m, err := ParseManifest(marshal(t, value), e)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []io.Reader{bytes.NewReader(raw[:len(raw)-1]), bytes.NewReader(append(bytes.Clone(raw), 0)), bytes.NewReader(bytes.Repeat([]byte{0}, len(raw)))} {
		if b, err := Stage(context.Background(), parent, m, input, ""); err == nil || b != nil {
			t.Fatal("compressed size/hash not enforced")
		}
	}
}
