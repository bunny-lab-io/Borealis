package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"testing"
	"time"
)

func preparationLeaseCheckForFixture(f sshPreparationAuthorityFixture) func(context.Context) error {
	return newClusterSSHPreparationLeaseCheck(f.read, func(ctx context.Context) error {
		return f.c.store.renewClusterSSHPreparationTarget(ctx, f.lease, f.sealed)
	})
}

func TestClusterSSHPreparationScopePostgresRenewsAndFences(t *testing.T) {
	for _, mode := range []string{"keeps alive", "controller replaced", "target reclaimed", "credential changed", "credential expired", "Aegis locked", "other target missing", "source changed"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHPreparationAuthorityFixture(t)
			store := f.c.store
			store.db.SetMaxOpenConns(1)
			originalDeadline := time.Now().Unix() + 2
			f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=$2 WHERE id=$1`, f.lease.TargetID, originalDeadline)
			before := f.events(t)
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			started := make(chan struct{})
			release := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- runClusterSSHPreparationScope(ctx, 50*time.Millisecond, preparationLeaseCheckForFixture(f), func(workCtx context.Context) error {
					close(started)
					select {
					case <-release:
						return nil
					case <-workCtx.Done():
						return workCtx.Err()
					}
				})
			}()
			joined := false
			t.Cleanup(func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("preparation worker did not stop")
					}
				}
			})
			select {
			case <-started:
			case err := <-done:
				joined = true
				t.Fatalf("scope failed to start: %v", err)
			case <-ctx.Done():
				t.Fatal("scope start deadline")
			}
			// A one-connection pool must remain usable during blocked acquisition.
			var probe int
			if store.db.QueryRowContext(ctx, `SELECT 1`).Scan(&probe) != nil {
				t.Fatal("worker retained connection during acquisition")
			}
			switch mode {
			case "controller replaced":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='replacement-controller' WHERE name=$1`, clusterControllerLeaseName)
			case "target reclaimed":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_holder=$2,lease_generation=lease_generation+1 WHERE id=$1`, f.lease.TargetID, newClusterUUID())
			case "credential changed":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.lease.TargetID)
			case "credential expired":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=0 WHERE target_id=$1`, f.lease.TargetID)
			case "Aegis locked":
				f.aegis.mu.Lock()
				clear(f.aegis.key)
				f.aegis.key = nil
				f.aegis.mu.Unlock()
			case "other target missing":
				f.exec(t, `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, f.targets[1].Binding.TargetID)
			case "source changed":
				f.exec(t, `UPDATE engine.cluster_nodes SET application_state='draining' WHERE membership_state='Active'`)
			case "keeps alive":
				ticker := time.NewTicker(50 * time.Millisecond)
				defer ticker.Stop()
				for {
					var beyond bool
					if store.db.QueryRowContext(ctx, `SELECT extract(epoch FROM clock_timestamp())>=$1`, originalDeadline+1).Scan(&beyond) != nil {
						t.Fatal("database progress while acquiring")
					}
					if beyond {
						break
					}
					select {
					case <-ticker.C:
					case err := <-done:
						joined = true
						t.Fatalf("lease expired during acquisition: %v", err)
					case <-ctx.Done():
						t.Fatal("renewal wait deadline")
					}
				}
				var expiry int64
				if store.db.QueryRowContext(ctx, `SELECT lease_expires_at FROM engine.cluster_onboarding_targets WHERE id=$1`, f.lease.TargetID).Scan(&expiry) != nil || expiry <= originalDeadline+1 {
					t.Fatal("target lease did not advance")
				}
				close(release)
			}
			select {
			case err := <-done:
				joined = true
				if mode == "keeps alive" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err != clusterbootstrap.ErrSessionAuthority {
					t.Fatal("authority loss did not fence scope")
				}
			case <-ctx.Done():
				t.Fatal("scope did not cancel after authority loss")
			}
			if store.db.Stats().InUse != 0 || f.events(t) != before {
				t.Fatal("scope retained connection or changed operation events")
			}
			if _, err := store.claimClusterSSHTarget(f.ctx, f.lease.OperationID, f.lease.TargetID, newClusterUUID()); err == nil {
				t.Fatal("preparation scope opened claim gate")
			}
		})
	}
}

func TestClusterSSHPreparationRenewalPostgresBindsCiphertextAtCommit(t *testing.T) {
	f := newSSHPreparationAuthorityFixture(t)
	if f.c.store.renewClusterSSHTarget(f.ctx, f.lease) == nil || f.c.store.renewClusterSSHTargetGeneration(f.ctx, f.lease, f.sealed.generation) == nil {
		t.Fatal("legacy renewal bypassed preparation ciphertext fence")
	}
	envelope, err := f.aegis.openClusterSSHCredentials(f.ctx, f.sealed, f.sealed.binding)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Password = "replacement-credential"
	replacement, err := f.aegis.sealClusterSSHCredentials(f.ctx, envelope)
	if err != nil || replacement.generation != f.sealed.generation || replacement.ciphertext == f.sealed.ciphertext {
		t.Fatal("replacement fixture")
	}
	var expiry int64
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT lease_expires_at FROM engine.cluster_onboarding_targets WHERE id=$1`, f.lease.TargetID).Scan(&expiry) != nil {
		t.Fatal("expiry read")
	}
	// Replace after full authority read, immediately before renewal's locks.
	check := newClusterSSHPreparationLeaseCheck(f.read, func(ctx context.Context) error {
		f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=$2 WHERE target_id=$1`, f.lease.TargetID, replacement.ciphertext)
		return f.c.store.renewClusterSSHPreparationTarget(ctx, f.lease, f.sealed)
	})
	if check(f.ctx) != clusterbootstrap.ErrSessionAuthority {
		t.Fatal("same-generation ciphertext replacement renewed stale worker")
	}
	var after int64
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT lease_expires_at FROM engine.cluster_onboarding_targets WHERE id=$1`, f.lease.TargetID).Scan(&after) != nil || after != expiry {
		t.Fatal("rejected renewal changed expiry")
	}
}

