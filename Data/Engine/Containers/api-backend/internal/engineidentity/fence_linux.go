package engineidentity

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

const recoveryMarker = ".borealis-identity-recovery.json"

// Consumer holds a shared Linux flock on the secrets directory inode, including
// through read-only bind mounts. Keep it alive until ALL key users have exited.
// Never replace the directory: identity transactions replace only fixed files.
type Consumer struct {
	root      *os.Root
	directory *os.File
	path      string
}

func openIdentityFence(path string, exclusive bool) (*Consumer, error) {
	root, err := openIdentityRoot(path)
	if err != nil {
		return nil, err
	}
	directory, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, errors.New("cannot open identity fence")
	}
	abs, _ := filepath.Abs(path)
	fence := &Consumer{root: root, directory: directory, path: abs}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(directory.Fd()), mode|syscall.LOCK_NB); err != nil {
		fence.Close()
		return nil, errors.New("identity consumers or recovery still active; keep target stopped and retry")
	}
	if err := fence.check(); err != nil {
		fence.Close()
		return nil, err
	}
	return fence, nil
}

func (fence *Consumer) check() error {
	if fence == nil || fence.directory == nil {
		return errors.New("identity fence is closed")
	}
	held, err := fence.directory.Stat()
	if err != nil || !held.IsDir() || held.Mode().Perm()&0o022 != 0 {
		return errors.New("identity directory must not be group/world writable")
	}
	real, err := filepath.EvalSymlinks(fence.path)
	if err != nil || real != fence.path {
		return errors.New("identity directory path changed")
	}
	current, err := os.Stat(fence.path)
	if err != nil || !os.SameFile(held, current) {
		return errors.New("identity directory inode changed")
	}
	return nil
}

// Close is for a fully stopped consumer, not a request or shutdown timeout.
// Production entrypoints retain their handle until kernel process teardown.
func (fence *Consumer) Close() error {
	if fence == nil || fence.directory == nil {
		return nil
	}
	err := fence.directory.Close()
	fence.directory = nil
	return errors.Join(err, fence.root.Close())
}

// AcquireConsumer must precede every startup key read/generation. A marker of
// any shape blocks startup; a crashed executor must not expose partial keys.
func AcquireConsumer(path string) (*Consumer, error) {
	fence, err := openIdentityFence(path, false)
	if err != nil {
		return nil, err
	}
	if _, err := fence.root.Lstat(recoveryMarker); !errors.Is(err, os.ErrNotExist) {
		fence.Close()
		return nil, errors.New("identity recovery incomplete; retain journal and keep consumers stopped")
	}
	return fence, nil
}

type recoveryFenceRecord struct {
	Version  int           `json:"version"`
	Binding  Binding       `json:"binding"`
	Target   TargetBinding `json:"target"`
	Journal  string        `json:"journal"`
	Incoming string        `json:"incoming_digest"`
}

// InstallFenced wraps the existing transaction for ONE joined-peer attempt.
// The original controller's supervised executor/MutationJournal must still own
// the host, verify the target and immutable guard-capable release, stop legacy
// consumers/writers, and supply independent Check/Quiesced evidence. A flock
// alone proves nothing about old binaries that never acquired a shared lock.
// Failure/rollback retains the marker: only verified commit under current
// authority permits startup. No automatic takeover or marker-removal API.
func (install Installation) InstallFenced(material *Material) (string, error) {
	return install.installFenced(material, nil)
}

// InstallFencedJournaled retains the exclusive consumer lock and restart marker
// until commit records the outer operation durably. commit must synchronously
// invoke the supplied transaction exactly once, persist its successful result,
// and return an error on uncertainty. It must not authorize or resume consumers.
// Failure at either journal boundary retains the marker for explicit recovery.
func (install Installation) InstallFencedJournaled(material *Material, commit func(func() (string, error)) error) (string, error) {
	if commit == nil {
		return "", errors.New("identity operation journal required")
	}
	return install.installFenced(material, commit)
}

