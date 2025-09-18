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


ROLE_NAME = 'device_inventory'
ROLE_CONTEXTS = ['interactive']


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


def collect_software():
    # Placeholder: fuller inventory can be added later
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
            try:
                ps_cmd = (
                    "Get-NetAdapter | Where-Object { $_.Status -eq 'Up' } | "
                    "ForEach-Object { $_ | Select-Object -Property InterfaceAlias, MacAddress } | ConvertTo-Json"
                )
                out = subprocess.run(["powershell", "-NoProfile", "-Command", ps_cmd], capture_output=True, text=True, timeout=60)
                data = json.loads(out.stdout or "[]")
                if isinstance(data, dict):
                    data = [data]
                for a in data:
                    adapters.append({'adapter': a.get('InterfaceAlias', 'unknown'), 'ips': [], 'mac': a.get('MacAddress', 'unknown')})
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
                url = (self.ctx.config.data.get('borealis_server_url', 'http://localhost:5000') or '').rstrip('/') + '/api/agent/details'
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

