package main

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const remoteFileDefaultTimeoutSeconds = 30.0
const remoteFileWorkerJSONResponseMaxBytes = 64 << 20

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
		case "upload":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteFileUploadHandler(auth)(w, r, hostname)
		case "download":
			if r.Method != http.MethodPost {
				writeMethodNotAllowed(w, "POST")
				return
			}
			remoteFileDownloadHandler(auth)(w, r, hostname)
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
			if transferID, action, ok := splitRemoteFileTransferSuffix(suffix); ok {
				switch action {
				case "status":
					if r.Method != http.MethodGet {
						writeMethodNotAllowed(w, "GET")
						return
					}
					remoteFileTransferStatusHandler(auth)(w, r, hostname, transferID)
				case "cancel":
					if r.Method != http.MethodPost {
						writeMethodNotAllowed(w, "POST")
						return
					}
					remoteFileTransferCancelHandler(auth)(w, r, hostname, transferID)
				case "content":
					if r.Method != http.MethodGet {
						writeMethodNotAllowed(w, "GET")
						return
					}
					remoteFileTransferContentHandler(auth)(w, r, hostname, transferID)
				default:
					proxyFallbackOrMethodNotAllowed(w, r, fallback, "GET, POST")
				}
				return
			}
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

func splitRemoteFileTransferSuffix(suffix string) (string, string, bool) {
	parts := strings.Split(suffix, "/")
	if len(parts) != 3 || parts[0] != "transfer" {
		return "", "", false
	}
	transferID := cleanText(parts[1])
	action := cleanText(parts[2])
	if transferID == "" || action == "" {
		return "", "", false
	}
	return transferID, action, true
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
		response, status, workerErr := remoteFileUploadConflictsPayload(r.Context(), auth, snapshot, operatorID, targetPath, items)
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

func remoteFileUploadHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		snapshot, operatorID, ok := requireRemoteFileContext(w, r, auth, hostname)
		if !ok {
			return
		}
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_multipart"})
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		targetPath := cleanText(r.FormValue("target_path"))
		if targetPath == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "target_path_required"})
			return
		}
		files := remoteFileUploadFiles(r)
		manifestItems := uploadManifestFromMultipartForm(r.FormValue("manifest"), files)
		if len(manifestItems) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "upload_files_required"})
			return
		}
		manifestOnlyEmptyUpload := remoteFileManifestOnlyEmptyUpload(files, manifestItems)
		if len(files) == 0 && !manifestOnlyEmptyUpload {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "upload_files_required"})
			return
		}
		if len(files) > 0 && len(manifestItems) != len(files) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "upload_manifest_mismatch"})
			return
		}
		deviceGUID := remoteFileTransferDeviceGUID(snapshot)
		if deviceGUID == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "device_guid_unavailable",
				"message": "File transfer requires a device GUID. Reconnect the Agent or refresh device inventory before uploading.",
			})
			return
		}

		conflictResolutions := normalizeConflictResolutionMap(r.FormValue("conflict_resolutions"))
		conflictPayload, status, workerErr := remoteFileUploadConflictsPayload(r.Context(), auth, snapshot, operatorID, targetPath, manifestItems)
		conflicts := []any{}
		legacyConflictSupport := false
		if workerErr != nil {
			if cleanText(workerErr["error"]) == "agent_update_required" {
				legacyConflictSupport = true
			} else {
				writeJSON(w, status, workerErr)
				return
			}
		} else {
			conflicts = arrayOrEmpty(conflictPayload["conflicts"])
		}
		unresolved := make([]any, 0)
		for _, conflict := range conflicts {
			row, _ := conflict.(map[string]any)
			clientKey := cleanText(row["client_key"])
			if clientKey == "" {
				continue
			}
			resolution := conflictResolutions[clientKey]
			if resolution != "replace" && resolution != "skip" {
				unresolved = append(unresolved, conflict)
			}
		}
		if len(unresolved) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":       "upload_conflicts",
				"message":     "The destination already contains one or more items with the same name.",
				"target_path": firstText(cleanText(conflictPayload["target_path"]), targetPath),
				"conflicts":   unresolved,
			})
			return
		}
		if legacyConflictSupport && len(conflictResolutions) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "agent_update_required",
				"message": "The device agent needs to be updated before duplicate upload resolution is available.",
			})
			return
		}

		filesToUpload := make([]*multipart.FileHeader, 0, len(files))
		manifestToUpload := make([]map[string]any, 0, len(manifestItems))
		overwriteKeys := []string{}
		skippedCount := 0
		for index, manifestRow := range manifestItems {
			var file *multipart.FileHeader
			if index < len(files) {
				file = files[index]
			}
			fileNameFallback := ""
			if file != nil {
				fileNameFallback = file.Filename
			}
			filename := sanitizeUploadName(firstText(cleanText(manifestRow["name"]), fileNameFallback))
			clientKey := firstText(cleanText(manifestRow["client_key"]), filename)
			if filename == "" || clientKey == "" {
				continue
			}
			switch conflictResolutions[clientKey] {
			case "skip":
				skippedCount++
				continue
			case "replace":
				overwriteKeys = append(overwriteKeys, clientKey)
			}
			if file != nil {
				filesToUpload = append(filesToUpload, file)
			}
			manifestToUpload = append(manifestToUpload, manifestRow)
		}
		if len(manifestToUpload) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":            true,
				"status":        "skipped",
				"target_path":   targetPath,
				"skipped_count": skippedCount,
			})
			return
		}
		workerURLs := remoteOpsWorkerURLs(r, snapshot.Route)
		session, workerStatus, workerErr := remoteFilePostWorkerUpload(r.Context(), auth, snapshot.Route, remoteFileUploadWorkerRequest{
			Hostname:        snapshot.Hostname,
			DeviceGUID:      deviceGUID,
			AgentID:         snapshot.AgentID,
			OperatorID:      operatorID,
			TargetPath:      targetPath,
			TransferBaseURL: cleanText(workerURLs["base"]),
			Files:           filesToUpload,
			ManifestItems:   manifestToUpload,
			OverwriteKeys:   overwriteKeys,
		})
		if workerErr != nil {
			writeJSON(w, workerStatus, workerErr)
			return
		}
		writeJSON(w, workerStatus, session)
	}
}

