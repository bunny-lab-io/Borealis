package patchmanagement

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
)

const (
	patchRefreshInterval = 5 * time.Minute
	patchBoostInterval   = 5 * time.Second
	patchBoostWindow     = 45 * time.Second
	patchCommandTimeout  = 3 * time.Minute
	patchInstallTimeout  = 90 * time.Minute
)

var kbPattern = regexp.MustCompile(`(?i)\bKB\d{4,9}\b`)

type Manager struct {
	authClient        *auth.Client
	hostname          string
	serviceMode       string
	runner            commandRunner
	publisher         func(context.Context, Snapshot) error
	wakeup            chan struct{}
	mu                sync.Mutex
	started           bool
	loopRunning       bool
	supported         bool
	unsupportedReason string
	lastError         string
	lastRefreshAt     int64
	lastPatchCount    int
	fastPollUntil     time.Time
	installRunning    bool
	lastInstallAt     int64
	lastInstallError  string
}

type Patch struct {
	PatchKey       string         `json:"patch_key"`
	KB             string         `json:"kb,omitempty"`
	Title          string         `json:"title"`
	State          string         `json:"state"`
	Source         string         `json:"source"`
	Classification string         `json:"classification,omitempty"`
	Severity       string         `json:"severity,omitempty"`
	InstalledOn    int64          `json:"installed_on,omitempty"`
	PublishedAt    int64          `json:"published_at,omitempty"`
	CapturedAt     int64          `json:"captured_at"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type Snapshot struct {
	Patches []Patch
}

type RoleHealth struct {
	Status     string
	StatusCode string
	Detail     string
	Details    map[string]any
}

type commandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type commandRunner func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error)

func New(authClient *auth.Client, hostname string, serviceMode string) *Manager {
	supported, reason := detectSupport()
	manager := &Manager{
		authClient:        authClient,
		hostname:          strings.TrimSpace(hostname),
		serviceMode:       auth.NormalizeServiceMode(serviceMode),
		runner:            runCommand,
		wakeup:            make(chan struct{}, 1),
		supported:         supported,
		unsupportedReason: reason,
	}
	manager.publisher = manager.publishPatches
	return manager
}

func detectSupport() (bool, string) {
	if runtime.GOOS == "windows" {
		return true, ""
	}
	return false, fmt.Sprintf("Patch management is unsupported on %s agents in this release.", runtime.GOOS)
}

func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	if !m.supported {
		m.lastError = m.unsupportedReason
		m.mu.Unlock()
		return
	}
	m.loopRunning = true
	m.mu.Unlock()
	go m.pollLoop(ctx)
}

func (m *Manager) Health() RoleHealth {
	m.mu.Lock()
	supported := m.supported
	unsupportedReason := m.unsupportedReason
	loopRunning := m.loopRunning
	lastError := m.lastError
	lastRefreshAt := m.lastRefreshAt
	lastPatchCount := m.lastPatchCount
	installRunning := m.installRunning
	lastInstallAt := m.lastInstallAt
	lastInstallError := m.lastInstallError
	m.mu.Unlock()

	details := map[string]any{
		"running_status":      "Running",
		"patch_count":         strconv.Itoa(lastPatchCount),
		"last_refresh_at":     strconv.FormatInt(lastRefreshAt, 10),
		"refresh_interval_ms": strconv.Itoa(int(patchRefreshInterval / time.Millisecond)),
		"install_running":     strconv.FormatBool(installRunning),
		"last_install_at":     strconv.FormatInt(lastInstallAt, 10),
		"runtime":             "go",
	}
	if strings.TrimSpace(lastInstallError) != "" {
		details["last_install_error"] = lastInstallError
	}
	if !supported {
		details["running_status"] = "Unsupported"
		return RoleHealth{
			Status:     "unsupported",
			StatusCode: "unsupported",
			Detail:     fallbackText(unsupportedReason, "Patch management is unsupported on this platform."),
			Details:    details,
		}
	}
	if !loopRunning {
		details["running_status"] = "Stopped"
		return RoleHealth{
			Status:     "pending",
			StatusCode: "pending",
			Detail:     "Waiting for patch inventory refresh loop.",
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
	if lastRefreshAt <= 0 {
		return RoleHealth{
			Status:     "pending",
			StatusCode: "pending",
			Detail:     "Waiting for initial patch inventory snapshot.",
			Details:    details,
		}
	}
	return RoleHealth{
		Status:     "healthy",
		StatusCode: "healthy",
		Detail:     "Patch inventory refresh loop active.",
		Details:    details,
	}
}

func (m *Manager) HandleRefreshRequest(ctx context.Context, payload any) (any, error) {
	body, ok := payload.(map[string]any)
	if !ok {
		return errorResponse("invalid_request", "Patch refresh payload must be an object."), nil
	}
	if !m.matchesTarget(body) {
		return errorResponse("not_for_host", "The patch refresh request targeted another device."), nil
	}
	if !m.supported {
		return errorResponse("unsupported_platform", fallbackText(m.unsupportedReason, "Patch management is unsupported on this platform.")), nil
	}
	reason := cleanText(body["reason"])
	if reason == "" {
		reason = "operator:" + fallbackText(cleanText(body["requested_by"]), "unknown")
	}
	m.RequestRefresh(reason)
	return map[string]any{
		"ok":     true,
		"status": "accepted",
		"reason": reason,
	}, nil
}

func (m *Manager) HandleInstallRequest(ctx context.Context, payload any) (any, error) {
	body, ok := payload.(map[string]any)
	if !ok {
		return errorResponse("invalid_request", "Patch install payload must be an object."), nil
	}
	if !m.matchesTarget(body) {
		return errorResponse("not_for_host", "The patch install request targeted another device."), nil
	}
	if !m.supported {
		return errorResponse("unsupported_platform", fallbackText(m.unsupportedReason, "Patch management is unsupported on this platform.")), nil
	}
	patch := patchInstallRequest(body)
	if patch == nil {
		return errorResponse("patch_required", "Patch install payload must include patch identity."), nil
	}
	if state := normalizePatchState(patch["state"]); state != "" && state != "pending" {
		return errorResponse("patch_not_pending", "Only pending Windows updates can be installed."), nil
	}
	if !m.beginInstall() {
		return errorResponse("install_in_progress", "Another patch install is already running on this device."), nil
	}
	requestID := cleanText(body["request_id"])
	if requestID == "" {
		requestID = fmt.Sprintf("patch-%d", time.Now().UnixNano())
	}
	patch["request_id"] = requestID
	patch["requested_at"] = coerceInt64(body["requested_at"])
	patch["requested_by"] = cleanText(body["requested_by"])
	patch["scope"] = cleanText(body["scope"])
	if truthy(body["wait_for_completion"]) || truthy(body["wait"]) {
		result := m.runInstallSync(ctx, patch, requestID)
		return result, nil
	}
	go m.runInstall(context.Background(), patch, requestID)
	return map[string]any{
		"ok":         true,
		"status":     "accepted",
		"request_id": requestID,
		"patch_key":  cleanText(patch["patch_key"]),
		"kb":         cleanText(patch["kb"]),
		"title":      cleanText(patch["title"]),
	}, nil
}

func (m *Manager) RequestRefresh(reason string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.fastPollUntil = time.Now().Add(patchBoostWindow)
	loopRunning := m.loopRunning
	m.mu.Unlock()
	select {
	case m.wakeup <- struct{}{}:
	default:
	}
	if !loopRunning {
		go func() {
			_ = m.Refresh(context.Background())
		}()
	}
}

func (m *Manager) Refresh(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if !m.supported {
		return fmt.Errorf("%s", fallbackText(m.unsupportedReason, "patch management unsupported"))
	}
	snapshot, err := m.buildSnapshot(ctx)
	if err != nil {
		m.recordError("Patch inventory refresh failed: " + err.Error())
		return err
	}
	if err := m.publisher(ctx, snapshot); err != nil {
		m.recordError("Patch inventory publish failed: " + err.Error())
		return err
	}
	m.mu.Lock()
	m.lastError = ""
	m.lastRefreshAt = time.Now().Unix()
	m.lastPatchCount = len(snapshot.Patches)
	m.mu.Unlock()
	return nil
}

func (m *Manager) pollLoop(ctx context.Context) {
	defer func() {
		m.mu.Lock()
		m.loopRunning = false
		m.mu.Unlock()
	}()
	for {
		if err := m.Refresh(ctx); err != nil {
			m.recordError("Patch inventory refresh failed: " + err.Error())
		}
		waitFor := patchRefreshInterval
		m.mu.Lock()
		if time.Now().Before(m.fastPollUntil) {
			waitFor = patchBoostInterval
		}
		m.mu.Unlock()
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.wakeup:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (m *Manager) buildSnapshot(ctx context.Context) (Snapshot, error) {
	rows, err := m.collectPatches(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Patches: rows}, nil
}

func (m *Manager) collectPatches(ctx context.Context) ([]Patch, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("%s", fallbackText(m.unsupportedReason, "unsupported platform"))
	}
	result, err := m.runner(ctx, patchCommandTimeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", windowsPatchInventoryScript())
	if err != nil || result.ExitCode != 0 {
		return nil, commandError(result, err)
	}
	return parseWindowsPatchInventory(result.Stdout)
}

func (m *Manager) beginInstall() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.installRunning {
		return false
	}
	m.installRunning = true
	m.lastInstallError = ""
	return true
}

func (m *Manager) finishInstall(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.installRunning = false
	m.lastInstallAt = time.Now().Unix()
	if err != nil {
		m.lastInstallError = err.Error()
	} else {
		m.lastInstallError = ""
	}
}

func (m *Manager) runInstall(ctx context.Context, patch map[string]any, requestID string) {
	_, err := m.installPatch(ctx, patch)
	m.finishInstall(err)
	m.RequestRefresh("patch_install_complete:" + fallbackText(requestID, "unknown"))
}

func (m *Manager) runInstallSync(ctx context.Context, patch map[string]any, requestID string) map[string]any {
	result, err := m.installPatch(ctx, patch)
	m.finishInstall(err)
	m.RequestRefresh("patch_install_complete:" + fallbackText(requestID, "unknown"))
	response := map[string]any{
		"ok":         err == nil,
		"status":     "completed",
		"request_id": requestID,
		"patch_key":  cleanText(patch["patch_key"]),
		"kb":         cleanText(patch["kb"]),
		"title":      cleanText(patch["title"]),
		"stdout":     result.Stdout,
		"stderr":     result.Stderr,
		"exit_code":  result.ExitCode,
	}
	if result.Parsed != nil {
		response["result"] = result.Parsed
		for _, key := range []string{"result_code", "reboot_required", "matched_count", "installed_count"} {
			if value, ok := result.Parsed[key]; ok {
				response[key] = value
			}
		}
	}
	if err != nil {
		response["status"] = "failed"
		response["error"] = "patch_install_failed"
		response["message"] = err.Error()
	}
	return response
}

type patchInstallResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Parsed   map[string]any
}

func (m *Manager) installPatch(ctx context.Context, patch map[string]any) (patchInstallResult, error) {
	if !m.supported {
		return patchInstallResult{}, fmt.Errorf("%s", fallbackText(m.unsupportedReason, "unsupported platform"))
	}
	result, err := m.runner(ctx, patchInstallTimeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", windowsPatchInstallScript(patch))
	installResult := patchInstallResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		Parsed:   parsePatchInstallResult(result.Stdout),
	}
	if parsed := installResult.Parsed; parsed != nil {
		if okValue, exists := parsed["ok"]; exists && !truthy(okValue) {
			message := fallbackText(cleanText(parsed["message"]), fallbackText(cleanText(parsed["error"]), "patch install failed"))
			return installResult, fmt.Errorf("%s", message)
		}
	}
	if err != nil || result.ExitCode != 0 {
		return installResult, commandError(result, err)
	}
	return installResult, nil
}

func (m *Manager) publishPatches(ctx context.Context, snapshot Snapshot) error {
	if m.authClient == nil {
		return fmt.Errorf("auth client unavailable")
	}
	details := map[string]any{
		"summary": map[string]any{
			"hostname":     m.hostname,
			"agent_id":     m.authClient.AgentID(),
			"service_mode": m.serviceMode,
		},
		"patches": snapshot.Patches,
	}
	payload := map[string]any{
		"agent_id":     m.authClient.AgentID(),
		"hostname":     m.hostname,
		"service_mode": m.serviceMode,
		"details":      details,
	}
	_, err := m.authClient.PostJSON(ctx, "/api/agent/details", payload, nil)
	return err
}

func (m *Manager) recordError(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = strings.TrimSpace(message)
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

func patchInstallRequest(body map[string]any) map[string]any {
	rawPatch, _ := body["patch"].(map[string]any)
	metadata := normalizeMetadata(firstNonNil(firstValue(rawPatch, "metadata"), body["metadata"]))
	out := map[string]any{}
	for _, key := range []string{"patch_key", "kb", "title", "state", "source", "classification", "severity"} {
		value := firstNonNil(firstValue(rawPatch, key), body[key])
		if clean := cleanText(value); clean != "" {
			out[key] = clean
		}
	}
	updateID := cleanText(firstNonNil(firstValue(metadata, "update_id", "updateID"), firstValue(rawPatch, "update_id", "updateID"), firstValue(body, "update_id", "updateID")))
	if updateID != "" {
		out["update_id"] = updateID
	}
	revision := coerceInt64(firstNonNil(firstValue(metadata, "revision_number", "revision"), firstValue(rawPatch, "revision_number", "revision"), firstValue(body, "revision_number", "revision")))
	if revision > 0 {
		out["revision_number"] = revision
	}
	if len(metadata) > 0 {
		out["metadata"] = metadata
	}
	if cleanText(out["patch_key"]) == "" && cleanText(out["kb"]) == "" && cleanText(out["title"]) == "" && updateID == "" {
		return nil
	}
	if kb := normalizeKB(out["kb"]); kb != "" {
		out["kb"] = kb
	}
	return out
}

func parsePatchInstallResult(output string) map[string]any {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil
	}
	return payload
}

func parseWindowsPatchInventory(output string) ([]Patch, error) {
	if strings.TrimSpace(output) == "" {
		return []Patch{}, nil
	}
	var raw any
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return nil, err
	}
	items := []map[string]any{}
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			if mapped, ok := item.(map[string]any); ok {
				items = append(items, mapped)
			}
		}
	case map[string]any:
		items = append(items, value)
	}
	rows := make([]Patch, 0, len(items))
	for _, item := range items {
		row := normalizePatch(item)
		if row.Title == "" && row.KB == "" {
			continue
		}
		rows = append(rows, row)
	}
	return dedupeAndSort(rows), nil
}

func normalizePatch(item map[string]any) Patch {
	metadata := normalizeMetadata(item["metadata"])
	kb := normalizeKB(firstValue(item, "kb", "hotfix_id", "kb_article_id", "kb_article_ids"))
	if kb == "" {
		kb = extractKB(cleanText(item["title"]))
	}
	state := normalizePatchState(item["state"])
	source := normalizePatchSource(item["source"])
	capturedAt := coerceInt64(item["captured_at"])
	if capturedAt <= 0 {
		capturedAt = time.Now().Unix()
	}
	row := Patch{
		KB:             kb,
		Title:          cleanText(item["title"]),
		State:          state,
		Source:         source,
		Classification: cleanText(item["classification"]),
		Severity:       cleanText(item["severity"]),
		InstalledOn:    coerceInt64(firstValue(item, "installed_on", "installed_at")),
		PublishedAt:    coerceInt64(firstValue(item, "published_at", "last_deployment_change_at")),
		CapturedAt:     capturedAt,
		Metadata:       metadata,
	}
	if row.State == "" {
		row.State = "pending"
	}
	if row.Source == "" {
		row.Source = "wua_pending"
	}
	row.PatchKey = patchRowKey(row)
	if len(row.Metadata) == 0 {
		row.Metadata = nil
	}
	return row
}

func dedupeAndSort(rows []Patch) []Patch {
	deduped := map[string]Patch{}
	for _, row := range rows {
		row.KB = normalizeKB(row.KB)
		if row.KB == "" {
			row.KB = extractKB(row.Title)
		}
		row.State = normalizePatchState(row.State)
		if row.State == "" {
			row.State = "pending"
		}
		row.Source = normalizePatchSource(row.Source)
		if row.Source == "" {
			row.Source = "wua_pending"
		}
		row.PatchKey = patchRowKey(row)
		if row.PatchKey == "" {
			continue
		}
		existing, found := deduped[row.PatchKey]
		if !found {
			deduped[row.PatchKey] = row
			continue
		}
		deduped[row.PatchKey] = mergePatchRows(existing, row)
	}
	out := make([]Patch, 0, len(deduped))
	for _, row := range deduped {
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.ToLower(out[i].State) + "\x00" + strings.ToLower(out[i].KB) + "\x00" + strings.ToLower(out[i].Title)
		right := strings.ToLower(out[j].State) + "\x00" + strings.ToLower(out[j].KB) + "\x00" + strings.ToLower(out[j].Title)
		return left < right
	})
	return out
}

func mergePatchRows(left Patch, right Patch) Patch {
	out := left
	if out.KB == "" {
		out.KB = right.KB
	}
	if len(right.Title) > len(out.Title) {
		out.Title = right.Title
	}
	if out.Classification == "" {
		out.Classification = right.Classification
	}
	if out.Severity == "" {
		out.Severity = right.Severity
	}
	if out.InstalledOn <= 0 {
		out.InstalledOn = right.InstalledOn
	}
	if out.PublishedAt <= 0 {
		out.PublishedAt = right.PublishedAt
	}
	if right.CapturedAt > out.CapturedAt {
		out.CapturedAt = right.CapturedAt
	}
	if out.Source == "wua_history" && right.Source == "quick_fix_engineering" {
		out.Source = right.Source
	}
	if out.Metadata == nil && right.Metadata != nil {
		out.Metadata = map[string]any{}
	}
	for key, value := range right.Metadata {
		if _, exists := out.Metadata[key]; !exists {
			out.Metadata[key] = value
		}
	}
	if out.Metadata != nil {
		out.Metadata["sources"] = mergeSourceList(out.Metadata["sources"], left.Source, right.Source)
	}
	return out
}

func mergeSourceList(existing any, values ...string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, item := range anySlice(existing) {
		text := normalizePatchSource(item)
		if text != "" && !seen[text] {
			seen[text] = true
			out = append(out, text)
		}
	}
	for _, value := range values {
		text := normalizePatchSource(value)
		if text != "" && !seen[text] {
			seen[text] = true
			out = append(out, text)
		}
	}
	sort.Strings(out)
	return out
}

func patchRowKey(row Patch) string {
	state := normalizePatchState(row.State)
	if state == "" {
		state = "pending"
	}
	if kb := normalizeKB(row.KB); kb != "" {
		return "kb:" + strings.ToUpper(kb) + ":state:" + state
	}
	metadata := row.Metadata
	updateID := cleanText(firstValue(metadata, "update_id", "updateID"))
	revision := cleanText(firstValue(metadata, "revision_number", "revision"))
	if updateID != "" {
		if revision != "" {
			return "update:" + strings.ToLower(updateID) + ":" + revision + ":state:" + state
		}
		return "update:" + strings.ToLower(updateID) + ":state:" + state
	}
	title := strings.ToLower(cleanText(row.Title))
	if title == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(title))
	return fmt.Sprintf("title:%x:state:%s", sum[:], state)
}

func normalizePatchState(value any) string {
	text := strings.ToLower(cleanText(value))
	switch text {
	case "pending", "available", "ready", "ready_to_install", "not_installed":
		return "pending"
	case "installed", "succeeded", "success":
		return "installed"
	default:
		return text
	}
}

func normalizePatchSource(value any) string {
	text := strings.ToLower(cleanText(value))
	switch text {
	case "wua", "windows_update", "windows_update_agent", "pending", "wua_pending":
		return "wua_pending"
	case "history", "wua_history", "windows_update_history":
		return "wua_history"
	case "qfe", "hotfix", "get_hotfix", "quickfixengineering", "quick_fix_engineering":
		return "quick_fix_engineering"
	default:
		return text
	}
}

func normalizeKB(value any) string {
	for _, item := range anySlice(value) {
		if kb := normalizeKBText(cleanText(item)); kb != "" {
			return kb
		}
	}
	return normalizeKBText(cleanText(value))
}

func normalizeKBText(value string) string {
	text := strings.ToUpper(strings.TrimSpace(value))
	if text == "" {
		return ""
	}
	if matches := kbPattern.FindString(text); matches != "" {
		return strings.ToUpper(matches)
	}
	digits := strings.TrimLeft(text, "KBkb")
	if digits != "" {
		allDigits := true
		for _, ch := range digits {
			if ch < '0' || ch > '9' {
				allDigits = false
				break
			}
		}
		if allDigits && len(digits) >= 4 && len(digits) <= 9 {
			return "KB" + digits
		}
	}
	return ""
}

func extractKB(title string) string {
	return normalizeKB(title)
}

func normalizeMetadata(value any) map[string]any {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for key, rawValue := range raw {
		cleanKey := cleanText(key)
		if cleanKey == "" || rawValue == nil {
			continue
		}
		switch typed := rawValue.(type) {
		case string:
			if cleanText(typed) != "" {
				out[cleanKey] = cleanText(typed)
			}
		case bool:
			out[cleanKey] = typed
		case float64:
			if typed != 0 {
				out[cleanKey] = typed
			}
		default:
			if cleanText(typed) != "" {
				out[cleanKey] = typed
			}
		}
	}
	return out
}

func commandError(result commandResult, err error) error {
	message := strings.TrimSpace(result.Stderr)
	if message == "" && err != nil {
		message = err.Error()
	}
	if message == "" {
		message = fmt.Sprintf("command exited with code %d", result.ExitCode)
	}
	return fmt.Errorf("%s", message)
}

func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, name, args...)
	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := commandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return result, err
	}
	if err != nil {
		result.ExitCode = -1
		return result, err
	}
	return result, nil
}

func errorResponse(code string, message string) map[string]any {
	return map[string]any{
		"ok":      false,
		"error":   code,
		"message": message,
	}
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if values == nil {
			return nil
		}
		if value, ok := values[key]; ok && value != nil {
			return value
		}
	}
	return nil
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func truthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "y", "ok", "accepted", "succeeded", "success":
			return true
		}
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case int64:
		return typed != 0
	}
	return false
}

func anySlice(value any) []any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		return typed
	case []string:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	default:
		text := cleanText(value)
		if text == "" {
			return []any{value}
		}
		if strings.Contains(text, ",") {
			parts := strings.Split(text, ",")
			out := make([]any, 0, len(parts))
			for _, part := range parts {
				if clean := cleanText(part); clean != "" {
					out = append(out, clean)
				}
			}
			return out
		}
		return []any{value}
	}
}

func coerceInt64(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
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
		text := cleanText(typed)
		if text == "" {
			return 0
		}
		parsed, _ := strconv.ParseInt(text, 10, 64)
		return parsed
	}
}

func cleanText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func fallbackText(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func windowsPatchInstallScript(request map[string]any) string {
	payload, _ := json.Marshal(request)
	encoded := base64.StdEncoding.EncodeToString(payload)
	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$request = ConvertFrom-Json ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')))
function Write-BorealisPatchResult {
  param($Payload)
  $Payload | ConvertTo-Json -Depth 8 -Compress
}
function Get-BorealisKB {
  param($Values, [string]$Title)
  foreach ($value in @($Values)) {
    $text = "$value".Trim()
    if (-not $text) { continue }
    if ($text -match '(?i)\bKB\d{4,9}\b') { return $Matches[0].ToUpperInvariant() }
    if ($text -match '^\d{4,9}$') { return "KB$text" }
  }
  if ($Title -match '(?i)\bKB\d{4,9}\b') { return $Matches[0].ToUpperInvariant() }
  return ''
}
function Test-BorealisPatchMatch {
  param($Update, $Request)
  $title = "$($Update.Title)".Trim()
  $requestedUpdateId = "$($Request.update_id)".Trim()
  $requestedRevision = "$($Request.revision_number)".Trim()
  if ($requestedUpdateId) {
    $candidateUpdateId = ''
    $candidateRevision = ''
    try { $candidateUpdateId = "$($Update.Identity.UpdateID)".Trim() } catch {}
    try { $candidateRevision = "$($Update.Identity.RevisionNumber)".Trim() } catch {}
    if ($candidateUpdateId -and $candidateUpdateId.Equals($requestedUpdateId, [StringComparison]::OrdinalIgnoreCase)) {
      if (-not $requestedRevision -or $candidateRevision -eq $requestedRevision) { return $true }
    }
    return $false
  }
  $requestedKB = "$($Request.kb)".Trim()
  if (-not $requestedKB) {
    $patchKey = "$($Request.patch_key)".Trim()
    if ($patchKey -match '(?i)\bKB\d{4,9}\b') { $requestedKB = $Matches[0].ToUpperInvariant() }
  }
  if ($requestedKB) {
    if ($requestedKB -match '^\d{4,9}$') { $requestedKB = "KB$requestedKB" }
    $candidateKB = Get-BorealisKB $Update.KBArticleIDs $title
    if ($candidateKB -and $candidateKB.Equals($requestedKB.ToUpperInvariant(), [StringComparison]::OrdinalIgnoreCase)) { return $true }
  }
  $requestedTitle = "$($Request.title)".Trim()
  if ($requestedTitle -and $title.Equals($requestedTitle, [StringComparison]::OrdinalIgnoreCase)) { return $true }
  return $false
}
try {
  $session = New-Object -ComObject Microsoft.Update.Session
  $searcher = $session.CreateUpdateSearcher()
  $searchResult = $searcher.Search("IsInstalled=0 and IsHidden=0")
  $matches = @()
  foreach ($update in @($searchResult.Updates)) {
    if (Test-BorealisPatchMatch $update $request) {
      $matches += $update
    }
  }
  if ($matches.Count -lt 1) {
    Write-BorealisPatchResult @{ ok = $false; error = 'update_not_found'; message = 'Matching Windows update was not found.' }
    exit 2
  }
  $collection = New-Object -ComObject Microsoft.Update.UpdateColl
  foreach ($update in $matches) {
    try {
      if (-not $update.EulaAccepted) { $update.AcceptEula() }
    } catch {}
    [void]$collection.Add($update)
  }
  $needsDownload = $false
  foreach ($update in $matches) {
    try {
      if (-not [bool]$update.IsDownloaded) { $needsDownload = $true }
    } catch {
      $needsDownload = $true
    }
  }
  if ($needsDownload) {
    $downloader = $session.CreateUpdateDownloader()
    $downloader.Updates = $collection
    [void]$downloader.Download()
  }
  $installCollection = New-Object -ComObject Microsoft.Update.UpdateColl
  foreach ($update in $matches) {
    $downloaded = $false
    try { $downloaded = [bool]$update.IsDownloaded } catch {}
    if ($downloaded) { [void]$installCollection.Add($update) }
  }
  if ($installCollection.Count -lt 1) {
    Write-BorealisPatchResult @{ ok = $false; error = 'download_failed'; message = 'Windows Update Agent did not download the selected update.' }
    exit 3
  }
  $installer = $session.CreateUpdateInstaller()
  $installer.Updates = $installCollection
  try { $installer.ForceQuiet = $true } catch {}
  try { $installer.AllowSourcePrompts = $false } catch {}
  $installResult = $installer.Install()
  $resultCode = [int]$installResult.ResultCode
  $ok = ($resultCode -eq 2 -or $resultCode -eq 3)
  $titles = @()
  foreach ($update in $matches) { $titles += "$($update.Title)".Trim() }
  Write-BorealisPatchResult @{
    ok = $ok
    status = $(if ($ok) { 'completed' } else { 'failed' })
    result_code = $resultCode
    reboot_required = [bool]$installResult.RebootRequired
    matched_count = [int]$matches.Count
    installed_count = [int]$installCollection.Count
    titles = $titles
  }
  if (-not $ok) { exit 4 }
} catch {
  Write-BorealisPatchResult @{ ok = $false; error = 'install_failed'; message = "$($_.Exception.Message)".Trim() }
  exit 5
}
`, encoded)
}

