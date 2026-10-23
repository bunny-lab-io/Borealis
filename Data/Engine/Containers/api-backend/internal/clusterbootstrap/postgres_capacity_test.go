//go:build linux

package clusterbootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPostgresImageReservationAndMeasuredDemand(t *testing.T) {
	for _, mode := range []string{"index", "manifest", "blob allocation fails", "archive allocation fails", "cancel allocation", "authority allocation", "short fetch", "oversized fetch"} {
		t.Run(mode, func(t *testing.T) {
			ref, blobs := postgresImageFixture(t, "gzip", mode == "manifest")
			parent := t.TempDir()
			if err := os.WriteFile(filepath.Join(parent, "caller-owned"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			allocated, bulk, metadata := 0, 0, 0
			archiveReserved, revoked := false, false
			var peak int64
			check := func(context.Context) error {
				if revoked {
					return ErrSessionAuthority
				}
				return nil
			}
			reserve := func(f *os.File, size int64) error {
				allocated++
				archive := filepath.Base(f.Name()) == postgresArchiveName
				if mode == "blob allocation fails" && !archive || mode == "archive allocation fails" && archive {
					return syscall.ENOSPC
				}
				if err := reserveImageFile(f, size); err != nil {
					return err
				}
				st, err := f.Stat()
				if err != nil || st.Size() != size || st.Mode().Perm() != 0600 {
					t.Fatal("reservation shape")
				}
				// Native allocation must own physical blocks, not only a sparse length.
				if st.Sys().(*syscall.Stat_t).Blocks*512 < size {
					t.Fatal("sparse reservation")
				}
				peak += size
				if mode == "cancel allocation" {
					cancel()
				}
				if mode == "authority allocation" {
					revoked = true
				}
				archiveReserved = archiveReserved || archive
				return nil
			}
			fetch := func(ctx context.Context, digest string, size int64, manifest bool, out io.Writer) error {
				raw := blobs[digest]
				if manifest {
					metadata++
					peak += int64(len(raw))
				} else {
					bulk++
					if !archiveReserved || allocated != 3 {
						t.Fatal("bulk download before complete reservation")
					}
					if mode == "short fetch" {
						raw = raw[:len(raw)-1]
					}
					if mode == "oversized fetch" {
						raw = append(bytes.Clone(raw), 0)
					}
				}
				_, err := out.Write(raw)
				return err
			}
			image, err := acquirePostgresImageReserved(ctx, parent, ref, check, fetch, reserve)
			good := mode == "index" || mode == "manifest"
			if !good {
				if err == nil || image != nil {
					t.Fatal("invalid acquisition escaped")
				}
				if !strings.Contains(mode, "fetch") && bulk != 0 {
					t.Fatal("failed reservation downloaded blobs")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer image.Close()
				p := image.Proof()
				d, err := image.StorageDemand(ctx, check)
				if err != nil {
					t.Fatal(err)
				}
				if d.Reference != ref || d.IncomingBytes != uint64(p.ArchiveBytes) || d.IncomingEntries != 1 || d.WorkerPeakBytes != uint64(peak) || d.WorkerPeakEntries != uint64(len(blobs)+2) || d.RuntimeBytes != uint64(2*p.ArchiveBytes+p.ContentBytes+p.Layers[0].TarBytes) || d.RuntimeEntries != uint64(2+p.ContentEntries+p.Layers[0].Entries) {
					t.Fatal("measured peak/target demand mismatch")
				}
				if metadata != len(blobs)-2 || bulk != 2 {
					t.Fatal("unexpected fetch set")
				}
				entries, err := os.ReadDir(image.set.path)
				if err != nil || len(entries) != 1 {
					t.Fatal("blob reservations retained after verification")
				}
				revoked = true
				if _, err = image.StorageDemand(ctx, check); err == nil {
					t.Fatal("authority loss accepted")
				}
				revoked = false
				if err = image.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err = image.StorageDemand(ctx, check); err == nil {
					t.Fatal("closed image supplied cached demand")
				}
			}
			entries, _ := os.ReadDir(parent)
			if len(entries) != 1 || entries[0].Name() != "caller-owned" {
				t.Fatal("cleanup changed caller files or leaked reservation")
			}
		})
	}
}

func TestPostgresImageDemandRejectsTamperAndFinalAuthorityLoss(t *testing.T) {
	for _, mode := range []string{"tamper", "final authority", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ref, blobs := postgresImageFixture(t, "gzip", false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			image, err := acquirePostgresImage(ctx, t.TempDir(), ref, func(context.Context) error { return nil }, func(_ context.Context, digest string, _ int64, _ bool, out io.Writer) error {
				_, err := out.Write(blobs[digest])
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			defer image.Close()
			if mode == "tamper" {
				if err = os.WriteFile(filepath.Join(image.set.path, postgresArchiveName), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "cancelled" {
				cancel()
			}
			checks := 0
			d, err := image.StorageDemand(ctx, func(context.Context) error {
				checks++
				if mode == "final authority" && checks == 3 {
					return errors.New("lost")
				}
				return nil
			})
			if err == nil || d != (PostgresImageDemand{}) {
				t.Fatal("invalid demand escaped")
			}
		})
	}
}
