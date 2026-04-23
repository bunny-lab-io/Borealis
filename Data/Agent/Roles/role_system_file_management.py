from __future__ import annotations

import asyncio
import contextlib
import mimetypes
import os
import platform
import shutil
import socket
import stat as stat_module
import string
import tempfile
import time
import zipfile
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional

try:
    from Roles.system_task_orchestration import LaneCoordinator
except ModuleNotFoundError as exc:  # pragma: no cover - package import fallback
    if not str(getattr(exc, "name", "") or "").startswith("Roles"):
        raise
    from Data.Agent.Roles.system_task_orchestration import LaneCoordinator


ROLE_NAME = "file_management"
ROLE_CONTEXTS = ["system"]

IS_WINDOWS = os.name == "nt"
IS_LINUX = platform.system().lower() == "linux"
STATUS_PROGRESS_INTERVAL_SECONDS = 2.0


class FileManagementError(RuntimeError):
    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code = str(code or "file_management_error").strip() or "file_management_error"
        self.message = str(message or self.code).strip() or self.code


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _hostname() -> str:
    try:
        return _clean_text(socket.gethostname())
    except Exception:
        return ""


def _normalize_upload_name(value: Any) -> str:
    raw = _clean_text(value).replace("\\", "/")
    if not raw:
        return ""
    return Path(raw).name.strip().replace("\x00", "")


def _normalize_requested_path(value: Any) -> str:
    raw = _clean_text(value)
    if not raw:
        raise FileManagementError("invalid_path", "A non-empty absolute path is required.")
    if IS_WINDOWS:
        candidate = raw.replace("/", "\\")
        if candidate.startswith("\\\\"):
            normalized = os.path.normpath(candidate)
            return normalized
        if len(candidate) >= 3 and candidate[1:3] == ":\\" and candidate[0].isalpha():
            normalized = os.path.normpath(candidate)
            if len(normalized) == 2 and normalized[1] == ":":
                normalized += "\\"
            return normalized
        raise FileManagementError("invalid_path", f"Expected an absolute Windows path, received '{raw}'.")
    if not raw.startswith("/"):
        raise FileManagementError("invalid_path", f"Expected an absolute POSIX path, received '{raw}'.")
    normalized = os.path.normpath(raw)
    return normalized or "/"


def _parent_path(path_value: str) -> str:
    normalized = _normalize_requested_path(path_value)
    if IS_WINDOWS:
        drive, tail = os.path.splitdrive(normalized)
        stripped = normalized.rstrip("\\/")
        if not tail or stripped.lower() == drive.rstrip("\\/").lower():
            return ""
        parent = os.path.dirname(stripped)
        if parent.lower() == drive.lower():
            parent += "\\"
        return parent
    if normalized == "/":
        return "/"
    parent = os.path.dirname(normalized) or "/"
    return parent


def _path_exists(path_value: str) -> bool:
    try:
        return os.path.lexists(path_value)
    except Exception:
        return False


def _ensure_directory(path_value: str) -> str:
    normalized = _normalize_requested_path(path_value)
    if not _path_exists(normalized):
        raise FileManagementError("path_not_found", f"'{normalized}' does not exist.")
    if not os.path.isdir(normalized):
        raise FileManagementError("not_a_directory", f"'{normalized}' is not a directory.")
    return normalized


def _safe_lstat(path_value: str):
    try:
        return os.lstat(path_value)
    except FileNotFoundError as exc:
        raise FileManagementError("path_not_found", f"'{path_value}' does not exist.") from exc
    except PermissionError as exc:
        raise FileManagementError("permission_denied", f"Permission denied for '{path_value}'.") from exc
    except OSError as exc:
        raise FileManagementError("invalid_path", str(exc)) from exc


def _is_hidden(name: str, path_value: str, stat_result) -> bool:
    if _clean_text(name).startswith("."):
        return True
    if IS_WINDOWS:
        file_attrs = getattr(stat_result, "st_file_attributes", 0)
        return bool(file_attrs & getattr(stat_module, "FILE_ATTRIBUTE_HIDDEN", 0))
    return False


