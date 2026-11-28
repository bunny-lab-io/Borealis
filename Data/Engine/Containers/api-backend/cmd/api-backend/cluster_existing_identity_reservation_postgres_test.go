package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type existingIdentityFixture struct {
	c                                      *clusterController
	ctx                                    context.Context
	maintenance, source, target, namespace string
	items                                  []any
}

func newExistingIdentityFixture(t *testing.T) existingIdentityFixture {
	t.Helper()
	s, ctx, _, _ := clusterAdmissionFixture(t)
	s.db.SetMaxOpenConns(1)
	t.Cleanup(seedAdmissionPeer(t, s, ctx, "admission-test-02", "192.168.90.11"))
	t.Cleanup(seedAdmissionPeer(t, s, ctx, "admission-test-03", "192.168.90.12"))
	f := existingIdentityFixture{c: &clusterController{store: s, holder: "original-controller-" + newClusterUUID(), now: time.Now}, ctx: ctx, maintenance: newClusterUUID(), namespace: newClusterUUID()}
	f.exec(t, `UPDATE engine.cluster_state SET active_size=3,desired_size=3,edge_vip=control_plane_vip WHERE id=1`)
	f.exec(t, `UPDATE engine.cluster_nodes SET release_tag='2026.9.5.1',release_sha=$1 WHERE membership_state='Active'`, strings.Repeat("a", 40))
	f.exec(t, `UPDATE engine.cluster_nodes SET application_state='drained',drain_reason='operator maintenance' WHERE node_name='admission-test-03'`)
	if s.db.QueryRowContext(ctx, `SELECT id FROM engine.cluster_nodes WHERE node_name='admission-test-01'`).Scan(&f.source) != nil ||
		s.db.QueryRowContext(ctx, `SELECT id FROM engine.cluster_nodes WHERE node_name='admission-test-03'`).Scan(&f.target) != nil {
		t.Fatal("fixture member identity")
	}
	now := time.Now().Unix()
	f.exec(t, `INSERT INTO engine.cluster_application_leases(name,holder,expires_at,updated_at) VALUES($1,$2,$3,$4)`, clusterControllerLeaseName, f.c.holder, now+300, now)
	f.exec(t, `INSERT INTO engine.cluster_operations(id,kind,state,current_step,target_node_id,requested_by,payload_json,created_at,started_at,finished_at,updated_at)
 VALUES($1,'node_maintenance','succeeded','complete',$2,'admission-test','{"action":"enter","reason":"operator maintenance"}',$3,$3,$4,$4)`, f.maintenance, f.target, now-20, now-10)
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, f.maintenance)
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_application_leases WHERE name=$1`, clusterControllerLeaseName)
	})
	snapshot, _, err := readClusterExistingIdentitySnapshot(ctx, s.db, f.c.holder, f.maintenance)
	if err != nil || !snapshot.valid(f.source) {
		t.Fatal("fixture reservation", err)
	}
	for i, m := range snapshot.Members {
		node := sshSourceKubernetesFixture(clusterSSHSourceMember{NodeID: m.ID, NodeUID: newClusterUUID(), Name: m.Name, Address: m.Address,
			MachineID: fmt.Sprintf("%032x", i+1), BootID: newClusterUUID()})
		node["metadata"].(map[string]any)["labels"] = map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": m.ApplicationState}
		f.items = append(f.items, node)
	}
	return f
}

func (f existingIdentityFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.c.store.db.ExecContext(f.ctx, q, args...); err != nil {
		t.Fatal(err)
	}
}
func (f existingIdentityFixture) read(t *testing.T) func(context.Context, string, any) error {
	return func(ctx context.Context, path string, out any) error {
		if f.c.store.db.Stats().InUse != 0 {
			t.Error("SQL connection retained during Kubernetes observation")
			return errClusterConflict
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 15*time.Second {
			t.Error("unbounded observation")
			return errClusterConflict
		}
		var value any
		switch path {
		case "/api/v1/namespaces/kube-system":
			value = map[string]any{"metadata": map[string]any{"uid": f.namespace}}
		case "/api/v1/nodes":
			value = map[string]any{"items": f.items}
		default:
			return errors.New("unexpected observation")
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, out)
	}
}
func (f existingIdentityFixture) assertNoReservation(t *testing.T, id string) {
	t.Helper()
	var count int
	var active string
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT (SELECT count(*) FROM engine.cluster_operations WHERE id=$1),COALESCE(active_operation_id,'') FROM engine.cluster_state WHERE id=1`, id).Scan(&count, &active) != nil || count != 0 {
		t.Fatal("failed reservation wrote operation")
	}
	var events int
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1`, id).Scan(&events) != nil || events != 0 {
		t.Fatal("failed reservation wrote event")
	}
}

func TestClusterExistingIdentityPostgresReservation(t *testing.T) {
	f := newExistingIdentityFixture(t)
	id := newClusterUUID()
	if owner, err := f.c.store.clusterExistingIdentityController(f.ctx); err != nil || owner != f.c.holder {
		t.Fatal("planning lost current controller", err)
	}
	plan, err := f.c.reserveClusterExistingIdentity(f.ctx, id, f.maintenance, f.source, f.target, "admin", f.read(t))
	if err != nil || plan.SourceID != f.source || plan.TargetID != f.target || plan.MaintenanceID != f.maintenance || plan.KubeSystemUID != f.namespace || len(plan.Members) != 3 {
		t.Fatal("reserve exact existing member", err)
	}
	var state, step, active, payload string
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT o.state,o.current_step,c.active_operation_id,o.payload_json FROM engine.cluster_operations o,engine.cluster_state c WHERE o.id=$1 AND c.id=1`, id).Scan(&state, &step, &active, &payload) != nil || state != "waiting" || step != clusterExistingIdentityReserved || active != id {
		t.Fatal("reservation lost inert boundary")
	}
	var stored clusterExistingIdentityPlan
	if json.Unmarshal([]byte(payload), &stored) != nil || !reflect.DeepEqual(plan, stored) {
		t.Fatal("plan not retained exactly")
	}
	readback, status, err := f.c.store.clusterExistingIdentityPlan(f.ctx, f.target, id)
	if err != nil || !reflect.DeepEqual(readback, plan) || status != "waiting" || f.c.store.db.Stats().InUse != 0 {
		t.Fatal("lost acknowledgement readback", err)
	}
	if _, _, err := f.c.store.clusterExistingIdentityPlan(f.ctx, f.source, id); err == nil {
		t.Fatal("readback accepted wrong target")
	}
	var targets int
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1`, id).Scan(&targets) != nil || targets != 0 {
		t.Fatal("existing member became onboarding target")
	}
	if _, claimed, err := f.c.claimOperation(f.ctx); err != nil || claimed {
		t.Fatal("generic controller claimed recovery reservation", err)
	}
	if pending, err := f.c.store.pendingClusterSSHIdentityRecoveries(f.ctx); err != nil || len(pending) != 0 {
		t.Fatal("reservation became executable recovery", err)
	}
	if _, err := f.c.reserveClusterExistingIdentity(f.ctx, id, f.maintenance, f.source, f.target, "admin", f.read(t)); err == nil {
		t.Fatal("reservation replay rewrote active operation")
	}
	if _, err := f.c.store.cancelClusterOperation(f.ctx, "admission-test", id); err != nil {
		t.Fatal("inert reservation cannot be cancelled", err)
	}
	if readback, status, err := f.c.store.clusterExistingIdentityPlan(f.ctx, f.target, id); err != nil || !reflect.DeepEqual(readback, plan) || status != "cancelled" {
		t.Fatal("cancelled plan lost historical readback", err)
	}
	var application, reason string
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT application_state,drain_reason FROM engine.cluster_nodes WHERE id=$1`, f.target).Scan(&application, &reason) != nil || application != "drained" || reason != "operator maintenance" {
		t.Fatal("cancellation cleared operator maintenance")
	}
	f.exec(t, `UPDATE engine.cluster_operations SET state='failed' WHERE id=$1`, id)
	if _, err := f.c.store.retryClusterOperation(f.ctx, "admission-test", id); !errors.Is(err, errClusterConflict) {
		t.Fatal("generic retry recreated recovery authority", err)
	}
	f.exec(t, `UPDATE engine.cluster_operations SET state='waiting',current_step='recover_identity' WHERE id=$1`, id)
	if _, err := f.c.store.cancelClusterOperation(f.ctx, "admission-test", id); !errors.Is(err, errClusterConflict) {
		t.Fatal("unknown recovery phase allowed cancellation", err)
	}
}

