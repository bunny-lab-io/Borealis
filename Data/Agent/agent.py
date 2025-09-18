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
import shutil
import string

import requests
try:
    import psutil
except Exception:
    psutil = None
import aiohttp

import socketio
# Reduce noisy Qt output and attempt to avoid Windows OleInitialize warnings
os.environ.setdefault("QT_LOGGING_RULES", "qt.qpa.*=false;*.debug=false")
from qasync import QEventLoop
from PyQt5 import QtCore, QtGui, QtWidgets
try:
    # Swallow Qt framework messages to keep console clean
    def _qt_msg_handler(mode, context, message):
        return
    QtCore.qInstallMessageHandler(_qt_msg_handler)
except Exception:
    pass
from PIL import ImageGrab

# New modularized components
from role_manager import RoleManager

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: CONFIG MANAGER
# //////////////////////////////////////////////////////////////////////////

def _user_config_default_path():
    """Return the prior per-user config file path (used for migration)."""
    try:
        plat = sys.platform
        if plat.startswith("win"):
            base = os.environ.get("APPDATA") or os.path.expanduser(r"~\AppData\Roaming")
            return os.path.join(base, "Borealis", "agent_settings.json")
        elif plat == "darwin":
            base = os.path.expanduser("~/Library/Application Support")
            return os.path.join(base, "Borealis", "agent_settings.json")
        else:
            base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
            return os.path.join(base, "borealis", "agent_settings.json")
    except Exception:
        return os.path.join(os.path.dirname(__file__), "agent_settings.json")

def _find_project_root():
    """Attempt to locate the Borealis project root (folder with Borealis.ps1 or users.json)."""
    # Allow explicit override
    override_root = os.environ.get("BOREALIS_ROOT") or os.environ.get("BOREALIS_PROJECT_ROOT")
    if override_root and os.path.isdir(override_root):
        return os.path.abspath(override_root)

    cur = os.path.abspath(os.path.dirname(__file__))
    for _ in range(8):
        if (
            os.path.exists(os.path.join(cur, "Borealis.ps1"))
            or os.path.exists(os.path.join(cur, "users.json"))
            or os.path.isdir(os.path.join(cur, ".git"))
        ):
            return cur
        parent = os.path.dirname(cur)
        if parent == cur:
            break
        cur = parent
    # Heuristic fallback: two levels up from Agent/Borealis
    return os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))

def _resolve_config_path():
    """
    Decide where to store agent_settings.json, per user’s requirement:
    - Prefer env var BOREALIS_AGENT_CONFIG (full file path)
    - Else use <ProjectRoot> alongside Borealis.ps1 and users.json
    - Migrate from legacy locations if found (user config dir or next to this script)
    """
    # Full file path override
    override_file = os.environ.get("BOREALIS_AGENT_CONFIG")
    if override_file:
        cfg_dir = os.path.dirname(override_file)
        if cfg_dir and not os.path.exists(cfg_dir):
            os.makedirs(cfg_dir, exist_ok=True)
        return override_file

    # Optional directory override
    override_dir = os.environ.get("BOREALIS_AGENT_CONFIG_DIR")
    if override_dir:
        os.makedirs(override_dir, exist_ok=True)
        return os.path.join(override_dir, "agent_settings.json")

    # Target config in project root
    project_root = _find_project_root()
    cfg_path = os.path.join(project_root, "agent_settings.json")
    if os.path.exists(cfg_path):
        return cfg_path

    # Migration: from legacy user dir or script dir
    legacy_user = _user_config_default_path()
    legacy_script_dir = os.path.join(os.path.dirname(__file__), "agent_settings.json")
    for legacy in (legacy_user, legacy_script_dir):
        try:
            if legacy and os.path.exists(legacy):
                try:
                    shutil.move(legacy, cfg_path)
                except Exception:
                    shutil.copy2(legacy, cfg_path)
                return cfg_path
        except Exception:
            pass

    # Nothing to migrate; return desired path in root
    return cfg_path

