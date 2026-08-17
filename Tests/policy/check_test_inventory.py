#!/usr/bin/env python3
"""Validate and query Borealis Engine Python test-domain inventory."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import sys


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_MANIFEST = REPO_ROOT / "Tests/manifests/engine-test-domains.json"
TEST_NAMES = ("test_*.py", "*_test.py")


def fail(message: str) -> None:
    print(f"TEST INVENTORY FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


def load_manifest(path: Path) -> dict:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        fail(f"cannot read {path.relative_to(REPO_ROOT)}: {exc}")
    if not isinstance(data, dict):
        fail("manifest root must be an object")
    return data


def discovered_tests(test_root: Path) -> set[str]:
    found: set[str] = set()
    for pattern in TEST_NAMES:
        for path in test_root.rglob(pattern):
            if path.is_file():
                found.add(path.relative_to(REPO_ROOT).as_posix())
    return found


def validate(data: dict, docs_path: Path | None = None) -> dict[str, list[str]]:
    test_root_value = data.get("test_root")
    runtime_owner = data.get("runtime_owner")
    domains_value = data.get("domains")
    all_only_value = data.get("all_only", [])
    if not isinstance(test_root_value, str) or not test_root_value:
        fail("test_root must be non-empty string")
    if runtime_owner != "site-worker":
        fail("runtime_owner must be site-worker; Go-owned tests belong in Go suites")
    if not isinstance(domains_value, dict) or not domains_value:
        fail("domains must be non-empty object")
    if not isinstance(all_only_value, list) or not all(isinstance(item, str) for item in all_only_value):
        fail("all_only must be string list")

    root = REPO_ROOT / test_root_value
    if not root.is_dir():
        fail(f"test root missing: {test_root_value}")

    domain_tests: dict[str, list[str]] = {}
    owners: dict[str, list[str]] = {}
    for domain, entry in domains_value.items():
        if not isinstance(domain, str) or not domain or domain in {"all", "webui", "go"}:
            fail(f"invalid Python domain name {domain!r}")
        if not isinstance(entry, dict):
            fail(f'domain "{domain}" must be object')
        description = entry.get("description")
        tests = entry.get("tests")
        if not isinstance(description, str) or not description.strip():
            fail(f'domain "{domain}" needs description')
        if not isinstance(tests, list) or not tests or not all(isinstance(item, str) for item in tests):
            fail(f'domain "{domain}" must resolve to non-empty string list')
        if len(tests) != len(set(tests)):
            fail(f'domain "{domain}" contains duplicate test paths')
        domain_tests[domain] = sorted(tests)
        for test_path in tests:
            candidate = REPO_ROOT / test_path
            if not candidate.is_file():
                fail(f'domain "{domain}" references missing {test_path}')
            owners.setdefault(test_path, []).append(domain)

    for test_path in all_only_value:
        candidate = REPO_ROOT / test_path
        if not candidate.is_file():
            fail(f"all-only path missing: {test_path}")
        owners.setdefault(test_path, []).append("all-only")

    discovered = discovered_tests(root)
    inventoried = set(owners)
    missing = sorted(discovered - inventoried)
    stale = sorted(inventoried - discovered)
    if missing:
        fail("unowned test files: " + ", ".join(missing))
    if stale:
        fail("inventoried paths are not tests: " + ", ".join(stale))

    if docs_path is not None:
        docs = docs_path.read_text(encoding="utf-8")
        undocumented = [domain for domain in sorted(domain_tests) if f"`{domain}`" not in docs]
        if undocumented:
            fail("domains absent from testing docs: " + ", ".join(undocumented))

    return domain_tests


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=Path, default=DEFAULT_MANIFEST)
    parser.add_argument("--list-domains", action="store_true")
    parser.add_argument("--list-files", metavar="DOMAIN")
    parser.add_argument("--check-docs", type=Path)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    manifest_path = args.manifest if args.manifest.is_absolute() else REPO_ROOT / args.manifest
    docs_path = None
    if args.check_docs:
        docs_path = args.check_docs if args.check_docs.is_absolute() else REPO_ROOT / args.check_docs
    data = load_manifest(manifest_path)
    domains = validate(data, docs_path)
    if args.list_domains:
        print("all")
        print("go")
        for domain in sorted(domains):
            print(domain)
        print("webui")
    elif args.list_files:
        if args.list_files == "all":
            files = sorted({path for paths in domains.values() for path in paths} | set(data.get("all_only", [])))
        else:
            files = domains.get(args.list_files)
            if files is None:
                fail(f'unknown Python domain "{args.list_files}"')
        print("\n".join(files))
    else:
        count = len({path for paths in domains.values() for path in paths} | set(data.get("all_only", [])))
        print(f"TEST INVENTORY PASS: {count} Engine Python test files across {len(domains)} domains")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
