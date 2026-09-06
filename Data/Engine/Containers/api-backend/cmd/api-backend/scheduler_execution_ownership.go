package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var errSchedulerOutcomeUnknown = errors.New("execution outcome unknown; inspect retained execution identity before any manual retry")

type schedulerExecutionContextKey struct{}

type schedulerExecutionRecord struct {
	ID         string `json:"execution_id"`
	State      string `json:"state"`
	ActivityID int64  `json:"activity_id,omitempty"`
	ResultID   string `json:"result_id,omitempty"`
	Holder     string `json:"holder,omitempty"`
	Generation int64  `json:"generation,omitempty"`
}

func schedulerExecutionKey(ctx context.Context) string {
	key, _ := ctx.Value(schedulerExecutionContextKey{}).(string)
	return firstText(key, "work")
}

func withSchedulerExecution(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, schedulerExecutionContextKey{}, key)
}

func (m *goSchedulerManager) beginOwnedWorkTx(ctx context.Context) (*sql.Tx, schedulerWorkItem, func(), error) {
	item, ok := ctx.Value(schedulerWorkContextKey{}).(schedulerWorkItem)
	if !ok || m.store == nil || m.store.db == nil {
		return nil, item, func() {}, errSchedulerOwnershipLost
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, err := m.ownershipPool().Conn(ctx)
	if err != nil {
		cancel()
		return nil, item, func() {}, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		conn.Close()
		cancel()
		return nil, item, func() {}, err
	}
	cleanup := func() { _ = tx.Rollback(); conn.Close(); cancel() }
	if err := requireSchedulerWork(ctx, tx, item); err != nil {
		cleanup()
		return nil, item, func() {}, err
	}
	return tx, item, cleanup, nil
}

func (m *goSchedulerManager) writeOwnedWork(ctx context.Context, query string, args ...any) error {
	tx, _, cleanup, err := m.beginOwnedWorkTx(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return tx.Commit()
}

func schedulerWorkOutcomeUnknown(ctx context.Context, tx *sql.Tx, item schedulerWorkItem) (bool, error) {
	var uncertain bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jsonb_each(COALESCE(payload_json::jsonb->'executions','{}'::jsonb)) e WHERE e.value->>'state'='dispatching') FROM engine.job_scheduler_work_items WHERE id=$1`, item.ID).Scan(&uncertain)
	return uncertain, err
}

// Dispatch uncertainty is a queue failure, not evidence that a remote execution
// stopped. Keep execution state and its original deadline for result callbacks.
func noteScheduledOutcomeUnknown(ctx context.Context, tx *sql.Tx, runID, workID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE engine.scheduled_job_runs SET error=$1,updated_at=$2 WHERE id=$3`,
		fmt.Sprintf("%s: work:%d", errSchedulerOutcomeUnknown, workID), time.Now().Unix(), runID)
	return err
}

func loadSchedulerExecution(ctx context.Context, tx *sql.Tx, item schedulerWorkItem) (schedulerExecutionRecord, error) {
	key := schedulerExecutionKey(ctx)
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT payload_json::jsonb->'executions'->$2 FROM engine.job_scheduler_work_items WHERE id=$1`, item.ID, key).Scan(&raw); err != nil {
		return schedulerExecutionRecord{}, err
	}
	record := schedulerExecutionRecord{ID: fmt.Sprintf("work:%d:%s", item.ID, key), State: "prepared"}
	if raw.Valid {
		if err := json.Unmarshal([]byte(raw.String), &record); err != nil {
			return record, err
		}
		if record.ID != fmt.Sprintf("work:%d:%s", item.ID, key) || !stringInSet(record.State, "prepared", "dispatching", "acknowledged") {
			return record, errors.New("scheduler execution identity or state mismatch")
		}
	}
	return record, nil
}

func storeSchedulerExecution(ctx context.Context, tx *sql.Tx, item schedulerWorkItem, record schedulerExecutionRecord) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items
		SET payload_json=jsonb_set(payload_json::jsonb,'{executions}',
		COALESCE(payload_json::jsonb->'executions','{}'::jsonb) || jsonb_build_object($2::text,$3::jsonb),true)::text,updated_at=$4
		WHERE id=$1`, item.ID, schedulerExecutionKey(ctx), string(raw), time.Now().Unix())
	return err
}

