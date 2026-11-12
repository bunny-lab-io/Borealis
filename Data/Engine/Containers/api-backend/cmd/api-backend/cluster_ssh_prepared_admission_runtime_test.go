package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClusterSSHPreparedAdmissionLabelPreconditions(t *testing.T) {
	for _, mode := range []string{"valid", "replaced UID", "expired authority", "missing revision", "foreign target"} {
		t.Run(mode, func(t *testing.T) {
			a, authority, targets := sshMembershipAuthorityFixture(t, false)
			prepared := authority.receipt.Targets[0]
			name := prepared.Prejoin.Receipt.Node.Hostname
			list := sshMembershipNativeFixture(a, targets, "/api/v1/nodes")
			var patched map[string]any
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/nodes" {
					for _, raw := range anySlice(list["items"]) {
						metadata := clusterSSHStorageMap(raw.(map[string]any), "metadata")
						if metadata["name"] == name {
							metadata["resourceVersion"] = "123"
							if mode == "replaced UID" {
								metadata["uid"] = "ffffffff-ffff-4fff-8fff-ffffffffffff"
							}
							if mode == "missing revision" {
								delete(metadata, "resourceVersion")
							}
						}
					}
					_ = json.NewEncoder(w).Encode(list)
					return
				}
				if r.Method == http.MethodPatch && r.URL.Path == "/api/v1/nodes/"+name {
					_ = json.NewDecoder(r.Body).Decode(&patched)
					_, _ = io.WriteString(w, "{}")
					return
				}
				t.Error("unexpected request", r.Method, r.URL.Path)
				w.WriteHeader(500)
			}))
			defer server.Close()
			runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "fixture", httpClient: server.Client()}}
			bound := &clusterSSHPreparedAdmissionRuntime{authority: authority, startBefore: func(context.Context) (int64, error) {
				return time.Now().Add(time.Minute).Unix(), nil
			}, check: func(ctx context.Context) error {
				if mode == "expired authority" {
					return errClusterConflict
				}
				return ctx.Err()
			}}
			ctx := context.WithValue(context.Background(), clusterSSHPreparedAdmissionRuntimeKey{}, bound)
			if mode == "foreign target" {
				name = "foreign-node"
			}
			err := runner.patchNodeLabels(ctx, name, map[string]string{"borealis.io/scheduler-eligible": "true"})
			if mode == "valid" {
				metadata := clusterSSHStorageMap(patched, "metadata")
				if err != nil || metadata["uid"] != prepared.Prejoin.Receipt.Node.NodeUID || metadata["resourceVersion"] != "123" {
					t.Fatal("patch lost exact Kubernetes preconditions", err, metadata)
				}
			} else if err == nil || patched != nil {
				t.Fatal("unbound target mutated", err)
			}
		})
	}
}

func TestClusterSSHPreparedAdmissionStartDeadline(t *testing.T) {
	_, authority, _ := sshMembershipAuthorityFixture(t, false)
	runtime := &clusterSSHPreparedAdmissionRuntime{
		authority: authority,
		check:     func(context.Context) error { return nil },
		startBefore: func(context.Context) (int64, error) {
			return time.Now().Add(time.Hour).Unix(), nil
		},
	}
	node := clusterControllerNode{ID: runtime.authority.receipt.Targets[0].TargetID, Name: runtime.authority.receipt.Targets[0].Prejoin.Receipt.Node.Hostname}
	before := time.Now()
	request, err := runtime.request(context.Background(), node, "InspectHealth")
	if err != nil {
		t.Fatal(err)
	}
	if request.StartBefore <= before.Unix() || request.StartBefore > before.Add(5*time.Minute+time.Second).Unix() {
		t.Fatal("unbounded prepared admission start deadline", request.StartBefore)
	}
	runtime.startBefore = func(context.Context) (int64, error) { return time.Now().Add(-time.Second).Unix(), nil }
	if _, err := runtime.request(context.Background(), node, "InspectHealth"); err == nil {
		t.Fatal("expired prepared admission start deadline accepted")
	}
}