CONFIG_PATH = _resolve_config_path()
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
        if not os.path.exists(self.path):
            print("[INFO] agent_settings.json not found - Creating...")
            self.data = DEFAULT_CONFIG.copy()
            self._write()
        else:
            try:
                with open(self.path, 'r') as f:
                    loaded = json.load(f)
                self.data = {**DEFAULT_CONFIG, **loaded}
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
        except Exception as e:
            print(f"[ERROR] Could not write config: {e}")

    def watch(self):
        try:
            mtime = os.path.getmtime(self.path)
            if self._last_mtime is None or mtime != self._last_mtime:
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
            # Aim for: "Microsoft Windows 11 Pro 24H2 Build 26100.5074"
            # Pull details from the registry when available and correct
            # historical quirks like CurrentVersion reporting 6.3.
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

                # Determine Windows major (10 vs 11) from build number to avoid relying
                # on inconsistent registry values like CurrentVersion (which may say 6.3).
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

                # Derive friendly edition name, prefer parsing from ProductName
                edition = ""
                pn = product_name or ""
                if pn.lower().startswith("windows "):
                    tokens = pn.split()
                    # tokens like ["Windows", "11", "Pro", ...]
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

                # Choose version label: DisplayVersion (preferred) then ReleaseId
                version_label = display_version or release_id or ""

                # Build string with UBR if present
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

                # Correct possible mislabeling in ProductName (e.g., says Windows 10 on Win 11)
                # by trusting build-based major_label.
                return " ".join(p for p in parts if p).strip()

            except Exception:
                # Safe fallback if registry lookups fail
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
# CORE SECTION: ASYNC TASK / WEBSOCKET
# //////////////////////////////////////////////////////////////////////////

sio = socketio.AsyncClient(reconnection=True, reconnection_attempts=0, reconnection_delay=5)
role_tasks = {}
background_tasks = []
AGENT_LOOP = None
ROLE_MANAGER = None

# ---------------- Local IPC Bridge (Service -> Agent) ----------------
def start_agent_bridge_pipe(loop_ref):
    import threading
    import win32pipe, win32file, win32con, pywintypes

    pipe_name = r"\\.\pipe\Borealis_Agent_Bridge"

    def forward_to_server(msg: dict):
        try:
            evt = msg.get('type')
            if evt == 'screenshot':
                payload = {
                    'agent_id': AGENT_ID,
                    'node_id': msg.get('node_id') or 'user_session',
                    'image_base64': msg.get('image_base64') or '',
                    'timestamp': msg.get('timestamp') or int(time.time())
                }
                asyncio.run_coroutine_threadsafe(sio.emit('agent_screenshot_task', payload), loop_ref)
        except Exception:
            pass

    def server_thread():
        while True:
            try:
                handle = win32pipe.CreateNamedPipe(
                    pipe_name,
                    win32con.PIPE_ACCESS_DUPLEX,
                    win32con.PIPE_TYPE_MESSAGE | win32con.PIPE_READMODE_MESSAGE | win32con.PIPE_WAIT,
                    1, 65536, 65536, 0, None)
            except pywintypes.error:
                time.sleep(1.0)
                continue

            try:
                win32pipe.ConnectNamedPipe(handle, None)
            except pywintypes.error:
                try:
                    win32file.CloseHandle(handle)
                except Exception:
                    pass
                time.sleep(0.5)
                continue

            # Read loop per connection
            try:
                while True:
                    try:
                        hr, data = win32file.ReadFile(handle, 65536)
                        if not data:
                            break
                        try:
                            obj = json.loads(data.decode('utf-8', errors='ignore'))
                            forward_to_server(obj)
                        except Exception:
                            pass
                    except pywintypes.error:
                        break
            finally:
                try:
                    win32file.CloseHandle(handle)
                except Exception:
                    pass
            time.sleep(0.2)

    t = threading.Thread(target=server_thread, daemon=True)
    t.start()

def send_service_control(msg: dict):
    try:
        import win32file
        pipe = r"\\.\pipe\Borealis_Service_Control"
        h = win32file.CreateFile(
            pipe,
            win32file.GENERIC_WRITE,
            0,
            None,
            win32file.OPEN_EXISTING,
            0,
            None,
        )
        try:
            win32file.WriteFile(h, json.dumps(msg).encode('utf-8'))
        finally:
            win32file.CloseHandle(h)
    except Exception:
        pass
