package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
)

type clusterSSHPreparationRecoveryReissuedProgressDependencies struct {
	source      func(clusterSSHPreparationRecoveryReissuedProofAuthority, string, func(context.Context) error) clusterSSHPreparationRecoverySourceRead
	download    func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error)
	connect     clusterSSHPreparationRecoveryProgressConnect
	closeInputs func(*clusterbootstrap.PreparationInputs) error
}

// Callable proof-only assembly. The separate reissued handle permits only the
// existing fixed progress inspection, never reconciliation, delivery or
// settlement. Cursors remain private until all cleanup and SQL checks join.
func (c *clusterController) observeClusterSSHPreparationRecoveryReissuedProgress(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryReissuedProofClaim, scratchParent string) ([]clusterSSHPreparationRecoveryTargetProgress, error) {
	return c.observeClusterSSHPreparationRecoveryReissuedProgressWith(parent, aegis, issued, scratchParent, clusterSSHPreparationRecoveryReissuedProgressDependencies{
		source: c.preparationRecoveryReissuedSourceRead, download: prepareClusterSSHBootstrap, connect: connectClusterSSHPreparationRecoveryProgress,
	})
}

func (c *clusterController) observeClusterSSHPreparationRecoveryReissuedProgressWith(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryReissuedProofClaim, scratchParent string, dependencies clusterSSHPreparationRecoveryReissuedProgressDependencies) ([]clusterSSHPreparationRecoveryTargetProgress, error) {
	claim, _, err := freezeClusterSSHPreparationRecoveryReissuedProof(issued)
	if err != nil {
		// Copies share the issued latch, including failed entry and lost ACK.
		_ = c.withClusterSSHPreparationRecoveryReissuedProof(parent, aegis, issued, nil)
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	var result []clusterSSHPreparationRecoveryTargetProgress
	err = c.withClusterSSHPreparationRecoveryReissuedProof(parent, aegis, claim,
		func(ctx context.Context, authority clusterSSHPreparationRecoveryReissuedProofAuthority, check func(context.Context) error) error {
			if dependencies.source == nil || dependencies.download == nil || dependencies.connect == nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			read := dependencies.source(authority, claim.payload, check)
			if read == nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			for i, target := range claim.Claims {
				progress, err := inspectClusterSSHPreparationRecoveryReissuedTarget(ctx, authority, claim.payload, i, target.Sealed,
					aegis, scratchParent, read, check, dependencies)
				if err != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				result = append(result, clusterSSHPreparationRecoveryTargetProgress{TargetID: target.Lease.TargetID, Progress: progress})
			}
			// The complete original source and SQL authority must remain current
			// after every target's SSH and private-input cleanup has finished.
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

func inspectClusterSSHPreparationRecoveryReissuedTarget(ctx context.Context, authority clusterSSHPreparationRecoveryReissuedProofAuthority,
	payload string, index int, sealed sealedClusterSSHCredentials, aegis clusterSSHWorkerAegis, scratchParent string,
	source clusterSSHPreparationRecoverySourceRead, check func(context.Context) error,
	dependencies clusterSSHPreparationRecoveryReissuedProgressDependencies) (clusterbootstrap.PreparationProgress, error) {
	var progress clusterbootstrap.PreparationProgress
	err := withClusterSSHPreparationRecoveryReissuedTargetInputs(ctx, authority, payload, index, sealed, aegis, scratchParent, source, check,
		dependencies.download, dependencies.closeInputs, func(target clusterSSHPreparationRecoveryTargetInputs) error {
			if dependencies.connect == nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			client, connectErr := dependencies.connect(ctx, target.target, target.key, target.credential)
			if client == nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			actionErr, boundaryErr := runClusterSSHPreparationRecoverySession(ctx, target, client.Close, func() error {
				if connectErr != nil {
					return connectErr
				}
				var err error
				progress, err = client.InspectPreparationProgress(ctx, target.inputs, target.expected, target.sudo, target.current)
				return err
			})
			if actionErr != nil || boundaryErr != nil || progress.Validate() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		})
	if err != nil {
		return clusterbootstrap.PreparationProgress{}, err
	}
	return progress, nil
}

func withClusterSSHPreparationRecoveryReissuedTargetInputs(ctx context.Context, authority clusterSSHPreparationRecoveryReissuedProofAuthority,
	payload string, index int, sealed sealedClusterSSHCredentials, aegis clusterSSHWorkerAegis, scratchParent string,
	source clusterSSHPreparationRecoverySourceRead, check func(context.Context) error,
	download func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error),
	closeInputs func(*clusterbootstrap.PreparationInputs) error, consume func(clusterSSHPreparationRecoveryTargetInputs) error) error {
	if !validClusterSSHPreparationRecoveryReissuedSource(authority, payload) || index < 0 || index >= len(authority.Cohort.Targets) {
		return clusterbootstrap.ErrPreparationConfig
	}
	digest, err := clusterSSHPreparationDigest([]string{sealed.generation, sealed.ciphertext})
	if err != nil || sealed.binding != authority.Cohort.Targets[index].Binding || digest != authority.Authorization.Targets[index].CredentialSHA256 {
		return clusterbootstrap.ErrPreparationConfig
	}
	return withClusterSSHPreparationRecoveryProjectedInputs(ctx,
		clusterSSHPreparationRecoveryInputProjection{authority.Cohort.Targets[index], len(authority.Leases)},
		func(version, pods, services string) (clusterbootstrap.PreparationExpected, error) {
			return buildClusterSSHPreparationRecoveryReissuedExpected(authority, payload, index, version, pods, services)
		}, index, sealed, aegis, scratchParent, source, check, download, closeInputs, consume)
}
