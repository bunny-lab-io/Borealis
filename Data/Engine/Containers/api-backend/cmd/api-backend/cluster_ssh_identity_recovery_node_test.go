package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func sshIdentityRecoveryKubernetesFixture(t *testing.T, claim clusterSSHIdentityRecoveryClaim) (map[string]any, []any) {
	t.Helper()
	var items []any
	for _, member := range claim.Topology.Members {
		node := sshSourceKubernetesFixture(member)
		node["metadata"].(map[string]any)["labels"] = map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "active"}
		items = append(items, node)
	}
	target := map[string]any{"metadata": map[string]any{"name": claim.Binding.Hostname, "uid": claim.Recovery.TargetNodeUID,
		"labels": map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "drained"}}, "status": map[string]any{
		"nodeInfo":   map[string]any{"machineID": claim.Binding.MachineID, "bootID": claim.Inspected.Report.BootID},
		"addresses":  []any{map[string]any{"type": "InternalIP", "address": claim.Binding.Address}},
		"conditions": []any{map[string]any{"type": "Ready", "status": "False"}},
	}}
	items = append(items, target)
	return target, items
}

func TestClusterSSHIdentityRecoveryNodeReadRequiresExactFreshNodes(t *testing.T) {
	for _, mode := range []string{"success", "source UID", "source state", "source ready", "source machine", "source address", "target missing", "target UID", "target state", "target machine", "target boot", "target address", "target deleting", "duplicate machine", "duplicate ready", "paginated", "namespace drift", "private read failure", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			claim, _, consumer := sshIdentityRecoveryWorkerFixture(t)
			consumer.Close()
			target, items := sshIdentityRecoveryKubernetesFixture(t, claim)
			source := items[0].(map[string]any)
			namespaceReads := 0
			switch mode {
			case "source UID":
				source["metadata"].(map[string]any)["uid"] = newClusterUUID()
			case "source state":
				source["metadata"].(map[string]any)["labels"].(map[string]any)["borealis.io/application-state"] = "drained"
			case "source ready":
				source["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
			case "source machine":
				source["status"].(map[string]any)["nodeInfo"].(map[string]any)["machineID"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			case "source address":
				source["status"].(map[string]any)["addresses"] = []any{map[string]any{"type": "InternalIP", "address": "192.168.90.98"}}
			case "target missing":
				items = items[:len(items)-1]
			case "target UID":
				target["metadata"].(map[string]any)["uid"] = newClusterUUID()
			case "target state":
				target["metadata"].(map[string]any)["labels"].(map[string]any)["borealis.io/application-state"] = "active"
			case "target machine":
				target["status"].(map[string]any)["nodeInfo"].(map[string]any)["machineID"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			case "target boot":
				target["status"].(map[string]any)["nodeInfo"].(map[string]any)["bootID"] = newClusterUUID()
			case "target address":
				target["status"].(map[string]any)["addresses"] = []any{map[string]any{"type": "InternalIP", "address": "192.168.90.99"}}
			case "target deleting":
				target["metadata"].(map[string]any)["deletionTimestamp"] = "2026-10-23T00:00:00Z"
			case "duplicate machine":
				target["status"].(map[string]any)["nodeInfo"].(map[string]any)["machineID"] = source["status"].(map[string]any)["nodeInfo"].(map[string]any)["machineID"]
			case "duplicate ready":
				conditions := target["status"].(map[string]any)["conditions"].([]any)
				target["status"].(map[string]any)["conditions"] = append(conditions, conditions[0])
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			read := func(_ context.Context, path string, out any) error {
				if mode == "private read failure" {
					return errors.New("private Kubernetes failure")
				}
				var value any
				switch path {
				case "/api/v1/namespaces/kube-system":
					namespaceReads++
					uid := claim.Topology.KubeSystemUID
					if mode == "namespace drift" && namespaceReads > 1 {
						uid = newClusterUUID()
					}
					value = map[string]any{"metadata": map[string]any{"uid": uid}}
				case "/api/v1/nodes":
					continuation := ""
					if mode == "paginated" {
						continuation = "private-token"
					}
					value = map[string]any{"metadata": map[string]any{"continue": continuation}, "items": items}
				default:
					t.Fatal("unexpected Kubernetes read")
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return err
				}
				return json.Unmarshal(raw, out)
			}
			err := newClusterSSHIdentityRecoveryNodeRead(read)(ctx, claim)
			if mode == "success" {
				if err != nil || namespaceReads != 2 {
					t.Fatalf("exact Node proof failed: %v reads=%d", err, namespaceReads)
				}
			} else if !errors.Is(err, clusterbootstrap.ErrPreparationConfig) || (err != nil && err.Error() == "private Kubernetes failure") {
				t.Fatalf("unsafe Node proof result: %v", err)
			}
		})
	}
}
