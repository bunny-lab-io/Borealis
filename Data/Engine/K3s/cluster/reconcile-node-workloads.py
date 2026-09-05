#!/usr/bin/env python3
"""Create or update Borealis application Deployments pinned to one Engine node."""

from __future__ import annotations

import argparse
import base64
import copy
import hashlib
import ipaddress
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
CANDIDATE_LABEL = "borealis.io/update-candidate"
TRAFFIC_LABEL = "borealis.io/traffic-state"
WIREGUARD_KEYS_SECRET = "borealis-wireguard-server-keys"
AEGIS_TRUST_CONFIGMAP = "borealis-aegis-trust"
WEBUI_DEVELOPMENT_VOLUME_NAMES = frozenset(
    {
        "webui-index",
        "webui-package",
        "webui-public",
        "webui-src",
        "webui-tsconfig",
        "webui-unit-tests",
        "webui-vite-cache",
        "webui-vite-config",
        "webui-vite-temp",
    }
)
PROBE_GUARD_DELAYS = {
    "api-backend": 130,
    "borealis-cluster-controller": 70,
    "borealis-operator": 130,
    "job-scheduler": 190,
    "remote-desktop-guacd": 130,
    "traefik-edge": 130,
    "webui-frontend": 190,
    "wireguard-tunnel": 190,
}


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


def ensure_aegis_trust_bundle() -> None:
    """Bootstrap independent public trust once; never overwrite rotation policy."""
    resource = f"configmap/{AEGIS_TRUST_CONFIGMAP}"
    if kubectl("-n", NAMESPACE, "get", resource, "--ignore-not-found", "-o", "json").strip():
        return
    deployments = json.loads(kubectl("-n", NAMESPACE, "get", "deployments", "-l", "borealis.io/aegis-peer=true", "-o", "json"))
    for deployment in deployments.get("items", []):
        volumes = deployment.get("spec", {}).get("template", {}).get("spec", {}).get("volumes", [])
        for volume in volumes:
            for source in volume.get("projected", {}).get("sources", []):
                if source.get("configMap", {}).get("name") == AEGIS_TRUST_CONFIGMAP:
                    raise RuntimeError("Aegis trust bundle missing after adoption; restore operator-approved CA bundle before redeploy")
    # Read only the public certificate field, never the Secret's private key.
    encoded = kubectl("-n", NAMESPACE, "get", "secret/borealis-api-aegis-mtls", "-o", "jsonpath={.data.ca\\.crt}").strip()
    ca = base64.b64decode(encoded, validate=True).decode("ascii")
    if not ca.startswith("-----BEGIN CERTIFICATE-----") or len(ca) > 65536:
        raise RuntimeError("Initial Aegis CA certificate is unavailable or oversized")
    manifest = {
        "apiVersion": "v1", "kind": "ConfigMap",
        "metadata": {"name": AEGIS_TRUST_CONFIGMAP, "namespace": NAMESPACE},
        "data": {"ca-bundle.crt": ca},
    }
    try:
        kubectl("create", "-f", "-", stdin=json.dumps(manifest))
    except RuntimeError:
        # Concurrent first-node reconciliation may create the same bundle.
        # Preserve the winner; authorization/network failures still fail closed.
        if not kubectl("-n", NAMESPACE, "get", resource, "--ignore-not-found", "-o", "json").strip():
            raise


def candidate_deployment_name(base: str, node: str) -> str:
    active = deployment_name(base, node)
    candidate = f"{active}-candidate"
    if len(candidate) <= 63:
        return candidate
    digest = hashlib.sha256(f"{base}:{node}:candidate".encode("utf-8")).hexdigest()[:8]
    return f"{base[: max(1, 63 - len(digest) - 11)]}-candidate-{digest}"


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


def cluster_virtual_ip() -> str:
    payload = load_json("borealiscluster.engine.borealis.io/borealis") or {}
    value = str(((payload.get("spec") or {}).get("clusterVIP") or "")).strip()
    try:
        address = ipaddress.ip_address(value)
    except ValueError as exc:
        raise RuntimeError("Borealis Cluster Virtual IP is unavailable") from exc
    if address.version != 4 or not address.is_private:
        raise RuntimeError("Borealis Cluster Virtual IP must be private IPv4")
    return value


