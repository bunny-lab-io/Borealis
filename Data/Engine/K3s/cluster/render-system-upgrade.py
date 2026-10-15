#!/usr/bin/env python3
"""Validate source-owned controller bounds and render the pinned manifest offline."""

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



def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate resource field")
        result[key] = value
    return result


def resources(raw):
    value = json.loads(raw, object_pairs_hook=unique_object)
    if not isinstance(value, dict) or set(value) != {"requests", "limits"}:
        raise ValueError("explicit requests and limits required")
    if any(not isinstance(value[key], dict) or set(value[key]) != {"cpu", "memory"} for key in value):
        raise ValueError("explicit CPU and memory required")
    for field, parse in (("cpu", cpu_milli), ("memory", memory_bytes)):
        if parse(value["requests"][field]) > parse(value["limits"][field]):
            raise ValueError("request exceeds limit")
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def read_resources(path):
    with pathlib.Path(path).open("rb") as stream:
        raw = stream.read(4097)
    if len(raw) > 4096:
        raise ValueError("resource configuration exceeds bound")
    return resources(raw)


def render(manifest, bounds):
    # Caller verifies dependencies.lock checksum before this exact pinned edit.
    # Preserve all upstream objects, images, placement, strategy and permissions.
    marker = "        name: system-upgrade-controller\n"
    if manifest.count(marker) != 1 or "        resources:" in manifest:
        raise ValueError("unexpected pinned controller template")
    return manifest.replace(marker, marker + "        resources: " + resources(bounds) + "\n")


def render_cert_manager(manifest, bounds):
    return render_cert_manager_deployment(manifest, bounds, "cert-manager")


def render_cert_manager_webhook(manifest, bounds):
    return render_cert_manager_deployment(manifest, bounds, "cert-manager-webhook")


def render_cert_manager_cainjector(manifest, bounds):
    return render_cert_manager_deployment(manifest, bounds, "cert-manager-cainjector")


def render_cert_manager_deployment(manifest, bounds, name):
    # Fixed reviewed Deployments only; never rewrite unrelated CRD/resource keys.
    containers = {"cert-manager": "cert-manager-controller", "cert-manager-webhook": "cert-manager-webhook",
                  "cert-manager-cainjector": "cert-manager-cainjector"}
    if name not in containers:
        raise ValueError("unsupported cert-manager Deployment")
    identity = f"kind: Deployment\nmetadata:\n  name: {name}\n  namespace: cert-manager\n"
    documents = manifest.split("\n---\n")
    matches = [i for i, document in enumerate(documents) if identity in document]
    marker = f"        - name: {containers[name]}\n"
    if len(matches) != 1:
        raise ValueError("unexpected pinned cert-manager Deployment")
    index = matches[0]
    document = documents[index]
    if document.count(marker) != 1 or "          resources:" in document:
        raise ValueError("unexpected pinned cert-manager container")
    documents[index] = document.replace(marker, marker + "          resources: " + resources(bounds) + "\n")
    return "\n---\n".join(documents)


def main():
    try:
        if len(sys.argv) == 3 and sys.argv[1] == "config":
            result = read_resources(sys.argv[2])
        elif len(sys.argv) == 4 and sys.argv[1] == "manifest":
            result = render(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"), sys.argv[3])
        elif len(sys.argv) == 6 and sys.argv[1] == "cert-manager-manifest":
            controller = render_cert_manager(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"), sys.argv[3])
            webhook = render_cert_manager_webhook(controller, sys.argv[4])
            result = render_cert_manager_cainjector(webhook, sys.argv[5])
        else:
            raise ValueError("invalid renderer arguments")
    except (ValueError, KeyError, TypeError, OSError):
        print("Controller resource rendering failed: require valid source CPU/memory requests and limits and intact pinned template.", file=sys.stderr)
        return 1
    sys.stdout.write(result)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
