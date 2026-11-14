package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
)

func prejoinHostRepositoryDigest(input string) string {
	sum := sha256.Sum256([]byte("prejoin-host-clean-repository-v1\x00" + input))
	return hex.EncodeToString(sum[:])
}

// Repository proof requires configured base packages, an exact owned repository
// prefix and absent Docker/generated host state. Only the fixed native adapter
// can complete this class; existing bytes are never labeled absent.
func (p *PreparationInputs) ResumeRepositoryPrejoinHost(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, actions PrejoinHostActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.resumeRepositoryPrejoinHost(ctx, j, install, material, actions, "/opt")
}

func (p *PreparationInputs) resumeRepositoryPrejoinHost(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, actions PrejoinHostActions, parent string) (string, error) {
	if actions.CheckFresh == nil || actions.Prepare == nil || actions.Prove == nil {
		return "", ErrMutationJournal
	}
	return p.prejoinHostTransaction(ctx, j, install, material, actions, parent, false, actions.CheckFresh, hostContinuationRepository)
}

func (j *MutationJournal) continueRepositoryPrejoinHostLocked(ctx context.Context, input string, repository func(context.Context) error, prepare func(context.Context, *os.File) (string, error)) (string, error) {
	return j.continuePrejoinHostClassLocked(ctx, input, prejoinHostRepositoryDigest(input), repository, prepare)
}
