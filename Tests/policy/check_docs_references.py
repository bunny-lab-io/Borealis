#!/usr/bin/env python3
"""Validate current documentation source maps and testing contracts."""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
DOCS = ROOT / "Docs"
UNIT_DOC = DOCS / "Reference/Unit_Testing.md"
REGRESSIONS = DOCS / "Reference/testing-regressions.md"
DOMAIN_MANIFEST = ROOT / "Tests/manifests/engine-test-domains.json"
CLASSIFICATIONS = ROOT / "Tests/manifests/doc-path-classifications.json"
FORBIDDEN_CURRENT = (
    "Data/Engine/Containers/site-worker/data/services/API/",
    "Data/Engine/Containers/site-worker/data/services/WebUI/",
)
SOURCE_SECTION = re.compile(
    r"^\s*### (?:Source map|Implementation references|Where endpoints are defined|Test Locations|Source vs runtime)\s*$",
    re.IGNORECASE,
)
NEXT_SECTION = re.compile(r"^\s*### ")
BACKTICK = re.compile(r"`([^`]+)`")
SOURCE_PREFIXES = ("Data/", "Tests/", ".github/")
SOURCE_FILES = {"AGENTS.md", "Engine.sh", "Engine_Unit_Tests.sh", "Docs/zensical.toml"}


def fail(message: str, failures: list[str]) -> None:
    failures.append(message)


def clean_path(token: str) -> str | None:
    token = token.strip().rstrip(".,;:")
    if token.startswith("./"):
        token = token[2:]
    if "::" in token:
        token = token.split("::", 1)[0]
    if ":" in token and re.search(r":\d+$", token):
        token = token.rsplit(":", 1)[0]
    if any(mark in token for mark in ("<", ">", "*", "$", "{", "}", "|")):
        return None
    if " " in token or token.startswith("/"):
        return None
    if token.startswith(SOURCE_PREFIXES) or token in SOURCE_FILES:
        return token.rstrip("/")
    if token.startswith(("Engine/", "Agent/", "Dependencies/")):
        return token.rstrip("/")
    return None


def classified(path: str, prefixes: dict[str, str]) -> bool:
    return any((path == prefix.rstrip("/") or path.startswith(prefix)) and reason.strip() for prefix, reason in prefixes.items())


def check_source_sections(failures: list[str]) -> None:
    prefixes = json.loads(CLASSIFICATIONS.read_text(encoding="utf-8"))["prefixes"]
    for doc in sorted(DOCS.rglob("*.md")):
        lines = doc.read_text(encoding="utf-8").splitlines()
        active = False
        for number, line in enumerate(lines, 1):
            if SOURCE_SECTION.match(line):
                active = True
                continue
            if active and NEXT_SECTION.match(line):
                active = False
            if not active:
                continue
            for token in BACKTICK.findall(line):
                path = clean_path(token)
                if not path or classified(path, prefixes):
                    continue
                if not (ROOT / path).exists():
                    fail(f"DOC PATH FAIL: {doc.relative_to(ROOT)}:{number}: {path}", failures)


def check_testing_contract(failures: list[str]) -> None:
    unit_text = UNIT_DOC.read_text(encoding="utf-8")
    manifest = json.loads(DOMAIN_MANIFEST.read_text(encoding="utf-8"))
    expected_domains = {"all", "go", "webui", *manifest["domains"].keys()}
    documented_domains = set(re.findall(r"\| `([^`]+)` \|", unit_text))
    if documented_domains != expected_domains:
        fail(
            "DOC TEST FAIL: documented Engine domains differ from engine-test-domains.json: "
            f"missing={sorted(expected_domains - documented_domains)} extra={sorted(documented_domains - expected_domains)}",
            failures,
        )

    commands = set(re.findall(r"^\./([^ \n]+)", unit_text, re.MULTILINE))
    for command in sorted(commands):
        if not (ROOT / command).is_file():
            fail(f"DOC TEST FAIL: documented command missing: {command}", failures)

    for line_number, line in enumerate(REGRESSIONS.read_text(encoding="utf-8").splitlines(), 1):
        if not line.startswith("| REG-TEST-"):
            continue
        columns = [column.strip() for column in line.strip("|").split("|")]
        status = columns[3] if len(columns) > 3 else ""
        for token in BACKTICK.findall(line):
            path = clean_path(token)
            if path and path.startswith(("Data/", "Tests/")) and not (ROOT / path).exists() and status != "stale-test":
                fail(f"DOC REGRESSION FAIL: {REGRESSIONS.relative_to(ROOT)}:{line_number}: {path}", failures)


def main() -> int:
    failures: list[str] = []
    for doc in sorted(DOCS.rglob("*.md")):
        text = doc.read_text(encoding="utf-8")
        for forbidden in FORBIDDEN_CURRENT:
            if forbidden in text:
                fail(f"DOC CUTOVER FAIL: {doc.relative_to(ROOT)} references retired current path {forbidden}", failures)

    check_source_sections(failures)
    check_testing_contract(failures)
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    print("Documentation reference policy passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
