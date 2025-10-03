import os
import sys
import re
import asyncio
import tempfile
import uuid
from typing import Dict, List
from PyQt5 import QtWidgets, QtGui


ROLE_NAME = 'script_exec_currentuser'
ROLE_CONTEXTS = ['interactive']


IS_WINDOWS = os.name == 'nt'


def _sanitize_env_map(raw) -> Dict[str, str]:
    env: Dict[str, str] = {}
    if isinstance(raw, dict):
        for key, value in raw.items():
            if key is None:
                continue
            name = str(key).strip()
            if not name:
                continue
            env_key = re.sub(r"[^A-Za-z0-9_]", "_", name).upper()
            if not env_key:
                continue
            if isinstance(value, bool):
                env_val = "True" if value else "False"
            elif value is None:
                env_val = ""
            else:
                env_val = str(value)
            env[env_key] = env_val
    return env


def _ps_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _build_wrapped_script(content: str, env_map: Dict[str, str], timeout_seconds: int) -> str:
    inner_lines: List[str] = []
    for key, value in (env_map or {}).items():
        if not key:
            continue
        inner_lines.append(f"$Env:{key} = {_ps_literal(value)}")
    inner_lines.append(content or "")
    inner = "\n".join(line for line in inner_lines if line is not None)
    script_block = "$__BorealisScript = {\n" + inner + "\n}\n"
    if timeout_seconds and timeout_seconds > 0:
        block = (
            "$job = Start-Job -ScriptBlock $__BorealisScript\n"
            f"if (Wait-Job -Job $job -Timeout {timeout_seconds}) {{\n"
            "  Receive-Job $job\n"
            "} else {\n"
            "  Stop-Job $job -Force\n"
            f"  throw \"Script timed out after {timeout_seconds} seconds\"\n"
            "}\n"
        )
        return script_block + block
    return script_block + "& $__BorealisScript\n"


def _write_temp_script(content: str, suffix: str, env_map: Dict[str, str], timeout_seconds: int):
    temp_dir = os.path.join(tempfile.gettempdir(), "Borealis", "quick_jobs")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="bj_", suffix=suffix, dir=temp_dir, text=True)
    final_content = _build_wrapped_script(content or "", env_map, timeout_seconds)
    with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as fh:
        fh.write(final_content)
    return path


async def _run_powershell_local(path: str):
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
            creationflags=(0x08000000 if IS_WINDOWS else 0)
        )
        out_b, err_b = await proc.communicate()
        return proc.returncode, (out_b or b"").decode(errors='replace'), (err_b or b"").decode(errors='replace')
    except Exception as e:
        return -1, "", str(e)


