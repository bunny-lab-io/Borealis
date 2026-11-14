package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func schedulerOwnershipFixture(t *testing.T) (*goSchedulerManager, context.Context, func(string, string) int64) {
	t.Helper()
	dsn := os.Getenv("BOREALIS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BOREALIS_TEST_DATABASE_URL not configured")
	}
	store, ownershipDB, closeStore, err := openSchedulerStores(gatewayConfig{
		DatabaseURL: dsn, DBSSLMode: "disable", DBConnectTimeout: 2 * time.Second,
		DBMaxOpenConns: 5, DBMaxIdleConns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeStore)
	db := store.db
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	m := &goSchedulerManager{store: store, ownershipDB: ownershipDB, leaseTransport: &postgresLeaseTransport{dsn: dsn}}
	if err := m.store.ensureClusterSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.ensureTables(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM engine.cluster_application_leases WHERE name='scheduler-leader'`); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, id := range ids {
			_, _ = db.ExecContext(cleanupCtx, `DELETE FROM engine.job_scheduler_work_items WHERE id=$1`, id)
		}
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM engine.cluster_application_leases WHERE name='scheduler-leader'`)
	})
	insert := func(kind, payload string) int64 {
		t.Helper()
		var id int64
		now := time.Now().Unix()
		if err := db.QueryRowContext(ctx, `INSERT INTO engine.job_scheduler_work_items
			(kind,lane,payload_json,status,attempt_count,priority,available_at,created_at,updated_at)
			VALUES($1,'scheduled_job',$2,'queued',0,1,$3,$3,$3) RETURNING id`, kind, payload, now).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		return id
	}
	return m, ctx, insert
}

func schedulerTestOwner(t *testing.T, m *goSchedulerManager, ctx context.Context, holder string) context.Context {
	t.Helper()
	owned, err := m.acquireSchedulerLeadership(ctx, holder, time.Now().Unix())
	if err != nil || !owned {
		t.Fatalf("acquire scheduler owner=%s owned=%v error=%v", holder, owned, err)
	}
	return context.WithValue(ctx, schedulerOwnerContextKey{}, holder)
}

