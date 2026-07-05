package patchmanagement

import (
	"bufio"
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
	patchInstallQueueMax = 2 * time.Hour
	patchPolicyTimeout   = 45 * time.Second
	patchRebootTimeout   = 20 * time.Second
)

var kbPattern = regexp.MustCompile(`(?i)\bKB\d{4,9}\b`)

type Manager struct {
	authClient        *auth.Client
	hostname          string
	serviceMode       string
	runner            commandRunner
	installRunner     patchInstallCommandRunner
	progressPoster    patchProgressPoster
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
type patchInstallCommandRunner func(ctx context.Context, timeout time.Duration, request map[string]any, onProgress func(map[string]any)) (patchInstallResult, error)
type patchProgressPoster func(ctx context.Context, payload map[string]any) error

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
		installRunner:     runWindowsPatchInstallCommand,
	}
	manager.publisher = manager.publishPatches
	manager.progressPoster = manager.postPatchInstallProgress
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
	requestID := cleanText(body["request_id"])
	if requestID == "" {
		requestID = fmt.Sprintf("patch-%d", time.Now().UnixNano())
	}
	patch["request_id"] = requestID
	patch["requested_at"] = coerceInt64(body["requested_at"])
	patch["requested_by"] = cleanText(body["requested_by"])
	patch["scope"] = cleanText(body["scope"])
	if jobID := coerceInt64(firstValue(body, "scheduled_job_id", "job_id")); jobID > 0 {
		patch["scheduled_job_id"] = jobID
	}
	if runID := coerceInt64(firstValue(body, "scheduled_job_run_id", "run_id")); runID > 0 {
		patch["scheduled_job_run_id"] = runID
	}
	if targetHostname := cleanText(firstValue(body, "hostname", "target_hostname")); targetHostname != "" {
		patch["hostname"] = targetHostname
	}
	if agentID := cleanText(body["agent_id"]); agentID != "" {
		patch["agent_id"] = agentID
	}
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

