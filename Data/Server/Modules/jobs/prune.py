from __future__ import annotations

from datetime import datetime, timezone
from typing import Callable

import eventlet
from flask_socketio import SocketIO


def start_prune_job(
    socketio: SocketIO,
    *,
    db_conn_factory: Callable[[], any],
    log: Callable[[str, str], None],
) -> None:
    def _job_loop():
        while True:
            try:
                _run_once(db_conn_factory, log)
            except Exception as exc:
                log("server", f"prune job failure: {exc}")
            eventlet.sleep(24 * 60 * 60)

    socketio.start_background_task(_job_loop)


def _run_once(db_conn_factory: Callable[[], any], log: Callable[[str, str], None]) -> None:
    now_iso = datetime.now(tz=timezone.utc).isoformat()
    conn = db_conn_factory()
    try:
        cur = conn.cursor()
        cur.execute(
            """
            DELETE FROM enrollment_install_codes
             WHERE used_at IS NULL
               AND expires_at < ?
            """,
            (now_iso,),
        )
        codes_pruned = cur.rowcount or 0

        cur.execute(
            """
            UPDATE device_approvals
               SET status = 'expired',
                   updated_at = ?
             WHERE status = 'pending'
               AND (
                    EXISTS (
                        SELECT 1
                          FROM enrollment_install_codes c
                         WHERE c.id = device_approvals.enrollment_code_id
                           AND c.expires_at < ?
                     )
                    OR created_at < ?
               )
            """,
            (now_iso, now_iso, now_iso),
        )
        approvals_marked = cur.rowcount or 0

        conn.commit()
    finally:
        conn.close()

    if codes_pruned:
        log("server", f"prune job removed {codes_pruned} expired enrollment codes")
    if approvals_marked:
        log("server", f"prune job expired {approvals_marked} device approvals")
