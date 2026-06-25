package vnc

import (
	"context"
	"crypto/des"
	"crypto/rand"
	"encoding/hex"
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
	defaultVNCPort             = 5900
	defaultAlwaysOnInterval    = 30 * time.Second
	serviceName                = "BorealisAgentUltraVNC"
	serviceDisplayName         = "Borealis Agent - UltraVNC"
	firewallRuleName           = "Borealis - VNC - UltraVNC"
	recentReadyGraceSeconds    = 20
	credentialRotationInterval = 24 * time.Hour
)

var (
	serviceTransitionWait           = 20 * time.Second
	serviceTransitionForceKillWait  = 10 * time.Second
	serviceAlreadyRunningVerifyWait = 8 * time.Second
	listenerReadyWait               = 20 * time.Second
)

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

type Manager struct {
	authClient  AuthClient
	hostname    string
	serviceMode string
	configPath  string
	baseDir     string
	logPath     string
	platform    string
	runner      commandRunner

	mu                  sync.Mutex
	ensureMu            sync.Mutex
	started             bool
	supported           bool
	unsupportedReason   string
	port                int
	allowedIPs          string
	removeWallpaper     bool
	controllerPassword  string
	credentialRevision  int64
	credentialIssuedAt  time.Time
	activeSessionID     string
	lastReadyAt         int64
	lastError           string
	lastEnsureAt        int64
	lastServiceState    string
	lastListenerState   string
	lastReady           bool
	serviceConfigLoaded bool
	serviceName         string
	vncExe              string
	displayCollector    func() []map[string]any
	stop                context.CancelFunc
}

type commandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type commandRunner func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error)

func New(client *auth.Client, hostname string, serviceMode string, configPath string) *Manager {
	baseDir := filepath.Dir(configPath)
	password, revision := newCredential()
	manager := &Manager{
		authClient:         authClientAdapter{client: client},
		hostname:           strings.TrimSpace(hostname),
		serviceMode:        auth.NormalizeServiceMode(serviceMode),
		configPath:         strings.TrimSpace(configPath),
		baseDir:            baseDir,
		logPath:            filepath.Join(baseDir, "Logs", "UltraVNC", "vnc.log"),
		platform:           runtime.GOOS,
		runner:             runCommand,
		supported:          runtime.GOOS == "windows",
		port:               resolveVNCPort(nil),
		removeWallpaper:    true,
		controllerPassword: password,
		credentialRevision: revision,
		credentialIssuedAt: time.Now(),
		serviceName:        serviceName,
		vncExe:             resolveVNCExe(),
		displayCollector:   collectDisplayTopology,
		lastServiceState:   "Stopped",
		lastListenerState:  "not_listening",
		lastReady:          false,
		lastEnsureAt:       time.Now().Unix(),
		unsupportedReason:  "Always-on UltraVNC is only supported on Windows agents.",
	}
	return manager
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
	m.logf("VNC supervisor starting platform=%s config=%s", m.platform, m.configPath)
	go m.alwaysOnLoop(runCtx)
	go m.ensureFromEngine(context.Background(), "agent_startup")
}