func remoteFileDownloadHandler(auth *authService) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string) {
		body, ok := decodeRemoteFileJSON(w, r, 2<<20)
		if !ok {
			return
		}
		selections := normalizeTransferEntries(firstNonEmpty(body["items"], body["paths"], body["path"]))
		if len(selections) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "paths_required"})
			return
		}
		snapshot, operatorID, ok := requireRemoteFileContext(w, r, auth, hostname)
		if !ok {
			return
		}
		archiveRequired := len(selections) != 1
		if !archiveRequired {
			kind := strings.ToLower(cleanText(selections[0]["kind"]))
			archiveRequired = kind == "directory"
		}
		deviceGUID := remoteFileTransferDeviceGUID(snapshot)
		if deviceGUID == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "device_guid_unavailable",
				"message": "File transfer requires a device GUID. Reconnect the Agent or refresh device inventory before downloading.",
			})
			return
		}
		archiveName := guessRemoteFileDownloadName(snapshot.Hostname, selections, archiveRequired)
		workerURLs := remoteOpsWorkerURLs(r, snapshot.Route)
		session, workerStatus, workerErr := remoteFilePostWorkerJSON(r.Context(), auth, snapshot.Route, "/remote-files/transfers/download", map[string]any{
			"hostname":          snapshot.Hostname,
			"device_guid":       deviceGUID,
			"agent_id":          snapshot.AgentID,
			"operator_id":       operatorID,
			"items":             selections,
			"archive_name":      archiveName,
			"archive_required":  archiveRequired,
			"transfer_base_url": cleanText(workerURLs["base"]),
		}, 30*time.Second)
		if workerErr != nil {
			writeJSON(w, workerStatus, workerErr)
			return
		}
		writeJSON(w, workerStatus, session)
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

