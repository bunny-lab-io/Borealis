package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const remoteFileDefaultTimeoutSeconds = 30.0

func registerRemoteFileRoutes(mux *http.ServeMux, auth *authService, fallback http.Handler) {
	mux.HandleFunc("/api/device/files/", remoteFileSubtreeHandler(auth, fallback))
}

func remoteFileSubtreeHandler(auth *authService, fallback http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hostname, suffix, ok := splitRemoteFilePath(r.URL.Path)
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
			remoteFileRootsHandler(auth)(w, r, hostname)
		case "children":
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, "GET")
				return
			}
			remoteFileChildrenHandler(auth)(w, r, hostname)
		case "upload/conflicts":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteFileUploadConflictsHandler(auth)(w, r, hostname)
		case "text":
			if r.Method == http.MethodGet {
				remoteFileReadTextHandler(auth)(w, r, hostname)
				return
			}
			if r.Method == http.MethodPost {
				remoteFileWriteTextHandler(auth)(w, r, hostname)
				return
			}
			writeMethodNotAllowed(w, "GET, POST")
		case "mkdir":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteFileMkdirHandler(auth)(w, r, hostname)
		case "rename":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteFileRenameHandler(auth)(w, r, hostname)
		case "move":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteFileMoveHandler(auth)(w, r, hostname)
		case "delete":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteFileDeleteHandler(auth)(w, r, hostname)
		case "paste":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteFilePasteHandler(auth)(w, r, hostname)
		default:
			proxyFallbackOrMethodNotAllowed(w, r, fallback, "GET, POST")
		}
	}
}

func splitRemoteFilePath(path string) (string, string, bool) {
	const prefix = "/api/device/files/"
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

func remoteFileRootsHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		snapshot, operatorID, ok := requireRemoteFileContext(w, r, auth, hostname)
		if !ok {
			return
		}
		response, status, workerErr := callRemoteFileRPC(r.Context(), auth, snapshot, map[string]any{
			"action":       "roots",
			"hostname":     snapshot.Hostname,
			"agent_id":     snapshot.AgentID,
			"requested_by": operatorID,
		}, remoteFileDefaultTimeoutSeconds)
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

func remoteFileChildrenHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		requestedPath := cleanText(r.URL.Query().Get("path"))
		if requestedPath == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "path_required"})
			return
		}
		snapshot, operatorID, ok := requireRemoteFileContext(w, r, auth, hostname)
		if !ok {
			return
		}
		response, status, workerErr := callRemoteFileRPC(r.Context(), auth, snapshot, map[string]any{
			"action":       "children",
			"hostname":     snapshot.Hostname,
			"agent_id":     snapshot.AgentID,
			"requested_by": operatorID,
			"path":         requestedPath,
		}, remoteFileDefaultTimeoutSeconds)
		if workerErr != nil {
			writeJSON(w, status, workerErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"hostname":     snapshot.Hostname,
			"current_path": firstText(cleanText(response["current_path"]), requestedPath),
			"entries":      arrayOrEmpty(response["entries"]),
		})
	}
}

func remoteFileUploadConflictsHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		targetPath := cleanText(body["target_path"])
		items := normalizeUploadManifestItems(body["items"])
		if targetPath == "" || len(items) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "target_path_and_items_required"})
			return
		}
		snapshot, operatorID, ok := requireRemoteFileContext(w, r, auth, hostname)
		if !ok {
			return
		}
		response, status, workerErr := callRemoteFileRPC(r.Context(), auth, snapshot, map[string]any{
			"action":       "upload_conflicts",
			"hostname":     snapshot.Hostname,
			"agent_id":     snapshot.AgentID,
			"requested_by": operatorID,
			"target_path":  targetPath,
			"items":        items,
		}, remoteFileDefaultTimeoutSeconds)
		if workerErr != nil {
			if cleanText(workerErr["error"]) == "agent_update_required" {
				writeJSON(w, http.StatusOK, map[string]any{
					"ok":                   true,
					"hostname":             snapshot.Hostname,
					"target_path":          targetPath,
					"conflicts":            []any{},
					"capability_supported": false,
					"message":              firstText(cleanText(workerErr["message"]), ""),
				})
				return
			}
			writeJSON(w, status, workerErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"hostname":    snapshot.Hostname,
			"target_path": firstText(cleanText(response["target_path"]), targetPath),
			"conflicts":   arrayOrEmpty(response["conflicts"]),
		})
	}
}

func remoteFileReadTextHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		requestedPath := cleanText(r.URL.Query().Get("path"))
		if requestedPath == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "path_required"})
			return
		}
		snapshot, operatorID, ok := requireRemoteFileContext(w, r, auth, hostname)
		if !ok {
			return
		}
		response, status, workerErr := callRemoteFileRPC(r.Context(), auth, snapshot, map[string]any{
			"action":       "read_text",
			"hostname":     snapshot.Hostname,
			"agent_id":     snapshot.AgentID,
			"requested_by": operatorID,
			"path":         requestedPath,
		}, remoteFileDefaultTimeoutSeconds)
		if workerErr != nil {
			writeJSON(w, status, workerErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"hostname":    snapshot.Hostname,
			"path":        firstText(cleanText(response["path"]), requestedPath),
			"content":     firstText(cleanText(response["content"]), ""),
			"encoding":    firstText(cleanText(response["encoding"]), "utf-8"),
			"line_ending": firstText(cleanText(response["line_ending"]), "lf"),
			"size_bytes":  coerceInt64(response["size_bytes"]),
			"modified_at": coerceInt64(response["modified_at"]),
			"entry":       objectOrNil(response["entry"]),
		})
	}
}

func remoteFileWriteTextHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		itemPath := cleanText(body["path"])
		if itemPath == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "path_required"})
			return
		}
		snapshot, operatorID, ok := requireRemoteFileContext(w, r, auth, hostname)
		if !ok {
			return
		}
		response, status, workerErr := callRemoteFileRPC(r.Context(), auth, snapshot, map[string]any{
			"action":       "write_text",
			"hostname":     snapshot.Hostname,
			"agent_id":     snapshot.AgentID,
			"requested_by": operatorID,
			"path":         itemPath,
			"content":      body["content"],
			"encoding":     body["encoding"],
			"line_ending":  body["line_ending"],
		}, remoteFileDefaultTimeoutSeconds)
		if workerErr != nil {
			writeJSON(w, status, workerErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"path":        firstText(cleanText(response["path"]), itemPath),
			"encoding":    firstText(cleanText(response["encoding"]), "utf-8"),
			"line_ending": firstText(cleanText(response["line_ending"]), "lf"),
			"size_bytes":  coerceInt64(response["size_bytes"]),
			"modified_at": coerceInt64(response["modified_at"]),
			"entry":       objectOrNil(response["entry"]),
		})
	}
}

func remoteFileMkdirHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		body, ok := decodeRemoteFileJSON(w, r, 1<<20)
		if !ok {
			return
		}
		parentPath := cleanText(body["path"])
		name := cleanText(body["name"])
		if parentPath == "" || name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "path_and_name_required"})
			return
		}
		remoteFileMutation(w, r, auth, hostname, "mkdir", map[string]any{"path": parentPath, "name": name}, remoteFileDefaultTimeoutSeconds, "entry")
	}
}

func remoteFileRenameHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		body, ok := decodeRemoteFileJSON(w, r, 1<<20)
		if !ok {
			return
		}
		itemPath := cleanText(body["path"])
		newName := cleanText(body["new_name"])
		if itemPath == "" || newName == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "path_and_new_name_required"})
			return
		}
		remoteFileMutation(w, r, auth, hostname, "rename", map[string]any{"path": itemPath, "new_name": newName}, remoteFileDefaultTimeoutSeconds, "entry")
	}
}

func remoteFileMoveHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		body, ok := decodeRemoteFileJSON(w, r, 2<<20)
		if !ok {
			return
		}
		destinationPath := cleanText(body["destination_path"])
		selections := normalizeTransferEntries(firstNonEmpty(body["paths"], body["items"], body["path"]))
		if destinationPath == "" || len(selections) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "paths_and_destination_required"})
			return
		}
		remoteFileMutation(w, r, auth, hostname, "move", map[string]any{"paths": selections, "destination_path": destinationPath}, 120.0, "moved")
	}
}

func remoteFileDeleteHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		body, ok := decodeRemoteFileJSON(w, r, 2<<20)
		if !ok {
			return
		}
		selections := normalizeTransferEntries(firstNonEmpty(body["paths"], body["items"], body["path"]))
		if len(selections) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "paths_required"})
			return
		}
		remoteFileMutation(w, r, auth, hostname, "delete", map[string]any{"paths": selections}, 120.0, "deleted")
	}
}

func remoteFilePasteHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		body, ok := decodeRemoteFileJSON(w, r, 2<<20)
		if !ok {
			return
		}
		destinationPath := cleanText(body["destination_path"])
		selections := normalizeTransferEntries(firstNonEmpty(body["paths"], body["items"], body["path"]))
		operation := strings.ToLower(cleanText(body["operation"]))
		if operation != "copy" && operation != "cut" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "operation_required"})
			return
		}
		if destinationPath == "" || len(selections) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "paths_and_destination_required"})
			return
		}
		remoteFileMutation(w, r, auth, hostname, "paste", map[string]any{"operation": operation, "paths": selections, "destination_path": destinationPath}, 300.0, "pasted")
	}
}

func remoteFileMutation(w http.ResponseWriter, r *http.Request, auth *authService, hostname string, action string, payload map[string]any, timeoutSeconds float64, resultKey string) {
	snapshot, operatorID, ok := requireRemoteFileContext(w, r, auth, hostname)
	if !ok {
		return
	}
	payload["action"] = action
	payload["hostname"] = snapshot.Hostname
	payload["agent_id"] = snapshot.AgentID
	payload["requested_by"] = operatorID
	response, status, workerErr := callRemoteFileRPC(r.Context(), auth, snapshot, payload, timeoutSeconds)
	if workerErr != nil {
		writeJSON(w, status, workerErr)
		return
	}
	result := map[string]any{"ok": true}
	if resultKey != "" {
		switch resultKey {
		case "entry":
			result[resultKey] = objectOrNil(response[resultKey])
		default:
			result[resultKey] = arrayOrEmpty(response[resultKey])
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeRemoteFileJSON(w http.ResponseWriter, r *http.Request, limit int64) (map[string]any, bool) {
	var body map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return nil, false
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, true
}

func requireRemoteFileContext(w http.ResponseWriter, r *http.Request, auth *authService, hostname string) (deviceProcessContext, string, bool) {
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
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "remote_files_unavailable"})
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
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "agent_unavailable"})
		return deviceProcessContext{}, "", false
	}
	workerCtx, workerCancel := context.WithTimeout(r.Context(), 5*time.Second)
	registered := workerHostServiceRegistered(workerCtx, auth, snapshot.Route, snapshot.Hostname, "system")
	workerCancel()
	if !registered {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "agent_unavailable"})
		return deviceProcessContext{}, "", false
	}
	return snapshot, firstText(cleanText(profile.Username), "unknown"), true
}

func callRemoteFileRPC(ctx context.Context, auth *authService, snapshot deviceProcessContext, payload map[string]any, timeoutSeconds float64) (map[string]any, int, map[string]any) {
	seconds := remoteFileTimeoutSeconds(timeoutSeconds)
	response, status, workerErr := callWorkerHostServiceEvent(ctx, auth, snapshot.Route, map[string]any{
		"hostname":        snapshot.Hostname,
		"service_mode":    "system",
		"event_name":      "file_management_request",
		"timeout_seconds": seconds,
		"payload":         payload,
	}, time.Duration(seconds+1.0)*time.Second)
	if workerErr != nil {
		return nil, status, remoteFileErrorPayload(status, workerErr)
	}
	return response, http.StatusOK, nil
}

