from __future__ import annotations

import unittest
from pathlib import Path

from Tests.policy.check_k3s_manifests import host_path_allowed
from Tests.tools.generate_api_route_inventory import reviewed_evidence


REPO_ROOT = Path(__file__).resolve().parents[2]


class K3sHostPathPolicyTests(unittest.TestCase):
    def test_allows_exact_directory_and_descendant(self) -> None:
        self.assertTrue(
            host_path_allowed(
                "api-backend",
                "/opt/Borealis/Engine/Services/api-backend/secrets",
            )
        )
        self.assertTrue(
            host_path_allowed(
                "api-backend",
                "/opt/Borealis/Engine/Services/api-backend/secrets/Certificates",
            )
        )

    def test_rejects_string_prefix_collision(self) -> None:
        self.assertFalse(
            host_path_allowed(
                "api-backend",
                "/opt/Borealis/Engine/Services/api-backend/secrets-copy",
            )
        )


class APIRouteEvidenceTests(unittest.TestCase):
    def test_inventory_contains_reviewed_route_specific_evidence(self) -> None:
        evidence = reviewed_evidence()
        self.assertIn(
            ("/health", "Data/Engine/Containers/api-backend/cmd/api-backend/main.go"),
            evidence,
        )

    def test_unknown_route_does_not_gain_companion_file_evidence(self) -> None:
        evidence = reviewed_evidence()
        self.assertNotIn(
            ("POST /api/new-route", "Data/Engine/Containers/api-backend/cmd/api-backend/main.go"),
            evidence,
        )


class PortableRunnerContractTests(unittest.TestCase):
    def test_full_runner_requests_every_container_image(self) -> None:
        runner = (REPO_ROOT / "Tests/run-all.sh").read_text(encoding="utf-8")
        self.assertIn('run-containers.sh" --all', runner)

    def test_container_runner_exposes_full_build_mode(self) -> None:
        runner = (REPO_ROOT / "Tests/run-containers.sh").read_text(encoding="utf-8")
        self.assertIn("--all) BUILD_ALL=1", runner)
        self.assertIn('SERVICES=("${ALL_SERVICES[@]}")', runner)


if __name__ == "__main__":
    unittest.main()