func TestSchedulerPostgresOwnershipFences(t *testing.T) {
	m, ctx, insert := schedulerOwnershipFixture(t)
	ownerA := schedulerTestOwner(t, m, ctx, "scheduler-a/"+newClusterUUID())
	id := insert(schedulerKindScheduledRun, `{}`)
	first, err := m.claimNextKindWorkItem(ownerA, []string{schedulerKindScheduledRun})
	if err != nil || first == nil || first.ID != id || first.AttemptCount != 1 {
		t.Fatalf("initial claim=%+v err=%v", first, err)
	}
	var expires int64
	if err := m.store.db.QueryRowContext(ctx, `SELECT lease_expires_at FROM engine.job_scheduler_work_items WHERE id=$1`, id).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if remaining := expires - time.Now().Unix(); remaining > schedulerWorkLeaseSeconds || remaining < 50 {
		t.Fatalf("work ownership followed script duration: remaining=%d", remaining)
	}
	if owned, err := m.heartbeatWorkItem(ownerA, *first); err != nil || !owned {
		t.Fatalf("current heartbeat owned=%v err=%v", owned, err)
	}
	if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.cluster_application_leases SET expires_at=$1 WHERE name='scheduler-leader'`, time.Now().Unix()-1); err != nil {
		t.Fatal(err)
	}
	ownerB := schedulerTestOwner(t, m, ctx, "scheduler-b/"+newClusterUUID())
	if err := m.completeWorkItem(ownerA, *first, workStatusSucceeded, "late response"); !errors.Is(err, errSchedulerOwnershipLost) {
		t.Fatalf("former leader completion=%v", err)
	}
	if owned, err := m.heartbeatWorkItem(ownerA, *first); err != nil || owned {
		t.Fatalf("former leader renewed claim: owned=%v err=%v", owned, err)
	}
	staleCtx := context.WithValue(ownerA, schedulerWorkContextKey{}, *first)
	if err := m.beginSchedulerDispatch(staleCtx, "late-dispatch"); !errors.Is(err, errSchedulerOwnershipLost) {
		t.Fatalf("former owner dispatch admission=%v", err)
	}
	for name, write := range map[string]func() error{
		"run start":        func() error { return m.markScheduledRunRunning(staleCtx, 999999) },
		"run failure":      func() error { return m.failScheduledRun(staleCtx, 999999, "late failure") },
		"activity failure": func() error { return m.markScheduledActivityFailed(staleCtx, 999999, "late failure") },
		"patch result": func() error {
			return m.updateScheduledPatchRunStatus(staleCtx, 999999, 0, scheduledStatusFailed, "", "", "late result")
		},
		"maintenance result": func() error {
			return m.updateAgentMaintenanceRunStatus(staleCtx, 999999, scheduledStatusFailed, "", "late result")
		},
	} {
		if err := write(); !errors.Is(err, errSchedulerOwnershipLost) {
			t.Fatalf("former owner %s write=%v", name, err)
		}
	}
	if err := m.expireStaleLeases(ownerA); !errors.Is(err, errSchedulerOwnershipLost) {
		t.Fatalf("former owner recovery write=%v", err)
	}
	if err := m.expireStaleLeases(ownerB); err != nil {
		t.Fatal(err)
	}
	second, err := m.claimNextKindWorkItem(ownerB, []string{schedulerKindScheduledRun})
	if err != nil || second == nil || second.ID != id || second.AttemptCount != 2 || second.LeaseOwner == first.LeaseOwner {
		t.Fatalf("takeover claim=%+v err=%v", second, err)
	}
	staleGeneration := *second
	staleGeneration.AttemptCount--
	if err := m.completeWorkItem(ownerB, staleGeneration, workStatusSucceeded, "old generation"); !errors.Is(err, errSchedulerOwnershipLost) {
		t.Fatalf("old generation completion=%v", err)
	}
	if owned, err := m.heartbeatWorkItem(ownerB, staleGeneration); err != nil || owned {
		t.Fatalf("old generation heartbeat owned=%v err=%v", owned, err)
	}
	runningCtx, cancelRunning := context.WithCancel(ownerB)
	entered := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- m.runClaimedWork(runningCtx, *second, func(workCtx context.Context, _ schedulerWorkItem) error {
			close(entered)
			<-workCtx.Done()
			return workCtx.Err()
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("owned work did not start")
	}
	cancelRunning()
	select {
	case err := <-finished:
		if !errors.Is(err, errSchedulerOwnershipLost) {
			t.Fatalf("canceled owner result=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("work outlived owner cancellation")
	}
	if err := m.completeWorkItem(ownerB, *second, workStatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.completeWorkItem(ownerA, *first, workStatusFailed, "late failure"); !errors.Is(err, errSchedulerOwnershipLost) {
		t.Fatalf("late failure replaced terminal result: %v", err)
	}
}

func TestSchedulerPostgresDispatchRecovery(t *testing.T) {
	m, ctx, insert := schedulerOwnershipFixture(t)
	owner := schedulerTestOwner(t, m, ctx, "scheduler-ledger/"+newClusterUUID())
	for _, acknowledge := range []bool{false, true} {
		t.Run(fmt.Sprintf("acknowledged=%v", acknowledge), func(t *testing.T) {
			id := insert(schedulerKindServiceAction, `{}`)
			item, err := m.claimNextKindWorkItem(owner, []string{schedulerKindServiceAction})
			if err != nil || item == nil || item.ID != id {
				t.Fatalf("claim=%+v err=%v", item, err)
			}
			workCtx := context.WithValue(owner, schedulerWorkContextKey{}, *item)
			if err := m.beginSchedulerDispatch(workCtx, "retained-execution"); err != nil {
				t.Fatal(err)
			}
			if acknowledge {
				if err := m.acknowledgeSchedulerDispatch(workCtx, "retained-execution"); err != nil {
					t.Fatal(err)
				}
			}
			// Owner death after send, with or without its acknowledgement commit.
			if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET lease_expires_at=$1 WHERE id=$2`, time.Now().Unix()-1, id); err != nil {
				t.Fatal(err)
			}
			if err := m.expireStaleLeases(owner); err != nil {
				t.Fatal(err)
			}
			reclaimed, err := m.claimNextKindWorkItem(owner, []string{schedulerKindServiceAction})
			if err != nil || reclaimed == nil || reclaimed.AttemptCount != 2 {
				t.Fatalf("reclaim=%+v err=%v", reclaimed, err)
			}
			newCtx := context.WithValue(owner, schedulerWorkContextKey{}, *reclaimed)
			skipped, resumeErr := m.resumeSchedulerExecution(newCtx)
			if acknowledge && (resumeErr != nil || !skipped) {
				t.Fatalf("acknowledged execution was not reused: skipped=%v err=%v", skipped, resumeErr)
			}
			if !acknowledge && !errors.Is(resumeErr, errSchedulerOutcomeUnknown) {
				t.Fatalf("unacknowledged dispatch was replayable: %v", resumeErr)
			}
			if err := m.acknowledgeSchedulerDispatch(workCtx, "late-ack"); !errors.Is(err, errSchedulerOwnershipLost) {
				t.Fatalf("stale acknowledgement=%v", err)
			}
			if err := m.completeWorkItem(newCtx, *reclaimed, workStatusSucceeded, ""); err != nil {
				t.Fatal(err)
			}
			var status, detail string
			if err := m.store.db.QueryRowContext(ctx, `SELECT status,COALESCE(error,'') FROM engine.job_scheduler_work_items WHERE id=$1`, id).Scan(&status, &detail); err != nil {
				t.Fatal(err)
			}
			if !acknowledge && (status != workStatusFailed || !strings.Contains(detail, "execution outcome unknown")) {
				t.Fatalf("uncertainty hidden: status=%s detail=%s", status, detail)
			}
			if acknowledge && status != workStatusSucceeded {
				t.Fatalf("acknowledged dispatch status=%s", status)
			}
		})
	}
	t.Run("partial component replay", func(t *testing.T) {
		id := insert(schedulerKindServiceAction, `{}`)
		item, err := m.claimNextKindWorkItem(owner, []string{schedulerKindServiceAction})
		if err != nil || item == nil || item.ID != id {
			t.Fatalf("component claim=%+v err=%v", item, err)
		}
		oldCtx := context.WithValue(owner, schedulerWorkContextKey{}, *item)
		firstCtx := withSchedulerExecution(oldCtx, "script:0")
		if err := m.beginSchedulerDispatch(firstCtx, "activity:first"); err != nil {
			t.Fatal(err)
		}
		if err := m.acknowledgeSchedulerDispatch(firstCtx, "activity:first"); err != nil {
			t.Fatal(err)
		}
		if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET lease_expires_at=$1 WHERE id=$2`, time.Now().Unix()-1, id); err != nil {
			t.Fatal(err)
		}
		if err := m.expireStaleLeases(owner); err != nil {
			t.Fatal(err)
		}
		reclaimed, err := m.claimNextKindWorkItem(owner, []string{schedulerKindServiceAction})
		if err != nil || reclaimed == nil || reclaimed.ID != id {
			t.Fatalf("component reclaim=%+v err=%v", reclaimed, err)
		}
		newCtx := context.WithValue(owner, schedulerWorkContextKey{}, *reclaimed)
		if skipped, err := m.resumeSchedulerExecution(withSchedulerExecution(newCtx, "script:0")); err != nil || !skipped {
			t.Fatalf("acknowledged component replayed: skipped=%v err=%v", skipped, err)
		}
		secondCtx := withSchedulerExecution(newCtx, "script:1")
		if skipped, err := m.resumeSchedulerExecution(secondCtx); err != nil || skipped {
			t.Fatalf("unsent component did not resume: skipped=%v err=%v", skipped, err)
		}
		if err := m.beginSchedulerDispatch(secondCtx, "activity:second"); err != nil {
			t.Fatal(err)
		}
		if err := m.beginSchedulerDispatch(secondCtx, "duplicate"); !errors.Is(err, errSchedulerOutcomeUnknown) {
			t.Fatalf("same generation admitted duplicate send: %v", err)
		}
		if err := m.acknowledgeSchedulerDispatch(secondCtx, "activity:second"); err != nil {
			t.Fatal(err)
		}
		if err := m.completeWorkItem(newCtx, *reclaimed, workStatusSucceeded, ""); err != nil {
			t.Fatal(err)
		}
	})
	legacy := insert(schedulerKindScheduledRun, `{}`)
	if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET status='running',lease_owner='job-scheduler',lease_expires_at=$1 WHERE id=$2`, time.Now().Unix()+7200, legacy); err != nil {
		t.Fatal(err)
	}
	if err := m.expireStaleLeases(owner); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := m.store.db.QueryRowContext(ctx, `SELECT payload_json::jsonb->>'recovery_state' FROM engine.job_scheduler_work_items WHERE id=$1`, legacy).Scan(&state); err != nil || state != "outcome_unknown" {
		t.Fatalf("legacy claim replay state=%s err=%v", state, err)
	}
}

