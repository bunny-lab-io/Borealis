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
	m.applyEngineSettings(data)
	reason := cleanText(data["reason"])
	if reason == "" {
		reason = "rdp_session_start"
	}
	if err := m.ensureReady(ctx, reason); err != nil {
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
		m.engineAddress = engineAddress
	}
	if localAddress != "" {
		m.localAddress = localAddress
	}
	m.port = port
	m.lastEnsureAt = time.Now().Unix()
	m.mu.Unlock()
}

func (m *Manager) ensureReady(ctx context.Context, reason string) error {
	if m == nil || !m.supported {
		return nil
	}
	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	m.mu.Lock()
	engineAddress := m.engineAddress
	localAddress := m.localAddress
	port := m.port
	m.lastEnsureAt = time.Now().Unix()
	m.mu.Unlock()
	if engineAddress == "" || localAddress == "" {
		return fmt.Errorf("RDP WireGuard firewall scope unavailable")
	}
	if err := m.ensureWindowsRDP(ctx); err != nil {
		m.recordNotReady(err)
		return err
	}
	if err := m.ensureFirewall(ctx, engineAddress, localAddress, port); err != nil {
		m.recordNotReady(err)
		return err
	}
	ready := m.waitForListener(port, listenerReadyWait)
	serviceState := m.queryServiceState(ctx)
	if !ready || !isServiceRunning(serviceState) {
		err := fmt.Errorf("RDP listener not ready on port %d; TermService=%s", port, displayServiceState(serviceState))
		m.recordNotReady(err)
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
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("RDP enablement failed: %s", compactLogText(result.Stdout+"\n"+result.Stderr))
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
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("RDP firewall ensure failed: %s", compactLogText(result.Stdout+"\n"+result.Stderr))
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
	return fmt.Sprintf(
		"$ErrorActionPreference = 'Stop'; "+
			"Get-NetFirewallRule -DisplayName %s -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction Stop; "+
			"New-NetFirewallRule -DisplayName %s -Direction Inbound -Action Allow -Protocol TCP -LocalPort %d -LocalAddress %s -RemoteAddress %s -Profile Any | Out-Null",
		name,
		name,
		port,
		powerShellSingleQuoted(local),
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

func (m *Manager) waitForListener(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.listenerReady(port) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return m.listenerReady(port)
}

func (m *Manager) recordNotReady(err error) {
	serviceState := m.queryServiceState(context.Background())
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
