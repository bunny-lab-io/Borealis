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

// PrejoinHostActions is a fixed native adapter, never commands from the wire.
// Prepare must preserve the complete published source/private configuration,
// account and identity tree, inherit BOTH borrowed locks into every child, honor
// cancellation and join all work. It must not install/join K3s, start identity
// consumers or release their marker. Prove reads actual prepared host state,
// not exit status, a journal label or a retained observation; it changes nothing.
// Current source, target, boot, mount and account custody remain mandatory in
// Installation.Check/Quiesced throughout the native adapter's work.
type PrejoinHostActions struct {
	// CheckFresh is read-only and runs under both fences before durable intent.
	// Prepare must repeat its initial checks before any native host mutation.
	CheckFresh func(context.Context) error
	Prepare    func(ctx context.Context, hostLock, identityLock *os.File) error
	Prove      func(context.Context) error
}

// PreparePrejoinHost commits only after exact prerequisite receipts, current
// source/configuration/key proof and fresh native host proof. The startup marker
// stays present. Any partial effect retains intent, with no automatic replay.
func (p *PreparationInputs) PreparePrejoinHost(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, actions PrejoinHostActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.preparePrejoinHost(ctx, j, install, material, actions, "/opt", false)
}

// ReconcilePrejoinHost has no mutation callback. A distinct executor must prove
// previous executors stopped and the entire requested host effect exists now.
// Partial preparation stays unknown; absence or a failed probe never enables it.
func (p *PreparationInputs) ReconcilePrejoinHost(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, prove func(context.Context) error) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.preparePrejoinHost(ctx, j, install, material, PrejoinHostActions{Prove: prove}, "/opt", true)
}

func (p *PreparationInputs) prejoinHostRequest(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, parent string) (engineidentity.Installation, string, prejoinDirectoryProof, error) {
	return p.prejoinHostPhaseRequest(ctx, j, install, material, parent, false)
}

// Artifact publication extends only this exact prefix. Earlier host actions
// retain their original phase boundary and cannot replay after image effects.
func (p *PreparationInputs) prejoinHostPhaseRequest(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, parent string, artifacts bool) (engineidentity.Installation, string, prejoinDirectoryProof, error) {
	maximum := 5
	if artifacts {
		maximum = 6
	}
	return p.prejoinHostPrefixRequest(ctx, j, install, material, parent, maximum)
}

func (p *PreparationInputs) prejoinHostPrefixRequest(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, parent string, maximum int) (engineidentity.Installation, string, prejoinDirectoryProof, error) {
	invalid := prejoinDirectoryProof{}
	if j == nil || len(j.state.Steps) < 4 || len(j.state.Steps) > maximum {
		return install, "", invalid, ErrMutationJournal
	}
	for step := range j.state.Steps {
		switch step {
		case "stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host":
		case "stage_images":
			if maximum < 6 {
				return install, "", invalid, ErrMutationJournal
			}
		case "install_k3s":
			if maximum < 7 {
				return install, "", invalid, ErrMutationJournal
			}
		case "configure_k3s":
			if maximum < 8 {
				return install, "", invalid, ErrMutationJournal
			}
		case "start_k3s":
			if maximum < 9 {
				return install, "", invalid, ErrMutationJournal
			}
		case "verify_images":
			if maximum != 10 {
				return install, "", invalid, ErrMutationJournal
			}
		default:
			return install, "", invalid, ErrMutationJournal
		}
	}
	source, err := p.sourcePublicationBinding(j)
	if err != nil {
		return install, "", invalid, err
	}
	rootInput, proof, err := prejoinDirectoryRequestFromSource(j, install, material, parent, source)
	root := j.state.Steps["prepare_identity_root"]
	if err != nil || root.State != "applied" || root.InputSHA256 != rootInput || root.ResultSHA256 != rootInput {
		return install, "", invalid, ErrMutationJournal
	}
	install, identityInput, err := j.identityRequest(ctx, install, material, true)
	identity := j.state.Steps["install_identity"]
	if err != nil || identity.State != "applied" || identity.InputSHA256 != identityInput || identity.ResultSHA256 != material.Digest() {
		return install, "", invalid, ErrMutationJournal
	}
	raw, err := json.Marshal(struct {
		Domain, Source, Directory, Identity string
	}{"prejoin-host-preparation-v2-fresh", source, rootInput, identityInput})
	if err != nil {
		return install, "", invalid, ErrMutationJournal
	}
	sum := sha256.Sum256(raw)
	return install, hex.EncodeToString(sum[:]), proof, nil
}

