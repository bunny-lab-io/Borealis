package clusterremote

// Fixed read-only observer. Mount identity comes from the opened descriptor's
// fdinfo, including bind mounts, rather than a guessed path-prefix match.
const filesystemScript = filesystemLibraryScript + `
if __name__ == "__main__":
    try:
        if len(sys.argv) != 2 or len(sys.argv[1]) > 16384:
            raise ValueError()
        paths = json.loads(base64.b64decode(sys.argv[1], validate=True))
        result = observe_filesystems(paths)
        wire = json.dumps(result, separators=(",", ":"), ensure_ascii=True)
        print(wire.replace("<", "\\u003c").replace(">", "\\u003e").replace("&", "\\u0026"))
    except Exception:
        sys.exit(1)
`

const filesystemLibraryScript = `
import base64, hashlib, json, os, posixpath, re, stat, sys

FS_LIMIT = (1 << 53) - 1
FS_OPEN = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC

def fs_integer(value, minimum=0):
    if type(value) is not int or not minimum <= value <= FS_LIMIT:
        raise ValueError()
    return value

def fs_path(value):
    if type(value) is not str or not 1 <= len(value) <= 1024 or not value.startswith("/") or posixpath.normpath(value) != value or value.startswith("//"):
        raise ValueError()
    parts = value.split("/")
    if len(parts) > 65 or any(len(part) > 255 for part in parts) or any(not 32 <= ord(c) <= 126 for c in value):
        raise ValueError()
    return value

def fs_read(path, limit):
    with open(path, "rb", buffering=0) as stream:
        result = stream.read(limit + 1)
    if not result or len(result) > limit:
        raise ValueError()
    return result

def fs_identity():
    machine = fs_read("/etc/machine-id", 64).decode("ascii").strip()
    boot = fs_read("/proc/sys/kernel/random/boot_id", 64).decode("ascii").strip()
    if not re.fullmatch(r"[0-9a-f]{32}", machine) or machine == "0" * 32 or not re.fullmatch(r"[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}", boot):
        raise ValueError()
    own, init = os.stat("/proc/self/ns/mnt"), os.stat("/proc/1/ns/mnt")
    root, init_root = os.stat("/"), os.stat("/proc/1/root")
    if (own.st_dev, own.st_ino) != (init.st_dev, init.st_ino) or (root.st_dev, root.st_ino) != (init_root.st_dev, init_root.st_ino):
        raise ValueError()
    return (machine, boot, fs_integer(own.st_ino, 1), root.st_dev, root.st_ino)

def fs_decimal(value, minimum=0, maximum=FS_LIMIT):
    if not re.fullmatch(r"0|[1-9][0-9]{0,15}", value) or not minimum <= int(value) <= maximum:
        raise ValueError()
    return int(value)

def fs_unescape(value):
    # mountinfo encodes precisely these four path characters.
    if re.search(r"\\(?!040|011|012|134)", value):
        raise ValueError()
    return re.sub(r"\\(040|011|012|134)", lambda m: chr(int(m[1], 8)), value)

def fs_mounts(raw):
    lines = raw.decode("ascii").splitlines()
    if not 1 <= len(lines) <= 4096 or not raw.endswith(b"\n"):
        raise ValueError()
    mounts = {}
    for line in lines:
        fields = line.split(" ")
        if len(fields) < 10 or "" in fields or fields.count("-") != 1:
            raise ValueError()
        split = fields.index("-")
        if split < 6 or len(fields) != split + 4:
            raise ValueError()
        number = fs_decimal(fields[0], 1)
        fs_decimal(fields[1], 1)
        device = fields[2].split(":")
        if len(device) != 2:
            raise ValueError()
        for item in device:
            fs_decimal(item, maximum=4294967295)
        if number in mounts:
            raise ValueError()
        mounts[number] = (fields[2], fs_unescape(fields[3]), fs_unescape(fields[4]), fields[5].split(","), fields[split+1], fields[split+3].split(","))
    return mounts

def fs_mnt_id(fd):
    raw = fs_read("/proc/self/fdinfo/" + str(fd), 16384).decode("ascii")
    values = re.findall(r"^mnt_id:\s*([0-9]+)$", raw, re.M)
    if len(values) != 1:
        raise ValueError()
    return fs_decimal(values[0], 1)

def fs_node(fd):
    info = os.fstat(fd)
    if not stat.S_ISDIR(info.st_mode):
        raise ValueError()
    return (info.st_dev, fs_integer(info.st_ino, 1), info.st_mode, info.st_uid, info.st_gid, fs_mnt_id(fd))

def fs_capacity(fd, mount):
    device, root, point, flags, kind, superflags = mount
    fs_path(root)
    fs_path(point)
    if kind not in ("ext4", "xfs") or "rw" not in flags or "ro" in flags or "rw" not in superflags or "ro" in superflags:
        raise ValueError()
    info, values = os.fstat(fd), os.fstatvfs(fd)
    if device != str(os.major(info.st_dev)) + ":" + str(os.minor(info.st_dev)) or values.f_flag & os.ST_RDONLY:
        raise ValueError()
    unit = fs_integer(values.f_frsize, 1)
    allocation = max(unit, fs_integer(values.f_bsize, 1))
    if allocation < 512 or allocation > 1 << 20 or allocation & (allocation - 1):
        raise ValueError()
    total = fs_integer(fs_integer(values.f_blocks, 1) * unit, 1)
    available = fs_integer(fs_integer(values.f_bavail) * unit)
    free = fs_integer(fs_integer(values.f_bfree) * unit)
    inodes = fs_integer(values.f_files, 1)
    available_inodes, free_inodes = fs_integer(values.f_favail), fs_integer(values.f_ffree)
    if not available_inodes <= free_inodes <= inodes:
        raise ValueError()
    if not available <= free <= total or type(values.f_fsid) is not int or not -(1 << 63) <= values.f_fsid < (1 << 64):
        raise ValueError()
    identity = format(values.f_fsid & ((1 << 64)-1), "016x")
    if identity == "0" * 16:
        raise ValueError()
    return {"id": identity, "device": device, "type": kind, "total_bytes": total, "available_bytes": available, "allocation_unit": allocation, "total_inodes": inodes, "available_inodes": available_inodes}

def fs_selected_path(value, mounts):
    fd = os.open("/", FS_OPEN)
    ancestor, chain = "/", []
    try:
        chain.append((ancestor, fs_node(fd)))
        for part in value.split("/")[1:]:
            if not part:
                continue
            try:
                child = os.open(part, FS_OPEN, dir_fd=fd)
            except FileNotFoundError:
                break
            os.close(fd)
            fd = child
            ancestor = posixpath.join(ancestor, part)
            chain.append((ancestor, fs_node(fd)))
        node = fs_node(fd)
        mount = mounts[node[5]]
        budget = fs_capacity(fd, mount)
        if not (mount[2] == "/" or ancestor == mount[2] or ancestor.startswith(mount[2] + "/")) or node != fs_node(fd):
            raise ValueError()
        result = {"path": value, "ancestor": ancestor, "inode": node[1], "mount_id": node[5], "mount_root": mount[1], "mount_point": mount[2], "filesystem": budget["id"]}
        return result, budget, chain
    finally:
        os.close(fd)

def fs_snapshot(paths):
    identity = fs_identity()
    raw = fs_read("/proc/self/mountinfo", 1048576)
    mounts = fs_mounts(raw)
    selected, budgets, devices, chains = [], {}, {}, []
    for path in paths:
        item, budget, chain = fs_selected_path(path, mounts)
        prior = budgets.get(budget["id"])
        if prior is not None:
            if any(prior[key] != budget[key] for key in ("device", "type", "total_bytes", "allocation_unit", "total_inodes")):
                raise ValueError()
            budget["available_bytes"] = min(prior["available_bytes"], budget["available_bytes"])
            budget["available_inodes"] = min(prior["available_inodes"], budget["available_inodes"])
        if devices.get(budget["device"], budget["id"]) != budget["id"]:
            raise ValueError()
        budgets[budget["id"]], devices[budget["device"]] = budget, budget["id"]
        selected.append(item)
        chains.append(chain)
    if identity != fs_identity() or raw != fs_read("/proc/self/mountinfo", 1048576):
        raise ValueError()
    receipt = hashlib.sha256(raw + b"\x00" + json.dumps(chains, separators=(",", ":")).encode("ascii")).hexdigest()
    evidence = {"mount_namespace": identity[2], "receipt": receipt, "paths": selected, "filesystems": [budgets[key] for key in sorted(budgets)]}
    return {"version": 3, "machine_id": identity[0], "boot_id": identity[1], "evidence": evidence}

def observe_filesystems(paths):
    if type(paths) is not list or not 1 <= len(paths) <= 8 or any(type(item) is not str for item in paths) or paths != sorted(set(paths)):
        raise ValueError()
    for path in paths:
        fs_path(path)
    first, second = fs_snapshot(paths), fs_snapshot(paths)
    if len(first["evidence"]["filesystems"]) != len(second["evidence"]["filesystems"]):
        raise ValueError()
    for left, right in zip(first["evidence"]["filesystems"], second["evidence"]["filesystems"]):
        available = min(left["available_bytes"], right["available_bytes"])
        left["available_bytes"] = right["available_bytes"] = available
        left["available_inodes"] = right["available_inodes"] = min(left["available_inodes"], right["available_inodes"])
    if first != second:
        raise ValueError()
    return first
`
