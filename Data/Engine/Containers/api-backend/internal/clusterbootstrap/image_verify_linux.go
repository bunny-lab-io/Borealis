package clusterbootstrap

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
)

// Observe is read-only but inherits both exclusions because its fixed ctr child
// must not outlive target authority or race identity/host work.
type PreparedImageVerificationActions struct {
	Observe func(context.Context, *os.File, *os.File, []ContainerdImageTarget) (PreparedImageObservation, error)
}

type PreparedImageObservation struct {
	Before K3sStartedObservation
	Images []ContainerdImageTarget
	After  K3sStartedObservation
}

type prejoinImageVerificationPhase struct {
	material *K3sJoinMaterial
	actions  PreparedImageVerificationActions
	pins     K3sInputPins
}

func (p *PreparationInputs) VerifyPreparedImages(ctx context.Context, j *MutationJournal, install engineidentity.Installation, identity *engineidentity.Material,
	artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, material *K3sJoinMaterial, actions PreparedImageVerificationActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.prejoinArtifactPhaseTransactionWithStart(ctx, j, install, identity, artifacts, postgresReference, proveHost, "/opt", "/", false, nil, nil, nil, &prejoinImageVerificationPhase{material, actions, K3sPins()})
}

func (p *PreparationInputs) ReconcilePreparedImages(ctx context.Context, j *MutationJournal, install engineidentity.Installation, identity *engineidentity.Material,
	artifacts *PreparationArtifacts, postgresReference string, proveHost func(context.Context) error, material *K3sJoinMaterial, actions PreparedImageVerificationActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.prejoinArtifactPhaseTransactionWithStart(ctx, j, install, identity, artifacts, postgresReference, proveHost, "/opt", "/", true, nil, nil, nil, &prejoinImageVerificationPhase{material, actions, K3sPins()})
}

// Receipt binds current image content to exact retained Node/member startup
// proof. Matching image rows cannot authorize a replaced Node or etcd member.
func (o PreparedImageObservation) result(expected PreparationExpected, startInput, startResult, input string, targets []ContainerdImageTarget) (string, error) {
	before, err := o.Before.result(expected, startInput)
	if err != nil || before != startResult {
		return "", ErrMutationUnknown
	}
	after, err := o.After.result(expected, startInput)
	if err != nil || after != startResult || !slices.Equal(o.Images, targets) {
		return "", ErrMutationUnknown
	}
	targetDigest, err := containerdImportTargetDigest(targets)
	if err != nil || !digestPattern.MatchString(input) {
		return "", ErrMutationUnknown
	}
	raw, err := json.Marshal(struct {
		Domain, Input, Startup, Targets string
	}{"prepared-image-verification-receipt-v1", input, startResult, targetDigest})
	if err != nil {
		return "", ErrMutationUnknown
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
