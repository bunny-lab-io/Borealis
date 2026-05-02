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
import ssl
import threading
import contextlib
import errno
import re
import ipaddress
from urllib.parse import urlparse, urlunparse
from typing import Any, Dict, Optional, List, Callable, Tuple

import requests
try:
    import psutil
except Exception:
    psutil = None
import aiohttp

import socketio
from security import AgentKeyStore
from signature_utils import decode_script_bytes as _decode_script_bytes, verify_and_store_script_signature as _verify_and_store_script_signature
from session_runtime import SessionHelperBroker, SessionHelperClient
try:
    from update_state import (
        read_installed_build_id as _read_installed_build_id,
        read_repo_build_id as _read_repo_build_id,
        read_update_status as _read_update_status,
        sync_installed_build_id as _sync_installed_build_id,
    )
except Exception:
    _read_installed_build_id = None
    _read_repo_build_id = None
    _read_update_status = None
    _sync_installed_build_id = None
try:
    import tray_state as _tray_state
except Exception:
    _tray_state = None

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519
from role_health import RoleHealthRegistry
try:
    from Roles.role_system_heartbeat import (
        initialize_early as _heartbeat_initialize_early,
        record_milestone as _heartbeat_record,
        complete_milestone as _heartbeat_complete,
        fail_milestone as _heartbeat_fail,
        flush_status as _heartbeat_flush,
    )
except Exception:
    def _heartbeat_initialize_early(*args, **kwargs):
        return None

    def _heartbeat_record(*args, **kwargs):
        return None

    def _heartbeat_complete(*args, **kwargs):
        return None

    def _heartbeat_fail(*args, **kwargs):
        return None

    def _heartbeat_flush(*args, **kwargs):
        return False

def _iter_exception_chain(exc: BaseException):
    seen = set()
    while exc and exc not in seen:
        yield exc
        seen.add(exc)
        if exc.__cause__ is not None:
            exc = exc.__cause__
        elif exc.__context__ is not None:
            exc = exc.__context__
        else:
            break


_TRANSIENT_REFRESH_STATUS_CODES = frozenset({500, 502, 503, 504})
_REFRESH_RETRY_DELAYS_SECONDS = (1.0, 2.0, 5.0)
_REFRESH_FALLBACK_MINIMUM_TTL_SECONDS = 15
_ENROLLMENT_RATE_LIMIT_MAX_ATTEMPTS = 5
_ENROLLMENT_RETRY_AFTER_MAX_SECONDS = 300.0
_AGENT_HEALTH_REFRESH_SECONDS = 300.0
_AGENT_HEALTH_REFRESH_INITIAL_DELAY_SECONDS = 60.0


def _response_retry_after_seconds(response: Any, *, default: float = 30.0) -> float:
    raw_value: Any = None
    try:
        raw_value = getattr(response, "headers", {}).get("Retry-After")
    except Exception:
        raw_value = None
    if raw_value in (None, ""):
        try:
            payload = response.json()
        except Exception:
            payload = {}
        if isinstance(payload, dict):
            raw_value = payload.get("retry_after")
    try:
        delay = float(raw_value)
    except Exception:
        delay = float(default)
    if delay <= 0:
        delay = 1.0
    return min(delay, _ENROLLMENT_RETRY_AFTER_MAX_SECONDS)

# Centralized logging helpers (Agent)
def _agent_logs_root() -> str:
    try:
        root = _find_project_root()
        return os.path.abspath(os.path.join(root, 'Agent', 'Logs'))
    except Exception:
        base_dir = os.path.abspath(os.path.dirname(__file__))
        return os.path.abspath(os.path.join(base_dir, 'Logs'))


def _rotate_daily(path: str):
    try:
        import datetime as _dt
        if os.path.isfile(path):
            mtime = os.path.getmtime(path)
            dt = _dt.datetime.fromtimestamp(mtime)
            today = _dt.datetime.now().date()
            if dt.date() != today:
                suffix = dt.strftime('%Y-%m-%d')
                newp = f"{path}.{suffix}"
                try:
                    os.replace(path, newp)
                except Exception:
                    pass
    except Exception:
        pass


# Early bootstrap logging (goes to agent.log)
_AGENT_CONTEXT_HEADER = "X-Borealis-Agent-Context"
_AGENT_SCOPE_PATTERN = re.compile(r"\\bscope=([A-Za-z0-9_-]+)", re.IGNORECASE)


def _canonical_scope_value(raw: Optional[str]) -> Optional[str]:
    if not raw:
        return None
    value = "".join(ch for ch in str(raw) if ch.isalnum() or ch in ("_", "-"))
    if not value:
        return None
    return value.upper()


def _agent_context_default() -> Optional[str]:
    suffix = globals().get("CONFIG_SUFFIX_CANONICAL")
    context = _canonical_scope_value(suffix)
    if context:
        return context
    service = globals().get("SERVICE_MODE_CANONICAL")
    context = _canonical_scope_value(service)
    if context:
        return context
    return None


def _infer_agent_scope(message: str, provided_scope: Optional[str] = None) -> Optional[str]:
    scope = _canonical_scope_value(provided_scope)
    if scope:
        return scope
    match = _AGENT_SCOPE_PATTERN.search(message or "")
    if match:
        scope = _canonical_scope_value(match.group(1))
        if scope:
            return scope
    return _agent_context_default()


def _format_agent_log_message(message: str, fname: str, scope: Optional[str] = None) -> str:
    context = _infer_agent_scope(message, scope)
    if fname == "agent.error.log":
        prefix = "[ERROR]"
        if context:
            prefix = f"{prefix}[CONTEXT-{context}]"
        return f"{prefix} {message}"
    if context:
        return f"[CONTEXT-{context}] {message}"
    return f"[INFO] {message}"


def _bootstrap_log(msg: str, *, scope: Optional[str] = None):
    try:
        base = _agent_logs_root()
        os.makedirs(base, exist_ok=True)
        path = os.path.join(base, 'agent.log')
        _rotate_daily(path)
        ts = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')
        line = _format_agent_log_message(msg, 'agent.log', scope)
        with open(path, 'a', encoding='utf-8') as fh:
            fh.write(f'[{ts}] {line}\n')
    except Exception:
        pass


def _describe_exception(exc: BaseException) -> str:
    try:
        primary = f"{exc.__class__.__name__}: {exc}"
    except Exception:
        primary = repr(exc)
    parts = [primary]
    try:
        cause = getattr(exc, "__cause__", None)
        if cause and cause is not exc:
            parts.append(f"cause={cause.__class__.__name__}: {cause}")
    except Exception:
        pass
    try:
        context = getattr(exc, "__context__", None)
        if context and context is not exc and context is not getattr(exc, "__cause__", None):
            parts.append(f"context={context.__class__.__name__}: {context}")
    except Exception:
        pass
    try:
        args = getattr(exc, "args", None)
        if isinstance(args, tuple) and len(args) > 1:
            parts.append(f"args={args!r}")
    except Exception:
        pass
    try:
        details = getattr(exc, "__dict__", None)
        if isinstance(details, dict):
            # Capture noteworthy nested attributes such as os_error/errno to help diagnose
            # connection failures that collapse into generic ConnectionError wrappers.
            for key in ("os_error", "errno", "code", "status"):
                if key in details and details[key]:
                    parts.append(f"{key}={details[key]!r}")
    except Exception:
        pass
    return "; ".join(part for part in parts if part)


def _log_exception_trace(prefix: str) -> None:
    try:
        tb = traceback.format_exc()
        if not tb:
            return
        for line in tb.rstrip().splitlines():
            _log_agent(f"{prefix} trace: {line}", fname="agent.error.log")
    except Exception:
        pass

# Headless/service mode flag (skip Qt and interactive UI)
SESSION_HELPER_MODE = ('--session-helper' in sys.argv) or (os.environ.get('BOREALIS_AGENT_MODE') == 'helper')
SYSTEM_SERVICE_MODE = (not SESSION_HELPER_MODE) and (('--system-service' in sys.argv) or (os.environ.get('BOREALIS_AGENT_MODE') == 'system'))
SERVICE_MODE = 'system' if SYSTEM_SERVICE_MODE else 'currentuser'
INTERACTIVE_RUNTIME_MODE = SESSION_HELPER_MODE or not SYSTEM_SERVICE_MODE
SERVICE_MODE_CANONICAL = SERVICE_MODE.upper()
_AGENT_STARTED_AT = int(time.time())
_TRAY_STATUS_LOCK = threading.RLock()
_TRAY_STATUS_STATE: Dict[str, Any] = {}
_PLANNED_SHUTDOWN_REASON: Optional[str] = None
_bootstrap_log(
    f'agent.py loaded; SYSTEM_SERVICE_MODE={SYSTEM_SERVICE_MODE}; SESSION_HELPER_MODE={SESSION_HELPER_MODE}; argv={sys.argv!r}'
)
if SYSTEM_SERVICE_MODE:
    try:
        _heartbeat_initialize_early(service_mode=SERVICE_MODE)
    except Exception:
        pass
def _argv_get(flag: str, default: str = None):
    try:
        if flag in sys.argv:
            idx = sys.argv.index(flag)
            if idx >= 0 and idx + 1 < len(sys.argv):
                return sys.argv[idx + 1]
    except Exception:
        pass
    return default
CONFIG_NAME_SUFFIX = _argv_get('--config', None)
HELPER_SESSION_ID = _argv_get('--helper-session-id', '')
HELPER_PIPE_ADDRESS = _argv_get('--helper-pipe-address', '')
HELPER_AUTH_TOKEN = _argv_get('--helper-auth-token', '')


def _canonical_config_suffix(raw_suffix: str) -> str:
    try:
        if not raw_suffix:
            return ''
        value = str(raw_suffix).strip()
        if not value:
            return ''
        normalized = value.lower()
        if normalized in ('svc', 'system', 'system_service', 'service'):
            return 'SYSTEM'
        if normalized in ('user', 'currentuser', 'interactive'):
            return 'CURRENTUSER'
        sanitized = ''.join(ch for ch in value if ch.isalnum() or ch in ('_', '-')).strip('_-')
        return sanitized
    except Exception:
        return ''


CONFIG_SUFFIX_CANONICAL = _canonical_config_suffix(CONFIG_NAME_SUFFIX)

INSTALLER_CODE_OVERRIDE = (
    (
        _argv_get('--installer-code')
        or _argv_get('--enrollment-code')
        or _argv_get('--enrollmentcode')
        or os.environ.get('BOREALIS_INSTALLER_CODE')
        or os.environ.get('BOREALIS_ENROLLMENT_CODE')
        or ''
    )
    .strip()
)


def _agent_guid_path() -> str:
    try:
        root = _find_project_root()
        return os.path.join(root, 'Agent', 'Borealis', 'Settings', 'Agent_GUID.txt')
    except Exception:
        return os.path.abspath(os.path.join(os.path.dirname(__file__), 'Settings', 'Agent_GUID.txt'))


def _settings_dir():
    try:
        return os.path.join(_find_project_root(), 'Agent', 'Borealis', 'Settings')
    except Exception:
        return os.path.abspath(os.path.join(os.path.dirname(__file__), 'Settings'))


def _is_literal_ip(value: Optional[str]) -> bool:
    try:
        if not value:
            return False
        candidate = value.strip().strip('[]')
        if '%' in candidate:
            candidate = candidate.split('%', 1)[0]
        ipaddress.ip_address(candidate)
        return True
    except Exception:
        return False


class _CrossProcessFileLock:
    def __init__(self, path: str) -> None:
        self.path = path
        self._handle = None

    def acquire(
        self,
        *,
        timeout: float = 120.0,
        poll_interval: float = 0.5,
        on_wait: Optional[Callable[[], None]] = None,
    ) -> None:
        directory = os.path.dirname(self.path)
        if directory:
            os.makedirs(directory, exist_ok=True)
        deadline = time.time() + timeout if timeout else None
        last_wait_log = 0.0
        while True:
            handle = open(self.path, 'a+b')
            try:
                self._try_lock(handle)
                self._handle = handle
                try:
                    handle.seek(0)
                    handle.truncate(0)
                    handle.write(f"pid={os.getpid()} ts={int(time.time())}\n".encode('utf-8'))
                    handle.flush()
                except Exception:
                    pass
                return
            except OSError as exc:
                handle.close()
                if not self._is_lock_unavailable(exc):
                    raise
                now = time.time()
                if on_wait and (now - last_wait_log) >= 2.0:
                    try:
                        on_wait()
                    except Exception:
                        pass
                    last_wait_log = now
                if deadline and now >= deadline:
                    raise TimeoutError('Timed out waiting for enrollment lock')
                time.sleep(poll_interval)
            except Exception:
                handle.close()
                raise

    def release(self) -> None:
        handle = self._handle
        if not handle:
            return
        try:
            self._unlock(handle)
        finally:
            try:
                handle.close()
            except Exception:
                pass
            self._handle = None

    @staticmethod
    def _is_lock_unavailable(exc: OSError) -> bool:
        err = exc.errno
        winerr = getattr(exc, 'winerror', None)
        unavailable = {errno.EACCES, errno.EAGAIN, getattr(errno, 'EWOULDBLOCK', errno.EAGAIN)}
        if err in unavailable:
            return True
        if winerr in (32, 33):
            return True
        return False

    @staticmethod
    def _try_lock(handle) -> None:
        handle.seek(0, os.SEEK_END)
        if handle.tell() == 0:
            try:
                handle.write(b'0')
                handle.flush()
            except Exception:
                pass
        handle.seek(0)
        if os.name == 'nt':
            import msvcrt  # type: ignore

            try:
                msvcrt.locking(handle.fileno(), msvcrt.LK_NBLCK, 1)
            except OSError:
                raise
        else:
            import fcntl  # type: ignore

            fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)

    @staticmethod
    def _unlock(handle) -> None:
        if os.name == 'nt':
            import msvcrt  # type: ignore

            try:
                msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)
            except OSError:
                pass
        else:
            import fcntl  # type: ignore

            try:
                fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
            except OSError:
                pass


_GUID_FILE_LOCK: Optional[_CrossProcessFileLock] = None


@contextlib.contextmanager
def _acquire_guid_lock(*, timeout: float = 60.0):
    global _GUID_FILE_LOCK
    if _GUID_FILE_LOCK is None:
        _GUID_FILE_LOCK = _CrossProcessFileLock(os.path.join(_settings_dir(), 'agent_guid.lock'))
    _GUID_FILE_LOCK.acquire(timeout=timeout)
    try:
        yield
    finally:
        _GUID_FILE_LOCK.release()


_ENROLLMENT_FILE_LOCK: Optional[_CrossProcessFileLock] = None


@contextlib.contextmanager
def _acquire_enrollment_lock(*, timeout: float = 180.0, on_wait: Optional[Callable[[], None]] = None):
    global _ENROLLMENT_FILE_LOCK
    if _ENROLLMENT_FILE_LOCK is None:
        _ENROLLMENT_FILE_LOCK = _CrossProcessFileLock(os.path.join(_settings_dir(), 'enrollment.lock'))
    _ENROLLMENT_FILE_LOCK.acquire(timeout=timeout, on_wait=on_wait)
    try:
        yield
    finally:
        _ENROLLMENT_FILE_LOCK.release()


_KEY_STORE_INSTANCE = None