async def _run_powershell_via_user_task(content: str, env_map: Dict[str, str], timeout_seconds: int):
    if not IS_WINDOWS:
        return -999, '', 'Windows only'
    ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
    if not os.path.isfile(ps):
        ps = 'powershell.exe'
    path = None
    out_path = None
    import tempfile as _tf
    try:
        temp_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..', 'Temp'))
        os.makedirs(temp_dir, exist_ok=True)
        fd, path = _tf.mkstemp(prefix='usr_task_', suffix='.ps1', dir=temp_dir, text=True)
        final_content = _build_wrapped_script(content or '', env_map, timeout_seconds)
        with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as f:
            f.write(final_content)
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
        proc = await asyncio.create_subprocess_exec(ps, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', task_ps,
                                                    stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
        out_b, err_b = await proc.communicate()
        if proc.returncode != 0:
            return -999, '', (err_b or out_b or b'').decode(errors='replace')
        # Wait a short time for output file; best-effort
        import time as _t
        deadline = _t.time() + (timeout_seconds if timeout_seconds > 0 else 30)
        out_data = ''
        while _t.time() < deadline:
            try:
                if os.path.isfile(out_path) and os.path.getsize(out_path) > 0:
                    with open(out_path, 'r', encoding='utf-8', errors='replace') as f:
                        out_data = f.read()
                    break
            except Exception:
                pass
            await asyncio.sleep(1)
        # Cleanup best-effort
        try:
            await asyncio.create_subprocess_exec('powershell.exe', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', f"try {{ Unregister-ScheduledTask -TaskName '{name}' -Confirm:$false }} catch {{}}")
        except Exception:
            pass
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
        return 0, out_data or '', ''
    except Exception as e:
        return -999, '', str(e)


class Role:
    def __init__(self, ctx):
        self.ctx = ctx
        # Setup tray icon in interactive session
        try:
            self._setup_tray()
        except Exception:
            pass

    def register_events(self):
        sio = self.ctx.sio

        @sio.on('quick_job_run')
        async def _on_quick_job_run(payload):
            try:
                import socket
                hostname = socket.gethostname()
                target = (payload.get('target_hostname') or '').strip().lower()
                if not target or target != hostname.lower():
                    return
                job_id = payload.get('job_id')
                script_type = (payload.get('script_type') or '').lower()
                run_mode = (payload.get('run_mode') or 'current_user').lower()
                content = payload.get('script_content') or ''
                raw_env = payload.get('environment')
                env_map = _sanitize_env_map(raw_env)
                variables = payload.get('variables') if isinstance(payload.get('variables'), list) else []
                for var in variables:
                    if not isinstance(var, dict):
                        continue
                    name = str(var.get('name') or '').strip()
                    if not name:
                        continue
                    key = re.sub(r"[^A-Za-z0-9_]", "_", name).upper()
                    if key in env_map:
                        continue
                    default_val = var.get('default')
                    if isinstance(default_val, bool):
                        env_map[key] = "True" if default_val else "False"
                    elif default_val is None:
                        env_map[key] = ""
                    else:
                        env_map[key] = str(default_val)
                try:
                    timeout_seconds = max(0, int(payload.get('timeout_seconds') or 0))
                except Exception:
                    timeout_seconds = 0
                if run_mode == 'system':
                    return
                if script_type != 'powershell':
                    await sio.emit('quick_job_result', { 'job_id': job_id, 'status': 'Failed', 'stdout': '', 'stderr': f"Unsupported type: {script_type}" })
                    return
                if run_mode == 'admin':
                    rc, out, err = -1, '', 'Admin credentialed runs are disabled; use SYSTEM or Current User.'
                else:
                    rc, out, err = await _run_powershell_via_user_task(content, env_map, timeout_seconds)
                    if rc == -999:
                        path = _write_temp_script(content, '.ps1', env_map, timeout_seconds)
                        try:
                            rc, out, err = await _run_powershell_local(path)
                        finally:
                            try:
                                if path and os.path.isfile(path):
                                    os.remove(path)
                            except Exception:
                                pass
                status = 'Success' if rc == 0 else 'Failed'
                await sio.emit('quick_job_result', {
                    'job_id': job_id,
                    'status': status,
                    'stdout': out,
                    'stderr': err,
                })
            except Exception as e:
                try:
                    await sio.emit('quick_job_result', {
                        'job_id': payload.get('job_id') if isinstance(payload, dict) else None,
                        'status': 'Failed',
                        'stdout': '',
                        'stderr': str(e),
                    })
                except Exception:
                    pass

    def _setup_tray(self):
        app = QtWidgets.QApplication.instance()
        if app is None:
            return
        icon = None
        try:
            icon_path = os.path.abspath(os.path.join(os.path.dirname(__file__), os.pardir, 'Borealis.ico'))
            if os.path.isfile(icon_path):
                icon = QtGui.QIcon(icon_path)
        except Exception:
            pass
        if icon is None:
            icon = app.style().standardIcon(QtWidgets.QStyle.SP_ComputerIcon)
        self.tray = QtWidgets.QSystemTrayIcon(icon)
        self.tray.setToolTip('Borealis Agent')
        menu = QtWidgets.QMenu()
        act_restart = menu.addAction('Restart Agent')
        act_quit = menu.addAction('Quit Agent')
        act_restart.triggered.connect(self._restart_agent)
        act_quit.triggered.connect(self._quit_agent)
        self.tray.setContextMenu(menu)
        self.tray.show()

    def _restart_agent(self):
        try:
            # __file__ => Agent/Borealis/Roles/...
            borealis_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), os.pardir))
            venv_root = os.path.abspath(os.path.join(borealis_dir, os.pardir))
            venv_scripts = os.path.join(venv_root, 'Scripts')
            pyw = os.path.join(venv_scripts, 'pythonw.exe')
            exe = pyw if os.path.isfile(pyw) else sys.executable
            agent_script = os.path.join(borealis_dir, 'agent.py')
            import subprocess
            subprocess.Popen([exe, '-W', 'ignore::SyntaxWarning', agent_script], cwd=borealis_dir)
        except Exception:
            pass
        try:
            QtWidgets.QApplication.instance().quit()
        except Exception:
            os._exit(0)

    def _quit_agent(self):
        try:
            QtWidgets.QApplication.instance().quit()
        except Exception:
            os._exit(0)
