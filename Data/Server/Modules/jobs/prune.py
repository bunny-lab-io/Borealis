from __future__ import annotations

from datetime import datetime, timedelta, timezone
from typing import Callable, Optional

import eventlet
from flask_socketio import SocketIO


def start_prune_job(
    socketio: SocketIO,
    *,
    db_conn_factory: Callable[[], any],
    log: Callable[[str, str, Optional[str]], None],
) -> None:
    def _job_loop():
        while True:
            try:
                _run_once(db_conn_factory, log)
            except Exception as exc:
                log("server", f"prune job failure: {exc}")
            eventlet.sleep(24 * 60 * 60)

    socketio.start_background_task(_job_loop)


def _run_once(db_conn_factory: Callable[[], any], log: Callable[[str, str, Optional[str]], None]) -> None:
    now = datetime.now(tz=timezone.utc)
    now_iso = now.isoformat()
    stale_before = (now - timedelta(hours=24)).isoformat()
    conn = db_conn_factory()
    try:
        cur = conn.cursor()
        cur.execute(
            """
            DELETE FROM enrollment_install_codes
             WHERE use_count = 0
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
                           AND (
                               c.expires_at < ?
                               OR c.use_count >= c.max_uses
                           )
                     )
                    OR created_at < ?
               )
            """,
            (now_iso, now_iso, stale_before),
        )
        approvals_marked = cur.rowcount or 0

        conn.commit()
    finally:
        conn.close()

    if codes_pruned:
        log("server", f"prune job removed {codes_pruned} expired enrollment codes")
    if approvals_marked:
        log("server", f"prune job expired {approvals_marked} device approvals")