func (m *Manager) RequestEnsure(reason string) {
	if m == nil {
		return
	}
	cleanReason := cleanText(reason)
	if cleanReason == "" {
		cleanReason = "role_supervisor_recovery"
	}
	go func() {
		if err := m.ensureFromEngine(context.Background(), cleanReason); err != nil {
			m.mu.Lock()
			m.lastError = err.Error()
			m.lastEnsureAt = time.Now().Unix()
			m.mu.Unlock()
			m.logf("VNC recovery ensure failed reason=%s error=%v", cleanReason, err)
		}
	}()
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

func (m *Manager) HandleStart(ctx context.Context, payload any) (any, error) {
	data := asMap(payload)
	if !m.payloadForThisAgent(data) {
		m.logf("VNC start request ignored target_agent_id=%s local_agent_id=%s", cleanText(data["agent_id"]), m.agentID())
		return map[string]any{"status": "ignored", "agent_id": m.agentID()}, nil
	}
	reason := cleanText(data["reason"])
	if reason == "" {
		reason = "vnc_session_start"
	}
	m.logf("VNC start request received reason=%s session_id=%s port=%s", reason, cleanText(data["session_id"]), cleanText(data["port"]))
	m.mu.Lock()
	if value := data["port"]; value != nil {
		m.port = resolveVNCPort(value)
	}
	if allowed := parseAllowedIPs(data["allowed_ips"]); allowed != "" {
		m.allowedIPs = allowed
	}
	if value, ok := data["remove_wallpaper"].(bool); ok {
		m.removeWallpaper = value
	}
	m.activeSessionID = cleanText(data["session_id"])
	m.mu.Unlock()
	err := m.ensureAlwaysOn(ctx, reason)
	if err != nil {
		return map[string]any{"status": "error", "detail": err.Error()}, nil
	}
	return map[string]any{"status": "ok", "ready": m.ready()}, nil
}

func (m *Manager) HandleStop(ctx context.Context, payload any) (any, error) {
	data := asMap(payload)
	if !m.payloadForThisAgent(data) {
		return map[string]any{"status": "ignored", "agent_id": m.agentID()}, nil
	}
	reason := cleanText(data["reason"])
	if reason == "" {
		reason = "vnc_session_end"
	}
	m.mu.Lock()
	m.activeSessionID = ""
	m.mu.Unlock()
	_ = m.ensureAlwaysOn(ctx, reason)
	return map[string]any{"status": "ok", "reason": reason}, nil
}

func (m *Manager) HandleRefresh(ctx context.Context, payload any) (any, error) {
	data := asMap(payload)
	if !m.payloadForThisAgent(data) {
		return map[string]any{"status": "ignored", "agent_id": m.agentID()}, nil
	}
	reason := cleanText(data["reason"])
	if reason == "" {
		reason = "engine_credential_refresh"
	}
	if shouldRotateCredential(reason) {
		m.rotateCredential(reason)
	}
	err := m.ensureFromEngine(ctx, reason)
	if err != nil {
		return map[string]any{"status": "error", "detail": err.Error()}, nil
	}
	return map[string]any{"status": "ok", "ready": m.ready()}, nil
}

func (m *Manager) HandleCredentialRequest(ctx context.Context, payload any) (any, error) {
	data := asMap(payload)
	if !m.payloadForThisAgent(data) {
		m.logf("VNC credential request ignored target_agent_id=%s local_agent_id=%s request_id=%s", cleanText(data["agent_id"]), m.agentID(), cleanText(data["request_id"]))
		return map[string]any{"status": "ignored", "agent_id": m.agentID()}, nil
	}
	reason := cleanText(data["reason"])
	if reason == "" {
		reason = "vnc_establish"
	}
	requestID := cleanText(data["request_id"])
	rotated := false
	fastPath := false
	if shouldRotateCredential(reason) {
		m.rotateCredential(reason)
		rotated = true
	} else {
		rotated = m.ensureCredentialFresh(reason)
		fastPath = !rotated && m.credentialFastPathReady()
	}
	m.logf("VNC credential request received reason=%s request_id=%s fast_path=%t rotated=%t", reason, requestID, fastPath, rotated)
	if fastPath {
		return m.credentialPayload(requestID, reason), nil
	}
	if err := m.ensureAlwaysOn(ctx, reason); err != nil {
		m.logf("VNC credential request ensure failed reason=%s request_id=%s error=%v", reason, requestID, err)
		return m.credentialErrorPayload(requestID, reason, err), nil
	}
	return m.credentialPayload(requestID, reason), nil
}

func (m *Manager) Health() RoleHealth {
	if m == nil {
		return RoleHealth{
			Status:     "unsupported",
			StatusCode: "unsupported",
			Detail:     "VNC role is unavailable.",
			Details: map[string]any{
				"running_status": "Unavailable",
				"runtime":        "go",
			},
		}
	}
	m.mu.Lock()
	supported := m.supported
	unsupportedReason := m.unsupportedReason
	started := m.started
	serviceState := m.lastServiceState
	listenerState := m.lastListenerState
	ready := m.lastReady
	lastReadyAt := m.lastReadyAt
	lastError := m.lastError
	port := m.port
	allowedIPs := m.allowedIPs
	activeSessionID := m.activeSessionID
	revision := m.credentialRevision
	vncExe := m.vncExe
	service := m.serviceName
	m.mu.Unlock()
	if !supported {
		return RoleHealth{
			Status:     "unsupported",
			StatusCode: "unsupported",
			Detail:     unsupportedReason,
			Details: map[string]any{
				"running_status": "Unsupported",
				"runtime":        "go",
			},
		}
	}
	if started {
		serviceState = m.queryServiceState(context.Background(), service)
		ready = m.listenerReady(port)
		serviceReady := ready && isServiceRunning(serviceState)
		listenerState = "not_listening"
		if ready {
			listenerState = "listening"
		}
		m.mu.Lock()
		m.lastReady = serviceReady
		m.lastServiceState = serviceState
		m.lastListenerState = listenerState
		if serviceReady {
			lastReadyAt = time.Now().Unix()
			m.lastReadyAt = lastReadyAt
			m.lastError = ""
		}
		m.mu.Unlock()
	}
	displayTopology, displayVirtualBounds := m.displaySnapshot()
	serviceReady := ready && isServiceRunning(serviceState)
	details := map[string]any{
		"running_status":              displayServiceState(serviceState),
		"service_state":               displayServiceState(serviceState),
		"listener_ip":                 "0.0.0.0",
		"listener_port":               strconv.Itoa(port),
		"service_name":                service,
		"allowed_ips":                 allowedIPs,
		"last_service_error":          lastError,
		"listener_state":              listenerState,
		"listener_ready":              strconv.FormatBool(ready),
		"ready":                       strconv.FormatBool(serviceReady),
		"last_ready_at":               strconv.FormatInt(lastReadyAt, 10),
		"active_session_id":           activeSessionID,
		"credential_revision":         strconv.FormatInt(revision, 10),
		"vnc_exe":                     vncExe,
		"display_count":               strconv.Itoa(len(displayTopology)),
		"display_topology":            jsonText(displayTopology),
		"display_virtual_bounds":      jsonText(displayVirtualBounds),
		"display_topology_json":       jsonText(displayTopology),
		"display_virtual_bounds_json": jsonText(displayVirtualBounds),
		"runtime":                     "go",
	}
	if serviceReady {
		return RoleHealth{
			Status:     "healthy",
			StatusCode: "healthy",
			Detail:     fmt.Sprintf("%s listener is ready.", service),
			Details:    details,
		}
	}
	if !started {
		return RoleHealth{
			Status:     "pending",
			StatusCode: "pending",
			Detail:     "Waiting for VNC role startup.",
			Details:    details,
		}
	}
	if m.recentlyReady(lastReadyAt) && isServiceRunning(serviceState) {
		details["listener_state"] = "recently_listening"
		return RoleHealth{
			Status:     "healthy",
			StatusCode: "healthy",
			Detail:     fmt.Sprintf("%s listener was recently ready.", service),
			Details:    details,
		}
	}
	detail := fmt.Sprintf("%s is %s; listener is %s.", service, displayServiceState(serviceState), listenerState)
	if lastError != "" {
		detail += " Last error: " + lastError
	}
	return RoleHealth{
		Status:     "recovering",
		StatusCode: "recovering",
		Detail:     detail,
		Details:    details,
	}
}

func (m *Manager) alwaysOnLoop(ctx context.Context) {
	ticker := time.NewTicker(alwaysOnInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reason := "always_on_check"
			if m.ensureCredentialFresh("scheduled_rotation") {
				reason = "credential_rotation"
			}
			if err := m.ensureFromEngine(ctx, reason); err != nil {
				m.setError(err.Error())
			}
		}
	}
}

