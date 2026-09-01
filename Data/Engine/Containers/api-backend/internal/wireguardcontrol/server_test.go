package wireguardcontrol

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testWireGuardKey = "AbCdEfGhIjKlMnOpQrStUvWxYz1234567890+/ABC="

func testConfig(t *testing.T) Config {
	t.Helper()
	serviceRoot := filepath.Join(t.TempDir(), "wireguard")
	for _, directory := range []string{"config", "logs", "run", "secrets"} {
		if err := os.MkdirAll(filepath.Join(serviceRoot, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(serviceRoot, "config", "borealis-wg.conf")
	keyPath := filepath.Join(serviceRoot, "secrets", "server_private.key")
	publicKeyPath := filepath.Join(serviceRoot, "secrets", "server_public.key")
	privateKey, publicKey, err := generateServerKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("[Interface]\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(privateKey+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicKeyPath, []byte(publicKey+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	return Config{
		ServiceRoot:   serviceRoot,
		SocketPath:    filepath.Join(serviceRoot, "run", "control.sock"),
		LogDirectory:  filepath.Join(serviceRoot, "logs"),
		EnginePrefix:  "10.255.0.1/32",
		PeerNetwork:   "10.255.0.0/16",
		Interface:     "borealis-wg",
		ListenPort:    30000,
		MTU:           1420,
		CommandRunner: func(context.Context, []string, int, Config) Result { return Result{} },
	}
}

func TestValidateCommandAllowsOnlyExpectedRuntimeCommands(t *testing.T) {
	cfg := testConfig(t)
	keyPath := filepath.Join(cfg.ServiceRoot, "secrets", "server_private.key")
	configPath := filepath.Join(cfg.ServiceRoot, "config", "borealis-wg.conf")
	allowed := [][]string{
		{"wg", "show", "borealis-wg"},
		{"wg", "show", "borealis-wg", "peers"},
		{"wg", "show", "borealis-wg", "latest-handshakes"},
		{"wg", "set", "borealis-wg", "listen-port", "30000", "private-key", keyPath},
		{"wg", "set", "borealis-wg", "peer", testWireGuardKey, "allowed-ips", "10.255.0.2/32"},
		{"wg", "set", "borealis-wg", "peer", testWireGuardKey, "remove"},
		{"wg-quick", "up", configPath},
		{"ip", "address", "replace", "10.255.0.1/32", "dev", "borealis-wg"},
		{"ip", "route", "replace", "10.255.0.0/16", "dev", "borealis-wg"},
		{"ip", "link", "set", "up", "dev", "borealis-wg"},
		{"ip", "link", "show", "dev", "borealis-wg"},
		{"iptables", "-N", "BOREALIS-WG-INPUT"},
		{"iptables", "-F", "BOREALIS-WG-FWD"},
		{"iptables", "-A", "BOREALIS-WG-INPUT", "-m", "conntrack", "--ctstate", "INVALID", "-m", "comment", "--comment", "borealis wg drop invalid ingress", "-j", "DROP"},
		{"iptables", "-A", "BOREALIS-WG-INPUT", "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-m", "comment", "--comment", "borealis wg established return", "-j", "ACCEPT"},
		{"iptables", "-A", "BOREALIS-WG-INPUT", "-s", "10.255.0.0/16", "-m", "comment", "--comment", "borealis deny agent host ingress", "-j", "DROP"},
		{"iptables", "-A", "BOREALIS-WG-FWD", "-m", "conntrack", "--ctstate", "INVALID", "-m", "comment", "--comment", "borealis wg drop invalid forward", "-j", "DROP"},
		{"iptables", "-A", "BOREALIS-WG-FWD", "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-m", "comment", "--comment", "borealis wg established forward", "-j", "ACCEPT"},
		{"iptables", "-A", "BOREALIS-WG-FWD", "-i", "borealis-wg", "-o", "borealis-wg", "-m", "comment", "--comment", "borealis deny agent lateral wg", "-j", "DROP"},
		{"iptables", "-A", "BOREALIS-WG-FWD", "-s", "10.255.0.0/16", "-m", "comment", "--comment", "borealis deny agent forwarding", "-j", "DROP"},
		{"iptables", "-C", "INPUT", "-i", "borealis-wg", "-j", "BOREALIS-WG-INPUT"},
		{"iptables", "-I", "INPUT", "1", "-i", "borealis-wg", "-j", "BOREALIS-WG-INPUT"},
	}
	for _, command := range allowed {
		if err := ValidateCommand(command, cfg); err != nil {
			t.Errorf("command rejected: %q: %v", command, err)
		}
	}
}

func TestValidateCommandRejectsBroadPrivilegedCommands(t *testing.T) {
	cfg := testConfig(t)
	rejected := [][]string{
		{"firewall-cmd", "--add-port=30000/udp"},
		{"iptables", "-A", "BOREALIS-WG-INPUT", "-j", "ACCEPT"},
		{"wg", "set", "borealis-wg", "peer", testWireGuardKey, "allowed-ips", "10.255.0.0/16"},
		{"wg", "set", "borealis-wg", "peer", testWireGuardKey, "allowed-ips", "10.255.0.1/32"},
		{"wg-quick", "up", filepath.Join(t.TempDir(), "attacker.conf")},
		{"ip", "route", "add", "0.0.0.0/0", "dev", "borealis-wg"},
		{"ip", "route", "replace", "0.0.0.0/0", "dev", "borealis-wg"},
		{"iptables", "-A", "BOREALIS-WG-INPUT", "-s", "0.0.0.0/0", "-m", "comment", "--comment", "borealis deny agent host ingress", "-j", "DROP"},
	}
	for _, command := range rejected {
		if err := ValidateCommand(command, cfg); err == nil {
			t.Errorf("broad command accepted: %q", command)
		}
	}
}

func TestValidateCommandRejectsSymlinkEscape(t *testing.T) {
	cfg := testConfig(t)
	outside := filepath.Join(t.TempDir(), "attacker.key")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(cfg.ServiceRoot, "secrets", "linked.key")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	command := []string{"wg", "set", "borealis-wg", "listen-port", "30000", "private-key", link}
	if err := ValidateCommand(command, cfg); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestConfiguredNetworksFallBackFromUnsafeValues(t *testing.T) {
	cfg := testConfig(t)
	cfg.EnginePrefix = "8.8.8.8/32"
	cfg.PeerNetwork = "0.0.0.0/0"
	if !isEnginePrefix(defaultEnginePrefix, cfg) || !isPeerNetwork(defaultPeerNetwork, cfg) {
		t.Fatal("unsafe network values did not fall back to Borealis defaults")
	}
}

func TestHandleRequestSupportsPingAndRejectsUnsupportedCommands(t *testing.T) {
	cfg := testConfig(t)
	ping := HandleRequest(context.Background(), []byte(`{"command":"ping"}`), cfg)
	var result Result
	if err := json.Unmarshal(ping, &result); err != nil {
		t.Fatal(err)
	}
	if result.ReturnCode != 0 || result.Stdout != "pong" {
		t.Fatalf("unexpected ping result %#v", result)
	}
	unsupported := HandleRequest(context.Background(), []byte(`{"command":"shell"}`), cfg)
	if err := json.Unmarshal(unsupported, &result); err != nil {
		t.Fatal(err)
	}
	if result.ReturnCode != 2 || !strings.Contains(result.Stderr, "unsupported command") {
		t.Fatalf("unexpected unsupported result %#v", result)
	}
}

func TestBootstrapRuntimeCreatesIdentityAndCompleteListener(t *testing.T) {
	cfg := testConfig(t)
	for _, path := range []string{
		filepath.Join(cfg.ServiceRoot, "config", "borealis-wg.conf"),
		filepath.Join(cfg.ServiceRoot, "secrets", "server_private.key"),
		filepath.Join(cfg.ServiceRoot, "secrets", "server_public.key"),
	} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	commands := []string{}
	cfg.CommandRunner = func(_ context.Context, args []string, _ int, _ Config) Result {
		commands = append(commands, strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "iptables" && (args[1] == "-N" || args[1] == "-C") {
			return Result{ReturnCode: 1, Stderr: "not present"}
		}
		return Result{}
	}

	if err := bootstrapRuntime(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(cfg.ServiceRoot, "secrets", "server_private.key"),
		filepath.Join(cfg.ServiceRoot, "secrets", "server_public.key"),
		filepath.Join(cfg.ServiceRoot, "config", "borealis-wg.conf"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("%s mode is %o, want 640", path, info.Mode().Perm())
		}
	}
	configRaw, err := os.ReadFile(filepath.Join(cfg.ServiceRoot, "config", "borealis-wg.conf"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(configRaw)
	for _, marker := range []string{"ListenPort = 30000", "Address = 10.255.0.1/32", "MTU = 1420"} {
		if !strings.Contains(config, marker) {
			t.Fatalf("base config missing %q: %s", marker, config)
		}
	}
	required := []string{
		"wg-quick up " + filepath.Join(cfg.ServiceRoot, "config", "borealis-wg.conf"),
		"ip address replace 10.255.0.1/32 dev borealis-wg",
		"ip route replace 10.255.0.0/16 dev borealis-wg",
		"ip link set up dev borealis-wg",
		"iptables -F BOREALIS-WG-INPUT",
		"iptables -F BOREALIS-WG-FWD",
		"iptables -I INPUT 1 -i borealis-wg -j BOREALIS-WG-INPUT",
		"iptables -I FORWARD 1 -i borealis-wg -j BOREALIS-WG-FWD",
	}
	for _, expected := range required {
		found := false
		for _, command := range commands {
			if command == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("bootstrap command %q missing from %#v", expected, commands)
		}
	}
}

func TestReconcileRuntimeLeavesCleanStandbyUnconfigured(t *testing.T) {
	cfg := testConfig(t)
	cfg.EdgeVIP = "10.10.10.10"
	cfg.OwnsEdgeVIP = func(string) bool { return false }
	commands := []string{}
	cfg.CommandRunner = func(_ context.Context, args []string, _ int, _ Config) Result {
		commands = append(commands, strings.Join(args, " "))
		return Result{ReturnCode: 1, Stderr: "interface missing"}
	}

	if err := reconcileRuntime(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || commands[0] != "wg show borealis-wg" {
		t.Fatalf("clean standby mutated runtime: %#v", commands)
	}
}

func TestWithdrawalRequestSuppressesOwnershipReactivation(t *testing.T) {
	cfg := testConfig(t)
	commands := []string{}
	cfg.CommandRunner = func(_ context.Context, args []string, _ int, _ Config) Result {
		commands = append(commands, strings.Join(args, " "))
		return Result{}
	}

	if result := requestWithdrawal(context.Background(), cfg); result.ReturnCode != 0 {
		t.Fatalf("withdrawal request failed: %#v", result)
	}
	if !withdrawalRequested(cfg) {
		t.Fatal("withdrawal request marker was not retained through preStop window")
	}
	commands = nil
	if err := reconcileRuntime(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 0 {
		t.Fatalf("withdrawn runtime reactivated: %#v", commands)
	}
	if err := clearWithdrawalRequest(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestStandbyAcceptsValidatedDesiredStateWithoutActivatingInterface(t *testing.T) {
	cfg := testConfig(t)
	cfg.EdgeVIP = "10.10.10.10"
	cfg.OwnsEdgeVIP = func(string) bool { return false }
	result := runCommand(context.Background(), []string{"wg", "set", "borealis-wg", "peer", testWireGuardKey, "allowed-ips", "10.255.0.2/32"}, 5, cfg)
	if result.ReturnCode != 0 || !strings.Contains(result.Stdout, "standby") {
		t.Fatalf("standby desired state rejected: %#v", result)
	}
	if !suppressStandbyMutation([]string{"wg-quick", "up", filepath.Join(cfg.ServiceRoot, "config", "borealis-wg.conf")}, cfg) {
		t.Fatal("standby interface activation was not suppressed")
	}
	if suppressStandbyMutation([]string{"wg", "show", "borealis-wg"}, cfg) || suppressStandbyMutation([]string{"wg-quick", "down", filepath.Join(cfg.ServiceRoot, "config", "borealis-wg.conf")}, cfg) {
		t.Fatal("standby read or withdrawal command was suppressed")
	}
}

func TestEdgeOwnershipUsesInjectedHostAddressCheck(t *testing.T) {
	cfg := testConfig(t)
	cfg.EdgeVIP = "10.10.10.10"
	cfg.OwnsEdgeVIP = func(value string) bool { return value == cfg.EdgeVIP }
	if !edgeVIPOwned(cfg) {
		t.Fatal("local edge VIP was not recognized")
	}
	cfg.OwnsEdgeVIP = func(string) bool { return false }
	if edgeVIPOwned(cfg) {
		t.Fatal("standby node reported edge ownership")
	}
}

func TestServeCreatesPrivateSocketAndAnswersPing(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, cfg)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("control server stopped before creating socket: %v", err)
		default:
		}
		if info, err := os.Stat(cfg.SocketPath); err == nil {
			if mode := info.Mode().Perm(); mode != 0o660 {
				t.Fatalf("socket mode is %o, want 660", mode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control socket was not created")
		}
		time.Sleep(10 * time.Millisecond)
	}
	connection, err := net.DialTimeout("unix", cfg.SocketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("{\"command\":\"ping\"}\n")); err != nil {
		t.Fatal(err)
	}
	response, err := bufio.NewReader(connection).ReadBytes('\n')
	_ = connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	var result Result
	if err := json.Unmarshal(response, &result); err != nil {
		t.Fatal(err)
	}
	if result.ReturnCode != 0 || result.Stdout != "pong" {
		t.Fatalf("unexpected ping result %#v", result)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control server did not stop after cancellation")
	}
	if _, err := os.Stat(cfg.SocketPath); !os.IsNotExist(err) {
		t.Fatalf("control socket remained after shutdown: %v", err)
	}
}
