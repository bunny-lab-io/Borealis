#!/usr/bin/env python3
"""Render production Engine.sh YAML and enforce Borealis K3s security contract."""

from __future__ import annotations

import argparse
from pathlib import Path
import subprocess
import sys
import tempfile

try:
    import yaml
except ImportError:
    print("K3S POLICY FAIL: PyYAML missing; install Tests/requirements-policy.txt", file=sys.stderr)
    raise SystemExit(127)


ROOT = Path(__file__).resolve().parents[2]
NAMESPACE = "borealis"
HOST_NETWORK_ALLOWLIST = {"traefik-edge", "wireguard-tunnel"}
CAPABILITY_ALLOWLIST = {
    ("traefik-edge", "traefik-edge"): {"DAC_OVERRIDE", "NET_BIND_SERVICE"},
    ("wireguard-tunnel", "wireguard-tunnel"): {"NET_ADMIN", "NET_RAW"},
    ("postgres-db", "postgres-data-permissions"): {"CHOWN", "DAC_OVERRIDE", "FOWNER"},
}


def validate_probe_conformance_contract() -> None:
    path = ROOT / "Data/Engine/K3s/cluster/run-probe-conformance.sh"
    try:
        source = path.read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read K3s probe conformance script: {exc}")
    required = {
        "/state/fail-liveness": "controlled liveness failure",
        ".status.containerStatuses[0].containerID": "replacement container identity check",
        ".status.containerStatuses[0].started": "replacement startup state check",
        "replacement startup-probe:failure": "replacement startup execution proof",
        "replacement liveness-probe:": "premature replacement liveness rejection",
        "Kubernetes issue 141155": "upstream regression context",
    }
    for marker, description in required.items():
        if marker not in source:
            fail(f"probe conformance lost {description}")
    if "kill -KILL 1" in source or "kill -TERM 1" in source:
        fail("probe conformance must trigger restart through liveness failure, not direct PID 1 signal")


def validate_cluster_controller_contract() -> None:
    path = ROOT / "Data/Engine/K3s/cluster/controller.yaml"
    try:
        objects = [item for item in yaml.safe_load_all(path.read_text(encoding="utf-8")) if item]
    except (OSError, yaml.YAMLError) as exc:
        fail(f"cannot parse cluster controller manifest: {exc}")
    deployments = [
        item
        for item in objects
        if item.get("kind") == "Deployment"
        and (item.get("metadata") or {}).get("name") == "borealis-cluster-controller"
    ]
    if len(deployments) != 1:
        fail("cluster controller manifest must contain one controller Deployment")
    pod = (((deployments[0].get("spec") or {}).get("template") or {}).get("spec") or {})
    if pod.get("serviceAccountName") != "borealis-cluster-controller" or pod.get("automountServiceAccountToken") is not True:
        fail("cluster controller must explicitly mount its dedicated ServiceAccount token")
    if (((pod.get("securityContext") or {}).get("seccompProfile") or {}).get("type")) != "RuntimeDefault":
        fail("cluster controller lacks RuntimeDefault seccomp")
    containers = pod.get("containers") or []
    if len(containers) != 1:
        fail("cluster controller must contain one controller container")
    security = containers[0].get("securityContext") or {}
    expected_security = {
        "allowPrivilegeEscalation": False,
        "readOnlyRootFilesystem": True,
        "runAsNonRoot": True,
        "runAsUser": 64646,
        "runAsGroup": 64646,
    }
    for field, expected in expected_security.items():
        if security.get(field) != expected:
            fail(f"cluster controller securityContext {field} must be {expected!r}")
    if set(((security.get("capabilities") or {}).get("drop") or [])) != {"ALL"}:
        fail("cluster controller must drop ALL capabilities")
    resources = containers[0].get("resources") or {}
    if not resources.get("requests") or not resources.get("limits"):
        fail("cluster controller must declare resource requests and limits")