def _attributes_for_path(name: str, path_value: str, stat_result, *, is_dir: bool, is_symlink: bool) -> list[str]:
    attributes: list[str] = []
    if _is_hidden(name, path_value, stat_result):
        attributes.append("hidden")
    if not os.access(path_value, os.W_OK):
        attributes.append("read_only")
    if is_symlink:
        attributes.append("symlink")
    if IS_WINDOWS:
        file_attrs = getattr(stat_result, "st_file_attributes", 0)
        if file_attrs & getattr(stat_module, "FILE_ATTRIBUTE_SYSTEM", 0):
            attributes.append("system")
        if file_attrs & getattr(stat_module, "FILE_ATTRIBUTE_REPARSE_POINT", 0):
            attributes.append("reparse_point")
    if is_dir:
        attributes.append("directory")
    return attributes


def _directory_has_children(path_value: str) -> bool:
    try:
        with os.scandir(path_value) as scan:
            for _entry in scan:
                return True
    except Exception:
        return False
    return False


def _entry_from_path(path_value: str, *, parent_path: str = "", force_name: str = "") -> Dict[str, Any]:
    normalized = _normalize_requested_path(path_value)
    stat_result = _safe_lstat(normalized)
    is_symlink = stat_module.S_ISLNK(stat_result.st_mode)
    is_dir = os.path.isdir(normalized)
    name = force_name or (
        normalized
        if (IS_WINDOWS and len(normalized) <= 3 and normalized[1:3] == ":\\")
        else (os.path.basename(normalized.rstrip("\\/")) if normalized not in {"/"} else "/")
    )
    kind = "directory" if is_dir and not is_symlink else "symlink" if is_symlink else "file"
    attributes = _attributes_for_path(name, normalized, stat_result, is_dir=is_dir, is_symlink=is_symlink)
    return {
        "path": normalized,
        "parent_path": parent_path,
        "name": name,
        "kind": kind,
        "size_bytes": 0 if is_dir else int(getattr(stat_result, "st_size", 0) or 0),
        "modified_at": int(getattr(stat_result, "st_mtime", 0) or 0),
        "attributes": attributes,
        "has_children": bool(is_dir and not is_symlink and _directory_has_children(normalized)),
        "is_hidden": "hidden" in attributes,
    }


def _sort_entries(entries: Iterable[Dict[str, Any]]) -> list[Dict[str, Any]]:
    def _key(row: Dict[str, Any]):
        kind = _clean_text(row.get("kind")).lower()
        name = _clean_text(row.get("name")).lower()
        return (0 if kind == "directory" else 1, name)

    return sorted((dict(row) for row in entries), key=_key)


def _list_children(path_value: str) -> list[Dict[str, Any]]:
    parent_path = _ensure_directory(path_value)
    entries: list[Dict[str, Any]] = []
    try:
        with os.scandir(parent_path) as scan:
            for item in scan:
                try:
                    entries.append(_entry_from_path(item.path, parent_path=parent_path))
                except FileManagementError:
                    continue
    except PermissionError as exc:
        raise FileManagementError("permission_denied", f"Permission denied for '{parent_path}'.") from exc
    except OSError as exc:
        raise FileManagementError("invalid_path", str(exc)) from exc
    return _sort_entries(entries)


def _roots_payload() -> Dict[str, Any]:
    if IS_WINDOWS:
        roots = []
        for letter in string.ascii_uppercase:
            drive = f"{letter}:\\"
            if not _path_exists(drive):
                continue
            try:
                roots.append(_entry_from_path(drive, parent_path="", force_name=drive))
            except FileManagementError:
                continue
        return {
            "ok": True,
            "platform": "windows",
            "context_label": "SYSTEM",
            "current_path": "",
            "entries": _sort_entries(roots),
        }
    return {
        "ok": True,
        "platform": "linux" if IS_LINUX else platform.system().lower() or "unknown",
        "context_label": "root",
        "current_path": "/",
        "entries": _list_children("/"),
    }


