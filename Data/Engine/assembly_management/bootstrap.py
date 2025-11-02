# ======================================================
# Data\Engine\assembly_management\bootstrap.py
# Description: Startup helpers that initialise assembly databases, payload storage, and cache services.
#
# API Endpoints (if applicable): None
# ======================================================

"""Assembly runtime bootstrap logic."""

from __future__ import annotations

import logging
import threading
from pathlib import Path
from typing import Dict, List, Mapping, Optional

from .databases import AssemblyDatabaseManager
from .models import AssemblyDomain, AssemblyRecord, CachedAssembly
from .payloads import PayloadManager


class AssemblyCache:
    """Caches assemblies in memory and coordinates background persistence."""

    _singleton: Optional["AssemblyCache"] = None
    _singleton_lock = threading.Lock()

    @classmethod
    def initialise(
        cls,
        database_manager: AssemblyDatabaseManager,
        payload_manager: PayloadManager,
        *,
        flush_interval_seconds: float = 60.0,
        logger: Optional[logging.Logger] = None,
    ) -> "AssemblyCache":
        with cls._singleton_lock:
            if cls._singleton is None:
                cls._singleton = cls(
                    database_manager=database_manager,
                    payload_manager=payload_manager,
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
        payload_manager: PayloadManager,
        flush_interval_seconds: float,
        logger: Optional[logging.Logger],
    ) -> None:
        self._db_manager = database_manager
        self._payload_manager = payload_manager
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
                    self._payload_manager.ensure_runtime_copy(record.payload)
                    entry = CachedAssembly(domain=domain, record=record, is_dirty=False, last_persisted=record.updated_at)
                    self._store[record.assembly_id] = entry
                    self._domain_index[domain][record.assembly_id] = entry

    def get(self, assembly_id: str) -> Optional[AssemblyRecord]:
        with self._lock:
            entry = self._store.get(assembly_id)
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
            entry = self._store.get(record.assembly_id)
            if entry is None:
                entry = CachedAssembly(domain=domain, record=record, is_dirty=True)
                entry.mark_dirty()
                self._store[record.assembly_id] = entry
                self._domain_index[domain][record.assembly_id] = entry
            else:
                entry.domain = domain
                entry.record = record
                entry.mark_dirty()
            self._pending_deletes.pop(record.assembly_id, None)
            self._dirty[record.assembly_id] = entry
            self._flush_event.set()

    def stage_delete(self, assembly_id: str) -> None:
        with self._lock:
            entry = self._store.get(assembly_id)
            if not entry:
                return
            entry.is_dirty = True
            self._dirty.pop(assembly_id, None)
            self._pending_deletes[assembly_id] = entry
            self._flush_event.set()

    def describe(self) -> List[Dict[str, str]]:
        with self._lock:
            snapshot = []
            for assembly_id, entry in self._store.items():
                snapshot.append(
                    {
                        "assembly_id": assembly_id,
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
                self._payload_manager.delete_payload(entry.record.payload)
                with self._lock:
                    self._store.pop(entry.record.assembly_id, None)
                    self._domain_index[entry.domain].pop(entry.record.assembly_id, None)
            except Exception as exc:
                self._logger.error(
                    "Failed to delete assembly %s in domain %s: %s",
                    entry.record.assembly_id,
                    entry.domain.value,
                    exc,
                )
                with self._lock:
                    self._pending_deletes[entry.record.assembly_id] = entry
                return

        for entry in dirty_items:
            try:
                self._db_manager.upsert_record(entry.domain, entry)
                self._payload_manager.ensure_runtime_copy(entry.record.payload)
                entry.mark_clean()
            except Exception as exc:
                self._logger.error(
                    "Failed to flush assembly %s in domain %s: %s",
                    entry.record.assembly_id,
                    entry.domain.value,
                    exc,
                )
                with self._lock:
                    self._dirty[entry.record.assembly_id] = entry
                break


def initialise_assembly_runtime(
    *,
    logger: Optional[logging.Logger] = None,
    config: Optional[Mapping[str, object]] = None,
) -> AssemblyCache:
    """Initialise assembly persistence subsystems and return the cache instance."""

    staging_root = _discover_staging_root()
    runtime_root = _discover_runtime_root()
    payload_staging = staging_root / "Payloads"
    payload_runtime = runtime_root / "Payloads"

    db_manager = AssemblyDatabaseManager(staging_root=staging_root, runtime_root=runtime_root, logger=logger)
    db_manager.initialise()

    payload_manager = PayloadManager(staging_root=payload_staging, runtime_root=payload_runtime, logger=logger)
    flush_interval = _resolve_flush_interval(config)

    return AssemblyCache.initialise(
        database_manager=db_manager,
        payload_manager=payload_manager,
        flush_interval_seconds=flush_interval,
        logger=logger,
    )


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


def _discover_runtime_root() -> Path:
    module_path = Path(__file__).resolve()
    for candidate in (module_path, *module_path.parents):
        engine_dir = candidate / "Engine"
        assemblies_dir = engine_dir / "Assemblies"
        if assemblies_dir.is_dir():
            return assemblies_dir.resolve()
    raise RuntimeError("Could not locate runtime assemblies directory (expected Engine/Assemblies).")


def _discover_staging_root() -> Path:
    module_path = Path(__file__).resolve()
    for candidate in (module_path, *module_path.parents):
        data_dir = candidate / "Data" / "Engine" / "Assemblies"
        if data_dir.is_dir():
            return data_dir.resolve()
    raise RuntimeError("Could not locate staging assemblies directory (expected Data/Engine/Assemblies).")

