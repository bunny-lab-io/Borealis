#!/usr/bin/env python3
"""Resolve Engine images affected by explicit files or Git revisions."""

from __future__ import annotations

import argparse
import fnmatch
import json
from pathlib import Path, PurePosixPath
import subprocess
import sys


REPO_ROOT = Path(__file__).resolve().parents[2]
MANIFEST_PATH = REPO_ROOT / "Data/Engine/Containers/build-manifest.json"


def fail(message: str) -> None:
    print(f"AFFECTED SERVICES FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


def load_services() -> dict:
    try:
        return json.loads(MANIFEST_PATH.read_text(encoding="utf-8"))["services"]
    except (OSError, KeyError, json.JSONDecodeError) as exc:
        fail(f"cannot read build manifest: {exc}")


def input_pattern(raw: object) -> str:
    if isinstance(raw, str):
        return raw
    if isinstance(raw, dict) and isinstance(raw.get("pattern"), str):
        return raw["pattern"]
    fail(f"invalid manifest input {raw!r}")


def path_matches(path: str, pattern: str) -> bool:
    normalized = PurePosixPath(path).as_posix().lstrip("./")
    pattern = PurePosixPath(pattern).as_posix().lstrip("./")
    if fnmatch.fnmatchcase(normalized, pattern):
        return True
    if pattern.endswith("/**"):
        return normalized == pattern[:-3].rstrip("/") or normalized.startswith(pattern[:-2])
    return False


def affected_services(changed_files: list[str], services: dict | None = None) -> list[str]:
    services = services or load_services()
    normalized = sorted({PurePosixPath(path).as_posix().lstrip("./") for path in changed_files if path.strip()})
    if any(path in {"Data/Engine/Containers/build-manifest.json", "Engine.sh"} for path in normalized):
        return sorted(services)
    affected: list[str] = []
    for service, entry in sorted(services.items()):
        patterns = [input_pattern(item) for item in entry["inputs"]]
        if any(path_matches(path, pattern) for path in normalized for pattern in patterns):
            affected.append(service)
    return affected


def git_changed_files(base: str, head: str) -> list[str]:
    proc = subprocess.run(
        ["git", "diff", "--name-only", "--diff-filter=ACDMRTUXB", base, head, "--"],
        cwd=REPO_ROOT,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if proc.returncode:
        fail(proc.stderr.strip() or f"git diff failed for {base}..{head}")
    return [line for line in proc.stdout.splitlines() if line]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base")
    parser.add_argument("--head")
    parser.add_argument("--file", dest="files", action="append", default=[])
    parser.add_argument("--files-from", type=Path)
    parser.add_argument("--format", choices=("json", "lines", "github"), default="lines")
    args = parser.parse_args()
    files = list(args.files)
    if args.files_from:
        files.extend(args.files_from.read_text(encoding="utf-8").splitlines())
    if args.base or args.head:
        if not args.base or not args.head:
            fail("--base and --head must be supplied together")
        files.extend(git_changed_files(args.base, args.head))
    if not files and not sys.stdin.isatty():
        files.extend(sys.stdin.read().splitlines())
    affected = affected_services(files)
    if args.format == "json":
        print(json.dumps(affected))
    elif args.format == "github":
        print(json.dumps({"service": affected}, separators=(",", ":")))
    else:
        print("\n".join(affected))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
