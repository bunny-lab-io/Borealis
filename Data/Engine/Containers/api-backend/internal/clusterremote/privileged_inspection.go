package clusterremote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrPrivilegeInspection = errors.New("SSH privileged inspection unavailable; remote diagnostics withheld")
	machineIDPattern       = regexp.MustCompile(`^[0-9a-f]{32}$`)
	publicUUIDPattern      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	interfacePattern       = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`)
)

type IPv4Address struct {
	Interface string
	Prefix    netip.Prefix
	Up        bool
	Dynamic   bool
	Permanent bool
}

type IPv4Route struct {
	Destination netip.Prefix
	Interface   string
	Gateway     netip.Addr
	Scope       string
	Type        string
	LinkDown    bool
	Protocol    string
}

// PrivilegedFacts contains public inventory only. Path classification reads
// directory metadata, never K3s, node-manager or application secret files.
// Existing state must be reconciled against cluster authority by the caller.
type PrivilegedFacts struct {
	MachineID      string
	BootID         string
	BorealisPath   string
	BorealisConfig string
	K3sConfig      string
	K3sData        string
	K3sBinary      string
	K3sLoad        string
	K3sActive      string
	K3sAgentLoad   string
	K3sAgentActive string
	KubeSystemUID  string
	NodesState     string
	Nodes          []InspectedKubernetesNode
	Addresses      []IPv4Address
	Routes         []IPv4Route
}

type InspectedKubernetesNode struct {
	Name string
	UID  string
}

// NoExistingInstallation is limited negative inventory. It does not authorize
// takeover, prove persistent static configuration, sizing, peer reachability,
// release compatibility or cluster membership eligibility.
func (facts PrivilegedFacts) NoExistingInstallation() bool {
	return machineIDPattern.MatchString(facts.MachineID) && facts.MachineID != strings.Repeat("0", 32) && validPublicUUID(facts.BootID) &&
		facts.BorealisPath == "absent" && facts.BorealisConfig == "absent" && facts.K3sConfig == "absent" && facts.K3sData == "absent" && facts.K3sBinary == "absent" &&
		facts.K3sLoad == "not-found" && facts.K3sActive == "inactive" && facts.K3sAgentLoad == "not-found" && facts.K3sAgentActive == "inactive" &&
		facts.KubeSystemUID == "not-present" && facts.NodesState == "not-present" && len(facts.Nodes) == 0
}

// ConnectedManagementNetwork requires one up, non-dynamic, permanent private
// management address and its direct link route containing VIP and peers.
// This is observed routing evidence, not proof of persistent host config or ARP.
func (facts PrivilegedFacts) ConnectedManagementNetwork(management string, peers []string) (netip.Prefix, error) {
	invalid := errors.New("SSH target lacks unique permanent management address and connected peer network")
	ip, err := netip.ParseAddr(management)
	if err != nil || !ip.Is4() || !ip.IsPrivate() || ip.String() != management {
		return netip.Prefix{}, invalid
	}
	var selected *IPv4Address
	for index := range facts.Addresses {
		address := &facts.Addresses[index]
		if address.Prefix.Addr() == ip {
			if selected != nil {
				return netip.Prefix{}, invalid
			}
			selected = address
		}
	}
	if selected == nil || !selected.Up || selected.Dynamic || !selected.Permanent || selected.Prefix.Bits() > 30 {
		return netip.Prefix{}, invalid
	}
	prefix := selected.Prefix.Masked()
	if !UsableManagementAddress(prefix, ip) {
		return netip.Prefix{}, invalid
	}
	for _, text := range peers {
		peer, err := netip.ParseAddr(text)
		if err != nil || peer.String() != text || !UsableManagementAddress(prefix, peer) {
			return netip.Prefix{}, invalid
		}
	}
	for _, route := range facts.Routes {
		if route.Destination == prefix && route.Interface == selected.Interface && !route.Gateway.IsValid() && route.Scope == "link" && route.Protocol == "kernel" &&
			(route.Type == "" || route.Type == "unicast") && !route.LinkDown {
			return prefix, nil
		}
	}
	return netip.Prefix{}, invalid
}

// UsableManagementAddress excludes subnet network/broadcast addresses. The
// supported management network is private IPv4 with room for VIP and peers.
func UsableManagementAddress(prefix netip.Prefix, address netip.Addr) bool {
	if !address.Is4() || !address.IsPrivate() {
		return false
	}
	minimumBits := 16
	switch address.As4()[0] {
	case 10:
		minimumBits = 8
	case 172:
		minimumBits = 12
	}
	return prefix.IsValid() && prefix == prefix.Masked() && prefix.Addr().Is4() && prefix.Bits() <= 30 &&
		prefix.Bits() >= minimumBits && prefix.Contains(address) && address != prefix.Addr() && prefix.Contains(address.Next())
}

const inspectPathScript = `inspect_path() {
  probe=/
  remaining=$1
  while [ -n "$remaining" ]; do
    component=${remaining%%/*}
    if [ "$component" = "$remaining" ]; then remaining=; else remaining=${remaining#*/}; fi
    kind=$(find -P "$probe" -mindepth 1 -maxdepth 1 -name "$component" -printf '%y' 2>/dev/null) || { printf unknown; return; }
    case "$kind" in
      '') printf absent; return ;;
      l) printf symlink; return ;;
      d) if [ -z "$remaining" ]; then printf directory; return; fi ;;
      f) if [ -z "$remaining" ]; then printf regular; else printf other; fi; return ;;
      *) printf other; return ;;
    esac
    probe=${probe%/}/$component
  done
  printf unknown
}
`

// Fixed root script does not source host configuration or read private tokens.
// Directory walks distinguish a missing component from failed/inaccessible
// metadata, and stop at symlinks or unexpected intermediate file types.
const privilegedInspectionScript = `set -eu
test "$(id -u)" = 0
printf 'protocol=1\nuid=0\n'
machine_id=$(cat /etc/machine-id 2>/dev/null) || machine_id=unknown
boot_id=$(cat /proc/sys/kernel/random/boot_id 2>/dev/null) || boot_id=unknown
printf 'machine_id=%s\nboot_id=%s\n' "$machine_id" "$boot_id"
` + inspectPathScript + `printf 'borealis_path='; inspect_path opt/Borealis; printf '\n'
printf 'borealis_config='; inspect_path etc/borealis; printf '\n'
printf 'k3s_config='; inspect_path etc/rancher/k3s; printf '\n'
printf 'k3s_data='; inspect_path var/lib/rancher/k3s; printf '\n'
k3s_binary=$(inspect_path usr/local/bin/k3s)
printf 'k3s_binary=%s\n' "$k3s_binary"
service_property() {
  value=$(systemctl show "$1" --property="$2" --value 2>/dev/null) || value=unknown
  case "$2:$value" in
    LoadState:loaded|LoadState:not-found|LoadState:masked|LoadState:error|LoadState:bad-setting|ActiveState:active|ActiveState:inactive|ActiveState:failed|ActiveState:activating|ActiveState:deactivating) printf '%s' "$value" ;;
    *) printf unknown ;;
  esac
}
k3s_load=$(service_property k3s.service LoadState)
k3s_active=$(service_property k3s.service ActiveState)
agent_load=$(service_property k3s-agent.service LoadState)
agent_active=$(service_property k3s-agent.service ActiveState)
printf 'k3s_load=%s\nk3s_active=%s\nk3s_agent_load=%s\nk3s_agent_active=%s\n' "$k3s_load" "$k3s_active" "$agent_load" "$agent_active"
printf 'addresses='; ip -j -4 address show
printf 'routes='; ip -j -4 route show table main
kube_uid=unknown
nodes=unknown
if [ "$k3s_binary" = absent ] && [ "$k3s_load" = not-found ] && [ "$agent_load" = not-found ]; then
  kube_uid=not-present
  nodes=not-present
