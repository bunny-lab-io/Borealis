import os
import sys
import json
import time
import socket
import platform
import subprocess
import getpass
import datetime
import shutil
import string

import requests
try:
    import psutil  # type: ignore
except Exception:
    psutil = None  # graceful degradation if unavailable
import aiohttp
import asyncio

# ---------------- Helpers for hidden subprocess on Windows ----------------
IS_WINDOWS = os.name == 'nt'
CREATE_NO_WINDOW = 0x08000000 if IS_WINDOWS else 0


def _run_hidden(cmd_list, timeout=None):
    """Run a subprocess hidden on Windows (no visible console window)."""
    kwargs = {"capture_output": True, "text": True}
    if timeout is not None:
        kwargs["timeout"] = timeout
    if IS_WINDOWS:
        kwargs["creationflags"] = CREATE_NO_WINDOW
    return subprocess.run(cmd_list, **kwargs)


def _run_powershell_hidden(ps_cmd: str, timeout: int = 60):
    """Run a powershell -NoProfile -Command string fully hidden on Windows."""
    ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
    if not os.path.isfile(ps):
        ps = "powershell.exe"
    return _run_hidden([ps, "-NoProfile", "-Command", ps_cmd], timeout=timeout)


def detect_agent_os():
    """
    Detects the full, user-friendly operating system name and version.
    Examples:
      - "Windows 11"
      - "Windows 10"
      - "Fedora Workstation 42"
      - "Ubuntu 22.04 LTS"
      - "macOS Sonoma"
    Falls back to a generic name if detection fails.
    """
    try:
        plat = platform.system().lower()

        if plat.startswith('win'):
            try:
                import winreg  # Only available on Windows

                reg_path = r"SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion"
                access = winreg.KEY_READ
                try:
                    access |= winreg.KEY_WOW64_64KEY
                except Exception:
                    pass

                try:
                    key = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, reg_path, 0, access)
                except OSError:
                    key = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, reg_path, 0, winreg.KEY_READ)

                def _get(name, default=None):
                    try:
                        return winreg.QueryValueEx(key, name)[0]
                    except Exception:
                        return default

                product_name = _get("ProductName", "")  # e.g., "Windows 11 Pro"
                edition_id = _get("EditionID", "")       # e.g., "Professional"
                display_version = _get("DisplayVersion", "")  # e.g., "24H2" / "22H2"
                release_id = _get("ReleaseId", "")            # e.g., "2004" on older Windows 10
                build_number = _get("CurrentBuildNumber", "") or _get("CurrentBuild", "")
                ubr = _get("UBR", None)  # Update Build Revision (int)

                try:
                    build_int = int(str(build_number).split(".")[0]) if build_number else 0
                except Exception:
                    build_int = 0
                if build_int >= 22000:
                    major_label = "11"
                elif build_int >= 10240:
                    major_label = "10"
                else:
                    major_label = platform.release()

                edition = ""
                pn = product_name or ""
                if pn.lower().startswith("windows "):
                    tokens = pn.split()
                    if len(tokens) >= 3:
                        edition = " ".join(tokens[2:])
                if not edition and edition_id:
                    eid_map = {
                        "Professional": "Pro",
                        "ProfessionalN": "Pro N",
                        "ProfessionalEducation": "Pro Education",
                        "ProfessionalWorkstation": "Pro for Workstations",
                        "Enterprise": "Enterprise",
                        "EnterpriseN": "Enterprise N",
                        "EnterpriseS": "Enterprise LTSC",
                        "Education": "Education",
                        "EducationN": "Education N",
                        "Core": "Home",
                        "CoreN": "Home N",
                        "CoreSingleLanguage": "Home Single Language",
                        "IoTEnterprise": "IoT Enterprise",
                    }
                    edition = eid_map.get(edition_id, edition_id)

                os_name = f"Windows {major_label}"
                version_label = display_version or release_id or ""

                if isinstance(ubr, int):
                    build_str = f"{build_number}.{ubr}" if build_number else str(ubr)
                else:
                    try:
                        build_str = f"{build_number}.{int(ubr)}" if build_number and ubr is not None else build_number
                    except Exception:
                        build_str = build_number

                parts = ["Microsoft", os_name]
                if edition:
                    parts.append(edition)
                if version_label:
                    parts.append(version_label)
                if build_str:
                    parts.append(f"Build {build_str}")

                return " ".join(p for p in parts if p).strip()

            except Exception:
                return f"Windows {platform.release()}"

        elif plat.startswith('linux'):
            try:
                import distro  # External package, better for Linux OS detection
                name = distro.name(pretty=True)  # e.g., "Fedora Workstation 42"
                if name:
                    return name
                else:
                    return f"{platform.system()} {platform.release()}"
            except ImportError:
                return f"{platform.system()} {platform.release()}"

        elif plat.startswith('darwin'):
            version = platform.mac_ver()[0]
            macos_names = {
                "14": "Sonoma",
                "13": "Ventura",
                "12": "Monterey",
                "11": "Big Sur",
                "10.15": "Catalina"
            }
            pretty_name = macos_names.get(".".join(version.split(".")[:2]), "")
            return f"macOS {pretty_name or version}"

        else:
            return f"Unknown OS ({platform.system()} {platform.release()})"

    except Exception as e:
        print(f"[WARN] OS detection failed: {e}")
        return "Unknown"


