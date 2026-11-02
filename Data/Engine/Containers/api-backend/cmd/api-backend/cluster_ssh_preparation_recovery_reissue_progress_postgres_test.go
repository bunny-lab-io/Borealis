package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func recoveryReissuedProgressSQLFixture(t *testing.T, replacement, fourth bool, baseline clusterbootstrap.Expected) (sshPreparationAuthorityFixture, clusterSSHPreparationRecoveryReissuedProofClaim, map[string]any) {
	t.Helper()
	// The source fixture's actual issuance chain uses this same original approved
	// receipt fixture. Here the baseline comes from the real immutable Bundle.
	f, original, secret := recoveryProgressClaimFixture(t, replacement, baseline)
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
	claim := recoveryReissueSQLClaim(t, f, original.Reservation.ID)
	if fourth {
		f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
		claim = recoveryReissueSQLClaim(t, f, claim.Lineage.Requests[0].ID)
	}
	f = recoveryTransportSQLExercise(t, f)
	f.c.store.db.SetMaxOpenConns(1)
	return f, claim, secret
}

func recoveryReissuedProgressSQLSource(f sshPreparationAuthorityFixture, secret map[string]any) func(clusterSSHPreparationRecoveryReissuedProofAuthority, string, func(context.Context) error) clusterSSHPreparationRecoverySourceRead {
	return func(value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string, check func(context.Context) error) clusterSSHPreparationRecoverySourceRead {
		return newClusterSSHPreparationRecoveryReissuedSourceRead(value, payload, check,
			func(ctx context.Context, path string, out any) error {
				return recoveryReissuedSourceSQLGet(ctx, f, value.Source, secret, path, out)
			}, func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
				if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
					return clusterbootstrap.SourceNetwork{}, err
				}
				return sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version), ctx.Err()
			})
	}
}

func assertRecoveryReissuedProgressSQLRetained(t *testing.T, f sshPreparationAuthorityFixture, claim clusterSSHPreparationRecoveryReissuedProofClaim, events int, parent string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(f.ctx), 5*time.Second)
	defer cancel()
	f.ctx = ctx
	var state, step, payload string
	if err := f.c.store.db.QueryRowContext(ctx, `SELECT state,current_step,payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state, &step, &payload); err != nil || state != "running" || step != clusterSSHPreparationRecoveryOperationStep || payload != claim.payload || events != f.events(t) {
		t.Fatal("progress changed durable recovery", err)
	}
	var count int
	if err := f.c.store.db.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1`, f.op.ID).Scan(&count); err != nil || count != len(claim.Claims) {
		t.Fatal("progress changed original target cohort", err)
	}
	for _, target := range claim.Claims {
		var holder string
		var generation int64
		if err := f.c.store.db.QueryRowContext(ctx, `SELECT state,current_step,lease_holder,lease_generation FROM engine.cluster_onboarding_targets WHERE id=$1 AND operation_id=$2`, target.Lease.TargetID, f.op.ID).Scan(&state, &step, &holder, &generation); err != nil || state != "running" || step != clusterSSHPreparationRecoveryClaimStep || holder != target.Lease.Holder || generation != target.Lease.Generation {
			t.Fatal("progress settled or advanced original target custody", err)
		}
	}
	if f.c.store.db.Stats().InUse != 0 {
		t.Fatal("progress retained SQL connection")
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatal("progress retained owned private inputs", err)
	}
}

