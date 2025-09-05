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


def _get_internal_ip():
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.connect(("8.8.8.8", 80))
        ip = s.getsockname()[0]
        s.close()
        return ip
    except Exception:
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
                try:
                    out = subprocess.run(
                        ["wmic", "os", "get", "lastbootuptime"],
                        capture_output=True,
                        text=True,
                        timeout=60,
                    )
                    raw = "".join(out.stdout.splitlines()[1:]).strip()
                    if raw:
                        boot = datetime.datetime.strptime(raw.split(".")[0], "%Y%m%d%H%M%S")
                        last_reboot = boot.strftime("%Y-%m-%d %H:%M:%S")
                except FileNotFoundError:
                    ps_cmd = "(Get-CimInstance Win32_OperatingSystem).LastBootUpTime"
                    out = subprocess.run(
                        ["powershell", "-NoProfile", "-Command", ps_cmd],
                        capture_output=True,
                        text=True,
                        timeout=60,
                    )
                    raw = out.stdout.strip()
                    if raw:
                        try:
                            boot = datetime.datetime.strptime(raw.split(".")[0], "%Y%m%d%H%M%S")
                            last_reboot = boot.strftime("%Y-%m-%d %H:%M:%S")
                        except Exception:
                            pass
            else:
                try:
                    out = subprocess.run(["uptime", "-s"], capture_output=True, text=True, timeout=30)
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

    try:
        external_ip = requests.get("https://api.ipify.org", timeout=5).text.strip()
    except Exception:
        external_ip = "unknown"

    return {
        "hostname": socket.gethostname(),
        "operating_system": config.data.get("agent_operating_system", detect_agent_os()),
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
                out = subprocess.run(["wmic", "product", "get", "name,version"],
                                     capture_output=True, text=True, timeout=60)
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
                out = subprocess.run(
                    ["powershell", "-NoProfile", "-Command", ps_cmd],
                    capture_output=True,
                    text=True,
                    timeout=60,
                )
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
                out = subprocess.run(
                    ["wmic", "memorychip", "get", "BankLabel,Speed,SerialNumber,Capacity"],
                    capture_output=True,
                    text=True,
                    timeout=60,
                )
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
                out = subprocess.run(
                    ["powershell", "-NoProfile", "-Command", ps_cmd],
                    capture_output=True,
                    text=True,
                    timeout=60,
                )
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
                    out = subprocess.run(
                        ["wmic", "logicaldisk", "get", "DeviceID,Size,FreeSpace"],
                        capture_output=True,
                        text=True,
                        timeout=60,
                    )
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
            out = subprocess.run(
                ["powershell", "-NoProfile", "-Command", ps_cmd],
                capture_output=True,
                text=True,
                timeout=60,
            )
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
        await asyncio.sleep(300)
