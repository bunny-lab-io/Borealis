package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newSSHPreparationApprovalFixture(t *testing.T, replacement bool) (sshPreparationAuthorityFixture, clusterSSHPreparationApprovalSnapshot, clusterSSHPreparationConfirmation, clusterSSHPreparationApprovalProof) {
	t.Helper()
	f, _ := newSSHPreparationClaimFixture(t, replacement)
	delete(f.op.Payload, "ssh_preparation_authorization")
	delete(f.op.Payload, "ssh_preparation_confirmation")
	delete(f.op.Payload, "ssh_preparation_readiness")
	f.op.State, f.op.CurrentStep = "waiting", clusterSSHQualificationStep
	f.exec(t, `UPDATE engine.cluster_operations SET state='waiting',current_step=$2,payload_json=$3 WHERE id=$1`,
		f.op.ID, clusterSSHQualificationStep, marshalClusterJSON(f.op.Payload))
	snapshot, now, err := f.c.loadClusterSSHPreparationApproval(f.ctx, f.op)
	if err != nil {
		t.Fatal("load approval", err)
	}
	plan, err := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	if err != nil {
		t.Fatal(err)
	}
	raw := marshalClusterJSON(map[string]any{"request_id": newClusterUUID(), "plan_sha256": plan, "confirmation": "PREPARE ENGINE NODES"})
	confirmation, err := newClusterSSHPreparationConfirmation([]byte(raw), "admin", now, snapshot.work.Authorization)
	if err != nil {
		t.Fatal(err)
	}
	// Persist explicit Admin intent separately; it grants no readiness or leases.
	f.op.Payload["ssh_preparation_request"] = confirmation
	f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(f.op.Payload))
	snapshot, _, err = f.c.loadClusterSSHPreparationApproval(f.ctx, f.op)
	if err != nil {
		t.Fatal("reload requested approval", err)
	}
	proof := clusterSSHPreparationApprovalProof{plan: plan, started: time.Now(), lifetime: f.ctx, recheck: func(context.Context) error {
		if f.c.store.db.Stats().InUse != 0 {
			t.Error("proof observation retained database connection")
		}
		return nil
	}}
	for i := range proof.receipts {
		proof.receipts[i] = strings.Repeat("a", 64)
	}
	return f, snapshot, confirmation, proof
}

func TestClusterSSHPreparationApprovalPostgresLifecycle(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "expansion", true: "replacement"}[replacement], func(t *testing.T) {
			f, snapshot, confirmation, proof := newSSHPreparationApprovalFixture(t, replacement)
			before := f.events(t)
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("snapshot retained connection")
			}
			if err := f.c.commitClusterSSHPreparationApproval(f.ctx, snapshot, confirmation, proof); err != nil {
				t.Fatal("approval failed", err)
			}
			if f.events(t) != before+1 {
				t.Fatal("missing approval event")
			}
			var state, step string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state, &step) != nil || state != "running" || step != clusterSSHPreparationOperationStep {
				t.Fatal("approval phase")
			}
			f.op.State, f.op.CurrentStep = state, step
			work, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID())
			if err != nil || len(work.Claims) != len(snapshot.credentials) {
				t.Fatal("approved plan did not feed claims", err)
			}
			want, _ := clusterSSHPreparationDigest(confirmation)
			if work.Authorization.ConfirmationSHA256 != want {
				t.Fatal("confirmation lost at claim")
			}
			for _, claim := range work.Claims {
				if _, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, claim.Lease, work.Baseline, claim.Sealed)(f.ctx); err != nil {
					t.Fatal("approved claim authority", err)
				}
			}
			if f.c.commitClusterSSHPreparationApproval(f.ctx, snapshot, confirmation, proof) == nil || f.events(t) != before+1 {
				t.Fatal("approval replay changed claimed operation")
			}
		})
	}
}

