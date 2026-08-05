package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	agentMaintenanceUpdateAction = "update_now"
	agentMaintenanceSwitchAction = "switch_branch_channel"
	agentMaintenanceJobKind      = "agent_maintenance"
	agentMaintenanceLane         = "scheduled_job"
)

type agentMaintenanceStore interface {
	queueAgentMaintenance(ctx context.Context, profile operatorProfile, request agentMaintenanceRequest) (map[string]any, int, error)
}

type agentMaintenanceRequest struct {
	Action         string
	ReleaseChannel string
	Branch         string
	DeviceGUIDs    []string
}

type agentMaintenanceDevice struct {
	GUID                string
	Hostname            string
	AgentID             string
	AgentReleaseChannel string
	AgentBranch         string
	SiteID              sql.NullInt64
	SiteName            string
}

type agentMaintenanceRunRef struct {
	RunID      int64
	ActivityID int64
	Metadata   map[string]any
}

func registerAgentMaintenanceRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("POST /api/devices/agent-maintenance", agentMaintenanceBulkHandler(auth))
	mux.HandleFunc("POST /api/device/update-agent/{hostname}", deviceAgentUpdateHandler(auth))
}

func agentMaintenanceBulkHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		body, err := readJSONMap(r)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		action := normalizeAgentMaintenanceAction(firstNonEmpty(body["action"], body["kind"], agentMaintenanceUpdateAction))
		if action == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_action"})
			return
		}
		if action == agentMaintenanceSwitchAction && !strings.EqualFold(cleanText(profile.Role), "admin") {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
			return
		}
		channel := normalizeAgentMaintenanceChannel(firstNonEmpty(body["release_channel"], body["channel"]))
		if channel != "stable" && channel != "unstable" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_channel"})
			return
		}
		branch := normalizeAgentMaintenanceBranch(channel, body["branch"])
		guids := normalizeAgentMaintenanceGUIDs(firstNonEmpty(body["guids"], body["device_guids"], body["devices"]))
		if len(guids) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "no_devices_targeted"})
			return
		}
		store, ok := auth.store.(agentMaintenanceStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "agent_maintenance_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		payload, status, err := store.queueAgentMaintenance(ctx, profile, agentMaintenanceRequest{
			Action:         action,
			ReleaseChannel: channel,
			Branch:         branch,
			DeviceGUIDs:    guids,
		})
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, status, payload)
	}
}

func deviceAgentUpdateHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		store, ok := auth.store.(deviceProcessStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "agent_update_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		snapshot, status, err := store.loadDeviceProcessContext(ctx, profile, r.PathValue("hostname"))
		cancel()
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		if snapshot.Route == nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "agent_unavailable",
				"message": "The agent SYSTEM socket is not available to start the local AutoUpdater task.",
			})
			return
		}

		requestedAt := time.Now().Unix()
		operationID := newUUIDString()
		eventPayload := map[string]any{
			"operation_id": operationID,
			"hostname":     firstText(snapshot.Hostname, r.PathValue("hostname")),
			"agent_id":     snapshot.AgentID,
			"requested_at": requestedAt,
			"requested_by": firstText(profile.Username, "unknown"),
		}
		response, _, workerErr := callWorkerHostServiceEvent(r.Context(), auth, snapshot.Route, map[string]any{
			"hostname":        firstText(snapshot.Hostname, r.PathValue("hostname")),
			"service_mode":    "system",
			"event_name":      "agent_update_request",
			"timeout_seconds": 30.0,
			"payload":         eventPayload,
		}, 31*time.Second)
		if workerErr != nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "agent_unavailable",
				"message": agentUpdateUnavailableMessage(workerErr),
			})
			return
		}
		if strings.EqualFold(cleanText(response["status"]), "error") {
			detail := firstText(cleanText(response["detail"]), cleanText(response["message"]), cleanText(response["error"]), "Agent rejected the AutoUpdater request.")
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "agent_unavailable",
				"message": "The agent rejected the local AutoUpdater request: " + detail,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":       "queued",
			"hostname":     firstText(snapshot.Hostname, r.PathValue("hostname")),
			"agent_id":     snapshot.AgentID,
			"operation_id": operationID,
			"requested_at": requestedAt,
		})
	}
}