IS_WINDOWS = sys.platform.startswith('win')

def _is_admin_windows():
    if not IS_WINDOWS:
        return False
    try:
        import ctypes
        return ctypes.windll.shell32.IsUserAnAdmin() != 0
    except Exception:
        return False

def _write_temp_script(content: str, suffix: str):
    import tempfile
    temp_dir = os.path.join(tempfile.gettempdir(), "Borealis", "quick_jobs")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="bj_", suffix=suffix, dir=temp_dir, text=True)
    with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as fh:
        fh.write(content or "")
    return path

async def _run_powershell_local(path: str):
    """Run powershell script as current user hidden window and capture output."""
    ps = None
    if IS_WINDOWS:
        ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
        if not os.path.isfile(ps):
            ps = "powershell.exe"
    else:
        ps = "pwsh"
    try:
        proc = await asyncio.create_subprocess_exec(
            ps,
            "-ExecutionPolicy", "Bypass" if IS_WINDOWS else "Bypass",
            "-NoProfile",
            "-File", path,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            creationflags=(0x08000000 if IS_WINDOWS else 0)  # CREATE_NO_WINDOW
        )
        out_b, err_b = await proc.communicate()
        rc = proc.returncode
        out = (out_b or b"").decode(errors='replace')
        err = (err_b or b"").decode(errors='replace')
        return rc, out, err
    except Exception as e:
        return -1, "", str(e)

