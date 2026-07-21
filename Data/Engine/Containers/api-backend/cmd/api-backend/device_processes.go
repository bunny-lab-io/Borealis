package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	processCollectingRetryAfterMS        = 1200
	processListHostServiceTimeoutSeconds = 24.0
	processListWorkerRequestTimeout      = 25 * time.Second
	processTerminateTimeoutSeconds       = 30.0
	processTerminateWorkerRequestTimeout = 31 * time.Second
)

type deviceProcessStore interface {
	loadDeviceProcessContext(ctx context.Context, profile operatorProfile, hostname string) (deviceProcessContext, int, error)
}

type deviceProcessContext struct {
	GUID     string
	Hostname string
	AgentID  string
	SiteID   *int64
	Route    *agentWorkerRoute
}

func registerProcessRoutes(mux *http.ServeMux, auth *authService, _ http.Handler) {
	mux.HandleFunc("GET /api/device/processes/{hostname}", deviceProcessListHandler(auth))
	mux.HandleFunc("POST /api/device/processes/{hostname}/terminate", deviceProcessTerminateHandler(auth))
}

func deviceProcessListHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot, ok := loadDeviceProcessSnapshotForRequest(w, r, auth)
		if !ok {
			return
		}

		maxAge := parseFloatDefault(r.URL.Query().Get("max_age_seconds"), 5.0)
		if maxAge < 0.25 {
			maxAge = 0.25
		}
		if maxAge > 15.0 {
			maxAge = 15.0
		}
		response, workerStatus, workerErr := callWorkerHostServiceEvent(r.Context(), auth, snapshot.Route, map[string]any{
			"hostname":        snapshot.Hostname,
			"service_mode":    "system",
			"event_name":      "process_management_request",
			"timeout_seconds": processListHostServiceTimeoutSeconds,
			"payload": map[string]any{
				"action":          "list",
				"max_age_seconds": maxAge,
				"requested_at":    time.Now().Unix(),
			},
		}, processListWorkerRequestTimeout)
		if workerErr != nil {
			writeJSON(w, workerStatus, workerErr)
			return
		}

		processes, _ := response["processes"].([]any)
		if processes == nil {
			processes = []any{}
		}
		reportedAt := coerceInt64(response["reported_at"])
		collectionState := processCollectionState(response, len(processes))
		retryAfterMS := processRetryAfterMS(response, collectionState)
		writeJSON(w, http.StatusOK, map[string]any{
			"status":              "ok",
			"collection_state":    collectionState,
			"hostname":            firstText(snapshot.Hostname, r.PathValue("hostname")),
			"agent_id":            snapshot.AgentID,
			"agent_socket":        true,
			"reported_at":         reportedAt,
			"refresh_interval_ms": maxInt64(5000, coerceInt64(firstNonEmpty(response["refresh_interval_ms"], 5000))),
			"retry_after_ms":      retryAfterMS,
			"count":               len(processes),
			"processes":           processes,
		})
	}
}

func deviceProcessTerminateHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := readJSONMap(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": "Request body must be valid JSON."})
			return
		}
		pid := coerceInt64(body["pid"])
		if pid <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "pid_required"})
			return
		}
		snapshot, ok := loadDeviceProcessSnapshotForRequest(w, r, auth)
		if !ok {
			return
		}
		profile, err := auth.currentProfile(r.Context(), r)
		if err != nil {
			if isUnauthorizedAuthError(err) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
			return
		}
		response, workerStatus, workerErr := callWorkerHostServiceEvent(r.Context(), auth, snapshot.Route, map[string]any{
			"hostname":        snapshot.Hostname,
			"service_mode":    "system",
			"event_name":      "process_management_request",
			"timeout_seconds": processTerminateTimeoutSeconds,
			"payload": map[string]any{
				"action":           "terminate",
				"pid":              pid,
				"include_children": boolFromAny(body["include_children"]),
				"requested_at":     time.Now().Unix(),
				"requested_by":     firstText(cleanText(profile.Username), "unknown"),
			},
		}, processTerminateWorkerRequestTimeout)
		if workerErr != nil {
			writeJSON(w, workerStatus, workerErr)
			return
		}
		processes, _ := response["processes"].([]any)
		if processes == nil {
			processes = []any{}
		}
		collectionState := processCollectionState(response, len(processes))
		writeJSON(w, http.StatusOK, map[string]any{
			"status":              "ok",
			"collection_state":    collectionState,
			"hostname":            firstText(snapshot.Hostname, r.PathValue("hostname")),
			"agent_id":            snapshot.AgentID,
			"terminated_pid":      pid,
			"reported_at":         coerceInt64(response["reported_at"]),
			"refresh_interval_ms": maxInt64(5000, coerceInt64(firstNonEmpty(response["refresh_interval_ms"], 5000))),
			"retry_after_ms":      processRetryAfterMS(response, collectionState),
			"count":               len(processes),
			"processes":           processes,
		})
	}
}

