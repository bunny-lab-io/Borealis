package clusterremote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
)

var ErrPersistentNetwork = errors.New("SSH persistent network declaration unavailable; host configuration withheld")

// PersistentNetworkDeclarations proves bounded persistent Netplan declarations,
// not active renderer ownership, reboot success, ARP reachability or permission
// to prepare a host. Only unambiguous named Ethernet declarations are projected.
type PersistentNetworkDeclarations struct {
	Version    int                          `json:"version"`
	MachineID  string                       `json:"machine_id"`
	BootID     string                       `json:"boot_id"`
	Interfaces []PersistentNetworkInterface `json:"interfaces"`
}

type PersistentNetworkInterface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}

func parsePersistentNetwork(raw []byte) (PersistentNetworkDeclarations, error) {
	var value PersistentNetworkDeclarations
	if len(raw) == 0 || len(raw) > MaxOutputBytes || json.Unmarshal(raw, &value) != nil || value.validate() != nil {
		return PersistentNetworkDeclarations{}, ErrPersistentNetwork
	}
	// Fixed producer emits this canonical public projection. Missing/duplicate,
	// case-aliased, unknown and null fields cannot become positive evidence.
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, bytes.TrimSpace(raw)) {
		return PersistentNetworkDeclarations{}, ErrPersistentNetwork
	}
	return value, nil
}

func (value PersistentNetworkDeclarations) validate() error {
	if value.Version != 1 || !machineIDPattern.MatchString(value.MachineID) || value.MachineID == "00000000000000000000000000000000" ||
		!validPublicUUID(value.BootID) || value.Interfaces == nil || len(value.Interfaces) > 64 {
		return ErrPersistentNetwork
	}
	seen, addresses, previous := map[string]bool{}, map[netip.Addr]bool{}, ""
	for _, iface := range value.Interfaces {
		if !interfacePattern.MatchString(iface.Name) || iface.Name <= previous || seen[iface.Name] || len(iface.Addresses) == 0 || len(iface.Addresses) > 32 {
			return ErrPersistentNetwork
		}
		previous, seen[iface.Name] = iface.Name, true
		last := ""
		for _, text := range iface.Addresses {
			p, err := netip.ParsePrefix(text)
			if err != nil || p.String() != text || !UsableManagementAddress(p.Masked(), p.Addr()) || addresses[p.Addr()] || text <= last {
				return ErrPersistentNetwork
			}
			last, addresses[p.Addr()] = text, true
		}
	}
	return nil
}

// ManagementDeclaration binds the persistent declaration to the separately
// observed machine, boot, current interface/address and connected peer subnet.
// Caller must still verify effective backend/routing and Layer2 reachability.
func (value PersistentNetworkDeclarations) ManagementDeclaration(facts PrivilegedFacts, management string, peers []string) (netip.Prefix, error) {
	if value.validate() != nil || value.MachineID != facts.MachineID || value.BootID != facts.BootID {
		return netip.Prefix{}, ErrPersistentNetwork
	}
	prefix, err := facts.ConnectedManagementNetwork(management, peers)
	if err != nil {
		return netip.Prefix{}, ErrPersistentNetwork
	}
	for _, address := range facts.Addresses {
		if address.Prefix.Addr().String() != management {
			continue
		}
		for _, iface := range value.Interfaces {
			if iface.Name == address.Interface {
				for _, declared := range iface.Addresses {
					if declared == address.Prefix.String() {
						return prefix, nil
					}
				}
			}
		}
	}
	return netip.Prefix{}, ErrPersistentNetwork
}

var persistentNetworkCommand = buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin",
	"exec /usr/bin/python3 -I -B -c "+shellConstant(persistentNetworkScript)+" 2>/dev/null")

// InspectPersistentNetwork reads fixed host configuration through Ubuntu's
// existing Netplan parser. No caller path, command or Python source is accepted.
// Absent/incompatible bindings fail; the observer never installs dependencies.
func (client *Client) InspectPersistentNetwork(ctx context.Context, sudoPassword []byte) (PersistentNetworkDeclarations, error) {
	raw, err := client.inspectPrivilegedOutput(ctx, sudoPassword, persistentNetworkCommand)
	if err != nil {
		return PersistentNetworkDeclarations{}, err
	}
	return parsePersistentNetwork(raw)
}

// Python runs on the Ubuntu target, not in an Engine container. Netplan owns
// YAML semantics; this adapter only selects immutable input files and a narrow
// public projection. memfd avoids writing either snapshots or generated config.
const persistentNetworkScript = persistentNetworkLibraryScript + `
if __name__ == "__main__":
    main()
`

