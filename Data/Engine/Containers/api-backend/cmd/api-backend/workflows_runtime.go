package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	workflowStatusPending  = "Pending"
	workflowStatusRunning  = "Running"
	workflowStatusSuccess  = "Success"
	workflowStatusWarning  = "Warning"
	workflowStatusFailed   = "Failed"
	workflowStatusTimedOut = "Timed Out"
	workflowStatusSkipped  = "Skipped"

	workflowNodeTriggerManual    = "workflow_trigger_manual"
	workflowNodeTriggerScheduled = "workflow_trigger_scheduled_job"
	workflowNodeTriggerWebhook   = "workflow_trigger_webhook"
	workflowNodeAgentFilter      = "workflow_agent_filter"
	workflowNodeAgentArray       = "workflow_agent_array"
	workflowNodeExecuteAssembly  = "workflow_execute_assembly"
	workflowNodeExecuteWorkflow  = "workflow_execute_subworkflow"

	workflowPortKindAction = "action"
	workflowPortKindData   = "data"

	workflowEdgeAlways   = "always"
	workflowEdgeSuccess  = "on_success"
	workflowEdgeWarning  = "on_warning"
	workflowEdgeFailed   = "on_failed"
	workflowEngineHost   = "borealis-engine-01"
	workflowPollInterval = 500 * time.Millisecond
)

type workflowStartRequest struct {
	WorkflowGUID     string
	SourceType       string
	SourceMetadata   map[string]any
	CreatedBy        string
	ExecuteAsync     bool
	RunnerProfile    operatorProfile
	Auth             *authService
	OperatorRealtime *operatorRealtimeHub
}

type workflowStartResult struct {
	Payload       map[string]any
	RunID         int64
	ShouldExecute bool
}

type workflowLoadedDocument struct {
	Export   map[string]any
	Document map[string]any
}

type workflowActivityResult struct {
	ID       int64
	Hostname string
	Status   string
	Stdout   string
	Stderr   string
}

func workflowCreatedBy(profile operatorProfile) string {
	username := strings.TrimSpace(profile.Username)
	role := strings.TrimSpace(profile.Role)
	if username != "" && role != "" {
		return username + " (" + role + ")"
	}
	return username
}

func mapStringAny(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return copyMap(typed)
	}
	return map[string]any{}
}

func workflowErrorPayload(err error, status int) map[string]any {
	message := ""
	if err != nil {
		message = err.Error()
	}
	if status == http.StatusBadRequest {
		return map[string]any{"error": "validation_failed", "message": message}
	}
	if status == http.StatusNotFound {
		return map[string]any{"error": "not found"}
	}
	if message == "" {
		message = "workflow_error"
	}
	return map[string]any{"error": message}
}

func (s *postgresOperatorStore) startWorkflowRun(ctx context.Context, req workflowStartRequest) (workflowStartResult, int, error) {
	req.WorkflowGUID = assemblyCoerceGUID(req.WorkflowGUID)
	req.SourceType = strings.ToLower(strings.TrimSpace(req.SourceType))
	if req.SourceType == "" {
		req.SourceType = "manual"
	}
	if req.WorkflowGUID == "" {
		return workflowStartResult{}, http.StatusBadRequest, errors.New("workflow_guid is required")
	}
	if workflowTriggerNodeForSource(req.SourceType) == "" {
		return workflowStartResult{}, http.StatusBadRequest, fmt.Errorf("Unsupported workflow trigger source '%s'.", req.SourceType)
	}
	loaded, err := s.loadWorkflowDocument(ctx, req.WorkflowGUID)
	if err != nil {
		return workflowStartResult{}, http.StatusBadRequest, err
	}
	if validation := workflowValidateDocument(req.WorkflowGUID, loaded.Document, req.SourceType, req.SourceMetadata); len(validation) > 0 {
		return workflowStartResult{}, http.StatusBadRequest, errors.New(strings.Join(validation, "; "))
	}
	sourceMetadata := copyMap(req.SourceMetadata)
	if sourceMetadata == nil {
		sourceMetadata = map[string]any{}
	}
	sourceMetadata["workflow_guid"] = req.WorkflowGUID
	now := time.Now().Unix()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return workflowStartResult{}, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	activeRunID, err := workflowActiveRunForWorkflow(ctx, conn, req.WorkflowGUID, sourceMetadata)
	if err != nil {
		return workflowStartResult{}, http.StatusInternalServerError, err
	}
	status := workflowStatusPending
	skipReason := ""
	shouldExecute := true
	if activeRunID.Valid {
		_ = activeRunID
		status = workflowStatusSkipped
		skipReason = "workflow_already_running"
		shouldExecute = false
	}
	runID, err := workflowInsertRun(ctx, conn, req.WorkflowGUID, workflowDocumentName(loaded), req.SourceType, sourceMetadata, loaded.Document, status, req.CreatedBy, now, skipReason)
	if err != nil {
		return workflowStartResult{}, http.StatusInternalServerError, err
	}
	if !shouldExecute {
		_ = workflowMirrorScheduledRun(ctx, conn, runID, sourceMetadata, workflowStatusSkipped, "A workflow run is already active for this saved workflow.")
	}
	run, found, err := s.getWorkflowRun(ctx, runID)
	if err != nil {
		return workflowStartResult{}, http.StatusInternalServerError, err
	}
	if !found {
		run = map[string]any{"id": runID, "status": status}
	}
	return workflowStartResult{
		Payload:       map[string]any{"started": shouldExecute, "run": run},
		RunID:         runID,
		ShouldExecute: shouldExecute,
	}, http.StatusOK, nil
}

func (s *postgresOperatorStore) triggerWorkflowWebhook(ctx context.Context, opaqueToken string) (workflowStartResult, int, error) {
	opaqueToken = strings.TrimSpace(opaqueToken)
	if opaqueToken == "" {
		return workflowStartResult{}, http.StatusNotFound, errors.New("not found")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return workflowStartResult{}, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	var webhook workflowWebhookRow
	err = conn.QueryRowContext(ctx, `
		SELECT id, workflow_guid, opaque_token, created_at, creator_username, creator_role, last_used_at
		  FROM engine.workflow_webhooks
		 WHERE opaque_token=$1
	`, opaqueToken).Scan(&webhook.ID, &webhook.WorkflowGUID, &webhook.OpaqueToken, &webhook.CreatedAt, &webhook.CreatorUsername, &webhook.CreatorRole, &webhook.LastUsedAt)
	_ = conn.Close()
	if errors.Is(err, sql.ErrNoRows) {
		return workflowStartResult{}, http.StatusNotFound, errors.New("not found")
	}
	if err != nil {
		return workflowStartResult{}, http.StatusInternalServerError, err
	}
	workflowGUID := assemblyCoerceGUID(nullString(webhook.WorkflowGUID))
	result, status, err := s.startWorkflowRun(ctx, workflowStartRequest{
		WorkflowGUID: workflowGUID,
		SourceType:   "webhook",
		SourceMetadata: map[string]any{
			"workflow_guid": workflowGUID,
			"webhook_id":    int64OrZero(webhook.ID),
			"created_by":    "Webhook",
		},
		CreatedBy:     "Webhook",
		ExecuteAsync:  true,
		RunnerProfile: operatorProfile{Username: "Webhook", Role: "Admin"},
	})
	if err != nil {
		return workflowStartResult{}, status, err
	}
	conn, err = s.db.Conn(ctx)
	if err == nil {
		_, _ = conn.ExecContext(ctx, "UPDATE engine.workflow_webhooks SET last_used_at=$1 WHERE id=$2", time.Now().Unix(), int64OrZero(webhook.ID))
		_ = conn.Close()
	}
	return result, status, nil
}

func (s *postgresOperatorStore) workflowEditorAccess(ctx context.Context, profile operatorProfile, workflowGUID string) (map[string]any, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if allowedSiteIDs == nil {
		return map[string]any{"allowed": true, "hidden_devices": []map[string]any{}, "hidden_filters": []map[string]any{}, "message": ""}, http.StatusOK, nil
	}
	loaded, err := s.loadWorkflowDocument(ctx, workflowGUID)
	if err != nil {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	allowed := map[int64]bool{}
	for _, siteID := range allowedSiteIDs {
		allowed[siteID] = true
	}
	allSites, _ := s.workflowAllSiteIDs(ctx)
	nodes := workflowMapList(loaded.Document["nodes"])
	hiddenDevices := []map[string]any{}
	hiddenFilters := []map[string]any{}
	seenDevices := map[string]bool{}
	seenFilters := map[string]bool{}
	recordDevice := func(node map[string]any, target map[string]any) {
		siteID := coerceInt64(target["site_id"])
		if siteID > 0 && allowed[siteID] {
			return
		}
		hostname := cleanText(firstNonEmptyAny(target["hostname"], target["device_guid"], target["guid"], "Unknown Device"))
		lookedUp, _ := s.workflowLookupDeviceSite(ctx, target)
		if coerceInt64(lookedUp["site_id"]) > 0 {
			siteID = coerceInt64(lookedUp["site_id"])
			if allowed[siteID] {
				return
			}
			if cleanText(lookedUp["hostname"]) != "" {
				hostname = cleanText(lookedUp["hostname"])
			}
			if cleanText(target["site_name"]) == "" {
				target["site_name"] = cleanText(lookedUp["site_name"])
			}
		}
		key := fmt.Sprintf("%d:%s", siteID, strings.ToLower(hostname))
		if seenDevices[key] {
			return
		}
		seenDevices[key] = true
		hiddenDevices = append(hiddenDevices, map[string]any{
			"node_id":    cleanText(node["id"]),
			"node_label": workflowNodeLabel(node),
			"hostname":   hostname,
			"site_id":    nullablePositiveIntArg(siteID),
			"site_name":  cleanText(target["site_name"]),
		})
	}
	recordFilter := func(node map[string]any, filterID int64, fallbackName string) {
		if filterID <= 0 {
			return
		}
		records, err := s.loadDeviceFilters(ctx, []int64{filterID}, true)
		if err != nil {
			return
		}
		record, ok := records[filterID]
		if !ok {
			return
		}
		effective := workflowFilterEffectiveSites(record, allSites)
		for siteID := range effective {
			if !allowed[siteID] {
				key := strconv.FormatInt(filterID, 10)
				if seenFilters[key] {
					return
				}
				seenFilters[key] = true
				hiddenFilters = append(hiddenFilters, map[string]any{
					"node_id":     cleanText(node["id"]),
					"node_label":  workflowNodeLabel(node),
					"filter_id":   filterID,
					"filter_name": firstText(cleanText(record["name"]), fallbackName, fmt.Sprintf("Filter %d", filterID)),
				})
				return
			}
		}
	}
	for _, node := range nodes {
		switch cleanText(node["type"]) {
		case workflowNodeAgentFilter:
			recordFilter(node, workflowNodeFilterID(node), workflowNodeLabel(node))
		case workflowNodeAgentArray:
			for _, target := range workflowNodeAgentArrayEntries(node) {
				recordDevice(node, target)
			}
		case workflowNodeExecuteAssembly:
			for _, target := range workflowNodeTargetDefinitionTargets(node) {
				kind := strings.ToLower(cleanText(firstNonEmptyAny(target["kind"], target["type"])))
				if kind == "filter" || coerceInt64(firstNonEmptyAny(target["filter_id"], target["id"])) > 0 {
					recordFilter(node, coerceInt64(firstNonEmptyAny(target["filter_id"], target["id"])), cleanText(target["name"]))
				} else {
					recordDevice(node, target)
				}
			}
		}
	}
	sort.SliceStable(hiddenDevices, func(i, j int) bool {
		return strings.ToLower(cleanText(hiddenDevices[i]["hostname"])) < strings.ToLower(cleanText(hiddenDevices[j]["hostname"]))
	})
	sort.SliceStable(hiddenFilters, func(i, j int) bool {
		return strings.ToLower(cleanText(hiddenFilters[i]["filter_name"])) < strings.ToLower(cleanText(hiddenFilters[j]["filter_name"]))
	})
	allowedResult := len(hiddenDevices) == 0 && len(hiddenFilters) == 0
	message := ""
	if !allowedResult {
		message = "This workflow references targets outside your assigned sites and cannot be opened."
	}
	return map[string]any{"allowed": allowedResult, "hidden_devices": hiddenDevices, "hidden_filters": hiddenFilters, "message": message}, http.StatusOK, nil
}

func (s *postgresOperatorStore) resolveWorkflowRun(ctx context.Context, runID int64, requestedStatus string, actor string) (map[string]any, int, error) {
	run, found, err := s.getWorkflowRun(ctx, runID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if !found {
		return map[string]any{"error": "not found"}, http.StatusNotFound, nil
	}
	currentStatus := workflowNormalizeStatus(run["status"])
	if workflowTerminal(currentStatus) {
		return map[string]any{
			"error":   "run_not_active",
			"message": "Workflow run is already terminal and does not need manual recovery.",
			"run":     run,
		}, http.StatusConflict, nil
	}
	resolvedStatus := workflowResolveRequestedStatus(requestedStatus, run)
	if resolvedStatus != workflowStatusFailed && resolvedStatus != workflowStatusTimedOut {
		return map[string]any{"error": "validation_failed", "message": "status must be Failed, Timed Out, or auto."}, http.StatusBadRequest, nil
	}
	now := time.Now().Unix()
	errorText := workflowRecoveryErrorText(currentStatus, resolvedStatus, actor)
	finalPayload := workflowOutput(resolvedStatus, map[string]any{"recovery": map[string]any{"recovered": true, "recovery_actor": actor, "previous_status": currentStatus, "resolved_status": resolvedStatus, "recovered_at": now}}, map[string]any{"recovered": true, "recovery_actor": actor}, map[string]any{})
	finalPayloadJSON, _ := json.Marshal(finalPayload)
	finalMetadataJSON, _ := json.Marshal(finalPayload["metadata"])
	err = s.withWorkflowConn(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, "SELECT id, node_id, status FROM engine.workflow_node_runs WHERE workflow_run_id=$1", runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		type nodeRow struct {
			id     int64
			nodeID string
			status string
		}
		nodeRows := []nodeRow{}
		for rows.Next() {
			var row nodeRow
			if err := rows.Scan(&row.id, &row.nodeID, &row.status); err != nil {
				return err
			}
			nodeRows = append(nodeRows, row)
		}
		for _, row := range nodeRows {
			status := workflowNormalizeStatus(row.status)
			if workflowTerminal(status) {
				continue
			}
			nodeStatus := workflowStatusSkipped
			nodeError := ""
			skipReason := "workflow_run_recovered"
			if status == workflowStatusRunning {
				nodeStatus = resolvedStatus
				nodeError = errorText
				skipReason = ""
			}
			output := workflowOutput(nodeStatus, nil, map[string]any{"reason": "workflow_run_recovered", "recovery_actor": actor, "previous_status": status}, map[string]any{})
			outputJSON, _ := json.Marshal(output)
			if _, err := conn.ExecContext(ctx, `
				UPDATE engine.workflow_node_runs
				   SET status=$1, skip_reason=$2, error=$3, output_envelope_json=$4, finished_ts=$5, updated_at=$6
				 WHERE id=$7
			`, nodeStatus, skipReason, nodeError, string(outputJSON), now, now, row.id); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, `
			UPDATE engine.workflow_child_jobs
			   SET status=$1, stderr_summary=COALESCE(NULLIF(stderr_summary, ''), $2), updated_at=$3
			 WHERE workflow_run_id=$4
			   AND COALESCE(status, '') NOT IN ($5,$6,$7,$8,$9)
		`, resolvedStatus, errorText, now, runID, workflowStatusSuccess, workflowStatusWarning, workflowStatusFailed, workflowStatusTimedOut, workflowStatusSkipped); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `
			UPDATE engine.workflow_runs
			   SET status=$1, error=$2, skip_reason='', final_payload_json=$3, final_metadata_json=$4,
			       started_ts=COALESCE(started_ts, created_at, $5), finished_ts=$6, updated_at=$7
			 WHERE id=$8
		`, resolvedStatus, errorText, string(finalPayloadJSON), string(finalMetadataJSON), now, now, now, runID); err != nil {
			return err
		}
		sourceMetadata := mapStringAny(run["source_metadata"])
		return workflowMirrorScheduledRun(ctx, conn, runID, sourceMetadata, resolvedStatus, errorText)
	})
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	updated, _, err := s.getWorkflowRun(ctx, runID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return map[string]any{"resolved": true, "reason": "manual_admin_resolve", "status": resolvedStatus, "run": updated}, http.StatusOK, nil
}

func (s *postgresOperatorStore) workflowAllSiteIDs(ctx context.Context) (map[int64]bool, error) {
	out := map[int64]bool{}
	err := s.withWorkflowConn(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, "SELECT id FROM engine.sites")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id sql.NullInt64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			if id.Valid {
				out[id.Int64] = true
			}
		}
		return rows.Err()
	})
	return out, err
}