def validate_node_manager_service_contract() -> None:
    path = ROOT / "Data/Engine/K3s/cluster/node-manager.service"
    try:
        source = path.read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read node-manager systemd unit: {exc}")
    required = {
        "RuntimeDirectory=borealis": "managed runtime directory",
        "ConfigurationDirectory=borealis": "managed configuration directory",
        "ProtectSystem=strict": "strict filesystem protection",
        "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK": "Unix socket, K3s API, release-fetch, and host network-inspection contract",
    }
    for marker, description in required.items():
        if marker not in source:
            fail(f"node-manager systemd unit lost {description}")
    try:
        engine_source = (ROOT / "Engine.sh").read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read Engine node-manager installer: {exc}")
    if 'install -d -m 0750 -o root -g root "$(dirname -- "${BOREALIS_NODE_MANAGER_TOKEN_FILE}")"' not in engine_source:
        fail("Engine node-manager installer must correct configuration-directory ownership and mode")


def fail(message: str) -> None:
    print(f"K3S POLICY FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


def render(destination: Path) -> None:
    proc = subprocess.run(
        [str(ROOT / "Tests/render-k3s-manifests.sh"), str(destination)],
        cwd=ROOT,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if proc.returncode:
        fail(proc.stderr.strip() or "production manifest render failed")


def documents(manifest_root: Path) -> list[tuple[Path, dict]]:
    result: list[tuple[Path, dict]] = []
    for path in sorted(manifest_root.glob("*.yaml")):
        try:
            result.extend((path, item) for item in yaml.safe_load_all(path.read_text(encoding="utf-8")) if item)
        except (OSError, yaml.YAMLError) as exc:
            fail(f"cannot parse {path.name}: {exc}")
    if not result:
        fail("render produced no Kubernetes objects")
    return result


def pod_spec(obj: dict) -> dict | None:
    kind = obj.get("kind")
    if kind in {"Deployment", "StatefulSet", "DaemonSet", "Job"}:
        return obj.get("spec", {}).get("template", {}).get("spec")
    if kind == "Pod":
        return obj.get("spec")
    return None


def validate_labels(obj: dict, source: Path) -> None:
    metadata = obj.get("metadata") or {}
    name = metadata.get("name", "<unnamed>")
    if obj.get("kind") != "StorageClass" and metadata.get("namespace") != NAMESPACE:
        fail(f"{source.name}:{obj.get('kind')}/{name} namespace is not {NAMESPACE}")
    labels = metadata.get("labels") or {}
    if labels.get("app.kubernetes.io/part-of") != "borealis":
        fail(f"{obj.get('kind')}/{name} lacks Borealis ownership label")


def validate_service(obj: dict) -> None:
    name = obj["metadata"]["name"]
    spec = obj.get("spec") or {}
    if name == "postgres-db-headless":
        if spec.get("clusterIP") != "None":
            fail("postgres-db-headless must remain explicit headless Service")
    elif spec.get("type", "ClusterIP") != "ClusterIP":
        fail(f"Service/{name} gained non-ClusterIP exposure")
    selector = spec.get("selector") or {}
    if selector.get("app.kubernetes.io/name") != name and name != "postgres-db-headless":
        fail(f"Service/{name} selector does not match workload name")
    for port in spec.get("ports") or []:
        if not port.get("targetPort"):
            fail(f"Service/{name} port lacks targetPort")


def validate_operator_role(obj: dict) -> None:
    allowed_resources = {"pods", "services", "deployments", "replicasets", "statefulsets"}
    allowed_verbs = {"get", "list", "create", "delete", "patch"}
    for rule in obj.get("rules") or []:
        resources = set(rule.get("resources") or [])
        verbs = set(rule.get("verbs") or [])
        if "*" in resources or "*" in verbs:
            fail("borealis-operator RBAC contains wildcard")
        if resources - allowed_resources:
            fail(f"borealis-operator RBAC gained resources: {sorted(resources - allowed_resources)}")
        if verbs - allowed_verbs:
            fail(f"borealis-operator RBAC gained verbs: {sorted(verbs - allowed_verbs)}")
        if resources & {"secrets", "nodes", "serviceaccounts", "roles", "rolebindings"}:
            fail(f"borealis-operator RBAC crosses credential/cluster boundary: {sorted(resources)}")


def host_path_allowed(workload: str, host_path: str) -> bool:
    if host_path in {"/etc/localtime", "/usr/share/zoneinfo"}:
        return True
    if workload == "wireguard-tunnel" and host_path == "/dev/net/tun":
        return True
    marker = "/Engine/"
    if marker not in host_path:
        return False
    suffix = host_path.split(marker, 1)[1]
    prefixes = {
        "api-backend": (
            "Services/api-backend/cache",
            "Services/api-backend/config",
            "Services/api-backend/logs",
            "Services/api-backend/secrets",
            "Services/traefik-edge/config",
            "Services/traefik-edge/env",
            "Services/traefik-edge/logs",
            "Services/traefik-edge/state",
            "Services/wireguard-tunnel/config",
            "Services/wireguard-tunnel/run",
            "Services/wireguard-tunnel/logs",
            "Services/wireguard-tunnel/secrets",
        ),
        "job-scheduler": (
            "Deploy",
            "Services/api-backend/cache",
            "Services/api-backend/config",
            "Services/api-backend/logs",
            "Services/api-backend/secrets",
            "Services/wireguard-tunnel/run",
            "Services/traefik-edge/config/dynamic",
        ),
        "traefik-edge": ("Services/traefik-edge",),
        "wireguard-tunnel": ("Services/wireguard-tunnel",),
        "webui-frontend": ("Services/webui-frontend/data/web-interface/",),
    }
    return any(
        suffix == prefix.rstrip("/") or suffix.startswith(prefix.rstrip("/") + "/")
        for prefix in prefixes.get(workload, ())
    )


def validate_workload(obj: dict, source: Path) -> None:
    name = obj["metadata"]["name"]
    spec = pod_spec(obj)
    if spec is None:
        return
    operator = name == "borealis-operator"
    expected_automount = True if operator else False
    if spec.get("automountServiceAccountToken") is not expected_automount:
        fail(f"{name} automountServiceAccountToken must be {str(expected_automount).lower()}")
    if operator and spec.get("serviceAccountName") != "borealis-operator":
        fail("borealis-operator lost dedicated ServiceAccount")
    if not operator and spec.get("serviceAccountName"):
        fail(f"{name} unexpectedly selects ServiceAccount {spec['serviceAccountName']}")
    if bool(spec.get("hostNetwork", False)) != (name in HOST_NETWORK_ALLOWLIST):
        fail(f"{name} hostNetwork differs from edge allowlist")
    pod_security = spec.get("securityContext") or {}
    if (pod_security.get("seccompProfile") or {}).get("type") != "RuntimeDefault":
        fail(f"{name} lacks RuntimeDefault seccomp")

    containers = (spec.get("initContainers") or []) + (spec.get("containers") or [])
    if not containers:
        fail(f"{name} has no containers")
    for container in containers:
        container_name = container.get("name", "<unnamed>")
        security = container.get("securityContext") or {}
        if security.get("privileged") is True:
            fail(f"{name}/{container_name} is privileged")
        if security.get("allowPrivilegeEscalation") is not False:
            fail(f"{name}/{container_name} allows privilege escalation")
        if security.get("readOnlyRootFilesystem") is not True:
            fail(f"{name}/{container_name} root filesystem is writable")
        capabilities = security.get("capabilities") or {}
        if set(capabilities.get("drop") or []) != {"ALL"}:
            fail(f"{name}/{container_name} does not drop ALL capabilities")
        additions = set(capabilities.get("add") or [])
        expected = CAPABILITY_ALLOWLIST.get((name, container_name), set())
        if additions != expected:
            fail(f"{name}/{container_name} capability additions {sorted(additions)} != {sorted(expected)}")

    for container in spec.get("containers") or []:
        if not (container.get("resources") or {}).get("limits"):
            fail(f"{name}/{container.get('name')} lacks resource limits")
        if obj.get("kind") != "Job" and (not container.get("livenessProbe") or not container.get("readinessProbe")):
            fail(f"{name}/{container.get('name')} lacks documented probes")

    for volume in spec.get("volumes") or []:
        host_path = (volume.get("hostPath") or {}).get("path")
        if not host_path:
            continue
        if "docker.sock" in host_path or "k3s.yaml" in host_path or "kubeconfig" in host_path.lower():
            fail(f"{name} mounts prohibited Kubernetes/Docker credential path {host_path}")
        if not host_path_allowed(name, host_path):
            fail(f"{name} hostPath outside allowlist: {host_path}")

    if name == "webui-frontend" and source.name == "webui-dev.yaml":
        host_names = {volume["name"] for volume in spec.get("volumes") or [] if volume.get("hostPath")}
        for container in spec.get("containers") or []:
            mounts = {mount["name"]: mount for mount in container.get("volumeMounts") or []}
            for host_name in host_names - {"host-localtime", "host-zoneinfo"}:
                if not mounts.get(host_name, {}).get("readOnly"):
                    fail(f"webui dev hostPath {host_name} must remain read-only")


def validate_postgres(objects: list[tuple[Path, dict]]) -> None:
    statefulsets = [obj for _, obj in objects if obj.get("kind") == "StatefulSet" and obj.get("metadata", {}).get("name") == "postgres-db"]
    if len(statefulsets) != 1:
        fail("expected one postgres-db StatefulSet")
    claims = statefulsets[0].get("spec", {}).get("volumeClaimTemplates") or []
    if len(claims) != 1:
        fail("postgres-db must retain one PVC template")
    if claims[0].get("spec", {}).get("storageClassName") != "borealis-longhorn":
        fail("postgres-db PVC lost Borealis Longhorn StorageClass")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifests", type=Path)
    args = parser.parse_args()
    if args.manifests:
        manifest_root = args.manifests
        objects = documents(manifest_root)
    else:
        with tempfile.TemporaryDirectory(prefix="borealis-k3s-policy-") as temp:
            manifest_root = Path(temp)
            render(manifest_root)
            objects = documents(manifest_root)
            validate(objects)
            return 0
    validate(objects)
    return 0


def validate(objects: list[tuple[Path, dict]]) -> None:
    validate_probe_conformance_contract()
    validate_cluster_controller_contract()
    validate_node_manager_service_contract()
    seen_workloads: set[tuple[str, str]] = set()
    for source, obj in objects:
        validate_labels(obj, source)
        kind = obj.get("kind")
        if kind == "Service":
            validate_service(obj)
        elif kind == "Role":
            validate_operator_role(obj)
        if pod_spec(obj) is not None:
            validate_workload(obj, source)
            seen_workloads.add((source.name, obj["metadata"]["name"]))
    required = {
        ("api-backend.yaml", "api-backend"),
        ("borealis-operator.yaml", "borealis-operator"),
        ("job-scheduler.yaml", "job-scheduler"),
        ("postgres-db.yaml", "postgres-db"),
        ("postgres-schema.yaml", "postgres-db-schema-initializer"),
        ("remote-desktop-guacd.yaml", "remote-desktop-guacd"),
        ("traefik-edge.yaml", "traefik-edge"),
        ("webui-dev.yaml", "webui-frontend"),
        ("webui-prod.yaml", "webui-frontend"),
        ("wireguard-tunnel.yaml", "wireguard-tunnel"),
    }
    if seen_workloads != required:
        fail(f"rendered workload set drifted: missing={sorted(required - seen_workloads)}, extra={sorted(seen_workloads - required)}")
    validate_postgres(objects)
    print(f"K3S POLICY PASS: {len(objects)} rendered objects satisfy namespace, RBAC, token, privilege, hostPath, network, Service, probe, and storage policies")


if __name__ == "__main__":
    raise SystemExit(main())