func TestSchedulerPostgresWorkflowDispatchRetainsIdentity(t *testing.T) {
	t.Run("outgoing identity", testSchedulerPostgresWorkflowDispatchIdentity)
	for _, status := range []string{workflowStatusPending, workflowStatusRunning, workflowStatusSuccess, workflowStatusSkipped, workflowStatusWarning, workflowStatusFailed, workflowStatusTimedOut} {
		t.Run("recovery/"+status, func(t *testing.T) {
			testSchedulerPostgresWorkflowDispatchRecovery(t, status, "matching", 1, false)
		})
	}
	t.Run("recovery/acknowledged Pending", func(t *testing.T) {
		testSchedulerPostgresWorkflowDispatchRecovery(t, workflowStatusPending, "matching", 1, true)
	})
	for _, tc := range []struct {
		name     string
		identity string
		rows     int
	}{
		{"no run", "matching", 0},
		{"missing identity", "", 1},
		{"unrelated identity", "unrelated-execution", 1},
		{"duplicate identity", "matching", 2},
	} {
		t.Run("recovery/"+tc.name, func(t *testing.T) {
			testSchedulerPostgresWorkflowDispatchRecovery(t, workflowStatusFailed, tc.identity, tc.rows, false)
		})
	}
}

func testSchedulerPostgresWorkflowDispatchIdentity(t *testing.T) {
	m, ctx, insert := schedulerOwnershipFixture(t)
	owner := schedulerTestOwner(t, m, ctx, "scheduler-workflow/"+newClusterUUID())
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/job-scheduler/workflow/start" || r.Header.Get(internalTokenHeader) != goInternalToken([]byte("test-secret")) {
			t.Error("unexpected workflow dispatch boundary")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"run_id": 77})
	}))
	defer server.Close()
	m.apiBase, m.secret, m.httpClient = server.URL, []byte("test-secret"), server.Client()
	id := insert(schedulerKindScheduledWorkflowRun, `{"workflow_component":{"assembly_guid":"wf-123","id":"node-1"},"workflow_site_scope":{"site_id":7}}`)
	item, err := m.claimNextKindWorkItem(owner, []string{schedulerKindScheduledWorkflowRun})
	if err != nil || item == nil || item.ID != id {
		t.Fatalf("workflow claim=%+v err=%v", item, err)
	}
	if err := m.runClaimedWork(owner, *item, m.runGlobalWorkItem); err != nil {
		t.Fatal(err)
	}
	metadata := mapStringAny(received["source_metadata"])
	if received["workflow_guid"] != "wf-123" || metadata["scheduler_execution_id"] != fmt.Sprintf("work:%d:work", id) {
		t.Fatalf("workflow lost durable identity: %#v", received)
	}
}

