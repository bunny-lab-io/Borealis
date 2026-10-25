package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type sshIdentityRecoveryStoreFixture struct {
	sshPreparationAuthorityFixture
	recovery clusterbootstrap.IdentityRecoveryRequest
}

func newSSHIdentityRecoveryReservationFixture(t *testing.T) (sshPreparationAuthorityFixture, clusterSSHIdentityRecoveryReservation) {
	t.Helper()
	f, completions := newSSHPreparationCompletionFixture(t, false)
	if err := f.c.store.completeClusterSSHPreparation(f.ctx, completions); err != nil {
		t.Fatal("complete reservation preparation", err)
	}
	reservation, err := f.c.loadClusterSSHIdentityRecoveryReservation(f.ctx, f.op)
	if err != nil {
		t.Fatal(err)
	}
	return f, reservation
}

func clusterSSHIdentityRecoveryReservationObserver(t *testing.T, reservation clusterSSHIdentityRecoveryReservation,
	mutate func(string, []any)) func(context.Context, string, any) error {
	t.Helper()
	var items []any
	for _, member := range reservation.Proof.Source.Members {
		node := sshSourceKubernetesFixture(member)
		node["metadata"].(map[string]any)["labels"] = map[string]any{
			"borealis.io/engine-node": "true", "borealis.io/application-state": "active",
		}
		items = append(items, node)
	}
	for _, target := range reservation.Proof.Cohort.Targets {
		items = append(items, map[string]any{
			"metadata": map[string]any{"name": target.Report.Hostname, "uid": newClusterUUID(), "labels": map[string]any{
				"borealis.io/engine-node": "true", "borealis.io/application-state": "drained",
			}},
			"status": map[string]any{
				"nodeInfo":   map[string]any{"machineID": target.Report.MachineID, "bootID": target.Report.BootID},
				"addresses":  []any{map[string]any{"type": "InternalIP", "address": target.Binding.Address}},
				"conditions": []any{map[string]any{"type": "Ready", "status": "False"}},
			},
		})
	}
	namespaceReads := 0
	return func(ctx context.Context, path string, out any) error {
		if mutate != nil {
			mutate(path, items)
		}
		var value any
		switch path {
		case "/api/v1/namespaces/kube-system":
			namespaceReads++
			value = map[string]any{"metadata": map[string]any{"uid": reservation.Proof.Source.KubeSystemUID}}
		case "/api/v1/nodes":
			value = map[string]any{"metadata": map[string]any{"continue": ""}, "items": items}
		default:
			t.Fatal("unexpected recovery reservation read", path)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
}

func TestClusterSSHIdentityRecoveryReservationPostgres(t *testing.T) {
	t.Run("original controller reserves one drained target", func(t *testing.T) {
		f, reservation := newSSHIdentityRecoveryReservationFixture(t)
		observe := clusterSSHIdentityRecoveryReservationObserver(t, reservation, nil)
		before := f.events(t)
		if err := f.c.runSSHIdentityRecoveryParent(f.ctx, f.op, observe); err != nil {
			t.Fatal(err)
		}
		var state, step, raw string
		if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step,payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).
			Scan(&state, &step, &raw) != nil || state != "running" || step != clusterSSHIdentityRecoveryOperationStep {
			t.Fatalf("wrong recovery reservation operation: %s/%s", state, step)
		}
		var payload struct {
			Recovery clusterbootstrap.IdentityRecoveryRequest `json:"ssh_identity_recovery"`
		}
		if json.Unmarshal([]byte(raw), &payload) != nil || payload.Recovery.SourceID != reservation.Proof.Source.Members[0].NodeID ||
			payload.Recovery.SourceNodeUID != reservation.Proof.Source.Members[0].NodeUID || !clusterUUIDRE.MatchString(payload.Recovery.TargetNodeUID) {
			t.Fatalf("recovery reservation lost source or target Node binding: %#v", payload.Recovery)
		}
		rows, err := f.c.store.db.QueryContext(f.ctx, `SELECT id,current_step,state FROM engine.cluster_onboarding_targets
 WHERE operation_id=$1 ORDER BY ordinal`, f.op.ID)
		if err != nil {
			t.Fatal(err)
		}
		steps := map[string]string{}
		for rows.Next() {
			var id, targetStep, targetState string
			if rows.Scan(&id, &targetStep, &targetState) != nil || targetState != "prepared" {
				rows.Close()
				t.Fatal("reservation changed prepared target state")
			}
			steps[id] = targetStep
		}
		if rows.Err() != nil || rows.Close() != nil || steps[reservation.Target.Binding.TargetID] != clusterSSHIdentityRecoveryTargetStep {
			t.Fatal("selected target was not reserved")
		}
		for id, targetStep := range steps {
			if id != reservation.Target.Binding.TargetID && targetStep != clusterSSHIdentityRecoveryPendingStep {
				t.Fatal("unselected target became executable")
			}
		}
		pending, err := f.c.store.pendingClusterSSHIdentityRecoveries(f.ctx)
		if err != nil || len(pending) != 1 || pending[0].TargetID != reservation.Target.Binding.TargetID {
			t.Fatalf("worker queue did not expose exactly reserved target: %#v %v", pending, err)
		}
		claim, err := f.c.store.claimClusterSSHIdentityRecoveryTarget(f.ctx, f.op.ID, pending[0].TargetID, newClusterUUID())
		if err != nil || !claim.valid() || claim.Recovery != payload.Recovery {
			t.Fatalf("reserved target could not enter existing worker: %#v %v", claim, err)
		}
		authority, err := newClusterSSHIdentityRecoveryAuthorityRead(f.c.store, f.aegis, claim)(f.ctx)
		if err != nil || !clusterSSHIdentityRecoveryAuthorityMatches(authority, claim) {
			t.Fatalf("reserved worker lost whole-cohort authority: %#v %v", authority, err)
		}
		if f.events(t) != before+1 {
			t.Fatal("reservation event was not persisted exactly once")
		}
		current := f.op
		current.CurrentStep = clusterSSHIdentityRecoveryOperationStep
		if err := f.c.runSSHIdentityRecoveryParent(f.ctx, current, nil); err != nil || f.events(t) != before+1 {
			t.Fatalf("recovery controller advanced restart or admission: %v", err)
		}
		var activeSize, desiredSize, nodes int64
		if f.c.store.db.QueryRowContext(f.ctx, `SELECT active_size,desired_size,
 (SELECT count(*) FROM engine.cluster_nodes WHERE membership_state='Active') FROM engine.cluster_state WHERE id=1`).
			Scan(&activeSize, &desiredSize, &nodes) != nil || activeSize != 1 || desiredSize != 1 || nodes != 1 {
			t.Fatal("reservation changed membership")
		}
		if f.c.store.db.Stats().InUse != 0 {
			t.Fatal("reservation retained database connection")
		}
	})

	for _, mode := range []string{"target not drained", "source not active", "qualification blocked", "preparation receipt missing", "preparation generation changed", "target not prepared", "credential expired", "controller changed", "operation changed", "target report changed"} {
		t.Run(mode, func(t *testing.T) {
			f, reservation := newSSHIdentityRecoveryReservationFixture(t)
			mutated := false
			mutate := func(path string, items []any) {
				if mutated || path != "/api/v1/nodes" {
					return
				}
				switch mode {
				case "target not drained":
					for _, raw := range items {
						node := raw.(map[string]any)
						if node["metadata"].(map[string]any)["name"] == reservation.Target.Report.Hostname {
							node["metadata"].(map[string]any)["labels"].(map[string]any)["borealis.io/application-state"] = "active"
						}
					}
				case "source not active":
					items[0].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)["borealis.io/application-state"] = "drained"
				case "controller changed":
					f.exec(t, `UPDATE engine.cluster_application_leases SET holder=$2 WHERE name=$1`, clusterControllerLeaseName, "replacement-controller")
				case "operation changed":
					f.exec(t, `UPDATE engine.cluster_operations SET payload_json=jsonb_set(payload_json::jsonb,'{changed}','true'::jsonb)::text WHERE id=$1`, f.op.ID)
				case "target report changed":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, reservation.Target.Binding.TargetID)
				}
				mutated = true
			}
			switch mode {
			case "qualification blocked":
				var raw string
				if f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&raw) != nil {
					t.Fatal("read qualification")
				}
				var payload map[string]any
				_ = json.Unmarshal([]byte(raw), &payload)
				report := newClusterSSHQualificationReport(f.op.Attempt)
				report.Checks[0].State = "blocked"
				payload["ssh_qualification"] = report
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(payload))
			case "preparation receipt missing":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=(payload_json::jsonb-'ssh_preparation')::text WHERE id=$1`, f.op.ID)
			case "preparation generation changed":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, reservation.Target.Binding.TargetID)
			case "target not prepared":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='queued' WHERE operation_id=$1`, f.op.ID)
			case "credential expired":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, reservation.Target.Binding.TargetID)
			}
			before := f.events(t)
			observe := clusterSSHIdentityRecoveryReservationObserver(t, reservation, mutate)
			if err := f.c.runSSHIdentityRecoveryParent(context.Background(), f.op, observe); err == nil {
				t.Fatal("unsafe identity recovery reservation accepted")
			}
			var step string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT current_step FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&step) != nil ||
				step != clusterSSHPreparationOperationStep || f.events(t) != before {
				t.Fatal("rejected reservation changed controller state")
			}
			var reserved int
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1
 AND current_step IN ($2,$3)`, f.op.ID, clusterSSHIdentityRecoveryTargetStep, clusterSSHIdentityRecoveryPendingStep).Scan(&reserved) != nil || reserved != 0 {
				t.Fatal("rejected reservation exposed worker authority")
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("rejected reservation retained database connection")
			}
		})
	}
}

func newSSHIdentityRecoveryStoreFixture(t *testing.T) sshIdentityRecoveryStoreFixture {
	t.Helper()
	f, completions := newSSHPreparationCompletionFixture(t, false)
	if err := f.c.store.completeClusterSSHPreparation(f.ctx, completions); err != nil {
		t.Fatal("complete worker preparation", err)
	}
	authority := completions[0].Authority
	source := authority.Source.Members[0]
	recovery := clusterbootstrap.IdentityRecoveryRequest{SourceID: source.NodeID, SourceNodeUID: source.NodeUID, TargetNodeUID: newClusterUUID()}
	var raw string
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&raw) != nil {
		t.Fatal("read recovery payload")
	}
	var payload map[string]any
	if json.Unmarshal([]byte(raw), &payload) != nil {
		t.Fatal("parse recovery payload")
	}
	payload["ssh_identity_recovery"] = recovery
	f.exec(t, `UPDATE engine.cluster_operations SET current_step=$2,payload_json=$3 WHERE id=$1`, f.op.ID, clusterSSHIdentityRecoveryOperationStep, marshalClusterJSON(payload))
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='prepared',current_step=$2,lease_holder='',lease_expires_at=0 WHERE operation_id=$1`, f.op.ID, clusterSSHIdentityRecoveryTargetStep)
	return sshIdentityRecoveryStoreFixture{sshPreparationAuthorityFixture: f, recovery: recovery}
}