elif [ "$k3s_binary" = regular ] && [ "$k3s_active" = active ]; then
  kube_uid=$(/usr/local/bin/k3s kubectl --request-timeout=4s get namespace kube-system -o 'jsonpath={.metadata.uid}' 2>/dev/null) || kube_uid=unknown
  nodes=$(/usr/local/bin/k3s kubectl --request-timeout=4s get nodes -o 'jsonpath={range .items[*]}{.metadata.name},{.metadata.uid};{end}' 2>/dev/null) || nodes=unknown
fi
printf 'kube_system_uid=%s\nnodes=%s\n' "$kube_uid" "$nodes"
`

// Both shells use -c with compile-time text. Stdin contains only one bounded
// sudo password line: NOPASSWD/root execution leaves it unread, never shell
// source. Root command has its own timeout even if SSH disconnects.
var privilegedInspectionCommand = buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin", privilegedInspectionScript)

// Parameters are compile-time production constants; tests substitute isolated
// command fixtures to exercise real shell/sudo stdin behavior without privilege.
func buildPrivilegedInspectionCommand(toolPath, script string) string {
	return "PATH=" + shellConstant(toolPath) + " LC_ALL=C /bin/sh -c " + shellConstant(
		"export PATH="+shellConstant(toolPath)+` LC_ALL=C
