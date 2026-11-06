package clusterremote

// Standard Ubuntu cloud-init can remain installed when its persistent disable
// marker prevents every service/socket from starting. File presence alone is
// insufficient: retain vendor conditions, overrides and loaded writer state.
const networkBootCloudLibraryScript = `
BOOT_CLOUD_UNITS = {"cloud-init-local.service", "cloud-init.service", "cloud-config.service", "cloud-final.service",
                    "cloud-init-hotplugd.service", "cloud-init-hotplugd.socket", "cloud-init.target", "cloud-config.target"}

def boot_cloud_name(name):
    return name.startswith(("cloud-init", "cloud-config.", "cloud-final."))

def boot_cloud_marker(root):
    fd = directory(root, "etc/cloud")
    try:
        marker = boot_entry(fd, "cloud-init.disabled")
        if marker[1] is not None:
            raise ValueError()
        return marker
    finally:
        os.close(fd)

def boot_cloud_fragment(unit, data):
    # cloud-config.target is only a synchronization target. Every executable
    # writer, hotplug socket and cloud-init.target must carry the AND condition.
    if unit == "cloud-config.target":
        return
    section, pending, conditions = "", "", []
    for raw in data.decode("utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith(("#", ";")):
            continue
        pending += line
        if pending.endswith("\\"):
            pending = pending[:-1]+" "
            continue
        line, pending = pending, ""
        if line.startswith("[") and line.endswith("]"):
            section = line[1:-1]
        elif section == "Unit" and "=" in line:
            key, value = (part.strip() for part in line.split("=", 1))
            if key == "ConditionPathExists":
                conditions.append(value)
    if pending or conditions != ["!/etc/cloud/cloud-init.disabled"]:
        raise ValueError()

def boot_cloud_loaded(owner, configured):
    # List only already loaded units. Never call LoadUnit or activate a writer
    # merely to inspect it. A configured but unloaded vendor unit remains
    # protected by its checked persistent condition.
    wire = bus_reply(owner, "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager",
                     "ListUnitsByPatterns", "asas", "0", "3", "cloud-init*", "cloud-config.*", "cloud-final.*")
    rows = wire["data"][0]
    if wire["type"] != "a(ssssssouso)" or type(rows) is not list or len(rows) > len(BOOT_CLOUD_UNITS):
        raise ValueError()
    observed = {}
    for row in rows:
        if type(row) is not list or len(row) != 10 or any(type(row[i]) is not str for i in (0,1,2,3,4,5,6,8,9)) or type(row[7]) is not int:
            raise ValueError()
        unit = row[0]
        path = "/org/freedesktop/systemd1/unit/"+unit.replace("-", "_2d").replace(".", "_2e")
        if unit not in configured or unit in observed or row[2:6] != ["loaded", "inactive", "dead", ""] or row[6] != path or row[7] != 0 or row[8] != "" or row[9] != "/":
            raise ValueError()
        values = boot_properties(owner, path, "org.freedesktop.systemd1.Unit")
        for name, wanted in (("Id", unit), ("LoadState", "loaded"), ("ActiveState", "inactive"), ("SubState", "dead"), ("SourcePath", "")):
            if boot_value(values, name, "s") != wanted:
                raise ValueError()
        if boot_canonical(boot_value(values, "FragmentPath", "s")) != "/"+BOOT_VENDOR+"/"+unit or boot_value(values, "DropInPaths", "as") or boot_value(values, "Names", "as") != [unit]:
            raise ValueError()
        if boot_value(values, "Transient", "b") or boot_value(values, "NeedDaemonReload", "b") or boot_value(values, "Job", "(uo)") != [0,"/"] or boot_value(values, "LoadError", "(ss)") != ["",""]:
            raise ValueError()
        conditions = boot_value(values, "Conditions", "a(sbbsi)")
        if type(conditions) is not list or len(conditions) > 16:
            raise ValueError()
        found = False
        for condition in conditions:
            if type(condition) is not list or len(condition) != 5 or type(condition[0]) is not str or type(condition[1]) is not bool or type(condition[2]) is not bool or type(condition[3]) is not str or type(condition[4]) is not int or condition[4] not in (-1,0,1):
                raise ValueError()
            if condition[:4] == ["ConditionPathExists",False,True,"/etc/cloud/cloud-init.disabled"]:
                found = True
        if unit != "cloud-config.target" and not found:
            raise ValueError()
        if unit.endswith(".service"):
            service = boot_properties(owner, path, "org.freedesktop.systemd1.Service")
            if boot_value(service, "MainPID", "u") != 0 or boot_value(service, "ControlPID", "u") != 0:
                raise ValueError()
        if unit.endswith((".service", ".socket")):
            processes = bus_reply(owner, "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager", "GetUnitProcesses", "s", unit)
            if processes["type"] != "a(sus)" or processes["data"] != [[]]:
                raise ValueError()
        observed[unit] = conditions
    return observed
`