func TestClusterSSHIdentityRecoveryAuthorityPostgresBindsWholeCohort(t *testing.T) {
	f := newSSHIdentityRecoveryStoreFixture(t)
	claim, err := f.c.store.claimClusterSSHIdentityRecoveryTarget(f.ctx, f.op.ID, f.lease.TargetID, newClusterUUID())
	if err != nil {
		t.Fatal(err)
	}
	read := newClusterSSHIdentityRecoveryAuthorityRead(f.c.store, f.aegis, claim)
	authority, err := read(f.ctx)
	if err != nil || !clusterSSHIdentityRecoveryAuthorityMatches(authority, claim) {
		t.Fatalf("exact recovery authority failed: %#v %v", authority, err)
	}
	if f.c.store.db.Stats().InUse != 0 {
		t.Fatal("recovery authority retained database connection")
	}
	var sibling string
	if f.c.store.db.QueryRowContext(f.ctx, `SELECT id FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND id<>$2 ORDER BY id LIMIT 1`, claim.Lease.OperationID, claim.Lease.TargetID).Scan(&sibling) != nil {
		t.Fatal("missing sibling target")
	}
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='recovery_required' WHERE id=$1`, sibling)
	if value, err := read(context.Background()); err == nil || !reflect.DeepEqual(value, clusterSSHPreparationAuthority{}) {
		t.Fatalf("changed sibling authority accepted: %#v %v", value, err)
	}
}

func TestClusterSSHIdentityRecoveryRuntimePostgresSelectsOnlyReservedWork(t *testing.T) {
	f := newSSHIdentityRecoveryStoreFixture(t)
	pending, err := f.c.store.pendingClusterSSHIdentityRecoveries(f.ctx)
	if err != nil || len(pending) != 1 || pending[0].OperationID != f.op.ID || pending[0].TargetID != f.targets[0].Binding.TargetID {
		t.Fatalf("reserved recovery work not selected exactly: %#v %v", pending, err)
	}
	claim, err := f.c.store.claimClusterSSHIdentityRecoveryTarget(f.ctx, pending[0].OperationID, pending[0].TargetID, newClusterUUID())
	if err != nil || !claim.valid() {
		t.Fatalf("selected recovery work could not be claimed: %#v %v", claim, err)
	}
	pending, err = f.c.store.pendingClusterSSHIdentityRecoveries(f.ctx)
	if err != nil || len(pending) != 1 || pending[0].TargetID == claim.Lease.TargetID {
		t.Fatalf("runtime reselected leased target or lost sibling: %#v %v", pending, err)
	}
	f.exec(t, `UPDATE engine.cluster_operations SET current_step='prepare_ssh_targets' WHERE id=$1`, f.op.ID)
	pending, err = f.c.store.pendingClusterSSHIdentityRecoveries(f.ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("non-recovery operation exposed mutating work: %#v %v", pending, err)
	}
	if f.c.store.db.Stats().InUse != 0 {
		t.Fatal("recovery runtime query retained database connection")
	}
}

func TestClusterSSHIdentityRecoveryClaimPostgresBindsAuthorityAndRetry(t *testing.T) {
	f := newSSHIdentityRecoveryStoreFixture(t)
	holder := newClusterUUID()
	claim, err := f.c.store.claimClusterSSHIdentityRecoveryTarget(f.ctx, f.op.ID, f.lease.TargetID, holder)
	if err != nil || !claim.valid() {
		t.Fatalf("claim failed: %#v %v", claim, err)
	}
	if claim.Recovery != f.recovery || claim.Source.SourceID != f.recovery.SourceID || claim.Source.SourceUID != f.recovery.SourceNodeUID ||
		claim.Binding.TargetID != f.lease.TargetID || claim.Binding.HolderID != holder || claim.Binding.Generation != claim.Lease.Generation ||
		claim.Key.Fingerprint != f.targets[0].Key.Fingerprint || claim.Sealed.binding != f.targets[0].Binding {
		t.Fatal("claim lost exact source, target, key, credential or lease binding")
	}
	if f.c.store.db.Stats().InUse != 0 {
		t.Fatal("claim retained database connection")
	}
	if _, err := f.c.store.claimClusterSSHIdentityRecoveryTarget(f.ctx, f.op.ID, f.lease.TargetID, newClusterUUID()); err == nil {
		t.Fatal("unexpired recovery claim was stolen")
	}
	if err := f.c.store.renewClusterSSHIdentityRecoveryTarget(f.ctx, claim); err != nil {
		t.Fatalf("recovery renewal failed: %v", err)
	}
	if err := f.c.store.renewClusterSSHPreparationTarget(f.ctx, claim.Lease, claim.Sealed); err == nil {
		t.Fatal("recovery claim entered preparation renewal")
	}
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='recovery_required',lease_expires_at=1 WHERE id=$1`, f.lease.TargetID)
	retry, err := f.c.store.claimClusterSSHIdentityRecoveryTarget(f.ctx, f.op.ID, f.lease.TargetID, newClusterUUID())
	if err != nil || !retry.valid() || retry.Lease.Generation != claim.Lease.Generation+1 || retry.Recovery != claim.Recovery || retry.Source != claim.Source {
		t.Fatalf("safe recovery reclaim failed: %#v %v", retry, err)
	}
}

