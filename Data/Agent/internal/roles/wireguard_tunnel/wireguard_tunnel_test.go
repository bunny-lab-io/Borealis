package wireguardtunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeAuthClient struct {
	agentID        string
	signingKey     string
	storedKey      string
	ensureResponse map[string]any
	posts          []fakePost
}

type fakePost struct {
	Path    string
	Payload any
}

func (f *fakeAuthClient) PostJSON(ctx context.Context, path string, requestPayload any, responsePayload any) (any, error) {
	f.posts = append(f.posts, fakePost{Path: path, Payload: requestPayload})
	if path == "/api/agent/vpn/ensure" && responsePayload != nil {
		if target, ok := responsePayload.(*map[string]any); ok {
			*target = cloneMap(f.ensureResponse)
		}
	}
	return nil, nil
}

func (f *fakeAuthClient) AgentID() string {
	if f.agentID == "" {
		return "agent-1"
	}
	return f.agentID
}

func (f *fakeAuthClient) LoadServerSigningKey() string {
	return f.storedKey
}

func (f *fakeAuthClient) StoreServerSigningKey(value string) error {
	f.storedKey = value
	return nil
}

func testManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	return &Manager{
		authClient:    &fakeAuthClient{agentID: "agent-1"},
		hostname:      "LAB-01",
		serviceMode:   "system",
		configPath:    dir + "/agent.json",
		baseDir:       dir,
		logPath:       filepath.Join(dir, "Logs", "WireGuard", "wireguard.log"),
		platform:      "linux",
		interfaceName: "wireguard",
		wgQuick:       "wg-quick",
		wg:            "wg",
		ip:            "ip",
		clientPrivate: "client-private",
		clientPublic:  "client-public",
		runner: func(context.Context, time.Duration, string, ...string) (commandResult, error) {
			return commandResult{}, nil
		},
		supported:          true,
		ensureWake:         make(chan string, 4),
		lastTunnelSnapshot: map[string]any{},
	}
}

func testPayload(expiresAt int64) map[string]any {
	return map[string]any{
		"agent_id":           "agent-1",
		"tunnel_id":          "tunnel-1",
		"virtual_ip":         "10.255.0.15/32",
		"allowed_ips":        "10.255.0.1/32",
		"endpoint":           "borealis.example.com:30000",
		"server_public_key":  "server-public",
		"client_private_key": "client-private",
		"client_public_key":  "client-public",
		"allowed_ports":      []any{47002, 5900, 22, 22},
		"token": map[string]any{
			"agent_id":   "agent-1",
			"tunnel_id":  "tunnel-1",
			"expires_at": expiresAt,
			"port":       30000,
		},
	}
}

