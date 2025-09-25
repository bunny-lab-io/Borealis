import os
import json
import time
import socket
import platform
import subprocess
import shutil
import string
import asyncio
import getpass
import datetime
import urllib.request
import re

try:
    import psutil  # type: ignore
except Exception:
    psutil = None

try:
    import aiohttp
except Exception:
    aiohttp = None


ROLE_NAME = 'device_audit'
# This role gathers elevated inventory details that require administrative
# access (e.g. HKLM uninstall registry hives). Ensure it only runs from the
# SYSTEM agent context where those permissions are available.
ROLE_CONTEXTS = ['system']


IS_WINDOWS = os.name == 'nt'
_EXTERNAL_IP_CACHE = {"value": "", "ts": 0.0}


def detect_agent_os():
    try:
        plat = platform.system().lower()
        if plat.startswith('win'):
            try:
                import winreg  # type: ignore
                reg_path = r"SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion"
                access = getattr(winreg, 'KEY_READ', 0x20019)
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

                product_name = _get("ProductName", "")
                display_version = _get("DisplayVersion", "")
                release_id = _get("ReleaseId", "")
                build_number = _get("CurrentBuildNumber", "") or _get("CurrentBuild", "")
                ubr = _get("UBR", None)

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

                os_name = f"Windows {major_label}"
                version_label = display_version or release_id or ""
                if isinstance(ubr, int):
                    build_str = f"{build_number}.{ubr}" if build_number else str(ubr)
                else:
                    try:
                        build_str = f"{build_number}.{int(ubr)}" if build_number and ubr else (build_number or "")
                    except Exception:
                        build_str = build_number or ""

                parts = [os_name]
                if product_name and product_name.lower().startswith('windows '):
                    try:
                        tail = product_name.split(' ', 2)[2]
                        if tail:
                            parts.append(tail)
                    except Exception:
                        pass
                if version_label:
                    parts.append(version_label)
                if build_str:
                    parts.append(f"Build {build_str}")
                return " ".join([p for p in parts if p]).strip() or platform.platform()
            except Exception:
                return platform.platform()
        elif plat == 'darwin':
            try:
                out = subprocess.run(["sw_vers", "-productVersion"], capture_output=True, text=True, timeout=3)
                ver = (out.stdout or '').strip()
                return f"macOS {ver}" if ver else "macOS"
            except Exception:
                return "macOS"
        else:
            try:
                import distro  # type: ignore
                name = distro.name(pretty=True) or distro.id()
                ver = distro.version()
                return f"{name} {ver}".strip()
            except Exception:
                return platform.platform()
    except Exception:
        return "Unknown"


