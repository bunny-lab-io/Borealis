package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHWorkerPostgresOwnedInspection(t *testing.T) {
	for _, mode := range []string{"success", "locked before connect", "locked during SSH", "controller changed during SSH", "remote diagnostic"} {
		t.Run(mode, func(t *testing.T) {
			store, aegis, ctx, operationID, targets, envelopes := clusterSSHCredentialsFixture(t)
			insertSSHFixtureTargets(t, store, ctx, operationID, targets)
			lease, err := store.claimClusterSSHTarget(ctx, operationID, targets[0].Binding.TargetID, newClusterUUID())
			if err != nil {
				t.Fatal(err)
			}
			worker := newClusterSSHInspectionWorker(store, aegis)
			worker.renewInterval = time.Hour
			if mode == "locked during SSH" {
				worker.renewInterval = 10 * time.Millisecond
			}
			client := &clusterSSHWorkerTestClient{}
			connected := false
			worker.connect = func(_ context.Context, target clusterremote.Target, key clusterremote.HostKey, _ *clusterremote.Credential) (clusterSSHInspectionClient, error) {
				connected = true
				if mode != "locked during SSH" && store.db.Stats().InUse != 0 {
					t.Error("SSH started with database connection checked out")
				}
				if target.Address != targets[0].Binding.Address || target.Port != targets[0].Binding.Port || key.Fingerprint != targets[0].Key.Fingerprint {
					t.Error("SSH target binding changed")
				}
				return client, nil
			}
			client.beforeInspect = func(context.Context) error {
				if mode != "locked during SSH" && store.db.Stats().InUse != 0 {
					t.Error("inspection held database connection")
				}
				return nil
			}
			client.beforePrivileged = func(remoteCtx context.Context, secret []byte) error {
				if string(secret) != envelopes[0].SudoPassword {
					t.Error("sudo secret syntax changed")
				}
				if mode != "locked during SSH" && store.db.Stats().InUse != 0 {
					t.Error("privileged inspection held database connection")
				}
				switch mode {
				case "locked during SSH":
					aegis.clearActiveKey()
					select {
					case <-remoteCtx.Done():
						return remoteCtx.Err()
					case <-time.After(time.Second):
						t.Error("locked worker did not cancel SSH")
						return errors.New("timeout")
					}
				case "controller changed during SSH":
					if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET holder=$1 WHERE name=$2`, newClusterUUID(), clusterControllerLeaseName); err != nil {
						t.Error(err)
					}
				case "remote diagnostic":
					return errors.New(envelopes[0].Password)
				}
				return nil
			}
			if mode == "locked before connect" {
				aegis.clearActiveKey()
			}
			inspectionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			err = worker.inspect(inspectionCtx, lease)
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err != errClusterSSHInspectionWorker {
				t.Fatalf("unfenced/private failure: %v", err)
			}
			if mode == "locked before connect" && connected {
				t.Fatal("locked Aegis reached SSH")
			}
			if connected && !client.closed.Load() {
				t.Fatal("worker leaked SSH client")
			}
			var step, raw, holder, state string
			var inspectedAt int64
			if err := store.db.QueryRowContext(ctx, `SELECT current_step,inspection_json,inspected_at,lease_holder,state FROM engine.cluster_onboarding_targets WHERE id=$1`, lease.TargetID).Scan(&step, &raw, &inspectedAt, &holder, &state); err != nil {
				t.Fatal(err)
			}
			var events int
			if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type='ssh_target_inspected'`, operationID).Scan(&events); err != nil {
				t.Fatal(err)
			}
			if mode == "success" {
				var report clusterSSHInspectionReport
				if json.Unmarshal([]byte(raw), &report) != nil || report.valid() != nil || step != "inspection_complete" || inspectedAt == 0 || holder != "" || state != "queued" || events != 1 {
					t.Fatal("public inspection result was not committed atomically")
				}
				if strings.Contains(raw, envelopes[0].Password) || strings.Contains(raw, envelopes[0].SudoPassword) || strings.Contains(raw, targets[0].Sealed.ciphertext) {
					t.Fatal("credential reached public report")
				}
				if err := store.completeClusterSSHInspection(ctx, lease, targets[0].Sealed.generation, report); err == nil {
					t.Fatal("completed lease replay wrote another result")
				}
				// Credential cleanup must retain this public outcome and step.
				if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, lease.TargetID); err != nil {
					t.Fatal(err)
				}
				if err := store.cleanupClusterSSHCredentials(ctx); err != nil {
					t.Fatal(err)
				}
				var retained string
				if err := store.db.QueryRowContext(ctx, `SELECT inspection_json FROM engine.cluster_onboarding_targets WHERE id=$1`, lease.TargetID).Scan(&retained); err != nil || retained != raw {
					t.Fatal("credential expiry erased inspection evidence")
				}
			} else if step != "inspect" || raw != "{}" || inspectedAt != 0 || events != 0 {
				t.Fatal("failed/fenced worker committed inspection")
			}
		})
	}
}

