#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
OUTPUT_DIR="${1:-${BOREALIS_K3S_RENDER_OUTPUT_DIR:-${REPO_ROOT}/Unit_Test_Results/k3s-render}}"
RENDER_PROFILE="${2:-fixture}"
case "${RENDER_PROFILE}" in
  fixture|small-business) ;;
  *) printf 'Unsupported manifest fixture profile: %s\n' "${RENDER_PROFILE}" >&2; exit 2 ;;
esac
FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "${FIXTURE_ROOT}"' EXIT

export BOREALIS_ENGINE_LIBRARY_MODE=1
export BOREALIS_ENGINE_RUNTIME_OWNER_UID=64646
export BOREALIS_ENGINE_RUNTIME_OWNER_GID=64646
# Keep library initialization away from any retained runtime environment.
export BOREALIS_ENGINE_HOST_ROOT="${FIXTURE_ROOT}"
export BOREALIS_ENGINE_RUNTIME_ROOT="${FIXTURE_ROOT}/Engine"
# shellcheck source=Engine.sh
source "${REPO_ROOT}/Engine.sh"

RUNTIME_ROOT="${FIXTURE_ROOT}/Engine"
DEPLOY_DIR="${RUNTIME_ROOT}/Deploy"
COMPOSE_ENV="${DEPLOY_DIR}/compose.env"
RUNTIME_ENV="${DEPLOY_DIR}/runtime.env"
WEBUI_RUNTIME_SOURCE_DIR="${RUNTIME_ROOT}/Services/webui-frontend/data/web-interface"
mkdir -p "${DEPLOY_DIR}" "${OUTPUT_DIR}"

if [[ "${RENDER_PROFILE}" == small-business ]]; then
  # Qualification fixture: inherited rank 1 / 16 GiB reference, no overrides.
  # Optional CPU/RAM arguments exercise stronger joining hosts, not new tuning.
  load_profile_tuning "${3:-8}" "${4:-16384}" 1 16384
  printf '{"rank":%s,"reference_memory_mib":%s,"scheduled_slots":%s}\n' \
    "${PROFILE_RANK}" "${PROFILE_CLUSTER_SIZING_MEMORY_MIB}" "${PROFILE_SITE_WORKER_CONCURRENCY}" \
    >"${OUTPUT_DIR}/profile.json"
fi

fixture_limit() {
  local kind="$1" fallback="$2" variable="$3"
  if [[ "${RENDER_PROFILE}" == small-business ]]; then
    "format_k3s_${kind}_quantity" "${!variable}"
  else
    printf '%s\n' "${fallback}"
  fi
}

