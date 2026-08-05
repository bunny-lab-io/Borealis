package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type patchInventoryStore interface {
	listPatchAudit(ctx context.Context, profile operatorProfile) ([]map[string]any, error)
	listDevicePatches(ctx context.Context, profile operatorProfile, hostname string) ([]map[string]any, string, int, error)
}

type patchInstallStore interface {
	listPatchInstallTargets(ctx context.Context, profile operatorProfile, request patchInstallLookupRequest) ([]patchInstallTarget, int, error)
}

type patchInventoryRow struct {
	ID              sql.NullInt64
	DeviceGUID      sql.NullString
	PatchKey        sql.NullString
	KB              sql.NullString
	Title           sql.NullString
	State           sql.NullString
	Source          sql.NullString
	Classification  sql.NullString
	Severity        sql.NullString
	InstalledOn     sql.NullInt64
	PublishedAt     sql.NullInt64
	CapturedAt      sql.NullInt64
	MetadataJSON    sql.NullString
	GUID            sql.NullString
	Hostname        sql.NullString
	AgentID         sql.NullString
	OperatingSystem sql.NullString
	SiteID          sql.NullInt64
	SiteName        sql.NullString
}

type patchInstallLookupRequest struct {
	Hostname    string
	PatchKey    string
	InventoryID int64
	KB          string
	Title       string
	SiteID      int64
}

type patchInstallTarget struct {
	Row   patchInventoryRow
	Route *agentWorkerRoute
}

func registerPatchRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("GET /api/patches/audit", patchAuditHandler(auth))
	mux.HandleFunc("/api/patches/policies", patchPoliciesRootHandler(auth, fallback))
	mux.HandleFunc("/api/patches/policies/", patchPoliciesSubtreeHandler(auth, fallback))
	mux.HandleFunc("GET /api/device/patches/{hostname}", devicePatchesHandler(auth))
	mux.HandleFunc("POST /api/device/patches/{hostname}/refresh", patchRefreshHandler(auth))
	mux.HandleFunc("/api/patches/", patchSubtreeHandler(fallback))
}

func patchAuditHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireUser(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		store, ok := auth.store.(patchInventoryStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "patch_audit_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		profile, err := auth.store.lookupOperator(ctx, identity.Username, identity.Role)
		if err != nil {
			if errors.Is(err, errOperatorNotFound) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "operator_lookup_failed", "message": err.Error()})
			return
		}
		rows, err := store.listPatchAudit(ctx, profile)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "patch_audit_failed", "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"patches": rows, "count": len(rows)})
	}
}

func devicePatchesHandler(auth *authService) http.HandlerFunc {
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
		store, ok := auth.store.(patchInventoryStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "patch_inventory_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		rows, hostname, status, err := store.listDevicePatches(ctx, profile, r.PathValue("hostname"))
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"hostname": hostname,
			"patches":  rows,
			"count":    len(rows),
		})
	}
}

func patchRefreshHandler(auth *authService) http.HandlerFunc {
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
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "patch_refresh_unavailable"})
			return
		}
		hostname := r.PathValue("hostname")
		requestedAt := time.Now().Unix()
		deadline := time.Now().Add(8 * time.Second)
		var lastSnapshot deviceProcessContext
		for {
			ctx, cancel := requestTimeout(r.Context(), auth)
			snapshot, status, err := store.loadDeviceProcessContext(ctx, profile, hostname)
			cancel()
			if err != nil {
				writeJSON(w, status, map[string]any{"error": err.Error()})
				return
			}
			lastSnapshot = snapshot
			if snapshot.Route != nil {
				eventPayload := map[string]any{
					"hostname":     snapshot.Hostname,
					"agent_id":     snapshot.AgentID,
					"requested_at": requestedAt,
					"requested_by": firstText(cleanText(profile.Username), "unknown"),
					"reason":       "operator_query_patch_inventory",
				}
				result, _, workerErr := emitWorkerHostServiceEvent(r.Context(), auth, snapshot.Route, map[string]any{
					"hostname":            snapshot.Hostname,
					"service_mode":        "system",
					"event_name":          "patch_inventory_refresh_request",
					"payload":             eventPayload,
					"allow_pending":       true,
					"pending_ttl_seconds": int64(180),
				}, 6*time.Second)
				if workerErr == nil && (boolFromAny(result["emitted"]) || boolFromAny(result["queued"])) {
					writeJSON(w, http.StatusOK, map[string]any{
						"status":       "queued",
						"hostname":     snapshot.Hostname,
						"agent_id":     snapshot.AgentID,
						"requested_at": requestedAt,
					})
					return
				}
			}
			if time.Now().After(deadline) {
				break
			}
			select {
			case <-r.Context().Done():
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "request_canceled"})
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":        "agent_unavailable",
			"message":      "The agent SYSTEM socket is not available to query patch inventory right now.",
			"hostname":     firstText(lastSnapshot.Hostname, hostname),
			"agent_id":     lastSnapshot.AgentID,
			"requested_at": requestedAt,
		})
	}
}

