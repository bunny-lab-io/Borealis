import os
import re
import asyncio
import datetime
import tempfile
import uuid
import time
import subprocess
import base64
from typing import Dict, List, Optional


ROLE_NAME = 'script_exec_system'
ROLE_CONTEXTS = ['system']


def _project_root():
    return os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..'))


def _find_borealis_root() -> Optional[str]:
    override = os.environ.get('BOREALIS_ROOT') or os.environ.get('BOREALIS_PROJECT_ROOT')
    if override:
        candidate = os.path.abspath(override)
        if os.path.isfile(os.path.join(candidate, 'Borealis.ps1')):
            return candidate

    cur = _project_root()
    for _ in range(8):
        if os.path.isfile(os.path.join(cur, 'Borealis.ps1')):
            return cur
        parent = os.path.dirname(cur)
        if parent == cur:
            break
        cur = parent

    fallback = os.path.abspath(os.path.join(_project_root(), '..'))
    if os.path.isfile(os.path.join(fallback, 'Borealis.ps1')):
        return fallback
    return None


def _agent_logs_root() -> str:
    root = _find_borealis_root() or _project_root()
    return os.path.abspath(os.path.join(root, 'Logs', 'Agent'))


def _rotate_daily(path: str):
    try:
        if os.path.isfile(path):
            mtime = os.path.getmtime(path)
            dt = datetime.datetime.fromtimestamp(mtime)
            today = datetime.datetime.now().date()
            if dt.date() != today:
                base, ext = os.path.splitext(path)
                suffix = dt.strftime('%Y-%m-%d')
                newp = f"{base}.{suffix}{ext}"
                try:
                    os.replace(path, newp)
                except Exception:
                    pass
    except Exception:
        pass


def _write_updater_log(message: str):
    try:
        log_dir = _agent_logs_root()
        os.makedirs(log_dir, exist_ok=True)
        path = os.path.join(log_dir, 'updater.log')
        _rotate_daily(path)
        ts = datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')
        with open(path, 'a', encoding='utf-8') as fh:
            fh.write(f'[{ts}] {message}\n')
    except Exception:
        pass


def _launch_silent_update_task():
    if os.name != 'nt':
        raise RuntimeError('Silent update is supported on Windows hosts only.')

    root = _find_borealis_root()
    if not root:
        raise RuntimeError('Unable to locate Borealis.ps1 on this agent.')

    ps_exe = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
    if not os.path.isfile(ps_exe):
        ps_exe = 'powershell.exe'

    task_name = f"Borealis Agent - SilentUpdate - {uuid.uuid4().hex}"
    task_literal = _ps_literal(task_name)
    ps_literal = _ps_literal(ps_exe)
    root_literal = _ps_literal(root)
    args_literal = _ps_literal('-NoProfile -ExecutionPolicy Bypass -File .\\Borealis.ps1 -SilentUpdate')

    cleanup_script = (
        "Start-Job -ScriptBlock { Start-Sleep -Seconds 600; "
        f"try {{ Unregister-ScheduledTask -TaskName {task_literal} -Confirm:$false -ErrorAction SilentlyContinue }} catch {{}} }} | Out-Null"
    )

    ps_script = "\n".join(
        [
            "$ErrorActionPreference='Stop'",
            f"$task = {task_literal}",
            "try { Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue } catch {}",
            f"$action = New-ScheduledTaskAction -Execute {ps_literal} -Argument {args_literal} -WorkingDirectory {root_literal}",
            "$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -DeleteExpiredTaskAfter (New-TimeSpan -Minutes 30)",
            "$principal= New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest",
            "Register-ScheduledTask -TaskName $task -Action $action -Settings $settings -Principal $principal -Force | Out-Null",
            "Start-ScheduledTask -TaskName $task | Out-Null",
            cleanup_script,
        ]
    )

    flags = 0x08000000 if os.name == 'nt' else 0
    proc = subprocess.run(
        [ps_exe, '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', ps_script],
        capture_output=True,
        text=True,
        creationflags=flags,
    )
    if proc.returncode != 0:
        stderr = proc.stderr or proc.stdout or 'scheduled task registration failed'
        raise RuntimeError(stderr.strip())
    return task_name

def _canonical_env_key(name: str) -> str:
    cleaned = re.sub(r"[^A-Za-z0-9_]", "_", (name or "").strip())
    return cleaned.upper()


def _sanitize_env_map(raw) -> Dict[str, str]:
    env: Dict[str, str] = {}
    if isinstance(raw, dict):
        for key, value in raw.items():
            if key is None:
                continue
            name = str(key).strip()
            if not name:
                continue
            env_key = _canonical_env_key(name)
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


