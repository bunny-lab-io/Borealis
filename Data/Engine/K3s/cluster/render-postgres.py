#!/usr/bin/env python3
"""Render CNPG with the deployed Engine PostgreSQL resource contract (no writes)."""

import json
import pathlib
import re
import sys


CAPACITY_LIMIT = 2**53 - 1


def cpu_milli(value):
    match = re.fullmatch(r"(0|[1-9][0-9]{0,8})(?:\.([0-9]{1,3}))?(m)?", value) if isinstance(value, str) else None
    if not match or (match[2] and match[3]):
        raise ValueError("unsupported CPU quantity")
    result = int(match[1]) if match[3] else int(match[1]) * 1000 + int((match[2] or "").ljust(3, "0"))
    if not 0 < result <= CAPACITY_LIMIT:
        raise ValueError("CPU quantity out of range")
    return result


def memory_bytes(value):
    match = re.fullmatch(r"([1-9][0-9]{0,15})(Ki|Mi|Gi|Ti|k|M|G|T)?", value) if isinstance(value, str) else None
    if not match:
        raise ValueError("unsupported memory quantity")
    factors = {None: 1, "Ki": 2**10, "Mi": 2**20, "Gi": 2**30, "Ti": 2**40, "k": 1000, "M": 10**6, "G": 10**9, "T": 10**12}
    result = int(match[1]) * factors[match[2]]
    if not 0 < result <= CAPACITY_LIMIT:
        raise ValueError("memory quantity out of range")
    return result


def source_resources(source):
    metadata = source["metadata"]
    labels = metadata["labels"]
    if (source["apiVersion"], source["kind"], metadata["namespace"], metadata["name"]) != ("apps/v1", "StatefulSet", "borealis", "postgres-db") or any(labels.get(key) != value for key, value in {
        "app.kubernetes.io/name": "postgres-db", "app.kubernetes.io/part-of": "borealis", "app.kubernetes.io/managed-by": "Engine.sh",
    }.items()):
        raise ValueError("expected Engine-owned postgres-db StatefulSet")
    containers = source["spec"]["template"]["spec"]["containers"]
    if len(containers) != 1 or containers[0]["name"] != "postgres-db":
        raise ValueError("expected one postgres-db container")
    resources = containers[0]["resources"]
    if set(resources) != {"requests", "limits"} or any(set(resources[key]) != {"cpu", "memory"} for key in resources):
        raise ValueError("explicit CPU/memory requests and limits required")
    for field, parse in (("cpu", cpu_milli), ("memory", memory_bytes)):
        if parse(resources["requests"][field]) > parse(resources["limits"][field]):
            raise ValueError("resource request exceeds limit")
    return resources


def render(template, source, instances):
    if instances not in (1, 3):
        raise ValueError("instance count must be one or three")
    resources = source_resources(source)
    replacements = {
        "  instances: 3\n": f"  instances: {instances}\n",
        "  # BOREALIS_POSTGRES_RESOURCES": "  resources: " + json.dumps(resources, sort_keys=True, separators=(",", ":")),
        "    # BOREALIS_SYNCHRONOUS_CONFIGURATION": "" if instances == 1 else "\n".join((
            "    synchronous:", "      method: any", "      number: 1", "      dataDurability: required",
        )),
    }
    for marker, replacement in replacements.items():
        if template.count(marker) != 1:
            raise ValueError("missing or repeated CNPG template marker")
        template = template.replace(marker, replacement)
    return template


def main():
    try:
        if len(sys.argv) != 3:
            raise ValueError("expected template path and instance count")
        raw = sys.stdin.buffer.read(1024 * 1024 + 1)
        if len(raw) > 1024 * 1024:
            raise ValueError("source StatefulSet exceeds size limit")
        result = render(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"), json.loads(raw), int(sys.argv[2]))
    except (ValueError, KeyError, TypeError, IndexError, AttributeError, OSError):
        # Do not echo source object fields, environment data or credentials.
        print("CNPG rendering failed: require Engine-owned postgres-db with explicit supported CPU/memory bounds and intact template.", file=sys.stderr)
        return 1
    sys.stdout.write(result)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
