package wireguardtunnel

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/auth"
	"github.com/bunny-lab-io/borealis/go-agent/internal/logutil"
	"github.com/bunny-lab-io/borealis/go-agent/internal/scripts"
)

const (
	defaultTunnelName   = "wireguard"
	tunnelDisplayName   = "Borealis Agent - WireGuard"
	defaultInterface    = "wireguard"
	legacyInterface     = "borealis"
	defaultInterfaceMTU = 1420
	defaultKeepalive    = 30
	defaultEnsureDelay  = 10 * time.Second
	defaultEnsureEvery  = 60 * time.Second
	commandTimeout      = 45 * time.Second
	readyNotifyCooldown = 60 * time.Second
	firewallRuleName    = "Borealis Agent - WireGuard"
	idleAddress         = "169.254.255.254/32"
)

type AuthClient interface {
	PostJSON(ctx context.Context, path string, requestPayload any, responsePayload any) (any, error)
	AgentID() string
	LoadServerSigningKey() string
	StoreServerSigningKey(value string) error
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

func (a authClientAdapter) LoadServerSigningKey() string {
	return a.client.LoadServerSigningKey()
}

func (a authClientAdapter) StoreServerSigningKey(value string) error {
	return a.client.StoreServerSigningKey(value)
}

type Manager struct {
	authClient     AuthClient
	hostname       string
	serviceMode    string
	configPath     string
	baseDir        string
	logPath        string
	platform       string
	interfaceName  string
	wireguardExe   string
	wgQuick        string
	wg             string
	ip             string
	clientPrivate  string
	clientPublic   string
	runner         commandRunner
	statusReporter func(context.Context, string, string, string) error

	sessionMu                sync.Mutex
	mu                       sync.Mutex
	started                  bool
	loopRunning              bool
	supported                bool
	unsupportedReason        string
	lastError                string
	lastEnsureAt             int64
	lastReadyAt              int64
	lastReadyNotificationKey string
	lastTunnelSnapshot       map[string]any
	session                  *SessionConfig
	ensureWake               chan string
}

type SessionConfig struct {
	Token            map[string]any
	TunnelID         string
	VirtualIP        string
	AllowedIPs       string
	Endpoint         string
	ServerPublicKey  string
	AllowedPorts     string
	IdleSeconds      int
	PresharedKey     string
	ClientPrivateKey string
	ClientPublicKey  string
	ForceRestart     bool
	RestartReason    string
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

func New(client *auth.Client, hostname string, serviceMode string, configPath string) *Manager {
	baseDir := filepath.Dir(configPath)
	logPath := filepath.Join(baseDir, "Logs", "WireGuard", "wireguard.log")
	privateKey, publicKey := generateWireGuardKeys()
	manager := &Manager{
		authClient:         authClientAdapter{client: client},
		hostname:           strings.TrimSpace(hostname),
		serviceMode:        auth.NormalizeServiceMode(serviceMode),
		configPath:         configPath,
		baseDir:            baseDir,
		logPath:            logPath,
		platform:           runtime.GOOS,
		interfaceName:      resolveInterfaceName(),
		wireguardExe:       resolveWireGuardExe(),
		wgQuick:            resolveExecutable("wg-quick"),
		wg:                 resolveExecutable("wg"),
		ip:                 resolveExecutable("ip"),
		clientPrivate:      privateKey,
		clientPublic:       publicKey,
		runner:             runCommand,
		supported:          runtime.GOOS == "windows" || runtime.GOOS == "linux",
		ensureWake:         make(chan string, 4),
		lastTunnelSnapshot: map[string]any{},
	}
	if !manager.supported {
		manager.unsupportedReason = fmt.Sprintf("WireGuard tunnel is unsupported on %s.", runtime.GOOS)
	}
	return manager
}

func (m *Manager) SetStatusReporter(reporter func(context.Context, string, string, string) error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.statusReporter = reporter
	m.mu.Unlock()
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
	m.logf("WireGuard ensure supervisor starting platform=%s config=%s", m.platform, m.configPath)
	go m.ensureLoop(ctx)
}

func (m *Manager) Stop(ctx context.Context) {
	if m == nil {
		return
	}
	_ = m.stopSession(ctx, "agent_shutdown", true)
}

func (m *Manager) RequestEnsure(reason string) {
	if m == nil {
		return
	}
	cleanReason := cleanText(reason)
	if cleanReason == "" {
		cleanReason = "manual"
	}
	select {
	case m.ensureWake <- cleanReason:
	default:
	}
}

func (m *Manager) Health() RoleHealth {
	m.mu.Lock()
	supported := m.supported
	unsupportedReason := m.unsupportedReason
	loopRunning := m.loopRunning
	lastError := m.lastError
	lastEnsureAt := m.lastEnsureAt
	lastReadyAt := m.lastReadyAt
	session := cloneSession(m.session)
	snapshot := cloneMap(m.lastTunnelSnapshot)
	m.mu.Unlock()

	serviceState := m.serviceState(context.Background())
	tunnelID := ""
	peerIP := ""
	endpoint := ""
	if session != nil {
		tunnelID = session.TunnelID
		peerIP = strings.Split(session.VirtualIP, "/")[0]
		endpoint = session.Endpoint
	}
	if tunnelID == "" {
		tunnelID = cleanText(snapshot["tunnel_id"])
	}
	if peerIP == "" {
		peerIP = strings.Split(cleanText(snapshot["virtual_ip"]), "/")[0]
	}
	if endpoint == "" {
		endpoint = cleanText(snapshot["endpoint"])
	}
	details := map[string]any{
		"running_status":    fallbackText(serviceState, "Stopped"),
		"wireguard_peer_ip": peerIP,
		"tunnel_id":         tunnelID,
		"endpoint":          endpoint,
		"last_ensure_at":    strconv.FormatInt(lastEnsureAt, 10),
		"last_ready_at":     strconv.FormatInt(lastReadyAt, 10),
		"runtime":           "go",
	}
	if lastError != "" {
		details["last_error"] = lastError
	}
	if !supported {
		details["running_status"] = "Unsupported"
		return RoleHealth{
			Status:     "unsupported",
			StatusCode: "unsupported",
			Detail:     fallbackText(unsupportedReason, "WireGuard tunnel is unsupported on this platform."),
			Details:    details,
		}
	}
	if !loopRunning {
		details["running_status"] = "Stopped"
		return RoleHealth{
			Status:     "pending",
			StatusCode: "pending",
			Detail:     "Waiting for WireGuard ensure loop.",
			Details:    details,
		}
	}
	if serviceState == "RUNNING" && peerIP != "" {
		return RoleHealth{
			Status:     "healthy",
			StatusCode: "healthy",
			Detail:     "Persistent WireGuard tunnel active.",
			Details:    details,
		}
	}
	if lastError != "" {
		return RoleHealth{
			Status:     "recovering",
			StatusCode: "recovering",
			Detail:     lastError,
			Details:    details,
		}
	}
	return RoleHealth{
		Status:     "recovering",
		StatusCode: "recovering",
		Detail:     "Awaiting persistent WireGuard tunnel session.",
		Details:    details,
	}
}

func (m *Manager) HandleStart(ctx context.Context, payload any) (any, error) {
	if m == nil {
		return map[string]any{"ok": false, "error": "wireguard_unavailable"}, nil
	}
	body, ok := payload.(map[string]any)
	if !ok {
		return map[string]any{"ok": false, "error": "invalid_payload"}, nil
	}
	if !m.matchesTarget(body) {
		return map[string]any{"ok": false, "error": "not_for_agent"}, nil
	}
	session, err := m.buildSession(body)
	if err != nil {
		m.recordError("WireGuard start payload rejected: " + err.Error())
		return map[string]any{"ok": false, "error": "invalid_payload", "message": err.Error()}, nil
	}
	go func() {
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := m.startSession(startCtx, session); err != nil {
			m.recordError("WireGuard start failed: " + err.Error())
			return
		}
		m.clearError()
		m.notifyReady(context.Background(), session, "vpn_tunnel_start")
	}()
	return map[string]any{"ok": true, "status": "accepted", "tunnel_id": session.TunnelID}, nil
}

func (m *Manager) HandleStop(ctx context.Context, payload any) (any, error) {
	body, _ := payload.(map[string]any)
	if len(body) > 0 && !m.matchesTarget(body) {
		return map[string]any{"ok": false, "error": "not_for_agent"}, nil
	}
	reason := cleanText(firstValue(body, "reason"))
	if reason == "" {
		reason = "server_stop"
	}
	m.logf("WireGuard stop requested reason=%s; persistent tunnel remains managed by ensure loop.", reason)
	return map[string]any{"ok": true, "status": "ignored_persistent"}, nil
}

func (m *Manager) HandleActivity(ctx context.Context, payload any) (any, error) {
	return map[string]any{"ok": true, "status": "noted"}, nil
}

func (m *Manager) ensureLoop(ctx context.Context) {
	defer func() {
		m.mu.Lock()
		m.loopRunning = false
		m.mu.Unlock()
	}()
	delay := envDuration("BOREALIS_WIREGUARD_ENSURE_DELAY", defaultEnsureDelay, 0, 5*time.Minute)
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case reason := <-m.ensureWake:
			timer.Stop()
			m.runEnsureCycle(ctx, reason)
		}
	}
	m.runEnsureCycle(ctx, "agent_boot")
	interval := envDuration("BOREALIS_WIREGUARD_ENSURE_INTERVAL", defaultEnsureEvery, 15*time.Second, time.Hour)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.runEnsureCycle(ctx, "agent_boot")
		case reason := <-m.ensureWake:
			m.runEnsureCycle(ctx, reason)
		}
	}
}

