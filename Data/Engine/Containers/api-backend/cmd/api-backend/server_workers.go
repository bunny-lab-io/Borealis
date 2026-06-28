package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	workerStatusStarting = "starting"
	workerStatusRunning  = "running"
	workerStatusIdle     = "idle"
	workerStatusStopped  = "stopped"
	workerStatusLost     = "lost"
	workStatusQueued     = "queued"
	workStatusRunning    = "running"
	workStatusSucceeded  = "succeeded"
	workStatusFailed     = "failed"
	workStatusCancelled  = "cancelled"
)

type serverWorkerStore interface {
	serverWorkerPayload(ctx context.Context, historySeconds int, includeContainerMetadata bool) (map[string]any, error)
}

type serverWorkerRecreateStore interface {
	queueSiteWorkerRecreate(ctx context.Context, workerGUID string) (map[string]any, int, error)
}

type workerSnapshotRow struct {
	WorkerGUID       sql.NullString
	ContainerName    sql.NullString
	SiteID           sql.NullInt64
	Status           sql.NullString
	StartedAt        sql.NullInt64
	LastSeenAt       sql.NullInt64
	IdleSince        sql.NullInt64
	StoppedAt        sql.NullInt64
	CurrentLanesJSON sql.NullString
	ClaimedCount     sql.NullInt64
	TaskLinksJSON    sql.NullString
	DockerState      sql.NullString
	ExitCode         sql.NullInt64
}

type workItemRow struct {
	ID            sql.NullInt64
	Kind          sql.NullString
	SiteID        sql.NullInt64
	Lane          sql.NullString
	JobID         sql.NullInt64
	RunID         sql.NullInt64
	TargetID      sql.NullInt64
	Status        sql.NullString
	LeaseOwner    sql.NullString
	WorkerGUID    sql.NullString
	ContainerName sql.NullString
	AttemptCount  sql.NullInt64
	HeartbeatAt   sql.NullInt64
	StartedAt     sql.NullInt64
	FinishedAt    sql.NullInt64
	UpdatedAt     sql.NullInt64
	PayloadJSON   sql.NullString
	Error         sql.NullString
}

type scheduledRunWorkRow struct {
	RunID          sql.NullInt64
	JobID          sql.NullInt64
	SiteID         sql.NullInt64
	TargetHostname sql.NullString
	Status         sql.NullString
	Error          sql.NullString
	StartedAt      sql.NullInt64
	FinishedAt     sql.NullInt64
	UpdatedAt      sql.NullInt64
	TargetCount    sql.NullInt64
	ComponentKind  sql.NullString
}

func registerServerWorkerRoutes(mux *http.ServeMux, auth *authService, _ http.Handler) {
	mux.HandleFunc("/api/server/workers", serverWorkersHandler(auth))
	mux.HandleFunc("/api/server/workers/", serverWorkerSubtreeHandler(auth))
}

func serverWorkersHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(serverWorkerStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "server_workers_unavailable"})
			return
		}
		timeout := auth.timeout
		if timeout <= 0 {
			timeout = defaultAuthTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		historySeconds := clampInt(parseIntDefault(r.URL.Query().Get("history_seconds"), 60), 0, 86400)
		payload, err := store.serverWorkerPayload(ctx, historySeconds, true)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func serverWorkerSubtreeHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/server/workers/"), "/"), "/")
		if len(parts) == 2 && parts[1] == "recreate" && r.Method == http.MethodPost {
			if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
				failure.write(w)
				return
			}
			store, ok := auth.store.(serverWorkerRecreateStore)
			if !ok {
				writeJSON(w, http.StatusBadGateway, map[string]any{"error": "server_workers_unavailable"})
				return
			}
			timeout := auth.timeout
			if timeout <= 0 {
				timeout = defaultAuthTimeout
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			payload, status, err := store.queueSiteWorkerRecreate(ctx, parts[0])
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	}
}

func (s *postgresOperatorStore) serverWorkerPayload(ctx context.Context, historySeconds int, includeContainerMetadata bool) (map[string]any, error) {
	workerIdleTTLSeconds := parseEnvIntMin("BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS", 300, 300)
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}

	rows, err := listWorkerSnapshots(ctx, conn, historySeconds)
	if err != nil {
		conn.Close()
		return nil, err
	}
	activeWork, err := listWorkerActiveWork(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	activeScheduledRunWork, err := listWorkerScheduledRunWork(ctx, conn, true, historySeconds)
	if err != nil {
		conn.Close()
		return nil, err
	}
	activeWork = filterScheduledDispatchWork(activeWork, activeScheduledRunWork)
	activeWork = append(activeWork, activeScheduledRunWork...)
	recentWork, err := listWorkerRecentWork(ctx, conn, historySeconds)
	if err != nil {
		conn.Close()
		return nil, err
	}
	recentScheduledRunWork, err := listWorkerScheduledRunWork(ctx, conn, false, historySeconds)
	if err != nil {
		conn.Close()
		return nil, err
	}
	recentWork = filterScheduledDispatchWork(recentWork, recentScheduledRunWork)
	recentWork = append(recentWork, recentScheduledRunWork...)
	siteNames, siteRows, siteDeviceCounts, siteOnlineDeviceCounts, jobNames, err := collectWorkerReferenceData(ctx, conn, rows, activeWork, recentWork)
	_ = conn.Close()
	if err != nil {
		return nil, err
	}

	enrichWorkerReferences(rows, activeWork, recentWork, siteNames, siteDeviceCounts, siteOnlineDeviceCounts, jobNames)
	if includeContainerMetadata {
		rows = attachWorkerContainerMetadata(rows)
	}

	activeSiteWorkers := 0
	activeManagers := 0
	for _, row := range rows {
		status := strings.ToLower(cleanText(row["status"]))
		if status != workerStatusStarting && status != workerStatusRunning && status != workerStatusIdle {
			continue
		}
		if coerceInt64(row["site_id"]) > 0 {
			activeSiteWorkers++
		} else {
			activeManagers++
		}
	}
	return map[string]any{
		"active_count":              activeSiteWorkers,
		"manager_active_count":      activeManagers,
		"workers":                   rows,
		"active_work":               activeWork,
		"recent_work":               recentWork,
		"site_names":                stringifyIntMap(siteNames),
		"site_device_counts":        stringifyCountMap(siteDeviceCounts),
		"site_online_device_counts": stringifyCountMap(siteOnlineDeviceCounts),
		"sites":                     siteRows,
		"worker_idle_ttl_seconds":   workerIdleTTLSeconds,
	}, nil
}

