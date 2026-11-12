package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHPreparedAdmissionJobCustody(t *testing.T) {
	for _, mode := range []string{"success", "cancel contains"} {
		t.Run(mode, func(t *testing.T) {
			_, authority, _ := sshMembershipAuthorityFixture(t, false)
			prepared := authority.receipt.Targets[0]
			request := clusterbootstrap.PreparedAdmissionRequest{
				Version: 1, Action: "InspectHealth", StartBefore: time.Now().Add(time.Minute).Unix(),
				Expected: prepared.Prejoin.Expected, Receipt: prepared.Prejoin.Receipt,
			}
			operation := clusterControllerOperation{ID: newClusterUUID(), Kind: "ssh_onboarding"}
			step := clusterControllerStep{Name: "admit:" + prepared.TargetID}
			node := clusterControllerNode{ID: prepared.TargetID, Name: prepared.Prejoin.Receipt.Node.Hostname}
			manifest := clusterActionJobManifest(clusterActionJobName(operation.ID, step.Name), "borealis", node.Name,
				"registry.example/node-manager@sha256:"+strings.Repeat("a", 64),
				[]string{"client", "--verb", clusterbootstrap.PreparedAdmissionVerb, "--prepared-admission", "fixture"}, operation.ID, step.Name)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var posts, deletes, reconciles atomic.Int64
			originalUID, originalPodUID := newClusterUUID(), newClusterUUID()
			reconcileUID, reconcilePodUID := newClusterUUID(), newClusterUUID()
			var original, reconcile map[string]any
			deleted := false
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method {
				case http.MethodPost:
					posts.Add(1)
					var job map[string]any
					if json.NewDecoder(r.Body).Decode(&job) != nil {
						t.Error("invalid Job manifest")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					metadata := clusterSSHStorageMap(job, "metadata")
					if strings.HasPrefix(cleanText(metadata["name"]), "borealis-reconcile-") {
						reconciles.Add(1)
						metadata["uid"], reconcile = reconcileUID, job
						job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					} else {
						metadata["uid"], original = originalUID, job
						if !validClusterSSHSourceJob(job, manifest, originalUID) {
							t.Error("fixture Job differs from submitted manifest", validateClusterActionJobIdentity(job, manifest), nestedMap(job, "spec"), nestedMap(manifest, "spec"))
						}
						if mode == "success" {
							job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
						} else {
							job["status"] = map[string]any{"active": 1}
						}
					}
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(job)
					return
				case http.MethodDelete:
					var options map[string]any
					_ = json.NewDecoder(r.Body).Decode(&options)
					if clusterSSHStorageMap(options, "preconditions")["uid"] != originalUID {
						t.Error("Job deletion lost exact UID precondition")
						w.WriteHeader(http.StatusConflict)
						return
					}
					deletes.Add(1)
					deleted = true
					_ = json.NewEncoder(w).Encode(map[string]any{})
					return
				case http.MethodGet:
					if strings.HasPrefix(r.URL.Path, "/apis/batch/v1/") {
						name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
						if original != nil && name == cleanText(nestedMap(original, "metadata")["name"]) {
							if deleted {
								w.WriteHeader(http.StatusNotFound)
								_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "code": 404})
								return
							}
							_ = json.NewEncoder(w).Encode(original)
							return
						}
						if reconcile != nil && name == cleanText(nestedMap(reconcile, "metadata")["name"]) {
							_ = json.NewEncoder(w).Encode(reconcile)
							return
						}
					}
					if r.URL.Path == "/api/v1/namespaces/borealis/pods" {
						selector := r.URL.Query().Get("labelSelector")
						switch selector {
						case "batch.kubernetes.io/controller-uid=" + originalUID:
							if deleted {
								_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
								return
							}
							if mode == "cancel contains" {
								cancel()
								w.WriteHeader(http.StatusServiceUnavailable)
								return
							}
							_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{sshJoinSourcePod(t, original, originalPodUID, "")}})
							return
						case "batch.kubernetes.io/controller-uid=" + reconcileUID:
							_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{sshJoinSourcePod(t, reconcile, reconcilePodUID, "")}})
							return
						}
					}
				}
				t.Error("unexpected Kubernetes request", r.Method, r.URL.String())
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()

			client := server.Client()
			kube := &kubernetesAPIClient{baseURL: server.URL, token: "fixture", httpClient: client}
			runner := &kubernetesClusterStepRunner{namespace: "borealis", kube: kube, actionImage: "registry.example/node-manager@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
			bound := &clusterSSHPreparedAdmissionRuntime{authority: authority, cleanupKube: kube}
			ctx = context.WithValue(ctx, clusterSSHPreparedAdmissionRuntimeKey{}, bound)
			err := runner.runClusterSSHPreparedAdmissionJob(ctx, operation, step, node, manifest, request)
			if mode == "success" {
				if err != nil || posts.Load() != 1 || deletes.Load() != 0 || reconciles.Load() != 0 {
					t.Fatal("successful Job custody failed", err, posts.Load(), deletes.Load(), reconciles.Load())
				}
			} else if err == nil || posts.Load() != 2 || deletes.Load() != 1 || reconciles.Load() != 1 {
				t.Fatal("cancelled Job not contained and reconciled", err, posts.Load(), deletes.Load(), reconciles.Load())
			}
		})
	}
}

