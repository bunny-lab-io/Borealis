package main

import (
	"context"
	"net/http"
	"strings"
	"time"
)

const remoteRegistryDefaultTimeoutSeconds = 30.0

func registerRemoteRegistryRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/device/registry/", remoteRegistrySubtreeHandler(auth, fallback))
}

func remoteRegistrySubtreeHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hostname, suffix, ok := splitRemoteRegistryPath(r.URL.Path)
		if !ok {
			proxyFallbackOrMethodNotAllowed(w, r, fallback, "GET, POST")
			return
		}
		switch suffix {
		case "roots":
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, "GET")
				return
			}
			remoteRegistryRootsHandler(auth)(w, r, hostname)
		case "children":
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, "GET")
				return
			}
			remoteRegistryChildrenHandler(auth)(w, r, hostname)
		case "key/create":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteRegistryMutateHandler(auth, "key_create")(w, r, hostname)
		case "key/rename":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteRegistryMutateHandler(auth, "key_rename")(w, r, hostname)
		case "key/delete":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteRegistryMutateHandler(auth, "key_delete")(w, r, hostname)
		case "value/create":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteRegistryMutateHandler(auth, "value_create")(w, r, hostname)
		case "value/update":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteRegistryMutateHandler(auth, "value_update")(w, r, hostname)
		case "value/delete":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteRegistryMutateHandler(auth, "value_delete")(w, r, hostname)
		default:
			proxyFallbackOrMethodNotAllowed(w, r, fallback, "GET, POST")
		}
	}
}

func splitRemoteRegistryPath(path string) (string, string, bool) {
	const prefix = "/api/device/registry/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	hostname := cleanText(parts[0])
	suffix := strings.Trim(strings.TrimSpace(parts[1]), "/")
	if hostname == "" || suffix == "" {
		return "", "", false
	}
	return hostname, suffix, true
}

func remoteRegistryRootsHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		snapshot, operatorID, ok := requireRemoteRegistryContext(w, r, auth, hostname)
		if !ok {
			return
		}
		response, status, workerErr := callRemoteRegistryRPC(r.Context(), auth, snapshot, map[string]any{
			"action":       "roots",
			"hostname":     snapshot.Hostname,
			"agent_id":     snapshot.AgentID,
			"requested_by": operatorID,
		}, remoteRegistryDefaultTimeoutSeconds)
		if workerErr != nil {
			writeJSON(w, status, workerErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"hostname":      snapshot.Hostname,
			"platform":      strings.ToLower(cleanText(response["platform"])),
			"context_label": firstText(cleanText(response["context_label"]), ""),
			"current_path":  firstNonEmpty(response["current_path"], nil),
			"entries":       arrayOrEmpty(response["entries"]),
		})
	}
}

func remoteRegistryChildrenHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		requestedPath := cleanText(r.URL.Query().Get("path"))
		if requestedPath == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "path_required"})
			return
		}
		if err := validateRegistryInput("path", requestedPath); err != nil {
			writePublicValidationErrors(w, []publicValidationError{{Field: "path", Message: err.Error()}})
			return
		}
		snapshot, operatorID, ok := requireRemoteRegistryContext(w, r, auth, hostname)
		if !ok {
			return
		}
		response, status, workerErr := callRemoteRegistryRPC(r.Context(), auth, snapshot, map[string]any{
			"action":       "children",
			"hostname":     snapshot.Hostname,
			"agent_id":     snapshot.AgentID,
			"requested_by": operatorID,
			"path":         requestedPath,
		}, remoteRegistryDefaultTimeoutSeconds)
		if workerErr != nil {
			writeJSON(w, status, workerErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"hostname":      snapshot.Hostname,
			"platform":      strings.ToLower(cleanText(response["platform"])),
			"context_label": firstText(cleanText(response["context_label"]), ""),
			"current_path":  firstText(cleanText(response["current_path"]), requestedPath),
			"entries":       arrayOrEmpty(response["entries"]),
			"values":        arrayOrEmpty(response["values"]),
		})
	}
}

