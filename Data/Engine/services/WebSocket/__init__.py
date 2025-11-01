# ======================================================
# Data\Engine\services\WebSocket\__init__.py
# Description: Socket.IO handlers for Engine runtime quick job updates and realtime notifications.
#
# API Endpoints (if applicable): None
# ======================================================

"""WebSocket service registration for the Borealis Engine runtime."""
from __future__ import annotations

import sqlite3
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Dict, Optional

from flask_socketio import SocketIO

from ...database import initialise_engine_database
from ...server import EngineContext
from ..API import _make_db_conn_factory, _make_service_logger


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
        initialise_engine_database(self.context.database_path, logger=self.context.logger)
        self.db_conn_factory = _make_db_conn_factory(self.context.database_path)

        log_file = str(
            self.context.config.get("log_file")
            or self.context.config.get("LOG_FILE")
            or ""
        ).strip()
        if log_file:
            base = Path(log_file).resolve().parent
        else:
            base = Path(self.context.database_path).resolve().parent
        self.service_log = _make_service_logger(base, self.context.logger)


def register_realtime(socket_server: SocketIO, context: EngineContext) -> None:
    """Register Socket.IO event handlers for the Engine runtime."""

    adapters = EngineRealtimeAdapters(context)
    logger = context.logger.getChild("realtime.quick_jobs")

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

        try:
            conn = adapters.db_conn_factory()
            cursor = conn.cursor()
            cursor.execute(
                "UPDATE activity_history SET status=?, stdout=?, stderr=? WHERE id=?",
                (status, stdout, stderr, job_id),
            )
            if cursor.rowcount == 0:
                logger.debug("quick_job_result missing activity_history row for job_id=%s", job_id)
            conn.commit()

            try:
                cursor.execute(
                    "SELECT run_id FROM scheduled_job_run_activity WHERE activity_id=?",
                    (job_id,),
                )
                link = cursor.fetchone()
            except sqlite3.Error:
                link = None

            if link:
                try:
                    run_id = int(link[0])
                    ts_now = _now_ts()
                    if status.lower() == "running":
                        cursor.execute(
                            "UPDATE scheduled_job_runs SET status='Running', updated_at=? WHERE id=?",
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
                            (status, ts_now, ts_now, run_id),
                        )
                    conn.commit()
                except Exception as exc:  # pragma: no cover - defensive guard
                    logger.debug(
                        "quick_job_result failed to update scheduled_job_runs for job_id=%s: %s",
                        job_id,
                        exc,
                    )

            try:
                cursor.execute(
                    "SELECT id, hostname, status FROM activity_history WHERE id=?",
                    (job_id,),
                )
                row = cursor.fetchone()
            except sqlite3.Error:
                row = None

            if row:
                hostname = (row[1] or "").strip()
                if hostname:
                    broadcast_payload = {
                        "activity_id": int(row[0]),
                        "hostname": hostname,
                        "status": row[2] or status,
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
