package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type activityHistoryStore interface {
	listDeviceActivity(ctx context.Context, profile operatorProfile, hostname string) (map[string]any, int, error)
	deleteDeviceActivity(ctx context.Context, profile operatorProfile, hostname string) (map[string]any, int, error)
	getActivityJob(ctx context.Context, profile operatorProfile, activityID int64) (map[string]any, int, error)
}

func registerActivityRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("/api/device/activity/", deviceActivityHandler(auth))
}

func deviceActivityHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, store, ok := activityRequestContext(w, r, auth)
		if !ok {
			return
		}
		rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/device/activity/"), "/")
		if strings.HasPrefix(rest, "job/") {
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, http.MethodGet)
				return
			}
			rawID := strings.Trim(strings.TrimPrefix(rest, "job/"), "/")
			activityID, err := strconv.ParseInt(rawID, 10, 64)
			if err != nil || activityID <= 0 {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "Not found"})
				return
			}
			ctx, cancel := activityTimeoutContext(r.Context(), auth)
			defer cancel()
			payload, status, err := store.getActivityJob(ctx, profile, activityID)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, status, payload)
			return
		}
		hostname := cleanText(rest)
		if hostname == "" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "Not found"})
			return
		}
		ctx, cancel := activityTimeoutContext(r.Context(), auth)
		defer cancel()
		switch r.Method {
		case http.MethodGet:
			payload, status, err := store.listDeviceActivity(ctx, profile, hostname)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, status, payload)
		case http.MethodDelete:
			payload, status, err := store.deleteDeviceActivity(ctx, profile, hostname)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, status, payload)
		default:
			writeMethodNotAllowed(w, "GET, DELETE")
		}
	}
}

func activityRequestContext(w http.ResponseWriter, r *http.Request, auth *authService) (operatorProfile, activityHistoryStore, bool) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			unauthorizedAuthFailure().write(w)
			return operatorProfile{}, nil, false
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return operatorProfile{}, nil, false
	}
	store, ok := auth.store.(activityHistoryStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "activity_history_unavailable"})
		return operatorProfile{}, nil, false
	}
	return profile, store, true
}

