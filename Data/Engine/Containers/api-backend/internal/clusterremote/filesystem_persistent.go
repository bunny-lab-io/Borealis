package clusterremote

// Root-backed ext4/xfs is the initial supported persistent installation shape.
// No mount, daemon reload, generator execution or host file creation occurs.
// Reuse the bounded no-activation bus transport and safe file readers already
// used by startup observation. Unused network/cloud helpers are not invoked.
const persistentFilesystemScript = persistentNetworkLibraryScript + activeNetworkLibraryScript + systemdObservationLibraryScript + filesystemLibraryScript + persistentFilesystemLibraryScript + `
if __name__ == "__main__":
    try:
        resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
        resource.setrlimit(resource.RLIMIT_AS, (268435456, 268435456))
        resource.setrlimit(resource.RLIMIT_CPU, (5, 5))
        if len(sys.argv) != 2 or len(sys.argv[1]) > 16384:
            raise ValueError()
        result = observe_persistent_filesystems(json.loads(base64.b64decode(sys.argv[1], validate=True)))
        wire = json.dumps(result, separators=(",", ":"), ensure_ascii=True)
        print(wire.replace("<", "\\u003c").replace(">", "\\u003e").replace("&", "\\u0026"))
    except BaseException:
        sys.exit(1)
`

