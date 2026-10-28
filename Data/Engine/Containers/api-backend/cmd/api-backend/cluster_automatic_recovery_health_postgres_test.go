package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClusterAutomaticRecoveryPostgresNativeNodeAuthority(t *testing.T) {
	for _, mode := range []string{"partial health", "holder before", "holder after", "expiry before", "expiry after", "cluster after", "drain after", "node after", "logical cluster mismatch", "runner mismatch", "target mismatch"} {
		t.Run(mode, func(t *testing.T) {
			f := newAutomaticRecoveryPostgresFixture(t)
			r := f.reserve(t)
			b, namespace, node := automaticRecoveryNativeFixture()
			b.ClusterID, b.NodeID, b.NodeName, b.NodeUID, b.Controller = r.ClusterID, r.NodeID, r.NodeName, r.NodeUID, r.Controller
			clusterSSHStorageMap(node, "metadata")["name"], clusterSSHStorageMap(node, "metadata")["uid"] = b.NodeName, b.NodeUID
			var calls atomic.Int32
			changeAuthority := func() {
				var query string
				var args []any
				switch mode {
				case "holder before", "holder after":
					query, args = `UPDATE engine.cluster_application_leases SET holder='replacement' WHERE name=$1`, []any{clusterControllerLeaseName}
				case "expiry before", "expiry after":
					query, args = `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, []any{clusterControllerLeaseName}
				case "cluster after":
					query, args = `UPDATE engine.cluster_state SET cluster_id=$1 WHERE id=1`, []any{newClusterUUID()}
				case "drain after":
					query, args = `UPDATE engine.cluster_nodes SET application_state='drained',drain_reason='operator' WHERE id=$1`, []any{b.NodeID}
				case "node after":
					query, args = `UPDATE engine.cluster_nodes SET node_name='replaced' WHERE id=$1`, []any{b.NodeID}
				}
				if query != "" {
					if _, err := f.c.controller.store.db.ExecContext(f.ctx, query, args...); err != nil {
						t.Error(err)
					}
				}
			}
			if strings.HasSuffix(mode, " before") {
				changeAuthority()
			}
			// Restore the logical row for the fixture's exact-identity cleanup.
			if mode == "cluster after" {
				t.Cleanup(func() { f.exec(t, `UPDATE engine.cluster_state SET cluster_id=$1 WHERE id=1`, r.ClusterID) })
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				call := calls.Add(1)
				if f.c.controller.store.db.Stats().InUse != 0 {
					t.Error("database connection retained during native health GET")
				}
				if request.Method != http.MethodGet {
					t.Error("native observation attempted mutation")
				}
				if call == 4 && strings.HasSuffix(mode, " after") {
					changeAuthority()
				}
				object := namespace
				if request.URL.Path == "/api/v1/nodes/"+b.NodeName {
					object = node
				} else if request.URL.Path != "/api/v1/namespaces/kube-system" {
					t.Error("unexpected native endpoint", request.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(object)
			}))
			defer server.Close()
			runner := &kubernetesClusterStepRunner{controllerHolder: r.Controller, kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}}
			f.c.controller.runner = runner
			if mode == "logical cluster mismatch" {
				b.ClusterID = newClusterUUID()
			}
			if mode == "runner mismatch" {
				runner.controllerHolder = "other"
			}
			target := clusterControllerNode{ID: r.NodeID, Name: r.NodeName, ApplicationState: "active"}
			if mode == "target mismatch" {
				target.ID = newClusterUUID()
			}
			observer := f.c.nodeHealthObserver(b)
			if mode != "partial health" {
				health, err := observer(f.ctx, target)
				if err == nil || health != (clusterAutomaticRecoveryHealth{}) {
					t.Fatal("changed authority retained health", health, err)
				}
				if !strings.HasSuffix(mode, " after") && calls.Load() != 0 {
					t.Fatal("unowned native health reached Kubernetes")
				}
				return
			}
			seed, err := f.c.Observe(f.ctx, r.OperationID, f.observer(t, true))
			if err != nil || seed.HealthySince == 0 {
				t.Fatal("healthy fixture interval missing", seed, err)
			}
			next, err := f.c.Observe(f.ctx, r.OperationID, observer)
			if err != nil || next.Phase != "recovering" || next.HealthySince != 0 || next.RecoveredAt != 0 ||
				next.RecoveryDeadline != r.RecoveryDeadline || next.NodeUID != r.NodeUID || calls.Load() != 4 {
				t.Fatal("partial native proof advanced automatic recovery", next, err)
			}
			f.assertInert(t, r.OperationID)
		})
	}
}
