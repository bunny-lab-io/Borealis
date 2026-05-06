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
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.db import get_database_manager
import time
from typing import Optional, Sequence

from . import database_migrations
from .services.task_scheduler.queue import ensure_task_scheduler_tables


def _generate_install_code() -> str:
    raw = secrets.token_hex(16).upper()
    return "-".join(raw[i : i + 4] for i in range(0, len(raw), 4))


def initialise_engine_database(database_url: str, *, logger: Optional[logging.Logger] = None) -> None:
    """Ensure the Engine database has the required schema."""

    database_url = str(database_url or "").strip()
    if not database_url:
        if logger:
            logger.warning("Engine database URL is empty; skipping initialisation.")
        return

    manager = get_database_manager(database_url, logger=logger)
    manager.ensure_schemas()
    conn = sqlite3.connect(database_url)
    try:
        _ensure_activity_history(conn, logger=logger)
        _ensure_device_list_views(conn, logger=logger)
        _ensure_sites(conn, logger=logger)
        _apply_engine_migrations(conn, logger=logger)
        _ensure_site_enrollment_codes(conn, logger=logger)
        _ensure_users_table(conn, logger=logger)
        _ensure_directory_services(conn, logger=logger)
        _ensure_user_passkeys(conn, logger=logger)
        _ensure_user_site_assignments(conn, logger=logger)
        _ensure_ansible_recaps(conn, logger=logger)
        _ensure_agent_service_accounts(conn, logger=logger)
        _ensure_credentials(conn, logger=logger)
        _ensure_github_token(conn, logger=logger)
        _ensure_aegis_cipher_state(conn, logger=logger)
        _ensure_device_filters(conn, logger=logger)
        _ensure_device_filter_sites(conn, logger=logger)
        _ensure_device_software_inventory(conn, logger=logger)
        _ensure_scheduled_jobs(conn, logger=logger)
        _ensure_scheduled_job_support_tables(conn, logger=logger)
        ensure_task_scheduler_tables(conn)
        _ensure_workflow_runtime_tables(conn, logger=logger)
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
        try:
            conn.rollback()
        except Exception:
            pass
        if logger:
            logger.error("Engine schema migration failed: %s", exc, exc_info=True)
        raise


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
        cur.execute("PRAGMA table_info(activity_history)")
        columns = {str(row[1]) for row in cur.fetchall() or [] if len(row) > 1}
        for column_name, sql in (
            ("queue_lane", "ALTER TABLE activity_history ADD COLUMN queue_lane TEXT"),
            ("activity_kind", "ALTER TABLE activity_history ADD COLUMN activity_kind TEXT"),
            ("metadata_json", "ALTER TABLE activity_history ADD COLUMN metadata_json TEXT"),
            ("started_at", "ALTER TABLE activity_history ADD COLUMN started_at INTEGER"),
            ("updated_at", "ALTER TABLE activity_history ADD COLUMN updated_at INTEGER"),
            ("finished_at", "ALTER TABLE activity_history ADD COLUMN finished_at INTEGER"),
        ):
            if column_name not in columns:
                cur.execute(sql)
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
                enrollment_code TEXT
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
        if "enrollment_code" not in columns:
            cur.execute("ALTER TABLE sites ADD COLUMN enrollment_code TEXT")
        if "enrollment_code_id" in columns:
            _rebuild_sites_table(conn, logger=logger)
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure site tables: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _rebuild_sites_table(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute("SAVEPOINT migrate_sites")
        cur.execute(
            """
            CREATE TABLE sites_new (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT UNIQUE NOT NULL,
                description TEXT,
                created_at INTEGER,
                enrollment_code TEXT
            )
            """
        )
        cur.execute(
            """
            INSERT INTO sites_new (id, name, description, created_at, enrollment_code)
            SELECT id,
                   name,
                   description,
                   created_at,
                   enrollment_code
              FROM sites
            """
        )
        cur.execute("DROP TABLE sites")
        cur.execute("ALTER TABLE sites_new RENAME TO sites")
        cur.execute("RELEASE SAVEPOINT migrate_sites")
    except Exception:
        try:
            cur.execute("ROLLBACK TO SAVEPOINT migrate_sites")
            cur.execute("RELEASE SAVEPOINT migrate_sites")
        except Exception:
            pass
        raise
    finally:
        cur.close()


def _ensure_site_enrollment_codes(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute("SELECT id, enrollment_code FROM sites")
        sites = cur.fetchall()
        if not sites:
            return
        assigned: set[str] = set()
        for site_id, current_code in sites:
            normalized = str(current_code or "").strip().upper()
            if normalized and normalized not in assigned:
                assigned.add(normalized)
                if normalized != current_code:
                    cur.execute(
                        "UPDATE sites SET enrollment_code = ? WHERE id = ?",
                        (normalized, site_id),
                    )
                continue

            while True:
                candidate = _generate_install_code()
                if candidate in assigned:
                    continue
                cur.execute(
                    "SELECT 1 FROM sites WHERE enrollment_code = ? AND id != ?",
                    (candidate, site_id),
                )
                if cur.fetchone() is None:
                    break

            cur.execute(
                "UPDATE sites SET enrollment_code = ? WHERE id = ?",
                (candidate, site_id),
            )
            assigned.add(candidate)
        cur.execute(
            """
            CREATE UNIQUE INDEX IF NOT EXISTS uq_sites_enrollment_code
                ON sites(enrollment_code)
            """
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
                mfa_disabled INTEGER NOT NULL DEFAULT 0,
                mfa_secret TEXT,
                auth_reset_required INTEGER NOT NULL DEFAULT 0,
                auth_reset_at INTEGER
            )
            """
        )

        cur.execute("PRAGMA table_info(users)")
        columns = {row[1] for row in cur.fetchall()}

        if "mfa_enabled" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN mfa_enabled INTEGER NOT NULL DEFAULT 0")
        if "mfa_disabled" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN mfa_disabled INTEGER NOT NULL DEFAULT 0")
        if "mfa_secret" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN mfa_secret TEXT")
        if "auth_reset_required" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN auth_reset_required INTEGER NOT NULL DEFAULT 0")
        if "auth_reset_at" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN auth_reset_at INTEGER")
        if "auth_source" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN auth_source TEXT NOT NULL DEFAULT 'local'")
        if "directory_provider_id" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN directory_provider_id INTEGER")
        if "directory_subject" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN directory_subject TEXT")
        if "directory_domain" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN directory_domain TEXT")
        if "directory_dn" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN directory_dn TEXT")
        if "directory_groups_json" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN directory_groups_json TEXT")
        if "directory_last_sync_at" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN directory_last_sync_at INTEGER")
        if "directory_disabled" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN directory_disabled INTEGER NOT NULL DEFAULT 0")
        if "directory_disabled_at" not in columns:
            cur.execute("ALTER TABLE users ADD COLUMN directory_disabled_at INTEGER")
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_users_auth_source
                ON users(auth_source)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_users_directory_provider
                ON users(directory_provider_id)
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure users table: %s", exc, exc_info=True)
        else:  # pragma: no cover - escalate without logger for tests
            raise
    finally:
        cur.close()


def _ensure_directory_services(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS directory_providers (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT UNIQUE NOT NULL,
                provider_type TEXT NOT NULL DEFAULT 'ldap',
                enabled INTEGER NOT NULL DEFAULT 0,
                priority INTEGER NOT NULL DEFAULT 100,
                domain_suffix TEXT,
                server_urls_json TEXT NOT NULL DEFAULT '[]',
                host_overrides_json TEXT NOT NULL DEFAULT '{}',
                use_ldaps INTEGER NOT NULL DEFAULT 0,
                tls_required INTEGER NOT NULL DEFAULT 1,
                tls_ca_pem TEXT,
                base_dn TEXT,
                bind_dn TEXT,
                bind_password_encrypted TEXT,
                user_search_filter TEXT,
                username_attribute TEXT,
                display_name_attribute TEXT,
                email_attribute TEXT,
                member_of_attribute TEXT,
                group_search_base_dn TEXT,
                nested_groups INTEGER NOT NULL DEFAULT 1,
                kerberos_realm TEXT,
                kerberos_kdc TEXT,
                kerberos_keytab_encrypted TEXT,
                sync_interval_seconds INTEGER NOT NULL DEFAULT 60,
                last_sync_at INTEGER,
                last_sync_status TEXT,
                last_sync_message TEXT,
                last_test_at INTEGER,
                last_test_status TEXT,
                last_test_message TEXT,
                created_at INTEGER,
                updated_at INTEGER
            )
            """
        )
        cur.execute("PRAGMA table_info(directory_providers)")
        columns = {str(row[1]) for row in cur.fetchall()}
        if "host_overrides_json" not in columns:
            cur.execute("ALTER TABLE directory_providers ADD COLUMN host_overrides_json TEXT NOT NULL DEFAULT '{}'")
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS directory_provider_group_mappings (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                provider_id INTEGER NOT NULL,
                group_dn TEXT NOT NULL,
                role TEXT NOT NULL DEFAULT 'User',
                created_at INTEGER,
                updated_at INTEGER,
                FOREIGN KEY(provider_id) REFERENCES directory_providers(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute(
            """
            CREATE UNIQUE INDEX IF NOT EXISTS uq_directory_provider_group_role
                ON directory_provider_group_mappings(provider_id, group_dn, role)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_directory_provider_enabled_priority
                ON directory_providers(enabled, priority)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_directory_group_provider
                ON directory_provider_group_mappings(provider_id)
            """
        )
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS directory_provider_site_mappings (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                provider_id INTEGER NOT NULL,
                label TEXT,
                group_dns_json TEXT NOT NULL DEFAULT '[]',
                site_ids_json TEXT NOT NULL DEFAULT '[]',
                position INTEGER NOT NULL DEFAULT 0,
                created_at INTEGER,
                updated_at INTEGER,
                FOREIGN KEY(provider_id) REFERENCES directory_providers(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_directory_site_mapping_provider
                ON directory_provider_site_mappings(provider_id, position)
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure directory service tables: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_user_passkeys(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS user_passkeys (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER NOT NULL,
                credential_id TEXT NOT NULL,
                public_key TEXT NOT NULL,
                sign_count INTEGER NOT NULL DEFAULT 0,
                label TEXT,
                transports_json TEXT,
                aaguid TEXT,
                created_at INTEGER,
                last_used_at INTEGER,
                credential_lookup_hmac TEXT,
                secret_encrypted TEXT,
                FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute("PRAGMA table_info(user_passkeys)")
        columns = {row[1] for row in cur.fetchall()}
        if "credential_lookup_hmac" not in columns:
            cur.execute("ALTER TABLE user_passkeys ADD COLUMN credential_lookup_hmac TEXT")
        if "secret_encrypted" not in columns:
            cur.execute("ALTER TABLE user_passkeys ADD COLUMN secret_encrypted TEXT")
        cur.execute("DROP INDEX IF EXISTS uq_user_passkeys_credential_id")
        cur.execute(
            """
            CREATE UNIQUE INDEX IF NOT EXISTS uq_user_passkeys_lookup_hmac
                ON user_passkeys(credential_lookup_hmac)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_user_passkeys_user_id
                ON user_passkeys(user_id)
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure user_passkeys table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()

def _ensure_user_site_assignments(
    conn: sqlite3.Connection,
    *,
    logger: Optional[logging.Logger],
) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS user_site_assignments (
                user_id INTEGER NOT NULL,
                site_id INTEGER NOT NULL,
                assigned_at INTEGER,
                FOREIGN KEY(site_id) REFERENCES sites(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute(
            """
            CREATE UNIQUE INDEX IF NOT EXISTS uq_user_site_assignments_user_site
                ON user_site_assignments(user_id, site_id)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_user_site_assignments_user_id
                ON user_site_assignments(user_id)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_user_site_assignments_site_id
                ON user_site_assignments(site_id)
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure user_site_assignments table: %s", exc, exc_info=True)
        else:
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
                token TEXT,
                reset_required INTEGER NOT NULL DEFAULT 0,
                reset_at INTEGER
            )
            """
        )
        cur.execute("PRAGMA table_info(github_token)")
        columns: Sequence[Sequence[object]] = cur.fetchall()
        existing = {row[1] for row in columns}
        if "reset_required" not in existing:
            cur.execute(
                "ALTER TABLE github_token ADD COLUMN reset_required INTEGER NOT NULL DEFAULT 0"
            )
        if "reset_at" not in existing:
            cur.execute("ALTER TABLE github_token ADD COLUMN reset_at INTEGER")
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure github_token table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_aegis_cipher_state(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS aegis_cipher_state (
                id INTEGER PRIMARY KEY,
                kdf_name TEXT NOT NULL,
                kdf_params_json TEXT NOT NULL,
                verification_token TEXT NOT NULL,
                created_at INTEGER NOT NULL,
                updated_at INTEGER NOT NULL
            )
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure aegis_cipher_state table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_device_filters(conn: sqlite3.Connection, *, logger: Optional[logging.Logger] = None) -> None:
    cur = conn.cursor()
    try:
        cur.execute("PRAGMA table_info(device_filters)")
        existing_columns: Sequence[Sequence[object]] = cur.fetchall()
        existing = {row[1] for row in existing_columns}
        expected = {
            "id",
            "name",
            "description",
            "archived",
            "criteria_mode",
            "site_mode",
            "basic_criteria_json",
            "advanced_criteria_json",
            "last_edited_by",
            "created_at",
            "updated_at",
        }
        rebuild_needed = bool(existing) and existing != expected

        if not existing:
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS device_filters (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    name TEXT NOT NULL UNIQUE,
                    description TEXT,
                    archived INTEGER NOT NULL DEFAULT 0,
                    criteria_mode TEXT NOT NULL DEFAULT 'basic',
                    site_mode TEXT NOT NULL DEFAULT 'global',
                    basic_criteria_json TEXT,
                    advanced_criteria_json TEXT,
                    last_edited_by TEXT,
                    created_at INTEGER NOT NULL,
                    updated_at INTEGER NOT NULL
                )
                """
            )
        elif rebuild_needed:
            cur.execute(
                """
                CREATE TABLE IF NOT EXISTS device_filters_new (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    name TEXT NOT NULL UNIQUE,
                    description TEXT,
                    archived INTEGER NOT NULL DEFAULT 0,
                    criteria_mode TEXT NOT NULL DEFAULT 'basic',
                    site_mode TEXT NOT NULL DEFAULT 'global',
                    basic_criteria_json TEXT,
                    advanced_criteria_json TEXT,
                    last_edited_by TEXT,
                    created_at INTEGER NOT NULL,
                    updated_at INTEGER NOT NULL
                )
                """
            )
            cur.execute("DROP TABLE device_filters")
            cur.execute("ALTER TABLE device_filters_new RENAME TO device_filters")

        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_device_filters_archived_updated
                ON device_filters(archived, updated_at)
            """
        )

    except Exception as exc:
        if logger:
            logger.error("Failed to ensure device_filters table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_device_filter_sites(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS device_filter_sites (
                filter_id INTEGER NOT NULL,
                site_id INTEGER NOT NULL,
                FOREIGN KEY(filter_id) REFERENCES device_filters(id) ON DELETE CASCADE,
                FOREIGN KEY(site_id) REFERENCES sites(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute(
            """
            CREATE UNIQUE INDEX IF NOT EXISTS uq_device_filter_sites_filter_site
                ON device_filter_sites(filter_id, site_id)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_device_filter_sites_site_id
                ON device_filter_sites(site_id)
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure device_filter_sites table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_device_software_inventory(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS device_software_inventory (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                device_guid TEXT NOT NULL,
                name TEXT NOT NULL,
                name_normalized TEXT NOT NULL,
                version TEXT,
                source TEXT NOT NULL,
                captured_at INTEGER NOT NULL,
                metadata_json TEXT
            )
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_device_software_inventory_guid
                ON device_software_inventory(device_guid)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_device_software_inventory_name
                ON device_software_inventory(name_normalized)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_device_software_inventory_source
                ON device_software_inventory(source)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_device_software_inventory_guid_name_source
                ON device_software_inventory(device_guid, name_normalized, source)
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure device_software_inventory table: %s", exc, exc_info=True)
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
                job_kind TEXT NOT NULL DEFAULT 'automation',
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
        if "job_kind" not in existing:
            cur.execute("ALTER TABLE scheduled_jobs ADD COLUMN job_kind TEXT NOT NULL DEFAULT 'automation'")
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure scheduled_jobs table: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_scheduled_job_support_tables(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS scheduled_job_runs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                job_id INTEGER NOT NULL,
                scheduled_ts INTEGER,
                started_ts INTEGER,
                finished_ts INTEGER,
                status TEXT,
                error TEXT,
                created_at INTEGER,
                updated_at INTEGER,
                target_hostname TEXT,
                skip_reason TEXT,
                shared_execution INTEGER NOT NULL DEFAULT 0,
                component_index INTEGER,
                component_kind TEXT,
                component_name TEXT,
                workflow_run_id INTEGER,
                FOREIGN KEY(job_id) REFERENCES scheduled_jobs(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute("PRAGMA table_info(scheduled_job_runs)")
        run_columns = {row[1] for row in cur.fetchall()}
        if "shared_execution" not in run_columns:
            cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN shared_execution INTEGER NOT NULL DEFAULT 0")
        if "component_index" not in run_columns:
            cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN component_index INTEGER")
        if "component_kind" not in run_columns:
            cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN component_kind TEXT")
        if "component_name" not in run_columns:
            cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN component_name TEXT")
        if "workflow_run_id" not in run_columns:
            cur.execute("ALTER TABLE scheduled_job_runs ADD COLUMN workflow_run_id INTEGER")
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_runs_job_sched_target
                ON scheduled_job_runs(job_id, scheduled_ts, target_hostname)
            """
        )
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS scheduled_job_run_activity (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                run_id INTEGER NOT NULL,
                activity_id INTEGER NOT NULL,
                component_kind TEXT,
                script_type TEXT,
                component_path TEXT,
                component_name TEXT,
                created_at INTEGER,
                FOREIGN KEY(run_id) REFERENCES scheduled_job_runs(id) ON DELETE CASCADE,
                FOREIGN KEY(activity_id) REFERENCES activity_history(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_run_activity_run
                ON scheduled_job_run_activity(run_id)
            """
        )
        cur.execute(
            """
            CREATE UNIQUE INDEX IF NOT EXISTS idx_run_activity_activity
                ON scheduled_job_run_activity(activity_id)
            """
        )
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS scheduled_job_onboarding_targets (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                run_id INTEGER NOT NULL,
                job_id INTEGER NOT NULL,
                scheduled_ts INTEGER NOT NULL,
                site_id INTEGER,
                target_input TEXT NOT NULL,
                target_address TEXT,
                target_hostname TEXT,
                ssh_port INTEGER NOT NULL DEFAULT 22,
                status TEXT NOT NULL,
                detail TEXT,
                stdout_snippet TEXT,
                stderr_snippet TEXT,
                approval_reference TEXT,
                created_at INTEGER NOT NULL,
                updated_at INTEGER NOT NULL,
                finished_at INTEGER,
                FOREIGN KEY(run_id) REFERENCES scheduled_job_runs(id) ON DELETE CASCADE,
                FOREIGN KEY(job_id) REFERENCES scheduled_jobs(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_onboarding_targets_run
                ON scheduled_job_onboarding_targets(run_id)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_onboarding_targets_job
                ON scheduled_job_onboarding_targets(job_id, scheduled_ts)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_onboarding_targets_status
                ON scheduled_job_onboarding_targets(status)
            """
        )
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS scheduled_job_onboarding_target_events (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                target_row_id INTEGER NOT NULL,
                run_id INTEGER NOT NULL,
                job_id INTEGER NOT NULL,
                status TEXT NOT NULL,
                task TEXT NOT NULL,
                detail TEXT,
                stdout_snippet TEXT,
                stderr_snippet TEXT,
                started_at INTEGER NOT NULL,
                finished_at INTEGER,
                created_at INTEGER NOT NULL,
                updated_at INTEGER NOT NULL,
                FOREIGN KEY(target_row_id) REFERENCES scheduled_job_onboarding_targets(id) ON DELETE CASCADE,
                FOREIGN KEY(run_id) REFERENCES scheduled_job_runs(id) ON DELETE CASCADE,
                FOREIGN KEY(job_id) REFERENCES scheduled_jobs(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_onboarding_target_events_target
                ON scheduled_job_onboarding_target_events(target_row_id, started_at)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_onboarding_target_events_run
                ON scheduled_job_onboarding_target_events(run_id)
            """
        )
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS scheduled_job_run_targets (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                run_id INTEGER NOT NULL,
                device_guid TEXT,
                hostname TEXT NOT NULL,
                site_id INTEGER,
                resolved_from_filter_id INTEGER,
                inventory_hostname TEXT,
                wireguard_peer_ip TEXT,
                resolved_connection TEXT,
                resolution_status TEXT,
                resolution_reason TEXT,
                resolved_from_filter_ids_json TEXT,
                created_at INTEGER NOT NULL,
                FOREIGN KEY(run_id) REFERENCES scheduled_job_runs(id) ON DELETE CASCADE,
                FOREIGN KEY(resolved_from_filter_id) REFERENCES device_filters(id) ON DELETE SET NULL
            )
            """
        )
        cur.execute("PRAGMA table_info(scheduled_job_run_targets)")
        target_columns = {row[1] for row in cur.fetchall()}
        if "inventory_hostname" not in target_columns:
            cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN inventory_hostname TEXT")
        if "wireguard_peer_ip" not in target_columns:
            cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN wireguard_peer_ip TEXT")
        if "resolved_connection" not in target_columns:
            cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN resolved_connection TEXT")
        if "resolution_status" not in target_columns:
            cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN resolution_status TEXT")
        if "resolution_reason" not in target_columns:
            cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN resolution_reason TEXT")
        if "resolved_from_filter_ids_json" not in target_columns:
            cur.execute("ALTER TABLE scheduled_job_run_targets ADD COLUMN resolved_from_filter_ids_json TEXT")
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_scheduled_job_run_targets_run
                ON scheduled_job_run_targets(run_id)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_scheduled_job_run_targets_filter
                ON scheduled_job_run_targets(resolved_from_filter_id)
            """
        )
        cur.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_scheduled_job_run_targets_host
                ON scheduled_job_run_targets(hostname)
            """
        )
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure scheduled job support tables: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()


def _ensure_workflow_runtime_tables(conn: sqlite3.Connection, *, logger: Optional[logging.Logger]) -> None:
    cur = conn.cursor()
    try:
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS workflow_runs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                workflow_guid TEXT NOT NULL,
                workflow_name TEXT,
                source_type TEXT NOT NULL,
                source_metadata_json TEXT,
                graph_snapshot_json TEXT NOT NULL,
                status TEXT NOT NULL,
                error TEXT,
                skip_reason TEXT,
                final_payload_json TEXT,
                final_metadata_json TEXT,
                parent_workflow_run_id INTEGER,
                parent_node_id TEXT,
                scheduled_job_id INTEGER,
                scheduled_job_run_id INTEGER,
                webhook_id INTEGER,
                created_by TEXT,
                created_at INTEGER NOT NULL,
                started_ts INTEGER,
                finished_ts INTEGER,
                updated_at INTEGER NOT NULL
            )
            """
        )
        cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_runs_guid ON workflow_runs(workflow_guid)")
        cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_runs_status ON workflow_runs(status)")
        cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_runs_created ON workflow_runs(created_at)")

        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS workflow_node_runs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                workflow_run_id INTEGER NOT NULL,
                node_id TEXT NOT NULL,
                node_type TEXT,
                node_label TEXT,
                node_snapshot_json TEXT,
                status TEXT NOT NULL,
                skip_reason TEXT,
                error TEXT,
                timeout_seconds INTEGER,
                input_envelope_json TEXT,
                output_envelope_json TEXT,
                ignored_inputs_json TEXT,
                linked_child_summary_json TEXT,
                created_at INTEGER NOT NULL,
                started_ts INTEGER,
                finished_ts INTEGER,
                updated_at INTEGER NOT NULL,
                FOREIGN KEY(workflow_run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_node_runs_run ON workflow_node_runs(workflow_run_id)")
        cur.execute(
            "CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_node_runs_identity ON workflow_node_runs(workflow_run_id, node_id)"
        )

        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS workflow_child_jobs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                workflow_run_id INTEGER NOT NULL,
                workflow_node_run_id INTEGER NOT NULL,
                child_kind TEXT NOT NULL,
                child_identifier TEXT,
                activity_id INTEGER,
                child_workflow_run_id INTEGER,
                target_hostname TEXT,
                component_guid TEXT,
                component_name TEXT,
                component_kind TEXT,
                status TEXT,
                stdout_summary TEXT,
                stderr_summary TEXT,
                payload_json TEXT,
                created_at INTEGER NOT NULL,
                updated_at INTEGER NOT NULL,
                FOREIGN KEY(workflow_run_id) REFERENCES workflow_runs(id) ON DELETE CASCADE,
                FOREIGN KEY(workflow_node_run_id) REFERENCES workflow_node_runs(id) ON DELETE CASCADE
            )
            """
        )
        cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_child_jobs_run ON workflow_child_jobs(workflow_run_id)")
        cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_child_jobs_node ON workflow_child_jobs(workflow_node_run_id)")

        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS workflow_webhooks (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                workflow_guid TEXT NOT NULL,
                opaque_token TEXT NOT NULL UNIQUE,
                created_at INTEGER NOT NULL,
                creator_username TEXT,
                creator_role TEXT,
                last_used_at INTEGER
            )
            """
        )
        cur.execute("CREATE INDEX IF NOT EXISTS idx_workflow_webhooks_guid ON workflow_webhooks(workflow_guid)")
    except Exception as exc:
        if logger:
            logger.error("Failed to ensure workflow runtime tables: %s", exc, exc_info=True)
        else:
            raise
    finally:
        cur.close()
