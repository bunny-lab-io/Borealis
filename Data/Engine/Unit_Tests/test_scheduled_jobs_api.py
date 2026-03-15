# ======================================================
# Data\Engine\Unit_Tests\test_scheduled_jobs_api.py
# Description: Covers scheduled job API behavior that differs between
#              SQLite and PostgreSQL-backed DB-API adapters.
# ======================================================

from __future__ import annotations

from Data.Engine.db import dbapi as sqlite3
from Data.Engine.server import create_app

from .conftest import EngineTestHarness


def _scheduled_jobs_client(engine_harness: EngineTestHarness):
    config = {
        "DATABASE_URL": f"sqlite:///{engine_harness.db_path.as_posix()}",
        "TLS_CERT_PATH": engine_harness.app.config["TLS_CERT_PATH"],
        "TLS_KEY_PATH": engine_harness.app.config["TLS_KEY_PATH"],
        "TLS_BUNDLE_PATH": engine_harness.app.config["TLS_BUNDLE_PATH"],
        "SECRET_KEY": engine_harness.app.config["SECRET_KEY"],
        "LOG_FILE": engine_harness.app.config["LOG_FILE"],
        "ERROR_LOG_FILE": engine_harness.app.config["ERROR_LOG_FILE"],
        "STATIC_FOLDER": engine_harness.app.config["STATIC_FOLDER"],
        "API_GROUPS": ("core", "auth", "tokens", "enrollment", "devices", "assemblies", "scheduled_jobs"),
    }
    app, _socketio, context = create_app(config)
    app.config.update(TESTING=True)
    client = app.test_client()
    with client.session_transaction() as sess:
        sess["username"] = "admin"
        sess["role"] = "Admin"
    return client, context.scheduler


class _MissingLastRowIdCursor:
    def __init__(self, inner) -> None:
        self._inner = inner
        self.lastrowid = None
        self.rowcount = getattr(inner, "rowcount", 0)

    def execute(self, sql, params=()):
        result = self._inner.execute(sql, params)
        normalized = " ".join(str(sql or "").strip().upper().split())
        if normalized.startswith("INSERT INTO SCHEDULED_JOBS"):
            self.lastrowid = None
        else:
            self.lastrowid = getattr(self._inner, "lastrowid", None)
        self.rowcount = getattr(self._inner, "rowcount", 0)
        return result

    def fetchone(self):
        return self._inner.fetchone()

    def fetchall(self):
        return self._inner.fetchall()

    def __getattr__(self, name):
        return getattr(self._inner, name)


class _MissingLastRowIdConnection:
    def __init__(self, inner) -> None:
        self._inner = inner

    def cursor(self):
        return _MissingLastRowIdCursor(self._inner.cursor())

    def __getattr__(self, name):
        return getattr(self._inner, name)


def test_scheduled_job_create_recovers_when_lastrowid_is_missing(
    engine_harness: EngineTestHarness,
    monkeypatch,
) -> None:
    client, scheduler = _scheduled_jobs_client(engine_harness)

    def _conn_with_missing_lastrowid():
        return _MissingLastRowIdConnection(sqlite3.connect(str(engine_harness.db_path)))

    monkeypatch.setattr(scheduler, "_conn", _conn_with_missing_lastrowid)

    response = client.post(
        "/api/scheduled_jobs",
        json={
            "name": "Nightly Inventory",
            "components": [{"kind": "script", "script": "Write-Host 'hello'"}],
            "targets": ["test-device"],
            "schedule": {"type": "immediately"},
            "execution_context": "system",
            "enabled": True,
        },
    )

    assert response.status_code == 200
    payload = response.get_json()
    assert payload["job"]["id"] is not None
    assert payload["job"]["name"] == "Nightly Inventory"
    assert payload["job"]["targets"] == ["test-device"]