def _detect_virtual_machine() -> bool:
    """Best-effort detection of whether this system is a virtual machine.

    Uses platform-specific signals but avoids heavy dependencies.
    """
    try:
        plat = platform.system().lower()

        if plat == "linux":
            # Prefer systemd-detect-virt if available
            try:
                out = subprocess.run([
                    "systemd-detect-virt", "--vm"
                ], capture_output=True, text=True, timeout=3)
                if out.returncode == 0 and (out.stdout or "").strip():
                    return True
            except Exception:
                pass

            # Fallback to DMI sysfs strings
            for p in (
                "/sys/class/dmi/id/product_name",
                "/sys/class/dmi/id/sys_vendor",
                "/sys/class/dmi/id/board_vendor",
            ):
                try:
                    with open(p, "r", encoding="utf-8", errors="ignore") as fh:
                        s = (fh.read() or "").lower()
                    if any(k in s for k in (
                        "kvm", "vmware", "virtualbox", "qemu", "xen", "hyper-v", "microsoft corporation")):
                        return True
                except Exception:
                    pass

        elif plat == "windows":
            # Inspect model/manufacturer via CIM
            try:
                ps_cmd = (
                    "$cs = Get-CimInstance Win32_ComputerSystem; "
                    "$model = [string]$cs.Model; $manu = [string]$cs.Manufacturer; "
                    "Write-Output ($model + '|' + $manu)"
                )
                out = _run_powershell_hidden(ps_cmd, timeout=6)
                s = (out.stdout or "").strip().lower()
                if any(k in s for k in ("virtual", "vmware", "virtualbox", "kvm", "qemu", "xen", "hyper-v")):
                    return True
            except Exception:
                pass

            # Fallback: registry BIOS strings often include virtualization hints
            try:
                import winreg  # type: ignore
                with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, r"HARDWARE\DESCRIPTION\System") as k:
                    for name in ("SystemBiosVersion", "VideoBiosVersion"):
                        try:
                            val, _ = winreg.QueryValueEx(k, name)
                            s = str(val).lower()
                            if any(x in s for x in ("virtual", "vmware", "virtualbox", "qemu", "xen", "hyper-v")):
                                return True
                        except Exception:
                            continue
            except Exception:
                pass

        elif plat == "darwin":
            # macOS guest detection is tricky; limited heuristic
            try:
                out = subprocess.run([
                    "sysctl", "-n", "machdep.cpu.features"
                ], capture_output=True, text=True, timeout=3)
                s = (out.stdout or "").lower()
                if "vmm" in s:
                    return True
            except Exception:
                pass
    except Exception:
        pass
    return False


