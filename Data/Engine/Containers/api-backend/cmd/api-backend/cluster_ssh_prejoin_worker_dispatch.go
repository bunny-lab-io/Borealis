package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"slices"
	"sync/atomic"
)

// Callable composition only. Every original target reaches configuration before
// any startup. Fresh retained scopes re-prove the complete cohort before each
// start. Approval, production polling and controller admission remain separate.
func newClusterSSHNativePrejoinPreparationWorker(store *postgresOperatorStore, aegis *goAegisService,
	scratchParent string, process clusterExistingIdentitySourceProcess, getJSON func(context.Context, string, any) error) *clusterSSHPreparationWorker {
	if store == nil || store.db == nil || aegis == nil {
		return nil
	}
	dispatch := func(ctx context.Context, claims []clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected, confirmation string,
		binding engineidentity.Binding, current func(context.Context) error) ([]clusterSSHPrejoinDispatchResult, error) {
		source := clusterSSHPrejoinSource{Binding: binding, Process: process}
		if !clusterSSHPrejoinLocalProcessMatches(source) {
			return nil, clusterbootstrap.ErrSessionAuthority
		}
		scope := func(ctx context.Context, index int, consume func(context.Context, clusterbootstrap.PreparationExpected, clusterSSHPrejoinNativeTarget) error) error {
			claim := claims[index]
			acquire := func(ctx context.Context, consume clusterSSHPrejoinWorkerSourceConsumer) error {
				return withClusterSSHPreparationSourceFor(ctx, scratchParent, store, aegis, claim.Lease, baseline, claim.Sealed, true, consume)
			}
			return withClusterSSHPrejoinWorkerInputs(ctx, claim, baseline, confirmation, source,
				newClusterSSHPreparationAuthorityRead(store, aegis, claim.Lease, baseline, claim.Sealed), acquire,
				func(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts,
					postgres string, expected clusterbootstrap.PreparationExpected, checks clusterSSHPreparationChecks) error {
					return withClusterSSHPrejoinNativeScope(ctx, store, aegis, source, claim, baseline, inputs, artifacts, postgres, expected,
						checks.Authority, checks.K3sJoin, true, func(ctx context.Context, target clusterSSHPrejoinNativeTarget) error {
							target.inputs = checks.Inputs
							return consume(ctx, expected, target)
						})
				}, current)
		}
		return dispatchClusterSSHPrejoinCohort(ctx, claims, baseline, binding, scope, current)
	}
	return newClusterSSHPrejoinCohortPreparationWorker(store, aegis, dispatch, getJSON)
}

type clusterSSHPrejoinWorkerSourceConsumer = func(context.Context, *clusterbootstrap.PreparationInputs, *clusterbootstrap.PreparationArtifacts,
	string, clusterSSHPreparedImageCapacity, clusterbootstrap.PreparationExpected, clusterSSHPreparationChecks) error

type clusterSSHPrejoinWorkerNativeDispatch func(context.Context, *clusterbootstrap.PreparationInputs, *clusterbootstrap.PreparationArtifacts,
	string, clusterbootstrap.PreparationExpected, clusterSSHPreparationChecks) (clusterbootstrap.PrejoinPreparationReceipt, error)

// Private composition seam keeps the actual immutable acquisition and native
// transport fixed above. A receipt escapes only after source/artifact/identity
// cleanup and all current-authority callbacks have joined successfully.
func dispatchClusterSSHPrejoinWorker(parent context.Context, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	confirmation string, source clusterSSHPrejoinSource, authority clusterSSHPreparationAuthorityRead,
	acquire func(context.Context, clusterSSHPrejoinWorkerSourceConsumer) error, native clusterSSHPrejoinWorkerNativeDispatch,
	current func(context.Context) error) (result clusterSSHPrejoinDispatchResult, resultErr error) {
	if native == nil {
		return result, clusterbootstrap.ErrSessionAuthority
	}
	err := withClusterSSHPrejoinWorkerInputs(parent, claim, baseline, confirmation, source, authority, acquire,
		func(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts,
			postgres string, expected clusterbootstrap.PreparationExpected, checks clusterSSHPreparationChecks) error {
			owned := expected
			owned.PeerAddresses = slices.Clone(expected.PeerAddresses)
			receipt, err := native(ctx, inputs, artifacts, postgres, expected, checks)
			if err != nil || receipt.Validate(owned, source.Binding) != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			result = clusterSSHPrejoinDispatchResult{Expected: owned, Receipt: receipt}
			return nil
		}, current)
	if err != nil {
		return clusterSSHPrejoinDispatchResult{}, err
	}
	return result, nil
}

type clusterSSHPrejoinWorkerInputConsumer func(context.Context, *clusterbootstrap.PreparationInputs, *clusterbootstrap.PreparationArtifacts,
	string, clusterbootstrap.PreparationExpected, clusterSSHPreparationChecks) error

