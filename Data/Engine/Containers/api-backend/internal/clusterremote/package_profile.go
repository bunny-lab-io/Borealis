package clusterremote

// Both pinned SSH observations run this program locally on the target. Only
// the digest crosses SSH. In particular apt-config output can contain private
// proxy settings. No hook, package install, service activation or reload runs.
const packageProfileLibraryScript = `
PROFILE_FILES = ("etc/apt/apt.conf", "etc/dpkg/dpkg.cfg", "etc/needrestart/needrestart.conf",
                 "etc/default/needrestart", "etc/default/nfs-common", "etc/default/rpcbind",
                 "etc/nfs.conf", "etc/idmapd.conf")
PROFILE_DIRS = ("etc/apt/apt.conf.d", "etc/dpkg/dpkg.cfg.d", "etc/needrestart/conf.d", "etc/nfs.conf.d")
PROFILE_UNITS = ("nfs-utils.service", "rpc-statd.service", "nfs-common.service", "rpc-statd-notify.service",
                 "rpcbind.target", "rpc-svcgssd.service", "proc-fs-nfsd.mount", "auth-rpcgss-module.service",
                 "nfs-server.service", "nfs-mountd.service", "nfs-kernel-server.service",
                 "rpc-gssd.service", "nfs-client.target", "nfs-idmapd.service", "rpcbind.service", "rpcbind.socket")

def package_install_profile(root):
    files, directories = {}, {}
    remaining = 1 << 20
    def fingerprint(fd, name):
        nonlocal remaining
        before = os.stat(name, dir_fd=fd, follow_symlinks=False)
        checked(before)
        if before.st_size > remaining:
            raise ValueError()
        identity, raw = read_file(fd, name)
        after = os.stat(name, dir_fd=fd, follow_symlinks=False)
        if ((before.st_dev, before.st_ino, before.st_ctime_ns) != (after.st_dev, after.st_ino, after.st_ctime_ns)
                or (after.st_dev, after.st_ino, after.st_mode, after.st_uid, after.st_nlink,
                    after.st_size, after.st_mtime_ns, after.st_ctime_ns) != identity or len(raw) != after.st_size):
            raise ValueError()
        remaining -= len(raw)
        return [after.st_mode, after.st_uid, after.st_gid, hashlib.sha256(raw).hexdigest()]
    for path in PROFILE_FILES:
        parent, name = path.rsplit("/", 1)
        try:
            fd = directory(root, parent)
        except FileNotFoundError:
            files[path] = None
            continue
        try:
            try:
                os.stat(name, dir_fd=fd, follow_symlinks=False)
            except FileNotFoundError:
                files[path] = None
            else:
                files[path] = fingerprint(fd, name)
        finally:
            os.close(fd)
    for path in PROFILE_DIRS:
        try:
            fd = directory(root, path)
        except FileNotFoundError:
            directories[path] = None
            continue
        try:
            entries = {}
            with os.scandir(fd) as names:
                for entry in names:
                    if len(entries) >= 128:
                        raise ValueError()
                    entries[entry.name] = fingerprint(fd, entry.name)
            directories[path] = entries
        finally:
            os.close(fd)
    try:
        fd = directory(root, "var/lib/nfs")
    except FileNotFoundError:
        nfs = "absent"
    else:
        try:
            with os.scandir(fd) as entries:
                nfs = "empty" if next(entries, None) is None else "populated"
        finally:
            os.close(fd)
    apt = bounded_command(["/usr/bin/apt-config", "dump"])
    if not apt or not apt.endswith(b"\n") or b"\x00" in apt:
        raise ValueError()
    reply = bus_reply("org.freedesktop.systemd1", "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager",
                      "ListUnitsByPatterns", "asas", "0", str(len(PROFILE_UNITS)), *PROFILE_UNITS)
    rows = reply["data"][0]
    if reply["type"] != "a(ssssssouso)" or type(rows) is not list or len(rows) > len(PROFILE_UNITS):
        raise ValueError()
    units = {}
    for row in rows:
        if (type(row) is not list or len(row) != 10 or type(row[0]) is not str or row[0] not in PROFILE_UNITS
                or row[0] in units or any(type(x) is not str or not x for x in row[2:5])
                or type(row[5]) is not str or type(row[7]) is not int or row[7] != 0):
            raise ValueError()
        units[row[0]] = row[2:6]
    raw = json.dumps([files, directories, hashlib.sha256(apt).hexdigest(), nfs, units],
                     separators=(",", ":"), sort_keys=True, ensure_ascii=True).encode("ascii")
    return hashlib.sha256(b"borealis-package-install-profile-v1\n" + raw).hexdigest()
`
