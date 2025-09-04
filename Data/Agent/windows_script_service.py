import win32serviceutil
import win32service
import win32event
import servicemanager
import subprocess
import os
import sys
import datetime


class BorealisScriptAgentService(win32serviceutil.ServiceFramework):
    _svc_name_ = "BorealisScriptService"
    _svc_display_name_ = "Borealis Script Execution Service"
    _svc_description_ = "Executes automation scripts (PowerShell, etc.) as LocalSystem and bridges to Borealis Server."

    def __init__(self, args):
        win32serviceutil.ServiceFramework.__init__(self, args)
        self.hWaitStop = win32event.CreateEvent(None, 0, 0, None)
        self.proc = None
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
        python = sys.executable
        try:
            self._log(f"Launching script_agent via {python}")
            self.proc = subprocess.Popen(
                [python, '-W', 'ignore::SyntaxWarning', agent_py],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                creationflags=0x08000000 if os.name == 'nt' else 0
            )
            self._log("script_agent started")
        except Exception:
            self.proc = None
            try:
                self._log("Failed to start script_agent")
            except Exception:
                pass

        # Wait until stop or child exits
        while True:
            rc = win32event.WaitForSingleObject(self.hWaitStop, 1000)
            if rc == win32event.WAIT_OBJECT_0:
                break
            if self.proc and self.proc.poll() is not None:
                break

    def _log(self, msg: str):
        try:
            root = os.path.abspath(os.path.join(os.path.dirname(__file__), os.pardir, os.pardir))
            logs = os.path.join(root, 'Logs')
            os.makedirs(logs, exist_ok=True)
            p = os.path.join(logs, 'ScriptService_Runtime.log')
            ts = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')
            with open(p, 'a', encoding='utf-8') as f:
                f.write(f"{ts} {msg}\n")
        except Exception:
            pass


if __name__ == '__main__':
    win32serviceutil.HandleCommandLine(BorealisScriptAgentService)
