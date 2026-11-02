package main

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func newSSHPreparationRecoveryFixture(t *testing.T, replacement bool) (sshPreparationAuthorityFixture, clusterSSHPreparationWork) {
	t.Helper()
	f, _ := newSSHPreparationClaimFixture(t, replacement)
	work, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID())
	if err != nil {
		t.Fatal("initial actual claim", err)
	}
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
	return f, work
}

func sshPreparationRecoveryState(t *testing.T, f sshPreparationAuthorityFixture) string {
	t.Helper()
	var state string
	if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT json_build_array(o.state,o.current_step,o.payload_json,c.active_operation_id,
 (SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=o.id),
 (SELECT json_agg(json_build_array(t.id,t.state,t.current_step,t.lease_holder,t.lease_generation,t.lease_expires_at,t.updated_at) ORDER BY t.id)
 FROM engine.cluster_onboarding_targets t WHERE t.operation_id=o.id))::text
 FROM engine.cluster_operations o CROSS JOIN engine.cluster_state c WHERE o.id=$1 AND c.id=1`, f.op.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertSSHPreparationRecoveryEvents(t *testing.T, f sshPreparationAuthorityFixture, record clusterSSHPreparationRecoveryRecord, count int) {
	t.Helper()
	rows, err := f.c.store.db.QueryContext(f.ctx, `SELECT event_type,state,message,details_json,COALESCE(admission_id,''),cluster_id FROM engine.cluster_operation_events
 WHERE operation_id=$1 AND event_type IN ('ssh_preparation_recovery_reserved','ssh_preparation_recovery_claimed') ORDER BY id`, f.op.ID)
	if err != nil {
		t.Fatal(err)
	}
	type event struct{ kind, state, message, details, admission, cluster string }
	var events []event
	for rows.Next() {
		var e event
		if rows.Scan(&e.kind, &e.state, &e.message, &e.details, &e.admission, &e.cluster) != nil {
			rows.Close()
			t.Fatal("event read")
		}
		events = append(events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(events) != count {
		t.Fatal("duplicate/missing recovery events", len(events), err)
	}
	want := marshalClusterJSON(map[string]any{"attempt": record.Attempt, "step": clusterSSHPreparationRecoveryOperationStep, "target_count": len(record.Targets), "recovery_id": record.ID})
	for i, e := range events {
		kind, message := "ssh_preparation_recovery_reserved", "Interrupted preparation retained for recovery."
		if i == 1 {
			kind, message = "ssh_preparation_recovery_claimed", "Preparation recovery verification pending."
		}
		if e.kind != kind || e.state != "running" || e.message != message || e.admission != "" || e.cluster != f.targets[0].Binding.ClusterID ||
			!sameClusterSSHStoredJSON(json.RawMessage(e.details), json.RawMessage(want), 0) {
			t.Fatal("recovery event changed fixed public envelope")
		}
	}
}

func assertSSHPreparationRecoveryInert(t *testing.T, f sshPreparationAuthorityFixture) {
	t.Helper()
	before, events := sshPreparationRecoveryState(t, f), f.events(t)
	if _, selected, err := f.c.claimOperation(f.ctx); err != nil || selected {
		t.Fatal("generic controller selected private recovery phase", err)
	}
	if _, err := f.c.store.retryClusterOperation(f.ctx, "admission-test", f.op.ID); err == nil {
		t.Fatal("generic retry consumed private recovery")
	}
	if _, err := f.c.store.cancelClusterOperation(f.ctx, "admission-test", f.op.ID); err == nil {
		t.Fatal("generic cancel consumed uncertain recovery")
	}
	if _, err := f.c.store.claimClusterSSHTarget(f.ctx, f.op.ID, f.targets[0].Binding.TargetID, newClusterUUID()); err == nil {
		t.Fatal("ordinary inspection worker consumed recovery")
	}
	if before != sshPreparationRecoveryState(t, f) || events != f.events(t) || f.c.store.db.Stats().InUse != 0 {
		t.Fatal("inert phase check mutated retained state or retained connection")
	}
}

func TestClusterSSHPreparationRecoveryPostgresAtomicCohort(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "expansion", true: "replacement"}[replacement], func(t *testing.T) {
			f, initial := newSSHPreparationRecoveryFixture(t, replacement)
			beforeEvents := f.events(t)
			record, err := f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op)
			if err != nil {
				t.Fatal("reserve", err)
			}
			assertSSHPreparationRecoveryEvents(t, f, record, 1)
			if _, err := f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op); err == nil {
				t.Fatal("lost reservation acknowledgement minted second reservation")
			}
			if _, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID()); err == nil {
				t.Fatal("initial claim replayed")
			}
			f.op.CurrentStep = clusterSSHPreparationRecoveryOperationStep
			assertSSHPreparationRecoveryInert(t, f)
			read, err := f.c.readClusterSSHPreparationRecovery(f.ctx, f.op)
			if err != nil || !reflect.DeepEqual(read.record, record) || !reflect.DeepEqual(read.work.Authorization, initial.Authorization) ||
				!reflect.DeepEqual(read.work.Proof, initial.Proof) || read.work.IdentitySource != initial.IdentitySource || read.work.Source != initial.Source {
				t.Fatal("reservation changed original lineage", err)
			}
			for i, prior := range record.Targets {
				var state, step, holder string
				var generation, expiry int64
				if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step,lease_holder,lease_generation,lease_expires_at
 FROM engine.cluster_onboarding_targets WHERE id=$1`, prior.TargetID).Scan(&state, &step, &holder, &generation, &expiry) != nil ||
					state != "recovery_required" || step != clusterSSHPreparationRecoveryReservedStep || holder != initial.Claims[i].Lease.Holder ||
					generation != initial.Claims[i].Lease.Generation || expiry != 1 || prior.PreviousHolder != holder || prior.PreviousGeneration != generation {
					t.Fatal("reservation discarded original actual claim")
				}
			}
			if _, err := f.c.claimClusterSSHPreparationRecovery(f.ctx, f.op, record.ID, initial.Claims[0].Lease.Holder); err == nil {
				t.Fatal("original holder reused")
			}
			holder := newClusterUUID()
			claim, err := f.c.claimClusterSSHPreparationRecovery(f.ctx, f.op, record.ID, holder)
			if err != nil || len(claim.Claims) != len(initial.Claims) || !reflect.DeepEqual(claim.Reservation, record) {
				t.Fatal("recovery claim", err)
			}
			assertSSHPreparationRecoveryEvents(t, f, record, 2)
			for i, target := range claim.Claims {
				if target.Lease.Generation != initial.Claims[i].Lease.Generation+1 || target.Lease.Holder != holder ||
					target.Previous != record.Targets[i] || target.Sealed != initial.Claims[i].Sealed || validClusterSSHPreparationLease(target.Lease) ||
					validClusterSSHObservationLease(target.Lease) {
					t.Fatal("typed recovery claim lost custody or gained ordinary authority")
				}
			}
			assertSSHPreparationRecoveryInert(t, f)
			for _, sameHolder := range []bool{true, false} {
				next := holder
				if !sameHolder {
					next = newClusterUUID()
				}
				if _, err := f.c.claimClusterSSHPreparationRecovery(f.ctx, f.op, record.ID, next); err == nil {
					t.Fatal("claim acknowledgement replay obtained another lineage")
				}
			}
			f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
			if _, err := f.c.claimClusterSSHPreparationRecovery(f.ctx, f.op, record.ID, newClusterUUID()); err == nil {
				t.Fatal("uncertain expired recovery claim replayed")
			}
			after, err := f.c.readClusterSSHPreparationRecovery(f.ctx, f.op)
			if err != nil || !reflect.DeepEqual(after.record, record) || f.c.store.db.Stats().InUse != 0 || f.events(t) != beforeEvents+2 {
				t.Fatal("retained reservation unavailable, connection retained or duplicate recovery event emitted", err)
			}
			assertSSHPreparationRecoveryEvents(t, f, record, 2)
		})
	}
	for _, phase := range []string{"reserve", "claim"} {
		t.Run(phase+" contenders", func(t *testing.T) {
			f, _ := newSSHPreparationRecoveryFixture(t, false)
			f.c.store.db.SetMaxOpenConns(4)
			var record clusterSSHPreparationRecoveryRecord
			if phase == "claim" {
				var err error
				record, err = f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op)
				if err != nil {
					t.Fatal(err)
				}
				f.op.CurrentStep = clusterSSHPreparationRecoveryOperationStep
			}
			start, results := make(chan struct{}), make(chan error, 2)
			var workers sync.WaitGroup
			for range 2 {
				workers.Add(1)
				go func() {
					defer workers.Done()
					<-start
					var err error
					if phase == "claim" {
						_, err = f.c.claimClusterSSHPreparationRecovery(f.ctx, f.op, record.ID, newClusterUUID())
					} else {
						_, err = f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op)
					}
					results <- err
				}()
			}
			close(start)
			workers.Wait()
			close(results)
			success := 0
			for err := range results {
				if err == nil {
					success++
				}
			}
			if success != 1 {
				t.Fatal("expected exactly one cohort transition", success)
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryPostgresRejectsDrift(t *testing.T) {
	for _, phase := range []string{"reserve", "claim"} {
		for _, mode := range []string{"controller", "controller expiry", "attempt", "phase", "payload", "source", "source member", "active pointer", "missing sibling", "credential", "credential expiry", "ciphertext", "aegis", "target generation", "target holder", "target expiry", "target key", "target report", "prepared sibling", "joined sibling", "changed snapshot", "credential snapshot"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				f, _ := newSSHPreparationRecoveryFixture(t, false)
				if phase == "claim" {
					if _, err := f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op); err != nil {
						t.Fatal(err)
					}
					f.op.CurrentStep = clusterSSHPreparationRecoveryOperationStep
				}
				snapshot, err := f.c.readClusterSSHPreparationRecovery(f.ctx, f.op)
				if err != nil {
					t.Fatal(err)
				}
				last := slices.Max([]string{f.targets[0].Binding.TargetID, f.targets[1].Binding.TargetID})
				switch mode {
				case "controller":
					f.exec(t, `UPDATE engine.cluster_application_leases SET holder='replacement-controller' WHERE name=$1`, clusterControllerLeaseName)
				case "controller expiry":
					f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
				case "attempt":
					f.exec(t, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, f.op.ID)
				case "phase":
					f.exec(t, `UPDATE engine.cluster_operations SET current_step='qualify_ssh_targets' WHERE id=$1`, f.op.ID)
				case "payload":
					f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
				case "source":
					f.exec(t, `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1`)
				case "source member":
					f.exec(t, `UPDATE engine.cluster_nodes SET node_name=node_name||'-changed' WHERE id=$1`, snapshot.work.Proof.Source.Members[0].NodeID)
				case "active pointer":
					f.exec(t, `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1`)
				case "missing sibling":
					f.exec(t, `DELETE FROM engine.cluster_onboarding_targets WHERE id=$1`, last)
				case "credential":
					f.exec(t, `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, last)
				case "credential expiry":
					f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, last)
				case "ciphertext":
					f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, last)
				case "aegis":
					f.exec(t, `UPDATE engine.aegis_cipher_state SET verification_token='changed' WHERE id=1`)
				case "target generation":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, last)
				case "target holder":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_holder=$2 WHERE id=$1`, last, newClusterUUID())
				case "target expiry":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=2 WHERE id=$1`, last)
				case "target key":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET host_key_base64='changed' WHERE id=$1`, last)
				case "target report":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, last)
				case "prepared sibling":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='prepared',current_step='preparation_complete' WHERE id=$1`, last)
				case "joined sibling":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='joined',current_step='joined' WHERE id=$1`, last)
				case "changed snapshot":
					snapshot.record.Targets[1].PreviousHolder = newClusterUUID()
				case "credential snapshot":
					snapshot.credentials[1].ciphertext += "changed"
				}
				before := sshPreparationRecoveryState(t, f)
				holder := ""
				if phase == "claim" {
					holder = newClusterUUID()
				}
				if f.c.commitClusterSSHPreparationRecovery(f.ctx, snapshot, holder) == nil {
					t.Fatal("changed authority accepted")
				}
				if after := sshPreparationRecoveryState(t, f); after != before || f.c.store.db.Stats().InUse != 0 {
					t.Fatal("failed cohort transition mutated durable state or retained connection")
				}
			})
		}
	}
	for _, mode := range []string{"unclaimed", "live initial", "partial settled", "legacy completion"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newSSHPreparationClaimFixture(t, false)
			if mode != "unclaimed" {
				if _, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID()); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "partial settled" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='prepared' WHERE id=$1`, f.targets[1].Binding.TargetID)
			}
			if mode == "legacy completion" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=(payload_json::jsonb || '{"ssh_preparation":{"version":1}}'::jsonb)::text WHERE id=$1`, f.op.ID)
			}
			before := sshPreparationRecoveryState(t, f)
			if _, err := f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op); err == nil || before != sshPreparationRecoveryState(t, f) {
				t.Fatal("non-initial interrupted cohort accepted")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryPostgresRechecksAfterWait(t *testing.T) {
	for _, phase := range []string{"reserve", "claim"} {
		for _, clock := range []string{"controller", "credential"} {
			t.Run(phase+"/event lock "+clock, func(t *testing.T) { assertSSHPreparationRecoveryEventWait(t, phase, clock) })
		}
	}
	for _, phase := range []string{"reserve", "claim"} {
		for _, mode := range []string{"payload", "source", "controller expiry", "credential expiry", "ciphertext", "cancel"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				f, _ := newSSHPreparationRecoveryFixture(t, false)
				if phase == "claim" {
					if _, err := f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op); err != nil {
						t.Fatal(err)
					}
					f.op.CurrentStep = clusterSSHPreparationRecoveryOperationStep
				}
				snapshot, err := f.c.readClusterSSHPreparationRecovery(f.ctx, f.op)
				if err != nil {
					t.Fatal(err)
				}
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
				done := make(chan error, 1)
				joined := false
				holder := ""
				if phase == "claim" {
					holder = newClusterUUID()
				}
				go func() { done <- f.c.commitClusterSSHPreparationRecovery(ctx, snapshot, holder) }()
				defer func() {
					blocker.Rollback()
					cancel()
					if !joined {
						<-done
					}
				}()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var waiting bool
					if f.c.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%engine.cluster_application_leases%')`).Scan(&waiting) != nil {
						t.Fatal("wait observation failed")
					}
					if waiting {
						break
					}
					select {
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal("transition did not reach lock wait")
					}
				}
				switch mode {
				case "payload":
					_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
				case "source":
					_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1`)
				case "controller expiry":
					_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
				case "credential expiry":
					_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, f.targets[1].Binding.TargetID)
				case "ciphertext":
					_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.targets[1].Binding.TargetID)
				case "cancel":
					cancel()
				}
				if err != nil || mode != "cancel" && blocker.Commit() != nil {
					t.Fatal("authority mutation failed", err)
				}
				result := <-done
				joined = true
				if result == nil {
					t.Fatal("transition survived changed authority")
				}
				if mode == "cancel" {
					blocker.Rollback()
				}
				var count int
				step := clusterSSHPreparationRecoveryReservedStep
				if phase == "claim" {
					step = clusterSSHPreparationRecoveryClaimStep
				}
				if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND current_step=$2`, f.op.ID, step).Scan(&count) != nil || count != 0 {
					t.Fatal("partial transition survived lock wait")
				}
			})
		}
	}
}

