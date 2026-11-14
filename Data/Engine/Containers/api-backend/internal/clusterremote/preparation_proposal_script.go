package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"encoding/json"
)

// This fixed observer runs using the target's existing Python. It never imports
// a helper, writes a file, repairs packages/accounts or invokes Engine/K3s.
// Native fresh-host contracts live in bootstrap_docker_preflight.go,
// bootstrap_host_prepare.go and bootstrap_runtime_account.go. This proposal
// observation is deliberately separate from their claimed mutation executor.
var preparationProposalScript = preparationProposalLibraryScript + clusterbootstrap.PreparationTargetCPUObserverLibrary + `
if __name__ == "__main__":
    main()
`

// This fixed program embeds only compiled pin identity, never caller selections.
func preparationProposalPackageConstants() string {
	plan, digest, err := clusterbootstrap.HostPackagePins()
	if err != nil {
		return "raise ValueError()\n"
	}
	pins := make([][2]string, 0, len(plan.Packages))
	for _, pin := range plan.Packages {
		pins = append(pins, [2]string{pin.Name, pin.Architecture})
	}
	raw, err := json.Marshal(pins)
	if err != nil {
		return "raise ValueError()\n"
	}
	suite, _ := json.Marshal(plan.Suite)
	return "HOST_PACKAGE_PLAN_SHA256 = \"" + digest + "\"\nHOST_PACKAGE_SUITE = " + string(suite) + "\nHOST_PACKAGE_PINS = " + string(raw) + "\n"
}

