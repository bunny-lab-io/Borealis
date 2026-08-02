package vnc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeVNCAuthClient struct {
	agentID string
}

func (f fakeVNCAuthClient) PostJSON(ctx context.Context, path string, requestPayload any, responsePayload any) (any, error) {
	return nil, nil
}

func (f fakeVNCAuthClient) AgentID() string {
	return f.agentID
}

func sampleDisplayTopology() []map[string]any {
	return []map[string]any{
		{
			"id":            "2",
			"display_index": 2,
			"label":         "2",
			"device_name":   `\\.\DISPLAY2`,
			"left":          -1920,
			"top":           0,
			"right":         0,
			"bottom":        1080,
			"width":         1920,
			"height":        1080,
			"work_left":     -1920,
			"work_top":      0,
			"work_right":    0,
			"work_bottom":   1040,
			"work_width":    1920,
			"work_height":   1040,
			"primary":       false,
			"source":        "display_settings",
		},
		{
			"id":            "1",
			"display_index": 1,
			"label":         "1",
			"device_name":   `\\.\DISPLAY1`,
			"left":          0,
			"top":           0,
			"right":         2560,
			"bottom":        1440,
			"width":         2560,
			"height":        1440,
			"work_left":     0,
			"work_top":      0,
			"work_right":    2560,
			"work_bottom":   1400,
			"work_width":    2560,
			"work_height":   1400,
			"primary":       true,
			"source":        "display_settings",
		},
	}
}

func TestNewUsesUltraVNCLogCategoryPath(t *testing.T) {
	dir := t.TempDir()
	manager := New(nil, "LAB-01", "system", filepath.Join(dir, "agent.json"))

	if got := manager.logPath; got != filepath.Join(dir, "Logs", "UltraVNC", "vnc.log") {
		t.Fatalf("unexpected log path: %s", got)
	}
}

func TestDisplayVirtualBoundsIncludesNegativeOrigins(t *testing.T) {
	bounds := displayVirtualBounds(sampleDisplayTopology())

	if bounds["left"] != -1920 || bounds["top"] != 0 || bounds["right"] != 2560 || bounds["bottom"] != 1440 {
		t.Fatalf("unexpected bounds: %#v", bounds)
	}
	if bounds["width"] != 4480 || bounds["height"] != 1440 {
		t.Fatalf("unexpected size: %#v", bounds)
	}
}

func TestSelectDisplayTopologyPrefersRicherFallback(t *testing.T) {
	primaryOnly := []map[string]any{
		{
			"id":            "1",
			"display_index": 1,
			"left":          0,
			"top":           0,
			"right":         5760,
			"bottom":        1440,
			"width":         5760,
			"height":        1440,
			"primary":       true,
		},
	}

	selected := selectDisplayTopology(primaryOnly, sampleDisplayTopology())

	if len(selected) != 2 {
		t.Fatalf("expected richer fallback topology, got %#v", selected)
	}
	if selected[0]["display_index"] != 1 || selected[1]["display_index"] != 2 {
		t.Fatalf("expected display-index sorting, got %#v", selected)
	}
}

func TestSelectDisplayTopologyPrefersLargerFallbackBounds(t *testing.T) {
	primaryOnly := []map[string]any{
		{
			"id":            "1",
			"display_index": 1,
			"left":          0,
			"top":           0,
			"right":         1920,
			"bottom":        1080,
			"width":         1920,
			"height":        1080,
			"primary":       true,
		},
	}
	fallback := []map[string]any{
		{
			"id":            "1",
			"display_index": 1,
			"left":          0,
			"top":           0,
			"right":         5760,
			"bottom":        1440,
			"width":         5760,
			"height":        1440,
			"primary":       true,
		},
	}

	selected := selectDisplayTopology(primaryOnly, fallback)

	if len(selected) != 1 || selected[0]["width"] != 5760 {
		t.Fatalf("expected larger fallback bounds, got %#v", selected)
	}
}