def _apply_variable_aliases(env_map: Dict[str, str], variables: List[Dict[str, str]]) -> Dict[str, str]:
    if not isinstance(env_map, dict) or not isinstance(variables, list):
        return env_map
    for var in variables:
        if not isinstance(var, dict):
            continue
        name = str(var.get('name') or '').strip()
        if not name:
            continue
        canonical = _canonical_env_key(name)
        if not canonical or canonical not in env_map:
            continue
        value = env_map[canonical]
        alias = re.sub(r"[^A-Za-z0-9_]", "_", name)
        if alias and alias not in env_map:
            env_map[alias] = value
        if alias == name:
            continue
        # Only add the original name when it results in a valid identifier.
        if re.match(r"^[A-Za-z_][A-Za-z0-9_]*$", name) and name not in env_map:
            env_map[name] = value
    return env_map


def _decode_base64_text(value: str) -> Optional[str]:
    if not isinstance(value, str):
        return None
    stripped = value.strip()
    if not stripped:
        return ""
    try:
        cleaned = re.sub(r"\s+", "", stripped)
    except Exception:
        cleaned = stripped
    try:
        decoded = base64.b64decode(cleaned, validate=True)
    except Exception:
        return None
    try:
        return decoded.decode("utf-8")
    except Exception:
        return decoded.decode("utf-8", errors="replace")


def _decode_script_content(raw_content, encoding_hint) -> str:
    if isinstance(raw_content, str):
        encoding = str(encoding_hint or "").strip().lower()
        if encoding in ("base64", "b64", "base-64"):
            decoded = _decode_base64_text(raw_content)
            if decoded is not None:
                return decoded
        decoded = _decode_base64_text(raw_content)
        if decoded is not None:
            return decoded
        return raw_content
    return ""


def _ps_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _build_wrapped_script(content: str, env_map: Dict[str, str], timeout_seconds: int) -> str:
    def _env_assignment_lines(lines: List[str]) -> None:
        for key, value in (env_map or {}).items():
            if not key:
                continue
            value_literal = _ps_literal(value)
            key_literal = _ps_literal(key)
            env_path_literal = f"[string]::Format('Env:{{0}}', {key_literal})"
            lines.append(
                f"try {{ [System.Environment]::SetEnvironmentVariable({key_literal}, {value_literal}, 'Process') }} catch {{}}"
            )
            lines.append(
                "try { Set-Item -LiteralPath (" + env_path_literal + ") -Value " + value_literal +
                " -ErrorAction Stop } catch { try { New-Item -Path (" + env_path_literal + ") -Value " +
                value_literal + " -Force | Out-Null } catch {} }"
            )

    prelude_lines: List[str] = []
    _env_assignment_lines(prelude_lines)

    inner_lines: List[str] = []
    _env_assignment_lines(inner_lines)
    inner_lines.append(content or "")

    prelude = "\n".join(prelude_lines)
    inner = "\n".join(line for line in inner_lines if line is not None)

    pieces: List[str] = []
    if prelude:
        pieces.append(prelude)
    pieces.append("$__BorealisScript = {\n" + inner + "\n}\n")
    script_block = "\n".join(pieces)
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


