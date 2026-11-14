package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func sshMembershipNativeFixture(a clusterSSHPreparationAuthority, uids map[string]string, path string) map[string]any {
	object := sshPreparedNativeFixture(a, uids, path)
	if path == "/api/v1/nodes" {
		for _, item := range anySlice(object["items"]) {
			node := item.(map[string]any)
			metadata := clusterSSHStorageMap(node, "metadata")
			name := metadata["name"].(string)
			metadata["annotations"] = map[string]any{"etcd.k3s.cattle.io/node-name": name + "-12345678"}
			info := clusterSSHStorageMap(clusterSSHStorageMap(node, "status"), "nodeInfo")
			info["kubeletVersion"], info["architecture"], info["operatingSystem"] = a.K3sVersion, "amd64", "linux"
		}
	}
	return object
}
func sshMembershipAuthorityFixture(t *testing.T, replacement bool) (clusterSSHPreparationAuthority, clusterSSHPreparedAuthority, map[string]string) {
	a, _, _ := sshPrejoinSourceFixture(t, replacement)
	prepared := clusterSSHPreparedAuthority{proof: clusterSSHParentProof{Cohort: a.Cohort, Source: a.Source}, version: a.K3sVersion}
	uids := map[string]string{}
	for i, target := range a.Cohort.Targets {
		lease := a.Lease
		lease.TargetID = target.Binding.TargetID
		lease.Generation = target.Generation + 1
		e, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
		if err != nil {
			t.Fatal(err)
		}
		uid := newClusterUUID()
		uids[target.Binding.TargetID] = uid
		receipt := sshPrejoinReceiptIdentityFixture(e, *a.IdentitySource, uid, math.MaxUint64-uint64(i))
		prepared.receipt.Targets = append(prepared.receipt.Targets, clusterSSHPreparationResult{TargetID: target.Binding.TargetID, Prejoin: &clusterSSHPrejoinResult{Expected: e, Receipt: receipt}})
	}
	return a, prepared, uids
}
func sshMembershipRequestFixture(t *testing.T, replacement bool) clusterbootstrap.MembershipObservationRequest {
	a, p, uids := sshMembershipAuthorityFixture(t, replacement)
	get := func(ctx context.Context, path string, out any) error {
		raw, _ := json.Marshal(sshMembershipNativeFixture(a, uids, path))
		return json.Unmarshal(raw, out)
	}
	nodes, _, err := readClusterSSHPreparedMembershipNodes(context.Background(), get, p)
	if err != nil {
		t.Fatal(err)
	}
	selected := a.Source.Members[len(a.Source.Members)-1]
	request := clusterbootstrap.MembershipObservationRequest{Source: *a.IdentitySource, Network: sshSourceNetworkFixture(selected, a.K3sVersion), KubeSystemUID: a.Source.KubeSystemUID, Nodes: nodes}
	for _, target := range p.receipt.Targets {
		request.Prepared = append(request.Prepared, clusterbootstrap.MembershipPreparedTarget{Expected: target.Prejoin.Expected, Receipt: target.Prejoin.Receipt})
	}
	if !request.Valid() {
		t.Fatal("invalid request fixture")
	}
	return request
}
func sshMembershipResultFixture(request clusterbootstrap.MembershipObservationRequest) clusterbootstrap.MembershipObservation {
	o := clusterbootstrap.MembershipObservation{Source: request.Source, RequestSHA256: request.SHA256(), Membership: clusterbootstrap.K3sMembership{Voting: true}}
	for i, node := range request.Nodes {
		id := uint64(9007199254740993 + uint64(i))
		for _, target := range request.Prepared {
			if target.Receipt.Node == node.Node {
				id = target.Receipt.MemberID
			}
		}
		o.Membership.Members = append(o.Membership.Members, clusterbootstrap.K3sEtcdMember{ID: id, Name: node.MemberName, Address: node.Node.Address})
		if node.Node.NodeUID == request.Source.SourceUID {
			o.LocalNode = node.Node
			o.Membership.LocalID = id
		}
	}
	slices.SortFunc(o.Membership.Members, func(a, b clusterbootstrap.K3sEtcdMember) int { return strings.Compare(a.Address, b.Address) })
	return o
}
func sshMembershipPod(t *testing.T, job map[string]any, request clusterbootstrap.MembershipObservationRequest, podUID string) map[string]any {
	t.Helper()
	pod := sourceActionPod(t, job, request.Network, podUID)
	container := anySlice(sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")["containers"])[0].(map[string]any)
	args := clusterStringSlice(container["args"])
	result := sshMembershipResultFixture(request)
	encoded, err := clusterbootstrap.EncodeMembershipObservationResult(result, request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceMembership", "result": map[string]any{"membership_observation": encoded}})
	receipt, err := clusterbootstrap.NewMembershipObservationReceipt(raw, request, args[0], sourceActionMap(job, "metadata")["uid"].(string), podUID)
	if err != nil {
		t.Fatal(err)
	}
	status := anySlice(sourceActionMap(pod, "status")["containerStatuses"])[0].(map[string]any)
	sourceActionMap(sourceActionMap(status, "state"), "terminated")["message"] = string(receipt)
	return pod
}

func TestClusterSSHPreparedMembershipFreshJob(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "fresh twice", "lost authority", "collision", "lost POST", "job replaced", "job missing metadata", "job command", "job args", "job image", "job failed", "job status alias", "job suspend alias", "job deletion alias", "job duplicate UID", "duplicate conditions", "two pods", "paginated pods", "pod list alias", "pod replaced", "wrong owner", "pod deleting", "pod deletion alias", "pod lastState alias", "pod security alias", "pod duplicate status", "wrong nonce", "wrong receipt pod", "wrong request", "sidecar", "token mounted", "privileged", "changed mounts", "pod retry", "pod failed", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			request := sshMembershipRequestFixture(t, mode == "replacement")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if mode == "deadline" {
				var end context.CancelFunc
				ctx, end = context.WithTimeout(ctx, 30*time.Millisecond)
				defer end()
			}
			var job map[string]any
			posts, podReads, checks := 0, 0, 0
			names := []string{}
			podUID := newClusterUUID()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					if mode == "collision" {
						w.WriteHeader(409)
						return
					}
					if mode == "lost POST" {
						w.WriteHeader(502)
						return
					}
					if r.URL.Path != "/apis/batch/v1/namespaces/borealis/jobs" || json.NewDecoder(r.Body).Decode(&job) != nil {
						t.Error("unexpected POST")
						w.WriteHeader(500)
						return
					}
					names = append(names, sourceActionMap(job, "metadata")["name"].(string))
					sourceActionMap(job, "metadata")["uid"] = newClusterUUID()
					container := anySlice(sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")["containers"])[0].(map[string]any)
					args := clusterStringSlice(container["args"])
					parsed, err := clusterbootstrap.DecodeMembershipObservationRequest(args[1])
					if len(args) != 2 || strings.Join(clusterStringSlice(container["command"]), " ") != "/usr/local/bin/borealis-node-manager source-membership-client" || err != nil || !reflect.DeepEqual(parsed, request) {
						t.Error("manifest request changed", err)
					}
					job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(job)
					return
				}
				if r.Method != "GET" || job == nil {
					t.Error("unexpected replay")
					w.WriteHeader(500)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/apis/batch/") {
					out := sourceActionCopy(t, job)
					container := anySlice(sourceActionMap(sourceActionMap(sourceActionMap(out, "spec"), "template"), "spec")["containers"])[0].(map[string]any)
					switch mode {
					case "job replaced":
						sourceActionMap(out, "metadata")["uid"] = newClusterUUID()
					case "job missing metadata":
						delete(out, "metadata")
					case "job command":
						container["command"] = []string{"arbitrary"}
					case "job args":
						container["args"] = []string{"arbitrary"}
					case "job image":
						container["image"] = "mutable:latest"
					case "job suspend alias":
						sourceActionMap(out, "spec")["Suspend"] = true
					case "job deletion alias":
						sourceActionMap(out, "metadata")["DeletionTimestamp"] = "2026-11-02T00:00:00Z"
					case "job status alias":
						sourceActionMap(out, "status")["Failed"] = 1
					case "job failed":
						sourceActionMap(out, "status")["failed"] = 1
					case "duplicate conditions":
						sourceActionMap(out, "status")["conditions"] = []any{map[string]any{"type": "Complete", "status": "True"}, map[string]any{"type": "Complete", "status": "True"}}
					}
					if mode == "job duplicate UID" {
						raw, _ := json.Marshal(out)
						raw = []byte(strings.Replace(string(raw), `"uid":`, `"uid":"`+newClusterUUID()+`","uid":`, 1))
						_, _ = w.Write(raw)
						return
					}
					_ = json.NewEncoder(w).Encode(out)
					return
				}
				if r.URL.Path != "/api/v1/namespaces/borealis/pods" {
					t.Error("foreign read")
					w.WriteHeader(500)
					return
				}
				podReads++
				pod := sshMembershipPod(t, job, request, podUID)
				metadata, spec, status := sourceActionMap(pod, "metadata"), sourceActionMap(pod, "spec"), sourceActionMap(pod, "status")
				container := anySlice(spec["containers"])[0].(map[string]any)
				terminated := sourceActionMap(sourceActionMap(anySlice(status["containerStatuses"])[0].(map[string]any), "state"), "terminated")
				switch mode {
				case "pod replaced":
					if podReads == 1 {
						status["phase"] = "Running"
					} else {
						metadata["uid"] = newClusterUUID()
					}
				case "wrong owner":
					anySlice(metadata["ownerReferences"])[0].(map[string]any)["uid"] = newClusterUUID()
				case "pod deletion alias":
					metadata["DeletionTimestamp"] = "2026-11-02T00:00:00Z"
				case "pod lastState alias":
					entry := anySlice(status["containerStatuses"])[0].(map[string]any)
					delete(entry, "lastState")
					entry["LastState"] = map[string]any{"terminated": map[string]any{"exitCode": 1}}
				case "pod security alias":
					sourceActionMap(container, "securityContext")["Privileged"] = true
				case "pod deleting":
					metadata["deletionTimestamp"] = "2026-11-02T00:00:00Z"
				case "wrong nonce":
					terminated["message"] = strings.Replace(terminated["message"].(string), clusterStringSlice(container["args"])[0], newClusterUUID(), 1)
				case "wrong receipt pod":
					terminated["message"] = strings.Replace(terminated["message"].(string), podUID, newClusterUUID(), 1)
				case "wrong request":
					terminated["message"] = strings.Replace(terminated["message"].(string), request.SHA256(), strings.Repeat("a", 64), 1)
				case "sidecar":
					spec["containers"] = append(anySlice(spec["containers"]), container)
				case "token mounted":
					spec["automountServiceAccountToken"] = true
				case "privileged":
					sourceActionMap(container, "securityContext")["privileged"] = true
				case "changed mounts":
					container["volumeMounts"] = []any{}
				case "pod retry":
					anySlice(status["containerStatuses"])[0].(map[string]any)["restartCount"] = 1
				case "pod failed":
					terminated["exitCode"] = 1
				case "cancel":
					cancel()
				case "deadline":
					status["phase"] = "Running"
				}
				items := []any{pod}
				metadataList := map[string]any{}
				if mode == "two pods" {
					items = append(items, pod)
				}
				if mode == "paginated pods" {
					metadataList["continue"] = "next"
				}
				if mode == "pod duplicate status" {
					raw, _ := json.Marshal(map[string]any{"items": items, "metadata": metadataList})
					raw = []byte(strings.Replace(string(raw), `"status":`, `"status":{},"status":`, 1))
					_, _ = w.Write(raw)
					return
				}
				if mode == "pod list alias" {
					_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "Metadata": map[string]any{"continue": "next"}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "metadata": metadataList})
			}))
			defer server.Close()
			runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}, namespace: "borealis", controllerHolder: "controller", actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
			check := func() bool { checks++; return mode != "lost authority" || checks < 2 }
			got, err := runner.readSSHPreparedMembership(ctx, request.Prepared[0].Expected.Target.OperationID, request, check)
			success := mode == "pair" || mode == "replacement" || mode == "fresh twice"
			if (err == nil) != success {
				t.Fatal("wrong result", err, "posts", posts, "podReads", podReads, "checks", checks)
			}
			if success && (!got.Observation.Valid(request) || got.Nonce == "" || got.PodUID != podUID || checks < 2) {
				t.Fatal("unbound result")
			}
			if !success && !reflect.DeepEqual(got, clusterSSHMembershipReceipt{}) {
				t.Fatal("partial proof escaped")
			}
			if mode == "fresh twice" {
				second, err := runner.readSSHPreparedMembership(ctx, request.Prepared[0].Expected.Target.OperationID, request, check)
				if err != nil || second.Nonce == got.Nonce || second.JobUID == got.JobUID || len(names) != 2 || names[0] == names[1] {
					t.Fatal("adopted prior Job", err)
				}
			} else if posts != 1 {
				t.Fatal("POST replay", posts)
			}
		})
	}
}

