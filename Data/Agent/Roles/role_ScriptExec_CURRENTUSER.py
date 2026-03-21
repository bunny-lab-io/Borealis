import os
import sys
import re
import asyncio
import contextlib
import tempfile
import uuid
import base64
import shutil
from typing import Dict, List, Optional
from PyQt5 import QtWidgets, QtGui

from signature_utils import decode_script_bytes, verify_and_store_script_signature
try:
    from update_state import busy_activity
except Exception:
    busy_activity = None

ROLE_NAME = 'script_exec_currentuser'
ROLE_CONTEXTS = ['interactive']


IS_WINDOWS = os.name == 'nt'


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


async def _run_subprocess(args: List[str], timeout_seconds: int, *, env: Optional[Dict[str, str]] = None):
    try:
        proc = await asyncio.create_subprocess_exec(
            *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            creationflags=(0x08000000 if IS_WINDOWS else 0),
            env=env,
        )
        try:
            if timeout_seconds and timeout_seconds > 0:
                out_b, err_b = await asyncio.wait_for(proc.communicate(), timeout=timeout_seconds)
            else:
                out_b, err_b = await proc.communicate()
        except asyncio.TimeoutError:
            with contextlib.suppress(ProcessLookupError):
                proc.kill()
            out_b, err_b = await proc.communicate()
            stdout_text = (out_b or b"").decode(errors="replace")
            stderr_text = (err_b or b"").decode(errors="replace")
            timeout_text = f"Script timed out after {timeout_seconds} seconds"
            if stderr_text:
                stderr_text = f"{stderr_text.rstrip()}\n{timeout_text}"
            else:
                stderr_text = timeout_text
            return -1, stdout_text, stderr_text
        return proc.returncode, (out_b or b"").decode(errors="replace"), (err_b or b"").decode(errors="replace")
    except Exception as exc:
        return -1, "", str(exc)


async def _run_powershell_script_content(content: str, env_map: Dict[str, str], timeout_seconds: int):
    path = _write_temp_script(content or "", ".ps1", env_map, timeout_seconds)
    try:
        return await _run_powershell_local(path)
    finally:
        try:
            if path and os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass


def _resolve_cmd_binary() -> str:
    candidates = []
    if IS_WINDOWS:
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


async def _run_batch_local(content: str, env_map: Dict[str, str], timeout_seconds: int):
    if not IS_WINDOWS:
        return -1, "", "Batch scripts are only supported on Windows agents."
    temp_dir = os.path.join(tempfile.gettempdir(), "Borealis", "quick_jobs")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="bj_", suffix=".bat", dir=temp_dir, text=True)
    try:
        normalized = (content or "").replace("\r\n", "\n").replace("\r", "\n")
        with os.fdopen(fd, "w", encoding="utf-8", newline="\r\n") as fh:
            fh.write(normalized)
        env = os.environ.copy()
        env.update(env_map or {})
        return await _run_subprocess([_resolve_cmd_binary(), "/D", "/C", path], timeout_seconds, env=env)
    finally:
        try:
            if os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass


async def _run_bash_local(content: str, env_map: Dict[str, str], timeout_seconds: int):
    bash_bin = _resolve_bash_binary()
    if not bash_bin:
        return -1, "", "Bash is not available on this agent."
    temp_dir = os.path.join(tempfile.gettempdir(), "Borealis", "quick_jobs")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="bj_", suffix=".sh", dir=temp_dir, text=True)
    try:
        normalized = (content or "").replace("\r\n", "\n").replace("\r", "\n")
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(normalized)
        if not IS_WINDOWS:
            os.chmod(path, 0o700)
        env = os.environ.copy()
        env.update(env_map or {})
        return await _run_subprocess([bash_bin, path], timeout_seconds, env=env)
    finally:
        try:
            if os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass


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
                if not target or target != hostname.lower():
                    return
                job_id = payload.get('job_id')
                script_type = (payload.get('script_type') or '').lower()
                run_mode = (payload.get('run_mode') or 'current_user').lower()
                if run_mode == 'system':
                    return
                job_label = job_id if job_id is not None else 'unknown'
                _log(f"quick_job_run(currentuser) received payload job_id={job_label}")
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
                    _log(f"quick_job_run(currentuser) invalid script payload job_id={job_label}", error=True)
                    await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', 'Invalid script payload (unable to decode)'))
                    return
                signature_b64 = payload.get('signature')
                sig_alg = (payload.get('sig_alg') or 'ed25519').lower()
                signing_key = payload.get('signing_key')
                if sig_alg and sig_alg not in ('ed25519', 'eddsa'):
                    _log(f"quick_job_run(currentuser) unsupported signature algorithm job_id={job_label} alg={sig_alg}", error=True)
                    await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', f'Unsupported script signature algorithm: {sig_alg}'))
                    return
                if not isinstance(signature_b64, str) or not signature_b64.strip():
                    _log(f"quick_job_run(currentuser) missing signature job_id={job_label}", error=True)
                    await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', 'Missing script signature; rejecting payload'))
                    return
                http_client_fn = getattr(self.ctx, 'hooks', {}).get('http_client') if hasattr(self.ctx, 'hooks') else None
                client = http_client_fn() if callable(http_client_fn) else None
                if client is None:
                    _log(f"quick_job_run(currentuser) missing http_client hook job_id={job_label}", error=True)
                    await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', 'Signature verification unavailable (client missing)'))
                    return
                if not verify_and_store_script_signature(client, script_bytes, signature_b64, signing_key):
                    _log(f"quick_job_run(currentuser) signature verification failed job_id={job_label}", error=True)
                    await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', 'Rejected script payload due to invalid signature'))
                    return
                _log(f"quick_job_run(currentuser) signature verified job_id={job_label}")
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
                busy_ctx = (
                    busy_activity(
                        'quick_job_currentuser',
                        metadata={
                            'job_id': str(job_label),
                            'script_type': script_type or 'unknown',
                        },
                    )
                    if callable(busy_activity)
                    else contextlib.nullcontext()
                )
                with busy_ctx:
                    if script_type == 'powershell':
                        rc, out, err = await _run_powershell_via_user_task(content, env_map, timeout_seconds)
                        if rc == -999:
                            rc, out, err = await _run_powershell_script_content(content, env_map, timeout_seconds)
                    elif script_type == 'batch':
                        rc, out, err = await _run_batch_local(content, env_map, timeout_seconds)
                    elif script_type == 'bash':
                        rc, out, err = await _run_bash_local(content, env_map, timeout_seconds)
                    else:
                        await sio.emit('quick_job_result', _result_payload(job_id, 'Failed', '', f'Unsupported type: {script_type}'))
                        return
                    status = 'Success' if rc == 0 else 'Failed'
                    await sio.emit('quick_job_result', _result_payload(job_id, status, out, err))
            except Exception as e:
                try:
                    context = payload.get('context') if isinstance(payload, dict) else None
                    result = {
                        'job_id': payload.get('job_id') if isinstance(payload, dict) else None,
                        'status': 'Failed',
                        'stdout': '',
                        'stderr': str(e),
                    }
                    if isinstance(context, dict):
                        result['context'] = context
                    await sio.emit('quick_job_result', result)
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
