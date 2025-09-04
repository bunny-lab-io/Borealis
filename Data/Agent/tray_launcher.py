import os
import sys
import subprocess
import signal
from PyQt5 import QtWidgets, QtGui


def project_paths():
    # Expected layout when running from venv: <Root>\Agent\Borealis
    borealis_dir = os.path.dirname(os.path.abspath(__file__))
    agent_dir = os.path.abspath(os.path.join(borealis_dir, os.pardir))
    venv_scripts = os.path.join(agent_dir, 'Scripts')
    pyw = os.path.join(venv_scripts, 'pythonw.exe')
    py = os.path.join(venv_scripts, 'python.exe')
    icon_path = os.path.join(borealis_dir, 'Borealis.ico')
    agent_script = os.path.join(borealis_dir, 'borealis-agent.py')
    return {
        'borealis_dir': borealis_dir,
        'venv_scripts': venv_scripts,
        'pythonw': pyw if os.path.isfile(pyw) else sys.executable,
        'python': py if os.path.isfile(py) else sys.executable,
        'agent_script': agent_script,
        'icon': icon_path if os.path.isfile(icon_path) else None,
    }


class TrayApp(QtWidgets.QSystemTrayIcon):
    def __init__(self, app):
        self.app = app
        paths = project_paths()
        self.paths = paths
        icon = QtGui.QIcon(paths['icon']) if paths['icon'] else app.style().standardIcon(QtWidgets.QStyle.SP_ComputerIcon)
        super().__init__(icon)
        self.setToolTip('Borealis Agent')
        self.menu = QtWidgets.QMenu()

        self.action_show_console = self.menu.addAction('Switch to Foreground Mode')
        self.action_hide_console = self.menu.addAction('Switch to Background Mode')
        self.action_restart = self.menu.addAction('Restart Agent')
        self.action_restart_service = self.menu.addAction('Restart Borealis Agent Service')
        self.menu.addSeparator()
        self.action_quit = self.menu.addAction('Quit Agent and Tray')

        self.action_show_console.triggered.connect(self.switch_to_console)
        self.action_hide_console.triggered.connect(self.switch_to_background)
        self.action_restart.triggered.connect(self.restart_agent)
        self.action_restart_service.triggered.connect(self.restart_script_service)
        self.action_quit.triggered.connect(self.quit_all)
        self.setContextMenu(self.menu)

        self.proc = None
        self.console_mode = False
        # Start in background mode by default
        self.switch_to_background()
        self.show()

    def _start_agent(self, console=False):
        self._stop_agent()
        exe = self.paths['python'] if console else self.paths['pythonw']
        args = [exe, '-W', 'ignore::SyntaxWarning', self.paths['agent_script']]
        creationflags = 0
        if not console and os.name == 'nt':
            # CREATE_NO_WINDOW
            creationflags = 0x08000000
        try:
            self.proc = subprocess.Popen(args, cwd=self.paths['borealis_dir'], creationflags=creationflags)
            self.console_mode = console
            self._update_actions(console)
        except Exception:
            self.proc = None

    def _stop_agent(self):
        if self.proc is not None:
            try:
                if os.name == 'nt':
                    self.proc.send_signal(signal.SIGTERM)
                else:
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
        self.proc = None

    def _update_actions(self, console):
        self.action_show_console.setEnabled(not console)
        self.action_hide_console.setEnabled(console)

    def switch_to_console(self):
        self._start_agent(console=True)

    def switch_to_background(self):
        self._start_agent(console=False)

    def restart_agent(self):
        # Restart using current mode
        self._start_agent(console=self.console_mode)

    def restart_script_service(self):
        # Try direct stop/start; if fails (likely due to permissions), attempt elevation via PowerShell
        service_name = 'BorealisAgent'
        try:
            # Stop service
            subprocess.run(["sc.exe", "stop", service_name], check=False, capture_output=True)
            # Start service
            subprocess.run(["sc.exe", "start", service_name], check=False, capture_output=True)
            return
        except Exception:
            pass

        # Fallback: elevate via PowerShell
        try:
            script = (
                f"$ErrorActionPreference='Continue'; "
                f"try {{ Stop-Service -Name '{service_name}' -Force -ErrorAction SilentlyContinue }} catch {{}}; "
                f"Start-Sleep -Seconds 1; "
                f"try {{ Start-Service -Name '{service_name}' }} catch {{}};"
            )
            # Start-Process PowerShell -Verb RunAs to elevate
            ps_cmd = [
                'powershell.exe', '-NoProfile', '-ExecutionPolicy', 'Bypass',
                '-Command',
                "Start-Process PowerShell -Verb RunAs -ArgumentList '-NoProfile -ExecutionPolicy Bypass -Command \"" + script.replace("\"", "\\\"") + "\"'"
            ]
            subprocess.Popen(ps_cmd)
        except Exception:
            pass

    def quit_all(self):
        self._stop_agent()
        self.hide()
        self.app.quit()


def main():
    app = QtWidgets.QApplication(sys.argv)
    tray = TrayApp(app)
    return app.exec_()


if __name__ == '__main__':
    sys.exit(main())
