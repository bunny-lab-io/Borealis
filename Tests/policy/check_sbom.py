#!/usr/bin/env python3
"""Compare direct dependency declarations and pinned downloads with SBOM."""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SBOM_PATH = ROOT / "Docs/Reference/SBOM.md"
WEBUI = ROOT / "Data/Engine/Containers/webui-frontend/data/web-interface"


def go_requirements(path: Path) -> list[tuple[str, str, str]]:
    dependencies: list[tuple[str, str, str]] = []
    in_block = False
    for line in path.read_text(encoding="utf-8").splitlines():
        stripped = line.strip()
        if stripped == "require (":
            in_block = True
            continue
        if in_block and stripped == ")":
            in_block = False
            continue
        if in_block and stripped and not stripped.startswith("//"):
            fields = stripped.split()
            dependencies.append((fields[0], fields[1], path.relative_to(ROOT).as_posix()))
    return dependencies


def python_requirements(path: Path, seen: set[Path] | None = None) -> list[tuple[str, str, str]]:
    seen = seen or set()
    path = path.resolve()
    if path in seen:
        return []
    seen.add(path)
    dependencies: list[tuple[str, str, str]] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if stripped.startswith("-r "):
            dependencies.extend(python_requirements(path.parent / stripped[3:].strip(), seen))
            continue
        match = re.fullmatch(r"([^=<>!~;]+)==([^;]+)", stripped)
        if match:
            name = re.sub(r"\[.*\]", "", match.group(1)).strip()
            dependencies.append((name, match.group(2).strip(), path.relative_to(ROOT).as_posix()))
    return dependencies


def main() -> int:
    sbom = SBOM_PATH.read_text(encoding="utf-8")
    lower_sbom = sbom.lower()
    failures: list[str] = []

    for row_number, row in enumerate(sbom.splitlines(), 1):
        if row.startswith("|") and row.count("|") >= 4 and not row.startswith("| ---") and not row.startswith("| :---"):
            if row.startswith("| Service"):
                continue
            if "](http" not in row:
                failures.append(f"SBOM FORMAT FAIL: dependency row {row_number} lacks license hyperlink")

    for dockerfile in sorted((ROOT / "Data/Engine/Containers").glob("*/Dockerfile")):
        docker_text = dockerfile.read_text(encoding="utf-8")
        aliases = set(re.findall(r"^FROM\s+\S+\s+AS\s+(\S+)", docker_text, re.MULTILINE | re.IGNORECASE))
        for image in re.findall(r"^FROM\s+([^\s]+)", docker_text, re.MULTILINE):
            image = image.split("@", 1)[0]
            if image in aliases:
                continue
            if image.lower() not in lower_sbom:
                failures.append(f"SBOM IMAGE FAIL: {dockerfile.relative_to(ROOT)} uses {image}, absent from SBOM")

    go_dependencies = go_requirements(ROOT / "Data/Agent/go.mod")
    go_dependencies += go_requirements(ROOT / "Data/Engine/Containers/api-backend/go.mod")
    for name, version, source in go_dependencies:
        if f"{name} {version}".lower() not in lower_sbom:
            failures.append(f"SBOM GO FAIL: {source} declares {name} {version}")

    requirement_files = [
        ROOT / "Data/Engine/Containers/api-backend/data/engine-worker-requirements.txt",
        ROOT / "Tests/requirements-policy.txt",
        ROOT / "Tests/requirements-docs.txt",
    ]
    seen_python: set[tuple[str, str]] = set()
    for requirements in requirement_files:
        for name, version, source in python_requirements(requirements):
            key = (name.lower().replace("_", "-"), version)
            if key in seen_python:
                continue
            seen_python.add(key)
            flexible_name = re.escape(name).replace("_", "[-_]").replace(r"\-", "[-_]")
            pattern = re.compile(rf"\b{flexible_name}(?:\[[^]]+\])?\s+{re.escape(version)}\b", re.IGNORECASE)
            if not pattern.search(sbom):
                failures.append(f"SBOM PYTHON FAIL: {source} declares {name} {version}")

    package = json.loads((WEBUI / "package.json").read_text(encoding="utf-8"))
    lock = json.loads((WEBUI / "package-lock.json").read_text(encoding="utf-8"))
    lock_root = lock.get("packages", {}).get("", {})
    for section in ("dependencies", "devDependencies"):
        declared = package.get(section, {})
        if lock_root.get(section, {}) != declared:
            failures.append(f"SBOM NPM FAIL: package-lock root {section} differs from package.json")
        for name, version_policy in declared.items():
            normalized = version_policy.lstrip("~^")
            if name.lower() not in lower_sbom:
                failures.append(f"SBOM NPM FAIL: {name} absent from SBOM")
            elif re.fullmatch(r"\d+(?:\.\d+)+(?:[-+][0-9A-Za-z.-]+)?", normalized) and normalized not in sbom:
                failures.append(f"SBOM NPM FAIL: {name} {version_policy} absent from SBOM")

    pinned_tokens = (
        'GUM_VERSION="v0.17.0"',
        'K3S_LONGHORN_VERSION="${BOREALIS_K3S_LONGHORN_VERSION:-v1.12.0}"',
        'go_version="${BOREALIS_GO_VERSION:-1.22.12}"',
        'go_version="${BOREALIS_GO_VERSION:-1.25.12}"',
    )
    source_text = "\n".join(
        (ROOT / path).read_text(encoding="utf-8")
        for path in ("Engine.sh", "Data/Agent/build-agent.sh", "Data/Engine/Containers/api-backend/build-api-backend.sh")
    )
    for token in pinned_tokens:
        if token not in source_text:
            failures.append(f"SBOM PIN FAIL: expected pinned source token missing: {token}")
    for version in ("v0.17.0", "v1.12.0", "1.22.12", "1.25.12"):
        if version not in sbom:
            failures.append(f"SBOM PIN FAIL: pinned version {version} absent from SBOM")
    for checksum_file in (
        ROOT / "Data/Agent/build-agent.sh",
        ROOT / "Data/Engine/Containers/api-backend/build-api-backend.sh",
    ):
        content = checksum_file.read_text(encoding="utf-8")
        if len(re.findall(r"[A-Za-z0-9_]+sha256=\"[0-9a-f]{64}\"", content)) < 2:
            failures.append(f"SBOM DOWNLOAD FAIL: {checksum_file.relative_to(ROOT)} lacks per-architecture SHA256 pins")

    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    print(f"SBOM policy passed: {len(go_dependencies)} Go and {len(seen_python)} Python dependencies")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