func assertSSHPreparationRecoveryEventWait(t *testing.T, phase, clock string) {
	t.Helper()
	f, _ := newSSHPreparationRecoveryFixture(t, false)
	if phase == "claim" {
		if _, err := f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op); err != nil {
			t.Fatal(err)
		}
		f.op.CurrentStep = clusterSSHPreparationRecoveryOperationStep
	}
	snapshot, err := f.c.readClusterSSHPreparationRecovery(f.ctx, f.op)
	if err != nil {
		t.Fatal(err)
	}
	f.c.store.db.SetMaxOpenConns(4)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	var deadline int64
	if f.c.store.db.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp()))::bigint+2`).Scan(&deadline) != nil {
		t.Fatal("clock")
	}
	if clock == "controller" {
		f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=$2 WHERE name=$1`, clusterControllerLeaseName, deadline)
	} else {
		f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=$2 WHERE target_id=$1`, f.targets[1].Binding.TargetID, deadline)
	}
	before := sshPreparationRecoveryState(t, f)
	blocker, err := f.c.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(ctx, `LOCK TABLE engine.cluster_operation_events IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	joined := false
	holder := ""
	if phase == "claim" {
		holder = newClusterUUID()
	}
	go func() { done <- f.c.commitClusterSSHPreparationRecovery(ctx, snapshot, holder) }()
	defer func() {
		blocker.Rollback()
		cancel()
		if !joined {
			<-done
		}
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		if f.c.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%INSERT INTO engine.cluster_operation_events%')`).Scan(&waiting) != nil {
			t.Fatal("event lock observation")
		}
		if waiting {
			break
		}
		select {
		case <-tick.C:
		case err := <-done:
			joined = true
			t.Fatal("transition did not reach event lock", err)
		case <-ctx.Done():
			t.Fatal("event wait deadline")
		}
	}
	for {
		var expired bool
		if f.c.store.db.QueryRowContext(ctx, `SELECT extract(epoch FROM clock_timestamp())>=$1`, deadline).Scan(&expired) != nil {
			t.Fatal("clock wait")
		}
		if expired {
			break
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal("expiry deadline")
		}
	}
	if blocker.Commit() != nil {
		t.Fatal("event unlock")
	}
	result := <-done
	joined = true
	if result == nil {
		t.Fatal("event lock wait bypassed final expiry")
	}
	if before != sshPreparationRecoveryState(t, f) || f.c.store.db.Stats().InUse != 0 {
		t.Fatal("failed event transaction retained partial state/event/connection")
	}
}

