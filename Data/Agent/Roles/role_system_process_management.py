from __future__ import annotations

import asyncio
import json
import os
import platform
import shlex
import shutil
import signal
import socket
import subprocess
import threading
import time
from typing import Any, Dict, List, Optional, Tuple

try:
    import psutil  # type: ignore
except Exception:  # pragma: no cover - optional dependency fallback
    psutil = None


ROLE_NAME = "process_management"
ROLE_CONTEXTS = ["system"]

IS_WINDOWS = os.name == "nt"
WINDOWS_NO_WINDOW = 0x08000000 if IS_WINDOWS else 0
REFRESH_INTERVAL_SECONDS = 5.0
ACTIVE_WINDOW_SECONDS = 65.0
CPU_WARMUP_DELAY_SECONDS = 0.18


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _coerce_int(value: Any, default: int = 0) -> int:
    try:
        if value in (None, ""):
            raise ValueError
        return int(float(value))
    except Exception:
        return default


def _coerce_float(value: Any, default: float = 0.0) -> float:
    try:
        if value in (None, ""):
            raise ValueError
        return float(value)
    except Exception:
        return default


def _hostname() -> str:
    try:
        return str(socket.gethostname() or "").strip()
    except Exception:
        return ""


def _command_line_text(value: Any) -> str:
    if isinstance(value, (list, tuple)):
        parts = [_clean_text(item) for item in value if _clean_text(item)]
        if not parts:
            return ""
        if IS_WINDOWS:
            rendered = []
            for part in parts:
                if not part:
                    continue
                if any(ch.isspace() for ch in part) and not (part.startswith('"') and part.endswith('"')):
                    rendered.append('"' + part.replace('"', '\\"') + '"')
                else:
                    rendered.append(part)
            return " ".join(rendered)
        return shlex.join(parts)
    return _clean_text(value)


def _normalize_cpu_percent(value: Any, cpu_count: int) -> float:
    raw = max(0.0, _coerce_float(value, 0.0))
    divisor = max(1, int(cpu_count or 1))
    return round(raw / divisor, 2)


def _normalize_memory_percent(memory_bytes: int, total_memory: int) -> float:
    if total_memory <= 0 or memory_bytes <= 0:
        return 0.0
    return round((float(memory_bytes) / float(total_memory)) * 100.0, 2)