def _validate_child_name(value: Any) -> str:
    name = _normalize_upload_name(value)
    if not name:
        raise FileManagementError("invalid_name", "A file or folder name is required.")
    if name in {".", ".."}:
        raise FileManagementError("invalid_name", "Relative path markers are not allowed.")
    return name


def _ensure_no_conflict(path_value: str) -> None:
    if _path_exists(path_value):
        raise FileManagementError("conflict", f"'{path_value}' already exists.")


def _normalize_selection_rows(value: Any) -> list[Dict[str, Any]]:
    rows: list[Dict[str, Any]] = []
    candidates = value if isinstance(value, list) else [value]
    for item in candidates:
        if isinstance(item, dict):
            path_value = _clean_text(item.get("path"))
            if not path_value:
                continue
            rows.append(
                {
                    "path": _normalize_requested_path(path_value),
                    "name": _clean_text(item.get("name")),
                    "kind": _clean_text(item.get("kind")).lower(),
                }
            )
            continue
        path_value = _clean_text(item)
        if not path_value:
            continue
        normalized = _normalize_requested_path(path_value)
        rows.append({"path": normalized, "name": os.path.basename(normalized.rstrip("\\/")), "kind": ""})
    deduped: dict[str, Dict[str, Any]] = {}
    for row in rows:
        deduped[row["path"]] = row
    return list(deduped.values())