func (m *Manager) HandlePolicyEnforcementRequest(ctx context.Context, payload any) (any, error) {
	body, ok := payload.(map[string]any)
	if !ok {
		return errorResponse("invalid_request", "Patch policy enforcement payload must be an object."), nil
	}
	if !m.matchesTarget(body) {
		return errorResponse("not_for_host", "The patch policy enforcement request targeted another device."), nil
	}
	if !m.supported {
		return errorResponse("unsupported_platform", fallbackText(m.unsupportedReason, "Patch management is unsupported on this platform.")), nil
	}
	mode := normalizePatchPolicyEnforcementMode(firstNonNil(firstValue(body, "enforcement_mode", "mode"), body["state"]))
	if mode == "" {
		return errorResponse("invalid_enforcement_mode", "Patch policy enforcement mode must be managed, frozen, or unmanaged."), nil
	}
	requestID := fallbackText(cleanText(body["request_id"]), fmt.Sprintf("patch-policy-%d", time.Now().UnixNano()))
	runner := m.runner
	if runner == nil {
		runner = runCommand
	}
	result, err := runner(ctx, patchPolicyTimeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", windowsPatchPolicyEnforcementScript(mode, requestID))
	parsed := parseLastJSONObject(result.Stdout)
	response := map[string]any{
		"ok":             err == nil && result.ExitCode == 0,
		"status":         "applied",
		"request_id":     requestID,
		"mode":           mode,
		"stdout":         result.Stdout,
		"stderr":         result.Stderr,
		"exit_code":      result.ExitCode,
		"drift_detected": false,
	}
	for key, value := range parsed {
		response[key] = value
	}
	if err != nil || result.ExitCode != 0 {
		response["ok"] = false
		response["status"] = "failed"
		response["error"] = "patch_policy_enforcement_failed"
		response["message"] = commandError(result, err).Error()
	}
	return response, nil
}

func (m *Manager) HandleRebootRequest(ctx context.Context, payload any) (any, error) {
	body, ok := payload.(map[string]any)
	if !ok {
		return errorResponse("invalid_request", "Patch reboot payload must be an object."), nil
	}
	if !m.matchesTarget(body) {
		return errorResponse("not_for_host", "The patch reboot request targeted another device."), nil
	}
	if !m.supported {
		return errorResponse("unsupported_platform", fallbackText(m.unsupportedReason, "Patch management is unsupported on this platform.")), nil
	}
	requestID := fallbackText(cleanText(body["request_id"]), fmt.Sprintf("patch-reboot-%d", time.Now().UnixNano()))
	delaySeconds := coerceInt64(firstNonNil(firstValue(body, "delay_seconds", "delay"), int64(60)))
	if delaySeconds < 30 {
		delaySeconds = 30
	}
	if delaySeconds > 3600 {
		delaySeconds = 3600
	}
	force := truthy(firstNonNil(firstValue(body, "force_logged_in_user", "force"), false))
	runner := m.runner
	if runner == nil {
		runner = runCommand
	}
	result, err := runner(ctx, patchRebootTimeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", windowsPatchRebootScript(requestID, delaySeconds, force))
	parsed := parseLastJSONObject(result.Stdout)
	response := map[string]any{
		"ok":            err == nil && result.ExitCode == 0,
		"status":        "scheduled",
		"request_id":    requestID,
		"delay_seconds": delaySeconds,
		"force":         force,
		"stdout":        result.Stdout,
		"stderr":        result.Stderr,
		"exit_code":     result.ExitCode,
	}
	for key, value := range parsed {
		response[key] = value
	}
	if err != nil || result.ExitCode != 0 {
		response["ok"] = false
		response["status"] = "failed"
		response["error"] = "patch_reboot_failed"
		response["message"] = commandError(result, err).Error()
	}
	return response, nil
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

func (m *Manager) waitForInstallSlot(ctx context.Context, patch map[string]any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, patchInstallQueueMax)
		defer cancel()
	}
	waiting := false
	lastProgressAt := time.Time{}
	for {
		m.mu.Lock()
		if !m.installRunning {
			m.installRunning = true
			m.lastInstallError = ""
			m.mu.Unlock()
			if waiting {
				m.queuePatchInstallProgress(patch, map[string]any{
					"phase":       "prepare",
					"percent":     int64(0),
					"message":     "Starting queued patch install.",
					"captured_at": time.Now().Unix(),
				})
			}
			return nil
		}
		m.mu.Unlock()
		now := time.Now()
		if !waiting || now.Sub(lastProgressAt) >= 30*time.Second {
			waiting = true
			lastProgressAt = now
			m.queuePatchInstallProgress(patch, map[string]any{
				"phase":       "prepare",
				"percent":     int64(0),
				"message":     "Waiting for current patch install to finish before starting this update.",
				"captured_at": now.Unix(),
			})
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("timed out waiting for current patch install to finish: %w", ctx.Err())
		case <-timer.C:
		}
	}
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
	err := m.waitForInstallSlot(ctx, patch)
	if err == nil {
		_, err = m.installPatch(ctx, patch)
		m.finishInstall(err)
	}
	m.RequestRefresh("patch_install_complete:" + fallbackText(requestID, "unknown"))
}

func (m *Manager) runInstallSync(ctx context.Context, patch map[string]any, requestID string) map[string]any {
	if err := m.waitForInstallSlot(ctx, patch); err != nil {
		return map[string]any{
			"ok":         false,
			"status":     "failed",
			"error":      "install_wait_failed",
			"message":    err.Error(),
			"request_id": requestID,
			"patch_key":  cleanText(patch["patch_key"]),
			"kb":         cleanText(patch["kb"]),
			"title":      cleanText(patch["title"]),
		}
	}
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
		for _, key := range []string{"result_code", "result_code_name", "reboot_required", "reboot_required_before_install", "matched_count", "installed_count", "already_installed"} {
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
	runner := m.installRunner
	if runner == nil {
		runner = runWindowsPatchInstallCommand
	}
	installResult, err := runner(ctx, patchInstallTimeout, patch, func(progress map[string]any) {
		m.queuePatchInstallProgress(patch, progress)
	})
	if parsed := installResult.Parsed; parsed != nil {
		if okValue, exists := parsed["ok"]; exists && !truthy(okValue) {
			message := fallbackText(cleanText(parsed["message"]), fallbackText(cleanText(parsed["error"]), "patch install failed"))
			return installResult, fmt.Errorf("%s", message)
		}
	}
	if err != nil || installResult.ExitCode != 0 {
		return installResult, commandError(commandResult{Stdout: installResult.Stdout, Stderr: installResult.Stderr, ExitCode: installResult.ExitCode}, err)
	}
	return installResult, nil
}

func (m *Manager) queuePatchInstallProgress(patch map[string]any, progress map[string]any) {
	if m == nil || len(progress) == 0 || m.progressPoster == nil {
		return
	}
	payload := patchInstallProgressPayload(m.hostname, patch, progress)
	if len(payload) == 0 {
		return
	}
	poster := m.progressPoster
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = poster(ctx, payload)
	}()
}

func (m *Manager) postPatchInstallProgress(ctx context.Context, payload map[string]any) error {
	if m == nil || m.authClient == nil {
		return nil
	}
	_, err := m.authClient.PostJSON(ctx, "/api/agent/patches/install-progress", payload, nil)
	return err
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

func normalizePatchPolicyEnforcementMode(value any) string {
	switch strings.ToLower(cleanText(value)) {
	case "managed", "lock", "locked", "enforced":
		return "managed"
	case "frozen", "freeze":
		return "frozen"
	case "unmanaged", "disabled", "off", "none":
		return "unmanaged"
	default:
		return ""
	}
}

func parseLastJSONObject(output string) map[string]any {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err == nil {
			return payload
		}
	}
	return nil
}

func windowsPatchPolicyEnforcementScript(mode string, requestID string) string {
	payload, _ := json.Marshal(map[string]any{
		"mode":       normalizePatchPolicyEnforcementMode(mode),
		"request_id": requestID,
	})
	encoded := base64.StdEncoding.EncodeToString(payload)
	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')) | ConvertFrom-Json
$Mode = [string]$payload.mode
$RequestID = [string]$payload.request_id
$BorealisPath = 'HKLM:\SOFTWARE\Borealis\PatchManagement'
$WUPath = 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate'
$AUPath = Join-Path $WUPath 'AU'
function Ensure-Key([string]$Path) {
  if (-not (Test-Path $Path)) { New-Item -Path $Path -Force | Out-Null }
}
function Read-Value([string]$Path, [string]$Name) {
  if (-not (Test-Path $Path)) { return $null }
  try { return (Get-ItemProperty -Path $Path -Name $Name -ErrorAction Stop).$Name } catch { return $null }
}
function Backup-Value([string]$SourcePath, [string]$Name, [string]$BackupName) {
  Ensure-Key $BorealisPath
  $current = Read-Value $SourcePath $Name
  if ($null -eq $current) {
    New-ItemProperty -Path $BorealisPath -Name ($BackupName + '_WasPresent') -Value 0 -PropertyType DWord -Force | Out-Null
  } else {
    New-ItemProperty -Path $BorealisPath -Name ($BackupName + '_WasPresent') -Value 1 -PropertyType DWord -Force | Out-Null
    New-ItemProperty -Path $BorealisPath -Name $BackupName -Value ([int]$current) -PropertyType DWord -Force | Out-Null
  }
}
function Restore-Value([string]$TargetPath, [string]$Name, [string]$BackupName) {
  Ensure-Key $TargetPath
  $wasPresent = Read-Value $BorealisPath ($BackupName + '_WasPresent')
  if ($wasPresent -eq 1) {
    $backup = Read-Value $BorealisPath $BackupName
    New-ItemProperty -Path $TargetPath -Name $Name -Value ([int]$backup) -PropertyType DWord -Force | Out-Null
  } else {
    Remove-ItemProperty -Path $TargetPath -Name $Name -ErrorAction SilentlyContinue
  }
}
Ensure-Key $WUPath
Ensure-Key $AUPath
Ensure-Key $BorealisPath
$previousMode = Read-Value $BorealisPath 'Mode'
if ($Mode -eq 'managed' -or $Mode -eq 'frozen') {
  Backup-Value $AUPath 'NoAutoUpdate' 'AU_NoAutoUpdate'
  Backup-Value $AUPath 'AUOptions' 'AU_AUOptions'
  Backup-Value $WUPath 'SetDisableUXWUAccess' 'WU_SetDisableUXWUAccess'
  New-ItemProperty -Path $AUPath -Name 'NoAutoUpdate' -Value 1 -PropertyType DWord -Force | Out-Null
  New-ItemProperty -Path $AUPath -Name 'AUOptions' -Value 2 -PropertyType DWord -Force | Out-Null
  if ($Mode -eq 'frozen') {
    New-ItemProperty -Path $WUPath -Name 'SetDisableUXWUAccess' -Value 1 -PropertyType DWord -Force | Out-Null
  } else {
    Remove-ItemProperty -Path $WUPath -Name 'SetDisableUXWUAccess' -ErrorAction SilentlyContinue
  }
  New-ItemProperty -Path $BorealisPath -Name 'ManagedBy' -Value 'Borealis' -PropertyType String -Force | Out-Null
  New-ItemProperty -Path $BorealisPath -Name 'Mode' -Value $Mode -PropertyType String -Force | Out-Null
} elseif ($Mode -eq 'unmanaged') {
  if ((Read-Value $BorealisPath 'ManagedBy') -eq 'Borealis') {
    Restore-Value $AUPath 'NoAutoUpdate' 'AU_NoAutoUpdate'
    Restore-Value $AUPath 'AUOptions' 'AU_AUOptions'
    Restore-Value $WUPath 'SetDisableUXWUAccess' 'WU_SetDisableUXWUAccess'
  }
  New-ItemProperty -Path $BorealisPath -Name 'Mode' -Value 'unmanaged' -PropertyType String -Force | Out-Null
}
$result = [ordered]@{
  ok = $true
  status = 'applied'
  request_id = $RequestID
  mode = $Mode
  previous_mode = [string]$previousMode
  no_auto_update = Read-Value $AUPath 'NoAutoUpdate'
  au_options = Read-Value $AUPath 'AUOptions'
  disable_ux = Read-Value $WUPath 'SetDisableUXWUAccess'
  drift_detected = $false
}
$result | ConvertTo-Json -Compress
`, encoded)
}

func windowsPatchRebootScript(requestID string, delaySeconds int64, force bool) string {
	payload, _ := json.Marshal(map[string]any{
		"request_id":    requestID,
		"delay_seconds": delaySeconds,
		"force":         force,
	})
	encoded := base64.StdEncoding.EncodeToString(payload)
	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')) | ConvertFrom-Json
$RequestID = [string]$payload.request_id
$DelaySeconds = [int]$payload.delay_seconds
$Force = [bool]$payload.force
$LoggedInUser = $null
try {
  $LoggedInUser = (Get-CimInstance -ClassName Win32_ComputerSystem -ErrorAction Stop).UserName
} catch {
  $LoggedInUser = $null
}
if ($LoggedInUser -and -not $Force) {
  [ordered]@{
    ok = $true
    status = 'skipped_logged_in_user'
    request_id = $RequestID
    logged_in_user = $LoggedInUser
    scheduled = $false
  } | ConvertTo-Json -Compress
  exit 0
}
$comment = 'Borealis Patch Management reboot ' + $RequestID
& shutdown.exe /r /t $DelaySeconds /c $comment /d p:2:17
[ordered]@{
  ok = $true
  status = 'scheduled'
  request_id = $RequestID
  delay_seconds = $DelaySeconds
  force = $Force
  logged_in_user = [string]$LoggedInUser
  scheduled = $true
} | ConvertTo-Json -Compress
`, encoded)
}

