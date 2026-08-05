package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type workflowStore interface {
	listWorkflowRuns(ctx context.Context, workflowGUID string, limit int) ([]map[string]any, error)
	getWorkflowRun(ctx context.Context, runID int64) (map[string]any, bool, error)
	getWorkflowNodeRun(ctx context.Context, runID int64, nodeID string) (map[string]any, bool, error)
	listWorkflowWebhooks(ctx context.Context, workflowGUID string) ([]map[string]any, error)
	createWorkflowWebhook(ctx context.Context, workflowGUID string, profile operatorProfile) (map[string]any, error)
	deleteWorkflowWebhook(ctx context.Context, workflowGUID string, webhookID int64) (bool, error)
	startWorkflowRun(ctx context.Context, req workflowStartRequest) (workflowStartResult, int, error)
	workflowEditorAccess(ctx context.Context, profile operatorProfile, workflowGUID string) (map[string]any, int, error)
	resolveWorkflowRun(ctx context.Context, runID int64, requestedStatus string, actor string) (map[string]any, int, error)
	triggerWorkflowWebhook(ctx context.Context, opaqueToken string) (workflowStartResult, int, error)
}

type workflowRunRow struct {
	ID                  sql.NullInt64
	WorkflowGUID        sql.NullString
	WorkflowName        sql.NullString
	SourceType          sql.NullString
	SourceMetadataJSON  sql.NullString
	Status              sql.NullString
	Error               sql.NullString
	SkipReason          sql.NullString
	FinalPayloadJSON    sql.NullString
	FinalMetadataJSON   sql.NullString
	ParentWorkflowRunID sql.NullInt64
	ParentNodeID        sql.NullString
	ScheduledJobID      sql.NullInt64
	ScheduledJobRunID   sql.NullInt64
	WebhookID           sql.NullInt64
	CreatedBy           sql.NullString
	CreatedAt           sql.NullInt64
	StartedTS           sql.NullInt64
	FinishedTS          sql.NullInt64
	UpdatedAt           sql.NullInt64
	GraphSnapshotJSON   sql.NullString
}

type workflowNodeRunRow struct {
	ID                     sql.NullInt64
	WorkflowRunID          sql.NullInt64
	NodeID                 sql.NullString
	NodeType               sql.NullString
	NodeLabel              sql.NullString
	NodeSnapshotJSON       sql.NullString
	Status                 sql.NullString
	SkipReason             sql.NullString
	Error                  sql.NullString
	TimeoutSeconds         sql.NullInt64
	InputEnvelopeJSON      sql.NullString
	OutputEnvelopeJSON     sql.NullString
	IgnoredInputsJSON      sql.NullString
	LinkedChildSummaryJSON sql.NullString
	CreatedAt              sql.NullInt64
	StartedTS              sql.NullInt64
	FinishedTS             sql.NullInt64
	UpdatedAt              sql.NullInt64
}

type workflowChildJobRow struct {
	ID                 sql.NullInt64
	WorkflowRunID      sql.NullInt64
	WorkflowNodeRunID  sql.NullInt64
	ChildKind          sql.NullString
	ChildIdentifier    sql.NullString
	ActivityID         sql.NullInt64
	ChildWorkflowRunID sql.NullInt64
	TargetHostname     sql.NullString
	ComponentGUID      sql.NullString
	ComponentName      sql.NullString
	ComponentKind      sql.NullString
	Status             sql.NullString
	StdoutSummary      sql.NullString
	StderrSummary      sql.NullString
	PayloadJSON        sql.NullString
	CreatedAt          sql.NullInt64
	UpdatedAt          sql.NullInt64
}

type workflowWebhookRow struct {
	ID              sql.NullInt64
	WorkflowGUID    sql.NullString
	OpaqueToken     sql.NullString
	CreatedAt       sql.NullInt64
	CreatorUsername sql.NullString
	CreatorRole     sql.NullString
	LastUsedAt      sql.NullInt64
}

func registerWorkflowRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/workflows/", workflowRootHandler(auth, fallback))
}

func workflowRootHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := workflowPathParts(r.URL.Path)
		if len(parts) == 1 && parts[0] == "run" && r.Method == http.MethodPost {
			workflowRunStart(w, r, auth)
			return
		}
		if len(parts) == 2 && parts[0] == "runs" && r.Method == http.MethodGet {
			workflowRunByID(w, r, auth, parts[1])
			return
		}
		if len(parts) == 3 && parts[0] == "runs" && parts[2] == "resolve" && r.Method == http.MethodPost {
			workflowRunResolve(w, r, auth, parts[1])
			return
		}
		if len(parts) == 4 && parts[0] == "runs" && parts[2] == "nodes" && r.Method == http.MethodGet {
			workflowNodeRunByID(w, r, auth, parts[1], parts[3])
			return
		}
		if len(parts) == 2 && parts[0] == "webhooks" && r.Method == http.MethodPost {
			workflowWebhookTrigger(w, r, auth, parts[1])
			return
		}
		if len(parts) == 2 && parts[1] == "runs" && r.Method == http.MethodGet {
			workflowRunsByGUID(w, r, auth, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "editor-access" && r.Method == http.MethodGet {
			workflowEditorAccessByGUID(w, r, auth, parts[0])
			return
		}
		if len(parts) == 2 && parts[1] == "webhooks" {
			switch r.Method {
			case http.MethodGet:
				workflowWebhooksByGUID(w, r, auth, parts[0])
			case http.MethodPost:
				workflowWebhookCreate(w, r, auth, parts[0])
			default:
				writeMethodNotAllowed(w, strings.Join([]string{http.MethodGet, http.MethodPost}, ", "))
			}
			return
		}
		if len(parts) == 3 && parts[1] == "webhooks" && r.Method == http.MethodDelete {
			workflowWebhookDelete(w, r, auth, parts[0], parts[2])
			return
		}
		fallback.ServeHTTP(w, r)
	}
}

func workflowRunStart(w http.ResponseWriter, r *http.Request, auth *authService) {
	_, store, profile, ok := workflowRequestContext(w, r, auth)
	if !ok {
		return
	}
	body, err := readJSONMapWithLimit(r, 2<<20)
	if err != nil {
		invalidJSONOrValidation(w, err)
		return
	}
	workflowGUID := assemblyCoerceGUID(firstNonEmptyAny(body["workflow_guid"], body["workflowGuid"]))
	if workflowGUID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "workflow_guid is required"})
		return
	}
	sourceMetadata := mapStringAny(body["source_metadata"])
	sourceMetadata["workflow_guid"] = workflowGUID
	sourceMetadata["created_by"] = workflowCreatedBy(profile)
	ctx, cancel := workflowTimeoutContext(r.Context(), auth)
	defer cancel()
	result, status, err := store.startWorkflowRun(ctx, workflowStartRequest{
		WorkflowGUID:     workflowGUID,
		SourceType:       "manual",
		SourceMetadata:   sourceMetadata,
		CreatedBy:        workflowCreatedBy(profile),
		ExecuteAsync:     true,
		RunnerProfile:    profile,
		Auth:             auth,
		OperatorRealtime: nil,
	})
	if err != nil {
		writeJSON(w, status, workflowErrorPayload(err, status))
		return
	}
	if result.ShouldExecute {
		go executeWorkflowRunBackground(auth, result.RunID, profile)
	}
	writeJSON(w, status, result.Payload)
}

func workflowEditorAccessByGUID(w http.ResponseWriter, r *http.Request, auth *authService, workflowGUID string) {
	_, store, profile, ok := workflowRequestContext(w, r, auth)
	if !ok {
		return
	}
	workflowGUID = assemblyCoerceGUID(workflowGUID)
	if workflowGUID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "workflow_guid is required"})
		return
	}
	ctx, cancel := workflowTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.workflowEditorAccess(ctx, profile, workflowGUID)
	if err != nil {
		writeJSON(w, status, workflowErrorPayload(err, status))
		return
	}
	writeJSON(w, status, payload)
}

