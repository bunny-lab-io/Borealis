package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestClusterSSHPreparationRecoveryScopePostgresOneUseAndCopies(t *testing.T) {
	for _, mode := range []string{"same-second copy", "caller mutation", "canceled entry", "invalid entry", "ignored check failure"} {
		t.Run(mode, func(t *testing.T) {
			f, claim := newSSHPreparationRecoveryClaimFixture(t)
			ctx, cancel := context.WithTimeout(f.ctx, 6*time.Second)
			defer cancel()
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var once sync.Once
			defer once.Do(func() { close(release) })
			var escaped func(context.Context) error
			consume := func(work context.Context, value clusterSSHPreparationRecoveryAuthority, check func(context.Context) error) error {
				escaped = check
				value.Reservation.Targets[0].PreviousHolder = "consumer changed"
				value.Cohort.Targets[0].Key.PublicKey[0] ^= 1
				close(started)
				select {
				case <-release:
					if mode == "ignored check failure" {
						_ = check(work)
						return nil
					}
					return check(work)
				case <-work.Done():
					return work.Err()
				}
			}
			if mode == "canceled entry" {
				cancel()
			}
			if mode == "invalid entry" {
				consume = nil
			}
			go func() {
				done <- f.c.withClusterSSHPreparationRecoveryProofEvery(ctx, f.aegis, claim, 50*time.Millisecond, consume)
			}()
			joined := false
			defer func() {
				cancel()
				once.Do(func() { close(release) })
				if !joined {
					<-done
				}
			}()
			if mode == "canceled entry" || mode == "invalid entry" {
				result := <-done
				joined = true
				if result == nil || !claim.custody.failed.Load() {
					t.Fatal("failed entry reusable")
				}
			} else {
				select {
				case <-started:
				case err := <-done:
					joined = true
					t.Fatal(err)
				case <-ctx.Done():
					t.Fatal("entry deadline")
				}
				copy := claim
				if f.c.withClusterSSHPreparationRecoveryProof(ctx, f.aegis, copy, func(context.Context, clusterSSHPreparationRecoveryAuthority, func(context.Context) error) error {
					t.Fatal("duplicate scope entered")
					return nil
				}) == nil {
					t.Fatal("same-second duplicate admitted")
				}
				if claim.custody.failed.Load() {
					t.Fatal("duplicate entry poisoned original legitimate owner")
				}
				if mode == "caller mutation" {
					claim.Reservation.Targets[0].PreviousHolder = "caller changed"
					claim.Claims[0].Sealed.ciphertext += "changed"
					claim.custody.assigned[claim.Claims[0].Lease.TargetID] = 1
				}
				if mode == "ignored check failure" {
					f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, claim.Claims[1].Lease.TargetID)
				}
				once.Do(func() { close(release) })
				err := <-done
				joined = true
				if (err != nil) != (mode == "ignored check failure") {
					t.Fatal("frozen scope changed", err)
				}
				if escaped(context.Background()) == nil {
					t.Fatal("escaped check outlived scope")
				}
			}
			if f.c.withClusterSSHPreparationRecoveryProof(f.ctx, f.aegis, claim, func(context.Context, clusterSSHPreparationRecoveryAuthority, func(context.Context) error) error {
				t.Fatal("closed/failed scope replayed")
				return nil
			}) == nil {
				t.Fatal("issued claim reused")
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("scope retained SQL connection")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryScopePostgresJoinsCancellation(t *testing.T) {
	for _, mode := range []string{"parent cancellation", "heartbeat loss", "escaped check"} {
		t.Run(mode, func(t *testing.T) {
			f, claim := newSSHPreparationRecoveryClaimFixture(t)
			f.c.store.db.SetMaxOpenConns(4)
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			started, stopping, cleanup, finish := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			checks := make(chan func(context.Context) error, 1)
			done := make(chan error, 1)
			var once sync.Once
			defer once.Do(func() { close(cleanup) })
			interval := 30 * time.Millisecond
			if mode == "escaped check" {
				interval = 15 * time.Second
			}
			go func() {
				done <- f.c.withClusterSSHPreparationRecoveryProofEvery(ctx, f.aegis, claim, interval, func(work context.Context, _ clusterSSHPreparationRecoveryAuthority, check func(context.Context) error) error {
					checks <- check
					close(started)
					if mode == "escaped check" {
						select {
						case <-finish:
							return nil
						case <-work.Done():
							return work.Err()
						}
					}
					<-work.Done()
					close(stopping)
					<-cleanup
					return work.Err()
				})
			}()
			joined := false
			defer func() {
				cancel()
				once.Do(func() { close(cleanup) })
				if !joined {
					<-done
				}
			}()
			select {
			case <-started:
			case err := <-done:
				joined = true
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatal("entry")
			}
			if mode == "parent cancellation" {
				cancel()
			}
			if mode == "heartbeat loss" {
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, claim.Claims[1].Lease.TargetID)
			}
			if mode != "escaped check" {
				select {
				case <-stopping:
				case <-time.After(4 * time.Second):
					t.Fatal("consumer not canceled")
				}
				select {
				case <-done:
					joined = true
					t.Fatal("scope returned before owned cleanup joined")
				default:
				}
				once.Do(func() { close(cleanup) })
			} else {
				blocker, err := f.c.store.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer blocker.Rollback()
				if _, err := blocker.ExecContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName); err != nil {
					t.Fatal(err)
				}
				checked := make(chan error, 1)
				check := <-checks
				go func() { checked <- check(context.Background()) }()
				waitSSHPreparationRecoveryControllerLock(t, ctx, f)
				close(finish)
				select {
				case err := <-checked:
					if err == nil {
						t.Fatal("escaped check succeeded after closure")
					}
				case <-time.After(4 * time.Second):
					t.Fatal("escaped SQL callback not joined")
				}
				blocker.Rollback()
			}
			select {
			case err := <-done:
				joined = true
				if err == nil {
					t.Fatal("canceled/lost scope succeeded")
				}
			case <-time.After(4 * time.Second):
				t.Fatal("scope did not join")
			}
			if !claim.custody.failed.Load() || f.c.store.db.Stats().InUse != 0 {
				t.Fatal("failure not poisoned or connection retained")
			}
		})
	}
}

func waitSSHPreparationRecoveryControllerLock(t *testing.T, ctx context.Context, f sshPreparationAuthorityFixture) {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		if f.c.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%engine.cluster_application_leases%')`).Scan(&waiting) != nil {
			t.Fatal("lock observation")
		}
		if waiting {
			return
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal("controller wait deadline")
		}
	}
}

