"""Shared Engine unit test helpers."""

from __future__ import annotations

from contextlib import contextmanager
from typing import Any, Iterator, Sequence

from Data.Engine.db import dbapi as sqlite3

DEFAULT_ADMIN_USERNAME = "admin"
DEFAULT_ADMIN_ROLE = "Admin"


def client_with_session(
    harness: Any,
    *,
    username: str = DEFAULT_ADMIN_USERNAME,
    role: str = DEFAULT_ADMIN_ROLE,
):
    """Return Flask test client with Borealis operator session state."""
    client = harness.app.test_client()
    with client.session_transaction() as session:
        session["username"] = username
        session["role"] = role
    return client


def admin_client(harness: Any):
    """Return Flask test client authenticated as seeded admin operator."""
    return client_with_session(harness)


@contextmanager
def db_connection(harness: Any) -> Iterator[Any]:
    """Open direct test DB connection for setup/inspection."""
    conn = sqlite3.connect(str(harness.db_path))
    try:
        yield conn
    finally:
        conn.close()


def execute_sql(
    harness: Any,
    sql: str,
    params: Sequence[Any] = (),
    *,
    commit: bool = True,
) -> int:
    """Execute SQL against harness DB and return affected row count."""
    with db_connection(harness) as conn:
        cur = conn.cursor()
        cur.execute(sql, tuple(params))
        rowcount = int(getattr(cur, "rowcount", -1))
        if commit:
            conn.commit()
        return rowcount


def fetch_one(harness: Any, sql: str, params: Sequence[Any] = ()) -> Any | None:
    """Fetch one row from harness DB."""
    with db_connection(harness) as conn:
        cur = conn.cursor()
        cur.execute(sql, tuple(params))
        return cur.fetchone()


def fetch_all(harness: Any, sql: str, params: Sequence[Any] = ()) -> list[Any]:
    """Fetch all rows from harness DB."""
    with db_connection(harness) as conn:
        cur = conn.cursor()
        cur.execute(sql, tuple(params))
        return list(cur.fetchall())
