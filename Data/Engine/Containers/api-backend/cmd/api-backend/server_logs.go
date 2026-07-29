package main

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLogRetentionDays = 30
	maxLogTailLines         = 2000
)

var (
	serviceLogLinePattern = regexp.MustCompile(`^\[(?P<ts>[^\]]+)\]\s+\[(?P<level>[A-Z0-9_-]+)\](?P<context>(?:\[[^\]]+\])*)\s+(?P<msg>.*)$`)
	contextLogPattern     = regexp.MustCompile(`(?i)\[CONTEXT-([^\]]+)\]`)
	pythonLogLinePattern  = regexp.MustCompile(`^(?P<ts>\d{4}-\d{2}-\d{2}\s+[0-9:,]+)-(?P<logger>.+?)-(?P<level>[A-Z]+):\s*(?P<msg>.*)$`)
)

func registerServerLogRoutes(mux *http.ServeMux, auth *authService, _ http.Handler) {
	mux.HandleFunc("/api/server/logs", serverLogsHandler(auth))
	mux.HandleFunc("/api/server/logs/", serverLogEntriesHandler(auth))
}

func serverLogsHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		root := resolveLogRoot()
		retention := loadLogRetention(root)
		deleted := applyLogRetention(root, retention, defaultLogRetentionDays)
		writeJSON(w, http.StatusOK, map[string]any{
			"log_root":               root,
			"logs":                   logDomainSnapshot(root, retention, defaultLogRetentionDays),
			"default_retention_days": defaultLogRetentionDays,
			"retention_overrides":    retention,
			"retention_deleted":      deleted,
		})
	}
}

func serverLogEntriesHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/api/server/logs/retention" {
			serverLogRetentionUpdate(w, r, auth)
			return
		}
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/server/logs/") {
			serverLogDelete(w, r, auth)
			return
		}
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		prefix := "/api/server/logs/"
		suffix := "/entries"
		requestPath := r.URL.EscapedPath()
		if !strings.HasPrefix(requestPath, prefix) || !strings.HasSuffix(requestPath, suffix) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		logName := routeLogName(strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix))
		limit := parseIntDefault(r.URL.Query().Get("limit"), 750)
		if limit < 50 {
			limit = 50
		}
		if limit > maxLogTailLines {
			limit = maxLogTailLines
		}
		payload, status, err := readLogEntries(resolveLogRoot(), logName, limit)
		if err != nil {
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, payload)
	}
}

func serverLogRetentionUpdate(w http.ResponseWriter, r *http.Request, auth *authService) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && err.Error() != "EOF" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
		return
	}
	updates := parseLogRetentionUpdates(payload["retention"])
	root := resolveLogRoot()
	retention := loadLogRetention(root)
	changed := 0
	for key, days := range updates {
		if days == nil {
			if _, ok := retention[key]; ok {
				delete(retention, key)
				changed++
			}
			continue
		}
		if int(coerceInt64(retention[key])) != *days {
			retention[key] = *days
			changed++
		}
	}
	if err := saveLogRetention(root, retention); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "retention_save_failed", "detail": err.Error()})
		return
	}
	retention = loadLogRetention(root)
	deleted := applyLogRetention(root, retention, defaultLogRetentionDays)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":              "ok",
		"logs":                logDomainSnapshot(root, retention, defaultLogRetentionDays),
		"retention_overrides": retention,
		"retention_deleted":   deleted,
		"changed":             changed,
	})
}

func serverLogDelete(w http.ResponseWriter, r *http.Request, auth *authService) {
	if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
		failure.write(w)
		return
	}
	prefix := "/api/server/logs/"
	requestPath := r.URL.EscapedPath()
	logName := routeLogName(strings.TrimPrefix(requestPath, prefix))
	root := resolveLogRoot()
	scope := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scope")))
	var deleted []string
	var err error
	if scope == "family" {
		deleted, err = deleteLogFamily(root, logName)
	} else {
		var file string
		file, err = deleteLogFile(root, logName)
		if file != "" {
			deleted = []string{file}
		}
	}
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "Log file not found."})
		return
	}
	retention := loadLogRetention(root)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "deleted",
		"deleted": deleted,
		"logs":    logDomainSnapshot(root, retention, defaultLogRetentionDays),
	})
}

