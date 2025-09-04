import os
import sys
import time
import subprocess
import threading
import datetime
import json
import ctypes
from ctypes import wintypes

# Optional pywin32 imports for per-session launching
try:
    import win32ts
    import win32con
    import win32process
    import win32security
    import win32profile
    import win32api
    import pywintypes
except Exception:
    win32ts = None


ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), os.pardir, os.pardir))
AGENT_DIR = os.path.join(ROOT, 'Agent')
BOREALIS_DIR = os.path.join(AGENT_DIR, 'Borealis')
LOG_DIR = os.path.join(ROOT, 'Logs', 'Agent')
os.makedirs(LOG_DIR, exist_ok=True)
LOG_FILE = os.path.join(LOG_DIR, 'Supervisor.log')
PID_FILE = os.path.join(LOG_DIR, 'script_agent.pid')

# Internal state for process + backoff
_script_proc = None
_spawn_backoff = 5  # seconds (exponential backoff start)
_max_backoff = 300  # cap at 5 minutes
_next_spawn_time = 0.0
_last_disable_log = 0.0
_last_fail_log = 0.0


def log(msg: str):
    try:
        # simple size-based rotation (~1MB)
        try:
            if os.path.isfile(LOG_FILE) and os.path.getsize(LOG_FILE) > 1_000_000:
                bak = LOG_FILE + '.1'
                try:
                    if os.path.isfile(bak):
                        os.remove(bak)
                except Exception:
                    pass
                try:
                    os.replace(LOG_FILE, bak)
                except Exception:
                    pass
        except Exception:
            pass

        ts = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')
        with open(LOG_FILE, 'a', encoding='utf-8') as f:
            f.write(f"[{ts}] {msg}\n")
    except Exception:
        pass


def venv_python():
    try:
        exe_dir = os.path.join(AGENT_DIR, 'Scripts')
        py = os.path.join(exe_dir, 'python.exe')
        if os.path.isfile(py):
            return py
    except Exception:
        pass
    return sys.executable


def venv_pythonw():
    try:
        exe_dir = os.path.join(AGENT_DIR, 'Scripts')
        pyw = os.path.join(exe_dir, 'pythonw.exe')
        if os.path.isfile(pyw):
            return pyw
    except Exception:
        pass
    return venv_python()


def _settings_path():
    return os.path.join(ROOT, 'agent_settings.json')


def load_settings():
    cfg = {}
    try:
        path = _settings_path()
        if os.path.isfile(path):
            with open(path, 'r', encoding='utf-8') as f:
                cfg = json.load(f)
    except Exception:
        cfg = {}
    return cfg or {}


def _psutil_process_exists(pid: int) -> bool:
    try:
        import psutil  # type: ignore
        if pid <= 0:
            return False
        p = psutil.Process(pid)
        return p.is_running() and (p.status() != psutil.STATUS_ZOMBIE)
    except Exception:
        return False


def _win_process_exists(pid: int) -> bool:
    try:
        if pid <= 0:
            return False
        PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
        kernel32 = ctypes.WinDLL('kernel32', use_last_error=True)
        OpenProcess = kernel32.OpenProcess
        OpenProcess.restype = wintypes.HANDLE
        OpenProcess.argtypes = (wintypes.DWORD, wintypes.BOOL, wintypes.DWORD)
        CloseHandle = kernel32.CloseHandle
        CloseHandle.argtypes = (wintypes.HANDLE,)
        h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, False, pid)
        if h:
            try:
                CloseHandle(h)
            except Exception:
                pass
            return True
        return False
    except Exception:
        return False


def process_exists(pid: int) -> bool:
    # Prefer psutil if available; else Win32 API
    return _psutil_process_exists(pid) or _win_process_exists(pid)


def _read_pid_file() -> int:
    try:
        if os.path.isfile(PID_FILE):
            with open(PID_FILE, 'r', encoding='utf-8') as f:
                s = f.read().strip()
                return int(s)
    except Exception:
        pass
    return 0


def _write_pid_file(pid: int):
    try:
        with open(PID_FILE, 'w', encoding='utf-8') as f:
            f.write(str(pid))
    except Exception:
        pass


def _clear_pid_file():
    try:
        if os.path.isfile(PID_FILE):
            os.remove(PID_FILE)
    except Exception:
        pass


