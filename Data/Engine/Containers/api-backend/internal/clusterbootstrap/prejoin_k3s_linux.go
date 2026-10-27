package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"os"
)

// Fixed native installation adapter. Install publishes the pinned server unit
// and empty environment without replacement, then reloads systemd under both
// borrowed fences. It must never enable/start K3s, import images, join a cluster
// or release identity consumers. Prove independently checks exact files and
// loaded, disabled, inactive runtime with no jobs/drop-ins/consumer processes.
type PrejoinK3sActions struct {
	CheckFresh func(context.Context) error
	Install    func(context.Context, *os.File, *os.File) error
	Prove      func(context.Context) error
}

func (p *PreparationInputs) InstallPrejoinK3s(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, actions PrejoinK3sActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.prejoinArtifactPhaseTransaction(ctx, j, install, material, artifacts, postgresReference, proveHost, "/opt", "/", false, &actions)
}

// Separate stopped executor, complete read-only proof, no installation replay.
func (p *PreparationInputs) ReconcilePrejoinK3s(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, proveHost, proveRuntime func(context.Context) error) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	actions := PrejoinK3sActions{Prove: proveRuntime}
	return p.prejoinArtifactPhaseTransaction(ctx, j, install, material, artifacts, postgresReference, proveHost, "/opt", "/", true, &actions)
}
