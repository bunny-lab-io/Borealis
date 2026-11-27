package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"time"
)

// A native dispatcher must own fresh domain/source proofs, fixed remote
// journals and joined cleanup. Returning hashes alone is not an implementation
// of that contract. The existing API runtime dispatches the native paired
// adapter only after the original controller releases approved work.
type clusterSSHPreparationDispatch func(context.Context, clusterSSHNetworkTargetClaim,
	clusterbootstrap.Expected, string, engineidentity.Binding, func(context.Context) error) (clusterSSHPreparationResult, error)

type clusterSSHPrejoinDispatchResult struct {
	Expected clusterbootstrap.PreparationExpected
	Receipt  clusterbootstrap.PrejoinPreparationReceipt
}
type clusterSSHPrejoinPreparationDispatch func(context.Context, clusterSSHNetworkTargetClaim, clusterbootstrap.Expected, string, engineidentity.Binding, func(context.Context) error) (clusterSSHPrejoinDispatchResult, error)
type clusterSSHPrejoinCohortDispatch func(context.Context, []clusterSSHNetworkTargetClaim, clusterbootstrap.Expected, string, engineidentity.Binding, func(context.Context) error) ([]clusterSSHPrejoinDispatchResult, error)

type clusterSSHPreparationWorker struct {
	store    *postgresOperatorStore
	aegis    *goAegisService
	dispatch clusterSSHPreparationDispatch
	interval time.Duration
	prejoin  clusterSSHPrejoinPreparationDispatch
	cohort   clusterSSHPrejoinCohortDispatch
	getJSON  func(context.Context, string, any) error
	joined   clusterSSHPrejoinJoinedRead
}

func newClusterSSHPreparationWorker(store *postgresOperatorStore, aegis *goAegisService, dispatch clusterSSHPreparationDispatch) *clusterSSHPreparationWorker {
	if store == nil || store.db == nil || aegis == nil || dispatch == nil {
		return nil
	}
	return &clusterSSHPreparationWorker{store: store, aegis: aegis, dispatch: dispatch, interval: 5 * time.Second}
}

// Callable-only pre-join variant reuses claim renewal, owned source selection,
// descendant joining and atomic settlement. No poller or admission is installed.
func newClusterSSHPrejoinPreparationWorker(store *postgresOperatorStore, aegis *goAegisService, dispatch clusterSSHPrejoinPreparationDispatch, getJSON func(context.Context, string, any) error) *clusterSSHPreparationWorker {
	if store == nil || store.db == nil || aegis == nil || dispatch == nil || getJSON == nil {
		return nil
	}
	return &clusterSSHPreparationWorker{store: store, aegis: aegis, prejoin: dispatch, getJSON: getJSON, interval: 5 * time.Second}
}

func newClusterSSHPrejoinCohortPreparationWorker(store *postgresOperatorStore, aegis *goAegisService, dispatch clusterSSHPrejoinCohortDispatch, getJSON func(context.Context, string, any) error) *clusterSSHPreparationWorker {
	if store == nil || store.db == nil || aegis == nil || dispatch == nil || getJSON == nil {
		return nil
	}
	return &clusterSSHPreparationWorker{store: store, aegis: aegis, cohort: dispatch, getJSON: getJSON, interval: 5 * time.Second}
}