func TestSelectDisplayTopologyPrefersFallbackWhenGeometryDiffers(t *testing.T) {
	primaryWrong := []map[string]any{
		{
			"id":            "1",
			"display_index": 1,
			"left":          0,
			"top":           0,
			"right":         5760,
			"bottom":        1440,
			"width":         5760,
			"height":        1440,
			"primary":       true,
		},
		{
			"id":            "2",
			"display_index": 2,
			"left":          5760,
			"top":           360,
			"right":         7680,
			"bottom":        1440,
			"width":         1920,
			"height":        1080,
			"primary":       false,
		},
	}
	fallbackCorrect := []map[string]any{
		{
			"id":            "1",
			"display_index": 1,
			"left":          0,
			"top":           0,
			"right":         5760,
			"bottom":        1440,
			"width":         5760,
			"height":        1440,
			"primary":       true,
		},
		{
			"id":            "2",
			"display_index": 2,
			"left":          -1920,
			"top":           360,
			"right":         0,
			"bottom":        1440,
			"width":         1920,
			"height":        1080,
			"primary":       false,
		},
	}

	selected := selectDisplayTopology(primaryWrong, fallbackCorrect)

	if len(selected) != 2 || selected[1]["left"] != -1920 {
		t.Fatalf("expected monitor-info fallback geometry, got %#v", selected)
	}
}

func TestEnsureRequestPayloadIncludesDisplayTopology(t *testing.T) {
	manager := &Manager{
		authClient:       fakeVNCAuthClient{agentID: "agent-1"},
		displayCollector: sampleDisplayTopology,
	}

	payload := manager.ensureRequestPayload("agent_boot", "bootpass", 12345)

	if got := payload["display_topology"].([]map[string]any); len(got) != 2 {
		t.Fatalf("unexpected topology: %#v", got)
	}
	bounds := payload["display_virtual_bounds"].(map[string]any)
	if bounds["width"] != 4480 || bounds["height"] != 1440 {
		t.Fatalf("unexpected bounds: %#v", bounds)
	}
}

func TestCredentialPayloadIncludesDisplayTopology(t *testing.T) {
	manager := &Manager{
		authClient:         fakeVNCAuthClient{agentID: "agent-1"},
		displayCollector:   sampleDisplayTopology,
		controllerPassword: "bootpass",
		credentialRevision: 12345,
		lastServiceState:   "RUNNING",
		lastListenerState:  "listening",
		lastReady:          true,
		port:               5900,
	}

	payload := manager.credentialPayload("request-1", "vnc_establish")

	if got := payload["display_topology"].([]map[string]any); len(got) != 2 {
		t.Fatalf("unexpected topology: %#v", got)
	}
	bounds := payload["display_virtual_bounds"].(map[string]any)
	if bounds["width"] != 4480 || bounds["height"] != 1440 {
		t.Fatalf("unexpected bounds: %#v", bounds)
	}
}

func TestCredentialRequestUsesFastPathWhenReady(t *testing.T) {
	runnerCalls := 0
	manager := &Manager{
		authClient:         fakeVNCAuthClient{agentID: "agent-1"},
		supported:          true,
		started:            true,
		displayCollector:   sampleDisplayTopology,
		controllerPassword: "bootpass",
		credentialRevision: 12345,
		credentialIssuedAt: time.Now(),
		allowedIPs:         "10.255.0.1/32",
		lastServiceState:   "RUNNING",
		lastListenerState:  "listening",
		lastReady:          true,
		port:               5900,
		runner: func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
			runnerCalls++
			return commandResult{}, errors.New("runner should not be called on credential fast path")
		},
	}

	rawPayload, err := manager.HandleCredentialRequest(context.Background(), map[string]any{
		"agent_id":   "agent-1",
		"request_id": "request-1",
		"reason":     "vnc_establish",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := rawPayload.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %#v", rawPayload)
	}
	if runnerCalls != 0 {
		t.Fatalf("credential fast path touched service runner %d times", runnerCalls)
	}
	if payload["controller_password"] != "bootpass" {
		t.Fatalf("unexpected password payload: %#v", payload)
	}
	if payload["ready"] != true {
		t.Fatalf("unexpected ready payload: %#v", payload)
	}
	if got := payload["display_topology"].([]map[string]any); len(got) != 2 {
		t.Fatalf("unexpected topology: %#v", got)
	}
}