func TestClusterSSHPreparationRecoveryPostgresInitialAuthorityFence(t *testing.T) {
	// Legacy preparation observation lacks an authorization record. Even that
	// read-only compatibility path must reject recovery fields before effects.
	for _, key := range []string{"ssh_preparation_recovery", "SSH_PREPARATION_RECOVERY"} {
		t.Run(key, func(t *testing.T) {
			f := newSSHPreparationAuthorityFixture(t)
			f.exec(t, `UPDATE engine.cluster_operations SET payload_json=left(payload_json,length(payload_json)-1)||$2 WHERE id=$1`, f.op.ID, `,"`+key+`":{}}`)
			if _, err := f.read(f.ctx); err == nil {
				t.Fatal("legacy observer accepted recovery field")
			}
		})
	}
	// Historical observations are never rewritten to manufacture freshness.
	f, _ := newSSHPreparationRecoveryFixture(t, false)
	record, err := f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op)
	if err != nil {
		t.Fatal(err)
	}
	f.op.CurrentStep = clusterSSHPreparationRecoveryOperationStep
	snapshot, err := f.c.readClusterSSHPreparationRecovery(f.ctx, f.op)
	if err != nil || !strings.Contains(snapshot.payload, record.PayloadSHA256) {
		t.Fatal("history missing", err)
	}
	if _, err := f.c.claimClusterSSHPreparationRecovery(f.ctx, f.op, newClusterUUID(), newClusterUUID()); err == nil {
		t.Fatal("wrong reservation accepted")
	}
}