func devicePatchInstallHandler(auth *authService) http.HandlerFunc {
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
		request := patchInstallLookupFromBody(body)
		request.Hostname = r.PathValue("hostname")
		if !request.hasPatchIdentity() {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "patch_required"})
			return
		}
		store, ok := auth.store.(patchInstallStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "patch_install_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		targets, status, err := store.listPatchInstallTargets(ctx, profile, request)
		cancel()
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		if len(targets) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "patch_not_found"})
			return
		}
		summary := dispatchPatchInstallTargets(r.Context(), auth, profile, targets[:1], "device")
		if patchInstallIntFromAny(summary["accepted_count"]) == 0 {
			writeJSON(w, firstPatchInstallFailureStatus(summary), map[string]any{
				"error":   firstPatchInstallFailureError(summary),
				"status":  "failed",
				"results": summary["results"],
			})
			return
		}
		writeJSON(w, http.StatusOK, summary)
	}
}

func fleetPatchInstallHandler(auth *authService) http.HandlerFunc {
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
		request := patchInstallLookupFromBody(body)
		if !request.hasPatchIdentity() {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "patch_required"})
			return
		}
		store, ok := auth.store.(patchInstallStore)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "patch_install_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		targets, status, err := store.listPatchInstallTargets(ctx, profile, request)
		cancel()
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		if len(targets) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "patch_not_found"})
			return
		}
		writeJSON(w, http.StatusOK, dispatchPatchInstallTargets(r.Context(), auth, profile, targets, "fleet"))
	}
}

func patchSubtreeHandler(fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if fallback == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		fallback.ServeHTTP(w, r)
	}
}

func patchInstallLookupFromBody(body map[string]any) patchInstallLookupRequest {
	patch, _ := body["patch"].(map[string]any)
	request := patchInstallLookupRequest{
		PatchKey: cleanText(firstNonEmpty(firstValueFromMap(patch, "patch_key"), body["patch_key"])),
		KB:       normalizePatchKB(firstNonEmpty(firstValueFromMap(patch, "kb"), body["kb"])),
		Title:    cleanText(firstNonEmpty(firstValueFromMap(patch, "title"), body["title"])),
	}
	request.InventoryID = coercePatchInstallInt64(firstNonEmpty(firstValueFromMap(patch, "inventory_id"), firstValueFromMap(patch, "id"), body["inventory_id"], body["id"]))
	request.SiteID = coercePatchInstallInt64(firstNonEmpty(body["site_id"], firstValueFromMap(patch, "site_id")))
	return request
}

func (r patchInstallLookupRequest) hasPatchIdentity() bool {
	return r.InventoryID > 0 || r.PatchKey != "" || r.KB != "" || r.Title != ""
}

func firstValueFromMap(values map[string]any, key string) any {
	if values == nil {
		return nil
	}
	return values[key]
}

func coercePatchInstallInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		parsed, _ := strconv.ParseInt(cleanText(value), 10, 64)
		return parsed
	}
}

