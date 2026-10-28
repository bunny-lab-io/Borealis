package main

import (
	"context"
	"testing"
	"time"
)

func TestClusterSSHWorkerPostgresAuthorityLocksAndPhaseClaims(t *testing.T) {
	for _, boundary := range []string{"operation change", "controller transition lock order"} {
		for _, action := range []string{"claim", "renew", "load credential", "complete"} {
			t.Run(boundary+"/"+action, func(t *testing.T) {
				store, _, ctx, operationID, targets, _ := clusterSSHCredentialsFixture(t)
				insertSSHFixtureTargets(t, store, ctx, operationID, targets)
				target := targets[0]
				var lease clusterSSHTargetLease
				if action != "claim" {
					var err error
					lease, err = store.claimClusterSSHTarget(ctx, operationID, target.Binding.TargetID, newClusterUUID())
					if err != nil {
						t.Fatal(err)
					}
				}
				var before string
				const retained = `SELECT json_build_array(lease_holder,lease_generation,lease_expires_at,state,inspection_json,inspected_at)::text
 FROM engine.cluster_onboarding_targets WHERE id=$1`
				if err := store.db.QueryRowContext(ctx, retained, target.Binding.TargetID).Scan(&before); err != nil {
					t.Fatal(err)
				}
				// Three connections: controller transition, worker and read-only lock
				// observer. Every other SSH fixture retains its one-connection proof.
				store.db.SetMaxOpenConns(3)
				blocked, err := store.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer blocked.Rollback()
				watch := "%engine.cluster_operations%"
				if boundary == "operation change" {
					_, err = blocked.ExecContext(ctx, `UPDATE engine.cluster_operations SET state='waiting' WHERE id=$1`, operationID)
				} else {
					watch = "%engine.cluster_application_leases%"
					var present int
					err = blocked.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName).Scan(&present)
				}
				if err != nil {
					t.Fatal(err)
				}
				workerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				done := make(chan error, 1)
				consumed := false
				t.Cleanup(func() {
					blocked.Rollback()
					cancel()
					if !consumed {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("blocked worker did not exit")
						}
					}
				})
				host, privileged := sshWorkerFacts()
				report, err := clusterSSHReport(host, privileged, target.Binding.Address)
				if err != nil {
					t.Fatal(err)
				}
				go func() {
					var err error
					switch action {
					case "claim":
						_, err = store.claimClusterSSHTarget(workerCtx, operationID, target.Binding.TargetID, newClusterUUID())
					case "renew":
						err = store.renewClusterSSHTargetGeneration(workerCtx, lease, target.Sealed.generation)
					case "load credential":
						_, err = store.loadClusterSSHTargetCredentials(workerCtx, lease)
					case "complete":
						err = store.completeClusterSSHInspection(workerCtx, lease, target.Sealed.generation, report)
					}
					done <- err
				}()
				// Observe an actual PostgreSQL lock wait, not a timing-only assertion.
				poll := time.NewTicker(10 * time.Millisecond)
				defer poll.Stop()
				for {
					select {
					case <-done:
						consumed = true
						t.Fatal("worker completed while parent authority transition was uncommitted")
					case <-workerCtx.Done():
						t.Fatal("worker did not reach authority lock boundary")
					case <-poll.C:
					}
					var waiting bool
					if err := store.db.QueryRowContext(workerCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, watch).Scan(&waiting); err != nil {
						t.Fatal(err)
					}
					if waiting {
						break
					}
				}
				if boundary == "controller transition lock order" {
					// Controller owns lease first, then operation. A worker that acquired
					// operation SHARE before lease SHARE would deadlock this transition.
					if _, err := blocked.ExecContext(workerCtx, `UPDATE engine.cluster_operations SET state='waiting' WHERE id=$1`, operationID); err != nil {
						t.Fatal("worker lock order prevented controller transition")
					}
				}
				if err := blocked.Commit(); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					consumed = true
					if err == nil {
						t.Fatal("worker reused stale authority after parent transition")
					}
				case <-workerCtx.Done():
					t.Fatal("worker did not reconcile changed parent authority")
				}
				var after string
				if err := store.db.QueryRowContext(ctx, retained, target.Binding.TargetID).Scan(&after); err != nil || before != after {
					t.Fatal("rejected action changed retained target evidence")
				}
				var events int
				if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type='ssh_target_inspected'`, operationID).Scan(&events); err != nil || events != 0 {
					t.Fatal("rejected completion published success")
				}
			})
		}
	}
}