def detect_device_type():
    try:
        plat = platform.system().lower()
        if plat == 'windows':
            try:
                ps_cmd = (
                    "(Get-CimInstance Win32_ComputerSystem | Select-Object -ExpandProperty PCSystemType)"
                )
                out = subprocess.run(
                    ["powershell", "-NoProfile", "-Command", ps_cmd],
                    capture_output=True,
                    text=True,
                    timeout=10,
                )
                pc_type = out.stdout.strip()
                mapping = {
                    "0": "Unspecified",
                    "1": "Desktop",
                    "2": "Mobile",
                    "3": "Workstation",
                    "4": "Enterprise Server",
                    "5": "SOHO Server",
                    "6": "Appliance",
                    "7": "Performance Server",
                    "8": "Slate",
                    "9": "Small Device",
                    "10": "Tablet",
                    "11": "Convertible",
                    "12": "Detachable",
                }
                if pc_type in mapping:
                    return mapping[pc_type]
            except Exception:
                pass
            try:
                ps_cmd = (
                    "Get-CimInstance Win32_SystemEnclosure | Select-Object -ExpandProperty ChassisTypes | ConvertTo-Json"
                )
                out = subprocess.run(
                    ["powershell", "-NoProfile", "-Command", ps_cmd],
                    capture_output=True,
                    text=True,
                    timeout=10,
                )
                data = json.loads(out.stdout or "[]")
                if isinstance(data, int):
                    data = [data]
                type_map = {
                    3: "Desktop",
                    4: "Low Profile Desktop",
                    5: "Pizza Box",
                    6: "Mini Tower",
                    7: "Tower",
                    8: "Portable",
                    9: "Laptop",
                    10: "Notebook",
                    11: "Handheld",
                    12: "Docking Station",
                    13: "All in One",
                    14: "Sub Notebook",
                    15: "Space-Saving",
                    16: "Lunch Box",
                    17: "Main System Chassis",
                    18: "Expansion Chassis",
                    19: "SubChassis",
                    20: "Bus Expansion Chassis",
                    21: "Peripheral Chassis",
                    22: "Storage Chassis",
                    23: "Rack Mount",
                    24: "Sealed-Case PC",
                    30: "Mini PC",
                    31: "Stick PC",
                }
                for value in data:
                    if value in type_map:
                        return type_map[value]
            except Exception:
                pass
        elif plat == 'linux':
            try:
                with open('/sys/class/dmi/id/chassis_type', 'r', encoding='utf-8') as f:
                    value = f.read().strip()
                type_map = {
                    '3': 'Desktop',
                    '4': 'Low Profile Desktop',
                    '5': 'Pizza Box',
                    '6': 'Mini Tower',
                    '7': 'Tower',
                    '8': 'Portable',
                    '9': 'Laptop',
                    '10': 'Notebook',
                    '11': 'Handheld',
                    '12': 'Docking Station',
                    '13': 'All in One',
                    '14': 'Sub Notebook',
                    '30': 'Mini PC',
                    '31': 'Stick PC',
                }
                if value in type_map:
                    return type_map[value]
            except Exception:
                pass
        elif plat == 'darwin':
            try:
                out = subprocess.run(
                    ["sysctl", "-n", "hw.model"],
                    capture_output=True,
                    text=True,
                    timeout=10,
                )
                model = out.stdout.strip().lower()
                if model.startswith('macbook'):
                    return 'Laptop'
                if model.startswith('imac') or model.startswith('macmini'):
                    return 'Desktop'
                if model.startswith('macpro'):
                    return 'Workstation'
            except Exception:
                pass
    except Exception:
        pass
    return "Unknown"


def detect_internal_ip():
    try:
        if psutil:
            addrs = psutil.net_if_addrs()
            for name, address_list in addrs.items():
                for addr in address_list:
                    if getattr(addr, 'family', None) == socket.AF_INET:
                        ip = addr.address
                        if ip and not ip.startswith('127.') and not ip.startswith('169.254.'):
                            return ip
    except Exception:
        pass
    try:
        hostname = socket.gethostname()
        ip = socket.gethostbyname(hostname)
        if ip and not ip.startswith('127.'):
            return ip
    except Exception:
        pass
    return ""


def detect_external_ip():
    now = time.time()
    cached_value = _EXTERNAL_IP_CACHE.get('value')
    cached_ts = _EXTERNAL_IP_CACHE.get('ts', 0)
    if cached_value and (now - cached_ts) < 900:
        return cached_value
    try:
        for url in ('https://api.ipify.org', 'https://ifconfig.me/ip'):
            try:
                with urllib.request.urlopen(url, timeout=5) as resp:
                    data = resp.read().decode('utf-8', errors='ignore').strip()
                    if data:
                        _EXTERNAL_IP_CACHE['value'] = data
                        _EXTERNAL_IP_CACHE['ts'] = now
                        return data
            except Exception:
                continue
    except Exception:
        pass
    return cached_value or ""


def detect_install_timestamp():
    try:
        plat = platform.system().lower()
        if plat == 'windows':
            try:
                ps_cmd = "(Get-CimInstance Win32_OperatingSystem).InstallDate"
                out = subprocess.run(
                    ["powershell", "-NoProfile", "-Command", ps_cmd],
                    capture_output=True,
                    text=True,
                    timeout=10,
                )
                raw = (out.stdout or '').strip()
                if raw:
                    match = re.match(r"(\d{14})", raw)
                    if match:
                        dt = datetime.datetime.strptime(match.group(1), "%Y%m%d%H%M%S")
                        return int(dt.timestamp())
            except Exception:
                pass
        elif plat == 'linux':
            try:
                stat = os.stat('/')
                return int(stat.st_ctime)
            except Exception:
                pass
        elif plat == 'darwin':
            try:
                out = subprocess.run(
                    ["sysctl", "-n", "kern.boottime"],
                    capture_output=True,
                    text=True,
                    timeout=10,
                )
                text = out.stdout
                match = re.search(r'sec = (\d+)', text or '')
                if match:
                    boot_ts = int(match.group(1))
                else:
                    boot_ts = int(time.time())
                # On macOS we approximate install date with the creation time of /var/db/.AppleSetupDone if available
                setup_done = '/var/db/.AppleSetupDone'
                if os.path.exists(setup_done):
                    return int(os.path.getctime(setup_done))
                return boot_ts
            except Exception:
                pass
    except Exception:
        pass
    return None