def set_container_environment(container: dict[str, Any], name: str, value: str) -> None:
    environment = container.setdefault("env", [])
    environment[:] = [item for item in environment if item.get("name") != name]
    environment.append({"name": name, "value": value})


def enforce_webui_production_candidate(pod_spec: dict[str, Any], container: dict[str, Any]) -> None:
    """Prevent release candidates from inheriting mutable HMR runtime state."""
    set_container_environment(container, "BOREALIS_WEBUI_MODE", "prod")
    mounts = container.setdefault("volumeMounts", [])
    mounts[:] = [mount for mount in mounts if mount.get("name") not in WEBUI_DEVELOPMENT_VOLUME_NAMES]
    volumes = pod_spec.setdefault("volumes", [])
    volumes[:] = [volume for volume in volumes if volume.get("name") not in WEBUI_DEVELOPMENT_VOLUME_NAMES]


def enforce_startup_liveness_guard(
    base: str, template_annotations: dict[str, str], containers: list[dict[str, Any]]
) -> None:
    delay = PROBE_GUARD_DELAYS.get(base)
    if delay is None:
        raise RuntimeError(f"probe guard delay missing for {base}")
    for container in containers:
        startup = container.get("startupProbe") or {}
        liveness = container.get("livenessProbe") or {}
        if not startup or not liveness:
            raise RuntimeError(f"deployment {base} requires separate startup and liveness probes")
        startup_budget = (
            int(startup.get("initialDelaySeconds") or 0)
            + int(startup.get("periodSeconds") or 10) * int(startup.get("failureThreshold") or 3)
            + int(startup.get("timeoutSeconds") or 1)
        )
        if delay <= startup_budget:
            raise RuntimeError(
                f"deployment {base} liveness delay {delay}s does not exceed startup budget {startup_budget}s"
            )
        liveness["initialDelaySeconds"] = delay
        container["livenessProbe"] = liveness
    template_annotations["borealis.io/liveness-startup-guard"] = str(delay)


def mount_shared_wireguard_keys(base: str, pod_spec: dict[str, Any], container: dict[str, Any]) -> None:
    volumes = pod_spec.setdefault("volumes", [])
    mounts = container.setdefault("volumeMounts", [])
    if base == "api-backend":
        volume_name = "wireguard-secrets"
    elif base == "wireguard-tunnel":
        volume_name = "wireguard-server-keys"
    else:
        return
    volumes[:] = [volume for volume in volumes if volume.get("name") != volume_name]
    volumes.append({"name": volume_name, "secret": {"secretName": WIREGUARD_KEYS_SECRET, "defaultMode": 0o440}})
    mounts[:] = [mount for mount in mounts if mount.get("name") != volume_name]
    mounts.append(
        {
            "name": volume_name,
            "mountPath": "/opt/Borealis/Engine/Services/wireguard-tunnel/secrets",
            "readOnly": True,
        }
    )


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


def clean_metadata(
    metadata: dict[str, Any], name: str, node: str, revision: str, base: str, candidate: bool
) -> dict[str, Any]:
    labels = copy.deepcopy(metadata.get("labels") or {})
    labels["borealis.io/engine-node"] = node
    labels["borealis.io/node-workload"] = "true"
    labels[CANDIDATE_LABEL] = "true" if candidate else "false"
    labels[TRAFFIC_LABEL] = "candidate" if candidate else "active"
    labels["app.kubernetes.io/name"] = f"{base}-candidate" if candidate else base
    if base == "api-backend":
        labels["borealis.io/aegis-peer"] = "true"
    annotations = copy.deepcopy(metadata.get("annotations") or {})
    # Deployment controller owns rollout revision. Reapplying copied value
    # conflicts after first candidate has created ReplicaSet history.
    annotations.pop("deployment.kubernetes.io/revision", None)
    annotations.pop("kubectl.kubernetes.io/last-applied-configuration", None)
    annotations["borealis.io/cluster-template"] = base
    annotations["borealis.io/revision"] = revision
    return {"name": name, "namespace": NAMESPACE, "labels": labels, "annotations": annotations}


