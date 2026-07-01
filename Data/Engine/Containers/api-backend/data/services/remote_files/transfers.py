"""Site-worker remote file-transfer session store."""

from __future__ import annotations

import json
import mimetypes
import os
import shutil
import threading
import time
import uuid
from pathlib import Path
from typing import Any, Dict, Iterable, Optional, Tuple

from Data.Engine.auth.guid_utils import normalize_guid


FILE_TRANSFER_SESSION_TTL_SECONDS = int(os.environ.get("BOREALIS_FILE_TRANSFER_TTL_SECONDS", 60 * 60))


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _sanitize_upload_name(value: Any) -> str:
    raw = _normalize_text(value).replace("\\", "/")
    if not raw:
        return ""
    return Path(raw).name.strip().replace("\x00", "")


def _sanitize_relative_upload_path(value: Any, *, fallback_name: Any = "") -> str:
    raw = _normalize_text(value).replace("\\", "/")
    if not raw:
        raw = _sanitize_upload_name(fallback_name)
    if raw.startswith("/") or raw.startswith("\\") or (len(raw) >= 2 and raw[1] == ":"):
        return ""
    segments: list[str] = []
    for part in raw.split("/"):
        normalized = _sanitize_upload_name(part)
        if not normalized:
            continue
        if normalized in {".", ".."}:
            return ""
        segments.append(normalized)
    return "/".join(segments)


def _normalize_transfer_entries(value: Any) -> list[Dict[str, Any]]:
    rows: list[Dict[str, Any]] = []
    candidates = value if isinstance(value, list) else [value]
    for item in candidates:
        if isinstance(item, dict):
            path = _normalize_text(item.get("path"))
            if not path:
                continue
            rows.append(
                {
                    "path": path,
                    "name": _normalize_text(item.get("name")),
                    "kind": _normalize_text(item.get("kind")).lower(),
                }
            )
            continue
        path = _normalize_text(item)
        if not path:
            continue
        rows.append({"path": path, "name": Path(path).name, "kind": ""})
    deduped: dict[str, Dict[str, Any]] = {}
    for row in rows:
        deduped[row["path"]] = row
    return list(deduped.values())


def _normalize_upload_manifest_items(value: Any) -> list[Dict[str, Any]]:
    rows: list[Dict[str, Any]] = []
    candidates = value if isinstance(value, list) else [value]
    for item in candidates:
        if not isinstance(item, dict):
            continue
        name = _sanitize_upload_name(item.get("name") or item.get("filename"))
        if not name:
            continue
        relative_path = _sanitize_relative_upload_path(item.get("relative_path"), fallback_name=name)
        if not relative_path:
            continue
        client_key = _normalize_text(item.get("client_key")) or relative_path
        rows.append(
            {
                "client_key": client_key,
                "name": name,
                "relative_path": relative_path,
                "size_bytes": int(item.get("size_bytes") or 0),
                "modified_at": int(item.get("modified_at") or 0),
            }
        )
    deduped: dict[str, Dict[str, Any]] = {}
    for row in rows:
        deduped[row["client_key"]] = row
    return list(deduped.values())


def _upload_manifest_from_form(form_value: Any, files: Iterable[Any]) -> list[Dict[str, Any]]:
    raw_value = form_value
    if isinstance(raw_value, str):
        raw_value = raw_value.strip()
        if raw_value:
            try:
                raw_value = json.loads(raw_value)
            except Exception:
                raw_value = []
        else:
            raw_value = []
    manifest_items = _normalize_upload_manifest_items(raw_value)
    if manifest_items:
        return manifest_items
    fallback_rows = []
    for storage in files:
        filename = _sanitize_upload_name(getattr(storage, "filename", ""))
        if not filename:
            continue
        fallback_rows.append(
            {
                "client_key": filename,
                "name": filename,
                "relative_path": filename,
                "size_bytes": 0,
                "modified_at": 0,
            }
        )
    return _normalize_upload_manifest_items(fallback_rows)


def _manifest_has_only_empty_files(manifest_items: Iterable[Dict[str, Any]]) -> bool:
    rows = list(manifest_items or [])
    if not rows:
        return False
    for row in rows:
        if not _sanitize_upload_name((row or {}).get("name")):
            return False
        if not _sanitize_relative_upload_path((row or {}).get("relative_path"), fallback_name=(row or {}).get("name")):
            return False
        try:
            if int((row or {}).get("size_bytes") or 0) != 0:
                return False
        except Exception:
            return False
    return True


