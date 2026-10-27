package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"context"
)

type clusterSSHK3sStartupClient interface {
	StartPreparedK3s(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, *clusterbootstrap.PreparationArtifacts, string, *clusterbootstrap.K3sJoinMaterial, []byte, func(context.Context) error) error
	ReconcilePreparedK3sStart(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, *clusterbootstrap.PreparationArtifacts, string, *clusterbootstrap.K3sJoinMaterial, []byte, func(context.Context) error) error
}

var _ clusterSSHK3sStartupClient = (*clusterremote.Client)(nil)

// Caller must acquire inputs with the explicit joining source scope. This
// delivers startup only; import/cohort admission and identity release remain
// separate. Production worker polling is not enabled by this entrypoint.
func withClusterSSHPreparedK3sStartupDispatch(ctx context.Context, store *postgresOperatorStore, aegis *goAegisService,
	source clusterSSHPrejoinSource, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgres string, expected clusterbootstrap.PreparationExpected, checks clusterSSHPreparationChecks) error {
	if checks.K3sJoin == nil || checks.Authority == nil || artifacts == nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	return withClusterSSHPrejoinStartupDispatch(ctx, store, aegis, source, claim, baseline, inputs, artifacts, postgres, expected, checks.Authority, checks.K3sJoin, true)
}
