# ======================================================
# Data\Engine\services\API\devices\file_management.py
# Description: Remote file-management browse, mutation, and transfer endpoints.
#
# API Endpoints (if applicable):
# - GET /api/device/files/<hostname>/roots (Token Authenticated) - Returns the remote file-management roots view for an in-scope device.
# - GET /api/device/files/<hostname>/children (Token Authenticated) - Returns direct children for a remote directory on an in-scope device.
# - POST /api/device/files/<hostname>/upload/conflicts (Token Authenticated) - Preflights upload name conflicts against a remote directory on an in-scope device.
# - GET /api/device/files/<hostname>/text (Token Authenticated) - Reads one lightweight-editable remote text file from an in-scope device.
# - POST /api/device/files/<hostname>/text (Token Authenticated) - Saves one lightweight-editable remote text file on an in-scope device.
# - POST /api/device/files/<hostname>/mkdir (Token Authenticated) - Creates a remote directory on an in-scope device.
# - POST /api/device/files/<hostname>/rename (Token Authenticated) - Renames one remote file-system item on an in-scope device.
# - POST /api/device/files/<hostname>/move (Token Authenticated) - Moves remote file-system items on an in-scope device.
# - POST /api/device/files/<hostname>/paste (Token Authenticated) - Pastes copied or cut remote file-system items into a destination directory on an in-scope device.
# - POST /api/device/files/<hostname>/delete (Token Authenticated) - Deletes remote file-system items on an in-scope device.
# - POST /api/device/files/<hostname>/upload (Token Authenticated) - Stages browser-uploaded files for transfer to an in-scope device.
# - POST /api/device/files/<hostname>/download (Token Authenticated) - Starts a remote file download transfer from an in-scope device.
# - GET /api/device/files/<hostname>/transfer/<transfer_id>/status (Token Authenticated) - Returns a staged transfer status snapshot.
# - POST /api/device/files/<hostname>/transfer/<transfer_id>/cancel (Token Authenticated) - Requests cancellation for a staged transfer.
# - GET /api/device/files/<hostname>/transfer/<transfer_id>/content (Token Authenticated) - Downloads a completed transfer artifact from Engine temp storage.
# - GET /api/agent/files/transfers/<transfer_id>/upload-item/<item_id> (Device Authenticated) - Streams one staged upload item to the device.
# - GET /api/agent/files/transfers/<transfer_id>/status (Device Authenticated) - Returns one staged transfer control snapshot to the device.
# - POST /api/agent/files/transfers/<transfer_id>/progress (Device Authenticated) - Updates the Engine-side transfer progress snapshot.
# - POST /api/agent/files/transfers/<transfer_id>/content (Device Authenticated) - Uploads a completed device-side transfer artifact back to the Engine.
# ======================================================

"""Remote file-management browse, mutation, and transfer endpoints for the Borealis Engine."""

from __future__ import annotations

import mimetypes
import os
import shutil
import tempfile
import threading
import time
import uuid
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, Iterable, List, Optional, Tuple

import requests
from flask import Blueprint, Response, g, jsonify, request, send_file, stream_with_context

from ....auth.device_auth import require_device_auth
from ....auth.guid_utils import normalize_guid
from ...auth import UserSiteAccessManager
from ...auth.secrets import require_app_secret
from ...job_scheduler.queue import active_worker_route_for_site, ensure_job_scheduler_tables
from ...job_scheduler.security import INTERNAL_TOKEN_HEADER, internal_token
from ...remote_ops.agent_routes import site_worker_route_urls
from .tunnel import _current_user, _require_login, _resolve_requested_agent_id

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from .. import EngineServiceAdapters


FILE_TRANSFER_SESSION_TTL_SECONDS = int(os.environ.get("BOREALIS_FILE_TRANSFER_TTL_SECONDS", 60 * 60))


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _request_remote() -> str:
    forwarded = _normalize_text(request.headers.get("X-Forwarded-For"))
    if forwarded:
        return forwarded.split(",", 1)[0].strip()
    return _normalize_text(request.remote_addr)


def _sanitize_upload_name(value: Any) -> str:
    raw = _normalize_text(value).replace("\\", "/")
    if not raw:
        return ""
    name = Path(raw).name.strip().replace("\x00", "")
    return name


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


def _normalize_conflict_resolution_map(value: Any) -> dict[str, str]:
    if isinstance(value, str):
        raw = value.strip()
        if not raw:
            return {}
        try:
            import json

            value = json.loads(raw)
        except Exception:
            return {}
    if not isinstance(value, dict):
        return {}
    rows: dict[str, str] = {}
    for key, resolution in value.items():
        name = _normalize_text(key)
        choice = _normalize_text(resolution).lower()
        if not name or choice not in {"replace", "skip"}:
            continue
        rows[name] = choice
    return rows


