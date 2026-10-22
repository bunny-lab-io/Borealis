package engineidentity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireStartupBlocked(t *testing.T, root string) {
	t.Helper()
	consumer, err := AcquireConsumer(root)
	if err == nil {
		consumer.Close()
		t.Fatal("identity consumer started through recovery fence")
	}
}

func requireStartupAllowed(t *testing.T, root string) {
	t.Helper()
	consumer, err := AcquireConsumer(root)
	if err != nil {
		t.Fatal(err)
	}
	consumer.Close()
}

func TestIdentityFenceSerializesConsumersAndInstall(t *testing.T) {
	install, _, after := installationFixture(t, false)
	before := fileInventory(t, install.Root)
	first, err := AcquireConsumer(install.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := AcquireConsumer(install.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := install.InstallFenced(after); err == nil {
		t.Fatal("live consumers did not block installation")
	}
	first.Close()
	if _, err := install.InstallFenced(after); err == nil {
		t.Fatal("remaining consumer did not block installation")
	}
	requireInventory(t, install.Root, before)
	if _, err := os.Stat(filepath.Join(install.Root, recoveryMarker)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("busy lock wrote marker")
	}
	second.Close()
	checks := 0
	install.Check = func() error {
		if _, err := os.Stat(filepath.Join(install.Root, recoveryMarker)); err == nil {
			checks++
			requireStartupBlocked(t, install.Root)
		}
		return nil
	}
	if state, err := install.InstallFenced(after); err != nil || state != "committed" {
		t.Fatalf("%s %v", state, err)
	}
	if checks < 5 {
		t.Fatal("missing in-transaction startup checks")
	}
	requireStartupAllowed(t, install.Root)
	if state, err := install.InstallFenced(after); err != nil || state != "already_committed" {
		t.Fatalf("%s %v", state, err)
	}
	requireStartupAllowed(t, install.Root)
}

func TestIdentityFenceLostAuthorityRetainsMarkerAfterRollback(t *testing.T) {
	install, _, after := installationFixture(t, false)
	before := fileInventory(t, install.Root)
	install.Check = func() error {
		raw, _ := os.ReadFile(filepath.Join(install.Root, EngineSecretPath))
		if bytes.Equal(raw, after.Files()[EngineSecretPath]) {
			return errors.New("original controller lost")
		}
		return nil
	}
	if _, err := install.InstallFenced(after); err == nil {
		t.Fatal("lost owner accepted")
	}
	requireInventory(t, install.Root, before)
	requireStartupBlocked(t, install.Root)
	install.Check = func() error { return nil }
	if _, err := install.InstallFenced(after); err == nil {
		t.Fatal("rolled-back attempt reused")
	}
	requireStartupBlocked(t, install.Root)
}

func TestIdentityFenceLostQuiescenceDoesNotRollbackThroughFence(t *testing.T) {
	install, _, after := installationFixture(t, false)
	before := fileInventory(t, install.Root)
	lost := false
	install.Check = func() error {
		raw, _ := os.ReadFile(filepath.Join(install.Root, EngineSecretPath))
		if bytes.Equal(raw, after.Files()[EngineSecretPath]) {
			lost = true
			return errors.New("target changed")
		}
		return nil
	}
	install.Quiesced = func() error {
		if lost {
			return errors.New("target changed")
		}
		return nil
	}
	if _, err := install.InstallFenced(after); err == nil {
		t.Fatal("lost target accepted")
	}
	partial := fileInventory(t, install.Root)
	if partial[EngineSecretPath].Digest == before[EngineSecretPath].Digest {
		t.Fatal("rollback wrote through lost fence")
	}
	requireStartupBlocked(t, install.Root)
	install.Check = func() error { return nil }
	install.Quiesced = func() error { return nil }
	if state, err := install.InstallFenced(after); err != nil || state != "rolled_back" {
		t.Fatalf("%s %v", state, err)
	}
	requireInventory(t, install.Root, before)
	requireStartupBlocked(t, install.Root)
}

func TestIdentityFenceRejectsChangedAttempt(t *testing.T) {
	for _, field := range []string{"source", "target", "ssh", "journal", "material", "fresh"} {
		t.Run(field, func(t *testing.T) {
			install, _, after := installationFixture(t, false)
			install.Check = func() error {
				if _, err := os.Stat(filepath.Join(install.Root, recoveryMarker)); err == nil {
					return errors.New("lost controller")
				}
				return nil
			}
			if _, err := install.InstallFenced(after); err == nil {
				t.Fatal("expected retained marker")
			}
			before := fileInventory(t, install.Root)
			install.Check = func() error { return nil }
			switch field {
			case "source":
				install.Binding.SourceUID = "60000000-0000-4000-8000-000000000006"
			case "target":
				install.Target.NodeUID = "60000000-0000-4000-8000-000000000006"
			case "ssh":
				install.Target.SSHFingerprint = "SHA256:" + strings.Repeat("A", 43)
			case "journal":
				install.Journal += "-other"
			case "material":
				after, _ = fixture(t)
			case "fresh":
				install.Target.NodeUID = ""
			}
			if _, err := install.InstallFenced(after); err == nil {
				t.Fatal("marker rebound")
			}
			requireInventory(t, install.Root, before)
			requireStartupBlocked(t, install.Root)
		})
	}
}

func TestIdentityFenceUnsafeMarkerAndDirectory(t *testing.T) {
	for _, kind := range []string{"empty", "garbage", "symlink", "directory", "hardlink", "mode", "root-symlink", "root-writable"} {
		t.Run(kind, func(t *testing.T) {
			install, _, after := installationFixture(t, false)
			before := fileInventory(t, install.Root)
			marker := filepath.Join(install.Root, recoveryMarker)
			var err error
			switch kind {
			case "empty":
				err = os.WriteFile(marker, nil, 0o600)
			case "garbage":
				err = os.WriteFile(marker, []byte("invalid"), 0o600)
			case "symlink":
				err = os.Symlink("missing", marker)
			case "directory":
				err = os.Mkdir(marker, 0o700)
			case "hardlink":
				err = os.Link(filepath.Join(install.Root, EngineSecretPath), marker)
			case "mode":
				err = os.WriteFile(marker, nil, 0o644)
			case "root-symlink":
				alias := install.Root + "-alias"
				err = os.Symlink(install.Root, alias)
				install.Root = alias
			case "root-writable":
				err = os.Chmod(install.Root, 0o777)
			}
			if err != nil {
				t.Fatal(err)
			}
			requireStartupBlocked(t, install.Root)
			if _, err := install.InstallFenced(after); err == nil {
				t.Fatal("unsafe fence accepted")
			}
			if kind == "root-symlink" {
				install.Root = strings.TrimSuffix(install.Root, "-alias")
			}
			if kind == "hardlink" {
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
			}
			requireInventory(t, install.Root, before)
		})
	}
}

func TestIdentityFenceDirectoryReplacementStopsRollback(t *testing.T) {
	install, _, after := installationFixture(t, false)
	old := install.Root + "-old"
	changed := false
	install.Check = func() error {
		raw, _ := os.ReadFile(filepath.Join(install.Root, EngineSecretPath))
		if !changed && bytes.Equal(raw, after.Files()[EngineSecretPath]) {
			changed = true
			if err := os.Rename(install.Root, old); err != nil {
				return err
			}
			return os.Mkdir(install.Root, 0o700)
		}
		return nil
	}
	if _, err := install.InstallFenced(after); err == nil || !changed {
		t.Fatal("directory replacement accepted")
	}
	raw, err := os.ReadFile(filepath.Join(old, EngineSecretPath))
	if err != nil || !bytes.Equal(raw, after.Files()[EngineSecretPath]) {
		t.Fatal("rollback wrote through replaced directory")
	}
	entries, err := os.ReadDir(install.Root)
	if err != nil || len(entries) != 0 {
		t.Fatal("replacement directory modified")
	}
	requireStartupBlocked(t, old)
}

// Real process death, not a callback pretending that the kernel released flock.
func TestIdentityFenceExecutorCrash(t *testing.T) {
	for _, phase := range []string{"write", "commit"} {
		t.Run(phase, func(t *testing.T) {
			install, _, after := installationFixture(t, false)
			before := fileInventory(t, install.Root)
			incoming := t.TempDir()
			for name, raw := range after.Files() {
				path := filepath.Join(incoming, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			record, err := json.Marshal(recoveryFenceRecord{1, install.Binding, install.Target, install.Journal, after.Digest()})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIdentityFenceProcessHelper$")
			cmd.Env = append(os.Environ(), "BOREALIS_FENCE_TEST_ROOT="+install.Root, "BOREALIS_FENCE_TEST_INCOMING="+incoming,
				"BOREALIS_FENCE_TEST_RECORD="+string(record), "BOREALIS_FENCE_TEST_PHASE="+phase)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			line, err := bufio.NewReader(stdout).ReadString('\n')
			if err != nil || line != "fenced\n" {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("helper failed: %v %s", err, stderr.String())
			}
			requireStartupBlocked(t, install.Root)
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("helper survived kill")
			}
			requireStartupBlocked(t, install.Root)
			state, err := install.InstallFenced(after)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "write" {
				if state != "rolled_back" {
					t.Fatal(state)
				}
				requireInventory(t, install.Root, before)
				requireStartupBlocked(t, install.Root)
			} else {
				if state != "already_committed" {
					t.Fatal(state)
				}
				requireStartupAllowed(t, install.Root)
			}
		})
	}
}

func TestIdentityFenceProcessHelper(t *testing.T) {
	root := os.Getenv("BOREALIS_FENCE_TEST_ROOT")
	if root == "" {
		return
	}
	var record recoveryFenceRecord
	if err := json.Unmarshal([]byte(os.Getenv("BOREALIS_FENCE_TEST_RECORD")), &record); err != nil {
		t.Fatal(err)
	}
	material, err := Load(os.Getenv("BOREALIS_FENCE_TEST_INCOMING"))
	if err != nil {
		t.Fatal(err)
	}
	check := func() error {
		ready := false
		if os.Getenv("BOREALIS_FENCE_TEST_PHASE") == "write" {
			raw, _ := os.ReadFile(filepath.Join(root, EngineSecretPath))
			ready = bytes.Equal(raw, material.Files()[EngineSecretPath])
		} else {
			raw, _ := os.ReadFile(filepath.Join(record.Journal, "journal.json"))
			var journal installationJournal
			_ = json.Unmarshal(raw, &journal)
			ready = journal.State == "committed"
		}
		if ready {
			fmt.Println("fenced")
			time.Sleep(time.Hour)
		}
		return nil
	}
	install := Installation{Root: root, Journal: record.Journal, Binding: record.Binding, Target: record.Target,
		UID: os.Geteuid(), GID: os.Getegid(), Check: check, Quiesced: func() error { return nil }}
	if _, err := install.InstallFenced(material); err != nil {
		t.Fatal(err)
	}
	t.Fatal("helper did not reach crash boundary")
}

func TestIdentityFencePythonWorkerInteroperability(t *testing.T) {
	install, _, after := installationFixture(t, false)
	module, err := filepath.Abs("../../../site-worker/data/identity_consumer.py")
	if err != nil {
		t.Fatal(err)
	}
	// Import actual shipped Python guard, without optional worker dependencies.
	script := `import importlib.util, os, sys
spec = importlib.util.spec_from_file_location("identity_consumer", sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
fd = module.acquire_identity_consumer(sys.argv[2])
print("fenced", flush=True)
sys.stdin.read()
`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script, module, install.Root)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
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
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "fenced\n" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("Python guard failed: %v %s", err, stderr.String())
	}
	requireStartupAllowed(t, install.Root) // Shared Python/Go readers coexist.
	if _, err := install.InstallFenced(after); err == nil {
		t.Fatal("Python consumer did not block Go recovery")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	for _, kind := range []string{"exclusive", "marker"} {
		fence, err := openIdentityFence(install.Root, true)
		if err != nil {
			t.Fatal(err)
		}
		if kind == "marker" {
			if err := os.WriteFile(filepath.Join(install.Root, recoveryMarker), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			fence.Close()
		}
		probe := exec.CommandContext(ctx, "python3", "-c", script, module, install.Root)
		probe.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
		output, err := probe.CombinedOutput()
		fence.Close()
		if err == nil || bytes.Contains(output, []byte("fenced\n")) {
			t.Fatalf("Python crossed Go %s fence", kind)
		}
	}
}

func TestIdentityFenceOuterJournalFailureNeverReleasesConsumers(t *testing.T) {
	for _, mode := range []string{"failed_commit", "missing_transaction", "duplicate_transaction", "swallowed_transaction_error"} {
		t.Run(mode, func(t *testing.T) {
			install, _, material := installationFixture(t, false)
			if mode == "swallowed_transaction_error" {
				install.Check = func() error {
					raw, _ := os.ReadFile(filepath.Join(install.Root, EngineSecretPath))
					if bytes.Equal(raw, material.Files()[EngineSecretPath]) {
						return errors.New("lost owner")
					}
					return nil
				}
			}
			_, err := install.InstallFencedJournaled(material, func(transaction func() (string, error)) error {
				requireStartupBlocked(t, install.Root)
				if mode == "missing_transaction" {
					return nil
				}
				state, err := transaction()
				if mode == "swallowed_transaction_error" {
					if err == nil {
						t.Fatal("transaction fault not reached")
					}
					return nil
				}
				if err != nil || state != "committed" {
					t.Fatalf("%s %v", state, err)
				}
				requireStartupBlocked(t, install.Root)
				if mode == "duplicate_transaction" {
					_, _ = transaction()
					return nil
				}
				return errors.New("outer journal persistence failed")
			})
			if err == nil {
				t.Fatal("invalid outer journal accepted")
			}
			requireStartupBlocked(t, install.Root)
		})
	}
}
