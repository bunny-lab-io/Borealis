#!/usr/bin/env python3
"""Protect completed Compose, Python API, and restricted-operator cutovers."""

from __future__ import annotations

import ast
from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[2]
PYTHON_ROOT = ROOT / "Data/Engine/Containers/site-worker/data"
ALLOWED_FLASK = "Data/Engine/Containers/site-worker/data/services/job_scheduler/worker_socket.py"


def fail(message: str) -> None:
    print(f"ARCHITECTURE CUTOVER FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


for removed in (
    "Data/Engine/Containers/site-worker/data/services/API",
    "Data/Engine/Containers/site-worker/data/services/WebUI",
    "Data/Engine/Containers/site-worker/data/assembly_management",
    "Data/Engine/Containers/site-worker/data/alembic",
    "Data/Engine/Containers/site-worker/data/auth/device_auth.py",
    "Data/Engine/Containers/site-worker/data/auth/dpop.py",
    "Data/Engine/Containers/site-worker/data/auth/rate_limit.py",
    "Data/Engine/Containers/site-worker/data/crypto",
    "Data/Engine/Containers/site-worker/data/enrollment",
    "Data/Engine/Containers/site-worker/data/integrations",
    "Data/Engine/Containers/site-worker/data/public_endpoints.py",
    "Data/Engine/Containers/site-worker/data/services/assemblies",
    "Data/Engine/Containers/site-worker/data/services/auth",
    "Data/Engine/Containers/site-worker/data/services/filters",
    "Data/Engine/Containers/site-worker/data/services/VPN",
    "Data/Engine/Containers/site-worker/data/services/aegis_cipher.py",
    "Data/Engine/Containers/site-worker/data/services/ansible/recap.py",
    "Data/Engine/Containers/site-worker/data/services/ansible/runtime_settings.py",
    "Data/Engine/Containers/site-worker/data/services/ansible/ssh_auth.py",
    "Data/Engine/Containers/site-worker/data/services/ansible/worker_dispatch.py",
    "Data/Engine/Containers/site-worker/data/services/job_scheduler/runtime_settings.py",
    "Data/Engine/Containers/site-worker/data/services/metadata_fields.py",
    "Data/Engine/Containers/site-worker/data/services/remote_ops/agent_routes.py",
    "Data/Engine/Containers/site-worker/data/services/remote_ops/worker_bridge.py",
    "Data/Engine/Containers/site-worker/data/security/certificates.py",
    "Data/Engine/Containers/site-worker/data/security/signing.py",
    "Data/Engine/Containers/wireguard-tunnel/control_server.py",
    "Data/Engine/Containers/wireguard-tunnel/control_client.py",
):
    candidate = ROOT / removed
    exists = candidate.is_file()
    if candidate.is_dir():
        exists = any(
            path.is_file() and "__pycache__" not in path.parts and path.suffix not in {".pyc", ".pyo"}
            for path in candidate.rglob("*")
        )
    if exists:
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
api_dockerfile = (ROOT / "Data/Engine/Containers/api-backend/Dockerfile").read_text(encoding="utf-8")
scheduler_entrypoint = (ROOT / "Data/Engine/Containers/job-scheduler/entrypoint.sh").read_text(encoding="utf-8")
operator_dockerfile = (ROOT / "Data/Engine/Containers/borealis-operator/Dockerfile").read_text(encoding="utf-8")
job_scheduler_dockerfile = (ROOT / "Data/Engine/Containers/job-scheduler/Dockerfile").read_text(encoding="utf-8")
wireguard_dockerfile = (ROOT / "Data/Engine/Containers/wireguard-tunnel/Dockerfile").read_text(encoding="utf-8")
if "exec /usr/local/bin/borealis-api-backend-go" not in api_entrypoint:
    fail("api-backend entrypoint no longer executes Go binary")
if any(path in api_dockerfile for path in ("api-backend/data", "site-worker/data")) or re.search(r"(?:^|\s)python3?(?:\s|$)", api_dockerfile, re.MULTILINE):
    fail("api-backend image regained Python source or runtime")
if 'exec /usr/local/bin/borealis-api-backend-go "${ROLE}"' not in scheduler_entrypoint:
    fail("job-scheduler entrypoint no longer executes Go binary with restricted role")
if 'ENTRYPOINT ["/usr/local/bin/borealis-api-backend-go", "borealis-operator"]' not in operator_dockerfile:
    fail("borealis-operator image no longer starts restricted Go operator mode")
for name, dockerfile in (("job-scheduler", job_scheduler_dockerfile), ("wireguard-tunnel", wireguard_dockerfile)):
    if re.search(r"(?:^|\s)python3?(?:\s|$)", dockerfile, re.MULTILINE):
        fail(f"{name} image regained Python runtime")

for name, source in (("api-backend", api_entrypoint), ("job-scheduler", scheduler_entrypoint)):
    if not re.search(r"site-worker-orchestrator.*retired", source, re.DOTALL):
        fail(f"{name} entrypoint lacks retired site-worker-orchestrator rejection")
    if "kubectl" in source or "KUBECONFIG" in source:
        fail(f"{name} entrypoint gained direct Kubernetes access")

print("ARCHITECTURE CUTOVER PASS: Go API, retired Compose/Python ownership, and restricted operator boundary preserved")
