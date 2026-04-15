# ======================================================
# Data/Agent/Roles/role_VNC.py
# Description: Always-on UltraVNC server lifecycle over WireGuard.
#
# API Endpoints (if applicable): None
# ======================================================

"""UltraVNC role (Windows) for always-on VNC sessions over WireGuard."""
from __future__ import annotations

import ipaddress
import json
import os
import secrets
import shutil
import socket
import subprocess
import tempfile
import threading
import time
from pathlib import Path
from typing import Any, Optional

try:
    from runtime_paths import agent_borealis_root, agent_logs_root, find_project_root
except Exception:
    import sys

    base_dir = Path(__file__).resolve().parents[1]
    if str(base_dir) not in sys.path:
        sys.path.insert(0, str(base_dir))
    from runtime_paths import agent_borealis_root, agent_logs_root, find_project_root

try:
    from update_state import busy_activity
except Exception:
    busy_activity = None


def _env_bool(value: Optional[str], default: bool) -> bool:
    if value is None:
        return default
    normalized = str(value).strip().lower()
    if normalized in {"1", "true", "yes", "on"}:
        return True
    if normalized in {"0", "false", "no", "off"}:
        return False
    return default


def _normalize_service_start_mode(value: Optional[str], default: str = "demand") -> str:
    raw = (value or default).strip().lower()
    aliases = {
        "manual": "demand",
        "automatic": "auto",
        "auto_start": "auto",
        "delayed": "delayed-auto",
        "delayed_auto": "delayed-auto",
    }
    normalized = aliases.get(raw, raw)
    if normalized not in {"demand", "auto", "disabled", "delayed-auto"}:
        return default
    return normalized


ROLE_NAME = "VNC"
ROLE_CONTEXTS = ["system"]

VNC_FIREWALL_RULE_NAME = "Borealis - VNC - UltraVNC"
DEFAULT_VNC_PORT = 5900
ULTRAVNC_SERVICE_NAME = os.environ.get("BOREALIS_ULTRAVNC_SERVICE") or "uvnc_service"
ULTRAVNC_SERVICE_START_MODE = _normalize_service_start_mode(
    os.environ.get("BOREALIS_ULTRAVNC_START_TYPE"),
    default="demand",
)
VNC_REQUIRE_ENGINE_READY = _env_bool(os.environ.get("BOREALIS_VNC_REQUIRE_ENGINE_READY"), True)
VNC_ALWAYS_ON_INTERVAL_SECONDS = 30


def _log_path() -> Path:
    root = agent_logs_root(__file__) / "VPN_Tunnel"
    root.mkdir(parents=True, exist_ok=True)
    return root / "vnc.log"


def _write_log(message: str) -> None:
    ts = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime())
    try:
        _log_path().open("a", encoding="utf-8").write(f"[{ts}] [vnc] {message}\n")
    except Exception:
        pass


def _coerce_int(value: Any, default: int, *, min_value: int = 1, max_value: Optional[int] = None) -> int:
    try:
        parsed = int(value)
    except Exception:
        return default
    if parsed < min_value:
        return min_value
    if max_value is not None and parsed > max_value:
        return max_value
    return parsed


VNC_DISCONNECT_GRACE_SECONDS = _coerce_int(
    os.environ.get("BOREALIS_VNC_DISCONNECT_GRACE_SECONDS"),
    45,
    min_value=0,
    max_value=600,
)
VNC_CREDENTIAL_ROTATION_SECONDS = _coerce_int(
    os.environ.get("BOREALIS_VNC_CREDENTIAL_ROTATION_SECONDS"),
    24 * 60 * 60,
    min_value=60,
    max_value=30 * 24 * 60 * 60,
)


def _generate_runtime_vnc_password() -> str:
    return secrets.token_hex(4)


