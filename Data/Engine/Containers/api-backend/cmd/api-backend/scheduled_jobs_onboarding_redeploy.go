package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/lib/pq"
)

func (s *postgresOperatorStore) redeployOnboardingJob(ctx context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	job, found, err := loadScheduledJobVisibility(ctx, conn, jobID, allowedSiteIDs)
	if err != nil || !found {
		_ = conn.Close()
		return map[string]any{"error": "not found"}, http.StatusNotFound, err
	}
	if normalizeScheduledJobKind(nullString(job.JobKind)) != scheduledJobKindOnboarding {
		_ = conn.Close()
		return map[string]any{"error": "not onboarding job"}, http.StatusBadRequest, nil
	}
	if err := conn.Close(); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	cleared, err := s.clearAllScheduledJobRuns(ctx, jobID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	now := time.Now().Unix()
	occurrence := (now / 60) * 60
	if err := s.recordScheduledOnboardingSnapshot(ctx, jobID, occurrence, now); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	runIDs, err := s.loadScheduledRunIDsForOccurrence(ctx, jobID, occurrence)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{
		"status":     "ok",
		"cleared":    cleared,
		"occurrence": occurrence,
		"run_ids":    int64SliceToAny(runIDs),
	}, http.StatusOK, nil
}

func (s *postgresOperatorStore) clearAllScheduledJobRuns(ctx context.Context, jobID int64) (int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, "SELECT id FROM engine.scheduled_job_runs WHERE job_id=$1", jobID)
	if err != nil {
		return 0, err
	}
	runIDs := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		runIDs = append(runIDs, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	runIDs = uniquePositiveInt64s(runIDs)
	if len(runIDs) == 0 {
		return 0, nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackQuietly(tx)
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.scheduled_job_run_activity WHERE run_id = ANY($1)", pq.Array(runIDs)); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.scheduled_job_run_targets WHERE run_id = ANY($1)", pq.Array(runIDs)); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.scheduled_job_onboarding_target_events WHERE run_id = ANY($1)", pq.Array(runIDs)); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.scheduled_job_onboarding_targets WHERE run_id = ANY($1)", pq.Array(runIDs)); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM engine.scheduled_job_runs WHERE id = ANY($1)", pq.Array(runIDs))
	if err != nil {
		return 0, err
	}
	cleared, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return cleared, nil
}

func (s *postgresOperatorStore) loadScheduledRunIDsForOccurrence(ctx context.Context, jobID, occurrence int64) ([]int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT id
		  FROM engine.scheduled_job_runs
		 WHERE job_id=$1 AND scheduled_ts=$2
	  ORDER BY id ASC
	`, jobID, occurrence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id sql.NullInt64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id.Valid {
			ids = append(ids, id.Int64)
		}
	}
	return ids, rows.Err()
}
