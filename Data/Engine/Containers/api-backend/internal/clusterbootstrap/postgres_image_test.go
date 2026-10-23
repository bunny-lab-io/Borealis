package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func postgresImageFixture(t *testing.T, mode string, platform bool) (string, map[string][]byte) {
	t.Helper()
	raw, pin := externalFixture(t, mode)
	blobs := map[string][]byte{}
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(h.Name, "blobs/sha256/") {
			blobs["sha256:"+strings.TrimPrefix(h.Name, "blobs/sha256/")], err = io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	root := pin.IndexDigest
	if platform {
		root = pin.ManifestDigest
		delete(blobs, pin.IndexDigest)
	}
	return PostgresImageRepository + "@" + root, blobs
}

func TestPostgresImageAcquisitionAndTransfer(t *testing.T) {
	for _, mode := range []string{"index", "manifest", "docker index", "docker manifest", "truncated", "bad digest", "cancelled", "authority lost", "wrong diff", "unsafe layer", "architecture", "extra blob metadata", "final authority lost", "scratch tamper"} {
		t.Run(mode, func(t *testing.T) {
			fixture := "gzip"
			if strings.HasPrefix(mode, "docker") {
				fixture = "docker"
			}
			if mode == "wrong diff" || mode == "unsafe layer" || mode == "architecture" {
				fixture = mode
			}
			ref, blobs := postgresImageFixture(t, fixture, strings.Contains(mode, "manifest"))
			parent := t.TempDir()
			if os.WriteFile(filepath.Join(parent, "caller-owned"), []byte("keep"), 0600) != nil {
				t.Fatal("marker")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads, checks := 0, 0
			check := func(context.Context) error {
				checks++
				if mode == "authority lost" && reads > 1 {
					return ErrSessionAuthority
				}
				return nil
			}
			fetch := func(ctx context.Context, digest string, size int64, manifest bool, out io.Writer) error {
				reads++
				raw, ok := blobs[digest]
				if !ok {
					return ErrImageArchive
				}
				if mode == "truncated" && reads == 3 {
					raw = raw[:len(raw)-1]
				}
				if mode == "bad digest" && reads == 3 {
					raw = append(bytes.Clone(raw), byte('x'))
				}
				if mode == "cancelled" && reads == 3 {
					cancel()
				}
				if mode == "extra blob metadata" && reads == 1 {
					raw = bytes.Replace(raw, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":3`), 1)
				}
				_, err := out.Write(raw)
				return err
			}
			image, err := acquirePostgresImage(ctx, parent, ref, check, fetch)
			good := mode == "index" || mode == "manifest" || strings.HasPrefix(mode, "docker") || mode == "final authority lost" || mode == "scratch tamper"
			if !good {
				if image != nil || err == nil {
					t.Fatal("invalid archive escaped", err)
				}
				entries, _ := os.ReadDir(parent)
				if len(entries) != 1 || entries[0].Name() != "caller-owned" {
					t.Fatal("cleanup damaged caller files")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer image.Close()
			proof := image.Proof()
			if proof.Reference != ref || len(proof.Layers) != 1 || proof.Layers[0].FileBytes != 4 {
				t.Fatal("incorrect measured archive")
			}
			if (proof.IndexDigest == "") != strings.Contains(mode, "manifest") {
				t.Fatal("wrong root identity")
			}
			proof.Layers[0].FileBytes = 99
			if image.Proof().Layers[0].FileBytes != 4 {
				t.Fatal("borrowed proof")
			}
			if _, err = json.Marshal(image); err == nil {
				t.Fatal("private archive serialized")
			}
			if mode == "scratch tamper" {
				if os.WriteFile(filepath.Join(image.set.path, postgresArchiveName), []byte("changed"), 0600) != nil {
					t.Fatal("tamper")
				}
			}
			var output bytes.Buffer
			calls := 0
			err = image.WriteArchive(ctx, &output, func(context.Context) error {
				calls++
				if mode == "final authority lost" && calls == 3 {
					return ErrSessionAuthority
				}
				return nil
			})
			if mode == "final authority lost" || mode == "scratch tamper" {
				if err == nil {
					t.Fatal("failed transfer gained receipt")
				}
				if mode == "scratch tamper" && output.Len() != 0 {
					t.Fatal("tampered bytes escaped")
				}
			} else {
				hash := sha256.Sum256(output.Bytes())
				if err != nil || int64(output.Len()) != image.Proof().ArchiveBytes || hex.EncodeToString(hash[:]) != image.Proof().ArchiveSHA256 {
					t.Fatal("transfer mismatch", err)
				}
				if _, err = InspectPostgresImageArchive(ctx, bytes.NewReader(output.Bytes()), int64(output.Len()), ref); err != nil {
					t.Fatal(err)
				}
				if _, err = InspectPostgresImageArchive(ctx, bytes.NewReader(output.Bytes()), int64(output.Len()), PostgresImageRepository+"@sha256:"+strings.Repeat("f", 64)); err == nil {
					t.Fatal("wrong source root accepted")
				}
			}
			if image.Close() != nil || image.WriteArchive(ctx, io.Discard, check) == nil {
				t.Fatal("closed image retained authority")
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 1 || entries[0].Name() != "caller-owned" {
				t.Fatal("owned scratch retained")
			}
		})
	}
}

func TestPostgresImageRejectsMutableOrForeignReference(t *testing.T) {
	for _, ref := range []string{PostgresImageRepository + ":18.4-system-trixie", PostgresImageRepository + "@sha256:" + strings.Repeat("0", 64), PostgresImageRepository + "@sha256:" + strings.Repeat("A", 64), "ghcr.io/foreign/postgresql@sha256:" + strings.Repeat("a", 64), PostgresImageRepository + "@sha256:" + strings.Repeat("a", 64) + "?x=1"} {
		calls := 0
		v, err := acquirePostgresImage(context.Background(), t.TempDir(), ref, func(context.Context) error { return nil }, func(context.Context, string, int64, bool, io.Writer) error { calls++; return nil })
		if err == nil || v != nil || calls != 0 {
			t.Fatal("unbounded registry input accepted")
		}
	}
}