func TestClusterSSHPreparationRecoveryPostgresPostWriteFence(t *testing.T) {
	for _, phase := range []string{"reserve", "claim"} {
		for _, mode := range []string{"deleted cohort", "substituted target", "added target", "generation", "holder", "expiry", "extended expiry", "report", "credential", "payload", "attempt", "parent state", "active pointer", "source", "source member", "controller expiry", "credential expiry"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				f, _ := newSSHPreparationRecoveryFixture(t, mode == "added target")
				if phase == "claim" {
					if _, err := f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op); err != nil {
						t.Fatal(err)
					}
					f.op.CurrentStep = clusterSSHPreparationRecoveryOperationStep
				}
				snapshot, err := f.c.readClusterSSHPreparationRecovery(f.ctx, f.op)
				if err != nil {
					t.Fatal(err)
				}
				id := f.targets[len(f.targets)-1].Binding.TargetID
				// Values interpolated below are generated UUID fixture identifiers,
				// never user input. All trigger work belongs to this disposable DB.
				mutation := ""
				switch mode {
				case "deleted cohort":
					mutation = `DELETE FROM engine.cluster_onboarding_targets WHERE operation_id=NEW.id;`
				case "substituted target":
					mutation = `SELECT * INTO target FROM engine.cluster_onboarding_targets WHERE id='` + id + `'; DELETE FROM engine.cluster_onboarding_targets WHERE id=target.id; target.id='` + newClusterUUID() + `'; INSERT INTO engine.cluster_onboarding_targets SELECT (target).*;`
				case "added target":
					mutation = `SELECT * INTO target FROM engine.cluster_onboarding_targets WHERE id='` + id + `'; target.id='` + newClusterUUID() + `'; target.ordinal=2; target.management_ip='192.0.2.199'; target.host_key_fingerprint=target.host_key_fingerprint||'-added'; INSERT INTO engine.cluster_onboarding_targets SELECT (target).*;`
				case "generation":
					mutation = `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id='` + id + `';`
				case "holder":
					mutation = `UPDATE engine.cluster_onboarding_targets SET lease_holder='` + newClusterUUID() + `' WHERE id='` + id + `';`
				case "expiry":
					value := "2"
					if phase == "claim" {
						value = "1"
					}
					mutation = `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=` + value + ` WHERE id='` + id + `';`
				case "report":
					mutation = `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id='` + id + `';`
				case "extended expiry":
					mutation = `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=lease_expires_at+86400 WHERE id='` + id + `';`
				case "credential":
					mutation = `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id='` + id + `';`
				case "payload":
					mutation = `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=NEW.id;`
				case "attempt":
					mutation = `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=NEW.id;`
				case "parent state":
					mutation = `UPDATE engine.cluster_operations SET state='waiting' WHERE id=NEW.id;`
				case "active pointer":
					mutation = `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1;`
				case "source":
					mutation = `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1;`
				case "source member":
					mutation = `UPDATE engine.cluster_nodes SET node_name=node_name||'-changed' WHERE id='` + snapshot.work.Proof.Source.Members[0].NodeID + `';`
				case "controller expiry":
					mutation = `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name='` + clusterControllerLeaseName + `';`
				case "credential expiry":
					mutation = `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id='` + id + `';`
				}
				f.exec(t, `CREATE OR REPLACE FUNCTION engine.test_preparation_recovery_drift() RETURNS trigger LANGUAGE plpgsql AS $body$
 DECLARE target engine.cluster_onboarding_targets%ROWTYPE;
 BEGIN IF pg_trigger_depth()=1 THEN `+mutation+` END IF; RETURN NEW; END; $body$`)
				f.exec(t, `CREATE TRIGGER test_preparation_recovery_drift AFTER UPDATE ON engine.cluster_operations
 FOR EACH ROW EXECUTE FUNCTION engine.test_preparation_recovery_drift()`)
				t.Cleanup(func() {
					f.exec(t, `DROP TRIGGER test_preparation_recovery_drift ON engine.cluster_operations`)
					f.exec(t, `DROP FUNCTION engine.test_preparation_recovery_drift()`)
				})
				before := sshPreparationRecoveryState(t, f)
				holder := ""
				if phase == "claim" {
					holder = newClusterUUID()
				}
				if f.c.commitClusterSSHPreparationRecovery(f.ctx, snapshot, holder) == nil {
					t.Fatal("post-write authority drift committed")
				}
				if sshPreparationRecoveryState(t, f) != before || f.c.store.db.Stats().InUse != 0 {
					t.Fatal("post-write rejection failed complete rollback/pool release")
				}
			})
		}
	}
}
