package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type automaticRecoveryPostgresFixture struct {
	c      *clusterAutomaticRecoveryCoordinator
	ctx    context.Context
	nodeID string
	health clusterAutomaticRecoveryHealth
}

func newAutomaticRecoveryPostgresFixture(t *testing.T) automaticRecoveryPostgresFixture {
	t.Helper()
	store, ctx, _, _ := clusterAdmissionFixture(t)
	store.db.SetMaxOpenConns(1)
	t.Cleanup(seedAdmissionPeer(t, store, ctx, "admission-test-02", "192.168.90.11"))
	t.Cleanup(seedAdmissionPeer(t, store, ctx, "admission-test-03", "192.168.90.12"))
	f := automaticRecoveryPostgresFixture{ctx: ctx, c: newClusterAutomaticRecoveryCoordinator(&clusterController{store: store, holder: "automatic-controller-" + newClusterUUID(), now: time.Now})}
	f.exec(t, `UPDATE engine.cluster_state SET active_size=3,desired_size=3 WHERE id=1`)
	if store.db.QueryRowContext(ctx, `SELECT id FROM engine.cluster_nodes WHERE node_name='admission-test-02'`).Scan(&f.nodeID) != nil {
		t.Fatal("target missing")
	}
	f.health = clusterAutomaticRecoveryHealth{NodeName: "admission-test-02", NodeUID: newClusterUUID(), Node: true, Application: true, Database: true, Storage: true, Roles: true, AgentPaths: true}
	f.exec(t, `INSERT INTO engine.cluster_application_leases(name,holder,expires_at,updated_at) VALUES($1,$2,$3,$4)`, clusterControllerLeaseName, f.c.controller.holder, time.Now().Unix()+300, time.Now().Unix())
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_application_leases WHERE name=$1`, clusterControllerLeaseName)
	})
	return f
}

func (f automaticRecoveryPostgresFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.c.controller.store.db.ExecContext(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f automaticRecoveryPostgresFixture) observer(t *testing.T, healthy bool) clusterAutomaticRecoveryObserve {
	t.Helper()
	return func(ctx context.Context, node clusterControllerNode) (clusterAutomaticRecoveryHealth, error) {
		if f.c.controller.store.db.Stats().InUse != 0 {
			t.Error("database connection retained across health observation")
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > clusterControllerLeaseAcquireTimeout {
			t.Error("observation deadline missing")
		}
		if node.ID != f.nodeID || node.Name != f.health.NodeName {
			t.Error("observer target changed")
		}
		health := f.health
		health.Application = healthy
		return health, nil
	}
}

func (f automaticRecoveryPostgresFixture) reserve(t *testing.T) clusterAutomaticRecoveryRecord {
	t.Helper()
	r, err := f.c.Reserve(f.ctx, f.nodeID, f.observer(t, false))
	if err != nil {
		t.Fatal("reserve", err)
	}
	return r
}

func (f automaticRecoveryPostgresFixture) writeRecord(t *testing.T, r clusterAutomaticRecoveryRecord) {
	t.Helper()
	f.exec(t, `UPDATE engine.cluster_operations SET payload_json=jsonb_set(payload_json::jsonb,ARRAY[$2],$3::jsonb)::text WHERE id=$1`, r.OperationID, clusterAutomaticRecoveryField, marshalClusterJSON(r))
}

func (f automaticRecoveryPostgresFixture) assertInert(t *testing.T, operation string) {
	t.Helper()
	var active, application, reason string
	if f.c.controller.store.db.QueryRowContext(f.ctx, `SELECT COALESCE(c.active_operation_id,''),n.application_state,COALESCE(n.drain_reason,'')
 FROM engine.cluster_state c,engine.cluster_nodes n WHERE c.id=1 AND n.id=$1`, f.nodeID).Scan(&active, &application, &reason) != nil || active != "" || application != "active" || reason != "" {
		t.Fatalf("observation changed runtime authority or drain: %q %q %q", active, application, reason)
	}
	if _, claimed, err := f.c.controller.claimOperation(f.ctx); err != nil || claimed {
		t.Fatal("generic controller claimed observation", claimed, err)
	}
	var payload string
	if f.c.controller.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, operation).Scan(&payload) != nil || strings.Contains(payload, "private failure") {
		t.Fatal("payload missing or private observation error leaked")
	}
}

func TestClusterAutomaticRecoveryPostgresReservationAndGenericFences(t *testing.T) {
	f := newAutomaticRecoveryPostgresFixture(t)
	if _, err := f.c.Reserve(f.ctx, f.nodeID, f.observer(t, true)); err == nil {
		t.Fatal("healthy node gained automatic recovery intent")
	}
	r := f.reserve(t)
	if !r.valid() || r.Attempt != 1 || r.Controller != f.c.controller.holder || r.NodeUID != f.health.NodeUID {
		t.Fatal("reservation lost exact origin", r)
	}
	f.assertInert(t, r.OperationID)
	if _, err := f.c.Reserve(f.ctx, f.nodeID, f.observer(t, false)); err == nil {
		t.Fatal("duplicate automatic recovery reserved")
	}
	if _, err := f.c.controller.store.retryClusterOperation(f.ctx, "admission-test", r.OperationID); err == nil {
		t.Fatal("generic retry activated automatic observation")
	}
	if _, err := f.c.controller.store.cancelClusterOperation(f.ctx, "admission-test", r.OperationID); err != nil {
		t.Fatal("untouched observation could not be cancelled", err)
	}
	f.assertInert(t, r.OperationID)
	if _, err := f.c.Observe(f.ctx, r.OperationID, f.observer(t, true)); err == nil {
		t.Fatal("cancelled observation resumed")
	}
}

func TestClusterAutomaticRecoveryPostgresObservationAndRestart(t *testing.T) {
	f := newAutomaticRecoveryPostgresFixture(t)
	r := f.reserve(t)
	// Both canonical Python INTEGER columns and Go-created BIGINT columns must
	// accept the same observation update, including its nullable terminal time.
	assertTimes := func(record clusterAutomaticRecoveryRecord, terminal bool) {
		t.Helper()
		var updated int64
		var finished sql.NullInt64
		if err := f.c.controller.store.db.QueryRowContext(f.ctx, `SELECT updated_at,finished_at FROM engine.cluster_operations WHERE id=$1`, record.OperationID).Scan(&updated, &finished); err != nil {
			t.Fatal(err)
		}
		if updated != record.LastObservedAt || finished.Valid != terminal || (terminal && finished.Int64 != record.LastObservedAt) {
			t.Fatalf("observation timestamps changed: updated=%d finished=%v record=%d terminal=%v", updated, finished, record.LastObservedAt, terminal)
		}
	}
	r, err := f.c.Observe(f.ctx, r.OperationID, f.observer(t, true))
	if err != nil || r.Phase != "soaking" {
		t.Fatal("healthy observation", err, r)
	}
	assertTimes(r, false)
	// Retain a proven recent interval without sleeping five minutes in a fixture.
	now := time.Now().Unix()
	r.DetectedAt, r.RecoveryDeadline, r.HealthySince, r.RecoveredAt, r.LastObservedAt = now-300, now, now-300, now-300, now
	f.writeRecord(t, r)
	r, err = f.c.Observe(f.ctx, r.OperationID, f.observer(t, true))
	if err != nil || r.Phase != "health_observed" {
		t.Fatal("continuous interval did not finish", err, r)
	}
	f.assertInert(t, r.OperationID)
	// Renew the fixture's initial observation timeline for independent restart proof.
	now = time.Now().Unix()
	r.DetectedAt, r.RecoveryDeadline, r.HealthySince, r.RecoveredAt, r.LastObservedAt = now-30, now+270, now-20, now-20, now
	r.Phase = "soaking"
	f.writeRecord(t, r)
	restarted := newClusterAutomaticRecoveryCoordinator(f.c.controller)
	next, err := restarted.Observe(f.ctx, r.OperationID, f.observer(t, true))
	if err != nil || next.Attempt != r.Attempt+1 || next.HealthySince < now || next.Session == r.Session || next.OriginController != r.OriginController {
		t.Fatal("restart retained old healthy interval/generation", err, next)
	}
	newHolder := "automatic-replacement-" + newClusterUUID()
	f.exec(t, `UPDATE engine.cluster_application_leases SET holder=$2 WHERE name=$1`, clusterControllerLeaseName, newHolder)
	if _, err := restarted.Observe(f.ctx, r.OperationID, f.observer(t, true)); err == nil {
		t.Fatal("former holder retained observation authority")
	}
	replacement := newClusterAutomaticRecoveryCoordinator(&clusterController{store: f.c.controller.store, holder: newHolder, now: time.Now})
	replaced, err := replacement.Observe(f.ctx, r.OperationID, f.observer(t, true))
	if err != nil || replaced.Attempt != next.Attempt+1 || replaced.OriginController != r.OriginController || replaced.Controller != newHolder {
		t.Fatal("new ownership lost original identity or reset", err, replaced)
	}
	unknown := func(context.Context, clusterControllerNode) (clusterAutomaticRecoveryHealth, error) {
		return f.health, errors.New("private failure")
	}
	reset, err := replacement.Observe(f.ctx, r.OperationID, unknown)
	if err != nil || reset.HealthySince != 0 || reset.Phase != "recovering" || reset.Reason != "observation_unknown" {
		t.Fatal("unknown observation retained healthy time", err, reset)
	}
	reset.DetectedAt, reset.RecoveryDeadline, reset.LastObservedAt = now-301, now-1, now
	f.writeRecord(t, reset)
	blocked, err := replacement.Observe(f.ctx, r.OperationID, f.observer(t, true))
	if err != nil || blocked.Phase != "blocked" || blocked.Reason != "recovery_deadline" {
		t.Fatal("expired recovery restarted budget", err, blocked)
	}
	assertTimes(blocked, true)
	if _, err := f.c.controller.store.retryClusterOperation(f.ctx, "admission-test", r.OperationID); err == nil {
		t.Fatal("failed observation became executable retry")
	}
	if _, err := replacement.Reserve(f.ctx, f.nodeID, f.observer(t, false)); err == nil {
		t.Fatal("new reservation bypassed unresolved recovery deadline")
	}
}

func TestClusterAutomaticRecoveryPostgresAuthorityAndDrift(t *testing.T) {
	for _, mode := range []string{"holder", "expiry", "operator_drain", "membership", "active_operation", "attempt", "payload", "cancel", "node_name"} {
		t.Run(mode, func(t *testing.T) {
			f := newAutomaticRecoveryPostgresFixture(t)
			r := f.reserve(t)
			previousSession := f.c.session
			observe := func(ctx context.Context, node clusterControllerNode) (clusterAutomaticRecoveryHealth, error) {
				health, err := f.observer(t, true)(ctx, node)
				switch mode {
				case "holder":
					f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`, clusterControllerLeaseName)
				case "expiry":
					f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
				case "operator_drain":
					f.exec(t, `UPDATE engine.cluster_nodes SET application_state='drained',drain_reason='operator maintenance' WHERE id=$1`, f.nodeID)
				case "membership":
					f.exec(t, `UPDATE engine.cluster_nodes SET membership_state='Removed' WHERE id=$1`, f.nodeID)
				case "active_operation":
					f.exec(t, `UPDATE engine.cluster_state SET active_operation_id=$1 WHERE id=1`, newClusterUUID())
				case "attempt":
					f.exec(t, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, r.OperationID)
				case "payload":
					f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, r.OperationID)
				case "cancel":
					f.exec(t, `UPDATE engine.cluster_operations SET state='cancelled' WHERE id=$1`, r.OperationID)
				case "node_name":
					f.exec(t, `UPDATE engine.cluster_nodes SET node_name='replacement' WHERE id=$1`, f.nodeID)
				}
				return health, err
			}
			if _, err := f.c.Observe(f.ctx, r.OperationID, observe); err == nil || f.c.session == previousSession {
				t.Fatal("drift retained authority or observation continuity")
			}
			var events int
			if f.c.controller.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1`, r.OperationID).Scan(&events) != nil || events != 1 {
				t.Fatal("failed observation committed event")
			}
			if mode == "operator_drain" {
				var state, reason string
				if f.c.controller.store.db.QueryRowContext(f.ctx, `SELECT application_state,drain_reason FROM engine.cluster_nodes WHERE id=$1`, f.nodeID).Scan(&state, &reason) != nil || state != "drained" || reason != "operator maintenance" {
					t.Fatal("automatic record cleared operator maintenance")
				}
			}
		})
	}
}

func TestClusterAutomaticRecoveryPostgresWaitFenceAndContenders(t *testing.T) {
	t.Run("one reservation", func(t *testing.T) {
		f := newAutomaticRecoveryPostgresFixture(t)
		f.c.controller.store.db.SetMaxOpenConns(4)
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				coordinator := newClusterAutomaticRecoveryCoordinator(f.c.controller)
				_, err := coordinator.Reserve(f.ctx, f.nodeID, func(context.Context, clusterControllerNode) (clusterAutomaticRecoveryHealth, error) {
					health := f.health
					health.Application = false
					return health, nil
				})
				results <- err
			}()
		}
		close(start)
		workers.Wait()
		close(results)
		wins := 0
		for err := range results {
			if err == nil {
				wins++
			}
		}
		if wins != 1 {
			t.Fatal("reservation did not have one winner", wins)
		}
	})
	for _, mode := range []string{"lease_expiry", "operator_drain", "payload"} {
		t.Run(mode, func(t *testing.T) {
			f := newAutomaticRecoveryPostgresFixture(t)
			r := f.reserve(t)
			f.c.controller.store.db.SetMaxOpenConns(4)
			tx, err := f.c.controller.store.db.BeginTx(f.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(f.ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := f.c.Observe(f.ctx, r.OperationID, func(context.Context, clusterControllerNode) (clusterAutomaticRecoveryHealth, error) {
					return f.health, nil
				})
				done <- err
			}()
			joined := false
			defer func() {
				tx.Rollback()
				if !joined {
					<-done
				}
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if f.c.controller.store.db.QueryRowContext(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%engine.cluster_application_leases%')`).Scan(&waiting) != nil {
					t.Fatal("wait observation failed")
				}
				if waiting {
					break
				}
				select {
				case <-ticker.C:
				case err := <-done:
					joined = true
					t.Fatal("observation ended before lock wait", err)
				case <-f.ctx.Done():
					t.Fatal("observation never reached lock wait")
				}
			}
			query := `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`
			arg := clusterControllerLeaseName
			if mode == "operator_drain" {
				query, arg = `UPDATE engine.cluster_nodes SET application_state='drained',drain_reason='operator maintenance' WHERE id=$1`, f.nodeID
			} else if mode == "payload" {
				query, arg = `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, r.OperationID
			}
			if _, err := tx.ExecContext(f.ctx, query, arg); err != nil || tx.Commit() != nil {
				t.Fatal("drift commit failed", err)
			}
			if err := <-done; err == nil {
				t.Fatal("wait fence accepted changed authority")
			}
			joined = true
		})
	}
}
