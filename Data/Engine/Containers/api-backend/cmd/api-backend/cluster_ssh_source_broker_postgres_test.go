package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHSourceBrokerPostgresAuthorityAndPrivateTransfer(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "controller changed", "worker expired", "credential removed", "Aegis locked", "Secret UID changed", "Secret revision changed", "excluded Secret data changed"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHPreparationAuthorityFixtureForTopology(t, mode == "replacement")
			f.c.store.db.SetMaxOpenConns(1)
			current, err := f.read(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			before := f.events(t)
			var jobs, secretReads atomic.Int64
			var changed atomic.Bool
			var mu sync.Mutex
			var job map[string]any
			podUID := newClusterUUID()
			kubernetes := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if f.c.store.db.Stats().InUse != 0 {
					t.Error("database connection retained during Kubernetes request")
				}
				if r.Method == "GET" {
					switch r.URL.Path {
					case "/api/v1/nodes":
						items := make([]any, 0, len(current.Source.Members))
						for _, member := range current.Source.Members {
							items = append(items, sshSourceKubernetesFixture(member))
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
						return
					case "/api/v1/namespaces/kube-system":
						_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{"uid": current.Source.KubeSystemUID}})
						return
					case clusterSSHRuntimeSecretPath:
						secretReads.Add(1)
						value := sshPreparationSecretFixture()
						if changed.Load() {
							switch mode {
							case "Secret UID changed":
								value["metadata"].(map[string]any)["uid"] = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
							case "Secret revision changed":
								value["metadata"].(map[string]any)["resourceVersion"] = "changed-revision"
							case "excluded Secret data changed":
								value["data"].(map[string]string)["BOREALIS_REPO_ROOT"] = base64.StdEncoding.EncodeToString([]byte("/changed"))
							}
						}
						_ = json.NewEncoder(w).Encode(value)
						return
					}
				}
				if r.Method == "POST" && r.URL.Path == "/apis/batch/v1/namespaces/borealis/jobs" {
					jobs.Add(1)
					job = nil
					if json.NewDecoder(r.Body).Decode(&job) != nil {
						t.Error("Job decode")
						w.WriteHeader(500)
						return
					}
					job["metadata"].(map[string]any)["uid"] = newClusterUUID()
					job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					switch mode {
					case "controller changed":
						f.exec(t, `UPDATE engine.cluster_application_leases SET holder='changed-controller' WHERE name=$1`, clusterControllerLeaseName)
					case "worker expired":
						f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=0 WHERE id=$1`, f.lease.TargetID)
					case "credential removed":
						f.exec(t, `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, f.lease.TargetID)
					case "Aegis locked":
						f.aegis.mu.Lock()
						clear(f.aegis.key)
						f.aegis.key = nil
						f.aegis.mu.Unlock()
					}
					w.WriteHeader(201)
					_ = json.NewEncoder(w).Encode(job)
					return
				}
				if r.Method == "GET" && job != nil {
					if strings.HasPrefix(r.URL.Path, "/apis/batch/") {
						_ = json.NewEncoder(w).Encode(job)
						return
					}
					if r.URL.Path == "/api/v1/namespaces/borealis/pods" {
						node := job["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["nodeName"]
						for _, member := range current.Source.Members {
							if member.Name != node {
								continue
							}
							network := clusterbootstrap.SourceNetwork{NodeUID: member.NodeUID, Hostname: member.Name, MachineID: member.MachineID, BootID: member.BootID, K3sVersion: current.K3sVersion, PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16"}
							_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{sourceActionPod(t, job, network, podUID)}})
							return
						}
					}
				}
				t.Error("unexpected Kubernetes request")
				w.WriteHeader(500)
			}))
			defer kubernetes.Close()
			runner := &kubernetesClusterStepRunner{controllerHolder: f.lease.ControllerHolder, namespace: "borealis", actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64),
				jobPollInterval: time.Millisecond, kube: &kubernetesAPIClient{baseURL: kubernetes.URL, token: "private-fixture", httpClient: kubernetes.Client()}}
			controller := &clusterController{store: f.c.store, runner: runner, holder: f.c.holder}
			t.Setenv("BOREALIS_OPERATOR_SECRET", sshBrokerTestSecret)
			server := httptest.NewServer(controller.healthServer().Handler)
			defer server.Close()
			client := sshBrokerClient(t, server.URL)
			client.httpClient.Timeout = 10 * time.Second
			read := client.preparationRead(f.read, f.lease, f.baseline, f.sealed)
			expected, settings, err := read(f.ctx)
			good := mode == "expansion" || mode == "replacement" || strings.Contains(mode, "Secret")
			if good {
				if err != nil || expected.Target.TargetID != f.lease.TargetID || !reflect.DeepEqual(settings, sshPreparationRuntimeFixture()) {
					t.Fatal("valid private broker source rejected")
				}
				changed.Store(true)
				_, next, err := read(f.ctx)
				if strings.Contains(mode, "Secret") {
					if err != clusterbootstrap.ErrPreparationConfig || next != nil {
						t.Fatal("Secret observation drift accepted")
					}
				} else if err != nil || !reflect.DeepEqual(next, settings) || jobs.Load() != int64(2*len(current.Source.Members)) {
					t.Fatal("fresh source Job not observed per member/read")
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || settings != nil {
				t.Fatal("stale authority returned private settings")
			}
			if jobs.Load() == 0 {
				t.Fatal("actual controller source transport was not exercised")
			}
			if !good && mode != "Aegis locked" && secretReads.Load() != 0 {
				t.Fatal("private Secret read after persisted authority loss")
			}
			if f.c.store.db.Stats().InUse != 0 || f.events(t) != before {
				t.Fatal("broker retained DB connection or published event")
			}
		})
	}
}
