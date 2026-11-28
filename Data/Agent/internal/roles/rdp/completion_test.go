package rdp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type readinessConn struct {
	net.Conn
	close func() error
}

func (c readinessConn) Close() error { return c.close() }
func readinessPayload() map[string]any {
	return map[string]any{"agent_id": "system:agent-1", "reason": "rdp_establish", "rdp_port": 3389, "allowed_ips": "10.255.0.1/32", "virtual_ip": "10.255.0.42/32", "timeout_seconds": 1}
}
func readinessFixture(t *testing.T) *Manager {
	t.Helper()
	m := &Manager{authClient: &fakeAuthClient{agentID: "system:agent-1"}, supported: true, port: 3389, engineAddress: "10.255.0.1/32", localAddress: "10.255.0.42/32", logPath: filepath.Join(t.TempDir(), "rdp.log")}
	m.runner = func(context.Context, time.Duration, string, ...string) (commandResult, error) {
		t.Error("tracked request used legacy runner")
		return commandResult{}, errors.New("legacy runner")
	}
	m.completionRunner = func(_ context.Context, timeout time.Duration, name string, args ...string) (commandResult, commandCompletion, error) {
		result := commandResult{}
		if name == "sc.exe" {
			if timeout != 10*time.Second || strings.Join(args, " ") != "query TermService" {
				t.Error("query contract changed")
			}
			result.Stdout = "STATE : 4 RUNNING"
		} else if name != "powershell.exe" || timeout != 30*time.Second || len(args) != 4 {
			t.Error("command contract changed", name, args)
		}
		return result, commandCompletion{true, true}, nil
	}
	m.readinessDial = func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "127.0.0.1:3389" {
			t.Error("probe scope changed", network, address)
		}
		return readinessConn{close: func() error { return nil }}, nil
	}
	return m
}
func markReadinessCached(m *Manager) {
	m.lastReady = true
	m.lastServiceState = "RUNNING"
	m.lastListenerState = "listening"
	m.lastReconcileAt = time.Now().Unix()
}

