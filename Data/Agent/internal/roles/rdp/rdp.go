package rdp

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	"github.com/bunny-lab-io/borealis/go-agent/internal/logutil"
)

const (
	defaultRDPPort         = 3389
	defaultEnsureInterval  = 30 * time.Second
	termServiceName        = "TermService"
	roleServiceDisplayName = "Borealis Agent - RDP"
	firewallRuleName       = "Borealis - RDP - WireGuard"
	readinessAuditInterval = 5 * time.Minute
	ensureLockPollInterval = 25 * time.Millisecond
	maxReadinessTimeout    = 60 * time.Second
)

var listenerReadyWait = 20 * time.Second

type AuthClient interface {
	PostJSON(ctx context.Context, path string, requestPayload any, responsePayload any) (any, error)
	AgentID() string
}

type authClientAdapter struct {
	client *auth.Client
}

func (a authClientAdapter) PostJSON(ctx context.Context, path string, requestPayload any, responsePayload any) (any, error) {
	return a.client.PostJSON(ctx, path, requestPayload, responsePayload)
}

func (a authClientAdapter) AgentID() string {
	return a.client.AgentID()
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

type Manager struct {
	authClient    AuthClient
	baseDir       string
	configPath    string
	logPath       string
	platform      string
	runner        commandRunner
	listenerProbe func(int) bool

	mu                sync.Mutex
	ensureMu          sync.Mutex
	started           bool
	supported         bool
	unsupportedReason string
	port              int
	engineAddress     string
	localAddress      string
	lastReadyAt       int64
	lastEnsureAt      int64
	lastReconcileAt   int64
	lastError         string
	lastServiceState  string
	lastListenerState string
	lastReady         bool
	stop              context.CancelFunc
}

func New(client *auth.Client, configPath string) *Manager {
	baseDir := filepath.Dir(configPath)
	return &Manager{
		authClient:        authClientAdapter{client: client},
		baseDir:           baseDir,
		configPath:        configPath,
		logPath:           filepath.Join(baseDir, "Logs", "RDP", "rdp.log"),
		platform:          runtime.GOOS,
		runner:            runCommand,
		listenerProbe:     nil,
		supported:         runtime.GOOS == "windows",
		unsupportedReason: "Native RDP is only supported on Windows agents.",
		port:              defaultRDPPort,
		lastEnsureAt:      time.Now().Unix(),
		lastServiceState:  "Stopped",
		lastListenerState: "not_listening",
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
	runCtx, cancel := context.WithCancel(ctx)
	m.stop = cancel
	m.mu.Unlock()
	m.logf("RDP supervisor starting platform=%s", m.platform)
	go m.ensureLoop(runCtx)
	go m.ensureFromEngine(context.Background(), "agent_startup")
}

func (m *Manager) Stop(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancel := m.stop
	m.stop = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	select {
	case <-ctx.Done():
	default:
	}
}

func (m *Manager) RequestEnsure(reason string) {
	if m == nil {
		return
	}
	reason = cleanText(reason)
	if reason == "" {
		reason = "role_supervisor_recovery"
	}
	go func() {
		if err := m.ensureFromEngine(context.Background(), reason); err != nil {
			m.setError(err)
		}
	}()
}

func (m *Manager) HandleStart(ctx context.Context, payload any) (any, error) {
	data := asMap(payload)
	if !m.payloadForThisAgent(data) {
		return map[string]any{"status": "ignored", "agent_id": m.agentID()}, nil
	}
	reason := cleanText(data["reason"])
	if reason == "" {
		reason = "rdp_session_start"
	}
	m.logf("RDP start request received reason=%s port=%s", reason, cleanText(firstNonEmpty(data["rdp_port"], data["port"])))
	m.applyEngineSettings(data)
	requestCtx, cancel := readinessRequestContext(ctx, data["timeout_seconds"])
	defer cancel()
	if supportsReadyFastPath(reason) && m.cachedReady() {
		m.logf("RDP start ready fast_path=true reason=%s port=%d", reason, m.currentPort())
		return map[string]any{"status": "ok", "ready": true, "port": m.currentPort()}, nil
	}
	if err := m.ensureReady(requestCtx, reason); err != nil {
		return map[string]any{"status": "error", "error": "rdp_service_not_ready", "detail": err.Error(), "ready": false}, nil
	}
	return map[string]any{"status": "ok", "ready": true, "port": m.currentPort()}, nil
}

func (m *Manager) Health() RoleHealth {
	if m == nil {
		return RoleHealth{Status: "unsupported", StatusCode: "unsupported", Detail: "RDP role is unavailable.", Details: map[string]any{"runtime": "go"}}
	}
	m.mu.Lock()
	supported := m.supported
	started := m.started
	serviceState := m.lastServiceState
	listenerState := m.lastListenerState
	ready := m.lastReady
	lastReadyAt := m.lastReadyAt
	lastEnsureAt := m.lastEnsureAt
	lastError := m.lastError
	port := m.port
	engineAddress := m.engineAddress
	localAddress := m.localAddress
	m.mu.Unlock()
	if !supported {
		return RoleHealth{Status: "unsupported", StatusCode: "unsupported", Detail: m.unsupportedReason, Details: map[string]any{"running_status": "Unsupported", "runtime": "go"}}
	}
	if started {
		serviceState = m.queryServiceState(context.Background())
		ready = isServiceRunning(serviceState) && m.listenerReady(port)
		listenerState = "not_listening"
		if ready {
			listenerState = "listening"
			lastReadyAt = time.Now().Unix()
		}
		m.mu.Lock()
		m.lastServiceState = serviceState
		m.lastListenerState = listenerState
		m.lastReady = ready
		if ready {
			m.lastReadyAt = lastReadyAt
			m.lastError = ""
		}
		m.mu.Unlock()
	}
	details := map[string]any{
		"running_status":     displayServiceState(serviceState),
		"service_state":      displayServiceState(serviceState),
		"service_name":       termServiceName,
		"service_alias":      roleServiceDisplayName,
		"listener_ip":        "0.0.0.0",
		"listener_port":      strconv.Itoa(port),
		"listener_state":     listenerState,
		"listener_ready":     strconv.FormatBool(ready),
		"ready":              strconv.FormatBool(ready),
		"engine_address":     engineAddress,
		"local_address":      localAddress,
		"firewall_rule":      firewallRuleName,
		"last_ready_at":      strconv.FormatInt(lastReadyAt, 10),
		"last_ensure_at":     strconv.FormatInt(lastEnsureAt, 10),
		"last_service_error": lastError,
		"runtime":            "go",
	}
	if ready {
		return RoleHealth{Status: "healthy", StatusCode: "healthy", Detail: roleServiceDisplayName + " listener is ready.", Details: details}
	}
	if !started {
		return RoleHealth{Status: "pending", StatusCode: "pending", Detail: "Waiting for RDP role startup.", Details: details}
	}
	detail := fmt.Sprintf("%s is %s; listener is %s.", roleServiceDisplayName, displayServiceState(serviceState), listenerState)
	if lastError != "" {
		detail += " Last error: " + lastError
	}
	return RoleHealth{Status: "recovering", StatusCode: "recovering", Detail: detail, Details: details}
}

func (m *Manager) ensureLoop(ctx context.Context) {
	ticker := time.NewTicker(defaultEnsureInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.ensureFromEngine(ctx, "always_on_check"); err != nil {
				m.setError(err)
			}
		}
	}
}

func (m *Manager) ensureFromEngine(ctx context.Context, reason string) error {
	if m == nil || !m.supported {
		return nil
	}
	var response map[string]any
	_, err := m.authClient.PostJSON(ctx, "/api/agent/rdp/ensure", map[string]any{
		"agent_id": m.agentID(),
		"reason":   cleanText(reason),
	}, &response)
	if err != nil {
		return err
	}
	m.applyEngineSettings(response)
	return m.ensureReady(ctx, reason)
}

func (m *Manager) applyEngineSettings(data map[string]any) {
	engineAddress := normalizeIPv4Address(firstNonEmpty(data["allowed_ips"], data["engine_virtual_ip"]))
	localAddress := normalizeIPv4Address(firstNonEmpty(data["virtual_ip"], data["local_address"]))
	port := resolveRDPPort(data["rdp_port"])
	m.mu.Lock()
	if engineAddress != "" {
		if engineAddress != m.engineAddress {
			m.lastReady = false
			m.lastReconcileAt = 0
		}
		m.engineAddress = engineAddress
	}
	if localAddress != "" {
		if localAddress != m.localAddress {
			m.lastReady = false
			m.lastReconcileAt = 0
		}
		m.localAddress = localAddress
	}
	if port != m.port {
		m.lastReady = false
		m.lastReconcileAt = 0
	}
	m.port = port
	m.lastEnsureAt = time.Now().Unix()
	m.mu.Unlock()
}

func (m *Manager) ensureReady(ctx context.Context, reason string) error {
	if m == nil || !m.supported {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if m.skipCachedEnsure(reason) {
		return nil
	}
	lockStarted := time.Now()
	locked, err := m.acquireEnsure(ctx, isCoalescibleEnsureReason(reason))
	if err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer m.ensureMu.Unlock()
	m.logEnsurePhase(reason, "lock_wait", lockStarted)
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.skipCachedEnsure(reason) {
		return nil
	}
	m.mu.Lock()
	engineAddress := m.engineAddress
	localAddress := m.localAddress
	port := m.port
	m.lastEnsureAt = time.Now().Unix()
	m.mu.Unlock()
	if engineAddress == "" || localAddress == "" {
		return fmt.Errorf("RDP WireGuard firewall scope unavailable")
	}
	m.mu.Lock()
	m.lastReconcileAt = time.Now().Unix()
	m.mu.Unlock()
	enablementStarted := time.Now()
	if err := m.ensureWindowsRDP(ctx); err != nil {
		m.recordNotReady(ctx, err)
		return err
	}
	m.logEnsurePhase(reason, "enablement", enablementStarted)
	firewallStarted := time.Now()
	if err := m.ensureFirewall(ctx, engineAddress, localAddress, port); err != nil {
		m.recordNotReady(ctx, err)
		return err
	}
	m.logEnsurePhase(reason, "firewall", firewallStarted)
	listenerStarted := time.Now()
	ready := m.waitForListener(ctx, port, listenerReadyWait)
	serviceState := m.queryServiceState(ctx)
	m.logEnsurePhase(reason, "listener", listenerStarted)
	if !ready || !isServiceRunning(serviceState) {
		err := fmt.Errorf("RDP listener not ready on port %d; TermService=%s", port, displayServiceState(serviceState))
		m.recordNotReady(ctx, err)
		return err
	}
	now := time.Now().Unix()
	m.mu.Lock()
	m.lastReady = true
	m.lastReadyAt = now
	m.lastServiceState = serviceState
	m.lastListenerState = "listening"
	m.lastError = ""
	m.mu.Unlock()
	m.logf("RDP service ready port=%d reason=%s local_address=%s engine_address=%s", port, cleanText(reason), localAddress, engineAddress)
	return nil
}

func (m *Manager) ensureWindowsRDP(ctx context.Context) error {
	command := "$ErrorActionPreference = 'Stop'; " +
		"$path = 'HKLM:\\SYSTEM\\CurrentControlSet\\Control\\Terminal Server'; " +
		"$current = (Get-ItemProperty -Path $path -Name fDenyTSConnections -ErrorAction Stop).fDenyTSConnections; " +
		"if ([int]$current -ne 0) { Set-ItemProperty -Path $path -Name fDenyTSConnections -Value 0 -Force }; " +
		"Set-Service -Name 'TermService' -StartupType Automatic; " +
		"if ((Get-Service -Name 'TermService').Status -ne 'Running') { Start-Service -Name 'TermService' }"
	result, err := m.runner(ctx, 30*time.Second, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	if err != nil {
		return commandExecutionError("RDP enablement", result, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("RDP enablement failed: exit_code=%d output=%s", result.ExitCode, compactLogText(result.Stdout+"\n"+result.Stderr))
	}
	return nil
}

func (m *Manager) ensureFirewall(ctx context.Context, engineAddress string, localAddress string, port int) error {
	command := buildFirewallCommand(engineAddress, localAddress, port)
	if command == "" {
		return fmt.Errorf("invalid RDP firewall scope")
	}
	result, err := m.runner(ctx, 30*time.Second, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	if err != nil {
		return commandExecutionError("RDP firewall ensure", result, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("RDP firewall ensure failed: exit_code=%d output=%s", result.ExitCode, compactLogText(result.Stdout+"\n"+result.Stderr))
	}
	return nil
}

func buildFirewallCommand(engineAddress string, localAddress string, port int) string {
	remote := normalizeIPv4Address(engineAddress)
	local := normalizeIPv4Address(localAddress)
	if remote == "" || local == "" || port <= 0 || port > 65535 {
		return ""
	}
	name := powerShellSingleQuoted(firewallRuleName)
	description := powerShellSingleQuoted(fmt.Sprintf("Borealis managed RDP; port=%d; local=%s; remote=%s", port, local, remote))
	localFilter := powerShellSingleQuoted(strings.TrimSuffix(local, "/32"))
	remoteFilter := powerShellSingleQuoted(strings.TrimSuffix(remote, "/32"))
	return fmt.Sprintf(
		"$ErrorActionPreference = 'Stop'; "+
			"$rules = @(Get-NetFirewallRule -DisplayName %s -ErrorAction SilentlyContinue); $rule = $rules | Select-Object -First 1; "+
			"$portFilter = if ($null -ne $rule) { $rule | Get-NetFirewallPortFilter }; "+
			"$addressFilter = if ($null -ne $rule) { $rule | Get-NetFirewallAddressFilter }; "+
			"$valid = ($rules.Count -eq 1) -and ([string]$rule.Description -eq %s) -and ([string]$rule.Enabled -eq 'True') -and ([string]$rule.Direction -eq 'Inbound') -and ([string]$rule.Action -eq 'Allow') -and ([string]$portFilter.Protocol -eq 'TCP') -and ([string]$portFilter.LocalPort -eq '%d') -and (([string]$addressFilter.LocalAddress -eq %s) -or ([string]$addressFilter.LocalAddress -eq %s)) -and (([string]$addressFilter.RemoteAddress -eq %s) -or ([string]$addressFilter.RemoteAddress -eq %s)); "+
			"if (-not $valid) { Get-NetFirewallRule -DisplayName %s -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue; "+
			"New-NetFirewallRule -DisplayName %s -Description %s -Direction Inbound -Action Allow -Protocol TCP -LocalPort %d -LocalAddress %s -RemoteAddress %s -Profile Any | Out-Null; "+
			"$rules = @(Get-NetFirewallRule -DisplayName %s -ErrorAction SilentlyContinue); $rule = $rules | Select-Object -First 1; "+
			"$portFilter = if ($null -ne $rule) { $rule | Get-NetFirewallPortFilter }; $addressFilter = if ($null -ne $rule) { $rule | Get-NetFirewallAddressFilter }; "+
			"$valid = ($rules.Count -eq 1) -and ([string]$rule.Description -eq %s) -and ([string]$rule.Enabled -eq 'True') -and ([string]$rule.Direction -eq 'Inbound') -and ([string]$rule.Action -eq 'Allow') -and ([string]$portFilter.Protocol -eq 'TCP') -and ([string]$portFilter.LocalPort -eq '%d') -and (([string]$addressFilter.LocalAddress -eq %s) -or ([string]$addressFilter.LocalAddress -eq %s)) -and (([string]$addressFilter.RemoteAddress -eq %s) -or ([string]$addressFilter.RemoteAddress -eq %s)); "+
			"if (-not $valid) { throw ('Borealis RDP firewall rule verification failed rules=' + ([string]$rules.Count) + ' enabled=' + ([string]$rule.Enabled) + ' direction=' + ([string]$rule.Direction) + ' action=' + ([string]$rule.Action) + ' protocol=' + ([string]$portFilter.Protocol) + ' port=' + ([string]$portFilter.LocalPort) + ' local=' + ([string]$addressFilter.LocalAddress) + ' remote=' + ([string]$addressFilter.RemoteAddress)) } }",
		name,
		description,
		port,
		localFilter,
		powerShellSingleQuoted(local),
		remoteFilter,
		powerShellSingleQuoted(remote),
		name,
		name,
		description,
		port,
		localFilter,
		remoteFilter,
		name,
		description,
		port,
		localFilter,
		powerShellSingleQuoted(local),
		remoteFilter,
		powerShellSingleQuoted(remote),
	)
}

func (m *Manager) queryServiceState(ctx context.Context) string {
	result, err := m.runner(ctx, 10*time.Second, "sc.exe", "query", termServiceName)
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	for _, line := range strings.Split(result.Stdout+"\n"+result.Stderr, "\n") {
		if !strings.Contains(line, "STATE") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			return strings.ToUpper(fields[len(fields)-1])
		}
	}
	return ""
}

func (m *Manager) listenerReady(port int) bool {
	if m != nil && m.listenerProbe != nil {
		return m.listenerProbe(port)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 750*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (m *Manager) waitForListener(ctx context.Context, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.listenerReady(port) {
			return true
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
	return m.listenerReady(port)
}

func (m *Manager) recordNotReady(ctx context.Context, err error) {
	serviceState := m.queryServiceState(ctx)
	m.mu.Lock()
	m.lastReady = false
	m.lastServiceState = serviceState
	m.lastListenerState = "not_listening"
	if err != nil {
		m.lastError = err.Error()
	}
	m.mu.Unlock()
}

func (m *Manager) setError(err error) {
	if m == nil || err == nil {
		return
	}
	m.mu.Lock()
	m.lastError = err.Error()
	m.lastEnsureAt = time.Now().Unix()
	m.mu.Unlock()
	m.logf("RDP ensure failed error=%v", err)
}

func (m *Manager) currentPort() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.port
}

func (m *Manager) cachedReady() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	port := m.port
	ready := m.lastReady &&
		isServiceRunning(m.lastServiceState) &&
		strings.EqualFold(strings.TrimSpace(m.lastListenerState), "listening")
	m.mu.Unlock()
	if !ready {
		return false
	}
	if m.listenerReady(port) {
		return true
	}
	m.mu.Lock()
	m.lastReady = false
	m.lastListenerState = "not_listening"
	m.mu.Unlock()
	return false
}

func (m *Manager) skipCachedEnsure(reason string) bool {
	if supportsReadyFastPath(reason) && !isRoutineEnsureReason(reason) {
		return m.cachedReady()
	}
	if !isAuditCoalescingReason(reason) {
		return false
	}
	m.mu.Lock()
	lastReconcileAt := m.lastReconcileAt
	m.mu.Unlock()
	return lastReconcileAt > 0 && time.Since(time.Unix(lastReconcileAt, 0)) < readinessAuditInterval
}

func (m *Manager) acquireEnsure(ctx context.Context, coalesce bool) (bool, error) {
	if coalesce {
		return m.ensureMu.TryLock(), nil
	}
	ticker := time.NewTicker(ensureLockPollInterval)
	defer ticker.Stop()
	for {
		if m.ensureMu.TryLock() {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) logEnsurePhase(reason string, phase string, started time.Time) {
	m.logf("RDP ensure phase reason=%s phase=%s duration_ms=%d", cleanText(reason), phase, time.Since(started).Milliseconds())
}

func supportsReadyFastPath(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "always_on_check", "rdp_establish", "rdp_session_start":
		return true
	default:
		return false
	}
}

func isRoutineEnsureReason(reason string) bool {
	return strings.EqualFold(strings.TrimSpace(reason), "always_on_check")
}

func isCoalescibleEnsureReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "always_on_check", "agent_startup", "role_supervisor_recovery":
		return true
	default:
		return false
	}
}

func isAuditCoalescingReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "always_on_check", "role_supervisor_recovery":
		return true
	default:
		return false
	}
}

func readinessRequestContext(parent context.Context, value any) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	seconds, err := strconv.ParseFloat(cleanText(value), 64)
	if err != nil || seconds <= 0 {
		return context.WithCancel(parent)
	}
	timeout := time.Duration(seconds * float64(time.Second))
	if timeout > maxReadinessTimeout {
		timeout = maxReadinessTimeout
	}
	return context.WithTimeout(parent, timeout)
}

func commandExecutionError(operation string, result commandResult, err error) error {
	output := compactLogText(strings.TrimSpace(result.Stdout + "\n" + result.Stderr))
	if output == "" {
		return fmt.Errorf("%s failed: %w", operation, err)
	}
	return fmt.Errorf("%s failed: %w output=%s", operation, err, output)
}

func (m *Manager) payloadForThisAgent(data map[string]any) bool {
	target := cleanText(data["agent_id"])
	return target == "" || strings.EqualFold(target, m.agentID())
}

func (m *Manager) agentID() string {
	if m == nil || m.authClient == nil {
		return ""
	}
	return cleanText(m.authClient.AgentID())
}

func (m *Manager) logf(format string, args ...any) {
	if m == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.logPath), 0755)
	logutil.Append(
		m.logPath,
		logutil.RetentionDaysFromConfig(m.configPath),
		"[%s] [rdp] %s",
		time.Now().Format("2006-01-02T15:04:05"),
		fmt.Sprintf(format, args...),
	)
}

func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, name, args...)
	stdout, err := cmd.Output()
	result := commandResult{Stdout: string(stdout), ExitCode: 0}
	if err == nil {
		return result, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.Stderr = string(exitErr.Stderr)
		result.ExitCode = exitErr.ExitCode()
	}
	return result, err
}

func normalizeIPv4Address(value any) string {
	text := cleanText(value)
	if strings.Contains(text, ",") {
		text = strings.TrimSpace(strings.Split(text, ",")[0])
	}
	text = strings.TrimSuffix(text, "/32")
	ip := net.ParseIP(text)
	if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		return ""
	}
	return ip.String() + "/32"
}

func resolveRDPPort(value any) int {
	port, err := strconv.Atoi(cleanText(value))
	if err != nil || port <= 0 || port > 65535 {
		return defaultRDPPort
	}
	return port
}

func powerShellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func asMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func cleanText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstNonEmpty(values ...any) any {
	for _, value := range values {
		if cleanText(value) != "" {
			return value
		}
	}
	return nil
}

func compactLogText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func displayServiceState(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Unavailable"
	}
	return value
}

func isServiceRunning(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "RUNNING")
}
