#!/usr/bin/env python3
"""Parse repository JSON/YAML and enforce executable entrypoint modes."""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    print("DATA POLICY FAIL: PyYAML missing; install Tests/requirements-policy.txt", file=sys.stderr)
    raise SystemExit(127)


ROOT = Path(__file__).resolve().parents[2]


def repository_files(patterns: list[str]) -> list[Path]:
    proc = subprocess.run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", *patterns],
        cwd=ROOT,
        check=True,
        stdout=subprocess.PIPE,
    )
    return [ROOT / raw.decode("utf-8") for raw in proc.stdout.split(b"\0") if raw]


def must_be_executable(path: Path) -> bool:
    relative = path.relative_to(ROOT).as_posix()
    return (
        relative in {"Engine.sh", "Engine_Unit_Tests.sh"}
        or relative.startswith("Tests/run-")
        or relative == "Tests/render-k3s-manifests.sh"
        or relative in {
            "Data/Agent/build-agent.sh",
            "Data/Agent/Unit_Tests/Agent_Unit_Tests.sh",
            "Data/Engine/Containers/api-backend/build-api-backend.sh",
            "Data/Engine/Containers/import-legacy-postgres-dump.sh",
            "Data/Engine/Containers/sterilize-systemd-runtime.sh",
        }
        or (relative.startswith("Data/Engine/Containers/") and path.name in {"entrypoint.sh", "healthcheck.sh"})
    )


def main() -> int:
    failures: list[str] = []
    json_files = repository_files(["*.json"])
    yaml_files = repository_files(["*.yml", "*.yaml"])
    for path in json_files:
        try:
            json.loads(path.read_text(encoding="utf-8"))
        except (OSError, UnicodeError, json.JSONDecodeError) as exc:
            failures.append(f"JSON FAIL: {path.relative_to(ROOT)}: {exc}")
    for path in yaml_files:
        try:
            list(yaml.safe_load_all(path.read_text(encoding="utf-8")))
        except (OSError, UnicodeError, yaml.YAMLError) as exc:
            failures.append(f"YAML FAIL: {path.relative_to(ROOT)}: {exc}")
    shell_files = repository_files(["*.sh"])
    for path in shell_files:
        if must_be_executable(path) and not os.access(path, os.X_OK):
            failures.append(f"EXECUTABLE FAIL: {path.relative_to(ROOT)} must have executable mode")
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    print(f"Data policy passed: {len(json_files)} JSON, {len(yaml_files)} YAML, executable entrypoints")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
