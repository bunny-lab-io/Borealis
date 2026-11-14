package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func mutationTempDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func mutationFixture() (SessionBinding, Expected) {
	r := sessionFixture()
	return r.Binding, Expected{Repository: r.Repository, Release: r.Release, SourceSHA: r.SourceSHA, AllowQualification: true}
}

// Portable ledger tests inject executor observations. Production has no observer override.
func openTestMutationJournal(ctx context.Context, root string, owner SessionBinding, source Expected, check func(context.Context) error) (*MutationJournal, error) {
	identity := testMutationExecutor(mutationDigest(root + time.Now().String()))
	return openMutationJournal(ctx, root, owner, source, check, func(context.Context) (ExecutorIdentity, error) { return identity, nil }, func(context.Context, ExecutorIdentity) error { return nil })
}
func testMutationExecutor(nonce string) ExecutorIdentity {
	unit, _ := ExecutorUnit(nonce)
	return ExecutorIdentity{Unit: unit, InvocationID: nonce[:32], BootID: "11111111-1111-4111-8111-111111111111", ControlGroup: "/system.slice/" + unit}
}
func mutationAuthority(context.Context) error { return nil }
func mutationDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
func readMutationFixture(t *testing.T, root string) mutationState {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, mutationJournalName))
	if err != nil {
		t.Fatal(err)
	}
	var s mutationState
	if json.Unmarshal(raw, &s) != nil || !s.valid() {
		t.Fatal("invalid retained journal")
	}
	return s
}
func nextMutationOwner(owner SessionBinding) SessionBinding {
	owner.Generation++
	owner.HolderID = "55555555-5555-4555-8555-555555555555"
	return owner
}

