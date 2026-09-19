package clusterremote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"
)

// TargetNetworkBoot adds bounded persistent startup wiring to native render
// correspondence. Boot-time naming and actual reboot survival still need their
// independent qualification. No historical/imported object grants authority.
type TargetNetworkBoot struct{ observation TargetNetworkRender }

func (v TargetNetworkBoot) Matches(notBefore time.Time, target Target, key HostKey, request NetworkRenderRequest) error {
	return v.observation.matchesVersion(notBefore, target, key, request, 2)
}

func networkBootCommand(targets RouteTargets) (string, error) {
	if targets.validate() != nil {
		return "", ErrNetworkRender
	}
	raw, err := json.Marshal(targets)
	if err != nil || len(raw) > 1024 {
		return "", ErrNetworkRender
	}
	return buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin", "exec /usr/bin/python3 -I -B -c "+shellConstant(networkBootScript)+" "+shellConstant(base64.StdEncoding.EncodeToString(raw))+" 2>/dev/null"), nil
}

// InspectNetworkBoot requires the same independent original target/cohort/VIP
// authority as InspectNetworkRender. It neither enables nor reloads a service.
func (client *Client) InspectNetworkBoot(ctx context.Context, sudoPassword []byte, expected Target, approved HostKey, request NetworkRenderRequest, check func(context.Context) error) (TargetNetworkBoot, error) {
	value, err := client.inspectNetworkRender(ctx, sudoPassword, expected, approved, request, check, true)
	if err != nil {
		return TargetNetworkBoot{}, err
	}
	return TargetNetworkBoot{observation: value}, nil
}

const networkBootScript = persistentNetworkLibraryScript + activeNetworkLibraryScript + routedNetworkLibraryScript + managementLinkLibraryScript + networkRenderLibraryScript + networkBootLibraryScript + `
if __name__ == "__main__":
    main(lambda: observe_network_render(boot_snapshot))
`