func parsePatchInstallResult(output string) map[string]any {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if payload := parsePatchInstallResultLine(lines[i]); payload != nil {
			return payload
		}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil
	}
	if isPatchInstallResultPayload(payload) {
		return payload
	}
	return nil
}

func parsePatchInstallResultLine(line string) map[string]any {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return nil
	}
	if !isPatchInstallResultPayload(payload) {
		return nil
	}
	delete(payload, "kind")
	return payload
}

func isPatchInstallResultPayload(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	kind := strings.ToLower(cleanText(payload["kind"]))
	if kind == "result" {
		return true
	}
	if kind != "" && kind != "final" {
		return false
	}
	if _, ok := payload["ok"]; ok {
		return true
	}
	status := strings.ToLower(cleanText(payload["status"]))
	return stringInSet(status, "completed", "failed", "success", "succeeded")
}

func parsePatchInstallProgressLine(line string) map[string]any {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return nil
	}
	kind := strings.ToLower(cleanText(payload["kind"]))
	phase := normalizePatchInstallProgressPhase(payload["phase"])
	if kind != "progress" && phase == "" {
		return nil
	}
	if phase == "" {
		phase = "install"
	}
	out := map[string]any{
		"kind":  "progress",
		"phase": phase,
	}
	for _, key := range []string{"request_id", "scheduled_job_id", "scheduled_job_run_id", "hostname", "agent_id", "patch_key", "kb", "title", "update_id", "revision_number", "message"} {
		if value := firstValue(payload, key); value != nil && cleanText(value) != "" {
			out[key] = value
		}
	}
	for _, key := range []string{"percent", "current_update_index", "current_update_percent", "captured_at"} {
		if value := coerceInt64(payload[key]); value > 0 || key == "percent" {
			out[key] = value
		}
	}
	if stdout := cleanText(payload["stdout"]); stdout != "" {
		out["stdout"] = stdout
	}
	if stderr := cleanText(payload["stderr"]); stderr != "" {
		out["stderr"] = stderr
	}
	return out
}

