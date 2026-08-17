#!/usr/bin/env python3
"""Protect completed Compose, Python API, and restricted-operator cutovers."""

from __future__ import annotations

import ast
from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[2]
PYTHON_ROOT = ROOT / "Data/Engine/Containers/api-backend/data"
ALLOWED_FLASK = "Data/Engine/Containers/api-backend/data/services/job_scheduler/worker_socket.py"


def fail(message: str) -> None:
    print(f"ARCHITECTURE CUTOVER FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


for removed in (
    "Data/Engine/Containers/api-backend/data/services/API",
    "Data/Engine/Containers/api-backend/data/services/WebUI",
):
    if (ROOT / removed).exists():
        fail(f"retired Python ownership path returned: {removed}")

flask_files: list[str] = []
for path in PYTHON_ROOT.rglob("*.py"):
    relative = path.relative_to(ROOT).as_posix()
    tree = ast.parse(path.read_text(encoding="utf-8"), filename=relative)
    for node in ast.walk(tree):
        if isinstance(node, ast.Call):
            name = ""
            if isinstance(node.func, ast.Name):
                name = node.func.id
            elif isinstance(node.func, ast.Attribute):
                name = node.func.attr
            if name in {"Flask", "Blueprint", "add_url_rule"}:
                flask_files.append(relative)
                break
if sorted(set(flask_files)) != [ALLOWED_FLASK]:
    fail(f"Python HTTP application ownership differs from allowlist: {sorted(set(flask_files))}")

api_entrypoint = (ROOT / "Data/Engine/Containers/api-backend/entrypoint.sh").read_text(encoding="utf-8")
scheduler_entrypoint = (ROOT / "Data/Engine/Containers/job-scheduler/entrypoint.sh").read_text(encoding="utf-8")
operator_dockerfile = (ROOT / "Data/Engine/Containers/borealis-operator/Dockerfile").read_text(encoding="utf-8")
if "exec /usr/local/bin/borealis-api-backend-go" not in api_entrypoint:
    fail("api-backend entrypoint no longer executes Go binary")
if 'exec /usr/local/bin/borealis-api-backend-go "${ROLE}"' not in scheduler_entrypoint:
    fail("job-scheduler entrypoint no longer executes Go binary with restricted role")
if 'ENTRYPOINT ["/usr/local/bin/borealis-api-backend-go", "borealis-operator"]' not in operator_dockerfile:
    fail("borealis-operator image no longer starts restricted Go operator mode")

for name, source in (("api-backend", api_entrypoint), ("job-scheduler", scheduler_entrypoint)):
    if not re.search(r"site-worker-orchestrator.*retired", source, re.DOTALL):
        fail(f"{name} entrypoint lacks retired site-worker-orchestrator rejection")
    if "kubectl" in source or "KUBECONFIG" in source:
        fail(f"{name} entrypoint gained direct Kubernetes access")

print("ARCHITECTURE CUTOVER PASS: Go API, retired Compose/Python ownership, and restricted operator boundary preserved")
