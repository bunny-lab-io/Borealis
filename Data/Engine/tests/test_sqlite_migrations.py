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
        finally:
            conn.close()


if __name__ == "__main__":  # pragma: no cover - convenience for local runs
    unittest.main()
