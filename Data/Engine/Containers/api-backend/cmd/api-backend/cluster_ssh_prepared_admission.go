package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
)

// Original settled-controller read scope for the admission boundary. Every
// observer must prove direct local linearizable completion and common etcd
// cluster identity. This does not mutate admission, grant a durable capability,
// or extend the existing 60-second scope over admission deployment/soak.
func (c *clusterController) withClusterSSHPreparedAdmissionProof(parent context.Context, operation clusterControllerOperation,
	consume func(context.Context, clusterSSHPreparedCohortMembershipObservation, clusterSSHPreparedMembershipChecks) error) error {
	if c == nil || parent.Err() != nil {
		return errClusterConflict
	}
	runner, ok := c.runner.(*kubernetesClusterStepRunner)
	if !ok || runner == nil || runner.controllerHolder != c.holder {
		return errClusterConflict
	}
	holder := c.holder
	read := func(ctx context.Context) (clusterSSHPreparedAuthority, int64, error) {
		if c.holder != holder || c.runner != runner || runner.controllerHolder != holder {
			return clusterSSHPreparedAuthority{}, 0, errClusterConflict
		}
		return c.loadClusterSSHPreparedAuthority(ctx, operation)
	}
	return withClusterSSHPreparedMembershipScopeForProof(parent, operation, holder, runner, read, clusterbootstrap.MembershipQuorumProofVersion, consume)
}