func (m *Manager) runEnsureCycle(ctx context.Context, reason string) {
	if m == nil || !m.supported {
		return
	}
	cleanReason := fallbackText(cleanText(reason), "agent_boot")
	m.mu.Lock()
	m.lastEnsureAt = time.Now().Unix()
	reporter := m.statusReporter
	m.mu.Unlock()
	if reporter != nil {
		_ = reporter(ctx, "wireguard_starting", "active", "Ensuring WireGuard tunnel.")
	}
	payload := map[string]any{
		"agent_id": m.authClient.AgentID(),
		"reason":   cleanReason,
	}
	var response map[string]any
	_, err := m.authClient.PostJSON(ctx, "/api/agent/vpn/ensure", payload, &response)
	if err != nil {
		m.recordError("WireGuard ensure request failed: " + err.Error())
		return
	}
	session, err := m.buildSession(response)
	if err != nil {
		m.recordError("WireGuard ensure response rejected: " + err.Error())
		return
	}
	if m.sessionConfigMatchesLive(ctx, session) {
		m.repairLiveSession(ctx, session)
		m.notifyReady(ctx, session, cleanReason)
		m.clearError()
		return
	}
	if err := m.startSession(ctx, session); err != nil {
		m.recordError("WireGuard ensure failed: " + err.Error())
		return
	}
	m.clearError()
	m.notifyReady(ctx, session, cleanReason)
}

