import os
import sys
import time
import subprocess
import threading
import datetime

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


def log(msg: str):
    try:
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


def ensure_script_agent():
    """Ensure LocalSystem script_agent.py is running; restart if not."""
    try:
        # best-effort: avoid duplicate spawns
        import psutil  # type: ignore
        for p in psutil.process_iter(['name', 'cmdline']):
            try:
                cl = (p.info.get('cmdline') or [])
                if any('script_agent.py' in (part or '') for part in cl):
                    return
            except Exception:
                pass
    except Exception:
        pass

    py = venv_python()
    script = os.path.join(ROOT, 'Data', 'Agent', 'script_agent.py')
    try:
        subprocess.Popen([py, '-W', 'ignore::SyntaxWarning', script], creationflags=(0x08000000 if os.name == 'nt' else 0))
        log('Launched script_agent.py')
    except Exception as e:
        log(f'Failed to launch script_agent.py: {e}')


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

