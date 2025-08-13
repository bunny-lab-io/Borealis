#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/borealis-agent.py
import sys
import uuid
import socket
import os
import json
import asyncio
import concurrent.futures
from functools import partial
from io import BytesIO
import base64
import traceback
import random  # Macro Randomization
import platform  # OS Detection
import importlib.util
import time  # Heartbeat timestamps
import subprocess
import getpass
import datetime

import requests
try:
    import psutil
except Exception:
    psutil = None
import aiohttp

import socketio
from qasync import QEventLoop
from PyQt5 import QtCore, QtGui, QtWidgets
from PIL import ImageGrab

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: CONFIG MANAGER
# //////////////////////////////////////////////////////////////////////////
CONFIG_PATH = os.path.join(os.path.dirname(__file__), "agent_settings.json")
DEFAULT_CONFIG = {
    "borealis_server_url": "http://localhost:5000",
    "max_task_workers": 8,
    "config_file_watcher_interval": 2,
    "agent_id": "",
    "regions": {}
}

class ConfigManager:
    def __init__(self, path):
        self.path = path
        self._last_mtime = None
        self.data = {}
        self.load()

    def load(self):
        print("[DEBUG] Loading config from disk.")
        if not os.path.exists(self.path):
            print("[DEBUG] Config file not found. Creating default.")
            self.data = DEFAULT_CONFIG.copy()
            self._write()
        else:
            try:
                with open(self.path, 'r') as f:
                    loaded = json.load(f)
                self.data = {**DEFAULT_CONFIG, **loaded}
                print("[DEBUG] Config loaded:", self.data)
            except Exception as e:
                print(f"[WARN] Failed to parse config: {e}")
                self.data = DEFAULT_CONFIG.copy()
        try:
            self._last_mtime = os.path.getmtime(self.path)
        except Exception:
            self._last_mtime = None

    def _write(self):
        try:
            with open(self.path, 'w') as f:
                json.dump(self.data, f, indent=2)
            print("[DEBUG] Config written to disk.")
        except Exception as e:
            print(f"[ERROR] Could not write config: {e}")

    def watch(self):
        try:
            mtime = os.path.getmtime(self.path)
            if self._last_mtime is None or mtime != self._last_mtime:
                print("[CONFIG] Detected config change, reloading.")
                self.load()
                return True
        except Exception:
            pass
        return False

CONFIG = ConfigManager(CONFIG_PATH)
CONFIG.load()

def init_agent_id():
    if not CONFIG.data.get('agent_id'):
        CONFIG.data['agent_id'] = f"{socket.gethostname().lower()}-agent-{uuid.uuid4().hex[:8]}"
        CONFIG._write()
    return CONFIG.data['agent_id']

AGENT_ID = init_agent_id()
print(f"[DEBUG] Using AGENT_ID: {AGENT_ID}")

def clear_regions_only():
    CONFIG.data['regions'] = CONFIG.data.get('regions', {})
    CONFIG._write()

clear_regions_only()

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: OPERATING SYSTEM DETECTION
# //////////////////////////////////////////////////////////////////////////
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
            # On Windows, platform.release() gives major version (e.g., "10", "11")
            # platform.version() can also give build info, but isn't always user-friendly
            return f"Windows {platform.release()}"

        elif plat.startswith('linux'):
            try:
                import distro  # External package, better for Linux OS detection
                name = distro.name(pretty=True)  # e.g., "Fedora Workstation 42"
                if name:
                    return name
                else:
                    # Fallback if pretty name not found
                    return f"{platform.system()} {platform.release()}"
            except ImportError:
                # Fallback to basic info if distro not installed
                return f"{platform.system()} {platform.release()}"

        elif plat.startswith('darwin'):
            # macOS — platform.mac_ver()[0] returns version number
            version = platform.mac_ver()[0]
            # Optional: map version numbers to marketing names
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

CONFIG.data['agent_operating_system'] = detect_agent_os()
CONFIG._write()

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: MACRO AUTOMATION
# //////////////////////////////////////////////////////////////////////////
MACRO_ENGINE_PATH = os.path.join(os.path.dirname(__file__), "Python_API_Endpoints", "macro_engines.py")
spec = importlib.util.spec_from_file_location("macro_engines", MACRO_ENGINE_PATH)
macro_engines = importlib.util.module_from_spec(spec)
spec.loader.exec_module(macro_engines)

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: ASYNC TASK / WEBSOCKET
# //////////////////////////////////////////////////////////////////////////