const persistentNetworkLibraryScript = `import io, json, os, re, resource, stat, sys

ROOT = "/"
EXPECTED_UID = 0

def checked(info, directory=False):
    if info.st_uid != EXPECTED_UID or info.st_mode & 0o022:
        raise ValueError()
    if directory:
        if not stat.S_ISDIR(info.st_mode):
            raise ValueError()
    elif not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > 65536:
        raise ValueError()

def directory(root, path):
    fd = os.dup(root)
    try:
        for part in path.split("/"):
            child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=fd)
            os.close(fd)
            fd = child
            checked(os.fstat(fd), True)
        return fd
    except BaseException:
        os.close(fd)
        raise

def read_file(parent, name):
    fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC, dir_fd=parent)
    with os.fdopen(fd, "rb") as source:
        before = os.fstat(source.fileno())
        checked(before)
        data = source.read(65537)
        after = os.fstat(source.fileno())
        identity = lambda s: (s.st_dev, s.st_ino, s.st_mode, s.st_uid, s.st_nlink, s.st_size, s.st_mtime_ns, s.st_ctime_ns)
        if len(data) > 65536 or identity(before) != identity(after):
            raise ValueError()
        return (identity(after), data)

def snapshot(root):
    result = {}
    # Ubuntu merged-/usr has exactly this vendor-directory alias. Other
    # intermediate symlinks and all final-file symlinks remain forbidden.
    vendor = "lib/netplan"
    try:
        if stat.S_ISLNK(os.stat("lib", dir_fd=root, follow_symlinks=False).st_mode):
            if os.readlink("lib", dir_fd=root) not in ("usr/lib", "/usr/lib"):
                raise ValueError()
            vendor = "usr/lib/netplan"
    except FileNotFoundError:
        pass
    total = 0
    for label, path in (("lib", vendor), ("etc", "etc/netplan"), ("run", "run/netplan")):
        try:
            fd = directory(root, path)
        except FileNotFoundError:
            continue
        try:
            count = 0
            with os.scandir(fd) as entries:
                for entry in entries:
                    count += 1
                    if count > 256:
                        raise ValueError()
                    if not entry.name.endswith(".yaml"):
                        continue
                    if label == "run" or len(result) >= 32:
                        raise ValueError()
                    value = read_file(fd, entry.name)
                    total += len(value[1])
                    if total > 262144:
                        raise ValueError()
                    result[(label, entry.name)] = value
        finally:
            os.close(fd)
    if not result:
        raise ValueError()
    return result

def parse_snapshot(files):
    import netplan
    parser = netplan.Parser()
    effective = {}
    # Equal basenames in /etc shadow /lib. Remaining names merge in lexical
    # order, independent of directory, using the upstream parser itself.
    for label in ("lib", "etc"):
        for (area, name), value in files.items():
            if area == label:
                effective[name] = value[1]
    for name in sorted(effective):
        fd = os.memfd_create("borealis-netplan-observation", os.MFD_CLOEXEC)
        with os.fdopen(fd, "w+b") as source:
            source.write(effective[name])
            source.seek(0)
            parser.load_yaml(source)
    state = netplan.State()
    state.import_parser_results(parser)
    return state

def project(state):
    import ipaddress, yaml
    if len(state) > 64:
        raise ValueError()
    # A second physical definition can match/rename the same live interface.
    # Hardware matching needs separate proof; do not infer exclusion from IDs.
    if any(item._has_match or item.set_name for item in state.netdefs.values()):
        return []
    rendered = io.StringIO()
    state._dump_yaml(rendered)
    if len(rendered.getvalue()) > 1048576:
        raise ValueError()
    network = yaml.safe_load(rendered.getvalue())["network"]
    blocked = set()
    for kind in ("bonds", "bridges", "vrfs"):
        for config in network.get(kind, {}).values():
            blocked.update(config.get("interfaces", []))
    result = []
    for name, definition in sorted(state.ethernets.items()):
        config = network["ethernets"][name]
        if not re.fullmatch(r"[A-Za-z0-9_.:-]{1,15}", name) or name in blocked:
            continue
        if definition.backend != "networkd" or definition._has_match or definition.set_name or definition.dhcp4 or definition.links:
            continue
        if any(key in config for key in ("activation-mode", "networkmanager", "openvswitch", "match", "set-name")):
            continue
        addresses = []
        for value in definition.addresses:
            address = ipaddress.ip_interface(value.address)
            if address.version != 4 or value.lifetime not in (None, "forever"):
                continue
            private = any(address.network.subnet_of(ipaddress.ip_network(block)) for block in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"))
            if private and address.network.prefixlen <= 30 and address.ip not in (address.network.network_address, address.network.broadcast_address):
                addresses.append(str(address))
        if len(addresses) > 32 or len(addresses) != len(set(addresses)):
            raise ValueError()
        if addresses:
            result.append({"name": name, "addresses": sorted(addresses)})
    return result

def identity(root):
    result = []
    for path, name, pattern in (("etc", "machine-id", r"[0-9a-f]{32}"), ("proc/sys/kernel/random", "boot_id", r"[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}")):
        fd = directory(root, path)
        try:
            value = read_file(fd, name)[1].decode("ascii").strip()
        finally:
            os.close(fd)
        if not re.fullmatch(pattern, value) or not value.replace("0", "").replace("-", ""):
            raise ValueError()
        result.append(value)
    return result

def observe():
    if os.geteuid() != EXPECTED_UID:
        raise ValueError()
    root = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        checked(os.fstat(root), True)
        before_id = identity(root)
        before = snapshot(root)
        interfaces = project(parse_snapshot(before))
        if before != snapshot(root) or before_id != identity(root):
            raise ValueError()
        return {"version": 1, "machine_id": before_id[0], "boot_id": before_id[1], "interfaces": interfaces}
    finally:
        os.close(root)

def main(observation=observe):
    try:
        resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
        resource.setrlimit(resource.RLIMIT_AS, (268435456, 268435456))
        resource.setrlimit(resource.RLIMIT_CPU, (5, 5))
        result = json.dumps(observation(), separators=(",", ":"), ensure_ascii=True)
        if len(result) > 16384:
            raise ValueError()
        print(result)
    except BaseException:
        sys.exit(1)

`
