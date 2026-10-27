package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func prejoinHostFixture(t *testing.T) (*PreparationInputs, *MutationJournal, string, string, engineidentity.Installation, *engineidentity.Material) {
	t.Helper()
	p, j, root, parent, install, material := prejoinDirectoryFixture(t)
	if _, err := p.preparePrejoinIdentityRoot(context.Background(), j, install, material, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := p.installPreparedPrejoinIdentity(context.Background(), j, install, material, parent); err != nil {
		t.Fatal(err)
	}
	return p, j, root, parent, install, material
}

func requirePrejoinHostLock(t *testing.T, inherited *os.File, path string) {
	t.Helper()
	if inherited == nil {
		t.Fatal("missing inherited exclusion")
	}
	held, err := inherited.Stat()
	current, statErr := os.Stat(path)
	if err != nil || statErr != nil || !os.SameFile(held, current) {
		t.Fatal("wrong inherited exclusion", err, statErr)
	}
	probe, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatal("inherited exclusion not held", err)
	}
}

func TestPrejoinHostIntentAndIndependentReconciliation(t *testing.T) {
	for _, mode := range []string{"success", "lost acknowledgement", "partial effect", "proof failure", "post commit loss"} {
		t.Run(mode, func(t *testing.T) {
			p, j, root, parent, install, material := prejoinHostFixture(t)
			ctx := context.Background()
			// Native host effects use a private file fixture outside source tree.
			// No package manager, systemd, real host configuration or Engine runs.
			hostPath := filepath.Join(t.TempDir(), "prepared-host")
			want := []byte("complete fixed native host contract")
			calls, proofs := 0, 0
			check := j.check
			lost := false
			j.check = func(ctx context.Context) error {
				lost = lost || mode == "post commit loss" && readMutationFixture(t, root).Steps["prepare_host"].State == "applied"
				if lost {
					return ErrSessionAuthority
				}
				return check(ctx)
			}
			actions := PrejoinHostActions{
				Prepare: func(_ context.Context, hostLock, identityLock *os.File) error {
					calls++
					record := readMutationFixture(t, root).Steps["prepare_host"]
					if record.State != "intent" || record.ResultSHA256 != "" || record.Executor != j.executor || record.Binding != j.state.Owner {
						t.Fatal("effect preceded exact durable intent")
					}
					requirePrejoinHostLock(t, hostLock, j.lock.Name())
					requirePrejoinHostLock(t, identityLock, install.Root)
					identityConsumerBlocked(t, install.Root)
					data := want
					if mode == "partial effect" {
						data = []byte("partial")
					}
					if err := os.WriteFile(hostPath, data, 0600); err != nil {
						t.Fatal(err)
					}
					if mode == "lost acknowledgement" || mode == "partial effect" {
						return errors.New("private host command output")
					}
					return nil
				},
				Prove: func(context.Context) error {
					proofs++
					identityConsumerBlocked(t, install.Root)
					got, err := os.ReadFile(hostPath)
					if mode == "proof failure" || err != nil || !bytes.Equal(got, want) {
						return errors.New("private incomplete host state")
					}
					return nil
				},
			}
			result, err := p.preparePrejoinHost(ctx, j, install, material, actions, parent, false)
			if (err == nil) != (mode == "success") || calls != 1 {
				t.Fatal("incorrect initial host outcome", err, calls)
			}
			if err != nil && err != ErrMutationUnknown {
				t.Fatal("private host error escaped", err)
			}
			record := readMutationFixture(t, root).Steps["prepare_host"]
			if mode == "success" || mode == "post commit loss" {
				if record.State != "applied" || !digestPattern.MatchString(record.ResultSHA256) {
					t.Fatal("completed effect missing")
				}
			} else if record.State != "intent" || record.ResultSHA256 != "" {
				t.Fatal("uncertainty erased")
			}
			j.check = check
			if _, err := p.preparePrejoinHost(ctx, j, install, material, actions, parent, false); err == nil || calls != 1 {
				t.Fatal("write replayed", err)
			}
			beforeProofs := proofs
			if _, err := p.preparePrejoinHost(ctx, j, install, material, actions, parent, true); err == nil || proofs != beforeProofs {
				t.Fatal("producer proved its own stopped executor", err)
			}
			j = reopenSourcePublication(t, p, j, root)
			// Deliberately leave Prepare present: reconciliation must never use it.
			got, err := p.preparePrejoinHost(ctx, j, install, material, actions, parent, true)
			valid := mode != "partial effect" && mode != "proof failure"
			if (err == nil) != valid || calls != 1 || proofs <= beforeProofs {
				t.Fatal("incorrect independent reconciliation", err, calls, proofs)
			}
			if valid {
				if !digestPattern.MatchString(got) || result != "" && result != got {
					t.Fatal("receipt changed across recovery")
				}
				if again, err := p.preparePrejoinHost(ctx, j, install, material, actions, parent, true); err != nil || again != got {
					t.Fatal("same proved result not idempotent", err)
				}
			} else if readMutationFixture(t, root).Steps["prepare_host"].State != "intent" {
				t.Fatal("partial effects settled")
			}
			identityConsumerBlocked(t, install.Root)
			if install.ProvePrejoinFencedCommitted(material) != nil {
				t.Fatal("host phase changed committed identity")
			}
			// Host receipt must not broaden source-only or identity-prefix proof.
			if _, err := p.reconcileSource(ctx, j, parent); err == nil {
				t.Fatal("host phase accepted as source-only state")
			}
			if _, err := p.reconcilePreparedPrejoinIdentity(ctx, j, install, material, parent); err == nil {
				t.Fatal("host phase accepted as identity-prefix state")
			}
		})
	}
}