def _detect_device_type_non_vm() -> str:
    """Classify non-VM device as Laptop, Desktop, or Server.

    This is intentionally conservative; if unsure, returns 'Desktop'.
    """
    try:
        plat = platform.system().lower()

        if plat == "windows":
            # Prefer PCSystemTypeEx, then chassis types, then battery presence
            try:
                ps_cmd = (
                    "$cs = Get-CimInstance Win32_ComputerSystem; "
                    "$typeEx = [int]($cs.PCSystemTypeEx); "
                    "$type = [int]($cs.PCSystemType); "
                    "$ch = (Get-CimInstance Win32_SystemEnclosure).ChassisTypes; "
                    "$hasBatt = @(Get-CimInstance Win32_Battery).Count -gt 0; "
                    "Write-Output ($typeEx.ToString() + '|' + $type.ToString() + '|' + "
                    "([string]::Join(',', $ch)) + '|' + $hasBatt)"
                )
                out = _run_powershell_hidden(ps_cmd, timeout=6)
                resp = (out.stdout or "").strip()
                parts = resp.split("|")
                type_ex = int(parts[0]) if len(parts) > 0 and parts[0].isdigit() else None
                type_pc = int(parts[1]) if len(parts) > 1 and parts[1].isdigit() else None
                chassis = []
                if len(parts) > 2 and parts[2].strip():
                    for t in parts[2].split(','):
                        t = t.strip()
                        if t.isdigit():
                            chassis.append(int(t))
                has_batt = False
                if len(parts) > 3:
                    has_batt = parts[3].strip().lower() in ("true", "1")

                # PCSystemTypeEx mapping per MS docs
                if type_ex in (4, 5, 7, 8):
                    return "Server"
                if type_ex == 2:
                    return "Laptop"
                if type_ex in (1, 3):
                    return "Desktop"

                # Fallback to PCSystemType
                if type_pc in (4, 5):
                    return "Server"
                if type_pc == 2:
                    return "Laptop"
                if type_pc in (1, 3):
                    return "Desktop"

                # ChassisType mapping (DMTF)
                laptop_types = {8, 9, 10, 14, 30, 31}
                server_types = {17, 23}
                desktop_types = {3, 4, 5, 6, 7, 15, 16, 24, 35}
                if any(ct in laptop_types for ct in chassis):
                    return "Laptop"
                if any(ct in server_types for ct in chassis):
                    return "Server"
                if any(ct in desktop_types for ct in chassis):
                    return "Desktop"

                if has_batt:
                    return "Laptop"
            except Exception:
                pass
            return "Desktop"

        if plat == "linux":
            # hostnamectl exposes chassis when available
            try:
                out = subprocess.run(["hostnamectl"], capture_output=True, text=True, timeout=3)
                for line in (out.stdout or "").splitlines():
                    if ":" in line:
                        k, v = line.split(":", 1)
                        if k.strip().lower() == "chassis":
                            val = v.strip().lower()
                            if "laptop" in val:
                                return "Laptop"
                            if "desktop" in val:
                                return "Desktop"
                            if "server" in val:
                                return "Server"
                            break
            except Exception:
                pass

            # DMI chassis type numeric
            try:
                with open("/sys/class/dmi/id/chassis_type", "r", encoding="utf-8", errors="ignore") as fh:
                    s = (fh.read() or "").strip()
                ct = int(s)
                laptop_types = {8, 9, 10, 14, 30, 31}
                server_types = {17, 23}
                desktop_types = {3, 4, 5, 6, 7, 15, 16, 24, 35}
                if ct in laptop_types:
                    return "Laptop"
                if ct in server_types:
                    return "Server"
                if ct in desktop_types:
                    return "Desktop"
            except Exception:
                pass

            # Battery presence heuristic
            try:
                if os.path.isdir("/sys/class/power_supply"):
                    for name in os.listdir("/sys/class/power_supply"):
                        if name.lower().startswith("bat"):
                            return "Laptop"
            except Exception:
                pass
            return "Desktop"

        if plat == "darwin":
            try:
                out = subprocess.run(["sysctl", "-n", "hw.model"], capture_output=True, text=True, timeout=3)
                model = (out.stdout or "").strip()
                if model:
                    if model.lower().startswith("macbook"):
                        return "Laptop"
                    # iMac, Macmini, MacPro -> treat as Desktop
                    return "Desktop"
            except Exception:
                pass
            return "Desktop"
    except Exception:
        pass
    return "Desktop"


