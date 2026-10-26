package engineidentity

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// Load reads a stable copy of all fixed identity material through this
// consumer's already locked directory handle. It never generates, exports or
// persists material. Callers must keep the Consumer alive for every use.
func (fence *Consumer) Load() (*Material, error) {
	markerAbsent := func() bool {
		_, err := fence.root.Lstat(recoveryMarker)
		return errors.Is(err, os.ErrNotExist)
	}
	if err := fence.check(); err != nil || !markerAbsent() {
		return nil, errors.New("identity source fence invalid")
	}
	first, err := loadIdentityRoot(fence.root)
	if err != nil {
		return nil, errors.New("fixed Engine identity unavailable")
	}
	second, err := loadIdentityRoot(fence.root)
	if err != nil || !first.Equal(second) || fence.check() != nil || !markerAbsent() {
		return nil, errors.New("fixed Engine identity changed during acquisition")
	}
	return first, nil
}

// LoadFrom binds an existing consumer to the exact reviewed mount path. It
// neither opens another directory nor creates a second consumer. Native host
// and Pod/mount proof remains the caller's separate responsibility.
func (fence *Consumer) LoadFrom(path string) (*Material, error) {
	if fence == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || fence.path != path {
		return nil, errors.New("identity source directory does not match consumer")
	}
	return fence.Load()
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
// Failure/rollback retains the marker. Normal success releases it in this call;
// recovered attempts require the separate outer-journal-owned release path.
func (install Installation) InstallFenced(material *Material) (string, error) {
	return install.installFenced(material, nil, false)
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
	return install.installFenced(material, commit, false)
}

// InstallFencedJournaledRetained keeps the exact restart marker after both
// journals commit. A later supervised executor must re-prove the committed
// state through ReleaseFencedCommitted before any consumer can start.
func (install Installation) InstallFencedJournaledRetained(material *Material, commit func(func() (string, error)) error) (string, error) {
	if commit == nil {
		return "", errors.New("identity operation journal required")
	}
	return install.installFenced(material, commit, true)
}

type committedFenceProof struct {
	fence         *Consumer
	marker        []byte
	markerPresent bool
}

func (proof *committedFenceProof) close() error {
	if proof == nil || proof.fence == nil {
		return nil
	}
	return proof.fence.Close()
}

func (install Installation) proveFencedCommitted(material *Material, markerRequired bool) (_ *committedFenceProof, resultErr error) {
	return install.proveFencedCommittedMode(material, markerRequired, false)
}

func (install Installation) proveFencedCommittedMode(material *Material, markerRequired, prejoin bool) (_ *committedFenceProof, resultErr error) {
	if !material.valid() || !validBinding(install.Binding) || !install.Target.valid(install.Binding) ||
		(install.Target.NodeUID == "") != prejoin || install.UID < 0 || install.GID < 0 || install.Check == nil || install.Quiesced == nil {
		return nil, errors.New("identity lifecycle proof contract invalid")
	}
	if err := install.Check(); err != nil {
		return nil, err
	}
	if err := install.Quiesced(); err != nil {
		return nil, err
	}
	fence, err := openIdentityFence(install.Root, true)
	if err != nil {
		return nil, err
	}
	proof := &committedFenceProof{fence: fence}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, proof.close())
		}
	}()
	info, err := fence.directory.Stat()
	if err != nil {
		return nil, errors.New("identity directory owner differs from target runtime owner")
	}
	directoryOwner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(directoryOwner.Uid) != install.UID {
		return nil, errors.New("identity directory owner differs from target runtime owner")
	}
	rootPath, err := filepath.Abs(install.Root)
	if err != nil {
		return nil, errors.New("identity directory invalid")
	}
	journalPath, err := filepath.Abs(install.Journal)
	if err != nil || journalPath == rootPath || strings.HasPrefix(journalPath, rootPath+string(filepath.Separator)) || len(journalPath) > 1024 {
		return nil, errors.New("identity journal path invalid")
	}
	proof.marker, err = json.Marshal(recoveryFenceRecord{1, install.Binding, install.Target, journalPath, material.Digest()})
	if err != nil {
		return nil, err
	}
	observed, metadata, err := readIdentityFile(fence.root, recoveryMarker)
	if err != nil || markerRequired && !metadata.Present {
		return nil, errors.New("identity recovery marker does not prove requested attempt")
	}
	proof.markerPresent = metadata.Present
	if proof.markerPresent && (metadata.UID != install.UID || metadata.GID != install.GID || !bytes.Equal(observed, proof.marker)) {
		return nil, errors.New("identity recovery marker does not prove requested attempt")
	}
	checkFence := func() error {
		if err := fence.check(); err != nil {
			return err
		}
		info, err := fence.directory.Stat()
		if err != nil {
			return errors.New("identity directory owner changed")
		}
		directoryOwner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(directoryOwner.Uid) != install.UID {
			return errors.New("identity directory owner changed")
		}
		observed, metadata, err := readIdentityFile(fence.root, recoveryMarker)
		if err != nil || metadata.Present != proof.markerPresent ||
			metadata.Present && (metadata.UID != install.UID || metadata.GID != install.GID || !bytes.Equal(observed, proof.marker)) {
			return errors.New("identity recovery marker does not prove requested attempt")
		}
		return nil
	}
	if err := checkFence(); err != nil {
		return nil, err
	}
	if err := install.Check(); err != nil {
		return nil, err
	}
	if err := install.Quiesced(); err != nil {
		return nil, err
	}
	backup, err := openIdentityRoot(journalPath)
	if err != nil {
		return nil, errors.New("identity recovery journal unavailable")
	}
	defer backup.Close()
	journalInfo, err := backup.Stat(".")
	if err != nil || !journalInfo.IsDir() || journalInfo.Mode().Perm() != 0o700 {
		return nil, errors.New("identity recovery journal ownership invalid")
	}
	journalOwner, ok := journalInfo.Sys().(*syscall.Stat_t)
	if !ok || int(journalOwner.Uid) != os.Geteuid() {
		return nil, errors.New("identity recovery journal ownership invalid")
	}
	raw, metadata, err := readIdentityFile(backup, "journal.json")
	if err != nil || !metadata.Present || metadata.UID != os.Geteuid() || metadata.GID != os.Getegid() {
		return nil, errors.New("identity recovery journal record invalid")
	}
	var journal installationJournal
	if json.Unmarshal(raw, &journal) != nil || journal.Version != 2 || journal.Binding != install.Binding ||
		journal.Target != install.Target || journal.Incoming != material.Digest() || journal.State != "committed" || len(journal.Files) != len(identityPaths) {
		return nil, errors.New("identity recovery journal does not prove committed request")
	}
	canonical, err := json.Marshal(journal)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, errors.New("identity recovery journal encoding invalid")
	}
	for _, name := range identityPaths {
		if previous, ok := journal.Files[name]; !ok || prejoin && previous != (fileRecord{}) {
			return nil, errors.New("identity recovery journal inventory incomplete")
		}
	}
	files := make(map[string][]byte, len(identityPaths))
	for _, name := range identityPaths {
		raw, metadata, err := readIdentityFile(fence.root, name)
		if err != nil || !metadata.Present || metadata.UID != install.UID || metadata.GID != install.GID {
			return nil, errors.New("committed identity files changed")
		}
		files[name] = raw
	}
	current, err := ParseFiles(files)
	if err != nil || !material.Equal(current) {
		return nil, errors.New("committed identity files changed")
	}
	if err := checkFence(); err != nil {
		return nil, err
	}
	if err := install.Quiesced(); err != nil {
		return nil, err
	}
	if err := install.Check(); err != nil {
		return nil, err
	}
	if err := checkFence(); err != nil {
		return nil, err
	}
	return proof, nil
}

