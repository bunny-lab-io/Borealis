package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func newSSHQualificationFixture(t *testing.T, replacement bool) sshPreparationAuthorityFixture {
	t.Helper()
	f := newSSHPreparationAuthorityFixtureForTopology(t, replacement)
	// Reuse the real controller-produced inspection proof, returning only the
	// isolated fixture's reserved preparation rows to their original wait.
	f.exec(t, `UPDATE engine.cluster_operations SET state='waiting',current_step='qualify_ssh_targets' WHERE id=$1`, f.op.ID)
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='queued',current_step='inspection_complete',lease_holder='',lease_expires_at=0,lease_generation=inspected_generation WHERE operation_id=$1`, f.op.ID)
	return f
}

func TestClusterSSHQualificationPostgresReadOnlyLifecycle(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "expansion", true: "replacement"}[replacement], func(t *testing.T) {
			f := newSSHQualificationFixture(t, replacement)
			s := f.c.store
			id, err := s.pendingClusterSSHQualification(f.ctx)
			if err != nil || id != f.op.ID {
				t.Fatal("missing read-only work", err)
			}
			work, err := s.claimClusterSSHQualification(f.ctx, id, newClusterUUID())
			if err != nil || len(work.Claims) != len(f.targets) {
				t.Fatal("claim", err)
			}
			for _, claim := range work.Claims {
				if !validClusterSSHObservationLease(claim.Lease) || validClusterSSHPreparationLease(claim.Lease) {
					t.Fatal("qualification gained preparation capability")
				}
				sealed, err := s.loadClusterSSHTargetCredentials(f.ctx, claim.Lease)
				if err != nil || sealed != claim.Sealed {
					t.Fatal("independent envelope", err)
				}
				read := newClusterSSHPreparationAuthorityRead(s, f.aegis, claim.Lease, work.Baseline, claim.Sealed)
				a, err := read(f.ctx)
				if err != nil || a.Lease != claim.Lease || s.db.Stats().InUse != 0 {
					t.Fatal("read-only authority or connection release", err)
				}
				if s.renewClusterSSHObservationTarget(f.ctx, claim.Lease, claim.Sealed) != nil {
					t.Fatal("read renewal")
				}
				if s.renewClusterSSHPreparationTarget(f.ctx, claim.Lease, claim.Sealed) == nil || s.renewClusterSSHTarget(f.ctx, claim.Lease) == nil {
					t.Fatal("weaker renewal bypass")
				}
				called := false
				err = withClusterSSHPreparationSource(f.ctx, t.TempDir(), s, f.aegis, claim.Lease, work.Baseline, claim.Sealed, func(context.Context, *clusterbootstrap.PreparationInputs, *clusterbootstrap.ImageSet, clusterbootstrap.PreparationExpected, clusterSSHPreparationChecks) error {
					called = true
					return nil
				})
				if err == nil || called {
					t.Fatal("read-only claim dispatched preparation")
				}
			}
			report := newClusterSSHQualificationReport(work.Proof.Cohort.Attempt)
			report.Checks[0].State = "blocked"
			if err := s.completeClusterSSHQualification(f.ctx, work, report); err != nil {
				t.Fatal("complete", err)
			}
			progress, err := s.clusterSSHInspectionProgress(f.ctx, id)
			if err != nil || progress.Qualification == nil || progress.Qualification.Ready || progress.Qualification.Checks[0].State != "blocked" || progress.State != "waiting" {
				t.Fatal("public report", err)
			}
			for _, target := range progress.Targets {
				if target.Step != "qualification_complete" || !target.CredentialsReady {
					t.Fatal("lost public target state")
				}
			}
			if _, err := s.claimClusterSSHQualification(f.ctx, id, newClusterUUID()); err == nil {
				t.Fatal("replayed completed read pass")
			}
			if err := s.completeClusterSSHQualification(f.ctx, work, report); err == nil {
				t.Fatal("replayed result")
			}
			if _, err := s.cancelClusterOperation(f.ctx, "admission-test", id); err != nil {
				t.Fatal("read-only cancellation", err)
			}
			for _, claim := range work.Claims {
				if s.renewClusterSSHObservationTarget(f.ctx, claim.Lease, claim.Sealed) == nil {
					t.Fatal("cancelled claim renewed")
				}
			}
			progress, err = s.clusterSSHInspectionProgress(f.ctx, id)
			if err != nil || progress.State != "cancelled" || progress.Qualification == nil {
				t.Fatal("historical report lost", err)
			}
		})
	}
	t.Run("runtime records blocked inputs without remote work", func(t *testing.T) {
		f := newSSHQualificationFixture(t, false)
		t.Setenv("BOREALIS_OPERATOR_SECRET", "")
		r := clusterSSHInspectionRuntime{store: f.c.store, aegis: f.aegis, worker: newClusterSSHInspectionWorker(f.c.store, f.aegis)}
		if err := r.runOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		progress, err := f.c.store.clusterSSHInspectionProgress(f.ctx, f.op.ID)
		if err != nil || progress.Qualification == nil || progress.Qualification.Checks[0].State != "blocked" {
			t.Fatal("worker did not publish actionable block", err)
		}
		if f.c.store.db.Stats().InUse != 0 {
			t.Fatal("runtime held DB connection")
		}
	})
}

func TestClusterSSHQualificationPostgresClaimAndCompletionFences(t *testing.T) {
	t.Run("competing cohorts", func(t *testing.T) {
		f := newSSHQualificationFixture(t, false)
		f.c.store.db.SetMaxOpenConns(3)
		var wg sync.WaitGroup
		success := make(chan clusterSSHQualificationWork, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w, err := f.c.store.claimClusterSSHQualification(f.ctx, f.op.ID, newClusterUUID())
				if err == nil {
					success <- w
				}
			}()
		}
		wg.Wait()
		close(success)
		if len(success) != 1 {
			t.Fatal("cohort split between workers")
		}
		work := <-success
		if len(work.Claims) != 2 || work.Claims[0].Lease.Holder != work.Claims[1].Lease.Holder {
			t.Fatal("partial claim")
		}
	})
	for _, mode := range []string{"expired sibling", "expired inspection", "missing sibling", "changed key", "source HMR", "source VIP", "source member", "source config"} {
		t.Run("claim "+mode, func(t *testing.T) {
			f := newSSHQualificationFixture(t, false)
			switch mode {
			case "expired sibling":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=0 WHERE target_id=$1`, f.targets[1].Binding.TargetID)
			case "expired inspection":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspected_at=1 WHERE operation_id=$1`, f.op.ID)
			case "missing sibling":
				f.exec(t, `DELETE FROM engine.cluster_onboarding_targets WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "changed key":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET host_key_base64='changed' WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "source HMR":
				f.exec(t, `UPDATE engine.cluster_state SET hmr_state='active' WHERE id=1`)
			case "source VIP":
				f.exec(t, `UPDATE engine.cluster_state SET edge_vip='192.168.3.199' WHERE id=1`)
			case "source member":
				f.exec(t, `UPDATE engine.cluster_nodes SET management_ip='192.168.3.198' WHERE membership_state='Active'`)
			case "source config":
				f.exec(t, `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1`)
			}
			if _, err := f.c.store.claimClusterSSHQualification(f.ctx, f.op.ID, newClusterUUID()); err == nil {
				t.Fatal("invalid cohort claimed")
			}
			var count int
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND current_step='qualify'`, f.op.ID).Scan(&count) != nil || count != 0 {
				t.Fatal("partial claim committed")
			}
		})
	}
	for _, mode := range []string{"controller", "cancel", "credential", "report", "key", "lease expired", "attempt", "source baseline", "source HMR", "source VIP", "source member", "source config", "unsafe ready"} {
		t.Run("complete "+mode, func(t *testing.T) {
			f := newSSHQualificationFixture(t, false)
			work, err := f.c.store.claimClusterSSHQualification(f.ctx, f.op.ID, newClusterUUID())
			if err != nil {
				t.Fatal(err)
			}
			report := newClusterSSHQualificationReport(1)
			switch mode {
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`, clusterControllerLeaseName)
			case "cancel":
				_, err = f.c.store.cancelClusterOperation(f.ctx, "admission-test", f.op.ID)
				if err != nil {
					t.Fatal(err)
				}
			case "credential":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=$2 WHERE target_id=$1`, work.Claims[1].Lease.TargetID, work.Claims[1].Sealed.ciphertext+"changed")
			case "report":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, work.Claims[1].Lease.TargetID)
			case "key":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET host_key_base64='changed' WHERE id=$1`, work.Claims[1].Lease.TargetID)
			case "lease expired":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE id=$1`, work.Claims[1].Lease.TargetID)
			case "attempt":
				f.exec(t, `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`, f.op.ID)
			case "source baseline":
				f.exec(t, `UPDATE engine.cluster_state SET baseline_sha=$1 WHERE id=1`, strings.Repeat("c", 40))
			case "source HMR":
				f.exec(t, `UPDATE engine.cluster_state SET hmr_state='active' WHERE id=1`)
			case "source VIP":
				f.exec(t, `UPDATE engine.cluster_state SET edge_vip='192.168.3.199' WHERE id=1`)
			case "source member":
				f.exec(t, `UPDATE engine.cluster_nodes SET application_state='drained' WHERE membership_state='Active'`)
			case "source config":
				f.exec(t, `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1`)
			case "unsafe ready":
				report.Ready = true
			}
			if err := f.c.store.completeClusterSSHQualification(f.ctx, work, report); err == nil {
				t.Fatal("stale qualification published")
			}
			var payload string
			_ = f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&payload)
			var object map[string]any
			_ = json.Unmarshal([]byte(payload), &object)
			if object["ssh_qualification"] != nil {
				t.Fatal("partial report persisted")
			}
		})
	}
}

func TestClusterSSHQualificationPostgresExpiredClaimResume(t *testing.T) {
	f := newSSHQualificationFixture(t, false)
	first, err := f.c.store.claimClusterSSHQualification(f.ctx, f.op.ID, newClusterUUID())
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=$2 WHERE operation_id=$1`, f.op.ID, time.Now().Unix()-1)
	second, err := f.c.store.claimClusterSSHQualification(f.ctx, f.op.ID, newClusterUUID())
	if err != nil {
		t.Fatal("read-only pass did not resume", err)
	}
	for i, claim := range second.Claims {
		if claim.Lease.Generation <= first.Claims[i].Lease.Generation || claim.Lease.Holder == first.Claims[i].Lease.Holder {
			t.Fatal("claim reused")
		}
		if _, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, first.Claims[i].Lease, first.Baseline, first.Claims[i].Sealed)(f.ctx); err == nil {
			t.Fatal("old generation remained authoritative")
		}
		if _, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, claim.Lease, second.Baseline, claim.Sealed)(f.ctx); err != nil {
			t.Fatal("new read authority", err)
		}
	}
	if f.c.store.completeClusterSSHQualification(f.ctx, first, newClusterSSHQualificationReport(1)) == nil {
		t.Fatal("old worker published after recovery")
	}
	if err := f.c.store.completeClusterSSHQualification(f.ctx, second, newClusterSSHQualificationReport(1)); err != nil {
		t.Fatal(err)
	}
}
