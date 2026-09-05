package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

const schedulerWorkLeaseSeconds = int64(60)

var errSchedulerOwnershipLost = errors.New("scheduler ownership lost")

type schedulerOwnerContextKey struct{}
type schedulerWorkContextKey struct{}

func schedulerLeadershipPod(holder string) string {
	pod, generation, found := strings.Cut(holder, "/")
	if found && !clusterUUIDRE.MatchString(generation) {
		return ""
	}
	return pod
}

// Lock leadership before the work row, so renewal, takeover and work mutation
// have one serialization order. A process identity alone is never a claim.
func requireSchedulerLeadership(ctx context.Context, tx *sql.Tx) (string, error) {
	holder, _ := ctx.Value(schedulerOwnerContextKey{}).(string)
	if holder == "" || ctx.Err() != nil {
		return "", errSchedulerOwnershipLost
	}
	var current string
	err := tx.QueryRowContext(ctx, `SELECT holder FROM engine.cluster_application_leases
		WHERE name='scheduler-leader' AND holder=$1 AND expires_at >= $2 FOR SHARE`, holder, time.Now().Unix()).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errSchedulerOwnershipLost
	}
	return current, err
}

func requireSchedulerWork(ctx context.Context, tx *sql.Tx, item schedulerWorkItem) error {
	holder, err := requireSchedulerLeadership(ctx, tx)
	if err != nil {
		return err
	}
	if item.ID <= 0 || item.AttemptCount <= 0 || item.LeaseOwner != holder {
		return errSchedulerOwnershipLost
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM engine.job_scheduler_work_items
		WHERE id=$1 AND lease_owner=$2 AND attempt_count=$3 AND status=$4
		AND lease_expires_at >= $5 FOR UPDATE`, item.ID, holder, item.AttemptCount, workStatusRunning, time.Now().Unix()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return errSchedulerOwnershipLost
	}
	return err
}

func (m *goSchedulerManager) heartbeatWorkItem(ctx context.Context, item schedulerWorkItem) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	holder, _ := ctx.Value(schedulerOwnerContextKey{}).(string)
	if holder == "" || item.LeaseOwner != holder || item.ID <= 0 || item.AttemptCount <= 0 {
		return false, errSchedulerOwnershipLost
	}
	executor := m.leaseTransport
	if executor == nil {
		executor = m.store.db
	}
	now := time.Now().Unix()
	result, err := executor.ExecContext(ctx, `WITH ownership AS (
		SELECT holder FROM engine.cluster_application_leases
		WHERE name='scheduler-leader' AND holder=$1 AND expires_at >= $2 FOR SHARE
	) UPDATE engine.job_scheduler_work_items SET heartbeat_at=$2, lease_expires_at=$3, updated_at=$2
	WHERE id=$4 AND lease_owner=$1 AND attempt_count=$5 AND status=$6 AND lease_expires_at >= $2
	AND EXISTS(SELECT 1 FROM ownership)`, holder, now, now+schedulerWorkLeaseSeconds, item.ID, item.AttemptCount, workStatusRunning)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// A work lease covers ownership, not script duration. Long executions retain
// their configured limits while the independent heartbeat renews this claim.
func (m *goSchedulerManager) runClaimedWork(ctx context.Context, item schedulerWorkItem, run func(context.Context, schedulerWorkItem) error) error {
	owned, err := m.heartbeatWorkItem(ctx, item)
	if err != nil {
		return err
	}
	if !owned {
		return errSchedulerOwnershipLost
	}
	workCtx := context.WithValue(ctx, schedulerWorkContextKey{}, item)
	guard := startOwnershipLeaseGuard(workCtx, 10*time.Second, 45*time.Second, time.Now(), errSchedulerOwnershipLost, func(renewCtx context.Context) (bool, error) {
		return m.heartbeatWorkItem(renewCtx, item)
	})
	defer guard.Close()
	status, message := workStatusSucceeded, ""
	if err := run(guard.Context(), item); err != nil {
		status, message = workStatusFailed, err.Error()
	}
	if guard.Context().Err() != nil {
		return fmt.Errorf("%w: %v", errSchedulerOwnershipLost, context.Cause(guard.Context()))
	}
	return m.completeWorkItem(guard.Context(), item, status, message)
}

func (m *goSchedulerManager) startClaimedWork(ctx context.Context, item schedulerWorkItem, run func(context.Context, schedulerWorkItem) error) {
	go func() {
		if err := m.runClaimedWork(ctx, item, run); err != nil {
			log.Printf("scheduler work item id=%d generation=%d stopped: %v", item.ID, item.AttemptCount, err)
		}
	}()
}

func (m *goSchedulerManager) completeWorkItem(ctx context.Context, item schedulerWorkItem, status, message string) error {
	if !stringInSet(status, workStatusSucceeded, workStatusFailed, workStatusCancelled) {
		status = workStatusFailed
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireSchedulerWork(ctx, tx, item); err != nil {
		return err
	}
	uncertain, err := schedulerWorkOutcomeUnknown(ctx, tx, item)
	if err != nil {
		return err
	}
	if uncertain {
		status = workStatusFailed
		message = fmt.Sprintf("%s: work:%d; %s", errSchedulerOutcomeUnknown, item.ID, message)
	}
	now := time.Now().Unix()
	if uncertain {
		if _, err := tx.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items SET payload_json=jsonb_set(payload_json::jsonb,'{recovery_state}','"outcome_unknown"'::jsonb,true)::text WHERE id=$1`, item.ID); err != nil {
			return err
		}
		if item.RunID.Valid {
			if err := noteScheduledOutcomeUnknown(ctx, tx, item.RunID.Int64, item.ID); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.job_scheduler_work_items
		SET status=$1, lease_expires_at=NULL, heartbeat_at=$2, error=$3, finished_at=$2, updated_at=$2
		WHERE id=$4 AND lease_owner=$5 AND attempt_count=$6`, status, now, truncateString(message, 2000), item.ID, item.LeaseOwner, item.AttemptCount); err != nil {
		return err
	}
	return tx.Commit()
}