func (m *Manager) buildSession(payload map[string]any) (*SessionConfig, error) {
	m.rememberTunnelSnapshot(payload)
	payloadAgentID := cleanText(firstValue(payload, "agent_id", "agent_guid"))
	if payloadAgentID != "" && !strings.EqualFold(payloadAgentID, m.authClient.AgentID()) {
		return nil, fmt.Errorf("payload targets another agent")
	}
	token := mapStringAny(firstValue(payload, "token", "orchestration_token"))
	if token == nil {
		return nil, fmt.Errorf("missing token")
	}
	tunnelID := cleanText(firstValue(payload, "tunnel_id"))
	if tunnelID == "" {
		tunnelID = cleanText(token["tunnel_id"])
	}
	if tunnelID == "" {
		return nil, fmt.Errorf("missing tunnel_id")
	}
	virtualIP := cleanText(firstValue(payload, "virtual_ip", "client_virtual_ip"))
	if !validSingleHostRoute(virtualIP) {
		return nil, fmt.Errorf("virtual_ip must be /32")
	}
	allowedIPs := parseAllowedIPs(firstValue(payload, "allowed_ips"), cleanText(firstValue(payload, "engine_virtual_ip", "engine_ip")))
	if !validSingleHostRoute(allowedIPs) {
		return nil, fmt.Errorf("allowed_ips must be single /32")
	}
	endpoint := cleanText(firstValue(payload, "endpoint", "server_endpoint"))
	serverPublicKey := cleanText(firstValue(payload, "server_public_key", "public_key"))
	if endpoint == "" || serverPublicKey == "" {
		return nil, fmt.Errorf("missing endpoint or server_public_key")
	}
	return &SessionConfig{
		Token:            token,
		TunnelID:         tunnelID,
		VirtualIP:        virtualIP,
		AllowedIPs:       allowedIPs,
		Endpoint:         endpoint,
		ServerPublicKey:  serverPublicKey,
		AllowedPorts:     formatPorts(parseAllowedPorts(firstValue(payload, "allowed_ports"))),
		IdleSeconds:      asInt(firstValue(payload, "idle_seconds"), 0),
		PresharedKey:     cleanText(firstValue(payload, "preshared_key")),
		ClientPrivateKey: cleanText(firstValue(payload, "client_private_key")),
		ClientPublicKey:  cleanText(firstValue(payload, "client_public_key")),
		ForceRestart:     asBool(firstValue(payload, "force_restart")),
		RestartReason:    cleanText(firstValue(payload, "restart_reason")),
	}, nil
}

func (m *Manager) startSession(ctx context.Context, session *SessionConfig) error {
	if session == nil {
		return fmt.Errorf("nil session")
	}
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	if err := m.validateToken(session.Token); err != nil {
		return err
	}
	m.mu.Lock()
	current := cloneSession(m.session)
	m.mu.Unlock()
	if current != nil && current.TunnelID == session.TunnelID && sessionEquivalent(current, session) && !session.ForceRestart {
		if m.serviceState(ctx) == "RUNNING" {
			m.repairLiveSession(ctx, session)
			m.mu.Lock()
			m.session = session
			m.mu.Unlock()
			m.logf("WireGuard session already active tunnel_id=%s", session.TunnelID)
			return nil
		}
	}
	rendered := m.renderConfig(session)
	if err := m.writeConfig(rendered); err != nil {
		return err
	}
	switch m.platform {
	case "windows":
		if err := m.applyWindowsSession(ctx, session); err != nil {
			return err
		}
	case "linux":
		if err := m.applyLinuxSession(ctx); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported platform %s", m.platform)
	}
	m.mu.Lock()
	m.session = session
	m.mu.Unlock()
	m.logf("WireGuard session started tunnel_id=%s virtual_ip=%s endpoint=%s", session.TunnelID, session.VirtualIP, session.Endpoint)
	return nil
}

