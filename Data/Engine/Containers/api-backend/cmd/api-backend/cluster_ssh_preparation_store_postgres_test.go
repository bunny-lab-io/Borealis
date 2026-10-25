package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func newSSHPreparationCompletionFixture(t *testing.T, replacement bool) (sshPreparationAuthorityFixture, []clusterSSHPreparationCompletion) {
	t.Helper()
	f := newSSHPreparationAuthorityFixtureForTopology(t, replacement)
	var raw string
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&raw) != nil {
		t.Fatal("read preparation payload")
	}
	var payload map[string]any
	if json.Unmarshal([]byte(raw), &payload) != nil {
		t.Fatal("parse preparation payload")
	}
	report := newClusterSSHQualificationReport(f.op.Attempt)
	for index := 0; index < len(report.Checks)-1; index++ {
		report.Checks[index].State = "passed"
	}
	payload["ssh_qualification"] = report
	raw = marshalClusterJSON(payload)
	f.exec(t, `UPDATE engine.cluster_operations SET state='running',current_step=$2,payload_json=$3 WHERE id=$1`,
		f.op.ID, clusterSSHPreparationOperationStep, raw)
	confirmation := strings.Repeat("a", 64)
	completions := make([]clusterSSHPreparationCompletion, 0, len(f.targets))
	for index, target := range f.targets {
		lease := clusterSSHTargetLease{TargetID: target.Binding.TargetID, OperationID: f.op.ID, Holder: newClusterUUID(),
			Generation: int64(index) + 2, Step: "stage_source", ControllerHolder: f.lease.ControllerHolder,
			OperationAttempt: f.op.Attempt, OperationStep: clusterSSHPreparationOperationStep, OperationKind: "ssh_onboarding"}
		// Generation must advance beyond original inspection, but need not match
		// another target's independently fenced worker generation.
		f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='running',current_step='stage_source',lease_holder=$2,
 lease_generation=$3,lease_expires_at=$4 WHERE id=$1`, lease.TargetID, lease.Holder, lease.Generation, time.Now().Unix()+300)
		sealed := f.targets[index].Sealed
		authority, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, lease, f.baseline, sealed)(f.ctx)
		if err != nil {
			t.Fatal("load completion authority", err)
		}
		result := clusterSSHPreparationResultFixture(lease, confirmation)
		result.StageSourceSHA256 = strings.Repeat([]string{"1", "2"}[index], 64)
		completions = append(completions, clusterSSHPreparationCompletion{Authority: authority, Sealed: sealed, Result: result})
	}
	f.op.State, f.op.CurrentStep, f.op.Payload = "running", clusterSSHPreparationOperationStep, payload
	return f, completions
}

func TestClusterSSHPreparationCompletionPostgres(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := map[bool]string{false: "paired expansion", true: "recorded replacement"}[replacement]
		t.Run(name, func(t *testing.T) {
			f, completions := newSSHPreparationCompletionFixture(t, replacement)
			before := f.events(t)
			if err := f.c.store.completeClusterSSHPreparation(f.ctx, completions); err != nil {
				t.Fatal("complete preparation", err)
			}
			if f.events(t) != before+1 {
				t.Fatal("missing preparation completion event")
			}
			rows, err := f.c.store.db.QueryContext(f.ctx, `SELECT id,state,current_step,lease_holder,lease_expires_at
 FROM engine.cluster_onboarding_targets WHERE operation_id=$1 ORDER BY ordinal`, f.op.ID)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for rows.Next() {
				var id, state, step, holder string
				var expires int64
				if rows.Scan(&id, &state, &step, &holder, &expires) != nil || state != "prepared" ||
					step != clusterSSHPreparationCompleteStep || holder != "" || expires != 0 {
					rows.Close()
					t.Fatal("preparation target not durably completed")
				}
				count++
			}
			rows.Close()
			if count != len(completions) {
				t.Fatal("partial preparation cohort")
			}
			var stored string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&stored) != nil {
				t.Fatal("read completed payload")
			}
			var receipt struct {
				Preparation clusterSSHPreparationReceipt `json:"ssh_preparation"`
			}
			if json.Unmarshal([]byte(stored), &receipt) != nil || receipt.Preparation.Version != 1 ||
				receipt.Preparation.Attempt != f.op.Attempt || len(receipt.Preparation.Targets) != len(completions) {
				t.Fatal("preparation receipt missing or incomplete")
			}
			if _, err := f.c.loadClusterSSHIdentityRecoveryReservation(f.ctx, f.op); err != nil {
				t.Fatal("completed cohort did not feed recovery reservation", err)
			}
			if err := f.c.store.completeClusterSSHPreparation(f.ctx, completions); err != nil || f.events(t) != before+1 {
				t.Fatal("same-result retry was not idempotent", err)
			}
			changed := append([]clusterSSHPreparationCompletion(nil), completions...)
			changed[0].Result.StageSourceSHA256 = strings.Repeat("f", 64)
			if f.c.store.completeClusterSSHPreparation(f.ctx, changed) == nil {
				t.Fatal("changed-result retry accepted")
			}
			f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, completions[0].Authority.Lease.TargetID)
			if f.c.store.completeClusterSSHPreparation(f.ctx, completions) == nil {
				t.Fatal("drifted-target retry accepted")
			}
		})
	}

	for _, mode := range []string{"controller", "credential", "source config", "payload digest", "missing sibling", "expired lease", "blocked qualification", "mixed confirmation"} {
		t.Run("reject "+mode, func(t *testing.T) {
			f, completions := newSSHPreparationCompletionFixture(t, false)
			switch mode {
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`, clusterControllerLeaseName)
			case "credential":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, completions[1].Authority.Lease.TargetID)
			case "source config":
				f.exec(t, `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1`)
			case "payload digest":
				completions[0].Authority.PayloadSHA256 = strings.Repeat("f", 64)
			case "missing sibling":
				f.exec(t, `DELETE FROM engine.cluster_onboarding_targets WHERE id=$1`, completions[1].Authority.Lease.TargetID)
			case "expired lease":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE id=$1`, completions[1].Authority.Lease.TargetID)
			case "blocked qualification":
				var raw string
				_ = f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&raw)
				var payload map[string]any
				_ = json.Unmarshal([]byte(raw), &payload)
				report := newClusterSSHQualificationReport(f.op.Attempt)
				report.Checks[0].State = "blocked"
				payload["ssh_qualification"] = report
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(payload))
			case "mixed confirmation":
				completions[1].Result.ConfirmationSHA256 = strings.Repeat("b", 64)
			}
			if err := f.c.store.completeClusterSSHPreparation(context.Background(), completions); err == nil {
				t.Fatal("changed authority completed preparation")
			}
			var count int
			_ = f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets
 WHERE operation_id=$1 AND state='prepared'`, f.op.ID).Scan(&count)
			if count != 0 {
				t.Fatal("partial preparation committed")
			}
		})
	}
}
