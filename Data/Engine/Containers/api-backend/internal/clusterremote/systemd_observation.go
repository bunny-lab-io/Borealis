package clusterremote

// Shared fixed read-only systemd observation helpers. No method here loads,
// enables, reloads or starts a unit. Keep file and D-Bus validation identical
// for network startup and persistent mount evidence.
const systemdObservationLibraryScript = `
BOOT_PATHS = ("etc/systemd/system.control", "run/systemd/system.control", "run/systemd/transient",
              "run/systemd/generator.early", "etc/systemd/system", "etc/systemd/system.attached",
              "run/systemd/system", "run/systemd/system.attached", "run/systemd/generator",
              "usr/local/lib/systemd/system", "usr/lib/systemd/system", "run/systemd/generator.late")
BOOT_VENDOR = "usr/lib/systemd/system"
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

def boot_manager_generation(owner):
    # Reloading is a signal, not a readable D-Bus property. systemd updates
    # UnitsLoadTimestamp at manager_reloading_start before serializing/reload;
    # bind repeated reads to that generation, including an initial zero value.
    path, interface = "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager"
    state = boot_property(owner, path, interface, "SystemState", "s")
    # Both states mean boot completed. Unrelated failed units do not replace
    # the independent strict health checks on every selected mount/network unit.
    if state not in ("running", "degraded"):
        raise ValueError()
    stamp = boot_property(owner, path, interface, "UnitsLoadTimestampMonotonic", "t")
    if type(stamp) is not int or not 0 <= stamp < 2**64:
        raise ValueError()
    return (state, stamp)

`
