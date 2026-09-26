package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sshQueueFixture(t *testing.T) (*postgresOperatorStore, *goAegisService, context.Context, clusterSSHQueueSource, []clusterSSHPlannedTarget, []clusterSSHCredentialEnvelope) {
	t.Helper()
	store, aegis, ctx, id, targets, envelopes := clusterSSHCredentialsFixture(t)
	if _, err := store.db.ExecContext(ctx, `DELETE FROM engine.cluster_operations WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=NULL,edge_vip=control_plane_vip WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	source, err := readClusterSSHQueueSource(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	if store.db.Stats().InUse != 0 {
		t.Fatal("source read retained connection before credential encryption")
	}
	return store, aegis, ctx, source, targets, envelopes
}

func TestClusterSSHQueuePostgresAtomicSourceAndCredentialFences(t *testing.T) {
	for _, mode := range []string{"success", "source replaced", "source config", "source release", "source membership", "source drain", "source disabled", "active operation", "source HMR", "Aegis changed", "partial pair", "duplicate address", "duplicate key", "VIP collision", "existing member", "changed binding", "cross operation", "reserved address", "reserved key"} {
		t.Run(mode, func(t *testing.T) {
			store, aegis, ctx, source, targets, envelopes := sshQueueFixture(t)
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := store.db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "source replaced":
				exec(`UPDATE engine.cluster_state SET cluster_id=$1 WHERE id=1`, newClusterUUID())
			case "source config":
				exec(`UPDATE engine.cluster_state SET config_json='{"k3s_version":"v1.36.4+k3s1"}' WHERE id=1`)
			case "source release":
				exec(`UPDATE engine.cluster_state SET baseline_sha=$1 WHERE id=1`, strings.Repeat("b", 40))
			case "source membership":
				exec(`UPDATE engine.cluster_state SET desired_size=3 WHERE id=1`)
			case "source drain":
				exec(`UPDATE engine.cluster_nodes SET application_state='standby' WHERE membership_state='Active'`)
			case "source disabled":
				exec(`UPDATE engine.cluster_state SET enabled=0 WHERE id=1`)
			case "active operation":
				exec(`UPDATE engine.cluster_state SET active_operation_id=$1 WHERE id=1`, newClusterUUID())
			case "source HMR":
				exec(`UPDATE engine.cluster_state SET hmr_state='isolated' WHERE id=1`)
			case "Aegis changed":
				exec(`UPDATE engine.aegis_cipher_state SET verification_token='changed' WHERE id=1`)
			case "partial pair":
				targets = targets[:1]
			case "duplicate address":
				targets[1].Binding.Address = targets[0].Binding.Address
			case "duplicate key":
				targets[1].Key, targets[1].KeyBase64, targets[1].Binding.Fingerprint = targets[0].Key, targets[0].KeyBase64, targets[0].Binding.Fingerprint
			case "VIP collision":
				targets[1].Binding.Address = source.ControlVIP
			case "existing member":
				targets[1].Binding.Address = "192.168.90.10"
			case "changed binding":
				targets[1].Binding.ClusterID = newClusterUUID()
			case "cross operation":
				targets[1].Binding.OperationID = newClusterUUID()
			case "reserved address", "reserved key":
				oldID := newClusterUUID()
				exec(`INSERT INTO engine.cluster_operations(id,kind,state,current_step,requested_by,created_at,updated_at) VALUES($1,'ssh_onboarding','failed','inspect_ssh_targets','admission-test',1,1)`, oldID)
				t.Cleanup(func() {
					_, _ = store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, oldID)
				})
				address, fingerprint := targets[1].Binding.Address, "different retained fingerprint"
				if mode == "reserved key" {
					address, fingerprint = "192.168.90.99", targets[1].Binding.Fingerprint
				}
				exec(`INSERT INTO engine.cluster_onboarding_targets(id,operation_id,cluster_id,ordinal,management_ip,ssh_port,host_key_algorithm,host_key_fingerprint,host_key_base64,state,current_step,created_at,updated_at)
 VALUES($1,$2,$3,1,$4,22,'ssh-ed25519',$5,'retained','recovery_required','prepare_host',1,1)`, newClusterUUID(), oldID, source.ClusterID, address, fingerprint)
			}
			if textInSet(mode, "duplicate address", "duplicate key", "VIP collision", "existing member", "changed binding", "cross operation") {
				envelopes[1].Binding = targets[1].Binding
				var err error
				targets[1].Sealed, err = aegis.sealClusterSSHCredentials(ctx, envelopes[1])
				if err != nil {
					t.Fatal(err)
				}
			}
			result, err := store.queueClusterSSHInspections(ctx, "admission-test", source, targets)
			id := targets[0].Binding.OperationID
			var operations, count, credentials, events int
			if queryErr := store.db.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM engine.cluster_operations WHERE id=$1),
 (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=$1),
 (SELECT count(*) FROM engine.cluster_onboarding_credentials p JOIN engine.cluster_onboarding_targets t ON t.id=p.target_id WHERE t.operation_id=$1),
 (SELECT count(*) FROM engine.cluster_operation_events WHERE operation_id=$1)`, id).Scan(&operations, &count, &credentials, &events); queryErr != nil {
				t.Fatal(queryErr)
			}
			if mode != "success" {
				if err == nil || result != nil || operations != 0 || count != 0 || credentials != 0 || events != 0 {
					t.Fatalf("rejected queue left partial cohort: %v", err)
				}
				return
			}
			if err != nil || cleanText(result["state"]) != "queued" || operations != 1 || count != 2 || credentials != 2 || events != 1 {
				t.Fatalf("queue did not atomically reserve pair: %v", err)
			}
			var activeID, state string
			var active, desired int
			if err := store.db.QueryRowContext(ctx, `SELECT active_operation_id,status,active_size,desired_size FROM engine.cluster_state WHERE id=1`).Scan(&activeID, &state, &active, &desired); err != nil {
				t.Fatal(err)
			}
			if activeID != id || state != "Healthy" || active != 1 || desired != 1 {
				t.Fatal("inspection queue changed membership or health")
			}
			if _, err := store.claimClusterSSHTarget(ctx, id, targets[0].Binding.TargetID, newClusterUUID()); err == nil {
				t.Fatal("queue bypassed parent preflight")
			}
			var holder string
			if err := store.db.QueryRowContext(ctx, `SELECT holder FROM engine.cluster_application_leases WHERE name=$1`, clusterControllerLeaseName).Scan(&holder); err != nil {
				t.Fatal(err)
			}
			controller := &clusterController{store: store, holder: holder, now: time.Now}
			if err := controller.runOnce(ctx); err != nil {
				t.Fatal(err)
			}
			for _, target := range targets {
				lease, err := store.claimClusterSSHTarget(ctx, id, target.Binding.TargetID, newClusterUUID())
				if err != nil {
					t.Fatal(err)
				}
				sealed, err := store.loadClusterSSHTargetCredentials(ctx, lease)
				if err != nil {
					t.Fatal(err)
				}
				if store.db.Stats().InUse != 0 {
					t.Fatal("queue credential read held connection for decryption")
				}
				if _, err := aegis.openClusterSSHCredentials(ctx, sealed, target.Binding); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.queueClusterSSHInspections(ctx, "admission-test", source, targets); err == nil {
				t.Fatal("queue replay replaced original operation")
			}
		})
	}
	t.Run("recorded degraded replacement reserves one target", func(t *testing.T) {
		store, _, ctx, _, targets, _ := sshQueueFixture(t)
		t.Cleanup(seedAdmissionPeer(t, store, ctx, "retained-replacement-peer", "192.168.90.40"))
		if _, err := store.db.ExecContext(ctx, `UPDATE engine.cluster_state SET active_size=2,desired_size=3,status='Degraded Quorum' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		source, err := readClusterSSHQueueSource(ctx, store.db)
		if err != nil {
			t.Fatal(err)
		}
		result, err := store.queueClusterSSHInspections(ctx, "admission-test", source, targets[:1])
		if err != nil || result["target_count"] != 1 {
			t.Fatalf("replacement queue failed: %v", err)
		}
		progress, err := store.clusterSSHInspectionProgress(ctx, targets[0].Binding.OperationID)
		if err != nil || len(progress.Targets) != 1 || progress.State != "queued" {
			t.Fatalf("replacement progress failed: %v", err)
		}
	})
	t.Run("concurrent complete cohorts have one winner", func(t *testing.T) {
		store, aegis, ctx, source, targets, envelopes := sshQueueFixture(t)
		second := append([]clusterSSHPlannedTarget(nil), targets...)
		secondID := newClusterUUID()
		t.Cleanup(func() {
			_, _ = store.db.ExecContext(context.Background(), `DELETE FROM engine.cluster_operations WHERE id=$1`, secondID)
		})
		for i := range second {
			second[i].Binding.OperationID, second[i].Binding.TargetID = secondID, newClusterUUID()
			envelopes[i].Binding = second[i].Binding
			var err error
			second[i].Sealed, err = aegis.sealClusterSSHCredentials(ctx, envelopes[i])
			if err != nil {
				t.Fatal(err)
			}
		}
		store.db.SetMaxOpenConns(2)
		done := make(chan error, 2)
		for _, batch := range [][]clusterSSHPlannedTarget{targets, second} {
			go func(batch []clusterSSHPlannedTarget) {
				_, err := store.queueClusterSSHInspections(ctx, "admission-test", source, batch)
				done <- err
			}(batch)
		}
		wins := 0
		for range 2 {
			if <-done == nil {
				wins++
			}
		}
		var operations, count, credentials int
		if err := store.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM engine.cluster_operations WHERE id IN ($1,$2)),
 (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id IN ($1,$2)),(SELECT count(*) FROM engine.cluster_onboarding_credentials)`, targets[0].Binding.OperationID, secondID).Scan(&operations, &count, &credentials); err != nil {
			t.Fatal(err)
		}
		if wins != 1 || operations != 1 || count != 2 || credentials != 2 {
			t.Fatal("concurrent queue did not retain exactly one complete cohort")
		}
	})
}

