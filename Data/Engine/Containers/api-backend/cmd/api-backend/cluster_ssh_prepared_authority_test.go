package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sshPreparedNativeFixture(a clusterSSHPreparationAuthority, uids map[string]string, path string) map[string]any {
	var object map[string]any
	_ = sshPrejoinCompletionNodes(a, uids)(context.Background(), path, &object)
	if path == "/api/v1/namespaces/kube-system" {
		object["apiVersion"], object["kind"] = "v1", "Namespace"
		metadata := clusterSSHStorageMap(object, "metadata")
		metadata["name"], metadata["resourceVersion"] = "kube-system", "1"
	} else {
		for _, item := range anySlice(object["items"]) {
			clusterSSHStorageMap(item.(map[string]any), "metadata")["resourceVersion"] = "1"
		}
	}
	return object
}

func TestClusterSSHPreparedNativeObservation(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "namespace deleting", "namespace alias", "namespace scope", "namespace kind", "namespace name", "namespace version", "namespace drift", "items alias", "pagination alias", "node namespace", "node deletion alias", "node status alias", "node info alias", "machine alias", "addresses alias", "address alias", "condition alias", "malformed condition", "duplicate uid", "duplicate status", "missing items", "trailing", "oversize", "redirect", "non200", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			a, _, _ := sshPrejoinSourceFixture(t, mode == "replacement")
			uids := map[string]string{}
			for _, target := range a.Cohort.Targets {
				uids[target.Binding.TargetID] = newClusterUUID()
			}
			ctx, cancel := context.WithTimeout(context.Background(), clusterSSHJoinedNodeObservationTimeout)
			defer cancel()
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.RawQuery != "" {
					t.Error("unexpected mutating or cached request")
				}
				object := sshPreparedNativeFixture(a, uids, r.URL.Path)
				if mode == "redirect" {
					http.Redirect(w, r, "/redirected", 307)
					return
				}
				if mode == "non200" {
					w.WriteHeader(202)
				}
				if mode == "cancelled" {
					cancel()
				}
				if r.URL.Path == "/api/v1/namespaces/kube-system" {
					metadata := clusterSSHStorageMap(object, "metadata")
					switch mode {
					case "namespace deleting":
						metadata["deletionTimestamp"] = "2026-10-28T00:00:00Z"
					case "namespace alias":
						metadata["DeletionTimestamp"] = "2026-10-28T00:00:00Z"
					case "namespace scope":
						metadata["namespace"] = "other"
					case "namespace kind":
						object["kind"] = "Node"
					case "namespace name":
						metadata["name"] = "other"
					case "namespace version":
						object["apiVersion"] = "v2"
					case "namespace drift":
						if calls > 1 {
							metadata["uid"] = newClusterUUID()
						}
					}
				} else {
					item := anySlice(object["items"])[0].(map[string]any)
					metadata, status := clusterSSHStorageMap(item, "metadata"), clusterSSHStorageMap(item, "status")
					rename := func(object map[string]any, old, new string) { object[new] = object[old]; delete(object, old) }
					switch mode {
					case "items alias":
						rename(object, "items", "Items")
					case "pagination alias":
						clusterSSHStorageMap(object, "metadata")["Continue"] = "next"
					case "missing items":
						delete(object, "items")
					case "node namespace":
						metadata["namespace"] = "other"
					case "node deletion alias":
						metadata["DeletionTimestamp"] = "2026-10-28T00:00:00Z"
					case "node status alias":
						rename(item, "status", "Status")
					case "node info alias":
						rename(status, "nodeInfo", "NodeInfo")
					case "machine alias":
						rename(clusterSSHStorageMap(status, "nodeInfo"), "machineID", "MachineID")
					case "addresses alias":
						rename(status, "addresses", "Addresses")
					case "address alias":
						rename(anySlice(status["addresses"])[0].(map[string]any), "type", "Type")
					case "malformed condition":
						status["conditions"] = append(anySlice(status["conditions"]), nil)
					case "condition alias":
						rename(anySlice(status["conditions"])[0].(map[string]any), "type", "Type")
					}
				}
				raw, _ := json.Marshal(object)
				wire := string(raw)
				if mode == "duplicate uid" {
					wire = strings.Replace(wire, `"uid":`, `"uid":"`+newClusterUUID()+`","uid":`, 1)
				}
				if mode == "duplicate status" && r.URL.Path == "/api/v1/nodes" {
					wire = strings.Replace(wire, `"status":"True"`, `"status":"False","status":"True"`, 1)
				}
				if mode == "trailing" {
					wire += "{}"
				}
				if mode == "oversize" {
					wire += strings.Repeat(" ", 2<<20)
				}
				_, _ = w.Write([]byte(wire))
			}))
			defer server.Close()
			kube := &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}
			nodes, err := observeClusterSSHJoinedNodes(ctx, kube.getClusterSSHNodeCohortJSON, a.Cohort, a.Source, a.K3sVersion)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success {
				t.Fatalf("wrong strict observation outcome: %v", err)
			}
			if success && len(nodes) != len(a.Cohort.Targets) {
				t.Fatal("incomplete cohort")
			}
			if !success && nodes != nil {
				t.Fatal("partial observation escaped")
			}
			if mode == "redirect" && calls != 1 {
				t.Fatal("redirect followed")
			}
		})
	}
}