def detect_device_type() -> str:
    """Return one of: 'Laptop', 'Desktop', 'Server', 'Virtual Machine'."""
    try:
        if _detect_virtual_machine():
            return "Virtual Machine"
        return _detect_device_type_non_vm()
    except Exception:
        return "Desktop"


def _get_internal_ip():
    """Best-effort detection of primary IPv4 address without external reachability.

    Order of attempts:
      1) psutil.net_if_addrs() – first non-loopback, non-APIPA IPv4
      2) UDP connect trick to 8.8.8.8 (common technique)
      3) Windows: PowerShell Get-NetIPAddress
      4) Linux/macOS: `ip -o -4 addr show` or `hostname -I`
    """
    # 1) psutil interfaces
    try:
        if psutil:
            for name, addrs in (psutil.net_if_addrs() or {}).items():
                for a in addrs:
                    if getattr(a, "family", None) == socket.AF_INET:
                        ip = a.address
                        if (
                            ip
                            and not ip.startswith("127.")
                            and not ip.startswith("169.254.")
                            and ip != "0.0.0.0"
                        ):
                            return ip
    except Exception:
        pass

    # 2) UDP connect trick
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.connect(("8.8.8.8", 80))
        ip = s.getsockname()[0]
        s.close()
        if ip:
            return ip
    except Exception:
        pass

    plat = platform.system().lower()
    # 3) Windows PowerShell
    if plat == "windows":
        try:
            ps_cmd = (
                "Get-NetIPAddress -AddressFamily IPv4 | "
                "Where-Object { $_.IPAddress -and $_.IPAddress -notmatch '^169\\.254\\.' -and $_.IPAddress -notmatch '^127\\.' } | "
                "Sort-Object -Property PrefixLength | Select-Object -First 1 -ExpandProperty IPAddress"
            )
            out = _run_powershell_hidden(ps_cmd, timeout=20)
            val = (out.stdout or "").strip()
            if val:
                return val
        except Exception:
            pass

    # 4) Linux/macOS
    try:
        out = subprocess.run(["ip", "-o", "-4", "addr", "show"], capture_output=True, text=True, timeout=10)
        for line in out.stdout.splitlines():
            parts = line.split()
            if len(parts) >= 4:
                ip = parts[3].split("/")[0]
                if ip and not ip.startswith("127.") and not ip.startswith("169.254."):
                    return ip
    except Exception:
        pass
    try:
        out = subprocess.run(["hostname", "-I"], capture_output=True, text=True, timeout=5)
        val = (out.stdout or "").strip().split()
        for ip in val:
            if ip and not ip.startswith("127.") and not ip.startswith("169.254."):
                return ip
    except Exception:
        pass

    return "unknown"


