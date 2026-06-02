# ======================================================
# Data\Engine\services\WebSocket\__init__.py
# Description: Socket.IO handlers for Engine runtime quick job updates and operator presence.
#
# API Endpoints (if applicable): None
# ======================================================

"""WebSocket service registration for the Borealis Engine runtime."""
from __future__ import annotations

import threading
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Dict, Optional

from flask import request, session
from flask_socketio import SocketIO

from ...db import dbapi as sqlite3
from ...database import initialise_engine_database
from ...server import EngineContext
from ..activity_history import (
    get_activity_history_row,
    normalize_activity_status,
    status_is_terminal,
    update_activity_history_row,
)


def _now_ts() -> int:
    return int(time.time())


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, bytes):
        try:
            return value.decode("utf-8")
        except Exception:
            return value.decode("utf-8", errors="replace")
    return str(value)


@dataclass
class EngineRealtimeAdapters:
    context: EngineContext
    db_conn_factory: Callable[[], sqlite3.Connection] = field(init=False)
    service_log: Callable[[str, str, Optional[str]], None] = field(init=False)

    def __post_init__(self) -> None:
        from ..API import _make_db_conn_factory, _make_service_logger  # Local import to avoid circular import at module load

        initialise_engine_database(self.context.database_url, logger=self.context.logger)
        self.db_conn_factory = _make_db_conn_factory(
            self.context.database_url,
            sslmode=str(self.context.config.get("db_sslmode") or "prefer"),
            pool_size=int(self.context.config.get("db_pool_size") or 10),
            max_overflow=int(self.context.config.get("db_max_overflow") or 20),
            connect_timeout=int(self.context.config.get("db_connect_timeout") or 15),
            idle_in_transaction_timeout_ms=int(self.context.config.get("db_idle_in_transaction_timeout_ms") or 60000),
            logger=self.context.logger,
        )

        log_file = str(
            self.context.config.get("log_file")
            or self.context.config.get("LOG_FILE")
            or ""
        ).strip()
        if log_file:
            base = Path(log_file).resolve().parent
        else:
            base = Path.cwd() / "Engine" / "Services" / "api-backend" / "logs"
        self.service_log = _make_service_logger(base, self.context.logger)


class OperatorPresenceRegistry:
    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._records_by_sid: Dict[str, Dict[str, Any]] = {}

    def register_or_update(
        self,
        sid: str,
        *,
        username: str,
        role: str,
        current_page: str = "",
    ) -> bool:
        normalized_sid = str(sid or "").strip()
        normalized_username = str(username or "").strip()
        if not normalized_sid or not normalized_username:
            return False

        normalized_role = str(role or "User").strip() or "User"
        normalized_page = self._normalize_page(current_page)
        now_ts = _now_ts()
        changed = False

        with self._lock:
            existing = self._records_by_sid.get(normalized_sid)
            if existing is None:
                self._records_by_sid[normalized_sid] = {
                    "sid": normalized_sid,
                    "username": normalized_username,
                    "role": normalized_role,
                    "current_page": normalized_page,
                    "connected_at": now_ts,
                    "last_seen_at": now_ts,
                }
                changed = True
            else:
                if (
                    str(existing.get("username") or "") != normalized_username
                    or str(existing.get("role") or "") != normalized_role
                    or str(existing.get("current_page") or "") != normalized_page
                ):
                    changed = True
                existing["username"] = normalized_username
                existing["role"] = normalized_role
                existing["current_page"] = normalized_page
                existing["last_seen_at"] = now_ts
        return changed

    def remove(self, sid: str) -> Optional[Dict[str, Any]]:
        normalized_sid = str(sid or "").strip()
        if not normalized_sid:
            return None
        with self._lock:
            record = self._records_by_sid.pop(normalized_sid, None)
        return dict(record) if isinstance(record, dict) else None

    def list_sessions(self) -> list[Dict[str, Any]]:
        with self._lock:
            records = [dict(record) for record in self._records_by_sid.values()]
        return sorted(
            records,
            key=lambda item: (
                -int(item.get("last_seen_at") or 0),
                -int(item.get("connected_at") or 0),
                str(item.get("username") or "").lower(),
                str(item.get("sid") or ""),
            ),
        )

    def count_sessions(self) -> int:
        with self._lock:
            return len(self._records_by_sid)

    @staticmethod
    def _normalize_page(value: Any) -> str:
        text = str(value or "").strip()
        if not text:
            return ""
        return text[:160]