func TestTrackedRDPReadinessOwnership(t *testing.T) {
	for _, mode := range []string{"success", "cached", "audit_not_ready", "start_failed", "unknown_enablement", "unknown_firewall", "unknown_query", "exit_failed", "stopped_service", "close_failed", "dial_error_with_connection", "late_settings", "cached_late_settings", "wrong_target", "unsupported", "missing_scope"} {
		t.Run(mode, func(t *testing.T) {
			m := readinessFixture(t)
			payload := readinessPayload()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, closes, completed := 0, 0, 0
			var terminalErr error
			original := m.completionRunner
			m.completionRunner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, commandCompletion, error) {
				calls++
				r, o, e := original(ctx, timeout, name, args...)
				switch {
				case mode == "start_failed":
					return commandResult{}, commandCompletion{}, errors.New("no launch")
				case mode == "unknown_enablement" && calls == 1, mode == "unknown_firewall" && calls == 2, mode == "unknown_query" && name == "sc.exe":
					o.joined = false
				case mode == "exit_failed" && calls == 1:
					r.ExitCode = 9
					e = errors.New("joined exit failure")
				case mode == "stopped_service" && name == "sc.exe":
					r.Stdout = "STATE : 1 STOPPED"
				}
				return r, o, e
			}
			m.readinessDial = func(context.Context, string, string) (net.Conn, error) {
				conn := readinessConn{close: func() error {
					closes++
					if mode == "close_failed" {
						return errors.New("close failed")
					}
					if mode == "late_settings" || mode == "cached_late_settings" {
						m.applyEngineSettings(map[string]any{"rdp_port": 3390})
					}
					return nil
				}}
				if mode == "dial_error_with_connection" {
					cancel()
					return conn, errors.New("dial failed with owned connection")
				}
				return conn, nil
			}
			switch mode {
			case "cached", "cached_late_settings":
				markReadinessCached(m)
			case "audit_not_ready":
				m.lastReconcileAt = time.Now().Unix()
				payload["reason"] = "always_on_check"
			case "wrong_target":
				payload["agent_id"] = "other"
			case "unsupported":
				m.supported = false
			case "missing_scope":
				m.engineAddress = ""
				delete(payload, "allowed_ips")
			}
			response, err := m.HandleStartWithCompletion(ctx, payload, func(err error) {
				completed++
				terminalErr = err
				if !m.ensureMu.TryLock() {
					t.Error("callback retained lifecycle lock")
				} else {
					m.ensureMu.Unlock()
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			body := response.(map[string]any)
			if strings.HasPrefix(mode, "unknown_") {
				want := map[string]int{"unknown_enablement": 1, "unknown_firewall": 2, "unknown_query": 3}[mode]
				if completed != 0 || body["status"] != "pending" || calls != want {
					t.Fatal("unknown command lost custody", completed, body, calls)
				}
				return
			}
			success := mode == "success" || mode == "cached" || mode == "audit_not_ready"
			if completed != 1 || (terminalErr == nil) != success || body["ready"] != true && success {
				t.Fatal("readiness terminal mismatch", completed, terminalErr, body)
			}
			if mode == "cached" && (calls != 0 || closes != 1) {
				t.Fatal("cache skipped real joined probe", calls, closes)
			}
			if mode == "audit_not_ready" && calls != 3 {
				t.Fatal("audit timestamp substituted for readiness", calls)
			}
			if mode == "dial_error_with_connection" && closes != 1 {
				t.Fatal("error connection not closed", closes)
			}
			if (mode == "wrong_target" || mode == "unsupported" || mode == "missing_scope") && calls != 0 {
				t.Fatal("prework rejection ran command", calls)
			}
		})
	}
}

func TestTrackedRDPReadinessJoinsCleanupAndCancellation(t *testing.T) {
	for _, mode := range []string{"probe_close", "probe_close_canceled", "command_join_canceled", "lock_canceled", "cached_canceled"} {
		t.Run(mode, func(t *testing.T) {
			m := readinessFixture(t)
			payload := readinessPayload()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			terminal := make(chan error, 1)
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			if mode == "lock_canceled" {
				m.ensureMu.Lock()
			} else if mode == "cached_canceled" {
				markReadinessCached(m)
				cancel()
			}
			original := m.completionRunner
			if mode == "command_join_canceled" {
				m.completionRunner = func(ctx context.Context, d time.Duration, n string, a ...string) (commandResult, commandCompletion, error) {
					close(entered)
					<-release
					r, o, _ := original(ctx, d, n, a...)
					return r, o, ctx.Err()
				}
			} else {
				m.readinessDial = func(context.Context, string, string) (net.Conn, error) {
					return readinessConn{close: func() error { close(entered); <-release; return nil }}, nil
				}
			}
			go func() {
				defer close(done)
				_, _ = m.HandleStartWithCompletion(ctx, payload, func(err error) { terminal <- err })
			}()
			defer func() {
				unblock()
				cancel()
				if mode == "lock_canceled" {
					m.ensureMu.Unlock()
				}
				<-done
			}()
			if mode == "lock_canceled" {
				cancel()
			} else if mode != "cached_canceled" {
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("owned work never entered")
				}
				if mode != "probe_close" {
					cancel()
				}
				select {
				case err := <-terminal:
					t.Fatal("terminal preceded joined cleanup", err)
				case <-time.After(25 * time.Millisecond):
				}
				unblock()
			}
			select {
			case err := <-terminal:
				if (err == nil) != (mode == "probe_close") {
					t.Fatal("wrong terminal after cleanup", err)
				}
			case <-time.After(time.Second):
				t.Fatal("terminal missing after joined cleanup")
			}
		})
	}
}

// Harmless subprocesses exercise Cmd.Output ownership without RDP/service actions.
func TestRDPCommandChild(t *testing.T) {
	if len(os.Args) < 3 {
		return
	}
	mode, path := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	switch mode {
	case "rdp-child-success":
		fmt.Print("STATE : 4 RUNNING")
		os.Exit(0)
	case "rdp-child-failure":
		fmt.Fprint(os.Stderr, "joined native failure")
		os.Exit(9)
	case "rdp-child-parent":
		exe, _ := os.Executable()
		child := exec.Command(exe, "-test.run=^TestRDPCommandChild$", "--", "rdp-child-output", path)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "rdp-child-output":
		_ = os.WriteFile(path+".entered", []byte("entered"), 0600)
		until := time.Now().Add(10 * time.Second)
		for time.Now().Before(until) {
			if _, err := os.Stat(path); err == nil {
				fmt.Print("STATE : 4 RUNNING")
				os.Exit(0)
			}
			time.Sleep(5 * time.Millisecond)
		}
		os.Exit(4)
	}
}
func TestRDPCommandNativeOwnershipAndLegacyParity(t *testing.T) {
	for _, mode := range []string{"success", "failure", "start_failed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			childMode := "rdp-child-" + mode
			if mode == "start_failed" {
				exe = filepath.Join(t.TempDir(), "missing")
			}
			if mode == "canceled" {
				cancel()
			}
			args := []string{"-test.run=^TestRDPCommandChild$", "--", childMode, "unused"}
			old := exec.CommandContext(ctx, exe, args...)
			stdout, oldErr := old.Output()
			result, owned, gotErr := runCommandWithCompletion(ctx, 10*time.Second, exe, args...)
			launched := mode == "success" || mode == "failure"
			if owned.started != launched || owned.joined != launched || result.Stdout != string(stdout) || fmt.Sprint(oldErr) != fmt.Sprint(gotErr) {
				t.Fatal("native ownership/parity mismatch", result, owned, gotErr, oldErr)
			}
			if mode == "failure" && (result.ExitCode != 9 || result.Stderr != "joined native failure") {
				t.Fatal("native failure output lost", result)
			}
		})
	}
}
func TestTrackedRDPReadinessNativeCommandJoin(t *testing.T) {
	for _, mode := range []string{"output_join", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			m := readinessFixture(t)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			release := filepath.Join(t.TempDir(), "release")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			terminal := make(chan error, 1)
			var once sync.Once
			unblock := func() { once.Do(func() { _ = os.WriteFile(release, []byte("done"), 0600) }) }
			defer func() { unblock(); cancel(); <-done }()
			calls := 0
			m.completionRunner = func(ctx context.Context, d time.Duration, n string, a ...string) (commandResult, commandCompletion, error) {
				calls++
				childMode := "rdp-child-success"
				if calls == 1 {
					childMode = "rdp-child-parent"
					if mode == "cancel" {
						childMode = "rdp-child-output"
					}
				}
				result, owned, err := runCommandWithCompletion(ctx, d, exe, "-test.run=^TestRDPCommandChild$", "--", childMode, release)
				if owned.joined && err == nil && result.Stdout != "STATE : 4 RUNNING" {
					t.Error("native command skipped owned output join")
				}
				return result, owned, err
			}
			go func() {
				defer close(done)
				payload := readinessPayload()
				payload["timeout_seconds"] = 60
				_, _ = m.HandleStartWithCompletion(ctx, payload, func(err error) { terminal <- err })
			}()
			until := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(release + ".entered"); err == nil {
					break
				}
				select {
				case err := <-terminal:
					t.Fatal("terminal before child checkpoint", err)
				default:
				}
				if time.Now().After(until) {
					t.Fatal("child did not enter")
				}
				time.Sleep(5 * time.Millisecond)
			}
			select {
			case err := <-terminal:
				t.Fatal("terminal before original output join", err)
			case <-time.After(25 * time.Millisecond):
			}
			if mode == "cancel" {
				cancel()
			} else {
				unblock()
			}
			select {
			case err := <-terminal:
				if (err == nil) != (mode == "output_join") {
					t.Fatal("native completion mismatch", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("terminal missing after native output join")
			}
		})
	}
}
