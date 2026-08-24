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
    probes = {
        "startupProbe": "/startup",
        "readinessProbe": "/ready",
        "livenessProbe": "/live",
    }
    for probe_name, expected_path in probes.items():
        probe = containers[0].get(probe_name) or {}
        actual_path = ((probe.get("httpGet") or {}).get("path"))
        if actual_path != expected_path:
            fail(f"cluster controller {probe_name} must use distinct {expected_path} contract")


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
        "ReadWritePaths=/opt/Borealis /run/borealis /etc/borealis /etc/rancher/k3s": "fixed-operation Borealis and K3s write paths",
        "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK": "Unix socket, K3s API, release-fetch, and host network-inspection contract",
    }
    for marker, description in required.items():
        if marker not in source:
            fail(f"node-manager systemd unit lost {description}")
    if "Requires=k3s.service" in source:
        fail("node-manager must survive controlled K3s restarts during cluster enrollment and upgrades")
    try:
        engine_source = (ROOT / "Engine.sh").read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read Engine node-manager installer: {exc}")
    if 'install -d -m 0750 -o root -g root "$(dirname -- "${BOREALIS_NODE_MANAGER_TOKEN_FILE}")"' not in engine_source:
        fail("Engine node-manager installer must correct configuration-directory ownership and mode")
    if 'systemctl restart "${BOREALIS_NODE_MANAGER_SERVICE}"' not in engine_source:
        fail("Engine node-manager installer must restart service after replacing binary or unit")
    required_engine_cluster_markers = {
        "render_k3s_registries_config": "managed Spegel registry source configuration",
        "docker.io:": "Borealis local-image registry mirror",
        "registry.k8s.io:": "Kubernetes dependency registry mirror",
        "generic_k3s_workload_replicas": "zero-replica generic templates in cluster mode",
        "if ! cluster_mode_enabled; then": "cluster role-label ownership guard",
    }
    for marker, description in required_engine_cluster_markers.items():
        if marker not in engine_source:
            fail(f"Engine cluster baseline lost {description}")


def validate_longhorn_host_dependency_contract() -> None:
    try:
        engine_source = (ROOT / "Engine.sh").read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read Longhorn host dependency contract: {exc}")
    required = {
        "ensure_longhorn_nfs_package": "Longhorn RWX NFS prerequisite function",
        "apt-get install -y nfs-common": "Debian NFS client package",
        "dnf install -y nfs-utils": "RHEL NFS client package",
        "pacman -Sy --noconfirm nfs-utils": "Arch NFS client package",
        "zypper --non-interactive install nfs-client": "SUSE NFS client package",
        'command_exists mount.nfs || die': "post-install NFS mount helper check",
    }
    for marker, description in required.items():
        if marker not in engine_source:
            fail(f"Engine Longhorn baseline lost {description}")
    dependency_function = engine_source.find("ensure_longhorn_node_dependencies()")
    iscsi_call = engine_source.find("  ensure_longhorn_iscsi_package", dependency_function)
    nfs_call = engine_source.find("  ensure_longhorn_nfs_package", dependency_function)
    module_call = engine_source.find("  ensure_longhorn_iscsi_kernel_module", dependency_function)
    if min(dependency_function, iscsi_call, nfs_call, module_call) < 0 or not iscsi_call < nfs_call < module_call:
        fail("Longhorn baseline must reconcile iSCSI and NFS packages before kernel/service checks")


