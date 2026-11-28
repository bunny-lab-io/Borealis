package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func sshNetworkClaimsPostgresFixture(t *testing.T, replacement bool) (sshPreparationAuthorityFixture, []clusterSSHNetworkTargetClaim) {
	t.Helper()
	f := newSSHPreparationAuthorityFixtureForTopology(t, replacement)
	a, err := f.read(f.ctx)
	if err != nil {
		t.Fatal("initial authority")
	}
	claims := make([]clusterSSHNetworkTargetClaim, len(a.Cohort.Targets))
	for i, item := range a.Cohort.Targets {
		lease := f.lease
		if i > 0 {
			lease.TargetID, lease.Holder, lease.Generation = item.Binding.TargetID, newClusterUUID(), item.Generation+1
			// Reserved preparation claims exist only in this isolated fixture.
			// Production claiming/phase transitions remain disabled.
			f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='running',lease_holder=$2,lease_generation=$3,lease_expires_at=$4 WHERE id=$1`, lease.TargetID, lease.Holder, lease.Generation, time.Now().Unix()+300)
		}
		claims[i] = clusterSSHNetworkTargetClaim{Lease: lease, Sealed: f.targets[i].Sealed}
	}
	return f, claims
}

func TestClusterSSHNetworkTargetsPostgresOriginalClaimsAndPoolRelease(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "first crypto", "second crypto"} {
		t.Run(mode, func(t *testing.T) {
			f, claims := sshNetworkClaimsPostgresFixture(t, mode == "replacement")
			store := f.c.store
			store.db.SetMaxOpenConns(1)
			before := f.events(t)
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			if mode == "expansion" || mode == "replacement" {
				deadline := time.Now().Unix() + 2
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=$2 WHERE operation_id=$1`, f.lease.OperationID, deadline)
				err := withClusterSSHNetworkTargets(ctx, store, f.aegis, f.baseline, claims, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
					a, err := readers.Authority(ctx)
					if err != nil || a.Lease != claims[0].Lease || len(a.Cohort.Targets) != len(claims) {
						return errors.New("complete original authority")
					}
					// The one-connection pool stays available while the consumer
					// waits past both original deadlines under joined heartbeats.
					ticker := time.NewTicker(50 * time.Millisecond)
					defer ticker.Stop()
					for {
						var expired bool
						if store.db.QueryRowContext(ctx, `SELECT extract(epoch FROM clock_timestamp())>$1`, deadline).Scan(&expired) != nil {
							return errors.New("pool unavailable")
						}
						if expired {
							return readers.Refresh(ctx)
						}
						select {
						case <-ticker.C:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
				})
				if err != nil {
					t.Fatal("independent original claims", err)
				}
				for _, claim := range claims {
					var expiry int64
					if store.db.QueryRowContext(ctx, `SELECT lease_expires_at FROM engine.cluster_onboarding_targets WHERE id=$1`, claim.Lease.TargetID).Scan(&expiry) != nil || expiry <= deadline {
						t.Fatal("sibling original claim not renewed")
					}
				}
			} else {
				selected := 0
				if mode == "second crypto" {
					selected = 1
				}
				opened, connected := false, false
				var credential *clusterremote.Credential
				deps := clusterSSHNetworkTargetDependencies{
					authority: func(c clusterSSHNetworkTargetClaim) clusterSSHPreparationAuthorityRead {
						return newClusterSSHPreparationAuthorityRead(store, f.aegis, c.Lease, f.baseline, c.Sealed)
					},
					renew: func(ctx context.Context, c clusterSSHNetworkTargetClaim) error {
						return store.renewClusterSSHPreparationTarget(ctx, c.Lease, c.Sealed)
					},
					open: func(ctx context.Context, sealed sealedClusterSSHCredentials, binding clusterSSHCredentialBinding) (clusterSSHCredentialEnvelope, error) {
						var one int
						if store.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one) != nil || sealed != claims[selected].Sealed || binding != claims[selected].Sealed.binding {
							t.Error("borrowed credential or retained SQL connection")
						}
						opened = true
						return f.aegis.openClusterSSHCredentials(ctx, sealed, binding)
					},
					connect: func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, c *clusterremote.Credential) (*clusterremote.Client, error) {
						connected = true
						credential = c
						var one int
						if store.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one) != nil || target.Address != claims[selected].Sealed.binding.Address {
							t.Error("pool retained during transport")
						}
						return nil, errors.New("synthetic transport boundary")
					},
				}
				err := runClusterSSHNetworkTargets(ctx, f.baseline, claims, deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
					a, err := readers.Authority(ctx)
					if err != nil {
						return err
					}
					_, err = readers.Peer(ctx, a.Cohort.Targets[selected], clusterSSHNetworkTargetPeers(a, a.Cohort.Targets[selected].Binding.Address))
					return err
				})
				if err == nil || !opened || !connected || credential == nil {
					t.Fatal("original credential boundary not exercised")
				}
				a, err := f.read(ctx)
				if err != nil {
					t.Fatal("authority after cleanup")
				}
				_, err = (clusterremote.Transport{}).Connect(ctx, clusterremote.Target{Address: claims[selected].Sealed.binding.Address, Port: 22}, a.Cohort.Targets[selected].Key, credential)
				if err != clusterremote.ErrInvalidAuth {
					t.Fatal("transport failure retained mutable credential")
				}
			}
			if store.db.Stats().InUse != 0 || f.events(t) != before {
				t.Fatal("scope retained connection or advanced operation")
			}
		})
	}
}