func resolveLogRoot() string {
	if value := cleanText(os.Getenv("BOREALIS_GO_API_LOG_ROOT")); value != "" {
		return filepath.Clean(value)
	}
	if value := cleanText(os.Getenv("BOREALIS_LOG_ROOT")); value != "" {
		return filepath.Clean(value)
	}
	if logFile := cleanText(os.Getenv("BOREALIS_LOG_FILE")); logFile != "" {
		return filepath.Dir(filepath.Clean(logFile))
	}
	return "/opt/Borealis/Engine/Services/api-backend/logs"
}

func routeLogName(raw string) string {
	cleaned := strings.Trim(raw, "/")
	if decoded, err := url.PathUnescape(cleaned); err == nil {
		return decoded
	}
	return cleaned
}

func canonicalLogName(name string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if decoded, err := url.PathUnescape(cleaned); err == nil {
		cleaned = strings.TrimSpace(strings.ReplaceAll(decoded, "\\", "/"))
	}
	if cleaned == "" || strings.HasPrefix(cleaned, "/") {
		return ""
	}
	normalized := pathpkg.Clean(cleaned)
	if normalized == "." || normalized == ".." || pathpkg.IsAbs(normalized) || strings.HasPrefix(normalized, "../") {
		return ""
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return ""
		}
	}
	return normalized
}

func logBaseName(filename string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(filename, "\\", "/"))
	dir, leaf := pathpkg.Split(normalized)
	if strings.HasSuffix(leaf, ".log") {
		return normalized
	}
	if index := strings.Index(leaf, ".log."); index >= 0 {
		return dir + leaf[:index+4]
	}
	return ""
}

func displayLogLabel(filename string) string {
	base := strings.TrimSpace(strings.ReplaceAll(filename, "\\", "/"))
	if domain := logBaseName(base); domain != "" {
		base = domain
	}
	base = strings.TrimSuffix(base, ".log")
	if base == "" {
		return filename
	}
	segments := strings.Split(base, "/")
	labels := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(segment, "_", " "), "-", " "))
		parts := strings.Fields(segment)
		for index, part := range parts {
			if part == "" {
				continue
			}
			if strings.ToUpper(part) == part {
				parts[index] = part
				continue
			}
			parts[index] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
		}
		if len(parts) > 0 {
			labels = append(labels, strings.Join(parts, " "))
		}
	}
	if len(labels) == 0 {
		return filename
	}
	return strings.Join(labels, " / ")
}

func logFileMetadata(path string) map[string]any {
	info, err := os.Stat(path)
	if err != nil {
		return map[string]any{"size_bytes": int64(0), "modified": nil}
	}
	return map[string]any{
		"size_bytes": info.Size(),
		"modified":   info.ModTime().UTC().Format(time.RFC3339),
	}
}

func loadLogRetention(root string) map[string]any {
	path := filepath.Join(root, "retention_policy.json")
	content, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		return map[string]any{}
	}
	rawOverrides, _ := payload["overrides"].(map[string]any)
	retention := map[string]any{}
	for key, value := range rawOverrides {
		canonical := canonicalLogName(key)
		if canonical == "" {
			continue
		}
		days := coerceInt64(value)
		if days <= 0 {
			continue
		}
		retention[canonical] = days
	}
	return retention
}

func saveLogRetention(root string, retention map[string]any) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	cleaned := map[string]int{}
	for key, value := range retention {
		canonical := canonicalLogName(key)
		days := int(coerceInt64(value))
		if canonical == "" || days <= 0 {
			continue
		}
		cleaned[canonical] = days
	}
	content, err := json.MarshalIndent(map[string]any{"overrides": cleaned}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, "retention_policy.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(content, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func parseLogRetentionUpdates(value any) map[string]*int {
	updates := map[string]*int{}
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			addLogRetentionUpdate(updates, key, item)
		}
	case []any:
		for _, item := range typed {
			entry := asMap(item)
			name := firstText(cleanText(entry["file"]), cleanText(entry["name"]))
			addLogRetentionUpdate(updates, name, entry["days"])
		}
	}
	return updates
}

