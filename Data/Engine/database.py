# ======================================================
# Data\Engine\database.py
# Description: Database initialisation helpers for the Engine runtime, ensuring schema parity and default operator bootstrap.
#
# API Endpoints (if applicable): None
# ======================================================

"""Database bootstrap helpers for the Borealis Engine runtime."""

from __future__ import annotations

import logging
import secrets
import sqlite3
import time
import uuid
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Optional, Sequence

from . import database_migrations


_DEFAULT_ADMIN_HASH = (
    "e6c83b282aeb2e022844595721cc00bbda47cb24537c1779f9bb84f04039e167"
    "6e6ba8573e588da1052510e3aa0a32a9e55879ae22b0c2d62136fc0a3e85f8bb"
)


def _iso(dt: datetime) -> str:
    return dt.astimezone(timezone.utc).isoformat()


def _generate_install_code() -> str:
    raw = secrets.token_hex(16).upper()
    return "-".join(raw[i : i + 4] for i in range(0, len(raw), 4))


def initialise_engine_database(database_path: str, *, logger: Optional[logging.Logger] = None) -> None:
    """Ensure the Engine database has the required schema and default admin account."""

    path = Path(database_path or "").expanduser()
    if not path:
        if logger:
            logger.warning("Engine database path is empty; skipping initialisation.")
        return

    try:
        path.parent.mkdir(parents=True, exist_ok=True)
    except Exception:
        # Directory creation failures surface more clearly during sqlite connect.
        pass

    conn = sqlite3.connect(str(path))
    try:
        _apply_engine_migrations(conn, logger=logger)
        _restore_persisted_enrollment_codes(conn, logger=logger)
        _ensure_activity_history(conn, logger=logger)
        _ensure_device_list_views(conn, logger=logger)
        _ensure_sites(conn, logger=logger)
        _ensure_site_enrollment_codes(conn, logger=logger)
        _ensure_users_table(conn, logger=logger)
        _ensure_default_admin(conn, logger=logger)
        _ensure_ansible_recaps(conn, logger=logger)
        _ensure_agent_service_accounts(conn, logger=logger)
        _ensure_credentials(conn, logger=logger)
        _ensure_github_token(conn, logger=logger)
        _ensure_device_filters(conn, database_path=str(path), logger=logger)
        _ensure_scheduled_jobs(conn, logger=logger)
        conn.commit()
    except Exception as exc:  # pragma: no cover - defensive runtime guard
        if logger:
            logger.error("Database initialisation failed: %s", exc, exc_info=True)
        else:
            raise
    finally:
        conn.close()