if [ "$(id -u)" = 0 ]; then
  exec timeout --signal=TERM --kill-after=2s 15s /bin/sh -c `+shellConstant(script)+`
fi
exec sudo -k -S -p '' -- timeout --signal=TERM --kill-after=2s 15s /bin/sh -c `+shellConstant(script))
}

func shellConstant(text string) string { return "'" + strings.ReplaceAll(text, "'", "'\\''") + "'" }

// ValidateSudoPassword preserves secret syntax within sudo's single-line stdin
// contract. Empty material explicitly permits only NOPASSWD/root capability.
func ValidateSudoPassword(value []byte) error {
	if len(value) > MaxPasswordBytes || len(value) > 0 && !validSecret(value, MaxPasswordBytes) || bytes.ContainsAny(value, "\r\n") {
		return ErrInvalidAuth
	}
	return nil
}

// InspectPrivileged supports an empty password for NOPASSWD/root. Sudo stdin is
// line-oriented, so CR/LF/NUL are rejected without changing otherwise meaningful
// password syntax. Caller retains/destroys its own secret; no persistence here.
func (client *Client) InspectPrivileged(ctx context.Context, sudoPassword []byte) (PrivilegedFacts, error) {
	if ValidateSudoPassword(sudoPassword) != nil {
		return PrivilegedFacts{}, ErrInvalidAuth
	}
	input := append(bytes.Clone(sudoPassword), '\n')
	defer clear(input)
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	session, err := client.ssh.NewSession()
	if err != nil {
		if ctx.Err() != nil {
			return PrivilegedFacts{}, ErrCancelled
		}
		return PrivilegedFacts{}, ErrTransport
	}
	defer session.Close()
	output := boundedOutput{close: func() { client.Close() }}
	diagnostics := boundedOutput{close: func() { client.Close() }}
	session.Stdout, session.Stderr = &output, &diagnostics
	stdin, err := session.StdinPipe()
	if err != nil {
		return PrivilegedFacts{}, ErrTransport
	}
	if err = session.Start(privilegedInspectionCommand); err == nil {
		// Root/NOPASSWD may finish before reading any stdin. Its successful
		// exit and validated UID0 inventory are authoritative even if this
		// bounded password write sees EOF. Session.Stdin's copier otherwise
		// turns that harmless race into a false command failure.
		_, _ = stdin.Write(input)
		_ = stdin.Close()
		err = session.Wait()
	}
	if output.overflow || diagnostics.overflow {
		return PrivilegedFacts{}, ErrOutputLimit
	}
	if ctx.Err() != nil {
		return PrivilegedFacts{}, ErrCancelled
	}
	if err != nil {
		return PrivilegedFacts{}, ErrPrivilegeInspection
	}
	return parsePrivilegedFacts(output.buffer.Bytes())
}

func parsePrivilegedFacts(raw []byte) (PrivilegedFacts, error) {
	invalid := ErrPrivilegeInspection
	if len(raw) == 0 || len(raw) > MaxOutputBytes || !utf8.Valid(raw) {
		return PrivilegedFacts{}, invalid
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		name, value, ok := strings.Cut(line, "=")
		if !ok || value == "" || values[name] != "" {
			return PrivilegedFacts{}, invalid
		}
		switch name {
		case "protocol", "uid", "machine_id", "boot_id", "borealis_path", "borealis_config", "k3s_config", "k3s_data", "k3s_binary", "k3s_load", "k3s_active", "k3s_agent_load", "k3s_agent_active", "addresses", "routes", "kube_system_uid", "nodes":
			values[name] = value
		default:
			return PrivilegedFacts{}, invalid
		}
	}
	if len(values) != 17 || values["protocol"] != "1" || values["uid"] != "0" ||
		(values["machine_id"] != "unknown" && (!machineIDPattern.MatchString(values["machine_id"]) || values["machine_id"] == strings.Repeat("0", 32))) ||
		(values["boot_id"] != "unknown" && !validPublicUUID(values["boot_id"])) {
		return PrivilegedFacts{}, invalid
	}
	for _, key := range []string{"borealis_path", "borealis_config", "k3s_config", "k3s_data", "k3s_binary"} {
		if !oneOf(values[key], "absent", "directory", "regular", "symlink", "other", "unknown") {
			return PrivilegedFacts{}, invalid
		}
	}
	for _, key := range []string{"k3s_load", "k3s_agent_load"} {
		if !oneOf(values[key], "loaded", "not-found", "masked", "error", "bad-setting", "unknown") {
			return PrivilegedFacts{}, invalid
		}
	}
	for _, key := range []string{"k3s_active", "k3s_agent_active"} {
		if !oneOf(values[key], "active", "inactive", "failed", "activating", "deactivating", "unknown") {
			return PrivilegedFacts{}, invalid
		}
	}
	facts := PrivilegedFacts{MachineID: values["machine_id"], BootID: values["boot_id"], BorealisPath: values["borealis_path"], BorealisConfig: values["borealis_config"],
		K3sConfig: values["k3s_config"], K3sData: values["k3s_data"], K3sBinary: values["k3s_binary"], K3sLoad: values["k3s_load"], K3sActive: values["k3s_active"],
		K3sAgentLoad: values["k3s_agent_load"], K3sAgentActive: values["k3s_agent_active"], KubeSystemUID: values["kube_system_uid"]}
	facts.NodesState = values["nodes"]
	if facts.KubeSystemUID != "unknown" && facts.KubeSystemUID != "not-present" && !validPublicUUID(facts.KubeSystemUID) {
		return PrivilegedFacts{}, invalid
	}
	if values["nodes"] != "unknown" && values["nodes"] != "not-present" {
		facts.NodesState = "observed"
		seenNames, seenIDs := map[string]bool{}, map[string]bool{}
		for _, item := range strings.Split(strings.TrimSuffix(values["nodes"], ";"), ";") {
			name, uid, ok := strings.Cut(item, ",")
			if !ok || !factPattern.MatchString(name) || !validPublicUUID(uid) || seenNames[name] || seenIDs[uid] || len(facts.Nodes) >= 100 {
				return PrivilegedFacts{}, invalid
			}
			seenNames[name], seenIDs[uid] = true, true
			facts.Nodes = append(facts.Nodes, InspectedKubernetesNode{name, uid})
		}
	}
	var err error
	facts.Addresses, facts.Routes, err = parseIPv4Inventory([]byte(values["addresses"]), []byte(values["routes"]))
	if err != nil {
		return PrivilegedFacts{}, invalid
	}
	return facts, nil
}

func validPublicUUID(value string) bool {
	return publicUUIDPattern.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}

func parseIPv4Inventory(addressJSON, routeJSON []byte) ([]IPv4Address, []IPv4Route, error) {
	var interfaces []struct {
		Name      string   `json:"ifname"`
		Flags     []string `json:"flags"`
		OperState string   `json:"operstate"`
		Info      []struct {
			Family     string  `json:"family"`
			Local      string  `json:"local"`
			PrefixLen  *int    `json:"prefixlen"`
			Dynamic    bool    `json:"dynamic"`
			Deprecated bool    `json:"deprecated"`
			Scope      string  `json:"scope"`
			Valid      *uint64 `json:"valid_life_time"`
			Preferred  *uint64 `json:"preferred_life_time"`
		} `json:"addr_info"`
	}
	var routes []struct {
		Destination string   `json:"dst"`
		Interface   string   `json:"dev"`
		Gateway     string   `json:"gateway"`
		Scope       string   `json:"scope"`
		Type        string   `json:"type"`
		Flags       []string `json:"flags"`
		Protocol    string   `json:"protocol"`
	}
	if !validInventoryJSON(addressJSON) || !validInventoryJSON(routeJSON) || json.Unmarshal(addressJSON, &interfaces) != nil || json.Unmarshal(routeJSON, &routes) != nil || len(interfaces) > 128 || len(routes) > 256 {
		return nil, nil, ErrPrivilegeInspection
	}
	var addresses []IPv4Address
	for _, iface := range interfaces {
		if !interfacePattern.MatchString(iface.Name) || len(iface.Info) > 128 {
			return nil, nil, ErrPrivilegeInspection
		}
		up, carrier := false, false
		for _, flag := range iface.Flags {
			up = up || flag == "UP"
			carrier = carrier || flag == "LOWER_UP"
		}
		for _, info := range iface.Info {
			address, err := netip.ParseAddr(info.Local)
			if err != nil || !address.Is4() || address.String() != info.Local || info.Family != "inet" || info.PrefixLen == nil || *info.PrefixLen < 0 || *info.PrefixLen > 32 || info.Valid == nil || info.Preferred == nil || len(addresses) >= 256 {
				return nil, nil, ErrPrivilegeInspection
			}
			addresses = append(addresses, IPv4Address{Interface: iface.Name, Prefix: netip.PrefixFrom(address, *info.PrefixLen), Up: up && carrier && iface.OperState == "UP", Dynamic: info.Dynamic, Permanent: !info.Deprecated && info.Scope == "global" && *info.Valid == 4294967295 && *info.Preferred == 4294967295})
		}
	}
	var observedRoutes []IPv4Route
	for _, route := range routes {
		destination := route.Destination
		if destination == "default" {
			destination = "0.0.0.0/0"
		} else if !strings.Contains(destination, "/") {
			destination += "/32"
		}
		prefix, err := netip.ParsePrefix(destination)
		if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || (route.Interface != "" && !interfacePattern.MatchString(route.Interface)) {
			return nil, nil, ErrPrivilegeInspection
		}
		var gateway netip.Addr
		if route.Gateway != "" {
			gateway, err = netip.ParseAddr(route.Gateway)
			if err != nil || !gateway.Is4() || gateway.String() != route.Gateway {
				return nil, nil, ErrPrivilegeInspection
			}
		}
		linkDown := false
		for _, flag := range route.Flags {
			linkDown = linkDown || flag == "linkdown"
		}
		observedRoutes = append(observedRoutes, IPv4Route{prefix, route.Interface, gateway, route.Scope, route.Type, linkDown, route.Protocol})
	}
	return addresses, observedRoutes, nil
}

// iproute2 JSON uses lowercase keys. Reject duplicates/case aliases and null
// observations before typed parsing, while allowing unrelated inventory keys.
func validInventoryJSON(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || len(raw) > MaxOutputBytes || !utf8.Valid(raw) || raw[0] != '[' {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 16 {
			return false
		}
		token, err := d.Token()
		if err != nil || token == nil {
			return false
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				token, err := d.Token()
				name, ok := token.(string)
				if err != nil || !ok || seen[name] || name != strings.ToLower(name) || !value(depth+1) {
					return false
				}
				seen[name] = true
			}
			token, err := d.Token()
			return err == nil && token == json.Delim('}')
		case json.Delim('['):
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			token, err := d.Token()
			return err == nil && token == json.Delim(']')
		}
		_, delimiter := token.(json.Delim)
		return !delimiter
	}
	return value(0) && d.Decode(&struct{}{}) == io.EOF
}
