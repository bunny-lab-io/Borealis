import win32serviceutil
import win32service
import win32event
import servicemanager
import subprocess
import os
import sys
import datetime
import threading

# Session/process helpers for per-user helper launch
try:
    import win32ts
    import win32con
    import win32process
    import win32security
    import win32profile
    import win32api
except Exception:
    win32ts = None


class BorealisAgentService(win32serviceutil.ServiceFramework):
    _svc_name_ = "BorealisAgent"
    _svc_display_name_ = "Borealis Agent"
    _svc_description_ = "Background agent for data collection and remote script execution."

    def __init__(self, args):
        win32serviceutil.ServiceFramework.__init__(self, args)
        self.hWaitStop = win32event.CreateEvent(None, 0, 0, None)
        self.proc = None
        self.user_helpers = {}
        self.helpers_thread = None
        try:
            self._log("Service initialized")
        except Exception:
            pass

    def SvcStop(self):
        self.ReportServiceStatus(win32service.SERVICE_STOP_PENDING)
        try:
            self._log("Stop requested")
        except Exception:
            pass
        try:
            if self.proc and self.proc.poll() is None:
                try:
                    self.proc.terminate()
                except Exception:
                    pass
        except Exception:
            pass
        # Stop user helpers
        try:
            for sid, h in list(self.user_helpers.items()):
                try:
                    hp = h.get('hProcess')
                    if hp:
                        win32api.TerminateProcess(hp, 0)
                except Exception:
                    pass
        except Exception:
            pass
        win32event.SetEvent(self.hWaitStop)

    def SvcDoRun(self):
        try:
            servicemanager.LogMsg(servicemanager.EVENTLOG_INFORMATION_TYPE,
                                  servicemanager.PYS_SERVICE_STARTED,
                                  (self._svc_name_, ''))
        except Exception:
            pass
        try:
            self._log("SvcDoRun entered")
        except Exception:
            pass
        # Mark the service as running once initialized
        try:
            self.ReportServiceStatus(win32service.SERVICE_RUNNING)
        except Exception:
            pass
        self.main()

    def main(self):
        script_dir = os.path.dirname(os.path.abspath(__file__))
        agent_py = os.path.join(script_dir, 'script_agent.py')

        def _venv_python():
            try:
                exe_dir = os.path.dirname(sys.executable)
                py = os.path.join(exe_dir, 'python.exe')
                if os.path.isfile(py):
                    return py
            except Exception:
                pass
            return sys.executable

        def _start_script_agent():
            python = _venv_python()
            try:
                self._log(f"Launching script_agent via {python}")
                return subprocess.Popen(
                    [python, '-W', 'ignore::SyntaxWarning', agent_py],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    creationflags=0x08000000 if os.name == 'nt' else 0
                )
            except Exception as e:
                try:
                    self._log(f"Failed to start script_agent: {e}")
                except Exception:
                    pass
                return None

        # Launch the system script agent (and keep it alive)
        self.proc = _start_script_agent()

        # Start per-user helper manager in a background thread
        if win32ts is not None:
            try:
                self.helpers_thread = threading.Thread(target=self._manage_user_helpers_loop, daemon=True)
                self.helpers_thread.start()
            except Exception:
                try:
                    self._log("Failed to start user helper manager thread")
                except Exception:
                    pass

        # Monitor stop event and child process; restart child if it exits
        while True:
            rc = win32event.WaitForSingleObject(self.hWaitStop, 2000)
            if rc == win32event.WAIT_OBJECT_0:
                break
            try:
                if not self.proc or (self.proc and self.proc.poll() is not None):
                    # child exited; attempt restart
                    self._log("script_agent exited; attempting restart")
                    self.proc = _start_script_agent()
            except Exception:
                pass

    def _log(self, msg: str):
        try:
            root = os.path.abspath(os.path.join(os.path.dirname(__file__), os.pardir, os.pardir))
            logs = os.path.join(root, 'Logs')
            os.makedirs(logs, exist_ok=True)
            p = os.path.join(logs, 'AgentService_Runtime.log')
            ts = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')
            with open(p, 'a', encoding='utf-8') as f:
                f.write(f"{ts} {msg}\n")
        except Exception:
            pass

    # ---------------------- Per-Session User Helper Management ----------------------
    def _enable_privileges(self):
        try:
            hProc = win32api.GetCurrentProcess()
            hTok = win32security.OpenProcessToken(
                hProc,
                win32con.TOKEN_ADJUST_PRIVILEGES | win32con.TOKEN_QUERY,
            )
            for name in [
                win32security.SE_ASSIGNPRIMARYTOKEN_NAME,
                win32security.SE_INCREASE_QUOTA_NAME,
                win32security.SE_TCB_NAME,
                win32security.SE_BACKUP_NAME,
                win32security.SE_RESTORE_NAME,
            ]:
                try:
                    luid = win32security.LookupPrivilegeValue(None, name)
                    win32security.AdjustTokenPrivileges(
                        hTok, False, [(luid, win32con.SE_PRIVILEGE_ENABLED)]
                    )
                except Exception:
                    pass
        except Exception:
            pass

    def _active_session_ids(self):
        ids = []
        try:
            sessions = win32ts.WTSEnumerateSessions(None, 1, 0)
            for s in sessions:
                # tuple: (SessionId, WinStationName, State)
                sess_id, _, state = s
                if state == win32ts.WTSActive:
                    ids.append(sess_id)
        except Exception:
            pass
        return ids

    def _launch_helper_in_session(self, session_id):
        try:
            self._enable_privileges()
            # User token for session
            hUser = win32ts.WTSQueryUserToken(session_id)
            # Duplicate to primary
            primary = win32security.DuplicateTokenEx(
                hUser,
                win32con.MAXIMUM_ALLOWED,
                win32security.SECURITY_ATTRIBUTES(),
                win32security.SecurityImpersonation,
                win32con.TOKEN_PRIMARY,
            )
            env = win32profile.CreateEnvironmentBlock(primary, True)
            startup = win32process.STARTUPINFO()
            startup.lpDesktop = "winsta0\\default"

            # Compute pythonw + helper script
            venv_dir = os.path.dirname(sys.executable)  # e.g., Agent
            pyw = os.path.join(venv_dir, 'pythonw.exe')
            agent_dir = os.path.dirname(os.path.abspath(__file__))
            helper = os.path.join(agent_dir, 'borealis-agent.py')
            cmd = f'"{pyw}" -W ignore::SyntaxWarning "{helper}"'

            flags = getattr(win32con, 'CREATE_NEW_PROCESS_GROUP', 0)
            flags |= getattr(win32con, 'CREATE_UNICODE_ENVIRONMENT', 0x00000400)
            proc_tuple = win32process.CreateProcessAsUser(
                primary,
                None,
                cmd,
                None,
                None,
                False,
                flags,
                env,
                agent_dir,
                startup,
            )
            # proc_tuple: (hProcess, hThread, dwProcessId, dwThreadId)
            self.user_helpers[session_id] = {
                'hProcess': proc_tuple[0],
                'hThread': proc_tuple[1],
                'pid': proc_tuple[2],
                'started': datetime.datetime.now(),
            }
            self._log(f"Started user helper in session {session_id}")
            return True
        except Exception as e:
            try:
                self._log(f"Failed to start helper in session {session_id}: {e}")
            except Exception:
                pass
            return False

    def _manage_user_helpers_loop(self):
        # Periodically ensure one user helper per active session
        while True:
            rc = win32event.WaitForSingleObject(self.hWaitStop, 2000)
            if rc == win32event.WAIT_OBJECT_0:
                break
            try:
                active = set(self._active_session_ids())
                # Start missing
                for sid in active:
                    if sid not in self.user_helpers:
                        self._launch_helper_in_session(sid)
                # Cleanup ended
                for sid in list(self.user_helpers.keys()):
                    if sid not in active:
                        info = self.user_helpers.pop(sid, None)
                        try:
                            hp = info and info.get('hProcess')
                            if hp:
                                win32api.TerminateProcess(hp, 0)
                        except Exception:
                            pass
                        self._log(f"Cleaned helper for session {sid}")
            except Exception:
                pass


if __name__ == '__main__':
    win32serviceutil.HandleCommandLine(BorealisAgentService)
