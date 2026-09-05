#!/usr/bin/env python3
"""Validate required PostgreSQL Go tests and audit actual go test -json results."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "Tests/manifests/postgres-tests.json"
SOURCE = ROOT / "Data/Engine/Containers/api-backend/cmd/api-backend"
PACKAGE = "borealis/api-backend/cmd/api-backend"


def required_tests(path: Path = MANIFEST) -> list[str]:
    data = json.loads(path.read_text(encoding="utf-8"))
    tests = data.get("tests") if isinstance(data, dict) else None
    if not isinstance(tests, list) or not tests or any(
        not isinstance(test, str) or not re.fullmatch(r"Test[A-Z0-9_]\w*", test) for test in tests
    ):
        raise ValueError("inventory requires non-empty valid test names")
    if len(tests) != len(set(tests)):
        raise ValueError("duplicate required tests")
    return sorted(tests)


def source_tests(source: Path = SOURCE) -> list[str]:
    candidate = ROOT / "Dependencies/Go/go1.25.12/bin/go"
    go = os.environ.get("BOREALIS_GO_BIN") or (str(candidate) if candidate.is_file() else "go")
    result = subprocess.run(
        [go, "run", str(ROOT / "Tests/tools/postgresinventory/main.go"), "--root", str(source)],
        cwd=ROOT, env={**os.environ, "GOWORK": "off"}, check=True, text=True, capture_output=True,
    )
    return json.loads(result.stdout)


def validate_inventory(required: list[str], discovered: list[str]) -> None:
    missing = sorted(set(discovered) - set(required))
    stale = sorted(set(required) - set(discovered))
    if missing or stale or not discovered:
        raise ValueError(f"source/inventory drift: unregistered={missing}, missing={stale}")


def audit_results(required: list[str], path: Path) -> None:
    started: set[str] = set()
    passed: set[str] = set()
    package_passed = False
    for line in path.read_text(encoding="utf-8").splitlines():
        event = json.loads(line)
        if event.get("Package") != PACKAGE:
            raise ValueError(f"unexpected result package: {event.get('Package')!r}")
        action, test = event.get("Action"), event.get("Test", "")
        if action in {"skip", "fail"}:
            raise ValueError(f"required PostgreSQL result {action}: {test or PACKAGE}")
        if test and test.split("/", 1)[0] not in required:
            raise ValueError(f"unexpected test result: {test}")
        if test in required:
            if action == "run":
                started.add(test)
            elif action == "pass":
                passed.add(test)
        elif not test and action == "pass":
            package_passed = True
    if started != set(required) or passed != set(required) or not package_passed:
        raise ValueError(
            f"incomplete PostgreSQL run: not run={sorted(set(required) - started)}, "
            f"not passed={sorted(set(required) - passed)}, package_passed={package_passed}"
        )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--run-pattern", action="store_true")
    parser.add_argument("--results", type=Path)
    args = parser.parse_args()
    try:
        tests = required_tests()
        validate_inventory(tests, source_tests())
        if args.results:
            audit_results(tests, args.results)
        if args.run_pattern:
            print("^(" + "|".join(tests) + ")$")
        else:
            print(f"POSTGRES INVENTORY PASS: {len(tests)} required tests" + (" executed without skips" if args.results else ""))
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        detail = exc.stderr if isinstance(exc, subprocess.CalledProcessError) else str(exc)
        print(f"POSTGRES INVENTORY FAIL: {detail}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