def reconcile_one(
    base: str, service: str, node: str, revision: str, images: dict[str, str], candidate: bool
) -> str:
    active_name = deployment_name(base, node)
    name = candidate_deployment_name(base, node) if candidate else active_name
    active = load_json(f"deployment/{active_name}")
    existing = load_json(f"deployment/{name}") if candidate else active
    # Generic zero-replica Deployment remains canonical runtime template in
    # cluster mode. Starting from existing node clone would retain stale env,
    # Secret references, probes, and config hashes after Engine.sh updates it.
    source = load_json(f"deployment/{base}") or active
    if not source:
        raise RuntimeError(f"required deployment template {base} does not exist")
    manifest = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": clean_metadata(source.get("metadata") or {}, name, node, revision, base, candidate),
        "spec": copy.deepcopy(source.get("spec") or {}),
    }
    spec = manifest["spec"]
    # Host-port candidates cannot run beside active workload on same node.
    # Keep both stopped during normal candidate staging. Traefik receives bounded
    # stop/start health handoff during promotion; WireGuard remains stopped until
    # active owner-aware workload is replaced.
    spec["replicas"] = 0 if base in HOST_PORT_DEPLOYMENTS and candidate else 1
    spec["revisionHistoryLimit"] = 2
    existing_selector = copy.deepcopy(
        (((existing or {}).get("spec") or {}).get("selector") or {}).get("matchLabels") or {}
    )
    selector = existing_selector or copy.deepcopy((spec.get("selector") or {}).get("matchLabels") or {})
    if not existing_selector:
        selector["borealis.io/engine-node"] = node
        selector[CANDIDATE_LABEL] = "true" if candidate else "false"
        selector[TRAFFIC_LABEL] = "candidate" if candidate else "active"
        selector["app.kubernetes.io/name"] = f"{base}-candidate" if candidate else base
    spec["selector"] = {"matchLabels": selector}
    template = spec.setdefault("template", {})
    template_metadata = template.setdefault("metadata", {})
    template_labels = copy.deepcopy(template_metadata.get("labels") or {})
    template_labels["borealis.io/engine-node"] = node
    template_labels["borealis.io/node-workload"] = "true"
    template_labels[CANDIDATE_LABEL] = "true" if candidate else "false"
    template_labels[TRAFFIC_LABEL] = "candidate" if candidate else "active"
    template_labels["app.kubernetes.io/name"] = f"{base}-candidate" if candidate else base
    if base == "api-backend":
        template_labels["borealis.io/aegis-peer"] = "true"
    template_labels.update(selector)
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
    enforce_startup_liveness_guard(base, template_annotations, containers)
    for container in containers:
        container["image"] = image
        container["imagePullPolicy"] = "IfNotPresent"
    if base == "webui-frontend" and candidate:
        enforce_webui_production_candidate(pod_spec, containers[0])
    if base == "job-scheduler":
        set_container_environment(containers[0], "BOREALIS_SCHEDULER_LEADERSHIP_ELIGIBLE", "false" if candidate else "true")
    if base == "borealis-cluster-controller":
        set_container_environment(containers[0], "BOREALIS_CLUSTER_CONTROLLER_ELIGIBLE", "false" if candidate else "true")
        set_container_environment(containers[0], "BOREALIS_CLUSTER_ACTION_IMAGE", image)
    if base == "api-backend":
        set_container_environment(containers[0], "BOREALIS_API_BACKGROUND_LOOPS", "0" if candidate else "1")
    if base in {"api-backend", "wireguard-tunnel"}:
        cluster_vip = cluster_virtual_ip()
        set_container_environment(containers[0], "BOREALIS_CLUSTER_EDGE_VIP", cluster_vip)
        mount_shared_wireguard_keys(base, pod_spec, containers[0])
    if base == "api-backend":
        volumes = pod_spec.setdefault("volumes", [])
        volumes[:] = [volume for volume in volumes if volume.get("name") not in {"agent-artifacts", "aegis-mtls"}]
        volumes.append({"name": "agent-artifacts", "persistentVolumeClaim": {"claimName": "borealis-agent-artifacts"}})
        volumes.append({"name": "aegis-mtls", "projected": {
            "defaultMode": 0o440,
            "sources": [
                {"secret": {"name": "borealis-api-aegis-mtls", "items": [
                    {"key": "tls.crt", "path": "tls.crt"}, {"key": "tls.key", "path": "tls.key"},
                ]}},
                {"configMap": {"name": AEGIS_TRUST_CONFIGMAP, "items": [
                    {"key": "ca-bundle.crt", "path": "trust-bundle.pem"},
                ]}},
            ],
        }})
        mounts = containers[0].setdefault("volumeMounts", [])
        mounts[:] = [mount for mount in mounts if mount.get("name") not in {"agent-artifacts", "aegis-mtls"}]
        mounts.append({"name": "agent-artifacts", "mountPath": "/opt/Borealis/Engine/Services/api-backend/cache/AgentUpdates"})
        mounts.append({"name": "aegis-mtls", "mountPath": "/var/run/secrets/borealis-aegis-mtls", "readOnly": True})
        environment = containers[0].setdefault("env", [])
        cluster_environment = {
            "BOREALIS_AEGIS_CLUSTER_FANOUT_ENABLED": "1",
            "BOREALIS_AEGIS_CLUSTER_LISTEN_HOST": "0.0.0.0",
            "BOREALIS_AEGIS_CLUSTER_PORT": "9444",
            "BOREALIS_AEGIS_CLUSTER_PEER_HOST": "api-backend-aegis.borealis.svc",
            "BOREALIS_AEGIS_CLUSTER_TLS_CERT": "/var/run/secrets/borealis-aegis-mtls/tls.crt",
            "BOREALIS_AEGIS_CLUSTER_TLS_KEY": "/var/run/secrets/borealis-aegis-mtls/tls.key",
            "BOREALIS_AEGIS_CLUSTER_TLS_CA": "/var/run/secrets/borealis-aegis-mtls/trust-bundle.pem",
        }
        environment[:] = [item for item in environment if item.get("name") not in cluster_environment]
        environment.extend({"name": key, "value": value} for key, value in cluster_environment.items())
        ports = containers[0].setdefault("ports", [])
        ports[:] = [port for port in ports if port.get("name") != "aegis-mtls"]
        ports.append({"name": "aegis-mtls", "containerPort": 9444, "protocol": "TCP"})
    if base in HOST_PORT_DEPLOYMENTS:
        spec["minReadySeconds"] = max(15, int(spec.get("minReadySeconds") or 0))
        spec["strategy"] = {"type": "Recreate"}
    else:
        spec["minReadySeconds"] = max(15, int(spec.get("minReadySeconds") or 0))
        spec["strategy"] = {"type": "RollingUpdate", "rollingUpdate": {"maxUnavailable": 0, "maxSurge": 1}}
    kubectl(
        "apply",
        "--server-side",
        "--force-conflicts",
        "--field-manager=borealis-node-workloads",
        "-f",
        "-",
        stdin=json.dumps(manifest),
    )
    if spec["replicas"] > 0:
        kubectl("-n", NAMESPACE, "rollout", "status", f"deployment/{name}", "--timeout=10m")
    return name


