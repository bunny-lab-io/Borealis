# ======================================================
# Data\Engine\Unit_Tests\conftest.py
# Description: Pytest fixtures that seed the Engine app, SQLite schema, TLS materials, and WebUI assets.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import json
import sqlite3
from dataclasses import dataclass
from pathlib import Path
from typing import Iterator

import pytest
from flask import Flask

import sys
import types

import importlib.machinery


def _ensure_eventlet_stub() -> None:
    if "eventlet" in sys.modules:
        return
    eventlet_module = types.ModuleType("eventlet")
    eventlet_module.monkey_patch = lambda **_kwargs: None  # type: ignore[attr-defined]
    eventlet_module.sleep = lambda _seconds: None  # type: ignore[attr-defined]

    wsgi_module = types.ModuleType("eventlet.wsgi")
    wsgi_module.HttpProtocol = object()  # type: ignore[attr-defined]

    eventlet_module.wsgi = wsgi_module  # type: ignore[attr-defined]

    eventlet_module.__spec__ = importlib.machinery.ModuleSpec("eventlet", loader=None)
    wsgi_module.__spec__ = importlib.machinery.ModuleSpec("eventlet.wsgi", loader=None)

    sys.modules["eventlet"] = eventlet_module
    sys.modules["eventlet.wsgi"] = wsgi_module


_ensure_eventlet_stub()

from Data.Engine.server import create_app