func activityTimeoutContext(ctx context.Context, auth *authService) (context.Context, context.CancelFunc) {
	timeout := auth.timeout
	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

type activityHistoryRow struct {
	ID           sql.NullInt64
	Hostname     sql.NullString
	ScriptName   sql.NullString
	ScriptPath   sql.NullString
	ScriptType   sql.NullString
	RanAt        sql.NullInt64
	Status       sql.NullString
	Stdout       sql.NullString
	Stderr       sql.NullString
	StdoutLen    sql.NullInt64
	StderrLen    sql.NullInt64
	QueueLane    sql.NullString
	ActivityKind sql.NullString
	MetadataJSON sql.NullString
	StartedAt    sql.NullInt64
	UpdatedAt    sql.NullInt64
	FinishedAt   sql.NullInt64
}

func (s *postgresOperatorStore) listDeviceActivity(ctx context.Context, profile operatorProfile, hostname string) (map[string]any, int, error) {
	if ok, err := s.profileCanAccessHostname(ctx, profile, hostname); err != nil {
		return nil, 0, err
	} else if !ok {
		return map[string]any{"error": "Not found"}, http.StatusNotFound, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT id, hostname, script_name, script_path, script_type, ran_at, status,
		       COALESCE(LENGTH(stdout), 0), COALESCE(LENGTH(stderr), 0),
		       queue_lane, activity_kind, metadata_json, started_at, updated_at, finished_at
		  FROM engine.activity_history
		 WHERE hostname = $1
		 ORDER BY CASE
		            WHEN LOWER(COALESCE(status, '')) IN ('queued', 'running', 'pending', 'created', 'started', 'in_progress')
		            THEN 0
		            ELSE 1
		          END ASC,
		          COALESCE(updated_at, started_at, ran_at, 0) DESC,
		          id DESC
	`, hostname)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	history := make([]map[string]any, 0)
	for rows.Next() {
		var row activityHistoryRow
		if err := rows.Scan(
			&row.ID,
			&row.Hostname,
			&row.ScriptName,
			&row.ScriptPath,
			&row.ScriptType,
			&row.RanAt,
			&row.Status,
			&row.StdoutLen,
			&row.StderrLen,
			&row.QueueLane,
			&row.ActivityKind,
			&row.MetadataJSON,
			&row.StartedAt,
			&row.UpdatedAt,
			&row.FinishedAt,
		); err != nil {
			return nil, 0, err
		}
		history = append(history, activityHistorySummaryPayload(row))
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return map[string]any{"history": history}, http.StatusOK, nil
}

func (s *postgresOperatorStore) deleteDeviceActivity(ctx context.Context, profile operatorProfile, hostname string) (map[string]any, int, error) {
	if ok, err := s.profileCanAccessHostname(ctx, profile, hostname); err != nil {
		return nil, 0, err
	} else if !ok {
		return map[string]any{"error": "Not found"}, http.StatusNotFound, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "DELETE FROM engine.activity_history WHERE hostname = $1", hostname); err != nil {
		return nil, 0, err
	}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func (s *postgresOperatorStore) getActivityJob(ctx context.Context, profile operatorProfile, activityID int64) (map[string]any, int, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	var row activityHistoryRow
	err = conn.QueryRowContext(ctx, `
		SELECT id, hostname, script_name, script_path, script_type, ran_at, status,
		       stdout, stderr, queue_lane, activity_kind, metadata_json, started_at, updated_at, finished_at
		  FROM engine.activity_history
		 WHERE id = $1
	`, activityID).Scan(
		&row.ID,
		&row.Hostname,
		&row.ScriptName,
		&row.ScriptPath,
		&row.ScriptType,
		&row.RanAt,
		&row.Status,
		&row.Stdout,
		&row.Stderr,
		&row.QueueLane,
		&row.ActivityKind,
		&row.MetadataJSON,
		&row.StartedAt,
		&row.UpdatedAt,
		&row.FinishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"error": "Not found"}, http.StatusNotFound, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if ok, err := s.profileCanAccessHostname(ctx, profile, nullString(row.Hostname)); err != nil {
		return nil, 0, err
	} else if !ok {
		return map[string]any{"error": "Not found"}, http.StatusNotFound, nil
	}
	return activityHistoryDetailPayload(row), http.StatusOK, nil
}

func (s *postgresOperatorStore) profileCanAccessHostname(ctx context.Context, profile operatorProfile, hostname string) (bool, error) {
	hostname = cleanText(hostname)
	if hostname == "" {
		return false, nil
	}
	if strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		return true, nil
	}
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return false, err
	}
	if len(allowedSiteIDs) == 0 {
		return false, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	placeholders := make([]string, 0, len(allowedSiteIDs))
	params := make([]any, 0, len(allowedSiteIDs)+1)
	params = append(params, hostname)
	for _, siteID := range allowedSiteIDs {
		params = append(params, siteID)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(params)))
	}
	var count int
	err = conn.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		   FROM engine.devices AS d
		   JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
		  WHERE LOWER(d.hostname) = LOWER($1)
		    AND ds.site_id IN (`+strings.Join(placeholders, ",")+`)`,
		params...,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func activityHistorySummaryPayload(row activityHistoryRow) map[string]any {
	return map[string]any{
		"id":            nullInt(row.ID),
		"hostname":      nullString(row.Hostname),
		"script_name":   nullString(row.ScriptName),
		"script_path":   nullString(row.ScriptPath),
		"script_type":   nullString(row.ScriptType),
		"ran_at":        nullInt(row.RanAt),
		"status":        normalizeActivityStatus(nullString(row.Status), cleanText(firstNonEmpty(nullString(row.Status), "Unknown"))),
		"has_stdout":    nullInt(row.StdoutLen) > 0,
		"has_stderr":    nullInt(row.StderrLen) > 0,
		"queue_lane":    nullString(row.QueueLane),
		"activity_kind": nullString(row.ActivityKind),
		"metadata":      parseActivityMetadata(nullString(row.MetadataJSON)),
		"started_at":    nullInt(row.StartedAt),
		"updated_at":    nullInt(row.UpdatedAt),
		"finished_at":   nullInt(row.FinishedAt),
	}
}

func activityHistoryDetailPayload(row activityHistoryRow) map[string]any {
	return map[string]any{
		"id":            nullInt(row.ID),
		"hostname":      nullString(row.Hostname),
		"script_name":   nullString(row.ScriptName),
		"script_path":   nullString(row.ScriptPath),
		"script_type":   nullString(row.ScriptType),
		"ran_at":        nullInt(row.RanAt),
		"status":        normalizeActivityStatus(nullString(row.Status), cleanText(firstNonEmpty(nullString(row.Status), "Unknown"))),
		"stdout":        nullString(row.Stdout),
		"stderr":        nullString(row.Stderr),
		"queue_lane":    nullString(row.QueueLane),
		"activity_kind": nullString(row.ActivityKind),
		"metadata":      parseActivityMetadata(nullString(row.MetadataJSON)),
		"started_at":    nullInt(row.StartedAt),
		"updated_at":    nullInt(row.UpdatedAt),
		"finished_at":   nullInt(row.FinishedAt),
	}
}

func normalizeActivityStatus(value string, fallback string) string {
	text := cleanText(value)
	if text == "" {
		return fallback
	}
	key := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(text), "-", "_"), " ", "_")
	switch key {
	case "queued", "pending", "created":
		return "Queued"
	case "running", "started", "in_progress":
		return "Running"
	case "success", "completed", "complete":
		return "Success"
	case "failed", "error":
		return "Failed"
	case "timed_out", "timeout":
		return "Timed Out"
	case "skipped":
		return "Skipped"
	default:
		return text
	}
}

func parseActivityMetadata(raw string) map[string]any {
	text := cleanText(raw)
	if text == "" {
		return map[string]any{}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return map[string]any{}
	}
	if parsed == nil {
		return map[string]any{}
	}
	return parsed
}
