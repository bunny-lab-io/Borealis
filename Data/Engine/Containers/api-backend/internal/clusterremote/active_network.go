package clusterremote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
)

var (
	ErrActiveNetwork = errors.New("SSH active static network ownership unavailable; host diagnostics withheld")
	networkBusOwner  = regexp.MustCompile(`^:[0-9]{1,20}\.[0-9]{1,20}$`)
)

// ActiveNetworkOwnership combines persistent declarations with a stable
// networkd/system-bus instance and matching kernel address observations. It
// does not prove boot activation, effective routing, ARP or admission readiness.
type ActiveNetworkOwnership struct {
	Version          int                           `json:"version"`
	Declarations     PersistentNetworkDeclarations `json:"declarations"`
	BusID            string                        `json:"bus_id"`
	Owner            string                        `json:"owner"`
	NetworkNamespace uint64                        `json:"network_namespace"`
	Interfaces       []ActiveNetworkInterface      `json:"interfaces"`
}

type ActiveNetworkInterface struct {
	Name      string   `json:"name"`
	Index     int      `json:"index"`
	Addresses []string `json:"addresses"`
}

func parseActiveNetwork(raw []byte) (ActiveNetworkOwnership, error) {
	var value ActiveNetworkOwnership
	if len(raw) == 0 || len(raw) > MaxOutputBytes || json.Unmarshal(raw, &value) != nil || value.validate() != nil {
		return ActiveNetworkOwnership{}, ErrActiveNetwork
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, bytes.TrimSpace(raw)) {
		return ActiveNetworkOwnership{}, ErrActiveNetwork
	}
	return value, nil
}

func (value ActiveNetworkOwnership) validate() error {
	if value.Version != 1 || value.Declarations.validate() != nil || !machineIDPattern.MatchString(value.BusID) || value.BusID == "00000000000000000000000000000000" ||
		!networkBusOwner.MatchString(value.Owner) || value.NetworkNamespace == 0 || value.NetworkNamespace > 1<<53-1 || value.Interfaces == nil || len(value.Interfaces) > len(value.Declarations.Interfaces) {
		return ErrActiveNetwork
	}
	declared := map[string]map[string]bool{}
	for _, iface := range value.Declarations.Interfaces {
		declared[iface.Name] = map[string]bool{}
		for _, address := range iface.Addresses {
			declared[iface.Name][address] = true
		}
	}
	previous, indices := "", map[int]bool{}
	for _, iface := range value.Interfaces {
		if iface.Name <= previous || declared[iface.Name] == nil || iface.Index < 1 || iface.Index > 2147483647 || indices[iface.Index] || len(iface.Addresses) == 0 || len(iface.Addresses) > 32 {
			return ErrActiveNetwork
		}
		previous, indices[iface.Index] = iface.Name, true
		last := ""
		for _, address := range iface.Addresses {
			if address <= last || !declared[iface.Name][address] {
				return ErrActiveNetwork
			}
			last = address
		}
	}
	return nil
}

// ManagementNetwork additionally binds the exact interface index to a separate
// privileged inspection. Callers must retain fresh authority and run the other
// network/host qualification gates before permitting preparation.
func (value ActiveNetworkOwnership) ManagementNetwork(facts PrivilegedFacts, management string, peers []string) (netip.Prefix, error) {
	if value.validate() != nil {
		return netip.Prefix{}, ErrActiveNetwork
	}
	prefix, err := value.Declarations.ManagementDeclaration(facts, management, peers)
	if err != nil {
		return netip.Prefix{}, ErrActiveNetwork
	}
	for _, address := range facts.Addresses {
		if address.Prefix.Addr().String() == management {
			for _, iface := range value.Interfaces {
				if iface.Name == address.Interface && iface.Index == address.Index {
					for _, observed := range iface.Addresses {
						if observed == address.Prefix.String() {
							return prefix, nil
						}
					}
				}
			}
		}
	}
	return netip.Prefix{}, ErrActiveNetwork
}

var activeNetworkCommand = buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin",
	"exec /usr/bin/python3 -I -B -c "+shellConstant(activeNetworkScript)+" 2>/dev/null")

func (client *Client) InspectActiveNetwork(ctx context.Context, sudoPassword []byte) (ActiveNetworkOwnership, error) {
	raw, err := client.inspectPrivilegedOutput(ctx, sudoPassword, activeNetworkCommand)
	if err != nil {
		return ActiveNetworkOwnership{}, err
	}
	return parseActiveNetwork(raw)
}

const activeNetworkScript = persistentNetworkLibraryScript + activeNetworkLibraryScript + `
if __name__ == "__main__":
    main(observe_active)
`

