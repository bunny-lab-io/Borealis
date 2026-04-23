from __future__ import annotations

import asyncio
import contextlib
import os
import re
import shutil
import subprocess
import tempfile
import uuid
from typing import Any, Dict, List, Optional, Tuple


IS_WINDOWS = os.name == "nt"


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


def _build_wrapped_script(content: str, env_map: Dict[str, str], timeout_seconds: int) -> str:
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
    script_block = "\n".join(
        [line for line in ["\n".join(prelude_lines) if prelude_lines else "", "$__BorealisScript = {\n" + (content or "") + "\n}\n"] if line]
    )
    if timeout_seconds and timeout_seconds > 0:
        return (
            script_block
            + "\n$job = Start-Job -ScriptBlock $__BorealisScript\n"
            + f"if (Wait-Job -Job $job -Timeout {timeout_seconds}) {{\n"
            + "  Receive-Job $job\n"
            + "} else {\n"
            + "  Stop-Job -Job $job -ErrorAction SilentlyContinue | Out-Null\n"
            + "  Remove-Job -Job $job -Force -ErrorAction SilentlyContinue | Out-Null\n"
            + f"  throw \"Script timed out after {timeout_seconds} seconds\"\n"
            + "}\n"
        )
    return script_block + "\n& $__BorealisScript\n"


def _write_temp_script(content: str, suffix: str, env_map: Dict[str, str], timeout_seconds: int) -> str:
    temp_dir = os.path.join(tempfile.gettempdir(), "Borealis", "quick_jobs")
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="bj_", suffix=suffix, dir=temp_dir, text=True)
    final_content = _build_wrapped_script(content or "", env_map, timeout_seconds)
    with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(final_content)
    return path


async def _run_subprocess(args: List[str], timeout_seconds: int, *, env: Optional[Dict[str, str]] = None) -> Tuple[int, str, str]:
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
            timeout_text = f"Script timed out after {timeout_seconds} seconds"
            stdout_text = (out_b or b"").decode(errors="replace")
            stderr_text = (err_b or b"").decode(errors="replace")
            stderr_text = f"{stderr_text.rstrip()}\n{timeout_text}".strip() if stderr_text else timeout_text
            return -1, stdout_text, stderr_text
        return proc.returncode, (out_b or b"").decode(errors="replace"), (err_b or b"").decode(errors="replace")
    except Exception as exc:
        return -1, "", str(exc)


async def _run_powershell_script_content(content: str, env_map: Dict[str, str], timeout_seconds: int) -> Tuple[int, str, str]:
    path = _write_temp_script(content or "", ".ps1", env_map, timeout_seconds)
    try:
        if IS_WINDOWS:
            ps = os.path.expandvars(r"%SystemRoot%\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")
            if not os.path.isfile(ps):
                ps = "powershell.exe"
        else:
            ps = "pwsh"
        return await _run_subprocess([ps, "-ExecutionPolicy", "Bypass", "-NoProfile", "-File", path], timeout_seconds)
    finally:
        try:
            if path and os.path.isfile(path):
                os.remove(path)
        except Exception:
            pass


def _resolve_cmd_binary() -> str:
    candidates: List[str] = []
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


async def _run_batch_local(content: str, env_map: Dict[str, str], timeout_seconds: int) -> Tuple[int, str, str]:
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


async def _run_bash_local(content: str, env_map: Dict[str, str], timeout_seconds: int) -> Tuple[int, str, str]:
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


async def run_currentuser_script(
    *,
    script_type: str,
    content: str,
    env_map: Dict[str, str],
    timeout_seconds: int,
) -> Tuple[int, str, str]:
    normalized_type = str(script_type or "").strip().lower()
    if normalized_type == "powershell":
        return await _run_powershell_script_content(content, env_map, timeout_seconds)
    if normalized_type == "batch":
        return await _run_batch_local(content, env_map, timeout_seconds)
    if normalized_type == "bash":
        return await _run_bash_local(content, env_map, timeout_seconds)
    return -1, "", f"Unsupported type: {normalized_type}"