func (s *postgresOperatorStore) queueAgentMaintenance(ctx context.Context, profile operatorProfile, request agentMaintenanceRequest) (map[string]any, int, error) {
	devices, err := s.loadAgentMaintenanceDevices(ctx, profile, request.DeviceGUIDs)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if len(devices) == 0 {
		return map[string]any{"error": "no_devices_targeted"}, http.StatusBadRequest, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
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

	now := time.Now().Unix()
	jobName := agentMaintenanceJobName(request.Action)
	component := map[string]any{
		"kind":            agentMaintenanceJobKind,
		"action":          request.Action,
		"release_channel": request.ReleaseChannel,
		"branch":          request.Branch,
	}
	targets := make([]map[string]any, 0, len(devices))
	for _, device := range devices {
		targets = append(targets, map[string]any{
			"kind":        "device",
			"device_guid": device.GUID,
			"hostname":    device.Hostname,
			"site_id":     nullableInt(device.SiteID),
			"site_name":   device.SiteName,
		})
	}
	componentsJSON, err := json.Marshal([]map[string]any{component})
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	targetsJSON, err := json.Marshal(targets)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	var jobID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO engine.scheduled_jobs(
			name, components_json, targets_json, schedule_type, start_ts,
			duration_stop_enabled, expiration, execution_context, credential_id,
			use_service_account, job_kind, enabled, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id
	`, jobName, string(componentsJSON), string(targetsJSON), "immediately", now, 0, "no_expire", "system", nil, 1, agentMaintenanceJobKind, 0, now, now).Scan(&jobID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	queued := make([]map[string]any, 0, len(devices))
	errorsList := make([]map[string]any, 0)
	for _, device := range devices {
		runRef, err := createAgentMaintenanceRun(ctx, tx, jobID, jobName, device, request, profile, now)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		operationID := newUUIDString()
		targetChannel := request.ReleaseChannel
		targetBranch := request.Branch
		if request.Action == agentMaintenanceUpdateAction {
			targetChannel = normalizeAgentMaintenanceChannel(device.AgentReleaseChannel)
			targetBranch = normalizeAgentMaintenanceBranch(targetChannel, firstText(device.AgentBranch, request.Branch))
		} else if device.GUID == "" {
			stderr := "Device GUID unavailable for branch/channel switch.\n"
			if err := updateAgentMaintenanceRun(ctx, tx, runRef, "Failed", "", stderr, operationID); err != nil {
				return nil, http.StatusInternalServerError, err
			}
			errorsList = append(errorsList, map[string]any{"hostname": device.Hostname, "guid": device.GUID, "error": "missing_guid", "run_id": runRef.RunID})
			continue
		} else if err := updateAgentMaintenanceDeviceChannel(ctx, tx, device.GUID, targetChannel, targetBranch); err != nil {
			stderr := fmt.Sprintf("Failed to update Agent branch/channel selection: %v\n", err)
			if updateErr := updateAgentMaintenanceRun(ctx, tx, runRef, "Failed", "", stderr, operationID); updateErr != nil {
				return nil, http.StatusInternalServerError, updateErr
			}
			errorsList = append(errorsList, map[string]any{"hostname": device.Hostname, "guid": device.GUID, "error": "channel_update_failed", "run_id": runRef.RunID})
			continue
		}

		eventPayload := map[string]any{
			"operation_id":         operationID,
			"kind":                 request.Action,
			"action":               request.Action,
			"hostname":             device.Hostname,
			"guid":                 device.GUID,
			"release_channel":      targetChannel,
			"channel":              targetChannel,
			"target_channel":       targetChannel,
			"branch":               targetBranch,
			"target_branch":        targetBranch,
			"requested_at":         now,
			"requested_by":         firstText(profile.Username, "unknown"),
			"scheduled_job_id":     jobID,
			"scheduled_job_run_id": runRef.RunID,
		}
		stdout := fmt.Sprintf("Queued operation_id=%s for site-worker dispatch release_channel=%s branch=%s\n", operationID, targetChannel, targetBranch)
		if err := updateAgentMaintenanceRun(ctx, tx, runRef, "Pending", stdout, "", operationID); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		workID, err := enqueueAgentMaintenanceWorkItem(ctx, tx, jobID, runRef.RunID, now, device, operationID, request.Action, targetChannel, targetBranch, eventPayload)
		if err != nil {
			stderr := fmt.Sprintf("Failed to queue site-worker agent maintenance dispatch: %v\n", err)
			if updateErr := updateAgentMaintenanceRun(ctx, tx, runRef, "Failed", "", stderr, operationID); updateErr != nil {
				return nil, http.StatusInternalServerError, updateErr
			}
			errorsList = append(errorsList, map[string]any{"hostname": device.Hostname, "guid": device.GUID, "error": "queue_failed", "run_id": runRef.RunID})
			continue
		}
		queued = append(queued, map[string]any{
			"hostname":     device.Hostname,
			"guid":         device.GUID,
			"operation_id": operationID,
			"run_id":       runRef.RunID,
			"work_id":      workID,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	committed = true

	status := http.StatusOK
	state := "queued"
	if len(queued) == 0 {
		status = http.StatusConflict
		state = "failed"
	}
	return map[string]any{
		"status": state,
		"job_id": jobID,
		"queued": queued,
		"errors": errorsList,
	}, status, nil
}

func (s *postgresOperatorStore) loadAgentMaintenanceDevices(ctx context.Context, profile operatorProfile, guids []string) ([]agentMaintenanceDevice, error) {
	normalized := uniqueAgentMaintenanceGUIDs(guids)
	if len(normalized) == 0 {
		return []agentMaintenanceDevice{}, nil
	}
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return []agentMaintenanceDevice{}, nil
	}
	allowedSet := int64Set(allowedSiteIDs)

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	upperGUIDs := make([]string, 0, len(normalized))
	for _, guid := range normalized {
		upperGUIDs = append(upperGUIDs, strings.ToUpper(guid))
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT d.guid,
		       COALESCE(d.hostname, ''),
		       COALESCE(d.agent_id, ''),
		       COALESCE(d.agent_release_channel, ''),
		       COALESCE(d.agent_branch, ''),
		       ds.site_id,
		       COALESCE(s.name, '')
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites AS s ON s.id = ds.site_id
		 WHERE UPPER(d.guid) = ANY($1)
	  ORDER BY d.hostname ASC
	`, pq.Array(upperGUIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	devices := make([]agentMaintenanceDevice, 0)
	for rows.Next() {
		var device agentMaintenanceDevice
		if err := rows.Scan(&device.GUID, &device.Hostname, &device.AgentID, &device.AgentReleaseChannel, &device.AgentBranch, &device.SiteID, &device.SiteName); err != nil {
			return nil, err
		}
		device.GUID = normalizeCanonicalGUID(device.GUID)
		device.Hostname = cleanText(device.Hostname)
		device.AgentID = cleanText(device.AgentID)
		device.AgentReleaseChannel = normalizeAgentMaintenanceChannel(device.AgentReleaseChannel)
		device.AgentBranch = normalizeAgentMaintenanceBranch(device.AgentReleaseChannel, device.AgentBranch)
		device.SiteName = cleanText(device.SiteName)
		if device.Hostname == "" {
			continue
		}
		if allowedSiteIDs != nil {
			if !device.SiteID.Valid {
				continue
			}
			if _, ok := allowedSet[device.SiteID.Int64]; !ok {
				continue
			}
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return devices, nil
}

func createAgentMaintenanceRun(ctx context.Context, tx *sql.Tx, jobID int64, jobName string, device agentMaintenanceDevice, request agentMaintenanceRequest, profile operatorProfile, now int64) (agentMaintenanceRunRef, error) {
	var runID int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO engine.scheduled_job_runs(
			job_id, target_hostname, scheduled_ts, started_ts, status,
			created_at, updated_at, component_index, component_kind, component_name
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id
	`, jobID, device.Hostname, now, now, "Pending", now, now, 0, agentMaintenanceJobKind, jobName).Scan(&runID)
	if err != nil {
		return agentMaintenanceRunRef{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO engine.scheduled_job_run_targets(
			run_id, device_guid, hostname, site_id, resolution_status, resolution_reason, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, runID, device.GUID, device.Hostname, nullableInt(device.SiteID), "pending", "", now)
	if err != nil {
		return agentMaintenanceRunRef{}, err
	}
	metadata := map[string]any{
		"scheduled_job_id":       jobID,
		"scheduled_job_run_id":   runID,
		"component_kind":         agentMaintenanceJobKind,
		"operation_id":           "",
		"requested_by":           firstText(profile.Username, "unknown"),
		"target_release_channel": request.ReleaseChannel,
		"target_branch":          request.Branch,
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return agentMaintenanceRunRef{}, err
	}
	stdout := fmt.Sprintf("Requested action=%s release_channel=%s branch=%s\n", request.Action, request.ReleaseChannel, request.Branch)
	var activityID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO engine.activity_history(
			hostname, script_path, script_name, script_type, ran_at, status,
			stdout, stderr, queue_lane, activity_kind, metadata_json,
			started_at, updated_at, finished_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id
	`, device.Hostname, "Internal/Agent_Maintenance", jobName, agentMaintenanceJobKind, now, "Queued", stdout, "", agentMaintenanceJobKind, "scheduled_job", string(metadataJSON), nil, now, nil).Scan(&activityID)
	if err != nil {
		return agentMaintenanceRunRef{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO engine.scheduled_job_run_activity(
			run_id, activity_id, component_kind, script_type, component_path, component_name, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, runID, activityID, agentMaintenanceJobKind, agentMaintenanceJobKind, "Internal/Agent_Maintenance", jobName, now)
	if err != nil {
		return agentMaintenanceRunRef{}, err
	}
	return agentMaintenanceRunRef{RunID: runID, ActivityID: activityID, Metadata: metadata}, nil
}

func updateAgentMaintenanceDeviceChannel(ctx context.Context, tx *sql.Tx, guid string, channel string, branch string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE engine.devices
		   SET agent_release_channel_override=$1,
		       agent_release_channel=$2,
		       agent_branch=$3
		 WHERE UPPER(guid)=UPPER($4)
	`, channel, channel, branch, guid)
	return err
}

func updateAgentMaintenanceRun(ctx context.Context, tx *sql.Tx, run agentMaintenanceRunRef, status string, stdout string, stderr string, operationID string) error {
	now := time.Now().Unix()
	var finished any
	if status == "Success" || status == "Failed" || status == "Skipped" {
		finished = now
	}
	errorText := stderr
	if len(errorText) > 512 {
		errorText = errorText[:512]
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_runs
		   SET status=$1,
		       updated_at=$2,
		       finished_ts=COALESCE($3, finished_ts),
		       error=$4
		 WHERE id=$5
	`, status, now, finished, errorText, run.RunID)
	if err != nil {
		return err
	}
	resolutionStatus := "eligible"
	if status == "Failed" {
		resolutionStatus = "unresolved"
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE engine.scheduled_job_run_targets
		   SET resolution_status=$1,
		       resolution_reason=$2
		 WHERE run_id=$3
	`, resolutionStatus, errorText, run.RunID)
	if err != nil {
		return err
	}
	metadata := copyMap(run.Metadata)
	if operationID != "" {
		metadata["operation_id"] = operationID
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE engine.activity_history
		   SET status=$1,
		       stdout=COALESCE(stdout, '') || $2,
		       stderr=COALESCE(stderr, '') || $3,
		       metadata_json=$4,
		       updated_at=$5,
		       finished_at=COALESCE($6, finished_at)
		 WHERE id=$7
	`, status, stdout, stderr, string(metadataJSON), now, finished, run.ActivityID)
	return err
}

func enqueueAgentMaintenanceWorkItem(ctx context.Context, tx *sql.Tx, jobID int64, runID int64, scheduledTS int64, device agentMaintenanceDevice, operationID string, action string, releaseChannel string, branch string, eventPayload map[string]any) (int64, error) {
	label := "Update Borealis Agent"
	if action == agentMaintenanceSwitchAction {
		label = "Switch Agent Branch/Channel"
	}
	taskLink := map[string]any{
		"kind":   agentMaintenanceJobKind,
		"label":  label,
		"job_id": jobID,
		"run_id": runID,
		"path":   fmt.Sprintf("/jobs/%d?tab=job_history", jobID),
	}
	payload := map[string]any{
		"job_id":          jobID,
		"run_id":          runID,
		"scheduled_ts":    scheduledTS,
		"hostname":        device.Hostname,
		"operation_id":    operationID,
		"action":          action,
		"release_channel": releaseChannel,
		"branch":          branch,
		"service_mode":    "system",
		"event_name":      "agent_maintenance_request",
		"event_payload":   eventPayload,
		"task_link":       taskLink,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	dedupe := fmt.Sprintf("agent-maintenance:%d:%s", runID, firstText(operationID, device.Hostname))
	var existingID sql.NullInt64
	err = tx.QueryRowContext(ctx, "SELECT id FROM engine.job_scheduler_work_items WHERE dedupe_key=$1 LIMIT 1", dedupe).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if existingID.Valid {
		_, err = tx.ExecContext(ctx, "UPDATE engine.job_scheduler_work_items SET updated_at=$1 WHERE id=$2", now, existingID.Int64)
		return existingID.Int64, err
	}
	var workID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO engine.job_scheduler_work_items(
			dedupe_key, kind, site_id, lane, job_id, run_id, target_id, payload_json,
			status, attempt_count, priority, available_at, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id
	`, dedupe, "agent_maintenance_run", agentMaintenanceSiteScope(device.SiteID), agentMaintenanceLane, jobID, runID, nil, string(payloadJSON), workStatusQueued, 0, 45, now, now, now).Scan(&workID)
	return workID, err
}

func normalizeAgentMaintenanceAction(value any) string {
	text := strings.ToLower(cleanText(value))
	switch text {
	case "switch", "switch_channel", "switch_branch", "switch_branch_channel":
		return agentMaintenanceSwitchAction
	case "", "update", "update_agent", "update_now":
		return agentMaintenanceUpdateAction
	default:
		return ""
	}
}

func normalizeAgentMaintenanceChannel(value any) string {
	text := strings.ToLower(cleanText(value))
	switch text {
	case "source", "branch", "repo", "repository", "unstable":
		return "unstable"
	case "", "release", "releases", "stable":
		return "stable"
	default:
		return text
	}
}

func normalizeAgentMaintenanceBranch(channel string, branch any) string {
	if normalizeAgentMaintenanceChannel(channel) != "unstable" {
		return "main"
	}
	return firstText(cleanText(branch), "main")
}

func normalizeAgentMaintenanceGUIDs(value any) []string {
	values := []any{}
	switch typed := value.(type) {
	case []any:
		values = typed
	case []string:
		for _, item := range typed {
			values = append(values, item)
		}
	case map[string]any:
		values = append(values, firstNonEmpty(typed["guid"], typed["device_guid"], typed["agent_guid"], typed["id"]))
	case string:
		values = append(values, typed)
	default:
		if cleanText(typed) != "" {
			values = append(values, typed)
		}
	}
	guids := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		if row, ok := value.(map[string]any); ok {
			value = firstNonEmpty(row["guid"], row["device_guid"], row["agent_guid"], row["id"])
		}
		guid := normalizeCanonicalGUID(value)
		if guid == "" {
			continue
		}
		key := strings.ToUpper(guid)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		guids = append(guids, guid)
	}
	return guids
}

func uniqueAgentMaintenanceGUIDs(values []string) []string {
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		guid := normalizeCanonicalGUID(value)
		if guid == "" {
			continue
		}
		key := strings.ToUpper(guid)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, guid)
	}
	sort.Slice(unique, func(i, j int) bool { return strings.ToUpper(unique[i]) < strings.ToUpper(unique[j]) })
	return unique
}

func agentMaintenanceJobName(action string) string {
	if action == agentMaintenanceSwitchAction {
		return "Switch Agent Branch/Channel"
	}
	return "Update Borealis Agent"
}

func agentUpdateUnavailableMessage(workerErr map[string]any) string {
	detail := firstText(cleanText(workerErr["detail"]), cleanText(workerErr["message"]), cleanText(workerErr["error"]))
	if strings.TrimSpace(detail) == "" {
		return "The agent SYSTEM socket is not available to start the local AutoUpdater task."
	}
	if strings.EqualFold(cleanText(workerErr["error"]), "agent_error") {
		return "The agent rejected the local AutoUpdater request: " + detail
	}
	return "The agent SYSTEM socket is not available to start the local AutoUpdater task."
}

func agentMaintenanceSiteScope(siteID sql.NullInt64) int64 {
	if siteID.Valid {
		return siteID.Int64
	}
	return 0
}

func newUUIDString() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

var _ agentMaintenanceStore = (*postgresOperatorStore)(nil)
