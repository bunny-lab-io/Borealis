package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// InstallJoinedIdentity composes the existing host/executor journal with the
// identity transaction. The caller still supplies independently verified
// original-source identity, joined Node UID, guard-capable release, stopped
// legacy consumers and stable mount evidence through Check/Quiesced. This is
// not a CLI dispatch or authority inferred from an SSH login/Pod inventory.
// Unknown/applied prior attempts require explicit reconciliation, never replay.
func (j *MutationJournal) InstallJoinedIdentity(ctx context.Context, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	if j == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.authority(ctx) != nil {
		return "", ErrSessionAuthority
	}
	if material == nil || material.Digest() == "" || install.Check == nil || install.Quiesced == nil ||
		install.Target.NodeUID == "" || install.Binding.ClusterID != j.state.Owner.ClusterID ||
		install.Target.ProvisioningID != j.state.Owner.TargetID || install.Target.SSHFingerprint != j.state.Owner.HostKeyFingerprint {
		return "", ErrMutationJournal
	}
	if prior, exists := j.state.Steps["install_identity"]; exists {
		if prior.State == "applied" {
			return "", ErrMutationApplied
		}
		return "", ErrMutationUnknown
	}
	for _, prior := range j.state.Steps {
		if prior.State != "applied" {
			return "", ErrMutationUnknown
		}
	}
	if err := j.priorExecutorsQuiescent(ctx); err != nil {
		return "", err
	}
	root, err := filepath.Abs(install.Root)
	if err != nil {
		return "", ErrMutationJournal
	}
	journal, err := filepath.Abs(install.Journal)
	if err != nil {
		return "", ErrMutationJournal
	}
	install.Root, install.Journal = root, journal
	// Private input digest binds exact domain request as well as the enclosing
	// journal's immutable release, SSH target and controller claim. No key bytes
	// enter the outer ledger or public result.
	raw, err := json.Marshal(struct {
		Binding                 engineidentity.Binding
		Target                  engineidentity.TargetBinding
		Root, Journal, Material string
		UID, GID                int
	}{install.Binding, install.Target, root, journal, material.Digest(), install.UID, install.GID})
	if err != nil {
		return "", ErrMutationJournal
	}
	digest := sha256.Sum256(raw)
	check := install.Check
	install.Check = func() error {
		if err := j.authority(ctx); err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		return j.authority(ctx)
	}
	return install.InstallFencedJournaled(material, func(transaction func() (string, error)) error {
		_, err := j.applyLocked(ctx, "install_identity", hex.EncodeToString(digest[:]), func(context.Context, *os.File) (string, error) {
			state, err := transaction()
			if err != nil || (state != "committed" && state != "already_committed") {
				return "", ErrMutationUnknown
			}
			return material.Digest(), nil
		})
		return err
	})
}