func TestClusterSSHPreparedMembershipNativeAnnotations(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "missing annotations", "annotations alias", "member name", "removed", "removed alias", "version", "architecture", "operating system", "target member name"} {
		t.Run(mode, func(t *testing.T) {
			a, p, uids := sshMembershipAuthorityFixture(t, mode == "replacement")
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				object := sshMembershipNativeFixture(a, uids, r.URL.Path)
				if r.URL.Path == "/api/v1/nodes" {
					items := anySlice(object["items"])
					node := items[0].(map[string]any)
					metadata, status := sourceActionMap(node, "metadata"), sourceActionMap(node, "status")
					annotations := sourceActionMap(metadata, "annotations")
					switch mode {
					case "missing annotations":
						delete(metadata, "annotations")
					case "annotations alias":
						metadata["Annotations"] = metadata["annotations"]
						delete(metadata, "annotations")
					case "member name":
						annotations["etcd.k3s.cattle.io/node-name"] = "foreign-12345678"
					case "removed":
						annotations["etcd.k3s.cattle.io/removed-node-name"] = annotations["etcd.k3s.cattle.io/node-name"]
					case "removed alias":
						annotations["Etcd.k3s.cattle.io/removed-node-name"] = "removed"
					case "version":
						sourceActionMap(status, "nodeInfo")["kubeletVersion"] = "v1.36.4+k3s1"
					case "architecture":
						sourceActionMap(status, "nodeInfo")["architecture"] = "arm64"
					case "operating system":
						sourceActionMap(status, "nodeInfo")["operatingSystem"] = "windows"
					case "target member name":
						metadata = sourceActionMap(items[len(items)-1].(map[string]any), "metadata")
						sourceActionMap(metadata, "annotations")["etcd.k3s.cattle.io/node-name"] = metadata["name"].(string) + "-87654321"
					}
				}
				_ = json.NewEncoder(w).Encode(object)
			}))
			defer server.Close()
			kube := &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}
			nodes, targets, err := readClusterSSHPreparedMembershipNodes(context.Background(), kube.getClusterSSHNodeCohortJSON, p)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success {
				t.Fatal("wrong native annotation result", err)
			}
			if success && (len(nodes) != 3 || len(targets) != len(p.receipt.Targets)) {
				t.Fatal("incomplete membership identities")
			}
			if !success && (nodes != nil || targets != nil) {
				t.Fatal("partial observation escaped")
			}
		})
	}
}
