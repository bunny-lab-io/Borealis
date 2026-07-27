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
from typing import Callable, List, Optional, Sequence, Tuple

from .auth import device_purge_state


DEVICE_TABLE = "devices"


SchemaProgressCallback = Callable[[str], None]


def _notify_schema_progress(progress_callback: Optional[SchemaProgressCallback], table_name: str) -> None:
    if progress_callback is not None:
        progress_callback(table_name)


def _run_schema_step(
    progress_callback: Optional[SchemaProgressCallback],
    table_names: Sequence[str],
    step: Callable[[], None],
) -> None:
    for table_name in table_names:
        _notify_schema_progress(progress_callback, table_name)
    step()


def apply_all(
    conn: sqlite3.Connection,
    *,
    progress_callback: Optional[SchemaProgressCallback] = None,
) -> None:
    """
    Run all known schema migrations against the provided DB-API connection.
    """

    _run_schema_step(progress_callback, ("devices",), lambda: _ensure_devices_table(conn))
    _run_schema_step(progress_callback, ("device_keys",), lambda: _ensure_device_aux_tables(conn))
    _run_schema_step(progress_callback, ("device_vpn_config",), lambda: _ensure_device_vpn_config_table(conn))
    _run_schema_step(
        progress_callback,
        ("device_vpn_ip_leases", "device_vpn_key_leases"),
        lambda: _ensure_device_vpn_lease_tables(conn),
    )
    _run_schema_step(progress_callback, ("refresh_tokens",), lambda: _ensure_refresh_token_table(conn))
    _run_schema_step(progress_callback, ("device_approvals",), lambda: _ensure_device_approval_table(conn))
    _run_schema_step(progress_callback, ("enrollment_code_failures",), lambda: _ensure_enrollment_code_failure_table(conn))
    _run_schema_step(
        progress_callback,
        (
            "watchdogs",
            "watchdog_sites",
            "watchdog_targets",
            "watchdog_device_overrides",
            "watchdog_incidents",
            "watchdog_device_state",
        ),
        lambda: _ensure_watchdog_tables(conn),
    )
    _run_schema_step(progress_callback, ("software_icon_assets",), lambda: _ensure_software_icon_assets_table(conn))
    _run_schema_step(progress_callback, ("device_purge_barriers",), lambda: device_purge_state.ensure_table(conn))

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
        "sessions": "TEXT",
        "processes": "TEXT",
        "device_type": "TEXT",
        "domain": "TEXT",
        "external_ip": "TEXT",
        "internal_ip": "TEXT",
        "last_reboot": "TEXT",
        "last_seen": "INTEGER",
        "cpu_percent": "REAL",
        "memory_percent": "REAL",
        "last_user": "TEXT",
        "operating_system": "TEXT",
        "uptime": "INTEGER",
        "agent_id": "TEXT",
        "connection_type": "TEXT",
        "connection_endpoint": "TEXT",
        "agent_release_channel_override": "TEXT",
        "agent_release_channel": "TEXT",
        "agent_branch": "TEXT",
        "agent_update_channel": "TEXT",
        "agent_update_target_build_id": "TEXT",
        "agent_update_state": "TEXT",
        "agent_update_error": "TEXT",
        "agent_update_source": "TEXT",
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


