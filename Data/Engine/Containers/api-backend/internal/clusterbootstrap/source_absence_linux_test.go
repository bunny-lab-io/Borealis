package clusterbootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func interruptedSourceFixture(t *testing.T) (*PreparationInputs, *MutationJournal, string, string, string) {
	t.Helper()
	p, j, path, destination := sourcePublicationFixture(t)
	input, err := p.sourcePublicationInput(j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Apply(context.Background(), "stage_source", input, func(context.Context, *os.File) (string, error) { return "", errors.New("interrupted before rename") }); err != ErrMutationUnknown {
		t.Fatal(err)
	}
	return p, j, path, destination, input
}

func TestSourceAbsenceFreshClaimAndRetainedScratch(t *testing.T) {
	p, j, path, destination, input := interruptedSourceFixture(t)
	ctx := context.Background()
	if _, err := p.reconcileSourceAbsent(ctx, j, destination); err == nil {
		t.Fatal("producer reconciled own absence")
	}
	// Interrupted private scratch is not the published effect and is not ours to
	// erase. Its continued existence must survive both proof and fresh retry.
	scratch := filepath.Join(destination, ".borealis-source-retained")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "private"), []byte("retained fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	owner, source := j.state.Owner, j.state.Source
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err := openTestMutationJournal(ctx, path, owner, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	result, err := p.reconcileSourceAbsent(ctx, j, destination)
	if err != nil || result != sourcePublicationAbsenceDigest(input) {
		t.Fatal(result, err)
	}
	if _, err := p.publishSource(ctx, j, destination); err != ErrMutationFreshClaim {
		t.Fatal("same claim retried", err)
	}
	if got, err := p.InspectPreparationProgress(ctx, j); err == nil || got != (PreparationProgress{}) {
		t.Fatal("same-generation absence cursor escaped")
	}
	j = reopenSourcePublication(t, p, j, path)
	if got, err := p.InspectPreparationProgress(ctx, j); err != nil || got.Step != "source_absent" {
		t.Fatal("fresh absence cursor lost", got, err)
	}
	if _, err := p.reconcileSourceAbsent(ctx, j, destination); err != nil {
		t.Fatal("absence readback", err)
	}
	if _, err := p.publishSource(ctx, j, destination); err != nil {
		t.Fatal("fresh claim failed", err)
	}
	state := readMutationFixture(t, path)
	if len(state.History) != 1 || state.History[0].Record.Binding != owner || state.History[0].Record.ResultSHA256 != result || state.Steps["stage_source"].State != "applied" {
		t.Fatal("absence history lost")
	}
	if raw, err := os.ReadFile(filepath.Join(scratch, "private")); err != nil || string(raw) != "retained fixture" {
		t.Fatal("private scratch changed")
	}
	if _, err := p.reconcileSourceAbsent(ctx, j, destination); err == nil {
		t.Fatal("applied effect rewritten absent")
	}
	j = reopenSourcePublication(t, p, j, path)
	if got, err := p.InspectPreparationProgress(ctx, j); err != nil || got.Step != "stage_source" {
		t.Fatal("completed retry/history rejected", got, err)
	}
}

func TestSourceAbsenceCursorRequiresExactProof(t *testing.T) {
	for _, mode := range []string{"valid", "wrong digest", "same generation", "wrong input", "later phase"} {
		t.Run(mode, func(t *testing.T) {
			j := progressJournalFixture(t, 1, false)
			r := j.state.Steps["stage_source"]
			r.State, r.ResultSHA256 = "not_applied", sourcePublicationAbsenceDigest(r.InputSHA256)
			j.state.Owner.Generation++
			switch mode {
			case "wrong digest":
				r.ResultSHA256 = mutationDigest("foreign proof")
			case "same generation":
				j.state.Owner.Generation--
			case "wrong input":
				r.InputSHA256 = mutationDigest("other configuration")
			case "later phase":
				j.state.Steps["prepare_host"] = r
			}
			j.state.Steps["stage_source"] = r
			got, err := j.preparationProgressLocked(context.Background(), mutationDigest("stage_source"))
			if mode == "valid" && (err != nil || got.Step != "source_absent") || mode != "valid" && (err == nil || got != (PreparationProgress{})) {
				t.Fatal("unsafe absence cursor", got, err)
			}
		})
	}
}

func TestSourceAbsenceRejectsUncertainState(t *testing.T) {
	p, initial, path, destination, _ := interruptedSourceFixture(t)
	ctx := context.Background()
	owner, source := initial.state.Owner, initial.state.Source
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"directory", "file", "symlink", "unsafe parent", "source drift", "active producer", "authority", "late appearance", "parent replacement"} {
		t.Run(mode, func(t *testing.T) {
			parent := mutationTempDir(t)
			check := mutationAuthority
			j, err := openTestMutationJournal(ctx, path, owner, source, func(ctx context.Context) error { return check(ctx) })
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			switch mode {
			case "directory":
				err = os.Mkdir(filepath.Join(parent, "Borealis"), 0700)
			case "file":
				err = os.WriteFile(filepath.Join(parent, "Borealis"), []byte("foreign"), 0600)
			case "symlink":
				err = os.Symlink(destination, filepath.Join(parent, "Borealis"))
			case "unsafe parent":
				err = os.Chmod(parent, 0777)
			case "source drift":
				old := p.configuration.wire.Expected.EdgeVIP
				p.configuration.wire.Expected.EdgeVIP = "192.168.50.99"
				defer func() { p.configuration.wire.Expected.EdgeVIP = old }()
			case "active producer":
				j.quiescent = func(context.Context, ExecutorIdentity) error { return errors.New("live producer") }
			case "authority":
				check = func(context.Context) error { return errors.New("lost authority") }
			case "late appearance", "parent replacement":
				j.quiescent = func(context.Context, ExecutorIdentity) error {
					if mode == "late appearance" {
						return os.Mkdir(filepath.Join(parent, "Borealis"), 0700)
					}
					if err := os.Rename(parent, parent+"-old"); err != nil {
						return err
					}
					return os.Mkdir(parent, 0700)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.reconcileSourceAbsent(ctx, j, parent); err == nil {
				t.Fatal("unknown state became absence")
			}
			if state := readMutationFixture(t, path); state.Steps["stage_source"].State != "intent" || len(state.History) != 0 {
				t.Fatal("failed proof changed evidence")
			}
		})
	}
}