func TestClusterSSHNetworkTargetsPostgresOriginalEnvelopeRevocation(t *testing.T) {
	for _, mode := range []string{"first ciphertext", "second ciphertext", "second reclaimed", "second expired", "Aegis locked"} {
		t.Run(mode, func(t *testing.T) {
			f, claims := sshNetworkClaimsPostgresFixture(t, false)
			store := f.c.store
			store.db.SetMaxOpenConns(1)
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			started := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- withClusterSSHNetworkTargets(ctx, store, f.aegis, f.baseline, claims, func(ctx context.Context, _ clusterSSHNetworkTargetReaders) error {
					close(started)
					<-ctx.Done()
					return nil
				})
			}()
			joined := false
			t.Cleanup(func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("cohort guard not joined")
					}
				}
			})
			select {
			case <-started:
			case err := <-done:
				joined = true
				t.Fatal("scope did not start", err)
			case <-ctx.Done():
				t.Fatal("scope deadline")
			}
			switch mode {
			case "first ciphertext", "second ciphertext":
				i := 0
				if mode == "second ciphertext" {
					i = 1
				}
				envelope, err := f.aegis.openClusterSSHCredentials(ctx, claims[i].Sealed, claims[i].Sealed.binding)
				if err != nil {
					t.Fatal("original envelope fixture")
				}
				envelope.Password += "replacement"
				replacement, err := f.aegis.sealClusterSSHCredentials(ctx, envelope)
				if err != nil || replacement.generation != claims[i].Sealed.generation || replacement.ciphertext == claims[i].Sealed.ciphertext {
					t.Fatal("same-generation replacement fixture")
				}
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=$2 WHERE target_id=$1`, claims[i].Lease.TargetID, replacement.ciphertext)
			case "second reclaimed":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_holder=$2,lease_generation=lease_generation+1 WHERE id=$1`, claims[1].Lease.TargetID, newClusterUUID())
			case "second expired":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=0 WHERE target_id=$1`, claims[1].Lease.TargetID)
			case "Aegis locked":
				f.aegis.mu.Lock()
				clear(f.aegis.key)
				f.aegis.key = nil
				f.aegis.mu.Unlock()
			}
			select {
			case err := <-done:
				joined = true
				if err == nil {
					t.Fatal("ignored whole-cohort authority loss")
				}
			case <-ctx.Done():
				t.Fatal("authority loss not observed")
			}
			if store.db.Stats().InUse != 0 {
				t.Fatal("authority loss retained connection")
			}
		})
	}
}

func TestClusterSSHNetworkTargetsPostgresRechecksSiblingAfterLockWait(t *testing.T) {
	for _, mode := range []string{"ciphertext", "reclaimed", "expired"} {
		t.Run(mode, func(t *testing.T) {
			f, claims := sshNetworkClaimsPostgresFixture(t, false)
			store := f.c.store
			store.db.SetMaxOpenConns(3)
			deadline := time.Now().Unix() + 2
			if mode == "expired" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=$2 WHERE id=$1`, claims[1].Lease.TargetID, deadline)
			}
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			blocker, err := store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal("blocker")
			}
			defer blocker.Rollback()
			var one int
			if blocker.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName).Scan(&one) != nil {
				t.Fatal("controller lock")
			}
			done := make(chan error, 1)
			joined := false
			var consumed atomic.Bool
			go func() {
				done <- withClusterSSHNetworkTargets(ctx, store, f.aegis, f.baseline, claims, func(context.Context, clusterSSHNetworkTargetReaders) error {
					consumed.Store(true)
					return nil
				})
			}()
			t.Cleanup(func() {
				blocker.Rollback()
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("cohort lock wait not joined")
					}
				}
			})
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%engine.cluster_application_leases%')`).Scan(&waiting) != nil {
					t.Fatal("lock observation")
				}
				if waiting {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("no real lock wait")
				}
			}
			switch mode {
			case "ciphertext":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'replacement' WHERE target_id=$1`, claims[1].Lease.TargetID)
			case "reclaimed":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET lease_holder=$2,lease_generation=lease_generation+1 WHERE id=$1`, claims[1].Lease.TargetID, newClusterUUID())
			case "expired":
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
						t.Fatal("expiry deadline")
					}
				}
			}
			if err != nil || blocker.Commit() != nil {
				t.Fatal("controller-first mutation blocked")
			}
			select {
			case err := <-done:
				joined = true
				if err == nil || consumed.Load() {
					t.Fatal("stale sibling accepted")
				}
			case <-time.After(time.Second):
				t.Fatal("waiter did not finish")
			}
		})
	}
}
