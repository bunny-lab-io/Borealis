package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func sshJoinedNodeFixture(target clusterSSHInspectedTarget, version, uid string) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{"name": target.Report.Hostname, "uid": uid, "labels": map[string]any{
		"borealis.io/engine-node": "true", "borealis.io/application-state": "drained", "borealis.io/control-plane-eligible": "false", "borealis.io/edge-eligible": "false", "borealis.io/scheduler-eligible": "false", "borealis.io/postgres-primary-eligible": "false"}},
		"status": map[string]any{"nodeInfo": map[string]any{"machineID": target.Report.MachineID, "bootID": target.Report.BootID, "kubeletVersion": version, "architecture": "amd64", "operatingSystem": "linux"},
			"addresses": []any{map[string]any{"type": "InternalIP", "address": target.Binding.Address}}, "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}}
}

func TestClusterSSHJoinedTargetObservation(t *testing.T) {
	modes := []string{"pair", "zero namespace", "zero source boot", "zero source UID", "zero source ID", "replacement", "absent targets", "one target", "pending", "readiness transition", "clock", "qualification", "missing source selection", "source selection drift", "generation", "namespace drift", "source UID drift", "source not ready", "source drained", "target UID drift", "target boot drift", "target active", "target eligible", "unknown node", "duplicate name", "duplicate UID", "source missing", "pagination", "missing items", "wrong kind", "authority drift", "aliased authority", "private error", "cancelled"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			a, _, _ := sshPrejoinSourceFixture(t, mode == "replacement")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "qualification" {
				a.Lease.OperationStep = clusterSSHQualificationStep
				a.Lease.Step = "qualify"
			}
			if mode == "missing source selection" {
				a.IdentitySource = nil
			}
			if mode == "source selection drift" {
				a.IdentitySource.SourceUID = newClusterUUID()
			}
			if mode == "generation" {
				a.Lease.Generation = a.Cohort.Targets[0].Generation
			}
			zero := "00000000-0000-0000-0000-000000000000"
			switch mode {
			case "zero namespace":
				a.Source.KubeSystemUID = zero
			case "zero source boot":
				a.Source.Members[0].BootID = zero
			case "zero source UID":
				a.Source.Members[0].NodeUID, a.IdentitySource.SourceUID = zero, zero
			case "zero source ID":
				a.Source.Members[0].NodeID, a.IdentitySource.SourceID = zero, zero
			}
			authCalls, lists, namespaces := 0, 0, 0
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				authCalls++
				if mode == "clock" {
					a.Cohort.ObservedAt++
				}
				if mode == "authority drift" && authCalls == 2 {
					a.PayloadSHA256 = strings.Repeat("d", 64)
				}
				return a, nil
			}
			targetUIDs := make([]string, len(a.Cohort.Targets))
			for i := range targetUIDs {
				targetUIDs[i] = newClusterUUID()
			}
			read := func(ctx context.Context, path string, out any) error {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 15*time.Second {
					t.Fatal("unbounded Kubernetes read")
				}
				if mode == "private error" {
					return errors.New("private diagnostic")
				}
				if mode == "cancelled" {
					cancel()
					return ctx.Err()
				}
				var value any
				switch path {
				case "/api/v1/namespaces/kube-system":
					namespaces++
					uid := a.Source.KubeSystemUID
					if mode == "namespace drift" && namespaces == 4 {
						uid = newClusterUUID()
					}
					value = map[string]any{"metadata": map[string]any{"uid": uid}}
				case "/api/v1/nodes":
					lists++
					var nodes []any
					for _, member := range a.Source.Members {
						node := sshSourceKubernetesFixture(member)
						node["apiVersion"], node["kind"] = "v1", "Node"
						meta := node["metadata"].(map[string]any)
						labels := map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "active"}
						meta["labels"] = labels
						if mode == "source UID drift" && lists == 2 {
							meta["uid"] = newClusterUUID()
						}
						if mode == "source drained" {
							labels["borealis.io/application-state"] = "drained"
						}
						if mode == "source not ready" {
							node["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
						}
						if mode != "source missing" {
							nodes = append(nodes, node)
						}
					}
					for i, target := range a.Cohort.Targets {
						if mode == "absent targets" || mode == "one target" && i > 0 {
							continue
						}
						node := sshJoinedNodeFixture(target, a.K3sVersion, targetUIDs[i])
						meta := node["metadata"].(map[string]any)
						status := node["status"].(map[string]any)
						if i == 0 {
							switch mode {
							case "pending":
								status["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
							case "readiness transition":
								if lists == 1 {
									status["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
								}
							case "target UID drift":
								if lists == 2 {
									meta["uid"] = newClusterUUID()
								}
							case "target boot drift":
								status["nodeInfo"].(map[string]any)["bootID"] = newClusterUUID()
							case "target active":
								meta["labels"].(map[string]any)["borealis.io/application-state"] = "active"
							case "target eligible":
								meta["labels"].(map[string]any)["borealis.io/edge-eligible"] = "true"
							case "unknown node":
								meta["name"] = "foreign"
							case "duplicate name":
								meta["name"] = a.Source.Members[0].Name
							case "duplicate UID":
								meta["uid"] = a.Source.Members[0].NodeUID
							}
						}
						nodes = append(nodes, node)
					}
					list := map[string]any{"apiVersion": "v1", "kind": "NodeList", "items": nodes}
					if mode == "pagination" {
						list["metadata"] = map[string]any{"continue": "next"}
					}
					if mode == "missing items" {
						delete(list, "items")
					}
					if mode == "wrong kind" {
						list["kind"] = "PodList"
					}
					value = list
					if mode == "aliased authority" && lists == 1 {
						a.Cohort.Targets[0].Report.BootID = newClusterUUID()
					}
				default:
					t.Fatal("unexpected path", path)
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return err
				}
				return json.Unmarshal(raw, out)
			}
			got, err := readClusterSSHJoinedTargets(ctx, authority, read)
			valid := mode == "pair" || mode == "replacement" || mode == "absent targets" || mode == "one target" || mode == "pending" || mode == "readiness transition" || mode == "clock"
			if (err == nil) != valid {
				t.Fatal("wrong observation result", err)
			}
			if !valid {
				if err != clusterbootstrap.ErrSessionAuthority || got.Nodes != nil || got.AuthoritySHA256 != "" {
					t.Fatal("partial/private evidence escaped")
				}
				return
			}
			want := len(a.Cohort.Targets)
			if mode == "absent targets" {
				want = 0
			}
			if mode == "one target" {
				want = 1
			}
			if len(got.Nodes) != want || authCalls != 2 || lists != 2 || namespaces != 4 || !validClusterSSHPreparationDigest(got.AuthoritySHA256) {
				t.Fatal("missing independent proof", len(got.Nodes), authCalls, lists, namespaces)
			}
			for i, target := range a.Cohort.Targets {
				node, ok := got.Nodes[target.Binding.TargetID]
				if !ok {
					continue
				}
				if node.NodeUID != targetUIDs[i] || node.Ready == (mode == "pending" && i == 0) {
					t.Fatal("readiness/identity misrepresented")
				}
			}
		})
	}
}