def collect_summary(config):
    try:
        username = getpass.getuser()
        domain = os.environ.get("USERDOMAIN") or socket.gethostname()
        last_user = f"{domain}\\{username}" if username else "unknown"
    except Exception:
        last_user = "unknown"

    try:
        last_reboot = "unknown"
        # First, prefer psutil if available
        if psutil:
            try:
                last_reboot = time.strftime(
                    "%Y-%m-%d %H:%M:%S",
                    time.localtime(psutil.boot_time()),
                )
            except Exception:
                last_reboot = "unknown"
        if last_reboot == "unknown":
            plat = platform.system().lower()
            if plat == "windows":
                # Try WMIC, then robust PowerShell fallback regardless of WMIC presence
                raw = ""
                try:
                    out = _run_hidden(["wmic", "os", "get", "lastbootuptime"], timeout=20)
                    raw = "".join(out.stdout.splitlines()[1:]).strip()
                except Exception:
                    raw = ""
                # If WMIC didn't yield a value, try CIM and format directly in PowerShell
                if not raw:
                    try:
                        ps_cmd = (
                            "(Get-CimInstance Win32_OperatingSystem).LastBootUpTime | "
                            "ForEach-Object { (Get-Date -Date $_ -Format 'yyyy-MM-dd HH:mm:ss') }"
                        )
                        out = _run_powershell_hidden(ps_cmd, timeout=20)
                        raw = (out.stdout or "").strip()
                        if raw:
                            last_reboot = raw
                    except Exception:
                        raw = ""
                # Parse WMIC-style if we had it
                if last_reboot == "unknown" and raw:
                    try:
                        boot = datetime.datetime.strptime(raw.split(".")[0], "%Y%m%d%H%M%S")
                        last_reboot = boot.strftime("%Y-%m-%d %H:%M:%S")
                    except Exception:
                        pass
            else:
                try:
                    out = subprocess.run(["uptime", "-s"], capture_output=True, text=True, timeout=10)
                    val = out.stdout.strip()
                    if val:
                        last_reboot = val
                except Exception:
                    pass
    except Exception:
        last_reboot = "unknown"

    created = config.data.get("created")
    if not created:
        created = time.strftime("%Y-%m-%d %H:%M:%S")
        config.data["created"] = created
        try:
            config._write()
        except Exception:
            pass

    # External IP detection with fallbacks
    external_ip = "unknown"
    for url in ("https://api.ipify.org", "https://api64.ipify.org", "https://ifconfig.me/ip"):
        try:
            external_ip = requests.get(url, timeout=5).text.strip()
            if external_ip:
                break
        except Exception:
            continue

    return {
        "hostname": socket.gethostname(),
        "operating_system": config.data.get("agent_operating_system", detect_agent_os()),
        "device_type": config.data.get("device_type", detect_device_type()),
        "last_user": last_user,
        "internal_ip": _get_internal_ip(),
        "external_ip": external_ip,
        "last_reboot": last_reboot,
        "created": created,
    }


def collect_software():
    items = []
    plat = platform.system().lower()
    try:
        if plat == "windows":
            try:
                out = _run_hidden(["wmic", "product", "get", "name,version"], timeout=60)
                for line in out.stdout.splitlines():
                    if line.strip() and not line.lower().startswith("name"):
                        parts = line.strip().split("  ")
                        name = parts[0].strip()
                        version = parts[-1].strip() if len(parts) > 1 else ""
                        if name:
                            items.append({"name": name, "version": version})
            except FileNotFoundError:
                ps_cmd = (
                    "Get-ItemProperty "
                    "'HKLM:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\*',"
                    "'HKLM:\\Software\\WOW6432Node\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\*' "
                    "| Where-Object { $_.DisplayName } "
                    "| Select-Object DisplayName,DisplayVersion "
                    "| ConvertTo-Json"
                )
                out = _run_powershell_hidden(ps_cmd, timeout=60)
                data = json.loads(out.stdout or "[]")
                if isinstance(data, dict):
                    data = [data]
                for pkg in data:
                    name = pkg.get("DisplayName")
                    if name:
                        items.append({
                            "name": name,
                            "version": pkg.get("DisplayVersion", "")
                        })
        elif plat == "linux":
            out = subprocess.run(["dpkg-query", "-W", "-f=${Package}\t${Version}\n"], capture_output=True, text=True)
            for line in out.stdout.splitlines():
                if "\t" in line:
                    name, version = line.split("\t", 1)
                    items.append({"name": name, "version": version})
        else:
            out = subprocess.run([sys.executable, "-m", "pip", "list", "--format", "json"], capture_output=True, text=True)
            data = json.loads(out.stdout or "[]")
            for pkg in data:
                items.append({"name": pkg.get("name"), "version": pkg.get("version")})
    except Exception as e:
        print(f"[WARN] collect_software failed: {e}")
    return items[:100]


