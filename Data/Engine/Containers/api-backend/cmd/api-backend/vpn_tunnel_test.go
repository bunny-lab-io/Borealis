package main

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testWireGuardRuntime(t *testing.T) *wireGuardRuntime {
	t.Helper()
	enginePrefix := netip.MustParsePrefix("10.255.0.1/32")
	peerPrefix := netip.MustParsePrefix("10.255.0.0/16")
	return &wireGuardRuntime{
		port:           defaultWireGuardPort,
		enginePrefix:   enginePrefix,
		peerPrefix:     peerPrefix,
		allowPorts:     []int{47002, 5900, 22},
		privateKeyPath: filepath.Join(t.TempDir(), "server.key"),
		publicKeyPath:  filepath.Join(t.TempDir(), "server.pub"),
		configRoot:     t.TempDir(),
		interfaceName:  defaultWireGuardInterface,
		managedPeers:   map[string]map[string]any{},
	}
}

func TestWireGuardRuntimeRejectsBroadAllowedIPsBeforeCommand(t *testing.T) {
	runtime := testWireGuardRuntime(t)
	commandCount := 0
	runtime.commandRunner = func(args []string) (int, string, string) {
		commandCount++
		return 0, "", ""
	}

	err := runtime.upsertPeer(map[string]any{
		"agent_id":    "agent-1",
		"public_key":  "peer-public-key-1",
		"allowed_ips": []string{"10.255.0.0/16"},
	})

	if err == nil || !strings.Contains(err.Error(), "ipv4_32") {
		t.Fatalf("expected /32 validation error, got %v", err)
	}
	if commandCount != 0 {
		t.Fatalf("invalid peer touched command path %d time(s)", commandCount)
	}
}

func TestWireGuardRuntimePrefixesRejectUnsafeOverlayConfig(t *testing.T) {
	t.Setenv("BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP", "10.255.0.1/32")
	t.Setenv("BOREALIS_WIREGUARD_PEER_NETWORK", "0.0.0.0/0")

	enginePrefix, peerPrefix := parseWireGuardRuntimePrefixes()

	if enginePrefix.String() != defaultWireGuardEngineIP {
		t.Fatalf("unsafe peer network should keep default engine prefix, got %s", enginePrefix)
	}
	if peerPrefix.String() != defaultWireGuardPeerCIDR {
		t.Fatalf("unsafe peer network should fall back to default peer prefix, got %s", peerPrefix)
	}
}

func TestWireGuardRuntimePrefixesAcceptPrivateContainedOverlayConfig(t *testing.T) {
	t.Setenv("BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP", "192.168.200.1/32")
	t.Setenv("BOREALIS_WIREGUARD_PEER_NETWORK", "192.168.200.0/24")

	enginePrefix, peerPrefix := parseWireGuardRuntimePrefixes()

	if enginePrefix.String() != "192.168.200.1/32" {
		t.Fatalf("expected custom engine prefix, got %s", enginePrefix)
	}
	if peerPrefix.String() != "192.168.200.0/24" {
		t.Fatalf("expected custom peer prefix, got %s", peerPrefix)
	}
}

func TestVPNSessionPayloadIncludesEngineWireGuardFallback(t *testing.T) {
	t.Setenv("BOREALIS_PUBLIC_WIREGUARD_HOST", "borealis.example.com")
	t.Setenv("BOREALIS_PUBLIC_WIREGUARD_PORT", "30000")
	t.Setenv("BOREALIS_ENGINE_IP_FALLBACK", "192.168.3.252")
	session := &vpnSession{
		TunnelID:         "tunnel-1",
		AgentID:          "agent-1",
		VirtualIP:        "10.255.0.35/32",
		ClientPrivateKey: "client-private",
		ClientPublicKey:  "client-public",
		Operators:        map[string]struct{}{},
	}

	payload := session.payload(false)
	if got := cleanText(payload["endpoint"]); got != "borealis.example.com:30000" {
		t.Fatalf("unexpected primary endpoint: %s", got)
	}
	if got := cleanText(payload["fallback_endpoint"]); got != "192.168.3.252:30000" {
		t.Fatalf("unexpected fallback endpoint: %s", got)
	}
}

