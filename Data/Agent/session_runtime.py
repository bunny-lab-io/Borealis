from __future__ import annotations

import asyncio
import base64
import contextlib
import os
import secrets
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
from dataclasses import dataclass, field
from multiprocessing.connection import Client, Listener
from typing import Any, Callable, Dict, Iterable, List, Mapping, Optional, Sequence, Tuple

from signature_utils import decode_script_bytes, verify_and_store_script_signature


IS_WINDOWS = os.name == "nt"
SESSION_TARGET_ALL = "all_active_sessions"
SESSION_TARGET_SPECIFIC = "specific_session"
_HELPER_HEARTBEAT_STALE_AFTER_SECONDS = 45
_SESSION_RECONCILE_INTERVAL_SECONDS = 10
_HELPER_LAUNCH_GRACE_SECONDS = max(30, _SESSION_RECONCILE_INTERVAL_SECONDS * 3)


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


def _format_helper_session_label(entry: Mapping[str, Any]) -> str:
    username = _clean_text(entry.get("username"))
    session_name = _clean_text(entry.get("session_name"))
    base = username or f"session-{normalize_target_session_id(entry.get('session_id')) or 'unknown'}"
    return f"{base} ({session_name})" if session_name else base


def _format_helper_session_lines(
    entries: Sequence[Mapping[str, Any]],
    *,
    status_text: str,
) -> str:
    lines: List[str] = []
    for entry in entries:
        if not isinstance(entry, Mapping):
            continue
        label = _format_helper_session_label(entry)
        if label:
            lines.append(f"{label} - {status_text}")
    return "\n".join(lines)


def normalize_session_target(value: Any, *, default: str = SESSION_TARGET_ALL) -> str:
    text = _clean_text(value).lower().replace("-", "_")
    if text in {"specific", "specific_session", "single", "session"}:
        return SESSION_TARGET_SPECIFIC
    if text in {"all", "all_active_sessions", "all_sessions", "fanout"}:
        return SESSION_TARGET_ALL
    return SESSION_TARGET_SPECIFIC if default == SESSION_TARGET_SPECIFIC else SESSION_TARGET_ALL


def normalize_target_session_id(value: Any) -> int:
    parsed = _coerce_int(value, 0)
    return parsed if parsed > 0 else 0


def is_interactive_session_state(value: Any) -> bool:
    return _clean_text(value).lower() in {"active", "locked"}


def build_currentuser_dispatch_fields(
    *,
    run_mode: Any,
    session_target: Any = None,
    target_session_id: Any = None,
    default_session_target: str = SESSION_TARGET_ALL,
) -> Dict[str, Any]:
    normalized_run_mode = _clean_text(run_mode).lower()
    payload = {
        "target_context": normalized_run_mode or "system",
    }
    if normalized_run_mode != "currentuser":
        return payload
    normalized_target = normalize_session_target(session_target, default=default_session_target)
    payload["session_target"] = normalized_target
    if normalized_target == SESSION_TARGET_SPECIFIC:
        session_id = normalize_target_session_id(target_session_id)
        if session_id > 0:
            payload["target_session_id"] = session_id
    return payload


def session_is_eligible(entry: Mapping[str, Any]) -> bool:
    return is_interactive_session_state(entry.get("state_code") or entry.get("state"))


def _connection_family() -> str:
    return "AF_PIPE" if IS_WINDOWS else "AF_UNIX"


def _listener_address(session_id: int, suffix: str = "") -> str:
    normalized_suffix = _clean_text(suffix)
    if normalized_suffix:
        normalized_suffix = "".join(ch for ch in normalized_suffix if ch.isalnum() or ch in ("-", "_"))
    if IS_WINDOWS:
        base = rf"\\.\pipe\Borealis_SessionHelper_{int(session_id)}"
        return f"{base}_{normalized_suffix}" if normalized_suffix else base
    base = os.path.join(tempfile.gettempdir(), f"borealis_session_helper_{int(session_id)}")
    return f"{base}_{normalized_suffix}.sock" if normalized_suffix else f"{base}.sock"


def _create_helper_listener(*, session_id: int, address: str, auth_token: str):
    authkey = auth_token.encode("utf-8")
    if IS_WINDOWS:
        return _WindowsSessionPipeListener(
            address=address,
            authkey=authkey,
            session_id=session_id,
        )
    return Listener(address, family=_connection_family(), authkey=authkey)


