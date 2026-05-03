# ======================================================
# Data\Engine\services\ansible\runner.py
# Description: Executes Ansible playbooks from the Linux Engine runtime for scheduled localhost and remote shared targeting.
#
# API Endpoints (if applicable): None
# ======================================================

"""Engine-side Ansible execution helpers."""

from __future__ import annotations

import base64
import json
import logging
import os
import signal
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import uuid
from pathlib import Path
from typing import Any, Callable, Dict, List, Mapping, Optional, Sequence

from ...db import dbapi as sqlite3


RUN_STATUS_SUCCESS = "Success"
RUN_STATUS_FAILED = "Failed"
RUN_STATUS_RUNNING = "Running"
RUN_STATUS_TIMED_OUT = "Timed Out"
_SHARED_ANSIBLE_RUN_TIMEOUT_ENV = "BOREALIS_SHARED_ANSIBLE_RUN_TIMEOUT_SECONDS"
_DEFAULT_SHARED_ANSIBLE_RUN_TIMEOUT_SECONDS = 900
_SHARED_ANSIBLE_TIMEOUT_TERMINATION_GRACE_SECONDS = 5


def _now_ts() -> int:
    return int(time.time())


def _safe_path_parts(raw_name: Any) -> List[str]:
    candidate = str(raw_name or "").replace("\\", "/").strip()
    if not candidate:
        return []
    parts: List[str] = []
    for item in candidate.split("/"):
        value = item.strip()
        if not value or value in {".", ".."}:
            continue
        parts.append(value)
    return parts


def _env_positive_int(name: str, default: int) -> int:
    raw_value = str(os.getenv(name, "") or "").strip()
    if not raw_value:
        return default
    try:
        return max(1, int(raw_value))
    except Exception:
        return default


def _safe_host_alias(hostname: Any) -> str:
    raw = str(hostname or "").strip()
    if not raw:
        return "borealis-engine-01"
    cleaned = "".join(ch if ch.isalnum() or ch in {"-", "_", "."} else "-" for ch in raw)
    cleaned = cleaned.strip(".-") or "borealis-engine-01"
    return cleaned


def _safe_inventory_group(raw_name: Any, *, fallback: str) -> str:
    value = str(raw_name or "").strip().lower()
    if not value:
        return fallback
    cleaned = "".join(ch if ch.isalnum() else "_" for ch in value)
    while "__" in cleaned:
        cleaned = cleaned.replace("__", "_")
    cleaned = cleaned.strip("_")
    return cleaned or fallback


def _log_preview(raw_text: Any, *, limit: int = 320) -> str:
    if raw_text is None:
        return ""
    compact = " | ".join(part.strip() for part in str(raw_text).splitlines() if part.strip())
    if len(compact) > limit:
        return compact[: max(0, limit - 3)] + "..."
    return compact


def _coerce_subprocess_output(raw_text: Any) -> str:
    if raw_text is None:
        return ""
    if isinstance(raw_text, bytes):
        return raw_text.decode("utf-8", errors="replace")
    return str(raw_text)


def _coerce_timeout_expired_exception(exc: Exception) -> Optional[Any]:
    if isinstance(exc, subprocess.TimeoutExpired):
        return exc
    cls = exc.__class__
    if cls.__name__ != "TimeoutExpired":
        return None
    if not hasattr(exc, "cmd") and not hasattr(exc, "timeout"):
        return None
    return exc


