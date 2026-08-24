#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/../../../.." && pwd)"
namespace="${BOREALIS_K3S_NAMESPACE:-borealis}"
result_file="${BOREALIS_K3S_PROBE_CONFORMANCE_FILE:-/var/lib/rancher/k3s/server/borealis-probe-conformance.json}"
operation="${1:-}"
control_vip="${2:-}"
edge_vip="${3:-}"
active_size="${BOREALIS_CLUSTER_ACTIVE_SIZE:-1}"
api_image="${BOREALIS_CLUSTER_API_IMAGE:-}"

[[ "${EUID}" -eq 0 ]] || { printf 'Cluster node workflow requires root.\n' >&2; exit 1; }
[[ "${operation}" == "enable" || "${operation}" == "redeploy" ]] || { printf 'Usage: cluster-node-workflow.sh <enable|redeploy> [control-vip] [edge-vip]\n' >&2; exit 64; }
[[ "${active_size}" == "1" || "${active_size}" == "3" || "${active_size}" == "5" ]] || { printf 'Active cluster size must be 1, 3, or 5.\n' >&2; exit 64; }

. /etc/os-release
[[ "${ID:-}" == "ubuntu" && "${VERSION_ID%%.*}" -ge 24 ]] || { printf 'Cluster nodes require Ubuntu 24.04 or newer.\n' >&2; exit 1; }
interface="$(ip -4 route show default | awk 'NR == 1 {print $5}')"
management_cidr="$(ip -o -4 address show dev "${interface}" scope global | awk 'NR == 1 {print $4}')"
[[ -n "${interface}" && -n "${management_cidr}" ]] || { printf 'Static IPv4 interface unavailable.\n' >&2; exit 1; }
if command -v networkctl >/dev/null 2>&1 && [[ "$(networkctl show "${interface}" -p DHCP4 --value 2>/dev/null || true)" == "yes" ]]; then
  printf 'Cluster nodes require static IPv4; %s reports DHCP4=yes.\n' "${interface}" >&2
  exit 1
fi

current_k3s_version="$(k3s --version | awk 'NR == 1 {print $3}')"
python3 - "${result_file}" "${current_k3s_version}" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
expected = sys.argv[2]
try:
    result = json.loads(path.read_text(encoding="utf-8"))
except Exception as exc:
    raise SystemExit(f"Stable K3s probe conformance result unavailable: {exc}")
if result.get("status") != "passed" or result.get("id") != "pod-restart-policy-startup-probe-v1" or result.get("k3s_version") != expected:
    raise SystemExit("Stable K3s probe conformance does not match running version; cluster mode remains disabled")
PY

if [[ "${operation}" == "redeploy" ]]; then
  [[ -n "${api_image}" ]] || { printf 'BOREALIS_CLUSTER_API_IMAGE required for node redeploy.\n' >&2; exit 64; }
  k3s ctr images list -q | grep -Fxq "${api_image}" || { printf 'Pinned API image missing from local K3s image store.\n' >&2; exit 1; }
  exit 0
fi

python3 - "${control_vip}" "${edge_vip}" <<'PY'
import ipaddress, sys
control = ipaddress.ip_address(sys.argv[1])
edge = ipaddress.ip_address(sys.argv[2])
if control.version != 4 or edge.version != 4 or control == edge:
    raise SystemExit("Distinct control-plane and edge IPv4 VIPs required")
PY

cluster_config_dir="/etc/rancher/k3s/config.yaml.d"
cluster_config="${cluster_config_dir}/30-borealis-cluster.yaml"
install -d -m 0700 "${cluster_config_dir}"
cluster_config_temp="$(mktemp)"
cat > "${cluster_config_temp}" <<EOF
cluster-init: true
tls-san:
  - ${control_vip}
EOF
install -m 0600 "${cluster_config_temp}" "${cluster_config}"
find "$(dirname -- "${cluster_config_temp}")" -maxdepth 1 -type f -name "$(basename -- "${cluster_config_temp}")" -delete
systemctl restart k3s.service
for attempt in {1..120}; do
  if systemctl is-active --quiet k3s.service \
    && k3s kubectl get node "$(hostname -s | tr '[:upper:]' '[:lower:]')" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null | grep -Fxq True; then
    break
  fi
  [[ "${attempt}" -lt 120 ]] || { printf 'K3s did not return Ready after embedded-etcd conversion.\n' >&2; exit 1; }
  sleep 2
done

