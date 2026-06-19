package remoteshell

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bunny-lab-io/borealis/go-agent/internal/logutil"
)

const (
	defaultShellHost = "0.0.0.0"
	defaultShellPort = 47002
	retryDelay       = 5 * time.Second
)

type RoleHealth struct {
	Status     string
	StatusCode string
	Detail     string
	Details    map[string]any
}

type Manager struct {
	hostname    string
	serviceMode string
	configPath  string
	baseDir     string
	logPath     string
	platform    string
	host        string
	port        int
	shellKind   string
	shellBin    string

	mu            sync.Mutex
	sessionMu     sync.Mutex
	started       bool
	listening     bool
	lastError     string
	lastCheckedAt int64
	listener      net.Listener
	cancel        context.CancelFunc
	session       *shellSession
}

type shellSession struct {
	conn      net.Conn
	address   string
	shellKind string
	shellBin  string
	logf      func(string, ...any)
	onClosed  func(*shellSession)
	onReady   func(*shellSession)

	writeMu sync.Mutex
	metaMu  sync.Mutex
	statsMu sync.Mutex
	procMu  sync.Mutex
	once    sync.Once
	ready   sync.Once

	proc   *exec.Cmd
	stdin  io.WriteCloser
	closed chan struct{}

	engineSessionID    string
	lastInput          inputMeta
	inputMessages      int
	inputBytes         int
	outputMessages     int
	outputBytes        int
	lastKeepaliveLogAt time.Time
}

type inputMeta struct {
	MessageID         string
	SentAtMS          *int64
	AgentReceivedAtMS int64
}

func New(hostname string, serviceMode string, configPath string) *Manager {
	baseDir := filepath.Dir(configPath)
	manager := &Manager{
		hostname:    strings.TrimSpace(hostname),
		serviceMode: strings.TrimSpace(serviceMode),
		configPath:  strings.TrimSpace(configPath),
		baseDir:     baseDir,
		logPath:     filepath.Join(baseDir, "Logs", "Agent", "remote_shell.log"),
		platform:    runtime.GOOS,
		host:        defaultShellHost,
		port:        resolveShellPort(),
		shellKind:   detectShellKind(),
	}
	switch manager.shellKind {
	case "powershell":
		manager.shellBin = resolvePowerShellBinary()
	case "bash":
		manager.shellBin = resolveBashBinary()
	}
	manager.lastCheckedAt = time.Now().Unix()
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
	if m.shellKind == "" || m.shellBin == "" {
		m.lastError = "No compatible remote shell binary is available."
		m.lastCheckedAt = time.Now().Unix()
		m.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.mu.Unlock()

	m.logf("Remote shell supervisor starting platform=%s host=%s port=%d shell=%s binary=%s", m.platform, m.host, m.port, m.shellKind, m.shellBin)
	go m.serve(runCtx)
}

func (m *Manager) Stop(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	listener := m.listener
	m.listener = nil
	m.listening = false
	m.started = false
	m.lastCheckedAt = time.Now().Unix()
	m.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	m.closeCurrentSession()
	select {
	case <-ctx.Done():
	default:
	}
}

func (m *Manager) Restart(ctx context.Context, reason string) {
	if m == nil {
		return
	}
	cleanReason := strings.TrimSpace(reason)
	if cleanReason == "" {
		cleanReason = "role_supervisor_recovery"
	}
	m.logf("Remote shell restart requested reason=%s", cleanReason)
	m.Stop(context.Background())
	m.Start(ctx)
}

func (m *Manager) Health() RoleHealth {
	if m == nil {
		return RoleHealth{
			Status:     "unsupported",
			StatusCode: "unsupported",
			Detail:     "Remote Shell role is unavailable.",
			Details: map[string]any{
				"running_status": "Unavailable",
				"runtime":        "go",
			},
		}
	}
	m.mu.Lock()
	started := m.started
	listening := m.listening
	lastError := m.lastError
	lastCheckedAt := m.lastCheckedAt
	host := m.host
	port := m.port
	shellKind := m.shellKind
	shellBin := m.shellBin
	m.mu.Unlock()
	if lastCheckedAt == 0 {
		lastCheckedAt = time.Now().Unix()
	}
	activeSession := false
	m.sessionMu.Lock()
	if m.session != nil {
		activeSession = !m.session.isClosed()
	}
	m.sessionMu.Unlock()
	details := map[string]any{
		"running_status":  "Stopped",
		"listener_ip":     host,
		"listener_port":   strconv.Itoa(port),
		"shell_type":      shellKind,
		"shell_binary":    shellBin,
		"active_session":  strconv.FormatBool(activeSession),
		"last_error":      lastError,
		"last_checked_at": strconv.FormatInt(lastCheckedAt, 10),
		"runtime":         "go",
	}
	if shellKind == "" || shellBin == "" {
		details["running_status"] = "Unsupported"
		return RoleHealth{
			Status:     "unsupported",
			StatusCode: "unsupported",
			Detail:     "No compatible remote shell binary is available.",
			Details:    details,
		}
	}
	if listening {
		details["running_status"] = "Running"
		return RoleHealth{
			Status:     "healthy",
			StatusCode: "healthy",
			Detail:     fmt.Sprintf("Listening on %s:%d.", host, port),
			Details:    details,
		}
	}
	if !started {
		details["running_status"] = "Pending"
		return RoleHealth{
			Status:     "pending",
			StatusCode: "pending",
			Detail:     "Waiting for Remote Shell listener startup.",
			Details:    details,
		}
	}
	details["running_status"] = "Recovering"
	detail := "Waiting for Remote Shell listener startup."
	if lastError != "" {
		detail = "Retrying after listener error: " + lastError
	}
	return RoleHealth{
		Status:     "recovering",
		StatusCode: "recovering",
		Detail:     detail,
		Details:    details,
	}
}

func (m *Manager) serve(ctx context.Context) {
	address := net.JoinHostPort(m.host, strconv.Itoa(m.port))
	for ctx.Err() == nil {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			m.setState(false, err.Error(), nil)
			m.logf("Remote shell listener failed host=%s port=%d error=%v; retrying in %s", m.host, m.port, err, retryDelay)
			if !sleepContext(ctx, retryDelay) {
				return
			}
			continue
		}
		m.setState(true, "", listener)
		m.logf("Remote shell listening host=%s port=%d shell=%s binary=%s", m.host, m.port, m.shellKind, m.shellBin)
		for ctx.Err() == nil {
			conn, err := listener.Accept()
			if err != nil {
				if ctx.Err() != nil {
					_ = listener.Close()
					return
				}
				m.setState(false, err.Error(), nil)
				m.logf("Remote shell accept error: %v", err)
				_ = listener.Close()
				break
			}
			m.handleConnection(conn)
		}
	}
}