func (m *Manager) repairLiveSession(ctx context.Context, session *SessionConfig) {
	if session == nil {
		return
	}
	_ = primeEnginePath(session)
	switch m.platform {
	case "windows":
		m.ensureWindowsServiceDisplayName(ctx)
		m.ensureWindowsFirewall(ctx, session.AllowedIPs, session.AllowedPorts)
	case "linux":
		m.ensureLinuxMTU(ctx)
	}
}

func (m *Manager) stopSession(ctx context.Context, reason string, ignoreMissing bool) error {
	switch m.platform {
	case "windows":
		if err := m.writeConfig(m.renderIdleConfig()); err != nil && !ignoreMissing {
			return err
		}
		_ = m.restartWindowsService(ctx)
	case "linux":
		_ = m.linuxDown(ctx)
	}
	m.mu.Lock()
	m.session = nil
	m.mu.Unlock()
	m.logf("WireGuard session stopped reason=%s", fallbackText(cleanText(reason), "stop"))
	return nil
}

func (m *Manager) applyWindowsSession(ctx context.Context, session *SessionConfig) error {
	if strings.TrimSpace(m.wireguardExe) == "" {
		return fmt.Errorf("wireguard.exe not found")
	}
	if !m.windowsServiceExists(ctx) {
		result, err := m.runner(ctx, commandTimeout, m.wireguardExe, "/installtunnelservice", m.configPathForPlatform())
		if err != nil {
			return err
		}
		if result.ExitCode != 0 && !strings.Contains(strings.ToLower(result.Stdout+result.Stderr), "already") {
			return fmt.Errorf("install WireGuard tunnel service failed: %s", commandDetail(result))
		}
	} else if serviceConfig := m.windowsServiceConfigPath(ctx); serviceConfig != "" && !samePath(serviceConfig, m.configPathForPlatform()) {
		m.logf("WireGuard service bound to stale config path=%s expected=%s; reinstalling.", serviceConfig, m.configPathForPlatform())
		_ = m.uninstallWindowsService(ctx)
		result, err := m.runner(ctx, commandTimeout, m.wireguardExe, "/installtunnelservice", m.configPathForPlatform())
		if err != nil {
			return err
		}
		if result.ExitCode != 0 && !strings.Contains(strings.ToLower(result.Stdout+result.Stderr), "already") {
			return fmt.Errorf("install WireGuard tunnel service failed: %s", commandDetail(result))
		}
	}
	if err := m.restartWindowsService(ctx); err != nil {
		return err
	}
	if state := m.waitForWindowsState(ctx, 8*time.Second, "RUNNING", "START_PENDING"); state != "RUNNING" && state != "START_PENDING" {
		return fmt.Errorf("WireGuard tunnel service state after restart: %s", fallbackText(state, "unknown"))
	}
	m.ensureWindowsServiceDisplayName(ctx)
	m.ensureWindowsFirewall(ctx, session.AllowedIPs, session.AllowedPorts)
	return nil
}

func (m *Manager) applyLinuxSession(ctx context.Context) error {
	if strings.TrimSpace(m.wgQuick) == "" {
		return fmt.Errorf("wg-quick not found")
	}
	_ = m.linuxDown(ctx)
	result, err := m.runner(ctx, commandTimeout, m.wgQuick, "up", m.configPathForPlatform())
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		_ = m.linuxDown(ctx)
		result, err = m.runner(ctx, commandTimeout, m.wgQuick, "up", m.configPathForPlatform())
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("wg-quick up failed: %s", commandDetail(result))
		}
	}
	m.ensureLinuxMTU(ctx)
	return nil
}

func (m *Manager) sessionConfigMatchesLive(ctx context.Context, desired *SessionConfig) bool {
	m.mu.Lock()
	current := cloneSession(m.session)
	m.mu.Unlock()
	if current == nil || desired == nil {
		return false
	}
	if !sessionEquivalent(current, desired) && current.TunnelID == desired.TunnelID {
		return false
	}
	return current.TunnelID == desired.TunnelID && m.serviceState(ctx) == "RUNNING"
}

func (m *Manager) notifyReady(ctx context.Context, session *SessionConfig, reason string) {
	if session == nil || m.serviceState(ctx) != "RUNNING" {
		return
	}
	ports := parseAllowedPorts(session.AllowedPorts)
	key := session.TunnelID + "|" + formatPorts(ports)
	now := time.Now()
	m.mu.Lock()
	if reason == "agent_boot" && m.lastReadyNotificationKey == key && now.Unix()-m.lastReadyAt < int64(readyNotifyCooldown/time.Second) {
		m.mu.Unlock()
		return
	}
	m.lastReadyNotificationKey = key
	m.lastReadyAt = now.Unix()
	reporter := m.statusReporter
	m.mu.Unlock()
	payload := map[string]any{
		"agent_id":      m.authClient.AgentID(),
		"tunnel_id":     session.TunnelID,
		"virtual_ip":    session.VirtualIP,
		"allowed_ports": ports,
		"service_state": "RUNNING",
		"reason":        fallbackText(cleanText(reason), "unknown"),
	}
	if _, err := m.authClient.PostJSON(ctx, "/api/agent/vpn/ready", payload, nil); err != nil {
		m.recordError("WireGuard readiness report failed: " + err.Error())
		return
	}
	if reporter != nil {
		_ = reporter(ctx, "wireguard_online", "complete", "WireGuard tunnel is online.")
	}
	m.logf("WireGuard readiness reported tunnel_id=%s ports=%s reason=%s", session.TunnelID, formatPorts(ports), fallbackText(cleanText(reason), "unknown"))
}