def _transfer_status_snapshot(session: Dict[str, Any]) -> Dict[str, Any]:
    return {
        "transfer_id": session.get("transfer_id") or "",
        "direction": session.get("direction") or "",
        "status": session.get("status") or "pending",
        "cancel_requested": bool(session.get("cancel_requested")),
        "bytes_complete": int(session.get("bytes_complete") or 0),
        "bytes_total": int(session.get("bytes_total") or 0),
        "item_count": int(session.get("item_count") or 0),
        "archive_name": session.get("archive_name") or "",
        "error": session.get("error") or "",
        "hostname": session.get("hostname") or "",
        "target_path": session.get("target_path") or "",
        "created_at": int(session.get("created_at") or 0),
        "updated_at": int(session.get("updated_at") or 0),
        "download_ready": bool(session.get("result_path")),
        "result_name": session.get("result_name") or "",
    }


class FileTransferStore:
    def __init__(self, root_dir: Path, *, ttl_seconds: int, logger) -> None:
        self._root_dir = root_dir
        self._ttl_seconds = max(300, int(ttl_seconds or FILE_TRANSFER_SESSION_TTL_SECONDS))
        self._logger = logger
        self._lock = threading.RLock()
        self._sessions: dict[str, Dict[str, Any]] = {}
        self._root_dir.mkdir(parents=True, exist_ok=True)
        self._startup_sweep()

    def _startup_sweep(self) -> None:
        try:
            for child in self._root_dir.iterdir():
                if child.is_dir():
                    shutil.rmtree(child, ignore_errors=True)
                else:
                    child.unlink(missing_ok=True)
        except Exception:
            self._logger.debug("File transfer store startup sweep failed.", exc_info=True)

    def cleanup_expired(self) -> None:
        now_ts = int(time.time())
        expired: list[str] = []
        with self._lock:
            for transfer_id, session in list(self._sessions.items()):
                expires_at = int(session.get("expires_at") or 0)
                if expires_at and expires_at > now_ts:
                    continue
                expired.append(transfer_id)
            for transfer_id in expired:
                session = self._sessions.pop(transfer_id, None)
                if not session:
                    continue
                self._remove_session_files(session)

    def _remove_session_files(self, session: Dict[str, Any]) -> None:
        session_dir = session.get("session_dir")
        try:
            if session_dir:
                shutil.rmtree(session_dir, ignore_errors=True)
        except Exception:
            self._logger.debug("Failed to remove file-management session temp files.", exc_info=True)

    def delete_session(self, transfer_id: str) -> None:
        normalized = _normalize_text(transfer_id)
        if not normalized:
            return
        with self._lock:
            session = self._sessions.pop(normalized, None)
        if session:
            self._remove_session_files(session)

    def _session_dir_for(self, transfer_id: str) -> Path:
        return self._root_dir / transfer_id

    def create_upload_session(
        self,
        *,
        hostname: str,
        device_guid: str,
        agent_id: str,
        operator_id: str,
        target_path: str,
        files: Iterable[Any],
        manifest_items: Optional[Iterable[Dict[str, Any]]] = None,
        overwrite_keys: Optional[Iterable[str]] = None,
        overwrite_names: Optional[Iterable[str]] = None,
    ) -> Dict[str, Any]:
        self.cleanup_expired()
        transfer_id = uuid.uuid4().hex
        session_dir = self._session_dir_for(transfer_id)
        uploads_dir = session_dir / "uploads"
        uploads_dir.mkdir(parents=True, exist_ok=True)
        item_rows: list[Dict[str, Any]] = []
        bytes_total = 0
        manifest_list = list(manifest_items or [])
        upload_list = list(files or [])
        manifest_only_empty_upload = not upload_list and _manifest_has_only_empty_files(manifest_list)
        if manifest_list and len(manifest_list) != len(upload_list) and not manifest_only_empty_upload:
            shutil.rmtree(session_dir, ignore_errors=True)
            raise ValueError("upload_manifest_mismatch")
        overwrite_source = list(overwrite_keys or []) or list(overwrite_names or [])
        overwrite_lookup = {_normalize_text(key): True for key in overwrite_source if _normalize_text(key)}
        if manifest_only_empty_upload:
            paired_rows = ((None, manifest_row) for manifest_row in manifest_list)
        elif manifest_list:
            paired_rows = zip(upload_list, manifest_list)
        else:
            paired_rows = (
                (storage, {"name": getattr(storage, "filename", ""), "relative_path": getattr(storage, "filename", "")})
                for storage in upload_list
            )
        for storage, manifest_row in paired_rows:
            filename = _sanitize_upload_name((manifest_row or {}).get("name") or getattr(storage, "filename", ""))
            relative_path = _sanitize_relative_upload_path((manifest_row or {}).get("relative_path"), fallback_name=filename)
            client_key = _normalize_text((manifest_row or {}).get("client_key")) or relative_path
            if not filename or not relative_path:
                continue
            item_id = uuid.uuid4().hex
            stored_path = uploads_dir / f"{item_id}.bin"
            if storage is None:
                stored_path.write_bytes(b"")
            else:
                storage.save(stored_path)
            try:
                size_bytes = int(stored_path.stat().st_size)
            except Exception:
                size_bytes = 0
            item_rows.append(
                {
                    "item_id": item_id,
                    "client_key": client_key,
                    "name": filename,
                    "relative_path": relative_path,
                    "size_bytes": size_bytes,
                    "stored_path": str(stored_path),
                    "overwrite_existing": bool(overwrite_lookup.get(client_key)),
                }
            )
            bytes_total += size_bytes
        if not item_rows:
            shutil.rmtree(session_dir, ignore_errors=True)
            raise ValueError("upload_files_required")
        now_ts = int(time.time())
        session = {
            "transfer_id": transfer_id,
            "direction": "upload",
            "status": "pending",
            "cancel_requested": False,
            "bytes_complete": 0,
            "bytes_total": bytes_total,
            "item_count": len(item_rows),
            "archive_name": "",
            "error": "",
            "hostname": hostname,
            "device_guid": normalize_guid(device_guid),
            "agent_id": agent_id,
            "operator_id": operator_id,
            "target_path": target_path,
            "created_at": now_ts,
            "updated_at": now_ts,
            "expires_at": now_ts + self._ttl_seconds,
            "session_dir": str(session_dir),
            "upload_items": item_rows,
            "selections": [],
            "result_path": "",
            "result_name": "",
            "result_mime": "",
        }
        with self._lock:
            self._sessions[transfer_id] = session
        return _transfer_status_snapshot(session)

    def create_download_session(
        self,
        *,
        hostname: str,
        device_guid: str,
        agent_id: str,
        operator_id: str,
        selections: list[Dict[str, Any]],
        archive_name: str,
    ) -> Dict[str, Any]:
        self.cleanup_expired()
        transfer_id = uuid.uuid4().hex
        session_dir = self._session_dir_for(transfer_id)
        session_dir.mkdir(parents=True, exist_ok=True)
        now_ts = int(time.time())
        session = {
            "transfer_id": transfer_id,
            "direction": "download",
            "status": "pending",
            "cancel_requested": False,
            "bytes_complete": 0,
            "bytes_total": 0,
            "item_count": len(selections),
            "archive_name": archive_name,
            "error": "",
            "hostname": hostname,
            "device_guid": normalize_guid(device_guid),
            "agent_id": agent_id,
            "operator_id": operator_id,
            "target_path": "",
            "created_at": now_ts,
            "updated_at": now_ts,
            "expires_at": now_ts + self._ttl_seconds,
            "session_dir": str(session_dir),
            "upload_items": [],
            "selections": [dict(row) for row in selections],
            "result_path": "",
            "result_name": "",
            "result_mime": "",
        }
        with self._lock:
            self._sessions[transfer_id] = session
        return _transfer_status_snapshot(session)

    def get_session(self, transfer_id: str) -> Optional[Dict[str, Any]]:
        self.cleanup_expired()
        normalized = _normalize_text(transfer_id)
        if not normalized:
            return None
        with self._lock:
            session = self._sessions.get(normalized)
            return dict(session) if isinstance(session, dict) else None

    def get_status(self, transfer_id: str) -> Optional[Dict[str, Any]]:
        session = self.get_session(transfer_id)
        return _transfer_status_snapshot(session) if session else None

    def mark_progress(
        self,
        transfer_id: str,
        *,
        status: str = "",
        bytes_complete: Optional[int] = None,
        bytes_total: Optional[int] = None,
        error: str = "",
        archive_name: str = "",
    ) -> Optional[Dict[str, Any]]:
        normalized = _normalize_text(transfer_id)
        if not normalized:
            return None
        with self._lock:
            session = self._sessions.get(normalized)
            if not session:
                return None
            if status:
                session["status"] = _normalize_text(status).lower() or session.get("status") or "pending"
            if bytes_complete is not None:
                session["bytes_complete"] = max(0, int(bytes_complete))
            if bytes_total is not None:
                session["bytes_total"] = max(0, int(bytes_total))
            if error:
                session["error"] = error
            if archive_name:
                session["archive_name"] = archive_name
            session["updated_at"] = int(time.time())
            session["expires_at"] = int(time.time()) + self._ttl_seconds
            return _transfer_status_snapshot(session)

    def mark_failed(self, transfer_id: str, error: str) -> Optional[Dict[str, Any]]:
        return self.mark_progress(transfer_id, status="failed", error=_normalize_text(error) or "transfer_failed")

    def request_cancel(self, transfer_id: str) -> Optional[Dict[str, Any]]:
        normalized = _normalize_text(transfer_id)
        if not normalized:
            return None
        with self._lock:
            session = self._sessions.get(normalized)
            if not session:
                return None
            session["cancel_requested"] = True
            current_status = _normalize_text(session.get("status")).lower() or "pending"
            if current_status not in {"completed", "failed", "canceled"}:
                session["status"] = "canceling"
            session["updated_at"] = int(time.time())
            session["expires_at"] = int(time.time()) + self._ttl_seconds
            return _transfer_status_snapshot(session)

    def mark_canceled(self, transfer_id: str, error: str = "") -> Optional[Dict[str, Any]]:
        normalized = _normalize_text(transfer_id)
        if not normalized:
            return None
        with self._lock:
            session = self._sessions.get(normalized)
            if not session:
                return None
            session["cancel_requested"] = True
            session["status"] = "canceled"
            session["error"] = _normalize_text(error) or "Transfer canceled by operator."
            session["updated_at"] = int(time.time())
            session["expires_at"] = int(time.time()) + self._ttl_seconds
            return _transfer_status_snapshot(session)

    def get_upload_item(self, transfer_id: str, item_id: str) -> Optional[Tuple[Dict[str, Any], Dict[str, Any]]]:
        normalized_transfer = _normalize_text(transfer_id)
        normalized_item = _normalize_text(item_id)
        if not normalized_transfer or not normalized_item:
            return None
        with self._lock:
            session = self._sessions.get(normalized_transfer)
            if not session or session.get("direction") != "upload":
                return None
            for row in session.get("upload_items") or []:
                if _normalize_text(row.get("item_id")) == normalized_item:
                    return dict(session), dict(row)
        return None

    def store_download_artifact(
        self,
        transfer_id: str,
        *,
        file_storage: Any,
        archive_name: str = "",
        mime_type: str = "",
    ) -> Optional[Dict[str, Any]]:
        normalized = _normalize_text(transfer_id)
        if not normalized:
            return None
        with self._lock:
            session = self._sessions.get(normalized)
            if not session or session.get("direction") != "download":
                return None
            session_dir = Path(session.get("session_dir") or self._session_dir_for(normalized))
            download_dir = session_dir / "download"
            download_dir.mkdir(parents=True, exist_ok=True)
            existing_path = _normalize_text(session.get("result_path"))
            if existing_path:
                try:
                    Path(existing_path).unlink(missing_ok=True)
                except Exception:
                    self._logger.debug("Failed to remove previous file-management artifact.", exc_info=True)
            resolved_name = _sanitize_upload_name(archive_name or getattr(file_storage, "filename", ""))
            if not resolved_name:
                resolved_name = session.get("archive_name") or f"{normalized}.bin"
            artifact_path = download_dir / resolved_name
            file_storage.save(artifact_path)
            try:
                size_bytes = int(artifact_path.stat().st_size)
            except Exception:
                size_bytes = 0
            session["result_path"] = str(artifact_path)
            session["result_name"] = resolved_name
            session["result_mime"] = _normalize_text(mime_type) or mimetypes.guess_type(resolved_name)[0] or "application/octet-stream"
            session["bytes_total"] = size_bytes
            session["bytes_complete"] = size_bytes
            session["status"] = "completed"
            session["error"] = ""
            session["updated_at"] = int(time.time())
            session["expires_at"] = int(time.time()) + self._ttl_seconds
            return _transfer_status_snapshot(session)
