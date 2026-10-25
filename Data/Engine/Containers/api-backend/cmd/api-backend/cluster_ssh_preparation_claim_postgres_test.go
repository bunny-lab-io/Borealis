package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func newSSHPreparationClaimFixture(t *testing.T, replacement bool) (sshPreparationAuthorityFixture, clusterSSHPreparationAuthorization) {
	t.Helper()
	f := newSSHPreparationAuthorityFixtureForTopology(t, replacement)
	var raw string
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&raw) != nil {
		t.Fatal("read payload")
	}
	proof, _, err := parseClusterSSHPreparationInspection([]byte(raw), f.lease, f.baseline)
	if err != nil {
		t.Fatal(err)
	}
	source, err := readClusterSSHQueueSource(f.ctx, f.c.store.db)
	if err != nil {
		t.Fatal(err)
	}
	report := newClusterSSHQualificationReport(f.op.Attempt)
	for i := 0; i < len(report.Checks)-1; i++ {
		report.Checks[i].State = "passed"
	}
	authorization := sshPreparationAuthorizationFixture(t, proof, source, &report)
	for i, target := range f.targets {
		authorization.Targets[i].CredentialSHA256, err = clusterSSHPreparationDigest([]string{target.Sealed.generation, target.Sealed.ciphertext})
		if err != nil {
			t.Fatal(err)
		}
	}
	var payload map[string]any
	if json.Unmarshal([]byte(raw), &payload) != nil {
		t.Fatal("decode payload")
	}
	payload["ssh_qualification"], payload["ssh_preparation_authorization"] = report, authorization
	f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(payload))
	for _, target := range authorization.Targets {
		f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='queued',current_step='qualification_complete',
 lease_holder='',lease_expires_at=0,lease_generation=$2 WHERE id=$1`, target.TargetID, target.QualificationGeneration)
	}
	f.op.State, f.op.CurrentStep, f.op.Payload = "running", clusterSSHPreparationOperationStep, payload
	return f, authorization
}

func TestClusterSSHPreparationClaimPostgresAtomicCohort(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "expansion", true: "replacement"}[replacement], func(t *testing.T) {
			f, authorization := newSSHPreparationClaimFixture(t, replacement)
			before := f.events(t)
			holder := newClusterUUID()
			work, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, holder)
			if err != nil || len(work.Claims) != len(f.targets) {
				t.Fatal("claim failed", err)
			}
			if f.c.store.db.Stats().InUse != 0 || f.events(t) != before {
				t.Fatal("claim retained connection or emitted phase event")
			}
			for i, claim := range work.Claims {
				if !validClusterSSHPreparationLease(claim.Lease) || claim.Lease.TargetID != f.targets[i].Binding.TargetID ||
					claim.Lease.Generation != authorization.Targets[i].QualificationGeneration+1 || claim.Lease.Holder != holder ||
					claim.Sealed != f.targets[i].Sealed {
					t.Fatal("claim changed ordering, generation or original credentials")
				}
				if _, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, claim.Lease, work.Baseline, claim.Sealed)(f.ctx); err != nil {
					t.Fatal("claim does not feed guarded authority", err)
				}
			}
			if _, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, holder); err == nil {
				t.Fatal("lost acknowledgement replay obtained claims")
			}
			f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
			if _, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID()); err == nil {
				t.Fatal("uncertain expired mutation reclaimed without reconciliation")
			}
		})
	}
	t.Run("concurrent contenders", func(t *testing.T) {
		f, _ := newSSHPreparationClaimFixture(t, false)
		f.c.store.db.SetMaxOpenConns(4)
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				_, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID())
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
			t.Fatal("expected one whole-cohort owner", success)
		}
	})
}

func TestClusterSSHPreparationClaimPostgresRejectsDrift(t *testing.T) {
	for _, mode := range []string{"missing authorization", "qualification phase", "controller", "generation", "report", "credential", "ciphertext", "aegis", "source", "missing sibling", "stale qualification", "existing claim"} {
		t.Run(mode, func(t *testing.T) {
			f, authorization := newSSHPreparationClaimFixture(t, false)
			// Mutate the target that sorts last so earlier updates must roll back.
			last := authorization.Targets[0].TargetID
			if authorization.Targets[1].TargetID > last {
				last = authorization.Targets[1].TargetID
			}
			switch mode {
			case "missing authorization":
				delete(f.op.Payload, "ssh_preparation_authorization")
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(f.op.Payload))
			case "qualification phase":
				f.exec(t, `UPDATE engine.cluster_operations SET state='waiting',current_step='qualify_ssh_targets' WHERE id=$1`, f.op.ID)
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='replacement-controller' WHERE name=$1`, clusterControllerLeaseName)
			case "generation":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, last)
			case "report":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, last)
			case "credential":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, last)
			case "ciphertext":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, last)
			case "aegis":
				f.exec(t, `UPDATE engine.aegis_cipher_state SET verification_token='changed' WHERE id=1`)
			case "source":
				f.exec(t, `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1`)
			case "missing sibling":
				f.exec(t, `DELETE FROM engine.cluster_onboarding_targets WHERE id=$1`, last)
			case "stale qualification":
				report := f.op.Payload["ssh_qualification"].(clusterSSHQualificationReport)
				report.ObservedAt -= clusterSSHInspectionLifetimeSeconds
				authorization.QualificationSHA256, _ = clusterSSHPreparationDigest(report)
				f.op.Payload["ssh_qualification"], f.op.Payload["ssh_preparation_authorization"] = report, authorization
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(f.op.Payload))
			case "existing claim":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='running',current_step='stage_source',lease_holder=$2,lease_expires_at=1 WHERE id=$1`, last, newClusterUUID())
			}
			holder := newClusterUUID()
			if _, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, holder); err == nil {
				t.Fatal("drift accepted")
			}
			var count int
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND lease_holder=$2`, f.op.ID, holder).Scan(&count) != nil || count != 0 {
				t.Fatal("partial claim committed")
			}
		})
	}
}

func TestClusterSSHPreparationClaimPostgresRechecksAfterWait(t *testing.T) {
	for _, mode := range []string{"payload", "source", "controller expiry", "credential expiry", "ciphertext", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newSSHPreparationClaimFixture(t, false)
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
			holder := newClusterUUID()
			go func() { _, err := f.c.claimClusterSSHPreparation(ctx, f.op, holder); done <- err }()
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
					t.Fatal("claim did not reach lock wait")
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
			if err != nil || (mode != "cancel" && blocker.Commit() != nil) {
				t.Fatal("authority mutation failed", err)
			}
			if err := <-done; err == nil {
				t.Fatal("claim survived changed authority")
			}
			joined = true
			var count int
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND lease_holder=$2`, f.op.ID, holder).Scan(&count) != nil || count != 0 {
				t.Fatal("claim partially committed after lock wait")
			}
		})
	}
}
