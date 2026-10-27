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

// PublishPrejoinArtifacts stages the complete immutable cohort without running
// K3s, its installer, image import or any identity consumer. The native host
// proof must independently recheck prepared packages/services/firewall while
// permitting only this phase's inert K3s files. Installation.Check retains
// current source, boot, mount, account, lease and producer custody throughout.
// postgresReference must come from independently fenced source evidence.
func (p *PreparationInputs) PublishPrejoinArtifacts(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.prejoinArtifactsTransaction(ctx, j, install, material, artifacts, postgresReference, proveHost, "/opt", "/", false)
}

// ReconcilePrejoinArtifacts reads all 37 files after prior producers stop. It
// never retries writes, removes interrupted files or declares partial effects
// absent. A complete independent proof is required before settling intent.
func (p *PreparationInputs) ReconcilePrejoinArtifacts(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.prejoinArtifactsTransaction(ctx, j, install, material, artifacts, postgresReference, proveHost, "/opt", "/", true)
}

func (p *PreparationInputs) prejoinArtifactsTransaction(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, parent, systemPath string, reconcile bool) (string, error) {
	if p == nil || j == nil || artifacts == nil || proveHost == nil || !ValidPostgresImageReference(postgresReference) {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	artifacts.mu.Lock()
	defer artifacts.mu.Unlock()
	install, hostInput, identity, err := p.prejoinHostPhaseRequest(ctx, j, install, material, parent, true)
	host := j.state.Steps["prepare_host"]
	if err != nil || host.State != "applied" || host.InputSHA256 != hostInput || host.ResultSHA256 != hostInput ||
		artifacts.expected != p.configuration.wire.Expected.Source || artifacts.k3sVersion != p.configuration.wire.Expected.K3sVersion || artifacts.postgresReference != postgresReference {
		return "", ErrMutationJournal
	}
	if !reconcile {
		if _, exists := j.state.Steps["stage_images"]; exists {
			return "", ErrMutationUnknown
		}
	}
	if p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil || j.priorExecutorsQuiescent(ctx) != nil {
		return "", ErrSessionAuthority
	}
	plan, err := artifacts.publicationPlan(ctx, j.authority)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct{ Domain, Host, Artifacts string }{"prejoin-artifact-publication-v1", hostInput, plan.digest})
	if err != nil {
		return "", ErrMutationJournal
	}
	sum := sha256.Sum256(raw)
	input := hex.EncodeToString(sum[:])
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
	identity.files = material.Files()
	defer func() {
		for _, raw := range identity.files {
			clear(raw)
		}
	}()
	system, err := os.OpenRoot(systemPath)
	if err != nil {
		return "", ErrImageArchive
	}
	defer system.Close()
	source, err := os.OpenRoot(filepath.Join(parent, "Borealis"))
	if err != nil {
		return "", ErrPreparationConfig
	}
	defer source.Close()
	targets := prejoinArtifactTargets{system, source}
	extras := prejoinArtifactSourceProof{input: input, plan: plan}
	proveSource := func(ctx context.Context) error {
		if prejoinDirectoryBoundary(ctx, j, install) != nil || verifySourceArchive(ctx, archive, p.bundle.asset) != nil ||
			verifyPublishedSourceWithArtifacts(ctx, archive, filepath.Join(parent, "Borealis"), environment, &identity, &extras) != nil ||
			verifySourceArchive(ctx, archive, p.bundle.asset) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil {
			return ErrPreparationConfig
		}
		return nil
	}
	boundary := func(ctx context.Context) error { return prejoinDirectoryBoundary(ctx, j, install) }
	result := ""
	err = install.WithPrejoinFencedCommitted(material, func(_ *os.File) error {
		// No child process is started: both exclusion descriptors remain held by
		// this process for every native filesystem effect and proof.
		if proveSource(ctx) != nil || proveHost(ctx) != nil {
			return ErrMutationUnknown
		}
		if !reconcile && (targets.fresh(ctx, plan, input, boundary) != nil || proveHost(ctx) != nil) {
			return ErrMutationUnknown
		}
		prove := func(ctx context.Context) (string, error) {
			if proveSource(ctx) != nil || proveHost(ctx) != nil || targets.prove(ctx, plan, input, boundary) != nil ||
				proveHost(ctx) != nil || targets.prove(ctx, plan, input, boundary) != nil ||
				p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil || proveSource(ctx) != nil {
				return "", ErrMutationUnknown
			}
			return input, nil
		}
		var err error
		if reconcile {
			result, err = j.reconcileLocked(ctx, "stage_images", input, prove, "applied")
		} else {
			result, err = j.applyLocked(ctx, "stage_images", input, func(ctx context.Context, _ *os.File) (string, error) {
				if proveSource(ctx) != nil || proveHost(ctx) != nil || targets.publishWithSource(ctx, plan, input, boundary, proveSource) != nil {
					return "", ErrMutationUnknown
				}
				return prove(ctx)
			})
		}
		if err != nil {
			return err
		}
		_, err = prove(ctx)
		return err
	})
	if err != nil {
		return "", ErrMutationUnknown
	}
	return result, nil
}