func dispatchPatchInstallTargets(ctx context.Context, auth *authService, profile operatorProfile, targets []patchInstallTarget, scope string) map[string]any {
	requestedAt := time.Now().Unix()
	requestedBy := firstText(cleanText(profile.Username), "unknown")
	results := make([]map[string]any, 0, len(targets))
	accepted := 0
	failed := 0
	for idx, target := range targets {
		rowPayload := patchInventoryPayload(target.Row)
		hostname := cleanText(rowPayload["hostname"])
		result := map[string]any{
			"hostname":  hostname,
			"agent_id":  cleanText(rowPayload["agent_id"]),
			"patch_key": cleanText(rowPayload["patch_key"]),
			"kb":        cleanText(rowPayload["kb"]),
			"title":     cleanText(rowPayload["title"]),
		}
		if target.Route == nil {
			failed++
			result["status"] = "failed"
			result["http_status"] = http.StatusServiceUnavailable
			result["error"] = "site_worker_unavailable"
			result["message"] = "No active site-worker route is available for this device."
			results = append(results, result)
			continue
		}
		requestID := "patch-" + strconv.FormatInt(requestedAt, 10) + "-" + strconv.Itoa(idx+1)
		eventPayload := map[string]any{
			"hostname":     hostname,
			"agent_id":     cleanText(rowPayload["agent_id"]),
			"requested_at": requestedAt,
			"requested_by": requestedBy,
			"request_id":   requestID,
			"scope":        scope,
			"patch":        rowPayload,
		}
		response, workerStatus, workerErr := callWorkerHostServiceEvent(ctx, auth, target.Route, map[string]any{
			"hostname":        hostname,
			"service_mode":    "system",
			"event_name":      "patch_install_request",
			"payload":         eventPayload,
			"timeout_seconds": 12,
		}, 15*time.Second)
		if workerErr != nil {
			failed++
			result["status"] = "failed"
			result["http_status"] = workerStatus
			result["error"] = firstText(cleanText(workerErr["error"]), "patch_install_failed")
			result["message"] = cleanText(workerErr["message"])
			results = append(results, result)
			continue
		}
		accepted++
		result["status"] = firstText(cleanText(response["status"]), "accepted")
		result["request_id"] = firstText(cleanText(response["request_id"]), requestID)
		results = append(results, result)
	}
	status := "accepted"
	if accepted > 0 && failed > 0 {
		status = "partial"
	} else if accepted == 0 {
		status = "failed"
	}
	return map[string]any{
		"status":         status,
		"requested_at":   requestedAt,
		"requested_by":   requestedBy,
		"target_count":   len(targets),
		"accepted_count": accepted,
		"failed_count":   failed,
		"results":        results,
	}
}

func patchInstallIntFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		parsed, _ := strconv.Atoi(cleanText(value))
		return parsed
	}
}

func firstPatchInstallFailureStatus(summary map[string]any) int {
	for _, raw := range anySliceFromAny(summary["results"]) {
		row, _ := raw.(map[string]any)
		status := patchInstallIntFromAny(row["http_status"])
		if status >= 400 {
			return status
		}
	}
	return http.StatusBadGateway
}

func firstPatchInstallFailureError(summary map[string]any) string {
	for _, raw := range anySliceFromAny(summary["results"]) {
		row, _ := raw.(map[string]any)
		if err := cleanText(row["error"]); err != "" {
			return err
		}
	}
	return "patch_install_failed"
}

func anySliceFromAny(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, row := range typed {
			out = append(out, row)
		}
		return out
	default:
		return nil
	}
}

