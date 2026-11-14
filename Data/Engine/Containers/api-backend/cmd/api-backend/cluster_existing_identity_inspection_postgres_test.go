package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func existingIdentityInspectionFixture(t *testing.T) (existingIdentityFixture, clusterExistingIdentityPlan, clusterSSHPlannedTarget, clusterExistingIdentitySourceProcess) {
	t.Helper()
	f, p, _, target, _ := existingIdentityConfirmationFixture(t)
	digest, _ := clusterSSHPreparationDigest(p)
	if _, err := f.c.store.confirmClusterExistingIdentity(f.ctx, "admin", p, digest, target); err != nil {
		t.Fatal(err)
	}
	process := clusterExistingIdentitySourceProcess{PodName: "api-backend-source-abcde", PodUID: newClusterUUID()}
	for _, m := range p.Members {
		if m.NodeID == p.SourceID {
			process.NodeName = m.Name
		}
	}
	return f, p, target, process
}

func existingIdentityInspectionResultFixture(c clusterExistingIdentityInspectionClaim, process clusterExistingIdentitySourceProcess) clusterExistingIdentityInspectionResult {
	binding, recovery, _ := c.session()
	return clusterExistingIdentityInspectionResult{Version: 1, PlanSHA256: c.Confirmation.PlanSHA256,
		Source: clusterExistingIdentitySourceCustody{Process: process, ContainerID: "containerd://" + strings.Repeat("b", 64), Image: "ghcr.io/bunny-lab-io/borealis/api-backend@sha256:" + strings.Repeat("a", 64)},
		Session: clusterbootstrap.SessionMessage{Protocol: 1, State: "verified", Nonce: strings.Repeat("c", 64), Binding: &binding,
			SourceSHA: c.Plan.Baseline.SourceSHA, ManagerSHA256: strings.Repeat("d", 64), IdentityRecovery: &recovery}}
}

func TestClusterExistingIdentityPostgresInspectionLifecycle(t *testing.T) {
	f, p, target, process := existingIdentityInspectionFixture(t)
	s := f.c.store
	c, err := s.claimClusterExistingIdentityInspection(f.ctx, p.OperationID, target.Binding.TargetID, newClusterUUID(), process)
	if err != nil || !c.valid() || c.Lease.Generation != 1 {
		t.Fatal("inspection claim", err)
	}
	if _, state, err := s.clusterExistingIdentityPlan(f.ctx, p.TargetID, p.OperationID); err != nil || state != "observing_existing_identity" {
		t.Fatal("inspection readback", err, state)
	}
	if validClusterSSHIdentityRecoveryLease(c.Lease) || validClusterSSHObservationLease(c.Lease) {
		t.Fatal("existing inspection became onboarding authority")
	}
	if _, err := s.claimClusterExistingIdentityInspection(f.ctx, p.OperationID, c.Lease.TargetID, newClusterUUID(), process); err == nil {
		t.Fatal("live inspection stolen")
	}
	if digest, err := s.loadClusterExistingIdentityInspection(f.ctx, c); err != nil || !validClusterSSHPreparationDigest(digest) {
		t.Fatal("load authority", err)
	}
	if s.renewClusterExistingIdentityInspection(f.ctx, c) != nil {
		t.Fatal("renew inspection")
	}
	if s.db.Stats().InUse != 0 {
		t.Fatal("inspection retains SQL connection")
	}
	if _, err := s.cancelClusterOperation(f.ctx, "admin", p.OperationID); err == nil {
		t.Fatal("generic cancellation accepted claimed work")
	}
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE id=$1`, c.Lease.TargetID)
	if s.renewClusterExistingIdentityInspection(f.ctx, c) == nil {
		t.Fatal("expired lease revived")
	}
	next, err := s.claimClusterExistingIdentityInspection(f.ctx, p.OperationID, c.Lease.TargetID, newClusterUUID(), process)
	if err != nil || next.Lease.Generation != 2 {
		t.Fatal("read-only retry", err)
	}
	if s.renewClusterExistingIdentityInspection(f.ctx, c) == nil {
		t.Fatal("stale generation renewed")
	}
	result := existingIdentityInspectionResultFixture(next, process)
	if err := s.completeClusterExistingIdentityInspection(f.ctx, next, result); err != nil {
		t.Fatal("complete read-only inspection", err)
	}
	if err := s.completeClusterExistingIdentityInspection(f.ctx, next, result); err != nil {
		t.Fatal("lost ack settlement", err)
	}
	if _, state, err := s.clusterExistingIdentityPlan(f.ctx, p.TargetID, p.OperationID); err != nil || state != "identity_observed" {
		t.Fatal("observed readback", err, state)
	}
	result.Session.Nonce = strings.Repeat("e", 64)
	if s.completeClusterExistingIdentityInspection(f.ctx, next, result) == nil {
		t.Fatal("opposite receipt replaced settled result")
	}
	if _, err := s.claimClusterExistingIdentityInspection(f.ctx, p.OperationID, c.Lease.TargetID, newClusterUUID(), process); err == nil {
		t.Fatal("completed inspection replayed")
	}
	if _, err := s.loadClusterExistingIdentityInspection(f.ctx, next); err == nil {
		t.Fatal("completed inspection still grants authority")
	}
	var events int
	if s.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type='existing_identity_observed'`, p.OperationID).Scan(&events) != nil || events != 1 {
		t.Fatal("duplicated settlement event")
	}
	var state, step, drain string
	if s.db.QueryRowContext(f.ctx, `SELECT o.state,o.current_step,n.application_state FROM engine.cluster_operations o JOIN engine.cluster_nodes n ON n.id=o.target_node_id WHERE o.id=$1`, p.OperationID).Scan(&state, &step, &drain) != nil || state != "waiting" || step != clusterExistingIdentityConfirmed || drain != "drained" {
		t.Fatal("inspection enabled mutation or return")
	}
}