// ProveFencedCommitted reads an exact retained recovery attempt without
// changing its files, journals or marker. It requires exclusive consumer
// exclusion and caller-supplied authority/quiescence throughout. Success proves
// only this committed identity effect; it never authorizes consumer restart.
func (install Installation) ProveFencedCommitted(material *Material) error {
	proof, err := install.proveFencedCommitted(material, true)
	if err != nil {
		return err
	}
	return proof.close()
}

func restoreRecoveryMarker(root *os.Root, marker []byte, uid, gid int) error {
	observed, metadata, err := readIdentityFile(root, recoveryMarker)
	if err != nil {
		return errors.New("cannot inspect identity recovery marker after failed release")
	}
	if metadata.Present {
		if metadata.UID != uid || metadata.GID != gid || !bytes.Equal(observed, marker) {
			return errors.New("identity recovery marker changed after failed release; keep target stopped")
		}
		file, err := root.OpenFile(recoveryMarker, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return errors.New("cannot reopen restored identity recovery marker; keep target stopped")
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if errors.Join(syncErr, closeErr, syncDirectory(root, ".")) != nil {
			return errors.New("identity recovery marker restoration failed; keep target stopped")
		}
		return nil
	}
	file, err := root.OpenFile(recoveryMarker, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return errors.New("cannot restore identity recovery marker; keep target stopped")
	}
	_, writeErr := file.Write(marker)
	ownerErr := file.Chown(uid, gid)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, ownerErr, syncErr, closeErr, syncDirectory(root, ".")); err != nil {
		return errors.New("identity recovery marker restoration failed; keep target stopped")
	}
	return nil
}