def promotion_selector(
    base: str, node: str, candidate: dict[str, Any], active: dict[str, Any] | None
) -> dict[str, str]:
    selector_source = active if active else candidate
    selector = copy.deepcopy(((selector_source.get("spec") or {}).get("selector") or {}).get("matchLabels") or {})
    if not selector:
        raise RuntimeError(f"promotion source for {deployment_name(base, node)} lacks immutable selector")
    if active:
        # Deployment selectors are immutable. Existing active workload keeps
        # exact selector; promoted pod labels are made to satisfy it below.
        return selector
    selector["app.kubernetes.io/name"] = base
    selector["borealis.io/engine-node"] = node
    selector["borealis.io/node-workload"] = "true"
    selector[CANDIDATE_LABEL] = "false"
    selector[TRAFFIC_LABEL] = "active"
    return selector


def enforce_wireguard_promotion_readiness(container: dict[str, Any]) -> None:
    """Keep pinned pre-standby-probe images ready while safely fenced."""
    probe = copy.deepcopy(container.get("readinessProbe") or {})
    probe["exec"] = {
        "command": [
            "sh",
            "-ec",
            """status="$(borealis-wireguard-control-client status)"
if printf '%s\\n' "${status}" | grep -Fq '"stdout":"standby"'; then
  if ip link show dev "${BOREALIS_WIREGUARD_INTERFACE:-borealis-wg}" >/dev/null 2>&1; then
    printf 'Standby WireGuard interface remains active.\\n' >&2
    exit 1
  fi
  exit 0
fi
exec borealis-wireguard-healthcheck""",
        ]
    }
    container["readinessProbe"] = probe


