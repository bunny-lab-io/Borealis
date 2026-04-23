from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import tempfile
import threading
import time
import uuid
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Tuple

try:
    from runtime_paths import agent_runtime_root
except Exception:  # pragma: no cover - fallback for runtime path issues
    def agent_runtime_root(start: Optional[Path] = None) -> Path:
        return Path(_project_root())


def _project_root() -> str:
    return os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))


def _temp_root() -> str:
    root = Path(agent_runtime_root(__file__)) / "Temp"
    root.mkdir(parents=True, exist_ok=True)
    return str(root)


def _log(log_callback: Optional[Callable[[str], None]], message: str) -> None:
    if not callable(log_callback):
        return
    try:
        log_callback(str(message or "").strip())
    except Exception:
        pass


def canonical_env_key(name: str) -> str:
    cleaned = re.sub(r"[^A-Za-z0-9_]", "_", (name or "").strip())
    return cleaned.upper()


def sanitize_env_map(raw: Any) -> Dict[str, str]:
    env: Dict[str, str] = {}
    if isinstance(raw, dict):
        for key, value in raw.items():
            if key is None:
                continue
            name = str(key).strip()
            if not name:
                continue
            env_key = canonical_env_key(name)
            if not env_key:
                continue
            if isinstance(value, bool):
                env[env_key] = "True" if value else "False"
            elif value is None:
                env[env_key] = ""
            else:
                env[env_key] = str(value)
    return env


def apply_variable_aliases(env_map: Dict[str, str], variables: List[Dict[str, Any]]) -> Dict[str, str]:
    if not isinstance(env_map, dict) or not isinstance(variables, list):
        return env_map
    for var in variables:
        if not isinstance(var, dict):
            continue
        name = str(var.get("name") or "").strip()
        if not name:
            continue
        canonical = canonical_env_key(name)
        if not canonical or canonical not in env_map:
            continue
        value = env_map[canonical]
        alias = re.sub(r"[^A-Za-z0-9_]", "_", name)
        if alias and alias not in env_map:
            env_map[alias] = value
        if alias != name and re.match(r"^[A-Za-z_][A-Za-z0-9_]*$", name) and name not in env_map:
            env_map[name] = value
    return env_map


def build_env_map(raw_env: Any, variables: Any) -> Dict[str, str]:
    env_map = sanitize_env_map(raw_env)
    safe_variables = variables if isinstance(variables, list) else []
    for var in safe_variables:
        if not isinstance(var, dict):
            continue
        name = str(var.get("name") or "").strip()
        if not name:
            continue
        key = canonical_env_key(name)
        if key in env_map:
            continue
        default_val = var.get("default")
        if isinstance(default_val, bool):
            env_map[key] = "True" if default_val else "False"
        elif default_val is None:
            env_map[key] = ""
        else:
            env_map[key] = str(default_val)
    return apply_variable_aliases(env_map, safe_variables)


def _ps_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def _build_wrapped_script(
    content: str,
    env_map: Dict[str, str],
    timeout_seconds: int,
    *,
    use_background_job: bool = True,
) -> str:
    prelude_lines: List[str] = []
    for key, value in (env_map or {}).items():
        if not key:
            continue
        value_literal = _ps_literal(value)
        key_literal = _ps_literal(key)
        env_path_literal = f"[string]::Format('Env:{{0}}', {key_literal})"
        prelude_lines.append(
            f"try {{ [System.Environment]::SetEnvironmentVariable({key_literal}, {value_literal}, 'Process') }} catch {{}}"
        )
        prelude_lines.append(
            "try { Set-Item -LiteralPath (" + env_path_literal + ") -Value " + value_literal
            + " -ErrorAction Stop } catch { try { New-Item -Path (" + env_path_literal + ") -Value "
            + value_literal + " -Force | Out-Null } catch {} }"
        )

    pieces: List[str] = []
    if prelude_lines:
        pieces.append("\n".join(prelude_lines))
    pieces.append("$__BorealisScript = {\n" + (content or "") + "\n}\n")
    script_block = "\n".join(pieces)
    if timeout_seconds and timeout_seconds > 0 and use_background_job:
        return (
            script_block
            + "$job = Start-Job -ScriptBlock $__BorealisScript\n"
            + f"if (Wait-Job -Job $job -Timeout {timeout_seconds}) {{\n"
            + "  Receive-Job $job\n"
            + "} else {\n"
            + "  Stop-Job $job -Force\n"
            + f"  throw \"Script timed out after {timeout_seconds} seconds\"\n"
            + "}\n"
        )
    return script_block + "& $__BorealisScript\n"


