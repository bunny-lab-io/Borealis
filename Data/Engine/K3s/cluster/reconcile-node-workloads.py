#!/usr/bin/env python3
"""Create or update Borealis application Deployments pinned to one Engine node."""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import pathlib
import re
import subprocess
import sys
from typing import Any


NAMESPACE = "borealis"
DEPLOYMENT_SERVICES = {
    "api-backend": "api-backend",
    "borealis-operator": "borealis-operator",
    "borealis-cluster-controller": "api-backend",
    "job-scheduler": "job-scheduler",
    "remote-desktop-guacd": "remote-desktop-guacd",
    "traefik-edge": "traefik-edge",
    "webui-frontend": "webui-frontend",
    "wireguard-tunnel": "wireguard-tunnel",
}
HOST_PORT_DEPLOYMENTS = {"traefik-edge", "wireguard-tunnel"}
NODE_RE = re.compile(r"^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$")
SHA_RE = re.compile(r"^[0-9a-f]{40}$")


def kubectl(*args: str, stdin: str | None = None) -> str:
    command = ["k3s", "kubectl", *args]
    result = subprocess.run(command, input=stdin, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    if result.returncode:
        raise RuntimeError(f"kubectl {' '.join(args)} failed: {result.stderr.strip()[:2048]}")
    return result.stdout


def deployment_name(base: str, node: str) -> str:
    candidate = f"{base}-{node}"
    if len(candidate) <= 63:
        return candidate
    digest = hashlib.sha256(node.encode("utf-8")).hexdigest()[:8]
    return f"{base}-{node[: max(1, 63 - len(base) - len(digest) - 2)]}-{digest}"


def load_json(resource: str) -> dict[str, Any] | None:
    result = subprocess.run(
        ["k3s", "kubectl", "-n", NAMESPACE, "get", resource, "-o", "json"],
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if result.returncode:
        return None
    return json.loads(result.stdout)


def node_label_true(node: str, label: str) -> bool:
    payload = load_json(f"node/{node}") or {}
    labels = ((payload.get("metadata") or {}).get("labels") or {})
    return str(labels.get(label) or "").lower() == "true"


def load_images(path: pathlib.Path) -> dict[str, str]:
    payload = json.loads(path.read_text(encoding="utf-8"))
    images: dict[str, str] = {}
    for service, record in (payload.get("services") or {}).items():
        image = str((record or {}).get("image") or "").strip()
        if image and ("@sha256:" in image or re.search(r":sha-[0-9a-f]{12,64}$", image)):
            images[str(service)] = image
    return images


def required_node_affinity(node: str, existing: Any) -> dict[str, Any]:
    affinity = copy.deepcopy(existing) if isinstance(existing, dict) else {}
    node_affinity = affinity.setdefault("nodeAffinity", {})
    node_affinity["requiredDuringSchedulingIgnoredDuringExecution"] = {
        "nodeSelectorTerms": [
            {
                "matchExpressions": [
                    {"key": "kubernetes.io/hostname", "operator": "In", "values": [node]},
                ]
            }
        ]
    }
    return affinity


def clean_metadata(metadata: dict[str, Any], name: str, node: str, revision: str, base: str) -> dict[str, Any]:
    labels = copy.deepcopy(metadata.get("labels") or {})
    labels["borealis.io/engine-node"] = node
    labels["borealis.io/node-workload"] = "true"
    annotations = copy.deepcopy(metadata.get("annotations") or {})
    annotations["borealis.io/cluster-template"] = base
    annotations["borealis.io/revision"] = revision
    return {"name": name, "namespace": NAMESPACE, "labels": labels, "annotations": annotations}


def reconcile_one(base: str, service: str, node: str, revision: str, images: dict[str, str]) -> str:
    name = deployment_name(base, node)
    source = load_json(f"deployment/{name}") or load_json(f"deployment/{base}")
    if not source:
        raise RuntimeError(f"required deployment template {base} does not exist")
    manifest = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": clean_metadata(source.get("metadata") or {}, name, node, revision, base),
        "spec": copy.deepcopy(source.get("spec") or {}),
    }
    spec = manifest["spec"]
    spec["replicas"] = 0 if base == "wireguard-tunnel" and not node_label_true(node, "borealis.io/edge-eligible") else 1
    spec["revisionHistoryLimit"] = 2
    selector = copy.deepcopy((spec.get("selector") or {}).get("matchLabels") or {})
    selector["borealis.io/engine-node"] = node
    spec["selector"] = {"matchLabels": selector}
    template = spec.setdefault("template", {})
    template_metadata = template.setdefault("metadata", {})
    template_labels = copy.deepcopy(template_metadata.get("labels") or {})
    template_labels["borealis.io/engine-node"] = node
    template_labels["borealis.io/node-workload"] = "true"
    template_metadata["labels"] = template_labels
    template_annotations = copy.deepcopy(template_metadata.get("annotations") or {})
    template_annotations["borealis.io/revision"] = revision
    template_metadata["annotations"] = template_annotations
    pod_spec = template.setdefault("spec", {})
    pod_spec.pop("nodeName", None)
    pod_spec["affinity"] = required_node_affinity(node, pod_spec.get("affinity"))
    image = images.get(service)
    if not image:
        raise RuntimeError(f"immutable image missing for {service}")
    containers = pod_spec.get("containers") or []
    if not containers:
        raise RuntimeError(f"deployment {base} has no containers")
    for container in containers:
        container["image"] = image
        container["imagePullPolicy"] = "IfNotPresent"
    if base == "api-backend":
        volumes = pod_spec.setdefault("volumes", [])
        volumes[:] = [volume for volume in volumes if volume.get("name") != "agent-artifacts"]
        volumes.append({"name": "agent-artifacts", "persistentVolumeClaim": {"claimName": "borealis-agent-artifacts"}})
        mounts = containers[0].setdefault("volumeMounts", [])
        mounts[:] = [mount for mount in mounts if mount.get("name") != "agent-artifacts"]
        mounts.append({"name": "agent-artifacts", "mountPath": "/opt/Borealis/Engine/Services/api-backend/cache/AgentUpdates"})
    if base in HOST_PORT_DEPLOYMENTS:
        spec["strategy"] = {"type": "Recreate"}
    else:
        spec["minReadySeconds"] = max(15, int(spec.get("minReadySeconds") or 0))
        spec["strategy"] = {"type": "RollingUpdate", "rollingUpdate": {"maxUnavailable": 0, "maxSurge": 1}}
    kubectl("apply", "--server-side", "--field-manager=borealis-node-workloads", "-f", "-", stdin=json.dumps(manifest))
    if spec["replicas"] > 0:
        kubectl("-n", NAMESPACE, "rollout", "status", f"deployment/{name}", "--timeout=10m")
    return name


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--node", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--image-manifest", required=True, type=pathlib.Path)
    parser.add_argument("--initialize", action="store_true")
    parser.add_argument("--service", action="append", choices=sorted(set(DEPLOYMENT_SERVICES.values())))
    args = parser.parse_args()
    node = args.node.strip().lower()
    revision = args.revision.strip().lower()
    if not NODE_RE.fullmatch(node) or not SHA_RE.fullmatch(revision):
        raise SystemExit("valid node name and lowercase 40-character revision required")
    images = load_images(args.image_manifest)
    reconciled = []
    selected = {value for value in (args.service or DEPLOYMENT_SERVICES.values())}
    selected_bases = [base for base, service in DEPLOYMENT_SERVICES.items() if service in selected]
    for base in selected_bases:
        service = DEPLOYMENT_SERVICES[base]
        reconciled.append(reconcile_one(base, service, node, revision, images))
    if args.initialize:
        for base in selected_bases:
            kubectl("-n", NAMESPACE, "scale", f"deployment/{base}", "--replicas=0")
    print(json.dumps({"node": node, "revision": revision, "deployments": reconciled}, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, RuntimeError, ValueError, json.JSONDecodeError) as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
