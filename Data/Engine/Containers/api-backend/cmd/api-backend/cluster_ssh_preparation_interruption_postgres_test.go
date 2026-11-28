package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Exercise the production poller, claim and controller. Only worker execution
// is replaced to deterministically interrupt either side of its allocation gate.
func testSSHInventoryInterruption(t *testing.T) {
	for _, mode := range []string{"inventory failed", "credentials expired", "controller restarted", "wrong node", "partial effects", "returned before native", "legacy expired"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newSSHPreparationClaimFixture(t, false)
			store := f.c.store
			var sourceNode string
			if err := store.db.QueryRowContext(f.ctx, `SELECT node_name FROM engine.cluster_nodes WHERE membership_state='Active'`).Scan(&sourceNode); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BOREALIS_ENGINE_NODE_NAME", sourceNode)
			t.Setenv("BOREALIS_OPERATOR_SECRET", strings.Repeat("x", 32))
			runtime := newClusterSSHPreparationRuntime(store, f.aegis)
			if runtime == nil {
				t.Fatal("production worker unavailable")
			}
			var claimed clusterSSHPreparationWork
			if mode == "legacy expired" {
				runtime.claim = store.claimClusterSSHPreparation
			}
			interrupted := errors.New("simulated inventory interruption")
			runtime.execute = func(ctx context.Context, work clusterSSHPreparationWork) error {
				claimed = work
				if mode == "partial effects" || mode == "returned before native" {
					if err := store.openSSHPreparationAllocation(ctx, work.Claims); err != nil {
						t.Fatal(err)
					}
					if err := store.openSSHPreparationAllocation(ctx, work.Claims); err == nil {
						t.Fatal("repeated allocation")
					}
				}
				if mode == "partial effects" {
					l := work.Claims[0].Lease
					if err := store.recordSSHPreparationCheckpoint(ctx, l, "SSH native preparation phase=wrapper_exit status=failed exit_code=1"); err != nil {
						t.Fatal(err)
					}
					l.Generation++
					if err := store.recordSSHPreparationCheckpoint(ctx, l, "unexpected generation"); err != errClusterConflict {
						t.Fatal("unbound diagnostic accepted")
					}
				}
				if mode == "returned before native" {
					l := work.Claims[0].Lease
					ctx = context.WithValue(ctx, clusterSSHPrejoinDiagnosticKey{}, &clusterSSHPrejoinDiagnostic{operation: l.OperationID, started: time.Now()})
					ctx = context.WithValue(ctx, clusterSSHPrejoinLeaseKey{}, l)
					reportClusterSSHPrejoinCheck(ctx, "external image staging")
					for range 2 {
						if err := store.recordSSHPreparationReadOnlyReturn(ctx, work.Claims[0].Lease); err != nil {
							t.Fatal(err)
						}
					}

				}
				return interrupted
			}
			var brokerCalls int
			if mode == "inventory failed" {
				// Exercise the real API worker up to its first authenticated
				// source inventory, with only the controller HTTP service doubled.
				scratch := t.TempDir()
				t.Setenv("BOREALIS_API_BACKGROUND_LOOPS", "1")
				proc := clusterExistingIdentitySourceProcess{NodeName: sourceNode, PodUID: newClusterUUID(), PodName: "source-api"}
				t.Setenv("BOREALIS_ENGINE_POD_UID", proc.PodUID)
				t.Setenv("BOREALIS_ENGINE_POD_NAME", proc.PodName)
				client, err := newClusterSSHSourceBrokerClientWithTimeout(clusterSSHReadOnlySnapshotTimeout)
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "source unavailable", http.StatusServiceUnavailable)
				}))
				defer server.Close()
				client.peers = func(context.Context) ([]string, error) { brokerCalls++; return []string{server.URL}, nil }
				worker := newClusterSSHPrejoinWorkerWithClient(store, f.aegis, scratch, proc, client.joinedTargetsRead, client)
				runtime.execute = func(ctx context.Context, work clusterSSHPreparationWork) error {
					claimed = work
					if err := worker.prepare(ctx, work); err == nil {
						t.Fatal("inventory failure ignored")
					}
					entries, err := os.ReadDir(scratch)
					if err != nil || len(entries) != 0 || brokerCalls != 1 {
						t.Fatal("wrong boundary", brokerCalls, len(entries), err)
					}
					return interrupted
				}
			}
			if err := runtime.runOnce(f.ctx); !errors.Is(err, interrupted) || len(claimed.Claims) != 2 {
				t.Fatal("paired dispatch", err)
			}
			if _, selected, err := f.c.claimOperation(f.ctx); err != nil || selected {
				t.Fatal("selected live workers", err)
			}
			if mode == "credentials expired" || mode == "legacy expired" {
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id IN(SELECT id FROM engine.cluster_onboarding_targets WHERE operation_id=$1)`, f.op.ID)
				if err := store.cleanupClusterSSHCredentials(f.ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_expires_at=1 WHERE operation_id=$1`, f.op.ID)
			}
			if mode == "controller restarted" || mode == "wrong node" {
				f.c.holder = newClusterUUID()
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder=$1 WHERE name=$2`, f.c.holder, clusterControllerLeaseName)
				if mode == "wrong node" {
					t.Setenv("BOREALIS_ENGINE_NODE_NAME", "other-engine")
				}
			}
			f.c.maxIdleConnections = 2
			for range 2 {
				if err := f.c.runOnce(f.ctx); err != nil {
					t.Fatal("controller poll", err)
				}
			}
			if mode == "returned before native" {
				l := claimed.Claims[0].Lease
				var evidenceOK bool
				if err := store.db.QueryRowContext(f.ctx, `SELECT count(*)=1 FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type=$2 AND details_json::jsonb #>> '{diagnostic,rejection,check}'='external image staging' AND details_json::jsonb #>> '{diagnostic,rejection,target_id}'=$3 AND details_json::jsonb #>> '{diagnostic,installation_effects}'='none'`, l.OperationID, sshPreparationReturned, l.TargetID).Scan(&evidenceOK); err != nil || !evidenceOK {
					t.Fatal("committed read-only receipt lost its first rejection")
				}
			}
			var state, step, reason, active string
			var credentials, events, targets int
			if err := store.db.QueryRowContext(f.ctx, `SELECT o.state,o.current_step,coalesce(o.error_text,''),coalesce(c.active_operation_id,''),
 (SELECT count(*) FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id WHERE t.operation_id=o.id),
 (SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=o.id AND event_type IN('operation_failed','ssh_preparation_reconciliation_blocked')),
 (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id AND state='failed' AND credential_state='required')
 FROM engine.cluster_operations o,engine.cluster_state c WHERE o.id=$1 AND c.id=1`, f.op.ID).Scan(&state, &step, &reason, &active, &credentials, &events, &targets); err != nil {
				t.Fatal(err)
			}
			if events != 1 {
				t.Fatal("missing or duplicate outcome", events)
			}
			if mode == "partial effects" || mode == "wrong node" || mode == "legacy expired" {
				if state != "running" || active != f.op.ID || targets != 0 || reason == "" {
					t.Fatal("lost uncertain operation", state, step)
				}
				return
			}
			if state != "failed" || active != "" || targets != 2 || credentials != 0 || reason != sshPreparationStopped {
				t.Fatal("incomplete terminal settlement", state, step, targets, credentials)
			}
			if err := store.openSSHPreparationAllocation(f.ctx, claimed.Claims); err == nil {
				t.Fatal("old worker crossed terminal fence")
			}
			if err := runtime.runOnce(f.ctx); err != nil {
				t.Fatal("terminal operation replayed", err)
			}
			if mode != "inventory failed" {
				return
			}
			// Read the committed record after automatic settlement and a second worker poll.
			var entered, failed, rejected int
			if err := store.db.QueryRowContext(f.ctx, `SELECT
count(*) FILTER (WHERE message LIKE 'Source preparation phase=source_snapshot status=entered%'),
count(*) FILTER (WHERE message LIKE 'Source preparation phase=source_snapshot status=failed%'),
count(*) FILTER (WHERE message LIKE 'Source broker status=failed check=http_status_expected_200 observed=http_503%')
FROM engine.cluster_operation_events WHERE operation_id=$1 AND event_type='ssh_preparation_checkpoint'
AND details_json::jsonb->>'target_id'=$2 AND (details_json::jsonb->>'claim_generation')::bigint=$3`,
				f.op.ID, claimed.Claims[0].Lease.TargetID, claimed.Claims[0].Lease.Generation).Scan(&entered, &failed, &rejected); err != nil || entered != 1 || failed != 1 || rejected != 1 {
				t.Fatal("missing or duplicate first source snapshot evidence", entered, failed, rejected, err)
			}
			source, err := readClusterSSHQueueSource(f.ctx, store.db)
			if err != nil {
				t.Fatal(err)
			}
			fresh := append([]clusterSSHPlannedTarget(nil), f.targets...)
			freshID := newClusterUUID()
			t.Cleanup(func() {
				store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, freshID)
			})
			for i := range fresh {
				env, err := f.aegis.openClusterSSHCredentials(f.ctx, fresh[i].Sealed, fresh[i].Binding)
				if err != nil {
					t.Fatal(err)
				}
				fresh[i].Binding.OperationID, fresh[i].Binding.TargetID = freshID, newClusterUUID()
				env.Binding = fresh[i].Binding
				fresh[i].Sealed, err = f.aegis.sealClusterSSHCredentials(f.ctx, env)
				if err != nil {
					t.Fatal(err)
				}
			}
			next, err := store.queueClusterSSHInspections(f.ctx, "test-operator", source, fresh)
			if err != nil || next["current_step"] != "preflight" {
				t.Fatal("fresh inspection", err)
			}
			if err := runtime.runOnce(f.ctx); err != nil {
				t.Fatal("fresh inspection inherited old approval", err)
			}
		})
	}
}
