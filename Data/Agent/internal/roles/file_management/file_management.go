package filemanagement

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
)

const (
	textEditorMaxBytes       = 1024 * 1024
	transferProgressInterval = 2 * time.Second
	transferControlInterval  = time.Second
)

type Manager struct {
	authClient      *auth.Client
	hostname        string
	httpClient      *http.Client
	tempRoot        string
	activeTransfers atomic.Int64
	mu              sync.Mutex
	listenerReady   bool
	lastError       string
	lastTransferAt  int64
}

type RoleHealth struct {
	Status     string
	StatusCode string
	Detail     string
	Details    map[string]any
}

type fmError struct {
	Code    string
	Message string
}

func (e fmError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Code
}

func New(authClient *auth.Client, hostname string) *Manager {
	tempRoot := filepath.Join(os.TempDir(), "Borealis", "file_management")
	_ = os.MkdirAll(tempRoot, 0o700)
	httpClient := &http.Client{Timeout: 15 * time.Minute}
	if authClient != nil {
		httpClient = authClient.HTTPClientWithTimeout(15 * time.Minute)
	}
	return &Manager{
		authClient:    authClient,
		hostname:      strings.TrimSpace(hostname),
		httpClient:    httpClient,
		tempRoot:      tempRoot,
		listenerReady: true,
	}
}

func (m *Manager) HandleRequest(ctx context.Context, payload any) (any, error) {
	body, ok := payload.(map[string]any)
	if !ok {
		return errorResponse("invalid_request", "Expected an object payload."), nil
	}
	if !m.matchesTarget(body) {
		return errorResponse("not_found", "The file-management request targeted another device."), nil
	}
	action := strings.ToLower(cleanText(body["action"]))
	response, err := m.handleAction(ctx, action, body)
	if err != nil {
		ferr := normalizeError(err)
		m.setLastError(ferr.Message)
		return errorResponse(ferr.Code, ferr.Message), nil
	}
	m.setLastError("")
	return response, nil
}

func (m *Manager) Health() RoleHealth {
	m.mu.Lock()
	lastError := m.lastError
	lastTransferAt := m.lastTransferAt
	ready := m.listenerReady
	m.mu.Unlock()
	details := map[string]any{
		"running_status":   "Ready",
		"listener_state":   "Registered",
		"active_transfers": strconv.FormatInt(maxInt64(0, m.activeTransfers.Load()), 10),
		"last_transfer_at": strconv.FormatInt(lastTransferAt, 10),
		"runtime":          "go",
	}
	if !ready {
		return RoleHealth{
			Status:     "unhealthy",
			StatusCode: "unhealthy",
			Detail:     "File-management listener is not registered.",
			Details:    details,
		}
	}
	if strings.TrimSpace(lastError) != "" {
		details["last_error"] = lastError
		return RoleHealth{
			Status:     "recovering",
			StatusCode: "recovering",
			Detail:     lastError,
			Details:    details,
		}
	}
	return RoleHealth{
		Status:     "healthy",
		StatusCode: "healthy",
		Detail:     "Remote file-management listeners are ready.",
		Details:    details,
	}
}

func (m *Manager) handleAction(ctx context.Context, action string, payload map[string]any) (any, error) {
	switch action {
	case "roots":
		return rootsPayload()
	case "children":
		currentPath, err := normalizeRequestedPath(payload["path"])
		if err != nil {
			return nil, err
		}
		entries, err := listChildren(currentPath)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "current_path": currentPath, "entries": entries}, nil
	case "upload_conflicts":
		return inspectUploadConflicts(payload["target_path"], payload["items"])
	case "read_text":
		textPayload, err := readTextFile(payload["path"])
		if err != nil {
			return nil, err
		}
		textPayload["ok"] = true
		return textPayload, nil
	case "write_text":
		textPayload, err := writeTextFile(payload["path"], payload["content"], payload["encoding"], payload["line_ending"])
		if err != nil {
			return nil, err
		}
		textPayload["ok"] = true
		return textPayload, nil
	case "mkdir":
		parentPath, err := ensureDirectory(payload["path"])
		if err != nil {
			return nil, err
		}
		name, err := validateChildName(payload["name"])
		if err != nil {
			return nil, err
		}
		destination := filepath.Join(parentPath, name)
		if pathExists(destination) {
			return nil, newError("conflict", fmt.Sprintf("'%s' already exists.", destination))
		}
		if err := os.Mkdir(destination, 0o755); err != nil {
			return nil, mapOSError(err, destination)
		}
		entry, err := entryFromPath(destination, parentPath, "")
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "entry": entry}, nil
	case "rename":
		return renameItem(payload)
	case "move":
		return moveItems(payload)
	case "paste":
		return pasteItems(payload)
	case "delete":
		return deleteItems(payload)
	case "upload_start", "download_start":
		transferID := cleanText(payload["transfer_id"])
		if transferID == "" {
			return nil, newError("invalid_request", "Transfer metadata is missing transfer_id.")
		}
		go m.runTransfer(context.Background(), cloneMap(payload))
		return map[string]any{"ok": true, "status": "accepted", "transfer_id": transferID}, nil
	default:
		return nil, newError("invalid_request", fmt.Sprintf("Unsupported file-management action '%s'.", action))
	}
}

func (m *Manager) runTransfer(ctx context.Context, payload map[string]any) {
	transferID := cleanText(payload["transfer_id"])
	if transferID == "" {
		transferID = "unknown"
	}
	m.activeTransfers.Add(1)
	m.mu.Lock()
	m.lastTransferAt = time.Now().Unix()
	m.mu.Unlock()
	defer m.activeTransfers.Add(-1)
	action := strings.ToLower(cleanText(payload["action"]))
	var err error
	switch action {
	case "upload_start":
		err = m.uploadTransfer(ctx, payload)
	case "download_start":
		err = m.downloadTransfer(ctx, payload)
	default:
		err = newError("invalid_request", fmt.Sprintf("Unsupported transfer action '%s'.", action))
	}
	if err == nil {
		m.setLastError("")
		return
	}
	ferr := normalizeError(err)
	m.setLastError(ferr.Message)
	status := "failed"
	if ferr.Code == "transfer_canceled" {
		status = "canceled"
	}
	_, _ = m.reportProgress(ctx, transferID, m.transferBaseURL(payload), map[string]any{"status": status, "error": ferr.Message})
}

func (m *Manager) matchesTarget(payload map[string]any) bool {
	targetHostname := strings.ToLower(cleanText(firstValue(payload, "hostname", "target_hostname")))
	if targetHostname != "" && targetHostname != strings.ToLower(strings.TrimSpace(m.hostname)) {
		return false
	}
	targetAgent := cleanText(payload["agent_id"])
	if targetAgent != "" && m.authClient != nil && targetAgent != m.authClient.AgentID() {
		return false
	}
	return true
}

