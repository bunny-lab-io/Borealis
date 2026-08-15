# ======================================================
# Data\Engine\Unit_Tests\test_database_schema.py
# Description: Validates Engine database schema bootstrap coverage.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import os
import tempfile

os.environ.setdefault(
    "BOREALIS_ENGINE_CERT_ROOT",
    tempfile.mkdtemp(prefix="borealis-schema-test-certs-"),
)

from Data.Engine import database
from Data.Engine.db import dbapi


def _table_columns(db_url: str, table_name: str) -> set[str]:
    conn = dbapi.connect(db_url)
    try:
        cur = conn.cursor()
        cur.execute(f"PRAGMA table_info({table_name})")
        return {str(row[1]) for row in cur.fetchall()}
    finally:
        conn.close()


def test_engine_database_initialisation_creates_vpn_key_lease_table(tmp_path) -> None:
    db_url = f"sqlite:///{(tmp_path / 'engine.sqlite3').as_posix()}"
    progress: list[str] = []

    database.initialise_engine_database(db_url, progress_callback=progress.append)

    columns = _table_columns(db_url, "device_vpn_key_leases")

    assert "device_vpn_key_leases" in progress
    assert columns == {
        "agent_id",
        "client_private_key",
        "client_public_key",
        "updated_at",
    }


def test_engine_database_migrations_repair_partial_vpn_key_lease_table(tmp_path) -> None:
    db_url = f"sqlite:///{(tmp_path / 'engine.sqlite3').as_posix()}"
    conn = dbapi.connect(db_url)
    try:
        cur = conn.cursor()
        cur.execute("CREATE TABLE device_vpn_key_leases (agent_id TEXT PRIMARY KEY)")
        conn.commit()
    finally:
        conn.close()

    database.initialise_engine_database(db_url)

    columns = _table_columns(db_url, "device_vpn_key_leases")

    assert columns == {
        "agent_id",
        "client_private_key",
        "client_public_key",
        "updated_at",
    }


def test_engine_database_migrations_remove_retired_agent_source_columns(tmp_path) -> None:
    db_url = f"sqlite:///{(tmp_path / 'engine.sqlite3').as_posix()}"

    database.initialise_engine_database(db_url)
    conn = dbapi.connect(db_url)
    try:
        cur = conn.cursor()
        for column in (
            "agent_release_channel_override",
            "agent_release_channel",
            "agent_branch",
            "agent_update_channel",
        ):
            cur.execute(f"ALTER TABLE devices ADD COLUMN {column} TEXT")
        cur.execute(
            """
            INSERT INTO devices(guid, hostname, agent_release_channel, agent_branch)
            VALUES (?, ?, ?, ?)
            """,
            (
                "2540DA38-E2B1-45B9-9113-BF7CF0E1778A",
                "legacy-agent",
                "unstable",
                "feature/legacy-agent-source",
            ),
        )
        conn.commit()
    finally:
        conn.close()

    database.initialise_engine_database(db_url)

    columns = _table_columns(db_url, "devices")
    assert "agent_release_channel_override" not in columns
    assert "agent_release_channel" not in columns
    assert "agent_branch" not in columns
    assert "agent_update_channel" not in columns

    conn = dbapi.connect(db_url)
    try:
        cur = conn.cursor()
        cur.execute("SELECT hostname FROM devices WHERE guid = ?", ("2540DA38-E2B1-45B9-9113-BF7CF0E1778A",))
        row = cur.fetchone()
    finally:
        conn.close()

    assert row == ("legacy-agent",)


def test_engine_database_initialisation_keeps_legacy_patch_policy_restore_columns(tmp_path) -> None:
    db_url = f"sqlite:///{(tmp_path / 'engine.sqlite3').as_posix()}"
    progress: list[str] = []

    database.initialise_engine_database(db_url, progress_callback=progress.append)

    columns = _table_columns(db_url, "patch_policies")

    assert "patch_policies" in progress
    assert "class_toggles_json" in columns
    assert "reboot_policy_json" in columns


def test_engine_database_migrations_repair_partial_patch_policy_restore_columns(tmp_path) -> None:
    db_url = f"sqlite:///{(tmp_path / 'engine.sqlite3').as_posix()}"
    conn = dbapi.connect(db_url)
    try:
        cur = conn.cursor()
        cur.execute(
            """
            CREATE TABLE patch_policies (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                created_at INTEGER NOT NULL,
                updated_at INTEGER NOT NULL
            )
            """
        )
        conn.commit()
    finally:
        conn.close()

    database.initialise_engine_database(db_url)

    columns = _table_columns(db_url, "patch_policies")

    assert "class_toggles_json" in columns
    assert "reboot_policy_json" in columns


def test_engine_database_accepts_legacy_patch_policy_backup_row_shape(tmp_path) -> None:
    db_url = f"sqlite:///{(tmp_path / 'engine.sqlite3').as_posix()}"

    database.initialise_engine_database(db_url)

    conn = dbapi.connect(db_url)
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO patch_policies(
                name,
                description,
                enabled,
                class_toggles_json,
                reboot_policy_json,
                created_at,
                updated_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "Legacy Backup Policy",
                "Legacy backup row shape.",
                1,
                '{"security":true}',
                '{"mode":"maintenance_window"}',
                1783000000,
                1783000000,
            ),
        )
        cur.execute(
            "SELECT policy_type, locked, role_scope FROM patch_policies WHERE name = ?",
            ("Legacy Backup Policy",),
        )
        row = cur.fetchone()
    finally:
        conn.close()

    assert row == ("site", 0, "Both")
