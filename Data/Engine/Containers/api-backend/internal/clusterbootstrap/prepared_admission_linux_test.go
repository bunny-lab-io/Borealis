package clusterbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preparedAdmissionJournalFixture(t *testing.T, request PreparedAdmissionRequest) (string, mutationState) {
	t.Helper()
	root := mutationTempDir(t)
	s := mutationState{Version: 2, Owner: request.Expected.Target, Source: request.Expected.Source, Steps: map[string]mutationRecord{}, History: []mutationPastAttempt{}}
	for _, step := range request.Receipt.Steps {
		s.Steps[step.Step] = mutationRecord{Binding: s.Owner, Executor: testMutationExecutor(strings.Repeat("a", 64)), InputSHA256: step.InputSHA256, ResultSHA256: step.ResultSHA256, State: "applied"}
	}
	if !s.valid() {
		t.Fatal("invalid fixture journal")
	}
	raw, _ := json.Marshal(s)
	if os.WriteFile(filepath.Join(root, mutationJournalName), raw, 0600) != nil || os.WriteFile(filepath.Join(root, "mutation.lock"), nil, 0600) != nil {
		t.Fatal("write fixture")
	}
	return root, s
}

func TestPreparedAdmissionJournal(t *testing.T) {
	for _, mode := range []string{"valid", "missing lock", "missing journal", "intent", "missing step", "receipt drift", "wrong owner", "wrong source", "unsafe mode", "symlink", "late journal", "late lock", "late cancel", "swallowed check", "callback error", "caller alias"} {
		t.Run(mode, func(t *testing.T) {
			request := preparedAdmissionFixture(t)
			path, state := preparedAdmissionJournalFixture(t, request)
			journal := filepath.Join(path, mutationJournalName)
			write := func() {
				raw, _ := json.Marshal(state)
				if os.WriteFile(journal, raw, 0600) != nil {
					t.Fatal("write")
				}
			}
			switch mode {
			case "missing lock":
				_ = os.Remove(filepath.Join(path, "mutation.lock"))
			case "missing journal":
				_ = os.Remove(journal)
			case "intent":
				step := state.Steps["activate_identity"]
				step.State = "intent"
				step.ResultSHA256 = ""
				state.Steps["activate_identity"] = step
				write()
			case "missing step":
				delete(state.Steps, "activate_identity")
				write()
			case "receipt drift":
				step := state.Steps["install_identity"]
				step.ResultSHA256 = strings.Repeat("b", 64)
				state.Steps["install_identity"] = step
				write()
			case "wrong owner":
				request.Expected.Target.Generation++
				request.Receipt.ExpectedSHA256 = preparationReceiptDigest(request.Expected)
			case "wrong source":
				state.Source.SourceSHA = strings.Repeat("b", 40)
				write()
			case "unsafe mode":
				_ = os.Chmod(journal, 0644)
			case "symlink":
				_ = os.Rename(journal, journal+"-old")
				_ = os.Symlink(journal+"-old", journal)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			var escaped func(context.Context) error
			err := WithPreparedAdmissionJournal(ctx, path, request, func(check func(context.Context) error) error {
				calls++
				escaped = check
				if err := WithPreparedAdmissionJournal(ctx, path, request, func(func(context.Context) error) error { t.Fatal("nested lock acquired"); return nil }); !errors.Is(err, ErrMutationBusy) {
					t.Fatal("missing exclusive lock", err)
				}
				switch mode {
				case "late journal":
					_ = os.Rename(journal, journal+"-old")
					write()
				case "late lock":
					_ = os.Rename(filepath.Join(path, "mutation.lock"), filepath.Join(path, "old.lock"))
					_ = os.WriteFile(filepath.Join(path, "mutation.lock"), nil, 0600)
				case "late cancel":
					cancel()
				case "swallowed check":
					bad, stop := context.WithCancel(ctx)
					stop()
					_ = check(bad)
				case "callback error":
					return errors.New("action failed")
				case "caller alias":
					request.Expected.PeerAddresses[0] = "192.168.3.99"
				}
				return nil
			})
			want := mode == "valid" || mode == "caller alias"
			if (err == nil) != want {
				t.Fatal("journal result", err)
			}
			if escaped != nil && escaped(context.Background()) == nil {
				t.Fatal("escaped check accepted")
			}
			if !want && !strings.HasPrefix(mode, "late ") && mode != "swallowed check" && mode != "callback error" && calls != 0 {
				t.Fatal("action before complete journal")
			}
			if mode == "missing lock" {
				if _, err := os.Stat(filepath.Join(path, "mutation.lock")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("created lock")
				}
			}
			if mode == "valid" {
				got := readMutationFixture(t, path)
				raw, _ := json.Marshal(got)
				before, _ := json.Marshal(state)
				if string(raw) != string(before) {
					t.Fatal("journal mutated")
				}
			}
		})
	}
}
