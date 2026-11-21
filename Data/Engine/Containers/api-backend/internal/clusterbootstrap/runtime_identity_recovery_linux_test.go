package clusterbootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeIdentityGroupOnlyRecovery(t *testing.T) {
	for _, mode := range []string{"success", "lost acknowledgement", "interrupted again", "source drift"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			p, j, path, parent := sourcePublicationFixture(t)
			if _, err := p.publishSource(ctx, j, parent); err != nil {
				t.Fatal(err)
			}
			group, user := false, false
			prove := func(context.Context) error {
				if !group || !user {
					return ErrMutationUnknown
				}
				return nil
			}
			_, err := p.prepareRuntimeIdentity(ctx, j, RuntimeIdentityActions{Prepare: func(context.Context, *os.File) error { group = true; return errors.New("interrupted group-only") }, Prove: prove}, parent, os.Geteuid(), os.Getegid(), false)
			if err != ErrMutationUnknown {
				t.Fatal(err)
			}
			original := readMutationFixture(t, path).Steps["prepare_runtime_identity"]
			j = reopenSourcePublication(t, p, j, path)
			calls := 0
			actions := RuntimeIdentityRecoveryActions{
				ProveGroupOnly: func(context.Context) error {
					if !group || user {
						return ErrMutationUnknown
					}
					return nil
				},
				CompleteUser: func(_ context.Context, lock *os.File) error {
					calls++
					state := readMutationFixture(t, path)
					if lock == nil || state.Version != 3 || len(state.History) != calls || state.History[0].Record.Executor != original.Executor || state.History[0].Record.Binding != original.Binding || state.History[0].Record.ResultSHA256 != runtimeIdentityGroupOnlyDigest(original.InputSHA256) || state.Steps["prepare_runtime_identity"].Executor != j.executor || state.Steps["prepare_runtime_identity"].State != "intent" {
						t.Fatal("effect preceded durable producer transfer")
					}
					if mode == "interrupted again" && calls == 1 {
						return errors.New("no user created")
					}
					user = true
					if mode == "lost acknowledgement" {
						return errors.New("user created, result lost")
					}
					return nil
				}, Prove: prove,
			}
			if mode == "source drift" {
				if err := os.WriteFile(filepath.Join(parent, "Borealis", "Engine.sh"), []byte("changed"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			_, err = p.completeRuntimeIdentity(ctx, j, actions, parent, os.Geteuid(), os.Getegid())
			if (err == nil) != (mode == "success") {
				t.Fatal("unexpected completion", err)
			}
			if mode == "source drift" {
				if calls != 0 || readMutationFixture(t, path).Version != 2 {
					t.Fatal("drift consumed intent")
				}
				return
			}
			if _, err := p.completeRuntimeIdentity(ctx, j, actions, parent, os.Geteuid(), os.Getegid()); err == nil || calls != 1 {
				t.Fatal("completion replayed", err, calls)
			}
			j = reopenSourcePublication(t, p, j, path)
			if mode == "interrupted again" {
				if _, err := p.completeRuntimeIdentity(ctx, j, actions, parent, os.Geteuid(), os.Getegid()); err != nil || calls != 2 {
					t.Fatal("partial continuation lost", err, calls)
				}
				j = reopenSourcePublication(t, p, j, path)
			}
			if _, err := p.prepareRuntimeIdentity(ctx, j, RuntimeIdentityActions{Prove: prove}, parent, os.Geteuid(), os.Getegid(), true); err != nil {
				t.Fatal("independent complete proof failed", err)
			}
			state := readMutationFixture(t, path)
			if state.Steps["prepare_runtime_identity"].State != "applied" || state.History[0].Record.State != "continued" {
				t.Fatal("history or final result lost")
			}
			if got, err := p.InspectPreparationProgress(ctx, j); err != nil || got.Step != "prepare_runtime_identity" {
				t.Fatal("continued history blocks resume", got, err)
			}
		})
	}
}

func TestRuntimeIdentityContinuationBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "same generation", "self", "active prior", "partial proof", "late partial drift", "authority", "input", "applied", "later phase", "history full", "persistence failure"} {
		t.Run(mode, func(t *testing.T) {
			j := progressJournalFixture(t, 2, true)
			j.state.Owner.Generation++
			input := j.state.Steps["prepare_runtime_identity"].InputSHA256
			before := j.state.Steps["prepare_runtime_identity"]
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
				j.state.Steps["prepare_runtime_identity"] = r
			case "later phase":
				r := before
				r.State = "applied"
				r.ResultSHA256 = input
				j.state.Steps["prepare_host"] = r
			case "history full":
				r := before
				r.State = "not_applied"
				r.ResultSHA256 = input
				for range 16 {
					j.state.History = append(j.state.History, mutationPastAttempt{Step: "prepare_runtime_identity", Record: r})
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
			_, err := j.continueRuntimeIdentityLocked(context.Background(), input, func(context.Context) error {
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

func TestRuntimeIdentityContinuationFormat(t *testing.T) {
	j := progressJournalFixture(t, 2, true)
	j.state.Owner.Generation++
	input := j.state.Steps["prepare_runtime_identity"].InputSHA256
	if _, err := j.continueRuntimeIdentityLocked(context.Background(), input, func(context.Context) error { return nil }, func(context.Context, *os.File) (string, error) { return input, nil }); err != nil {
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
				s.Version = 2
			case "wrong step":
				s.History[0].Step = "prepare_host"
			case "wrong partial proof":
				s.History[0].Record.ResultSHA256 = mutationDigest("foreign")
			case "future history":
				s.History[0].Record.Binding.Generation = s.Owner.Generation
			case "missing current":
				delete(s.Steps, "prepare_runtime_identity")
			case "wrong current input":
				r := s.Steps["prepare_runtime_identity"]
				r.InputSHA256 = mutationDigest("other")
				s.Steps["prepare_runtime_identity"] = r
			case "absent current":
				r := s.Steps["prepare_runtime_identity"]
				r.State = "not_applied"
				s.Steps["prepare_runtime_identity"] = r
			}
			if s.valid() {
				t.Fatal("malformed continuation accepted")
			}
		})
	}
}