func withClusterSSHPrejoinWorkerInputs(parent context.Context, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	confirmation string, source clusterSSHPrejoinSource, authority clusterSSHPreparationAuthorityRead,
	acquire func(context.Context, clusterSSHPrejoinWorkerSourceConsumer) error, consume clusterSSHPrejoinWorkerInputConsumer,
	current func(context.Context) error) (resultErr error) {
	fail := clusterbootstrap.ErrSessionAuthority
	if parent.Err() != nil || !validClusterSSHPreparationLease(claim.Lease) || baseline.Validate() != nil ||
		!validClusterSSHPreparationDigest(confirmation) || !source.validIdentity() || authority == nil || acquire == nil || consume == nil || current == nil {
		return fail
	}
	ctx, cancel := context.WithCancel(parent)
	lifetime := &clusterSSHNetworkReaderLifetime{ctx: ctx, cancel: cancel}
	defer func() {
		if lifetime.close() != nil {
			resultErr = fail
		}
	}()
	read := func(ctx context.Context) (clusterSSHPreparationAuthority, error) {
		value, err := authority(ctx)
		if err != nil || !source.valid(value) || value.Lease != claim.Lease || value.Baseline != baseline {
			return clusterSSHPreparationAuthority{}, fail
		}
		proof, _, err := parseClusterSSHPreparationInspection([]byte(value.payload), claim.Lease, baseline)
		if err != nil {
			return clusterSSHPreparationAuthority{}, fail
		}
		authorized, _, err := parseClusterSSHPreparationAuthorization(value.payload, proof, value.Queue)
		var raw json.RawMessage
		if err != nil || authorized.ConfirmationSHA256 != confirmation ||
			clusterBootstrapObject([]byte(value.payload), map[string]any{"ssh_preparation_confirmation": &raw}) != nil {
			return clusterSSHPreparationAuthority{}, fail
		}
		confirmed, err := parseClusterSSHPreparationConfirmation(raw, authorized, value.Source)
		if err != nil || confirmed.IdentitySource != source.Binding {
			return clusterSSHPreparationAuthority{}, fail
		}
		return value, nil
	}
	// Freeze current complete authority while reusing the worker's whole-cohort
	// renewal at each boundary. Never replace it with this target's receipt.
	boundary := newClusterSSHPreparationLeaseCheck(read, current)
	check := func(ctx context.Context) error { return lifetime.run(ctx, boundary) }
	if check(ctx) != nil {
		return fail
	}
	var called atomic.Bool
	err := acquire(ctx, func(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts,
		postgres string, _ clusterSSHPreparedImageCapacity, expected clusterbootstrap.PreparationExpected, checks clusterSSHPreparationChecks) error {
		if !called.CompareAndSwap(false, true) || inputs == nil || artifacts == nil || checks.Inputs == nil || checks.Authority == nil || checks.K3sJoin == nil ||
			expected.Validate() != nil || !clusterbootstrap.ValidPostgresImageReference(postgres) || check(ctx) != nil {
			cancel()
			return fail
		}
		value, err := read(ctx)
		if err != nil {
			cancel()
			return fail
		}
		if _, err := clusterSSHPrejoinDispatchKey(value, source, claim, baseline, expected); err != nil {
			cancel()
			return fail
		}
		expected.PeerAddresses = slices.Clone(expected.PeerAddresses)
		wrap := func(prove func(context.Context) error) func(context.Context) error {
			return func(caller context.Context) error {
				return lifetime.run(caller, func(bound context.Context) error {
					if check(bound) != nil || prove(bound) != nil || check(bound) != nil {
						return fail
					}
					return nil
				})
			}
		}
		joined := checks.K3sJoin
		checks.Inputs, checks.Authority = wrap(checks.Inputs), wrap(checks.Authority)
		checks.K3sJoin = func(caller context.Context, binding engineidentity.Binding, consume clusterSSHJoinMaterialConsumer) error {
			return lifetime.run(caller, func(bound context.Context) error {
				if binding != source.Binding || consume == nil || check(bound) != nil {
					return fail
				}
				if joined(bound, binding, func(ctx context.Context, material *clusterbootstrap.K3sJoinMaterial, joinCheck func(context.Context) error) error {
					if joinCheck == nil || check(ctx) != nil {
						return fail
					}
					return consume(ctx, material, wrap(joinCheck))
				}) != nil || check(bound) != nil {
					return fail
				}
				return nil
			})
		}
		if checks.Inputs(ctx) != nil {
			return fail
		}
		nativeExpected := expected
		nativeExpected.PeerAddresses = slices.Clone(expected.PeerAddresses)
		if consume(ctx, inputs, artifacts, postgres, nativeExpected, checks) != nil || checks.Inputs(ctx) != nil || ctx.Err() != nil {
			cancel()
			return fail
		}
		return nil
	})
	if err != nil || !called.Load() || check(ctx) != nil || ctx.Err() != nil {
		return fail
	}
	return nil
}