def _session_user_sid_windows(session_id: int) -> str:
    if not IS_WINDOWS:
        return ""

    import ctypes
    from ctypes import wintypes

    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    advapi32 = ctypes.WinDLL("advapi32", use_last_error=True)
    wtsapi32 = ctypes.WinDLL("wtsapi32", use_last_error=True)

    TokenUser = 1

    class SID_AND_ATTRIBUTES(ctypes.Structure):
        _fields_ = [
            ("Sid", wintypes.LPVOID),
            ("Attributes", wintypes.DWORD),
        ]

    class TOKEN_USER(ctypes.Structure):
        _fields_ = [("User", SID_AND_ATTRIBUTES)]

    wtsapi32.WTSQueryUserToken.argtypes = [wintypes.ULONG, ctypes.POINTER(wintypes.HANDLE)]
    wtsapi32.WTSQueryUserToken.restype = wintypes.BOOL

    advapi32.GetTokenInformation.argtypes = [
        wintypes.HANDLE,
        wintypes.DWORD,
        wintypes.LPVOID,
        wintypes.DWORD,
        ctypes.POINTER(wintypes.DWORD),
    ]
    advapi32.GetTokenInformation.restype = wintypes.BOOL
    advapi32.ConvertSidToStringSidW.argtypes = [wintypes.LPVOID, ctypes.POINTER(wintypes.LPWSTR)]
    advapi32.ConvertSidToStringSidW.restype = wintypes.BOOL

    kernel32.LocalFree.argtypes = [wintypes.LPVOID]
    kernel32.LocalFree.restype = wintypes.LPVOID
    kernel32.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel32.CloseHandle.restype = wintypes.BOOL

    token = wintypes.HANDLE()
    buffer = None
    sid_text = wintypes.LPWSTR()
    required = wintypes.DWORD(0)
    try:
        if not wtsapi32.WTSQueryUserToken(int(session_id), ctypes.byref(token)):
            raise ctypes.WinError(ctypes.get_last_error())
        advapi32.GetTokenInformation(token, TokenUser, None, 0, ctypes.byref(required))
        if int(required.value or 0) <= 0:
            raise ctypes.WinError(ctypes.get_last_error())
        buffer = ctypes.create_string_buffer(required.value)
        if not advapi32.GetTokenInformation(token, TokenUser, buffer, required, ctypes.byref(required)):
            raise ctypes.WinError(ctypes.get_last_error())
        token_user = ctypes.cast(buffer, ctypes.POINTER(TOKEN_USER)).contents
        if not advapi32.ConvertSidToStringSidW(token_user.User.Sid, ctypes.byref(sid_text)):
            raise ctypes.WinError(ctypes.get_last_error())
        return _clean_text(sid_text.value)
    finally:
        if sid_text:
            with contextlib.suppress(Exception):
                kernel32.LocalFree(sid_text)
        if token:
            with contextlib.suppress(Exception):
                kernel32.CloseHandle(token)


class _WindowsSessionPipeListener:
    def __init__(self, *, address: str, authkey: bytes, session_id: int) -> None:
        if not IS_WINDOWS:
            raise RuntimeError("Windows pipe listener is only available on Windows")

        import ctypes
        from ctypes import wintypes
        import multiprocessing.connection as mp_connection

        self._ctypes = ctypes
        self._wintypes = wintypes
        self._mp_connection = mp_connection
        self._address = address
        self._authkey = authkey
        self._session_id = int(session_id)
        self._last_accepted = None
        self._kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
        self._advapi32 = ctypes.WinDLL("advapi32", use_last_error=True)

        class SECURITY_ATTRIBUTES(ctypes.Structure):
            _fields_ = [
                ("nLength", wintypes.DWORD),
                ("lpSecurityDescriptor", wintypes.LPVOID),
                ("bInheritHandle", wintypes.BOOL),
            ]

        self._SECURITY_ATTRIBUTES = SECURITY_ATTRIBUTES
        self._kernel32.CreateNamedPipeW.argtypes = [
            wintypes.LPCWSTR,
            wintypes.DWORD,
            wintypes.DWORD,
            wintypes.DWORD,
            wintypes.DWORD,
            wintypes.DWORD,
            wintypes.DWORD,
            ctypes.POINTER(SECURITY_ATTRIBUTES),
        ]
        self._kernel32.CreateNamedPipeW.restype = wintypes.HANDLE
        self._kernel32.ConnectNamedPipe.argtypes = [wintypes.HANDLE, wintypes.LPVOID]
        self._kernel32.ConnectNamedPipe.restype = wintypes.BOOL
        self._kernel32.CloseHandle.argtypes = [wintypes.HANDLE]
        self._kernel32.CloseHandle.restype = wintypes.BOOL
        self._kernel32.LocalFree.argtypes = [wintypes.LPVOID]
        self._kernel32.LocalFree.restype = wintypes.LPVOID
        self._advapi32.ConvertStringSecurityDescriptorToSecurityDescriptorW.argtypes = [
            wintypes.LPCWSTR,
            wintypes.DWORD,
            ctypes.POINTER(wintypes.LPVOID),
            ctypes.POINTER(wintypes.DWORD),
        ]
        self._advapi32.ConvertStringSecurityDescriptorToSecurityDescriptorW.restype = wintypes.BOOL

        self._handle = self._create_pipe_handle()

    def _create_pipe_handle(self):
        ctypes = self._ctypes
        wintypes = self._wintypes
        import _winapi

        user_sid = _session_user_sid_windows(self._session_id)
        sddl = "D:(A;;GA;;;SY)(A;;GA;;;BA)"
        if user_sid:
            sddl = f"{sddl}(A;;GA;;;{user_sid})"
        else:
            sddl = f"{sddl}(A;;GA;;;AU)"

        security_descriptor = wintypes.LPVOID()
        if not self._advapi32.ConvertStringSecurityDescriptorToSecurityDescriptorW(
            sddl,
            1,
            ctypes.byref(security_descriptor),
            None,
        ):
            raise ctypes.WinError(ctypes.get_last_error())
        try:
            security_attributes = self._SECURITY_ATTRIBUTES()
            security_attributes.nLength = ctypes.sizeof(self._SECURITY_ATTRIBUTES)
            security_attributes.lpSecurityDescriptor = security_descriptor
            security_attributes.bInheritHandle = False
            handle = self._kernel32.CreateNamedPipeW(
                self._address,
                _winapi.PIPE_ACCESS_DUPLEX | _winapi.FILE_FLAG_FIRST_PIPE_INSTANCE,
                _winapi.PIPE_TYPE_MESSAGE | _winapi.PIPE_READMODE_MESSAGE | _winapi.PIPE_WAIT,
                1,
                8192,
                8192,
                _winapi.NMPWAIT_WAIT_FOREVER,
                ctypes.byref(security_attributes),
            )
        finally:
            with contextlib.suppress(Exception):
                self._kernel32.LocalFree(security_descriptor)
        invalid_handle = ctypes.c_void_p(-1).value
        if int(handle or 0) == int(invalid_handle or 0):
            raise ctypes.WinError(ctypes.get_last_error())
        return handle

    def accept(self):
        import _winapi

        if not self._handle:
            raise OSError("listener is closed")

        connected = self._kernel32.ConnectNamedPipe(self._handle, None)
        if not connected:
            error = self._ctypes.get_last_error()
            if error not in (_winapi.ERROR_PIPE_CONNECTED, _winapi.ERROR_NO_DATA):
                raise self._ctypes.WinError(error)

        pipe_connection = getattr(self._mp_connection, "PipeConnection", None)
        if pipe_connection is None:
            raise RuntimeError("PipeConnection is unavailable on this runtime")
        connection = pipe_connection(self._handle)
        self._handle = None
        self._last_accepted = self._address
        if self._authkey is not None:
            self._mp_connection.deliver_challenge(connection, self._authkey)
            self._mp_connection.answer_challenge(connection, self._authkey)
        return connection

    def close(self) -> None:
        if self._handle:
            with contextlib.suppress(Exception):
                self._kernel32.CloseHandle(self._handle)
            self._handle = None