func TestClusterSSHIdentityRecoveryClaimPostgresRejectsChangedEvidence(t *testing.T) {
	for _, mode := range []string{"operation phase", "target phase", "target state", "prepared generation", "controller expiry", "credential expiry", "host key", "source drained", "source topology", "missing recovery", "wrong source", "wrong target Node"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHIdentityRecoveryStoreFixture(t)
			switch mode {
			case "operation phase":
				f.exec(t, `UPDATE engine.cluster_operations SET current_step='qualify_ssh_targets' WHERE id=$1`, f.op.ID)
			case "target phase":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET current_step='qualification_complete' WHERE id=$1`, f.lease.TargetID)
			case "target state":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='joined' WHERE id=$1`, f.lease.TargetID)
			case "prepared generation":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, f.lease.TargetID)
			case "controller expiry":
				f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
			case "credential expiry":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, f.lease.TargetID)
			case "host key":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET host_key_fingerprint=$2 WHERE id=$1`, f.lease.TargetID, "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
			case "source drained":
				f.exec(t, `UPDATE engine.cluster_nodes SET application_state='drained' WHERE membership_state='Active'`)
			case "source topology":
				f.exec(t, `UPDATE engine.cluster_state SET status='Degraded Quorum' WHERE id=1`)
			case "missing recovery":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=(payload_json::jsonb-'ssh_identity_recovery')::text WHERE id=$1`, f.op.ID)
			case "wrong source":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=jsonb_set(payload_json::jsonb,'{ssh_identity_recovery,source_node_id}',to_jsonb($2::text))::text WHERE id=$1`, f.op.ID, newClusterUUID())
			case "wrong target Node":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=jsonb_set(payload_json::jsonb,'{ssh_identity_recovery,target_node_uid}',to_jsonb((payload_json::jsonb#>>'{ssh_identity_recovery,source_node_uid}')::text))::text WHERE id=$1`, f.op.ID)
			}
			claim, err := f.c.store.claimClusterSSHIdentityRecoveryTarget(context.Background(), f.op.ID, f.lease.TargetID, newClusterUUID())
			if err != errClusterConflict || !reflect.DeepEqual(claim, clusterSSHIdentityRecoveryClaim{}) {
				t.Fatalf("changed recovery evidence accepted: %#v %v", claim, err)
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("rejected claim retained database connection")
			}
		})
	}
}