func (m *Manager) ensureFromEngine(ctx context.Context, reason string) error {
	if m == nil || !m.supported {
		return nil
	}
	m.ensureCredentialFresh(reason)
	m.mu.Lock()
	credential := m.controllerPassword
	revision := m.credentialRevision
	m.lastEnsureAt = time.Now().Unix()
	m.mu.Unlock()
	request := m.ensureRequestPayload(reason, credential, revision)
	var response map[string]any
	_, err := m.authClient.PostJSON(ctx, "/api/agent/vnc/ensure", request, &response)
	if err != nil {
		return err
	}
	if allowed := parseAllowedIPs(response["allowed_ips"]); allowed != "" {
		m.mu.Lock()
		m.allowedIPs = allowed
		m.mu.Unlock()
	}
	if value := response["vnc_port"]; value != nil {
		m.mu.Lock()
		m.port = resolveVNCPort(value)
		m.mu.Unlock()
	}
	if sessionID := cleanText(response["session_id"]); sessionID != "" {
		m.mu.Lock()
		m.activeSessionID = sessionID
		m.mu.Unlock()
	}
	return m.ensureAlwaysOn(ctx, reason)
}

func (m *Manager) ensureRequestPayload(reason string, credential string, revision int64) map[string]any {
	displayTopology, displayVirtualBounds := m.displaySnapshot()
	return map[string]any{
		"agent_id":               m.agentID(),
		"reason":                 reason,
		"controller_password":    credential,
		"credential_revision":    revision,
		"display_topology":       displayTopology,
		"display_virtual_bounds": displayVirtualBounds,
	}
}

