# ======================================================
# Data\Engine\services\auth\bootstrap_state.py
# Description: Shared helpers for determining the public Borealis operator bootstrap phase.
#
# API Endpoints (if applicable): None
# ======================================================

"""Shared bootstrap-state helpers for operator authentication gating."""

from __future__ import annotations

from typing import Any, Callable, Dict

from Data.Engine.db import dbapi as sqlite3

BOOTSTRAP_PHASE_AEGIS_SETUP_REQUIRED = "aegis_setup_required"
BOOTSTRAP_PHASE_AEGIS_UNLOCK_REQUIRED = "aegis_unlock_required"
BOOTSTRAP_PHASE_ADMIN_SETUP_REQUIRED = "admin_setup_required"
BOOTSTRAP_PHASE_ADMIN_RECOVERY_REQUIRED = "admin_recovery_required"
BOOTSTRAP_PHASE_LOGIN_REQUIRED = "login_required"


def _count(cur, query: str, params: tuple[Any, ...] = ()) -> int:
    cur.execute(query, params)
    row = cur.fetchone()
    return int((row or [0])[0] or 0)


def determine_bootstrap_state(
    *,
    db_conn_factory: Callable[[], sqlite3.Connection],
    aegis_cipher_service,
) -> Dict[str, Any]:
    configured = bool(aegis_cipher_service.is_configured())
    locked = bool(configured and aegis_cipher_service.is_locked())

    total_users = 0
    admin_count = 0
    ready_admin_count = 0
    reset_required_count = 0
    conn = None
    try:
        conn = db_conn_factory()
        cur = conn.cursor()
        total_users = _count(cur, "SELECT COUNT(*) FROM users")
        admin_count = _count(cur, "SELECT COUNT(*) FROM users WHERE LOWER(role)='admin'")
        ready_admin_count = _count(
            cur,
            """
            SELECT COUNT(*)
              FROM users
             WHERE LOWER(role)='admin'
               AND COALESCE(auth_reset_required, 0)=0
               AND COALESCE(password_sha512, '')<>''
            """,
        )
        reset_required_count = _count(
            cur,
            "SELECT COUNT(*) FROM users WHERE COALESCE(auth_reset_required, 0)<>0",
        )
    finally:
        if conn is not None:
            conn.close()

    if not configured:
        phase = BOOTSTRAP_PHASE_AEGIS_SETUP_REQUIRED
    elif locked:
        phase = BOOTSTRAP_PHASE_AEGIS_UNLOCK_REQUIRED
    elif total_users <= 0 or admin_count <= 0:
        phase = BOOTSTRAP_PHASE_ADMIN_SETUP_REQUIRED
    elif ready_admin_count <= 0:
        phase = BOOTSTRAP_PHASE_ADMIN_RECOVERY_REQUIRED
    else:
        phase = BOOTSTRAP_PHASE_LOGIN_REQUIRED

    return {
        "phase": phase,
        "configured": configured,
        "locked": locked,
        "user_count": total_users,
        "admin_count": admin_count,
        "ready_admin_count": ready_admin_count,
        "auth_reset_required_count": reset_required_count,
    }


def operator_auth_allowed(
    *,
    db_conn_factory: Callable[[], sqlite3.Connection],
    aegis_cipher_service,
) -> bool:
    return (
        determine_bootstrap_state(
            db_conn_factory=db_conn_factory,
            aegis_cipher_service=aegis_cipher_service,
        )["phase"]
        == BOOTSTRAP_PHASE_LOGIN_REQUIRED
    )


__all__ = [
    "BOOTSTRAP_PHASE_ADMIN_RECOVERY_REQUIRED",
    "BOOTSTRAP_PHASE_ADMIN_SETUP_REQUIRED",
    "BOOTSTRAP_PHASE_AEGIS_SETUP_REQUIRED",
    "BOOTSTRAP_PHASE_AEGIS_UNLOCK_REQUIRED",
    "BOOTSTRAP_PHASE_LOGIN_REQUIRED",
    "determine_bootstrap_state",
    "operator_auth_allowed",
]
