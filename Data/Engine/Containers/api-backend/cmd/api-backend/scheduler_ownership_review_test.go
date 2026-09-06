package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func schedulerReviewJob(t *testing.T, m *goSchedulerManager, ctx context.Context) int64 {
	t.Helper()
	var id int64
	if err := m.store.db.QueryRowContext(ctx, `INSERT INTO engine.scheduled_jobs
		(name,components_json,targets_json,schedule_type,execution_context)
		VALUES('ownership-review-test','[]','[]','immediate','system') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = m.store.db.ExecContext(cleanupCtx, `DELETE FROM engine.scheduled_jobs WHERE id=$1`, id)
	})
	return id
}

func TestSchedulerPostgresPatchResultAcknowledgementAtomic(t *testing.T) {
	testSchedulerResultAcknowledgementAtomic(t, schedulerKindPatchInstallRun, []string{scheduledStatusSuccess, scheduledStatusFailed})
}

func TestSchedulerPostgresMaintenanceAcknowledgementAtomic(t *testing.T) {
	testSchedulerResultAcknowledgementAtomic(t, schedulerKindAgentMaintenanceRun, []string{"Running", "Skipped"})
}

func testSchedulerResultAcknowledgementAtomic(t *testing.T, kind string, statuses []string) {
	t.Helper()
	for _, resultStatus := range statuses {
		t.Run(resultStatus, func(t *testing.T) {
			m, ctx, insert := schedulerOwnershipFixture(t)
			owner := schedulerTestOwner(t, m, ctx, "patch-results/"+newClusterUUID())
			jobID := schedulerReviewJob(t, m, ctx)
			var runID int64
			if err := m.store.db.QueryRowContext(ctx, `INSERT INTO engine.scheduled_job_runs
				(job_id,status,started_ts) VALUES($1,'Running',$2) RETURNING id`, jobID, time.Now().Unix()).Scan(&runID); err != nil {
				t.Fatal(err)
			}
			id := insert(kind, `{}`)
			if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET run_id=$1 WHERE id=$2`, runID, id); err != nil {
				t.Fatal(err)
			}
			item, err := m.claimNextKindWorkItem(owner, []string{kind})
			if err != nil || item == nil || item.ID != id {
				t.Fatalf("claim=%+v err=%v", item, err)
			}
			workCtx := context.WithValue(owner, schedulerWorkContextKey{}, *item)
			activityID, err := m.insertScheduledActivity(workCtx, scheduledActivityInsert{
				RunID: runID, Hostname: "patch-result-host", Status: scheduledStatusRunning,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = m.store.db.ExecContext(context.Background(), `DELETE FROM engine.activity_history WHERE id=$1`, activityID)
			})
			requestID := fmt.Sprintf("patch-job-%d-run-%d", jobID, runID)
			finish := func(resultCtx context.Context, status, stdout string) error {
				if kind == schedulerKindAgentMaintenanceRun {
					return m.finishAgentMaintenanceDispatch(resultCtx, requestID, runID, status, stdout)
				}
				return m.finishScheduledPatchDispatch(resultCtx, requestID, runID, activityID, status, stdout, "patch diagnostics", "")
			}
			runner := m.runPatchInstallWorkItem
			if kind == schedulerKindAgentMaintenanceRun {
				runner = m.runAgentMaintenanceWorkItem
			}
			if err := m.beginSchedulerDispatch(workCtx, requestID); err != nil {
				t.Fatal(err)
			}
			// Block the final activity write after acknowledgement and run update
			// have executed. Cancellation must roll back all three changes.
			blocker, err := m.store.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			if _, err := blocker.ExecContext(ctx, `SELECT id FROM engine.activity_history WHERE id=$1 FOR UPDATE`, activityID); err != nil {
				t.Fatal(err)
			}
			shortCtx, cancel := context.WithCancel(workCtx)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				finished <- finish(shortCtx, resultStatus, "terminal response\n")
			}()
			for {
				var waiting bool
				if err := m.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
					WHERE datname=current_database() AND wait_event_type='Lock'
					AND query LIKE '%UPDATE engine.activity_history%')`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-finished:
					t.Fatalf("result exited before final activity write: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			cancel()
			select {
			case err := <-finished:
				if err == nil {
					t.Fatal("blocked result write was acknowledged")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := blocker.Rollback(); err != nil {
				t.Fatal(err)
			}
			assertResult := func(wantState, wantStatus, wantOutput string) {
				t.Helper()
				var state, runStatus, activityStatus, output string
				var runFinished, activityFinished sql.NullInt64
				if err := m.store.db.QueryRowContext(ctx, `SELECT w.payload_json::jsonb->'executions'->'work'->>'state',
					r.status,r.finished_ts,a.status,a.finished_at,COALESCE(a.stdout,'')
					FROM engine.job_scheduler_work_items w,engine.scheduled_job_runs r,engine.activity_history a
					WHERE w.id=$1 AND r.id=$2 AND a.id=$3`, id, runID, activityID).
					Scan(&state, &runStatus, &runFinished, &activityStatus, &activityFinished, &output); err != nil {
					t.Fatal(err)
				}
				terminal := scheduledTerminalStatus(wantStatus)
				if state != wantState || runStatus != wantStatus || activityStatus != wantStatus || output != wantOutput ||
					runFinished.Valid != terminal || activityFinished.Valid != terminal {
					t.Fatalf("result state=%s run=%s/%v activity=%s/%v output=%q", state, runStatus, runFinished, activityStatus, activityFinished, output)
				}
			}
			assertResult("dispatching", scheduledStatusRunning, "")
			if err := finish(workCtx, resultStatus, "terminal response\n"); err != nil {
				t.Fatal(err)
			}
			assertResult("acknowledged", resultStatus, "terminal response\n")
			// Simulate death after the result transaction but before queue completion.
			if _, err := m.store.db.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET lease_expires_at=$1 WHERE id=$2`, time.Now().Unix()-1, id); err != nil {
				t.Fatal(err)
			}
			if err := m.expireStaleLeases(owner); err != nil {
				t.Fatal(err)
			}
			reclaimed, err := m.claimNextKindWorkItem(owner, []string{kind})
			if err != nil || reclaimed == nil || reclaimed.AttemptCount != 2 {
				t.Fatalf("reclaim=%+v err=%v", reclaimed, err)
			}
			// Empty dispatch payload cannot execute: successful replay proves the
			// acknowledged component was skipped and its result retained.
			if err := m.runClaimedWork(owner, *reclaimed, runner); err != nil {
				t.Fatal(err)
			}
			assertResult("acknowledged", resultStatus, "terminal response\n")
			if err := finish(workCtx, resultStatus, "late overwrite"); !errors.Is(err, errSchedulerOwnershipLost) {
				t.Fatalf("stale patch result=%v", err)
			}
		})
	}
}