func TestClusterSSHPreparedAdmissionPhaseNodes(t *testing.T) {
	for _, mode := range []string{"initial drained", "initial active rejects", "apply drained", "apply active", "verify active", "verify drained rejects", "verify partial roles rejects", "apply foreign UID rejects"} {
		t.Run(mode, func(t *testing.T) {
			a, authority, targets := sshMembershipAuthorityFixture(t, false)
			phase := clusterSSHPreparationOperationStep
			if strings.HasPrefix(mode, "apply") {
				phase = "apply_membership"
			} else if strings.HasPrefix(mode, "verify") {
				phase = "verify_quorum"
			}
			get := func(ctx context.Context, path string, out any) error {
				value := sshMembershipNativeFixture(a, targets, path)
				if path == "/api/v1/nodes" {
					for _, raw := range anySlice(value["items"]) {
						metadata := clusterSSHStorageMap(raw.(map[string]any), "metadata")
						for _, prepared := range authority.receipt.Targets {
							if metadata["name"] != prepared.Prejoin.Receipt.Node.Hostname {
								continue
							}
							labels := clusterSSHStorageMap(metadata, "labels")
							if strings.Contains(mode, "active") || strings.Contains(mode, "partial") {
								labels["borealis.io/application-state"] = "active"
								for _, role := range []string{"control-plane", "edge", "scheduler", "postgres-primary"} {
									labels["borealis.io/"+role+"-eligible"] = "true"
								}
							}
							if strings.Contains(mode, "partial") {
								labels["borealis.io/edge-eligible"] = "false"
							}
							if strings.Contains(mode, "foreign UID") {
								metadata["uid"] = "ffffffff-ffff-4fff-8fff-ffffffffffff"
							}
						}
					}
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return err
				}
				return json.Unmarshal(raw, out)
			}
			_, _, err := readClusterSSHPreparedMembershipNodesForPhase(context.Background(), get, authority, phase)
			if (err != nil) != strings.Contains(mode, "rejects") {
				t.Fatal("wrong phase-bound Node result", err)
			}
		})
	}
}

func TestClusterSSHPreparedAdmissionHTTPExpiry(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			live, effects := true, 0
			transport := clusterSSHPreparedAdmissionHTTP{
				transport: bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodGet {
						live = false // Existing-Job read spent remaining authority.
					} else {
						effects++
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
				}),
				check: func(ctx context.Context) error {
					if !live {
						return errClusterConflict
					}
					return ctx.Err()
				},
			}
			request, _ := http.NewRequest(method, "https://fixture.invalid/action", nil)
			response, err := transport.RoundTrip(request)
			if err != nil || effects != 1 {
				t.Fatal("live action blocked", err)
			}
			_ = response.Body.Close()
			read, _ := http.NewRequest(http.MethodGet, "https://fixture.invalid/job", nil)
			response, err = transport.RoundTrip(read)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			response, err = transport.RoundTrip(request)
			if err == nil || response != nil || effects != 1 {
				t.Fatal("expired authority reached mutation transport", err, effects)
			}
		})
	}
}

func TestClusterSSHPreparedAdmissionJobIdentity(t *testing.T) {
	for _, mode := range []string{"valid", "job replaced", "job altered", "pod invalid", "wrong owner", "sidecar", "retry", "failed"} {
		t.Run(mode, func(t *testing.T) {
			operationID, jobUID, podUID := newClusterUUID(), newClusterUUID(), newClusterUUID()
			manifest := clusterActionJobManifest("borealis-admission-fixture", "borealis", "engine-02",
				"ghcr.io/bunny-lab-io/borealis/api-backend@sha256:"+strings.Repeat("a", 64),
				[]string{"client", "--verb", clusterbootstrap.PreparedAdmissionVerb, "--prepared-admission", "private"}, operationID, "admit:fixture")
			configureClusterSSHPreparedAdmissionJob(manifest)
			job := sourceActionCopy(t, manifest)
			clusterSSHStorageMap(job, "metadata")["uid"] = jobUID
			job["status"] = map[string]any{"succeeded": 1, "active": 0, "failed": 0,
				"conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
			pod := sshJoinSourcePod(t, job, podUID, "ack")
			switch mode {
			case "job replaced":
				clusterSSHStorageMap(job, "metadata")["uid"] = newClusterUUID()
			case "job altered":
				clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(job, "spec"), "template"), "spec")["nodeName"] = "engine-03"
			case "pod invalid":
				clusterSSHStorageMap(pod, "metadata")["uid"] = "00000000-0000-0000-0000-000000000000"
			case "wrong owner":
				anySlice(clusterSSHStorageMap(pod, "metadata")["ownerReferences"])[0].(map[string]any)["uid"] = newClusterUUID()
			case "sidecar":
				spec := clusterSSHStorageMap(pod, "spec")
				spec["containers"] = append(anySlice(spec["containers"]), map[string]any{"name": "sidecar"})
			case "retry":
				anySlice(clusterSSHStorageMap(pod, "status")["containerStatuses"])[0].(map[string]any)["restartCount"] = 1
			case "failed":
				terminated := clusterSSHStorageMap(clusterSSHStorageMap(anySlice(clusterSSHStorageMap(pod, "status")["containerStatuses"])[0].(map[string]any), "state"), "terminated")
				terminated["exitCode"], terminated["reason"] = 1, "Error"
			}
			jobValid := validClusterSSHSourceJob(job, manifest, jobUID)
			complete, err := validClusterSSHPreparedAdmissionPod(pod, manifest, jobUID)
			if mode == "valid" {
				if !jobValid || err != nil || !complete {
					t.Fatal("exact Job/Pod rejected", jobValid, complete, err)
				}
			} else if jobValid && err == nil && complete {
				t.Fatal("changed Job/Pod accepted", mode)
			}
		})
	}
}