var preparationProposalLibraryScript = `import base64, hashlib, json, os, re, resource, selectors, stat, subprocess, sys, time
` + preparationProposalPackageConstants() + `

ROOT = "/"
EXPECTED_UID = 0
EXPECTED_GID = 0
LIMIT = 2 << 20
ACCOUNT_LIMIT = 4 << 20
UNIT_PROPERTIES = "LoadState,ActiveState,SubState,NeedDaemonReload,Job"
SOURCE_UNIT_PROPERTIES = "LoadState,ActiveState,SubState,MainPID,ControlPID,NeedDaemonReload,Job"
ABSENT_PATHS = (
    "opt/Borealis", "etc/borealis", "etc/rancher/k3s", "var/lib/rancher/k3s", "usr/local/bin/k3s", "var/lib/kubelet", "etc/kubernetes",
    "etc/docker", "etc/containerd", "var/lib/docker", "var/lib/containerd", "run/docker", "run/docker.sock", "run/containerd",
    "usr/bin/docker", "usr/bin/dockerd", "usr/bin/containerd", "usr/bin/runc",
    "usr/local/bin/docker", "usr/local/bin/dockerd", "usr/local/bin/containerd", "usr/local/bin/runc",
    "etc/apt/keyrings/docker.gpg", "etc/apt/trusted.gpg.d/docker.asc", "etc/apt/trusted.gpg.d/docker.gpg",
    "etc/apt/keyrings/docker.asc", "etc/apt/sources.list.d/docker.sources",
    "usr/local/lib/borealis/k3s-api-firewall.sh", "etc/systemd/system/borealis-k3s-api-firewall.service")
UNITS = ("docker.service", "docker.socket", "containerd.service", "k3s.service", "k3s-agent.service", "borealis-node-manager.service", "borealis-k3s-api-firewall.service")

def require(condition):
    if not condition:
        raise ValueError()

def digest(raw):
    return hashlib.sha256(raw).hexdigest()

def canonical(value):
    return json.dumps(value, separators=(",", ":"), sort_keys=True, ensure_ascii=True).encode("ascii")

def json_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result)
        result[key] = value
    return result

def invalid_constant(value):
    raise ValueError()

def metadata(info, directory=False):
    value = [info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_gid]
    if not directory:
        value += [info.st_nlink, info.st_size, info.st_mtime_ns, info.st_ctime_ns]
    return value

def safe(info, directory=False):
    require(info.st_uid == EXPECTED_UID and info.st_gid == EXPECTED_GID and not info.st_mode & 0o022)
    require(stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode) and info.st_nlink == 1)
    require(not info.st_mode & 0o7000)

class Snapshot:
    def __init__(self):
        self.root = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
        safe(os.fstat(self.root), True)
        self.parents = {"": metadata(os.fstat(self.root), True)}
        self.files = {}
        self.absent = []
        self.listings = {}

    def directory(self, path):
        fd = os.dup(self.root)
        current = ""
        try:
            for part in path.split("/") if path else ():
                require(part not in ("", ".", ".."))
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=fd)
                os.close(fd)
                fd = child
                info = os.fstat(fd)
                safe(info, True)
                current = current + "/" + part if current else part
                identity = metadata(info, True)
                require(current not in self.parents or self.parents[current] == identity)
                self.parents[current] = identity
            return fd
        except BaseException:
            os.close(fd)
            raise

    def absent_path(self, path):
        parent, leaf = path.rsplit("/", 1)
        try:
            fd = self.directory(parent)
        except FileNotFoundError:
            self.absent.append(path)
            return True
        try:
            try:
                os.stat(leaf, dir_fd=fd, follow_symlinks=False)
                return False
            except FileNotFoundError:
                self.absent.append(path)
                return True
        finally:
            os.close(fd)

    def read(self, path, limit, projection=None):
        parent, leaf = path.rsplit("/", 1)
        directory = self.directory(parent)
        try:
            before = os.stat(leaf, dir_fd=directory, follow_symlinks=False)
            safe(before)
            require(before.st_size <= limit)
            fd = os.open(leaf, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC, dir_fd=directory)
            with os.fdopen(fd, "rb") as source:
                require(metadata(os.fstat(source.fileno())) == metadata(before))
                raw = source.read(limit + 1)
                require(len(raw) <= limit and metadata(os.fstat(source.fileno())) == metadata(before))
            require(metadata(os.stat(leaf, dir_fd=directory, follow_symlinks=False)) == metadata(before))
            value = [metadata(before), digest(projection(raw) if projection else raw)]
            require(path not in self.files or self.files[path] == value)
            self.files[path] = value
            return raw
        finally:
            os.close(directory)

    def entries(self, path, limit):
        try:
            fd = self.directory(path)
        except FileNotFoundError:
            self.listings[path] = None
            return []
        try:
            names = []
            with os.scandir(fd) as entries:
                for entry in entries:
                    names.append(entry.name)
                    require(len(names) <= limit)
            names.sort()
            self.listings[path] = names
            return names
        finally:
            os.close(fd)

def command(args, limit=LIMIT, seconds=3):
    # Fixed argv only. Read incrementally, kill and join on timeout/overflow.
    process = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        cwd="/", env={"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL": "C"})
    output = bytearray()
    selector = selectors.DefaultSelector()
    try:
        selector.register(process.stdout, selectors.EVENT_READ)
        deadline = time.monotonic() + seconds
        while True:
            remaining = deadline - time.monotonic()
            require(remaining > 0 and selector.select(remaining))
            part = os.read(process.stdout.fileno(), 65536)
            if not part:
                break
            output.extend(part)
            require(len(output) <= limit)
        require(process.wait(timeout=max(0.001, deadline - time.monotonic())) == 0)
        return bytes(output)
    finally:
        selector.close()
        if process.poll() is None:
            process.kill()
        process.wait()
        process.stdout.close()

def text(raw, limit, newline=True):
    require(0 < len(raw) <= limit and b"\x00" not in raw and (not newline or raw.endswith(b"\n")))
    return raw.decode("utf-8", "strict")

def platform(snapshot):
    parent = snapshot.directory("etc")
    try:
        info = os.stat("os-release", dir_fd=parent, follow_symlinks=False)
        path, link = "etc/os-release", ""
        if stat.S_ISLNK(info.st_mode):
            link = os.readlink("os-release", dir_fd=parent)
            require(link == "../usr/lib/os-release" and info.st_uid == EXPECTED_UID and info.st_gid == EXPECTED_GID)
            path = "usr/lib/os-release"
            snapshot.files["etc/os-release"] = [metadata(info), link]
    finally:
        os.close(parent)
    values = {}
    for line in text(snapshot.read(path, 16384), 16384).splitlines():
        if not line or line.startswith("#"):
            continue
        key, sep, value = line.partition("=")
        require(sep and key and key not in values)
        if len(value) >= 2 and value[0] in ("'", '"') and value[-1] == value[0]:
            value = value[1:-1]
        values[key] = value
    version, suite = values.get("VERSION_ID", ""), values.get("VERSION_CODENAME", "")
    require(values.get("ID") == "ubuntu" and re.fullmatch(r"[0-9]{2}\.(04|10)", version))
    require(tuple(map(int, version.split("."))) >= (24, 4) and re.fullmatch(r"[a-z][a-z0-9]{0,31}", suite))
    require(values.get("UBUNTU_CODENAME", suite) in ("", suite))
    require(command(["/usr/bin/dpkg", "--print-architecture"]) == b"amd64\n")
    return suite

def packages(suite):
    require(suite == HOST_PACKAGE_SUITE)
    raw = command(["/usr/bin/dpkg-query", "--show", "--showformat=${Package}\t${Architecture}\t${Status}\t${Version}\n"])
    seen, required, states = set(), {"apt": False, "dpkg": False, "base-files": False}, {}
    for line in text(raw, LIMIT)[:-1].split("\n"):
        fields = line.split("\t")
        require(len(fields) == 4)
        name, arch, state, version = fields
        require(re.fullmatch(r"[a-z0-9][a-z0-9+.-]{0,199}", name) and re.fullmatch(r"[a-z0-9][a-z0-9+.-]{0,199}", arch))
        require((name, arch) not in seen and re.fullmatch(r"[0-9][A-Za-z0-9.+:~\-]{0,199}", version))
        require(state in ("install ok installed", "deinstall ok config-files"))
        require(not name.startswith(("docker", "containerd")) and name not in ("runc", "podman-docker"))
        seen.add((name, arch))
        states.setdefault(name, []).append((arch, state))
        if name in required:
            require(not required[name] and state == "install ok installed" and arch in ("amd64", "all"))
            required[name] = True
    require(all(required.values()))
    missing = []
    for name, architecture in HOST_PACKAGE_PINS:
        entries = states.get(name, [])
        require(not entries or entries == [(architecture, "install ok installed")])
        if not entries:
            missing.append(name)
    return {"plan_sha256": HOST_PACKAGE_PLAN_SHA256, "inventory_sha256": digest(raw), "missing": sorted(missing)}

def sources(snapshot):
    paths = []
    if not snapshot.absent_path("etc/apt/sources.list"):
        paths.append("etc/apt/sources.list")
    for name in snapshot.entries("etc/apt/sources.list.d", 128):
        require(not name.startswith(".borealis-docker-"))
        paths.append("etc/apt/sources.list.d/" + name)
    for name in snapshot.entries("etc/apt/keyrings", 128):
        require(not name.startswith(".borealis-docker-"))
    active, total = False, 0
    for path in paths:
        raw = snapshot.read(path, 65536)
        total += len(raw)
        require(total <= LIMIT and not any(value in raw.lower() for value in (b"download.docker.com", b"docker.asc", b"docker.gpg")))
        if raw.strip() and path.endswith((".list", ".sources")):
            active = True
    require(active)
    # No reservation-owned pending filename can be derived from a fake future
    # claim. Fresh proposals reject every retained native publication prefix.
    for path in ("etc/rancher/k3s/config.yaml.d", "etc/rancher/k3s", "usr/local/lib/borealis", "etc/systemd/system"):
        for name in snapshot.entries(path, 32768):
            require(not name.startswith(".borealis-host-"))

def units():
    result = {}
    for name in UNITS:
        expected = {"LoadState": "not-found", "ActiveState": "inactive", "SubState": "dead", "NeedDaemonReload": "no", "Job": ""}
        properties, limit, seconds = UNIT_PROPERTIES, LIMIT, 3
        if name not in UNITS[:3]:
            expected.update({"MainPID": "0", "ControlPID": "0"})
            properties, limit, seconds = SOURCE_UNIT_PROPERTIES, 4096, 2
        raw = command(["/usr/bin/systemctl", "show", "--no-pager", "--property=" + properties, "--", name], limit, seconds)
        values = {}
        for line in text(raw, limit)[:-1].split("\n"):
            key, sep, value = line.partition("=")
            require(sep and key not in values)
            values[key] = value
        require(values == expected)
        result[name] = values
    return result

def supervision(snapshot):
    # Observe existing prerequisites only. The claimed immutable helper proves
    # its exact watchdog/cgroup properties before any journaled host effect.
    # Never launch a transient unit, create a journal or claim a worker here.
    require(snapshot.read("proc/1/comm", 64) == b"systemd\n")
    require(snapshot.read("proc/1/cgroup", 4096) == b"0::/init.scope\n")
    pid = snapshot.directory("proc/1")
    try:
        info = os.stat("exe", dir_fd=pid, follow_symlinks=False)
        require(stat.S_ISLNK(info.st_mode) and info.st_uid == EXPECTED_UID and info.st_gid == EXPECTED_GID)
        executable = os.readlink("exe", dir_fd=pid)
        require(executable in ("/usr/lib/systemd/systemd", "/lib/systemd/systemd"))
        snapshot.files["proc/1/exe"] = [metadata(info), executable]
    finally:
        os.close(pid)
    manager = command(["/usr/bin/systemctl", "show", "--no-pager", "--property=Version,Features"])
    values = {}
    for line in text(manager, 16384)[:-1].split("\n"):
        key, sep, value = line.partition("=")
        require(sep and key not in values)
        values[key] = value
    require(set(values) == {"Version", "Features"} and re.fullmatch(r"[0-9][A-Za-z0-9.+:~\-]{0,199}", values["Version"]))
    require(values["Features"] and len(values["Features"]) <= 8192)
    cli = command(["/usr/bin/systemd-run", "--version"], 16384)
    require(re.fullmatch(r"systemd [0-9]{1,6} .+", text(cli, 16384).splitlines()[0]))
    version = cli.split(b" ", 2)[1].decode("ascii")
    require(values["Version"].split(".", 1)[0].split("-", 1)[0] == version)
    # Kernel mount identity, not the existence of an ordinary lookalike file,
    # establishes the unified hierarchy. No nested/rebound cgroup mount is used.
    mountinfo = snapshot.read("proc/1/mountinfo", LIMIT)
    mounts = []
    for line in text(mountinfo, LIMIT).splitlines():
        fields = line.split(" ")
        require(len(fields) >= 10 and fields.count("-") == 1)
        separator = fields.index("-")
        require(separator >= 6 and len(fields) == separator + 4)
        if fields[4] == "/sys/fs/cgroup":
            require(fields[3] == "/" and fields[separator + 1] == "cgroup2")
            mounts.append(line)
        require(not fields[4].startswith("/sys/fs/cgroup/"))
    require(len(mounts) == 1)
    controllers = text(snapshot.read("sys/fs/cgroup/cgroup.controllers", 4096), 4096).split()
    require(len(controllers) == len(set(controllers)) and all(re.fullmatch(r"[a-z][a-z0-9_]{0,63}", value) for value in controllers))
    require({"cpu", "memory", "pids"}.issubset(controllers))
    require(snapshot.absent_path("var/lib/borealis/cluster-bootstrap"))
    require(command(["/usr/bin/systemctl", "list-units", "--all", "--type=service", "--no-legend", "--plain", "borealis-bootstrap-*.service"], 16384) == b"")
    cgroups = snapshot.directory("sys/fs/cgroup/system.slice")
    os.close(cgroups)
    for path in ("sys/fs/cgroup/system.slice", "run/systemd/transient"):
        require(not any(name.startswith("borealis-bootstrap-") for name in snapshot.entries(path, 32768)))
    return {"manager": values, "runner": digest(cli), "mount": mounts[0], "controllers": controllers,
        "journal_absent": True, "bootstrap_producers_absent": True}

def integer(value):
    require(re.fullmatch(r"0|[1-9][0-9]{0,9}", value) and int(value) <= 4294967295)
    return int(value)

def runtime_uid_absent(snapshot):
    # Process churn is allowed; the positive assertion is absence of all four
    # UID64646 credentials. No PID/account rows leave the target.
    proc = snapshot.directory("proc")
    try:
        entries = []
        with os.scandir(proc) as iterator:
            for entry in iterator:
                entries.append(entry.name)
                require(len(entries) <= 32768)
        for name in entries:
            if not re.fullmatch(r"[1-9][0-9]{0,9}", name) or int(name) > 4294967295:
                continue
            try:
                directory = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=proc)
                try:
                    fd = os.open("status", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC, dir_fd=directory)
                finally:
                    os.close(directory)
            except FileNotFoundError:
                continue
            with os.fdopen(fd, "rb") as source:
                require(stat.S_ISREG(os.fstat(source.fileno()).st_mode))
                raw = source.read(65537)
            require(len(raw) <= 65536)
            rows = [line[4:].split() for line in text(raw, 65536).splitlines() if line.startswith("Uid:")]
            require(len(rows) == 1 and len(rows[0]) == 4)
            require(all(integer(value) != 64646 for value in rows[0]))
    finally:
        os.close(proc)

def accounts(snapshot):
    nss = snapshot.read("etc/nsswitch.conf", 65536)
    seen = set()
    for line in text(nss, 65536, False).splitlines():
        key, sep, value = line.split("#", 1)[0].partition(":")
        key = key.strip()
        if key not in ("passwd", "group"):
            continue
        require(sep and key not in seen and value.split() in (["files"], ["files", "systemd"]))
        seen.add(key)
    require(len(seen) == 2)
    passwd = command(["/usr/bin/getent", "passwd"], ACCOUNT_LIMIT, 2)
    groups = command(["/usr/bin/getent", "group"], ACCOUNT_LIMIT, 2)
    user, group = False, False
    for line in text(passwd, ACCOUNT_LIMIT)[:-1].split("\n"):
        fields = line.split(":")
        require(len(fields) == 7 and len(line.encode("utf-8")) <= 4096)
        uid, gid = integer(fields[2]), integer(fields[3])
        if fields[0] != "borealis-engine" and uid != 64646:
            require(gid != 64646)
            continue
        require(not user and fields[0] == "borealis-engine" and uid == gid == 64646 and fields[5] == "/nonexistent")
        require(fields[6] in ("/usr/sbin/nologin", "/sbin/nologin", "/bin/false"))
        user = True
    for line in text(groups, ACCOUNT_LIMIT)[:-1].split("\n"):
        fields = line.split(":")
        require(len(fields) == 4 and len(line.encode("utf-8")) <= 4096)
        gid = integer(fields[2])
        if fields[0] != "borealis-engine" and gid != 64646:
            require("borealis-engine" not in fields[3].split(","))
            continue
        require(not group and fields[0] == "borealis-engine" and gid == 64646 and fields[3] == "")
        group = True
    runtime_uid_absent(snapshot)
    require(snapshot.read("etc/nsswitch.conf", 65536) == nss)
    # Digests cover the complete local public-account inventory without exporting
    # names, password columns, IDs, homes or supplementary membership rows.
    return [digest(passwd), digest(groups), user, group]

def host_identity(snapshot, proposal):
    machine = text(snapshot.read("etc/machine-id", 64), 64)
    boot = text(snapshot.read("proc/sys/kernel/random/boot_id", 64), 64)
    hostname = text(snapshot.read("proc/sys/kernel/hostname", 64), 64)
    require((machine, boot, hostname) == (proposal["machine_id"] + "\n", proposal["boot_id"] + "\n", proposal["hostname"] + "\n"))
    raw = command(["/usr/sbin/ip", "-j", "-4", "address", "show"])
    addresses = json.loads(text(raw, LIMIT, False), object_pairs_hook=json_object, parse_constant=invalid_constant)
    require(type(addresses) is list and 0 < len(addresses) <= 64)
    found = []
    for interface in addresses:
        require(type(interface) is dict and type(interface.get("addr_info")) is list and len(interface["addr_info"]) <= 64)
        require(type(interface.get("flags")) is list and all(type(flag) is str for flag in interface["flags"]))
        for address in interface["addr_info"]:
            require(type(address) is dict)
            if address.get("local") == proposal["address"]:
                require(address.get("family") == "inet" and "UP" in interface.get("flags", []) and not address.get("tentative", False) and not address.get("dadfailed", False))
                require(type(interface.get("ifname")) is str and re.fullmatch(r"[A-Za-z0-9_.:-]{1,15}", interface["ifname"]))
                require(type(interface.get("ifindex")) is int and interface["ifindex"] > 0 and type(address.get("prefixlen")) is int and 1 <= address["prefixlen"] <= 32)
                found.append([interface["ifindex"], interface["ifname"], address["local"], address["prefixlen"]])
    require(len(found) == 1)
    return [machine, boot, hostname, found]

def observe(proposal):
    snapshot = Snapshot()
    try:
        identity = host_identity(snapshot, proposal)
        suite = platform(snapshot)
        for path in ABSENT_PATHS:
            require(snapshot.absent_path(path))
        sources(snapshot)
        package_selection = packages(suite)
        unit_state = units()
        supervision_state = supervision(snapshot)
        def cpu_read(path, limit, projection=None):
            # Supervision retains the complete mount table. Use that same
            # observation here; the shared CPU reader still validates cgroup
            # mounts in the returned bytes. Other CPU projections stay intact.
            if path == "proc/1/mountinfo":
                projection = None
            return snapshot.read(path, limit, projection)
        def cpu_entries(path, limit, optional=False):
            if optional and snapshot.absent_path(path):
                return None
            return snapshot.entries(path, limit)
        cpu_state = preparation_target_cpu(cpu_read, cpu_entries)
        account_state = accounts(snapshot)
        # Reopen every observed ancestor to detect replacement during this pass.
        for path, identity_before in list(snapshot.parents.items()):
            fd = snapshot.directory(path)
            try:
                require(metadata(os.fstat(fd), True) == identity_before)
            finally:
                os.close(fd)
        return {"identity": identity, "suite": suite, "parents": snapshot.parents, "files": snapshot.files,
            "absent": snapshot.absent, "listings": snapshot.listings, "packages": package_selection,
            "units": unit_state, "supervision": supervision_state, "cpu": cpu_state, "accounts": account_state, "runtime_uid_absent": True}
    finally:
        os.close(snapshot.root)

def main():
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_AS, (256 << 20, 256 << 20))
    resource.setrlimit(resource.RLIMIT_CPU, (12, 12))
    require(os.geteuid() == EXPECTED_UID and len(sys.argv) == 2 and 0 < len(sys.argv[1]) <= 8192)
    encoded = sys.argv[1]
    raw = base64.b64decode(encoded + "=" * (-len(encoded) % 4), validate=True)
    proposal = json.loads(raw)
    require(type(proposal) is dict and type(proposal.get("version")) is int and proposal["version"] == 4)
    before = observe(proposal)
    after = observe(proposal)
    require(before == after)
    observation = digest(b"borealis-preparation-native-observation-v4\n" + canonical(before))
    request = digest(b"borealis-preparation-proposal-v4\n" + raw)
    cpu = {"version": 1, "plan_sha256": proposal["cpu_plan_sha256"], "online_cpus": before["cpu"]["online_cpus"],
        "allocatable_milli": before["cpu"]["allocatable_milli"], "memory_kib": before["cpu"]["memory_kib"], "observation_sha256": digest(b"borealis-preparation-target-cpu-v1\n" + canonical(before["cpu"]))}
    print(json.dumps({"version": 4, "request_sha256": request, "nonce": proposal["nonce"], "observation_sha256": observation, "cpu": cpu, "packages": before["packages"]}, separators=(",", ":")))
`
