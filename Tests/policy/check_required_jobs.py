#!/usr/bin/env python3
"""Check applicable PR jobs and all audited database shards before merge."""

import json
import os
import sys

from Tests.helpers.engine_go_shard import LANES, receipt
from Tests.policy.check_postgres_inventory import ci_identity, required_shards, shard_receipt


# Keys are the path-selector outputs. Each listed job must run when selected.
SCOPE = {
    "agent": ("agent-linux", "agent-windows"),
    "engine_go": ("engine-go",),
    "engine_python": ("engine-python",),
    "webui": ("webui",),
    "database": ("database",),
    "runtime_policy": ("runtime-policy",),
    "containers": ("containers",),
    "migration": ("migration",),
    "docs": ("docs",),
}


def audit_jobs(needs, shards, identity):
    expected = {"changes": "success", "repository-policy": "success"}
    scope = needs["changes"]["outputs"]
    for group, jobs in SCOPE.items():
        if scope.get(group) not in {"true", "false"}:
            raise ValueError(f"missing or invalid scope for {group}")
        for job in jobs:
            expected[job] = "success" if scope[group] == "true" else "skipped"
    actual = {job: record["result"] for job, record in needs.items()}
    if actual != expected:
        raise ValueError(f"required job mismatch: expected {expected}, received {actual}")
    if scope["engine_go"] == "true":
        if needs["engine-go"]["outputs"] != {lane: receipt(identity, lane) for lane in LANES}:
            raise ValueError("missing, stale or mismatched Engine Go lane results")
    if scope["database"] == "true":
        receipts = needs["database"]["outputs"]
        expected_receipts = {
            f"shard_{number}": shard_receipt(identity, number, tests)
            for number, tests in enumerate(shards, start=1)
        }
        if receipts != expected_receipts:
            raise ValueError("missing, stale or mismatched PostgreSQL shard results")


if __name__ == "__main__":
    try:
        audit_jobs(json.loads(os.environ["BOREALIS_NEEDS_JSON"]), required_shards(), ci_identity())
    except (KeyError, OSError, ValueError) as exc:
        sys.exit(f"Required validation failed: {exc}")
    print("All applicable PR validation jobs passed.")