func (s *postgresOperatorStore) queueSiteWorkerRecreate(ctx context.Context, workerGUID string) (map[string]any, int, error) {
	workerGUID = cleanText(workerGUID)
	if workerGUID == "" {
		return map[string]any{
			"error":   "site_worker_recreate_unavailable",
			"message": "A site-worker id is required.",
		}, http.StatusBadRequest, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	cutoff := time.Now().Unix() - 86400
	var row workerSnapshotRow
	var siteName sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT
			worker_guid, container_name, site_id, status, started_at, last_seen_at,
			idle_since, stopped_at, current_lanes_json, claimed_count, task_links_json,
			docker_state, exit_code, s.name
		  FROM engine.job_scheduler_workers
	 LEFT JOIN engine.sites AS s ON s.id = job_scheduler_workers.site_id
		 WHERE worker_guid = $1
		   AND COALESCE(job_scheduler_workers.site_id, 0) > 0
		   AND (
		         status NOT IN ($2, $3)
		      OR COALESCE(stopped_at, last_seen_at, updated_at, started_at, 0) >= $4
		   )
	  ORDER BY COALESCE(stopped_at, last_seen_at, started_at) DESC
		 LIMIT 1
	`, workerGUID, workerStatusStopped, workerStatusLost, cutoff).Scan(
		&row.WorkerGUID,
		&row.ContainerName,
		&row.SiteID,
		&row.Status,
		&row.StartedAt,
		&row.LastSeenAt,
		&row.IdleSince,
		&row.StoppedAt,
		&row.CurrentLanesJSON,
		&row.ClaimedCount,
		&row.TaskLinksJSON,
		&row.DockerState,
		&row.ExitCode,
		&siteName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, 0, commitErr
		}
		committed = true
		return map[string]any{
			"error":   "site_worker_recreate_unavailable",
			"message": "Site worker not found.",
		}, http.StatusNotFound, nil
	}
	if err != nil {
		return nil, 0, err
	}

	containerName := nullString(row.ContainerName)
	if containerName == "" {
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, 0, commitErr
		}
		committed = true
		return map[string]any{
			"error":   "site_worker_recreate_unavailable",
			"message": "Site worker has no tracked container.",
		}, http.StatusConflict, nil
	}

	action := map[string]any{
		"id":             "recreate",
		"label":          "Re-Create Site Worker",
		"action":         "recreate",
		"worker_guid":    workerGUID,
		"container_name": containerName,
		"site_id":        nullInt(row.SiteID),
	}
	workItemID, err := enqueueGoServiceAction(ctx, tx, "site-worker", action)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	committed = true

	return map[string]any{
		"queued":         true,
		"worker_guid":    nullString(row.WorkerGUID),
		"site_id":        nullInt(row.SiteID),
		"site_name":      nullString(siteName),
		"container_name": containerName,
		"work_item_id":   workItemID,
	}, http.StatusAccepted, nil
}

func enqueueGoServiceAction(ctx context.Context, tx *sql.Tx, serviceKey string, action map[string]any) (int64, error) {
	now := time.Now().Unix()
	normalizedService := strings.ToLower(cleanText(serviceKey))
	actionName := strings.ToLower(cleanText(action["action"]))
	actionMode := strings.ToLower(cleanText(action["mode"]))
	actionScope := strings.ToLower(cleanText(firstNonEmpty(action["scope"], action["worker_guid"], action["container_name"])))
	scopeKey := "global"
	if actionScope != "" {
		sum := sha256.Sum256([]byte(actionScope))
		scopeKey = fmt.Sprintf("%x", sum[:])[:16]
	}
	dedupe := fmt.Sprintf("service-action:%s:%s:%s:%s:%d", normalizedService, actionName, actionMode, scopeKey, now/60)
	payloadJSON, err := json.Marshal(map[string]any{"service_key": normalizedService, "action": action})
	if err != nil {
		return 0, err
	}
	var existingID sql.NullInt64
	err = tx.QueryRowContext(ctx, "SELECT id FROM engine.job_scheduler_work_items WHERE dedupe_key = $1 LIMIT 1", dedupe).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if existingID.Valid {
		if _, err := tx.ExecContext(ctx, "UPDATE engine.job_scheduler_work_items SET updated_at = $1 WHERE id = $2", now, existingID.Int64); err != nil {
			return 0, err
		}
		return existingID.Int64, nil
	}
	var workItemID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO engine.job_scheduler_work_items(
			dedupe_key, kind, site_id, lane, payload_json,
			status, attempt_count, priority, available_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id
	`, dedupe, "service_action", 0, "service_action", string(payloadJSON), workStatusQueued, 0, 100, now, now, now).Scan(&workItemID)
	return workItemID, err
}