func testSchedulerPostgresWorkflowDispatchRecovery(t *testing.T, status, identity string, rows int, acknowledged bool) {
	t.Helper()
	m, ctx, insert := schedulerOwnershipFixture(t)
	owner := schedulerTestOwner(t, m, ctx, "scheduler-workflow-recovery/"+newClusterUUID())
	var jobID, scheduledRunID int64
	if err := m.store.db.QueryRowContext(ctx, `INSERT INTO engine.scheduled_jobs
		(name,components_json,targets_json,schedule_type,execution_context)
		VALUES('workflow-recovery-test','[]','[]','immediate','system') RETURNING id`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = m.store.db.ExecContext(cleanupCtx, `DELETE FROM engine.workflow_runs WHERE scheduled_job_id=$1`, jobID)
		_, _ = m.store.db.ExecContext(cleanupCtx, `DELETE FROM engine.scheduled_jobs WHERE id=$1`, jobID)
	})
	if err := m.store.db.QueryRowContext(ctx, `INSERT INTO engine.scheduled_job_runs
		(job_id,status,started_ts) VALUES($1,'Running',$2) RETURNING id`, jobID, time.Now().Unix()).Scan(&scheduledRunID); err != nil {
		t.Fatal(err)
	}
	id := insert(schedulerKindScheduledWorkflowRun, `{"workflow_component":{"assembly_guid":"wf-recovery"}}`)
	if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET run_id=$1,job_id=$2 WHERE id=$3`, scheduledRunID, jobID, id); err != nil {
		t.Fatal(err)
	}
	item, err := m.claimNextKindWorkItem(owner, []string{schedulerKindScheduledWorkflowRun})
	if err != nil || item == nil || item.ID != id {
		t.Fatalf("workflow recovery claim=%+v err=%v", item, err)
	}
	oldCtx := context.WithValue(owner, schedulerWorkContextKey{}, *item)
	if err := m.beginSchedulerDispatch(oldCtx, "response-not-received"); err != nil {
		t.Fatal(err)
	}
	// Pending can persist before executor launch. A skipped run or execution
	// progress can also precede acknowledgement. Retain the real mirrored result.
	metadata := map[string]any{"scheduled_job_id": jobID, "scheduled_job_run_id": scheduledRunID}
	if identity == "matching" {
		metadata["scheduler_execution_id"] = fmt.Sprintf("work:%d:work", id)
	} else if identity != "" {
		metadata["scheduler_execution_id"] = identity
	}
	var workflowRunID int64
	for i := 0; i < rows; i++ {
		rowStatus := status
		if i > 0 {
			// A duplicate Pending row must not disappear behind a status filter.
			rowStatus = workflowStatusPending
		}
		err := m.store.withWorkflowConn(ctx, func(conn *sql.Conn) error {
			var err error
			workflowRunID, err = workflowInsertRun(ctx, conn, "wf-recovery", "Recovery test", "scheduled_job", metadata, map[string]any{}, rowStatus, "scheduler", time.Now().Unix(), "retained skip reason")
			if err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, `UPDATE engine.workflow_runs SET error='retained workflow diagnostic' WHERE id=$1`, workflowRunID); err != nil {
				return err
			}
			if rowStatus == workflowStatusPending {
				return nil
			}
			return workflowMirrorScheduledRun(ctx, conn, workflowRunID, metadata, rowStatus, "retained workflow diagnostic")
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if acknowledged {
		if err := m.acknowledgeSchedulerDispatch(oldCtx, fmt.Sprint(workflowRunID)); err != nil {
			t.Fatal(err)
		}
	}
	// Capture complete result rows, including timestamps and diagnostic fields.
	snapshot := func() string {
		t.Helper()
		var result string
		if err := m.store.db.QueryRowContext(ctx, `SELECT jsonb_build_object('scheduled',to_jsonb(r),'workflow',to_jsonb(w))::text
			FROM engine.scheduled_job_runs r LEFT JOIN engine.workflow_runs w ON w.id=$2 WHERE r.id=$1`, scheduledRunID, workflowRunID).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	// Lose ownership after API persistence, with or without acknowledgement.
	if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET lease_expires_at=$1 WHERE id=$2`, time.Now().Unix()-1, id); err != nil {
		t.Fatal(err)
	}
	if err := m.expireStaleLeases(owner); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := m.claimNextKindWorkItem(owner, []string{schedulerKindScheduledWorkflowRun})
	if err != nil || reclaimed == nil || reclaimed.ID != id || reclaimed.AttemptCount != 2 {
		t.Fatalf("workflow reclaim=%+v err=%v", reclaimed, err)
	}
	newCtx := context.WithValue(owner, schedulerWorkContextKey{}, *reclaimed)
	accepted := acknowledged || (identity == "matching" && rows == 1 && status != workflowStatusPending)
	skipped, resumeErr := m.resumeSchedulerExecution(newCtx)
	if accepted {
		if resumeErr != nil || !skipped {
			t.Fatalf("persisted %s workflow was not accepted: skipped=%v err=%v", status, skipped, resumeErr)
		}
		if err := m.runGlobalWorkItem(newCtx, *reclaimed); err != nil {
			t.Fatalf("accepted workflow attempted dispatch again: %v", err)
		}
	} else if skipped || !errors.Is(resumeErr, errSchedulerOutcomeUnknown) {
		t.Fatalf("ambiguous workflow accepted: skipped=%v err=%v", skipped, resumeErr)
	}
	if err := m.beginSchedulerDispatch(newCtx, "duplicate"); !errors.Is(err, errSchedulerOutcomeUnknown) {
		t.Fatalf("workflow replay admitted: %v", err)
	}
	if err := m.acknowledgeSchedulerDispatch(oldCtx, "late-ack"); !errors.Is(err, errSchedulerOwnershipLost) {
		t.Fatalf("former workflow claim acknowledged: %v", err)
	}
	if err := m.completeWorkItem(newCtx, *reclaimed, workStatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	var workStatus, executionState, resultID string
	if err := m.store.db.QueryRowContext(ctx, `SELECT status,payload_json::jsonb->'executions'->'work'->>'state',payload_json::jsonb->'executions'->'work'->>'result_id'
		FROM engine.job_scheduler_work_items WHERE id=$1`, id).Scan(&workStatus, &executionState, &resultID); err != nil {
		t.Fatal(err)
	}
	if accepted {
		if workStatus != workStatusSucceeded || executionState != "acknowledged" || resultID != fmt.Sprint(workflowRunID) {
			t.Fatalf("workflow identity not retained: status=%s execution=%s result=%s", workStatus, executionState, resultID)
		}
		if after := snapshot(); after != before {
			t.Fatalf("workflow recovery changed saved results: before=%s after=%s", before, after)
		}
	} else if workStatus != workStatusFailed || executionState != "dispatching" || resultID != "response-not-received" {
		t.Fatalf("ambiguous workflow lost uncertainty: status=%s execution=%s result=%s", workStatus, executionState, resultID)
	}
	if !accepted && status == workflowStatusPending {
		var runStatus, runError, workflowStatus, workflowError string
		if err := m.store.db.QueryRowContext(ctx, `SELECT r.status,r.error,w.status,w.error
			FROM engine.scheduled_job_runs r,engine.workflow_runs w WHERE r.id=$1 AND w.id=$2`, scheduledRunID, workflowRunID).
			Scan(&runStatus, &runError, &workflowStatus, &workflowError); err != nil {
			t.Fatal(err)
		}
		if runStatus != "Running" || !strings.Contains(runError, "execution outcome unknown") || workflowStatus != workflowStatusPending || workflowError != "retained workflow diagnostic" {
			t.Fatalf("pending launch uncertainty hidden or result changed: scheduled=%s/%s workflow=%s/%s", runStatus, runError, workflowStatus, workflowError)
		}
	}
}