func TestClusterSSHPreparationApprovalPostgresFreshCoordinator(t *testing.T) {
	for _, mode := range []string{"success", "missing gate", "changed source", "changed gate"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, confirmation, _ := newSSHPreparationApprovalFixture(t, false)
			gates := sshPreparationGatesFixture()
			calls := 0
			gates[0] = func(context.Context, string) (string, error) {
				calls++
				if f.c.store.db.Stats().InUse != 0 {
					t.Error("phase gate held connection")
				}
				if mode == "changed gate" && calls > 1 {
					return strings.Repeat("f", 64), nil
				}
				return strings.Repeat("a", 64), nil
			}
			if mode == "missing gate" {
				gates[5] = nil
			}
			get := func(ctx context.Context, path string, out any) error {
				if f.c.store.db.Stats().InUse != 0 {
					t.Error("source proof held connection")
				}
				if mode == "changed source" && calls > 0 {
					return errClusterConflict
				}
				return f.observe(ctx, path, out)
			}
			err := f.c.approveClusterSSHPreparation(f.ctx, snapshot, confirmation, get, gates)
			if (err == nil) != (mode == "success") {
				t.Fatal("coordinator outcome", err)
			}
			var state string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT state FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state) != nil {
				t.Fatal("parent state")
			}
			if (state == "running") != (mode == "success") {
				t.Fatal("failed proof changed parent")
			}
		})
	}
}

func TestClusterSSHPreparationApprovalPostgresRejectsDrift(t *testing.T) {
	for _, mode := range []string{"controller", "payload", "source", "target", "credential", "expired credential", "missing target", "qualification generation", "confirmation", "missing proof", "expired proof", "wrong proof", "proof rejected"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, confirmation, proof := newSSHPreparationApprovalFixture(t, false)
			switch mode {
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other' WHERE name=$1`, clusterControllerLeaseName)
			case "payload":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
			case "source":
				f.exec(t, `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1`)
			case "target":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "credential":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.targets[1].Binding.TargetID)
			case "expired credential":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, f.targets[1].Binding.TargetID)
			case "missing target":
				f.exec(t, `DELETE FROM engine.cluster_onboarding_targets WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "qualification generation":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "confirmation":
				confirmation.PlanSHA256 = "changed"
			case "missing proof":
				proof = clusterSSHPreparationApprovalProof{}
			case "expired proof":
				proof.started = time.Now().Add(-time.Minute)
			case "wrong proof":
				proof.plan = "changed"
			case "proof rejected":
				proof.recheck = func(context.Context) error { return errClusterConflict }
			}
			before := f.events(t)
			if f.c.commitClusterSSHPreparationApproval(f.ctx, snapshot, confirmation, proof) == nil {
				t.Fatal("drift approved")
			}
			var state, step string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state, &step) != nil || state != "waiting" || step != clusterSSHQualificationStep || f.events(t) != before {
				t.Fatal("failed approval changed parent")
			}
		})
	}
}

func TestClusterSSHPreparationApprovalPostgresWaitFence(t *testing.T) {
	for _, mode := range []string{"credential expiry", "controller expiry", "proof cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, confirmation, proof := newSSHPreparationApprovalFixture(t, false)
			f.c.store.db.SetMaxOpenConns(4)
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			proofCtx, proofCancel := context.WithCancel(ctx)
			defer proofCancel()
			proof.lifetime, proof.recheck = proofCtx, func(context.Context) error { return nil }
			blocker, err := f.c.store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			if _, err = blocker.ExecContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			joined := false
			go func() { done <- f.c.commitClusterSSHPreparationApproval(ctx, snapshot, confirmation, proof) }()
			defer func() {
				blocker.Rollback()
				cancel()
				if !joined {
					<-done
				}
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var waiting bool
				if f.c.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%engine.cluster_application_leases%')`).Scan(&waiting) != nil {
					t.Fatal("wait observation")
				}
				if waiting {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("missing lock wait")
				}
			}
			switch mode {
			case "credential expiry":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, f.targets[1].Binding.TargetID)
			case "controller expiry":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
			case "proof cancelled":
				proofCancel()
			}
			if err != nil || blocker.Commit() != nil {
				t.Fatal("blocking writer", err)
			}
			if err := <-done; err == nil {
				t.Fatal("wait changed authority accepted")
			}
			joined = true
			var state string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT state FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&state) != nil || state != "waiting" {
				t.Fatal("stale approval committed")
			}
		})
	}
}
