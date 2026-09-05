package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClusterHMREntryCannotReachStoreOrRuntime(t *testing.T) {
	// Nil DB/Kubernetes handles make any downstream access a test failure.
	store := &postgresOperatorStore{}
	if _, err := store.createClusterOperation(context.Background(), "operator", clusterMutation{Kind: "hmr_start"}); !errors.Is(err, errClusterHMREntryDisabled) {
		t.Fatalf("entry reached DB: %v", err)
	}
	runner := &kubernetesClusterStepRunner{}
	for _, step := range []string{"preflight", "pre_change_snapshot", "hmr_move_roles", "hmr_drain_standby", "hmr_activate_target"} {
		if err := runner.Run(context.Background(), clusterControllerOperation{Kind: "hmr_start"}, clusterControllerStep{Name: step}, nil); !errors.Is(err, errClusterHMREntryDisabled) {
			t.Fatalf("legacy step %s reached runtime: %v", step, err)
		}
	}
}

type hmrGateRunner struct{ calls int }

func (r *hmrGateRunner) Run(context.Context, clusterControllerOperation, clusterControllerStep, []clusterControllerNode) error {
	r.calls++
	return errors.New("unexpected HMR entry runtime action")
}

func TestClusterHMREntryPostgresRejectsLegacyQueuePreservesRecovery(t *testing.T) {
	store, ctx, _, _ := clusterAdmissionFixture(t)
	var nodeID string
	if err := store.db.QueryRowContext(ctx, `SELECT id FROM engine.cluster_nodes WHERE node_name='admission-test-01'`).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	runner := &hmrGateRunner{}
	holder := "hmr-gate-" + newClusterUUID()
	controller := &clusterController{store: store, holder: holder, now: time.Now, runner: runner}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_application_leases WHERE holder=$1`, holder)
	})
	for _, size := range []int{1, 3} {
		id := newClusterUUID()
		now := time.Now().Unix()
		if _, err := store.db.ExecContext(ctx, `INSERT INTO engine.cluster_operations(id,kind,state,current_step,target_node_id,requested_by,payload_json,created_at,updated_at) VALUES($1,'hmr_start','queued','hmr_drain_standby',$2,'admission-test','{}',$3,$3)`, id, nodeID, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_state SET active_size=$1,desired_size=$1,status='Healthy',hmr_state='activating',hmr_node_id=$2,active_operation_id=$3 WHERE id=1`, size, nodeID, id); err != nil {
			t.Fatal(err)
		}
		if err := controller.runOnce(ctx); !errors.Is(err, errClusterHMREntryDisabled) {
			t.Fatalf("legacy %d-node entry not fenced: %v", size, err)
		}
		var state, hmrState, target string
		if err := store.db.QueryRowContext(ctx, `SELECT o.state,c.hmr_state,c.hmr_node_id FROM engine.cluster_operations o JOIN engine.cluster_state c ON c.id=1 WHERE o.id=$1`, id).Scan(&state, &hmrState, &target); err != nil {
			t.Fatal(err)
		}
		if state != "failed" || hmrState != "restore_failed" || target != nodeID || runner.calls != 0 {
			t.Fatalf("entry lost recovery state: %s %s %s calls=%d", state, hmrState, target, runner.calls)
		}
		if _, err := store.retryClusterOperation(ctx, "admission-test", id); !errors.Is(err, errClusterHMREntryDisabled) {
			t.Fatalf("legacy entry retried: %v", err)
		}
		result, err := store.createClusterOperation(ctx, "admission-test", clusterMutation{Kind: "hmr_exit", Payload: map[string]any{}})
		if err != nil {
			t.Fatalf("failed-entry recovery blocked: %v", err)
		}
		exitID := cleanText(result["operation_id"])
		if cleanText(result["target_node_id"]) != nodeID || cleanText(result["target_release"]) != "2026.9.5.1" {
			t.Fatalf("recovery lost pinned target: %+v", result)
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='failed',current_step='preflight' WHERE id=$1`, exitID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.retryClusterOperation(ctx, "admission-test", exitID); err != nil {
			t.Fatalf("existing exit retry blocked: %v", err)
		}
		if _, err := store.cancelClusterOperation(ctx, "admission-test", exitID); err != nil {
			t.Fatal(err)
		}
	}
}
