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
import urllib.request
import re
import ctypes

try:
    from ctypes import wintypes
except Exception:  # pragma: no cover - non-Windows platforms
    wintypes = None

try:
    import psutil  # type: ignore
except Exception:
    psutil = None

try:
    import aiohttp
except Exception:
    aiohttp = None

if os.name == 'nt':  # pragma: no cover - runtime guard
    try:
        import winreg  # type: ignore
    except Exception:  # pragma: no cover
        winreg = None
else:  # pragma: no cover - non-Windows platforms
    winreg = None


ROLE_NAME = 'device_audit'
# This role gathers elevated inventory details that require administrative
# access (e.g. HKLM uninstall registry hives). Ensure it only runs from the
# SYSTEM agent context where those permissions are available.
ROLE_CONTEXTS = ['system']


IS_WINDOWS = os.name == 'nt'
_EXTERNAL_IP_CACHE = {"value": "", "ts": 0.0}

if IS_WINDOWS:
    WINDOWS_TICK = 10_000_000
    WINDOWS_EPOCH = 11644473600
else:
    WINDOWS_TICK = 0
    WINDOWS_EPOCH = 0


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


def _to_unix_time(filetime):
    try:
        if not filetime or not WINDOWS_TICK:
            return None
        return int(filetime / WINDOWS_TICK - WINDOWS_EPOCH)
    except Exception:
        return None


def _combine_filetime(low, high):
    try:
        if low is None and high is None:
            return None
        low = int(low or 0) & 0xFFFFFFFF
        high = int(high or 0) & 0xFFFFFFFF
        combined = (high << 32) | low
        return _to_unix_time(combined)
    except Exception:
        return None


def _get_registry_views():
    if not IS_WINDOWS or not winreg:
        return []
    views = [winreg.KEY_READ]
    wow64_64 = getattr(winreg, 'KEY_WOW64_64KEY', 0)
    wow64_32 = getattr(winreg, 'KEY_WOW64_32KEY', 0)
    if wow64_64:
        views.insert(0, winreg.KEY_READ | wow64_64)
    if wow64_32:
        views.append(winreg.KEY_READ | wow64_32)
    return views


def _read_registry_value(root, path, name):
    if not IS_WINDOWS or not winreg:
        return None
    for view in _get_registry_views() or [winreg.KEY_READ]:
        try:
            handle = winreg.OpenKey(root, path, 0, view)
        except Exception:
            continue
        try:
            value, _ = winreg.QueryValueEx(handle, name)
            return value
        except Exception:
            pass
        finally:
            try:
                winreg.CloseKey(handle)
            except Exception:
                pass
    return None


def _enumerate_registry_subkeys(root, path):
    names = []
    if not IS_WINDOWS or not winreg:
        return names
    seen = set()
    for view in _get_registry_views() or [winreg.KEY_READ]:
        try:
            handle = winreg.OpenKey(root, path, 0, view)
        except Exception:
            continue
        try:
            count = winreg.QueryInfoKey(handle)[0]
            for index in range(count):
                try:
                    name = winreg.EnumKey(handle, index)
                except Exception:
                    continue
                if name in seen:
                    continue
                seen.add(name)
                names.append((name, view))
        finally:
            try:
                winreg.CloseKey(handle)
            except Exception:
                pass
    return names


def _lookup_account_from_sid(sid_value):
    if not (IS_WINDOWS and wintypes and sid_value):
        return ''
    try:
        advapi32 = ctypes.WinDLL('advapi32', use_last_error=True)
        convert = getattr(advapi32, 'ConvertStringSidToSidW', None)
        lookup = getattr(advapi32, 'LookupAccountSidW', None)
        if not convert or not lookup:
            return ''
        psid = ctypes.c_void_p()
        if not convert(wintypes.LPCWSTR(sid_value), ctypes.byref(psid)):
            return ''
        try:
            name_size = wintypes.DWORD(0)
            domain_size = wintypes.DWORD(0)
            sid_use = wintypes.DWORD(0)
            lookup(
                None,
                psid,
                None,
                ctypes.byref(name_size),
                None,
                ctypes.byref(domain_size),
                ctypes.byref(sid_use),
            )
            name_buffer = ctypes.create_unicode_buffer(max(1, name_size.value))
            domain_buffer = ctypes.create_unicode_buffer(max(1, domain_size.value))
            if lookup(
                None,
                psid,
                name_buffer,
                ctypes.byref(name_size),
                domain_buffer,
                ctypes.byref(domain_size),
                ctypes.byref(sid_use),
            ):
                if domain_buffer.value:
                    return f"{domain_buffer.value}\\{name_buffer.value}".strip('\\')
                return name_buffer.value
        finally:
            try:
                ctypes.windll.kernel32.LocalFree(psid)
            except Exception:
                pass
    except Exception:
        return ''
    return ''


