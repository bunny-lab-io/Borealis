package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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

type removalCompletionFixture struct {
	c         *clusterController
	ctx       context.Context
	operation clusterControllerOperation
	nodes     []clusterControllerNode
	now       int64
}

func newRemovalCompletionFixture(t *testing.T) removalCompletionFixture {
	t.Helper()
	store, ctx, _, _ := clusterAdmissionFixture(t)
	store.db.SetMaxOpenConns(1)
	t.Cleanup(seedAdmissionPeer(t, store, ctx, "admission-test-02", "192.168.90.11"))
	t.Cleanup(seedAdmissionPeer(t, store, ctx, "admission-test-03", "192.168.90.12"))
	c := &clusterController{store: store, holder: "removal-settlement-" + newClusterUUID(), now: time.Now}
	nodes, err := c.activeNodes(ctx)
	if err != nil || len(nodes) != 3 {
		t.Fatal("fixture requires exact original three nodes", err, len(nodes))
	}
	operation, _, fence := removalFenceFixture()
	operation.ID, operation.TargetNodeID, operation.CurrentStep, operation.Attempt = newClusterUUID(), nodes[1].ID, "verify_quorum", 2
	operation.Payload = map[string]any{"action_image": fence.ActionImage, "target_size": 1, "paired_node_id": nodes[2].ID, "removal_node_ids": []any{nodes[1].ID, nodes[2].ID}}
	now := time.Now().Unix()
	for _, node := range nodes[1:] {
		fence.OperationID, fence.NodeID, fence.NodeName = operation.ID, node.ID, node.Name
		fence.NodeUID, fence.EtcdMemberName, fence.AcknowledgedAt = newClusterUUID(), node.Name+"-member", now-1
		setRemovalFenceRecord(operation, fence)
	}
	f := removalCompletionFixture{c: c, ctx: ctx, operation: operation, nodes: nodes, now: now}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = store.db.ExecContext(cleanup, `DELETE FROM engine.realtime_outbox WHERE payload_json LIKE '%' || $1 || '%'`, operation.ID)
		_, _ = store.db.ExecContext(cleanup, `DELETE FROM engine.cluster_operation_events WHERE operation_id=$1`, operation.ID)
		_, _ = store.db.ExecContext(cleanup, `DELETE FROM engine.cluster_operations WHERE id=$1`, operation.ID)
		_, _ = store.db.ExecContext(cleanup, `DELETE FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2`, clusterControllerLeaseName, c.holder)
	})
	f.exec(t, `UPDATE engine.cluster_state SET active_size=3,desired_size=3,active_operation_id=$1 WHERE id=1`, operation.ID)
	f.exec(t, `INSERT INTO engine.cluster_operations(id,kind,state,current_step,target_node_id,requested_by,payload_json,attempt,created_at,updated_at)
 VALUES($1,'node_remove','running','verify_quorum',$2,$3,$4,2,$5,$5)`, operation.ID, operation.TargetNodeID, c.holder, marshalClusterJSON(operation.Payload), now)
	f.exec(t, `INSERT INTO engine.cluster_application_leases(name,holder,expires_at,updated_at) VALUES($1,$2,$3,$4)
 ON CONFLICT(name) DO UPDATE SET holder=EXCLUDED.holder,expires_at=EXCLUDED.expires_at,updated_at=EXCLUDED.updated_at`, clusterControllerLeaseName, c.holder, now+60, now)
	// Every drift occurs after independently reading both original names as
	// absent. Retained acknowledgements belong to attempt 1; retry is attempt 2.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if store.db.Stats().InUse != 0 {
			t.Error("SQL retained during native absence proof")
		}
		if request.Method != http.MethodGet || (request.URL.Path != "/api/v1/nodes/"+nodes[1].Name && request.URL.Path != "/api/v1/nodes/"+nodes[2].Name) {
			t.Error("unrelated native absence read", request.Method, request.URL.Path)
		}
		http.NotFound(w, request)
	}))
	runner := &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}}
	err = runner.requireRemovalCohortAbsent(ctx, operation, nodes)
	server.Close()
	if err != nil {
		t.Fatal("original native absence proof", err)
	}
	return f
}