def _ensure_device_vpn_lease_tables(conn: sqlite3.Connection) -> None:
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
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS device_vpn_key_leases (
            agent_id TEXT PRIMARY KEY,
            client_private_key TEXT NOT NULL,
            client_public_key TEXT NOT NULL,
            updated_at TEXT NOT NULL
        )
        """
    )
    cur.execute("PRAGMA table_info(device_vpn_key_leases)")
    key_lease_columns = {str(row[1]) for row in cur.fetchall()}
    for column_name, column_sql in (
        ("client_private_key", "ALTER TABLE device_vpn_key_leases ADD COLUMN client_private_key TEXT NOT NULL DEFAULT ''"),
        ("client_public_key", "ALTER TABLE device_vpn_key_leases ADD COLUMN client_public_key TEXT NOT NULL DEFAULT ''"),
        ("updated_at", "ALTER TABLE device_vpn_key_leases ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''"),
    ):
        if column_name not in key_lease_columns:
            cur.execute(column_sql)


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
            approved_by_user_id TEXT,
            onboarding_job_id INTEGER,
            onboarding_run_id INTEGER,
            onboarding_target TEXT
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
    if "onboarding_job_id" not in columns:
        cur.execute("ALTER TABLE device_approvals ADD COLUMN onboarding_job_id INTEGER")
    if "onboarding_run_id" not in columns:
        cur.execute("ALTER TABLE device_approvals ADD COLUMN onboarding_run_id INTEGER")
    if "onboarding_target" not in columns:
        cur.execute("ALTER TABLE device_approvals ADD COLUMN onboarding_target TEXT")

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
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_da_onboarding_job
            ON device_approvals(onboarding_job_id)
        """
    )


