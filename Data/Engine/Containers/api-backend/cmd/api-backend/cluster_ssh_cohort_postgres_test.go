package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestClusterSSHCohortPostgresRejectsStaleOrPartialInspection(t *testing.T) {
	for _, mode := range []string{"success", "prior attempt", "reclaimed generation", "missing proof", "expired report", "future report", "missing credential", "expired credential", "Aegis rotation", "cleanup retained report", "wrong target cluster", "wrong step", "active target", "controller replaced", "operation replaced", "operation kind", "operation step", "operation state", "target state", "unknown report field"} {
		t.Run(mode, func(t *testing.T) {
			store, _, ctx, operationID, targets, _ := clusterSSHCredentialsFixture(t)
			insertSSHFixtureTargets(t, store, ctx, operationID, targets)
			if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET current_step=$1 WHERE id=$2`, clusterSSHInspectionOperationStep, operationID); err != nil {
				t.Fatal(err)
			}
			var holder string
			for _, target := range targets {
				lease, err := store.claimClusterSSHTarget(ctx, operationID, target.Binding.TargetID, newClusterUUID())
				if err != nil {
					t.Fatal(err)
				}
				holder = lease.ControllerHolder
				host, privileged := sshWorkerFacts()
				report, err := clusterSSHReport(host, privileged, target.Binding.Address)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.completeClusterSSHInspection(ctx, lease, target.Sealed.generation, report); err != nil {
					t.Fatal(err)
				}
			}
			// Mutate only disposable fixture state. Retained public evidence must not
			// become valid merely because a retry changes current row ownership.
			targetID := targets[1].Binding.TargetID
			var query string
			args := []any{targetID}
			attempt := int64(1)
			switch mode {
			case "prior attempt":
				attempt = 2
				query = `WITH retry AS (UPDATE engine.cluster_operations SET attempt=2 WHERE id=$1 RETURNING id)
     UPDATE engine.cluster_onboarding_targets SET operation_attempt=2 FROM retry WHERE operation_id=retry.id`
				args = []any{operationID}
			case "reclaimed generation":
				query = `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`
			case "missing proof":
				query = `UPDATE engine.cluster_onboarding_targets SET inspected_attempt=0,inspected_generation=0 WHERE id=$1`
			case "expired report":
				query = `UPDATE engine.cluster_onboarding_targets SET inspected_at=floor(extract(epoch FROM clock_timestamp()))::bigint-300 WHERE id=$1`
			case "future report":
				query = `UPDATE engine.cluster_onboarding_targets SET inspected_at=floor(extract(epoch FROM clock_timestamp()))::bigint+60 WHERE id=$1`
			case "missing credential":
				query = `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id=$1`
			case "expired credential", "cleanup retained report":
				query = `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`
			case "Aegis rotation":
				query = `UPDATE engine.aegis_cipher_state SET verification_token='changed' WHERE id=1`
				args = nil
			case "wrong target cluster":
				query = `UPDATE engine.cluster_onboarding_targets SET cluster_id=$2 WHERE id=$1`
				args = append(args, newClusterUUID())
			case "wrong step":
				query = `UPDATE engine.cluster_onboarding_targets SET current_step='inspect' WHERE id=$1`
			case "active target":
				query = `UPDATE engine.cluster_onboarding_targets SET lease_holder=$2,lease_expires_at=floor(extract(epoch FROM clock_timestamp()))::bigint+45 WHERE id=$1`
				args = append(args, newClusterUUID())
			case "controller replaced":
				query = `UPDATE engine.cluster_application_leases SET holder=$1 WHERE name=$2`
				args = []any{newClusterUUID(), clusterControllerLeaseName}
			case "operation replaced":
				query = `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1`
				args = nil
			case "operation kind":
				query = `UPDATE engine.cluster_operations SET kind='engine_update' WHERE id=$1`
				args = []any{operationID}
			case "operation step":
				query = `UPDATE engine.cluster_operations SET current_step='prepare_targets' WHERE id=$1`
				args = []any{operationID}
			case "operation state":
				query = `UPDATE engine.cluster_operations SET state='failed' WHERE id=$1`
				args = []any{operationID}
			case "target state":
				query = `UPDATE engine.cluster_onboarding_targets SET state='recovery_required' WHERE id=$1`
			case "unknown report field":
				query = `UPDATE engine.cluster_onboarding_targets SET inspection_json=replace(inspection_json,'"version":1','"extra":true,"version":1') WHERE id=$1`
			}
			if query != "" {
				if _, err := store.db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "cleanup retained report" {
				if err := store.cleanupClusterSSHCredentials(ctx); err != nil {
					t.Fatal(err)
				}
				var raw string
				if err := store.db.QueryRowContext(ctx, `SELECT inspection_json FROM engine.cluster_onboarding_targets WHERE id=$1`, targetID).Scan(&raw); err != nil || !strings.Contains(raw, `"version":1`) {
					t.Fatal("cleanup erased historical report")
				}
			}
			cohort, err := store.loadClusterSSHInspectionCohort(ctx, operationID, holder, attempt)
			if store.db.Stats().InUse != 0 {
				t.Fatal("cohort reader retained connection")
			}
			if mode == "success" {
				if err != nil || len(cohort.Targets) != 2 || cohort.Attempt != 1 || cohort.ObservedAt <= 0 {
					t.Fatalf("cohort read failed: %v", err)
				}
				for i, target := range cohort.Targets {
					if target.Binding != targets[i].Binding || target.Attempt != 1 || target.Generation != 1 || target.InspectedAt <= 0 {
						t.Fatal("inspection binding lost")
					}
				}
			} else if err != errClusterSSHCohort || len(cohort.Targets) != 0 {
				t.Fatalf("stale or partial cohort escaped: %v", err)
			}
			// This component may only read. It never authorizes preparation or appends
			// a second event while assessing the same inspection multiple times.
			var prepared int
			if err := store.db.QueryRowContext(context.Background(), `SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1 AND state IN ('prepared','joined')`, operationID).Scan(&prepared); err != nil || prepared != 0 {
				t.Fatal("cohort read changed membership preparation")
			}
		})
	}
}

func TestClusterSSHCohortPostgresReconcilesSourceOutsideConnection(t *testing.T) {
	for _, mode := range []string{"success", "topology changed", "holder changed", "target reclaimed", "disabled", "unrecorded member", "source clone"} {
		t.Run(mode, func(t *testing.T) {
			store, _, ctx, operationID, targets, _ := clusterSSHCredentialsFixture(t)
			insertSSHFixtureTargets(t, store, ctx, operationID, targets)
			if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_operations SET current_step=$1 WHERE id=$2`, clusterSSHInspectionOperationStep, operationID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_state SET edge_vip=control_plane_vip WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			var holder string
			sample, _ := sshInspectionCohortFixture(t)
			for i, target := range targets {
				lease, err := store.claimClusterSSHTarget(ctx, operationID, target.Binding.TargetID, newClusterUUID())
				if err != nil {
					t.Fatal(err)
				}
				holder = lease.ControllerHolder
				if err := store.completeClusterSSHInspection(ctx, lease, target.Sealed.generation, sample.Targets[i].Report); err != nil {
					t.Fatal(err)
				}
			}
			source, err := store.loadClusterSSHSourceCohort(ctx, operationID, holder, 1)
			if err != nil {
				t.Fatal(err)
			}
			member := source.Members[0]
			member.NodeUID, member.BootID, member.MachineID = newClusterUUID(), newClusterUUID(), strings.Repeat("b", 32)
			if mode == "source clone" {
				member.MachineID = sample.Targets[0].Report.MachineID
			}
			namespace := newClusterUUID()
			if mode == "disabled" {
				if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_state SET enabled=0 WHERE id=1`); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			read := func(readCtx context.Context, path string, out any) error {
				calls++
				if store.db.Stats().InUse != 0 {
					t.Fatal("Kubernetes request held database connection")
				}
				if _, ok := readCtx.Deadline(); !ok {
					t.Fatal("source inspection unbounded")
				}
				var value any
				switch path {
				case "/api/v1/namespaces/kube-system":
					value = map[string]any{"metadata": map[string]any{"uid": namespace}}
				case "/api/v1/nodes":
					node := sshSourceKubernetesFixture(member)
					items := []any{node}
					if mode == "unrecorded member" {
						items = append(items, node)
					}
					value = map[string]any{"items": items}
					query := ""
					var args []any
					switch mode {
					case "topology changed":
						query = `UPDATE engine.cluster_state SET edge_vip='192.168.90.247' WHERE id=1`
					case "holder changed":
						query = `UPDATE engine.cluster_application_leases SET holder=$1 WHERE name=$2`
						args = []any{newClusterUUID(), clusterControllerLeaseName}
					case "target reclaimed":
						query = `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`
						args = []any{targets[1].Binding.TargetID}
					}
					if query != "" {
						if _, err := store.db.ExecContext(readCtx, query, args...); err != nil {
							return err
						}
					}
				default:
					t.Fatal("unexpected source read")
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return err
				}
				return json.Unmarshal(raw, out)
			}
			cohort, observed, err := store.assessClusterSSHInspectionCohort(ctx, operationID, holder, 1, read)
			if mode == "success" {
				if err != nil || len(cohort.Targets) != 2 || observed.KubeSystemUID != namespace || observed.Members[0].NodeUID != member.NodeUID {
					t.Fatalf("source reconciliation failed: %v", err)
				}
			} else if err == nil || len(cohort.Targets) != 0 || len(observed.Members) != 0 {
				t.Fatal("stale source became cohort acceptance")
			}
			if mode == "disabled" && calls != 0 {
				t.Fatal("disabled cluster reached Kubernetes")
			}
		})
	}
}
