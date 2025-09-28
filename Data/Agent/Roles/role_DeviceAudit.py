import os
import json
import time
import socket
import platform
import subprocess
import shutil
import string
import asyncio

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
            'os': CONFIG.data.get('agent_operating_system', detect_agent_os()),
            'username': os.environ.get('USERNAME') or os.environ.get('USER') or '',
            'domain': os.environ.get('USERDOMAIN') or '',
            'uptime_sec': int(time.time() - psutil.boot_time()) if psutil else None,
        }
    except Exception:
        return {'hostname': socket.gethostname()}


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
                    "$ad = Get-NetAdapter | ForEach-Object { $_ | Select-Object -Property InterfaceAlias, MacAddress }; "
                    "$map = @{}; foreach($a in $ad){ $map[$a.InterfaceAlias] = $a.MacAddress }; "
                    "$out = @(); foreach($e in $ip){ $mac = $map[$e.InterfaceAlias]; $out += [pscustomobject]@{ InterfaceAlias=$e.InterfaceAlias; IPAddress=$e.IPAddress; MacAddress=$mac } } "
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
                    if not ip:
                        continue
                    item = tmp.setdefault(alias, {'adapter': alias, 'ips': [], 'mac': mac})
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


class Role:
    def __init__(self, ctx):
        self.ctx = ctx
        self._ext_ip = None
        self._ext_ip_ts = 0
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
                # Derive additional summary fields
                try:
                    # Internal IP: first IPv4 on first adapter
                    internal_ip = ''
                    for a in (details.get('network') or []):
                        for ip in (a.get('ips') or []):
                            if ip and not ip.startswith('169.254.') and ip != '127.0.0.1':
                                internal_ip = ip
                                break
                        if internal_ip:
                            break
                    details['summary']['internal_ip'] = internal_ip
                except Exception:
                    pass
                try:
                    details['summary']['device_type'] = detect_device_type()
                except Exception:
                    pass
                try:
                    if psutil:
                        details['summary']['last_reboot'] = int(psutil.boot_time())
                except Exception:
                    pass
                url = (self.ctx.config.data.get('borealis_server_url', 'http://localhost:5000') or '').rstrip('/') + '/api/agent/details'
                payload = {
                    'agent_id': self.ctx.agent_id,
                    'hostname': details.get('summary', {}).get('hostname', socket.gethostname()),
                    'details': details,
                }
                if aiohttp is not None:
                    async with aiohttp.ClientSession() as session:
                        # External IP: refresh at most every 30 minutes
                        try:
                            now = time.time()
                            if (now - self._ext_ip_ts) > 1800:
                                # Try ipify JSON; fallback to plain-text ifconfig.me
                                ok = False
                                try:
                                    async with session.get('https://api.ipify.org?format=json', timeout=8) as resp:
                                        if resp.status == 200:
                                            j = await resp.json()
                                            self._ext_ip = (j.get('ip') or '').strip()
                                            self._ext_ip_ts = now
                                            ok = True
                                except Exception:
                                    pass
                                if not ok:
                                    try:
                                        async with session.get('https://ifconfig.me/ip', timeout=8) as resp2:
                                            if resp2.status == 200:
                                                t = (await resp2.text()) or ''
                                                t = t.strip()
                                                if t:
                                                    self._ext_ip = t
                                                    self._ext_ip_ts = now
                                                    ok = True
                                    except Exception:
                                        pass
                            if self._ext_ip:
                                details['summary']['external_ip'] = self._ext_ip
                        except Exception:
                            pass
                        await session.post(url, json=payload, timeout=10)
            except Exception:
                pass
            await asyncio.sleep(300)
