package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func newSSHPreparedAuthorityFixture(t *testing.T, replacement bool) (sshPreparationAuthorityFixture, clusterSSHPreparationAuthority, map[string]string) {
	t.Helper()
	f, legacy := newSSHPreparationCompletionFixture(t, replacement)
	var completions []clusterSSHPrejoinCompletion
	uids := map[string]string{}
	for i, value := range legacy {
		a := value.Authority
		expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16", a.payload)
		if err != nil {
			t.Fatal(err)
		}
		receipt := sshPrejoinReceiptIdentityFixture(expected, *a.IdentitySource, newClusterUUID(), uint64(42+i))
		completions = append(completions, clusterSSHPrejoinCompletion{PayloadSHA256: a.PayloadSHA256, Claim: clusterSSHNetworkTargetClaim{Lease: a.Lease, Sealed: value.Sealed}, Expected: expected, Receipt: receipt})
		uids[a.Lease.TargetID] = receipt.Node.NodeUID
	}
	if err := f.c.store.completeClusterSSHPrejoinPreparation(f.ctx, f.aegis, completions, sshPrejoinCompletionNodes(legacy[0].Authority, uids)); err != nil {
		t.Fatal("settle v2 fixture", err)
	}
	return f, legacy[0].Authority, uids
}

func TestClusterSSHPreparedAuthorityPostgresObservation(t *testing.T) {
	for _, mode := range []string{"pair", "replacement", "second target replaced", "source replaced", "namespace drift", "not ready", "missing target", "controller lost", "attempt drift", "payload drift", "member receipt drift", "source row drift", "target row drift", "credential drift", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f, a, uids := newSSHPreparedAuthorityFixture(t, mode == "replacement")
			before, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
			if err != nil {
				t.Fatal("load prepared fixture", err)
			}
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			f.c.store.db.SetMaxOpenConns(1)
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				if f.c.store.db.Stats().InUse != 0 {
					t.Error("SQL held during native observation")
				}
				object := sshPreparedNativeFixture(a, uids, r.URL.Path)
				if call == 4 {
					var query string
					var args []any
					switch mode {
					case "controller lost":
						query = `UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`
						args = []any{clusterControllerLeaseName}
					case "attempt drift":
						query = `UPDATE engine.cluster_operations SET attempt=attempt+1 WHERE id=$1`
						args = []any{f.op.ID}
					case "payload drift":
						query = `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`
						args = []any{f.op.ID}
					case "member receipt drift":
						var payload map[string]json.RawMessage
						_ = json.Unmarshal([]byte(before.payload), &payload)
						receipt := before.receipt
						receipt.Targets[0].Prejoin.Receipt.MemberID++
						payload["ssh_preparation"], _ = json.Marshal(receipt)
						query = `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`
						args = []any{f.op.ID, marshalClusterJSON(payload)}
					case "source row drift":
						query = `UPDATE engine.cluster_nodes SET application_state='drained' WHERE id=$1`
						args = []any{a.Source.Members[0].NodeID}
					case "target row drift":
						query = `UPDATE engine.cluster_onboarding_targets SET lease_generation=lease_generation+1 WHERE id=$1`
						args = []any{a.Cohort.Targets[len(a.Cohort.Targets)-1].Binding.TargetID}
					case "credential drift":
						query = `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`
						args = []any{a.Cohort.Targets[0].Binding.TargetID}
					case "cancelled":
						cancel()
					}
					if query != "" {
						if _, err := f.c.store.db.ExecContext(ctx, query, args...); err != nil {
							t.Error("mid-observation drift", err)
						}
					}
				}
				if r.URL.Path == "/api/v1/nodes" {
					items := anySlice(object["items"])
					target := items[len(items)-1].(map[string]any)
					switch mode {
					case "second target replaced":
						if call > 3 {
							clusterSSHStorageMap(target, "metadata")["uid"] = newClusterUUID()
						}
					case "source replaced":
						clusterSSHStorageMap(items[0].(map[string]any), "metadata")["uid"] = newClusterUUID()
					case "not ready":
						clusterSSHStorageMap(target, "status")["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
					case "missing target":
						object["items"] = items[:len(items)-1]
					}
				} else if mode == "namespace drift" && call > 3 {
					clusterSSHStorageMap(object, "metadata")["uid"] = newClusterUUID()
				}
				if err := json.NewEncoder(w).Encode(object); err != nil && mode != "cancelled" {
					t.Error(err)
				}
			}))
			defer server.Close()
			f.c.runner = &kubernetesClusterStepRunner{kube: &kubernetesAPIClient{baseURL: server.URL, httpClient: server.Client()}, controllerHolder: f.c.holder}
			events := f.events(t)
			got, err := f.c.observeClusterSSHPrepared(ctx, f.op)
			success := mode == "pair" || mode == "replacement"
			if (err == nil) != success {
				t.Fatal("wrong prepared observation outcome", err)
			}
			if success {
				if calls.Load() != 6 || len(got.Nodes) != len(a.Cohort.Targets) || got.OperationID != f.op.ID || got.Attempt != f.op.Attempt || got.Controller != f.c.holder || got.ClusterID != a.Source.ClusterID || got.KubeSystemUID != a.Source.KubeSystemUID || got.PayloadSHA256 != preparationReceiptHash(before.payload) || got.ObservedAt <= 0 {
					t.Fatal("lost observation binding")
				}
				for _, target := range before.receipt.Targets {
					if got.Nodes[target.TargetID] != target.Prejoin.Receipt.Node {
						t.Fatal("receipt Node changed")
					}
				}
				after, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("read changed durable authority", err)
				}
				if _, err := f.c.claimClusterSSHPreparation(f.ctx, f.op, newClusterUUID()); err == nil {
					t.Fatal("settled cohort regained preparation claims")
				}
				if _, _, err := parseClusterSSHPreparationAuthorization(before.payload, before.proof, before.queue); err == nil {
					t.Fatal("old claim parser accepted completed payload")
				}
			} else if got.Nodes != nil || got.OperationID != "" {
				t.Fatal("failed proof leaked observation")
			}
			if f.events(t) != events {
				t.Fatal("read wrote operation event")
			}
		})
	}
}

