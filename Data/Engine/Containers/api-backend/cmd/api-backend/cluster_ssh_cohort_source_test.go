package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func sshSourceKubernetesFixture(member clusterSSHSourceMember) map[string]any {
	return map[string]any{"metadata": map[string]any{"name": member.Name, "uid": member.NodeUID}, "status": map[string]any{
		"nodeInfo":   map[string]any{"machineID": member.MachineID, "bootID": member.BootID},
		"addresses":  []any{map[string]any{"type": "InternalIP", "address": member.Address}},
		"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
	}}
}

func TestClusterSSHSourceCohortRequiresActualNodeIdentity(t *testing.T) {
	for _, mode := range []string{"success", "missing node", "extra node", "foreign name", "missing UID", "missing machine", "zero machine", "missing boot", "wrong address", "duplicate address", "not ready", "duplicate ready", "deleting node", "missing namespace"} {
		t.Run(mode, func(t *testing.T) {
			_, source := sshInspectionCohortFixture(t)
			node := sshSourceKubernetesFixture(source.Members[0])
			metadata := node["metadata"].(map[string]any)
			status := node["status"].(map[string]any)
			info := status["nodeInfo"].(map[string]any)
			items := []any{node}
			namespace := source.KubeSystemUID
			switch mode {
			case "missing node":
				items = nil
			case "extra node":
				items = append(items, node)
			case "foreign name":
				metadata["name"] = "other-member"
			case "missing UID":
				delete(metadata, "uid")
			case "missing machine":
				delete(info, "machineID")
			case "zero machine":
				info["machineID"] = strings.Repeat("0", 32)
			case "missing boot":
				delete(info, "bootID")
			case "wrong address":
				status["addresses"] = []any{map[string]any{"type": "InternalIP", "address": "192.168.90.29"}}
			case "duplicate address":
				addresses := status["addresses"].([]any)
				status["addresses"] = append(addresses, addresses[0])
			case "not ready":
				status["conditions"] = []any{map[string]any{"type": "Ready", "status": "Unknown"}}
			case "duplicate ready":
				conditions := status["conditions"].([]any)
				status["conditions"] = append(conditions, conditions[0])
			case "deleting node":
				metadata["deletionTimestamp"] = "2026-09-07T13:00:00Z"
			case "missing namespace":
				namespace = ""
			}
			read := func(ctx context.Context, path string, out any) error {
				var value any
				switch path {
				case "/api/v1/namespaces/kube-system":
					value = map[string]any{"metadata": map[string]any{"uid": namespace}}
				case "/api/v1/nodes":
					value = map[string]any{"items": items}
				default:
					t.Fatal("unexpected Kubernetes read")
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return err
				}
				return json.Unmarshal(raw, out)
			}
			original := source.Members[0]
			got, err := observeClusterSSHSourceCohort(context.Background(), read, source)
			if mode == "success" {
				if err != nil || got.Members[0] != original || got.KubeSystemUID != source.KubeSystemUID {
					t.Fatal("actual identity lost")
				}
			} else if err != errClusterSSHCohort {
				t.Fatalf("ambiguous Kubernetes identity accepted: %v", err)
			}
			if source.Members[0] != original {
				t.Fatal("observation mutated input source")
			}
		})
	}
}
