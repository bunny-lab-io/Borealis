# ======================================================
# Data\Agent\Roles\role_RemoteShell.py
# Description: Cross-platform remote shell TCP server for VPN shell access.
# Engine connects over WireGuard /32.
#
# API Endpoints (if applicable): None
# ======================================================

"""VPN remote shell server for the WireGuard tunnel (Windows PowerShell + Linux Bash)."""

from __future__ import annotations

import base64
import json
import os
import platform
import select
import socket
import subprocess
import threading
import time
from pathlib import Path
from typing import Any, Optional

try:
    from runtime_paths import agent_logs_root
except Exception:
    import sys

    base_dir = Path(__file__).resolve().parents[1]
    if str(base_dir) not in sys.path:
        sys.path.insert(0, str(base_dir))
    from runtime_paths import agent_logs_root

try:
    from update_state import busy_activity
except Exception:
    busy_activity = None

ROLE_NAME = "RemoteShell"
ROLE_CONTEXTS = ["system"]


def _log_path() -> Path:
    # Keep shell logs alongside other VPN tunnel artifacts.
    root = agent_logs_root(__file__) / "VPN_Tunnel"
    root.mkdir(parents=True, exist_ok=True)
    return root / "remote_shell.log"


def _write_log(message: str) -> None:
    # Lightweight file logger for the shell bridge; avoid raising on failures.
    ts = time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime())
    try:
        _log_path().open("a", encoding="utf-8").write(f"[{ts}] [vpn-shell] {message}\n")
    except Exception:
        pass


def _b64encode(data: bytes) -> str:
    # Wire payloads are JSON lines; encode binary stdout safely.
    return base64.b64encode(data).decode("ascii").strip()


def _b64decode(value: str) -> bytes:
    # Decode base64-encoded stdin payloads from the engine.
    return base64.b64decode(value.encode("ascii"))


def _now_ms() -> int:
    return int(time.time() * 1000)


def _coerce_int(value: Any) -> Optional[int]:
    try:
        if value in (None, ""):
            return None
        return int(value)
    except Exception:
        return None


def _configure_tcp_socket(conn: socket.socket) -> None:
    try:
        conn.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    except Exception:
        pass


def _resolve_shell_port() -> int:
    # Use the configured port when present, otherwise default to 47002.
    raw = os.environ.get("BOREALIS_WIREGUARD_SHELL_PORT")
    try:
        value = int(raw) if raw is not None else 47002
    except Exception:
        value = 47002
    if value < 1 or value > 65535:
        return 47002
    return value


def _detect_shell_kind() -> str:
    if os.name == "nt":
        return "powershell"
    if platform.system().lower() == "linux":
        return "bash"
    return ""