def collect_memory():
    entries = []
    plat = platform.system().lower()
    try:
        if plat == "windows":
            try:
                out = _run_hidden(["wmic", "memorychip", "get", "BankLabel,Speed,SerialNumber,Capacity"], timeout=60)
                lines = [l for l in out.stdout.splitlines() if l.strip() and "BankLabel" not in l]
                for line in lines:
                    parts = [p for p in line.split() if p]
                    if len(parts) >= 4:
                        entries.append({
                            "slot": parts[0],
                            "speed": parts[1],
                            "serial": parts[2],
                            "capacity": parts[3],
                        })
            except FileNotFoundError:
                ps_cmd = (
                    "Get-CimInstance Win32_PhysicalMemory | "
                    "Select-Object BankLabel,Speed,SerialNumber,Capacity | ConvertTo-Json"
                )
                out = _run_powershell_hidden(ps_cmd, timeout=60)
                data = json.loads(out.stdout or "[]")
                if isinstance(data, dict):
                    data = [data]
                for stick in data:
                    entries.append({
                        "slot": stick.get("BankLabel", "unknown"),
                        "speed": str(stick.get("Speed", "unknown")),
                        "serial": stick.get("SerialNumber", "unknown"),
                        "capacity": stick.get("Capacity", "unknown"),
                    })
        elif plat == "linux":
            out = subprocess.run(["dmidecode", "-t", "17"], capture_output=True, text=True)
            slot = speed = serial = capacity = None
            for line in out.stdout.splitlines():
                line = line.strip()
                if line.startswith("Locator:"):
                    slot = line.split(":", 1)[1].strip()
                elif line.startswith("Speed:"):
                    speed = line.split(":", 1)[1].strip()
                elif line.startswith("Serial Number:"):
                    serial = line.split(":", 1)[1].strip()
                elif line.startswith("Size:"):
                    capacity = line.split(":", 1)[1].strip()
                elif not line and slot:
                    entries.append({
                        "slot": slot,
                        "speed": speed or "unknown",
                        "serial": serial or "unknown",
                        "capacity": capacity or "unknown",
                    })
                    slot = speed = serial = capacity = None
            if slot:
                entries.append({
                    "slot": slot,
                    "speed": speed or "unknown",
                    "serial": serial or "unknown",
                    "capacity": capacity or "unknown",
                })
    except Exception as e:
        print(f"[WARN] collect_memory failed: {e}")

    if not entries:
        try:
            if psutil:
                vm = psutil.virtual_memory()
                entries.append({
                    "slot": "physical",
                    "speed": "unknown",
                    "serial": "unknown",
                    "capacity": vm.total,
                })
        except Exception:
            pass
    return entries