func loadDeviceProcessSnapshotForRequest(w http.ResponseWriter, r *http.Request, auth *authService) (deviceProcessContext, bool) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return deviceProcessContext{}, false
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return deviceProcessContext{}, false
	}
	store, ok := auth.store.(deviceProcessStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "device_processes_unavailable"})
		return deviceProcessContext{}, false
	}
	timeout := auth.timeout
	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	snapshot, status, err := store.loadDeviceProcessContext(ctx, profile, r.PathValue("hostname"))
	if err != nil {
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return deviceProcessContext{}, false
	}
	if snapshot.Route == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "agent_unavailable",
			"message": "The agent SYSTEM socket is not available.",
		})
		return deviceProcessContext{}, false
	}

	workerCtx, workerCancel := context.WithTimeout(r.Context(), 5*time.Second)
	registered := workerHostServiceRegistered(workerCtx, auth, snapshot.Route, snapshot.Hostname, "system")
	workerCancel()
	if !registered {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "agent_unavailable",
			"message": "The agent SYSTEM socket is not available.",
		})
		return deviceProcessContext{}, false
	}
	return snapshot, true
}

func processCollectionState(response map[string]any, processCount int) string {
	state := strings.ToLower(cleanText(response["collection_state"]))
	switch state {
	case "ready", "collecting":
		return state
	}
	if processCount == 0 {
		return "collecting"
	}
	return "ready"
}

func processRetryAfterMS(response map[string]any, collectionState string) int64 {
	retryAfterMS := coerceInt64(response["retry_after_ms"])
	if retryAfterMS <= 0 && collectionState == "collecting" {
		return processCollectingRetryAfterMS
	}
	if retryAfterMS < 0 {
		return 0
	}
	return retryAfterMS
}

func (s *postgresOperatorStore) loadDeviceProcessContext(ctx context.Context, profile operatorProfile, hostname string) (deviceProcessContext, int, error) {
	hostname = cleanText(hostname)
	if hostname == "" {
		return deviceProcessContext{}, http.StatusNotFound, errors.New("not found")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return deviceProcessContext{}, http.StatusInternalServerError, errors.Join(errOperatorStoreDown, err)
	}
	defer conn.Close()

	var row struct {
		GUID     sql.NullString
		Hostname sql.NullString
		AgentID  sql.NullString
		Status   sql.NullString
		SiteID   sql.NullInt64
	}
	err = conn.QueryRowContext(ctx, `
		SELECT d.guid, d.hostname, d.agent_id, COALESCE(d.status, 'active'), ds.site_id
		  FROM engine.devices AS d
	 LEFT JOIN engine.device_sites AS ds ON ds.device_hostname = d.hostname
		 WHERE LOWER(d.hostname) = LOWER($1)
	  ORDER BY COALESCE(d.last_seen, 0) DESC
		 LIMIT 1
	`, hostname).Scan(&row.GUID, &row.Hostname, &row.AgentID, &row.Status, &row.SiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return deviceProcessContext{}, http.StatusNotFound, errors.New("not found")
	}
	if err != nil {
		return deviceProcessContext{}, http.StatusInternalServerError, err
	}
	if row.SiteID.Valid {
		allowed, err := profileCanAccessSite(ctx, conn, profile, row.SiteID.Int64)
		if err != nil {
			return deviceProcessContext{}, http.StatusInternalServerError, err
		}
		if !allowed {
			return deviceProcessContext{}, http.StatusNotFound, errors.New("not found")
		}
	} else if !strings.EqualFold(strings.TrimSpace(profile.Role), "admin") {
		return deviceProcessContext{}, http.StatusNotFound, errors.New("not found")
	}
	if !deviceAllowsRemoteAccess(row.Status.String) {
		return deviceProcessContext{}, http.StatusForbidden, errors.New("device_quarantined")
	}

	snapshot := deviceProcessContext{
		GUID:     normalizeCanonicalGUID(row.GUID.String),
		Hostname: nullString(row.Hostname),
		AgentID:  row.AgentID.String,
	}
	if row.SiteID.Valid && row.SiteID.Int64 > 0 {
		siteID := row.SiteID.Int64
		snapshot.SiteID = &siteID
		route, err := fetchAgentWorkerRoute(ctx, conn, siteID)
		if err != nil {
			return deviceProcessContext{}, http.StatusInternalServerError, err
		}
		snapshot.Route = route
	}
	return snapshot, http.StatusOK, nil
}

