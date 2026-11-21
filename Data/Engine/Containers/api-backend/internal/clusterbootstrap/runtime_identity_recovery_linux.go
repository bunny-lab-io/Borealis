package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
)

func runtimeIdentityGroupOnlyDigest(input string) string {
	sum := sha256.Sum256([]byte("runtime-identity-group-only-v1\x00" + input))
	return hex.EncodeToString(sum[:])
}

// RuntimeIdentityRecoveryActions permits only completion of the fixed missing
// user after independently proving the exact existing group and absent user.
// It cannot create/rewrite a group, repair an account or replay unknown work.
type RuntimeIdentityRecoveryActions struct {
	ProveGroupOnly func(context.Context) error
	CompleteUser   func(context.Context, *os.File) error
	Prove          func(context.Context) error
}

func (p *PreparationInputs) CompleteRuntimeIdentity(ctx context.Context, j *MutationJournal, actions RuntimeIdentityRecoveryActions) (string, error) {
	if os.Geteuid() != 0 {
		return "", ErrMutationJournal
	}
	return p.completeRuntimeIdentity(ctx, j, actions, "/opt", PreparationRuntimeID, PreparationRuntimeID)
}

func (p *PreparationInputs) completeRuntimeIdentity(ctx context.Context, j *MutationJournal, actions RuntimeIdentityRecoveryActions, parent string, uid, gid int) (string, error) {
	if actions.ProveGroupOnly == nil || actions.CompleteUser == nil || actions.Prove == nil {
		return "", ErrMutationJournal
	}
	return p.runtimeIdentityTransaction(ctx, j, RuntimeIdentityActions{Prepare: actions.CompleteUser, Prove: actions.Prove}, parent, uid, gid, false, actions.ProveGroupOnly)
}

// Only the runtime-account transaction can call this while retaining exact
// source/configuration proof. Transfer producer ownership before effects, never
// overwrite an old executor in place or mislabel a partial group as absent.
func (j *MutationJournal) continueRuntimeIdentityLocked(ctx context.Context, input string, proveGroupOnly func(context.Context) error, complete func(context.Context, *os.File) (string, error)) (string, error) {
	const step = "prepare_runtime_identity"
	if j.authority(ctx) != nil {
		return "", ErrSessionAuthority
	}
	previous, exists := j.state.Steps[step]
	if !j.state.valid() || !exists || previous.State != "intent" || previous.InputSHA256 != input || !digestPattern.MatchString(input) || proveGroupOnly == nil || complete == nil || len(j.state.History) >= 16 {
		return "", ErrMutationJournal
	}
	if j.state.Owner.Generation <= previous.Binding.Generation {
		return "", ErrMutationFreshClaim
	}
	if previous.Executor == j.executor {
		return "", ErrMutationUnknown
	}
	for priorStep, record := range j.state.Steps {
		if priorStep != step && (priorStep != "stage_source" || record.State != "applied") {
			return "", ErrMutationJournal
		}
	}
	for range 2 {
		if j.priorExecutorsQuiescent(ctx) != nil || j.authority(ctx) != nil || proveGroupOnly(ctx) != nil || j.authority(ctx) != nil || j.priorExecutorsQuiescent(ctx) != nil {
			return "", ErrMutationUnknown
		}
	}
	// Version3 is introduced only with a retained, narrowly validated partial
	// account proof. Version2 journals remain valid without rewrite; old helpers
	// reject Version3 rather than forgetting an executor that could still write.
	previous.State, previous.ResultSHA256 = "continued", runtimeIdentityGroupOnlyDigest(input)
	j.state.History = append(j.state.History, mutationPastAttempt{Step: step, Record: previous})
	j.state.Version = 3
	j.state.Steps[step] = mutationRecord{Binding: j.state.Owner, Executor: j.executor, InputSHA256: input, State: "intent"}
	if err := j.persist(ctx); err != nil {
		j.failed = true
		return "", ErrMutationUnknown
	}
	return j.executeIntentLocked(ctx, step, complete)
}
