package clusterremote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
)

var ErrRoutedNetwork = errors.New("SSH direct management routing unavailable; host diagnostics withheld")

// RoutedNetworkOwnership proves direct IPv4 routing under the standard RPDB.
// Exclusions retain local-table and competing route destinations without
// exporting unrelated host routes or policy details. This is not ARP, firewall,
// persistent boot configuration, complete cohort or preparation authority.
type RoutedNetworkOwnership struct {
	Version  int                    `json:"version"`
	Targets  RouteTargets           `json:"targets"`
	Active   ActiveNetworkOwnership `json:"active"`
	Networks []DirectNetwork        `json:"networks"`
	Resolved []string               `json:"resolved"`
}

// RouteTargets contains public canonical private IPv4 data only. The worker
// supplies authoritative management/peer/VIP addresses; no paths or commands.
type RouteTargets struct {
	Management string   `json:"management"`
	Peers      []string `json:"peers"`
}

func (targets RouteTargets) validate() error {
	management, err := netip.ParseAddr(targets.Management)
	if err != nil || !management.Is4() || !management.IsPrivate() || management.String() != targets.Management || len(targets.Peers) == 0 || len(targets.Peers) > 32 {
		return ErrRoutedNetwork
	}
	previous := ""
	for _, text := range targets.Peers {
		peer, err := netip.ParseAddr(text)
		if err != nil || !peer.Is4() || !peer.IsPrivate() || peer.String() != text || text <= previous || peer == management {
			return ErrRoutedNetwork
		}
		previous = text
	}
	return nil
}

type DirectNetwork struct {
	Interface string   `json:"interface"`
	Index     int      `json:"index"`
	Address   string   `json:"address"`
	Excluded  []string `json:"excluded"`
}

func parseRoutedNetwork(raw []byte) (RoutedNetworkOwnership, error) {
	var value RoutedNetworkOwnership
	if len(raw) == 0 || len(raw) > MaxOutputBytes || json.Unmarshal(raw, &value) != nil || value.validate() != nil {
		return RoutedNetworkOwnership{}, ErrRoutedNetwork
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, bytes.TrimSpace(raw)) {
		return RoutedNetworkOwnership{}, ErrRoutedNetwork
	}
	return value, nil
}

func (value RoutedNetworkOwnership) validate() error {
	if value.Version != 1 || value.Targets.validate() != nil || value.Active.validate() != nil || value.Networks == nil || len(value.Networks) > 128 || value.Resolved == nil || len(value.Resolved) > len(value.Targets.Peers) {
		return ErrRoutedNetwork
	}
	previous := ""
	for _, peer := range value.Resolved {
		if peer <= previous || !slices.Contains(value.Targets.Peers, peer) {
			return ErrRoutedNetwork
		}
		previous = peer
	}
	previousInterface, previousAddress, count := "", "", 0
	for _, network := range value.Networks {
		if network.Interface < previousInterface || network.Interface == previousInterface && network.Address <= previousAddress || network.Excluded == nil || len(network.Excluded) == 0 || len(network.Excluded) > 256 {
			return ErrRoutedNetwork
		}
		previousInterface, previousAddress = network.Interface, network.Address
		matched := false
		for _, iface := range value.Active.Interfaces {
			if iface.Name == network.Interface && iface.Index == network.Index {
				for _, address := range iface.Addresses {
					matched = matched || address == network.Address
				}
			}
		}
		if !matched {
			return ErrRoutedNetwork
		}
		address, _ := netip.ParsePrefix(network.Address) // Active validation established canonical IPv4.
		prefix := address.Masked()
		last, local := "", false
		var excluded []netip.Prefix
		for _, text := range network.Excluded {
			p, err := netip.ParsePrefix(text)
			if err != nil || !p.Addr().Is4() || p != p.Masked() || p.String() != text || text <= last || p.Bits() <= prefix.Bits() || !prefix.Contains(p.Addr()) {
				return ErrRoutedNetwork
			}
			for _, other := range excluded {
				if p.Overlaps(other) {
					return ErrRoutedNetwork
				}
			}
			excluded = append(excluded, p)
			local = local || p.Contains(address.Addr())
			last, count = text, count+1
		}
		if !local || count > 512 {
			return ErrRoutedNetwork
		}
	}
	return nil
}

