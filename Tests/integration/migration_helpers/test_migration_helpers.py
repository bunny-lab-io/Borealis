#!/usr/bin/env python3
"""Command-contract tests for high-impact Engine migration helpers."""

from __future__ import annotations

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[3]
IMPORT = ROOT / "Data/Engine/Containers/import-legacy-postgres-dump.sh"
STERILIZE = ROOT / "Data/Engine/Containers/sterilize-systemd-runtime.sh"


def executable(path: Path, source: str) -> None:
    path.write_text(textwrap.dedent(source).lstrip(), encoding="utf-8")
    path.chmod(0o755)


class ImportLegacyDumpTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = Path(tempfile.mkdtemp(prefix="borealis-import-helper-"))
        self.bin = self.temp / "bin"
        self.bin.mkdir()
        self.log = self.temp / "commands.log"
        self.kubeconfig = self.temp / "k3s.yaml"
        self.kubeconfig.write_text("fixture", encoding="utf-8")
        self.dump = self.temp / "legacy.sql"
        self.dump.write_text("SELECT 1;\n", encoding="utf-8")
        for command in ("bash", "cat", "date", "dirname"):
            source = shutil.which(command)
            if source is None:
                self.fail(f"required test command missing: {command}")
            (self.bin / command).symlink_to(source)

    def tearDown(self) -> None:
        shutil.rmtree(self.temp)

    def run_helper(self, *args: str, with_k3s: bool = True) -> subprocess.CompletedProcess[str]:
        if with_k3s:
            executable(
                self.bin / "k3s",
                """
                #!/bin/sh
                printf '%s\n' "$*" >> "$BOREALIS_TEST_COMMAND_LOG"
                case "$*" in
                  *" get pod postgres-db-0"*) exit 0 ;;
                  *" exec -i postgres-db-0 -c postgres-db -- sh -lc "*) cat > "$BOREALIS_TEST_IMPORTED_SQL"; exit 0 ;;
                esac
                exit 1
                """,
            )
        env = {
            **os.environ,
            "PATH": str(self.bin),
            "BOREALIS_TEST_COMMAND_LOG": str(self.log),
            "BOREALIS_TEST_IMPORTED_SQL": str(self.temp / "imported.sql"),
            "K3S_KUBECONFIG": str(self.kubeconfig),
            "BOREALIS_K3S_NAMESPACE": "fixture-namespace",
        }
        return subprocess.run([str(IMPORT), *args], text=True, capture_output=True, env=env)

    def test_missing_argument_dump_and_kubeconfig_fail(self) -> None:
        self.assertNotEqual(self.run_helper().returncode, 0)
        self.assertNotEqual(self.run_helper(str(self.temp / "missing.sql")).returncode, 0)
        self.kubeconfig.unlink()
        result = self.run_helper(str(self.dump))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("K3s kubeconfig missing", result.stderr)

    def test_missing_kubernetes_client_fails_clearly(self) -> None:
        result = self.run_helper(str(self.dump), with_k3s=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("k3s or kubectl missing", result.stderr)

    def test_namespace_kubeconfig_pod_container_and_on_error_contract(self) -> None:
        result = self.run_helper(str(self.dump))
        self.assertEqual(result.returncode, 0, result.stderr)
        commands = self.log.read_text(encoding="utf-8")
        self.assertIn(f"--kubeconfig {self.kubeconfig}", commands)
        self.assertIn("-n fixture-namespace get pod postgres-db-0", commands)
        self.assertIn("exec -i postgres-db-0 -c postgres-db", commands)
        self.assertIn("psql -v ON_ERROR_STOP=1", commands)
        self.assertEqual((self.temp / "imported.sql").read_text(encoding="utf-8"), "SELECT 1;\n")


class SterilizeRuntimeTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = Path(tempfile.mkdtemp(prefix="borealis-sterilize-helper-"))
        self.repo = self.temp / "repo"
        self.runtime = self.repo / "Engine"
        self.backup = self.repo / "Engine.old"
        self.bin = self.temp / "bin"
        self.log = self.temp / "commands.log"
        self.repo.mkdir()
        self.runtime.mkdir()
        (self.runtime / "runtime-marker").write_text("preserve", encoding="utf-8")
        (self.repo / ".gitignore").write_text("/Engine/\n", encoding="utf-8")
        self.bin.mkdir()
        executable(
            self.bin / "systemctl",
            """
            #!/bin/sh
            printf 'systemctl %s\n' "$*" >> "$BOREALIS_TEST_COMMAND_LOG"
            exit 0
            """,
        )
        executable(
            self.bin / "pg_dump",
            """
            #!/bin/sh
            printf 'pg_dump %s\n' "$*" >> "$BOREALIS_TEST_COMMAND_LOG"
            output=""
            while [ "$#" -gt 0 ]; do
              if [ "$1" = "-f" ]; then output="$2"; break; fi
              shift
            done
            : > "$output"
            if [ "${BOREALIS_TEST_DUMP_FAIL:-0}" = "1" ]; then exit 1; fi
            printf '%s\n' '-- PostgreSQL fixture dump' > "$output"
            exit 0
            """,
        )
        executable(
            self.bin / "runuser",
            """
            #!/bin/sh
            printf 'runuser %s\n' "$*" >> "$BOREALIS_TEST_COMMAND_LOG"
            exit 1
            """,
        )
        executable(
            self.bin / "ip",
            """
            #!/bin/sh
            printf 'ip %s\n' "$*" >> "$BOREALIS_TEST_COMMAND_LOG"
            exit 0
            """,
        )
        executable(
            self.bin / "iptables",
            """
            #!/bin/sh
            printf 'iptables %s\n' "$*" >> "$BOREALIS_TEST_COMMAND_LOG"
            exit 0
            """,
        )

    def tearDown(self) -> None:
        shutil.rmtree(self.temp)

    def run_helper(self, *, dump_fail: bool = False) -> subprocess.CompletedProcess[str]:
        env = {
            **os.environ,
            "PATH": f"{self.bin}:/usr/bin:/bin",
            "BOREALIS_TEST_COMMAND_LOG": str(self.log),
            "BOREALIS_MIGRATION_TEST_MODE": "1",
            "BOREALIS_MIGRATION_REPO_ROOT": str(self.repo),
            "BOREALIS_MIGRATION_RUNTIME_ROOT": str(self.runtime),
            "BOREALIS_MIGRATION_BACKUP_ROOT": str(self.backup),
            "BOREALIS_TEST_DUMP_FAIL": "1" if dump_fail else "0",
        }
        return subprocess.run([str(STERILIZE)], text=True, capture_output=True, env=env)

    def test_existing_backup_refuses_before_commands(self) -> None:
        self.backup.mkdir()
        result = self.run_helper()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("refusing to overwrite", result.stderr)
        self.assertFalse(self.log.exists())

    def test_dump_precedes_units_and_runtime_is_renamed_not_deleted(self) -> None:
        result = self.run_helper()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.runtime.exists())
        self.assertEqual((self.backup / "runtime-marker").read_text(encoding="utf-8"), "preserve")
        dump_files = list((self.backup / "Deploy").glob("legacy-postgres-borealis-*.sql"))
        self.assertEqual(len(dump_files), 1)
        self.assertGreater(dump_files[0].stat().st_size, 0)
        commands = self.log.read_text(encoding="utf-8")
        self.assertLess(commands.index("pg_dump"), commands.index("systemctl"))
        self.assertIn("ip link delete dev borealis-wg", commands)
        self.assertNotIn("wg0", commands)
        ignored = (self.repo / ".gitignore").read_text(encoding="utf-8")
        self.assertEqual(ignored.count("/Engine.old/"), 1)

    def test_failed_dump_does_not_leave_empty_artifact(self) -> None:
        result = self.run_helper(dump_fail=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(list((self.backup / "Deploy").glob("legacy-postgres-borealis-*.sql")), [])


if __name__ == "__main__":
    unittest.main()
