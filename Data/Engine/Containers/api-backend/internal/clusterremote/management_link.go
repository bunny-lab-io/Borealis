package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
)

var ErrManagementLink = errors.New("SSH management link identity unavailable; host diagnostics withheld")

// TargetManagementLink retains one authenticated observation, not a durable
// qualification receipt. Only the pinned client can construct its provenance.
// Callers must retain current cohort/lease authority and independently qualify
// VIP ownership, ARP, boot/render correspondence and preparation eligibility.
type TargetManagementLink struct {
	target   Target
	approved HostKey
	wire     managementLinkObservation
}

type managementLinkObservation struct {
	Version int                             `json:"version"`
	Routing RoutedNetworkOwnership          `json:"routing"`
	Link    clusterbootstrap.ManagementLink `json:"link"`
}

func (value managementLinkObservation) validate() error {
	if value.Version != 1 || value.Routing.validate() != nil || !value.Link.MatchesAddress(value.Routing.Targets.Management) ||
		value.Link.NetworkNamespace != value.Routing.Active.NetworkNamespace || !slices.Equal(value.Routing.Resolved, value.Routing.Targets.Peers) {
		return ErrManagementLink
	}
	for _, network := range value.Routing.Networks {
		if network.Interface == value.Link.Interface && network.Index == value.Link.Index && network.Address == value.Link.Address {
			return nil
		}
	}
	return ErrManagementLink
}

func parseManagementLink(raw []byte) (managementLinkObservation, error) {
	var value managementLinkObservation
	if len(raw) == 0 || len(raw) > MaxOutputBytes || json.Unmarshal(raw, &value) != nil || value.validate() != nil {
		return managementLinkObservation{}, ErrManagementLink
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, bytes.TrimSpace(raw)) {
		return managementLinkObservation{}, ErrManagementLink
	}
	return value, nil
}

// ManagementLink binds the observation to independently supplied approved SSH
// identity, fresh privileged machine/boot/interface facts and complete peer/VIP
// addresses. Namespace and interface indices are meaningful on this host only.
func (value TargetManagementLink) ManagementLink(facts PrivilegedFacts, target Target, approved HostKey, peers []string) (clusterbootstrap.ManagementLink, error) {
	if target.Validate() != nil || approved.Validate() != nil || value.target != target || value.approved.Algorithm != approved.Algorithm ||
		value.approved.Fingerprint != approved.Fingerprint || !bytes.Equal(value.approved.PublicKey, approved.PublicKey) || value.wire.validate() != nil ||
		value.wire.Routing.Targets.Management != target.Address {
		return clusterbootstrap.ManagementLink{}, ErrManagementLink
	}
	if _, err := value.wire.Routing.ManagementRoutes(facts, target.Address, peers); err != nil {
		return clusterbootstrap.ManagementLink{}, ErrManagementLink
	}
	return value.wire.Link, nil
}

func managementLinkCommand(targets RouteTargets) (string, error) {
	if targets.validate() != nil {
		return "", ErrManagementLink
	}
	raw, err := json.Marshal(targets)
	if err != nil || len(raw) > 1024 {
		return "", ErrManagementLink
	}
	return buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin",
		"exec /usr/bin/python3 -I -B -c "+shellConstant(managementLinkScript)+" "+shellConstant(base64.StdEncoding.EncodeToString(raw))+" 2>/dev/null"), nil
}

// InspectManagementLink uses fixed read-only commands on the authenticated SSH
// host. Address data is independently validated in Go/Python; stdin remains only
// the sudo password line. The existing 15-second root/25-second SSH bounds apply.
func (client *Client) InspectManagementLink(ctx context.Context, sudoPassword []byte, targets RouteTargets) (TargetManagementLink, error) {
	targets.Peers = slices.Clone(targets.Peers)
	slices.Sort(targets.Peers)
	command, err := managementLinkCommand(targets)
	if err != nil || client == nil || client.ssh == nil || client.target.Validate() != nil || client.approved.Validate() != nil || targets.Management != client.target.Address {
		return TargetManagementLink{}, ErrManagementLink
	}
	raw, err := client.inspectPrivilegedOutput(ctx, sudoPassword, command)
	if err != nil {
		return TargetManagementLink{}, err
	}
	value, err := parseManagementLink(raw)
	if err != nil || value.Routing.Targets.Management != targets.Management || !slices.Equal(value.Routing.Targets.Peers, targets.Peers) {
		return TargetManagementLink{}, ErrManagementLink
	}
	return TargetManagementLink{target: client.target,
		approved: HostKey{Algorithm: client.approved.Algorithm, Fingerprint: client.approved.Fingerprint, PublicKey: bytes.Clone(client.approved.PublicKey)}, wire: value}, nil
}

