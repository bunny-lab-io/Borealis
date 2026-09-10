package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sourceActionMap(m map[string]any, key string) map[string]any {
	value, _ := m[key].(map[string]any)
	return value
}

func sourceActionCopy(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var copy map[string]any
	if json.Unmarshal(raw, &copy) != nil {
		t.Fatal("copy")
	}
	return copy
}

func sourceActionPod(t *testing.T, job map[string]any, network clusterbootstrap.SourceNetwork, podUID string) map[string]any {
	t.Helper()
	spec := sourceActionMap(sourceActionMap(sourceActionMap(job, "spec"), "template"), "spec")
	nonce := clusterStringSlice(anySlice(spec["containers"])[0].(map[string]any)["args"])[0]
	jobUID := sourceActionMap(job, "metadata")["uid"].(string)
	raw, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceNetwork", "result": map[string]any{"source_network": network}})
	receipt, err := clusterbootstrap.NewSourceNetworkReceipt(raw, nonce, jobUID, podUID)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{
		"name": sourceActionMap(job, "metadata")["name"].(string) + "-pod", "namespace": "borealis", "uid": podUID,
		"labels":          map[string]any{"batch.kubernetes.io/controller-uid": jobUID},
		"ownerReferences": []any{map[string]any{"apiVersion": "batch/v1", "kind": "Job", "name": sourceActionMap(job, "metadata")["name"], "uid": jobUID, "controller": true}},
	}, "spec": sourceActionCopy(t, spec), "status": map[string]any{"phase": "Succeeded", "containerStatuses": []any{map[string]any{
		"name": "action", "restartCount": 0, "lastState": map[string]any{}, "state": map[string]any{"terminated": map[string]any{"exitCode": 0, "reason": "Completed", "message": string(receipt)}},
	}}}}
}

