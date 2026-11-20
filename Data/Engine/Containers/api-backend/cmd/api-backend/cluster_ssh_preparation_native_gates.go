package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
)

type clusterSSHPreparationNativeGateConsumer func(context.Context, clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error)

// Return fresh fixed diagnostics, not proof receipts or a caller-controlled
// readiness override. Claimed observers cannot break the approval/claim cycle.
func clusterSSHPreparationNativeBlockers(cleanTargets, capacity, storage bool) []clusterSSHPreparationGateBlocked {
	var blocked []clusterSSHPreparationGateBlocked
	if !cleanTargets {
		blocked = append(blocked, clusterSSHPreparationGateBlocked{"clean_targets", "Fresh pinned-SSH proposal observation is unavailable through the authenticated API executor; its original credentials and unlocked Aegis must remain bound to the waiting proposal."})
	}
	if !capacity {
		blocked = append(blocked, clusterSSHPreparationGateBlocked{"complete_capacity", "Preclaim capacity proof is unavailable: bind native target CPU/memory, target Longhorn policy and complete inherited-profile workload demand to the waiting proposal and recheck before effects."})
	}
	if !storage {
		blocked = append(blocked, clusterSSHPreparationGateBlocked{"persistent_storage", "Verify usable space and inodes for installation, workload, recovery and startup headroom on each original target filesystem."})
	}
	if !cleanTargets {
		blocked = append(blocked, clusterSSHPreparationGateBlocked{"supervised_recovery", "Fresh native systemd/cgroup and prior journal/producer absence proof is unavailable through the authenticated API executor; preparation cannot rely on an older clean-host receipt."})
	}
	return blocked
}

func clusterSSHPreparationNativeBlockedError(cleanTargets, capacity, storage bool) error {
	blocked := clusterSSHPreparationNativeBlockers(cleanTargets, capacity, storage)
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
	scratch string, cleanTargets, capacity, configuration func(context.Context, string) (string, error),
	source clusterSSHPreparationStorageGateSourceRead, filesystems clusterSSHPreparationFilesystemReader, consume clusterSSHPreparationNativeGateConsumer) error {
	if c == nil || consume == nil || ctx.Err() != nil || c.holder != snapshot.work.Authorization.ControllerHolder {
		return errClusterConflict
	}
	if _, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization); err != nil || c.preparationCleanTargetsProposalCheck(snapshot)(ctx) != nil {
		return errClusterConflict
	}
	if err := clusterSSHPreparationNativeBlockedError(cleanTargets != nil, capacity != nil, source != nil && filesystems != nil); err != nil {
		return err
	}
	return c.withPreparationArtifacts(ctx, snapshot, scratch, clusterSSHPreparationArtifactConsumerForGates(snapshot,
		c.preparationCleanTargetsProposalCheck(snapshot), cleanTargets, capacity, configuration, source, filesystems, consume))
}

func clusterSSHPreparationArtifactConsumerForGates(snapshot clusterSSHPreparationApprovalSnapshot, check func(context.Context) error,
	cleanTargets, capacity, configuration func(context.Context, string) (string, error), source clusterSSHPreparationStorageGateSourceRead,
	filesystems clusterSSHPreparationFilesystemReader, consume clusterSSHPreparationNativeGateConsumer) clusterSSHPreparationArtifactConsumer {
	return func(ctx context.Context, artifacts clusterSSHPreparationArtifactGate, storageDemand clusterSSHPreparationArtifactDemandRead, helper clusterSSHPreparationArtifactGate) (clusterSSHPreparationReadinessReceipt, error) {
		storage := newClusterSSHPreparationStorageRead(snapshot, check, source, preparationWaitingStorageDemand(storageDemand), filesystems)
		gates := clusterSSHPreparationNativeGates(snapshot, artifacts, helper, cleanTargets, capacity, storage, configuration)
		return consumeClusterSSHPreparationNativeGates(ctx, gates, consume)
	}
}

// Build demand while the immutable artifact owner and the waiting proposal
// are both live. Configuration is derived without acquiring a future worker lease.
func preparationWaitingStorageDemand(artifacts clusterSSHPreparationArtifactDemandRead) clusterSSHPreparationCompleteStorageDemandRead {
	return func(ctx context.Context, digest string, plan clusterbootstrap.PreparationPlan) ([]clusterSSHFilesystemDemand, error) {
		if artifacts == nil || digest != plan.PlanSHA256 {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		known, err := artifacts(ctx, digest)
		if err != nil || len(known) == 0 {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		packages, _, err := clusterbootstrap.HostPackagePins()
		if err != nil {
			return nil, err
		}
		host, err := clusterSSHPreparationPlanHostFileDemands(plan, packages.Suite)
		if err != nil {
			return nil, err
		}
		return mergeClusterSSHFilesystemDemands(known, host, clusterSSHK3sConfigurationBudget())
	}
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
func clusterSSHPreparationNativeGates(snapshot clusterSSHPreparationApprovalSnapshot, artifacts, helper clusterSSHPreparationArtifactGate,
	cleanTargets, capacity, storage, configuration func(context.Context, string) (string, error)) clusterSSHPreparationGates {
	var gates clusterSSHPreparationGates
	gates[0], gates[1], gates[2], gates[3], gates[4] = artifacts, cleanTargets, capacity, storage, configuration
	gates[5] = newClusterSSHPreparationSupervisedRecoveryRead(snapshot, helper, cleanTargets)
	for _, blocked := range clusterSSHPreparationNativeBlockers(cleanTargets != nil, capacity != nil, storage != nil) {
		for i, code := range clusterSSHPreparationGateCodes {
			if code == blocked.Gate {
				gates[i] = func(context.Context, string) (string, error) { return "", blocked }
			}
		}
	}
	return gates
}