func TestBuildSessionNormalizesAllowedPortsAndHostRoute(t *testing.T) {
	manager := testManager(t)

	session, err := manager.buildSession(testPayload(time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatalf("buildSession returned error: %v", err)
	}

	if session.TunnelID != "tunnel-1" {
		t.Fatalf("unexpected tunnel id: %s", session.TunnelID)
	}
	if session.AllowedIPs != "10.255.0.1/32" {
		t.Fatalf("unexpected allowed ips: %s", session.AllowedIPs)
	}
	if session.AllowedPorts != "22,5900,47002" {
		t.Fatalf("unexpected allowed ports: %s", session.AllowedPorts)
	}
	if got := manager.configPathForPlatform(); got != filepath.Join(manager.baseDir, "wireguard.conf") {
		t.Fatalf("unexpected config path: %s", got)
	}
}

func TestBuildSessionAcceptsEngineProvidedFallbackEndpoint(t *testing.T) {
	manager := testManager(t)
	payload := testPayload(time.Now().Add(time.Hour).Unix())
	payload["fallback_endpoint"] = "192.168.3.252:30000"

	session, err := manager.buildSession(payload)
	if err != nil {
		t.Fatalf("buildSession returned error: %v", err)
	}
	if session.FallbackEndpoint != "192.168.3.252:30000" {
		t.Fatalf("unexpected fallback endpoint: %s", session.FallbackEndpoint)
	}
}

func TestBuildSessionRejectsBroadRoutes(t *testing.T) {
	manager := testManager(t)
	payload := testPayload(time.Now().Add(time.Hour).Unix())
	payload["allowed_ips"] = "10.255.0.0/16"

	if _, err := manager.buildSession(payload); err == nil || !strings.Contains(err.Error(), "allowed_ips must be single /32") {
		t.Fatalf("expected broad allowed_ips rejection, got %v", err)
	}

	payload = testPayload(time.Now().Add(time.Hour).Unix())
	payload["virtual_ip"] = "10.255.0.15/24"
	if _, err := manager.buildSession(payload); err == nil || !strings.Contains(err.Error(), "virtual_ip must be /32") {
		t.Fatalf("expected broad virtual_ip rejection, got %v", err)
	}
}

func TestNewUsesWireGuardLogCategoryPath(t *testing.T) {
	dir := t.TempDir()
	manager := New(nil, "LAB-01", "system", filepath.Join(dir, "agent.json"))

	if got := manager.logPath; got != filepath.Join(dir, "Logs", "WireGuard", "wireguard.log") {
		t.Fatalf("unexpected log path: %s", got)
	}
}

func TestWindowsTunnelServiceIDMatchesConfigBasename(t *testing.T) {
	manager := testManager(t)
	manager.platform = "windows"

	if got := manager.tunnelName(); got != "wireguard" {
		t.Fatalf("unexpected tunnel name: %s", got)
	}
	if got := manager.tunnelServiceID(); got != "WireGuardTunnel$wireguard" {
		t.Fatalf("unexpected tunnel service id: %s", got)
	}
}

func TestWindowsApplySessionUsesServiceIDFromConfigBasename(t *testing.T) {
	manager := testManager(t)
	manager.platform = "windows"
	manager.wireguardExe = "wireguard.exe"
	session, err := manager.buildSession(testPayload(time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	queryCount := 0
	manager.runner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
		call := name + " " + joinArgs(args)
		calls = append(calls, call)
		if strings.Contains(call, "WireGuardTunnel$Borealis") {
			t.Fatalf("legacy Borealis tunnel service used: %#v", calls)
		}
		if name == "sc.exe" && len(args) >= 2 && args[0] == "query" {
			if args[1] != "WireGuardTunnel$wireguard" {
				t.Fatalf("unexpected service query target: %s", args[1])
			}
			queryCount++
			if queryCount == 1 {
				return commandResult{ExitCode: 1}, nil
			}
			return commandResult{ExitCode: 0, Stdout: "STATE              : 4  RUNNING"}, nil
		}
		return commandResult{ExitCode: 0}, nil
	}

	if err := manager.applyWindowsSession(context.Background(), session); err != nil {
		t.Fatalf("applyWindowsSession returned error: %v", err)
	}

	expectedCalls := []string{
		"wireguard.exe /installtunnelservice " + manager.configPathForPlatform(),
		"sc.exe stop WireGuardTunnel$wireguard",
		"sc.exe start WireGuardTunnel$wireguard",
		"sc.exe config WireGuardTunnel$wireguard DisplayName= Borealis Agent - WireGuard",
	}
	for _, expected := range expectedCalls {
		if !containsCall(calls, expected) {
			t.Fatalf("missing call %q in %#v", expected, calls)
		}
	}
}

func TestWindowsFirewallUsesPowerShellPortArray(t *testing.T) {
	manager := testManager(t)
	manager.platform = "windows"
	var addCommands []string
	manager.runner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
		if name == "powershell.exe" && len(args) > 0 {
			command := args[len(args)-1]
			if strings.Contains(command, "New-NetFirewallRule") {
				addCommands = append(addCommands, command)
			}
		}
		return commandResult{ExitCode: 0}, nil
	}

	manager.ensureWindowsFirewall(context.Background(), "10.255.0.1/32", "47002,22,5900")

	if len(addCommands) != 2 {
		t.Fatalf("expected two firewall add commands, got %#v", addCommands)
	}
	for _, command := range addCommands {
		if !strings.Contains(command, "-LocalPort @(22,5900,47002)") {
			t.Fatalf("firewall command did not use port array: %s", command)
		}
		if strings.Contains(command, "-LocalPort '22,5900,47002'") {
			t.Fatalf("firewall command still quotes comma port string: %s", command)
		}
	}
}

func TestValidateSignedTokenStoresServerSigningKey(t *testing.T) {
	manager := testManager(t)
	authClient := &fakeAuthClient{agentID: "agent-1"}
	manager.authClient = authClient
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	signingKey := base64.StdEncoding.EncodeToString(publicDER)
	token := map[string]any{
		"agent_id":    "agent-1",
		"tunnel_id":   "tunnel-1",
		"expires_at":  time.Now().Add(time.Hour).Unix(),
		"port":        30000,
		"signing_key": signingKey,
		"sig_alg":     "ed25519",
	}
	payloadBytes, err := canonicalTokenPayload(token)
	if err != nil {
		t.Fatal(err)
	}
	token["signature"] = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payloadBytes))

	if err := manager.validateToken(token); err != nil {
		t.Fatalf("validateToken returned error: %v", err)
	}
	if authClient.storedKey != signingKey {
		t.Fatalf("signing key was not stored")
	}
}