func TestSchedulerLeadershipPodIncarnation(t *testing.T) {
	for holder, want := range map[string]string{
		"job-scheduler-abc":                     "job-scheduler-abc",
		"job-scheduler-abc/" + newClusterUUID(): "job-scheduler-abc",
		"job-scheduler-abc/not-a-generation":    "",
	} {
		if got := schedulerLeadershipPod(holder); got != want {
			t.Fatalf("holder=%q pod=%q want=%q", holder, got, want)
		}
	}
}

func TestSchedulerPostgresUnknownOutcomePreservesExecution(t *testing.T) {
	for _, status := range []string{"Success", "Failed"} {
		t.Run(status, func(t *testing.T) {
			testSchedulerPostgresUnknownOutcomePreservesExecution(t, status)
		})
	}
}

func testSchedulerPostgresUnknownOutcomePreservesExecution(t *testing.T, terminalStatus string) {
	m, ctx, insert := schedulerOwnershipFixture(t)
	owner := schedulerTestOwner(t, m, ctx, "scheduler-results/"+newClusterUUID())
	var jobID, runID, activityID int64
	if err := m.store.db.QueryRowContext(ctx, `INSERT INTO engine.scheduled_jobs
		(name,components_json,targets_json,schedule_type,execution_context)
		VALUES('ownership-result-test','[]','[]','immediate','system') RETURNING id`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = m.store.db.ExecContext(cleanupCtx, `DELETE FROM engine.scheduled_jobs WHERE id=$1`, jobID)
		_, _ = m.store.db.ExecContext(cleanupCtx, `DELETE FROM engine.activity_history WHERE id=$1`, activityID)
	})
	if err := m.store.db.QueryRowContext(ctx, `INSERT INTO engine.scheduled_job_runs
		(job_id,status,started_ts) VALUES($1,'Running',$2) RETURNING id`, jobID, time.Now().Unix()).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	id := insert(schedulerKindScheduledRun, `{}`)
	if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET run_id=$1 WHERE id=$2`, runID, id); err != nil {
		t.Fatal(err)
	}
	item, err := m.claimNextKindWorkItem(owner, []string{schedulerKindScheduledRun})
	if err != nil || item == nil || item.ID != id {
		t.Fatalf("result claim=%+v err=%v", item, err)
	}
	workCtx := context.WithValue(owner, schedulerWorkContextKey{}, *item)
	activityID, err = m.insertScheduledActivity(workCtx, scheduledActivityInsert{
		RunID: runID, Hostname: "ownership-result-host", Status: "Running",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.beginSchedulerDispatch(workCtx, "remote-result"); err != nil {
		t.Fatal(err)
	}
	assertStillRunning := func() {
		t.Helper()
		var runStatus, activityStatus, runError, activityError string
		var runFinished, activityFinished sql.NullInt64
		if err := m.store.db.QueryRowContext(ctx, `SELECT r.status,r.finished_ts,COALESCE(r.error,''),a.status,a.finished_at,COALESCE(a.stderr,'')
			FROM engine.scheduled_job_runs r,engine.activity_history a WHERE r.id=$1 AND a.id=$2`, runID, activityID).
			Scan(&runStatus, &runFinished, &runError, &activityStatus, &activityFinished, &activityError); err != nil {
			t.Fatal(err)
		}
		if runStatus != "Running" || activityStatus != "Running" || runFinished.Valid || activityFinished.Valid {
			t.Fatalf("unknown result ended remote execution: run=%s/%v activity=%s/%v", runStatus, runFinished, activityStatus, activityFinished)
		}
		if !strings.Contains(runError, "execution outcome unknown") || !strings.Contains(activityError, "execution outcome unknown") {
			t.Fatalf("unknown result hidden: run=%q activity=%q", runError, activityError)
		}
	}
	if err := m.markScheduledActivityFailed(workCtx, activityID, "response lost"); err != nil {
		t.Fatal(err)
	}
	if err := m.failScheduledRun(workCtx, runID, "response lost"); err != nil {
		t.Fatal(err)
	}
	assertStillRunning()
	if err := m.updateScheduledPatchRunStatus(workCtx, runID, activityID, scheduledStatusFailed, "", "response lost", "response lost"); err != nil {
		t.Fatal(err)
	}
	assertStillRunning()
	if err := m.completeWorkItem(workCtx, *item, workStatusFailed, "response lost"); err != nil {
		t.Fatal(err)
	}
	assertStillRunning()
	// A delivered result can reconcile an unacknowledged send, but a matching
	// activity ID without its retained execution identity is insufficient.
	if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET status='queued',lease_owner=NULL,lease_expires_at=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := m.claimNextKindWorkItem(owner, []string{schedulerKindScheduledRun})
	if err != nil || reclaimed == nil {
		t.Fatalf("result reconciliation claim=%+v err=%v", reclaimed, err)
	}
	newCtx := context.WithValue(owner, schedulerWorkContextKey{}, *reclaimed)
	if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.activity_history SET status=$2,metadata_json='{}',stderr='definitive result' WHERE id=$1`, activityID, terminalStatus); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.scheduled_job_runs SET status=$2,error='definitive result' WHERE id=$1`, runID, terminalStatus); err != nil {
		t.Fatal(err)
	}
	if _, err := m.resumeSchedulerExecution(newCtx); !errors.Is(err, errSchedulerOutcomeUnknown) {
		t.Fatalf("unrelated terminal result settled execution: %v", err)
	}
	if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.activity_history SET metadata_json=jsonb_build_object('scheduler_execution_id',$1::text)::text WHERE id=$2`, fmt.Sprintf("work:%d:work", id), activityID); err != nil {
		t.Fatal(err)
	}
	if skipped, err := m.resumeSchedulerExecution(newCtx); err != nil || !skipped {
		t.Fatalf("retained execution result did not reconcile: skip=%v err=%v", skipped, err)
	}
	if err := m.completeWorkItem(newCtx, *reclaimed, workStatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	var runStatus, activityStatus, runError, activityError string
	if err := m.store.db.QueryRowContext(ctx, `SELECT r.status,a.status,r.error,a.stderr
		FROM engine.scheduled_job_runs r,engine.activity_history a WHERE r.id=$1 AND a.id=$2`, runID, activityID).
		Scan(&runStatus, &activityStatus, &runError, &activityError); err != nil {
		t.Fatal(err)
	}
	if runStatus != terminalStatus || activityStatus != terminalStatus || runError != "definitive result" || activityError != "definitive result" {
		t.Fatalf("reconciliation changed definitive result: run=%s/%s activity=%s/%s", runStatus, runError, activityStatus, activityError)
	}
}