def _helper_command(
    *,
    session_id: int,
    address: str,
    auth_token: str,
) -> Tuple[List[str], str]:
    borealis_dir = os.path.abspath(os.path.dirname(__file__))
    venv_root = os.path.abspath(os.path.join(borealis_dir, os.pardir))
    venv_scripts = os.path.join(venv_root, "Scripts")
    preferred = [
        os.path.join(venv_scripts, "pythonw.exe"),
        os.path.join(venv_scripts, "python.exe"),
        sys.executable,
    ]
    executable = ""
    for candidate in preferred:
        if candidate and os.path.isfile(candidate):
            executable = candidate
            break
    if not executable:
        executable = sys.executable
    return (
        [
            executable,
            "-W",
            "ignore::SyntaxWarning",
            os.path.join(borealis_dir, "agent.py"),
            "--session-helper",
            "--helper-session-id",
            str(int(session_id)),
            "--helper-pipe-address",
            address,
            "--helper-auth-token",
            auth_token,
            "--config",
            "CURRENTUSER",
        ],
        borealis_dir,
    )


def _launch_helper_process_as_user_windows(
    *,
    session_id: int,
    command: Sequence[str],
    cwd: str,
) -> Tuple[bool, str, int]:
    import ctypes
    from ctypes import wintypes

    kernel32 = ctypes.WinDLL("kernel32", use_last_error=True)
    advapi32 = ctypes.WinDLL("advapi32", use_last_error=True)
    userenv = ctypes.WinDLL("userenv", use_last_error=True)
    wtsapi32 = ctypes.WinDLL("wtsapi32", use_last_error=True)

    CREATE_UNICODE_ENVIRONMENT = 0x00000400
    CREATE_NEW_PROCESS_GROUP = 0x00000200
    DETACHED_PROCESS = 0x00000008
    TOKEN_ASSIGN_PRIMARY = 0x0001
    TOKEN_DUPLICATE = 0x0002
    TOKEN_QUERY = 0x0008
    TOKEN_ADJUST_DEFAULT = 0x0080
    TOKEN_ADJUST_SESSIONID = 0x0100
    MAXIMUM_ALLOWED = 0x02000000
    WTSUserName = 5
    WTSDomainName = 7
    SecurityImpersonation = 2
    TokenPrimary = 1
    PI_NOUI = 0x00000001

    class STARTUPINFO(ctypes.Structure):
        _fields_ = [
            ("cb", wintypes.DWORD),
            ("lpReserved", wintypes.LPWSTR),
            ("lpDesktop", wintypes.LPWSTR),
            ("lpTitle", wintypes.LPWSTR),
            ("dwX", wintypes.DWORD),
            ("dwY", wintypes.DWORD),
            ("dwXSize", wintypes.DWORD),
            ("dwYSize", wintypes.DWORD),
            ("dwXCountChars", wintypes.DWORD),
            ("dwYCountChars", wintypes.DWORD),
            ("dwFillAttribute", wintypes.DWORD),
            ("dwFlags", wintypes.DWORD),
            ("wShowWindow", wintypes.WORD),
            ("cbReserved2", wintypes.WORD),
            ("lpReserved2", ctypes.POINTER(ctypes.c_byte)),
            ("hStdInput", wintypes.HANDLE),
            ("hStdOutput", wintypes.HANDLE),
            ("hStdError", wintypes.HANDLE),
        ]

    class PROCESS_INFORMATION(ctypes.Structure):
        _fields_ = [
            ("hProcess", wintypes.HANDLE),
            ("hThread", wintypes.HANDLE),
            ("dwProcessId", wintypes.DWORD),
            ("dwThreadId", wintypes.DWORD),
        ]

    class PROFILEINFO(ctypes.Structure):
        _fields_ = [
            ("dwSize", wintypes.DWORD),
            ("dwFlags", wintypes.DWORD),
            ("lpUserName", wintypes.LPWSTR),
            ("lpProfilePath", wintypes.LPWSTR),
            ("lpDefaultPath", wintypes.LPWSTR),
            ("lpServerName", wintypes.LPWSTR),
            ("lpPolicyPath", wintypes.LPWSTR),
            ("hProfile", wintypes.HANDLE),
        ]

    wtsapi32.WTSQueryUserToken.argtypes = [wintypes.ULONG, ctypes.POINTER(wintypes.HANDLE)]
    wtsapi32.WTSQueryUserToken.restype = wintypes.BOOL
    wtsapi32.WTSQuerySessionInformationW.argtypes = [
        wintypes.HANDLE,
        wintypes.DWORD,
        wintypes.DWORD,
        ctypes.POINTER(wintypes.LPWSTR),
        ctypes.POINTER(wintypes.DWORD),
    ]
    wtsapi32.WTSQuerySessionInformationW.restype = wintypes.BOOL
    wtsapi32.WTSFreeMemory.argtypes = [wintypes.LPVOID]
    wtsapi32.WTSFreeMemory.restype = None

    advapi32.DuplicateTokenEx.argtypes = [
        wintypes.HANDLE,
        wintypes.DWORD,
        wintypes.LPVOID,
        wintypes.DWORD,
        wintypes.DWORD,
        ctypes.POINTER(wintypes.HANDLE),
    ]
    advapi32.DuplicateTokenEx.restype = wintypes.BOOL
    advapi32.CreateProcessAsUserW.argtypes = [
        wintypes.HANDLE,
        wintypes.LPCWSTR,
        wintypes.LPWSTR,
        wintypes.LPVOID,
        wintypes.LPVOID,
        wintypes.BOOL,
        wintypes.DWORD,
        wintypes.LPVOID,
        wintypes.LPCWSTR,
        ctypes.POINTER(STARTUPINFO),
        ctypes.POINTER(PROCESS_INFORMATION),
    ]
    advapi32.CreateProcessAsUserW.restype = wintypes.BOOL

    userenv.CreateEnvironmentBlock.argtypes = [ctypes.POINTER(wintypes.LPVOID), wintypes.HANDLE, wintypes.BOOL]
    userenv.CreateEnvironmentBlock.restype = wintypes.BOOL
    userenv.DestroyEnvironmentBlock.argtypes = [wintypes.LPVOID]
    userenv.DestroyEnvironmentBlock.restype = wintypes.BOOL
    userenv.LoadUserProfileW.argtypes = [wintypes.HANDLE, ctypes.POINTER(PROFILEINFO)]
    userenv.LoadUserProfileW.restype = wintypes.BOOL
    userenv.UnloadUserProfile.argtypes = [wintypes.HANDLE, wintypes.HANDLE]
    userenv.UnloadUserProfile.restype = wintypes.BOOL

    kernel32.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel32.CloseHandle.restype = wintypes.BOOL

    def _query_session_string(info_class: int) -> str:
        value = wintypes.LPWSTR()
        size = wintypes.DWORD(0)
        try:
            ok = wtsapi32.WTSQuerySessionInformationW(
                0,
                int(session_id),
                int(info_class),
                ctypes.byref(value),
                ctypes.byref(size),
            )
            if not ok or not value:
                return ""
            return _clean_text(value.value)
        finally:
            if value:
                with contextlib.suppress(Exception):
                    wtsapi32.WTSFreeMemory(value)

    user_token = wintypes.HANDLE()
    primary_token = wintypes.HANDLE()
    environment = wintypes.LPVOID()
    profile_info: Optional[PROFILEINFO] = None
    process_info = PROCESS_INFORMATION()
    try:
        if not wtsapi32.WTSQueryUserToken(int(session_id), ctypes.byref(user_token)):
            raise ctypes.WinError(ctypes.get_last_error())

        desired_access = (
            TOKEN_ASSIGN_PRIMARY
            | TOKEN_DUPLICATE
            | TOKEN_QUERY
            | TOKEN_ADJUST_DEFAULT
            | TOKEN_ADJUST_SESSIONID
            | MAXIMUM_ALLOWED
        )
        if not advapi32.DuplicateTokenEx(
            user_token,
            desired_access,
            None,
            SecurityImpersonation,
            TokenPrimary,
            ctypes.byref(primary_token),
        ):
            raise ctypes.WinError(ctypes.get_last_error())

        if not userenv.CreateEnvironmentBlock(ctypes.byref(environment), primary_token, False):
            raise ctypes.WinError(ctypes.get_last_error())

        username = _query_session_string(WTSUserName)
        domain = _query_session_string(WTSDomainName)
        profile_user = username
        if username:
            profile_info = PROFILEINFO()
            profile_info.dwSize = ctypes.sizeof(PROFILEINFO)
            profile_info.dwFlags = PI_NOUI
            profile_info.lpUserName = wintypes.LPWSTR(profile_user)
            if not userenv.LoadUserProfileW(primary_token, ctypes.byref(profile_info)):
                profile_info = None

        startup = STARTUPINFO()
        startup.cb = ctypes.sizeof(STARTUPINFO)
        startup.lpDesktop = wintypes.LPWSTR("winsta0\\default")

        executable = _clean_text(command[0])
        command_line = subprocess.list2cmdline(list(command))
        command_buffer = ctypes.create_unicode_buffer(command_line)
        flags = CREATE_UNICODE_ENVIRONMENT | CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS
        if not advapi32.CreateProcessAsUserW(
            primary_token,
            executable,
            command_buffer,
            None,
            None,
            False,
            flags,
            environment,
            cwd,
            ctypes.byref(startup),
            ctypes.byref(process_info),
        ):
            raise ctypes.WinError(ctypes.get_last_error())
        pid = int(process_info.dwProcessId or 0)
        session_label = f"{domain}\\{username}" if username and domain else username or f"session-{int(session_id)}"
        return True, f"launched helper pid={pid} user={session_label}", pid
    except Exception as exc:
        return False, str(exc), 0
    finally:
        for handle in (process_info.hThread, process_info.hProcess):
            if handle:
                with contextlib.suppress(Exception):
                    kernel32.CloseHandle(handle)
        if profile_info is not None and profile_info.hProfile:
            with contextlib.suppress(Exception):
                userenv.UnloadUserProfile(primary_token, profile_info.hProfile)
        if environment:
            with contextlib.suppress(Exception):
                userenv.DestroyEnvironmentBlock(environment)
        for handle in (primary_token, user_token):
            if handle:
                with contextlib.suppress(Exception):
                    kernel32.CloseHandle(handle)