func TestLinuxStartSessionWritesConfigAndRunsWgQuick(t *testing.T) {
	manager := testManager(t)
	session, err := manager.buildSession(testPayload(time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	manager.runner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
		calls = append(calls, name+" "+joinArgs(args))
		return commandResult{ExitCode: 0}, nil
	}

	if err := manager.startSession(context.Background(), session); err != nil {
		t.Fatalf("startSession returned error: %v", err)
	}

	configBytes, err := os.ReadFile(manager.configPathForPlatform())
	if err != nil {
		t.Fatal(err)
	}
	configText := string(configBytes)
	for _, expected := range []string{
		"PrivateKey = client-private",
		"Address = 10.255.0.15/32",
		"AllowedIPs = 10.255.0.1/32",
		"Endpoint = borealis.example.com:30000",
		"MTU = 1420",
	} {
		if !strings.Contains(configText, expected) {
			t.Fatalf("config missing %q:\n%s", expected, configText)
		}
	}
	expectedCalls := []string{
		"wg-quick down " + manager.configPathForPlatform(),
		"ip link delete dev wireguard",
		"ip link delete dev borealis",
		"wg-quick up " + manager.configPathForPlatform(),
		"ip link set dev wireguard mtu 1420",
	}
	for _, expected := range expectedCalls {
		if !containsCall(calls, expected) {
			t.Fatalf("missing call %q in %#v", expected, calls)
		}
	}
}

func TestLinuxStartSessionInstallsWireGuardToolsWithDNFWhenMissing(t *testing.T) {
	manager := testManager(t)
	manager.wgQuick = ""
	manager.wg = ""
	installed := false
	manager.executableResolver = func(name string) string {
		switch name {
		case "dnf":
			return "/usr/bin/dnf"
		case "wg-quick", "wg":
			if installed {
				return "/usr/bin/" + name
			}
		}
		return ""
	}
	session, err := manager.buildSession(testPayload(time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	manager.runner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
		calls = append(calls, name+" "+joinArgs(args))
		if name == "/usr/bin/dnf" {
			if timeout != packageInstallTimeout {
				t.Fatalf("unexpected package install timeout: %s", timeout)
			}
			installed = true
		}
		return commandResult{ExitCode: 0}, nil
	}

	if err := manager.startSession(context.Background(), session); err != nil {
		t.Fatalf("startSession returned error: %v", err)
	}

	expectedCalls := []string{
		"/usr/bin/dnf install -y wireguard-tools",
		"/usr/bin/wg-quick down " + manager.configPathForPlatform(),
		"/usr/bin/wg-quick up " + manager.configPathForPlatform(),
	}
	for _, expected := range expectedCalls {
		if !containsCall(calls, expected) {
			t.Fatalf("missing call %q in %#v", expected, calls)
		}
	}
}

func TestLinuxWireGuardToolsInstallFailureUsesRetryCooldown(t *testing.T) {
	manager := testManager(t)
	manager.wgQuick = ""
	manager.wg = ""
	manager.executableResolver = func(name string) string {
		if name == "dnf" {
			return "/usr/bin/dnf"
		}
		return ""
	}
	installCalls := 0
	manager.runner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
		if name == "/usr/bin/dnf" {
			installCalls++
			return commandResult{ExitCode: 1, Stderr: "AppStream unavailable"}, nil
		}
		return commandResult{ExitCode: 0}, nil
	}

	firstErr := manager.ensureLinuxWireGuardTools(context.Background())
	if firstErr == nil || !strings.Contains(firstErr.Error(), "dnf install wireguard-tools failed: AppStream unavailable") {
		t.Fatalf("unexpected first dependency error: %v", firstErr)
	}
	secondErr := manager.ensureLinuxWireGuardTools(context.Background())
	if secondErr == nil || !strings.Contains(secondErr.Error(), "automatic retry deferred") {
		t.Fatalf("unexpected cooldown dependency error: %v", secondErr)
	}
	if installCalls != 1 {
		t.Fatalf("expected one package install attempt during cooldown, got %d", installCalls)
	}
}

func TestLinuxWireGuardToolsReprobeDetectsManualRepairDuringCooldown(t *testing.T) {
	manager := testManager(t)
	manager.wgQuick = ""
	manager.wg = ""
	installed := false
	manager.executableResolver = func(name string) string {
		switch name {
		case "dnf":
			return "/usr/bin/dnf"
		case "wg-quick", "wg":
			if installed {
				return "/usr/bin/" + name
			}
		}
		return ""
	}
	installCalls := 0
	manager.runner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
		if name == "/usr/bin/dnf" {
			installCalls++
			return commandResult{ExitCode: 1, Stderr: "temporary repository error"}, nil
		}
		return commandResult{ExitCode: 0}, nil
	}

	if err := manager.ensureLinuxWireGuardTools(context.Background()); err == nil {
		t.Fatal("expected first package install attempt to fail")
	}
	installed = true
	if err := manager.ensureLinuxWireGuardTools(context.Background()); err != nil {
		t.Fatalf("manual dependency repair was not detected during cooldown: %v", err)
	}
	if installCalls != 1 {
		t.Fatalf("unexpected repeated package install after manual repair: %d", installCalls)
	}
}