"${script_dir}/apply-pinned-dependencies.sh"
k3s kubectl wait --for=condition=Established crd/volumesnapshotclasses.snapshot.storage.k8s.io --timeout=2m
k3s kubectl wait --for=condition=Established crd/volumesnapshotcontents.snapshot.storage.k8s.io --timeout=2m
k3s kubectl wait --for=condition=Established crd/volumesnapshots.snapshot.storage.k8s.io --timeout=2m
k3s kubectl apply --server-side --field-manager=borealis-cluster-bootstrap -f "${script_dir}/snapshot-controller.yaml"
k3s kubectl -n kube-system rollout status deployment/snapshot-controller --timeout=5m
# CNPG discovers VolumeSnapshot capability during startup. Restart also repairs a
# prior bootstrap attempt where operator started before snapshot CRDs existed.
k3s kubectl -n cnpg-system rollout restart deployment/cnpg-controller-manager
k3s kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=5m
k3s kubectl apply --server-side --field-manager=borealis-cluster-bootstrap -f "${script_dir}/aegis-mtls.yaml"
k3s kubectl -n "${namespace}" wait --for=condition=Ready certificate/borealis-cluster-ca --timeout=5m
k3s kubectl -n "${namespace}" wait --for=condition=Ready certificate/borealis-api-aegis-mtls --timeout=5m
k3s kubectl apply --server-side --field-manager=borealis-cluster-bootstrap -f "${script_dir}/crds.yaml"

vip_manifest="$(mktemp)"
sed -e "s|\${BOREALIS_CLUSTER_INTERFACE}|${interface}|g" \
    -e "s|\${BOREALIS_CONTROL_PLANE_VIP}|${control_vip}|g" \
    -e "s|\${BOREALIS_EDGE_VIP}|${edge_vip}|g" \
    "${script_dir}/kube-vip.yaml.in" > "${vip_manifest}"
k3s kubectl apply --server-side --field-manager=borealis-cluster-bootstrap -f "${vip_manifest}"
find "$(dirname -- "${vip_manifest}")" -maxdepth 1 -type f -name "$(basename -- "${vip_manifest}")" -delete
for daemonset in kube-vip-borealis-control kube-vip-borealis-edge; do
  k3s kubectl -n kube-system rollout status "daemonset/${daemonset}" --timeout=3m
done
for lease in borealis-control-vip borealis-edge-vip; do
  for attempt in {1..60}; do
    lease_holder="$(k3s kubectl -n kube-system get "lease/${lease}" -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)"
    [[ -n "${lease_holder}" ]] && break
    [[ "${attempt}" -lt 60 ]] || { printf 'kube-vip lease %s has no holder.\n' "${lease}" >&2; exit 1; }
    sleep 2
  done
done
for vip in "${control_vip}" "${edge_vip}"; do
  for attempt in {1..60}; do
    ip -o -4 address show dev "${interface}" | awk '{print $4}' | cut -d/ -f1 | grep -Fxq "${vip}" && break
    [[ "${attempt}" -lt 60 ]] || { printf 'kube-vip address %s not advertised on %s.\n' "${vip}" "${interface}" >&2; exit 1; }
    sleep 2
  done
done
k3s kubectl --server="https://${control_vip}:6443" get --raw=/readyz >/dev/null

postgres_user="$(awk -F= '$1 == "POSTGRES_USER" {print substr($0, index($0, "=") + 1); exit}' "${repo_root}/Engine/Deploy/runtime.env")"
postgres_password="$(awk -F= '$1 == "POSTGRES_PASSWORD" {print substr($0, index($0, "=") + 1); exit}' "${repo_root}/Engine/Deploy/runtime.env")"
postgres_database="$(awk -F= '$1 == "POSTGRES_DB" {print substr($0, index($0, "=") + 1); exit}' "${repo_root}/Engine/Deploy/runtime.env")"
[[ -n "${postgres_user}" && -n "${postgres_password}" && -n "${postgres_database}" ]] || { printf 'Existing PostgreSQL runtime credentials unavailable.\n' >&2; exit 1; }
k3s kubectl -n "${namespace}" create secret generic borealis-postgres-app \
  --from-literal=username="${postgres_user}" --from-literal=password="${postgres_password}" \
  --dry-run=client -o yaml | k3s kubectl apply -f -

postgres_manifest="$(mktemp)"
python3 - "${script_dir}/postgres.yaml" "${postgres_manifest}" "${active_size}" <<'PY'
import pathlib, sys
source, target, size = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), int(sys.argv[3])
text = source.read_text(encoding="utf-8").replace("instances: 3", f"instances: {size}", 1)
marker = "    # BOREALIS_SYNCHRONOUS_CONFIGURATION"
sync = ""
if size in (3, 5):
    acknowledgements = 1 if size == 3 else 2
    sync = "\n".join((
        "    synchronous:",
        "      method: any",
        f"      number: {acknowledgements}",
        "      dataDurability: required",
    ))
if marker not in text:
    raise SystemExit("CloudNativePG synchronous configuration marker missing")
