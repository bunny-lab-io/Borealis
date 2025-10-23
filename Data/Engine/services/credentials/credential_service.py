"""Expose read access to stored credentials."""

from __future__ import annotations

import logging
from typing import List, Optional

from Data.Engine.repositories.sqlite.credential_repository import SQLiteCredentialRepository

__all__ = ["CredentialService"]


class CredentialService:
    def __init__(
        self,
        repository: SQLiteCredentialRepository,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._repo = repository
        self._log = logger or logging.getLogger("borealis.engine.services.credentials")

    def list_credentials(
        self,
        *,
        site_id: Optional[int] = None,
        connection_type: Optional[str] = None,
    ) -> List[dict]:
        return self._repo.list_credentials(site_id=site_id, connection_type=connection_type)
