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
from pathlib import Path

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


def collect_summary(CONFIG):
    try:
        hostname = socket.gethostname()
        return {
            'hostname': hostname,
            'os': detect_agent_os(),
            'username': os.environ.get('USERNAME') or os.environ.get('USER') or '',
            'domain': os.environ.get('USERDOMAIN') or '',
            'uptime_sec': int(time.time() - psutil.boot_time()) if psutil else None,
        }
    except Exception:
        return {'hostname': socket.gethostname()}


def _project_root():
    try:
        # Agent layout: Agent/Borealis/{this_file}; root is two levels up
        return os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..'))
    except Exception:
        return os.getcwd()


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
      Select-Object DisplayName, DisplayVersion
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
            out.append({'name': name, 'version': ver})
        if out:
            return out
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
                            items.append({'name': str(name).strip(), 'version': str(ver or '').strip()})
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
                key = (it['name'] or '').lower()
                if not key:
                    continue
                if key not in merged:
                    merged[key] = it
        return sorted(merged.values(), key=lambda x: x['name'])
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
                cores = 0
                with open('/proc/cpuinfo', 'r', encoding='utf-8', errors='ignore') as fh:
                    for line in fh:
                        if not brand and 'model name' in line:
                            brand = line.split(':', 1)[-1].strip()
                        if 'processor' in line:
                            cores += 1
                out = {'name': brand, 'logical_cores': cores or None}
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
        summary = {'hostname': socket.gethostname()}
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
            return ip.startswith('10.') or ip.startswith('192.168.') or (ip.startswith('172.') and any(ip.startswith(f'172.{n}.') for n in list(range(16,32))))
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
        last_user = _collect_last_user_registry()
        if last_user:
            summary['last_user'] = last_user
    except Exception:
        pass

    # CPU information (summary + display string)
    try:
        cpu = collect_cpu()
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

    async def _report_loop(self):
        interval_sec = 300  # post heartbeat/details every 5 minutes
        while True:
            try:
                # Determine audit refresh interval (minutes), default 30
                try:
                    refresh_min = int(self.ctx.config.data.get('audit_interval_minutes', 30))
                except Exception:
                    refresh_min = 30
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
                url = (get_url() or '').rstrip('/') + '/api/agent/details'
                payload = {
                    'agent_id': self.ctx.agent_id,
                    'hostname': details_to_send.get('summary', {}).get('hostname', socket.gethostname()),
                    'details': details_to_send,
                }
                if aiohttp is not None:
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
                lu2 = _collect_last_user_registry().strip()
                summary['last_user'] = lu2 if lu2 else 'No Users Logged In'
        except Exception:
            pass

        details['summary'] = summary
        return details