@dataclass
class _PendingJob:
    job_id: int
    context: Dict[str, Any]
    expected_sessions: Dict[int, Dict[str, Any]]
    results: Dict[int, Dict[str, Any]] = field(default_factory=dict)
    created_at: int = field(default_factory=lambda: int(time.time()))


@dataclass
class _HelperState:
    session_id: int
    session: Dict[str, Any]
    address: str
    auth_token: str
    listener: Optional[Listener] = None
    listener_thread: Optional[threading.Thread] = None
    reader_thread: Optional[threading.Thread] = None
    connection: Any = None
    launched_at: int = 0
    connected_at: int = 0
    last_seen_at: int = 0
    helper_pid: int = 0
    last_error: str = ""

    def ready(self) -> bool:
        now = int(time.time())
        return bool(self.connection is not None and self.last_seen_at and (now - int(self.last_seen_at)) <= _HELPER_HEARTBEAT_STALE_AFTER_SECONDS)

    def launch_in_progress(self) -> bool:
        if self.ready():
            return False
        now = int(time.time())
        if self.connection is not None:
            return bool(self.last_seen_at and (now - int(self.last_seen_at)) <= _HELPER_HEARTBEAT_STALE_AFTER_SECONDS)
        if self.connected_at > 0:
            return bool(self.last_seen_at and (now - int(self.last_seen_at)) <= _HELPER_HEARTBEAT_STALE_AFTER_SECONDS)
        if int(self.launched_at or 0) <= 0:
            return False
        listener_thread = self.listener_thread
        if listener_thread is None or not listener_thread.is_alive():
            return False
        return (now - int(self.launched_at)) <= _HELPER_LAUNCH_GRACE_SECONDS


