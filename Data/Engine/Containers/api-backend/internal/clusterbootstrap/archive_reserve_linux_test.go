//go:build linux

package clusterbootstrap

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type bootstrapReservationReader struct {
	input  io.Reader
	before func()
}

func (r bootstrapReservationReader) Read(p []byte) (int, error) { r.before(); return r.input.Read(p) }

func TestBootstrapScratchReservation(t *testing.T) {
	expected, value, members := archiveFixture(t)
	raw := pack(t, members)
	value["asset"] = map[string]any{"name": BundleName, "size": len(raw), "sha256": digest(raw), "url": AssetURL(expected, BundleName)}
	manifest, err := ParseManifest(marshal(t, value), expected)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "archive space failure", "extraction space failure", "cancel archive", "cancel extraction"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			parent := t.TempDir()
			marker := filepath.Join(parent, "caller-owned")
			if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			allocated, reads := 0, 0
			reserve := func(f *os.File, n int64) error {
				allocated++
				archive := filepath.Base(f.Name()) == BundleName
				if mode == "archive space failure" && archive || mode == "extraction space failure" && !archive {
					return syscall.ENOSPC
				}
				if err := reserveImageFile(f, n); err != nil {
					return err
				}
				info, err := f.Stat()
				if err != nil || info.Size() != n || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Blocks*512 < n {
					t.Fatal("bootstrap reservation is not physical destination storage")
				}
				if mode == "cancel archive" && archive || mode == "cancel extraction" && !archive {
					cancel()
				}
				return nil
			}
			input := bootstrapReservationReader{bytes.NewReader(raw), func() {
				reads++
				if allocated == 0 {
					t.Fatal("archive consumed before reservation")
				}
			}}
			bundle, err := stageBootstrapReserved(ctx, parent, manifest, input, "", reserve)
			if mode == "success" {
				if err != nil || bundle == nil || allocated < 2 || reads == 0 {
					t.Fatal("reserved bootstrap rejected", err)
				}
				if err := bundle.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || bundle != nil {
				t.Fatal("failed reservation escaped")
			}
			if (mode == "archive space failure" || mode == "cancel archive") && reads != 0 {
				t.Fatal("failed archive reservation consumed network body")
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 1 || entries[0].Name() != "caller-owned" {
				t.Fatal("owned scratch not removed")
			}
			keep, err := os.ReadFile(marker)
			if err != nil || string(keep) != "keep" {
				t.Fatal("caller file changed")
			}
		})
	}
}