func TestClusterSSHSourceActionFreshJobReceiptAndAuthority(t *testing.T) {
	for _, mode := range []string{"success", "source reader", "lost authority", "wrong controller", "foreign member", "mutable image", "collision", "lost POST", "job replaced", "job missing metadata", "job altered", "job image", "job failed", "duplicate conditions", "two pods", "paginated pods", "pod replaced", "wrong owner", "pod deleting", "wrong nonce", "wrong receipt pod", "private receipt", "wrong source", "sidecar", "token mounted", "privileged", "logs fallback", "changed mounts", "pod retry", "pod failed", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			cohort, source, lease, baseline := sshPreparationFixture(t)
			current := clusterSSHPreparationAuthority{Cohort: cohort, Source: source, Lease: lease, Baseline: baseline, K3sVersion: "v1.36.3+k3s1"}
			member := source.Members[0]
			network := clusterbootstrap.SourceNetwork{NodeUID: member.NodeUID, Hostname: member.Name, MachineID: member.MachineID, BootID: member.BootID, K3sVersion: current.K3sVersion, PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16"}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if mode == "deadline" {
				var end context.CancelFunc
				ctx, end = context.WithTimeout(ctx, 30*time.Millisecond)
				defer end()
			}
			authorityCalls := 0
			var posts, podReads atomic.Int64
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				authorityCalls++
				if mode == "lost authority" && authorityCalls >= 3 {
					return clusterSSHPreparationAuthority{}, errors.New("private authority")
				}
				copy := current
				copy.Cohort.ObservedAt += int64(authorityCalls)
				return copy, nil
			}
			var job map[string]any
			names := make(chan string, 2)
			podUID := newClusterUUID()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Authorization") != "Bearer synthetic-private" {
					t.Error("authorization missing")
				}
				if request.Method == "GET" {
					switch request.URL.Path {
					case "/api/v1/nodes":
						_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{sshSourceKubernetesFixture(source.Members[0])}})
						return
					case "/api/v1/namespaces/kube-system":
						_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"uid": source.KubeSystemUID}})
						return
					case clusterSSHRuntimeSecretPath:
						_ = json.NewEncoder(w).Encode(sshPreparationSecretFixture())
						return
					}
				}
				if request.Method == "POST" {
					posts.Add(1)
					if request.URL.Path != "/apis/batch/v1/namespaces/borealis/jobs" {
						t.Error("unexpected write")
					}
					if mode == "collision" {
						w.WriteHeader(409)
						return
					}
					if mode == "lost POST" {
						w.WriteHeader(502)
						w.Write([]byte("private transport failure"))
						return
					}
					if json.NewDecoder(request.Body).Decode(&job) != nil {
						t.Error("manifest missing")
						return
					}
					metadata := sourceActionMap(job, "metadata")
					metadata["uid"] = newClusterUUID()
					names <- metadata["name"].(string)
					job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					w.WriteHeader(201)
					_ = json.NewEncoder(w).Encode(job)
					return
				}
				if request.Method != "GET" || job == nil {
					t.Error("unexpected lookup/replay")
					w.WriteHeader(500)
					return
				}
				if strings.HasPrefix(request.URL.Path, "/apis/batch/") {
					out := sourceActionCopy(t, job)
					if mode == "job replaced" {
						sourceActionMap(out, "metadata")["uid"] = newClusterUUID()
					}
					if mode == "job missing metadata" {
						delete(out, "metadata")
					}
					if mode == "job altered" {
						sourceActionMap(out, "spec")["backoffLimit"] = 1
					}
					if mode == "job image" {
						sourceActionMap(sourceActionMap(sourceActionMap(out, "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)["image"] = "registry.example/api@sha256:" + strings.Repeat("b", 64)
					}

					if mode == "job failed" {
						sourceActionMap(out, "status")["failed"] = 1
					}
					if mode == "duplicate conditions" {
						s := sourceActionMap(out, "status")
						s["conditions"] = append(anySlice(s["conditions"]), map[string]any{"type": "Complete", "status": "True"})
					}
					_ = json.NewEncoder(w).Encode(out)
					return
				}
				if request.URL.Path != "/api/v1/namespaces/borealis/pods" || request.URL.Query().Get("labelSelector") != "batch.kubernetes.io/controller-uid="+sourceActionMap(job, "metadata")["uid"].(string) {
					t.Error("unscoped Pod read")
					w.WriteHeader(500)
					return
				}
				if mode == "cancel" {
					cancel()
					<-request.Context().Done()
					return
				}
				if mode == "deadline" {
					<-request.Context().Done()
					return
				}
				podReads.Add(1)
				n := network
				if mode == "wrong source" {
					n.BootID = newClusterUUID()
				}
				p := sourceActionPod(t, job, n, podUID)
				meta, spec, status := sourceActionMap(p, "metadata"), sourceActionMap(p, "spec"), sourceActionMap(p, "status")
				c := anySlice(status["containerStatuses"])[0].(map[string]any)
				terminated := sourceActionMap(sourceActionMap(c, "state"), "terminated")
				switch mode {
				case "pod replaced":
					if podReads.Load() == 1 {
						status["phase"] = "Running"
						c["state"] = map[string]any{"running": map[string]any{}}
					} else {
						meta["uid"] = newClusterUUID()
					}
				case "wrong owner":
					anySlice(meta["ownerReferences"])[0].(map[string]any)["uid"] = newClusterUUID()
				case "pod deleting":
					meta["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
				case "wrong nonce":
					terminated["message"] = strings.Replace(terminated["message"].(string), `"nonce":"`, `"nonce":"invalid`, 1)
				case "wrong receipt pod":
					terminated["message"] = strings.ReplaceAll(terminated["message"].(string), podUID, newClusterUUID())
				case "private receipt":
					terminated["message"] = strings.Replace(terminated["message"].(string), `"version":1`, `"version":1,"private":"never publish"`, 1)
				case "sidecar":
					spec["containers"] = append(anySlice(spec["containers"]), map[string]any{"name": "sidecar"})
				case "token mounted":
					spec["automountServiceAccountToken"] = true
				case "privileged":
					sourceActionMap(anySlice(spec["containers"])[0].(map[string]any), "securityContext")["privileged"] = true
				case "logs fallback":
					anySlice(spec["containers"])[0].(map[string]any)["terminationMessagePolicy"] = "FallbackToLogsOnError"
				case "changed mounts":
					anySlice(spec["volumes"])[0].(map[string]any)["hostPath"] = map[string]any{"path": "/private", "type": "Directory"}
				case "pod retry":
					c["restartCount"] = 1
				case "pod failed":
					terminated["exitCode"] = 1
					status["phase"] = "Failed"
				}
				items := []any{p}
				if mode == "two pods" {
					items = append(items, sourceActionPod(t, job, network, newClusterUUID()))
				}
				metadata := map[string]any{}
				if mode == "paginated pods" {
					metadata["continue"] = "more"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"metadata": metadata, "items": items})
			}))
			defer server.Close()
			runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, token: "synthetic-private", httpClient: server.Client()}, namespace: "borealis", controllerHolder: lease.ControllerHolder, actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
			if mode == "wrong controller" {
				runner.controllerHolder = "other"
			}
			if mode == "mutable image" {
				runner.actionImage = "registry.example/api:main"
			}
			if mode == "foreign member" {
				member.NodeUID = newClusterUUID()
			}
			read := runner.newSSHSourceNetworkRead(authority)
			if mode == "source reader" {
				observe := newClusterSSHPreparationSourceRead(authority, runner.kube.getClusterSSHPreparationJSON, read)
				expected, settings, err := observe(ctx)
				if err != nil || expected.Target.TargetID != lease.TargetID || settings["POSTGRES_PASSWORD"] != "private-test" || posts.Load() != 1 {
					t.Fatalf("source composition failed: %v; posts=%d", err, posts.Load())
				}
				return
			}
			got, err := read(ctx, member)
			if mode == "success" {
				if err != nil || got != network {
					t.Fatalf("observation failed: %v; posts=%d pods=%d authority=%d", err, posts.Load(), podReads.Load(), authorityCalls)
				}
				got, err = read(ctx, member)
				if err != nil || got != network || posts.Load() != 2 || <-names == <-names {
					t.Fatal("observation reused Job")
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || got != (clusterbootstrap.SourceNetwork{}) {
				t.Fatalf("unsafe observation: %v", err)
			}
			if mode != "success" && posts.Load() > 1 {
				t.Fatal("POST retried")
			}
			if (mode == "wrong controller" || mode == "foreign member" || mode == "mutable image") && posts.Load() != 0 {
				t.Fatal("invalid authority created Job")
			}
		})
	}
}