def collect_storage():
    disks = []
    plat = platform.system().lower()
    try:
        if psutil:
            for part in psutil.disk_partitions():
                try:
                    usage = psutil.disk_usage(part.mountpoint)
                except Exception:
                    continue
                disks.append({
                    "drive": part.device,
                    "disk_type": "Removable" if "removable" in part.opts.lower() else "Fixed Disk",
                    "usage": usage.percent,
                    "total": usage.total,
                    "free": usage.free,
                    "used": usage.used,
                })
        elif plat == "windows":
            found = False
            for letter in string.ascii_uppercase:
                drive = f"{letter}:\\"
                if os.path.exists(drive):
                    try:
                        usage = shutil.disk_usage(drive)
                    except Exception:
                        continue
                    disks.append({
                        "drive": drive,
                        "disk_type": "Fixed Disk",
                        "usage": (usage.used / usage.total * 100) if usage.total else 0,
                        "total": usage.total,
                        "free": usage.free,
                        "used": usage.used,
                    })
                    found = True
            if not found:
                try:
                    out = _run_hidden(["wmic", "logicaldisk", "get", "DeviceID,Size,FreeSpace"], timeout=60)
                    lines = [l for l in out.stdout.splitlines() if l.strip()][1:]
                    for line in lines:
                        parts = line.split()
                        if len(parts) >= 3:
                            drive, free, size = parts[0], parts[1], parts[2]
                            try:
                                total = float(size)
                                free_bytes = float(free)
                                used = total - free_bytes
                                usage = (used / total * 100) if total else 0
                                disks.append({
                                    "drive": drive,
                                    "disk_type": "Fixed Disk",
                                    "usage": usage,
                                    "total": total,
                                    "free": free_bytes,
                                    "used": used,
                                })
                            except Exception:
                                pass
                except Exception:
                    pass
        else:
            try:
                out = subprocess.run(["df", "-hP"], capture_output=True, text=True)
                for line in out.stdout.splitlines()[1:]:
                    parts = line.split()
                    if len(parts) >= 6:
                        try:
                            usage_str = parts[4].rstrip('%')
                            usage = float(usage_str)
                        except Exception:
                            usage = 0
                        total = parts[1]
                        free_bytes = parts[3]
                        used = parts[2]
                        disks.append({
                            "drive": parts[0],
                            "disk_type": "Mounted",
                            "usage": usage,
                            "total": total,
                            "free": free_bytes,
                            "used": used,
                        })
            except Exception:
                pass
    except Exception as e:
        print(f"[WARN] collect_storage failed: {e}")
    return disks


def collect_network():
    adapters = []
    plat = platform.system().lower()
    try:
        if psutil:
            for name, addrs in psutil.net_if_addrs().items():
                ips = [a.address for a in addrs if getattr(a, "family", None) == socket.AF_INET]
                mac = next((a.address for a in addrs if getattr(a, "family", None) == getattr(psutil, "AF_LINK", object)), "unknown")
                adapters.append({"adapter": name, "ips": ips, "mac": mac})
        elif plat == "windows":
            ps_cmd = (
                "Get-NetIPConfiguration | "
                "Select-Object InterfaceAlias,@{Name='IPv4';Expression={$_.IPv4Address.IPAddress}},"
                "@{Name='MAC';Expression={$_.NetAdapter.MacAddress}} | ConvertTo-Json"
            )
            out = _run_powershell_hidden(ps_cmd, timeout=60)
            data = json.loads(out.stdout or "[]")
            if isinstance(data, dict):
                data = [data]
            for a in data:
                ip = a.get("IPv4")
                adapters.append({
                    "adapter": a.get("InterfaceAlias", "unknown"),
                    "ips": [ip] if ip else [],
                    "mac": a.get("MAC", "unknown"),
                })
        else:
            out = subprocess.run(["ip", "-o", "-4", "addr", "show"], capture_output=True, text=True, timeout=60)
            for line in out.stdout.splitlines():
                parts = line.split()
                if len(parts) >= 4:
                    name = parts[1]
                    ip = parts[3].split("/")[0]
                    adapters.append({"adapter": name, "ips": [ip], "mac": "unknown"})
    except Exception as e:
        print(f"[WARN] collect_network failed: {e}")
    return adapters


async def send_agent_details(agent_id, config):
    """Collect detailed agent data and send to server periodically."""
    while True:
        try:
            details = {
                "summary": collect_summary(config),
                "software": collect_software(),
                "memory": collect_memory(),
                "storage": collect_storage(),
                "network": collect_network(),
            }
            url = config.data.get("borealis_server_url", "http://localhost:5000") + "/api/agent/details"
            payload = {
                "agent_id": agent_id,
                "hostname": details.get("summary", {}).get("hostname", socket.gethostname()),
                "details": details,
            }
            async with aiohttp.ClientSession() as session:
                await session.post(url, json=payload, timeout=10)
        except Exception as e:
            print(f"[WARN] Failed to send agent details: {e}")
        # Report every ~2 minutes
        await asyncio.sleep(120)
