# ======================================================
# Data/Agent/Roles/role_VNC.py
# Description: On-demand UltraVNC server lifecycle over WireGuard.
#
# API Endpoints (if applicable): None
# ======================================================

"""UltraVNC role (Windows) for on-demand VNC sessions over WireGuard."""
from __future__ import annotations

import ipaddress
import os
import subprocess
import threading
import time
from pathlib import Path
from typing import Any, Optional

ROLE_NAME = "VNC"
ROLE_CONTEXTS = ["system"]

VNC_FIREWALL_RULE_NAME = "Borealis - VNC - UltraVNC"
DEFAULT_VNC_PORT = 5900
ULTRAVNC_SERVICE_NAME = os.environ.get("BOREALIS_ULTRAVNC_SERVICE") or "uvnc_service"


def _log_path() -> Path:
    root = Path(__file__).resolve().parents[2] / "Logs" / "VPN_Tunnel"
    root.mkdir(parents=True, exist_ok=True)
    return root / "vnc.log"


def _write_log(message: str) -> None:
    ts = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime())
    try:
        _log_path().open("a", encoding="utf-8").write(f"[{ts}] [vnc] {message}\n")
    except Exception:
        pass


def _find_project_root() -> Optional[Path]:
    override = os.environ.get("BOREALIS_ROOT") or os.environ.get("BOREALIS_PROJECT_ROOT")
    if override:
        try:
            override_path = Path(override).expanduser().resolve()
            if override_path.is_dir():
                return override_path
        except Exception:
            pass
    current = Path(__file__).resolve()
    for parent in (current, *current.parents):
        try:
            if (parent / "Borealis.ps1").is_file() or (parent / "users.json").is_file():
                return parent
        except Exception:
            continue
    try:
        return current.parents[3]
    except Exception:
        return None


def _looks_like_vnc_root(path: Path) -> bool:
    if not path.is_dir():
        return False
    try:
        if (path / "winvnc.exe").is_file() or (path / "winvnc64.exe").is_file():
            return True
        if (path / "payload" / "x64" / "winvnc.exe").is_file():
            return True
        if (path / "payload" / "x64" / "winvnc64.exe").is_file():
            return True
        if (path / "payload" / "x86" / "winvnc.exe").is_file():
            return True
    except Exception:
        return False
    return False


def _resolve_vnc_root() -> Optional[Path]:
    override = os.environ.get("BOREALIS_VNC_ROOT") or os.environ.get("BOREALIS_ULTRAVNC_ROOT")
    if override:
        try:
            override_path = Path(override).expanduser().resolve()
            if override_path.is_dir():
                return override_path
        except Exception:
            pass
    root = _find_project_root()
    candidates: list[Path] = []
    if root:
        candidates.append(root / "Agent" / "Borealis" / "Tools" / "UltraVNC" / "Server")
        candidates.append(root / "Agent" / "Borealis" / "Tools" / "UltraVNC")
        candidates.append(root / "Dependencies" / "UltraVNC_Server")
        candidates.append(root / "UltraVNC_Server")
    try:
        current = Path(__file__).resolve()
        for parent in (current, *current.parents):
            candidates.append(parent / "Dependencies" / "UltraVNC_Server")
            candidates.append(parent / "UltraVNC_Server")
    except Exception:
        pass
    try:
        cwd = Path.cwd().resolve()
        for parent in (cwd, *cwd.parents):
            candidates.append(parent / "Dependencies" / "UltraVNC_Server")
            candidates.append(parent / "UltraVNC_Server")
    except Exception:
        pass
    seen = set()
    for candidate in candidates:
        try:
            resolved = candidate.resolve()
        except Exception:
            resolved = candidate
        if resolved in seen:
            continue
        seen.add(resolved)
        if _looks_like_vnc_root(candidate):
            return candidate
    return None


