# ======================================================
# Data\Engine\Unit_Tests\test_core_api.py
# Description: Validates the Engine /health endpoint wiring.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

from Data.Engine import database


def test_health_endpoint(engine_harness):
    client = engine_harness.app.test_client()
    response = client.get("/health")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload == {"status": "ok"}


def test_database_initialisation_creates_site_schema_before_migrations(
    monkeypatch,
    tmp_path,
):
    observed = {}
    original_apply_all = database.database_migrations.apply_all

    def _record_site_tables(conn):
        cur = conn.cursor()
        try:
            cur.execute(
                "SELECT name FROM sqlite_master WHERE type='table' AND name='sites'"
            )
            observed["sites"] = cur.fetchone() is not None
            cur.execute(
                "SELECT name FROM sqlite_master WHERE type='table' AND name='device_sites'"
            )
            observed["device_sites"] = cur.fetchone() is not None
        finally:
            cur.close()
        original_apply_all(conn)

    monkeypatch.setattr(database.database_migrations, "apply_all", _record_site_tables)

    database.initialise_engine_database(str(tmp_path / "engine.db"))

    assert observed == {"sites": True, "device_sites": True}