func windowsPatchInventoryScript() string {
	return `
$ErrorActionPreference = 'SilentlyContinue'
$now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$rows = @()
function Convert-BorealisUnixTime {
  param($Value)
  if ($null -eq $Value) { return 0 }
  try {
    $dt = [datetime]$Value
    if ($dt.Kind -eq [DateTimeKind]::Unspecified) {
      $dt = [DateTime]::SpecifyKind($dt, [DateTimeKind]::Local)
    }
    return [int64]([DateTimeOffset]$dt).ToUnixTimeSeconds()
  } catch {
    return 0
  }
}
function Get-BorealisKB {
  param($Values, [string]$Title)
  foreach ($value in @($Values)) {
    $text = "$value".Trim()
    if (-not $text) { continue }
    if ($text -match '(?i)\bKB\d{4,9}\b') { return $Matches[0].ToUpperInvariant() }
    if ($text -match '^\d{4,9}$') { return "KB$text" }
  }
  if ($Title -match '(?i)\bKB\d{4,9}\b') { return $Matches[0].ToUpperInvariant() }
  return ''
}
function Get-BorealisClassification {
  param($Update)
  try {
    foreach ($category in @($Update.Categories)) {
      $name = "$($category.Name)".Trim()
      if ($name) { return $name }
    }
  } catch {}
  return ''
}
try {
  $session = New-Object -ComObject Microsoft.Update.Session
  $searcher = $session.CreateUpdateSearcher()
  $searchResult = $searcher.Search("IsInstalled=0 and IsHidden=0")
  foreach ($update in @($searchResult.Updates)) {
    $title = "$($update.Title)".Trim()
    $kb = Get-BorealisKB $update.KBArticleIDs $title
    $metadata = [ordered]@{}
    try { $metadata.is_downloaded = [bool]$update.IsDownloaded } catch {}
    try { $metadata.is_mandatory = [bool]$update.IsMandatory } catch {}
    try { $metadata.requires_reboot = [bool]$update.RebootRequired } catch {}
    try { if ($update.Identity.UpdateID) { $metadata.update_id = "$($update.Identity.UpdateID)".Trim() } } catch {}
    try { if ($null -ne $update.Identity.RevisionNumber) { $metadata.revision_number = [int64]$update.Identity.RevisionNumber } } catch {}
    $rows += [pscustomobject]@{
      kb = $kb
      title = $title
      state = 'pending'
      source = 'wua_pending'
      classification = Get-BorealisClassification $update
      severity = "$($update.MsrcSeverity)".Trim()
      published_at = Convert-BorealisUnixTime $update.LastDeploymentChangeTime
      captured_at = $now
      metadata = $metadata
    }
  }
  try {
    $totalHistory = [int]$searcher.GetTotalHistoryCount()
    $historyCount = [Math]::Min($totalHistory, 2000)
    if ($historyCount -gt 0) {
      $history = $searcher.QueryHistory(0, $historyCount)
      foreach ($entry in @($history)) {
        if ([int]$entry.Operation -ne 1) { continue }
        if ([int]$entry.ResultCode -ne 2 -and [int]$entry.ResultCode -ne 3) { continue }
        $title = "$($entry.Title)".Trim()
        $kb = Get-BorealisKB @() $title
        if (-not $kb -and -not $title) { continue }
        $metadata = [ordered]@{}
        try { if ($entry.UpdateIdentity.UpdateID) { $metadata.update_id = "$($entry.UpdateIdentity.UpdateID)".Trim() } } catch {}
        try { if ($null -ne $entry.UpdateIdentity.RevisionNumber) { $metadata.revision_number = [int64]$entry.UpdateIdentity.RevisionNumber } } catch {}
        try { $metadata.wua_result_code = [int64]$entry.ResultCode } catch {}
        $rows += [pscustomobject]@{
          kb = $kb
          title = $title
          state = 'installed'
          source = 'wua_history'
          installed_on = Convert-BorealisUnixTime $entry.Date
          captured_at = $now
          metadata = $metadata
        }
      }
    }
  } catch {}
} catch {}
try {
  Get-HotFix -ErrorAction SilentlyContinue | ForEach-Object {
    $title = "$($_.Description)".Trim()
    $kb = Get-BorealisKB $_.HotFixID $title
    if (-not $kb -and -not $title) { return }
    $metadata = [ordered]@{}
    if ($_.InstalledBy) { $metadata.installed_by = "$($_.InstalledBy)".Trim() }
    if ($_.PSComputerName) { $metadata.computer_name = "$($_.PSComputerName)".Trim() }
    $rows += [pscustomobject]@{
      kb = $kb
      title = $(if ($title) { "$kb $title".Trim() } else { $kb })
      state = 'installed'
      source = 'quick_fix_engineering'
      classification = "$($_.Description)".Trim()
      installed_on = Convert-BorealisUnixTime $_.InstalledOn
      captured_at = $now
      metadata = $metadata
    }
  }
} catch {}
$rows | Sort-Object state,kb,title,source -Unique | ConvertTo-Json -Depth 8 -Compress
`
}
