# ======================================================
# Data\Engine\database_migrations.py
# Description: Provides schema evolution helpers for the Engine sqlite
#              database without importing the legacy ``Modules`` package.
#
# API Endpoints (if applicable): None
# ======================================================

"""Engine database schema migration helpers."""

from __future__ import annotations

import sqlite3
import uuid
from datetime import datetime, timezone
from typing import List, Optional, Sequence, Tuple


DEVICE_TABLE = "devices"


def apply_all(conn: sqlite3.Connection) -> None:
    """
    Run all known schema migrations against the provided sqlite3 connection.
    """

    _ensure_devices_table(conn)
    _ensure_device_aux_tables(conn)
    _ensure_device_vpn_config_table(conn)
    _ensure_refresh_token_table(conn)
    _ensure_install_code_table(conn)
    _ensure_install_code_persistence_table(conn)
    _ensure_device_approval_table(conn)

    conn.commit()


def _ensure_devices_table(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    if not _table_exists(cur, DEVICE_TABLE):
        _create_devices_table(cur)
        return

    column_info = _table_info(cur, DEVICE_TABLE)
    col_names = [c[1] for c in column_info]
    pk_cols = [c[1] for c in column_info if c[5]]

    needs_rebuild = pk_cols != ["guid"]
    required_columns = {
        "guid": "TEXT",
        "hostname": "TEXT",
        "description": "TEXT",
        "created_at": "INTEGER",
        "agent_hash": "TEXT",
        "memory": "TEXT",
        "network": "TEXT",
        "software": "TEXT",
        "storage": "TEXT",
        "cpu": "TEXT",
        "device_type": "TEXT",
        "domain": "TEXT",
        "external_ip": "TEXT",
        "internal_ip": "TEXT",
        "last_reboot": "TEXT",
        "last_seen": "INTEGER",
        "last_user": "TEXT",
        "operating_system": "TEXT",
        "uptime": "INTEGER",
        "agent_id": "TEXT",
        "ansible_ee_ver": "TEXT",
        "connection_type": "TEXT",
        "connection_endpoint": "TEXT",
        "agent_vnc_password": "TEXT",
        "ssl_key_fingerprint": "TEXT",
        "token_version": "INTEGER",
        "status": "TEXT",
        "key_added_at": "TEXT",
    }

    missing_columns = [col for col in required_columns if col not in col_names]
    if missing_columns:
        needs_rebuild = True

    if needs_rebuild:
        _rebuild_devices_table(conn, column_info)
    else:
        _ensure_column_defaults(cur)

    _ensure_device_indexes(cur)


def _ensure_device_aux_tables(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS device_keys (
            id TEXT PRIMARY KEY,
            guid TEXT NOT NULL,
            ssl_key_fingerprint TEXT NOT NULL,
            added_at TEXT NOT NULL,
            retired_at TEXT
        )
        """
    )
    cur.execute(
        """
        CREATE UNIQUE INDEX IF NOT EXISTS uq_device_keys_guid_fingerprint
            ON device_keys(guid, ssl_key_fingerprint)
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_device_keys_guid
            ON device_keys(guid)
        """
    )


def _ensure_device_vpn_config_table(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS device_vpn_config (
            agent_id TEXT PRIMARY KEY,
            allowed_ports TEXT,
            updated_at TEXT,
            updated_by TEXT
        )
        """
    )


def _ensure_refresh_token_table(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS refresh_tokens (
            id TEXT PRIMARY KEY,
            guid TEXT NOT NULL,
            token_hash TEXT NOT NULL,
            dpop_jkt TEXT,
            created_at TEXT NOT NULL,
            expires_at TEXT NOT NULL,
            revoked_at TEXT,
            last_used_at TEXT
        )
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_refresh_tokens_guid
            ON refresh_tokens(guid)
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at
            ON refresh_tokens(expires_at)
        """
    )


def _ensure_install_code_table(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS enrollment_install_codes (
            id TEXT PRIMARY KEY,
            code TEXT NOT NULL UNIQUE,
            expires_at TEXT NOT NULL,
            created_by_user_id TEXT,
            used_at TEXT,
            used_by_guid TEXT,
            max_uses INTEGER NOT NULL DEFAULT 1,
            use_count INTEGER NOT NULL DEFAULT 0,
            last_used_at TEXT,
            site_id INTEGER
        )
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_eic_expires_at
            ON enrollment_install_codes(expires_at)
        """
    )

    columns = {row[1] for row in _table_info(cur, "enrollment_install_codes")}
    if "max_uses" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes
                ADD COLUMN max_uses INTEGER NOT NULL DEFAULT 1
            """
        )
    if "use_count" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes
                ADD COLUMN use_count INTEGER NOT NULL DEFAULT 0
            """
        )
    if "last_used_at" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes
                ADD COLUMN last_used_at TEXT
            """
        )
    if "site_id" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes
                ADD COLUMN site_id INTEGER
            """
        )