sio = socketio.AsyncClient(reconnection=True, reconnection_attempts=0, reconnection_delay=5)
role_tasks = {}
overlay_widgets = {}
background_tasks = []

async def stop_all_roles():
    print("[DEBUG] Stopping all roles.")
    for task in list(role_tasks.values()):
        print(f"[DEBUG] Cancelling task for node: {task}")
        task.cancel()
    role_tasks.clear()
    for node_id, widget in overlay_widgets.items():
        print(f"[DEBUG] Closing overlay widget: {node_id}")
        try:
            widget.close()
        except Exception as e:
            print(f"[WARN] Error closing widget: {e}")
    overlay_widgets.clear()

# ---------------- Heartbeat ----------------
async def send_heartbeat():
    """
    Periodically send agent heartbeat to the server so the Devices page can
    show hostname, OS, and last_seen.
    """
    while True:
        try:
            payload = {
                "agent_id": AGENT_ID,
                "hostname": socket.gethostname(),
                "agent_operating_system": CONFIG.data.get("agent_operating_system", detect_agent_os()),
                "last_seen": int(time.time())
            }
            await sio.emit("agent_heartbeat", payload)
        except Exception as e:
            print(f"[WARN] heartbeat emit failed: {e}")
        await asyncio.sleep(5)

# ---------------- Detailed Agent Data ----------------

def _get_internal_ip():
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.connect(("8.8.8.8", 80))
        ip = s.getsockname()[0]
        s.close()
        return ip
    except Exception:
        return "unknown"