func normalizePatchInstallProgressPhase(value any) string {
	switch strings.ToLower(cleanText(value)) {
	case "download", "downloading":
		return "download"
	case "install", "installing":
		return "install"
	case "search", "searching", "prepare", "preparing":
		return "prepare"
	case "finalize", "finalizing":
		return "finalize"
	default:
		return ""
	}
}

func patchInstallProgressPayload(hostname string, patch map[string]any, progress map[string]any) map[string]any {
	if len(progress) == 0 {
		return nil
	}
	payload := map[string]any{}
	copyIfPresent := func(dstKey string, values ...any) {
		for _, value := range values {
			if cleanText(value) != "" {
				payload[dstKey] = value
				return
			}
		}
	}
	copyIfInt := func(dstKey string, values ...any) {
		for _, value := range values {
			if number := coerceInt64(value); number > 0 || dstKey == "percent" {
				payload[dstKey] = number
				return
			}
		}
	}
	copyIfPresent("request_id", progress["request_id"], patch["request_id"])
	copyIfInt("scheduled_job_id", progress["scheduled_job_id"], patch["scheduled_job_id"])
	copyIfInt("scheduled_job_run_id", progress["scheduled_job_run_id"], patch["scheduled_job_run_id"])
	copyIfPresent("hostname", progress["hostname"], patch["hostname"], hostname)
	copyIfPresent("agent_id", progress["agent_id"], patch["agent_id"])
	copyIfPresent("patch_key", progress["patch_key"], patch["patch_key"])
	copyIfPresent("kb", progress["kb"], patch["kb"])
	copyIfPresent("title", progress["title"], patch["title"])
	copyIfPresent("update_id", progress["update_id"], patch["update_id"])
	copyIfInt("revision_number", progress["revision_number"], patch["revision_number"])
	copyIfPresent("phase", normalizePatchInstallProgressPhase(progress["phase"]))
	if payload["phase"] == nil {
		payload["phase"] = "install"
	}
	copyIfInt("percent", progress["percent"])
	copyIfInt("current_update_index", progress["current_update_index"])
	copyIfInt("current_update_percent", progress["current_update_percent"])
	copyIfPresent("message", progress["message"])
	copyIfPresent("stdout", progress["stdout"])
	copyIfPresent("stderr", progress["stderr"])
	copyIfInt("captured_at", progress["captured_at"])
	if payload["captured_at"] == nil {
		payload["captured_at"] = time.Now().Unix()
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

func runWindowsPatchInstallCommand(ctx context.Context, timeout time.Duration, request map[string]any, onProgress func(map[string]any)) (patchInstallResult, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", windowsPatchInstallScript(request))
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return patchInstallResult{ExitCode: -1}, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return patchInstallResult{ExitCode: -1}, err
	}
	if err := cmd.Start(); err != nil {
		return patchInstallResult{ExitCode: -1}, err
	}

	var stdout strings.Builder
	var stderr strings.Builder
	var parsed map[string]any
	stdoutDone := make(chan error, 1)
	stderrDone := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for scanner.Scan() {
			line := scanner.Text()
			stdout.WriteString(line)
			stdout.WriteString("\n")
			if progress := parsePatchInstallProgressLine(line); progress != nil && onProgress != nil {
				onProgress(progress)
			}
			if result := parsePatchInstallResultLine(line); result != nil {
				parsed = result
			}
		}
		stdoutDone <- scanner.Err()
	}()
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for scanner.Scan() {
			stderr.WriteString(scanner.Text())
			stderr.WriteString("\n")
		}
		stderrDone <- scanner.Err()
	}()

	waitErr := cmd.Wait()
	stdoutErr := <-stdoutDone
	stderrErr := <-stderrDone
	exitCode := 0
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if waitErr != nil {
		exitCode = -1
	}
	result := patchInstallResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Parsed:   parsed,
	}
	if result.Parsed == nil {
		result.Parsed = parsePatchInstallResult(result.Stdout)
	}
	if stdoutErr != nil && waitErr == nil {
		waitErr = stdoutErr
	}
	if stderrErr != nil && waitErr == nil {
		waitErr = stderrErr
	}
	return result, waitErr
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

