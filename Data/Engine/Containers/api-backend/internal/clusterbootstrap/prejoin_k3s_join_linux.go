package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"os"
)

// Configuration is separate from installation/startup. All callbacks retain
// inactive K3s, complete installed-service proof and both borrowed exclusions.
// Configure may publish only fixed token/config files; it must start no child,
// enable no service, import nothing and retain the shared identity marker.
type PrejoinK3sJoinActions struct {
	Installed  func(context.Context) error
	CheckFresh func(context.Context) error
	Configure  func(context.Context, *os.File, *os.File) error
	Prove      func(context.Context) error
}

type prejoinK3sJoinPhase struct {
	material *K3sJoinMaterial
	actions  PrejoinK3sJoinActions
}

func (p *PreparationInputs) ConfigurePrejoinK3s(ctx context.Context, j *MutationJournal, install engineidentity.Installation, identity *engineidentity.Material,
	artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, material *K3sJoinMaterial, actions PrejoinK3sJoinActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, identity, artifacts, postgresReference, proveHost, "/opt", "/", false, nil, &prejoinK3sJoinPhase{material, actions})
}

func (p *PreparationInputs) ReconcilePrejoinK3sConfiguration(ctx context.Context, j *MutationJournal, install engineidentity.Installation, identity *engineidentity.Material,
	artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, material *K3sJoinMaterial, actions PrejoinK3sJoinActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, identity, artifacts, postgresReference, proveHost, "/opt", "/", true, nil, &prejoinK3sJoinPhase{material, actions})
}

func (m *K3sJoinMaterial) inputDigest(ctx context.Context, expected PreparationExpected, source engineidentity.Binding, check func(context.Context) error) (string, error) {
	if m.Verify(ctx, expected, source, check) != nil {
		return "", ErrK3sJoinMaterial
	}
	m.owned.mu.Lock()
	defer m.owned.mu.Unlock()
	if m.owned.closed {
		return "", ErrK3sJoinMaterial
	}
	return m.owned.digest, nil
}
