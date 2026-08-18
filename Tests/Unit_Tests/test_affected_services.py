from __future__ import annotations

import json
from contextlib import redirect_stdout
from io import StringIO
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

from Tests.helpers.affected_services import affected_services, main


REPO_ROOT = Path(__file__).resolve().parents[2]
SERVICES = json.loads((REPO_ROOT / "Data/Engine/Containers/build-manifest.json").read_text(encoding="utf-8"))["services"]


class AffectedServicesTests(unittest.TestCase):
    def test_service_local_change(self) -> None:
        self.assertEqual(affected_services(["Data/Engine/Containers/traefik-edge/entrypoint.sh"], SERVICES), ["traefik-edge"])

    def test_agent_change_rebuilds_consumers(self) -> None:
        self.assertEqual(
            affected_services(["Data/Agent/internal/config/config.go"], SERVICES),
            ["api-backend", "site-worker"],
        )

    def test_engine_go_change_rebuilds_multi_role_binary_consumers(self) -> None:
        self.assertEqual(
            affected_services(["Data/Engine/Containers/api-backend/cmd/api-backend/main.go"], SERVICES),
            ["api-backend", "borealis-operator", "job-scheduler"],
        )

    def test_python_change_rebuilds_python_consumers(self) -> None:
        self.assertEqual(
            affected_services(["Data/Engine/Containers/site-worker/data/database.py"], SERVICES),
            ["site-worker"],
        )

    def test_webui_change_only_rebuilds_webui(self) -> None:
        self.assertEqual(
            affected_services(["Data/Engine/Containers/webui-frontend/data/web-interface/src/main.jsx"], SERVICES),
            ["webui-frontend"],
        )

    def test_manifest_or_engine_controller_change_rebuilds_all(self) -> None:
        expected = sorted(SERVICES)
        self.assertEqual(affected_services(["Data/Engine/Containers/build-manifest.json"], SERVICES), expected)
        self.assertEqual(affected_services(["Engine.sh"], SERVICES), expected)

    def test_deleted_input_path_still_matches(self) -> None:
        self.assertEqual(
            affected_services(["Data/Engine/Containers/site-worker/removed-entrypoint.sh"], SERVICES),
            ["site-worker"],
        )

    def test_unknown_path_has_no_images(self) -> None:
        self.assertEqual(affected_services(["Docs/index.md"], SERVICES), [])

    def test_cli_emits_no_empty_service_for_unknown_path(self) -> None:
        output = StringIO()
        with patch.object(sys, "argv", ["affected_services.py", "--file", "Docs/index.md"]), redirect_stdout(output):
            self.assertEqual(main(), 0)
        self.assertEqual(output.getvalue(), "")


if __name__ == "__main__":
    unittest.main()