class SessionHelperBroker:
    def __init__(
        self,
        *,
        loop,
        log: Callable[[str], None],
        emit_quick_job_result: Callable[[Dict[str, Any]], None],
        http_client_factory: Optional[Callable[[], Any]] = None,
    ) -> None:
        self._loop = loop
        self._log = log
        self._emit_quick_job_result = emit_quick_job_result
        self._http_client_factory = http_client_factory
        self._lock = threading.RLock()
        self._helpers: Dict[int, _HelperState] = {}
        self._pending_jobs: Dict[int, _PendingJob] = {}

    @property
    def reconcile_interval_seconds(self) -> int:
        return _SESSION_RECONCILE_INTERVAL_SECONDS

    def supports_currentuser_dispatch(self) -> bool:
        return True

    def currentuser_role_health(self) -> Dict[str, Any]:
        inventory = self.session_inventory_payload()
        sessions = inventory.get("sessions") or []
        eligible = [entry for entry in sessions if bool(entry.get("eligible_for_interactive"))]
        ready = [entry for entry in eligible if bool(entry.get("helper_ready"))]
        ready_helper_lines = _format_helper_session_lines(ready, status_text="Loaded Successfully")
        pending_helper_lines = _format_helper_session_lines(
            [entry for entry in eligible if not bool(entry.get("helper_ready"))],
            status_text="Helper Warming Up",
        )
        if ready:
            return {
                "role_name": "context_currentuser",
                "context": "currentuser",
                "status": "healthy",
                "role_label": "Current User Context",
                "detail": f"{len(ready)} interactive helper session(s) ready.",
                "details": {
                    "running_status": "Ready",
                    "execution_context": "CURRENTUSER",
                    "eligible_sessions": str(len(eligible)),
                    "listener_ready": True,
                    "listener_state": "Registered",
                    "loaded_helper_sessions": ready_helper_lines,
                    "ready_helpers": str(len(ready)),
                },
            }
        if eligible:
            return {
                "role_name": "context_currentuser",
                "context": "currentuser",
                "status": "recovering",
                "role_label": "Current User Context",
                "detail": "Interactive sessions are present, but helper connections are still warming up.",
                "details": {
                    "running_status": "Recovering",
                    "execution_context": "CURRENTUSER",
                    "eligible_sessions": str(len(eligible)),
                    "listener_ready": False,
                    "listener_state": "Registering",
                    "loaded_helper_sessions": ready_helper_lines,
                    "pending_helper_sessions": pending_helper_lines,
                    "ready_helpers": "0",
                },
            }
        return {
            "role_name": "context_currentuser",
            "context": "currentuser",
            "status": "pending",
            "role_label": "Current User Context",
            "detail": "No interactive user sessions are currently eligible.",
            "details": {
                "running_status": "Waiting",
                "execution_context": "CURRENTUSER",
                "eligible_sessions": "0",
                "listener_ready": False,
                "listener_state": "Not Registered",
                "loaded_helper_sessions": "",
                "ready_helpers": "0",
            },
        }

    def session_inventory_payload(self) -> Dict[str, Any]:
        try:
            from Roles.role_system_device_auditor import collect_sessions  # type: ignore
        except Exception:
            try:
                from Data.Agent.Roles.role_system_device_auditor import collect_sessions  # type: ignore
            except Exception:
                collect_sessions = None
        raw_payload = collect_sessions() if callable(collect_sessions) else {"reported_at": int(time.time()), "sessions": []}
        return self.enrich_session_inventory(raw_payload)

    def enrich_session_inventory(self, raw_payload: Any) -> Dict[str, Any]:
        reported_at = int(time.time())
        sessions = []
        candidate_sessions = []
        if isinstance(raw_payload, dict):
            reported_at = _coerce_int(raw_payload.get("reported_at"), reported_at)
            candidate_sessions = raw_payload.get("sessions") if isinstance(raw_payload.get("sessions"), list) else []
        elif isinstance(raw_payload, list):
            candidate_sessions = raw_payload
        helper_map: Dict[int, _HelperState]
        with self._lock:
            helper_map = dict(self._helpers)
        for item in candidate_sessions or []:
            if not isinstance(item, dict):
                continue
            session_id = normalize_target_session_id(item.get("session_id"))
            helper = helper_map.get(session_id)
            helper_ready = bool(helper.ready()) if helper is not None else False
            sessions.append(
                {
                    "session_id": session_id,
                    "username": _clean_text(item.get("username")),
                    "session_name": _clean_text(item.get("session_name")),
                    "state": _clean_text(item.get("state") or item.get("state_code") or "unknown").lower() or "unknown",
                    "state_code": _clean_text(item.get("state_code") or item.get("state") or "unknown").lower() or "unknown",
                    "protocol": _clean_text(item.get("protocol")).lower() or "other",
                    "is_rdp": bool(item.get("is_rdp")),
                    "eligible_for_interactive": session_is_eligible(item),
                    "helper_ready": helper_ready,
                    "helper_pid": int(helper.helper_pid or 0) if helper is not None else 0,
                    "helper_last_seen_at": int(helper.last_seen_at or 0) if helper is not None else 0,
                }
            )
        return {"reported_at": reported_at, "sessions": sessions}

    def reconcile_sessions(self, raw_payload: Any = None) -> Dict[str, Any]:
        payload = self.enrich_session_inventory(raw_payload if raw_payload is not None else self.session_inventory_payload())
        desired_sessions = {
            int(entry.get("session_id") or 0): dict(entry)
            for entry in (payload.get("sessions") or [])
            if int(entry.get("session_id") or 0) > 0 and bool(entry.get("eligible_for_interactive"))
        }
        with self._lock:
            existing_ids = set(self._helpers.keys())
        for session_id, session in desired_sessions.items():
            self._ensure_helper(session_id, session)
        for stale_id in sorted(existing_ids - set(desired_sessions.keys())):
            self._stop_helper(stale_id, reason="session_ineligible")
        return payload

    def dispatch_currentuser_quick_job(self, payload: Dict[str, Any]) -> Tuple[bool, str]:
        if not isinstance(payload, dict):
            return False, "invalid quick-job payload"
        job_id = _coerce_int(payload.get("job_id"), 0)
        if job_id <= 0:
            return False, "missing job_id"
        verified, verify_error = self._verify_payload_signature(payload)
        if not verified:
            return False, verify_error or "signature_verification_failed"
        session_target = normalize_session_target(payload.get("session_target"), default=SESSION_TARGET_ALL)
        target_session_id = normalize_target_session_id(payload.get("target_session_id"))
        inventory = self.reconcile_sessions()
        eligible_sessions = {
            int(entry.get("session_id") or 0): dict(entry)
            for entry in (inventory.get("sessions") or [])
            if int(entry.get("session_id") or 0) > 0 and bool(entry.get("helper_ready")) and bool(entry.get("eligible_for_interactive"))
        }
        if session_target == SESSION_TARGET_SPECIFIC:
            if target_session_id <= 0:
                return False, "target_session_id is required for specific_session dispatch"
            eligible_sessions = {
                session_id: session
                for session_id, session in eligible_sessions.items()
                if session_id == target_session_id
            }
        if not eligible_sessions:
            return False, "no_interactive_user_session"

        accepted_sessions: Dict[int, Dict[str, Any]] = {}
        for session_id, session in eligible_sessions.items():
            helper = self._helper_for_session(session_id)
            if helper is None or not helper.ready():
                continue
            message = {
                "type": "quick_job_run",
                "payload": {
                    **payload,
                    "broker_verified": True,
                    "session_id": session_id,
                    "target_session_id": session_id if session_target == SESSION_TARGET_SPECIFIC else payload.get("target_session_id"),
                },
            }
            if self._send_helper_message(helper, message):
                accepted_sessions[session_id] = session

        if not accepted_sessions:
            return False, "currentuser_helper_not_ready"

        pending = _PendingJob(
            job_id=job_id,
            context=dict(payload.get("context") or {}),
            expected_sessions=accepted_sessions,
        )
        with self._lock:
            self._pending_jobs[job_id] = pending
        if len(accepted_sessions) == 1:
            return True, ""
        return True, ""

    def stop(self) -> None:
        with self._lock:
            helper_ids = list(self._helpers.keys())
        for session_id in helper_ids:
            self._stop_helper(session_id, reason="broker_stop")

    def _verify_payload_signature(self, payload: Dict[str, Any]) -> Tuple[bool, str]:
        script_bytes = decode_script_bytes(payload.get("script_content"), payload.get("script_encoding"))
        if script_bytes is None:
            return False, "Invalid script payload (unable to decode)."
        signature_b64 = payload.get("signature")
        sig_alg = _clean_text(payload.get("sig_alg") or "ed25519").lower()
        signing_key = payload.get("signing_key")
        if sig_alg and sig_alg not in {"ed25519", "eddsa"}:
            return False, f"Unsupported script signature algorithm: {sig_alg}"
        if not isinstance(signature_b64, str) or not signature_b64.strip():
            return False, "Missing script signature; rejecting payload"
        if not callable(self._http_client_factory):
            return False, "Signature verification unavailable (client missing)"
        try:
            client = self._http_client_factory()
        except Exception as exc:
            return False, f"Signature verification unavailable ({exc})"
        if client is None:
            return False, "Signature verification unavailable (client missing)"
        if not verify_and_store_script_signature(client, script_bytes, signature_b64, signing_key):
            return False, "Rejected script payload due to invalid signature"
        return True, ""

    def _helper_for_session(self, session_id: int) -> Optional[_HelperState]:
        with self._lock:
            return self._helpers.get(int(session_id))

    def _ensure_helper(self, session_id: int, session: Dict[str, Any]) -> None:
        existing_error = ""
        existing_helper = None
        with self._lock:
            helper = self._helpers.get(session_id)
            if helper is not None:
                helper.session = dict(session)
                if helper.ready() or helper.launch_in_progress():
                    return
                existing_error = _clean_text(helper.last_error)
                existing_helper = helper
        if existing_helper is not None:
            detail = existing_error or "helper_not_ready"
            self._log(f"session helper session_id={session_id} restarting stale helper error={detail}")
            self._stop_helper(session_id, reason="helper_restart")
        address_suffix = f"{os.getpid()}_{int(time.time())}_{secrets.token_hex(4)}"
        address = _listener_address(session_id, address_suffix)
        auth_token = base64.urlsafe_b64encode(secrets.token_bytes(18)).decode("ascii")
        helper = _HelperState(session_id=session_id, session=dict(session), address=address, auth_token=auth_token)
        try:
            if not IS_WINDOWS and os.path.exists(address):
                os.unlink(address)
        except Exception:
            pass
        try:
            listener = _create_helper_listener(
                session_id=session_id,
                address=address,
                auth_token=auth_token,
            )
        except Exception as exc:
            helper.last_error = f"listener_create_failed: {exc}"
            self._log(
                "session helper session_id={0} listener create failed address={1} error={2}".format(
                    session_id,
                    address,
                    exc,
                )
            )
            return
        helper.listener = listener
        helper.launched_at = int(time.time())
        with self._lock:
            self._helpers[session_id] = helper
        helper.listener_thread = threading.Thread(
            target=self._accept_helper_connection,
            args=(helper,),
            name=f"borealis-helper-listener-{session_id}",
            daemon=True,
        )
        helper.listener_thread.start()
        ok, detail, pid = self._launch_helper(helper)
        helper.helper_pid = int(pid or 0)
        helper.last_error = "" if ok else detail
        if detail:
            self._log(f"session helper session_id={session_id} {detail}")

    def _launch_helper(self, helper: _HelperState) -> Tuple[bool, str, int]:
        command, cwd = _helper_command(
            session_id=helper.session_id,
            address=helper.address,
            auth_token=helper.auth_token,
        )
        if IS_WINDOWS:
            return _launch_helper_process_as_user_windows(
                session_id=helper.session_id,
                command=command,
                cwd=cwd,
            )
        try:
            process = subprocess.Popen(
                command,
                cwd=cwd,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                start_new_session=True,
            )
            return True, f"launched helper pid={int(process.pid or 0)} session_id={helper.session_id}", int(process.pid or 0)
        except Exception as exc:
            return False, str(exc), 0

    def _accept_helper_connection(self, helper: _HelperState) -> None:
        try:
            assert helper.listener is not None
            connection = helper.listener.accept()
        except Exception as exc:
            helper.last_error = str(exc)
            return
        superseded = False
        with self._lock:
            stored = self._helpers.get(helper.session_id)
            if stored is helper:
                stored.connection = connection
                stored.connected_at = int(time.time())
                stored.last_seen_at = int(time.time())
            else:
                superseded = True
        if superseded:
            with contextlib.suppress(Exception):
                connection.send({"type": "shutdown", "reason": "helper_superseded"})
            with contextlib.suppress(Exception):
                connection.close()
            with contextlib.suppress(Exception):
                if helper.listener is not None:
                    helper.listener.close()
            return
        reader = threading.Thread(
            target=self._helper_reader,
            args=(helper.session_id, connection),
            name=f"borealis-helper-reader-{helper.session_id}",
            daemon=True,
        )
        with self._lock:
            stored = self._helpers.get(helper.session_id)
            if stored is not None:
                stored.reader_thread = reader
        reader.start()

    def _helper_reader(self, session_id: int, connection: Any) -> None:
        while True:
            try:
                message = connection.recv()
            except EOFError:
                break
            except Exception:
                break
            if not isinstance(message, dict):
                continue
            self._handle_helper_message(session_id, message)
        with self._lock:
            helper = self._helpers.get(session_id)
            if helper is not None and helper.connection is connection:
                helper.connection = None
                helper.last_seen_at = 0

    def _handle_helper_message(self, session_id: int, message: Dict[str, Any]) -> None:
        message_type = _clean_text(message.get("type")).lower()
        helper = self._helper_for_session(session_id)
        if helper is None:
            return
        helper.last_seen_at = int(time.time())
        if message_type == "handshake":
            helper.helper_pid = _coerce_int(message.get("pid"), 0)
            helper.last_error = ""
            return
        if message_type == "heartbeat":
            helper.helper_pid = _coerce_int(message.get("pid"), helper.helper_pid)
            return
        if message_type == "quick_job_result":
            payload = message.get("payload") if isinstance(message.get("payload"), dict) else {}
            self._handle_helper_result(session_id, payload)

    def _handle_helper_result(self, session_id: int, payload: Dict[str, Any]) -> None:
        job_id = _coerce_int(payload.get("job_id"), 0)
        if job_id <= 0:
            return
        with self._lock:
            pending = self._pending_jobs.get(job_id)
        session_meta = {}
        if pending is not None:
            session_meta = pending.expected_sessions.get(session_id) or {}
        result_payload = {
            "job_id": job_id,
            "status": _clean_text(payload.get("status") or "Failed") or "Failed",
            "stdout": _clean_text(payload.get("stdout")),
            "stderr": _clean_text(payload.get("stderr")),
            "context": dict(payload.get("context") or {}),
        }
        if session_meta:
            result_payload["context"].update(
                {
                    "session_id": session_id,
                    "session_username": _clean_text(session_meta.get("username")),
                    "session_name": _clean_text(session_meta.get("session_name")),
                }
            )
        if pending is None:
            self._emit_quick_job_result(result_payload)
            return
        with self._lock:
            pending.results[session_id] = result_payload
            completed = len(pending.results) >= len(pending.expected_sessions)
            if completed:
                self._pending_jobs.pop(job_id, None)
        if not completed:
            return
        if len(pending.expected_sessions) == 1:
            self._emit_quick_job_result(next(iter(pending.results.values())))
            return
        self._emit_quick_job_result(self._aggregate_pending_results(pending))

    def _aggregate_pending_results(self, pending: _PendingJob) -> Dict[str, Any]:
        stdout_parts: List[str] = []
        stderr_parts: List[str] = []
        session_results: List[Dict[str, Any]] = []
        overall_success = True
        for session_id in sorted(pending.results.keys()):
            result = pending.results[session_id]
            session_meta = pending.expected_sessions.get(session_id) or {}
            username = _clean_text(session_meta.get("username")) or f"session-{session_id}"
            header = f"[Session {session_id} | {username}]"
            stdout_text = _clean_text(result.get("stdout"))
            stderr_text = _clean_text(result.get("stderr"))
            if stdout_text:
                stdout_parts.append(f"{header}\n{stdout_text}")
            if stderr_text:
                stderr_parts.append(f"{header}\n{stderr_text}")
            if _clean_text(result.get("status")).lower() != "success":
                overall_success = False
            session_results.append(
                {
                    "session_id": session_id,
                    "username": username,
                    "status": _clean_text(result.get("status") or "Failed") or "Failed",
                    "stdout": stdout_text,
                    "stderr": stderr_text,
                }
            )
        context = dict(pending.context or {})
        context["session_results"] = session_results
        context["session_target"] = SESSION_TARGET_ALL
        return {
            "job_id": pending.job_id,
            "status": "Success" if overall_success else "Failed",
            "stdout": "\n\n".join(stdout_parts),
            "stderr": "\n\n".join(stderr_parts),
            "context": context,
        }

    def _send_helper_message(self, helper: _HelperState, message: Dict[str, Any]) -> bool:
        try:
            if helper.connection is None:
                return False
            helper.connection.send(message)
            return True
        except Exception as exc:
            helper.last_error = str(exc)
            return False

    def _stop_helper(self, session_id: int, *, reason: str) -> None:
        with self._lock:
            helper = self._helpers.pop(int(session_id), None)
        if helper is None:
            return
        try:
            if helper.connection is not None:
                with contextlib.suppress(Exception):
                    helper.connection.send({"type": "shutdown", "reason": reason})
        finally:
            with contextlib.suppress(Exception):
                if helper.connection is not None:
                    helper.connection.close()
        with contextlib.suppress(Exception):
            if helper.listener is not None:
                helper.listener.close()
        if helper.connection is None and int(helper.helper_pid or 0) > 0:
            with contextlib.suppress(Exception):
                os.kill(int(helper.helper_pid), signal.SIGTERM)
        if not IS_WINDOWS:
            with contextlib.suppress(Exception):
                if os.path.exists(helper.address):
                    os.unlink(helper.address)


