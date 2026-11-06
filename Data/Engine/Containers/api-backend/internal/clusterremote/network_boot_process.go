package clusterremote

// Fixed procfs reads bind networkd's present process to its loaded command.
// Historical ExecStart timestamps/PID may be cleared while MainPID survives.
const networkBootProcessLibraryScript = `
def boot_network_process(root, pid, executable):
    if type(pid) is not int or not 1 < pid <= 2147483647 or boot_canonical(executable) != "/usr/lib/systemd/systemd-networkd":
        raise ValueError()
    def identity(info):
        return (info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_gid, info.st_nlink, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    def read_proc(fd, name):
        child = os.open(name, os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK|os.O_CLOEXEC, dir_fd=fd)
        with os.fdopen(child, "rb") as source:
            before = os.fstat(source.fileno())
            if not stat.S_ISREG(before.st_mode):
                raise ValueError()
            data = source.read(4097)
            if len(data) > 4096 or identity(before) != identity(os.fstat(source.fileno())):
                raise ValueError()
            return data
    def start(fd):
        raw = read_proc(fd, "stat").decode("ascii")
        if not raw.startswith(str(pid)+" ("):
            raise ValueError()
        fields = raw[raw.rindex(")")+1:].split()
        if len(fields) < 20 or fields[0] not in ("R", "S", "D") or not re.fullmatch(r"[0-9]{1,20}", fields[19]) or not 0 < int(fields[19]) < 2**64:
            raise ValueError()
        return int(fields[19])
    vendor = directory(root, "usr/lib/systemd")
    try:
        binary = os.open("systemd-networkd", os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK|os.O_CLOEXEC, dir_fd=vendor)
    finally:
        os.close(vendor)
    try:
        info = os.fstat(binary)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != EXPECTED_UID or info.st_mode & 0o022 or not info.st_mode & 0o111 or info.st_nlink != 1:
            raise ValueError()
        binary_id = identity(info)
        proc = directory(root, "proc")
        try:
            # Kernel owns this fixed numeric namespace. Service proc entries
            # belong to its service UID, unlike root-owned configuration files.
            fd = os.open(str(pid), os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW|os.O_CLOEXEC, dir_fd=proc)
        finally:
            os.close(proc)
        try:
            started = start(fd)
            proc_id = os.fstat(fd)
            expected_link = os.path.join(ROOT, "usr/lib/systemd/systemd-networkd")
            if os.readlink("exe", dir_fd=fd) != expected_link or identity(os.stat("exe", dir_fd=fd)) != binary_id:
                raise ValueError()
            # cmdline alone is process-controlled; never substitute it for
            # executable inode, start time and independently held bus ownership.
            command = read_proc(fd, "cmdline")
            if command != executable.encode("ascii")+b"\0":
                raise ValueError()
            if started != start(fd) or command != read_proc(fd, "cmdline") or os.readlink("exe", dir_fd=fd) != expected_link or identity(os.stat("exe", dir_fd=fd)) != binary_id or identity(os.fstat(binary)) != binary_id:
                raise ValueError()
            return (pid, started, proc_id.st_dev, proc_id.st_ino, binary_id, command)
        finally:
            os.close(fd)
    finally:
        os.close(binary)
`
