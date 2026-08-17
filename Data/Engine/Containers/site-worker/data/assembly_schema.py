# ======================================================
# Data\Engine\assembly_schema.py
# Description: Minimal assembly-table bootstrap used by Engine schema Job.
#
# API Endpoints (if applicable): None
# ======================================================

"""Create Assembly tables before Go API startup."""

from __future__ import annotations

import logging
from typing import Optional

from Data.Engine.db import dbapi as sqlite3
from Data.Engine.db import get_database_manager


ASSEMBLY_COLUMNS: tuple[tuple[str, str], ...] = (
    ("assembly_guid", "TEXT PRIMARY KEY"),
    ("display_name", "TEXT NOT NULL"),
    ("summary", "TEXT"),
    ("assembly_type", "TEXT NOT NULL"),
    ("assembly_subtype", "TEXT"),
    ("payload_json", "TEXT NOT NULL"),
    ("source_repo", "TEXT"),
    ("source_path", "TEXT"),
    ("source_version", "TEXT"),
    ("content_hash", "TEXT"),
    ("payload_size_bytes", "BIGINT NOT NULL DEFAULT 0"),
    ("created_at", "TEXT NOT NULL"),
    ("updated_at", "TEXT NOT NULL"),
)

OFFICIAL_CATALOG_COLUMNS: tuple[tuple[str, str], ...] = (
    ("assembly_guid", "TEXT PRIMARY KEY"),
    ("bundled_hash", "TEXT"),
    ("remote_hash", "TEXT"),
    ("catalog_hash", "TEXT"),
    ("applied_hash", "TEXT"),
    ("last_applied_source", "TEXT"),
    ("repo_url", "TEXT"),
    ("source_url", "TEXT"),
    ("source_repo", "TEXT"),
    ("source_path", "TEXT"),
    ("source_version", "TEXT"),
    ("last_catalog_sync_at", "TEXT"),
    ("updated_at", "TEXT NOT NULL"),
)


def ensure_assembly_tables(database_url: str, *, logger: Optional[logging.Logger] = None) -> None:
    """Ensure Go-owned Assembly tables exist before API pod starts."""

    manager = get_database_manager(database_url, logger=logger)
    manager.ensure_schemas()
    postgres = str(manager.engine.url).startswith("postgresql+psycopg://")
    _ensure_table(
        database_url,
        "assemblies.official_catalog_state" if postgres else "official_catalog_state",
        OFFICIAL_CATALOG_COLUMNS,
    )
    for table_name in ("official_assemblies", "community_assemblies", "user_created_assemblies"):
        qualified_name = f"assemblies.{table_name}" if postgres else table_name
        _ensure_table(
            database_url,
            qualified_name,
            ASSEMBLY_COLUMNS,
            index_name=f"idx_{table_name}_assembly_type",
        )


def _ensure_table(
    database_url: str,
    qualified_name: str,
    columns: tuple[tuple[str, str], ...],
    *,
    index_name: str = "",
) -> None:
    conn = sqlite3.connect(database_url, timeout=30)
    try:
        cur = conn.cursor()
        column_sql = ",\n    ".join(f"{name} {definition}" for name, definition in columns)
        cur.execute(f"CREATE TABLE IF NOT EXISTS {qualified_name} (\n    {column_sql}\n)")
        cur.execute(f"PRAGMA table_info({qualified_name})")
        existing = {str(row[1]) for row in cur.fetchall()}
        for name, definition in columns:
            if name not in existing:
                cur.execute(f"ALTER TABLE {qualified_name} ADD COLUMN {name} {definition}")
        if index_name:
            cur.execute(f"CREATE INDEX IF NOT EXISTS {index_name} ON {qualified_name}(assembly_type)")
        cur.execute(f"PRAGMA table_info({qualified_name})")
        actual = {str(row[1]) for row in cur.fetchall()}
        expected = {name for name, _definition in columns}
        if actual != expected:
            raise RuntimeError(
                f"Assemblies schema validation failed for {qualified_name}: expected {expected}, got {actual}."
            )
        conn.commit()
    finally:
        conn.close()
