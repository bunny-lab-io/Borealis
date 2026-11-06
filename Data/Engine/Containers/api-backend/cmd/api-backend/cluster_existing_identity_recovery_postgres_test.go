package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func existingIdentityMutationFixture(t *testing.T) (existingIdentityFixture, clusterExistingIdentityInspectionClaim, clusterExistingIdentitySourceProcess, func(context.Context, string, any) error) {
	t.Helper()
	f, p, target, process := existingIdentityInspectionFixture(t)
	i, err := f.c.store.claimClusterExistingIdentityInspection(f.ctx, p.OperationID, target.Binding.TargetID, newClusterUUID(), process)
	if err != nil {
		t.Fatal(err)
	}
	result := existingIdentityInspectionResultFixture(i, process)
	if f.c.store.completeClusterExistingIdentityInspection(f.ctx, i, result) != nil {
		t.Fatal("settle inspection fixture")
	}
	_, _, pod := existingIdentitySourceFixture(t)
	meta := clusterSSHStorageMap(pod, "metadata")
	meta["name"], meta["uid"] = process.PodName, process.PodUID
	clusterSSHStorageMap(meta, "labels")["borealis.io/engine-node"] = process.NodeName
	clusterSSHStorageMap(pod, "spec")["nodeName"] = process.NodeName
	native := f.read(t)
	read := func(ctx context.Context, path string, out any) error {
		if f.c.store.db.Stats().InUse != 0 {
			t.Fatal("connection held during mutation proof")
		}
		if path == "/api/v1/namespaces/borealis/pods/"+process.PodName {
			raw, _ := json.Marshal(pod)
			return json.Unmarshal(raw, out)
		}
		return native(ctx, path, out)
	}
	return f, i, process, read
}

func TestClusterExistingIdentityPostgresRecoveryLifecycle(t *testing.T) {
	f, i, process, read := existingIdentityMutationFixture(t)
	s, p := f.c.store, i.Plan
	if queue, err := s.pendingClusterExistingIdentityRecoveries(f.ctx); err != nil || len(queue) != 0 {
		t.Fatal("inspection became mutation work")
	}
	if _, err := s.claimClusterExistingIdentityRecovery(f.ctx, p.OperationID, i.Lease.TargetID, newClusterUUID(), process); err == nil {
		t.Fatal("mutation claimed without original controller")
	}
	if handled, err := f.c.reconcileClusterExistingIdentityMutation(f.ctx, read); err != nil || !handled {
		t.Fatal("controller did not reserve exact target", err)
	}
	if queue, err := s.pendingClusterExistingIdentityRecoveries(f.ctx); err != nil || len(queue) != 1 {
		t.Fatal("reserved mutation not queued", err)
	}
	c, err := s.claimClusterExistingIdentityRecovery(f.ctx, p.OperationID, i.Lease.TargetID, newClusterUUID(), process)
	if err != nil || !c.valid() || c.Lease.Generation != i.Lease.Generation+1 {
		t.Fatal("claim mutation", err)
	}
	if validClusterSSHIdentityRecoveryLease(c.Lease) || validClusterSSHObservationLease(c.Lease) {
		t.Fatal("existing recovery became clean-host authority")
	}
	if _, err := s.claimClusterExistingIdentityRecovery(f.ctx, p.OperationID, i.Lease.TargetID, newClusterUUID(), process); err == nil {
		t.Fatal("live mutation stolen")
	}
	if s.renewClusterExistingIdentityRecovery(f.ctx, c) != nil {
		t.Fatal("renew exact mutation")
	}
	if _, state, err := s.clusterExistingIdentityPlan(f.ctx, p.TargetID, p.OperationID); err != nil || state != "recovering_identity" {
		t.Fatal("recovery readback", state, err)
	}
	if digest, err := s.loadClusterExistingIdentityRecovery(f.ctx, c); err != nil || !validClusterSSHPreparationDigest(digest) {
		t.Fatal("load mutation authority", err)
	}
	request := clusterExistingIdentityCustodyRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 20, Lease: c.Lease, Process: process}
	if proof, digest, err := f.c.observeClusterExistingIdentityCustody(f.ctx, request, read); err != nil || !proof.valid(p) || !validClusterSSHPreparationDigest(digest) {
		t.Fatal("mutation custody broker authority", err)
	}
	request.Lease.Generation--
	if _, _, err := f.c.observeClusterExistingIdentityCustody(f.ctx, request, read); err == nil {
		t.Fatal("old generation obtained native custody proof")
	}
	if s.requireClusterExistingIdentityRecovery(f.ctx, c) != nil || s.requireClusterExistingIdentityRecovery(f.ctx, c) != nil {
		t.Fatal("failure settlement not idempotent")
	}
	if s.completeClusterExistingIdentityRecovery(f.ctx, c) == nil {
		t.Fatal("opposite outcome accepted")
	}
	var expiry int64
	var holder string
	if s.db.QueryRowContext(f.ctx, `SELECT lease_expires_at,lease_holder FROM engine.cluster_onboarding_targets WHERE id=$1`, c.Lease.TargetID).Scan(&expiry, &holder) != nil || expiry == 0 || holder != c.Lease.Holder {
		t.Fatal("uncertain failure dropped ownership")
	}
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE id=$1`, c.Lease.TargetID)
	next, err := s.claimClusterExistingIdentityRecovery(f.ctx, p.OperationID, i.Lease.TargetID, newClusterUUID(), process)
	if err != nil || next.Lease.Generation != c.Lease.Generation+1 {
		t.Fatal("expired recovery retry", err)
	}
	if s.renewClusterExistingIdentityRecovery(f.ctx, c) == nil {
		t.Fatal("old generation still authoritative")
	}
	if s.completeClusterExistingIdentityRecovery(f.ctx, next) != nil || s.completeClusterExistingIdentityRecovery(f.ctx, next) != nil {
		t.Fatal("success settlement not idempotent")
	}
	if _, state, err := s.clusterExistingIdentityPlan(f.ctx, p.TargetID, p.OperationID); err != nil || state != "identity_recovered_pending_return" {
		t.Fatal("recovered readback", state, err)
	}
	if s.requireClusterExistingIdentityRecovery(f.ctx, next) == nil || s.renewClusterExistingIdentityRecovery(f.ctx, next) == nil {
		t.Fatal("completed recovery revived")
	}
	if queue, err := s.pendingClusterExistingIdentityRecoveries(f.ctx); err != nil || len(queue) != 0 {
		t.Fatal("completed recovery remained executable")
	}
	var count int
	if s.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type IN ('existing_identity_recovery_required','existing_identity_recovered')`, p.OperationID).Scan(&count) != nil || count != 2 {
		t.Fatal("duplicate recovery outcomes")
	}
	var state, step, application string
	if s.db.QueryRowContext(f.ctx, `SELECT o.state,o.current_step,n.application_state FROM engine.cluster_operations o JOIN engine.cluster_nodes n ON n.id=o.target_node_id WHERE o.id=$1`, p.OperationID).Scan(&state, &step, &application) != nil || state != "running" || step != clusterExistingIdentityRecover || application != "drained" {
		t.Fatal("recovery restarted or admitted target")
	}
	if handled, err := f.c.reconcileClusterExistingIdentityMutation(f.ctx, read); err != nil || !handled {
		t.Fatal("recovery operation did not settle", err)
	}
	if handled, err := f.c.reconcileClusterExistingIdentityMutation(f.ctx, read); err != nil || handled {
		t.Fatal("terminal recovery replayed", err)
	}
	if _, state, err := s.clusterExistingIdentityPlan(f.ctx, p.TargetID, p.OperationID); err != nil || state != "identity_recovered" {
		t.Fatal("terminal recovery readback", state, err)
	}
	var active, reason string
	if s.db.QueryRowContext(f.ctx, `SELECT COALESCE(c.active_operation_id,''),n.application_state,n.drain_reason FROM engine.cluster_state c,engine.cluster_nodes n WHERE c.id=1 AND n.id=$1`, p.TargetID).Scan(&active, &application, &reason) != nil || active != "" || application != "drained" || reason != "operator maintenance" {
		t.Fatal("completion changed operator maintenance")
	}
}

