package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"time"
)

// Joining custody is deliberately distinct from prejoin custody. Only a caller
// holding durable startup intent may use this observation during membership
// change. It grants no start, admission, identity release or completion itself.
// Registered contains Node UIDs, not ready Nodes; kubelet status may be pending.
type clusterSSHJoiningSourceCustody struct {
	Original   clusterSSHPrejoinSourceCustody
	Registered map[string]string
}

func readClusterSSHJoiningSourceCustody(parent context.Context, source clusterSSHPrejoinSource, authority clusterSSHPreparationAuthorityRead,
	getJSON func(context.Context, string, any) error) (clusterSSHJoiningSourceCustody, error) {
	fail := func() (clusterSSHJoiningSourceCustody, error) {
		return clusterSSHJoiningSourceCustody{}, clusterbootstrap.ErrSessionAuthority
	}
	if authority == nil || getJSON == nil || parent.Err() != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	initial, err := authority(ctx)
	if err != nil || !source.valid(initial) || initial.K3sVersion != clusterbootstrap.K3sPins().Version {
		return fail()
	}
	initial.Cohort.ObservedAt = 0
	raw, err := json.Marshal(initial)
	if err != nil || len(raw) > 256<<10 {
		return fail()
	}
	var frozen clusterSSHPreparationAuthority
	if json.Unmarshal(raw, &frozen) != nil {
		return fail()
	}
	digest, err := clusterSSHPreparationDigest(frozen)
	if err != nil {
		return fail()
	}
	registered := map[string]string{}
	reads := 0
	// Existing reader still proves exact original Nodes, source Pod, namespace
	// and current authority on both sides. Only approved drained targets are
	// removed from its original-members projection, never unknown Nodes.
	proof, err := readClusterSSHPrejoinSourceCustody(ctx, source, authority, func(ctx context.Context, path string, out any) error {
		if path != "/api/v1/nodes" {
			return getJSON(ctx, path, out)
		}
		originals, current, err := projectClusterSSHJoiningNodes(ctx, frozen, getJSON)
		if err != nil {
			return err
		}
		// Registration may grow during startup, never disappear or change UID.
		for id, uid := range registered {
			if current[id] != uid {
				return clusterbootstrap.ErrSessionAuthority
			}
		}
		registered = current
		reads++
		filtered, err := json.Marshal(map[string]any{"items": originals})
		if err != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		return json.Unmarshal(filtered, out)
	})
	if err != nil || proof.AuthoritySHA256 != digest || reads != 2 || ctx.Err() != nil {
		return fail()
	}
	return clusterSSHJoiningSourceCustody{proof, registered}, nil
}

// Shared read-only projection for joining custody and preparation snapshots.
// Every unrecorded Node rejects before original-source observers see the list.
func projectClusterSSHJoiningNodes(ctx context.Context, frozen clusterSSHPreparationAuthority, getJSON func(context.Context, string, any) error) ([]json.RawMessage, map[string]string, error) {
	var list struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Continue string `json:"continue"`
		} `json:"metadata"`
		Items *[]json.RawMessage `json:"items"`
	}
	if getJSON(ctx, "/api/v1/nodes", &list) != nil || list.APIVersion != "v1" || list.Kind != "NodeList" || list.Metadata.Continue != "" || list.Items == nil ||
		len(*list.Items) < len(frozen.Source.Members) || len(*list.Items) > len(frozen.Source.Members)+len(frozen.Cohort.Targets) {
		return nil, nil, clusterbootstrap.ErrSessionAuthority
	}
	originals := []json.RawMessage{}
	current := map[string]string{}
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
			return nil, nil, clusterbootstrap.ErrSessionAuthority
		}
		names[node.Metadata.Name], uids[node.Metadata.UID] = true, true
		original := false
		for _, member := range frozen.Source.Members {
			if node.Metadata.Name == member.Name {
				original = true
				break
			}
		}
		if original {
			if node.Metadata.Labels["borealis.io/engine-node"] != "true" || node.Metadata.Labels["borealis.io/application-state"] != "active" {
				return nil, nil, clusterbootstrap.ErrSessionAuthority
			}
			originals = append(originals, raw)
			continue
		}
		found := false
		for _, target := range frozen.Cohort.Targets {
			if node.Metadata.Name != target.Report.Hostname {
				continue
			}
			identity := clusterbootstrap.K3sJoinedNodeIdentity{Hostname: target.Report.Hostname, MachineID: target.Report.MachineID, BootID: target.Report.BootID, Address: target.Binding.Address, K3sVersion: frozen.K3sVersion}
			uid, err := clusterbootstrap.ParseK3sRegisteringNode(raw, identity)
			if err != nil {
				return nil, nil, clusterbootstrap.ErrSessionAuthority
			}
			current[target.Binding.TargetID], found = uid, true
		}
		if !found {
			return nil, nil, clusterbootstrap.ErrSessionAuthority
		}
	}
	return originals, current, nil
}