const persistentFilesystemLibraryScript = `
PFS_UUID = r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"
PFS_OPTIONS = {"defaults", "rw", "relatime", "noatime", "nodiratime", "lazytime", "discard", "errors=remount-ro"}

def pfs_overlap(left, right):
    def contains(parent, child):
        return parent == "/" or parent == child or child.startswith(parent+"/")
    return contains(left, right) or contains(right, left)

def pfs_options(value):
    options = value.split(",")
    if not options or len(options) != len(set(options)) or not set(options) <= PFS_OPTIONS:
        raise ValueError()
    return options

def pfs_fstab(raw, paths):
    root = None
    for line in raw.decode("ascii").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        fields = line.split()
        if not 4 <= len(fields) <= 6:
            raise ValueError()
        source, point, kind, options = [fs_unescape(s) for s in fields[:4]]
        if any(not re.fullmatch(r"[0-9]+", s) for s in fields[4:]):
            raise ValueError()
        if kind == "swap" and point == "none":
            continue
        fs_path(point)
        if point != "/":
            if any(pfs_overlap(point, p) for p in paths):
                raise ValueError()
            continue
        if root is not None or kind not in ("ext4", "xfs"):
            raise ValueError()
        uuid = source.removeprefix("UUID=") if source.startswith("UUID=") else source.removeprefix("/dev/disk/by-uuid/")
        if source not in ("UUID="+uuid, "/dev/disk/by-uuid/"+uuid) or not re.fullmatch(PFS_UUID, uuid):
            raise ValueError()
        pfs_options(options)
        root = (uuid, kind, options)
    if root is None:
        raise ValueError()
    return root

def pfs_unit_point(name, suffix):
    stem = name.removesuffix(suffix)
    if stem == "-":
        return "/"
    if not stem or re.search(r"[^A-Za-z0-9:_.\\-]|\\(?!x[0-9a-f]{2})", stem):
        raise ValueError()
    # Escape '-' is a literal hyphen; unescaped '-' separates components.
    value = re.sub(r"\\x([0-9a-f]{2})", lambda m: chr(int(m[1], 16)), stem.replace("-", "/"))
    return fs_path("/"+value)

def pfs_file(root, path):
    parent, name = path.rsplit("/", 1)
    fd = directory(root, parent)
    try:
        meta, raw = read_file(fd, name)
        return (meta, hashlib.sha256(raw).hexdigest()), raw
    finally:
        os.close(fd)

def pfs_generated(raw, declaration):
    section, values = "", {}
    for line in raw.decode("ascii").splitlines():
        line = line.strip()
        if not line or line.startswith(("#", ";")):
            continue
        if line in ("[Unit]", "[Mount]"):
            section = line[1:-1]
            continue
        if "=" not in line or line.endswith("\\"):
            raise ValueError()
        key, value = line.split("=", 1)
        pair = (section, key)
        allowed = {("Unit", "Documentation"), ("Unit", "SourcePath"), ("Unit", "Before"), ("Unit", "After"),
                   ("Mount", "What"), ("Mount", "Where"), ("Mount", "Type"), ("Mount", "Options")}
        if pair not in allowed or pair in values:
            raise ValueError()
        values[pair] = value
    uuid, kind, options = declaration
    for pair, value in {("Unit", "SourcePath"): "/etc/fstab", ("Unit", "Before"): "local-fs.target",
                        ("Mount", "What"): "/dev/disk/by-uuid/"+uuid, ("Mount", "Where"): "/", ("Mount", "Type"): kind}.items():
        if values.get(pair) != value:
            raise ValueError()
    if values.get(("Mount", "Options"), "defaults") != options:
        raise ValueError()
    # Only the standard source-device ordering may supplement the root mount.
    escaped = ("dev-disk-by\\x2duuid-"+uuid.replace("-", "\\x2d"))
    if values.get(("Unit", "After"), "") not in ("", "blockdev@"+escaped+".target"):
        raise ValueError()

def pfs_files(root, paths):
    # merged-/usr is part of the existing Ubuntu startup contract.
    lib = boot_entry(root, "lib")
    if lib[1] not in ("usr/lib", "/usr/lib"):
        raise ValueError()
    proof, raw = pfs_file(root, "etc/fstab")
    declaration = pfs_fstab(raw, paths)
    result = {"fstab": proof, "lib": lib}
    generated = None
    count = 0
    for area in BOOT_PATHS:
        try:
            fd = directory(root, area)
        except FileNotFoundError:
            continue
        try:
            for name in sorted(os.listdir(fd)):
                count += 1
                if count > 8192:
                    raise ValueError()
                dropin = name.endswith((".mount.d", ".automount.d")) or name in ("mount.d", "automount.d")
                if not dropin and not name.endswith((".mount", ".automount")):
                    continue
                unit = name.removesuffix(".d") if dropin else name
                suffix = ".automount" if unit.endswith(".automount") else ".mount"
                # A dash-prefix drop-in also applies to descendant mount names.
                stem = unit.removesuffix(suffix)
                if dropin and stem.endswith("-") and stem != "-":
                    unit = stem[:-1]+suffix
                point = "/" if name in ("mount.d", "automount.d") else pfs_unit_point(unit, suffix)
                if not any(pfs_overlap(point, p) for p in paths):
                    continue
                if dropin:
                    child = directory(root, area+"/"+name)
                    try:
                        names = os.listdir(child)
                        count += len(names)
                        if count > 8192 or any(n.endswith(".conf") for n in names):
                            raise ValueError()
                    finally:
                        os.close(child)
                    continue
                if area != "run/systemd/generator" or name != "-.mount" or generated is not None:
                    raise ValueError()
                meta, data = read_file(fd, name)
                pfs_generated(data, declaration)
                generated = (meta, hashlib.sha256(data).hexdigest())
        finally:
            os.close(fd)
    if generated is None:
        raise ValueError()
    result["generated"] = generated
    # GPT auto-discovery could mount /var independently of fstab. This first
    # supported shape requires the persistent mask observed on the lab hosts.
    mask = None
    for area in ("run/systemd/system-generators", "etc/systemd/system-generators", "usr/local/lib/systemd/system-generators"):
        try:
            fd = directory(root, area)
        except FileNotFoundError:
            continue
        try:
            for name in os.listdir(fd):
                if area != "etc/systemd/system-generators" or name != "systemd-gpt-auto-generator" or mask is not None:
                    raise ValueError()
                mask = boot_entry(fd, name)
                if mask[1] != "/dev/null":
                    raise ValueError()
        finally:
            os.close(fd)
    if mask is None:
        raise ValueError()
    result["gpt-mask"] = mask
    return declaration, result

def pfs_device(root, uuid):
    fd = directory(root, "dev/disk/by-uuid")
    try:
        link = boot_entry(fd, uuid)
        # udev's fixed relative device link; no arbitrary symlink chain.
        if link[1] is None or not re.fullmatch(r"\.\./\.\./[A-Za-z0-9_:-]{1,128}", link[1]):
            raise ValueError()
    finally:
        os.close(fd)
    dev = directory(root, "dev")
    try:
        node = os.stat(link[1][6:], dir_fd=dev, follow_symlinks=False)
        if not stat.S_ISBLK(node.st_mode) or node.st_uid != EXPECTED_UID or node.st_mode & 0o002:
            raise ValueError()
        device = str(os.major(node.st_rdev))+":"+str(os.minor(node.st_rdev))
        return device, (link, node.st_dev, node.st_ino, node.st_rdev, node.st_mode, node.st_uid, node.st_gid)
    finally:
        os.close(dev)

def pfs_bus(paths, declaration, device):
    common = ("org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus")
    bus = bus_string(*common, "GetId")
    owner = bus_string(*common, "GetNameOwner", "s", "org.freedesktop.systemd1")
    if not re.fullmatch(r"[0-9a-f]{32}", bus) or bus == "0"*32 or not re.fullmatch(r":[0-9]{1,20}\.[0-9]{1,20}", owner) or boot_pid(owner) != 1:
        raise ValueError()
    manager, interface = "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager"
    generation = boot_manager_generation(owner)
    if [boot_canonical(p) for p in boot_property(owner, manager, interface, "UnitPath", "as")] != ["/"+p for p in BOOT_PATHS]:
        raise ValueError()
    # Do not load absent units. Include inactive loaded mounts/automounts so a
    # stale manager definition cannot hide behind a deleted configuration file.
    wire = bus_reply(owner, manager, interface, "ListUnitsByPatterns", "asas", "0", "2", "*.mount", "*.automount")
    rows = wire["data"][0]
    if wire["type"] != "a(ssssssouso)" or type(rows) is not list or len(rows) > 2048:
        raise ValueError()
    found = False
    for row in rows:
        if type(row) is not list or len(row) != 10 or any(type(row[i]) is not str for i in (0,1,2,3,4,5,6,8,9)) or type(row[7]) is not int:
            raise ValueError()
        name = row[0]
        suffix = ".automount" if name.endswith(".automount") else ".mount"
        if not name.endswith(suffix):
            raise ValueError()
        if not any(pfs_overlap(pfs_unit_point(name, suffix), p) for p in paths):
            continue
        if name != "-.mount" or found or row[2:6] != ["loaded", "active", "mounted", ""] or row[6] != "/org/freedesktop/systemd1/unit/_2d_2emount" or row[7] != 0:
            raise ValueError()
        found = True
    if not found:
        raise ValueError()
    path = "/org/freedesktop/systemd1/unit/_2d_2emount"
    unit = boot_properties(owner, path, "org.freedesktop.systemd1.Unit")
    for name, expected in {"Id": "-.mount", "LoadState": "loaded", "ActiveState": "active", "SubState": "mounted", "FragmentPath": "/run/systemd/generator/-.mount", "SourcePath": "/etc/fstab", "UnitFileState": "generated"}.items():
        if boot_value(unit, name, "s") != expected:
            raise ValueError()
    for name in ("Transient", "NeedDaemonReload"):
        if boot_value(unit, name, "b"):
            raise ValueError()
    if boot_value(unit, "Names", "as") != ["-.mount"] or boot_value(unit, "DropInPaths", "as") or boot_value(unit, "Job", "(uo)") != [0, "/"] or boot_value(unit, "LoadError", "(ss)") != ["", ""]:
        raise ValueError()
    if boot_value(unit, "Conditions", "a(sbbsi)") != [] or boot_value(unit, "Asserts", "a(sbbsi)") != []:
        raise ValueError()
    mount = boot_properties(owner, path, "org.freedesktop.systemd1.Mount")
    what = boot_value(mount, "What", "s")
    # The manager reports the kernel device, not necessarily fstab's UUID link.
    if not re.fullmatch(r"/dev/[A-Za-z0-9_:-]{1,128}", what):
        raise ValueError()
    node = os.stat(ROOT.rstrip("/")+what, follow_symlinks=False)
    if not stat.S_ISBLK(node.st_mode) or device != str(os.major(node.st_rdev))+":"+str(os.minor(node.st_rdev)):
        raise ValueError()
    if boot_value(mount, "Where", "s") != "/" or boot_value(mount, "Type", "s") != declaration[1] or boot_value(mount, "ControlPID", "u") != 0 or boot_value(mount, "Result", "s") != "success":
        raise ValueError()
    options = boot_value(mount, "Options", "s")
    pfs_options(options)
    if "rw" not in options.split(","):
        raise ValueError()
    if bus != bus_string(*common, "GetId") or owner != bus_string(*common, "GetNameOwner", "s", "org.freedesktop.systemd1") or boot_pid(owner) != 1 or generation != boot_manager_generation(owner):
        raise ValueError()
    return (bus, owner, what, options, generation)

def pfs_snapshot(paths):
    root = os.open(ROOT, FS_OPEN)
    try:
        checked(os.fstat(root), True)
        declaration, files = pfs_files(root, paths)
        device, device_proof = pfs_device(root, declaration[0])
        observation = fs_snapshot(paths)
        evidence = observation["evidence"]
        mounts = fs_mounts(fs_read("/proc/self/mountinfo", 1048576))
        roots = [(n,m) for n,m in mounts.items() if m[2] == "/"]
        if len(roots) != 1:
            raise ValueError()
        number, mount = roots[0]
        if mount[0] != device or mount[1] != "/" or mount[4] != declaration[1] or "rw" not in mount[3]:
            raise ValueError()
        pfs_options(",".join(mount[3]))
        if not set(mount[5]) <= PFS_OPTIONS | {"attr2", "inode64", "logbufs=8", "logbsize=32k", "noquota"} or "rw" not in mount[5]:
            raise ValueError()
        if any(p["mount_id"] != number or p["mount_point"] != "/" or p["mount_root"] != "/" for p in evidence["paths"]) or len(evidence["filesystems"]) != 1 or evidence["filesystems"][0]["device"] != device:
            raise ValueError()
        if any(n != number and any(pfs_overlap(m[2], p) for p in paths) for n,m in mounts.items()):
            raise ValueError()
        manager = pfs_bus(paths, declaration, device)
        # File/device proof brackets manager reads; a final marker/unit removal
        # must invalidate this acquisition rather than wait for another call.
        if (declaration, files) != pfs_files(root, paths) or (device, device_proof) != pfs_device(root, declaration[0]):
            raise ValueError()
        final = fs_snapshot(paths)
        for left, right in zip(evidence["filesystems"], final["evidence"]["filesystems"]):
            left["available_bytes"] = right["available_bytes"] = min(left["available_bytes"], right["available_bytes"])
        if observation != final:
            raise ValueError()
        proof = json.dumps((files, device_proof, manager), sort_keys=True, separators=(",", ":")).encode("ascii")
        evidence["receipt"] = hashlib.sha256(evidence["receipt"].encode("ascii")+b"\x00"+proof).hexdigest()
        observation["version"] = 2
        return observation
    finally:
        os.close(root)

def observe_persistent_filesystems(paths):
    if type(paths) is not list or not 1 <= len(paths) <= 8 or any(type(p) is not str for p in paths) or paths != sorted(set(paths)):
        raise ValueError()
    for p in paths:
        fs_path(p)
    first, second = pfs_snapshot(paths), pfs_snapshot(paths)
    if len(first["evidence"]["filesystems"]) != len(second["evidence"]["filesystems"]):
        raise ValueError()
    for left, right in zip(first["evidence"]["filesystems"], second["evidence"]["filesystems"]):
        left["available_bytes"] = right["available_bytes"] = min(left["available_bytes"], right["available_bytes"])
    if first != second:
        raise ValueError()
    return first
`
