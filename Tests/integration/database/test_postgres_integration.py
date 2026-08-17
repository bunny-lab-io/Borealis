#!/usr/bin/env python3
"""Production PostgreSQL 17 integration contract for retained Engine database layer."""

from __future__ import annotations

import os

from Data.Engine import database
from Data.Engine.db import dbapi
from Data.Engine.db import get_database_manager


DATABASE_URL = os.environ["BOREALIS_TEST_DATABASE_URL"]


def rows(sql: str, params=()):
    connection = dbapi.connect(DATABASE_URL)
    try:
        cursor = connection.cursor()
        cursor.execute(sql, params)
        return cursor.fetchall()
    finally:
        connection.close()


def scalar(sql: str, params=()):
    result = rows(sql, params)
    return result[0][0] if result else None


def assert_schema_contract() -> None:
    schemas = {item[0] for item in rows("SELECT schema_name FROM information_schema.schemata")}
    assert {"engine", "assemblies"}.issubset(schemas), schemas

    required_tables = {
        "activity_history",
        "devices",
        "sites",
        "users",
        "patch_policies",
        "scheduled_jobs",
        "job_scheduler_work_items",
        "workflow_runs",
    }
    actual_tables = {
        item[0]
        for item in rows(
            "SELECT table_name FROM information_schema.tables WHERE table_schema = ?",
            ("engine",),
        )
    }
    assert required_tables.issubset(actual_tables), required_tables - actual_tables

    assembly_tables = {
        item[0]
        for item in rows(
            "SELECT table_name FROM information_schema.tables WHERE table_schema = ?",
            ("assemblies",),
        )
    }
    assert {
        "official_catalog_state",
        "official_assemblies",
        "community_assemblies",
        "user_created_assemblies",
    }.issubset(assembly_tables)

    indexes = {item[0] for item in rows("SELECT indexname FROM pg_indexes WHERE schemaname = ?", ("engine",))}
    assert "uq_devices_hostname" in indexes
    assert "idx_job_scheduler_work_claim" in indexes


def assert_translation_and_transactions() -> None:
    connection = dbapi.connect(DATABASE_URL)
    try:
        cursor = connection.cursor()
        cursor.execute("CREATE TABLE IF NOT EXISTS ci_translation (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE)")
        cursor.execute("DELETE FROM ci_translation")
        cursor.execute("INSERT INTO ci_translation(name) VALUES (?)", ("alpha",))
        cursor.execute("INSERT OR IGNORE INTO ci_translation(name) VALUES (?)", ("alpha",))
        connection.commit()
        cursor.execute("SELECT COUNT(*) FROM ci_translation WHERE name = ?", ("alpha",))
        assert cursor.fetchone()[0] == 1
        cursor.execute("PRAGMA table_info(ci_translation)")
        columns = {row[1] for row in cursor.fetchall()}
        assert columns == {"id", "name"}
        cursor.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", ("ci_translation",))
        assert cursor.fetchone() == (1,)
        cursor.execute("INSERT INTO ci_translation(name) VALUES (?)", ("rollback",))
        connection.rollback()
        cursor.execute("SELECT COUNT(*) FROM ci_translation WHERE name = ?", ("rollback",))
        assert cursor.fetchone()[0] == 0
    finally:
        connection.close()


def assert_error_mapping() -> None:
    connection = dbapi.connect(DATABASE_URL)
    try:
        cursor = connection.cursor()
        cursor.execute("CREATE TABLE IF NOT EXISTS ci_parent (id INTEGER PRIMARY KEY)")
        cursor.execute("CREATE TABLE IF NOT EXISTS ci_child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES ci_parent(id))")
        cursor.execute("DELETE FROM ci_child")
        cursor.execute("DELETE FROM ci_parent")
        cursor.execute("INSERT INTO ci_parent(id) VALUES (?)", (1,))
        connection.commit()
        try:
            cursor.execute("INSERT INTO ci_parent(id) VALUES (?)", (1,))
        except dbapi.IntegrityError:
            connection.rollback()
        else:
            raise AssertionError("uniqueness violation did not map to dbapi.IntegrityError")
        try:
            cursor.execute("INSERT INTO ci_child(id, parent_id) VALUES (?, ?)", (1, 999))
        except dbapi.IntegrityError:
            connection.rollback()
        else:
            raise AssertionError("foreign-key violation did not map to dbapi.IntegrityError")
    finally:
        connection.close()


def assert_partial_and_legacy_repair() -> None:
    database.initialise_engine_database(DATABASE_URL)
    connection = dbapi.connect(DATABASE_URL)
    try:
        cursor = connection.cursor()
        for column in (
            "agent_release_channel_override",
            "agent_release_channel",
            "agent_branch",
            "agent_update_channel",
        ):
            cursor.execute(f"ALTER TABLE devices ADD COLUMN {column} TEXT")
        cursor.execute(
            "INSERT INTO devices(guid, hostname, agent_release_channel, agent_branch) VALUES (?, ?, ?, ?)",
            ("2540DA38-E2B1-45B9-9113-BF7CF0E1778A", "legacy-agent", "unstable", "feature/legacy"),
        )
        cursor.execute("ALTER TABLE patch_policies DROP COLUMN class_toggles_json")
        cursor.execute("ALTER TABLE patch_policies DROP COLUMN reboot_policy_json")
        connection.commit()
    finally:
        connection.close()

    database.initialise_engine_database(DATABASE_URL)
    device_columns = {item[1] for item in rows("PRAGMA table_info(devices)")}
    assert not {
        "agent_release_channel_override",
        "agent_release_channel",
        "agent_branch",
        "agent_update_channel",
    } & device_columns
    assert scalar("SELECT hostname FROM devices WHERE guid = ?", ("2540DA38-E2B1-45B9-9113-BF7CF0E1778A",)) == "legacy-agent"
    patch_columns = {item[1] for item in rows("PRAGMA table_info(patch_policies)")}
    assert {"class_toggles_json", "reboot_policy_json"}.issubset(patch_columns)


def assert_manager_session_contract() -> None:
    manager = get_database_manager(DATABASE_URL, sslmode="disable")
    connection = manager.raw_connection()
    try:
        cursor = connection.cursor()
        cursor.execute("SHOW search_path")
        search_path = cursor.fetchone()[0]
        assert "engine" in search_path and "assemblies" in search_path
        cursor.execute("SHOW TIME ZONE")
        assert cursor.fetchone()[0].upper() == "UTC"
    finally:
        connection.close()


def main() -> int:
    progress: list[str] = []
    database.initialise_engine_database(DATABASE_URL, progress_callback=progress.append)
    assert "users" in progress and "assemblies.official_assemblies" in progress
    assert_schema_contract()
    database.initialise_engine_database(DATABASE_URL)
    assert_schema_contract()
    assert_translation_and_transactions()
    assert_error_mapping()
    assert_partial_and_legacy_repair()
    assert_manager_session_contract()
    print("POSTGRES INTEGRATION PASS: fresh/idempotent init, repair, translation, transactions, constraints, search_path, and UTC verified")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