func TestLinuxStartSessionUsesServerIPFallbackWhenWireGuardCannotResolveEndpoint(t *testing.T) {
	manager := testManager(t)
	manager.serverHost = "borealis.example.com"
	manager.serverIPFallback = "192.168.3.251"
	session, err := manager.buildSession(testPayload(time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatal(err)
	}
	var wgUpEndpoints []string
	manager.runner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
		if name == "wg-quick" && len(args) > 0 && args[0] == "up" {
			configBytes, err := os.ReadFile(manager.configPathForPlatform())
			if err != nil {
				t.Fatal(err)
			}
			configText := string(configBytes)
			wgUpEndpoints = append(wgUpEndpoints, endpointLine(configText))
			if strings.Contains(configText, "Endpoint = borealis.example.com:30000") {
				return commandResult{ExitCode: 1, Stderr: "Name or service not known: `borealis.example.com:30000'\nConfiguration parsing error"}, nil
			}
			if strings.Contains(configText, "Endpoint = 192.168.3.251:30000") {
				return commandResult{ExitCode: 0}, nil
			}
			return commandResult{ExitCode: 1, Stderr: "unexpected endpoint"}, nil
		}
		if name == "wg" && len(args) == 3 && args[2] == "latest-handshakes" {
			configBytes, err := os.ReadFile(manager.configPathForPlatform())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(configBytes), "Endpoint = 192.168.3.251:30000") {
				return commandResult{ExitCode: 0, Stdout: "server-public " + strconv.FormatInt(time.Now().Unix(), 10)}, nil
			}
		}
		return commandResult{ExitCode: 0}, nil
	}

	if err := manager.startSession(context.Background(), session); err != nil {
		t.Fatalf("startSession returned error: %v", err)
	}

	configBytes, err := os.ReadFile(manager.configPathForPlatform())
	if err != nil {
		t.Fatal(err)
	}
	configText := string(configBytes)
	if !strings.Contains(configText, "Endpoint = 192.168.3.251:30000") {
		t.Fatalf("config did not use server_ip_fallback:\n%s", configText)
	}
	if manager.lastAppliedEndpoint != "192.168.3.251:30000" {
		t.Fatalf("unexpected applied endpoint: %s", manager.lastAppliedEndpoint)
	}
	expected := []string{"Endpoint = borealis.example.com:30000", "Endpoint = 192.168.3.251:30000"}
	if strings.Join(wgUpEndpoints, "|") != strings.Join(expected, "|") {
		t.Fatalf("unexpected wg-quick endpoint attempts: %#v", wgUpEndpoints)
	}
}

