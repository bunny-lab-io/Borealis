# ======================================================
# Data\Engine\assembly_management\databases.py
# Description: Manages assembly SQLite databases with WAL/shared-cache configuration and schema validation.
#
# API Endpoints (if applicable): None
# ======================================================

"""SQLite persistence helpers for Engine assemblies."""

from __future__ import annotations

import datetime as _dt
import logging
import shutil
import sqlite3
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, Iterable, List, Optional

from .models import AssemblyDomain, AssemblyRecord, CachedAssembly, PayloadDescriptor


_SCHEMA_STATEMENTS: Iterable[str] = (
    """
    CREATE TABLE IF NOT EXISTS assemblies (
        assembly_guid TEXT PRIMARY KEY,
        display_name TEXT NOT NULL,
        summary TEXT,
        assembly_type TEXT NOT NULL,
        assembly_subtype TEXT,
        payload_json TEXT NOT NULL,
        payload_size_bytes INTEGER NOT NULL DEFAULT 0,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
    )
    """,
    "CREATE INDEX IF NOT EXISTS idx_assemblies_type ON assemblies(assembly_type)",
)

_ASSEMBLIES_COLUMNS: tuple[str, ...] = (
    "assembly_guid",
    "display_name",
    "summary",
    "assembly_type",
    "assembly_subtype",
    "payload_json",
    "payload_size_bytes",
    "created_at",
    "updated_at",
)

def _parse_datetime(value: str) -> _dt.datetime:
    try:
        return _dt.datetime.fromisoformat(value)
    except Exception:
        return _dt.datetime.utcnow()


@dataclass(slots=True)
class AssemblyDatabasePaths:
    """Resolved paths for staging and runtime copies of an assembly database."""

    staging: Path
    runtime: Path


