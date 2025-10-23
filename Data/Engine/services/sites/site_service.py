"""Site management service that mirrors the legacy Flask behaviour."""

from __future__ import annotations

import logging
from typing import Dict, Iterable, List, Optional

from Data.Engine.domain.sites import SiteDeviceMapping, SiteSummary
from Data.Engine.repositories.sqlite.site_repository import SQLiteSiteRepository

__all__ = ["SiteService"]


class SiteService:
    def __init__(self, repository: SQLiteSiteRepository, *, logger: Optional[logging.Logger] = None) -> None:
        self._repo = repository
        self._log = logger or logging.getLogger("borealis.engine.services.sites")

    def list_sites(self) -> List[SiteSummary]:
        return self._repo.list_sites()

    def create_site(self, name: str, description: str) -> SiteSummary:
        normalized_name = (name or "").strip()
        normalized_description = (description or "").strip()
        if not normalized_name:
            raise ValueError("missing_name")
        try:
            return self._repo.create_site(normalized_name, normalized_description)
        except ValueError as exc:
            if str(exc) == "duplicate":
                raise ValueError("duplicate") from exc
            raise

    def delete_sites(self, ids: Iterable[int]) -> int:
        normalized = []
        for value in ids:
            try:
                normalized.append(int(value))
            except Exception:
                continue
        if not normalized:
            return 0
        return self._repo.delete_sites(tuple(normalized))

    def rename_site(self, site_id: int, new_name: str) -> SiteSummary:
        normalized_name = (new_name or "").strip()
        if not normalized_name:
            raise ValueError("missing_name")
        try:
            return self._repo.rename_site(int(site_id), normalized_name)
        except ValueError as exc:
            if str(exc) == "duplicate":
                raise ValueError("duplicate") from exc
            raise

    def map_devices(self, hostnames: Optional[Iterable[str]] = None) -> Dict[str, SiteDeviceMapping]:
        return self._repo.map_devices(hostnames)

    def assign_devices(self, site_id: int, hostnames: Iterable[str]) -> None:
        try:
            numeric_id = int(site_id)
        except Exception as exc:
            raise ValueError("invalid_site_id") from exc
        normalized = [hn for hn in hostnames if isinstance(hn, str) and hn.strip()]
        if not normalized:
            raise ValueError("invalid_hostnames")
        try:
            self._repo.assign_devices(numeric_id, normalized)
        except LookupError as exc:
            if str(exc) == "not_found":
                raise LookupError("not_found") from exc
            raise