func workflowRunResolve(w http.ResponseWriter, r *http.Request, auth *authService, runIDText string) {
	identity, failure := requireAdmin(r.Context(), auth, r)
	if failure != nil {
		failure.write(w)
		return
	}
	store, ok := auth.store.(workflowStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "workflows_unavailable"})
		return
	}
	runID, err := parsePositivePathInt(runIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	body := map[string]any{}
	if r.Body != nil {
		var err error
		body, err = readJSONMap(r)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
	}
	ctx, cancel := workflowTimeoutContext(r.Context(), auth)
	defer cancel()
	payload, status, err := store.resolveWorkflowRun(ctx, runID, cleanText(body["status"]), workflowCreatedBy(operatorProfile{Username: identity.Username, Role: identity.Role}))
	if err != nil {
		writeJSON(w, status, workflowErrorPayload(err, status))
		return
	}
	writeJSON(w, status, payload)
}

func workflowWebhookTrigger(w http.ResponseWriter, r *http.Request, auth *authService, opaqueToken string) {
	store, ok := auth.store.(workflowStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "workflows_unavailable"})
		return
	}
	opaqueToken = strings.TrimSpace(opaqueToken)
	if opaqueToken == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	ctx, cancel := workflowTimeoutContext(r.Context(), auth)
	defer cancel()
	result, status, err := store.triggerWorkflowWebhook(ctx, opaqueToken)
	if err != nil {
		writeJSON(w, status, workflowErrorPayload(err, status))
		return
	}
	if result.ShouldExecute {
		go executeWorkflowRunBackground(auth, result.RunID, operatorProfile{Username: "Webhook", Role: "Admin"})
	}
	writeJSON(w, status, result.Payload)
}

func internalWorkflowStartHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		if !validInternalSchedulerRequest(auth, r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		store, ok := auth.store.(workflowStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "workflow_store_unavailable"})
			return
		}
		body, err := readJSONMapWithLimit(r, 2<<20)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		sourceMetadata := mapStringAny(body["source_metadata"])
		ctx, cancel := workflowTimeoutContext(r.Context(), auth)
		defer cancel()
		result, status, err := store.startWorkflowRun(ctx, workflowStartRequest{
			WorkflowGUID:   assemblyCoerceGUID(body["workflow_guid"]),
			SourceType:     firstText(strings.ToLower(cleanText(body["source_type"])), "scheduled_job"),
			SourceMetadata: sourceMetadata,
			CreatedBy:      firstText(cleanText(body["created_by"]), "scheduler"),
			ExecuteAsync:   true,
			RunnerProfile:  operatorProfile{Username: "job-scheduler", Role: "Admin"},
			Auth:           auth,
		})
		if err != nil {
			writeJSON(w, status, workflowErrorPayload(err, status))
			return
		}
		if result.ShouldExecute {
			go executeWorkflowRunBackground(auth, result.RunID, operatorProfile{Username: "job-scheduler", Role: "Admin"})
		}
		writeJSON(w, status, result.Payload)
	}
}

func workflowPathParts(pathText string) []string {
	trimmed := strings.Trim(strings.TrimPrefix(pathText, "/api/workflows/"), "/")
	if trimmed == "" {
		return nil
	}
	rawParts := strings.Split(trimmed, "/")
	parts := make([]string, 0, len(rawParts))
	for _, raw := range rawParts {
		if raw == "" {
			continue
		}
		decoded, err := url.PathUnescape(raw)
		if err != nil {
			decoded = raw
		}
		parts = append(parts, decoded)
	}
	return parts
}

func workflowRunsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowRunsByGUID(w, r, auth, r.PathValue("workflow_guid"))
	}
}

