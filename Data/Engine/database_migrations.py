# ======================================================
# Data\Engine\database_migrations.py
# Description: Provides schema evolution helpers for the Engine database
#              without importing the legacy ``Modules`` package.
#
# API Endpoints (if applicable): None
# ======================================================

"""Engine database schema migration helpers."""

from __future__ import annotations

from Data.Engine.db import dbapi as sqlite3
import uuid
from datetime import datetime, timezone
from typing import List, Optional, Sequence, Tuple

from .auth import device_purge_state


DEVICE_TABLE = "devices"


def apply_all(conn: sqlite3.Connection) -> None:
    """
    Run all known schema migrations against the provided DB-API connection.
    """

    _ensure_devices_table(conn)
    _ensure_device_aux_tables(conn)
    _ensure_device_vpn_config_table(conn)
    _ensure_device_vpn_ip_lease_table(conn)
    _ensure_refresh_token_table(conn)
    _ensure_device_approval_table(conn)
    device_purge_state.ensure_table(conn)

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
        "last_enrollment_at": "INTEGER",
        "agent_hash": "TEXT",
        "agent_role_health": "TEXT",
        "memory": "TEXT",
        "network": "TEXT",
        "software": "TEXT",
        "services": "TEXT",
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


def _ensure_device_vpn_ip_lease_table(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS device_vpn_ip_leases (
            agent_id TEXT PRIMARY KEY,
            virtual_ip TEXT NOT NULL,
            updated_at TEXT NOT NULL
        )
        """
    )
    cur.execute(
        """
        CREATE UNIQUE INDEX IF NOT EXISTS uq_device_vpn_ip_leases_virtual_ip
            ON device_vpn_ip_leases(virtual_ip)
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
            enrollment_code TEXT NOT NULL,
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
    column_info = cur.fetchall()
    columns = {row[1] for row in column_info}
    if "enrollment_code_id" in columns:
        _rebuild_device_approvals_table(conn, column_info)
        cur = conn.cursor()
        cur.execute("PRAGMA table_info(device_approvals)")
        columns = {row[1] for row in cur.fetchall()}
    if "enrollment_code" not in columns:
        cur.execute(
            """
            ALTER TABLE device_approvals
                ADD COLUMN enrollment_code TEXT
            """
        )
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


def _rebuild_device_approvals_table(
    conn: sqlite3.Connection, column_info: Sequence[Tuple]
) -> None:
    cur = conn.cursor()
    try:
        cur.execute("SAVEPOINT migrate_device_approvals")
        cur.execute("ALTER TABLE device_approvals RENAME TO device_approvals_legacy")
        cur.execute(
            """
            CREATE TABLE device_approvals (
                id TEXT PRIMARY KEY,
                approval_reference TEXT NOT NULL UNIQUE,
                guid TEXT,
                hostname_claimed TEXT NOT NULL,
                ssl_key_fingerprint_claimed TEXT NOT NULL,
                enrollment_code TEXT NOT NULL,
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

        legacy_columns = {c[1] for c in column_info}
        selected_columns = [
            col
            for col in (
                "id",
                "approval_reference",
                "guid",
                "hostname_claimed",
                "ssl_key_fingerprint_claimed",
                "enrollment_code",
                "site_id",
                "status",
                "client_nonce",
                "server_nonce",
                "agent_pubkey_der",
                "created_at",
                "updated_at",
                "approved_by_user_id",
            )
            if col in legacy_columns
        ]
        rows = []
        if selected_columns:
            cur.execute(
                f"SELECT {', '.join(selected_columns)} FROM device_approvals_legacy"
            )
            rows = cur.fetchall()

        now_iso = datetime.now(tz=timezone.utc).isoformat()
        insert_sql = (
            """
            INSERT INTO device_approvals (
                id,
                approval_reference,
                guid,
                hostname_claimed,
                ssl_key_fingerprint_claimed,
                enrollment_code,
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
            ON CONFLICT(id) DO UPDATE SET
                approval_reference = EXCLUDED.approval_reference,
                guid = EXCLUDED.guid,
                hostname_claimed = EXCLUDED.hostname_claimed,
                ssl_key_fingerprint_claimed = EXCLUDED.ssl_key_fingerprint_claimed,
                enrollment_code = EXCLUDED.enrollment_code,
                site_id = EXCLUDED.site_id,
                status = EXCLUDED.status,
                client_nonce = EXCLUDED.client_nonce,
                server_nonce = EXCLUDED.server_nonce,
                agent_pubkey_der = EXCLUDED.agent_pubkey_der,
                created_at = EXCLUDED.created_at,
                updated_at = EXCLUDED.updated_at,
                approved_by_user_id = EXCLUDED.approved_by_user_id
            """
        )
        for row in rows:
            record = dict(zip(selected_columns, row))
            hostname_claimed = str(record.get("hostname_claimed") or "").strip()
            ssl_fp = str(record.get("ssl_key_fingerprint_claimed") or "").strip()
            client_nonce = str(record.get("client_nonce") or "").strip()
            server_nonce = str(record.get("server_nonce") or "").strip()
            agent_pubkey_der = record.get("agent_pubkey_der")
            if (
                not hostname_claimed
                or not ssl_fp
                or not client_nonce
                or not server_nonce
                or agent_pubkey_der is None
            ):
                # Legacy/incomplete rows are dropped during rebuild.
                continue
            cur.execute(
                insert_sql,
                (
                    str(record.get("id") or uuid.uuid4()),
                    str(record.get("approval_reference") or uuid.uuid4()),
                    _normalized_guid(record.get("guid")) or None,
                    hostname_claimed,
                    ssl_fp,
                    str(record.get("enrollment_code") or ""),
                    record.get("site_id"),
                    str(record.get("status") or "pending"),
                    client_nonce,
                    server_nonce,
                    agent_pubkey_der,
                    record.get("created_at") or now_iso,
                    record.get("updated_at") or now_iso,
                    record.get("approved_by_user_id"),
                ),
            )

        cur.execute("DROP TABLE device_approvals_legacy")
        cur.execute("RELEASE SAVEPOINT migrate_device_approvals")
    except Exception:
        try:
            cur.execute("ROLLBACK TO SAVEPOINT migrate_device_approvals")
            cur.execute("RELEASE SAVEPOINT migrate_device_approvals")
        except Exception:
            pass
        raise


def _create_devices_table(cur: sqlite3.Cursor) -> None:
    cur.execute(
        """
        CREATE TABLE devices (
            guid TEXT PRIMARY KEY,
            hostname TEXT,
            description TEXT,
            created_at INTEGER,
            last_enrollment_at INTEGER,
            agent_hash TEXT,
            agent_role_health TEXT,
            memory TEXT,
            network TEXT,
            software TEXT,
            services TEXT,
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
        INSERT INTO devices (
            guid, hostname, description, created_at, last_enrollment_at, agent_hash, agent_role_health, memory,
            network, software, services, storage, cpu, device_type, domain, external_ip,
            internal_ip, last_reboot, last_seen, last_user, operating_system,
            uptime, agent_id, connection_type, connection_endpoint,
            agent_vnc_password, ssl_key_fingerprint, token_version, status, key_added_at
        )
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(guid) DO UPDATE SET
            hostname = EXCLUDED.hostname,
            description = EXCLUDED.description,
            created_at = EXCLUDED.created_at,
            last_enrollment_at = EXCLUDED.last_enrollment_at,
            agent_hash = EXCLUDED.agent_hash,
            agent_role_health = EXCLUDED.agent_role_health,
            memory = EXCLUDED.memory,
            network = EXCLUDED.network,
            software = EXCLUDED.software,
            services = EXCLUDED.services,
            storage = EXCLUDED.storage,
            cpu = EXCLUDED.cpu,
            device_type = EXCLUDED.device_type,
            domain = EXCLUDED.domain,
            external_ip = EXCLUDED.external_ip,
            internal_ip = EXCLUDED.internal_ip,
            last_reboot = EXCLUDED.last_reboot,
            last_seen = EXCLUDED.last_seen,
            last_user = EXCLUDED.last_user,
            operating_system = EXCLUDED.operating_system,
            uptime = EXCLUDED.uptime,
            agent_id = EXCLUDED.agent_id,
            connection_type = EXCLUDED.connection_type,
            connection_endpoint = EXCLUDED.connection_endpoint,
            agent_vnc_password = EXCLUDED.agent_vnc_password,
            ssl_key_fingerprint = EXCLUDED.ssl_key_fingerprint,
            token_version = EXCLUDED.token_version,
            status = EXCLUDED.status,
            key_added_at = EXCLUDED.key_added_at
        """
    )

    for row in rows:
        record = dict(zip(legacy_columns, row))
        guid = _normalized_guid(record.get("guid"))
        if not guid:
            guid = str(uuid.uuid4())
        hostname = record.get("hostname")
        created_at = record.get("created_at")
        last_enrollment_at = record.get("last_enrollment_at")
        if last_enrollment_at is None:
            last_enrollment_at = created_at
        key_added_at = record.get("key_added_at")
        if key_added_at is None:
            key_added_at = _default_key_added_at(created_at)

        params: Tuple = (
            guid,
            hostname,
            record.get("description"),
            created_at,
            last_enrollment_at,
            record.get("agent_hash"),
            record.get("agent_role_health"),
            record.get("memory"),
            record.get("network"),
            record.get("software"),
            record.get("services"),
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