const activeNetworkLibraryScript = `
import ipaddress, selectors, subprocess, time

def bounded_command(arguments):
    # Only the fixed busctl/ip calls below reach this function. No inherited
    # bus address, pager, proxy, Python path, caller environment or stdin.
    child = subprocess.Popen(arguments, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                             stderr=subprocess.DEVNULL, close_fds=True,
                             env={"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL": "C"})
    deadline = time.monotonic() + 2.5
    result = bytearray()
    try:
        with selectors.DefaultSelector() as ready:
            ready.register(child.stdout, selectors.EVENT_READ)
            while True:
                remaining = deadline - time.monotonic()
                if remaining <= 0 or not ready.select(remaining):
                    raise ValueError()
                chunk = os.read(child.stdout.fileno(), min(4096, 131073-len(result)))
                if not chunk:
                    break
                result.extend(chunk)
                if len(result) > 131072:
                    raise ValueError()
        if child.wait(timeout=max(0, deadline-time.monotonic())) != 0:
            raise ValueError()
        return bytes(result)
    finally:
        if child.poll() is None:
            child.kill()
        child.wait()
        child.stdout.close()

def strict_json(raw):
    def pairs(items):
        result, seen = {}, {}
        for key, value in items:
            if key in result:
                raise ValueError()
            if key.casefold() in seen:
                previous = seen[key.casefold()]
                # systemd255 emits both exact lifetime spellings for backward
                # compatibility. Permit only these equal-valued pairs.
                if {key, previous} not in ({"PreferredLifetimeUSec", "PreferredLifetimeUsec"}, {"ValidLifetimeUSec", "ValidLifetimeUsec"}) or type(value) is not type(result[previous]) or value != result[previous]:
                    raise ValueError()
            seen[key.casefold()] = key
            result[key] = value
        return result
    def invalid(value):
        raise ValueError()
    return json.loads(raw, object_pairs_hook=pairs, parse_constant=invalid)

def bus_reply(destination, path, interface, method, *parameters):
    raw = bounded_command(["/usr/bin/busctl", "--system", "--auto-start=no",
                           "--allow-interactive-authorization=no", "--no-pager",
                           "--timeout=2s", "--json=short", "call", destination,
                           path, interface, method, *parameters])
    wire = strict_json(raw)
    if type(wire) is not dict or set(wire) != {"type", "data"} or type(wire["data"]) is not list or len(wire["data"]) != 1:
        raise ValueError()
    return wire

def bus_string(*arguments):
    wire = bus_reply(*arguments)
    if wire["type"] != "s" or type(wire["data"][0]) is not str:
        raise ValueError()
    return wire["data"][0]

def bus_identity():
    common = ("org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus")
    bus = bus_string(*common, "GetId")
    owner = bus_string(*common, "GetNameOwner", "s", "org.freedesktop.network1")
    if not re.fullmatch(r"[0-9a-f]{32}", bus) or bus == "0"*32 or not re.fullmatch(r":[0-9]{1,20}\.[0-9]{1,20}", owner):
        raise ValueError()
    return (bus, owner)

def describe(owner):
    return strict_json(bus_string(owner, "/org/freedesktop/network1", "org.freedesktop.network1.Manager", "Describe"))

def network_namespace(root_path, owner):
    wire = bus_reply(owner, "/org/freedesktop/network1", "org.freedesktop.DBus.Properties", "Get",
                     "ss", "org.freedesktop.network1.Manager", "NamespaceId")
    value = wire["data"][0]
    if wire["type"] != "v" or type(value) is not dict or set(value) != {"type", "data"} or value["type"] != "t" or type(value["data"]) is not int or not 0 < value["data"] <= 9007199254740991:
        raise ValueError()
    # NamespaceId is the namespace inode (same contract networkctl checks).
    # Following this fixed procfs magic link reads metadata only.
    if value["data"] != os.stat(root_path+"/proc/self/ns/net").st_ino:
        raise ValueError()
    return value["data"]

def kernel_addresses():
    return strict_json(bounded_command(["/usr/sbin/ip", "-j", "-4", "address", "show"]))

def positive_index(value):
    return type(value) is int and 0 < value <= 2147483647

def index_interfaces(values, name_key, index_key):
    if type(values) is not list or len(values) > 128:
        raise ValueError()
    result, indices = {}, set()
    for value in values:
        if type(value) is not dict or type(value.get(name_key)) is not str or not re.fullmatch(r"[A-Za-z0-9_.:-]{1,15}", value[name_key]) or not positive_index(value.get(index_key)):
            raise ValueError()
        name, index = value[name_key], value[index_key]
        if name in result or index in indices:
            raise ValueError()
        result[name] = value
        indices.add(index)
    return result

def address_rows(value, key):
    rows = value.get(key, [])
    if type(rows) is not list or len(rows) > 256 or any(type(row) is not dict for row in rows):
        raise ValueError()
    return rows

def daemon_address(row):
    if type(row.get("Family")) is not int or row["Family"] != 2:
        return None
    address = row.get("Address")
    if type(address) is not list or len(address) != 4 or any(type(n) is not int or not 0 <= n <= 255 for n in address):
        raise ValueError()
    return str(ipaddress.IPv4Address(bytes(address)))

def active_projection(declarations, daemon, kernel):
    networkd = index_interfaces(daemon["Interfaces"], "Name", "Index")
    observed = index_interfaces(kernel, "ifname", "ifindex")
    result = []
    for declared in declarations:
        name = declared["name"]
        if name not in networkd or name not in observed:
            continue
        link, live = networkd[name], observed[name]
        flags = link.get("Flags")
        if link["Index"] != live["ifindex"] or type(flags) is not int or not 0 <= flags <= 4294967295 or flags & 65537 != 65537 or flags & 24:
            continue
        if any(link.get(key) != value for key, value in (("AdministrativeState", "configured"), ("OperationalState", "routable"), ("CarrierState", "carrier"), ("AddressState", "routable"), ("KernelOperationalStateString", "up"), ("ActivationPolicy", "up"))):
            continue
        if link.get("Kind") or link.get("MasterInterfaceIndex") or link.get("WirelessLanInterfaceType"):
            continue
        if link.get("NetworkFile") != "/run/systemd/network/10-netplan-"+name+".network" or "NetworkFileDropins" not in link or link["NetworkFileDropins"] not in (None, []):
            continue
        live_flags = live.get("flags")
        if type(live_flags) is not list or not all(type(flag) is str for flag in live_flags) or not {"UP", "LOWER_UP"}.issubset(live_flags) or live.get("operstate") != "UP":
            continue
        addresses = []
        for prefix in declared["addresses"]:
            address = ipaddress.ip_interface(prefix)
            # Count across every interface, not just the selected name. An IP
            # duplicated on another link cannot become unique owner evidence.
            sources = [(n, row) for n, item in networkd.items() for row in address_rows(item, "Addresses") if daemon_address(row) == str(address.ip)]
            current = [(n, row) for n, item in observed.items() for row in address_rows(item, "addr_info") if row.get("local") == str(address.ip)]
            if len(sources) != 1 or len(current) != 1 or sources[0][0] != name or current[0][0] != name:
                continue
            source, current = sources[0][1], current[0][1]
            if source.get("ConfigSource") != "static" or source.get("ConfigState") != "configured" or type(source.get("PrefixLength")) is not int or source["PrefixLength"] != address.network.prefixlen or type(source.get("Scope")) is not int or source["Scope"] != 0:
                continue
            flags = source.get("Flags")
            if type(flags) is not int or not 0 <= flags <= 4294967295 or not flags & 128 or flags & (4|8|32|64) or any(key in source for key in ("Peer", "ConfigProvider", "PreferredLifetimeUSec", "PreferredLifetimeUsec", "ValidLifetimeUSec", "ValidLifetimeUsec")):
                continue
            if current.get("family") != "inet" or current.get("scope") != "global" or type(current.get("prefixlen")) is not int or current["prefixlen"] != address.network.prefixlen or "peer" in current:
                continue
            if any(type(current.get(key, False)) is not bool or current.get(key, False) for key in ("dynamic", "deprecated", "tentative", "dadfailed")):
                continue
            if any(type(current.get(key)) is not int or current[key] != 4294967295 for key in ("valid_life_time", "preferred_life_time")):
                continue
            addresses.append(prefix)
        if addresses:
            result.append({"name": name, "index": link["Index"], "addresses": addresses})
    return result

def runtime_files(root, interfaces):
    if not interfaces:
        return {}
    fd = directory(root, "run/systemd/network")
    try:
        return {item["name"]: read_file(fd, "10-netplan-"+item["name"]+".network") for item in interfaces}
    finally:
        os.close(fd)

def observe_active(extra_snapshot=None):
    if os.geteuid() != EXPECTED_UID:
        raise ValueError()
    root = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        checked(os.fstat(root), True)
        host = identity(root)
        files = snapshot(root)
        declarations = project(parse_snapshot(files))
        bus, owner = bus_identity()
        namespace = network_namespace(ROOT, owner)
        first = active_projection(declarations, describe(owner), kernel_addresses())
        generated = runtime_files(root, first)
        extra = extra_snapshot(first) if extra_snapshot is not None else None
        second = active_projection(declarations, describe(owner), kernel_addresses())
        if extra_snapshot is not None and extra != extra_snapshot(second):
            raise ValueError()
        if first != second or generated != runtime_files(root, second) or namespace != network_namespace(ROOT, owner) or (bus, owner) != bus_identity() or files != snapshot(root) or host != identity(root):
            raise ValueError()
        active = {"version": 1, "declarations": {"version": 1, "machine_id": host[0], "boot_id": host[1], "interfaces": declarations},
                  "bus_id": bus, "owner": owner, "network_namespace": namespace, "interfaces": second}
        return active if extra_snapshot is None else (active, extra)
    finally:
        os.close(root)
`