const networkBootLibraryScript = `
BOOT_PATHS = ("etc/systemd/system.control", "run/systemd/system.control", "run/systemd/transient",
              "run/systemd/generator.early", "etc/systemd/system", "etc/systemd/system.attached",
              "run/systemd/system", "run/systemd/system.attached", "run/systemd/generator",
              "usr/local/lib/systemd/system", "usr/lib/systemd/system", "run/systemd/generator.late")
BOOT_VENDOR = "usr/lib/systemd/system"
BOOT_ALIASES = {
    "systemd-networkd.service": {"systemd-networkd.service", "dbus-org.freedesktop.network1.service"},
    "systemd-networkd.socket": {"systemd-networkd.socket"},
    "multi-user.target": {"multi-user.target", "default.target", "runlevel2.target", "runlevel3.target", "runlevel4.target"},
    "graphical.target": {"graphical.target", "default.target", "runlevel5.target"},
    "sockets.target": {"sockets.target"},
}

def boot_entry(fd, name):
    def stable(info):
        return (info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_gid, info.st_nlink, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    info = os.stat(name, dir_fd=fd, follow_symlinks=False)
    if info.st_uid != EXPECTED_UID or info.st_nlink != 1:
        raise ValueError()
    if stat.S_ISLNK(info.st_mode):
        link = os.readlink(name, dir_fd=fd)
        if len(link) > 4096:
            raise ValueError()
    else:
        checked(info)
        link = None
    if stable(info) != stable(os.stat(name, dir_fd=fd, follow_symlinks=False)):
        raise ValueError()
    return (stable(info), link)

def boot_canonical(path):
    return "/usr"+path if path.startswith("/lib/systemd/") else path

def boot_files(root):
    # This activation contract requires Ubuntu's standard merged-/usr layout.
    lib = boot_entry(root, "lib")
    if lib[1] not in ("usr/lib", "/usr/lib"):
        raise ValueError()
    result, inventory, links, count = {}, {}, {}, 0
    for path in ("run/systemd/system-generators", "etc/systemd/system-generators", "usr/local/lib/systemd/system-generators"):
        try:
            fd = directory(root, path)
        except FileNotFoundError:
            continue
        try:
            with os.scandir(fd) as entries:
                if next(entries, None) is not None:
                    raise ValueError()
        finally:
            os.close(fd)
    for path in BOOT_PATHS:
        try:
            fd = directory(root, path)
        except FileNotFoundError:
            continue
        try:
            with os.scandir(fd) as entries:
                for entry in entries:
                    count += 1
                    if count > 8192:
                        raise ValueError()
                    if not entry.name.endswith((".service", ".socket", ".target")):
                        continue
                    value = boot_entry(fd, entry.name)
                    inventory[(path, entry.name)] = value
                    if value[1] is not None:
                        links.setdefault(entry.name, set()).add(os.path.basename(value[1]))
        finally:
            os.close(fd)
    # Conservative alias closure includes even shadowed symlinks. Unknown
    # aliases could contribute drop-ins after reload despite today's Names.
    for unit, allowed in BOOT_ALIASES.items():
        aliases = {unit}
        for _ in range(16):
            added = {name for name, targets in links.items() if targets & aliases}
            if added <= aliases:
                break
            aliases |= added
        else:
            raise ValueError()
        if not aliases <= allowed:
            raise ValueError()
    effective = {}
    for path in reversed(BOOT_PATHS):
        for (area, name), value in inventory.items():
            if area == path:
                effective[name] = (area, value)
    choice = effective.get("default.target")
    if choice is None or choice[0] not in ("etc/systemd/system", BOOT_VENDOR) or choice[1][1] is None:
        raise ValueError()
    default = os.path.basename(choice[1][1])
    if default not in ("multi-user.target", "graphical.target"):
        raise ValueError()
    if boot_canonical(choice[1][1]) not in ("/"+BOOT_VENDOR+"/"+default, default):
        raise ValueError()
    units = ["systemd-networkd.service", "systemd-networkd.socket", "multi-user.target", "sockets.target"]
    if default == "graphical.target":
        units.append(default)
    # Unknown network config writers are not qualified through a mere disabled
    # or active/exited status. Explicit writer quiescence/support remains later
    # work; this initial contract requires their units to be absent entirely.
    for name in effective:
        if name.startswith(("cloud-init", "cloud-config.", "cloud-final.")) or name in ("NetworkManager.service", "networking.service", "connman.service", "wicked.service"):
            raise ValueError()
    for unit in units:
        if effective.get(unit, (None,))[0] != BOOT_VENDOR or inventory[(BOOT_VENDOR, unit)][1] is not None:
            raise ValueError()
        fd = directory(root, BOOT_VENDOR)
        try:
            result[unit] = read_file(fd, unit)
        finally:
            os.close(fd)
        dropins = set()
        for alias in BOOT_ALIASES[unit]:
            stem, kind = alias.rsplit(".", 1)
            dropins |= {alias+".d", kind+".d"}
            for i, char in enumerate(stem):
                if char == "-":
                    dropins.add(stem[:i+1]+"."+kind+".d")
        for path in BOOT_PATHS:
            for name in sorted(dropins):
                try:
                    fd = directory(root, path+"/"+name)
                except FileNotFoundError:
                    continue
                try:
                    with os.scandir(fd) as entries:
                        for entry in entries:
                            count += 1
                            if count > 8192 or entry.name.endswith(".conf"):
                                raise ValueError()
                finally:
                    os.close(fd)
        # Enablement/dependency directories merge independently of fragment
        # selection. Retain every applicable link, including shadowed links;
        # refuse masks/foreign destinations for either networkd dependency.
        for path in BOOT_PATHS:
            for alias in sorted(BOOT_ALIASES[unit]):
                for kind in ("wants", "requires"):
                    area = path+"/"+alias+"."+kind
                    try:
                        fd = directory(root, area)
                    except FileNotFoundError:
                        continue
                    try:
                        with os.scandir(fd) as entries:
                            for entry in entries:
                                count += 1
                                if count > 8192:
                                    raise ValueError()
                                value = boot_entry(fd, entry.name)
                                inventory[(area, entry.name)] = value
                                if value[1] is None:
                                    raise ValueError()
                                if entry.name in ("systemd-networkd.service", "systemd-networkd.socket") and boot_canonical(value[1]) not in ("/"+BOOT_VENDOR+"/"+entry.name, "../"+entry.name):
                                    raise ValueError()
                                if unit == "systemd-networkd.service" and (kind != "wants" or entry.name not in ("network.target", "systemd-networkd.socket", "netplan-ovs-cleanup.service")):
                                    raise ValueError()
                    finally:
                        os.close(fd)
    for parent, unit in (("multi-user.target", "systemd-networkd.service"), ("sockets.target", "systemd-networkd.socket")):
        fd = directory(root, "etc/systemd/system/"+parent+".wants")
        try:
            value = boot_entry(fd, unit)
            if value[1] is None or boot_canonical(value[1]) != "/"+BOOT_VENDOR+"/"+unit:
                raise ValueError()
            result[parent] = value
        finally:
            os.close(fd)
    return (default, units, inventory, result, lib)

def boot_value(values, name, signature):
    value = values.get(name)
    if type(value) is not dict or set(value) != {"type", "data"} or value["type"] != signature:
        raise ValueError()
    data = value["data"]
    if signature == "s" and type(data) is not str or signature == "b" and type(data) is not bool or signature == "u" and (type(data) is not int or not 0 <= data <= 4294967295):
        raise ValueError()
    if signature in ("as", "ay"):
        if type(data) is not list or len(data) > 256:
            raise ValueError()
        if signature == "as" and (any(type(item) is not str for item in data) or len(data) != len(set(data))):
            raise ValueError()
        if signature == "ay" and any(type(item) is not int or not 0 <= item <= 255 for item in data):
            raise ValueError()
    if signature == "(uo)" and (type(data) is not list or len(data) != 2 or type(data[0]) is not int or not 0 <= data[0] <= 4294967295 or type(data[1]) is not str):
        raise ValueError()
    if signature == "(ss)" and (type(data) is not list or len(data) != 2 or any(type(item) is not str for item in data)):
        raise ValueError()
    return data

def boot_property(owner, path, interface, name, signature):
    wire = bus_reply(owner, path, "org.freedesktop.DBus.Properties", "Get", "ss", interface, name)
    if wire["type"] != "v":
        raise ValueError()
    return boot_value({name: wire["data"][0]}, name, signature)

def boot_properties(owner, path, interface):
    wire = bus_reply(owner, path, "org.freedesktop.DBus.Properties", "GetAll", "s", interface)
    if wire["type"] != "a{sv}" or type(wire["data"][0]) is not dict:
        raise ValueError()
    return wire["data"][0]

def boot_pid(owner):
    wire = bus_reply("org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "GetConnectionUnixProcessID", "s", owner)
    if wire["type"] != "u" or type(wire["data"][0]) is not int or not 0 < wire["data"][0] <= 2147483647:
        raise ValueError()
    return wire["data"][0]

def boot_unit(owner, unit):
    wire = bus_reply(owner, "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager", "GetUnit", "s", unit)
    expected = "/org/freedesktop/systemd1/unit/"+unit.replace("-", "_2d").replace(".", "_2e")
    if wire["type"] != "o" or wire["data"][0] != expected:
        raise ValueError()
    values = boot_properties(owner, expected, "org.freedesktop.systemd1.Unit")
    common = {"Id": unit, "LoadState": "loaded", "ActiveState": "active", "SourcePath": "", "UnitFileState": "enabled" if unit.endswith((".service", ".socket")) else "static",
              "SubState": "running" if unit.endswith(".service") else "listening" if unit.endswith(".socket") else "active"}
    for name, wanted in common.items():
        if boot_value(values, name, "s") != wanted:
            raise ValueError()
    if boot_canonical(boot_value(values, "FragmentPath", "s")) != "/"+BOOT_VENDOR+"/"+unit or boot_value(values, "DropInPaths", "as"):
        raise ValueError()
    for name, wanted in (("Transient", False), ("NeedDaemonReload", False), ("ConditionResult", True), ("AssertResult", True)):
        if boot_value(values, name, "b") is not wanted:
            raise ValueError()
    if boot_value(values, "Job", "(uo)") != [0, "/"] or boot_value(values, "LoadError", "(ss)") != ["", ""]:
        raise ValueError()
    names = boot_value(values, "Names", "as")
    invocation = boot_value(values, "InvocationID", "ay")
    if unit not in names or not set(names) <= BOOT_ALIASES[unit] or len(invocation) != 16 or not any(invocation):
        raise ValueError()
    conditions = boot_value(values, "Conditions", "a(sbbsi)")
    expected_conditions = [["ConditionCapability", False, False, "CAP_NET_ADMIN", 1]] if unit.endswith((".service", ".socket")) else []
    # JSON equality alone would alias false/0 and true/1 in nested conditions.
    if json.dumps(conditions) != json.dumps(expected_conditions) or boot_value(values, "Asserts", "a(sbbsi)") != []:
        raise ValueError()
    return (expected, values, invocation)

def boot_snapshot(root):
    files = boot_files(root)
    own, init = os.stat(ROOT+"/proc/self/ns/mnt"), os.stat(ROOT+"/proc/1/ns/mnt")
    if not os.path.samestat(own, init):
        raise ValueError()
    bus, network_owner = bus_identity()
    owner = bus_string("org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus", "GetNameOwner", "s", "org.freedesktop.systemd1")
    if not re.fullmatch(r":[0-9]{1,20}\.[0-9]{1,20}", owner) or boot_pid(owner) != 1:
        raise ValueError()
    manager_path, manager_interface = "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager"
    paths = boot_property(owner, manager_path, manager_interface, "UnitPath", "as")
    if [boot_canonical(path) for path in paths] != ["/"+path for path in BOOT_PATHS] or boot_property(owner, manager_path, manager_interface, "Reloading", "b"):
        raise ValueError()
    observed = {}
    for unit in files[1]:
        path, values, invocation = boot_unit(owner, unit)
        # Native properties can change without restarting the unit. Retain
        # forward dependencies and configured conditions, not reverse ordering
        # edges contributed by unrelated jobs.
        observed[unit] = {name: values[name] for name in ("InvocationID", "Names", "Conditions", "Asserts")}
        for name in ("Wants", "Requires"):
            observed[unit][name] = sorted(boot_value(values, name, "as"))
        if unit == "systemd-networkd.service":
            if not {"network.target", "systemd-networkd.socket"} <= set(boot_value(values, "Wants", "as")) or not set(boot_value(values, "Wants", "as")) <= {"network.target", "systemd-networkd.socket", "netplan-ovs-cleanup.service"}:
                raise ValueError()
            if not set(boot_value(values, "Requires", "as")) <= {"-.mount", "system.slice"} or "multi-user.target" not in boot_value(values, "Before", "as") or "systemd-networkd.socket" not in boot_value(values, "After", "as"):
                raise ValueError()
            service = boot_properties(owner, path, "org.freedesktop.systemd1.Service")
            pid = boot_value(service, "MainPID", "u")
            if pid != boot_pid(network_owner) or boot_value(service, "ControlPID", "u") != 0:
                raise ValueError()
            for name, wanted in (("BusName", "org.freedesktop.network1"), ("Type", "notify-reload"), ("User", "systemd-network"), ("RootDirectory", ""), ("RootImage", ""), ("WorkingDirectory", "")):
                if boot_value(service, name, "s") != wanted:
                    raise ValueError()
            for name in ("Environment", "PassEnvironment", "UnsetEnvironment"):
                if boot_value(service, name, "as"):
                    raise ValueError()
            if boot_value(service, "EnvironmentFiles", "a(sb)") != []:
                raise ValueError()
            for name in ("ExecStartPre", "ExecStartPost", "ExecCondition", "ExecReload"):
                if boot_value(service, name, "a(sasbttttuii)") != []:
                    raise ValueError()
            command = boot_value(service, "ExecStart", "a(sasbttttuii)")
            if type(command) is not list or len(command) != 1 or type(command[0]) is not list or len(command[0]) != 10:
                raise ValueError()
            row = command[0]
            if type(row[0]) is not str or boot_canonical(row[0]) != "/usr/lib/systemd/systemd-networkd" or row[1] != [row[0]] or row[2] is not False or any(type(n) is not int or not 0 <= n < 2**64 for n in row[3:]) or row[7] != pid or row[8:] != [0, 0]:
                raise ValueError()
            observed[unit]["service"] = (pid, command)
        elif unit == "multi-user.target":
            if "systemd-networkd.service" not in boot_value(values, "Wants", "as") or "basic.target" not in boot_value(values, "Requires", "as"):
                raise ValueError()
        elif unit == "graphical.target" and "multi-user.target" not in boot_value(values, "Requires", "as"):
            raise ValueError()
        elif unit == "sockets.target" and "systemd-networkd.socket" not in boot_value(values, "Wants", "as"):
            raise ValueError()
    wire = bus_reply(owner, manager_path, manager_interface, "GetUnit", "s", "default.target")
    default_path = "/org/freedesktop/systemd1/unit/"+files[0].replace("-", "_2d").replace(".", "_2e")
    if wire["type"] != "o" or wire["data"][0] != default_path:
        raise ValueError()
    if (bus, network_owner) != bus_identity() or boot_pid(owner) != 1 or boot_property(owner, manager_path, manager_interface, "Reloading", "b"):
        raise ValueError()
    return (files, bus, owner, network_owner, (own.st_dev, own.st_ino), observed)
`
