package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"reflect"
	"slices"
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
		if getJSON == nil || !claim.valid() || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return readClusterSSHIdentityRecoveryNodes(ctx, getJSON, claim.Topology, claim.Inspected, claim.Binding, claim.Recovery)
	}
}

// Controller-side adapter reconstructs every expected Node field from one
// exact SQL authority read. API workers receive only success/failure over the
// authenticated broker and never gain Kubernetes credentials.
func newClusterSSHIdentityRecoveryAuthorityNodeRead(getJSON func(context.Context, string, any) error) func(context.Context, clusterSSHPreparationAuthority) error {
	return func(ctx context.Context, authority clusterSSHPreparationAuthority) error {
		if getJSON == nil || !validClusterSSHIdentityRecoveryLease(authority.Lease) || authority.Recovery == nil ||
			validateClusterSSHInspectionCohort(authority.Cohort, authority.Source) != nil || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		binding := *authority.Recovery
		if binding.Source.ClusterID != authority.Source.ClusterID || binding.Recovery.SourceID != binding.Source.SourceID ||
			binding.Recovery.SourceNodeUID != binding.Source.SourceUID ||
			!slices.ContainsFunc(authority.Source.Members, func(member clusterSSHSourceMember) bool {
				return member.NodeID == binding.Source.SourceID && member.NodeUID == binding.Source.SourceUID
			}) || !slices.ContainsFunc(authority.Cohort.Targets, func(target clusterSSHInspectedTarget) bool { return reflect.DeepEqual(target, binding.Target) }) {
			return clusterbootstrap.ErrPreparationConfig
		}
		target := binding.Target
		session := clusterbootstrap.SessionBinding{ClusterID: target.Binding.ClusterID, OperationID: authority.Lease.OperationID,
			TargetID: authority.Lease.TargetID, HolderID: authority.Lease.Holder, Generation: authority.Lease.Generation,
			OperationAttempt: authority.Lease.OperationAttempt, Address: target.Binding.Address, Port: target.Binding.Port,
			Hostname: target.Report.Hostname, MachineID: target.Report.MachineID, HostKeyAlgorithm: target.Key.Algorithm,
			HostKeyFingerprint: target.Key.Fingerprint}
		return readClusterSSHIdentityRecoveryNodes(ctx, getJSON, authority.Source, target, session, binding.Recovery)
	}
}

func readClusterSSHIdentityRecoveryNodes(ctx context.Context, getJSON func(context.Context, string, any) error,
	topology clusterSSHSourceCohort, target clusterSSHInspectedTarget, session clusterbootstrap.SessionBinding,
	recovery clusterbootstrap.IdentityRecoveryRequest) error {
	fail := func() error { return clusterbootstrap.ErrPreparationConfig }
	if getJSON == nil || session.ClusterID != topology.ClusterID || session.OperationID != target.Binding.OperationID ||
		session.TargetID != target.Binding.TargetID || session.OperationAttempt != target.Attempt || session.Address != target.Binding.Address ||
		session.Port != target.Binding.Port || session.Hostname != target.Report.Hostname || session.MachineID != target.Report.MachineID ||
		session.HostKeyAlgorithm != target.Key.Algorithm || session.HostKeyFingerprint != target.Key.Fingerprint ||
		recovery.Validate(session) != nil || ctx.Err() != nil {
		return fail()
	}
	readNamespace := func() (string, error) {
		var namespace struct {
			Metadata struct {
				UID string `json:"uid"`
			} `json:"metadata"`
		}
		if getJSON(ctx, "/api/v1/namespaces/kube-system", &namespace) != nil || ctx.Err() != nil || namespace.Metadata.UID != topology.KubeSystemUID {
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
	expected := make(map[string]clusterSSHSourceMember, len(topology.Members))
	for _, member := range topology.Members {
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
		if name == target.Report.Hostname {
			if targetSeen || uid != recovery.TargetNodeUID || machine != target.Report.MachineID || boot != target.Report.BootID ||
				internal != target.Binding.Address || ready == "" || node.Metadata.Labels["borealis.io/engine-node"] != "true" ||
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