def refresh_generic_template_images(base: str, promoted_manifest: dict[str, Any], revision: str) -> None:
    generic = load_json(f"deployment/{base}")
    if not generic:
        raise RuntimeError(f"generic deployment {base} does not exist")
    generic_spec = generic.get("spec") or {}
    if generic_spec.get("replicas") != 0:
        raise RuntimeError(f"generic deployment {base} must remain at zero replicas")
    resource_version = str((generic.get("metadata") or {}).get("resourceVersion") or "")
    if not resource_version:
        raise RuntimeError(f"generic deployment {base} lacks resource version")
    generic_containers = (((generic_spec.get("template") or {}).get("spec") or {}).get("containers") or [])
    promoted_containers = (
        ((((promoted_manifest.get("spec") or {}).get("template") or {}).get("spec") or {}).get("containers") or [])
    )
    promoted_images = {
        str(container.get("name") or ""): str(container.get("image") or "")
        for container in promoted_containers
        if container.get("name")
    }
    image_patch = []
    for container in generic_containers:
        name = str(container.get("name") or "")
        image = promoted_images.get(name, "")
        if not name or not image or not ("@sha256:" in image or re.search(r":sha-[0-9a-f]{12,64}$", image)):
            raise RuntimeError(f"generic deployment {base} container images do not match promoted workload")
        image_patch.append({"name": name, "image": image})
    if not image_patch:
        raise RuntimeError(f"generic deployment {base} has no containers")
    patch = {
        "metadata": {
            "resourceVersion": resource_version,
            "annotations": {"borealis.io/revision": revision},
        },
        "spec": {
            "template": {
                "metadata": {"annotations": {"borealis.io/revision": revision}},
                "spec": {"containers": image_patch},
            }
        },
    }
    output = kubectl(
        "-n",
        NAMESPACE,
        "patch",
        f"deployment/{base}",
        "--type=strategic",
        "-p",
        json.dumps(patch, sort_keys=True),
        "-o",
        "json",
    )
    try:
        updated = json.loads(output)
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"generic deployment {base} patch returned invalid state") from exc
    updated_spec = updated.get("spec") or {}
    updated_annotations = (updated.get("metadata") or {}).get("annotations") or {}
    updated_template = updated_spec.get("template") or {}
    updated_template_annotations = (updated_template.get("metadata") or {}).get("annotations") or {}
    updated_containers = ((updated_template.get("spec") or {}).get("containers") or [])
    updated_images = {
        str(container.get("name") or ""): str(container.get("image") or "")
        for container in updated_containers
        if container.get("name")
    }
    expected_images = {item["name"]: item["image"] for item in image_patch}
    if (
        updated_spec.get("replicas") != 0
        or updated_annotations.get("borealis.io/revision") != revision
        or updated_template_annotations.get("borealis.io/revision") != revision
        or updated_images != expected_images
    ):
        raise RuntimeError(f"generic deployment {base} patch did not converge")


