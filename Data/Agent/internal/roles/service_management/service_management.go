package servicemanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
)

const (
	serviceRefreshInterval = 60 * time.Second
	serviceBoostInterval   = 3 * time.Second
	serviceBoostWindow     = 30 * time.Second
	serviceCommandTimeout  = 180 * time.Second
)

var serviceStatusLabels = map[string]string{
	"running":  "Running",
	"stopped":  "Stopped",
	"starting": "Starting",
	"stopping": "Stopping",
	"paused":   "Paused",
	"failed":   "Failed",
	"unknown":  "Unknown",
}

type Manager struct {
	authClient        *auth.Client
	hostname          string
	serviceMode       string
	runner            commandRunner
	publisher         func(context.Context, []Service) error
	wakeup            chan struct{}
	mu                sync.Mutex
	started           bool
	loopRunning       bool
	supported         bool
	unsupportedReason string
	lastError         string
	lastRefreshAt     int64
	lastServiceCount  int
	fastPollUntil     time.Time
}

type Service struct {
	ServiceID   string `json:"service_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	StatusCode  string `json:"status_code"`
	Status      string `json:"status"`
	CapturedAt  int64  `json:"captured_at"`
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
	manager.publisher = manager.publishServices
	return manager
}

func detectSupport() (bool, string) {
	switch runtime.GOOS {
	case "windows":
		return true, ""
	case "linux":
		if _, err := exec.LookPath("systemctl"); err == nil {
			return true, ""
		}
		return false, "systemctl is unavailable on this Linux agent."
	default:
		return false, fmt.Sprintf("Unsupported service-management platform '%s'.", runtime.GOOS)
	}
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
	lastServiceCount := m.lastServiceCount
	m.mu.Unlock()
	details := map[string]any{
		"running_status":      "Running",
		"service_count":       strconv.Itoa(lastServiceCount),
		"last_refresh_at":     strconv.FormatInt(lastRefreshAt, 10),
		"refresh_interval_ms": strconv.Itoa(int(serviceRefreshInterval / time.Millisecond)),
		"runtime":             "go",
	}
	if !supported {
		details["running_status"] = "Unsupported"
		return RoleHealth{
			Status:     "unsupported",
			StatusCode: "unsupported",
			Detail:     fallbackText(unsupportedReason, "Service management is unsupported on this platform."),
			Details:    details,
		}
	}
	if !loopRunning {
		details["running_status"] = "Stopped"
		return RoleHealth{
			Status:     "pending",
			StatusCode: "pending",
			Detail:     "Waiting for service inventory refresh loop.",
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
			Detail:     "Waiting for initial service inventory snapshot.",
			Details:    details,
		}
	}
	return RoleHealth{
		Status:     "healthy",
		StatusCode: "healthy",
		Detail:     "Service inventory refresh loop active.",
		Details:    details,
	}
}

func (m *Manager) HandleControlAction(ctx context.Context, payload any) (any, error) {
	body, ok := payload.(map[string]any)
	if !ok {
		return errorResponse("invalid_request", "Service control payload must be an object."), nil
	}
	if !m.matchesTarget(body) {
		return errorResponse("not_for_host", "The service-control request targeted another device."), nil
	}
	if !m.supported {
		return errorResponse("unsupported_platform", fallbackText(m.unsupportedReason, "Service management is unsupported on this platform.")), nil
	}
	serviceName := cleanText(firstValue(body, "service_name", "name"))
	if serviceName == "" {
		return errorResponse("service_name_required", "A service name is required."), nil
	}
	action := normalizeServiceAction(body["action"])
	if action == "" {
		return errorResponse("invalid_action", "Service action must be start, stop, or restart."), nil
	}
	requestedBy := cleanText(body["requested_by"])
	go m.runAction(context.Background(), serviceName, action, requestedBy)
	return map[string]any{
		"ok":           true,
		"status":       "accepted",
		"service_name": serviceName,
		"action":       action,
	}, nil
}

func (m *Manager) Refresh(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if !m.supported {
		return fmt.Errorf("%s", fallbackText(m.unsupportedReason, "service management unsupported"))
	}
	services, err := m.collectServices(ctx)
	if err != nil {
		m.recordError("Service inventory refresh failed: " + err.Error())
		return err
	}
	if err := m.publisher(ctx, services); err != nil {
		m.recordError("Service inventory publish failed: " + err.Error())
		return err
	}
	m.mu.Lock()
	m.lastError = ""
	m.lastRefreshAt = time.Now().Unix()
	m.lastServiceCount = len(services)
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
			m.recordError("Service inventory refresh failed: " + err.Error())
		}
		waitFor := serviceRefreshInterval
		m.mu.Lock()
		if time.Now().Before(m.fastPollUntil) {
			waitFor = serviceBoostInterval
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

func (m *Manager) runAction(ctx context.Context, serviceName string, action string, requestedBy string) {
	if err := m.runServiceAction(ctx, serviceName, action); err != nil {
		m.recordError(fmt.Sprintf("Service control failed action=%s service_name=%s requested_by=%s error=%s", action, serviceName, fallbackText(requestedBy, "-"), err.Error()))
	} else {
		m.mu.Lock()
		m.lastError = ""
		m.mu.Unlock()
	}
	if err := m.Refresh(ctx); err != nil {
		m.recordError("Post-action service refresh failed: " + err.Error())
	}
	m.requestBoost()
}

func (m *Manager) collectServices(ctx context.Context) ([]Service, error) {
	switch runtime.GOOS {
	case "windows":
		return m.queryWindowsServices(ctx)
	case "linux":
		return m.queryLinuxServices(ctx)
	default:
		return nil, fmt.Errorf("%s", fallbackText(m.unsupportedReason, "unsupported platform"))
	}
}

func (m *Manager) queryWindowsServices(ctx context.Context) ([]Service, error) {
	command := `$ErrorActionPreference='Stop'; Get-CimInstance Win32_Service | Select-Object Name,DisplayName,Description,State | ConvertTo-Json -Depth 3 -Compress`
	result, err := m.runner(ctx, serviceCommandTimeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
	if err != nil || result.ExitCode != 0 {
		return nil, commandError(result, err)
	}
	return parseWindowsServices(result.Stdout, time.Now().Unix())
}

func (m *Manager) queryLinuxServices(ctx context.Context) ([]Service, error) {
	result, err := m.runner(ctx, serviceCommandTimeout, "systemctl", "list-units", "--type=service", "--all", "--no-pager", "--no-legend", "--plain", "--full")
	if err != nil || result.ExitCode != 0 {
		return nil, commandError(result, err)
	}
	return parseLinuxServices(result.Stdout, time.Now().Unix()), nil
}

func (m *Manager) runServiceAction(ctx context.Context, serviceName string, action string) error {
	if runtime.GOOS == "windows" {
		command := windowsServiceCommand(action, serviceName)
		result, err := m.runner(ctx, serviceCommandTimeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
		if err != nil || result.ExitCode != 0 {
			return commandError(result, err)
		}
		return nil
	}
	result, err := m.runner(ctx, serviceCommandTimeout, "systemctl", action, serviceName)
	if err != nil || result.ExitCode != 0 {
		return commandError(result, err)
	}
	return nil
}

func (m *Manager) publishServices(ctx context.Context, services []Service) error {
	if m.authClient == nil {
		return fmt.Errorf("auth client unavailable")
	}
	payload := map[string]any{
		"agent_id":     m.authClient.AgentID(),
		"hostname":     m.hostname,
		"service_mode": m.serviceMode,
		"details": map[string]any{
			"summary": map[string]any{
				"hostname":     m.hostname,
				"agent_id":     m.authClient.AgentID(),
				"service_mode": m.serviceMode,
			},
			"services": services,
		},
	}
	_, err := m.authClient.PostJSON(ctx, "/api/agent/details", payload, nil)
	return err
}

func (m *Manager) requestBoost() {
	m.mu.Lock()
	m.fastPollUntil = time.Now().Add(serviceBoostWindow)
	m.mu.Unlock()
	select {
	case m.wakeup <- struct{}{}:
	default:
	}
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

func parseWindowsServices(output string, capturedAt int64) ([]Service, error) {
	if strings.TrimSpace(output) == "" {
		return []Service{}, nil
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
	services := make([]Service, 0, len(items))
	for _, item := range items {
		name := cleanText(item["Name"])
		if name == "" {
			continue
		}
		statusCode := normalizeServiceStatus(item["State"])
		services = append(services, Service{
			ServiceID:   serviceIDForName(name),
			Name:        name,
			DisplayName: cleanText(item["DisplayName"]),
			Description: cleanText(item["Description"]),
			StatusCode:  statusCode,
			Status:      serviceStatusLabels[statusCode],
			CapturedAt:  capturedAt,
		})
	}
	return sortServices(services), nil
}

func parseLinuxServices(output string, capturedAt int64) []Service {
	services := []Service{}
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		name := cleanText(parts[0])
		if name == "" {
			continue
		}
		description := ""
		if len(parts) >= 5 {
			description = strings.TrimSpace(strings.Join(parts[4:], " "))
		}
		statusCode := linuxStatusCode(parts[2], parts[3])
		services = append(services, Service{
			ServiceID:   serviceIDForName(name),
			Name:        name,
			DisplayName: description,
			Description: description,
			StatusCode:  statusCode,
			Status:      serviceStatusLabels[statusCode],
			CapturedAt:  capturedAt,
		})
	}
	return sortServices(services)
}

func sortServices(services []Service) []Service {
	sort.SliceStable(services, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(services[i].DisplayName))
		if left == "" {
			left = strings.ToLower(strings.TrimSpace(services[i].Name))
		}
		right := strings.ToLower(strings.TrimSpace(services[j].DisplayName))
		if right == "" {
			right = strings.ToLower(strings.TrimSpace(services[j].Name))
		}
		if left == right {
			return strings.ToLower(services[i].Name) < strings.ToLower(services[j].Name)
		}
		return left < right
	})
	return services
}

func linuxStatusCode(activeState string, subState string) string {
	active := strings.ToLower(cleanText(activeState))
	sub := strings.ToLower(cleanText(subState))
	switch active {
	case "active":
		return "running"
	case "inactive":
		return "stopped"
	case "failed":
		return "failed"
	case "activating":
		return "starting"
	case "deactivating":
		return "stopping"
	default:
		return normalizeServiceStatus(firstNonEmpty(sub, active))
	}
}

func normalizeServiceStatus(value any) string {
	text := strings.ToLower(cleanText(value))
	text = strings.ReplaceAll(text, " ", "_")
	text = strings.ReplaceAll(text, "-", "_")
	aliases := map[string]string{
		"active":           "running",
		"running":          "running",
		"up":               "running",
		"online":           "running",
		"inactive":         "stopped",
		"stopped":          "stopped",
		"dead":             "stopped",
		"down":             "stopped",
		"disabled":         "stopped",
		"activating":       "starting",
		"start_pending":    "starting",
		"starting":         "starting",
		"reloading":        "starting",
		"deactivating":     "stopping",
		"stop_pending":     "stopping",
		"stopping":         "stopping",
		"paused":           "paused",
		"pause_pending":    "paused",
		"continue_pending": "starting",
		"failed":           "failed",
		"error":            "failed",
	}
	if normalized := aliases[text]; normalized != "" {
		return normalized
	}
	if _, ok := serviceStatusLabels[text]; ok {
		return text
	}
	return "unknown"
}

func normalizeServiceAction(value any) string {
	switch strings.ToLower(cleanText(value)) {
	case "start", "stop", "restart":
		return strings.ToLower(cleanText(value))
	default:
		return ""
	}
}

func windowsServiceCommand(action string, serviceName string) string {
	escaped := strings.ReplaceAll(serviceName, "'", "''")
	switch action {
	case "start":
		return fmt.Sprintf("$ErrorActionPreference='Stop'; Start-Service -Name '%s'", escaped)
	case "stop":
		return fmt.Sprintf("$ErrorActionPreference='Stop'; Stop-Service -Name '%s' -Force", escaped)
	default:
		return fmt.Sprintf("$ErrorActionPreference='Stop'; Restart-Service -Name '%s' -Force", escaped)
	}
}

func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, name, args...)
	stdout, stderr := strings.Builder{}, strings.Builder{}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return commandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}, runCtx.Err()
	}
	return commandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}, err
}

func commandError(result commandResult, err error) error {
	detail := cleanText(result.Stderr)
	if detail == "" {
		detail = cleanText(result.Stdout)
	}
	if detail == "" && err != nil {
		detail = err.Error()
	}
	if detail == "" {
		detail = fmt.Sprintf("exit %d", result.ExitCode)
	}
	return fmt.Errorf("%s", detail)
}

func errorResponse(code string, message string) map[string]any {
	return map[string]any{"ok": false, "error": code, "message": message}
}

func serviceIDForName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func cleanText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok && cleanText(value) != "" {
			return value
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func fallbackText(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