def _generate_runtime_credential_revision() -> int:
    return int(time.time_ns() // 1_000_000)


def _new_runtime_vnc_credential(now: Optional[float] = None) -> dict[str, Any]:
    issued_at = float(time.time() if now is None else now)
    return {
        "controller_password": _generate_runtime_vnc_password(),
        "credential_revision": _generate_runtime_credential_revision(),
        "issued_at": issued_at,
    }


def _vnc_state_path() -> Path:
    root = agent_borealis_root(__file__) / "Settings" / "UltraVNC"
    root.mkdir(parents=True, exist_ok=True)
    return root / "vnc_state.json"


def _load_vnc_state() -> dict[str, Any]:
    path = _vnc_state_path()
    if not path.is_file():
        return {}
    try:
        raw = path.read_text(encoding="utf-8", errors="ignore")
    except Exception:
        return {}
    if not raw.strip():
        return {}
    try:
        data = json.loads(raw)
    except Exception:
        return {}
    if not isinstance(data, dict):
        return {}
    return data


def _save_vnc_state(state: dict[str, Any]) -> None:
    path = _vnc_state_path()
    try:
        payload = json.dumps(state, ensure_ascii=True, sort_keys=True, indent=2)
        path.write_text(payload + "\n", encoding="ascii")
    except Exception:
        return


def _find_project_root() -> Optional[Path]:
    try:
        return find_project_root(__file__)
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
        base = agent_borealis_root(__file__).parent
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
    ultra_lines: list[str] = ["[UltraVNC]"]
    secure_value: Optional[str] = None
    for key in order:
        normalized_key = str(key or "").strip()
        if not normalized_key:
            continue
        value = str(data.get(key, "") or "")
        if normalized_key.lower() == "secure":
            secure_value = value
            continue
        ultra_lines.append(f"{normalized_key}={value}")
    lines = ultra_lines
    if secure_value is not None:
        lines.extend(["", "[admin]", f"Secure={secure_value}"])
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


def _ensure_ultravnc_ini(
    config_path: Path,
    port: int,
    *,
    remove_wallpaper: bool = True,
) -> Optional[Path]:
    base_settings = {
        "UseRegistry": "0",
        "AuthRequired": "1",
        "MSLogonRequired": "0",
        "NewMSLogon": "0",
        "PortNumber": str(port),
        "AutoPortSelect": "0",
        "SocketConnect": "1",
        "HTTPConnect": "0",
        "AllowShutdown": "1",
        "DisableTrayIcon": "1",
        "EnableFileTransfer": "0",
        "RemoveWallpaper": "1" if remove_wallpaper else "0",
    }
    if not _write_ultravnc_config(config_path, base_settings):
        return None
    return config_path


def _read_ultravnc_password_hash(
    password_tool: str, password: str, config_dir: Path
) -> tuple[Optional[str], Optional[str]]:
    tool_path = Path(password_tool)
    _ = config_dir
    try:
        with tempfile.TemporaryDirectory(prefix="borealis-vnc-hash-") as temp_root:
            scratch_dir = Path(temp_root)
            scratch_tool = scratch_dir / tool_path.name
            shutil.copy2(tool_path, scratch_tool)
            ini_path = scratch_dir / "UltraVNC.ini"
            ini_path.write_text("[UltraVNC]\npasswd=\n", encoding="utf-8")
            result = subprocess.run(
                [str(scratch_tool), password],
                cwd=str(scratch_dir),
                capture_output=True,
                text=True,
                check=False,
            )
            if result.returncode != 0:
                detail = (result.stderr or result.stdout or "").strip()
                _write_log(f"Failed to generate VNC password hash: {detail or 'exit ' + str(result.returncode)}")
                return None, None
            candidates = [
                ini_path,
                scratch_dir / "ultravnc.ini",
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
    except Exception as exc:
        _write_log(f"Failed to generate VNC password hash: {exc}")
        return None, None


def _apply_ultravnc_password_hash(config_path: Path, password_hash: str, *, key: str = "passwd") -> bool:
    normalized_key = str(key or "").strip().lower()
    if normalized_key not in {"passwd", "passwd2"}:
        _write_log(f"Unsupported UltraVNC password key requested: {key}")
        return False
    try:
        if not _write_ultravnc_config(config_path, {normalized_key: password_hash}):
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
                    out_lines.append(f"{normalized_key}={password_hash}")
                    passwd_written = True
                section_name = stripped[1:-1].strip()
                in_section = section_name.lower() == "ultravnc"
                if in_section:
                    section_found = True
                out_lines.append(line)
                continue
            if in_section and stripped.lower().startswith(f"{normalized_key}="):
                out_lines.append(f"{normalized_key}={password_hash}")
                passwd_written = True
            else:
                out_lines.append(line)
        if section_found:
            if in_section and not passwd_written:
                out_lines.append(f"{normalized_key}={password_hash}")
        else:
            if out_lines and out_lines[-1].strip():
                out_lines.append("")
            out_lines.append("[UltraVNC]")
            out_lines.append(f"{normalized_key}={password_hash}")
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


def _clear_ultravnc_secure_flag(config_path: Path) -> None:
    try:
        raw = config_path.read_text(encoding="utf-8", errors="ignore")
    except Exception:
        return
    lines = raw.splitlines()
    out_lines: list[str] = []
    in_admin = False
    admin_lines: list[str] = []
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            if in_admin:
                # emit admin section if anything besides Secure= remains
                if admin_lines:
                    out_lines.append("[admin]")
                    out_lines.extend(admin_lines)
                admin_lines = []
            section_name = stripped[1:-1].strip().lower()
            in_admin = section_name == "admin"
            if not in_admin:
                out_lines.append(line)
            continue
        if in_admin:
            if stripped.lower().startswith("secure="):
                continue
            if stripped:
                admin_lines.append(line)
            continue
        out_lines.append(line)
    if in_admin and admin_lines:
        out_lines.append("[admin]")
        out_lines.extend(admin_lines)
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
    password_hashes: dict[str, str],
    secure_value: Optional[str],
    *,
    config_dir: Optional[Path] = None,
) -> None:
    if config_dir and _same_path(target_dir, config_dir):
        return
    ini_path = target_dir / "UltraVNC.ini"
    lines = ["[UltraVNC]"]
    for key in ("passwd", "passwd2"):
        value = str(password_hashes.get(key) or "").strip()
        if value:
            lines.append(f"{key}={value}")
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
        self._last_controller_password: Optional[str] = None
        self._last_view_only_password: Optional[str] = None
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

    def is_running(self) -> bool:
        service_name = self._resolve_service_name()
        if not service_name:
            return False
        return self._service_state_by_name(service_name) == "RUNNING"

    def is_listener_ready(self, port: Optional[int] = None) -> bool:
        if os.name != "nt":
            return False
        target_port = _resolve_vnc_port(port if port is not None else self._last_port)
        try:
            with socket.create_connection(("127.0.0.1", target_port), timeout=0.75):
                return True
        except Exception:
            return False

    def ensure_firewall(self, allowed_ips: Optional[str], port: int) -> None:
        self._ensure_firewall(allowed_ips, port)

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

    def _configure_service_start_mode(self, service_name: str, start_mode: str) -> None:
        if os.name != "nt":
            return
        try:
            result = subprocess.run(
                ["sc.exe", "config", service_name, "start=", start_mode],
                capture_output=True,
                text=True,
                check=False,
            )
            if result.returncode != 0:
                detail = (result.stderr or result.stdout or "").strip()
                _write_log(
                    "UltraVNC service start-type config failed: {0}".format(
                        detail or f"exit {result.returncode}"
                    )
                )
        except Exception as exc:
            _write_log(f"UltraVNC service start-type config failed: {exc}")

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
        desired_start = ULTRAVNC_SERVICE_START_MODE
        service_name = self._resolve_service_name()
        state = self._service_state_by_name(service_name) if service_name else None
        if service_name and state is not None:
            self._configure_service_start_mode(service_name, desired_start)
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
                service_name = ULTRAVNC_SERVICE_NAME or "uvnc_service"
                desired = f"\"{self._vnc_exe}\" -service -config \"{config_path}\""
                create_args = [
                    "sc.exe",
                    "create",
                    service_name,
                    "binPath=",
                    desired,
                    "start=",
                    desired_start,
                    "type=",
                    "own",
                    "DisplayName=",
                    service_name,
                ]
                create_result = subprocess.run(
                    create_args,
                    capture_output=True,
                    text=True,
                    check=False,
                )
                if create_result.returncode != 0:
                    detail = (create_result.stderr or create_result.stdout or "").strip()
                    _write_log(
                        "UltraVNC service create failed: {0}".format(
                            detail or f"exit {create_result.returncode}"
                        )
                    )
                service_name = self._resolve_service_name(refresh=True) or service_name
                updated_binpath = self._ensure_service_binpath(service_name, config_path)
            if not service_name:
                service_name = ULTRAVNC_SERVICE_NAME
            self._configure_service_start_mode(service_name, desired_start)
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

    def ensure_standby(self, *, reason: str = "standby") -> None:
        if os.name != "nt":
            return
        with self._lock:
            service_name = self._resolve_service_name()
            if not service_name:
                return
            state = self._service_state_by_name(service_name)
            if state is None:
                return
            self._configure_service_start_mode(service_name, ULTRAVNC_SERVICE_START_MODE)
            if state not in {"RUNNING", "START_PENDING", "STOP_PENDING"}:
                return
            try:
                subprocess.run(["sc.exe", "stop", service_name], capture_output=True, text=True, check=False)
                if not self._wait_for_service_stopped(service_name, timeout=8.0):
                    _write_log(f"UltraVNC standby stop timed out (service={service_name}); forcing stop.")
                    self._kill_winvnc_processes()
                _write_log(f"VNC service standing by reason={reason}.")
            except Exception as exc:
                _write_log(f"Failed to place VNC service in standby: {exc}")

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
            "$rule = Get-NetFirewallRule -DisplayName '{name}' -ErrorAction SilentlyContinue; "
            "if (-not $rule) {{ "
            "New-NetFirewallRule -DisplayName '{name}' -Direction Inbound -Action Allow "
            "-Protocol TCP -LocalPort {port} -RemoteAddress {remote} -Profile Any | Out-Null; "
            "}} else {{ "
            "Set-NetFirewallRule -DisplayName '{name}' -Direction Inbound -Action Allow "
            "-Protocol TCP -LocalPort {port} -RemoteAddress {remote} -Profile Any -Enabled True | Out-Null; "
            "}}"
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

    def _apply_passwords(
        self,
        config_dir: Path,
        config_path: Path,
        controller_password: str,
        view_only_password: Optional[str],
    ) -> tuple[Optional[str], Optional[str]]:
        if not controller_password:
            _write_log("VNC controller password missing; refusing to start without auth.")
            return None, None
        trimmed_controller = str(controller_password)[:8]
        if trimmed_controller != controller_password:
            _write_log("VNC controller password trimmed to 8 characters for UltraVNC compatibility.")
        trimmed_view_only = str(view_only_password or "")[:8] if view_only_password else ""
        if view_only_password and trimmed_view_only != str(view_only_password):
            _write_log("VNC view-only password trimmed to 8 characters for UltraVNC compatibility.")
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
            return None, None
        controller_hash, secure_value = _read_ultravnc_password_hash(
            self._password_tool, trimmed_controller, config_dir
        )
        if not controller_hash:
            return None, None
        if not _apply_ultravnc_password_hash(config_path, controller_hash, key="passwd"):
            return None, None
        password_hashes = {"passwd": controller_hash}
        if trimmed_view_only:
            view_only_hash, secondary_secure_value = _read_ultravnc_password_hash(
                self._password_tool,
                trimmed_view_only,
                config_dir,
            )
            if not view_only_hash:
                return None, None
            if not _apply_ultravnc_password_hash(config_path, view_only_hash, key="passwd2"):
                return None, None
            password_hashes["passwd2"] = view_only_hash
            if secure_value is None:
                secure_value = secondary_secure_value
        else:
            _write_ultravnc_config(config_path, {"passwd2": ""})
        if secure_value is not None:
            _apply_ultravnc_secure_flag(config_path, secure_value)
        else:
            _clear_ultravnc_secure_flag(config_path)
        if self._password_tool:
            tool_dir = Path(self._password_tool).parent
            _write_ultravnc_password_file(
                tool_dir,
                password_hashes,
                secure_value,
                config_dir=config_dir,
            )
        return trimmed_controller, trimmed_view_only or None

    def start(
        self,
        *,
        port: Optional[int],
        allowed_ips: Optional[str],
        controller_password: Optional[str],
        view_only_password: Optional[str],
        remove_wallpaper: Optional[bool] = None,
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
            remove_wallpaper_value = True if remove_wallpaper is None else bool(remove_wallpaper)
            ini_path = _ensure_ultravnc_ini(
                config_path,
                port_value,
                remove_wallpaper=remove_wallpaper_value,
            )
            if not ini_path:
                return
            applied_controller_password, applied_view_only_password = self._apply_passwords(
                config_dir,
                config_path,
                controller_password or "",
                view_only_password,
            )
            if not applied_controller_password:
                return

            service_name = self._resolve_service_name()
            service_was_running = False
            if service_name:
                state = self._service_state_by_name(service_name)
                service_was_running = state == "RUNNING"
            if not self._ensure_service_running(config_path=config_path):
                _write_log("Failed to start UltraVNC service.")
                return

            if service_was_running and (
                self._last_port != port_value
                or self._last_controller_password != applied_controller_password
                or self._last_view_only_password != applied_view_only_password
            ):
                self._restart_service()
            self._last_port = port_value
            self._last_controller_password = applied_controller_password
            self._last_view_only_password = applied_view_only_password
            _write_log(f"VNC service running port={port_value} reason={reason}.")

    def stop(self, *, reason: str = "stop") -> None:
        self.ensure_standby(reason=reason)
        with self._lock:
            self._last_controller_password = None
            self._last_view_only_password = None


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.role_health_label = "UltraVNC Service"
        hooks = getattr(ctx, "hooks", {}) or {}
        self._log_hook = hooks.get("log_agent")
        self._http_client_factory = hooks.get("http_client")
        self._always_on_stop = threading.Event()
        self._always_on_thread: Optional[threading.Thread] = None
        self._missing_password_logged = False
        self._engine_ready_for_vnc = not VNC_REQUIRE_ENGINE_READY
        self._engine_wait_logged = False
        self._last_ready_at = 0
        self._state = _load_vnc_state()
        self._sanitize_state()
        self._last_allowed_ips = _parse_allowed_ips(self._state.get("allowed_ips"))
        self._agent_runtime_credentials = _new_runtime_vnc_credential()
        self._disconnect_grace: dict[str, Any] = {
            "deadline": 0.0,
            "controller_password": None,
            "view_only_password": None,
            "allowed_ips": None,
            "port": self._state.get("port"),
            "remove_wallpaper": bool(self._state.get("remove_wallpaper", True)),
            "reason": "",
        }
        self._runtime_session: dict[str, Any] = {
            "session_id": "",
            "controller_password": self._agent_runtime_credentials["controller_password"],
            "view_only_password": None,
            "credential_revision": self._agent_runtime_credentials["credential_revision"],
            "remove_wallpaper": bool(self._state.get("remove_wallpaper", True)),
        }
        self._session_busy_lease = None
        self.vnc = VncManager()
        try:
            self._log(
                "VNC role initialized runtime_root={0} config_dir={1} vnc_root={2}".format(
                    agent_borealis_root(__file__).parent,
                    _resolve_vnc_config_dir() or "-",
                    self.vnc._vnc_root or "-",
                )
            )
        except Exception:
            pass
        try:
            config_dir = _resolve_vnc_config_dir()
            if config_dir:
                _ensure_ultravnc_ini(config_dir / "ultravnc.ini", DEFAULT_VNC_PORT, remove_wallpaper=True)
        except Exception:
            self._log("Failed to ensure UltraVNC config present.", error=True)
        self._ensure_always_on(reason="agent_startup")
        self._always_on_thread = threading.Thread(target=self._always_on_loop, daemon=True)
        self._always_on_thread.start()

    def _log(self, message: str, *, error: bool = False) -> None:
        if callable(self._log_hook):
            try:
                self._log_hook(message, fname="VPN_Tunnel/vnc.log")
                if error:
                    self._log_hook(message, fname="agent.error.log")
            except Exception:
                pass
        _write_log(message)

    def _http_client(self) -> Optional[Any]:
        if callable(self._http_client_factory):
            try:
                return self._http_client_factory()
            except Exception:
                return None
        return None

    def _mark_engine_ready(self, source: str) -> None:
        if self._engine_ready_for_vnc:
            return
        self._engine_ready_for_vnc = True
        self._engine_wait_logged = False
        self._log(f"VNC engine readiness confirmed via {source}.")

    def _acquire_session_busy(self, reason: str) -> None:
        if self._session_busy_lease is not None or not callable(busy_activity):
            return
        try:
            self._session_busy_lease = busy_activity(
                "vnc_session",
                metadata={"reason": str(reason or "vnc_session_start")},
            ).acquire()
        except Exception as exc:
            self._log(f"Failed to acquire VNC busy lease: {exc}", error=True)
            self._session_busy_lease = None

    def _release_session_busy(self) -> None:
        if self._session_busy_lease is None:
            return
        try:
            self._session_busy_lease.close()
        except Exception:
            pass
        self._session_busy_lease = None

    def _sanitize_state(self) -> None:
        dirty = False
        for key in ("password", "controller_password", "view_only_password", "session_id", "credential_revision"):
            if key in self._state:
                self._state.pop(key, None)
                dirty = True
        if dirty:
            _save_vnc_state(self._state)

    def _controller_password(self) -> Optional[str]:
        value = self._runtime_session.get("controller_password")
        if not value:
            value = (getattr(self, "_agent_runtime_credentials", {}) or {}).get("controller_password")
        if isinstance(value, str) and value.strip():
            return value.strip()[:8]
        return None

    def _view_only_password(self) -> Optional[str]:
        value = self._runtime_session.get("view_only_password")
        if isinstance(value, str) and value.strip():
            return value.strip()[:8]
        return None

    def _active_session_id(self) -> str:
        return str(self._runtime_session.get("session_id") or "").strip()

    def _credential_revision(self) -> int:
        try:
            value = self._runtime_session.get("credential_revision")
            if value in (None, ""):
                value = (getattr(self, "_agent_runtime_credentials", {}) or {}).get("credential_revision")
            return int(value or 0)
        except Exception:
            return 0

    def _runtime_credential_issued_at(self) -> float:
        try:
            value = (getattr(self, "_agent_runtime_credentials", {}) or {}).get("issued_at")
            return float(value or 0.0)
        except Exception:
            return 0.0

    def _sync_runtime_session_credentials(self) -> None:
        credential = getattr(self, "_agent_runtime_credentials", {}) or {}
        controller_password = str(credential.get("controller_password") or "").strip()[:8] or None
        try:
            credential_revision = int(credential.get("credential_revision") or 0)
        except Exception:
            credential_revision = 0
        if not isinstance(getattr(self, "_runtime_session", None), dict):
            self._runtime_session = {}
        self._runtime_session["controller_password"] = controller_password
        self._runtime_session["credential_revision"] = credential_revision
        if "view_only_password" not in self._runtime_session:
            self._runtime_session["view_only_password"] = None

    def _runtime_credential_due(self, *, now: Optional[float] = None) -> bool:
        if VNC_CREDENTIAL_ROTATION_SECONDS <= 0:
            return False
        current_time = time.time() if now is None else float(now)
        issued_at = self._runtime_credential_issued_at()
        if issued_at <= 0:
            return True
        return (current_time - issued_at) >= float(VNC_CREDENTIAL_ROTATION_SECONDS)

    def _rotate_runtime_credential(self, *, reason: str, now: Optional[float] = None) -> None:
        self._agent_runtime_credentials = _new_runtime_vnc_credential(now=now)
        self._clear_disconnect_grace()
        self._sync_runtime_session_credentials()
        self._log(f"VNC runtime credential rotated (reason={reason}).")

    def _ensure_runtime_credential_fresh(self, *, reason: str, now: Optional[float] = None) -> bool:
        if not self._runtime_credential_due(now=now):
            return False
        self._rotate_runtime_credential(reason=reason, now=now)
        return True

    def _state_port(self) -> int:
        return _resolve_vnc_port(self._state.get("port"))

    def _remove_wallpaper_enabled(self) -> bool:
        runtime_value = self._runtime_session.get("remove_wallpaper")
        if isinstance(runtime_value, bool):
            return runtime_value
        return bool(self._state.get("remove_wallpaper", True))

    def _clear_disconnect_grace(self) -> None:
        self._disconnect_grace = {
            "deadline": 0.0,
            "controller_password": None,
            "view_only_password": None,
            "allowed_ips": None,
            "port": self._state.get("port"),
            "remove_wallpaper": self._remove_wallpaper_enabled(),
            "reason": "",
        }

    def _disconnect_grace_snapshot(self, *, now: Optional[float] = None) -> Optional[dict[str, Any]]:
        current_time = time.time() if now is None else float(now)
        grace_state = getattr(self, "_disconnect_grace", None)
        if not isinstance(grace_state, dict):
            return None
        deadline = float(grace_state.get("deadline") or 0.0)
        controller_password = grace_state.get("controller_password")
        if deadline <= current_time or not controller_password:
            if deadline > 0:
                self._clear_disconnect_grace()
            return None
        return {
            "deadline": deadline,
            "controller_password": str(controller_password),
            "view_only_password": grace_state.get("view_only_password"),
            "allowed_ips": grace_state.get("allowed_ips") or self._last_allowed_ips,
            "port": _resolve_vnc_port(grace_state.get("port")),
            "remove_wallpaper": bool(grace_state.get("remove_wallpaper", True)),
            "reason": str(grace_state.get("reason") or "").strip(),
            "remaining_seconds": max(0, int(round(deadline - current_time))),
        }

    def _disconnect_grace_active(self, *, now: Optional[float] = None) -> bool:
        return self._disconnect_grace_snapshot(now=now) is not None

    def _schedule_disconnect_grace(self, reason: str) -> bool:
        normalized_reason = str(reason or "").strip().lower()
        if normalized_reason not in {"operator_disconnect", "component_unmount"}:
            self._clear_disconnect_grace()
            return False
        if VNC_DISCONNECT_GRACE_SECONDS <= 0:
            self._clear_disconnect_grace()
            return False
        controller_password = self._controller_password()
        allowed_ips = self._last_allowed_ips or _parse_allowed_ips(self._state.get("allowed_ips"))
        if not controller_password or not allowed_ips:
            self._clear_disconnect_grace()
            return False
        deadline = time.time() + float(VNC_DISCONNECT_GRACE_SECONDS)
        self._disconnect_grace = {
            "deadline": deadline,
            "controller_password": controller_password,
            "view_only_password": self._view_only_password(),
            "allowed_ips": allowed_ips,
            "port": self._state_port(),
            "remove_wallpaper": self._remove_wallpaper_enabled(),
            "reason": normalized_reason,
        }
        self._log(
            "VNC reconnect grace active for {0}s after {1}.".format(
                int(VNC_DISCONNECT_GRACE_SECONDS),
                normalized_reason,
            )
        )
        return True

    def _clear_runtime_session(self) -> None:
        credential = getattr(self, "_agent_runtime_credentials", {}) or {}
        self._runtime_session = {
            "session_id": "",
            "controller_password": credential.get("controller_password"),
            "view_only_password": None,
            "credential_revision": credential.get("credential_revision") or 0,
            "remove_wallpaper": self._remove_wallpaper_enabled(),
        }

    def _update_runtime_session(
        self,
        *,
        session_id: Optional[str],
        controller_password: Optional[str],
        view_only_password: Optional[str],
        credential_revision: Any = None,
        remove_wallpaper: Optional[bool] = None,
    ) -> None:
        credential = getattr(self, "_agent_runtime_credentials", {}) or {}
        normalized_session_id = str(session_id or "").strip()
        normalized_controller = (
            str(controller_password or credential.get("controller_password") or "").strip()[:8]
            or None
        )
        normalized_view_only = str(view_only_password or "").strip()[:8] if view_only_password else None
        try:
            revision_value = (
                int(credential_revision)
                if credential_revision is not None
                else int(credential.get("credential_revision") or 0)
            )
        except Exception:
            revision_value = int(credential.get("credential_revision") or 0)
        self._runtime_session = {
            "session_id": normalized_session_id,
            "controller_password": normalized_controller,
            "view_only_password": normalized_view_only,
            "credential_revision": revision_value,
            "remove_wallpaper": self._remove_wallpaper_enabled() if remove_wallpaper is None else bool(remove_wallpaper),
        }

    def _update_state(
        self,
        *,
        allowed_ips: Optional[str] = None,
        port: Optional[int] = None,
        remove_wallpaper: Optional[bool] = None,
    ) -> None:
        updated = False
        if allowed_ips:
            normalized = _parse_allowed_ips(allowed_ips)
            if normalized:
                self._state["allowed_ips"] = normalized
                self._last_allowed_ips = normalized
                updated = True
        if port is not None:
            port_value = _resolve_vnc_port(port)
            self._state["port"] = port_value
            updated = True
        if remove_wallpaper is not None:
            self._state["remove_wallpaper"] = bool(remove_wallpaper)
            updated = True
        if updated:
            _save_vnc_state(self._state)

    def _apply_bootstrap_payload(self, payload: Any) -> None:
        if not isinstance(payload, dict):
            return
        controller_password = (
            payload.get("controller_password")
            or payload.get("vnc_password")
            or payload.get("password")
        )
        view_only_password = payload.get("view_only_password") or payload.get("spectator_password")
        port = payload.get("vnc_port") or payload.get("port")
        allowed_ips = payload.get("allowed_ips") or payload.get("engine_virtual_ip")
        remove_wallpaper = payload.get("remove_wallpaper")
        self._update_state(
            allowed_ips=allowed_ips if allowed_ips else None,
            port=port if port is not None else None,
            remove_wallpaper=remove_wallpaper if isinstance(remove_wallpaper, bool) else None,
        )
        if controller_password:
            self._clear_disconnect_grace()
            self._update_runtime_session(
                session_id=payload.get("session_id"),
                controller_password=None,
                view_only_password=None,
                credential_revision=None,
                remove_wallpaper=remove_wallpaper if isinstance(remove_wallpaper, bool) else None,
            )
            self._acquire_session_busy(str(payload.get("reason") or payload.get("session_state") or "bootstrap_restore"))
        else:
            self._clear_runtime_session()
            self._release_session_busy()

    def _request_vnc_bootstrap(self, reason: str) -> Optional[dict]:
        client = self._http_client()
        if client is None:
            return None
        credential = getattr(self, "_agent_runtime_credentials", {}) or {}
        try:
            payload = client.post_json(
                "/api/agent/vnc/ensure",
                {
                    "agent_id": self.ctx.agent_id,
                    "reason": reason,
                    "controller_password": credential.get("controller_password") or "",
                    "credential_revision": int(credential.get("credential_revision") or 0),
                },
                require_auth=True,
            )
        except Exception as exc:
            self._log(f"VNC ensure request failed: {exc}", error=True)
            return None
        if isinstance(payload, dict):
            return payload
        return None

    def _ensure_always_on(self, *, reason: str) -> None:
        controller_password = self._controller_password()
        grace_snapshot = self._disconnect_grace_snapshot()
        if not controller_password and grace_snapshot is None:
            if not self._missing_password_logged:
                self._missing_password_logged = True
                self._log("VNC always-on pending: runtime credential unavailable.")
            self.vnc.ensure_standby(reason="no_active_session")
            return
        self._missing_password_logged = False
        port_value = grace_snapshot["port"] if grace_snapshot else self._state_port()
        allowed_ips = (
            str(grace_snapshot.get("allowed_ips") or "").strip()
            if grace_snapshot
            else (self._last_allowed_ips or _parse_allowed_ips(self._state.get("allowed_ips")))
        )
        if not allowed_ips:
            if not self._engine_wait_logged:
                self._engine_wait_logged = True
                self._log("VNC always-on pending: waiting for tunnel firewall scope.")
            self.vnc.ensure_standby(reason="missing_allowed_ips")
            return
        self._engine_wait_logged = False
        self.vnc.start(
            port=port_value,
            allowed_ips=allowed_ips,
            controller_password=controller_password or str(grace_snapshot.get("controller_password") or ""),
            view_only_password=(
                grace_snapshot.get("view_only_password") if grace_snapshot else self._view_only_password()
            ),
            remove_wallpaper=(
                bool(grace_snapshot.get("remove_wallpaper", True))
                if grace_snapshot
                else self._remove_wallpaper_enabled()
            ),
            reason=reason,
        )
        if self.vnc.is_listener_ready(port_value):
            self._last_ready_at = int(time.time())

    def _always_on_loop(self) -> None:
        interval = _coerce_int(VNC_ALWAYS_ON_INTERVAL_SECONDS, 30, min_value=5)
        while not self._always_on_stop.is_set():
            try:
                rotated = self._ensure_runtime_credential_fresh(reason="scheduled_rotation")
                has_allowed_ips = bool(
                    self._last_allowed_ips
                    or (self._disconnect_grace_snapshot() or {}).get("allowed_ips")
                )
                listener_ready = self.vnc.is_listener_ready(self._state_port())
                if rotated or not has_allowed_ips or not listener_ready:
                    bootstrap_reason = "credential_rotation" if rotated else "agent_boot"
                    payload = self._request_vnc_bootstrap(reason=bootstrap_reason)
                    if payload:
                        self._apply_bootstrap_payload(payload)
                        self._mark_engine_ready("bootstrap_api")
                self._ensure_always_on(reason="credential_rotation" if rotated else "always_on_check")
            except Exception as exc:
                self._log(f"VNC always-on loop error: {exc}", error=True)
            self._always_on_stop.wait(interval)

    def health_report(self) -> dict:
        if os.name != "nt":
            return {
                "status": "unsupported",
                "role_label": self.role_health_label,
                "detail": "Always-on UltraVNC is only supported on Windows agents.",
                "details": {
                    "running_status": "Unsupported",
                },
            }
        service_name = self.vnc._resolve_service_name()
        service_state = self.vnc._service_state_by_name(service_name) if service_name else None
        loop_alive = bool(self._always_on_thread and self._always_on_thread.is_alive())
        port_value = self._state_port()
        active_session_id = self._active_session_id()
        listener_ready = self.vnc.is_listener_ready(port_value)
        grace_snapshot = self._disconnect_grace_snapshot()
        if listener_ready:
            self._last_ready_at = max(self._last_ready_at, int(time.time()))
        details = {
            "running_status": str(service_state or "Stopped"),
            "service_state": str(service_state or "Stopped"),
            "listener_ip": "0.0.0.0",
            "listener_port": str(port_value),
            "service_name": service_name or ULTRAVNC_SERVICE_NAME,
            "listener_state": (
                "listening"
                if listener_ready
                else "not_listening"
            ),
            "listener_ready": "true" if listener_ready else "false",
            "ready": (
                "true"
                if listener_ready and service_state in {"RUNNING", "START_PENDING"}
                else "false"
            ),
            "last_ready_at": str(int(self._last_ready_at or 0)),
            "active_session_id": active_session_id,
            "credential_revision": str(self._credential_revision()),
            "disconnect_grace_active": "true" if grace_snapshot else "false",
            "disconnect_grace_until": str(int(grace_snapshot["deadline"])) if grace_snapshot else "0",
        }
        if service_state in {"RUNNING", "START_PENDING"} and listener_ready:
            return {
                "status": "healthy",
                "role_label": self.role_health_label,
                "detail": (
                    f"{service_name or ULTRAVNC_SERVICE_NAME} listener is ready for session {active_session_id}."
                    if active_session_id
                    else f"{service_name or ULTRAVNC_SERVICE_NAME} listener is ready for always-on access."
                ),
                "details": details,
            }
        if not loop_alive:
            return {
                "status": "unhealthy",
                "role_label": self.role_health_label,
                "detail": "Always-on reconciliation loop stopped.",
                "details": details,
            }
        if not self._engine_ready_for_vnc:
            return {
                "status": "recovering",
                "role_label": self.role_health_label,
                "detail": "Waiting for engine readiness before enabling UltraVNC.",
                "details": details,
            }
        if not self._controller_password():
            if grace_snapshot:
                return {
                    "status": "pending",
                    "role_label": self.role_health_label,
                    "detail": "UltraVNC warm reconnect grace is active.",
                    "details": details,
                }
            return {
                "status": "pending",
                "role_label": self.role_health_label,
                "detail": "UltraVNC is waiting for a controller credential.",
                "details": details,
            }
        if service_state in {"RUNNING", "START_PENDING"} and not listener_ready:
            return {
                "status": "recovering",
                "role_label": self.role_health_label,
                "detail": "UltraVNC service is running but the VNC listener is not ready yet.",
                "details": details,
            }
        return {
            "status": "recovering",
            "role_label": self.role_health_label,
            "detail": f"{service_name or ULTRAVNC_SERVICE_NAME} is {service_state or 'stopped'}; restart will be retried.",
            "details": details,
        }

    def register_events(self) -> None:
        sio = self.ctx.sio

        @sio.on("vpn_tunnel_start")
        async def _vpn_tunnel_start(payload):
            if isinstance(payload, dict):
                target_agent = payload.get("agent_id")
                if target_agent and str(target_agent).strip() != str(self.ctx.agent_id).strip():
                    return
                allowed_ips = payload.get("allowed_ips") or payload.get("engine_virtual_ip")
                self._update_state(allowed_ips=allowed_ips if allowed_ips else None)
                self._mark_engine_ready("vpn_tunnel_start")
                self._ensure_always_on(reason="vpn_tunnel_start")

        @sio.on("vpn_tunnel_stop")
        async def _vpn_tunnel_stop(payload):
            reason = "server_stop"
            if isinstance(payload, dict):
                target_agent = payload.get("agent_id")
                if target_agent and str(target_agent).strip() != str(self.ctx.agent_id).strip():
                    return
                reason = payload.get("reason") or reason
            self._log(f"VNC stop requested (reason={reason}).")
            self._clear_disconnect_grace()
            self._release_session_busy()
            self.vnc.stop(reason=str(reason))

        @sio.on("vnc_start")
        async def _vnc_start(payload):
            if isinstance(payload, dict):
                target_agent = payload.get("agent_id")
                if target_agent and str(target_agent).strip() != str(self.ctx.agent_id).strip():
                    return
                port = payload.get("port")
                allowed_ips = payload.get("allowed_ips") or self._last_allowed_ips
                controller_password = (
                    payload.get("controller_password")
                    or payload.get("vnc_password")
                    or payload.get("password")
                    or ""
                )
                view_only_password = payload.get("view_only_password") or payload.get("spectator_password") or ""
                remove_wallpaper = payload.get("remove_wallpaper")
                session_id = payload.get("session_id")
                credential_revision = payload.get("credential_revision")
                reason = payload.get("reason") or "vnc_session_start"
            else:
                port = None
                allowed_ips = self._last_allowed_ips
                controller_password = ""
                view_only_password = ""
                remove_wallpaper = None
                session_id = ""
                credential_revision = 0
                reason = "vnc_session_start"
            self._log(f"VNC start request received (reason={reason}).")
            self._update_state(
                allowed_ips=allowed_ips if allowed_ips else None,
                port=port if port is not None else None,
                remove_wallpaper=remove_wallpaper if isinstance(remove_wallpaper, bool) else None,
            )
            self._update_runtime_session(
                session_id=session_id,
                controller_password=None,
                view_only_password=None,
                credential_revision=None,
                remove_wallpaper=remove_wallpaper if isinstance(remove_wallpaper, bool) else None,
            )
            self._clear_disconnect_grace()
            self._acquire_session_busy(str(reason))
            self._mark_engine_ready("vnc_start_event")
            self._ensure_always_on(reason=str(reason))

        @sio.on("vnc_stop")
        async def _vnc_stop(payload):
            reason = "vnc_session_end"
            if isinstance(payload, dict):
                target_agent = payload.get("agent_id")
                if target_agent and str(target_agent).strip() != str(self.ctx.agent_id).strip():
                    return
                reason = payload.get("reason") or reason
            self._log(f"VNC stop requested (reason={reason}).")
            grace_scheduled = self._schedule_disconnect_grace(str(reason))
            self._clear_runtime_session()
            self._release_session_busy()
            if grace_scheduled:
                self._ensure_always_on(reason="disconnect_grace")
            else:
                self._ensure_always_on(reason="session_idle")

    def stop_all(self) -> None:
        try:
            self._always_on_stop.set()
        except Exception:
            pass
        self._clear_disconnect_grace()
        self._release_session_busy()
