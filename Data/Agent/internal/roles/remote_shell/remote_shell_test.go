package remoteshell

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
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

func TestShellSessionPingPongAndBashStdout(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh unavailable")
	}
	agentConn, engineConn := net.Pipe()
	defer engineConn.Close()
	_ = engineConn.SetDeadline(time.Now().Add(5 * time.Second))

	var logs []string
	session := newShellSession(agentConn, "10.255.0.1:12345", "bash", "/bin/sh", func(format string, args ...any) {
		logs = append(logs, strings.TrimSpace(format))
	}, nil)
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
	t.Fatalf("did not receive expected stdout; logs=%#v", logs)
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