class EngineAnsibleRunner:
    """Queue and execute Engine-side Ansible playbooks."""

    def __init__(
        self,
        *,
        socketio: Any,
        db_conn_factory: Callable[[], sqlite3.Connection],
        service_log: Optional[Callable[[str, str, Optional[str]], None]] = None,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self.socketio = socketio
        self._db_conn_factory = db_conn_factory
        self._service_log = service_log
        self._logger = logger or logging.getLogger(__name__)

    def queue_run(
        self,
        *,
        hostname: str,
        playbook_rel_path: str,
        playbook_name: str,
        playbook_abs_path: str = "",
        playbook_content: Optional[str] = None,
        credential_id: Optional[int] = None,
        variable_values: Optional[Mapping[str, Any]] = None,
        payload_files: Optional[Sequence[Mapping[str, Any]]] = None,
        target_specifications: Optional[Sequence[Mapping[str, Any]]] = None,
        runtime_files: Optional[Sequence[Mapping[str, Any]]] = None,
        source: str = "scheduled_job",
        activity_id: Optional[int] = None,
        scheduled_job_id: Optional[int] = None,
        scheduled_run_id: Optional[int] = None,
        scheduled_job_run_row_id: Optional[int] = None,
        connection: str = "local",
    ) -> str:
        run_id = uuid.uuid4().hex
        worker = threading.Thread(
            target=self._run_playbook,
            kwargs={
                "run_id": run_id,
                "hostname": hostname,
                "playbook_abs_path": playbook_abs_path,
                "playbook_content": playbook_content,
                "playbook_rel_path": playbook_rel_path,
                "playbook_name": playbook_name,
                "credential_id": credential_id,
                "variable_values": dict(variable_values or {}),
                "payload_files": list(payload_files or []),
                "target_specifications": list(target_specifications or []),
                "runtime_files": list(runtime_files or []),
                "source": source,
                "activity_id": activity_id,
                "scheduled_job_id": scheduled_job_id,
                "scheduled_run_id": scheduled_run_id,
                "scheduled_job_run_row_id": scheduled_job_run_row_id,
                "connection": connection,
            },
            daemon=True,
        )
        worker.start()
        return run_id

    # ------------------------------------------------------------------
    # Path helpers
    # ------------------------------------------------------------------
    def _project_root(self) -> Path:
        root_env = os.environ.get("BOREALIS_PROJECT_ROOT")
        if root_env:
            return Path(root_env).expanduser().resolve()
        current = Path(__file__).resolve().parent
        for candidate in (current, *current.parents):
            if (candidate / "Engine.sh").is_file():
                return candidate
        raise RuntimeError("Unable to locate the Borealis project root for Engine Ansible execution.")

    def _engine_root(self) -> Path:
        return self._project_root() / "Engine"

    def _ansible_root(self) -> Path:
        root_env = os.environ.get("BOREALIS_ANSIBLE_RUNTIME_ROOT")
        if root_env:
            return Path(root_env).expanduser().resolve()
        return self._engine_root() / "Services" / "api-backend" / "cache" / "Ansible"

    def _collections_root(self) -> Path:
        return self._ansible_root() / "collections"

    def _generated_runtime_root(self) -> Path:
        return self._ansible_root() / "Generated" / "Runtime"

    def _engine_python_path(self) -> Path:
        # Preserve the venv launcher path. Resolving symlinks here can collapse
        # the Engine venv back to /usr/bin/python*, which breaks Ansible imports.
        return Path(sys.executable)

    def _ansible_playbook_command(self) -> List[str]:
        engine_python = self._engine_python_path()
        candidate = engine_python.parent / "ansible-playbook"
        if candidate.is_file():
            return [str(candidate)]
        return [str(engine_python), "-m", "ansible.cli.playbook"]

    # ------------------------------------------------------------------
    # Logging helpers
    # ------------------------------------------------------------------
    def _log(self, message: str, *, level: str = "INFO", host: Optional[str] = None) -> None:
        scope = f"ansible-{host}" if host else None
        if callable(self._service_log):
            try:
                self._service_log("ansible", message, scope=scope, level=level)
            except Exception:
                self._logger.debug("Ansible service log write failed", exc_info=True)
        numeric_level = getattr(logging, level.upper(), logging.INFO)
        self._logger.log(numeric_level, "%s", message)

    def _handle_timeout_exception(
        self,
        *,
        run_id: str,
        hostname: str,
        exc: Any,
        workspace: Optional[Dict[str, Any]],
        recap_payload: Dict[str, Any],
    ) -> tuple[str, str, str]:
        stdout_raw = getattr(exc, "stdout", None)
        if stdout_raw is None:
            stdout_raw = getattr(exc, "output", None)
        stdout_text = _coerce_subprocess_output(stdout_raw).strip()
        partial_stderr = _coerce_subprocess_output(getattr(exc, "stderr", None)).strip()
        timeout_seconds = _env_positive_int(
            _SHARED_ANSIBLE_RUN_TIMEOUT_ENV,
            _DEFAULT_SHARED_ANSIBLE_RUN_TIMEOUT_SECONDS,
        )
        timeout_message = f"Ansible playbook execution exceeded {timeout_seconds} seconds and was terminated."
        stderr_parts = [timeout_message]
        if partial_stderr:
            stderr_parts.append(partial_stderr)
        stderr_text = "\n\n".join(part for part in stderr_parts if part).strip()
        recap_payload.update(
            {
                "timed_out": True,
                "inventory_group": (workspace or {}).get("limit_name", ""),
                "inventory_hosts": list((workspace or {}).get("inventory_hosts") or []),
                "timeout_seconds": timeout_seconds,
                "workspace": str((workspace or {}).get("root") or ""),
            }
        )
        preview_bits = []
        stdout_preview = _log_preview(stdout_text)
        stderr_preview = _log_preview(stderr_text)
        if stdout_preview:
            preview_bits.append(f"stdout_preview={stdout_preview}")
        if stderr_preview:
            preview_bits.append(f"stderr_preview={stderr_preview}")
        preview_suffix = f" {' '.join(preview_bits)}" if preview_bits else ""
        self._log(
            (
                f"run timed out run_id={run_id} host={hostname} "
                f"timeout_seconds={timeout_seconds}{preview_suffix}"
            ),
            level="ERROR",
            host=hostname,
        )
        return RUN_STATUS_TIMED_OUT, stdout_text, stderr_text

    def _terminate_process_group(self, process: Optional[subprocess.Popen[str]], *, hostname: str) -> None:
        if process is None or process.poll() is not None:
            return
        try:
            if hasattr(os, "killpg") and getattr(process, "pid", None):
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=_SHARED_ANSIBLE_TIMEOUT_TERMINATION_GRACE_SECONDS)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait(timeout=_SHARED_ANSIBLE_TIMEOUT_TERMINATION_GRACE_SECONDS)
                return
            process.terminate()
            try:
                process.wait(timeout=_SHARED_ANSIBLE_TIMEOUT_TERMINATION_GRACE_SECONDS)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=_SHARED_ANSIBLE_TIMEOUT_TERMINATION_GRACE_SECONDS)
        except ProcessLookupError:
            return
        except Exception:
            self._logger.debug(
                "Failed to terminate timed-out Ansible process tree for host=%s",
                hostname,
                exc_info=True,
            )

    def _collect_timeout_process_output(self, process: Optional[subprocess.Popen[str]], exc: Any) -> None:
        if process is None:
            return
        try:
            trailing_stdout, trailing_stderr = process.communicate(timeout=1)
        except Exception:
            return
        existing_stdout = _coerce_subprocess_output(getattr(exc, "stdout", None))
        existing_stderr = _coerce_subprocess_output(getattr(exc, "stderr", None))
        trailing_stdout = _coerce_subprocess_output(trailing_stdout)
        trailing_stderr = _coerce_subprocess_output(trailing_stderr)
        if trailing_stdout:
            combined_stdout = existing_stdout or ""
            if trailing_stdout not in combined_stdout:
                combined_stdout += trailing_stdout
            setattr(exc, "stdout", combined_stdout)
        if trailing_stderr:
            combined_stderr = existing_stderr or ""
            if trailing_stderr not in combined_stderr:
                combined_stderr += trailing_stderr
            setattr(exc, "stderr", combined_stderr)

    # ------------------------------------------------------------------
    # Runtime staging helpers
    # ------------------------------------------------------------------
    def _allowed_local_targets(self) -> set[str]:
        values = {"localhost", "127.0.0.1", "::1", "borealis-engine-01"}
        try:
            values.add(socket.gethostname().strip().lower())
        except Exception:
            pass
        try:
            values.add(socket.getfqdn().strip().lower())
        except Exception:
            pass
        return {value for value in values if value}

    def _write_inventory(
        self,
        inventory_path: Path,
        *,
        hostname: str,
        connection: str,
        target_specifications: Sequence[Mapping[str, Any]],
    ) -> Dict[str, Any]:
        connection_norm = str(connection or "local").strip().lower() or "local"
        engine_python = self._engine_python_path()
        children: Dict[str, Any] = {
            "borealis_targets": {
                "hosts": {},
            }
        }
        inventory_hosts: List[str] = []

        normalized_specs = list(target_specifications or [])
        if not normalized_specs:
            alias = _safe_host_alias(hostname)
            target = str(hostname or "").strip().lower()
            if connection_norm != "local":
                raise RuntimeError(f"Unsupported Engine Ansible connection '{connection_norm}'.")
            if target not in self._allowed_local_targets():
                raise RuntimeError(
                    "Local Ansible execution is currently limited to the Engine host aliases "
                    "(localhost, 127.0.0.1, ::1, borealis-engine-01, or the engine hostname)."
                )
            normalized_specs = [
                {
                    "hostname": hostname,
                    "inventory_hostname": alias,
                    "site_group": "site_local",
                    "host_vars": {
                        "ansible_host": "127.0.0.1",
                        "ansible_connection": "local",
                        "ansible_python_interpreter": str(engine_python),
                    },
                }
            ]

        for spec in normalized_specs:
            if not isinstance(spec, Mapping):
                continue
            alias = _safe_host_alias(spec.get("inventory_hostname") or spec.get("hostname"))
            host_vars = dict(spec.get("host_vars") or {})
            if not alias:
                continue
            if connection_norm == "local":
                target = str(spec.get("hostname") or hostname or "").strip().lower()
                if target not in self._allowed_local_targets():
                    raise RuntimeError(
                        "Local Ansible execution is currently limited to the Engine host aliases "
                        "(localhost, 127.0.0.1, ::1, borealis-engine-01, or the engine hostname)."
                    )
                host_vars.setdefault("ansible_host", "127.0.0.1")
                host_vars.setdefault("ansible_connection", "local")
                host_vars.setdefault("ansible_python_interpreter", str(engine_python))
            elif connection_norm not in {"ssh", "winrm"}:
                raise RuntimeError(f"Unsupported Engine Ansible connection '{connection_norm}'.")
            children["borealis_targets"]["hosts"][alias] = host_vars
            inventory_hosts.append(alias)
            site_group = str(spec.get("site_group") or "").strip()
            if site_group:
                group_name = _safe_inventory_group(site_group, fallback="site_unassigned")
                group = children.setdefault(group_name, {"hosts": {}})
                group["hosts"][alias] = {}

        if not inventory_hosts:
            raise RuntimeError("No eligible Ansible targets were provided for inventory generation.")

        inventory_payload = {
            "all": {
                "children": children,
            }
        }
        inventory_path.write_text(json.dumps(inventory_payload, indent=2), encoding="utf-8")
        return {
            "limit_name": "borealis_targets",
            "inventory_hosts": inventory_hosts,
        }

    def _stage_files(self, project_dir: Path, payload_files: Sequence[Mapping[str, Any]]) -> None:
        for item in payload_files:
            if not isinstance(item, Mapping):
                continue
            parts = _safe_path_parts(item.get("file_name") or item.get("name"))
            if not parts:
                continue
            raw_data = item.get("data")
            if not isinstance(raw_data, str):
                continue
            try:
                decoded = base64.b64decode(raw_data, validate=False)
            except Exception as exc:
                raise RuntimeError(f"Unable to decode attached file '{'/'.join(parts)}': {exc}") from exc
            destination = project_dir.joinpath(*parts)
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(decoded)

    def _stage_runtime_files(self, runtime_dir: Path, runtime_files: Sequence[Mapping[str, Any]]) -> None:
        for item in runtime_files:
            if not isinstance(item, Mapping):
                continue
            parts = _safe_path_parts(item.get("file_name") or item.get("name") or item.get("relative_path"))
            if not parts:
                continue
            content = item.get("content")
            if content is None:
                continue
            destination = runtime_dir.joinpath(*parts)
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_text(str(content), encoding="utf-8")
            try:
                mode_raw = item.get("mode")
                if mode_raw not in (None, ""):
                    os.chmod(destination, int(mode_raw))
            except Exception:
                self._logger.debug("Failed to chmod runtime file %s", destination, exc_info=True)

    def _write_ansible_cfg(
        self,
        cfg_path: Path,
        *,
        inventory_path: Path,
        tmp_root: Path,
        control_path_dir: Path,
        ssh_known_hosts_path: Path,
    ) -> None:
        collections_root = self._collections_root().resolve()
        local_tmp = tmp_root / "local_tmp"
        local_tmp.mkdir(parents=True, exist_ok=True)
        control_path_dir.mkdir(parents=True, exist_ok=True)
        ssh_known_hosts_path.parent.mkdir(parents=True, exist_ok=True)
        ssh_known_hosts_path.touch(exist_ok=True)
        try:
            os.chmod(ssh_known_hosts_path, 0o600)
        except Exception:
            self._logger.debug("Failed to chmod SSH known_hosts file %s", ssh_known_hosts_path, exc_info=True)
        ssh_common_args_parts = [
            f"-o UserKnownHostsFile={ssh_known_hosts_path}",
            "-o GlobalKnownHostsFile=/dev/null",
            "-o StrictHostKeyChecking=no",
            "-o UpdateHostKeys=no",
        ]
        cfg_path.write_text(
            "\n".join(
                [
                    "[defaults]",
                    f"inventory = {inventory_path}",
                    "host_key_checking = False",
                    "retry_files_enabled = False",
                    "deprecation_warnings = False",
                    "interpreter_python = auto_silent",
                    "stdout_callback = default",
                    "bin_ansible_callbacks = True",
                    "display_skipped_hosts = True",
                    f"collections_path = {collections_root}",
                    f"local_tmp = {local_tmp}",
                    "remote_tmp = /tmp/.ansible-borealis",
                    "",
                    "[ssh_connection]",
                    f"control_path_dir = {control_path_dir}",
                    "password_mechanism = sshpass",
                    "ssh_common_args = " + " ".join(ssh_common_args_parts),
                    "",
                ]
            ),
            encoding="utf-8",
        )

    def _prepare_workspace(
        self,
        *,
        run_id: str,
        hostname: str,
        playbook_abs_path: str,
        playbook_content: Optional[str],
        playbook_rel_path: str,
        playbook_name: str,
        payload_files: Sequence[Mapping[str, Any]],
        runtime_files: Sequence[Mapping[str, Any]],
        target_specifications: Sequence[Mapping[str, Any]],
        connection: str,
    ) -> Dict[str, Any]:
        root = self._generated_runtime_root() / run_id
        project_dir = root / "project"
        inventory_path = root / "inventory.yml"
        cfg_path = root / "ansible.cfg"
        ssh_known_hosts_path = root / "ssh_known_hosts"
        extra_vars_path = root / "extra_vars.json"
        runtime_dir = root / "runtime"
        control_path_dir = Path(tempfile.gettempdir()) / "ansible_controlplane" / run_id[:12]
        root.mkdir(parents=True, exist_ok=True)
        project_dir.mkdir(parents=True, exist_ok=True)
        runtime_dir.mkdir(parents=True, exist_ok=True)

        staged_file_name = "playbook.yml"
        playbook_parts = _safe_path_parts(playbook_rel_path)
        if playbook_parts:
            staged_file_name = playbook_parts[-1]
        elif playbook_abs_path:
            try:
                staged_file_name = Path(playbook_abs_path).name or staged_file_name
            except Exception:
                staged_file_name = staged_file_name
        elif playbook_name:
            safe_name = "".join(ch if ch.isalnum() or ch in {"-", "_", "."} else "_" for ch in str(playbook_name))
            safe_name = safe_name.strip("._") or "playbook"
            staged_file_name = safe_name if "." in safe_name else f"{safe_name}.yml"
        if "." not in staged_file_name:
            staged_file_name = f"{staged_file_name}.yml"
        staged_playbook = project_dir / staged_file_name
        if playbook_content is not None:
            staged_playbook.write_text(str(playbook_content), encoding="utf-8")
        else:
            if not playbook_abs_path:
                raise RuntimeError("No Ansible playbook source content or path was provided for Engine execution.")
            source_playbook = Path(playbook_abs_path).resolve()
            shutil.copy2(source_playbook, staged_playbook)
        self._stage_files(project_dir, payload_files)
        self._stage_runtime_files(runtime_dir, runtime_files)

        normalized_specs: List[Dict[str, Any]] = []
        for raw_spec in target_specifications or []:
            if not isinstance(raw_spec, Mapping):
                continue
            spec = dict(raw_spec)
            host_vars = dict(spec.get("host_vars") or {})
            for key, value in list(host_vars.items()):
                if isinstance(value, str) and "{{BOREALIS_RUNTIME_DIR}}" in value:
                    host_vars[key] = value.replace("{{BOREALIS_RUNTIME_DIR}}", str(runtime_dir))
            spec["host_vars"] = host_vars
            normalized_specs.append(spec)

        inventory_details = self._write_inventory(
            inventory_path,
            hostname=hostname,
            connection=connection,
            target_specifications=normalized_specs,
        )
        self._write_ansible_cfg(
            cfg_path,
            inventory_path=inventory_path,
            tmp_root=root / "tmp",
            control_path_dir=control_path_dir,
            ssh_known_hosts_path=ssh_known_hosts_path,
        )

        return {
            "root": root,
            "project_dir": project_dir,
            "playbook_path": staged_playbook,
            "inventory_path": inventory_path,
            "cfg_path": cfg_path,
            "ssh_known_hosts_path": ssh_known_hosts_path,
            "extra_vars_path": extra_vars_path,
            "runtime_dir": runtime_dir,
            "control_path_dir": control_path_dir,
            "limit_name": inventory_details["limit_name"],
            "inventory_hosts": inventory_details["inventory_hosts"],
        }

    # ------------------------------------------------------------------
    # Database helpers
    # ------------------------------------------------------------------
    def _conn(self) -> sqlite3.Connection:
        return self._db_conn_factory()

    def _emit_activity_change(self, hostname: str, activity_id: Optional[int], *, change: str) -> None:
        if not activity_id:
            return
        try:
            self.socketio.emit(
                "device_activity_changed",
                {
                    "hostname": str(hostname),
                    "activity_id": int(activity_id),
                    "change": change,
                    "source": "scheduled_job",
                },
            )
        except Exception:
            self._logger.debug("Failed to emit device_activity_changed for activity_id=%s", activity_id, exc_info=True)

    def _record_recap_start(
        self,
        *,
        run_id: str,
        hostname: str,
        playbook_rel_path: str,
        playbook_name: str,
        scheduled_job_id: Optional[int],
        scheduled_run_id: Optional[int],
        activity_id: Optional[int],
        started_ts: int,
    ) -> None:
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                INSERT INTO ansible_play_recaps(
                    run_id,
                    hostname,
                    agent_id,
                    playbook_path,
                    playbook_name,
                    scheduled_job_id,
                    scheduled_run_id,
                    activity_job_id,
                    status,
                    recap_text,
                    recap_json,
                    started_ts,
                    finished_ts,
                    created_at,
                    updated_at
                ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
                """,
                (
                    run_id,
                    str(hostname),
                    None,
                    str(playbook_rel_path or ""),
                    str(playbook_name or ""),
                    int(scheduled_job_id) if scheduled_job_id is not None else None,
                    int(scheduled_run_id) if scheduled_run_id is not None else None,
                    int(activity_id) if activity_id is not None else None,
                    RUN_STATUS_RUNNING,
                    "",
                    json.dumps({}),
                    int(started_ts),
                    None,
                    int(started_ts),
                    int(started_ts),
                ),
            )
            conn.commit()
        finally:
            conn.close()

    def _finalize_run(
        self,
        *,
        run_id: str,
        hostname: str,
        activity_id: Optional[int],
        scheduled_run_id: Optional[int],
        status: str,
        stdout_text: str,
        stderr_text: str,
        finished_ts: int,
        recap_payload: Optional[Mapping[str, Any]] = None,
    ) -> None:
        error_text = ""
        if status != RUN_STATUS_SUCCESS:
            error_text = (stderr_text or stdout_text or "Ansible playbook execution failed").strip()
        conn = self._conn()
        try:
            cur = conn.cursor()
            if activity_id is not None:
                cur.execute(
                    "UPDATE activity_history SET status=?, stdout=?, stderr=? WHERE id=?",
                    (status, stdout_text, stderr_text, int(activity_id)),
                )
            if scheduled_run_id is not None:
                cur.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status=?,
                           finished_ts=?,
                           updated_at=?,
                           error=?
                     WHERE id=?
                    """,
                    (
                        status,
                        int(finished_ts),
                        int(finished_ts),
                        error_text[:512],
                        int(scheduled_run_id),
                    ),
                )
            cur.execute(
                """
                UPDATE ansible_play_recaps
                   SET status=?,
                       recap_text=?,
                       recap_json=?,
                       finished_ts=?,
                       updated_at=?
                 WHERE run_id=?
                """,
                (
                    status,
                    (stdout_text or stderr_text or "")[:200000],
                    json.dumps(dict(recap_payload or {})),
                    int(finished_ts),
                    int(finished_ts),
                    run_id,
                ),
            )
            conn.commit()
        finally:
            conn.close()
        self._emit_activity_change(hostname, activity_id, change="updated")

    # ------------------------------------------------------------------
    # Execution path
    # ------------------------------------------------------------------
    def _run_playbook(
        self,
        *,
        run_id: str,
        hostname: str,
        playbook_abs_path: str,
        playbook_content: Optional[str],
        playbook_rel_path: str,
        playbook_name: str,
        credential_id: Optional[int],
        variable_values: Mapping[str, Any],
        payload_files: Sequence[Mapping[str, Any]],
        target_specifications: Sequence[Mapping[str, Any]],
        runtime_files: Sequence[Mapping[str, Any]],
        source: str,
        activity_id: Optional[int],
        scheduled_job_id: Optional[int],
        scheduled_run_id: Optional[int],
        scheduled_job_run_row_id: Optional[int],
        connection: str,
    ) -> None:
        start_ts = _now_ts()
        effective_run_row_id = scheduled_job_run_row_id if scheduled_job_run_row_id is not None else scheduled_run_id
        self._record_recap_start(
            run_id=run_id,
            hostname=hostname,
            playbook_rel_path=playbook_rel_path,
            playbook_name=playbook_name,
            scheduled_job_id=scheduled_job_id,
            scheduled_run_id=effective_run_row_id,
            activity_id=activity_id,
            started_ts=start_ts,
        )
        self._log(
            (
                f"queue start run_id={run_id} host={hostname} source={source} "
                f"connection={connection} playbook={playbook_rel_path} credential_id={credential_id}"
            ),
            host=hostname,
        )

        status = RUN_STATUS_FAILED
        stdout_text = ""
        stderr_text = ""
        recap_payload: Dict[str, Any] = {
            "run_id": run_id,
            "hostname": hostname,
            "connection": connection,
            "source": source,
            "target_count": len(list(target_specifications or [])),
        }
        workspace: Optional[Dict[str, Any]] = None
        process: Optional[subprocess.Popen[str]] = None

        try:
            workspace = self._prepare_workspace(
                run_id=run_id,
                hostname=hostname,
                playbook_abs_path=playbook_abs_path,
                playbook_content=playbook_content,
                playbook_rel_path=playbook_rel_path,
                playbook_name=playbook_name,
                payload_files=payload_files,
                runtime_files=runtime_files,
                target_specifications=target_specifications,
                connection=connection,
            )

            extra_vars = dict(variable_values or {})
            if extra_vars:
                workspace["extra_vars_path"].write_text(json.dumps(extra_vars), encoding="utf-8")

            env = os.environ.copy()
            collections_root = self._collections_root().resolve()
            env["ANSIBLE_CONFIG"] = str(workspace["cfg_path"])
            env["ANSIBLE_COLLECTIONS_PATH"] = str(collections_root)
            env["ANSIBLE_COLLECTIONS_PATHS"] = str(collections_root)
            env["PYTHONUNBUFFERED"] = "1"
            env["PYTHONUTF8"] = "1"
            run_timeout_seconds = _env_positive_int(
                _SHARED_ANSIBLE_RUN_TIMEOUT_ENV,
                _DEFAULT_SHARED_ANSIBLE_RUN_TIMEOUT_SECONDS,
            )

            command = self._ansible_playbook_command()
            command.extend(
                [
                    "-i",
                    str(workspace["inventory_path"]),
                    "--limit",
                    str(workspace["limit_name"]),
                ]
            )
            if extra_vars:
                command.extend(["--extra-vars", f"@{workspace['extra_vars_path']}"])
            command.append(str(workspace["playbook_path"]))

            self._log(
                (
                    f"launch run_id={run_id} host={hostname} timeout_seconds={run_timeout_seconds} "
                    f"cmd={' '.join(command)}"
                ),
                host=hostname,
            )
            process = subprocess.Popen(
                command,
                cwd=str(workspace["project_dir"]),
                env=env,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                errors="replace",
                start_new_session=True,
            )
            stdout_raw, stderr_raw = process.communicate(timeout=run_timeout_seconds)
            stdout_text = (stdout_raw or "").strip()
            stderr_text = (stderr_raw or "").strip()
            returncode = int(process.returncode or 0)
            status = RUN_STATUS_SUCCESS if returncode == 0 else RUN_STATUS_FAILED
            recap_payload.update(
                {
                    "returncode": returncode,
                    "inventory_group": workspace["limit_name"],
                    "inventory_hosts": list(workspace.get("inventory_hosts") or []),
                    "timeout_seconds": int(run_timeout_seconds),
                    "workspace": str(workspace["root"]),
                }
            )
            if status == RUN_STATUS_SUCCESS:
                self._log(
                    f"run complete run_id={run_id} host={hostname} rc={returncode}",
                    host=hostname,
                )
            else:
                preview_bits = []
                stdout_preview = _log_preview(stdout_text)
                stderr_preview = _log_preview(stderr_text)
                if stdout_preview:
                    preview_bits.append(f"stdout_preview={stdout_preview}")
                if stderr_preview:
                    preview_bits.append(f"stderr_preview={stderr_preview}")
                preview_suffix = f" {' '.join(preview_bits)}" if preview_bits else ""
                self._log(
                    f"run failed run_id={run_id} host={hostname} rc={returncode}{preview_suffix}",
                    level="ERROR",
                    host=hostname,
                )
        except subprocess.TimeoutExpired as exc:
            self._terminate_process_group(process, hostname=hostname)
            self._collect_timeout_process_output(process, exc)
            status, stdout_text, stderr_text = self._handle_timeout_exception(
                run_id=run_id,
                hostname=hostname,
                exc=exc,
                workspace=workspace,
                recap_payload=recap_payload,
            )
        except Exception as exc:
            timeout_exc = _coerce_timeout_expired_exception(exc)
            if timeout_exc is not None:
                self._terminate_process_group(process, hostname=hostname)
                self._collect_timeout_process_output(process, timeout_exc)
                status, stdout_text, stderr_text = self._handle_timeout_exception(
                    run_id=run_id,
                    hostname=hostname,
                    exc=timeout_exc,
                    workspace=workspace,
                    recap_payload=recap_payload,
                )
            else:
                stderr_text = str(exc)
                recap_payload["exception"] = str(exc)
                self._log(
                    f"run exception run_id={run_id} host={hostname} err={exc}",
                    level="ERROR",
                    host=hostname,
                )
        finally:
            finished_ts = _now_ts()
            self._finalize_run(
                run_id=run_id,
                hostname=hostname,
                activity_id=activity_id,
                scheduled_run_id=effective_run_row_id,
                status=status,
                stdout_text=stdout_text,
                stderr_text=stderr_text,
                finished_ts=finished_ts,
                recap_payload=recap_payload,
            )
            if workspace and workspace.get("root"):
                try:
                    shutil.rmtree(str(workspace["root"]), ignore_errors=True)
                except Exception:
                    self._logger.debug("Failed to clean Ansible workspace %s", workspace.get("root"), exc_info=True)
            if workspace and workspace.get("control_path_dir"):
                try:
                    shutil.rmtree(str(workspace["control_path_dir"]), ignore_errors=True)
                except Exception:
                    self._logger.debug(
                        "Failed to clean Ansible control-path workspace %s",
                        workspace.get("control_path_dir"),
                        exc_info=True,
                    )