def _get_windows_battery_flag():
    if not (IS_WINDOWS and wintypes):
        return None
    try:
        class SYSTEM_POWER_STATUS(ctypes.Structure):  # pragma: no cover - Windows specific
            _fields_ = [
                ('ACLineStatus', wintypes.BYTE),
                ('BatteryFlag', wintypes.BYTE),
                ('BatteryLifePercent', wintypes.BYTE),
                ('SystemStatusFlag', wintypes.BYTE),
                ('BatteryLifeTime', wintypes.DWORD),
                ('BatteryFullLifeTime', wintypes.DWORD),
            ]

        status = SYSTEM_POWER_STATUS()
        if ctypes.windll.kernel32.GetSystemPowerStatus(ctypes.byref(status)):
            return int(status.BatteryFlag)
    except Exception:
        return None
    return None


def _determine_windows_device_type():
    if not IS_WINDOWS:
        return None
    try:
        powrprof = ctypes.WinDLL('powrprof', use_last_error=True)
        fn = getattr(powrprof, 'PowerDeterminePlatformRoleEx', None)
        if fn:
            role = int(fn(1))
            mapped = _map_pc_system_type(role)
            if mapped:
                return mapped
    except Exception:
        pass
    battery_flag = _get_windows_battery_flag()
    try:
        get_metric = getattr(ctypes.windll.user32, 'GetSystemMetrics', None)
    except Exception:
        get_metric = None
    if battery_flag is not None and battery_flag not in (128, 255):
        if get_metric:
            SM_CONVERTIBLESLATEMODE = 0x2003
            try:
                mode = get_metric(SM_CONVERTIBLESLATEMODE)
                if mode == 0:
                    return 'Tablet'
            except Exception:
                pass
        return 'Laptop'
    if get_metric:
        try:
            SM_SYSTEMDOCKED = 0x0007
            docked = get_metric(SM_SYSTEMDOCKED)
            if docked:
                return 'Workstation'
        except Exception:
            pass
    return 'Desktop'
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
            device_type = _determine_windows_device_type()
            if device_type:
                return device_type
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
                install_date = _read_registry_value(
                    winreg.HKEY_LOCAL_MACHINE,
                    r"SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion",
                    "InstallDate",
                )
                if install_date:
                    return int(install_date)
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


def _get_windows_boot_time():
    if psutil:
        try:
            return int(psutil.boot_time())
        except Exception:
            pass
    if IS_WINDOWS:
        try:
            get_tick = getattr(ctypes.windll.kernel32, 'GetTickCount64', None)
            if get_tick:
                uptime = int(get_tick())
                return int(time.time() - (uptime / 1000))
        except Exception:
            pass
    return None


def _get_windows_install_time():
    try:
        value = _read_registry_value(
            winreg.HKEY_LOCAL_MACHINE,
            r"SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion",
            "InstallDate",
        )
        if value:
            return int(value)
    except Exception:
        pass
    return detect_install_timestamp()


def _get_windows_manufacturer_model():
    manufacturer = ''
    model = ''
    if not (IS_WINDOWS and winreg):
        return manufacturer, model
    try:
        manufacturer = _read_registry_value(
            winreg.HKEY_LOCAL_MACHINE,
            r"HARDWARE\\DESCRIPTION\\System\\BIOS",
            "SystemManufacturer",
        ) or ''
    except Exception:
        manufacturer = ''
    try:
        model = _read_registry_value(
            winreg.HKEY_LOCAL_MACHINE,
            r"HARDWARE\\DESCRIPTION\\System\\BIOS",
            "SystemProductName",
        ) or ''
    except Exception:
        model = ''
    return str(manufacturer or '').strip(), str(model or '').strip()