func TestVPNTunnelAllocateVirtualIPSkipsPeerNetworkAddress(t *testing.T) {
	service := &vpnTunnelService{
		enginePrefix:    netip.MustParsePrefix("10.255.0.1/32"),
		peerPrefix:      netip.MustParsePrefix("10.255.0.0/16"),
		ipLeases:        map[string]string{},
		sessionsByAgent: map[string]*vpnSession{},
	}

	virtualIP, err := service.allocateVirtualIPLocked("agent-1")
	if err != nil {
		t.Fatalf("allocate virtual IP failed: %v", err)
	}
	if virtualIP != "10.255.0.2/32" {
		t.Fatalf("expected first usable peer IP after network and engine addresses, got %s", virtualIP)
	}
}

func TestVPNTunnelAllocateVirtualIPReplacesReservedLease(t *testing.T) {
	service := &vpnTunnelService{
		enginePrefix:    netip.MustParsePrefix("10.255.0.1/32"),
		peerPrefix:      netip.MustParsePrefix("10.255.0.0/16"),
		ipLeases:        map[string]string{"agent-1": "10.255.0.0/32"},
		sessionsByAgent: map[string]*vpnSession{},
	}

	virtualIP, err := service.allocateVirtualIPLocked("agent-1")
	if err != nil {
		t.Fatalf("allocate virtual IP failed: %v", err)
	}
	if virtualIP != "10.255.0.2/32" {
		t.Fatalf("expected reserved lease replacement, got %s", virtualIP)
	}
}

func TestWireGuardRuntimeRejectsReservedAllowedIP(t *testing.T) {
	runtime := testWireGuardRuntime(t)
	commandCount := 0
	runtime.commandRunner = func(args []string) (int, string, string) {
		commandCount++
		return 0, "", ""
	}

	err := runtime.upsertPeer(map[string]any{
		"agent_id":    "agent-1",
		"public_key":  "peer-public-key-1",
		"allowed_ips": []string{"10.255.0.0/32"},
	})

	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved allowed IP rejection, got %v", err)
	}
	if commandCount != 0 {
		t.Fatalf("reserved peer touched command path %d time(s)", commandCount)
	}
}

func TestWireGuardRuntimeRejectsDuplicateAllowedIPAndPublicKey(t *testing.T) {
	runtime := testWireGuardRuntime(t)
	runtime.managedPeers["agent-1"] = map[string]any{
		"agent_id":    "agent-1",
		"public_key":  "peer-public-key-1",
		"allowed_ips": []string{"10.255.0.2/32"},
	}

	_, err := runtime.validatePeerPolicyLocked(
		"agent-2",
		"peer-public-key-2",
		[]string{"10.255.0.2/32"},
		runtime.occupiedPeerPolicyLocked("agent-2"),
	)
	if err == nil || !strings.Contains(err.Error(), "already_assigned") {
		t.Fatalf("expected duplicate allowed IP rejection, got %v", err)
	}

	_, err = runtime.validatePeerPolicyLocked(
		"agent-2",
		"peer-public-key-1",
		[]string{"10.255.0.3/32"},
		runtime.occupiedPeerPolicyLocked("agent-2"),
	)
	if err == nil || !strings.Contains(err.Error(), "public_key_already_assigned") {
		t.Fatalf("expected duplicate public key rejection, got %v", err)
	}
}

