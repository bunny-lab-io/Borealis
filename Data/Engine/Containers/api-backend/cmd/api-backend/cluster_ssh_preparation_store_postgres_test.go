package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func newSSHPreparationCompletionFixture(t *testing.T, replacement bool) (sshPreparationAuthorityFixture, []clusterSSHPreparationCompletion) {
	t.Helper()
	f, snapshot, confirmation, proof := newSSHPreparationApprovalFixture(t, replacement)
	if err := f.c.commitClusterSSHPreparationApproval(f.ctx, snapshot, confirmation, proof); err != nil {
		t.Fatal("approve completion fixture", err)
	}
	f.op.State, f.op.CurrentStep = "running", clusterSSHPreparationOperationStep
	work, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID())
	if err != nil {
		t.Fatal("claim completion fixture", err)
	}
	if json.Unmarshal([]byte(work.Payload), &f.op.Payload) != nil {
		t.Fatal("completion fixture payload")
	}
	f.lease, f.sealed = work.Claims[0].Lease, work.Claims[0].Sealed
	completions := make([]clusterSSHPreparationCompletion, 0, len(f.targets))
	for index, claim := range work.Claims {
		lease, sealed := claim.Lease, claim.Sealed
		authority, err := newClusterSSHPreparationAuthorityRead(f.c.store, f.aegis, lease, f.baseline, sealed)(f.ctx)
		if err != nil {
			t.Fatal("load completion authority", err)
		}
		result := clusterSSHPreparationResultFixture(lease, work.Authorization.ConfirmationSHA256)
		result.StageSourceSHA256 = strings.Repeat([]string{"1", "2"}[index], 64)
		completions = append(completions, clusterSSHPreparationCompletion{Authority: authority, Sealed: sealed, Result: result})
	}
	return f, completions
}

func TestClusterSSHPreparationCompletionPostgres(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := map[bool]string{false: "paired expansion", true: "recorded replacement"}[replacement]
		t.Run(name, func(t *testing.T) {
			f, completions := newSSHPreparationCompletionFixture(t, replacement)
			// Independent authority reads may cross a database second. Exclude
			// observation clocks only; retain exact inspected identity/timestamps.
			if len(completions) > 1 {
				completions[1].Authority.Cohort.ObservedAt++
			}
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

	for _, mode := range []string{"controller", "credential", "source config", "missing source", "changed sibling source", "payload digest", "missing sibling", "expired lease", "blocked qualification", "mixed confirmation", "unapproved confirmation", "unreserved generation", "missing authorization"} {
		t.Run("reject "+mode, func(t *testing.T) {
			f, completions := newSSHPreparationCompletionFixture(t, false)
			switch mode {
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`, clusterControllerLeaseName)
			case "credential":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, completions[1].Authority.Lease.TargetID)
			case "source config":
				f.exec(t, `UPDATE engine.cluster_state SET config_json='{}' WHERE id=1`)
			case "missing source":
				completions[0].Authority.IdentitySource = nil
			case "changed sibling source":
				selected := *completions[1].Authority.IdentitySource
				selected.SourceUID = newClusterUUID()
				completions[1].Authority.IdentitySource = &selected
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
			case "unapproved confirmation":
				for i := range completions {
					completions[i].Result.ConfirmationSHA256 = strings.Repeat("b", 64)
				}
			case "unreserved generation":
				completions[0].Authority.Lease.Generation++
				completions[0].Result.Generation++
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, completions[0].Authority.Lease.TargetID)
			case "missing authorization":
				delete(f.op.Payload, "ssh_preparation_authorization")
				raw := marshalClusterJSON(f.op.Payload)
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, raw)
				for i := range completions {
					completions[i].Authority.payload = raw
					digest := sha256.Sum256([]byte(raw))
					completions[i].Authority.PayloadSHA256 = hex.EncodeToString(digest[:])
				}
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