func (m *Manager) validateToken(token map[string]any) error {
	if token == nil {
		return fmt.Errorf("missing token")
	}
	for _, field := range []string{"agent_id", "tunnel_id", "expires_at", "port"} {
		if cleanText(token[field]) == "" {
			return fmt.Errorf("missing token field %s", field)
		}
	}
	if m.authClient != nil && !strings.EqualFold(cleanText(token["agent_id"]), m.authClient.AgentID()) {
		return fmt.Errorf("token targets another agent")
	}
	expiresAt := asFloat(token["expires_at"])
	if expiresAt <= float64(time.Now().Unix()) {
		return fmt.Errorf("token expired")
	}
	port := asInt(token["port"], 0)
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid token port")
	}
	signature := cleanText(token["signature"])
	signingKey := cleanText(token["signing_key"])
	sigAlg := strings.ToLower(cleanText(token["sig_alg"]))
	if sigAlg != "" && sigAlg != "ed25519" && sigAlg != "eddsa" {
		return fmt.Errorf("unsupported token signature algorithm")
	}
	storedKey := ""
	if m.authClient != nil {
		storedKey = cleanText(m.authClient.LoadServerSigningKey())
	}
	if signature == "" {
		if signingKey != "" || sigAlg != "" || storedKey != "" {
			return fmt.Errorf("token signature missing")
		}
		return nil
	}
	payloadBytes, err := canonicalTokenPayload(token)
	if err != nil {
		return err
	}
	candidates := []string{}
	if signingKey != "" {
		candidates = append(candidates, signingKey)
	}
	if storedKey != "" && storedKey != signingKey {
		candidates = append(candidates, storedKey)
	}
	for _, candidate := range candidates {
		if scripts.VerifySignature(payloadBytes, signature, candidate) {
			if m.authClient != nil && candidate != "" {
				_ = m.authClient.StoreServerSigningKey(candidate)
			}
			return nil
		}
	}
	return fmt.Errorf("token signature invalid")
}

func canonicalTokenPayload(token map[string]any) ([]byte, error) {
	payload := map[string]any{}
	for key, value := range token {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "signature", "signing_key", "sig_alg":
			continue
		default:
			payload[key] = value
		}
	}
	return json.Marshal(payload)
}

func (m *Manager) renderConfig(session *SessionConfig) string {
	privateKey := cleanText(session.ClientPrivateKey)
	if privateKey == "" {
		privateKey = m.clientPrivate
	}
	lines := []string{
		"[Interface]",
		"PrivateKey = " + privateKey,
		"Address = " + session.VirtualIP,
		fmt.Sprintf("MTU = %d", interfaceMTU()),
		"",
		"[Peer]",
		"PublicKey = " + session.ServerPublicKey,
		"AllowedIPs = " + session.AllowedIPs,
		"Endpoint = " + session.Endpoint,
		fmt.Sprintf("PersistentKeepalive = %d", keepaliveSeconds()),
	}
	if strings.TrimSpace(session.PresharedKey) != "" {
		lines = append(lines, "PresharedKey = "+strings.TrimSpace(session.PresharedKey))
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m *Manager) renderIdleConfig() string {
	return strings.Join([]string{
		"[Interface]",
		"PrivateKey = " + m.clientPrivate,
		"Address = " + idleAddress,
		"ListenPort = 0",
		fmt.Sprintf("MTU = %d", interfaceMTU()),
	}, "\n") + "\n"
}

func (m *Manager) configPathForPlatform() string {
	return filepath.Join(m.baseDir, "wireguard.conf")
}

func (m *Manager) tunnelName() string {
	configPath := ""
	if m != nil {
		configPath = m.configPathForPlatform()
	}
	raw := strings.TrimSuffix(filepath.Base(configPath), filepath.Ext(configPath))
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" || cleaned == "." || cleaned == string(filepath.Separator) {
		cleaned = defaultTunnelName
	}
	return cleaned
}

func (m *Manager) tunnelServiceID() string {
	return "WireGuardTunnel$" + m.tunnelName()
}