func TestWireGuardRuntimeCreatesGroupReadableKeys(t *testing.T) {
	runtime := testWireGuardRuntime(t)
	runtime.privateKeyPath = filepath.Join(t.TempDir(), "secrets", "server_private.key")
	runtime.publicKeyPath = filepath.Join(filepath.Dir(runtime.privateKeyPath), "server_public.key")

	privateKey, publicKey := runtime.ensureServerKeys()
	if privateKey == "" || publicKey == "" {
		t.Fatalf("expected generated WireGuard key pair")
	}
	assertFileMode(t, filepath.Dir(runtime.privateKeyPath), 0o750)
	assertFileMode(t, runtime.privateKeyPath, 0o640)
	assertFileMode(t, runtime.publicKeyPath, 0o640)
}

func TestWireGuardRuntimeCreatesGroupReadableConfig(t *testing.T) {
	runtime := testWireGuardRuntime(t)
	runtime.serverPrivate = "test-private-key"
	runtime.commandRunner = func(args []string) (int, string, string) {
		if len(args) >= 3 && filepath.Base(args[0]) == "wg" && args[1] == "show" {
			return 1, "", "missing interface"
		}
		if len(args) >= 5 && filepath.Base(args[0]) == "ip" && args[1] == "link" && args[2] == "show" {
			return 1, "", "missing interface"
		}
		return 0, "", ""
	}

	if err := runtime.ensureListenerLocked(); err != nil {
		t.Fatalf("ensureListenerLocked returned error: %v", err)
	}

	assertFileMode(t, runtime.configRoot, 0o750)
	assertFileMode(t, filepath.Join(runtime.configRoot, defaultWireGuardConfigName+".conf"), 0o640)
}

func TestWireGuardRuntimeInstallsDefaultDenyFirewallChains(t *testing.T) {
	runtime := testWireGuardRuntime(t)
	var calls [][]string
	runtime.commandRunner = func(args []string) (int, string, string) {
		copied := append([]string{}, args...)
		calls = append(calls, copied)
		if len(args) >= 3 && args[1] == "-C" {
			return 1, "", "missing"
		}
		return 0, "", ""
	}

	if err := runtime.ensureLinuxFirewallLocked(); err != nil {
		t.Fatalf("ensureLinuxFirewallLocked returned error: %v", err)
	}

	for _, expected := range [][]string{
		{"iptables", "-F", wireGuardInputChain},
		{"iptables", "-A", wireGuardInputChain, "-m", "conntrack", "--ctstate", "INVALID", "-m", "comment", "--comment", "borealis wg drop invalid ingress", "-j", "DROP"},
		{"iptables", "-A", wireGuardInputChain, "-s", "10.255.0.0/16", "-m", "comment", "--comment", "borealis deny agent host ingress", "-j", "DROP"},
		{"iptables", "-A", wireGuardForwardChain, "-m", "conntrack", "--ctstate", "INVALID", "-m", "comment", "--comment", "borealis wg drop invalid forward", "-j", "DROP"},
		{"iptables", "-A", wireGuardForwardChain, "-i", defaultWireGuardInterface, "-o", defaultWireGuardInterface, "-m", "comment", "--comment", "borealis deny agent lateral wg", "-j", "DROP"},
		{"iptables", "-A", wireGuardForwardChain, "-s", "10.255.0.0/16", "-m", "comment", "--comment", "borealis deny agent forwarding", "-j", "DROP"},
		{"iptables", "-I", "INPUT", "1", "-i", defaultWireGuardInterface, "-j", wireGuardInputChain},
		{"iptables", "-I", "FORWARD", "1", "-i", defaultWireGuardInterface, "-j", wireGuardForwardChain},
	} {
		if !containsCommandSuffix(calls, expected) {
			t.Fatalf("missing firewall command %v in %#v", expected, calls)
		}
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %o, want %o", path, got, want)
	}
}

func containsCommandSuffix(calls [][]string, expected []string) bool {
	for _, call := range calls {
		if len(call) != len(expected) {
			continue
		}
		matched := true
		for index := range expected {
			actual := call[index]
			if index == 0 {
				actual = filepath.Base(actual)
			}
			if actual != expected[index] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
