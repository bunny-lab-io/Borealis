package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"testing"
)

func TestClusterSSHPreparedAdmissionProof(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "consumer copy", "foreign etcd cluster", "listener-only producer", "native read failure", "Node drift", "Job replay"} {
		t.Run(mode, func(t *testing.T) {
			a, authority, targets := sshMembershipAuthorityFixture(t, mode == "replacement")
			authority.payload, authority.baseline, authority.queue.ClusterID = `{"settled":"admission"}`, a.Baseline, a.Source.ClusterID
			op := clusterControllerOperation{ID: a.Lease.OperationID, Kind: "ssh_onboarding", State: "running", CurrentStep: clusterSSHPreparationOperationStep, Attempt: a.Lease.OperationAttempt}
			n := newPreparedMembershipNativeForProof(t, a, authority, targets, mode, clusterbootstrap.MembershipQuorumProofVersion)
			read := func(ctx context.Context) (clusterSSHPreparedAuthority, int64, error) {
				return authority, 100, ctx.Err()
			}
			consumed := false
			err := withClusterSSHPreparedMembershipScopeForProof(context.Background(), op, a.Lease.ControllerHolder, n.runner, read, clusterbootstrap.MembershipQuorumProofVersion,
				func(ctx context.Context, got clusterSSHPreparedCohortMembershipObservation, checks clusterSSHPreparedMembershipChecks) error {
					consumed = true
					if len(got.Views) != 3 || n.posts.Load() != 6 || checks.Authority(ctx) != nil {
						t.Fatal("missing original custody")
					}
					for _, view := range got.Views {
						if view.Observation.Quorum == nil || view.Observation.Quorum.ClusterID != 100 || view.Observation.Quorum.MemberID != view.Observation.Membership.LocalID {
							t.Fatal("missing exact responder")
						}
					}
					if mode == "consumer copy" {
						got.Views[0].Observation.Quorum.ClusterID++
					}
					n.phase.Store(1)
					return nil
				})
			if mode == "pair" || mode == "replacement" || mode == "consumer copy" {
				if err != nil || !consumed || n.posts.Load() != 12 {
					t.Fatal("complete bounded proof", err)
				}
			} else if err == nil {
				t.Fatal("incomplete admission proof accepted")
			}
			if (mode == "foreign etcd cluster" || mode == "listener-only producer") && consumed {
				t.Fatal("weak proof reached consumer")
			}
			if n.active.Load() != 0 {
				t.Fatal("HTTP cleanup escaped")
			}
		})
	}
}
