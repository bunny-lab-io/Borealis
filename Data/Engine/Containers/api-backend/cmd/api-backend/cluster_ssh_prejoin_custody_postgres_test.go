package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClusterSSHPrejoinCustodyPostgresAuthorityAndPoolRelease(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "controller drift", "credential revoked", "source Pod replaced", "final authority drift"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHPreparationAuthorityFixtureForTopology(t, mode == "replacement")
			f.c.store.db.SetMaxOpenConns(1)
			a, err := f.read(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, process, pod := existingIdentitySourceFixture(t)
			m := a.Source.Members[len(a.Source.Members)-1]
			process.NodeName = m.Name
			meta := clusterSSHStorageMap(pod, "metadata")
			clusterSSHStorageMap(meta, "labels")["borealis.io/engine-node"] = m.Name
			clusterSSHStorageMap(meta, "annotations")["borealis.io/revision"] = a.Baseline.SourceSHA
			clusterSSHStorageMap(pod, "spec")["nodeName"] = m.Name
			source := clusterSSHPrejoinSource{Binding: engineidentity.Binding{ClusterID: a.Source.ClusterID, SourceID: m.NodeID, SourceUID: m.NodeUID}, Process: process}
			pods := 0
			kube := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// HTTP does not transmit the caller's context deadline. Bound the
				// synthetic server's own fixture reads independently.
				ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
				defer cancel()
				if f.c.store.db.Stats().InUse != 0 {
					t.Error("connection held during Kubernetes IO")
					w.WriteHeader(500)
					return
				}
				var one int
				if f.c.store.db.QueryRowContext(r.Context(), "SELECT 1").Scan(&one) != nil || one != 1 {
					t.Error("one-connection pool unavailable")
					w.WriteHeader(500)
					return
				}
				var value any
				if r.URL.Path == "/api/v1/namespaces/borealis/pods/"+process.PodName {
					pods++
					if pods == 2 {
						switch mode {
						case "controller drift":
							f.exec(t, `UPDATE engine.cluster_application_leases SET holder='replacement' WHERE name=$1`, clusterControllerLeaseName)
						case "credential revoked":
							f.exec(t, `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, f.lease.TargetID)
						case "source Pod replaced":
							meta["uid"] = newClusterUUID()
						}
					}
					value = pod
				} else {
					var object map[string]any
					if err := f.observe(ctx, r.URL.Path, &object); err != nil {
						t.Error("native fixture failed", err)
						w.WriteHeader(500)
						return
					}
					if r.URL.Path == "/api/v1/nodes" {
						for _, node := range anySlice(object["items"]) {
							clusterSSHStorageMap(node.(map[string]any), "metadata")["labels"] = map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "active"}
						}
					}
					value = object
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(value)
			}))
			defer kube.Close()
			runner := &kubernetesClusterStepRunner{controllerHolder: f.c.holder, kube: &kubernetesAPIClient{baseURL: kube.URL, token: "private-test", httpClient: kube.Client()}}
			controller := &clusterController{store: f.c.store, runner: runner, holder: f.c.holder}
			t.Setenv("BOREALIS_OPERATOR_SECRET", sshBrokerTestSecret)
			t.Setenv("BOREALIS_CLUSTER_CONTROLLER_ELIGIBLE", "1")
			server := httptest.NewServer(controller.healthServer().Handler)
			defer server.Close()
			client, err := newClusterSSHPrejoinCustodyClient()
			if err != nil {
				t.Fatal(err)
			}
			client.httpClient = server.Client()
			client.peers = func(context.Context) ([]string, error) { return []string{server.URL}, nil }
			read := func(ctx context.Context) (clusterSSHPrejoinSourceCustody, error) {
				return client.observe(ctx, source, f.lease, f.baseline, f.sealed, f.read)
			}
			root := t.TempDir()
			want := sshIdentitySourceMaterial(t, root)
			consumer, err := engineidentity.AcquireConsumer(root)
			if err != nil {
				t.Fatal(err)
			}
			defer consumer.Close()
			used := false
			err = withClusterSSHPrejoinSourceMaterial(f.ctx, consumer, root, source, read, func(ctx context.Context, material *engineidentity.Material) error {
				used = true
				if f.c.store.db.Stats().InUse != 0 || !material.Equal(want) {
					t.Fatal("material or connection custody failed")
				}
				if mode == "final authority drift" {
					f.exec(t, `UPDATE engine.cluster_application_leases SET holder='replacement' WHERE name=$1`, clusterControllerLeaseName)
				}
				return nil
			})
			if mode == "expansion" || mode == "replacement" {
				if err != nil || !used || pods != 6 {
					t.Fatal("valid custody rejected", err, used, pods)
				}
			} else {
				if err != clusterbootstrap.ErrSessionAuthority || used != (mode == "final authority drift") {
					t.Fatal("unsafe custody accepted", err, used)
				}
				if strings.Contains(err.Error(), "private") {
					t.Fatal("private diagnostic escaped")
				}
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("connection leaked")
			}
		})
	}
}
