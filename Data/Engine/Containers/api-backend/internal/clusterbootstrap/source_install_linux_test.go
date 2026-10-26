package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func sourcePublicationFixture(t *testing.T) (*PreparationInputs, *MutationJournal, string, string) {
	t.Helper()
	source, manifest, members := archiveFixture(t)
	bundle, err := stageFixture(t, context.Background(), source, manifest, pack(t, members), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bundle.Close() })
	expected, runtime := preparationFixture()
	expected.Source = source
	config, err := NewPreparationConfiguration(expected, runtime)
	if err != nil {
		t.Fatal(err)
	}
	p, err := BindPreparationInputs(context.Background(), bundle, config, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	journalPath, destination := mutationTempDir(t), mutationTempDir(t)
	j, err := openTestMutationJournal(context.Background(), journalPath, expected.Target, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return p, j, journalPath, destination
}

func reopenSourcePublication(t *testing.T, p *PreparationInputs, j *MutationJournal, path string) *MutationJournal {
	t.Helper()
	e := p.configuration.wire.Expected
	e.Target = nextMutationOwner(e.Target)
	config, err := NewPreparationConfiguration(e, p.configuration.wire.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	p.configuration = config
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = openTestMutationJournal(context.Background(), path, e.Target, e.Source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j
}

func TestSourcePublicationAtomicAndReconciled(t *testing.T) {
	p, j, journalPath, destination := sourcePublicationFixture(t)
	ctx := context.Background()
	result, err := p.publishSource(ctx, j, destination)
	if err != nil || !digestPattern.MatchString(result) {
		t.Fatal(result, err)
	}
	state := readMutationFixture(t, journalPath)
	if len(state.Steps) != 1 || state.Steps["stage_source"].State != "applied" {
		t.Fatal("wrong phase")
	}
	if _, err := os.Stat(filepath.Join(destination, "Borealis", "Engine", "Services")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("consumer runtime unexpectedly created")
	}
	environment, err := p.configuration.Environment()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"compose.env", "runtime.env"} {
		path := filepath.Join(destination, "Borealis", "Engine", "Deploy", name)
		raw, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, environment) {
			t.Fatal("inherited configuration changed", err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("configuration not private", err)
		}
	}
	if _, err := p.publishSource(ctx, j, destination); err == nil {
		t.Fatal("publication replayed")
	}
	if _, err := p.reconcileSource(ctx, j, destination); err == nil {
		t.Fatal("producer proved own quiescence")
	}
	j = reopenSourcePublication(t, p, j, journalPath)
	for i := 0; i < 2; i++ {
		if got, err := p.reconcileSource(ctx, j, destination); err != nil || got != result {
			t.Fatal(got, err)
		}
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 1 || entries[0].Name() != "Borealis" {
		t.Fatal("scratch leaked", err)
	}
}

func TestSourcePublicationPrivateReceiverUmask(t *testing.T) {
	// umask is process-global: isolate the production receiver's setting so
	// unrelated concurrent package fixtures cannot inherit it.
	if os.Getenv("BOREALIS_TEST_SOURCE_PRIVATE_UMASK") == "1" {
		syscall.Umask(077)
		TestSourcePublicationAtomicAndReconciled(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestSourcePublicationPrivateReceiverUmask$", "-test.count=1")
	command.Env = append(os.Environ(), "BOREALIS_TEST_SOURCE_PRIVATE_UMASK=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("private receiver fixture failed: %v\n%s", err, output)
	}
}

func TestSourcePublicationRejectsOccupiedAndDriftingInputs(t *testing.T) {
	for _, mode := range []string{"empty directory", "file", "symlink", "changed source", "claim", "authority", "later phase", "unsafe parent"} {
		t.Run(mode, func(t *testing.T) {
			p, j, _, destination := sourcePublicationFixture(t)
			target := filepath.Join(destination, "Borealis")
			switch mode {
			case "empty directory":
				if err := os.Mkdir(target, 0755); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), target); err != nil {
					t.Fatal(err)
				}
			case "changed source":
				if err := os.WriteFile(filepath.Join(p.bundle.SourcePath(), "Engine.sh"), []byte("wrong"), 0755); err != nil {
					t.Fatal(err)
				}
			case "claim":
				j.state.Owner.Generation++
			case "authority":
				j.check = func(context.Context) error { return ErrSessionAuthority }
			case "later phase":
				prejoinAppliedFixture(t, j, "install_identity")
			case "unsafe parent":
				if err := os.Chmod(destination, 0777); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.publishSource(context.Background(), j, destination); err == nil {
				t.Fatal("unsafe publication accepted")
			}
			if _, ok := j.state.Steps["stage_source"]; ok {
				t.Fatal("rejected input consumed intent")
			}
			if mode == "file" {
				raw, _ := os.ReadFile(target)
				if string(raw) != "preserve" {
					t.Fatal("existing file changed")
				}
			}
		})
	}
}

func TestSourcePublicationLostAuthorityPreservesIntentAndProvesEffect(t *testing.T) {
	for _, mode := range []string{"before publish", "after publish", "competing destination"} {
		t.Run(mode, func(t *testing.T) {
			p, j, journalPath, destination := sourcePublicationFixture(t)
			original := j.check
			injected := false
			j.check = func(ctx context.Context) error {
				raw, persisted := os.ReadFile(filepath.Join(journalPath, mutationJournalName))
				var durable mutationState
				if persisted == nil && json.Unmarshal(raw, &durable) == nil && durable.Steps["stage_source"].State == "intent" {
					_, exists := os.Stat(filepath.Join(destination, "Borealis"))
					if mode == "after publish" && exists == nil {
						injected = true
						return ErrSessionAuthority
					}
					if mode == "before publish" {
						injected = true
						return ErrSessionAuthority
					}
					if mode == "competing destination" && !injected {
						injected = true
						if err := os.Mkdir(filepath.Join(destination, "Borealis"), 0755); err != nil {
							t.Fatal(err)
						}
					}
				}
				return original(ctx)
			}
			if _, err := p.publishSource(context.Background(), j, destination); err == nil || !injected {
				t.Fatal("fault not exercised", err)
			}
			state := readMutationFixture(t, journalPath)
			if state.Steps["stage_source"].State != "intent" {
				t.Fatal("uncertain effect lost")
			}
			j = reopenSourcePublication(t, p, j, journalPath)
			_, err := p.reconcileSource(context.Background(), j, destination)
			if (err == nil) != (mode == "after publish") {
				t.Fatal("wrong reconciliation", err)
			}
			if mode != "after publish" && j.state.Steps["stage_source"].State != "intent" {
				t.Fatal("absence enabled replay")
			}
		})
	}
}

func TestSourcePublicationReconciliationRejectsChangedTree(t *testing.T) {
	for _, mode := range []string{"bytes", "extra file", "mode", "hardlink", "symlink", "directory mode", "configuration", "active executor", "environment bytes", "environment mode", "missing environment", "extra runtime"} {
		t.Run(mode, func(t *testing.T) {
			p, j, journalPath, destination := sourcePublicationFixture(t)
			if _, err := p.publishSource(context.Background(), j, destination); err != nil {
				t.Fatal(err)
			}
			j = reopenSourcePublication(t, p, j, journalPath)
			path := filepath.Join(destination, "Borealis", "Engine.sh")
			switch mode {
			case "bytes":
				if err := os.WriteFile(path, []byte("changed"), 0755); err != nil {
					t.Fatal(err)
				}
			case "extra file":
				if err := os.WriteFile(filepath.Join(destination, "Borealis", "extra"), []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(destination, "alias")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/etc/hostname", path); err != nil {
					t.Fatal(err)
				}
			case "directory mode":
				if err := os.Chmod(filepath.Join(destination, "Borealis", ".git"), 0700); err != nil {
					t.Fatal(err)
				}
			case "configuration":
				p.configuration.wire.Runtime["BOREALIS_OPERATOR_SECRET"] = "changed"
			case "active executor":
				j.quiescent = func(context.Context, ExecutorIdentity) error { return ErrExecutorContainment }
			case "environment bytes":
				if err := os.WriteFile(filepath.Join(destination, "Borealis", "Engine", "Deploy", "runtime.env"), []byte("standalone=false\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "environment mode":
				if err := os.Chmod(filepath.Join(destination, "Borealis", "Engine", "Deploy", "compose.env"), 0644); err != nil {
					t.Fatal(err)
				}
			case "missing environment":
				if err := os.Remove(filepath.Join(destination, "Borealis", "Engine", "Deploy", "runtime.env")); err != nil {
					t.Fatal(err)
				}
			case "extra runtime":
				if err := os.Mkdir(filepath.Join(destination, "Borealis", "Engine", "Services"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.reconcileSource(context.Background(), j, destination); err == nil {
				t.Fatal("changed effect accepted")
			}
		})
	}
}