_SCHEMA_DEFINITION = """CREATE TABLE IF NOT EXISTS devices (
    guid TEXT PRIMARY KEY,
    hostname TEXT,
    description TEXT,
    created_at INTEGER,
    agent_hash TEXT,
    memory TEXT,
    network TEXT,
    software TEXT,
    storage TEXT,
    cpu TEXT,
    device_type TEXT,
    domain TEXT,
    external_ip TEXT,
    internal_ip TEXT,
    last_reboot TEXT,
    last_seen INTEGER,
    last_user TEXT,
    operating_system TEXT,
    uptime INTEGER,
    agent_id TEXT,
    ansible_ee_ver TEXT,
    connection_type TEXT,
    connection_endpoint TEXT,
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
    created_by_user_id TEXT,
    used_at TEXT,
    used_by_guid TEXT,
    max_uses INTEGER,
    use_count INTEGER,
    last_used_at TEXT,
    site_id INTEGER
);
CREATE TABLE IF NOT EXISTS enrollment_install_codes_persistent (
    id TEXT PRIMARY KEY,
    code TEXT UNIQUE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_by_user_id TEXT,
    used_at TEXT,
    used_by_guid TEXT,
    max_uses INTEGER NOT NULL DEFAULT 1,
    last_known_use_count INTEGER NOT NULL DEFAULT 0,
    last_used_at TEXT,
    is_active INTEGER NOT NULL DEFAULT 1,
    archived_at TEXT,
    consumed_at TEXT,
    site_id INTEGER
);
CREATE TABLE IF NOT EXISTS device_approvals (
    id TEXT PRIMARY KEY,
    approval_reference TEXT UNIQUE,
    guid TEXT,
    hostname_claimed TEXT,
    ssl_key_fingerprint_claimed TEXT,
    enrollment_code_id TEXT,
    site_id INTEGER,
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
CREATE TABLE IF NOT EXISTS device_list_views (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE,
    columns_json TEXT,
    filters_json TEXT,
    created_at INTEGER,
    updated_at INTEGER
);
CREATE TABLE IF NOT EXISTS sites (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT,
    description TEXT,
    created_at INTEGER,
    enrollment_code_id TEXT
);
CREATE TABLE IF NOT EXISTS device_sites (
    device_hostname TEXT PRIMARY KEY,
    site_id INTEGER,
    assigned_at INTEGER
);
CREATE TABLE IF NOT EXISTS github_token (
    id INTEGER PRIMARY KEY,
    token TEXT
);
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE,
    display_name TEXT,
    password_sha512 TEXT,
    role TEXT,
    last_login INTEGER,
    created_at INTEGER,
    updated_at INTEGER
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
    monkeypatch.setenv("BOREALIS_ENGINE_DISABLE_LEGACY_PROXY", "1")

    db_path = tmp_path / "database" / "engine.sqlite3"
    _initialise_legacy_schema(db_path)
    conn = sqlite3.connect(str(db_path))
    try:
        cur = conn.cursor()
        cur.execute(
            """
            INSERT INTO devices (
                guid,
                hostname,
                description,
                created_at,
                agent_hash,
                memory,
                network,
                software,
                storage,
                cpu,
                device_type,
                domain,
                external_ip,
                internal_ip,
                last_reboot,
                last_seen,
                last_user,
                operating_system,
                uptime,
                agent_id,
                ansible_ee_ver,
                connection_type,
                connection_endpoint,
                ssl_key_fingerprint,
                token_version,
                status,
                key_added_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "GUID-TEST-0001",
                "test-device",
                "Test device for Engine API",
                1_700_000_000,
                "hash-123",
                json.dumps([{"slot": "DIMM1", "size_gb": 16}]),
                json.dumps([{"iface": "eth0", "mac": "00:11:22:33:44:55"}]),
                json.dumps(["sample-app"]),
                json.dumps([{"drive": "C", "size_gb": 256}]),
                json.dumps({"name": "Intel", "cores": 8}),
                "Workstation",
                "example.local",
                "203.0.113.5",
                "10.0.0.10",
                "2025-10-01T00:00:00Z",
                1_700_000_500,
                "Alice",
                "Windows 11 Pro",
                7200,
                "test-device-agent",
                "1.0.0",
                "",
                "",
                "FF:FF:FF",
                1,
                "active",
                "2025-10-01T00:00:00Z",
            ),
        )
        site_code_id = "SITE-CODE-0001"
        site_code_value = "SITE-MAIN-CODE"
        site_code_created = "2025-01-01T00:00:00Z"
        site_code_expires = "2030-01-01T00:00:00Z"
        cur.execute(
            """
            INSERT INTO enrollment_install_codes (
                id,
                code,
                expires_at,
                created_by_user_id,
                used_at,
                used_by_guid,
                max_uses,
                use_count,
                last_used_at,
                site_id
            ) VALUES (?, ?, ?, ?, NULL, NULL, 0, 0, NULL, ?)
            """,
            (site_code_id, site_code_value, site_code_expires, "admin", 1),
        )
        cur.execute(
            """
            INSERT INTO enrollment_install_codes_persistent (
                id,
                code,
                created_at,
                expires_at,
                created_by_user_id,
                used_at,
                used_by_guid,
                max_uses,
                last_known_use_count,
                last_used_at,
                is_active,
                archived_at,
                consumed_at,
                site_id
            ) VALUES (?, ?, ?, ?, ?, NULL, NULL, 0, 0, NULL, 1, NULL, NULL, ?)
            """,
            (site_code_id, site_code_value, site_code_created, site_code_expires, "admin", 1),
        )
        cur.execute(
            "INSERT INTO sites (id, name, description, created_at, enrollment_code_id) VALUES (?, ?, ?, ?, ?)",
            (1, "Main Lab", "Primary integration site", 1_700_000_000, site_code_id),
        )
        cur.execute(
            "INSERT INTO device_sites (device_hostname, site_id, assigned_at) VALUES (?, ?, ?)",
            ("test-device", 1, 1_700_000_500),
        )
        cur.execute(
            """
            INSERT INTO users (id, username, display_name, password_sha512, role, last_login, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (1, "admin", "Administrator", "test", "Admin", 0, 0, 0),
        )
        cur.execute(
            """
            INSERT INTO device_approvals (
                id,
                approval_reference,
                guid,
                hostname_claimed,
                ssl_key_fingerprint_claimed,
                enrollment_code_id,
                site_id,
                status,
                client_nonce,
                server_nonce,
                agent_pubkey_der,
                created_at,
                updated_at,
                approved_by_user_id
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                "approval-1",
                "APP-REF-1",
                None,
                "pending-device",
                "aa:bb:cc:dd",
                site_code_id,
                1,
                "pending",
                "client-nonce",
                "server-nonce",
                None,
                "2025-01-01T00:00:00Z",
                "2025-01-01T00:00:00Z",
                None,
            ),
        )
        conn.commit()
    finally:
        conn.close()

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
        "API_GROUPS": ("core", "auth", "tokens", "enrollment", "devices", "assemblies"),
    }

    app, _socketio, _context = create_app(config)
    app.config.update(TESTING=True)

    yield EngineTestHarness(app=app, db_path=db_path, bundle_contents=bundle_contents)