func TestClusterSSHPreparedAuthorityPostgresRejectsDrift(t *testing.T) {
	for _, mode := range []string{"missing receipt", "version1", "partial receipt", "member changed", "generation changed", "recovery", "controller", "active parent", "operation phase", "lease expiry", "credential missing", "credential expiry", "Aegis generation", "target holder", "target state", "source baseline"} {
		t.Run(mode, func(t *testing.T) {
			f, _, _ := newSSHPreparedAuthorityFixture(t, false)
			a, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op)
			if err != nil {
				t.Fatal("load prepared fixture", err)
			}
			var payload map[string]json.RawMessage
			_ = json.Unmarshal([]byte(a.payload), &payload)
			receipt := a.receipt
			switch mode {
			case "missing receipt":
				delete(payload, "ssh_preparation")
			case "version1":
				receipt.Version = 1
			case "partial receipt":
				receipt.Targets = receipt.Targets[:1]
			case "member changed":
				receipt.Targets[0].Prejoin.Receipt.MemberID++
			case "generation changed":
				receipt.Targets[0].Generation++
			case "recovery":
				payload["ssh_identity_recovery"] = json.RawMessage(`{}`)
			case "controller":
				f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other' WHERE name=$1`, clusterControllerLeaseName)
			case "active parent":
				f.exec(t, `UPDATE engine.cluster_state SET active_operation_id=NULL WHERE id=1`)
			case "operation phase":
				f.exec(t, `UPDATE engine.cluster_operations SET current_step='admit_pending_members' WHERE id=$1`, f.op.ID)
			case "lease expiry":
				f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=0 WHERE name=$1`, clusterControllerLeaseName)
			case "credential missing":
				f.exec(t, `DELETE FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, f.targets[1].Binding.TargetID)
			case "credential expiry":
				f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=0 WHERE target_id=$1`, f.targets[1].Binding.TargetID)
			case "Aegis generation":
				f.exec(t, `UPDATE engine.aegis_cipher_state SET verification_token='changed' WHERE id=1`)
			case "target holder":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET lease_holder=$2 WHERE id=$1`, f.targets[1].Binding.TargetID, newClusterUUID())
			case "target state":
				f.exec(t, `UPDATE engine.cluster_onboarding_targets SET state='running' WHERE id=$1`, f.targets[1].Binding.TargetID)
			case "source baseline":
				f.exec(t, `UPDATE engine.cluster_state SET baseline_release='2026.09.998-rc.1' WHERE id=1`)
			}
			switch mode {
			case "version1", "partial receipt", "member changed", "generation changed":
				payload["ssh_preparation"], _ = json.Marshal(receipt)
			}
			f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2 WHERE id=$1`, f.op.ID, marshalClusterJSON(payload))
			events := f.events(t)
			if got, _, err := f.c.loadClusterSSHPreparedAuthority(f.ctx, f.op); err == nil || got.payload != "" {
				t.Fatal("changed prepared authority accepted", err)
			}
			if f.events(t) != events {
				t.Fatal("rejection mutated event history")
			}
			if mode == "recovery" {
				if _, _, err := parseClusterSSHPreparationAuthorization(marshalClusterJSON(payload), a.proof, a.queue); err == nil {
					t.Fatal("old claim parser accepted recovery")
				}
			}
		})
	}
}

func TestClusterSSHPreparedAuthorityPostgresLockWaitExpiry(t *testing.T) {
	f, _, _ := newSSHPreparedAuthorityFixture(t, false)
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	f.c.store.db.SetMaxOpenConns(4)
	// Lease expires while this reader waits for original operation row. Existing
	// controller lock stays shared, so final clock check must fence expiry itself.
	f.exec(t, `UPDATE engine.cluster_application_leases SET expires_at=ceil(extract(epoch FROM clock_timestamp()))::bigint+2 WHERE name=$1`, clusterControllerLeaseName)
	tx, err := f.c.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM engine.cluster_operations WHERE id=$1 FOR UPDATE`, f.op.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, _, err := f.c.loadClusterSSHPreparedAuthority(ctx, f.op); done <- err }()
	waiting := false
	for !waiting {
		if err := f.c.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%engine.cluster_operations%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			t.Fatal("reader bypassed locked operation", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	expired := false
	for !expired {
		if err := f.c.store.db.QueryRowContext(ctx, `SELECT expires_at<=extract(epoch FROM clock_timestamp()) FROM engine.cluster_application_leases WHERE name=$1`, clusterControllerLeaseName).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if !expired {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expired controller escaped wait fence")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
