package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests exercise evaluator-issued policy work against the canonical
// PostgreSQL schema. Receipt fixtures represent the independently committed
// WorkerAdmission tombstone, not an HTTP acknowledgement or an Agent process.
type policyEnforcementSQLFixture struct {
	t        *testing.T
	m        *goSchedulerManager
	ctx      context.Context
	owner    context.Context
	policyID int64
	siteID   int64
	guid     string
	hostname string
	agentID  string
	now      int64
}

func newPolicyEnforcementSQLFixture(t *testing.T) *policyEnforcementSQLFixture {
	t.Helper()
	m, ctx, _ := schedulerOwnershipFixture(t)
	f := &policyEnforcementSQLFixture{t: t, m: m, ctx: ctx, now: time.Now().Unix(), guid: strings.ToUpper(newClusterUUID())}
	// Preserve UUID shape while making case-folding cases deterministic.
	f.guid = f.guid[:len(f.guid)-1] + "A"
	f.hostname = "policy-sql-" + strings.ToLower(f.guid)
	f.agentID = f.hostname + "_" + f.guid + "_SYSTEM"
	f.owner = schedulerTestOwner(t, m, ctx, "policy-enforcement/"+newClusterUUID())
	if err := m.store.ensurePatchPolicySchema(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		queries := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM engine.activity_history WHERE hostname=$1`, []any{f.hostname}},
			{`DELETE FROM engine.job_scheduler_work_items WHERE payload_json::jsonb->'policy_enforcement'->>'device_guid'=$1`, []any{f.guid}},
			{`DELETE FROM engine.patch_policy_device_state WHERE device_guid=$1`, []any{f.guid}},
			{`DELETE FROM engine.patch_policies WHERE name=$1`, []any{f.hostname}},
			{`DELETE FROM engine.device_filter_sites WHERE filter_id IN (SELECT id FROM engine.device_filters WHERE name LIKE $1)`, []any{f.hostname + "%"}},
			{`DELETE FROM engine.device_filters WHERE name LIKE $1`, []any{f.hostname + "%"}},
			{`DELETE FROM engine.device_sites WHERE LOWER(device_hostname)=LOWER($1)`, []any{f.hostname}},
			{`DELETE FROM engine.devices WHERE hostname=$1`, []any{f.hostname}},
			{`DELETE FROM engine.job_scheduler_worker_routes WHERE site_id IN (SELECT id FROM engine.sites WHERE name LIKE $1)`, []any{f.hostname + "%"}},
			{`DELETE FROM engine.job_scheduler_workers WHERE site_id IN (SELECT id FROM engine.sites WHERE name LIKE $1)`, []any{f.hostname + "%"}},
			{`DELETE FROM engine.sites WHERE name LIKE $1`, []any{f.hostname + "%"}},
		}
		for _, q := range queries {
			if _, err := m.store.db.ExecContext(cleanup, q.sql, q.args...); err != nil {
				t.Error("policy fixture cleanup", err)
			}
		}
	})
	if err := m.store.db.QueryRowContext(ctx, `INSERT INTO engine.sites(name) VALUES($1) RETURNING id`, f.hostname).Scan(&f.siteID); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO engine.devices(guid,hostname,agent_id,status,last_seen,device_type,operating_system) VALUES($1,$2,$3,'active',$4,'Workstation','Windows 11')`, f.guid, f.hostname, f.agentID, f.now)
	f.exec(`INSERT INTO engine.device_sites(device_hostname,site_id,assigned_at) VALUES($1,$2,$3)`, f.hostname, f.siteID, f.now)
	if err := m.store.db.QueryRowContext(ctx, `INSERT INTO engine.patch_policies(name,policy_type,enabled,role_scope,managed_update_mode,install_schedule_type,created_at,updated_at) VALUES($1,'site',1,'Workstation',1,'weekly',$2,$2) RETURNING id`, f.hostname, f.now).Scan(&f.policyID); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO engine.patch_policy_sites(policy_id,site_id,created_at) VALUES($1,$2,$3)`, f.policyID, f.siteID, f.now)
	return f
}

func (f *policyEnforcementSQLFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.m.store.db.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *policyEnforcementSQLFixture) evaluate(scheduledTS int64) (int64, error) {
	_, err := f.m.store.evaluatePatchPoliciesAt(f.owner, operatorProfile{Username: "policy-sql", Role: "Admin"}, f.policyID, scheduledTS, f.now, true)
	if err != nil {
		return 0, err
	}
	var workID int64
	err = f.m.store.db.QueryRowContext(f.ctx, `SELECT id FROM engine.job_scheduler_work_items WHERE kind='patch_policy_enforcement' AND payload_json::jsonb->'policy_enforcement'->>'device_guid'=$1 AND (payload_json::jsonb->'policy_enforcement'->>'scheduled_ts')::bigint=$2`, f.guid, scheduledTS).Scan(&workID)
	return workID, err
}

func (f *policyEnforcementSQLFixture) claim(workID int64) (schedulerWorkItem, context.Context) {
	f.t.Helper()
	item, err := f.m.claimNextKindWorkItem(f.owner, []string{"patch_policy_enforcement"})
	if err != nil || item == nil || item.ID != workID {
		f.t.Fatalf("policy claim id=%d item=%+v error=%v", workID, item, err)
	}
	return *item, context.WithValue(f.owner, schedulerWorkContextKey{}, *item)
}

func (f *policyEnforcementSQLFixture) state() string {
	f.t.Helper()
	var raw string
	if err := f.m.store.db.QueryRowContext(f.ctx, `SELECT COALESCE(json_agg(to_jsonb(s) ORDER BY id),'[]'::json)::text FROM engine.patch_policy_device_state s WHERE device_guid=$1`, f.guid).Scan(&raw); err != nil {
		f.t.Fatal(err)
	}
	return raw
}

func (f *policyEnforcementSQLFixture) work(workID int64) (string, map[string]any) {
	f.t.Helper()
	var status, raw string
	if err := f.m.store.db.QueryRowContext(f.ctx, `SELECT status,payload_json FROM engine.job_scheduler_work_items WHERE id=$1`, workID).Scan(&status, &raw); err != nil {
		f.t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		f.t.Fatal(err)
	}
	return status, payload
}

func (f *policyEnforcementSQLFixture) activity(workID int64) int64 {
	f.t.Helper()
	var id int64
	if err := f.m.store.db.QueryRowContext(f.ctx, `SELECT id FROM engine.activity_history WHERE metadata_json::jsonb->>'scheduler_execution_id'=$1`, fmt.Sprintf("work:%d:work", workID)).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *policyEnforcementSQLFixture) assertPoolsReleased() error {
	for _, pool := range []*sql.DB{f.m.store.db, f.m.ownershipPool()} {
		ctx, cancel := context.WithTimeout(f.ctx, time.Second)
		conn, err := pool.Conn(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("policy dispatch retained a database connection: %w", err)
		}
		if err := conn.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (f *policyEnforcementSQLFixture) admit(activityID int64) error {
	operation := schedulerWorkerActivityFixture()
	operation["job_id"], operation["site_id"] = activityID, f.siteID
	operation["worker_guid"] = schedulerK3sSiteWorkerGUID(f.siteID)
	operation["kind"], operation["agent_guid"] = "patch_policy_enforcement_request", f.guid
	operation["grant_nonce"] = strings.Repeat("b", 32)
	delete(operation, "receipt_sha256")
	result, err := f.m.store.db.ExecContext(f.ctx, `UPDATE engine.activity_history SET metadata_json=jsonb_set(metadata_json::jsonb,'{worker_operation}',$2::jsonb)::text WHERE id=$1 AND NOT metadata_json::jsonb ? 'worker_operation'`, activityID, mustJSON(operation))
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		return fmt.Errorf("original admission changed rows=%d error=%v", rows, err)
	}
	return nil
}

// WorkerAdmission canonicalizes only this exact terminal envelope. Keeping the
// fixed field order explicit makes this oracle independent of the Go projector.
// Complete only the admission already retained before the simulated Agent ACK.
func (f *policyEnforcementSQLFixture) commitReceipt(activityID int64, terminal string) string {
	f.t.Helper()
	var raw string
	if err := f.m.store.db.QueryRowContext(f.ctx, `SELECT metadata_json::jsonb->'worker_operation' FROM engine.activity_history WHERE id=$1`, activityID).Scan(&raw); err != nil {
		f.t.Fatal(err)
	}
	var operation map[string]any
	if err := json.Unmarshal([]byte(raw), &operation); err != nil {
		f.t.Fatal(err)
	}
	token, ok := operation["token"].(string)
	if !ok || operation["grant_nonce"] == nil || operation["receipt_sha256"] != nil {
		f.t.Fatal("missing original uncommitted grant")
	}
	canonical := fmt.Sprintf(`{"context":{"worker_operation":{"token":"%s","version":1}},"job_id":%d,"status":"%s","stderr":"","stdout":""}`, token, activityID, terminal)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	operation["receipt_sha256"] = digest
	status := terminal
	if terminal == "Completed" {
		status = "Success"
	}
	f.exec(`UPDATE engine.activity_history SET status=$2,stdout='',stderr='',finished_at=$3,updated_at=$3,metadata_json=jsonb_set(metadata_json::jsonb,'{worker_operation}',$4::jsonb)::text WHERE id=$1`, activityID, status, f.now, mustJSON(operation))
	return digest
}

func (f *policyEnforcementSQLFixture) installWorker(reply string) (*atomic.Int64, <-chan error) {
	f.t.Helper()
	count := new(atomic.Int64)
	checks := make(chan error, 8)
	redirectURL := ""
	if reply == "HTTP 307" {
		destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count.Add(1)
			checks <- errors.New("redirect replayed original policy command")
			_, _ = w.Write([]byte(`{"called":true,"response":{"status":"applied"}}`))
		}))
		f.t.Cleanup(destination.Close)
		redirectURL = destination.URL
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		var request map[string]any
		err := json.NewDecoder(r.Body).Decode(&request)
		if err == nil {
			err = f.assertPoolsReleased()
		}
		payload := schedulerAnyMap(request["payload"])
		if err == nil && (request["event_name"] != "patch_policy_enforcement_request" || request["hostname"] != f.hostname || request["service_mode"] != "system" || payload["agent_id"] != f.agentID || coerceInt64(payload["job_id"]) <= 0) {
			err = fmt.Errorf("dispatch lost original target/activity: %v", request)
		}
		if err == nil {
			var raw string
			err = f.m.store.db.QueryRowContext(f.ctx, `SELECT w.payload_json::jsonb->'policy_enforcement' FROM engine.job_scheduler_work_items w JOIN engine.activity_history a ON (a.metadata_json::jsonb->>'scheduler_work_id')::bigint=w.id WHERE a.id=$1`, coerceInt64(payload["job_id"])).Scan(&raw)
			var original map[string]any
			if err == nil {
				err = json.Unmarshal([]byte(raw), &original)
			}
			context := schedulerAnyMap(payload["context"])
			if err == nil && (payload["request_id"] != original["request_id"] || payload["mode"] != original["mode"] || context["policy_run_id"] != original["policy_run_id"] || context["policy_id"] != original["policy_id"] || request["allow_pending"] != false) {
				err = errors.New("dispatch changed original policy request or enabled pending retry")
			}
		}
		if err == nil {
			err = f.admit(coerceInt64(payload["job_id"]))
		}
		checks <- err
		if err != nil {
			http.Error(w, "fixture invariant", http.StatusInternalServerError)
			return
		}
		if redirectURL != "" {
			w.Header().Set("Location", redirectURL)
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		if reply == "lost ACK" {
			conn, _, hijackErr := w.(http.Hijacker).Hijack()
			if hijackErr == nil {
				_ = conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"called":true,"response":{"status":"applied","mode":"managed"}}`))
	}))
	f.t.Cleanup(server.Close)
	f.m.httpClient = server.Client()
	f.m.secret = []byte("policy-sql-fixture-secret")
	route := routeForTestWorker(f.t, server.URL)
	worker := schedulerK3sSiteWorkerGUID(f.siteID)
	f.t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "1")
	f.t.Setenv("BOREALIS_CLUSTER_SIZING_RANK", "1")
	placement := schedulerWorkerPlacement(schedulerAdmissionSnapshot(f.siteID))
	placement["route_generation"] = 1
	f.exec(`INSERT INTO engine.job_scheduler_worker_routes(worker_guid,site_id,container_name,route_name,route_path_prefix,route_file_path,upstream_scheme,upstream_host,upstream_port,status,generation,metadata_json,created_at,updated_at) VALUES($1,$2,'policy-sql','policy-sql','/fixture','','http',$3,$4,'active',1,$6,$5,$5)`, worker, f.siteID, route.UpstreamHost, route.UpstreamPort, f.now, mustJSON(map[string]any{schedulerWorkerPlacementKey: placement}))
	f.m.store.db.SetMaxOpenConns(1)
	f.m.ownershipPool().SetMaxOpenConns(1)
	return count, checks
}

