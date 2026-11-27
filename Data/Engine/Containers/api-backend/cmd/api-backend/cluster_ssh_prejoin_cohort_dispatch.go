package main

import (
	"context"
	"slices"

	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
)

type clusterSSHPrejoinTargetScope func(context.Context, int, func(context.Context, clusterbootstrap.PreparationExpected, clusterSSHPrejoinNativeTarget) error) error

type clusterSSHInstallPhase int

const (
	installAllSteps clusterSSHInstallPhase = iota
	prepareBeforeJoin
	joinDrainedServer
	finishJoinedServer
)

// Hold each target's verified installation inputs once. The native journal
// verifies the configured prefix before starting a server and actual membership
// before returning from that join. Do no archive acquisition or image activation
// between the two membership changes.
func dispatchClusterSSHPrejoinCohort(ctx context.Context, claims []clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	source engineidentity.Binding, scope clusterSSHPrejoinTargetScope, current func(context.Context) error) ([]clusterSSHPrejoinDispatchResult, error) {
	fail := clusterbootstrap.ErrSessionAuthority
	if ctx.Err() != nil || len(claims) < 1 || len(claims) > 2 || baseline.Validate() != nil || scope == nil || current == nil {
		return nil, fail
	}
	for i, claim := range claims {
		if !validClusterSSHPreparationLease(claim.Lease) || i > 0 && claim.Lease.TargetID == claims[0].Lease.TargetID {
			return nil, fail
		}
	}
	targets := make([]clusterSSHPrejoinNativeTarget, len(claims))
	results := make([]clusterSSHPrejoinDispatchResult, len(claims))
	var open func(context.Context, int) error
	open = func(ctx context.Context, i int) error {
		if i == len(targets) {
			for _, phase := range []clusterSSHInstallPhase{prepareBeforeJoin, joinDrainedServer, finishJoinedServer} {
				for i, target := range targets {
					if current(ctx) != nil || target.current(ctx) != nil {
						return fail
					}
					var receipt clusterbootstrap.PrejoinPreparationReceipt
					if err := target.run(ctx, phase, &receipt); err != nil {
						return err
					}
					if phase == finishJoinedServer {
						if receipt.Validate(results[i].Expected, source) != nil {
							return fail
						}
						results[i].Receipt = receipt
					}
				}
			}
			return current(ctx)
		}
		called, completed := false, false
		err := scope(ctx, i, func(ctx context.Context, expected clusterbootstrap.PreparationExpected, target clusterSSHPrejoinNativeTarget) error {
			lease := claims[i].Lease
			if called || target.run == nil || target.current == nil || expected.Validate() != nil || expected.Source != baseline ||
				expected.Target.OperationID != lease.OperationID || expected.Target.TargetID != lease.TargetID || expected.Target.Generation != lease.Generation ||
				expected.Target.HolderID != lease.Holder || expected.Target.OperationAttempt != lease.OperationAttempt || current(ctx) != nil {
				return fail
			}
			called = true
			expected.PeerAddresses = slices.Clone(expected.PeerAddresses)
			results[i].Expected = expected
			targets[i] = target
			err := open(ctx, i+1)
			completed = err == nil
			return err
		})
		if err != nil || !completed || ctx.Err() != nil {
			return fail
		}
		return nil
	}
	if err := open(ctx, 0); err != nil {
		return nil, err
	}
	return results, nil
}
