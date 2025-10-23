import hashlib
import sqlite3
import unittest

from Data.Engine.repositories.sqlite import migrations


class MigrationTests(unittest.TestCase):
    def test_apply_all_creates_expected_tables(self) -> None:
        conn = sqlite3.connect(":memory:")
        try:
            migrations.apply_all(conn)
            cursor = conn.cursor()
            tables = {
                row[0]
                for row in cursor.execute(
                    "SELECT name FROM sqlite_master WHERE type='table'"
                )
            }

            self.assertIn("devices", tables)
            self.assertIn("refresh_tokens", tables)
            self.assertIn("enrollment_install_codes", tables)
            self.assertIn("device_approvals", tables)
            self.assertIn("scheduled_jobs", tables)
            self.assertIn("scheduled_job_runs", tables)
            self.assertIn("github_token", tables)
            self.assertIn("users", tables)

            cursor.execute(
                "SELECT username, role, password_sha512 FROM users WHERE LOWER(username)=LOWER(?)",
                ("admin",),
            )
            row = cursor.fetchone()
            self.assertIsNotNone(row)
            if row:
                self.assertEqual(row[0], "admin")
                self.assertEqual(row[1].lower(), "admin")
                self.assertEqual(row[2], hashlib.sha512(b"Password").hexdigest())
        finally:
            conn.close()

    def test_ensure_default_admin_promotes_existing_user(self) -> None:
        conn = sqlite3.connect(":memory:")
        try:
            conn.execute(
                """
                CREATE TABLE users (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    username TEXT UNIQUE NOT NULL,
                    display_name TEXT,
                    password_sha512 TEXT,
                    role TEXT,
                    last_login INTEGER,
                    created_at INTEGER,
                    updated_at INTEGER,
                    mfa_enabled INTEGER DEFAULT 0,
                    mfa_secret TEXT
                )
                """
            )
            conn.execute(
                "INSERT INTO users (username, display_name, password_sha512, role) VALUES (?, ?, ?, ?)",
                ("admin", "Custom", "hash", "user"),
            )
            conn.commit()

            migrations.ensure_default_admin(conn)

            cursor = conn.cursor()
            cursor.execute(
                "SELECT role, password_sha512 FROM users WHERE LOWER(username)=LOWER(?)",
                ("admin",),
            )
            role, password_hash = cursor.fetchone()
            self.assertEqual(role.lower(), "admin")
            self.assertEqual(password_hash, "hash")
        finally:
            conn.close()


if __name__ == "__main__":  # pragma: no cover - convenience for local runs
    unittest.main()