def validate_pinned_dependency_adoption_contract() -> None:
    path = ROOT / "Data/Engine/K3s/cluster/apply-pinned-dependencies.sh"
    try:
        source = path.read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read pinned dependency installer: {exc}")
    if "longhorn)" not in source or '${kubectl_bin} apply -f "${target}"' not in source:
        fail("cluster bootstrap must preserve standalone Longhorn client-side ownership during adoption")

    try:
        dependency_lock = (ROOT / "Data/Engine/K3s/cluster/dependencies.lock").read_text(encoding="utf-8")
        workflow = (ROOT / "Data/Engine/K3s/cluster/cluster-node-workflow.sh").read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read snapshot dependency contract: {exc}")
    for dependency in (
        "csi-volume-snapshot-class-crd|v8.5.0|",
        "csi-volume-snapshot-content-crd|v8.5.0|",
        "csi-volume-snapshot-crd|v8.5.0|",
    ):
        if dependency not in dependency_lock:
            fail(f"cluster dependency lock lost {dependency.rstrip('|')}")
    if "rollout restart deployment/cnpg-controller-manager" not in workflow:
        fail("cluster bootstrap must restart CNPG after installing snapshot CRDs")
    if 'k3s crictl inspecti "${api_image}"' not in workflow or "k3s ctr images list -q" in workflow:
        fail("cluster redeploy must resolve normalized image references through CRI")
    operator_stop = workflow.find("for deployment in api-backend borealis-operator job-scheduler borealis-cluster-controller")
    worker_stop = workflow.find("# Site workers are intentionally bare")
    database_dump = workflow.find("pg_dump -Fc")
    if min(operator_stop, worker_stop, database_dump) < 0 or not operator_stop < worker_stop < database_dump:
        fail("cluster database migration must stop operator and drain site workers before dump")
    if 'wait --for=condition=Ready "${worker}"' not in workflow:
        fail("cluster database migration must wait for drained site workers to return after cutover")
    if "get endpointslice" not in workflow or "get endpoints borealis-postgres-rw" in workflow:
        fail("cluster database migration must resolve ready PostgreSQL primary through EndpointSlice")
    if 'pg_restore --clean --if-exists --no-owner -h 127.0.0.1' not in workflow:
        fail("cluster database restore must use password-authenticated CloudNativePG loopback connection")
    if 'psql -h 127.0.0.1 -At -U "${postgres_user}"' not in workflow:
        fail("cluster database validation must use password-authenticated CloudNativePG loopback connection")
    for marker in (
        "automountServiceAccountToken: false",
        "securityContext: {seccompProfile: {type: RuntimeDefault}}",
        "readOnlyRootFilesystem: true",
        'capabilities: {add: ["CHOWN", "DAC_OVERRIDE", "FOWNER"], drop: ["ALL"]}',
        'if [ ! -f "\\${target}" ] || ! cmp -s "\\${source}" "\\${target}"',
        'cmp "\\${source}" "\\${target}"',
        "pod/borealis-agent-artifact-seed --timeout=25m",
    ):
        if marker not in workflow:
            fail(f"cluster Agent artifact seed lost security marker {marker!r}")


def validate_snapshot_controller_contract() -> None:
    path = ROOT / "Data/Engine/K3s/cluster/snapshot-controller.yaml"
    try:
        objects = [item for item in yaml.safe_load_all(path.read_text(encoding="utf-8")) if item]
    except (OSError, yaml.YAMLError) as exc:
        fail(f"cannot parse snapshot controller manifest: {exc}")
    deployments = [
        item
        for item in objects
        if item.get("kind") == "Deployment"
        and (item.get("metadata") or {}).get("name") == "snapshot-controller"
    ]
    if len(deployments) != 1:
        fail("snapshot controller manifest must contain one Deployment")
    deployment = deployments[0]
    if (deployment.get("spec") or {}).get("replicas") != 2:
        fail("snapshot controller must retain two leader-elected replicas")
    pod = (((deployment.get("spec") or {}).get("template") or {}).get("spec") or {})
    containers = pod.get("containers") or []
    if len(containers) != 1:
        fail("snapshot controller must contain one container")
    container = containers[0]
    if container.get("image") != "registry.k8s.io/sig-storage/snapshot-controller@sha256:74ca61ab13e978f03cf0f336a607281d15f04cda0a38a881306365473b28a3d8":
        fail("snapshot controller image must retain v8.5.0 manifest-list digest")
    if not container.get("startupProbe") or not container.get("readinessProbe") or not container.get("livenessProbe"):
        fail("snapshot controller must retain separate startup, readiness, and liveness probes")
    security = container.get("securityContext") or {}
    expected_security = {
        "allowPrivilegeEscalation": False,
        "readOnlyRootFilesystem": True,
        "runAsNonRoot": True,
        "runAsUser": 65532,
        "runAsGroup": 65532,
    }
    for field, expected in expected_security.items():
        if security.get(field) != expected:
            fail(f"snapshot controller securityContext {field} must be {expected!r}")
    if set(((security.get("capabilities") or {}).get("drop") or [])) != {"ALL"}:
        fail("snapshot controller must drop ALL capabilities")
    resources = container.get("resources") or {}
    if not resources.get("requests") or not resources.get("limits"):
        fail("snapshot controller must declare resource requests and limits")
    if not pod.get("topologySpreadConstraints"):
        fail("snapshot controller must retain topology spread constraints")
    pdbs = [item for item in objects if item.get("kind") == "PodDisruptionBudget"]
    if len(pdbs) != 1 or ((pdbs[0].get("spec") or {}).get("minAvailable")) != 1:
        fail("snapshot controller must retain minAvailable=1 disruption budget")
    for item in objects:
        labels = (item.get("metadata") or {}).get("labels") or {}
        if labels.get("app.kubernetes.io/part-of") != "borealis":
            fail(f"snapshot controller {item.get('kind')} lost Borealis ownership label")
        if item.get("kind") in {"ClusterRole", "Role"}:
            for rule in item.get("rules") or []:
                if "*" in (rule.get("resources") or []) or "*" in (rule.get("verbs") or []):
                    fail("snapshot controller RBAC contains wildcard")


