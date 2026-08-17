#!/usr/bin/env python3
"""Validate Engine build-manifest coverage and required inputs."""

from __future__ import annotations

import argparse
import fnmatch
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_MANIFEST = REPO_ROOT / "Data/Engine/Containers/build-manifest.json"


def fail(message: str) -> None:
    print(f"BUILD MANIFEST FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


def load_manifest(path: Path) -> dict:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        fail(f"cannot read manifest: {exc}")
    services = data.get("services") if isinstance(data, dict) else None
    if not isinstance(services, dict) or not services:
        fail("services must be non-empty object")
    return services


def build_roles() -> list[str]:
    source = (REPO_ROOT / "Engine.sh").read_text(encoding="utf-8")
    match = re.search(r"^BUILD_ROLES=\(\n(?P<body>.*?)^\)", source, re.MULTILINE | re.DOTALL)
    if not match:
        fail("cannot parse Engine.sh BUILD_ROLES")
    return re.findall(r'^\s*"([^"]+)"\s*$', match.group("body"), re.MULTILINE)


def normalize_input(raw: object, service: str) -> tuple[str, bool]:
    if isinstance(raw, str):
        return raw, False
    if isinstance(raw, dict) and isinstance(raw.get("pattern"), str) and isinstance(raw.get("optional", False), bool):
        return raw["pattern"], raw.get("optional", False)
    fail(f"{service} input must be string or pattern/optional object")


def tracked_files() -> list[str]:
    proc = subprocess.run(
        ["git", "ls-files", "-z"],
        cwd=REPO_ROOT,
        check=True,
        stdout=subprocess.PIPE,
    )
    return [item.decode("utf-8") for item in proc.stdout.split(b"\0") if item]


TRACKED_FILES = tracked_files()


def pattern_matches(path: str, pattern: str) -> bool:
    if fnmatch.fnmatchcase(path, pattern):
        return True
    return pattern.endswith("/**") and (path == pattern[:-3].rstrip("/") or path.startswith(pattern[:-2]))


def matching_files(pattern: str) -> list[Path]:
    return [REPO_ROOT / path for path in TRACKED_FILES if pattern_matches(path, pattern)]


def validate_service(name: str, entry: object) -> list[str]:
    if not isinstance(entry, dict):
        fail(f"{name} entry must be object")
    dockerfile = entry.get("dockerfile")
    context = entry.get("context")
    inputs = entry.get("inputs")
    if not isinstance(dockerfile, str) or not dockerfile:
        fail(f"{name} dockerfile missing")
    if not isinstance(context, str) or not context:
        fail(f"{name} context missing")
    if not isinstance(inputs, list) or not inputs:
        fail(f"{name} inputs must be non-empty list")
    if not (REPO_ROOT / dockerfile).is_file():
        fail(f"{name} Dockerfile missing: {dockerfile}")
    if not (REPO_ROOT / context).is_dir():
        fail(f"{name} context missing: {context}")

    patterns: list[str] = []
    for raw in inputs:
        pattern, optional = normalize_input(raw, name)
        path = PurePosixPath(pattern)
        if path.is_absolute() or ".." in path.parts:
            fail(f"{name} input escapes repository: {pattern}")
        if pattern in patterns:
            fail(f"{name} duplicates input pattern: {pattern}")
        patterns.append(pattern)
        if not optional and not matching_files(pattern):
            fail(f"{name} input {pattern} matched no files")

    targets = entry.get("targets")
    if targets is not None:
        if not isinstance(targets, dict) or not targets:
            fail(f"{name} targets must be non-empty object")
        docker_source = (REPO_ROOT / dockerfile).read_text(encoding="utf-8")
        available = set(re.findall(r"^FROM\s+\S+\s+AS\s+(\S+)\s*$", docker_source, re.MULTILINE | re.IGNORECASE))
        missing = sorted(set(targets.values()) - available)
        if missing:
            fail(f"{name} Dockerfile lacks targets: {', '.join(missing)}")
    return patterns


def service_hash(entry: dict) -> str:
    digest = hashlib.sha256()
    normalized = [normalize_input(item, "hash")[0] for item in entry["inputs"]]
    files: dict[str, Path] = {}
    for pattern in normalized:
        for path in matching_files(pattern):
            files[path.relative_to(REPO_ROOT).as_posix()] = path
    for relative, path in sorted(files.items()):
        digest.update(relative.encode("utf-8") + b"\0")
        digest.update(hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def validate(services: dict) -> None:
    patterns_by_service = {name: validate_service(name, entry) for name, entry in services.items()}
    del patterns_by_service
    roles = build_roles()
    if set(roles) != set(services):
        missing = sorted(set(roles) - set(services))
        extra = sorted(set(services) - set(roles))
        fail(f"service set differs from BUILD_ROLES; missing={missing}, extra={extra}")
    dockerfiles = sorted(path.relative_to(REPO_ROOT).as_posix() for path in (REPO_ROOT / "Data/Engine/Containers").glob("*/Dockerfile"))
    represented = [entry["dockerfile"] for entry in services.values()]
    if sorted(represented) != dockerfiles:
        fail(f"Dockerfile coverage mismatch; repository={dockerfiles}, manifest={sorted(represented)}")
    if len(represented) != len(set(represented)):
        fail("Dockerfile represented more than once")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=Path, default=DEFAULT_MANIFEST)
    parser.add_argument("--hash", dest="hash_service")
    args = parser.parse_args()
    path = args.manifest if args.manifest.is_absolute() else REPO_ROOT / args.manifest
    services = load_manifest(path)
    validate(services)
    if args.hash_service:
        if args.hash_service not in services:
            fail(f"unknown service {args.hash_service}")
        print(service_hash(services[args.hash_service]))
    else:
        print(f"BUILD MANIFEST PASS: {len(services)} Dockerfiles and Engine BUILD_ROLES agree")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