func policyEnforcementSQLCheckRequest(t *testing.T, checks <-chan error) {
	t.Helper()
	select {
	case err := <-checks:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("worker request did not reach checked fixture")
	}
}

func TestSchedulerPatchPolicyEnforcementPostgresIntent(t *testing.T) {
	t.Run("same run retains exact intent", func(t *testing.T) {
		f := newPolicyEnforcementSQLFixture(t)
		first, err := f.evaluate(schedulerFloorMinute(f.now))
		if err != nil {
			t.Fatal(err)
		}
		state := f.state()
		_, before := f.work(first)
		second, err := f.evaluate(schedulerFloorMinute(f.now))
		if err != nil || second != first {
			t.Fatalf("repeated evaluator changed intent first=%d second=%d error=%v", first, second, err)
		}
		_, after := f.work(first)
		if f.state() != state || mustJSON(before) != mustJSON(after) {
			t.Fatal("same policy run rewrote retained intent")
		}
		var workCount, runCount, activityCount int
		if err := f.m.store.db.QueryRowContext(f.ctx, `SELECT (SELECT COUNT(*) FROM engine.job_scheduler_work_items WHERE payload_json::jsonb->'policy_enforcement'->>'device_guid'=$1),(SELECT COUNT(*) FROM engine.patch_policy_runs WHERE policy_id=$2),(SELECT COUNT(*) FROM engine.activity_history WHERE hostname=$3)`, f.guid, f.policyID, f.hostname).Scan(&workCount, &runCount, &activityCount); err != nil {
			t.Fatal(err)
		}
		if workCount != 1 || runCount != 1 || activityCount != 0 {
			t.Fatalf("evaluator duplicated or dispatched work=%d runs=%d activities=%d", workCount, runCount, activityCount)
		}
	})
	t.Run("lowercase stored GUID retains canonical intent", func(t *testing.T) {
		f := newPolicyEnforcementSQLFixture(t)
		f.exec(`UPDATE engine.devices SET guid=LOWER(guid) WHERE guid=$1`, f.guid)
		workID, err := f.evaluate(schedulerFloorMinute(f.now))
		if err != nil {
			t.Fatal("legal lowercase GUID could not publish canonical intent", err)
		}
		count, checks := f.installWorker("applied ACK")
		item, workCtx := f.claim(workID)
		if err := f.m.runPatchPolicyEnforcementWorkItem(workCtx, item); !errors.Is(err, errSchedulerOutcomeUnknown) {
			t.Fatal(err)
		}
		policyEnforcementSQLCheckRequest(t, checks)
		f.commitReceipt(f.activity(workID), "Completed")
		if err := f.m.reconcilePatchPolicyEnforcementReceipts(f.owner, f.now); err != nil {
			t.Fatal(err)
		}
		f.assertProjection(workID, "applied")
		var storedGUID string
		if err := f.m.store.db.QueryRowContext(f.ctx, `SELECT guid FROM engine.devices WHERE hostname=$1`, f.hostname).Scan(&storedGUID); err != nil || storedGUID != strings.ToLower(f.guid) || count.Load() != 1 {
			t.Fatalf("canonical intent changed original stored identity %q sends=%d error=%v", storedGUID, count.Load(), err)
		}
	})
	t.Run("duplicate case aliases cannot publish intent", func(t *testing.T) {
		f := newPolicyEnforcementSQLFixture(t)
		aliasHost := f.hostname + "-alias"
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := f.m.store.db.ExecContext(ctx, `DELETE FROM engine.device_sites WHERE device_hostname=$1`, aliasHost); err != nil {
				t.Error(err)
			}
			if _, err := f.m.store.db.ExecContext(ctx, `DELETE FROM engine.devices WHERE hostname=$1`, aliasHost); err != nil {
				t.Error(err)
			}
		})
		f.exec(`INSERT INTO engine.devices(guid,hostname,agent_id,status,last_seen,device_type,operating_system) VALUES(LOWER($1),$2,$3,'active',$4,'Workstation','Windows 11')`, f.guid, aliasHost, aliasHost+"_"+f.guid+"_SYSTEM", f.now)
		f.exec(`INSERT INTO engine.device_sites(device_hostname,site_id,assigned_at) VALUES($1,$2,$3)`, aliasHost, f.siteID, f.now)
		_, err := f.m.store.evaluatePatchPoliciesAt(f.owner, operatorProfile{Username: "policy-sql", Role: "Admin"}, f.policyID, schedulerFloorMinute(f.now), f.now, true)
		if err == nil {
			t.Fatal("case aliases published ambiguous intent")
		}
		var works int
		if err := f.m.store.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM engine.job_scheduler_work_items WHERE payload_json::jsonb->'policy_enforcement'->>'device_guid'=$1`, f.guid).Scan(&works); err != nil || works != 0 || f.state() != "[]" {
			t.Fatalf("ambiguous aliases partially published work=%d error=%v", works, err)
		}
	})
	t.Run("concurrent evaluators publish one original run", testPolicyEnforcementSQLConcurrentIntent)
}

func TestSchedulerPatchPolicyEnforcementPostgresDispatch(t *testing.T) {
	for _, reply := range []string{"applied ACK", "lost ACK", "HTTP 307"} {
		t.Run(reply, func(t *testing.T) {
			f := newPolicyEnforcementSQLFixture(t)
			workID, err := f.evaluate(schedulerFloorMinute(f.now))
			if err != nil {
				t.Fatal(err)
			}
			count, checks := f.installWorker(reply)
			item, workCtx := f.claim(workID)
			if err := f.m.runPatchPolicyEnforcementWorkItem(workCtx, item); !errors.Is(err, errSchedulerOutcomeUnknown) {
				t.Fatalf("HTTP acknowledgement settled policy command: %v", err)
			}
			policyEnforcementSQLCheckRequest(t, checks)
			if count.Load() != 1 {
				t.Fatal("initial policy dispatch count", count.Load())
			}
			activityID := f.activity(workID)
			var receipt sql.NullString
			if err := f.m.store.db.QueryRowContext(f.ctx, `SELECT metadata_json::jsonb->'worker_operation'->>'receipt_sha256' FROM engine.activity_history WHERE id=$1`, activityID).Scan(&receipt); err != nil || receipt.Valid {
				t.Fatalf("ACK became terminal receipt=%v error=%v", receipt, err)
			}
			f.exec(`UPDATE engine.job_scheduler_work_items SET lease_expires_at=$2 WHERE id=$1`, workID, f.now-1)
			if err := f.m.expireStaleLeases(f.owner); err != nil {
				t.Fatal(err)
			}
			reclaimed, reclaimedCtx := f.claim(workID)
			if reclaimed.AttemptCount != item.AttemptCount+1 {
				t.Fatal("fixture failed to reclaim original work")
			}
			if err := f.m.runPatchPolicyEnforcementWorkItem(reclaimedCtx, reclaimed); err == nil {
				t.Fatal("uncertain dispatch silently succeeded after reclaim")
			}
			if count.Load() != 1 || f.activity(workID) != activityID {
				t.Fatal("uncertain policy dispatch replayed or replaced activity")
			}
			if err := f.m.acknowledgeSchedulerDispatch(workCtx, "stale"); !errors.Is(err, errSchedulerOwnershipLost) {
				t.Fatalf("stale holder acknowledged original dispatch: %v", err)
			}
		})
	}
}

func (f *policyEnforcementSQLFixture) assertProjection(workID int64, want string) {
	f.t.Helper()
	var status string
	var desired int64
	if err := f.m.store.db.QueryRowContext(f.ctx, `SELECT enforcement_status,(metadata_json::jsonb->'policy_enforcement'->>'desired_work_id')::bigint FROM engine.patch_policy_device_state WHERE device_guid=$1`, f.guid).Scan(&status, &desired); err != nil {
		f.t.Fatal(err)
	}
	if status != want || desired != workID {
		f.t.Fatalf("policy projection status=%s desired=%d; want %s work=%d", status, desired, want, workID)
	}
}

func TestSchedulerPatchPolicyEnforcementPostgresReceipt(t *testing.T) {
	for _, mode := range []string{"Completed", "Failed", "failed work receives late receipt", "status only", "changed stdout", "null stdout", "changed stderr", "changed status", "missing grant", "wrong Agent", "wrong site", "changed original request", "changed execution identity"} {
		t.Run(mode, func(t *testing.T) {
			f := newPolicyEnforcementSQLFixture(t)
			workID, err := f.evaluate(schedulerFloorMinute(f.now))
			if err != nil {
				t.Fatal(err)
			}
			count, checks := f.installWorker("applied ACK")
			item, workCtx := f.claim(workID)
			_ = f.m.runPatchPolicyEnforcementWorkItem(workCtx, item)
			policyEnforcementSQLCheckRequest(t, checks)
			activityID := f.activity(workID)
			f.assertProjection(workID, "pending")
			if mode == "failed work receives late receipt" {
				if err := f.m.completeWorkItem(workCtx, item, workStatusFailed, "fixture unknown outcome"); err != nil {
					t.Fatal(err)
				}
				status, _ := f.work(workID)
				if status != workStatusFailed {
					t.Fatal("fixture did not end queue work before receipt")
				}
			}
			terminal := "Completed"
			if mode == "Failed" {
				terminal = "Failed"
			}
			if mode == "status only" {
				f.exec(`UPDATE engine.activity_history SET status='Success' WHERE id=$1`, activityID)
			} else {
				f.commitReceipt(activityID, terminal)
			}
			switch mode {
			case "changed stdout":
				f.exec(`UPDATE engine.activity_history SET stdout='changed' WHERE id=$1`, activityID)
			case "null stdout":
				f.exec(`UPDATE engine.activity_history SET stdout=NULL WHERE id=$1`, activityID)
			case "changed stderr":
				f.exec(`UPDATE engine.activity_history SET stderr='changed' WHERE id=$1`, activityID)
			case "changed status":
				f.exec(`UPDATE engine.activity_history SET status='Failed' WHERE id=$1`, activityID)
			case "missing grant":
				f.exec(`UPDATE engine.activity_history SET metadata_json=(metadata_json::jsonb #- '{worker_operation,grant_nonce}')::text WHERE id=$1`, activityID)
			case "wrong Agent":
				f.exec(`UPDATE engine.activity_history SET metadata_json=jsonb_set(metadata_json::jsonb,'{worker_operation,agent_guid}',to_jsonb($2::text))::text WHERE id=$1`, activityID, strings.ToUpper(newClusterUUID()))
			case "wrong site":
				f.exec(`UPDATE engine.activity_history SET metadata_json=jsonb_set(metadata_json::jsonb,'{worker_operation,site_id}',to_jsonb($2::bigint))::text WHERE id=$1`, activityID, f.siteID+100000)
			case "changed original request":
				f.exec(`UPDATE engine.activity_history SET metadata_json=jsonb_set(metadata_json::jsonb,'{policy_enforcement,request_id}','"substituted"'::jsonb)::text WHERE id=$1`, activityID)
			case "changed execution identity":
				f.exec(`UPDATE engine.activity_history SET metadata_json=jsonb_set(metadata_json::jsonb,'{scheduler_execution_id}','"work:999:work"'::jsonb)::text WHERE id=$1`, activityID)
			}
			before := f.state()
			_, workBefore := f.work(workID)
			projectErr := f.m.reconcilePatchPolicyEnforcementReceipts(f.owner, f.now)
			valid := mode == "Completed" || mode == "Failed" || mode == "failed work receives late receipt"
			if !valid {
				_, workAfter := f.work(workID)
				if f.state() != before || mustJSON(workAfter) != mustJSON(workBefore) {
					t.Fatal("uncommitted or altered receipt changed policy state")
				}
			} else {
				if projectErr != nil {
					t.Fatal(projectErr)
				}
				want := "applied"
				if terminal == "Failed" {
					want = "failed"
				}
				f.assertProjection(workID, want)
				settled := f.state()
				if err := f.m.reconcilePatchPolicyEnforcementReceipts(f.owner, f.now); err != nil {
					t.Fatal(err)
				}
				if f.state() != settled {
					t.Fatal("exact committed receipt replay rewrote policy state")
				}
			}
			if count.Load() != 1 {
				t.Fatal("receipt projection dispatched policy command")
			}
			if err := f.assertPoolsReleased(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func policyEnforcementSQLBlock(t *testing.T, f *policyEnforcementSQLFixture, query string, args ...any) (*sql.Tx, int) {
	t.Helper()
	tx, err := f.m.store.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	var pid int
	if err := tx.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
	return tx, pid
}

func policyEnforcementSQLWaitBlocked(t *testing.T, f *policyEnforcementSQLFixture, blocker int, finished <-chan error) {
	policyEnforcementSQLWaitBlockedCount(t, f, blocker, finished, 1)
}

func policyEnforcementSQLWaitBlockedCount(t *testing.T, f *policyEnforcementSQLFixture, blocker int, finished <-chan error, wanted int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting int
		if err := f.m.store.db.QueryRowContext(ctx, `WITH RECURSIVE blocked(pid) AS (SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)) UNION SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid)) WHERE a.datname=current_database()) SELECT COUNT(*) FROM blocked`, blocker).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= wanted {
			return
		}
		select {
		case err := <-finished:
			t.Fatalf("operation escaped SQL ownership barrier: %v", err)
		case <-ctx.Done():
			t.Fatal("operation never reached SQL ownership barrier")
		case <-tick.C:
		}
	}
}

func TestSchedulerPatchPolicyEnforcementPostgresRollback(t *testing.T) {
	t.Run("evaluator run state and intent roll back together", func(t *testing.T) {
		f := newPolicyEnforcementSQLFixture(t)
		blocker, pid := policyEnforcementSQLBlock(t, f, `SELECT guid FROM engine.devices WHERE guid=$1 FOR UPDATE`, f.guid)
		ctx, cancel := context.WithCancel(f.owner)
		defer cancel()
		finished := make(chan error, 1)
		joined := make(chan struct{})
		go func() {
			defer close(joined)
			_, err := f.m.store.evaluatePatchPoliciesAt(ctx, operatorProfile{Username: "policy-sql", Role: "Admin"}, f.policyID, schedulerFloorMinute(f.now), f.now, true)
			finished <- err
		}()
		defer func() {
			cancel()
			_ = blocker.Rollback()
			<-joined
		}()
		policyEnforcementSQLWaitBlocked(t, f, pid, finished)
		cancel()
		if err := <-finished; err == nil {
			t.Fatal("cancelled evaluation committed")
		}
		if err := blocker.Rollback(); err != nil {
			t.Fatal(err)
		}
		var workCount, runCount int
		if err := f.m.store.db.QueryRowContext(f.ctx, `SELECT (SELECT COUNT(*) FROM engine.job_scheduler_work_items WHERE payload_json::jsonb->'policy_enforcement'->>'device_guid'=$1),(SELECT COUNT(*) FROM engine.patch_policy_runs WHERE policy_id=$2)`, f.guid, f.policyID).Scan(&workCount, &runCount); err != nil {
			t.Fatal(err)
		}
		if workCount != 0 || runCount != 0 || f.state() != "[]" {
			t.Fatalf("partial evaluator publication work=%d runs=%d state=%s", workCount, runCount, f.state())
		}
		if _, err := f.evaluate(schedulerFloorMinute(f.now)); err != nil {
			t.Fatal("rolled-back evaluation could not retry", err)
		}
	})
	t.Run("receipt projection and execution acknowledgement roll back together", func(t *testing.T) {
		f := newPolicyEnforcementSQLFixture(t)
		workID, err := f.evaluate(schedulerFloorMinute(f.now))
		if err != nil {
			t.Fatal(err)
		}
		_, checks := f.installWorker("applied ACK")
		item, workCtx := f.claim(workID)
		_ = f.m.runPatchPolicyEnforcementWorkItem(workCtx, item)
		policyEnforcementSQLCheckRequest(t, checks)
		f.commitReceipt(f.activity(workID), "Completed")
		f.m.store.db.SetMaxOpenConns(5)
		f.m.ownershipPool().SetMaxOpenConns(5)
		before := f.state()
		_, executionBefore := f.work(workID)
		blocker, pid := policyEnforcementSQLBlock(t, f, `SELECT id FROM engine.patch_policy_device_state WHERE device_guid=$1 FOR UPDATE`, f.guid)
		ctx, cancel := context.WithCancel(f.owner)
		defer cancel()
		finished := make(chan error, 1)
		joined := make(chan struct{})
		go func() {
			defer close(joined)
			finished <- f.m.reconcilePatchPolicyEnforcementReceipts(ctx, f.now)
		}()
		defer func() {
			cancel()
			_ = blocker.Rollback()
			<-joined
		}()
		policyEnforcementSQLWaitBlocked(t, f, pid, finished)
		cancel()
		if err := <-finished; err == nil {
			t.Fatal("cancelled receipt projection committed")
		}
		if err := blocker.Rollback(); err != nil {
			t.Fatal(err)
		}
		_, executionAfter := f.work(workID)
		if f.state() != before || mustJSON(executionAfter) != mustJSON(executionBefore) {
			t.Fatal("cancelled projection retained partial state or execution acknowledgement")
		}
		if err := f.m.reconcilePatchPolicyEnforcementReceipts(f.owner, f.now); err != nil {
			t.Fatal(err)
		}
		f.assertProjection(workID, "applied")
	})
	for _, boundary := range []string{"dispatch work expiry", "dispatch leader expiry", "projector leader expiry"} {
		t.Run(boundary, func(t *testing.T) {
			f := newPolicyEnforcementSQLFixture(t)
			workID, err := f.evaluate(schedulerFloorMinute(f.now))
			if err != nil {
				t.Fatal(err)
			}
			count, checks := f.installWorker("applied ACK")
			item, workCtx := f.claim(workID)
			projector := boundary == "projector leader expiry"
			if projector {
				_ = f.m.runPatchPolicyEnforcementWorkItem(workCtx, item)
				policyEnforcementSQLCheckRequest(t, checks)
				f.commitReceipt(f.activity(workID), "Completed")
			}
			f.m.store.db.SetMaxOpenConns(5)
			f.m.ownershipPool().SetMaxOpenConns(5)
			before := f.state()
			_, workBefore := f.work(workID)
			query := `SELECT guid FROM engine.devices WHERE guid=$1 FOR UPDATE`
			if projector {
				query = `SELECT id FROM engine.patch_policy_device_state WHERE device_guid=$1 FOR UPDATE`
			}
			blocker, pid := policyEnforcementSQLBlock(t, f, query, f.guid)
			expires := time.Now().Unix() + 2
			if boundary == "dispatch work expiry" {
				f.exec(`UPDATE engine.job_scheduler_work_items SET lease_expires_at=$2 WHERE id=$1`, workID, expires)
			} else {
				f.exec(`UPDATE engine.cluster_application_leases SET expires_at=$1 WHERE name='scheduler-leader'`, expires)
			}
			ctx, cancel := context.WithCancel(workCtx)
			finished := make(chan error, 1)
			joined := make(chan struct{})
			go func() {
				defer close(joined)
				if projector {
					finished <- f.m.reconcilePatchPolicyEnforcementReceipts(ctx, f.now)
				} else {
					finished <- f.m.runPatchPolicyEnforcementWorkItem(ctx, item)
				}
			}()
			defer func() { cancel(); _ = blocker.Rollback(); <-joined }()
			// Native lock evidence proves initial ownership succeeded. Cross its
			// original Unix-second expiry inside the unchanged five-second scope.
			policyEnforcementSQLWaitBlocked(t, f, pid, finished)
			timer := time.NewTimer(time.Until(time.Unix(expires+1, 0).Add(10 * time.Millisecond)))
			defer timer.Stop()
			select {
			case <-timer.C:
			case err := <-finished:
				t.Fatalf("ownership operation escaped held barrier: %v", err)
			case <-f.ctx.Done():
				t.Fatal(f.ctx.Err())
			}
			if time.Now().Unix() <= expires {
				t.Fatal("fixture did not cross original lease expiry")
			}
			if err := blocker.Rollback(); err != nil {
				t.Fatal(err)
			}
			err = <-finished
			_, workAfter := f.work(workID)
			wantSends := int64(0)
			if projector {
				wantSends = 1
			}
			stateChanged := f.state() != before
			workChanged := mustJSON(workBefore) != mustJSON(workAfter)
			var activities int64
			if queryErr := f.m.store.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM engine.activity_history WHERE hostname=$1`, f.hostname).Scan(&activities); queryErr != nil {
				t.Fatal(queryErr)
			}
			if !errors.Is(err, errSchedulerOwnershipLost) || count.Load() != wantSends || stateChanged || workChanged || activities != wantSends {
				t.Fatalf("expired ownership committed policy effects: sends=%d activities=%d state_changed=%t work_changed=%t error=%v", count.Load(), activities, stateChanged, workChanged, err)
			}
		})
	}
}

func (f *policyEnforcementSQLFixture) newSite(label string) int64 {
	f.t.Helper()
	var id int64
	if err := f.m.store.db.QueryRowContext(f.ctx, `INSERT INTO engine.sites(name) VALUES($1) RETURNING id`, f.hostname+"-"+label).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func TestSchedulerPatchPolicyEnforcementPostgresPredecessor(t *testing.T) {
	for _, mode := range []string{"new run", "new run same mode", "state deleted", "policy deleted", "site moved", "acknowledged succeeded", "failed outcome unknown", "activity missing"} {
		t.Run(mode, func(t *testing.T) {
			f := newPolicyEnforcementSQLFixture(t)
			scheduled := schedulerFloorMinute(f.now)
			oldID, err := f.evaluate(scheduled)
			if err != nil {
				t.Fatal(err)
			}
			oldCalls, checks := f.installWorker("applied ACK")
			oldItem, oldCtx := f.claim(oldID)
			_ = f.m.runPatchPolicyEnforcementWorkItem(oldCtx, oldItem)
			policyEnforcementSQLCheckRequest(t, checks)
			activityID := f.activity(oldID)
			switch mode {
			case "state deleted":
				f.exec(`DELETE FROM engine.patch_policy_device_state WHERE device_guid=$1`, f.guid)
			case "policy deleted":
				f.exec(`DELETE FROM engine.patch_policies WHERE id=$1`, f.policyID)
				if err := f.m.store.db.QueryRowContext(f.ctx, `INSERT INTO engine.patch_policies(name,policy_type,enabled,role_scope,managed_update_mode,install_schedule_type,created_at,updated_at) VALUES($1,'site',1,'Workstation',1,'weekly',$2,$2) RETURNING id`, f.hostname, f.now).Scan(&f.policyID); err != nil {
					t.Fatal(err)
				}
				f.exec(`INSERT INTO engine.patch_policy_sites(policy_id,site_id,created_at) VALUES($1,$2,$3)`, f.policyID, f.siteID, f.now)
			case "site moved":
				f.siteID = f.newSite("moved")
				f.exec(`UPDATE engine.device_sites SET site_id=$2 WHERE device_hostname=$1`, f.hostname, f.siteID)
				f.exec(`UPDATE engine.patch_policy_sites SET site_id=$2 WHERE policy_id=$1`, f.policyID, f.siteID)
			case "acknowledged succeeded":
				if err := f.m.acknowledgeSchedulerDispatch(oldCtx, ""); err != nil {
					t.Fatal(err)
				}
				if err := f.m.completeWorkItem(oldCtx, oldItem, workStatusSucceeded, "HTTP ACK only"); err != nil {
					t.Fatal(err)
				}
			case "failed outcome unknown":
				if err := f.m.completeWorkItem(oldCtx, oldItem, workStatusFailed, "fixture unknown outcome"); err != nil {
					t.Fatal(err)
				}
			case "activity missing":
				// Simulate missing historical evidence, not an authorized deletion.
				f.exec(`DELETE FROM engine.activity_history WHERE id=$1`, activityID)
			}
			if mode != "new run same mode" {
				f.exec(`UPDATE engine.patch_policies SET managed_update_mode=0 WHERE id=$1`, f.policyID)
			}
			newID, err := f.evaluate(scheduled + 60)
			if err != nil || newID == oldID {
				t.Fatalf("successor generation id=%d error=%v", newID, err)
			}
			newCalls := oldCalls
			newChecks := checks
			initialCalls := int64(1)
			if mode == "site moved" {
				newCalls, newChecks = f.installWorker("applied ACK")
				initialCalls = 0
			}
			newItem, newCtx := f.claim(newID)
			if err := f.m.runPatchPolicyEnforcementWorkItem(newCtx, newItem); err == nil {
				t.Fatal("successor accepted unresolved original command")
			}
			if newCalls.Load() != initialCalls || oldCalls.Load() != 1 {
				t.Fatal("new desired state overtook unresolved predecessor")
			}
			f.assertProjection(newID, "pending")
			if mode == "activity missing" {
				if err := f.m.reconcilePatchPolicyEnforcementReceipts(f.owner, f.now); err != nil {
					t.Fatal(err)
				}
				if err := f.m.runPatchPolicyEnforcementWorkItem(newCtx, newItem); err == nil || newCalls.Load() != initialCalls {
					t.Fatal("missing historical activity released predecessor")
				}
				return
			}
			// A blocked unsent successor is terminal queue work, not an automatic
			// retry. Only a later existing policy run may create fresh intent.
			if err := f.m.completeWorkItem(newCtx, newItem, workStatusFailed, "predecessor pending"); err != nil {
				t.Fatal(err)
			}
			pending := f.state()
			f.commitReceipt(activityID, "Completed")
			if err := f.m.reconcilePatchPolicyEnforcementReceipts(f.owner, f.now); err != nil {
				t.Fatal(err)
			}
			if f.state() != pending {
				t.Fatal("old terminal receipt overwrote newer pending state")
			}
			f.assertProjection(newID, "pending")
			freshID, err := f.evaluate(scheduled + 120)
			if err != nil || freshID == newID || freshID == oldID {
				t.Fatalf("fresh cadence intent=%d error=%v", freshID, err)
			}
			freshItem, freshCtx := f.claim(freshID)
			_ = f.m.runPatchPolicyEnforcementWorkItem(freshCtx, freshItem)
			policyEnforcementSQLCheckRequest(t, newChecks)
			if newCalls.Load() != initialCalls+1 {
				t.Fatal("proven predecessor closure did not permit one fresh command")
			}
			if f.activity(freshID) == activityID {
				t.Fatal("fresh mode reused predecessor activity")
			}
		})
	}
}

func (f *policyEnforcementSQLFixture) useDescriptionFilter() {
	f.t.Helper()
	f.exec(`UPDATE engine.devices SET description='policy-sql-eligible' WHERE guid=$1`, f.guid)
	var filterID int64
	criteria := `{"groups":[{"conditions":[{"field":"description","operator":"contains","value":"policy-sql-eligible"}]}]}`
	if err := f.m.store.db.QueryRowContext(f.ctx, `INSERT INTO engine.device_filters(name,archived,criteria_mode,site_mode,basic_criteria_json,advanced_criteria_json,created_at,updated_at) VALUES($1,0,'advanced','global','{"criteria":[]}',$2,$3,$3) RETURNING id`, f.hostname+"-filter", criteria, f.now).Scan(&filterID); err != nil {
		f.t.Fatal(err)
	}
	f.exec(`UPDATE engine.patch_policies SET policy_type='device_filter' WHERE id=$1`, f.policyID)
	f.exec(`DELETE FROM engine.patch_policy_sites WHERE policy_id=$1`, f.policyID)
	f.exec(`INSERT INTO engine.patch_policy_targets(policy_id,target_type,filter_id,target_json,created_at) VALUES($1,'filter',$2,$3,$4)`, f.policyID, filterID, mustJSON(map[string]any{"filter_id": filterID}), f.now)
}

func TestSchedulerPatchPolicyEnforcementPostgresScope(t *testing.T) {
	for _, mode := range []string{"policy mode", "policy disabled", "Agent identity", "device GUID", "site moved", "dynamic filter no longer matches", "malformed selected filter shape", "unknown selected filter site mode", "static telemetry", "duplicate legacy state", "ambiguous route", "retained work changed"} {
		t.Run(mode, func(t *testing.T) {
			f := newPolicyEnforcementSQLFixture(t)
			if mode == "dynamic filter no longer matches" || mode == "malformed selected filter shape" || mode == "unknown selected filter site mode" {
				f.useDescriptionFilter()
			}
			workID, err := f.evaluate(schedulerFloorMinute(f.now))
			if err != nil {
				t.Fatal(err)
			}
			count, checks := f.installWorker("applied ACK")
			item, workCtx := f.claim(workID)
			switch mode {
			case "policy mode":
				f.exec(`UPDATE engine.patch_policies SET managed_update_mode=0 WHERE id=$1`, f.policyID)
			case "policy disabled":
				f.exec(`UPDATE engine.patch_policies SET enabled=0 WHERE id=$1`, f.policyID)
			case "Agent identity":
				f.exec(`UPDATE engine.devices SET agent_id='replacement-agent' WHERE guid=$1`, f.guid)
			case "device GUID":
				f.exec(`UPDATE engine.devices SET guid=$2 WHERE guid=$1`, f.guid, strings.ToUpper(newClusterUUID()))
			case "site moved":
				f.exec(`UPDATE engine.device_sites SET site_id=$2 WHERE device_hostname=$1`, f.hostname, f.newSite("scope-move"))
			case "dynamic filter no longer matches":
				f.exec(`UPDATE engine.devices SET description='no-match' WHERE guid=$1`, f.guid)
			case "malformed selected filter shape":
				f.exec(`UPDATE engine.device_filters SET advanced_criteria_json='[]' WHERE name=$1`, f.hostname+"-filter")
			case "unknown selected filter site mode":
				f.exec(`UPDATE engine.device_filters SET site_mode='unsupported' WHERE name=$1`, f.hostname+"-filter")
			case "static telemetry":
				f.exec(`UPDATE engine.devices SET last_seen=$2,memory='{"total":12345}',description='unrelated telemetry' WHERE guid=$1`, f.guid, f.now+10)
			case "duplicate legacy state":
				f.exec(`INSERT INTO engine.patch_policy_device_state(device_guid,hostname,effective_policy_id,exclusion_mode,enforcement_mode,enforcement_status,drift_detected,last_evaluated_at,last_enforced_at,metadata_json) SELECT device_guid,hostname,effective_policy_id,exclusion_mode,enforcement_mode,enforcement_status,drift_detected,last_evaluated_at,last_enforced_at,metadata_json FROM engine.patch_policy_device_state WHERE device_guid=$1`, f.guid)
			case "ambiguous route":
				f.exec(`INSERT INTO engine.job_scheduler_worker_routes(worker_guid,site_id,container_name,route_name,route_path_prefix,route_file_path,upstream_scheme,upstream_host,upstream_port,status,generation,metadata_json,created_at,updated_at) SELECT $2,site_id,container_name,route_name,route_path_prefix,route_file_path,upstream_scheme,upstream_host,upstream_port,status,generation,metadata_json,created_at,updated_at FROM engine.job_scheduler_worker_routes WHERE site_id=$1`, f.siteID, newClusterUUID())
			case "retained work changed":
				f.exec(`UPDATE engine.job_scheduler_work_items SET payload_json=jsonb_set(payload_json::jsonb,'{policy_enforcement,request_id}','"substituted"'::jsonb)::text WHERE id=$1`, workID)
			}
			before := f.state()
			err = f.m.runPatchPolicyEnforcementWorkItem(workCtx, item)
			if mode == "static telemetry" {
				policyEnforcementSQLCheckRequest(t, checks)
				if count.Load() != 1 {
					t.Fatal("unrelated static-policy telemetry invalidated original intent", err)
				}
			} else {
				if err == nil || count.Load() != 0 || f.state() != before {
					t.Fatalf("changed policy authority reached effect count=%d error=%v", count.Load(), err)
				}
			}
			if err := f.assertPoolsReleased(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, boundary := range []string{"late policy drift while acquiring device custody", "late policy drift while writing original activity"} {
		t.Run(boundary, func(t *testing.T) {
			f := newPolicyEnforcementSQLFixture(t)
			workID, err := f.evaluate(schedulerFloorMinute(f.now))
			if err != nil {
				t.Fatal(err)
			}
			count, _ := f.installWorker("applied ACK")
			item, workCtx := f.claim(workID)
			// Independent mutation and lock observation occur while dispatch waits.
			f.m.store.db.SetMaxOpenConns(5)
			f.m.ownershipPool().SetMaxOpenConns(5)
			before := f.state()
			query, args := `SELECT guid FROM engine.devices WHERE guid=$1 FOR UPDATE`, []any{f.guid}
			if boundary == "late policy drift while writing original activity" {
				query, args = `LOCK TABLE engine.activity_history IN SHARE MODE`, nil
			}
			blocker, pid := policyEnforcementSQLBlock(t, f, query, args...)
			ctx, cancel := context.WithCancel(workCtx)
			finished := make(chan error, 1)
			joined := make(chan struct{})
			go func() { defer close(joined); finished <- f.m.runPatchPolicyEnforcementWorkItem(ctx, item) }()
			defer func() { cancel(); _ = blocker.Rollback(); <-joined }()
			policyEnforcementSQLWaitBlocked(t, f, pid, finished)
			f.exec(`UPDATE engine.patch_policies SET managed_update_mode=0 WHERE id=$1`, f.policyID)
			if err := blocker.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := <-finished; err == nil || count.Load() != 0 || f.state() != before {
				t.Fatalf("late scope mutation escaped final SQL fence: sends=%d error=%v", count.Load(), err)
			}
		})
	}
}

func testPolicyEnforcementSQLConcurrentIntent(t *testing.T) {
	f := newPolicyEnforcementSQLFixture(t)
	// Blocker, two evaluators, and native lock observer need separate sessions.
	f.m.store.db.SetMaxOpenConns(5)
	blocker, pid := policyEnforcementSQLBlock(t, f, `SELECT id FROM engine.patch_policies WHERE id=$1 FOR UPDATE`, f.policyID)
	ctx, cancel := context.WithCancel(f.owner)
	defer cancel()
	finished := make(chan error, 2)
	joined := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			defer func() { joined <- struct{}{} }()
			_, err := f.m.store.evaluatePatchPoliciesAt(ctx, operatorProfile{Username: "policy-sql", Role: "Admin"}, f.policyID, schedulerFloorMinute(f.now), f.now, true)
			finished <- err
		}()
	}
	defer func() {
		cancel()
		_ = blocker.Rollback()
		<-joined
		<-joined
	}()
	policyEnforcementSQLWaitBlockedCount(t, f, pid, finished, 2)
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	first, second := <-finished, <-finished
	if first != nil && second != nil {
		t.Fatalf("both serialized evaluators failed: %v; %v", first, second)
	}
	workID, err := f.evaluate(schedulerFloorMinute(f.now))
	if err != nil {
		t.Fatal("same original run did not recover after contention", err)
	}
	f.assertProjection(workID, "pending")
	var works, runs, states int
	if err := f.m.store.db.QueryRowContext(f.ctx, `SELECT (SELECT COUNT(*) FROM engine.job_scheduler_work_items WHERE payload_json::jsonb->'policy_enforcement'->>'device_guid'=$1),(SELECT COUNT(*) FROM engine.patch_policy_runs WHERE policy_id=$2),(SELECT COUNT(*) FROM engine.patch_policy_device_state WHERE device_guid=$1)`, f.guid, f.policyID).Scan(&works, &runs, &states); err != nil {
		t.Fatal(err)
	}
	if works != 1 || runs != 1 || states != 1 {
		t.Fatalf("concurrent evaluator split original identity work=%d runs=%d state=%d", works, runs, states)
	}
}
