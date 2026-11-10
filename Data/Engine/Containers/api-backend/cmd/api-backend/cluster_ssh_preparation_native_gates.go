package main

import (
	"context"
	"errors"
)

type clusterSSHPreparationNativeGateConsumer func(context.Context, clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error)

// Return fresh fixed diagnostics, not proof receipts or a caller-controlled
// readiness override. Claimed observers cannot break the approval/claim cycle.
func clusterSSHPreparationNativeBlockers(cleanTargets bool) []clusterSSHPreparationGateBlocked {
	var blocked []clusterSSHPreparationGateBlocked
	if !cleanTargets {
		blocked = append(blocked, clusterSSHPreparationGateBlocked{"clean_targets", "Fresh pinned-SSH proposal observation is unavailable through the authenticated API executor; its original credentials and unlocked Aegis must remain bound to the waiting proposal."})
	}
	blocked = append(blocked,
		clusterSSHPreparationGateBlocked{"complete_capacity", "Preclaim capacity proof is incomplete: bind target kubelet reservations/effective allocatable CPU, target Longhorn CPU override policy and complete inherited-profile workload demand to the waiting proposal. Existing claimed sizing scopes cannot authorize approval."},
		clusterSSHPreparationGateBlocked{"persistent_storage", "Preclaim storage proof is incomplete: bind complete installation/download/runtime demand and fresh persistent filesystem, mount and inode evidence to every original target. Conditional claimed storage budgets cannot authorize approval."},
	)
	if !cleanTargets {
		blocked = append(blocked, clusterSSHPreparationGateBlocked{"supervised_recovery", "Fresh proposal-v2 systemd/cgroup and prior journal/producer absence proof is unavailable through the authenticated API executor; preparation cannot rely on an older clean-host receipt."})
	}
	return blocked
}

func clusterSSHPreparationNativeBlockedError(cleanTargets bool) error {
	blocked := clusterSSHPreparationNativeBlockers(cleanTargets)
	errs := make([]error, len(blocked))
	for i := range blocked {
		errs[i] = blocked[i]
	}
	return errors.Join(errs...)
}

// This is the production assembly boundary, not a new readiness framework.
// The controller borrows a proposal-bound clean-target capability from its API
// executor; no Aegis key, private identity or SSH credential enters the controller.
// Known missing gates stop before release-sized artifact acquisition. When the
// native producers exist, acquisition owns the immutable gate throughout the
// synchronous approval callback and verifies its exact returned readiness.
func (c *clusterController) withPreparationNativeGates(ctx context.Context, snapshot clusterSSHPreparationApprovalSnapshot,
	scratch string, cleanTargets func(context.Context, string) (string, error), consume clusterSSHPreparationNativeGateConsumer) error {
	if c == nil || consume == nil || ctx.Err() != nil || c.holder != snapshot.work.Authorization.ControllerHolder {
		return errClusterConflict
	}
	if _, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization); err != nil || c.preparationCleanTargetsProposalCheck(snapshot)(ctx) != nil {
		return errClusterConflict
	}
	if err := clusterSSHPreparationNativeBlockedError(cleanTargets != nil); err != nil {
		return err
	}
	return c.withPreparationArtifacts(ctx, snapshot, scratch, func(ctx context.Context, artifacts clusterSSHPreparationArtifactGate) (clusterSSHPreparationReadinessReceipt, error) {
		gates := clusterSSHPreparationNativeGates(snapshot, artifacts, cleanTargets, c.preparationConfigurationRead(snapshot))
		return consumeClusterSSHPreparationNativeGates(ctx, gates, consume)
	})
}

// Artifact ownership encloses every complete native callback, including target
// reads after byte proof. Join escaped/blocked callbacks before returning to the
// artifact owner's terminal verification and cleanup.
func consumeClusterSSHPreparationNativeGates(parent context.Context, gates clusterSSHPreparationGates,
	consume clusterSSHPreparationNativeGateConsumer) (receipt clusterSSHPreparationReadinessReceipt, result error) {
	if parent.Err() != nil || consume == nil {
		return receipt, errClusterConflict
	}
	ctx, cancel := context.WithCancel(parent)
	lifetime := &clusterSSHNetworkReaderLifetime{ctx: ctx, cancel: cancel}
	defer func() {
		if lifetime.close() != nil || parent.Err() != nil {
			result = errors.Join(result, errClusterConflict)
		}
	}()
	var scoped clusterSSHPreparationGates
	for i, observe := range gates {
		scoped[i] = func(caller context.Context, plan string) (string, error) {
			var digest string
			var observedErr error
			err := lifetime.run(caller, func(ctx context.Context) error {
				if observe == nil {
					return errClusterConflict
				}
				digest, observedErr = observe(ctx, plan)
				if observedErr != nil || !validClusterSSHPreparationDigest(digest) {
					return errClusterConflict
				}
				return nil
			})
			if err != nil {
				return "", clusterSSHPreparationGateError(i, observedErr)
			}
			return digest, nil
		}
	}
	return consume(ctx, scoped)
}

// Keep the exact six existing gate positions. Missing producers always return
// an error with no digest; qualification/profile minima and source-only storage
// observations are never promoted into complete readiness evidence.
func clusterSSHPreparationNativeGates(snapshot clusterSSHPreparationApprovalSnapshot, artifacts clusterSSHPreparationArtifactGate,
	cleanTargets, configuration func(context.Context, string) (string, error)) clusterSSHPreparationGates {
	var gates clusterSSHPreparationGates
	gates[0], gates[1], gates[4] = artifacts, cleanTargets, configuration
	gates[5] = newClusterSSHPreparationSupervisedRecoveryRead(snapshot, artifacts, cleanTargets)
	for _, blocked := range clusterSSHPreparationNativeBlockers(cleanTargets != nil) {
		for i, code := range clusterSSHPreparationGateCodes {
			if code == blocked.Gate {
				gates[i] = func(context.Context, string) (string, error) { return "", blocked }
			}
		}
	}
	return gates
}
