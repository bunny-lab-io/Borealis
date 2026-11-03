package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"reflect"
	"time"
)

type clusterSSHPreparationRecoveryReissuedSourceStageDependencies struct {
	source      func(clusterSSHPreparationRecoveryReissuedProofAuthority, string, func(context.Context) error) clusterSSHPreparationRecoverySourceRead
	download    func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error)
	connect     func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationRecoverySourceStageClient, error)
	closeInputs func(*clusterbootstrap.PreparationInputs) error
}

type clusterSSHPreparationRecoveryReissuedSourceReconciliationConsumer func(context.Context, clusterSSHPreparationRecoveryReissuedProofAuthority, func(context.Context) error) error

// Separate source-reconciliation entry shares only the exact typed lease
// mechanics. Native reconciliation can persist an existing journal intent's
// result; no source delivery, later preparation or SQL settlement is supplied.
func (c *clusterController) withClusterSSHPreparationRecoveryReissuedSourceReconciliation(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryReissuedProofClaim, consume clusterSSHPreparationRecoveryReissuedSourceReconciliationConsumer) error {
	return c.withClusterSSHPreparationRecoveryReissuedProofEvery(parent, aegis, issued, time.Second, clusterSSHPreparationRecoveryReissuedProofConsumer(consume))
}

func (c *clusterController) recoverClusterSSHPreparationReissuedSourceStage(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryReissuedProofClaim, scratchParent string) ([]clusterSSHPreparationRecoverySourceStageResult, error) {
	return c.recoverClusterSSHPreparationReissuedSourceStageWith(parent, aegis, issued, scratchParent, clusterSSHPreparationRecoveryReissuedSourceStageDependencies{
		source: c.preparationRecoveryReissuedSourceRead, download: prepareClusterSSHBootstrap, connect: connectClusterSSHPreparationRecoverySourceStage,
	})
}

func (c *clusterController) recoverClusterSSHPreparationReissuedSourceStageWith(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryReissuedProofClaim, scratchParent string, dependencies clusterSSHPreparationRecoveryReissuedSourceStageDependencies) ([]clusterSSHPreparationRecoverySourceStageResult, error) {
	claim, _, err := freezeClusterSSHPreparationRecoveryReissuedProof(issued)
	if err != nil {
		_ = c.withClusterSSHPreparationRecoveryReissuedSourceReconciliation(parent, aegis, issued, nil)
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	var result []clusterSSHPreparationRecoverySourceStageResult
	err = c.withClusterSSHPreparationRecoveryReissuedSourceReconciliation(parent, aegis, claim,
		func(ctx context.Context, authority clusterSSHPreparationRecoveryReissuedProofAuthority, check func(context.Context) error) error {
			var err error
			result, err = reconcileClusterSSHPreparationRecoveryReissuedSourceCohort(ctx, authority, claim, aegis, scratchParent, check, dependencies)
			return err
		})
	if err != nil {
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	return result, nil
}

// Called only within the separately named SQL-issued reconciliation scope.
// A cursor selects the bounded source-only proof path, never proves an effect.
// The unchanged native commands require the original stable input and stopped
// distinct producer, and may persist only that journal's proved result.
func reconcileClusterSSHPreparationRecoveryReissuedSourceCohort(ctx context.Context, authority clusterSSHPreparationRecoveryReissuedProofAuthority,
	claim clusterSSHPreparationRecoveryReissuedProofClaim, aegis clusterSSHWorkerAegis, scratchParent string, check func(context.Context) error,
	dependencies clusterSSHPreparationRecoveryReissuedSourceStageDependencies) ([]clusterSSHPreparationRecoverySourceStageResult, error) {
	fail := func() ([]clusterSSHPreparationRecoverySourceStageResult, error) {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	if !validClusterSSHPreparationRecoveryReissuedSource(authority, claim.payload) || !reflect.DeepEqual(claim.Lineage, authority.Lineage) ||
		claim.Baseline != authority.Baseline || len(claim.Claims) != len(authority.Leases) || aegis == nil || check == nil ||
		dependencies.source == nil || dependencies.download == nil || dependencies.connect == nil {
		return fail()
	}
	_, _, tip := authority.Lineage.tip()
	for i, target := range claim.Claims {
		digest, err := clusterSSHPreparationDigest([]string{target.Sealed.generation, target.Sealed.ciphertext})
		if err != nil || target.Lease != authority.Leases[i] || target.Previous != tip[i] ||
			target.Sealed.binding != authority.Cohort.Targets[i].Binding || digest != authority.Authorization.Targets[i].CredentialSHA256 {
			return fail()
		}
	}
	read := dependencies.source(authority, claim.payload, check)
	if read == nil {
		return fail()
	}
	progressDependencies := clusterSSHPreparationRecoveryReissuedProgressDependencies{
		download: dependencies.download, closeInputs: dependencies.closeInputs,
		connect: func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
			return dependencies.connect(ctx, target, key, credential)
		},
	}
	// Finish inspection and cleanup of the complete original cohort before any
	// native result can be persisted, including when the final sibling rejects.
	for i, target := range claim.Claims {
		progress, err := inspectClusterSSHPreparationRecoveryReissuedTarget(ctx, authority, claim.payload, i, target.Sealed,
			aegis, scratchParent, read, check, progressDependencies)
		if err != nil || !clusterSSHSourceStageCursor(progress) {
			return fail()
		}
	}
	var result []clusterSSHPreparationRecoverySourceStageResult
	for i, target := range claim.Claims {
		var resolution string
		err := withClusterSSHPreparationRecoveryReissuedTargetInputs(ctx, authority, claim.payload, i, target.Sealed,
			aegis, scratchParent, read, check, dependencies.download, dependencies.closeInputs,
			func(inputs clusterSSHPreparationRecoveryTargetInputs) error {
				var err error
				resolution, err = reconcileClusterSSHPreparationSourceStage(ctx, inputs, dependencies.connect)
				return err
			})
		if err != nil {
			return fail()
		}
		result = append(result, clusterSSHPreparationRecoverySourceStageResult{TargetID: target.Lease.TargetID, Resolution: resolution})
	}
	if _, err := read(ctx); err != nil || check(ctx) != nil {
		return fail()
	}
	return result, nil
}