func addLogRetentionUpdate(updates map[string]*int, name string, value any) {
	canonical := canonicalLogName(name)
	if canonical == "" {
		return
	}
	if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
		return
	}
	if boolean, ok := value.(bool); ok && !boolean {
		return
	}
	if value == nil {
		updates[canonical] = nil
		return
	}
	days := int(coerceInt64(value))
	if days <= 0 {
		updates[canonical] = nil
		return
	}
	updates[canonical] = &days
}

func logRetentionDays(retention map[string]any, base string, defaultDays int) int {
	if value, ok := retention[base]; ok {
		days := int(coerceInt64(value))
		if days > 0 {
			return days
		}
	}
	return defaultDays
}

func applyLogRetention(root string, retention map[string]any, defaultDays int) []any {
	now := time.Now().UTC()
	deleted := []any{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		name = filepath.ToSlash(name)
		base := logBaseName(name)
		if base == "" || name == base {
			return nil
		}
		days := logRetentionDays(retention, base, defaultDays)
		if days <= 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().UTC().Before(now.Add(-time.Duration(days) * 24 * time.Hour)) {
			if err := os.Remove(path); err == nil {
				deleted = append(deleted, name)
			}
		}
		return nil
	})
	return deleted
}

func logDomainSnapshot(root string, retention map[string]any, defaultDays int) []any {
	domains := map[string]map[string]any{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		name = filepath.ToSlash(name)
		base := logBaseName(name)
		if base == "" {
			return nil
		}
		domain := domains[base]
		if domain == nil {
			domain = map[string]any{
				"file":              base,
				"display_name":      displayLogLabel(base),
				"rotations":         []any{},
				"family_size_bytes": int64(0),
				"active":            false,
			}
			domains[base] = domain
		}
		metadata := logFileMetadata(path)
		metadata["file"] = name
		domain["family_size_bytes"] = coerceInt64(domain["family_size_bytes"]) + coerceInt64(metadata["size_bytes"])
		if name == base {
			domain["active"] = true
			for key, value := range metadata {
				domain[key] = value
			}
		} else {
			domain["rotations"] = append(jsonArray(domain["rotations"]), metadata)
		}
		return nil
	})
	results := make([]map[string]any, 0, len(domains))
	for _, domain := range domains {
		rotations := jsonArray(domain["rotations"])
		sort.SliceStable(rotations, func(left, right int) bool {
			return cleanText(asMap(rotations[left])["modified"]) > cleanText(asMap(rotations[right])["modified"])
		})
		domain["rotations"] = rotations
		domain["rotation_count"] = len(rotations)
		domain["retention_days"] = logRetentionDays(retention, cleanText(domain["file"]), defaultDays)
		hasActive := domain["active"] == true
		delete(domain, "active")
		versions := []any{}
		if hasActive {
			versions = append(versions, map[string]any{
				"file":       domain["file"],
				"label":      "Active",
				"modified":   domain["modified"],
				"size_bytes": domain["size_bytes"],
			})
		} else {
			domain["size_bytes"] = int64(0)
			domain["modified"] = nil
		}
		for _, rotation := range rotations {
			item := asMap(rotation)
			versions = append(versions, map[string]any{
				"file":       item["file"],
				"label":      item["file"],
				"modified":   item["modified"],
				"size_bytes": item["size_bytes"],
			})
		}
		domain["versions"] = versions
		results = append(results, domain)
	}
	sort.SliceStable(results, func(left, right int) bool {
		return cleanText(results[left]["display_name"]) < cleanText(results[right]["display_name"])
	})
	out := make([]any, 0, len(results))
	for _, result := range results {
		out = append(out, result)
	}
	return out
}