func (m *Manager) ensureAlwaysOn(ctx context.Context, reason string) error {
	if m == nil || !m.supported {
		return nil
	}
	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	if isPassiveDisconnectEnsureReason(reason) && m.credentialFastPathReady() {
		m.tracef("VNC ensure skipped reason=%s service=%s ready=true", reason, m.serviceName)
		return nil
	}
	m.mu.Lock()
	port := m.port
	allowedIPs := m.allowedIPs
	password := m.controllerPassword
	revision := m.credentialRevision
	removeWallpaper := m.removeWallpaper
	serviceConfigLoaded := m.serviceConfigLoaded
	previousReady := m.lastReady
	previousServiceState := m.lastServiceState
	previousListenerState := m.lastListenerState
	m.mu.Unlock()
	routineEnsure := isRoutineEnsureReason(reason)
	if password == "" {
		return fmt.Errorf("VNC controller credential unavailable")
	}
	if allowedIPs == "" {
		return fmt.Errorf("VNC tunnel firewall scope unavailable")
	}
	if m.vncExe == "" {
		m.vncExe = resolveVNCExe()
	}
	if m.vncExe == "" {
		return fmt.Errorf("UltraVNC winvnc.exe not found")
	}
	serviceStateBefore := m.queryServiceState(ctx, m.serviceName)
	if routineEnsure && isServiceRunning(serviceStateBefore) {
		m.tracef("VNC ensure start reason=%s service=%s state=%s port=%d allowed_ips=%s credential_revision=%d remove_wallpaper=%t", reason, m.serviceName, displayServiceState(serviceStateBefore), port, allowedIPs, revision, removeWallpaper)
	} else {
		m.logf("VNC ensure start reason=%s service=%s state=%s port=%d allowed_ips=%s credential_revision=%d remove_wallpaper=%t", reason, m.serviceName, displayServiceState(serviceStateBefore), port, allowedIPs, revision, removeWallpaper)
	}
	if err := m.ensureFirewall(ctx, allowedIPs, port); err != nil {
		m.logf("VNC firewall ensure failed: %v", err)
	}
	configPath, configChanged, err := m.ensureConfig(port, password, removeWallpaper)
	if err != nil {
		m.setError(err.Error())
		return err
	}
	if routineEnsure && !configChanged {
		m.tracef("VNC config ensured path=%s changed=%t", configPath, configChanged)
	} else {
		m.logf("VNC config ensured path=%s changed=%t", configPath, configChanged)
	}
	reloadReason := ""
	if configChanged {
		reloadReason = "config_changed"
	} else if !serviceConfigLoaded {
		reloadReason = "initial_config_sync"
	}
	if err := m.ensureService(ctx, reloadReason, reason); err != nil {
		serviceState := m.queryServiceState(ctx, m.serviceName)
		listenerState := "not_listening"
		if m.listenerReady(port) {
			listenerState = "listening"
		}
		m.mu.Lock()
		m.lastReady = false
		m.lastServiceState = serviceState
		m.lastListenerState = listenerState
		m.lastError = err.Error()
		m.mu.Unlock()
		return err
	}
	m.mu.Lock()
	m.serviceConfigLoaded = true
	m.mu.Unlock()
	listenerReady := m.waitForListener(port, listenerReadyWait)
	serviceState := m.queryServiceState(ctx, m.serviceName)
	ready := listenerReady && isServiceRunning(serviceState)
	listenerState := "not_listening"
	if listenerReady {
		listenerState = "listening"
	}
	m.mu.Lock()
	m.lastReady = ready
	m.lastListenerState = listenerState
	m.lastServiceState = serviceState
	if ready {
		m.lastReadyAt = time.Now().Unix()
		m.lastError = ""
	} else {
		if listenerReady && !isServiceRunning(serviceState) {
			m.lastError = fmt.Sprintf("VNC service %s while listener accepts TCP on port %d", displayServiceState(serviceState), port)
		} else {
			m.lastError = fmt.Sprintf("VNC listener not ready on port %d", port)
		}
	}
	m.mu.Unlock()
	if ready {
		if routineEnsure && previousReady && isServiceRunning(previousServiceState) && strings.EqualFold(previousListenerState, "listening") {
			m.tracef("VNC service ready port=%d reason=%s", port, reason)
		} else {
			m.logf("VNC service ready port=%d reason=%s", port, reason)
		}
		return nil
	}
	if listenerReady && !isServiceRunning(serviceState) {
		return fmt.Errorf("VNC service %s while listener accepts TCP on port %d", displayServiceState(serviceState), port)
	}
	return fmt.Errorf("VNC listener not ready on port %d", port)
}

func (m *Manager) credentialPayload(requestID string, reason string) map[string]any {
	m.mu.Lock()
	port := m.port
	password := m.controllerPassword
	revision := m.credentialRevision
	serviceState := m.lastServiceState
	listenerState := m.lastListenerState
	ready := m.lastReady
	m.mu.Unlock()
	displayTopology, displayVirtualBounds := m.displaySnapshot()
	return map[string]any{
		"status":                 "ok",
		"agent_id":               m.agentID(),
		"request_id":             requestID,
		"reason":                 reason,
		"controller_password":    password,
		"credential_revision":    revision,
		"display_topology":       displayTopology,
		"display_virtual_bounds": displayVirtualBounds,
		"ready":                  ready,
		"service_state":          serviceState,
		"listener_state":         listenerState,
		"port":                   port,
		"auth_verified":          false,
		"auth_verify_reason":     "local_probe_disabled",
	}
}

