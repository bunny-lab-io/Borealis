package engineidentity

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// InstallPrejoinFencedJournaledRetained is only for a qualified, never-serving
// target before Kubernetes creates its Node UID. Provisioning UUID and approved
// SSH key bind this distinct lifecycle. Existing identity or a retained marker
// is not a fresh target and cannot be overwritten through this method.
//
// Caller must hold the supervised host journal and repeatedly prove clean-host
// ownership, no installed/active legacy consumers, fixed mount custody and
// original source/controller authority. Empty files or a flock alone prove none
// of those facts. Both journals commit while exclusive consumer lock is held;
// the restart marker remains until a separate proof-based release.
func (install Installation) InstallPrejoinFencedJournaledRetained(material *Material, commit func(func() (string, error)) error) (string, error) {
	if commit == nil {
		return "", errors.New("pre-join identity operation journal required")
	}
	return install.installFencedMode(material, commit, true, true, false)
}

// PrejoinPreparationMarker returns the private, exact startup-blocking marker
// for an independently journaled fresh-directory publication. The caller must
// publish it atomically with the new directory, after runtime-account custody
// and source/target proof. Encoding grants no mutation authority. Never log it.
func (install Installation) PrejoinPreparationMarker(material *Material) ([]byte, error) {
	if !material.valid() || !validBinding(install.Binding) || !install.Target.valid(install.Binding) || install.Binding.SourceID == install.Target.ProvisioningID || install.Target.NodeUID != "" || install.UID < 0 || install.GID < 0 ||
		!filepath.IsAbs(install.Root) || filepath.Clean(install.Root) != install.Root || !filepath.IsAbs(install.Journal) || filepath.Clean(install.Journal) != install.Journal || len(install.Journal) > 1024 ||
		install.Journal == install.Root || strings.HasPrefix(install.Journal, install.Root+string(filepath.Separator)) {
		return nil, errors.New("pre-join directory marker contract invalid")
	}
	return json.Marshal(recoveryFenceRecord{1, install.Binding, install.Target, install.Journal, material.Digest()})
}

// InstallPreparedPrejoinFencedJournaledRetained consumes only the exact marker
// published by the separate host-journal-owned directory step. Existing keys,
// an inner transaction, a changed marker or a joined UID still reject. Caller
// must independently prove that directory receipt and current custody; marker
// possession alone grants no authority. Normal pre-join entry stays marker-free.
func (install Installation) InstallPreparedPrejoinFencedJournaledRetained(material *Material, commit func(func() (string, error)) error) (string, error) {
	if commit == nil {
		return "", errors.New("prepared pre-join operation journal required")
	}
	if _, err := install.PrejoinPreparationMarker(material); err != nil {
		return "", err
	}
	return install.installFencedMode(material, commit, true, true, true)
}

// ProvePrejoinFencedCommitted proves the same empty-NodeUID transaction and
// originally absent fixed-file inventory. It does not reinterpret a joined
// transaction or bind a newly assigned Node UID into the retained journal.
func (install Installation) ProvePrejoinFencedCommitted(material *Material) error {
	proof, err := install.proveFencedCommittedMode(material, true, true)
	if err != nil {
		return err
	}
	return proof.close()
}

// WithPrejoinFencedCommitted retains exclusive identity exclusion throughout
// one synchronous host-preparation operation. It grants no host mutation
// authority: caller still owns durable intent, supervised execution and actual
// prepared-state proof. Pass lock through ExtraFiles to every child and join
// them before returning. Callback must not close or unlock the borrowed handle.
// Child inheritance keeps exclusion if parent dies.
// Neither normal completion nor error releases the startup marker.
func (install Installation) WithPrejoinFencedCommitted(material *Material, work func(*os.File) error) (result error) {
	if work == nil {
		return errors.New("pre-join fenced work required")
	}
	proof, err := install.proveFencedCommittedMode(material, true, true)
	if err != nil {
		return err
	}
	defer func() {
		// Also runs on panic: restore only this exact marker if unexpectedly
		// removed while original directory remains pinned. Never overwrite a
		// foreign marker or write through a replaced directory.
		if err := proof.fence.check(); err != nil {
			result = errors.Join(result, err)
		} else {
			result = errors.Join(result, restoreRecoveryMarker(proof.fence.root, proof.marker, install.UID, install.GID))
		}
		result = errors.Join(result, proof.close())
	}()
	syscall.ForkLock.RLock()
	descriptor, err := syscall.Dup(int(proof.fence.directory.Fd()))
	if err == nil {
		syscall.CloseOnExec(descriptor)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return errors.New("pre-join inherited exclusion unavailable")
	}
	lock := os.NewFile(uintptr(descriptor), "prejoin-identity-lock")
	defer func() { result = errors.Join(result, lock.Close()) }()
	workErr := work(lock)
	proofErr := install.recheckCommittedFence(material, proof, true)
	if workErr != nil || proofErr != nil {
		return errors.New("pre-join fenced operation incomplete; retain journal and keep consumers stopped")
	}
	return nil
}