func (m *Manager) writeConfig(text string) error {
	path := m.configPathForPlatform()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

func (m *Manager) serviceState(ctx context.Context) string {
	switch m.platform {
	case "windows":
		result, err := m.runner(ctx, commandTimeout, "sc.exe", "query", m.tunnelServiceID())
		if err != nil || result.ExitCode != 0 {
			return ""
		}
		text := result.Stdout + "\n" + result.Stderr
		for _, line := range strings.Split(text, "\n") {
			if !strings.Contains(strings.ToUpper(line), "STATE") {
				continue
			}
			parts := regexp.MustCompile(`STATE\s*:\s*\d+\s+(\w+)`).FindStringSubmatch(line)
			if len(parts) == 2 {
				return strings.ToUpper(parts[1])
			}
		}
	case "linux":
		if m.wg != "" {
			result, err := m.runner(ctx, commandTimeout, m.wg, "show", m.interfaceName)
			if err == nil && result.ExitCode == 0 {
				return "RUNNING"
			}
		}
		if m.ip != "" {
			result, err := m.runner(ctx, commandTimeout, m.ip, "link", "show", "dev", m.interfaceName)
			if err == nil && result.ExitCode == 0 {
				return "RUNNING"
			}
		}
	}
	return ""
}

func (m *Manager) windowsServiceExists(ctx context.Context) bool {
	result, err := m.runner(ctx, commandTimeout, "sc.exe", "query", m.tunnelServiceID())
	return err == nil && result.ExitCode == 0
}

func (m *Manager) windowsServiceConfigPath(ctx context.Context) string {
	result, err := m.runner(ctx, commandTimeout, "sc.exe", "qc", m.tunnelServiceID())
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	text := result.Stdout + "\n" + result.Stderr
	match := regexp.MustCompile(`(?i)/tunnelservice\s+"([^"]+)"`).FindStringSubmatch(text)
	if len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	match = regexp.MustCompile(`(?i)/tunnelservice\s+(\S+)`).FindStringSubmatch(text)
	if len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func (m *Manager) uninstallWindowsService(ctx context.Context) error {
	if strings.TrimSpace(m.wireguardExe) == "" {
		return fmt.Errorf("wireguard.exe not found")
	}
	_, _ = m.runner(ctx, commandTimeout, "sc.exe", "stop", m.tunnelServiceID())
	for _, name := range uniqueStrings([]string{m.tunnelName(), "Borealis", "borealis-wg"}) {
		_, _ = m.runner(ctx, commandTimeout, m.wireguardExe, "/uninstalltunnelservice", name)
	}
	for _, serviceID := range uniqueStrings([]string{m.tunnelServiceID(), "WireGuardTunnel$Borealis", "WireGuardTunnel$borealis-wg"}) {
		_, _ = m.runner(ctx, commandTimeout, "sc.exe", "delete", serviceID)
	}
	return nil
}

func (m *Manager) restartWindowsService(ctx context.Context) error {
	_, _ = m.runner(ctx, commandTimeout, "sc.exe", "stop", m.tunnelServiceID())
	time.Sleep(time.Second)
	result, err := m.runner(ctx, commandTimeout, "sc.exe", "start", m.tunnelServiceID())
	if err != nil {
		return err
	}
	if result.ExitCode != 0 && !strings.Contains(strings.ToLower(result.Stdout+result.Stderr), "already") {
		return fmt.Errorf("sc start failed: %s", commandDetail(result))
	}
	return nil
}

func (m *Manager) waitForWindowsState(ctx context.Context, timeout time.Duration, states ...string) string {
	want := map[string]bool{}
	for _, state := range states {
		want[strings.ToUpper(state)] = true
	}
	deadline := time.Now().Add(timeout)
	for {
		state := m.serviceState(ctx)
		if want[state] {
			return state
		}
		if time.Now().After(deadline) {
			return state
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (m *Manager) ensureWindowsServiceDisplayName(ctx context.Context) {
	_, _ = m.runner(ctx, commandTimeout, "sc.exe", "config", m.tunnelServiceID(), "DisplayName=", tunnelDisplayName)
}

func (m *Manager) ensureWindowsFirewall(ctx context.Context, allowedIPs string, allowedPorts string) {
	remote := strings.TrimSpace(allowedIPs)
	if !validSingleHostRoute(remote) {
		m.logf("WireGuard firewall skipped invalid remote=%s", remote)
		return
	}
	ports := parseAllowedPorts(allowedPorts)
	if len(ports) == 0 {
		ports = []int{22, 5900, 47002}
	}
	portExpression := powerShellPortArray(ports)
	for _, protocol := range []string{"TCP", "UDP"} {
		rule := firewallRuleName + " (" + protocol + ")"
		removeCommand := fmt.Sprintf("Get-NetFirewallRule -DisplayName %s -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue", powerShellLiteral(rule))
		_, _ = m.runner(ctx, commandTimeout, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", removeCommand)
		addCommand := fmt.Sprintf("New-NetFirewallRule -DisplayName %s -Direction Inbound -Action Allow -Protocol %s -LocalPort %s -RemoteAddress %s -Profile Any | Out-Null", powerShellLiteral(rule), protocol, portExpression, powerShellLiteral(remote))
		result, err := m.runner(ctx, commandTimeout, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", addCommand)
		if err != nil || result.ExitCode != 0 {
			m.logf("WireGuard firewall rule create failed protocol=%s detail=%s err=%v", protocol, commandDetail(result), err)
		}
	}
}

func (m *Manager) linuxDown(ctx context.Context) error {
	if m.wgQuick != "" {
		_, _ = m.runner(ctx, commandTimeout, m.wgQuick, "down", m.configPathForPlatform())
	}
	if m.ip != "" {
		for _, interfaceName := range uniqueStrings([]string{m.interfaceName, legacyInterface}) {
			_, _ = m.runner(ctx, commandTimeout, m.ip, "link", "delete", "dev", interfaceName)
		}
	}
	return nil
}

func (m *Manager) ensureLinuxMTU(ctx context.Context) {
	if m.ip == "" {
		return
	}
	result, err := m.runner(ctx, commandTimeout, m.ip, "link", "set", "dev", m.interfaceName, "mtu", strconv.Itoa(interfaceMTU()))
	if err != nil || result.ExitCode != 0 {
		m.logf("WireGuard Linux MTU apply failed detail=%s err=%v", commandDetail(result), err)
	}
}

func (m *Manager) rememberTunnelSnapshot(payload map[string]any) {
	if payload == nil {
		return
	}
	token := mapStringAny(firstValue(payload, "token", "orchestration_token"))
	tunnelID := cleanText(payload["tunnel_id"])
	if tunnelID == "" && token != nil {
		tunnelID = cleanText(token["tunnel_id"])
	}
	snapshot := map[string]any{
		"tunnel_id":         tunnelID,
		"virtual_ip":        cleanText(firstValue(payload, "virtual_ip", "client_virtual_ip")),
		"endpoint":          cleanText(firstValue(payload, "endpoint", "server_endpoint")),
		"server_public_key": cleanText(firstValue(payload, "server_public_key", "public_key")),
		"observed_at":       time.Now().Unix(),
	}
	m.mu.Lock()
	m.lastTunnelSnapshot = snapshot
	m.mu.Unlock()
}

func (m *Manager) matchesTarget(payload map[string]any) bool {
	if payload == nil {
		return true
	}
	agentID := cleanText(firstValue(payload, "agent_id", "agent_guid"))
	if agentID != "" && !strings.EqualFold(agentID, m.authClient.AgentID()) {
		return false
	}
	hostname := strings.ToLower(cleanText(firstValue(payload, "hostname", "target_hostname")))
	return hostname == "" || hostname == strings.ToLower(m.hostname)
}

func (m *Manager) recordError(message string) {
	cleaned := cleanText(message)
	m.mu.Lock()
	m.lastError = cleaned
	m.mu.Unlock()
	if cleaned != "" {
		m.logf("%s", cleaned)
	}
}

func (m *Manager) clearError() {
	m.mu.Lock()
	m.lastError = ""
	m.mu.Unlock()
}

func (m *Manager) logf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02T15:04:05")
	if m == nil || m.logPath == "" {
		return
	}
	logutil.Append(m.logPath, logutil.RetentionDaysFromConfig(m.configPath), "[%s] [wg-client] %s", ts, message)
}

func sessionEquivalent(left *SessionConfig, right *SessionConfig) bool {
	if left == nil || right == nil {
		return false
	}
	return cleanText(left.VirtualIP) == cleanText(right.VirtualIP) &&
		cleanText(left.AllowedIPs) == cleanText(right.AllowedIPs) &&
		cleanText(left.Endpoint) == cleanText(right.Endpoint) &&
		cleanText(left.ServerPublicKey) == cleanText(right.ServerPublicKey) &&
		cleanText(left.AllowedPorts) == cleanText(right.AllowedPorts) &&
		cleanText(left.PresharedKey) == cleanText(right.PresharedKey) &&
		cleanText(left.ClientPrivateKey) == cleanText(right.ClientPrivateKey)
}

func cloneSession(session *SessionConfig) *SessionConfig {
	if session == nil {
		return nil
	}
	copied := *session
	copied.Token = cloneMap(session.Token)
	return &copied
}

func cloneMap(input map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range input {
		out[key] = value
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		cleaned := strings.TrimSpace(value)
		if cleaned == "" || seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		out = append(out, cleaned)
	}
	return out
}

func primeEnginePath(session *SessionConfig) error {
	host := strings.Split(strings.TrimSpace(session.AllowedIPs), "/")[0]
	if host == "" {
		return nil
	}
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, "9"), 100*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte("borealis-wg-probe"))
	return err
}

func parseAllowedIPs(value any, fallback string) string {
	switch typed := value.(type) {
	case []any:
		if len(typed) > 0 {
			return cleanText(typed[0])
		}
	case []string:
		if len(typed) > 0 {
			return strings.TrimSpace(typed[0])
		}
	default:
		if text := cleanText(value); text != "" {
			return text
		}
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return ""
	}
	if strings.Contains(fallback, "/") {
		return fallback
	}
	return fallback + "/32"
}

func validSingleHostRoute(value string) bool {
	text := strings.TrimSpace(value)
	if text == "" || strings.Contains(text, ",") {
		return false
	}
	prefix, err := netip.ParsePrefix(text)
	return err == nil && prefix.Addr().Is4() && prefix.Bits() == 32
}

func parseAllowedPorts(value any) []int {
	items := []any{}
	switch typed := value.(type) {
	case []any:
		items = typed
	case []int:
		for _, item := range typed {
			items = append(items, item)
		}
	case []string:
		for _, item := range typed {
			items = append(items, item)
		}
	default:
		for _, part := range strings.Split(cleanText(value), ",") {
			if strings.TrimSpace(part) != "" {
				items = append(items, strings.TrimSpace(part))
			}
		}
	}
	seen := map[int]bool{}
	out := []int{}
	for _, item := range items {
		port := asInt(item, 0)
		if port < 1 || port > 65535 || seen[port] {
			continue
		}
		seen[port] = true
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}

func formatPorts(ports []int) string {
	if len(ports) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		parts = append(parts, strconv.Itoa(port))
	}
	return strings.Join(parts, ",")
}

func powerShellPortArray(ports []int) string {
	return "@(" + formatPorts(ports) + ")"
}

func firstValue(payload map[string]any, keys ...string) any {
	if payload == nil {
		return nil
	}
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			return value
		}
	}
	return nil
}

func mapStringAny(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		out := map[string]any{}
		for key, value := range typed {
			out[key] = value
		}
		return out
	default:
		return nil
	}
}

func cleanText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func fallbackText(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func asInt(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return int(i)
		}
	}
	text := cleanText(value)
	if text == "" {
		return fallback
	}
	i, err := strconv.Atoi(text)
	if err != nil {
		return fallback
	}
	return i
}

func asFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		f, _ := typed.Float64()
		return f
	}
	f, _ := strconv.ParseFloat(cleanText(value), 64)
	return f
}

func asBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		return normalized == "1" || normalized == "true" || normalized == "yes" || normalized == "on"
	default:
		return false
	}
}

func envDuration(name string, fallback time.Duration, min time.Duration, max time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	duration := time.Duration(value) * time.Second
	if duration < min {
		return min
	}
	if max > 0 && duration > max {
		return max
	}
	return duration
}

func keepaliveSeconds() int {
	value := asInt(os.Getenv("BOREALIS_WIREGUARD_KEEPALIVE_SECONDS"), defaultKeepalive)
	if value < 10 {
		return 10
	}
	if value > 600 {
		return 600
	}
	return value
}

func interfaceMTU() int {
	value := asInt(os.Getenv("BOREALIS_WIREGUARD_MTU"), defaultInterfaceMTU)
	if value < 1280 {
		return 1280
	}
	if value > 65535 {
		return 65535
	}
	return value
}

func resolveInterfaceName() string {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("BOREALIS_WIREGUARD_INTERFACE")))
	if raw == "" {
		raw = defaultInterface
	}
	cleaned := regexp.MustCompile(`[^a-z0-9_.-]`).ReplaceAllString(raw, "")
	if cleaned == "" {
		cleaned = defaultInterface
	}
	if len(cleaned) > 15 {
		cleaned = cleaned[:15]
	}
	return cleaned
}