async def _run_powershell_as_system(path: str):
    """Attempt to run as SYSTEM using schtasks; requires admin."""
    if not IS_WINDOWS:
        return -1, "", "SYSTEM run not supported on this OS"
    # Name with timestamp to avoid collisions
    name = f"Borealis_QuickJob_{int(time.time())}_{random.randint(1000,9999)}"
    # Create scheduled task
    # Start time: 1 minute from now (HH:MM)
    t = time.localtime(time.time() + 60)
    st = f"{t.tm_hour:02d}:{t.tm_min:02d}"
    create_cmd = [
        "schtasks", "/Create", "/TN", name,
        "/TR", f"\"powershell.exe -ExecutionPolicy Bypass -NoProfile -WindowStyle Hidden -File \"\"{path}\"\"\"",
        "/SC", "ONCE", "/ST", st, "/RL", "HIGHEST", "/RU", "SYSTEM", "/F"
    ]
    try:
        p1 = await asyncio.create_subprocess_exec(*create_cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
        c_out, c_err = await p1.communicate()
        if p1.returncode != 0:
            return p1.returncode, "", (c_err or b"").decode(errors='replace')
        # Run immediately
        run_cmd = ["schtasks", "/Run", "/TN", name]
        p2 = await asyncio.create_subprocess_exec(*run_cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
        r_out, r_err = await p2.communicate()
        # Give some time for task to run and finish (best-effort)
        await asyncio.sleep(5)
        # We cannot reliably capture stdout from scheduled task directly; advise writing output to file in script if needed.
        # Return status of scheduling; actual script result unknown. We will try to check last run result.
        query_cmd = ["schtasks", "/Query", "/TN", name, "/V", "/FO", "LIST"]
        p3 = await asyncio.create_subprocess_exec(*query_cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
        q_out, q_err = await p3.communicate()
        status_txt = (q_out or b"").decode(errors='replace')
        # Cleanup
        await asyncio.create_subprocess_exec("schtasks", "/Delete", "/TN", name, "/F")
        # We cannot get stdout/stderr; return status text to stderr and treat success based on return codes
        status = "Success" if p2.returncode == 0 else "Failed"
        return 0 if status == "Success" else 1, "", status_txt
    except Exception as e:
        return -1, "", str(e)

async def _run_powershell_with_credentials(path: str, username: str, password: str):
    if not IS_WINDOWS:
        return -1, "", "Credentialed run not supported on this OS"
    # Build a one-liner to convert plaintext password to SecureString and run Start-Process -Credential
    ps_cmd = (
        f"$user=\"{username}\"; "
        f"$pass=\"{password}\"; "
        f"$sec=ConvertTo-SecureString $pass -AsPlainText -Force; "
        f"$cred=New-Object System.Management.Automation.PSCredential($user,$sec); "
        f"Start-Process powershell -ArgumentList '-ExecutionPolicy Bypass -NoProfile -File \"{path}\"' -Credential $cred -WindowStyle Hidden -PassThru | Wait-Process;"
    )
    try:
        proc = await asyncio.create_subprocess_exec(
            "powershell.exe", "-NoProfile", "-Command", ps_cmd,
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
            creationflags=(0x08000000 if IS_WINDOWS else 0)
        )
        out_b, err_b = await proc.communicate()
        out = (out_b or b"").decode(errors='replace')
        err = (err_b or b"").decode(errors='replace')
        return proc.returncode, out, err
    except Exception as e:
        return -1, "", str(e)

async def stop_all_roles():
    print("[DEBUG] Stopping all roles.")
    try:
        if ROLE_MANAGER is not None:
            ROLE_MANAGER.stop_all()
    except Exception:
        pass

# ---------------- Heartbeat ----------------
async def send_heartbeat():
    """
    Periodically send agent heartbeat to the server so the Devices page can
    show hostname, OS, and last_seen.
    """
    # Initial heartbeat is sent in the WebSocket 'connect' handler.
    # Delay the loop start so we don't double-send immediately.
    await asyncio.sleep(60)
    while True:
        try:
            payload = {
                "agent_id": AGENT_ID,
                "hostname": socket.gethostname(),
                "agent_operating_system": CONFIG.data.get("agent_operating_system", detect_agent_os()),
                "last_seen": int(time.time())
            }
            await sio.emit("agent_heartbeat", payload)
            # Also report collector status alive ping with last_user
            import getpass
            await sio.emit('collector_status', {
                'agent_id': AGENT_ID,
                'hostname': socket.gethostname(),
                'active': True,
                'last_user': f"{os.environ.get('USERDOMAIN') or socket.gethostname()}\\{getpass.getuser()}"
            })
        except Exception as e:
            print(f"[WARN] heartbeat emit failed: {e}")
        # Send periodic heartbeats every 60 seconds
        await asyncio.sleep(60)

# ---------------- Detailed Agent Data ----------------
## Moved to agent_info module

def collect_summary():
    # migrated to role_DeviceInventory
    return {}

def collect_software():
    # migrated to role_DeviceInventory
    return []

def collect_memory():
    # migrated to role_DeviceInventory
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
    # migrated to role_DeviceInventory
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
    # migrated to role_DeviceInventory
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
    print(f"[INFO] Successfully Connected to Borealis Server!")
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

    # Let server know collector is active and who the user is
    try:
        import getpass
        await sio.emit('collector_status', {
            'agent_id': AGENT_ID,
            'hostname': socket.gethostname(),
            'active': True,
            'last_user': f"{os.environ.get('USERDOMAIN') or socket.gethostname()}\\{getpass.getuser()}"
        })
    except Exception:
        pass

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

    # Forward screenshot config to service helper (interval only)
    try:
        for role_cfg in roles:
            if role_cfg.get('role') == 'screenshot':
                interval_ms = int(role_cfg.get('interval', 1000))
                send_service_control({'type': 'screenshot_config', 'interval_ms': interval_ms})
                break
    except Exception:
        pass

    try:
        if ROLE_MANAGER is not None:
            ROLE_MANAGER.on_config(roles)
    except Exception as e:
        print(f"[WARN] role manager apply config failed: {e}")

## Script execution and list windows handlers are registered by roles

# ---------------- Config Watcher ----------------
async def config_watcher():
    while True:
        CONFIG.watch()
        await asyncio.sleep(CONFIG.data.get('config_file_watcher_interval',2))

# ---------------- Persistent Idle Task ----------------
async def idle_task():
    try:
        while True:
            await asyncio.sleep(60)
    except asyncio.CancelledError:
        print("[FATAL] Idle task was cancelled!")
    except Exception as e:
        print(f"[FATAL] Idle task crashed: {e}")
        traceback.print_exc()

async def _run_powershell_via_user_task(content: str):
    ps = None
    if IS_WINDOWS:
        ps = os.path.expandvars(r"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe")
        if not os.path.isfile(ps):
            ps = "powershell.exe"
    else:
        return -999, '', 'Windows only'
    path = None
    out_path = None
    try:
        temp_dir = os.path.join(os.path.dirname(__file__), '..', '..', 'Temp')
        temp_dir = os.path.abspath(temp_dir)
        os.makedirs(temp_dir, exist_ok=True)
        fd, path = tempfile.mkstemp(prefix='usr_task_', suffix='.ps1', dir=temp_dir, text=True)
        with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as f:
            f.write(content or '')
        out_path = os.path.join(temp_dir, f'out_{uuid.uuid4().hex}.txt')
        name = f"Borealis Agent - Task - {uuid.uuid4().hex} @ CurrentUser"
        task_ps = f"""
$ErrorActionPreference='Continue'
$task = "{name}"
$ps   = "{ps}"
$scr  = "{path}"
$out  = "{out_path}"
try {{ Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue }} catch {{}}
$action   = New-ScheduledTaskAction -Execute $ps -Argument ('-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $scr + '" *> "' + $out + '"')
$settings = New-ScheduledTaskSettingsSet -DeleteExpiredTaskAfter (New-TimeSpan -Minutes 5) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
$principal= New-ScheduledTaskPrincipal -UserId ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName $task -Action $action -Settings $settings -Principal $principal -Force | Out-Null
Start-ScheduledTask -TaskName $task | Out-Null
Start-Sleep -Seconds 2
Get-ScheduledTask -TaskName $task | Out-Null
"""
        proc = await asyncio.create_subprocess_exec(ps, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', task_ps, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
        await proc.communicate()
        if proc.returncode != 0:
            return -999, '', 'failed to create user task'
        # Wait for output
        deadline = time.time() + 60
        out_data = ''
        while time.time() < deadline:
            try:
                if os.path.isfile(out_path) and os.path.getsize(out_path) > 0:
                    with open(out_path, 'r', encoding='utf-8', errors='replace') as f:
                        out_data = f.read()
                    break
            except Exception:
                pass
            await asyncio.sleep(1)
        cleanup = f"try {{ Unregister-ScheduledTask -TaskName '{name}' -Confirm:$false }} catch {{}}"
        await asyncio.create_subprocess_exec(ps, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', cleanup)
        return 0, out_data or '', ''
    except Exception as e:
        return -999, '', str(e)
    finally:
        # Best-effort cleanup of temp script and output files
        try:
            if path and os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass
        try:
            if out_path and os.path.isfile(out_path):
                os.remove(out_path)
        except Exception:
            pass

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
            print(f"[INFO] Connecting Agent to {url}...")
            await sio.connect(url,transports=['websocket'])
            break
        except Exception as e:
            print(f"[WebSocket] Server unavailable: {e}. Retrying in {retry}s...")
            await asyncio.sleep(retry)

if __name__=='__main__':
    app=QtWidgets.QApplication(sys.argv)
    loop=QEventLoop(app); asyncio.set_event_loop(loop)
    AGENT_LOOP = loop
    try:
        start_agent_bridge_pipe(loop)
    except Exception:
        pass
    dummy_window=PersistentWindow(); dummy_window.show()
    # Initialize roles context for role tasks
    # Initialize role manager and hot-load roles from Roles/
    try:
        hooks = {'send_service_control': send_service_control}
        ROLE_MANAGER = RoleManager(
            base_dir=os.path.dirname(__file__),
            context='interactive',
            sio=sio,
            agent_id=AGENT_ID,
            config=CONFIG,
            loop=loop,
            hooks=hooks,
        )
        ROLE_MANAGER.load()
    except Exception:
        pass
    try:
        background_tasks.append(loop.create_task(config_watcher()))
        background_tasks.append(loop.create_task(connect_loop()))
        background_tasks.append(loop.create_task(idle_task()))
        # Start periodic heartbeats
        background_tasks.append(loop.create_task(send_heartbeat()))
        loop.run_forever()
    except Exception as e:
        print(f"[FATAL] Event loop crashed: {e}")
        traceback.print_exc()
    finally:
        print("[FATAL] Agent exited unexpectedly.")