def validate_kube_vip_contract() -> None:
    path = ROOT / "Data/Engine/K3s/cluster/kube-vip.yaml.in"
    try:
        objects = [item for item in yaml.safe_load_all(path.read_text(encoding="utf-8")) if item]
        workflow = (ROOT / "Data/Engine/K3s/cluster/cluster-node-workflow.sh").read_text(encoding="utf-8")
    except (OSError, yaml.YAMLError) as exc:
        fail(f"cannot read kube-vip contract: {exc}")
    daemonsets = {
        (item.get("metadata") or {}).get("name"): item
        for item in objects
        if item.get("kind") == "DaemonSet"
    }
    expected = {
        "kube-vip-borealis-control": {
            "address": "${BOREALIS_CONTROL_PLANE_VIP}",
            "port": "6443",
            "prometheus_server": ":2112",
            "health_check_port": "2114",
        },
        "kube-vip-borealis-edge": {
            "address": "${BOREALIS_EDGE_VIP}",
            "port": "443",
            "prometheus_server": ":2113",
            "health_check_port": "2115",
        },
    }
    if set(daemonsets) != set(expected):
        fail("kube-vip manifest must contain separate control and edge DaemonSets")
    for name, required_env in expected.items():
        daemonset = daemonsets[name]
        spec = daemonset.get("spec") or {}
        if spec.get("minReadySeconds") != 5:
            fail(f"{name} must retain five-second minimum readiness")
        pod = (((spec.get("template") or {}).get("spec")) or {})
        if pod.get("serviceAccountName") != "kube-vip-borealis" or pod.get("automountServiceAccountToken") is not True:
            fail(f"{name} must use dedicated kube-vip ServiceAccount")
        containers = pod.get("containers") or []
        if len(containers) != 1:
            fail(f"{name} must contain one kube-vip container")
        container = containers[0]
        env = {entry.get("name"): entry.get("value") for entry in container.get("env") or []}
        if "vip_address" in env:
            fail(f"{name} uses deprecated vip_address instead of kube-vip v1.1 address")
        for key, value in required_env.items():
            if env.get(key) != value:
                fail(f"{name} {key} must be {value!r}")
        for key, value in {"vip_subnet": "32", "cp_namespace": "kube-system", "vip_leaderelection": "true"}.items():
            if env.get(key) != value:
                fail(f"{name} {key} must be {value!r}")
        node_name = next((entry for entry in container.get("env") or [] if entry.get("name") == "vip_nodename"), {})
        if (((node_name.get("valueFrom") or {}).get("fieldRef") or {}).get("fieldPath")) != "spec.nodeName":
            fail(f"{name} must identify lease holder from spec.nodeName")
        if not container.get("startupProbe") or not container.get("readinessProbe") or not container.get("livenessProbe"):
            fail(f"{name} must retain separate startup, readiness, and liveness probes")
        resources = container.get("resources") or {}
        if not resources.get("requests") or not resources.get("limits"):
            fail(f"{name} must declare resource requests and limits")
    pdb_names = {
        (item.get("metadata") or {}).get("name")
        for item in objects
        if item.get("kind") == "PodDisruptionBudget" and ((item.get("spec") or {}).get("minAvailable")) == 1
    }
    if pdb_names != set(expected):
        fail("control and edge kube-vip workloads must retain minAvailable=1 disruption budgets")
    for marker in (
        'rollout status "daemonset/${daemonset}"',
        'get "lease/${lease}"',
        'kube-vip address %s not advertised',
        'get --raw=/readyz',
    ):
        if marker not in workflow:
            fail(f"cluster workflow lost kube-vip verification marker {marker!r}")


