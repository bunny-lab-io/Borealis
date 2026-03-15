# ======================================================
# Data\Engine\assembly_management\bootstrap.py
# Description: Startup helpers that initialise assembly databases and cache services.
#
# API Endpoints (if applicable): None
# ======================================================

"""Assembly runtime bootstrap logic."""

from __future__ import annotations

import copy
import logging
import os
import threading
from pathlib import Path
from typing import Dict, List, Mapping, Optional

from .databases import AssemblyDatabaseManager
from .models import AssemblyDomain, AssemblyRecord, CachedAssembly
from ..services.assemblies.official_catalog import (
    DEFAULT_OFFICIAL_REPO_URL,
    DEFAULT_REMOTE_REFRESH_SECONDS,
    OfficialAssemblyCatalogService,
)


class AssemblyCache:
    """Caches assemblies in memory and coordinates background persistence."""

    _singleton: Optional["AssemblyCache"] = None
    _singleton_lock = threading.Lock()

    @classmethod
    def initialise(
        cls,
        database_manager: AssemblyDatabaseManager,
        *,
        flush_interval_seconds: float = 60.0,
        logger: Optional[logging.Logger] = None,
    ) -> "AssemblyCache":
        with cls._singleton_lock:
            if cls._singleton is None:
                cls._singleton = cls(
                    database_manager=database_manager,
                    flush_interval_seconds=flush_interval_seconds,
                    logger=logger,
                )
            return cls._singleton

    @classmethod
    def get(cls) -> Optional["AssemblyCache"]:
        with cls._singleton_lock:
            return cls._singleton

    def __init__(
        self,
        *,
        database_manager: AssemblyDatabaseManager,
        flush_interval_seconds: float,
        logger: Optional[logging.Logger],
    ) -> None:
        self._db_manager = database_manager
        self._flush_interval = max(5.0, float(flush_interval_seconds))
        self._logger = logger or logging.getLogger(__name__)
        self._store: Dict[str, CachedAssembly] = {}
        self._dirty: Dict[str, CachedAssembly] = {}
        self._pending_deletes: Dict[str, CachedAssembly] = {}
        self._domain_index: Dict[AssemblyDomain, Dict[str, CachedAssembly]] = {
            domain: {} for domain in AssemblyDomain
        }
        self._lock = threading.RLock()
        self._stop_event = threading.Event()
        self._flush_event = threading.Event()
        self.reload()
        self._worker = threading.Thread(target=self._worker_loop, name="AssemblyCacheFlush", daemon=True)
        self._worker.start()

    # ------------------------------------------------------------------
    # Cache interactions
    # ------------------------------------------------------------------
    def reload(self) -> None:
        """Hydrate cache from persistence."""

        with self._lock:
            self._store.clear()
            self._dirty.clear()
            self._pending_deletes.clear()
            for domain in AssemblyDomain:
                self._domain_index[domain].clear()
                records = self._db_manager.load_all(domain)
                for record in records:
                    entry = CachedAssembly(domain=domain, record=record, is_dirty=False, last_persisted=record.updated_at)
                    self._store[record.assembly_guid] = entry
                    self._domain_index[domain][record.assembly_guid] = entry

    def get_entry(self, assembly_guid: str) -> Optional[CachedAssembly]:
        """Return a defensive copy of the cached assembly."""

        with self._lock:
            entry = self._store.get(assembly_guid)
            if entry is None:
                return None
            return self._clone_entry(entry)

    def list_entries(self, *, domain: Optional[AssemblyDomain] = None) -> List[CachedAssembly]:
        """Return defensive copies of cached assemblies, optionally filtered by domain."""

        with self._lock:
            if domain is None:
                return [self._clone_entry(entry) for entry in self._store.values()]
            return [self._clone_entry(entry) for entry in self._domain_index[domain].values()]

    def get(self, assembly_guid: str) -> Optional[AssemblyRecord]:
        with self._lock:
            entry = self._store.get(assembly_guid)
            if not entry:
                return None
            return entry.record

    def list_records(self, *, domain: Optional[AssemblyDomain] = None) -> List[AssemblyRecord]:
        with self._lock:
            if domain is None:
                return [entry.record for entry in self._store.values()]
            return [entry.record for entry in self._domain_index[domain].values()]

    def stage_upsert(self, domain: AssemblyDomain, record: AssemblyRecord) -> None:
        with self._lock:
            entry = self._store.get(record.assembly_guid)
            if entry is None:
                entry = CachedAssembly(domain=domain, record=record, is_dirty=True)
                entry.mark_dirty()
                self._store[record.assembly_guid] = entry
                self._domain_index[domain][record.assembly_guid] = entry
            else:
                entry.domain = domain
                entry.record = record
                entry.mark_dirty()
            self._pending_deletes.pop(record.assembly_guid, None)
            self._dirty[record.assembly_guid] = entry
            self._flush_event.set()

    def stage_delete(self, assembly_guid: str) -> None:
        with self._lock:
            entry = self._store.get(assembly_guid)
            if not entry:
                return
            entry.is_dirty = True
            self._dirty.pop(assembly_guid, None)
            self._pending_deletes[assembly_guid] = entry
            self._flush_event.set()

    def describe(self) -> List[Dict[str, str]]:
        with self._lock:
            snapshot = []
            for assembly_guid, entry in self._store.items():
                snapshot.append(
                    {
                        "assembly_guid": assembly_guid,
                        "domain": entry.domain.value,
                        "is_dirty": "true" if entry.is_dirty else "false",
                        "dirty_since": entry.dirty_since.isoformat() if entry.dirty_since else "",
                        "last_persisted": entry.last_persisted.isoformat() if entry.last_persisted else "",
                    }
                )
            return snapshot

    def flush_now(self) -> None:
        self._flush_dirty_entries()

    def shutdown(self, *, flush: bool = True) -> None:
        self._stop_event.set()
        self._flush_event.set()
        if self._worker.is_alive():
            self._worker.join(timeout=10.0)
        if flush:
            self._flush_dirty_entries()

    def read_payload_bytes(self, assembly_guid: str) -> bytes:
        """Return the payload bytes for the specified assembly from the PostgreSQL-backed cache."""

        with self._lock:
            entry = self._store.get(assembly_guid)
        if not entry:
            raise KeyError(f"Assembly '{assembly_guid}' not found in cache")
        return (entry.record.payload_json or "{}").encode("utf-8")

    @property
    def database_manager(self) -> AssemblyDatabaseManager:
        return self._db_manager

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------
    def _worker_loop(self) -> None:
        while not self._stop_event.is_set():
            triggered = self._flush_event.wait(timeout=self._flush_interval)
            if self._stop_event.is_set():
                break
            if triggered:
                self._flush_event.clear()
            self._flush_dirty_entries()

    def _flush_dirty_entries(self) -> None:
        dirty_items: List[CachedAssembly]
        delete_items: List[CachedAssembly]
        with self._lock:
            if not self._dirty and not self._pending_deletes:
                return
            dirty_items = list(self._dirty.values())
            delete_items = list(self._pending_deletes.values())
            self._dirty.clear()
            self._pending_deletes.clear()

        for entry in delete_items:
            try:
                self._db_manager.delete_record(entry.domain, entry)
                with self._lock:
                    self._store.pop(entry.record.assembly_guid, None)
                    self._domain_index[entry.domain].pop(entry.record.assembly_guid, None)
            except Exception as exc:
                self._logger.error(
                    "Failed to delete assembly %s in domain %s: %s",
                    entry.record.assembly_guid,
                    entry.domain.value,
                    exc,
                )
                with self._lock:
                    self._pending_deletes[entry.record.assembly_guid] = entry
                return

        for entry in dirty_items:
            try:
                self._db_manager.upsert_record(entry.domain, entry)
                entry.mark_clean()
            except Exception as exc:
                self._logger.error(
                    "Failed to flush assembly %s in domain %s: %s",
                    entry.record.assembly_guid,
                    entry.domain.value,
                    exc,
                )
                with self._lock:
                    self._dirty[entry.record.assembly_guid] = entry
                break

    def _clone_entry(self, entry: CachedAssembly) -> CachedAssembly:
        record_copy = copy.deepcopy(entry.record)
        return CachedAssembly(
            domain=entry.domain,
            record=record_copy,
            is_dirty=entry.is_dirty,
            last_persisted=entry.last_persisted,
            dirty_since=entry.dirty_since,
        )