func (m *Manager) credentialErrorPayload(requestID string, reason string, err error) map[string]any {
	m.mu.Lock()
	port := m.port
	serviceState := m.lastServiceState
	listenerState := m.lastListenerState
	m.mu.Unlock()
	detail := ""
	if err != nil {
		detail = strings.TrimSpace(err.Error())
	}
	if detail == "" {
		detail = "VNC service not ready"
	}
	displayTopology, displayVirtualBounds := m.displaySnapshot()
	return map[string]any{
		"status":                 "error",
		"error":                  "vnc_service_not_ready",
		"detail":                 detail,
		"agent_id":               m.agentID(),
		"request_id":             requestID,
		"reason":                 reason,
		"display_topology":       displayTopology,
		"display_virtual_bounds": displayVirtualBounds,
		"ready":                  false,
		"service_state":          serviceState,
		"listener_state":         listenerState,
		"port":                   port,
		"auth_verified":          false,
		"auth_verify_reason":     "local_probe_disabled",
	}
}

func (m *Manager) displaySnapshot() ([]map[string]any, map[string]any) {
	collector := collectDisplayTopology
	if m != nil && m.displayCollector != nil {
		collector = m.displayCollector
	}
	topology := collector()
	return topology, displayVirtualBounds(topology)
}

func (m *Manager) ensureConfig(port int, password string, removeWallpaper bool) (string, bool, error) {
	configDir := resolveVNCConfigDir()
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", false, err
	}
	configPath := filepath.Join(configDir, serviceName+".ini")
	hashValue, err := ultraVNCPasswordHash(password)
	if err != nil {
		return "", false, err
	}
	settings := ultraVNCSettings(port, hashValue, removeWallpaper, m.vncExe)
	content := renderUltraVNCConfig(settings)
	changed, err := writeFileIfChanged(configPath, content)
	if err != nil {
		return "", false, fmt.Errorf("write UltraVNC config %s failed: %w", configPath, err)
	}
	legacyPath := filepath.Join(configDir, "ultravnc.ini")
	if !samePath(configPath, legacyPath) {
		legacyChanged, err := writeFileIfChanged(legacyPath, content)
		if err != nil {
			return "", false, fmt.Errorf("write UltraVNC legacy config %s failed: %w", legacyPath, err)
		}
		if legacyChanged {
			changed = true
		}
	}
	return configPath, changed, nil
}

func (m *Manager) ensureService(ctx context.Context, reloadReason string, reason string) error {
	service := m.serviceName
	state := m.queryServiceState(ctx, service)
	if state == "" {
		m.logf("VNC service missing; creating service=%s exe=%s", service, m.vncExe)
		if err := m.createService(ctx, service); err != nil {
			return err
		}
		state = m.queryServiceState(ctx, service)
	}
	_ = m.configureService(ctx, service)
	state = m.stabilizeServiceState(ctx, service, state, reason)
	if isServicePending(state) {
		return fmt.Errorf("UltraVNC service is %s", displayServiceState(state))
	}
	if state == "RUNNING" {
		if strings.TrimSpace(reloadReason) != "" {
			return m.restartService(ctx, service, reloadReason)
		}
		if isRoutineEnsureReason(reason) {
			m.tracef("VNC service already running service=%s reload_required=false", service)
		} else {
			m.logf("VNC service already running service=%s reload_required=false", service)
		}
		return nil
	}
	return m.startService(ctx, service)
}

func (m *Manager) stabilizeServiceState(ctx context.Context, service string, state string, reason string) string {
	if !isServicePending(state) {
		return state
	}
	m.logf("VNC service pending service=%s state=%s reason=%s", service, displayServiceState(state), reason)
	stableState := m.waitForServiceStable(ctx, service, serviceTransitionWait)
	if !isServicePending(stableState) {
		return stableState
	}
	if strings.EqualFold(stableState, "STOP_PENDING") && m.forceKillServiceProcess(ctx, service, reason) {
		stableState = m.waitForServiceStable(ctx, service, serviceTransitionForceKillWait)
	}
	return stableState
}

func (m *Manager) startService(ctx context.Context, service string) error {
	result, err := m.runner(ctx, 30*time.Second, "sc.exe", "start", service)
	output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	alreadyRunning := isServiceAlreadyRunning(result, output, err)
	if err != nil && !alreadyRunning {
		return err
	}
	if result.ExitCode != 0 && !alreadyRunning {
		return fmt.Errorf("UltraVNC service start failed: %s", output)
	}
	m.logf("VNC service start requested service=%s exit_code=%d output=%s", service, result.ExitCode, compactLogText(output))
	verifyWait := serviceTransitionWait
	if alreadyRunning {
		verifyWait = serviceAlreadyRunningVerifyWait
	}
	state := m.waitForServiceStable(ctx, service, verifyWait)
	if !isServiceRunning(state) {
		return fmt.Errorf("UltraVNC service start did not reach RUNNING; state=%s", displayServiceState(state))
	}
	return nil
}

func isServiceAlreadyRunning(result commandResult, output string, err error) bool {
	lowerOutput := strings.ToLower(output)
	if result.ExitCode == 1056 || strings.Contains(output, "1056") || strings.Contains(lowerOutput, "already running") {
		return true
	}
	if err != nil {
		lowerError := strings.ToLower(err.Error())
		return strings.Contains(lowerError, "exit status 1056") || strings.Contains(lowerError, "already running")
	}
	return false
}