target.write_text(text.replace(marker, sync), encoding="utf-8")
PY
k3s kubectl apply --server-side --field-manager=borealis-cluster-bootstrap -f "${postgres_manifest}"
find "$(dirname -- "${postgres_manifest}")" -maxdepth 1 -type f -name "$(basename -- "${postgres_manifest}")" -delete
k3s kubectl -n "${namespace}" wait --for=condition=Ready "cluster/borealis-postgres" --timeout=10m

migration_dir="${repo_root}/Engine/Backups/ClusterMigration"
install -d -m 0700 "${migration_dir}"
migration_dump="${migration_dir}/standalone-to-cnpg-$(date -u +%Y%m%dT%H%M%SZ).dump.enc"
engine_secret="${repo_root}/Engine/Services/api-backend/secrets/engine_secret.txt"
[[ -s "${engine_secret}" ]] || { printf 'Engine secret missing; encrypted migration dump cannot be created.\n' >&2; exit 1; }

if k3s kubectl -n "${namespace}" get statefulset/postgres-db >/dev/null 2>&1 && [[ "$(k3s kubectl -n "${namespace}" get statefulset/postgres-db -o jsonpath='{.spec.replicas}')" != "0" ]]; then
  declare -A original_replicas=()
  for deployment in api-backend job-scheduler borealis-cluster-controller; do
    if k3s kubectl -n "${namespace}" get "deployment/${deployment}" >/dev/null 2>&1; then
      original_replicas["${deployment}"]="$(k3s kubectl -n "${namespace}" get "deployment/${deployment}" -o jsonpath='{.spec.replicas}')"
      k3s kubectl -n "${namespace}" scale "deployment/${deployment}" --replicas=0
      k3s kubectl -n "${namespace}" wait --for=delete pod -l "app.kubernetes.io/name=${deployment}" --timeout=3m
    fi
  done
  restore_cluster_services() {
    local deployment=""
    for deployment in "${!original_replicas[@]}"; do
      k3s kubectl -n "${namespace}" scale "deployment/${deployment}" --replicas="${original_replicas[${deployment}]}" >/dev/null 2>&1 || true
    done
  }
  trap restore_cluster_services EXIT
  k3s kubectl -n "${namespace}" exec postgres-db-0 -- env PGPASSWORD="${postgres_password}" pg_dump -Fc -U "${postgres_user}" -d "${postgres_database}" \
    | openssl enc -aes-256-cbc -pbkdf2 -salt -pass file:"${engine_secret}" -out "${migration_dump}"
  primary_pod="$(k3s kubectl -n "${namespace}" get endpoints borealis-postgres-rw -o jsonpath='{.subsets[0].addresses[0].targetRef.name}')"
  [[ -n "${primary_pod}" ]] || { printf 'CloudNativePG primary pod unavailable.\n' >&2; exit 1; }
  openssl enc -d -aes-256-cbc -pbkdf2 -pass file:"${engine_secret}" -in "${migration_dump}" \
    | k3s kubectl -n "${namespace}" exec -i "${primary_pod}" -- pg_restore --clean --if-exists --no-owner -U "${postgres_user}" -d "${postgres_database}"
  source_counts="$(k3s kubectl -n "${namespace}" exec postgres-db-0 -- env PGPASSWORD="${postgres_password}" psql -At -U "${postgres_user}" -d "${postgres_database}" -c "ANALYZE; SELECT schemaname||'.'||relname||'='||n_live_tup FROM pg_stat_user_tables ORDER BY 1")"
  target_counts="$(k3s kubectl -n "${namespace}" exec "${primary_pod}" -- psql -At -U "${postgres_user}" -d "${postgres_database}" -c "ANALYZE; SELECT schemaname||'.'||relname||'='||n_live_tup FROM pg_stat_user_tables ORDER BY 1")"
  [[ "${source_counts}" == "${target_counts}" ]] || { printf 'CloudNativePG migrated table counts differ; standalone database remains active.\n' >&2; exit 1; }

  current_database_url="$(awk -F= '$1 == "BOREALIS_DATABASE_URL" {print substr($0, index($0, "=") + 1); exit}' "${repo_root}/Engine/Deploy/runtime.env")"
  new_database_url="$(python3 - "${current_database_url}" <<'PY'