def register_realtime(socket_server: SocketIO, context: EngineContext) -> None:
    """Register Socket.IO event handlers for the Engine runtime."""

    adapters = EngineRealtimeAdapters(context)
    logger = context.logger.getChild("realtime.quick_jobs")
    agent_logger = context.logger.getChild("realtime.agents")
    operator_logger = context.logger.getChild("realtime.operators")
    operator_registry = OperatorPresenceRegistry()
    setattr(context, "operator_presence_registry", operator_registry)

    def _emit_operator_presence_changed() -> None:
        try:
            socket_server.emit("server_operator_presence_changed", {"changed_at": _now_ts()})
        except Exception:
            operator_logger.debug("Failed to emit operator presence change event.", exc_info=True)

    def _current_operator_identity() -> Optional[Dict[str, str]]:
        username = str(session.get("username") or "").strip()
        if not username:
            return None
        role = str(session.get("role") or "User").strip() or "User"
        return {"username": username, "role": role}

    def _resolve_scheduled_run_context(
        cursor: Any,
        *,
        job_id: int,
        context_info: Optional[Dict[str, Any]],
    ) -> tuple[Optional[int], Optional[int]]:
        try:
            cursor.execute(
                "SELECT run_id FROM scheduled_job_run_activity WHERE activity_id=?",
                (job_id,),
            )
            link = cursor.fetchone()
        except sqlite3.Error:
            link = None

        run_id: Optional[int] = None
        scheduled_ts_ctx: Optional[int] = None
        if link:
            try:
                run_id = int(link[0])
            except Exception:
                run_id = None

        if run_id is None and context_info:
            ctx_run = context_info.get("scheduled_job_run_id") or context_info.get("run_id")
            try:
                if ctx_run is not None:
                    run_id = int(ctx_run)
            except (TypeError, ValueError):
                run_id = None
            try:
                if context_info.get("scheduled_ts") is not None:
                    scheduled_ts_ctx = int(context_info.get("scheduled_ts"))
            except (TypeError, ValueError):
                scheduled_ts_ctx = None
        return run_id, scheduled_ts_ctx

    def _update_scheduled_run_state(
        cursor: Any,
        *,
        run_id: Optional[int],
        scheduled_ts_ctx: Optional[int],
        status: str,
        activity_id: int,
        context_info: Optional[Dict[str, Any]] = None,
    ) -> None:
        if run_id is None:
            if context_info:
                adapters.service_log(
                    "scheduled_jobs",
                    f"scheduled run update skipped (no run_id) activity_id={activity_id} status={status} context={context_info}",
                    level="WARNING",
                )
            return
        ts_now = _now_ts()
        normalized_status = normalize_activity_status(status, default="Failed")
        lowered = normalized_status.lower()
        try:
            if lowered == "running":
                cursor.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status='Running',
                           started_ts=COALESCE(started_ts, ?),
                           updated_at=?
                     WHERE id=?
                    """,
                    (ts_now, ts_now, run_id),
                )
            elif lowered == "queued":
                cursor.execute(
                    "UPDATE scheduled_job_runs SET updated_at=? WHERE id=?",
                    (ts_now, run_id),
                )
            else:
                cursor.execute(
                    """
                    UPDATE scheduled_job_runs
                       SET status=?,
                           finished_ts=COALESCE(finished_ts, ?),
                           updated_at=?
                     WHERE id=?
                    """,
                    (normalized_status, ts_now, ts_now, run_id),
                )
            if scheduled_ts_ctx is not None:
                cursor.execute(
                    "UPDATE scheduled_job_runs SET scheduled_ts=COALESCE(scheduled_ts, ?) WHERE id=?",
                    (scheduled_ts_ctx, run_id),
                )
            adapters.service_log(
                "scheduled_jobs",
                f"scheduled run update run_id={run_id} activity_id={activity_id} status={normalized_status}",
            )
        except Exception as exc:  # pragma: no cover - defensive guard
            logger.debug(
                "quick_job progress update failed for activity_id=%s run_id=%s: %s",
                activity_id,
                run_id,
                exc,
            )

    @socket_server.on("quick_job_progress")
    def _handle_quick_job_progress(data: Any) -> None:
        if not isinstance(data, dict):
            logger.debug("quick_job_progress payload ignored (non-dict): %r", data)
            return

        job_id_raw = data.get("job_id")
        try:
            job_id = int(job_id_raw)
        except (TypeError, ValueError):
            logger.debug("quick_job_progress missing valid job_id: %r", job_id_raw)
            return

        normalized_status = normalize_activity_status(data.get("status"), default="Running")
        stdout = data.get("stdout")
        stderr = data.get("stderr")
        append_output = bool(data.get("append_output"))
        queue_lane = _normalize_text(data.get("queue_lane"))
        activity_kind = _normalize_text(data.get("activity_kind"))
        metadata = data.get("metadata") if isinstance(data.get("metadata"), dict) else None
        ctx_payload = data.get("context")
        context_info: Optional[Dict[str, Any]] = ctx_payload if isinstance(ctx_payload, dict) else None
        ts_now = _now_ts()

        conn: Optional[sqlite3.Connection] = None
        cursor = None
        broadcast_payload: Optional[Dict[str, Any]] = None

        try:
            conn = adapters.db_conn_factory()
            cursor = conn.cursor()
            existing_row = get_activity_history_row(conn, job_id)
            existing_metadata = existing_row.get("metadata") if isinstance(existing_row, dict) else {}
            merged_metadata = dict(existing_metadata) if isinstance(existing_metadata, dict) else {}
            if metadata:
                merged_metadata.update(metadata)
            if isinstance(context_info, dict):
                for key in ("assembly_source", "assembly_guid", "scheduled_job_id", "scheduled_job_run_id", "scheduled_ts"):
                    if key in context_info and context_info.get(key) not in (None, ""):
                        merged_metadata.setdefault(key, context_info.get(key))
            update_kwargs: Dict[str, Any] = {
                "status": normalized_status,
                "updated_at": ts_now,
            }
            if stdout is not None:
                update_kwargs["stdout"] = stdout
            if stderr is not None:
                update_kwargs["stderr"] = stderr
            if append_output:
                update_kwargs["append_output"] = True
            if queue_lane:
                update_kwargs["queue_lane"] = queue_lane
            if activity_kind:
                update_kwargs["activity_kind"] = activity_kind
            if merged_metadata:
                update_kwargs["metadata"] = merged_metadata
            if normalized_status.lower() == "running":
                update_kwargs["started_at"] = ts_now
            if status_is_terminal(normalized_status):
                update_kwargs["finished_at"] = ts_now
            rowcount = update_activity_history_row(conn, job_id, **update_kwargs)
            if rowcount == 0:
                logger.debug("quick_job_progress missing activity_history row for job_id=%s", job_id)

            run_id, scheduled_ts_ctx = _resolve_scheduled_run_context(cursor, job_id=job_id, context_info=context_info)
            _update_scheduled_run_state(
                cursor,
                run_id=run_id,
                scheduled_ts_ctx=scheduled_ts_ctx,
                status=normalized_status,
                activity_id=job_id,
                context_info=context_info,
            )
            conn.commit()

            row = get_activity_history_row(conn, job_id)
            if row and row.get("hostname"):
                broadcast_payload = {
                    "activity_id": int(row["id"]),
                    "hostname": row.get("hostname"),
                    "status": row.get("status") or normalized_status,
                    "queue_lane": row.get("queue_lane") or queue_lane,
                    "activity_kind": row.get("activity_kind") or activity_kind,
                    "change": "updated",
                    "source": "quick_job_progress",
                }
        except Exception as exc:  # pragma: no cover - defensive guard
            logger.warning(
                "quick_job_progress handler error for job_id=%s: %s",
                job_id,
                exc,
                exc_info=True,
            )
        finally:
            if cursor is not None:
                try:
                    cursor.close()
                except Exception:
                    pass
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

        if broadcast_payload:
            try:
                socket_server.emit("device_activity_changed", broadcast_payload)
            except Exception as exc:  # pragma: no cover - defensive guard
                logger.debug(
                    "Failed to emit device_activity_changed for progress job_id=%s: %s",
                    job_id,
                    exc,
                )

    @socket_server.on("quick_job_result")
    def _handle_quick_job_result(data: Any) -> None:
        if not isinstance(data, dict):
            logger.debug("quick_job_result payload ignored (non-dict): %r", data)
            return

        job_id_raw = data.get("job_id")
        try:
            job_id = int(job_id_raw)
        except (TypeError, ValueError):
            logger.debug("quick_job_result missing valid job_id: %r", job_id_raw)
            return

        status = str(data.get("status") or "").strip() or "Failed"
        stdout = _normalize_text(data.get("stdout"))
        stderr = _normalize_text(data.get("stderr"))

        conn: Optional[sqlite3.Connection] = None
        cursor = None
        broadcast_payload: Optional[Dict[str, Any]] = None

        ctx_payload = data.get("context")
        context_info: Optional[Dict[str, Any]] = ctx_payload if isinstance(ctx_payload, dict) else None

        try:
            conn = adapters.db_conn_factory()
            cursor = conn.cursor()
            existing_row = get_activity_history_row(conn, job_id)
            existing_metadata = existing_row.get("metadata") if isinstance(existing_row, dict) else {}
            merged_metadata = dict(existing_metadata) if isinstance(existing_metadata, dict) else {}
            if isinstance(context_info, dict):
                for key in ("assembly_source", "assembly_guid", "scheduled_job_id", "scheduled_job_run_id", "scheduled_ts"):
                    if key in context_info and context_info.get(key) not in (None, ""):
                        merged_metadata.setdefault(key, context_info.get(key))
            result_ts = _now_ts()
            update_kwargs: Dict[str, Any] = {
                "status": status,
                "stdout": stdout,
                "stderr": stderr,
                "updated_at": result_ts,
                "finished_at": result_ts,
            }
            if merged_metadata:
                update_kwargs["metadata"] = merged_metadata
            rowcount = update_activity_history_row(conn, job_id, **update_kwargs)
            if rowcount == 0:
                logger.debug("quick_job_result missing activity_history row for job_id=%s", job_id)
            run_id, scheduled_ts_ctx = _resolve_scheduled_run_context(cursor, job_id=job_id, context_info=context_info)
            _update_scheduled_run_state(
                cursor,
                run_id=run_id,
                scheduled_ts_ctx=scheduled_ts_ctx,
                status=status,
                activity_id=job_id,
                context_info=context_info,
            )
            conn.commit()

            row = get_activity_history_row(conn, job_id)
            if row and row.get("hostname"):
                broadcast_payload = {
                    "activity_id": int(row["id"]),
                    "hostname": row.get("hostname"),
                    "status": row.get("status") or status,
                    "queue_lane": row.get("queue_lane") or "",
                    "activity_kind": row.get("activity_kind") or "",
                    "change": "updated",
                    "source": "quick_job",
                }

            adapters.service_log(
                "assemblies",
                f"quick_job_result processed job_id={job_id} status={status}",
            )
        except Exception as exc:  # pragma: no cover - defensive guard
            logger.warning(
                "quick_job_result handler error for job_id=%s: %s",
                job_id,
                exc,
                exc_info=True,
            )
        finally:
            if cursor is not None:
                try:
                    cursor.close()
                except Exception:
                    pass
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass

        if broadcast_payload:
            try:
                socket_server.emit("device_activity_changed", broadcast_payload)
            except Exception as exc:  # pragma: no cover - defensive guard
                logger.debug(
                    "Failed to emit device_activity_changed for job_id=%s: %s",
                    job_id,
                    exc,
                )

    @socket_server.on("connect_agent")
    def _connect_agent(data: Any) -> Dict[str, Any]:
        agent_id = ""
        if isinstance(data, dict):
            agent_id = str(data.get("agent_id") or "").strip()
        elif isinstance(data, str):
            agent_id = data.strip()
        if not agent_id:
            return {"error": "agent_id_required"}
        agent_logger.warning(
            "Deprecated api-backend Agent Socket.IO registration rejected agent_id=%s sid=%s",
            agent_id,
            request.sid,
        )
        return {"error": "site_worker_route_required"}

    @socket_server.on("operator_presence_sync")
    def _operator_presence_sync(data: Any) -> Dict[str, Any]:
        identity = _current_operator_identity()
        if identity is None:
            return {"error": "unauthorized"}

        current_page = ""
        if isinstance(data, dict):
            current_page = str(data.get("current_page") or "").strip()
        changed = operator_registry.register_or_update(
            request.sid,
            username=identity["username"],
            role=identity["role"],
            current_page=current_page,
        )
        operator_logger.debug(
            "Operator presence synced sid=%s username=%s role=%s page=%s changed=%s",
            request.sid,
            identity["username"],
            identity["role"],
            current_page or "-",
            changed,
        )
        if changed:
            _emit_operator_presence_changed()
        return {"status": "ok"}

    @socket_server.on("operator_presence_clear")
    def _operator_presence_clear(_data: Any = None) -> Dict[str, Any]:
        removed = operator_registry.remove(request.sid)
        if removed:
            operator_logger.debug(
                "Operator presence cleared sid=%s username=%s",
                request.sid,
                removed.get("username") or "-",
            )
            _emit_operator_presence_changed()
        return {"status": "ok"}

    @socket_server.on("disconnect")
    def _ws_disconnect() -> None:
        removed_operator = operator_registry.remove(request.sid)
        if removed_operator:
            operator_logger.debug(
                "Operator socket disconnected sid=%s username=%s",
                request.sid,
                removed_operator.get("username") or "-",
            )
            _emit_operator_presence_changed()
