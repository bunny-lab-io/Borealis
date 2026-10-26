package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"os"
	"path/filepath"
)

// ReconcilePreparedPrejoinIdentity settles only the exact already-committed
// inner effect. It proves original source/configuration, account/directory
// receipts, complete fixed identity tree and retained marker. No writes repeat
// and no marker is released; a different stopped-producer executor is required.
func (p *PreparationInputs) ReconcilePreparedPrejoinIdentity(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.reconcilePreparedPrejoinIdentity(ctx, j, install, material, "/opt")
}

func (p *PreparationInputs) reconcilePreparedPrejoinIdentity(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, parent string) (string, error) {
	if j == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	rootInput, proof, err := p.prejoinDirectoryRequestMode(j, install, material, parent, true)
	if err != nil {
		return "", err
	}
	root := j.state.Steps["prepare_identity_root"]
	if len(j.state.Steps) != 4 || root.State != "applied" || root.InputSHA256 != rootInput || root.ResultSHA256 != rootInput {
		return "", ErrMutationJournal
	}
	install, input, err := j.identityRequest(ctx, install, material, true)
	if err != nil {
		return "", err
	}
	proof.files = material.Files()
	if len(proof.files) != 4 {
		return "", ErrMutationJournal
	}
	defer func() {
		for _, raw := range proof.files {
			clear(raw)
		}
	}()
	if p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil {
		return "", ErrSessionAuthority
	}
	return j.reconcileLocked(ctx, "install_identity", input, func(ctx context.Context) (string, error) {
		archive, err := os.Open(p.bundle.ArchivePath())
		if err != nil {
			return "", ErrPreparationConfig
		}
		defer archive.Close()
		environment, err := p.configuration.Environment()
		if err != nil {
			return "", err
		}
		defer clear(environment)
		prove := func() error {
			if verifySourceArchive(ctx, archive, p.bundle.asset) != nil ||
				verifyPublishedSourceWithIdentityRoot(ctx, archive, filepath.Join(parent, "Borealis"), environment, &proof) != nil ||
				verifySourceArchive(ctx, archive, p.bundle.asset) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil {
				return ErrPreparationConfig
			}
			return nil
		}
		if prove() != nil || install.ProvePrejoinFencedCommitted(material) != nil || prove() != nil {
			return "", ErrMutationJournal
		}
		return material.Digest(), nil
	}, "applied")
}
