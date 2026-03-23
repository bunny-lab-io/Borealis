import os
import sys
import json
import time
import socket
import platform
import subprocess
import shutil
import string
import asyncio
import re
import ipaddress

try:
    import psutil  # type: ignore
except Exception:
    psutil = None

try:
    import aiohttp
except Exception:
    aiohttp = None


ROLE_NAME = 'device_audit'
ROLE_CONTEXTS = ['system']


IS_WINDOWS = os.name == 'nt'
IS_LINUX = platform.system().lower() == 'linux'
SERIAL_UNAVAILABLE = "<Unable to Retrieve S/N>"


def _normalize_identity_text(value) -> str:
    try:
        text = str(value or '').strip()
    except Exception:
        return ''
    if not text:
        return ''
    lowered = text.lower()
    invalid_values = {
        'none',
        'null',
        'unknown',
        'n/a',
        'na',
        'not available',
        'not specified',
        'default string',
        'to be filled by o.e.m.',
        'to be filled by oem',
    }
    return '' if lowered in invalid_values else text


def _is_invalid_serial(value: str) -> bool:
    text = _normalize_identity_text(value)
    if not text:
        return True
    lowered = text.lower()
    if lowered in {
        'system serial number',
        'serial number',
        '123456789',
        '1234567890',
        '0123456789',
    }:
        return True
    compact = re.sub(r'[^a-z0-9]+', '', lowered)
    if compact in {'', '0', 'none', 'null', 'unknown', 'na'}:
        return True
    return False


def _first_valid_serial(*candidates) -> str:
    for candidate in candidates:
        if isinstance(candidate, (list, tuple, set)):
            nested = _first_valid_serial(*list(candidate))
            if nested:
                return nested
            continue
        text = _normalize_identity_text(candidate)
        if text and not _is_invalid_serial(text):
            return text
    return ''


def _read_identity_file(path: str) -> str:
    try:
        with open(path, 'r', encoding='utf-8', errors='ignore') as fh:
            return (fh.read() or '').strip()
    except Exception:
        return ''


def _run_command(args, timeout: int = 15) -> str:
    try:
        out = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
        if out.returncode == 0:
            return (out.stdout or '').strip()
    except Exception:
        pass
    return ''


def _normalize_hostname_for_display(value) -> str:
    try:
        text = str(value or '').strip()
    except Exception:
        return ''
    if '.' in text:
        return text.split('.', 1)[0]
    return text


def _local_hostname() -> str:
    try:
        return str(socket.gethostname() or '').strip()
    except Exception:
        return ''


def _read_os_release() -> dict:
    data = {}
    try:
        with open('/etc/os-release', 'r', encoding='utf-8', errors='ignore') as fh:
            for raw_line in fh:
                line = raw_line.strip()
                if not line or line.startswith('#') or '=' not in line:
                    continue
                key, value = line.split('=', 1)
                key = key.strip()
                value = value.strip().strip('"').strip("'")
                data[key] = value
    except Exception:
        pass
    return data


def _linux_os_pretty_name() -> str:
    info = _read_os_release()
    pretty = (info.get('PRETTY_NAME') or '').strip()
    if pretty:
        return pretty
    name = (info.get('NAME') or '').strip()
    version = (info.get('VERSION_ID') or info.get('VERSION') or '').strip()
    if name and version:
        return f"{name} {version}".strip()
    return name


def _parse_size_to_bytes(raw_value: str) -> int:
    text = (raw_value or '').strip()
    if not text:
        return 0
    lowered = text.lower()
    if 'no module installed' in lowered or lowered in {'unknown', 'n/a', 'na'}:
        return 0

    match = re.match(r'^(\d+(?:\.\d+)?)\s*([kmgt]?b)$', lowered)
    if match:
        value = float(match.group(1))
        unit = match.group(2)
        multipliers = {
            'b': 1,
            'kb': 1024,
            'mb': 1024 ** 2,
            'gb': 1024 ** 3,
            'tb': 1024 ** 4,
        }
        mult = multipliers.get(unit, 1)
        return int(value * mult)

    compact = re.sub(r'[^0-9]', '', text)
    if compact:
        try:
            return int(compact)
        except Exception:
            return 0
    return 0


def _linux_mac_for_interface(name: str) -> str:
    if not name:
        return 'unknown'
    mac = _read_identity_file(f'/sys/class/net/{name}/address')
    return mac or 'unknown'


def _linux_link_speed_for_interface(name: str) -> str:
    if not name:
        return ''
    speed = _read_identity_file(f'/sys/class/net/{name}/speed')
    if not speed:
        return ''
    try:
        value = int(str(speed).strip())
        if value > 0:
            return f"{value} Mb/s"
    except Exception:
        pass
    return ''


def _linux_is_virtual_machine() -> bool:
    if platform.system().lower() != 'linux':
        return False

    detected = _run_command(['systemd-detect-virt'], timeout=5).strip().lower()
    if detected and detected not in {'none', 'wsl'}:
        return True

    hints = [
        _read_identity_file('/sys/class/dmi/id/sys_vendor'),
        _read_identity_file('/sys/class/dmi/id/product_name'),
        _read_identity_file('/sys/class/dmi/id/product_version'),
    ]
    combined = ' '.join([str(v or '') for v in hints]).lower()
    vm_tokens = (
        'virtual',
        'vmware',
        'kvm',
        'xen',
        'qemu',
        'hyper-v',
        'hyperv',
        'virtualbox',
        'bhyve',
        'parallels',
    )
    return any(token in combined for token in vm_tokens)


