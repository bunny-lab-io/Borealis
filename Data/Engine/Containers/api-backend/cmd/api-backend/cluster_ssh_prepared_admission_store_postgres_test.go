package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func assertPreparedAdmissionStoreState(t *testing.T, f sshPreparationAuthorityFixture, before string, success bool) {
	t.Helper()
	after, err := preparedCohortSQLState(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if !success {
		if before != after {
			t.Fatal("rejected transition changed retained rows or events")
		}
		return
	}
	var prior, current []json.RawMessage
	if json.Unmarshal([]byte(before), &prior) != nil || json.Unmarshal([]byte(after), &current) != nil || len(prior) != 7 || len(current) != 7 {
		t.Fatal("invalid full row oracle")
	}
	for i := 1; i < 6; i++ {
		if string(prior[i]) != string(current[i]) {
			t.Fatal("transition changed target/source/credential/reservation custody", i)
		}
	}
	var oldOp, newOp map[string]any
	_ = json.Unmarshal(prior[0], &oldOp)
	_ = json.Unmarshal(current[0], &newOp)
	if newOp["current_step"] != "apply_membership" || newOp["kind"] != "ssh_onboarding" || newOp["state"] != "running" {
		t.Fatal("wrong transition")
	}
	oldOp["current_step"], oldOp["updated_at"] = newOp["current_step"], newOp["updated_at"]
	if !reflect.DeepEqual(oldOp, newOp) {
		t.Fatal("transition changed immutable operation payload or identity")
	}
	var oldEvents, newEvents []map[string]any
	_ = json.Unmarshal(prior[6], &oldEvents)
	_ = json.Unmarshal(current[6], &newEvents)
	if len(newEvents) != len(oldEvents)+1 || !reflect.DeepEqual(oldEvents, newEvents[:len(oldEvents)]) {
		t.Fatal("transition changed prior events or duplicated event")
	}
	last := newEvents[len(newEvents)-1]
	if last["event_type"] != "ssh_prepared_admission_started" || last["message"] != "Prepared cohort entered admission." {
		t.Fatal("misleading admission event")
	}
}

func TestClusterSSHPreparedAdmissionStorePostgresTransition(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "payload", "attempt", "phase", "kind", "controller", "controller expired", "credential", "credential expired", "Aegis", "target generation", "target lease", "target missing", "source member", "source queue", "baseline", "observation old", "observation future", "unbounded", "cancelled", "mutated original"} {
		t.Run(mode, func(t *testing.T) {
			f, _, _ := newSSHPreparedAuthorityFixture(t, mode == "replacement")
			a, now, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
			if err != nil {
				t.Fatal(err)
			}
			observed := preparedAdmissionStoreObservation(a, f.op, f.c.holder, now)
			ctx, cancel := context.WithTimeout(f.ctx, 60*time.Second)
			defer cancel()
			switch mode {
			case "payload":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
			case "attempt":
				f.exec(t, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, f.op.ID)
			case "phase":
				f.exec(t, `UPDATE engine.cluster_operations SET current_step='apply_membership' WHERE id=$1`, f.op.ID)
			case "kind":
				f.exec(t, `UPDATE engine.cluster_operations SET kind='membership_admit' WHERE id=$1`, f.op.ID)
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other' WHERE name=$1`, clusterControllerLeaseName)
			case "controller expired":
				f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=0 WHERE name=$1`, clusterControllerLeaseName)
			case "credential":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.targets[0].Binding.TargetID)
			case "credential expired":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=0 WHERE target_id=$1`, f.targets[0].Binding.TargetID)
			case "Aegis":
				f.exec(t, `UPDATE engine.aegis_cipher_state SET verification_token='changed' WHERE id=1`)
			case "target generation":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, f.targets[0].Binding.TargetID)
			case "target lease":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_holder='other' WHERE id=$1`, f.targets[0].Binding.TargetID)
			case "target missing":
				f.exec(t, `DELETE FROM engine.cluster_onboarding_targets WHERE id=$1`, f.targets[0].Binding.TargetID)
			case "source member":
				f.exec(t, `UPDATE engine.cluster_nodes SET application_state='drained' WHERE id=$1`, a.proof.Source.Members[0].NodeID)
			case "source queue":
				f.exec(t, `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1`)
			case "baseline":
				f.exec(t, `UPDATE engine.cluster_state SET baseline_sha=$1 WHERE id=1`, strings.Repeat("9", 40))
			case "observation old":
				observed.ObservedAt -= 60
			case "observation future":
				observed.ObservedAt += 60
			case "unbounded":
				ctx = context.Background()
			case "cancelled":
				cancel()
			case "mutated original":
				a.receipt.Targets[0].Prejoin.Receipt.MemberID++
			}
			before, err := preparedCohortSQLState(f.ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			err = f.c.beginClusterSSHPreparedAdmission(ctx, f.op, a, observed)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success {
				t.Fatal("wrong transaction outcome", err)
			}
			assertPreparedAdmissionStoreState(t, f, before, success)
			if !success {
				return
			}
			committed, err := preparedCohortSQLState(f.ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			if f.c.beginClusterSSHPreparedAdmission(ctx, f.op, a, observed) == nil {
				t.Fatal("replayed admission transition")
			}
			assertPreparedAdmissionStoreState(t, f, committed, false)
			if _, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op); err == nil {
				t.Fatal("stale caller phase accepted")
			}
			for _, phase := range []string{"apply_membership", "verify_quorum"} {
				if phase == "verify_quorum" {
					f.exec(t, `UPDATE engine.cluster_operations SET current_step=$2 WHERE id=$1`, f.op.ID, phase)
				}
				op := f.op
				op.CurrentStep = phase
				got, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, op)
				if err != nil || !reflect.DeepEqual(got, a) {
					t.Fatal("lost exact settled custody in admission phase", phase, err)
				}
				if _, err := f.c.claimClusterSSHPreparation(f.ctx, op, newClusterUUID()); err == nil {
					t.Fatal("admission phase regained preparation claim")
				}
			}
			f.op.CurrentStep = "apply_membership"
			if _, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op); err == nil {
				t.Fatal("caller relabeled stored verify phase")
			}
		})
	}
}

func TestClusterSSHPreparedAdmissionStorePostgresLockWait(t *testing.T) {
	for _, mode := range []string{"controller expiry", "credential expiry", "proof expiry", "cancel", "credential drift", "payload drift", "event wait"} {
		t.Run(mode, func(t *testing.T) {
			f, _, _ := newSSHPreparedAuthorityFixture(t, false)
			a, now, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			observed := preparedAdmissionStoreObservation(a, f.op, f.c.holder, now)
			f.c.store.db.SetMaxOpenConns(4)
			if mode == "controller expiry" {
				f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=floor(extract(epoch FROM clock_timestamp()))::bigint+2 WHERE name=$1`, clusterControllerLeaseName)
			}
			if mode == "credential expiry" {
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=floor(extract(epoch FROM clock_timestamp()))::bigint+2 WHERE target_id=$1`, f.targets[0].Binding.TargetID)
			}
			if mode == "proof expiry" || mode == "event wait" {
				observed.ObservedAt -= 58
			}
			lock, err := f.c.store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback()
			pattern := "%engine.cluster_operations%FOR UPDATE%"
			if mode == "event wait" {
				_, err = lock.ExecContext(ctx, `LOCK TABLE engine.cluster_operation_events IN SHARE MODE`)
				pattern = "%INSERT INTO engine.cluster_operation_events%"
			} else {
				_, err = lock.ExecContext(ctx, `SELECT 1 FROM engine.cluster_operations WHERE id=$1 FOR UPDATE`, f.op.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := preparedCohortSQLState(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- f.c.beginClusterSSHPreparedAdmission(ctx, f.op, a, observed) }()
			for {
				var waiting bool
				if err := f.c.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatal("transition bypassed held lock", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			switch mode {
			case "cancel":
				cancel()
			case "credential drift":
				if _, err := lock.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.targets[0].Binding.TargetID); err != nil {
					t.Fatal(err)
				}
			case "payload drift":
				if _, err := lock.ExecContext(ctx, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID); err != nil {
					t.Fatal(err)
				}
			default:
				for {
					var expired bool
					query, args := `SELECT extract(epoch FROM clock_timestamp()) >= $1::bigint+60`, []any{observed.ObservedAt}
					if mode == "controller expiry" {
						query, args = `SELECT expires_at<=extract(epoch FROM clock_timestamp()) FROM engine.cluster_application_leases WHERE name=$1`, []any{clusterControllerLeaseName}
					}
					if mode == "credential expiry" {
						query, args = `SELECT expires_at<=extract(epoch FROM clock_timestamp()) FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, []any{f.targets[0].Binding.TargetID}
					}
					if err := f.c.store.db.QueryRowContext(ctx, query, args...).Scan(&expired); err != nil {
						t.Fatal(err)
					}
					if expired {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(5 * time.Millisecond):
					}
				}
			}
			if mode == "cancel" {
				_ = lock.Rollback()
			} else if err := lock.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("late fence allowed transition")
				}
			case <-f.ctx.Done():
				t.Fatal(f.ctx.Err())
			}
			if mode == "credential drift" || mode == "payload drift" {
				// Compare every retained row against the deliberate fault, excluding
				// only that field; the transaction must not add an event or step.
				after, err := preparedCohortSQLState(f.ctx, f)
				if err != nil {
					t.Fatal(err)
				}
				var left, right []any
				if json.Unmarshal([]byte(before), &left) != nil || json.Unmarshal([]byte(after), &right) != nil {
					t.Fatal("invalid late fault oracle")
				}
				if mode == "payload drift" {
					op := left[0].(map[string]any)
					op["payload_json"] = op["payload_json"].(string) + " "
				} else {
					for _, value := range left[3].([]any) {
						credential := value.(map[string]any)
						if credential["target_id"] == f.targets[0].Binding.TargetID {
							credential["ciphertext"] = credential["ciphertext"].(string) + "changed"
						}
					}
				}
				if !reflect.DeepEqual(left, right) {
					t.Fatal("rejected late fault changed retained rows or events")
				}
			} else {
				assertPreparedAdmissionStoreState(t, f, before, false)
			}
		})
	}
}