func TestClusterSSHQueuePostgresPublicProgressRetainsEvidence(t *testing.T) {
	f := newSSHParentFixture(t)
	lease := f.complete(t, 0)
	var hidden string
	if err := f.c.store.db.QueryRowContext(f.ctx, `SELECT ciphertext FROM engine.cluster_onboarding_credentials WHERE target_id=$1`, lease.TargetID).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	// Synthetic encrypted fixture data deliberately placed in unrelated operation
	// fields. The public projection must not select or serialize either field.
	f.exec(t, `UPDATE engine.cluster_operations SET payload_json=$2,error_text=$3 WHERE id=$1`, f.op.ID, marshalClusterJSON(map[string]any{"private_fixture": hidden}), hidden)
	for _, mode := range []string{"running", "expired credential", "cancelled", "corrupt report"} {
		if mode == "expired credential" {
			f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET expires_at=1 WHERE target_id=$1`, lease.TargetID)
		}
		if mode == "cancelled" {
			if _, err := f.c.store.cancelClusterOperation(f.ctx, "admission-test", f.op.ID); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "corrupt report" {
			f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspection_json=$2 WHERE id=$1`, lease.TargetID, marshalClusterJSON(map[string]any{"password": hidden}))
		}
		progress, err := f.c.store.clusterSSHInspectionProgress(f.ctx, f.op.ID)
		if mode == "corrupt report" {
			if err == nil {
				t.Fatal("corrupt report reached public projection")
			}
			continue
		}
		if err != nil || len(progress.Targets) != 2 {
			t.Fatalf("public progress unavailable: %v", err)
		}
		if f.c.store.db.Stats().InUse != 0 {
			t.Fatal("progress retained database connection")
		}
		raw, err := json.Marshal(progress)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{hidden, "ciphertext", "private_fixture", "error_text", "aegis_generation"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("public progress disclosed private storage")
			}
		}
		target := progress.Targets[0]
		if target.InspectedGeneration != lease.Generation || target.InspectedAttempt != 1 || target.Report == nil || target.Report.MachineID != f.reports[0].MachineID || target.CredentialsReady != (mode == "running") {
			t.Fatal("progress lost historical inspection identity or current credential availability")
		}
		if progress.Targets[1].Report != nil {
			t.Fatal("pending target acquired an invented report")
		}
	}
	if _, err := f.c.store.clusterSSHInspectionProgress(f.ctx, newClusterUUID()); err != errClusterNotFound {
		t.Fatal("missing operation not reported")
	}
	if _, err := (*postgresOperatorStore)(nil).clusterSSHInspectionProgress(f.ctx, "bad-id"); err != errClusterNotFound {
		t.Fatal("invalid ID reached database")
	}
}