func remoteFileTransferStatusHandler(auth *authService) func(http.ResponseWriter, *http.Request, string, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string, transferID string) {
		snapshot, _, ok := loadRemoteFileContext(w, r, auth, hostname, false)
		if !ok {
			return
		}
		payload, status, workerErr := remoteFileGetWorkerJSON(r.Context(), auth, snapshot.Route, "/remote-files/transfers/"+transferID+"/status", 15*time.Second)
		if workerErr != nil {
			writeJSON(w, status, workerErr)
			return
		}
		if !remoteFileTransferMatchesHost(payload, snapshot.Hostname) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "transfer_not_found"})
			return
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func remoteFileTransferCancelHandler(auth *authService) func(http.ResponseWriter, *http.Request, string, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string, transferID string) {
		snapshot, _, ok := loadRemoteFileContext(w, r, auth, hostname, false)
		if !ok {
			return
		}
		payload, status, workerErr := remoteFilePostWorkerJSON(r.Context(), auth, snapshot.Route, "/remote-files/transfers/"+transferID+"/cancel", map[string]any{}, 10*time.Second)
		if workerErr != nil {
			writeJSON(w, status, workerErr)
			return
		}
		if !remoteFileTransferMatchesHost(payload, snapshot.Hostname) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "transfer_not_found"})
			return
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func remoteFileTransferContentHandler(auth *authService) func(http.ResponseWriter, *http.Request, string, string) {
	return func(w http.ResponseWriter, r *http.Request, hostname string, transferID string) {
		snapshot, _, ok := loadRemoteFileContext(w, r, auth, hostname, false)
		if !ok {
			return
		}
		statusPayload, status, workerErr := remoteFileGetWorkerJSON(r.Context(), auth, snapshot.Route, "/remote-files/transfers/"+transferID+"/status", 15*time.Second)
		if workerErr != nil {
			writeJSON(w, status, workerErr)
			return
		}
		if !remoteFileTransferMatchesHost(statusPayload, snapshot.Hostname) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "transfer_not_found"})
			return
		}
		resp, status, workerErr := remoteFileWorkerContentResponse(r.Context(), auth, snapshot.Route, "/remote-files/transfers/"+transferID+"/content", 900*time.Second)
		if workerErr != nil {
			writeJSON(w, status, workerErr)
			return
		}
		defer resp.Body.Close()
		for _, header := range []string{"Content-Type", "Content-Length", "Content-Disposition"} {
			if value := resp.Header.Get(header); value != "" {
				w.Header().Set(header, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

func remoteFileUploadConflictsPayload(ctx context.Context, auth *authService, snapshot deviceProcessContext, operatorID string, targetPath string, items []map[string]any) (map[string]any, int, map[string]any) {
	response, status, workerErr := callRemoteFileRPC(ctx, auth, snapshot, map[string]any{
		"action":       "upload_conflicts",
		"hostname":     snapshot.Hostname,
		"agent_id":     snapshot.AgentID,
		"requested_by": operatorID,
		"target_path":  targetPath,
		"items":        items,
	}, remoteFileDefaultTimeoutSeconds)
	if workerErr != nil {
		return nil, status, workerErr
	}
	return map[string]any{
		"ok":          true,
		"hostname":    snapshot.Hostname,
		"target_path": firstText(cleanText(response["target_path"]), targetPath),
		"conflicts":   arrayOrEmpty(response["conflicts"]),
	}, http.StatusOK, nil
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

func remoteFileTransferDeviceGUID(snapshot deviceProcessContext) string {
	return firstText(normalizeCanonicalGUID(snapshot.GUID), guidFromAgentID(snapshot.AgentID))
}

func requireRemoteFileContext(w http.ResponseWriter, r *http.Request, auth *authService, hostname string) (deviceProcessContext, string, bool) {
	return loadRemoteFileContext(w, r, auth, hostname, true)
}

func loadRemoteFileContext(w http.ResponseWriter, r *http.Request, auth *authService, hostname string, requireSocket bool) (deviceProcessContext, string, bool) {
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
	if !requireSocket {
		return snapshot, firstText(cleanText(profile.Username), "unknown"), true
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

type remoteFileUploadWorkerRequest struct {
	Hostname        string
	DeviceGUID      string
	AgentID         string
	OperatorID      string
	TargetPath      string
	TransferBaseURL string
	Files           []*multipart.FileHeader
	ManifestItems   []map[string]any
	OverwriteKeys   []string
}

func remoteFilePostWorkerUpload(ctx context.Context, auth *authService, route *agentWorkerRoute, request remoteFileUploadWorkerRequest) (map[string]any, int, map[string]any) {
	if auth == nil || route == nil {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}
	}
	target := workerInternalURL(route, "/remote-files/transfers/upload")
	if target == "" {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}
	}
	body, contentType, contentLength, cleanup, err := remoteFileUploadMultipartBody(request)
	if err != nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "worker_request_failed", "message": err.Error()}
	}
	defer cleanup()

	requestCtx, cancel := context.WithTimeout(ctx, 900*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target, body)
	if err != nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "worker_request_failed", "message": err.Error()}
	}
	req.ContentLength = contentLength
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}
	}
	defer resp.Body.Close()
	return decodeRemoteFileWorkerJSONResponse(resp)
}

func remoteFileUploadMultipartBody(request remoteFileUploadWorkerRequest) (io.ReadCloser, string, int64, func(), error) {
	file, err := os.CreateTemp("", "borealis-remote-file-upload-*.multipart")
	if err != nil {
		return nil, "", 0, func() {}, err
	}
	cleanup := func() {
		name := file.Name()
		_ = file.Close()
		if name != "" {
			_ = os.Remove(name)
		}
	}
	writer := multipart.NewWriter(file)
	contentType := writer.FormDataContentType()
	writeErr := writeRemoteFileUploadMultipart(writer, request)
	closeErr := writer.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		cleanup()
		return nil, "", 0, func() {}, writeErr
	}
	stat, err := file.Stat()
	if err != nil {
		cleanup()
		return nil, "", 0, func() {}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, "", 0, func() {}, err
	}
	return file, contentType, stat.Size(), cleanup, nil
}