def initialise_assembly_runtime(
    *,
    logger: Optional[logging.Logger] = None,
    config: Optional[Mapping[str, object]] = None,
) -> AssemblyCache:
    """Initialise assembly persistence subsystems and return the cache instance."""

    catalog_root = _discover_official_catalog_root(config)
    database_url = ""
    if config:
        database_url = str(config.get("database_url") or config.get("DATABASE_URL") or "").strip()
    db_manager = AssemblyDatabaseManager(
        database_url=database_url,
        logger=logger,
    )
    db_manager.initialise()

    flush_interval = _resolve_flush_interval(config)
    cache = AssemblyCache.initialise(
        database_manager=db_manager,
        flush_interval_seconds=flush_interval,
        logger=logger,
    )
    repo_url, manifest_url, refresh_seconds = _resolve_official_catalog_config(config)
    catalog_service = OfficialAssemblyCatalogService(
        cache=cache,
        database_manager=db_manager,
        logger=logger,
        bundled_root=catalog_root,
        repo_url=repo_url,
        manifest_url=manifest_url,
        refresh_seconds=refresh_seconds,
    )
    catalog_service.sync_bundled_catalog()
    return cache


# ----------------------------------------------------------------------
# Helper utilities
# ----------------------------------------------------------------------
def _resolve_flush_interval(config: Optional[Mapping[str, object]]) -> float:
    if not config:
        return 60.0
    for key in ("assemblies_flush_interval", "ASSEMBLIES_FLUSH_INTERVAL"):
        if key in config:
            value = config[key]
            try:
                return max(5.0, float(value))
            except (TypeError, ValueError):
                continue
    return 60.0


