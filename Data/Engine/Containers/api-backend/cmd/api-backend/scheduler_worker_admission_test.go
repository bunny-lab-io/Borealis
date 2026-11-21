package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func schedulerAdmissionSnapshot(site int64) map[string]any {
	return map[string]any{"worker_guid": schedulerK3sSiteWorkerGUID(site), "site_id": site,
		"uid": "b85f09f4-7c78-4eae-a1e5-f4ce67a360fb", "name": "site-worker-fixture", "node_name": "engine-one"}
}

func TestSchedulerWorkerAdmissionPlacement(t *testing.T) {
	for _, mode := range []string{"rank 0", "rank 1", "rank 2", "rank 3", "rank missing", "rank unknown", "rank normalized", "rank conflict", "unknown UID", "unknown node", "malformed node", "foreign GUID", "wrong site GUID", "invalid site", "fractional site", "text site"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "1")
			snapshot := schedulerAdmissionSnapshot(1)
			valid := strings.HasPrefix(mode, "rank ") && len(mode) == 6
			if valid {
				t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", mode[5:])
			}
			switch mode {
			case "rank missing":
				t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "")
			case "rank unknown":
				t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "4")
			case "rank normalized":
				t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "01")
			case "rank conflict":
				t.Setenv("BOREALIS_CLUSTER_SIZING_RANK", "2")
			case "unknown UID":
				delete(snapshot, "uid")
			case "unknown node":
				delete(snapshot, "node_name")
			case "malformed node":
				snapshot["node_name"] = " engine-one "
			case "foreign GUID":
				snapshot["worker_guid"] = "not-a-guid"
			case "wrong site GUID":
				snapshot["worker_guid"] = schedulerK3sSiteWorkerGUID(2)
			case "invalid site":
				snapshot["site_id"] = 0
			case "fractional site":
				snapshot["site_id"] = 1.5
			case "text site":
				snapshot["site_id"] = "1"
			}
			placement := schedulerWorkerPlacement(snapshot)
			if (placement != nil) != valid {
				t.Fatalf("placement valid=%v: %#v", valid, placement)
			}
			if valid && (placement["pod_uid"] != snapshot["uid"] || placement["node_name"] != snapshot["node_name"]) {
				t.Fatal("native placement identity changed")
			}
		})
	}
}

func TestSchedulerWorkerAdmissionMetadata(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		empty bool
	}{
		{`{}`, true}, {`{"worker_admission":{"version":1,"operations":{}}}`, true},
		{`{"worker_admission":{"version":1,"operations":{"token":{}}}}`, false},
		{`{"worker_admission":null}`, false}, {`{"worker_admission":{"version":2,"operations":{}}}`, false},
		{`{"worker_admission":{"version":1,"operations":null}}`, false}, {`{"worker_admission":{"version":1,"operations":[],"extra":1}}`, false},
		{`{"worker_admission":{"version":1,"operations":{"token":{}},"operations":{}}}`, false},
		{`{"worker_admission":{}}`, false}, {`null`, false}, {`[]`, false}, {`{} {}`, false},
	} {
		if schedulerAdmissionEmpty(tc.raw) != tc.empty {
			t.Fatalf("wrong prune eligibility: %s", tc.raw)
		}
	}
	old := `{"worker_admission":{"version":1,"operations":{"token":{"node_name":"original-node"}}}}`
	merged, generation, err := schedulerMergeAdmissionMetadata(old, map[string]any{
		schedulerWorkerAdmissionKey: map[string]any{"version": 1, "operations": map[string]any{}},
	}, 9, false)
	if err != nil || generation != 10 || !strings.Contains(mustJSON(merged), "original-node") || merged[schedulerWorkerPlacementKey] != nil {
		t.Fatal("route refresh erased ledger or kept unknown placement", generation, err, merged)
	}
	_, sameGeneration, err := schedulerMergeAdmissionMetadata(mustJSON(merged), merged, generation, false)
	if err != nil || sameGeneration != generation {
		t.Fatal("stable refresh changed generation", sameGeneration, err)
	}
}