def validate_cluster_workload_handoff_contract() -> None:
    path = ROOT / "Data/Engine/K3s/cluster/reconcile-node-workloads.py"
    try:
        source = path.read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read cluster workload reconciler: {exc}")
    required = {
        "stopped_generic_host_workloads": "standalone host-port handoff tracking",
        'kubectl("-n", NAMESPACE, "scale", f"deployment/{base}", "--replicas=0")': "standalone host-port withdrawal",
        "for base, replicas in reversed(stopped_generic_host_workloads)": "failed initialization restoration",
        'kubectl("-n", NAMESPACE, "scale", f"deployment/{candidate_name}", "--replicas=0")': "candidate host-port withdrawal before promotion",
    }
    for marker, description in required.items():
        if marker not in source:
            fail(f"cluster workload reconciler lost {description}")


def validate_api_release_identity_contract() -> None:
    try:
        engine_source = (ROOT / "Engine.sh").read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read Engine API manifest renderer: {exc}")
    for marker in ('"release_version=${release_version}"', '"source_sha=${source_sha}"'):
        if marker not in engine_source:
            fail("API manifest cache must include immutable Engine release identity")


def validate_k3s_peer_allowlist_contract() -> None:
    try:
        engine_source = (ROOT / "Engine.sh").read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read Engine K3s peer allowlist contract: {exc}")
    required = {
        'elif [[ -r "${RUNTIME_ENV}" ]]': "existing readable runtime allowlist fallback",
        'BOREALIS_K3S_PEER_CIDRS=${K3S_PEER_CIDRS}': "runtime Secret persistence",
        'K3S_PEER_CIDRS="$(awk -F= \'$1 == "BOREALIS_K3S_PEER_CIDRS"': "cluster node runtime hydration",
        'Cluster runtime environment is missing BOREALIS_K3S_PEER_CIDRS.': "fail-closed cluster node redeploy",
        'networks.append(str(network))': "canonical private CIDR normalization",
        'cluster_prepare_node()': "fixed blank-node preparation entrypoint",
        '  ensure_longhorn_node_dependencies\n  write_k3s_borealis_config >/dev/null || true\n  write_k3s_registries_config >/dev/null || true\n  ensure_k3s_api_firewall\n  printf \'Cluster node host preparation complete.': "blank-node Longhorn, Spegel, and firewall preparation",
        'Engine.sh --cluster-prepare-node': "documented fixed node preparation command",
    }
    for marker, description in required.items():
        if marker not in engine_source:
            fail(f"Engine K3s peer allowlist lost {description}")


def validate_cluster_enable_vip_recovery_contract() -> None:
    try:
        workflow = (ROOT / "Data/Engine/K3s/cluster/cluster-node-workflow.sh").read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read cluster node workflow: {exc}")
    restart = 'k3s kubectl -n kube-system rollout restart "daemonset/${daemonset}"'
    rollout = 'k3s kubectl -n kube-system rollout status "daemonset/${daemonset}" --timeout=3m'
    address = 'for vip in "${control_vip}" "${edge_vip}"; do'
    if restart not in workflow or workflow.find(restart) > workflow.find(rollout) or workflow.find(rollout) > workflow.find(address):
        fail("cluster enable must restart kube-vip after K3s datastore conversion before address verification")


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
    validate_longhorn_host_dependency_contract()
    validate_pinned_dependency_adoption_contract()
    validate_snapshot_controller_contract()
    validate_kube_vip_contract()
    validate_cluster_workload_handoff_contract()
    validate_api_release_identity_contract()
    validate_k3s_peer_allowlist_contract()
    validate_cluster_enable_vip_recovery_contract()
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