func workflowRunsByGUID(w http.ResponseWriter, r *http.Request, auth *authService, workflowGUID string) {
	_, store, _, ok := workflowRequestContext(w, r, auth)
	if !ok {
		return
	}
	workflowGUID = strings.TrimSpace(workflowGUID)
	if workflowGUID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "workflow_guid is required"})
		return
	}
	ctx, cancel := workflowTimeoutContext(r.Context(), auth)
	defer cancel()
	runs, err := store.listWorkflowRuns(ctx, workflowGUID, parseWorkflowLimit(r.URL.Query().Get("limit")))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func workflowRunHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowRunByID(w, r, auth, r.PathValue("run_id"))
	}
}

func workflowRunByID(w http.ResponseWriter, r *http.Request, auth *authService, runIDText string) {
	_, store, _, ok := workflowRequestContext(w, r, auth)
	if !ok {
		return
	}
	runID, err := parsePositivePathInt(runIDText)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	ctx, cancel := workflowTimeoutContext(r.Context(), auth)
	defer cancel()
	run, found, err := store.getWorkflowRun(ctx, runID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeWorkflowFound(w, run, found)
}

func workflowNodeRunHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowNodeRunByID(w, r, auth, r.PathValue("run_id"), r.PathValue("node_id"))
	}
}

func workflowNodeRunByID(w http.ResponseWriter, r *http.Request, auth *authService, runIDText string, nodeID string) {
	_, store, _, ok := workflowRequestContext(w, r, auth)
	if !ok {
		return
	}
	runID, err := parsePositivePathInt(runIDText)
	nodeID = strings.TrimSpace(nodeID)
	if err != nil || nodeID == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	ctx, cancel := workflowTimeoutContext(r.Context(), auth)
	defer cancel()
	nodeRun, found, err := store.getWorkflowNodeRun(ctx, runID, nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeWorkflowFound(w, nodeRun, found)
}

func workflowWebhooksListHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowWebhooksByGUID(w, r, auth, r.PathValue("workflow_guid"))
	}
}

func workflowWebhooksByGUID(w http.ResponseWriter, r *http.Request, auth *authService, workflowGUID string) {
	_, store, _, ok := workflowRequestContext(w, r, auth)
	if !ok {
		return
	}
	workflowGUID = strings.TrimSpace(workflowGUID)
	if workflowGUID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "workflow_guid is required"})
		return
	}
	ctx, cancel := workflowTimeoutContext(r.Context(), auth)
	defer cancel()
	webhooks, err := store.listWorkflowWebhooks(ctx, workflowGUID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	attachWorkflowWebhookURLs(r, webhooks)
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": webhooks})
}

func workflowWebhookCreateHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowWebhookCreate(w, r, auth, r.PathValue("workflow_guid"))
	}
}

func workflowWebhookCreate(w http.ResponseWriter, r *http.Request, auth *authService, workflowGUID string) {
	_, store, profile, ok := workflowRequestContext(w, r, auth)
	if !ok {
		return
	}
	workflowGUID = strings.TrimSpace(workflowGUID)
	if workflowGUID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "workflow_guid is required"})
		return
	}
	ctx, cancel := workflowTimeoutContext(r.Context(), auth)
	defer cancel()
	webhook, err := store.createWorkflowWebhook(ctx, workflowGUID, profile)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	webhooks := []map[string]any{webhook}
	attachWorkflowWebhookURLs(r, webhooks)
	writeJSON(w, http.StatusOK, map[string]any{"webhook": webhooks[0]})
}

func workflowWebhookDeleteHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowWebhookDelete(w, r, auth, r.PathValue("workflow_guid"), r.PathValue("webhook_id"))
	}
}