def _sort_processes(processes: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    return sorted(
        processes,
        key=lambda item: (
            -_coerce_float(item.get("cpu_percent"), 0.0),
            -_coerce_float(item.get("memory_percent"), 0.0),
            _clean_text(item.get("name")).lower(),
            _coerce_int(item.get("pid"), 0),
        ),
    )


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.role_health_label = "Process Management"
        hooks = getattr(ctx, "hooks", {}) or {}
        self._log_hook = hooks.get("log_agent")
        self._stop = threading.Event()
        self._wakeup = threading.Event()
        self._lock = threading.RLock()
        self._snapshot: Dict[str, Any] = {
            "reported_at": 0,
            "processes": [],
            "refresh_interval_ms": int(REFRESH_INTERVAL_SECONDS * 1000),
        }
        self._last_error = ""
        self._last_refresh_at = 0
        self._last_process_count = 0
        self._active_until = 0.0
        self._cpu_count = self._resolve_cpu_count()
        self._supported, self._unsupported_reason = self._detect_support()
        self._thread: Optional[threading.Thread] = None
        if self._supported:
            self._prime_cpu_counters()
            self._thread = threading.Thread(target=self._poll_loop, daemon=True)
            self._thread.start()
        else:
            self._log(self._unsupported_reason or "Process management is unsupported on this platform.", error=True)

    def _detect_support(self) -> Tuple[bool, str]:
        if psutil is not None:
            return True, ""
        if IS_WINDOWS and (shutil.which("powershell.exe") or shutil.which("powershell")):
            return True, ""
        if not IS_WINDOWS and shutil.which("ps"):
            return True, ""
        return False, f"Unsupported process-management platform '{platform.system()}'."

    def _resolve_cpu_count(self) -> int:
        try:
            if psutil is not None and hasattr(psutil, "cpu_count"):
                return int(psutil.cpu_count(logical=True) or 1)
        except Exception:
            pass
        try:
            return int(os.cpu_count() or 1)
        except Exception:
            return 1

    def _log(self, message: str, *, error: bool = False) -> None:
        if callable(self._log_hook):
            try:
                self._log_hook(message, fname="process_management.log")
                if error:
                    self._log_hook(message, fname="agent.error.log")
            except Exception:
                pass

    def _run_subprocess(self, args: List[str], *, timeout: int = 30) -> subprocess.CompletedProcess:
        return subprocess.run(
            args,
            capture_output=True,
            text=True,
            timeout=timeout,
            creationflags=WINDOWS_NO_WINDOW,
            check=False,
        )

    def _prime_cpu_counters(self) -> None:
        if psutil is None:
            return
        try:
            for proc in psutil.process_iter():
                try:
                    proc.cpu_percent(interval=None)
                except Exception:
                    continue
            time.sleep(CPU_WARMUP_DELAY_SECONDS)
        except Exception:
            pass

    def _collect_psutil_processes(self) -> List[Dict[str, Any]]:
        if psutil is None:
            return []
        captured_at = int(time.time())
        try:
            total_memory = int(psutil.virtual_memory().total or 0)
        except Exception:
            total_memory = 0
        processes: List[Dict[str, Any]] = []
        attrs = ["pid", "ppid", "name", "cmdline", "exe", "memory_info", "create_time", "username", "status"]
        for proc in psutil.process_iter(attrs=attrs):
            try:
                info = proc.info or {}
            except Exception:
                info = {}
            pid = _coerce_int(info.get("pid") or getattr(proc, "pid", 0), 0)
            if pid <= 0:
                continue
            name = _clean_text(info.get("name"))
            if not name:
                try:
                    name = _clean_text(proc.name())
                except Exception:
                    name = f"PID {pid}"
            parent_pid = _coerce_int(info.get("ppid"), 0)
            try:
                raw_cpu = proc.cpu_percent(interval=None)
            except Exception:
                raw_cpu = 0.0
            memory_info = info.get("memory_info")
            memory_bytes = 0
            try:
                memory_bytes = int(getattr(memory_info, "rss", 0) or 0)
            except Exception:
                memory_bytes = 0
            try:
                memory_percent = round(float(proc.memory_percent() or 0.0), 2)
            except Exception:
                memory_percent = _normalize_memory_percent(memory_bytes, total_memory)
            command_line = _command_line_text(info.get("cmdline"))
            executable_path = _clean_text(info.get("exe"))
            if not command_line:
                command_line = executable_path or name
            create_time = _coerce_float(info.get("create_time"), 0.0)
            processes.append(
                {
                    "id": f"{pid}:{int(create_time or 0)}",
                    "pid": pid,
                    "parent_pid": parent_pid,
                    "name": name,
                    "cpu_percent": _normalize_cpu_percent(raw_cpu, self._cpu_count),
                    "raw_cpu_percent": round(max(0.0, _coerce_float(raw_cpu, 0.0)), 2),
                    "memory_percent": memory_percent,
                    "memory_bytes": memory_bytes,
                    "command_line": command_line,
                    "executable_path": executable_path,
                    "username": _clean_text(info.get("username")),
                    "status": _clean_text(info.get("status")),
                    "created_at": create_time,
                    "captured_at": captured_at,
                }
            )
        return processes

    def _collect_windows_fallback_processes(self) -> List[Dict[str, Any]]:
        powershell = shutil.which("powershell.exe") or shutil.which("powershell") or "powershell.exe"
        command = (
            "$items = Get-CimInstance Win32_Process | "
            "Select-Object ProcessId,ParentProcessId,Name,CommandLine,ExecutablePath,WorkingSetSize; "
            "$items | ConvertTo-Json -Depth 3 -Compress"
        )
        result = self._run_subprocess([powershell, "-NoProfile", "-Command", command], timeout=45)
        if result.returncode != 0:
            detail = _clean_text(result.stderr) or _clean_text(result.stdout) or f"exit {result.returncode}"
            raise RuntimeError(detail)
        payload = json.loads(result.stdout or "[]")
        if isinstance(payload, dict):
            payload = [payload]
        try:
            total_memory = int(psutil.virtual_memory().total or 0) if psutil is not None else 0
        except Exception:
            total_memory = 0
        captured_at = int(time.time())
        rows: List[Dict[str, Any]] = []
        for item in payload or []:
            if not isinstance(item, dict):
                continue
            pid = _coerce_int(item.get("ProcessId"), 0)
            if pid <= 0:
                continue
            name = _clean_text(item.get("Name")) or f"PID {pid}"
            memory_bytes = _coerce_int(item.get("WorkingSetSize"), 0)
            executable_path = _clean_text(item.get("ExecutablePath"))
            command_line = _clean_text(item.get("CommandLine")) or executable_path or name
            rows.append(
                {
                    "id": f"{pid}:0",
                    "pid": pid,
                    "parent_pid": _coerce_int(item.get("ParentProcessId"), 0),
                    "name": name,
                    "cpu_percent": 0.0,
                    "raw_cpu_percent": 0.0,
                    "memory_percent": _normalize_memory_percent(memory_bytes, total_memory),
                    "memory_bytes": memory_bytes,
                    "command_line": command_line,
                    "executable_path": executable_path,
                    "username": "",
                    "status": "",
                    "created_at": 0,
                    "captured_at": captured_at,
                }
            )
        return rows

    def _collect_posix_fallback_processes(self) -> List[Dict[str, Any]]:
        result = self._run_subprocess(
            ["ps", "-eo", "pid=,ppid=,pcpu=,rss=,comm=,args="],
            timeout=30,
        )
        if result.returncode != 0:
            detail = _clean_text(result.stderr) or _clean_text(result.stdout) or f"exit {result.returncode}"
            raise RuntimeError(detail)
        try:
            total_memory = os.sysconf("SC_PAGE_SIZE") * os.sysconf("SC_PHYS_PAGES")
        except Exception:
            total_memory = 0
        captured_at = int(time.time())
        rows: List[Dict[str, Any]] = []
        for raw_line in (result.stdout or "").splitlines():
            parts = raw_line.strip().split(None, 5)
            if len(parts) < 5:
                continue
            pid = _coerce_int(parts[0], 0)
            if pid <= 0:
                continue
            memory_bytes = _coerce_int(parts[3], 0) * 1024
            name = _clean_text(parts[4]) or f"PID {pid}"
            command_line = _clean_text(parts[5] if len(parts) > 5 else "") or name
            rows.append(
                {
                    "id": f"{pid}:0",
                    "pid": pid,
                    "parent_pid": _coerce_int(parts[1], 0),
                    "name": name,
                    "cpu_percent": _normalize_cpu_percent(parts[2], self._cpu_count),
                    "raw_cpu_percent": round(max(0.0, _coerce_float(parts[2], 0.0)), 2),
                    "memory_percent": _normalize_memory_percent(memory_bytes, int(total_memory or 0)),
                    "memory_bytes": memory_bytes,
                    "command_line": command_line,
                    "executable_path": "",
                    "username": "",
                    "status": "",
                    "created_at": 0,
                    "captured_at": captured_at,
                }
            )
        return rows

    def _collect_processes(self) -> Dict[str, Any]:
        if not self._supported:
            raise RuntimeError(self._unsupported_reason or "Process management is unsupported.")
        processes = self._collect_psutil_processes()
        if not processes:
            if IS_WINDOWS:
                processes = self._collect_windows_fallback_processes()
            else:
                processes = self._collect_posix_fallback_processes()
        parent_counts: Dict[int, int] = {}
        for item in processes:
            parent_pid = _coerce_int(item.get("parent_pid"), 0)
            if parent_pid > 0:
                parent_counts[parent_pid] = parent_counts.get(parent_pid, 0) + 1
        for item in processes:
            pid = _coerce_int(item.get("pid"), 0)
            item["child_count"] = parent_counts.get(pid, 0)
            item["has_children"] = parent_counts.get(pid, 0) > 0
        return {
            "reported_at": int(time.time()),
            "refresh_interval_ms": int(REFRESH_INTERVAL_SECONDS * 1000),
            "processes": _sort_processes(processes),
        }

    def _set_active_window(self) -> None:
        with self._lock:
            self._active_until = max(self._active_until, time.time() + ACTIVE_WINDOW_SECONDS)
        self._wakeup.set()

    def _store_snapshot(self, snapshot: Dict[str, Any]) -> Dict[str, Any]:
        processes = snapshot.get("processes") if isinstance(snapshot.get("processes"), list) else []
        with self._lock:
            self._snapshot = dict(snapshot)
            self._last_refresh_at = _coerce_int(snapshot.get("reported_at"), int(time.time()))
            self._last_process_count = len(processes)
            self._last_error = ""
            return dict(self._snapshot)

    def _record_error(self, message: str) -> None:
        with self._lock:
            self._last_error = message
        self._log(message, error=True)

    def _ensure_fresh_snapshot(self, *, max_age_seconds: float = REFRESH_INTERVAL_SECONDS) -> Dict[str, Any]:
        self._set_active_window()
        with self._lock:
            current = dict(self._snapshot)
            reported_at = _coerce_int(current.get("reported_at"), 0)
        if reported_at and (time.time() - reported_at) <= max_age_seconds:
            return current
        snapshot = self._collect_processes()
        return self._store_snapshot(snapshot)

    def _poll_loop(self) -> None:
        while not self._stop.is_set():
            with self._lock:
                active = time.time() < self._active_until
            if active:
                try:
                    self._store_snapshot(self._collect_processes())
                except Exception as exc:
                    self._record_error(f"Process inventory refresh failed: {exc}")
                self._wakeup.wait(REFRESH_INTERVAL_SECONDS)
                self._wakeup.clear()
                continue
            self._wakeup.wait(REFRESH_INTERVAL_SECONDS)
            self._wakeup.clear()

    def _process_exists(self, pid: int) -> bool:
        if pid <= 0:
            return False
        if psutil is not None:
            try:
                return bool(psutil.pid_exists(pid))
            except Exception:
                pass
        try:
            os.kill(pid, 0)
            return True
        except OSError:
            return False
        except Exception:
            return False

    def _terminate_with_psutil(self, pid: int, *, include_children: bool = False) -> List[int]:
        if psutil is None:
            return []
        process = psutil.Process(pid)
        targets = []
        if include_children:
            try:
                targets.extend(process.children(recursive=True))
            except Exception:
                pass
        targets.append(process)
        terminated: List[int] = []
        for target in targets:
            target_pid = _coerce_int(getattr(target, "pid", 0), 0)
            if target_pid <= 0 or target_pid == os.getpid():
                continue
            try:
                target.terminate()
                terminated.append(target_pid)
            except psutil.NoSuchProcess:
                continue
        gone, alive = psutil.wait_procs(targets, timeout=3)
        for target in alive:
            target_pid = _coerce_int(getattr(target, "pid", 0), 0)
            if target_pid <= 0 or target_pid == os.getpid():
                continue
            try:
                target.kill()
                if target_pid not in terminated:
                    terminated.append(target_pid)
            except psutil.NoSuchProcess:
                continue
        for target in gone:
            target_pid = _coerce_int(getattr(target, "pid", 0), 0)
            if target_pid > 0 and target_pid not in terminated:
                terminated.append(target_pid)
        return terminated

    def _terminate_fallback(self, pid: int, *, include_children: bool = False) -> List[int]:
        if IS_WINDOWS:
            command = ["taskkill", "/PID", str(pid), "/F"]
            if include_children:
                command.append("/T")
            result = self._run_subprocess(command, timeout=30)
            if result.returncode != 0:
                detail = _clean_text(result.stderr) or _clean_text(result.stdout) or f"exit {result.returncode}"
                raise RuntimeError(detail)
            return [pid]
        os.kill(pid, signal.SIGTERM)
        deadline = time.time() + 3
        while time.time() < deadline:
            if not self._process_exists(pid):
                return [pid]
            time.sleep(0.15)
        os.kill(pid, signal.SIGKILL)
        return [pid]

    def _terminate_process(self, pid: int, *, include_children: bool = False) -> Dict[str, Any]:
        if pid <= 0:
            return {"ok": False, "error": "pid_required", "message": "A process id is required."}
        if pid == os.getpid():
            return {
                "ok": False,
                "error": "protected_process",
                "message": "Borealis will not terminate its own agent process.",
            }
        if not self._process_exists(pid):
            return {"ok": False, "error": "process_not_found", "message": "The process is no longer running."}
        try:
            terminated = self._terminate_with_psutil(pid, include_children=include_children)
            if not terminated:
                terminated = self._terminate_fallback(pid, include_children=include_children)
        except PermissionError as exc:
            return {"ok": False, "error": "access_denied", "message": str(exc) or "Access denied."}
        except Exception as exc:
            return {"ok": False, "error": "termination_failed", "message": str(exc)}
        time.sleep(0.25)
        snapshot = self._store_snapshot(self._collect_processes())
        return {
            "ok": True,
            "terminated_pids": sorted(set(terminated)),
            **snapshot,
        }

    def handle_request(self, payload: Any) -> Dict[str, Any]:
        if not isinstance(payload, dict):
            return {"ok": False, "error": "invalid_request", "message": "Process request payload must be an object."}
        if not self._supported:
            return {
                "ok": False,
                "error": "unsupported",
                "message": self._unsupported_reason or "Process management is unsupported on this platform.",
            }
        action = _clean_text(payload.get("action")).lower()
        if action in {"", "list", "snapshot"}:
            try:
                snapshot = self._ensure_fresh_snapshot()
                return {"ok": True, **snapshot}
            except Exception as exc:
                self._record_error(f"Process inventory request failed: {exc}")
                return {"ok": False, "error": "agent_error", "message": str(exc)}
        if action in {"terminate", "kill", "end_task"}:
            return self._terminate_process(
                _coerce_int(payload.get("pid"), 0),
                include_children=bool(payload.get("include_children")),
            )
        return {"ok": False, "error": "invalid_action", "message": f"Unsupported process action '{action}'."}

    def register_events(self) -> None:
        sio = self.ctx.sio

        @sio.on("process_management_request")
        async def _on_process_management_request(payload):
            if not isinstance(payload, dict):
                return {"ok": False, "error": "invalid_request", "message": "Process request payload must be an object."}
            target_agent = _clean_text(payload.get("agent_id"))
            if target_agent and target_agent != _clean_text(self.ctx.agent_id):
                return {"ok": False, "error": "not_for_agent"}
            target_hostname = _clean_text(payload.get("hostname") or payload.get("target_hostname")).lower()
            if target_hostname and target_hostname != _hostname().lower():
                return {"ok": False, "error": "not_for_host"}
            return await asyncio.to_thread(self.handle_request, payload)

    def health_report(self) -> Dict[str, Any]:
        if not self._supported:
            return {
                "status": "unsupported",
                "role_label": self.role_health_label,
                "detail": self._unsupported_reason or "Process management is unsupported on this platform.",
                "details": {
                    "running_status": "Unsupported",
                },
            }
        thread_alive = bool(self._thread and self._thread.is_alive())
        with self._lock:
            last_error = self._last_error
            last_refresh_at = self._last_refresh_at
            process_count = self._last_process_count
            active = time.time() < self._active_until
        details = {
            "running_status": "Running" if thread_alive else "Stopped",
            "process_count": str(process_count),
            "last_refresh_at": str(last_refresh_at or 0),
            "active_polling": "true" if active else "false",
        }
        if last_error:
            details["last_error"] = last_error
        if not thread_alive:
            return {
                "status": "unhealthy",
                "role_label": self.role_health_label,
                "detail": "Process inventory refresh loop stopped.",
                "details": details,
            }
        if last_error:
            return {
                "status": "recovering",
                "role_label": self.role_health_label,
                "detail": last_error,
                "details": details,
            }
        return {
            "status": "healthy",
            "role_label": self.role_health_label,
            "detail": "Process management request handler is ready.",
            "details": details,
        }

    def stop_all(self) -> None:
        try:
            self._stop.set()
        except Exception:
            pass
        try:
            self._wakeup.set()
        except Exception:
            pass
