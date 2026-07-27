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


def test_engine_database_initialisation_creates_vpn_key_lease_table(tmp_path) -> None:
    db_url = f"sqlite:///{(tmp_path / 'engine.sqlite3').as_posix()}"
    progress: list[str] = []

    database.initialise_engine_database(db_url, progress_callback=progress.append)

    conn = dbapi.connect(db_url)
    try:
        cur = conn.cursor()
        cur.execute("PRAGMA table_info(device_vpn_key_leases)")
        columns = {str(row[1]) for row in cur.fetchall()}
    finally:
        conn.close()

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

    conn = dbapi.connect(db_url)
    try:
        cur = conn.cursor()
        cur.execute("PRAGMA table_info(device_vpn_key_leases)")
        columns = {str(row[1]) for row in cur.fetchall()}
    finally:
        conn.close()

    assert columns == {
        "agent_id",
        "client_private_key",
        "client_public_key",
        "updated_at",
    }
