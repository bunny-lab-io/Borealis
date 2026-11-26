package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHPrejoinCohortWorkerPostgres(t *testing.T) {
	t.Run("paired native transfer", testPairedNativeTransfer)
	for _, mode := range []string{"pair", "replacement", "missing sibling", "reordered receipts", "dispatch failure", "ignored authority failure", "wrong generation"} {
		t.Run(mode, func(t *testing.T) {
			f, work := newSSHPreparationWorkerFixture(t, mode == "replacement")
			var authority clusterSSHPreparationAuthority
			uids := map[string]string{}
			var escaped func(context.Context) error
			dispatch := func(ctx context.Context, claims []clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected, confirmation string,
				source engineidentity.Binding, check func(context.Context) error) ([]clusterSSHPrejoinDispatchResult, error) {
				if !reflect.DeepEqual(claims, work.Claims) || baseline != work.Baseline || source != work.IdentitySource || confirmation != work.Authorization.ConfirmationSHA256 || f.c.store.db.Stats().InUse != 0 {
					t.Fatal("cohort dispatch lost exact work or retained SQL connection")
				}
				escaped = check
				var values []clusterSSHPrejoinDispatchResult
				for i, claim := range claims {
					a, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, claim.Lease, baseline, claim.Sealed)(ctx)
					if err != nil || check(ctx) != nil {
						return nil, errClusterConflict
					}
					authority = a
					expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16", a.payload)
					if err != nil {
						return nil, err
					}
					receipt := sshPrejoinReceiptIdentityFixture(expected, source, newClusterUUID(), uint64(42+i))
					uids[claim.Lease.TargetID] = receipt.Node.NodeUID
					values = append(values, clusterSSHPrejoinDispatchResult{Expected: expected, Receipt: receipt})
				}
				switch mode {
				case "missing sibling":
					values = values[:1]
				case "reordered receipts":
					values[0], values[1] = values[1], values[0]
				case "dispatch failure":
					return values, errClusterConflict
				case "ignored authority failure":
					failed, cancel := context.WithCancel(ctx)
					cancel()
					_ = check(failed)
				case "wrong generation":
					values[0].Expected.Target.Generation++
				}
				return values, nil
			}
			getJSON := func(ctx context.Context, path string, out any) error {
				if escaped == nil || escaped(context.Background()) == nil || f.c.store.db.Stats().InUse != 0 {
					t.Fatal("settlement preceded scope cleanup or retained SQL connection")
				}
				return sshPrejoinCompletionNodes(authority, uids)(ctx, path, out)
			}
			worker := newClusterSSHPrejoinCohortPreparationWorker(f.c.store, f.aegis, dispatch, getJSON)
			before := f.events(t)
			err := worker.prepare(f.ctx, work)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success || success && f.events(t) != before+1 || !success && f.events(t) != before {
				t.Fatal("incorrect whole-cohort settlement", err)
			}
			if _, err := f.c.loadClusterSSHIdentityRecoveryReservation(f.ctx, f.op); err == nil {
				t.Fatal("cohort barrier entered legacy joined recovery")
			}
		})
	}
}

