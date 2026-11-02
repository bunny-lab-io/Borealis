package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
)

// Deliberately excludes DeliverPreparation and every later native phase.
// Reconciliation may persist a native intent's exact result, never replay it.
type clusterSSHPreparationRecoverySourceStageClient interface {
	InspectPreparationProgress(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) (clusterbootstrap.PreparationProgress, error)
	ReconcilePreparation(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	ReconcilePreparationAbsent(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	Close() error
}

type clusterSSHPreparationRecoverySourceStageDependencies struct {
	source      func(clusterSSHPreparationRecoveryAuthority, string, func(context.Context) error) clusterSSHPreparationRecoverySourceRead
	download    func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error)
	connect     func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationRecoverySourceStageClient, error)
	closeInputs func(*clusterbootstrap.PreparationInputs) error
}

// Private evidence only. No receipt here authorizes source delivery, SQL
// settlement, later host preparation, membership or a new recovery generation.
type clusterSSHPreparationRecoverySourceStageResult struct {
	TargetID   string
	Resolution string
}

func connectClusterSSHPreparationRecoverySourceStage(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationRecoverySourceStageClient, error) {
	client, err := (clusterremote.Transport{}).Connect(ctx, target, key, credential)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// Callable source-only reconciliation. The full original cohort is inspected
// before any journal resolution. Empty/later journals stay blocked. Original
// readiness digests are not fresh preparation gates; delivery remains absent.
func (c *clusterController) recoverClusterSSHPreparationSourceStage(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryClaim, scratchParent string) ([]clusterSSHPreparationRecoverySourceStageResult, error) {
	return c.recoverClusterSSHPreparationSourceStageWith(parent, aegis, issued, scratchParent, clusterSSHPreparationRecoverySourceStageDependencies{
		source: c.preparationRecoverySourceRead, download: prepareClusterSSHBootstrap, connect: connectClusterSSHPreparationRecoverySourceStage,
	})
}

func (c *clusterController) recoverClusterSSHPreparationSourceStageWith(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryClaim, scratchParent string, dependencies clusterSSHPreparationRecoverySourceStageDependencies) ([]clusterSSHPreparationRecoverySourceStageResult, error) {
	claim, _, err := freezeClusterSSHPreparationRecoveryClaim(issued)
	if err != nil {
		_ = c.withClusterSSHPreparationRecoverySourceReconciliation(parent, aegis, issued, nil)
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	var result []clusterSSHPreparationRecoverySourceStageResult
	err = c.withClusterSSHPreparationRecoverySourceReconciliation(parent, aegis, claim,
		func(ctx context.Context, authority clusterSSHPreparationRecoveryAuthority, check func(context.Context) error) error {
			if dependencies.source == nil || dependencies.download == nil || dependencies.connect == nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			read := dependencies.source(authority, claim.payload, check)
			if read == nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			progressDependencies := clusterSSHPreparationRecoveryProgressDependencies{
				download: dependencies.download, closeInputs: dependencies.closeInputs,
				connect: func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
					return dependencies.connect(ctx, target, key, credential)
				},
			}
			for i, target := range claim.Claims {
				progress, err := inspectClusterSSHPreparationRecoveryTarget(ctx, authority, i, target.Sealed, aegis, scratchParent, read, check, progressDependencies)
				if err != nil || !clusterSSHSourceStageCursor(progress) {
					return clusterbootstrap.ErrPreparationConfig
				}
			}
			for i, target := range claim.Claims {
				var resolution string
				err := withClusterSSHPreparationRecoveryTargetInputs(ctx, authority, i, target.Sealed, aegis, scratchParent, read, check,
					dependencies.download, dependencies.closeInputs, func(inputs clusterSSHPreparationRecoveryTargetInputs) error {
						var err error
						resolution, err = reconcileClusterSSHPreparationSourceStage(ctx, inputs, dependencies.connect)
						return err
					})
				if err != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				result = append(result, clusterSSHPreparationRecoverySourceStageResult{TargetID: target.Lease.TargetID, Resolution: resolution})
			}
			if _, err := read(ctx); err != nil || check(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		})
	if err != nil {
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	return result, nil
}

func clusterSSHSourceStageCursor(progress clusterbootstrap.PreparationProgress) bool {
	return progress.Step == "stage_source" || progress.Step == "source_absent"
}

func reconcileClusterSSHPreparationSourceStage(ctx context.Context, target clusterSSHPreparationRecoveryTargetInputs,
	connect func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationRecoverySourceStageClient, error)) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	if connect == nil || ctx.Err() != nil {
		return fail()
	}
	run := func(action func(clusterSSHPreparationRecoverySourceStageClient) error) (error, error) {
		if target.current(ctx) != nil || ctx.Err() != nil {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		client, err := connect(ctx, target.target, target.key, target.credential)
		if client == nil {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		// A dial failure is a boundary failure, including an owned connection
		// returned with an error. Always join that connection's cleanup first.
		actionErr, boundaryErr := runClusterSSHPreparationRecoverySession(ctx, target, client.Close, func() error {
			if err != nil {
				return err
			}
			return action(client)
		})
		if err != nil {
			boundaryErr = clusterbootstrap.ErrPreparationConfig
		}
		return actionErr, boundaryErr
	}
	var progress clusterbootstrap.PreparationProgress
	inspect := func(client clusterSSHPreparationRecoverySourceStageClient) error {
		var err error
		progress, err = client.InspectPreparationProgress(ctx, target.inputs, target.expected, target.sudo, target.current)
		return err
	}
	if err, boundary := run(inspect); err != nil || boundary != nil || !clusterSSHSourceStageCursor(progress) {
		return fail()
	}
	if progress.Step == "stage_source" {
		err, boundary := run(func(client clusterSSHPreparationRecoverySourceStageClient) error {
			return client.ReconcilePreparation(ctx, target.inputs, target.expected, target.sudo, target.current)
		})
		if boundary != nil {
			return fail()
		}
		if err == nil {
			return "applied", nil
		}
	}
	// Failed completion alone proves nothing. A different supervised session
	// must prove destination absence and original executor quiescence, retaining
	// the original input/producer record. Never turn absence into a delivery.
	if err, boundary := run(func(client clusterSSHPreparationRecoverySourceStageClient) error {
		return client.ReconcilePreparationAbsent(ctx, target.inputs, target.expected, target.sudo, target.current)
	}); err != nil || boundary != nil {
		return fail()
	}
	if err, boundary := run(inspect); err != nil || boundary != nil || progress.Step != "source_absent" {
		return fail()
	}
	return "not_applied", nil
}
