package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"

	"borealis/api-backend/internal/engineidentity"
)

func prejoinHostConfiguredDigest(input string) string {
	sum := sha256.Sum256([]byte("prejoin-host-configured-packages-v1\x00" + input))
	return hex.EncodeToString(sum[:])
}

// Fixed continuation only: configured packages, exact repository/file prefix,
// empty running Docker and independently known native service/kernel state.
// No package replay, file replacement or unknown-state repair is permitted.
type PrejoinHostRecoveryActions struct {
	ProveConfigured func(context.Context) error
	Complete        func(context.Context, *os.File, *os.File) error
	Prove           func(context.Context) error
}

func (p *PreparationInputs) CompletePrejoinHost(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, actions PrejoinHostRecoveryActions) (string, error) {
	if os.Geteuid() != 0 || install.UID != PreparationRuntimeID || install.GID != PreparationRuntimeID {
		return "", ErrMutationJournal
	}
	return p.completePrejoinHost(ctx, j, install, material, actions, "/opt")
}

func (p *PreparationInputs) completePrejoinHost(ctx context.Context, j *MutationJournal, install engineidentity.Installation, material *engineidentity.Material, actions PrejoinHostRecoveryActions, parent string) (string, error) {
	if actions.ProveConfigured == nil || actions.Complete == nil || actions.Prove == nil {
		return "", ErrMutationJournal
	}
	return p.prejoinHostTransaction(ctx, j, install, material, PrejoinHostActions{Prepare: actions.Complete, Prove: actions.Prove}, parent, false, actions.ProveConfigured, false)
}

// Called only inside the source/key-verified transaction holding BOTH fences.
// Retain every old producer before a newer executor can finish bounded effects.
func (j *MutationJournal) continuePrejoinHostLocked(ctx context.Context, input string, configured func(context.Context) error, complete func(context.Context, *os.File) (string, error)) (string, error) {
	return j.continuePrejoinHostClassLocked(ctx, input, prejoinHostConfiguredDigest(input), configured, complete)
}

func (j *MutationJournal) continuePrejoinHostClassLocked(ctx context.Context, input, partial string, configured func(context.Context) error, complete func(context.Context, *os.File) (string, error)) (string, error) {
	if partial != prejoinHostConfiguredDigest(input) && partial != prejoinHostFirewallDigest(input) {
		return "", ErrMutationJournal
	}
	const step = "prepare_host"
	if j.authority(ctx) != nil {
		return "", ErrSessionAuthority
	}
	previous, exists := j.state.Steps[step]
	if !j.state.valid() || !exists || previous.State != "intent" || previous.InputSHA256 != input || !digestPattern.MatchString(input) || configured == nil || complete == nil || len(j.state.History) >= 16 {
		return "", ErrMutationJournal
	}
	if j.state.Owner.Generation <= previous.Binding.Generation {
		return "", ErrMutationFreshClaim
	}
	if previous.Executor == j.executor {
		return "", ErrMutationUnknown
	}
	if len(j.state.Steps) != 5 {
		return "", ErrMutationJournal
	}
	for _, prior := range []string{"stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity"} {
		if j.state.Steps[prior].State != "applied" {
			return "", ErrMutationJournal
		}
	}
	for range 2 {
		if j.priorExecutorsQuiescent(ctx) != nil || j.authority(ctx) != nil || configured(ctx) != nil || j.authority(ctx) != nil || j.priorExecutorsQuiescent(ctx) != nil {
			return "", ErrMutationUnknown
		}
	}
	previous.State, previous.ResultSHA256 = "continued", partial
	j.state.History = append(j.state.History, mutationPastAttempt{Step: step, Record: previous})
	// Older helpers must reject this new continuation contract. Account history
	// remains retained and validated; ordinary version2/3 journals are unchanged.
	j.state.Version = 4
	j.state.Steps[step] = mutationRecord{Binding: j.state.Owner, Executor: j.executor, InputSHA256: input, State: "intent"}
	if j.persist(ctx) != nil {
		j.failed = true
		return "", ErrMutationUnknown
	}
	return j.executeIntentLocked(ctx, step, complete)
}
