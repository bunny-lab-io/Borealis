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
	return p.prejoinArtifactPhaseTransaction(ctx, j, install, material, artifacts, postgresReference, proveHost, parent, systemPath, reconcile, nil)
}

func (p *PreparationInputs) prejoinArtifactPhaseTransaction(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, parent, systemPath string, reconcile bool, runtime *PrejoinK3sActions) (string, error) {
	return p.prejoinArtifactPhaseTransactionWithJoin(ctx, j, install, material, artifacts, postgresReference, proveHost, parent, systemPath, reconcile, runtime, nil)
}

func (p *PreparationInputs) prejoinArtifactPhaseTransactionWithJoin(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, parent, systemPath string, reconcile bool, runtime *PrejoinK3sActions, join *prejoinK3sJoinPhase) (string, error) {
	if p == nil || j == nil || artifacts == nil || proveHost == nil || !ValidPostgresImageReference(postgresReference) {
		return "", ErrMutationJournal
	}
	step, maximum := "stage_images", 6
	if runtime != nil {
		step, maximum = "install_k3s", 7
		if runtime.Prove == nil || !reconcile && (runtime.CheckFresh == nil || runtime.Install == nil) {
			return "", ErrMutationJournal
		}
	}
	if join != nil {
		step, maximum = "configure_k3s", 8
		if runtime != nil || join.material == nil || join.actions.Installed == nil || join.actions.Prove == nil || !reconcile && (join.actions.CheckFresh == nil || join.actions.Configure == nil) {
			return "", ErrMutationJournal
		}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	artifacts.mu.Lock()
	defer artifacts.mu.Unlock()
	install, hostInput, identity, err := p.prejoinHostPrefixRequest(ctx, j, install, material, parent, maximum)
	host := j.state.Steps["prepare_host"]
	if err != nil || host.State != "applied" || host.InputSHA256 != hostInput || host.ResultSHA256 != hostInput ||
		artifacts.expected != p.configuration.wire.Expected.Source || artifacts.k3sVersion != p.configuration.wire.Expected.K3sVersion || artifacts.postgresReference != postgresReference {
		return "", ErrMutationJournal
	}
	if !reconcile {
		if _, exists := j.state.Steps[step]; exists {
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
	artifactInput := hex.EncodeToString(sum[:])
	input := artifactInput
	if runtime != nil || join != nil {
		prior := j.state.Steps["stage_images"]
		if prior.State != "applied" || prior.InputSHA256 != artifactInput || prior.ResultSHA256 != artifactInput {
			return "", ErrMutationJournal
		}
		digest := sha256.Sum256([]byte("prejoin-k3s-installation-v1\x00" + artifactInput))
		input = hex.EncodeToString(digest[:])
	}
	if join != nil {
		prior := j.state.Steps["install_k3s"]
		if prior.State != "applied" || prior.InputSHA256 != input || prior.ResultSHA256 != input {
			return "", ErrMutationJournal
		}
		joinDigest, err := join.material.inputDigest(ctx, p.configuration.wire.Expected, install.Binding, j.authority)
		if err != nil {
			return "", ErrMutationJournal
		}
		config, err := PrejoinK3sJoinConfiguration(p.configuration.wire.Expected)
		if err != nil {
			return "", ErrMutationJournal
		}
		digest := sha256.Sum256([]byte("prejoin-k3s-configuration-v1\x00" + input + "\x00" + joinDigest + "\x00" + config))
		input = hex.EncodeToString(digest[:])
	}
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
	extras := prejoinArtifactSourceProof{input: artifactInput, plan: plan}
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
	err = install.WithPrejoinFencedCommitted(material, func(identityLock *os.File) error {
		// Both fences stay held. Artifact publication starts no child; the
		// fixed runtime adapter must borrow both descriptors for daemon-reload.
		if proveSource(ctx) != nil || proveHost(ctx) != nil {
			return ErrMutationUnknown
		}
		if join != nil {
			if targets.prove(ctx, plan, artifactInput, boundary) != nil || join.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil || join.actions.Installed(ctx) != nil || !reconcile && join.actions.CheckFresh(ctx) != nil {
				return ErrMutationUnknown
			}
		} else if runtime != nil {
			if targets.prove(ctx, plan, artifactInput, boundary) != nil || !reconcile && runtime.CheckFresh(ctx) != nil {
				return ErrMutationUnknown
			}
		} else if !reconcile && (targets.fresh(ctx, plan, artifactInput, boundary) != nil || proveHost(ctx) != nil) {
			return ErrMutationUnknown
		}
		prove := func(ctx context.Context) (string, error) {
			if proveSource(ctx) != nil || proveHost(ctx) != nil || targets.prove(ctx, plan, artifactInput, boundary) != nil ||
				proveHost(ctx) != nil || targets.prove(ctx, plan, artifactInput, boundary) != nil ||
				p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil || proveSource(ctx) != nil {
				return "", ErrMutationUnknown
			}
			if runtime != nil && (runtime.Prove(ctx) != nil || proveSource(ctx) != nil || runtime.Prove(ctx) != nil) {
				return "", ErrMutationUnknown
			}
			if join != nil && (join.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil || join.actions.Installed(ctx) != nil || join.actions.Prove(ctx) != nil || proveSource(ctx) != nil || join.actions.Prove(ctx) != nil || join.actions.Installed(ctx) != nil) {
				return "", ErrMutationUnknown
			}
			return input, nil
		}
		var err error
		if reconcile {
			result, err = j.reconcileLocked(ctx, step, input, prove, "applied")
		} else {
			result, err = j.applyLocked(ctx, step, input, func(ctx context.Context, hostLock *os.File) (string, error) {
				if proveSource(ctx) != nil || proveHost(ctx) != nil {
					return "", ErrMutationUnknown
				}
				if join != nil {
					if targets.prove(ctx, plan, artifactInput, boundary) != nil || join.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil || join.actions.Installed(ctx) != nil || join.actions.CheckFresh(ctx) != nil || join.actions.Configure(ctx, hostLock, identityLock) != nil {
						return "", ErrMutationUnknown
					}
				} else if runtime != nil {
					if targets.prove(ctx, plan, artifactInput, boundary) != nil || runtime.CheckFresh(ctx) != nil || runtime.Install(ctx, hostLock, identityLock) != nil {
						return "", ErrMutationUnknown
					}
				} else if targets.publishWithSource(ctx, plan, artifactInput, boundary, proveSource) != nil {
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
