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
        if candidate.is_dir():
            return candidate
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


def _ensure_ultravnc_ini(config_dir: Path, port: int) -> Optional[Path]:
    try:
        config_dir.mkdir(parents=True, exist_ok=True)
    except Exception:
        pass
    ini_path = config_dir / "ultravnc.ini"
    try:
        lines = [
            "UseRegistry=0",
            "AuthRequired=1",
            "MSLogonRequired=0",
            "NewMSLogon=0",
            f"PortNumber={port}",
            "AutoPortSelect=0",
            "SocketConnect=1",
            "HTTPConnect=0",
            "AllowShutdown=0",
            "DisableTrayIcon=1",
            "EnableFileTransfer=0",
        ]
        ini_path.write_text("\n".join(lines), encoding="utf-8")
        return ini_path
    except Exception as exc:
        _write_log(f"Failed to write ultravnc.ini: {exc}")
        return None


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
        self._password_tool: Optional[str] = None
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
            time.sleep(1)
            subprocess.run(["sc.exe", "start", service_name], capture_output=True, text=True, check=False)
            if not self._wait_for_service(service_name, timeout=8.0):
                _write_log(f"UltraVNC service restart timed out (service={service_name}).")
        except Exception as exc:
            _write_log(f"Failed to restart UltraVNC service: {exc}")

    def _ensure_service_running(self) -> bool:
        if os.name != "nt":
            return False
        service_name = self._resolve_service_name()
        state = self._service_state_by_name(service_name) if service_name else None
        if state == "RUNNING":
            return True
        if state == "START_PENDING" and service_name:
            return self._wait_for_service(service_name, timeout=10.0)
        if not self._vnc_exe:
            self._vnc_exe = _resolve_vnc_exe()
        if not self._vnc_exe:
            return False
        try:
            if state is None:
                subprocess.run([self._vnc_exe, "-install"], capture_output=True, text=True, check=False)
                service_name = self._resolve_service_name(refresh=True)
            if not service_name:
                service_name = ULTRAVNC_SERVICE_NAME
            subprocess.run(
                ["sc.exe", "config", service_name, "start=", "auto"],
                capture_output=True,
                text=True,
                check=False,
            )
            start_result = subprocess.run(
                ["sc.exe", "start", service_name],
                capture_output=True,
                text=True,
                check=False,
            )
            start_output = (start_result.stdout or "") + (start_result.stderr or "")
            if "SERVICE_ALREADY_RUNNING" in start_output:
                return True
        except Exception as exc:
            _write_log(f"Failed to ensure UltraVNC service running: {exc}")
            return False
        return self._wait_for_service(service_name, timeout=10.0)

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

    def _apply_password(self, config_dir: Path, password: str) -> Optional[str]:
        if not password:
            _write_log("VNC password missing; refusing to start without auth.")
            return None
        trimmed = str(password)[:8]
        if trimmed != password:
            _write_log("VNC password trimmed to 8 characters for UltraVNC compatibility.")
        if not self._password_tool:
            self._password_tool = _resolve_vnc_password_tool(config_dir)
        if not self._password_tool:
            _write_log(
                "VNC password tool not found; expected createpassword.exe under "
                "Dependencies/UltraVNC_Server/tools or payload."
            )
            return None
        try:
            result = subprocess.run(
                [self._password_tool, "-secure", trimmed],
                cwd=str(config_dir),
                capture_output=True,
                text=True,
                check=False,
            )
            if result.returncode != 0:
                _write_log(f"Failed to apply VNC password: {result.stderr.strip()}")
                return None
            return trimmed
        except Exception as exc:
            _write_log(f"Failed to apply VNC password: {exc}")
            return None

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

            exe_path = Path(self._vnc_exe)
            config_dir = exe_path.parent
            ini_path = _ensure_ultravnc_ini(config_dir, port_value)
            if not ini_path:
                return
            applied_password = self._apply_password(config_dir, password or "")
            if not applied_password:
                return

            if not self._ensure_service_running():
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
            self.vnc._ensure_service_running()
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