func remoteRegistryMutateHandler(auth *authService, action string) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		body, err := readJSONMap(r)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		if errs := validateRemoteRegistryMutationBody(body); len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		snapshot, operatorID, ok := requireRemoteRegistryContext(w, r, auth, hostname)
		if !ok {
			return
		}
		payload := map[string]any{
			"action":       action,
			"hostname":     snapshot.Hostname,
			"agent_id":     snapshot.AgentID,
			"requested_by": operatorID,
		}
		for key, value := range body {
			payload[key] = value
		}
		response, status, workerErr := callRemoteRegistryRPC(r.Context(), auth, snapshot, payload, remoteRegistryDefaultTimeoutSeconds)
		if workerErr != nil {
			writeJSON(w, status, workerErr)
			return
		}
		result := map[string]any{"hostname": snapshot.Hostname}
		for key, value := range response {
			result[key] = value
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func requireRemoteRegistryContext(w http.ResponseWriter, r *http.Request, auth *authService, hostname string) (deviceProcessContext, string, bool) {
	profile, err := auth.currentProfile(r.Context(), r)
	if err != nil {
		if isUnauthorizedAuthError(err) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return deviceProcessContext{}, "", false
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "auth_unavailable", "detail": err.Error()})
		return deviceProcessContext{}, "", false
	}
	store, ok := auth.store.(deviceProcessStore)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "remote_registry_unavailable"})
		return deviceProcessContext{}, "", false
	}
	ctx, cancel := requestTimeout(r.Context(), auth)
	defer cancel()
	snapshot, status, err := store.loadDeviceProcessContext(ctx, profile, hostname)
	if err != nil {
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return deviceProcessContext{}, "", false
	}
	if snapshot.Route == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "agent_unavailable",
			"message": "The agent SYSTEM socket is not available.",
		})
		return deviceProcessContext{}, "", false
	}
	workerCtx, workerCancel := requestTimeout(r.Context(), auth)
	registered := workerHostServiceRegistered(workerCtx, auth, snapshot.Route, snapshot.Hostname, "system")
	workerCancel()
	if !registered {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "agent_unavailable",
			"message": "The agent SYSTEM socket is not available.",
		})
		return deviceProcessContext{}, "", false
	}
	return snapshot, firstText(cleanText(profile.Username), "unknown"), true
}

func callRemoteRegistryRPC(ctx context.Context, auth *authService, snapshot deviceProcessContext, payload map[string]any, timeoutSeconds float64) (map[string]any, int, map[string]any) {
	seconds := remoteRegistryTimeoutSeconds(timeoutSeconds)
	response, status, workerErr := callWorkerHostServiceEvent(ctx, auth, snapshot.Route, map[string]any{
		"hostname":        snapshot.Hostname,
		"service_mode":    "system",
		"event_name":      "registry_management_request",
		"timeout_seconds": seconds,
		"payload":         payload,
	}, time.Duration(seconds+1.0)*time.Second)
	if workerErr != nil {
		return nil, status, remoteRegistryErrorPayload(status, workerErr)
	}
	return response, http.StatusOK, nil
}

func remoteRegistryErrorPayload(status int, payload map[string]any) map[string]any {
	errorCode := cleanText(payload["error"])
	message := cleanText(payload["message"])
	result := map[string]any{}
	for key, value := range payload {
		result[key] = value
	}
	if strings.EqualFold(errorCode, "invalid_request") && strings.Contains(message, "Unsupported registry-management action") {
		result["error"] = "agent_update_required"
		result["message"] = "The device agent needs to be updated before Registry Editor is available."
		return result
	}
	if errorCode == "" {
		errorCode = "worker_error"
		result["error"] = errorCode
	}
	if message == "" {
		switch status {
		case http.StatusServiceUnavailable:
			message = "The agent SYSTEM socket is not available."
		case http.StatusGatewayTimeout:
			message = "The device did not answer the registry request in time."
		}
		if message != "" {
			result["message"] = message
		}
	}
	return result
}

func validateRemoteRegistryMutationBody(body map[string]any) []publicValidationError {
	errs := make([]publicValidationError, 0)
	for _, key := range []string{"path", "parent_path", "source_path", "destination_path", "target_path", "confirm_path"} {
		if value := cleanText(body[key]); value != "" {
			if err := validateRegistryInput(key, value); err != nil {
				errs = append(errs, publicValidationError{Field: key, Message: err.Error()})
			}
		}
	}
	for _, key := range []string{"name", "new_name", "value_name"} {
		if value := cleanText(body[key]); value != "" {
			if err := validateRegistryInput(key, value); err != nil {
				errs = append(errs, publicValidationError{Field: key, Message: err.Error()})
			}
		}
	}
	if kind := cleanText(firstNonEmpty(body["kind"], body["type"], body["value_type"])); kind != "" {
		if err := validateIdentifierInput("value_type", kind); err != nil {
			errs = append(errs, publicValidationError{Field: "value_type", Message: err.Error()})
		}
	}
	return errs
}

func remoteRegistryTimeoutSeconds(value float64) float64 {
	if value <= 0 {
		return remoteRegistryDefaultTimeoutSeconds
	}
	if value < 1 {
		return 1
	}
	if value > 300 {
		return 300
	}
	return value
}