func resolveExecutable(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return ""
}

func resolveWireGuardExe() string {
	candidates := []string{}
	if pf := strings.TrimSpace(os.Getenv("ProgramFiles")); pf != "" {
		candidates = append(candidates, filepath.Join(pf, "WireGuard", "wireguard.exe"))
	}
	if pfx86 := strings.TrimSpace(os.Getenv("ProgramFiles(x86)")); pfx86 != "" {
		candidates = append(candidates, filepath.Join(pfx86, "WireGuard", "wireguard.exe"))
	}
	if path, err := exec.LookPath("wireguard.exe"); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return "wireguard.exe"
}

func generateWireGuardKeys() (string, string) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", ""
	}
	return base64.StdEncoding.EncodeToString(private.Bytes()), base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())
}

func samePath(left string, right string) bool {
	left = strings.ToLower(strings.ReplaceAll(filepath.Clean(strings.TrimSpace(left)), "/", `\`))
	right = strings.ToLower(strings.ReplaceAll(filepath.Clean(strings.TrimSpace(right)), "/", `\`))
	return left == right
}

func commandDetail(result commandResult) string {
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		detail = fmt.Sprintf("exit code %d", result.ExitCode)
	}
	return detail
}

func powerShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
	if timeout <= 0 {
		timeout = commandTimeout
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, name, args...)
	stdout, err := cmd.Output()
	stderr := ""
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
			exitCode = exitErr.ExitCode()
		} else {
			return commandResult{Stdout: string(stdout), Stderr: err.Error(), ExitCode: -1}, err
		}
	}
	if cmdCtx.Err() == context.DeadlineExceeded {
		return commandResult{Stdout: string(stdout), Stderr: "command timed out", ExitCode: -1}, cmdCtx.Err()
	}
	return commandResult{
		Stdout:   strings.TrimSpace(string(stdout)),
		Stderr:   strings.TrimSpace(stderr),
		ExitCode: exitCode,
	}, nil
}
