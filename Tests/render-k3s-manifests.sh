#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
OUTPUT_DIR="${1:-${BOREALIS_K3S_RENDER_OUTPUT_DIR:-${REPO_ROOT}/Unit_Test_Results/k3s-render}}"
FIXTURE_ROOT="$(mktemp -d)"
trap 'rm -rf "${FIXTURE_ROOT}"' EXIT

export BOREALIS_ENGINE_LIBRARY_MODE=1
export BOREALIS_ENGINE_RUNTIME_OWNER_UID=64646
export BOREALIS_ENGINE_RUNTIME_OWNER_GID=64646
# shellcheck source=Engine.sh
source "${REPO_ROOT}/Engine.sh"

RUNTIME_ROOT="${FIXTURE_ROOT}/Engine"
DEPLOY_DIR="${RUNTIME_ROOT}/Deploy"
COMPOSE_ENV="${DEPLOY_DIR}/compose.env"
RUNTIME_ENV="${DEPLOY_DIR}/runtime.env"
WEBUI_RUNTIME_SOURCE_DIR="${RUNTIME_ROOT}/Services/webui-frontend/data/web-interface"
mkdir -p "${DEPLOY_DIR}" "${OUTPUT_DIR}"

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
BOREALIS_DB_POOL_SIZE=5
BOREALIS_DB_MAX_OVERFLOW=2
BOREALIS_DB_CONNECT_TIMEOUT=5
BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS=30000
BOREALIS_INTERNAL_API_BASE_URL=http://api-backend.borealis.svc.cluster.local:5001
BOREALIS_ENGINE_SECRET_PATH=${RUNTIME_ROOT}/Services/api-backend/secrets/engine_secret.txt
BOREALIS_ENGINE_CERT_ROOT=${RUNTIME_ROOT}/Services/api-backend/secrets/Certificates
BOREALIS_ENGINE_AUTH_TOKEN_ROOT=${RUNTIME_ROOT}/Services/api-backend/secrets/Auth_Tokens
BOREALIS_ANSIBLE_RUNTIME_ROOT=${RUNTIME_ROOT}/Services/api-backend/cache/Ansible
BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH=${RUNTIME_ROOT}/Services/api-backend/config/ansible_runner_settings.json
BOREALIS_SITE_WORKER_SETTINGS_PATH=${RUNTIME_ROOT}/Services/api-backend/config/site_worker_settings.json
BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY=5
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
  "borealis/api-backend:fixture" "fixture-hash" "64646" "64646" "5001" "1Gi" "1" "k3s" \
  "2026.08.8.999" "0123456789abcdef0123456789abcdef01234567" \
  >"${OUTPUT_DIR}/api-backend.yaml"
render_k3s_job_scheduler_manifest \
  "borealis/job-scheduler:fixture" "borealis/site-worker:fixture" "fixture-hash" \
  "64646" "64646" "1Gi" "1" "http://api-backend.borealis.svc.cluster.local:5001" \
  >"${OUTPUT_DIR}/job-scheduler.yaml"
render_k3s_postgres_statefulset_manifest \
  "postgres:17-bookworm" "fixture-hash" "999" "999" "1Gi" "1" \
  "borealis-longhorn" "20Gi" "k3s" \
  >"${OUTPUT_DIR}/postgres-db.yaml"
render_k3s_postgres_schema_job_manifest \
  "borealis/api-backend:fixture" "fixture-hash" "64646" "64646" \
  >"${OUTPUT_DIR}/postgres-schema.yaml"
render_k3s_webui_frontend_bridge_manifest \
  "borealis/webui-frontend:fixture" "prod" "fixture-hash" "64646" "64646" "8000" "512Mi" "1" "k3s" \
  >"${OUTPUT_DIR}/webui-prod.yaml"
render_k3s_webui_frontend_bridge_manifest \
  "borealis/webui-frontend:fixture" "dev" "fixture-hash" "64646" "64646" "8000" "1Gi" "1" "k3s" \
  >"${OUTPUT_DIR}/webui-dev.yaml"
render_k3s_remote_desktop_guacd_bridge_manifest \
  "guacamole/guacd:1.6.0" "fixture-hash" "64646" "64646" "4822" "256Mi" "1" \
  >"${OUTPUT_DIR}/remote-desktop-guacd.yaml"
render_k3s_wireguard_tunnel_manifest \
  "borealis/wireguard-tunnel:fixture" "fixture-hash" "64646" "30000" "256Mi" "1" \
  >"${OUTPUT_DIR}/wireguard-tunnel.yaml"
render_k3s_traefik_edge_manifest \
  "traefik:v3.7" "fixture-hash" "64646" "8082" "256Mi" "1" \
  >"${OUTPUT_DIR}/traefik-edge.yaml"

printf 'Rendered production K3s manifests without host mutation: %s\n' "${OUTPUT_DIR}"
