package main

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"
)

func newSSHPreparationRecoveryClaimFixture(t *testing.T) (sshPreparationAuthorityFixture, clusterSSHPreparationRecoveryClaim) {
	t.Helper()
	f, _ := newSSHPreparationRecoveryFixture(t, false)
	record, err := f.c.reserveClusterSSHPreparationRecovery(f.ctx, f.op)
	if err != nil {
		t.Fatal(err)
	}
	f.op.CurrentStep = clusterSSHPreparationRecoveryOperationStep
	claim, err := f.c.claimClusterSSHPreparationRecovery(f.ctx, f.op, record.ID, newClusterUUID())
	if err != nil || claim.custody == nil {
		t.Fatal("issued claim custody", err)
	}
	return f, claim
}

func TestClusterSSHPreparationRecoveryAuthorityPostgresPostWriteFence(t *testing.T) {
	for _, mode := range []string{"before expiry extension", "after expiry extension", "sibling generation", "parent payload", "deleted cohort", "credential deadline"} {
		t.Run(mode, func(t *testing.T) {
			f, issued := newSSHPreparationRecoveryClaimFixture(t)
			issued.custody.started.Store(true)
			claim, expected, err := freezeClusterSSHPreparationRecoveryClaim(issued)
			if err != nil {
				t.Fatal(err)
			}
			last := slices.Max([]string{claim.Claims[0].Lease.TargetID, claim.Claims[1].Lease.TargetID})
			moment := "AFTER"
			mutation := ""
			switch mode {
			case "before expiry extension":
				moment = "BEFORE"
				mutation = `NEW.lease_expires_at=NEW.lease_expires_at+86400;`
			case "after expiry extension":
				mutation = `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=lease_expires_at+86400 WHERE id=NEW.id;`
			case "sibling generation":
				mutation = `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE operation_id=NEW.operation_id;`
			case "parent payload":
				mutation = `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=NEW.operation_id;`
			case "deleted cohort":
				mutation = `DELETE FROM engine.cluster_onboarding_targets WHERE operation_id=NEW.operation_id;`
			case "credential deadline":
				mutation = `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=NEW.id;`
			}
			f.exec(t, `CREATE OR REPLACE FUNCTION engine.test_recovery_renewal_drift() RETURNS trigger LANGUAGE plpgsql AS $body$
 BEGIN IF pg_trigger_depth()=1 AND NEW.id='`+last+`' THEN `+mutation+` END IF; RETURN NEW; END; $body$`)
			f.exec(t, `CREATE TRIGGER test_recovery_renewal_drift `+moment+` UPDATE OF lease_expires_at ON engine.cluster_onboarding_targets
 FOR EACH ROW EXECUTE FUNCTION engine.test_recovery_renewal_drift()`)
			t.Cleanup(func() {
				f.exec(t, `DROP TRIGGER test_recovery_renewal_drift ON engine.cluster_onboarding_targets`)
				f.exec(t, `DROP FUNCTION engine.test_recovery_renewal_drift()`)
			})
			before := sshPreparationRecoveryState(t, f)
			if _, _, err := f.c.observeClusterSSHPreparationRecovery(f.ctx, claim, expected, true); err == nil {
				t.Fatal("post-write renewal drift accepted")
			}
			if before != sshPreparationRecoveryState(t, f) || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("failed renewal committed partial state or retained connection")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryAuthorityPostgresRenewsAndFences(t *testing.T) {
	for _, mode := range []string{"keeps alive", "controller", "source", "sibling", "ciphertext", "credential expiry", "Aegis locked", "parent phase", "payload", "foreign expiry", "expired claim", "initial authority"} {
		t.Run(mode, func(t *testing.T) {
			f, claim := newSSHPreparationRecoveryClaimFixture(t)
			f.c.store.db.SetMaxOpenConns(1)
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			original := maps.Clone(claim.custody.assigned)
			beforeEvents := f.events(t)
			if mode == "expired claim" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE id=$1`, claim.Claims[0].Lease.TargetID)
			}
			if mode == "initial authority" {
				claim.Claims[0].Lease.OperationStep = clusterSSHPreparationOperationStep
			}
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				done <- f.c.withClusterSSHPreparationRecoveryProofEvery(ctx, f.aegis, claim, 40*time.Millisecond,
					func(work context.Context, value clusterSSHPreparationRecoveryAuthority, check func(context.Context) error) error {
						if len(value.Leases) != 2 || value.Leases[0].Generation != claim.Reservation.Targets[0].Generation {
							return errClusterConflict
						}
						close(started)
						select {
						case <-release:
							return nil
						case <-work.Done():
							return work.Err()
						}
					})
			}()
			joined := false
			defer func() {
				cancel()
				if !joined {
					<-done
				}
			}()
			if mode == "expired claim" || mode == "initial authority" {
				result := <-done
				joined = true
				if result == nil {
					t.Fatal("invalid claim entered proof scope")
				}
				select {
				case <-started:
					t.Fatal("invalid claim reached consumer")
				default:
				}
				return
			}
			select {
			case <-started:
			case err := <-done:
				joined = true
				t.Fatal("scope entry", err)
			case <-ctx.Done():
				t.Fatal("entry deadline")
			}
			// Prove SQL connection is released while proof work waits.
			var one int
			if f.c.store.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one) != nil {
				t.Fatal("proof work retained connection")
			}
			sibling := claim.Claims[1].Lease.TargetID
			switch mode {
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='replacement-controller' WHERE name=$1`, clusterControllerLeaseName)
			case "source":
				f.exec(t, `UPDATE engine.cluster_nodes SET application_state='draining' WHERE membership_state='Active'`)
			case "sibling":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, sibling)
			case "ciphertext":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, sibling)
			case "credential expiry":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, sibling)
			case "Aegis locked":
				f.aegis.mu.Lock()
				clear(f.aegis.key)
				f.aegis.key = nil
				f.aegis.mu.Unlock()
			case "parent phase":
				f.exec(t, `UPDATE engine.cluster_operations SET current_step='prepare_ssh_targets' WHERE id=$1`, f.op.ID)
			case "payload":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
			case "foreign expiry":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=lease_expires_at+86400 WHERE id=$1`, sibling)
			case "keeps alive":
				tick := time.NewTicker(30 * time.Millisecond)
				defer tick.Stop()
				for {
					var expiry int64
					if f.c.store.db.QueryRowContext(ctx, `SELECT lease_expires_at FROM engine.cluster_onboarding_targets WHERE id=$1`, sibling).Scan(&expiry) != nil {
						t.Fatal("expiry read")
					}
					if expiry > original[sibling] {
						break
					}
					select {
					case <-tick.C:
					case err := <-done:
						joined = true
						t.Fatal("renewal failed", err)
					case <-ctx.Done():
						t.Fatal("renewal deadline")
					}
				}
				close(release)
			}
			select {
			case err := <-done:
				joined = true
				if (err == nil) != (mode == "keeps alive") {
					t.Fatal("scope authority outcome", err)
				}
			case <-ctx.Done():
				t.Fatal("scope failed to cancel/join")
			}
			if f.events(t) != beforeEvents || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("renewal published event or retained connection")
			}
			if !maps.Equal(claim.custody.assigned, original) {
				t.Fatal("renewal rewrote issued expiry history")
			}
			if f.c.withClusterSSHPreparationRecoveryProof(ctx, f.aegis, claim, func(context.Context, clusterSSHPreparationRecoveryAuthority, func(context.Context) error) error {
				t.Fatal("scope replayed")
				return nil
			}) == nil {
				t.Fatal("issued handle reused")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryAuthorityPostgresExactRenewal(t *testing.T) {
	f, issued := newSSHPreparationRecoveryClaimFixture(t)
	issued.custody.started.Store(true)
	claim, expected, err := freezeClusterSSHPreparationRecoveryClaim(issued)
	if err != nil {
		t.Fatal(err)
	}
	// Snapshot every credential/history field and operation/event state. Only
	// the existing target expiry may change during proof-scope renewal.
	const stable = `SELECT json_build_array(o.payload_json,o.state,o.current_step,o.updated_at,
 (SELECT json_agg(to_jsonb(t)-'lease_expires_at' ORDER BY t.id) FROM engine.cluster_onboarding_targets t WHERE t.operation_id=o.id),
 (SELECT json_agg(to_jsonb(p) ORDER BY p.target_id) FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id WHERE t.operation_id=o.id),
 (SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=o.id))::text FROM engine.cluster_operations o WHERE id=$1`
	var before, after string
	if f.c.store.db.QueryRowContext(f.ctx, stable, f.op.ID).Scan(&before) != nil {
		t.Fatal("history snapshot")
	}
	value, next, err := f.c.observeClusterSSHPreparationRecovery(f.ctx, claim, expected, true)
	if err != nil || !maps.Equal(value.ExpiresAt, next) {
		t.Fatal("exact renewal", err)
	}
	if f.c.store.db.QueryRowContext(f.ctx, stable, f.op.ID).Scan(&after) != nil || before != after {
		t.Fatal("renewal changed immutable history or credential deadline")
	}
	for id, expiry := range next {
		var remaining float64
		if f.c.store.db.QueryRowContext(f.ctx, `SELECT $1::bigint-extract(epoch FROM clock_timestamp())`, expiry).Scan(&remaining) != nil || remaining <= 0 || remaining > 45 {
			t.Fatal("renewal exceeded fixed lease bound", id)
		}
	}
	wrong := maps.Clone(next)
	wrong[claim.Claims[1].Lease.TargetID]++
	before = sshPreparationRecoveryState(t, f)
	if _, _, err := f.c.observeClusterSSHPreparationRecovery(f.ctx, claim, wrong, true); err == nil || before != sshPreparationRecoveryState(t, f) {
		t.Fatal("foreign expiry accepted or partial renewal committed")
	}
}