def _run_sync_process(args: List[str], timeout_seconds: int, *, env: Optional[Dict[str, str]] = None) -> Tuple[int, str, str]:
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
        stderr_text = f"{stderr_text.rstrip()}\n{timeout_text}".strip() if stderr_text else timeout_text
        return -1, stdout_text, stderr_text
    except Exception as exc:
        return -1, "", str(exc)


def _resolve_cmd_binary() -> str:
    candidates: List[str] = []
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
    candidates: List[str] = []
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


def _run_batch_script_content(content: str, env_map: Dict[str, str], timeout_seconds: int) -> Tuple[int, str, str]:
    if os.name != "nt":
        return -1, "", "Batch scripts are only supported on Windows agents."
    temp_dir = _temp_root()
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


def _run_bash_script_content(content: str, env_map: Dict[str, str], timeout_seconds: int) -> Tuple[int, str, str]:
    bash_bin = _resolve_bash_binary()
    if not bash_bin:
        return -1, "", "Bash is not available on this agent."
    temp_dir = _temp_root()
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


def _resolve_powershell_binary() -> str:
    if os.name == "nt":
        ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
        if os.path.isfile(ps):
            return ps
        return "powershell.exe"
    return shutil.which("pwsh") or "pwsh"


def _run_powershell_script_content(
    content: str,
    env_map: Dict[str, str],
    timeout_seconds: int,
    *,
    progress_callback: Optional[Callable[[str], None]] = None,
    log_callback: Optional[Callable[[str], None]] = None,
    use_background_job: bool = True,
) -> Tuple[int, str, str]:
    temp_dir = _temp_root()
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="sj_", suffix=".ps1", dir=temp_dir, text=True)
    final_content = _build_wrapped_script(
        content or "",
        env_map,
        timeout_seconds,
        use_background_job=use_background_job,
    )
    with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(final_content)

    ps = _resolve_powershell_binary()
    try:
        proc_timeout = timeout_seconds + 30 if timeout_seconds else 60 * 60
        flags = 0x08000000 if os.name == "nt" else 0
        _log(
            log_callback,
            "system powershell direct launch script_path={0} timeout_seconds={1} background_job={2} temp_root={3}".format(
                path,
                timeout_seconds,
                "yes" if use_background_job else "no",
                temp_dir,
            ),
        )
        if callable(progress_callback):
            proc = subprocess.Popen(
                [ps, "-ExecutionPolicy", "Bypass", "-NoProfile", "-File", path],
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                bufsize=1,
                creationflags=flags,
            )
            _log(log_callback, f"system powershell direct launch pid={proc.pid} script_path={path}")
            output_chunks: List[str] = []

            def _reader() -> None:
                stream = getattr(proc, "stdout", None)
                if stream is None:
                    return
                try:
                    while True:
                        line = stream.readline()
                        if line == "":
                            break
                        output_chunks.append(line)
                        try:
                            progress_callback(line)
                        except Exception:
                            pass
                finally:
                    try:
                        stream.close()
                    except Exception:
                        pass

            reader_thread = threading.Thread(target=_reader, daemon=True)
            reader_thread.start()
            try:
                proc.wait(timeout=proc_timeout)
            except subprocess.TimeoutExpired:
                _log(log_callback, f"system powershell direct launch timed out pid={proc.pid} script_path={path}")
                try:
                    proc.kill()
                except Exception:
                    pass
                reader_thread.join(timeout=2)
                merged_output = "".join(output_chunks)
                return -1, merged_output, f"Script timed out after {timeout_seconds} seconds"
            reader_thread.join(timeout=2)
            merged_output = "".join(output_chunks)
            _log(
                log_callback,
                "system powershell direct launch finished pid={0} rc={1} output_bytes={2} script_path={3}".format(
                    proc.pid,
                    proc.returncode,
                    len(merged_output.encode("utf-8", errors="replace")),
                    path,
                ),
            )
            return proc.returncode or 0, merged_output, ""

        proc = subprocess.run(
            [ps, "-ExecutionPolicy", "Bypass", "-NoProfile", "-File", path],
            capture_output=True,
            text=True,
            timeout=proc_timeout,
            creationflags=flags,
        )
        stdout_text = proc.stdout or ""
        stderr_text = proc.stderr or ""
        _log(
            log_callback,
            "system powershell direct launch finished rc={0} stdout_bytes={1} stderr_bytes={2} script_path={3}".format(
                proc.returncode,
                len(stdout_text.encode("utf-8", errors="replace")),
                len(stderr_text.encode("utf-8", errors="replace")),
                path,
            ),
        )
        return proc.returncode, stdout_text, stderr_text
    except subprocess.TimeoutExpired as exc:
        stdout_text = exc.stdout or ""
        stderr_text = exc.stderr or ""
        timeout_text = f"Script timed out after {timeout_seconds} seconds"
        stderr_text = f"{stderr_text.rstrip()}\n{timeout_text}".strip() if stderr_text else timeout_text
        _log(log_callback, f"system powershell direct launch timed out script_path={path}")
        return -1, stdout_text, stderr_text
    except Exception as exc:
        _log(log_callback, f"system powershell direct launch failed script_path={path} error={exc}")
        return -1, "", str(exc)
    finally:
        try:
            if os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass


def _read_output_delta(path: str, previous_size: int) -> Tuple[int, str]:
    try:
        if not os.path.isfile(path):
            return previous_size, ""
        current_size = os.path.getsize(path)
        if current_size <= previous_size:
            return current_size, ""
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            fh.seek(previous_size)
            chunk = fh.read()
        return current_size, chunk or ""
    except Exception:
        return previous_size, ""


def _scheduled_task_state(ps_exe: str, task_name: str) -> Dict[str, Any]:
    state_command = f"""
$task = {json.dumps(task_name)}
$taskObj = Get-ScheduledTask -TaskName $task -ErrorAction SilentlyContinue
$info = Get-ScheduledTaskInfo -TaskName $task -ErrorAction SilentlyContinue
[pscustomobject]@{{
  State = if ($taskObj) {{ [string]$taskObj.State }} else {{ '' }}
  LastTaskResult = if ($info) {{ [int]$info.LastTaskResult }} else {{ 0 }}
}} | ConvertTo-Json -Compress
"""
    result = subprocess.run(
        [ps_exe, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", state_command],
        capture_output=True,
        text=True,
        creationflags=(0x08000000 if os.name == "nt" else 0),
        timeout=15,
    )
    if result.returncode != 0:
        return {}
    try:
        payload = json.loads(result.stdout or "{}")
    except Exception:
        return {}
    return payload if isinstance(payload, dict) else {}


def _run_powershell_via_system_task(
    content: str,
    env_map: Dict[str, str],
    timeout_seconds: int,
    *,
    progress_callback: Optional[Callable[[str], None]] = None,
    log_callback: Optional[Callable[[str], None]] = None,
    use_background_job: bool = True,
) -> Tuple[int, str, str]:
    ps_exe = _resolve_powershell_binary()
    script_path = ""
    out_path = ""
    task_name = f"Borealis Agent - Task - {uuid.uuid4().hex} @ SYSTEM"
    try:
        temp_dir = _temp_root()
        os.makedirs(temp_dir, exist_ok=True)
        script_fd, script_path = tempfile.mkstemp(
            prefix="sys_task_",
            suffix=".ps1",
            dir=temp_dir,
            text=True,
        )
        with os.fdopen(script_fd, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(
                _build_wrapped_script(
                    content or "",
                    env_map,
                    timeout_seconds,
                    use_background_job=use_background_job,
                )
            )
        out_path = os.path.join(temp_dir, f"out_{uuid.uuid4().hex}.txt")
        _log(
            log_callback,
            "system powershell scheduled-task launch requested task_name={0} script_path={1} output_path={2} timeout_seconds={3} background_job={4}".format(
                task_name,
                script_path,
                out_path,
                timeout_seconds,
                "yes" if use_background_job else "no",
            ),
        )
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
"""
        created = subprocess.run(
            [ps_exe, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", task_ps],
            capture_output=True,
            text=True,
            creationflags=(0x08000000 if os.name == "nt" else 0),
            timeout=30,
        )
        if created.returncode != 0:
            detail = created.stderr or created.stdout or "scheduled task creation failed"
            _log(
                log_callback,
                "system powershell scheduled-task launch failed task_name={0} detail={1}".format(task_name, detail.strip()),
            )
            return -999, "", detail

        deadline = time.time() + max(timeout_seconds + 30, 90) if timeout_seconds else time.time() + 90
        last_size = 0
        full_output = ""
        started_seen = False
        rc = 0
        last_state_name = ""
        while time.time() < deadline:
            last_size, chunk = _read_output_delta(out_path, last_size)
            if chunk:
                started_seen = True
                full_output += chunk
                if callable(progress_callback):
                    try:
                        progress_callback(chunk)
                    except Exception:
                        pass

            state = _scheduled_task_state(ps_exe, task_name)
            state_name = str(state.get("State") or "").strip().lower()
            try:
                rc = int(state.get("LastTaskResult") or 0)
            except Exception:
                rc = 0
            if state_name != last_state_name:
                _log(
                    log_callback,
                    "system powershell scheduled-task state task_name={0} state={1} last_task_result={2}".format(
                        task_name,
                        state_name or "unknown",
                        rc,
                    ),
                )
                last_state_name = state_name

            if state_name in {"running", "queued"}:
                started_seen = True
            if started_seen and state_name == "ready":
                break
            if not state_name and started_seen:
                break
            time.sleep(1)
        else:
            _log(
                log_callback,
                "system powershell scheduled-task timed out task_name={0} output_path={1}".format(
                    task_name,
                    out_path,
                ),
            )
            try:
                subprocess.run(
                    [
                        ps_exe,
                        "-NoProfile",
                        "-ExecutionPolicy",
                        "Bypass",
                        "-Command",
                        f"try {{ Stop-ScheduledTask -TaskName '{task_name}' -ErrorAction SilentlyContinue }} catch {{}}",
                    ],
                    capture_output=True,
                    text=True,
                    creationflags=(0x08000000 if os.name == "nt" else 0),
                    timeout=15,
                )
            except Exception:
                pass
            return -1, full_output, f"Script timed out after {timeout_seconds} seconds"

        last_size, chunk = _read_output_delta(out_path, last_size)
        if chunk:
            full_output += chunk
            if callable(progress_callback):
                try:
                    progress_callback(chunk)
                except Exception:
                    pass
        _log(
            log_callback,
            "system powershell scheduled-task finished task_name={0} rc={1} output_bytes={2} output_path={3}".format(
                task_name,
                rc,
                len(full_output.encode("utf-8", errors="replace")),
                out_path,
            ),
        )
        return rc, full_output, ""
    except Exception as exc:
        _log(log_callback, f"system powershell scheduled-task failed task_name={task_name} error={exc}")
        return -999, "", str(exc)
    finally:
        cleanup_ps = f"try {{ Unregister-ScheduledTask -TaskName '{task_name}' -Confirm:$false }} catch {{}}"
        try:
            subprocess.run(
                [ps_exe, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", cleanup_ps],
                capture_output=True,
                text=True,
                creationflags=(0x08000000 if os.name == "nt" else 0),
                timeout=15,
            )
        except Exception:
            pass
        for path in (script_path, out_path):
            try:
                if path and os.path.isfile(path):
                    os.remove(path)
            except Exception:
                pass


def run_system_script(
    *,
    script_type: str,
    content: str,
    env_map: Dict[str, str],
    timeout_seconds: int,
    progress_callback: Optional[Callable[[str], None]] = None,
    log_callback: Optional[Callable[[str], None]] = None,
) -> Tuple[int, str, str]:
    normalized_type = str(script_type or "").strip().lower()
    if normalized_type == "powershell":
        use_background_job = not callable(progress_callback)
        if os.name == "nt":
            rc, out, err = _run_powershell_via_system_task(
                content,
                env_map,
                timeout_seconds,
                progress_callback=progress_callback,
                log_callback=log_callback,
                use_background_job=use_background_job,
            )
            if rc == -999:
                _log(
                    log_callback,
                    "system powershell scheduled-task fallback triggered detail={0}".format(str(err or "").strip() or "unknown"),
                )
                return _run_powershell_script_content(
                    content,
                    env_map,
                    timeout_seconds,
                    progress_callback=progress_callback,
                    log_callback=log_callback,
                    use_background_job=use_background_job,
                )
            return rc, out, err
        return _run_powershell_script_content(
            content,
            env_map,
            timeout_seconds,
            progress_callback=progress_callback,
            log_callback=log_callback,
            use_background_job=use_background_job,
        )
    if normalized_type == "batch":
        return _run_batch_script_content(content, env_map, timeout_seconds)
    if normalized_type == "bash":
        return _run_bash_script_content(content, env_map, timeout_seconds)
    return -1, "", f"Unsupported type: {normalized_type}"