func (m *Manager) restartService(ctx context.Context, service string, reason string) error {
	m.logf("VNC service restart requested service=%s reason=%s", service, reason)
	result, err := m.runner(ctx, 30*time.Second, "sc.exe", "stop", service)
	if err != nil {
		m.logf("VNC service stop command failed service=%s error=%v", service, err)
	} else {
		output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
		if result.ExitCode != 0 && !strings.Contains(output, "1062") && !strings.Contains(strings.ToLower(output), "not been started") {
			m.logf("VNC service stop returned nonzero service=%s exit_code=%d output=%s", service, result.ExitCode, compactLogText(output))
		} else {
			m.logf("VNC service stop requested service=%s exit_code=%d output=%s", service, result.ExitCode, compactLogText(output))
		}
	}
	m.waitForServiceNotRunning(ctx, service, 10*time.Second)
	return m.startService(ctx, service)
}

func (m *Manager) waitForServiceNotRunning(ctx context.Context, service string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state := m.queryServiceState(ctx, service)
		if !isServiceRunning(state) && !isServicePending(state) {
			return true
		}
		sleepRemaining(deadline, 250*time.Millisecond)
	}
	state := m.queryServiceState(ctx, service)
	return !isServiceRunning(state) && !isServicePending(state)
}

func (m *Manager) waitForServiceStable(ctx context.Context, service string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	lastState := ""
	for time.Now().Before(deadline) {
		lastState = m.queryServiceState(ctx, service)
		if !isServicePending(lastState) {
			return lastState
		}
		sleepRemaining(deadline, 250*time.Millisecond)
	}
	if lastState == "" {
		lastState = m.queryServiceState(ctx, service)
	}
	return lastState
}

func (m *Manager) forceKillServiceProcess(ctx context.Context, service string, reason string) bool {
	pid := m.queryServicePID(ctx, service)
	if pid <= 0 {
		m.logf("VNC service pending force-kill skipped service=%s reason=%s pid=0", service, reason)
		return false
	}
	result, err := m.runner(ctx, 20*time.Second, "taskkill.exe", "/PID", strconv.Itoa(pid), "/F")
	output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	if err != nil || result.ExitCode != 0 {
		m.logf("VNC service pending force-kill failed service=%s reason=%s pid=%d exit_code=%d error=%v output=%s", service, reason, pid, result.ExitCode, err, compactLogText(output))
		return false
	}
	m.logf("VNC service pending force-kill completed service=%s reason=%s pid=%d output=%s", service, reason, pid, compactLogText(output))
	return true
}

func (m *Manager) queryServicePID(ctx context.Context, service string) int {
	result, err := m.runner(ctx, 10*time.Second, "sc.exe", "queryex", service)
	if err != nil || result.ExitCode != 0 {
		return 0
	}
	for _, line := range strings.Split(result.Stdout+"\n"+result.Stderr, "\n") {
		if !strings.Contains(strings.ToUpper(line), "PID") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pid, err := strconv.Atoi(fields[len(fields)-1])
		if err == nil && pid > 0 {
			return pid
		}
	}
	return 0
}

func sleepRemaining(deadline time.Time, maxSleep time.Duration) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return
	}
	if remaining < maxSleep {
		time.Sleep(remaining)
		return
	}
	time.Sleep(maxSleep)
}

func (m *Manager) createService(ctx context.Context, service string) error {
	if m.vncExe == "" {
		return fmt.Errorf("UltraVNC executable missing")
	}
	result, err := m.runner(
		ctx,
		30*time.Second,
		"sc.exe",
		"create",
		service,
		"binPath=",
		fmt.Sprintf(`"%s" -service`, m.vncExe),
		"start=",
		"auto",
		"type=",
		"own",
		"DisplayName=",
		serviceDisplayName,
	)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("UltraVNC service create failed: %s", strings.TrimSpace(result.Stdout+"\n"+result.Stderr))
	}
	return nil
}

func (m *Manager) configureService(ctx context.Context, service string) error {
	_, _ = m.runner(ctx, 20*time.Second, "sc.exe", "config", service, "start=", "auto", "DisplayName=", serviceDisplayName)
	_, _ = m.runner(ctx, 20*time.Second, "sc.exe", "failure", service, "reset=", "86400", "actions=", "restart/5000/restart/15000/restart/30000")
	return nil
}

