#!/usr/bin/env python3
"""Report whether Git changes intersect named CI path group."""

from __future__ import annotations

import argparse
import fnmatch
import json
import os
from pathlib import Path, PurePosixPath
import subprocess
import sys


REPO_ROOT = Path(__file__).resolve().parents[2]
MANIFEST = REPO_ROOT / "Tests/manifests/ci-paths.json"
GLOBAL_PATHS = {
    ".github/workflows/pr-validation.yml",
    "Tests/lib.sh",
    "Tests/run-all.sh",
    "Tests/manifests/ci-paths.json",
}


def matches(path: str, pattern: str) -> bool:
    path = PurePosixPath(path).as_posix().lstrip("./")
    pattern = PurePosixPath(pattern).as_posix().lstrip("./")
    if fnmatch.fnmatchcase(path, pattern):
        return True
    return pattern.endswith("/**") and (path == pattern[:-3].rstrip("/") or path.startswith(pattern[:-2]))


def changed(base: str, head: str) -> list[str]:
    proc = subprocess.run(
        ["git", "diff", "--name-only", "--diff-filter=ACDMRTUXB", base, head, "--"],
        cwd=REPO_ROOT,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if proc.returncode:
        print(f"CHANGED PATHS FAIL: {proc.stderr.strip()}", file=sys.stderr)
        raise SystemExit(1)
    return proc.stdout.splitlines()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--group", required=True)
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    args = parser.parse_args()
    if os.environ.get("BOREALIS_CI_FORCE_ALL", "").lower() == "true":
        print("true")
        return 0
    groups = json.loads(MANIFEST.read_text(encoding="utf-8"))
    if args.group not in groups:
        print(f"CHANGED PATHS FAIL: unknown group {args.group}", file=sys.stderr)
        return 2
    paths = changed(args.base, args.head)
    result = any(path in GLOBAL_PATHS for path in paths) or any(
        matches(path, pattern) for path in paths for pattern in groups[args.group]
    )
    print("true" if result else "false")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