func TestClusterSSHWorkerPostgresInspectionCompletionFences(t *testing.T) {
	for _, mode := range []string{"holder", "generation", "expired target", "target step", "target state", "credential state", "controller holder", "expired controller", "operation state", "operation kind", "operation attempt", "operation step", "active operation", "expired credential", "Aegis generation", "replaced credential generation"} {
		t.Run(mode, func(t *testing.T) {
			store, _, ctx, operationID, targets, _ := clusterSSHCredentialsFixture(t)
			insertSSHFixtureTargets(t, store, ctx, operationID, targets)
			lease, err := store.claimClusterSSHTarget(ctx, operationID, targets[0].Binding.TargetID, newClusterUUID())
			if err != nil {
				t.Fatal(err)
			}
			query := ""
			var args []any
			switch mode {
			case "holder":
				lease.Holder = newClusterUUID()
			case "generation":
				lease.Generation++
			case "expired target":
				query = `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE id=$1`
				args = []any{lease.TargetID}
			case "target step":
				query = `UPDATE engine.cluster_onboarding_targets SET current_step='prepare' WHERE id=$1`
				args = []any{lease.TargetID}
			case "target state":
				query = `UPDATE engine.cluster_onboarding_targets SET state='failed' WHERE id=$1`
				args = []any{lease.TargetID}
			case "credential state":
				query = `UPDATE engine.cluster_onboarding_targets SET credential_state='required' WHERE id=$1`
				args = []any{lease.TargetID}
			case "controller holder":
				query = `UPDATE engine.cluster_application_leases SET holder=$1 WHERE name=$2`
				args = []any{newClusterUUID(), clusterControllerLeaseName}
			case "expired controller":
				query = `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`
				args = []any{clusterControllerLeaseName}
			case "operation state":
				query = `UPDATE engine.cluster_operations SET state='waiting' WHERE id=$1`
				args = []any{operationID}
			case "operation kind":
				query = `UPDATE engine.cluster_operations SET kind='engine_update' WHERE id=$1`
				args = []any{operationID}
			case "operation attempt":
				query = `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`
				args = []any{operationID}
			case "operation step":
				query = `UPDATE engine.cluster_operations SET current_step='prepare_targets' WHERE id=$1`
				args = []any{operationID}
			case "active operation":
				query = `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1`
			case "expired credential":
				query = `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`
				args = []any{lease.TargetID}
			case "Aegis generation":
				query = `UPDATE engine.aegis_cipher_state SET verification_token='changed-generation' WHERE id=1`
			case "replaced credential generation":
				query = `WITH replaced AS (UPDATE engine.aegis_cipher_state SET verification_token='changed-generation' WHERE id=1 RETURNING verification_token)
				UPDATE engine.cluster_onboarding_credentials SET aegis_generation=replaced.verification_token FROM replaced WHERE target_id=$1`
				args = []any{lease.TargetID}
			}
			if query != "" {
				if _, err := store.db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			host, privileged := sshWorkerFacts()
			report, err := clusterSSHReport(host, privileged, targets[0].Binding.Address)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.completeClusterSSHInspection(ctx, lease, targets[0].Sealed.generation, report); err != errClusterConflict {
				t.Fatalf("stale authority accepted or query invalid: %v", err)
			}
			if mode == "replaced credential generation" {
				if err := store.renewClusterSSHTargetGeneration(ctx, lease, targets[0].Sealed.generation); err != errClusterConflict {
					t.Fatal("worker renewed using replaced credential generation")
				}
			}
			var raw string
			if err := store.db.QueryRowContext(ctx, `SELECT inspection_json FROM engine.cluster_onboarding_targets WHERE id=$1`, lease.TargetID).Scan(&raw); err != nil || raw != "{}" {
				t.Fatal("stale result persisted")
			}
		})
	}
}

func TestClusterSSHWorkerPostgresRuntimeClaimsOnlyExplicitInspection(t *testing.T) {
	store, aegis, ctx, operationID, targets, _ := clusterSSHCredentialsFixture(t)
	insertSSHFixtureTargets(t, store, ctx, operationID, targets)
	if pending, err := store.pendingClusterSSHInspections(ctx); err != nil || len(pending) != 0 {
		t.Fatal("ordinary preflight exposed SSH work")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET current_step=$1 WHERE id=$2`, clusterSSHInspectionOperationStep, operationID); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.pendingClusterSSHInspections(ctx); err != nil || len(pending) != 2 {
		t.Fatal("explicit inspection did not expose cohort")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET kind='engine_update' WHERE id=$1`, operationID); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.pendingClusterSSHInspections(ctx); err != nil || len(pending) != 0 {
		t.Fatal("unrelated operation exposed SSH work")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET kind='ssh_onboarding' WHERE id=$1`, operationID); err != nil {
		t.Fatal(err)
	}
	worker := newClusterSSHInspectionWorker(store, aegis)
	worker.renewInterval = time.Hour
	var calls atomic.Int32
	worker.connect = func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHInspectionClient, error) {
		calls.Add(1)
		return &clusterSSHWorkerTestClient{}, nil
	}
	cold := &clusterSSHInspectionRuntime{store: store, aegis: newGoAegisService(store.db, nil), worker: worker}
	if err := cold.runOnce(ctx); err != nil || calls.Load() != 0 {
		t.Fatal("locked API runtime started SSH")
	}
	store.db.SetMaxOpenConns(2)
	r := &clusterSSHInspectionRuntime{store: store, aegis: aegis, worker: worker}
	var done sync.WaitGroup
	for n := 0; n < 2; n++ {
		done.Add(1)
		go func() {
			defer done.Done()
			if err := r.runOnce(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	done.Wait()
	if calls.Load() != 2 {
		t.Fatalf("replicas duplicated or lost cohort execution: %d", calls.Load())
	}
	var complete, events int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND current_step='inspection_complete' AND state='queued' AND inspected_at>0`, operationID).Scan(&complete); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type='ssh_target_inspected'`, operationID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if complete != 2 || events != 2 {
		t.Fatal("cohort public outcomes missing or duplicated")
	}
	var state, step string
	if err := store.db.QueryRowContext(ctx, `SELECT state,current_step FROM engine.cluster_operations WHERE id=$1`, operationID).Scan(&state, &step); err != nil || state != "running" || step != clusterSSHInspectionOperationStep {
		t.Fatal("API worker advanced membership controller operation")
	}
	if err := r.runOnce(ctx); err != nil || calls.Load() != 2 {
		t.Fatal("completed inspections were replayed")
	}
	if pending, err := store.pendingClusterSSHInspections(ctx); err != nil || len(pending) != 0 {
		t.Fatal("completed target remained dispatchable")
	}
}