def _resolve_vnc_config_dir() -> Optional[Path]:
    override = os.environ.get("BOREALIS_VNC_CONFIG_DIR") or os.environ.get(
        "BOREALIS_ULTRAVNC_CONFIG_DIR"
    )
    if override:
        try:
            override_path = Path(override).expanduser().resolve()
            if override_path.is_dir():
                return override_path
        except Exception:
            pass
    root = _find_project_root()
    if root:
        tools_config = root / "Agent" / "Borealis" / "Tools" / "UltraVNC" / "Server"
        if tools_config.is_dir():
            return tools_config
        return root / "Agent" / "Borealis" / "Settings" / "UltraVNC"
    try:
        base = Path(__file__).resolve().parents[2]
        tools_config = base / "Borealis" / "Tools" / "UltraVNC" / "Server"
        if tools_config.is_dir():
            return tools_config
        return base / "Borealis" / "Settings" / "UltraVNC"
    except Exception:
        return None


def _resolve_vnc_exe() -> Optional[str]:
    override = os.environ.get("BOREALIS_VNC_SERVER_BIN")
    if override:
        try:
            if Path(override).is_file():
                return str(Path(override))
        except Exception:
            pass
    vnc_root = _resolve_vnc_root()
    if vnc_root:
        preferred = [
            vnc_root / "payload" / "x64" / "winvnc.exe",
            vnc_root / "payload" / "x64" / "winvnc64.exe",
            vnc_root / "winvnc64.exe",
            vnc_root / "winvnc.exe",
            vnc_root / "payload" / "x86" / "winvnc.exe",
        ]
        for candidate in preferred:
            if candidate.is_file():
                return str(candidate)
        try:
            for candidate in vnc_root.rglob("winvnc*.exe"):
                if candidate.is_file():
                    return str(candidate)
        except Exception:
            pass
    return None


def _read_ultravnc_config(path: Path) -> tuple[dict[str, str], list[str]]:
    data: dict[str, str] = {}
    order: list[str] = []
    if not path.is_file():
        return data, order
    try:
        raw_bytes = path.read_bytes()
    except Exception:
        return data, order
    if not raw_bytes:
        return data, order
    text: str = ""
    try:
        if raw_bytes.startswith((b"\xff\xfe", b"\xfe\xff")) or b"\x00" in raw_bytes:
            text = raw_bytes.decode("utf-16", errors="ignore")
        else:
            text = raw_bytes.decode("utf-8-sig", errors="ignore")
    except Exception:
        try:
            text = raw_bytes.decode("utf-8", errors="ignore")
        except Exception:
            return data, order
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        if stripped.startswith(("#", ";")):
            continue
        if "=" not in stripped:
            continue
        key, value = stripped.split("=", 1)
        key = key.strip().replace("\x00", "")
        if not key:
            continue
        if key not in order:
            order.append(key)
        data[key] = value.strip().replace("\x00", "")
    return data, order


def _find_password_hash_in_paths(paths: list[Path]) -> tuple[Optional[str], Optional[str]]:
    secure_value: Optional[str] = None
    for path in paths:
        data, _ = _read_ultravnc_config(path)
        hash_value: Optional[str] = None
        for key, value in data.items():
            key_lower = key.strip().lower()
            if key_lower == "secure" and value.strip():
                secure_value = value.strip()
            if key_lower == "passwd":
                candidate = value.strip()
                if candidate:
                    hash_value = candidate
        if hash_value:
            return hash_value, secure_value
    return None, secure_value


def _write_ultravnc_config(path: Path, updates: dict[str, str]) -> bool:
    data, order = _read_ultravnc_config(path)
    for key, value in updates.items():
        if key not in order:
            order.append(key)
        data[key] = value
    if not order:
        order = list(updates.keys())
    lines = [f"{key}={data.get(key, '')}" for key in order]
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("\n".join(lines) + "\n", encoding="ascii")
        return True
    except Exception as exc:
        _write_log(f"Failed to write UltraVNC config at {path}: {exc}")
        return False


def _resolve_vnc_port(value: Any = None) -> int:
    raw = value if value is not None else os.environ.get("BOREALIS_VNC_PORT")
    try:
        port = int(raw) if raw is not None else DEFAULT_VNC_PORT
    except Exception:
        port = DEFAULT_VNC_PORT
    if port < 1 or port > 65535:
        return DEFAULT_VNC_PORT
    return port


