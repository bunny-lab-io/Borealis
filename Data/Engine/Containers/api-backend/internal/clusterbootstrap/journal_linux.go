package clusterbootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var (
	ErrMutationJournal    = errors.New("bootstrap mutation journal unavailable or inconsistent")
	ErrMutationBusy       = errors.New("another bootstrap executor holds the host mutation lock")
	ErrMutationUnknown    = errors.New("bootstrap mutation outcome requires read-only reconciliation")
	ErrMutationApplied    = errors.New("bootstrap mutation already recorded; verify current target state before continuing")
	ErrMutationFreshClaim = errors.New("retry of a proven absent mutation requires a new worker claim")
)

const mutationJournalName = "mutation.json"
const maxMutationJournalBytes = 32 << 10

type mutationRecord struct {
	Binding      SessionBinding `json:"binding"`
	InputSHA256  string         `json:"input_sha256"`
	ResultSHA256 string         `json:"result_sha256"`
	State        string         `json:"state"`
}

type mutationPastAttempt struct {
	Step   string         `json:"step"`
	Record mutationRecord `json:"record"`
}

type mutationState struct {
	Version int                       `json:"version"`
	Owner   SessionBinding            `json:"owner"`
	Source  Expected                  `json:"source"`
	Steps   map[string]mutationRecord `json:"steps"`
	History []mutationPastAttempt     `json:"history"`
}

// MutationJournal is a private, host-wide serialization and intent ledger.
// It does not select commands, approve takeover, or grant operation authority.
// Caller supplies a pre-existing private root and verifies live controller/
// target authority, actual host identity and quiescence through Check at every
// boundary. Quiescence includes proving no prior executor can still write;
// the lock alone does not prove this after process loss.
// The bootstrap CLI must not expose mutations until its fixed dispatcher and
// quiescence/reconciliation contracts are implemented.
type MutationJournal struct {
	mu     sync.Mutex
	root   *os.Root
	lock   *os.File
	state  mutationState
	check  func(context.Context) error
	failed bool
}

func stableMutationBinding(b SessionBinding) SessionBinding {
	b.HolderID, b.Generation, b.OperationAttempt = "", 0, 0
	return b
}

func validMutationStep(step string) bool {
	switch step {
	case "stage_source", "install_identity", "prepare_host", "join_cluster":
		return true
	}
	return false
}

func (s mutationState) valid() bool {
	if s.Version != 1 || s.Owner.Validate() != nil || s.Source.Validate() != nil || s.Steps == nil || s.History == nil || len(s.Steps) > 4 || len(s.History) > 16 {
		return false
	}
	pending := 0
	for step, r := range s.Steps {
		if !validMutationStep(step) || r.Binding.Validate() != nil || stableMutationBinding(r.Binding) != stableMutationBinding(s.Owner) ||
			r.Binding.Generation > s.Owner.Generation || r.Binding.OperationAttempt > s.Owner.OperationAttempt || !digestPattern.MatchString(r.InputSHA256) {
			return false
		}
		switch r.State {
		case "intent":
			if r.ResultSHA256 != "" {
				return false
			}
			pending++
		case "applied", "not_applied":
			if !digestPattern.MatchString(r.ResultSHA256) {
				return false
			}
		default:
			return false
		}
	}
	for _, prior := range s.History {
		r := prior.Record
		if !validMutationStep(prior.Step) {
			return false
		}
		if r.State != "not_applied" || r.Binding.Validate() != nil || stableMutationBinding(r.Binding) != stableMutationBinding(s.Owner) ||
			r.Binding.Generation >= s.Owner.Generation || r.Binding.OperationAttempt > s.Owner.OperationAttempt || !digestPattern.MatchString(r.InputSHA256) || !digestPattern.MatchString(r.ResultSHA256) {
			return false
		}
	}
	return pending <= 1
}

