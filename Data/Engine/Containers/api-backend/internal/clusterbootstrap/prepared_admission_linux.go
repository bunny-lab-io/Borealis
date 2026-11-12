package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// WithPreparedAdmissionJournal reads the existing complete journal under its
// existing exclusive host lock. It never creates a file, takes over a claim or
// rewrites a journal. Settlement alone cannot authorize an action: callers own
// current operation authority and independent live host/identity observations.
func WithPreparedAdmissionJournal(ctx context.Context, path string, request PreparedAdmissionRequest, consume func(func(context.Context) error) error) (resultErr error) {
	encoded, err := EncodePreparedAdmissionRequest(request)
	if err != nil || consume == nil || ctx.Err() != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrMutationJournal
	}
	// Freeze slices/pointers before callbacks can mutate the caller's request.
	request, err = DecodePreparedAdmissionRequest(encoded)
	if err != nil {
		return ErrMutationJournal
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path {
		return ErrMutationJournal
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return ErrMutationJournal
	}
	defer func() {
		if root.Close() != nil || ctx.Err() != nil {
			resultErr = ErrMutationJournal
		}
	}()
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return ErrMutationJournal
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return ErrMutationJournal
	}
	lock, err := root.OpenFile("mutation.lock", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ErrMutationJournal
	}
	defer func() {
		if lock.Close() != nil {
			resultErr = ErrMutationJournal
		}
	}()
	if !mutationFileValid(lock, 0) {
		return ErrMutationJournal
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return ErrMutationBusy
	}
	lockInfo, err := lock.Stat()
	if err != nil {
		return ErrMutationJournal
	}
	var mu sync.Mutex
	closed, failed := false, false
	var original os.FileInfo
	check := func(current context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		fail := func() error { failed = true; return ErrMutationJournal }
		if closed || failed || ctx.Err() != nil || current.Err() != nil {
			return fail()
		}
		real, err := filepath.EvalSymlinks(path)
		now, statErr := os.Stat(path)
		currentLock, lockErr := root.Lstat("mutation.lock")
		if err != nil || real != path || statErr != nil || !os.SameFile(info, now) || now.Mode() != info.Mode() || lockErr != nil || !os.SameFile(lockInfo, currentLock) || !mutationFileValid(lock, 0) {
			return fail()
		}
		currentOwner, ok := now.Sys().(*syscall.Stat_t)
		if !ok || currentOwner.Uid != owner.Uid || currentOwner.Gid != owner.Gid {
			return fail()
		}
		file, err := root.OpenFile(mutationJournalName, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return fail()
		}
		if !mutationFileValid(file, maxMutationJournalBytes) {
			_ = file.Close()
			return fail()
		}
		fileInfo, statErr := file.Stat()
		raw, readErr := io.ReadAll(io.LimitReader(file, maxMutationJournalBytes+1))
		closeErr := file.Close()
		var state mutationState
		if statErr != nil || readErr != nil || closeErr != nil || len(raw) > maxMutationJournalBytes || json.Unmarshal(raw, &state) != nil || !state.valid() {
			return fail()
		}
		canonical, err := json.Marshal(state)
		if err != nil || !bytes.Equal(raw, canonical) || state.Owner != request.Expected.Target || state.Source != request.Expected.Source || len(state.Steps) != len(request.Receipt.Steps) {
			return fail()
		}
		for _, receipt := range request.Receipt.Steps {
			step, ok := state.Steps[receipt.Step]
			if !ok || step.State != "applied" || step.InputSHA256 != receipt.InputSHA256 || step.ResultSHA256 != receipt.ResultSHA256 {
				return fail()
			}
		}
		if original != nil && (!os.SameFile(original, fileInfo) || !original.ModTime().Equal(fileInfo.ModTime())) {
			return fail()
		}
		original = fileInfo
		return nil
	}
	defer func() { mu.Lock(); closed = true; mu.Unlock() }()
	if check(ctx) != nil {
		return ErrMutationJournal
	}
	err = consume(check)
	if final := check(ctx); final != nil {
		return errors.Join(err, final)
	}
	return err
}