func listWorkerSnapshots(ctx context.Context, conn *sql.Conn, historySeconds int) ([]map[string]any, error) {
	cutoff := time.Now().Unix() - int64(maxInt(historySeconds, 0))
	rows, err := conn.QueryContext(ctx, `
		SELECT
			worker_guid, container_name, site_id, status, started_at, last_seen_at,
			idle_since, stopped_at, current_lanes_json, claimed_count, task_links_json,
			docker_state, exit_code
		  FROM engine.job_scheduler_workers
		 WHERE status NOT IN ($1, $2)
		    OR COALESCE(stopped_at, last_seen_at, updated_at, started_at, 0) >= $3
	  ORDER BY COALESCE(stopped_at, last_seen_at, started_at) DESC
	`, workerStatusStopped, workerStatusLost, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []map[string]any{}
	for rows.Next() {
		var row workerSnapshotRow
		if err := rows.Scan(
			&row.WorkerGUID,
			&row.ContainerName,
			&row.SiteID,
			&row.Status,
			&row.StartedAt,
			&row.LastSeenAt,
			&row.IdleSince,
			&row.StoppedAt,
			&row.CurrentLanesJSON,
			&row.ClaimedCount,
			&row.TaskLinksJSON,
			&row.DockerState,
			&row.ExitCode,
		); err != nil {
			return nil, err
		}
		results = append(results, map[string]any{
			"worker_guid":    nullString(row.WorkerGUID),
			"container_name": nullString(row.ContainerName),
			"site_id":        nullableInt(row.SiteID),
			"status":         nullString(row.Status),
			"started_at":     nullableInt(row.StartedAt),
			"last_seen_at":   nullableInt(row.LastSeenAt),
			"idle_since":     nullableInt(row.IdleSince),
			"stopped_at":     nullableInt(row.StoppedAt),
			"current_lanes":  parseJSONText(row.CurrentLanesJSON, []any{}),
			"claimed_count":  nullInt(row.ClaimedCount),
			"task_links":     parseJSONText(row.TaskLinksJSON, []any{}),
			"docker_state":   nullString(row.DockerState),
			"exit_code":      nullableInt(row.ExitCode),
		})
	}
	return results, rows.Err()
}

func listWorkerActiveWork(ctx context.Context, conn *sql.Conn) ([]map[string]any, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT id, kind, site_id, lane, job_id, run_id, target_id, status, lease_owner,
		       worker_guid, container_name, attempt_count, heartbeat_at, started_at,
		       payload_json
		  FROM engine.job_scheduler_work_items
		 WHERE status = $1
	  ORDER BY started_at DESC, id DESC
	`, workStatusRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []map[string]any{}
	for rows.Next() {
		var row workItemRow
		if err := rows.Scan(
			&row.ID,
			&row.Kind,
			&row.SiteID,
			&row.Lane,
			&row.JobID,
			&row.RunID,
			&row.TargetID,
			&row.Status,
			&row.LeaseOwner,
			&row.WorkerGUID,
			&row.ContainerName,
			&row.AttemptCount,
			&row.HeartbeatAt,
			&row.StartedAt,
			&row.PayloadJSON,
		); err != nil {
			return nil, err
		}
		payload := parseJSONText(row.PayloadJSON, map[string]any{})
		payloadMap, _ := payload.(map[string]any)
		results = append(results, map[string]any{
			"id":             nullInt(row.ID),
			"kind":           nullString(row.Kind),
			"site_id":        nullableInt(row.SiteID),
			"lane":           nullString(row.Lane),
			"job_id":         nullableInt(row.JobID),
			"run_id":         nullableInt(row.RunID),
			"target_id":      nullableInt(row.TargetID),
			"status":         nullString(row.Status),
			"lease_owner":    nullString(row.LeaseOwner),
			"worker_guid":    nullString(row.WorkerGUID),
			"container_name": nullString(row.ContainerName),
			"attempt_count":  nullInt(row.AttemptCount),
			"heartbeat_at":   nullableInt(row.HeartbeatAt),
			"started_at":     nullableInt(row.StartedAt),
			"target_count":   payloadTargetCount(payloadMap),
			"task_type":      payloadTaskType(nullString(row.Kind), payloadMap),
		})
	}
	return results, rows.Err()
}

func listWorkerRecentWork(ctx context.Context, conn *sql.Conn, historySeconds int) ([]map[string]any, error) {
	cutoff := time.Now().Unix() - int64(maxInt(historySeconds, 0))
	rows, err := conn.QueryContext(ctx, `
		SELECT id, kind, site_id, lane, job_id, run_id, target_id, status, lease_owner,
		       worker_guid, container_name, attempt_count, heartbeat_at, started_at,
		       finished_at, updated_at, payload_json, error
		  FROM engine.job_scheduler_work_items
		 WHERE status IN ($1, $2, $3, $4, $5)
		   AND COALESCE(finished_at, heartbeat_at, started_at, updated_at, created_at, 0) >= $6
	  ORDER BY COALESCE(finished_at, heartbeat_at, started_at, updated_at, created_at, 0) DESC, id DESC
	`, workStatusQueued, workStatusRunning, workStatusSucceeded, workStatusFailed, workStatusCancelled, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []map[string]any{}
	for rows.Next() {
		var row workItemRow
		if err := rows.Scan(
			&row.ID,
			&row.Kind,
			&row.SiteID,
			&row.Lane,
			&row.JobID,
			&row.RunID,
			&row.TargetID,
			&row.Status,
			&row.LeaseOwner,
			&row.WorkerGUID,
			&row.ContainerName,
			&row.AttemptCount,
			&row.HeartbeatAt,
			&row.StartedAt,
			&row.FinishedAt,
			&row.UpdatedAt,
			&row.PayloadJSON,
			&row.Error,
		); err != nil {
			return nil, err
		}
		payload := parseJSONText(row.PayloadJSON, map[string]any{})
		payloadMap, _ := payload.(map[string]any)
		taskLink, _ := payloadMap["task_link"].(map[string]any)
		if taskLink == nil {
			taskLink = map[string]any{}
		}
		results = append(results, map[string]any{
			"id":             nullInt(row.ID),
			"kind":           nullString(row.Kind),
			"site_id":        nullableInt(row.SiteID),
			"lane":           nullString(row.Lane),
			"job_id":         nullableInt(row.JobID),
			"run_id":         nullableInt(row.RunID),
			"target_id":      nullableInt(row.TargetID),
			"status":         nullString(row.Status),
			"lease_owner":    nullString(row.LeaseOwner),
			"worker_guid":    nullString(row.WorkerGUID),
			"container_name": nullString(row.ContainerName),
			"attempt_count":  nullInt(row.AttemptCount),
			"heartbeat_at":   nullableInt(row.HeartbeatAt),
			"started_at":     nullableInt(row.StartedAt),
			"finished_at":    nullableInt(row.FinishedAt),
			"updated_at":     nullableInt(row.UpdatedAt),
			"task_link":      copyMap(taskLink),
			"target_count":   payloadTargetCount(payloadMap),
			"task_type":      payloadTaskType(nullString(row.Kind), payloadMap),
			"error":          nullString(row.Error),
		})
	}
	return results, rows.Err()
}

func listWorkerScheduledRunWork(ctx context.Context, conn *sql.Conn, activeOnly bool, historySeconds int) ([]map[string]any, error) {
	params := []any{}
	where := ""
	if activeOnly {
		where = "WHERE r.status=$1"
		params = append(params, scheduledStatusRunning)
	} else {
		cutoff := time.Now().Unix() - int64(maxInt(historySeconds, 0))
		where = `
			WHERE r.status IN ($1,$2,$3,$4,$5,$6)
			  AND COALESCE(r.finished_ts, r.updated_at, r.started_ts, r.scheduled_ts, 0) >= $7
		`
		params = append(params,
			scheduledStatusSuccess,
			scheduledStatusWarning,
			scheduledStatusFailed,
			scheduledStatusExpired,
			scheduledStatusTimedOut,
			scheduledStatusSkipped,
			cutoff,
		)
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT r.id, r.job_id, COALESCE(MIN(t.site_id), 0) AS site_id,
		       r.target_hostname, r.status, r.error, r.started_ts, r.finished_ts,
		       r.updated_at, COUNT(t.id) AS target_count, r.component_kind
		  FROM engine.scheduled_job_runs AS r
	 LEFT JOIN engine.scheduled_job_run_targets AS t ON t.run_id=r.id
		`+where+`
	  GROUP BY r.id, r.job_id, r.target_hostname, r.status, r.error, r.started_ts, r.finished_ts, r.updated_at, r.component_kind
	  ORDER BY COALESCE(r.finished_ts, r.updated_at, r.started_ts, r.scheduled_ts, 0) DESC, r.id DESC
	`, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []map[string]any{}
	for rows.Next() {
		var row scheduledRunWorkRow
		if err := rows.Scan(
			&row.RunID,
			&row.JobID,
			&row.SiteID,
			&row.TargetHostname,
			&row.Status,
			&row.Error,
			&row.StartedAt,
			&row.FinishedAt,
			&row.UpdatedAt,
			&row.TargetCount,
			&row.ComponentKind,
		); err != nil {
			return nil, err
		}
		results = append(results, scheduledRunWorkPayload(row))
	}
	return results, rows.Err()
}

func scheduledRunWorkPayload(row scheduledRunWorkRow) map[string]any {
	runID := nullInt(row.RunID)
	jobID := nullInt(row.JobID)
	targetCount := nullInt(row.TargetCount)
	if targetCount <= 0 && cleanText(nullString(row.TargetHostname)) != "" {
		targetCount = 1
	}
	taskType := "Assembly"
	kind := schedulerKindScheduledRun
	switch strings.ToLower(cleanText(nullString(row.ComponentKind))) {
	case "workflow":
		taskType = "Workflow"
		kind = schedulerKindScheduledWorkflowRun
	case "ansible":
		taskType = "Playbook"
	}
	return map[string]any{
		"id":             "scheduled-run:" + strconv.FormatInt(runID, 10),
		"kind":           kind,
		"site_id":        nullableInt(row.SiteID),
		"lane":           schedulerLaneScheduledJob,
		"job_id":         nullableInt(row.JobID),
		"run_id":         nullableInt(row.RunID),
		"target_id":      nil,
		"status":         nullString(row.Status),
		"lease_owner":    "",
		"worker_guid":    "",
		"container_name": "",
		"attempt_count":  int64(0),
		"heartbeat_at":   nullableInt(row.UpdatedAt),
		"started_at":     nullableInt(row.StartedAt),
		"finished_at":    nullableInt(row.FinishedAt),
		"updated_at":     nullableInt(row.UpdatedAt),
		"task_link":      schedulerTaskLink(jobID, runID, "scheduled_job"),
		"target_count":   targetCount,
		"task_type":      taskType,
		"error":          nullString(row.Error),
	}
}

func filterScheduledDispatchWork(workItems []map[string]any, scheduledRunWork []map[string]any) []map[string]any {
	if len(workItems) == 0 || len(scheduledRunWork) == 0 {
		return workItems
	}
	canonicalRunIDs := map[int64]bool{}
	for _, item := range scheduledRunWork {
		if runID := coerceInt64(item["run_id"]); runID > 0 {
			canonicalRunIDs[runID] = true
		}
	}
	if len(canonicalRunIDs) == 0 {
		return workItems
	}
	filtered := make([]map[string]any, 0, len(workItems))
	for _, item := range workItems {
		kind := strings.ToLower(cleanText(item["kind"]))
		runID := coerceInt64(item["run_id"])
		if runID > 0 && canonicalRunIDs[runID] && (kind == schedulerKindScheduledRun || kind == schedulerKindScheduledWorkflowRun) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func collectWorkerReferenceData(ctx context.Context, conn *sql.Conn, collections ...[]map[string]any) (map[int64]string, []map[string]any, map[int64]int64, map[int64]int64, map[int64]string, error) {
	siteNames := map[int64]string{}
	siteDeviceCounts := map[int64]int64{}
	siteOnlineDeviceCounts := map[int64]int64{}
	jobNames := map[int64]string{}
	referencedSiteIDs := map[int64]struct{}{}
	jobIDs := map[int64]struct{}{}
	for _, collection := range collections {
		for _, item := range collection {
			siteID := coerceInt64(item["site_id"])
			if siteID > 0 {
				referencedSiteIDs[siteID] = struct{}{}
			}
			jobID := coerceInt64(item["job_id"])
			if jobID > 0 {
				jobIDs[jobID] = struct{}{}
			}
		}
	}

	rows, err := conn.QueryContext(ctx, "SELECT id, name FROM engine.sites ORDER BY lower(name), id")
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	for rows.Next() {
		var siteID sql.NullInt64
		var siteName sql.NullString
		if err := rows.Scan(&siteID, &siteName); err != nil {
			rows.Close()
			return nil, nil, nil, nil, nil, err
		}
		if siteID.Valid {
			siteNames[siteID.Int64] = firstText(nullString(siteName), "Site "+strconv.FormatInt(siteID.Int64, 10))
		}
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, nil, nil, err
	}
	for siteID := range siteNames {
		referencedSiteIDs[siteID] = struct{}{}
	}

	siteIDs := sortedIntKeys(referencedSiteIDs)
	if len(siteIDs) > 0 {
		placeholders := postgresPlaceholders(1, len(siteIDs))
		params := intsToAny(siteIDs)
		countRows, err := conn.QueryContext(ctx, `
			SELECT site_id, COUNT(DISTINCT device_hostname)
			  FROM engine.device_sites
			 WHERE site_id IN (`+placeholders+`)
		  GROUP BY site_id
		`, params...)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		for countRows.Next() {
			var siteID sql.NullInt64
			var count sql.NullInt64
			if err := countRows.Scan(&siteID, &count); err != nil {
				countRows.Close()
				return nil, nil, nil, nil, nil, err
			}
			if siteID.Valid {
				siteDeviceCounts[siteID.Int64] = nullInt(count)
			}
		}
		if err := countRows.Close(); err != nil {
			return nil, nil, nil, nil, nil, err
		}

		onlineCutoff := time.Now().Unix() - int64(agentOnlineWindowSeconds())
		onlineParams := append(intsToAny(siteIDs), onlineCutoff)
		onlineRows, err := conn.QueryContext(ctx, `
			SELECT ds.site_id, COUNT(DISTINCT d.guid)
			  FROM engine.device_sites ds
			  JOIN engine.devices d ON lower(d.hostname)=lower(ds.device_hostname)
			 WHERE ds.site_id IN (`+placeholders+`)
			   AND d.last_seen IS NOT NULL
			   AND d.last_seen >= $`+strconv.Itoa(len(siteIDs)+1)+`
			   AND COALESCE(NULLIF(d.status, ''), 'active') <> 'purged'
		  GROUP BY ds.site_id
		`, onlineParams...)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		for onlineRows.Next() {
			var siteID sql.NullInt64
			var count sql.NullInt64
			if err := onlineRows.Scan(&siteID, &count); err != nil {
				onlineRows.Close()
				return nil, nil, nil, nil, nil, err
			}
			if siteID.Valid {
				siteOnlineDeviceCounts[siteID.Int64] = nullInt(count)
			}
		}
		if err := onlineRows.Close(); err != nil {
			return nil, nil, nil, nil, nil, err
		}
	}

	jobIDList := sortedIntKeys(jobIDs)
	if len(jobIDList) > 0 {
		rows, err := conn.QueryContext(ctx, `
			SELECT id, name
			  FROM engine.scheduled_jobs
			 WHERE id IN (`+postgresPlaceholders(1, len(jobIDList))+`)
		`, intsToAny(jobIDList)...)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		for rows.Next() {
			var jobID sql.NullInt64
			var jobName sql.NullString
			if err := rows.Scan(&jobID, &jobName); err != nil {
				rows.Close()
				return nil, nil, nil, nil, nil, err
			}
			if jobID.Valid {
				jobNames[jobID.Int64] = cleanText(nullString(jobName))
			}
		}
		if err := rows.Close(); err != nil {
			return nil, nil, nil, nil, nil, err
		}
	}

	siteRows := make([]map[string]any, 0, len(siteNames))
	for _, siteID := range sortedSiteIDsByName(siteNames) {
		siteRows = append(siteRows, map[string]any{
			"id":                  siteID,
			"name":                siteNames[siteID],
			"device_count":        siteDeviceCounts[siteID],
			"online_device_count": siteOnlineDeviceCounts[siteID],
		})
	}
	return siteNames, siteRows, siteDeviceCounts, siteOnlineDeviceCounts, jobNames, nil
}

func enrichWorkerReferences(rows []map[string]any, activeWork []map[string]any, recentWork []map[string]any, siteNames map[int64]string, siteDeviceCounts map[int64]int64, siteOnlineDeviceCounts map[int64]int64, jobNames map[int64]string) {
	for _, collection := range [][]map[string]any{rows, activeWork, recentWork} {
		for _, item := range collection {
			siteID := coerceInt64(item["site_id"])
			if siteID > 0 {
				item["site_name"] = firstText(siteNames[siteID], "Site "+strconv.FormatInt(siteID, 10))
				item["site_device_count"] = siteDeviceCounts[siteID]
				item["site_online_device_count"] = siteOnlineDeviceCounts[siteID]
			}
			jobID := coerceInt64(item["job_id"])
			jobName := jobNames[jobID]
			if jobID > 0 && jobName != "" {
				item["job_name"] = jobName
				if link, ok := item["task_link"].(map[string]any); ok {
					enriched := copyMap(link)
					enriched["job_name"] = jobName
					item["task_link"] = enriched
				}
			}
		}
	}
}

func attachWorkerContainerMetadata(rows []map[string]any) []map[string]any {
	for _, row := range rows {
		containerName := cleanText(row["container_name"])
		if containerName == "" {
			continue
		}
		inspected := dockerInspectContainer(containerName)
		if len(inspected) == 0 {
			continue
		}
		containerID := shortContainerID(cleanText(inspected["Id"]))
		if containerID != "" {
			row["container_id"] = containerID
			row["container_id_full"] = cleanText(inspected["Id"])
		}
		if config, ok := inspected["Config"].(map[string]any); ok {
			if image := cleanText(config["Image"]); image != "" {
				row["container_image"] = image
			}
		}
		applyDockerInspectSizeMetadata(row, inspected)
	}
	attachDockerStatsToRows(rows, func(row map[string]any) string {
		return cleanText(row["container_name"])
	})
	return rows
}

func dockerInspectContainer(containerName string) map[string]any {
	containerName = strings.TrimSpace(containerName)
	if containerName == "" {
		return map[string]any{}
	}
	return dockerProxyJSON("/containers/" + url.PathEscape(containerName) + "/json?size=1")
}

func applyDockerInspectSizeMetadata(row map[string]any, inspected map[string]any) {
	if row == nil || inspected == nil {
		return
	}
	if value, ok := dockerInspectInt64(inspected, "SizeRootFs"); ok {
		row["container_size_rootfs_bytes"] = maxInt64(value, 0)
	}
	if value, ok := dockerInspectInt64(inspected, "SizeRw"); ok {
		row["container_size_rw_bytes"] = maxInt64(value, 0)
	}
	if limit, source := dockerInspectStorageLimit(inspected); limit > 0 {
		row["container_storage_limit_bytes"] = limit
		row["container_storage_limit_source"] = source
	}
}

func dockerInspectInt64(payload map[string]any, key string) (int64, bool) {
	if payload == nil {
		return 0, false
	}
	value, ok := payload[key]
	if !ok {
		return 0, false
	}
	return coerceInt64(value), true
}

func dockerInspectStorageLimit(inspected map[string]any) (int64, string) {
	hostConfig := mapStringAny(inspected["HostConfig"])
	if value, ok := dockerInspectInt64(hostConfig, "DiskQuota"); ok && value > 0 {
		return value, "HostConfig.DiskQuota"
	}
	storageOpt := mapStringAny(hostConfig["StorageOpt"])
	for _, key := range []string{"size", "Size", "dm.basesize", "dm.size"} {
		if value, ok := storageOpt[key]; ok {
			if parsed := parseDockerSizeBytes(value); parsed > 0 {
				return parsed, "HostConfig.StorageOpt." + key
			}
		}
	}
	return 0, ""
}

func parseDockerSizeBytes(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return parsed
		}
		floatParsed, err := typed.Float64()
		if err == nil {
			return int64(floatParsed)
		}
	case string:
		raw := strings.TrimSpace(typed)
		if raw == "" {
			return 0
		}
		upper := strings.ToUpper(strings.ReplaceAll(raw, " ", ""))
		multipliers := []struct {
			suffix string
			value  float64
		}{
			{"KIB", 1024},
			{"MIB", 1024 * 1024},
			{"GIB", 1024 * 1024 * 1024},
			{"TIB", 1024 * 1024 * 1024 * 1024},
			{"KB", 1000},
			{"MB", 1000 * 1000},
			{"GB", 1000 * 1000 * 1000},
			{"TB", 1000 * 1000 * 1000 * 1000},
			{"K", 1024},
			{"M", 1024 * 1024},
			{"G", 1024 * 1024 * 1024},
			{"T", 1024 * 1024 * 1024 * 1024},
			{"B", 1},
		}
		multiplier := float64(1)
		numeric := upper
		for _, unit := range multipliers {
			if strings.HasSuffix(upper, unit.suffix) {
				multiplier = unit.value
				numeric = strings.TrimSuffix(upper, unit.suffix)
				break
			}
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(numeric), 64)
		if err == nil && parsed > 0 {
			return int64(parsed * multiplier)
		}
	}
	return 0
}

func dockerContainerStats(containerName string) map[string]any {
	containerName = strings.TrimSpace(containerName)
	if containerName == "" {
		return map[string]any{}
	}
	raw := dockerProxyJSON("/containers/" + url.PathEscape(containerName) + "/stats?stream=false")
	if len(raw) == 0 {
		return map[string]any{}
	}
	return normalizeDockerContainerStats(raw)
}

func dockerProxyJSON(path string) map[string]any {
	base := dockerProxyBaseURL()
	if base == "" || strings.TrimSpace(path) == "" {
		return map[string]any{}
	}
	client := http.Client{Timeout: 2500 * time.Millisecond}
	requestURL := base + path
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return map[string]any{}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return map[string]any{}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return map[string]any{}
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return map[string]any{}
	}
	return payload
}

func dockerProxyBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("BOREALIS_DOCKER_PROXY_URL")), "/")
}

func attachDockerStatsToRows(rows []map[string]any, containerName func(map[string]any) string) {
	if len(rows) == 0 || dockerProxyBaseURL() == "" {
		return
	}
	type statsResult struct {
		index int
		stats map[string]any
	}
	const maxConcurrentStatsReads = 6
	sem := make(chan struct{}, maxConcurrentStatsReads)
	results := make(chan statsResult, len(rows))
	var wg sync.WaitGroup
	for index, row := range rows {
		name := strings.TrimSpace(containerName(row))
		if name == "" {
			continue
		}
		wg.Add(1)
		go func(rowIndex int, container string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			stats := dockerContainerStats(container)
			if len(stats) > 0 {
				results <- statsResult{index: rowIndex, stats: stats}
			}
		}(index, name)
	}
	wg.Wait()
	close(results)
	for result := range results {
		rows[result.index]["docker_stats"] = result.stats
	}
}

func normalizeDockerContainerStats(payload map[string]any) map[string]any {
	cpuStats := mapStringAny(payload["cpu_stats"])
	precpuStats := mapStringAny(payload["precpu_stats"])
	cpuUsage := mapStringAny(cpuStats["cpu_usage"])
	precpuUsage := mapStringAny(precpuStats["cpu_usage"])
	cpuDelta := coerceFloat64(cpuUsage["total_usage"]) - coerceFloat64(precpuUsage["total_usage"])
	systemDelta := coerceFloat64(cpuStats["system_cpu_usage"]) - coerceFloat64(precpuStats["system_cpu_usage"])
	onlineCPUs := coerceFloat64(cpuStats["online_cpus"])
	if onlineCPUs <= 0 {
		if perCPU, ok := cpuUsage["percpu_usage"].([]any); ok {
			onlineCPUs = float64(len(perCPU))
		}
	}
	cpuPercent := float64(0)
	if cpuDelta > 0 && systemDelta > 0 && onlineCPUs > 0 {
		cpuPercent = round2((cpuDelta / systemDelta) * onlineCPUs * 100)
	}

	memoryStats := mapStringAny(payload["memory_stats"])
	memoryUsage := coerceInt64(memoryStats["usage"])
	memoryLimit := coerceInt64(memoryStats["limit"])
	memoryStatValues := mapStringAny(memoryStats["stats"])
	memoryCache := firstPositiveDockerStatInt64(
		coerceInt64(memoryStatValues["total_inactive_file"]),
		coerceInt64(memoryStatValues["inactive_file"]),
		coerceInt64(memoryStatValues["cache"]),
	)
	if memoryCache > 0 && memoryUsage > memoryCache {
		memoryUsage -= memoryCache
	}
	memoryPercent := float64(0)
	if memoryLimit > 0 && memoryUsage >= 0 {
		memoryPercent = round2((float64(memoryUsage) / float64(memoryLimit)) * 100)
	}

	var networkInput int64
	var networkOutput int64
	if networks, ok := payload["networks"].(map[string]any); ok {
		for _, networkValue := range networks {
			networkPayload := mapStringAny(networkValue)
			networkInput += coerceInt64(networkPayload["rx_bytes"])
			networkOutput += coerceInt64(networkPayload["tx_bytes"])
		}
	}

	var blockInput int64
	var blockOutput int64
	blkioStats := mapStringAny(payload["blkio_stats"])
	if entries, ok := blkioStats["io_service_bytes_recursive"].([]any); ok {
		for _, entryValue := range entries {
			entry := mapStringAny(entryValue)
			switch strings.ToLower(cleanText(entry["op"])) {
			case "read":
				blockInput += coerceInt64(entry["value"])
			case "write":
				blockOutput += coerceInt64(entry["value"])
			}
		}
	}

	pidsStats := mapStringAny(payload["pids_stats"])
	result := map[string]any{
		"cpu_percent":        cpuPercent,
		"memory_usage_bytes": maxInt64(memoryUsage, 0),
		"memory_limit_bytes": maxInt64(memoryLimit, 0),
		"memory_percent":     memoryPercent,
		"net_input_bytes":    maxInt64(networkInput, 0),
		"net_output_bytes":   maxInt64(networkOutput, 0),
		"block_input_bytes":  maxInt64(blockInput, 0),
		"block_output_bytes": maxInt64(blockOutput, 0),
		"pids":               maxInt64(coerceInt64(pidsStats["current"]), 0),
	}
	if readAt := cleanText(payload["read"]); readAt != "" {
		result["read_at"] = readAt
	}
	return result
}

func firstPositiveDockerStatInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func coerceFloat64(value any) float64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	case json.Number:
		parsed, err := typed.Float64()
		if err == nil {
			return parsed
		}
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}

func payloadTargetCount(payload map[string]any) any {
	for _, key := range []string{"target_row_ids", "targets"} {
		values, ok := payload[key].([]any)
		if !ok {
			continue
		}
		count := 0
		for _, value := range values {
			if value != nil {
				count++
			}
		}
		return count
	}
	return nil
}

func payloadTaskType(kind string, payload map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "onboarding_run":
		return "Onboarding"
	case "scheduled_workflow_run":
		return "Workflow"
	case "agent_maintenance_run":
		return "Agent Maintenance"
	case "scheduled_run":
		hasScripts := len(jsonArray(payload["script_components"])) > 0
		hasAnsible := len(jsonArray(payload["ansible_components"])) > 0
		if hasAnsible && !hasScripts {
			return "Playbook"
		}
		return "Assembly"
	default:
		return ""
	}
}

func parseJSONText(raw sql.NullString, fallback any) any {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return fallback
	}
	var value any
	if err := json.Unmarshal([]byte(raw.String), &value); err != nil || value == nil {
		return fallback
	}
	return value
}

func jsonArray(value any) []any {
	items, ok := value.([]any)
	if !ok {
		return []any{}
	}
	return items
}