def collect_summary(CONFIG):
    try:
        hostname = socket.gethostname()
        os_name = CONFIG.data.get('agent_operating_system', detect_agent_os())
        domain = os.environ.get('USERDOMAIN') or ''
        username = ''
        try:
            if callable(getattr(getpass, 'getuser', None)):
                username = getpass.getuser()
        except Exception:
            username = ''
        if not username:
            username = os.environ.get('USERNAME') or os.environ.get('USER') or ''
        last_user = ""
        if domain and username:
            last_user = f"{domain}\\{username}"
        else:
            last_user = username or domain or ''

        summary = {
            'hostname': hostname,
            'os': os_name,
            'operating_system': os_name,
            'agent_operating_system': os_name,
            'username': username,
            'domain': domain,
            'last_user': last_user,
            'device_type': detect_device_type(),
            'internal_ip': detect_internal_ip(),
            'external_ip': detect_external_ip(),
            'last_reboot': int(psutil.boot_time()) if psutil else None,
            'created': detect_install_timestamp(),
            'uptime_sec': int(time.time() - psutil.boot_time()) if psutil else None,
        }
        return summary
    except Exception:
        return {'hostname': socket.gethostname()}


def collect_software():
    entries = []
    plat = platform.system().lower()
    try:
        if plat == 'windows':
            try:
                import winreg  # type: ignore

                def _walk_uninstall(root, path):
                    try:
                        key = winreg.OpenKey(root, path)
                    except Exception:
                        return
                    count = 0
                    try:
                        count = winreg.QueryInfoKey(key)[0]
                    except Exception:
                        pass
                    for i in range(count):
                        try:
                            sub = winreg.EnumKey(key, i)
                        except Exception:
                            continue
                        try:
                            subkey = winreg.OpenKey(key, sub)
                        except Exception:
                            continue
                        try:
                            name = winreg.QueryValueEx(subkey, 'DisplayName')[0]
                        except Exception:
                            continue
                        if not name:
                            continue
                        version = ''
                        publisher = ''
                        install_date = ''
                        try:
                            version = winreg.QueryValueEx(subkey, 'DisplayVersion')[0]
                        except Exception:
                            pass
                        try:
                            publisher = winreg.QueryValueEx(subkey, 'Publisher')[0]
                        except Exception:
                            pass
                        try:
                            install_date = winreg.QueryValueEx(subkey, 'InstallDate')[0]
                            if isinstance(install_date, (int, float)):
                                install_date = str(int(install_date))
                        except Exception:
                            pass
                        if isinstance(install_date, str) and len(install_date) == 8 and install_date.isdigit():
                            install_date = f"{install_date[0:4]}-{install_date[4:6]}-{install_date[6:8]}"
                        entries.append({
                            'name': str(name),
                            'version': str(version or ''),
                            'publisher': str(publisher or ''),
                            'install_date': str(install_date or ''),
                        })

                uninstall_paths = [
                    (winreg.HKEY_LOCAL_MACHINE, r"SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Uninstall"),
                    (winreg.HKEY_LOCAL_MACHINE, r"SOFTWARE\\WOW6432Node\\Microsoft\\Windows\\CurrentVersion\\Uninstall"),
                    (winreg.HKEY_CURRENT_USER, r"SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Uninstall"),
                ]
                for root, path in uninstall_paths:
                    _walk_uninstall(root, path)
            except Exception:
                pass
        elif plat == 'linux':
            try:
                out = subprocess.run(
                    ['dpkg-query', '-W', '-f=${Package}\t${Version}\t${Maintainer}\n'],
                    capture_output=True,
                    text=True,
                    timeout=60,
                )
                if out.returncode == 0:
                    for line in out.stdout.splitlines():
                        if not line.strip():
                            continue
                        parts = line.split('\t')
                        if not parts:
                            continue
                        name = parts[0]
                        version = parts[1] if len(parts) > 1 else ''
                        publisher = parts[2] if len(parts) > 2 else ''
                        entries.append({
                            'name': name,
                            'version': version,
                            'publisher': publisher,
                            'install_date': '',
                        })
                else:
                    raise RuntimeError('dpkg-query failed')
            except Exception:
                try:
                    out = subprocess.run(
                        ['rpm', '-qa', '--qf', '%{NAME}\t%{VERSION}-%{RELEASE}\t%{VENDOR}\n'],
                        capture_output=True,
                        text=True,
                        timeout=60,
                    )
                    if out.returncode == 0:
                        for line in out.stdout.splitlines():
                            if not line.strip():
                                continue
                            parts = line.split('\t')
                            name = parts[0]
                            version = parts[1] if len(parts) > 1 else ''
                            publisher = parts[2] if len(parts) > 2 else ''
                            entries.append({
                                'name': name,
                                'version': version,
                                'publisher': publisher,
                                'install_date': '',
                            })
                except Exception:
                    pass
        elif plat == 'darwin':
            try:
                out = subprocess.run(
                    ['system_profiler', 'SPApplicationsDataType', '-json'],
                    capture_output=True,
                    text=True,
                    timeout=120,
                )
                data = json.loads(out.stdout or '{}')
                apps = data.get('SPApplicationsDataType', [])
                for app in apps:
                    name = app.get('_name') or ''
                    if not name:
                        continue
                    version = app.get('version') or ''
                    publisher = app.get('obtained_from') or ''
                    entries.append({
                        'name': name,
                        'version': version,
                        'publisher': publisher,
                        'install_date': '',
                    })
            except Exception:
                pass
    except Exception:
        pass

    deduped = []
    seen = set()
    for entry in entries:
        name = entry.get('name')
        version = entry.get('version')
        key = (name, version)
        if not name or key in seen:
            continue
        seen.add(key)
        deduped.append(entry)
    try:
        deduped.sort(key=lambda x: (str(x.get('name') or '').lower(), str(x.get('version') or '').lower()))
    except Exception:
        pass
    return deduped