def _resolve_vnc_password_tool(root: Optional[Path]) -> Optional[str]:
    override = os.environ.get("BOREALIS_VNC_PASSWORD_TOOL")
    if override:
        try:
            if Path(override).is_file():
                return str(Path(override))
        except Exception:
            pass
    candidates: list[Path] = []
    if root:
        candidates.extend(
            [
                root / "createpassword.exe",
                root / "tools" / "createpassword.exe",
                root / "createpassword64.exe",
                root / "tools" / "createpassword64.exe",
                root / "CreatePassword.exe",
            ]
        )
    project_root = _find_project_root()
    if project_root:
        tools_root = project_root / "Agent" / "Borealis" / "Tools" / "UltraVNC"
        candidates.extend(
            [
                tools_root / "createpassword.exe",
                tools_root / "createpassword64.exe",
            ]
        )
        deps_root = project_root / "Dependencies" / "UltraVNC_Server"
        candidates.extend(
            [
                deps_root / "createpassword.exe",
                deps_root / "tools" / "createpassword.exe",
                deps_root / "createpassword64.exe",
                deps_root / "tools" / "createpassword64.exe",
                deps_root / "payload" / "x64" / "createpassword.exe",
                deps_root / "payload" / "x64" / "createpassword64.exe",
            ]
        )
    config_root = _resolve_vnc_config_dir()
    if config_root and config_root.name.lower() == "server" and config_root.parent.name.lower() == "ultravnc":
        tools_root = config_root.parent
        candidates.extend(
            [
                tools_root / "createpassword.exe",
                tools_root / "createpassword64.exe",
            ]
        )
    vnc_root = _resolve_vnc_root()
    if vnc_root and vnc_root != root:
        candidates.extend(
            [
                vnc_root / "createpassword.exe",
                vnc_root / "tools" / "createpassword.exe",
                vnc_root / "createpassword64.exe",
                vnc_root / "tools" / "createpassword64.exe",
                vnc_root / "payload" / "x64" / "createpassword.exe",
                vnc_root / "payload" / "x64" / "createpassword64.exe",
            ]
        )
    for candidate in candidates:
        if candidate.is_file():
            return str(candidate)
    try:
        if root:
            for candidate in root.rglob("createpassword.exe"):
                if candidate.is_file():
                    return str(candidate)
    except Exception:
        pass
    return None


def _discover_ultravnc_service_name() -> Optional[str]:
    if os.name != "nt":
        return None
    command = (
        "Get-Service -ErrorAction SilentlyContinue | "
        "Where-Object { $_.Name -like '*uvnc*' -or $_.DisplayName -like '*UltraVNC*' } | "
        "Select-Object -First 1 -ExpandProperty Name"
    )
    try:
        result = subprocess.run(
            ["powershell.exe", "-NoProfile", "-Command", command],
            capture_output=True,
            text=True,
            check=False,
        )
        output = (result.stdout or "").strip()
        if output:
            return output.splitlines()[0].strip()
    except Exception:
        return None
    return None


def _ensure_ultravnc_ini(config_path: Path, port: int) -> Optional[Path]:
    base_settings = {
        "UseRegistry": "0",
        "AuthRequired": "1",
        "MSLogonRequired": "0",
        "NewMSLogon": "0",
        "PortNumber": str(port),
        "AutoPortSelect": "0",
        "SocketConnect": "1",
        "HTTPConnect": "0",
        "AllowShutdown": "0",
        "DisableTrayIcon": "1",
        "EnableFileTransfer": "0",
    }
    if not _write_ultravnc_config(config_path, base_settings):
        return None
    return config_path


def _read_ultravnc_password_hash(
    password_tool: str, password: str, config_dir: Path
) -> tuple[Optional[str], Optional[str]]:
    tool_path = Path(password_tool)
    tool_dir = tool_path.parent
    ini_path = tool_dir / "UltraVNC.ini"
    try:
        if not ini_path.is_file():
            ini_path.write_text("[UltraVNC]\npasswd=\n", encoding="utf-8")
    except Exception as exc:
        _write_log(f"Failed to prepare UltraVNC.ini for password tool: {exc}")
        return None, None
    try:
        result = subprocess.run(
            [password_tool, "-secure", password],
            cwd=str(tool_dir),
            capture_output=True,
            text=True,
            check=False,
        )
        if result.returncode != 0:
            detail = (result.stderr or result.stdout or "").strip()
            _write_log(f"Failed to generate VNC password hash: {detail or 'exit ' + str(result.returncode)}")
            return None, None
    except Exception as exc:
        _write_log(f"Failed to generate VNC password hash: {exc}")
        return None, None
    candidates = [
        ini_path,
        tool_dir / "ultravnc.ini",
        config_dir / "UltraVNC.ini",
    ]
    hash_value, secure_value = _find_password_hash_in_paths(candidates)
    if not hash_value:
        _write_log(
            "VNC password hash missing after password tool run (tool={0}, ini={1}).".format(
                tool_path, ini_path
            )
        )
        return None, secure_value
    return hash_value, secure_value


