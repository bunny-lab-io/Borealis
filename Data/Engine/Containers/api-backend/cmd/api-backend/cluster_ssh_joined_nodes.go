package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"reflect"
	"time"
)

// Read-only controller evidence. Missing/NotReady targets remain incomplete;
// observation never authorizes startup, changes source membership or settles a
// target. Final admission must match each UID to independent native target proof.
type clusterSSHJoinedTargets struct {
	AuthoritySHA256 string
	Nodes           map[string]clusterbootstrap.K3sJoinedNode
}

func readClusterSSHJoinedTargets(parent context.Context, authority clusterSSHPreparationAuthorityRead, getJSON func(context.Context, string, any) error) (clusterSSHJoinedTargets, error) {
	fail := func() (clusterSSHJoinedTargets, error) {
		return clusterSSHJoinedTargets{}, clusterbootstrap.ErrSessionAuthority
	}
	if authority == nil || getJSON == nil || parent.Err() != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	before, err := authority(ctx)
	if err != nil || !validClusterSSHPreparationLease(before.Lease) || before.Recovery != nil || before.IdentitySource == nil || before.Baseline.Validate() != nil ||
		validateClusterSSHInspectionCohort(before.Cohort, before.Source) != nil || !validClusterSSHPreparationDigest(before.PayloadSHA256) ||
		before.Lease.ControllerHolder != before.Cohort.ControllerHolder || before.Lease.OperationID != before.Cohort.OperationID || before.Lease.OperationAttempt != before.Cohort.Attempt || before.K3sVersion != clusterbootstrap.K3sPins().Version {
		return fail()
	}
	for _, id := range []string{before.Source.ClusterID, before.Source.KubeSystemUID, before.IdentitySource.ClusterID, before.IdentitySource.SourceID, before.IdentitySource.SourceUID} {
		if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return fail()
		}
	}
	for _, member := range before.Source.Members {
		for _, id := range []string{member.NodeID, member.NodeUID, member.BootID} {
			if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
				return fail()
			}
		}
	}
	selected, claimed := false, false
	for _, member := range before.Source.Members {
		if before.IdentitySource.ClusterID == before.Source.ClusterID && before.IdentitySource.SourceID == member.NodeID && before.IdentitySource.SourceUID == member.NodeUID {
			selected = true
		}
	}
	for _, target := range before.Cohort.Targets {
		if target.Binding.TargetID == before.Lease.TargetID && before.Lease.Generation > target.Generation {
			claimed = true
		}
	}
	if !selected || !claimed {
		return fail()
	}
	before.Cohort.ObservedAt = 0
	frozenRaw, err := json.Marshal(before)
	if err != nil || len(frozenRaw) > 256<<10 {
		return fail()
	}
	var frozen clusterSSHPreparationAuthority
	if json.Unmarshal(frozenRaw, &frozen) != nil {
		return fail()
	}
	digest, err := clusterSSHPreparationDigest(frozen)
	if err != nil {
		return fail()
	}
	observe := func() (map[string]clusterbootstrap.K3sJoinedNode, error) {
		targets := map[string]clusterbootstrap.K3sJoinedNode{}
		observed, err := observeClusterSSHSourceCohort(ctx, func(ctx context.Context, path string, out any) error {
			if path != "/api/v1/nodes" {
				return getJSON(ctx, path, out)
			}
			var list struct {
				APIVersion string `json:"apiVersion"`
				Kind       string `json:"kind"`
				Metadata   struct {
					Continue string `json:"continue"`
				} `json:"metadata"`
				Items *[]json.RawMessage `json:"items"`
			}
			if getJSON(ctx, path, &list) != nil || list.APIVersion != "v1" || list.Kind != "NodeList" || list.Metadata.Continue != "" || list.Items == nil || len(*list.Items) < len(frozen.Source.Members) || len(*list.Items) > len(frozen.Source.Members)+len(frozen.Cohort.Targets) {
				return clusterbootstrap.ErrSessionAuthority
			}
			originals := make([]json.RawMessage, 0, len(frozen.Source.Members))
			names, uids := map[string]bool{}, map[string]bool{}
			for _, raw := range *list.Items {
				var node struct {
					APIVersion string `json:"apiVersion"`
					Kind       string `json:"kind"`
					Metadata   struct {
						Name   string            `json:"name"`
						UID    string            `json:"uid"`
						Labels map[string]string `json:"labels"`
					} `json:"metadata"`
				}
				if len(raw) > 128<<10 || json.Unmarshal(raw, &node) != nil || node.APIVersion != "v1" || node.Kind != "Node" || names[node.Metadata.Name] || uids[node.Metadata.UID] {
					return clusterbootstrap.ErrSessionAuthority
				}
				names[node.Metadata.Name], uids[node.Metadata.UID] = true, true
				original := false
				for _, member := range frozen.Source.Members {
					if member.Name == node.Metadata.Name {
						original = true
						break
					}
				}
				if original {
					if node.Metadata.Labels["borealis.io/engine-node"] != "true" || node.Metadata.Labels["borealis.io/application-state"] != "active" {
						return clusterbootstrap.ErrSessionAuthority
					}
					originals = append(originals, raw)
					continue
				}
				found := false
				for _, target := range frozen.Cohort.Targets {
					if target.Report.Hostname != node.Metadata.Name {
						continue
					}
					identity := clusterbootstrap.K3sJoinedNodeIdentity{Hostname: target.Report.Hostname, MachineID: target.Report.MachineID, BootID: target.Report.BootID, Address: target.Binding.Address, K3sVersion: frozen.K3sVersion}
					observed, err := clusterbootstrap.ParseK3sJoinedNodeIdentity(raw, identity)
					if err != nil {
						return clusterbootstrap.ErrSessionAuthority
					}
					targets[target.Binding.TargetID], found = observed, true
				}
				if !found {
					return clusterbootstrap.ErrSessionAuthority
				}
			}
			wire, err := json.Marshal(map[string]any{"items": originals})
			if err != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			return json.Unmarshal(wire, out)
		}, frozen.Source)
		if err != nil || !reflect.DeepEqual(observed, frozen.Source) {
			return nil, clusterbootstrap.ErrSessionAuthority
		}
		var namespace struct {
			Metadata struct {
				UID string `json:"uid"`
			} `json:"metadata"`
		}
		if getJSON(ctx, "/api/v1/namespaces/kube-system", &namespace) != nil || namespace.Metadata.UID != frozen.Source.KubeSystemUID || ctx.Err() != nil {
			return nil, clusterbootstrap.ErrSessionAuthority
		}
		return targets, nil
	}
	first, err := observe()
	if err != nil {
		return fail()
	}
	second, err := observe()
	if err != nil || len(first) != len(second) {
		return fail()
	}
	for id, node := range first {
		other, ok := second[id]
		node.Ready, other.Ready = false, false
		if !ok || node != other {
			return fail()
		}
	}
	after, err := authority(ctx)
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	after.Cohort.ObservedAt = 0
	afterDigest, err := clusterSSHPreparationDigest(after)
	if err != nil || afterDigest != digest {
		return fail()
	}
	return clusterSSHJoinedTargets{digest, second}, nil
}
