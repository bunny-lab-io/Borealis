package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestImageStagingAndGuardedTransfer(t *testing.T) {
	for _, mode := range []string{"success", "truncated", "cancel download", "lost authority", "changed measurement", "tampered scratch", "lost transfer authority", "cancel transfer", "unknown role"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected, wire := imageInventoryFixture(t)
			archives := map[string][]byte{}
			// Retain these exact archives: fixture tar member order is not stable.
			for i, proof := range wire.Images {
				raw := imageFixture(t, "gzip", proof.Role)
				archives[proof.Role] = raw
				wire.Images[i], _ = InspectImageArchive(ctx, bytes.NewReader(raw), int64(len(raw)), proof.Role, expected.SourceSHA)
			}
			if mode == "changed measurement" {
				wire.Images[1].Layers[0].FileBytes++
			}
			raw, _ := json.Marshal(wire)
			inventory, err := ParseImageInventory(raw, expected)
			if err != nil {
				t.Fatal(err)
			}
			parent := t.TempDir()
			opened := 0
			check := func(context.Context) error {
				if mode == "lost authority" && opened == 2 {
					return ErrSessionAuthority
				}
				return nil
			}
			set, err := StageImages(ctx, parent, inventory, func(_ context.Context, proof ImageArchiveProof) (io.ReadCloser, error) {
				opened++
				data := archives[proof.Role]
				if opened == 2 {
					if mode == "truncated" {
						data = data[:len(data)-1]
					}
					if mode == "cancel download" {
						cancel()
					}
				}
				return io.NopCloser(bytes.NewReader(data)), nil
			}, check)
			stageFails := mode == "truncated" || mode == "cancel download" || mode == "lost authority" || mode == "changed measurement"
			if stageFails {
				entries, _ := os.ReadDir(parent)
				if err == nil || set != nil || len(entries) != 0 {
					t.Fatalf("partial stage escaped: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			role := ImageRoles()[0]
			if mode == "tampered scratch" {
				if err := os.WriteFile(filepath.Join(set.path, ImageAssetName(role)), bytes.Repeat([]byte("x"), len(archives[role])), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unknown role" {
				role = "foreign"
			}
			var output bytes.Buffer
			calls := 0
			err = set.WriteArchive(ctx, role, &output, func(context.Context) error {
				calls++
				if calls == 2 {
					if mode == "lost transfer authority" {
						return ErrSessionAuthority
					}
					if mode == "cancel transfer" {
						cancel()
					}
				}
				return nil
			})
			if mode == "success" {
				if err != nil || !bytes.Equal(output.Bytes(), archives[role]) {
					t.Fatalf("transfer failed: %v", err)
				}
			} else if err == nil || output.Len() != 0 {
				t.Fatalf("invalid transfer emitted bytes: %v", err)
			}
			if set.Close() != nil {
				t.Fatal("cleanup failed")
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 0 || set.WriteArchive(context.Background(), role, io.Discard, check) == nil {
				t.Fatal("closed set retained authority or files")
			}
		})
	}
}
