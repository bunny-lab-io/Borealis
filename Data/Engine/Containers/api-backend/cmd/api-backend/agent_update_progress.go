package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	agentUpdateProgressEventName = "agent_update_progress_changed"
	agentUpdateHistoryMaxRows    = 100
)

type agentUpdateProgressStore interface {
	recordAgentUpdateProgress(ctx context.Context, deviceCtx deviceBearerAuthContext, request agentUpdateProgressRequest) (map[string]any, int, error)
}

type agentUpdateHistoryStore interface {
	agentUpdateHistory(ctx context.Context, profile operatorProfile, deviceGUID string, operationID string, limit int) (map[string]any, int, error)
}

type agentUpdateProgressRequest struct {
	OperationID          string
	ScheduledJobID       int64
	ScheduledJobRunID    int64
	Source               string
	RequestedBy          string
	TargetBuildID        string
	InstalledBuildBefore string
	InstalledBuildAfter  string
	Events               []map[string]any
}

type agentUpdateProgressRef struct {
	JobID      int64
	RunID      int64
	ActivityID int64
	RunStatus  string
	Hostname   string
	GUID       string
	Metadata   map[string]any
}

type agentUpdateHistoryRow struct {
	JobID        int64
	RunID        int64
	ActivityID   int64
	JobName      string
	RunStatus    string
	StartedTS    sql.NullInt64
	FinishedTS   sql.NullInt64
	UpdatedAt    sql.NullInt64
	MetadataJSON sql.NullString
	Stdout       sql.NullString
	Stderr       sql.NullString
}

func registerAgentUpdateProgressRoute(mux *http.ServeMux, auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, realtime *operatorRealtimeHub) {
	mux.HandleFunc("POST /api/agent/update/progress", agentUpdateProgressHandler(auth, signer, dpop, realtime))
}

const agentUpdateProgressChangedKey = "_agent_update_progress_changed"

func agentUpdateProgressHandler(auth *authService, signer *agentJWTSigner, dpop *dpopVerifier, realtime *operatorRealtimeHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceCtx, failure := authenticateDeviceBearer(r.Context(), r, auth, signer, dpop)
		if failure != nil {
			failure.write(w)
			return
		}
		body, err := readAgentJSONMap(w, r, 64<<10)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		request, validationErrors := normalizeAgentUpdateProgressRequest(body)
		if len(validationErrors) > 0 {
			writePublicValidationErrors(w, validationErrors)
			return
		}
		store, ok := auth.store.(agentUpdateProgressStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "agent_update_progress_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, status, err := store.recordAgentUpdateProgress(ctx, deviceCtx, request)
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		if realtime != nil && boolFromAny(payload["changed"]) {
			_ = realtime.emit(agentUpdateProgressEventName, map[string]any{
				"device_guid":  normalizeCanonicalGUID(deviceCtx.GUID),
				"operation_id": request.OperationID,
				"event":        request.Events[len(request.Events)-1],
			})
		}
		writeJSON(w, status, payload)
	}
}

func agentUpdateHistoryHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable"})
			return
		}
		deviceGUID := normalizeCanonicalGUID(r.PathValue("device_guid"))
		if deviceGUID == "" {
			writePublicValidationErrors(w, []publicValidationError{{Field: "path.device_guid", Message: "Device GUID is invalid."}})
			return
		}
		operationID := cleanText(r.URL.Query().Get("operation_id"))
		if operationID != "" {
			if err := validateIdentifierInput("query.operation_id", operationID); err != nil || len(operationID) > 128 {
				writePublicValidationErrors(w, []publicValidationError{{Field: "query.operation_id", Message: "Operation ID is invalid."}})
				return
			}
		}
		limit := 50
		if rawLimit := cleanText(r.URL.Query().Get("limit")); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil || parsed < 1 || parsed > agentUpdateHistoryMaxRows {
				writePublicValidationErrors(w, []publicValidationError{{Field: "query.limit", Message: "Limit must be between 1 and 100."}})
				return
			}
			limit = parsed
		}
		store, ok := auth.store.(agentUpdateHistoryStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "agent_update_history_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, status, err := store.agentUpdateHistory(ctx, profile, deviceGUID, operationID, limit)
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func normalizeAgentUpdateProgressRequest(body map[string]any) (agentUpdateProgressRequest, []publicValidationError) {
	request := agentUpdateProgressRequest{
		OperationID:          cleanText(body["operation_id"]),
		ScheduledJobID:       coerceInt64(body["scheduled_job_id"]),
		ScheduledJobRunID:    coerceInt64(body["scheduled_job_run_id"]),
		Source:               normalizeAgentUpdateSource(body["source"]),
		RequestedBy:          sanitizeAgentUpdateText(body["requested_by"], 128),
		TargetBuildID:        sanitizeAgentUpdateText(body["target_build_id"], 128),
		InstalledBuildBefore: sanitizeAgentUpdateText(body["installed_build_before"], 128),
		InstalledBuildAfter:  sanitizeAgentUpdateText(body["installed_build_after"], 128),
	}
	errs := []publicValidationError{}
	if request.OperationID == "" || len(request.OperationID) > 128 || validateIdentifierInput("operation_id", request.OperationID) != nil {
		errs = append(errs, publicValidationError{Field: "operation_id", Message: "Operation ID is required and must be a valid identifier."})
	}
	if request.Source == "" {
		errs = append(errs, publicValidationError{Field: "source", Message: "Update source is invalid."})
	}
	if request.ScheduledJobID < 0 {
		errs = append(errs, publicValidationError{Field: "scheduled_job_id", Message: "Scheduled Job ID must be positive when provided."})
	}
	if request.ScheduledJobRunID < 0 {
		errs = append(errs, publicValidationError{Field: "scheduled_job_run_id", Message: "Scheduled Job run ID must be positive when provided."})
	}
	rawEvents := mapSlice(body["events"])
	if event, ok := body["event"].(map[string]any); ok {
		rawEvents = []map[string]any{event}
	}
	for index, event := range rawEvents {
		if index >= 128 {
			break
		}
		prefix := fmt.Sprintf("events[%d]", index)
		if cleanText(event["event_id"]) == "" || len(cleanText(event["event_id"])) > 256 {
			errs = append(errs, publicValidationError{Field: prefix + ".event_id", Message: "Event ID is required and must not exceed 256 characters."})
		}
		if cleanText(event["phase_id"]) == "" || len(cleanText(event["phase_id"])) > 96 {
			errs = append(errs, publicValidationError{Field: prefix + ".phase_id", Message: "Phase ID is required and must not exceed 96 characters."})
		}
		if len(cleanText(event["parent_phase_id"])) > 96 {
			errs = append(errs, publicValidationError{Field: prefix + ".parent_phase_id", Message: "Parent phase ID must not exceed 96 characters."})
		}
		state := strings.ToLower(cleanText(event["state"]))
		if !agentUpdateEventStateAllowed(state) {
			errs = append(errs, publicValidationError{Field: prefix + ".state", Message: "Progress state is invalid."})
		}
		terminal := strings.ToLower(cleanText(event["terminal_status"]))
		if terminal != "" && terminal != "success" && terminal != "failed" && terminal != "timed_out" {
			errs = append(errs, publicValidationError{Field: prefix + ".terminal_status", Message: "Terminal status is invalid."})
		}
		request.Events = append(request.Events, normalizeAgentUpdateProgressEvent(event))
	}
	if len(request.Events) == 0 {
		errs = append(errs, publicValidationError{Field: "events", Message: "At least one progress event is required."})
	}
	return request, errs
}

func normalizeAgentUpdateProgressEvent(event map[string]any) map[string]any {
	state := strings.ToLower(cleanText(event["state"]))
	if !agentUpdateEventStateAllowed(state) {
		state = "pending"
	}
	terminal := strings.ToLower(cleanText(event["terminal_status"]))
	if terminal != "success" && terminal != "failed" && terminal != "timed_out" {
		terminal = ""
	}
	return map[string]any{
		"event_id":                 sanitizeAgentUpdateText(event["event_id"], 256),
		"phase_id":                 strings.ToLower(sanitizeAgentUpdateText(event["phase_id"], 96)),
		"parent_phase_id":          strings.ToLower(sanitizeAgentUpdateText(event["parent_phase_id"], 96)),
		"state":                    state,
		"agent_timestamp":          coerceInt64(event["agent_timestamp"]),
		"engine_receive_timestamp": time.Now().Unix(),
		"duration_ms":              maxInt64(coerceInt64(event["duration_ms"]), 0),
		"summary":                  sanitizeAgentUpdateText(event["summary"], 240),
		"detail":                   sanitizeAgentUpdateText(event["detail"], 1024),
		"retry_count":              maxInt64(coerceInt64(event["retry_count"]), 0),
		"terminal_status":          terminal,
	}
}

func normalizeAgentUpdateSource(value any) string {
	text := strings.ToLower(strings.ReplaceAll(cleanText(value), "-", "_"))
	switch text {
	case "operator", "operator_initiated", "update_now":
		return "operator_initiated"
	case "hourly", "hourly_update_checker", "auto_updater":
		return "hourly_update_checker"
	default:
		return ""
	}
}

func sanitizeAgentUpdateText(value any, limit int) string {
	text := strings.TrimSpace(strings.ReplaceAll(cleanText(value), "\x00", ""))
	lower := strings.ToLower(text)
	for _, marker := range []string{"access_token", "refresh_token", "private_key", "password"} {
		if strings.Contains(lower, marker) {
			return "Sensitive diagnostic detail redacted."
		}
	}
	if limit > 0 && len(text) > limit {
		text = text[:limit]
	}
	return text
}

func agentUpdateEventStateAllowed(value string) bool {
	switch value {
	case "pending", "running", "success", "failed", "timed_out", "skipped", "recovering":
		return true
	default:
		return false
	}
}

func (s *postgresOperatorStore) recordAgentUpdateProgress(ctx context.Context, deviceCtx deviceBearerAuthContext, request agentUpdateProgressRequest) (map[string]any, int, error) {
	guid := normalizeCanonicalGUID(deviceCtx.GUID)
	if guid == "" {
		return nil, http.StatusNotFound, errors.New("device_not_registered")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, request.OperationID); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	ref, err := findAgentUpdateProgressRef(ctx, tx, guid, request)
	if errors.Is(err, sql.ErrNoRows) && request.Source == "hourly_update_checker" {
		ref, err = createHourlyAgentUpdateProgressRef(ctx, tx, guid, request)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, http.StatusNotFound, errors.New("agent_update_operation_not_found")
		}
		return nil, http.StatusInternalServerError, err
	}

	metadata := copyMap(ref.Metadata)
	update := schedulerAnyMap(metadata["agent_update"])
	update["operation_id"] = request.OperationID
	update["source"] = request.Source
	update["requested_by"] = request.RequestedBy
	update["target_build_id"] = request.TargetBuildID
	update["installed_build_before"] = request.InstalledBuildBefore
	if request.InstalledBuildAfter != "" {
		update["installed_build_after"] = request.InstalledBuildAfter
	}
	update["scheduled_job_id"] = ref.JobID
	update["scheduled_job_run_id"] = ref.RunID
	update["device_guid"] = guid
	update["hostname"] = ref.Hostname
	update["updated_at"] = time.Now().Unix()

	existingEvents := mapSlice(update["events"])
	acceptedEvents := make([]map[string]any, 0, len(request.Events))
	for _, event := range request.Events {
		var accepted bool
		existingEvents, accepted = mergeAgentUpdateProgressEvent(existingEvents, event)
		if !accepted {
			continue
		}
		acceptedEvents = append(acceptedEvents, event)
		update["status"] = agentUpdateStatusFromEvent(firstText(cleanText(update["status"]), "running"), event)
		if coerceInt64(update["started_at"]) <= 0 {
			update["started_at"] = firstPositiveInt64(coerceInt64(event["agent_timestamp"]), time.Now().Unix())
		}
		if terminal := cleanText(event["terminal_status"]); terminal != "" {
			update["completed_at"] = time.Now().Unix()
			if terminal == "failed" || terminal == "timed_out" {
				update["failure_summary"] = firstText(cleanText(event["detail"]), cleanText(event["summary"]))
			}
		}
	}
	update["events"] = existingEvents
	update["status"] = agentUpdateHistoryStatus(cleanText(update["status"]), ref.RunStatus)
	if len(acceptedEvents) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		committed = true
		return map[string]any{
			"status":                   cleanText(update["status"]),
			"operation_id":             request.OperationID,
			"scheduled_job_id":         ref.JobID,
			"scheduled_job_run_id":     ref.RunID,
			"engine_receive_timestamp": time.Now().Unix(),
			"changed":                  false,
		}, http.StatusOK, nil
	}
	metadata["operation_id"] = request.OperationID
	metadata["requested_by"] = request.RequestedBy
	metadata["agent_update_source"] = request.Source
	metadata["agent_update"] = update
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	runStatus := agentUpdateRunStatus(cleanText(update["status"]))
	now := time.Now().Unix()
	var finished any
	if runStatus == "Success" || runStatus == "Failed" || runStatus == "Timed Out" {
		finished = now
	}
	failureSummary := cleanText(update["failure_summary"])
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_runs
		   SET status=$1, updated_at=$2, finished_ts=COALESCE($3, finished_ts), error=$4
		 WHERE id=$5
	`, runStatus, now, finished, truncateMetadataText(failureSummary, 512), ref.RunID); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	resolutionStatus := "eligible"
	if runStatus == "Failed" || runStatus == "Timed Out" {
		resolutionStatus = "unresolved"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_run_targets
		   SET resolution_status=$1, resolution_reason=$2
		 WHERE run_id=$3
	`, resolutionStatus, truncateMetadataText(failureSummary, 512), ref.RunID); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	stdout, stderr := agentUpdateActivityOutput(acceptedEvents)
	if _, err := tx.ExecContext(ctx, `
		UPDATE engine.activity_history
		   SET status=$1,
		       stdout=COALESCE(stdout, '') || $2,
		       stderr=COALESCE(stderr, '') || $3,
		       metadata_json=$4,
		       updated_at=$5,
		       finished_at=COALESCE($6, finished_at)
		 WHERE id=$7
	`, runStatus, stdout, stderr, string(metadataJSON), now, finished, ref.ActivityID); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	committed = true
	return map[string]any{
		"status":                   cleanText(update["status"]),
		"operation_id":             request.OperationID,
		"scheduled_job_id":         ref.JobID,
		"scheduled_job_run_id":     ref.RunID,
		"engine_receive_timestamp": now,
		"changed":                  true,
	}, http.StatusOK, nil
}

