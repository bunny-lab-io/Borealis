package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func k3sStageFixture(t *testing.T) (*K3sInventory, K3sInputPins, map[string][]byte) {
	t.Helper()
	archive, pins := k3sArchiveFixture(t, "gzip")
	data := map[string][]byte{K3sArchiveName: archive, K3sBinaryName: []byte("inert K3s fixture"), K3sInstallerName: []byte("inert installer fixture")}
	pin := func(name string) K3sAssetPin {
		h := sha256.Sum256(data[name])
		return K3sAssetPin{Name: name, Size: int64(len(data[name])), SHA256: hex.EncodeToString(h[:])}
	}
	pins.Binary, pins.Installer, pins.Version = pin(K3sBinaryName), pin(K3sInstallerName), "v1-fixture"
	proof, err := inspectK3sArchive(context.Background(), bytes.NewReader(archive), int64(len(archive)), pins)
	if err != nil {
		t.Fatal(err)
	}
	pins.Payload = K3sPayloadPin{CompressedBytes: 1, SHA256: strings.Repeat("a", 64), TarSHA256: strings.Repeat("b", 64), TarBytes: 1024, FileBytes: 1, Entries: 1}
	wire := k3sInventoryWire{Version: 2, Repository: "bunny-lab-io/Borealis", Release: "2026.09.999-rc.1", SourceSHA: imageTestSHA, Platform: "linux-amd64", K3sVersion: pins.Version, Binary: pins.Binary, Installer: pins.Installer, Payload: pins.Payload, Archive: proof}
	raw, _ := json.Marshal(wire)
	return &K3sInventory{wire: wire, raw: raw}, pins, data
}

func TestK3sStagingAndTransfer(t *testing.T) {
	for _, mode := range []string{"success", "truncated", "oversized", "changed bytes", "changed measurement", "allocation", "nil input", "open error", "lost authority", "cancel download", "unreviewed pins", "tampered scratch", "linked scratch", "mode drift", "unknown asset", "lost transfer authority", "cancel transfer", "writer failure", "final authority", "closed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			inventory, pins, data := k3sStageFixture(t)
			parent := t.TempDir()
			if err := os.WriteFile(filepath.Join(parent, "caller-owned"), []byte("retain"), 0600); err != nil {
				t.Fatal(err)
			}
			allocated, opened, closed := 0, 0, 0
			allocate := func(f *os.File, n int64) error {
				allocated++
				if opened != 0 {
					t.Fatal("network preceded complete reservation")
				}
				if mode == "allocation" && allocated == 2 {
					return ErrImageArchive
				}
				return reserveImageFile(f, n)
			}
			open := func(_ context.Context, name string) (io.ReadCloser, error) {
				opened++
				if allocated != 3 {
					t.Fatal("incomplete reservation")
				}
				raw := bytes.Clone(data[name])
				if opened == 2 {
					switch mode {
					case "truncated":
						raw = raw[:len(raw)-1]
					case "oversized":
						raw = append(raw, 'x')
					case "changed bytes":
						raw[0] ^= 1
					case "nil input":
						return nil, nil
					case "cancel download":
						cancel()
					}
				}
				input := &k3sStageReader{Reader: bytes.NewReader(raw), close: func() { closed++ }}
				if mode == "open error" && opened == 2 {
					return input, ErrImageArchive
				}
				return input, nil
			}
			check := func(context.Context) error {
				if mode == "lost authority" && opened == 2 {
					return ErrSessionAuthority
				}
				return nil
			}
			if mode == "changed measurement" {
				inventory.wire.Archive.ExpandedBytes++
			}
			if mode == "unreviewed pins" {
				pins = K3sPins()
			}
			set, err := stageK3sInputs(ctx, parent, inventory, open, check, pins, allocate)
			stageFails := mode == "truncated" || mode == "oversized" || mode == "changed bytes" || mode == "changed measurement" || mode == "allocation" || mode == "nil input" || mode == "open error" || mode == "lost authority" || mode == "cancel download" || mode == "unreviewed pins"
			wantClosed := opened
			if mode == "nil input" {
				wantClosed--
			}
			if closed != wantClosed {
				t.Fatal("unclosed input", opened, closed)
			}
			if stageFails {
				entries, readErr := os.ReadDir(parent)
				if err == nil || set != nil || readErr != nil || len(entries) != 1 || entries[0].Name() != "caller-owned" {
					t.Fatal("partial set escaped or cleanup damaged caller", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			if strings.Contains(fmt.Sprintf("%v %#v", set, set), set.path) {
				t.Fatal("private path leaked")
			}
			if _, err := json.Marshal(set); err == nil {
				t.Fatal("generic serialization accepted")
			}
			name := K3sBinaryName
			path := filepath.Join(set.path, name)
			switch mode {
			case "tampered scratch":
				os.WriteFile(path, bytes.Repeat([]byte("x"), len(data[name])), 0600)
			case "linked scratch":
				os.Rename(path, path+".real")
				os.Symlink(path+".real", path)
			case "mode drift":
				os.Chmod(path, 0755)
			case "unknown asset":
				name = "../../unknown"
			case "closed":
				if set.Close() != nil {
					t.Fatal("close failed")
				}
			}
			var output bytes.Buffer
			writer := io.Writer(&output)
			if mode == "writer failure" {
				writer = externalStageFailWriter{}
			}
			calls := 0
			err = set.WriteAsset(ctx, name, writer, func(context.Context) error {
				calls++
				if mode == "lost transfer authority" && calls == 2 || mode == "final authority" && calls == 3 {
					return ErrSessionAuthority
				}
				if mode == "cancel transfer" && calls == 2 {
					cancel()
				}
				return nil
			})
			if (err == nil) != (mode == "success") {
				t.Fatal("incorrect guarded transfer", err)
			}
			if mode == "success" {
				if !bytes.Equal(output.Bytes(), data[name]) {
					t.Fatal("changed stream")
				}
				for _, other := range []string{K3sInstallerName, K3sArchiveName} {
					output.Reset()
					if set.WriteAsset(ctx, other, &output, check) != nil || !bytes.Equal(output.Bytes(), data[other]) {
						t.Fatal("incomplete set transfer")
					}
				}
			}
			if set.Close() != nil || set.Close() != nil {
				t.Fatal("cleanup not idempotent")
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 1 || entries[0].Name() != "caller-owned" {
				t.Fatal("owned scratch retained or caller removed")
			}
		})
	}
}

type k3sStageReader struct {
	io.Reader
	close func()
}

func (r *k3sStageReader) Close() error { r.close(); return nil }