def _apply_ultravnc_password_hash(config_path: Path, password_hash: str) -> bool:
    try:
        if not _write_ultravnc_config(config_path, {"passwd": password_hash}):
            return False
        try:
            raw = config_path.read_text(encoding="utf-8", errors="ignore")
        except Exception:
            return True
        lines = raw.splitlines()
        out_lines: list[str] = []
        in_section = False
        section_found = False
        passwd_written = False
        for line in lines:
            stripped = line.strip()
            if stripped.startswith("[") and stripped.endswith("]"):
                if in_section and not passwd_written:
                    out_lines.append(f"passwd={password_hash}")
                    passwd_written = True
                section_name = stripped[1:-1].strip()
                in_section = section_name.lower() == "ultravnc"
                if in_section:
                    section_found = True
                out_lines.append(line)
                continue
            if in_section and stripped.lower().startswith("passwd="):
                out_lines.append(f"passwd={password_hash}")
                passwd_written = True
            else:
                out_lines.append(line)
        if section_found:
            if in_section and not passwd_written:
                out_lines.append(f"passwd={password_hash}")
        else:
            if out_lines and out_lines[-1].strip():
                out_lines.append("")
            out_lines.append("[UltraVNC]")
            out_lines.append(f"passwd={password_hash}")
        config_path.write_text("\n".join(out_lines) + "\n", encoding="ascii")
        return True
    except Exception as exc:
        _write_log(f"Failed to apply VNC password hash to config: {exc}")
        return False


def _apply_ultravnc_secure_flag(config_path: Path, secure_value: Optional[str]) -> None:
    if secure_value is None:
        return
    try:
        raw = config_path.read_text(encoding="utf-8", errors="ignore")
    except Exception:
        return
    lines = raw.splitlines()
    out_lines: list[str] = []
    in_section = False
    section_found = False
    secure_written = False
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            if in_section and not secure_written:
                out_lines.append(f"Secure={secure_value}")
                secure_written = True
            section_name = stripped[1:-1].strip().lower()
            in_section = section_name == "admin"
            if in_section:
                section_found = True
            out_lines.append(line)
            continue
        if in_section and stripped.lower().startswith("secure="):
            out_lines.append(f"Secure={secure_value}")
            secure_written = True
        else:
            out_lines.append(line)
    if section_found:
        if in_section and not secure_written:
            out_lines.append(f"Secure={secure_value}")
    else:
        if out_lines and out_lines[-1].strip():
            out_lines.append("")
        out_lines.append("[admin]")
        out_lines.append(f"Secure={secure_value}")
    try:
        config_path.write_text("\n".join(out_lines) + "\n", encoding="ascii")
    except Exception:
        return


def _same_path(a: Path, b: Path) -> bool:
    try:
        return os.path.normcase(str(a.resolve())) == os.path.normcase(str(b.resolve()))
    except Exception:
        return os.path.normcase(str(a)) == os.path.normcase(str(b))


def _write_ultravnc_password_file(
    target_dir: Path,
    password_hash: str,
    secure_value: Optional[str],
    *,
    config_dir: Optional[Path] = None,
) -> None:
    if config_dir and _same_path(target_dir, config_dir):
        return
    ini_path = target_dir / "UltraVNC.ini"
    lines = ["[UltraVNC]", f"passwd={password_hash}"]
    if secure_value is not None:
        lines.extend(["", "[admin]", f"Secure={secure_value}"])
    try:
        ini_path.write_text("\n".join(lines) + "\n", encoding="ascii")
    except Exception:
        return


def _parse_allowed_ips(value: Any) -> Optional[str]:
    if isinstance(value, list):
        if not value:
            return None
        return str(value[0])
    if isinstance(value, str) and value.strip():
        return value.strip()
    return None