class Role:
    def __init__(self, ctx) -> None:
        self.ctx = ctx
        self.role_health_label = "File Management"
        hooks = getattr(ctx, "hooks", {}) or {}
        self._log_hook = hooks.get("log_agent")
        self._http_client_factory = hooks.get("http_client")
        self._listener_registered = False
        self._lane_coordinator = LaneCoordinator(log=lambda message: self._log(message))
        self._background_tasks: set[asyncio.Task[Any]] = set()
        self._last_error = ""
        self._last_transfer_at = 0
        self._active_transfers = 0
        self._temp_root = Path(tempfile.gettempdir()) / "Borealis" / "file_management"
        self._temp_root.mkdir(parents=True, exist_ok=True)

    def _log(self, message: str, *, error: bool = False) -> None:
        if callable(self._log_hook):
            try:
                self._log_hook(message, fname="file_management.log")
                if error:
                    self._log_hook(message, fname="agent.error.log")
            except Exception:
                pass

    def _http_client(self) -> Any:
        if not callable(self._http_client_factory):
            raise FileManagementError("client_unavailable", "The Borealis HTTP client is unavailable.")
        try:
            client = self._http_client_factory()
        except Exception as exc:
            raise FileManagementError("client_unavailable", str(exc)) from exc
        if client is None:
            raise FileManagementError("client_unavailable", "The Borealis HTTP client is unavailable.")
        return client

    def _matches_target(self, payload: Dict[str, Any]) -> bool:
        target_hostname = _clean_text(payload.get("hostname") or payload.get("target_hostname")).lower()
        if target_hostname and target_hostname != _hostname().lower():
            return False
        target_agent = _clean_text(payload.get("agent_id"))
        if target_agent and target_agent != _clean_text(self.ctx.agent_id):
            return False
        return True

    def _report_progress(
        self,
        transfer_id: str,
        *,
        status: str = "",
        bytes_complete: Optional[int] = None,
        bytes_total: Optional[int] = None,
        error: str = "",
        archive_name: str = "",
    ) -> None:
        client = self._http_client()
        payload: Dict[str, Any] = {}
        if status:
            payload["status"] = status
        if bytes_complete is not None:
            payload["bytes_complete"] = int(bytes_complete)
        if bytes_total is not None:
            payload["bytes_total"] = int(bytes_total)
        if error:
            payload["error"] = error
        if archive_name:
            payload["archive_name"] = archive_name
        client.post_json(f"/api/agent/files/transfers/{transfer_id}/progress", payload, require_auth=True)

    def _stream_upload_item(
        self,
        *,
        transfer_id: str,
        item_id: str,
        destination_path: str,
        progress_state: Dict[str, Any],
    ) -> None:
        client = self._http_client()
        client.ensure_authenticated()
        client.refresh_base_url()
        headers = client.auth_headers()
        item_url = f"{client.base_url}/api/agent/files/transfers/{transfer_id}/upload-item/{item_id}"
        parent_dir = os.path.dirname(destination_path)
        if parent_dir:
            os.makedirs(parent_dir, exist_ok=True)
        temp_file = None
        try:
            if _path_exists(destination_path):
                raise FileManagementError("conflict", f"'{destination_path}' already exists.")
            with client.session.get(item_url, headers=headers, timeout=300, stream=True) as response:
                response.raise_for_status()
                fd, temp_file = tempfile.mkstemp(prefix=".borealis-upload-", suffix=".tmp", dir=parent_dir or None)
                with os.fdopen(fd, "wb") as handle:
                    for chunk in response.iter_content(chunk_size=64 * 1024):
                        if not chunk:
                            continue
                        handle.write(chunk)
                        progress_state["bytes_complete"] = int(progress_state.get("bytes_complete") or 0) + len(chunk)
                        now_ts = time.time()
                        if now_ts - float(progress_state.get("last_report_at") or 0.0) >= STATUS_PROGRESS_INTERVAL_SECONDS:
                            self._report_progress(
                                transfer_id,
                                status="running",
                                bytes_complete=progress_state["bytes_complete"],
                                bytes_total=progress_state.get("bytes_total"),
                            )
                            progress_state["last_report_at"] = now_ts
                os.replace(temp_file, destination_path)
                temp_file = None
        except PermissionError as exc:
            raise FileManagementError("permission_denied", f"Permission denied for '{destination_path}'.") from exc
        except FileManagementError:
            raise
        except Exception as exc:
            raise FileManagementError("transfer_failed", str(exc)) from exc
        finally:
            if temp_file:
                with contextlib.suppress(Exception):
                    os.remove(temp_file)

    def _upload_transfer_worker(self, payload: Dict[str, Any]) -> None:
        transfer_id = _clean_text(payload.get("transfer_id"))
        target_path = _ensure_directory(payload.get("target_path"))
        items = payload.get("items") if isinstance(payload.get("items"), list) else []
        if not transfer_id or not items:
            raise FileManagementError("invalid_request", "Upload manifest is missing transfer metadata.")
        bytes_total = sum(int((row or {}).get("size_bytes") or 0) for row in items if isinstance(row, dict))
        progress_state = {
            "bytes_complete": 0,
            "bytes_total": bytes_total,
            "last_report_at": 0.0,
        }
        self._report_progress(transfer_id, status="running", bytes_complete=0, bytes_total=bytes_total)
        for row in items:
            if not isinstance(row, dict):
                continue
            item_id = _clean_text(row.get("item_id"))
            item_name = _validate_child_name(row.get("name"))
            destination_path = os.path.join(target_path, item_name)
            self._stream_upload_item(
                transfer_id=transfer_id,
                item_id=item_id,
                destination_path=destination_path,
                progress_state=progress_state,
            )
        self._report_progress(
            transfer_id,
            status="completed",
            bytes_complete=progress_state["bytes_complete"],
            bytes_total=progress_state["bytes_total"],
        )
        self._last_error = ""

    def _zip_selection(self, selections: list[Dict[str, Any]], archive_path: str) -> int:
        total_bytes = 0
        with zipfile.ZipFile(archive_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for selection in selections:
                source_path = _normalize_requested_path(selection.get("path"))
                label = _normalize_upload_name(selection.get("name")) or os.path.basename(source_path.rstrip("\\/")) or source_path
                if os.path.isdir(source_path) and not os.path.islink(source_path):
                    emitted = False
                    for root, dirs, files in os.walk(source_path, topdown=True, followlinks=False):
                        dirs[:] = [name for name in dirs if not os.path.islink(os.path.join(root, name))]
                        relative_root = os.path.relpath(root, source_path)
                        if relative_root == ".":
                            relative_root = ""
                        arc_root = "/".join(part for part in [label, relative_root.replace("\\", "/")] if part)
                        if not dirs and not files:
                            archive.writestr(f"{arc_root}/", b"")
                            emitted = True
                        for file_name in files:
                            file_path = os.path.join(root, file_name)
                            if os.path.isdir(file_path):
                                continue
                            relative_name = os.path.relpath(file_path, source_path).replace("\\", "/")
                            archive_name = "/".join(part for part in [label, relative_name] if part)
                            archive.write(file_path, archive_name)
                            emitted = True
                    if not emitted:
                        archive.writestr(f"{label}/", b"")
                    continue
                archive_name = label
                archive.write(source_path, archive_name)
        try:
            total_bytes = int(os.path.getsize(archive_path))
        except Exception:
            total_bytes = 0
        return total_bytes

    def _post_download_artifact(self, *, transfer_id: str, artifact_path: str, artifact_name: str, mime_type: str) -> None:
        client = self._http_client()
        client.ensure_authenticated()
        client.refresh_base_url()
        url = f"{client.base_url}/api/agent/files/transfers/{transfer_id}/content"
        headers = client.auth_headers()
        with open(artifact_path, "rb") as handle:
            response = client.session.post(
                url,
                headers=headers,
                files={"artifact": (artifact_name, handle, mime_type or "application/octet-stream")},
                data={"archive_name": artifact_name, "mime_type": mime_type or "application/octet-stream"},
                timeout=900,
            )
        response.raise_for_status()

    def _download_transfer_worker(self, payload: Dict[str, Any]) -> None:
        transfer_id = _clean_text(payload.get("transfer_id"))
        selections = _normalize_selection_rows(payload.get("items"))
        if not transfer_id or not selections:
            raise FileManagementError("invalid_request", "Download manifest is missing transfer metadata.")
        archive_required = bool(payload.get("archive_required"))
        archive_name = _normalize_upload_name(payload.get("archive_name")) or "download.zip"

        if not archive_required and len(selections) == 1 and os.path.isfile(selections[0]["path"]):
            source_path = selections[0]["path"]
            artifact_name = _normalize_upload_name(selections[0].get("name")) or os.path.basename(source_path)
            mime_type = mimetypes.guess_type(artifact_name)[0] or "application/octet-stream"
            total_bytes = int(os.path.getsize(source_path) or 0)
            self._report_progress(transfer_id, status="running", bytes_complete=0, bytes_total=total_bytes)
            self._post_download_artifact(
                transfer_id=transfer_id,
                artifact_path=source_path,
                artifact_name=artifact_name,
                mime_type=mime_type,
            )
            self._last_error = ""
            return

        self._temp_root.mkdir(parents=True, exist_ok=True)
        fd, archive_path = tempfile.mkstemp(prefix="download-", suffix=".zip", dir=self._temp_root)
        os.close(fd)
        try:
            total_bytes = self._zip_selection(selections, archive_path)
            self._report_progress(
                transfer_id,
                status="running",
                bytes_complete=0,
                bytes_total=total_bytes,
                archive_name=archive_name,
            )
            self._post_download_artifact(
                transfer_id=transfer_id,
                artifact_path=archive_path,
                artifact_name=archive_name,
                mime_type="application/zip",
            )
            self._last_error = ""
        finally:
            with contextlib.suppress(Exception):
                os.remove(archive_path)

    def _track_task(self, task: asyncio.Task[Any]) -> None:
        self._background_tasks.add(task)

        def _cleanup(done: asyncio.Task[Any]) -> None:
            self._background_tasks.discard(done)
            if done.cancelled():
                return
            try:
                done.result()
            except Exception as exc:
                self._last_error = str(exc)
                self._log(f"file_management background task failed: {exc}", error=True)

        task.add_done_callback(_cleanup)

    async def _run_transfer_background(self, payload: Dict[str, Any]) -> None:
        transfer_id = _clean_text(payload.get("transfer_id")) or "unknown"
        action = _clean_text(payload.get("action")).lower()
        self._active_transfers += 1
        self._last_transfer_at = int(time.time())
        try:
            if action == "upload_start":
                await self._lane_coordinator.run(
                    lane="file_management",
                    job_id=f"upload:{transfer_id}",
                    work=lambda: asyncio.to_thread(self._upload_transfer_worker, dict(payload)),
                )
            elif action == "download_start":
                await self._lane_coordinator.run(
                    lane="file_management",
                    job_id=f"download:{transfer_id}",
                    work=lambda: asyncio.to_thread(self._download_transfer_worker, dict(payload)),
                )
            else:
                raise FileManagementError("invalid_request", f"Unsupported transfer action '{action}'.")
        except Exception as exc:
            self._last_error = str(exc)
            self._log(f"file_management transfer failed transfer_id={transfer_id} error={exc}", error=True)
            try:
                self._report_progress(transfer_id, status="failed", error=str(exc))
            except Exception:
                self._log(f"file_management failed to report transfer failure transfer_id={transfer_id}", error=True)
        finally:
            self._active_transfers = max(0, int(self._active_transfers) - 1)
            self._last_transfer_at = int(time.time())

    def health_report(self) -> Dict[str, Any]:
        lane_details = self._lane_coordinator.snapshot() if self._lane_coordinator is not None else {}
        if not self._listener_registered:
            return {
                "status": "unhealthy",
                "role_label": self.role_health_label,
                "detail": "File-management listener is not registered.",
                "details": {
                    "running_status": "Stopped",
                    "listener_state": "Not Registered",
                },
            }
        details = {
            "running_status": "Ready",
            "listener_state": "Registered",
            "active_transfers": str(max(0, int(self._active_transfers))),
            "last_transfer_at": str(int(self._last_transfer_at or 0)),
            **lane_details,
        }
        if self._last_error:
            details["last_error"] = self._last_error
            return {
                "status": "recovering",
                "role_label": self.role_health_label,
                "detail": self._last_error,
                "details": details,
            }
        return {
            "status": "healthy",
            "role_label": self.role_health_label,
            "detail": "Remote file-management listeners are ready.",
            "details": details,
        }

    def register_events(self) -> None:
        sio = self.ctx.sio
        self._listener_registered = True

        @sio.on("file_management_request")
        async def _on_file_management_request(payload):
            if not isinstance(payload, dict):
                return {"ok": False, "error": "invalid_request", "message": "Expected an object payload."}
            if not self._matches_target(payload):
                return {"ok": False, "error": "not_found", "message": "The file-management request targeted another device."}
            action = _clean_text(payload.get("action")).lower()
            try:
                if action == "roots":
                    self._last_error = ""
                    return _roots_payload()
                if action == "children":
                    current_path = _normalize_requested_path(payload.get("path"))
                    self._last_error = ""
                    return {"ok": True, "current_path": current_path, "entries": _list_children(current_path)}
                if action == "mkdir":
                    parent_path = _ensure_directory(payload.get("path"))
                    name = _validate_child_name(payload.get("name"))

                    def _mkdir():
                        destination = os.path.join(parent_path, name)
                        _ensure_no_conflict(destination)
                        os.mkdir(destination)
                        return _entry_from_path(destination, parent_path=parent_path)

                    entry = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id=f"mkdir:{parent_path}:{name}",
                        work=lambda: asyncio.to_thread(_mkdir),
                    )
                    self._last_error = ""
                    return {"ok": True, "entry": entry}
                if action == "rename":
                    source_path = _normalize_requested_path(payload.get("path"))
                    if not _path_exists(source_path):
                        raise FileManagementError("path_not_found", f"'{source_path}' does not exist.")
                    new_name = _validate_child_name(payload.get("new_name"))

                    def _rename():
                        parent_path = _parent_path(source_path)
                        if not parent_path or _clean_text(parent_path).lower() == _clean_text(source_path).lower():
                            raise FileManagementError("invalid_path", "Root paths cannot be renamed.")
                        destination = os.path.join(parent_path, new_name)
                        _ensure_no_conflict(destination)
                        os.rename(source_path, destination)
                        return _entry_from_path(destination, parent_path=parent_path)

                    entry = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id=f"rename:{source_path}",
                        work=lambda: asyncio.to_thread(_rename),
                    )
                    self._last_error = ""
                    return {"ok": True, "entry": entry}
                if action == "move":
                    selections = _normalize_selection_rows(payload.get("paths"))
                    destination_path = _ensure_directory(payload.get("destination_path"))
                    if not selections:
                        raise FileManagementError("invalid_request", "At least one source path is required.")

                    def _move():
                        moved = []
                        for row in selections:
                            source_path = row["path"]
                            if not _path_exists(source_path):
                                raise FileManagementError("path_not_found", f"'{source_path}' does not exist.")
                            final_path = os.path.join(destination_path, os.path.basename(source_path.rstrip("\\/")))
                            if source_path == final_path:
                                continue
                            if os.path.isdir(source_path) and not os.path.islink(source_path):
                                try:
                                    common = os.path.commonpath([source_path, destination_path])
                                except Exception:
                                    common = ""
                                if common and _clean_text(common).lower() == _clean_text(source_path).lower():
                                    raise FileManagementError("invalid_path", "A folder cannot be moved into itself.")
                            _ensure_no_conflict(final_path)
                        for row in selections:
                            source_path = row["path"]
                            final_path = os.path.join(destination_path, os.path.basename(source_path.rstrip("\\/")))
                            if source_path == final_path:
                                continue
                            shutil.move(source_path, final_path)
                            moved.append(_entry_from_path(final_path, parent_path=destination_path))
                        return moved

                    moved = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id=f"move:{destination_path}",
                        work=lambda: asyncio.to_thread(_move),
                    )
                    self._last_error = ""
                    return {"ok": True, "moved": moved}
                if action == "delete":
                    selections = _normalize_selection_rows(payload.get("paths"))
                    if not selections:
                        raise FileManagementError("invalid_request", "At least one path is required.")

                    def _delete():
                        deleted = []
                        for row in selections:
                            source_path = row["path"]
                            if not _path_exists(source_path):
                                raise FileManagementError("path_not_found", f"'{source_path}' does not exist.")
                        for row in selections:
                            source_path = row["path"]
                            deleted.append(source_path)
                            if os.path.isdir(source_path) and not os.path.islink(source_path):
                                shutil.rmtree(source_path)
                            else:
                                os.remove(source_path)
                        return deleted

                    deleted = await self._lane_coordinator.run(
                        lane="file_management",
                        job_id="delete",
                        work=lambda: asyncio.to_thread(_delete),
                    )
                    self._last_error = ""
                    return {"ok": True, "deleted": deleted}
                if action in {"upload_start", "download_start"}:
                    transfer_id = _clean_text(payload.get("transfer_id"))
                    if not transfer_id:
                        raise FileManagementError("invalid_request", "Transfer metadata is missing transfer_id.")
                    self._track_task(asyncio.create_task(self._run_transfer_background(dict(payload))))
                    self._last_error = ""
                    return {"ok": True, "status": "accepted", "transfer_id": transfer_id}
                raise FileManagementError("invalid_request", f"Unsupported file-management action '{action}'.")
            except FileManagementError as exc:
                self._last_error = exc.message
                self._log(f"file_management request failed action={action} error={exc.message}", error=True)
                return {"ok": False, "error": exc.code, "message": exc.message}
            except Exception as exc:
                self._last_error = str(exc)
                self._log(f"file_management request crashed action={action} error={exc}", error=True)
                return {"ok": False, "error": "internal_error", "message": str(exc)}

    def stop_all(self) -> None:
        for task in list(self._background_tasks):
            task.cancel()