func TestHealthIncludesDisplayTopologyDetails(t *testing.T) {
	manager := &Manager{
		authClient:         fakeVNCAuthClient{agentID: "agent-1"},
		supported:          true,
		displayCollector:   sampleDisplayTopology,
		controllerPassword: "bootpass",
		credentialRevision: 12345,
		serviceName:        serviceName,
		lastServiceState:   "RUNNING",
		lastListenerState:  "listening",
		lastReady:          true,
		port:               5900,
	}

	health := manager.Health()
	topologyRaw, ok := health.Details["display_topology"].(string)
	if !ok || topologyRaw == "" {
		t.Fatalf("missing display_topology detail: %#v", health.Details)
	}
	if topologyRaw != health.Details["display_topology_json"] {
		t.Fatalf("display topology json aliases diverged: %#v", health.Details)
	}
	var topology []map[string]any
	if err := json.Unmarshal([]byte(topologyRaw), &topology); err != nil {
		t.Fatalf("display topology detail is not JSON: %v", err)
	}
	if len(topology) != 2 {
		t.Fatalf("unexpected topology: %#v", topology)
	}
	boundsRaw, ok := health.Details["display_virtual_bounds"].(string)
	if !ok || boundsRaw == "" {
		t.Fatalf("missing display_virtual_bounds detail: %#v", health.Details)
	}
	var bounds map[string]any
	if err := json.Unmarshal([]byte(boundsRaw), &bounds); err != nil {
		t.Fatalf("display virtual bounds detail is not JSON: %v", err)
	}
	if int(bounds["width"].(float64)) != 4480 || int(bounds["height"].(float64)) != 1440 {
		t.Fatalf("unexpected bounds: %#v", bounds)
	}
}

func TestUltraVNCPasswordHashMatchesStoredFormat(t *testing.T) {
	got, err := ultraVNCPasswordHash("password")
	if err != nil {
		t.Fatal(err)
	}
	if got != "DBD83CFD727A145800" {
		t.Fatalf("unexpected password hash: %s", got)
	}

	got, err = ultraVNCPasswordHash("bootpass")
	if err != nil {
		t.Fatal(err)
	}
	if got != "E82E982EF7C0723800" {
		t.Fatalf("unexpected bootpass hash: %s", got)
	}

	truncated, err := ultraVNCPasswordHash("password-more")
	if err != nil {
		t.Fatal(err)
	}
	if truncated != "DBD83CFD727A145800" {
		t.Fatalf("unexpected truncated password hash: %s", truncated)
	}
}

func TestNormalizeFirewallRemoteRequiresSingleHost(t *testing.T) {
	if got := normalizeFirewallRemote("10.255.0.1/32"); got != "10.255.0.1/32" {
		t.Fatalf("unexpected normalized host: %s", got)
	}
	if got := normalizeFirewallRemote("10.255.0.1"); got != "10.255.0.1/32" {
		t.Fatalf("unexpected normalized bare host: %s", got)
	}
	if got := normalizeFirewallRemote("not-an-ip"); got != "" {
		t.Fatalf("expected invalid host to be rejected, got %s", got)
	}
}

func TestEnsureFirewallRecreatesRule(t *testing.T) {
	manager := &Manager{}
	var commands []string
	manager.runner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
		if name == "powershell.exe" && len(args) > 0 {
			commands = append(commands, args[len(args)-1])
		}
		return commandResult{ExitCode: 0}, nil
	}

	if err := manager.ensureFirewall(context.Background(), "10.255.0.1/32", 5900); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 {
		t.Fatalf("expected one firewall command, got %#v", commands)
	}
	command := commands[0]
	for _, expected := range []string{
		"Remove-NetFirewallRule",
		"New-NetFirewallRule",
		"-LocalPort 5900",
		"-RemoteAddress '10.255.0.1/32'",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("firewall command missing %q: %s", expected, command)
		}
	}
	if strings.Contains(command, "Set-NetFirewallRule") {
		t.Fatalf("firewall command should recreate rule instead of updating filters: %s", command)
	}
}