func TestSchedulerPostgresSnapshotOutlivesOwnershipTransactionBudget(t *testing.T) {
	m, ctx, _ := schedulerOwnershipFixture(t)
	jobID := schedulerReviewJob(t, m, ctx)
	var ordinaryTimeout, ownershipTimeout string
	if err := m.store.db.QueryRowContext(ctx, `SHOW transaction_timeout`).Scan(&ordinaryTimeout); err != nil {
		t.Fatal(err)
	}
	if err := m.ownershipPool().QueryRowContext(ctx, `SHOW transaction_timeout`).Scan(&ownershipTimeout); err != nil {
		t.Fatal(err)
	}
	if ordinaryTimeout != "0" || ownershipTimeout != "5s" {
		t.Fatalf("snapshot timeout=%q ownership timeout=%q", ordinaryTimeout, ownershipTimeout)
	}
	blocker, err := m.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(ctx, `LOCK TABLE engine.scheduled_job_run_targets IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		done <- m.store.recordScheduledGeneralSnapshot(ctx, jobID, started.Unix(), []*scheduledResolvedTarget{
			{Hostname: "snapshot-host-01"}, {Hostname: "snapshot-host-02"},
		}, started.Unix())
	}()
	// Observe the real snapshot waiting in PostgreSQL, then keep it blocked
	// beyond the ownership transaction budget. No large synthetic fleet needed.
	for {
		var waiting bool
		err := m.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND wait_event_type='Lock'
			AND query LIKE '%INSERT INTO engine.scheduled_job_run_targets%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("snapshot exited before lock wait: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case err := <-done:
		t.Fatalf("ordinary snapshot hit ownership deadline after %s: %v", time.Since(started), err)
	case <-time.After(5250 * time.Millisecond):
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var count int
	var hosts string
	if err := m.store.db.QueryRowContext(ctx, `SELECT COUNT(*),string_agg(t.hostname,',' ORDER BY t.hostname)
		FROM engine.scheduled_job_run_targets t JOIN engine.scheduled_job_runs r ON r.id=t.run_id
		WHERE r.job_id=$1`, jobID).Scan(&count, &hosts); err != nil {
		t.Fatal(err)
	}
	if count != 2 || !strings.Contains(hosts, "snapshot-host-01,snapshot-host-02") {
		t.Fatalf("snapshot incomplete: targets=%d hosts=%s", count, hosts)
	}
	t.Logf("ordinary snapshot committed both targets after %s; ownership transaction budget remains 5s", time.Since(started))
}
