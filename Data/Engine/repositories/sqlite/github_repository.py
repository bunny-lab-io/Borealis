"""SQLite-backed GitHub token persistence."""

from __future__ import annotations

import logging
from contextlib import closing
from typing import Optional

from .connection import SQLiteConnectionFactory

__all__ = ["SQLiteGitHubRepository"]


class SQLiteGitHubRepository:
    """Store and retrieve GitHub API tokens for the Engine."""

    def __init__(
        self,
        connection_factory: SQLiteConnectionFactory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._connections = connection_factory
        self._log = logger or logging.getLogger("borealis.engine.repositories.github")

    def load_token(self) -> Optional[str]:
        """Return the stored GitHub token if one exists."""

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute("SELECT token FROM github_token LIMIT 1")
            row = cur.fetchone()

        if not row:
            return None

        token = (row[0] or "").strip()
        return token or None

    def store_token(self, token: Optional[str]) -> None:
        """Persist *token*, replacing any prior value."""

        normalized = (token or "").strip()

        with closing(self._connections()) as conn:
            cur = conn.cursor()
            cur.execute("DELETE FROM github_token")
            if normalized:
                cur.execute("INSERT INTO github_token (token) VALUES (?)", (normalized,))
            conn.commit()

        self._log.info("stored-token has_token=%s", bool(normalized))