func TestClusterSSHPreparationRecoveryReissueProgressPostgresCustody(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPackWithCompressionLevel(t, members, gzip.NoCompression)
	for _, mode := range []string{"pair Q3", "replacement Q4", "second target failure", "final Secret drift", "final credential drift", "SSH cleanup failure", "input cleanup failure"} {
		t.Run(mode, func(t *testing.T) {
			replacement := mode != "pair Q3" && mode != "second target failure"
			f, claim, secret := recoveryReissuedProgressSQLFixture(t, replacement, replacement, baseline)
			copied := claim
			parent := t.TempDir()
			var expectedCredentials, borrowedCredentials []*clusterremote.Credential
			var expectedSudo [][]byte
			for _, target := range claim.Claims {
				envelope, err := f.aegis.openClusterSSHCredentials(f.ctx, target.Sealed, target.Sealed.binding)
				if err != nil {
					t.Fatal("original encrypted credential", err)
				}
				credential, sudo, err := clusterSSHNetworkCredential(envelope)
				if err != nil {
					t.Fatal(err)
				}
				expectedCredentials = append(expectedCredentials, credential)
				expectedSudo = append(expectedSudo, sudo)
				t.Cleanup(func() { credential.Destroy(); clear(sudo) })
			}
			factories, reads, downloads, connected, inspected, closed, cleaned, finalReads := 0, 0, 0, 0, 0, 0, 0, 0
			var invariantFailure string
			var failureOnce sync.Once
			fail := func(message string) error {
				failureOnce.Do(func() { invariantFailure = message })
				return errors.New(message)
			}
			var escaped func(context.Context) error
			source := recoveryReissuedProgressSQLSource(f, secret)
			deps := clusterSSHPreparationRecoveryReissuedProgressDependencies{
				source: func(value clusterSSHPreparationRecoveryReissuedProofAuthority, payload string, check func(context.Context) error) clusterSSHPreparationRecoverySourceRead {
					factories++
					read := source(value, payload, check)
					return func(ctx context.Context) (clusterSSHPreparationRecoverySource, error) {
						reads++
						if cleaned == len(claim.Claims) {
							finalReads++
						}
						return read(ctx)
					}
				},
				download: func(ctx context.Context, e clusterbootstrap.Expected, scratch string) (*clusterbootstrap.Bundle, error) {
					downloads++
					if e != baseline || recoveryProgressSQLPoolAvailable(ctx, f) != nil {
						return nil, fail("download lost original baseline or released SQL pool")
					}
					return recoveryProgressStageFixture(t, ctx, e, manifest, archive, scratch)
				},
				closeInputs: func(inputs *clusterbootstrap.PreparationInputs) error {
					cleaned++
					if err := recoveryProgressSQLPoolAvailable(f.ctx, f); err != nil {
						return fail("input cleanup retained SQL")
					}
					if err := inputs.Close(); err != nil {
						return fail("actual private-input cleanup failed")
					}
					if cleaned == len(claim.Claims) {
						switch mode {
						case "final Secret drift":
							secret["metadata"].(map[string]any)["resourceVersion"] = "changed-after-last-input-close"
						case "final credential drift":
							if _, err := f.c.store.db.ExecContext(f.ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, claim.Claims[0].Lease.TargetID); err != nil {
								return fail("late credential fixture mutation failed")
							}
						case "input cleanup failure":
							return errors.New("synthetic input cleanup failure")
						}
					}
					return nil
				},
			}
			deps.connect = func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
				index := connected
				connected++
				if index >= len(claim.Claims) || recoveryProgressSQLPoolAvailable(ctx, f) != nil {
					return nil, fail("unexpected session or retained SQL")
				}
				original := f.targets[index]
				if target.Address != original.Binding.Address || target.Port != original.Binding.Port || !reflect.DeepEqual(key, original.Key) || !reflect.DeepEqual(credential, expectedCredentials[index]) {
					return nil, fail("session lost exact original target, pin or decrypted credential")
				}
				borrowedCredentials = append(borrowedCredentials, credential)
				return &recoveryProgressClient{
					close: func() error {
						closed++
						if recoveryProgressSQLPoolAvailable(f.ctx, f) != nil {
							return fail("SSH cleanup retained SQL")
						}
						if mode == "SSH cleanup failure" {
							return errors.New("synthetic SSH cleanup failure")
						}
						return nil
					},
					inspect: func(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, sudo []byte, check func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
						inspected++
						escaped = check
						lease := claim.Claims[index].Lease
						if expected.Target.Generation != lease.Generation || expected.Target.HolderID != lease.Holder || expected.Target.TargetID != lease.TargetID || expected.Source != baseline || !reflect.DeepEqual(sudo, expectedSudo[index]) || recoveryProgressSQLPoolAvailable(ctx, f) != nil {
							return clusterbootstrap.PreparationProgress{}, fail("inspection lost issued tip, sudo bytes or released SQL pool")
						}
						if _, err := inputs.Configuration(ctx, expected, check); err != nil {
							return clusterbootstrap.PreparationProgress{}, fail("real original configuration unavailable")
						}
						if _, err := inputs.SourceBundle(ctx, expected, check); err != nil {
							return clusterbootstrap.PreparationProgress{}, fail("real original Bundle unavailable")
						}
						if mode == "second target failure" && index == 1 {
							return clusterbootstrap.PreparationProgress{Step: "stage_source"}, errors.New("synthetic second target EOF")
						}
						return clusterbootstrap.PreparationProgress{Step: "stage_source"}, nil
					},
				}, nil
			}
			events := f.events(t)
			got, err := f.c.observeClusterSSHPreparationRecoveryReissuedProgressWith(f.ctx, f.aegis, claim, parent, deps)
			valid := strings.HasSuffix(mode, "Q3") || strings.HasSuffix(mode, "Q4")
			if invariantFailure != "" {
				t.Fatal(invariantFailure)
			}
			if (err == nil) != valid || !valid && len(got) != 0 {
				t.Fatal("failed cohort released private cursors", err)
			}
			if factories != 1 || connected != len(claim.Claims) || inspected != connected || closed != connected || cleaned != connected || downloads != connected || !claim.custody.started.Load() {
				t.Fatal("incomplete issued cohort lifecycle", factories, downloads, connected, inspected, closed, cleaned)
			}
			if valid {
				if len(got) != len(claim.Claims) || finalReads != 1 {
					t.Fatal("cohort escaped without final source proof after all cleanup")
				}
				for i, result := range got {
					if result.TargetID != claim.Claims[i].Lease.TargetID || result.Progress.Step != "stage_source" {
						t.Fatal("cursor changed original target or claimed completion")
					}
				}
			}
			if escaped == nil || escaped(context.Background()) == nil {
				t.Fatal("native authority survived scope closure")
			}
			for _, credential := range borrowedCredentials {
				if !reflect.DeepEqual(credential, &clusterremote.Credential{}) {
					t.Fatal("borrowed credential survived cleanup")
				}
			}
			before := [7]int{factories, reads, downloads, connected, inspected, closed, cleaned}
			// Consume-once rejection precedes SQL as well as native IO. Holding the
			// sole connection prevents stale SQL expiry from masking a broken latch.
			blocked, err := f.c.store.db.Conn(f.ctx)
			if err != nil {
				t.Fatal("hold replay SQL boundary", err)
			}
			replayCtx, replayCancel := context.WithTimeout(context.WithoutCancel(f.ctx), 250*time.Millisecond)
			beforeWaits := f.c.store.db.Stats().WaitCount
			if replayCtx.Err() != nil {
				replayCancel()
				_ = blocked.Close()
				t.Fatal("replay fixture context expired before invocation")
			}
			again, replayErr := f.c.observeClusterSSHPreparationRecoveryReissuedProgressWith(replayCtx, f.aegis, copied, parent, deps)
			replayDeadline := replayCtx.Err()
			afterWaits := f.c.store.db.Stats().WaitCount
			replayCancel()
			if err := blocked.Close(); err != nil {
				t.Fatal("release replay SQL boundary", err)
			}
			if replayErr == nil || replayDeadline != nil || afterWaits != beforeWaits || len(again) != 0 || before != [7]int{factories, reads, downloads, connected, inspected, closed, cleaned} {
				t.Fatal("copied handle attempted SQL, source or native inputs")
			}
			assertRecoveryReissuedProgressSQLRetained(t, f, claim, events, parent)
		})
	}
}

