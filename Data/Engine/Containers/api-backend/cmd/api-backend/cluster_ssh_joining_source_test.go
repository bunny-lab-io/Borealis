package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestClusterSSHJoiningSourceCustody(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "absent", "registering", "appears", "disappears", "replaced", "wrong boot", "active", "unknown", "duplicate", "source changed", "pod changed", "namespace changed", "authority changed", "aliased authority", "pagination", "missing items", "wrong version"} {
		t.Run(mode, func(t *testing.T) {
			a, source, pod := sshPrejoinSourceFixture(t, mode == "replacement")
			if mode == "wrong version" {
				a.K3sVersion = "v1.36.4+k3s1"
			}
			calls, lists, pods, namespaces := 0, 0, 0, 0
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				calls++
				if mode == "authority changed" && calls > 1 {
					a.PayloadSHA256 = strings.Repeat("c", 64)
				}
				return a, nil
			}
			uids := make([]string, len(a.Cohort.Targets))
			for i := range uids {
				uids[i] = newClusterUUID()
			}
			read := func(ctx context.Context, path string, out any) error {
				var value any
				switch path {
				case "/api/v1/namespaces/kube-system":
					namespaces++
					uid := a.Source.KubeSystemUID
					if mode == "namespace changed" && namespaces == 4 {
						uid = newClusterUUID()
					}
					value = map[string]any{"metadata": map[string]any{"uid": uid}}
				case "/api/v1/namespaces/borealis/pods/" + source.Process.PodName:
					pods++
					if mode == "pod changed" && pods == 2 {
						pod["metadata"].(map[string]any)["uid"] = newClusterUUID()
					}
					value = pod
				case "/api/v1/nodes":
					lists++
					nodes := []any{}
					for _, member := range a.Source.Members {
						node := sshSourceKubernetesFixture(member)
						node["apiVersion"], node["kind"] = "v1", "Node"
						meta := node["metadata"].(map[string]any)
						meta["labels"] = map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "active"}
						if mode == "source changed" && lists == 2 {
							meta["uid"] = newClusterUUID()
						}
						nodes = append(nodes, node)
					}
					for i, target := range a.Cohort.Targets {
						if mode == "absent" || mode == "appears" && lists == 1 || mode == "disappears" && lists == 2 {
							continue
						}
						node := sshJoinedNodeFixture(target, a.K3sVersion, uids[i])
						meta := node["metadata"].(map[string]any)
						switch mode {
						case "registering":
							delete(node, "status")
						case "replaced":
							if lists == 2 {
								meta["uid"] = newClusterUUID()
							}
						case "wrong boot":
							node["status"].(map[string]any)["nodeInfo"].(map[string]any)["bootID"] = newClusterUUID()
						case "active":
							meta["labels"].(map[string]any)["borealis.io/application-state"] = "active"
						case "unknown":
							meta["name"] = "foreign"
						case "duplicate":
							meta["uid"] = a.Source.Members[0].NodeUID
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
					if mode == "aliased authority" && lists == 1 {
						a.Cohort.Targets[0].Report.BootID = newClusterUUID()
					}
					value = list
				default:
					t.Fatal("unexpected path", path)
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return err
				}
				return json.Unmarshal(raw, out)
			}
			got, err := readClusterSSHJoiningSourceCustody(context.Background(), source, authority, read)
			valid := mode == "pair" || mode == "replacement" || mode == "absent" || mode == "registering" || mode == "appears"
			if (err == nil) != valid {
				t.Fatal("wrong joining custody", err)
			}
			if !valid {
				if got.Registered != nil || got.Original != (clusterSSHPrejoinSourceCustody{}) {
					t.Fatal("partial custody escaped")
				}
				return
			}
			want := len(uids)
			if mode == "absent" {
				want = 0
			}
			if len(got.Registered) != want || !got.Original.valid(source) || lists != 2 || pods != 2 || calls != 3 || namespaces != 4 {
				t.Fatal("missing independent source proof")
			}
			// Original entrypoint remains strict even for this approved cohort.
			lists, pods, namespaces = 0, 0, 0
			if mode == "pair" || mode == "replacement" || mode == "registering" {
				if _, err := readClusterSSHPrejoinSourceCustody(context.Background(), source, authority, read); err == nil {
					t.Fatal("prejoin boundary weakened")
				}
			}
		})
	}
}