// ManagementRoutes checks every supplied peer/VIP against the same direct
// subnet proof. Callers must supply the complete authoritative cohort and
// retain fresh ownership; this method does not invent or approve that cohort.
func (value RoutedNetworkOwnership) ManagementRoutes(facts PrivilegedFacts, management string, peers []string) (netip.Prefix, error) {
	if value.validate() != nil || management != value.Targets.Management || len(peers) == 0 || len(peers) > 32 || !slices.Equal(value.Resolved, value.Targets.Peers) {
		return netip.Prefix{}, ErrRoutedNetwork
	}
	ordered := slices.Clone(peers)
	slices.Sort(ordered)
	if !slices.Equal(ordered, value.Targets.Peers) {
		return netip.Prefix{}, ErrRoutedNetwork
	}
	prefix, err := value.Active.ManagementNetwork(facts, management, peers)
	if err != nil {
		return netip.Prefix{}, ErrRoutedNetwork
	}
	seen := map[string]bool{management: true}
	for _, peer := range peers {
		if seen[peer] {
			return netip.Prefix{}, ErrRoutedNetwork
		}
		seen[peer] = true
	}
	for _, network := range value.Networks {
		address, _ := netip.ParsePrefix(network.Address)
		if address.Addr().String() != management || address.Masked() != prefix {
			continue
		}
		for _, peer := range peers {
			ip, _ := netip.ParseAddr(peer) // Active.ManagementNetwork checked canonical usable peers.
			for _, text := range network.Excluded {
				excluded, _ := netip.ParsePrefix(text)
				if excluded.Contains(ip) {
					return netip.Prefix{}, ErrRoutedNetwork
				}
			}
		}
		return prefix, nil
	}
	return netip.Prefix{}, ErrRoutedNetwork
}

func routedNetworkCommand(targets RouteTargets) (string, error) {
	if targets.validate() != nil {
		return "", ErrRoutedNetwork
	}
	raw, err := json.Marshal(targets)
	if err != nil || len(raw) > 1024 {
		return "", ErrRoutedNetwork
	}
	// Address data is public, bounded and independently revalidated by Python.
	// It is one quoted argv value, never executable Python/shell text or stdin.
	return buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin",
		"exec /usr/bin/python3 -I -B -c "+shellConstant(routedNetworkScript)+" "+shellConstant(base64.StdEncoding.EncodeToString(raw))+" 2>/dev/null"), nil
}

func (client *Client) InspectRoutedNetwork(ctx context.Context, sudoPassword []byte, targets RouteTargets) (RoutedNetworkOwnership, error) {
	targets.Peers = slices.Clone(targets.Peers)
	slices.Sort(targets.Peers)
	command, err := routedNetworkCommand(targets)
	if err != nil {
		return RoutedNetworkOwnership{}, err
	}
	raw, err := client.inspectPrivilegedOutput(ctx, sudoPassword, command)
	if err != nil {
		return RoutedNetworkOwnership{}, err
	}
	value, err := parseRoutedNetwork(raw)
	if err != nil || value.Targets.Management != targets.Management || !slices.Equal(value.Targets.Peers, targets.Peers) {
		return RoutedNetworkOwnership{}, ErrRoutedNetwork
	}
	return value, nil
}

const routedNetworkScript = persistentNetworkLibraryScript + activeNetworkLibraryScript + routedNetworkLibraryScript + `
if __name__ == "__main__":
    main(observe_routing)
`