func TestUltraVNCConfigIncludesSecurityAndCaptureSettings(t *testing.T) {
	settings := ultraVNCSettings(5901, "DBD83CFD727A145800", true, "")
	rendered := renderUltraVNCConfig(settings)
	for _, expected := range []string{
		"[admin]",
		"[UltraVNC]",
		"[poll]",
		"UseRegistry=0",
		"AuthRequired=1",
		"PortNumber=5901",
		"SocketConnect=1",
		"AllowLoopback=1",
		"FileTransferEnabled=0",
		"RemoveWallpaper=1",
		"passwd=DBD83CFD727A145800",
		"passwd2=",
		"PollFullScreen=1",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("config missing %q:\n%s", expected, rendered)
		}
	}
	for _, expected := range []string{"primary=1", "secondary=1"} {
		if !sectionContains(rendered, "admin", expected) {
			t.Fatalf("admin section missing %q:\n%s", expected, rendered)
		}
	}
	if !sectionContains(rendered, "poll", "EnableVirtual=0") {
		t.Fatalf("poll section missing EnableVirtual=0:\n%s", rendered)
	}
	if !sectionContains(rendered, "UltraVNC", "passwd=DBD83CFD727A145800") {
		t.Fatalf("UltraVNC section missing password:\n%s", rendered)
	}
}

func TestEnsureConfigReportsOnlyActualChanges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BOREALIS_VNC_CONFIG_DIR", dir)
	manager := &Manager{}

	configPath, changed, err := manager.ensureConfig(5900, "bootpass", true)
	if err != nil {
		t.Fatal(err)
	}
	if configPath != filepath.Join(dir, serviceName+".ini") {
		t.Fatalf("unexpected config path: %s", configPath)
	}
	if !changed {
		t.Fatalf("expected first config write to report changed")
	}

	_, changed, err = manager.ensureConfig(5900, "bootpass", true)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("expected identical config write to report unchanged")
	}

	_, changed, err = manager.ensureConfig(5900, "newpass", true)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatalf("expected password change to report changed")
	}
}

func TestRoutineAlwaysOnEnsureDoesNotWriteSteadyStateLogs(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	t.Setenv("BOREALIS_VNC_CONFIG_DIR", configDir)
	t.Setenv("BOREALIS_VNC_TRACE", "")

	exePath := filepath.Join(dir, "winvnc.exe")
	if err := os.WriteFile(exePath, []byte("stub"), 0644); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	manager := &Manager{
		supported:           true,
		configPath:          filepath.Join(dir, "agent.json"),
		logPath:             filepath.Join(dir, "vnc.log"),
		serviceName:         serviceName,
		vncExe:              exePath,
		port:                port,
		allowedIPs:          "10.255.0.1/32",
		controllerPassword:  "bootpass",
		credentialRevision:  12345,
		removeWallpaper:     true,
		serviceConfigLoaded: true,
		lastReady:           true,
		lastServiceState:    "RUNNING",
		lastListenerState:   "listening",
		runner: func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
			if name == "sc.exe" && len(args) > 0 && args[0] == "query" {
				return commandResult{Stdout: "STATE              : 4  RUNNING", ExitCode: 0}, nil
			}
			return commandResult{Stdout: "ok", ExitCode: 0}, nil
		},
	}
	if _, _, err := manager.ensureConfig(port, "bootpass", true); err != nil {
		t.Fatal(err)
	}

	if err := manager.ensureAlwaysOn(context.Background(), "always_on_check"); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(manager.logPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("expected steady always-on check to stay quiet, got:\n%s", string(raw))
	}
}

func TestDisconnectEnsureSkipsHeavyWorkWhenAlreadyReady(t *testing.T) {
	for _, reason := range []string{"operator_disconnect", "component_unmount", "vnc_session_end"} {
		t.Run(reason, func(t *testing.T) {
			runnerCalls := 0
			manager := &Manager{
				supported:          true,
				serviceName:        serviceName,
				port:               5900,
				allowedIPs:         "10.255.0.1/32",
				controllerPassword: "bootpass",
				lastReady:          true,
				lastServiceState:   "RUNNING",
				lastListenerState:  "listening",
				runner: func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
					runnerCalls++
					return commandResult{}, errors.New("disconnect ensure should not touch service runner")
				},
			}

			if err := manager.ensureAlwaysOn(context.Background(), reason); err != nil {
				t.Fatal(err)
			}
			if runnerCalls != 0 {
				t.Fatalf("disconnect ensure touched service runner %d times", runnerCalls)
			}
		})
	}
}