def _get_windows_logon_details():
    if not (IS_WINDOWS and winreg):
        return '', None
    try:
        logon_path = r"SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Authentication\\LogonUI"
        last_user = _read_registry_value(winreg.HKEY_LOCAL_MACHINE, logon_path, 'LastLoggedOnUser')
        last_ts = _read_registry_value(winreg.HKEY_LOCAL_MACHINE, logon_path, 'LastLoggedOnTime')
        if isinstance(last_ts, int):
            last_ts = _to_unix_time(last_ts)
        else:
            last_ts = None
        if last_user:
            return str(last_user), last_ts
    except Exception:
        pass

    best_user = ''
    best_ts = None
    profile_root = r"SOFTWARE\\Microsoft\\Windows NT\\CurrentVersion\\ProfileList"
    for sid, view in _enumerate_registry_subkeys(winreg.HKEY_LOCAL_MACHINE, profile_root):
        if not sid or not sid.startswith('S-1-5-'):
            continue
        if sid in {'S-1-5-18', 'S-1-5-19', 'S-1-5-20'}:
            continue
        try:
            handle = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, f"{profile_root}\\{sid}", 0, view)
        except Exception:
            continue
        try:
            try:
                low = winreg.QueryValueEx(handle, 'ProfileLoadTimeLow')[0]
            except Exception:
                low = None
            try:
                high = winreg.QueryValueEx(handle, 'ProfileLoadTimeHigh')[0]
            except Exception:
                high = None
            ts = _combine_filetime(low, high)
            try:
                image_path = winreg.QueryValueEx(handle, 'ProfileImagePath')[0]
            except Exception:
                image_path = ''
        finally:
            try:
                winreg.CloseKey(handle)
            except Exception:
                pass
        candidate = _lookup_account_from_sid(sid)
        if not candidate:
            try:
                if image_path:
                    candidate = os.path.basename(str(image_path))
            except Exception:
                candidate = ''
        if candidate and (best_ts is None or (ts or 0) > (best_ts or 0)):
            best_user = candidate
            best_ts = ts
    return best_user, best_ts



def _collect_summary_windows(CONFIG):
    if not IS_WINDOWS:
        return None

    hostname = socket.gethostname()
    os_label = detect_agent_os()
    manufacturer, model = _get_windows_manufacturer_model()
    install_ts = _get_windows_install_time()
    last_boot = _get_windows_boot_time()
    uptime = int(time.time() - last_boot) if last_boot else None
    internal_ip = detect_internal_ip()
    external_ip = detect_external_ip()
    last_user, last_user_ts = _get_windows_logon_details()

    default_domain = ''
    username = ''
    if last_user:
        if '\\' in last_user:
            default_domain, username = last_user.split('\\', 1)
        elif '@' in last_user:
            username, default_domain = last_user.split('@', 1)
        else:
            username = last_user

    try:
        default_domain_reg = _read_registry_value(
            winreg.HKEY_LOCAL_MACHINE,
            r"SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon",
            'DefaultDomainName',
        )
        if default_domain_reg and not default_domain:
            default_domain = str(default_domain_reg)
        default_user_reg = _read_registry_value(
            winreg.HKEY_LOCAL_MACHINE,
            r"SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon",
            'DefaultUserName',
        )
        if default_user_reg and not username:
            username = str(default_user_reg)
        if not last_user and (username or default_domain):
            if default_domain and username:
                last_user = f"{default_domain}\\{username}"
            else:
                last_user = username or default_domain or ''
    except Exception:
        pass

    if not default_domain:
        default_domain = os.environ.get('USERDOMAIN') or ''

    device_type = detect_device_type()

    summary = {
        'hostname': hostname,
        'os': os_label,
        'operating_system': os_label,
        'agent_operating_system': os_label,
        'username': username or '',
        'domain': default_domain or '',
        'last_user': last_user or '',
        'device_type': device_type,
        'internal_ip': internal_ip,
        'external_ip': external_ip,
        'last_reboot': last_boot,
        'created': install_ts,
        'uptime_sec': uptime,
        'manufacturer': manufacturer,
        'model': model,
    }
    if last_user_ts:
        summary['last_user_timestamp'] = last_user_ts
    if not summary['internal_ip']:
        summary['internal_ip'] = detect_internal_ip()
    if not summary['created']:
        summary['created'] = detect_install_timestamp()
    if not summary['device_type']:
        summary['device_type'] = detect_device_type()

    try:
        CONFIG.data['agent_operating_system'] = os_label
        CONFIG._write()
    except Exception:
        pass
    return summary

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