func readLogEntries(root string, filename string, limit int) (map[string]any, int, error) {
	canonical := canonicalLogName(filename)
	if canonical == "" || logBaseName(canonical) == "" {
		return map[string]any{"error": "not_found", "message": "Log file not found."}, http.StatusNotFound, os.ErrNotExist
	}
	path := filepath.Clean(filepath.Join(root, canonical))
	rootClean, _ := filepath.Abs(root)
	pathAbs, _ := filepath.Abs(path)
	if !strings.HasPrefix(pathAbs, rootClean+string(os.PathSeparator)) && pathAbs != rootClean {
		return map[string]any{"error": "not_found", "message": "Log file not found."}, http.StatusNotFound, os.ErrNotExist
	}
	info, err := os.Stat(pathAbs)
	if err != nil || info.IsDir() {
		return map[string]any{"error": "not_found", "message": "Log file not found."}, http.StatusNotFound, os.ErrNotExist
	}
	lines, total, truncated, err := tailLogLines(pathAbs, limit)
	if err != nil {
		return map[string]any{"error": err.Error()}, http.StatusInternalServerError, err
	}
	serviceName := displayLogLabel(canonical)
	startIndex := total - len(lines)
	entries := make([]any, 0, len(lines))
	for index, line := range lines {
		parsed := parseLogLine(line, serviceName)
		parsed["id"] = canonical + ":" + strconv.Itoa(startIndex+index)
		entries = append(entries, parsed)
	}
	return map[string]any{
		"file":           canonical,
		"entries":        entries,
		"total_lines":    total,
		"returned_lines": len(entries),
		"truncated":      truncated,
		"size_bytes":     info.Size(),
		"modified":       info.ModTime().UTC().Format(time.RFC3339),
	}, http.StatusOK, nil
}

func resolveLogPath(root string, filename string) (string, string, error) {
	canonical := canonicalLogName(filename)
	if canonical == "" || logBaseName(canonical) == "" {
		return "", "", os.ErrNotExist
	}
	path := filepath.Clean(filepath.Join(root, canonical))
	rootClean, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(pathAbs, rootClean+string(os.PathSeparator)) && pathAbs != rootClean {
		return "", "", os.ErrNotExist
	}
	info, err := os.Stat(pathAbs)
	if err != nil || info.IsDir() {
		return "", "", os.ErrNotExist
	}
	return pathAbs, canonical, nil
}

func deleteLogFile(root string, filename string) (string, error) {
	path, canonical, err := resolveLogPath(root, filename)
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return canonical, nil
}

func deleteLogFamily(root string, filename string) ([]string, error) {
	base := logBaseName(filename)
	if base == "" {
		base = filename
	}
	canonical := canonicalLogName(base)
	if canonical == "" {
		return nil, os.ErrNotExist
	}
	prefix := canonical + "."
	deleted := []string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		name = filepath.ToSlash(name)
		if name != canonical && !strings.HasPrefix(name, prefix) {
			return nil
		}
		if _, _, err := resolveLogPath(root, name); err != nil {
			return nil
		}
		if err := os.Remove(path); err == nil {
			deleted = append(deleted, name)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if len(deleted) == 0 {
		return nil, os.ErrNotExist
	}
	sort.Strings(deleted)
	return deleted, nil
}

func tailLogLines(path string, limit int) ([]string, int, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	initialCap := limit
	if initialCap > 128 {
		initialCap = 128
	}
	lines := make([]string, 0, initialCap)
	total := 0
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			total++
			line = strings.TrimRight(line, "\r\n")
			if len(lines) < limit {
				lines = append(lines, line)
			} else {
				copy(lines, lines[1:])
				lines[len(lines)-1] = line
			}
		}
		if err != nil {
			break
		}
	}
	return lines, total, total > limit, nil
}

func parseLogLine(raw string, serviceName string) map[string]any {
	if match := serviceLogLinePattern.FindStringSubmatch(raw); match != nil {
		ts := match[1]
		level := strings.ToUpper(match[2])
		contextBlock := match[3]
		message := strings.TrimSpace(match[4])
		var scope any
		if contextMatch := contextLogPattern.FindStringSubmatch(contextBlock); contextMatch != nil {
			scope = contextMatch[1]
		}
		return map[string]any{"timestamp": ts, "level": level, "scope": scope, "service": serviceName, "message": message, "raw": raw}
	}
	if match := pythonLogLinePattern.FindStringSubmatch(raw); match != nil {
		return map[string]any{"timestamp": match[1], "level": strings.ToUpper(match[3]), "scope": nil, "service": match[2], "message": strings.TrimSpace(match[4]), "raw": raw}
	}
	return map[string]any{"timestamp": nil, "level": nil, "scope": nil, "service": serviceName, "message": strings.TrimSpace(raw), "raw": raw}
}

func asMap(value any) map[string]any {
	typed, _ := value.(map[string]any)
	if typed == nil {
		return map[string]any{}
	}
	return typed
}
