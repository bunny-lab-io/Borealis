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
SHARD_COUNT = 4


def required_shards(path: Path = MANIFEST) -> list[list[str]]:
    data = json.loads(path.read_text(encoding="utf-8"))
    shards = data.get("shards") if isinstance(data, dict) else None
    if not isinstance(shards, list) or len(shards) != SHARD_COUNT:
        raise ValueError(f"expected {SHARD_COUNT} PostgreSQL shards")
    seen = set()
    for shard in shards:
        if not isinstance(shard, list) or not shard:
            raise ValueError("empty or invalid PostgreSQL shard")
        for test in shard:
            if not isinstance(test, str) or not re.fullmatch(r"Test[A-Z0-9_]\w*", test):
                raise ValueError(f"invalid test name: {test!r}")
            if test in seen:
                raise ValueError(f"duplicate required test: {test}")
            seen.add(test)
    return shards


def required_tests(path: Path = MANIFEST) -> list[str]:
    return sorted(test for shard in required_shards(path) for test in shard)


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
    expected = set(required)
    package_started = package_passed = False
    with path.open(encoding="utf-8") as stream:
        for line in stream:
            event = json.loads(line)
            if event.get("Package") != PACKAGE:
                raise ValueError(f"unexpected result package: {event.get('Package')!r}")
            action, test = event.get("Action"), event.get("Test", "")
            if action in {"skip", "fail", "build-fail"}:
                raise ValueError(f"required PostgreSQL result {action}: {test or PACKAGE}")
            if test and test.partition("/")[0] not in expected:
                raise ValueError(f"unexpected test result: {test}")
            if not package_started:
                if test or action != "start":
                    raise ValueError("missing package start")
                package_started = True
                continue
            if action == "start" or package_passed:
                raise ValueError("duplicate package or result after completion")
            if not test:
                package_passed = action == "pass"
            elif action == "run":
                if test in started:
                    raise ValueError(f"duplicate test run: {test}")
                started.add(test)
            elif action == "pass":
                if test not in started or test in passed:
                    raise ValueError(f"unexpected test pass: {test}")
                passed.add(test)
    if expected - passed or started != passed or not package_passed:
        raise ValueError(
            f"incomplete PostgreSQL run: missing={sorted(expected - passed)}, "
            f"unfinished={sorted(started - passed)}, package_passed={package_passed}"
        )


def audit_python_results(directory: Path) -> None:
    schema = (directory / "postgres-integration.log").read_text(encoding="utf-8").splitlines()
    if not schema or not schema[-1].startswith("POSTGRES INTEGRATION PASS:"):
        raise ValueError("canonical PostgreSQL integration result missing")
    admission = (directory / "worker-admission-postgres.log").read_text(encoding="utf-8").splitlines()
    passed = [line.removeprefix("PASS PostgreSQL ") for line in admission if line.startswith("PASS PostgreSQL ")]
    if len(set(passed)) != 18 or len(passed) != 18 or json.loads(admission[-1]) != {
        "suite": "worker_admission_postgres", "passed": 18, "failed": 0, "skipped": 0, "cases": passed,
    }:
        raise ValueError("incomplete Python PostgreSQL admission evidence")


def ci_identity() -> list[str]:
    head = os.environ["BOREALIS_CI_HEAD"]
    checkout = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    if checkout != os.environ["GITHUB_SHA"] or not re.fullmatch(r"[0-9a-f]{40}", head):
        raise ValueError("unexpected CI checkout or head")
    return [os.environ["GITHUB_RUN_ID"], head, checkout]


def shard_receipt(identity: list[str], shard: int, tests: list[str]) -> str:
    return json.dumps([*identity, shard, sorted(tests)], separators=(",", ":"))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--run-pattern", action="store_true")
    parser.add_argument("--results", type=Path)
    parser.add_argument("--shard", type=int, choices=range(1, SHARD_COUNT + 1))
    parser.add_argument("--ci-receipt", action="store_true")
    args = parser.parse_args()
    try:
        shards = required_shards()
        tests = sorted(test for shard in shards for test in shard)
        validate_inventory(tests, source_tests())
        if args.shard:
            tests = shards[args.shard - 1]
        if args.results:
            audit_results(tests, args.results)
        if args.ci_receipt:
            if not args.shard or not args.results:
                raise ValueError("CI receipt requires audited shard results")
            audit_python_results(args.results.parent)
            receipt = shard_receipt(ci_identity(), args.shard, tests)
            with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
                print(f"shard_{args.shard}={receipt}", file=output)
        if args.run_pattern:
            print("^(" + "|".join(tests) + ")$")
        else:
            print(f"POSTGRES INVENTORY PASS: {len(tests)} required tests" + (" executed without skips" if args.results else ""))
    except (KeyError, OSError, ValueError, subprocess.CalledProcessError) as exc:
        detail = exc.stderr if isinstance(exc, subprocess.CalledProcessError) else str(exc)
        print(f"POSTGRES INVENTORY FAIL: {detail}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
