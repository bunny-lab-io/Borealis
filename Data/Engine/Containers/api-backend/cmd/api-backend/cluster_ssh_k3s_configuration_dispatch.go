package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"context"
)

type clusterSSHK3sConfigurationClient interface {
	ConfigurePrejoinK3s(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, *clusterbootstrap.PreparationArtifacts, string, *clusterbootstrap.K3sJoinMaterial, []byte, func(context.Context) error) error
	ReconcilePrejoinK3sConfiguration(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, *clusterbootstrap.PreparationArtifacts, string, *clusterbootstrap.K3sJoinMaterial, []byte, func(context.Context) error) error
}

var _ clusterSSHK3sConfigurationClient = (*clusterremote.Client)(nil)

func withClusterSSHPreparedK3sDispatch(ctx context.Context, store *postgresOperatorStore, aegis *goAegisService,
	source clusterSSHPrejoinSource, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgres string, expected clusterbootstrap.PreparationExpected, checks clusterSSHPreparationChecks) error {
	if checks.K3sJoin == nil || checks.Authority == nil || artifacts == nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	return withClusterSSHPrejoinConfigurationDispatch(ctx, store, aegis, source, claim, baseline, inputs, artifacts, postgres, expected, checks.Authority, checks.K3sJoin)
}

func clusterSSHK3sConfigurationDemands(expected clusterbootstrap.PreparationExpected) ([]clusterSSHFilesystemDemand, error) {
	config, err := clusterbootstrap.PrejoinK3sJoinConfiguration(expected)
	if err != nil {
		return nil, err
	}
	// Exact generated YAML plus protocol's maximum secure token. Reserve before
	// private acquisition; do not read source credentials early to measure them.
	return []clusterSSHFilesystemDemand{{Path: "/etc/rancher/k3s", Bytes: uint64(len(config) + clusterbootstrap.MaxK3sJoinTokenBytes), Entries: 2}}, nil
}