func (m *Manager) handleConnection(conn net.Conn) {
	remoteHost := remoteHost(conn.RemoteAddr())
	if !isAllowedShellRemote(remoteHost) {
		m.logf("Rejected remote shell connection from %s", remoteHost)
		_ = conn.Close()
		return
	}
	configureTCPSocket(conn)
	session := newShellSession(conn, conn.RemoteAddr().String(), m.shellKind, m.shellBin, m.logf, m.onSessionClosed, m.activateSession)
	m.logf("Accepted remote shell connection from %s", conn.RemoteAddr().String())
	go session.start()
}

func (m *Manager) activateSession(session *shellSession) {
	if session == nil || session.isClosed() {
		return
	}
	m.sessionMu.Lock()
	prior := m.session
	if prior == session {
		m.sessionMu.Unlock()
		return
	}
	m.session = session
	m.sessionMu.Unlock()
	if prior != nil && !prior.isClosed() {
		m.logf("Closing superseded remote shell session prior_session_id=%s prior_remote=%s", prior.sessionID(), prior.address)
		prior.close()
	}
}

func (m *Manager) onSessionClosed(session *shellSession) {
	m.sessionMu.Lock()
	if m.session == session {
		m.session = nil
	}
	m.sessionMu.Unlock()
}

func (m *Manager) closeCurrentSession() {
	m.sessionMu.Lock()
	session := m.session
	m.session = nil
	m.sessionMu.Unlock()
	if session != nil {
		session.close()
	}
}

func (m *Manager) setState(listening bool, lastError string, listener net.Listener) {
	m.mu.Lock()
	m.listening = listening
	m.lastError = strings.TrimSpace(lastError)
	m.lastCheckedAt = time.Now().Unix()
	if listener != nil || !listening {
		m.listener = listener
	}
	m.mu.Unlock()
}

