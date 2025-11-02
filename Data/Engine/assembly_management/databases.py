# ======================================================
# Data\Engine\assembly_management\databases.py
# Description: Manages assembly SQLite databases with WAL/shared-cache configuration and schema validation.
#
# API Endpoints (if applicable): None
# ======================================================

"""SQLite persistence helpers for Engine assemblies."""

from __future__ import annotations

import datetime as _dt
import json
import logging
import shutil
import sqlite3
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, Iterable, List, Optional

from .models import AssemblyDomain, AssemblyRecord, CachedAssembly, PayloadDescriptor, PayloadType


_SCHEMA_STATEMENTS: Iterable[str] = (
    """
    CREATE TABLE IF NOT EXISTS payloads (
        payload_guid TEXT PRIMARY KEY,
        payload_type TEXT NOT NULL,
        file_name TEXT NOT NULL,
        file_extension TEXT NOT NULL,
        size_bytes INTEGER NOT NULL DEFAULT 0,
        checksum TEXT,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
    )
    """,
    """
    CREATE TABLE IF NOT EXISTS assemblies (
        assembly_id TEXT PRIMARY KEY,
        display_name TEXT NOT NULL,
        summary TEXT,
        category TEXT,
        assembly_kind TEXT NOT NULL,
        assembly_type TEXT,
        version INTEGER NOT NULL DEFAULT 1,
        payload_guid TEXT NOT NULL,
        metadata_json TEXT,
        tags_json TEXT,
        checksum TEXT,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        FOREIGN KEY(payload_guid) REFERENCES payloads(payload_guid) ON DELETE CASCADE
    )
    """,
    "CREATE INDEX IF NOT EXISTS idx_assemblies_kind ON assemblies(assembly_kind)",
    "CREATE INDEX IF NOT EXISTS idx_assemblies_category ON assemblies(category)",
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

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------
    def initialise(self) -> None:
        """Ensure all databases exist, apply schema, and mirror to the runtime directory."""

        for domain in AssemblyDomain:
            conn = self._open_connection(domain)
            try:
                self._apply_schema(conn)
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
                    a.assembly_id AS assembly_id,
                    a.display_name AS display_name,
                    a.summary AS summary,
                    a.category AS category,
                    a.assembly_kind AS assembly_kind,
                    a.assembly_type AS assembly_type,
                    a.version AS version,
                    a.payload_guid AS payload_guid,
                    a.metadata_json AS metadata_json,
                    a.tags_json AS tags_json,
                    a.checksum AS assembly_checksum,
                    a.created_at AS assembly_created_at,
                    a.updated_at AS assembly_updated_at,
                    p.payload_guid AS payload_guid,
                    p.payload_type AS payload_type,
                    p.file_name AS payload_file_name,
                    p.file_extension AS payload_file_extension,
                    p.size_bytes AS payload_size_bytes,
                    p.checksum AS payload_checksum,
                    p.created_at AS payload_created_at,
                    p.updated_at AS payload_updated_at
                FROM assemblies AS a
                JOIN payloads AS p ON p.payload_guid = a.payload_guid
                """
            )
            records: List[AssemblyRecord] = []
            for row in cur.fetchall():
                payload_type_raw = row["payload_type"]
                try:
                    payload_type = PayloadType(payload_type_raw)
                except Exception:
                    payload_type = PayloadType.UNKNOWN
                payload = PayloadDescriptor(
                    guid=row["payload_guid"],
                    payload_type=payload_type,
                    file_name=row["payload_file_name"],
                    file_extension=row["payload_file_extension"],
                    size_bytes=row["payload_size_bytes"],
                    checksum=row["payload_checksum"],
                    created_at=_parse_datetime(row["payload_created_at"]),
                    updated_at=_parse_datetime(row["payload_updated_at"]),
                )
                metadata_json = row["metadata_json"] or "{}"
                tags_json = row["tags_json"] or "{}"
                try:
                    metadata = json.loads(metadata_json)
                except Exception:
                    metadata = {}
                try:
                    tags = json.loads(tags_json)
                except Exception:
                    tags = {}
                record = AssemblyRecord(
                    assembly_id=row["assembly_id"],
                    display_name=row["display_name"],
                    summary=row["summary"],
                    category=row["category"],
                    assembly_kind=row["assembly_kind"],
                    assembly_type=row["assembly_type"],
                    version=row["version"],
                    payload=payload,
                    metadata=metadata,
                    tags=tags,
                    checksum=row["assembly_checksum"],
                    created_at=_parse_datetime(row["assembly_created_at"]),
                    updated_at=_parse_datetime(row["assembly_updated_at"]),
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
            cur.execute(
                """
                INSERT INTO payloads (payload_guid, payload_type, file_name, file_extension, size_bytes, checksum, created_at, updated_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(payload_guid) DO UPDATE SET
                    payload_type = excluded.payload_type,
                    file_name = excluded.file_name,
                    file_extension = excluded.file_extension,
                    size_bytes = excluded.size_bytes,
                    checksum = excluded.checksum,
                    updated_at = excluded.updated_at
                """,
                (
                    payload.guid,
                    payload.payload_type.value,
                    payload.file_name,
                    payload.file_extension,
                    payload.size_bytes,
                    payload.checksum,
                    payload.created_at.isoformat(),
                    payload.updated_at.isoformat(),
                ),
            )
            metadata_json = json.dumps(record.metadata or {})
            tags_json = json.dumps(record.tags or {})
            cur.execute(
                """
                INSERT INTO assemblies (
                    assembly_id,
                    display_name,
                    summary,
                    category,
                    assembly_kind,
                    assembly_type,
                    version,
                    payload_guid,
                    metadata_json,
                    tags_json,
                    checksum,
                    created_at,
                    updated_at
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(assembly_id) DO UPDATE SET
                    display_name = excluded.display_name,
                    summary = excluded.summary,
                    category = excluded.category,
                    assembly_kind = excluded.assembly_kind,
                    assembly_type = excluded.assembly_type,
                    version = excluded.version,
                    payload_guid = excluded.payload_guid,
                    metadata_json = excluded.metadata_json,
                    tags_json = excluded.tags_json,
                    checksum = excluded.checksum,
                    updated_at = excluded.updated_at
                """,
                (
                    record.assembly_id,
                    record.display_name,
                    record.summary,
                    record.category,
                    record.assembly_kind,
                    record.assembly_type,
                    record.version,
                    payload.guid,
                    metadata_json,
                    tags_json,
                    record.checksum,
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
            cur.execute("DELETE FROM assemblies WHERE assembly_id = ?", (record.assembly_id,))
            conn.commit()
        finally:
            conn.close()
            self._mirror_database(domain)

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------
    def _open_connection(self, domain: AssemblyDomain, *, readonly: bool = False) -> sqlite3.Connection:
        paths = self._paths[domain]
        flags = "ro" if readonly else "rwc"
        uri = f"file:{paths.staging.as_posix()}?mode={flags}&cache=shared"
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
        conn.commit()

    def _mirror_database(self, domain: AssemblyDomain) -> None:
        paths = self._paths[domain]
        staging_db = paths.staging
        runtime_db = paths.runtime
        runtime_db.parent.mkdir(parents=True, exist_ok=True)

        for suffix in ("", "-wal", "-shm"):
            staging_candidate = staging_db.parent / f"{staging_db.name}{suffix}"
            runtime_candidate = runtime_db.parent / f"{runtime_db.name}{suffix}"
            if staging_candidate.exists():
                try:
                    shutil.copy2(staging_candidate, runtime_candidate)
                except Exception as exc:  # pragma: no cover - best effort mirror
                    self._logger.debug(
                        "Failed to mirror assembly database file %s -> %s: %s",
                        staging_candidate,
                        runtime_candidate,
                        exc,
                    )

