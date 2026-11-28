package clusterbootstrap

// PreparationTargetCPUObserverLibrary is shared by the pinned SSH proposal
// observer and the native pre-mutation check. It only reads fixed host paths.
// The caller supplies safe bounded read/list
// functions; no caller-selected path, setting or resource value enters.
// cAdvisor v0.56.2 counts online logical CPUs through sysfs (utils/sysinfo).
// Require every present CPU online, covering its non-NUMA fallback too. This
// deliberately supports unconstrained hosts only: quotas, restricted cpusets,
// affinity, memory limits and unknown topology fail instead of guessing capacity.
const PreparationTargetCPUObserverLibrary = `
def cpu_list(raw):
    require(type(raw) is bytes and 0 < len(raw) <= 32768 and raw.endswith(b"\n"))
    value = raw[:-1].decode("ascii")
    require(re.fullmatch(r"(?:0|[1-9][0-9]{0,3})(?:-(?:0|[1-9][0-9]{0,3}))?(?:,(?:0|[1-9][0-9]{0,3})(?:-(?:0|[1-9][0-9]{0,3}))?)*", value))
    cpus = []
    previous = -2
    for item in value.split(","):
        ends = item.split("-")
        first, last = int(ends[0]), int(ends[-1])
        require(first <= last < 4096 and (len(ends) == 1 or first < last) and first > previous + 1)
        cpus.extend(range(first, last + 1))
        previous = last
    require(0 < len(cpus) <= 4096)
    return value, cpus

def cpu_status(raw):
    require(type(raw) is bytes and len(raw) <= 65536 and raw.endswith(b"\n"))
    rows = [row.split(b":", 1)[1].strip() for row in raw.splitlines() if row.startswith(b"Cpus_allowed_list:")]
    require(len(rows) == 1)
    return (cpu_list(rows[0] + b"\n")[0] + "\n").encode("ascii")

def cpu_memtotal(raw):
    require(type(raw) is bytes and 0 < len(raw) <= 65536 and raw.endswith(b"\n"))
    rows = [row for row in raw.split(b"\n") if row.startswith(b"MemTotal:")]
    require(len(rows) == 1)
    match = re.fullmatch(rb"MemTotal:[ \t]+([1-9][0-9]{0,15}) kB", rows[0])
    require(match is not None)
    require(int(match[1]) <= ((1 << 53) - 1) // 1024)
    return b"MemTotal: " + match[1] + b" kB\n"

def cpu_controllers(raw):
    require(type(raw) is bytes and 0 < len(raw) <= 4096 and raw.endswith(b"\n"))
    value = raw[:-1].decode("ascii")
    require(not value or re.fullmatch(r"[a-z][a-z0-9_]*(?: [a-z][a-z0-9_]*)*", value))
    names = value.split(" ") if value else []
    require(len(names) == len(set(names)))
    return set(names)

def preparation_target_cpu(read, entries):
    memory_kib = int(cpu_memtotal(read("proc/meminfo", 65536, cpu_memtotal)).split()[1])
    online, cpus = cpu_list(read("sys/devices/system/cpu/online", 32768))
    require(cpu_list(read("sys/devices/system/cpu/present", 32768))[0] == online)
    present = entries("sys/devices/system/cpu", 8192)
    numbered = sorted(int(name[3:]) for name in present if re.fullmatch(r"cpu(?:0|[1-9][0-9]{0,3})", name))
    require(numbered == cpus)
    # NUMA node CPU lists partition the same logical CPUs. An absent node
    # directory uses cAdvisor's all-present-online fallback, without guessing.
    nodes = entries("sys/devices/system/node", 4096, True)
    topology = []
    memory_nodes = "0"
    if nodes is not None:
        memory_nodes = cpu_list(read("sys/devices/system/node/has_memory", 32768))[0]
        numbered_nodes = sorted(name for name in nodes if re.fullmatch(r"node(?:0|[1-9][0-9]{0,3})", name))
        require(numbered_nodes)
        found = []
        for name in numbered_nodes:
            mask, members = cpu_list(read("sys/devices/system/node/" + name + "/cpulist", 32768))
            topology.append([name, mask])
            found.extend(members)
        require(sorted(found) == cpus)
    require(read("proc/1/cgroup", 4096) == b"0::/init.scope\n")
    require(cpu_status(read("proc/1/status", 65536, cpu_status)).decode("ascii") == online + "\n")
    mountinfo = read("proc/1/mountinfo", 1 << 20, cpu_mount)
    cpu_mount(mountinfo)
    root = "sys/fs/cgroup"
    available = cpu_controllers(read(root + "/cgroup.controllers", 4096))
    enabled = cpu_controllers(read(root + "/cgroup.subtree_control", 4096))
    require({"cpu", "cpuset", "memory"} <= available and enabled <= available)
    require(cpu_list(read(root + "/cpuset.cpus.effective", 32768))[0] == online)
    require(cpu_list(read(root + "/cpuset.mems.effective", 32768))[0] == memory_nodes)
    for path in (root + "/system.slice", root + "/init.scope"):
        names = entries(path, 4096)
        # Disabled root controllers create no child interface. Prove absence
        # through the bounded directory listing; a failed read is not absence.
        # The child then inherits the unrestricted root CPU resources.
        require(("cpuset.cpus.effective" in names) == ("cpuset" in enabled))
        require(("cpuset.mems.effective" in names) == ("cpuset" in enabled))
        require(("cpu.max" in names) == ("cpu" in enabled))
        for name in ("memory.max", "memory.high"):
            require((name in names) == ("memory" in enabled))
            if "memory" in enabled:
                require(read(path + "/" + name, 128) == b"max\n")
        if "cpuset" in enabled:
            require(cpu_list(read(path + "/cpuset.cpus.effective", 32768))[0] == online)
            require(cpu_list(read(path + "/cpuset.mems.effective", 32768))[0] == memory_nodes)
        if "cpu" in enabled:
            quota = read(path + "/cpu.max", 128)
            require(re.fullmatch(rb"max [1-9][0-9]{0,8}\n", quota))
    # PID1's kernel affinity is systemd's default child affinity. The native
    # installer separately proves its fixed K3s unit and absence of drop-ins;
    # that unit specifies neither an affinity nor a quota/cpuset override.
    return {"online_cpus": online, "allocatable_milli": len(cpus) * 1000,
        "memory_kib": memory_kib, "memory_nodes": memory_nodes, "topology": topology, "pid1_affinity": online, "unconstrained": True}

def cpu_mount(raw):
    require(type(raw) is bytes and len(raw) <= 1 << 20 and raw.endswith(b"\n"))
    selected = []
    for row in raw.decode("ascii").splitlines():
        fields = row.split()
        require(len(fields) >= 10 and "-" in fields)
        if fields[4] == "/sys/fs/cgroup":
            split = fields.index("-")
            require(fields[3] == "/" and fields[split + 1] == "cgroup2")
            selected.append(row)
        require(not fields[4].startswith("/sys/fs/cgroup/"))
    require(len(selected) == 1)
    return (selected[0] + "\n").encode("ascii")
`
