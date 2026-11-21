package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"fmt"
	"maps"
	"strings"
	"testing"
)

func TestClusterSSHPreparedAdmissionStoreRedaction(t *testing.T) {
	binding := clusterSSHPreparedStoreBinding{credentials: [][2]string{{"private-generation", "private-ciphertext"}}}
	for _, value := range []any{binding, &binding} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if output := fmt.Sprintf(format, value); strings.Contains(output, "private-") || !strings.Contains(output, "redacted") {
				t.Fatal("private store binding reached formatted output")
			}
		}
	}
}

func preparedAdmissionStoreObservation(a clusterSSHPreparedAuthority, op clusterControllerOperation, holder string, now int64) clusterSSHPreparedObservation {
	nodes := map[string]clusterbootstrap.K3sJoinedNode{}
	for _, target := range a.receipt.Targets {
		nodes[target.TargetID] = target.Prejoin.Receipt.Node
	}
	return clusterSSHPreparedObservation{op.ID, holder, preparationReceiptHash(a.payload), a.queue.ClusterID,
		a.proof.Source.KubeSystemUID, op.Attempt, now, nodes}
}

func TestClusterSSHPreparedAdmissionStoreObservationBinding(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "kind", "state", "step", "operation", "holder", "attempt", "payload", "cluster", "namespace", "clock", "missing", "extra", "uid", "machine", "address"} {
		t.Run(mode, func(t *testing.T) {
			a, authority, _ := sshMembershipAuthorityFixture(t, mode == "replacement")
			authority.payload, authority.queue.ClusterID = "owned exact payload", a.Source.ClusterID
			op := clusterControllerOperation{ID: a.Lease.OperationID, Kind: "ssh_onboarding", State: "running", CurrentStep: clusterSSHPreparationOperationStep, Attempt: a.Lease.OperationAttempt}
			observed := preparedAdmissionStoreObservation(authority, op, a.Lease.ControllerHolder, 100)
			original := maps.Clone(observed.Nodes)
			switch mode {
			case "kind":
				op.Kind = "membership_admit"
			case "state":
				op.State = "waiting"
			case "step":
				op.CurrentStep = "apply_membership"
			case "operation":
				observed.OperationID = newClusterUUID()
			case "holder":
				observed.Controller = "replacement"
			case "attempt":
				observed.Attempt++
			case "payload":
				observed.PayloadSHA256 = preparationReceiptHash("different")
			case "cluster":
				observed.ClusterID = newClusterUUID()
			case "namespace":
				observed.KubeSystemUID = newClusterUUID()
			case "clock":
				observed.ObservedAt = 0
			case "missing":
				delete(observed.Nodes, authority.receipt.Targets[0].TargetID)
			case "extra":
				observed.Nodes[newClusterUUID()] = authority.receipt.Targets[0].Prejoin.Receipt.Node
			case "uid", "machine", "address":
				id := authority.receipt.Targets[0].TargetID
				node := observed.Nodes[id]
				switch mode {
				case "uid":
					node.NodeUID = newClusterUUID()
				case "machine":
					node.MachineID = "00000000000000000000000000000001"
				case "address":
					node.Address = "192.168.10.253"
				}
				observed.Nodes[id] = node
			}
			if got := clusterSSHPreparedAdmissionObservationMatches(op, a.Lease.ControllerHolder, authority, observed); got != (mode == "pair" || mode == "replacement") {
				t.Fatal("wrong exact admission observation binding", got)
			}
			for _, target := range authority.receipt.Targets {
				if original[target.TargetID] != target.Prejoin.Receipt.Node {
					t.Fatal("observation changed original receipt")
				}
			}
		})
	}
}

func TestClusterSSHPreparedAdmissionStorePhases(t *testing.T) {
	for _, phase := range []string{clusterSSHPreparationOperationStep, "apply_membership", "verify_quorum", "", "qualify_ssh_targets", "admit_pending_members", "enable_roles", "complete", "recover_identity"} {
		want := phase == clusterSSHPreparationOperationStep || phase == "apply_membership" || phase == "verify_quorum"
		if clusterSSHPreparedAuthorityPhase(phase) != want {
			t.Fatal("unsupported prepared phase", phase)
		}
	}
}