class SessionHelperClient:
    def __init__(
        self,
        *,
        loop,
        address: str,
        auth_token: str,
        session_id: int,
        log: Callable[[str], None],
        shutdown_callback: Optional[Callable[[], None]] = None,
    ) -> None:
        self._loop = loop
        self._address = address
        self._auth_token = auth_token.encode("utf-8")
        self._session_id = int(session_id)
        self._log = log
        self._shutdown_callback = shutdown_callback
        self._handler: Optional[Callable[[Dict[str, Any]], Any]] = None
        self._thread: Optional[threading.Thread] = None
        self._stop = threading.Event()

    def register_handler(self, handler: Callable[[Dict[str, Any]], Any]) -> None:
        self._handler = handler

    def start(self) -> None:
        if self._thread is not None:
            return
        self._thread = threading.Thread(target=self._run, name=f"borealis-helper-client-{self._session_id}", daemon=True)
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()

    def _run(self) -> None:
        try:
            connection = Client(self._address, family=_connection_family(), authkey=self._auth_token)
        except Exception as exc:
            self._log(f"session helper connect failed session_id={self._session_id} error={exc}")
            return
        try:
            connection.send({"type": "handshake", "session_id": self._session_id, "pid": os.getpid(), "hostname": socket.gethostname()})
            while not self._stop.is_set():
                if connection.poll(10.0):
                    try:
                        message = connection.recv()
                    except EOFError:
                        break
                    if not isinstance(message, dict):
                        continue
                    message_type = _clean_text(message.get("type")).lower()
                    if message_type == "quick_job_run":
                        payload = message.get("payload") if isinstance(message.get("payload"), dict) else {}
                        result = self._execute_payload(payload)
                        if result is not None:
                            connection.send({"type": "quick_job_result", "payload": result})
                    elif message_type == "shutdown":
                        if callable(self._shutdown_callback):
                            try:
                                self._shutdown_callback()
                            except Exception:
                                pass
                        break
                else:
                    connection.send({"type": "heartbeat", "session_id": self._session_id, "pid": os.getpid()})
        except Exception as exc:
            self._log(f"session helper loop failed session_id={self._session_id} error={exc}")
        finally:
            with contextlib.suppress(Exception):
                connection.close()

    def _execute_payload(self, payload: Dict[str, Any]) -> Optional[Dict[str, Any]]:
        handler = self._handler
        if not callable(handler):
            return {
                "job_id": _coerce_int(payload.get("job_id"), 0),
                "status": "Failed",
                "stdout": "",
                "stderr": "helper_handler_not_ready",
                "context": dict(payload.get("context") or {}),
            }
        try:
            future = asyncio.run_coroutine_threadsafe(handler(payload), self._loop)
            return future.result()
        except Exception as exc:
            return {
                "job_id": _coerce_int(payload.get("job_id"), 0),
                "status": "Failed",
                "stdout": "",
                "stderr": str(exc),
                "context": dict(payload.get("context") or {}),
            }
