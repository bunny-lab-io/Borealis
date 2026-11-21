package remoteshell

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewUsesAgentRemoteShellLogCategoryPath(t *testing.T) {
	dir := t.TempDir()
	manager := New("LAB-01", "system", filepath.Join(dir, "agent.json"))

	if got := manager.logPath; got != filepath.Join(dir, "Logs", "Agent", "remote_shell.log") {
		t.Fatalf("unexpected log path: %s", got)
	}
}

func TestAllowedShellRemoteRequiresWireGuardSubnet(t *testing.T) {
	if !isAllowedShellRemote("10.255.0.1") {
		t.Fatal("expected WireGuard engine address to be allowed")
	}
	if isAllowedShellRemote("10.0.0.1") {
		t.Fatal("expected non-WireGuard private address to be rejected")
	}
	if isAllowedShellRemote("127.0.0.1") {
		t.Fatal("expected loopback address to be rejected")
	}
}

func TestResolveShellPortDefaultsAndValidates(t *testing.T) {
	t.Setenv("BOREALIS_WIREGUARD_SHELL_PORT", "")
	if got := resolveShellPort(); got != defaultShellPort {
		t.Fatalf("unexpected default port: %d", got)
	}
	t.Setenv("BOREALIS_WIREGUARD_SHELL_PORT", "48000")
	if got := resolveShellPort(); got != 48000 {
		t.Fatalf("unexpected configured port: %d", got)
	}
	t.Setenv("BOREALIS_WIREGUARD_SHELL_PORT", "99999")
	if got := resolveShellPort(); got != defaultShellPort {
		t.Fatalf("unexpected invalid fallback port: %d", got)
	}
}

func TestPowerShellArgsUseDurableInteractiveProcess(t *testing.T) {
	args := strings.Join(shellArgs("powershell", "powershell.exe"), " ")
	if strings.Contains(args, "-Command") || strings.Contains(args, " - ") {
		t.Fatalf("PowerShell shell args should not use stdin script mode: %s", args)
	}
	for _, expected := range []string{"-NoLogo", "-NoProfile", "-ExecutionPolicy Bypass", "-NoExit"} {
		if !strings.Contains(args, expected) {
			t.Fatalf("PowerShell args missing %s: %s", expected, args)
		}
	}
}

func TestNormalizeShellInputUsesPlatformLineEndings(t *testing.T) {
	if got := string(normalizeShellInput("bash", []byte("whoami\r\nhostname\r"))); got != "whoami\nhostname\n" {
		t.Fatalf("unexpected bash input normalization: %q", got)
	}
	if got := string(normalizeShellInput("powershell", []byte("whoami\nhostname"))); got != "whoami\r\nhostname\r\n" {
		t.Fatalf("unexpected PowerShell input normalization: %q", got)
	}
}

func TestShellSessionPingPongAndBashStdout(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}
	agentConn, engineConn := net.Pipe()
	defer engineConn.Close()
	_ = engineConn.SetDeadline(time.Now().Add(5 * time.Second))

	var logs []string
	var logsMu sync.Mutex
	session := newShellSession(agentConn, "10.255.0.1:12345", "bash", "/bin/sh", func(format string, args ...any) {
		logsMu.Lock()
		defer logsMu.Unlock()
		logs = append(logs, strings.TrimSpace(format))
	}, nil, nil)
	go session.start()
	defer session.close()

	reader := bufio.NewReader(engineConn)
	writeJSON(t, engineConn, map[string]any{
		"type":       "ping",
		"ping_id":    "ready-1",
		"session_id": "session-1",
		"reason":     "ready",
	})
	pong := readJSON(t, reader)
	if pong["type"] != "pong" || pong["ping_id"] != "ready-1" || pong["session_id"] != "session-1" {
		t.Fatalf("unexpected pong payload: %#v", pong)
	}

	writeJSON(t, engineConn, map[string]any{
		"type":       "stdin",
		"session_id": "session-1",
		"message_id": "msg-1",
		"data":       base64.StdEncoding.EncodeToString([]byte("printf 'shell-ok\\n'\n")),
	})

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		msg := readJSON(t, reader)
		if msg["type"] != "stdout" {
			continue
		}
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(msg["data"].(string)))
		if strings.Contains(string(raw), "shell-ok") {
			if msg["message_id"] != "msg-1" {
				t.Fatalf("stdout missing message correlation: %#v", msg)
			}
			return
		}
	}
	logsMu.Lock()
	defer logsMu.Unlock()
	t.Fatalf("did not receive expected stdout; logs=%#v", logs)
}