def _key_store() -> AgentKeyStore:
    global _KEY_STORE_INSTANCE
    if _KEY_STORE_INSTANCE is None:
        scope = 'SYSTEM' if SYSTEM_SERVICE_MODE else 'CURRENTUSER'
        _KEY_STORE_INSTANCE = AgentKeyStore(_settings_dir(), scope=scope)
    return _KEY_STORE_INSTANCE


def _persist_agent_guid_local(guid: str):
    _persist_agent_guid_local_internal(guid, assume_locked=False)


def _persist_agent_guid_local_internal(guid: str, *, assume_locked: bool) -> None:
    normalized = _normalize_agent_guid(guid)
    if not normalized:
        return

    def _write():
        try:
            _key_store().save_guid(normalized)
        except Exception as exc:
            _log_agent(f'Unable to persist guid via key store: {exc}', fname='agent.error.log')
        path = _agent_guid_path()
        try:
            directory = os.path.dirname(path)
            if directory:
                os.makedirs(directory, exist_ok=True)
            existing = ''
            if os.path.isfile(path):
                try:
                    with open(path, 'r', encoding='utf-8') as fh:
                        existing = fh.read().strip()
                except Exception:
                    existing = ''
            if existing != normalized:
                with open(path, 'w', encoding='utf-8') as fh:
                    fh.write(normalized)
        except Exception as exc:
            _log_agent(f'Failed to persist agent GUID locally: {exc}', fname='agent.error.log')

        legacy_paths: List[str] = []
        try:
            root = _find_project_root()
            legacy_paths.extend(
                [
                    os.path.join(root, 'Agent', 'Borealis', 'agent_GUID'),
                    os.path.join(root, 'Agent', 'Settings', 'agent_GUID'),
                ]
            )
        except Exception:
            pass
        for legacy in legacy_paths:
            try:
                if legacy and os.path.isfile(legacy) and os.path.abspath(legacy) != os.path.abspath(path):
                    os.remove(legacy)
            except Exception:
                pass

    if assume_locked:
        _write()
    else:
        with _acquire_guid_lock():
            _write()

if INTERACTIVE_RUNTIME_MODE:
    # Reduce noisy Qt output and attempt to avoid Windows OleInitialize warnings
    os.environ.setdefault("QT_LOGGING_RULES", "qt.qpa.*=false;*.debug=false")
    from qt_compat import QEventLoop, QtCore, QtGui, QtWidgets, qt_enum
    try:
        # Swallow Qt framework messages to keep console clean
        def _qt_msg_handler(mode, context, message):
            return
        if QtCore is not None:
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
    """Locate the Borealis project root for the running agent tree."""
    current = os.path.abspath(os.path.dirname(__file__))
    discovered = None
    cur = current
    for _ in range(8):
        if (
            os.path.exists(os.path.join(cur, "Borealis.ps1"))
            or os.path.exists(os.path.join(cur, "users.json"))
            or os.path.isdir(os.path.join(cur, ".git"))
        ):
            discovered = cur
            break
        parent = os.path.dirname(cur)
        if parent == cur:
            break
        cur = parent

    override_root = os.environ.get("BOREALIS_ROOT") or os.environ.get("BOREALIS_PROJECT_ROOT")
    if override_root and os.path.isdir(override_root):
        override_abs = os.path.abspath(override_root)
        # Only honor the override when the running agent is actually inside that
        # tree; otherwise a stale service environment can redirect runtime paths
        # back to an older checkout.
        try:
            common_path = os.path.commonpath([current, override_abs])
        except Exception:
            common_path = ""
        if common_path and common_path == override_abs:
            return override_abs

    if discovered:
        return discovered

    if override_root and os.path.isdir(override_root):
        return os.path.abspath(override_root)

    # Heuristic fallback: two levels up from Agent/Borealis
    return os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))

# Simple file logger under Agent/Logs
def _log_agent(message: str, fname: str = 'agent.log', *, scope: Optional[str] = None):
    try:
        log_dir = _agent_logs_root()
        os.makedirs(log_dir, exist_ok=True)
        ts = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')
        path = os.path.join(log_dir, fname)
        parent = os.path.dirname(path)
        if parent:
            os.makedirs(parent, exist_ok=True)
        _rotate_daily(path)
        line = _format_agent_log_message(message, fname, scope)
        with open(path, 'a', encoding='utf-8') as fh:
            fh.write(f'[{ts}] {line}\n')
    except Exception:
        pass


def _mask_sensitive(value: str, *, prefix: int = 4, suffix: int = 4) -> str:
    try:
        if not value:
            return ''
        trimmed = value.strip()
        if len(trimmed) <= prefix + suffix:
            return '*' * len(trimmed)
        return f"{trimmed[:prefix]}***{trimmed[-suffix:]}"
    except Exception:
        return '***'


def _current_agent_build_id(*, sync: bool = False) -> str:
    try:
        if callable(_read_installed_build_id):
            build_id = (_read_installed_build_id() or "").strip()
            if build_id:
                return build_id
    except Exception:
        pass
    try:
        if callable(_read_repo_build_id):
            build_id = (_read_repo_build_id() or "").strip()
            if build_id:
                return build_id
    except Exception:
        pass
    return ""


def _current_agent_update_status() -> Dict[str, Any]:
    try:
        if callable(_read_update_status):
            payload = _read_update_status() or {}
            if isinstance(payload, dict):
                return payload
    except Exception:
        pass
    return {}


def _tray_server_url() -> str:
    try:
        return str(get_server_url() or "").strip()
    except Exception:
        return ""


def _default_role_health_snapshot() -> Dict[str, Any]:
    return {"roles": [], "reported_at": int(time.time())}


def _tray_snapshot_updates(*, client=None) -> Dict[str, Any]:
    resolved_client = client
    if resolved_client is None:
        try:
            resolved_client = HTTP_CLIENT
        except Exception:
            resolved_client = None

    server_url = ""
    guid_present = False
    verify_enabled = True
    if resolved_client is not None:
        try:
            server_url = str(getattr(resolved_client, "base_url", "") or "").strip()
        except Exception:
            server_url = ""
        try:
            guid_present = bool(getattr(resolved_client, "guid", None))
        except Exception:
            guid_present = False
        try:
            verify_enabled = bool(getattr(getattr(resolved_client, "session", None), "verify", True))
        except Exception:
            verify_enabled = True
    if not server_url:
        server_url = _tray_server_url()
    if not guid_present:
        try:
            guid_present = bool(_read_agent_guid_from_disk())
        except Exception:
            guid_present = False
    try:
        socket_connected = bool(getattr(sio, "connected", False))
    except Exception:
        socket_connected = False
    try:
        role_health = _collect_agent_role_health()
    except Exception:
        role_health = _default_role_health_snapshot()
    return {
        "server_url": server_url,
        "guid_present": guid_present,
        "verify_enabled": verify_enabled,
        "socket_connected": socket_connected,
        "role_health": role_health,
    }


def _sync_tray_status(updates: Optional[Dict[str, Any]] = None, *, client=None) -> Dict[str, Any]:
    if _tray_state is None:
        return {}
    timestamp = int(time.time())
    with _TRAY_STATUS_LOCK:
        state = dict(_TRAY_STATUS_STATE)
        if not state:
            state = _tray_state.build_default_snapshot(
                SERVICE_MODE,
                now=timestamp,
                started_at=_AGENT_STARTED_AT,
                pid=os.getpid(),
                server_url=_tray_server_url(),
            )
        state.update(_tray_snapshot_updates(client=client))
        if updates:
            state.update(dict(updates))
        state["schema_version"] = getattr(_tray_state, "SCHEMA_VERSION", 1)
        state["service_mode"] = SERVICE_MODE
        state["pid"] = int(state.get("pid") or os.getpid())
        state["started_at"] = int(state.get("started_at") or _AGENT_STARTED_AT)
        _TRAY_STATUS_STATE.clear()
        _TRAY_STATUS_STATE.update(state)
        if SESSION_HELPER_MODE:
            return dict(state)
        try:
            persisted = _tray_state.write_status_snapshot(SERVICE_MODE, state, now=timestamp)
        except Exception as exc:
            _log_agent(f"Failed to persist tray status snapshot: {exc}", fname="agent.error.log")
            return state
        _TRAY_STATUS_STATE.clear()
        _TRAY_STATUS_STATE.update(persisted)
        return dict(persisted)


def _clear_tray_error_if_matches(*kinds: str, client=None) -> Dict[str, Any]:
    normalized = {str(kind or "").strip().lower() for kind in kinds if str(kind or "").strip()}
    current_kind = str(_TRAY_STATUS_STATE.get("last_error_kind") or "").strip().lower()
    updates: Dict[str, Any] = {}
    if current_kind in normalized:
        updates["last_error_kind"] = ""
        updates["last_error_message"] = ""
    return _sync_tray_status(updates, client=client)


def _record_tray_auth_success(*, client=None) -> Dict[str, Any]:
    return _sync_tray_status(
        {
            "last_auth_success_at": int(time.time()),
            "last_error_kind": "",
            "last_error_message": "",
        },
        client=client,
    )


def _record_tray_heartbeat_success(*, client=None) -> Dict[str, Any]:
    return _sync_tray_status(
        {
            "last_heartbeat_success_at": int(time.time()),
            "last_error_kind": "",
            "last_error_message": "",
        },
        client=client,
    )


def _record_tray_error(kind: str, message: str, *, client=None, socket_connected: Optional[bool] = None) -> Dict[str, Any]:
    updates: Dict[str, Any] = {
        "last_error_kind": str(kind or "").strip().lower(),
        "last_error_message": str(message or "").strip(),
    }
    if socket_connected is not None:
        updates["socket_connected"] = bool(socket_connected)
    return _sync_tray_status(updates, client=client)


def _mark_planned_shutdown(reason: str) -> None:
    global _PLANNED_SHUTDOWN_REASON
    _PLANNED_SHUTDOWN_REASON = str(reason or "").strip()


def _background_process_popen_kwargs(cwd: str, *, windows: Optional[bool] = None) -> Dict[str, Any]:
    is_windows = (os.name == "nt") if windows is None else bool(windows)
    popen_kwargs: Dict[str, Any] = {
        "cwd": cwd,
        "stdin": subprocess.DEVNULL,
        "stdout": subprocess.DEVNULL,
        "stderr": subprocess.DEVNULL,
    }
    if is_windows:
        creationflags = (
            getattr(subprocess, "CREATE_NEW_PROCESS_GROUP", 0x00000200)
            | getattr(subprocess, "DETACHED_PROCESS", 0x00000008)
            | getattr(subprocess, "CREATE_NO_WINDOW", 0x08000000)
        )
        if creationflags:
            popen_kwargs["creationflags"] = creationflags
    else:
        popen_kwargs["start_new_session"] = True
    return popen_kwargs


def _launch_windows_restart_task_helper() -> bool:
    try:
        borealis_dir = os.path.abspath(os.path.dirname(__file__))
        helper_script = os.path.join(borealis_dir, "restart_agent_tasks.ps1")
        if not os.path.isfile(helper_script):
            raise FileNotFoundError(f"restart helper missing: {helper_script}")
        powershell = os.path.expandvars(r"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe")
        if not os.path.isfile(powershell):
            powershell = "powershell.exe"
        popen_kwargs = _background_process_popen_kwargs(borealis_dir, windows=True)
        subprocess.Popen(
            [
                powershell,
                "-NoProfile",
                "-ExecutionPolicy",
                "Bypass",
                "-WindowStyle",
                "Hidden",
                "-File",
                helper_script,
            ],
            **popen_kwargs,
        )
        return True
    except Exception as exc:
        _log_agent(f"Failed to launch Windows restart task helper: {exc}", fname="agent.error.log")
        return False


def _launch_replacement_agent_process(service_mode: Optional[str] = None) -> bool:
    normalized_mode = str(service_mode or SERVICE_MODE).strip().lower() or SERVICE_MODE
    try:
        args, popen_kwargs = _replacement_launch_spec(normalized_mode)
        subprocess.Popen(args, **popen_kwargs)
        return True
    except Exception as exc:
        _log_agent(f"Failed to launch replacement {normalized_mode} agent: {exc}", fname="agent.error.log")
        return False


def _replacement_launch_spec(
    service_mode: Optional[str] = None,
    *,
    windows: Optional[bool] = None,
) -> Tuple[List[str], Dict[str, Any]]:
    normalized_mode = str(service_mode or SERVICE_MODE).strip().lower() or SERVICE_MODE
    is_windows = (os.name == "nt") if windows is None else bool(windows)
    try:
        borealis_dir = os.path.abspath(os.path.dirname(__file__))
        venv_root = os.path.abspath(os.path.join(borealis_dir, os.pardir))
        venv_scripts = os.path.join(venv_root, "Scripts")
        popen_kwargs = _background_process_popen_kwargs(borealis_dir, windows=is_windows)
        if normalized_mode == "system" and is_windows:
            service_wrapper = os.path.join(borealis_dir, "launch_service.ps1")
            if os.path.isfile(service_wrapper):
                powershell = os.path.expandvars(r"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe")
                if not os.path.isfile(powershell):
                    powershell = "powershell.exe"
                return (
                    [
                        powershell,
                        "-NoProfile",
                        "-ExecutionPolicy",
                        "Bypass",
                        "-WindowStyle",
                        "Hidden",
                        "-File",
                        service_wrapper,
                    ],
                    popen_kwargs,
                )

        preferred: List[str] = []
        if normalized_mode == "currentuser":
            preferred.extend(
                [
                    os.path.join(venv_scripts, "pythonw.exe"),
                    os.path.join(venv_scripts, "python.exe"),
                ]
            )
        else:
            preferred.extend(
                [
                    os.path.join(venv_scripts, "python.exe"),
                    os.path.join(venv_scripts, "pythonw.exe"),
                ]
            )
        preferred.append(sys.executable)
        exe = ""
        for candidate in preferred:
            if candidate and os.path.isfile(candidate):
                exe = candidate
                break
        if not exe:
            exe = sys.executable
        agent_script = os.path.join(borealis_dir, "agent.py")
        args = [exe, "-W", "ignore::SyntaxWarning", agent_script]
        if normalized_mode == "system":
            args.extend(["--system-service", "--config", "SYSTEM"])
        else:
            args.extend(["--config", "CURRENTUSER"])
        return args, popen_kwargs
    except Exception as exc:
        raise RuntimeError(f"could not build replacement launch spec for {normalized_mode}: {exc}") from exc


def _shutdown_for_tray_restart() -> None:
    _mark_planned_shutdown("tray_restart")
    if SYSTEM_SERVICE_MODE:
        try:
            if ROLE_MANAGER is not None:
                ROLE_MANAGER.stop_all()
        except Exception:
            pass
        try:
            if ROLE_MANAGER_SYS is not None:
                ROLE_MANAGER_SYS.stop_all()
        except Exception:
            pass
        try:
            if AGENT_LOOP is not None:
                AGENT_LOOP.call_soon_threadsafe(AGENT_LOOP.stop)
        except Exception:
            pass
        threading.Thread(
            target=lambda: (time.sleep(0.5), os._exit(0)),
            name="borealis-tray-restart-exit",
            daemon=True,
        ).start()
        return

    try:
        app = QtWidgets.QApplication.instance()
        if app is not None:
            app.quit()
            return
    except Exception:
        pass
    os._exit(0)


