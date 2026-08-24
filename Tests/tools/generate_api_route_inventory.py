#!/usr/bin/env python3
"""Generate reviewed route contract and API documentation table from Go AST."""

from __future__ import annotations

import json
from pathlib import Path
import subprocess


ROOT = Path(__file__).resolve().parents[2]
GO = ROOT / "Dependencies/Go/go1.25.12/bin/go"
SOURCE_ROOT = "Data/Engine/Containers/api-backend/cmd/api-backend"
MANIFEST = ROOT / "Tests/manifests/api-routes.json"
DOC = ROOT / "Docs/Reference/Data and Schema/api-route-inventory.md"
PROBE_TEST = "Data/Engine/Containers/api-backend/cmd/api-backend/probe_contracts_test.go"
CONTROLLER_TEST = "Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller_test.go"
CLUSTER_TEST = "Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster_test.go"
AEGIS_CLUSTER_TEST = "Data/Engine/Containers/api-backend/cmd/api-backend/aegis_cluster_fanout_test.go"

REVIEWED_ROUTE_TESTS = {
    (pattern, "Data/Engine/Containers/api-backend/cmd/api-backend/server_cluster.go"): CLUSTER_TEST
    for pattern in (
        "GET /api/bootstrap/cluster/join/{id}/events",
        "GET /api/server/cluster",
        "GET /api/server/cluster/banner",
        "GET /api/server/cluster/events",
        "GET /api/server/cluster/releases",
        "POST /api/bootstrap/cluster/join",
        "POST /api/server/cluster/admissions/{id}/approve",
        "POST /api/server/cluster/enable",
        "POST /api/server/cluster/hmr/exit",
        "POST /api/server/cluster/hmr/start",
        "POST /api/server/cluster/invitations",
        "POST /api/server/cluster/membership/scale",
        "POST /api/server/cluster/nodes/{id}/maintenance",
        "POST /api/server/cluster/nodes/{id}/remove",
        "POST /api/server/cluster/operations/{id}/cancel",
        "POST /api/server/cluster/operations/{id}/retry",
        "POST /api/server/cluster/postgres/emergency-failover",
        "POST /api/server/cluster/postgres/switchover",
        "POST /api/server/cluster/updates",
    )
}
REVIEWED_ROUTE_TESTS.update(
    {
        (pattern, "Data/Engine/Containers/api-backend/cmd/api-backend/main.go"): PROBE_TEST
        for pattern in ("/startup", "/ready", "/live")
    }
)
REVIEWED_ROUTE_TESTS[(
    "POST /internal/cluster/aegis-key",
    "Data/Engine/Containers/api-backend/cmd/api-backend/aegis_cluster_fanout.go",
)] = AEGIS_CLUSTER_TEST
REVIEWED_ROUTE_TESTS.update(
    {
        (pattern, "Data/Engine/Containers/api-backend/cmd/api-backend/borealis_operator.go"): (
            "Data/Engine/Containers/api-backend/cmd/api-backend/borealis_operator_test.go"
        )
        for pattern in ("/startup", "/ready", "/live")
    }
)
REVIEWED_ROUTE_TESTS.update(
    {
        (pattern, "Data/Engine/Containers/api-backend/cmd/api-backend/cluster_controller.go"): CONTROLLER_TEST
        for pattern in ("GET /ready", "GET /live")
    }
)


def classify(pattern: str) -> tuple[str, str]:
    route_path = pattern.split(" ", 1)[-1]
    if route_path in {"/health", "/healthz", "/startup", "/ready", "/live"}:
        return "health", "none"
    if route_path.startswith("/v1/"):
        return "operator", "operator-hmac"
    if route_path.startswith("/api/internal/"):
        return "internal-scheduler", "internal-hmac"
    if route_path.startswith("/internal/cluster/"):
        return "internal-cluster", "mutual-tls"
    if route_path.startswith("/api/agent/"):
        if route_path.startswith("/api/agent/enroll/"):
            return "agent-enrollment", "public-enrollment-contract"
        return "agent", "device-token-dpop"
    if route_path.startswith("/api/bootstrap/") or route_path in {
        "/api/auth/login",
        "/api/auth/passkeys/authenticate/options",
        "/api/auth/passkeys/authenticate/verify",
    }:
        return "public-bootstrap", "bootstrap-contract"
    if route_path == "/":
        return "fallback", "shared-public-gate"
    return "operator-api", "operator-session"


def reviewed_evidence() -> dict[tuple[str, str], tuple[str | None, str | None]]:
    """Load route-specific test choices already accepted into inventory.

    New routes intentionally receive no automatic companion-file assignment.
    Policy then fails until author records focused test or reviewed exemption.
    """

    try:
        inventory = json.loads(MANIFEST.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {}
    if not isinstance(inventory, list):
        return {}
    evidence = {
        (entry["pattern"], entry["source"]): (entry.get("test"), entry.get("test_exemption"))
        for entry in inventory
        if isinstance(entry, dict) and isinstance(entry.get("pattern"), str) and isinstance(entry.get("source"), str)
    }
    for key, test_path in REVIEWED_ROUTE_TESTS.items():
        evidence[key] = (test_path, None)
    return evidence


def main() -> int:
    go_bin = GO if GO.is_file() else Path("go")
    proc = subprocess.run(
        [str(go_bin), "run", "Tests/tools/routeinventory/main.go", "--root", SOURCE_ROOT, "--repo", "."],
        cwd=ROOT,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
    )
    routes = json.loads(proc.stdout)
    evidence = reviewed_evidence()
    inventory = []
    for route in routes:
        route_class, auth = classify(route["pattern"])
        test_path, exemption = evidence.get((route["pattern"], route["source"]), (None, None))
        inventory.append(
            {
                **route,
                "class": route_class,
                "authentication": auth,
                "test": test_path,
                "test_exemption": exemption,
            }
        )
    MANIFEST.write_text(json.dumps(inventory, indent=2) + "\n", encoding="utf-8")

    lines = [
        "# Engine API Route Inventory",
        "",
        "This generated review surface lists routes registered by production Go backend. Route classes separate public operator, device, bootstrap, internal scheduler, operator bridge, health, and fallback contracts.",
        "",
        "Regenerate with `python3 Tests/tools/generate_api_route_inventory.py`, then run `python3 Tests/policy/check_api_routes.py`.",
        "",
        "| Route pattern | Class | Authentication | Source |",
        "| --- | --- | --- | --- |",
    ]
    for route in inventory:
        lines.append(
            f"| `{route['pattern']}` | `{route['class']}` | `{route['authentication']}` | `{route['source']}:{route['line']}` |"
        )
    lines.extend(
        [
            "",
            '??? example "Detailed Codex Breakdown"',
            "",
            "    - Inventory source: `Tests/manifests/api-routes.json`.",
            "    - AST extractor: `Tests/tools/routeinventory/main.go`.",
            "    - Policy: `Tests/policy/check_api_routes.py`.",
            "    - Go route registration remains authoritative; this page must not be edited independently from generated inventory.",
            "",
        ]
    )
    DOC.write_text("\n".join(lines), encoding="utf-8")
    print(f"Generated {len(inventory)} route contracts")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