func remoteFileErrorPayload(status int, payload map[string]any) map[string]any {
	errorCode := cleanText(payload["error"])
	message := cleanText(payload["message"])
	result := map[string]any{}
	for key, value := range payload {
		result[key] = value
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
			message = "The device did not answer the file request in time."
		}
		if message != "" {
			result["message"] = message
		}
	}
	return result
}

func remoteFileTimeoutSeconds(value float64) float64 {
	if value <= 0 {
		return remoteFileDefaultTimeoutSeconds
	}
	if value < 1 {
		return 1
	}
	if value > 300 {
		return 300
	}
	return value
}

func normalizeUploadManifestItems(value any) []map[string]any {
	rawItems, ok := value.([]any)
	if !ok {
		return []map[string]any{}
	}
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := sanitizeUploadName(firstText(cleanText(row["name"]), cleanText(row["filename"])))
		if name == "" {
			continue
		}
		clientKey := firstText(cleanText(row["client_key"]), name)
		relativePath := sanitizeRelativeUploadPath(row["relative_path"])
		if relativePath == "" {
			relativePath = name
		}
		item := map[string]any{
			"name":          name,
			"client_key":    clientKey,
			"relative_path": relativePath,
			"kind":          firstText(cleanText(row["kind"]), "file"),
			"size":          coerceInt64(row["size"]),
			"last_modified": coerceInt64(firstNonEmpty(row["last_modified"], row["modified_at"])),
		}
		if value := cleanText(row["mime_type"]); value != "" {
			item["mime_type"] = value
		}
		items = append(items, item)
	}
	return items
}

func normalizeTransferEntries(value any) []map[string]any {
	rawItems := []any{}
	switch typed := value.(type) {
	case nil:
		return []map[string]any{}
	case string:
		rawItems = []any{typed}
	case []any:
		rawItems = typed
	case []string:
		for _, item := range typed {
			rawItems = append(rawItems, item)
		}
	case map[string]any:
		rawItems = []any{typed}
	default:
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		switch typed := raw.(type) {
		case string:
			itemPath := cleanText(typed)
			if itemPath == "" {
				continue
			}
			result = append(result, map[string]any{
				"path": itemPath,
				"name": filepath.Base(strings.TrimRight(strings.ReplaceAll(itemPath, "\\", "/"), "/")),
				"kind": "file",
			})
		case map[string]any:
			itemPath := cleanText(typed["path"])
			if itemPath == "" {
				continue
			}
			name := firstText(cleanText(typed["name"]), filepath.Base(strings.TrimRight(strings.ReplaceAll(itemPath, "\\", "/"), "/")))
			kind := firstText(cleanText(typed["kind"]), cleanText(typed["type"]), "file")
			result = append(result, map[string]any{
				"path": itemPath,
				"name": name,
				"kind": kind,
			})
		}
	}
	return result
}

func sanitizeUploadName(value string) string {
	name := cleanText(value)
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.Trim(strings.TrimSpace(filepath.Base(name)), ". ")
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

func sanitizeRelativeUploadPath(value any) string {
	text := cleanText(value)
	text = strings.ReplaceAll(text, "\\", "/")
	parts := strings.Split(text, "/")
	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), ". ")
		if part == "" || part == "." || part == ".." {
			continue
		}
		cleanParts = append(cleanParts, part)
	}
	if len(cleanParts) == 0 {
		return ""
	}
	return strings.Join(cleanParts, "/")
}

func arrayOrEmpty(value any) []any {
	if items, ok := value.([]any); ok {
		return items
	}
	return []any{}
}

func objectOrNil(value any) any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return nil
}