// ReleaseFencedCommitted removes only the exact retained restart marker after
// re-proving the committed identity under exclusive consumer exclusion. It is
// idempotent for an already absent marker, but never repairs identity or
// journals. Call only from the outer-journal release contract. Any failed
// post-unlink check restores and fsyncs the marker before consumer unlock.
func (install Installation) ReleaseFencedCommitted(material *Material) (state string, resultErr error) {
	return install.releaseFencedCommittedMode(material, false)
}

func (install Installation) releaseFencedCommittedMode(material *Material, prejoin bool) (state string, resultErr error) {
	proof, err := install.proveFencedCommittedMode(material, false, prejoin)
	if err != nil {
		return "", err
	}
	defer proof.close()
	if !proof.markerPresent {
		return "already_released", nil
	}
	removed := false
	defer func() {
		if resultErr != nil && removed {
			resultErr = errors.Join(resultErr, restoreRecoveryMarker(proof.fence.root, proof.marker, install.UID, install.GID))
		}
	}()
	if err := proof.fence.root.Remove(recoveryMarker); err != nil {
		return "", err
	}
	removed = true
	if err := syncDirectory(proof.fence.root, "."); err != nil {
		return "", err
	}
	checkReleased := func() error {
		if err := proof.fence.check(); err != nil {
			return err
		}
		info, err := proof.fence.directory.Stat()
		if err != nil {
			return errors.New("identity directory changed during release")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(owner.Uid) != install.UID {
			return errors.New("identity directory owner changed during release")
		}
		if _, metadata, err := readIdentityFile(proof.fence.root, recoveryMarker); err != nil || metadata.Present {
			return errors.New("identity recovery marker removal changed")
		}
		return nil
	}
	if err := checkReleased(); err != nil {
		return "", err
	}
	if err := install.Quiesced(); err != nil {
		return "", err
	}
	if err := install.Check(); err != nil {
		return "", err
	}
	if err := checkReleased(); err != nil {
		return "", err
	}
	return "released", nil
}

func (install Installation) installFenced(material *Material, commit func(func() (string, error)) error, retainMarker bool) (string, error) {
	return install.installFencedMode(material, commit, retainMarker, false)
}

func (install Installation) installFencedMode(material *Material, commit func(func() (string, error)) error, retainMarker, prejoin bool) (string, error) {
	if !material.valid() || !validBinding(install.Binding) || !install.Target.valid(install.Binding) ||
		(install.Target.NodeUID == "") != prejoin || install.UID < 0 || install.GID < 0 || install.Check == nil || install.Quiesced == nil {
		return "", errors.New("identity lifecycle installation contract invalid")
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
	if prejoin {
		if metadata.Present || commit == nil || !retainMarker {
			return "", errors.New("pre-join identity requires new retained journaled attempt")
		}
		for _, name := range identityPaths {
			if _, existing, err := readIdentityFile(fence.root, name); err != nil || existing.Present {
				return "", errors.New("pre-join target already contains identity; explicit recovery required")
			}
		}
		install.requireFresh = true
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
	if retainMarker {
		return state, syncDirectory(fence.root, ".")
	}
	if err := fence.root.Remove(recoveryMarker); err != nil {
		return state, err
	}
	return state, syncDirectory(fence.root, ".")
}