// Prepare neither claims nor reclaims targets. Any uncertain dispatch outcome
// retains all original claims until expiry; expiry cannot authorize replay.
// Only successful whole-cohort dispatch and joined scope cleanup can settle.
func (w *clusterSSHPreparationWorker) prepare(parent context.Context, work clusterSSHPreparationWork) error {
	if w == nil || w.store == nil || w.store.db == nil || w.aegis == nil ||
		(w.dispatch == nil && w.prejoin == nil && w.cohort == nil) || w.dispatch != nil && (w.prejoin != nil || w.cohort != nil) || w.prejoin != nil && w.cohort != nil ||
		(w.prejoin != nil || w.cohort != nil) && (w.getJSON == nil) == (w.joined == nil) ||
		parent.Err() != nil || len(work.Claims) < 1 || len(work.Claims) > 2 || work.Baseline.Validate() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	// Reconstruct owned authority from immutable payload, not caller-owned slices.
	claims := slices.Clone(work.Claims)
	proof, version, err := parseClusterSSHPreparationInspection([]byte(work.Payload), claims[0].Lease, work.Baseline)
	if err != nil || !reflect.DeepEqual(proof, work.Proof) || len(proof.Cohort.Targets) != len(claims) {
		return clusterbootstrap.ErrPreparationConfig
	}
	authorization, _, err := parseClusterSSHPreparationAuthorization(work.Payload, proof, work.Source)
	if err != nil || !reflect.DeepEqual(authorization, work.Authorization) {
		return clusterbootstrap.ErrPreparationConfig
	}
	var confirmationRaw json.RawMessage
	if clusterBootstrapObject([]byte(work.Payload), map[string]any{"ssh_preparation_confirmation": &confirmationRaw}) != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	confirmation, err := parseClusterSSHPreparationConfirmation(confirmationRaw, authorization, proof.Source)
	if err != nil || confirmation.IdentitySource != work.IdentitySource {
		return clusterbootstrap.ErrPreparationConfig
	}
	digest := sha256.Sum256([]byte(work.Payload))
	payloadHash := hex.EncodeToString(digest[:])
	readers := make([]clusterSSHPreparationAuthorityRead, len(claims))
	for i, claim := range claims {
		target, reserved := proof.Cohort.Targets[i], authorization.Targets[i]
		credentialHash, err := clusterSSHPreparationDigest([]string{claim.Sealed.generation, claim.Sealed.ciphertext})
		if !validClusterSSHPreparationLease(claim.Lease) || claim.Lease.OperationID != proof.Cohort.OperationID ||
			claim.Lease.OperationAttempt != proof.Cohort.Attempt || claim.Lease.ControllerHolder != proof.Cohort.ControllerHolder ||
			claim.Lease.TargetID != target.Binding.TargetID || claim.Lease.Generation != reserved.QualificationGeneration+1+pairRecoveryOffset(work.Payload) ||
			claim.Sealed.binding != target.Binding || err != nil || credentialHash != reserved.CredentialSHA256 {
			return clusterbootstrap.ErrPreparationConfig
		}
		read := newClusterSSHPreparationAuthorityRead(w.store, w.aegis, claim.Lease, work.Baseline, claim.Sealed)
		readers[i] = func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
			value, err := read(ctx)
			stable, inspected := value.Cohort, proof.Cohort
			stable.ObservedAt, inspected.ObservedAt = 0, 0
			if err != nil || value.IdentitySource == nil || *value.IdentitySource != work.IdentitySource || value.Lease != claim.Lease || value.Baseline != work.Baseline || value.Recovery != nil ||
				value.payload != work.Payload || value.PayloadSHA256 != payloadHash || value.K3sVersion != version ||
				value.Queue != work.Source || !reflect.DeepEqual(value.Source, proof.Source) || !reflect.DeepEqual(stable, inspected) {
				return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrSessionAuthority
			}
			return value, nil
		}
		if _, err := readers[i](parent); err != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
	}
	if qualification, err := pairReport(work.Payload); err == nil {
		parent = context.WithValue(parent, clusterSSHPairManifestKey{}, qualification.Installation.Manifests)
	}
	if w.cohort != nil {
		parent = context.WithValue(parent, clusterSSHPrejoinDiagnosticKey{}, &clusterSSHPrejoinDiagnostic{operation: claims[0].Lease.OperationID, started: time.Now()})
	}
	results := make([]clusterSSHPreparationResult, len(claims))
	prepared := make([]clusterSSHPrejoinCompletion, len(claims))
	maximum := clusterSSHPreparationWorkerBudget(w.prejoin != nil || w.cohort != nil, len(claims))
	if _, err := pairReport(work.Payload); err == nil {
		hardLimit := time.Unix(confirmation.ConfirmedAt, 0).Add(maximum)
		var cancel context.CancelFunc
		parent, cancel = context.WithDeadline(parent, hardLimit)
		defer cancel()
	}
	err = w.store.withSSHOperationOwnership(parent, w.aegis, work, w.interval, maximum, func(scope context.Context, checkAll func(context.Context) error) error {
		ctx, cancel := context.WithCancel(scope)
		defer cancel()
		boundary := clusterSSHPreparationBoundary(ctx, checkAll, func(context.Context) error { return nil })
		check := func(caller context.Context) error {
			if boundary(caller) != nil {
				cancel() // Ignored boundary failure still poisons this dispatch scope.
				return clusterbootstrap.ErrSessionAuthority
			}
			return nil
		}
		if w.cohort != nil {
			values, err := w.cohort(ctx, slices.Clone(claims), work.Baseline, authorization.ConfirmationSHA256, work.IdentitySource, check)
			if err != nil || len(values) != len(claims) || check(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			for i, value := range values {
				if value.Expected.Source != work.Baseline || value.Receipt.Validate(value.Expected, work.IdentitySource) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				value.Expected.PeerAddresses = slices.Clone(value.Expected.PeerAddresses)
				prepared[i] = clusterSSHPrejoinCompletion{Claim: claims[i], Expected: value.Expected, Receipt: value.Receipt, PayloadSHA256: payloadHash}
			}
			return nil
		}
		for i, claim := range claims {
			if check(ctx) != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			if w.prejoin != nil {
				value, err := w.prejoin(ctx, claim, work.Baseline, authorization.ConfirmationSHA256, work.IdentitySource, check)
				if err != nil || ctx.Err() != nil || value.Expected.Source != work.Baseline || value.Receipt.Validate(value.Expected, work.IdentitySource) != nil || check(ctx) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				value.Expected.PeerAddresses = slices.Clone(value.Expected.PeerAddresses)
				prepared[i] = clusterSSHPrejoinCompletion{Claim: claim, Expected: value.Expected, Receipt: value.Receipt, PayloadSHA256: payloadHash}
				continue
			}
			result, err := w.dispatch(ctx, claim, work.Baseline, authorization.ConfirmationSHA256, work.IdentitySource, check)
			if err != nil || ctx.Err() != nil || result.Version != 1 || !result.valid(claim.Lease) ||
				result.ConfirmationSHA256 != authorization.ConfirmationSHA256 || check(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			results[i] = result
		}
		return nil
	})
	if err != nil || parent.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	if w.prejoin != nil || w.cohort != nil {
		joined := w.joined
		if joined == nil {
			joined = func(ctx context.Context, read clusterSSHPreparationAuthorityRead, _ clusterSSHNetworkTargetClaim, _ clusterbootstrap.Expected) (clusterSSHJoinedTargets, error) {
				return readClusterSSHJoinedTargets(ctx, read, w.getJSON)
			}
		}
		if w.store.completeClusterSSHPrejoinPreparationRead(parent, w.aegis, prepared, joined) != nil || parent.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
	// Heartbeat and all dispatch descendants have joined before final authority
	// collection and atomic settlement. Cancellation cannot become success.
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	completions := make([]clusterSSHPreparationCompletion, len(claims))
	for i, claim := range claims {
		value, err := readers[i](ctx)
		if err != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		completions[i] = clusterSSHPreparationCompletion{Authority: value, Sealed: claim.Sealed, Result: results[i]}
	}
	if w.store.completeClusterSSHPreparation(ctx, completions) != nil || ctx.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return nil
}

func clusterSSHPreparationWorkerBudget(prejoin bool, targets int) time.Duration {
	if targets < 1 || targets > 2 {
		return 0
	}
	if prejoin {
		// Claims are dispatched serially while the whole exact cohort renews.
		return time.Duration(targets) * clusterSSHPreparationSourceBudget(true)
	}
	return 10 * time.Minute
}
