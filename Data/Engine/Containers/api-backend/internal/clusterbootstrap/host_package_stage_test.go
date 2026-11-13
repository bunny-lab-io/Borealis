package clusterbootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type hostPackageTestBody struct {
	io.Reader
	close func() error
}

func (b hostPackageTestBody) Close() error { return b.close() }

func hostPackageStageFixture(t *testing.T) (Expected, *HostPackageInventory, []byte) {
	t.Helper()
	plan, digest := hostPackageTestPlan(t)
	archive := hostPackageTestArchive(t, plan, "valid")
	expected, wire := hostPackageTestInventory(t, plan, digest, archive)
	raw, _ := json.Marshal(wire)
	inventory, err := parseHostPackageInventory(raw, expected, plan, digest)
	if err != nil {
		t.Fatal(err)
	}
	return expected, inventory, archive
}

func TestHostPackageStageOwnership(t *testing.T) {
	for _, mode := range []string{"valid", "source mismatch", "body failure", "open failure with body", "short", "long", "changed archive", "cancel during closure", "authority after closure"} {
		t.Run(mode, func(t *testing.T) {
			expected, inventory, archive := hostPackageStageFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			closed, opened, lost := 0, 0, false
			if mode == "source mismatch" {
				expected.Release = "2026.11.998-rc.1"
			}
			if mode == "short" {
				archive = archive[:len(archive)-1]
			}
			if mode == "long" {
				archive = append(archive, 1)
			}
			if mode == "changed archive" {
				archive[1024] ^= 1
			}
			parent := t.TempDir()
			set, err := StageHostPackages(ctx, parent, expected, inventory, func(context.Context) (io.ReadCloser, error) {
				opened++
				body := hostPackageTestBody{Reader: bytes.NewReader(archive), close: func() error {
					closed++
					if mode == "cancel during closure" {
						cancel()
					}
					if mode == "authority after closure" {
						lost = true
					}
					if mode == "body failure" {
						return errors.New("fixture close")
					}
					return nil
				}}
				if mode == "open failure with body" {
					return body, errors.New("fixture open")
				}
				return body, nil
			}, func(ctx context.Context) error {
				if lost {
					return ErrSessionAuthority
				}
				return ctx.Err()
			})
			if mode == "valid" {
				if err != nil || set == nil {
					t.Fatal("valid acquisition rejected", err)
				}
				var output bytes.Buffer
				if set.WriteArchive(ctx, &output, func(ctx context.Context) error { return ctx.Err() }) != nil || !bytes.Equal(output.Bytes(), archive) || set.Close() != nil {
					t.Fatal("owned archive did not complete transfer and cleanup")
				}
				if set.WriteArchive(ctx, io.Discard, func(context.Context) error { return nil }) == nil {
					t.Fatal("closed owner reused")
				}
			} else if err == nil || set != nil {
				t.Fatal("failed input retained usable owner", err)
			}
			if closed != opened || mode != "source mismatch" && closed != 1 || mode == "source mismatch" && opened != 0 {
				t.Fatal("response lifetime not joined", opened, closed)
			}
			left, err := os.ReadDir(parent)
			if err != nil || len(left) != 0 {
				t.Fatal("owned scratch escaped", left, err)
			}
		})
	}
}

func TestHostPackageStageReproofAndReplacement(t *testing.T) {
	for _, mode := range []string{"bytes changed", "writer failure", "directory replaced", "unexpected child"} {
		t.Run(mode, func(t *testing.T) {
			expected, inventory, archive := hostPackageStageFixture(t)
			ctx := context.Background()
			check := func(context.Context) error { return nil }
			parent := t.TempDir()
			set, err := StageHostPackages(ctx, parent, expected, inventory, func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(archive)), nil }, check)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "directory replaced" {
				old := set.path
				if os.Rename(old, old+"-retained") != nil || os.Mkdir(old, 0700) != nil || os.WriteFile(filepath.Join(old, "unrelated"), []byte("keep"), 0600) != nil {
					t.Fatal("fixture replacement failed")
				}
				if set.Close() == nil {
					t.Fatal("replacement accepted as owned cleanup")
				}
				if raw, err := os.ReadFile(filepath.Join(old, "unrelated")); err != nil || string(raw) != "keep" {
					t.Fatal("removed unrelated replacement")
				}
				if _, err := os.Lstat(filepath.Join(old+"-retained", inventory.Archive().Name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("renamed owner retained archive instead of descriptor-rooted cleanup", err)
				}
				return
			}
			if mode == "unexpected child" {
				unrelated := filepath.Join(set.path, "unrelated", "keep")
				if os.Mkdir(filepath.Dir(unrelated), 0700) != nil || os.WriteFile(unrelated, []byte("keep"), 0600) != nil {
					t.Fatal("fixture child failed")
				}
				if set.Close() == nil {
					t.Fatal("unexpected contents acknowledged as complete cleanup")
				}
				if raw, err := os.ReadFile(unrelated); err != nil || string(raw) != "keep" {
					t.Fatal("recursively removed unrelated child")
				}
				return
			}
			defer set.Close()
			var output io.Writer = io.Discard
			if mode == "bytes changed" {
				archive[1024] ^= 1
				if os.WriteFile(filepath.Join(set.path, inventory.Archive().Name), archive, 0600) != nil {
					t.Fatal("fixture change failed")
				}
			} else {
				output = hostPackageTestWriter(func([]byte) (int, error) { return 0, errors.New("fixture writer") })
			}
			if set.WriteArchive(ctx, output, check) == nil || set.WriteArchive(ctx, io.Discard, check) == nil {
				t.Fatal("failed byte/transport proof did not latch")
			}
		})
	}
}

