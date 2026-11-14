package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"reflect"
	"slices"
)

type clusterSSHPrejoinTargetScope func(context.Context, int, func(context.Context, clusterbootstrap.PreparationExpected, clusterSSHPrejoinNativeTarget) error) error

// First prepare every original target, without starting any. Reopened scopes
// retain all source/artifact/identity custody across each start. The immediate
// pre-start callback independently re-proves every target; a prior successful
// callback or progress cursor never substitutes for this native observation.
// Full source snapshots bracket each complete native proof barrier and the
// selected target's completion. During sessions, only bounded authority and
// private identity checks run; snapshot Jobs never enter transport challenges.
func dispatchClusterSSHPrejoinCohort(parent context.Context, claims []clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	source engineidentity.Binding, scope clusterSSHPrejoinTargetScope, current func(context.Context) error) ([]clusterSSHPrejoinDispatchResult, error) {
	fail := clusterbootstrap.ErrSessionAuthority
	if parent.Err() != nil || len(claims) < 1 || len(claims) > 2 || baseline.Validate() != nil || scope == nil || current == nil {
		return nil, fail
	}
	claims = slices.Clone(claims)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	results := make([]clusterSSHPrejoinDispatchResult, len(claims))
	complete := make([]bool, len(claims))
	seen := map[string]bool{}
	for _, claim := range claims {
		if !validClusterSSHPreparationLease(claim.Lease) || seen[claim.Lease.TargetID] {
			return nil, fail
		}
		seen[claim.Lease.TargetID] = true
	}
	check := func(ctx context.Context) error {
		if ctx.Err() != nil || current(ctx) != nil {
			cancel()
			return fail
		}
		return nil
	}
	for i := range claims {
		if check(ctx) != nil {
			return nil, fail
		}
		called := false
		err := scope(ctx, i, func(ctx context.Context, expected clusterbootstrap.PreparationExpected, target clusterSSHPrejoinNativeTarget) error {
			if called || target.run == nil || target.current == nil || target.inputs == nil || expected.Validate() != nil || expected.Source != baseline ||
				expected.Target.TargetID != claims[i].Lease.TargetID || expected.Target.Generation != claims[i].Lease.Generation || expected.Target.HolderID != claims[i].Lease.Holder || check(ctx) != nil {
				cancel()
				return fail
			}
			called = true
			expected.PeerAddresses = slices.Clone(expected.PeerAddresses)
			var receipt clusterbootstrap.PrejoinPreparationReceipt
			if target.inputs(ctx) != nil || target.run(ctx, true, -1, nil, check, &receipt) != nil || target.inputs(ctx) != nil || check(ctx) != nil {
				cancel()
				return fail
			}
			if receipt != (clusterbootstrap.PrejoinPreparationReceipt{}) {
				if receipt.Validate(expected, source) != nil {
					cancel()
					return fail
				}
				complete[i] = true
			}
			results[i] = clusterSSHPrejoinDispatchResult{Expected: expected, Receipt: receipt}
			return nil
		})
		if err != nil || !called || check(ctx) != nil {
			return nil, fail
		}
	}
	for selected := range claims {
		if complete[selected] {
			continue
		}
		live := make([]clusterSSHPrejoinNativeTarget, len(claims))
		var retain func(context.Context, int) error
		retain = func(ctx context.Context, index int) error {
			if index < len(claims) {
				called := false
				err := scope(ctx, index, func(ctx context.Context, expected clusterbootstrap.PreparationExpected, target clusterSSHPrejoinNativeTarget) error {
					if called || target.current == nil || target.inputs == nil || target.run == nil || !reflect.DeepEqual(expected, results[index].Expected) || check(ctx) != nil {
						cancel()
						return fail
					}
					called = true
					live[index] = target
					return retain(ctx, index+1)
				})
				if err != nil || !called || check(ctx) != nil {
					cancel()
					return fail
				}
				return nil
			}
			cohortCurrent := func(ctx context.Context) error {
				if check(ctx) != nil {
					return fail
				}
				for _, target := range live {
					if target.current(ctx) != nil {
						cancel()
						return fail
					}
				}
				return check(ctx)
			}
			cohortInputs := func(ctx context.Context) error {
				if cohortCurrent(ctx) != nil {
					return fail
				}
				for _, target := range live {
					if target.inputs(ctx) != nil || cohortCurrent(ctx) != nil {
						cancel()
						return fail
					}
				}
				return nil
			}
			proveAll := func(ctx context.Context) error {
				if cohortInputs(ctx) != nil {
					return fail
				}
				// Re-prove the selected target last, immediately before its start.
				order := make([]int, 0, len(claims))
				for i := range claims {
					if i != selected {
						order = append(order, i)
					}
				}
				order = append(order, selected)
				for _, i := range order {
					step := 7 // Exact configured prefix; never a cursor.
					if complete[i] {
						step = 10 // Separate typed final receipt proof.
					}
					var receipt clusterbootstrap.PrejoinPreparationReceipt
					if cohortCurrent(ctx) != nil || live[i].run(ctx, false, step, nil, cohortCurrent, &receipt) != nil ||
						complete[i] && receipt != results[i].Receipt || !complete[i] && receipt != (clusterbootstrap.PrejoinPreparationReceipt{}) || cohortCurrent(ctx) != nil {
						cancel()
						return fail
					}
				}
				return cohortInputs(ctx)
			}
			if proveAll(ctx) != nil {
				return fail
			}
			var receipt clusterbootstrap.PrejoinPreparationReceipt
			started := false
			beforeStart := func(ctx context.Context) error {
				if started || proveAll(ctx) != nil {
					cancel()
					return fail
				}
				started = true
				return nil
			}
			if live[selected].run(ctx, false, -1, beforeStart, cohortCurrent, &receipt) != nil || !started ||
				receipt.Validate(results[selected].Expected, source) != nil || cohortInputs(ctx) != nil {
				return fail
			}
			results[selected].Receipt = receipt
			return nil
		}
		if retain(ctx, 0) != nil || check(ctx) != nil {
			return nil, fail
		}
		complete[selected] = true
	}
	if check(ctx) != nil {
		return nil, fail
	}
	return results, nil
}