func TestClusterSSHPreparationRecoveryAuthorityPostgresRechecksAfterWait(t *testing.T) {
	for _, mode := range []string{"controller", "ciphertext", "sibling", "source", "expired claim", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f, issued := newSSHPreparationRecoveryClaimFixture(t)
			issued.custody.started.Store(true)
			claim, expected, err := freezeClusterSSHPreparationRecoveryClaim(issued)
			if err != nil {
				t.Fatal(err)
			}
			f.c.store.db.SetMaxOpenConns(4)
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
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
			go func() { _, _, err := f.c.observeClusterSSHPreparationRecovery(ctx, claim, expected, true); done <- err }()
			defer func() {
				blocker.Rollback()
				cancel()
				if !joined {
					<-done
				}
			}()
			waitSSHPreparationRecoveryControllerLock(t, ctx, f)
			sibling := claim.Claims[1].Lease.TargetID
			switch mode {
			case "controller":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`, clusterControllerLeaseName)
			case "ciphertext":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, sibling)
			case "sibling":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, sibling)
			case "source":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_nodes SET application_state='draining' WHERE membership_state='Active'`)
			case "expired claim":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE id=$1`, sibling)
			case "cancel":
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode != "cancel" && blocker.Commit() != nil {
				t.Fatal("unlock")
			}
			result := <-done
			joined = true
			if result == nil {
				t.Fatal("changed authority renewed after wait")
			}
			if mode == "cancel" {
				blocker.Rollback()
			}
			for id, expiry := range expected {
				if mode == "expired claim" && id == sibling {
					expiry = 1
				}
				var after int64
				if f.c.store.db.QueryRowContext(f.ctx, `SELECT lease_expires_at FROM engine.cluster_onboarding_targets WHERE id=$1`, id).Scan(&after) != nil || after != expiry {
					t.Fatal("partial renewal committed")
				}
			}
		})
	}
}