func TestClusterExistingIdentityPostgresInspectionDrift(t *testing.T) {
	for _, mode := range []string{"controller", "controller expired", "config", "maintenance", "member", "Aegis", "ciphertext", "credentials expired", "target pin", "phase", "payload", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f, p, target, process := existingIdentityInspectionFixture(t)
			c, err := f.c.store.claimClusterExistingIdentityInspection(f.ctx, p.OperationID, target.Binding.TargetID, newClusterUUID(), process)
			if err != nil {
				t.Fatal("initial claim", err)
			}
			switch mode {
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other' WHERE name=$1`, clusterControllerLeaseName)
			case "controller expired":
				f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
			case "config":
				f.exec(t, `UPDATE engine.cluster_state SET config_json=config_json||' ' WHERE id=1`)
			case "maintenance":
				f.exec(t, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, p.MaintenanceID)
			case "member":
				f.exec(t, `UPDATE engine.cluster_nodes SET drain_reason='other' WHERE id=$1`, p.TargetID)
			case "Aegis":
				f.exec(t, `UPDATE engine.aegis_cipher_state SET verification_token='other' WHERE id=1`)
			case "ciphertext":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'x' WHERE target_id=$1`, c.Lease.TargetID)
			case "credentials expired":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, c.Lease.TargetID)
			case "target pin":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET host_key_base64=host_key_base64||'x' WHERE id=$1`, c.Lease.TargetID)
			case "phase":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET current_step='recover_identity' WHERE id=$1`, c.Lease.TargetID)
			case "payload":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, p.OperationID)
			case "cancelled":
				f.exec(t, `UPDATE engine.cluster_operations SET state='cancelled' WHERE id=$1`, p.OperationID)
			}
			if _, err := f.c.store.loadClusterExistingIdentityInspection(f.ctx, c); err == nil {
				t.Fatal("drifted authority loaded")
			}
			if f.c.store.renewClusterExistingIdentityInspection(f.ctx, c) == nil {
				t.Fatal("drifted authority renewed")
			}
			if f.c.store.completeClusterExistingIdentityInspection(f.ctx, c, existingIdentityInspectionResultFixture(c, process)) == nil {
				t.Fatal("drifted authority settled")
			}
		})
	}
}

func TestClusterExistingIdentityPostgresConcurrentInspection(t *testing.T) {
	f, p, target, process := existingIdentityInspectionFixture(t)
	f.c.store.db.SetMaxOpenConns(4)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.c.store.claimClusterExistingIdentityInspection(context.Background(), p.OperationID, target.Binding.TargetID, newClusterUUID(), process)
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
		t.Fatal("inspection claim winners", winners)
	}
}

func TestClusterExistingIdentityPostgresInspectionWaitFence(t *testing.T) {
	for _, phase := range []string{"claim", "renew", "complete", "event expiry"} {
		t.Run(phase, func(t *testing.T) {
			f, p, target, process := existingIdentityInspectionFixture(t)
			s := f.c.store
			s.db.SetMaxOpenConns(4)
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			var c clusterExistingIdentityInspectionClaim
			var err error
			if phase != "claim" {
				c, err = s.claimClusterExistingIdentityInspection(ctx, p.OperationID, target.Binding.TargetID, newClusterUUID(), process)
				if err != nil {
					t.Fatal(err)
				}
			}
			expires := time.Now().Unix() + 2
			if phase == "event expiry" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=$2 WHERE id=$1`, target.Binding.TargetID, expires)
			}
			blocker, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			pattern := "%engine.cluster_application_leases%"
			if phase == "event expiry" {
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
				case "claim":
					_, err = s.claimClusterExistingIdentityInspection(ctx, p.OperationID, target.Binding.TargetID, newClusterUUID(), process)
				case "renew":
					err = s.renewClusterExistingIdentityInspection(ctx, c)
				default:
					err = s.completeClusterExistingIdentityInspection(ctx, c, existingIdentityInspectionResultFixture(c, process))
				}
				done <- err
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&waiting) != nil {
					t.Fatal("lock wait observation")
				}
				if waiting {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("inspection never waited")
				}
			}
			if phase == "event expiry" {
				for {
					var expired bool
					if s.db.QueryRowContext(ctx, `SELECT extract(epoch FROM clock_timestamp()) >= $1`, expires).Scan(&expired) != nil {
						t.Fatal("database expiry clock")
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
				t.Fatal("release waiter")
			}
			err = <-done
			joined = true
			if err == nil {
				t.Fatal("stale inspection authority survived wait")
			}
			var step string
			var inspected, events int
			if s.db.QueryRowContext(ctx, `SELECT current_step,inspected_at,(SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type='existing_identity_observed') FROM engine.cluster_onboarding_targets WHERE id=$2`, p.OperationID, target.Binding.TargetID).Scan(&step, &inspected, &events) != nil || step != clusterExistingIdentityInspect || inspected != 0 || events != 0 {
				t.Fatal("partial inspection settlement survived rollback")
			}
		})
	}
}