func TestPrejoinHostRejectsInvalidPrerequisites(t *testing.T) {
	for _, mode := range []string{"source receipt", "account receipt", "directory receipt", "identity receipt", "missing identity", "source bytes", "key bytes", "marker absent", "joined", "claim", "source selection", "later phase", "authority", "quiescence", "old executor active", "no prepare", "no proof"} {
		t.Run(mode, func(t *testing.T) {
			p, j, _, parent, install, material := prejoinHostFixture(t)
			calls := 0
			actions := PrejoinHostActions{Prepare: func(context.Context, *os.File, *os.File) error { calls++; return nil }, Prove: func(context.Context) error { calls++; return nil }}
			var err error
			switch mode {
			case "source receipt", "account receipt", "directory receipt", "identity receipt":
				step := map[string]string{"source receipt": "stage_source", "account receipt": "prepare_runtime_identity", "directory receipt": "prepare_identity_root", "identity receipt": "install_identity"}[mode]
				record := j.state.Steps[step]
				record.ResultSHA256 = mutationDigest("substitution")
				j.state.Steps[step] = record
			case "missing identity":
				delete(j.state.Steps, "install_identity")
			case "source bytes":
				err = os.WriteFile(filepath.Join(parent, "Borealis", "Engine.sh"), []byte("changed"), 0755)
			case "key bytes":
				err = os.WriteFile(filepath.Join(install.Root, engineidentity.EngineSecretPath), []byte("changed"), 0600)
			case "marker absent":
				err = os.Remove(filepath.Join(install.Root, engineidentity.RecoveryMarkerName))
			case "joined":
				install.Target.NodeUID = "40000000-0000-4000-8000-000000000004"
			case "claim":
				j.state.Owner.Generation++
			case "source selection":
				install.Binding.SourceID = "50000000-0000-4000-8000-000000000005"
			case "later phase":
				prejoinAppliedFixture(t, j, "stage_images")
			case "authority":
				install.Check = func() error { return ErrSessionAuthority }
			case "quiescence":
				install.Quiesced = func() error { return ErrExecutorContainment }
			case "old executor active":
				record := j.state.Steps["install_identity"]
				record.Executor.InvocationID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
				j.state.Steps["install_identity"] = record
				j.quiescent = func(context.Context, ExecutorIdentity) error { return ErrExecutorContainment }
			case "no prepare":
				actions.Prepare = nil
			case "no proof":
				actions.Prove = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.preparePrejoinHost(context.Background(), j, install, material, actions, parent, false); err == nil || calls != 0 {
				t.Fatal("invalid prerequisite reached native host action", err, calls)
			}
			if _, exists := j.state.Steps["prepare_host"]; exists {
				t.Fatal("invalid prerequisite consumed host intent")
			}
		})
	}
}

func TestPrejoinHostRejectsDriftDuringEffects(t *testing.T) {
	for _, mode := range []string{"source", "environment", "key", "marker", "inner journal", "authority", "quiescence", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			p, j, _, parent, install, material := prejoinHostFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			valid := true
			check := func() error {
				if !valid {
					return ErrSessionAuthority
				}
				return nil
			}
			if mode == "authority" {
				install.Check = check
			} else if mode == "quiescence" {
				install.Quiesced = check
			}
			actions := PrejoinHostActions{Prove: func(context.Context) error { return nil }, Prepare: func(context.Context, *os.File, *os.File) error {
				switch mode {
				case "source":
					return os.WriteFile(filepath.Join(parent, "Borealis", "Engine.sh"), []byte("changed"), 0755)
				case "environment":
					return os.WriteFile(filepath.Join(parent, "Borealis", "Engine", "Deploy", "runtime.env"), []byte("changed"), 0600)
				case "key":
					return os.WriteFile(filepath.Join(install.Root, engineidentity.EngineSecretPath), []byte("changed"), 0600)
				case "marker":
					return os.Remove(filepath.Join(install.Root, engineidentity.RecoveryMarkerName))
				case "inner journal":
					return os.WriteFile(filepath.Join(install.Journal, "journal.json"), []byte("{}"), 0600)
				case "authority", "quiescence":
					valid = false
				case "cancel":
					cancel()
				}
				return nil
			}}
			if _, err := p.preparePrejoinHost(ctx, j, install, material, actions, parent, false); err != ErrMutationUnknown {
				t.Fatal("drift acknowledged", err)
			}
			if _, exists := j.state.Steps["prepare_host"]; !exists {
				t.Fatal("partial effect lost durable intent")
			}
			identityConsumerBlocked(t, install.Root)
		})
	}
}