func TestClusterSSHPreparedAdmissionPairContainmentAfterCancellation(t *testing.T) {
	for _, mode := range []string{"exact pair", "replaced node", "node fence rejected"} {
		t.Run(mode, func(t *testing.T) {
			a, authority, uids := sshMembershipAuthorityFixture(t, false)
			native := sshMembershipNativeFixture(a, uids, "/api/v1/nodes")
			nodesByName := map[string]map[string]any{}
			for _, item := range anySlice(native["items"]) {
				node := item.(map[string]any)
				metadata := clusterSSHStorageMap(node, "metadata")
				metadata["resourceVersion"] = "fixture-version"
				if mode == "replaced node" && cleanText(metadata["name"]) == "engine-02" {
					metadata["uid"] = newClusterUUID()
				}
				nodesByName[cleanText(metadata["name"])] = node
			}
			jobs := map[string]map[string]any{}
			deploymentUIDs := map[string]string{}
			var nodePatches, workloadPatches, actions atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/nodes/"):
					_ = json.NewEncoder(w).Encode(nodesByName[strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")])
					return
				case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/v1/nodes/"):
					if mode == "replaced node" && r.URL.Path == "/api/v1/nodes/engine-02" {
						t.Error("old operation patched replacement Node")
					}
					var patch map[string]any
					_ = json.NewDecoder(r.Body).Decode(&patch)
					metadata := clusterSSHStorageMap(patch, "metadata")
					labels := clusterStringMap(metadata["labels"])
					name := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")
					if metadata["uid"] != uids[authority.receipt.Targets[nodeOrdinal(authority, name)].TargetID] || metadata["resourceVersion"] != "fixture-version" ||
						labels["borealis.io/application-state"] != "drained" || labels["borealis.io/control-plane-eligible"] != "false" ||
						labels["borealis.io/edge-eligible"] != "false" || labels["borealis.io/scheduler-eligible"] != "false" || labels["borealis.io/postgres-primary-eligible"] != "false" {
						t.Error("pair containment lost exact Node fence", patch)
					}
					nodePatches.Add(1)
					if mode == "node fence rejected" && name == "engine-02" {
						w.WriteHeader(http.StatusConflict)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{})
					return
				case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/apis/apps/v1/namespaces/borealis/deployments"):
					selector := r.URL.Query().Get("labelSelector")
					nodeName := "engine-02"
					if strings.Contains(selector, "engine-03") {
						nodeName = "engine-03"
					}
					if mode != "exact pair" && nodeName == "engine-02" {
						t.Error("old operation discovered workload without receipt-bound Node fence")
					}
					name, uid := "wireguard-tunnel-"+nodeName, newClusterUUID()
					deploymentUIDs[name] = uid
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{
						"name": name, "namespace": "borealis", "uid": uid, "resourceVersion": "deployment-version", "labels": map[string]any{
							"app.kubernetes.io/name": "wireguard-tunnel", "borealis.io/engine-node": nodeName, "borealis.io/node-workload": "true",
						}}}}})
					return
				case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/apis/apps/v1/namespaces/borealis/deployments/"):
					var patch map[string]any
					_ = json.NewDecoder(r.Body).Decode(&patch)
					name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					metadata := nestedMap(patch, "metadata")
					if metadata["uid"] != deploymentUIDs[name] || metadata["resourceVersion"] != "deployment-version" || coerceInt64(nestedMap(patch, "spec")["replicas"]) != 0 {
						t.Error("containment did not stop target workload")
					}
					workloadPatches.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{})
					return
				case r.Method == http.MethodPost && r.URL.Path == "/apis/batch/v1/namespaces/borealis/jobs":
					var job map[string]any
					_ = json.NewDecoder(r.Body).Decode(&job)
					container := anySlice(nestedMap(nestedMap(nestedMap(job, "spec"), "template"), "spec")["containers"])[0].(map[string]any)
					args := clusterStringSlice(container["args"])
					request, err := clusterbootstrap.DecodePreparedAdmissionRequest(args[len(args)-1])
					if err != nil || request.Action != "EnterApplicationDrain" {
						t.Error("containment used wrong native action", err, request.Action)
					}
					metadata := clusterSSHStorageMap(job, "metadata")
					metadata["uid"] = newClusterUUID()
					job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					jobs[cleanText(metadata["name"])] = job
					actions.Add(1)
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(job)
					return
				case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/apis/batch/v1/namespaces/borealis/jobs/"):
					_ = json.NewEncoder(w).Encode(jobs[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]])
					return
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/pods":
					uid := strings.TrimPrefix(r.URL.Query().Get("labelSelector"), "batch.kubernetes.io/controller-uid=")
					for _, job := range jobs {
						if cleanText(nestedMap(job, "metadata")["uid"]) == uid {
							_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{sshJoinSourcePod(t, job, newClusterUUID(), "")}})
							return
						}
					}
				}
				t.Error("unexpected containment request", r.Method, r.URL.String())
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			kube := &kubernetesAPIClient{baseURL: server.URL, token: "fixture", httpClient: server.Client()}
			runner := &kubernetesClusterStepRunner{namespace: "borealis", kube: kube, actionImage: "registry.example/node-manager@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
			operation := clusterControllerOperation{ID: newClusterUUID(), Kind: "ssh_onboarding", Attempt: 1}
			nodes := make([]clusterControllerNode, 0, len(authority.receipt.Targets))
			for _, target := range authority.receipt.Targets {
				nodes = append(nodes, clusterControllerNode{ID: target.TargetID, Name: target.Prejoin.Receipt.Node.Hostname})
			}
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), clusterSSHPreparedAdmissionRuntimeKey{}, &clusterSSHPreparedAdmissionRuntime{operation: operation, authority: authority, cleanupKube: kube}))
			cancel()
			if err := runner.containClusterSSHPreparedAdmissionTargets(ctx, operation, nodes); (err != nil) != (mode != "exact pair") {
				t.Fatal("cancelled pair containment", err)
			}
			wantNodes, wantWorkloads := int64(2), int64(2)
			if mode == "replaced node" {
				wantNodes = 1
			}
			if mode != "exact pair" {
				wantWorkloads = 1
			}
			if nodePatches.Load() != wantNodes || workloadPatches.Load() != wantWorkloads || actions.Load() != 2 {
				t.Fatal("incomplete pair containment", nodePatches.Load(), workloadPatches.Load(), actions.Load())
			}
		})
	}
}

func nodeOrdinal(authority clusterSSHPreparedAuthority, name string) int {
	for index, target := range authority.receipt.Targets {
		if target.Prejoin != nil && target.Prejoin.Receipt.Node.Hostname == name {
			return index
		}
	}
	return -1
}
