package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
)

// Only the explicit joining path wraps controller-native reads. It preserves
// all other paths and freezes current authority before projecting approved
// registering targets out of original-source inventory. The caller still owns
// durable startup intent; this read capability cannot authorize mutation.
func newClusterSSHJoiningProjection(authority clusterSSHPreparationAuthorityRead, getJSON func(context.Context, string, any) error) func(context.Context, string, any) error {
	gate := make(chan struct{}, 1)
	digest := ""
	registered := map[string]string{}
	failed := false
	return func(ctx context.Context, path string, out any) error {
		if authority == nil || getJSON == nil || ctx.Err() != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		if path != "/api/v1/nodes" {
			return getJSON(ctx, path, out)
		}
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return clusterbootstrap.ErrSessionAuthority
		}
		fail := func() error { failed = true; return clusterbootstrap.ErrSessionAuthority }
		if failed || ctx.Err() != nil {
			return fail()
		}
		current, err := authority(ctx)
		if err != nil || !validClusterSSHPreparationLease(current.Lease) || current.Recovery != nil || current.IdentitySource == nil || current.Baseline.Validate() != nil ||
			validateClusterSSHInspectionCohort(current.Cohort, current.Source) != nil || current.K3sVersion != clusterbootstrap.K3sPins().Version || !validClusterSSHPreparationDigest(current.PayloadSHA256) ||
			current.Lease.ControllerHolder != current.Cohort.ControllerHolder || current.Lease.OperationID != current.Cohort.OperationID || current.Lease.OperationAttempt != current.Cohort.Attempt {
			return fail()
		}
		for _, id := range []string{current.Source.ClusterID, current.Source.KubeSystemUID, current.IdentitySource.ClusterID, current.IdentitySource.SourceID, current.IdentitySource.SourceUID} {
			if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
				return fail()
			}
		}
		for _, member := range current.Source.Members {
			for _, id := range []string{member.NodeID, member.NodeUID, member.BootID} {
				if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
					return fail()
				}
			}
		}
		selected, claimed := false, false
		for _, member := range current.Source.Members {
			if current.IdentitySource.ClusterID == current.Source.ClusterID && current.IdentitySource.SourceID == member.NodeID && current.IdentitySource.SourceUID == member.NodeUID {
				selected = true
			}
		}
		for _, target := range current.Cohort.Targets {
			if target.Binding.TargetID == current.Lease.TargetID && current.Lease.Generation > target.Generation {
				claimed = true
			}
		}
		if !selected || !claimed {
			return fail()
		}
		current.Cohort.ObservedAt = 0
		raw, err := json.Marshal(current)
		if err != nil || len(raw) > 256<<10 {
			return fail()
		}
		var frozen clusterSSHPreparationAuthority
		if json.Unmarshal(raw, &frozen) != nil {
			return fail()
		}
		before, err := clusterSSHPreparationDigest(frozen)
		if err != nil || digest != "" && digest != before {
			return fail()
		}
		originals, nodes, err := projectClusterSSHJoiningNodes(ctx, frozen, getJSON)
		if err != nil {
			return fail()
		}
		for id, uid := range registered {
			if nodes[id] != uid {
				return fail()
			}
		}
		after, err := authority(ctx)
		if err != nil || ctx.Err() != nil {
			return fail()
		}
		after.Cohort.ObservedAt = 0
		afterDigest, err := clusterSSHPreparationDigest(after)
		if err != nil || afterDigest != before {
			return fail()
		}
		wire, err := json.Marshal(map[string]any{"items": originals})
		if err != nil || json.Unmarshal(wire, out) != nil {
			return fail()
		}
		digest, registered = before, nodes
		return nil
	}
}
