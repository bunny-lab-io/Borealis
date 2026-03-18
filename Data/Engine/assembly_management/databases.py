# ======================================================
# Data\Engine\assembly_management\databases.py
# Description: Manages PostgreSQL-backed assembly persistence for Borealis Engine domains.
#
# API Endpoints (if applicable): None
# ======================================================

"""PostgreSQL persistence helpers for Engine assemblies."""

from __future__ import annotations

import datetime as _dt
import logging
from dataclasses import dataclass
from typing import Dict, List, Optional

from Data.Engine.db import dbapi as sqlite3
from Data.Engine.db import get_database_manager

from .models import AssemblyDomain, AssemblyRecord, CachedAssembly, PayloadDescriptor


_SCHEMA_STATEMENT_TEMPLATE = """
CREATE TABLE IF NOT EXISTS {qualified_table_name} (
    assembly_guid TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    summary TEXT,
    assembly_type TEXT NOT NULL,
    assembly_subtype TEXT,
    payload_json TEXT NOT NULL,
    source_repo TEXT,
    source_path TEXT,
    source_version TEXT,
    content_hash TEXT,
    payload_size_bytes BIGINT NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)
"""

_INDEX_STATEMENT_TEMPLATE = """
CREATE INDEX IF NOT EXISTS idx_{table_name}_assembly_type
    ON {qualified_table_name}(assembly_type)
"""

_OFFICIAL_CATALOG_STATE_TABLE = "official_catalog_state"

_OFFICIAL_CATALOG_STATE_SCHEMA_TEMPLATE = """
CREATE TABLE IF NOT EXISTS {qualified_table_name} (
    assembly_guid TEXT PRIMARY KEY,
    bundled_hash TEXT,
    remote_hash TEXT,
    catalog_hash TEXT,
    applied_hash TEXT,
    last_applied_source TEXT,
    repo_url TEXT,
    source_url TEXT,
    source_repo TEXT,
    source_path TEXT,
    source_version TEXT,
    last_catalog_sync_at TEXT,
    updated_at TEXT NOT NULL
)
"""

_ASSEMBLIES_COLUMNS: tuple[str, ...] = (
    "assembly_guid",
    "display_name",
    "summary",
    "assembly_type",
    "assembly_subtype",
    "payload_json",
    "source_repo",
    "source_path",
    "source_version",
    "content_hash",
    "payload_size_bytes",
    "created_at",
    "updated_at",
)

_ASSEMBLY_COLUMN_DEFINITIONS: Dict[str, str] = {
    "assembly_guid": "TEXT PRIMARY KEY",
    "display_name": "TEXT NOT NULL",
    "summary": "TEXT",
    "assembly_type": "TEXT NOT NULL",
    "assembly_subtype": "TEXT",
    "payload_json": "TEXT NOT NULL",
    "source_repo": "TEXT",
    "source_path": "TEXT",
    "source_version": "TEXT",
    "content_hash": "TEXT",
    "payload_size_bytes": "BIGINT NOT NULL DEFAULT 0",
    "created_at": "TEXT NOT NULL",
    "updated_at": "TEXT NOT NULL",
}

_OFFICIAL_CATALOG_STATE_COLUMNS: tuple[str, ...] = (
    "assembly_guid",
    "bundled_hash",
    "remote_hash",
    "catalog_hash",
    "applied_hash",
    "last_applied_source",
    "repo_url",
    "source_url",
    "source_repo",
    "source_path",
    "source_version",
    "last_catalog_sync_at",
    "updated_at",
)

_OFFICIAL_CATALOG_STATE_COLUMN_DEFINITIONS: Dict[str, str] = {
    "assembly_guid": "TEXT PRIMARY KEY",
    "bundled_hash": "TEXT",
    "remote_hash": "TEXT",
    "catalog_hash": "TEXT",
    "applied_hash": "TEXT",
    "last_applied_source": "TEXT",
    "repo_url": "TEXT",
    "source_url": "TEXT",
    "source_repo": "TEXT",
    "source_path": "TEXT",
    "source_version": "TEXT",
    "last_catalog_sync_at": "TEXT",
    "updated_at": "TEXT NOT NULL",
}


def _parse_datetime(value: str) -> _dt.datetime:
    try:
        return _dt.datetime.fromisoformat(value)
    except Exception:
        return _dt.datetime.utcnow()


def _table_name_for_domain(domain: AssemblyDomain) -> str:
    mapping = {
        AssemblyDomain.OFFICIAL: "official_assemblies",
        AssemblyDomain.COMMUNITY: "community_assemblies",
        AssemblyDomain.USER: "user_created_assemblies",
    }
    return mapping[domain]


