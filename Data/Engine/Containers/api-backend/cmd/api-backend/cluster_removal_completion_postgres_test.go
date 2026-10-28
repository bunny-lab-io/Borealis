package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestClusterRemovalCompletionPostgresRetainsValidatedCohort(t *testing.T) {
	for _, mode := range []string{"legacy pair", "explicit pair", "null cohort", "empty cohort", "duplicate target", "missing target", "partial pair", "wrong type"} {
		t.Run(mode, func(t *testing.T) {
			store, ctx, _, _ := clusterAdmissionFixture(t)
			store.db.SetMaxOpenConns(1)
			t.Cleanup(seedAdmissionPeer(t, store, ctx, "admission-test-02", "192.168.90.11"))
			t.Cleanup(seedAdmissionPeer(t, store, ctx, "admission-test-03", "192.168.90.12"))
			controller := &clusterController{store: store, holder: "removal-completion-" + newClusterUUID(), now: time.Now}
			nodes, err := controller.activeNodes(ctx)
			if err != nil || len(nodes) != 3 {
				t.Fatal("fixture requires exact original three nodes", err, len(nodes))
			}
			operation, _, fence := removalFenceFixture()
			operation.ID, operation.TargetNodeID, operation.CurrentStep, operation.Attempt = newClusterUUID(), nodes[1].ID, "verify_quorum", 2
			operation.Payload = map[string]any{"action_image": fence.ActionImage, "target_size": 1, "paired_node_id": nodes[2].ID}
			now := time.Now().Unix()
			for _, node := range nodes[1:] {
				fence.OperationID, fence.NodeID, fence.NodeName = operation.ID, node.ID, node.Name
				fence.NodeUID, fence.EtcdMemberName, fence.AcknowledgedAt = newClusterUUID(), node.Name+"-member", now-1
				setRemovalFenceRecord(operation, fence)
			}
			success := mode == "legacy pair" || mode == "explicit pair"
			switch mode {
			case "explicit pair":
				operation.Payload["removal_node_ids"] = []any{nodes[2].ID, nodes[1].ID}
			case "null cohort":
				operation.Payload["removal_node_ids"] = nil
			case "empty cohort":
				operation.Payload["removal_node_ids"] = []any{}
			case "duplicate target":
				operation.Payload["removal_node_ids"] = []any{nodes[1].ID, nodes[1].ID}
			case "missing target":
				operation.Payload["removal_node_ids"] = []any{nodes[1].ID, newClusterUUID()}
			case "partial pair":
				operation.Payload["removal_node_ids"] = []any{nodes[1].ID}
			case "wrong type":
				operation.Payload["removal_node_ids"] = nodes[1].ID
			}
			payload := marshalClusterJSON(operation.Payload)
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := store.db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = store.db.ExecContext(cleanup, `DELETE FROM engine.realtime_outbox WHERE payload_json LIKE '%' || $1 || '%'`, operation.ID)
				_, _ = store.db.ExecContext(cleanup, `DELETE FROM engine.cluster_operation_events WHERE operation_id=$1`, operation.ID)
				_, _ = store.db.ExecContext(cleanup, `DELETE FROM engine.cluster_operations WHERE id=$1`, operation.ID)
				_, _ = store.db.ExecContext(cleanup, `DELETE FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2`, clusterControllerLeaseName, controller.holder)
			})
			exec(`UPDATE engine.cluster_state SET active_size=3,desired_size=3,active_operation_id=$1 WHERE id=1`, operation.ID)
			exec(`INSERT INTO engine.cluster_operations(id,kind,state,current_step,target_node_id,requested_by,payload_json,attempt,created_at,updated_at)
 VALUES($1,'node_remove','running','verify_quorum',$2,$3,$4,2,$5,$5)`, operation.ID, operation.TargetNodeID, controller.holder, payload, now)
			exec(`INSERT INTO engine.cluster_application_leases(name,holder,expires_at,updated_at) VALUES($1,$2,$3,$4)
 ON CONFLICT(name) DO UPDATE SET holder=EXCLUDED.holder,expires_at=EXCLUDED.expires_at,updated_at=EXCLUDED.updated_at`, clusterControllerLeaseName, controller.holder, now+60, now)
			// Exercise the existing exact acknowledgement/absence prerequisite
			// before settlement. No connection remains held during native reads.
			observed := map[string]bool{}
			var observedMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if store.db.Stats().InUse != 0 {
					t.Error("completion retained SQL connection during absence proof")
				}
				for _, node := range nodes[1:] {
					if request.Method == http.MethodGet && request.URL.Path == "/api/v1/nodes/"+node.Name {
						observedMu.Lock()
						observed[node.ID] = true
						observedMu.Unlock()
						http.NotFound(w, request)
						return
					}
				}
				t.Error("final absence proof targeted unrelated member or mutation", request.Method, request.URL.Path)
				http.Error(w, "unexpected request", http.StatusBadRequest)
			}))
			defer server.Close()
			runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}}
			if err := runner.requireRemovalCohortAbsent(ctx, operation, nodes); (err == nil) != success {
				t.Fatal("incorrect native cohort validation", err)
			}
			observedMu.Lock()
			observedCount := len(observed)
			observedMu.Unlock()
			if success && observedCount != 2 {
				t.Fatal("whole original pair was not independently observed")
			}
			if err := controller.completeOperation(ctx, operation, nodes); (err == nil) != success {
				t.Fatal("incorrect completion outcome", err)
			}
			if store.db.Stats().InUse != 0 {
				t.Fatal("completion retained SQL connection")
			}
			for i, node := range nodes {
				var id, membership, application string
				if err := store.db.QueryRowContext(ctx, `SELECT id,membership_state,application_state FROM engine.cluster_nodes WHERE node_name=$1`, node.Name).Scan(&id, &membership, &application); err != nil {
					t.Fatal(err)
				}
				wantMembership, wantApplication := "Active", "active"
				if success && i != 0 {
					wantMembership, wantApplication = "Removed", "drained"
				}
				if id != node.ID || membership != wantMembership || application != wantApplication {
					t.Fatalf("original node %s changed incorrectly: id=%s membership=%s application=%s", node.Name, id, membership, application)
				}
			}
			var active, desired, durableActive, events int
			var activeOperation, state, step, retainedPayload string
			if err := store.db.QueryRowContext(ctx, `SELECT active_size,desired_size,COALESCE(active_operation_id,''),
 (SELECT count(*) FROM engine.cluster_nodes WHERE membership_state='Active') FROM engine.cluster_state WHERE id=1`).Scan(&active, &desired, &activeOperation, &durableActive); err != nil {
				t.Fatal(err)
			}
			if err := store.db.QueryRowContext(ctx, `SELECT state,current_step,payload_json,
 (SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1) FROM engine.cluster_operations WHERE id=$1`, operation.ID).Scan(&state, &step, &retainedPayload, &events); err != nil {
				t.Fatal(err)
			}
			if retainedPayload != payload {
				t.Fatal("completion rewrote original cohort/fence evidence")
			}
			if success {
				if active != 1 || desired != 1 || durableActive != 1 || activeOperation != "" || state != "succeeded" || step != "complete" || events != 1 {
					t.Fatalf("successful removal retained inconsistent membership: %d/%d durable=%d active=%q state=%s step=%s events=%d", active, desired, durableActive, activeOperation, state, step, events)
				}
			} else if active != 3 || desired != 3 || durableActive != 3 || activeOperation != operation.ID || state != "running" || step != "verify_quorum" || events != 0 {
				t.Fatal("invalid cohort changed durable state instead of rolling back")
			}
		})
	}
}