// Replaying a claim may revisit already acknowledged components. Their durable
// execution identity wins: skip dispatch rather than create a second activity.
// Without acknowledgement, only an actual terminal execution result can
// settle uncertainty; absence of a result is never permission to resend.
func (m *goSchedulerManager) resumeSchedulerExecution(ctx context.Context) (bool, error) {
	tx, item, cleanup, err := m.beginOwnedWorkTx(ctx)
	if err != nil {
		return false, err
	}
	defer cleanup()
	record, err := loadSchedulerExecution(ctx, tx, item)
	if err != nil {
		return false, err
	}
	if record.State == "dispatching" && record.ActivityID > 0 {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT status FROM engine.activity_history
			WHERE id=$1 AND metadata_json::jsonb->>'scheduler_execution_id'=$2`, record.ActivityID, record.ID).Scan(&state)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if err == nil && stringInSet(strings.ToLower(state), "success", "completed", "succeeded", "failed") {
			record.State = "acknowledged"
			if err := storeSchedulerExecution(ctx, tx, item, record); err != nil {
				return false, err
			}
		}
	}
	if record.State == "dispatching" && item.Kind == schedulerKindScheduledWorkflowRun {
		var count int
		var runID sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),MIN(id) FROM engine.workflow_runs WHERE source_metadata_json::jsonb->>'scheduler_execution_id'=$1 AND LOWER(status) IN ('running','success','completed','succeeded')`, record.ID).Scan(&count, &runID); err != nil {
			return false, err
		}
		if count == 1 && runID.Valid {
			record.State, record.ResultID = "acknowledged", fmt.Sprint(runID.Int64)
			if err := storeSchedulerExecution(ctx, tx, item, record); err != nil {
				return false, err
			}
		}
	}
	if record.State == "dispatching" {
		return false, fmt.Errorf("%w: %s", errSchedulerOutcomeUnknown, record.ID)
	}
	return record.State == "acknowledged", tx.Commit()
}

// This transaction is dispatch admission's linearization point. Once marked
// dispatching, another generation may inspect the result but must never blindly
// replay the network side effect, including when the sending process dies.
func (m *goSchedulerManager) beginSchedulerDispatch(ctx context.Context, resultID string) error {
	tx, item, cleanup, err := m.beginOwnedWorkTx(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	record, err := loadSchedulerExecution(ctx, tx, item)
	if err != nil {
		return err
	}
	if record.State != "prepared" {
		return fmt.Errorf("%w: %s", errSchedulerOutcomeUnknown, record.ID)
	}
	record.State, record.Holder, record.Generation = "dispatching", item.LeaseOwner, item.AttemptCount
	if resultID != "" {
		record.ResultID = resultID
	}
	if err := storeSchedulerExecution(ctx, tx, item, record); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *goSchedulerManager) acknowledgeSchedulerDispatch(ctx context.Context, resultID string) error {
	tx, item, cleanup, err := m.beginOwnedWorkTx(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := acknowledgeSchedulerDispatchTx(ctx, tx, item, resultID); err != nil {
		return err
	}
	return tx.Commit()
}

func acknowledgeSchedulerDispatchTx(ctx context.Context, tx *sql.Tx, item schedulerWorkItem, resultID string) error {
	record, err := loadSchedulerExecution(ctx, tx, item)
	if err != nil {
		return err
	}
	if record.State != "dispatching" || record.Holder != item.LeaseOwner || record.Generation != item.AttemptCount {
		return errSchedulerOwnershipLost
	}
	record.State = "acknowledged"
	if resultID != "" {
		record.ResultID = resultID
	}
	return storeSchedulerExecution(ctx, tx, item, record)
}