const managementLinkScript = persistentNetworkLibraryScript + activeNetworkLibraryScript + routedNetworkLibraryScript + managementLinkLibraryScript + `
if __name__ == "__main__":
    main(observe_management_link)
`

const managementLinkLibraryScript = `
def management_json(raw):
    # Kernel link JSON has no systemd lifetime-alias compatibility exception.
    def pairs(items):
        result, seen = {}, set()
        for key, value in items:
            if key.casefold() in seen:
                raise ValueError()
            seen.add(key.casefold())
            result[key] = value
        return result
    def invalid(value):
        raise ValueError()
    return json.loads(raw, object_pairs_hook=pairs, parse_constant=invalid)

def management_namespace():
    own, init = os.stat(ROOT+"/proc/self/ns/net"), os.stat(ROOT+"/proc/1/ns/net")
    if not os.path.samestat(own, init) or not 0 < own.st_ino <= 9007199254740991:
        raise ValueError()
    return own.st_ino

def management_link(wire, namespace, address):
    if type(namespace) is not int or not 0 < namespace <= 9007199254740991:
        raise ValueError()
    observed = index_interfaces(wire, "ifname", "ifindex")
    matches, total = [], 0
    for name, iface in observed.items():
        if "addr_info" not in iface:
            raise ValueError()
        rows = address_rows(iface, "addr_info")
        total += len(rows)
        if total > 1024:
            raise ValueError()
        for row in rows:
            local = row.get("local")
            if type(local) is not str or len(local) > 15 or str(ipaddress.IPv4Address(local)) != local:
                raise ValueError()
            if local == address:
                matches.append((iface, row))
    if len(matches) != 1:
        raise ValueError()
    iface, row = matches[0]
    if any(key in iface for key in ("master", "link", "link_index", "link_netnsid", "linkinfo", "link_pointtopoint")):
        raise ValueError()
    if any(iface.get(key) != value for key, value in (("link_type", "ether"), ("operstate", "UP"), ("broadcast", "ff:ff:ff:ff:ff:ff"))):
        raise ValueError()
    flags = iface.get("flags")
    if type(flags) is not list or not 3 <= len(flags) <= 4 or any(type(flag) is not str for flag in flags) or len(set(flags)) != len(flags) or not {"UP", "LOWER_UP", "BROADCAST"}.issubset(flags) or set(flags)-{"UP", "LOWER_UP", "BROADCAST", "MULTICAST"}:
        raise ValueError()
    mac = iface.get("address")
    if type(mac) is not str or not re.fullmatch(r"[0-9a-f]{2}(?::[0-9a-f]{2}){5}", mac) or mac == "00:00:00:00:00:00" or int(mac[:2], 16) & 1:
        raise ValueError()
    if row.get("family") != "inet" or row.get("scope") != "global" or "peer" in row or type(row.get("prefixlen")) is not int or not 0 <= row["prefixlen"] <= 30:
        raise ValueError()
    if any(type(row.get(key, False)) is not bool or row.get(key, False) for key in ("dynamic", "deprecated", "tentative", "dadfailed")) or any(type(row.get(key)) is not int or row[key] != 4294967295 for key in ("valid_life_time", "preferred_life_time")):
        raise ValueError()
    prefix = ipaddress.IPv4Interface(address+"/"+str(row["prefixlen"]))
    if prefix.ip in (prefix.network.network_address, prefix.network.broadcast_address) or not any(prefix.network.subnet_of(ipaddress.IPv4Network(value)) for value in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")):
        raise ValueError()
    return {"interface": iface["ifname"], "index": iface["ifindex"], "address": str(prefix), "mac": mac, "network_namespace": namespace}

def read_management_link(address):
    namespace = management_namespace()
    wire = management_json(bounded_command(["/usr/sbin/ip", "-j", "-d", "-4", "address", "show"]))
    link = management_link(wire, namespace, address)
    if namespace != management_namespace():
        raise ValueError()
    return link

def management_snapshot(interfaces, targets):
    before = read_management_link(targets["management"])
    routing = routing_snapshot(interfaces, targets)
    after = read_management_link(targets["management"])
    if before != after:
        raise ValueError()
    return json.dumps([routing, after], separators=(",", ":"))

def observe_management_link():
    targets = route_targets()
    active, state = observe_active(lambda interfaces: management_snapshot(interfaces, targets))
    routing, link = strict_json(state)
    _, _, networks, resolved = strict_json(routing)
    if link["network_namespace"] != active["network_namespace"] or resolved != targets["peers"] or not any(network["interface"] == link["interface"] and network["index"] == link["index"] and network["address"] == link["address"] for network in networks):
        raise ValueError()
    return {"version": 1, "routing": {"version": 1, "targets": targets, "active": active, "networks": networks, "resolved": resolved}, "link": link}
`