// The worker supplies real PostgreSQL-backed authority and temporary access for
// both claims. The in-process peer validates the bound archive and configuration.
// The companion Linux test exercises the real journal and first filesystem effect.
func testPairedNativeTransfer(t *testing.T) {
	base, manifest, members := recoveryProgressArchiveFixture(t)
	wire := recoveryProgressPackWithCompressionLevel(t, members, gzip.NoCompression)
	for _, revoke := range []bool{false, true} {
		mode := "both cursors"
		if revoke {
			mode = "sibling credential expires during transfer"
		}
		t.Run(mode, func(t *testing.T) {
			f, rawSecret := sourceBoundPreparationFixture(t, false, base)
			f = recoveryTransportSQLExercise(t, f)
			work, err := f.c.store.claimFencedSSHPreparation(f.ctx, f.op, f.c.holder, newClusterUUID())
			if err != nil || len(work.Claims) != 2 {
				t.Fatal("paired claim", err)
			}
			secret, err := clusterbootstrap.ParsePreparationRuntimeSecret(recoveryProgressMarshal(t, rawSecret))
			if err != nil {
				t.Fatal(err)
			}
			cursors := 0
			stop := errors.New("fixture ends at successful pair inspection")
			var dispatchErr error
			worker := newClusterSSHPrejoinCohortPreparationWorker(f.c.store, f.aegis, func(ctx context.Context, claims []clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected, _ string, _ engineidentity.Binding, check func(context.Context) error) ([]clusterSSHPrejoinDispatchResult, error) {
				if err := f.c.store.openSSHPreparationAllocation(ctx, claims); err != nil {
					return nil, err
				}
				for i, claim := range claims {
					a, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, claim.Lease, baseline, claim.Sealed)(ctx)
					if err != nil {
						return nil, err
					}
					expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16", a.payload)
					if err != nil {
						return nil, err
					}
					config, err := clusterbootstrap.NewPreparationConfiguration(expected, secret.Settings())
					if err != nil {
						return nil, err
					}
					bundle, err := recoveryProgressStageFixture(t, ctx, baseline, manifest, wire, t.TempDir())
					if err != nil {
						return nil, err
					}
					defer bundle.Close()
					inputs, err := clusterbootstrap.BindPreparationInputs(ctx, bundle, config, check)
					if err != nil {
						return nil, err
					}
					_, manager, _, err := bundle.ManagerProof()
					if err != nil {
						return nil, err
					}
					req, err := clusterbootstrap.NewPreparationSessionRequest(expected, manager, strings.Repeat("a", 64), "inspect_preparation_progress")
					if err != nil {
						return nil, err
					}
					scratch := t.TempDir()
					local, peer := net.Pipe()
					done := make(chan error, 1)
					var producing atomic.Bool
					pulse := make(chan struct{}, 1)
					go func() {
						done <- clusterbootstrap.ServePreparationSessionWithProgress(ctx, peer, peer, req.Session.Nonce,
							func(_ context.Context, r clusterbootstrap.PreparationSessionRequest) error {
								if r != req {
									return clusterbootstrap.ErrSessionProtocol
								}
								return nil
							},
							func(ctx context.Context, _ clusterbootstrap.PreparationSessionRequest, r io.Reader) (clusterbootstrap.PreparationProgress, error) {
								e := clusterbootstrap.ConsumePreparation(ctx, scratch, expected, manager, r, func(c context.Context) error { return c.Err() }, func(c context.Context, received *clusterbootstrap.PreparationInputs) error {
									return received.Verify(c, expected, func(c context.Context) error { return c.Err() })
								})
								return clusterbootstrap.PreparationProgress{Step: "empty"}, e
							}, func(time.Time) error {
								if producing.Load() {
									select {
									case pulse <- struct{}{}:
									default:
									}
								}
								return nil
							})
					}()
					var revoked atomic.Bool
					cursor, sendErr := clusterbootstrap.ExchangePreparationProgressSession(ctx, local, local, req, func(c context.Context, w io.Writer) error {
						producing.Store(true)
						if i == 0 {
							if revoke {
								_, err := f.c.store.db.ExecContext(c, "UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1", claims[1].Lease.TargetID)
								if err != nil {
									return err
								}
								revoked.Store(true)
							}
							select {
							case <-pulse:
							case <-c.Done():
								return c.Err()
							}
						}
						return inputs.WritePreparation(c, expected, w, check)
					}, check)
					peerErr := <-done
					local.Close()
					files, err := os.ReadDir(scratch)
					if err != nil || len(files) != 0 {
						t.Error("receiver retained scratch", err)
					}
					if revoke {
						if !revoked.Load() || sendErr == nil || peerErr == nil || cursor != (clusterbootstrap.PreparationProgress{}) {
							t.Error("sibling expiry failed to contain transfer", cursor, sendErr, peerErr)
						}
						dispatchErr = sendErr
						return nil, sendErr
					}
					if sendErr != nil || peerErr != nil || cursor.Step != "empty" {
						return nil, errors.Join(sendErr, peerErr, errors.New("paired cursor absent"))
					}
					cursors++
				}
				dispatchErr = stop
				return nil, stop
			}, func(context.Context, string, any) error {
				t.Error("settlement without installation receipts")
				return stop
			})
			if worker.prepare(f.ctx, work) == nil || (revoke && (cursors != 0 || dispatchErr == nil)) || (!revoke && (cursors != 2 || dispatchErr != stop)) {
				t.Fatal("unexpected transfer disposition", cursors, dispatchErr)
			}
			var held int
			err = f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets t
JOIN engine.cluster_state s ON s.active_operation_id=t.operation_id
WHERE t.operation_id=$1 AND t.state='running' AND t.lease_holder=$2`, f.op.ID, work.Claims[0].Lease.Holder).Scan(&held)
			if err != nil || held != 2 {
				t.Fatal("inspection-only fixture lost paired custody", held, err)
			}

		})
	}
}