import sys
from urllib.parse import urlsplit, urlunsplit
parsed = urlsplit(sys.argv[1])
userinfo = parsed.netloc.rsplit("@", 1)[0] + "@" if "@" in parsed.netloc else ""
port = f":{parsed.port}" if parsed.port else ""
print(urlunsplit((parsed.scheme, userinfo + "borealis-postgres-rw" + port, parsed.path, parsed.query, parsed.fragment)))
PY
)"
  [[ "${new_database_url}" == *"@borealis-postgres-rw"* || "${new_database_url}" == *"//borealis-postgres-rw"* ]] \
    || { printf 'Unable to construct CloudNativePG database URL.\n' >&2; exit 1; }
  encoded_database_url="$(printf '%s' "${new_database_url}" | base64 -w0)"
  for runtime_secret in borealis-api-backend-runtime-env borealis-job-scheduler-runtime-env borealis-site-worker-runtime-env; do
    if k3s kubectl -n "${namespace}" get "secret/${runtime_secret}" >/dev/null 2>&1; then
      k3s kubectl -n "${namespace}" patch "secret/${runtime_secret}" --type=merge -p "{\"data\":{\"BOREALIS_DATABASE_URL\":\"${encoded_database_url}\"}}"
    fi
  done
  runtime_temp="$(mktemp)"
  awk -v value="${new_database_url}" 'BEGIN {written=0} /^BOREALIS_DATABASE_URL=/ {print "BOREALIS_DATABASE_URL=" value; written=1; next} {print} END {if (!written) print "BOREALIS_DATABASE_URL=" value}' "${repo_root}/Engine/Deploy/runtime.env" > "${runtime_temp}"
  install -m 0600 "${runtime_temp}" "${repo_root}/Engine/Deploy/runtime.env"
  find "$(dirname -- "${runtime_temp}")" -maxdepth 1 -type f -name "$(basename -- "${runtime_temp}")" -delete

  k3s kubectl -n "${namespace}" scale statefulset/postgres-db --replicas=0
  restore_cluster_services
  trap - EXIT
  for deployment in api-backend job-scheduler borealis-cluster-controller; do
    if [[ -n "${original_replicas[${deployment}]:-}" && "${original_replicas[${deployment}]}" != "0" ]]; then
      k3s kubectl -n "${namespace}" rollout status "deployment/${deployment}" --timeout=5m
    fi
  done
fi

[[ -n "${api_image}" ]] || { printf 'BOREALIS_CLUSTER_API_IMAGE required to seed shared Agent artifacts.\n' >&2; exit 64; }
k3s kubectl -n "${namespace}" wait --for=jsonpath='{.status.phase}'=Bound persistentvolumeclaim/borealis-agent-artifacts --timeout=5m >/dev/null 2>&1 \
  || k3s kubectl -n "${namespace}" wait --for=jsonpath='{.status.phase}'=Bound pvc/borealis-agent-artifacts --timeout=5m
k3s kubectl -n "${namespace}" delete pod/borealis-agent-artifact-seed --ignore-not-found=true --wait=true
k3s kubectl -n "${namespace}" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: borealis-agent-artifact-seed
  labels: {app.kubernetes.io/name: borealis-agent-artifact-seed, app.kubernetes.io/part-of: borealis}
spec:
  restartPolicy: Never
  nodeName: $(hostname -s | tr '[:upper:]' '[:lower:]')
  automountServiceAccountToken: false
  containers:
    - name: seed
      image: ${api_image}
      imagePullPolicy: IfNotPresent
      command: ["/bin/sh", "-c", "cp -a /source/. /target/"]
      securityContext: {allowPrivilegeEscalation: false, runAsUser: 0, runAsGroup: 0}
      volumeMounts:
        - {name: source, mountPath: /source, readOnly: true}
        - {name: target, mountPath: /target}
  volumes:
    - name: source
      hostPath: {path: ${repo_root}/Engine/Services/api-backend/cache/AgentUpdates, type: Directory}
    - name: target
      persistentVolumeClaim: {claimName: borealis-agent-artifacts}
EOF
k3s kubectl -n "${namespace}" wait --for=jsonpath='{.status.phase}'=Succeeded pod/borealis-agent-artifact-seed --timeout=5m
k3s kubectl -n "${namespace}" delete pod/borealis-agent-artifact-seed --wait=true

k3s kubectl -n "${namespace}" apply -f - <<EOF
apiVersion: engine.borealis.io/v1alpha1
kind: BorealisCluster
metadata:
  name: borealis
  labels: {app.kubernetes.io/part-of: borealis}
spec:
  activeSize: ${active_size}
  desiredSize: ${active_size}
  controlPlaneVIP: ${control_vip}
  edgeVIP: ${edge_vip}
EOF

node_name="${BOREALIS_CLUSTER_NODE_NAME:-$(hostname -s | tr '[:upper:]' '[:lower:]')}"
revision="$(git -C "${repo_root}" rev-parse HEAD)"
python3 "${script_dir}/reconcile-node-workloads.py" \
  --node "${node_name}" \
  --revision "${revision}" \
  --image-manifest "${repo_root}/Engine/Deploy/image-manifest.json" \
  --initialize

printf 'Cluster foundation active. Old PostgreSQL PVC and encrypted dump retained at %s.\n' "${migration_dump}"