func (s *postgresOperatorStore) workflowLookupDeviceSite(ctx context.Context, target map[string]any) (map[string]any, error) {
	result := map[string]any{}
	hostname := cleanText(target["hostname"])
	guid := cleanText(firstNonEmptyAny(target["device_guid"], target["guid"]))
	err := s.withWorkflowConn(ctx, func(conn *sql.Conn) error {
		query := `
			SELECT d.hostname, ds.site_id, s.name
			  FROM engine.devices d
		 LEFT JOIN engine.device_sites ds ON ds.device_hostname=d.hostname
		 LEFT JOIN engine.sites s ON s.id=ds.site_id
			 WHERE `
		args := []any{}
		if guid != "" {
			query += "LOWER(d.guid)=LOWER($1)"
			args = append(args, guid)
		} else if hostname != "" {
			query += "LOWER(d.hostname)=LOWER($1)"
			args = append(args, hostname)
		} else {
			return nil
		}
		query += " LIMIT 1"
		var host sql.NullString
		var siteID sql.NullInt64
		var siteName sql.NullString
		if err := conn.QueryRowContext(ctx, query, args...).Scan(&host, &siteID, &siteName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		result["hostname"] = nullString(host)
		result["site_id"] = nullableInt(siteID)
		result["site_name"] = nullString(siteName)
		return nil
	})
	return result, err
}

func workflowFilterEffectiveSites(record map[string]any, allSites map[int64]bool) map[int64]bool {
	configured := map[int64]bool{}
	for _, value := range siteIDsFromAny(firstNonEmptyAny(record["site_ids"], record["sites"])) {
		if value > 0 {
			configured[value] = true
		}
	}
	mode := strings.ToLower(cleanText(firstNonEmptyAny(record["site_mode"], "global")))
	switch mode {
	case "specific_sites":
		return configured
	case "global_exclusions":
		out := map[int64]bool{}
		for siteID := range allSites {
			if !configured[siteID] {
				out[siteID] = true
			}
		}
		return out
	default:
		return allSites
	}
}

func workflowResolveRequestedStatus(requested string, run map[string]any) string {
	raw := strings.ToLower(strings.TrimSpace(requested))
	if raw == "" || raw == "auto" {
		for _, node := range workflowMapList(run["node_runs"]) {
			if workflowNormalizeStatus(node["status"]) != workflowStatusRunning {
				continue
			}
			timeout := coerceInt64(node["timeout_seconds"])
			started := coerceInt64(node["started_ts"])
			if timeout > 0 && started > 0 && started+timeout <= time.Now().Unix() {
				return workflowStatusTimedOut
			}
		}
		return workflowStatusFailed
	}
	status := workflowNormalizeStatus(requested)
	if status == workflowStatusTimedOut || status == workflowStatusFailed {
		return status
	}
	return ""
}

func workflowRecoveryErrorText(previous string, resolved string, actor string) string {
	actor = firstText(strings.TrimSpace(actor), "Administrator")
	if resolved == workflowStatusTimedOut {
		return fmt.Sprintf("%s recovered an orphaned workflow run that was still %s and marked it Timed Out.", actor, previous)
	}
	return fmt.Sprintf("%s recovered an orphaned workflow run that was still %s and marked it Failed.", actor, previous)
}

func (s *postgresOperatorStore) loadWorkflowDocument(ctx context.Context, workflowGUID string) (workflowLoadedDocument, error) {
	item, found, err := s.getAssembly(ctx, workflowGUID, true)
	if err != nil {
		return workflowLoadedDocument{}, err
	}
	if !found {
		return workflowLoadedDocument{}, errors.New("Selected workflow assembly was not found.")
	}
	exportDoc := assemblyExportDocument(item)
	workflowPayload, err := workflowDecodeWorkflowPayload(exportDoc["workflow"])
	if err != nil {
		return workflowLoadedDocument{}, err
	}
	return workflowLoadedDocument{Export: exportDoc, Document: workflowPayload}, nil
}

func workflowDecodeWorkflowPayload(value any) (map[string]any, error) {
	if typed, ok := value.(map[string]any); ok {
		return copyMap(typed), nil
	}
	text := strings.TrimSpace(cleanText(value))
	if text == "" {
		return nil, errors.New("Selected workflow assembly does not contain a valid workflow document.")
	}
	if decoded, err := base64.StdEncoding.DecodeString(text); err == nil {
		text = string(decoded)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil || payload == nil {
		return nil, errors.New("Selected workflow assembly does not contain a valid workflow document.")
	}
	return payload, nil
}

func workflowDocumentName(loaded workflowLoadedDocument) string {
	return firstText(cleanText(loaded.Export["name"]), cleanText(loaded.Document["tab_name"]), "Workflow")
}

func workflowActiveRunForWorkflow(ctx context.Context, conn *sql.Conn, workflowGUID string, sourceMetadata map[string]any) (sql.NullInt64, error) {
	scope := mapStringAny(sourceMetadata["workflow_site_scope"])
	siteID := coerceInt64(scope["site_id"])
	if siteID > 0 && coerceInt64(sourceMetadata["scheduled_job_run_id"]) > 0 {
		return sql.NullInt64{}, nil
	}
	var activeID sql.NullInt64
	err := conn.QueryRowContext(ctx, `
		SELECT id
		  FROM engine.workflow_runs
		 WHERE LOWER(workflow_guid)=LOWER($1)
		   AND status IN ($2, $3)
	  ORDER BY id DESC
		 LIMIT 1
	`, workflowGUID, workflowStatusPending, workflowStatusRunning).Scan(&activeID)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullInt64{}, nil
	}
	return activeID, err
}

func workflowInsertRun(ctx context.Context, conn *sql.Conn, workflowGUID string, workflowName string, sourceType string, sourceMetadata map[string]any, graphSnapshot map[string]any, status string, createdBy string, now int64, skipReason string) (int64, error) {
	sourceMetadataJSON, _ := json.Marshal(sourceMetadata)
	graphJSON, _ := json.Marshal(graphSnapshot)
	var finished any
	if workflowTerminal(status) {
		finished = now
	}
	var runID int64
	err := conn.QueryRowContext(ctx, `
		INSERT INTO engine.workflow_runs(
			workflow_guid, workflow_name, source_type, source_metadata_json, graph_snapshot_json,
			status, error, skip_reason, final_payload_json, final_metadata_json,
			parent_workflow_run_id, parent_node_id, scheduled_job_id, scheduled_job_run_id, webhook_id,
			created_by, created_at, started_ts, finished_ts, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,'',$7,'','',$8,$9,$10,$11,$12,$13,$14,NULL,$15,$16)
		RETURNING id
	`,
		workflowGUID,
		workflowName,
		sourceType,
		string(sourceMetadataJSON),
		string(graphJSON),
		status,
		skipReason,
		nullablePositiveIntArg(coerceInt64(sourceMetadata["parent_workflow_run_id"])),
		nullableCleanString(cleanText(sourceMetadata["parent_node_id"])),
		nullablePositiveIntArg(coerceInt64(sourceMetadata["scheduled_job_id"])),
		nullablePositiveIntArg(coerceInt64(sourceMetadata["scheduled_job_run_id"])),
		nullablePositiveIntArg(coerceInt64(sourceMetadata["webhook_id"])),
		nullableCleanString(createdBy),
		now,
		finished,
		now,
	).Scan(&runID)
	return runID, err
}

func nullablePositiveIntArg(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullableCleanString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func executeWorkflowRunBackground(auth *authService, runID int64, profile operatorProfile) {
	if auth == nil {
		return
	}
	store, ok := auth.store.(*postgresOperatorStore)
	if !ok {
		return
	}
	ctx := context.Background()
	if err := store.executeWorkflowRun(ctx, auth, runID, profile); err != nil {
		store.failWorkflowRun(ctx, runID, err.Error())
	}
}

func (s *postgresOperatorStore) executeWorkflowRun(ctx context.Context, auth *authService, runID int64, profile operatorProfile) error {
	run, found, err := s.getWorkflowRun(ctx, runID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if workflowTerminal(cleanText(run["status"])) || cleanText(run["status"]) == workflowStatusRunning {
		return nil
	}
	graph := mapStringAny(run["graph_snapshot"])
	nodes := workflowMapList(graph["nodes"])
	edges := workflowMapList(graph["edges"])
	sourceType := firstText(strings.ToLower(cleanText(run["source_type"])), "manual")
	sourceMetadata := mapStringAny(run["source_metadata"])
	now := time.Now().Unix()
	if err := s.updateWorkflowRun(ctx, runID, map[string]any{"status": workflowStatusRunning, "started_ts": now, "updated_at": now}); err != nil {
		return err
	}
	_ = s.withWorkflowConn(ctx, func(conn *sql.Conn) error {
		return workflowMirrorScheduledRun(ctx, conn, runID, sourceMetadata, workflowStatusRunning, "")
	})
	nodeMap := map[string]map[string]any{}
	for _, node := range nodes {
		id := cleanText(node["id"])
		if id != "" {
			nodeMap[id] = node
		}
	}
	incoming := map[string][]map[string]any{}
	for _, edge := range edges {
		target := cleanText(edge["target"])
		if target != "" {
			incoming[target] = append(incoming[target], edge)
		}
	}
	nodeRunIDs, err := s.createWorkflowNodeRuns(ctx, runID, nodes)
	if err != nil {
		return err
	}
	outputs := map[string]map[string]any{}
	statuses := map[string]string{}
	processed := map[string]bool{}
	activeTrigger := workflowTriggerNodeForSource(sourceType)
	for len(processed) < len(nodeMap) {
		progressed := false
		for nodeID, node := range nodeMap {
			if processed[nodeID] || !workflowIncomingProcessed(incoming[nodeID], processed) {
				continue
			}
			nodeType := cleanText(node["type"])
			nodeRunID := nodeRunIDs[nodeID]
			input, ignored, matched := workflowBuildInputEnvelope(nodeID, incoming[nodeID], outputs, nodeMap)
			if workflowIsTriggerNode(nodeType) {
				status := workflowStatusSuccess
				metadata := map[string]any{"source_type": sourceType}
				if nodeType != activeTrigger {
					status = workflowStatusSkipped
					metadata["reason"] = "inactive_trigger"
				}
				output := workflowOutput(status, map[string]any{"source_metadata": sourceMetadata}, metadata, map[string]any{})
				if err := s.finalizeWorkflowNode(ctx, nodeRunID, status, input, output, ignored, "", nil); err != nil {
					return err
				}
				outputs[nodeID] = output
				statuses[nodeID] = status
				processed[nodeID] = true
				progressed = true
				continue
			}
			if matched == 0 && len(incoming[nodeID]) > 0 {
				output := workflowOutput(workflowStatusSkipped, nil, map[string]any{"reason": "no_matched_inputs"}, map[string]any{})
				if err := s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusSkipped, input, output, ignored, "No incoming workflow route matched this node.", nil); err != nil {
					return err
				}
				outputs[nodeID] = output
				statuses[nodeID] = workflowStatusSkipped
				processed[nodeID] = true
				progressed = true
				continue
			}
			missing := workflowMissingRequiredInputs(node, input)
			if len(missing) > 0 {
				output := workflowOutput(workflowStatusSkipped, nil, map[string]any{"reason": "missing_required_inputs", "missing_inputs": missing}, map[string]any{})
				if err := s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusSkipped, input, output, ignored, "Required input ports were not satisfied.", nil); err != nil {
					return err
				}
				outputs[nodeID] = output
				statuses[nodeID] = workflowStatusSkipped
				processed[nodeID] = true
				progressed = true
				continue
			}
			result, execErr := s.executeWorkflowNode(ctx, auth, runID, nodeRunID, node, input, sourceMetadata, profile)
			if execErr != nil {
				result = workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "node_execution_exception"}, map[string]any{})
				_ = s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, result, ignored, execErr.Error(), nil)
			}
			status := workflowNormalizeStatus(result["status"])
			outputs[nodeID] = result
			statuses[nodeID] = status
			processed[nodeID] = true
			progressed = true
		}
		if !progressed {
			return errors.New("workflow graph could not make progress")
		}
	}
	sinkNodes := workflowSinkOutputs(nodeMap, edges, outputs)
	finalStatus := workflowRollupStatuses(workflowMapValues(statuses))
	finalPayload := workflowOutput(finalStatus, map[string]any{
		"sink_nodes": sinkNodes,
		"exports":    workflowCollectExports(nodeMap, outputs),
		"job_output": workflowCollectTerminalJobOutput(sinkNodes),
	}, map[string]any{"node_statuses": statuses}, map[string]any{})
	finished := time.Now().Unix()
	finalPayloadJSON, _ := json.Marshal(finalPayload)
	finalMetadataJSON, _ := json.Marshal(map[string]any{"node_statuses": statuses})
	err = s.updateWorkflowRun(ctx, runID, map[string]any{
		"status":              finalStatus,
		"final_payload_json":  string(finalPayloadJSON),
		"final_metadata_json": string(finalMetadataJSON),
		"finished_ts":         finished,
		"updated_at":          finished,
	})
	if err == nil {
		_ = s.withWorkflowConn(ctx, func(conn *sql.Conn) error {
			return workflowMirrorScheduledRun(ctx, conn, runID, sourceMetadata, finalStatus, "")
		})
	}
	return err
}