def _process_tray_restart_request_now() -> bool:
    if _tray_state is None:
        return False
    try:
        payload = _tray_state.load_restart_request(SERVICE_MODE)
    except Exception:
        payload = {}
    if not payload:
        return False
    requested_at = int(payload.get("requested_at") or 0)
    ttl_seconds = int(getattr(_tray_state, "RESTART_REQUEST_TTL_SECONDS", 120) or 120)
    if requested_at > 0 and (int(time.time()) - requested_at) > ttl_seconds:
        _tray_state.clear_restart_request(SERVICE_MODE)
        return False
    request_id = str(payload.get("request_id") or "").strip() or "unknown"
    launcher = _launch_replacement_agent_process
    if os.name == "nt" and str(SERVICE_MODE or "").strip().lower() == "system":
        launcher = lambda mode: _launch_windows_restart_task_helper()
    if not launcher(SERVICE_MODE):
        _record_tray_error("transport", f"Restart request {request_id} could not launch a replacement process.")
        return False
    try:
        _tray_state.clear_restart_request(SERVICE_MODE)
    except Exception:
        pass
    if os.name == "nt" and str(SERVICE_MODE or "").strip().lower() == "system":
        try:
            _tray_state.clear_restart_request("currentuser")
        except Exception:
            pass
    _log_agent(f"Processing tray restart request request_id={request_id} scope={SERVICE_MODE}", fname="agent.log")
    _shutdown_for_tray_restart()
    return True


_MANUAL_UPDATE_REQUEST_LOCK = threading.Lock()
_WINDOWS_AUTO_UPDATER_TASK_NAME = "Borealis Agent (AutoUpdater)"
_LINUX_AUTO_UPDATER_SERVICE_NAME = "borealis-agent-updater.service"


def _manual_update_command(project_root: str) -> Tuple[Optional[List[str]], str]:
    if os.name == 'nt':
        schtasks = os.path.expandvars(r"%SystemRoot%\System32\schtasks.exe")
        if not os.path.isfile(schtasks):
            schtasks = "schtasks.exe"
        return [schtasks, "/Run", "/TN", _WINDOWS_AUTO_UPDATER_TASK_NAME], _WINDOWS_AUTO_UPDATER_TASK_NAME

    systemctl = shutil.which("systemctl") or "/bin/systemctl"
    if not systemctl or (systemctl != "systemctl" and not os.path.isfile(systemctl)):
        return None, _LINUX_AUTO_UPDATER_SERVICE_NAME
    return [systemctl, "start", "--no-block", _LINUX_AUTO_UPDATER_SERVICE_NAME], _LINUX_AUTO_UPDATER_SERVICE_NAME


def _launch_manual_agent_update(*, requested_by: str = "", requested_at: Any = None) -> Tuple[bool, str]:
    project_root = _find_project_root()
    command, updater_target = _manual_update_command(project_root)
    if not command:
        return False, f"updater_missing:{updater_target}"

    with _MANUAL_UPDATE_REQUEST_LOCK:
        env = os.environ.copy()
        env["BOREALIS_PROJECT_ROOT"] = project_root
        env.setdefault("BOREALIS_ROOT", project_root)
        run_kwargs: Dict[str, Any] = {
            "cwd": project_root,
            "env": env,
            "stdin": subprocess.DEVNULL,
            "stdout": subprocess.PIPE,
            "stderr": subprocess.PIPE,
            "text": True,
        }
        if os.name == "nt":
            creationflags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
            if creationflags:
                run_kwargs["creationflags"] = creationflags
        try:
            result = subprocess.run(command, timeout=30, **run_kwargs)
        except subprocess.TimeoutExpired:
            result = None
        except Exception as exc:
            return False, f"launcher_failed:{exc}"

    _log_agent(
        "Manual agent AutoUpdater start requested requested_by={0} requested_at={1} target={2}".format(
            requested_by or "-",
            requested_at if requested_at is not None else "-",
            updater_target,
        ),
        fname='agent.log',
    )
    if result is None:
        return True, "queued"
    if result.returncode == 0:
        return True, "queued"
    stderr_text = str(getattr(result, "stderr", "") or "").strip()
    stdout_text = str(getattr(result, "stdout", "") or "").strip()
    detail = stderr_text or stdout_text or f"exit_{result.returncode}"
    return False, f"launcher_failed:{detail}"


def _log_manual_update_request_failure(*, requested_by: str = "", requested_at: Any = None, detail: str = "") -> None:
    _log_agent(
        "Manual agent update request failed requested_by={0} requested_at={1} detail={2}".format(
            requested_by or "-",
            requested_at if requested_at is not None else "-",
            detail or "-",
        ),
        fname='agent.error.log',
    )


def _format_debug_pairs(pairs: Dict[str, Any]) -> str:
    try:
        parts = []
        for key, value in pairs.items():
            parts.append(f"{key}={value!r}")
        return ", ".join(parts)
    except Exception:
        return repr(pairs)


def _summarize_headers(headers: Dict[str, str]) -> str:
    try:
        rendered: List[str] = []
        for key, value in headers.items():
            lowered = key.lower()
            display = value
            if lowered == 'authorization':
                token = value.split()[-1] if value and ' ' in value else value
                display = f"Bearer {_mask_sensitive(token)}"
            rendered.append(f"{key}={display}")
        return ", ".join(rendered)
    except Exception:
        return '<unavailable>'


def _decode_base64_text(value):
    if not isinstance(value, str):
        return None
    stripped = value.strip()
    if not stripped:
        return ""
    cleaned = ''.join(stripped.split())
    if not cleaned:
        return ""
    try:
        decoded = base64.b64decode(cleaned, validate=True)
    except Exception:
        return None
    try:
        return decoded.decode('utf-8')
    except Exception:
        return decoded.decode('utf-8', errors='replace')


def _decode_base64_bytes(value):
    if not isinstance(value, str):
        return None
    stripped = value.strip()
    if not stripped:
        return b""
    cleaned = ''.join(stripped.split())
    if not cleaned:
        return b""
    try:
        return base64.b64decode(cleaned, validate=True)
    except Exception:
        return None


def _decode_script_payload(content, encoding_hint):
    if isinstance(content, str):
        encoding = str(encoding_hint or '').strip().lower()
        if encoding in ('base64', 'b64', 'base-64'):
            decoded = _decode_base64_text(content)
            if decoded is not None:
                return decoded
        decoded = _decode_base64_text(content)
        if decoded is not None:
            return decoded
        return content
    return ''