def _guess_download_name(hostname: str, selections: list[Dict[str, Any]], *, archive_required: bool) -> str:
    now_label = time.strftime("%Y%m%d-%H%M%S")
    if archive_required:
        normalized_host = _normalize_text(hostname) or "device"
        return f"{normalized_host}-files-{now_label}.zip"
    if not selections:
        return f"download-{now_label}.bin"
    only = selections[0]
    return _sanitize_upload_name(only.get("name") or Path(_normalize_text(only.get("path"))).name) or f"download-{now_label}.bin"


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
        if manifest_list and len(manifest_list) != len(upload_list):
            shutil.rmtree(session_dir, ignore_errors=True)
            raise ValueError("upload_manifest_mismatch")
        overwrite_source = list(overwrite_keys or []) or list(overwrite_names or [])
        overwrite_lookup = {_normalize_text(key): True for key in overwrite_source if _normalize_text(key)}
        paired_rows = zip(upload_list, manifest_list) if manifest_list else (
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


def _get_transfer_store(context: Any, logger) -> FileTransferStore:
    existing = getattr(context, "file_transfer_store", None)
    if isinstance(existing, FileTransferStore):
        return existing
    lock = getattr(context, "_file_transfer_store_init_lock", None)
    if lock is None:
        lock = threading.Lock()
        setattr(context, "_file_transfer_store_init_lock", lock)
    with lock:
        current = getattr(context, "file_transfer_store", None)
        if isinstance(current, FileTransferStore):
            return current
        root_dir = Path(tempfile.gettempdir()) / "Borealis" / "engine_file_management"
        store = FileTransferStore(root_dir, ttl_seconds=FILE_TRANSFER_SESSION_TTL_SECONDS, logger=logger)
        setattr(context, "file_transfer_store", store)
        return store


def _system_socket_available(app, adapters: "EngineServiceAdapters", record: Dict[str, Any]) -> bool:
    route, error = _worker_route_for_record(adapters, record)
    if error or route is None:
        return False
    payload, rpc_error = _post_worker_json(
        app,
        route,
        "/remote-ops/host-service/status",
        {
            "hostname": record.get("hostname") or "",
            "service_mode": "system",
        },
        timeout=5.0,
    )
    if rpc_error:
        return False
    return bool((payload or {}).get("registered"))


def _load_device_record(adapters: "EngineServiceAdapters", hostname: str) -> Optional[Dict[str, Any]]:
    conn = None
    try:
        conn = adapters.db_conn_factory()
        cur = conn.cursor()
        cur.execute(
            """
            SELECT d.guid, d.hostname, d.agent_id, d.operating_system, d.last_seen, ds.site_id
              FROM devices AS d
         LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
             WHERE LOWER(d.hostname) = LOWER(?)
          ORDER BY d.last_seen DESC
             LIMIT 1
            """,
            (hostname,),
        )
        row = cur.fetchone()
        if not row:
            return None
        return {
            "guid": normalize_guid(row[0]),
            "hostname": _normalize_text(row[1]),
            "agent_id": _normalize_text(row[2]),
            "operating_system": _normalize_text(row[3]),
            "last_seen": int(row[4] or 0),
            "site_id": int(row[5] or 0),
        }
    finally:
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass


def _worker_route_url(worker_route: Dict[str, Any], path: str) -> str:
    scheme = _normalize_text(worker_route.get("upstream_scheme")) or "http"
    host = _normalize_text(worker_route.get("upstream_host")) or "127.0.0.1"
    try:
        port = int(worker_route.get("upstream_port") or 0)
    except Exception:
        port = 0
    if port <= 0:
        raise RuntimeError("site_worker_route_missing_port")
    normalized_path = _normalize_text(path)
    if not normalized_path.startswith("/"):
        normalized_path = f"/{normalized_path}"
    return f"{scheme}://{host}:{port}{normalized_path}"


def _worker_route_for_record(
    adapters: "EngineServiceAdapters",
    record: Dict[str, Any],
) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
    try:
        site_id = int(record.get("site_id") or 0)
    except Exception:
        site_id = 0
    if site_id <= 0:
        return None, ({"error": "device_site_unassigned"}, 409)
    conn = None
    try:
        conn = adapters.db_conn_factory()
        ensure_job_scheduler_tables(conn)
        route = active_worker_route_for_site(conn, site_id=site_id)
    finally:
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass
    if not route:
        return None, ({"error": "site_worker_unavailable", "message": "No active site-worker route is available for this device site."}, 503)
    return dict(route), None


def _worker_internal_headers(app) -> Dict[str, str]:
    return {
        INTERNAL_TOKEN_HEADER: internal_token(require_app_secret(app)),
        "Accept": "application/json",
    }


def _post_worker_json(
    app,
    worker_route: Dict[str, Any],
    path: str,
    payload: Dict[str, Any],
    *,
    timeout: float = 30.0,
) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
    try:
        response = requests.post(
            _worker_route_url(worker_route, path),
            headers={**_worker_internal_headers(app), "Content-Type": "application/json"},
            json=dict(payload or {}),
            timeout=max(1.0, float(timeout)),
        )
    except Exception:
        return None, ({"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}, 503)
    try:
        data = response.json() if response.headers.get("content-type", "").lower().startswith("application/json") else {}
    except Exception:
        data = {}
    if response.status_code >= 400:
        return None, (data if isinstance(data, dict) else {"error": "site_worker_error"}, response.status_code)
    return data if isinstance(data, dict) else {}, None


def _get_worker_json(
    app,
    worker_route: Dict[str, Any],
    path: str,
    *,
    timeout: float = 15.0,
) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
    try:
        response = requests.get(
            _worker_route_url(worker_route, path),
            headers=_worker_internal_headers(app),
            timeout=max(1.0, float(timeout)),
        )
    except Exception:
        return None, ({"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}, 503)
    try:
        data = response.json() if response.headers.get("content-type", "").lower().startswith("application/json") else {}
    except Exception:
        data = {}
    if response.status_code >= 400:
        return None, (data if isinstance(data, dict) else {"error": "site_worker_error"}, response.status_code)
    return data if isinstance(data, dict) else {}, None


def _post_worker_upload(
    app,
    worker_route: Dict[str, Any],
    *,
    hostname: str,
    device_guid: str,
    agent_id: str,
    operator_id: str,
    target_path: str,
    transfer_base_url: str,
    files: Iterable[Any],
    manifest_items: Iterable[Dict[str, Any]],
    overwrite_keys: Iterable[str],
) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
    import json as _json

    multipart_files = []
    for storage in files:
        try:
            storage.stream.seek(0)
        except Exception:
            pass
        multipart_files.append(
            (
                "files",
                (
                    _sanitize_upload_name(getattr(storage, "filename", "")) or "upload.bin",
                    storage.stream,
                    _normalize_text(getattr(storage, "mimetype", "")) or "application/octet-stream",
                ),
            )
        )
    data = {
        "hostname": hostname,
        "device_guid": device_guid,
        "agent_id": agent_id,
        "operator_id": operator_id,
        "target_path": target_path,
        "transfer_base_url": transfer_base_url,
        "manifest": _json.dumps(list(manifest_items or []), separators=(",", ":")),
        "overwrite_keys": _json.dumps([_normalize_text(key) for key in overwrite_keys if _normalize_text(key)], separators=(",", ":")),
    }
    try:
        response = requests.post(
            _worker_route_url(worker_route, "/remote-files/transfers/upload"),
            headers=_worker_internal_headers(app),
            data=data,
            files=multipart_files,
            timeout=900.0,
        )
    except Exception:
        return None, ({"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}, 503)
    try:
        payload = response.json()
    except Exception:
        payload = {}
    if response.status_code >= 400:
        return None, (payload if isinstance(payload, dict) else {"error": "site_worker_error"}, response.status_code)
    return payload if isinstance(payload, dict) else {}, None


def _rpc_error_response(response: Any) -> Tuple[Dict[str, Any], int]:
    if not isinstance(response, dict):
        return {"error": "agent_unavailable", "message": "The device file-management channel did not respond."}, 503
    error_code = _normalize_text(response.get("error")).lower() or "agent_error"
    message = _normalize_text(response.get("message")) or error_code
    if error_code == "invalid_request" and "Unsupported file-management action" in message:
        return {
            "error": "agent_update_required",
            "message": "The device agent needs to be updated before this File Management capability is available.",
        }, 409
    status_code = {
        "not_found": 404,
        "path_not_found": 404,
        "file_not_found": 404,
        "directory_not_found": 404,
        "not_a_directory": 400,
        "not_a_file": 400,
        "invalid_path": 400,
        "invalid_name": 400,
        "invalid_request": 400,
        "file_too_large": 413,
        "binary_not_supported": 415,
        "text_encoding_not_supported": 415,
        "permission_denied": 403,
        "conflict": 409,
        "path_conflict": 409,
        "transfer_canceled": 409,
        "agent_unavailable": 503,
        "timeout": 504,
    }.get(error_code, 502)
    return {"error": error_code, "message": message}, status_code


def _call_file_management_rpc(
    adapters: "EngineServiceAdapters",
    *,
    app,
    record: Dict[str, Any],
    hostname: str,
    payload: Dict[str, Any],
    timeout: float = 30.0,
) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
    route, route_error = _worker_route_for_record(adapters, record)
    if route_error:
        return None, route_error
    assert route is not None
    data, worker_error = _post_worker_json(
        app,
        route,
        "/remote-ops/host-service/call",
        {
            "hostname": hostname,
            "service_mode": "system",
            "event_name": "file_management_request",
            "payload": payload,
            "timeout_seconds": max(0.5, float(timeout)),
        },
        timeout=max(1.0, float(timeout) + 1.0),
    )
    if worker_error:
        return None, worker_error
    if not bool((data or {}).get("called")):
        return None, ({"error": "timeout", "message": "The device did not answer the file-management request in time."}, 504)
    response = (data or {}).get("response")
    if not isinstance(response, dict):
        return None, ({"error": "invalid_agent_response", "message": "The device returned an invalid file-management response."}, 502)
    if response.get("ok") is False:
        return None, _rpc_error_response(response)
    return response, None


def _upload_manifest_from_form(form_value: Any, files: Iterable[Any]) -> list[Dict[str, Any]]:
    raw_value = form_value
    if isinstance(raw_value, str):
        raw_value = raw_value.strip()
        if raw_value:
            try:
                import json

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


def register_file_management(app, adapters: "EngineServiceAdapters") -> None:
    """Register remote file-management routes."""

    blueprint = Blueprint("device_file_management", __name__)
    logger = adapters.context.logger.getChild("device.file_management")
    log = adapters.service_log
    auth_manager = adapters.device_auth_manager
    site_access = UserSiteAccessManager(adapters.db_conn_factory, logger=logger)

    def _service_log_event(message: str, *, level: str = "INFO") -> None:
        if not callable(log):
            return
        try:
            log("file_management", message, level=level)
        except Exception:
            logger.debug("file-management service log write failed", exc_info=True)

    def _store() -> FileTransferStore:
        return _get_transfer_store(adapters.context, logger)

    def _device_auth_context() -> Any:
        return getattr(g, "device_auth", None)

    def _operator_context(hostname: str) -> Tuple[Optional[Dict[str, Any]], Optional[Dict[str, Any]], Optional[str], Optional[Tuple[Dict[str, Any], int]]]:
        requirement = _require_login(app)
        if requirement:
            return None, None, None, requirement
        user = _current_user(app) or {}
        if not site_access.user_can_access_hostname(user, hostname):
            return None, None, None, ({"error": "not found"}, 404)
        record = _load_device_record(adapters, hostname)
        if record is None:
            return None, None, None, ({"error": "not found"}, 404)
        agent_id = _resolve_requested_agent_id(adapters, record.get("agent_id"), expected_guid=record.get("guid"))
        record["agent_id"] = agent_id
        operator_id = _normalize_text(user.get("username")) or "unknown"
        return user, record, operator_id, None

    def _validate_operator_transfer(hostname: str, transfer_id: str) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
        requirement = _require_login(app)
        if requirement:
            return None, requirement
        user = _current_user(app) or {}
        if not site_access.user_can_access_hostname(user, hostname):
            return None, ({"error": "not found"}, 404)
        session = _store().get_session(transfer_id)
        if session is None:
            return None, ({"error": "transfer_not_found"}, 404)
        if _normalize_text(session.get("hostname")).lower() != _normalize_text(hostname).lower():
            return None, ({"error": "transfer_not_found"}, 404)
        return session, None

    def _agent_transfer_session(transfer_id: str) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
        ctx = _device_auth_context()
        if ctx is None:
            return None, ({"error": "auth_context_missing"}, 500)
        if _normalize_text(getattr(ctx, "service_mode", "")) not in {"", "SYSTEM"}:
            return None, ({"error": "context_not_allowed"}, 403)
        session = _store().get_session(transfer_id)
        if session is None:
            return None, ({"error": "transfer_not_found"}, 404)
        if normalize_guid(getattr(ctx, "guid", "")) != normalize_guid(session.get("device_guid")):
            return None, ({"error": "transfer_not_found"}, 404)
        return session, None

    @blueprint.route("/api/device/files/<hostname>/roots", methods=["GET"])
    def get_roots(hostname: str):
        user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        assert record is not None
        if not _system_socket_available(app, adapters, record):
            return jsonify({"error": "agent_unavailable"}), 503
        response, rpc_error = _call_file_management_rpc(
            adapters,
            app=app,
            record=record,
            hostname=record.get("hostname") or hostname,
            payload={
                "action": "roots",
                "hostname": record.get("hostname") or hostname,
                "agent_id": record.get("agent_id") or "",
                "requested_by": operator_id,
            },
        )
        if rpc_error:
            payload, status = rpc_error
            return jsonify(payload), status
        assert response is not None
        return jsonify(
            {
                "hostname": record.get("hostname") or hostname,
                "platform": _normalize_text(response.get("platform")).lower(),
                "context_label": response.get("context_label") or "",
                "current_path": response.get("current_path"),
                "entries": response.get("entries") if isinstance(response.get("entries"), list) else [],
            }
        )

    @blueprint.route("/api/device/files/<hostname>/children", methods=["GET"])
    def get_children(hostname: str):
        user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        requested_path = _normalize_text(request.args.get("path"))
        if not requested_path:
            return jsonify({"error": "path_required"}), 400
        assert record is not None
        if not _system_socket_available(app, adapters, record):
            return jsonify({"error": "agent_unavailable"}), 503
        response, rpc_error = _call_file_management_rpc(
            adapters,
            app=app,
            record=record,
            hostname=record.get("hostname") or hostname,
            payload={
                "action": "children",
                "hostname": record.get("hostname") or hostname,
                "agent_id": record.get("agent_id") or "",
                "requested_by": operator_id,
                "path": requested_path,
            },
        )
        if rpc_error:
            payload, status = rpc_error
            return jsonify(payload), status
        assert response is not None
        return jsonify(
            {
                "hostname": record.get("hostname") or hostname,
                "current_path": response.get("current_path") or requested_path,
                "entries": response.get("entries") if isinstance(response.get("entries"), list) else [],
            }
        )

    def _upload_conflicts_payload(record: Dict[str, Any], hostname: str, operator_id: str, target_path: str, items: list[Dict[str, Any]]):
        response, rpc_error = _call_file_management_rpc(
            adapters,
            app=app,
            record=record,
            hostname=record.get("hostname") or hostname,
            payload={
                "action": "upload_conflicts",
                "hostname": record.get("hostname") or hostname,
                "agent_id": record.get("agent_id") or "",
                "requested_by": operator_id,
                "target_path": target_path,
                "items": items,
            },
        )
        if rpc_error:
            return None, rpc_error
        assert response is not None
        return {
            "ok": True,
            "hostname": record.get("hostname") or hostname,
            "target_path": response.get("target_path") or target_path,
            "conflicts": response.get("conflicts") if isinstance(response.get("conflicts"), list) else [],
        }, None

    @blueprint.route("/api/device/files/<hostname>/upload/conflicts", methods=["POST"])
    def get_upload_conflicts(hostname: str):
        _user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        target_path = _normalize_text(body.get("target_path"))
        items = _normalize_upload_manifest_items(body.get("items"))
        if not target_path or not items:
            return jsonify({"error": "target_path_and_items_required"}), 400
        assert record is not None
        if not _system_socket_available(app, adapters, record):
            return jsonify({"error": "agent_unavailable"}), 503
        payload, rpc_error = _upload_conflicts_payload(record, hostname, operator_id, target_path, items)
        if rpc_error:
            error_payload, status = rpc_error
            if error_payload.get("error") == "agent_update_required":
                return jsonify(
                    {
                        "ok": True,
                        "hostname": record.get("hostname") or hostname,
                        "target_path": target_path,
                        "conflicts": [],
                        "capability_supported": False,
                        "message": error_payload.get("message") or "",
                    }
                )
            return jsonify(error_payload), status
        return jsonify(payload)

    @blueprint.route("/api/device/files/<hostname>/text", methods=["GET"])
    def read_text_file(hostname: str):
        _user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        requested_path = _normalize_text(request.args.get("path"))
        if not requested_path:
            return jsonify({"error": "path_required"}), 400
        assert record is not None
        if not _system_socket_available(app, adapters, record):
            return jsonify({"error": "agent_unavailable"}), 503
        response, rpc_error = _call_file_management_rpc(
            adapters,
            app=app,
            record=record,
            hostname=record.get("hostname") or hostname,
            payload={
                "action": "read_text",
                "hostname": record.get("hostname") or hostname,
                "agent_id": record.get("agent_id") or "",
                "requested_by": operator_id,
                "path": requested_path,
            },
        )
        if rpc_error:
            payload, status = rpc_error
            return jsonify(payload), status
        assert response is not None
        return jsonify(
            {
                "ok": True,
                "hostname": record.get("hostname") or hostname,
                "path": response.get("path") or requested_path,
                "content": response.get("content") or "",
                "encoding": response.get("encoding") or "utf-8",
                "line_ending": response.get("line_ending") or "lf",
                "size_bytes": int(response.get("size_bytes") or 0),
                "modified_at": int(response.get("modified_at") or 0),
                "entry": response.get("entry") if isinstance(response.get("entry"), dict) else None,
            }
        )

    @blueprint.route("/api/device/files/<hostname>/text", methods=["POST"])
    def write_text_file(hostname: str):
        _user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        item_path = _normalize_text(body.get("path"))
        if not item_path:
            return jsonify({"error": "path_required"}), 400
        assert record is not None
        if not _system_socket_available(app, adapters, record):
            return jsonify({"error": "agent_unavailable"}), 503
        response, rpc_error = _call_file_management_rpc(
            adapters,
            app=app,
            record=record,
            hostname=record.get("hostname") or hostname,
            payload={
                "action": "write_text",
                "hostname": record.get("hostname") or hostname,
                "agent_id": record.get("agent_id") or "",
                "requested_by": operator_id,
                "path": item_path,
                "content": body.get("content"),
                "encoding": body.get("encoding"),
                "line_ending": body.get("line_ending"),
            },
        )
        if rpc_error:
            payload, status = rpc_error
            return jsonify(payload), status
        _service_log_event(
            "file_management_write_text hostname={hostname} operator={operator} remote={remote} path={path}".format(
                hostname=record.get("hostname") or hostname,
                operator=operator_id,
                remote=_request_remote() or "-",
                path=item_path,
            )
        )
        assert response is not None
        return jsonify(
            {
                "ok": True,
                "path": response.get("path") or item_path,
                "encoding": response.get("encoding") or "utf-8",
                "line_ending": response.get("line_ending") or "lf",
                "size_bytes": int(response.get("size_bytes") or 0),
                "modified_at": int(response.get("modified_at") or 0),
                "entry": response.get("entry") if isinstance(response.get("entry"), dict) else None,
            }
        )

    @blueprint.route("/api/device/files/<hostname>/mkdir", methods=["POST"])
    def create_directory(hostname: str):
        user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        parent_path = _normalize_text(body.get("path"))
        name = _normalize_text(body.get("name"))
        if not parent_path or not name:
            return jsonify({"error": "path_and_name_required"}), 400
        response, rpc_error = _call_file_management_rpc(
            adapters,
            app=app,
            record=record,
            hostname=record.get("hostname") or hostname,
            payload={
                "action": "mkdir",
                "hostname": record.get("hostname") or hostname,
                "agent_id": record.get("agent_id") or "",
                "requested_by": operator_id,
                "path": parent_path,
                "name": name,
            },
        )
        if rpc_error:
            payload, status = rpc_error
            return jsonify(payload), status
        _service_log_event(
            "file_management_mkdir hostname={hostname} operator={operator} remote={remote} path={path} name={name}".format(
                hostname=record.get("hostname") or hostname,
                operator=operator_id,
                remote=_request_remote() or "-",
                path=parent_path,
                name=name,
            )
        )
        return jsonify({"ok": True, "entry": response.get("entry") if isinstance(response, dict) else None})

    @blueprint.route("/api/device/files/<hostname>/rename", methods=["POST"])
    def rename_item(hostname: str):
        user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        item_path = _normalize_text(body.get("path"))
        new_name = _normalize_text(body.get("new_name"))
        if not item_path or not new_name:
            return jsonify({"error": "path_and_new_name_required"}), 400
        response, rpc_error = _call_file_management_rpc(
            adapters,
            app=app,
            record=record,
            hostname=record.get("hostname") or hostname,
            payload={
                "action": "rename",
                "hostname": record.get("hostname") or hostname,
                "agent_id": record.get("agent_id") or "",
                "requested_by": operator_id,
                "path": item_path,
                "new_name": new_name,
            },
        )
        if rpc_error:
            payload, status = rpc_error
            return jsonify(payload), status
        _service_log_event(
            "file_management_rename hostname={hostname} operator={operator} remote={remote} path={path} new_name={new_name}".format(
                hostname=record.get("hostname") or hostname,
                operator=operator_id,
                remote=_request_remote() or "-",
                path=item_path,
                new_name=new_name,
            )
        )
        return jsonify({"ok": True, "entry": response.get("entry") if isinstance(response, dict) else None})

    @blueprint.route("/api/device/files/<hostname>/move", methods=["POST"])
    def move_items(hostname: str):
        user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        destination_path = _normalize_text(body.get("destination_path"))
        selections = _normalize_transfer_entries(body.get("paths") or body.get("items") or body.get("path"))
        if not destination_path or not selections:
            return jsonify({"error": "paths_and_destination_required"}), 400
        response, rpc_error = _call_file_management_rpc(
            adapters,
            app=app,
            record=record,
            hostname=record.get("hostname") or hostname,
            payload={
                "action": "move",
                "hostname": record.get("hostname") or hostname,
                "agent_id": record.get("agent_id") or "",
                "requested_by": operator_id,
                "paths": selections,
                "destination_path": destination_path,
            },
            timeout=120.0,
        )
        if rpc_error:
            payload, status = rpc_error
            return jsonify(payload), status
        _service_log_event(
            "file_management_move hostname={hostname} operator={operator} remote={remote} items={items} destination={destination}".format(
                hostname=record.get("hostname") or hostname,
                operator=operator_id,
                remote=_request_remote() or "-",
                items=len(selections),
                destination=destination_path,
            )
        )
        return jsonify({"ok": True, "moved": response.get("moved") if isinstance(response, dict) else []})

    @blueprint.route("/api/device/files/<hostname>/delete", methods=["POST"])
    def delete_items(hostname: str):
        user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        selections = _normalize_transfer_entries(body.get("paths") or body.get("items") or body.get("path"))
        if not selections:
            return jsonify({"error": "paths_required"}), 400
        response, rpc_error = _call_file_management_rpc(
            adapters,
            app=app,
            record=record,
            hostname=record.get("hostname") or hostname,
            payload={
                "action": "delete",
                "hostname": record.get("hostname") or hostname,
                "agent_id": record.get("agent_id") or "",
                "requested_by": operator_id,
                "paths": selections,
            },
            timeout=120.0,
        )
        if rpc_error:
            payload, status = rpc_error
            return jsonify(payload), status
        _service_log_event(
            "file_management_delete hostname={hostname} operator={operator} remote={remote} items={items}".format(
                hostname=record.get("hostname") or hostname,
                operator=operator_id,
                remote=_request_remote() or "-",
                items=len(selections),
            ),
            level="WARNING",
        )
        return jsonify({"ok": True, "deleted": response.get("deleted") if isinstance(response, dict) else []})

    @blueprint.route("/api/device/files/<hostname>/upload", methods=["POST"])
    def start_upload(hostname: str):
        _user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        target_path = _normalize_text(request.form.get("target_path"))
        files = request.files.getlist("files")
        conflict_resolutions = _normalize_conflict_resolution_map(request.form.get("conflict_resolutions"))
        upload_manifest = _upload_manifest_from_form(request.form.get("manifest"), files)
        if not target_path:
            return jsonify({"error": "target_path_required"}), 400
        if not files:
            return jsonify({"error": "upload_files_required"}), 400
        if not upload_manifest:
            return jsonify({"error": "upload_files_required"}), 400
        if len(upload_manifest) != len(files):
            return jsonify({"error": "upload_manifest_mismatch"}), 400
        assert record is not None
        if not _system_socket_available(app, adapters, record):
            return jsonify({"error": "agent_unavailable"}), 503
        conflict_payload, rpc_error = _upload_conflicts_payload(record, hostname, operator_id, target_path, upload_manifest)
        conflicts: list[Dict[str, Any]] = []
        legacy_conflict_support = False
        if rpc_error:
            payload, status = rpc_error
            if payload.get("error") == "agent_update_required":
                legacy_conflict_support = True
            else:
                return jsonify(payload), status
        else:
            assert conflict_payload is not None
            conflicts = conflict_payload.get("conflicts") if isinstance(conflict_payload.get("conflicts"), list) else []
        unresolved_conflicts = [conflict for conflict in conflicts if conflict_resolutions.get(_normalize_text(conflict.get("client_key"))) not in {"replace", "skip"}]
        if unresolved_conflicts:
            return (
                jsonify(
                    {
                        "error": "upload_conflicts",
                        "message": "The destination already contains one or more items with the same name.",
                        "target_path": conflict_payload.get("target_path") or target_path,
                        "conflicts": unresolved_conflicts,
                    }
                ),
                409,
            )
        if legacy_conflict_support and conflict_resolutions:
            return (
                jsonify(
                    {
                        "error": "agent_update_required",
                        "message": "The device agent needs to be updated before duplicate upload resolution is available.",
                    }
                ),
                409,
            )
        files_to_upload = []
        manifest_to_upload: list[Dict[str, Any]] = []
        overwrite_keys: list[str] = []
        skipped_names: list[str] = []
        for storage, manifest_row in zip(files, upload_manifest):
            filename = _sanitize_upload_name((manifest_row or {}).get("name") or getattr(storage, "filename", ""))
            client_key = _normalize_text((manifest_row or {}).get("client_key")) or _sanitize_upload_name(filename)
            if not filename or not client_key:
                continue
            resolution = conflict_resolutions.get(client_key)
            if resolution == "skip":
                skipped_names.append(filename)
                continue
            if resolution == "replace":
                overwrite_keys.append(client_key)
            files_to_upload.append(storage)
            manifest_to_upload.append(dict(manifest_row))
        if not files_to_upload:
            return jsonify(
                {
                    "ok": True,
                    "status": "skipped",
                    "target_path": target_path,
                    "skipped_count": len(skipped_names),
                }
            )
        worker_route, route_error = _worker_route_for_record(adapters, record)
        if route_error:
            payload, status = route_error
            return jsonify(payload), status
        assert worker_route is not None
        worker_urls = site_worker_route_urls(adapters.context, request, worker_route)
        session, worker_error = _post_worker_upload(
            app,
            worker_route,
            hostname=record.get("hostname") or hostname,
            device_guid=record.get("guid") or "",
            agent_id=record.get("agent_id") or "",
            operator_id=operator_id or "unknown",
            target_path=target_path,
            transfer_base_url=worker_urls["base"],
            files=files_to_upload,
            manifest_items=manifest_to_upload,
            overwrite_keys=overwrite_keys,
        )
        if worker_error:
            payload, status = worker_error
            return jsonify(payload), status
        assert session is not None
        _service_log_event(
            "file_management_upload_start hostname={hostname} operator={operator} remote={remote} transfer_id={transfer_id} items={items} bytes_total={bytes_total} target={target}".format(
                hostname=record.get("hostname") or hostname,
                operator=operator_id,
                remote=_request_remote() or "-",
                transfer_id=session["transfer_id"],
                items=session["item_count"],
                bytes_total=session["bytes_total"],
                target=target_path,
            )
        )
        return jsonify(session), 202

    @blueprint.route("/api/device/files/<hostname>/paste", methods=["POST"])
    def paste_items(hostname: str):
        _user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        destination_path = _normalize_text(body.get("destination_path"))
        selections = _normalize_transfer_entries(body.get("paths") or body.get("items") or body.get("path"))
        operation = _normalize_text(body.get("operation")).lower()
        if operation not in {"copy", "cut"}:
            return jsonify({"error": "operation_required"}), 400
        if not destination_path or not selections:
            return jsonify({"error": "paths_and_destination_required"}), 400
        response, rpc_error = _call_file_management_rpc(
            adapters,
            app=app,
            record=record,
            hostname=record.get("hostname") or hostname,
            payload={
                "action": "paste",
                "hostname": record.get("hostname") or hostname,
                "agent_id": record.get("agent_id") or "",
                "requested_by": operator_id,
                "operation": operation,
                "paths": selections,
                "destination_path": destination_path,
            },
            timeout=300.0,
        )
        if rpc_error:
            payload, status = rpc_error
            return jsonify(payload), status
        _service_log_event(
            "file_management_paste hostname={hostname} operator={operator} remote={remote} items={items} destination={destination} operation={operation}".format(
                hostname=record.get("hostname") or hostname,
                operator=operator_id,
                remote=_request_remote() or "-",
                items=len(selections),
                destination=destination_path,
                operation=operation,
            )
        )
        return jsonify({"ok": True, "pasted": response.get("pasted") if isinstance(response, dict) else []})

    @blueprint.route("/api/device/files/<hostname>/download", methods=["POST"])
    def start_download(hostname: str):
        user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        selections = _normalize_transfer_entries(body.get("items") or body.get("paths") or body.get("path"))
        if not selections:
            return jsonify({"error": "paths_required"}), 400
        if not _system_socket_available(app, adapters, record):
            return jsonify({"error": "agent_unavailable"}), 503
        archive_required = len(selections) != 1 or any(_normalize_text(row.get("kind")).lower() == "directory" for row in selections)
        archive_name = _guess_download_name(record.get("hostname") or hostname, selections, archive_required=archive_required)
        worker_route, route_error = _worker_route_for_record(adapters, record)
        if route_error:
            payload, status = route_error
            return jsonify(payload), status
        assert worker_route is not None
        worker_urls = site_worker_route_urls(adapters.context, request, worker_route)
        session, worker_error = _post_worker_json(
            app,
            worker_route,
            "/remote-files/transfers/download",
            {
                "hostname": record.get("hostname") or hostname,
                "device_guid": record.get("guid") or "",
                "agent_id": record.get("agent_id") or "",
                "operator_id": operator_id or "unknown",
                "items": selections,
                "archive_name": archive_name,
                "archive_required": archive_required,
                "transfer_base_url": worker_urls["base"],
            },
            timeout=30.0,
        )
        if worker_error:
            payload, status = worker_error
            return jsonify(payload), status
        assert session is not None
        _service_log_event(
            "file_management_download_start hostname={hostname} operator={operator} remote={remote} transfer_id={transfer_id} items={items} archive={archive}".format(
                hostname=record.get("hostname") or hostname,
                operator=operator_id,
                remote=_request_remote() or "-",
                transfer_id=session["transfer_id"],
                items=len(selections),
                archive=archive_name,
            )
        )
        return jsonify(session), 202

    @blueprint.route("/api/device/files/<hostname>/transfer/<transfer_id>/status", methods=["GET"])
    def get_transfer_status(hostname: str, transfer_id: str):
        _user, record, _operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        assert record is not None
        worker_route, route_error = _worker_route_for_record(adapters, record)
        if route_error:
            payload, status = route_error
            return jsonify(payload), status
        assert worker_route is not None
        snapshot, worker_error = _get_worker_json(app, worker_route, f"/remote-files/transfers/{transfer_id}/status")
        if worker_error:
            payload, status = worker_error
            return jsonify(payload), status
        if _normalize_text((snapshot or {}).get("hostname")).lower() != _normalize_text(record.get("hostname") or hostname).lower():
            return jsonify({"error": "transfer_not_found"}), 404
        return jsonify(snapshot)

    @blueprint.route("/api/device/files/<hostname>/transfer/<transfer_id>/cancel", methods=["POST"])
    def cancel_transfer(hostname: str, transfer_id: str):
        _user, record, _operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        assert record is not None
        worker_route, route_error = _worker_route_for_record(adapters, record)
        if route_error:
            payload, status = route_error
            return jsonify(payload), status
        assert worker_route is not None
        snapshot, worker_error = _post_worker_json(app, worker_route, f"/remote-files/transfers/{transfer_id}/cancel", {}, timeout=10.0)
        if worker_error:
            payload, status = worker_error
            return jsonify(payload), status
        if _normalize_text((snapshot or {}).get("hostname")).lower() != _normalize_text(record.get("hostname") or hostname).lower():
            return jsonify({"error": "transfer_not_found"}), 404
        _service_log_event(
            "file_management_transfer_cancel hostname={hostname} operator={operator} remote={remote} transfer_id={transfer_id}".format(
                hostname=record.get("hostname") or hostname,
                operator=_normalize_text((_current_user(app) or {}).get("username")) or "unknown",
                remote=_request_remote() or "-",
                transfer_id=transfer_id,
            ),
            level="WARNING",
        )
        return jsonify(snapshot)

    @blueprint.route("/api/device/files/<hostname>/transfer/<transfer_id>/content", methods=["GET"])
    def get_transfer_content(hostname: str, transfer_id: str):
        _user, record, _operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        assert record is not None
        worker_route, route_error = _worker_route_for_record(adapters, record)
        if route_error:
            payload, status = route_error
            return jsonify(payload), status
        assert worker_route is not None
        snapshot, worker_error = _get_worker_json(app, worker_route, f"/remote-files/transfers/{transfer_id}/status")
        if worker_error:
            payload, status = worker_error
            return jsonify(payload), status
        if _normalize_text((snapshot or {}).get("hostname")).lower() != _normalize_text(record.get("hostname") or hostname).lower():
            return jsonify({"error": "transfer_not_found"}), 404
        try:
            worker_response = requests.get(
                _worker_route_url(worker_route, f"/remote-files/transfers/{transfer_id}/content"),
                headers=_worker_internal_headers(app),
                stream=True,
                timeout=900.0,
            )
        except Exception:
            return jsonify({"error": "site_worker_unavailable", "message": "The site-worker route did not answer."}), 503
        if worker_response.status_code >= 400:
            try:
                payload = worker_response.json()
            except Exception:
                payload = {"error": "site_worker_error"}
            finally:
                worker_response.close()
            return jsonify(payload), worker_response.status_code

        def _stream():
            try:
                for chunk in worker_response.iter_content(chunk_size=1024 * 1024):
                    if chunk:
                        yield chunk
            finally:
                worker_response.close()

        headers = {}
        for header in ("Content-Type", "Content-Length", "Content-Disposition"):
            value = worker_response.headers.get(header)
            if value:
                headers[header] = value
        return Response(
            stream_with_context(_stream()),
            status=worker_response.status_code,
            headers=headers,
            direct_passthrough=True,
        )

    @blueprint.route("/api/agent/files/transfers/<transfer_id>/upload-item/<item_id>", methods=["GET"])
    @require_device_auth(auth_manager)
    def get_upload_item(transfer_id: str, item_id: str):
        session, error = _agent_transfer_session(transfer_id)
        if error:
            payload, status = error
            return jsonify(payload), status
        assert session is not None
        if bool(session.get("cancel_requested")) or _normalize_text(session.get("status")).lower() in {"canceling", "canceled"}:
            return jsonify({"error": "transfer_canceled"}), 409
        item = _store().get_upload_item(transfer_id, item_id)
        if item is None:
            return jsonify({"error": "upload_item_not_found"}), 404
        _session, upload_item = item
        stored_path = _normalize_text(upload_item.get("stored_path"))
        if not stored_path or not Path(stored_path).is_file():
            return jsonify({"error": "upload_item_not_found"}), 404
        return send_file(
            stored_path,
            as_attachment=True,
            download_name=_normalize_text(upload_item.get("name")) or Path(stored_path).name,
            mimetype="application/octet-stream",
            max_age=0,
        )

    @blueprint.route("/api/agent/files/transfers/<transfer_id>/status", methods=["GET"])
    @require_device_auth(auth_manager)
    def get_agent_transfer_status(transfer_id: str):
        session, error = _agent_transfer_session(transfer_id)
        if error:
            payload, status = error
            return jsonify(payload), status
        assert session is not None
        return jsonify(_transfer_status_snapshot(session))

    @blueprint.route("/api/agent/files/transfers/<transfer_id>/progress", methods=["POST"])
    @require_device_auth(auth_manager)
    def update_transfer_progress(transfer_id: str):
        session, error = _agent_transfer_session(transfer_id)
        if error:
            payload, status = error
            return jsonify(payload), status
        body = request.get_json(silent=True) or {}
        status_value = _normalize_text(body.get("status")).lower()
        bytes_complete = body.get("bytes_complete")
        bytes_total = body.get("bytes_total")
        archive_name = _normalize_text(body.get("archive_name"))
        error_text = _normalize_text(body.get("error"))
        if status_value == "failed":
            snapshot = _store().mark_failed(transfer_id, error_text or "transfer_failed")
        elif status_value == "canceled":
            snapshot = _store().mark_canceled(transfer_id, error_text or "Transfer canceled by operator.")
        else:
            snapshot = _store().mark_progress(
                transfer_id,
                status=status_value,
                bytes_complete=int(bytes_complete) if bytes_complete is not None else None,
                bytes_total=int(bytes_total) if bytes_total is not None else None,
                error=error_text,
                archive_name=archive_name,
            )
        if snapshot is None:
            return jsonify({"error": "transfer_not_found"}), 404
        return jsonify(snapshot)

    @blueprint.route("/api/agent/files/transfers/<transfer_id>/content", methods=["POST"])
    @require_device_auth(auth_manager)
    def store_transfer_content(transfer_id: str):
        session, error = _agent_transfer_session(transfer_id)
        if error:
            payload, status = error
            return jsonify(payload), status
        assert session is not None
        if bool(session.get("cancel_requested")) or _normalize_text(session.get("status")).lower() in {"canceling", "canceled"}:
            _store().mark_canceled(transfer_id, "Transfer canceled by operator.")
            return jsonify({"error": "transfer_canceled"}), 409
        file_storage = request.files.get("artifact")
        if file_storage is None:
            return jsonify({"error": "artifact_required"}), 400
        archive_name = _normalize_text(request.form.get("archive_name")) or _normalize_text(request.form.get("filename"))
        mime_type = _normalize_text(request.form.get("mime_type"))
        snapshot = _store().store_download_artifact(
            transfer_id,
            file_storage=file_storage,
            archive_name=archive_name,
            mime_type=mime_type,
        )
        if snapshot is None:
            return jsonify({"error": "transfer_not_found"}), 404
        return jsonify(snapshot)

    app.register_blueprint(blueprint)