def _ensure_install_code_persistence_table(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS enrollment_install_codes_persistent (
            id TEXT PRIMARY KEY,
            code TEXT NOT NULL UNIQUE,
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
        )
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_eicp_active
            ON enrollment_install_codes_persistent(is_active, expires_at)
        """
    )
    cur.execute(
        """
        CREATE UNIQUE INDEX IF NOT EXISTS uq_eicp_code
            ON enrollment_install_codes_persistent(code)
        """
    )

    columns = {row[1] for row in _table_info(cur, "enrollment_install_codes_persistent")}
    if "last_known_use_count" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes_persistent
                ADD COLUMN last_known_use_count INTEGER NOT NULL DEFAULT 0
            """
        )
    if "archived_at" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes_persistent
                ADD COLUMN archived_at TEXT
            """
        )
    if "consumed_at" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes_persistent
                ADD COLUMN consumed_at TEXT
            """
        )
    if "is_active" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes_persistent
                ADD COLUMN is_active INTEGER NOT NULL DEFAULT 1
            """
        )
    if "used_at" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes_persistent
                ADD COLUMN used_at TEXT
            """
        )
    if "used_by_guid" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes_persistent
                ADD COLUMN used_by_guid TEXT
            """
        )
    if "last_used_at" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes_persistent
                ADD COLUMN last_used_at TEXT
            """
        )
    if "site_id" not in columns:
        cur.execute(
            """
            ALTER TABLE enrollment_install_codes_persistent
                ADD COLUMN site_id INTEGER
            """
        )


def _ensure_device_approval_table(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS device_approvals (
            id TEXT PRIMARY KEY,
            approval_reference TEXT NOT NULL UNIQUE,
            guid TEXT,
            hostname_claimed TEXT NOT NULL,
            ssl_key_fingerprint_claimed TEXT NOT NULL,
            enrollment_code_id TEXT NOT NULL,
            site_id INTEGER,
            status TEXT NOT NULL,
            client_nonce TEXT NOT NULL,
            server_nonce TEXT NOT NULL,
            agent_pubkey_der BLOB NOT NULL,
            created_at TEXT NOT NULL,
            updated_at TEXT NOT NULL,
            approved_by_user_id TEXT
        )
        """
    )
    cur.execute("PRAGMA table_info(device_approvals)")
    columns = {row[1] for row in cur.fetchall()}
    if "site_id" not in columns:
        cur.execute(
            """
            ALTER TABLE device_approvals
                ADD COLUMN site_id INTEGER
            """
        )

    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_da_status
            ON device_approvals(status)
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_da_fp_status
            ON device_approvals(ssl_key_fingerprint_claimed, status)
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_da_site
            ON device_approvals(site_id)
        """
    )


def _create_devices_table(cur: sqlite3.Cursor) -> None:
    cur.execute(
        """
        CREATE TABLE devices (
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
            agent_vnc_password TEXT,
            ssl_key_fingerprint TEXT,
            token_version INTEGER DEFAULT 1,
            status TEXT DEFAULT 'active',
            key_added_at TEXT
        )
        """
    )
    _ensure_device_indexes(cur)