func (m *Manager) ensureFirewall(ctx context.Context, allowedIPs string, port int) error {
	remote := normalizeFirewallRemote(allowedIPs)
	if remote == "" {
		return fmt.Errorf("invalid VNC firewall remote scope: %s", allowedIPs)
	}
	name := powerShellSingleQuoted(firewallRuleName)
	remoteLiteral := powerShellSingleQuoted(remote)
	command := fmt.Sprintf(
		"$ErrorActionPreference = 'Stop'; "+
			"Get-NetFirewallRule -DisplayName %s -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue; "+
			"New-NetFirewallRule -DisplayName %s -Direction Inbound -Action Allow -Protocol TCP -LocalPort %d -RemoteAddress %s -Profile Any | Out-Null",
		name, name, port, remoteLiteral,
	)
	result, err := m.runner(ctx, 30*time.Second, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s", strings.TrimSpace(result.Stdout+"\n"+result.Stderr))
	}
	return nil
}

func powerShellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func (m *Manager) queryServiceState(ctx context.Context, service string) string {
	result, err := m.runner(ctx, 10*time.Second, "sc.exe", "query", service)
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

func (m *Manager) rotateCredential(reason string) {
	password, revision := newCredential()
	m.mu.Lock()
	m.controllerPassword = password
	m.credentialRevision = revision
	m.credentialIssuedAt = time.Now()
	m.mu.Unlock()
	m.logf("VNC runtime credential rotated reason=%s revision=%d", reason, revision)
}

func (m *Manager) ensureCredentialFresh(reason string) bool {
	m.mu.Lock()
	issuedAt := m.credentialIssuedAt
	m.mu.Unlock()
	if time.Since(issuedAt) < credentialRotationInterval {
		return false
	}
	m.rotateCredential(reason)
	return true
}

func (m *Manager) ready() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastReady
}

func (m *Manager) credentialFastPathReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.TrimSpace(m.controllerPassword) != "" &&
		strings.TrimSpace(m.allowedIPs) != "" &&
		m.port > 0 &&
		m.lastReady &&
		isServiceRunning(m.lastServiceState) &&
		strings.EqualFold(strings.TrimSpace(m.lastListenerState), "listening")
}

func (m *Manager) recentlyReady(lastReadyAt int64) bool {
	return lastReadyAt > 0 && time.Now().Unix()-lastReadyAt <= recentReadyGraceSeconds
}

func (m *Manager) payloadForThisAgent(payload map[string]any) bool {
	target := cleanText(payload["agent_id"])
	return target == "" || target == m.agentID()
}

func (m *Manager) agentID() string {
	if m == nil || m.authClient == nil {
		return ""
	}
	return m.authClient.AgentID()
}

func (m *Manager) setError(message string) {
	m.mu.Lock()
	m.lastError = strings.TrimSpace(message)
	m.mu.Unlock()
}

func (m *Manager) logf(format string, args ...any) {
	if m == nil {
		return
	}
	logutil.Append(
		m.logPath,
		logutil.RetentionDaysFromConfig(m.configPath),
		"[%s] [vnc] %s",
		time.Now().Format("2006-01-02T15:04:05"),
		fmt.Sprintf(format, args...),
	)
}

func (m *Manager) tracef(format string, args ...any) {
	if !vncTraceEnabled() {
		return
	}
	m.logf("vnc_trace "+format, args...)
}

func isRoutineEnsureReason(reason string) bool {
	return strings.EqualFold(strings.TrimSpace(reason), "always_on_check")
}

func isPassiveDisconnectEnsureReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "operator_disconnect", "component_unmount", "vnc_session_end":
		return true
	default:
		return false
	}
}

func vncTraceEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BOREALIS_VNC_TRACE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, name, args...)
	output, err := cmd.CombinedOutput()
	result := commandResult{Stdout: string(output)}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		result.ExitCode = 1
	}
	return result, err
}

func newCredential() (string, int64) {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		return strconv.FormatInt(time.Now().UnixNano()%100000000, 16), time.Now().UnixMilli()
	}
	return hex.EncodeToString(buffer), time.Now().UnixMilli()
}

func resolveVNCPort(value any) int {
	raw := value
	if raw == nil {
		raw = os.Getenv("BOREALIS_VNC_PORT")
	}
	port, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(raw)))
	if err != nil || port < 1 || port > 65535 {
		return defaultVNCPort
	}
	return port
}

func alwaysOnInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("BOREALIS_VNC_ALWAYS_ON_INTERVAL_SECONDS"))
	if raw == "" {
		return defaultAlwaysOnInterval
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 5 {
		return defaultAlwaysOnInterval
	}
	return time.Duration(seconds) * time.Second
}

func shouldRotateCredential(reason string) bool {
	return strings.EqualFold(strings.TrimSpace(reason), "vnc_auth_retry")
}

func resolveVNCExe() string {
	if override := strings.TrimSpace(os.Getenv("BOREALIS_VNC_SERVER_BIN")); override != "" && fileExists(override) {
		return override
	}
	candidates := []string{}
	for _, envName := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		base := strings.TrimSpace(os.Getenv(envName))
		if base == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(base, "uvnc bvba", "UltraVNC", "winvnc.exe"),
			filepath.Join(base, "UltraVNC", "winvnc.exe"),
			filepath.Join(base, "uvnc bvba", "UltraVNC", "winvnc64.exe"),
		)
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func resolveVNCConfigDir() string {
	if override := strings.TrimSpace(os.Getenv("BOREALIS_VNC_CONFIG_DIR")); override != "" {
		return override
	}
	programData := strings.TrimSpace(os.Getenv("ProgramData"))
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return filepath.Join(programData, "UltraVNC")
}

