from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest

from Tests.helpers.changed_paths import MANIFEST as CI_MANIFEST, matches
from Tests.policy.check_postgres_inventory import (
    PACKAGE, audit_results, required_tests, source_tests, validate_inventory,
)


class PostgresInventoryTests(unittest.TestCase):
    def test_inventory_covers_current_database_tests(self):
        tests = required_tests()
        validate_inventory(tests, source_tests())
        self.assertTrue({
            "TestClusterMaintenanceStorePostgresRecoveryGates",
            "TestClusterCompletionPostgresPreservesAndClearsRecoveryState",
            "TestClusterOperationRetryPersistsFailedStepCheckpoint",
            "TestClusterControllerClaimPinsOperationActionImage",
        }.issubset(tests))

    def test_source_discovery_follows_helpers_and_ignores_comment(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "database_test.go").write_text('''package example
import ("os"; "testing")
func databaseURL() string { return os.Getenv("BOREALIS_TEST_DATABASE_URL") }
func prepareDB() string { return databaseURL() }
func TestIndirect(t *testing.T) { _ = prepareDB() }
func TestUnit(t *testing.T) { /* BOREALIS_TEST_DATABASE_URL */ }
''')
            self.assertEqual(source_tests(root), ["TestIndirect"])

    def test_source_discovery_resolves_package_helpers_without_name_collisions(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "database_test.go").write_text('''package example
import ("os"; "testing")
func databaseURL() string { return os.Getenv("BOREALIS_TEST_DATABASE_URL") }
type fixture struct{}
func (fixture) databaseURL() string { return "unit" }
func TestIndirect(t *testing.T) { _ = databaseURL() }
func TestMethod(t *testing.T) { _ = (fixture{}).databaseURL() }
func TestShadow(t *testing.T) { databaseURL := func() string { return "unit" }; _ = databaseURL() }
''')
            (root / "other_test.go").write_text('''package example
import "testing"
func TestCrossFile(t *testing.T) { _ = databaseURL() }
''')
            (root / "external_test.go").write_text('''package example_test
import "testing"
func databaseURL() string { return "unit" }
func TestExternal(t *testing.T) { _ = databaseURL() }
''')
            self.assertEqual(source_tests(root), ["TestCrossFile", "TestIndirect"])

    def test_missing_unregistered_and_empty_source_fail(self):
        for required, discovered in [(["TestGone"], []), ([], ["TestNew"]), ([], [])]:
            with self.subTest(required=required, discovered=discovered), self.assertRaises(ValueError):
                validate_inventory(required, discovered)

    def test_invalid_manifests_fail(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "manifest.json"
            for tests in [[], ["TestOne", "TestOne"], ["Test.*"], [12], "TestOne"]:
                path.write_text(json.dumps({"tests": tests}))
                with self.subTest(tests=tests), self.assertRaises(ValueError):
                    required_tests(path)

    def test_database_paths_select_runtime_and_inventory_changes(self):
        patterns = json.loads(CI_MANIFEST.read_text())["database"]
        prefix = "Data/Engine/Containers/api-backend/"
        for path in [
            prefix + "cmd/api-backend/cluster_controller.go",
            prefix + "cmd/api-backend/server_cluster.go",
            prefix + "cmd/api-backend/server_cluster_store.go",
            prefix + "cmd/api-backend/scheduler_manager.go",
            prefix + "cmd/api-backend/vpn_session_store.go",
            prefix + "cmd/api-backend/cluster_controller_test.go",
            prefix + "internal/db/sql.go",
            prefix + "go.mod", prefix + "go.sum",
            "Tests/manifests/postgres-tests.json",
            "Tests/policy/check_postgres_inventory.py",
            "Tests/tools/postgresinventory/main.go",
            "Tests/helpers/changed_paths.py",
        ]:
            with self.subTest(path=path):
                self.assertTrue(any(matches(path, pattern) for pattern in patterns))
        self.assertFalse(any(matches("Docs/index.md", pattern) for pattern in patterns))


class PostgresResultTests(unittest.TestCase):
    def audit(self, events):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "results.jsonl"
            path.write_text("".join(json.dumps({"Package": PACKAGE, **event}) + "\n" for event in events))
            audit_results(["TestOne"], path)

    def test_complete_run_passes(self):
        self.audit([
            {"Action": "start"}, {"Action": "run", "Test": "TestOne"},
            {"Action": "output", "Test": "TestOne", "Output": "diagnostic"},
            {"Action": "pass", "Test": "TestOne"}, {"Action": "pass"},
        ])

    def test_skip_failure_empty_truncated_and_wrong_package_fail(self):
        for events in [
            [], [{"Action": "pass"}],
            [{"Action": "run", "Test": "TestOne"}],
            [{"Action": "pass", "Test": "TestOne"}, {"Action": "pass"}],
            [{"Action": "run", "Test": "TestOne"}, {"Action": "skip", "Test": "TestOne"}, {"Action": "pass"}],
            [{"Action": "skip", "Test": "TestOne/required_subtest"}],
            [{"Action": "fail", "Test": "TestOne"}],
            [{"Action": "fail"}],
            [{"Action": "run", "Test": "TestUnexpected"}],
            [{"Action": "pass", "Package": "wrong/package"}],
        ]:
            with self.subTest(events=events), self.assertRaises(ValueError):
                self.audit(events)


if __name__ == "__main__":
    unittest.main()