func TestClusterSSHIdentityRecoverySettlementPostgresPersistsExactOutcomes(t *testing.T) {
	for _, complete := range []bool{true, false} {
		name := "recovery required"
		if complete {
			name = "complete"
		}
		t.Run(name, func(t *testing.T) {
			f := newSSHIdentityRecoveryStoreFixture(t)
			claim, err := f.c.store.claimClusterSSHIdentityRecoveryTarget(f.ctx, f.op.ID, f.lease.TargetID, newClusterUUID())
			if err != nil {
				t.Fatal(err)
			}
			settle := f.c.store.requireClusterSSHIdentityRecovery
			wantState, wantStep, wantHolder, event := "recovery_required", clusterSSHIdentityRecoveryTargetStep, claim.Lease.Holder, "ssh_identity_recovery_required"
			if complete {
				settle = f.c.store.completeClusterSSHIdentityRecovery
				wantState, wantStep, wantHolder, event = "prepared", clusterSSHIdentityRecoveryCompleteStep, "", "ssh_identity_recovered"
			}
			if err := settle(f.ctx, claim); err != nil {
				t.Fatalf("settlement failed: %v", err)
			}
			var state, step, holder string
			var generation, expires int64
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step,lease_holder,lease_generation,lease_expires_at
 FROM engine.cluster_onboarding_targets WHERE id=$1`, claim.Lease.TargetID).Scan(&state, &step, &holder, &generation, &expires) != nil ||
				state != wantState || step != wantStep || holder != wantHolder || generation != claim.Lease.Generation || (complete && expires != 0) || (!complete && expires <= 1) {
				t.Fatalf("wrong settlement row: state=%s step=%s holder=%s generation=%d expires=%d", state, step, holder, generation, expires)
			}
			if err := settle(f.ctx, claim); err != nil {
				t.Fatalf("same settlement was not idempotent: %v", err)
			}
			var events int
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type=$2`, claim.Lease.OperationID, event).Scan(&events) != nil || events != 1 {
				t.Fatalf("wrong settlement event count: %d", events)
			}
			other := f.c.store.completeClusterSSHIdentityRecovery
			if complete {
				other = f.c.store.requireClusterSSHIdentityRecovery
			}
			if err := other(f.ctx, claim); err != errClusterConflict {
				t.Fatalf("opposite settlement accepted: %v", err)
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("settlement retained database connection")
			}
		})
	}
}

