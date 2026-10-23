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

// Caller holds j.mu. The digest binds exact private domain input alongside the
// enclosing journal's immutable release, approved SSH target and owner claim.
// No key bytes enter the outer ledger or public result.
func (j *MutationJournal) joinedIdentityRequest(ctx context.Context, install engineidentity.Installation, material *engineidentity.Material) (engineidentity.Installation, string, error) {
	if material == nil || material.Digest() == "" || install.Check == nil || install.Quiesced == nil ||
		install.Target.NodeUID == "" || install.Binding.ClusterID != j.state.Owner.ClusterID ||
		install.Target.ProvisioningID != j.state.Owner.TargetID || install.Target.SSHFingerprint != j.state.Owner.HostKeyFingerprint {
		return install, "", ErrMutationJournal
	}
	root, err := filepath.Abs(install.Root)
	if err != nil {
		return install, "", ErrMutationJournal
	}
	journal, err := filepath.Abs(install.Journal)
	if err != nil {
		return install, "", ErrMutationJournal
	}
	install.Root, install.Journal = root, journal
	raw, err := json.Marshal(struct {
		Binding                 engineidentity.Binding
		Target                  engineidentity.TargetBinding
		Root, Journal, Material string
		UID, GID                int
	}{install.Binding, install.Target, root, journal, material.Digest(), install.UID, install.GID})
	if err != nil {
		return install, "", ErrMutationJournal
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
	return install, hex.EncodeToString(digest[:]), nil
}

// InstallJoinedIdentity composes the existing host/executor journal with the
// identity transaction. The caller still supplies independently verified
// original-source identity, joined Node UID, guard-capable release, stopped
// legacy consumers and stable mount evidence through Check/Quiesced. This is
// not authority inferred from an SSH login/Pod inventory. Successful writes
// retain the restart marker; a distinct supervised executor must explicitly
// release it after re-proving both journals. Unknown/applied prior attempts
// require explicit reconciliation, never replay.
func (j *MutationJournal) InstallJoinedIdentity(ctx context.Context, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	if j == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.authority(ctx) != nil {
		return "", ErrSessionAuthority
	}
	install, inputSHA256, err := j.joinedIdentityRequest(ctx, install, material)
	if err != nil {
		return "", err
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
	return install.InstallFencedJournaledRetained(material, func(transaction func() (string, error)) error {
		_, err := j.applyLocked(ctx, "install_identity", inputSHA256, func(context.Context, *os.File) (string, error) {
			state, err := transaction()
			if err != nil || (state != "committed" && state != "already_committed") {
				return "", ErrMutationUnknown
			}
			return material.Digest(), nil
		})
		return err
	})
}

// ReconcileJoinedIdentity proves an uncertain identity effect from retained
// marker, committed inner journal and exact fixed files. It records only the
// outer result. Marker removal and consumer restart require a later authorized
// release operation and are deliberately outside reconciliation.
func (j *MutationJournal) ReconcileJoinedIdentity(ctx context.Context, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	if j == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.authority(ctx) != nil {
		return "", ErrSessionAuthority
	}
	install, inputSHA256, err := j.joinedIdentityRequest(ctx, install, material)
	if err != nil {
		return "", err
	}
	return j.reconcileLocked(ctx, "install_identity", inputSHA256, func(context.Context) (string, error) {
		if err := install.ProveFencedCommitted(material); err != nil {
			return "", err
		}
		return material.Digest(), nil
	}, "applied")
}

// ReleaseJoinedIdentity removes the restart marker only after the exact outer
// install result is durably applied and every prior supervised executor remains
// quiescent. It does not start consumers or expose a remote mutation command.
func (j *MutationJournal) ReleaseJoinedIdentity(ctx context.Context, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	if j == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.authority(ctx) != nil {
		return "", ErrSessionAuthority
	}
	install, inputSHA256, err := j.joinedIdentityRequest(ctx, install, material)
	if err != nil {
		return "", err
	}
	record, ok := j.state.Steps["install_identity"]
	if !ok || record.InputSHA256 != inputSHA256 {
		return "", ErrMutationJournal
	}
	if record.State != "applied" {
		return "", ErrMutationUnknown
	}
	if record.ResultSHA256 != material.Digest() {
		return "", ErrMutationJournal
	}
	// Marker release always requires a distinct executor that can independently
	// prove the producer dead; successful installation deliberately retains it.
	if record.Executor == j.executor {
		return "", ErrMutationUnknown
	}
	if err := j.priorExecutorsQuiescent(ctx); err != nil {
		return "", err
	}
	quiesced := install.Quiesced
	install.Quiesced = func() error {
		if err := j.priorExecutorsQuiescent(ctx); err != nil {
			return err
		}
		if err := quiesced(); err != nil {
			return err
		}
		return j.priorExecutorsQuiescent(ctx)
	}
	state, err := install.ReleaseFencedCommitted(material)
	if err != nil {
		return "", ErrMutationUnknown
	}
	return state, nil
}