func TestClusterSSHPreparationRenewalPostgresRechecksAfterLockWait(t *testing.T) {
	for _, mode := range []string{"ciphertext changed", "controller changed", "own lease expires", "canceled wait"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHPreparationAuthorityFixture(t)
			store := f.c.store
			store.db.SetMaxOpenConns(3)
			deadline := time.Now().Unix() + 2
			if mode == "own lease expires" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=$2 WHERE id=$1`, f.lease.TargetID, deadline)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			blocker, err := store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			var present int
			if err := blocker.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName).Scan(&present); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			joined := false
			go func() { done <- store.renewClusterSSHPreparationTarget(ctx, f.lease, f.sealed) }()
			t.Cleanup(func() {
				blocker.Rollback()
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("renewal did not stop")
					}
				}
			})
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%engine.cluster_application_leases%')`).Scan(&waiting) != nil {
					t.Fatal("lock wait observation")
				}
				if waiting {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("renewal did not reach controller lock")
				}
			}
			switch mode {
			case "ciphertext changed":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.lease.TargetID)
			case "controller changed":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`, clusterControllerLeaseName)
			case "canceled wait":
				cancel()
			case "own lease expires":
				for {
					var expired bool
					if store.db.QueryRowContext(ctx, `SELECT extract(epoch FROM clock_timestamp())>=$1`, deadline).Scan(&expired) != nil {
						t.Fatal("database clock")
					}
					if expired {
						break
					}
					select {
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal("expiry wait deadline")
					}
				}
			}
			if err != nil {
				t.Fatal("controller-first ordering blocked authority change")
			}
			if mode != "canceled wait" {
				if blocker.Commit() != nil {
					t.Fatal("blocker commit")
				}
			}
			select {
			case err := <-done:
				joined = true
				if err == nil {
					t.Fatal("stale renewal accepted after waiting")
				}
			case <-time.After(time.Second):
				t.Fatal("renewal did not return")
			}
		})
	}
}