func TestEnsureAlwaysOnSerializesLifecycleWork(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	t.Setenv("BOREALIS_VNC_CONFIG_DIR", configDir)
	exePath := filepath.Join(dir, "winvnc.exe")
	if err := os.WriteFile(exePath, []byte("stub"), 0644); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	var activeRunnerCalls int32
	var maxActiveRunnerCalls int32
	manager := &Manager{
		supported:           true,
		configPath:          filepath.Join(dir, "agent.json"),
		logPath:             filepath.Join(dir, "vnc.log"),
		serviceName:         serviceName,
		vncExe:              exePath,
		port:                port,
		allowedIPs:          "10.255.0.1/32",
		controllerPassword:  "bootpass",
		credentialRevision:  12345,
		removeWallpaper:     true,
		serviceConfigLoaded: true,
		lastReady:           true,
		lastServiceState:    "RUNNING",
		lastListenerState:   "listening",
		runner: func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
			current := atomic.AddInt32(&activeRunnerCalls, 1)
			for {
				maxObserved := atomic.LoadInt32(&maxActiveRunnerCalls)
				if current <= maxObserved || atomic.CompareAndSwapInt32(&maxActiveRunnerCalls, maxObserved, current) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			defer atomic.AddInt32(&activeRunnerCalls, -1)

			if name == "sc.exe" && len(args) > 0 && args[0] == "query" {
				return commandResult{Stdout: "STATE              : 4  RUNNING", ExitCode: 0}, nil
			}
			return commandResult{Stdout: "ok", ExitCode: 0}, nil
		},
	}
	if _, _, err := manager.ensureConfig(port, "bootpass", true); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- manager.ensureAlwaysOn(context.Background(), "vnc_establish")
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if max := atomic.LoadInt32(&maxActiveRunnerCalls); max > 1 {
		t.Fatalf("expected serialized VNC lifecycle work, observed %d concurrent runner calls", max)
	}
}

func TestEnsureServiceRestartsRunningServiceWhenConfigChanged(t *testing.T) {
	calls := []string{}
	queryCount := 0
	started := false
	manager := &Manager{
		serviceName: serviceName,
		runner: func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
			call := name + " " + strings.Join(args, " ")
			calls = append(calls, call)
			if len(args) == 0 {
				return commandResult{}, nil
			}
			switch args[0] {
			case "query":
				queryCount++
				if started {
					return commandResult{Stdout: "STATE              : 4  RUNNING", ExitCode: 0}, nil
				}
				if queryCount == 1 {
					return commandResult{Stdout: "STATE              : 4  RUNNING", ExitCode: 0}, nil
				}
				return commandResult{Stdout: "STATE              : 1  STOPPED", ExitCode: 0}, nil
			case "start":
				started = true
				return commandResult{Stdout: "ok", ExitCode: 0}, nil
			case "config", "failure", "stop":
				return commandResult{Stdout: "ok", ExitCode: 0}, nil
			default:
				return commandResult{Stdout: "ok", ExitCode: 0}, nil
			}
		},
	}

	if err := manager.ensureService(context.Background(), "config_changed", "vnc_session_start"); err != nil {
		t.Fatal(err)
	}

	if !containsCall(calls, "sc.exe stop "+serviceName) {
		t.Fatalf("expected service stop call, got %#v", calls)
	}
	if !containsCall(calls, "sc.exe start "+serviceName) {
		t.Fatalf("expected service start call, got %#v", calls)
	}
}

func TestEnsureServiceLeavesRunningServiceAloneWhenConfigUnchanged(t *testing.T) {
	calls := []string{}
	manager := &Manager{
		serviceName: serviceName,
		runner: func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
			call := name + " " + strings.Join(args, " ")
			calls = append(calls, call)
			if len(args) > 0 && args[0] == "query" {
				return commandResult{Stdout: "STATE              : 4  RUNNING", ExitCode: 0}, nil
			}
			return commandResult{Stdout: "ok", ExitCode: 0}, nil
		},
	}

	if err := manager.ensureService(context.Background(), "", "vnc_session_start"); err != nil {
		t.Fatal(err)
	}

	if containsCall(calls, "sc.exe stop "+serviceName) || containsCall(calls, "sc.exe start "+serviceName) {
		t.Fatalf("expected no stop/start calls for unchanged config, got %#v", calls)
	}
}