func (p *PreparationInputs) preparePrejoinHost(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, actions PrejoinHostActions, parent string, reconcile bool) (string, error) {
	return p.prejoinHostTransaction(ctx, j, install, material, actions, parent, reconcile, nil, hostContinuationNone)
}

func (p *PreparationInputs) prejoinHostTransaction(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, actions PrejoinHostActions, parent string, reconcile bool, configured func(context.Context) error, continuation prejoinHostContinuation) (string, error) {
	if j == nil || actions.Prove == nil || !reconcile && (actions.Prepare == nil || configured == nil && actions.CheckFresh == nil) {
		return "", ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	install, input, proof, err := p.prejoinHostRequest(ctx, j, install, material, parent)
	if err != nil {
		return "", err
	}
	if !reconcile && configured == nil {
		if _, exists := j.state.Steps["prepare_host"]; exists {
			return "", ErrMutationUnknown // Existing intents need their own proof/continuation.
		}
	}
	if p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil || j.priorExecutorsQuiescent(ctx) != nil {
		return "", ErrSessionAuthority
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
	proof.files = material.Files()
	defer func() {
		for _, raw := range proof.files {
			clear(raw)
		}
	}()
	proveSource := func(ctx context.Context) error {
		if prejoinDirectoryBoundary(ctx, j, install) != nil || verifySourceArchive(ctx, archive, p.bundle.asset) != nil ||
			verifyPublishedSourceWithIdentityRoot(ctx, archive, filepath.Join(parent, "Borealis"), environment, &proof) != nil ||
			verifySourceArchive(ctx, archive, p.bundle.asset) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil {
			return ErrPreparationConfig
		}
		return nil
	}
	result := ""
	err = install.WithPrejoinFencedCommitted(material, func(identityLock *os.File) error {
		if proveSource(ctx) != nil {
			return ErrPreparationConfig
		}
		if !reconcile && configured == nil && (actions.CheckFresh(ctx) != nil || proveSource(ctx) != nil) {
			return ErrMutationUnknown // No host intent exists when fresh proof fails.
		}
		prove := func(ctx context.Context) (string, error) {
			if proveSource(ctx) != nil || actions.Prove(ctx) != nil || proveSource(ctx) != nil || actions.Prove(ctx) != nil ||
				p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil || proveSource(ctx) != nil {
				return "", ErrMutationUnknown
			}
			return input, nil
		}
		var err error
		if reconcile {
			result, err = j.reconcileLocked(ctx, "prepare_host", input, prove, "applied")
		} else {
			mutate := func(ctx context.Context, hostLock *os.File) (string, error) {
				if proveSource(ctx) != nil || actions.Prepare(ctx, hostLock, identityLock) != nil || prejoinDirectoryBoundary(ctx, j, install) != nil {
					return "", ErrMutationUnknown
				}
				return prove(ctx)
			}
			if configured != nil {
				continueHost := j.continuePrejoinHostLocked
				switch continuation {
				case hostContinuationConfigured:
				case hostContinuationFirewall:
					continueHost = j.continuePrejoinFirewallLocked
				case hostContinuationFresh:
					continueHost = j.continueFreshPrejoinHostLocked
				case hostContinuationRepository:
					continueHost = j.continueRepositoryPrejoinHostLocked
				default:
					return ErrMutationJournal
				}
				result, err = continueHost(ctx, input, func(ctx context.Context) error {
					if proveSource(ctx) != nil || configured(ctx) != nil || proveSource(ctx) != nil {
						return ErrMutationUnknown
					}
					return nil
				}, mutate)
			} else {
				result, err = j.applyLocked(ctx, "prepare_host", input, mutate)
			}
		}
		if err != nil {
			return err
		}
		// Applied outer state is not an acknowledgement if custody/source or
		// actual host state changed during persistence. Separate proof is needed.
		if _, err := prove(ctx); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", ErrMutationUnknown
	}
	return result, nil
}