func ultraVNCSettings(port int, passwordHash string, removeWallpaper bool, vncExe string) map[string]string {
	settings := map[string]string{
		"UseRegistry":        "0",
		"AuthRequired":       "1",
		"MSLogonRequired":    "0",
		"NewMSLogon":         "0",
		"PortNumber":         strconv.Itoa(port),
		"AutoPortSelect":     "0",
		"SocketConnect":      "1",
		"AllowLoopback":      "1",
		"LoopbackOnly":       "0",
		"HTTPConnect":        "0",
		"AllowShutdown":      "1",
		"DisableTrayIcon":    "1",
		"EnableFileTransfer": "0",
		"RemoveWallpaper":    boolInt(removeWallpaper),
		"TurboMode":          "1",
		"PollUnderCursor":    "0",
		"PollForeground":     "0",
		"PollFullScreen":     "1",
		"OnlyPollConsole":    "0",
		"OnlyPollOnEvent":    "0",
		"EnableVirtual":      "0",
		"SingleWindow":       "0",
		"passwd":             passwordHash,
		"passwd2":            "",
	}
	root := filepath.Dir(vncExe)
	if root != "." && root != "" {
		if fileExists(filepath.Join(root, "ddengine64.dll")) || fileExists(filepath.Join(root, "ddengine.dll")) {
			settings["EnableDriver"] = "1"
		} else {
			settings["EnableDriver"] = "0"
		}
		if fileExists(filepath.Join(root, "vnchooks.dll")) {
			settings["EnableHook"] = "1"
		} else {
			settings["EnableHook"] = "0"
		}
	}
	return settings
}

func renderUltraVNCConfig(settings map[string]string) string {
	order := []string{
		"UseRegistry", "AuthRequired", "MSLogonRequired", "NewMSLogon", "PortNumber", "AutoPortSelect",
		"SocketConnect", "AllowLoopback", "LoopbackOnly", "HTTPConnect", "AllowShutdown", "DisableTrayIcon",
		"EnableFileTransfer", "RemoveWallpaper", "TurboMode", "PollUnderCursor", "PollForeground",
		"PollFullScreen", "OnlyPollConsole", "OnlyPollOnEvent", "EnableDriver", "EnableHook",
		"EnableVirtual", "SingleWindow", "passwd", "passwd2",
	}
	var builder strings.Builder
	builder.WriteString("[UltraVNC]\n")
	for _, key := range order {
		value, ok := settings[key]
		if !ok {
			continue
		}
		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(value)
		builder.WriteString("\n")
	}
	return builder.String()
}

func ultraVNCPasswordHash(password string) (string, error) {
	raw := []byte(password)
	if len(raw) > 8 {
		raw = raw[:8]
	}
	blockInput := make([]byte, 8)
	copy(blockInput, raw)
	block, err := des.NewCipher([]byte{0xE8, 0x4A, 0xD6, 0x60, 0xC4, 0x72, 0x1A, 0xE0})
	if err != nil {
		return "", err
	}
	encrypted := make([]byte, 8)
	block.Encrypt(encrypted, blockInput)
	return strings.ToUpper(hex.EncodeToString(encrypted)) + "00", nil
}

func normalizeFirewallRemote(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	if strings.Contains(text, ",") {
		text = strings.TrimSpace(strings.Split(text, ",")[0])
	}
	host := strings.TrimSuffix(text, "/32")
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return ""
	}
	return ip.String() + "/32"
}

func parseAllowedIPs(value any) string {
	switch typed := value.(type) {
	case []any:
		if len(typed) == 0 {
			return ""
		}
		return normalizeFirewallRemote(fmt.Sprint(typed[0]))
	case []string:
		if len(typed) == 0 {
			return ""
		}
		return normalizeFirewallRemote(typed[0])
	default:
		return normalizeFirewallRemote(fmt.Sprint(value))
	}
}

func asMap(payload any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	if typed, ok := payload.(map[string]any); ok {
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

func writeFileIfChanged(path string, content string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err == nil && string(raw) == content {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return false, err
	}
	return true, nil
}

func compactLogText(value string) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(text) <= 240 {
		return text
	}
	return text[:237] + "..."
}

func boolInt(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func displayServiceState(value string) string {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if normalized == "" {
		return "Stopped"
	}
	return normalized
}

func isServiceRunning(value string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	return normalized == "RUNNING"
}

func isServicePending(value string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	return strings.HasSuffix(normalized, "_PENDING")
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func samePath(a string, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