func (m *Manager) logf(format string, args ...any) {
	if m == nil {
		return
	}
	message := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02T15:04:05")
	logutil.Append(m.logPath, logutil.RetentionDaysFromConfig(m.configPath), "[%s] [vpn-shell] %s", timestamp, message)
}

func newShellSession(conn net.Conn, address string, shellKind string, shellBin string, logf func(string, ...any), onClosed func(*shellSession), onReady func(*shellSession)) *shellSession {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &shellSession{
		conn:      conn,
		address:   strings.TrimSpace(address),
		shellKind: strings.TrimSpace(shellKind),
		shellBin:  strings.TrimSpace(shellBin),
		logf:      logf,
		onClosed:  onClosed,
		onReady:   onReady,
		closed:    make(chan struct{}),
	}
}

func (s *shellSession) start() {
	s.logf("Shell session starting remote=%s type=%s", s.address, s.shellKind)
	s.writerLoop()
}

func (s *shellSession) ensureProcessStarted() error {
	s.procMu.Lock()
	if s.proc != nil && s.stdin != nil {
		s.procMu.Unlock()
		return nil
	}
	stdout, stderr, err := s.startProcessLocked()
	s.procMu.Unlock()
	if err != nil {
		return err
	}
	go s.readOutput(stdout, "stdout")
	go s.readOutput(stderr, "stderr")
	go s.waitProcess()
	return nil
}

func (s *shellSession) startProcessLocked() (io.Reader, io.Reader, error) {
	if s.shellBin == "" {
		return nil, nil, fmt.Errorf("missing shell binary")
	}
	args := shellArgs(s.shellKind, s.shellBin)
	cmd := exec.Command(s.shellBin, args...)
	if s.shellKind == "bash" {
		cmd.Env = append(os.Environ(),
			"TERM=dumb",
			"NO_COLOR=1",
			"CLICOLOR=0",
			"FORCE_COLOR=0",
			"SYSTEMD_COLORS=0",
		)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	s.proc = cmd
	s.stdin = stdin
	s.logf("Shell subprocess started pid=%d shell=%s type=%s", cmd.Process.Pid, s.shellBin, s.shellKind)
	return stdout, stderr, nil
}

func (s *shellSession) waitProcess() {
	s.procMu.Lock()
	proc := s.proc
	s.procMu.Unlock()
	if proc == nil {
		return
	}
	err := proc.Wait()
	if err != nil {
		s.logf("Shell subprocess exited error=%v", err)
	} else {
		s.logf("Shell subprocess exited cleanly")
	}
	s.close()
}

func (s *shellSession) readOutput(reader io.Reader, label string) {
	if reader == nil {
		return
	}
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buffer[:n])
			s.sendStdout(chunk)
			s.logf("Shell %s forwarded bytes=%d session_id=%s", label, n, s.sessionID())
		}
		if err != nil {
			if err != io.EOF && !s.isClosed() {
				s.logf("Shell %s read error: %v session_id=%s", label, err, s.sessionID())
			}
			return
		}
	}
}

func (s *shellSession) writerLoop() {
	reader := bufio.NewReader(s.conn)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			s.handleLine(line)
		}
		if err != nil {
			if err != io.EOF && !s.isClosed() {
				s.logf("Shell stdin recv error: %v session_id=%s", err, s.sessionID())
			}
			s.close()
			return
		}
	}
}

func (s *shellSession) handleLine(line []byte) {
	text := strings.TrimSpace(string(line))
	if text == "" {
		return
	}
	var msg map[string]any
	if err := json.Unmarshal([]byte(text), &msg); err != nil {
		return
	}
	s.captureSessionID(msg)
	s.markReady()
	if s.handleControlMessage(msg) {
		return
	}
	msgType := strings.ToLower(strings.TrimSpace(fmt.Sprint(msg["type"])))
	switch msgType {
	case "stdin":
		s.handleStdin(msg)
	case "close":
		s.logf("Shell close requested by engine session_id=%s", s.sessionID())
		s.close()
	}
}

