package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type sshParentFixture struct {
	c       *clusterController
	ctx     context.Context
	op      clusterControllerOperation
	targets []clusterSSHPlannedTarget
	reports []clusterSSHInspectionReport
	observe func(context.Context, string, any) error
}

func newSSHParentFixture(t *testing.T) sshParentFixture {
	t.Helper()
	store, _, ctx, id, targets, _ := clusterSSHCredentialsFixture(t)
	insertSSHFixtureTargets(t, store, ctx, id, targets)
	var holder string
	if err := store.db.QueryRowContext(ctx, `SELECT holder FROM engine.cluster_application_leases WHERE name=$1`, clusterControllerLeaseName).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_state SET edge_vip=control_plane_vip WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	source, err := store.loadClusterSSHSourceCohort(ctx, id, holder, 1)
	if err != nil {
		t.Fatal(err)
	}
	member := source.Members[0]
	member.NodeUID, member.BootID, member.MachineID = newClusterUUID(), newClusterUUID(), strings.Repeat("b", 32)
	namespace := newClusterUUID()
	f := sshParentFixture{c: &clusterController{store: store, holder: holder, now: time.Now}, ctx: ctx,
		op: clusterControllerOperation{ID: id, Kind: "ssh_onboarding", State: "running", CurrentStep: clusterSSHInspectionOperationStep, Attempt: 1, Payload: map[string]any{}}, targets: targets}
	sample, _ := sshInspectionCohortFixture(t)
	for _, target := range sample.Targets {
		f.reports = append(f.reports, target.Report)
	}
	f.observe = func(ctx context.Context, path string, out any) error {
		if store.db.Stats().InUse != 0 {
			t.Fatal("parent held database connection during Kubernetes observation")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("parent observation has no deadline")
		}
		var value any
		switch path {
		case "/api/v1/namespaces/kube-system":
			value = map[string]any{"metadata": map[string]any{"uid": namespace}}
		case "/api/v1/nodes":
			value = map[string]any{"items": []any{sshSourceKubernetesFixture(member)}}
		default:
			t.Fatal("unexpected parent network request")
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, out)
	}
	return f
}

