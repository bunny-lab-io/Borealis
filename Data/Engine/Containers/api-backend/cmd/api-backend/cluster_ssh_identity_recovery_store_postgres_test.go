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

func newSSHIdentityRecoveryStoreFixture(t *testing.T) sshIdentityRecoveryStoreFixture {
	t.Helper()
	f := newSSHPreparationAuthorityFixture(t)
	authority, err := f.read(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
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
	f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='prepared',current_step=$2,lease_holder='',lease_expires_at=0 WHERE id=$1`, f.lease.TargetID, clusterSSHIdentityRecoveryTargetStep)
	return sshIdentityRecoveryStoreFixture{sshPreparationAuthorityFixture: f, recovery: recovery}
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
	for _, mode := range []string{"operation phase", "target phase", "target state", "controller expiry", "credential expiry", "host key", "source drained", "source topology", "missing recovery", "wrong source", "wrong target Node"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHIdentityRecoveryStoreFixture(t)
			switch mode {
			case "operation phase":
				f.exec(t, `UPDATE engine.cluster_operations SET current_step='qualify_ssh_targets' WHERE id=$1`, f.op.ID)
			case "target phase":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET current_step='qualification_complete' WHERE id=$1`, f.lease.TargetID)
			case "target state":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='joined' WHERE id=$1`, f.lease.TargetID)
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
	for _, mode := range []string{"operation payload", "target report", "credential", "controller", "lease", "host key", "baseline"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHIdentityRecoveryStoreFixture(t)
			claim, err := f.c.store.claimClusterSSHIdentityRecoveryTarget(f.ctx, f.op.ID, f.lease.TargetID, newClusterUUID())
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "operation payload":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json=jsonb_set(payload_json::jsonb,'{changed}','true'::jsonb)::text WHERE id=$1`, claim.Lease.OperationID)
			case "target report":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, claim.Lease.TargetID)
			case "credential":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=$2 WHERE target_id=$1`, claim.Lease.TargetID, aegisEnvelopePrefix+"changed")
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder=$2 WHERE name=$1`, clusterControllerLeaseName, "changed-controller")
			case "lease":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, claim.Lease.TargetID)
			case "host key":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET host_key_fingerprint=$2 WHERE id=$1`, claim.Lease.TargetID, "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
			case "baseline":
				f.exec(t, `UPDATE engine.cluster_state SET baseline_sha=$2 WHERE id=1`, strings.Repeat("f", 40))
			}
			if err := f.c.store.completeClusterSSHIdentityRecovery(context.Background(), claim); err != errClusterConflict {
				t.Fatalf("changed authority completed: %v", err)
			}
			var state, step string
			if f.c.store.db.QueryRowContext(f.ctx, `SELECT state,current_step FROM engine.cluster_onboarding_targets WHERE id=$1`, claim.Lease.TargetID).Scan(&state, &step) != nil || state != "running" || step != clusterSSHIdentityRecoveryTargetStep {
				t.Fatalf("rejected settlement changed target: %s/%s", state, step)
			}
			if f.c.store.db.Stats().InUse != 0 {
				t.Fatal("rejected settlement retained database connection")
			}
		})
	}
}