func (s *postgresOperatorStore) executeWorkflowNode(ctx context.Context, auth *authService, runID int64, nodeRunID int64, node map[string]any, input map[string]any, sourceMetadata map[string]any, profile operatorProfile) (map[string]any, error) {
	switch cleanText(node["type"]) {
	case workflowNodeAgentArray:
		return s.executeWorkflowAgentArray(ctx, nodeRunID, node, input, sourceMetadata, profile)
	case workflowNodeAgentFilter:
		return s.executeWorkflowAgentFilter(ctx, nodeRunID, node, input, sourceMetadata, profile)
	case workflowNodeExecuteAssembly:
		return s.executeWorkflowAssembly(ctx, auth, runID, nodeRunID, node, input, profile)
	case workflowNodeExecuteWorkflow:
		return s.executeWorkflowSubworkflow(ctx, auth, runID, nodeRunID, node, input, sourceMetadata, profile)
	default:
		return nil, fmt.Errorf("Unsupported executable node type '%s'.", cleanText(node["type"]))
	}
}

func (s *postgresOperatorStore) executeWorkflowAgentArray(ctx context.Context, nodeRunID int64, node map[string]any, input map[string]any, sourceMetadata map[string]any, profile operatorProfile) (map[string]any, error) {
	entries := workflowNodeAgentArrayEntries(node)
	devices, err := s.listDevices(ctx, profile, deviceListFilter{})
	if err != nil {
		return nil, err
	}
	targets := workflowResolveDeviceEntries(entries, devices)
	targets = workflowScopeTargets(targets, sourceMetadata)
	status := workflowStatusSuccess
	errorText := ""
	if len(targets) == 0 {
		status = workflowStatusWarning
		errorText = "List of Devices node resolved zero devices."
	}
	output := workflowOutput(status, map[string]any{"target_definition": entries, "targets": targets}, map[string]any{"target_node_type": workflowNodeAgentArray, "target_count": len(targets), "selected_count": len(entries)}, map[string]any{})
	return output, s.finalizeWorkflowNode(ctx, nodeRunID, status, input, output, nil, errorText, nil)
}

func (s *postgresOperatorStore) executeWorkflowAgentFilter(ctx context.Context, nodeRunID int64, node map[string]any, input map[string]any, sourceMetadata map[string]any, profile operatorProfile) (map[string]any, error) {
	filterID := workflowNodeFilterID(node)
	if filterID <= 0 {
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "missing_filter_id"}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, "Device Filter node is missing a selected device filter.", nil)
	}
	preview, statusCode, err := s.previewDeviceFilter(ctx, profile, map[string]any{"filter_id": filterID})
	if err != nil {
		return nil, err
	}
	if statusCode >= 400 {
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "filter_not_found", "filter_id": filterID}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, firstText(cleanText(preview["message"]), "Selected device filter was not found."), nil)
	}
	devices := workflowMapList(preview["devices"])
	targets := workflowScopeTargets(workflowDevicePayloadsToTargets(devices), sourceMetadata)
	status := workflowStatusSuccess
	errorText := ""
	if len(targets) == 0 {
		status = workflowStatusWarning
		errorText = "Selected device filter resolved zero devices."
	}
	output := workflowOutput(status, map[string]any{"target_definition": []any{map[string]any{"kind": "filter", "filter_id": filterID}}, "targets": targets}, map[string]any{"target_node_type": workflowNodeAgentFilter, "target_count": len(targets), "filter_id": filterID}, map[string]any{})
	return output, s.finalizeWorkflowNode(ctx, nodeRunID, status, input, output, nil, errorText, nil)
}

func (s *postgresOperatorStore) executeWorkflowAssembly(ctx context.Context, auth *authService, runID int64, nodeRunID int64, node map[string]any, input map[string]any, profile operatorProfile) (map[string]any, error) {
	data := mapStringAny(node["data"])
	assemblyGUID := assemblyCoerceGUID(firstNonEmptyAny(data["assembly_guid"], data["assemblyGuid"]))
	if assemblyGUID == "" {
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "missing_assembly_guid"}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, "Execute Assembly node is missing a selected assembly.", nil)
	}
	item, found, err := s.getAssembly(ctx, assemblyGUID, true)
	if err != nil {
		return nil, err
	}
	if !found {
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "assembly_not_found"}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, "Selected assembly was not found.", nil)
	}
	if strings.EqualFold(cleanText(item["assembly_type"]), "workflow") {
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "workflow_requires_subworkflow_node"}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, "Workflow assemblies must be executed with Execute Subworkflow nodes.", nil)
	}
	if strings.EqualFold(cleanText(item["assembly_type"]), "ansible") || strings.EqualFold(cleanText(item["assembly_subtype"]), "ansible") {
		return s.executeWorkflowAnsibleAssembly(ctx, auth, runID, nodeRunID, node, input, item, profile, assemblyGUID)
	}
	targets := workflowExtractTargetsFromInput(input)
	if len(targets) == 0 {
		targets = workflowNodeTargetDefinitionTargets(node)
	}
	devices, err := s.listDevices(ctx, profile, deviceListFilter{})
	if err != nil {
		return nil, err
	}
	targets = workflowResolveDeviceEntries(targets, devices)
	active, skipped := workflowClassifyTargets(targets, workflowNodeExecutionMode(node))
	if len(active) == 0 {
		status := workflowStatusWarning
		output := workflowOutput(status, map[string]any{"results": []any{}, "job_output": workflowBuildJobOutput(nil, targets, skipped)}, map[string]any{"reason": "no_active_targets", "skipped_targets": skipped}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, status, input, output, nil, "", map[string]any{"count": 0, "status": status})
	}
	qstore, ok := any(s).(quickRunStore)
	if !ok {
		return nil, errors.New("quick-run dispatcher unavailable")
	}
	doc := quickRunLoadAssemblyDocument(quickRunItemPath(item), "powershell", quickRunPayloadMap(item))
	runMode := workflowNodeExecutionMode(node)
	if runMode != "system" && runMode != "currentuser" {
		runMode = "system"
	}
	qtargets := make([]quickRunTarget, 0, len(active))
	dispatchFailures := []map[string]any{}
	for _, target := range active {
		hostname := cleanText(target["hostname"])
		if hostname == "" {
			continue
		}
		snapshot, statusCode, err := s.loadDeviceProcessContext(ctx, profile, hostname)
		if err != nil {
			dispatchFailures = append(dispatchFailures, map[string]any{"hostname": hostname, "status": workflowStatusFailed, "stderr": firstText(err.Error(), fmt.Sprintf("device lookup failed status=%d", statusCode))})
			continue
		}
		qtargets = append(qtargets, quickRunTarget{Hostname: firstText(snapshot.Hostname, hostname), Context: snapshot})
	}
	results, code, payload := dispatchQuickRun(ctx, auth, nil, qstore, qtargets, doc, quickRunItemPath(item), workflowCreatedBy(profile), workflowVariableOverrides(node), map[string]any{
		"run_mode":        runMode,
		"assembly_source": "workflow_run",
		"assembly_guid":   assemblyGUID,
	})
	if code != 0 {
		return nil, errors.New(firstText(cleanText(payload["message"]), cleanText(payload["error"]), "workflow dispatch failed"))
	}
	waited := s.waitForWorkflowActivities(ctx, results, workflowNodeTimeoutSeconds(node, doc))
	waited = append(waited, dispatchFailures...)
	status := workflowRollupResults(waited, skipped)
	jobOutput := workflowBuildJobOutput(waited, targets, skipped)
	output := workflowOutput(status, map[string]any{"results": waited, "job_output": jobOutput}, map[string]any{"skipped_targets": skipped, "execution_mode": runMode, "requested_targets": targets}, map[string]any{"activity_ids": workflowActivityIDs(waited)})
	return output, s.finalizeWorkflowNode(ctx, nodeRunID, status, input, output, nil, "", map[string]any{"count": len(waited), "status": status})
}