func TestClusterSSHIdentityRecoverySettlementPostgresRejectsChangedAuthority(t *testing.T) {
	for _, outcome := range []string{"running", "complete", "recovery required"} {
		for _, mode := range []string{"operation payload", "target report", "credential", "credential state", "controller", "lease", "host key", "baseline"} {
			t.Run(outcome+"/"+mode, func(t *testing.T) {
				f := newSSHIdentityRecoveryStoreFixture(t)
				claim, err := f.c.store.claimClusterSSHIdentityRecoveryTarget(f.ctx, f.op.ID, f.lease.TargetID, newClusterUUID())
				if err != nil {
					t.Fatal(err)
				}
				settle := f.c.store.completeClusterSSHIdentityRecovery
				wantState, wantStep := "running", clusterSSHIdentityRecoveryTargetStep
				if outcome != "running" {
					wantState, wantStep = "prepared", clusterSSHIdentityRecoveryCompleteStep
					if outcome == "recovery required" {
						settle = f.c.store.requireClusterSSHIdentityRecovery
						wantState, wantStep = "recovery_required", clusterSSHIdentityRecoveryTargetStep
					}
					if err := settle(f.ctx, claim); err != nil {
						t.Fatalf("initial settlement failed: %v", err)
					}
				}
				beforeEvents := f.events(t)
				switch mode {
				case "operation payload":
					f.exec(t, `UPDATE engine.cluster_operations SET payload_json=jsonb_set(payload_json::jsonb,'{changed}','true'::jsonb)::text WHERE id=$1`, claim.Lease.OperationID)
				case "target report":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, claim.Lease.TargetID)
				case "credential":
					f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=$2 WHERE target_id=$1`, claim.Lease.TargetID, aegisEnvelopePrefix+"changed")
				case "credential state":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET credential_state='required' WHERE id=$1`, claim.Lease.TargetID)
				case "controller":
					f.exec(t, `UPDATE engine.cluster_application_leases SET holder=$2 WHERE name=$1`, clusterControllerLeaseName, "changed-controller")
				case "lease":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, claim.Lease.TargetID)
				case "host key":
					f.exec(t, `UPDATE engine.cluster_onboarding_targets SET host_key_fingerprint=$2 WHERE id=$1`, claim.Lease.TargetID, "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
				case "baseline":
					f.exec(t, `UPDATE engine.cluster_state SET baseline_sha=$1 WHERE id=1`, strings.Repeat("f", 40))
				}
				if err := settle(context.Background(), claim); err != errClusterConflict {
					t.Fatalf("changed authority completed: %v", err)
				}
				var state, step string
				if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step FROM engine.cluster_onboarding_targets WHERE id=$1`, claim.Lease.TargetID).Scan(&state, &step) != nil || state != wantState || step != wantStep {
					t.Fatalf("rejected settlement changed target: %s/%s", state, step)
				}
				if f.events(t) != beforeEvents {
					t.Fatal("rejected settlement added an event")
				}
				if f.c.store.db.Stats().InUse != 0 {
					t.Fatal("rejected settlement retained database connection")
				}
			})
		}
	}
}