func stringInSet(value string, candidates ...string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range candidates {
		if value == strings.ToLower(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func windowsPatchInstallScript(request map[string]any) string {
	payload, _ := json.Marshal(request)
	encoded := base64.StdEncoding.EncodeToString(payload)
	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$request = ConvertFrom-Json ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')))
function Write-BorealisPatchPayload {
  param($Payload)
  if (-not $Payload.ContainsKey('captured_at') -or $null -eq $Payload['captured_at']) {
    try { $Payload['captured_at'] = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds() } catch {}
  }
  [Console]::Out.WriteLine(($Payload | ConvertTo-Json -Depth 8 -Compress))
  [Console]::Out.Flush()
}
function Write-BorealisPatchResult {
  param($Payload)
  $Payload['kind'] = 'result'
  Write-BorealisPatchPayload $Payload
}
function Get-BorealisErrorMessage {
  param($ErrorRecord)
  $message = ''
  try { $message = "$($ErrorRecord.Exception.Message)".Trim() } catch {}
  if (-not $message) { $message = "$ErrorRecord".Trim() }
  $hresult = ''
  try {
    $code = [int64]$ErrorRecord.Exception.HResult
    if ($code -ne 0) {
      if ($code -lt 0) { $code = $code + 4294967296 }
      $hresult = ('0x{0:X8}' -f $code)
    }
  } catch {}
  if ($hresult) { return "$message (HRESULT $hresult)" }
  return $message
}
$script:BorealisLastProgress = @{}
function Write-BorealisPatchProgress {
  param([string]$Phase, $Job, [string]$Message, [bool]$Force, [int]$PercentOverride = -1, [string]$Diagnostic = '')
  $progress = $null
  try { if ($null -ne $Job) { $progress = $Job.GetProgress() } } catch {}
  $percent = 0
  $currentIndex = 0
  $currentPercent = 0
  try { $percent = [int]$progress.PercentComplete } catch {}
  try { $currentIndex = [int]$progress.CurrentUpdateIndex } catch {}
  try { $currentPercent = [int]$progress.CurrentUpdatePercentComplete } catch {}
  if ($PercentOverride -ge 0) { $percent = $PercentOverride; $currentPercent = $PercentOverride }
  if ($percent -lt 0) { $percent = 0 }
  if ($percent -gt 100) { $percent = 100 }
  if ($currentPercent -lt 0) { $currentPercent = 0 }
  if ($currentPercent -gt 100) { $currentPercent = 100 }
  $nowMs = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
  $signature = "$Phase|$percent|$currentIndex|$currentPercent|$Message"
  $previous = $script:BorealisLastProgress[$Phase]
  if (-not $Force -and $null -ne $previous -and $previous.signature -eq $signature -and (($nowMs - [int64]$previous.at) -lt 15000)) {
    return
  }
  $script:BorealisLastProgress[$Phase] = @{ signature = $signature; at = $nowMs }
  $payload = @{
    kind = 'progress'
    request_id = "$($request.request_id)".Trim()
    scheduled_job_id = $request.scheduled_job_id
    scheduled_job_run_id = $request.scheduled_job_run_id
    hostname = "$($request.hostname)".Trim()
    agent_id = "$($request.agent_id)".Trim()
    patch_key = "$($request.patch_key)".Trim()
    kb = "$($request.kb)".Trim()
    title = "$($request.title)".Trim()
    update_id = "$($request.update_id)".Trim()
    revision_number = $request.revision_number
    phase = $Phase
    percent = $percent
    current_update_index = $currentIndex
    current_update_percent = $currentPercent
    message = $Message
  }
  if ($Diagnostic) { $payload['stderr'] = $Diagnostic }
  Write-BorealisPatchPayload $payload
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
function Test-BorealisKBMatch {
  param($Values, [string]$Title, [string]$RequestedKB)
  $expected = Get-BorealisKB @($RequestedKB) ''
  if (-not $expected) { return $false }
  foreach ($value in @($Values)) {
    $candidate = Get-BorealisKB @($value) ''
    if ($candidate -and $candidate.Equals($expected, [StringComparison]::OrdinalIgnoreCase)) { return $true }
  }
  foreach ($match in [regex]::Matches("$Title", '(?i)\bKB\d{4,9}\b')) {
    $candidate = Get-BorealisKB @($match.Value) ''
    if ($candidate -and $candidate.Equals($expected, [StringComparison]::OrdinalIgnoreCase)) { return $true }
  }
  return $false
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
  }
  $requestedKB = "$($Request.kb)".Trim()
  if (-not $requestedKB) {
    $patchKey = "$($Request.patch_key)".Trim()
    if ($patchKey -match '(?i)\bKB\d{4,9}\b') { $requestedKB = $Matches[0].ToUpperInvariant() }
  }
  if ($requestedKB) {
    if (Test-BorealisKBMatch $Update.KBArticleIDs $title $requestedKB) { return $true }
  }
  $requestedTitle = "$($Request.title)".Trim()
  if ($requestedTitle -and $title.Equals($requestedTitle, [StringComparison]::OrdinalIgnoreCase)) { return $true }
  return $false
}
function Test-BorealisHistoryMatch {
  param($Entry, $Request)
  $title = "$($Entry.Title)".Trim()
  $requestedUpdateId = "$($Request.update_id)".Trim()
  $requestedRevision = "$($Request.revision_number)".Trim()
  if ($requestedUpdateId) {
    $candidateUpdateId = ''
    $candidateRevision = ''
    try { $candidateUpdateId = "$($Entry.UpdateIdentity.UpdateID)".Trim() } catch {}
    try { $candidateRevision = "$($Entry.UpdateIdentity.RevisionNumber)".Trim() } catch {}
    if ($candidateUpdateId -and $candidateUpdateId.Equals($requestedUpdateId, [StringComparison]::OrdinalIgnoreCase)) {
      if (-not $requestedRevision -or $candidateRevision -eq $requestedRevision) { return $true }
    }
  }
  $requestedTitle = "$($Request.title)".Trim()
  if ($requestedTitle -and $title.Equals($requestedTitle, [StringComparison]::OrdinalIgnoreCase)) { return $true }
  return $false
}
function Get-BorealisOperationResultName {
  param($Code)
  switch ([int]$Code) {
    0 { return 'NotStarted' }
    1 { return 'InProgress' }
    2 { return 'Succeeded' }
    3 { return 'SucceededWithErrors' }
    4 { return 'Failed' }
    5 { return 'Aborted' }
    default { return 'Unknown' }
  }
}
function Get-BorealisUpdateSummary {
  param($Update)
  $item = [ordered]@{}
  try { $item['title'] = "$($Update.Title)".Trim() } catch {}
  try {
    $kb = Get-BorealisKB $Update.KBArticleIDs $item['title']
    if ($kb) { $item['kb'] = $kb }
  } catch {}
  try { if ($Update.Identity.UpdateID) { $item['update_id'] = "$($Update.Identity.UpdateID)".Trim() } } catch {}
  try { if ($null -ne $Update.Identity.RevisionNumber) { $item['revision_number'] = [int64]$Update.Identity.RevisionNumber } } catch {}
  return $item
}
function Get-BorealisUpdateResultSummaries {
  param($InstallResult, $UpdateCollection)
  $items = @()
  $successCount = 0
  for ($index = 0; $index -lt [int]$UpdateCollection.Count; $index++) {
    $update = $null
    try { $update = $UpdateCollection.Item($index) } catch {}
    $item = Get-BorealisUpdateSummary $update
    $item['index'] = $index
    try {
      $updateResult = $InstallResult.GetUpdateResult($index)
      $code = [int]$updateResult.ResultCode
      $item['result_code'] = $code
      $item['result_code_name'] = Get-BorealisOperationResultName $code
      try {
        $hresult = [int64]$updateResult.HResult
        if ($hresult -ne 0) {
          if ($hresult -lt 0) { $hresult = $hresult + 4294967296 }
          $item['hresult'] = ('0x{0:X8}' -f $hresult)
        }
      } catch {}
      try { $item['reboot_required'] = [bool]$updateResult.RebootRequired } catch {}
      if ($code -eq 2 -or $code -eq 3) { $successCount += 1 }
    } catch {}
    $items += [pscustomobject]$item
  }
  return [pscustomobject]@{ items = $items; success_count = $successCount }
}
function Get-BorealisMatchingInstalledUpdates {
  param($Searcher, $Request)
  $matches = @()
  try {
    $installedResult = $Searcher.Search("IsInstalled=1")
    foreach ($update in @($installedResult.Updates)) {
      if (Test-BorealisPatchMatch $update $Request) { $matches += $update }
    }
  } catch {}
  return $matches
}
function Get-BorealisMatchingInstalledHistory {
  param($Searcher, $Request)
  $matches = @()
  try {
    $totalHistory = [int]$Searcher.GetTotalHistoryCount()
    $historyCount = [Math]::Min($totalHistory, 2000)
    if ($historyCount -gt 0) {
      $history = $Searcher.QueryHistory(0, $historyCount)
      foreach ($entry in @($history)) {
        if ([int]$entry.Operation -ne 1) { continue }
        if ([int]$entry.ResultCode -ne 2 -and [int]$entry.ResultCode -ne 3) { continue }
        if (Test-BorealisHistoryMatch $entry $Request) { $matches += $entry }
      }
    }
  } catch {}
  return $matches
}
function Wait-BorealisWUAIdle {
  param($Installer, [string]$Phase)
  $deadline = [DateTimeOffset]::UtcNow.AddMinutes(30)
  $lastProgress = [DateTimeOffset]::MinValue
  while ($true) {
    $busy = $false
    try { $busy = [bool]$Installer.IsBusy } catch { return $true }
    if (-not $busy) { return $true }
    $now = [DateTimeOffset]::UtcNow
    if ($now -gt $deadline) { return $false }
    if (($now - $lastProgress).TotalSeconds -ge 30) {
      $lastProgress = $now
      Write-BorealisPatchProgress $Phase $null 'Waiting for Windows Update Agent to become available.' $true
    }
    Start-Sleep -Seconds 5
  }
}
try {
  Write-BorealisPatchProgress 'prepare' $null 'Searching Windows Update Agent for matching update.' $true
  $script:BorealisPatchPhase = 'search'
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
    $installedMatches = @(Get-BorealisMatchingInstalledUpdates $searcher $request)
    $historyMatches = @(Get-BorealisMatchingInstalledHistory $searcher $request)
    if ($installedMatches.Count -gt 0 -or $historyMatches.Count -gt 0) {
      $titles = @()
      foreach ($update in $installedMatches) {
        try { $titles += "$($update.Title)".Trim() } catch {}
      }
      foreach ($entry in $historyMatches) {
        try {
          $entryTitle = "$($entry.Title)".Trim()
          if ($entryTitle -and -not $titles.Contains($entryTitle)) { $titles += $entryTitle }
        } catch {}
      }
      Write-BorealisPatchProgress 'finalize' $null 'Selected update is already installed or no longer applicable.' $true 100
      Write-BorealisPatchResult @{
        ok = $true
        status = 'completed'
        result_code = 2
        result_code_name = (Get-BorealisOperationResultName 2)
        reboot_required = $false
        already_installed = $true
        matched_count = 0
        installed_count = 0
        installed_match_count = [int]$installedMatches.Count
        history_match_count = [int]$historyMatches.Count
        titles = $titles
      }
      exit 0
    }
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
    $script:BorealisPatchPhase = 'download'
    $busyProbe = $session.CreateUpdateInstaller()
    if (-not (Wait-BorealisWUAIdle $busyProbe 'download')) {
      Write-BorealisPatchResult @{ ok = $false; error = 'wua_busy_timeout'; phase = 'download'; message = 'Windows Update Agent stayed busy before download.' }
      exit 6
    }
    $downloadState = "$($request.request_id)".Trim()
    Write-BorealisPatchProgress 'download' $null 'Downloading selected update.' $true
    $downloadJob = $null
    $downloadResult = $null
    try {
      try {
        $downloadJob = $downloader.BeginDownload($null, $null, $downloadState)
      } catch {
        $asyncError = Get-BorealisErrorMessage $_
        Write-BorealisPatchProgress 'download' $null 'Using synchronous WUA download path.' $true -1 $asyncError
        $downloadResult = $downloader.Download()
      }
      if ($null -ne $downloadJob) {
        while (-not [bool]$downloadJob.IsCompleted) {
          Write-BorealisPatchProgress 'download' $downloadJob 'Downloading selected update.' $false
          Start-Sleep -Seconds 2
        }
        Write-BorealisPatchProgress 'download' $downloadJob 'Download complete.' $true
        $downloadResult = $downloader.EndDownload($downloadJob)
      } else {
        Write-BorealisPatchProgress 'download' $null 'Download complete.' $true 100
      }
    } finally {
      try { if ($null -ne $downloadJob) { $downloadJob.CleanUp() } } catch {}
    }
    $downloadResultCode = 0
    try { $downloadResultCode = [int]$downloadResult.ResultCode } catch {}
    if ($downloadResultCode -ne 2 -and $downloadResultCode -ne 3) {
      Write-BorealisPatchResult @{ ok = $false; error = 'download_failed'; phase = 'download'; result_code = $downloadResultCode; message = 'Windows Update Agent did not download the selected update.' }
      exit 3
    }
  } else {
    Write-BorealisPatchProgress 'download' $null 'Selected update already downloaded.' $true 100
  }
  $installCollection = New-Object -ComObject Microsoft.Update.UpdateColl
  foreach ($update in $matches) {
    [void]$installCollection.Add($update)
  }
  if ($installCollection.Count -lt 1) {
    Write-BorealisPatchResult @{ ok = $false; error = 'download_failed'; message = 'Windows Update Agent did not download the selected update.' }
    exit 3
  }
  $installer = $session.CreateUpdateInstaller()
  $installer.Updates = $installCollection
  try { $installer.ForceQuiet = $true } catch {}
  try { $installer.AllowSourcePrompts = $false } catch {}
  $script:BorealisPatchPhase = 'install'
  $rebootRequiredBeforeInstall = $false
  try { $rebootRequiredBeforeInstall = [bool]$installer.RebootRequiredBeforeInstallation } catch {}
  if (-not (Wait-BorealisWUAIdle $installer 'install')) {
    Write-BorealisPatchResult @{ ok = $false; error = 'wua_busy_timeout'; phase = 'install'; message = 'Windows Update Agent stayed busy before install.' }
    exit 6
  }
  $installState = "$($request.request_id)".Trim()
  Write-BorealisPatchProgress 'install' $null 'Installing selected update.' $true
  $installJob = $null
  $installResult = $null
  try {
    try {
      $installJob = $installer.BeginInstall($null, $null, $installState)
    } catch {
      $asyncError = Get-BorealisErrorMessage $_
      Write-BorealisPatchProgress 'install' $null 'Using synchronous WUA install path.' $true -1 $asyncError
      $installResult = $installer.Install()
    }
    if ($null -ne $installJob) {
      while (-not [bool]$installJob.IsCompleted) {
        Write-BorealisPatchProgress 'install' $installJob 'Installing selected update.' $false
        Start-Sleep -Seconds 2
      }
      Write-BorealisPatchProgress 'install' $installJob 'Install complete.' $true
      $installResult = $installer.EndInstall($installJob)
    } else {
      Write-BorealisPatchProgress 'install' $null 'Install complete.' $true 100
    }
  } finally {
    try { if ($null -ne $installJob) { $installJob.CleanUp() } } catch {}
  }
  $resultCode = [int]$installResult.ResultCode
  $updateResults = Get-BorealisUpdateResultSummaries $installResult $installCollection
  $installedCount = [int]$updateResults.success_count
  if ($installedCount -le 0 -and ($resultCode -eq 2 -or $resultCode -eq 3)) {
    $installedCount = [int]$installCollection.Count
  }
  $ok = ($resultCode -eq 2 -or ($resultCode -eq 3 -and $installedCount -gt 0))
  $titles = @()
  foreach ($update in $matches) { $titles += "$($update.Title)".Trim() }
  Write-BorealisPatchResult @{
    ok = $ok
    status = $(if ($ok) { 'completed' } else { 'failed' })
    result_code = $resultCode
    result_code_name = (Get-BorealisOperationResultName $resultCode)
    reboot_required = [bool]$installResult.RebootRequired
    reboot_required_before_install = $rebootRequiredBeforeInstall
    matched_count = [int]$matches.Count
    installed_count = $installedCount
    update_results = $updateResults.items
    titles = $titles
  }
  if (-not $ok) { exit 4 }
} catch {
  $phase = "$script:BorealisPatchPhase".Trim()
  if (-not $phase) { $phase = 'install' }
  Write-BorealisPatchResult @{ ok = $false; error = 'install_failed'; phase = $phase; message = (Get-BorealisErrorMessage $_) }
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
