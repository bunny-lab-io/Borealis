package wireguardcontrol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultProjectRoot  = "/opt/Borealis"
	defaultEnginePrefix = "10.255.0.1/32"
	defaultPeerNetwork  = "10.255.0.0/16"
	maxRequestBytes     = 1 << 20
)

var (
	interfacePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)
	keyPattern       = regexp.MustCompile(`^[A-Za-z0-9+/]{40,60}={0,2}$`)
	allowedCommands  = map[string]bool{"wg": true, "wg-quick": true, "ip": true, "iptables": true}
	wireGuardChains  = map[string]bool{"BOREALIS-WG-INPUT": true, "BOREALIS-WG-FWD": true}
	parentChains     = map[string]bool{"INPUT": true, "FORWARD": true}
)

type Config struct {
	ProjectRoot  string
	ServiceRoot  string
	SocketPath   string
	LogDirectory string
	EnginePrefix string
	PeerNetwork  string
	Interface    string
	EdgeVIP      string
	OwnsEdgeVIP  func(string) bool
}

type Result struct {
	ReturnCode int    `json:"returncode"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
}

func ConfigFromEnv() Config {
	projectRoot := firstText(os.Getenv("BOREALIS_PROJECT_ROOT"), defaultProjectRoot)
	serviceRoot := filepath.Join(projectRoot, "Engine", "Services", "wireguard-tunnel")
	return Config{
		ProjectRoot:  projectRoot,
		ServiceRoot:  serviceRoot,
		SocketPath:   firstText(os.Getenv("BOREALIS_WIREGUARD_CONTROL_SOCKET"), filepath.Join(serviceRoot, "run", "control.sock")),
		LogDirectory: filepath.Join(serviceRoot, "logs"),
		EnginePrefix: firstText(os.Getenv("BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP"), defaultEnginePrefix),
		PeerNetwork:  firstText(os.Getenv("BOREALIS_WIREGUARD_PEER_NETWORK"), defaultPeerNetwork),
		Interface:    firstText(os.Getenv("BOREALIS_WIREGUARD_INTERFACE"), "borealis-wg"),
		EdgeVIP:      strings.TrimSpace(os.Getenv("BOREALIS_CLUSTER_EDGE_VIP")),
	}
}

func ValidateCommand(args []string, cfg Config) error {
	name := ""
	if len(args) > 0 {
		name = filepath.Base(args[0])
	}
	switch name {
	case "wg":
		return validateWG(args, cfg)
	case "wg-quick":
		return validateWGQuick(args, cfg)
	case "ip":
		return validateIP(args, cfg)
	case "iptables":
		return validateIPTables(args, cfg)
	default:
		return fmt.Errorf("command_not_allowed:%s", name)
	}
}

func HandleRequest(ctx context.Context, raw []byte, cfg Config) []byte {
	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &payload); err != nil {
		return encodeResult(Result{ReturnCode: 2, Stderr: "invalid json: " + err.Error()})
	}
	command := cleanText(payload["command"])
	if command == "" {
		command = "run"
	}
	var result Result
	switch command {
	case "run":
		result = runCommand(ctx, stringSlice(payload["args"]), integer(payload["timeout"], 30), cfg)
	case "ping":
		result = Result{ReturnCode: 0, Stdout: "pong"}
	case "status", "reconcile":
		result = status(ctx, cfg)
	case "withdraw":
		result = withdraw(ctx, cfg)
	default:
		result = Result{ReturnCode: 2, Stderr: "unsupported command: " + command}
	}
	return encodeResult(result)
}

func Serve(ctx context.Context, cfg Config) error {
	if cfg.ServiceRoot == "" {
		cfg = ConfigFromEnv()
	}
	if !isInterface(firstText(cfg.Interface, "borealis-wg")) {
		return errors.New("BOREALIS_WIREGUARD_INTERFACE is invalid")
	}
	if cfg.EdgeVIP != "" {
		address := net.ParseIP(cfg.EdgeVIP)
		if address == nil || address.To4() == nil || !address.IsPrivate() {
			return errors.New("BOREALIS_CLUSTER_EDGE_VIP must be private IPv4")
		}
	}
	runDirectory := filepath.Dir(cfg.SocketPath)
	if err := os.MkdirAll(runDirectory, 0o775); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.LogDirectory, 0o775); err != nil {
		return err
	}
	if err := os.Remove(cfg.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(cfg.SocketPath)
	if err := os.Chmod(cfg.SocketPath, 0o660); err != nil {
		return err
	}
	logMessage(cfg, "WireGuard control socket listening at "+cfg.SocketPath)
	go reconcileEdgeOwnership(ctx, cfg)
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				logMessage(cfg, "WireGuard control socket stopped")
				return nil
			}
			return err
		}
		handleConnection(ctx, connection, cfg)
	}
}

func handleConnection(ctx context.Context, connection net.Conn, cfg Config) {
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(io.LimitReader(connection, maxRequestBytes))
	raw, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		_, _ = connection.Write(encodeResult(Result{ReturnCode: 2, Stderr: err.Error()}))
		return
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	_, _ = connection.Write(HandleRequest(ctx, raw, cfg))
}

func validateWG(args []string, cfg Config) error {
	if (len(args) == 3 || len(args) == 4) && args[1] == "show" && isInterface(args[2]) {
		if len(args) == 3 || args[3] == "peers" || args[3] == "latest-handshakes" {
			return nil
		}
	}
	if len(args) == 7 && args[1] == "set" && isInterface(args[2]) && args[3] == "peer" && keyPattern.MatchString(args[4]) && args[5] == "allowed-ips" && isPeerHost(args[6], cfg) {
		return nil
	}
	if len(args) == 6 && args[1] == "set" && isInterface(args[2]) && args[3] == "peer" && keyPattern.MatchString(args[4]) && args[5] == "remove" {
		return nil
	}
	if len(args) == 7 && args[1] == "set" && isInterface(args[2]) && args[3] == "listen-port" && isPort(args[4]) && args[5] == "private-key" && isPathUnder(args[6], filepath.Join(cfg.ServiceRoot, "secrets"), ".key") {
		return nil
	}
	return errors.New("command_shape_not_allowed:wg")
}

func validateWGQuick(args []string, cfg Config) error {
	if len(args) == 3 && (args[1] == "up" || args[1] == "down") && isPathUnder(args[2], filepath.Join(cfg.ServiceRoot, "config"), ".conf") {
		return nil
	}
	return errors.New("command_shape_not_allowed:wg-quick")
}

func withdraw(ctx context.Context, cfg Config) Result {
	configPath := filepath.Join(cfg.ServiceRoot, "config", "borealis-wg.conf")
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		return Result{}
	} else if err != nil {
		return Result{ReturnCode: 1, Stderr: err.Error()}
	}
	return runCommand(ctx, []string{"wg-quick", "down", configPath}, 20, cfg)
}

func validateIP(args []string, cfg Config) error {
	if len(args) == 6 && equalStrings(args[1:3], []string{"address", "replace"}) && isEnginePrefix(args[3], cfg) && args[4] == "dev" && isInterface(args[5]) {
		return nil
	}
	if len(args) == 6 && equalStrings(args[1:3], []string{"route", "replace"}) && isPeerNetwork(args[3], cfg) && args[4] == "dev" && isInterface(args[5]) {
		return nil
	}
	if len(args) == 6 && equalStrings(args[1:4], []string{"link", "set", "up"}) && args[4] == "dev" && isInterface(args[5]) {
		return nil
	}
	if len(args) == 5 && equalStrings(args[1:4], []string{"link", "show", "dev"}) && isInterface(args[4]) {
		return nil
	}
	return errors.New("command_shape_not_allowed:ip")
}

func validateIPTables(args []string, cfg Config) error {
	if len(args) == 3 && (args[1] == "-N" || args[1] == "-F") && wireGuardChains[args[2]] {
		return nil
	}
	if len(args) >= 4 && args[1] == "-A" {
		chain := args[2]
		tail := args[3:]
		if chain == "BOREALIS-WG-INPUT" {
			if equalStrings(tail, []string{"-m", "conntrack", "--ctstate", "INVALID", "-m", "comment", "--comment", "borealis wg drop invalid ingress", "-j", "DROP"}) ||
				equalStrings(tail, []string{"-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-m", "comment", "--comment", "borealis wg established return", "-j", "ACCEPT"}) {
				return nil
			}
			if len(tail) == 8 && tail[0] == "-s" && isPeerNetwork(tail[1], cfg) && equalStrings(tail[2:], []string{"-m", "comment", "--comment", "borealis deny agent host ingress", "-j", "DROP"}) {
				return nil
			}
		}
		if chain == "BOREALIS-WG-FWD" {
			if equalStrings(tail, []string{"-m", "conntrack", "--ctstate", "INVALID", "-m", "comment", "--comment", "borealis wg drop invalid forward", "-j", "DROP"}) ||
				equalStrings(tail, []string{"-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-m", "comment", "--comment", "borealis wg established forward", "-j", "ACCEPT"}) {
				return nil
			}
			if len(tail) == 10 && tail[0] == "-i" && isInterface(tail[1]) && tail[2] == "-o" && tail[1] == tail[3] && equalStrings(tail[4:], []string{"-m", "comment", "--comment", "borealis deny agent lateral wg", "-j", "DROP"}) {
				return nil
			}
			if len(tail) == 8 && tail[0] == "-s" && isPeerNetwork(tail[1], cfg) && equalStrings(tail[2:], []string{"-m", "comment", "--comment", "borealis deny agent forwarding", "-j", "DROP"}) {
				return nil
			}
		}
	}
	if len(args) == 7 && args[1] == "-C" && parentChains[args[2]] && args[3] == "-i" && isInterface(args[4]) && args[5] == "-j" && wireGuardChains[args[6]] {
		return nil
	}
	if len(args) == 8 && args[1] == "-I" && parentChains[args[2]] && args[3] == "1" && args[4] == "-i" && isInterface(args[5]) && args[6] == "-j" && wireGuardChains[args[7]] {
		return nil
	}
	return errors.New("command_shape_not_allowed:iptables")
}

func runCommand(ctx context.Context, args []string, timeoutSeconds int, cfg Config) Result {
	if len(args) == 0 {
		return Result{ReturnCode: 2, Stderr: "missing command"}
	}
	if err := ValidateCommand(args, cfg); err != nil {
		return Result{ReturnCode: 126, Stderr: err.Error()}
	}
	if suppressStandbyMutation(args, cfg) {
		return Result{Stdout: "standby: edge VIP is not local"}
	}
	executable, err := resolveExecutable(args[0])
	if err != nil {
		return Result{ReturnCode: 126, Stderr: err.Error()}
	}
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	command := exec.CommandContext(runCtx, executable, args[1:]...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return Result{ReturnCode: 124, Stdout: strings.TrimSpace(stdout.String()), Stderr: "command timed out"}
	}
	if err == nil {
		return Result{ReturnCode: 0, Stdout: strings.TrimSpace(stdout.String()), Stderr: strings.TrimSpace(stderr.String())}
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return Result{ReturnCode: exitError.ExitCode(), Stdout: strings.TrimSpace(stdout.String()), Stderr: strings.TrimSpace(stderr.String())}
	}
	return Result{ReturnCode: 1, Stdout: strings.TrimSpace(stdout.String()), Stderr: err.Error()}
}

func status(ctx context.Context, cfg Config) Result {
	wg, wgErr := exec.LookPath("wg")
	_, ipErr := exec.LookPath("ip")
	if wgErr != nil || ipErr != nil {
		return Result{ReturnCode: 1, Stderr: "wg or ip missing"}
	}
	command := exec.CommandContext(ctx, wg, "show", firstText(cfg.Interface, "borealis-wg"))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if cfg.EdgeVIP != "" && !edgeVIPOwned(cfg) {
		if err == nil {
			return Result{ReturnCode: 1, Stdout: strings.TrimSpace(stdout.String()), Stderr: "standby node still owns WireGuard interface"}
		}
		return Result{Stdout: "standby", Stderr: strings.TrimSpace(stderr.String())}
	}
	if err == nil {
		return Result{Stdout: strings.TrimSpace(stdout.String()), Stderr: strings.TrimSpace(stderr.String())}
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return Result{ReturnCode: exitError.ExitCode(), Stdout: strings.TrimSpace(stdout.String()), Stderr: strings.TrimSpace(stderr.String())}
	}
	return Result{ReturnCode: 1, Stdout: strings.TrimSpace(stdout.String()), Stderr: err.Error()}
}

func suppressStandbyMutation(args []string, cfg Config) bool {
	if cfg.EdgeVIP == "" || edgeVIPOwned(cfg) || len(args) < 2 {
		return false
	}
	switch filepath.Base(args[0]) {
	case "wg":
		return args[1] == "set"
	case "wg-quick":
		return args[1] == "up"
	case "ip":
		return !(len(args) >= 4 && args[1] == "link" && args[2] == "show")
	case "iptables":
		return true
	default:
		return false
	}
}

func edgeVIPOwned(cfg Config) bool {
	if cfg.EdgeVIP == "" {
		return true
	}
	if cfg.OwnsEdgeVIP != nil {
		return cfg.OwnsEdgeVIP(cfg.EdgeVIP)
	}
	target := net.ParseIP(cfg.EdgeVIP)
	if target == nil {
		return false
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range interfaces {
		addresses, addressErr := iface.Addrs()
		if addressErr != nil {
			continue
		}
		for _, raw := range addresses {
			ip, _, parseErr := net.ParseCIDR(raw.String())
			if parseErr == nil && ip.Equal(target) {
				return true
			}
		}
	}
	return false
}

func reconcileEdgeOwnership(ctx context.Context, cfg Config) {
	if cfg.EdgeVIP == "" {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if !edgeVIPOwned(cfg) && wireGuardInterfacePresent(ctx, firstText(cfg.Interface, "borealis-wg")) {
			result := withdraw(ctx, cfg)
			if result.ReturnCode != 0 {
				logMessage(cfg, "WireGuard standby withdrawal pending: "+firstText(result.Stderr, result.Stdout, "unknown"))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func wireGuardInterfacePresent(ctx context.Context, interfaceName string) bool {
	wg, err := exec.LookPath("wg")
	if err != nil {
		return false
	}
	return exec.CommandContext(ctx, wg, "show", interfaceName).Run() == nil
}

func resolveExecutable(raw string) (string, error) {
	name := filepath.Base(raw)
	if !allowedCommands[name] {
		return "", fmt.Errorf("command_not_allowed:%s", name)
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("command_not_found:%s", name)
	}
	return resolved, nil
}

func configuredNetworks(cfg Config) (netip.Prefix, netip.Prefix) {
	engine, engineErr := netip.ParsePrefix(strings.TrimSpace(cfg.EnginePrefix))
	peer, peerErr := netip.ParsePrefix(strings.TrimSpace(cfg.PeerNetwork))
	if engineErr == nil {
		engine = engine.Masked()
	}
	if peerErr == nil {
		peer = peer.Masked()
	}
	if engineErr != nil || peerErr != nil || !engine.Addr().Is4() || !peer.Addr().Is4() || engine.Bits() != 32 || !engine.Addr().IsPrivate() || !peer.Addr().IsPrivate() || peer.Bits() < 16 || peer.Bits() > 30 || !peer.Contains(engine.Addr()) {
		engine, _ = netip.ParsePrefix(defaultEnginePrefix)
		peer, _ = netip.ParsePrefix(defaultPeerNetwork)
	}
	return engine, peer
}

func isEnginePrefix(value string, cfg Config) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	engine, _ := configuredNetworks(cfg)
	return err == nil && prefix.Masked() == engine
}

func isPeerNetwork(value string, cfg Config) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	_, peer := configuredNetworks(cfg)
	return err == nil && prefix.Masked() == peer
}

func isPeerHost(value string, cfg Config) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil || prefix.Bits() != 32 || !prefix.Addr().Is4() {
		return false
	}
	engine, peer := configuredNetworks(cfg)
	return peer.Contains(prefix.Addr()) && prefix.Addr() != engine.Addr()
}

func isPathUnder(path string, root string, suffix string) bool {
	resolvedPath, err := canonicalPath(path)
	if err != nil {
		return false
	}
	resolvedRoot, err := canonicalPath(root)
	if err != nil {
		return false
	}
	if suffix != "" && filepath.Ext(resolvedPath) != suffix {
		return false
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedPath)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	parent, parentErr := filepath.EvalSymlinks(filepath.Dir(absolute))
	if parentErr != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func isInterface(value string) bool {
	return interfacePattern.MatchString(value)
}

func isPort(value string) bool {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	return err == nil && port >= 1 && port <= 65535
}

func encodeResult(result Result) []byte {
	encoded, _ := json.Marshal(result)
	return append(encoded, '\n')
}

func stringSlice(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, fmt.Sprint(item))
	}
	return out
}

func integer(value any, fallback int) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := strconv.Atoi(typed.String())
		if err == nil {
			return parsed
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func cleanText(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func logMessage(cfg Config, message string) {
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02T15:04:05-0700"), message)
	if err := os.MkdirAll(cfg.LogDirectory, 0o775); err == nil {
		if handle, openErr := os.OpenFile(filepath.Join(cfg.LogDirectory, "control.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o664); openErr == nil {
			_, _ = handle.WriteString(line)
			_ = handle.Close()
		}
	}
	_, _ = os.Stdout.WriteString(line)
}
