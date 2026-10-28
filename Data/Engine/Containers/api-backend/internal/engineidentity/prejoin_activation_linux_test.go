package engineidentity

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrejoinActivationExclusionAndFailureRestoration(t *testing.T) {
	for _, mode := range []string{"complete", "journal failure", "panic", "missing release", "repeated release", "post-unlink authority", "changed marker", "borrowed lock closed", "cancelled", "replaced directory"} {
		t.Run(mode, func(t *testing.T) {
			install, _, material := installationFixture(t, true)
			if _, err := install.InstallPrejoinFencedJournaledRetained(material, prejoinCommit); err != nil {
				t.Fatal(err)
			}
			journalBefore, _ := os.ReadFile(filepath.Join(install.Journal, "journal.json"))
			marker := filepath.Join(install.Root, recoveryMarker)
			if mode == "post-unlink authority" {
				install.Check = func() error {
					if _, err := os.Lstat(marker); errors.Is(err, os.ErrNotExist) {
						return errors.New("authority lost after unlink")
					}
					return nil
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				install.Check = ctx.Err
			}
			panicked := false
			var got error
			func() {
				defer func() { panicked = recover() != nil }()
				got = install.WithPrejoinActivationFencedCommitted(material, false, func(lock *os.File, present bool, release func() error) error {
					if lock == nil || !present {
						t.Fatal("missing retained exclusion/marker")
					}
					requireStartupBlocked(t, install.Root)
					if mode == "missing release" {
						return nil
					}
					if err := release(); err != nil {
						return err
					}
					requireStartupBlocked(t, install.Root)
					if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("release retained marker", err)
					}
					switch mode {
					case "journal failure":
						return errors.New("outer persistence failed")
					case "panic":
						panic("outer persistence panic")
					case "repeated release":
						_ = release() // Swallowed duplicate still fails closed.
					case "cancelled":
						cancel()
					case "replaced directory":
						if err := os.Rename(install.Root, install.Root+"-original"); err != nil {
							t.Fatal(err)
						}
						return os.Mkdir(install.Root, 0700)
					case "borrowed lock closed":
						return lock.Close()
					case "changed marker":
						return os.WriteFile(marker, []byte("foreign"), 0600)
					}
					return nil
				})
			}()
			journalAfter, _ := os.ReadFile(filepath.Join(install.Journal, "journal.json"))
			if !bytes.Equal(journalBefore, journalAfter) {
				t.Fatal("activation changed empty-NodeUID identity journal")
			}
			if mode == "complete" {
				if got != nil || panicked {
					t.Fatal("activation failed", got)
				}
				consumer, err := AcquireConsumer(install.Root)
				if err != nil {
					t.Fatal(err)
				}
				consumer.Close()
				return
			}
			if got == nil && !panicked {
				t.Fatal("uncertain activation succeeded")
			}
			if mode == "replaced directory" {
				entries, err := os.ReadDir(install.Root)
				if err != nil || len(entries) != 0 {
					t.Fatal("cleanup wrote through replaced directory", err)
				}
				return
			}
			requireStartupBlocked(t, install.Root)
			if mode == "changed marker" {
				value, _ := os.ReadFile(marker)
				if string(value) != "foreign" {
					t.Fatal("foreign marker overwritten")
				}
			}
		})
	}
}

func TestPrejoinActivationReconciliationNeverRemovesRetainedMarker(t *testing.T) {
	install, _, material := installationFixture(t, true)
	if _, err := install.InstallPrejoinFencedJournaledRetained(material, prejoinCommit); err != nil {
		t.Fatal(err)
	}
	called := false
	work := func(_ *os.File, present bool, release func() error) error {
		called = true
		if present {
			t.Fatal("reconciliation admitted retained marker")
		}
		return release()
	}
	if err := install.WithPrejoinActivationFencedCommitted(material, true, work); err == nil || called {
		t.Fatal("reconciliation replayed marker removal")
	}
	if _, err := install.ReleasePrejoinFencedCommitted(material); err != nil {
		t.Fatal(err)
	}
	if err := install.WithPrejoinActivationFencedCommitted(material, false, work); err == nil || called {
		t.Fatal("fresh activation accepted absent marker")
	}
	if err := install.WithPrejoinActivationFencedCommitted(material, true, work); err != nil || !called {
		t.Fatal("absent marker could not be proved", err)
	}
}