func (s *postgresOperatorStore) executeWorkflowAnsibleAssembly(ctx context.Context, auth *authService, runID int64, nodeRunID int64, node map[string]any, input map[string]any, item map[string]any, profile operatorProfile, assemblyGUID string) (map[string]any, error) {
	transport := normalizeScheduledAnsibleTransport(workflowNodeExecutionMode(node))
	if !stringInSet(transport, "local", "ssh", "winrm") {
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "unsupported_ansible_execution_mode"}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, "Ansible workflow nodes require execution mode local, ssh, or winrm.", nil)
	}
	payload := quickRunPayloadMap(item)
	if payload == nil {
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "assembly_payload_unavailable"}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, "Selected Ansible assembly payload could not be loaded.", nil)
	}
	relPath := firstText(scheduledAnsibleRelPath(quickRunItemPath(item)), "Ansible_Playbooks/workflow-node-playbook.yml")
	doc := quickRunLoadAssemblyDocument(relPath, "ansible", payload)
	targets := workflowExtractTargetsFromInput(input)
	if len(targets) == 0 {
		targets = workflowNodeTargetDefinitionTargets(node)
	}
	devices, err := s.listDevices(ctx, profile, deviceListFilter{})
	if err != nil {
		return nil, err
	}
	targets = workflowResolveDeviceEntries(targets, devices)
	active, skipped := workflowClassifyTargets(targets, transport)
	if transport != "local" && len(active) == 0 {
		status := workflowStatusWarning
		output := workflowOutput(status, map[string]any{"results": []any{}, "job_output": workflowBuildJobOutput(nil, targets, skipped)}, map[string]any{"reason": "no_active_targets", "skipped_targets": skipped, "execution_mode": transport}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, status, input, output, nil, "", map[string]any{"count": 0, "status": status})
	}

	data := mapStringAny(node["data"])
	credentialID := coerceInt64Ptr(firstNonEmptyAny(data["credential_id"], data["credentialId"]))
	useServiceAccount := boolFromAny(firstNonEmptyAny(data["use_service_account"], data["useServiceAccount"]))
	targetSpecs, runtimeFiles, err := s.workflowAnsibleTargetSpecs(ctx, auth, transport, active, credentialID, useServiceAccount)
	if err != nil {
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "ansible_target_resolution_failed"}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, err.Error(), nil)
	}

	friendlyName := firstText(cleanText(doc["name"]), cleanText(item["display_name"]), cleanText(item["name"]), "Workflow Playbook")
	metadata := map[string]any{
		"assembly_source":  "workflow_run",
		"workflow_run_id":  runID,
		"workflow_node_id": cleanText(node["id"]),
		"component_kind":   "ansible",
		"component_name":   friendlyName,
		"assembly_guid":    assemblyGUID,
	}
	activityID, err := s.insertQuickRunActivity(ctx, workflowEngineHost, relPath, friendlyName, "ansible", workflowStatusRunning, metadata)
	if err != nil {
		return nil, err
	}
	childJobID, err := s.createWorkflowChildJob(ctx, workflowChildJobInsert{
		WorkflowRunID:     runID,
		WorkflowNodeRunID: nodeRunID,
		ChildKind:         "ansible",
		ChildIdentifier:   strconv.FormatInt(activityID, 10),
		ActivityID:        activityID,
		TargetHostname:    workflowEngineHost,
		ComponentGUID:     assemblyGUID,
		ComponentName:     friendlyName,
		ComponentKind:     "ansible",
		Status:            workflowStatusRunning,
		Payload:           map[string]any{"execution_mode": transport},
	})
	if err != nil {
		_ = s.markQuickRunActivityFailed(ctx, activityID, err.Error())
		return nil, err
	}

	route, err := s.watchdogAnsibleRoute(ctx, int64PtrOrNil(workflowAnsibleRouteSiteID(targetSpecs)))
	if err != nil {
		_ = s.markQuickRunActivityFailed(ctx, activityID, err.Error())
		_ = s.updateWorkflowChildJob(ctx, childJobID, workflowChildJobUpdate{Status: workflowStatusFailed, StderrSummary: err.Error(), Payload: map[string]any{"error": err.Error()}})
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "site_worker_unavailable"}, map[string]any{"activity_id": activityID})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, err.Error(), map[string]any{"count": 1, "status": workflowStatusFailed})
	}
	queueRun := map[string]any{
		"hostname":              workflowEngineHost,
		"playbook_rel_path":     relPath,
		"playbook_name":         friendlyName,
		"playbook_content":      strings.ReplaceAll(cleanText(doc["script"]), "\r\n", "\n"),
		"credential_id":         nullableScheduledCredentialID(credentialID),
		"variable_values":       workflowVariableOverrides(node),
		"payload_files":         quickRunFiles(doc["files"]),
		"target_specifications": targetSpecs,
		"runtime_files":         runtimeFiles,
		"source":                "workflow_run",
		"activity_id":           activityID,
		"workflow_run_id":       runID,
		"workflow_node_run_id":  nodeRunID,
		"connection":            transport,
	}
	response, errPayload := postWatchdogWorkerJSON(ctx, auth, route, "/automation/ansible/run", map[string]any{"queue_run": queueRun}, 10*time.Second)
	if errPayload != nil {
		message := firstText(cleanText(errPayload["message"]), cleanText(errPayload["error"]), "Ansible playbook dispatch failed.")
		_ = s.markQuickRunActivityFailed(ctx, activityID, message)
		_ = s.updateWorkflowChildJob(ctx, childJobID, workflowChildJobUpdate{Status: workflowStatusFailed, StderrSummary: message, Payload: errPayload})
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "ansible_dispatch_failed"}, map[string]any{"activity_id": activityID})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, message, map[string]any{"count": 1, "status": workflowStatusFailed})
	}
	if cleanText(response["run_id"]) == "" {
		message := "Site-worker did not return an Ansible run id."
		_ = s.markQuickRunActivityFailed(ctx, activityID, message)
		_ = s.updateWorkflowChildJob(ctx, childJobID, workflowChildJobUpdate{Status: workflowStatusFailed, StderrSummary: message, Payload: response})
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "ansible_dispatch_failed"}, map[string]any{"activity_id": activityID})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, message, map[string]any{"count": 1, "status": workflowStatusFailed})
	}

	results := s.waitForWorkflowActivities(ctx, []map[string]any{{"child_job_id": childJobID, "hostname": workflowEngineHost, "activity_id": activityID, "status": workflowStatusRunning}}, workflowNodeTimeoutSeconds(node, doc))
	if len(results) == 0 {
		results = []map[string]any{{"hostname": workflowEngineHost, "activity_id": activityID, "status": workflowStatusFailed, "stdout": "", "stderr": "Ansible execution returned no result."}}
	}
	result := results[0]
	_ = s.updateWorkflowChildJob(ctx, childJobID, workflowChildJobUpdate{
		Status:        workflowNormalizeStatus(result["status"]),
		StdoutSummary: cleanText(result["stdout"]),
		StderrSummary: cleanText(firstNonEmptyAny(result["stderr"], result["error"])),
		Payload:       result,
	})
	status := workflowRollupResults(results, skipped)
	output := workflowOutput(status, map[string]any{"results": results, "job_output": workflowBuildJobOutput(results, targets, skipped)}, map[string]any{"skipped_targets": skipped, "execution_mode": transport}, map[string]any{"activity_id": activityID})
	return output, s.finalizeWorkflowNode(ctx, nodeRunID, status, input, output, nil, "", map[string]any{"count": 1, "status": status})
}

func (s *postgresOperatorStore) executeWorkflowSubworkflow(ctx context.Context, auth *authService, runID int64, nodeRunID int64, node map[string]any, input map[string]any, sourceMetadata map[string]any, profile operatorProfile) (map[string]any, error) {
	data := mapStringAny(node["data"])
	workflowGUID := assemblyCoerceGUID(firstNonEmptyAny(data["workflow_guid"], data["workflowGuid"]))
	if workflowGUID == "" {
		output := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "missing_workflow_guid"}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, workflowStatusFailed, input, output, nil, "Execute Subworkflow node is missing a selected workflow.", nil)
	}
	ancestry := workflowStringList(sourceMetadata["workflow_ancestry"])
	parentGUID := assemblyCoerceGUID(sourceMetadata["workflow_guid"])
	if parentGUID != "" {
		ancestry = append(ancestry, parentGUID)
	}
	nextMetadata := copyMap(sourceMetadata)
	nextMetadata["workflow_ancestry"] = ancestry
	nextMetadata["parent_workflow_run_id"] = runID
	nextMetadata["parent_node_id"] = cleanText(node["id"])
	result, statusCode, err := s.startWorkflowRun(ctx, workflowStartRequest{
		WorkflowGUID:   workflowGUID,
		SourceType:     "subworkflow",
		SourceMetadata: nextMetadata,
		CreatedBy:      firstText(cleanText(sourceMetadata["created_by"]), workflowCreatedBy(profile)),
		ExecuteAsync:   true,
		RunnerProfile:  profile,
		Auth:           auth,
	})
	if err != nil {
		status := workflowStatusFailed
		output := workflowOutput(status, nil, map[string]any{"reason": "subworkflow_start_failed", "status_code": statusCode}, map[string]any{})
		return output, s.finalizeWorkflowNode(ctx, nodeRunID, status, input, output, nil, err.Error(), nil)
	}
	childRun := mapStringAny(result.Payload["run"])
	childRunID := coerceInt64(childRun["id"])
	childStatus := workflowNormalizeStatus(childRun["status"])
	childJobID, err := s.createWorkflowChildJob(ctx, workflowChildJobInsert{
		WorkflowRunID:      runID,
		WorkflowNodeRunID:  nodeRunID,
		ChildKind:          "workflow",
		ChildIdentifier:    strconv.FormatInt(childRunID, 10),
		ChildWorkflowRunID: childRunID,
		ComponentGUID:      workflowGUID,
		ComponentName:      firstText(cleanText(childRun["workflow_name"]), workflowGUID),
		ComponentKind:      "workflow",
		Status:             childStatus,
		Payload:            map[string]any{"workflow_guid": workflowGUID},
	})
	if err != nil {
		return nil, err
	}
	timeoutSeconds := workflowNodeTimeoutSeconds(node, nil)
	if result.ShouldExecute && childRunID > 0 {
		childCtx := ctx
		cancel := func() {}
		if timeoutSeconds > 0 {
			childCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
		}
		runErr := s.executeWorkflowRun(childCtx, auth, childRunID, profile)
		cancel()
		if runErr != nil {
			if errors.Is(childCtx.Err(), context.DeadlineExceeded) {
				s.markWorkflowRunTimedOut(context.Background(), childRunID, "Child workflow run timed out.")
			} else {
				s.failWorkflowRun(context.Background(), childRunID, runErr.Error())
			}
		}
	}
	childResult := s.waitForWorkflowRun(ctx, childRunID, timeoutSeconds)
	_ = s.updateWorkflowChildJob(ctx, childJobID, workflowChildJobUpdate{Status: workflowNormalizeStatus(childResult["status"]), Payload: childResult})
	finalPayload := mapStringAny(childResult["final_payload"])
	finalData := mapStringAny(finalPayload["data"])
	exports := mapStringAny(finalData["exports"])
	jobOutput := workflowMapList(finalData["job_output"])
	childStatus = workflowNormalizeStatus(childResult["status"])
	output := workflowOutput(childStatus, map[string]any{
		"workflow_run_id": childRunID,
		"final_payload":   finalPayload,
		"exports":         exports,
		"job_output":      jobOutput,
	}, map[string]any{"workflow_guid": workflowGUID}, map[string]any{"child_workflow_run_id": childRunID})
	return output, s.finalizeWorkflowNode(ctx, nodeRunID, childStatus, input, output, nil, cleanText(childResult["error"]), map[string]any{"child_workflow_run_id": childRunID, "status": childStatus})
}