def collect_memory():
    entries = []
    try:
        plat = platform.system().lower()
        if plat == 'windows':
            try:
                ps_cmd = (
                    "Get-CimInstance Win32_PhysicalMemory | "
                    "Select-Object BankLabel,Speed,SerialNumber,Capacity | ConvertTo-Json"
                )
                out = subprocess.run(["powershell", "-NoProfile", "-Command", ps_cmd], capture_output=True, text=True, timeout=60)
                data = json.loads(out.stdout or "[]")
                if isinstance(data, dict):
                    data = [data]
                for stick in data:
                    entries.append({
                        'slot': stick.get('BankLabel', 'unknown'),
                        'speed': str(stick.get('Speed', 'unknown')),
                        'serial': stick.get('SerialNumber', 'unknown'),
                        'capacity': stick.get('Capacity', 'unknown'),
                    })
            except Exception:
                pass
    except Exception:
        pass
    if not entries and psutil:
        try:
            vm = psutil.virtual_memory()
            entries.append({'slot': 'physical', 'speed': 'unknown', 'serial': 'unknown', 'capacity': vm.total})
        except Exception:
            pass
    return entries


def collect_storage():
    disks = []
    try:
        if psutil:
            for part in psutil.disk_partitions():
                try:
                    usage = psutil.disk_usage(part.mountpoint)
                except Exception:
                    continue
                disks.append({
                    'drive': part.device,
                    'disk_type': 'Removable' if isinstance(part.opts, str) and 'removable' in part.opts.lower() else 'Fixed Disk',
                    'usage': usage.percent,
                    'total': usage.total,
                    'free': usage.free,
                    'used': usage.used,
                })
        else:
            # Fallback basic detection on Windows via drive letters
            if IS_WINDOWS:
                for letter in string.ascii_uppercase:
                    drive = f"{letter}:\\"
                    if os.path.exists(drive):
                        try:
                            usage = shutil.disk_usage(drive)
                        except Exception:
                            continue
                        disks.append({
                            'drive': drive,
                            'disk_type': 'Fixed Disk',
                            'usage': (usage.used / usage.total * 100) if usage.total else 0,
                            'total': usage.total,
                            'free': usage.free,
                            'used': usage.used,
                        })
    except Exception:
        pass
    return disks