class VncManager:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._last_port: Optional[int] = None
        self._last_password: Optional[str] = None
        self._vnc_exe = _resolve_vnc_exe()
        self._vnc_root = _resolve_vnc_root()
        self._password_tool: Optional[str] = None
        self._password_tool_logged = False
        self._service_name: Optional[str] = None

    def _service_state_by_name(self, service_name: str) -> Optional[str]:
        if os.name != "nt":
            return None
        try:
            result = subprocess.run(
                ["sc.exe", "query", service_name],
                capture_output=True,
                text=True,
                check=False,
            )
            if result.returncode != 0:
                return None
            for line in (result.stdout or "").splitlines():
                if "STATE" in line:
                    parts = line.strip().split()
                    if parts:
                        return parts[-1].upper()
        except Exception:
            return None
        return None

    def _resolve_service_name(self, *, refresh: bool = False) -> Optional[str]:
        if self._service_name and not refresh:
            return self._service_name
        candidate = ULTRAVNC_SERVICE_NAME
        if candidate:
            state = self._service_state_by_name(candidate)
            if state is not None:
                self._service_name = candidate
                return candidate
        discovered = _discover_ultravnc_service_name()
        if discovered:
            self._service_name = discovered
            return discovered
        if candidate:
            self._service_name = candidate
            return candidate
        return None

    def _service_binpath(self, service_name: str) -> Optional[str]:
        if os.name != "nt":
            return None
        try:
            result = subprocess.run(
                ["sc.exe", "qc", service_name],
                capture_output=True,
                text=True,
                check=False,
            )
            if result.returncode != 0:
                return None
            for line in (result.stdout or "").splitlines():
                if "BINARY_PATH_NAME" in line:
                    parts = line.split(":", 1)
                    if len(parts) == 2:
                        return parts[1].strip()
        except Exception:
            return None
        return None

    def _ensure_service_binpath(self, service_name: str, config_path: Optional[Path]) -> bool:
        if os.name != "nt":
            return False
        if not config_path:
            return False
        if not self._vnc_exe:
            return False
        desired = f"\"{self._vnc_exe}\" -service -config \"{config_path}\""
        current = self._service_binpath(service_name)
        if current and "-config" in current:
            normalized = current.replace("'", "").replace('"', "").lower()
            desired_norm = desired.replace("'", "").replace('"', "").lower()
            if normalized == desired_norm:
                return False
        try:
            command = f"sc.exe config \"{service_name}\" binPath= '{desired}'"
            result = subprocess.run(
                ["powershell.exe", "-NoProfile", "-Command", command],
                capture_output=True,
                text=True,
                check=False,
            )
            if result.returncode != 0:
                detail = (result.stderr or result.stdout or "").strip()
                _write_log(
                    "UltraVNC service binPath update failed: {0}".format(
                        detail or f"exit {result.returncode}"
                    )
                )
                return False
            _write_log(f"UltraVNC service binPath updated for config {config_path}.")
            return True
        except Exception as exc:
            _write_log(f"UltraVNC service binPath update failed: {exc}")
            return False

    def _wait_for_service(self, service_name: str, timeout: float = 5.0) -> bool:
        deadline = time.time() + max(1.0, timeout)
        while time.time() < deadline:
            state = self._service_state_by_name(service_name)
            if state == "RUNNING":
                return True
            if state is None:
                return False
            time.sleep(0.5)
        return False

    def _wait_for_service_stopped(self, service_name: str, timeout: float = 8.0) -> bool:
        deadline = time.time() + max(1.0, timeout)
        while time.time() < deadline:
            state = self._service_state_by_name(service_name)
            if state in ("STOPPED", None):
                return True
            time.sleep(0.5)
        return False

    def _kill_winvnc_processes(self) -> None:
        if os.name != "nt":
            return
        command = (
            "Get-CimInstance Win32_Process -Filter \"Name='winvnc.exe'\" | "
            "Select-Object -ExpandProperty ProcessId"
        )
        try:
            result = subprocess.run(
                ["powershell.exe", "-NoProfile", "-Command", command],
                capture_output=True,
                text=True,
                check=False,
            )
            pids = [
                pid.strip()
                for pid in (result.stdout or "").splitlines()
                if pid.strip().isdigit()
            ]
        except Exception:
            return
        for pid in pids:
            try:
                subprocess.run(
                    ["taskkill.exe", "/PID", pid, "/F"],
                    capture_output=True,
                    text=True,
                    check=False,
                )
            except Exception:
                pass

    def _restart_service(self) -> None:
        if os.name != "nt":
            return
        service_name = self._resolve_service_name()
        if not service_name:
            return
        state = self._service_state_by_name(service_name)
        if state != "RUNNING":
            return
        try:
            subprocess.run(["sc.exe", "stop", service_name], capture_output=True, text=True, check=False)
            if not self._wait_for_service_stopped(service_name, timeout=6.0):
                _write_log(f"UltraVNC service stop timed out (service={service_name}); forcing stop.")
                self._kill_winvnc_processes()
            time.sleep(1)
            start_result = subprocess.run(
                ["sc.exe", "start", service_name], capture_output=True, text=True, check=False
            )
            if start_result.returncode != 0:
                detail = (start_result.stderr or start_result.stdout or "").strip()
                _write_log(
                    "UltraVNC service restart failed: {0}".format(detail or f"exit {start_result.returncode}")
                )
            if not self._wait_for_service(service_name, timeout=12.0):
                _write_log(f"UltraVNC service restart timed out (service={service_name}).")
        except Exception as exc:
            _write_log(f"Failed to restart UltraVNC service: {exc}")

    def _ensure_service_running(self, config_path: Optional[Path] = None) -> bool:
        if os.name != "nt":
            return False
        service_name = self._resolve_service_name()
        state = self._service_state_by_name(service_name) if service_name else None
        if state == "STOP_PENDING" and service_name:
            if not self._wait_for_service_stopped(service_name, timeout=8.0):
                _write_log(
                    f"UltraVNC service stop pending too long (service={service_name}); forcing stop."
                )
                self._kill_winvnc_processes()
            state = self._service_state_by_name(service_name)
        updated_binpath = False
        if service_name:
            updated_binpath = self._ensure_service_binpath(service_name, config_path)
        if state == "RUNNING":
            if updated_binpath:
                self._restart_service()
            return True
        if state == "START_PENDING" and service_name:
            return self._wait_for_service(service_name, timeout=10.0)
        if not self._vnc_exe:
            self._vnc_exe = _resolve_vnc_exe()
        if not self._vnc_exe:
            return False
        if not self._vnc_root:
            self._vnc_root = _resolve_vnc_root()
        try:
            if state is None:
                install_result = subprocess.run(
                    [self._vnc_exe, "-install"],
                    capture_output=True,
                    text=True,
                    check=False,
                )
                if install_result.returncode != 0:
                    detail = (install_result.stderr or install_result.stdout or "").strip()
                    _write_log(
                        "UltraVNC service install failed: {0}".format(
                            detail or f"exit {install_result.returncode}"
                        )
                    )
                service_name = self._resolve_service_name(refresh=True)
                if service_name:
                    updated_binpath = self._ensure_service_binpath(service_name, config_path)
            if not service_name:
                service_name = ULTRAVNC_SERVICE_NAME
            config_result = subprocess.run(
                ["sc.exe", "config", service_name, "start=", "auto"],
                capture_output=True,
                text=True,
                check=False,
            )
            if config_result.returncode != 0:
                detail = (config_result.stderr or config_result.stdout or "").strip()
                _write_log(
                    "UltraVNC service config failed: {0}".format(
                        detail or f"exit {config_result.returncode}"
                    )
                )
            start_result = subprocess.run(
                ["sc.exe", "start", service_name],
                capture_output=True,
                text=True,
                check=False,
            )
            start_output = (start_result.stdout or "") + (start_result.stderr or "")
            if (
                "SERVICE_ALREADY_RUNNING" in start_output
                or "already running" in start_output.lower()
                or "1056" in start_output
            ):
                if updated_binpath:
                    self._restart_service()
                return True
            if start_result.returncode != 0:
                detail = start_output.strip()
                _write_log(
                    "UltraVNC service start failed: {0}".format(
                        detail or f"exit {start_result.returncode}"
                    )
                )
        except Exception as exc:
            _write_log(f"Failed to ensure UltraVNC service running: {exc}")
            return False
        if self._wait_for_service(service_name, timeout=10.0):
            if updated_binpath:
                self._restart_service()
            return True
        return False

    def _normalize_firewall_remote(self, allowed_ips: Optional[str]) -> Optional[str]:
        if not allowed_ips:
            return None
        try:
            network = ipaddress.ip_network(str(allowed_ips).strip(), strict=False)
        except Exception:
            _write_log(f"Refusing to apply VNC firewall rule; invalid allowed_ips={allowed_ips}.")
            return None
        if network.prefixlen != 32:
            _write_log(f"Refusing to apply VNC firewall rule; allowed_ips not /32: {network}.")
            return None
        return str(network)

    def _ensure_firewall(self, allowed_ips: Optional[str], port: int) -> None:
        if os.name != "nt":
            return
        remote = self._normalize_firewall_remote(allowed_ips)
        if not remote:
            return
        rule_name = VNC_FIREWALL_RULE_NAME.replace("'", "''")
        command = (
            "Remove-NetFirewallRule -DisplayName '{name}' -ErrorAction SilentlyContinue; "
            "New-NetFirewallRule -DisplayName '{name}' -Direction Inbound -Action Allow "
            "-Protocol TCP -LocalPort {port} -RemoteAddress {remote} -Profile Any"
        ).format(name=rule_name, port=port, remote=remote)
        try:
            result = subprocess.run(
                ["powershell.exe", "-NoProfile", "-Command", command],
                capture_output=True,
                text=True,
                check=False,
            )
            if result.returncode != 0:
                _write_log(f"Failed to ensure VNC firewall rule: {result.stderr.strip()}")
            else:
                _write_log(f"Ensured VNC firewall rule for {remote} on port {port}.")
        except Exception as exc:
            _write_log(f"Failed to ensure VNC firewall rule: {exc}")

    def _remove_firewall(self) -> None:
        if os.name != "nt":
            return
        rule_name = VNC_FIREWALL_RULE_NAME.replace("'", "''")
        command = "Remove-NetFirewallRule -DisplayName '{name}' -ErrorAction SilentlyContinue".format(
            name=rule_name
        )
        try:
            subprocess.run(
                ["powershell.exe", "-NoProfile", "-Command", command],
                capture_output=True,
                text=True,
                check=False,
            )
        except Exception:
            pass

    def _apply_password(self, config_dir: Path, config_path: Path, password: str) -> Optional[str]:
        if not password:
            _write_log("VNC password missing; refusing to start without auth.")
            return None
        trimmed = str(password)[:8]
        if trimmed != password:
            _write_log("VNC password trimmed to 8 characters for UltraVNC compatibility.")
        if not self._password_tool:
            self._password_tool = _resolve_vnc_password_tool(config_dir)
            if self._password_tool and not self._password_tool_logged:
                self._password_tool_logged = True
                _write_log(f"Using VNC password tool: {self._password_tool}")
        if not self._password_tool:
            _write_log(
                "VNC password tool not found; expected createpassword.exe under "
                "Agent/Borealis/Tools/UltraVNC or Dependencies/UltraVNC_Server/tools."
            )
            return None
        password_hash, secure_value = _read_ultravnc_password_hash(
            self._password_tool, trimmed, config_dir
        )
        if not password_hash:
            return None
        if not _apply_ultravnc_password_hash(config_path, password_hash):
            return None
        _apply_ultravnc_secure_flag(config_path, secure_value)
        if self._password_tool:
            tool_dir = Path(self._password_tool).parent
            _write_ultravnc_password_file(
                tool_dir,
                password_hash,
                secure_value,
                config_dir=config_dir,
            )
        return trimmed

    def start(
        self,
        *,
        port: Optional[int],
        allowed_ips: Optional[str],
        password: Optional[str],
        reason: str = "start",
    ) -> None:
        with self._lock:
            port_value = _resolve_vnc_port(port)
            self._ensure_firewall(allowed_ips, port_value)

            if not self._vnc_exe:
                self._vnc_exe = _resolve_vnc_exe()
            if not self._vnc_exe:
                _write_log(
                    "UltraVNC server binary not found; expected under "
                    "Dependencies/UltraVNC_Server/payload (or set BOREALIS_VNC_SERVER_BIN)."
                )
                return

            config_dir = _resolve_vnc_config_dir()
            if not config_dir:
                _write_log("Unable to resolve UltraVNC config directory.")
                return
            config_path = config_dir / "ultravnc.ini"
            ini_path = _ensure_ultravnc_ini(config_path, port_value)
            if not ini_path:
                return
            applied_password = self._apply_password(config_dir, config_path, password or "")
            if not applied_password:
                return

            if not self._ensure_service_running(config_path=config_path):
                _write_log("Failed to start UltraVNC service.")
                return

            if self._last_port != port_value or self._last_password != applied_password:
                self._restart_service()
            self._last_port = port_value
            self._last_password = applied_password
            _write_log(f"VNC service running port={port_value} reason={reason}.")

    def stop(self, *, reason: str = "stop") -> None:
        with self._lock:
            self._remove_firewall()
            self._last_port = None
            _write_log(f"VNC firewall closed reason={reason}.")


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.vnc = VncManager()
        self._last_allowed_ips: Optional[str] = None
        hooks = getattr(ctx, "hooks", {}) or {}
        self._log_hook = hooks.get("log_agent")
        try:
            self.vnc.stop(reason="agent_startup")
        except Exception:
            self._log("Failed to preflight VNC cleanup.", error=True)
        try:
            config_dir = _resolve_vnc_config_dir()
            config_path = None
            if config_dir:
                config_path = _ensure_ultravnc_ini(config_dir / "ultravnc.ini", DEFAULT_VNC_PORT)
            self.vnc._ensure_service_running(config_path=config_path)
        except Exception:
            self._log("Failed to ensure UltraVNC service running.", error=True)

    def _log(self, message: str, *, error: bool = False) -> None:
        if callable(self._log_hook):
            try:
                self._log_hook(message, fname="VPN_Tunnel/vnc.log")
                if error:
                    self._log_hook(message, fname="agent.error.log")
            except Exception:
                pass
        _write_log(message)

    def register_events(self) -> None:
        sio = self.ctx.sio

        @sio.on("vpn_tunnel_start")
        async def _vpn_tunnel_start(payload):
            if isinstance(payload, dict):
                target_agent = payload.get("agent_id")
                if target_agent and str(target_agent).strip() != str(self.ctx.agent_id).strip():
                    return
                allowed_ips = payload.get("allowed_ips")
                self._last_allowed_ips = _parse_allowed_ips(allowed_ips)

        @sio.on("vpn_tunnel_stop")
        async def _vpn_tunnel_stop(payload):
            reason = "server_stop"
            if isinstance(payload, dict):
                target_agent = payload.get("agent_id")
                if target_agent and str(target_agent).strip() != str(self.ctx.agent_id).strip():
                    return
                reason = payload.get("reason") or reason
            self._log(f"VNC stop requested (reason={reason}).")
            self.vnc.stop(reason=str(reason))

        @sio.on("vnc_start")
        async def _vnc_start(payload):
            if isinstance(payload, dict):
                target_agent = payload.get("agent_id")
                if target_agent and str(target_agent).strip() != str(self.ctx.agent_id).strip():
                    return
                port = payload.get("port")
                allowed_ips = payload.get("allowed_ips") or self._last_allowed_ips
                password = payload.get("password") or ""
                reason = payload.get("reason") or "vnc_session_start"
            else:
                port = None
                allowed_ips = self._last_allowed_ips
                password = ""
                reason = "vnc_session_start"
            self._log(f"VNC start request received (reason={reason}).")
            self.vnc.start(port=port, allowed_ips=allowed_ips, password=password, reason=str(reason))

        @sio.on("vnc_stop")
        async def _vnc_stop(payload):
            reason = "vnc_session_end"
            if isinstance(payload, dict):
                target_agent = payload.get("agent_id")
                if target_agent and str(target_agent).strip() != str(self.ctx.agent_id).strip():
                    return
                reason = payload.get("reason") or reason
            self._log(f"VNC stop requested (reason={reason}).")
            self.vnc.stop(reason=str(reason))

    def stop_all(self) -> None:
        try:
            self.vnc.stop(reason="agent_shutdown")
        except Exception:
            self._log("Failed to stop VNC during shutdown.", error=True)