func TestClusterSSHSourceActionHTTPBoundary(t *testing.T) {
	for _, mode := range []string{"success", "wrong status", "redirect", "plaintext", "oversize", "secret write", "unscoped pods", "path traversal", "extra query"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if mode == "redirect" {
					w.Header().Set("Location", "/forbidden")
					w.WriteHeader(302)
					return
				}
				if mode != "wrong status" {
					w.WriteHeader(201)
				}
				if mode == "oversize" {
					w.Write([]byte(strings.Repeat(" ", (2<<20)+1)))
					return
				}
				w.Write([]byte(`{"accepted":true}`))
			}))
			defer server.Close()
			client := &kubernetesAPIClient{baseURL: server.URL, token: "private", httpClient: server.Client()}
			method, path := http.MethodPost, "/apis/batch/v1/namespaces/borealis/jobs"
			switch mode {
			case "plaintext":
				client.baseURL = strings.Replace(server.URL, "https:", "http:", 1)
			case "secret write":
				path = clusterSSHRuntimeSecretPath
			case "unscoped pods":
				method, path = "GET", "/api/v1/namespaces/borealis/pods"
			case "path traversal":
				method, path = "GET", path+"/borealis-source-../../secrets"
			case "extra query":
				method, path = "GET", "/api/v1/namespaces/borealis/pods?labelSelector=batch.kubernetes.io%2Fcontroller-uid%3D11111111-1111-4111-8111-111111111111&watch=true"
			}
			var out map[string]any
			err := client.doClusterSSHSourceJSON(context.Background(), method, path, map[string]any{}, &out)
			if mode == "success" {
				if err != nil || out["accepted"] != true {
					t.Fatal("valid bounded request failed")
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || out != nil {
				t.Fatal("boundary accepted or disclosed response")
			}
			if calls.Load() > 1 {
				t.Fatal("redirect or request replay")
			}
			if (mode == "plaintext" || mode == "secret write" || mode == "unscoped pods" || mode == "path traversal" || mode == "extra query") && calls.Load() != 0 {
				t.Fatal("invalid boundary sent credential")
			}
		})
	}
}