def collect_summary():
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
                    out = subprocess.run(
                        ["uptime", "-s"], capture_output=True, text=True, timeout=30
                    )
                    val = out.stdout.strip()
                    if val:
                        last_reboot = val
                except Exception:
                    pass
    except Exception:
        last_reboot = "unknown"

    created = CONFIG.data.get("created")
    if not created:
        created = time.strftime("%Y-%m-%d %H:%M:%S")
        CONFIG.data["created"] = created
        CONFIG._write()

    try:
        external_ip = requests.get("https://api.ipify.org", timeout=5).text.strip()
    except Exception:
        external_ip = "unknown"

    return {
        "hostname": socket.gethostname(),
        "operating_system": CONFIG.data.get("agent_operating_system", detect_agent_os()),
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
            except FileNotFoundError:
                ps_cmd = (
                    "Get-PSDrive -PSProvider FileSystem | "
                    "Select-Object Name,Free,Used,Capacity,Root | ConvertTo-Json"
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
                for d in data:
                    total = d.get("Capacity") or 0
                    used = d.get("Used") or 0
                    free_bytes = d.get("Free") or max(total - used, 0)
                    usage = (used / total * 100) if total else 0
                    drive = d.get("Root") or f"{d.get('Name','')}:"
                    disks.append({
                        "drive": drive,
                        "disk_type": "Fixed Disk",
                        "usage": usage,
                        "total": total,
                        "free": free_bytes,
                        "used": used,
                    })
        else:
            out = subprocess.run(
                ["df", "-kP"], capture_output=True, text=True, timeout=60
            )
            lines = out.stdout.strip().splitlines()[1:]
            for line in lines:
                parts = line.split()
                if len(parts) >= 6:
                    total = int(parts[1]) * 1024
                    used = int(parts[2]) * 1024
                    free_bytes = int(parts[3]) * 1024
                    usage = float(parts[4].rstrip("%"))
                    disks.append({
                        "drive": parts[5],
                        "disk_type": "Fixed Disk",
                        "usage": usage,
                        "total": total,
                        "free": free_bytes,
                        "used": used,
                    })
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
            out = subprocess.run(
                ["ip", "-o", "-4", "addr", "show"],
                capture_output=True,
                text=True,
                timeout=60,
            )
            for line in out.stdout.splitlines():
                parts = line.split()
                if len(parts) >= 4:
                    name = parts[1]
                    ip = parts[3].split("/")[0]
                    adapters.append({"adapter": name, "ips": [ip], "mac": "unknown"})
    except Exception as e:
        print(f"[WARN] collect_network failed: {e}")
    return adapters

async def send_agent_details():
    """Collect detailed agent data and send to server periodically."""
    while True:
        try:
            details = {
                "summary": collect_summary(),
                "software": collect_software(),
                "memory": collect_memory(),
                "storage": collect_storage(),
                "network": collect_network(),
            }
            url = CONFIG.data.get("borealis_server_url", "http://localhost:5000") + "/api/agent/details"
            payload = {
                "agent_id": AGENT_ID,
                "hostname": details.get("summary", {}).get("hostname", socket.gethostname()),
                "details": details,
            }
            async with aiohttp.ClientSession() as session:
                await session.post(url, json=payload, timeout=10)
        except Exception as e:
            print(f"[WARN] Failed to send agent details: {e}")
        await asyncio.sleep(300)

@sio.event
async def connect():
    print(f"[WebSocket] Connected to Borealis Server with Agent ID: {AGENT_ID}")
    await sio.emit('connect_agent', {"agent_id": AGENT_ID})

    # Send an immediate heartbeat so the UI can populate instantly.
    try:
        await sio.emit("agent_heartbeat", {
            "agent_id": AGENT_ID,
            "hostname": socket.gethostname(),
            "agent_operating_system": CONFIG.data.get("agent_operating_system", detect_agent_os()),
            "last_seen": int(time.time())
        })
    except Exception as e:
        print(f"[WARN] initial heartbeat failed: {e}")

    await sio.emit('request_config', {"agent_id": AGENT_ID})

@sio.event
async def disconnect():
    print("[WebSocket] Disconnected from Borealis server.")
    await stop_all_roles()
    CONFIG.data['regions'].clear()
    CONFIG._write()

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: AGENT CONFIG MANAGEMENT / WINDOW MANAGEMENT
# //////////////////////////////////////////////////////////////////////////
@sio.on('agent_config')
async def on_agent_config(cfg):
    print("[DEBUG] agent_config event received.")
    roles = cfg.get('roles', [])
    if not roles:
        print("[CONFIG] Config Reset by Borealis Server Operator - Awaiting New Config...")
        await stop_all_roles()
        return

    print(f"[CONFIG] Received New Agent Config with {len(roles)} Role(s).")

    new_ids = {r.get('node_id') for r in roles if r.get('node_id')}
    old_ids = set(role_tasks.keys())
    removed = old_ids - new_ids

    for rid in removed:
        print(f"[DEBUG] Removing node {rid} from regions/overlays.")
        CONFIG.data['regions'].pop(rid, None)
        w = overlay_widgets.pop(rid, None)
        if w:
            try:
                w.close()
            except:
                pass
    if removed:
        CONFIG._write()

    for task in list(role_tasks.values()):
        task.cancel()
    role_tasks.clear()

    for role_cfg in roles:
        nid = role_cfg.get('node_id')
        role = role_cfg.get('role')
        if role == 'screenshot':
            print(f"[DEBUG] Starting screenshot task for {nid}")
            task = asyncio.create_task(screenshot_task(role_cfg))
            role_tasks[nid] = task
        elif role == 'macro':
            print(f"[DEBUG] Starting macro task for {nid}")
            task = asyncio.create_task(macro_task(role_cfg))
            role_tasks[nid] = task

@sio.on('list_agent_windows')
async def handle_list_agent_windows(data):
    windows = macro_engines.list_windows()
    await sio.emit('agent_window_list', {
        'agent_id': AGENT_ID,
        'windows': windows
    })

# ---------------- Overlay Widget ----------------
overlay_green_thickness = 4
overlay_gray_thickness = 2
handle_size = overlay_green_thickness * 2
extra_top_padding = overlay_green_thickness * 2 + 4  # give space above the top-center green bar

class ScreenshotRegion(QtWidgets.QWidget):
    def __init__(self, node_id, x=100, y=100, w=300, h=200, alias=None):
        super().__init__()
        self.node_id = node_id
        self.alias = alias
        self.setGeometry(x - handle_size, y - handle_size - extra_top_padding, w + handle_size*2, h + handle_size*2 + extra_top_padding)
        self.setWindowFlags(QtCore.Qt.FramelessWindowHint | QtCore.Qt.WindowStaysOnTopHint)
        self.setAttribute(QtCore.Qt.WA_TranslucentBackground)
        self.resize_dir = None
        self.drag_offset = None
        self._start_geom = None
        self._start_pos = None
        self.setMouseTracking(True)

    def paintEvent(self, event):
        p = QtGui.QPainter(self)
        p.setRenderHint(QtGui.QPainter.Antialiasing)
        w = self.width()
        h = self.height()

        # draw gray capture box
        p.setPen(QtGui.QPen(QtGui.QColor(130,130,130), overlay_gray_thickness))
        p.drawRect(handle_size, handle_size + extra_top_padding, w-handle_size*2, h-handle_size*2 - extra_top_padding)

        p.setPen(QtCore.Qt.NoPen)
        p.setBrush(QtGui.QBrush(QtGui.QColor(0,191,255)))
        edge = overlay_green_thickness*3

        # corner handles
        p.drawRect(0,extra_top_padding,edge,overlay_green_thickness)
        p.drawRect(0,extra_top_padding,overlay_green_thickness,edge)
        p.drawRect(w-edge,extra_top_padding,edge,overlay_green_thickness)
        p.drawRect(w-overlay_green_thickness,extra_top_padding,overlay_green_thickness,edge)
        p.drawRect(0,h-overlay_green_thickness,edge,overlay_green_thickness)
        p.drawRect(0,h-edge,overlay_green_thickness,edge)
        p.drawRect(w-edge,h-overlay_green_thickness,edge,overlay_green_thickness)
        p.drawRect(w-overlay_green_thickness,h-edge,overlay_green_thickness,edge)

        # side handles
        long = overlay_green_thickness*6
        p.drawRect((w-long)//2,extra_top_padding,long,overlay_green_thickness)
        p.drawRect((w-long)//2,h-overlay_green_thickness,long,overlay_green_thickness)
        p.drawRect(0,(h+extra_top_padding-long)//2,overlay_green_thickness,long)
        p.drawRect(w-overlay_green_thickness,(h+extra_top_padding-long)//2,overlay_green_thickness,long)

        # draw grabber bar (same size as top-center bar, but above it)
        bar_width = overlay_green_thickness * 6
        bar_height = overlay_green_thickness
        bar_x = (w - bar_width) // 2
        bar_y = 6  # 6-8 px down from top

        p.setBrush(QtGui.QColor(0,191,255))  # Borealis Blue
        p.drawRect(bar_x, bar_y - bar_height - 10, bar_width, bar_height * 4)  # 2px padding above green bar


    def get_geometry(self):
        g = self.geometry()
        return (g.x() + handle_size, g.y() + handle_size + extra_top_padding, g.width() - handle_size*2, g.height() - handle_size*2 - extra_top_padding)

    def mousePressEvent(self,e):
        if e.button()==QtCore.Qt.LeftButton:
            pos=e.pos()
            bar_width = overlay_green_thickness * 6
            bar_height = overlay_green_thickness
            bar_x = (self.width() - bar_width) // 2
            bar_y = 2
            bar_rect = QtCore.QRect(bar_x, bar_y, bar_width, bar_height)

            if bar_rect.contains(pos):
                self.drag_offset = e.globalPos() - self.frameGeometry().topLeft()
                return

            x1,y1,self_w,self_h = self.geometry().getRect()
            m=handle_size
            dirs = []
            if pos.x()<=m: dirs.append('left')
            if pos.x()>=self.width()-m: dirs.append('right')
            if pos.y()<=m+extra_top_padding: dirs.append('top')
            if pos.y()>=self.height()-m: dirs.append('bottom')
            if dirs:
                self.resize_dir = '_'.join(dirs)
                self._start_geom = self.geometry()
                self._start_pos = e.globalPos()
            else:
                self.drag_offset = e.globalPos()-self.frameGeometry().topLeft()

    def mouseMoveEvent(self,e):
        if self.resize_dir and self._start_geom and self._start_pos:
            dx = e.globalX() - self._start_pos.x()
            dy = e.globalY() - self._start_pos.y()
            geom = QtCore.QRect(self._start_geom)
            if 'left' in self.resize_dir:
                new_x = geom.x() + dx
                new_w = geom.width() - dx
                geom.setX(new_x)
                geom.setWidth(new_w)
            if 'right' in self.resize_dir:
                geom.setWidth(self._start_geom.width() + dx)
            if 'top' in self.resize_dir:
                new_y = geom.y() + dy
                new_h = geom.height() - dy
                geom.setY(new_y)
                geom.setHeight(new_h)
            if 'bottom' in self.resize_dir:
                geom.setHeight(self._start_geom.height() + dy)
            self.setGeometry(geom)
        elif self.drag_offset and e.buttons() & QtCore.Qt.LeftButton:
            self.move(e.globalPos()-self.drag_offset)

    def mouseReleaseEvent(self,e):
        self.drag_offset=None
        self.resize_dir=None
        self._start_geom=None
        self._start_pos=None
        x,y,w,h=self.get_geometry()
        CONFIG.data['regions'][self.node_id]={'x':x,'y':y,'w':w,'h':h}
        CONFIG._write()
        asyncio.create_task(sio.emit('agent_screenshot_task',{ 'agent_id':AGENT_ID,'node_id':self.node_id,'image_base64':'','x':x,'y':y,'w':w,'h':h}))

# ---------------- Screenshot Task ----------------
async def screenshot_task(cfg):
    nid=cfg.get('node_id')
    alias=cfg.get('alias','')
    r=CONFIG.data['regions'].get(nid)
    if r:
        region=(r['x'],r['y'],r['w'],r['h'])
    else:
        region=(cfg.get('x',100),cfg.get('y',100),cfg.get('w',300),cfg.get('h',200))
        CONFIG.data['regions'][nid]={'x':region[0],'y':region[1],'w':region[2],'h':region[3]}
        CONFIG._write()
    if nid not in overlay_widgets:
        widget=ScreenshotRegion(nid,*region,alias=alias)
        overlay_widgets[nid]=widget; widget.show()
    await sio.emit('agent_screenshot_task',{'agent_id':AGENT_ID,'node_id':nid,'image_base64':'','x':region[0],'y':region[1],'w':region[2],'h':region[3]})
    interval=cfg.get('interval',1000)/1000.0
    loop=asyncio.get_event_loop()
    executor=concurrent.futures.ThreadPoolExecutor(max_workers=CONFIG.data.get('max_task_workers',8))
    try:
        while True:
            x,y,w,h=overlay_widgets[nid].get_geometry()
            grab=partial(ImageGrab.grab,bbox=(x,y,x+w,y+h))
            img=await loop.run_in_executor(executor,grab)
            buf=BytesIO(); img.save(buf,format='PNG'); encoded=base64.b64encode(buf.getvalue()).decode('utf-8')
            await sio.emit('agent_screenshot_task',{'agent_id':AGENT_ID,'node_id':nid,'image_base64':encoded,'x':x,'y':y,'w':w,'h':h})
            await asyncio.sleep(interval)
    except asyncio.CancelledError:
        print(f"[TASK] Screenshot role {nid} cancelled.")
    except Exception as e:
        print(f"[ERROR] Screenshot task {nid} failed: {e}")
        traceback.print_exc()

# ---------------- Macro Task ----------------
async def macro_task(cfg):
    """
    Improved macro_task supporting all operation modes, live config, error reporting, and UI feedback.
    """
    nid = cfg.get('node_id')

    # Track trigger state for edge/level changes
    last_trigger_value = 0
    has_run_once = False

    while True:
        # Always re-fetch config (hot reload support)
        window_handle = cfg.get('window_handle')
        macro_type = cfg.get('macro_type', 'keypress')
        operation_mode = cfg.get('operation_mode', 'Continuous')
        key = cfg.get('key')
        text = cfg.get('text')
        interval_ms = int(cfg.get('interval_ms', 1000))
        randomize = cfg.get('randomize_interval', False)
        random_min = int(cfg.get('random_min', 750))
        random_max = int(cfg.get('random_max', 950))
        active = cfg.get('active', True)
        trigger = int(cfg.get('trigger', 0))

        async def emit_macro_status(success, message=""):
            await sio.emit('macro_status', {
                "agent_id": AGENT_ID,
                "node_id": nid,
                "success": success,
                "message": message,
                "timestamp": int(asyncio.get_event_loop().time() * 1000)
            })

        if not (active is True or str(active).lower() == "true"):
            await asyncio.sleep(0.2)
            continue

        try:
            send_macro = False

            if operation_mode == "Run Once":
                if not has_run_once:
                    send_macro = True
                    has_run_once = True
            elif operation_mode == "Continuous":
                send_macro = True
            elif operation_mode == "Trigger-Continuous":
                send_macro = (trigger == 1)
            elif operation_mode == "Trigger-Once":
                if last_trigger_value == 0 and trigger == 1:
                    send_macro = True
                last_trigger_value = trigger
            else:
                send_macro = True

            if send_macro:
                if macro_type == 'keypress' and key:
                    result = macro_engines.send_keypress_to_window(window_handle, key)
                elif macro_type == 'typed_text' and text:
                    result = macro_engines.type_text_to_window(window_handle, text)
                else:
                    await emit_macro_status(False, "Invalid macro type or missing key/text")
                    await asyncio.sleep(0.2)
                    continue

                if isinstance(result, tuple):
                    success, err = result
                else:
                    success, err = bool(result), ""

                if success:
                    await emit_macro_status(True, f"Macro sent: {macro_type}")
                else:
                    await emit_macro_status(False, err or "Unknown macro engine failure")
            else:
                await asyncio.sleep(0.05)

            if send_macro:
                if randomize:
                    ms = random.randint(random_min, random_max)
                else:
                    ms = interval_ms
                await asyncio.sleep(ms / 1000.0)
            else:
                await asyncio.sleep(0.1)

        except asyncio.CancelledError:
            print(f"[TASK] Macro role {nid} cancelled.")
            break
        except Exception as e:
            print(f"[ERROR] Macro task {nid} failed: {e}")
            import traceback
            traceback.print_exc()
            await emit_macro_status(False, str(e))
            await asyncio.sleep(0.5)

# ---------------- Config Watcher ----------------
async def config_watcher():
    print("[DEBUG] Starting config watcher")
    while True:
        CONFIG.watch()
        await asyncio.sleep(CONFIG.data.get('config_file_watcher_interval',2))

# ---------------- Persistent Idle Task ----------------
async def idle_task():
    print("[Agent] Entering idle state. Awaiting instructions...")
    try:
        while True:
            await asyncio.sleep(60)
            print("[DEBUG] Idle task still alive.")
    except asyncio.CancelledError:
        print("[FATAL] Idle task was cancelled!")
    except Exception as e:
        print(f"[FATAL] Idle task crashed: {e}")
        traceback.print_exc()

# ---------------- Dummy Qt Widget to Prevent Exit ----------------
class PersistentWindow(QtWidgets.QWidget):
    def __init__(self):
        super().__init__()
        self.setWindowTitle("KeepAlive")
        self.setGeometry(-1000,-1000,1,1)
        self.setAttribute(QtCore.Qt.WA_DontShowOnScreen)
        self.hide()

# //////////////////////////////////////////////////////////////////////////
# MAIN & EVENT LOOP
# //////////////////////////////////////////////////////////////////////////
async def connect_loop():
    retry=5
    while True:
        try:
            url=CONFIG.data.get('borealis_server_url',"http://localhost:5000")
            print(f"[WebSocket] Connecting to {url}...")
            await sio.connect(url,transports=['websocket'])
            break
        except Exception as e:
            print(f"[WebSocket] Server unavailable: {e}. Retrying in {retry}s...")
            await asyncio.sleep(retry)

if __name__=='__main__':
    app=QtWidgets.QApplication(sys.argv)
    loop=QEventLoop(app); asyncio.set_event_loop(loop)
    dummy_window=PersistentWindow(); dummy_window.show()
    print("[DEBUG] Dummy window shown to prevent Qt exit")
    try:
        background_tasks.append(loop.create_task(config_watcher()))
        background_tasks.append(loop.create_task(connect_loop()))
        background_tasks.append(loop.create_task(idle_task()))
        # Start periodic heartbeats
        background_tasks.append(loop.create_task(send_heartbeat()))
        background_tasks.append(loop.create_task(send_agent_details()))
        loop.run_forever()
    except Exception as e:
        print(f"[FATAL] Event loop crashed: {e}")
        traceback.print_exc()
    finally:
        print("[FATAL] Agent exited unexpectedly.")
