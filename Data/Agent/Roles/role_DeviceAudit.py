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
import textwrap

try:
    import psutil  # type: ignore
except Exception:
    psutil = None

try:
    import aiohttp
except Exception:
    aiohttp = None

try:
    import win32com.client  # type: ignore
except Exception:
    win32com = None

try:
    import pythoncom  # type: ignore
except Exception:
    pythoncom = None

try:
    import win32security  # type: ignore
except Exception:
    win32security = None

try:
    import pywintypes  # type: ignore
except Exception:
    pywintypes = None


ROLE_NAME = 'device_audit'
# This role gathers elevated inventory details that require administrative
# access (e.g. HKLM uninstall registry hives). Ensure it only runs from the
# SYSTEM agent context where those permissions are available.
ROLE_CONTEXTS = ['system']


IS_WINDOWS = os.name == 'nt'
_EXTERNAL_IP_CACHE = {"value": "", "ts": 0.0}
_WINDOWS_COLLECTOR = None
_WINDOWS_COLLECTOR_FAILED = False


class WindowsInventoryCollector:
    def __init__(self):
        if not IS_WINDOWS or win32com is None or pythoncom is None:
            raise RuntimeError("WMI access is unavailable")
        self._services = {}

    def _get_service(self, namespace: str):
        svc = self._services.get(namespace)
        if svc is not None:
            return svc
        pythoncom.CoInitialize()
        locator = win32com.client.Dispatch("WbemScripting.SWbemLocator")
        svc = locator.ConnectServer(".", namespace)
        self._services[namespace] = svc
        return svc

    def query(self, query: str, namespace: str = "root\\cimv2"):
        try:
            svc = self._get_service(namespace)
            result = svc.ExecQuery(query)
            return list(result)
        except Exception:
            return []

    @staticmethod
    def to_dict(record):
        data = {}
        try:
            for prop in record.Properties_:
                data[prop.Name] = prop.Value
        except Exception:
            pass
        return data


def _ensure_windows_collector():
    global _WINDOWS_COLLECTOR, _WINDOWS_COLLECTOR_FAILED
    if _WINDOWS_COLLECTOR_FAILED:
        return None
    if not IS_WINDOWS or win32com is None or pythoncom is None:
        _WINDOWS_COLLECTOR_FAILED = True
        return None
    if _WINDOWS_COLLECTOR is None:
        try:
            _WINDOWS_COLLECTOR = WindowsInventoryCollector()
        except Exception:
            _WINDOWS_COLLECTOR_FAILED = True
            return None
    return _WINDOWS_COLLECTOR


def _wmi_time_to_epoch(value):
    if not value:
        return None
    try:
        text = str(value)
        match = re.match(r"(\d{4})(\d{2})(\d{2})(\d{2})(\d{2})(\d{2})(?:\.(\d+))?([+-]\d{3})?", text)
        if not match:
            return None
        year, month, day, hour, minute, second = [int(match.group(i)) for i in range(1, 7)]
        dt = datetime.datetime(year, month, day, hour, minute, second)
        offset = match.group(8)
        if offset:
            minutes = int(offset)
            dt -= datetime.timedelta(minutes=minutes)
        return int(dt.timestamp())
    except Exception:
        return None


def _windows_sid_to_account(sid_str: str) -> str:
    if not sid_str or win32security is None:
        return ""
    try:
        sid = win32security.ConvertStringSidToSid(sid_str)
        name, domain, _ = win32security.LookupAccountSid(None, sid)
        if domain:
            return f"{domain}\\{name}"
        return name
    except Exception:
        return ""


def _map_pc_system_type(value):
    mapping = {
        0: "Unspecified",
        1: "Desktop",
        2: "Mobile",
        3: "Workstation",
        4: "Enterprise Server",
        5: "SOHO Server",
        6: "Appliance",
        7: "Performance Server",
        8: "Slate",
        9: "Thin Client",
        10: "Tablet",
        11: "Convertible",
        12: "Detachable",
        13: "IoT Gateway",
        14: "Embedded PC",
        15: "Mini PC",
    }
    try:
        return mapping.get(int(value))
    except Exception:
        return None