def _run_powershell_script_content(content: str, env_map: Dict[str, str], timeout_seconds: int):
    temp_dir = os.path.join(_project_root(), "Temp")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="sj_", suffix=".ps1", dir=temp_dir, text=True)
    final_content = _build_wrapped_script(content or "", env_map, timeout_seconds)
    with os.fdopen(fd, 'w', encoding='utf-8', newline='\n') as fh:
        fh.write(final_content)

    ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
    if not os.path.isfile(ps):
        ps = "powershell.exe"
    try:
        flags = 0x08000000 if os.name == 'nt' else 0
        proc_timeout = timeout_seconds + 30 if timeout_seconds else 60 * 60
        proc = subprocess.run(
            [ps, "-ExecutionPolicy", "Bypass", "-NoProfile", "-File", path],
            capture_output=True,
            text=True,
            timeout=proc_timeout,
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


def _run_powershell_via_system_task(content: str, env_map: Dict[str, str], timeout_seconds: int):
    ps_exe = os.path.expandvars(r"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe")
    if not os.path.isfile(ps_exe):
        ps_exe = 'powershell.exe'
    try:
        os.makedirs(os.path.join(_project_root(), 'Temp'), exist_ok=True)
        script_fd, script_path = tempfile.mkstemp(prefix='sys_task_', suffix='.ps1', dir=os.path.join(_project_root(), 'Temp'), text=True)
        final_content = _build_wrapped_script(content or '', env_map, timeout_seconds)
        with os.fdopen(script_fd, 'w', encoding='utf-8', newline='\n') as f:
            f.write(final_content)
        try:
            log_dir = os.path.join(_project_root(), 'Logs', 'Agent')
            os.makedirs(log_dir, exist_ok=True)
            with open(os.path.join(log_dir, 'system_last.ps1'), 'w', encoding='utf-8', newline='\n') as df:
                df.write(content or '')
        except Exception:
            pass
        out_path = os.path.join(_project_root(), 'Temp', f'out_{uuid.uuid4().hex}.txt')
        task_name = f"Borealis Agent - Task - {uuid.uuid4().hex} @ SYSTEM"
        # Use WorkingDirectory set to the script folder to avoid 0x2 'file not found' issues
        # on some systems when PowerShell resolves relative paths.
        task_ps = f"""
$ErrorActionPreference='Continue'
$task = "{task_name}"
$ps   = "{ps_exe}"
$scr  = "{script_path}"
$out  = "{out_path}"
try {{ Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue }} catch {{}}
$action   = New-ScheduledTaskAction -Execute $ps -Argument ('-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $scr + '" *> "' + $out + '"') -WorkingDirectory (Split-Path -Parent $scr)
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

        @sio.on('agent_silent_update')
        async def _on_agent_silent_update(payload):
            hostname = None
            details = payload if isinstance(payload, dict) else {}
            try:
                import socket

                hostname = socket.gethostname()
                target = (details.get('target_hostname') or '').strip().lower()
                if target and target != hostname.lower():
                    return

                loop = asyncio.get_running_loop()
                request_id = (details.get('request_id') or '').strip()
                req_disp = request_id or 'n/a'
                host_disp = hostname or 'unknown'
                _write_updater_log(f"Silent update request received for host '{host_disp}' (request_id={req_disp})")
                try:
                    task_name = await loop.run_in_executor(None, _launch_silent_update_task)
                except Exception as exc:
                    _write_updater_log(
                        f"Silent update launch failed for host '{host_disp}' (request_id={req_disp}): {exc}"
                    )
                    try:
                        details['_silent_update_error_logged'] = True
                    except Exception:
                        pass
                    raise

                _write_updater_log(
                    f"Silent update scheduled via task '{task_name}' for host '{host_disp}' (request_id={req_disp})"
                )

                try:
                    await sio.emit(
                        'agent_silent_update_status',
                        {
                            'request_id': details.get('request_id') or '',
                            'hostname': hostname,
                            'status': 'started',
                        },
                    )
                except Exception:
                    pass
            except Exception as e:
                if not isinstance(details, dict) or not details.get('_silent_update_error_logged'):
                    try:
                        request_id = (details.get('request_id') or '').strip()
                        req_disp = request_id or 'n/a'
                        host_disp = hostname or 'unknown'
                        _write_updater_log(
                            f"Silent update encountered error on host '{host_disp}' (request_id={req_disp}): {e}"
                        )
                    except Exception:
                        pass
                try:
                    await sio.emit(
                        'agent_silent_update_status',
                        {
                            'request_id': details.get('request_id') or '',
                            'hostname': hostname or '',
                            'status': 'error',
                            'error': str(e),
                        },
                    )
                except Exception:
                    pass

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
                content = _decode_script_content(payload.get('script_content'), payload.get('script_encoding'))
                raw_env = payload.get('environment')
                env_map = _sanitize_env_map(raw_env)
                variables = payload.get('variables') if isinstance(payload.get('variables'), list) else []
                for var in variables:
                    if not isinstance(var, dict):
                        continue
                    name = str(var.get('name') or '').strip()
                    if not name:
                        continue
                    key = _canonical_env_key(name)
                    if key in env_map:
                        continue
                    default_val = var.get('default')
                    if isinstance(default_val, bool):
                        env_map[key] = "True" if default_val else "False"
                    elif default_val is None:
                        env_map[key] = ""
                    else:
                        env_map[key] = str(default_val)
                env_map = _apply_variable_aliases(env_map, variables)
                try:
                    timeout_seconds = max(0, int(payload.get('timeout_seconds') or 0))
                except Exception:
                    timeout_seconds = 0
                if script_type != 'powershell':
                    await sio.emit('quick_job_result', {
                        'job_id': job_id,
                        'status': 'Failed',
                        'stdout': '',
                        'stderr': f"Unsupported type: {script_type}"
                    })
                    return
                rc, out, err = _run_powershell_via_system_task(content, env_map, timeout_seconds)
                if rc == -999:
                    rc, out, err = _run_powershell_script_content(content, env_map, timeout_seconds)
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
