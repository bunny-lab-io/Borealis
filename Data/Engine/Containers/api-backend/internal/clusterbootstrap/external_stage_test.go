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

func TestExternalImageStagingAndGuardedTransfer(t *testing.T) {
	for _, mode := range []string{"success", "truncated", "cancel download", "lost authority", "changed measurement", "tampered scratch", "lost transfer authority", "cancel transfer", "unknown reference", "final transfer authority", "writer failure", "unreviewed pin"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wire := externalInventoryWire{}
			archives := map[string][]byte{}
			pins := map[string]ExternalImagePin{}
			for _, compiled := range ExternalImagePins() {
				raw, pin := externalFixtureReference(t, "gzip", compiled.Reference)
				archives[pin.Reference], pins[pin.Reference] = raw, pin
				proof, err := inspectExternalImageArchive(ctx, bytes.NewReader(raw), int64(len(raw)), pin)
				if err != nil {
					t.Fatal(err)
				}
				wire.Images = append(wire.Images, proof)
			}
			if mode == "changed measurement" {
				wire.Images[1].Layers[0].FileBytes++
			}
			raw, _ := json.Marshal(wire)
			// Private seam uses synthetic blobs. Public StageExternalImages always
			// selects compiled pins; real release archives exercise that path separately.
			inventory := &ExternalImageInventory{wire: wire, raw: raw}
			inspect := func(ctx context.Context, r io.ReaderAt, n int64, reference string) (ExternalImageProof, error) {
				return inspectExternalImageArchive(ctx, r, n, pins[reference])
			}
			if mode == "unreviewed pin" {
				inspect = InspectExternalImageArchive
			}
			parent := t.TempDir()
			marker := filepath.Join(parent, "caller-owned")
			if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			opened := 0
			check := func(context.Context) error {
				if mode == "lost authority" && opened == 2 {
					return ErrSessionAuthority
				}
				return nil
			}
			set, err := stageExternalImages(ctx, parent, inventory, func(_ context.Context, proof ExternalImageProof) (io.ReadCloser, error) {
				opened++
				data := archives[proof.Reference]
				if opened == 2 {
					if mode == "truncated" {
						data = data[:len(data)-1]
					}
					if mode == "cancel download" {
						cancel()
					}
				}
				return io.NopCloser(bytes.NewReader(data)), nil
			}, check, inspect)
			stageFails := mode == "truncated" || mode == "cancel download" || mode == "lost authority" || mode == "changed measurement" || mode == "unreviewed pin"
			if stageFails {
				entries, _ := os.ReadDir(parent)
				if err == nil || set != nil || len(entries) != 1 || entries[0].Name() != "caller-owned" {
					t.Fatalf("partial stage escaped: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer set.Close()
			reference := ExternalImagePins()[0].Reference
			if mode == "tampered scratch" {
				if err := os.WriteFile(filepath.Join(set.path, ExternalImageAssetName(reference)), bytes.Repeat([]byte("x"), len(archives[reference])), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unknown reference" {
				reference = "foreign"
			}
			var output bytes.Buffer
			calls := 0
			destination := io.Writer(&output)
			if mode == "writer failure" {
				destination = externalStageFailWriter{}
			}
			err = set.WriteArchive(ctx, reference, destination, func(context.Context) error {
				calls++
				if calls == 3 && mode == "final transfer authority" {
					return ErrSessionAuthority
				}
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
				if err != nil || !bytes.Equal(output.Bytes(), archives[reference]) {
					t.Fatalf("transfer failed: %v", err)
				}
			} else if mode == "final transfer authority" {
				if err == nil || !bytes.Equal(output.Bytes(), archives[reference]) {
					t.Fatal("completed bytes gained authority", err)
				}
			} else if err == nil || output.Len() != 0 {
				t.Fatalf("invalid transfer emitted bytes: %v", err)
			}
			if set.Close() != nil {
				t.Fatal("cleanup failed")
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 1 || entries[0].Name() != "caller-owned" || set.WriteArchive(context.Background(), reference, io.Discard, check) == nil {
				t.Fatal("closed set retained authority or files")
			}
		})
	}
}

// A destination failure cannot become a successful transfer receipt.
type externalStageFailWriter struct{}

func (externalStageFailWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
