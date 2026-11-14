package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"strings"
	"testing"
	"time"
)

func newSSHPreparationWorkerFixture(t *testing.T, replacement bool) (sshPreparationAuthorityFixture, clusterSSHPreparationWork) {
	t.Helper()
	f, snapshot, confirmation, proof := newSSHPreparationApprovalFixture(t, replacement)
	if err := f.c.commitClusterSSHPreparationApproval(f.ctx, snapshot, confirmation, proof); err != nil {
		t.Fatal("approve worker fixture", err)
	}
	f.op.State, f.op.CurrentStep = "running", clusterSSHPreparationOperationStep
	work, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID())
	if err != nil {
		t.Fatal("claim worker fixture", err)
	}
	return f, work
}

func TestClusterSSHPreparationWorkerPostgres(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "wrong source", "missing dispatcher", "missing sibling", "wrong envelope", "qualification lease", "dispatch failure", "wrong result", "wrong confirmation", "controller loss", "aegis lock", "ignored boundary failure", "cancelled", "cleanup failure"} {
		t.Run(mode, func(t *testing.T) {
			f, work := newSSHPreparationWorkerFixture(t, mode == "replacement")
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			calls := 0
			var escaped func(context.Context) error
			dispatch := func(ctx context.Context, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected, confirmation string, source engineidentity.Binding,
				check func(context.Context) error) (clusterSSHPreparationResult, error) {
				if source != work.IdentitySource || baseline != work.Baseline || claim != work.Claims[calls] || f.c.store.db.Stats().InUse != 0 {
					t.Error("dispatch changed input, order, or retained database connection")
				}
				calls++
				escaped = check
				if err := check(ctx); err != nil {
					return clusterSSHPreparationResult{}, err
				}
				result := clusterSSHPreparationResultFixture(claim.Lease, confirmation)
				if calls == len(work.Claims) {
					switch mode {
					case "dispatch failure", "cleanup failure":
						return result, errClusterConflict
					case "wrong result":
						result.Generation++
					case "wrong confirmation":
						result.ConfirmationSHA256 = strings.Repeat("f", 64)
					case "controller loss":
						f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`, clusterControllerLeaseName)
					case "aegis lock":
						f.aegis.mu.Lock()
						clear(f.aegis.key)
						f.aegis.key = nil
						f.aegis.mu.Unlock()
					case "ignored boundary failure":
						failed, stop := context.WithCancel(ctx)
						stop()
						if check(failed) == nil {
							t.Error("cancelled boundary accepted")
						}
					case "cancelled":
						cancel()
					}
				}
				return result, nil
			}
			switch mode {
			case "wrong source":
				work.IdentitySource.SourceUID = newClusterUUID()
			case "missing dispatcher":
				dispatch = nil
			case "missing sibling":
				work.Claims = work.Claims[:1]
			case "wrong envelope":
				work.Claims[0].Sealed.ciphertext += "changed"
			case "qualification lease":
				work.Claims[0].Lease.OperationStep, work.Claims[0].Lease.Step = clusterSSHQualificationStep, "qualify"
			}
			worker := newClusterSSHPreparationWorker(f.c.store, f.aegis, dispatch)
			before := f.events(t)
			err := worker.prepare(ctx, work)
			if escaped != nil && escaped(context.Background()) == nil {
				t.Fatal("dispatch authority escaped joined scope")
			}
			if mode == "expansion" || mode == "replacement" {
				if err != nil || calls != len(work.Claims) || f.events(t) != before+1 {
					t.Fatal("whole cohort failed to settle", err)
				}
				if _, err := f.c.loadClusterSSHIdentityRecoveryReservation(f.ctx, f.op); err != nil {
					t.Fatal("worker receipt did not feed recovery reservation", err)
				}
				return
			}
			if err == nil || f.events(t) != before {
				t.Fatal("failed worker settled")
			}
			if mode == "wrong source" && calls != 0 {
				t.Fatal("substituted source reached dispatcher")
			}
			var retained int
			if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets
 WHERE operation_id=$1 AND state='running' AND current_step='stage_source' AND lease_holder<>''`, f.op.ID).Scan(&retained); err != nil || retained != len(f.targets) {
				t.Fatal("failure cleared claims or partially settled", err)
			}
			f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
			if _, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID()); err == nil {
				t.Fatal("uncertain expired work replayed")
			}
		})
	}
}

func TestClusterSSHPreparationWorkerPostgresRenewsCohort(t *testing.T) {
	f, work := newSSHPreparationWorkerFixture(t, false)
	dispatch := func(ctx context.Context, claim clusterSSHNetworkTargetClaim, _ clusterbootstrap.Expected, confirmation string, source engineidentity.Binding,
		_ func(context.Context) error) (clusterSSHPreparationResult, error) {
		// Force both claims near expiry while dispatcher is busy. The sibling
		// not yet dispatched must receive heartbeats too, with a one-slot pool.
		f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=floor(extract(epoch FROM clock_timestamp()))+2 WHERE operation_id=$1`, f.op.ID)
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return clusterSSHPreparationResult{}, ctx.Err()
		case <-timer.C:
		}
		var renewed int
		if err := f.c.store.db.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets
 WHERE operation_id=$1 AND lease_expires_at>extract(epoch FROM clock_timestamp())+10`, f.op.ID).Scan(&renewed); err != nil || renewed != len(work.Claims) {
			t.Error("whole cohort not renewed", err)
			return clusterSSHPreparationResult{}, errClusterConflict
		}
		return clusterSSHPreparationResultFixture(claim.Lease, confirmation), nil
	}
	worker := newClusterSSHPreparationWorker(f.c.store, f.aegis, dispatch)
	worker.interval = 5 * time.Millisecond
	if err := worker.prepare(f.ctx, work); err != nil {
		t.Fatal("renewed worker failed", err)
	}
}