func TestClusterSSHPreparedAdmissionStorePostgresProofCleanup(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "blocked closure", "failed closure"} {
		t.Run(mode, func(t *testing.T) {
			f, a, targets := newSSHPreparedAuthorityFixture(t, mode == "replacement")
			original, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.WithoutCancel(f.ctx), 60*time.Second)
			defer cancel()
			f.c.store.db.SetMaxOpenConns(1)
			before, err := preparedCohortSQLState(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			n := newPreparedMembershipNativeForProof(t, a, original, targets, "", clusterbootstrap.MembershipQuorumProofVersion)
			n.before = func(ctx context.Context) error { return recoveryProgressSQLPoolAvailable(ctx, f) }
			closing, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			n.closeHook = func(ctx context.Context, post int) error {
				if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
					return err
				}
				if post == 12 && (mode == "blocked closure" || mode == "failed closure") {
					close(closing)
					<-release
					if mode == "failed closure" {
						return errors.New("synthetic final close failure")
					}
				}
				return nil
			}
			f.c.runner = n.runner
			done := make(chan error, 1)
			go func() {
				var observed clusterSSHPreparedObservation
				err := f.c.withClusterSSHPreparedAdmissionProof(ctx, f.op, func(_ context.Context, value clusterSSHPreparedCohortMembershipObservation, _ clusterSSHPreparedMembershipChecks) error {
					observed = value.Prepared
					return nil
				})
				if err == nil {
					if n.active.Load() != 0 || n.posts.Load() != 12 {
						done <- errors.New("proof returned before final cleanup")
						return
					}
					err = f.c.beginClusterSSHPreparedAdmission(ctx, f.op, original, observed)
				}
				done <- err
			}()
			if mode == "blocked closure" || mode == "failed closure" {
				select {
				case <-closing:
				case err := <-done:
					t.Fatal("returned before closure", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				select {
				case err := <-done:
					t.Fatal("transition escaped blocked cleanup", err)
				case <-time.After(20 * time.Millisecond):
				}
				assertPreparedAdmissionStoreState(t, f, before, false)
				once.Do(func() { close(release) })
			}
			select {
			case err = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			success := mode != "failed closure"
			if (err == nil) != success {
				t.Fatal("wrong joined proof/transition result", err)
			}
			assertPreparedAdmissionStoreState(t, f, before, success)
			if n.active.Load() != 0 || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("proof/transaction retained resources")
			}
		})
	}
}
