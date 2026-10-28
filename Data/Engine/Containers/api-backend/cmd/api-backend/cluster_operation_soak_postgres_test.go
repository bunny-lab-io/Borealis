package main

import (
	"context"
	"testing"
	"time"
)

func soakPostgresFixture(t *testing.T) (*clusterController, context.Context, clusterOperationSoak) {
	store, ctx, _, _ := clusterAdmissionFixture(t)
	store.db.SetMaxOpenConns(1)
	c := &clusterController{store: store, holder: "soak-controller-" + newClusterUUID(), now: time.Now}
	now := time.Now()
	run := clusterOperationSoak{OperationID: newClusterUUID(), Attempt: 1, Step: "node:test:minimum_ready_soak", RunID: newClusterUUID(), Holder: c.holder, Phase: "ready", StartedAtMS: now.UnixMilli(), DeadlineAtMS: now.Add(30 * time.Second).UnixMilli()}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO engine.cluster_operations(id,kind,state,current_step,payload_json,attempt,requested_by,created_at,updated_at) VALUES($1,'node_maintenance','running',$2,'{}',1,'admission-test',$3,$3)`, []any{run.OperationID, run.Step, now.Unix()}},
		{`UPDATE engine.cluster_state SET active_operation_id=$1 WHERE id=1`, []any{run.OperationID}},
		{`INSERT INTO engine.cluster_application_leases(name,holder,expires_at,updated_at) VALUES($1,$2,$3,$4)`, []any{clusterControllerLeaseName, c.holder, now.Unix() + 300, now.Unix()}},
	} {
		if _, err := store.db.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_application_leases WHERE name=$1`, clusterControllerLeaseName)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, run.OperationID)
	})
	return c, ctx, run
}

func TestClusterOperationSoakPostgresExactFences(t *testing.T) {
	for _, field := range []string{"lease", "expired", "active", "attempt", "step", "state", "cancelled", "clock"} {
		t.Run(field, func(t *testing.T) {
			c, ctx, run := soakPostgresFixture(t)
			if err := c.recordOperationSoak(ctx, run, "operation_soak_started"); err != nil {
				t.Fatal(err)
			}
			var query string
			switch field {
			case "lease":
				query = `UPDATE engine.cluster_application_leases SET holder='another-controller'`
			case "expired":
				query = `UPDATE engine.cluster_application_leases SET expires_at=0`
			case "active":
				query = `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1`
			case "attempt":
				query = `UPDATE engine.cluster_operations SET attempt=2 WHERE id=$1`
			case "step":
				query = `UPDATE engine.cluster_operations SET current_step='exit_drain' WHERE id=$1`
			case "state":
				query = `UPDATE engine.cluster_operations SET state='failed' WHERE id=$1`
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "clock":
				c.now = func() time.Time { return time.UnixMilli(run.StartedAtMS - 1) }
			}
			if query != "" {
				var args []any
				if field == "attempt" || field == "step" || field == "state" {
					args = []any{run.OperationID}
				}
				if _, err := c.store.db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.recordOperationSoak(ctx, run, "operation_soak_observed"); err == nil {
				t.Fatal("stale observation accepted")
			}
			var count int
			if err := c.store.db.QueryRowContext(context.Background(), `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1`, run.OperationID).Scan(&count); err != nil || count != 1 {
				t.Fatal("failed fence wrote event", count, err)
			}
		})
	}
}

func TestClusterOperationSoakPostgresRestartAndElapsed(t *testing.T) {
	c, ctx, run := soakPostgresFixture(t)
	write := func(r clusterOperationSoak, event string) {
		t.Helper()
		if err := c.recordOperationSoak(ctx, r, event); err != nil {
			t.Fatal(event, err)
		}
		if c.store.db.Stats().InUse != 0 {
			t.Fatal("event transaction retained connection")
		}
	}
	write(run, "operation_soak_started")
	write(run, "operation_soak_observed")
	restarted := run
	restarted.RunID = newClusterUUID()
	restarted.StartedAtMS = time.Now().UnixMilli()
	restarted.DeadlineAtMS = restarted.StartedAtMS + 30_000
	write(restarted, "operation_soak_started")
	lateStart := run
	lateStart.RunID = newClusterUUID()
	lateStart.StartedAtMS = run.StartedAtMS - 1
	if err := c.recordOperationSoak(ctx, lateStart, "operation_soak_started"); err == nil {
		t.Fatal("late initial write replaced newer timer")
	}
	if err := c.recordOperationSoak(ctx, run, "operation_soak_elapsed"); err == nil {
		t.Fatal("old run completed replacement")
	}
	if err := c.recordOperationSoak(ctx, restarted, "operation_soak_elapsed"); err == nil {
		t.Fatal("timer completed before its recorded deadline")
	}
	c.now = func() time.Time { return time.UnixMilli(restarted.DeadlineAtMS) }
	write(restarted, "operation_soak_elapsed")
	if err := c.recordOperationSoak(ctx, restarted, "operation_soak_observed"); err == nil {
		t.Fatal("elapsed run revived")
	}
	rows, err := c.store.db.QueryContext(ctx, `SELECT event_type,details_json FROM engine.cluster_operation_events WHERE operation_id=$1 ORDER BY id`, run.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for rows.Next() {
		var event, details string
		if err := rows.Scan(&event, &details); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		types = append(types, event)
		detail := parseClusterJSON(details)
		if detail["step"] != run.Step || detail["holder"] != c.holder {
			rows.Close()
			t.Fatal(event, details)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"operation_soak_started", "operation_soak_observed", "operation_soak_interrupted", "operation_soak_started", "operation_soak_elapsed"}
	if len(types) != len(want) {
		t.Fatal(types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatal(types)
		}
	}
	var state, step string
	if err := c.store.db.QueryRowContext(ctx, `SELECT state,current_step FROM engine.cluster_operations WHERE id=$1`, run.OperationID).Scan(&state, &step); err != nil || state != "running" || step != run.Step {
		t.Fatal("elapsed event changed operation authority", state, step, err)
	}
}

func TestClusterOperationSoakPostgresLockWaitExpiry(t *testing.T) {
	c, ctx, run := soakPostgresFixture(t)
	c.store.db.SetMaxOpenConns(3)
	expires := time.Now().Unix() + 2
	if _, err := c.store.db.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET expires_at=$1`, expires); err != nil {
		t.Fatal(err)
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT id FROM engine.cluster_operations WHERE id=$1 FOR UPDATE`, run.OperationID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.recordOperationSoak(ctx, run, "operation_soak_started") }()
	// Prove the writer reached the operation lock before expiring its lease.
	blocked := false
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		if err := c.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT state,current_step,attempt FROM engine.cluster_operations%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("writer never waited on operation lock")
	}
	time.Sleep(time.Until(time.Unix(expires, 0).Add(30 * time.Millisecond)))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("waited past lease expiry and wrote timer")
	}
	var count int
	if err := c.store.db.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1`, run.OperationID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