func TestLinuxStartSessionFallsBackWhenPrimaryEndpointNeverHandshakes(t *testing.T) {
	manager := testManager(t)
	payload := testPayload(time.Now().Add(time.Hour).Unix())
	payload["fallback_endpoint"] = "192.168.3.252:30000"
	session, err := manager.buildSession(payload)
	if err != nil {
		t.Fatal(err)
	}
	var wgUpEndpoints []string
	manager.runner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
		if name == "wg-quick" && len(args) > 0 && args[0] == "up" {
			configBytes, readErr := os.ReadFile(manager.configPathForPlatform())
			if readErr != nil {
				t.Fatal(readErr)
			}
			wgUpEndpoints = append(wgUpEndpoints, endpointLine(string(configBytes)))
		}
		if name == "wg" && len(args) == 3 && args[2] == "latest-handshakes" {
			configBytes, readErr := os.ReadFile(manager.configPathForPlatform())
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(configBytes), "Endpoint = 192.168.3.252:30000") {
				return commandResult{ExitCode: 0, Stdout: "server-public " + strconv.FormatInt(time.Now().Unix(), 10)}, nil
			}
		}
		return commandResult{ExitCode: 0}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if err := manager.startSession(ctx, session); err != nil {
		t.Fatalf("startSession returned error: %v", err)
	}

	expected := []string{"Endpoint = borealis.example.com:30000", "Endpoint = 192.168.3.252:30000"}
	if strings.Join(wgUpEndpoints, "|") != strings.Join(expected, "|") {
		t.Fatalf("unexpected wg-quick endpoint attempts: %#v", wgUpEndpoints)
	}
	if manager.lastAppliedEndpoint != "192.168.3.252:30000" {
		t.Fatalf("unexpected applied endpoint: %s", manager.lastAppliedEndpoint)
	}
}

func TestWireGuardServerIPFallbackRequiresEngineHostMatch(t *testing.T) {
	manager := testManager(t)
	manager.serverHost = "borealis.example.com"
	manager.serverIPFallback = "192.168.3.251"

	if endpoint, ok := manager.wireGuardFallbackEndpoint("borealis.example.com:30000"); !ok || endpoint != "192.168.3.251:30000" {
		t.Fatalf("expected fallback endpoint, got endpoint=%q ok=%v", endpoint, ok)
	}
	if endpoint, ok := manager.wireGuardFallbackEndpoint("other.example.com:30000"); ok || endpoint != "" {
		t.Fatalf("unexpected fallback endpoint for unrelated host endpoint=%q ok=%v", endpoint, ok)
	}
}