const workerHostServiceCallResponseMaxBytes = 64 << 20

func callWorkerHostServiceEvent(ctx context.Context, auth *authService, route *agentWorkerRoute, body map[string]any, timeout time.Duration) (map[string]any, int, map[string]any) {
	if auth == nil || route == nil {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "agent_unavailable", "message": "The agent SYSTEM socket is not available."}
	}
	target := workerInternalURL(route, "/remote-ops/host-service/call")
	if target == "" {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "agent_unavailable", "message": "The agent SYSTEM socket is not available."}
	}
	if timeout <= 0 {
		timeout = 9 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": "The process request could not be encoded."}
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "worker_request_failed", "message": err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "agent_unavailable", "message": err.Error()}
	}
	defer resp.Body.Close()

	var payloadMap map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, workerHostServiceCallResponseMaxBytes)).Decode(&payloadMap); err != nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "invalid_worker_response", "message": "The site-worker returned an invalid response."}
	}
	if resp.StatusCode >= 400 {
		status := resp.StatusCode
		errorCode := cleanText(payloadMap["error"])
		if status == 0 {
			status = http.StatusBadGateway
		}
		if errorCode == "" {
			payloadMap["error"] = "worker_error"
		}
		return nil, status, payloadMap
	}
	if !boolFromAny(payloadMap["called"]) {
		return nil, http.StatusGatewayTimeout, map[string]any{"error": "timeout", "message": "The device did not answer the process request in time."}
	}
	response, ok := payloadMap["response"].(map[string]any)
	if !ok {
		return nil, http.StatusBadGateway, map[string]any{"error": "invalid_agent_response", "message": "The device returned an invalid process response."}
	}
	if boolFromAny(response["ok"]) == false && response["ok"] != nil {
		errorCode := cleanText(response["error"])
		if errorCode == "" {
			errorCode = "agent_error"
		}
		message := cleanText(response["message"])
		if message == "" {
			message = errorCode
		}
		return nil, processErrorStatus(errorCode), map[string]any{"error": errorCode, "message": message}
	}
	return response, http.StatusOK, nil
}

func processErrorStatus(errorCode string) int {
	switch strings.ToLower(strings.TrimSpace(errorCode)) {
	case "invalid_action", "invalid_request", "pid_required", "path_required", "invalid_path", "invalid_hive", "invalid_name", "type_required", "confirmation_required", "invalid_value", "patch_required":
		return http.StatusBadRequest
	case "process_not_found", "not_found", "path_not_found", "update_not_found":
		return http.StatusNotFound
	case "access_denied", "permission_denied":
		return http.StatusForbidden
	case "protected_process", "termination_failed", "conflict", "key_has_children", "patch_not_pending", "download_failed", "install_failed", "install_in_progress":
		return http.StatusConflict
	case "value_too_large":
		return http.StatusRequestEntityTooLarge
	case "unsupported", "unsupported_platform", "unsupported_type":
		return http.StatusNotImplemented
	case "timeout":
		return http.StatusGatewayTimeout
	case "agent_unavailable", "socket_unavailable":
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}