def _ensure_device_indexes(cur: sqlite3.Cursor) -> None:
    cur.execute(
        """
        CREATE UNIQUE INDEX IF NOT EXISTS uq_devices_hostname
            ON devices(hostname)
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_devices_ssl_key
            ON devices(ssl_key_fingerprint)
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_devices_status
            ON devices(status)
        """
    )


def _ensure_column_defaults(cur: sqlite3.Cursor) -> None:
    cur.execute(
        """
        UPDATE devices
           SET token_version = COALESCE(token_version, 1)
         WHERE token_version IS NULL
        """
    )
    cur.execute(
        """
        UPDATE devices
           SET status = COALESCE(status, 'active')
         WHERE status IS NULL OR status = ''
        """
    )


def _rebuild_devices_table(conn: sqlite3.Connection, column_info: Sequence[Tuple]) -> None:
    cur = conn.cursor()
    cur.execute("PRAGMA foreign_keys=OFF")
    cur.execute("BEGIN IMMEDIATE")

    cur.execute("ALTER TABLE devices RENAME TO devices_legacy")
    _create_devices_table(cur)

    legacy_columns = [c[1] for c in column_info]
    cur.execute(f"SELECT {', '.join(legacy_columns)} FROM devices_legacy")
    rows = cur.fetchall()

    insert_sql = (
        """
        INSERT OR REPLACE INTO devices (
            guid, hostname, description, created_at, agent_hash, memory,
            network, software, storage, cpu, device_type, domain, external_ip,
            internal_ip, last_reboot, last_seen, last_user, operating_system,
            uptime, agent_id, ansible_ee_ver, connection_type, connection_endpoint,
            agent_vnc_password, ssl_key_fingerprint, token_version, status, key_added_at
        )
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """
    )

    for row in rows:
        record = dict(zip(legacy_columns, row))
        guid = _normalized_guid(record.get("guid"))
        if not guid:
            guid = str(uuid.uuid4())
        hostname = record.get("hostname")
        created_at = record.get("created_at")
        key_added_at = record.get("key_added_at")
        if key_added_at is None:
            key_added_at = _default_key_added_at(created_at)

        params: Tuple = (
            guid,
            hostname,
            record.get("description"),
            created_at,
            record.get("agent_hash"),
            record.get("memory"),
            record.get("network"),
            record.get("software"),
            record.get("storage"),
            record.get("cpu"),
            record.get("device_type"),
            record.get("domain"),
            record.get("external_ip"),
            record.get("internal_ip"),
            record.get("last_reboot"),
            record.get("last_seen"),
            record.get("last_user"),
            record.get("operating_system"),
            record.get("uptime"),
            record.get("agent_id"),
            record.get("ansible_ee_ver"),
            record.get("connection_type"),
            record.get("connection_endpoint"),
            record.get("agent_vnc_password"),
            record.get("ssl_key_fingerprint"),
            record.get("token_version") or 1,
            record.get("status") or "active",
            key_added_at,
        )
        cur.execute(insert_sql, params)

    cur.execute("DROP TABLE devices_legacy")
    cur.execute("COMMIT")
    cur.execute("PRAGMA foreign_keys=ON")


def _default_key_added_at(created_at: Optional[int]) -> Optional[str]:
    if created_at:
        try:
            dt = datetime.fromtimestamp(int(created_at), tz=timezone.utc)
            return dt.isoformat()
        except Exception:
            pass
    return datetime.now(tz=timezone.utc).isoformat()


def _table_exists(cur: sqlite3.Cursor, name: str) -> bool:
    cur.execute(
        "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?",
        (name,),
    )
    return cur.fetchone() is not None


def _table_info(cur: sqlite3.Cursor, name: str) -> List[Tuple]:
    cur.execute(f"PRAGMA table_info({name})")
    return cur.fetchall()


def _normalized_guid(value: Optional[str]) -> str:
    if not value:
        return ""
    return str(value).strip()
