package vnc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
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

func TestUltraVNCConfigIncludesSecurityAndCaptureSettings(t *testing.T) {
	settings := ultraVNCSettings(5901, "DBD83CFD727A145800", true, "")
	rendered := renderUltraVNCConfig(settings)
	for _, expected := range []string{
		"[UltraVNC]",
		"UseRegistry=0",
		"AuthRequired=1",
		"PortNumber=5901",
		"SocketConnect=1",
		"AllowLoopback=1",
		"RemoveWallpaper=1",
		"passwd=DBD83CFD727A145800",
		"passwd2=",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("config missing %q:\n%s", expected, rendered)
		}
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

func TestEnsureServiceRestartsRunningServiceWhenConfigChanged(t *testing.T) {
	calls := []string{}
	queryCount := 0
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
				if queryCount == 1 {
					return commandResult{Stdout: "STATE              : 4  RUNNING", ExitCode: 0}, nil
				}
				return commandResult{Stdout: "STATE              : 1  STOPPED", ExitCode: 0}, nil
			case "config", "failure", "stop", "start":
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

func TestStartServiceTreatsAlreadyRunningErrorAsSuccess(t *testing.T) {
	manager := &Manager{
		serviceName: serviceName,
		runner: func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
			return commandResult{Stdout: "[SC] StartService FAILED 1056:\n\nAn instance of the service is already running.", ExitCode: 1056}, errors.New("exit status 1056")
		},
	}

	if err := manager.startService(context.Background(), serviceName); err != nil {
		t.Fatalf("startService returned error for already-running service: %v", err)
	}
}

func containsCall(calls []string, expected string) bool {
	for _, call := range calls {
		if call == expected {
			return true
		}
	}
	return false
}