func (s *postgresOperatorStore) createWorkflowNodeRuns(ctx context.Context, runID int64, nodes []map[string]any) (map[string]int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	now := time.Now().Unix()
	out := map[string]int64{}
	for _, node := range nodes {
		nodeID := cleanText(node["id"])
		if nodeID == "" {
			continue
		}
		nodeJSON, _ := json.Marshal(node)
		var nodeRunID int64
		err := conn.QueryRowContext(ctx, `
			INSERT INTO engine.workflow_node_runs(
				workflow_run_id, node_id, node_type, node_label, node_snapshot_json, status,
				skip_reason, error, timeout_seconds, input_envelope_json, output_envelope_json,
				ignored_inputs_json, linked_child_summary_json, created_at, started_ts, finished_ts, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,'','',$7,'','','[]','',$8,NULL,NULL,$9)
			ON CONFLICT (workflow_run_id, node_id) DO UPDATE SET updated_at=EXCLUDED.updated_at
			RETURNING id
		`, runID, nodeID, cleanText(node["type"]), workflowNodeLabel(node), string(nodeJSON), workflowStatusPending, workflowNodeTimeoutSeconds(node, nil), now, now).Scan(&nodeRunID)
		if err != nil {
			return nil, err
		}
		out[nodeID] = nodeRunID
	}
	return out, nil
}

func (s *postgresOperatorStore) finalizeWorkflowNode(ctx context.Context, nodeRunID int64, status string, input map[string]any, output map[string]any, ignored []map[string]any, errorText string, childSummary map[string]any) error {
	now := time.Now().Unix()
	inputJSON, _ := json.Marshal(input)
	outputJSON, _ := json.Marshal(output)
	ignoredJSON, _ := json.Marshal(ignored)
	childJSON := ""
	if childSummary != nil {
		raw, _ := json.Marshal(childSummary)
		childJSON = string(raw)
	}
	return s.withWorkflowConn(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `
			UPDATE engine.workflow_node_runs
			   SET status=$1,
			       skip_reason=$2,
			       error=$3,
			       input_envelope_json=$4,
			       output_envelope_json=$5,
			       ignored_inputs_json=$6,
			       linked_child_summary_json=$7,
			       started_ts=COALESCE(started_ts, $8),
			       finished_ts=$9,
			       updated_at=$10
			 WHERE id=$11
		`, workflowNormalizeStatus(status), "", errorText, string(inputJSON), string(outputJSON), string(ignoredJSON), childJSON, now, now, now, nodeRunID)
		return err
	})
}

func (s *postgresOperatorStore) updateWorkflowRun(ctx context.Context, runID int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	query, args, err := workflowUpdateSQL("engine.workflow_runs", "id", runID, fields, workflowRunUpdateColumns)
	if err != nil {
		return err
	}
	return s.withWorkflowConn(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, query, args...)
		return err
	})
}

func (s *postgresOperatorStore) failWorkflowRun(ctx context.Context, runID int64, errorText string) {
	now := time.Now().Unix()
	finalPayload := workflowOutput(workflowStatusFailed, nil, map[string]any{"reason": "runtime_exception"}, map[string]any{})
	raw, _ := json.Marshal(finalPayload)
	_ = s.updateWorkflowRun(ctx, runID, map[string]any{
		"status":             workflowStatusFailed,
		"error":              errorText,
		"final_payload_json": string(raw),
		"finished_ts":        now,
		"updated_at":         now,
	})
}

func (s *postgresOperatorStore) withWorkflowConn(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()
	return fn(conn)
}

type workflowChildJobInsert struct {
	WorkflowRunID      int64
	WorkflowNodeRunID  int64
	ChildKind          string
	ChildIdentifier    string
	ActivityID         int64
	ChildWorkflowRunID int64
	TargetHostname     string
	ComponentGUID      string
	ComponentName      string
	ComponentKind      string
	Status             string
	Payload            map[string]any
}

type workflowChildJobUpdate struct {
	Status        string
	StdoutSummary string
	StderrSummary string
	Payload       map[string]any
}

var workflowRunUpdateColumns = map[string]string{
	"status":              "status",
	"started_ts":          "started_ts",
	"updated_at":          "updated_at",
	"error":               "error",
	"final_payload_json":  "final_payload_json",
	"final_metadata_json": "final_metadata_json",
	"finished_ts":         "finished_ts",
}

var workflowChildJobUpdateColumns = map[string]string{
	"status":         "status",
	"updated_at":     "updated_at",
	"stdout_summary": "stdout_summary",
	"stderr_summary": "stderr_summary",
	"payload_json":   "payload_json",
}

func (s *postgresOperatorStore) createWorkflowChildJob(ctx context.Context, req workflowChildJobInsert) (int64, error) {
	now := time.Now().Unix()
	payloadJSON, _ := json.Marshal(req.Payload)
	var activityID any
	if req.ActivityID > 0 {
		activityID = req.ActivityID
	}
	var childRunID any
	if req.ChildWorkflowRunID > 0 {
		childRunID = req.ChildWorkflowRunID
	}
	var childJobID int64
	err := s.withWorkflowConn(ctx, func(conn *sql.Conn) error {
		return conn.QueryRowContext(ctx, `
			INSERT INTO engine.workflow_child_jobs(
				workflow_run_id, workflow_node_run_id, child_kind, child_identifier,
				activity_id, child_workflow_run_id, target_hostname, component_guid,
				component_name, component_kind, status, stdout_summary, stderr_summary,
				payload_json, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'','',$12,$13,$14)
			RETURNING id
		`, req.WorkflowRunID, req.WorkflowNodeRunID, cleanText(req.ChildKind), nullableCleanString(req.ChildIdentifier), activityID, childRunID, nullableCleanString(req.TargetHostname), nullableCleanString(req.ComponentGUID), nullableCleanString(req.ComponentName), nullableCleanString(req.ComponentKind), workflowNormalizeStatus(req.Status), string(payloadJSON), now, now).Scan(&childJobID)
	})
	return childJobID, err
}

func (s *postgresOperatorStore) updateWorkflowChildJob(ctx context.Context, childJobID int64, req workflowChildJobUpdate) error {
	if childJobID <= 0 {
		return nil
	}
	now := time.Now().Unix()
	fields := map[string]any{
		"status":     workflowNormalizeStatus(req.Status),
		"updated_at": now,
	}
	if req.StdoutSummary != "" {
		fields["stdout_summary"] = truncateString(req.StdoutSummary, 2048)
	}
	if req.StderrSummary != "" {
		fields["stderr_summary"] = truncateString(req.StderrSummary, 2048)
	}
	if req.Payload != nil {
		payloadJSON, _ := json.Marshal(req.Payload)
		fields["payload_json"] = string(payloadJSON)
	}
	query, args, err := workflowUpdateSQL("engine.workflow_child_jobs", "id", childJobID, fields, workflowChildJobUpdateColumns)
	if err != nil {
		return err
	}
	return s.withWorkflowConn(ctx, func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, query, args...)
		return err
	})
}

func workflowUpdateSQL(table string, idColumn string, id int64, fields map[string]any, allowed map[string]string) (string, []any, error) {
	if len(fields) == 0 {
		return "", nil, errors.New("workflow_update_fields_required")
	}
	if len(fields) > len(allowed) {
		return "", nil, errors.New("workflow_update_field_count_invalid")
	}
	sets := make([]string, 0, len(fields))
	args := make([]any, 0, len(fields)+1)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			return "", nil, fmt.Errorf("workflow_update_field_not_allowed: %s", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, fields[key])
		sets = append(sets, allowed[key]+"=$"+strconv.Itoa(len(args)))
	}
	args = append(args, id)
	return "UPDATE " + table + " SET " + strings.Join(sets, ", ") + " WHERE " + idColumn + "=$" + strconv.Itoa(len(args)), args, nil
}

func (s *postgresOperatorStore) waitForWorkflowRun(ctx context.Context, runID int64, timeoutSeconds int64) map[string]any {
	if runID <= 0 {
		return map[string]any{"id": runID, "status": workflowStatusFailed, "error": "Child workflow run was not created."}
	}
	deadline := time.Time{}
	if timeoutSeconds > 0 {
		deadline = time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	}
	for {
		run, found, err := s.getWorkflowRun(ctx, runID)
		if err != nil {
			return map[string]any{"id": runID, "status": workflowStatusFailed, "error": err.Error()}
		}
		if !found {
			return map[string]any{"id": runID, "status": workflowStatusFailed, "error": "Child workflow run was not found."}
		}
		if workflowTerminal(cleanText(run["status"])) {
			return run
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			s.markWorkflowRunTimedOut(ctx, runID, "Child workflow run timed out.")
			updated, found, _ := s.getWorkflowRun(ctx, runID)
			if found {
				return updated
			}
			return map[string]any{"id": runID, "status": workflowStatusTimedOut, "error": "Child workflow run timed out."}
		}
		time.Sleep(workflowPollInterval)
	}
}

func (s *postgresOperatorStore) markWorkflowRunTimedOut(ctx context.Context, runID int64, message string) {
	now := time.Now().Unix()
	finalPayload := workflowOutput(workflowStatusTimedOut, nil, map[string]any{"reason": "child_workflow_timeout"}, map[string]any{})
	raw, _ := json.Marshal(finalPayload)
	_ = s.updateWorkflowRun(ctx, runID, map[string]any{
		"status":             workflowStatusTimedOut,
		"error":              message,
		"final_payload_json": string(raw),
		"finished_ts":        now,
		"updated_at":         now,
	})
}