func TestClusterExistingIdentityPostgresRecoveryReservationDrift(t *testing.T) {
	for _, mode := range []string{"controller", "inspection expired", "receipt", "config during native", "source Pod", "source Node"} {
		t.Run(mode, func(t *testing.T) {
			f, i, _, read := existingIdentityMutationFixture(t)
			switch mode {
			case "controller":
				f.c.holder = "replacement"
			case "inspection expired":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspected_at=1 WHERE id=$1`, i.Lease.TargetID)
			case "receipt":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json=inspection_json||' ' WHERE id=$1`, i.Lease.TargetID)
			}
			calls := 0
			get := func(ctx context.Context, path string, out any) error {
				calls++
				if mode == "config during native" && calls == 1 {
					f.exec(t, `UPDATE engine.cluster_state SET config_json=config_json||' ' WHERE id=1`)
				}
				if mode == "source Node" && path == "/api/v1/nodes" {
					return errClusterConflict
				}
				if mode == "source Pod" && strings.Contains(path, "/pods/") {
					return errClusterConflict
				}
				return read(ctx, path, out)
			}
			if f.c.reserveClusterExistingIdentityMutation(f.ctx, i.Plan.OperationID, i.Lease.TargetID, get) == nil {
				t.Fatal("drift enabled mutation")
			}
			var step string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT current_step FROM engine.cluster_operations WHERE id=$1`, i.Plan.OperationID).Scan(&step) != nil || step != clusterExistingIdentityConfirmed {
				t.Fatal("partial mutation reservation")
			}
		})
	}
}

func TestClusterExistingIdentityPostgresConcurrentRecovery(t *testing.T) {
	f, i, process, read := existingIdentityMutationFixture(t)
	if f.c.reserveClusterExistingIdentityMutation(f.ctx, i.Plan.OperationID, i.Lease.TargetID, read) != nil {
		t.Fatal("reserve mutation")
	}
	f.c.store.db.SetMaxOpenConns(4)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.c.store.claimClusterExistingIdentityRecovery(f.ctx, i.Plan.OperationID, i.Lease.TargetID, newClusterUUID(), process)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("mutation winners", winners)
	}
}

func TestClusterExistingIdentityPostgresRecoveryWaitFence(t *testing.T) {
	for _, phase := range []string{"reserve", "claim", "renew", "complete", "reserve event expiry", "complete event expiry", "finish event expiry"} {
		t.Run(phase, func(t *testing.T) {
			f, i, process, read := existingIdentityMutationFixture(t)
			s := f.c.store
			ctx, cancel := context.WithTimeout(f.ctx, 12*time.Second)
			defer cancel()
			cache := map[string]json.RawMessage{}
			for _, path := range []string{"/api/v1/namespaces/kube-system", "/api/v1/nodes", "/api/v1/namespaces/borealis/pods/" + process.PodName} {
				var raw json.RawMessage
				if read(ctx, path, &raw) != nil {
					t.Fatal("capture native fixture")
				}
				cache[path] = raw
			}
			get := func(_ context.Context, path string, out any) error {
				raw, ok := cache[path]
				if !ok {
					return errClusterConflict
				}
				return json.Unmarshal(raw, out)
			}
			var c clusterExistingIdentityRecoveryClaim
			var err error
			if !strings.HasPrefix(phase, "reserve") {
				if f.c.reserveClusterExistingIdentityMutation(ctx, i.Plan.OperationID, i.Lease.TargetID, read) != nil {
					t.Fatal("reserve fixture")
				}
				if phase != "claim" {
					c, err = s.claimClusterExistingIdentityRecovery(ctx, i.Plan.OperationID, i.Lease.TargetID, newClusterUUID(), process)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if phase == "finish event expiry" && s.completeClusterExistingIdentityRecovery(ctx, c) != nil {
				t.Fatal("settle fixture")
			}
			expires := time.Now().Unix() + 2
			eventWait := strings.Contains(phase, "event expiry")
			if phase == "complete event expiry" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=$2 WHERE id=$1`, i.Lease.TargetID, expires)
			} else if eventWait {
				f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=$2 WHERE name=$1`, clusterControllerLeaseName, expires)
			}
			capture := func() string {
				var value string
				if s.db.QueryRowContext(ctx, `SELECT json_build_array(o.state,o.current_step,t.state,t.current_step,t.lease_holder,t.lease_generation,t.lease_expires_at,c.active_operation_id,
 (SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=o.id))::text FROM engine.cluster_operations o JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id CROSS JOIN engine.cluster_state c WHERE o.id=$1 AND t.id=$2 AND c.id=1`, i.Plan.OperationID, i.Lease.TargetID).Scan(&value) != nil {
					t.Fatal("capture recovery state")
				}
				return value
			}
			before := capture()
			s.db.SetMaxOpenConns(4)
			blocker, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			pattern := "%engine.cluster_application_leases%"
			if eventWait {
				_, err = blocker.ExecContext(ctx, `LOCK TABLE engine.cluster_operation_events IN ACCESS EXCLUSIVE MODE`)
				pattern = "%INSERT INTO engine.cluster_operation_events%"
			} else {
				_, err = blocker.ExecContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName)
			}
			if err != nil {
				t.Fatal(err)
			}
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
				var err error
				switch phase {
				case "reserve", "reserve event expiry":
					err = f.c.reserveClusterExistingIdentityMutation(ctx, i.Plan.OperationID, i.Lease.TargetID, get)
				case "claim":
					_, err = s.claimClusterExistingIdentityRecovery(ctx, i.Plan.OperationID, i.Lease.TargetID, newClusterUUID(), process)
				case "renew":
					err = s.renewClusterExistingIdentityRecovery(ctx, c)
				case "finish event expiry":
					err = f.c.completeClusterExistingIdentityOperation(ctx, i.Plan.OperationID, i.Lease.TargetID, get)
				default:
					err = s.completeClusterExistingIdentityRecovery(ctx, c)
				}
				done <- err
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&waiting) != nil {
					t.Fatal("observe recovery lock wait")
				}
				if waiting {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("no recovery waiter")
				}
			}
			if eventWait {
				for {
					var expired bool
					if s.db.QueryRowContext(ctx, `SELECT extract(epoch FROM clock_timestamp()) >= $1`, expires).Scan(&expired) != nil {
						t.Fatal("expiry clock")
					}
					if expired {
						break
					}
					select {
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal("expiry timeout")
					}
				}
			} else if _, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET holder='replacement' WHERE name=$1`, clusterControllerLeaseName); err != nil {
				t.Fatal(err)
			}
			if blocker.Commit() != nil {
				t.Fatal("release recovery waiter")
			}
			err = <-done
			joined = true
			if err == nil || capture() != before {
				t.Fatal("stale mutation crossed wait or partial changes survived", err)
			}
		})
	}
}