func (m *Manager) setLastError(value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = strings.TrimSpace(value)
}

func rootsPayload() (map[string]any, error) {
	if runtime.GOOS == "windows" {
		roots := []map[string]any{}
		for letter := 'A'; letter <= 'Z'; letter++ {
			drive := string(letter) + `:\`
			if !pathExists(drive) {
				continue
			}
			entry, err := entryFromPath(drive, "", drive)
			if err == nil {
				roots = append(roots, entry)
			}
		}
		return map[string]any{
			"ok":            true,
			"platform":      "windows",
			"context_label": "SYSTEM",
			"current_path":  "",
			"entries":       sortEntries(roots),
		}, nil
	}
	entries, err := listChildren("/")
	if err != nil {
		return nil, err
	}
	platform := runtime.GOOS
	if platform == "" {
		platform = "unknown"
	}
	return map[string]any{
		"ok":            true,
		"platform":      platform,
		"context_label": "root",
		"current_path":  "/",
		"entries":       entries,
	}, nil
}

func listChildren(pathValue string) ([]map[string]any, error) {
	parent, err := ensureDirectory(pathValue)
	if err != nil {
		return nil, err
	}
	rows, err := os.ReadDir(parent)
	if err != nil {
		return nil, mapOSError(err, parent)
	}
	entries := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		entry, err := entryFromPath(filepath.Join(parent, row.Name()), parent, "")
		if err == nil {
			entries = append(entries, entry)
		}
	}
	return sortEntries(entries), nil
}

func entryFromPath(pathValue string, parentPath string, forceName string) (map[string]any, error) {
	normalized, err := normalizeRequestedPath(pathValue)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(normalized)
	if err != nil {
		return nil, mapOSError(err, normalized)
	}
	mode := info.Mode()
	isSymlink := mode&os.ModeSymlink != 0
	isDir := info.IsDir()
	if isSymlink {
		if targetInfo, statErr := os.Stat(normalized); statErr == nil {
			isDir = targetInfo.IsDir()
		}
	}
	name := forceName
	if name == "" {
		name = entryName(normalized)
	}
	kind := "file"
	if isSymlink {
		kind = "symlink"
	} else if isDir {
		kind = "directory"
	}
	attributes := []string{}
	if strings.HasPrefix(name, ".") {
		attributes = append(attributes, "hidden")
	}
	if isSymlink {
		attributes = append(attributes, "symlink")
	}
	if isDir {
		attributes = append(attributes, "directory")
	}
	if mode.Perm()&0o222 == 0 {
		attributes = append(attributes, "read_only")
	}
	return map[string]any{
		"path":         normalized,
		"parent_path":  parentPath,
		"name":         name,
		"kind":         kind,
		"size_bytes":   sizeForEntry(info, isDir),
		"modified_at":  info.ModTime().Unix(),
		"attributes":   attributes,
		"has_children": isDir && !isSymlink,
		"is_hidden":    containsString(attributes, "hidden"),
	}, nil
}

func inspectUploadConflicts(pathValue any, items any) (map[string]any, error) {
	targetPath, err := ensureDirectory(pathValue)
	if err != nil {
		return nil, err
	}
	rows, err := normalizeUploadCandidateRows(items)
	if err != nil {
		return nil, err
	}
	conflicts := []map[string]any{}
	for _, row := range rows {
		relativePath := cleanText(row["relative_path"])
		destination := filepath.Join(append([]string{targetPath}, strings.Split(relativePath, "/")...)...)
		if !pathExists(destination) {
			continue
		}
		entry, err := entryFromPath(destination, parentPath(destination), "")
		if err != nil {
			return nil, err
		}
		conflicts = append(conflicts, map[string]any{
			"client_key":         cleanText(row["client_key"]),
			"name":               cleanText(row["name"]),
			"relative_path":      relativePath,
			"display_name":       displayName(relativePath, cleanText(row["name"])),
			"destination":        entry,
			"upload_size_bytes":  asInt64(row["size_bytes"]),
			"upload_modified_at": asInt64(row["modified_at"]),
			"replace_supported":  cleanText(entry["kind"]) != "directory",
		})
	}
	return map[string]any{"ok": true, "target_path": targetPath, "conflicts": conflicts}, nil
}

func readTextFile(pathValue any) (map[string]any, error) {
	sourcePath, err := normalizeRequestedPath(pathValue)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, mapOSError(err, sourcePath)
	}
	if info.IsDir() {
		return nil, newError("not_a_file", fmt.Sprintf("'%s' is not a file.", sourcePath))
	}
	if info.Size() > textEditorMaxBytes {
		return nil, newError("file_too_large", fmt.Sprintf("'%s' exceeds the lightweight editor limit of %d bytes.", sourcePath, textEditorMaxBytes))
	}
	payload, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, mapOSError(err, sourcePath)
	}
	if looksLikeBinaryBytes(payload) {
		return nil, newError("binary_not_supported", "Binary files cannot be opened in the lightweight text editor.")
	}
	content, encoding, err := decodeTextPayload(payload)
	if err != nil {
		return nil, err
	}
	if looksLikeBinaryText(content) {
		return nil, newError("binary_not_supported", "Binary files cannot be opened in the lightweight text editor.")
	}
	entry, err := entryFromPath(sourcePath, parentPath(sourcePath), "")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"path":        sourcePath,
		"content":     content,
		"encoding":    encoding,
		"line_ending": detectLineEnding(content),
		"size_bytes":  len(payload),
		"modified_at": entry["modified_at"],
		"entry":       entry,
	}, nil
}

func writeTextFile(pathValue any, contentValue any, encodingValue any, lineEndingValue any) (map[string]any, error) {
	destinationPath, err := normalizeRequestedPath(pathValue)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(destinationPath)
	if err != nil {
		return nil, mapOSError(err, destinationPath)
	}
	if info.IsDir() {
		return nil, newError("not_a_file", fmt.Sprintf("'%s' is not a file.", destinationPath))
	}
	encodingName := cleanText(encodingValue)
	if encodingName == "" {
		encodingName = "utf-8"
	}
	lineEnding, err := normalizeLineEnding(lineEndingValue)
	if err != nil {
		return nil, err
	}
	content := normalizeContentLineEndings(fmt.Sprint(contentValue), lineEnding)
	encoded, err := encodeTextPayload(content, encodingName)
	if err != nil {
		return nil, err
	}
	if len(encoded) > textEditorMaxBytes {
		return nil, newError("file_too_large", fmt.Sprintf("'%s' exceeds the lightweight editor limit of %d bytes.", destinationPath, textEditorMaxBytes))
	}
	handle, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return nil, mapOSError(err, destinationPath)
	}
	if _, err := handle.Write(encoded); err != nil {
		_ = handle.Close()
		return nil, mapOSError(err, destinationPath)
	}
	if err := handle.Close(); err != nil {
		return nil, mapOSError(err, destinationPath)
	}
	entry, err := entryFromPath(destinationPath, parentPath(destinationPath), "")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"path":        destinationPath,
		"encoding":    encodingName,
		"line_ending": lineEnding,
		"size_bytes":  len(encoded),
		"modified_at": entry["modified_at"],
		"entry":       entry,
	}, nil
}

func renameItem(payload map[string]any) (map[string]any, error) {
	sourcePath, err := normalizeRequestedPath(payload["path"])
	if err != nil {
		return nil, err
	}
	if !pathExists(sourcePath) {
		return nil, newError("path_not_found", fmt.Sprintf("'%s' does not exist.", sourcePath))
	}
	name, err := validateChildName(payload["new_name"])
	if err != nil {
		return nil, err
	}
	parent := parentPath(sourcePath)
	if parent == "" || samePath(parent, sourcePath) {
		return nil, newError("invalid_path", "Root paths cannot be renamed.")
	}
	destination := filepath.Join(parent, name)
	if pathExists(destination) {
		return nil, newError("conflict", fmt.Sprintf("'%s' already exists.", destination))
	}
	if err := os.Rename(sourcePath, destination); err != nil {
		return nil, mapOSError(err, sourcePath)
	}
	entry, err := entryFromPath(destination, parent, "")
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "entry": entry}, nil
}

func moveItems(payload map[string]any) (map[string]any, error) {
	selections, err := normalizeSelectionRows(payload["paths"])
	if err != nil {
		return nil, err
	}
	destination, err := ensureDirectory(payload["destination_path"])
	if err != nil {
		return nil, err
	}
	if len(selections) == 0 {
		return nil, newError("invalid_request", "At least one source path is required.")
	}
	moved := []map[string]any{}
	for _, row := range selections {
		source := cleanText(row["path"])
		if !pathExists(source) {
			return nil, newError("path_not_found", fmt.Sprintf("'%s' does not exist.", source))
		}
		finalPath := filepath.Join(destination, filepath.Base(strings.TrimRight(source, `\/`)))
		if samePath(source, finalPath) {
			continue
		}
		if destinationInsideSource(source, destination) {
			return nil, newError("invalid_path", "A folder cannot be moved into itself.")
		}
		if pathExists(finalPath) {
			return nil, newError("conflict", fmt.Sprintf("'%s' already exists.", finalPath))
		}
	}
	for _, row := range selections {
		source := cleanText(row["path"])
		finalPath := filepath.Join(destination, filepath.Base(strings.TrimRight(source, `\/`)))
		if samePath(source, finalPath) {
			continue
		}
		if err := os.Rename(source, finalPath); err != nil {
			return nil, mapOSError(err, source)
		}
		entry, err := entryFromPath(finalPath, destination, "")
		if err != nil {
			return nil, err
		}
		moved = append(moved, entry)
	}
	return map[string]any{"ok": true, "moved": moved}, nil
}

func pasteItems(payload map[string]any) (map[string]any, error) {
	operation := strings.ToLower(cleanText(payload["operation"]))
	if operation != "copy" && operation != "cut" {
		return nil, newError("invalid_request", fmt.Sprintf("Unsupported paste operation '%s'.", operation))
	}
	selections, err := normalizeSelectionRows(payload["paths"])
	if err != nil {
		return nil, err
	}
	destination, err := ensureDirectory(payload["destination_path"])
	if err != nil {
		return nil, err
	}
	if len(selections) == 0 {
		return nil, newError("invalid_request", "At least one source path is required.")
	}
	type planRow struct{ source, destination string }
	plan := []planRow{}
	for _, row := range selections {
		source := cleanText(row["path"])
		if !pathExists(source) {
			return nil, newError("path_not_found", fmt.Sprintf("'%s' does not exist.", source))
		}
		finalPath := filepath.Join(destination, filepath.Base(strings.TrimRight(source, `\/`)))
		if destinationInsideSource(source, destination) {
			return nil, newError("invalid_path", "A folder cannot be pasted into itself.")
		}
		if operation == "cut" {
			if samePath(source, finalPath) {
				continue
			}
			if pathExists(finalPath) {
				return nil, newError("conflict", fmt.Sprintf("'%s' already exists.", finalPath))
			}
		} else if samePath(source, finalPath) || pathExists(finalPath) {
			finalPath = nextCopyDestination(finalPath)
		}
		plan = append(plan, planRow{source: source, destination: finalPath})
	}
	pasted := []map[string]any{}
	for _, row := range plan {
		if operation == "cut" {
			if err := os.Rename(row.source, row.destination); err != nil {
				return nil, mapOSError(err, row.source)
			}
		} else if err := copyItem(row.source, row.destination); err != nil {
			return nil, err
		}
		entry, err := entryFromPath(row.destination, destination, "")
		if err != nil {
			return nil, err
		}
		pasted = append(pasted, entry)
	}
	return map[string]any{"ok": true, "pasted": pasted}, nil
}

func deleteItems(payload map[string]any) (map[string]any, error) {
	selections, err := normalizeSelectionRows(payload["paths"])
	if err != nil {
		return nil, err
	}
	if len(selections) == 0 {
		return nil, newError("invalid_request", "At least one path is required.")
	}
	deleted := []string{}
	for _, row := range selections {
		source := cleanText(row["path"])
		if !pathExists(source) {
			return nil, newError("path_not_found", fmt.Sprintf("'%s' does not exist.", source))
		}
	}
	for _, row := range selections {
		source := cleanText(row["path"])
		deleted = append(deleted, source)
		info, err := os.Lstat(source)
		if err != nil {
			return nil, mapOSError(err, source)
		}
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			if err := os.RemoveAll(source); err != nil {
				return nil, mapOSError(err, source)
			}
		} else if err := os.Remove(source); err != nil {
			return nil, mapOSError(err, source)
		}
	}
	return map[string]any{"ok": true, "deleted": deleted}, nil
}

func (m *Manager) uploadTransfer(ctx context.Context, payload map[string]any) error {
	transferID := cleanText(payload["transfer_id"])
	transferBaseURL := m.transferBaseURL(payload)
	targetPath, err := ensureDirectory(payload["target_path"])
	if err != nil {
		return err
	}
	items := mapSlice(payload["items"])
	if transferID == "" || len(items) == 0 {
		return newError("invalid_request", "Upload manifest is missing transfer metadata.")
	}
	var bytesTotal int64
	for _, row := range items {
		bytesTotal += asInt64(row["size_bytes"])
	}
	progress := map[string]any{"bytes_complete": int64(0), "bytes_total": bytesTotal, "last_report_at": time.Time{}}
	if snapshot, err := m.reportProgress(ctx, transferID, transferBaseURL, map[string]any{"status": "running", "bytes_complete": 0, "bytes_total": bytesTotal}); err == nil && isCanceled(snapshot) {
		return newError("transfer_canceled", "Transfer canceled by operator.")
	}
	for _, row := range items {
		if err := m.ensureTransferNotCanceled(ctx, transferID, transferBaseURL); err != nil {
			return err
		}
		itemID := cleanText(row["item_id"])
		relativePath, err := normalizeRelativeUploadPath(row["relative_path"], row["name"])
		if err != nil {
			return err
		}
		destination := filepath.Join(append([]string{targetPath}, strings.Split(relativePath, "/")...)...)
		if err := m.streamUploadItem(ctx, transferBaseURL, transferID, itemID, destination, progress, asBool(row["overwrite_existing"])); err != nil {
			return err
		}
	}
	bytesComplete := asInt64(progress["bytes_complete"])
	_, err = m.reportProgress(ctx, transferID, transferBaseURL, map[string]any{"status": "completed", "bytes_complete": bytesComplete, "bytes_total": bytesTotal})
	return err
}

func (m *Manager) streamUploadItem(ctx context.Context, transferBaseURL string, transferID string, itemID string, destinationPath string, progress map[string]any, overwriteExisting bool) error {
	if transferID == "" || itemID == "" {
		return newError("invalid_request", "Upload item metadata is missing.")
	}
	if pathExists(destinationPath) && !overwriteExisting {
		return newError("conflict", fmt.Sprintf("'%s' already exists.", destinationPath))
	}
	if overwriteExisting && isDirectory(destinationPath) {
		return newError("conflict", fmt.Sprintf("'%s' already exists as a directory.", destinationPath))
	}
	parent := filepath.Dir(destinationPath)
	if parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return mapOSError(err, parent)
		}
	}
	req, err := m.authRequest(ctx, http.MethodGet, fmt.Sprintf("/api/agent/files/transfers/%s/upload-item/%s", transferID, itemID), nil, transferBaseURL)
	if err != nil {
		return err
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return newError("transfer_failed", err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return newError("transfer_canceled", "Transfer canceled by operator.")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return newError("transfer_failed", fmt.Sprintf("upload item fetch failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}
	tempFile, err := os.CreateTemp(parent, ".borealis-upload-*.tmp")
	if err != nil {
		return mapOSError(err, parent)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()
	buf := make([]byte, 64*1024)
	lastControlCheck := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := tempFile.Write(buf[:n]); err != nil {
				_ = tempFile.Close()
				return mapOSError(err, tempPath)
			}
			progress["bytes_complete"] = asInt64(progress["bytes_complete"]) + int64(n)
			now := time.Now()
			lastReport, _ := progress["last_report_at"].(time.Time)
			if now.Sub(lastReport) >= transferProgressInterval {
				snapshot, _ := m.reportProgress(ctx, transferID, transferBaseURL, map[string]any{
					"status":         "running",
					"bytes_complete": asInt64(progress["bytes_complete"]),
					"bytes_total":    asInt64(progress["bytes_total"]),
				})
				progress["last_report_at"] = now
				if isCanceled(snapshot) {
					_ = tempFile.Close()
					return newError("transfer_canceled", "Transfer canceled by operator.")
				}
			} else if now.Sub(lastControlCheck) >= transferControlInterval {
				if err := m.ensureTransferNotCanceled(ctx, transferID, transferBaseURL); err != nil {
					_ = tempFile.Close()
					return err
				}
				lastControlCheck = now
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = tempFile.Close()
			return newError("transfer_failed", readErr.Error())
		}
	}
	if err := tempFile.Close(); err != nil {
		return mapOSError(err, tempPath)
	}
	if err := os.Rename(tempPath, destinationPath); err != nil {
		if overwriteExisting && pathExists(destinationPath) && !isDirectory(destinationPath) {
			if removeErr := os.Remove(destinationPath); removeErr != nil {
				return mapOSError(removeErr, destinationPath)
			}
			err = os.Rename(tempPath, destinationPath)
		}
		if err != nil {
			return mapOSError(err, destinationPath)
		}
	}
	return nil
}

func (m *Manager) downloadTransfer(ctx context.Context, payload map[string]any) error {
	transferID := cleanText(payload["transfer_id"])
	transferBaseURL := m.transferBaseURL(payload)
	selections, err := normalizeSelectionRows(payload["items"])
	if err != nil {
		return err
	}
	if transferID == "" || len(selections) == 0 {
		return newError("invalid_request", "Download manifest is missing transfer metadata.")
	}
	if err := m.ensureTransferNotCanceled(ctx, transferID, transferBaseURL); err != nil {
		return err
	}
	archiveRequired := asBool(payload["archive_required"])
	requestedName := normalizeUploadName(payload["archive_name"])
	if !archiveRequired && len(selections) == 1 && isRegularFile(cleanText(selections[0]["path"])) {
		sourcePath := cleanText(selections[0]["path"])
		artifactName := normalizeUploadName(selections[0]["name"])
		if artifactName == "" {
			artifactName = filepath.Base(sourcePath)
		}
		mimeType := mime.TypeByExtension(filepath.Ext(artifactName))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		totalBytes := fileSize(sourcePath)
		if snapshot, err := m.reportProgress(ctx, transferID, transferBaseURL, map[string]any{"status": "running", "bytes_complete": 0, "bytes_total": totalBytes}); err == nil && isCanceled(snapshot) {
			return newError("transfer_canceled", "Transfer canceled by operator.")
		}
		if err := m.ensureTransferNotCanceled(ctx, transferID, transferBaseURL); err != nil {
			return err
		}
		return m.postDownloadArtifact(ctx, transferBaseURL, transferID, sourcePath, artifactName, mimeType)
	}
	if requestedName == "" {
		requestedName = "download.zip"
	}
	archiveName := normalizeArchiveName(requestedName, ".zip")
	if err := os.MkdirAll(m.tempRoot, 0o700); err != nil {
		return mapOSError(err, m.tempRoot)
	}
	tempFile, err := os.CreateTemp(m.tempRoot, "download-*.zip")
	if err != nil {
		return mapOSError(err, m.tempRoot)
	}
	archivePath := tempFile.Name()
	_ = tempFile.Close()
	defer func() {
		_ = os.Remove(archivePath)
	}()
	totalBytes, err := zipSelection(selections, archivePath, func() error { return m.ensureTransferNotCanceled(ctx, transferID, transferBaseURL) })
	if err != nil {
		return err
	}
	if snapshot, err := m.reportProgress(ctx, transferID, transferBaseURL, map[string]any{"status": "running", "bytes_complete": 0, "bytes_total": totalBytes, "archive_name": archiveName}); err == nil && isCanceled(snapshot) {
		return newError("transfer_canceled", "Transfer canceled by operator.")
	}
	if err := m.ensureTransferNotCanceled(ctx, transferID, transferBaseURL); err != nil {
		return err
	}
	return m.postDownloadArtifact(ctx, transferBaseURL, transferID, archivePath, archiveName, "application/zip")
}

func (m *Manager) transferBaseURL(payload map[string]any) string {
	if baseURL := strings.TrimRight(cleanText(payload["transfer_base_url"]), "/"); baseURL != "" {
		return baseURL
	}
	if m.authClient == nil {
		return ""
	}
	return m.authClient.BaseURL()
}

func (m *Manager) authRequest(ctx context.Context, method string, path string, body io.Reader, baseURLOverride ...string) (*http.Request, error) {
	if m.authClient == nil {
		return nil, newError("client_unavailable", "The Borealis HTTP client is unavailable.")
	}
	if err := m.authClient.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}
	baseURL := ""
	if len(baseURLOverride) > 0 {
		baseURL = strings.TrimRight(strings.TrimSpace(baseURLOverride[0]), "/")
	}
	if baseURL == "" {
		baseURL = m.authClient.BaseURL()
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, err
	}
	for key, value := range m.authClient.AuthHeaders() {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	return req, nil
}

func (m *Manager) getJSON(ctx context.Context, path string, baseURLOverride ...string) (map[string]any, error) {
	req, err := m.authRequest(ctx, http.MethodGet, path, nil, baseURLOverride...)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return nil, newError("transfer_failed", fmt.Sprintf("GET %s failed: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body))))
	}
	out := map[string]any{}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &out)
	}
	return out, nil
}

func (m *Manager) postJSON(ctx context.Context, path string, payload map[string]any, baseURLOverride ...string) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := m.authRequest(ctx, http.MethodPost, path, bytes.NewReader(raw), baseURLOverride...)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return nil, newError("transfer_failed", fmt.Sprintf("POST %s failed: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body))))
	}
	out := map[string]any{}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &out)
	}
	return out, nil
}

func (m *Manager) ensureTransferNotCanceled(ctx context.Context, transferID string, baseURLOverride ...string) error {
	snapshot, err := m.getJSON(ctx, "/api/agent/files/transfers/"+transferID+"/status", baseURLOverride...)
	if err != nil {
		return err
	}
	if isCanceled(snapshot) {
		return newError("transfer_canceled", "Transfer canceled by operator.")
	}
	return nil
}

func (m *Manager) reportProgress(ctx context.Context, transferID string, transferBaseURL string, payload map[string]any) (map[string]any, error) {
	return m.postJSON(ctx, "/api/agent/files/transfers/"+transferID+"/progress", payload, transferBaseURL)
}

func (m *Manager) postDownloadArtifact(ctx context.Context, transferBaseURL string, transferID string, artifactPath string, artifactName string, mimeType string) error {
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	body, contentType, contentLength, cleanup, err := m.buildMultipartArtifactBody(artifactPath, artifactName, mimeType)
	if err != nil {
		return err
	}
	defer cleanup()
	req, err := m.authRequest(ctx, http.MethodPost, "/api/agent/files/transfers/"+transferID+"/content", body, transferBaseURL)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	req.ContentLength = contentLength
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return newError("transfer_canceled", "Transfer canceled by operator.")
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return newError("transfer_failed", fmt.Sprintf("artifact upload failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))))
	}
	return nil
}

func (m *Manager) buildMultipartArtifactBody(artifactPath string, artifactName string, mimeType string) (*os.File, string, int64, func(), error) {
	if strings.TrimSpace(artifactName) == "" {
		artifactName = filepath.Base(artifactPath)
	}
	if err := os.MkdirAll(m.tempRoot, 0o700); err != nil {
		return nil, "", 0, func() {}, mapOSError(err, m.tempRoot)
	}
	tempFile, err := os.CreateTemp(m.tempRoot, "download-artifact-*.multipart")
	if err != nil {
		return nil, "", 0, func() {}, mapOSError(err, m.tempRoot)
	}
	tempPath := tempFile.Name()
	cleanup := func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}
	writer := multipart.NewWriter(tempFile)
	if err := writer.WriteField("archive_name", artifactName); err != nil {
		cleanup()
		return nil, "", 0, func() {}, err
	}
	if err := writer.WriteField("mime_type", mimeType); err != nil {
		cleanup()
		return nil, "", 0, func() {}, err
	}
	part, err := writer.CreateFormFile("artifact", artifactName)
	if err != nil {
		cleanup()
		return nil, "", 0, func() {}, err
	}
	artifact, err := os.Open(artifactPath)
	if err != nil {
		cleanup()
		return nil, "", 0, func() {}, mapOSError(err, artifactPath)
	}
	_, copyErr := io.Copy(part, artifact)
	closeErr := artifact.Close()
	if copyErr != nil {
		cleanup()
		return nil, "", 0, func() {}, copyErr
	}
	if closeErr != nil {
		cleanup()
		return nil, "", 0, func() {}, closeErr
	}
	if err := writer.Close(); err != nil {
		cleanup()
		return nil, "", 0, func() {}, err
	}
	stat, err := tempFile.Stat()
	if err != nil {
		cleanup()
		return nil, "", 0, func() {}, err
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, "", 0, func() {}, err
	}
	return tempFile, writer.FormDataContentType(), stat.Size(), cleanup, nil
}

func normalizeRequestedPath(value any) (string, error) {
	raw := cleanText(value)
	if raw == "" {
		return "", newError("invalid_path", "A non-empty absolute path is required.")
	}
	if runtime.GOOS == "windows" {
		candidate := strings.ReplaceAll(raw, "/", `\`)
		if strings.HasPrefix(candidate, `\\`) {
			return filepath.Clean(candidate), nil
		}
		if len(candidate) >= 3 && candidate[1:3] == `:\` && isASCIIAlpha(candidate[0]) {
			normalized := filepath.Clean(candidate)
			if len(normalized) == 2 && normalized[1] == ':' {
				normalized += `\`
			}
			return normalized, nil
		}
		return "", newError("invalid_path", fmt.Sprintf("Expected an absolute Windows path, received '%s'.", raw))
	}
	if !strings.HasPrefix(raw, "/") {
		return "", newError("invalid_path", fmt.Sprintf("Expected an absolute POSIX path, received '%s'.", raw))
	}
	normalized := filepath.Clean(raw)
	if normalized == "." {
		return "/", nil
	}
	return normalized, nil
}

func ensureDirectory(value any) (string, error) {
	normalized, err := normalizeRequestedPath(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(normalized)
	if err != nil {
		return "", mapOSError(err, normalized)
	}
	if !info.IsDir() {
		return "", newError("not_a_directory", fmt.Sprintf("'%s' is not a directory.", normalized))
	}
	return normalized, nil
}

func parentPath(pathValue string) string {
	normalized, err := normalizeRequestedPath(pathValue)
	if err != nil {
		return ""
	}
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(normalized)
		stripped := strings.TrimRight(normalized, `\/`)
		if stripped == volume {
			return ""
		}
		parent := filepath.Dir(stripped)
		if parent == volume {
			parent += `\`
		}
		return parent
	}
	if normalized == "/" {
		return "/"
	}
	parent := filepath.Dir(normalized)
	if parent == "" || parent == "." {
		return "/"
	}
	return parent
}

func validateChildName(value any) (string, error) {
	raw := cleanText(value)
	if raw == "" {
		return "", newError("invalid_name", "A file or folder name is required.")
	}
	if strings.ContainsAny(raw, `/\`) || strings.Contains(raw, "\x00") {
		return "", newError("invalid_name", "Path separators are not allowed in file or folder names.")
	}
	if runtime.GOOS == "windows" && strings.ContainsAny(raw, `:*?"<>|`) {
		return "", newError("invalid_name", "Windows reserved filename characters are not allowed.")
	}
	name := normalizeUploadName(raw)
	if name == "" {
		return "", newError("invalid_name", "A file or folder name is required.")
	}
	if name == "." || name == ".." {
		return "", newError("invalid_name", "Relative path markers are not allowed.")
	}
	return name, nil
}

func normalizeUploadName(value any) string {
	raw := strings.ReplaceAll(cleanText(value), `\`, "/")
	if raw == "" {
		return ""
	}
	return strings.ReplaceAll(filepath.Base(raw), "\x00", "")
}

func normalizeRelativeUploadPath(value any, fallbackName any) (string, error) {
	raw := strings.ReplaceAll(cleanText(value), `\`, "/")
	if raw == "" {
		name, err := validateChildName(fallbackName)
		if err != nil {
			return "", err
		}
		raw = name
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) || (len(raw) >= 2 && raw[1] == ':') {
		return "", newError("invalid_name", fmt.Sprintf("Expected a relative upload path, received '%s'.", value))
	}
	segments := []string{}
	for _, segment := range strings.Split(raw, "/") {
		cleaned := cleanText(segment)
		if cleaned == "" {
			continue
		}
		name, err := validateChildName(cleaned)
		if err != nil {
			return "", err
		}
		segments = append(segments, name)
	}
	if len(segments) == 0 {
		return "", newError("invalid_name", "A relative upload path is required.")
	}
	return strings.Join(segments, "/"), nil
}

func normalizeUploadCandidateRows(value any) ([]map[string]any, error) {
	rows := []map[string]any{}
	for _, item := range mapSlice(value) {
		name, err := validateChildName(firstValue(item, "name", "filename"))
		if err != nil {
			return nil, err
		}
		relativePath, err := normalizeRelativeUploadPath(item["relative_path"], name)
		if err != nil {
			return nil, err
		}
		clientKey := cleanText(item["client_key"])
		if clientKey == "" {
			clientKey = relativePath
		}
		rows = append(rows, map[string]any{
			"client_key":    clientKey,
			"name":          name,
			"relative_path": relativePath,
			"size_bytes":    asInt64(item["size_bytes"]),
			"modified_at":   asInt64(item["modified_at"]),
		})
	}
	deduped := map[string]map[string]any{}
	for _, row := range rows {
		deduped[cleanText(row["client_key"])] = row
	}
	out := make([]map[string]any, 0, len(deduped))
	for _, row := range deduped {
		out = append(out, row)
	}
	return out, nil
}

func normalizeSelectionRows(value any) ([]map[string]any, error) {
	candidates := []any{}
	if list, ok := value.([]any); ok {
		candidates = list
	} else if list, ok := value.([]map[string]any); ok {
		candidates = make([]any, 0, len(list))
		for _, row := range list {
			candidates = append(candidates, row)
		}
	} else {
		candidates = []any{value}
	}
	rows := []map[string]any{}
	for _, item := range candidates {
		if itemMap, ok := item.(map[string]any); ok {
			pathValue := cleanText(itemMap["path"])
			if pathValue == "" {
				continue
			}
			normalized, err := normalizeRequestedPath(pathValue)
			if err != nil {
				return nil, err
			}
			rows = append(rows, map[string]any{
				"path": normalized,
				"name": cleanText(itemMap["name"]),
				"kind": strings.ToLower(cleanText(itemMap["kind"])),
			})
			continue
		}
		pathValue := cleanText(item)
		if pathValue == "" {
			continue
		}
		normalized, err := normalizeRequestedPath(pathValue)
		if err != nil {
			return nil, err
		}
		rows = append(rows, map[string]any{"path": normalized, "name": filepath.Base(strings.TrimRight(normalized, `\/`)), "kind": ""})
	}
	deduped := map[string]map[string]any{}
	for _, row := range rows {
		deduped[cleanText(row["path"])] = row
	}
	out := make([]map[string]any, 0, len(deduped))
	for _, row := range deduped {
		out = append(out, row)
	}
	return out, nil
}

func copyItem(sourcePath string, destinationPath string) error {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return mapOSError(err, sourcePath)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(sourcePath)
		if err == nil {
			if err := os.Symlink(target, destinationPath); err == nil {
				return nil
			}
		}
	}
	if info.IsDir() {
		return copyDir(sourcePath, destinationPath)
	}
	return copyFile(sourcePath, destinationPath, info.Mode().Perm())
}

func copyDir(sourcePath string, destinationPath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return mapOSError(err, sourcePath)
	}
	if err := os.Mkdir(destinationPath, info.Mode().Perm()); err != nil {
		return mapOSError(err, destinationPath)
	}
	entries, err := os.ReadDir(sourcePath)
	if err != nil {
		return mapOSError(err, sourcePath)
	}
	for _, entry := range entries {
		src := filepath.Join(sourcePath, entry.Name())
		dst := filepath.Join(destinationPath, entry.Name())
		if err := copyItem(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(sourcePath string, destinationPath string, mode os.FileMode) error {
	src, err := os.Open(sourcePath)
	if err != nil {
		return mapOSError(err, sourcePath)
	}
	defer src.Close()
	dst, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return mapOSError(err, destinationPath)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return nil
}

func zipSelection(selections []map[string]any, archivePath string, checkCanceled func() error) (int64, error) {
	file, err := os.Create(archivePath)
	if err != nil {
		return 0, mapOSError(err, archivePath)
	}
	archive := zip.NewWriter(file)
	for _, selection := range selections {
		if checkCanceled != nil {
			if err := checkCanceled(); err != nil {
				_ = archive.Close()
				_ = file.Close()
				return 0, err
			}
		}
		sourcePath := cleanText(selection["path"])
		label := normalizeUploadName(selection["name"])
		if label == "" {
			label = filepath.Base(strings.TrimRight(sourcePath, `\/`))
		}
		if label == "" {
			label = "download"
		}
		if isDirectory(sourcePath) && !isSymlink(sourcePath) {
			if err := zipDir(archive, sourcePath, label, checkCanceled); err != nil {
				_ = archive.Close()
				_ = file.Close()
				return 0, err
			}
			continue
		}
		if err := zipFile(archive, sourcePath, label); err != nil {
			_ = archive.Close()
			_ = file.Close()
			return 0, err
		}
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	return fileSize(archivePath), nil
}

func zipDir(archive *zip.Writer, sourcePath string, label string, checkCanceled func() error) error {
	emitted := false
	err := filepath.WalkDir(sourcePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if checkCanceled != nil {
			if err := checkCanceled(); err != nil {
				return err
			}
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == sourcePath {
			return nil
		}
		rel, _ := filepath.Rel(sourcePath, path)
		archiveName := strings.ReplaceAll(filepath.Join(label, rel), string(filepath.Separator), "/")
		if entry.IsDir() {
			_, err := archive.Create(archiveName + "/")
			if err == nil {
				emitted = true
			}
			return err
		}
		if err := zipFile(archive, path, archiveName); err != nil {
			return err
		}
		emitted = true
		return nil
	})
	if err != nil {
		return mapOSError(err, sourcePath)
	}
	if !emitted {
		_, err := archive.Create(strings.TrimRight(label, "/") + "/")
		return err
	}
	return nil
}

func zipFile(archive *zip.Writer, sourcePath string, archiveName string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return mapOSError(err, sourcePath)
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = strings.ReplaceAll(archiveName, string(filepath.Separator), "/")
	header.Method = zip.Deflate
	writer, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	file, err := os.Open(sourcePath)
	if err != nil {
		return mapOSError(err, sourcePath)
	}
	defer file.Close()
	_, err = io.Copy(writer, file)
	return err
}

func detectLineEnding(text string) string {
	if strings.Contains(text, "\r\n") {
		return "crlf"
	}
	if strings.Contains(text, "\r") {
		return "cr"
	}
	return "lf"
}

func normalizeLineEnding(value any) (string, error) {
	switch strings.ToLower(cleanText(value)) {
	case "", "lf", `\n`:
		return "lf", nil
	case "crlf", `\r\n`:
		return "crlf", nil
	case "cr", `\r`:
		return "cr", nil
	default:
		return "", newError("invalid_request", fmt.Sprintf("Unsupported line ending '%s'.", value))
	}
}

func normalizeContentLineEndings(content string, lineEnding string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	switch lineEnding {
	case "crlf":
		return strings.ReplaceAll(content, "\n", "\r\n")
	case "cr":
		return strings.ReplaceAll(content, "\n", "\r")
	default:
		return content
	}
}

func decodeTextPayload(payload []byte) (string, string, error) {
	if len(payload) == 0 {
		return "", "utf-8", nil
	}
	if bytes.HasPrefix(payload, []byte{0xEF, 0xBB, 0xBF}) {
		return string(payload[3:]), "utf-8-sig", nil
	}
	if bytes.HasPrefix(payload, []byte{0xFF, 0xFE}) {
		if (len(payload)-2)%2 != 0 {
			return "", "", newError("text_encoding_not_supported", "This text file uses an unsupported UTF-16 payload.")
		}
		units := make([]uint16, 0, (len(payload)-2)/2)
		for i := 2; i+1 < len(payload); i += 2 {
			units = append(units, uint16(payload[i])|uint16(payload[i+1])<<8)
		}
		return string(utf16.Decode(units)), "utf-16le", nil
	}
	if bytes.HasPrefix(payload, []byte{0xFE, 0xFF}) {
		if (len(payload)-2)%2 != 0 {
			return "", "", newError("text_encoding_not_supported", "This text file uses an unsupported UTF-16 payload.")
		}
		units := make([]uint16, 0, (len(payload)-2)/2)
		for i := 2; i+1 < len(payload); i += 2 {
			units = append(units, uint16(payload[i])<<8|uint16(payload[i+1]))
		}
		return string(utf16.Decode(units)), "utf-16be", nil
	}
	if !utf8.Valid(payload) {
		return "", "", newError("text_encoding_not_supported", "This text file uses an unsupported text encoding.")
	}
	return string(payload), "utf-8", nil
}

func encodeTextPayload(content string, encodingName string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encodingName)) {
	case "", "utf-8":
		return []byte(content), nil
	case "utf-8-sig":
		return append([]byte{0xEF, 0xBB, 0xBF}, []byte(content)...), nil
	case "utf-16", "utf-16le":
		units := utf16.Encode([]rune(content))
		out := []byte{0xFF, 0xFE}
		for _, unit := range units {
			out = append(out, byte(unit), byte(unit>>8))
		}
		return out, nil
	case "utf-16be":
		units := utf16.Encode([]rune(content))
		out := []byte{0xFE, 0xFF}
		for _, unit := range units {
			out = append(out, byte(unit>>8), byte(unit))
		}
		return out, nil
	default:
		return nil, newError("text_encoding_not_supported", fmt.Sprintf("Unsupported text encoding '%s'.", encodingName))
	}
}

func looksLikeBinaryBytes(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	allowNulls := bytes.HasPrefix(payload, []byte{0xFF, 0xFE}) || bytes.HasPrefix(payload, []byte{0xFE, 0xFF})
	sample := payload
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	if !allowNulls && bytes.Contains(sample, []byte{0}) {
		return true
	}
	control := 0
	for _, value := range sample {
		if value == 9 || value == 10 || value == 13 {
			continue
		}
		if value < 32 || value == 127 {
			control++
		}
	}
	return control > maxInt(1, len(sample)/5)
}

func looksLikeBinaryText(text string) bool {
	if text == "" {
		return false
	}
	runes := []rune(text)
	if len(runes) > 4096 {
		runes = runes[:4096]
	}
	control := 0
	for _, value := range runes {
		if value == '\t' || value == '\n' || value == '\r' {
			continue
		}
		if value < 32 || value == 127 {
			control++
		}
	}
	return control > maxInt(1, len(runes)/5)
}

func errorResponse(code string, message string) map[string]any {
	normalized := strings.TrimSpace(code)
	if normalized == "" {
		normalized = "agent_error"
	}
	return map[string]any{
		"ok":      false,
		"error":   normalized,
		"message": strings.TrimSpace(message),
	}
}

func normalizeError(err error) fmError {
	if err == nil {
		return fmError{Code: "agent_error", Message: "Unknown file-management error."}
	}
	if ferr, ok := err.(fmError); ok {
		return ferr
	}
	return fmError{Code: "agent_error", Message: err.Error()}
}

func newError(code string, message string) fmError {
	normalized := strings.TrimSpace(code)
	if normalized == "" {
		normalized = "agent_error"
	}
	return fmError{Code: normalized, Message: strings.TrimSpace(message)}
}

func cleanText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func firstValue(row map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := row[key]; ok {
			return value
		}
	}
	return nil
}

func cloneMap(row map[string]any) map[string]any {
	out := make(map[string]any, len(row))
	for key, value := range row {
		out[key] = value
	}
	return out
}

func mapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []map[string]any:
		out := make([]map[string]any, 0, len(typed))
		for _, row := range typed {
			out = append(out, row)
		}
		return out
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row, ok := item.(map[string]any); ok {
				out = append(out, row)
			}
		}
		return out
	default:
		if row, ok := typed.(map[string]any); ok {
			return []map[string]any{row}
		}
		return nil
	}
}

func asInt64(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case int:
		return int64(typed)
	case int8:
		return int64(typed)
	case int16:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint:
		return int64(typed)
	case uint8:
		return int64(typed)
	case uint16:
		return int64(typed)
	case uint32:
		return int64(typed)
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return int64(^uint64(0) >> 1)
		}
		return int64(typed)
	case float32:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed
		}
		if parsed, err := strconv.ParseFloat(typed.String(), 64); err == nil {
			return int64(parsed)
		}
		return 0
	default:
		parsed, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(typed)), 10, 64)
		if err == nil {
			return parsed
		}
		return 0
	}
}

func asBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "y", "on":
			return true
		default:
			return false
		}
	default:
		return asInt64(value) != 0
	}
}

func isCanceled(snapshot map[string]any) bool {
	if snapshot == nil {
		return false
	}
	if asBool(snapshot["cancel_requested"]) {
		return true
	}
	switch strings.ToLower(cleanText(snapshot["status"])) {
	case "canceling", "canceled":
		return true
	default:
		return false
	}
}

func pathExists(pathValue string) bool {
	if strings.TrimSpace(pathValue) == "" {
		return false
	}
	_, err := os.Lstat(pathValue)
	return err == nil
}

func isDirectory(pathValue string) bool {
	info, err := os.Stat(pathValue)
	return err == nil && info.IsDir()
}

func isRegularFile(pathValue string) bool {
	info, err := os.Stat(pathValue)
	return err == nil && info.Mode().IsRegular()
}

func isSymlink(pathValue string) bool {
	info, err := os.Lstat(pathValue)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func fileSize(pathValue string) int64 {
	info, err := os.Stat(pathValue)
	if err != nil {
		return 0
	}
	return info.Size()
}

func normalizeArchiveName(value string, extension string) string {
	name := normalizeUploadName(value)
	if name == "" {
		name = "download"
	}
	ext := strings.TrimSpace(extension)
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if ext != "" && !strings.EqualFold(filepath.Ext(name), ext) {
		name += ext
	}
	return name
}

func mapOSError(err error, pathValue string) error {
	if err == nil {
		return nil
	}
	if ferr, ok := err.(fmError); ok {
		return ferr
	}
	target := strings.TrimSpace(pathValue)
	if os.IsNotExist(err) {
		return newError("path_not_found", fmt.Sprintf("'%s' does not exist.", target))
	}
	if os.IsPermission(err) {
		return newError("permission_denied", fmt.Sprintf("Permission denied for '%s'.", target))
	}
	if os.IsExist(err) {
		return newError("conflict", fmt.Sprintf("'%s' already exists.", target))
	}
	return newError("agent_error", err.Error())
}

func samePath(left string, right string) bool {
	leftClean := filepath.Clean(strings.TrimRight(strings.TrimSpace(left), `\/`))
	rightClean := filepath.Clean(strings.TrimRight(strings.TrimSpace(right), `\/`))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftClean, rightClean)
	}
	return leftClean == rightClean
}

func destinationInsideSource(sourcePath string, destinationPath string) bool {
	if !isDirectory(sourcePath) {
		return false
	}
	sourceClean := filepath.Clean(strings.TrimRight(sourcePath, `\/`))
	destinationClean := filepath.Clean(destinationPath)
	if samePath(sourceClean, destinationClean) {
		return true
	}
	relative, err := filepath.Rel(sourceClean, destinationClean)
	if err != nil {
		return false
	}
	if relative == "." || relative == "" {
		return true
	}
	if strings.HasPrefix(relative, "..") {
		return false
	}
	return !filepath.IsAbs(relative)
}

func nextCopyDestination(destinationPath string) string {
	dir := filepath.Dir(destinationPath)
	name := filepath.Base(strings.TrimRight(destinationPath, `\/`))
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem = name
		ext = ""
	}
	candidate := filepath.Join(dir, stem+" - Copy"+ext)
	if !pathExists(candidate) {
		return candidate
	}
	for index := 2; index < 10_000; index++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s - Copy (%d)%s", stem, index, ext))
		if !pathExists(candidate) {
			return candidate
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s - Copy (%d)%s", stem, time.Now().UnixNano(), ext))
}

func entryName(pathValue string) string {
	normalized := filepath.Clean(pathValue)
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(normalized)
		if volume != "" && strings.TrimRight(normalized, `\/`) == volume {
			return volume + `\`
		}
	}
	if normalized == string(filepath.Separator) {
		return normalized
	}
	return filepath.Base(strings.TrimRight(normalized, `\/`))
}

func sizeForEntry(info os.FileInfo, isDir bool) int64 {
	if info == nil || isDir {
		return 0
	}
	return info.Size()
}

func sortEntries(entries []map[string]any) []map[string]any {
	out := append([]map[string]any(nil), entries...)
	sort.SliceStable(out, func(i, j int) bool {
		leftKind := cleanText(out[i]["kind"])
		rightKind := cleanText(out[j]["kind"])
		leftDir := leftKind == "directory"
		rightDir := rightKind == "directory"
		if leftDir != rightDir {
			return leftDir
		}
		return strings.ToLower(cleanText(out[i]["name"])) < strings.ToLower(cleanText(out[j]["name"]))
	})
	return out
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func displayName(relativePath string, fallbackName string) string {
	if strings.TrimSpace(relativePath) != "" {
		return relativePath
	}
	return fallbackName
}

func isASCIIAlpha(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

func maxInt64(left int64, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
