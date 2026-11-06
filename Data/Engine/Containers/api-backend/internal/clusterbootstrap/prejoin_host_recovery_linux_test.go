package clusterbootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrejoinHostConfiguredRecovery(t *testing.T) {
	for _, mode := range []string{"success", "lost acknowledgement", "interrupted again", "source drift", "key drift"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			p, j, path, parent, install, material := prejoinHostFixture(t)
			configured, complete := false, false
			prove := func(context.Context) error {
				if !complete {
					return ErrMutationUnknown
				}
				return nil
			}
			_, err := p.preparePrejoinHost(ctx, j, install, material, PrejoinHostActions{
				CheckFresh: func(context.Context) error { return nil },
				Prepare: func(context.Context, *os.File, *os.File) error {
					configured = true
					return errors.New("packages configured, files incomplete")
				}, Prove: prove,
			}, parent, false)
			if err != ErrMutationUnknown {
				t.Fatal(err)
			}
			old := readMutationFixture(t, path).Steps["prepare_host"]
			j = reopenSourcePublication(t, p, j, path)
			calls := 0
			actions := PrejoinHostRecoveryActions{
				ProveConfigured: func(context.Context) error {
					requirePrejoinHostLock(t, j.lock, j.lock.Name())
					identityConsumerBlocked(t, install.Root)
					if !configured || complete {
						return ErrMutationUnknown
					}
					return nil
				},
				Complete: func(_ context.Context, host, identity *os.File) error {
					calls++
					state := readMutationFixture(t, path)
					if state.Version != 4 || len(state.History) != calls || state.History[0].Record.Executor != old.Executor || state.History[0].Record.Binding != old.Binding || state.History[0].Record.ResultSHA256 != prejoinHostConfiguredDigest(old.InputSHA256) || state.Steps["prepare_host"].Executor != j.executor || state.Steps["prepare_host"].State != "intent" {
						t.Fatal("host effects preceded durable producer transfer")
					}
					requirePrejoinHostLock(t, host, j.lock.Name())
					requirePrejoinHostLock(t, identity, install.Root)
					identityConsumerBlocked(t, install.Root)
					if mode == "interrupted again" && calls == 1 {
						return errors.New("interrupted")
					}
					complete = true
					if mode == "lost acknowledgement" {
						return errors.New("complete, ack lost")
					}
					return nil
				}, Prove: prove,
			}
			if mode == "source drift" {
				if err := os.WriteFile(filepath.Join(parent, "Borealis", "Engine.sh"), []byte("foreign"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "key drift" {
				for name := range material.Files() {
					if err := os.WriteFile(filepath.Join(install.Root, name), []byte("foreign"), 0600); err != nil {
						t.Fatal(err)
					}
					break
				}
			}
			_, err = p.completePrejoinHost(ctx, j, install, material, actions, parent)
			if (err == nil) != (mode == "success") {
				t.Fatal("incorrect continuation", err)
			}
			if mode == "source drift" || mode == "key drift" {
				if calls != 0 || readMutationFixture(t, path).Version != 2 {
					t.Fatal("drift consumed continuation")
				}
				return
			}
			if _, err := p.completePrejoinHost(ctx, j, install, material, actions, parent); err == nil || calls != 1 {
				t.Fatal("same producer replayed", err, calls)
			}
			j = reopenSourcePublication(t, p, j, path)
			if mode == "interrupted again" {
				if _, err := p.completePrejoinHost(ctx, j, install, material, actions, parent); err != nil || calls != 2 {
					t.Fatal("second claim failed", err, calls)
				}
				j = reopenSourcePublication(t, p, j, path)
			}
			if _, err := p.preparePrejoinHost(ctx, j, install, material, PrejoinHostActions{Prove: prove}, parent, true); err != nil {
				t.Fatal("independent complete proof", err)
			}
			state := readMutationFixture(t, path)
			if state.Steps["prepare_host"].State != "applied" || state.History[0].Record.State != "continued" {
				t.Fatal("lost history")
			}
			if progress, err := p.InspectPreparationProgress(ctx, j); err != nil || progress.Step != "prepare_host" {
				t.Fatal("resume lost continuation", progress, err)
			}
			if install.ProvePrejoinFencedCommitted(material) != nil {
				t.Fatal("identity fence changed")
			}
		})
	}
}

func TestPrejoinHostContinuationBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "same generation", "self", "active prior", "partial proof", "late partial drift", "authority", "input", "applied", "later phase", "history full", "persistence failure"} {
		t.Run(mode, func(t *testing.T) {
			j := progressJournalFixture(t, 5, true)
			j.state.Owner.Generation++
			input := j.state.Steps["prepare_host"].InputSHA256
			before := j.state.Steps["prepare_host"]
			switch mode {
			case "same generation":
				j.state.Owner.Generation--
			case "self":
				j.executor = before.Executor
			case "active prior":
				j.quiescent = func(context.Context, ExecutorIdentity) error { return ErrMutationUnknown }
			case "authority":
				j.check = func(context.Context) error { return ErrSessionAuthority }
			case "input":
				input = mutationDigest("other")
			case "applied":
				r := before
				r.State = "applied"
				r.ResultSHA256 = input
				j.state.Steps["prepare_host"] = r
			case "later phase":
				r := before
				r.State = "applied"
				r.ResultSHA256 = input
				j.state.Steps["deliver_images"] = r
			case "history full":
				r := before
				r.State = "not_applied"
				r.ResultSHA256 = input
				for range 16 {
					j.state.History = append(j.state.History, mutationPastAttempt{Step: "prepare_host", Record: r})
				}
			case "persistence failure":
				if err := j.root.Remove(mutationJournalName); err != nil {
					t.Fatal(err)
				}
				if err := j.root.Mkdir(mutationJournalName, 0700); err != nil {
					t.Fatal(err)
				}
			}
			proofs, writes := 0, 0
			_, err := j.continuePrejoinHostLocked(context.Background(), input, func(context.Context) error {
				proofs++
				if mode == "partial proof" || mode == "late partial drift" && proofs == 2 {
					return ErrMutationUnknown
				}
				return nil
			}, func(context.Context, *os.File) (string, error) { writes++; return input, nil })
			if (err == nil) != (mode == "valid") || writes != map[bool]int{true: 1}[mode == "valid"] {
				t.Fatal("unsafe continuation", err, proofs, writes)
			}
			if mode == "persistence failure" && !j.failed {
				t.Fatal("failed persistence left usable journal")
			}
		})
	}
}