def collect_device_identity() -> dict:
    manufacturer = ''
    model = ''
    serial_candidates = []
    try:
        plat = platform.system().lower()
        if plat == 'windows':
            ps = r"""
$ErrorActionPreference = 'SilentlyContinue'
function _getCim($cls){
  try { return Get-CimInstance $cls -ErrorAction Stop }
  catch { try { return Get-WmiObject -Class $cls -ErrorAction Stop } catch { return $null } }
}
$cs = _getCim 'Win32_ComputerSystem'
$bios = _getCim 'Win32_BIOS'
$bb = _getCim 'Win32_BaseBoard'
$enc = _getCim 'Win32_SystemEnclosure'
[pscustomobject]@{
  Manufacturer = if ($cs) { [string]$cs.Manufacturer } else { '' }
  Model = if ($cs) { [string]$cs.Model } else { '' }
  BiosSerial = if ($bios) { [string]$bios.SerialNumber } else { '' }
  BoardSerial = if ($bb) { [string]$bb.SerialNumber } else { '' }
  ChassisSerial = if ($enc) { [string]$enc.SerialNumber } else { '' }
} | ConvertTo-Json -Compress
"""
            data = _ps_json(ps, timeout=20)
            if isinstance(data, dict):
                manufacturer = data.get('Manufacturer') or ''
                model = data.get('Model') or ''
                serial_candidates.extend(
                    [
                        data.get('BiosSerial'),
                        data.get('BoardSerial'),
                        data.get('ChassisSerial'),
                    ]
                )
        elif plat == 'linux':
            manufacturer = _read_identity_file('/sys/class/dmi/id/sys_vendor')
            model = _read_identity_file('/sys/class/dmi/id/product_name') or _read_identity_file(
                '/sys/class/dmi/id/product_version'
            )
            serial_candidates.extend(
                [
                    _read_identity_file('/sys/class/dmi/id/product_serial'),
                    _read_identity_file('/sys/class/dmi/id/board_serial'),
                    _read_identity_file('/sys/class/dmi/id/chassis_serial'),
                    _read_identity_file('/sys/class/dmi/id/product_uuid'),
                ]
            )
            if (not manufacturer or not model or not _first_valid_serial(serial_candidates)) and shutil.which('dmidecode'):
                try:
                    out = subprocess.run(
                        ['dmidecode', '-t', 'system'],
                        capture_output=True,
                        text=True,
                        timeout=20,
                    )
                    for raw_line in (out.stdout or '').splitlines():
                        if ':' not in raw_line:
                            continue
                        key, value = raw_line.split(':', 1)
                        key = key.strip().lower()
                        value = value.strip()
                        if not value:
                            continue
                        if key == 'manufacturer' and not manufacturer:
                            manufacturer = value
                        elif key == 'product name' and not model:
                            model = value
                        elif key in ('serial number', 'uuid'):
                            serial_candidates.append(value)
                except Exception:
                    pass
            if not _first_valid_serial(serial_candidates):
                serial_candidates.append(_read_identity_file('/etc/machine-id'))
        elif plat == 'darwin':
            out = subprocess.run(
                ["system_profiler", "SPHardwareDataType"],
                capture_output=True,
                text=True,
                timeout=15,
            )
            for line in (out.stdout or '').splitlines():
                if ':' not in line:
                    continue
                key, value = line.split(':', 1)
                key = key.strip().lower()
                value = value.strip()
                if not value:
                    continue
                if key == 'model name':
                    manufacturer = manufacturer or 'Apple'
                    model = value
                elif key == 'model identifier' and not model:
                    manufacturer = manufacturer or 'Apple'
                    model = value
                elif key.startswith('serial number'):
                    serial_candidates.append(value)
    except Exception:
        pass

    manufacturer = _normalize_identity_text(manufacturer)
    model = _normalize_identity_text(model)
    combined_model = " ".join([manufacturer, model]).strip() or model or manufacturer or 'unknown'
    serial_number = _first_valid_serial(serial_candidates) or SERIAL_UNAVAILABLE
    return {
        'manufacturer': manufacturer,
        'system_model': model,
        'device_model': model,
        'model': combined_model,
        'serial_number': serial_number,
        'serial': serial_number,
        'bios_serial': serial_number,
    }


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
                edition_id = _get("EditionID", "")
                installation_type = _get("InstallationType", "")

                wmi_info = {}
                try:
                    cmd = "Get-CimInstance Win32_OperatingSystem | Select-Object Caption,ProductType,BuildNumber | ConvertTo-Json -Compress"
                    out = subprocess.run(
                        ["powershell", "-NoProfile", "-Command", cmd],
                        capture_output=True,
                        text=True,
                        timeout=5,
                    )
                    raw = (out.stdout or "").strip()
                    if raw:
                        data = json.loads(raw)
                        if isinstance(data, list):
                            data = data[0] if data else {}
                        if isinstance(data, dict):
                            wmi_info = data
                except Exception:
                    wmi_info = {}

                wmi_caption = ""
                caption_val = wmi_info.get("Caption")
                if isinstance(caption_val, str):
                    wmi_caption = caption_val.strip()
                    if wmi_caption.lower().startswith("microsoft "):
                        wmi_caption = wmi_caption[10:].strip()

                def _parse_int(value) -> int:
                    try:
                        return int(str(value).split(".")[0])
                    except Exception:
                        return 0

                build_int = 0
                for candidate in (build_number, wmi_info.get("BuildNumber")):
                    if candidate:
                        parsed = _parse_int(candidate)
                        if parsed:
                            build_int = parsed
                            break

                if not build_int:
                    try:
                        build_int = _parse_int(sys.getwindowsversion().build)  # type: ignore[attr-defined]
                    except Exception:
                        build_int = 0

                product_type_val = wmi_info.get("ProductType")
                if isinstance(product_type_val, str):
                    try:
                        product_type_val = int(product_type_val.strip())
                    except Exception:
                        product_type_val = None
                if not isinstance(product_type_val, int):
                    try:
                        product_type_val = getattr(sys.getwindowsversion(), 'product_type', None)  # type: ignore[attr-defined]
                    except Exception:
                        product_type_val = None
                if not isinstance(product_type_val, int):
                    product_type_val = 0

                def _contains_server(text) -> bool:
                    try:
                        return isinstance(text, str) and 'server' in text.lower()
                    except Exception:
                        return False

                server_hints = []
                if isinstance(product_type_val, int) and product_type_val not in (0, 1):
                    server_hints.append("product_type")
                if isinstance(product_type_val, int) and product_type_val == 1 and _contains_server(product_name):
                    # Some environments misreport ProductType; prefer explicit product hints
                    server_hints.append("product_type_mismatch")
                for hint in (product_name, wmi_caption, edition_id, installation_type):
                    if _contains_server(hint):
                        server_hints.append("string_hint")
                        break
                if installation_type and str(installation_type).strip().lower() == 'server':
                    server_hints.append("installation_type")
                if isinstance(edition_id, str) and edition_id.lower().startswith('server'):
                    server_hints.append("edition_id")
                if build_int in (20348, 26100, 17763) and _contains_server(product_name or wmi_caption or edition_id or installation_type):
                    server_hints.append("build_hint")

                is_server = bool(server_hints)

                if is_server:
                    if build_int >= 26100:
                        family = "Windows Server 2025"
                    elif build_int >= 20348:
                        family = "Windows Server 2022"
                    elif build_int >= 17763:
                        family = "Windows Server 2019"
                    else:
                        family = "Windows Server"
                else:
                    family = "Windows 11" if build_int >= 22000 else "Windows 10"

                if not family:
                    family = (product_name or wmi_caption or "Windows").strip()

                def _extract_edition(source: str) -> str:
                    if not isinstance(source, str):
                        return ""
                    text = source.strip()
                    if not text:
                        return ""
                    lower = text.lower()
                    if lower.startswith("microsoft "):
                        text = text[len("Microsoft "):].strip()
                        lower = text.lower()
                    fam_words = family.split()
                    source_words = text.split()
                    i = 0
                    while i < len(fam_words) and i < len(source_words):
                        if fam_words[i].lower() != source_words[i].lower():
                            break
                        i += 1
                    if i < len(fam_words):
                        return ""
                    if i >= len(source_words):
                        return ""
                    suffix = " ".join(source_words[i:]).strip()
                    if suffix.startswith("-"):
                        suffix = suffix[1:].strip()
                    return suffix

                def _edition_from_id(value: str, drop_server: bool) -> str:
                    if not isinstance(value, str):
                        return ""
                    text = value.replace("_", " ")
                    text = re.sub(r"(?<!^)(?=[A-Z])", " ", text)
                    text = re.sub(r"\bEdition\b", "", text, flags=re.IGNORECASE)
                    text = " ".join(text.split()).strip()
                    if drop_server and text.lower().startswith("server "):
                        text = text[7:].strip()
                    return text

                edition_part = _extract_edition(product_name) or _extract_edition(wmi_caption)
                if not edition_part:
                    edition_part = _edition_from_id(edition_id, is_server)

                version_label = ""
                for val in (display_version, release_id):
                    if isinstance(val, str) and val.strip():
                        version_label = val.strip()
                        break

                if isinstance(ubr, int):
                    build_str = f"{build_number}.{ubr}" if build_number else str(ubr)
                else:
                    try:
                        build_str = f"{build_number}.{int(ubr)}" if build_number and ubr else (build_number or "")
                    except Exception:
                        build_str = build_number or ""

                parts = [family]
                if edition_part:
                    parts.append(edition_part)
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
                if name or ver:
                    return f"{name} {ver}".strip()
            except Exception:
                pass
            try:
                pretty = _linux_os_pretty_name()
                if pretty:
                    return pretty
            except Exception:
                pass
            return platform.platform()
    except Exception:
        return "Unknown"


