package engineidentity

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
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

// ReleasePrejoinFencedCommitted removes only the exact committed restart marker.
// Call from a distinct supervised outer-journal executor after source authority,
// inherited configuration and stopped-consumer prerequisites are proved. It
// starts no service, creates no database/admin/Aegis identity and grants neither
// membership admission nor traffic eligibility. Later Node UID is recorded by
// admission separately; this journal must remain unchanged.
func (install Installation) ReleasePrejoinFencedCommitted(material *Material) (string, error) {
	return install.releaseFencedCommittedMode(material, true)
}