def _resolve_config_path():
    """
    Resolve the path for agent settings json in the centralized location:
    <ProjectRoot>/Agent/Borealis/Settings/agent_settings[_{suffix}].json

    Precedence/order:
    - If BOREALIS_AGENT_CONFIG is set (full path), use it.
    - Else if BOREALIS_AGENT_CONFIG_DIR is set (dir), use agent_settings.json under it.
    - Else use <ProjectRoot>/Agent/Borealis/Settings and migrate any legacy files into it.
    - If suffix is provided, seed from base if present.
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

    project_root = _find_project_root()
    settings_dir = os.path.join(project_root, 'Agent', 'Borealis', 'Settings')
    try:
        os.makedirs(settings_dir, exist_ok=True)
    except Exception:
        pass

    # Determine filename with optional suffix
    cfg_basename = 'agent_settings.json'
    suffix = CONFIG_SUFFIX_CANONICAL
    if suffix:
        cfg_basename = f"agent_settings_{suffix}.json"

    cfg_path = os.path.join(settings_dir, cfg_basename)
    if os.path.exists(cfg_path):
        return cfg_path

    # Migrate legacy suffixed config names to the new canonical form
    legacy_map = {
        'SYSTEM': ['agent_settings_svc.json'],
        'CURRENTUSER': ['agent_settings_user.json'],
    }
    try:
        legacy_candidates = []
        if suffix:
            for legacy_name in legacy_map.get(suffix.upper(), []):
                legacy_candidates.extend([
                    os.path.join(settings_dir, legacy_name),
                    os.path.join(project_root, legacy_name),
                    os.path.join(project_root, 'Agent', 'Settings', legacy_name),
                ])
        for legacy in legacy_candidates:
            if os.path.exists(legacy):
                try:
                    shutil.move(legacy, cfg_path)
                except Exception:
                    shutil.copy2(legacy, cfg_path)
                return cfg_path
    except Exception:
        pass

    # If using a suffixed config and there is a base config (new or legacy), seed from it
    try:
        if suffix:
            base_new = os.path.join(settings_dir, 'agent_settings.json')
            base_old_settings = os.path.join(project_root, 'Agent', 'Settings', 'agent_settings.json')
            base_legacy = os.path.join(project_root, 'agent_settings.json')
            seed_from = None
            if os.path.exists(base_new):
                seed_from = base_new
            elif os.path.exists(base_old_settings):
                seed_from = base_old_settings
            elif os.path.exists(base_legacy):
                seed_from = base_legacy
            if seed_from:
                try:
                    shutil.copy2(seed_from, cfg_path)
                    return cfg_path
                except Exception:
                    pass
    except Exception:
        pass

    # Migrate legacy root configs or prior Agent/Settings into Agent/Borealis/Settings
    try:
        legacy_names = [cfg_basename]
        try:
            if suffix:
                legacy_names.extend(legacy_map.get(suffix.upper(), []))
        except Exception:
            pass
        old_settings_dir = os.path.join(project_root, 'Agent', 'Settings')
        for legacy_name in legacy_names:
            legacy_root = os.path.join(project_root, legacy_name)
            if os.path.exists(legacy_root):
                try:
                    shutil.move(legacy_root, cfg_path)
                except Exception:
                    shutil.copy2(legacy_root, cfg_path)
                return cfg_path
            legacy_old_settings = os.path.join(old_settings_dir, legacy_name)
            if os.path.exists(legacy_old_settings):
                try:
                    shutil.move(legacy_old_settings, cfg_path)
                except Exception:
                    shutil.copy2(legacy_old_settings, cfg_path)
                return cfg_path
    except Exception:
        pass

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

    # Nothing to migrate; return desired path in new Settings dir
    return cfg_path

CONFIG_PATH = _resolve_config_path()
DEFAULT_CONFIG = {
    "config_file_watcher_interval": 2,
    "agent_id": "",
    "regions": {},
    "installer_code": ""
}


def _load_installer_code_from_file(path: str) -> str:
    try:
        with open(path, "r", encoding="utf-8") as fh:
            data = json.load(fh)
    except Exception:
        return ""
    if not isinstance(data, dict):
        return ""
    for key in ("enrollment_code", "installer_code"):
        value = data.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()
    return ""


def _clear_installer_code_from_file(path: str, rejected_code: str) -> bool:
    rejected = (rejected_code or "").strip()
    if not rejected or not path:
        return False
    try:
        with open(path, "r", encoding="utf-8") as fh:
            data = json.load(fh)
    except Exception:
        return False
    if not isinstance(data, dict):
        return False
    changed = False
    for key in ("enrollment_code", "installer_code"):
        value = data.get(key)
        if isinstance(value, str) and value.strip() == rejected:
            data[key] = ""
            changed = True
    if not changed:
        return False
    try:
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(data, fh)
        return True
    except Exception:
        return False


def _fallback_installer_code(current_path: str) -> str:
    settings_dir = os.path.dirname(current_path)
    candidates: List[str] = []
    suffix = CONFIG_SUFFIX_CANONICAL
    sibling_map = {
        "SYSTEM": "agent_settings_CURRENTUSER.json",
        "CURRENTUSER": "agent_settings_SYSTEM.json",
    }
    sibling_name = sibling_map.get(suffix or "")
    # Prefer the shared config written by installers before sibling runtime configs.
    candidates.append(os.path.join(settings_dir, "agent_settings.json"))
    if sibling_name:
        candidates.append(os.path.join(settings_dir, sibling_name))
    # Legacy location fallback
    try:
        project_root = _find_project_root()
        legacy_dir = os.path.join(project_root, "Agent", "Settings")
        candidates.append(os.path.join(legacy_dir, "agent_settings.json"))
        if sibling_name:
            candidates.append(os.path.join(legacy_dir, sibling_name))
    except Exception:
        pass

    current_abspath = os.path.abspath(current_path)
    for candidate in candidates:
        if not candidate:
            continue
        try:
            candidate_path = os.path.abspath(candidate)
        except Exception:
            continue
        if candidate_path == current_abspath or not os.path.isfile(candidate_path):
            continue
        code = _load_installer_code_from_file(candidate_path)
        if code:
            return code
    return ""

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
                # Strip deprecated/relocated fields
                for k in ('borealis_server_url','max_task_workers','agent_operating_system','created'):
                    if k in self.data:
                        self.data.pop(k, None)
                        # persist cleanup best-effort
                        try:
                            self._write()
                        except Exception:
                            pass
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
if SYSTEM_SERVICE_MODE:
    try:
        _heartbeat_complete("server_config_loaded", f"Config loaded from {CONFIG_PATH}.")
    except Exception:
        pass


class AgentHttpClient:
    def __init__(self):
        self.key_store = _key_store()
        self.identity = IDENTITY
        self.session = requests.Session()
        context_label = _agent_context_default()
        if context_label:
            self.session.headers.setdefault(_AGENT_CONTEXT_HEADER, context_label)
        self.base_url: Optional[str] = None
        self.guid: Optional[str] = None
        self.access_token: Optional[str] = None
        self.refresh_token: Optional[str] = None
        self.access_expires_at: Optional[int] = None
        self._auth_lock = threading.RLock()
        self._active_installer_code: Optional[str] = None
        self._socketio_http_session = None
        self._socketio_session_mode: Optional[Tuple[str, Optional[str]]] = None
        self._last_reload_state: Optional[Tuple[Optional[str], bool, bool, Optional[int]]] = None
        self.refresh_base_url()
        self._configure_verify()
        self._reload_tokens_from_disk()
        self.session.headers.setdefault("User-Agent", "Borealis-Agent/secure")

    # ------------------------------------------------------------------
    # Session helpers
    # ------------------------------------------------------------------
    def refresh_base_url(self) -> None:
        try:
            url = (get_server_url() or "").strip()
        except Exception:
            url = ""
        if not url:
            raise RuntimeError(
                "Borealis public server URL is missing or invalid. Configure an HTTPS FQDN in "
                "Agent/Borealis/Settings/server_url.txt or set BOREALIS_SERVER_URL."
            )
        if url.endswith("/"):
            url = url[:-1]
        self.base_url = url

    def _configure_verify(self) -> None:
        self.session.verify = True

    def _reload_tokens_from_disk(self) -> None:
        raw_guid = self.key_store.load_guid()
        normalized_guid = _normalize_agent_guid(raw_guid) if raw_guid else ''
        access_token = self.key_store.load_access_token()
        refresh_token = self.key_store.load_refresh_token()
        access_expiry = self.key_store.get_access_expiry()
        if normalized_guid and normalized_guid != (raw_guid or ""):
            try:
                self.key_store.save_guid(normalized_guid)
            except Exception:
                pass
        prev_state = self._last_reload_state
        self.guid = normalized_guid or None
        self.access_token = access_token if access_token else None
        self.refresh_token = refresh_token if refresh_token else None
        self.access_expires_at = access_expiry if access_expiry else None
        if self.access_token:
            self.session.headers.update({"Authorization": f"Bearer {self.access_token}"})
        else:
            self.session.headers.pop("Authorization", None)
        if self.guid:
            desired = _compose_agent_id(socket.gethostname(), self.guid, _get_context_label())
            existing = (CONFIG.data.get('agent_id') or '').strip()
            if desired and existing != desired:
                try:
                    _update_agent_id_for_guid(self.guid)
                except Exception:
                    pass
        state = (self.guid, bool(self.access_token), bool(self.refresh_token), self.access_expires_at)
        if state != prev_state:
            try:
                _log_agent(
                    "Reloaded tokens from disk "
                    f"guid={'yes' if self.guid else 'no'} "
                    f"access={'yes' if self.access_token else 'no'} "
                    f"refresh={'yes' if self.refresh_token else 'no'} "
                    f"expiry={self.access_expires_at}",
                    fname="agent.log",
                )
            except Exception:
                pass
        self._last_reload_state = state

    def auth_headers(self) -> Dict[str, str]:
        headers: Dict[str, str] = {}
        if self.access_token:
            headers["Authorization"] = f"Bearer {self.access_token}"
        context_label = _agent_context_default()
        if context_label:
            headers[_AGENT_CONTEXT_HEADER] = context_label
        return headers

    def configure_socketio(self, client: "socketio.AsyncClient") -> None:
        """Align the Socket.IO engine with standard CA and hostname validation."""

        try:
            verify = getattr(self.session, "verify", True)
            engine = getattr(client, "eio", None)
            if engine is None:
                _log_agent(
                    "SocketIO TLS alignment skipped; AsyncClient.eio missing",
                    fname="agent.error.log",
                )
                return

            http_iface = getattr(engine, "http", None)

            debug_info = {
                "verify_type": type(verify).__name__,
                "verify_value": verify,
                "engine_type": type(engine).__name__,
                "http_iface_present": http_iface is not None,
            }
            _log_agent(
                f"SocketIO TLS alignment start: {_format_debug_pairs(debug_info)}",
                fname="agent.log",
            )

            def _set_attr(target: Any, name: str, value: Any) -> None:
                if target is None:
                    return
                try:
                    setattr(target, name, value)
                except Exception:
                    pass

            def _reset_cached_session() -> None:
                if http_iface is None:
                    return
                try:
                    if hasattr(http_iface, "session"):
                        setattr(http_iface, "session", None)
                except Exception:
                    pass
            verify_flag = False if verify is False else True
            _set_attr(engine, "ssl_context", None)
            _set_attr(engine, "ssl_verify", verify_flag)
            _set_attr(engine, "verify_ssl", verify_flag)
            _set_attr(http_iface, "ssl_context", None)
            _set_attr(http_iface, "ssl_verify", verify_flag)
            _set_attr(http_iface, "verify_ssl", verify_flag)
            _reset_cached_session()
            self._apply_socketio_transport(client, verify=verify_flag)
            _log_agent(f"SocketIO TLS alignment using standard verify_flag={verify_flag}", fname="agent.log")
        except Exception:
            _log_agent(
                "SocketIO TLS alignment encountered unexpected error",
                fname="agent.error.log",
            )
            _log_exception_trace("configure_socketio")

    def socketio_ssl_params(self) -> Dict[str, Any]:
        # Socket.IO AsyncClient.connect does not accept SSL kwargs; configuration is
        # handled via the Engine.IO client setup in configure_socketio.
        return {}

    def _schedule_socketio_session_close(self, session) -> None:
        if session is None:
            return
        try:
            if session.closed:
                return
        except Exception:
            return
        try:
            loop = asyncio.get_running_loop()
            loop.create_task(session.close())
        except RuntimeError:
            try:
                asyncio.run(session.close())
            except Exception:
                pass

    def _set_socketio_http_session(
        self,
        client: "socketio.AsyncClient",
        session,
        mode: Optional[Tuple[str, Optional[str]]],
    ) -> None:
        engine = getattr(client, "eio", None)
        if engine is None:
            return
        if self._socketio_http_session is not session and self._socketio_http_session is not None:
            self._schedule_socketio_session_close(self._socketio_http_session)
        self._socketio_http_session = session
        self._socketio_session_mode = mode
        if session is None:
            engine.http = None
            engine.external_http = False
        else:
            engine.http = session
            engine.external_http = True

    def _apply_socketio_transport(
        self,
        client: "socketio.AsyncClient",
        *,
        verify: bool,
    ) -> None:
        engine = getattr(client, "eio", None)
        if engine is None:
            return
        options = getattr(engine, "websocket_extra_options", {}) or {}
        options = dict(options)
        options.pop("ssl", None)
        if not verify:
            options["ssl"] = False
        self._set_socketio_http_session(client, None, ("default", None))
        engine.ssl_verify = verify
        engine.websocket_extra_options = options

    # ------------------------------------------------------------------
    # Enrollment & token management
    # ------------------------------------------------------------------
    def ensure_authenticated(self) -> None:
        try:
            with self._auth_lock:
                self._ensure_authenticated_locked()
        except Exception as exc:
            _record_tray_error("auth", _describe_exception(exc), client=self, socket_connected=False)
            raise
        _record_tray_auth_success(client=self)

    def _ensure_authenticated_locked(self) -> None:
        self._reload_tokens_from_disk()
        self.refresh_base_url()
        if not self.guid or not self.refresh_token:
            self._perform_enrollment_locked()
        if not self.access_token or self._token_expiring_soon():
            self._refresh_access_token_locked()

    def _token_expiring_soon(self) -> bool:
        if not self.access_token:
            return True
        if not self.access_expires_at:
            return True
        return (self.access_expires_at - time.time()) < 60

    def perform_enrollment(self) -> None:
        try:
            with self._auth_lock:
                self._perform_enrollment_locked()
        except Exception as exc:
            _record_tray_error("auth", _describe_exception(exc), client=self, socket_connected=False)
            raise
        _record_tray_auth_success(client=self)

    def _perform_enrollment_locked(self) -> None:
        self._reload_tokens_from_disk()
        if self.guid and self.refresh_token:
            return
        code = self._resolve_installer_code()
        if not code:
            raise RuntimeError(
                "Installer code is required for enrollment. "
                "Set BOREALIS_INSTALLER_CODE, pass --installer-code, or update agent_settings.json."
            )
        self._active_installer_code = code

        wait_state = {"count": 0, "tokens_seen": False}

        def _on_lock_wait() -> None:
            wait_state["count"] += 1
            _log_agent(
                f"Enrollment waiting for shared lock scope={SERVICE_MODE} attempt={wait_state['count']}",
                fname="agent.log",
            )
            if not wait_state["tokens_seen"]:
                self._reload_tokens_from_disk()
                if self.guid and self.refresh_token:
                    wait_state["tokens_seen"] = True
                    _log_agent(
                        "Enrollment credentials detected while waiting for lock; will reuse when available",
                        fname="agent.log",
                    )

        try:
            with _acquire_enrollment_lock(timeout=180.0, on_wait=_on_lock_wait):
                self._reload_tokens_from_disk()
                if self.guid and self.refresh_token:
                    _log_agent(
                        "Enrollment skipped after acquiring lock; credentials already present",
                        fname="agent.log",
                    )
                    return

                self.refresh_base_url()
                base_url = self.base_url
                code_masked = _mask_sensitive(code)
                _log_agent(
                    "Enrollment handshake starting "
                    f"base_url={base_url} scope={SERVICE_MODE} "
                    f"fingerprint={SSL_KEY_FINGERPRINT[:16]} installer_code={code_masked}",
                    fname="agent.log",
                )
                client_nonce = os.urandom(32)
                payload = {
                    "hostname": socket.gethostname(),
                    "enrollment_code": code,
                    "agent_pubkey": PUBLIC_KEY_B64,
                    "client_nonce": base64.b64encode(client_nonce).decode("ascii"),
                }
                request_url = f"{self.base_url}/api/agent/enroll/request"
                request_attempt = 1
                while True:
                    _log_agent(
                        "Starting enrollment request... "
                        f"url={request_url} hostname={payload['hostname']} pubkey_prefix={PUBLIC_KEY_B64[:24]}"
                        f" attempt={request_attempt}",
                        fname="agent.log",
                    )
                    resp = self.session.post(request_url, json=payload, timeout=30)
                    _log_agent(
                        f"Enrollment request HTTP status={resp.status_code} retry_after={resp.headers.get('Retry-After')}"
                        f" body_len={len(resp.content)} attempt={request_attempt}",
                        fname="agent.log",
                    )
                    try:
                        resp.raise_for_status()
                        break
                    except requests.HTTPError:
                        snippet = resp.text[:512] if hasattr(resp, "text") else ""
                        _log_agent(
                            f"Enrollment request failed status={resp.status_code} body_snippet={snippet}"
                            f" attempt={request_attempt}",
                            fname="agent.error.log",
                        )
                        if resp.status_code == 429 and request_attempt < _ENROLLMENT_RATE_LIMIT_MAX_ATTEMPTS:
                            delay = _response_retry_after_seconds(resp)
                            _log_agent(
                                "Enrollment request rate limited; honoring Retry-After "
                                f"delay={delay:.1f}s attempt={request_attempt}",
                                fname="agent.log",
                            )
                            time.sleep(delay)
                            self._reload_tokens_from_disk()
                            if self.guid and self.refresh_token:
                                _log_agent(
                                    "Enrollment credentials detected after rate-limit wait; skipping re-enrollment",
                                    fname="agent.log",
                                )
                                return
                            request_attempt += 1
                            continue
                        if resp.status_code == 400:
                            try:
                                err_payload = resp.json()
                            except Exception:
                                err_payload = {}
                            if (err_payload or {}).get("error") in {"invalid_enrollment_code", "code_consumed"}:
                                self._reload_tokens_from_disk()
                                if self.guid and self.refresh_token:
                                    _log_agent(
                                        "Enrollment code rejected but existing credentials are present; skipping re-enrollment",
                                        fname="agent.log",
                                    )
                                    return
                                error_code = (err_payload or {}).get("error") or "invalid_enrollment_code"
                                self._discard_rejected_installer_code(code, reason=str(error_code))
                                raise RuntimeError(
                                    f"Enrollment code rejected by Engine ({error_code}); cleared persisted code. "
                                    "Provide a fresh enrollment code to retry."
                                )
                        raise
                data = resp.json()
                _log_agent(
                    "Enrollment request accepted "
                    f"status={data.get('status')} approval_ref={data.get('approval_reference')} "
                    f"poll_after_ms={data.get('poll_after_ms')}"
                    f" signing_key={'yes' if data.get('signing_key') else 'no'}",
                    fname="agent.log",
                )
                signing_key = data.get("signing_key")
                if signing_key:
                    try:
                        self.store_server_signing_key(signing_key)
                    except Exception as exc:
                        _log_agent(f'Unable to persist signing key from enrollment handshake: {exc}', fname='agent.error.log')
                if data.get("status") != "pending":
                    raise RuntimeError(f"Unexpected enrollment status: {data}")
                approval_reference = data.get("approval_reference")
                server_nonce_b64 = data.get("server_nonce")
                if not approval_reference or not server_nonce_b64:
                    raise RuntimeError("Enrollment response missing approval_reference or server_nonce")
                server_nonce = base64.b64decode(server_nonce_b64)
                poll_delay = max(int(data.get("poll_after_ms", 3000)) / 1000, 1)
                attempt = 1
                while True:
                    time.sleep(min(poll_delay, 15))
                    signature = self.identity.sign(server_nonce + approval_reference.encode("utf-8") + client_nonce)
                    poll_payload = {
                        "approval_reference": approval_reference,
                        "client_nonce": base64.b64encode(client_nonce).decode("ascii"),
                        "proof_sig": base64.b64encode(signature).decode("ascii"),
                    }
                    _log_agent(
                        f"Enrollment poll attempt={attempt} ref={approval_reference} delay={poll_delay}s",
                        fname="agent.log",
                    )
                    poll_resp = self.session.post(
                        f"{self.base_url}/api/agent/enroll/poll",
                        json=poll_payload,
                        timeout=30,
                    )
                    _log_agent(
                        "Enrollment poll response "
                        f"status_code={poll_resp.status_code} retry_after={poll_resp.headers.get('Retry-After')}"
                        f" body_len={len(poll_resp.content)}",
                        fname="agent.log",
                    )
                    try:
                        poll_resp.raise_for_status()
                    except requests.HTTPError:
                        snippet = poll_resp.text[:512] if hasattr(poll_resp, "text") else ""
                        _log_agent(
                            f"Enrollment poll failed attempt={attempt} status={poll_resp.status_code} "
                            f"body_snippet={snippet}",
                            fname="agent.error.log",
                        )
                        raise
                    poll_data = poll_resp.json()
                    _log_agent(
                        f"Enrollment poll decoded attempt={attempt} status={poll_data.get('status')}"
                        f" next_delay={poll_data.get('poll_after_ms')}"
                        f" guid_hint={poll_data.get('guid')}",
                        fname="agent.log",
                    )
                    status = poll_data.get("status")
                    if status == "pending":
                        poll_delay = max(int(poll_data.get("poll_after_ms", 5000)) / 1000, 1)
                        _log_agent(
                            f"Enrollment still pending attempt={attempt} new_delay={poll_delay}s",
                            fname="agent.log",
                        )
                        attempt += 1
                        continue
                    if status == "denied":
                        _log_agent("Enrollment denied by operator", fname="agent.error.log")
                        raise RuntimeError("Enrollment denied by operator")
                    if status in ("expired", "unknown"):
                        _log_agent(
                            f"Enrollment failed status={status} attempt={attempt}",
                            fname="agent.error.log",
                        )
                        raise RuntimeError(f"Enrollment failed with status={status}")
                    if status in ("approved", "completed"):
                        _log_agent(
                            f"Enrollment approved attempt={attempt} ref={approval_reference}",
                            fname="agent.log",
                        )
                        self._finalize_enrollment(poll_data)
                        break
                    raise RuntimeError(f"Unexpected enrollment poll response: {poll_data}")
        except TimeoutError:
            self._reload_tokens_from_disk()
            if self.guid and self.refresh_token:
                _log_agent(
                    "Enrollment lock wait timed out but credentials materialized; reusing existing tokens",
                    fname="agent.log",
                )
                return
            raise

    def _finalize_enrollment(self, payload: Dict[str, Any]) -> None:
        signing_key = payload.get("signing_key")
        if signing_key:
            try:
                self.store_server_signing_key(signing_key)
            except Exception as exc:
                _log_agent(f'Unable to persist signing key from enrollment approval: {exc}', fname='agent.error.log')
        guid = _normalize_agent_guid(payload.get("guid"))
        access_token = payload.get("access_token")
        refresh_token = payload.get("refresh_token")
        expires_in = int(payload.get("expires_in") or 900)
        if not (guid and access_token and refresh_token):
            raise RuntimeError("Enrollment approval response missing tokens or guid")
        _log_agent(
            "Enrollment approval payload received "
            f"guid={guid} access_token_len={len(access_token)} refresh_token_len={len(refresh_token)} "
            f"expires_in={expires_in}",
            fname="agent.log",
        )
        self.guid = guid
        self.access_token = access_token.strip()
        self.refresh_token = refresh_token.strip()
        expiry = int(time.time()) + max(expires_in - 5, 0)
        self.access_expires_at = expiry
        self.key_store.save_guid(self.guid)
        self.key_store.save_refresh_token(self.refresh_token)
        self.key_store.save_access_token(self.access_token, expires_at=expiry)
        self.key_store.set_access_binding(SSL_KEY_FINGERPRINT)
        self.session.headers.update({"Authorization": f"Bearer {self.access_token}"})
        try:
            _update_agent_id_for_guid(self.guid)
        except Exception as exc:
            _log_agent(f"Failed to update agent id after enrollment: {exc}", fname="agent.error.log")
        self._consume_installer_code()
        _log_agent(f"Enrollment finalized for guid={self.guid}", fname="agent.log")

    def refresh_access_token(self) -> None:
        try:
            with self._auth_lock:
                self._refresh_access_token_locked()
        except Exception as exc:
            _record_tray_error("auth", _describe_exception(exc), client=self, socket_connected=False)
            raise
        _record_tray_auth_success(client=self)

    def _refresh_access_token_locked(self) -> None:
        if not self.refresh_token or not self.guid:
            self._clear_tokens_locked()
            self._perform_enrollment_locked()
            return
        payload = {"guid": self.guid, "refresh_token": self.refresh_token}
        total_attempts = len(_REFRESH_RETRY_DELAYS_SECONDS) + 1
        last_error: Optional[BaseException] = None
        last_failure_transient = False

        for attempt in range(1, total_attempts + 1):
            resp: Optional[requests.Response] = None
            try:
                resp = self.session.post(
                    f"{self.base_url}/api/agent/token/refresh",
                    json=payload,
                    headers=self.auth_headers(),
                    timeout=20,
                )
                if resp.status_code in (401, 403):
                    error_code, snippet = self._error_details(resp)
                    if resp.status_code == 403 and error_code == 'guid_mismatch':
                        try:
                            _log_agent(
                                "Refresh token request saw guid mismatch; reloading credentials from disk",
                                fname="agent.log",
                            )
                        except Exception:
                            pass
                        self._reload_tokens_from_disk()
                        if self.access_token:
                            self.session.headers.update({"Authorization": f"Bearer {self.access_token}"})
                            return
                    if resp.status_code == 401 and self._should_retry_auth(resp.status_code, error_code):
                        _log_agent(
                            "Refresh token rejected; attempting re-enrollment"
                            f" error={error_code or '<unknown>'}",
                            fname="agent.error.log",
                        )
                        self._clear_tokens_locked()
                        self._perform_enrollment_locked()
                        return
                    _log_agent(
                        "Refresh token request forbidden "
                        f"status={resp.status_code} error={error_code or '<unknown>'}"
                        f" body_snippet={snippet}",
                        fname="agent.error.log",
                    )

                if resp.status_code in _TRANSIENT_REFRESH_STATUS_CODES and attempt < total_attempts:
                    error_code, snippet = self._error_details(resp)
                    delay = _REFRESH_RETRY_DELAYS_SECONDS[attempt - 1]
                    _log_agent(
                        "Refresh token transient failure; retrying "
                        f"attempt={attempt}/{total_attempts} status={resp.status_code} "
                        f"error={error_code or '<unknown>'} next_delay={delay}s "
                        f"body_snippet={snippet}",
                        fname="agent.error.log",
                    )
                    last_failure_transient = True
                    time.sleep(delay)
                    continue

                resp.raise_for_status()
                data = resp.json()
                access_token = data.get("access_token")
                expires_in = int(data.get("expires_in") or 900)
                if not access_token:
                    raise RuntimeError("Token refresh response missing access_token")
                self.access_token = access_token.strip()
                expiry = int(time.time()) + max(expires_in - 5, 0)
                self.access_expires_at = expiry
                self.key_store.save_access_token(self.access_token, expires_at=expiry)
                self.key_store.set_access_binding(SSL_KEY_FINGERPRINT)
                self.session.headers.update({"Authorization": f"Bearer {self.access_token}"})
                if attempt > 1:
                    _log_agent(
                        "Refresh token request recovered "
                        f"attempt={attempt}/{total_attempts} status={resp.status_code}",
                        fname="agent.log",
                    )
                return
            except requests.HTTPError as exc:
                last_error = exc
                status_code = None
                error_code = None
                snippet = "<unavailable>"
                if resp is not None:
                    status_code = resp.status_code
                    error_code, snippet = self._error_details(resp)
                transient = status_code in _TRANSIENT_REFRESH_STATUS_CODES
                last_failure_transient = transient
                if transient and attempt < total_attempts:
                    delay = _REFRESH_RETRY_DELAYS_SECONDS[attempt - 1]
                    _log_agent(
                        "Refresh token HTTP error; retrying "
                        f"attempt={attempt}/{total_attempts} status={status_code} "
                        f"error={error_code or '<unknown>'} next_delay={delay}s "
                        f"body_snippet={snippet}",
                        fname="agent.error.log",
                    )
                    time.sleep(delay)
                    continue
                break
            except requests.RequestException as exc:
                last_error = exc
                last_failure_transient = True
                if attempt < total_attempts:
                    delay = _REFRESH_RETRY_DELAYS_SECONDS[attempt - 1]
                    _log_agent(
                        "Refresh token transport error; retrying "
                        f"attempt={attempt}/{total_attempts} next_delay={delay}s error={exc}",
                        fname="agent.error.log",
                    )
                    time.sleep(delay)
                    continue
                break

        if last_failure_transient and self._access_token_still_usable():
            remaining = max(0, int((self.access_expires_at or 0) - time.time()))
            _log_agent(
                "Refresh token temporarily unavailable; continuing with current access token "
                f"remaining_seconds={remaining}",
                fname="agent.log",
            )
            return
        if last_error is not None:
            raise last_error
        raise RuntimeError("Token refresh failed without a recoverable response")

    def clear_tokens(self) -> None:
        with self._auth_lock:
            self._clear_tokens_locked()

    def _clear_tokens_locked(self) -> None:
        self.key_store.clear_tokens()
        self.access_token = None
        self.refresh_token = None
        self.access_expires_at = None
        self.guid = self.key_store.load_guid()
        self.session.headers.pop("Authorization", None)

    def _error_details(self, response: requests.Response) -> Tuple[Optional[str], str]:
        error_code: Optional[str] = None
        snippet = ""
        try:
            snippet = response.text[:256]
        except Exception:
            snippet = "<unavailable>"
        try:
            data = response.json()
        except Exception:
            data = None
        if isinstance(data, dict):
            for key in ("error", "code", "status"):
                value = data.get(key)
                if isinstance(value, str) and value.strip():
                    error_code = value.strip()
                    break
        return error_code, snippet

    def _access_token_still_usable(
        self,
        *,
        minimum_ttl: int = _REFRESH_FALLBACK_MINIMUM_TTL_SECONDS,
    ) -> bool:
        if not self.access_token or not self.access_expires_at:
            return False
        return (self.access_expires_at - time.time()) > max(0, int(minimum_ttl))

    def _should_retry_auth(self, status_code: int, error_code: Optional[str]) -> bool:
        if status_code == 401:
            return True
        retryable_forbidden = {"fingerprint_mismatch"}
        if status_code == 403 and error_code in retryable_forbidden:
            return True
        return False

    def _resolve_installer_code(self) -> str:
        if INSTALLER_CODE_OVERRIDE:
            code = INSTALLER_CODE_OVERRIDE.strip()
            if code:
                try:
                    self.key_store.cache_installer_code(code, consumer=SERVICE_MODE_CANONICAL)
                except Exception:
                    pass
            return code
        code = ""
        try:
            code = (
                CONFIG.data.get("enrollment_code")
                or CONFIG.data.get("installer_code")
                or ""
            ).strip()
        except Exception:
            code = ""
        if code:
            try:
                self.key_store.cache_installer_code(code, consumer=SERVICE_MODE_CANONICAL)
            except Exception:
                pass
            return code
        try:
            cached = self.key_store.load_cached_installer_code()
        except Exception:
            cached = None
        if cached:
            try:
                self.key_store.cache_installer_code(cached, consumer=SERVICE_MODE_CANONICAL)
            except Exception:
                pass
            return cached
        fallback = _fallback_installer_code(CONFIG.path)
        if fallback:
            try:
                CONFIG.data["enrollment_code"] = fallback
                CONFIG.data["installer_code"] = fallback
                CONFIG._write()
                _log_agent(
                    "Adopted enrollment code from fallback configuration", fname="agent.log"
                )
            except Exception:
                pass
            try:
                self.key_store.cache_installer_code(fallback, consumer=SERVICE_MODE_CANONICAL)
            except Exception:
                pass
            return fallback
        return ""

    def _consume_installer_code(self) -> None:
        # Avoid clearing explicit CLI/env overrides; only mutate persisted config.
        self._active_installer_code = None
        if INSTALLER_CODE_OVERRIDE:
            return
        try:
            changed = False
            if CONFIG.data.get("enrollment_code"):
                CONFIG.data["enrollment_code"] = ""
                changed = True
            if CONFIG.data.get("installer_code"):
                CONFIG.data["installer_code"] = ""
                changed = True
            if changed:
                CONFIG._write()
                _log_agent("Cleared persisted installer code after successful enrollment", fname="agent.log")
        except Exception as exc:
            _log_agent(f"Failed to clear installer code after enrollment: {exc}", fname="agent.error.log")
        try:
            self.key_store.mark_installer_code_consumed(SERVICE_MODE_CANONICAL)
        except Exception as exc:
            _log_agent(
                f"Failed to update shared installer code cache: {exc}",
                fname="agent.error.log",
            )

    def _discard_rejected_installer_code(self, code: str, *, reason: str) -> None:
        rejected = (code or "").strip()
        if not rejected:
            return
        self._active_installer_code = None
        if INSTALLER_CODE_OVERRIDE and INSTALLER_CODE_OVERRIDE.strip() == rejected:
            _log_agent(
                "Rejected enrollment code came from explicit override; persisted configs not cleared "
                f"reason={reason}",
                fname="agent.error.log",
            )
            return
        cleared_paths: List[str] = []
        candidate_paths: List[str] = []

        def _add(path: str) -> None:
            if not path:
                return
            try:
                normalized = os.path.abspath(path)
            except Exception:
                return
            if normalized not in candidate_paths:
                candidate_paths.append(normalized)

        try:
            _add(CONFIG.path)
            settings_dir = os.path.dirname(CONFIG.path)
            for name in (
                "agent_settings.json",
                "agent_settings_SYSTEM.json",
                "agent_settings_CURRENTUSER.json",
                "agent_settings_svc.json",
                "agent_settings_user.json",
            ):
                _add(os.path.join(settings_dir, name))
        except Exception:
            pass
        try:
            project_root = _find_project_root()
            for base in (
                os.path.join(project_root, "Agent", "Borealis", "Settings"),
                os.path.join(project_root, "Agent", "Settings"),
                project_root,
            ):
                for name in (
                    "agent_settings.json",
                    "agent_settings_SYSTEM.json",
                    "agent_settings_CURRENTUSER.json",
                    "agent_settings_svc.json",
                    "agent_settings_user.json",
                ):
                    _add(os.path.join(base, name))
        except Exception:
            pass

        try:
            changed = False
            for key in ("enrollment_code", "installer_code"):
                value = CONFIG.data.get(key)
                if isinstance(value, str) and value.strip() == rejected:
                    CONFIG.data[key] = ""
                    changed = True
            if changed:
                CONFIG._write()
                cleared_paths.append(os.path.abspath(CONFIG.path))
        except Exception as exc:
            _log_agent(
                f"Failed to clear rejected enrollment code from active config: {exc}",
                fname="agent.error.log",
            )

        for path in candidate_paths:
            try:
                if _clear_installer_code_from_file(path, rejected):
                    cleared_paths.append(path)
            except Exception:
                pass
        try:
            discard = getattr(self.key_store, "discard_installer_code", None)
            if callable(discard):
                discard(rejected)
        except Exception as exc:
            _log_agent(
                f"Failed to discard rejected shared installer code: {exc}",
                fname="agent.error.log",
            )
        try:
            unique_paths = sorted({path for path in cleared_paths if path})
            _log_agent(
                "Cleared rejected enrollment code "
                f"reason={reason} files={len(unique_paths)}",
                fname="agent.log",
            )
        except Exception:
            pass

    # ------------------------------------------------------------------
    # HTTP helpers
    # ------------------------------------------------------------------
    def post_json(self, path: str, payload: Optional[Dict[str, Any]] = None, *, require_auth: bool = True) -> Any:
        attempt = 0
        max_attempts = 3
        while True:
            if require_auth:
                self.ensure_authenticated()
            url = f"{self.base_url}{path}"
            headers = self.auth_headers()
            response = self.session.post(url, json=payload, headers=headers, timeout=30)
            if require_auth and response.status_code in (401, 403):
                error_code, snippet = self._error_details(response)
                if response.status_code == 403 and error_code == 'guid_mismatch' and attempt < max_attempts:
                    attempt += 1
                    self._reload_tokens_from_disk()
                    if self.guid:
                        try:
                            _update_agent_id_for_guid(self.guid)
                        except Exception:
                            pass
                    continue
                if self._should_retry_auth(response.status_code, error_code) and attempt < max_attempts:
                    self.clear_tokens()
                    attempt += 1
                    continue
                _log_agent(
                    "Authenticated request rejected "
                    f"path={path} status={response.status_code} error={error_code or '<unknown>'}"
                    f" body_snippet={snippet}",
                    fname="agent.error.log",
                )
                _record_tray_error(
                    "auth",
                    f"Authenticated request rejected for {path}: {error_code or response.status_code}",
                    client=self,
                    socket_connected=False,
                )
            response.raise_for_status()
            if response.headers.get("Content-Type", "").lower().startswith("application/json"):
                return response.json()
            return response.text

    async def async_post_json(
        self,
        path: str,
        payload: Optional[Dict[str, Any]] = None,
        *,
        require_auth: bool = True,
    ) -> Any:
        loop = asyncio.get_running_loop()
        task = partial(self.post_json, path, payload, require_auth=require_auth)
        return await loop.run_in_executor(None, task)

    def websocket_base_url(self) -> str:
        self.refresh_base_url()
        if not self.base_url:
            raise RuntimeError("Borealis public server URL is not configured.")
        return self.base_url

    def store_server_signing_key(self, value: str) -> None:
        try:
            self.key_store.save_server_signing_key(value)
        except Exception as exc:
            _log_agent(f"Unable to store server signing key: {exc}", fname="agent.error.log")

    def load_server_signing_key(self) -> Optional[str]:
        try:
            return self.key_store.load_server_signing_key()
        except Exception:
            return None


HTTP_CLIENT: Optional[AgentHttpClient] = None


def http_client() -> AgentHttpClient:
    global HTTP_CLIENT
    if HTTP_CLIENT is None:
        HTTP_CLIENT = AgentHttpClient()
    return HTTP_CLIENT

def _get_context_label() -> str:
    return 'SYSTEM' if SYSTEM_SERVICE_MODE else 'CURRENTUSER'


def _normalize_agent_guid(guid: str) -> str:
    try:
        if not guid:
            return ''
        value = str(guid).strip().replace('\ufeff', '')
        if not value:
            return ''
        value = value.strip('{}')
        try:
            return str(uuid.UUID(value)).upper()
        except Exception:
            cleaned = ''.join(ch for ch in value if ch in string.hexdigits or ch == '-')
            cleaned = cleaned.strip('-')
            if cleaned:
                try:
                    return str(uuid.UUID(cleaned)).upper()
                except Exception:
                    pass
            return value.upper()
    except Exception:
        return ''


def _read_agent_guid_from_disk() -> str:
    try:
        path = _agent_guid_path()
        candidates = [path]
        try:
            root = _find_project_root()
            legacy_candidates = [
                os.path.join(root, 'Agent', 'Borealis', 'agent_GUID'),
                os.path.join(root, 'Agent', 'Settings', 'agent_GUID'),
            ]
            for candidate in legacy_candidates:
                if candidate not in candidates:
                    candidates.append(candidate)
        except Exception:
            pass
        for candidate in candidates:
            if os.path.isfile(candidate):
                try:
                    with open(candidate, 'r', encoding='utf-8') as fh:
                        value = fh.read()
                except Exception:
                    value = ''
                guid = _normalize_agent_guid(value)
                if guid:
                    return guid
    except Exception:
        pass
    return ''


def _ensure_agent_guid() -> str:
    with _acquire_guid_lock():
        guid = _read_agent_guid_from_disk()
        if not guid:
            try:
                ks_guid = _key_store().load_guid()
            except Exception:
                ks_guid = None
            guid = _normalize_agent_guid(ks_guid or '')
        if not guid:
            guid = str(uuid.uuid4()).upper()
        _persist_agent_guid_local_internal(guid, assume_locked=True)
        try:
            _update_agent_id_for_guid(guid)
        except Exception:
            pass
        return guid


def _compose_agent_id(hostname: str, guid: str, context: str) -> str:
    host = (hostname or '').strip()
    if not host:
        host = 'UNKNOWN-HOST'
    host = host.replace(' ', '-').upper()
    normalized_guid = _normalize_agent_guid(guid) or guid or 'UNKNOWN-GUID'
    context_label = (context or '').strip().upper() or _get_context_label()
    return f"{host}_{normalized_guid}_{context_label}"


def _update_agent_id_for_guid(guid: str):
    normalized_guid = _normalize_agent_guid(guid)
    if not normalized_guid:
        return
    desired = _compose_agent_id(socket.gethostname(), normalized_guid, _get_context_label())
    existing = (CONFIG.data.get('agent_id') or '').strip()
    if existing != desired:
        CONFIG.data['agent_id'] = desired
        CONFIG._write()
    global AGENT_ID
    AGENT_ID = CONFIG.data['agent_id']


def init_agent_id():
    guid = _ensure_agent_guid()
    _update_agent_id_for_guid(guid)
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
                installation_type = _get("InstallationType", "")  # e.g., "Server"
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

                wmi_info = {}
                try:
                    cmd = "Get-CimInstance Win32_OperatingSystem | Select-Object Caption,ProductType,BuildNumber | ConvertTo-Json -Compress"
                    out = subprocess.run(
                        ["powershell", "-NoProfile", "-Command", cmd],
                        capture_output=True,
                        text=True,
                        timeout=5,
                    )
                    raw = (out.stdout or "").strip()
                    if raw:
                        data = json.loads(raw)
                        if isinstance(data, list):
                            data = data[0] if data else {}
                        if isinstance(data, dict):
                            wmi_info = data
                except Exception:
                    wmi_info = {}

                wmi_caption = ""
                caption_val = wmi_info.get("Caption")
                if isinstance(caption_val, str):
                    wmi_caption = caption_val.strip()
                    if wmi_caption.lower().startswith("microsoft "):
                        wmi_caption = wmi_caption[10:].strip()

                def _parse_int(value) -> int:
                    try:
                        return int(str(value).split(".")[0])
                    except Exception:
                        return 0

                if not build_int:
                    for candidate in (wmi_info.get("BuildNumber"), None):
                        if candidate:
                            parsed = _parse_int(candidate)
                            if parsed:
                                build_int = parsed
                                break
                    if not build_int:
                        try:
                            build_int = _parse_int(sys.getwindowsversion().build)  # type: ignore[attr-defined]
                        except Exception:
                            build_int = 0

                product_type_val = wmi_info.get("ProductType")
                if isinstance(product_type_val, str):
                    try:
                        product_type_val = int(product_type_val.strip())
                    except Exception:
                        product_type_val = None
                if not isinstance(product_type_val, int):
                    try:
                        product_type_val = getattr(sys.getwindowsversion(), 'product_type', None)  # type: ignore[attr-defined]
                    except Exception:
                        product_type_val = None
                if not isinstance(product_type_val, int):
                    product_type_val = 0

                def _contains_server(text) -> bool:
                    try:
                        return isinstance(text, str) and 'server' in text.lower()
                    except Exception:
                        return False

                server_hints = []
                if isinstance(product_type_val, int) and product_type_val not in (0, 1):
                    server_hints.append("product_type")
                if isinstance(product_type_val, int) and product_type_val == 1 and _contains_server(product_name):
                    server_hints.append("product_type_mismatch")
                for hint in (product_name, wmi_caption, edition_id, installation_type):
                    if _contains_server(hint):
                        server_hints.append("string_hint")
                        break
                if installation_type and str(installation_type).strip().lower() == 'server':
                    server_hints.append("installation_type")
                if isinstance(edition_id, str) and edition_id.lower().startswith('server'):
                    server_hints.append("edition_id")
                if build_int in (20348, 26100, 17763) and _contains_server(product_name or wmi_caption or edition_id or installation_type):
                    server_hints.append("build_hint")

                is_server = bool(server_hints)

                if is_server:
                    if build_int >= 26100:
                        family = "Windows Server 2025"
                    elif build_int >= 20348:
                        family = "Windows Server 2022"
                    elif build_int >= 17763:
                        family = "Windows Server 2019"
                    else:
                        family = "Windows Server"
                else:
                    if build_int >= 22000:
                        family = "Windows 11"
                    elif build_int >= 10240:
                        family = "Windows 10"
                    else:
                        family = platform.release() or "Windows"

                # Derive friendly edition name, prefer parsing from ProductName
                edition = ""
                pn = product_name or ""
                if pn.lower().startswith("windows "):
                    tokens = pn.split()
                    # tokens like ["Windows", "Server", "2022", "Standard", ...] or ["Windows", "11", "Pro", ...]
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

                version_label = display_version or release_id or ""

                if isinstance(ubr, int):
                    build_str = f"{build_number}.{ubr}" if build_number else str(ubr)
                else:
                    try:
                        build_str = f"{build_number}.{int(ubr)}" if build_number and ubr is not None else build_number
                    except Exception:
                        build_str = build_number

                parts = ["Microsoft", family]
                if edition:
                    parts.append(edition)
                if version_label:
                    parts.append(version_label)
                if build_str:
                    parts.append(f"Build {build_str}")

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


def _system_uptime_seconds() -> Optional[int]:
    try:
        if psutil and hasattr(psutil, "boot_time"):
            return int(time.time() - psutil.boot_time())
    except Exception:
        pass
    return None


def _collect_heartbeat_metrics() -> Dict[str, Any]:
    metrics: Dict[str, Any] = {
        "service_mode": SERVICE_MODE,
    }
    uptime = _system_uptime_seconds()
    if uptime is not None:
        metrics["uptime"] = uptime
    try:
        metrics["hostname"] = socket.gethostname()
    except Exception:
        pass
    try:
        metrics["username"] = getpass.getuser()
    except Exception:
        pass
    if psutil:
        try:
            cpu = psutil.cpu_percent(interval=None)
            metrics["cpu_percent"] = cpu
        except Exception:
            pass
        try:
            mem = psutil.virtual_memory()
            metrics["memory_percent"] = getattr(mem, "percent", None)
        except Exception:
            pass
    return metrics


IDENTITY = _key_store().load_or_create_identity()
SSL_KEY_FINGERPRINT = IDENTITY.fingerprint
PUBLIC_KEY_B64 = IDENTITY.public_key_b64
if SYSTEM_SERVICE_MODE:
    try:
        _heartbeat_complete("identity_loaded", f"Identity fingerprint {SSL_KEY_FINGERPRINT[:16]}.")
    except Exception:
        pass


def get_server_url() -> str:
    """Return the Borealis public HTTPS FQDN from env or Agent/Borealis/Settings/server_url.txt."""

    def _sanitize(val: str) -> str:
        try:
            s = (val or '').strip().replace('\ufeff', '')
            if not s:
                return ''
            if '://' not in s:
                s = 'https://' + s
            parsed = urlparse(s)
            scheme = (parsed.scheme or '').lower()
            hostname = (parsed.hostname or '').strip().lower()
            if scheme != 'https' or not hostname:
                return ''
            if hostname == 'localhost' or _is_literal_ip(hostname):
                return ''
            netloc = hostname
            if parsed.port:
                netloc = f'{netloc}:{parsed.port}'
            normalized = parsed._replace(
                scheme='https',
                netloc=netloc,
                params='',
                query=parsed.query,
                fragment='',
            )
            return urlunparse(normalized).rstrip('/')
        except Exception:
            return ''

    try:
        env_url = os.environ.get('BOREALIS_SERVER_URL')
        if env_url and _sanitize(env_url):
            return _sanitize(env_url)
        path = os.path.join(_settings_dir(), 'server_url.txt')
        if os.path.isfile(path):
            try:
                with open(path, 'r', encoding='utf-8-sig') as f:
                    txt = f.read()
                s = _sanitize(txt)
                if s:
                    return s
            except Exception:
                pass
    except Exception:
        pass
    return ''

# //////////////////////////////////////////////////////////////////////////
# CORE SECTION: ASYNC TASK / WEBSOCKET
# //////////////////////////////////////////////////////////////////////////

_SOCKETIO_RETRY_SECONDS = 5
_SOCKETIO_RECONNECT_PAUSE_SECONDS = 1

# We supervise reconnects ourselves so every reconnect attempt re-authenticates,
# refreshes TLS/socket transport settings, and re-emits `connect_agent`.
#
# Create the real Socket.IO client only after the runtime event loop exists.
# Python 3.9 binds asyncio Queue/Future internals to the loop that first touches
# them, so constructing the Engine.IO client during module import can strand
# wait queues on the wrong loop under Linux service startup.
class _SocketIORegistrationProxy:
    def __init__(self) -> None:
        self._registrations: List[Tuple[str, Optional[str], Callable[..., Any]]] = []
        self.connected = False

    def event(self, handler: Callable[..., Any]) -> Callable[..., Any]:
        self._registrations.append(("event", None, handler))
        return handler

    def on(self, event_name: str):
        def _decorator(handler: Callable[..., Any]) -> Callable[..., Any]:
            self._registrations.append(("on", str(event_name), handler))
            return handler

        return _decorator

    def apply_to(self, client: "socketio.AsyncClient") -> None:
        for kind, event_name, handler in list(self._registrations):
            if kind == "event":
                client.event(handler)
            else:
                client.on(str(event_name), handler=handler)


_SOCKETIO_REGISTRATIONS = _SocketIORegistrationProxy()
sio = _SOCKETIO_REGISTRATIONS
role_tasks = {}
background_tasks = []
AGENT_LOOP = None
ROLE_MANAGER = None
ROLE_MANAGER_SYS = None
ROLE_HEALTH_REGISTRY = RoleHealthRegistry()
SESSION_HELPER_BROKER = None
HELPER_CLIENT = None


def _create_socketio_client(loop_ref=None) -> "socketio.AsyncClient":
    client = socketio.AsyncClient(reconnection=False)
    _SOCKETIO_REGISTRATIONS.apply_to(client)
    try:
        setattr(client, "_borealis_loop_id", id(loop_ref) if loop_ref is not None else None)
    except Exception:
        pass
    return client


def _ensure_socketio_client(loop_ref=None):
    global sio
    if sio is None or isinstance(sio, _SocketIORegistrationProxy):
        sio = _create_socketio_client(loop_ref)
    return sio

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
    try:
        if ROLE_MANAGER_SYS is not None:
            ROLE_MANAGER_SYS.stop_all()
    except Exception:
        pass


def _collect_agent_role_health() -> Dict[str, Any]:
    try:
        snapshot = ROLE_HEALTH_REGISTRY.snapshot()
        if isinstance(snapshot, dict):
            return snapshot
    except Exception as exc:
        _log_agent(f'Role health snapshot failed: {exc}', fname='agent.error.log')
    return {"roles": [], "reported_at": int(time.time())}


def _emit_quick_job_result_local(payload: Dict[str, Any]) -> None:
    async def _emit():
        try:
            active_sio = _ensure_socketio_client(AGENT_LOOP)
            await active_sio.emit('quick_job_result', payload)
        except Exception as exc:
            _log_agent(f'Failed to emit local quick_job_result: {exc}', fname='agent.error.log')

    try:
        if AGENT_LOOP is not None:
            asyncio.run_coroutine_threadsafe(_emit(), AGENT_LOOP)
    except Exception as exc:
        _log_agent(f'Failed to schedule local quick_job_result emit: {exc}', fname='agent.error.log')


async def watch_session_helper_reconcile():
    await asyncio.sleep(2)
    while True:
        try:
            broker = SESSION_HELPER_BROKER
            if broker is not None:
                broker.reconcile_sessions()
        except Exception as exc:
            _log_agent(f'Session helper reconcile failed: {exc}', fname='agent.error.log')
        await asyncio.sleep(10)


# ---------------- Heartbeat ----------------
async def send_heartbeat():
    """
    Periodically send agent heartbeat to the server so the Devices page can
    show hostname, OS, and last_seen.
    """
    await asyncio.sleep(15)
    client = http_client()
    while True:
        try:
            client.ensure_authenticated()
            payload = {
                "guid": client.guid or _read_agent_guid_from_disk(),
                "hostname": socket.gethostname(),
                "metrics": _collect_heartbeat_metrics(),
                "agent_build_id": _current_agent_build_id(sync=True),
                "agent_update_status": _current_agent_update_status(),
                "agent_role_health": _collect_agent_role_health(),
                "service_mode": SERVICE_MODE,
            }
            response = await client.async_post_json("/api/agent/heartbeat", payload, require_auth=True)
            if SYSTEM_SERVICE_MODE:
                try:
                    _heartbeat_complete("steady_state_online", "Heartbeat accepted by Engine.")
                    _heartbeat_flush(reason="heartbeat")
                except Exception:
                    pass
            tray_updates = {
                "last_heartbeat_success_at": int(time.time()),
                "last_error_kind": "",
                "last_error_message": "",
            }
            if isinstance(response, dict):
                tray_updates["site_name"] = str(response.get("site_name") or "").strip()
                site_id = response.get("site_id")
                try:
                    tray_updates["site_id"] = int(site_id) if site_id is not None else None
                except Exception:
                    tray_updates["site_id"] = None
            _sync_tray_status(tray_updates, client=client)
        except Exception as exc:
            _log_agent(f'Heartbeat post failed: {exc}', fname='agent.error.log')
            _record_tray_error("transport", _describe_exception(exc), client=client, socket_connected=False)
            if SYSTEM_SERVICE_MODE:
                try:
                    _heartbeat_fail("steady_state_online", _describe_exception(exc))
                except Exception:
                    pass
        await asyncio.sleep(60)


def _refresh_startup_telemetry_once(reason: str = "periodic_agent_health") -> bool:
    if not SYSTEM_SERVICE_MODE:
        return False
    try:
        _heartbeat_complete("steady_state_online", "Periodic agent health refresh accepted.")
        return bool(_heartbeat_flush(reason=reason))
    except Exception as exc:
        _log_agent(f'Periodic agent startup status refresh failed: {exc}', fname='agent.error.log')
        return False


async def refresh_startup_telemetry():
    await asyncio.sleep(_AGENT_HEALTH_REFRESH_INITIAL_DELAY_SECONDS)
    while True:
        _refresh_startup_telemetry_once(reason="periodic_agent_health")
        await asyncio.sleep(_AGENT_HEALTH_REFRESH_SECONDS)


async def poll_script_requests():
    await asyncio.sleep(20)
    client = http_client()
    while True:
        try:
            client.ensure_authenticated()
            payload = {"guid": client.guid or _read_agent_guid_from_disk()}
            response = await client.async_post_json("/api/agent/script/request", payload, require_auth=True)
            if isinstance(response, dict):
                signing_key = response.get("signing_key")
                script_b64 = response.get("script")
                signature_b64 = response.get("signature")
                sig_alg = (response.get("sig_alg") or "").lower()
                if script_b64 and signature_b64:
                    script_bytes = _decode_script_bytes(script_b64, "base64")
                    if script_bytes is None:
                        _log_agent('received script payload with invalid encoding', fname='agent.error.log')
                    elif sig_alg and sig_alg not in ("ed25519", "eddsa"):
                        _log_agent(f'unsupported script signature algorithm: {sig_alg}', fname='agent.error.log')
                    else:
                        existing_key = client.load_server_signing_key()
                        key_available = bool(
                            (isinstance(signing_key, str) and signing_key.strip())
                            or (isinstance(existing_key, str) and existing_key.strip())
                        )
                        if not key_available:
                            _log_agent('no server signing key available to verify script payload', fname='agent.error.log')
                        elif _verify_and_store_script_signature(client, script_bytes, signature_b64, signing_key):
                            _log_agent('received signed script payload (verification succeeded); awaiting executor integration')
                        else:
                            _log_agent('rejected script payload due to invalid signature', fname='agent.error.log')
                elif signing_key:
                    # No script content, but we may need to persist updated signing key.
                    try:
                        client.store_server_signing_key(signing_key)
                    except Exception as exc:
                        _log_agent(f'failed to persist server signing key: {exc}', fname='agent.error.log')
        except Exception as exc:
            _log_agent(f'script request poll failed: {exc}', fname='agent.error.log')
        await asyncio.sleep(30)

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
            payload = {
                "agent_id": AGENT_ID,
                "hostname": details.get("summary", {}).get("hostname", socket.gethostname()),
                "details": details,
                "agent_build_id": _current_agent_build_id(sync=True),
                "agent_update_status": _current_agent_update_status(),
                "agent_role_health": _collect_agent_role_health(),
                "service_mode": SERVICE_MODE,
            }
            client = http_client()
            await client.async_post_json("/api/agent/details", payload, require_auth=True)
            _log_agent('Posted agent details to server.')
        except Exception as e:
            print(f"[WARN] Failed to send agent details: {e}")
            _log_agent(f'Failed to send agent details: {e}', fname='agent.error.log')
        await asyncio.sleep(300)

async def send_agent_details_once():
    try:
        details = {
            "summary": collect_summary(),
            "software": collect_software(),
            "memory": collect_memory(),
            "storage": collect_storage(),
            "network": collect_network(),
        }
        payload = {
            "agent_id": AGENT_ID,
            "hostname": details.get("summary", {}).get("hostname", socket.gethostname()),
            "details": details,
            "agent_build_id": _current_agent_build_id(sync=True),
            "agent_update_status": _current_agent_update_status(),
            "agent_role_health": _collect_agent_role_health(),
            "service_mode": SERVICE_MODE,
        }
        client = http_client()
        await client.async_post_json("/api/agent/details", payload, require_auth=True)
        _log_agent('Posted agent details (once) to server.')
    except Exception as e:
        _log_agent(f'Failed to post agent details once: {e}', fname='agent.error.log')


def _request_system_wireguard_ensure(reason: str) -> None:
    if not SYSTEM_SERVICE_MODE:
        return
    manager = ROLE_MANAGER_SYS
    if manager is None:
        return
    try:
        role = manager.roles.get("wireguard")
    except Exception:
        return
    requester = getattr(role, "request_immediate_ensure", None)
    if not callable(requester):
        return
    try:
        requester(reason=reason)
    except Exception as exc:
        _log_agent(f'WireGuard immediate ensure request failed: {exc}', fname='agent.error.log')

@sio.event
async def connect():
    print(f"[INFO] Successfully Connected to Borealis Server!")
    _log_agent('Connected to server.')
    _clear_tray_error_if_matches("tls", "transport", client=http_client())
    _sync_tray_status({"socket_connected": True}, client=http_client())
    if SYSTEM_SERVICE_MODE:
        try:
            _heartbeat_complete("socket_connected", "Socket.IO connection established.")
            _heartbeat_flush(reason="socket_connected")
        except Exception:
            pass
    try:
        sid = getattr(sio, 'sid', None)
        transport = getattr(sio, 'transport', None)
        _log_agent(
            f'WebSocket handshake established sid={sid!r} transport={transport!r}',
            fname='agent.log',
        )
    except Exception:
        pass
    connect_payload = {"agent_id": AGENT_ID, "service_mode": SERVICE_MODE}
    try:
        if socket.gethostname():
            connect_payload["hostname"] = socket.gethostname()
    except Exception:
        pass
    try:
        if SYSTEM_SERVICE_MODE and SESSION_HELPER_BROKER is not None and SESSION_HELPER_BROKER.supports_currentuser_dispatch():
            connect_payload["helper_contexts"] = ["currentuser"]
    except Exception:
        pass
    await sio.emit('connect_agent', connect_payload)
    _request_system_wireguard_ensure("socket_connect")

    # Send an immediate heartbeat via authenticated REST call.
    try:
        client = http_client()
        client.ensure_authenticated()
        payload = {
            "guid": client.guid or _read_agent_guid_from_disk(),
            "hostname": socket.gethostname(),
            "inventory": {},
            "metrics": _collect_heartbeat_metrics(),
            "agent_build_id": _current_agent_build_id(sync=True),
            "agent_update_status": _current_agent_update_status(),
            "agent_role_health": _collect_agent_role_health(),
            "service_mode": SERVICE_MODE,
        }
        response = await client.async_post_json("/api/agent/heartbeat", payload, require_auth=True)
        tray_updates = {
            "last_heartbeat_success_at": int(time.time()),
            "last_error_kind": "",
            "last_error_message": "",
        }
        if isinstance(response, dict):
            tray_updates["site_name"] = str(response.get("site_name") or "").strip()
            site_id = response.get("site_id")
            try:
                tray_updates["site_id"] = int(site_id) if site_id is not None else None
            except Exception:
                tray_updates["site_id"] = None
        _sync_tray_status(tray_updates, client=client)
    except Exception as exc:
        _log_agent(f'Initial REST heartbeat failed: {exc}', fname='agent.error.log')
        _record_tray_error("transport", _describe_exception(exc), client=client, socket_connected=True)

    await sio.emit('request_config', {"agent_id": AGENT_ID})
    # Inventory details posting is managed by the DeviceAudit role (SYSTEM). No one-shot post here.
    # Fire-and-forget service account check-in so server can provision WinRM credentials
    try:
        async def _svc_checkin_once():
            try:
                payload = {"agent_id": AGENT_ID, "hostname": socket.gethostname(), "username": ".\\svcBorealis"}
                client = http_client()
                response = await client.async_post_json("/api/agent/checkin", payload, require_auth=True)
                if isinstance(response, dict):
                    guid_value = (response.get('agent_guid') or '').strip()
                    if guid_value:
                        _persist_agent_guid_local(guid_value)
                        _update_agent_id_for_guid(guid_value)
            except Exception:
                pass
        asyncio.create_task(_svc_checkin_once())
    except Exception:
        pass

@sio.event
async def connect_error(data):
    try:
        setattr(sio, "connection_error", data)
    except Exception:
        pass
    try:
        _log_agent(f'Socket connect_error event: {data!r}', fname='agent.error.log')
    except Exception:
        pass
    _sync_tray_status({"socket_connected": False}, client=http_client())

@sio.event
async def disconnect():
    print("[WebSocket] Disconnected from Borealis server.")
    # Do not tear down roles/tunnels on control-plane drop; idle/grace timers inside roles handle cleanup.
    _log_agent('SocketIO disconnect observed; leaving roles/tunnels running to survive transient drops.', fname='agent.log')
    _sync_tray_status({"socket_connected": False}, client=http_client())
    CONFIG.data['regions'].clear()
    CONFIG._write()


async def watch_tray_restart_requests():
    await asyncio.sleep(2)
    while True:
        try:
            if _process_tray_restart_request_now():
                return
        except Exception as exc:
            _log_agent(f"Tray restart watcher failed: {exc}", fname="agent.error.log")
        await asyncio.sleep(2)


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
            if role_cfg.get('role') in {'screenshot', 'node_screenshot'}:
                interval_ms = int(role_cfg.get('interval', 1000))
                send_service_control({'type': 'screenshot_config', 'interval_ms': interval_ms})
                break
    except Exception:
        pass

    try:
        if ROLE_MANAGER is not None:
            ROLE_MANAGER.on_config(roles)
    except Exception as e:
        print(f"[WARN] role manager (interactive) apply config failed: {e}")
    try:
        if ROLE_MANAGER_SYS is not None:
            ROLE_MANAGER_SYS.on_config(roles)
    except Exception as e:
        print(f"[WARN] role manager (system) apply config failed: {e}")

## Script execution and list windows handlers are registered by roles


@sio.on('agent_update_request')
async def on_agent_update_request(payload):
    try:
        if not isinstance(payload, dict):
            return
        if str(SERVICE_MODE_CANONICAL or "").upper() != "SYSTEM":
            return
        target_agent = str(payload.get("agent_id") or "").strip()
        if target_agent and target_agent != AGENT_ID:
            return
        target_hostname = str(payload.get("hostname") or "").strip().lower()
        if target_hostname and target_hostname != socket.gethostname().strip().lower():
            return

        requested_by = str(payload.get("requested_by") or "").strip()
        requested_at = payload.get("requested_at")
        launched, status = _launch_manual_agent_update(
            requested_by=requested_by,
            requested_at=requested_at,
        )
        if launched:
            return
        _log_manual_update_request_failure(
            requested_by=requested_by,
            requested_at=requested_at,
            detail=status,
        )
    except Exception as exc:
        _log_agent(f"Manual agent update request handler failed: {exc}", fname='agent.error.log')

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

# ---------------- Quick Job Helpers (System/Local) ----------------
def _project_root_for_temp():
    try:
        return os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..'))
    except Exception:
        return os.path.abspath(os.path.dirname(__file__))

def _run_powershell_script_content_local(content: str):
    try:
        temp_dir = os.path.join(_project_root_for_temp(), "Temp")
        os.makedirs(temp_dir, exist_ok=True)
        import tempfile as _tf
        fd, path = _tf.mkstemp(prefix="sj_", suffix=".ps1", dir=temp_dir, text=True)
        with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as fh:
            fh.write(content or "")
        ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
        if not os.path.isfile(ps):
            ps = "powershell.exe"
        flags = 0x08000000 if os.name == 'nt' else 0
        proc = subprocess.run([ps, "-ExecutionPolicy", "Bypass", "-NoProfile", "-File", path], capture_output=True, text=True, timeout=60*60, creationflags=flags)
        return proc.returncode, proc.stdout or "", proc.stderr or ""
    except Exception as e:
        return -1, "", str(e)
    finally:
        try:
            if 'path' in locals() and os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass

def _run_powershell_via_system_task(content: str):
    ps_exe = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
    if not os.path.isfile(ps_exe):
        ps_exe = 'powershell.exe'
    script_path = None
    out_path = None
    try:
        temp_root = os.path.join(_project_root_for_temp(), 'Temp')
        os.makedirs(temp_root, exist_ok=True)
        import tempfile as _tf
        fd, script_path = _tf.mkstemp(prefix='sys_task_', suffix='.ps1', dir=temp_root, text=True)
        with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as f:
            f.write(content or '')
        try:
            log_dir = os.path.join(_project_root_for_temp(), 'Agent', 'Logs')
            os.makedirs(log_dir, exist_ok=True)
            debug_copy = os.path.join(log_dir, 'system_last.ps1')
            with open(debug_copy, 'w', encoding='utf-8', newline='\n') as df:
                df.write(content or '')
        except Exception:
            pass
        out_path = os.path.join(temp_root, f'out_{uuid.uuid4().hex}.txt')
        task_name = f"Borealis Agent - Task - {uuid.uuid4().hex} @ SYSTEM"
        task_ps = f"""