def ensure_script_agent():
    """Ensure LocalSystem script_agent.py is running; restart if not, with backoff and PID tracking."""
    global _script_proc, _spawn_backoff, _next_spawn_time, _last_disable_log, _last_fail_log

    # Allow disabling via config
    try:
        cfg = load_settings()
        if not cfg.get('enable_system_script_agent', True):
            now = time.time()
            if now - _last_disable_log > 60:
                log('System script agent disabled by config (enable_system_script_agent=false)')
                _last_disable_log = now
            return
    except Exception:
        pass

    # If we have a running child process, keep it
    try:
        if _script_proc is not None:
            if _script_proc.poll() is None:
                return
            else:
                # Child exited; clear PID file for safety
                _clear_pid_file()
                _script_proc = None
    except Exception:
        pass

    # If PID file points to a living process, don't spawn
    try:
        pid = _read_pid_file()
        if pid and process_exists(pid):
            return
        elif pid and not process_exists(pid):
            _clear_pid_file()
    except Exception:
        pass

    # Honor backoff window
    if time.time() < _next_spawn_time:
        return

    py = venv_python()
    script = os.path.join(ROOT, 'Data', 'Agent', 'script_agent.py')
    try:
        proc = subprocess.Popen(
            [py, '-W', 'ignore::SyntaxWarning', script],
            creationflags=(0x08000000 if os.name == 'nt' else 0),
        )
        _script_proc = proc
        _write_pid_file(proc.pid)
        log(f'Launched script_agent.py (pid {proc.pid})')
        # reset backoff on success
        _spawn_backoff = 5
        _next_spawn_time = 0.0
    except Exception as e:
        msg = f'Failed to launch script_agent.py: {e}'
        now = time.time()
        # rate-limit identical failure logs to once per 10s
        if now - _last_fail_log > 10:
            log(msg)
            _last_fail_log = now
        # exponential backoff
        _spawn_backoff = min(_spawn_backoff * 2, _max_backoff)
        _next_spawn_time = time.time() + _spawn_backoff


def _enable_privileges():
    try:
        hProc = win32api.GetCurrentProcess()
        hTok = win32security.OpenProcessToken(hProc, win32con.TOKEN_ADJUST_PRIVILEGES | win32con.TOKEN_QUERY)
        for name in [
            win32security.SE_ASSIGNPRIMARYTOKEN_NAME,
            win32security.SE_INCREASE_QUOTA_NAME,
            win32security.SE_TCB_NAME,
            win32security.SE_BACKUP_NAME,
            win32security.SE_RESTORE_NAME,
        ]:
            try:
                luid = win32security.LookupPrivilegeValue(None, name)
                win32security.AdjustTokenPrivileges(hTok, False, [(luid, win32con.SE_PRIVILEGE_ENABLED)])
            except Exception:
                pass
    except Exception:
        pass


def active_sessions():
    ids = []
    try:
        if win32ts is None:
            return ids
        for s in win32ts.WTSEnumerateSessions(None, 1, 0):
            sid, _, state = s
            if state == win32ts.WTSActive:
                ids.append(sid)
    except Exception:
        pass
    return ids


def launch_helper_in_session(session_id):
    try:
        if win32ts is None:
            return False
        _enable_privileges()
        hUser = win32ts.WTSQueryUserToken(session_id)
        primary = win32security.DuplicateTokenEx(
            hUser,
            win32con.MAXIMUM_ALLOWED,
            win32security.SECURITY_ATTRIBUTES(),
            win32security.SecurityImpersonation,
            win32con.TOKEN_PRIMARY,
        )
        env = win32profile.CreateEnvironmentBlock(primary, True)
        si = win32process.STARTUPINFO()
        si.lpDesktop = 'winsta0\\default'
        cmd = f'"{venv_pythonw()}" -W ignore::SyntaxWarning "{os.path.join(BOREALIS_DIR, "borealis-agent.py")}"'
        flags = getattr(win32con, 'CREATE_UNICODE_ENVIRONMENT', 0x00000400)
        win32process.CreateProcessAsUser(primary, None, cmd, None, None, False, flags, env, BOREALIS_DIR, si)
        log(f'Started user helper in session {session_id}')
        return True
    except Exception as e:
        log(f'Failed to start helper in session {session_id}: {e}')
        return False


def manage_user_helpers_loop():
    known = set()
    while True:
        try:
            cur = set(active_sessions())
            for sid in cur:
                if sid not in known:
                    launch_helper_in_session(sid)
            known = cur
        except Exception:
            pass
        time.sleep(3)


def main():
    log('Supervisor starting')
    t = threading.Thread(target=manage_user_helpers_loop, daemon=True)
    t.start()
    while True:
        ensure_script_agent()
        time.sleep(5)


if __name__ == '__main__':
    main()