func TestPrejoinHostContinuationFormat(t *testing.T) {
	j := progressJournalFixture(t, 5, true)
	j.state.Owner.Generation++
	input := j.state.Steps["prepare_host"].InputSHA256
	if _, err := j.continuePrejoinHostLocked(context.Background(), input, func(context.Context) error { return nil }, func(context.Context, *os.File) (string, error) { return input, nil }); err != nil {
		t.Fatal(err)
	}
	valid := j.state
	for _, mode := range []string{"old version", "wrong step", "wrong partial proof", "future history", "missing current", "wrong current input", "absent current"} {
		t.Run(mode, func(t *testing.T) {
			s := valid
			s.History = append([]mutationPastAttempt(nil), valid.History...)
			s.Steps = map[string]mutationRecord{}
			for k, v := range valid.Steps {
				s.Steps[k] = v
			}
			switch mode {
			case "old version":
				s.Version = 3
			case "wrong step":
				s.History[0].Step = "prepare_runtime_identity"
			case "wrong partial proof":
				s.History[0].Record.ResultSHA256 = mutationDigest("foreign")
			case "future history":
				s.History[0].Record.Binding.Generation = s.Owner.Generation
			case "missing current":
				delete(s.Steps, "prepare_host")
			case "wrong current input":
				r := s.Steps["prepare_host"]
				r.InputSHA256 = mutationDigest("other")
				s.Steps["prepare_host"] = r
			case "absent current":
				r := s.Steps["prepare_host"]
				r.State = "not_applied"
				s.Steps["prepare_host"] = r
			}
			if s.valid() {
				t.Fatal("malformed continuation accepted")
			}
		})
	}
}

func TestPrejoinHostContinuationPreservesAccountHistory(t *testing.T) {
	j := progressJournalFixture(t, 5, true)
	old := j.state.Steps["prepare_runtime_identity"]
	for _, step := range []string{"prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host"} {
		r := j.state.Steps[step]
		r.Binding.Generation++
		j.state.Steps[step] = r
	}
	old.State, old.ResultSHA256 = "continued", runtimeIdentityGroupOnlyDigest(old.InputSHA256)
	j.state.Version = 3
	j.state.History = append(j.state.History, mutationPastAttempt{Step: "prepare_runtime_identity", Record: old})
	j.state.Owner.Generation += 2
	input := j.state.Steps["prepare_host"].InputSHA256
	if _, err := j.continuePrejoinHostLocked(context.Background(), input, func(context.Context) error { return nil }, func(context.Context, *os.File) (string, error) { return input, nil }); err != nil {
		t.Fatal(err)
	}
	if !j.state.valid() || j.state.Version != 4 || len(j.state.History) != 2 || j.state.History[0].Record != old {
		t.Fatal("account producer lost")
	}
	// Removing the host history must not make version4 an ordinary account journal.
	j.state.History = j.state.History[:1]
	if j.state.valid() {
		t.Fatal("version4 without host contract accepted")
	}
}