def _ensure_enrollment_code_failure_table(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS enrollment_code_failures (
            id TEXT PRIMARY KEY,
            hostname_claimed TEXT NOT NULL,
            ssl_key_fingerprint_claimed TEXT NOT NULL,
            enrollment_code_mask TEXT NOT NULL,
            remote_addr TEXT,
            first_seen_at TEXT NOT NULL,
            last_seen_at TEXT NOT NULL,
            attempt_count INTEGER NOT NULL DEFAULT 1,
            last_error TEXT NOT NULL
        )
        """
    )
    cur.execute(
        """
        CREATE UNIQUE INDEX IF NOT EXISTS uq_enrollment_code_failures_fp
            ON enrollment_code_failures(ssl_key_fingerprint_claimed)
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_enrollment_code_failures_last_seen
            ON enrollment_code_failures(last_seen_at)
        """
    )


def _ensure_watchdog_tables(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS watchdogs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL UNIQUE,
            description TEXT,
            archived INTEGER NOT NULL DEFAULT 0,
            enabled INTEGER NOT NULL DEFAULT 1,
            severity TEXT NOT NULL DEFAULT 'warning',
            match_mode TEXT NOT NULL DEFAULT 'all',
            site_mode TEXT NOT NULL DEFAULT 'global',
            criteria_json TEXT NOT NULL DEFAULT '{"rules":[]}',
            actions_json TEXT NOT NULL DEFAULT '{"actions":[]}',
            evaluation_interval_seconds INTEGER NOT NULL DEFAULT 60,
            cooldown_seconds INTEGER NOT NULL DEFAULT 900,
            auto_resolve_after_seconds INTEGER NOT NULL DEFAULT 300,
            min_consecutive_matches INTEGER NOT NULL DEFAULT 1,
            boot_grace_seconds INTEGER NOT NULL DEFAULT 0,
            last_edited_by TEXT,
            created_at INTEGER NOT NULL,
            updated_at INTEGER NOT NULL,
            last_evaluated_at INTEGER,
            target_device_count INTEGER NOT NULL DEFAULT 0
        )
        """
    )
    cur.execute("PRAGMA table_info(watchdogs)")
    watchdog_columns = {str(row[1]) for row in cur.fetchall()}
    if "target_device_count" not in watchdog_columns:
        cur.execute(
            """
            ALTER TABLE watchdogs
                ADD COLUMN target_device_count INTEGER NOT NULL DEFAULT 0
            """
        )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_watchdogs_archived_updated
            ON watchdogs(archived, updated_at)
        """
    )

    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS watchdog_sites (
            watchdog_id INTEGER NOT NULL,
            site_id INTEGER NOT NULL,
            FOREIGN KEY(watchdog_id) REFERENCES watchdogs(id) ON DELETE CASCADE,
            FOREIGN KEY(site_id) REFERENCES sites(id) ON DELETE CASCADE
        )
        """
    )
    cur.execute(
        """
        CREATE UNIQUE INDEX IF NOT EXISTS uq_watchdog_sites_watchdog_site
            ON watchdog_sites(watchdog_id, site_id)
        """
    )

    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS watchdog_targets (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            watchdog_id INTEGER NOT NULL,
            kind TEXT NOT NULL,
            target_json TEXT NOT NULL,
            created_at INTEGER NOT NULL,
            FOREIGN KEY(watchdog_id) REFERENCES watchdogs(id) ON DELETE CASCADE
        )
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_watchdog_targets_watchdog
            ON watchdog_targets(watchdog_id)
        """
    )

    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS watchdog_device_overrides (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            watchdog_id INTEGER NOT NULL,
            device_guid TEXT,
            hostname TEXT NOT NULL,
            site_id INTEGER,
            state TEXT NOT NULL,
            reason TEXT,
            created_by TEXT,
            created_at INTEGER NOT NULL,
            expires_at INTEGER,
            updated_at INTEGER NOT NULL,
            FOREIGN KEY(watchdog_id) REFERENCES watchdogs(id) ON DELETE CASCADE
        )
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_watchdog_device_overrides_lookup
            ON watchdog_device_overrides(watchdog_id, hostname, state, expires_at)
        """
    )

    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS watchdog_incidents (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            watchdog_id INTEGER NOT NULL,
            device_guid TEXT,
            hostname TEXT NOT NULL,
            site_id INTEGER,
            severity TEXT NOT NULL,
            state TEXT NOT NULL,
            title TEXT,
            message TEXT,
            sample_json TEXT,
            rule_summary_json TEXT,
            action_summary_json TEXT,
            opened_at INTEGER NOT NULL,
            updated_at INTEGER NOT NULL,
            resolved_at INTEGER,
            resolution_reason TEXT,
            acknowledged_at INTEGER,
            acknowledged_by TEXT,
            trigger_count INTEGER NOT NULL DEFAULT 1,
            FOREIGN KEY(watchdog_id) REFERENCES watchdogs(id) ON DELETE CASCADE
        )
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_watchdog_incidents_watchdog_state
            ON watchdog_incidents(watchdog_id, state, updated_at)
        """
    )
    cur.execute(
        """
        CREATE INDEX IF NOT EXISTS idx_watchdog_incidents_hostname_state
            ON watchdog_incidents(hostname, state, updated_at)
        """
    )

    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS watchdog_device_state (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            watchdog_id INTEGER NOT NULL,
            device_guid TEXT,
            hostname TEXT NOT NULL,
            site_id INTEGER,
            state TEXT NOT NULL,
            consecutive_matches INTEGER NOT NULL DEFAULT 0,
            first_matched_at INTEGER,
            clear_started_at INTEGER,
            last_evaluated_at INTEGER NOT NULL,
            last_matched_at INTEGER,
            last_sample_json TEXT,
            current_incident_id INTEGER,
            last_action_at INTEGER,
            updated_at INTEGER NOT NULL,
            FOREIGN KEY(watchdog_id) REFERENCES watchdogs(id) ON DELETE CASCADE,
            FOREIGN KEY(current_incident_id) REFERENCES watchdog_incidents(id) ON DELETE SET NULL
        )
        """
    )
    cur.execute(
        """
        CREATE UNIQUE INDEX IF NOT EXISTS uq_watchdog_device_state_identity
            ON watchdog_device_state(watchdog_id, hostname)
        """
    )


