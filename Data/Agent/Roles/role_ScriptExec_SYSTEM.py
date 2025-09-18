import os
import asyncio
import tempfile
import uuid
import time
import subprocess


ROLE_NAME = 'script_exec_system'
ROLE_CONTEXTS = ['system']


def _project_root():
    return os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..'))


def _run_powershell_script_content(content: str):
    temp_dir = os.path.join(_project_root(), "Temp")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="sj_", suffix=".ps1", dir=temp_dir, text=True)
    with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as fh:
        fh.write(content or "")

    ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
    if not os.path.isfile(ps):
        ps = "powershell.exe"
    try:
        flags = 0x08000000 if os.name == 'nt' else 0
        proc = subprocess.run(
            [ps, "-ExecutionPolicy", "Bypass", "-NoProfile", "-File", path],
            capture_output=True,
            text=True,
            timeout=60*60,
            creationflags=flags,
        )
        return proc.returncode, proc.stdout or "", proc.stderr or ""
    except Exception as e:
        return -1, "", str(e)
    finally:
        try:
            if os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass


def _run_powershell_via_system_task(content: str):
    ps_exe = os.path.expandvars(r"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe")
    if not os.path.isfile(ps_exe):
        ps_exe = 'powershell.exe'
    try:
        os.makedirs(os.path.join(_project_root(), 'Temp'), exist_ok=True)
        script_fd, script_path = tempfile.mkstemp(prefix='sys_task_', suffix='.ps1', dir=os.path.join(_project_root(), 'Temp'), text=True)
        with os.fdopen(script_fd, 'w', encoding='utf-8', newline='\n') as f:
            f.write(content or '')
        out_path = os.path.join(_project_root(), 'Temp', f'out_{uuid.uuid4().hex}.txt')
        task_name = f"Borealis Agent - Task - {uuid.uuid4().hex} @ SYSTEM"
        task_ps = f"""
$ErrorActionPreference='Continue'
$task = "{task_name}"
$ps   = "{ps_exe}"
$scr  = "{script_path}"
$out  = "{out_path}"
try {{ Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue }} catch {{}}
$action   = New-ScheduledTaskAction -Execute $ps -Argument ('-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $scr + '" *> "' + $out + '"')
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
        cleanup_ps = f"try {{ Unregister-ScheduledTask -TaskName '{task_name}' -Confirm:$false }} catch {{}}"
        subprocess.run([ps_exe, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', cleanup_ps], capture_output=True, text=True)
        try:
            if os.path.isfile(script_path):
                os.remove(script_path)
        except Exception:
            pass
        try:
            if os.path.isfile(out_path):
                os.remove(out_path)
        except Exception:
            pass
        return 0, out_data or '', ''
    except Exception as e:
        return -999, '', str(e)


class Role:
    def __init__(self, ctx):
        self.ctx = ctx

    def register_events(self):
        sio = self.ctx.sio

        @sio.on('quick_job_run')
        async def _on_quick_job_run(payload):
            try:
                import socket
                hostname = socket.gethostname()
                target = (payload.get('target_hostname') or '').strip().lower()
                if target and target != hostname.lower():
                    return
                run_mode = (payload.get('run_mode') or 'current_user').lower()
                if run_mode != 'system':
                    return
                job_id = payload.get('job_id')
                script_type = (payload.get('script_type') or '').lower()
                content = payload.get('script_content') or ''
                if script_type != 'powershell':
                    await sio.emit('quick_job_result', {
                        'job_id': job_id,
                        'status': 'Failed',
                        'stdout': '',
                        'stderr': f"Unsupported type: {script_type}"
                    })
                    return
                rc, out, err = _run_powershell_via_system_task(content)
                if rc == -999:
                    rc, out, err = _run_powershell_script_content(content)
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