func TestSchedulerWorkerAdmissionPostgresCustody(t *testing.T) {
	t.Run("activity metadata retains private custody", testSchedulerWorkerActivityCustodyPostgres)
	for _, mode := range []string{"upsert retains charge", "replacement retains old node", "unknown placement", "lost status retains charge", "prune unresolved", "prune malformed", "prune malformed metadata", "prune claimed without ledger", "prune missing route", "prune empty", "concurrent upsert", "concurrent prune"} {
		t.Run(mode, func(t *testing.T) {
			m, ctx, _ := schedulerOwnershipFixture(t)
			t.Setenv("BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR", t.TempDir())
			t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "1")
			const site int64 = 8192041
			snapshot := schedulerAdmissionSnapshot(site)
			guid := snapshot["worker_guid"].(string)
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = m.store.db.ExecContext(cleanup, `DELETE FROM engine.job_scheduler_worker_routes WHERE worker_guid=$1`, guid)
				_, _ = m.store.db.ExecContext(cleanup, `DELETE FROM engine.job_scheduler_workers WHERE worker_guid=$1`, guid)
			})
			metadata := func() map[string]any {
				value := schedulerWorkerRouteMetadataForHost(guid, "10.43.0.24", 8123, 8124, "borealis-operator")
				value[schedulerWorkerPlacementKey] = schedulerWorkerPlacement(snapshot)
				return value
			}
			// A single connection proves worker upsert releases its connection before
			// route registration opens another; no callback relies on spare capacity.
			m.store.db.SetMaxOpenConns(1)
			if err := m.upsertWorker(ctx, guid, "site-worker-fixture", site, schedulerWorkerStatusRunning, nil, nil, 8123, metadata()); err != nil {
				t.Fatal(err)
			}
			m.store.db.SetMaxOpenConns(5)
			ledger := fmt.Sprintf(`{"version":1,"operations":{"0123456789abcdef0123456789abcdef":{"job_id":1,"node_name":"engine-one","pod_uid":%q,"route_generation":1,"site_id":%d,"kind":"script","agent_guid":""}}}`, snapshot["uid"], site)
			if mode == "prune malformed" {
				ledger = `{"version":1,"operations":null}`
			}
			if mode == "prune empty" {
				ledger = `{"version":1,"operations":{}}`
			}
			if mode != "prune claimed without ledger" && !strings.HasPrefix(mode, "concurrent") {
				if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_worker_routes SET metadata_json=jsonb_set(metadata_json::jsonb,'{worker_admission}',$2::jsonb)::text WHERE worker_guid=$1`, guid, ledger); err != nil {
					t.Fatal(err)
				}
			}
			wantCount := int64(1)
			if mode == "prune unresolved" || mode == "prune malformed" || mode == "prune malformed metadata" || mode == "prune empty" {
				wantCount = 0
			}
			if wantCount > 0 && !strings.HasPrefix(mode, "concurrent") {
				if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_workers SET claimed_count=1 WHERE worker_guid=$1`, guid); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "prune malformed metadata" {
				if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_worker_routes SET metadata_json='{"worker_admission":{},"worker_admission":null}' WHERE worker_guid=$1`, guid); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "prune missing route" {
				if _, err := m.store.db.ExecContext(ctx, `DELETE FROM engine.job_scheduler_worker_routes WHERE worker_guid=$1`, guid); err != nil {
					t.Fatal(err)
				}
			}
			pruning := strings.Contains(mode, "prune")
			if pruning {
				if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_workers SET status='lost',stopped_at=1,last_seen_at=1,updated_at=1 WHERE worker_guid=$1`, guid); err != nil {
					t.Fatal(err)
				}
				if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_worker_routes SET status='lost',retired_at=1,updated_at=1 WHERE worker_guid=$1`, guid); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "replacement retains old node" {
				snapshot["uid"] = "b17ab452-286b-47ba-a033-2aaec0d7c855"
				snapshot["node_name"] = "engine-two"
			}
			if mode == "unknown placement" {
				delete(snapshot, "node_name")
			}
			action := func() error {
				if pruning {
					return m.pruneWorkerHistory(ctx, 300)
				}
				if mode == "lost status retains charge" {
					return m.stopWorker(ctx, guid, schedulerWorkerStatusLost)
				}
				return m.upsertWorker(ctx, guid, "site-worker-fixture", site, schedulerWorkerStatusRunning, nil, nil, 8123, metadata())
			}
			if strings.HasPrefix(mode, "concurrent") {
				blocker, err := m.store.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer rollbackQuietly(blocker)
				if _, err := blocker.ExecContext(ctx, `SELECT worker_guid FROM engine.job_scheduler_worker_routes WHERE worker_guid=$1 FOR UPDATE`, guid); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- action() }()
				waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
				for {
					var waiting bool
					if err := m.store.db.QueryRowContext(waitCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%engine.job_scheduler_worker_routes%')`).Scan(&waiting); err != nil {
						blocker.Rollback()
						<-done
						t.Fatal(err)
					}
					if waiting {
						break
					}
				}
				if _, err := blocker.ExecContext(ctx, `UPDATE engine.job_scheduler_worker_routes SET metadata_json=jsonb_set(metadata_json::jsonb,'{worker_admission}',$2::jsonb)::text WHERE worker_guid=$1`, guid, ledger); err != nil {
					blocker.Rollback()
					<-done
					t.Fatal(err)
				}
				if _, err := blocker.ExecContext(ctx, `UPDATE engine.job_scheduler_workers SET claimed_count=1 WHERE worker_guid=$1`, guid); err != nil {
					blocker.Rollback()
					<-done
					t.Fatal(err)
				}
				if err := blocker.Commit(); err != nil {
					<-done
					t.Fatal(err)
				}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			} else if err := action(); err != nil {
				t.Fatal(err)
			}
			var count, rows int64
			if err := m.store.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(claimed_count),0) FROM engine.job_scheduler_workers WHERE worker_guid=$1`, guid).Scan(&rows, &count); err != nil {
				t.Fatal(err)
			}
			if mode == "prune empty" {
				if rows != 0 {
					t.Fatal("empty terminal worker not pruned")
				}
				return
			}
			if rows != 1 || count != wantCount {
				t.Fatal("durable charge lost", rows, count)
			}
			if mode == "prune missing route" {
				return
			}
			var raw string
			var generation int64
			if err := m.store.db.QueryRowContext(ctx, `SELECT metadata_json,generation FROM engine.job_scheduler_worker_routes WHERE worker_guid=$1`, guid).Scan(&raw, &generation); err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatal(err)
			}
			if mode != "prune claimed without ledger" && mode != "prune malformed metadata" {
				var actual, expected any
				_ = json.Unmarshal(got[schedulerWorkerAdmissionKey], &actual)
				_ = json.Unmarshal([]byte(ledger), &expected)
				if mustJSON(actual) != mustJSON(expected) {
					t.Fatal("original admission ledger changed", raw)
				}
			}
			if mode == "unknown placement" && string(got[schedulerWorkerPlacementKey]) != "null" {
				t.Fatal("unknown placement retained eligibility", raw)
			}
			if mode == "replacement retains old node" && (generation != 2 || !strings.Contains(string(got[schedulerWorkerPlacementKey]), "engine-two")) {
				t.Fatal("replacement placement not independently bound", raw, generation)
			}
			if m.store.db.Stats().InUse != 0 {
				t.Fatal("SQL connection retained")
			}
		})
	}
}

func schedulerWorkerActivityFixture() map[string]any {
	return map[string]any{"version": 1, "token": "0123456789abcdef0123456789abcdef", "worker_guid": schedulerK3sSiteWorkerGUID(1),
		"job_id": 7, "site_id": 1, "route_generation": 1, "pod_uid": "b85f09f4-7c78-4eae-a1e5-f4ce67a360fb",
		"node_name": "engine-one", "kind": "script", "agent_guid": "", "receipt_sha256": strings.Repeat("a", 64)}
}

func TestSchedulerWorkerActivityCustody(t *testing.T) {
	for _, mode := range []string{"committed local", "committed Agent", "Agent missing grant", "local foreign grant", "malformed grant", "unresolved", "wrong activity", "bool version", "bool job", "malformed node", "lowercase Agent", "null Agent", "bad receipt", "extra field", "duplicate field"} {
		t.Run(mode, func(t *testing.T) {
			operation := schedulerWorkerActivityFixture()
			valid := strings.HasPrefix(mode, "committed")
			switch mode {
			case "committed Agent":
				operation["agent_guid"] = "ABCDEF12-ABCD-0000-0000-0123456789AB"
				operation["grant_nonce"] = strings.Repeat("b", 32)
			case "Agent missing grant":
				operation["agent_guid"] = "ABCDEF12-ABCD-0000-0000-0123456789AB"
			case "local foreign grant":
				operation["grant_nonce"] = strings.Repeat("b", 32)
			case "malformed grant":
				operation["agent_guid"] = "ABCDEF12-ABCD-0000-0000-0123456789AB"
				operation["grant_nonce"] = strings.Repeat("b", 31)
			case "unresolved":
				delete(operation, "receipt_sha256")
				operation["status"] = "Completed"
			case "wrong activity":
				operation["job_id"] = 8
			case "bool version":
				operation["version"] = true
			case "bool job":
				operation["job_id"] = true
			case "malformed node":
				operation["node_name"] = "engine..one"
			case "lowercase Agent":
				operation["agent_guid"] = "abcdef12-abcd-0000-0000-0123456789ab"
			case "null Agent":
				operation["agent_guid"] = nil
			case "bad receipt":
				operation["receipt_sha256"] = strings.Repeat("0", 64)
			case "extra field":
				operation["extra"] = true
			}
			raw := mustJSON(map[string]any{schedulerWorkerOperationKey: operation, "progress": "visible"})
			if mode == "duplicate field" {
				raw = strings.Replace(raw, `"version":1`, `"version":1,"version":1`, 1)
			}
			if workerOperationActivityDeletionAllowed(7, raw) != valid {
				t.Fatal("wrong deletion permission", raw)
			}
			row := activityHistoryRow{ID: sqlNullInt(7), MetadataJSON: sqlNullString(raw)}
			for _, payload := range []map[string]any{activityHistorySummaryPayload(row), activityHistoryDetailPayload(row)} {
				metadata := payload["metadata"].(map[string]any)
				if _, leaked := metadata[schedulerWorkerOperationKey]; leaked || metadata["progress"] != "visible" {
					t.Fatal("private receipt leaked or progress removed", metadata)
				}
			}
		})
	}
	for _, raw := range []string{"", `{}`, `{"other":"value"}`} {
		if !workerOperationActivityDeletionAllowed(7, raw) {
			t.Fatal("untracked deletion rejected", raw)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"worker_operation":null}`, `{"worker_operation":{},"worker_operation":{}}`} {
		if workerOperationActivityDeletionAllowed(7, raw) {
			t.Fatal("malformed custody permitted deletion", raw)
		}
	}
}