func (s *postgresOperatorStore) workflowAnsibleTargetSpecs(ctx context.Context, auth *authService, transport string, activeTargets []map[string]any, credentialID *int64, useServiceAccount bool) ([]any, []any, error) {
	if transport == "local" {
		siteID := workflowSingleSiteID(activeTargets)
		return []any{map[string]any{
			"hostname":           workflowEngineHost,
			"inventory_hostname": workflowEngineHost,
			"site_group":         "site_local",
			"site_id":            siteID,
			"host_vars":          map[string]any{"ansible_connection": "local"},
		}}, []any{}, nil
	}
	var credential map[string]any
	if credentialID != nil {
		if auth == nil || auth.aegis == nil {
			return nil, nil, errAegisLocked
		}
		loaded, found, err := s.loadDecryptedSchedulerCredential(ctx, auth.aegis, *credentialID)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			return nil, nil, errors.New("Selected credential was not found.")
		}
		credential = loaded
		if connectionType := strings.ToLower(cleanText(credential["connection_type"])); connectionType != "" && connectionType != transport {
			return nil, nil, errors.New("Selected credential does not match the execution context.")
		}
	}
	privateKeyPath := ""
	runtimeFiles := []any{}
	if transport == "ssh" && credential != nil {
		privateKey, err := ansibleSSHPrivateKeyContent(credential, "workflow Ansible runs")
		if err != nil {
			return nil, nil, err
		}
		if privateKey != "" {
			privateKeyPath = "{{BOREALIS_RUNTIME_DIR}}/auth/id_borealis_ssh"
			runtimeFiles = append(runtimeFiles, map[string]any{"relative_path": "auth/id_borealis_ssh", "content": privateKey, "mode": 384})
		}
	}

	agentIDs := []any{}
	requiredPorts := []any{}
	targetPorts := map[string]int{}
	for _, target := range activeTargets {
		agentID := cleanText(target["agent_id"])
		if agentID == "" {
			continue
		}
		port := scheduledEndpointPort(cleanText(target["connection_endpoint"]))
		if port <= 0 {
			if transport == "winrm" {
				port = 5985
			} else {
				port = 22
			}
		}
		agentIDs = append(agentIDs, agentID)
		requiredPorts = append(requiredPorts, port)
		targetPorts[agentID] = port
	}
	vpnPayload, err := workflowInternalJSON(ctx, auth, http.MethodPost, "/api/internal/job-scheduler/vpn-prepare", map[string]any{
		"agent_ids":             agentIDs,
		"required_ports":        requiredPorts,
		"reason":                "workflow_ansible",
		"timeout_seconds":       45,
		"poll_interval_seconds": 0.5,
	}, 60*time.Second)
	if err != nil {
		return nil, nil, err
	}
	sessions := schedulerAnyMap(vpnPayload["sessions"])
	specs := []any{}
	for _, target := range activeTargets {
		hostname := cleanText(target["hostname"])
		agentID := cleanText(target["agent_id"])
		if hostname == "" || agentID == "" {
			continue
		}
		session := schedulerAnyMap(sessions[agentID])
		peerIP := strings.Split(cleanText(session["virtual_ip"]), "/")[0]
		if peerIP == "" || !boolFromAny(session["dispatch_ready"]) {
			return nil, nil, fmt.Errorf("WireGuard connectivity is unavailable for '%s'.", hostname)
		}
		port := targetPorts[agentID]
		hostVars := map[string]any{
			"ansible_host":       peerIP,
			"ansible_connection": transport,
		}
		if port > 0 {
			hostVars["ansible_port"] = port
		}
		if transport == "ssh" {
			hostVars["ansible_ssh_retries"] = envInt("BOREALIS_SHARED_ANSIBLE_SSH_RETRIES", 3, 1, 20)
			hostVars["ansible_ssh_timeout"] = envInt("BOREALIS_SHARED_ANSIBLE_SSH_TIMEOUT_SECONDS", 10, 1, 120)
			hostVars["ansible_ssh_transfer_method"] = firstText(cleanText(os.Getenv("BOREALIS_SHARED_ANSIBLE_SSH_TRANSFER_METHOD")), "sftp")
			workflowApplySSHCredentialHostVars(hostVars, credential, privateKeyPath)
		} else {
			username := ""
			password := ""
			winRMTransport := "ntlm"
			if useServiceAccount {
				account, found, err := s.loadSchedulerServiceAccount(ctx, agentID)
				if err != nil {
					return nil, nil, err
				}
				if found {
					username = cleanText(account["username"])
					password = cleanText(account["password"])
				}
			} else if credential != nil {
				username = cleanText(credential["username"])
				password = cleanText(credential["password"])
				metadata := schedulerAnyMap(credential["metadata"])
				winRMTransport = firstText(cleanText(metadata["winrm_transport"]), "ntlm")
			}
			if username == "" || password == "" {
				return nil, nil, fmt.Errorf("WinRM workflow nodes require a credential with username and password for '%s'.", hostname)
			}
			hostVars["ansible_user"] = username
			hostVars["ansible_password"] = password
			hostVars["ansible_winrm_transport"] = winRMTransport
			hostVars["ansible_winrm_server_cert_validation"] = "ignore"
		}
		siteID := coerceInt64(target["site_id"])
		siteName := cleanText(target["site_name"])
		specs = append(specs, map[string]any{
			"hostname":           hostname,
			"inventory_hostname": firstText(cleanText(target["inventory_hostname"]), scheduledSafeInventoryLabel(hostname, "host")),
			"site_group":         scheduledSiteGroupName(siteName, siteID),
			"site_id":            siteID,
			"host_vars":          hostVars,
		})
	}
	if len(specs) == 0 {
		return nil, nil, errors.New("No eligible Ansible targets were available for this workflow node.")
	}
	return specs, runtimeFiles, nil
}

func workflowInternalJSON(ctx context.Context, auth *authService, method string, path string, body map[string]any, timeout time.Duration) (map[string]any, error) {
	if auth == nil || auth.verifier == nil || len(auth.verifier.secret) == 0 {
		return nil, errors.New("internal API token unavailable")
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = strings.NewReader(string(raw))
	}
	target := strings.TrimRight(envDefault("BOREALIS_INTERNAL_API_BASE_URL", "http://127.0.0.1:5000"), "/") + path
	req, err := http.NewRequestWithContext(requestCtx, method, target, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload map[string]any
	_ = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload)
	if payload == nil {
		payload = map[string]any{}
	}
	if resp.StatusCode >= 400 {
		return payload, fmt.Errorf("internal API %s returned %d: %s", path, resp.StatusCode, firstText(cleanText(payload["message"]), cleanText(payload["error"])))
	}
	return payload, nil
}

func workflowSingleSiteID(targets []map[string]any) int64 {
	siteID := int64(0)
	for _, target := range targets {
		current := coerceInt64(target["site_id"])
		if current <= 0 {
			continue
		}
		if siteID == 0 {
			siteID = current
			continue
		}
		if siteID != current {
			return 0
		}
	}
	return siteID
}

func workflowAnsibleRouteSiteID(specs []any) int64 {
	for _, raw := range specs {
		spec := schedulerAnyMap(raw)
		if siteID := coerceInt64(spec["site_id"]); siteID > 0 {
			return siteID
		}
	}
	return 0
}

func workflowApplySSHCredentialHostVars(hostVars map[string]any, credential map[string]any, privateKeyPath string) {
	if credential == nil {
		return
	}
	if username := cleanText(credential["username"]); username != "" {
		hostVars["ansible_user"] = username
	}
	if password := cleanText(credential["password"]); password != "" {
		hostVars["ansible_password"] = password
		hostVars["ansible_ssh_password_mechanism"] = "sshpass"
	}
	if privateKeyPath != "" {
		hostVars["ansible_ssh_private_key_file"] = privateKeyPath
		existing := cleanText(hostVars["ansible_ssh_extra_args"])
		addition := "-o IdentitiesOnly=yes -o PreferredAuthentications=publickey,password,keyboard-interactive -o PubkeyAuthentication=yes -o PasswordAuthentication=yes -o KbdInteractiveAuthentication=yes"
		if existing == "" {
			hostVars["ansible_ssh_extra_args"] = addition
		} else if !strings.Contains(existing, addition) {
			hostVars["ansible_ssh_extra_args"] = existing + " " + addition
		}
	}
	if become := cleanText(credential["become_method"]); become != "" {
		hostVars["ansible_become"] = true
		hostVars["ansible_become_method"] = become
		if username := cleanText(credential["become_username"]); username != "" {
			hostVars["ansible_become_user"] = username
		}
		if password := cleanText(credential["become_password"]); password != "" {
			hostVars["ansible_become_password"] = password
		}
	}
}

func workflowMapList(value any) []map[string]any {
	raw, ok := value.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if typed, ok := item.(map[string]any); ok {
			out = append(out, copyMap(typed))
		}
	}
	return out
}

func workflowTriggerNodeForSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "manual", "subworkflow":
		return workflowNodeTriggerManual
	case "scheduled_job":
		return workflowNodeTriggerScheduled
	case "webhook":
		return workflowNodeTriggerWebhook
	default:
		return ""
	}
}

func workflowIsTriggerNode(nodeType string) bool {
	switch nodeType {
	case workflowNodeTriggerManual, workflowNodeTriggerScheduled, workflowNodeTriggerWebhook:
		return true
	default:
		return false
	}
}

func workflowSupportedNode(nodeType string) bool {
	switch nodeType {
	case workflowNodeTriggerManual, workflowNodeTriggerScheduled, workflowNodeTriggerWebhook, workflowNodeAgentFilter, workflowNodeAgentArray, workflowNodeExecuteAssembly, workflowNodeExecuteWorkflow:
		return true
	default:
		return false
	}
}

func workflowTerminal(status string) bool {
	switch workflowNormalizeStatus(status) {
	case workflowStatusSuccess, workflowStatusWarning, workflowStatusFailed, workflowStatusTimedOut, workflowStatusSkipped:
		return true
	default:
		return false
	}
}

func workflowNormalizeStatus(value any) string {
	text := cleanText(value)
	key := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(text), "-", "_"), " ", "_")
	switch key {
	case "pending", "queued", "created":
		return workflowStatusPending
	case "running", "started", "in_progress":
		return workflowStatusRunning
	case "success", "succeeded", "completed", "complete":
		return workflowStatusSuccess
	case "warning", "warn":
		return workflowStatusWarning
	case "failed", "failure", "error":
		return workflowStatusFailed
	case "timed_out", "timeout":
		return workflowStatusTimedOut
	case "skipped":
		return workflowStatusSkipped
	default:
		if text == "" {
			return workflowStatusPending
		}
		return text
	}
}

func workflowOutput(status string, data any, metadata map[string]any, artifacts map[string]any) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if artifacts == nil {
		artifacts = map[string]any{}
	}
	return map[string]any{"status": workflowNormalizeStatus(status), "data": data, "metadata": metadata, "artifacts": artifacts}
}