def _resolve_official_catalog_config(config: Optional[Mapping[str, object]]) -> tuple[str, str, int]:
    if not config:
        return DEFAULT_OFFICIAL_REPO_URL, "", DEFAULT_REMOTE_REFRESH_SECONDS

    repo_url = str(
        config.get("official_assemblies_repo_url")
        or config.get("OFFICIAL_ASSEMBLIES_REPO_URL")
        or DEFAULT_OFFICIAL_REPO_URL
    ).strip() or DEFAULT_OFFICIAL_REPO_URL
    manifest_url = str(
        config.get("official_assemblies_manifest_url")
        or config.get("OFFICIAL_ASSEMBLIES_MANIFEST_URL")
        or ""
    ).strip()
    refresh_raw = config.get("official_assemblies_refresh_seconds") or config.get("OFFICIAL_ASSEMBLIES_REFRESH_SECONDS")
    try:
        refresh_seconds = int(refresh_raw) if refresh_raw is not None else DEFAULT_REMOTE_REFRESH_SECONDS
    except (TypeError, ValueError):
        refresh_seconds = DEFAULT_REMOTE_REFRESH_SECONDS
    return repo_url, manifest_url, max(30, refresh_seconds)


def _discover_official_catalog_root(config: Optional[Mapping[str, object]] = None) -> Path:
    if config:
        override = str(
            config.get("official_assemblies_root")
            or config.get("OFFICIAL_ASSEMBLIES_ROOT")
            or ""
        ).strip()
        if override:
            path = Path(override).expanduser().resolve()
            path.mkdir(parents=True, exist_ok=True)
            return path

    module_path = Path(__file__).resolve()
    for candidate in (module_path, *module_path.parents):
        data_dir = candidate / "Engine" / "Data" / "Engine" / "Official_Assemblies"
        if data_dir.is_dir():
            return data_dir.resolve()
        data_dir = candidate / "Data" / "Engine" / "Official_Assemblies"
        if data_dir.is_dir():
            return data_dir.resolve()
    for candidate in _path_discovery_roots(module_path):
        data_dir = candidate / "Engine" / "Data" / "Engine" / "Official_Assemblies"
        try:
            data_dir.mkdir(parents=True, exist_ok=True)
            return data_dir.resolve()
        except Exception:
            continue
        data_dir = candidate / "Data" / "Engine" / "Official_Assemblies"
        try:
            data_dir.mkdir(parents=True, exist_ok=True)
            return data_dir.resolve()
        except Exception:
            continue
    raise RuntimeError("Could not locate bundled official assemblies directory (expected Data/Engine/Official_Assemblies).")


def _path_discovery_roots(module_path: Path) -> List[Path]:
    roots: List[Path] = []
    seen: set[Path] = set()

    env_root = str(os.environ.get("BOREALIS_PROJECT_ROOT") or "").strip()
    if env_root:
        root = Path(env_root).resolve()
        if root not in seen:
            seen.add(root)
            roots.append(root)

    for candidate in module_path.parents:
        if (candidate / "Engine").is_dir() or (candidate / "Data").is_dir():
            resolved = candidate.resolve()
            if resolved not in seen:
                seen.add(resolved)
                roots.append(resolved)

    return roots
