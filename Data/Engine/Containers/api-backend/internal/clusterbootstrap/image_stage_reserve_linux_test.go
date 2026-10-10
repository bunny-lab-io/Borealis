//go:build linux

package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestImageSetsReserveBeforeDownload(t *testing.T) {
	for _, kind := range []string{"application", "external"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			expected, appWire := imageInventoryFixture(t)
			appArchives := map[string][]byte{}
			for i, proof := range appWire.Images {
				raw := imageFixture(t, "gzip", proof.Role)
				appArchives[proof.Role] = raw
				appWire.Images[i], _ = InspectImageArchive(ctx, bytes.NewReader(raw), int64(len(raw)), proof.Role, expected.SourceSHA)
			}
			raw, _ := json.Marshal(appWire)
			apps, err := ParseImageInventory(raw, expected)
			if err != nil {
				t.Fatal(err)
			}
			externalWire := externalInventoryWire{}
			externalArchives := map[string][]byte{}
			pins := map[string]ExternalImagePin{}
			for _, compiled := range ExternalImagePins() {
				raw, pin := externalFixtureReference(t, "gzip", compiled.Reference)
				externalArchives[pin.Reference], pins[pin.Reference] = raw, pin
				proof, err := inspectExternalImageArchive(ctx, bytes.NewReader(raw), int64(len(raw)), pin)
				if err != nil {
					t.Fatal(err)
				}
				externalWire.Images = append(externalWire.Images, proof)
			}
			raw, _ = json.Marshal(externalWire)
			external := &ExternalImageInventory{wire: externalWire, raw: raw}
			inspect := func(ctx context.Context, r io.ReaderAt, n int64, reference string) (ExternalImageProof, error) {
				return inspectExternalImageArchive(ctx, r, n, pins[reference])
			}
			for _, mode := range []string{"success", "space failure", "cancel allocation", "authority lost", "replaced reservation"} {
				t.Run(mode, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					parent := t.TempDir()
					marker := filepath.Join(parent, "caller-owned")
					if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
						t.Fatal(err)
					}
					allocated, opened := 0, 0
					revoked := false
					files := []*os.File{}
					check := func(context.Context) error {
						if revoked {
							return ErrSessionAuthority
						}
						return nil
					}
					reserve := func(f *os.File, n int64) error {
						files = append(files, f)
						allocated++
						if allocated == 3 && mode == "space failure" {
							return syscall.ENOSPC
						}
						if err := reserveImageFile(f, n); err != nil {
							return err
						}
						st, err := f.Stat()
						if err != nil || st.Size() != n || st.Mode().Perm() != 0600 || st.Sys().(*syscall.Stat_t).Blocks*512 < n {
							t.Fatal("reservation is not physical destination storage")
						}
						if allocated == 3 && mode == "cancel allocation" {
							cancel()
						}
						if allocated == 3 && mode == "authority lost" {
							revoked = true
						}
						return nil
					}
					beforeFetch := func() {
						opened++
						count := len(apps.Images())
						if kind == "external" {
							count = len(external.Images())
						}
						if allocated != count {
							t.Fatal("download preceded complete-set reservation")
						}
						if opened == 1 && mode == "replaced reservation" {
							// Replace every named destination while descriptors retain original inodes.
							for _, f := range files {
								name := f.Name()
								if err := os.Rename(name, name+".moved"); err != nil {
									t.Fatal(err)
								}
								if err := os.WriteFile(name, []byte("other"), 0600); err != nil {
									t.Fatal(err)
								}
							}
						}
					}
					var closeSet func() error
					if kind == "application" {
						set, err := stageImagesReserved(ctx, parent, apps, func(_ context.Context, p ImageArchiveProof) (io.ReadCloser, error) {
							beforeFetch()
							return io.NopCloser(bytes.NewReader(appArchives[p.Role])), nil
						}, check, reserve)
						if mode == "success" {
							if err != nil || set == nil {
								t.Fatal(err)
							}
							closeSet = set.Close
						} else if err == nil || set != nil {
							t.Fatal("unreserved or replaced set escaped")
						}
					} else {
						set, err := stageExternalImagesReserved(ctx, parent, external, func(_ context.Context, p ExternalImageProof) (io.ReadCloser, error) {
							beforeFetch()
							return io.NopCloser(bytes.NewReader(externalArchives[p.Reference])), nil
						}, check, inspect, reserve)
						if mode == "success" {
							if err != nil || set == nil {
								t.Fatal(err)
							}
							closeSet = set.Close
						} else if err == nil || set != nil {
							t.Fatal("unreserved or replaced set escaped")
						}
					}
					if mode != "success" && mode != "replaced reservation" && opened != 0 {
						t.Fatal("failed allocation reached network")
					}
					for _, f := range files {
						if _, err := f.Stat(); err == nil {
							t.Fatal("reservation descriptor leaked")
						}
					}
					if closeSet != nil && closeSet() != nil {
						t.Fatal("cleanup failed")
					}
					entries, err := os.ReadDir(parent)
					if err != nil || len(entries) != 1 || entries[0].Name() != "caller-owned" {
						t.Fatal("owned cleanup failed")
					}
					keep, err := os.ReadFile(marker)
					if err != nil || string(keep) != "keep" {
						t.Fatal("caller file changed")
					}
				})
			}
		})
	}
}
