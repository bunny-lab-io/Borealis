package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
	return p.prejoinArtifactPhaseTransactionWithStart(ctx, j, install, material, artifacts, postgresReference, proveHost, parent, systemPath, reconcile, runtime, join, nil, nil)
}

func (p *PreparationInputs) prejoinArtifactPhaseTransactionWithStart(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, parent, systemPath string, reconcile bool, runtime *PrejoinK3sActions, join *prejoinK3sJoinPhase, start *prejoinK3sStartPhase, images *prejoinImageVerificationPhase) (string, error) {
	return p.prejoinArtifactPhaseTransactionWithActivation(ctx, j, install, material, artifacts, postgresReference, proveHost, parent, systemPath, reconcile, runtime, join, start, images, false)
}

func (p *PreparationInputs) prejoinArtifactPhaseTransactionWithActivation(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, parent, systemPath string, reconcile bool, runtime *PrejoinK3sActions, join *prejoinK3sJoinPhase, start *prejoinK3sStartPhase, images *prejoinImageVerificationPhase, activate bool) (string, error) {
	return p.prejoinArtifactPhaseTransactionWithReceipt(ctx, j, install, material, artifacts, postgresReference, proveHost, parent, systemPath, reconcile, runtime, join, start, images, activate, nil)
}

func (p *PreparationInputs) prejoinArtifactPhaseTransactionWithReceipt(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, parent, systemPath string, reconcile bool, runtime *PrejoinK3sActions, join *prejoinK3sJoinPhase, start *prejoinK3sStartPhase, images *prejoinImageVerificationPhase, activate bool, receipt *PrejoinPreparationReceipt) (string, error) {
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
	if start != nil {
		if runtime != nil || join != nil || start.material == nil || start.actions.Configured == nil || start.actions.Prove == nil || !reconcile && (start.actions.CheckFresh == nil || start.actions.Start == nil) {
			return "", ErrMutationJournal
		}
		step, maximum = "start_k3s", 9
		join = &prejoinK3sJoinPhase{material: start.material}
	}
	if images != nil {
		if runtime != nil || join != nil || start != nil || images.material == nil || images.actions.Observe == nil {
			return "", ErrMutationJournal
		}
		step, maximum = "verify_images", 10
		join = &prejoinK3sJoinPhase{material: images.material}
	}
	if activate {
		if images == nil {
			return "", ErrMutationJournal
		}
		step, maximum = "activate_identity", 11
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
	if activate {
		expectedCount := 10
		if reconcile {
			expectedCount = 11
		}
		if len(j.state.Steps) != expectedCount {
			return "", ErrMutationJournal
		}
		for name, record := range j.state.Steps {
			if record.Executor == j.executor || name != step && record.State != "applied" || name == step && record.State != "intent" && record.State != "applied" {
				return "", ErrMutationUnknown
			}
		}
		for _, prior := range j.state.History {
			if prior.Record.Executor == j.executor {
				return "", ErrMutationUnknown
			}
		}
		quiesced := install.Quiesced
		install.Quiesced = func() error {
			if j.priorExecutorsQuiescent(ctx) != nil || quiesced() != nil || j.priorExecutorsQuiescent(ctx) != nil {
				return ErrMutationUnknown
			}
			return nil
		}
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
	startInput := ""
	if start != nil || images != nil {
		prior := j.state.Steps["configure_k3s"]
		if prior.State != "applied" || prior.InputSHA256 != input || prior.ResultSHA256 != input {
			return "", ErrMutationJournal
		}
		digest := sha256.Sum256([]byte("prepared-k3s-start-v1\x00" + input))
		input = hex.EncodeToString(digest[:])
		startInput = input
	}
	var imageTargets []ContainerdImageTarget
	startResult := ""
	if images != nil {
		prior := j.state.Steps["start_k3s"]
		if prior.State != "applied" || prior.InputSHA256 != startInput || !digestPattern.MatchString(prior.ResultSHA256) {
			return "", ErrMutationJournal
		}
		startResult = prior.ResultSHA256
		imageTargets, err = artifacts.importTargetsLocked(ctx, j.authority, images.pins)
		if err != nil {
			return "", ErrMutationJournal
		}
		digest, err := containerdImportTargetDigest(imageTargets)
		if err != nil {
			return "", ErrMutationJournal
		}
		sum := sha256.Sum256([]byte("prepared-image-verification-v1\x00" + startInput + "\x00" + startResult + "\x00" + digest))
		input = hex.EncodeToString(sum[:])
	}
	var observed K3sStartedObservation
	var completedReceipt PrejoinPreparationReceipt
	imageInput, imageResult := input, ""
	if activate {
		prior := j.state.Steps["verify_images"]
		if prior.State != "applied" || prior.InputSHA256 != imageInput || !digestPattern.MatchString(prior.ResultSHA256) {
			return "", ErrMutationJournal
		}
		imageResult = prior.ResultSHA256
		sum := sha256.Sum256([]byte("prejoin-identity-activation-v1\x00" + imageInput + "\x00" + imageResult))
		input = hex.EncodeToString(sum[:])
		if reconcile && j.state.Steps[step].InputSHA256 != input {
			return "", ErrMutationJournal
		}
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
	proveArtifacts := targets.prove
	if start != nil || images != nil {
		proveArtifacts = targets.proveRunning
	}
	result := ""
	work := func(identityLock *os.File, markerPresent bool, release func() error) error {
		identity.markerReleased = activate && !markerPresent
		// Both fences stay held. Artifact publication starts no child; the
		// fixed runtime adapter must borrow both descriptors for daemon-reload.
		if proveSource(ctx) != nil || proveHost(ctx) != nil {
			return ErrMutationUnknown
		}
		if images != nil {
			if proveArtifacts(ctx, plan, artifactInput, boundary) != nil || images.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil {
				return ErrMutationUnknown
			}
		} else if start != nil {
			if proveArtifacts(ctx, plan, artifactInput, boundary) != nil || start.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil || start.actions.Configured(ctx) != nil ||
				!reconcile && (targets.prove(ctx, plan, artifactInput, boundary) != nil || start.actions.CheckFresh(ctx) != nil) {
				return ErrMutationUnknown
			}
		} else if join != nil {
			if proveArtifacts(ctx, plan, artifactInput, boundary) != nil || join.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil || join.actions.Installed(ctx) != nil || !reconcile && join.actions.CheckFresh(ctx) != nil {
				return ErrMutationUnknown
			}
		} else if runtime != nil {
			if proveArtifacts(ctx, plan, artifactInput, boundary) != nil || !reconcile && runtime.CheckFresh(ctx) != nil {
				return ErrMutationUnknown
			}
		} else if !reconcile && (targets.fresh(ctx, plan, artifactInput, boundary) != nil || proveHost(ctx) != nil) {
			return ErrMutationUnknown
		}
		prove := func(ctx context.Context, hostLock *os.File) (string, error) {
			if proveSource(ctx) != nil || proveHost(ctx) != nil || proveArtifacts(ctx, plan, artifactInput, boundary) != nil ||
				proveHost(ctx) != nil || proveArtifacts(ctx, plan, artifactInput, boundary) != nil ||
				p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil || proveSource(ctx) != nil {
				return "", ErrMutationUnknown
			}
			if runtime != nil && (runtime.Prove(ctx) != nil || proveSource(ctx) != nil || runtime.Prove(ctx) != nil) {
				return "", ErrMutationUnknown
			}
			if images != nil {
				if hostLock == nil || images.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil {
					return "", ErrMutationUnknown
				}
				observation, err := images.actions.Observe(ctx, hostLock, identityLock, slices.Clone(imageTargets))
				if err != nil || proveSource(ctx) != nil || images.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil {
					return "", ErrMutationUnknown
				}
				result, err := observation.result(p.configuration.wire.Expected, startInput, startResult, imageInput, imageTargets)
				if activate {
					if err != nil || result != imageResult {
						return "", ErrMutationUnknown
					}
					observed = observation.After
					return input, nil
				}
				return result, err
			}
			if start != nil {
				if start.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil || start.actions.Configured(ctx) != nil {
					return "", ErrMutationUnknown
				}
				observation, err := start.actions.Prove(ctx)
				if err != nil || proveSource(ctx) != nil || start.actions.Configured(ctx) != nil {
					return "", ErrMutationUnknown
				}
				return observation.result(p.configuration.wire.Expected, input)
			}
			if join != nil && (join.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil || join.actions.Installed(ctx) != nil || join.actions.Prove(ctx) != nil || proveSource(ctx) != nil || join.actions.Prove(ctx) != nil || join.actions.Installed(ctx) != nil) {
				return "", ErrMutationUnknown
			}
			return input, nil
		}
		if activate {
			activateIdentity := func(ctx context.Context, hostLock *os.File) (string, error) {
				if _, err := prove(ctx, hostLock); err != nil {
					return "", err
				}
				if release == nil || release() != nil {
					return "", ErrMutationUnknown
				}
				identity.markerReleased = true
				return prove(ctx, hostLock)
			}
			if reconcile {
				result, err = j.reconcileLocked(ctx, step, input, func(ctx context.Context) (string, error) { return activateIdentity(ctx, j.lock) }, "applied")
			} else {
				result, err = j.applyLocked(ctx, step, input, activateIdentity)
			}
			if err != nil {
				return err
			}
			final, err := prove(ctx, j.lock)
			if err != nil || final != result {
				return ErrMutationUnknown
			}
			if receipt != nil {
				completedReceipt = PrejoinPreparationReceipt{Version: 1, ExpectedSHA256: preparationReceiptDigest(p.configuration.wire.Expected), Source: install.Binding, Node: observed.Node, MemberID: observed.Membership.LocalID}
				completedReceipt.ImageTargetsSHA256, err = containerdImportTargetDigest(imageTargets)
				if err != nil {
					return ErrMutationUnknown
				}
				for _, member := range observed.Membership.Members {
					if member.ID == completedReceipt.MemberID {
						completedReceipt.MemberName = member.Name
					}
				}
				for i, name := range []string{"stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host", "stage_images", "install_k3s", "configure_k3s", "start_k3s", "verify_images", "activate_identity"} {
					record := j.state.Steps[name]
					if record.State != "applied" {
						return ErrMutationUnknown
					}
					completedReceipt.Steps[i] = PreparationStepReceipt{name, record.InputSHA256, record.ResultSHA256}
				}
				if completedReceipt.Validate(p.configuration.wire.Expected, install.Binding) != nil {
					return ErrMutationUnknown
				}
			}
			return nil
		}
		var err error
		if reconcile {
			result, err = j.reconcileLocked(ctx, step, input, func(ctx context.Context) (string, error) { return prove(ctx, j.lock) }, "applied")
		} else {
			result, err = j.applyLocked(ctx, step, input, func(ctx context.Context, hostLock *os.File) (string, error) {
				if proveSource(ctx) != nil || proveHost(ctx) != nil {
					return "", ErrMutationUnknown
				}
				if images != nil {
					return prove(ctx, hostLock)
				}
				if start != nil {
					if targets.prove(ctx, plan, artifactInput, boundary) != nil || start.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil || start.actions.Configured(ctx) != nil || start.actions.CheckFresh(ctx) != nil || start.actions.Start(ctx, hostLock, identityLock) != nil {
						return "", ErrMutationUnknown
					}
				} else if join != nil {
					if proveArtifacts(ctx, plan, artifactInput, boundary) != nil || join.material.Verify(ctx, p.configuration.wire.Expected, install.Binding, j.authority) != nil || join.actions.Installed(ctx) != nil || join.actions.CheckFresh(ctx) != nil || join.actions.Configure(ctx, hostLock, identityLock) != nil {
						return "", ErrMutationUnknown
					}
				} else if runtime != nil {
					if proveArtifacts(ctx, plan, artifactInput, boundary) != nil || runtime.CheckFresh(ctx) != nil || runtime.Install(ctx, hostLock, identityLock) != nil {
						return "", ErrMutationUnknown
					}
				} else if targets.publishWithSource(ctx, plan, artifactInput, boundary, proveSource) != nil {
					return "", ErrMutationUnknown
				}
				return prove(ctx, hostLock)
			})
		}
		if err != nil {
			return err
		}
		final, err := prove(ctx, j.lock)
		if err != nil || final != result {
			return ErrMutationUnknown
		}
		return nil
	}
	if activate {
		err = install.WithPrejoinActivationFencedCommitted(material, reconcile, work)
	} else {
		err = install.WithPrejoinFencedCommitted(material, func(lock *os.File) error { return work(lock, true, nil) })
	}
	if err != nil {
		return "", ErrMutationUnknown
	}
	if receipt != nil {
		*receipt = completedReceipt
	}
	return result, nil
}