func workflowWebhookDelete(w http.ResponseWriter, r *http.Request, auth *authService, workflowGUID string, webhookIDText string) {
	_, store, _, ok := workflowRequestContext(w, r, auth)
	if !ok {
		return
	}
	workflowGUID = strings.TrimSpace(workflowGUID)
	webhookID, err := parsePositivePathInt(webhookIDText)
	if workflowGUID == "" || err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	ctx, cancel := workflowTimeoutContext(r.Context(), auth)
	defer cancel()
	deleted, err := store.deleteWorkflowWebhook(ctx, workflowGUID, webhookID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !deleted {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func workflowRequestContext(w http.ResponseWriter, r *http.Request, auth *authService) (operatorIdentity, workflowStore, operatorProfile, bool) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			unauthorizedAuthFailure().write(w)
			return operatorIdentity{}, nil, operatorProfile{}, false
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return operatorIdentity{}, nil, operatorProfile{}, false
	}
	store, ok := auth.store.(workflowStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "workflows_unavailable"})
		return operatorIdentity{}, nil, operatorProfile{}, false
	}
	return operatorIdentity{Username: profile.Username, Role: profile.Role}, store, profile, true
}

func workflowTimeoutContext(ctx context.Context, auth *authService) (context.Context, context.CancelFunc) {
	timeout := auth.timeout
	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func writeWorkflowFound(w http.ResponseWriter, payload map[string]any, found bool) {
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func parseWorkflowLimit(value string) int {
	limit, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func parsePositivePathInt(value string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New("invalid id")
	}
	return parsed, nil
}

func attachWorkflowWebhookURLs(r *http.Request, webhooks []map[string]any) {
	baseURL := publicBaseURLForRequest(r)
	for _, webhook := range webhooks {
		token := strings.TrimSpace(cleanText(webhook["opaque_token"]))
		webhook["webhook_url"] = joinURL(baseURL, "/api/workflows/webhooks/"+token)
		webhook["created"] = coerceInt64(webhook["created_at"])
		creator := strings.TrimSpace(cleanText(webhook["creator_username"]))
		if creator == "" {
			creator = "Unknown"
		}
		webhook["creator"] = creator
	}
}

func (s *postgresOperatorStore) listWorkflowRuns(ctx context.Context, workflowGUID string, limit int) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT
			id, workflow_guid, workflow_name, source_type, source_metadata_json, status,
			error, skip_reason, final_payload_json, final_metadata_json, parent_workflow_run_id,
			parent_node_id, scheduled_job_id, scheduled_job_run_id, webhook_id, created_by,
			created_at, started_ts, finished_ts, updated_at, graph_snapshot_json
		  FROM engine.workflow_runs
		 WHERE LOWER(workflow_guid)=LOWER($1)
	  ORDER BY COALESCE(started_ts, created_at, 0) DESC, id DESC
		 LIMIT $2
	`, strings.TrimSpace(workflowGUID), maxInt(limit, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := []map[string]any{}
	for rows.Next() {
		row, err := scanWorkflowRunRow(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, workflowRunPayload(row))
	}
	return runs, rows.Err()
}

func (s *postgresOperatorStore) getWorkflowRun(ctx context.Context, runID int64) (map[string]any, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, errors.Join(errOperatorStoreDown, err)
	}

	row, err := scanWorkflowRunRow(conn.QueryRowContext(ctx, `
		SELECT
			id, workflow_guid, workflow_name, source_type, source_metadata_json, status,
			error, skip_reason, final_payload_json, final_metadata_json, parent_workflow_run_id,
			parent_node_id, scheduled_job_id, scheduled_job_run_id, webhook_id, created_by,
			created_at, started_ts, finished_ts, updated_at, graph_snapshot_json
		  FROM engine.workflow_runs
		 WHERE id = $1
	`, runID))
	if errors.Is(err, sql.ErrNoRows) {
		_ = conn.Close()
		return nil, false, nil
	}
	if err != nil {
		_ = conn.Close()
		return nil, false, err
	}
	nodeRuns, err := listWorkflowNodeRunsForRun(ctx, conn, runID)
	_ = conn.Close()
	if err != nil {
		return nil, false, err
	}
	payload := workflowRunPayload(row)
	payload["node_runs"] = nodeRuns
	return payload, true, nil
}

func (s *postgresOperatorStore) getWorkflowNodeRun(ctx context.Context, runID int64, nodeID string) (map[string]any, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, errors.Join(errOperatorStoreDown, err)
	}

	row, err := scanWorkflowNodeRunRow(conn.QueryRowContext(ctx, `
		SELECT
			id, workflow_run_id, node_id, node_type, node_label, node_snapshot_json, status,
			skip_reason, error, timeout_seconds, input_envelope_json, output_envelope_json,
			ignored_inputs_json, linked_child_summary_json, created_at, started_ts, finished_ts, updated_at
		  FROM engine.workflow_node_runs
		 WHERE workflow_run_id = $1 AND node_id = $2
	`, runID, strings.TrimSpace(nodeID)))
	if errors.Is(err, sql.ErrNoRows) {
		_ = conn.Close()
		return nil, false, nil
	}
	if err != nil {
		_ = conn.Close()
		return nil, false, err
	}
	childJobs, err := listWorkflowChildJobsForNodeRun(ctx, conn, int64OrZero(row.ID))
	_ = conn.Close()
	if err != nil {
		return nil, false, err
	}
	payload := workflowNodeRunPayload(row)
	payload["child_jobs"] = childJobs
	return payload, true, nil
}

func (s *postgresOperatorStore) listWorkflowWebhooks(ctx context.Context, workflowGUID string) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT id, workflow_guid, opaque_token, created_at, creator_username, creator_role, last_used_at
		  FROM engine.workflow_webhooks
		 WHERE LOWER(workflow_guid)=LOWER($1)
	  ORDER BY created_at DESC, id DESC
	`, strings.TrimSpace(workflowGUID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	webhooks := []map[string]any{}
	for rows.Next() {
		var row workflowWebhookRow
		if err := rows.Scan(
			&row.ID,
			&row.WorkflowGUID,
			&row.OpaqueToken,
			&row.CreatedAt,
			&row.CreatorUsername,
			&row.CreatorRole,
			&row.LastUsedAt,
		); err != nil {
			return nil, err
		}
		webhooks = append(webhooks, workflowWebhookPayload(row))
	}
	return webhooks, rows.Err()
}

func (s *postgresOperatorStore) createWorkflowWebhook(ctx context.Context, workflowGUID string, profile operatorProfile) (map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	token, err := newWorkflowWebhookToken()
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	var row workflowWebhookRow
	err = conn.QueryRowContext(ctx, `
		INSERT INTO engine.workflow_webhooks(
			workflow_guid, opaque_token, created_at, creator_username, creator_role, last_used_at
		)
		VALUES ($1, $2, $3, $4, $5, NULL)
		RETURNING id, workflow_guid, opaque_token, created_at, creator_username, creator_role, last_used_at
	`, strings.TrimSpace(workflowGUID), token, now, strings.TrimSpace(profile.Username), strings.TrimSpace(profile.Role)).Scan(
		&row.ID,
		&row.WorkflowGUID,
		&row.OpaqueToken,
		&row.CreatedAt,
		&row.CreatorUsername,
		&row.CreatorRole,
		&row.LastUsedAt,
	)
	if err != nil {
		return nil, err
	}
	return workflowWebhookPayload(row), nil
}

func (s *postgresOperatorStore) deleteWorkflowWebhook(ctx context.Context, workflowGUID string, webhookID int64) (bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	result, err := conn.ExecContext(ctx, `
		DELETE FROM engine.workflow_webhooks
		 WHERE id = $1 AND LOWER(workflow_guid)=LOWER($2)
	`, webhookID, strings.TrimSpace(workflowGUID))
	if err != nil {
		return false, err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return deleted > 0, nil
}

type workflowRowScanner interface {
	Scan(dest ...any) error
}

func scanWorkflowRunRow(scanner workflowRowScanner) (workflowRunRow, error) {
	var row workflowRunRow
	err := scanner.Scan(
		&row.ID,
		&row.WorkflowGUID,
		&row.WorkflowName,
		&row.SourceType,
		&row.SourceMetadataJSON,
		&row.Status,
		&row.Error,
		&row.SkipReason,
		&row.FinalPayloadJSON,
		&row.FinalMetadataJSON,
		&row.ParentWorkflowRunID,
		&row.ParentNodeID,
		&row.ScheduledJobID,
		&row.ScheduledJobRunID,
		&row.WebhookID,
		&row.CreatedBy,
		&row.CreatedAt,
		&row.StartedTS,
		&row.FinishedTS,
		&row.UpdatedAt,
		&row.GraphSnapshotJSON,
	)
	return row, err
}

func scanWorkflowNodeRunRow(scanner workflowRowScanner) (workflowNodeRunRow, error) {
	var row workflowNodeRunRow
	err := scanner.Scan(
		&row.ID,
		&row.WorkflowRunID,
		&row.NodeID,
		&row.NodeType,
		&row.NodeLabel,
		&row.NodeSnapshotJSON,
		&row.Status,
		&row.SkipReason,
		&row.Error,
		&row.TimeoutSeconds,
		&row.InputEnvelopeJSON,
		&row.OutputEnvelopeJSON,
		&row.IgnoredInputsJSON,
		&row.LinkedChildSummaryJSON,
		&row.CreatedAt,
		&row.StartedTS,
		&row.FinishedTS,
		&row.UpdatedAt,
	)
	return row, err
}

func listWorkflowNodeRunsForRun(ctx context.Context, conn *sql.Conn, runID int64) ([]map[string]any, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT
			id, workflow_run_id, node_id, node_type, node_label, node_snapshot_json, status,
			skip_reason, error, timeout_seconds, input_envelope_json, output_envelope_json,
			ignored_inputs_json, linked_child_summary_json, created_at, started_ts, finished_ts, updated_at
		  FROM engine.workflow_node_runs
		 WHERE workflow_run_id = $1
	  ORDER BY id ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodeRuns := []map[string]any{}
	for rows.Next() {
		row, err := scanWorkflowNodeRunRow(rows)
		if err != nil {
			return nil, err
		}
		nodeRuns = append(nodeRuns, workflowNodeRunPayload(row))
	}
	return nodeRuns, rows.Err()
}

func listWorkflowChildJobsForNodeRun(ctx context.Context, conn *sql.Conn, nodeRunID int64) ([]map[string]any, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT
			id, workflow_run_id, workflow_node_run_id, child_kind, child_identifier, activity_id,
			child_workflow_run_id, target_hostname, component_guid, component_name, component_kind,
			status, stdout_summary, stderr_summary, payload_json, created_at, updated_at
		  FROM engine.workflow_child_jobs
		 WHERE workflow_node_run_id = $1
	  ORDER BY id ASC
	`, nodeRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	childJobs := []map[string]any{}
	for rows.Next() {
		var row workflowChildJobRow
		if err := rows.Scan(
			&row.ID,
			&row.WorkflowRunID,
			&row.WorkflowNodeRunID,
			&row.ChildKind,
			&row.ChildIdentifier,
			&row.ActivityID,
			&row.ChildWorkflowRunID,
			&row.TargetHostname,
			&row.ComponentGUID,
			&row.ComponentName,
			&row.ComponentKind,
			&row.Status,
			&row.StdoutSummary,
			&row.StderrSummary,
			&row.PayloadJSON,
			&row.CreatedAt,
			&row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		childJobs = append(childJobs, workflowChildJobPayload(row))
	}
	return childJobs, rows.Err()
}

func workflowRunPayload(row workflowRunRow) map[string]any {
	return map[string]any{
		"id":                     int64OrZero(row.ID),
		"workflow_guid":          nullString(row.WorkflowGUID),
		"workflow_name":          nullString(row.WorkflowName),
		"source_type":            nullString(row.SourceType),
		"source_metadata":        parseJSONObject(row.SourceMetadataJSON),
		"status":                 nullString(row.Status),
		"error":                  nullString(row.Error),
		"skip_reason":            nullString(row.SkipReason),
		"final_payload":          parseJSONObject(row.FinalPayloadJSON),
		"final_metadata":         parseJSONObject(row.FinalMetadataJSON),
		"parent_workflow_run_id": nullableInt(row.ParentWorkflowRunID),
		"parent_node_id":         nullString(row.ParentNodeID),
		"scheduled_job_id":       nullableInt(row.ScheduledJobID),
		"scheduled_job_run_id":   nullableInt(row.ScheduledJobRunID),
		"webhook_id":             nullableInt(row.WebhookID),
		"created_by":             nullString(row.CreatedBy),
		"created_at":             int64OrZero(row.CreatedAt),
		"started_ts":             nullableInt(row.StartedTS),
		"finished_ts":            nullableInt(row.FinishedTS),
		"updated_at":             int64OrZero(row.UpdatedAt),
		"graph_snapshot":         parseJSONObject(row.GraphSnapshotJSON),
	}
}

func workflowNodeRunPayload(row workflowNodeRunRow) map[string]any {
	return map[string]any{
		"id":                   int64OrZero(row.ID),
		"workflow_run_id":      int64OrZero(row.WorkflowRunID),
		"node_id":              nullString(row.NodeID),
		"node_type":            nullString(row.NodeType),
		"node_label":           nullString(row.NodeLabel),
		"node_snapshot":        parseJSONObject(row.NodeSnapshotJSON),
		"status":               nullString(row.Status),
		"skip_reason":          nullString(row.SkipReason),
		"error":                nullString(row.Error),
		"timeout_seconds":      int64OrZero(row.TimeoutSeconds),
		"input_envelope":       parseJSONObject(row.InputEnvelopeJSON),
		"output_envelope":      parseJSONObject(row.OutputEnvelopeJSON),
		"ignored_inputs":       parseJSONArray(row.IgnoredInputsJSON),
		"linked_child_summary": parseJSONObject(row.LinkedChildSummaryJSON),
		"created_at":           int64OrZero(row.CreatedAt),
		"started_ts":           nullableInt(row.StartedTS),
		"finished_ts":          nullableInt(row.FinishedTS),
		"updated_at":           int64OrZero(row.UpdatedAt),
	}
}

func workflowChildJobPayload(row workflowChildJobRow) map[string]any {
	return map[string]any{
		"id":                    int64OrZero(row.ID),
		"workflow_run_id":       int64OrZero(row.WorkflowRunID),
		"workflow_node_run_id":  int64OrZero(row.WorkflowNodeRunID),
		"child_kind":            nullString(row.ChildKind),
		"child_identifier":      nullString(row.ChildIdentifier),
		"activity_id":           nullableInt(row.ActivityID),
		"child_workflow_run_id": nullableInt(row.ChildWorkflowRunID),
		"target_hostname":       nullString(row.TargetHostname),
		"component_guid":        nullString(row.ComponentGUID),
		"component_name":        nullString(row.ComponentName),
		"component_kind":        nullString(row.ComponentKind),
		"status":                nullString(row.Status),
		"stdout_summary":        nullString(row.StdoutSummary),
		"stderr_summary":        nullString(row.StderrSummary),
		"payload":               parseJSONObject(row.PayloadJSON),
		"created_at":            int64OrZero(row.CreatedAt),
		"updated_at":            int64OrZero(row.UpdatedAt),
	}
}

func workflowWebhookPayload(row workflowWebhookRow) map[string]any {
	return map[string]any{
		"id":               int64OrZero(row.ID),
		"workflow_guid":    nullString(row.WorkflowGUID),
		"opaque_token":     nullString(row.OpaqueToken),
		"created_at":       int64OrZero(row.CreatedAt),
		"creator_username": nullString(row.CreatorUsername),
		"creator_role":     nullString(row.CreatorRole),
		"last_used_at":     nullableInt(row.LastUsedAt),
	}
}

func int64OrZero(value sql.NullInt64) int64 {
	if value.Valid {
		return value.Int64
	}
	return 0
}

func newWorkflowWebhookToken() (string, error) {
	buffer := make([]byte, 36)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