func (s *shellSession) handleControlMessage(msg map[string]any) bool {
	msgType := strings.ToLower(strings.TrimSpace(fmt.Sprint(msg["type"])))
	if msgType != "ping" {
		return false
	}
	pingID := strings.TrimSpace(fmt.Sprint(msg["ping_id"]))
	reason := strings.ToLower(strings.TrimSpace(fmt.Sprint(msg["reason"])))
	sentAtMS, hasSentAt := coerceInt64(msg["sent_at_ms"])
	receivedAt := nowMS()
	payload := map[string]any{
		"type":                 "pong",
		"agent_received_at_ms": receivedAt,
		"agent_pong_at_ms":     nowMS(),
	}
	if pingID != "" {
		payload["ping_id"] = pingID
	}
	if hasSentAt {
		payload["sent_at_ms"] = sentAtMS
	}
	if sessionID := s.sessionID(); sessionID != "" {
		payload["session_id"] = sessionID
	}
	_ = s.sendJSON(payload)
	if reason == "idle_keepalive" {
		s.metaMu.Lock()
		shouldLog := time.Since(s.lastKeepaliveLogAt) >= 30*time.Second
		if shouldLog {
			s.lastKeepaliveLogAt = time.Now()
		}
		s.metaMu.Unlock()
		if shouldLog {
			s.logf("Shell keepalive pong sent ping_id=%s recv_at_ms=%d session_id=%s", valueOrDash(pingID), receivedAt, s.sessionID())
		}
	} else {
		s.logf("Shell ready pong sent ping_id=%s recv_at_ms=%d session_id=%s", valueOrDash(pingID), receivedAt, s.sessionID())
	}
	return true
}

func (s *shellSession) handleStdin(msg map[string]any) {
	payload := strings.TrimSpace(fmt.Sprint(msg["data"]))
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		s.logf("Shell stdin decode failed session_id=%s", s.sessionID())
		return
	}
	if err := s.ensureProcessStarted(); err != nil {
		s.logf("Shell subprocess start failed error=%v session_id=%s", err, s.sessionID())
		s.sendStdout([]byte(fmt.Sprintf("[borealis] Failed to start remote shell: %v\n", err)))
		s.close()
		return
	}
	decoded = normalizeShellInput(s.shellKind, decoded)
	messageID := strings.TrimSpace(fmt.Sprint(msg["message_id"]))
	sentAt, hasSentAt := coerceInt64(msg["sent_at_ms"])
	receivedAt := nowMS()
	meta := inputMeta{
		MessageID:         messageID,
		AgentReceivedAtMS: receivedAt,
	}
	if hasSentAt {
		meta.SentAtMS = &sentAt
	}
	s.metaMu.Lock()
	s.lastInput = meta
	s.metaMu.Unlock()
	s.procMu.Lock()
	stdin := s.stdin
	s.procMu.Unlock()
	if stdin == nil {
		s.logf("Shell stdin unavailable session_id=%s", s.sessionID())
		return
	}
	if _, err := stdin.Write(decoded); err != nil {
		s.logf("Shell stdin write failed session_id=%s", s.sessionID())
		return
	}
	s.statsMu.Lock()
	s.inputMessages++
	s.inputBytes += len(decoded)
	s.statsMu.Unlock()
	s.logf("Shell stdin received bytes=%d message_id=%s recv_at_ms=%d session_id=%s", len(decoded), valueOrDash(messageID), receivedAt, s.sessionID())
}

func (s *shellSession) sendStdout(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	s.statsMu.Lock()
	s.outputMessages++
	s.outputBytes += len(chunk)
	s.statsMu.Unlock()
	s.metaMu.Lock()
	meta := s.lastInput
	sessionID := s.engineSessionID
	s.metaMu.Unlock()
	payload := map[string]any{
		"type":               "stdout",
		"data":               base64.StdEncoding.EncodeToString(chunk),
		"agent_stdout_at_ms": nowMS(),
	}
	if sessionID != "" {
		payload["session_id"] = sessionID
	}
	if meta.MessageID != "" {
		payload["message_id"] = meta.MessageID
	}
	if meta.SentAtMS != nil {
		payload["sent_at_ms"] = *meta.SentAtMS
	}
	if meta.AgentReceivedAtMS > 0 {
		payload["agent_received_at_ms"] = meta.AgentReceivedAtMS
	}
	if err := s.sendJSON(payload); err != nil {
		s.logf("Shell stdout send failed: %v session_id=%s", err, s.sessionID())
	}
}

func (s *shellSession) sendJSON(payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.isClosed() {
		return io.ErrClosedPipe
	}
	_, err = s.conn.Write(append(data, '\n'))
	return err
}