func loadActivePatchInstallJobs(ctx context.Context, conn *sql.Conn, profile operatorProfile) (map[string]map[string]any, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT j.id, j.name, j.schedule_type, j.start_ts, j.components_json,
		       COALESCE(latest.scheduled_ts, 0), COALESCE(latest.status, '')
		  FROM engine.scheduled_jobs AS j
	 LEFT JOIN LATERAL (
			SELECT r.scheduled_ts, r.status
			  FROM engine.scheduled_job_runs AS r
			 WHERE r.job_id=j.id
		  ORDER BY r.scheduled_ts DESC, r.id DESC
			 LIMIT 1
	       ) AS latest ON TRUE
		 WHERE j.enabled=1
		   AND j.job_kind=$1
	  ORDER BY j.id ASC
	`, scheduledJobKindPatchInstall)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	for rows.Next() {
		var id, startTS, latestTS sql.NullInt64
		var name, scheduleType, componentsJSON, latestStatus sql.NullString
		if err := rows.Scan(&id, &name, &scheduleType, &startTS, &componentsJSON, &latestTS, &latestStatus); err != nil {
			return nil, err
		}
		if !id.Valid || id.Int64 <= 0 {
			continue
		}
		schedule := strings.ToLower(firstText(nullString(scheduleType), "immediately"))
		status := nullString(latestStatus)
		if latestTS.Valid && latestTS.Int64 > 0 && scheduledTerminalStatus(status) && stringInSet(schedule, "immediately", "once") {
			continue
		}
		components := parseJSONArray(componentsJSON)
		component := scheduledPatchInstallComponent(components)
		if component == nil {
			continue
		}
		patch := scheduledPatchInstallSpec(component)
		keys := patchActiveIdentityKeys(patch)
		if len(keys) == 0 {
			continue
		}
		labelPrefix := "Scheduled"
		if schedule == "immediately" {
			labelPrefix = "Immediate"
		}
		job := map[string]any{
			"id":            id.Int64,
			"name":          nullString(name),
			"schedule_type": schedule,
			"start_ts":      nullableInt(startTS),
			"status":        status,
			"scheduled_ts":  nullableInt(latestTS),
			"label":         labelPrefix + " - Job ID: " + strconv.FormatInt(id.Int64, 10),
			"path":          "/jobs/" + strconv.FormatInt(id.Int64, 10) + "?tab=job_history",
			"patch":         patch,
		}
		if !patchJobVisibleToProfile(component, profile) {
			continue
		}
		for _, key := range keys {
			if _, exists := out[key]; !exists {
				out[key] = job
			}
		}
	}
	return out, rows.Err()
}

func patchJobVisibleToProfile(component map[string]any, profile operatorProfile) bool {
	// Patch jobs are operator-visible through the regular scheduled-jobs route. Keep this
	// lightweight here; site scoping is enforced when jobs are opened.
	return true
}

func attachActivePatchInstallJob(payload map[string]any, activeJobs map[string]map[string]any) {
	if len(payload) == 0 || len(activeJobs) == 0 {
		return
	}
	patch := map[string]any{
		"patch_key":       payload["patch_key"],
		"kb":              payload["kb"],
		"title":           payload["title"],
		"metadata":        payload["metadata"],
		"update_id":       payload["update_id"],
		"revision_number": payload["revision_number"],
	}
	for _, key := range patchActiveIdentityKeys(patch) {
		if job := activeJobs[key]; len(job) > 0 {
			payload["active_install_job"] = job
			return
		}
	}
}

func patchActiveIdentityKeys(patch map[string]any) []string {
	keys := []string{}
	if patch == nil {
		return keys
	}
	addKey := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		for _, existing := range keys {
			if existing == key {
				return
			}
		}
		keys = append(keys, key)
	}
	if patchKey := cleanText(patch["patch_key"]); patchKey != "" {
		addKey("patch:" + strings.ToLower(patchKey))
	}
	if kb := normalizePatchKB(patch["kb"]); kb != "" {
		addKey("kb:" + strings.ToUpper(kb))
	}
	if titleKB := normalizePatchKB(patch["title"]); titleKB != "" {
		addKey("kb:" + strings.ToUpper(titleKB))
	}
	metadata := schedulerAnyMap(patch["metadata"])
	updateID := cleanText(firstPresentAny(metadata["update_id"], metadata["updateID"], patch["update_id"], patch["updateID"]))
	if updateID != "" {
		updateKey := "update:" + strings.ToLower(updateID)
		if revision := coerceInt64(firstPresentAny(metadata["revision_number"], metadata["revision"], patch["revision_number"], patch["revision"])); revision > 0 {
			addKey(updateKey + ":" + strconv.FormatInt(revision, 10))
		}
		addKey(updateKey)
	}
	if title := strings.ToLower(cleanText(patch["title"])); title != "" {
		addKey("title:" + title)
	}
	return keys
}

func (s *postgresOperatorStore) listPatchAudit(ctx context.Context, profile operatorProfile) ([]map[string]any, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return []map[string]any{}, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errOperatorStoreDown, err)
	}
	activeJobs, err := loadActivePatchInstallJobs(ctx, conn, profile)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	query := `
		SELECT
			dpi.id,
			dpi.device_guid,
			dpi.patch_key,
			dpi.kb,
			dpi.title,
			dpi.state,
			dpi.source,
			dpi.classification,
			dpi.severity,
			dpi.installed_on,
			dpi.published_at,
			dpi.captured_at,
			dpi.metadata_json,
			d.guid,
			d.hostname,
			d.agent_id,
			d.operating_system,
			ds.site_id,
			s.name
		  FROM engine.device_patch_inventory AS dpi
		  JOIN engine.devices AS d
		    ON d.guid = dpi.device_guid
	 LEFT JOIN engine.device_sites AS ds
		    ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites AS s
		    ON s.id = ds.site_id
		 WHERE TRIM(COALESCE(dpi.title, dpi.kb, '')) <> ''
	`
	args := []any{}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for idx, siteID := range allowedSiteIDs {
			placeholders = append(placeholders, "$"+strconv.Itoa(idx+1))
			args = append(args, siteID)
		}
		query += " AND ds.site_id IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY LOWER(COALESCE(dpi.kb, '')), LOWER(dpi.title), LOWER(d.hostname)"

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	result := []map[string]any{}
	for rows.Next() {
		row, err := scanPatchInventoryRow(rows)
		if err != nil {
			_ = rows.Close()
			_ = conn.Close()
			return nil, err
		}
		payload := patchInventoryPayload(row)
		attachActivePatchInstallJob(payload, activeJobs)
		result = append(result, payload)
	}
	closeRowsErr := rows.Close()
	closeConnErr := conn.Close()
	if err := rows.Err(); err != nil {
		if closeRowsErr != nil {
			return nil, errors.Join(err, closeRowsErr)
		}
		if closeConnErr != nil {
			return nil, errors.Join(err, closeConnErr)
		}
		return nil, err
	}
	if closeRowsErr != nil {
		if closeConnErr != nil {
			return nil, errors.Join(closeRowsErr, closeConnErr)
		}
		return nil, closeRowsErr
	}
	if closeConnErr != nil {
		return nil, closeConnErr
	}
	policyIndex, err := s.patchPolicyPendingInventoryIndex(ctx, profile, nil, activeJobs)
	if err != nil {
		return nil, err
	}
	for _, payload := range result {
		attachPatchPolicyInventoryPayload(payload, policyIndex)
	}
	return result, nil
}

func (s *postgresOperatorStore) listDevicePatches(ctx context.Context, profile operatorProfile, hostname string) ([]map[string]any, string, int, error) {
	hostname = cleanText(hostname)
	if hostname == "" {
		return nil, "", http.StatusNotFound, errors.New("not found")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, "", http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	var device struct {
		GUID            sql.NullString
		Hostname        sql.NullString
		AgentID         sql.NullString
		OperatingSystem sql.NullString
		SiteID          sql.NullInt64
		SiteName        sql.NullString
	}
	err = conn.QueryRowContext(ctx, `
		SELECT d.guid, d.hostname, d.agent_id, d.operating_system, ds.site_id, s.name
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites AS s ON s.id = ds.site_id
		 WHERE LOWER(d.hostname) = LOWER($1)
	  ORDER BY COALESCE(d.last_seen, 0) DESC
		 LIMIT 1
	`, hostname).Scan(&device.GUID, &device.Hostname, &device.AgentID, &device.OperatingSystem, &device.SiteID, &device.SiteName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", http.StatusNotFound, errors.New("not found")
	}
	if err != nil {
		return nil, "", http.StatusInternalServerError, err
	}
	if device.SiteID.Valid {
		allowed, err := profileCanAccessSite(ctx, conn, profile, device.SiteID.Int64)
		if err != nil {
			return nil, "", http.StatusInternalServerError, err
		}
		if !allowed {
			return nil, "", http.StatusNotFound, errors.New("not found")
		}
	} else if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		return nil, "", http.StatusNotFound, errors.New("not found")
	}
	activeJobs, err := loadActivePatchInstallJobs(ctx, conn, profile)
	if err != nil {
		return nil, "", http.StatusInternalServerError, err
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT
			dpi.id,
			dpi.device_guid,
			dpi.patch_key,
			dpi.kb,
			dpi.title,
			dpi.state,
			dpi.source,
			dpi.classification,
			dpi.severity,
			dpi.installed_on,
			dpi.published_at,
			dpi.captured_at,
			dpi.metadata_json,
			$2::text AS guid,
			$3::text AS hostname,
			$4::text AS agent_id,
			$5::text AS operating_system,
			$6::bigint AS site_id,
			$7::text AS site_name
		  FROM engine.device_patch_inventory AS dpi
		 WHERE UPPER(dpi.device_guid)=UPPER($1)
	  ORDER BY LOWER(COALESCE(dpi.kb, '')), LOWER(dpi.title), LOWER(dpi.state)
	`,
		normalizeCanonicalGUID(device.GUID.String),
		nullString(device.GUID),
		nullString(device.Hostname),
		nullString(device.AgentID),
		nullString(device.OperatingSystem),
		nullableInt(device.SiteID),
		nullString(device.SiteName),
	)
	if err != nil {
		return nil, "", http.StatusInternalServerError, err
	}
	defer rows.Close()

	result := []map[string]any{}
	for rows.Next() {
		row, err := scanPatchInventoryRow(rows)
		if err != nil {
			return nil, "", http.StatusInternalServerError, err
		}
		payload := patchInventoryPayload(row)
		attachActivePatchInstallJob(payload, activeJobs)
		result = append(result, payload)
	}
	if err := rows.Err(); err != nil {
		return nil, "", http.StatusInternalServerError, err
	}
	return result, nullString(device.Hostname), http.StatusOK, nil
}

