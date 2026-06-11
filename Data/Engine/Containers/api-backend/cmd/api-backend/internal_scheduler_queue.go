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

const (
	schedulerLaneOnboarding           = "onboarding"
	schedulerLaneScheduledJob         = "scheduled_job"
	schedulerKindOnboardingRun        = "onboarding_run"
	schedulerKindScheduledRun         = "scheduled_run"
	schedulerKindScheduledWorkflowRun = "scheduled_workflow_run"
)

type schedulerWorkItemInsert struct {
	DedupeKey string
	Kind      string
	SiteID    sql.NullInt64
	Lane      string
	JobID     sql.NullInt64
	RunID     sql.NullInt64
	TargetID  sql.NullInt64
	Payload   map[string]any
	Priority  int64
}

func (s *postgresOperatorStore) enqueueInternalSchedulerWorkItem(ctx context.Context, kind string, payload map[string]any) (int64, error) {
	item, updateRunRunning, err := schedulerWorkItemFromPayload(kind, payload)
	if err != nil {
		return 0, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	workID, err := insertSchedulerWorkItem(ctx, tx, item)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if updateRunRunning {
		now := int64Now()
		if _, err := tx.ExecContext(ctx, `
			UPDATE engine.scheduled_job_runs
			   SET status=$1,
			       started_ts=COALESCE(started_ts, $2),
			       updated_at=$3
			 WHERE id=$4
		`, scheduledStatusRunning, now, now, item.RunID.Int64); err != nil {
			_ = tx.Rollback()
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return workID, nil
}

func schedulerWorkItemFromPayload(kind string, body map[string]any) (schedulerWorkItemInsert, bool, error) {
	kind = strings.ToLower(cleanText(kind))
	switch kind {
	case schedulerKindOnboardingRun:
		runID := schedulerRequiredID(firstNonEmpty(body["run_id"], body["run_row_id"]))
		jobID := schedulerRequiredID(body["job_id"])
		if !jobID.Valid || !runID.Valid {
			return schedulerWorkItemInsert{}, false, errors.New("job_id_and_run_id_required")
		}
		payload := map[string]any{
			"job_id":       jobID.Int64,
			"run_id":       runID.Int64,
			"scheduled_ts": coerceInt64(body["scheduled_ts"]),
			"components":   schedulerAnyList(body["components"]),
			"targets":      schedulerAnyList(body["targets"]),
			"credential_id": schedulerNullableValue(
				body["credential_id"],
			),
		}
		return schedulerWorkItemInsert{
			DedupeKey: fmt.Sprintf("onboarding:%d", runID.Int64),
			Kind:      schedulerKindOnboardingRun,
			SiteID:    schedulerOptionalInt64(body["site_id"]),
			Lane:      schedulerLaneOnboarding,
			JobID:     jobID,
			RunID:     runID,
			Payload:   payload,
			Priority:  50,
		}, true, nil
	case schedulerKindScheduledRun:
		runID := schedulerRequiredID(body["run_id"])
		jobID := schedulerRequiredID(body["job_id"])
		if !jobID.Valid || !runID.Valid {
			return schedulerWorkItemInsert{}, false, errors.New("job_id_and_run_id_required")
		}
		targetIDs := schedulerPositiveInt64List(body["target_row_ids"])
		payload := map[string]any{
			"job_id":              jobID.Int64,
			"run_id":              runID.Int64,
			"scheduled_ts":        coerceInt64(body["scheduled_ts"]),
			"run_mode":            firstText(strings.ToLower(cleanText(body["run_mode"])), "system"),
			"script_components":   schedulerAnyList(body["script_components"]),
			"ansible_components":  schedulerAnyList(body["ansible_components"]),
			"credential_id":       schedulerNullableValue(body["credential_id"]),
			"use_service_account": boolFromAny(body["use_service_account"]),
			"shared_execution":    boolFromAny(body["shared_execution"]),
			"component_index":     schedulerNullableValue(body["component_index"]),
			"target_row_ids":      targetIDs,
			"task_link":           schedulerAnyMap(body["task_link"]),
		}
		targetSuffix := "all"
		if len(targetIDs) > 0 {
			parts := make([]string, 0, len(targetIDs))
			for _, value := range targetIDs {
				parts = append(parts, fmt.Sprintf("%d", value))
			}
			targetSuffix = strings.Join(parts, ",")
		}
		return schedulerWorkItemInsert{
			DedupeKey: fmt.Sprintf("scheduled-run:%d:%s", runID.Int64, targetSuffix),
			Kind:      schedulerKindScheduledRun,
			SiteID:    schedulerOptionalInt64(body["site_id"]),
			Lane:      schedulerLaneScheduledJob,
			JobID:     jobID,
			RunID:     runID,
			Payload:   payload,
			Priority:  40,
		}, false, nil
	case schedulerKindScheduledWorkflowRun:
		runID := schedulerRequiredID(body["run_id"])
		jobID := schedulerRequiredID(body["job_id"])
		if !jobID.Valid || !runID.Valid {
			return schedulerWorkItemInsert{}, false, errors.New("job_id_and_run_id_required")
		}
		siteID := schedulerOptionalInt64(body["site_id"])
		siteSuffix := int64(0)
		if siteID.Valid {
			siteSuffix = siteID.Int64
		}
		payload := map[string]any{
			"job_id":              jobID.Int64,
			"run_id":              runID.Int64,
			"scheduled_ts":        coerceInt64(body["scheduled_ts"]),
			"workflow_component":  schedulerAnyMap(body["workflow_component"]),
			"workflow_site_scope": map[string]any{"site_id": schedulerNullableValue(body["site_id"])},
			"task_link":           schedulerAnyMap(body["task_link"]),
		}
		return schedulerWorkItemInsert{
			DedupeKey: fmt.Sprintf("scheduled-workflow:%d:%d", runID.Int64, siteSuffix),
			Kind:      schedulerKindScheduledWorkflowRun,
			SiteID:    siteID,
			Lane:      schedulerLaneScheduledJob,
			JobID:     jobID,
			RunID:     runID,
			Payload:   payload,
			Priority:  40,
		}, false, nil
	default:
		return schedulerWorkItemInsert{}, false, fmt.Errorf("unsupported_work_item_kind:%s", kind)
	}
}

func insertSchedulerWorkItem(ctx context.Context, tx *sql.Tx, item schedulerWorkItemInsert) (int64, error) {
	now := int64Now()
	if item.DedupeKey != "" {
		var existingID sql.NullInt64
		err := tx.QueryRowContext(ctx, "SELECT id FROM engine.job_scheduler_work_items WHERE dedupe_key=$1 LIMIT 1", item.DedupeKey).Scan(&existingID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if existingID.Valid {
			if _, err := tx.ExecContext(ctx, "UPDATE engine.job_scheduler_work_items SET updated_at=$1 WHERE id=$2", now, existingID.Int64); err != nil {
				return 0, err
			}
			return existingID.Int64, nil
		}
	}
	payloadJSON, err := json.Marshal(item.Payload)
	if err != nil {
		return 0, err
	}
	var workID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO engine.job_scheduler_work_items(
			dedupe_key, kind, site_id, lane, job_id, run_id, target_id, payload_json,
			status, attempt_count, priority, available_at, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id
	`, schedulerNullableString(item.DedupeKey), item.Kind, sqlNullIntArg(item.SiteID), item.Lane, sqlNullIntArg(item.JobID), sqlNullIntArg(item.RunID), sqlNullIntArg(item.TargetID), string(payloadJSON), workStatusQueued, 0, item.Priority, now, now, now).Scan(&workID)
	return workID, err
}

func schedulerRequiredID(value any) sql.NullInt64 {
	parsed := coerceInt64(value)
	if parsed <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: parsed, Valid: true}
}

func schedulerOptionalInt64(value any) sql.NullInt64 {
	if value == nil || cleanText(value) == "" {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: coerceInt64(value), Valid: true}
}

func schedulerNullableValue(value any) any {
	if value == nil || cleanText(value) == "" {
		return nil
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return parsed
		}
		return cleanText(value)
	default:
		return value
	}
}

func schedulerNullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func schedulerAnyList(value any) []any {
	if value == nil {
		return []any{}
	}
	if typed, ok := value.([]any); ok {
		out := make([]any, 0, len(typed))
		out = append(out, typed...)
		return out
	}
	return []any{value}
}

func schedulerAnyMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return copyMap(typed)
	}
	return map[string]any{}
}

func schedulerPositiveInt64List(value any) []int64 {
	raw := schedulerAnyList(value)
	out := make([]int64, 0, len(raw))
	for _, item := range raw {
		parsed := coerceInt64(item)
		if parsed > 0 {
			out = append(out, parsed)
		}
	}
	return out
}

func int64Now() int64 {
	return time.Now().Unix()
}