func TestShellSessionReadyPingDoesNotStartProcess(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}
	agentConn, engineConn := net.Pipe()
	defer engineConn.Close()
	_ = engineConn.SetDeadline(time.Now().Add(2 * time.Second))

	ready := make(chan struct{}, 1)
	session := newShellSession(agentConn, "10.255.0.1:12345", "bash", "/bin/sh", nil, nil, func(*shellSession) {
		ready <- struct{}{}
	})
	go session.start()
	defer session.close()

	reader := bufio.NewReader(engineConn)
	writeJSON(t, engineConn, map[string]any{
		"type":       "ping",
		"ping_id":    "ready-1",
		"session_id": "session-1",
		"reason":     "ready",
	})
	pong := readJSON(t, reader)
	if pong["type"] != "pong" || pong["ping_id"] != "ready-1" {
		t.Fatalf("unexpected pong payload: %#v", pong)
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("session was not activated by ready ping")
	}
	session.procMu.Lock()
	proc := session.proc
	session.procMu.Unlock()
	if proc != nil {
		t.Fatalf("ready ping started shell process pid=%d", proc.Process.Pid)
	}
}

func TestTCPProbeDoesNotReplaceActiveSession(t *testing.T) {
	activeAgent, activeEngine := net.Pipe()
	defer activeEngine.Close()
	active := newShellSession(activeAgent, "10.255.0.1:11111", "bash", "/bin/sh", nil, nil, nil)
	defer active.close()

	manager := &Manager{}
	manager.session = active

	probeAgent, probeEngine := net.Pipe()
	probe := newShellSession(probeAgent, "10.255.0.1:22222", "bash", "/bin/sh", nil, manager.onSessionClosed, manager.activateSession)
	go probe.start()
	_ = probeEngine.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !probe.isClosed() {
		time.Sleep(10 * time.Millisecond)
	}
	if !probe.isClosed() {
		t.Fatal("probe session did not close")
	}
	manager.sessionMu.Lock()
	current := manager.session
	manager.sessionMu.Unlock()
	if current != active {
		t.Fatal("probe connection replaced active shell session")
	}
	if active.isClosed() {
		t.Fatal("probe connection closed active shell session")
	}
}

