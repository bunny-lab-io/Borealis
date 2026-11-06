package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func newSSHPreparationRequestFixture(t *testing.T) (sshPreparationAuthorityFixture, []byte) {
	t.Helper()
	f, snapshot, _, _ := newSSHPreparationApprovalFixture(t, false)
	delete(f.op.Payload, "ssh_preparation_request")
	f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(f.op.Payload))
	plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	return f, []byte(marshalClusterJSON(map[string]any{"request_id": newClusterUUID(), "plan_sha256": plan, "confirmation": "PREPARE ENGINE NODES", "source_id": snapshot.work.Proof.Source.Members[0].NodeID}))
}

func TestClusterSSHPreparationSelectedSourcePostgres(t *testing.T) {
	f, snapshot, selected, proof := newSSHPreparationApprovalFixture(t, true)
	body := map[string]any{"request_id": selected.RequestID, "plan_sha256": selected.PlanSHA256, "confirmation": "PREPARE ENGINE NODES", "source_id": selected.IdentitySource.SourceID}
	before := f.events(t)
	got, err := f.c.store.requestClusterSSHPreparation(f.ctx, f.op.ID, selected.Actor, []byte(marshalClusterJSON(body)))
	if err != nil || got != selected || f.events(t) != before {
		t.Fatal("selected source replay changed intent", err)
	}
	body["source_id"] = snapshot.work.Proof.Source.Members[0].NodeID
	if body["source_id"] == selected.IdentitySource.SourceID {
		t.Fatal("fixture lacks alternate survivor")
	}
	if _, err := f.c.store.requestClusterSSHPreparation(f.ctx, f.op.ID, selected.Actor, []byte(marshalClusterJSON(body))); err == nil {
		t.Fatal("same request changed source")
	}
	if f.events(t) != before {
		t.Fatal("rejected source emitted success event")
	}
	if err := f.c.commitClusterSSHPreparationApproval(f.ctx, snapshot, selected, proof); err != nil {
		t.Fatal(err)
	}
	f.op.State, f.op.CurrentStep = "running", clusterSSHPreparationOperationStep
	work, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID())
	if err != nil || work.IdentitySource != selected.IdentitySource {
		t.Fatal("claim changed source", err)
	}
	f.lease, f.sealed = work.Claims[0].Lease, work.Claims[0].Sealed
	authority, err := f.read(f.ctx)
	if err != nil || authority.IdentitySource == nil || *authority.IdentitySource != selected.IdentitySource {
		t.Fatal("authority lost selected source", err)
	}
	_, process, _ := existingIdentitySourceFixture(t)
	member := snapshot.work.Proof.Source.Members[1]
	process.NodeName = member.Name
	source := clusterSSHPrejoinSource{Binding: selected.IdentitySource, Process: process}
	if !source.valid(authority) {
		t.Fatal("selected source cannot prove custody")
	}
	member = snapshot.work.Proof.Source.Members[0]
	source.Binding.SourceID, source.Binding.SourceUID, source.Process.NodeName = member.NodeID, member.NodeUID, member.Name
	if source.valid(authority) {
		t.Fatal("custody permitted unselected original survivor")
	}
}

func TestClusterSSHPreparationRequestPostgresIntentAndReplay(t *testing.T) {
	f, raw := newSSHPreparationRequestFixture(t)
	before := f.events(t)
	confirmation, err := f.c.store.requestClusterSSHPreparation(f.ctx, f.op.ID, "admin", raw)
	if err != nil || f.events(t) != before+1 {
		t.Fatal("record confirmation intent", err)
	}
	replay, err := f.c.store.requestClusterSSHPreparation(f.ctx, f.op.ID, "admin", raw)
	if err != nil || replay != confirmation || f.events(t) != before+1 {
		t.Fatal("intent replay was not idempotent", err)
	}
	f.assertState(t, "waiting", clusterSSHQualificationStep, true)
	op := f.op
	op.State, op.CurrentStep = "running", clusterSSHPreparationOperationStep
	if _, err := f.c.claimClusterSSHPreparation(f.ctx, op, newClusterUUID()); err == nil {
		t.Fatal("intent granted worker leases")
	}
	for _, mode := range []string{"actor", "request", "plan"} {
		actor, candidate := "admin", raw
		if mode == "actor" {
			actor = "other-admin"
		} else {
			var request map[string]any
			_ = json.Unmarshal(raw, &request)
			if mode == "request" {
				request["request_id"] = newClusterUUID()
			} else {
				request["plan_sha256"] = strings.Repeat("f", 64)
			}
			candidate = []byte(marshalClusterJSON(request))
		}
		if _, err := f.c.store.requestClusterSSHPreparation(f.ctx, f.op.ID, actor, candidate); err == nil {
			t.Fatal("changed intent accepted", mode)
		}
	}
	snapshot, _, err := f.c.loadClusterSSHPreparationApproval(f.ctx, f.op)
	if err != nil {
		t.Fatal(err)
	}
	proof := clusterSSHPreparationApprovalProof{plan: confirmation.PlanSHA256, started: time.Now(), lifetime: f.ctx, recheck: func(context.Context) error { return nil }}
	for i := range proof.receipts {
		proof.receipts[i] = strings.Repeat("a", 64)
	}
	missing := snapshot
	var missingPayload map[string]any
	if json.Unmarshal([]byte(missing.work.Payload), &missingPayload) != nil {
		t.Fatal("invalid fixture payload")
	}
	delete(missingPayload, "ssh_preparation_request")
	missing.work.Payload = marshalClusterJSON(missingPayload)
	if f.c.commitClusterSSHPreparationApproval(f.ctx, missing, confirmation, proof) == nil {
		t.Fatal("controller approved without stored operator intent")
	}
	changed := confirmation
	changed.RequestID = newClusterUUID()
	if f.c.commitClusterSSHPreparationApproval(f.ctx, snapshot, changed, proof) == nil {
		t.Fatal("controller replaced operator intent")
	}
	if err := f.c.commitClusterSSHPreparationApproval(f.ctx, snapshot, confirmation, proof); err != nil {
		t.Fatal("exact intent and independent proof did not approve", err)
	}
}

func TestClusterSSHPreparationRequestPostgresWaitFence(t *testing.T) {
	for _, mode := range []string{"controller expiry", "credential expiry", "credential changed", "payload", "cancelled", "competing request"} {
		t.Run(mode, func(t *testing.T) {
			f, raw := newSSHPreparationRequestFixture(t)
			before := f.events(t)
			f.c.store.db.SetMaxOpenConns(4)
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			blocker, err := f.c.store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			if _, err := blocker.ExecContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			joined := false
			go func() { _, err := f.c.store.requestClusterSSHPreparation(ctx, f.op.ID, "admin", raw); done <- err }()
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
			case "controller expiry":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET expires_at=1 WHERE name=$1`, clusterControllerLeaseName)
			case "credential expiry":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, f.targets[1].Binding.TargetID)
			case "credential changed":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.targets[1].Binding.TargetID)
			case "payload":
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
			case "cancelled":
				cancel()
			case "competing request":
				f.op.Payload["ssh_preparation_request"] = map[string]any{"request_id": newClusterUUID()}
				_, err = blocker.ExecContext(ctx, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(f.op.Payload))
			}
			if mode == "cancelled" {
				_ = blocker.Rollback()
			} else if err != nil || blocker.Commit() != nil {
				t.Fatal("blocking writer", err)
			}
			if err := <-done; err == nil {
				t.Fatal("changed authority recorded intent")
			}
			joined = true
			if f.events(t) != before {
				t.Fatal("failed intent published event")
			}
		})
	}
}
