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
    CREATE TABLE IF NOT EXISTS assemblies (
        assembly_guid TEXT PRIMARY KEY,
        display_name TEXT NOT NULL,
        summary TEXT,
        category TEXT,
        assembly_kind TEXT NOT NULL,
        assembly_type TEXT,
        version INTEGER NOT NULL DEFAULT 1,
        metadata_json TEXT,
        tags_json TEXT,
        checksum TEXT,
        payload_type TEXT NOT NULL,
        payload_file_name TEXT NOT NULL,
        payload_file_extension TEXT NOT NULL,
        payload_size_bytes INTEGER NOT NULL DEFAULT 0,
        payload_checksum TEXT,
        payload_created_at TEXT NOT NULL,
        payload_updated_at TEXT NOT NULL,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
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
            conn = self._open_connection(domain)
            try:
                self._apply_schema(conn)
                conn.commit()
            finally:
                conn.close()
            self._mirror_database(domain)

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
                    category,
                    assembly_kind,
                    assembly_type,
                    version,
                    metadata_json,
                    tags_json,
                    checksum AS assembly_checksum,
                    payload_type,
                    payload_file_name,
                    payload_file_extension,
                    payload_size_bytes,
                    payload_checksum,
                    payload_created_at,
                    payload_updated_at,
                    created_at AS assembly_created_at,
                    updated_at AS assembly_updated_at
                FROM assemblies
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
                    assembly_guid=row["assembly_guid"],
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
                    assembly_guid=row["assembly_guid"],
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
            metadata_json = json.dumps(record.metadata or {})
            tags_json = json.dumps(record.tags or {})
            cur.execute(
                """
                INSERT INTO assemblies (
                    assembly_guid,
                    display_name,
                    summary,
                    category,
                    assembly_kind,
                    assembly_type,
                    version,
                    metadata_json,
                    tags_json,
                    checksum,
                    payload_type,
                    payload_file_name,
                    payload_file_extension,
                    payload_size_bytes,
                    payload_checksum,
                    payload_created_at,
                    payload_updated_at,
                    created_at,
                    updated_at
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(assembly_guid) DO UPDATE SET
                    display_name = excluded.display_name,
                    summary = excluded.summary,
                    category = excluded.category,
                    assembly_kind = excluded.assembly_kind,
                    assembly_type = excluded.assembly_type,
                    version = excluded.version,
                    metadata_json = excluded.metadata_json,
                    tags_json = excluded.tags_json,
                    checksum = excluded.checksum,
                    payload_type = excluded.payload_type,
                    payload_file_name = excluded.payload_file_name,
                    payload_file_extension = excluded.payload_file_extension,
                    payload_size_bytes = excluded.payload_size_bytes,
                    payload_checksum = excluded.payload_checksum,
                    payload_created_at = excluded.payload_created_at,
                    payload_updated_at = excluded.payload_updated_at,
                    updated_at = excluded.updated_at
                """,
                (
                    record.assembly_guid,
                    record.display_name,
                    record.summary,
                    record.category,
                    record.assembly_kind,
                    record.assembly_type,
                    record.version,
                    metadata_json,
                    tags_json,
                    record.checksum,
                    payload.payload_type.value,
                    payload.file_name,
                    payload.file_extension,
                    payload.size_bytes,
                    payload.checksum,
                    payload.created_at.isoformat(),
                    payload.updated_at.isoformat(),
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
        self._migrate_legacy_schema(cur)
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
    
    def _migrate_legacy_schema(self, cur: sqlite3.Cursor) -> None:
        """Upgrade legacy assembly/payload tables to the consolidated schema."""

        cur.execute("SELECT name FROM sqlite_master WHERE type='table' AND name='assemblies'")
        if not cur.fetchone():
            return

        cur.execute("PRAGMA table_info('assemblies')")
        legacy_columns = {row[1] for row in cur.fetchall()}
        if "assembly_guid" in legacy_columns:
            return  # Already migrated

        self._logger.info("Migrating legacy assemblies schema to assembly_guid layout.")
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS assemblies_new (
                assembly_guid TEXT PRIMARY KEY,
                display_name TEXT NOT NULL,
                summary TEXT,
                category TEXT,
                assembly_kind TEXT NOT NULL,
                assembly_type TEXT,
                version INTEGER NOT NULL DEFAULT 1,
                metadata_json TEXT,
                tags_json TEXT,
                checksum TEXT,
                payload_type TEXT NOT NULL,
                payload_file_name TEXT NOT NULL,
                payload_file_extension TEXT NOT NULL,
                payload_size_bytes INTEGER NOT NULL DEFAULT 0,
                payload_checksum TEXT,
                payload_created_at TEXT NOT NULL,
                payload_updated_at TEXT NOT NULL,
                created_at TEXT NOT NULL,
                updated_at TEXT NOT NULL
            )
            """
        )

        cur.execute("SELECT name FROM sqlite_master WHERE type='table' AND name='payloads'")
        has_payloads = cur.fetchone() is not None

        if has_payloads:
            cur.execute(
                """
                INSERT INTO assemblies_new (
                    assembly_guid,
                    display_name,
                    summary,
                    category,
                    assembly_kind,
                    assembly_type,
                    version,
                    metadata_json,
                    tags_json,
                    checksum,
                    payload_type,
                    payload_file_name,
                    payload_file_extension,
                    payload_size_bytes,
                    payload_checksum,
                    payload_created_at,
                    payload_updated_at,
                    created_at,
                    updated_at
                )
                SELECT
                    a.assembly_id AS assembly_guid,
                    a.display_name,
                    a.summary,
                    a.category,
                    a.assembly_kind,
                    a.assembly_type,
                    a.version,
                    COALESCE(a.metadata_json, '{}') AS metadata_json,
                    COALESCE(a.tags_json, '{}') AS tags_json,
                    a.checksum,
                    COALESCE(p.payload_type, 'unknown') AS payload_type,
                    COALESCE(p.file_name, 'payload.json') AS payload_file_name,
                    COALESCE(p.file_extension, '.json') AS payload_file_extension,
                    COALESCE(p.size_bytes, 0) AS payload_size_bytes,
                    COALESCE(p.checksum, '') AS payload_checksum,
                    COALESCE(p.created_at, a.created_at) AS payload_created_at,
                    COALESCE(p.updated_at, a.updated_at) AS payload_updated_at,
                    a.created_at,
                    a.updated_at
                FROM assemblies AS a
                LEFT JOIN payloads AS p ON p.payload_guid = a.payload_guid
                """
            )
        else:
            cur.execute(
                """
                INSERT INTO assemblies_new (
                    assembly_guid,
                    display_name,
                    summary,
                    category,
                    assembly_kind,
                    assembly_type,
                    version,
                    metadata_json,
                    tags_json,
                    checksum,
                    payload_type,
                    payload_file_name,
                    payload_file_extension,
                    payload_size_bytes,
                    payload_checksum,
                    payload_created_at,
                    payload_updated_at,
                    created_at,
                    updated_at
                )
                SELECT
                    assembly_id AS assembly_guid,
                    display_name,
                    summary,
                    category,
                    assembly_kind,
                    assembly_type,
                    version,
                    COALESCE(metadata_json, '{}'),
                    COALESCE(tags_json, '{}'),
                    checksum,
                    'unknown' AS payload_type,
                    'payload.json' AS payload_file_name,
                    '.json' AS payload_file_extension,
                    0 AS payload_size_bytes,
                    '' AS payload_checksum,
                    created_at AS payload_created_at,
                    updated_at AS payload_updated_at,
                    created_at,
                    updated_at
                FROM assemblies
                """
            )

        cur.execute("DROP TABLE assemblies")
        if has_payloads:
            cur.execute("DROP TABLE payloads")
        cur.execute("ALTER TABLE assemblies_new RENAME TO assemblies")
        self._logger.info("Legacy assemblies schema migration completed.")