func TestReadySessionReplacesActiveSession(t *testing.T) {
	activeAgent, activeEngine := net.Pipe()
	defer activeEngine.Close()
	active := newShellSession(activeAgent, "10.255.0.1:11111", "bash", "/bin/sh", nil, nil, nil)
	defer active.close()

	manager := &Manager{}
	manager.session = active

	agentConn, engineConn := net.Pipe()
	defer engineConn.Close()
	_ = engineConn.SetDeadline(time.Now().Add(2 * time.Second))
	session := newShellSession(agentConn, "10.255.0.1:22222", "bash", "/bin/sh", nil, manager.onSessionClosed, manager.activateSession)
	go session.start()
	defer session.close()

	reader := bufio.NewReader(engineConn)
	writeJSON(t, engineConn, map[string]any{
		"type":       "ping",
		"ping_id":    "ready-1",
		"session_id": "session-1",
		"reason":     "ready",
	})
	_ = readJSON(t, reader)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.sessionMu.Lock()
		current := manager.session
		manager.sessionMu.Unlock()
		if current == session {
			if !active.isClosed() {
				t.Fatal("ready session did not close prior active session")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ready session did not become active")
}

func writeJSON(t *testing.T, conn net.Conn, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(line, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestTrackedShellPassiveReadinessAndBindBeforeStdin(t *testing.T) {
	agentConn, engineConn := net.Pipe()
	defer engineConn.Close()
	_ = engineConn.SetDeadline(time.Now().Add(5 * time.Second))
	var ready atomic.Int32
	complete := make(chan error, 1)
	session := newShellSession(agentConn, "10.255.0.1:12345", "bash", "/missing-shell", nil, nil, func(*shellSession) { ready.Add(1) })
	session.requireOperation = true
	session.bindOperation = func(_ context.Context, msg map[string]any) (func(error), error) {
		if msg["type"] != "worker_operation_bind" || msg["session_id"] != "session-1" {
			return nil, errors.New("bad bind")
		}
		return func(err error) { complete <- err }, nil
	}
	go session.start()
	defer session.close()
	reader := bufio.NewReader(engineConn)
	writeJSON(t, engineConn, map[string]any{"type": "ping", "session_id": "session-1", "ping_id": "ready"})
	if pong := readJSON(t, reader); pong["type"] != "pong" {
		t.Fatal(pong)
	}
	if ready.Load() != 0 {
		t.Fatal("passive ping replaced active session")
	}
	writeJSON(t, engineConn, map[string]any{"type": "worker_operation_bind", "session_id": "session-1", "job_id": 17, "context": map[string]any{"worker_operation": map[string]any{"version": 1, "token": strings.Repeat("a", 32)}}})
	bound := readJSON(t, reader)
	if bound["type"] != "worker_operation_bound" || bound["status"] != "accepted" || ready.Load() != 1 {
		t.Fatal(bound)
	}
	session.procMu.Lock()
	proc := session.proc
	session.procMu.Unlock()
	if proc != nil {
		t.Fatal("bind started process before stdin")
	}
	select {
	case <-complete:
		t.Fatal("accepted became terminal")
	default:
	}
	_ = engineConn.Close()
	select {
	case err := <-complete:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("closed-before-start terminal missing")
	}
}

func TestTrackedShellRequiresBindingAndPreservesBinderGeneration(t *testing.T) {
	agentConn, engineConn := net.Pipe()
	defer agentConn.Close()
	defer engineConn.Close()
	session := newShellSession(agentConn, "fixture", "bash", "/bin/sh", nil, nil, nil)
	session.requireOperation = true
	if err := session.ensureProcessStarted(); err == nil {
		t.Fatal("unbound stdin started process")
	}
	session.bound = true
	session.close()
	if err := session.ensureProcessStarted(); err == nil {
		t.Fatal("closed session restarted process")
	}
	manager := New("host", "system", filepath.Join(t.TempDir(), "agent.json"))
	old := manager.SetOperationBinder(func(context.Context, map[string]any) (func(error), error) { return nil, errors.New("old") })
	current := manager.SetOperationBinder(func(context.Context, map[string]any) (func(error), error) { return nil, errors.New("current") })
	old()
	if _, err := manager.operationBinder(context.Background(), nil); err.Error() != "current" {
		t.Fatal("old cleanup erased current binder")
	}
	current()
	if manager.operationBinder != nil || !manager.requireOperation {
		t.Fatal("disconnected shell lost active fence")
	}
}

func TestTrackedShellEOFWaitsForSubprocessJoin(t *testing.T) {
	agentConn, engineConn := net.Pipe()
	defer engineConn.Close()
	session := newShellSession(agentConn, "fixture", "bash", "", nil, nil, nil)
	complete := make(chan error, 2)
	session.bound = true
	session.complete = func(err error) { complete <- err }
	// Non-nil process represents process custody. Wait completion is explicitly
	// blocked, so EOF/close cannot substitute for the actual join.
	session.proc = &exec.Cmd{}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		session.waitForTerminal(func() (error, bool) { close(entered); <-release; return errors.New("terminated and joined"), true })
	}()
	<-entered
	session.close()
	select {
	case <-complete:
		t.Fatal("EOF released slot before Wait")
	default:
	}
	close(release)
	<-done
	select {
	case err := <-complete:
		if err == nil || err.Error() != "terminated and joined" {
			t.Fatal(err)
		}
	default:
		t.Fatal("joined terminal missing")
	}
	session.completeTerminal(nil)
	select {
	case <-complete:
		t.Fatal("duplicate completion")
	default:
	}
}

func TestTrackedShellActualProcessExitPrecedesReceipt(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}
	agentConn, engineConn := net.Pipe()
	defer engineConn.Close()
	session := newShellSession(agentConn, "fixture", "bash", "/bin/sh", nil, nil, nil)
	session.requireOperation = true
	session.bound = true
	done := make(chan error, 1)
	session.complete = func(err error) {
		session.procMu.Lock()
		proc := session.proc
		session.procMu.Unlock()
		if proc == nil || proc.ProcessState == nil || !proc.ProcessState.Exited() {
			done <- errors.New("receipt before process exit")
			return
		}
		done <- err
	}
	if err := session.ensureProcessStarted(); err != nil {
		t.Fatal(err)
	}
	session.procMu.Lock()
	stdin := session.stdin
	session.procMu.Unlock()
	if _, err := stdin.Write([]byte("exit 0\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		session.close()
		t.Fatal("process receipt timeout")
	}
}

func TestTrackedShellUnknownWaitRetainsCharge(t *testing.T) {
	agentConn, engineConn := net.Pipe()
	defer engineConn.Close()
	session := newShellSession(agentConn, "fixture", "bash", "", nil, nil, nil)
	var completed atomic.Int32
	session.bound = true
	session.complete = func(error) { completed.Add(1) }
	session.proc = &exec.Cmd{}
	session.waitForTerminal(func() (error, bool) { return errors.New("native wait failed without process state"), false })
	if completed.Load() != 0 {
		t.Fatal("unconfirmed exit emitted terminal")
	}
	if !session.isClosed() {
		t.Fatal("failed transport not closed")
	}
}