func TestClusterExistingIdentityPostgresRejectsDrift(t *testing.T) {
	for _, mode := range []string{"wrong target", "source target", "source missing", "bad actor", "maintenance exit", "maintenance failed", "newer maintenance", "second drain", "source release", "config drift", "controller drift", "target resumed", "namespace drift", "Node UID drift", "extra node", "missing node", "wrong label", "not ready", "duplicate machine", "duplicate boot", "target address", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := newExistingIdentityFixture(t)
			id, source, actor := newClusterUUID(), f.source, "admin"
			target := f.target
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			switch mode {
			case "wrong target":
				target = newClusterUUID()
			case "source target":
				source = f.target
			case "source missing":
				source = newClusterUUID()
			case "bad actor":
				actor = "admin\n"
			case "maintenance exit":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json='{"action":"exit"}' WHERE id=$1`, f.maintenance)
			case "maintenance failed":
				f.exec(t, `UPDATE engine.cluster_operations SET state='failed' WHERE id=$1`, f.maintenance)
			case "newer maintenance":
				other := newClusterUUID()
				f.exec(t, `INSERT INTO engine.cluster_operations(id,kind,state,current_step,target_node_id,requested_by,created_at,finished_at,updated_at) VALUES($1,'node_maintenance','succeeded','complete',$2,'admission-test',$3,$3,$3)`, other, f.target, time.Now().Unix())
				t.Cleanup(func() {
					_, _ = f.c.store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, other)
				})
			case "second drain":
				f.exec(t, `UPDATE engine.cluster_nodes SET application_state='drained' WHERE id=$1`, f.source)
			case "source release":
				f.exec(t, `UPDATE engine.cluster_nodes SET release_sha=$2 WHERE id=$1`, f.source, strings.Repeat("b", 40))
			case "extra node":
				f.items = append(f.items, f.items[0])
			case "missing node":
				f.items = f.items[:2]
			case "wrong label":
				f.items[0].(map[string]any)["metadata"].(map[string]any)["labels"] = map[string]any{}
			case "not ready":
				for _, item := range f.items {
					n := item.(map[string]any)
					if n["metadata"].(map[string]any)["name"] == "admission-test-01" {
						n["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
					}
				}
			case "duplicate machine", "duplicate boot":
				key := "machineID"
				if mode == "duplicate boot" {
					key = "bootID"
				}
				f.items[1].(map[string]any)["status"].(map[string]any)["nodeInfo"].(map[string]any)[key] = f.items[0].(map[string]any)["status"].(map[string]any)["nodeInfo"].(map[string]any)[key]
			case "target address":
				f.items[0].(map[string]any)["status"].(map[string]any)["addresses"] = []any{map[string]any{"type": "InternalIP", "address": "192.168.90.99"}}
			}
			base := f.read(t)
			calls := 0
			read := func(ctx context.Context, path string, out any) error {
				calls++
				if calls == 4 {
					switch mode {
					case "config drift":
						f.exec(t, `UPDATE engine.cluster_state SET config_json=config_json||' ' WHERE id=1`)
					case "controller drift":
						f.exec(t, `UPDATE engine.cluster_application_leases SET holder='replacement' WHERE name=$1`, clusterControllerLeaseName)
					case "target resumed":
						f.exec(t, `UPDATE engine.cluster_nodes SET application_state='active' WHERE id=$1`, f.target)
					case "namespace drift":
						return json.Unmarshal([]byte(`{"metadata":{"uid":"`+newClusterUUID()+`"}}`), out)
					case "Node UID drift":
						f.items[0].(map[string]any)["metadata"].(map[string]any)["uid"] = newClusterUUID()
					case "cancelled":
						cancel()
					}
				}
				return base(ctx, path, out)
			}
			if _, err := f.c.reserveClusterExistingIdentity(ctx, id, f.maintenance, source, target, actor, read); err == nil {
				t.Fatal("unsafe reservation accepted", mode)
			}
			f.assertNoReservation(t, id)
		})
	}
}

func TestClusterExistingIdentityPostgresConcurrentReservation(t *testing.T) {
	f := newExistingIdentityFixture(t)
	f.c.store.db.SetMaxOpenConns(4)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			calls := 0
			// Independent SQL readers can overlap; only network callbacks themselves
			// are connection-free. The one-connection fixture above proves lifecycle.
			read := func(ctx context.Context, path string, out any) error {
				calls++
				var value any
				if path == "/api/v1/nodes" {
					value = map[string]any{"items": f.items}
				} else {
					value = map[string]any{"metadata": map[string]any{"uid": f.namespace}}
				}
				if calls == 6 {
					arrived <- struct{}{}
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				raw, _ := json.Marshal(value)
				return json.Unmarshal(raw, out)
			}
			_, err := f.c.reserveClusterExistingIdentity(f.ctx, newClusterUUID(), f.maintenance, f.source, f.target, "admin", read)
			results <- err
		}()
	}
	for range 2 {
		select {
		case <-arrived:
		case <-f.ctx.Done():
			close(release)
			wg.Wait()
			t.Fatal("contenders did not reach observation barrier")
		}
	}
	close(release)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("reservation not exclusive", success)
	}
}

func TestClusterExistingIdentityPostgresLockWaitFence(t *testing.T) {
	for _, mode := range []string{"controller expired", "controller changed", "maintenance changed", "target resumed", "source address", "configuration", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := newExistingIdentityFixture(t)
			f.c.store.db.SetMaxOpenConns(4)
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			blocker, err := f.c.store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			if _, err := blocker.ExecContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName); err != nil {
				t.Fatal(err)
			}
			id := newClusterUUID()
			done := make(chan error, 1)
			joined := false
			defer func() {
				blocker.Rollback()
				cancel()
				if !joined {
					<-done
				}
			}()
			go func() {
				read := func(ctx context.Context, path string, out any) error {
					var value any
					if path == "/api/v1/nodes" {
						value = map[string]any{"items": f.items}
					} else {
						value = map[string]any{"metadata": map[string]any{"uid": f.namespace}}
					}
					raw, _ := json.Marshal(value)
					return json.Unmarshal(raw, out)
				}
				_, err := f.c.reserveClusterExistingIdentity(ctx, id, f.maintenance, f.source, f.target, "admin", read)
				done <- err
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if f.c.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%engine.cluster_application_leases%')`).Scan(&waiting) != nil {
					t.Fatal("lock wait observation")
				}
				if waiting {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("missing controller lock wait")
				}
			}
			switch mode {
			case "controller expired":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
			case "controller changed":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET holder='replacement' WHERE name=$1`, clusterControllerLeaseName)
			case "maintenance changed":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.maintenance)
			case "target resumed":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_nodes SET application_state='active' WHERE id=$1`, f.target)
			case "source address":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_nodes SET management_ip='192.168.90.99' WHERE id=$1`, f.source)
			case "configuration":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_state SET config_json=config_json||' ' WHERE id=1`)
			case "cancelled":
				cancel()
			}
			if mode == "cancelled" {
				_ = blocker.Rollback()
			} else if err != nil || blocker.Commit() != nil {
				t.Fatal("blocking writer", err)
			}
			if err := <-done; err == nil {
				t.Fatal("changed authority survived lock wait")
			}
			joined = true
			f.assertNoReservation(t, id)
		})
	}
}