func TestMutationJournalPersistsIntentAndNeverReplays(t *testing.T) {
	root := mutationTempDir(t)
	owner, source := mutationFixture()
	ctx := context.Background()
	j, err := openTestMutationJournal(ctx, root, owner, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	input, result := mutationDigest("source input"), mutationDigest("verified effect")
	calls := 0
	mutate := func(context.Context, *os.File) (string, error) {
		calls++
		state := readMutationFixture(t, root)
		if r := state.Steps["stage_source"]; r.State != "intent" || r.InputSHA256 != input || r.Binding != owner {
			t.Fatal("mutation started before bound intent persisted")
		}
		return result, nil
	}
	if got, err := j.Apply(ctx, "stage_source", input, mutate); err != nil || got != result {
		t.Fatal(err)
	}
	if _, err := j.Apply(ctx, "stage_source", input, mutate); err != ErrMutationApplied || calls != 1 {
		t.Fatal("definitive step replayed")
	}
	j.Close()
	j, err = openTestMutationJournal(ctx, root, owner, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Reconcile(ctx, "stage_source", input, func(context.Context) (string, error) { return mutationDigest("different effect"), nil }); err != ErrMutationJournal {
		t.Fatal("definitive outcome overwritten")
	}
	if j.Close() != nil {
		t.Fatal("journal close failed")
	}
	j, err = openTestMutationJournal(ctx, root, nextMutationOwner(owner), source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if _, err := j.Apply(ctx, "stage_source", input, mutate); err != ErrMutationApplied || calls != 1 {
		t.Fatal("new owner replayed definitive step")
	}
	if got, err := j.Reconcile(ctx, "stage_source", input, func(context.Context) (string, error) { return result, nil }); err != nil || got != result {
		t.Fatal("verified result lost")
	}
	if _, err := j.Apply(ctx, "arbitrary_command", input, mutate); err != ErrMutationJournal {
		t.Fatal("unknown step accepted")
	}
}

func TestMutationJournalUnknownRequiresProofAcrossHandoff(t *testing.T) {
	for _, mode := range []string{"callback error", "authority lost", "invalid result", "cancelled context"} {
		t.Run(mode, func(t *testing.T) {
			root := mutationTempDir(t)
			owner, source := mutationFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var lost atomic.Bool
			check := func(context.Context) error {
				if lost.Load() {
					return errors.New("private authority diagnostic")
				}
				return nil
			}
			j, err := openTestMutationJournal(ctx, root, owner, source, check)
			if err != nil {
				t.Fatal(err)
			}
			input, result := mutationDigest("input"), mutationDigest("effect")
			calls := 0
			_, err = j.Apply(ctx, "install_identity", input, func(context.Context, *os.File) (string, error) {
				calls++
				switch mode {
				case "callback error":
					return "", errors.New("private mutation diagnostic")
				case "authority lost":
					lost.Store(true)
				case "invalid result":
					return "raw output", nil
				case "cancelled context":
					cancel()
				}
				return result, nil
			})
			if err != ErrMutationUnknown {
				t.Fatalf("uncertain/private result escaped: %v", err)
			}
			if readMutationFixture(t, root).Steps["install_identity"].State != "intent" {
				t.Fatal("uncertain work lost intent")
			}
			j.Close()
			current := nextMutationOwner(owner)
			current.OperationAttempt++
			j, err = openTestMutationJournal(context.Background(), root, current, source, mutationAuthority)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			mutate := func(context.Context, *os.File) (string, error) { calls++; return result, nil }
			if _, err := j.Apply(context.Background(), "install_identity", input, mutate); err != ErrMutationUnknown || calls != 1 {
				t.Fatal("unknown work replayed")
			}
			if _, err := j.Apply(context.Background(), "join_cluster", input, mutate); err != ErrMutationUnknown || calls != 1 {
				t.Fatal("pending intent skipped")
			}
			if _, err := j.Reconcile(context.Background(), "install_identity", input, func(context.Context) (string, error) { return "", errors.New("private proof diagnostic") }); err != ErrMutationUnknown {
				t.Fatal("failed proof accepted")
			}
			if got, err := j.Reconcile(context.Background(), "install_identity", input, func(context.Context) (string, error) { return result, nil }); err != nil || got != result {
				t.Fatal("new owner could not settle proven outcome")
			}
			if readMutationFixture(t, root).Steps["install_identity"].Binding != owner {
				t.Fatal("reconciliation relabeled producing owner")
			}
		})
	}
}

func TestMutationJournalLocksThroughCallbackAndRejectsStaleOwners(t *testing.T) {
	root := mutationTempDir(t)
	owner, source := mutationFixture()
	ctx := context.Background()
	j, err := openTestMutationJournal(ctx, root, owner, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := openTestMutationJournal(ctx, root, nextMutationOwner(owner), source, mutationAuthority); err != ErrMutationBusy {
		if other != nil {
			other.Close()
		}
		t.Fatal("overlapping host owner accepted")
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := j.Apply(ctx, "prepare_host", mutationDigest("input"), func(context.Context, *os.File) (string, error) {
			close(started)
			<-release
			return mutationDigest("effect"), nil
		})
		done <- err
	}()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- j.Close() }()
	select {
	case <-closed:
		t.Fatal("Close released lock with active mutation")
	case <-time.After(20 * time.Millisecond):
	}
	if other, err := openTestMutationJournal(ctx, root, nextMutationOwner(owner), source, mutationAuthority); err != ErrMutationBusy {
		if other != nil {
			other.Close()
		}
		t.Fatal("replacement overlapped active callback")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	current := nextMutationOwner(owner)
	j, err = openTestMutationJournal(ctx, root, current, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	j.Close()
	for _, mode := range []string{"generation", "same generation new holder", "attempt", "source", "cluster", "target", "address", "machine", "host key"} {
		t.Run(mode, func(t *testing.T) {
			stale := current
			changedSource := source
			switch mode {
			case "generation":
				stale.Generation--
			case "same generation new holder":
				stale.HolderID = owner.HolderID
			case "attempt":
				stale.OperationAttempt--
			case "source":
				changedSource.SourceSHA = strings.Repeat("b", 40)
			case "cluster":
				stale.ClusterID = owner.TargetID
			case "target":
				stale.TargetID = owner.ClusterID
			case "address":
				stale.Address = "192.168.3.250"
			case "machine":
				stale.MachineID = strings.Repeat("b", 32)
			case "host key":
				stale.HostKeyAlgorithm = "ssh-rsa"
			}
			previous, _ := os.ReadFile(filepath.Join(root, mutationJournalName))
			other, err := openTestMutationJournal(ctx, root, stale, changedSource, mutationAuthority)
			if err != ErrSessionAuthority {
				if other != nil {
					other.Close()
				}
				t.Fatalf("stale journal accepted: %v", err)
			}
			after, _ := os.ReadFile(filepath.Join(root, mutationJournalName))
			if string(previous) != string(after) {
				t.Fatal("rejected owner rewrote journal")
			}
		})
	}
}

func TestMutationJournalRejectsUnsafeStorage(t *testing.T) {
	for _, mode := range []string{"root permissions", "root symlink", "lock symlink", "lock hardlink", "lock FIFO", "journal symlink", "journal hardlink", "journal FIFO", "journal permissions", "oversized", "unknown field", "duplicate field", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			root := mutationTempDir(t)
			owner, source := mutationFixture()
			ctx := context.Background()
			j, err := openTestMutationJournal(ctx, root, owner, source, mutationAuthority)
			if err != nil {
				t.Fatal(err)
			}
			j.Close()
			journal := filepath.Join(root, mutationJournalName)
			lock := filepath.Join(root, "mutation.lock")
			raw, _ := os.ReadFile(journal)
			switch mode {
			case "root permissions":
				err = os.Chmod(root, 0o755)
			case "root symlink":
				link := filepath.Join(t.TempDir(), "alias")
				err = os.Symlink(root, link)
				root = link
			case "lock symlink", "journal symlink", "lock hardlink", "journal hardlink", "lock FIFO", "journal FIFO":
				path := journal
				if strings.HasPrefix(mode, "lock") {
					path = lock
				}
				old := filepath.Join(t.TempDir(), "retained")
				if err = os.Rename(path, old); err != nil {
					t.Fatal(err)
				}
				switch {
				case strings.HasSuffix(mode, "symlink"):
					err = os.Symlink(old, path)
				case strings.HasSuffix(mode, "hardlink"):
					err = os.Link(old, path)
					if err == nil {
						err = os.Link(old, old+"-second")
					}
				case strings.HasSuffix(mode, "FIFO"):
					err = syscall.Mkfifo(path, 0o600)
				}
			case "journal permissions":
				err = os.Chmod(journal, 0o644)
			case "oversized":
				err = os.WriteFile(journal, []byte(strings.Repeat("x", maxMutationJournalBytes+1)), 0o600)
			case "unknown field":
				err = os.WriteFile(journal, []byte(strings.Replace(string(raw), `"version":2`, `"extra":true,"version":2`, 1)), 0o600)
			case "duplicate field":
				err = os.WriteFile(journal, []byte(strings.Replace(string(raw), `"version":2`, `"version":2,"version":2`, 1)), 0o600)
			case "malformed":
				err = os.WriteFile(journal, []byte(`{"version":`), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			other, err := openTestMutationJournal(ctx, root, nextMutationOwner(owner), source, mutationAuthority)
			if err == nil {
				other.Close()
				t.Fatal("unsafe journal path/data accepted")
			}
		})
	}
}

func TestMutationJournalProcessCrash(t *testing.T) {
	owner, source := mutationFixture()
	input := mutationDigest("input")
	effect := []byte("verified source stage")
	if root := os.Getenv("BOREALIS_TEST_MUTATION_CRASH_ROOT"); root != "" {
		j, err := openTestMutationJournal(context.Background(), root, owner, source, mutationAuthority)
		if err != nil {
			os.Exit(90)
		}
		_, _ = j.Apply(context.Background(), "stage_source", input, func(context.Context, *os.File) (string, error) {
			if os.WriteFile(filepath.Join(root, "effect"), effect, 0o600) != nil {
				os.Exit(91)
			}
			os.Exit(44)
			return "", nil
		})
		os.Exit(92)
	}
	root := mutationTempDir(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestMutationJournalProcessCrash$")
	command.Env = append(os.Environ(), "BOREALIS_TEST_MUTATION_CRASH_ROOT="+root)
	err = command.Run()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 44 {
		t.Fatalf("crash fixture failed: %v", err)
	}
	j, err := openTestMutationJournal(context.Background(), root, nextMutationOwner(owner), source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	called := false
	if _, err := j.Apply(context.Background(), "stage_source", input, func(context.Context, *os.File) (string, error) {
		called = true
		return mutationDigest(string(effect)), nil
	}); err != ErrMutationUnknown || called {
		t.Fatal("process death caused blind replay")
	}
	got, err := j.Reconcile(context.Background(), "stage_source", input, func(context.Context) (string, error) {
		observed, err := os.ReadFile(filepath.Join(root, "effect"))
		if err != nil || string(observed) != string(effect) {
			return "", ErrMutationUnknown
		}
		return mutationDigest(string(observed)), nil
	})
	if err != nil || got != mutationDigest(string(effect)) {
		t.Fatal("surviving effect could not be reconciled")
	}
}

func TestMutationJournalProvenAbsenceNeedsFreshClaimAndRetainsHistory(t *testing.T) {
	root := mutationTempDir(t)
	owner, source := mutationFixture()
	ctx := context.Background()
	j, err := openTestMutationJournal(ctx, root, owner, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	input, absent, result := mutationDigest("input"), mutationDigest("exact effect absent"), mutationDigest("effect applied")
	calls := 0
	mutate := func(context.Context, *os.File) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("launch not acknowledged")
		}
		return result, nil
	}
	if _, err := j.Apply(ctx, "stage_source", input, mutate); err != ErrMutationUnknown {
		t.Fatal(err)
	}
	if _, err := j.ReconcileAbsent(ctx, "stage_source", input, func(context.Context) (string, error) { return "", errors.New("cannot prove absence") }); err != ErrMutationUnknown {
		t.Fatal("uncertain absence became retry permission")
	}
	if _, err := j.Apply(ctx, "stage_source", input, mutate); err != ErrMutationUnknown || calls != 1 {
		t.Fatal("failed proof enabled replay")
	}
	j.Close()
	j, err = openTestMutationJournal(ctx, root, owner, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.ReconcileAbsent(ctx, "stage_source", input, func(context.Context) (string, error) { return absent, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Apply(ctx, "stage_source", input, mutate); err != ErrMutationFreshClaim || calls != 1 {
		t.Fatal("same worker retried after absence proof")
	}
	if _, err := j.Apply(ctx, "prepare_host", input, mutate); err != ErrMutationUnknown || calls != 1 {
		t.Fatal("known missing effect skipped")
	}
	j.Close()
	j, err = openTestMutationJournal(ctx, root, nextMutationOwner(owner), source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if _, err := j.Apply(ctx, "stage_source", mutationDigest("changed input"), mutate); err != ErrMutationJournal || calls != 1 {
		t.Fatal("absence proof authorized different input")
	}
	if got, err := j.Apply(ctx, "stage_source", input, mutate); err != nil || got != result || calls != 2 {
		t.Fatal("proven absence could not resume under fresh owner")
	}
	state := readMutationFixture(t, root)
	if len(state.History) != 1 || state.History[0].Step != "stage_source" || state.History[0].Record.Binding != owner || state.History[0].Record.State != "not_applied" || state.History[0].Record.ResultSHA256 != absent {
		t.Fatal("prior attempt/absence proof lost")
	}
	if _, err := j.ReconcileAbsent(ctx, "stage_source", input, func(context.Context) (string, error) { return absent, nil }); err != ErrMutationJournal {
		t.Fatal("definitive applied result rewritten as absent")
	}
}

func TestMutationJournalAuthorityCancellationDuringCheckStopsBeforeMutation(t *testing.T) {
	root := mutationTempDir(t)
	owner, source := mutationFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := false
	check := func(context.Context) error {
		if stop {
			cancel()
		}
		return nil
	}
	j, err := openTestMutationJournal(ctx, root, owner, source, check)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	stop = true
	called := false
	if _, err := j.Apply(ctx, "stage_source", mutationDigest("input"), func(context.Context, *os.File) (string, error) { called = true; return mutationDigest("effect"), nil }); err != ErrSessionAuthority || called {
		t.Fatal("authority callback cancellation did not fence mutation")
	}
	if len(readMutationFixture(t, root).Steps) != 0 {
		t.Fatal("cancelled authority persisted intent")
	}
}

func TestMutationJournalChildRetainsHostLock(t *testing.T) {
	root := mutationTempDir(t)
	owner, source := mutationFixture()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	j, err := openTestMutationJournal(ctx, root, owner, source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "sh", "-c", "read -r waiting_for_test_cleanup")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	started := false
	t.Cleanup(func() {
		input.Close()
		if started {
			command.Wait()
		}
		j.Close()
	})
	_, err = j.Apply(ctx, "prepare_host", mutationDigest("input"), func(_ context.Context, lock *os.File) (string, error) {
		command.ExtraFiles = []*os.File{lock}
		if err := command.Start(); err != nil {
			return "", err
		}
		started = true
		// Deliberately simulate parent executor disappearing before child exit.
		// Production callbacks must join children; descriptor inheritance also
		// preserves the lock when abrupt process death prevents that join.
		return "", errors.New("simulated parent loss")
	})
	if err != ErrMutationUnknown || !started {
		t.Fatal("child lock fixture failed")
	}
	j.Close()
	if other, err := openTestMutationJournal(ctx, root, nextMutationOwner(owner), source, mutationAuthority); err != ErrMutationBusy {
		if other != nil {
			other.Close()
		}
		t.Fatal("live child lost inherited host lock")
	}
	input.Close()
	_ = command.Wait()
	started = false
	replacement, err := openTestMutationJournal(ctx, root, nextMutationOwner(owner), source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if _, err := replacement.Apply(ctx, "prepare_host", mutationDigest("input"), func(context.Context, *os.File) (string, error) {
		t.Fatal("unacknowledged child work replayed")
		return "", nil
	}); err != ErrMutationUnknown {
		t.Fatal("child exit erased uncertain intent")
	}
}

func TestMutationJournalRestrictiveUmask(t *testing.T) {
	owner, source := mutationFixture()
	if root := os.Getenv("BOREALIS_TEST_MUTATION_UMASK_ROOT"); root != "" {
		syscall.Umask(0o777)
		j, err := openTestMutationJournal(context.Background(), root, owner, source, mutationAuthority)
		if err != nil {
			os.Exit(90)
		}
		_, err = j.Apply(context.Background(), "stage_source", mutationDigest("input"), func(context.Context, *os.File) (string, error) { return mutationDigest("effect"), nil })
		if err != nil {
			os.Exit(91)
		}
		j.Close()
		os.Exit(0)
	}
	root := mutationTempDir(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestMutationJournalRestrictiveUmask$")
	command.Env = append(os.Environ(), "BOREALIS_TEST_MUTATION_UMASK_ROOT="+root)
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mutation.lock", mutationJournalName} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("umask changed required private file permissions")
		}
	}
	j, err := openTestMutationJournal(context.Background(), root, nextMutationOwner(owner), source, mutationAuthority)
	if err != nil {
		t.Fatal(err)
	}
	j.Close()
}

func TestMutationJournalStorageFailureCannotLaunchOrAcknowledge(t *testing.T) {
	for _, afterEffect := range []bool{false, true} {
		name := "before intent"
		if afterEffect {
			name = "after effect"
		}
		t.Run(name, func(t *testing.T) {
			root := mutationTempDir(t)
			owner, source := mutationFixture()
			ctx := context.Background()
			j, err := openTestMutationJournal(ctx, root, owner, source, mutationAuthority)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			// Closing the directory descriptor injects a real filesystem failure
			// without altering retained evidence or depending on test-user privilege.
			if !afterEffect {
				if err := j.root.Close(); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			input := mutationDigest("input")
			mutate := func(context.Context, *os.File) (string, error) {
				calls++
				if err := j.root.Close(); err != nil {
					t.Fatal(err)
				}
				return mutationDigest("effect"), nil
			}
			got, err := j.Apply(ctx, "prepare_host", input, mutate)
			wantErr, wantCalls := ErrMutationJournal, 0
			if afterEffect {
				wantErr, wantCalls = ErrMutationUnknown, 1
			}
			if err != wantErr || got != "" || calls != wantCalls {
				t.Fatal("failed persistence launched or acknowledged mutation")
			}
			if _, err := j.Apply(ctx, "prepare_host", input, mutate); err != ErrSessionAuthority || calls != wantCalls {
				t.Fatal("failed journal handle remained usable")
			}
			j.Close()
			state := readMutationFixture(t, root)
			if afterEffect {
				if r := state.Steps["prepare_host"]; r.State != "intent" || r.ResultSHA256 != "" || r.Binding != owner {
					t.Fatal("failed acknowledgement overwrote uncertain intent")
				}
			} else if len(state.Steps) != 0 {
				t.Fatal("failed intent was recorded as committed")
			}
			replacement, err := openTestMutationJournal(ctx, root, nextMutationOwner(owner), source, mutationAuthority)
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			if afterEffect {
				if _, err := replacement.Apply(ctx, "prepare_host", input, mutate); err != ErrMutationUnknown || calls != 1 {
					t.Fatal("handoff replayed effect after failed acknowledgement")
				}
			}
		})
	}
}

func TestMutationJournalRetryHistoryCannotBeDiscardedAtLimit(t *testing.T) {
	root := mutationTempDir(t)
	owner, source := mutationFixture()
	ctx := context.Background()
	input, absent := mutationDigest("input"), mutationDigest("verified absence")
	calls := 0
	for attempt := 0; attempt < 18; attempt++ {
		j, err := openTestMutationJournal(ctx, root, owner, source, mutationAuthority)
		if err != nil {
			t.Fatal(err)
		}
		defer j.Close()
		previous, err := os.ReadFile(filepath.Join(root, mutationJournalName))
		if err != nil {
			t.Fatal(err)
		}
		_, err = j.Apply(ctx, "join_cluster", input, func(context.Context, *os.File) (string, error) {
			calls++
			return "", errors.New("unacknowledged execution")
		})
		if attempt == 17 {
			if err != ErrMutationJournal || calls != 17 {
				t.Fatal("retry discarded bounded history to launch again")
			}
			after, err := os.ReadFile(filepath.Join(root, mutationJournalName))
			if err != nil || string(previous) != string(after) {
				t.Fatal("history limit rewrote retained attempts")
			}
		} else {
			if err != ErrMutationUnknown {
				t.Fatal(err)
			}
			j.Close()
			j, err = openTestMutationJournal(ctx, root, owner, source, mutationAuthority)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := j.ReconcileAbsent(ctx, "join_cluster", input, func(context.Context) (string, error) { return absent, nil }); err != nil {
				t.Fatal(err)
			}
		}
		j.Close()
		owner = nextMutationOwner(owner)
	}
	state := readMutationFixture(t, root)
	if len(state.History) != 16 || state.Steps["join_cluster"].State != "not_applied" {
		t.Fatal("bounded retry evidence lost")
	}
}