func (f removalCompletionFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.c.store.db.ExecContext(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

// Compare all durable contents, including timestamps and outbox/events, after
// intentional drift. Rejection must preserve the new owner/state exactly.
func (f removalCompletionFixture) snapshot(t *testing.T) string {
	t.Helper()
	var snapshot string
	err := f.c.store.db.QueryRowContext(f.ctx, `SELECT jsonb_build_object(
 'cluster',(SELECT to_jsonb(s) FROM engine.cluster_state s WHERE id=1),
 'operation',(SELECT to_jsonb(o) FROM engine.cluster_operations o WHERE id=$1),
 'nodes',(SELECT jsonb_agg(to_jsonb(n) ORDER BY n.id) FROM engine.cluster_nodes n),
 'events',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM engine.cluster_operation_events e WHERE operation_id=$1),
 'outbox',(SELECT jsonb_agg(to_jsonb(b) ORDER BY b.id) FROM engine.realtime_outbox b WHERE payload_json LIKE '%' || $1 || '%'))::text`, f.operation.ID).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (f removalCompletionFixture) requireUnchanged(t *testing.T, before string) {
	t.Helper()
	if f.c.store.db.Stats().InUse != 0 {
		t.Fatal("settlement retained SQL connection")
	}
	if after := f.snapshot(t); after != before {
		t.Fatalf("rejected settlement changed durable contents\nbefore: %s\nafter: %s", before, after)
	}
}

func TestClusterRemovalCompletionPostgresRejectsDrift(t *testing.T) {
	for _, mode := range []string{"attempt", "state", "kind", "step", "target", "release", "sha", "payload", "fence", "cohort", "active pointer", "missing operation", "target name", "target removed", "survivor name", "survivor missing", "survivor replacement", "extra active", "active count", "target size"} {
		t.Run(mode, func(t *testing.T) {
			f := newRemovalCompletionFixture(t)
			switch mode {
			case "attempt":
				f.exec(t, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, f.operation.ID)
			case "state":
				f.exec(t, `UPDATE engine.cluster_operations SET state='failed' WHERE id=$1`, f.operation.ID)
			case "kind":
				f.exec(t, `UPDATE engine.cluster_operations SET kind='node_maintenance' WHERE id=$1`, f.operation.ID)
			case "step":
				f.exec(t, `UPDATE engine.cluster_operations SET current_step='preflight' WHERE id=$1`, f.operation.ID)
			case "target":
				f.exec(t, `UPDATE engine.cluster_operations SET target_node_id=$2 WHERE id=$1`, f.operation.ID, f.nodes[0].ID)
			case "release":
				f.exec(t, `UPDATE engine.cluster_operations SET target_release='changed' WHERE id=$1`, f.operation.ID)
			case "sha":
				f.exec(t, `UPDATE engine.cluster_operations SET target_sha='changed' WHERE id=$1`, f.operation.ID)
			case "payload":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=(payload_json::jsonb || '{"reason":"changed"}')::text WHERE id=$1`, f.operation.ID)
			case "fence":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=(payload_json::jsonb - 'removal_fences')::text WHERE id=$1`, f.operation.ID)
			case "cohort":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=(payload_json::jsonb - 'removal_node_ids')::text WHERE id=$1`, f.operation.ID)
			case "active pointer":
				f.exec(t, `UPDATE engine.cluster_state SET active_operation_id=$1 WHERE id=1`, newClusterUUID())
			case "missing operation":
				f.exec(t, `DELETE FROM engine.cluster_operations WHERE id=$1`, f.operation.ID)
			case "target name":
				f.exec(t, `UPDATE engine.cluster_nodes SET node_name=node_name || '-changed' WHERE id=$1`, f.nodes[1].ID)
			case "target removed":
				f.exec(t, `UPDATE engine.cluster_nodes SET membership_state='Removed' WHERE id=$1`, f.nodes[1].ID)
			case "survivor name":
				f.exec(t, `UPDATE engine.cluster_nodes SET node_name=node_name || '-changed' WHERE id=$1`, f.nodes[0].ID)
			case "survivor missing":
				f.exec(t, `DELETE FROM engine.cluster_nodes WHERE id=$1`, f.nodes[0].ID)
			case "survivor replacement":
				f.exec(t, `DELETE FROM engine.cluster_nodes WHERE id=$1`, f.nodes[0].ID)
				t.Cleanup(seedAdmissionPeer(t, f.c.store, f.ctx, f.nodes[0].Name, "192.168.90.10"))
			case "extra active":
				t.Cleanup(seedAdmissionPeer(t, f.c.store, f.ctx, "admission-test-04", "192.168.90.13"))
			case "active count":
				f.exec(t, `UPDATE engine.cluster_state SET active_size=2 WHERE id=1`)
			case "target size":
				// Even equal caller/durable payloads cannot change the arithmetic
				// bound when the operation reserved its original cohort.
				f.operation.Payload["target_size"] = 2
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.operation.ID, marshalClusterJSON(f.operation.Payload))
			}
			before := f.snapshot(t)
			if err := f.c.completeOperation(f.ctx, f.operation, f.nodes); err == nil {
				t.Fatal("stale removal settlement accepted")
			}
			f.requireUnchanged(t, before)
		})
	}
}

func TestClusterRemovalCompletionPostgresAllowsObservationChanges(t *testing.T) {
	for _, mode := range []string{"mutable health", "emergency"} {
		t.Run(mode, func(t *testing.T) {
			f := newRemovalCompletionFixture(t)
			if mode == "emergency" {
				f.operation.Payload["emergency"] = true
				f.operation.Payload["target_size"] = 2
				f.operation.Payload["removal_node_ids"] = []any{f.nodes[1].ID}
				delete(f.operation.Payload, "paired_node_id")
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.operation.ID, marshalClusterJSON(f.operation.Payload))
			} else {
				f.exec(t, `UPDATE engine.cluster_nodes SET roles_json='{"scheduler_leader":true}',probe_health_json='{"readiness":"unknown"}',application_state='drained' WHERE membership_state='Active'`)
			}
			if err := f.c.completeOperation(f.ctx, f.operation, f.nodes); err != nil {
				t.Fatal(err)
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("settlement retained SQL connection")
			}
			var active, desired, removed, events int
			var status, state, step string
			err := f.c.store.db.QueryRowContext(f.ctx, `SELECT s.active_size,s.desired_size,s.status,o.state,o.current_step,
 (SELECT count(*) FROM engine.cluster_nodes WHERE membership_state='Removed'),
 (SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1)
 FROM engine.cluster_state s JOIN engine.cluster_operations o ON o.id=$1 WHERE s.id=1 AND s.active_operation_id IS NULL`, f.operation.ID).Scan(&active, &desired, &status, &state, &step, &removed, &events)
			wantActive, wantDesired, wantRemoved, wantStatus := 1, 1, 2, "Healthy"
			if mode == "emergency" {
				wantActive, wantDesired, wantRemoved, wantStatus = 2, 3, 1, "Degraded Quorum"
			}
			if err != nil || active != wantActive || desired != wantDesired || removed != wantRemoved || status != wantStatus || state != "succeeded" || step != "complete" || events != 1 {
				t.Fatal("incorrect settled membership", active, desired, removed, status, state, step, events, err)
			}
		})
	}
}

func TestClusterRemovalCompletionPostgresLockWaitFence(t *testing.T) {
	for _, mode := range []string{"cluster expiry", "operation expiry", "member expiry", "member cancellation", "before commit expiry"} {
		t.Run(mode, func(t *testing.T) {
			f := newRemovalCompletionFixture(t)
			before := f.snapshot(t)
			var clock atomic.Int64
			clock.Store(f.now)
			f.c.now = func() time.Time { return time.Unix(clock.Load(), 0) }
			if mode == "before commit expiry" {
				calls := 0
				f.c.now = func() time.Time {
					calls++
					// Initial lease check, write timestamp, pre-write check,
					// then expiry at the final guard after all settlement SQL.
					if calls == 4 {
						return time.Unix(f.now+61, 0)
					}
					return time.Unix(f.now, 0)
				}
				if err := f.c.completeOperation(f.ctx, f.operation, f.nodes); !errors.Is(err, errClusterControllerLeaseLost) || calls != 4 {
					t.Fatal("late lease loss did not roll back settlement", calls, err)
				}
				f.requireUnchanged(t, before)
				return
			}
			f.c.store.db.SetMaxOpenConns(3)
			blocker, err := f.c.store.db.BeginTx(f.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			lockQuery, watch := `SELECT id FROM engine.cluster_nodes WHERE id=$1 FOR UPDATE`, "%SELECT id,node_name FROM engine.cluster_nodes%"
			var args []any = []any{f.nodes[2].ID}
			if mode == "cluster expiry" {
				lockQuery, watch, args = `SELECT id FROM engine.cluster_state WHERE id=1 FOR UPDATE`, "%SELECT status,active_size,desired_size,config_json FROM engine.cluster_state%", nil
			} else if mode == "operation expiry" {
				lockQuery, watch, args = `SELECT id FROM engine.cluster_operations WHERE id=$1 FOR UPDATE`, "%SELECT o.id FROM engine.cluster_operations%", []any{f.operation.ID}
			}
			if _, err := blocker.ExecContext(f.ctx, lockQuery, args...); err != nil {
				t.Fatal(err)
			}
			workerCtx, cancel := context.WithCancel(f.ctx)
			done := make(chan error, 1)
			go func() { done <- f.c.completeOperation(workerCtx, f.operation, f.nodes) }()
			joined := false
			defer func() {
				cancel()
				_ = blocker.Rollback()
				if !joined {
					<-done
				}
			}()
			waitCtx, stopWait := context.WithTimeout(f.ctx, time.Second)
			defer stopWait()
			for {
				var waiting bool
				if err := f.c.store.db.QueryRowContext(waitCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, watch).Scan(&waiting); err != nil {
					t.Fatal("settlement did not reach expected PostgreSQL lock wait", err)
				}
				if waiting {
					break
				}
			}
			if mode == "member cancellation" {
				cancel()
			} else {
				clock.Store(f.now + 61)
			}
			if err := blocker.Rollback(); err != nil {
				t.Fatal(err)
			}
			err = <-done
			joined = true
			if mode == "member cancellation" {
				if err == nil || workerCtx.Err() == nil {
					t.Fatal("cancelled lock waiter settled removal", err)
				}
			} else if !errors.Is(err, errClusterControllerLeaseLost) {
				t.Fatal("expired lock waiter settled removal", err)
			}
			f.requireUnchanged(t, before)
		})
	}
}
