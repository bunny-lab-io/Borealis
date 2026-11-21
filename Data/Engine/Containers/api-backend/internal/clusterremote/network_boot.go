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

const networkBootLibraryScript = networkBootCloudLibraryScript + systemdObservationLibraryScript + networkBootProcessLibraryScript + `
BOOT_ALIASES = {
    "systemd-networkd.service": {"systemd-networkd.service", "dbus-org.freedesktop.network1.service"},
    "systemd-networkd.socket": {"systemd-networkd.socket"},
    "multi-user.target": {"multi-user.target", "default.target", "runlevel2.target", "runlevel3.target", "runlevel4.target"},
    "graphical.target": {"graphical.target", "default.target", "runlevel5.target"},
    "sockets.target": {"sockets.target"},
}

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
                for entry in entries:
                    # Ubuntu installations may persistently disable GPT auto
                    # discovery. This exact mask cannot introduce a writer;
                    # retain its identity and reject every other override.
                    if path != "etc/systemd/system-generators" or entry.name != "systemd-gpt-auto-generator":
                        raise ValueError()
                    value = boot_entry(fd, entry.name)
                    if value[1] != "/dev/null":
                        raise ValueError()
                    result["gpt-auto-generator-mask"] = value
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
                    if not entry.name.endswith((".service", ".socket", ".target")) and not (boot_cloud_name(entry.name) and entry.name.endswith((".timer", ".path", ".mount", ".automount", ".slice", ".scope", ".device"))):
                        continue
                    value = boot_entry(fd, entry.name)
                    inventory[(path, entry.name)] = value
                    if value[1] is not None:
                        links.setdefault(entry.name, set()).add(os.path.basename(value[1]))
        finally:
            os.close(fd)
    # Conservative alias closure includes even shadowed symlinks. Unknown
    # aliases could contribute drop-ins after reload despite today's Names.
    aliases_by_unit = dict(BOOT_ALIASES, **{unit: {unit} for unit in BOOT_CLOUD_UNITS})
    for unit, allowed in aliases_by_unit.items():
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
    cloud_units = sorted(name for name in effective if boot_cloud_name(name))
    if cloud_units:
        if set(cloud_units) != BOOT_CLOUD_UNITS:
            raise ValueError()
        result["cloud-init.disabled"] = boot_cloud_marker(root)
    # A disabled service flag alone never establishes writer quiescence. Only
    # the explicitly guarded standard cloud-init layout gains support here.
    for name in effective:
        if name in ("NetworkManager.service", "networking.service", "connman.service", "wicked.service"):
            raise ValueError()
    for unit in units+cloud_units:
        if effective.get(unit, (None,))[0] != BOOT_VENDOR or inventory[(BOOT_VENDOR, unit)][1] is not None:
            raise ValueError()
        fd = directory(root, BOOT_VENDOR)
        try:
            result[unit] = read_file(fd, unit)
            if unit in BOOT_CLOUD_UNITS:
                boot_cloud_fragment(unit, result[unit][1])
        finally:
            os.close(fd)
        dropins = set()
        for alias in aliases_by_unit[unit]:
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
            for alias in sorted(aliases_by_unit[unit]):
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
    return (default, units, inventory, result, lib, cloud_units)

def boot_manager_capabilities(root):
    # ConditionCapability uses the system manager's CapBnd, not the observer's
    # effective capabilities or the service's permissions. Read PID1 directly;
    # the caller independently binds the systemd bus owner to PID1/host namespace.
    fd = directory(root, "proc/1")
    try:
        _, raw = read_file(fd, "status")
    finally:
        os.close(fd)
    text = raw.decode("ascii")
    if re.findall(r"^Pid:\s*([0-9]+)$", text, re.M) != ["1"] or re.findall(r"^Tgid:\s*([0-9]+)$", text, re.M) != ["1"]:
        raise ValueError()
    values = re.findall(r"^CapBnd:\s*([0-9a-f]{16})$", text, re.M)
    if len(values) != 1 or len(re.findall(r"^CapBnd:", text, re.M)) != 1:
        raise ValueError()
    return int(values[0], 16)

def boot_unit(owner, unit, capabilities):
    wire = bus_reply(owner, "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager", "GetUnit", "s", unit)
    expected = "/org/freedesktop/systemd1/unit/"+unit.replace("-", "_2d").replace(".", "_2e")
    if wire["type"] != "o" or wire["data"][0] != expected:
        raise ValueError()
    values = boot_properties(owner, expected, "org.freedesktop.systemd1.Unit")
    common = {"Id": unit, "LoadState": "loaded", "ActiveState": "active", "SourcePath": "", "UnitFileState": "enabled" if unit.endswith((".service", ".socket")) else "static"}
    for name, wanted in common.items():
        if boot_value(values, name, "s") != wanted:
            raise ValueError()
    states = ("listening", "running") if unit.endswith(".socket") else ("running",) if unit.endswith(".service") else ("active",)
    if boot_value(values, "SubState", "s") not in states:
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
    network_unit = unit.endswith((".service", ".socket"))
    expected_conditions = [[["ConditionCapability", False, False, "CAP_NET_ADMIN", result]] for result in (0, 1)] if network_unit else [[]]
    # JSON equality alone would alias false/0 and true/1 in nested conditions.
    # Result0 after reload is not historical success: current PID1 CapBnd must
    # independently satisfy the exact standard condition, including for result1.
    if all(json.dumps(conditions) != json.dumps(expected) for expected in expected_conditions) or boot_value(values, "Asserts", "a(sbbsi)") != [] or network_unit and not capabilities & (1 << 12):
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
    generation = boot_manager_generation(owner)
    capabilities = boot_manager_capabilities(root)
    if [boot_canonical(path) for path in paths] != ["/"+path for path in BOOT_PATHS]:
        raise ValueError()
    observed = {"cloud-init": boot_cloud_loaded(owner, files[5])}
    for unit in files[1]:
        path, values, invocation = boot_unit(owner, unit, capabilities)
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
            if type(row[0]) is not str or boot_canonical(row[0]) != "/usr/lib/systemd/systemd-networkd" or row[1] != [row[0]] or row[2] is not False or any(type(n) is not int or not 0 <= n < 2**64 for n in row[3:]) or row[8:] != [0, 0]:
                raise ValueError()
            if row[7] != pid and row[3:] != [0]*7:
                raise ValueError()
            process = boot_network_process(root, pid, row[0])
            observed[unit]["service"] = (pid, command, process)
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
    if (bus, network_owner) != bus_identity() or boot_pid(owner) != 1 or generation != boot_manager_generation(owner) or capabilities != boot_manager_capabilities(root) or boot_files(root) != files:
        raise ValueError()
    if boot_pid(network_owner) != pid or boot_network_process(root, pid, command[0][0]) != process:
        raise ValueError()
    return (files, bus, owner, network_owner, (own.st_dev, own.st_ino), observed, generation, capabilities)
`