func (s *postgresOperatorStore) listPatchInstallTargets(ctx context.Context, profile operatorProfile, request patchInstallLookupRequest) ([]patchInstallTarget, int, error) {
	allowedSiteIDs, err := s.siteIDsForProfile(ctx, profile)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if allowedSiteIDs != nil && len(allowedSiteIDs) == 0 {
		return []patchInstallTarget{}, http.StatusOK, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	query := `
		SELECT
			dpi.id,
			dpi.device_guid,
			dpi.patch_key,
			dpi.kb,
			dpi.title,
			dpi.state,
			dpi.source,
			dpi.classification,
			dpi.severity,
			dpi.installed_on,
			dpi.published_at,
			dpi.captured_at,
			dpi.metadata_json,
			d.guid,
			d.hostname,
			d.agent_id,
			d.operating_system,
			ds.site_id,
			s.name
		  FROM engine.device_patch_inventory AS dpi
		  JOIN engine.devices AS d
		    ON d.guid = dpi.device_guid
	 LEFT JOIN engine.device_sites AS ds
		    ON ds.device_hostname = d.hostname
	 LEFT JOIN engine.sites AS s
		    ON s.id = ds.site_id
		 WHERE LOWER(TRIM(COALESCE(dpi.state, ''))) = 'pending'
	`
	args := []any{}
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if request.Hostname != "" {
		query += " AND LOWER(d.hostname) = LOWER(" + addArg(request.Hostname) + ")"
	}
	switch {
	case request.InventoryID > 0:
		query += " AND dpi.id = " + addArg(request.InventoryID)
	case request.PatchKey != "":
		query += " AND dpi.patch_key = " + addArg(request.PatchKey)
	case request.KB != "":
		query += " AND UPPER(COALESCE(dpi.kb, '')) = UPPER(" + addArg(request.KB) + ")"
	case request.Title != "":
		query += " AND LOWER(COALESCE(dpi.title, '')) = LOWER(" + addArg(request.Title) + ")"
	default:
		return []patchInstallTarget{}, http.StatusOK, nil
	}
	if request.SiteID > 0 {
		query += " AND ds.site_id = " + addArg(request.SiteID)
	}
	if allowedSiteIDs != nil {
		placeholders := make([]string, 0, len(allowedSiteIDs))
		for _, siteID := range allowedSiteIDs {
			placeholders = append(placeholders, addArg(siteID))
		}
		query += " AND ds.site_id IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY LOWER(d.hostname), dpi.id"

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	targets := []patchInstallTarget{}
	siteIDs := map[int64]struct{}{}
	for rows.Next() {
		row, err := scanPatchInventoryRow(rows)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		if row.SiteID.Valid && row.SiteID.Int64 > 0 {
			siteIDs[row.SiteID.Int64] = struct{}{}
		}
		targets = append(targets, patchInstallTarget{Row: row})
	}
	if err := rows.Err(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if err := rows.Close(); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	routeCache := map[int64]*agentWorkerRoute{}
	for siteID := range siteIDs {
		route, err := fetchAgentWorkerRoute(ctx, conn, siteID)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		routeCache[siteID] = route
	}
	for idx := range targets {
		if targets[idx].Row.SiteID.Valid {
			targets[idx].Route = routeCache[targets[idx].Row.SiteID.Int64]
		}
	}
	return targets, http.StatusOK, nil
}

type patchRowScanner interface {
	Scan(dest ...any) error
}

func scanPatchInventoryRow(scanner patchRowScanner) (patchInventoryRow, error) {
	var row patchInventoryRow
	err := scanner.Scan(
		&row.ID,
		&row.DeviceGUID,
		&row.PatchKey,
		&row.KB,
		&row.Title,
		&row.State,
		&row.Source,
		&row.Classification,
		&row.Severity,
		&row.InstalledOn,
		&row.PublishedAt,
		&row.CapturedAt,
		&row.MetadataJSON,
		&row.GUID,
		&row.Hostname,
		&row.AgentID,
		&row.OperatingSystem,
		&row.SiteID,
		&row.SiteName,
	)
	return row, err
}

func patchInventoryPayload(row patchInventoryRow) map[string]any {
	metadata := agentDetailsMapFromAny(parseJSON(row.MetadataJSON))
	payload := map[string]any{
		"id":               nullInt(row.ID),
		"inventory_id":     nullInt(row.ID),
		"device_guid":      cleanText(nullString(row.DeviceGUID)),
		"patch_key":        cleanText(nullString(row.PatchKey)),
		"kb":               cleanText(nullString(row.KB)),
		"title":            cleanText(nullString(row.Title)),
		"state":            normalizePatchState(nullString(row.State)),
		"source":           normalizePatchSource(nullString(row.Source)),
		"classification":   cleanText(nullString(row.Classification)),
		"severity":         cleanText(nullString(row.Severity)),
		"installed_on":     nullableInt(row.InstalledOn),
		"published_at":     nullableInt(row.PublishedAt),
		"captured_at":      nullableInt(row.CapturedAt),
		"metadata":         metadata,
		"hostname":         cleanText(nullString(row.Hostname)),
		"agent_id":         cleanText(nullString(row.AgentID)),
		"operating_system": cleanText(nullString(row.OperatingSystem)),
		"platform":         normalizeSoftwarePlatform(nullString(row.OperatingSystem)),
		"site_id":          nil,
		"site_name":        cleanText(nullString(row.SiteName)),
	}
	if row.SiteID.Valid {
		payload["site_id"] = row.SiteID.Int64
	}
	for _, key := range []string{"is_downloaded", "is_mandatory", "requires_reboot", "update_id", "revision_number"} {
		if value, ok := metadata[key]; ok {
			payload[key] = value
		}
	}
	return payload
}

var _ patchInventoryStore = (*postgresOperatorStore)(nil)