type hostPackageTestWriter func([]byte) (int, error)

func (w hostPackageTestWriter) Write(b []byte) (int, error) { return w(b) }

func TestHostPackageStageRejectsTransientStreamChange(t *testing.T) {
	plan, _ := hostPackageTestPlan(t)
	var archive bytes.Buffer
	tarWriter := tar.NewWriter(&archive)
	for i := range plan.Packages {
		p := &plan.Packages[i]
		body := bytes.Repeat([]byte("fixture "+p.Name), 4096)
		digest := sha256.Sum256(body)
		p.Size, p.SHA256 = int64(len(body)), hex.EncodeToString(digest[:])
		if err := tarWriter.WriteHeader(&tar.Header{Name: p.File, Size: p.Size, Typeflag: tar.TypeReg, Mode: 0644, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	for archive.Len()%10240 != 0 {
		archive.WriteByte(0)
	}
	raw, _ := json.Marshal(plan)
	plan, digest, err := parseHostPackagePlan(append(raw, '\n'))
	if err != nil {
		t.Fatal(err)
	}
	expected, wire := hostPackageTestInventory(t, plan, digest, archive.Bytes())
	raw, _ = json.Marshal(wire)
	inventory, err := parseHostPackageInventory(raw, expected, plan, digest)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	check := func(context.Context) error { return nil }
	set, err := StageHostPackages(ctx, t.TempDir(), expected, inventory, func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(archive.Bytes())), nil
	}, check)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	var output bytes.Buffer
	writes, changedOffset := 0, int64(0)
	err = set.WriteArchive(ctx, hostPackageTestWriter(func(p []byte) (int, error) {
		writes++
		n, err := output.Write(p)
		if err != nil {
			return n, err
		}
		if writes == 1 {
			changedOffset = int64(n) + 1
			if changedOffset >= int64(archive.Len()) {
				return n, errors.New("fixture missed multi-chunk transfer")
			}
			_, err = set.file.WriteAt([]byte{archive.Bytes()[changedOffset] ^ 1}, changedOffset)
		} else if writes == 2 {
			_, err = set.file.WriteAt(archive.Bytes()[changedOffset:changedOffset+1], changedOffset)
		}
		return n, err
	}), check)
	if writes < 2 || bytes.Equal(output.Bytes(), archive.Bytes()) || inventory.VerifyArchive(ctx, set.file, int64(archive.Len())) != nil {
		t.Fatal("fixture failed to emit changed bytes and restore valid owned archive")
	}
	if err == nil || set.WriteArchive(ctx, io.Discard, check) == nil {
		t.Fatal("transient stream corruption accepted or did not latch failure")
	}
}

func TestHostPackageStageJoinsActiveWriter(t *testing.T) {
	expected, inventory, archive := hostPackageStageFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	check := func(ctx context.Context) error { return ctx.Err() }
	set, err := StageHostPackages(ctx, t.TempDir(), expected, inventory, func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(archive)), nil }, check)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	written, closed := make(chan error, 1), make(chan error, 1)
	go func() {
		written <- set.WriteArchive(ctx, hostPackageTestWriter(func(p []byte) (int, error) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-release
			return len(p), nil
		}), check)
	}()
	<-started
	go func() { closed <- set.Close() }()
	select {
	case <-closed:
		t.Fatal("cleanup escaped active writer")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	close(release)
	if <-written == nil || <-closed != nil {
		t.Fatal("cancelled writer or joined cleanup result wrong")
	}
}
