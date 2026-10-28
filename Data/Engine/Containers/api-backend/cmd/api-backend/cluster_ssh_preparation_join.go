package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"slices"
)

// Acquire only when the later join phase asks. Earlier artifact/service stages
// neither read root join credentials nor hold stale tokens throughout staging.
// Source networks are owned snapshots from original controller, never caller
// addresses. Current confirmed source/claim is checked independently by client.
func newClusterSSHPreparationJoinRead(scope context.Context, client *clusterSSHJoinBrokerClient,
	expected clusterbootstrap.PreparationExpected, networks []clusterbootstrap.SourceNetwork,
	lease clusterSSHTargetLease, baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials,
	authority clusterSSHPreparationAuthorityRead, preparation func(context.Context) error) func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error {
	expected.PeerAddresses = slices.Clone(expected.PeerAddresses)
	networks = slices.Clone(networks)
	return func(caller context.Context, source engineidentity.Binding, consume clusterSSHJoinMaterialConsumer) error {
		if scope.Err() != nil || caller.Err() != nil || preparation == nil || client == nil {
			return clusterbootstrap.ErrK3sJoinMaterial
		}
		ctx, cancel := context.WithCancel(scope)
		defer cancel()
		interrupt := context.AfterFunc(caller, cancel)
		defer interrupt()
		var network clusterbootstrap.SourceNetwork
		found := 0
		for _, observed := range networks {
			if observed.NodeUID == source.SourceUID {
				network = observed
				found++
			}
		}
		if found != 1 || preparation(ctx) != nil {
			return clusterbootstrap.ErrK3sJoinMaterial
		}
		return client.withMaterial(ctx, expected, source, network, lease, baseline, sealed, authority, preparation, consume)
	}
}
