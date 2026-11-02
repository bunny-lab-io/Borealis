package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHPreparationConfigurationPostgresNative(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "controller", "expired controller", "attempt", "claimed target", "credential", "source", "running phase", "Secret replacement", "controller during Job", "foreign receipt", "namespace deleting", "namespace kind", "namespace name", "namespace alias", "source status alias", "source machine alias", "source address alias"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, _, _ := newSSHPreparationApprovalFixture(t, mode == "replacement")
			f.c.store.db.SetMaxOpenConns(1)
			source := snapshot.work.Proof.Source
			secret := sshPreparationConfigurationSecret()
			var mu sync.Mutex
			jobs := map[string]map[string]any{}
			pods := map[string]map[string]any{}
			var posts atomic.Int64
			var changed atomic.Bool
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if f.c.store.db.Stats().InUse != 0 {
					t.Error("native source IO retained PostgreSQL connection")
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-preclaim" {
					t.Error("missing private source transport authority")
				}
				mu.Lock()
				defer mu.Unlock()
				var value any
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/apis/batch/v1/namespaces/borealis/jobs":
					var job map[string]any
					if json.NewDecoder(r.Body).Decode(&job) != nil {
						t.Error("bad source Job")
						w.WriteHeader(400)
						return
					}
					metadata := sourceActionMap(job, "metadata")
					name, _ := metadata["name"].(string)
					if jobs[name] != nil {
						t.Error("source Job name reused")
					}
					metadata["uid"] = newClusterUUID()
					job["status"] = map[string]any{"succeeded": 1, "conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}
					node, _ := nestedMap(nestedMap(nestedMap(job, "spec"), "template"), "spec")["nodeName"].(string)
					var member clusterSSHSourceMember
					for _, candidate := range source.Members {
						if candidate.Name == node {
							member = candidate
						}
					}
					if member.Name == "" {
						t.Error("Job targeted foreign node")
						w.WriteHeader(400)
						return
					}
					network := sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version)
					if mode == "foreign receipt" && changed.Load() {
						network.BootID = newClusterUUID()
					}
					pod := sourceActionPod(t, job, network, newClusterUUID())
					jobs[name], pods[metadata["uid"].(string)] = job, pod
					posts.Add(1)
					if mode == "controller during Job" && changed.CompareAndSwap(true, false) {
						f.exec(t, `UPDATE engine.cluster_application_leases SET holder='foreign-controller' WHERE name=$1`, clusterControllerLeaseName)
					}
					w.WriteHeader(http.StatusCreated)
					value = job
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/kube-system":
					object := map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "kube-system", "uid": source.KubeSystemUID, "resourceVersion": "1"}}
					if changed.Load() {
						metadata := object["metadata"].(map[string]any)
						switch mode {
						case "namespace deleting":
							metadata["deletionTimestamp"] = "2026-10-28T00:00:00Z"
						case "namespace kind":
							object["kind"] = "Node"
						case "namespace name":
							metadata["name"] = "foreign"
						case "namespace alias":
							metadata["DeletionTimestamp"] = "2026-10-28T00:00:00Z"
						}
					}
					value = object
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/nodes":
					items := []any{}
					for _, member := range source.Members {
						node := sshSourceKubernetesFixture(member)
						node["apiVersion"], node["kind"] = "v1", "Node"
						node["metadata"].(map[string]any)["resourceVersion"] = "1"
						if changed.Load() {
							status := node["status"].(map[string]any)
							rename := func(object map[string]any, old, new string) { object[new] = object[old]; delete(object, old) }
							switch mode {
							case "source status alias":
								rename(node, "status", "Status")
							case "source machine alias":
								rename(status["nodeInfo"].(map[string]any), "machineID", "MachineID")
							case "source address alias":
								rename(status["addresses"].([]any)[0].(map[string]any), "address", "Address")
							}
						}
						items = append(items, node)
					}
					value = map[string]any{"apiVersion": "v1", "kind": "NodeList", "metadata": map[string]any{}, "items": items}
				case r.Method == http.MethodGet && r.URL.Path == clusterSSHRuntimeSecretPath:
					value = secret
				case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/apis/batch/v1/namespaces/borealis/jobs/"):
					value = jobs[strings.TrimPrefix(r.URL.Path, "/apis/batch/v1/namespaces/borealis/jobs/")]
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/borealis/pods":
					uid := strings.TrimPrefix(r.URL.Query().Get("labelSelector"), "batch.kubernetes.io/controller-uid=")
					value = map[string]any{"items": []any{pods[uid]}}
				default:
					t.Error("unexpected source request", r.Method, r.URL.Path)
					w.WriteHeader(400)
					return
				}
				if value == nil {
					t.Error("missing exact source Job")
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(value)
			}))
			defer server.Close()
			f.c.runner = &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client(), token: "synthetic-preclaim"}, namespace: "borealis", controllerHolder: f.c.holder,
				actionImage: "registry.example/api@sha256:" + strings.Repeat("a", 64), jobPollInterval: time.Millisecond}
			read := f.c.preparationConfigurationRead(snapshot)
			plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
			first, err := read(f.ctx, plan)
			if err != nil || !validClusterSSHPreparationDigest(first) || posts.Load() != int64(2*len(source.Members)) {
				t.Fatal("initial preclaim native read", err, posts.Load())
			}
			switch mode {
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='foreign-controller' WHERE name=$1`, clusterControllerLeaseName)
			case "expired controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=0 WHERE name=$1`, clusterControllerLeaseName)
			case "attempt":
				f.exec(t, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, f.op.ID)
			case "claimed target":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_holder='foreign-worker' WHERE operation_id=$1`, f.op.ID)
			case "credential":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, snapshot.work.Proof.Cohort.Targets[0].Binding.TargetID)
			case "source":
				f.exec(t, `UPDATE engine.cluster_state SET desired_size=desired_size+2 WHERE id=1`)
			case "running phase":
				f.exec(t, `UPDATE engine.cluster_operations SET state='running',current_step=$2 WHERE id=$1`, f.op.ID, clusterSSHPreparationOperationStep)
			case "Secret replacement":
				mu.Lock()
				secret["metadata"].(map[string]any)["uid"] = newClusterUUID()
				mu.Unlock()
			case "controller during Job", "foreign receipt", "namespace deleting", "namespace kind", "namespace name", "namespace alias", "source status alias", "source machine alias", "source address alias":
				changed.Store(true)
			}
			second, err := read(f.ctx, plan)
			valid := mode == "pair" || mode == "replacement"
			if (err == nil) != valid || (valid && second != first) {
				t.Fatal(mode, err)
			}
			if valid && posts.Load() != int64(4*len(source.Members)) {
				t.Fatal("source receipt reused", posts.Load())
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("gate retained database connection")
			}
			var grants int
			if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_operations WHERE id=$1 AND payload_json LIKE '%ssh_preparation_authorization%'`, f.op.ID).Scan(&grants); err != nil || grants != 0 {
				t.Fatal("gate granted preparation authority", err)
			}
		})
	}
}