func TestNotifyReadyPostsPayloadAndReportsStatus(t *testing.T) {
	manager := testManager(t)
	authClient := &fakeAuthClient{agentID: "agent-1"}
	manager.authClient = authClient
	session, err := manager.buildSession(testPayload(time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatal(err)
	}
	manager.session = session
	manager.runner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
		if name == "wg" && len(args) == 3 && args[2] == "latest-handshakes" {
			return commandResult{ExitCode: 0, Stdout: "server-public " + strconv.FormatInt(time.Now().Unix(), 10)}, nil
		}
		if name == "wg" {
			return commandResult{ExitCode: 0, Stdout: "interface: wireguard"}, nil
		}
		return commandResult{ExitCode: 0}, nil
	}
	var statusPhases []string
	manager.statusReporter = func(ctx context.Context, phase string, status string, message string) error {
		statusPhases = append(statusPhases, phase+":"+status)
		return nil
	}

	manager.notifyReady(context.Background(), session, "vpn_tunnel_start")

	if len(authClient.posts) != 1 {
		t.Fatalf("expected one post, got %#v", authClient.posts)
	}
	if authClient.posts[0].Path != "/api/agent/vpn/ready" {
		t.Fatalf("unexpected post path: %s", authClient.posts[0].Path)
	}
	payload := authClient.posts[0].Payload.(map[string]any)
	if payload["tunnel_id"] != "tunnel-1" || payload["service_state"] != "RUNNING" {
		t.Fatalf("unexpected ready payload: %#v", payload)
	}
	if len(statusPhases) != 1 || statusPhases[0] != "wireguard_online:complete" {
		t.Fatalf("unexpected status phases: %#v", statusPhases)
	}
}

func TestHealthRequiresFreshLinuxPeerHandshake(t *testing.T) {
	manager := testManager(t)
	session, err := manager.buildSession(testPayload(time.Now().Add(time.Hour).Unix()))
	if err != nil {
		t.Fatal(err)
	}
	manager.session = session
	manager.loopRunning = true
	manager.runner = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
		if name == "wg" && len(args) == 2 && args[0] == "show" {
			return commandResult{ExitCode: 0, Stdout: "interface: wireguard"}, nil
		}
		if name == "wg" && len(args) == 3 && args[2] == "latest-handshakes" {
			return commandResult{ExitCode: 0, Stdout: "server-public 0"}, nil
		}
		return commandResult{ExitCode: 1}, nil
	}

	health := manager.Health()
	if health.StatusCode != "recovering" {
		t.Fatalf("expected recovering health without handshake, got %#v", health)
	}
	if !strings.Contains(strings.ToLower(health.Detail), "awaiting peer handshake") {
		t.Fatalf("unexpected health detail: %s", health.Detail)
	}
	if health.Details["last_handshake_at"] != "0" {
		t.Fatalf("unexpected handshake detail: %#v", health.Details)
	}
}

func TestWireGuardHandshakeFreshnessAllowsNormalRekeyWindow(t *testing.T) {
	now := time.Now().Unix()
	if !wireGuardHandshakeFresh(now-int64((handshakeFreshness-time.Second)/time.Second), now) {
		t.Fatal("expected handshake inside freshness window")
	}
	if wireGuardHandshakeFresh(now-int64((handshakeFreshness+time.Second)/time.Second), now) {
		t.Fatal("expected handshake beyond freshness window to be stale")
	}
}

func TestResolveInterfaceNameDefaultsToWireguardConfigBasename(t *testing.T) {
	t.Setenv("BOREALIS_WIREGUARD_INTERFACE", "")

	if got := resolveInterfaceName(); got != "wireguard" {
		t.Fatalf("unexpected default interface name: %s", got)
	}
}

func TestHandleStartRejectsOtherAgent(t *testing.T) {
	manager := testManager(t)
	payload := testPayload(time.Now().Add(time.Hour).Unix())
	payload["agent_id"] = "other-agent"

	response, err := manager.HandleStart(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	body := response.(map[string]any)
	if body["error"] != "not_for_agent" {
		t.Fatalf("unexpected response: %#v", body)
	}
}

func joinArgs(args []string) string {
	out := ""
	for index, arg := range args {
		if index > 0 {
			out += " "
		}
		out += arg
	}
	return out
}

func containsCall(calls []string, expected string) bool {
	for _, call := range calls {
		if call == expected {
			return true
		}
	}
	return false
}

func endpointLine(configText string) string {
	for _, line := range strings.Split(configText, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Endpoint = ") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
