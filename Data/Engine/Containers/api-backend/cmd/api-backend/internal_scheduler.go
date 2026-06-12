package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func registerInternalSchedulerRoutes(mux *http.ServeMux, auth *authService, vpnRuntime *vpnTunnelService, fallback http.Handler) {
	_ = fallback
	mux.HandleFunc("/api/internal/job-scheduler/credential/", internalSchedulerCredentialHandler(auth))
	mux.HandleFunc("/api/internal/job-scheduler/service-account/", internalSchedulerServiceAccountHandler(auth))
	mux.HandleFunc("/api/internal/job-scheduler/public-base-url", internalSchedulerPublicBaseURLHandler(auth))
	mux.HandleFunc("/api/internal/job-scheduler/host-service-event", internalSchedulerHostServiceEventHandler(auth))
	mux.HandleFunc("/api/internal/job-scheduler/vpn-sessions", internalSchedulerVPNSessionsHandler(auth, vpnRuntime))
	mux.HandleFunc("/api/internal/job-scheduler/vpn-prepare", internalSchedulerVPNPrepareHandler(auth, vpnRuntime))
	mux.HandleFunc("/api/internal/job-scheduler/work-items", internalSchedulerWorkItemsHandler(auth))
}

var errSchedulerCredentialResetRequired = errors.New("Stored credential secret material was reset. Edit and save the credential before enabling jobs that use it.")

type internalSchedulerCredentialStore interface {
	loadDecryptedSchedulerCredential(ctx context.Context, secret authSecretService, credentialID int64) (map[string]any, bool, error)
}

type internalSchedulerServiceAccountStore interface {
	loadSchedulerServiceAccount(ctx context.Context, agentID string) (map[string]any, bool, error)
}

type internalSchedulerWorkItemStore interface {
	enqueueInternalSchedulerWorkItem(ctx context.Context, kind string, payload map[string]any) (int64, error)
}

func internalSchedulerCredentialHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		if !validInternalSchedulerRequest(auth, r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		if auth == nil || auth.aegis == nil {
			writeJSON(w, http.StatusLocked, map[string]any{"error": "aegis_locked", "message": errAegisLocked.Error()})
			return
		}
		store, ok := auth.store.(internalSchedulerCredentialStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "credential_store_unavailable"})
			return
		}
		rawID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/internal/job-scheduler/credential/"), "/")
		credentialID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || credentialID <= 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "credential_not_found"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		credential, found, err := store.loadDecryptedSchedulerCredential(ctx, auth.aegis, credentialID)
		if err != nil {
			if errors.Is(err, errSchedulerCredentialResetRequired) {
				writeJSON(w, http.StatusLocked, map[string]any{"error": "credential_reset_required", "message": errSchedulerCredentialResetRequired.Error()})
				return
			}
			if errors.Is(err, errAegisNotConfigured) || errors.Is(err, errAegisLocked) || errors.Is(err, errAegisDataCorruption) {
				writeJSON(w, protectedSecretErrorStatus(err), protectedSecretErrorBody(err))
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "credential_not_found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"credential": credential})
	}
}

func internalSchedulerServiceAccountHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		if !validInternalSchedulerRequest(auth, r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		store, ok := auth.store.(internalSchedulerServiceAccountStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "service_account_store_unavailable"})
			return
		}
		rawAgentID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/internal/job-scheduler/service-account/"), "/")
		agentID, err := url.PathUnescape(rawAgentID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_account_not_found"})
			return
		}
		agentID = strings.TrimSpace(agentID)
		if agentID == "" {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_account_not_found"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		serviceAccount, found, err := store.loadSchedulerServiceAccount(ctx, agentID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_account_not_found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"service_account": serviceAccount})
	}
}

func internalSchedulerPublicBaseURLHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		if !validInternalSchedulerRequest(auth, r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"public_base_url": configuredPublicBaseURL(r)})
	}
}

func internalSchedulerWorkItemsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		if !validInternalSchedulerRequest(auth, r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		store, ok := auth.store.(internalSchedulerWorkItemStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "scheduler_work_item_store_unavailable"})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		kind := strings.ToLower(cleanText(firstNonEmpty(body["kind"], body["work_kind"])))
		if kind == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "work_item_kind_required"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		workID, err := store.enqueueInternalSchedulerWorkItem(ctx, kind, body)
		if err != nil {
			writeJSON(w, internalSchedulerWorkItemErrorStatus(err), map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"work_id": workID})
	}
}

func internalSchedulerWorkItemErrorStatus(err error) int {
	message := ""
	if err != nil {
		message = err.Error()
	}
	if strings.Contains(message, "_required") || strings.HasPrefix(message, "unsupported_work_item_kind:") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func validInternalSchedulerRequest(auth *authService, r *http.Request) bool {
	if auth == nil || auth.verifier == nil || len(auth.verifier.secret) == 0 {
		return false
	}
	expected := goInternalToken(auth.verifier.secret)
	presented := strings.TrimSpace(r.Header.Get(internalTokenHeader))
	if expected == "" || presented == "" {
		return false
	}
	return hmac.Equal([]byte(expected), []byte(presented))
}

func configuredPublicBaseURL(r *http.Request) string {
	for _, name := range []string{"BOREALIS_PUBLIC_BASE_URL", "PUBLIC_BASE_URL"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return strings.TrimRight(value, "/")
		}
	}
	return publicBaseURLForRequest(r)
}

func internalSchedulerHostServiceEventHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		if !validInternalSchedulerRequest(auth, r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		store, ok := auth.store.(deviceProcessStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "device_route_lookup_unavailable"})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		hostname := cleanText(body["hostname"])
		serviceMode := firstText(cleanText(firstNonEmpty(body["service_mode"], body["mode"])), "system")
		eventName := cleanText(body["event_name"])
		if hostname == "" || eventName == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "hostname_and_event_name_required"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		snapshot, status, err := store.loadDeviceProcessContext(ctx, operatorProfile{Username: "job-scheduler", Role: "Admin"}, hostname)
		if err != nil {
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		if snapshot.Route == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "No active site-worker route is available for this device site."})
			return
		}
		pendingTTL := coerceInt64(body["pending_ttl_seconds"])
		if pendingTTL <= 0 {
			pendingTTL = 180
		}
		result, workerStatus, workerErr := emitWorkerHostServiceEvent(r.Context(), auth, snapshot.Route, map[string]any{
			"hostname":            snapshot.Hostname,
			"service_mode":        serviceMode,
			"event_name":          eventName,
			"payload":             firstNonEmpty(body["payload"], map[string]any{}),
			"allow_pending":       boolFromAny(body["allow_pending"]),
			"pending_ttl_seconds": pendingTTL,
		}, 6*time.Second)
		if workerErr != nil {
			writeJSON(w, workerStatus, workerErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"emitted": boolFromAny(result["emitted"]),
			"queued":  boolFromAny(result["queued"]),
		})
	}
}

func internalSchedulerVPNSessionsHandler(auth *authService, vpnRuntime *vpnTunnelService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		if !validInternalSchedulerRequest(auth, r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": vpnSessionMap(vpnRuntime)})
	}
}

func internalSchedulerVPNPrepareHandler(auth *authService, vpnRuntime *vpnTunnelService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method_not_allowed"})
			return
		}
		if !validInternalSchedulerRequest(auth, r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		if vpnRuntime == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "tunnel_unavailable"})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		agentIDs := schedulerCleanStringList(body["agent_ids"])
		requiredPorts := coercePortList(body["required_ports"])
		endpointHost := cleanText(body["endpoint_host"])
		if endpointHost == "" {
			endpointHost = inferWireGuardEndpointHost(r)
		}
		requestedStart := false
		for _, agentID := range agentIDs {
			if vpnRuntime.sessionPayload(agentID, false) != nil {
				vpnRuntime.requestAgentStart(r.Context(), agentID, false, firstText(cleanText(body["reason"]), "job_scheduler_prepare"), requiredPorts)
				requestedStart = true
				continue
			}
			if _, err := vpnRuntime.connect(r.Context(), vpnConnectRequest{
				AgentID:       agentID,
				EndpointHost:  endpointHost,
				MarkActivity:  false,
				RequiredPorts: requiredPorts,
			}); err == nil {
				requestedStart = true
			}
		}
		sessions := vpnSessionMap(vpnRuntime)
		if requestedStart && len(agentIDs) > 0 {
			sessions = vpnRuntime.waitForSessionsReady(
				agentIDs,
				requiredPorts,
				coercePositiveFloat(firstNonEmpty(body["timeout_seconds"], body["wait_seconds"]), 45),
				coercePositiveFloat(body["poll_interval_seconds"], 0.5),
			)
		}
		for _, agentID := range agentIDs {
			if payload := sessions[agentID]; payload != nil {
				payload["_requested_start"] = true
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
	}
}

func vpnSessionMap(vpnRuntime *vpnTunnelService) map[string]map[string]any {
	out := map[string]map[string]any{}
	if vpnRuntime == nil {
		return out
	}
	for _, session := range vpnRuntime.listSessions() {
		agentID := cleanText(session["agent_id"])
		if agentID != "" {
			out[agentID] = session
		}
	}
	return out
}

func schedulerCleanStringList(value any) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(item any) {
		cleaned := cleanText(item)
		if cleaned == "" {
			return
		}
		if _, ok := seen[cleaned]; ok {
			return
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			add(item)
		}
	case []string:
		for _, item := range typed {
			add(item)
		}
	case string:
		for _, item := range strings.FieldsFunc(typed, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' }) {
			add(item)
		}
	default:
		add(typed)
	}
	return out
}

func coercePositiveFloat(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return typed
		}
	case float32:
		if typed > 0 {
			return float64(typed)
		}
	case int:
		if typed > 0 {
			return float64(typed)
		}
	case int64:
		if typed > 0 {
			return float64(typed)
		}
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func emitWorkerHostServiceEvent(ctx context.Context, auth *authService, route *agentWorkerRoute, body map[string]any, timeout time.Duration) (map[string]any, int, map[string]any) {
	if auth == nil || route == nil {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable"}
	}
	target := workerInternalURL(route, "/remote-ops/host-service/event")
	if target == "" {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable"}
	}
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": "The host-service event request could not be encoded."}
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
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}
	}
	defer resp.Body.Close()
	var payloadMap map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payloadMap); err != nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "invalid_worker_response", "message": "The site-worker returned an invalid response."}
	}
	if resp.StatusCode >= 400 {
		status := resp.StatusCode
		if cleanText(payloadMap["error"]) == "" {
			payloadMap["error"] = "site_worker_error"
		}
		return nil, status, payloadMap
	}
	return payloadMap, http.StatusOK, nil
}
