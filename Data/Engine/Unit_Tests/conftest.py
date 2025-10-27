from __future__ import annotations

import sqlite3
from dataclasses import dataclass
from pathlib import Path
from typing import Iterator

import pytest
from flask import Flask

from Data.Engine.server import create_app


_SCHEMA_DEFINITION = """
CREATE TABLE IF NOT EXISTS devices (
    guid TEXT PRIMARY KEY,
    hostname TEXT,
    created_at INTEGER,
    last_seen INTEGER,
    ssl_key_fingerprint TEXT,
    token_version INTEGER,
    status TEXT,
    key_added_at TEXT
);
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id TEXT PRIMARY KEY,
    guid TEXT,
    token_hash TEXT,
    dpop_jkt TEXT,
    created_at TEXT,
    expires_at TEXT,
    revoked_at TEXT,
    last_used_at TEXT
);
CREATE TABLE IF NOT EXISTS enrollment_install_codes (
    id TEXT PRIMARY KEY,
    code TEXT UNIQUE,
    expires_at TEXT,
    used_at TEXT,
    used_by_guid TEXT,
    max_uses INTEGER,
    use_count INTEGER,
    last_used_at TEXT
);
CREATE TABLE IF NOT EXISTS device_approvals (
    id TEXT PRIMARY KEY,
    approval_reference TEXT UNIQUE,
    guid TEXT,
    hostname_claimed TEXT,
    ssl_key_fingerprint_claimed TEXT,
    enrollment_code_id TEXT,
    status TEXT,
    client_nonce TEXT,
    server_nonce TEXT,
    agent_pubkey_der BLOB,
    created_at TEXT,
    updated_at TEXT,
    approved_by_user_id TEXT
);
CREATE TABLE IF NOT EXISTS device_keys (
    id TEXT PRIMARY KEY,
    guid TEXT,
    ssl_key_fingerprint TEXT,
    added_at TEXT,
    retired_at TEXT
);
"""


@dataclass
class EngineTestHarness:
    app: Flask
    db_path: Path
    bundle_contents: str


def _initialise_legacy_schema(db_path: Path) -> None:
    db_path.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(str(db_path))
    try:
        conn.executescript(_SCHEMA_DEFINITION)
        conn.commit()
    finally:
        conn.close()


@pytest.fixture()
def engine_harness(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Iterator[EngineTestHarness]:
    project_root = Path(__file__).resolve().parents[3]
    monkeypatch.setenv("BOREALIS_PROJECT_ROOT", str(project_root))

    runtime_dir = tmp_path / "runtime"
    runtime_dir.mkdir()
    cert_root = tmp_path / "certificates"
    cert_root.mkdir()

    monkeypatch.setenv("BOREALIS_SERVER_ROOT", str(runtime_dir))
    monkeypatch.setenv("BOREALIS_CERT_ROOT", str(cert_root))
    monkeypatch.setenv("BOREALIS_SERVER_CERT_ROOT", str(cert_root / "Server"))
    monkeypatch.setenv("BOREALIS_AGENT_CERT_ROOT", str(cert_root / "Agent"))

    db_path = tmp_path / "database" / "engine.sqlite3"
    _initialise_legacy_schema(db_path)

    tls_dir = tmp_path / "tls"
    tls_dir.mkdir()
    bundle_contents = "-----BEGIN CERTIFICATE-----\nengine-test\n-----END CERTIFICATE-----\n"
    cert_path = tls_dir / "server-cert.pem"
    key_path = tls_dir / "server-key.pem"
    bundle_path = tls_dir / "server-bundle.pem"
    cert_path.write_text(bundle_contents, encoding="utf-8")
    key_path.write_text("test-key", encoding="utf-8")
    bundle_path.write_text(bundle_contents, encoding="utf-8")

    logs_dir = tmp_path / "logs"
    logs_dir.mkdir(parents=True, exist_ok=True)
    log_path = logs_dir / "server.log"
    error_log_path = logs_dir / "error.log"

    static_dir = tmp_path / "static"
    static_dir.mkdir(parents=True, exist_ok=True)
    (static_dir / "index.html").write_text("<html><body>Engine Test UI</body></html>", encoding="utf-8")
    assets_dir = static_dir / "assets"
    assets_dir.mkdir(parents=True, exist_ok=True)
    (assets_dir / "example.txt").write_text("asset", encoding="utf-8")

    config = {
        "DATABASE_PATH": str(db_path),
        "TLS_CERT_PATH": str(cert_path),
        "TLS_KEY_PATH": str(key_path),
        "TLS_BUNDLE_PATH": str(bundle_path),
        "LOG_FILE": str(log_path),
        "ERROR_LOG_FILE": str(error_log_path),
        "STATIC_FOLDER": str(static_dir),
        "API_GROUPS": ("core", "tokens", "enrollment"),
    }

    app, _socketio, _context = create_app(config)
    app.config.update(TESTING=True)

    yield EngineTestHarness(app=app, db_path=db_path, bundle_contents=bundle_contents)
