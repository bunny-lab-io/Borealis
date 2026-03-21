import os
import re
import asyncio
import tempfile
import uuid
import time
import subprocess
import base64
import shutil
from typing import Dict, List, Optional

from signature_utils import decode_script_bytes, verify_and_store_script_signature


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

    if os.name == "nt":
        ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
        if not os.path.isfile(ps):
            ps = "powershell.exe"
    else:
        ps = shutil.which("pwsh") or "pwsh"
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


def _run_sync_process(args: List[str], timeout_seconds: int, *, env: Optional[Dict[str, str]] = None):
    try:
        flags = 0x08000000 if os.name == "nt" else 0
        proc = subprocess.run(
            args,
            capture_output=True,
            text=True,
            timeout=(timeout_seconds if timeout_seconds and timeout_seconds > 0 else None),
            creationflags=flags,
            env=env,
        )
        return proc.returncode, proc.stdout or "", proc.stderr or ""
    except subprocess.TimeoutExpired as exc:
        stdout_text = exc.stdout or ""
        stderr_text = exc.stderr or ""
        timeout_text = f"Script timed out after {timeout_seconds} seconds"
        if stderr_text:
            stderr_text = f"{stderr_text.rstrip()}\n{timeout_text}"
        else:
            stderr_text = timeout_text
        return -1, stdout_text, stderr_text
    except Exception as exc:
        return -1, "", str(exc)


def _resolve_cmd_binary() -> str:
    candidates = []
    if os.name == "nt":
        system_root = os.environ.get("SystemRoot") or r"C:\Windows"
        candidates.append(os.path.join(system_root, "System32", "cmd.exe"))
    candidates.extend(["cmd.exe", "cmd"])
    for candidate in candidates:
        if not candidate:
            continue
        if os.path.isabs(candidate):
            if os.path.isfile(candidate):
                return candidate
            continue
        resolved = shutil.which(candidate)
        if resolved:
            return resolved
        return candidate
    return "cmd.exe"


def _resolve_bash_binary() -> str:
    candidates = []
    override = (os.environ.get("BOREALIS_BASH_BIN") or os.environ.get("BOREALIS_REMOTE_BASH_BIN") or "").strip()
    if override:
        candidates.append(override)
    candidates.extend(["bash", "bash.exe", "/bin/bash", "/usr/bin/bash", "sh", "sh.exe", "/bin/sh", "/usr/bin/sh"])
    for candidate in candidates:
        if not candidate:
            continue
        if os.path.isabs(candidate):
            if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
                return candidate
            continue
        resolved = shutil.which(candidate)
        if resolved:
            return resolved
    return ""


def _run_batch_script_content(content: str, env_map: Dict[str, str], timeout_seconds: int):
    if os.name != "nt":
        return -1, "", "Batch scripts are only supported on Windows agents."
    temp_dir = os.path.join(_project_root(), "Temp")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="sj_", suffix=".bat", dir=temp_dir, text=True)
    try:
        normalized = (content or "").replace("\r\n", "\n").replace("\r", "\n")
        with os.fdopen(fd, "w", encoding="utf-8", newline="\r\n") as fh:
            fh.write(normalized)
        env = os.environ.copy()
        env.update(env_map or {})
        return _run_sync_process([_resolve_cmd_binary(), "/D", "/C", path], timeout_seconds, env=env)
    finally:
        try:
            if os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass


def _run_bash_script_content(content: str, env_map: Dict[str, str], timeout_seconds: int):
    bash_bin = _resolve_bash_binary()
    if not bash_bin:
        return -1, "", "Bash is not available on this agent."
    temp_dir = os.path.join(_project_root(), "Temp")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="sj_", suffix=".sh", dir=temp_dir, text=True)
    try:
        normalized = (content or "").replace("\r\n", "\n").replace("\r", "\n")
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(normalized)
        if os.name != "nt":
            os.chmod(path, 0o700)
        env = os.environ.copy()
        env.update(env_map or {})
        return _run_sync_process([bash_bin, path], timeout_seconds, env=env)
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
            log_dir = os.path.join(_project_root(), 'Agent', 'Logs')
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
        hooks = getattr(self.ctx, 'hooks', {}) or {}
        log_agent_hook = hooks.get('log_agent')

        def _log(message: str, *, error: bool = False) -> None:
            if callable(log_agent_hook):
                try:
                    log_agent_hook(message)
                    if error:
                        log_agent_hook(message, fname='agent.error.log')
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
                job_label = job_id if job_id is not None else 'unknown'
                _log(f"quick_job_run(system) received payload job_id={job_label}")
                context = payload.get('context') if isinstance(payload, dict) else None

                def _result_payload(job_value, status_value, stdout_value="", stderr_value=""):
                    result = {
                        'job_id': job_value,
                        'status': status_value,
                        'stdout': stdout_value,
                        'stderr': stderr_value,
                    }
                    if isinstance(context, dict):
                        result['context'] = context
                    return result

                script_bytes = decode_script_bytes(payload.get('script_content'), payload.get('script_encoding'))
                if script_bytes is None:
                    _log(f"quick_job_run(system) invalid script payload job_id={job_label}", error=True)
                    await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', 'Invalid script payload (unable to decode)'))
                    return
                signature_b64 = payload.get('signature')
                sig_alg = (payload.get('sig_alg') or 'ed25519').lower()
                signing_key = payload.get('signing_key')
                if sig_alg and sig_alg not in ('ed25519', 'eddsa'):
                    _log(f"quick_job_run(system) unsupported signature algorithm job_id={job_label} alg={sig_alg}", error=True)
                    await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', f'Unsupported script signature algorithm: {sig_alg}'))
                    return
                if not isinstance(signature_b64, str) or not signature_b64.strip():
                    _log(f"quick_job_run(system) missing signature job_id={job_label}", error=True)
                    await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', 'Missing script signature; rejecting payload'))
                    return
                http_client_fn = getattr(self.ctx, 'hooks', {}).get('http_client') if hasattr(self.ctx, 'hooks') else None
                client = http_client_fn() if callable(http_client_fn) else None
                if client is None:
                    _log(f"quick_job_run(system) missing http_client hook job_id={job_label}", error=True)
                    await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', 'Signature verification unavailable (client missing)'))
                    return
                if not verify_and_store_script_signature(client, script_bytes, signature_b64, signing_key):
                    _log(f"quick_job_run(system) signature verification failed job_id={job_label}", error=True)
                    await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', 'Rejected script payload due to invalid signature'))
                    return
                _log(f"quick_job_run(system) signature verified job_id={job_label}")
                content = script_bytes.decode('utf-8', errors='replace')
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
                if script_type == 'powershell':
                    if os.name == 'nt':
                        rc, out, err = _run_powershell_via_system_task(content, env_map, timeout_seconds)
                        if rc == -999:
                            rc, out, err = _run_powershell_script_content(content, env_map, timeout_seconds)
                    else:
                        rc, out, err = _run_powershell_script_content(content, env_map, timeout_seconds)
                elif script_type == 'batch':
                    rc, out, err = _run_batch_script_content(content, env_map, timeout_seconds)
                elif script_type == 'bash':
                    rc, out, err = _run_bash_script_content(content, env_map, timeout_seconds)
                else:
                    await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', f"Unsupported type: {script_type}"))
                    return
                status = 'Success' if rc == 0 else 'Failed'
                await sio.emit('quick_job_result', _result_payload(job_id, status, out, err))
            except Exception as e:
                context = payload.get('context') if isinstance(payload, dict) else None
                def _error_payload(job_value, message):
                    result = {
                        'job_id': job_value,
                        'status': 'Failed',
                        'stdout': '',
                        'stderr': message,
                    }
                    if isinstance(context, dict):
                        result['context'] = context
                    return result
                try:
                    await sio.emit('quick_job_result', _error_payload(payload.get('job_id') if isinstance(payload, dict) else None, str(e)))
                except Exception:
                    pass