func nullableInt(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}
	return nil
}

func copyMap(source map[string]any) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func stringifyIntMap(values map[int64]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[strconv.FormatInt(key, 10)] = value
	}
	return out
}

func stringifyCountMap(values map[int64]int64) map[string]int64 {
	out := make(map[string]int64, len(values))
	for key, value := range values {
		out[strconv.FormatInt(key, 10)] = value
	}
	return out
}

func sortedIntKeys[T any](values map[int64]T) []int64 {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedSiteIDsByName(siteNames map[int64]string) []int64 {
	keys := sortedIntKeys(siteNames)
	sort.Slice(keys, func(i, j int) bool {
		left := strings.ToLower(siteNames[keys[i]])
		right := strings.ToLower(siteNames[keys[j]])
		if left == right {
			return keys[i] < keys[j]
		}
		return left < right
	})
	return keys
}

func intsToAny(values []int64) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func postgresPlaceholders(start int, count int) string {
	parts := make([]string, 0, count)
	for idx := 0; idx < count; idx++ {
		parts = append(parts, "$"+strconv.Itoa(start+idx))
	}
	return strings.Join(parts, ",")
}

func shortContainerID(value string) string {
	text := strings.TrimSpace(value)
	if strings.HasPrefix(text, "sha256:") {
		text = strings.SplitN(text, ":", 2)[1]
	}
	if len(text) > 12 {
		return text[:12]
	}
	return text
}

func agentOnlineWindowSeconds() int {
	return parseEnvIntMin("BOREALIS_AGENT_ONLINE_WINDOW_SECONDS", 300, 60)
}

func parseEnvIntMin(name string, fallback int, minimum int) int {
	return maxInt(parseIntDefault(os.Getenv(name), fallback), minimum)
}

func parseIntDefault(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func clampInt(value int, minimum int, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func maxInt(value int, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}