def _resolve_powershell_binary() -> str:
    candidates = []
    override = (os.environ.get("BOREALIS_REMOTE_POWERSHELL_BIN") or "").strip()
    if override:
        candidates.append(override)
    try:
        system_root = os.environ.get("SystemRoot") or r"C:\Windows"
        candidates.append(os.path.join(system_root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
    except Exception:
        pass
    candidates.append("powershell.exe")
    for candidate in candidates:
        if not candidate:
            continue
        if candidate.lower().endswith(".exe"):
            if os.path.isfile(candidate):
                return candidate
        else:
            # Keep command-name fallback (powershell.exe) in case PATH resolves it.
            return candidate
    return "powershell.exe"


def _resolve_bash_binary() -> str:
    # Allow overrides, then prefer bash, then sh as a fallback.
    candidates = []
    override = (os.environ.get("BOREALIS_REMOTE_BASH_BIN") or "").strip()
    if override:
        candidates.append(override)
    candidates.extend(("/bin/bash", "/usr/bin/bash", "/bin/sh", "/usr/bin/sh"))
    for candidate in candidates:
        if candidate and os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate
    return ""


class ShellSession:
    def __init__(self, conn: socket.socket, address: tuple[str, int], shell_kind: str, shell_bin: str) -> None:
        self.conn = conn
        self.address = address
        self.shell_kind = shell_kind
        self.shell_bin = shell_bin
        self.proc: Optional[subprocess.Popen] = None
        self._stop = threading.Event()
        self._closed = threading.Event()
        self.input_messages = 0
        self.input_bytes = 0
        self.output_lines = 0
        self.output_bytes = 0
        self._busy_lease = None
        self._last_input_meta: dict[str, Any] = {}
        self.engine_session_id = ""

    def _log(self, message: str) -> None:
        session_id = str(self.engine_session_id or "").strip()
        if session_id:
            _write_log(f"{message} session_id={session_id}")
            return
        _write_log(message)

    def _capture_session_id(self, msg: dict[str, Any]) -> str:
        session_id = str(msg.get("session_id") or "").strip()
        if session_id and not self.engine_session_id:
            self.engine_session_id = session_id
        return self.engine_session_id

    def _send_control_message(self, payload_obj: dict[str, Any], *, log_label: str) -> bool:
        session_id = str(self.engine_session_id or "").strip()
        if session_id and "session_id" not in payload_obj:
            payload_obj["session_id"] = session_id
        try:
            self.conn.sendall(json.dumps(payload_obj).encode("utf-8") + b"\n")
            return True
        except Exception as exc:
            self._log(f"{log_label} failed: {exc}")
            return False

    def _handle_control_message(self, msg: dict[str, Any]) -> bool:
        self._capture_session_id(msg)
        msg_type = str(msg.get("type") or "").strip().lower()
        if msg_type != "ping":
            return False
        ping_id = str(msg.get("ping_id") or "").strip()
        sent_at_ms = _coerce_int(msg.get("sent_at_ms"))
        agent_received_at_ms = _now_ms()
        agent_pong_at_ms = _now_ms()
        payload_obj = {
            "type": "pong",
            "agent_received_at_ms": agent_received_at_ms,
            "agent_pong_at_ms": agent_pong_at_ms,
        }
        if ping_id:
            payload_obj["ping_id"] = ping_id
        if sent_at_ms is not None:
            payload_obj["sent_at_ms"] = sent_at_ms
        if self._send_control_message(payload_obj, log_label="Shell readiness pong send"):
            transit_ms = (
                str(max(0, agent_received_at_ms - sent_at_ms))
                if sent_at_ms is not None
                else "-"
            )
            self._log(
                "Shell readiness pong sent ping_id={0} sent_at_ms={1} recv_at_ms={2} transit_ms={3}".format(
                    ping_id or "-",
                    sent_at_ms if sent_at_ms is not None else "-",
                    agent_received_at_ms,
                    transit_ms,
                )
            )
        return True

    def start(self) -> None:
        # Spawn an interactive shell process and bridge stdin/stdout.
        self._log(f"Shell session starting for {self.address[0]}:{self.address[1]} type={self.shell_kind}")
        if callable(busy_activity):
            try:
                self._busy_lease = busy_activity(
                    "remote_shell",
                    metadata={
                        "remote_ip": self.address[0],
                        "remote_port": self.address[1],
                        "shell_kind": self.shell_kind,
                    },
                ).acquire()
            except Exception as exc:
                self._log(f"Remote shell busy lease acquisition failed: {exc}")
        if self.shell_kind == "powershell":
            self._start_powershell()
            return
        if self.shell_kind == "bash":
            self._start_bash()
            return
        self._send_stdout(b"[borealis] Unsupported remote shell platform.\n")
        self.close()

    def _start_powershell(self) -> None:
        try:
            self.proc = subprocess.Popen(
                [self.shell_bin, "-NoLogo", "-NoProfile", "-NoExit", "-Command", "-"],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
                bufsize=0,
            )
        except Exception as exc:
            self._send_stdout(f"[borealis] Failed to start PowerShell: {exc}\n".encode("utf-8", errors="replace"))
            self.close()
            return
        self._log(
            "PowerShell subprocess started pid={0} shell={1}".format(
                getattr(self.proc, "pid", "-"),
                self.shell_bin,
            )
        )
        threading.Thread(target=self._reader_loop_powershell, daemon=True).start()
        self._writer_loop_powershell()

    def _start_bash(self) -> None:
        if not self.shell_bin:
            self._send_stdout(b"[borealis] No bash-compatible shell found on this agent.\n")
            self.close()
            return

        try:
            env = os.environ.copy()
            env.setdefault("TERM", "dumb")
            env.setdefault("NO_COLOR", "1")
            env.setdefault("CLICOLOR", "0")
            env.setdefault("FORCE_COLOR", "0")
            env.setdefault("SYSTEMD_COLORS", "0")
            cmd = [self.shell_bin]
            if os.path.basename(self.shell_bin) == "bash":
                cmd.extend(["--noprofile", "--norc", "-s"])
            else:
                cmd.append("-s")
            self.proc = subprocess.Popen(
                cmd,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                close_fds=True,
                start_new_session=True,
                env=env,
                bufsize=0,
            )
        except Exception as exc:
            self._send_stdout(f"[borealis] Failed to start bash shell: {exc}\n".encode("utf-8", errors="replace"))
            self.close()
            return

        threading.Thread(target=self._reader_loop_bash, daemon=True).start()
        self._writer_loop_bash()

    def _send_stdout(self, chunk: bytes) -> None:
        if not chunk:
            return
        self.output_lines += 1
        self.output_bytes += len(chunk)
        payload_obj = {"type": "stdout", "data": _b64encode(chunk), "agent_stdout_at_ms": _now_ms()}
        session_id = str(self.engine_session_id or "").strip()
        if session_id:
            payload_obj["session_id"] = session_id
        message_id = str(self._last_input_meta.get("message_id") or "").strip()
        if message_id:
            payload_obj["message_id"] = message_id
        sent_at_ms = _coerce_int(self._last_input_meta.get("sent_at_ms"))
        if sent_at_ms is not None:
            payload_obj["sent_at_ms"] = sent_at_ms
        agent_received_at_ms = _coerce_int(self._last_input_meta.get("agent_received_at_ms"))
        if agent_received_at_ms is not None:
            payload_obj["agent_received_at_ms"] = agent_received_at_ms
        payload = json.dumps(payload_obj)
        try:
            self.conn.sendall(payload.encode("utf-8") + b"\n")
        except Exception as exc:
            self._log(f"Shell stdout send failed: {exc}")

    def _reader_loop_powershell(self) -> None:
        if not self.proc or not self.proc.stdout:
            return
        stdout_fd = None
        try:
            stdout_fd = self.proc.stdout.fileno()
        except Exception:
            stdout_fd = None
        try:
            while not self._stop.is_set():
                if stdout_fd is not None:
                    try:
                        # Read raw pipe bytes so prompt fragments and short command output do not
                        # wait on newline-oriented buffering before we forward them to the engine.
                        chunk = os.read(stdout_fd, 4096)
                    except OSError as exc:
                        if self.proc and self.proc.poll() is None and not self._stop.is_set():
                            self._log(f"Shell stdout read error: {exc}")
                        break
                else:
                    chunk = self.proc.stdout.read(4096)
                if not chunk:
                    if stdout_fd is None and self.proc and self.proc.poll() is None:
                        time.sleep(0.05)
                        continue
                    break
                self._send_stdout(chunk)
                self._log(f"Shell stdout forwarded bytes={len(chunk)}")
        except Exception as exc:
            self._log(f"Shell stdout error: {exc}")

    def _reader_loop_bash(self) -> None:
        if not self.proc or not self.proc.stdout:
            return
        stdout_fd = None
        try:
            stdout_fd = self.proc.stdout.fileno()
        except Exception:
            stdout_fd = None
        try:
            while not self._stop.is_set():
                if stdout_fd is not None:
                    try:
                        readable, _, _ = select.select([stdout_fd], [], [], 0.5)
                    except Exception:
                        readable = []

                    if not readable:
                        if self.proc and self.proc.poll() is not None:
                            break
                        continue

                    try:
                        chunk = os.read(stdout_fd, 4096)
                    except OSError:
                        break
                else:
                    chunk = self.proc.stdout.read(4096)
                if not chunk:
                    if self.proc and self.proc.poll() is not None:
                        break
                    continue
                self._send_stdout(chunk)
                self._log(f"Shell stdout forwarded bytes={len(chunk)}")
        except Exception as exc:
            self._log(f"Shell stdout error: {exc}")

    def _writer_loop_powershell(self) -> None:
        # Read JSONL stdin from the engine and feed it into PowerShell.
        buffer = b""
        try:
            while not self._stop.is_set():
                try:
                    data = self.conn.recv(4096)
                except Exception as exc:
                    self._log(f"Shell stdin recv error: {exc}")
                    break
                if not data:
                    break
                buffer += data
                while b"\n" in buffer:
                    line, buffer = buffer.split(b"\n", 1)
                    if not line:
                        continue
                    try:
                        msg = json.loads(line.decode("utf-8"))
                    except Exception:
                        continue
                    self._capture_session_id(msg)
                    if self._handle_control_message(msg):
                        continue
                    if msg.get("type") == "stdin":
                        payload = msg.get("data") or ""
                        if self.proc and self.proc.stdin:
                            try:
                                decoded = _b64decode(str(payload))
                                message_id = str(msg.get("message_id") or "").strip()
                                sent_at_ms = _coerce_int(msg.get("sent_at_ms"))
                                agent_received_at_ms = _now_ms()
                                self._last_input_meta = {
                                    "message_id": message_id,
                                    "sent_at_ms": sent_at_ms,
                                    "agent_received_at_ms": agent_received_at_ms,
                                    "session_id": self.engine_session_id,
                                }
                                self.proc.stdin.write(decoded)
                                self.proc.stdin.flush()
                                self.input_messages += 1
                                self.input_bytes += len(decoded)
                                transit_ms = (
                                    str(max(0, agent_received_at_ms - sent_at_ms))
                                    if sent_at_ms is not None
                                    else "-"
                                )
                                self._log(
                                    "Shell stdin received bytes={0} message_id={1} sent_at_ms={2} recv_at_ms={3} transit_ms={4}".format(
                                        len(decoded),
                                        message_id or "-",
                                        sent_at_ms if sent_at_ms is not None else "-",
                                        agent_received_at_ms,
                                        transit_ms,
                                    )
                                )
                            except Exception:
                                self._log("Shell stdin write failed.")
                    if msg.get("type") == "close":
                        self._stop.set()
                        self._log("Shell close requested by engine.")
                        break
        finally:
            self.close()

    def _writer_loop_bash(self) -> None:
        # Read JSONL stdin from the engine and feed it into bash/sh.
        buffer = b""
        try:
            while not self._stop.is_set():
                try:
                    data = self.conn.recv(4096)
                except Exception as exc:
                    self._log(f"Shell stdin recv error: {exc}")
                    break
                if not data:
                    break
                buffer += data
                while b"\n" in buffer:
                    line, buffer = buffer.split(b"\n", 1)
                    if not line:
                        continue
                    try:
                        msg = json.loads(line.decode("utf-8"))
                    except Exception:
                        continue
                    self._capture_session_id(msg)
                    if self._handle_control_message(msg):
                        continue
                    if msg.get("type") == "stdin":
                        payload = msg.get("data") or ""
                        if not self.proc or not self.proc.stdin:
                            continue
                        try:
                            decoded = _b64decode(str(payload))
                            normalized = decoded.replace(b"\r\n", b"\n").replace(b"\r", b"\n")
                            message_id = str(msg.get("message_id") or "").strip()
                            sent_at_ms = _coerce_int(msg.get("sent_at_ms"))
                            agent_received_at_ms = _now_ms()
                            self._last_input_meta = {
                                "message_id": message_id,
                                "sent_at_ms": sent_at_ms,
                                "agent_received_at_ms": agent_received_at_ms,
                                "session_id": self.engine_session_id,
                            }
                            self.proc.stdin.write(normalized)
                            self.proc.stdin.flush()
                            self.input_messages += 1
                            self.input_bytes += len(normalized)
                            transit_ms = (
                                str(max(0, agent_received_at_ms - sent_at_ms))
                                if sent_at_ms is not None
                                else "-"
                            )
                            self._log(
                                "Shell stdin received bytes={0} message_id={1} sent_at_ms={2} recv_at_ms={3} transit_ms={4}".format(
                                    len(normalized),
                                    message_id or "-",
                                    sent_at_ms if sent_at_ms is not None else "-",
                                    agent_received_at_ms,
                                    transit_ms,
                                )
                            )
                        except Exception:
                            self._log("Shell stdin write failed.")
                    if msg.get("type") == "close":
                        self._stop.set()
                        self._log("Shell close requested by engine.")
                        break
        finally:
            self.close()

    def close(self) -> None:
        # Ensure the TCP connection and shell child are cleaned up.
        if self._closed.is_set():
            return
        self._closed.set()
        self._stop.set()
        try:
            self.conn.close()
        except Exception:
            pass
        if self.proc:
            try:
                if self.proc.stdin:
                    self.proc.stdin.close()
            except Exception:
                pass
            try:
                if self.proc.stdout:
                    self.proc.stdout.close()
            except Exception:
                pass
            try:
                self.proc.terminate()
            except Exception:
                pass
            try:
                self.proc.wait(timeout=3)
            except Exception:
                try:
                    self.proc.kill()
                except Exception:
                    pass
        self._log(
            "Shell session closed inputs={0} input_bytes={1} output_lines={2} output_bytes={3}".format(
                self.input_messages,
                self.input_bytes,
                self.output_lines,
                self.output_bytes,
            )
        )
        if self.input_messages > 0 and self.output_lines == 0:
            self._log(
                "Shell session closed without stdout after input inputs={0} input_bytes={1}".format(
                    self.input_messages,
                    self.input_bytes,
                )
            )
        if self._busy_lease is not None:
            try:
                self._busy_lease.close()
            except Exception:
                pass
            self._busy_lease = None


class ShellServer:
    def __init__(self, shell_kind: str, shell_bin: str, host: str = "0.0.0.0", port: Optional[int] = None) -> None:
        self.host = host
        self.port = port or _resolve_shell_port()
        self.shell_kind = shell_kind
        self.shell_bin = shell_bin
        self._stop = threading.Event()
        self._state_lock = threading.Lock()
        self._listening = False
        self._last_error = ""
        self._last_checked_at = 0
        self._thread = threading.Thread(target=self._serve, daemon=True)
        self._thread.start()

    def _set_state(self, *, listening: bool, last_error: str = "") -> None:
        with self._state_lock:
            self._listening = bool(listening)
            self._last_error = str(last_error or "")
            self._last_checked_at = int(time.time())

    def health_report(self) -> dict:
        with self._state_lock:
            listening = self._listening
            last_error = self._last_error
            last_checked_at = self._last_checked_at or int(time.time())
        details = {
            "running_status": "Running" if listening else "Stopped",
            "listener_ip": self.host,
            "listener_port": str(self.port),
            "shell_binary": self.shell_bin,
            "last_error": last_error,
        }
        if not self._thread.is_alive():
            return {
                "status": "unhealthy",
                "role_label": "Remote Shell Service",
                "detail": "Remote shell listener thread stopped.",
                "details": details,
                "last_checked_at": last_checked_at,
            }
        if not self.shell_bin:
            return {
                "status": "unsupported",
                "role_label": "Remote Shell Service",
                "detail": "No compatible shell binary is available.",
                "details": {
                    **details,
                    "running_status": "Unsupported",
                },
                "last_checked_at": last_checked_at,
            }
        if listening:
            return {
                "status": "healthy",
                "role_label": "Remote Shell Service",
                "detail": f"Listening on {self.host}:{self.port}.",
                "details": {
                    **details,
                    "running_status": "Running",
                },
                "last_checked_at": last_checked_at,
            }
        if last_error:
            return {
                "status": "recovering",
                "role_label": "Remote Shell Service",
                "detail": f"Retrying after listener error: {last_error}",
                "details": {
                    **details,
                    "running_status": "Recovering",
                },
                "last_checked_at": last_checked_at,
            }
        return {
            "status": "recovering",
            "role_label": "Remote Shell Service",
            "detail": "Waiting for listener startup.",
            "details": {
                **details,
                "running_status": "Recovering",
            },
            "last_checked_at": last_checked_at,
        }

    def _serve(self) -> None:
        # Accept TCP shell connections; restrict to the WireGuard subnet and keep retrying on bind failures.
        retry_delay = 5
        while not self._stop.is_set():
            try:
                with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as server:
                    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                    server.bind((self.host, self.port))
                    server.listen(5)
                    server.settimeout(1.0)
                    self._set_state(listening=True, last_error="")
                    _write_log(
                        "VPN shell server listening on {0}:{1} type={2} shell={3}".format(
                            self.host,
                            self.port,
                            self.shell_kind,
                            self.shell_bin or "missing",
                        )
                    )
                    while not self._stop.is_set():
                        try:
                            conn, addr = server.accept()
                        except socket.timeout:
                            continue
                        except Exception as exc:
                            if self._stop.is_set():
                                break
                            self._set_state(listening=False, last_error=str(exc))
                            _write_log(
                                "VPN shell server accept error on {0}:{1}: {2}".format(
                                    self.host,
                                    self.port,
                                    exc,
                                )
                            )
                            break
                        remote_ip = addr[0]
                        if not remote_ip.startswith("10.255."):
                            _write_log(f"Rejected shell connection from {remote_ip}")
                            conn.close()
                            continue
                        _configure_tcp_socket(conn)
                        _write_log(f"Accepted shell connection from {remote_ip}")
                        session = ShellSession(conn, addr, self.shell_kind, self.shell_bin)
                        threading.Thread(target=session.start, daemon=True).start()
            except Exception as exc:
                if self._stop.is_set():
                    break
                self._set_state(listening=False, last_error=str(exc))
                _write_log(
                    "VPN shell server failed on {0}:{1} type={2} shell={3} error={4}; retrying in {5}s".format(
                        self.host,
                        self.port,
                        self.shell_kind,
                        self.shell_bin or "missing",
                        exc,
                        retry_delay,
                    )
                )
                time.sleep(retry_delay)

    def stop(self) -> None:
        self._stop.set()
        self._set_state(listening=False, last_error="listener stopped")


class Role:
    def __init__(self, ctx) -> None:
        # Start the shell server immediately when the role loads.
        self.ctx = ctx
        self.server = None
        self.role_health_label = "Remote Shell Service"
        try:
            _write_log(f"RemoteShell role initialized logs_root={agent_logs_root(__file__)}")
        except Exception:
            pass

        shell_kind = _detect_shell_kind()
        if shell_kind == "powershell":
            self.server = ShellServer(shell_kind="powershell", shell_bin=_resolve_powershell_binary())
            return
        if shell_kind == "bash":
            self.server = ShellServer(shell_kind="bash", shell_bin=_resolve_bash_binary())
            return

        _write_log(f"RemoteShell role skipped on unsupported platform '{platform.system()}'.")

    def register_events(self) -> None:
        return

    def health_report(self) -> dict:
        if self.server is None:
            return {
                "status": "unsupported",
                "role_label": self.role_health_label,
                "detail": f"Unsupported remote shell platform '{platform.system()}'.",
                "details": {
                    "running_status": "Unsupported",
                },
            }
        return self.server.health_report()

    def stop_all(self) -> None:
        if self.server and hasattr(self.server, "stop"):
            try:
                self.server.stop()
            except Exception:
                pass
        return