func TestEnsureServiceForceKillsStuckStopPendingService(t *testing.T) {
	oldTransitionWait := serviceTransitionWait
	oldForceKillWait := serviceTransitionForceKillWait
	oldAlreadyRunningWait := serviceAlreadyRunningVerifyWait
	serviceTransitionWait = time.Millisecond
	serviceTransitionForceKillWait = time.Millisecond
	serviceAlreadyRunningVerifyWait = time.Millisecond
	defer func() {
		serviceTransitionWait = oldTransitionWait
		serviceTransitionForceKillWait = oldForceKillWait
		serviceAlreadyRunningVerifyWait = oldAlreadyRunningWait
	}()

	calls := []string{}
	killed := false
	started := false
	manager := &Manager{
		serviceName: serviceName,
		runner: func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
			call := name + " " + strings.Join(args, " ")
			calls = append(calls, call)
			if name == "taskkill.exe" {
				if strings.Join(args, " ") == "/PID 4242 /F" {
					killed = true
					return commandResult{Stdout: "SUCCESS", ExitCode: 0}, nil
				}
				return commandResult{Stdout: "unexpected pid", ExitCode: 1}, errors.New("unexpected pid")
			}
			if name != "sc.exe" || len(args) == 0 {
				return commandResult{Stdout: "ok", ExitCode: 0}, nil
			}
			switch args[0] {
			case "query":
				if started {
					return commandResult{Stdout: "STATE              : 4  RUNNING", ExitCode: 0}, nil
				}
				if killed {
					return commandResult{Stdout: "STATE              : 1  STOPPED", ExitCode: 0}, nil
				}
				return commandResult{Stdout: "STATE              : 3  STOP_PENDING", ExitCode: 0}, nil
			case "queryex":
				return commandResult{Stdout: "PID                : 4242", ExitCode: 0}, nil
			case "start":
				started = true
				return commandResult{Stdout: "ok", ExitCode: 0}, nil
			case "config", "failure":
				return commandResult{Stdout: "ok", ExitCode: 0}, nil
			default:
				return commandResult{Stdout: "ok", ExitCode: 0}, nil
			}
		},
	}

	if err := manager.ensureService(context.Background(), "", "vnc_establish"); err != nil {
		t.Fatal(err)
	}
	if !containsCall(calls, "taskkill.exe /PID 4242 /F") {
		t.Fatalf("expected stuck service process kill, got %#v", calls)
	}
	if !containsCall(calls, "sc.exe start "+serviceName) {
		t.Fatalf("expected service restart, got %#v", calls)
	}
}

func TestEnsureAlwaysOnClearsReadyWhenServiceStopPending(t *testing.T) {
	oldTransitionWait := serviceTransitionWait
	oldForceKillWait := serviceTransitionForceKillWait
	serviceTransitionWait = time.Millisecond
	serviceTransitionForceKillWait = time.Millisecond
	defer func() {
		serviceTransitionWait = oldTransitionWait
		serviceTransitionForceKillWait = oldForceKillWait
	}()

	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	t.Setenv("BOREALIS_VNC_CONFIG_DIR", configDir)
	exePath := filepath.Join(dir, "winvnc.exe")
	if err := os.WriteFile(exePath, []byte("stub"), 0644); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	manager := &Manager{
		supported:          true,
		configPath:         filepath.Join(dir, "agent.json"),
		logPath:            filepath.Join(dir, "vnc.log"),
		serviceName:        serviceName,
		vncExe:             exePath,
		port:               port,
		allowedIPs:         "10.255.0.1/32",
		controllerPassword: "bootpass",
		lastReady:          true,
		lastServiceState:   "RUNNING",
		lastListenerState:  "listening",
		runner: func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
			if name == "sc.exe" && len(args) > 0 {
				switch args[0] {
				case "query":
					return commandResult{Stdout: "STATE              : 3  STOP_PENDING", ExitCode: 0}, nil
				case "queryex":
					return commandResult{Stdout: "PID                : 0", ExitCode: 0}, nil
				case "config", "failure":
					return commandResult{Stdout: "ok", ExitCode: 0}, nil
				}
			}
			return commandResult{Stdout: "ok", ExitCode: 0}, nil
		},
	}

	err = manager.ensureAlwaysOn(context.Background(), "vnc_establish")
	if err == nil {
		t.Fatal("expected STOP_PENDING service to fail readiness")
	}
	if manager.lastReady {
		t.Fatalf("expected lastReady to be cleared")
	}
	if manager.lastServiceState != "STOP_PENDING" || manager.lastListenerState != "listening" {
		t.Fatalf("unexpected state service=%s listener=%s", manager.lastServiceState, manager.lastListenerState)
	}
}