func writeRemoteFileUploadMultipart(writer *multipart.Writer, request remoteFileUploadWorkerRequest) error {
	fields := map[string]string{
		"hostname":          request.Hostname,
		"device_guid":       request.DeviceGUID,
		"agent_id":          request.AgentID,
		"operator_id":       firstText(request.OperatorID, "unknown"),
		"target_path":       request.TargetPath,
		"transfer_base_url": request.TransferBaseURL,
		"manifest":          mustCompactJSON(request.ManifestItems),
		"overwrite_keys":    mustCompactJSON(request.OverwriteKeys),
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return err
		}
	}
	for _, fileHeader := range request.Files {
		if fileHeader == nil {
			continue
		}
		source, err := fileHeader.Open()
		if err != nil {
			return err
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="files"; filename="`+escapeMultipartFilename(sanitizeUploadName(fileHeader.Filename))+`"`)
		header.Set("Content-Type", firstText(fileHeader.Header.Get("Content-Type"), "application/octet-stream"))
		part, err := writer.CreatePart(header)
		if err != nil {
			_ = source.Close()
			return err
		}
		if _, err = io.Copy(part, source); err != nil {
			_ = source.Close()
			return err
		}
		if err = source.Close(); err != nil {
			return err
		}
	}
	return nil
}

func remoteFilePostWorkerJSON(ctx context.Context, auth *authService, route *agentWorkerRoute, path string, payload map[string]any, timeout time.Duration) (map[string]any, int, map[string]any) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, http.StatusBadRequest, map[string]any{"error": "invalid_request", "message": "The worker request could not be encoded."}
	}
	return remoteFileWorkerJSONRequest(ctx, auth, route, http.MethodPost, path, body, timeout)
}

func remoteFileGetWorkerJSON(ctx context.Context, auth *authService, route *agentWorkerRoute, path string, timeout time.Duration) (map[string]any, int, map[string]any) {
	return remoteFileWorkerJSONRequest(ctx, auth, route, http.MethodGet, path, nil, timeout)
}

func remoteFileWorkerJSONRequest(ctx context.Context, auth *authService, route *agentWorkerRoute, method string, path string, body []byte, timeout time.Duration) (map[string]any, int, map[string]any) {
	if auth == nil || route == nil {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}
	}
	target := workerInternalURL(route, path)
	if target == "" {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(requestCtx, method, target, reader)
	if err != nil {
		return nil, http.StatusBadGateway, map[string]any{"error": "worker_request_failed", "message": err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}
	}
	defer resp.Body.Close()
	return decodeRemoteFileWorkerJSONResponse(resp)
}

func remoteFileWorkerContentResponse(ctx context.Context, auth *authService, route *agentWorkerRoute, path string, timeout time.Duration) (*http.Response, int, map[string]any) {
	if auth == nil || route == nil {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}
	}
	target := workerInternalURL(route, path)
	if target == "" {
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}
	}
	if timeout <= 0 {
		timeout = 900 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		cancel()
		return nil, http.StatusBadGateway, map[string]any{"error": "worker_request_failed", "message": err.Error()}
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set(internalTokenHeader, goInternalToken(auth.verifier.secret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return nil, http.StatusServiceUnavailable, map[string]any{"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}
	}
	if resp.StatusCode >= 400 {
		defer cancel()
		defer resp.Body.Close()
		payload, status, workerErr := decodeRemoteFileWorkerJSONResponse(resp)
		if workerErr != nil {
			return nil, status, workerErr
		}
		return nil, status, payload
	}
	resp.Body = &cancelOnCloseReadCloser{ReadCloser: resp.Body, cancel: cancel}
	return resp, resp.StatusCode, nil
}

func decodeRemoteFileWorkerJSONResponse(resp *http.Response) (map[string]any, int, map[string]any) {
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, remoteFileWorkerJSONResponseMaxBytes)).Decode(&payload); err != nil {
		if resp.StatusCode >= 400 {
			return nil, resp.StatusCode, map[string]any{"error": "site_worker_error"}
		}
		return nil, http.StatusBadGateway, map[string]any{"error": "invalid_worker_response", "message": "The site-worker returned an invalid response."}
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if resp.StatusCode >= 400 {
		if cleanText(payload["error"]) == "" {
			payload["error"] = "site_worker_error"
		}
		return nil, resp.StatusCode, payload
	}
	return payload, resp.StatusCode, nil
}

type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnCloseReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
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
	if strings.EqualFold(errorCode, "invalid_request") && strings.Contains(message, "Unsupported file-management action") {
		result["error"] = "agent_update_required"
		result["message"] = "The device agent needs to be updated before this File Management capability is available."
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

func remoteFileUploadFiles(r *http.Request) []*multipart.FileHeader {
	if r == nil || r.MultipartForm == nil || r.MultipartForm.File == nil {
		return nil
	}
	files := r.MultipartForm.File["files"]
	result := make([]*multipart.FileHeader, 0, len(files))
	for _, file := range files {
		if file == nil || sanitizeUploadName(file.Filename) == "" {
			continue
		}
		result = append(result, file)
	}
	return result
}

func uploadManifestFromMultipartForm(value string, files []*multipart.FileHeader) []map[string]any {
	raw := strings.TrimSpace(value)
	if raw != "" {
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			if items := normalizeUploadManifestItems(parsed); len(items) > 0 {
				return items
			}
		}
	}
	fallbackRows := make([]any, 0, len(files))
	for _, file := range files {
		if file == nil {
			continue
		}
		filename := sanitizeUploadName(file.Filename)
		if filename == "" {
			continue
		}
		fallbackRows = append(fallbackRows, map[string]any{
			"client_key":    filename,
			"name":          filename,
			"relative_path": filename,
			"size_bytes":    file.Size,
			"modified_at":   0,
		})
	}
	return normalizeUploadManifestItems(fallbackRows)
}

func remoteFileManifestOnlyEmptyUpload(files []*multipart.FileHeader, manifestItems []map[string]any) bool {
	if len(files) != 0 || len(manifestItems) == 0 {
		return false
	}
	for _, row := range manifestItems {
		if sanitizeUploadName(cleanText(row["name"])) == "" || cleanText(row["relative_path"]) == "" {
			return false
		}
		if coerceInt64(row["size_bytes"]) != 0 {
			return false
		}
	}
	return true
}

func normalizeConflictResolutionMap(value string) map[string]string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return map[string]string{}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return map[string]string{}
	}
	result := map[string]string{}
	for key, value := range parsed {
		name := cleanText(key)
		choice := strings.ToLower(cleanText(value))
		if name == "" || (choice != "replace" && choice != "skip") {
			continue
		}
		result[name] = choice
	}
	return result
}

func guessRemoteFileDownloadName(hostname string, selections []map[string]any, archiveRequired bool) string {
	nowLabel := time.Now().Format("20060102-150405")
	if archiveRequired {
		normalizedHost := firstText(cleanText(hostname), "device")
		return normalizedHost + "-files-" + nowLabel + ".zip"
	}
	if len(selections) == 0 {
		return "download-" + nowLabel + ".bin"
	}
	only := selections[0]
	name := sanitizeUploadName(firstText(cleanText(only["name"]), filepath.Base(strings.TrimRight(strings.ReplaceAll(cleanText(only["path"]), "\\", "/"), "/"))))
	if name == "" {
		return "download-" + nowLabel + ".bin"
	}
	return name
}

func remoteFileTransferMatchesHost(payload map[string]any, hostname string) bool {
	return strings.EqualFold(cleanText(payload["hostname"]), cleanText(hostname))
}

func mustCompactJSON(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(payload)
}

func escapeMultipartFilename(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	if value == "" {
		return "upload.bin"
	}
	return value
}

func normalizeUploadManifestItems(value any) []map[string]any {
	rawItems := []any{}
	switch typed := value.(type) {
	case []any:
		rawItems = typed
	case []map[string]any:
		for _, item := range typed {
			rawItems = append(rawItems, item)
		}
	case map[string]any:
		rawItems = []any{typed}
	default:
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
		sizeBytes := coerceInt64(firstNonEmpty(row["size"], row["size_bytes"]))
		item := map[string]any{
			"name":          name,
			"client_key":    clientKey,
			"relative_path": relativePath,
			"kind":          firstText(cleanText(row["kind"]), "file"),
			"size":          sizeBytes,
			"size_bytes":    sizeBytes,
			"last_modified": coerceInt64(firstNonEmpty(row["last_modified"], row["modified_at"])),
			"modified_at":   coerceInt64(firstNonEmpty(row["modified_at"], row["last_modified"])),
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
	name = strings.ReplaceAll(name, "\x00", "")
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