func mutationFileValid(file *os.File, maxSize int64) bool {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > maxSize {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1 && int(stat.Uid) == os.Geteuid()
}

// OpenMutationJournal never creates the root or follows symlinks. Production
// caller must supply its fixed root-owned mode0700 host journal directory.
// Tests use an isolated directory owned by the test process.
func OpenMutationJournal(ctx context.Context, path string, owner SessionBinding, source Expected, check func(context.Context) error) (*MutationJournal, error) {
	if owner.Validate() != nil || source.Validate() != nil || check == nil || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
		return nil, ErrSessionAuthority
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrMutationJournal
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil || abs != real {
		return nil, ErrMutationJournal
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, ErrMutationJournal
	}
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		root.Close()
		return nil, ErrMutationJournal
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		root.Close()
		return nil, ErrMutationJournal
	}
	lock, err := root.OpenFile("mutation.lock", os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err == nil {
		// Establish exact permissions only for a file this call created. Never
		// repair permissions on an existing foreign/unsafe lock file.
		if lock.Chmod(0o600) != nil {
			lock.Close()
			root.Close()
			return nil, ErrMutationJournal
		}
	} else if errors.Is(err, os.ErrExist) {
		lock, err = root.OpenFile("mutation.lock", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		root.Close()
		return nil, ErrMutationJournal
	}
	if !mutationFileValid(lock, 0) {
		lock.Close()
		root.Close()
		return nil, ErrMutationJournal
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		lock.Close()
		root.Close()
		return nil, ErrMutationBusy
	}
	j := &MutationJournal{root: root, lock: lock, check: check}
	fail := func(err error) (*MutationJournal, error) { j.Close(); return nil, err }
	file, err := root.OpenFile(mutationJournalName, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		j.state = mutationState{Version: 1, Owner: owner, Source: source, Steps: map[string]mutationRecord{}, History: []mutationPastAttempt{}}
	} else {
		if err != nil {
			return fail(ErrMutationJournal)
		}
		if !mutationFileValid(file, maxMutationJournalBytes) {
			file.Close()
			return fail(ErrMutationJournal)
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, maxMutationJournalBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(raw) > maxMutationJournalBytes || json.Unmarshal(raw, &j.state) != nil || !j.state.valid() {
			return fail(ErrMutationJournal)
		}
		canonical, err := json.Marshal(j.state)
		if err != nil || string(canonical) != string(raw) {
			return fail(ErrMutationJournal)
		}
		old := j.state.Owner
		if j.state.Source != source || stableMutationBinding(old) != stableMutationBinding(owner) || owner.Generation < old.Generation || owner.OperationAttempt < old.OperationAttempt ||
			(owner.Generation == old.Generation && (owner.HolderID != old.HolderID || owner.OperationAttempt != old.OperationAttempt)) {
			return fail(ErrSessionAuthority)
		}
		j.state.Owner = owner
	}
	if err := j.persist(ctx); err != nil {
		return fail(err)
	}
	return j, nil
}

func (j *MutationJournal) authority(ctx context.Context) error {
	if j == nil || j.root == nil || j.failed || ctx.Err() != nil || j.check(ctx) != nil || ctx.Err() != nil {
		return ErrSessionAuthority
	}
	return nil
}

// persist uses a unique private temporary file, fsync, atomic rename and root
// directory fsync. An interrupted temporary is inert retained evidence; it
// cannot block the next owner or be mistaken for committed intent.
func (j *MutationJournal) persist(ctx context.Context) error {
	if j.authority(ctx) != nil {
		return ErrSessionAuthority
	}
	if !j.state.valid() {
		j.failed = true
		return ErrMutationJournal
	}
	raw, err := json.Marshal(j.state)
	if err != nil || len(raw) > maxMutationJournalBytes {
		j.failed = true
		return ErrMutationJournal
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		j.failed = true
		return ErrMutationJournal
	}
	temporary := "mutation-next-" + hex.EncodeToString(nonce[:])
	file, err := j.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		j.failed = true
		return ErrMutationJournal
	}
	defer j.root.Remove(temporary)
	if file.Chmod(0o600) != nil {
		file.Close()
		j.failed = true
		return ErrMutationJournal
	}
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		j.failed = true
		return ErrMutationJournal
	}
	if j.authority(ctx) != nil {
		j.failed = true
		return ErrSessionAuthority
	}
	if j.root.Rename(temporary, mutationJournalName) != nil {
		j.failed = true
		return ErrMutationJournal
	}
	directory, err := j.root.Open(".")
	if err != nil {
		j.failed = true
		return ErrMutationJournal
	}
	syncErr = directory.Sync()
	closeErr = directory.Close()
	if syncErr != nil || closeErr != nil {
		j.failed = true
		return ErrMutationJournal
	}
	return nil
}

// Apply runs only a fixed caller-owned mutation, synchronously while holding
// the host lock. Mutate must honor context and join all subprocesses before
// returning. Pass supplied lock file in each subprocess ExtraFiles. Never
// detach work or close this journal around an active child.
// Any error after durable intent remains outcome_unknown, even if launch was
// not acknowledged. Later calls cannot blindly replay it or skip past it.
func (j *MutationJournal) Apply(ctx context.Context, step, inputSHA256 string, mutate func(context.Context, *os.File) (string, error)) (string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.authority(ctx) != nil {
		return "", ErrSessionAuthority
	}
	if !validMutationStep(step) || !digestPattern.MatchString(inputSHA256) || mutate == nil {
		return "", ErrMutationJournal
	}
	if previous, ok := j.state.Steps[step]; ok {
		if previous.InputSHA256 != inputSHA256 {
			return "", ErrMutationJournal
		}
		if previous.State == "applied" {
			return "", ErrMutationApplied
		}
		if previous.State != "not_applied" {
			return "", ErrMutationUnknown
		}
		if j.state.Owner.Generation <= previous.Binding.Generation {
			return "", ErrMutationFreshClaim
		}
		if len(j.state.History) >= 16 {
			return "", ErrMutationJournal
		}
	}
	for previousStep, previous := range j.state.Steps {
		if previousStep != step && previous.State != "applied" {
			return "", ErrMutationUnknown
		}
	}
	if previous, ok := j.state.Steps[step]; ok {
		j.state.History = append(j.state.History, mutationPastAttempt{Step: step, Record: previous})
	}
	j.state.Steps[step] = mutationRecord{Binding: j.state.Owner, InputSHA256: inputSHA256, State: "intent"}
	if err := j.persist(ctx); err != nil {
		j.failed = true
		return "", err
	}
	if j.authority(ctx) != nil {
		return "", ErrMutationUnknown
	}
	// Pass a duplicate open-file description to every subprocess via ExtraFiles.
	// It retains the same flock if the parent dies. The fixed dispatcher must
	// also prove its process group/cgroup quiescent before absence reconciliation.
	syscall.ForkLock.RLock()
	descriptor, err := syscall.Dup(int(j.lock.Fd()))
	if err == nil {
		syscall.CloseOnExec(descriptor)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return "", ErrMutationUnknown
	}
	childLock := os.NewFile(uintptr(descriptor), "bootstrap-mutation-lock")
	defer childLock.Close()
	result, err := mutate(ctx, childLock)
	if err != nil || !digestPattern.MatchString(result) || j.authority(ctx) != nil {
		return "", ErrMutationUnknown
	}
	record := j.state.Steps[step]
	record.ResultSHA256, record.State = result, "applied"
	j.state.Steps[step] = record
	if j.persist(ctx) != nil {
		j.failed = true
		return "", ErrMutationUnknown
	}
	return result, nil
}

// Reconcile runs a read-only proof callback against actual target state while
// holding current authority and host lock. It may settle an uncertain intent,
// but cannot rewrite a definitive result. It never invokes a mutation callback.
func (j *MutationJournal) Reconcile(ctx context.Context, step, inputSHA256 string, prove func(context.Context) (string, error)) (string, error) {
	return j.reconcile(ctx, step, inputSHA256, prove, "applied")
}

// ReconcileAbsent requires a read-only proof that the exact requested effect
// did not occur. Only a later worker generation may retry; prior intent and
// absence proof remain in bounded history. A failed proof never enables replay.
func (j *MutationJournal) ReconcileAbsent(ctx context.Context, step, inputSHA256 string, prove func(context.Context) (string, error)) (string, error) {
	return j.reconcile(ctx, step, inputSHA256, prove, "not_applied")
}

func (j *MutationJournal) reconcile(ctx context.Context, step, inputSHA256 string, prove func(context.Context) (string, error), resolution string) (string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.authority(ctx) != nil {
		return "", ErrSessionAuthority
	}
	record, ok := j.state.Steps[step]
	if !ok || record.InputSHA256 != inputSHA256 || prove == nil {
		return "", ErrMutationJournal
	}
	result, err := prove(ctx)
	if err != nil || !digestPattern.MatchString(result) || j.authority(ctx) != nil {
		return "", ErrMutationUnknown
	}
	if record.State != "intent" {
		if record.State != resolution || record.ResultSHA256 != result {
			return "", ErrMutationJournal
		}
		return result, nil
	}
	record.State, record.ResultSHA256 = resolution, result
	j.state.Steps[step] = record
	if j.persist(ctx) != nil {
		j.failed = true
		return "", ErrMutationUnknown
	}
	return result, nil
}

// Close blocks until an active Apply/Reconcile callback has returned. It does
// not erase journal outcomes or authorize cleanup of installed target state.
func (j *MutationJournal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	var err error
	if j.lock != nil {
		err = j.lock.Close()
		j.lock = nil
	}
	if j.root != nil {
		err = errors.Join(err, j.root.Close())
		j.root = nil
	}
	return err
}
