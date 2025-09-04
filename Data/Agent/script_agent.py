import os
import sys
import time
import socket
import asyncio
import json
import subprocess
import tempfile

import socketio
import platform
import time
import uuid
import tempfile


def get_project_root():
    return os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))


def get_server_url():
    # Try to reuse the agent config if present
    cfg_path = os.path.join(get_project_root(), "agent_settings.json")
    try:
        if os.path.isfile(cfg_path):
            with open(cfg_path, "r", encoding="utf-8") as f:
                data = json.load(f)
                url = data.get("borealis_server_url")
                if isinstance(url, str) and url.strip():
                    return url.strip()
    except Exception:
        pass
    return "http://localhost:5000"


def run_powershell_script_content(content: str):
    # Store ephemeral script under <ProjectRoot>/Temp
    temp_dir = os.path.join(get_project_root(), "Temp")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="sj_", suffix=".ps1", dir=temp_dir, text=True)
    with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as fh:
        fh.write(content or "")

    ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
    if not os.path.isfile(ps):
        ps = "powershell.exe"
    try:
        proc = subprocess.run(
            [ps, "-ExecutionPolicy", "Bypass", "-NoProfile", "-File", path],
            capture_output=True,
            text=True,
            timeout=60*60,
        )
        return proc.returncode, proc.stdout or "", proc.stderr or ""
    except Exception as e:
        return -1, "", str(e)


async def main():
    sio = socketio.AsyncClient(reconnection=True)
    hostname = socket.gethostname()

    @sio.event
    async def connect():
        print("[ScriptAgent] Connected to server")
        # Identify as script agent (no heartbeat to avoid UI duplication)
        try:
            await sio.emit("connect_agent", {"agent_id": f"{hostname}-script"})
        except Exception:
            pass

    @sio.on("quick_job_run")
    async def on_quick_job_run(payload):
        # Treat as generic script_run internally
        try:
            target = (payload.get('target_hostname') or '').strip().lower()
            if target and target != hostname.lower():
                return
            run_mode = (payload.get('run_mode') or 'current_user').lower()
            # Only the SYSTEM service handles system-mode jobs; ignore others
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
            # Preferred: run via ephemeral scheduled task under SYSTEM for isolation
            rc, out, err = run_powershell_via_system_task(content)
            if rc == -999:
                # Fallback to direct execution if task creation not available
                rc, out, err = run_powershell_script_content(content)
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

    @sio.event
    async def disconnect():
        print("[ScriptAgent] Disconnected")

    async def heartbeat_loop():
        # Minimal heartbeat so device appears online even without a user helper
        while True:
            try:
                await sio.emit("agent_heartbeat", {
                    "agent_id": f"{hostname}-script",
                    "hostname": hostname,
                    "agent_operating_system": f"{platform.system()} {platform.release()} (Service)",
                    "last_seen": int(time.time())
                })
            except Exception:
                pass
            await asyncio.sleep(30)

    url = get_server_url()
    while True:
        try:
            await sio.connect(url, transports=['websocket'])
            # Heartbeat while connected
            hb = asyncio.create_task(heartbeat_loop())
            try:
                await sio.wait()
            finally:
                try:
                    hb.cancel()
                except Exception:
                    pass
        except Exception as e:
            print(f"[ScriptAgent] reconnect in 5s: {e}")
            await asyncio.sleep(5)


def run_powershell_via_system_task(content: str):
    """Create an ephemeral scheduled task under SYSTEM to run the script.
    Returns (rc, stdout, stderr). If the environment lacks PowerShell ScheduledTasks module, returns (-999, '', 'unavailable').
    """
    ps_exe = os.path.expandvars(r"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe")
    if not os.path.isfile(ps_exe):
        ps_exe = 'powershell.exe'
    try:
        os.makedirs(os.path.join(get_project_root(), 'Temp'), exist_ok=True)
        # Write the target script
        script_fd, script_path = tempfile.mkstemp(prefix='sys_task_', suffix='.ps1', dir=os.path.join(get_project_root(), 'Temp'), text=True)
        with os.fdopen(script_fd, 'w', encoding='utf-8', newline='\n') as f:
            f.write(content or '')
        # Output capture path
        out_path = os.path.join(get_project_root(), 'Temp', f'out_{uuid.uuid4().hex}.txt')
        task_name = f"Borealis Agent - Task - {uuid.uuid4().hex} @ SYSTEM"
        # Build PS to create/run task with DeleteExpiredTaskAfter
        task_ps = f"""
$ErrorActionPreference='Continue'
$task = "{task_name}"
$ps   = "{ps_exe}"
$scr  = "{script_path}"
$out  = "{out_path}"
try {{ Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue }} catch {{}}
$action   = New-ScheduledTaskAction -Execute $ps -Argument ('-NoProfile -ExecutionPolicy Bypass -File "' + $scr + '" *> "' + $out + '"')
$settings = New-ScheduledTaskSettingsSet -DeleteExpiredTaskAfter (New-TimeSpan -Minutes 5) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
$principal= New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -TaskName $task -Action $action -Settings $settings -Principal $principal -Force | Out-Null
Start-ScheduledTask -TaskName $task | Out-Null
Start-Sleep -Seconds 2
Get-ScheduledTask -TaskName $task | Out-Null
"""
        # Run task creation
        proc = subprocess.run([ps_exe, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', task_ps], capture_output=True, text=True)
        if proc.returncode != 0:
            return -999, '', (proc.stderr or proc.stdout or 'scheduled task creation failed')
        # Wait up to 60s for output to be written
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
        # Cleanup task (best-effort)
        cleanup_ps = f"try {{ Unregister-ScheduledTask -TaskName '{task_name}' -Confirm:$false }} catch {{}}"
        subprocess.run([ps_exe, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', cleanup_ps], capture_output=True, text=True)
        return 0, out_data or '', ''
    except Exception as e:
        return -999, '', str(e)


if __name__ == '__main__':
    asyncio.run(main())