$ErrorActionPreference='Continue'
$task = "{task_name}"
$ps   = "{ps_exe}"
$scr  = "{script_path}"
$out  = "{out_path}"
try {{ Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue }} catch {{}}
$action   = New-ScheduledTaskAction -Execute $ps -Argument ('-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $scr + '" *> "' + $out + '"') -WorkingDirectory (Split-Path -Parent $scr)
$settings = New-ScheduledTaskSettingsSet -DeleteExpiredTaskAfter (New-TimeSpan -Minutes 5) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
$principal= New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -TaskName $task -Action $action -Settings $settings -Principal $principal -Force | Out-Null
Start-ScheduledTask -TaskName $task | Out-Null
Start-Sleep -Seconds 2
Get-ScheduledTask -TaskName $task | Out-Null
"""
        proc = subprocess.run([ps_exe, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', task_ps], capture_output=True, text=True)
        if proc.returncode != 0:
            return -999, '', (proc.stderr or proc.stdout or 'scheduled task creation failed')
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
            time.sleep(1)
        # Cleanup best-effort
        try:
            cleanup_ps = f"try {{ Unregister-ScheduledTask -TaskName '{task_name}' -Confirm:$false }} catch {{}}"
            subprocess.run([ps_exe, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', cleanup_ps], capture_output=True, text=True)
        except Exception:
            pass
        try:
            if script_path and os.path.isfile(script_path):
                os.remove(script_path)
        except Exception:
            pass
        try:
            if out_path and os.path.isfile(out_path):
                os.remove(out_path)
        except Exception:
            pass
        return 0, out_data or '', ''
    except Exception as e:
        return -999, '', str(e)

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
if INTERACTIVE_RUNTIME_MODE:
    class PersistentWindow(QtWidgets.QWidget):
        def __init__(self):
            super().__init__()
            self.setWindowTitle("KeepAlive")
            self.setGeometry(-1000,-1000,1,1)
            dont_show_attr = qt_enum(
                QtCore.Qt,
                "WidgetAttribute.WA_DontShowOnScreen",
                getattr(QtCore.Qt, "WA_DontShowOnScreen", None),
            )
            if dont_show_attr is not None:
                self.setAttribute(dont_show_attr)
            self.hide()

# //////////////////////////////////////////////////////////////////////////
# MAIN & EVENT LOOP
# //////////////////////////////////////////////////////////////////////////
async def connect_loop():
    retry = _SOCKETIO_RETRY_SECONDS
    client = http_client()
    attempt = 0
    while True:
        active_sio = _ensure_socketio_client(AGENT_LOOP)
        if getattr(active_sio, "connected", False):
            await asyncio.sleep(_SOCKETIO_RECONNECT_PAUSE_SECONDS)
            continue
        attempt += 1
        heartbeat_stage = "auth"
        try:
            _log_agent(
                f'connect_loop attempt={attempt} starting authentication phase',
                fname='agent.log',
            )
            _sync_tray_status({}, client=client)
            if SYSTEM_SERVICE_MODE:
                try:
                    _heartbeat_record("authenticating", "active", f"Socket connect attempt {attempt} authentication.")
                except Exception:
                    pass
            client.ensure_authenticated()
            if SYSTEM_SERVICE_MODE:
                try:
                    _heartbeat_complete("authenticated", "Engine device authentication succeeded.")
                    _heartbeat_flush(reason="authenticated")
                except Exception:
                    pass
            auth_snapshot = {
                'guid_present': bool(client.guid),
                'access_token': bool(client.access_token),
                'refresh_token': bool(client.refresh_token),
                'access_expiry': client.access_expires_at,
            }
            _log_agent(
                f"connect_loop attempt={attempt} auth snapshot: {_format_debug_pairs(auth_snapshot)}",
                fname='agent.log',
            )
            heartbeat_stage = "socket"
            if SYSTEM_SERVICE_MODE:
                try:
                    _heartbeat_record("socket_connecting", "active", f"Socket connect attempt {attempt}.")
                    _heartbeat_flush(reason="socket_connecting")
                except Exception:
                    pass
            client.configure_socketio(active_sio)
            try:
                setattr(active_sio, "connection_error", None)
            except Exception:
                pass
            url = client.websocket_base_url()
            headers = client.auth_headers()
            header_summary = _summarize_headers(headers)
            verify_value = getattr(client.session, 'verify', None)
            ssl_kwargs = client.socketio_ssl_params()
            ssl_summary: Dict[str, Any] = {}
            for key, value in ssl_kwargs.items():
                if isinstance(value, ssl.SSLContext):
                    ssl_summary[key] = "SSLContext"
                else:
                    ssl_summary[key] = value
            _log_agent(
                f"connect_loop attempt={attempt} dialing websocket url={url} transports=['websocket'] "
                f"verify={verify_value!r} headers={header_summary} ssl={ssl_summary or '{}'}",
                fname='agent.log',
            )
            print(f"[INFO] Connecting Agent to {url}...")
            await active_sio.connect(
                url,
                transports=['websocket'],
                headers=headers,
                **ssl_kwargs,
            )
            _log_agent(
                f'connect_loop attempt={attempt} sio.connect completed successfully',
                fname='agent.log',
            )
            attempt = 0
            try:
                await active_sio.wait()
            finally:
                _log_agent(
                    'connect_loop observed websocket session end; re-authenticating before reconnect.',
                    fname='agent.log',
                )
            await asyncio.sleep(_SOCKETIO_RECONNECT_PAUSE_SECONDS)
        except Exception as e:
            detail = _describe_exception(e)
            try:
                conn_err = getattr(active_sio, "connection_error", None)
            except Exception:
                conn_err = None
            if conn_err:
                detail = f"{detail}; connection_error={conn_err!r}"

            tls_error = False
            _log_agent(
                f"connect_loop attempt={attempt} raw failure detail={detail}",
                fname="agent.error.log",
            )
            for exc in _iter_exception_chain(e):
                if isinstance(exc, aiohttp.client_exceptions.ClientConnectorCertificateError):
                    tls_error = True
                    break
                message = str(exc)
                if "CERTIFICATE_VERIFY_FAILED" in message.upper():
                    tls_error = True
                    break

            if not tls_error and conn_err and isinstance(conn_err, Exception):
                for exc in _iter_exception_chain(conn_err):
                    if isinstance(exc, aiohttp.client_exceptions.ClientConnectorCertificateError):
                        tls_error = True
                        break
                    message = str(exc)
                    if "CERTIFICATE_VERIFY_FAILED" in message.upper():
                        tls_error = True
                        break

            if tls_error:
                _log_agent(
                    f"connect_loop attempt={attempt} TLS failure detected detail={detail}",
                    fname="agent.error.log",
                )
                _log_exception_trace(f"connect_loop attempt={attempt} TLS failure")
                _record_tray_error("tls", detail, client=client, socket_connected=False)
            else:
                current_error_kind = str(_TRAY_STATUS_STATE.get("last_error_kind") or "").strip().lower()
                if current_error_kind != "auth":
                    _record_tray_error("transport", detail, client=client, socket_connected=False)
            if SYSTEM_SERVICE_MODE:
                try:
                    if heartbeat_stage == "auth":
                        _heartbeat_fail("authenticating", detail)
                        _heartbeat_flush(reason="auth_failed")
                    else:
                        _heartbeat_fail("socket_connecting", detail)
                        _heartbeat_flush(reason="socket_connect_failed")
                except Exception:
                    pass

            message = (
                f"connect_loop attempt={attempt} server unavailable: {detail}. "
                f"Retrying in {retry}s..."
            )
            print(f"[WebSocket] {message}")
            _log_agent(message, fname='agent.error.log')
            _log_exception_trace(f'connect_loop attempt={attempt}')
            await asyncio.sleep(retry)

if __name__=='__main__':
    try:
        _bootstrap_log('enter __main__')
    except Exception:
        pass
    _sync_tray_status(
        {
            "last_error_kind": "",
            "last_error_message": "",
            "socket_connected": False,
        }
    )
    # Ansible logs are rotated daily on write; no explicit clearing on startup
    if SYSTEM_SERVICE_MODE:
        loop = asyncio.new_event_loop(); asyncio.set_event_loop(loop)
    else:
        if QtWidgets is None or QEventLoop is None:
            raise RuntimeError("Qt runtime unavailable for interactive Borealis mode.")
        app=QtWidgets.QApplication(sys.argv)
        loop=QEventLoop(app); asyncio.set_event_loop(loop)
    AGENT_LOOP = loop
    sio = _create_socketio_client(loop)
    try:
        if not SYSTEM_SERVICE_MODE and not SESSION_HELPER_MODE:
            start_agent_bridge_pipe(loop)
    except Exception:
        pass
    if INTERACTIVE_RUNTIME_MODE:
        dummy_window=PersistentWindow(); dummy_window.show()
    # Initialize roles context for role tasks
    # Initialize role manager and hot-load roles from Roles/
    client = None
    if not SESSION_HELPER_MODE:
        client = http_client()
        if SYSTEM_SERVICE_MODE:
            try:
                _heartbeat_initialize_early(
                    hooks={
                        'http_client': http_client,
                        'log_agent': lambda message, **kwargs: _log_agent(message, **kwargs),
                    },
                    service_mode=SERVICE_MODE,
                )
            except Exception:
                pass
        try:
            if SYSTEM_SERVICE_MODE:
                _heartbeat_record("authenticating", "active", "Requesting Engine authentication.")
            client.ensure_authenticated()
            if SYSTEM_SERVICE_MODE:
                _heartbeat_complete("authenticated", "Engine device authentication succeeded.")
                _heartbeat_flush(reason="authenticated")
        except Exception as exc:
            _log_agent(f'Authentication bootstrap failed: {exc}', fname='agent.error.log')
            print(f"[WARN] Authentication bootstrap failed: {exc}")
            _record_tray_error("auth", _describe_exception(exc), client=client, socket_connected=False)
            if SYSTEM_SERVICE_MODE:
                _heartbeat_fail("authenticating", _describe_exception(exc))
        try:
            build_id = _current_agent_build_id(sync=True)
            if build_id:
                _log_agent(f'Agent build id ready: {build_id}')
        except Exception:
            pass
    else:
        _log_agent('Starting Borealis helper runtime without Engine authentication bootstrap.')
    try:
        base_hooks = {
            'send_service_control': send_service_control,
            'get_server_url': get_server_url,
            'get_agent_id': lambda: AGENT_ID,
            'get_agent_build_id': lambda: _current_agent_build_id(sync=True),
            'log_agent': lambda message, **kwargs: _log_agent(message, **kwargs),
            'role_health_registry': ROLE_HEALTH_REGISTRY,
            'process_restart_request_now': _process_tray_restart_request_now,
            'sync_tray_status': _sync_tray_status,
            'launch_manual_agent_update': _launch_manual_agent_update,
            'agent_status_record': _heartbeat_record,
            'agent_status_complete': _heartbeat_complete,
            'agent_status_failed': _heartbeat_fail,
            'agent_status_flush': _heartbeat_flush,
        }
        def _request_system_service_action(service_name: str, action: str, requested_by: str) -> None:
            manager = ROLE_MANAGER_SYS
            if manager is None:
                raise RuntimeError("system role manager unavailable")
            role = manager.roles.get("service_management")
            requester = getattr(role, "request_action", None)
            if not callable(requester):
                raise RuntimeError("service management role unavailable")
            requester(service_name, action, requested_by)

        def _request_system_software_refresh(*, reason: str = "") -> None:
            manager = ROLE_MANAGER_SYS
            if manager is None:
                raise RuntimeError("system role manager unavailable")
            role = manager.roles.get("software_management")
            requester = getattr(role, "request_refresh", None)
            if not callable(requester):
                raise RuntimeError("software management role unavailable")
            requester(reason=reason)

        base_hooks['request_system_service_action'] = _request_system_service_action
        base_hooks['request_system_software_refresh'] = _request_system_software_refresh
        if not SESSION_HELPER_MODE:
            base_hooks['http_client'] = http_client
        if SYSTEM_SERVICE_MODE:
            try:
                _heartbeat_record("roles_loading", "active", "Loading SYSTEM agent roles.")
            except Exception:
                pass
        if SYSTEM_SERVICE_MODE:
            SESSION_HELPER_BROKER = SessionHelperBroker(
                loop=loop,
                log=lambda message: _log_agent(message, fname='agent.log'),
                emit_quick_job_result=_emit_quick_job_result_local,
                http_client_factory=http_client,
            )
            try:
                ROLE_HEALTH_REGISTRY.register_role(
                    'context_currentuser',
                    context='currentuser',
                    reporter=SESSION_HELPER_BROKER.currentuser_role_health,
                    role_label='Current User Context',
                )
            except Exception as exc:
                _log_agent(f'Failed to register currentuser helper health reporter: {exc}', fname='agent.error.log')
            base_hooks['dispatch_currentuser_quick_job'] = SESSION_HELPER_BROKER.dispatch_currentuser_quick_job
            base_hooks['session_inventory_enricher'] = SESSION_HELPER_BROKER.enrich_session_inventory
            try:
                _heartbeat_complete("helper_broker_ready", "Current-user session broker initialized.")
            except Exception:
                pass
        if SESSION_HELPER_MODE:
            def _shutdown_helper_runtime() -> None:
                try:
                    if QtWidgets is not None:
                        app_instance = QtWidgets.QApplication.instance()
                        if app_instance is not None:
                            app_instance.quit()
                            return
                except Exception:
                    pass
                try:
                    if AGENT_LOOP is not None:
                        AGENT_LOOP.call_soon_threadsafe(AGENT_LOOP.stop)
                except Exception:
                    pass

            HELPER_CLIENT = SessionHelperClient(
                loop=loop,
                address=str(HELPER_PIPE_ADDRESS or ''),
                auth_token=str(HELPER_AUTH_TOKEN or ''),
                session_id=int(HELPER_SESSION_ID or 0),
                log=lambda message: _log_agent(message, fname='agent.log'),
                shutdown_callback=_shutdown_helper_runtime,
            )
            HELPER_CLIENT.start()
            hooks_helper = {
                **base_hooks,
                'service_mode': 'currentuser',
                'register_local_helper_handler': HELPER_CLIENT.register_handler,
            }
            ROLE_MANAGER = RoleManager(
                base_dir=os.path.dirname(__file__),
                context='helper',
                sio=sio,
                agent_id=AGENT_ID,
                config=CONFIG,
                loop=loop,
                hooks=hooks_helper,
            )
            ROLE_MANAGER.load()
        elif not SYSTEM_SERVICE_MODE:
            # Load interactive-context roles (tray/UI, current-user execution, screenshot, etc.)
            hooks_interactive = {**base_hooks, 'service_mode': 'currentuser'}
            ROLE_MANAGER = RoleManager(
                base_dir=os.path.dirname(__file__),
                context='interactive',
                sio=sio,
                agent_id=AGENT_ID,
                config=CONFIG,
                loop=loop,
                hooks=hooks_interactive,
            )
            ROLE_MANAGER.load()
        # Load system roles only when running in SYSTEM service mode
        ROLE_MANAGER_SYS = None
        if SYSTEM_SERVICE_MODE:
            hooks_system = {**base_hooks, 'service_mode': 'system'}
            ROLE_MANAGER_SYS = RoleManager(
                base_dir=os.path.dirname(__file__),
                context='system',
                sio=sio,
                agent_id=AGENT_ID,
                config=CONFIG,
                loop=loop,
                hooks=hooks_system,
            )
            ROLE_MANAGER_SYS.load()
            try:
                _heartbeat_complete("roles_ready", "SYSTEM roles loaded.")
                _heartbeat_flush(reason="roles_ready")
            except Exception:
                pass
    except Exception as e:
        try:
            _bootstrap_log(f'role load init failed: {e}')
        except Exception:
            pass
        if SYSTEM_SERVICE_MODE:
            try:
                _heartbeat_fail("roles_loading", _describe_exception(e) if isinstance(e, BaseException) else str(e))
                _heartbeat_flush(reason="roles_loading_failed")
            except Exception:
                pass
    _sync_tray_status({}, client=client)
    try:
        if not SESSION_HELPER_MODE:
            background_tasks.append(loop.create_task(config_watcher()))
            background_tasks.append(loop.create_task(connect_loop()))
            background_tasks.append(loop.create_task(idle_task()))
            background_tasks.append(loop.create_task(send_heartbeat()))
            if SYSTEM_SERVICE_MODE:
                background_tasks.append(loop.create_task(refresh_startup_telemetry()))
            background_tasks.append(loop.create_task(watch_tray_restart_requests()))
            background_tasks.append(loop.create_task(poll_script_requests()))
        if SYSTEM_SERVICE_MODE and SESSION_HELPER_BROKER is not None:
            background_tasks.append(loop.create_task(watch_session_helper_reconcile()))
        # Inventory upload is handled by the DeviceAudit role running in SYSTEM context.
        # Do not schedule the legacy agent-level details poster to avoid duplicates.
        
        _bootstrap_log('starting event loop...')
        loop.run_forever()
    except Exception as e:
        try:
            _bootstrap_log(f'FATAL: event loop crashed: {e}')
        except Exception:
            pass
        print(f"[FATAL] Event loop crashed: {e}")
        traceback.print_exc()
    finally:
        try:
            if HELPER_CLIENT is not None:
                HELPER_CLIENT.stop()
        except Exception:
            pass
        try:
            if SESSION_HELPER_BROKER is not None:
                SESSION_HELPER_BROKER.stop()
        except Exception:
            pass
        try:
            if _PLANNED_SHUTDOWN_REASON == "tray_restart":
                _bootstrap_log('Agent restarting per tray request.')
            else:
                _bootstrap_log('Agent exited unexpectedly.')
        except Exception:
            pass
        if _PLANNED_SHUTDOWN_REASON == "tray_restart":
            print("[INFO] Agent restarting per tray request.")
        else:
            print("[FATAL] Agent exited unexpectedly.")