def _ensure_software_icon_assets_table(conn: sqlite3.Connection) -> None:
    cur = conn.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS software_icon_assets (
            icon_hash TEXT PRIMARY KEY,
            mime_type TEXT NOT NULL,
            icon_bytes BLOB NOT NULL,
            byte_size INTEGER NOT NULL,
            created_at INTEGER NOT NULL,
            updated_at INTEGER NOT NULL
        )
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
                approved_by_user_id TEXT,
                onboarding_job_id INTEGER,
                onboarding_run_id INTEGER,
                onboarding_target TEXT
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
                "onboarding_job_id",
                "onboarding_run_id",
                "onboarding_target",
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
                approved_by_user_id,
                onboarding_job_id,
                onboarding_run_id,
                onboarding_target
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
                approved_by_user_id = EXCLUDED.approved_by_user_id,
                onboarding_job_id = EXCLUDED.onboarding_job_id,
                onboarding_run_id = EXCLUDED.onboarding_run_id,
                onboarding_target = EXCLUDED.onboarding_target
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
                    record.get("onboarding_job_id"),
                    record.get("onboarding_run_id"),
                    record.get("onboarding_target"),
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
            sessions TEXT,
            processes TEXT,
            device_type TEXT,
            domain TEXT,
            external_ip TEXT,
            internal_ip TEXT,
            last_reboot TEXT,
            last_seen INTEGER,
            cpu_percent REAL,
            memory_percent REAL,
            last_user TEXT,
            operating_system TEXT,
            uptime INTEGER,
            agent_id TEXT,
            connection_type TEXT,
            connection_endpoint TEXT,
            agent_release_channel_override TEXT,
            agent_release_channel TEXT,
            agent_branch TEXT,
            agent_update_channel TEXT,
            agent_update_target_build_id TEXT,
            agent_update_state TEXT,
            agent_update_error TEXT,
            agent_update_source TEXT,
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
            network, software, services, storage, cpu, sessions, processes, device_type, domain, external_ip,
            internal_ip, last_reboot, last_seen, cpu_percent, memory_percent, last_user, operating_system,
            uptime, agent_id, connection_type, connection_endpoint,
            agent_release_channel_override, agent_release_channel, agent_branch, agent_update_channel, agent_update_target_build_id,
            agent_update_state, agent_update_error, agent_update_source,
            agent_vnc_password, ssl_key_fingerprint, token_version, status, key_added_at
        )
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
            sessions = EXCLUDED.sessions,
            processes = EXCLUDED.processes,
            device_type = EXCLUDED.device_type,
            domain = EXCLUDED.domain,
            external_ip = EXCLUDED.external_ip,
            internal_ip = EXCLUDED.internal_ip,
            last_reboot = EXCLUDED.last_reboot,
            last_seen = EXCLUDED.last_seen,
            cpu_percent = EXCLUDED.cpu_percent,
            memory_percent = EXCLUDED.memory_percent,
            last_user = EXCLUDED.last_user,
            operating_system = EXCLUDED.operating_system,
            uptime = EXCLUDED.uptime,
            agent_id = EXCLUDED.agent_id,
            connection_type = EXCLUDED.connection_type,
            connection_endpoint = EXCLUDED.connection_endpoint,
            agent_release_channel_override = EXCLUDED.agent_release_channel_override,
            agent_release_channel = EXCLUDED.agent_release_channel,
            agent_branch = EXCLUDED.agent_branch,
            agent_update_channel = EXCLUDED.agent_update_channel,
            agent_update_target_build_id = EXCLUDED.agent_update_target_build_id,
            agent_update_state = EXCLUDED.agent_update_state,
            agent_update_error = EXCLUDED.agent_update_error,
            agent_update_source = EXCLUDED.agent_update_source,
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
            record.get("sessions"),
            record.get("processes"),
            record.get("device_type"),
            record.get("domain"),
            record.get("external_ip"),
            record.get("internal_ip"),
            record.get("last_reboot"),
            record.get("last_seen"),
            record.get("cpu_percent"),
            record.get("memory_percent"),
            record.get("last_user"),
            record.get("operating_system"),
            record.get("uptime"),
            record.get("agent_id"),
            record.get("connection_type"),
            record.get("connection_endpoint"),
            record.get("agent_release_channel_override"),
            record.get("agent_release_channel"),
            record.get("agent_branch"),
            record.get("agent_update_channel"),
            record.get("agent_update_target_build_id"),
            record.get("agent_update_state"),
            record.get("agent_update_error"),
            record.get("agent_update_source"),
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