def collect_network():
    adapters = []
    try:
        if psutil:
            stats = {}
            try:
                stats = psutil.net_if_stats()
            except Exception:
                stats = {}
            addrs = psutil.net_if_addrs()
            for name, addresses in addrs.items():
                stat = stats.get(name)
                if stat and not stat.isup:
                    continue
                entry = {'adapter': name, 'ips': [], 'mac': 'unknown'}
                for addr in addresses:
                    family = getattr(addr, 'family', None)
                    if family == socket.AF_INET:
                        entry['ips'].append(addr.address)
                    elif family == socket.AF_INET6:
                        continue
                    else:
                        mac_family = getattr(psutil, 'AF_LINK', None)
                        packet_family = getattr(socket, 'AF_PACKET', None)
                        if family in (mac_family, packet_family):
                            if addr.address:
                                entry['mac'] = addr.address
                if entry['ips'] or entry['mac'] != 'unknown':
                    adapters.append(entry)
        else:
            if IS_WINDOWS:
                try:
                    ps_cmd = (
                        "Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | "
                        "Where-Object { $_.IPAddress -and $_.IPAddress -notlike '169.254*' -and $_.IPAddress -ne '127.0.0.1' } | "
                        "Select-Object InterfaceAlias,IPAddress | ConvertTo-Json"
                    )
                    out = subprocess.run(["powershell", "-NoProfile", "-Command", ps_cmd], capture_output=True, text=True, timeout=60)
                    data = json.loads(out.stdout or "[]")
                    if isinstance(data, dict):
                        data = [data]
                    temp = {}
                    for item in data:
                        alias = item.get('InterfaceAlias', 'unknown')
                        temp.setdefault(alias, {'adapter': alias, 'ips': [], 'mac': 'unknown'})
                        ip = item.get('IPAddress')
                        if ip:
                            temp[alias]['ips'].append(ip)
                    adapters.extend(temp.values())
                except Exception:
                    pass
            else:
                out = subprocess.run(["ip", "-o", "-4", "addr", "show"], capture_output=True, text=True, timeout=60)
                for line in out.stdout.splitlines():
                    parts = line.split()
                    if len(parts) >= 4:
                        name = parts[1]
                        ip = parts[3].split("/")[0]
                        adapters.append({'adapter': name, 'ips': [ip], 'mac': 'unknown'})
    except Exception:
        pass
    return adapters


class Role:
    def __init__(self, ctx):
        self.ctx = ctx
        try:
            # Set OS string once
            self.ctx.config.data['agent_operating_system'] = detect_agent_os()
            self.ctx.config._write()
        except Exception:
            pass
        # Start periodic reporter
        try:
            self.task = self.ctx.loop.create_task(self._report_loop())
        except Exception:
            self.task = None

    def stop_all(self):
        try:
            if self.task:
                self.task.cancel()
        except Exception:
            pass

    async def _report_loop(self):
        while True:
            try:
                details = {
                    'summary': collect_summary(self.ctx.config),
                    'software': collect_software(),
                    'memory': collect_memory(),
                    'storage': collect_storage(),
                    'network': collect_network(),
                }
                base_url = (self.ctx.config.data.get('borealis_server_url', 'http://localhost:5000') or '').rstrip('/')
                url = f"{base_url}/api/agent/details"
                payload = {
                    'agent_id': self.ctx.agent_id,
                    'hostname': details.get('summary', {}).get('hostname', socket.gethostname()),
                    'details': details,
                }
                if aiohttp is not None:
                    async with aiohttp.ClientSession() as session:
                        await session.post(url, json=payload, timeout=10)
            except Exception:
                pass
            await asyncio.sleep(300)