@dataclass(slots=True)
class OfficialCatalogState:
    """Persisted metadata tracking which official catalog content has been applied."""

    assembly_guid: str
    bundled_hash: Optional[str] = None
    remote_hash: Optional[str] = None
    catalog_hash: Optional[str] = None
    applied_hash: Optional[str] = None
    last_applied_source: Optional[str] = None
    repo_url: Optional[str] = None
    source_url: Optional[str] = None
    source_repo: Optional[str] = None
    source_path: Optional[str] = None
    source_version: Optional[str] = None
    last_catalog_sync_at: Optional[_dt.datetime] = None
    updated_at: Optional[_dt.datetime] = None


class AssemblyDatabaseManager:
    """Coordinates PostgreSQL table access for assembly persistence."""

    def __init__(
        self,
        *,
        database_url: str,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._database_url = str(database_url or "").strip()
        self._logger = logger or logging.getLogger(__name__)
        self._manager = get_database_manager(self._database_url, logger=self._logger)

    def table_name_for_domain(self, domain: AssemblyDomain) -> str:
        return _table_name_for_domain(domain)

    def _qualified_table_name(self, domain: AssemblyDomain) -> str:
        table_name = _table_name_for_domain(domain)
        if str(self._manager.engine.url).startswith("postgresql+psycopg://"):
            return f"assemblies.{table_name}"
        return table_name

    def _qualified_official_catalog_state_table(self) -> str:
        if str(self._manager.engine.url).startswith("postgresql+psycopg://"):
            return f"assemblies.{_OFFICIAL_CATALOG_STATE_TABLE}"
        return _OFFICIAL_CATALOG_STATE_TABLE

    def initialise(self) -> None:
        """Ensure all assembly tables exist."""

        self._manager.ensure_schemas()
        bootstrap_conn = self._open_connection(AssemblyDomain.OFFICIAL)
        try:
            bootstrap_cur = bootstrap_conn.cursor()
            bootstrap_cur.execute(
                _OFFICIAL_CATALOG_STATE_SCHEMA_TEMPLATE.format(
                    qualified_table_name=self._qualified_official_catalog_state_table(),
                )
            )
            self._ensure_columns(
                bootstrap_cur,
                self._qualified_official_catalog_state_table(),
                _OFFICIAL_CATALOG_STATE_COLUMN_DEFINITIONS,
            )
            bootstrap_conn.commit()
        finally:
            bootstrap_conn.close()
        for domain in AssemblyDomain:
            conn = self._open_connection(domain)
            try:
                self._apply_schema(conn, domain)
                conn.commit()
            finally:
                conn.close()

    def reset_domain(self, domain: AssemblyDomain) -> None:
        """Remove all assemblies for the specified domain."""

        conn = self._open_connection(domain)
        try:
            cur = conn.cursor()
            cur.execute(f"DELETE FROM {self._qualified_table_name(domain)}")
            conn.commit()
        finally:
            conn.close()

    def load_all(self, domain: AssemblyDomain) -> List[AssemblyRecord]:
        """Load all assembly records for the given domain."""

        table_name = _table_name_for_domain(domain)
        qualified_table_name = self._qualified_table_name(domain)
        conn = self._open_connection(domain)
        conn.row_factory = sqlite3.Row
        try:
            cur = conn.cursor()
            cur.execute(
                f"""
                SELECT
                    assembly_guid,
                    display_name,
                    summary,
                    assembly_type,
                    assembly_subtype,
                    payload_json,
                    source_repo,
                    source_path,
                    source_version,
                    content_hash,
                    payload_size_bytes,
                    created_at,
                    updated_at
                FROM {qualified_table_name}
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
                records.append(
                    AssemblyRecord(
                        assembly_guid=row["assembly_guid"],
                        display_name=row["display_name"],
                        summary=row["summary"],
                        assembly_type=assembly_type,
                        assembly_subtype=assembly_subtype,
                        payload=payload,
                        payload_json=payload_json,
                        source_repo=row["source_repo"],
                        source_path=row["source_path"],
                        source_version=row["source_version"],
                        content_hash=row["content_hash"],
                        created_at=row_created,
                        updated_at=row_updated,
                    )
                )
            return records
        finally:
            conn.close()

    def load_official_catalog_state(self) -> Dict[str, OfficialCatalogState]:
        """Return persisted official-catalog sync metadata keyed by assembly GUID."""

        conn = self._open_connection(AssemblyDomain.OFFICIAL)
        conn.row_factory = sqlite3.Row
        try:
            cur = conn.cursor()
            cur.execute(
                f"""
                SELECT
                    assembly_guid,
                    bundled_hash,
                    remote_hash,
                    catalog_hash,
                    applied_hash,
                    last_applied_source,
                    repo_url,
                    source_url,
                    source_repo,
                    source_path,
                    source_version,
                    last_catalog_sync_at,
                    updated_at
                FROM {self._qualified_official_catalog_state_table()}
                """
            )
            state: Dict[str, OfficialCatalogState] = {}
            for row in cur.fetchall():
                guid = str(row["assembly_guid"] or "").strip().lower()
                if not guid:
                    continue
                state[guid] = OfficialCatalogState(
                    assembly_guid=guid,
                    bundled_hash=row["bundled_hash"],
                    remote_hash=row["remote_hash"],
                    catalog_hash=row["catalog_hash"],
                    applied_hash=row["applied_hash"],
                    last_applied_source=row["last_applied_source"],
                    repo_url=row["repo_url"],
                    source_url=row["source_url"],
                    source_repo=row["source_repo"],
                    source_path=row["source_path"],
                    source_version=row["source_version"],
                    last_catalog_sync_at=_parse_datetime(row["last_catalog_sync_at"]) if row["last_catalog_sync_at"] else None,
                    updated_at=_parse_datetime(row["updated_at"]) if row["updated_at"] else None,
                )
            return state
        finally:
            conn.close()

    def upsert_official_catalog_state(
        self,
        assembly_guid: str,
        *,
        bundled_hash: Optional[str] = None,
        remote_hash: Optional[str] = None,
        catalog_hash: Optional[str] = None,
        applied_hash: Optional[str] = None,
        last_applied_source: Optional[str] = None,
        repo_url: Optional[str] = None,
        source_url: Optional[str] = None,
        source_repo: Optional[str] = None,
        source_path: Optional[str] = None,
        source_version: Optional[str] = None,
        last_catalog_sync_at: Optional[str] = None,
    ) -> None:
        """Persist the latest bundled/remote hash metadata for an official assembly."""

        guid = str(assembly_guid or "").strip().lower()
        if not guid:
            raise ValueError("assembly_guid required for official catalog state")

        existing = self.load_official_catalog_state().get(guid)
        now = _dt.datetime.utcnow().replace(microsecond=0).isoformat()
        conn = self._open_connection(AssemblyDomain.OFFICIAL)
        try:
            cur = conn.cursor()
            cur.execute(
                f"""
                INSERT INTO {self._qualified_official_catalog_state_table()} (
                    assembly_guid,
                    bundled_hash,
                    remote_hash,
                    catalog_hash,
                    applied_hash,
                    last_applied_source,
                    repo_url,
                    source_url,
                    source_repo,
                    source_path,
                    source_version,
                    last_catalog_sync_at,
                    updated_at
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(assembly_guid) DO UPDATE SET
                    bundled_hash = EXCLUDED.bundled_hash,
                    remote_hash = EXCLUDED.remote_hash,
                    catalog_hash = EXCLUDED.catalog_hash,
                    applied_hash = EXCLUDED.applied_hash,
                    last_applied_source = EXCLUDED.last_applied_source,
                    repo_url = EXCLUDED.repo_url,
                    source_url = EXCLUDED.source_url,
                    source_repo = EXCLUDED.source_repo,
                    source_path = EXCLUDED.source_path,
                    source_version = EXCLUDED.source_version,
                    last_catalog_sync_at = EXCLUDED.last_catalog_sync_at,
                    updated_at = EXCLUDED.updated_at
                """,
                (
                    guid,
                    bundled_hash if bundled_hash is not None else (existing.bundled_hash if existing else None),
                    remote_hash if remote_hash is not None else (existing.remote_hash if existing else None),
                    catalog_hash if catalog_hash is not None else (existing.catalog_hash if existing else None),
                    applied_hash if applied_hash is not None else (existing.applied_hash if existing else None),
                    last_applied_source
                    if last_applied_source is not None
                    else (existing.last_applied_source if existing else None),
                    repo_url if repo_url is not None else (existing.repo_url if existing else None),
                    source_url if source_url is not None else (existing.source_url if existing else None),
                    source_repo if source_repo is not None else (existing.source_repo if existing else None),
                    source_path if source_path is not None else (existing.source_path if existing else None),
                    source_version
                    if source_version is not None
                    else (existing.source_version if existing else None),
                    last_catalog_sync_at
                    if last_catalog_sync_at is not None
                    else (
                        existing.last_catalog_sync_at.isoformat()
                        if existing and existing.last_catalog_sync_at
                        else None
                    ),
                    now,
                ),
            )
            conn.commit()
        finally:
            conn.close()

    def upsert_record(self, domain: AssemblyDomain, entry: CachedAssembly) -> None:
        """Insert or update an assembly record."""

        table_name = _table_name_for_domain(domain)
        qualified_table_name = self._qualified_table_name(domain)
        record = entry.record
        conn = self._open_connection(domain)
        try:
            cur = conn.cursor()
            payload_size = int(record.payload.size_bytes or 0)
            if payload_size <= 0:
                payload_size = len((record.payload_json or "{}").encode("utf-8"))
            cur.execute(
                f"""
                INSERT INTO {qualified_table_name} (
                    assembly_guid,
                    display_name,
                    summary,
                    assembly_type,
                    assembly_subtype,
                    payload_json,
                    source_repo,
                    source_path,
                    source_version,
                    content_hash,
                    payload_size_bytes,
                    created_at,
                    updated_at
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(assembly_guid) DO UPDATE SET
                    display_name = EXCLUDED.display_name,
                    summary = EXCLUDED.summary,
                    assembly_type = EXCLUDED.assembly_type,
                    assembly_subtype = EXCLUDED.assembly_subtype,
                    payload_json = EXCLUDED.payload_json,
                    source_repo = EXCLUDED.source_repo,
                    source_path = EXCLUDED.source_path,
                    source_version = EXCLUDED.source_version,
                    content_hash = EXCLUDED.content_hash,
                    payload_size_bytes = EXCLUDED.payload_size_bytes,
                    updated_at = EXCLUDED.updated_at
                """,
                (
                    record.assembly_guid,
                    record.display_name,
                    record.summary,
                    record.assembly_type,
                    record.assembly_subtype,
                    record.payload_json,
                    record.source_repo,
                    record.source_path,
                    record.source_version,
                    record.content_hash,
                    payload_size,
                    record.created_at.isoformat(),
                    record.updated_at.isoformat(),
                ),
            )
            conn.commit()
        finally:
            conn.close()

    def delete_record(self, domain: AssemblyDomain, entry: CachedAssembly) -> None:
        """Delete an assembly record."""

        qualified_table_name = self._qualified_table_name(domain)
        conn = self._open_connection(domain)
        try:
            cur = conn.cursor()
            cur.execute(
                f"DELETE FROM {qualified_table_name} WHERE assembly_guid = ?",
                (entry.record.assembly_guid,),
            )
            conn.commit()
        finally:
            conn.close()

    def _open_connection(self, _domain: AssemblyDomain) -> sqlite3.Connection:
        conn = sqlite3.connect(self._database_url, timeout=30)
        conn.row_factory = sqlite3.Row
        return conn

    def _apply_schema(self, conn: sqlite3.Connection, domain: AssemblyDomain) -> None:
        table_name = _table_name_for_domain(domain)
        qualified_table_name = self._qualified_table_name(domain)
        cur = conn.cursor()
        cur.execute(
            _SCHEMA_STATEMENT_TEMPLATE.format(
                table_name=table_name,
                qualified_table_name=qualified_table_name,
            )
        )
        self._ensure_columns(cur, qualified_table_name, _ASSEMBLY_COLUMN_DEFINITIONS)
        cur.execute(
            _INDEX_STATEMENT_TEMPLATE.format(
                table_name=table_name,
                qualified_table_name=qualified_table_name,
            )
        )
        self._validate_schema(cur, domain)
        conn.commit()

    def _ensure_columns(
        self,
        cur: sqlite3.Cursor,
        table_name: str,
        column_definitions: Dict[str, str],
    ) -> None:
        cur.execute(f"PRAGMA table_info({table_name})")
        existing_columns = {row[1] for row in cur.fetchall()}
        for column_name, definition in column_definitions.items():
            if column_name in existing_columns:
                continue
            cur.execute(f"ALTER TABLE {table_name} ADD COLUMN {column_name} {definition}")

    def _validate_schema(self, cur: sqlite3.Cursor, domain: AssemblyDomain) -> None:
        cur.execute(f"PRAGMA table_info({self._qualified_table_name(domain)})")
        columns = [row[1] for row in cur.fetchall()]
        if not columns:
            raise RuntimeError("Assemblies schema validation failed: missing assembly table.")
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
            raise RuntimeError(f"Assemblies schema validation failed ({'; '.join(problems)}).")