const routedNetworkLibraryScript = `
import base64

def route_targets():
    if len(sys.argv) != 2 or len(sys.argv[1]) > 1368:
        raise ValueError()
    raw = base64.b64decode(sys.argv[1], validate=True)
    if len(raw) > 1024 or base64.b64encode(raw).decode() != sys.argv[1]:
        raise ValueError()
    targets = strict_json(raw)
    if type(targets) is not dict or set(targets) != {"management", "peers"} or type(targets["peers"]) is not list or not 1 <= len(targets["peers"]) <= 32:
        raise ValueError()
    private = [ipaddress.IPv4Network(value) for value in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")]
    for text in [targets["management"]]+targets["peers"]:
        if type(text) is not str or len(text) > 15:
            raise ValueError()
        address = ipaddress.IPv4Address(text)
        if str(address) != text or not any(address in network for network in private):
            raise ValueError()
    if targets["peers"] != sorted(set(targets["peers"])) or targets["management"] in targets["peers"] or raw != json.dumps(targets, separators=(",", ":")).encode():
        raise ValueError()
    return targets

def routing_snapshot(interfaces, targets):
    # Numeric detailed output avoids host-defined table/protocol/scope aliases
    # and omission of main-table/type/default metadata. No query input is used.
    rules = strict_json(bounded_command(["/usr/sbin/ip", "-j", "-N", "-d", "-4", "rule", "show"]))
    routes = strict_json(bounded_command(["/usr/sbin/ip", "-j", "-N", "-d", "-4", "route", "show", "table", "all"]))
    if type(rules) is not list or len(rules) > 128 or type(routes) is not list or len(routes) > 512:
        raise ValueError()
    if any(type(row) is not dict for row in rules+routes):
        raise ValueError()
    networks = direct_networks({"interfaces": interfaces}, json.dumps([rules, routes]))
    selected = [network for network in networks if str(ipaddress.ip_interface(network["address"]).ip) == targets["management"]]
    resolved = []
    if len(selected) == 1:
        network = selected[0]
        prefix = ipaddress.ip_interface(network["address"]).network
        for peer in targets["peers"]:
            ip = ipaddress.IPv4Address(peer)
            if ip not in prefix or ip in (prefix.network_address, prefix.broadcast_address) or any(ip in ipaddress.IPv4Network(value) for value in network["excluded"]):
                continue
            # No output-interface forcing. Read actual kernel destination,
            # including cached next-hop exceptions that a FIB dump omits.
            wire = strict_json(bounded_command(["/usr/sbin/ip", "-j", "-N", "-d", "-4", "route", "get", peer, "from", targets["management"]]))
            if not resolved_direct(wire, targets["management"], peer, network["interface"]):
                raise ValueError()
            resolved.append(peer)
    # Preserve every scalar type and relevant result during drift checks.
    return json.dumps([rules, routes, networks, resolved], separators=(",", ":"))

def resolved_direct(wire, management, peer, interface):
    if type(wire) is not list or len(wire) != 1 or type(wire[0]) is not dict:
        return False
    route = wire[0]
    required = {"type", "dst", "from", "dev", "table", "flags", "uid", "cache"}
    if set(route) != required:
        return False
    return (route["type"] == "1" and route["dst"] == peer and route["from"] == management and
            route["dev"] == interface and route["table"] == "254" and route["flags"] == [] and
            type(route["uid"]) is int and route["uid"] == EXPECTED_UID and route["cache"] == [])

def default_rules(rules):
    if len(rules) != 3:
        return False
    for rule, priority, table in zip(rules, (0, 32766, 32767), ("255", "254", "253")):
        if set(rule) != {"priority", "src", "table", "protocol"} or type(rule["priority"]) is not int or rule["priority"] != priority or rule["src"] != "all" or rule["table"] != table or rule["protocol"] != "2":
            return False
    return True

def route_destination(route):
    table, kind, text = route.get("table"), route.get("type"), route.get("dst")
    if type(table) is not str or not re.fullmatch(r"[1-9][0-9]{0,9}", table) or int(table) > 4294967295 or type(kind) is not str or not re.fullmatch(r"[1-9][0-9]{0,2}", kind) or int(kind) > 255 or type(text) is not str:
        raise ValueError()
    if text == "default":
        return ipaddress.IPv4Network("0.0.0.0/0")
    if "/" not in text:
        address = ipaddress.IPv4Address(text)
        if str(address) != text:
            raise ValueError()
        return ipaddress.IPv4Network(text+"/32")
    destination = ipaddress.IPv4Network(text, strict=True)
    if str(destination) != text:
        raise ValueError()
    return destination

def plain_kernel_route(route, iface, address, local=False):
    required = {"type", "dst", "dev", "table", "protocol", "scope", "prefsrc", "flags"}
    if not required.issubset(route) or set(route)-required-{"metric"}:
        return False
    if "metric" in route and (type(route["metric"]) is not int or not 0 <= route["metric"] <= 4294967295):
        return False
    return (route["type"] == ("2" if local else "1") and route["table"] == ("255" if local else "254") and
            route["protocol"] == "2" and route["scope"] == ("254" if local else "253") and
            route["dev"] == iface["name"] and route["prefsrc"] == str(address.ip) and route["flags"] == [])

def direct_networks(active, state):
    rules, raw_routes = strict_json(state)
    routes = [(row, route_destination(row)) for row in raw_routes]
    if not default_rules(rules):
        return []
    result = []
    for iface in active["interfaces"]:
        for text in iface["addresses"]:
            address = ipaddress.ip_interface(text)
            network = address.network
            # Multiple equal-prefix routes are inconclusive even if their
            # metrics differ: do not guess TOS, multipath or special actions.
            bases = [row for row, dst in routes if row["table"] == "254" and dst == network]
            local = [row for row, dst in routes if row["table"] == "255" and dst == ipaddress.IPv4Network(str(address.ip)+"/32")]
            if len(bases) != 1 or not plain_kernel_route(bases[0], iface, address) or len(local) != 1 or not plain_kernel_route(local[0], iface, address, True):
                continue
            excluded = []
            for row, destination in routes:
                if row["table"] == "255" and destination.overlaps(network):
                    excluded.append(destination if destination.subnet_of(network) else network)
                elif row["table"] == "254" and destination.prefixlen > network.prefixlen and destination.subnet_of(network):
                    excluded.append(destination)
            excluded = list(ipaddress.collapse_addresses(excluded))
            if network in excluded:
                continue
            result.append({"interface": iface["name"], "index": iface["index"], "address": text,
                           "excluded": sorted(str(value) for value in excluded)})
    return result

def observe_routing():
    targets = route_targets()
    active, state = observe_active(lambda interfaces: routing_snapshot(interfaces, targets))
    _, _, networks, resolved = strict_json(state)
    return {"version": 1, "targets": targets, "active": active, "networks": networks, "resolved": resolved}
`