func TestClusterExistingIdentityPostgresCustodyAuthority(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(map[bool]string{false: "stable", true: "controller drift"}[drift], func(t *testing.T) {
			f, p, target, process := existingIdentityInspectionFixture(t)
			c, err := f.c.store.claimClusterExistingIdentityInspection(f.ctx, p.OperationID, target.Binding.TargetID, newClusterUUID(), process)
			if err != nil {
				t.Fatal(err)
			}
			_, _, pod := existingIdentitySourceFixture(t)
			meta := clusterSSHStorageMap(pod, "metadata")
			meta["name"], meta["uid"] = process.PodName, process.PodUID
			clusterSSHStorageMap(meta, "labels")["borealis.io/engine-node"] = process.NodeName
			clusterSSHStorageMap(pod, "spec")["nodeName"] = process.NodeName
			read := f.read(t)
			pods := 0
			get := func(ctx context.Context, path string, out any) error {
				if f.c.store.db.Stats().InUse != 0 {
					t.Fatal("connection held during custody proof")
				}
				if path == "/api/v1/namespaces/borealis/pods/"+process.PodName {
					pods++
					if drift && pods == 2 {
						f.exec(t, `UPDATE engine.cluster_application_leases SET holder='replacement' WHERE name=$1`, clusterControllerLeaseName)
					}
					raw, _ := json.Marshal(pod)
					return json.Unmarshal(raw, out)
				}
				return read(ctx, path, out)
			}
			r := clusterExistingIdentityCustodyRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 20, Lease: c.Lease, Process: process}
			proof, digest, err := f.c.observeClusterExistingIdentityCustody(f.ctx, r, get)
			if drift {
				if err == nil {
					t.Fatal("authority drift survived native proof")
				}
			} else if err != nil || !proof.valid(p) || !validClusterSSHPreparationDigest(digest) || pods != 2 {
				t.Fatal("native custody lost current authority", err)
			}
		})
	}
}