func findAgentUpdateProgressRef(ctx context.Context, tx *sql.Tx, guid string, request agentUpdateProgressRequest) (agentUpdateProgressRef, error) {
	query := `
		SELECT j.id, r.id, h.id, COALESCE(r.status, ''), COALESCE(h.hostname, ''), COALESCE(h.metadata_json, '')
		  FROM engine.scheduled_job_runs r
		  JOIN engine.scheduled_jobs j ON j.id=r.job_id
		  JOIN engine.scheduled_job_run_activity a ON a.run_id=r.id
		  JOIN engine.activity_history h ON h.id=a.activity_id
		  JOIN engine.scheduled_job_run_targets t ON t.run_id=r.id
		 WHERE COALESCE(j.job_kind, '')='agent_maintenance'
		   AND UPPER(COALESCE(t.device_guid, ''))=UPPER($1)
		   AND (`
	args := []any{guid}
	if request.ScheduledJobRunID > 0 {
		query += `r.id=$2 OR COALESCE(h.metadata_json, '') LIKE $3 OR COALESCE(h.stdout, '') LIKE $3)`
		args = append(args, request.ScheduledJobRunID, "%"+request.OperationID+"%")
	} else {
		query += `COALESCE(h.metadata_json, '') LIKE $2 OR COALESCE(h.stdout, '') LIKE $2)`
		args = append(args, "%"+request.OperationID+"%")
	}
	query += ` ORDER BY r.id DESC LIMIT 1 FOR UPDATE OF r, h`
	var ref agentUpdateProgressRef
	var metadataJSON string
	err := tx.QueryRowContext(ctx, query, args...).Scan(&ref.JobID, &ref.RunID, &ref.ActivityID, &ref.RunStatus, &ref.Hostname, &metadataJSON)
	if err != nil {
		return agentUpdateProgressRef{}, err
	}
	if strings.TrimSpace(metadataJSON) != "" {
		_ = json.Unmarshal([]byte(metadataJSON), &ref.Metadata)
	}
	if ref.Metadata == nil {
		ref.Metadata = map[string]any{}
	}
	ref.GUID = guid
	return ref, nil
}