func workflowValidateDocument(workflowGUID string, doc map[string]any, sourceType string, sourceMetadata map[string]any) []string {
	nodes := workflowMapList(doc["nodes"])
	edges := workflowMapList(doc["edges"])
	errorsOut := []string{}
	nodeIDs := map[string]bool{}
	nodeMap := map[string]map[string]any{}
	triggerCounts := map[string]int{}
	for _, node := range nodes {
		nodeID := cleanText(node["id"])
		if nodeID == "" {
			errorsOut = append(errorsOut, "Workflow contains a node with no id.")
			continue
		}
		if nodeIDs[nodeID] {
			errorsOut = append(errorsOut, fmt.Sprintf("Duplicate node id '%s'.", nodeID))
		}
		nodeIDs[nodeID] = true
		nodeMap[nodeID] = node
		nodeType := cleanText(node["type"])
		if !workflowSupportedNode(nodeType) {
			errorsOut = append(errorsOut, fmt.Sprintf("Unsupported executable node '%s' (%s).", workflowNodeLabel(node), nodeType))
		}
		if workflowIsTriggerNode(nodeType) {
			triggerCounts[nodeType]++
		}
		data := mapStringAny(node["data"])
		if nodeType == workflowNodeExecuteAssembly && assemblyCoerceGUID(firstNonEmptyAny(data["assembly_guid"], data["assemblyGuid"])) == "" {
			errorsOut = append(errorsOut, fmt.Sprintf("Execute Assembly node '%s' is missing an assembly selection.", workflowNodeLabel(node)))
		}
		if nodeType == workflowNodeAgentFilter && workflowNodeFilterID(node) <= 0 {
			errorsOut = append(errorsOut, fmt.Sprintf("Device Filter node '%s' is missing a device filter selection.", workflowNodeLabel(node)))
		}
		if nodeType == workflowNodeAgentArray && len(workflowNodeAgentArrayEntries(node)) == 0 {
			errorsOut = append(errorsOut, fmt.Sprintf("List of Devices node '%s' does not contain any selected devices.", workflowNodeLabel(node)))
		}
		if nodeType == workflowNodeExecuteWorkflow {
			childGUID := assemblyCoerceGUID(firstNonEmptyAny(data["workflow_guid"], data["workflowGuid"]))
			if childGUID == "" {
				errorsOut = append(errorsOut, fmt.Sprintf("Execute Subworkflow node '%s' is missing a workflow selection.", workflowNodeLabel(node)))
			}
			ancestry := workflowStringList(sourceMetadata["workflow_ancestry"])
			if strings.EqualFold(childGUID, workflowGUID) || stringListContainsFold(ancestry, childGUID) {
				errorsOut = append(errorsOut, fmt.Sprintf("Subworkflow node '%s' would recurse into an ancestor workflow.", workflowNodeLabel(node)))
			}
		}
	}
	for triggerType, count := range triggerCounts {
		if count > 1 {
			errorsOut = append(errorsOut, fmt.Sprintf("Workflow may contain at most one '%s' trigger node.", triggerType))
		}
	}
	requiredTrigger := workflowTriggerNodeForSource(sourceType)
	if requiredTrigger != "" && triggerCounts[requiredTrigger] != 1 {
		errorsOut = append(errorsOut, fmt.Sprintf("Workflow requires exactly one '%s' trigger node for this launch source.", strings.Title(strings.ReplaceAll(sourceType, "_", " "))))
	}
	indegree := map[string]int{}
	adjacency := map[string][]string{}
	for id := range nodeIDs {
		indegree[id] = 0
		adjacency[id] = []string{}
	}
	for _, edge := range edges {
		source := cleanText(edge["source"])
		target := cleanText(edge["target"])
		if !nodeIDs[source] || !nodeIDs[target] {
			errorsOut = append(errorsOut, "Workflow contains an edge that references a missing node.")
			continue
		}
		if workflowPortKind(nodeMap[source], edge["sourceHandle"], "output") != workflowPortKind(nodeMap[target], edge["targetHandle"], "input") {
			errorsOut = append(errorsOut, fmt.Sprintf("Workflow edge '%s' connects incompatible ports.", firstText(cleanText(edge["id"]), source+"-"+target)))
		}
		indegree[target]++
		adjacency[source] = append(adjacency[source], target)
	}
	queue := []string{}
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range adjacency[current] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if len(nodeIDs) > 0 && visited != len(nodeIDs) {
		errorsOut = append(errorsOut, "Workflow graph contains a cycle. Workflow Runtime v1 only supports acyclic graphs.")
	}
	return errorsOut
}

func workflowPortKind(node map[string]any, handle any, direction string) string {
	nodeType := cleanText(node["type"])
	portID := strings.ToLower(cleanText(handle))
	switch nodeType {
	case workflowNodeTriggerManual, workflowNodeTriggerScheduled, workflowNodeTriggerWebhook:
		if direction == "output" && portID == "action" {
			return workflowPortKindAction
		}
	case workflowNodeAgentFilter, workflowNodeAgentArray:
		if direction == "output" && portID == "targets" {
			return workflowPortKindData
		}
	case workflowNodeExecuteAssembly, workflowNodeExecuteWorkflow:
		if direction == "input" && portID == "trigger" {
			return workflowPortKindAction
		}
		if direction == "input" && portID == "targets" {
			return workflowPortKindData
		}
		if direction == "output" && (portID == "action") {
			return workflowPortKindAction
		}
		if direction == "output" && portID == "job_output" {
			return workflowPortKindData
		}
	}
	return ""
}

func workflowNodeLabel(node map[string]any) string {
	data := mapStringAny(node["data"])
	return firstText(cleanText(data["label"]), cleanText(node["label"]), cleanText(node["type"]), "Node")
}

func workflowIncomingProcessed(edges []map[string]any, processed map[string]bool) bool {
	for _, edge := range edges {
		if !processed[cleanText(edge["source"])] {
			return false
		}
	}
	return true
}

func workflowBuildInputEnvelope(nodeID string, incoming []map[string]any, outputs map[string]map[string]any, nodeMap map[string]map[string]any) (map[string]any, []map[string]any, int) {
	matched := []map[string]any{}
	ignored := []map[string]any{}
	byPort := map[string]any{}
	for _, edge := range incoming {
		sourceID := cleanText(edge["source"])
		sourceOutput := outputs[sourceID]
		sourceStatus := workflowNormalizeStatus(sourceOutput["status"])
		route := workflowEdgeRoute(edge)
		sourceKind := workflowPortKind(nodeMap[sourceID], edge["sourceHandle"], "output")
		targetKind := workflowPortKind(nodeMap[cleanText(edge["target"])], edge["targetHandle"], "input")
		targetPort := strings.ToLower(cleanText(edge["targetHandle"]))
		portKind := firstText(targetKind, sourceKind, workflowPortKindData)
		ok := false
		if portKind == workflowPortKindAction {
			ok = workflowMatchEdgeRoute(route, sourceStatus)
		} else {
			ok = workflowDataEdgeStatus(sourceStatus)
		}
		summary := map[string]any{
			"edge_id":        cleanText(edge["id"]),
			"source_node_id": sourceID,
			"source_port_id": cleanText(edge["sourceHandle"]),
			"target_port_id": targetPort,
			"port_kind":      portKind,
			"route_on":       route,
			"status":         sourceStatus,
			"output":         sourceOutput,
		}
		if ok {
			matched = append(matched, summary)
			entry := mapStringAny(byPort[targetPort])
			if entry == nil {
				entry = map[string]any{"label": targetPort, "kind": portKind, "inputs": []any{}}
			}
			inputs, _ := entry["inputs"].([]any)
			inputs = append(inputs, summary)
			entry["inputs"] = inputs
			byPort[targetPort] = entry
		} else {
			ignored = append(ignored, summary)
		}
	}
	status := workflowStatusPending
	if len(matched) > 0 {
		values := []string{}
		for _, item := range matched {
			values = append(values, cleanText(item["status"]))
		}
		status = workflowRollupStatuses(values)
	}
	return workflowOutput(status, map[string]any{"inputs": matched, "inputs_by_port": byPort}, map[string]any{"matched_input_count": len(matched), "target_node_id": nodeID}, map[string]any{}), ignored, len(matched)
}

func workflowMissingRequiredInputs(node map[string]any, input map[string]any) []map[string]any {
	nodeType := cleanText(node["type"])
	required := []string{}
	switch nodeType {
	case workflowNodeExecuteAssembly:
		required = []string{"trigger", "targets"}
	case workflowNodeExecuteWorkflow:
		required = []string{"trigger"}
	default:
		return nil
	}
	data := mapStringAny(input["data"])
	byPort := mapStringAny(data["inputs_by_port"])
	missing := []map[string]any{}
	for _, port := range required {
		entry := mapStringAny(byPort[port])
		if inputs, ok := entry["inputs"].([]any); ok && len(inputs) > 0 {
			continue
		}
		missing = append(missing, map[string]any{"id": port, "label": strings.Title(port)})
	}
	return missing
}

func workflowEdgeRoute(edge map[string]any) string {
	data := mapStringAny(edge["data"])
	route := strings.ToLower(cleanText(firstNonEmptyAny(data["route_on"], data["routeOn"], workflowEdgeAlways)))
	switch route {
	case workflowEdgeAlways, workflowEdgeSuccess, workflowEdgeWarning, workflowEdgeFailed:
		return route
	default:
		return workflowEdgeAlways
	}
}

func workflowMatchEdgeRoute(route string, status string) bool {
	status = workflowNormalizeStatus(status)
	switch route {
	case workflowEdgeAlways:
		return status == workflowStatusSuccess || status == workflowStatusWarning || status == workflowStatusFailed || status == workflowStatusTimedOut
	case workflowEdgeSuccess:
		return status == workflowStatusSuccess
	case workflowEdgeWarning:
		return status == workflowStatusWarning
	case workflowEdgeFailed:
		return status == workflowStatusFailed || status == workflowStatusTimedOut
	default:
		return false
	}
}

func workflowDataEdgeStatus(status string) bool {
	status = workflowNormalizeStatus(status)
	return status == workflowStatusSuccess || status == workflowStatusWarning || status == workflowStatusFailed || status == workflowStatusTimedOut
}

func workflowNodeFilterID(node map[string]any) int64 {
	data := mapStringAny(node["data"])
	return coerceInt64(firstNonEmptyAny(data["filter_id"], data["agent_filter_id"], data["selected_filter_id"], data["id"]))
}

func workflowNodeAgentArrayEntries(node map[string]any) []map[string]any {
	data := mapStringAny(node["data"])
	raw := firstNonEmptyAny(data["selected_devices"], data["selectedDevices"], data["devices"], data["targets"])
	if text := cleanText(raw); text != "" {
		var parsed []any
		if err := json.Unmarshal([]byte(text), &parsed); err == nil {
			raw = parsed
		}
	}
	items, _ := raw.([]any)
	out := []map[string]any{}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		hostname := cleanText(entry["hostname"])
		guid := cleanText(firstNonEmptyAny(entry["device_guid"], entry["guid"]))
		if hostname == "" && guid == "" {
			continue
		}
		out = append(out, map[string]any{
			"kind":            "device",
			"hostname":        hostname,
			"device_guid":     guid,
			"site_id":         nullablePositiveIntArg(coerceInt64(entry["site_id"])),
			"site_name":       cleanText(firstNonEmptyAny(entry["site_name"], entry["site"])),
			"agent_id":        cleanText(entry["agent_id"]),
			"connection_type": cleanText(entry["connection_type"]),
		})
	}
	return out
}

func workflowNodeTargetDefinitionTargets(node map[string]any) []map[string]any {
	data := mapStringAny(node["data"])
	raw := firstNonEmptyAny(data["target_definition"], data["targetDefinition"], data["targets"], data["targets_json"], data["targetsJson"])
	if text := cleanText(raw); text != "" {
		var parsed []any
		if err := json.Unmarshal([]byte(text), &parsed); err == nil {
			raw = parsed
		} else {
			var object map[string]any
			if err := json.Unmarshal([]byte(text), &object); err == nil {
				raw = []any{object}
			}
		}
	}
	items, ok := raw.([]any)
	if !ok {
		if object, ok := raw.(map[string]any); ok {
			items = []any{object}
		}
	}
	out := []map[string]any{}
	for _, item := range items {
		if entry, ok := item.(map[string]any); ok {
			out = append(out, copyMap(entry))
		}
	}
	return out
}

func workflowExtractTargetsFromInput(input map[string]any) []map[string]any {
	data := mapStringAny(input["data"])
	byPort := mapStringAny(data["inputs_by_port"])
	targetPort := mapStringAny(byPort["targets"])
	inputs, _ := targetPort["inputs"].([]any)
	targets := []map[string]any{}
	for _, raw := range inputs {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		output := mapStringAny(item["output"])
		outputData := mapStringAny(output["data"])
		targets = append(targets, workflowMapList(outputData["targets"])...)
	}
	return workflowDedupeTargets(targets)
}

func workflowResolveDeviceEntries(entries []map[string]any, devices []map[string]any) []map[string]any {
	byHost := map[string]map[string]any{}
	byGUID := map[string]map[string]any{}
	for _, device := range devices {
		host := strings.ToLower(cleanText(device["hostname"]))
		guid := strings.ToLower(cleanText(firstNonEmptyAny(device["guid"], device["device_guid"])))
		target := workflowDevicePayloadToTarget(device)
		if host != "" {
			byHost[host] = target
		}
		if guid != "" {
			byGUID[guid] = target
		}
	}
	out := []map[string]any{}
	for _, entry := range entries {
		if strings.EqualFold(cleanText(firstNonEmptyAny(entry["kind"], entry["type"])), "filter") {
			continue
		}
		host := strings.ToLower(cleanText(entry["hostname"]))
		guid := strings.ToLower(cleanText(firstNonEmptyAny(entry["device_guid"], entry["guid"])))
		if guid != "" {
			if device, ok := byGUID[guid]; ok {
				out = append(out, device)
				continue
			}
		}
		if host != "" {
			if device, ok := byHost[host]; ok {
				out = append(out, device)
				continue
			}
		}
		out = append(out, copyMap(entry))
	}
	return workflowDedupeTargets(out)
}

