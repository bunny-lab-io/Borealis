# ======================================================
# Data\Engine\services\API\devices\file_management.py
# Description: Remote file-management browse, mutation, and transfer endpoints.
#
# API Endpoints (if applicable):
# - GET /api/device/files/<hostname>/roots (Token Authenticated) - Returns the remote file-management roots view for an in-scope device.
# - GET /api/device/files/<hostname>/children (Token Authenticated) - Returns direct children for a remote directory on an in-scope device.
# - POST /api/device/files/<hostname>/mkdir (Token Authenticated) - Creates a remote directory on an in-scope device.
# - POST /api/device/files/<hostname>/rename (Token Authenticated) - Renames one remote file-system item on an in-scope device.
# - POST /api/device/files/<hostname>/move (Token Authenticated) - Moves remote file-system items on an in-scope device.
# - POST /api/device/files/<hostname>/delete (Token Authenticated) - Deletes remote file-system items on an in-scope device.
# - POST /api/device/files/<hostname>/upload (Token Authenticated) - Stages browser-uploaded files for transfer to an in-scope device.
# - POST /api/device/files/<hostname>/download (Token Authenticated) - Starts a remote file download transfer from an in-scope device.
# - GET /api/device/files/<hostname>/transfer/<transfer_id>/status (Token Authenticated) - Returns a staged transfer status snapshot.
# - GET /api/device/files/<hostname>/transfer/<transfer_id>/content (Token Authenticated) - Downloads a completed transfer artifact from Engine temp storage.
# - GET /api/agent/files/transfers/<transfer_id>/upload-item/<item_id> (Device Authenticated) - Streams one staged upload item to the device.
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

from flask import Blueprint, g, jsonify, request, send_file

from ....auth.device_auth import require_device_auth
from ....auth.guid_utils import normalize_guid
from ...auth import UserSiteAccessManager
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
    ) -> Dict[str, Any]:
        self.cleanup_expired()
        transfer_id = uuid.uuid4().hex
        session_dir = self._session_dir_for(transfer_id)
        uploads_dir = session_dir / "uploads"
        uploads_dir.mkdir(parents=True, exist_ok=True)
        item_rows: list[Dict[str, Any]] = []
        bytes_total = 0
        for storage in files:
            filename = _sanitize_upload_name(getattr(storage, "filename", ""))
            if not filename:
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
                    "name": filename,
                    "size_bytes": size_bytes,
                    "stored_path": str(stored_path),
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


def _system_socket_available(adapters: "EngineServiceAdapters", hostname: str, agent_id: str) -> bool:
    has_host_socket = getattr(adapters.context, "has_host_service_socket", None)
    if callable(has_host_socket):
        try:
            if bool(has_host_socket(hostname, "system")):
                return True
        except Exception:
            adapters.context.logger.debug("has_host_service_socket failed hostname=%s", hostname, exc_info=True)
    registry = getattr(adapters.context, "agent_socket_registry", None)
    if registry and hasattr(registry, "is_registered") and agent_id:
        try:
            return bool(registry.is_registered(agent_id))
        except Exception:
            adapters.context.logger.debug("agent socket registry lookup failed agent_id=%s", agent_id, exc_info=True)
    return False


