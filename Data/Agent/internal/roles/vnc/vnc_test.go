package vnc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
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
	manager := New(nil, "LAB-01", "system", filepath.Join(dir, "config.json"))

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