def _apply_engine_migrations(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    try:
        database_migrations.apply_all(conn)
    except Exception as exc:
        if logger:
            logger.error("Engine schema migration failed: %s", exc, exc_info=True)
        else:  # pragma: no cover - escalated in tests if logger absent
            raise


def _restore_persisted_enrollment_codes(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            "SELECT 1 FROM sqlite_master WHERE type='table' AND name='enrollment_install_codes_persistent'"
        )
        if cur.fetchone() is None:
            return
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
            )
            SELECT
                p.id,
                p.code,
                p.expires_at,
                p.created_by_user_id,
                p.used_at,
                p.used_by_guid,
                p.max_uses,
                p.last_known_use_count,
                p.last_used_at,
                p.site_id
              FROM enrollment_install_codes_persistent AS p
             WHERE p.is_active = 1
            ON CONFLICT(id) DO UPDATE
                  SET code = excluded.code,
                      expires_at = excluded.expires_at,
                      created_by_user_id = excluded.created_by_user_id,
                      used_at = excluded.used_at,
                      used_by_guid = excluded.used_by_guid,
                      max_uses = excluded.max_uses,
                      use_count = excluded.use_count,
                      last_used_at = excluded.last_used_at,
                      site_id = excluded.site_id
            """
        )
        conn.commit()
    except Exception as exc:
        if logger:
            logger.error("Failed to restore enrollment codes from persistence: %s", exc, exc_info=True)
    finally:
        cur.close()


def _ensure_activity_history(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS activity_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                hostname TEXT,
                script_path TEXT,
                script_name TEXT,
                script_type TEXT,
                ran_at INTEGER,
                status TEXT,
                stdout TEXT,
                stderr TEXT
            )
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure activity_history table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_device_list_views(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS device_list_views (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT UNIQUE NOT NULL,
                columns_json TEXT NOT NULL,
                filters_json TEXT,
                created_at INTEGER,
                updated_at INTEGER
            )
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure device_list_views table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_sites(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS sites (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT UNIQUE NOT NULL,
                description TEXT,
                created_at INTEGER,
                enrollment_code_id TEXT
            )
            """
        )
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS device_sites (
                device_hostname TEXT UNIQUE NOT NULL,
                site_id INTEGER NOT NULL,
                assigned_at INTEGER,
                FOREIGN KEY(site_id) REFERENCES sites(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute("PRAGMA table_info(sites)")
        columns = {row[1] for row in cur.fetchall()}
        if "enrollment_code_id" not in columns:
            cur.execute("ALTER TABLE sites ADD COLUMN enrollment_code_id TEXT")
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure site tables: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_site_enrollment_codes(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute("SELECT id, enrollment_code_id FROM sites")
        sites = cur.fetchall()
        if not sites:
            return

        now = datetime.now(tz=timezone.utc)
        long_expiry = _iso(now + timedelta(days=3650))
        for site_id, current_code_id in sites:
            active_code_id: Optional[str] = None
            if current_code_id:
                cur.execute(
                    "SELECT id, site_id FROM enrollment_install_codes WHERE id = ?",
                    (current_code_id,),
                )
                existing = cur.fetchone()
                if existing:
                    active_code_id = current_code_id
                    if existing[1] is None:
                        cur.execute(
                            "UPDATE enrollment_install_codes SET site_id = ? WHERE id = ?",
                            (site_id, current_code_id),
                        )
                        cur.execute(
                            "UPDATE enrollment_install_codes_persistent SET site_id = COALESCE(site_id, ?) WHERE id = ?",
                            (site_id, current_code_id),
                        )

            if not active_code_id:
                cur.execute(
                    """
                    SELECT id, code, created_at, expires_at, max_uses, last_known_use_count, last_used_at, site_id
                      FROM enrollment_install_codes_persistent
                     WHERE site_id = ? AND is_active = 1
                     ORDER BY datetime(created_at) DESC
                     LIMIT 1
                    """,
                    (site_id,),
                )
                row = cur.fetchone()
                if row:
                    active_code_id = row[0]
                    if row[7] is None:
                        cur.execute(
                            "UPDATE enrollment_install_codes_persistent SET site_id = ? WHERE id = ?",
                            (site_id, active_code_id),
                        )
                    cur.execute(
                        """
                        INSERT OR REPLACE INTO enrollment_install_codes (
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
                        )
                        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                        """,
                        (
                            row[0],
                            row[1],
                            row[3] or long_expiry,
                            "system",
                            None,
                            None,
                            row[4] or 0,
                            row[5] or 0,
                            row[6],
                            site_id,
                        ),
                    )

            if not active_code_id:
                new_id = str(uuid.uuid4())
                code_value = _generate_install_code()
                issued_at = _iso(now)
                cur.execute(
                    """
                    INSERT OR REPLACE INTO enrollment_install_codes (
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
                    )
                    VALUES (?, ?, ?, 'system', NULL, NULL, 0, 0, NULL, ?)
                    """,
                    (new_id, code_value, long_expiry, site_id),
                )
                cur.execute(
                    """
                    INSERT OR REPLACE INTO enrollment_install_codes_persistent (
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
                    )
                    VALUES (?, ?, ?, ?, 'system', NULL, NULL, 0, 0, NULL, 1, NULL, NULL, ?)
                    """,
                    (new_id, code_value, issued_at, long_expiry, site_id),
                )
                active_code_id = new_id

            if active_code_id and active_code_id != current_code_id:
                cur.execute(
                    "UPDATE sites SET enrollment_code_id = ? WHERE id = ?",
                    (active_code_id, site_id),
                )
        conn.commit()
    except Exception as exc:
        conn.rollback()
        if logger:
            logger.error("Failed to ensure site enrollment codes: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_users_table(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS users (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT UNIQUE NOT NULL,
                display_name TEXT,
                password_sha512 TEXT NOT NULL,
                role TEXT NOT NULL DEFAULT 'Admin',
                last_login INTEGER,
                created_at INTEGER,
                updated_at INTEGER,
                mfa_enabled INTEGER NOT NULL DEFAULT 0,
                mfa_secret TEXT
            )
            """
        )

        cur.execute("PRAGMA table_info(users)")
        columns = {row[1] for row in cur.fetchall()}

        if "mfa_enabled" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN mfa_enabled INTEGER NOT NULL DEFAULT 0")
        if "mfa_secret" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN mfa_secret TEXT")
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure users table: %s", exc, exc_info=True)
        else:  # pragma: no cover - escalate without logger for tests
            raise
    finally:
        cur.close()


def _ensure_default_admin(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute("SELECT COUNT(*) FROM users WHERE LOWER(role)='admin'")
        admin_count = (cur.fetchone() or [0])[0] or 0
        if admin_count > 0:
            return

        now_ts = int(time.time())

        cur.execute("SELECT id, role FROM users WHERE LOWER(username)='admin'")
        existing = cur.fetchone()
        if existing:
            cur.execute(
                "UPDATE users SET role='Admin', updated_at=? WHERE id=? AND LOWER(role)!='admin'",
                (now_ts, existing[0]),
            )
        else:
            cur.execute(
                """
                INSERT INTO users(username, display_name, password_sha512, role, created_at, updated_at)
                VALUES(?,?,?,?,?,?)
                """,
                ("admin", "Administrator", _DEFAULT_ADMIN_HASH, "Admin", now_ts, now_ts),
            )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure default admin: %s", exc, exc_info=True)
        else:  # pragma: no cover - escalate without logger for tests
            raise
    finally:
        cur.close()


def _ensure_ansible_recaps(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS ansible_play_recaps (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                run_id TEXT UNIQUE NOT NULL,
                hostname TEXT,
                agent_id TEXT,
                playbook_path TEXT,
                playbook_name TEXT,
                scheduled_job_id INTEGER,
                scheduled_run_id INTEGER,
                activity_job_id INTEGER,
                status TEXT,
                recap_text TEXT,
                recap_json TEXT,
                started_ts INTEGER,
                finished_ts INTEGER,
                created_at INTEGER,
                updated_at INTEGER
            )
            """
        )
        try:
            cur.execute(
                "CREATE INDEX IF NOT EXISTS idx_ansible_recaps_host_created "
                "ON ansible_play_recaps(hostname, created_at)"
            )
            cur.execute(
                "CREATE INDEX IF NOT EXISTS idx_ansible_recaps_status "
                "ON ansible_play_recaps(status)"
            )
        except Exception:
            # Index creation failures are non-fatal; continue without logging noise.
            pass
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure ansible_play_recaps table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_agent_service_accounts(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS agent_service_account (
                agent_id TEXT PRIMARY KEY,
                username TEXT NOT NULL,
                password_hash BLOB,
                password_encrypted BLOB NOT NULL,
                last_rotated_utc TEXT NOT NULL,
                version INTEGER NOT NULL DEFAULT 1
            )
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure agent_service_account table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_credentials(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS credentials (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL UNIQUE,
                description TEXT,
                site_id INTEGER,
                credential_type TEXT NOT NULL DEFAULT 'machine',
                connection_type TEXT NOT NULL DEFAULT 'ssh',
                username TEXT,
                password_encrypted BLOB,
                private_key_encrypted BLOB,
                private_key_passphrase_encrypted BLOB,
                become_method TEXT,
                become_username TEXT,
                become_password_encrypted BLOB,
                metadata_json TEXT,
                created_at INTEGER NOT NULL,
                updated_at INTEGER NOT NULL,
                FOREIGN KEY(site_id) REFERENCES sites(id) ON DELETE SET NULL
            )
            """
        )
        cur.execute("PRAGMA table_info(credentials)")
        columns: Sequence[Sequence[object]] = cur.fetchall()
        existing = {row[1] for row in columns}

        alterations = [
            ("connection_type", "ALTER TABLE credentials ADD COLUMN connection_type TEXT NOT NULL DEFAULT 'ssh'"),
            ("credential_type", "ALTER TABLE credentials ADD COLUMN credential_type TEXT NOT NULL DEFAULT 'machine'"),
            ("metadata_json", "ALTER TABLE credentials ADD COLUMN metadata_json TEXT"),
            ("private_key_passphrase_encrypted", "ALTER TABLE credentials ADD COLUMN private_key_passphrase_encrypted BLOB"),
            ("become_method", "ALTER TABLE credentials ADD COLUMN become_method TEXT"),
            ("become_username", "ALTER TABLE credentials ADD COLUMN become_username TEXT"),
            ("become_password_encrypted", "ALTER TABLE credentials ADD COLUMN become_password_encrypted BLOB"),
            ("site_id", "ALTER TABLE credentials ADD COLUMN site_id INTEGER"),
            ("description", "ALTER TABLE credentials ADD COLUMN description TEXT"),
        ]

        for column, statement in alterations:
            if column not in existing:
                cur.execute(statement)
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure credentials table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_github_token(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS github_token (
                token TEXT
            )
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure github_token table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_device_filters(
    conn: sqlite3.Connection, *, database_path: Optional[str] = None, logger: Optional[logging.Logger] = None
) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS device_filters (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                site_scope TEXT NOT NULL DEFAULT 'global',
                site_name TEXT,
                criteria_json TEXT,
                last_edited_by TEXT,
                last_edited TEXT,
                created_at TEXT,
                updated_at TEXT
            )
            """
        )
        cur.execute("PRAGMA table_info(device_filters)")
        columns: Sequence[Sequence[object]] = cur.fetchall()
        existing = {row[1] for row in columns}

        alterations = [
            ("site_scope", "ALTER TABLE device_filters ADD COLUMN site_scope TEXT"),
            ("site_name", "ALTER TABLE device_filters ADD COLUMN site_name TEXT"),
            ("criteria_json", "ALTER TABLE device_filters ADD COLUMN criteria_json TEXT"),
            ("last_edited_by", "ALTER TABLE device_filters ADD COLUMN last_edited_by TEXT"),
            ("last_edited", "ALTER TABLE device_filters ADD COLUMN last_edited TEXT"),
            ("created_at", "ALTER TABLE device_filters ADD COLUMN created_at TEXT"),
            ("updated_at", "ALTER TABLE device_filters ADD COLUMN updated_at TEXT"),
        ]
        for column, statement in alterations:
            if column not in existing:
                cur.execute(statement)

        # Rebuild table if legacy columns are present (scope/apply_to_all_sites)
        rebuild_needed = "scope" in existing or "apply_to_all_sites" in existing
        if rebuild_needed:
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS device_filters_new (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    name TEXT NOT NULL,
                    site_scope TEXT NOT NULL DEFAULT 'global',
                    site_name TEXT,
                    criteria_json TEXT,
                    last_edited_by TEXT,
                    last_edited TEXT,
                    created_at TEXT,
                    updated_at TEXT
                )
                """
            )

            cur.execute(
                """
                SELECT id, name, scope, apply_to_all_sites, site_name, site_scope, criteria_json,
                       last_edited_by, last_edited, created_at, updated_at
                  FROM device_filters
                """
            )
            rows = cur.fetchall()
            payloads = []
            for (
                pid,
                name,
                legacy_scope,
                apply_all,
                site_name,
                site_scope,
                criteria_json,
                last_edited_by,
                last_edited,
                created_at,
                updated_at,
            ) in rows:
                basis = (site_scope or legacy_scope or "global")
                basis = str(basis).lower()
                resolved_scope = "global" if basis == "global" or bool(apply_all) else "scoped"
                payloads.append(
                    (
                        pid,
                        name,
                        resolved_scope,
                        site_name,
                        criteria_json,
                        last_edited_by,
                        last_edited,
                        created_at,
                        updated_at,
                    )
                )
            if payloads:
                cur.executemany(
                    """
                    INSERT INTO device_filters_new (
                        id, name, site_scope, site_name, criteria_json,
                        last_edited_by, last_edited, created_at, updated_at
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    payloads,
                )
            cur.execute("DROP TABLE device_filters")
            cur.execute("ALTER TABLE device_filters_new RENAME TO device_filters")

    except Exception as exc:
        if logger:
            logger.error("Failed to ensure device_filters table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_scheduled_jobs(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS scheduled_jobs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                components_json TEXT NOT NULL,
                targets_json TEXT NOT NULL,
                schedule_type TEXT NOT NULL,
                start_ts INTEGER,
                duration_stop_enabled INTEGER DEFAULT 0,
                expiration TEXT,
                execution_context TEXT NOT NULL,
                credential_id INTEGER,
                use_service_account INTEGER NOT NULL DEFAULT 1,
                enabled INTEGER DEFAULT 1,
                created_at INTEGER,
                updated_at INTEGER
            )
            """
        )
        cur.execute("PRAGMA table_info(scheduled_jobs)")
        columns: Sequence[Sequence[object]] = cur.fetchall()
        existing = {row[1] for row in columns}

        if "credential_id" not in existing:
            cur.execute("ALTER TABLE scheduled_jobs ADD COLUMN credential_id INTEGER")
        if "use_service_account" not in existing:
            cur.execute(
                "ALTER TABLE scheduled_jobs ADD COLUMN use_service_account INTEGER NOT NULL DEFAULT 1"
            )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure scheduled_jobs table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()
