package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	scheduledStatusPending  = "Pending"
	scheduledStatusRunning  = "Running"
	scheduledStatusSuccess  = "Success"
	scheduledStatusWarning  = "Warning"
	scheduledStatusFailed   = "Failed"
	scheduledStatusExpired  = "Expired"
	scheduledStatusTimedOut = "Timed Out"
	scheduledStatusSkipped  = "Skipped"

	scheduledJobKindAutomation       = "automation"
	scheduledJobKindOnboarding       = "onboarding"
	scheduledJobKindAgentMaintenance = "agent_maintenance"
	scheduledSkipNoTargets           = "no_devices_targeted"
	defaultOnboardingSSHPort         = 22
	defaultScheduledRunHistoryDays   = 30
)

type scheduledJobStore interface {
	listScheduledJobs(ctx context.Context, profile operatorProfile, filter scheduledJobListFilter) ([]map[string]any, error)
	getScheduledJob(ctx context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error)
	toggleScheduledJob(ctx context.Context, profile operatorProfile, jobID int64, enabled bool) (map[string]any, int, error)
	deleteScheduledJob(ctx context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error)
	listScheduledJobRuns(ctx context.Context, profile operatorProfile, jobID int64, days int) (map[string]any, int, error)
	clearScheduledJobRuns(ctx context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error)
	listScheduledJobDevices(ctx context.Context, profile operatorProfile, jobID int64, occurrence *int64) (map[string]any, int, error)
	listOnboardingJobTargets(ctx context.Context, profile operatorProfile, jobID int64, occurrence *int64) (map[string]any, int, error)
}

type scheduledJobListFilter struct {
	SiteID *int64
}

type scheduledJobRow struct {
	ID                  sql.NullInt64
	Name                sql.NullString
	ComponentsJSON      sql.NullString
	TargetsJSON         sql.NullString
	ScheduleType        sql.NullString
	StartTS             sql.NullInt64
	DurationStopEnabled sql.NullInt64
	Expiration          sql.NullString
	ExecutionContext    sql.NullString
	CredentialID        sql.NullInt64
	UseServiceAccount   sql.NullInt64
	Enabled             sql.NullInt64
	CreatedAt           sql.NullInt64
	UpdatedAt           sql.NullInt64
	JobKind             sql.NullString
}

type scheduledRunRow struct {
	ID              sql.NullInt64
	TargetHostname  sql.NullString
	ScheduledTS     sql.NullInt64
	StartedTS       sql.NullInt64
	FinishedTS      sql.NullInt64
	Status          sql.NullString
	Error           sql.NullString
	SkipReason      sql.NullString
	SharedExecution sql.NullInt64
	ComponentIndex  sql.NullInt64
	ComponentKind   sql.NullString
	ComponentName   sql.NullString
	WorkflowRunID   sql.NullInt64
}

type scheduledTargetRow struct {
	ID                        sql.NullInt64
	RunID                     sql.NullInt64
	DeviceGUID                sql.NullString
	Hostname                  sql.NullString
	SiteID                    sql.NullInt64
	SiteName                  sql.NullString
	InventoryHostname         sql.NullString
	WireGuardPeerIP           sql.NullString
	ResolvedConnection        sql.NullString
	ResolutionStatus          sql.NullString
	ResolutionReason          sql.NullString
	ResolvedFromFilterIDsJSON sql.NullString
	RunStatus                 sql.NullString
	StartedTS                 sql.NullInt64
	FinishedTS                sql.NullInt64
	SkipReason                sql.NullString
	SharedExecution           sql.NullInt64
	ComponentName             sql.NullString
}

type onboardingTargetRow struct {
	ID                sql.NullInt64
	RunID             sql.NullInt64
	JobID             sql.NullInt64
	ScheduledTS       sql.NullInt64
	SiteID            sql.NullInt64
	TargetInput       sql.NullString
	TargetAddress     sql.NullString
	TargetHostname    sql.NullString
	SSHPort           sql.NullInt64
	Status            sql.NullString
	Detail            sql.NullString
	StdoutSnippet     sql.NullString
	StderrSnippet     sql.NullString
	ApprovalReference sql.NullString
	CreatedAt         sql.NullInt64
	UpdatedAt         sql.NullInt64
	FinishedAt        sql.NullInt64
}

func registerScheduledJobRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/scheduled_jobs", scheduledJobsRootHandler(auth, fallback))
	mux.HandleFunc("/api/scheduled_jobs/", scheduledJobsSubtreeHandler(auth, fallback))
	mux.HandleFunc("/api/onboarding/jobs/", onboardingJobsSubtreeHandler(auth, fallback))
}

func scheduledJobsRootHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimRight(r.URL.Path, "/") == "/api/scheduled_jobs" && r.Method == http.MethodGet {
			scheduledJobList(w, r, auth)
			return
		}
		fallback.ServeHTTP(w, r)
	}
}

func scheduledJobsSubtreeHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/scheduled_jobs/"), "/"), "/")
		if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodGet {
			scheduledJobDetail(w, r, auth, parts[0])
			return
		}
		if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodDelete {
			scheduledJobDelete(w, r, auth, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "toggle" && r.Method == http.MethodPost {
			scheduledJobToggle(w, r, auth, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "runs" && r.Method == http.MethodGet {
			scheduledJobRuns(w, r, auth, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "runs" && r.Method == http.MethodDelete {
			scheduledJobRunsClear(w, r, auth, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "devices" && r.Method == http.MethodGet {
			scheduledJobDevices(w, r, auth, parts[0])
			return
		}
		fallback.ServeHTTP(w, r)
	}
}

func onboardingJobsSubtreeHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/onboarding/jobs/"), "/"), "/")
		if len(parts) == 2 && parts[1] == "targets" && r.Method == http.MethodGet {
			onboardingJobTargets(w, r, auth, parts[0])
			return
		}
		fallback.ServeHTTP(w, r)
	}
}

func scheduledJobList(w http.ResponseWriter, r *http.Request, auth *authService) {
	profile, store, ok := scheduledJobRequestContext(w, r, auth)
	if !ok {
		return
	}
	filter := scheduledJobListFilter{
		SiteID: parseOptionalPositiveInt64(cleanText(firstNonEmpty(r.URL.Query().Get("site"), r.URL.Query().Get("site_id")))),
	}
	ctx, cancel := scheduledJobTimeoutContext(r.Context(), auth)
	defer cancel()
	jobs, err := store.listScheduledJobs(ctx, profile, filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func scheduledJobDetail(w http.ResponseWriter, r *http.Request, auth *authService, jobIDText string) {
	profile, store, ok := scheduledJobRequestContext(w, r, auth)
	if !ok {
		return
	}
	jobID, err := parsePositivePathInt(jobIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	ctx, cancel := scheduledJobTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.getScheduledJob(ctx, profile, jobID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, payload)
}

func scheduledJobToggle(w http.ResponseWriter, r *http.Request, auth *authService, jobIDText string) {
	profile, store, ok := scheduledJobRequestContext(w, r, auth)
	if !ok {
		return
	}
	jobID, err := parsePositivePathInt(jobIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return
	}
	enabled := true
	if raw, exists := body["enabled"]; exists {
		enabled = boolFromAny(raw)
	}
	ctx, cancel := scheduledJobTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.toggleScheduledJob(ctx, profile, jobID, enabled)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, payload)
}

func scheduledJobDelete(w http.ResponseWriter, r *http.Request, auth *authService, jobIDText string) {
	profile, store, ok := scheduledJobRequestContext(w, r, auth)
	if !ok {
		return
	}
	jobID, err := parsePositivePathInt(jobIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	ctx, cancel := scheduledJobTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.deleteScheduledJob(ctx, profile, jobID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, payload)
}

func scheduledJobRuns(w http.ResponseWriter, r *http.Request, auth *authService, jobIDText string) {
	profile, store, ok := scheduledJobRequestContext(w, r, auth)
	if !ok {
		return
	}
	jobID, err := parsePositivePathInt(jobIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	days := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		days = coerceInt64(raw)
	}
	if days <= 0 {
		days = defaultScheduledRunHistoryDays
	}
	ctx, cancel := scheduledJobTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.listScheduledJobRuns(ctx, profile, jobID, int(days))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, payload)
}

func scheduledJobRunsClear(w http.ResponseWriter, r *http.Request, auth *authService, jobIDText string) {
	profile, store, ok := scheduledJobRequestContext(w, r, auth)
	if !ok {
		return
	}
	jobID, err := parsePositivePathInt(jobIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	ctx, cancel := scheduledJobTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.clearScheduledJobRuns(ctx, profile, jobID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, payload)
}

func scheduledJobDevices(w http.ResponseWriter, r *http.Request, auth *authService, jobIDText string) {
	profile, store, ok := scheduledJobRequestContext(w, r, auth)
	if !ok {
		return
	}
	jobID, err := parsePositivePathInt(jobIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	occurrence := parseOptionalPositiveInt64(r.URL.Query().Get("occurrence"))
	ctx, cancel := scheduledJobTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.listScheduledJobDevices(ctx, profile, jobID, occurrence)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, payload)
}

func onboardingJobTargets(w http.ResponseWriter, r *http.Request, auth *authService, jobIDText string) {
	profile, store, ok := scheduledJobRequestContext(w, r, auth)
	if !ok {
		return
	}
	jobID, err := parsePositivePathInt(jobIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	occurrence := parseOptionalPositiveInt64(r.URL.Query().Get("occurrence"))
	ctx, cancel := scheduledJobTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.listOnboardingJobTargets(ctx, profile, jobID, occurrence)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, status, payload)
}

func scheduledJobRequestContext(w http.ResponseWriter, r *http.Request, auth *authService) (operatorProfile, scheduledJobStore, bool) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			unauthorizedAuthFailure().write(w)
			return operatorProfile{}, nil, false
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return operatorProfile{}, nil, false
	}
	store, ok := auth.store.(scheduledJobStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "scheduled_jobs_unavailable"})
		return operatorProfile{}, nil, false
	}
	return profile, store, true
}

func scheduledJobTimeoutContext(ctx context.Context, auth *authService) (context.Context, context.CancelFunc) {
	timeout := auth.timeout
	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func (s *postgresOperatorStore) listScheduledJobs(ctx context.Context, profile operatorProfile, filter scheduledJobListFilter) ([]map[string]any, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if filter.SiteID != nil && !siteIDVisible(*filter.SiteID, allowedSiteIDs) {
		return []map[string]any{}, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := queryScheduledJobRows(ctx, conn, nil)
	if err != nil {
		return nil, err
	}
	jobs := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		targets := parseJSONArray(row.TargetsJSON)
		visible, err := scheduledJobTargetsVisible(ctx, conn, targets, allowedSiteIDs)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		if filter.SiteID != nil {
			matches, err := scheduledJobTargetsMatchSite(ctx, conn, targets, *filter.SiteID)
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}
		}
		payload, err := scheduledJobPayload(ctx, conn, row)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, payload)
	}
	return jobs, nil
}

func (s *postgresOperatorStore) getScheduledJob(ctx context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := queryScheduledJobRows(ctx, conn, &jobID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if len(rows) == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	targets := parseJSONArray(rows[0].TargetsJSON)
	visible, err := scheduledJobTargetsVisible(ctx, conn, targets, allowedSiteIDs)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !visible {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	payload, err := scheduledJobPayload(ctx, conn, rows[0])
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{"job": payload}, http.StatusOK, nil
}

func (s *postgresOperatorStore) toggleScheduledJob(ctx context.Context, profile operatorProfile, jobID int64, enabled bool) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	job, found, err := loadScheduledJobVisibility(ctx, conn, jobID, allowedSiteIDs)
	if err != nil || !found {
		return map[string]any{"error": "not found"}, http.StatusNotFound, err
	}
	if enabled {
		warning, err := scheduledCredentialResetWarning(ctx, conn, job.CredentialID)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		if warning != nil {
			return map[string]any{
				"error":   warning["warning_code"],
				"message": warning["warning_message"],
			}, http.StatusConflict, nil
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer rollbackQuietly(tx)

	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	result, err := tx.ExecContext(ctx, "UPDATE engine.scheduled_jobs SET enabled=$1, updated_at=$2 WHERE id=$3", enabledInt, time.Now().Unix(), jobID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if updated == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	rows, err := queryScheduledJobRows(ctx, conn, &jobID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if len(rows) == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	payload, err := scheduledJobPayload(ctx, conn, rows[0])
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{"job": payload}, http.StatusOK, nil
}

func (s *postgresOperatorStore) deleteScheduledJob(ctx context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	if _, found, err := loadScheduledJobVisibility(ctx, conn, jobID, allowedSiteIDs); err != nil || !found {
		return map[string]any{"error": "not found"}, http.StatusNotFound, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer rollbackQuietly(tx)
	result, err := tx.ExecContext(ctx, "DELETE FROM engine.scheduled_jobs WHERE id=$1", jobID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if deleted == 0 {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func (s *postgresOperatorStore) listScheduledJobRuns(ctx context.Context, profile operatorProfile, jobID int64, days int) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	job, found, err := loadScheduledJobVisibility(ctx, conn, jobID, allowedSiteIDs)
	if err != nil || !found {
		return map[string]any{"error": "not found"}, http.StatusNotFound, err
	}
	_ = job
	if days <= 0 {
		days = defaultScheduledRunHistoryDays
	}
	cutoff := time.Now().Unix() - int64(days*86400)
	rows, err := conn.QueryContext(ctx, `
		SELECT id, scheduled_ts, started_ts, finished_ts, status, error, skip_reason, target_hostname,
		       shared_execution, component_index, component_kind, component_name, workflow_run_id
		  FROM engine.scheduled_job_runs
		 WHERE job_id=$1 AND COALESCE(finished_ts, started_ts, scheduled_ts, 0) >= $2
	  ORDER BY COALESCE(started_ts, scheduled_ts, 0) DESC, id DESC
		 LIMIT 500
	`, jobID, cutoff)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer rows.Close()
	runs := []map[string]any{}
	for rows.Next() {
		var row scheduledRunRow
		if err := rows.Scan(&row.ID, &row.ScheduledTS, &row.StartedTS, &row.FinishedTS, &row.Status, &row.Error, &row.SkipReason, &row.TargetHostname, &row.SharedExecution, &row.ComponentIndex, &row.ComponentKind, &row.ComponentName, &row.WorkflowRunID); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		runs = append(runs, scheduledRunPayload(row))
	}
	if err := rows.Err(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{"runs": runs}, http.StatusOK, nil
}

func (s *postgresOperatorStore) clearScheduledJobRuns(ctx context.Context, profile operatorProfile, jobID int64) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	if _, found, err := loadScheduledJobVisibility(ctx, conn, jobID, allowedSiteIDs); err != nil || !found {
		return map[string]any{"error": "not found"}, http.StatusNotFound, err
	}
	var latest sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT MAX(scheduled_ts) FROM engine.scheduled_job_runs WHERE job_id=$1", jobID).Scan(&latest); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !latest.Valid {
		return map[string]any{"status": "ok", "cleared": int64(0)}, http.StatusOK, nil
	}
	rows, err := conn.QueryContext(ctx, "SELECT id FROM engine.scheduled_job_runs WHERE job_id=$1 AND COALESCE(scheduled_ts, 0) < $2", jobID, latest.Int64)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	oldRunIDs := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, http.StatusInternalServerError, err
		}
		oldRunIDs = append(oldRunIDs, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, http.StatusInternalServerError, err
	}
	if err := rows.Close(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if len(oldRunIDs) == 0 {
		return map[string]any{"status": "ok", "cleared": int64(0), "kept_occurrence": latest.Int64}, http.StatusOK, nil
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer rollbackQuietly(tx)
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.scheduled_job_run_activity WHERE run_id = ANY($1)", pq.Array(oldRunIDs)); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.scheduled_job_run_targets WHERE run_id = ANY($1)", pq.Array(oldRunIDs)); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.scheduled_job_onboarding_target_events WHERE run_id = ANY($1)", pq.Array(oldRunIDs)); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM engine.scheduled_job_onboarding_targets WHERE run_id = ANY($1)", pq.Array(oldRunIDs)); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM engine.scheduled_job_runs WHERE id = ANY($1)", pq.Array(oldRunIDs))
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	cleared, err := result.RowsAffected()
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{"status": "ok", "cleared": cleared, "kept_occurrence": latest.Int64}, http.StatusOK, nil
}

func (s *postgresOperatorStore) listScheduledJobDevices(ctx context.Context, profile operatorProfile, jobID int64, occurrence *int64) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	job, found, err := loadScheduledJobVisibility(ctx, conn, jobID, allowedSiteIDs)
	if err != nil || !found {
		return map[string]any{"error": "not found"}, http.StatusNotFound, err
	}
	occ, err := resolveScheduledJobOccurrence(ctx, conn, jobID, occurrence)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	jobKind := normalizeScheduledJobKind(nullString(job.JobKind))
	if jobKind == scheduledJobKindOnboarding {
		rows, err := loadOnboardingTargetRows(ctx, conn, jobID, occ)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		devices := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			devices = append(devices, onboardingTargetDevicePayload(row))
		}
		return map[string]any{"occurrence": occAny(occ), "devices": devices, "job_kind": scheduledJobKindOnboarding}, http.StatusOK, nil
	}
	runs, targets, err := loadOccurrenceForJob(ctx, conn, jobID, occ)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	aggregated := aggregateScheduledDevices(runs, targets)
	if len(aggregated) == 0 {
		aggregated = scheduledDevicesFromSavedTargets(parseJSONArray(job.TargetsJSON))
	}
	runIDs := scheduledDeviceRunIDs(aggregated)
	activities, err := loadScheduledRunActivities(ctx, conn, runIDs)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	online, err := loadOnlineHostnames(ctx, conn)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	devices := make([]map[string]any, 0, len(aggregated))
	for _, rec := range aggregated {
		hostKey := strings.ToLower(cleanText(rec["hostname"]))
		acts := scheduledActivitiesForRuns(rec["run_ids"], activities)
		devices = append(devices, map[string]any{
			"hostname":            cleanText(rec["hostname"]),
			"online":              online[hostKey],
			"site_id":             rec["site_id"],
			"site_name":           cleanText(rec["site_name"]),
			"site":                cleanText(rec["site_name"]),
			"inventory_hostname":  cleanText(rec["inventory_hostname"]),
			"wireguard_peer_ip":   cleanText(rec["wireguard_peer_ip"]),
			"resolved_connection": cleanText(rec["resolved_connection"]),
			"resolution_status":   cleanText(rec["resolution_status"]),
			"resolution_reason":   cleanText(rec["resolution_reason"]),
			"ran_on":              firstPresentAny(rec["finished_ts"], rec["started_ts"]),
			"job_status":          firstText(cleanText(rec["status"]), scheduledStatusPending),
			"has_stdout":          scheduledActivitiesHave(acts, "has_stdout"),
			"has_stderr":          scheduledActivitiesHave(acts, "has_stderr"),
			"activities":          acts,
		})
	}
	sort.SliceStable(devices, func(i, j int) bool {
		return strings.ToLower(cleanText(devices[i]["hostname"])) < strings.ToLower(cleanText(devices[j]["hostname"]))
	})
	return map[string]any{"occurrence": occAny(occ), "devices": devices}, http.StatusOK, nil
}

func (s *postgresOperatorStore) listOnboardingJobTargets(ctx context.Context, profile operatorProfile, jobID int64, occurrence *int64) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	job, found, err := loadScheduledJobVisibility(ctx, conn, jobID, allowedSiteIDs)
	if err != nil || !found {
		return map[string]any{"error": "not found"}, http.StatusNotFound, err
	}
	if normalizeScheduledJobKind(nullString(job.JobKind)) != scheduledJobKindOnboarding {
		return map[string]any{"error": "not onboarding job"}, http.StatusBadRequest, nil
	}
	occ, err := resolveScheduledJobOccurrence(ctx, conn, jobID, occurrence)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	rows, err := loadOnboardingTargetRows(ctx, conn, jobID, occ)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	targetIDs := []int64{}
	for _, row := range rows {
		targetIDs = append(targetIDs, nullInt(row.ID))
	}
	timeline, err := loadOnboardingTargetEvents(ctx, conn, targetIDs)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	approvals, err := loadOnboardingApprovalLookup(ctx, conn, rows)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	targets := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		payload := onboardingTargetPayload(row)
		payload["timeline"] = timeline[nullInt(row.ID)]
		payload["events"] = timeline[nullInt(row.ID)]
		if approval := approvalForOnboardingTarget(row, approvals); approval != nil {
			payload["approval_id"] = approval["id"]
			payload["approval_status"] = approval["status"]
		}
		targets = append(targets, payload)
	}
	return map[string]any{"occurrence": occAny(occ), "targets": targets}, http.StatusOK, nil
}

func queryScheduledJobRows(ctx context.Context, conn *sql.Conn, jobID *int64) ([]scheduledJobRow, error) {
	where := ""
	params := []any{}
	if jobID != nil {
		where = "WHERE id=$1"
		params = append(params, *jobID)
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT id, name, components_json, targets_json, schedule_type, start_ts,
		       duration_stop_enabled, expiration, execution_context, credential_id,
		       use_service_account, enabled, created_at, updated_at, job_kind
		  FROM engine.scheduled_jobs
		  `+where+`
	  ORDER BY created_at DESC
	`, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []scheduledJobRow{}
	for rows.Next() {
		var row scheduledJobRow
		if err := rows.Scan(&row.ID, &row.Name, &row.ComponentsJSON, &row.TargetsJSON, &row.ScheduleType, &row.StartTS, &row.DurationStopEnabled, &row.Expiration, &row.ExecutionContext, &row.CredentialID, &row.UseServiceAccount, &row.Enabled, &row.CreatedAt, &row.UpdatedAt, &row.JobKind); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func loadScheduledJobVisibility(ctx context.Context, conn *sql.Conn, jobID int64, allowedSiteIDs []int64) (scheduledJobRow, bool, error) {
	rows, err := queryScheduledJobRows(ctx, conn, &jobID)
	if err != nil || len(rows) == 0 {
		return scheduledJobRow{}, false, err
	}
	visible, err := scheduledJobTargetsVisible(ctx, conn, parseJSONArray(rows[0].TargetsJSON), allowedSiteIDs)
	if err != nil || !visible {
		return scheduledJobRow{}, false, err
	}
	return rows[0], true, nil
}

func scheduledJobTargetsVisible(ctx context.Context, conn *sql.Conn, targets []any, allowedSiteIDs []int64) (bool, error) {
	return filterUsageTargetsFitScope(ctx, conn, targets, allowedSiteIDs)
}

func scheduledJobTargetsMatchSite(ctx context.Context, conn *sql.Conn, targets []any, siteID int64) (bool, error) {
	if siteID <= 0 || len(targets) == 0 {
		return false, nil
	}
	for _, target := range targets {
		scope, err := filterUsageTargetSiteScope(ctx, conn, target)
		if err != nil {
			return false, err
		}
		if scope == nil {
			return true, nil
		}
		for _, value := range scope {
			if value == siteID {
				return true, nil
			}
		}
	}
	return false, nil
}

func scheduledJobPayload(ctx context.Context, conn *sql.Conn, row scheduledJobRow) (map[string]any, error) {
	jobID := nullInt(row.ID)
	components := parseJSONArray(row.ComponentsJSON)
	targets := parseJSONArray(row.TargetsJSON)
	jobKind := normalizeScheduledJobKind(nullString(row.JobKind))
	lastRunTS, summaryStatus, counts, err := loadScheduledJobSummary(ctx, conn, jobID, targets, jobKind)
	if err != nil {
		return nil, err
	}
	startTS := nullableInt(row.StartTS)
	nextRunTS := computeScheduledNextRun(nullString(row.ScheduleType), nullInt(row.StartTS), lastRunTS, time.Now().Unix())
	payload := map[string]any{
		"id":                    jobID,
		"name":                  nullString(row.Name),
		"components":            components,
		"targets":               targets,
		"schedule_type":         firstText(nullString(row.ScheduleType), "immediately"),
		"start_ts":              startTS,
		"duration_stop_enabled": boolInt64(row.DurationStopEnabled),
		"expiration":            firstText(nullString(row.Expiration), "no_expire"),
		"execution_context":     firstText(nullString(row.ExecutionContext), "system"),
		"credential_id":         nullableInt(row.CredentialID),
		"use_service_account":   boolInt64(row.UseServiceAccount),
		"enabled":               boolInt64(row.Enabled),
		"created_at":            nullInt(row.CreatedAt),
		"updated_at":            nullInt(row.UpdatedAt),
		"job_kind":              jobKind,
		"last_run_ts":           nullablePlainInt(lastRunTS),
		"last_status":           firstText(summaryStatus, scheduledStatusForUnrun(startTS)),
		"latest_occurrence":     nullablePlainInt(lastRunTS),
		"result_counts":         counts,
		"next_run_ts":           nullablePlainInt(nextRunTS),
		"warning_code":          "",
		"warning_message":       "",
	}
	return payload, nil
}

func scheduledCredentialResetWarning(ctx context.Context, conn *sql.Conn, credentialID sql.NullInt64) (map[string]string, error) {
	if !credentialID.Valid || credentialID.Int64 <= 0 {
		return nil, nil
	}
	var metadata sql.NullString
	err := conn.QueryRowContext(ctx, "SELECT metadata_json FROM engine.credentials WHERE id=$1", credentialID.Int64).Scan(&metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !credentialSecretResetRequired(parseJSONObject(metadata)) {
		return nil, nil
	}
	return map[string]string{
		"warning_code":    "credential_reset_required",
		"warning_message": "Stored credential secret material was reset. Edit and save the credential before enabling jobs that use it.",
	}, nil
}

func credentialSecretResetRequired(metadata map[string]any) bool {
	state := strings.ToLower(cleanText(metadata["aegis_secret_state"]))
	if state != "reset_required" {
		return false
	}
	lostFields, ok := metadata["aegis_lost_secret_fields"].([]any)
	if !ok || len(lostFields) == 0 {
		return false
	}
	allowed := map[string]bool{
		"password":               true,
		"private_key":            true,
		"private_key_passphrase": true,
		"become_password":        true,
	}
	for _, field := range lostFields {
		if allowed[strings.ToLower(cleanText(field))] {
			return true
		}
	}
	return false
}

func loadScheduledJobSummary(ctx context.Context, conn *sql.Conn, jobID int64, targets []any, jobKind string) (*int64, string, map[string]int64, error) {
	counts := emptyScheduledResultCounts(int64(len(targets)))
	occ, err := latestScheduledOccurrence(ctx, conn, jobID)
	if err != nil || occ == nil {
		counts["pending"] = int64(len(targets))
		return nil, "", counts, err
	}
	runs, targetRows, err := loadOccurrenceForJob(ctx, conn, jobID, occ)
	if err != nil {
		return nil, "", counts, err
	}
	if jobKind == scheduledJobKindOnboarding {
		onboardingRows, err := loadOnboardingTargetRows(ctx, conn, jobID, occ)
		if err != nil {
			return nil, "", counts, err
		}
		counts = emptyScheduledResultCounts(int64(len(onboardingRows)))
		for _, row := range onboardingRows {
			bucket := onboardingStatusBucket(nullString(row.Status))
			if bucket != "" {
				counts[bucket]++
			}
		}
		if len(onboardingRows) == 0 {
			counts = scheduledRunCounts(runs)
			counts["total_targets"] = maxInt64(1, int64(len(runs)))
		}
		return occ, scheduledSummaryStatus(counts, runs), counts, nil
	}
	aggregated := aggregateScheduledDevices(runs, targetRows)
	if len(aggregated) > 0 {
		counts = scheduledDeviceCounts(aggregated)
		return occ, scheduledSummaryStatus(counts, runs), counts, nil
	}
	workflowOnly := true
	for _, row := range runs {
		if strings.ToLower(nullString(row.ComponentKind)) != "workflow" || strings.TrimSpace(nullString(row.TargetHostname)) != "" {
			workflowOnly = false
			break
		}
	}
	if workflowOnly && len(runs) > 0 {
		counts = scheduledRunCounts(runs)
		counts["total_targets"] = int64(len(runs))
		return occ, scheduledSummaryStatus(counts, runs), counts, nil
	}
	counts = scheduledRunCounts(runs)
	if counts["total_targets"] == 0 {
		if scheduledHasNoTargetSkip(runs) {
			return occ, "No Devices Targeted", counts, nil
		}
		counts["total_targets"] = int64(len(targets))
	}
	return occ, scheduledSummaryStatus(counts, runs), counts, nil
}

func latestScheduledOccurrence(ctx context.Context, conn *sql.Conn, jobID int64) (*int64, error) {
	var value sql.NullInt64
	err := conn.QueryRowContext(ctx, "SELECT MAX(scheduled_ts) FROM engine.scheduled_job_runs WHERE job_id=$1", jobID).Scan(&value)
	if err != nil || !value.Valid {
		return nil, err
	}
	return &value.Int64, nil
}

func resolveScheduledJobOccurrence(ctx context.Context, conn *sql.Conn, jobID int64, occurrence *int64) (*int64, error) {
	if occurrence != nil {
		return occurrence, nil
	}
	return latestScheduledOccurrence(ctx, conn, jobID)
}

func loadOccurrenceForJob(ctx context.Context, conn *sql.Conn, jobID int64, occurrence *int64) ([]scheduledRunRow, []scheduledTargetRow, error) {
	if occurrence == nil {
		return []scheduledRunRow{}, []scheduledTargetRow{}, nil
	}
	runs, err := loadScheduledRunsForOccurrence(ctx, conn, jobID, *occurrence)
	if err != nil {
		return nil, nil, err
	}
	targets, err := loadScheduledTargetRows(ctx, conn, jobID, *occurrence)
	if err != nil {
		return nil, nil, err
	}
	return runs, targets, nil
}

func loadScheduledRunsForOccurrence(ctx context.Context, conn *sql.Conn, jobID int64, occurrence int64) ([]scheduledRunRow, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT id, target_hostname, scheduled_ts, started_ts, finished_ts, status, error, skip_reason,
		       shared_execution, component_index, component_kind, component_name, workflow_run_id
		  FROM engine.scheduled_job_runs
		 WHERE job_id=$1 AND scheduled_ts=$2
	  ORDER BY id ASC
	`, jobID, occurrence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []scheduledRunRow{}
	for rows.Next() {
		var row scheduledRunRow
		if err := rows.Scan(&row.ID, &row.TargetHostname, &row.ScheduledTS, &row.StartedTS, &row.FinishedTS, &row.Status, &row.Error, &row.SkipReason, &row.SharedExecution, &row.ComponentIndex, &row.ComponentKind, &row.ComponentName, &row.WorkflowRunID); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func loadScheduledTargetRows(ctx context.Context, conn *sql.Conn, jobID int64, occurrence int64) ([]scheduledTargetRow, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT t.id, t.run_id, t.device_guid, t.hostname, t.site_id, s.name, t.inventory_hostname,
		       t.wireguard_peer_ip, t.resolved_connection, t.resolution_status, t.resolution_reason,
		       t.resolved_from_filter_ids_json, r.status, r.started_ts, r.finished_ts, r.skip_reason,
		       r.shared_execution, r.component_name
		  FROM engine.scheduled_job_run_targets AS t
		  JOIN engine.scheduled_job_runs AS r ON r.id=t.run_id
	 LEFT JOIN engine.sites AS s ON s.id=t.site_id
		 WHERE r.job_id=$1 AND r.scheduled_ts=$2
	  ORDER BY t.id ASC
	`, jobID, occurrence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []scheduledTargetRow{}
	for rows.Next() {
		var row scheduledTargetRow
		if err := rows.Scan(&row.ID, &row.RunID, &row.DeviceGUID, &row.Hostname, &row.SiteID, &row.SiteName, &row.InventoryHostname, &row.WireGuardPeerIP, &row.ResolvedConnection, &row.ResolutionStatus, &row.ResolutionReason, &row.ResolvedFromFilterIDsJSON, &row.RunStatus, &row.StartedTS, &row.FinishedTS, &row.SkipReason, &row.SharedExecution, &row.ComponentName); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func loadOnboardingTargetRows(ctx context.Context, conn *sql.Conn, jobID int64, occurrence *int64) ([]onboardingTargetRow, error) {
	if occurrence == nil {
		return []onboardingTargetRow{}, nil
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT id, run_id, job_id, scheduled_ts, site_id, target_input, target_address, target_hostname,
		       ssh_port, status, detail, stdout_snippet, stderr_snippet, approval_reference, created_at,
		       updated_at, finished_at
		  FROM engine.scheduled_job_onboarding_targets
		 WHERE job_id=$1 AND scheduled_ts=$2
	  ORDER BY id ASC
	`, jobID, *occurrence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []onboardingTargetRow{}
	for rows.Next() {
		var row onboardingTargetRow
		if err := rows.Scan(&row.ID, &row.RunID, &row.JobID, &row.ScheduledTS, &row.SiteID, &row.TargetInput, &row.TargetAddress, &row.TargetHostname, &row.SSHPort, &row.Status, &row.Detail, &row.StdoutSnippet, &row.StderrSnippet, &row.ApprovalReference, &row.CreatedAt, &row.UpdatedAt, &row.FinishedAt); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func loadOnboardingTargetEvents(ctx context.Context, conn *sql.Conn, targetIDs []int64) (map[int64][]map[string]any, error) {
	result := map[int64][]map[string]any{}
	targetIDs = uniquePositiveInt64s(targetIDs)
	if len(targetIDs) == 0 {
		return result, nil
	}
	query, params := inClauseQuery(`
		SELECT id, target_row_id, run_id, job_id, status, task, detail, stdout_snippet, stderr_snippet,
		       started_at, finished_at, created_at, updated_at
		  FROM engine.scheduled_job_onboarding_target_events
		 WHERE target_row_id IN (%s)
	  ORDER BY target_row_id ASC, COALESCE(started_at, created_at, 0) ASC, id ASC
	`, targetIDs)
	rows, err := conn.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, targetRowID, runID, jobID, startedAt, finishedAt, createdAt, updatedAt sql.NullInt64
		var status, task, detail, stdoutSnippet, stderrSnippet sql.NullString
		if err := rows.Scan(&id, &targetRowID, &runID, &jobID, &status, &task, &detail, &stdoutSnippet, &stderrSnippet, &startedAt, &finishedAt, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		targetID := nullInt(targetRowID)
		result[targetID] = append(result[targetID], map[string]any{
			"id":             nullInt(id),
			"target_row_id":  targetID,
			"run_id":         nullInt(runID),
			"job_id":         nullInt(jobID),
			"status":         nullString(status),
			"task":           nullString(task),
			"detail":         nullString(detail),
			"stdout_snippet": nullString(stdoutSnippet),
			"stderr_snippet": nullString(stderrSnippet),
			"started_at":     nullableInt(startedAt),
			"finished_at":    nullableInt(finishedAt),
			"created_at":     nullableInt(createdAt),
			"updated_at":     nullableInt(updatedAt),
		})
	}
	return result, rows.Err()
}

func loadScheduledRunActivities(ctx context.Context, conn *sql.Conn, runIDs []int64) (map[int64][]map[string]any, error) {
	result := map[int64][]map[string]any{}
	runIDs = uniquePositiveInt64s(runIDs)
	if len(runIDs) == 0 {
		return result, nil
	}
	query, params := inClauseQuery(`
		SELECT s.run_id, s.activity_id, s.component_kind, s.script_type, s.component_path, s.component_name,
		       COALESCE(LENGTH(h.stdout), 0), COALESCE(LENGTH(h.stderr), 0)
		  FROM engine.scheduled_job_run_activity AS s
	 LEFT JOIN engine.activity_history AS h ON h.id=s.activity_id
		 WHERE s.run_id IN (%s)
	`, runIDs)
	rows, err := conn.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var runID, activityID, stdoutLen, stderrLen sql.NullInt64
		var kind, scriptType, path, name sql.NullString
		if err := rows.Scan(&runID, &activityID, &kind, &scriptType, &path, &name, &stdoutLen, &stderrLen); err != nil {
			return nil, err
		}
		rid := nullInt(runID)
		result[rid] = append(result[rid], map[string]any{
			"activity_id":    nullInt(activityID),
			"component_kind": nullString(kind),
			"script_type":    nullString(scriptType),
			"component_path": nullString(path),
			"component_name": nullString(name),
			"has_stdout":     nullInt(stdoutLen) > 0,
			"has_stderr":     nullInt(stderrLen) > 0,
		})
	}
	return result, rows.Err()
}

func loadOnlineHostnames(ctx context.Context, conn *sql.Conn) (map[string]bool, error) {
	threshold := time.Now().Unix() - 300
	rows, err := conn.QueryContext(ctx, "SELECT hostname FROM engine.devices WHERE last_seen IS NOT NULL AND last_seen >= $1", threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	online := map[string]bool{}
	for rows.Next() {
		var hostname sql.NullString
		if err := rows.Scan(&hostname); err != nil {
			return nil, err
		}
		name := strings.ToLower(nullString(hostname))
		if name != "" {
			online[name] = true
		}
	}
	return online, rows.Err()
}

func loadOnboardingApprovalLookup(ctx context.Context, conn *sql.Conn, rows []onboardingTargetRow) (map[string]map[string]any, error) {
	keys := []string{}
	for _, row := range rows {
		for _, value := range []string{nullString(row.ApprovalReference), nullString(row.TargetHostname), nullString(row.TargetAddress)} {
			if strings.TrimSpace(value) != "" {
				keys = append(keys, strings.ToLower(strings.TrimSpace(value)))
			}
		}
	}
	keys = uniqueStrings(keys)
	if len(keys) == 0 {
		return map[string]map[string]any{}, nil
	}
	firstClause, firstParams := stringInClause(keys, 1)
	secondClause, secondParams := stringInClause(keys, len(firstParams)+1)
	query := `
		SELECT id, approval_reference, hostname_claimed, status
		  FROM engine.device_approvals
		 WHERE LOWER(COALESCE(approval_reference, '')) IN (` + firstClause + `)
		    OR LOWER(COALESCE(hostname_claimed, '')) IN (` + secondClause + `)
	`
	params := append(firstParams, secondParams...)
	rowsRaw, err := conn.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rowsRaw.Close()
	lookup := map[string]map[string]any{}
	for rowsRaw.Next() {
		var id sql.NullInt64
		var ref, host, status sql.NullString
		if err := rowsRaw.Scan(&id, &ref, &host, &status); err != nil {
			return nil, err
		}
		payload := map[string]any{"id": nullInt(id), "status": nullString(status)}
		for _, value := range []string{nullString(ref), nullString(host)} {
			key := strings.ToLower(strings.TrimSpace(value))
			if key != "" {
				lookup[key] = payload
			}
		}
	}
	return lookup, rowsRaw.Err()
}

func approvalForOnboardingTarget(row onboardingTargetRow, lookup map[string]map[string]any) map[string]any {
	for _, value := range []string{nullString(row.ApprovalReference), nullString(row.TargetHostname), nullString(row.TargetAddress)} {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" {
			continue
		}
		if payload := lookup[key]; payload != nil {
			return payload
		}
	}
	return nil
}

func scheduledRunPayload(row scheduledRunRow) map[string]any {
	return map[string]any{
		"id":               nullInt(row.ID),
		"scheduled_ts":     nullableInt(row.ScheduledTS),
		"started_ts":       nullableInt(row.StartedTS),
		"finished_ts":      nullableInt(row.FinishedTS),
		"status":           nullString(row.Status),
		"error":            nullString(row.Error),
		"skip_reason":      nullString(row.SkipReason),
		"target_hostname":  nullString(row.TargetHostname),
		"shared_execution": boolInt64(row.SharedExecution),
		"component_index":  nullableInt(row.ComponentIndex),
		"component_kind":   nullString(row.ComponentKind),
		"component_name":   nullString(row.ComponentName),
		"workflow_run_id":  nullableInt(row.WorkflowRunID),
	}
}

func onboardingTargetPayload(row onboardingTargetRow) map[string]any {
	sshPort := nullInt(row.SSHPort)
	if sshPort <= 0 {
		sshPort = defaultOnboardingSSHPort
	}
	return map[string]any{
		"id":                 nullInt(row.ID),
		"run_id":             nullInt(row.RunID),
		"job_id":             nullInt(row.JobID),
		"scheduled_ts":       nullInt(row.ScheduledTS),
		"site_id":            nullableInt(row.SiteID),
		"target_input":       nullString(row.TargetInput),
		"target_address":     nullString(row.TargetAddress),
		"target_hostname":    nullString(row.TargetHostname),
		"ssh_port":           sshPort,
		"status":             nullString(row.Status),
		"detail":             nullString(row.Detail),
		"stdout_snippet":     nullString(row.StdoutSnippet),
		"stderr_snippet":     nullString(row.StderrSnippet),
		"approval_reference": nullString(row.ApprovalReference),
		"created_at":         nullableInt(row.CreatedAt),
		"updated_at":         nullableInt(row.UpdatedAt),
		"finished_at":        nullableInt(row.FinishedAt),
	}
}

func onboardingTargetDevicePayload(row onboardingTargetRow) map[string]any {
	sshPort := nullInt(row.SSHPort)
	if sshPort <= 0 {
		sshPort = defaultOnboardingSSHPort
	}
	hostname := firstText(nullString(row.TargetHostname), nullString(row.TargetAddress))
	return map[string]any{
		"hostname":            hostname,
		"online":              false,
		"site_id":             nullableInt(row.SiteID),
		"site_name":           "",
		"site":                "",
		"inventory_hostname":  nullString(row.TargetHostname),
		"wireguard_peer_ip":   "",
		"resolved_connection": "local_network_onboarding",
		"resolution_status":   nullString(row.Status),
		"resolution_reason":   nullString(row.Detail),
		"ran_on":              firstPresentAny(nullableInt(row.FinishedAt), nullableInt(row.UpdatedAt)),
		"job_status":          firstText(nullString(row.Status), "pending"),
		"has_stdout":          strings.TrimSpace(nullString(row.StdoutSnippet)) != "",
		"has_stderr":          strings.TrimSpace(nullString(row.StderrSnippet)) != "",
		"target_input":        nullString(row.TargetInput),
		"ssh_port":            sshPort,
		"detail":              nullString(row.Detail),
		"stdout_snippet":      nullString(row.StdoutSnippet),
		"stderr_snippet":      nullString(row.StderrSnippet),
		"approval_reference":  nullString(row.ApprovalReference),
		"activities":          []map[string]any{},
	}
}

func aggregateScheduledDevices(runs []scheduledRunRow, targetRows []scheduledTargetRow) []map[string]any {
	if len(targetRows) > 0 {
		grouped := map[string]map[string]any{}
		for _, row := range targetRows {
			hostname := nullString(row.Hostname)
			if hostname == "" {
				continue
			}
			key := scheduledTargetGroupKey(row)
			group := grouped[key]
			if group == nil {
				group = map[string]any{
					"hostname":            hostname,
					"site_id":             nullableInt(row.SiteID),
					"site_name":           nullString(row.SiteName),
					"inventory_hostname":  nullString(row.InventoryHostname),
					"wireguard_peer_ip":   nullString(row.WireGuardPeerIP),
					"resolved_connection": nullString(row.ResolvedConnection),
					"resolution_status":   "",
					"resolution_reason":   "",
					"run_ids":             []int64{},
					"eligible_runs":       []scheduledRunSummary{},
				}
				grouped[key] = group
			}
			group["run_ids"] = appendInt64Unique(group["run_ids"], nullInt(row.RunID))
			if !hasValue(group["site_name"]) && nullString(row.SiteName) != "" {
				group["site_name"] = nullString(row.SiteName)
			}
			for _, field := range []struct {
				key   string
				value string
			}{
				{"inventory_hostname", nullString(row.InventoryHostname)},
				{"wireguard_peer_ip", nullString(row.WireGuardPeerIP)},
				{"resolved_connection", nullString(row.ResolvedConnection)},
			} {
				if !hasValue(group[field.key]) && field.value != "" {
					group[field.key] = field.value
				}
			}
			resolutionStatus := strings.ToLower(nullString(row.ResolutionStatus))
			if resolutionStatus != "" && resolutionStatus != "pending" && resolutionStatus != "eligible" {
				if !hasValue(group["resolution_status"]) {
					group["resolution_status"] = resolutionStatus
					group["resolution_reason"] = nullString(row.ResolutionReason)
				}
				continue
			}
			group["eligible_runs"] = append(group["eligible_runs"].([]scheduledRunSummary), scheduledRunSummary{
				ID:         nullInt(row.RunID),
				Status:     nullString(row.RunStatus),
				StartedTS:  nullableInt(row.StartedTS),
				FinishedTS: nullableInt(row.FinishedTS),
			})
			if !hasValue(group["resolution_status"]) {
				group["resolution_status"] = "eligible"
			}
		}
		out := make([]map[string]any, 0, len(grouped))
		for _, group := range grouped {
			status := scheduledStatusPending
			eligible := group["eligible_runs"].([]scheduledRunSummary)
			if len(eligible) > 0 {
				sort.SliceStable(eligible, func(i, j int) bool {
					return scheduledRunPriority(eligible[i]) > scheduledRunPriority(eligible[j])
				})
				status = firstText(eligible[0].Status, scheduledStatusPending)
				group["started_ts"] = eligible[0].StartedTS
				group["finished_ts"] = eligible[0].FinishedTS
			} else if strings.ToLower(cleanText(group["resolution_status"])) == "skipped" || strings.ToLower(cleanText(group["resolution_status"])) == "unresolved" {
				status = scheduledStatusSkipped
			}
			group["status"] = status
			delete(group, "eligible_runs")
			out = append(out, group)
		}
		return out
	}
	grouped := map[string][]scheduledRunRow{}
	for _, row := range runs {
		host := nullString(row.TargetHostname)
		if host == "" {
			continue
		}
		grouped[strings.ToLower(host)] = append(grouped[strings.ToLower(host)], row)
	}
	out := make([]map[string]any, 0, len(grouped))
	for _, rows := range grouped {
		sort.SliceStable(rows, func(i, j int) bool {
			return scheduledRunRowPriority(rows[i]) > scheduledRunRowPriority(rows[j])
		})
		row := rows[0]
		out = append(out, map[string]any{
			"hostname":          nullString(row.TargetHostname),
			"status":            firstText(nullString(row.Status), scheduledStatusPending),
			"started_ts":        nullableInt(row.StartedTS),
			"finished_ts":       nullableInt(row.FinishedTS),
			"run_ids":           []int64{nullInt(row.ID)},
			"resolution_status": "",
			"resolution_reason": "",
		})
	}
	return out
}

type scheduledRunSummary struct {
	ID         int64
	Status     string
	StartedTS  any
	FinishedTS any
}

func scheduledTargetGroupKey(row scheduledTargetRow) string {
	guid := strings.ToLower(nullString(row.DeviceGUID))
	if guid != "" {
		return "guid:" + guid
	}
	hostname := strings.ToLower(nullString(row.Hostname))
	if row.SiteID.Valid {
		return fmt.Sprintf("site:%d:%s", row.SiteID.Int64, hostname)
	}
	return "host:" + hostname
}

func scheduledDevicesFromSavedTargets(targets []any) []map[string]any {
	out := []map[string]any{}
	for _, target := range targets {
		hostname := ""
		siteID := any(nil)
		switch typed := target.(type) {
		case string:
			hostname = strings.TrimSpace(typed)
		case map[string]any:
			hostname = firstText(cleanText(typed["hostname"]), cleanText(typed["host"]), cleanText(typed["device_hostname"]))
			if site, ok := int64Value(firstPresentAny(typed["site_id"], typed["siteId"])); ok {
				siteID = site
			}
		}
		if hostname == "" {
			continue
		}
		out = append(out, map[string]any{
			"hostname":          hostname,
			"site_id":           siteID,
			"status":            scheduledStatusPending,
			"run_ids":           []int64{},
			"resolution_status": "",
			"resolution_reason": "",
		})
	}
	return out
}

func scheduledDeviceCounts(devices []map[string]any) map[string]int64 {
	counts := emptyScheduledResultCounts(int64(len(devices)))
	for _, device := range devices {
		bucket := scheduledStatusBucket(cleanText(device["status"]))
		if bucket != "" {
			counts[bucket]++
		}
	}
	return counts
}

func scheduledRunCounts(runs []scheduledRunRow) map[string]int64 {
	counts := emptyScheduledResultCounts(0)
	seenHosts := map[string]struct{}{}
	for _, run := range runs {
		host := strings.ToLower(nullString(run.TargetHostname))
		if host != "" {
			seenHosts[host] = struct{}{}
		}
		bucket := scheduledStatusBucket(nullString(run.Status))
		if bucket != "" {
			counts[bucket]++
		}
	}
	if len(seenHosts) > 0 {
		counts["total_targets"] = int64(len(seenHosts))
	} else {
		counts["total_targets"] = int64(len(runs))
	}
	return counts
}

func emptyScheduledResultCounts(total int64) map[string]int64 {
	return map[string]int64{
		"pending":       0,
		"running":       0,
		"success":       0,
		"warning":       0,
		"failed":        0,
		"expired":       0,
		"timed_out":     0,
		"skipped":       0,
		"total_targets": total,
	}
}

func scheduledSummaryStatus(counts map[string]int64, runs []scheduledRunRow) string {
	for _, item := range []struct {
		key    string
		status string
	}{
		{"running", scheduledStatusRunning},
		{"failed", scheduledStatusFailed},
		{"timed_out", scheduledStatusTimedOut},
		{"warning", scheduledStatusWarning},
		{"expired", scheduledStatusExpired},
		{"pending", scheduledStatusPending},
		{"success", scheduledStatusSuccess},
		{"skipped", scheduledStatusSkipped},
	} {
		if counts[item.key] > 0 {
			return item.status
		}
	}
	if len(runs) > 0 {
		return nullString(runs[0].Status)
	}
	return ""
}

func scheduledStatusBucket(status string) string {
	switch strings.TrimSpace(status) {
	case scheduledStatusPending:
		return "pending"
	case scheduledStatusRunning:
		return "running"
	case scheduledStatusSuccess:
		return "success"
	case scheduledStatusWarning:
		return "warning"
	case scheduledStatusFailed:
		return "failed"
	case scheduledStatusExpired:
		return "expired"
	case scheduledStatusTimedOut:
		return "timed_out"
	case scheduledStatusSkipped:
		return "skipped"
	default:
		return ""
	}
}

func onboardingStatusBucket(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "connecting", "installing", "enrolling", "waiting", "queued", "pending":
		if strings.EqualFold(status, "pending") || strings.EqualFold(status, "queued") {
			return "pending"
		}
		return "running"
	case "success", "succeeded", "completed", "already_enrolled":
		return "success"
	case "warning":
		return "warning"
	case "failed", "error":
		return "failed"
	case "expired":
		return "expired"
	case "timed_out", "timeout":
		return "timed_out"
	case "skipped":
		return "skipped"
	default:
		return "pending"
	}
}

func scheduledHasNoTargetSkip(runs []scheduledRunRow) bool {
	for _, run := range runs {
		if nullString(run.Status) == scheduledStatusSkipped && strings.ToLower(nullString(run.SkipReason)) == scheduledSkipNoTargets {
			return true
		}
	}
	return false
}

func scheduledRunPriority(run scheduledRunSummary) int64 {
	statusPriority := map[string]int64{
		scheduledStatusRunning:  70,
		scheduledStatusFailed:   60,
		scheduledStatusTimedOut: 50,
		scheduledStatusWarning:  45,
		scheduledStatusExpired:  40,
		scheduledStatusSuccess:  30,
		scheduledStatusPending:  20,
		scheduledStatusSkipped:  10,
	}[run.Status]
	return statusPriority*1_000_000_000_000 + coerceInt64(firstPresentAny(run.FinishedTS, run.StartedTS))
}

func scheduledRunRowPriority(row scheduledRunRow) int64 {
	return scheduledRunPriority(scheduledRunSummary{
		ID:         nullInt(row.ID),
		Status:     nullString(row.Status),
		StartedTS:  nullableInt(row.StartedTS),
		FinishedTS: nullableInt(row.FinishedTS),
	}) + nullInt(row.ID)
}

func scheduledDeviceRunIDs(devices []map[string]any) []int64 {
	ids := []int64{}
	for _, device := range devices {
		ids = append(ids, int64SliceFromAny(device["run_ids"])...)
	}
	return uniquePositiveInt64s(ids)
}

func scheduledActivitiesForRuns(runIDs any, lookup map[int64][]map[string]any) []map[string]any {
	seen := map[int64]struct{}{}
	out := []map[string]any{}
	for _, runID := range int64SliceFromAny(runIDs) {
		for _, activity := range lookup[runID] {
			activityID := coerceInt64(activity["activity_id"])
			if activityID > 0 {
				if _, ok := seen[activityID]; ok {
					continue
				}
				seen[activityID] = struct{}{}
			}
			out = append(out, activity)
		}
	}
	return out
}

func scheduledActivitiesHave(activities []map[string]any, key string) bool {
	for _, activity := range activities {
		if value, ok := activity[key].(bool); ok && value {
			return true
		}
	}
	return false
}

func computeScheduledNextRun(scheduleType string, startTS int64, lastRun *int64, nowTS int64) *int64 {
	st := strings.ToLower(strings.TrimSpace(scheduleType))
	if st == "" {
		st = "immediately"
	}
	start := floorMinute(startTS)
	now := floorMinute(nowTS)
	last := int64(0)
	if lastRun != nil {
		last = floorMinute(*lastRun)
	}
	switch st {
	case "immediately":
		if last > 0 {
			return nil
		}
		return &now
	case "once":
		if start <= 0 || last > 0 {
			return nil
		}
		return &start
	}
	if start <= 0 {
		return nil
	}
	if last <= 0 {
		return &start
	}
	periods := map[string]int64{
		"every_5_minutes":  300,
		"every_10_minutes": 600,
		"every_15_minutes": 900,
		"every_30_minutes": 1800,
		"every_hour":       3600,
		"daily":            86400,
		"weekly":           7 * 86400,
	}
	if period, ok := periods[st]; ok {
		next := last + period
		return &next
	}
	t := time.Unix(last, 0)
	if st == "monthly" {
		next := t.AddDate(0, 1, 0).Unix()
		return &next
	}
	if st == "yearly" {
		next := t.AddDate(1, 0, 0).Unix()
		return &next
	}
	return nil
}

func normalizeScheduledJobKind(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "device_onboarding", "automatic_onboarding", "ssh_onboarding", scheduledJobKindOnboarding:
		return scheduledJobKindOnboarding
	case "agent_maintenance", "agent_update", "agent_channel_switch":
		return scheduledJobKindAgentMaintenance
	default:
		return scheduledJobKindAutomation
	}
}

func scheduledStatusForUnrun(startTS any) string {
	if startTS != nil && coerceInt64(startTS) > 0 {
		return "Scheduled"
	}
	return ""
}

func floorMinute(value int64) int64 {
	if value <= 0 {
		return 0
	}
	return value - (value % 60)
}

func boolInt64(value sql.NullInt64) bool {
	return value.Valid && value.Int64 != 0
}

func nullablePlainInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func occAny(value *int64) any {
	return nullablePlainInt(value)
}

func int64SliceFromAny(value any) []int64 {
	switch typed := value.(type) {
	case []int64:
		return typed
	case []any:
		out := []int64{}
		for _, item := range typed {
			if parsed := coerceInt64(item); parsed > 0 {
				out = append(out, parsed)
			}
		}
		return out
	default:
		return []int64{}
	}
}

func uniquePositiveInt64s(values []int64) []int64 {
	seen := map[int64]struct{}{}
	out := []int64{}
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func appendInt64Unique(value any, item int64) []int64 {
	values := int64SliceFromAny(value)
	if item <= 0 {
		return values
	}
	for _, existing := range values {
		if existing == item {
			return values
		}
	}
	return append(values, item)
}

func hasValue(value any) bool {
	return strings.TrimSpace(cleanText(value)) != ""
}

func stringInClause(values []string, startIndex int) (string, []any) {
	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for idx, value := range values {
		placeholders = append(placeholders, "$"+strconv.Itoa(startIndex+idx))
		args = append(args, value)
	}
	return strings.Join(placeholders, ","), args
}