def collect_summary(CONFIG):
    try:
        raw_hostname = socket.gethostname()
        hostname = str(raw_hostname or '').strip() or _local_hostname()
        short_hostname = _normalize_hostname_for_display(hostname)
        domain = os.environ.get('USERDOMAIN') or ''
        if not domain and not IS_WINDOWS:
            try:
                fqdn = socket.getfqdn()
                if fqdn and '.' in fqdn:
                    domain = fqdn.split('.', 1)[1]
            except Exception:
                pass
            if not domain and raw_hostname and '.' in raw_hostname:
                domain = raw_hostname.split('.', 1)[1]
        summary = {
            'hostname': hostname,
            'os': detect_agent_os(),
            'username': os.environ.get('USERNAME') or os.environ.get('USER') or '',
            'domain': domain,
            'uptime_sec': int(time.time() - psutil.boot_time()) if psutil else None,
        }
        if short_hostname and short_hostname != hostname:
            summary['hostname_short'] = short_hostname
        return summary
    except Exception:
        hostname = _local_hostname()
        summary = {
            'hostname': hostname,
        }
        short_hostname = _normalize_hostname_for_display(hostname)
        if short_hostname and short_hostname != hostname:
            summary['hostname_short'] = short_hostname
        return summary


# Removed Ansible-based audit path; Python collectors provide details directly.


def _ps_json(cmd: str, timeout: int = 60):
    try:
        out = subprocess.run(["powershell", "-NoProfile", "-Command", cmd], capture_output=True, text=True, timeout=timeout)
        txt = out.stdout or ""
        if txt.strip():
            try:
                data = json.loads(txt)
                return data
            except Exception:
                # Sometimes PS emits BOM or warnings; try to find JSON block
                try:
                    start = txt.find('[{')
                    if start == -1:
                        start = txt.find('{')
                    end = txt.rfind('}')
                    if start != -1 and end != -1 and end > start:
                        return json.loads(txt[start:end+1])
                except Exception:
                    pass
        return None
    except Exception:
        return None