func TestClusterSSHPreparationRecoveryReissueProgressPostgresJoinsCancellation(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPackWithCompressionLevel(t, members, gzip.NoCompression)
	for _, mode := range []string{"parent cancellation", "authority loss"} {
		t.Run(mode, func(t *testing.T) {
			f, claim, secret := recoveryReissuedProgressSQLFixture(t, false, true, baseline)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			started, trigger, closing, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			done := make(chan error, 1)
			var result []clusterSSHPreparationRecoveryTargetProgress
			var escaped func(context.Context) error
			closed, cleaned, inspected := 0, 0, 0
			parent := t.TempDir()
			events := f.events(t)
			deps := clusterSSHPreparationRecoveryReissuedProgressDependencies{
				source: recoveryReissuedProgressSQLSource(f, secret),
				download: func(ctx context.Context, e clusterbootstrap.Expected, scratch string) (*clusterbootstrap.Bundle, error) {
					if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
						return nil, err
					}
					return recoveryProgressStageFixture(t, ctx, e, manifest, archive, scratch)
				},
				closeInputs: func(inputs *clusterbootstrap.PreparationInputs) error { cleaned++; return inputs.Close() },
				connect: func(ctx context.Context, _ clusterremote.Target, _ clusterremote.HostKey, _ *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
					if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
						return nil, err
					}
					return &recoveryProgressClient{
						close: func() error { closed++; close(closing); <-release; return nil },
						inspect: func(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ clusterbootstrap.PreparationExpected, _ []byte, check func(context.Context) error) (clusterbootstrap.PreparationProgress, error) {
							inspected++
							escaped = check
							if err := recoveryProgressSQLPoolAvailable(ctx, f); err != nil {
								return clusterbootstrap.PreparationProgress{}, err
							}
							close(started)
							select {
							case <-trigger:
							case <-ctx.Done():
							}
							_ = check(context.Background())
							return clusterbootstrap.PreparationProgress{Step: "stage_source"}, nil
						},
					}, nil
				},
			}
			go func() {
				err := errors.New("progress callback exited before returning")
				defer func() { done <- err }()
				result, err = f.c.observeClusterSSHPreparationRecoveryReissuedProgressWith(ctx, f.aegis, claim, parent, deps)
			}()
			joined := false
			defer func() {
				cancel()
				releaseOnce.Do(func() { close(release) })
				if !joined {
					<-done
				}
			}()
			select {
			case <-started:
			case err := <-done:
				joined = true
				t.Fatal("progress entry", err)
			case <-ctx.Done():
				t.Fatal("progress entry timeout")
			}
			if mode == "parent cancellation" {
				cancel()
			} else {
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, claim.Claims[1].Lease.TargetID)
				close(trigger)
			}
			select {
			case <-closing:
			case err := <-done:
				joined = true
				t.Fatal("scope escaped owned SSH cleanup", err)
			case <-time.After(5 * time.Second):
				t.Fatal("SSH cleanup did not start")
			}
			select {
			case <-done:
				joined = true
				t.Fatal("scope returned before owned SSH cleanup joined")
			case <-time.After(25 * time.Millisecond):
			}
			releaseOnce.Do(func() { close(release) })
			select {
			case err := <-done:
				joined = true
				if err == nil || len(result) != 0 || inspected != 1 || closed != 1 || cleaned != 1 || escaped == nil || escaped(context.Background()) == nil || !claim.custody.failed.Load() {
					t.Fatal("cancellation released cursor or unjoined authority", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("progress did not join cleanup")
			}
			assertRecoveryReissuedProgressSQLRetained(t, f, claim, events, parent)
		})
	}
}