func (install Installation) installFenced(material *Material, commit func(func() (string, error)) error) (string, error) {
	if !material.valid() || !validBinding(install.Binding) || !install.Target.valid(install.Binding) ||
		install.Target.NodeUID == "" || install.UID < 0 || install.GID < 0 || install.Check == nil || install.Quiesced == nil {
		return "", errors.New("joined identity recovery contract invalid")
	}
	if err := install.Check(); err != nil {
		return "", err
	}
	if err := install.Quiesced(); err != nil {
		return "", err
	}
	fence, err := openIdentityFence(install.Root, true)
	if err != nil {
		return "", err
	}
	defer fence.Close()
	info, err := fence.directory.Stat()
	if err != nil || int(info.Sys().(*syscall.Stat_t).Uid) != install.UID {
		return "", errors.New("identity directory owner differs from target runtime owner")
	}
	journal, err := filepath.Abs(install.Journal)
	if err != nil || len(journal) > 1024 {
		return "", errors.New("identity journal path invalid")
	}
	if err := install.Check(); err != nil {
		return "", err
	}
	if err := install.Quiesced(); err != nil {
		return "", err
	}
	if err := fence.check(); err != nil {
		return "", err
	}
	record := recoveryFenceRecord{1, install.Binding, install.Target, journal, material.Digest()}
	raw, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	previous, metadata, err := readIdentityFile(fence.root, recoveryMarker)
	if err != nil {
		return "", err
	}
	if metadata.Present {
		// Exact encoding is intentional: this private marker is not an input API.
		if string(previous) != string(raw) || metadata.UID != install.UID || metadata.GID != install.GID {
			return "", errors.New("identity recovery marker belongs to another attempt or changed")
		}
		// A prior attempt may have failed during fsync, before any key write.
		// Re-establish durability even when the retained bytes already match.
		file, err := fence.root.OpenFile(recoveryMarker, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return "", errors.New("cannot reopen identity recovery marker")
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(syncErr, closeErr, syncDirectory(fence.root, ".")); err != nil {
			return "", errors.New("identity recovery marker persistence failed; keep target stopped")
		}
	} else {
		// Create in place, not rename: even a torn/empty marker fails startup closed.
		file, err := fence.root.OpenFile(recoveryMarker, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			return "", errors.New("cannot establish identity recovery marker")
		}
		_, writeErr := file.Write(raw)
		ownerErr := file.Chown(install.UID, install.GID)
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(writeErr, ownerErr, syncErr, closeErr, syncDirectory(fence.root, ".")); err != nil {
			return "", errors.New("identity recovery marker persistence failed; keep target stopped")
		}
	}
	checkFence := func() error {
		if err := fence.check(); err != nil {
			return err
		}
		info, err := fence.directory.Stat()
		if err != nil || int(info.Sys().(*syscall.Stat_t).Uid) != install.UID {
			return errors.New("identity directory owner changed")
		}
		observed, meta, err := readIdentityFile(fence.root, recoveryMarker)
		if err != nil || !meta.Present || string(observed) != string(raw) || meta.UID != install.UID || meta.GID != install.GID {
			return errors.New("identity recovery marker changed; keep target stopped")
		}
		return nil
	}
	check, quiesced := install.Check, install.Quiesced
	install.Check = func() error {
		if err := checkFence(); err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		return checkFence()
	}
	install.Quiesced = func() error {
		if err := checkFence(); err != nil {
			return err
		}
		if err := quiesced(); err != nil {
			return err
		}
		return checkFence()
	}
	var state string
	if commit == nil {
		state, err = install.Install(material)
	} else {
		calls := 0
		var transactionErr error
		commitErr := commit(func() (string, error) {
			calls++
			if calls != 1 {
				return "", errors.New("identity transaction cannot be replayed")
			}
			state, transactionErr = install.Install(material)
			return state, transactionErr
		})
		if calls != 1 {
			return state, errors.Join(commitErr, errors.New("identity operation journal did not execute exactly one transaction"))
		}
		err = errors.Join(commitErr, transactionErr)
	}
	if err != nil || (state != "committed" && state != "already_committed") {
		return state, err
	}
	if err := install.Check(); err != nil {
		return state, err
	}
	if err := install.Quiesced(); err != nil {
		return state, err
	}
	current, err := Load(install.Root)
	if err != nil || !material.Equal(current) {
		return state, errors.New("committed identity changed; keep target stopped")
	}
	// File verification can block on storage; do not release on authority that
	// was current only before those reads.
	if err := install.Check(); err != nil {
		return state, err
	}
	if err := fence.root.Remove(recoveryMarker); err != nil {
		return state, err
	}
	return state, syncDirectory(fence.root, ".")
}