func testSchedulerWorkerActivityCustodyPostgres(t *testing.T) {
	m, ctx, _ := schedulerOwnershipFixture(t)
	var id int64
	if err := m.store.db.QueryRowContext(ctx, `INSERT INTO engine.activity_history(hostname,script_path,script_name,script_type,ran_at,status,metadata_json)
		VALUES('worker-custody-fixture','','fixture','',1,'Running','{}') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = m.store.db.ExecContext(cleanup, `DELETE FROM engine.activity_history WHERE id=$1`, id)
	})
	operation := schedulerWorkerActivityFixture()
	operation["job_id"] = id
	delete(operation, "receipt_sha256")
	raw := mustJSON(map[string]any{schedulerWorkerOperationKey: operation, "existing": "progress"})
	if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.activity_history SET metadata_json=$1 WHERE id=$2`, raw, id); err != nil {
		t.Fatal(err)
	}
	tx, err := m.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackQuietly(tx)
	forged := schedulerWorkerActivityFixture()
	if err := updateAgentMaintenanceRun(ctx, tx, agentMaintenanceRunRef{ActivityID: id, Metadata: map[string]any{schedulerWorkerOperationKey: forged}}, "Success", "ordinary status", "", "maintenance-operation"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := m.store.db.QueryRowContext(ctx, `SELECT metadata_json FROM engine.activity_history WHERE id=$1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	metadata, err := schedulerActivityMetadata(after)
	if err != nil {
		t.Fatal(err)
	}
	var preserved any
	_ = json.Unmarshal(metadata[schedulerWorkerOperationKey].(json.RawMessage), &preserved)
	if mustJSON(preserved) != mustJSON(operation) || workerOperationActivityDeletionAllowed(id, after) {
		t.Fatal("ordinary success forged terminal custody", after)
	}
	// Fresh progress parser keeps the private object opaque, including malformed
	// state; duplicate keys instead fail before any metadata replacement.
	metadata["patch_progress"] = map[string]any{"phase": "installing"}
	if !strings.Contains(mustJSON(metadata), `"worker_operation"`) {
		t.Fatal("progress parser erased custody")
	}
	if _, err := schedulerActivityMetadata(`{"worker_operation":{},"worker_operation":null}`); err == nil {
		t.Fatal("ambiguous progress metadata accepted")
	}
}