func TestEnsureAlwaysOnWaitsForDelayedListener(t *testing.T) {
	oldListenerReadyWait := listenerReadyWait
	listenerReadyWait = 250 * time.Millisecond
	defer func() {
		listenerReadyWait = oldListenerReadyWait
	}()

	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	t.Setenv("BOREALIS_VNC_CONFIG_DIR", configDir)
	exePath := filepath.Join(dir, "winvnc.exe")
	if err := os.WriteFile(exePath, []byte("stub"), 0644); err != nil {
		t.Fatal(err)
	}
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := reserved.Addr().String()
	port := reserved.Addr().(*net.TCPAddr).Port
	if err := reserved.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(50 * time.Millisecond)
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			t.Errorf("delayed listener failed: %v", err)
			return
		}
		defer listener.Close()
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	manager := &Manager{
		supported:          true,
		configPath:         filepath.Join(dir, "agent.json"),
		logPath:            filepath.Join(dir, "vnc.log"),
		serviceName:        serviceName,
		vncExe:             exePath,
		port:               port,
		allowedIPs:         "10.255.0.1/32",
		controllerPassword: "bootpass",
		runner: func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
			if name == "powershell.exe" {
				return commandResult{ExitCode: 0}, nil
			}
			if name == "sc.exe" && len(args) > 0 {
				switch args[0] {
				case "query":
					return commandResult{Stdout: "STATE              : 4  RUNNING", ExitCode: 0}, nil
				case "config", "failure":
					return commandResult{Stdout: "ok", ExitCode: 0}, nil
				}
			}
			return commandResult{Stdout: "ok", ExitCode: 0}, nil
		},
	}

	if err := manager.ensureAlwaysOn(context.Background(), "vnc_establish"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("delayed listener did not receive readiness probe")
	}
	if !manager.lastReady || manager.lastListenerState != "listening" || manager.lastServiceState != "RUNNING" {
		t.Fatalf("expected delayed listener readiness, ready=%t service=%s listener=%s", manager.lastReady, manager.lastServiceState, manager.lastListenerState)
	}
}

func TestStartServiceTreatsAlreadyRunningErrorAsSuccess(t *testing.T) {
	manager := &Manager{
		serviceName: serviceName,
		runner: func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
			if name == "sc.exe" && len(args) > 0 && args[0] == "query" {
				return commandResult{Stdout: "STATE              : 4  RUNNING", ExitCode: 0}, nil
			}
			return commandResult{Stdout: "[SC] StartService FAILED 1056:\n\nAn instance of the service is already running.", ExitCode: 1056}, errors.New("exit status 1056")
		},
	}

	if err := manager.startService(context.Background(), serviceName); err != nil {
		t.Fatalf("startService returned error for already-running service: %v", err)
	}
}

func sectionContains(content string, section string, expected string) bool {
	header := "[" + section + "]"
	start := strings.Index(content, header)
	if start < 0 {
		return false
	}
	sectionText := content[start+len(header):]
	if next := strings.Index(sectionText, "\n["); next >= 0 {
		sectionText = sectionText[:next]
	}
	return strings.Contains(sectionText, expected)
}

func containsCall(calls []string, expected string) bool {
	for _, call := range calls {
		if call == expected {
			return true
		}
	}
	return false
}
