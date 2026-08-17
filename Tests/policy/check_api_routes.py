#!/usr/bin/env python3
"""Compare production Go route registrations with reviewed contract inventory."""

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "Tests/manifests/api-routes.json"
DOC = ROOT / "Docs/Reference/Data and Schema/api-route-inventory.md"
SOURCE_ROOT = "Data/Engine/Containers/api-backend/cmd/api-backend"


def fail(message: str) -> None:
    print(f"API ROUTE FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


def go_binary() -> str:
    override = os.environ.get("BOREALIS_GO_BIN")
    candidates = [override, str(ROOT / "Dependencies/Go/go1.25.12/bin/go"), "go"]
    for candidate in candidates:
        if not candidate:
            continue
        if candidate == "go" or Path(candidate).is_file():
            return candidate
    fail("Go 1.25 toolchain unavailable for AST route extraction")


def source_routes() -> list[dict]:
    proc = subprocess.run(
        [go_binary(), "run", "Tests/tools/routeinventory/main.go", "--root", SOURCE_ROOT, "--repo", "."],
        cwd=ROOT,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if proc.returncode:
        fail(proc.stderr.strip() or "Go AST extractor failed")
    return json.loads(proc.stdout)


def main() -> int:
    try:
        inventory = json.loads(MANIFEST.read_text(encoding="utf-8"))
        docs = DOC.read_text(encoding="utf-8")
    except (OSError, json.JSONDecodeError) as exc:
        fail(f"cannot read contract surfaces: {exc}")
    if not isinstance(inventory, list) or not inventory:
        fail("inventory must be non-empty list")
    required = {"pattern", "source", "line", "class", "authentication", "test", "test_exemption"}
    for entry in inventory:
        if not isinstance(entry, dict) or set(entry) != required:
            fail(f"invalid inventory entry: {entry!r}")
        if not entry["class"] or not entry["authentication"]:
            fail(f"route lacks class/authentication: {entry['pattern']}")
        if not entry["test"] and not entry["test_exemption"]:
            fail(f"route lacks focused test or exemption: {entry['pattern']}")
        if entry["test"] and not (ROOT / entry["test"]).is_file():
            fail(f"route test missing: {entry['test']}")
        if f"`{entry['pattern']}`" not in docs:
            fail(f"{entry['pattern']} absent from API route documentation")

    source = source_routes()
    source_contract = [(item["pattern"], item["source"], item["line"]) for item in source]
    inventory_contract = [(item["pattern"], item["source"], item["line"]) for item in inventory]
    if source_contract != inventory_contract:
        source_set = set(source_contract)
        inventory_set = set(inventory_contract)
        fail(
            "source/inventory drift; add="
            + repr(sorted(source_set - inventory_set))
            + ", remove="
            + repr(sorted(inventory_set - source_set))
        )
    print(f"API ROUTE PASS: {len(inventory)} Go registrations classified, tested/exempted, and documented")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
