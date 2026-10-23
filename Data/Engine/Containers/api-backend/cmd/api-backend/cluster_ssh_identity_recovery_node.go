package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
)

type clusterSSHIdentityRecoveryKubernetesNode struct {
	Metadata struct {
		Name              string            `json:"name"`
		UID               string            `json:"uid"`
		DeletionTimestamp *string           `json:"deletionTimestamp"`
		Labels            map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		NodeInfo struct {
			MachineID string `json:"machineID"`
			BootID    string `json:"bootID"`
		} `json:"nodeInfo"`
		Addresses []struct {
			Type    string `json:"type"`
			Address string `json:"address"`
		} `json:"addresses"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

// Sender-side proof is independent from receiver-side
// InspectIdentityConsumers. It binds the original active source Nodes and the
// exact drained target Node to one fresh Kubernetes list snapshot, bracketed by
// the original kube-system UID. It observes only; it never drains or restarts.
func newClusterSSHIdentityRecoveryNodeRead(getJSON func(context.Context, string, any) error) func(context.Context, clusterSSHIdentityRecoveryClaim) error {
	return func(ctx context.Context, claim clusterSSHIdentityRecoveryClaim) error {
		fail := func() error { return clusterbootstrap.ErrPreparationConfig }
		if getJSON == nil || !claim.valid() || ctx.Err() != nil {
			return fail()
		}
		readNamespace := func() (string, error) {
			var namespace struct {
				Metadata struct {
					UID string `json:"uid"`
				} `json:"metadata"`
			}
			if getJSON(ctx, "/api/v1/namespaces/kube-system", &namespace) != nil || ctx.Err() != nil || namespace.Metadata.UID != claim.Topology.KubeSystemUID {
				return "", fail()
			}
			return namespace.Metadata.UID, nil
		}
		before, err := readNamespace()
		if err != nil {
			return fail()
		}
		var nodes struct {
			Metadata struct {
				Continue string `json:"continue"`
			} `json:"metadata"`
			Items *[]clusterSSHIdentityRecoveryKubernetesNode `json:"items"`
		}
		if getJSON(ctx, "/api/v1/nodes", &nodes) != nil || ctx.Err() != nil || nodes.Items == nil || nodes.Metadata.Continue != "" {
			return fail()
		}
		expected := make(map[string]clusterSSHSourceMember, len(claim.Topology.Members))
		for _, member := range claim.Topology.Members {
			expected[member.Name] = member
		}
		seen, identities, machines, addresses := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
		targetSeen := false
		for _, node := range *nodes.Items {
			name, uid, machine, boot := node.Metadata.Name, node.Metadata.UID, node.Status.NodeInfo.MachineID, node.Status.NodeInfo.BootID
			if name == "" || uid == "" || machine == "" || boot == "" || node.Metadata.DeletionTimestamp != nil || seen[name] || identities[uid] || machines[machine] {
				return fail()
			}
			seen[name], identities[uid], machines[machine] = true, true, true
			internal, ready := "", ""
			for _, address := range node.Status.Addresses {
				if address.Type == "InternalIP" {
					if internal != "" || addresses[address.Address] {
						return fail()
					}
					internal, addresses[address.Address] = address.Address, true
				}
			}
			for _, condition := range node.Status.Conditions {
				if condition.Type == "Ready" {
					if ready != "" {
						return fail()
					}
					ready = condition.Status
				}
			}
			if member, ok := expected[name]; ok {
				if uid != member.NodeUID || machine != member.MachineID || boot != member.BootID || internal != member.Address || ready != "True" ||
					node.Metadata.Labels["borealis.io/engine-node"] != "true" || node.Metadata.Labels["borealis.io/application-state"] != "active" {
					return fail()
				}
				delete(expected, name)
			}
			if name == claim.Binding.Hostname {
				if targetSeen || uid != claim.Recovery.TargetNodeUID || machine != claim.Binding.MachineID || boot != claim.Inspected.Report.BootID ||
					internal != claim.Binding.Address || ready == "" || node.Metadata.Labels["borealis.io/engine-node"] != "true" ||
					node.Metadata.Labels["borealis.io/application-state"] != "drained" {
					return fail()
				}
				targetSeen = true
			}
		}
		after, err := readNamespace()
		if err != nil || before != after || len(expected) != 0 || !targetSeen || ctx.Err() != nil {
			return fail()
		}
		return nil
	}
}
