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


def classify(pattern: str) -> tuple[str, str]:
    route_path = pattern.split(" ", 1)[-1]
    if route_path in {"/health", "/healthz"}:
        return "health", "none"
    if route_path.startswith("/v1/"):
        return "operator", "operator-hmac"
    if route_path.startswith("/api/internal/"):
        return "internal-scheduler", "internal-hmac"
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
    return {
        (entry["pattern"], entry["source"]): (entry.get("test"), entry.get("test_exemption"))
        for entry in inventory
        if isinstance(entry, dict) and isinstance(entry.get("pattern"), str) and isinstance(entry.get("source"), str)
    }


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
