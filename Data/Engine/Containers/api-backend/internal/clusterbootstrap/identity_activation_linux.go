package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"os"
)

// ActivatePrejoinIdentity removes only the committed startup marker after all
// ten preparation stages and current original-source, private configuration,
// identity, Node/member and complete image proof. Both exclusions cover durable
// intent, marker release, outer completion and final proof. It starts no
// consumer, clears no drain/taint and grants no controller admission authority.
func (p *PreparationInputs) ActivatePrejoinIdentity(ctx context.Context, j *MutationJournal, install engineidentity.Installation, identity *engineidentity.Material,
	artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, material *K3sJoinMaterial, actions PreparedImageVerificationActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.prejoinArtifactPhaseTransactionWithActivation(ctx, j, install, identity, artifacts, postgresReference, proveHost, "/opt", "/", false, nil, nil, nil,
		&prejoinImageVerificationPhase{material, actions, K3sPins()}, true)
}

// ReconcilePrejoinIdentityActivation requires a retained exact activation
// record and a distinct executor that independently proves all prior producers
// stopped. It never removes a retained/restored marker or repeats activation.
// Missing marker alone never proves successful activation.
func (p *PreparationInputs) ReconcilePrejoinIdentityActivation(ctx context.Context, j *MutationJournal, install engineidentity.Installation, identity *engineidentity.Material,
	artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, material *K3sJoinMaterial, actions PreparedImageVerificationActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.prejoinArtifactPhaseTransactionWithActivation(ctx, j, install, identity, artifacts, postgresReference, proveHost, "/opt", "/", true, nil, nil, nil,
		&prejoinImageVerificationPhase{material, actions, K3sPins()}, true)
}