def _map_chassis_type(values):
    mapping = {
        1: "Other",
        2: "Unknown",
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
    if not isinstance(values, (list, tuple)):
        return None
    for value in values:
        try:
            mapped = mapping.get(int(value))
            if mapped:
                return mapped
        except Exception:
            continue
    return None


def _windows_detect_last_user(collector: WindowsInventoryCollector):
    best_account = ""
    best_ts = None
    for record in collector.query("SELECT SID, LocalPath, LastUseTime FROM Win32_UserProfile WHERE Special = FALSE"):
        data = WindowsInventoryCollector.to_dict(record)
        ts = _wmi_time_to_epoch(data.get("LastUseTime"))
        if ts is None:
            continue
        account = _windows_sid_to_account(data.get("SID"))
        if not account:
            path = data.get("LocalPath") or ""
            if path:
                account = os.path.basename(path)
        if not account:
            continue
        if best_ts is None or ts > best_ts:
            best_ts = ts
            best_account = account
    return best_account, best_ts


def _windows_primary_ip(collector: WindowsInventoryCollector):
    for record in collector.query(
        "SELECT IPAddress FROM Win32_NetworkAdapterConfiguration WHERE IPEnabled = TRUE"
    ):
        data = WindowsInventoryCollector.to_dict(record)
        addresses = data.get("IPAddress")
        if isinstance(addresses, (list, tuple)):
            for addr in addresses:
                if not addr or not isinstance(addr, str):
                    continue
                if addr.startswith("127.") or addr.startswith("169.254"):
                    continue
                if ":" in addr:
                    continue
                return addr
    return ""
def detect_agent_os():
    try:
        if IS_WINDOWS:
            collector = _ensure_windows_collector()
            if collector:
                os_records = collector.query("SELECT Caption, Version, BuildNumber FROM Win32_OperatingSystem")
                if os_records:
                    data = WindowsInventoryCollector.to_dict(os_records[0])
                    caption = data.get('Caption') or ''
                    version = data.get('Version') or ''
                    build = data.get('BuildNumber') or ''
                    parts = [caption.strip(), version.strip()]
                    if build:
                        parts.append(f"Build {build}")
                    label = " ".join([p for p in parts if p])
                    if label:
                        return label
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


def _collect_summary_windows(CONFIG):
    collector = _ensure_windows_collector()
    if not collector:
        return None
    try:
        cs_records = collector.query(
            "SELECT Name, Domain, Workgroup, UserName, PartOfDomain, Manufacturer, Model, PCSystemType, PCSystemTypeEx FROM Win32_ComputerSystem"
        )
        if not cs_records:
            return None
        cs = WindowsInventoryCollector.to_dict(cs_records[0])
        os_records = collector.query(
            "SELECT Caption, Version, BuildNumber, LastBootUpTime, InstallDate FROM Win32_OperatingSystem"
        )
        os_data = WindowsInventoryCollector.to_dict(os_records[0]) if os_records else {}

        hostname = cs.get('Name') or socket.gethostname()
        part_of_domain = bool(cs.get('PartOfDomain'))
        domain = cs.get('Domain') if part_of_domain else cs.get('Workgroup')
        domain = domain or ''

        raw_user = cs.get('UserName') or ''
        raw_user = str(raw_user)
        if '\\' in raw_user:
            default_domain, default_user = raw_user.split('\\', 1)
        else:
            default_domain, default_user = domain, raw_user

        last_user, last_user_ts = _windows_detect_last_user(collector)
        if not last_user:
            if default_domain and default_user:
                last_user = f"{default_domain}\\{default_user}" if default_domain else default_user
            else:
                last_user = raw_user or ''

        device_type = (
            _map_pc_system_type(cs.get('PCSystemTypeEx'))
            or _map_pc_system_type(cs.get('PCSystemType'))
        )
        if not device_type:
            enclosure_records = collector.query("SELECT ChassisTypes FROM Win32_SystemEnclosure")
            for enclosure in enclosure_records:
                enclosure_data = WindowsInventoryCollector.to_dict(enclosure)
                device_type = _map_chassis_type(enclosure_data.get('ChassisTypes'))
                if device_type:
                    break

        internal_ip = _windows_primary_ip(collector) or detect_internal_ip()
        external_ip = detect_external_ip()

        last_boot = _wmi_time_to_epoch(os_data.get('LastBootUpTime'))
        install_ts = _wmi_time_to_epoch(os_data.get('InstallDate'))
        uptime = int(time.time() - last_boot) if last_boot else None

        caption = (os_data.get('Caption') or '').strip()
        version = (os_data.get('Version') or '').strip()
        build = (os_data.get('BuildNumber') or '').strip()
        os_label_parts = [caption, version]
        os_label = " ".join([p for p in os_label_parts if p]).strip()
        if build:
            os_label = f"{os_label} Build {build}".strip()
        if not os_label:
            os_label = detect_agent_os()

        summary = {
            'hostname': hostname,
            'os': os_label,
            'operating_system': os_label,
            'agent_operating_system': os_label,
            'username': default_user or '',
            'domain': default_domain or domain,
            'last_user': last_user or '',
            'device_type': device_type or detect_device_type(),
            'internal_ip': internal_ip,
            'external_ip': external_ip,
            'last_reboot': last_boot,
            'created': install_ts or detect_install_timestamp(),
            'uptime_sec': uptime,
            'manufacturer': cs.get('Manufacturer') or '',
            'model': cs.get('Model') or '',
        }
        if last_user_ts:
            summary['last_user_timestamp'] = last_user_ts
        try:
            CONFIG.data['agent_operating_system'] = os_label
            CONFIG._write()
        except Exception:
            pass
        return summary
    except Exception:
        return None


def _collect_software_windows():
    if not IS_WINDOWS:
        return []
    script = textwrap.dedent(
        """
$ErrorActionPreference = 'SilentlyContinue'
$apps = Get-CimInstance -ClassName Win32Reg_AddRemovePrograms -ErrorAction SilentlyContinue
if (-not $apps) { $apps = @() }
$normalized = foreach ($app in $apps) {
    if (-not $app.DisplayName) { continue }
    if ($app.PSObject.Properties['SystemComponent'] -and $app.SystemComponent) { continue }
    if ($app.PSObject.Properties['ParentKeyName'] -and $app.ParentKeyName) { continue }
    $date = ''
    if ($app.InstallDate) {
        $id = [string]$app.InstallDate
        if ($id.Length -ge 8) {
            $date = '{0}-{1}-{2}' -f $id.Substring(0,4), $id.Substring(4,2), $id.Substring(6,2)
        } else {
            $date = $id
        }
    }
    [pscustomobject]@{
        Name = [string]$app.DisplayName
        Version = [string]($app.DisplayVersion)
        Publisher = [string]($app.Publisher)
        InstallDate = $date
    }
}
$normalized | Sort-Object Name, Version | ConvertTo-Json -Depth 3 -Compress
        """
    ).strip()
    try:
        out = subprocess.run(
            ["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script],
            capture_output=True,
            text=True,
            timeout=180,
        )
    except Exception:
        return []
    if out.returncode != 0:
        return []
    stdout = (out.stdout or '').strip()
    if not stdout:
        return []
    try:
        data = json.loads(stdout)
    except Exception:
        return []
    if isinstance(data, dict):
        data = [data]
    entries = []
    for item in data or []:
        try:
            name = str(item.get('Name') or item.get('DisplayName') or '').strip()
            if not name:
                continue
            version = str(item.get('Version') or item.get('DisplayVersion') or '').strip()
            publisher = str(item.get('Publisher') or '').strip()
            install_date = str(item.get('InstallDate') or '').strip()
            entries.append(
                {
                    'name': name,
                    'version': version,
                    'publisher': publisher,
                    'install_date': install_date,
                }
            )
        except Exception:
            continue
    return entries


def _collect_software_windows_registry():
    entries = []
    if not IS_WINDOWS:
        return entries
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
                entries.append(
                    {
                        'name': str(name),
                        'version': str(version or ''),
                        'publisher': str(publisher or ''),
                        'install_date': str(install_date or ''),
                    }
                )

        uninstall_paths = [
            (winreg.HKEY_LOCAL_MACHINE, r"SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Uninstall"),
            (winreg.HKEY_LOCAL_MACHINE, r"SOFTWARE\\WOW6432Node\\Microsoft\\Windows\\CurrentVersion\\Uninstall"),
            (winreg.HKEY_CURRENT_USER, r"SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Uninstall"),
        ]
        for root, path in uninstall_paths:
            _walk_uninstall(root, path)
    except Exception:
        return []
    return entries


def collect_summary(CONFIG):
    if IS_WINDOWS:
        summary = _collect_summary_windows(CONFIG)
        if summary:
            return summary
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
            windows_entries = _collect_software_windows()
            if windows_entries:
                entries.extend(windows_entries)
            if not entries:
                entries.extend(_collect_software_windows_registry())
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