def collect_software():
    plat = platform.system().lower()
    def _dedupe_and_sort(rows):
        deduped = {}
        for row in rows or []:
            if not isinstance(row, dict):
                continue
            name = str(row.get('name') or '').strip()
            if not name:
                continue
            version = str(row.get('version') or '').strip()
            source = str(row.get('source') or 'local_installed').strip().lower() or 'local_installed'
            payload = {
                'name': name,
                'version': version,
                'source': source,
            }
            metadata = row.get('metadata') if isinstance(row.get('metadata'), dict) else {}
            if metadata:
                payload['metadata'] = metadata
            deduped[(name.lower(), version.lower(), source)] = payload
        return sorted(
            deduped.values(),
            key=lambda item: (
                str(item.get('name') or '').lower(),
                str(item.get('source') or '').lower(),
                str(item.get('version') or '').lower(),
            ),
        )

    if plat == 'linux':
        packages = []
        try:
            if shutil.which('dpkg-query'):
                out = subprocess.run(
                    ['dpkg-query', '-W', '-f=${Package}\t${Version}\n'],
                    capture_output=True,
                    text=True,
                    timeout=120,
                )
                for line in (out.stdout or '').splitlines():
                    parts = line.split('\t', 1)
                    name = (parts[0] if parts else '').strip()
                    version = (parts[1] if len(parts) > 1 else '').strip()
                    if name:
                        packages.append({'name': name, 'version': version, 'source': 'dpkg'})
            elif shutil.which('rpm'):
                out = subprocess.run(
                    ['rpm', '-qa', '--qf', '%{NAME}\t%{VERSION}-%{RELEASE}\n'],
                    capture_output=True,
                    text=True,
                    timeout=120,
                )
                for line in (out.stdout or '').splitlines():
                    parts = line.split('\t', 1)
                    name = (parts[0] if parts else '').strip()
                    version = (parts[1] if len(parts) > 1 else '').strip()
                    if name:
                        packages.append({'name': name, 'version': version, 'source': 'rpm'})
        except Exception:
            packages = []

        return _dedupe_and_sort(packages)

    if plat != 'windows':
        return []
    # 1) Try PowerShell registry scrape (fast when ConvertTo-Json is available)
    try:
        ps = r"""
$paths = @(
  'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',
  'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*',
  'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'
)
$list = @()
foreach ($p in $paths) {
  try {
    $list += Get-ItemProperty -Path $p -ErrorAction SilentlyContinue |
      Select-Object DisplayName, DisplayVersion, Publisher, InstallLocation, InstallDate
  } catch {}
}
$list = $list | Where-Object { $_.DisplayName -and ("$($_.DisplayName)".Trim().Length -gt 0) }
$list | Sort-Object DisplayName -Unique | ConvertTo-Json -Depth 2
"""
        data = _ps_json(ps, timeout=120)
        out = []
        if isinstance(data, dict):
            data = [data]
        for it in (data or []):
            name = str(it.get('DisplayName') or '').strip()
            if not name:
                continue
            ver = str(it.get('DisplayVersion') or '').strip()
            metadata = {}
            publisher = str(it.get('Publisher') or '').strip()
            if publisher:
                metadata['publisher'] = publisher
            install_location = str(it.get('InstallLocation') or '').strip()
            if install_location:
                metadata['install_location'] = install_location
            install_date = str(it.get('InstallDate') or '').strip()
            if install_date:
                metadata['install_date'] = install_date
            payload = {'name': name, 'version': ver, 'source': 'local_installed'}
            if metadata:
                payload['metadata'] = metadata
            out.append(payload)
        try:
            appx_ps = r"""
$list = @()
try {
  $list = Get-AppxPackage -AllUsers -ErrorAction Stop |
    Where-Object { -not $_.IsFramework -and -not $_.IsResourcePackage } |
    Select-Object Name, Version, Publisher, InstallLocation, PackageFamilyName
} catch {
  try {
    $list = Get-AppxPackage -ErrorAction SilentlyContinue |
      Where-Object { -not $_.IsFramework -and -not $_.IsResourcePackage } |
      Select-Object Name, Version, Publisher, InstallLocation, PackageFamilyName
  } catch {}
}
$list | Sort-Object Name -Unique | ConvertTo-Json -Depth 3
"""
            appx_data = _ps_json(appx_ps, timeout=120)
            if isinstance(appx_data, dict):
                appx_data = [appx_data]
            for it in (appx_data or []):
                name = str(it.get('Name') or '').strip()
                if not name:
                    continue
                ver = str(it.get('Version') or '').strip()
                metadata = {}
                publisher = str(it.get('Publisher') or '').strip()
                if publisher:
                    metadata['publisher'] = publisher
                install_location = str(it.get('InstallLocation') or '').strip()
                if install_location:
                    metadata['install_location'] = install_location
                family_name = str(it.get('PackageFamilyName') or '').strip()
                if family_name:
                    metadata['package_family_name'] = family_name
                payload = {'name': name, 'version': ver, 'source': 'windows_store'}
                if metadata:
                    payload['metadata'] = metadata
                out.append(payload)
        except Exception:
            pass
        if out:
            return _dedupe_and_sort(out)
    except Exception:
        pass

    # 2) Fallback: read registry directly via Python winreg (works on Win7+)
    try:
        try:
            import winreg  # type: ignore
        except Exception:
            return []

        def _enum_uninstall(root, path, wow_flag=0):
            items = []
            try:
                key = winreg.OpenKey(root, path, 0, winreg.KEY_READ | wow_flag)
            except Exception:
                return items
            try:
                i = 0
                while True:
                    try:
                        sub = winreg.EnumKey(key, i)
                    except OSError:
                        break
                    i += 1
                    try:
                        sk = winreg.OpenKey(key, sub, 0, winreg.KEY_READ | wow_flag)
                        try:
                            name, _ = winreg.QueryValueEx(sk, 'DisplayName')
                        except Exception:
                            name = ''
                        if name and str(name).strip():
                            try:
                                ver, _ = winreg.QueryValueEx(sk, 'DisplayVersion')
                            except Exception:
                                ver = ''
                            metadata = {}
                            try:
                                publisher, _ = winreg.QueryValueEx(sk, 'Publisher')
                                if publisher:
                                    metadata['publisher'] = str(publisher).strip()
                            except Exception:
                                pass
                            try:
                                install_location, _ = winreg.QueryValueEx(sk, 'InstallLocation')
                                if install_location:
                                    metadata['install_location'] = str(install_location).strip()
                            except Exception:
                                pass
                            payload = {
                                'name': str(name).strip(),
                                'version': str(ver or '').strip(),
                                'source': 'local_installed',
                            }
                            if metadata:
                                payload['metadata'] = metadata
                            items.append(payload)
                    except Exception:
                        continue
            except Exception:
                pass
            return items

        HKLM = getattr(winreg, 'HKEY_LOCAL_MACHINE')
        HKCU = getattr(winreg, 'HKEY_CURRENT_USER')
        WOW64_64 = getattr(winreg, 'KEY_WOW64_64KEY', 0)
        WOW64_32 = getattr(winreg, 'KEY_WOW64_32KEY', 0)
        paths = [
            (HKLM, r"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall", WOW64_64),
            (HKLM, r"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall", WOW64_32),
            (HKLM, r"SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall", 0),
            (HKCU, r"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall", 0),
        ]
        merged = {}
        for root, path, flag in paths:
            for it in _enum_uninstall(root, path, flag):
                name_key = (it.get('name') or '').lower()
                if not name_key:
                    continue
                key = (name_key, (it.get('version') or '').lower(), (it.get('source') or 'local_installed'))
                if key not in merged:
                    merged[key] = it
        return _dedupe_and_sort(list(merged.values()))
    except Exception:
        return []


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
        elif plat == 'linux':
            # Try per-module memory details (requires dmidecode privileges on many systems).
            if shutil.which('dmidecode'):
                try:
                    out = subprocess.run(
                        ['dmidecode', '-t', '17'],
                        capture_output=True,
                        text=True,
                        timeout=30,
                    )
                    current = None
                    for raw_line in (out.stdout or '').splitlines():
                        line = raw_line.strip()
                        if line.startswith('Memory Device'):
                            if current and int(current.get('capacity') or 0) > 0:
                                entries.append(current)
                            current = {'slot': 'unknown', 'speed': 'unknown', 'serial': 'unknown', 'capacity': 0}
                            continue
                        if not current or ':' not in line:
                            continue
                        key, value = line.split(':', 1)
                        key = key.strip().lower()
                        value = value.strip()
                        if key == 'locator' and value:
                            current['slot'] = value
                        elif key == 'speed' and value and value.lower() != 'unknown':
                            current['speed'] = value
                        elif key == 'serial number' and value and value.lower() != 'unknown':
                            current['serial'] = value
                        elif key == 'size':
                            current['capacity'] = _parse_size_to_bytes(value)
                    if current and int(current.get('capacity') or 0) > 0:
                        entries.append(current)
                except Exception:
                    pass

            if not entries:
                mem_total_kib = 0
                try:
                    with open('/proc/meminfo', 'r', encoding='utf-8', errors='ignore') as fh:
                        for raw_line in fh:
                            if raw_line.startswith('MemTotal:'):
                                parts = raw_line.split()
                                if len(parts) >= 2:
                                    mem_total_kib = int(parts[1])
                                break
                except Exception:
                    mem_total_kib = 0
                if mem_total_kib > 0:
                    entries.append(
                        {
                            'slot': 'physical',
                            'speed': 'unknown',
                            'serial': 'unknown',
                            'capacity': int(mem_total_kib) * 1024,
                        }
                    )
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
        plat = platform.system().lower()
        if psutil:
            seen_mounts = set()
            linux_skip_fstypes = {
                'tmpfs',
                'devtmpfs',
                'overlay',
                'squashfs',
                'proc',
                'sysfs',
                'cgroup',
                'cgroup2',
                'nsfs',
                'mqueue',
                'fusectl',
                'tracefs',
                'debugfs',
                'configfs',
                'securityfs',
                'pstore',
                'efivarfs',
                'ramfs',
            }
            for part in psutil.disk_partitions():
                mountpoint = (part.mountpoint or '').strip()
                fstype = (part.fstype or '').strip().lower()
                if plat == 'linux':
                    if not mountpoint or mountpoint in seen_mounts:
                        continue
                    if fstype in linux_skip_fstypes:
                        continue
                    if mountpoint.startswith('/snap/'):
                        continue
                    seen_mounts.add(mountpoint)
                try:
                    usage = psutil.disk_usage(part.mountpoint)
                except Exception:
                    continue
                disks.append({
                    'drive': part.device or mountpoint,
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
        if IS_WINDOWS:
            # Try modern Get-NetIPAddress; fallback to ipconfig parsing (Win7)
            try:
                ps_cmd = (
                    "try { "
                    "$ip = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction Stop | "
                    "Where-Object { $_.IPAddress -and $_.IPAddress -notmatch '^169\\.254\\.' -and $_.IPAddress -ne '127.0.0.1' }; "
                    "$ad = Get-NetAdapter | ForEach-Object { $_ | Select-Object -Property InterfaceAlias, MacAddress, LinkSpeed }; "
                    "$map = @{}; foreach($a in $ad){ $map[$a.InterfaceAlias] = @{ Mac=$a.MacAddress; LinkSpeed=('' + $a.LinkSpeed).Trim() } }; "
                    "$out = @(); foreach($e in $ip){ $m = $map[$e.InterfaceAlias]; $mac = $m.Mac; $ls = $m.LinkSpeed; $out += [pscustomobject]@{ InterfaceAlias=$e.InterfaceAlias; IPAddress=$e.IPAddress; MacAddress=$mac; LinkSpeed=$ls } } "
                    "$out | ConvertTo-Json -Depth 3 } catch { '' }"
                )
                data = _ps_json(ps_cmd, timeout=60)
                if isinstance(data, dict):
                    data = [data]
                tmp = {}
                for e in (data or []):
                    alias = e.get('InterfaceAlias') or 'unknown'
                    ip = e.get('IPAddress') or ''
                    mac = e.get('MacAddress') or 'unknown'
                    link = e.get('LinkSpeed') or ''
                    if not ip:
                        continue
                    item = tmp.setdefault(alias, {'adapter': alias, 'ips': [], 'mac': mac, 'link_speed': link})
                    if ip not in item['ips']:
                        item['ips'].append(ip)
                if tmp:
                    adapters = list(tmp.values())
                else:
                    raise Exception('empty')
            except Exception:
                # Win7/older fallback: parse ipconfig
                try:
                    out = subprocess.run(["ipconfig"], capture_output=True, text=True, timeout=30)
                    cur = None
                    for line in (out.stdout or '').splitlines():
                        s = line.strip()
                        if not s:
                            continue
                        if s.endswith(":") and ('adapter' in s.lower() or 'ethernet' in s.lower() or 'wireless' in s.lower()):
                            cur = {'adapter': s.replace(':','').strip(), 'ips': [], 'mac': 'unknown'}
                            adapters.append(cur)
                            continue
                        if s.lower().startswith('ipv4 address') or s.lower().startswith('ipv4-adresse') or 'ipv4' in s.lower():
                            try:
                                ip = s.split(':')[-1].strip()
                            except Exception:
                                ip = ''
                            if ip and not ip.startswith('169.254.') and ip != '127.0.0.1' and cur:
                                cur['ips'].append(ip)
                        if s.lower().startswith('physical address') or s.lower().startswith('mac address'):
                            try:
                                mac = s.split(':')[-1].strip()
                            except Exception:
                                mac = ''
                            if mac and cur:
                                cur['mac'] = mac
                except Exception:
                    pass
        else:
            if shutil.which('ip'):
                out = subprocess.run(
                    ["ip", "-o", "-4", "addr", "show", "up", "scope", "global"],
                    capture_output=True,
                    text=True,
                    timeout=60,
                )
                tmp = {}
                for line in (out.stdout or '').splitlines():
                    parts = line.split()
                    if len(parts) < 4:
                        continue
                    name = (parts[1] or '').split('@', 1)[0]
                    ip = parts[3].split("/", 1)[0]
                    if not name or not ip or ip.startswith('169.254.') or ip == '127.0.0.1':
                        continue
                    item = tmp.setdefault(
                        name,
                        {
                            'adapter': name,
                            'ips': [],
                            'mac': _linux_mac_for_interface(name),
                            'link_speed': _linux_link_speed_for_interface(name),
                        },
                    )
                    if ip not in item['ips']:
                        item['ips'].append(ip)
                if tmp:
                    adapters = list(tmp.values())

            if not adapters and psutil:
                try:
                    for name, addrs in psutil.net_if_addrs().items():
                        ips = []
                        mac = 'unknown'
                        for addr in addrs:
                            fam = getattr(addr, 'family', None)
                            value = getattr(addr, 'address', '')
                            if fam == socket.AF_INET and value and value != '127.0.0.1' and not value.startswith('169.254.'):
                                ips.append(value)
                            elif str(fam).endswith('AF_LINK') or str(fam).endswith('AF_PACKET'):
                                if value:
                                    mac = value
                        if ips:
                            adapters.append({'adapter': name, 'ips': ips, 'mac': mac, 'link_speed': ''})
                except Exception:
                    pass
    except Exception:
        pass
    return adapters


def collect_cpu() -> dict:
    """Collect CPU model, cores, and base clock (best-effort cross-platform)."""
    out: dict = {}
    try:
        plat = platform.system().lower()
        if plat == 'windows':
            try:
                ps_cmd = (
                    "Get-CimInstance Win32_Processor | "
                    "Select-Object Name, NumberOfCores, NumberOfLogicalProcessors, MaxClockSpeed | ConvertTo-Json"
                )
                data = _ps_json(ps_cmd, timeout=15)
                if isinstance(data, dict):
                    data = [data]
                name = ''
                phys = 0
                logi = 0
                mhz = 0
                for idx, cpu in enumerate(data or []):
                    if idx == 0:
                        name = str(cpu.get('Name') or '')
                        try:
                            mhz = int(cpu.get('MaxClockSpeed') or 0)
                        except Exception:
                            mhz = 0
                    try:
                        phys += int(cpu.get('NumberOfCores') or 0)
                    except Exception:
                        pass
                    try:
                        logi += int(cpu.get('NumberOfLogicalProcessors') or 0)
                    except Exception:
                        pass
                out = {
                    'name': name.strip(),
                    'physical_cores': phys or None,
                    'logical_cores': logi or None,
                    'base_clock_ghz': (float(mhz) / 1000.0) if mhz else None,
                }
                return out
            except Exception:
                pass
        elif plat == 'darwin':
            try:
                name = subprocess.run(["sysctl", "-n", "machdep.cpu.brand_string"], capture_output=True, text=True, timeout=5).stdout.strip()
            except Exception:
                name = ''
            try:
                cores = int(subprocess.run(["sysctl", "-n", "hw.ncpu"], capture_output=True, text=True, timeout=5).stdout.strip() or '0')
            except Exception:
                cores = 0
            out = {'name': name, 'logical_cores': cores or None}
            return out
        else:
            # Linux
            try:
                brand = ''
                logical_cores = 0
                with open('/proc/cpuinfo', 'r', encoding='utf-8', errors='ignore') as fh:
                    for line in fh:
                        if not brand and 'model name' in line:
                            brand = line.split(':', 1)[-1].strip()
                        if 'processor' in line:
                            logical_cores += 1

                physical_cores = None
                if psutil and hasattr(psutil, 'cpu_count'):
                    try:
                        physical_cores = psutil.cpu_count(logical=False)
                    except Exception:
                        physical_cores = None

                base_clock_ghz = None
                if psutil and hasattr(psutil, 'cpu_freq'):
                    try:
                        freq = psutil.cpu_freq()
                        if freq:
                            mhz = float(freq.max or 0.0) or float(freq.current or 0.0)
                            if mhz > 0:
                                base_clock_ghz = mhz / 1000.0
                    except Exception:
                        base_clock_ghz = None
                if base_clock_ghz is None:
                    lscpu_out = _run_command(['lscpu'], timeout=10)
                    for raw_line in lscpu_out.splitlines():
                        line = raw_line.strip()
                        if ':' not in line:
                            continue
                        key, value = line.split(':', 1)
                        key = key.strip().lower()
                        val = value.strip()
                        if key in {'cpu max mhz', 'cpu mhz'} and val:
                            try:
                                base_clock_ghz = float(val) / 1000.0
                                break
                            except Exception:
                                continue

                out = {
                    'name': brand,
                    'physical_cores': physical_cores,
                    'logical_cores': logical_cores or None,
                    'base_clock_ghz': base_clock_ghz,
                }
                return out
            except Exception:
                pass
    except Exception:
        pass
    # psutil fallback
    try:
        if psutil:
            return {
                'name': platform.processor() or '',
                'physical_cores': psutil.cpu_count(logical=False) if hasattr(psutil, 'cpu_count') else None,
                'logical_cores': psutil.cpu_count(logical=True) if hasattr(psutil, 'cpu_count') else None,
            }
    except Exception:
        pass
    return out or {}

def detect_device_type():
    try:
        plat = platform.system().lower()
        if plat == 'linux':
            if _linux_is_virtual_machine():
                return 'Virtual Machine'
            if any(
                os.environ.get(name)
                for name in ('XDG_CURRENT_DESKTOP', 'DESKTOP_SESSION', 'DISPLAY', 'WAYLAND_DISPLAY')
            ):
                return 'Workstation'
            default_target = _run_command(['systemctl', 'get-default'], timeout=5).strip().lower()
            if default_target == 'graphical.target':
                return 'Workstation'
            return 'Server'
        if plat == 'darwin':
            return 'Workstation'
        if plat != 'windows':
            return ''
        ps = r"""
function _getCim($cls){ try { return Get-CimInstance $cls -ErrorAction Stop } catch { try { return Get-WmiObject -Class $cls -ErrorAction Stop } catch { return $null } } }
$os = _getCim 'Win32_OperatingSystem'
$cs = _getCim 'Win32_ComputerSystem'
$caption = ""; if ($os) { $caption = [string]$os.Caption }
$model = ""; if ($cs) { $model = [string]$cs.Model }
$manu  = ""; if ($cs) { $manu = [string]$cs.Manufacturer }
$virt = $false
if ($model -match 'Virtual' -or $manu -match 'Microsoft Corporation' -and $model -match 'Virtual Machine' -or $manu -match 'VMware' -or $manu -match 'innotek' -or $manu -match 'VirtualBox' -or $manu -match 'QEMU' -or $manu -match 'Xen' -or $manu -match 'Parallels') { $virt = $true }
if ($virt) { 'Virtual Machine' }
elseif ($caption -match 'Server') { 'Server' }
else { 'Workstation' }
"""
        out = subprocess.run(["powershell", "-NoProfile", "-Command", ps], capture_output=True, text=True, timeout=15)
        s = (out.stdout or '').strip()
        return s.splitlines()[0].strip() if s else ''
    except Exception:
        return ''


def _collect_last_user_linux() -> str:
    if platform.system().lower() != 'linux':
        return ''

    invalid = {'', 'reboot', 'shutdown', 'wtmp'}
    try:
        users = []
        out = subprocess.run(['who'], capture_output=True, text=True, timeout=10)
        for line in (out.stdout or '').splitlines():
            parts = line.split()
            if not parts:
                continue
            user = (parts[0] or '').strip()
            if user and user not in invalid and user not in users:
                users.append(user)
        if users:
            return ', '.join(users)
    except Exception:
        pass

    try:
        out = subprocess.run(['last', '-n', '5', '-w'], capture_output=True, text=True, timeout=10)
        for line in (out.stdout or '').splitlines():
            raw = line.strip()
            if not raw or raw.lower().startswith('wtmp begins'):
                continue
            user = raw.split()[0] if raw.split() else ''
            if user and user not in invalid:
                return user
    except Exception:
        pass
    return ''


def _collect_last_user_registry() -> str:
    if not IS_WINDOWS:
        return ''
    # Registry-first approach: LogonUI LastLoggedOnSAMUser / LastLoggedOnUser
    try:
        ps = r"""
$ErrorActionPreference = 'SilentlyContinue'
function Normalize-Sam([string]$s) {
  if ([string]::IsNullOrWhiteSpace($s)) { return '' }
  if ($s -match '\$$') { return '' }
  if ($s -like 'DWM-*' -or $s -like 'UMFD-*') { return '' }
  if ($s -eq 'SYSTEM' -or $s -eq 'LOCAL SERVICE' -or $s -eq 'NETWORK SERVICE' -or $s -eq 'ANONYMOUS LOGON') { return '' }
  return $s
}
$regPath = 'HKLM:\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Authentication\\LogonUI'
$sam = ''; $upn = ''
try { $sam = (Get-ItemProperty -Path $regPath -Name 'LastLoggedOnSAMUser' -ErrorAction Stop).LastLoggedOnSAMUser } catch {}
try { $upn = (Get-ItemProperty -Path $regPath -Name 'LastLoggedOnUser' -ErrorAction Stop).LastLoggedOnUser } catch {}
$user = Normalize-Sam $sam
if (-not $user) {
  $user = Normalize-Sam $upn
  if ($user -and $user -like '*@*') {
    $domDns = (Get-WmiObject Win32_ComputerSystem).Domain
    $domShort = ''
    if ($domDns) { $domShort = ($domDns -split '\\.')[0].ToUpper() }
    $parts = $user -split '@'
    if ($parts.Length -ge 1) {
      $u = $parts[0]
      if ($domShort) { $user = "$domShort\\$u" }
    }
  }
}
if ($user) { $user } else { '' }
"""
        out = subprocess.run(["powershell", "-NoProfile", "-Command", ps], capture_output=True, text=True, timeout=10)
        s = (out.stdout or '').strip()
        if s:
            return s.splitlines()[0].strip()
    except Exception:
        pass
    # Fallback to Python winreg lookup
    try:
        import winreg  # type: ignore
        key_path = r"SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Authentication\\LogonUI"
        def _qval(name):
            try:
                k = winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE, key_path, 0, winreg.KEY_READ | getattr(winreg, 'KEY_WOW64_64KEY', 0))
                try:
                    val, _ = winreg.QueryValueEx(k, name)
                finally:
                    winreg.CloseKey(k)
                return str(val or '').strip()
            except Exception:
                return ''
        sam = _qval('LastLoggedOnSAMUser')
        upn = _qval('LastLoggedOnUser')
        def _ok(s: str) -> bool:
            if not s:
                return False
            su = s.upper()
            return not (s.endswith('$') or su in ('SYSTEM','LOCAL SERVICE','NETWORK SERVICE','ANONYMOUS LOGON') or s.startswith('DWM-') or s.startswith('UMFD-'))
        if _ok(sam):
            return sam
        if _ok(upn):
            if '@' in upn:
                try:
                    user, dom = upn.split('@', 1)
                    dom_short = (dom.split('.')[0] or dom).upper()
                    return f"{dom_short}\\{user}"
                except Exception:
                    pass
            return upn
    except Exception:
        pass
    return ''


def _collect_last_user_string() -> str:
    if not IS_WINDOWS:
        return ''
    try:
        ps = r"""
$ErrorActionPreference = 'SilentlyContinue'
function Get-InteractiveUsers {
  $users = @()
  try {
    $ls = Get-CimInstance Win32_LogonSession | Where-Object { $_.LogonType -in 2,10 }
    foreach ($sess in $ls) {
      $accs = Get-CimAssociatedInstance -InputObject $sess -Association Win32_LoggedOnUser -ResultClassName Win32_Account
      foreach ($a in $accs) {
        if (-not $a -or -not $a.Name) { continue }
        $name = [string]$a.Name
        $domain = [string]$a.Domain
        if ($name -match '\$$') { continue }
        if ($domain -eq 'NT AUTHORITY' -or $domain -eq 'NT SERVICE') { continue }
        if ($name -like 'DWM-*' -or $name -like 'UMFD-*') { continue }
        if ($domain) { $users += ("{0}\{1}" -f $domain,$name) } else { $users += $name }
      }
    }
  } catch {}
  $users | Sort-Object -Unique
}

function Get-QuserUsers {
  $list=@()
  try {
    $q = (quser 2>$null) -split '\r?\n'
    foreach ($line in $q) {
      if (-not $line) { continue }
      if ($line -match '^USERNAME') { continue }
      $s = ($line -replace '^>','').Trim()
      if (-not $s) { continue }
      $parts = $s -split '\s+'
      if ($parts.Length -lt 1) { continue }
      $u = $parts[0]
      if (-not $u) { continue }
      if ($u -match '\$$') { continue }
      if ($u -like 'DWM-*' -or $u -like 'UMFD-*') { continue }
      $list += $u
    }
  } catch {}
  $list | Sort-Object -Unique
}

$u1 = Get-InteractiveUsers
$u2 = Get-QuserUsers
$combined = @()
foreach ($u in $u1) { if ($combined -notcontains $u) { $combined += $u } }
foreach ($u in $u2) { if ($combined -notcontains $u) { $combined += $u } }
if ($combined.Count -eq 0) { 'No Users Logged In' } else { $combined -join ', ' }
"""
        out = subprocess.run(["powershell", "-NoProfile", "-Command", ps], capture_output=True, text=True, timeout=25)
        s = (out.stdout or '').strip()
        # PowerShell may emit newlines; take first non-empty line result
        if s:
            for line in s.splitlines():
                t = line.strip()
                if t:
                    return t
        return ''
    except Exception:
        return ''


def _build_details_fallback() -> dict:
    # Construct a details object similar to Ansible playbook output
    try:
        summary = collect_summary(type('C', (), {'data': {}, '_write': lambda s: None})())
    except Exception:
        hostname = _local_hostname()
        summary = {'hostname': hostname}
        short_hostname = _normalize_hostname_for_display(hostname)
        if short_hostname and short_hostname != hostname:
            summary['hostname_short'] = short_hostname
    # Normalize OS field
    if summary.get('os') and not summary.get('operating_system'):
        summary['operating_system'] = summary.get('os')
    # Last reboot in UTC string
    try:
        if psutil and hasattr(psutil, 'boot_time'):
            from datetime import datetime, timezone
            summary['last_reboot'] = datetime.fromtimestamp(psutil.boot_time(), timezone.utc).strftime('%Y-%m-%d %H:%M:%S')
    except Exception:
        pass
    # Device type
    try:
        dt = detect_device_type()
        if dt:
            summary['device_type'] = dt
    except Exception:
        pass
    # Network
    network = collect_network()
    try:
        # Derive internal_ip from first private IPv4
        def is_private(ip: str) -> bool:
            try:
                return ipaddress.ip_address(ip).is_private
            except Exception:
                return False
        ips = []
        for a in network:
            for ip in (a.get('ips') or []):
                if ip and is_private(ip):
                    ips.append(ip)
        summary['internal_ip'] = ips[0] if ips else (network[0]['ips'][0] if network and network[0].get('ips') else '')
    except Exception:
        pass
    # External IP best-effort
    try:
        ext = ''
        for url in ('https://api.ipify.org', 'https://checkip.amazonaws.com'):
            try:
                import urllib.request
                with urllib.request.urlopen(url, timeout=3) as resp:
                    txt = (resp.read() or b'').decode('utf-8', errors='ignore').strip()
                    if txt and '\n' in txt:
                        txt = txt.split('\n', 1)[0].strip()
                    if '{' in txt:
                        try:
                            obj = json.loads(txt); txt = (obj.get('ip') or '').strip()
                        except Exception:
                            pass
                    if txt:
                        ext = txt; break
            except Exception:
                continue
        if ext:
            summary['external_ip'] = ext
    except Exception:
        pass
    # Last user(s)
    try:
        if IS_WINDOWS:
            last_user = _collect_last_user_registry()
        else:
            last_user = _collect_last_user_linux()
        if last_user:
            summary['last_user'] = last_user
    except Exception:
        pass

    # Device identity (manufacturer, model, serial)
    identity = {}
    try:
        identity = collect_device_identity()
        manufacturer = (identity.get('manufacturer') or '').strip()
        system_model = (identity.get('system_model') or '').strip()
        combined_model = (identity.get('model') or '').strip()
        serial_number = (identity.get('serial_number') or '').strip()
        if manufacturer:
            summary['manufacturer'] = manufacturer
        if system_model:
            summary['system_model'] = system_model
            summary.setdefault('device_model', system_model)
        if combined_model:
            summary['model'] = combined_model
        if serial_number:
            summary['serial_number'] = serial_number
            summary.setdefault('serial', serial_number)
            summary.setdefault('bios_serial', serial_number)
    except Exception:
        identity = {}

    # CPU information (summary + display string)
    try:
        cpu = collect_cpu() or {}
        if identity:
            manufacturer = (identity.get('manufacturer') or '').strip()
            system_model = (identity.get('system_model') or '').strip()
            combined_model = (identity.get('model') or '').strip()
            serial_number = (identity.get('serial_number') or '').strip()
            if manufacturer:
                cpu.setdefault('system_manufacturer', manufacturer)
            if system_model:
                cpu.setdefault('system_model_raw', system_model)
            if combined_model:
                cpu.setdefault('system_model', combined_model)
            if serial_number:
                cpu.setdefault('system_serial_number', serial_number)
        if cpu:
            summary['cpu'] = cpu
            cores = cpu.get('logical_cores') or cpu.get('physical_cores')
            ghz = cpu.get('base_clock_ghz')
            name = (cpu.get('name') or '').strip()
            parts = []
            if name:
                parts.append(name)
            if ghz:
                try:
                    parts.append(f"({float(ghz):.1f}GHz)")
                except Exception:
                    pass
            if cores:
                parts.append(f"@ {int(cores)} Cores")
            if parts:
                summary['processor'] = ' '.join(parts)
    except Exception:
        pass

    # Total RAM (bytes) for quick UI metrics
    try:
        total = 0
        mem_list = collect_memory()
        for m in (mem_list or []):
            try:
                total += int(m.get('capacity') or 0)
            except Exception:
                pass
        if not total and psutil:
            try:
                total = int(psutil.virtual_memory().total)
            except Exception:
                pass
        if total:
            summary['total_ram'] = total
    except Exception:
        pass

    details = {
        'summary': summary,
        'software': collect_software(),
        'memory': collect_memory(),
        'storage': collect_storage(),
        'network': network,
    }
    return details


class Role:
    def __init__(self, ctx):
        self.ctx = ctx
        self.role_health_label = "Device Audit"
        self._ext_ip = None
        self._ext_ip_ts = 0
        self._refresh_ts = 0
        self._last_details = None
        # OS is collected dynamically; do not persist in config
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

    def health_report(self) -> dict:
        task = getattr(self, 'task', None)
        if task is None:
            return {
                'status': 'unhealthy',
                'role_label': self.role_health_label,
                'detail': 'Audit reporter task was not created.',
            }
        if task.done():
            return {
                'status': 'unhealthy',
                'role_label': self.role_health_label,
                'detail': 'Audit reporter task stopped.',
            }
        return {
            'status': 'healthy',
            'role_label': self.role_health_label,
            'detail': 'Audit reporter active.',
        }

    async def _report_loop(self):
        interval_sec = 300  # post heartbeat/details every 5 minutes
        while True:
            try:
                # Determine audit refresh interval (minutes), default 30
                try:
                    refresh_min = int(self.ctx.config.data.get('audit_interval_minutes', 5))
                except Exception:
                    refresh_min = 5
                refresh_sec = max(300, refresh_min * 60)

                now = time.time()
                need_refresh = (not self._last_details) or ((now - self._refresh_ts) > refresh_sec)
                if need_refresh:
                    # Always collect via built-in Python collectors
                    details = _build_details_fallback()
                    # Best-effort fill of missing/renamed fields so UI is happy
                    try:
                        details = self._normalize_details(details)
                    except Exception:
                        pass
                    if details:
                        self._last_details = details
                        self._refresh_ts = now

                # Always post the latest available details (possibly cached)
                details_to_send = self._last_details or {'summary': collect_summary(self.ctx.config)}
                get_url = (self.ctx.hooks.get('get_server_url') if isinstance(self.ctx.hooks, dict) else None) or (lambda: 'http://localhost:5000')
                agent_build_id = ''
                if isinstance(self.ctx.hooks, dict):
                    build_id_getter = self.ctx.hooks.get('get_agent_build_id')
                    if callable(build_id_getter):
                        try:
                            agent_build_id = str(build_id_getter() or '').strip()
                        except Exception:
                            agent_build_id = ''
                details_to_send.setdefault('summary', {})
                if agent_build_id and not details_to_send['summary'].get('agent_build_id'):
                    details_to_send['summary']['agent_build_id'] = agent_build_id
                payload = {
                    'agent_id': self.ctx.agent_id,
                    'hostname': details_to_send.get('summary', {}).get('hostname', _local_hostname()),
                    'details': details_to_send,
                    'agent_build_id': agent_build_id,
                }
                client_factory = None
                if isinstance(self.ctx.hooks, dict):
                    client_factory = self.ctx.hooks.get('http_client')

                if callable(client_factory):
                    try:
                        client = client_factory()
                    except Exception:
                        client = None
                    else:
                        try:
                            await client.async_post_json("/api/agent/details", payload, require_auth=True)
                            await asyncio.sleep(interval_sec)
                            continue
                        except Exception:
                            pass

                if aiohttp is not None:
                    url = (get_url() or '').rstrip('/') + '/api/agent/details'
                    async with aiohttp.ClientSession() as session:
                        await session.post(url, json=payload, timeout=10)
            except Exception:
                pass
            await asyncio.sleep(interval_sec)

    def _normalize_details(self, details: dict) -> dict:
        if not isinstance(details, dict):
            return {}
        details.setdefault('summary', {})
        summary = details['summary']
        # Map legacy 'os' to 'operating_system'
        try:
            if not summary.get('operating_system') and summary.get('os'):
                summary['operating_system'] = summary.get('os')
        except Exception:
            pass

        # Device type fallback
        try:
            dt = (summary.get('device_type') or '').strip()
            if not dt:
                dt = detect_device_type() or ''
                if dt:
                    summary['device_type'] = dt
        except Exception:
            pass

        # Internal IP fallback from network list
        try:
            if not summary.get('internal_ip'):
                net = details.get('network') or []
                ipv4s = []
                for a in net:
                    for ip in (a.get('ips') or []):
                        try:
                            if ip and isinstance(ip, str) and ip.count('.') == 3 and not ip.startswith('169.254.') and ip != '127.0.0.1':
                                ipv4s.append(ip)
                        except Exception:
                            pass
                summary['internal_ip'] = ipv4s[0] if ipv4s else ''
        except Exception:
            pass

        # External IP best-effort (cache ~15 min) if still missing
        try:
            now = time.time()
            ext = (summary.get('external_ip') or '').strip()
            if not ext and (now - self._ext_ip_ts > 900):
                # lightweight fetch without blocking forever
                import urllib.request  # lazy import
                for url in (
                    'https://api.ipify.org',
                    'https://checkip.amazonaws.com',
                ):
                    try:
                        with urllib.request.urlopen(url, timeout=3) as resp:
                            txt = (resp.read() or b'').decode('utf-8', errors='ignore').strip()
                            if txt and '\n' in txt:
                                txt = txt.split('\n', 1)[0].strip()
                            # api.ipify.org returns plain IP by default; if JSON was served, handle a small case
                            if '{' in txt:
                                try:
                                    obj = json.loads(txt)
                                    txt = (obj.get('ip') or '').strip()
                                except Exception:
                                    pass
                            if txt:
                                self._ext_ip = txt
                                self._ext_ip_ts = now
                                break
                    except Exception:
                        continue
            if not ext and self._ext_ip:
                summary['external_ip'] = self._ext_ip
        except Exception:
            pass

        # Last reboot (UTC string) if missing/unknown
        try:
            val = (summary.get('last_reboot') or '').strip()
            if not val or val.lower() == 'unknown':
                if psutil and hasattr(psutil, 'boot_time'):
                    from datetime import datetime, timezone
                    summary['last_reboot'] = datetime.fromtimestamp(psutil.boot_time(), timezone.utc).strftime('%Y-%m-%d %H:%M:%S')
                elif IS_WINDOWS:
                    ps = (
                        "$b=(Get-CimInstance Win32_OperatingSystem).LastBootUpTime; "
                        "(Get-Date $b).ToUniversalTime().ToString('yyyy-MM-dd HH:mm:ss')"
                    )
                    out = subprocess.run(["powershell", "-NoProfile", "-Command", ps], capture_output=True, text=True, timeout=10)
                    s = (out.stdout or '').strip()
                    if s:
                        summary['last_reboot'] = s.splitlines()[0].strip()
        except Exception:
            pass

        # Last user fix-up: compute if missing/unknown or contains machine account entries
        try:
            lu = (summary.get('last_user') or '').strip()
            def _contains_machine_accounts(s: str) -> bool:
                try:
                    for part in s.split(','):
                        if part.strip().endswith('$'):
                            return True
                except Exception:
                    pass
                return False
            if (not lu) or (lu.lower() == 'unknown') or _contains_machine_accounts(lu):
                if IS_WINDOWS:
                    lu2 = _collect_last_user_registry().strip()
                else:
                    lu2 = _collect_last_user_linux().strip()
                summary['last_user'] = lu2 if lu2 else 'No Users Logged In'
        except Exception:
            pass

        # Device identity fix-up (manufacturer/model/serial)
        try:
            manufacturer = _normalize_identity_text(summary.get('manufacturer') or summary.get('vendor'))
            system_model = _normalize_identity_text(summary.get('system_model') or summary.get('device_model'))
            combined_model = _normalize_identity_text(summary.get('model'))
            serial_number = _first_valid_serial(
                summary.get('serial_number'),
                summary.get('serial'),
                summary.get('bios_serial'),
            )

            if not manufacturer or not (system_model or combined_model) or not serial_number:
                identity = collect_device_identity()
                if not manufacturer:
                    manufacturer = _normalize_identity_text(identity.get('manufacturer'))
                if not system_model:
                    system_model = _normalize_identity_text(identity.get('system_model'))
                if not combined_model:
                    combined_model = _normalize_identity_text(identity.get('model'))
                if not serial_number:
                    serial_number = _first_valid_serial(identity.get('serial_number'))

            if not combined_model and (manufacturer or system_model):
                combined_model = " ".join([manufacturer, system_model]).strip()
            if not serial_number:
                serial_number = SERIAL_UNAVAILABLE

            if manufacturer:
                summary['manufacturer'] = manufacturer
            if system_model:
                summary['system_model'] = system_model
                summary.setdefault('device_model', system_model)
            if combined_model:
                summary['model'] = combined_model
            summary['serial_number'] = serial_number
            summary.setdefault('serial', serial_number)
            summary.setdefault('bios_serial', serial_number)

            cpu_obj = summary.get('cpu')
            if not isinstance(cpu_obj, dict):
                cpu_obj = details.get('cpu') if isinstance(details.get('cpu'), dict) else {}
            if isinstance(cpu_obj, dict):
                if manufacturer:
                    cpu_obj.setdefault('system_manufacturer', manufacturer)
                if system_model:
                    cpu_obj.setdefault('system_model_raw', system_model)
                if combined_model:
                    cpu_obj.setdefault('system_model', combined_model)
                cpu_obj.setdefault('system_serial_number', serial_number)
                if cpu_obj:
                    summary['cpu'] = cpu_obj
                    details['cpu'] = cpu_obj
        except Exception:
            pass

        details['summary'] = summary
        return details