func (s *shellSession) captureSessionID(msg map[string]any) string {
	sessionID := strings.TrimSpace(fmt.Sprint(msg["session_id"]))
	if sessionID == "" {
		return s.sessionID()
	}
	s.metaMu.Lock()
	if s.engineSessionID == "" {
		s.engineSessionID = sessionID
	}
	out := s.engineSessionID
	s.metaMu.Unlock()
	return out
}

func (s *shellSession) markReady() {
	s.ready.Do(func() {
		if s.onReady != nil {
			s.onReady(s)
		}
	})
}

func (s *shellSession) sessionID() string {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	return s.engineSessionID
}

func (s *shellSession) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func (s *shellSession) close() {
	s.once.Do(func() {
		close(s.closed)
		_ = s.conn.Close()
		s.procMu.Lock()
		stdin := s.stdin
		proc := s.proc
		s.stdin = nil
		s.proc = nil
		s.procMu.Unlock()
		if stdin != nil {
			_ = stdin.Close()
		}
		if proc != nil && proc.Process != nil {
			_ = proc.Process.Kill()
		}
		s.statsMu.Lock()
		inputMessages := s.inputMessages
		inputBytes := s.inputBytes
		outputMessages := s.outputMessages
		outputBytes := s.outputBytes
		s.statsMu.Unlock()
		s.logf("Shell session closed inputs=%d input_bytes=%d outputs=%d output_bytes=%d session_id=%s", inputMessages, inputBytes, outputMessages, outputBytes, s.sessionID())
		if inputMessages > 0 && outputMessages == 0 {
			s.logf("Shell session closed without stdout after input inputs=%d input_bytes=%d session_id=%s", inputMessages, inputBytes, s.sessionID())
		}
		if s.onClosed != nil {
			s.onClosed(s)
		}
	})
}

func shellArgs(shellKind string, shellBin string) []string {
	switch shellKind {
	case "powershell":
		return []string{"-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-NoExit"}
	case "bash":
		base := strings.ToLower(filepath.Base(shellBin))
		if base == "bash" {
			return []string{"--noprofile", "--norc", "-s"}
		}
		return []string{"-s"}
	default:
		return nil
	}
}

func detectShellKind() string {
	switch runtime.GOOS {
	case "windows":
		return "powershell"
	case "linux":
		return "bash"
	default:
		return ""
	}
}

func resolveShellPort() int {
	raw := strings.TrimSpace(os.Getenv("BOREALIS_WIREGUARD_SHELL_PORT"))
	if raw == "" {
		return defaultShellPort
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 65535 {
		return defaultShellPort
	}
	return value
}

func resolvePowerShellBinary() string {
	override := strings.TrimSpace(os.Getenv("BOREALIS_REMOTE_POWERSHELL_BIN"))
	if override != "" {
		return override
	}
	systemRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	candidate := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if fileExists(candidate) {
		return candidate
	}
	return "powershell.exe"
}

func resolveBashBinary() string {
	candidates := []string{}
	if override := strings.TrimSpace(os.Getenv("BOREALIS_REMOTE_BASH_BIN")); override != "" {
		candidates = append(candidates, override)
	}
	candidates = append(candidates, "/bin/bash", "/usr/bin/bash", "/bin/sh", "/usr/bin/sh")
	for _, candidate := range candidates {
		if executableFile(candidate) {
			return candidate
		}
	}
	return ""
}

func executableFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0111 != 0
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isAllowedShellRemote(host string) bool {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 10 && ip4[1] == 255
}

func remoteHost(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		return host
	}
	return addr.String()
}

func configureTCPSocket(conn net.Conn) {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tcp.SetNoDelay(true)
	_ = tcp.SetKeepAlive(true)
	_ = tcp.SetKeepAlivePeriod(30 * time.Second)
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func coerceInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case json.Number:
		out, err := typed.Int64()
		return out, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func nowMS() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

func normalizeLineEndings(input []byte) []byte {
	text := strings.ReplaceAll(string(input), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return []byte(text)
}

func normalizeShellInput(shellKind string, input []byte) []byte {
	text := string(normalizeLineEndings(input))
	if shellKind != "powershell" {
		return []byte(text)
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return []byte(strings.ReplaceAll(text, "\n", "\r\n"))
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