def promote_one(base: str, node: str, expected_revision: str) -> str:
    active_name = deployment_name(base, node)
    candidate_name = candidate_deployment_name(base, node)
    candidate = load_json(f"deployment/{candidate_name}")
    active = load_json(f"deployment/{active_name}")
    if not candidate:
        raise RuntimeError(f"candidate deployment missing for {base} on {node}")
    revision = str(((candidate.get("metadata") or {}).get("annotations") or {}).get("borealis.io/revision") or "")
    if not SHA_RE.fullmatch(revision):
        raise RuntimeError(f"candidate deployment {candidate_name} lacks pinned revision")
    if revision != expected_revision:
        raise RuntimeError(f"candidate deployment {candidate_name} does not match requested revision")
    manifest = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": clean_metadata(candidate.get("metadata") or {}, active_name, node, revision, base, False),
        "spec": copy.deepcopy(candidate.get("spec") or {}),
    }
    spec = manifest["spec"]
    existing_selector = promotion_selector(base, node, candidate, active)
    spec["selector"] = {"matchLabels": existing_selector}
    replicas = 1
    spec["replicas"] = replicas
    template = spec.setdefault("template", {})
    template_labels = copy.deepcopy((template.setdefault("metadata", {})).get("labels") or {})
    template_labels.update(existing_selector)
    template_labels["app.kubernetes.io/name"] = base
    template_labels["borealis.io/engine-node"] = node
    template_labels["borealis.io/node-workload"] = "true"
    template_labels[CANDIDATE_LABEL] = "false"
    template_labels[TRAFFIC_LABEL] = "active"
    template["metadata"]["labels"] = template_labels
    if base == "api-backend":
        template["metadata"]["labels"]["borealis.io/aegis-peer"] = "true"
        containers = ((template.get("spec") or {}).get("containers") or [])
        if not containers:
            raise RuntimeError(f"candidate deployment {candidate_name} has no API container")
        set_container_environment(containers[0], "BOREALIS_API_BACKGROUND_LOOPS", "1")
    if base == "job-scheduler":
        containers = ((template.get("spec") or {}).get("containers") or [])
        if not containers:
            raise RuntimeError(f"candidate deployment {candidate_name} has no scheduler container")
        set_container_environment(containers[0], "BOREALIS_SCHEDULER_LEADERSHIP_ELIGIBLE", "true")
    if base == "borealis-cluster-controller":
        containers = ((template.get("spec") or {}).get("containers") or [])
        if not containers:
            raise RuntimeError(f"candidate deployment {candidate_name} has no cluster controller container")
        set_container_environment(containers[0], "BOREALIS_CLUSTER_CONTROLLER_ELIGIBLE", "true")
    if base == "traefik-edge":
        # Candidate and active Traefik cannot bind same host ports. Stop active,
        # run isolated candidate through readiness/min-ready soak, then stop it
        # before applying active target. Restore old active workload when
        # candidate fails before target manifest is committed.
        active_replicas = int(((active or {}).get("spec") or {}).get("replicas") or 0)
        if active_replicas > 0:
            kubectl("-n", NAMESPACE, "scale", f"deployment/{active_name}", "--replicas=0")
            kubectl("-n", NAMESPACE, "rollout", "status", f"deployment/{active_name}", "--timeout=5m")
        try:
            kubectl("-n", NAMESPACE, "scale", f"deployment/{candidate_name}", "--replicas=1")
            kubectl("-n", NAMESPACE, "rollout", "status", f"deployment/{candidate_name}", "--timeout=10m")
        except RuntimeError:
            try:
                kubectl("-n", NAMESPACE, "scale", f"deployment/{candidate_name}", "--replicas=0")
            except RuntimeError:
                pass
            if active_replicas > 0:
                kubectl("-n", NAMESPACE, "scale", f"deployment/{active_name}", f"--replicas={active_replicas}")
                kubectl("-n", NAMESPACE, "rollout", "status", f"deployment/{active_name}", "--timeout=5m")
            raise
        kubectl("-n", NAMESPACE, "scale", f"deployment/{candidate_name}", "--replicas=0")
        kubectl("-n", NAMESPACE, "rollout", "status", f"deployment/{candidate_name}", "--timeout=5m")
    elif base == "wireguard-tunnel":
        # WireGuard candidate stays stopped. Active controller validates edge
        # lease ownership and usable tunnel state after replacement. Promotion
        # supplies owner-aware readiness for pinned images predating standby
        # probe support; active owners still run the image's full healthcheck.
        kubectl("-n", NAMESPACE, "scale", f"deployment/{candidate_name}", "--replicas=0")
        kubectl("-n", NAMESPACE, "rollout", "status", f"deployment/{candidate_name}", "--timeout=5m")
        containers = ((template.get("spec") or {}).get("containers") or [])
        if not containers:
            raise RuntimeError(f"candidate deployment {candidate_name} has no WireGuard container")
        enforce_wireguard_promotion_readiness(containers[0])
    kubectl("apply", "--server-side", "--force-conflicts", "--field-manager=borealis-node-workloads", "-f", "-", stdin=json.dumps(manifest))
    if replicas > 0:
        kubectl("-n", NAMESPACE, "rollout", "status", f"deployment/{active_name}", "--timeout=10m")
    refresh_generic_template_images(base, manifest, revision)
    kubectl("-n", NAMESPACE, "delete", f"deployment/{candidate_name}", "--wait=true", "--timeout=5m")
    return active_name


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--node", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--image-manifest", required=True, type=pathlib.Path)
    parser.add_argument("--initialize", action="store_true")
    parser.add_argument("--candidate", action="store_true")
    parser.add_argument("--promote-candidate", action="store_true")
    parser.add_argument("--service", action="append", choices=sorted(set(DEPLOYMENT_SERVICES.values())))
    args = parser.parse_args()
    node = args.node.strip().lower()
    revision = args.revision.strip().lower()
    if not NODE_RE.fullmatch(node) or not SHA_RE.fullmatch(revision):
        raise SystemExit("valid node name and lowercase 40-character revision required")
    if args.candidate and args.promote_candidate:
        raise SystemExit("--candidate and --promote-candidate are mutually exclusive")
    images = load_images(args.image_manifest)
    reconciled = []
    selected = {value for value in (args.service or DEPLOYMENT_SERVICES.values())}
    selected_bases = [base for base, service in DEPLOYMENT_SERVICES.items() if service in selected]
    if "api-backend" in selected_bases:
        ensure_aegis_trust_bundle()
    stopped_generic_host_workloads: list[tuple[str, int]] = []
    try:
        for base in selected_bases:
            service = DEPLOYMENT_SERVICES[base]
            if args.initialize and not args.candidate and not args.promote_candidate and base in HOST_PORT_DEPLOYMENTS:
                generic = load_json(f"deployment/{base}") or {}
                replicas = int(((generic.get("spec") or {}).get("replicas")) or 0)
                if replicas > 0:
                    kubectl("-n", NAMESPACE, "scale", f"deployment/{base}", "--replicas=0")
                    kubectl("-n", NAMESPACE, "rollout", "status", f"deployment/{base}", "--timeout=5m")
                    stopped_generic_host_workloads.append((base, replicas))
            if args.promote_candidate:
                reconciled.append(promote_one(base, node, revision))
            else:
                reconciled.append(reconcile_one(base, service, node, revision, images, args.candidate))
    except Exception:
        for base, replicas in reversed(stopped_generic_host_workloads):
            active_name = deployment_name(base, node)
            if load_json(f"deployment/{active_name}"):
                try:
                    kubectl("-n", NAMESPACE, "scale", f"deployment/{active_name}", "--replicas=0")
                except RuntimeError:
                    pass
            try:
                kubectl("-n", NAMESPACE, "scale", f"deployment/{base}", f"--replicas={replicas}")
            except RuntimeError:
                pass
        raise
    if args.initialize and not args.candidate and not args.promote_candidate:
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
