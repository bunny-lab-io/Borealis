package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func hostPackageExtractionFixture(t *testing.T) (*HostPackageSet, []byte) {
	t.Helper()
	plan, digest := hostPackageTestPlan(t)
	archive := hostPackageTestArchive(t, plan, "valid")
	expected, wire := hostPackageTestInventory(t, plan, digest, archive)
	raw, _ := json.Marshal(wire)
	inventory, err := parseHostPackageInventory(raw, expected, plan, digest)
	if err != nil {
		t.Fatal(err)
	}
	set, err := StageHostPackages(context.Background(), t.TempDir(), expected, inventory, func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(archive)), nil
	}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close() })
	return set, archive
}

func TestHostPackageExtractionScopedFilesAndFailures(t *testing.T) {
	for _, mode := range []string{"success", "callback error", "cancel callback", "authority loss", "authority loss during cleanup", "cancel allocation", "malformed source", "source changed in callback", "package changed", "package replaced", "unknown child", "child replaced", "parent replaced"} {
		t.Run(mode, func(t *testing.T) {
			set, original := hostPackageExtractionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var lost atomic.Bool
			called, path := 0, ""
			check := func(ctx context.Context) error {
				if lost.Load() {
					return ErrSessionAuthority
				}
				if mode == "authority loss during cleanup" && path != "" {
					if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
						return ErrSessionAuthority
					}
				}
				if mode == "cancel allocation" {
					root, err := set.root.Open(".")
					if err != nil {
						return err
					}
					names, err := root.Readdirnames(-1)
					_ = root.Close()
					if err != nil {
						return err
					}
					for _, name := range names {
						if strings.HasPrefix(name, "archives-") {
							cancel()
						}
					}
				}
				return ctx.Err()
			}
			if mode == "malformed source" {
				if _, err := set.file.WriteAt([]byte("!"), 0); err != nil {
					t.Fatal(err)
				}
			}
			callbackFailure := errors.New("consumer failed")
			err := set.WithArchives(ctx, check, func(_ context.Context, directory string, pins []HostPackagePin) error {
				called++
				path = directory
				if !filepath.IsAbs(path) || filepath.Dir(path) != set.path || !strings.HasPrefix(filepath.Base(path), "archives-") {
					t.Fatal("unowned extraction directory", path)
				}
				info, err := os.Lstat(path)
				if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
					t.Fatal("private directory unavailable", err)
				}
				entries, err := os.ReadDir(path)
				if err != nil || len(entries) != len(pins) {
					t.Fatal("partial package set reached consumer", err)
				}
				for _, pin := range pins {
					raw, err := os.ReadFile(filepath.Join(path, pin.File))
					digest := sha256.Sum256(raw)
					info, statErr := os.Lstat(filepath.Join(path, pin.File))
					if err != nil || statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || int64(len(raw)) != pin.Size || hex.EncodeToString(digest[:]) != pin.SHA256 {
						t.Fatal("unverified archive reached consumer", pin.Name, err)
					}
				}
				first := filepath.Join(path, pins[0].File)
				switch mode {
				case "callback error":
					return callbackFailure
				case "cancel callback":
					cancel()
				case "authority loss":
					lost.Store(true)
				case "source changed in callback":
					if _, err := set.file.WriteAt([]byte("!"), 0); err != nil {
						t.Fatal(err)
					}
				case "package changed":
					file, err := os.OpenFile(first, os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					_, writeErr := file.WriteAt([]byte("!"), 0)
					if errors.Join(writeErr, file.Close()) != nil {
						t.Fatal("cannot inject content drift")
					}
				case "package replaced":
					if os.Rename(first, first+".retained") != nil || os.WriteFile(first, []byte("foreign"), 0600) != nil {
						t.Fatal("replacement injection failed")
					}
				case "unknown child":
					if os.WriteFile(filepath.Join(path, "foreign"), []byte("retained"), 0600) != nil {
						t.Fatal("unknown child injection failed")
					}
				case "child replaced":
					if os.Rename(path, path+"-retained") != nil || os.Mkdir(path, 0700) != nil || os.WriteFile(filepath.Join(path, "foreign"), []byte("retained"), 0600) != nil {
						t.Fatal("child replacement injection failed")
					}
				case "parent replaced":
					if os.Rename(set.path, set.path+"-retained") != nil || os.Mkdir(set.path, 0700) != nil || os.WriteFile(filepath.Join(set.path, "foreign"), []byte("retained"), 0600) != nil {
						t.Fatal("parent replacement injection failed")
					}
				}
				pins[0].File = "caller-changed.deb"
				return nil
			})
			if mode == "success" {
				if err != nil || called != 1 {
					t.Fatal("complete scope rejected", err, called)
				}
				var output bytes.Buffer
				if set.WriteArchive(context.Background(), &output, func(context.Context) error { return nil }) != nil || !bytes.Equal(output.Bytes(), original) {
					t.Fatal("consumer modified retained source")
				}
			} else {
				if err == nil {
					t.Fatal("failed extraction or cleanup accepted")
				}
				if mode == "callback error" && !errors.Is(err, callbackFailure) {
					t.Fatal("callback failure lost", err)
				}
				if (mode == "malformed source" || mode == "cancel allocation") && called != 0 {
					t.Fatal("partial/invalid bytes reached consumer")
				}
				if err := set.WithArchives(context.Background(), func(context.Context) error { return nil }, func(context.Context, string, []HostPackagePin) error { t.Fatal("failed owner revived"); return nil }); err == nil {
					t.Fatal("failed owner revived")
				}
			}
			switch mode {
			case "package replaced":
				pin := set.inventory.Packages()[0]
				if got, err := os.ReadFile(filepath.Join(path, pin.File)); err != nil || string(got) != "foreign" {
					t.Fatal("foreign replacement removed", err)
				}
				if _, err := os.Stat(filepath.Join(path, pin.File+".retained")); err != nil {
					t.Fatal("renamed package removed", err)
				}
			case "unknown child", "child replaced":
				if got, err := os.ReadFile(filepath.Join(path, "foreign")); err != nil || string(got) != "retained" {
					t.Fatal("foreign child removed", err)
				}
			case "parent replaced":
				if got, err := os.ReadFile(filepath.Join(set.path, "foreign")); err != nil || string(got) != "retained" {
					t.Fatal("foreign parent removed", err)
				}
			default:
				if path != "" {
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("owned extraction retained", err)
					}
				}
			}
		})
	}
	if directory := os.Getenv("BOREALIS_TEST_HOST_PACKAGE_ASSETS"); directory != "" {
		raw, err := os.ReadFile(filepath.Join(directory, HostPackageInventoryName))
		if err != nil {
			t.Fatal(err)
		}
		var wire hostPackageInventoryWire
		if json.Unmarshal(raw, &wire) != nil {
			t.Fatal("actual producer identity unavailable")
		}
		expected := Expected{Repository: wire.Repository, Release: wire.Release, SourceSHA: wire.SourceSHA, AllowQualification: true}
		inventory, err := ParseHostPackageInventory(raw, expected)
		if err != nil {
			t.Fatal(err)
		}
		check := func(ctx context.Context) error { return ctx.Err() }
		set, err := StageHostPackages(context.Background(), t.TempDir(), expected, inventory, func(context.Context) (io.ReadCloser, error) {
			return os.Open(filepath.Join(directory, HostPackageArchiveName))
		}, check)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := set.Close(); err != nil {
				t.Error(err)
			}
		}()
		if err := set.WithArchives(context.Background(), check, func(_ context.Context, path string, pins []HostPackagePin) error {
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) != len(inventory.Packages()) || len(entries) != len(pins) {
				t.Fatal("actual complete package set unavailable", err)
			}
			return nil
		}); err != nil {
			t.Fatal("actual pinned package extraction failed", err)
		}
	}
}

func TestHostPackageExtractionJoinsConsumerAndClose(t *testing.T) {
	set, _ := hostPackageExtractionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release, done := make(chan string, 1), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- set.WithArchives(ctx, func(ctx context.Context) error { return ctx.Err() }, func(_ context.Context, path string, _ []HostPackagePin) error {
			entered <- path
			<-release
			return nil
		})
	}()
	path := <-entered
	closeStarted, closed := make(chan struct{}), make(chan error, 1)
	go func() { close(closeStarted); closed <- set.Close() }()
	<-closeStarted
	cancel()
	select {
	case err := <-closed:
		t.Fatal("Close escaped active consumer", err)
	case err := <-done:
		t.Fatal("extraction escaped active consumer", err)
	case <-time.After(20 * time.Millisecond):
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("active consumer lost owned files", err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("cancelled consumer accepted")
	}
	if err := <-closed; err != nil {
		t.Fatal("Close failed after joined cleanup", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("joined consumer retained path", err)
	}
}
