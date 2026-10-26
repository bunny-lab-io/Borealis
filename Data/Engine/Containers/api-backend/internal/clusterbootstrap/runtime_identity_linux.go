package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// RuntimeIdentityActions is a fixed native account adapter, not a command from
// the wire. Prepare must use only the named Borealis UID/GID, join every child
// and pass the inherited host lock to each process. Prove checks exact local
// account custody and absent UID consumers without creating/changing anything.
type RuntimeIdentityActions struct {
	Prepare func(context.Context, *os.File) error
	Prove   func(context.Context) error
}

func (p *PreparationInputs) PrepareRuntimeIdentity(ctx context.Context, j *MutationJournal, actions RuntimeIdentityActions) (string, error) {
	if os.Geteuid() != 0 {
		return "", ErrMutationJournal
	}
	return p.prepareRuntimeIdentity(ctx, j, actions, "/opt", PreparationRuntimeID, PreparationRuntimeID, false)
}

func (p *PreparationInputs) ReconcileRuntimeIdentity(ctx context.Context, j *MutationJournal, prove func(context.Context) error) (string, error) {
	if os.Geteuid() != 0 {
		return "", ErrMutationJournal
	}
	return p.prepareRuntimeIdentity(ctx, j, RuntimeIdentityActions{Prove: prove}, "/opt", PreparationRuntimeID, PreparationRuntimeID, true)
}

func (p *PreparationInputs) runtimeIdentityInput(j *MutationJournal, uid, gid int) (string, error) {
	source, err := p.sourcePublicationInputWithIdentityRoot(j, true)
	if err != nil || uid < 0 || gid < 0 {
		return "", ErrMutationJournal
	}
	record := j.state.Steps["stage_source"]
	if record.State != "applied" || record.InputSHA256 != source || record.ResultSHA256 != source {
		return "", ErrMutationJournal
	}
	raw, err := json.Marshal(struct {
		Domain, Source, Name string
		UID, GID             int
	}{"fresh-runtime-identity-v1", source, "borealis-engine", uid, gid})
	if err != nil {
		return "", ErrMutationJournal
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (p *PreparationInputs) prepareRuntimeIdentity(ctx context.Context, j *MutationJournal, actions RuntimeIdentityActions, parent string, uid, gid int, reconcile bool) (string, error) {
	if j == nil || actions.Prove == nil || !reconcile && actions.Prepare == nil {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	input, err := p.runtimeIdentityInput(j, uid, gid)
	if err != nil {
		return "", err
	}
	for step := range j.state.Steps {
		if step != "stage_source" && step != "prepare_runtime_identity" {
			return "", ErrMutationJournal
		}
	}
	if p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil {
		return "", ErrSessionAuthority
	}
	proveSource := func(ctx context.Context) error {
		archive, err := os.Open(p.bundle.ArchivePath())
		if err != nil {
			return ErrPreparationConfig
		}
		defer archive.Close()
		environment, err := p.configuration.Environment()
		if err != nil {
			return err
		}
		defer clear(environment)
		if verifySourceArchive(ctx, archive, p.bundle.asset) != nil || verifyPublishedSource(ctx, archive, filepath.Join(parent, "Borealis"), environment) != nil || verifySourceArchive(ctx, archive, p.bundle.asset) != nil || j.authority(ctx) != nil {
			return ErrPreparationConfig
		}
		return nil
	}
	if proveSource(ctx) != nil {
		return "", ErrPreparationConfig
	}
	prove := func(ctx context.Context) (string, error) {
		if j.authority(ctx) != nil || actions.Prove(ctx) != nil || proveSource(ctx) != nil || actions.Prove(ctx) != nil || j.authority(ctx) != nil {
			return "", ErrSessionAuthority
		}
		return input, nil
	}
	if reconcile {
		return j.reconcileLocked(ctx, "prepare_runtime_identity", input, prove, "applied")
	}
	return j.applyLocked(ctx, "prepare_runtime_identity", input, func(ctx context.Context, lock *os.File) (string, error) {
		if actions.Prepare(ctx, lock) != nil || j.authority(ctx) != nil {
			return "", ErrSessionAuthority
		}
		return prove(ctx)
	})
}