cat >"${COMPOSE_ENV}" <<EOF
BOREALIS_PROJECT_ROOT=${FIXTURE_ROOT}
BOREALIS_ENGINE_MODE=production
BOREALIS_ENGINE_CONTAINERIZED=1
BOREALIS_ENGINE_HOST_TIMEZONE=UTC
TZ=UTC
POSTGRES_DB=borealis
POSTGRES_USER=borealis
POSTGRES_PASSWORD=fixture-password
BOREALIS_DATABASE_URL=postgresql://borealis:fixture-password@postgres-db.borealis.svc:5432/borealis
BOREALIS_DB_SSLMODE=disable
BOREALIS_DB_POOL_SIZE=${PROFILE_DB_POOL_SIZE:-5}
BOREALIS_DB_MAX_OVERFLOW=${PROFILE_DB_MAX_OVERFLOW:-2}
BOREALIS_DB_CONNECT_TIMEOUT=5
BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS=30000
BOREALIS_INTERNAL_API_BASE_URL=http://api-backend.borealis.svc.cluster.local:5001
BOREALIS_ENGINE_SECRET_PATH=${RUNTIME_ROOT}/Services/api-backend/secrets/engine_secret.txt
BOREALIS_ENGINE_CERT_ROOT=${RUNTIME_ROOT}/Services/api-backend/secrets/Certificates
BOREALIS_ENGINE_AUTH_TOKEN_ROOT=${RUNTIME_ROOT}/Services/api-backend/secrets/Auth_Tokens
BOREALIS_ANSIBLE_RUNTIME_ROOT=${RUNTIME_ROOT}/Services/api-backend/cache/Ansible
BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH=${RUNTIME_ROOT}/Services/api-backend/config/ansible_runner_settings.json
BOREALIS_SITE_WORKER_SETTINGS_PATH=${RUNTIME_ROOT}/Services/api-backend/config/site_worker_settings.json
BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY=${PROFILE_SITE_WORKER_CONCURRENCY:-5}
BOREALIS_SITE_WORKER_ANSIBLE_CONCURRENCY=2
BOREALIS_OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT=${RUNTIME_ROOT}/Services/api-backend/cache/Aurora
BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE=eventlet
BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS=300
BOREALIS_GUACD_HOST=remote-desktop-guacd.borealis.svc
BOREALIS_GUACD_PORT=4822
BOREALIS_GUACAMOLE_VNC_WS_PATH=/vnc
BOREALIS_PUBLIC_HOSTNAME=borealis.example.test
BOREALIS_PUBLIC_HOSTNAME_ALIASES=
BOREALIS_PUBLIC_BASE_URL=https://borealis.example.test
BOREALIS_PUBLIC_HTTP_PORT=80
BOREALIS_PUBLIC_HTTPS_PORT=443
BOREALIS_PUBLIC_VNC_PATH=/vnc
BOREALIS_PUBLIC_WIREGUARD_HOST=borealis.example.test
BOREALIS_PUBLIC_WIREGUARD_PORT=30000
BOREALIS_ENGINE_NETWORK_MODE=local
BOREALIS_ENGINE_NETWORK_MODE_LABEL=Local
BOREALIS_ENGINE_DEPLOYMENT_PROFILE=internal-only
BOREALIS_ENGINE_DEPLOYMENT_PROFILE_LABEL=Local
BOREALIS_ACME_EMAIL=
BOREALIS_LOCAL_CA_ENABLED=1
BOREALIS_LOCAL_CA_CERT_PATH=${RUNTIME_ROOT}/Services/traefik-edge/state/local-ca/ca.crt
BOREALIS_LOCAL_TLS_CERT_PATH=${RUNTIME_ROOT}/Services/traefik-edge/state/local-certs/tls.crt
BOREALIS_LOCAL_TLS_KEY_PATH=${RUNTIME_ROOT}/Services/traefik-edge/state/local-certs/tls.key
BOREALIS_WEBUI_TRAFFIC_OWNER=k3s
BOREALIS_WEBUI_UPSTREAM_HOST=webui-frontend.borealis.svc.cluster.local
BOREALIS_WEBUI_UPSTREAM_PORT=8000
BOREALIS_API_BACKEND_TRAFFIC_OWNER=k3s
BOREALIS_API_BACKEND_UPSTREAM_HOST=api-backend.borealis.svc.cluster.local
BOREALIS_API_BACKEND_UPSTREAM_PORT=5001
BOREALIS_VNC_WS_PORT=4822
BOREALIS_TRAEFIK_HEALTH_PORT=8082
BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS=
BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS=
BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS=
BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR=${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic
BOREALIS_TRAEFIK_DYNAMIC_CONFIG_PATH=${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic/core.yml
BOREALIS_ENGINE_RUNTIME_OWNER_UID=64646
BOREALIS_ENGINE_RUNTIME_OWNER_GID=64646
EOF
cp "${COMPOSE_ENV}" "${RUNTIME_ENV}"

render_borealis_longhorn_storage_class_manifest "borealis-longhorn" "1" >"${OUTPUT_DIR}/longhorn-storage.yaml"
render_borealis_operator_manifest \
  "borealis/borealis-operator:fixture" "fixture-operator-secret" "fixture-hash" \
  "borealis/api-backend:fixture,borealis/job-scheduler:fixture" \
  "borealis/site-worker:fixture" "fixture-runtime-hash" \
  >"${OUTPUT_DIR}/borealis-operator.yaml"
render_k3s_api_backend_bridge_manifest \
  "borealis/api-backend:fixture" "fixture-hash" "64646" "64646" "5001" \
  "$(fixture_limit memory 1Gi PROFILE_API_BACKEND_MEMORY_LIMIT)" "$(fixture_limit cpu 1 PROFILE_API_BACKEND_CPU_LIMIT)" "k3s" \
  "2026.08.8.999" "0123456789abcdef0123456789abcdef01234567" "passed" "v1.36.3+k3s1" \
  >"${OUTPUT_DIR}/api-backend.yaml"
render_k3s_job_scheduler_manifest \
  "borealis/job-scheduler:fixture" "borealis/site-worker:fixture" "fixture-hash" \
  "64646" "64646" "$(fixture_limit memory 1Gi PROFILE_JOB_SCHEDULER_MEMORY_LIMIT)" \
  "$(fixture_limit cpu 1 PROFILE_JOB_SCHEDULER_CPU_LIMIT)" "http://api-backend.borealis.svc.cluster.local:5001" \
  >"${OUTPUT_DIR}/job-scheduler.yaml"
render_k3s_postgres_statefulset_manifest \
  "postgres:17-bookworm" "fixture-hash" "999" "999" \
  "$(fixture_limit memory 1Gi PROFILE_POSTGRES_DB_MEMORY_LIMIT)" "$(fixture_limit cpu 1 PROFILE_POSTGRES_DB_CPU_LIMIT)" \
  "borealis-longhorn" "20Gi" "k3s" \
  >"${OUTPUT_DIR}/postgres-db.yaml"
render_k3s_postgres_schema_job_manifest \
  "borealis/api-backend:fixture" "fixture-hash" "64646" "64646" \
  >"${OUTPUT_DIR}/postgres-schema.yaml"
render_cluster_schema_phase_job_manifest \
  "borealis-schema-expand-0123456789ab" "borealis/site-worker:fixture" "engine-1" "expand" \
  "0123456789abcdef0123456789abcdef01234567" "64646" "64646" \
  >"${OUTPUT_DIR}/cluster-schema.yaml"
render_k3s_webui_frontend_bridge_manifest \
  "borealis/webui-frontend:fixture" "prod" "fixture-hash" "64646" "64646" "8000" \
  "$(fixture_limit memory 512Mi PROFILE_WEBUI_FRONTEND_MEMORY_LIMIT)" "$(fixture_limit cpu 1 PROFILE_WEBUI_FRONTEND_CPU_LIMIT)" "k3s" \
  >"${OUTPUT_DIR}/webui-prod.yaml"
render_k3s_webui_frontend_bridge_manifest \
  "borealis/webui-frontend:fixture" "dev" "fixture-hash" "64646" "64646" "8000" \
  "$(fixture_limit memory 1Gi PROFILE_WEBUI_FRONTEND_DEV_MEMORY_LIMIT)" "$(fixture_limit cpu 1 PROFILE_WEBUI_FRONTEND_DEV_CPU_LIMIT)" "k3s" \
  >"${OUTPUT_DIR}/webui-dev.yaml"
render_k3s_remote_desktop_guacd_bridge_manifest \
  "guacamole/guacd:1.6.0" "fixture-hash" "64646" "64646" "4822" \
  "$(fixture_limit memory 256Mi PROFILE_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT)" "$(fixture_limit cpu 1 PROFILE_REMOTE_DESKTOP_GUACD_CPU_LIMIT)" \
  >"${OUTPUT_DIR}/remote-desktop-guacd.yaml"
render_k3s_wireguard_tunnel_manifest \
  "borealis/wireguard-tunnel:fixture" "fixture-hash" "64646" "30000" \
  "$(fixture_limit memory 256Mi PROFILE_WIREGUARD_TUNNEL_MEMORY_LIMIT)" "$(fixture_limit cpu 1 PROFILE_WIREGUARD_TUNNEL_CPU_LIMIT)" \
  >"${OUTPUT_DIR}/wireguard-tunnel.yaml"
render_k3s_traefik_edge_manifest \
  "traefik:v3.7" "fixture-hash" "64646" "8082" \
  "$(fixture_limit memory 256Mi PROFILE_TRAEFIK_EDGE_MEMORY_LIMIT)" "$(fixture_limit cpu 1 PROFILE_TRAEFIK_EDGE_CPU_LIMIT)" \
  >"${OUTPUT_DIR}/traefik-edge.yaml"

printf 'Rendered production K3s manifests without host mutation: %s\n' "${OUTPUT_DIR}"