func workflowDevicePayloadsToTargets(devices []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(devices))
	for _, device := range devices {
		out = append(out, workflowDevicePayloadToTarget(device))
	}
	return workflowDedupeTargets(out)
}

func workflowDevicePayloadToTarget(device map[string]any) map[string]any {
	return map[string]any{
		"kind":                "device",
		"hostname":            cleanText(device["hostname"]),
		"device_guid":         cleanText(firstNonEmptyAny(device["guid"], device["device_guid"])),
		"site_id":             nullablePositiveIntArg(coerceInt64(device["site_id"])),
		"site_name":           cleanText(device["site_name"]),
		"agent_id":            cleanText(device["agent_id"]),
		"connection_type":     cleanText(device["connection_type"]),
		"connection_endpoint": cleanText(device["connection_endpoint"]),
	}
}

func workflowDedupeTargets(records []map[string]any) []map[string]any {
	seen := map[string]bool{}
	out := []map[string]any{}
	for _, record := range records {
		host := strings.ToLower(cleanText(record["hostname"]))
		guid := strings.ToLower(cleanText(firstNonEmptyAny(record["device_guid"], record["guid"])))
		siteID := coerceInt64(record["site_id"])
		key := ""
		if guid != "" {
			key = "guid:" + guid
		} else if host != "" && siteID > 0 {
			key = fmt.Sprintf("site:%d:%s", siteID, host)
		} else if host != "" {
			key = "host:" + host
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, copyMap(record))
	}
	return out
}

func workflowScopeTargets(records []map[string]any, sourceMetadata map[string]any) []map[string]any {
	scope := mapStringAny(sourceMetadata["workflow_site_scope"])
	siteID := coerceInt64(scope["site_id"])
	if siteID <= 0 {
		return workflowDedupeTargets(records)
	}
	out := []map[string]any{}
	for _, record := range records {
		if coerceInt64(record["site_id"]) == siteID {
			out = append(out, record)
		}
	}
	return workflowDedupeTargets(out)
}

func workflowClassifyTargets(targets []map[string]any, executionMode string) ([]map[string]any, []map[string]any) {
	active := []map[string]any{}
	skipped := []map[string]any{}
	localAliases := map[string]bool{workflowEngineHost: true, "localhost": true, "127.0.0.1": true, "::1": true}
	for _, target := range targets {
		host := strings.ToLower(cleanText(target["hostname"]))
		reason := ""
		switch executionMode {
		case "local":
			if !localAliases[host] {
				reason = "local_mode_requires_engine_host"
			}
		case "ssh", "winrm":
			if cleanText(target["agent_id"]) == "" {
				reason = "missing_agent_id"
			}
		}
		if reason != "" {
			next := copyMap(target)
			next["reason"] = reason
			skipped = append(skipped, next)
		} else {
			active = append(active, target)
		}
	}
	return active, skipped
}

func workflowNodeExecutionMode(node map[string]any) string {
	data := mapStringAny(node["data"])
	mode := strings.ToLower(cleanText(firstNonEmptyAny(data["execution_mode"], data["executionMode"], data["run_mode"], data["runMode"], data["execution_context"])))
	if mode == "" {
		return "system"
	}
	return mode
}

func workflowVariableOverrides(node map[string]any) map[string]any {
	data := mapStringAny(node["data"])
	raw := firstNonEmptyAny(data["variable_values"], data["variableValues"], data["variables_json"])
	if typed, ok := raw.(map[string]any); ok {
		return copyMap(typed)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(cleanText(raw)), &parsed); err == nil && parsed != nil {
		return parsed
	}
	return map[string]any{}
}

func workflowNodeTimeoutSeconds(node map[string]any, doc map[string]any) int64 {
	data := mapStringAny(node["data"])
	for _, key := range []string{"timeout_override_seconds", "timeout_seconds", "timeout", "timeoutSeconds"} {
		value := coerceInt64(data[key])
		if value > 0 {
			return value
		}
	}
	if doc != nil {
		value := coerceInt64(firstNonEmptyAny(doc["timeout_seconds"], doc["timeout"]))
		if value > 0 {
			return value
		}
	}
	return 3600
}

func (s *postgresOperatorStore) waitForWorkflowActivities(ctx context.Context, dispatch []map[string]any, timeoutSeconds int64) []map[string]any {
	pending := map[int64]map[string]any{}
	results := []map[string]any{}
	for _, item := range dispatch {
		activityID := coerceInt64(firstNonEmptyAny(item["activity_id"], item["job_id"]))
		if activityID <= 0 || workflowTerminal(cleanText(item["status"])) {
			results = append(results, item)
			continue
		}
		pending[activityID] = item
	}
	deadline := time.Time{}
	if timeoutSeconds > 0 {
		deadline = time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	}
	for len(pending) > 0 {
		for activityID, item := range pending {
			activity, found, _ := s.loadWorkflowActivity(ctx, activityID)
			if !found {
				continue
			}
			status := workflowNormalizeStatus(activity.Status)
			if workflowTerminal(status) && status != workflowStatusSkipped {
				results = append(results, map[string]any{"hostname": firstText(item["hostname"].(string), activity.Hostname), "activity_id": activityID, "status": status, "stdout": activity.Stdout, "stderr": activity.Stderr})
				delete(pending, activityID)
			}
		}
		if len(pending) == 0 {
			break
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			for activityID, item := range pending {
				_ = s.markQuickRunActivityFailed(ctx, activityID, "Workflow node timed out while waiting for activity completion.")
				results = append(results, map[string]any{"hostname": cleanText(item["hostname"]), "activity_id": activityID, "status": workflowStatusTimedOut, "stdout": "", "stderr": "Workflow node timed out while waiting for activity completion."})
				delete(pending, activityID)
			}
			break
		}
		time.Sleep(workflowPollInterval)
	}
	return results
}

func (s *postgresOperatorStore) loadWorkflowActivity(ctx context.Context, activityID int64) (workflowActivityResult, bool, error) {
	var row workflowActivityResult
	err := s.withWorkflowConn(ctx, func(conn *sql.Conn) error {
		return conn.QueryRowContext(ctx, `
			SELECT id, hostname, status, stdout, stderr
			  FROM engine.activity_history
			 WHERE id=$1
		`, activityID).Scan(&row.ID, &row.Hostname, &row.Status, &row.Stdout, &row.Stderr)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return workflowActivityResult{}, false, nil
	}
	return row, err == nil, err
}

func workflowBuildJobOutput(results []map[string]any, requested []map[string]any, skipped []map[string]any) []map[string]any {
	targets := map[string]map[string]any{}
	for _, target := range requested {
		host := strings.ToLower(cleanText(target["hostname"]))
		if host != "" {
			targets[host] = target
		}
	}
	out := []map[string]any{}
	for _, result := range results {
		host := cleanText(result["hostname"])
		target := targets[strings.ToLower(host)]
		out = append(out, map[string]any{
			"hostname":    host,
			"device_guid": cleanText(firstNonEmptyAny(target["device_guid"], target["guid"])),
			"site_id":     nullablePositiveIntArg(coerceInt64(target["site_id"])),
			"site_name":   cleanText(target["site_name"]),
			"agent_id":    cleanText(target["agent_id"]),
			"status":      workflowNormalizeStatus(result["status"]),
			"activity_id": nullablePositiveIntArg(coerceInt64(firstNonEmptyAny(result["activity_id"], result["job_id"]))),
			"stdout":      cleanText(result["stdout"]),
			"stderr":      cleanText(firstNonEmptyAny(result["stderr"], result["error"])),
		})
	}
	for _, target := range skipped {
		out = append(out, map[string]any{
			"hostname":    cleanText(target["hostname"]),
			"device_guid": cleanText(firstNonEmptyAny(target["device_guid"], target["guid"])),
			"site_id":     nullablePositiveIntArg(coerceInt64(target["site_id"])),
			"site_name":   cleanText(target["site_name"]),
			"agent_id":    cleanText(target["agent_id"]),
			"status":      workflowStatusWarning,
			"activity_id": nil,
			"stdout":      "",
			"stderr":      firstText(cleanText(target["reason"]), "Target was skipped before execution."),
		})
	}
	return out
}

func workflowRollupResults(results []map[string]any, skipped []map[string]any) string {
	statuses := []string{}
	for _, result := range results {
		statuses = append(statuses, cleanText(result["status"]))
	}
	if len(statuses) == 0 {
		if len(skipped) > 0 {
			return workflowStatusWarning
		}
		return workflowStatusSkipped
	}
	for _, status := range statuses {
		normalized := workflowNormalizeStatus(status)
		if normalized == workflowStatusFailed || normalized == workflowStatusTimedOut {
			return workflowStatusFailed
		}
	}
	if len(skipped) > 0 {
		return workflowStatusWarning
	}
	return workflowStatusSuccess
}

func workflowRollupStatuses(statuses []string) string {
	normalized := []string{}
	for _, status := range statuses {
		normalized = append(normalized, workflowNormalizeStatus(status))
	}
	for _, status := range normalized {
		if status == workflowStatusFailed || status == workflowStatusTimedOut {
			return workflowStatusFailed
		}
	}
	for _, status := range normalized {
		if status == workflowStatusWarning {
			return workflowStatusWarning
		}
	}
	for _, status := range normalized {
		if status == workflowStatusRunning {
			return workflowStatusRunning
		}
	}
	for _, status := range normalized {
		if status == workflowStatusPending {
			return workflowStatusPending
		}
	}
	for _, status := range normalized {
		if status == workflowStatusSuccess {
			return workflowStatusSuccess
		}
	}
	return workflowStatusSkipped
}

func workflowMapValues(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func workflowActivityIDs(results []map[string]any) []int64 {
	out := []int64{}
	for _, result := range results {
		id := coerceInt64(firstNonEmptyAny(result["activity_id"], result["job_id"]))
		if id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func workflowSinkOutputs(nodeMap map[string]map[string]any, edges []map[string]any, outputs map[string]map[string]any) []map[string]any {
	hasOutgoing := map[string]bool{}
	for _, edge := range edges {
		hasOutgoing[cleanText(edge["source"])] = true
	}
	out := []map[string]any{}
	for nodeID := range nodeMap {
		if hasOutgoing[nodeID] {
			continue
		}
		if output, ok := outputs[nodeID]; ok {
			out = append(out, map[string]any{"node_id": nodeID, "output": output})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return cleanText(out[i]["node_id"]) < cleanText(out[j]["node_id"]) })
	return out
}

func workflowCollectExports(nodeMap map[string]map[string]any, outputs map[string]map[string]any) map[string]any {
	out := map[string]any{}
	for nodeID, node := range nodeMap {
		data := mapStringAny(node["data"])
		key := cleanText(firstNonEmptyAny(data["export_key"], data["exportKey"]))
		if key != "" {
			out[key] = outputs[nodeID]
		}
	}
	return out
}

func workflowCollectTerminalJobOutput(sinks []map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, sink := range sinks {
		output := mapStringAny(sink["output"])
		data := mapStringAny(output["data"])
		out = append(out, workflowMapList(data["job_output"])...)
	}
	return workflowDedupeTargets(out)
}

func workflowStringList(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, item := range raw {
		if text := cleanText(item); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func stringListContainsFold(values []string, needle string) bool {
	for _, value := range values {
		if strings.EqualFold(value, needle) {
			return true
		}
	}
	return false
}

func workflowMirrorScheduledRun(ctx context.Context, conn *sql.Conn, workflowRunID int64, sourceMetadata map[string]any, status string, errorText string) error {
	scheduledRunID := coerceInt64(sourceMetadata["scheduled_job_run_id"])
	if scheduledRunID <= 0 {
		return nil
	}
	now := time.Now().Unix()
	var finished any
	if workflowTerminal(status) {
		finished = now
	}
	_, err := conn.ExecContext(ctx, `
		UPDATE engine.scheduled_job_runs
		   SET status=$1,
		       workflow_run_id=$2,
		       error=$3,
		       finished_ts=COALESCE($4, finished_ts),
		       updated_at=$5
		 WHERE id=$6
	`, workflowNormalizeStatus(status), workflowRunID, errorText, finished, now, scheduledRunID)
	return err
}
