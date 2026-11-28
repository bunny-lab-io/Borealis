package engineidentity

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrejoinFencedScopeRejectsInvalidEntry(t *testing.T) {
	for _, mode := range []string{"nil work", "missing marker", "foreign marker", "missing journal", "changed source", "changed target", "joined", "wrong material", "authority", "quiescence", "active executor"} {
		t.Run(mode, func(t *testing.T) {
			install, other, material := installationFixture(t, true)
			if _, err := install.InstallPrejoinFencedJournaledRetained(material, prejoinCommit); err != nil {
				t.Fatal(err)
			}
			called := false
			work := func(*os.File) error { called = true; return nil }
			switch mode {
			case "nil work":
				work = nil
			case "missing marker":
				if err := os.Remove(filepath.Join(install.Root, recoveryMarker)); err != nil {
					t.Fatal(err)
				}
			case "foreign marker":
				if err := os.WriteFile(filepath.Join(install.Root, recoveryMarker), []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing journal":
				install.Journal += "-missing"
			case "changed source":
				install.Binding.SourceID = "50000000-0000-4000-8000-000000000005"
			case "changed target":
				install.Target.ProvisioningID = "60000000-0000-4000-8000-000000000006"
			case "joined":
				install.Target.NodeUID = "40000000-0000-4000-8000-000000000004"
			case "wrong material":
				material = other
			case "authority":
				install.Check = func() error { return errors.New("authority lost") }
			case "quiescence":
				install.Quiesced = func() error { return errors.New("consumer present") }
			case "active executor":
				fence, err := openIdentityFence(install.Root, true)
				if err != nil {
					t.Fatal(err)
				}
				defer fence.Close()
			}
			if err := install.WithPrejoinFencedCommitted(material, work); err == nil || called {
				t.Fatal("invalid entry reached host work", err, called)
			}
		})
	}
}

func TestPrejoinFencedScopeRetainsExactIdentity(t *testing.T) {
	for _, mode := range []string{"success", "work failure", "authority", "quiescence", "keys", "journal", "marker removed", "foreign marker", "directory replaced", "panic"} {
		t.Run(mode, func(t *testing.T) {
			install, other, material := installationFixture(t, true)
			if _, err := install.InstallPrejoinFencedJournaledRetained(material, prejoinCommit); err != nil {
				t.Fatal(err)
			}
			markerPath := filepath.Join(install.Root, recoveryMarker)
			marker, err := os.ReadFile(markerPath)
			if err != nil {
				t.Fatal(err)
			}
			journalPath := filepath.Join(install.Journal, "journal.json")
			journal, err := os.ReadFile(journalPath)
			if err != nil {
				t.Fatal(err)
			}
			valid := true
			check := func() error {
				if !valid {
					return errors.New("private authority detail")
				}
				return nil
			}
			if mode == "authority" {
				install.Check = check
			} else if mode == "quiescence" {
				install.Quiesced = check
			}
			var borrowed *os.File
			panicked := false
			func() {
				defer func() {
					if value := recover(); value != nil {
						if value != "test panic" {
							panic(value)
						}
						panicked = true
					}
				}()
				err = install.WithPrejoinFencedCommitted(material, func(lock *os.File) error {
					borrowed = lock
					held, statErr := lock.Stat()
					current, rootErr := os.Stat(install.Root)
					if statErr != nil || rootErr != nil || !os.SameFile(held, current) {
						t.Fatal("wrong inherited lock", statErr, rootErr)
					}
					requireStartupBlocked(t, install.Root)
					switch mode {
					case "work failure":
						return errors.New("private command output must not escape")
					case "authority", "quiescence":
						valid = false
					case "keys":
						return os.WriteFile(filepath.Join(install.Root, EngineSecretPath), other.Files()[EngineSecretPath], 0600)
					case "journal":
						return os.WriteFile(journalPath, []byte("{}"), 0600)
					case "marker removed", "panic":
						if err := os.Remove(markerPath); err != nil {
							t.Fatal(err)
						}
						// Lock alone must exclude consumers while marker is absent.
						requireStartupBlocked(t, install.Root)
						if mode == "panic" {
							panic("test panic")
						}
					case "foreign marker":
						return os.WriteFile(markerPath, []byte("foreign"), 0600)
					case "directory replaced":
						if err := os.Rename(install.Root, install.Root+"-old"); err != nil {
							t.Fatal(err)
						}
						return os.Mkdir(install.Root, 0700)
					}
					return nil
				})
			}()
			if borrowed == nil {
				t.Fatal("callback never reached")
			}
			if _, err := borrowed.Stat(); err == nil {
				t.Fatal("borrowed descriptor escaped scope")
			}
			if mode == "panic" {
				if !panicked {
					t.Fatal("panic swallowed")
				}
			} else if (err == nil) != (mode == "success") || err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("incorrect scoped outcome", err)
			}
			if mode == "directory replaced" {
				requireStartupBlocked(t, install.Root+"-old")
				entries, err := os.ReadDir(install.Root)
				if err != nil || len(entries) != 0 {
					t.Fatal("wrote through replacement directory", err)
				}
				return
			}
			want := marker
			if mode == "foreign marker" {
				want = []byte("foreign")
			}
			if got, err := os.ReadFile(markerPath); err != nil || !bytes.Equal(got, want) {
				t.Fatal("exact retained marker changed", err)
			}
			requireStartupBlocked(t, install.Root)
			if mode != "journal" {
				if got, err := os.ReadFile(journalPath); err != nil || !bytes.Equal(got, journal) {
					t.Fatal("scope modified private journal", err)
				}
			}
			// No leaked exclusive descriptor after synchronous scope teardown.
			fence, err := openIdentityFence(install.Root, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := fence.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrejoinFencedScopeInheritedChild(t *testing.T) {
	install, _, material := installationFixture(t, true)
	if _, err := install.InstallPrejoinFencedJournaledRetained(material, prejoinCommit); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPrejoinFencedScopeChildHelper$")
	cmd.Env = append(os.Environ(), "BOREALIS_PREJOIN_FENCE_CHILD=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	started := false
	joined := false
	defer func() {
		if started && !joined {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	err = install.WithPrejoinFencedCommitted(material, func(lock *os.File) error {
		cmd.ExtraFiles = []*os.File{lock}
		if err := cmd.Start(); err != nil {
			return err
		}
		started = true
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil || line != "locked\n" {
			return fmt.Errorf("helper failed: %v %s", err, stderr.String())
		}
		// Deliberately violate normal join contract to model loss of both
		// parent's descriptors while child is still alive.
		return errors.New("parent lost")
	})
	if err == nil || !started {
		t.Fatal("inherited scope fixture failed", err)
	}
	if fence, err := openIdentityFence(install.Root, false); err == nil {
		fence.Close()
		t.Fatal("child failed to retain exclusive kernel lock")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("child survived kill")
	}
	joined = true
	if err := install.ProvePrejoinFencedCommitted(material); err != nil {
		t.Fatal("child termination did not release lock", err)
	}
	requireStartupBlocked(t, install.Root)
}

func TestPrejoinFencedScopeChildHelper(t *testing.T) {
	if os.Getenv("BOREALIS_PREJOIN_FENCE_CHILD") != "1" {
		return
	}
	lock := os.NewFile(3, "inherited-prejoin-lock")
	if info, err := lock.Stat(); err != nil || !info.IsDir() {
		t.Fatal("inherited lock unavailable", err)
	}
	defer lock.Close()
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
}
