import os
import sys
import time
import socket
import asyncio
import json
import subprocess
import tempfile

import socketio


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

    url = get_server_url()
    while True:
        try:
            await sio.connect(url, transports=['websocket'])
            await sio.wait()
        except Exception as e:
            print(f"[ScriptAgent] reconnect in 5s: {e}")
            await asyncio.sleep(5)


if __name__ == '__main__':
    asyncio.run(main())