func createHourlyAgentUpdateProgressRef(ctx context.Context, tx *sql.Tx, guid string, request agentUpdateProgressRequest) (agentUpdateProgressRef, error) {
	var device agentMaintenanceDevice
	err := tx.QueryRowContext(ctx, `
		SELECT d.guid, COALESCE(d.hostname, ''), COALESCE(d.agent_id, ''), ds.site_id, COALESCE(s.name, '')
		  FROM engine.devices d
		  LEFT JOIN engine.device_sites ds ON ds.device_hostname=d.hostname
		  LEFT JOIN engine.sites s ON s.id=ds.site_id
		 WHERE UPPER(d.guid)=UPPER($1)
		 LIMIT 1
	`, guid).Scan(&device.GUID, &device.Hostname, &device.AgentID, &device.SiteID, &device.SiteName)
	if err != nil {
		return agentUpdateProgressRef{}, err
	}
	now := time.Now().Unix()
	componentsJSON, _ := json.Marshal([]map[string]any{{"kind": agentMaintenanceJobKind, "action": agentMaintenanceUpdateAction, "source": "hourly_update_checker"}})
	targetsJSON, _ := json.Marshal([]map[string]any{{"kind": "device", "device_guid": device.GUID, "hostname": device.Hostname, "site_id": nullableInt(device.SiteID), "site_name": device.SiteName}})
	var jobID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO engine.scheduled_jobs(
			name, components_json, targets_json, schedule_type, start_ts,
			duration_stop_enabled, expiration, execution_context, credential_id,
			use_service_account, job_kind, enabled, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id
	`, agentMaintenanceJobName(agentMaintenanceUpdateAction), string(componentsJSON), string(targetsJSON), "immediately", now, 0, "no_expire", "system", nil, 1, agentMaintenanceJobKind, 0, now, now).Scan(&jobID); err != nil {
		return agentUpdateProgressRef{}, err
	}
	run, err := createAgentMaintenanceRun(ctx, tx, jobID, agentMaintenanceJobName(agentMaintenanceUpdateAction), device, agentMaintenanceRequest{Action: agentMaintenanceUpdateAction}, operatorProfile{Username: "Hourly Update Checker", Role: "Admin"}, now)
	if err != nil {
		return agentUpdateProgressRef{}, err
	}
	run.Metadata["operation_id"] = request.OperationID
	run.Metadata["requested_by"] = "Hourly Update Checker"
	run.Metadata["agent_update_source"] = "hourly_update_checker"
	metadataJSON, _ := json.Marshal(run.Metadata)
	if _, err := tx.ExecContext(ctx, `UPDATE engine.activity_history SET metadata_json=$1 WHERE id=$2`, string(metadataJSON), run.ActivityID); err != nil {
		return agentUpdateProgressRef{}, err
	}
	return agentUpdateProgressRef{JobID: jobID, RunID: run.RunID, ActivityID: run.ActivityID, RunStatus: "Pending", Hostname: device.Hostname, GUID: device.GUID, Metadata: run.Metadata}, nil
}

func mergeAgentUpdateProgressEvent(events []map[string]any, event map[string]any) ([]map[string]any, bool) {
	eventID := cleanText(event["event_id"])
	if eventID != "" {
		for _, existing := range events {
			if cleanText(existing["event_id"]) == eventID {
				return events, false
			}
		}
	}
	events = append(events, event)
	if len(events) > 256 {
		events = append([]map[string]any(nil), events[len(events)-256:]...)
	}
	return events, true
}

func agentUpdateStatusFromEvent(current string, event map[string]any) string {
	if terminal := cleanText(event["terminal_status"]); terminal != "" {
		return terminal
	}
	state := cleanText(event["state"])
	if state == "failed" {
		return "running"
	}
	if current == "" || current == "pending" {
		return "running"
	}
	return current
}

func agentUpdateRunStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "completed", "complete":
		return "Success"
	case "failed", "error":
		return "Failed"
	case "timed_out", "timeout":
		return "Timed Out"
	case "skipped":
		return "Skipped"
	case "pending", "requested":
		return "Pending"
	default:
		return "Running"
	}
}

func agentUpdateActivityOutput(events []map[string]any) (string, string) {
	stdout := strings.Builder{}
	stderr := strings.Builder{}
	for _, event := range events {
		line := fmt.Sprintf("Agent update phase=%s state=%s summary=%s\n", cleanText(event["phase_id"]), cleanText(event["state"]), cleanText(event["summary"]))
		if cleanText(event["state"]) == "failed" || cleanText(event["state"]) == "timed_out" {
			stderr.WriteString(line)
			if detail := cleanText(event["detail"]); detail != "" {
				stderr.WriteString("Agent update detail=" + detail + "\n")
			}
		} else {
			stdout.WriteString(line)
		}
	}
	return stdout.String(), stderr.String()
}

func (s *postgresOperatorStore) agentUpdateHistory(ctx context.Context, profile operatorProfile, deviceGUID string, operationID string, limit int) (map[string]any, int, error) {
	devices, err := s.loadAgentMaintenanceDevices(ctx, profile, []string{deviceGUID})
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if len(devices) == 0 {
		return nil, http.StatusNotFound, errors.New("device_not_found")
	}
	device := devices[0]
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	query := `
		SELECT j.id, r.id, h.id, COALESCE(j.name, ''), COALESCE(r.status, ''),
		       r.started_ts, r.finished_ts, r.updated_at,
		       COALESCE(h.metadata_json, ''), COALESCE(h.stdout, ''), COALESCE(h.stderr, '')
		  FROM engine.scheduled_job_runs r
		  JOIN engine.scheduled_jobs j ON j.id=r.job_id
		  JOIN engine.scheduled_job_run_activity a ON a.run_id=r.id
		  JOIN engine.activity_history h ON h.id=a.activity_id
		  JOIN engine.scheduled_job_run_targets t ON t.run_id=r.id
		 WHERE COALESCE(j.job_kind, '')='agent_maintenance'
		   AND UPPER(COALESCE(t.device_guid, ''))=UPPER($1)`
	args := []any{device.GUID}
	if operationID != "" {
		query += ` AND (COALESCE(h.metadata_json, '') LIKE $2 OR COALESCE(h.stdout, '') LIKE $2)`
		args = append(args, "%"+operationID+"%")
	}
	query += fmt.Sprintf(" ORDER BY COALESCE(r.started_ts, r.created_at) DESC LIMIT %d", limit)
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		conn.Close()
		return nil, http.StatusInternalServerError, err
	}
	rawRows := []agentUpdateHistoryRow{}
	for rows.Next() {
		var row agentUpdateHistoryRow
		if err := rows.Scan(&row.JobID, &row.RunID, &row.ActivityID, &row.JobName, &row.RunStatus, &row.StartedTS, &row.FinishedTS, &row.UpdatedAt, &row.MetadataJSON, &row.Stdout, &row.Stderr); err != nil {
			rows.Close()
			conn.Close()
			return nil, http.StatusInternalServerError, err
		}
		rawRows = append(rawRows, row)
	}
	rowErr := rows.Err()
	rows.Close()
	conn.Close()
	if rowErr != nil {
		return nil, http.StatusInternalServerError, rowErr
	}

	operations := make([]map[string]any, 0, len(rawRows))
	for _, row := range rawRows {
		metadata := map[string]any{}
		if row.MetadataJSON.Valid && strings.TrimSpace(row.MetadataJSON.String) != "" {
			_ = json.Unmarshal([]byte(row.MetadataJSON.String), &metadata)
		}
		update := schedulerAnyMap(metadata["agent_update"])
		operation := map[string]any{
			"operation_id":           firstText(cleanText(update["operation_id"]), cleanText(metadata["operation_id"])),
			"scheduled_job_id":       row.JobID,
			"scheduled_job_run_id":   row.RunID,
			"activity_id":            row.ActivityID,
			"device_guid":            device.GUID,
			"hostname":               device.Hostname,
			"source":                 firstText(cleanText(update["source"]), cleanText(metadata["agent_update_source"]), "operator_initiated"),
			"requested_by":           firstText(cleanText(update["requested_by"]), cleanText(metadata["requested_by"])),
			"target_build_id":        cleanText(update["target_build_id"]),
			"installed_build_before": cleanText(update["installed_build_before"]),
			"installed_build_after":  cleanText(update["installed_build_after"]),
			"started_at":             firstPositiveInt64(coerceInt64(update["started_at"]), nullInt(row.StartedTS)),
			"ended_at":               firstPositiveInt64(coerceInt64(update["completed_at"]), nullInt(row.FinishedTS)),
			"updated_at":             firstPositiveInt64(coerceInt64(update["updated_at"]), nullInt(row.UpdatedAt)),
			"status":                 agentUpdateHistoryStatus(cleanText(update["status"]), row.RunStatus),
			"failure_summary":        firstText(cleanText(update["failure_summary"]), truncateMetadataText(row.Stderr.String, 512)),
			"events":                 mapSlice(update["events"]),
			"stdout":                 row.Stdout.String,
			"stderr":                 row.Stderr.String,
		}
		operations = append(operations, operation)
	}
	sort.SliceStable(operations, func(i, j int) bool {
		return coerceInt64(operations[i]["started_at"]) > coerceInt64(operations[j]["started_at"])
	})
	active := map[string]any(nil)
	for _, operation := range operations {
		if agentUpdateRunStatus(cleanText(operation["status"])) == "Running" || agentUpdateRunStatus(cleanText(operation["status"])) == "Pending" {
			active = operation
			break
		}
	}
	return map[string]any{
		"status":           "ok",
		"device_guid":      device.GUID,
		"hostname":         device.Hostname,
		"active_operation": active,
		"operations":       operations,
	}, http.StatusOK, nil
}

func agentUpdateHistoryStatus(updateStatus string, runStatus string) string {
	normalizedRun := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(runStatus), " ", "_"))
	if normalizedRun == "success" || normalizedRun == "failed" || normalizedRun == "timed_out" || normalizedRun == "skipped" {
		return normalizedRun
	}
	return firstText(strings.ToLower(strings.TrimSpace(updateStatus)), normalizedRun, "pending")
}