def _load_device_record(adapters: "EngineServiceAdapters", hostname: str) -> Optional[Dict[str, Any]]:
    conn = None
    try:
        conn = adapters.db_conn_factory()
        cur = conn.cursor()
        cur.execute(
            """
            SELECT guid, hostname, agent_id, operating_system, last_seen
              FROM devices
             WHERE LOWER(hostname) = LOWER(?)
          ORDER BY last_seen DESC
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
        }
    finally:
        if conn is not None:
            try:
                conn.close()
            except Exception:
                pass


def _rpc_error_response(response: Any) -> Tuple[Dict[str, Any], int]:
    if not isinstance(response, dict):
        return {"error": "agent_unavailable", "message": "The device file-management channel did not respond."}, 503
    error_code = _normalize_text(response.get("error")).lower() or "agent_error"
    message = _normalize_text(response.get("message")) or error_code
    status_code = {
        "not_found": 404,
        "path_not_found": 404,
        "file_not_found": 404,
        "directory_not_found": 404,
        "not_a_directory": 400,
        "invalid_path": 400,
        "invalid_name": 400,
        "invalid_request": 400,
        "permission_denied": 403,
        "conflict": 409,
        "path_conflict": 409,
        "agent_unavailable": 503,
        "timeout": 504,
    }.get(error_code, 502)
    return {"error": error_code, "message": message}, status_code


def _call_file_management_rpc(
    adapters: "EngineServiceAdapters",
    *,
    hostname: str,
    payload: Dict[str, Any],
    timeout: float = 30.0,
) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
    caller = getattr(adapters.context, "call_host_service_event", None)
    if not callable(caller):
        return None, ({"error": "agent_unavailable", "message": "The file-management socket bridge is unavailable."}, 503)
    try:
        response = caller(hostname, "system", "file_management_request", payload, timeout=timeout)
    except Exception:
        adapters.context.logger.debug("file-management rpc failed hostname=%s", hostname, exc_info=True)
        response = None
    if response is None:
        return None, ({"error": "timeout", "message": "The device did not answer the file-management request in time."}, 504)
    if not isinstance(response, dict):
        return None, ({"error": "invalid_agent_response", "message": "The device returned an invalid file-management response."}, 502)
    if response.get("ok") is False:
        return None, _rpc_error_response(response)
    return response, None


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
        if not _system_socket_available(adapters, record.get("hostname") or hostname, record.get("agent_id") or ""):
            return jsonify({"error": "agent_unavailable"}), 503
        response, rpc_error = _call_file_management_rpc(
            adapters,
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
        if not _system_socket_available(adapters, record.get("hostname") or hostname, record.get("agent_id") or ""):
            return jsonify({"error": "agent_unavailable"}), 503
        response, rpc_error = _call_file_management_rpc(
            adapters,
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
        user, record, operator_id, error = _operator_context(hostname)
        if error:
            payload, status = error
            return jsonify(payload), status
        target_path = _normalize_text(request.form.get("target_path"))
        files = request.files.getlist("files")
        if not target_path:
            return jsonify({"error": "target_path_required"}), 400
        if not files:
            return jsonify({"error": "upload_files_required"}), 400
        if not _system_socket_available(adapters, record.get("hostname") or hostname, record.get("agent_id") or ""):
            return jsonify({"error": "agent_unavailable"}), 503
        try:
            session = _store().create_upload_session(
                hostname=record.get("hostname") or hostname,
                device_guid=record.get("guid") or "",
                agent_id=record.get("agent_id") or "",
                operator_id=operator_id or "unknown",
                target_path=target_path,
                files=files,
            )
        except ValueError as exc:
            return jsonify({"error": _normalize_text(exc) or "upload_files_required"}), 400
        emitter = getattr(adapters.context, "emit_host_service_event", None)
        emitted = False
        if callable(emitter):
            try:
                emitted = bool(
                    emitter(
                        record.get("hostname") or hostname,
                        "system",
                        "file_management_request",
                        {
                            "action": "upload_start",
                            "hostname": record.get("hostname") or hostname,
                            "agent_id": record.get("agent_id") or "",
                            "requested_by": operator_id,
                            "transfer_id": session["transfer_id"],
                            "target_path": target_path,
                            "items": [
                                {
                                    "item_id": row.get("item_id"),
                                    "name": row.get("name"),
                                    "size_bytes": int(row.get("size_bytes") or 0),
                                }
                                for row in (_store().get_session(session["transfer_id"]) or {}).get("upload_items") or []
                            ],
                        },
                    )
                )
            except Exception:
                emitted = False
        if not emitted:
            _store().mark_failed(session["transfer_id"], "agent_unavailable")
            return jsonify({"error": "agent_unavailable"}), 503
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
        if not _system_socket_available(adapters, record.get("hostname") or hostname, record.get("agent_id") or ""):
            return jsonify({"error": "agent_unavailable"}), 503
        archive_required = len(selections) != 1 or any(_normalize_text(row.get("kind")).lower() == "directory" for row in selections)
        archive_name = _guess_download_name(record.get("hostname") or hostname, selections, archive_required=archive_required)
        session = _store().create_download_session(
            hostname=record.get("hostname") or hostname,
            device_guid=record.get("guid") or "",
            agent_id=record.get("agent_id") or "",
            operator_id=operator_id or "unknown",
            selections=selections,
            archive_name=archive_name,
        )
        emitter = getattr(adapters.context, "emit_host_service_event", None)
        emitted = False
        if callable(emitter):
            try:
                emitted = bool(
                    emitter(
                        record.get("hostname") or hostname,
                        "system",
                        "file_management_request",
                        {
                            "action": "download_start",
                            "hostname": record.get("hostname") or hostname,
                            "agent_id": record.get("agent_id") or "",
                            "requested_by": operator_id,
                            "transfer_id": session["transfer_id"],
                            "archive_name": archive_name,
                            "archive_required": archive_required,
                            "items": selections,
                        },
                    )
                )
            except Exception:
                emitted = False
        if not emitted:
            _store().mark_failed(session["transfer_id"], "agent_unavailable")
            return jsonify({"error": "agent_unavailable"}), 503
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
        session, error = _validate_operator_transfer(hostname, transfer_id)
        if error:
            payload, status = error
            return jsonify(payload), status
        assert session is not None
        return jsonify(_transfer_status_snapshot(session))

    @blueprint.route("/api/device/files/<hostname>/transfer/<transfer_id>/content", methods=["GET"])
    def get_transfer_content(hostname: str, transfer_id: str):
        session, error = _validate_operator_transfer(hostname, transfer_id)
        if error:
            payload, status = error
            return jsonify(payload), status
        assert session is not None
        if _normalize_text(session.get("direction")) != "download":
            return jsonify({"error": "content_not_available"}), 404
        if _normalize_text(session.get("status")) != "completed":
            return jsonify({"error": "transfer_not_ready"}), 409
        result_path = _normalize_text(session.get("result_path"))
        if not result_path or not Path(result_path).is_file():
            return jsonify({"error": "content_not_available"}), 404
        return send_file(
            result_path,
            as_attachment=True,
            download_name=_normalize_text(session.get("result_name")) or Path(result_path).name,
            mimetype=_normalize_text(session.get("result_mime")) or "application/octet-stream",
            max_age=0,
        )

    @blueprint.route("/api/agent/files/transfers/<transfer_id>/upload-item/<item_id>", methods=["GET"])
    @require_device_auth(auth_manager)
    def get_upload_item(transfer_id: str, item_id: str):
        session, error = _agent_transfer_session(transfer_id)
        if error:
            payload, status = error
            return jsonify(payload), status
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
