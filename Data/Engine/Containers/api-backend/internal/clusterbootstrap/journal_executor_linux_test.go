package clusterbootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMutationJournalPersistsExecutorBeforeEffectAndPreservesProducingIdentity(t *testing.T) {
	root := mutationTempDir(t)
	owner, source := mutationFixture()
	ctx := context.Background()
	first := testMutationExecutor(mutationDigest("first"))
	actual := first
	stopped := false
	probes := 0
	open := func() *MutationJournal {
		j, err := openMutationJournal(ctx, root, owner, source, mutationAuthority,
			func(context.Context) (ExecutorIdentity, error) { return actual, nil },
			func(_ context.Context, prior ExecutorIdentity) error {
				probes++
				if prior != first || !stopped {
					return ErrExecutorContainment
				}
				return nil
			})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	j := open()
	input, result := mutationDigest("input"), mutationDigest("effect")
	_, err := j.Apply(ctx, "prepare_host", input, func(context.Context, *os.File) (string, error) {
		r := readMutationFixture(t, root).Steps["prepare_host"]
		if r.Executor != first || r.State != "intent" {
			t.Fatal("executor not persisted before effect")
		}
		return "", ErrMutationUnknown
	})
	if err != ErrMutationUnknown {
		t.Fatal(err)
	}
	called := 0
	proof := func(context.Context) (string, error) { called++; return result, nil }
	if _, err := j.Reconcile(ctx, "prepare_host", input, proof); err != ErrMutationUnknown || called != 0 || probes != 0 {
		t.Fatal("live receiver reconciled itself")
	}
	j.Close()
	actual = testMutationExecutor(mutationDigest("replacement"))
	owner = nextMutationOwner(owner)
	j = open()
	defer j.Close()
	for _, reconcile := range []func(context.Context, string, string, func(context.Context) (string, error)) (string, error){j.Reconcile, j.ReconcileAbsent} {
		if _, err := reconcile(ctx, "prepare_host", input, proof); err != ErrMutationUnknown || called != 0 {
			t.Fatal("free lock permitted proof while old executor remained live")
		}
	}
	stopped = true
	// A manager/kernel observation can change during domain proof. No outcome
	// may be committed from the earlier observation in that case.
	if _, err := j.ReconcileAbsent(ctx, "prepare_host", input, func(context.Context) (string, error) { stopped = false; return result, nil }); err != ErrMutationUnknown {
		t.Fatal("quiescence loss during proof committed absence")
	}
	if readMutationFixture(t, root).Steps["prepare_host"].State != "intent" {
		t.Fatal("uncertainty discarded")
	}
	stopped = true
	before := probes
	if got, err := j.Reconcile(ctx, "prepare_host", input, proof); err != nil || got != result || probes != before+2 {
		t.Fatal("valid recovery failed independent before/after proof")
	}
	r := readMutationFixture(t, root).Steps["prepare_host"]
	if r.Executor != first || r.Binding.Generation == owner.Generation || r.State != "applied" {
		t.Fatal("recovery relabeled producing executor/owner")
	}
}

func TestMutationJournalFencesPriorExecutorsBeforeNextEffect(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{false: "applied", true: "absence history"}[absent], func(t *testing.T) {
			root := mutationTempDir(t)
			owner, source := mutationFixture()
			ctx := context.Background()
			first := testMutationExecutor(mutationDigest("first"))
			actual := first
			stopped := true
			open := func() *MutationJournal {
				j, err := openMutationJournal(ctx, root, owner, source, mutationAuthority, func(context.Context) (ExecutorIdentity, error) { return actual, nil }, func(_ context.Context, prior ExecutorIdentity) error {
					if prior == first && !stopped {
						return ErrExecutorContainment
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				return j
			}
			input, result := mutationDigest("input"), mutationDigest("effect")
			j := open()
			_, err := j.Apply(ctx, "stage_source", input, func(context.Context, *os.File) (string, error) {
				if absent {
					return "", ErrMutationUnknown
				}
				return result, nil
			})
			if !absent && err != nil || absent && err != ErrMutationUnknown {
				t.Fatal(err)
			}
			j.Close()
			actual = testMutationExecutor(mutationDigest("second"))
			owner = nextMutationOwner(owner)
			j = open()
			if absent {
				if _, err := j.ReconcileAbsent(ctx, "stage_source", input, func(context.Context) (string, error) { return mutationDigest("absence"), nil }); err != nil {
					t.Fatal(err)
				}
				if _, err := j.Apply(ctx, "stage_source", input, func(context.Context, *os.File) (string, error) { return result, nil }); err != nil {
					t.Fatal(err)
				}
			}
			j.Close()
			actual = testMutationExecutor(mutationDigest("third"))
			owner = nextMutationOwner(owner)
			j = open()
			defer j.Close()
			stopped = false
			called := false
			if _, err := j.Apply(ctx, "prepare_host", input, func(context.Context, *os.File) (string, error) { called = true; return result, nil }); err != ErrMutationUnknown || called {
				t.Fatal("prior executor bypassed before next effect")
			}
			if _, ok := readMutationFixture(t, root).Steps["prepare_host"]; ok {
				t.Fatal("failed prior proof created new intent")
			}
		})
	}
}

func TestMutationJournalRejectsLostSupervisionAndLegacyIdentity(t *testing.T) {
	root := mutationTempDir(t)
	owner, source := mutationFixture()
	ctx := context.Background()
	if j, err := OpenMutationJournal(ctx, root, owner, source, "invalid nonce", mutationAuthority); err != ErrExecutorContainment || j != nil {
		t.Fatal("public journal allowed unsupervised caller")
	}
	actual := testMutationExecutor(mutationDigest("current"))
	j, err := openMutationJournal(ctx, root, owner, source, mutationAuthority, func(context.Context) (ExecutorIdentity, error) { return actual, nil }, func(context.Context, ExecutorIdentity) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	actual.InvocationID = strings.Repeat("b", 32)
	if _, err := j.Apply(ctx, "stage_source", mutationDigest("input"), func(context.Context, *os.File) (string, error) {
		t.Fatal("changed current executor ran")
		return "", nil
	}); err != ErrSessionAuthority {
		t.Fatal(err)
	}
	j.Close()
	path := filepath.Join(root, mutationJournalName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if json.Unmarshal(raw, &state) != nil {
		t.Fatal("fixture decode")
	}
	state["version"] = 1
	legacy, _ := json.Marshal(state)
	if os.WriteFile(path, legacy, 0o600) != nil {
		t.Fatal("fixture write")
	}
	if j, err := openTestMutationJournal(ctx, root, owner, source, mutationAuthority); err != ErrMutationJournal || j != nil {
		t.Fatal("legacy journal fabricated executor evidence")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(legacy) {
		t.Fatal("legacy evidence changed")
	}
}
