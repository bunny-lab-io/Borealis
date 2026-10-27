package clusterbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func progressJournalFixture(t *testing.T, count int, intent bool) *MutationJournal {
	t.Helper()
	owner, source := mutationFixture()
	j, err := openTestMutationJournal(context.Background(), mutationTempDir(t), owner, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	for i, step := range []string{"stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host", "stage_images", "install_k3s"} {
		if i >= count {
			break
		}
		r := mutationRecord{Binding: owner, Executor: testMutationExecutor(mutationDigest(step)), InputSHA256: mutationDigest(step), ResultSHA256: mutationDigest(step), State: "applied"}
		if intent && i == count-1 {
			r.State, r.ResultSHA256 = "intent", ""
		}
		j.state.Steps[step] = r
	}
	if j.persist(context.Background()) != nil {
		t.Fatal("fixture persistence")
	}
	return j
}

func TestPreparationProgressRetainedPrefixes(t *testing.T) {
	steps := []string{"empty", "stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host", "stage_images", "install_k3s"}
	for count, step := range steps {
		for _, intent := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/intent=%t", step, intent), func(t *testing.T) {
				j := progressJournalFixture(t, count, intent)
				before, _ := json.Marshal(j.state)
				stopped := 0
				j.quiescent = func(context.Context, ExecutorIdentity) error { stopped++; return nil }
				got, err := j.preparationProgressLocked(context.Background(), mutationDigest("stage_source"))
				if err != nil || got.Step != step || stopped != count*2 {
					t.Fatalf("cursor=%+v err=%v stops=%d", got, err, stopped)
				}
				after, _ := json.Marshal(j.state)
				disk, err := os.ReadFile(filepath.Join(j.root.Name(), mutationJournalName))
				if err != nil || string(before) != string(after) || string(before) != string(disk) {
					t.Fatal("inspection changed journal")
				}
			})
		}
	}
}

func TestPreparationProgressInputBinding(t *testing.T) {
	p, j, path, destination := sourcePublicationFixture(t)
	ctx := context.Background()
	if got, err := p.InspectPreparationProgress(ctx, j); err != nil || got.Step != "empty" {
		t.Fatal(got, err)
	}
	if _, err := p.publishSource(ctx, j, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := p.InspectPreparationProgress(ctx, j); err == nil {
		t.Fatal("producer inspected own cursor")
	}
	j = reopenSourcePublication(t, p, j, path)
	if got, err := p.InspectPreparationProgress(ctx, j); err != nil || got.Step != "stage_source" {
		t.Fatal(got, err)
	}
	// Immutable configuration is part of the source input even when the release
	// and target authority still match. A cursor must not hide changed settings.
	p.configuration.wire.Expected.EdgeVIP = "192.168.50.99"
	if got, err := p.InspectPreparationProgress(ctx, j); err == nil || got != (PreparationProgress{}) {
		t.Fatal("changed configuration accepted")
	}
	if got, err := (*PreparationInputs)(nil).InspectPreparationProgress(ctx, j); err == nil || got != (PreparationProgress{}) {
		t.Fatal("missing inputs accepted")
	}
}

func TestPreparationProgressRejectsUnsafeJournal(t *testing.T) {
	for _, mode := range []string{"hole", "joined", "images", "earlier intent", "absent effect", "self executor", "source input", "source result", "generation reversal", "history without current", "history input", "active executor", "late active executor", "authority lost", "late authority", "cancelled", "failed journal", "closed"} {
		t.Run(mode, func(t *testing.T) {
			j := progressJournalFixture(t, 5, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := j.state.Steps["prepare_host"]
			switch mode {
			case "hole":
				delete(j.state.Steps, "prepare_runtime_identity")
			case "joined":
				j.state.Steps["join_cluster"] = r
			case "images":
				delete(j.state.Steps, "prepare_host")
				j.state.Steps["stage_images"] = r
			case "earlier intent":
				x := j.state.Steps["stage_source"]
				x.State, x.ResultSHA256 = "intent", ""
				j.state.Steps["stage_source"] = x
			case "absent effect":
				r.State = "not_applied"
				j.state.Steps["prepare_host"] = r
			case "self executor":
				r.Executor = j.executor
				j.state.Steps["prepare_host"] = r
			case "source input":
				x := j.state.Steps["stage_source"]
				x.InputSHA256 = mutationDigest("other")
				j.state.Steps["stage_source"] = x
			case "source result":
				x := j.state.Steps["stage_source"]
				x.ResultSHA256 = mutationDigest("other")
				j.state.Steps["stage_source"] = x
			case "generation reversal":
				j.state.Owner.Generation++
				x := j.state.Steps["stage_source"]
				x.Binding.Generation++
				j.state.Steps["stage_source"] = x
			case "history without current", "history input":
				j.state.Owner.Generation++
				r.State = "not_applied"
				r.InputSHA256 = mutationDigest("other")
				step := "prepare_host"
				if mode == "history without current" {
					step = "join_cluster"
				}
				j.state.History = append(j.state.History, mutationPastAttempt{Step: step, Record: r})
			case "active executor":
				j.quiescent = func(context.Context, ExecutorIdentity) error { return errors.New("still active") }
			case "late active executor":
				n := 0
				j.quiescent = func(context.Context, ExecutorIdentity) error {
					n++
					if n > 5 {
						return errors.New("active again")
					}
					return nil
				}
			case "authority lost":
				j.check = func(context.Context) error { return errors.New("lost") }
			case "late authority":
				n := 0
				j.check = func(context.Context) error {
					n++
					if n > 7 {
						return errors.New("lost")
					}
					return nil
				}
			case "cancelled":
				cancel()
			case "failed journal":
				j.failed = true
			case "closed":
				j.Close()
			}
			before, _ := json.Marshal(j.state)
			got, err := j.preparationProgressLocked(ctx, mutationDigest("stage_source"))
			if err == nil || got != (PreparationProgress{}) {
				t.Fatal("unsafe cursor escaped")
			}
			after, _ := json.Marshal(j.state)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("failure rewrote evidence")
			}
		})
	}
}