class AssemblyDatabaseManager:
    """Coordinates SQLite database access for assembly persistence."""

    def __init__(self, staging_root: Path, runtime_root: Path, *, logger: Optional[logging.Logger] = None) -> None:
        self._staging_root = staging_root
        self._runtime_root = runtime_root
        self._logger = logger or logging.getLogger(__name__)
        self._paths: Dict[AssemblyDomain, AssemblyDatabasePaths] = {}
        self._staging_root.mkdir(parents=True, exist_ok=True)
        self._runtime_root.mkdir(parents=True, exist_ok=True)
        for domain in AssemblyDomain:
            staging = (self._staging_root / domain.database_name).resolve()
            runtime = (self._runtime_root / domain.database_name).resolve()
            self._paths[domain] = AssemblyDatabasePaths(staging=staging, runtime=runtime)

    @property
    def staging_root(self) -> Path:
        return self._staging_root

    @property
    def runtime_root(self) -> Path:
        return self._runtime_root

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------
    def initialise(self) -> None:
        """Ensure all databases exist, apply schema, and mirror to the runtime directory."""

        for domain in AssemblyDomain:
            self._ensure_runtime_database(domain)
            conn = self._open_connection(domain)
            try:
                self._apply_schema(conn)
                conn.commit()
            finally:
                conn.close()

    def reset_domain(self, domain: AssemblyDomain) -> None:
        """Remove all assemblies and payload metadata for the specified domain."""

        conn = self._open_connection(domain)
        try:
            cur = conn.cursor()
            cur.execute("DELETE FROM assemblies")
            conn.commit()
        finally:
            conn.close()
            self._mirror_database(domain)

    def load_all(self, domain: AssemblyDomain) -> List[AssemblyRecord]:
        """Load all assembly records for the given domain."""

        conn = self._open_connection(domain, readonly=True)
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT
                    assembly_guid,
                    display_name,
                    summary,
                    assembly_type,
                    assembly_subtype,
                    payload_json,
                    payload_size_bytes,
                    created_at,
                    updated_at
                FROM assemblies
                """
            )
            records: List[AssemblyRecord] = []
            for row in cur.fetchall():
                payload_json = row["payload_json"] or "{}"
                assembly_type = str(row["assembly_type"] or "script").strip().lower() or "script"
                assembly_subtype = row["assembly_subtype"]
                if not assembly_subtype:
                    if assembly_type == "workflow":
                        assembly_subtype = "workflow"
                    elif assembly_type == "ansible":
                        assembly_subtype = "ansible"
                    else:
                        assembly_subtype = "powershell"

                payload_size = int(row["payload_size_bytes"] or 0)
                if payload_size <= 0:
                    payload_size = len(payload_json.encode("utf-8"))
                row_created = _parse_datetime(row["created_at"])
                row_updated = _parse_datetime(row["updated_at"])
                payload = PayloadDescriptor(
                    assembly_guid=row["assembly_guid"],
                    file_name="payload.json",
                    file_extension=".json",
                    size_bytes=payload_size,
                    created_at=row_created,
                    updated_at=row_updated,
                )
                record = AssemblyRecord(
                    assembly_guid=row["assembly_guid"],
                    display_name=row["display_name"],
                    summary=row["summary"],
                    assembly_type=assembly_type,
                    assembly_subtype=assembly_subtype,
                    payload=payload,
                    payload_json=payload_json,
                    created_at=row_created,
                    updated_at=row_updated,
                )
                records.append(record)
            return records
        finally:
            conn.close()

    def upsert_record(self, domain: AssemblyDomain, entry: CachedAssembly) -> None:
        """Insert or update an assembly record and its payload metadata."""

        record = entry.record
        conn = self._open_connection(domain)
        try:
            cur = conn.cursor()
            payload = record.payload
            payload_size = int(payload.size_bytes or 0)
            if payload_size <= 0:
                payload_size = len((record.payload_json or "{}").encode("utf-8"))
            cur.execute(
                """
                INSERT INTO assemblies (
                    assembly_guid,
                    display_name,
                    summary,
                    assembly_type,
                    assembly_subtype,
                    payload_json,
                    payload_size_bytes,
                    created_at,
                    updated_at
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(assembly_guid) DO UPDATE SET
                    display_name = excluded.display_name,
                    summary = excluded.summary,
                    assembly_type = excluded.assembly_type,
                    assembly_subtype = excluded.assembly_subtype,
                    payload_json = excluded.payload_json,
                    payload_size_bytes = excluded.payload_size_bytes,
                    updated_at = excluded.updated_at
                """,
                (
                    record.assembly_guid,
                    record.display_name,
                    record.summary,
                    record.assembly_type,
                    record.assembly_subtype,
                    record.payload_json,
                    payload_size,
                    record.created_at.isoformat(),
                    record.updated_at.isoformat(),
                ),
            )
            conn.commit()
        finally:
            conn.close()
            self._mirror_database(domain)

    def delete_record(self, domain: AssemblyDomain, entry: CachedAssembly) -> None:
        """Delete an assembly and its payload metadata."""

        record = entry.record
        conn = self._open_connection(domain)
        try:
            cur = conn.cursor()
            cur.execute("DELETE FROM assemblies WHERE assembly_guid = ?", (record.assembly_guid,))
            conn.commit()
        finally:
            conn.close()
            self._mirror_database(domain)

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------
    def _ensure_runtime_database(self, domain: AssemblyDomain) -> None:
        """Ensure the runtime database exists by copying the staging asset when needed."""

        paths = self._paths[domain]
        runtime_db = paths.runtime
        staging_db = paths.staging
        runtime_db.parent.mkdir(parents=True, exist_ok=True)
        if runtime_db.exists():
            return

        if staging_db.exists():
            for suffix in ("", "-wal", "-shm"):
                staging_candidate = staging_db.parent / f"{staging_db.name}{suffix}"
                runtime_candidate = runtime_db.parent / f"{runtime_db.name}{suffix}"
                if staging_candidate.exists():
                    try:
                        shutil.copy2(staging_candidate, runtime_candidate)
                    except Exception as exc:  # pragma: no cover - best effort seed
                        self._logger.debug(
                            "Failed to seed runtime assembly database file %s -> %s: %s",
                            staging_candidate,
                            runtime_candidate,
                            exc,
                        )
            return

        runtime_db.touch()

    def _open_connection(self, domain: AssemblyDomain, *, readonly: bool = False) -> sqlite3.Connection:
        self._ensure_runtime_database(domain)
        paths = self._paths[domain]
        flags = "ro" if readonly else "rwc"
        uri = f"file:{paths.runtime.as_posix()}?mode={flags}&cache=shared"
        conn = sqlite3.connect(uri, uri=True, timeout=30)
        if readonly:
            conn.isolation_level = None
        conn.row_factory = sqlite3.Row
        cur = conn.cursor()
        cur.execute("PRAGMA journal_mode=WAL")
        cur.execute("PRAGMA synchronous=NORMAL")
        cur.execute("PRAGMA foreign_keys=ON")
        cur.execute("PRAGMA busy_timeout=5000")
        cur.execute("PRAGMA cache_size=-8000")
        cur.execute("PRAGMA temp_store=MEMORY")
        return conn

    def _apply_schema(self, conn: sqlite3.Connection) -> None:
        cur = conn.cursor()
        for statement in _SCHEMA_STATEMENTS:
            cur.execute(statement)
        self._validate_schema(cur)
        conn.commit()

    def _validate_schema(self, cur: sqlite3.Cursor) -> None:
        cur.execute("PRAGMA table_info('assemblies')")
        columns = [row[1] for row in cur.fetchall()]
        if not columns:
            raise RuntimeError("Assemblies schema validation failed: missing assemblies table.")
        expected_set = set(_ASSEMBLIES_COLUMNS)
        column_set = set(columns)
        missing = [name for name in _ASSEMBLIES_COLUMNS if name not in column_set]
        extra = [name for name in columns if name not in expected_set]
        if missing or extra:
            problems: List[str] = []
            if missing:
                problems.append(f"missing columns: {', '.join(missing)}")
            if extra:
                problems.append(f"unexpected columns: {', '.join(extra)}")
            joined = "; ".join(problems)
            raise RuntimeError(
                f"Assemblies schema validation failed ({joined}). "
                "Run the documented manual SQL rebuild for assemblies DB files."
            )

    def _mirror_database(self, domain: AssemblyDomain) -> None:
        paths = self._paths[domain]
        runtime_db = paths.runtime
        staging_db = paths.staging
        staging_db.parent.mkdir(parents=True, exist_ok=True)

        for suffix in ("", "-wal", "-shm"):
            runtime_candidate = runtime_db.parent / f"{runtime_db.name}{suffix}"
            staging_candidate = staging_db.parent / f"{staging_db.name}{suffix}"
            if not runtime_candidate.exists():
                if staging_candidate.exists():
                    try:
                        staging_candidate.unlink()
                    except Exception as exc:
                        self._logger.debug(
                            "Failed to remove stale staging assembly database file %s: %s",
                            staging_candidate,
                            exc,
                        )
                continue
            try:
                shutil.copy2(runtime_candidate, staging_candidate)
            except Exception as exc:
                self._logger.debug(
                    "Failed to mirror runtime assembly database file %s -> %s: %s",
                    runtime_candidate,
                    staging_candidate,
                    exc,
                )