func (f sshParentFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.c.store.db.ExecContext(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f sshParentFixture) complete(t *testing.T, index int) clusterSSHTargetLease {
	t.Helper()
	target := f.targets[index]
	lease, err := f.c.store.claimClusterSSHTarget(f.ctx, f.op.ID, target.Binding.TargetID, newClusterUUID())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.c.store.completeClusterSSHInspection(f.ctx, lease, target.Sealed.generation, f.reports[index]); err != nil {
		t.Fatal(err)
	}
	return lease
}

func (f sshParentFixture) assertState(t *testing.T, state, step string, active bool) string {
	t.Helper()
	var gotState, gotStep, payload, health, activeID string
	var activeSize, desiredSize, members, prepared int
	err := f.c.store.db.QueryRowContext(f.ctx, `SELECT o.state,o.current_step,o.payload_json,c.status,COALESCE(c.active_operation_id,''),c.active_size,c.desired_size,
 (SELECT count(*) FROM engine.cluster_nodes WHERE membership_state='Active'),
 (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id AND state IN ('prepared','joined'))
 FROM engine.cluster_operations o CROSS JOIN engine.cluster_state c WHERE o.id=$1 AND c.id=1`, f.op.ID).
		Scan(&gotState, &gotStep, &payload, &health, &activeID, &activeSize, &desiredSize, &members, &prepared)
	if err != nil {
		t.Fatal(err)
	}
	expectedID := ""
	if active {
		expectedID = f.op.ID
	}
	if gotState != state || gotStep != step || health != "Healthy" || activeID != expectedID || activeSize != 1 || desiredSize != 1 || members != 1 || prepared != 0 {
		t.Fatalf("inspection changed unexpected state: operation=%s/%s health=%s active=%t membership=%d/%d members=%d prepared=%d", gotState, gotStep, health, activeID != "", activeSize, desiredSize, members, prepared)
	}
	if f.c.store.db.Stats().InUse != 0 {
		t.Fatal("parent retained connection after transition")
	}
	return payload
}

func (f sshParentFixture) events(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1`, f.op.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestClusterSSHParentPostgresInspectionLifecycle(t *testing.T) {
	t.Run("queued start partial progress and durable qualification wait", func(t *testing.T) {
		f := newSSHParentFixture(t)
		f.exec(t, `UPDATE engine.cluster_operations SET state='queued',current_step='preflight' WHERE id=$1`, f.op.ID)
		// Exercise the real controller dispatch. No generic mutation runner is
		// installed: this read-only operation must never reach one.
		if err := f.c.runOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		f.assertState(t, "running", clusterSSHInspectionOperationStep, true)
		before := f.events(t)
		for n := 0; n < 3; n++ {
			if n == 1 {
				f.complete(t, 0)
				before++
			}
			if err := f.c.runOnce(f.ctx); err != nil || f.events(t) != before {
				t.Fatalf("pending inspection failed or emitted duplicate event: %v", err)
			}
		}
		f.complete(t, 1)
		// Simulate resumed controller process using the same current lease identity.
		resumed := &clusterController{store: f.c.store, holder: f.c.holder, now: time.Now}
		if err := resumed.runSSHInspectionParent(f.ctx, f.op, f.observe); err != nil {
			t.Fatal(err)
		}
		raw := f.assertState(t, "waiting", clusterSSHQualificationStep, true)
		var payload struct {
			Inspection struct {
				SHA256   string          `json:"sha256"`
				Proof    json.RawMessage `json:"proof"`
				Required bool            `json:"qualification_required"`
			} `json:"ssh_inspection"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload.Inspection.Proof)
		var proof clusterSSHParentProof
		if err := json.Unmarshal(payload.Inspection.Proof, &proof); err != nil || !payload.Inspection.Required || hex.EncodeToString(digest[:]) != payload.Inspection.SHA256 || proof.Version != 1 || proof.Cohort.OperationID != f.op.ID || len(proof.Cohort.Targets) != 2 || proof.Source.KubeSystemUID == "" {
			t.Fatalf("qualification proof lost binding or digest: %v", err)
		}
		before = f.events(t)
		for range 2 {
			if _, claimed, err := resumed.claimOperation(f.ctx); err != nil || claimed {
				t.Fatalf("waiting qualification automatically reclaimed: %v", err)
			}
		}
		if f.events(t) != before {
			t.Fatal("qualification wait emitted repeated events")
		}
		if _, err := f.c.store.claimClusterSSHTarget(f.ctx, f.op.ID, f.targets[0].Binding.TargetID, newClusterUUID()); err == nil {
			t.Fatal("qualification granted more target work")
		}
	})
	for _, mode := range []string{"namespace unavailable", "nodes unavailable", "cancelled observation", "cloned host", "expired report", "expired credentials", "unknown prepared target"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHParentFixture(t)
			if mode == "cloned host" {
				f.reports[1].MachineID = f.reports[0].MachineID
			}
			f.complete(t, 0)
			f.complete(t, 1)
			observe := f.observe
			ctx := f.ctx
			if mode == "cancelled observation" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				observe = func(context.Context, string, any) error { cancel(); return context.Canceled }
			} else if strings.HasSuffix(mode, "unavailable") {
				observe = func(ctx context.Context, path string, out any) error {
					if mode == "namespace unavailable" || path == "/api/v1/nodes" {
						return errors.New("transport failure with private diagnostics")
					}
					return f.observe(ctx, path, out)
				}
			} else if mode == "expired report" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspected_at=1 WHERE id=$1`, f.targets[1].Binding.TargetID)
			} else if mode == "expired credentials" {
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, f.targets[1].Binding.TargetID)
			} else if mode == "unknown prepared target" {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET current_step='prepare_host' WHERE id=$1`, f.targets[1].Binding.TargetID)
			}
			before := f.events(t)
			err := f.c.runSSHInspectionParent(ctx, f.op, observe)
			retain := strings.HasSuffix(mode, "unavailable") || mode == "cancelled observation" || mode == "unknown prepared target"
			if retain {
				if err == nil || f.events(t) != before {
					t.Fatalf("uncertain observation changed operation: %v", err)
				}
				f.assertState(t, "running", clusterSSHInspectionOperationStep, true)
			} else {
				if err != nil || f.events(t) != before+1 {
					t.Fatalf("read-only failure not settled once: %v", err)
				}
				f.assertState(t, "failed", clusterSSHInspectionOperationStep, false)
			}
			var credentials, reports int
			if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT (SELECT count(*) FROM engine.cluster_onboarding_credentials),
 (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND inspected_attempt=1 AND inspection_json<>'{}')`, f.op.ID).Scan(&credentials, &reports); err != nil {
				t.Fatal(err)
			}
			wantCredentials := 0
			if retain {
				wantCredentials = 2
			}
			if credentials != wantCredentials || reports != 2 {
				t.Fatal("parent lost retained evidence or changed credentials across wrong boundary")
			}
		})
	}
}

func TestClusterSSHParentPostgresProofCommitFences(t *testing.T) {
	for _, mode := range []string{"controller", "operation attempt", "operation payload", "operation state", "active operation", "target generation", "target report", "target key", "credential expiry", "Aegis generation", "source VIP", "source member", "source size"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHParentFixture(t)
			f.complete(t, 0)
			f.complete(t, 1)
			cohort, source, err := f.c.store.assessClusterSSHInspectionCohort(f.ctx, f.op.ID, f.c.holder, 1, f.observe)
			if err != nil {
				t.Fatal(err)
			}
			proof := &clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}
			switch mode {
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder=$1 WHERE name=$2`, "replacement-"+newClusterUUID(), clusterControllerLeaseName)
			case "operation attempt":
				f.exec(t, `UPDATE engine.cluster_operations SET attempt=2 WHERE id=$1`, f.op.ID)
			case "operation payload":
				f.exec(t, `UPDATE engine.cluster_operations SET payload_json='{"changed":true}' WHERE id=$1`, f.op.ID)
			case "operation state":
				f.exec(t, `UPDATE engine.cluster_operations SET state='waiting' WHERE id=$1`, f.op.ID)
			case "active operation":
				f.exec(t, `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1`)
			case "target generation":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "target report":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json='{}' WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "target key":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET host_key_base64='changed' WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "credential expiry":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, f.targets[1].Binding.TargetID)
			case "Aegis generation":
				f.exec(t, `UPDATE engine.aegis_cipher_state SET verification_token='changed' WHERE id=1`)
			case "source VIP":
				f.exec(t, `UPDATE engine.cluster_state SET edge_vip='192.168.90.247' WHERE id=1`)
			case "source member":
				f.exec(t, `UPDATE engine.cluster_nodes SET management_ip='192.168.90.19' WHERE id=$1`, source.Members[0].NodeID)
			case "source size":
				f.exec(t, `UPDATE engine.cluster_state SET desired_size=3 WHERE id=1`)
			}
			before := f.events(t)
			if err := f.c.transitionSSHInspectionParent(f.ctx, f.op, "review", proof, nil); err == nil {
				t.Fatal("historical inspection became current qualification evidence")
			}
			var step, payload string
			if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT current_step,payload_json FROM engine.cluster_operations WHERE id=$1`, f.op.ID).Scan(&step, &payload); err != nil {
				t.Fatal(err)
			}
			if step != clusterSSHInspectionOperationStep || strings.Contains(payload, "ssh_inspection") || f.events(t) != before {
				t.Fatal("rejected proof changed operation or published event")
			}
		})
	}
}

func TestClusterSSHParentPostgresCancellationAndRetryFences(t *testing.T) {
	for _, mode := range []string{"queued", "running", "waiting qualification", "target prepared", "target joined", "target unknown step", "parent mutation step", "terminal", "retry failed"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHParentFixture(t)
			lease := f.complete(t, 0)
			inflight, err := f.c.store.claimClusterSSHTarget(f.ctx, f.op.ID, f.targets[1].Binding.TargetID, newClusterUUID())
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "queued":
				f.exec(t, `UPDATE engine.cluster_operations SET state='queued',current_step='preflight' WHERE id=$1`, f.op.ID)
			case "waiting qualification":
				f.exec(t, `UPDATE engine.cluster_operations SET state='waiting',current_step=$2 WHERE id=$1`, f.op.ID, clusterSSHQualificationStep)
			case "target prepared":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='prepared' WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "target joined":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='joined' WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "target unknown step":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET current_step='prepare_host' WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "parent mutation step":
				f.exec(t, `UPDATE engine.cluster_operations SET current_step='prepare_targets' WHERE id=$1`, f.op.ID)
			case "terminal", "retry failed":
				f.exec(t, `UPDATE engine.cluster_operations SET state='failed' WHERE id=$1`, f.op.ID)
			}
			before := f.events(t)
			const snapshot = `SELECT json_build_array(o.state,o.current_step,o.attempt,o.payload_json,c.active_operation_id,
 (SELECT json_agg(row_to_json(t) ORDER BY t.id) FROM engine.cluster_onboarding_targets t WHERE t.operation_id=o.id))::text
 FROM engine.cluster_operations o CROSS JOIN engine.cluster_state c WHERE o.id=$1 AND c.id=1`
			var prior, after string
			if err := f.c.store.db.QueryRowContext(f.ctx, snapshot, f.op.ID).Scan(&prior); err != nil {
				t.Fatal(err)
			}
			if mode == "retry failed" {
				_, err = f.c.store.retryClusterOperation(f.ctx, "admission-test", f.op.ID)
			} else {
				_, err = f.c.store.cancelClusterOperation(f.ctx, "admission-test", f.op.ID)
			}
			safe := textInSet(mode, "queued", "running", "waiting qualification")
			if !safe {
				if !errors.Is(err, errClusterConflict) {
					t.Fatalf("unsafe cancel/retry accepted: %v", err)
				}
				if err := f.c.store.db.QueryRowContext(f.ctx, snapshot, f.op.ID).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if prior != after || f.events(t) != before {
					t.Fatal("rejected cancellation changed retained state")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			f.assertState(t, "cancelled", "cancelled", false)
			var targets, credentials, retained int
			if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT
 (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND state='cancelled' AND credential_state='required' AND lease_holder='' AND lease_expires_at=0 AND lease_generation=2),
 (SELECT count(*) FROM engine.cluster_onboarding_credentials),
 (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE id=$2 AND inspected_attempt=1 AND inspected_generation=$3 AND inspection_json<>'{}')`, f.op.ID, lease.TargetID, lease.Generation).Scan(&targets, &credentials, &retained); err != nil {
				t.Fatal(err)
			}
			if targets != 2 || credentials != 0 || retained != 1 || f.events(t) != before+1 {
				t.Fatal("cancel did not fence workers, clean credentials and retain report once")
			}
			if err := f.c.store.completeClusterSSHInspection(f.ctx, inflight, f.targets[1].Sealed.generation, f.reports[1]); err == nil {
				t.Fatal("inflight inspection committed after cancellation")
			}
			if _, err := f.c.store.retryClusterOperation(f.ctx, "admission-test", f.op.ID); !errors.Is(err, errClusterConflict) {
				t.Fatal("cancelled operation blindly retried")
			}
		})
	}
}
