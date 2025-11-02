# ======================================================
# Data\Engine\database.py
# Description: Database initialisation helpers for the Engine runtime, ensuring schema parity and default operator bootstrap.
#
# API Endpoints (if applicable): None
# ======================================================

"""Database bootstrap helpers for the Borealis Engine runtime."""

from __future__ import annotations

import logging
import sqlite3
import time
from pathlib import Path
from typing import Optional, Sequence

from . import database_migrations


_DEFAULT_ADMIN_HASH = (
    "e6c83b282aeb2e022844595721cc00bbda47cb24537c1779f9bb84f04039e167"
    "6e6ba8573e588da1052510e3aa0a32a9e55879ae22b0c2d62136fc0a3e85f8bb"
)


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
        _ensure_users_table(conn, logger=logger)
        _ensure_default_admin(conn, logger=logger)
        _ensure_ansible_recaps(conn, logger=logger)
        _ensure_agent_service_accounts(conn, logger=logger)
        _ensure_credentials(conn, logger=logger)
        _ensure_github_token(conn, logger=logger)
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
                last_used_at
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
                p.last_used_at
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
                      last_used_at = excluded.last_used_at
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
                created_at INTEGER
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
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure site tables: %s", exc, exc_info=True)
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