// ReleasePrejoinFencedCommitted removes only the exact committed restart marker.
// Call from a distinct supervised outer-journal executor after source authority,
// inherited configuration and stopped-consumer prerequisites are proved. It
// starts no service, creates no database/admin/Aegis identity and grants neither
// membership admission nor traffic eligibility. Later Node UID is recorded by
// admission separately; this journal must remain unchanged.
func (install Installation) ReleasePrejoinFencedCommitted(material *Material) (string, error) {
	return install.releaseFencedCommittedMode(material, true)
}

// WithPrejoinActivationFencedCommitted extends the existing committed release
// proof through the caller's durable activation journal and final observation.
// allowAbsent is only for proof-only reconciliation of a recorded activation
// whose marker is already absent, by a distinct executor after independently
// proving the producer stopped. The callback must establish all
// live prerequisites before calling release and must join its children while
// inheriting the borrowed lock. It may remove only this exact marker once.
// Failure or panic restores the marker before exclusion ends, provided the
// original directory is still pinned. No identity file or inner journal changes.
func (install Installation) WithPrejoinActivationFencedCommitted(material *Material, allowAbsent bool, work func(*os.File, bool, func() error) error) (result error) {
	if work == nil {
		return errors.New("pre-join activation operation journal required")
	}
	proof, err := install.proveFencedCommittedMode(material, !allowAbsent, true)
	if err != nil {
		return err
	}
	if allowAbsent && proof.markerPresent {
		return errors.Join(errors.New("pre-join activation marker remains; no removal replay"), proof.close())
	}
	completed := false
	defer func() {
		if !completed || result != nil {
			if err := proof.fence.check(); err != nil {
				result = errors.Join(result, err)
			} else {
				result = errors.Join(result, restoreRecoveryMarker(proof.fence.root, proof.marker, install.UID, install.GID))
			}
		}
		result = errors.Join(result, proof.close())
	}()
	syscall.ForkLock.RLock()
	descriptor, err := syscall.Dup(int(proof.fence.directory.Fd()))
	if err == nil {
		syscall.CloseOnExec(descriptor)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return errors.New("pre-join activation exclusion unavailable")
	}
	lock := os.NewFile(uintptr(descriptor), "prejoin-activation-lock")
	defer func() { result = errors.Join(result, lock.Close()) }()
	released, releaseFailed := false, false
	release := func() error {
		if released || releaseFailed {
			releaseFailed = true
			return errors.New("pre-join activation release repeated")
		}
		releaseFailed = true
		if err := install.recheckCommittedFence(material, proof, true); err != nil {
			return err
		}
		if proof.markerPresent {
			if err := proof.fence.root.Remove(recoveryMarker); err != nil {
				return err
			}
			proof.markerPresent = false
			if err := syncDirectory(proof.fence.root, "."); err != nil {
				return err
			}
		}
		if err := install.recheckCommittedFence(material, proof, true); err != nil {
			return err
		}
		released, releaseFailed = true, false
		return nil
	}
	if err := work(lock, proof.markerPresent, release); err != nil {
		return err
	}
	if !released || releaseFailed || install.recheckCommittedFence(material, proof, true) != nil {
		return errors.New("pre-join activation incomplete; keep consumers stopped")
	}
	completed = true
	return nil
}
