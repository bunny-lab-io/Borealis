"""SQLite connection utilities for the Borealis Engine."""

from __future__ import annotations

import sqlite3
from contextlib import contextmanager
from pathlib import Path
from typing import Iterator, Protocol

__all__ = [
    "SQLiteConnectionFactory",
    "configure_connection",
    "connect",
    "connection_factory",
    "connection_scope",
]


class SQLiteConnectionFactory(Protocol):
    """Callable protocol for obtaining configured SQLite connections."""

    def __call__(self) -> sqlite3.Connection:
        """Return a new :class:`sqlite3.Connection`."""


def configure_connection(conn: sqlite3.Connection) -> None:
    """Apply the Borealis-standard pragmas to *conn*."""

    cur = conn.cursor()
    try:
        cur.execute("PRAGMA journal_mode=WAL")
        cur.execute("PRAGMA busy_timeout=5000")
        cur.execute("PRAGMA synchronous=NORMAL")
        conn.commit()
    except Exception:
        # Pragmas are best-effort; failing to apply them should not block startup.
        conn.rollback()
    finally:
        cur.close()


def connect(path: Path, *, timeout: float = 15.0) -> sqlite3.Connection:
    """Create a new SQLite connection to *path* with Engine pragmas applied."""

    conn = sqlite3.connect(str(path), timeout=timeout)
    configure_connection(conn)
    return conn


def connection_factory(path: Path, *, timeout: float = 15.0) -> SQLiteConnectionFactory:
    """Return a factory that opens connections to *path* when invoked."""

    def factory() -> sqlite3.Connection:
        return connect(path, timeout=timeout)

    return factory


@contextmanager
def connection_scope(path: Path, *, timeout: float = 15.0) -> Iterator[sqlite3.Connection]:
    """Context manager yielding a configured connection to *path*."""

    conn = connect(path, timeout=timeout)
    try:
        yield conn
    finally:
        conn.close()
