#!/usr/bin/env bash
# Borealis Engine container deploy controller.

set -o errexit
set -o nounset
set -o pipefail

if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then
  SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
else
  SCRIPT_DIR="$(pwd)"
fi
cd "${SCRIPT_DIR}"

PROJECT_NAME="borealis-engine"
CONTAINER_SOURCE_DIR="${SCRIPT_DIR}/Data/Engine/Containers"
COMPOSE_FILE="${CONTAINER_SOURCE_DIR}/compose.yaml"
BUILD_MANIFEST="${CONTAINER_SOURCE_DIR}/build-manifest.json"
ENV_EXAMPLE="${CONTAINER_SOURCE_DIR}/compose.env.example"
ENGINE_HOST_ROOT="${BOREALIS_ENGINE_HOST_ROOT:-${SCRIPT_DIR}}"
RUNTIME_ROOT="${BOREALIS_ENGINE_RUNTIME_ROOT:-${ENGINE_HOST_ROOT}/Engine}"
AGENT_STAGED_SOURCE_DIR="${SCRIPT_DIR}/Data/Agent"
AGENT_UPDATE_CACHE_ROOT="${RUNTIME_ROOT}/Services/api-backend/cache/AgentUpdates"
WEBUI_STAGED_SOURCE_DIR="${CONTAINER_SOURCE_DIR}/webui-frontend/data/web-interface"
WEBUI_RUNTIME_SOURCE_DIR="${RUNTIME_ROOT}/Services/webui-frontend/data/web-interface"
DEPLOY_DIR="${RUNTIME_ROOT}/Deploy"
COMPOSE_ENV="${DEPLOY_DIR}/compose.env"
RUNTIME_ENV="${DEPLOY_DIR}/runtime.env"
WEBUI_ENV="${DEPLOY_DIR}/webui-frontend.env"
IMAGE_MANIFEST="${DEPLOY_DIR}/image-manifest.json"
CLUSTER_STAGED_REVISION_FILE="${DEPLOY_DIR}/cluster-staged-revision"
DEPLOY_MANIFEST="${DEPLOY_DIR}/deploy-manifest.json"
BUILD_LOG="${DEPLOY_DIR}/build.log"
K3S_NAMESPACE="${BOREALIS_K3S_NAMESPACE:-borealis}"
K3S_SERVICE_NAME="${BOREALIS_K3S_SERVICE_NAME:-k3s}"
K3S_CONFIG_DIR="${BOREALIS_K3S_CONFIG_DIR:-/etc/rancher/k3s/config.yaml.d}"
K3S_BOREALIS_CONFIG="${BOREALIS_K3S_CONFIG_PATH:-${K3S_CONFIG_DIR}/10-borealis.yaml}"
K3S_REGISTRIES_CONFIG="${BOREALIS_K3S_REGISTRIES_PATH:-/etc/rancher/k3s/registries.yaml}"
K3S_KUBECONFIG="${BOREALIS_K3S_KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"
K3S_IMAGE_IMPORT_DIR="${BOREALIS_K3S_IMAGE_IMPORT_DIR:-/var/lib/rancher/k3s/agent/images}"
K3S_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-baseline.sha256"
K3S_CLUSTER_ASSET_DIR="${SCRIPT_DIR}/Data/Engine/K3s/cluster"
K3S_PROBE_CONFORMANCE_FILE="${BOREALIS_K3S_PROBE_CONFORMANCE_FILE:-/etc/rancher/k3s/borealis-probe-conformance.json}"
BOREALIS_NODE_MANAGER_BINARY="${BOREALIS_NODE_MANAGER_BINARY:-/usr/local/sbin/borealis-node-manager}"
BOREALIS_NODE_MANAGER_TOKEN_FILE="${BOREALIS_NODE_MANAGER_TOKEN_FILE:-/etc/borealis/node-manager.token}"
BOREALIS_NODE_MANAGER_SERVICE="${BOREALIS_NODE_MANAGER_SERVICE:-borealis-node-manager.service}"
K3S_FIREWALL_SCRIPT="${BOREALIS_K3S_FIREWALL_SCRIPT:-/usr/local/lib/borealis/k3s-api-firewall.sh}"
K3S_FIREWALL_SERVICE="${BOREALIS_K3S_FIREWALL_SERVICE:-borealis-k3s-api-firewall.service}"
K3S_API_PORT="${BOREALIS_K3S_API_PORT:-6443}"
if [[ -n "${BOREALIS_K3S_PEER_CIDRS+x}" ]]; then
  K3S_PEER_CIDRS="${BOREALIS_K3S_PEER_CIDRS}"
elif [[ -r "${RUNTIME_ENV}" ]]; then
  K3S_PEER_CIDRS="$(awk -F= '$1 == "BOREALIS_K3S_PEER_CIDRS" {print substr($0, index($0, "=") + 1); exit}' "${RUNTIME_ENV}")"
else
  K3S_PEER_CIDRS=""
fi
K3S_CLUSTER_CIDR="${BOREALIS_K3S_CLUSTER_CIDR:-10.42.0.0/16}"
K3S_SERVICE_CIDR="${BOREALIS_K3S_SERVICE_CIDR:-10.43.0.0/16}"
K3S_KUBECONFIG_MODE="${BOREALIS_K3S_KUBECONFIG_MODE:-0600}"
K3S_CONTAINER_LOG_MAX_SIZE="${BOREALIS_K3S_CONTAINER_LOG_MAX_SIZE:-}"
K3S_CONTAINER_LOG_MAX_FILES="${BOREALIS_K3S_CONTAINER_LOG_MAX_FILES:-}"
K3S_INSTALL_SCRIPT_URL="${BOREALIS_K3S_INSTALL_SCRIPT_URL:-https://get.k3s.io}"
K3S_INSTALL_CHANNEL="${BOREALIS_K3S_INSTALL_CHANNEL:-stable}"
K3S_INSTALL_VERSION="${BOREALIS_K3S_INSTALL_VERSION:-v1.36.3+k3s1}"
K3S_UPGRADE_IMAGE="${BOREALIS_K3S_UPGRADE_IMAGE:-}"
if [[ -n "${K3S_UPGRADE_IMAGE}" && ! "${K3S_UPGRADE_IMAGE}" =~ ^[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[0-9a-f]{64}$ ]]; then
  printf 'BOREALIS_K3S_UPGRADE_IMAGE must use immutable registry/repository@sha256:digest form.\n' >&2
  exit 64
fi
K3S_BASELINE_VERSION="1"
K3S_LONGHORN_ENABLED="${BOREALIS_K3S_LONGHORN_ENABLED:-1}"
K3S_LONGHORN_NAMESPACE="${BOREALIS_K3S_LONGHORN_NAMESPACE:-longhorn-system}"
K3S_LONGHORN_VERSION="${BOREALIS_K3S_LONGHORN_VERSION:-v1.12.0}"
K3S_LONGHORN_MANIFEST_URL="${BOREALIS_K3S_LONGHORN_MANIFEST_URL:-https://raw.githubusercontent.com/longhorn/longhorn/${K3S_LONGHORN_VERSION}/deploy/longhorn.yaml}"
K3S_LONGHORN_ROLLOUT_TIMEOUT="${BOREALIS_K3S_LONGHORN_ROLLOUT_TIMEOUT:-300s}"
K3S_LONGHORN_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-longhorn.sha256"
K3S_LONGHORN_UPSTREAM_STORAGE_CLASS="${BOREALIS_K3S_LONGHORN_UPSTREAM_STORAGE_CLASS:-longhorn}"
K3S_BOREALIS_LONGHORN_STORAGE_CLASS="${BOREALIS_K3S_BOREALIS_LONGHORN_STORAGE_CLASS:-borealis-longhorn}"
K3S_BOREALIS_LONGHORN_REPLICA_COUNT="${BOREALIS_K3S_BOREALIS_LONGHORN_REPLICA_COUNT:-1}"
K3S_PVC_STORAGE_CLASS="${BOREALIS_K3S_PVC_STORAGE_CLASS:-${BOREALIS_K3S_STORAGE_CLASS:-${K3S_BOREALIS_LONGHORN_STORAGE_CLASS}}}"
ENGINE_FILE_LOG_RETENTION_DAYS="${BOREALIS_ENGINE_FILE_LOG_RETENTION_DAYS:-30}"
BOREALIS_OPERATOR_SERVICE_NAME="${BOREALIS_OPERATOR_SERVICE_NAME:-borealis-operator}"
BOREALIS_OPERATOR_SECRET_NAME="${BOREALIS_OPERATOR_SECRET_NAME:-borealis-operator-auth}"
BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME="${BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME:-borealis-site-worker-runtime-env}"
BOREALIS_API_BACKEND_RUNTIME_SECRET_NAME="${BOREALIS_API_BACKEND_RUNTIME_SECRET_NAME:-borealis-api-backend-runtime-env}"
BOREALIS_API_BACKEND_SHADOW_DB_RUNTIME_SECRET_NAME="${BOREALIS_API_BACKEND_SHADOW_DB_RUNTIME_SECRET_NAME:-borealis-api-backend-shadow-db-runtime-env}"
BOREALIS_JOB_SCHEDULER_RUNTIME_SECRET_NAME="${BOREALIS_JOB_SCHEDULER_RUNTIME_SECRET_NAME:-borealis-job-scheduler-runtime-env}"
BOREALIS_WIREGUARD_TUNNEL_RUNTIME_SECRET_NAME="${BOREALIS_WIREGUARD_TUNNEL_RUNTIME_SECRET_NAME:-borealis-wireguard-tunnel-runtime-env}"
BOREALIS_TRAEFIK_EDGE_RUNTIME_SECRET_NAME="${BOREALIS_TRAEFIK_EDGE_RUNTIME_SECRET_NAME:-borealis-traefik-edge-runtime-env}"
BOREALIS_API_BACKEND_K3S_BRIDGE_PORT="${BOREALIS_API_BACKEND_K3S_BRIDGE_PORT:-5001}"
BOREALIS_OPERATOR_PORT="${BOREALIS_OPERATOR_PORT:-8088}"
BOREALIS_OPERATOR_CONFIG_HASH_FILE="${DEPLOY_DIR}/borealis-operator.sha256"
K3S_API_BACKEND_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-api-backend.sha256"
K3S_BRIDGE_WORKLOADS_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-bridge-workloads.sha256"
K3S_WEBUI_FRONTEND_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-webui-frontend.sha256"
K3S_REMOTE_DESKTOP_GUACD_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-remote-desktop-guacd.sha256"
K3S_JOB_SCHEDULER_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-job-scheduler.sha256"
K3S_WIREGUARD_TUNNEL_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-wireguard-tunnel.sha256"
K3S_TRAEFIK_EDGE_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-traefik-edge.sha256"
K3S_POSTGRES_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-postgres-db.sha256"
K3S_POSTGRES_SCHEMA_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-postgres-schema.sha256"
K3S_SITE_WORKER_RUNTIME_CONFIG_HASH_FILE="${DEPLOY_DIR}/k3s-site-worker-runtime-env.sha256"
BOREALIS_POSTGRES_RUNTIME_SECRET_NAME="${BOREALIS_POSTGRES_RUNTIME_SECRET_NAME:-borealis-postgres-runtime-env}"
K3S_API_BACKEND_BRIDGE_VERSION="4"
K3S_API_BACKEND_DB_VALIDATION_VERSION="3"
K3S_API_BACKEND_DB_VALIDATOR_JOB_NAME="${BOREALIS_K3S_API_BACKEND_DB_VALIDATOR_JOB_NAME:-api-backend-shadow-db-validator}"
K3S_API_BACKEND_DB_VALIDATION_TIMEOUT="${BOREALIS_K3S_API_BACKEND_DB_VALIDATION_TIMEOUT:-120s}"
K3S_BRIDGE_WORKLOADS_VERSION="4"
K3S_WEBUI_FRONTEND_VERSION="1"
K3S_REMOTE_DESKTOP_GUACD_VERSION="1"
K3S_JOB_SCHEDULER_VERSION="4"
K3S_WIREGUARD_TUNNEL_VERSION="1"
K3S_TRAEFIK_EDGE_VERSION="1"
K3S_POSTGRES_VERSION="3"
K3S_POSTGRES_SCHEMA_JOB_NAME="${BOREALIS_K3S_POSTGRES_SCHEMA_JOB_NAME:-postgres-db-schema-initializer}"
K3S_POSTGRES_SCHEMA_TIMEOUT="${BOREALIS_K3S_POSTGRES_SCHEMA_TIMEOUT:-180s}"
K3S_POSTGRES_ENABLED="${BOREALIS_K3S_POSTGRES_ENABLED:-1}"
K3S_POSTGRES_STORAGE_SIZE="${BOREALIS_K3S_POSTGRES_STORAGE_SIZE:-20Gi}"
K3S_POSTGRES_ROLLOUT_TIMEOUT="${BOREALIS_K3S_POSTGRES_ROLLOUT_TIMEOUT:-180s}"
BUILD_CACHE_RETENTION_DAYS=7
GUM_VERSION="v0.17.0"
GUM_VERSION_NUMBER="${GUM_VERSION#v}"
GUM_RELEASE_BASE_URL="${BOREALIS_GUM_RELEASE_BASE_URL:-https://github.com/charmbracelet/gum/releases/download/${GUM_VERSION}}"
GUM_LINUX_X86_64_SHA256="69ee169bd6387331928864e94d47ed01ef649fbfe875baed1bbf27b5377a6fdb"
GUM_LINUX_ARM64_SHA256="b0b9ed95cbf7c8b7073f17b9591811f5c001e33c7cfd066ca83ce8a07c576f9c"
GUM_BIN="${BOREALIS_GUM_BIN:-}"
GUM_READY=0
DEFAULT_INSTALL_DIR="/opt/Borealis"
DEFAULT_REPO_URL="https://github.com/bunny-lab-io/Borealis.git"
DEFAULT_REPO_REF="main"
DEFAULT_RELEASE_CHANNEL="${BOREALIS_ENGINE_RELEASE_CHANNEL:-unstable}"
DEFAULT_STABLE_REF="${BOREALIS_ENGINE_STABLE_REF:-}"
DEFAULT_UNSTABLE_REF="${BOREALIS_ENGINE_UNSTABLE_REF:-${DEFAULT_REPO_REF}}"
INSTALL_DIR="${BOREALIS_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"
ENGINE_RUNTIME_USER="${BOREALIS_ENGINE_RUNTIME_USER:-borealis-engine}"
ENGINE_RUNTIME_GROUP="${BOREALIS_ENGINE_RUNTIME_GROUP:-borealis-engine}"
ENGINE_RUNTIME_UID="${BOREALIS_ENGINE_RUNTIME_UID:-64646}"
ENGINE_RUNTIME_GID="${BOREALIS_ENGINE_RUNTIME_GID:-64646}"
POSTGRES_RUNTIME_UID="${BOREALIS_POSTGRES_RUNTIME_UID:-}"
POSTGRES_RUNTIME_GID="${BOREALIS_POSTGRES_RUNTIME_GID:-}"
REPO_URL="${BOREALIS_ENGINE_REPO_URL:-${DEFAULT_REPO_URL}}"
REPO_REF="${BOREALIS_ENGINE_REF:-}"
REPO_CHECKOUT_BRANCH="${BOREALIS_ENGINE_CHECKOUT_BRANCH:-}"
REPO_REF_EXPLICIT=0
RELEASE_CHANNEL="${DEFAULT_RELEASE_CHANNEL}"
ENGINE_NETWORK_MODE="${BOREALIS_ENGINE_NETWORK_MODE:-}"
ENGINE_DEPLOYMENT_PROFILE="${BOREALIS_ENGINE_DEPLOYMENT_PROFILE:-}"
SYNC_REQUESTED=0
DISTRO_ID="unknown"
LAUNCH_ARGS=()
CLUSTER_NON_HA_ACKNOWLEDGED=0
CLUSTER_CONTROL_PLANE_VIP=""
CLUSTER_EDGE_VIP=""
CLUSTER_TARGET_REVISION=""
CLUSTER_SCHEMA_PHASE=""
if [[ -n "${REPO_REF}" ]]; then
  REPO_REF_EXPLICIT=1
fi
SERVICE_ROLES=()
SERVICE_ACTION_ROLES=(
  "traefik-edge"
  "remote-desktop-guacd"
  "wireguard-tunnel"
  "postgres-db"
  "api-backend"
  "job-scheduler"
  "webui-frontend"
)
BUILD_ROLES=(
  "borealis-operator"
  "api-backend"
  "job-scheduler"
  "traefik-edge"
  "postgres-db"
  "remote-desktop-guacd"
  "wireguard-tunnel"
  "site-worker"
  "webui-frontend"
)
BUILD_SECTION_FRONTEND=(
  "webui-frontend"
)
BUILD_SECTION_K3S_CLUSTER=(
  "borealis-operator"
)
BUILD_SECTION_BACKEND=(
  "api-backend"
  "job-scheduler"
  "site-worker"
  "remote-desktop-guacd"
)
BUILD_SECTION_NETWORKING=(
  "traefik-edge"
  "wireguard-tunnel"
)
BUILD_SECTION_DATABASE=(
  "postgres-db"
)

if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
  C_RESET=$'\033[0m'
  C_BOLD=$'\033[1m'
  C_DIM=$'\033[2m'
  C_GREEN=$'\033[32m'
  C_YELLOW=$'\033[33m'
  C_BLUE=$'\033[34m'
  C_RED=$'\033[31m'
else
  C_RESET=""
  C_BOLD=""
  C_DIM=""
  C_GREEN=""
  C_YELLOW=""
  C_BLUE=""
  C_RED=""
fi

declare -A IMAGE_TAGS
declare -A IMAGE_HASHES
declare -A DOCKERFILES
declare -A BUILD_CONTEXTS
declare -A BUILD_STATUSES
declare -A DASHBOARD_STATUS
declare -A DASHBOARD_COLOR
declare -A DASHBOARD_UPDATED
declare -A DASHBOARD_ROW_SECTION
CURRENT_BUILD_SELECTION=()
K3S_POSTGRES_CUTOVER_SITE_WORKER_COUNT=0
DASHBOARD_ACTIVE=0
DASHBOARD_CURSOR_HIDDEN=0
DASHBOARD_MODE_LABEL=""
DASHBOARD_NETWORK_LABEL=""
DASHBOARD_PROFILE=""
DASHBOARD_WEBUI_URL=""
DASHBOARD_DOMAIN_WIDTH=16
DASHBOARD_ITEM_WIDTH=30
DASHBOARD_STATUS_WIDTH=40
DASHBOARD_UPDATED_WIDTH=30
DASHBOARD_DYNAMIC_ROWS=()
DASHBOARD_CURRENT_SUBJECT=""
DASHBOARD_CURRENT_STATUS=""
DASHBOARD_TITLE="Borealis Engine Deployment"
GO_API_BACKEND_BINARY_PREPARED=0
AGENT_REDEPLOY_ACTIVE=0
AGENT_REDEPLOY_COMMIT_STARTED=0
AGENT_REDEPLOY_SCHEDULER_PAUSED=0
AGENT_REDEPLOY_TARGET_IMAGE=""
AGENT_REDEPLOY_PREVIOUS_IMAGE=""
AGENT_REDEPLOY_MODE="prod"
AGENT_REDEPLOY_ARTIFACT_ID=""
AGENT_REDEPLOY_BUILD_ID=""
AGENT_REDEPLOY_COMPILED_AT=""
AGENT_REDEPLOY_ARTIFACT_PATH=""
BOREALIS_SITE_WORKER_IMAGE_ALLOWLIST_EXTRA=""
BOREALIS_JOB_SCHEDULER_REPLICAS_OVERRIDE=""
declare -A AGENT_REDEPLOY_CANDIDATE_BY_SERVICE
declare -A AGENT_REDEPLOY_OLD_POD_BY_SERVICE
declare -A AGENT_REDEPLOY_OLD_REVISION_BY_SERVICE
declare -A AGENT_REDEPLOY_ORIGINAL_REVISION_BY_SERVICE
declare -A AGENT_REDEPLOY_CUTOVER_BY_SERVICE
declare -A AGENT_REDEPLOY_OLD_LABEL_ADDED_BY_SERVICE
declare -A AGENT_REDEPLOY_SCHEDULER_REPLICAS
AGENT_REDEPLOY_SERVICES=()
AGENT_REDEPLOY_SCHEDULERS=()

log() {
  printf '[%s] %s\n' "$(date +%FT%T)" "$*"
}

dashboard_static_row() {
  case "$1" in
    "Agent Installer Cache"|"site-worker"|"Ensuring Cluster Exists"|"k3s-longhorn-storage"|"k3s-postgres-db"|"borealis-operator"|"k3s-api-backend"|"k3s-job-scheduler"|"k3s-wireguard-tunnel"|"k3s-traefik-edge"|"k3s-webui-frontend"|"k3s-remote-desktop-guacd"|"Docker Compose"|"Docker Cleanup"|"WebUI Accessible")
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

dashboard_row_section() {
  case "$1" in
    "webui-frontend")
      printf '%s\n' "Frontend"
      ;;
    "api-backend"|"api-backend > job-scheduler"|"Agent Installer Cache"|"site-worker"|"remote-desktop-guacd")
      printf '%s\n' "Backend"
      ;;
    "traefik-edge"|"wireguard-tunnel"|"Local CA"|"Local TLS leaf")
      printf '%s\n' "Networking"
      ;;
    "postgres-db"|"Profile")
      printf '%s\n' "Database"
      ;;
    "Docker Compose")
      printf '%s\n' "Reconciliation"
      ;;
    "Ensuring Cluster Exists"|"k3s-longhorn-storage"|"k3s-postgres-db"|"borealis-operator"|"k3s-api-backend"|"k3s-job-scheduler"|"k3s-wireguard-tunnel"|"k3s-traefik-edge"|"k3s-webui-frontend"|"k3s-remote-desktop-guacd")
      printf '%s\n' "k3s Cluster"
      ;;
    "Docker Cleanup")
      printf '%s\n' "Housekeeping"
      ;;
    "WebUI Accessible")
      printf '%s\n' "Complete"
      ;;
    *)
      printf '%s\n' "Events"
      ;;
  esac
}

dashboard_dynamic_row_present() {
  local row="$1"
  local candidate=""
  for candidate in "${DASHBOARD_DYNAMIC_ROWS[@]}"; do
    [[ "${candidate}" == "${row}" ]] && return 0
  done
  return 1
}

dashboard_ensure_row() {
  local row="$1"
  DASHBOARD_ROW_SECTION["${row}"]="$(dashboard_row_section "${row}")"
  if dashboard_static_row "${row}" || dashboard_dynamic_row_present "${row}"; then
    return 0
  fi
  DASHBOARD_DYNAMIC_ROWS+=("${row}")
}

dashboard_seed_rows() {
  local row=""
  for row in \
    "Agent Installer Cache" \
    "Ensuring Cluster Exists" \
    "k3s-longhorn-storage" \
    "k3s-postgres-db" \
    "borealis-operator" \
    "k3s-api-backend" \
    "k3s-job-scheduler" \
    "k3s-wireguard-tunnel" \
    "k3s-traefik-edge" \
    "k3s-webui-frontend" \
    "k3s-remote-desktop-guacd" \
    "site-worker" \
    "Docker Compose" \
    "Docker Cleanup" \
    "WebUI Accessible"; do
    dashboard_ensure_row "${row}"
    DASHBOARD_STATUS["${row}"]="${DASHBOARD_STATUS[${row}]:-Pending...}"
    DASHBOARD_COLOR["${row}"]="${DASHBOARD_COLOR[${row}]:-${C_DIM}}"
    DASHBOARD_UPDATED["${row}"]="${DASHBOARD_UPDATED[${row}]:--}"
  done
}

dashboard_start() {
  local mode="$1"
  local network_mode="$2"
  DASHBOARD_ACTIVE=1
  DASHBOARD_MODE_LABEL="$(deploy_mode_display_label "${mode}")"
  DASHBOARD_NETWORK_LABEL="$(engine_network_mode_display_label "${network_mode}")"
  dashboard_seed_rows
  if [[ "${DASHBOARD_CURSOR_HIDDEN}" -eq 0 ]]; then
    printf '\033[?25l'
    DASHBOARD_CURSOR_HIDDEN=1
  fi
  printf '\033[2J'
  dashboard_render
}

dashboard_finish() {
  if [[ "${DASHBOARD_ACTIVE}" -eq 1 ]]; then
    dashboard_render
  fi
  if [[ "${DASHBOARD_CURSOR_HIDDEN}" -eq 1 ]]; then
    printf '\033[?25h\n'
    DASHBOARD_CURSOR_HIDDEN=0
  fi
  DASHBOARD_ACTIVE=0
}

dashboard_status_text() {
  local row="$1"
  printf '%s\n' "${DASHBOARD_STATUS[${row}]:-Pending...}"
}

dashboard_status_color() {
  local row="$1"
  printf '%s\n' "${DASHBOARD_COLOR[${row}]:-${C_DIM}}"
}

dashboard_updated_text() {
  local row="$1"
  printf '%s\n' "${DASHBOARD_UPDATED[${row}]:--}"
}

dashboard_day_suffix() {
  local day="$1"
  case $((day % 100)) in
    11|12|13)
      printf '%s\n' "th"
      return 0
      ;;
  esac
  case $((day % 10)) in
    1) printf '%s\n' "st" ;;
    2) printf '%s\n' "nd" ;;
    3) printf '%s\n' "rd" ;;
    *) printf '%s\n' "th" ;;
  esac
}

dashboard_human_timestamp() {
  local day
  local month
  local suffix
  local timestamp
  local time
  local year
  timestamp="$(LC_TIME=C date '+%B|%-d|%Y|%-I:%M%p')"
  IFS='|' read -r month day year time <<< "${timestamp}"
  suffix="$(dashboard_day_suffix "${day}")"
  printf '%s %s%s %s @ %s\n' "${month}" "${day}" "${suffix}" "${year}" "${time}"
}

dashboard_row_label() {
  case "$1" in
    "webui-frontend")
      printf '%s\n' "WebUI Frontend"
      ;;
    "api-backend")
      printf '%s\n' "API Backend"
      ;;
    "api-backend > job-scheduler")
      printf '%s\n' "Job Scheduler"
      ;;
    "Agent Installer Cache")
      printf '%s\n' "Agent Installer Cache"
      ;;
    "site-worker")
      printf '%s\n' "Site Worker"
      ;;
    "remote-desktop-guacd")
      printf '%s\n' "Guacamole Remote Desktop"
      ;;
    "traefik-edge")
      printf '%s\n' "Traefik Reverse Proxy"
      ;;
    "wireguard-tunnel")
      printf '%s\n' "WireGuard Server"
      ;;
    "postgres-db")
      printf '%s\n' "PostgreSQL DB"
      ;;
    "Ensuring Cluster Exists")
      printf '%s\n' "Ensuring k3s Cluster Exists"
      ;;
    "borealis-operator")
      printf '%s\n' "Borealis Operator"
      ;;
    "k3s-longhorn-storage")
      printf '%s\n' "Longhorn Cluster Storage"
      ;;
    "k3s-postgres-db")
      printf '%s\n' "PostgreSQL Database"
      ;;
    "k3s-api-backend")
      printf '%s\n' "API Backend"
      ;;
    "k3s-job-scheduler")
      printf '%s\n' "Job Scheduler"
      ;;
    "k3s-wireguard-tunnel")
      printf '%s\n' "WireGuard Server"
      ;;
    "k3s-traefik-edge")
      printf '%s\n' "Traefik Reverse Cluster Proxy"
      ;;
    "k3s-webui-frontend")
      printf '%s\n' "WebUI Frontend"
      ;;
    "k3s-remote-desktop-guacd")
      printf '%s\n' "Apache Guacamole"
      ;;
    "Docker Compose")
      printf '%s\n' "Docker Compose"
      ;;
    "Docker Cleanup")
      printf '%s\n' "Docker Build Cache Cleanup"
      ;;
    "WebUI Accessible")
      printf '%s\n' "WebUI Accessible"
      ;;
    *)
      printf '%s\n' "$1"
      ;;
  esac
}

dashboard_terminal_columns() {
  local cols="${COLUMNS:-}"
  if [[ -z "${cols}" ]] && command_exists tput; then
    cols="$(tput cols 2>/dev/null || true)"
  fi
  if [[ ! "${cols}" =~ ^[0-9]+$ ]]; then
    cols=120
  fi
  if ((cols < 80)); then
    cols=80
  fi
  printf '%s\n' "${cols}"
}

dashboard_compute_table_widths() {
  local cols
  cols="$(dashboard_terminal_columns)"
  DASHBOARD_DOMAIN_WIDTH=16
  DASHBOARD_UPDATED_WIDTH=30
  DASHBOARD_ITEM_WIDTH=30
  DASHBOARD_STATUS_WIDTH=$((cols - DASHBOARD_DOMAIN_WIDTH - DASHBOARD_ITEM_WIDTH - DASHBOARD_UPDATED_WIDTH - 10))
  if ((DASHBOARD_STATUS_WIDTH < 28)); then
    DASHBOARD_STATUS_WIDTH=28
    DASHBOARD_ITEM_WIDTH=$((cols - DASHBOARD_DOMAIN_WIDTH - DASHBOARD_STATUS_WIDTH - DASHBOARD_UPDATED_WIDTH - 10))
  fi
  if ((DASHBOARD_ITEM_WIDTH < 24)); then
    DASHBOARD_ITEM_WIDTH=24
    DASHBOARD_STATUS_WIDTH=$((cols - DASHBOARD_DOMAIN_WIDTH - DASHBOARD_ITEM_WIDTH - DASHBOARD_UPDATED_WIDTH - 10))
  fi
  if ((DASHBOARD_STATUS_WIDTH < 20)); then
    DASHBOARD_STATUS_WIDTH=20
  fi
}

dashboard_fit_text() {
  local value="$1"
  local width="$2"
  if ((width <= 0)); then
    printf '%s\n' ""
    return 0
  fi
  if ((${#value} <= width)); then
    printf '%s\n' "${value}"
    return 0
  fi
  if ((width > 3)); then
    printf '%s...\n' "${value:0:$((width - 3))}"
  else
    printf '%s\n' "${value:0:${width}}"
  fi
}

dashboard_repeat_char() {
  local char="$1"
  local count="$2"
  printf '%*s' "${count}" "" | tr ' ' "${char}"
}

dashboard_render_table_header() {
  dashboard_compute_table_widths
  printf '\n'
  printf '  %-*s  %-*s  %-*s  %-*s\n' \
    "${DASHBOARD_DOMAIN_WIDTH}" "Domain" \
    "${DASHBOARD_ITEM_WIDTH}" "Item" \
    "${DASHBOARD_STATUS_WIDTH}" "Status" \
    "${DASHBOARD_UPDATED_WIDTH}" "Last Status Update"
  printf '  %-*s  %-*s  %-*s  %-*s\n' \
    "${DASHBOARD_DOMAIN_WIDTH}" "$(dashboard_repeat_char '-' "${DASHBOARD_DOMAIN_WIDTH}")" \
    "${DASHBOARD_ITEM_WIDTH}" "$(dashboard_repeat_char '-' "${DASHBOARD_ITEM_WIDTH}")" \
    "${DASHBOARD_STATUS_WIDTH}" "$(dashboard_repeat_char '-' "${DASHBOARD_STATUS_WIDTH}")" \
    "${DASHBOARD_UPDATED_WIDTH}" "$(dashboard_repeat_char '-' "${DASHBOARD_UPDATED_WIDTH}")"
}

dashboard_render_row() {
  local row="$1"
  local color
  local domain
  local item
  local status
  local updated
  color="$(dashboard_status_color "${row}")"
  domain="$(dashboard_fit_text "${DASHBOARD_ROW_SECTION[${row}]:-Events}" "${DASHBOARD_DOMAIN_WIDTH}")"
  item="$(dashboard_fit_text "$(dashboard_row_label "${row}")" "${DASHBOARD_ITEM_WIDTH}")"
  status="$(dashboard_fit_text "$(dashboard_status_text "${row}")" "${DASHBOARD_STATUS_WIDTH}")"
  updated="$(dashboard_fit_text "$(dashboard_updated_text "${row}")" "${DASHBOARD_UPDATED_WIDTH}")"
  printf '  %-*s  %-*s  %b%-*s%b  %-*s\n' \
    "${DASHBOARD_DOMAIN_WIDTH}" "${domain}" \
    "${DASHBOARD_ITEM_WIDTH}" "${item}" \
    "${color}${C_BOLD}" "${DASHBOARD_STATUS_WIDTH}" "${status}" "${C_RESET}" \
    "${DASHBOARD_UPDATED_WIDTH}" "${updated}"
}

dashboard_render_table() {
  local row=""
  dashboard_render_table_header
  while IFS= read -r row; do
    dashboard_row_visible "${row}" || continue
    dashboard_render_row "${row}"
  done < <(dashboard_ordered_rows)
}

dashboard_ordered_rows() {
  local row=""
  for row in \
    "Ensuring Cluster Exists" \
    "k3s-longhorn-storage" \
    "k3s-postgres-db" \
    "borealis-operator" \
    "k3s-api-backend" \
    "k3s-job-scheduler" \
    "k3s-wireguard-tunnel" \
    "k3s-traefik-edge" \
    "k3s-webui-frontend" \
    "k3s-remote-desktop-guacd" \
    "site-worker" \
    "Docker Compose" \
    "Docker Cleanup" \
    "WebUI Accessible"; do
    printf '%s\n' "${row}"
  done
  for row in "${DASHBOARD_DYNAMIC_ROWS[@]}"; do
    printf '%s\n' "${row}"
  done
}

dashboard_row_visible() {
  case "$1" in
    "Docker Compose"|"WebUI Accessible")
      return 1
      ;;
    *)
      return 0
      ;;
  esac
}

dashboard_ordered_gum_rows() {
  local row=""
  while IFS= read -r row; do
    dashboard_row_visible "${row}" || continue
    printf '%s\n' "${row}"
  done < <(dashboard_ordered_rows)
}

dashboard_state_for_status() {
  local status="$1"
  case "${status}" in
    Pending...|"")
      printf '%s\n' "Pending"
      ;;
    *Failed*|*Not\ Ready*|*Missing*|*Unexpected*|*Too\ Open*|*Mismatch*)
      printf '%s\n' "Failed"
      ;;
    Retired)
      printf '%s\n' "Retired"
      ;;
    Up-to-Date|Skipped*)
      printf '%s\n' "Unchanged"
      ;;
    Complete|Completed*)
      printf '%s\n' "Complete"
      ;;
    Ready*|Running*|Installed|Service\ Running|Node\ Ready|Cutover\ Data\ Imported)
      printf '%s\n' "Ready"
      ;;
    *)
      printf '%s\n' "Running"
      ;;
  esac
}

dashboard_state_for_row() {
  local row="$1"
  local status="$2"
  local state=""
  if [[ "${row}" == "site-worker" ]]; then
    case "${status}" in
      Up-to-Date|Unchanged|Ready*)
        printf '%s\n' "Ready"
        return 0
        ;;
    esac
  fi
  state="$(dashboard_state_for_status "${status}")"
  if dashboard_status_is_completed_subtask_with_pending "${row}" "${status}"; then
    printf '%s\n' "Running"
    return 0
  fi
  printf '%s\n' "${state}"
}

dashboard_action_for_row() {
  local row="$1"
  local status="$2"
  if [[ "${status}" == *"Image"* || "${status}" == *"Building"* ]]; then
    printf '%s\n' "build"
    return 0
  fi
  if [[ "${status}" == *"Rollout"* || "${status}" == *"Restarting"* || "${status}" == *"Reloading"* ]]; then
    printf '%s\n' "rollout"
    return 0
  fi
  if [[ "${status}" == *"Ensuring Table"* || "${status}" == *"Schema"* || "${row}" == "k3s-postgres-db" ]]; then
    printf '%s\n' "schema"
    return 0
  fi
  case "${row}" in
    "Ensuring Cluster Exists")
      printf '%s\n' "bootstrap"
      ;;
    "k3s-longhorn-storage")
      printf '%s\n' "storage"
      ;;
    "borealis-operator")
      printf '%s\n' "operator"
      ;;
    "site-worker")
      printf '%s\n' "workers"
      ;;
    "Docker Compose")
      printf '%s\n' "retire"
      ;;
    "Docker Cleanup")
      printf '%s\n' "cleanup"
      ;;
    "WebUI Accessible")
      printf '%s\n' "smoke"
      ;;
    *)
      printf '%s\n' "reconcile"
      ;;
  esac
}

dashboard_kubernetes_for_row() {
  case "$1" in
    "Ensuring Cluster Exists")
      printf '%s\n' "node/k3s"
      ;;
    "k3s-longhorn-storage")
      printf '%s\n' "longhorn-system"
      ;;
    "k3s-postgres-db")
      printf '%s\n' "statefulset/postgres-db"
      ;;
    "borealis-operator")
      printf '%s\n' "deployment/borealis-operator"
      ;;
    "k3s-api-backend")
      printf '%s\n' "deployment/api-backend"
      ;;
    "k3s-job-scheduler")
      printf '%s\n' "deployment/job-scheduler"
      ;;
    "k3s-wireguard-tunnel")
      printf '%s\n' "deployment/wireguard-tunnel hostNet"
      ;;
    "k3s-traefik-edge")
      printf '%s\n' "deployment/traefik-edge hostNet"
      ;;
    "k3s-webui-frontend")
      printf '%s\n' "deployment/webui-frontend"
      ;;
    "k3s-remote-desktop-guacd")
      printf '%s\n' "deployment/remote-desktop-guacd"
      ;;
    "site-worker")
      printf '%s\n' "pods/site-worker-*"
      ;;
    "Docker Compose")
      printf '%s\n' "services 0"
      ;;
    "Docker Cleanup")
      printf '%s\n' "docker/buildx"
      ;;
    "WebUI Accessible")
      printf '%s\n' "https"
      ;;
    *)
      printf '%s\n' "-"
      ;;
  esac
}

dashboard_subtask_specs_for_row() {
  case "$1" in
    "Ensuring Cluster Exists")
      printf '%s\n' \
        "Render k3s config|config.yaml.d|Config*" \
        "Reconcile API firewall|systemd/firewall|Reconciling API Firewall;Firewall Failed" \
        "Install k3s when missing|systemd/k3s|Installing K3s;Installed;Already Installed;Install Failed" \
        "Start k3s service|systemd/k3s|Starting K3s Service;Service Running;Service Failed" \
        "Wait for node readiness|node/k3s|Waiting For Node Readiness;Node Not Ready;Node Ready" \
        "Verify kubeconfig|k3s.yaml|Kubeconfig*" \
        "Verify bundled ingress disabled|traefik/servicelb|Verifying Ingress Disabled;Bundled Ingress Active" \
        "Reconcile Borealis namespace|namespace/borealis|Reconciling Namespace;Ready"
      ;;
    "k3s-longhorn-storage")
      printf '%s\n' \
        "Install storage dependencies|open-iscsi/nfs-utils|Installing iSCSI Dependency;iSCSI*;Installing NFS Dependency;NFS*" \
        "Apply Longhorn manifests|longhorn-system|Applying Manifests;Apply Failed" \
        "Wait for CSI controllers|longhorn-system|Waiting For csi-*;Waiting For longhorn-*;Rollout Failed" \
        "Reconcile StorageClass policy|storageclass|Reconciling StorageClass Policy;StorageClass*" \
        "Verify Longhorn ready|storageclass/borealis-longhorn|Ready - StorageClass"
      ;;
    "k3s-postgres-db")
      printf '%s\n' \
        "Prepare image|image/postgres-db|*Image*;*Building*" \
        "Apply StatefulSet and services|statefulset/postgres-db|Applying Manifests;Apply Failed" \
        "Wait for PVC|pvc/postgres-db|Waiting For PVC;PVC Not Bound" \
        "Wait for rollout|statefulset/postgres-db|Waiting For Rollout;Rollout Failed" \
        "Run schema initializer|job/postgres-db-schema-initializer|Preparing K3s Engine Tables;Ensuring K3s Engine Tables;K3s Job*" \
        "Ensure database tables|engine schemas|Ensuring Table *;Ready - K3s DB;Ready - Traffic Owner"
      ;;
    "borealis-operator")
      printf '%s\n' \
        "Prepare image|image/borealis-operator|*Image*;*Building*" \
        "Clean legacy RBAC|rbac|Legacy RBAC Cleanup*" \
        "Apply manifests|deployment/borealis-operator|Applying Manifests;Apply Failed" \
        "Wait for rollout|deployment/borealis-operator|Waiting For Rollout;Rollout Failed" \
        "Verify operator API|service/borealis-operator|Ready"
      ;;
    "k3s-api-backend")
      printf '%s\n' \
        "Prepare Go image|image/api-backend|Building Go binary;*Image*" \
        "Validate database path|shadow db|Preparing Shadow DB Validator;Validating Shadow DB;Shadow DB*" \
        "Apply manifests|deployment/api-backend|Applying Manifests;Apply Failed" \
        "Wait for rollout|deployment/api-backend|Waiting For Rollout;Rollout Failed" \
        "Own API traffic|127.0.0.1:5001|Ready - Traffic Owner"
      ;;
    "k3s-job-scheduler")
      printf '%s\n' \
        "Prepare image|image/job-scheduler|*Image*;*Building*" \
        "Apply manifests|deployment/job-scheduler|Applying Manifests;Apply Failed" \
        "Wait for rollout|deployment/job-scheduler|Waiting For Rollout;Rollout Failed" \
        "Own scheduler loop|deployment/job-scheduler|Ready - Traffic Owner"
      ;;
    "k3s-wireguard-tunnel")
      printf '%s\n' \
        "Prepare image|image/wireguard-tunnel|*Image*;*Building*" \
        "Apply host-network pod|deployment/wireguard-tunnel|Applying Manifests;Apply Failed" \
        "Wait for rollout|deployment/wireguard-tunnel|Waiting For Rollout;Rollout Failed" \
        "Verify control socket|wireguard-control.sock|Verifying Control Socket;Control Socket Failed" \
        "Verify tunnel listener|udp/30000|Ready"
      ;;
    "k3s-traefik-edge")
      printf '%s\n' \
        "Prepare image|image/traefik-edge|*Image*;*Building*" \
        "Apply edge manifests|deployment/traefik-edge|Applying Manifests;Apply Failed" \
        "Wait for rollout|deployment/traefik-edge|Waiting For Rollout;Rollout Failed" \
        "Verify ping endpoint|traefik ping|Verifying Ping;Healthcheck Failed" \
        "Own HTTP/HTTPS traffic|ports 80/443|Ready - Traffic Owner"
      ;;
    "k3s-webui-frontend")
      printf '%s\n' \
        "Prepare WebUI image|image/webui-frontend|*Image*;*Building*" \
        "Apply frontend manifests|deployment/webui-frontend|Applying Manifests;Apply Failed" \
        "Wait for rollout|deployment/webui-frontend|Waiting For Rollout;Rollout Failed" \
        "Own WebUI traffic|service/webui-frontend|Ready - Traffic Owner;Ready - Bridge"
      ;;
    "k3s-remote-desktop-guacd")
      printf '%s\n' \
        "Prepare guacd image|image/remote-desktop-guacd|*Image*;*Building*" \
        "Apply guacd manifests|deployment/remote-desktop-guacd|Applying Manifests;Apply Failed" \
        "Wait for rollout|deployment/remote-desktop-guacd|Waiting For Rollout;Rollout Failed" \
        "Verify guacd ready|service/remote-desktop-guacd|Ready"
      ;;
    "site-worker")
      printf '%s\n' \
        "Verify worker image|image/site-worker|*Image*;*Building*" \
        "Reconcile worker pods|pods/site-worker-*|Waiting For K3s Workers;Ready - K3s DB;Up-to-Date;Ready" \
        "Recycle for API cutover|pods/site-worker-*|Recycling K3s Workers For API Cutover;Recycled - API Cutover" \
        "Recycle for runtime env|pods/site-worker-*|Recycling K3s Workers For Runtime Env;Recycled - Runtime Env" \
        "Recycle for timezone|pods/site-worker-*|Recycling K3s Workers For Timezone;Recycled - Timezone"
      ;;
    "Docker Cleanup")
      printf '%s\n' \
        "Prune inactive images|docker images|Pruning Inactive Container Images;Image Prune Failed" \
        "Restore required images|docker images|Restoring Required Container Images" \
        "Prune BuildKit cache|docker buildx|Pruning Engine Build Cache*;Builder Cache Prune Failed;Engine Build Cache*" \
        "Finish cleanup|docker/buildx|Complete;Completed With Warnings;Skipped"
      ;;
  esac
}

dashboard_status_matches_patterns() {
  local status="$1"
  local patterns="$2"
  local pattern=""
  local -a pattern_list
  IFS=';' read -ra pattern_list <<< "${patterns}"
  for pattern in "${pattern_list[@]}"; do
    [[ -n "${pattern}" && "${status}" == ${pattern} ]] && return 0
  done
  return 1
}

dashboard_subtask_count() {
  local row="$1"
  local count=0
  local label=""
  local patterns=""
  local target=""
  while IFS='|' read -r label target patterns; do
    count=$((count + 1))
  done < <(dashboard_subtask_specs_for_row "${row}")
  printf '%s\n' "${count}"
}

dashboard_subtask_matching_index() {
  local row="$1"
  local status="$2"
  local index=0
  local label=""
  local target=""
  local patterns=""
  while IFS='|' read -r label target patterns; do
    if dashboard_status_matches_patterns "${status}" "${patterns:-}"; then
      printf '%s\n' "${index}"
      return 0
    fi
    index=$((index + 1))
  done < <(dashboard_subtask_specs_for_row "${row}")
  printf '%s\n' "-1"
}

dashboard_subtask_active_index() {
  local row="$1"
  local status="$2"
  local match_index
  match_index="$(dashboard_subtask_matching_index "${row}" "${status}")"
  if ((match_index >= 0)); then
    printf '%s\n' "${match_index}"
    return 0
  fi
  printf '%s\n' "0"
}

dashboard_status_is_completed_subtask_with_pending() {
  local row="$1"
  local status="$2"
  local match_index=0
  local state=""
  local total=0
  total="$(dashboard_subtask_count "${row}")"
  ((total > 0)) || return 1
  match_index="$(dashboard_subtask_matching_index "${row}" "${status}")"
  ((match_index >= 0 && match_index < total - 1)) || return 1
  case "${status}" in
    Up-to-Date|Unchanged|Complete|Completed*|Skipped*|Retired)
      return 1
      ;;
  esac
  state="$(dashboard_state_for_status "${status}")"
  case "${state}" in
    Ready|Complete|Unchanged)
      return 0
      ;;
  esac
  return 1
}

dashboard_subtask_state() {
  local parent_state="$1"
  local index="$2"
  local active_index="$3"
  case "${parent_state}" in
    Ready|Complete|Unchanged)
      printf '%s\n' "Complete"
      ;;
    Failed)
      if ((index == active_index)); then
        printf '%s\n' "Failed"
      elif ((index < active_index)); then
        printf '%s\n' "Complete"
      else
        printf '%s\n' "Pending"
      fi
      ;;
    Running)
      if ((index < active_index)); then
        printf '%s\n' "Complete"
      elif ((index == active_index)); then
        printf '%s\n' "Running"
      else
        printf '%s\n' "Pending"
      fi
      ;;
    *)
      printf '%s\n' "Pending"
      ;;
  esac
}

dashboard_gum_progress_bar() {
  local completed="$1"
  local total="$2"
  local state="$3"
  local width=12
  local filled=0
  local empty=0
  local fill=""
  local rest=""
  local i=0
  local color="38;5;246"
  if ((total > 0)); then
    filled=$((completed * width / total))
  fi
  if ((completed > 0 && filled == 0)); then
    filled=1
  fi
  if ((filled > width)); then
    filled="${width}"
  fi
  empty=$((width - filled))
  for ((i = 0; i < filled; i++)); do
    fill+="#"
  done
  for ((i = 0; i < empty; i++)); do
    rest+="-"
  done
  case "${state}" in
    Ready|Complete|Unchanged)
      color="1;32"
      ;;
    Running)
      color="1;38;5;228"
      ;;
    Failed)
      color="1;38;5;203"
      ;;
  esac
  printf '['
  if [[ -n "${fill}" ]]; then
    dashboard_gum_text_style "${color}" "${fill}"
  fi
  if [[ -n "${rest}" ]]; then
    dashboard_gum_text_style "38;5;240" "${rest}"
  fi
  printf ']'
}

dashboard_title_case_words() {
  local value="$1"
  local result=""
  local separator=""
  local word=""
  local first=""
  local -a words=()
  IFS=' ' read -ra words <<< "${value}"
  for word in "${words[@]}"; do
    if [[ -n "${word}" ]]; then
      first="${word:0:1}"
      word="${first^^}${word:1}"
    fi
    result+="${separator}${word}"
    separator=" "
  done
  printf '%s' "${result}"
}

dashboard_gum_subtask_cell() {
  dashboard_gum_text_style "38;5;245" "$1"
}

dashboard_completed_task_count_for_row() {
  local row="$1"
  local status="$2"
  local parent_state="$3"
  local active_index=0
  local completed=0
  local index=0
  local label=""
  local patterns=""
  local sub_state=""
  local target=""
  local total=0
  total="$(dashboard_subtask_count "${row}")"
  if ((total == 0)); then
    case "${parent_state}" in
      Ready|Complete|Unchanged|Retired)
        printf '%s\n' "1"
        ;;
      *)
        printf '%s\n' "0"
        ;;
    esac
    return 0
  fi
  active_index="$(dashboard_subtask_active_index "${row}" "${status}")"
  if dashboard_status_is_completed_subtask_with_pending "${row}" "${status}"; then
    printf '%s\n' "$((active_index + 1))"
    return 0
  fi
  case "${parent_state}" in
    Ready|Complete|Unchanged|Retired)
      printf '%s\n' "${total}"
      return 0
      ;;
    Pending)
      printf '%s\n' "0"
      return 0
      ;;
  esac
  while IFS='|' read -r label target patterns; do
    sub_state="$(dashboard_subtask_state "${parent_state}" "${index}" "${active_index}")"
    if [[ "${sub_state}" == "Complete" ]]; then
      completed=$((completed + 1))
    fi
    index=$((index + 1))
  done < <(dashboard_subtask_specs_for_row "${row}")
  printf '%s\n' "${completed}"
}

dashboard_current_task_label() {
  local row="$1"
  local status="$2"
  local parent_state="$3"
  local active_index=0
  local index=0
  local label=""
  local patterns=""
  local target=""
  local total=0
  total="$(dashboard_subtask_count "${row}")"
  if ((total == 0)); then
    dashboard_title_case_words "$(dashboard_action_for_row "${row}" "${status}")"
    return 0
  fi
  if dashboard_status_is_completed_subtask_with_pending "${row}" "${status}"; then
    printf '%s' "Idle"
    return 0
  fi
  case "${parent_state}" in
    Ready|Complete|Unchanged)
      printf '%s' "Complete"
      ;;
    Pending)
      printf '%s' "Pending..."
      ;;
    *)
      active_index="$(dashboard_subtask_active_index "${row}" "${status}")"
      while IFS='|' read -r label target patterns; do
        if ((index == active_index)); then
          dashboard_title_case_words "${label}"
          return 0
        fi
        index=$((index + 1))
      done < <(dashboard_subtask_specs_for_row "${row}")
      printf '%s' "Working"
      ;;
  esac
}

dashboard_gum_task_cell() {
  local row="$1"
  local status="$2"
  local parent_state="$3"
  local completed=0
  local label=""
  local total=0
  total="$(dashboard_subtask_count "${row}")"
  if ((total == 0)); then
    total=1
  fi
  completed="$(dashboard_completed_task_count_for_row "${row}" "${status}" "${parent_state}")"
  label="$(dashboard_current_task_label "${row}" "${status}" "${parent_state}")"
  printf '%s %s %s' \
    "$(dashboard_gum_progress_bar "${completed}" "${total}" "${parent_state}")" \
    "$(dashboard_gum_text_style "38;5;245" "[${completed}/${total}]")" \
    "${label}"
}

dashboard_gum_subtask_status_cell() {
  local row="$1"
  local status="$2"
  local parent_state="$3"
  local detail=""
  if dashboard_status_is_completed_subtask_with_pending "${row}" "${status}"; then
    detail="Idle"
  else
    case "${parent_state}" in
      Ready|Complete|Unchanged)
        detail="Complete"
        ;;
      Pending)
        detail="Pending..."
        ;;
      *)
        detail="$(dashboard_title_case_words "$(dashboard_detail_for_row "${row}" "${status}")")"
        ;;
    esac
  fi
  dashboard_gum_subtask_cell "${detail}"
}

dashboard_detail_for_row() {
  local row="$1"
  local status="$2"
  case "${status}" in
    Pending...|"")
      printf '%s\n' "Pending..."
      ;;
    *"Ready - Traffic Owner"*)
      printf '%s\n' "traffic owner"
      ;;
    *"Ready - Image (Re)Built"*)
      printf '%s\n' "image refreshed"
      ;;
    *"Ready - StorageClass"*)
      printf '%s\n' "StorageClass ready"
      ;;
    *"Ensuring Table "*)
      printf '%s\n' "${status#Ensuring }"
      ;;
    *)
      printf '%s\n' "${status}"
      ;;
  esac
}

dashboard_gum_enabled() {
  [[ "${BOREALIS_DEPLOY_UI:-gum}" != "plain" ]] || return 1
  [[ "${GUM_READY:-0}" -eq 1 && -n "${GUM_BIN:-}" && -x "${GUM_BIN}" ]] || return 1
  [[ -t 1 && -z "${NO_COLOR:-}" ]] || return 1
}

dashboard_cell() {
  local value="$1"
  value="${value//$'\t'/ }"
  value="${value//$'\n'/ }"
  value="${value//$'\r'/ }"
  printf '%s' "${value}"
}

dashboard_gum_text_style() {
  local code="$1"
  local text="$2"
  printf '\033[%sm%s\033[0m' "${code}" "${text}"
}

dashboard_gum_label() {
  dashboard_gum_text_style "1;38;5;39" "$1"
}

dashboard_gum_state_cell() {
  local state="$1"
  local label="$1"
  if [[ "${state}" == "Running" ]]; then
    label="Configuring"
  fi
  case "${state}" in
    Ready)
      dashboard_gum_text_style "1;32" "${label}"
      ;;
    Failed)
      dashboard_gum_text_style "1;38;5;203" "${label}"
      ;;
    Running)
      dashboard_gum_text_style "1;38;5;228" "${label}"
      ;;
    Complete)
      dashboard_gum_text_style "1;32" "${label}"
      ;;
    Unchanged|Retired|Pending)
      dashboard_gum_text_style "38;5;246" "${label}"
      ;;
    *)
      printf '%s' "${label}"
      ;;
  esac
}

dashboard_gum_header() {
  dashboard_gum_text_style "1;38;5;39" "$1"
}

dashboard_gum_resource_cell() {
  dashboard_gum_text_style "1;38;5;39" "$1"
}

dashboard_gum_line() {
  printf '%s\033[K\n' "$1"
}

dashboard_render_gum_summary() {
  local mode_text="${DASHBOARD_MODE_LABEL:-Production} [${DASHBOARD_NETWORK_LABEL:-Public}]"
  local profile_text="${DASHBOARD_PROFILE:-Pending...}"
  dashboard_gum_line "$(printf '%s %s' "$(dashboard_gum_label "Mode:")" "${mode_text}")"
  dashboard_gum_line "$(printf '%s %s' "$(dashboard_gum_label "Profile:")" "${profile_text}")"
  dashboard_gum_line "$(printf '%s %s' "$(dashboard_gum_label "Log:")" "${BUILD_LOG}")"
}

dashboard_render_gum_current() {
  local subject="${DASHBOARD_CURRENT_SUBJECT:-}"
  local status="${DASHBOARD_CURRENT_STATUS:-}"
  if [[ -z "${subject}" ]]; then
    subject="Deployment"
    status="Waiting for first phase"
  fi
  dashboard_gum_line ""
  dashboard_gum_line "$(printf '%s %s' "$(dashboard_gum_label "Current:")" "$(dashboard_row_label "${subject}")")"
  dashboard_gum_line "$(printf '%s %s' "$(dashboard_gum_label "State:")" "${status}")"
}

dashboard_render_gum_table() {
  local cols
  local widths
  local header_columns
  cols="$(dashboard_terminal_columns)"
  if ((cols >= 180)); then
    widths="31,14,54,51"
  elif ((cols >= 150)); then
    widths="29,14,46,35"
  elif ((cols >= 120)); then
    widths="27,13,39,21"
  else
    widths="23,12,34,14"
  fi
  header_columns="$(dashboard_gum_header "Resource"),$(dashboard_gum_header "Status"),$(dashboard_gum_header "Task"),$(dashboard_gum_header "Sub-Task")"
  {
    local row=""
    local state=""
    local status=""
    # Gum print mode still styles the first data row as selected in a TTY.
    # Feed one inert row and remove it after rendering so real rows stay uniform.
    printf '%s\t%s\t%s\t%s\n' " " " " " " " "
    while IFS= read -r row; do
      status="$(dashboard_status_text "${row}")"
      state="$(dashboard_state_for_row "${row}" "${status}")"
      printf '%s\t%s\t%s\t%s\n' \
        "$(dashboard_cell "$(dashboard_gum_resource_cell "$(dashboard_row_label "${row}")")")" \
        "$(dashboard_cell "$(dashboard_gum_state_cell "${state}")")" \
        "$(dashboard_cell "$(dashboard_gum_task_cell "${row}" "${status}" "${state}")")" \
        "$(dashboard_cell "$(dashboard_gum_subtask_status_cell "${row}" "${status}" "${state}")")"
    done < <(dashboard_ordered_gum_rows)
  } | "${GUM_BIN}" table \
    --print \
    --separator $'\t' \
    --columns "${header_columns}" \
    --widths "${widths}" \
    --border rounded \
    --lazy-quotes \
    --border.foreground 240 \
    --header.foreground 39 \
    --selected.foreground 252 | sed '4d' | while IFS= read -r line || [[ -n "${line}" ]]; do
      dashboard_gum_line "${line}"
    done
}

dashboard_render_gum() {
  dashboard_gum_enabled || return 1
  printf '\033[H'
  dashboard_gum_line "$("${GUM_BIN}" style --bold --foreground 39 "${DASHBOARD_TITLE}")"
  dashboard_render_gum_summary
  dashboard_gum_line ""
  dashboard_render_gum_table
  printf '\033[J'
}

dashboard_render_plain() {
  [[ "${DASHBOARD_ACTIVE}" -eq 1 ]] || return 0
  printf '\033[H'
  printf '%b%s%b\n' "${C_BLUE}${C_BOLD}" "${DASHBOARD_TITLE}" "${C_RESET}"
  printf 'Mode: %s [%s]\n' "${DASHBOARD_MODE_LABEL:-Production}" "${DASHBOARD_NETWORK_LABEL:-Public}"
  if [[ -n "${DASHBOARD_PROFILE}" ]]; then
    printf 'Profile: %s\n' "${DASHBOARD_PROFILE}"
  else
    printf 'Profile: Pending...\n'
  fi
  printf 'Log: %s\n' "${BUILD_LOG}"
  dashboard_render_table
  printf '\033[J'
}

dashboard_render() {
  [[ "${DASHBOARD_ACTIVE}" -eq 1 ]] || return 0
  if dashboard_render_gum; then
    return 0
  fi
  dashboard_render_plain
}

dashboard_update_status() {
  local subject="$1"
  local status="$2"
  local color="$3"
  if [[ "${subject}" == "Database schema" ]]; then
    subject="k3s-postgres-db"
  fi
  dashboard_ensure_row "${subject}"
  if [[ "${DASHBOARD_STATUS[${subject}]:-}" == "${status}" && "${DASHBOARD_COLOR[${subject}]:-}" == "${color}" ]]; then
    return 0
  fi
  DASHBOARD_STATUS["${subject}"]="${status}"
  DASHBOARD_COLOR["${subject}"]="${color}"
  DASHBOARD_UPDATED["${subject}"]="$(dashboard_human_timestamp)"
  DASHBOARD_CURRENT_SUBJECT="${subject}"
  DASHBOARD_CURRENT_STATUS="${status}"
  if [[ "${subject}" == "Profile" ]]; then
    DASHBOARD_PROFILE="${status}"
  fi
  dashboard_render
}

log_status() {
  local subject="$1"
  local status="$2"
  local color="$3"
  if [[ "${DASHBOARD_ACTIVE}" -eq 1 ]]; then
    if [[ "${subject}" == "Profile" ]]; then
      DASHBOARD_PROFILE="${status}"
      dashboard_render
      return 0
    fi
    dashboard_update_status "${subject}" "${status}" "${color}"
    return 0
  fi
  printf '[%s] %s: %b[%s]%b\n' "$(date +%FT%T)" "${subject}" "${color}${C_BOLD}" "${status}" "${C_RESET}"
}

log_section() {
  local label="$1"
  if [[ "${DASHBOARD_ACTIVE}" -eq 1 ]]; then
    dashboard_render
    return 0
  fi
  printf '\n%b[%s]%b\n' "${C_BLUE}${C_BOLD}" "${label}" "${C_RESET}"
}

deploy_mode_display_label() {
  case "$(normalize_mode "$1")" in
    dev) printf '%s\n' "Development" ;;
    *) printf '%s\n' "Production" ;;
  esac
}

log_deploy_header() {
  local mode="$1"
  local network_mode="$2"
  dashboard_start "${mode}" "${network_mode}"
  if [[ "${DASHBOARD_ACTIVE}" -eq 1 ]]; then
    return 0
  fi
  printf '[%s] Deploying %s [%s] Borealis Engine:\n' "$(date +%FT%T)" "$(deploy_mode_display_label "${mode}")" "$(engine_network_mode_display_label "${network_mode}")"
}

build_status_subject() {
  local service="$1"
  case "${service}" in
    job-scheduler)
      printf '%s\n' "api-backend > job-scheduler"
      ;;
    *)
      printf '%s\n' "${service}"
      ;;
  esac
}

log_build_status() {
  local service="$1"
  local status="$2"
  local color="$3"
  case "${service}" in
    api-backend)
      if [[ "${DASHBOARD_STATUS[k3s-api-backend]:-}" == "Ready - Traffic Owner" ]]; then
        printf '[%s] %s image status preserved as %s; K3s API backend remains traffic owner\n' "$(date +%FT%T)" "${service}" "${status}" >> "${BUILD_LOG}"
        return 0
      fi
      log_status "k3s-api-backend" "${status}" "${color}"
      return 0
      ;;
    job-scheduler)
      if [[ "${DASHBOARD_STATUS[k3s-job-scheduler]:-}" == "Ready - Traffic Owner" ]]; then
        printf '[%s] %s image status preserved as %s; K3s job scheduler remains traffic owner\n' "$(date +%FT%T)" "${service}" "${status}" >> "${BUILD_LOG}"
        return 0
      fi
      log_status "k3s-job-scheduler" "${status}" "${color}"
      return 0
      ;;
    webui-frontend)
      if [[ "${DASHBOARD_STATUS[k3s-webui-frontend]:-}" == "Ready - Traffic Owner" ]]; then
        printf '[%s] %s image status preserved as %s; K3s WebUI frontend remains traffic owner\n' "$(date +%FT%T)" "${service}" "${status}" >> "${BUILD_LOG}"
        return 0
      fi
      log_status "k3s-webui-frontend" "${status}" "${color}"
      return 0
      ;;
    remote-desktop-guacd)
      if [[ "${DASHBOARD_STATUS[k3s-remote-desktop-guacd]:-}" == "Ready" ]]; then
        printf '[%s] %s image status preserved as %s; K3s guacd remains traffic owner\n' "$(date +%FT%T)" "${service}" "${status}" >> "${BUILD_LOG}"
        return 0
      fi
      log_status "k3s-remote-desktop-guacd" "${status}" "${color}"
      return 0
      ;;
    postgres-db)
      if [[ "${DASHBOARD_STATUS[k3s-postgres-db]:-}" == "Ready - Traffic Owner" ]]; then
        printf '[%s] %s image status preserved as %s; K3s PostgreSQL remains traffic owner\n' "$(date +%FT%T)" "${service}" "${status}" >> "${BUILD_LOG}"
        return 0
      fi
      log_status "k3s-postgres-db" "${status}" "${color}"
      return 0
      ;;
    wireguard-tunnel)
      if [[ "${DASHBOARD_STATUS[k3s-wireguard-tunnel]:-}" == "Ready" ]]; then
        printf '[%s] %s image status preserved as %s; K3s WireGuard tunnel remains traffic owner\n' "$(date +%FT%T)" "${service}" "${status}" >> "${BUILD_LOG}"
        return 0
      fi
      log_status "k3s-wireguard-tunnel" "${status}" "${color}"
      return 0
      ;;
    traefik-edge)
      if [[ "${DASHBOARD_STATUS[k3s-traefik-edge]:-}" == "Ready - Traffic Owner" ]]; then
        printf '[%s] %s image status preserved as %s; K3s Traefik edge remains traffic owner\n' "$(date +%FT%T)" "${service}" "${status}" >> "${BUILD_LOG}"
        return 0
      fi
      log_status "k3s-traefik-edge" "${status}" "${color}"
      return 0
      ;;
  esac
  log_status "$(build_status_subject "${service}")" "${status}" "${color}"
}

selected_build_role_present() {
  local needle="$1"
  shift || true
  local candidate=""
  for candidate in "$@"; do
    [[ "${candidate}" == "${needle}" ]] && return 0
  done
  return 1
}

build_section_images() {
  local mode="$1"
  local label="$2"
  shift 2
  local section_services=("$@")
  local service=""
  local has_selected=0
  for service in "${section_services[@]}"; do
    if selected_build_role_present "${service}" "${CURRENT_BUILD_SELECTION[@]}"; then
      has_selected=1
      break
    fi
  done
  [[ "${has_selected}" -eq 1 ]] || return 0

  log_section "${label}"
  for service in "${section_services[@]}"; do
    selected_build_role_present "${service}" "${CURRENT_BUILD_SELECTION[@]}" || continue
    build_service_image "${service}" "${mode}"
  done
}

log_detail() {
  printf '[%s] %b%s%b\n' "$(date +%FT%T)" "${C_DIM}" "$*" "${C_RESET}"
}

log_webui_url() {
  local public_base_url
  public_base_url="$(read_env_value BOREALIS_PUBLIC_BASE_URL)"
  [[ -n "${public_base_url}" ]] || return 0
  if [[ "${DASHBOARD_ACTIVE}" -eq 1 ]]; then
    DASHBOARD_WEBUI_URL="${public_base_url}"
    dashboard_update_status "WebUI Accessible" "${public_base_url}" "${C_GREEN}"
    return 0
  fi
  printf '[%s] WebUI Accessible @ %b%s%b\n' "$(date +%FT%T)" "${C_BLUE}${C_BOLD}" "${public_base_url}" "${C_RESET}"
}

die() {
  if [[ "${DASHBOARD_ACTIVE}" -eq 1 ]]; then
    dashboard_update_status "Error" "$*" "${C_RED}"
    dashboard_finish
  fi
  printf '[%s] %bERROR:%b %s\n' "$(date +%FT%T)" "${C_RED}${C_BOLD}" "${C_RESET}" "$*" >&2
  exit 1
}

trap dashboard_finish EXIT

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

run_privileged() {
  if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
    "$@"
    return $?
  fi
  if command_exists sudo; then
    sudo "$@"
    return $?
  fi
  return 1
}

validate_numeric_id() {
  local label="$1"
  local value="$2"
  [[ "${value}" =~ ^[0-9]+$ && "${value}" -gt 0 && "${value}" -lt 65535 ]] || die "${label} must be a numeric id from 1 through 65534."
}

getent_field() {
  local database="$1"
  local key="$2"
  local field="$3"
  getent "${database}" "${key}" 2>/dev/null | awk -F: -v field="${field}" '{print $field; exit}' || true
}

ensure_engine_runtime_identity() {
  validate_numeric_id "BOREALIS_ENGINE_RUNTIME_UID" "${ENGINE_RUNTIME_UID}"
  validate_numeric_id "BOREALIS_ENGINE_RUNTIME_GID" "${ENGINE_RUNTIME_GID}"

  local existing_group_gid
  existing_group_gid="$(getent_field group "${ENGINE_RUNTIME_GROUP}" 3)"
  if [[ -n "${existing_group_gid}" && "${existing_group_gid}" != "${ENGINE_RUNTIME_GID}" ]]; then
    die "Group ${ENGINE_RUNTIME_GROUP} exists with gid ${existing_group_gid}; expected ${ENGINE_RUNTIME_GID}."
  fi
  local existing_gid_group
  existing_gid_group="$(getent group 2>/dev/null | awk -F: -v gid="${ENGINE_RUNTIME_GID}" '$3 == gid {print $1; exit}')"
  if [[ -n "${existing_gid_group}" && "${existing_gid_group}" != "${ENGINE_RUNTIME_GROUP}" ]]; then
    die "gid ${ENGINE_RUNTIME_GID} already belongs to ${existing_gid_group}; set a free Borealis runtime id before deploy."
  fi
  if [[ -z "${existing_group_gid}" ]]; then
    local groupadd_args=(--system --gid "${ENGINE_RUNTIME_GID}")
    if groupadd --help 2>&1 | grep -q -- '--key'; then
      groupadd_args=(-K SYS_GID_MAX=65534 "${groupadd_args[@]}")
    fi
    run_privileged groupadd "${groupadd_args[@]}" "${ENGINE_RUNTIME_GROUP}" \
      || die "Failed to create ${ENGINE_RUNTIME_GROUP} runtime group."
  fi

  local existing_user_uid
  existing_user_uid="$(getent_field passwd "${ENGINE_RUNTIME_USER}" 3)"
  if [[ -n "${existing_user_uid}" && "${existing_user_uid}" != "${ENGINE_RUNTIME_UID}" ]]; then
    die "User ${ENGINE_RUNTIME_USER} exists with uid ${existing_user_uid}; expected ${ENGINE_RUNTIME_UID}."
  fi
  local existing_uid_user
  existing_uid_user="$(getent passwd 2>/dev/null | awk -F: -v uid="${ENGINE_RUNTIME_UID}" '$3 == uid {print $1; exit}')"
  if [[ -n "${existing_uid_user}" && "${existing_uid_user}" != "${ENGINE_RUNTIME_USER}" ]]; then
    die "uid ${ENGINE_RUNTIME_UID} already belongs to ${existing_uid_user}; set a free Borealis runtime id before deploy."
  fi
  if [[ -z "${existing_user_uid}" ]]; then
    local nologin_shell="/usr/sbin/nologin"
    if [[ ! -x "${nologin_shell}" ]]; then
      nologin_shell="/sbin/nologin"
    fi
    if [[ ! -x "${nologin_shell}" ]]; then
      nologin_shell="/bin/false"
    fi
    local useradd_args=(
      --system
      --uid "${ENGINE_RUNTIME_UID}"
      --gid "${ENGINE_RUNTIME_GROUP}"
      --home-dir /nonexistent
      --no-create-home
      --shell "${nologin_shell}"
    )
    if useradd --help 2>&1 | grep -q -- '--key'; then
      useradd_args=(-K SYS_UID_MAX=65534 "${useradd_args[@]}")
    fi
    run_privileged useradd \
      "${useradd_args[@]}" \
      "${ENGINE_RUNTIME_USER}" \
      || die "Failed to create ${ENGINE_RUNTIME_USER} runtime user."
  fi
}

detect_distro() {
  DISTRO_ID="unknown"
  if [[ -f /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    DISTRO_ID="${ID:-unknown}"
  fi
}

gum_bootstrap_root() {
  if [[ "${SYNC_REQUESTED:-0}" -eq 1 ]]; then
    printf '%s\n' "${INSTALL_DIR}/Dependencies/Gum"
    return 0
  fi
  if source_available; then
    printf '%s\n' "${SCRIPT_DIR}/Dependencies/Gum"
    return 0
  fi
  printf '%s\n' "${INSTALL_DIR}/Dependencies/Gum"
}

gum_release_arch() {
  local machine
  machine="$(uname -m 2>/dev/null || true)"
  case "${machine}" in
    x86_64|amd64)
      printf '%s\n' "x86_64"
      ;;
    aarch64|arm64)
      printf '%s\n' "arm64"
      ;;
    *)
      die "Unsupported Gum architecture '${machine}'. Borealis deploy UI supports Linux x86_64 and arm64."
      ;;
  esac
}

gum_release_sha256() {
  case "$1" in
    x86_64)
      printf '%s\n' "${GUM_LINUX_X86_64_SHA256}"
      ;;
    arm64)
      printf '%s\n' "${GUM_LINUX_ARM64_SHA256}"
      ;;
    *)
      die "Unsupported Gum release architecture '$1'."
      ;;
  esac
}

ensure_gum_bootstrap_dependencies() {
  command_exists curl && command_exists tar && command_exists gzip && command_exists sha256sum && return 0
  detect_distro
  case "${DISTRO_ID}" in
    ubuntu|debian|linuxmint|pop)
      run_privileged apt-get update -qq >/dev/null
      run_privileged apt-get install -y ca-certificates curl tar gzip coreutils >/dev/null
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        run_privileged dnf install -y ca-certificates curl tar gzip coreutils >/dev/null
      else
        run_privileged yum install -y ca-certificates curl tar gzip coreutils >/dev/null
      fi
      ;;
    arch)
      run_privileged pacman -Sy --noconfirm ca-certificates curl tar gzip coreutils >/dev/null
      ;;
    opensuse*|sles)
      run_privileged zypper --non-interactive install ca-certificates curl tar gzip coreutils >/dev/null
      ;;
    *)
      ;;
  esac
  command_exists curl && command_exists tar && command_exists gzip && command_exists sha256sum \
    || die "Gum bootstrap needs curl, tar, gzip, and sha256sum. Install them and rerun Engine.sh."
}

ensure_gum_bootstrap() {
  local root
  root="$(gum_bootstrap_root)"
  if [[ -n "${GUM_BIN}" && -x "${GUM_BIN}" ]]; then
    GUM_READY=1
    export PATH="$(dirname "${GUM_BIN}"):${PATH}"
    return 0
  fi
  GUM_BIN="${root}/bin/gum"
  if [[ -x "${GUM_BIN}" ]] && "${GUM_BIN}" --version 2>/dev/null | grep -q "${GUM_VERSION_NUMBER}"; then
    GUM_READY=1
    export PATH="${root}/bin:${PATH}"
    return 0
  fi

  ensure_gum_bootstrap_dependencies
  local arch
  local archive
  local checksum
  local extracted_dir
  local tmp_dir
  arch="$(gum_release_arch)"
  archive="gum_${GUM_VERSION_NUMBER}_Linux_${arch}.tar.gz"
  checksum="$(gum_release_sha256 "${arch}")"
  extracted_dir="gum_${GUM_VERSION_NUMBER}_Linux_${arch}"
  tmp_dir="$(mktemp -d)"

  curl -fsSL "${GUM_RELEASE_BASE_URL}/${archive}" -o "${tmp_dir}/${archive}" \
    || die "Failed to download Gum ${GUM_VERSION}."
  printf '%s  %s\n' "${checksum}" "${tmp_dir}/${archive}" | sha256sum -c - >/dev/null \
    || die "Gum ${GUM_VERSION} checksum verification failed."
  tar -xzf "${tmp_dir}/${archive}" -C "${tmp_dir}" \
    || die "Failed to extract Gum ${GUM_VERSION}."
  run_privileged install -m 0755 -d "${root}/bin" \
    || die "Failed to create Gum dependency directory '${root}/bin'."
  run_privileged install -m 0755 "${tmp_dir}/${extracted_dir}/gum" "${GUM_BIN}" \
    || die "Failed to install Gum to '${GUM_BIN}'."
  rm -rf "${tmp_dir}"
  GUM_READY=1
  export PATH="${root}/bin:${PATH}"
}

selinux_enforcing() {
  if command_exists selinuxenabled; then
    selinuxenabled
    return $?
  fi
  if [[ -r /sys/fs/selinux/enforce ]]; then
    [[ "$(cat /sys/fs/selinux/enforce 2>/dev/null || echo 0)" == "1" ]]
    return $?
  fi
  return 1
}

restore_selinux_context_if_needed() {
  local target="$1"
  [[ -e "${target}" ]] || return 0
  selinux_enforcing || return 0
  command_exists restorecon || return 0
  run_privileged restorecon -RF "${target}" >/dev/null 2>&1 || true
}

normalize_release_channel() {
  local raw="${1:-}"
  raw="$(printf '%s' "${raw}" | tr '[:upper:]' '[:lower:]')"
  case "${raw}" in
    ""|unstable) printf '%s\n' "unstable" ;;
    stable) printf '%s\n' "stable" ;;
    *) die "Unsupported release channel '${1}'. Use stable or unstable." ;;
  esac
}

resolve_latest_stable_tag() {
  local repo_url="$1"
  git ls-remote --tags --refs "${repo_url}" \
    | awk '{print $2}' \
    | sed 's#refs/tags/##' \
    | grep -E '^[0-9]+(\.[0-9]+)*$' \
    | sort -V \
    | tail -n 1
}

resolve_repo_ref() {
  RELEASE_CHANNEL="$(normalize_release_channel "${RELEASE_CHANNEL}")"
  if [[ "${REPO_REF_EXPLICIT}" -eq 1 ]]; then
    [[ -n "${REPO_REF}" ]] || die "Repository ref cannot be empty."
    return 0
  fi

  case "${RELEASE_CHANNEL}" in
    stable)
      if [[ -n "${DEFAULT_STABLE_REF}" ]]; then
        REPO_REF="${DEFAULT_STABLE_REF}"
        log "Resolved stable release channel to configured ref '${REPO_REF}'."
        return 0
      fi
      local stable_tag=""
      stable_tag="$(resolve_latest_stable_tag "${REPO_URL}" || true)"
      if [[ -n "${stable_tag}" ]]; then
        REPO_REF="${stable_tag}"
        log "Resolved stable release channel to latest tag '${REPO_REF}'."
        return 0
      fi
      REPO_REF="${DEFAULT_UNSTABLE_REF}"
      log "Stable release channel could not resolve a remote release tag; falling back to '${REPO_REF}'."
      ;;
    unstable)
      REPO_REF="${DEFAULT_UNSTABLE_REF}"
      log "Resolved unstable release channel to ref '${REPO_REF}'."
      ;;
  esac
}

checkout_branch_name() {
  local raw="${REPO_CHECKOUT_BRANCH:-${REPO_REF}}"
  raw="${raw#refs/heads/}"
  if [[ -z "${raw}" || "${raw}" =~ ^[0-9a-fA-F]{40}$ ]]; then
    raw="borealis-deploy"
  fi
  printf '%s\n' "${raw}"
}

ensure_git_dependency() {
  command_exists git && return 0
  detect_distro
  case "${DISTRO_ID}" in
    ubuntu|debian|linuxmint|pop)
      run_privileged apt update -qq
      run_privileged apt install -y git ca-certificates
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        run_privileged dnf install -y git ca-certificates
      else
        run_privileged yum install -y git ca-certificates
      fi
      ;;
    arch)
      run_privileged pacman -Sy --noconfirm git ca-certificates
      ;;
    opensuse*|sles)
      run_privileged zypper --non-interactive install git ca-certificates
      ;;
    *)
      ;;
  esac
  command_exists git || die "Git is required. Install git and rerun Engine.sh."
}

sync_repo() {
  [[ -n "${INSTALL_DIR}" && "${INSTALL_DIR}" != "/" ]] || die "Refusing to install into empty path or '/'."
  [[ -n "${REPO_URL}" ]] || die "Repository URL cannot be empty."
  ensure_git_dependency
  resolve_repo_ref

  log "Syncing Borealis ref '${REPO_REF}' from ${REPO_URL} into ${INSTALL_DIR}."
  run_privileged mkdir -p "${INSTALL_DIR}"

  if [[ ! -d "${INSTALL_DIR}/.git" ]]; then
    run_privileged find "${INSTALL_DIR}" -mindepth 1 -maxdepth 1 \
      ! -name "Engine" \
      ! -name "Engine.old" \
      ! -name "Agent" \
      -exec rm -rf {} +
    run_privileged git -C "${INSTALL_DIR}" init
    run_privileged git -C "${INSTALL_DIR}" remote add origin "${REPO_URL}"
  else
    local origin_url=""
    origin_url="$(run_privileged git -C "${INSTALL_DIR}" remote get-url origin 2>/dev/null || true)"
    if [[ -z "${origin_url}" ]]; then
      run_privileged git -C "${INSTALL_DIR}" remote add origin "${REPO_URL}"
    elif [[ "${origin_url}" != "${REPO_URL}" ]]; then
      run_privileged git -C "${INSTALL_DIR}" remote set-url origin "${REPO_URL}"
    fi
  fi

  local checkout_branch
  checkout_branch="$(checkout_branch_name)"
  run_privileged git -C "${INSTALL_DIR}" fetch --depth 1 --force origin "${REPO_REF}"
  run_privileged git -C "${INSTALL_DIR}" checkout --force -B "${checkout_branch}" FETCH_HEAD
  run_privileged git -C "${INSTALL_DIR}" reset --hard FETCH_HEAD
  run_privileged git -C "${INSTALL_DIR}" clean -fdx -e Engine -e Engine.old -e Agent
  run_privileged chmod +x "${INSTALL_DIR}/Engine.sh" >/dev/null 2>&1 || true
  restore_selinux_context_if_needed "${INSTALL_DIR}"
}

source_available() {
  [[ -f "${COMPOSE_FILE}" && -f "${BUILD_MANIFEST}" && -d "${CONTAINER_SOURCE_DIR}" ]]
}

parse_launch_options() {
  LAUNCH_ARGS=()
  while (($#)); do
    case "$1" in
      --install-dir|--repo-url|--ref|--branch|--repo-branch|--repo_branch|--release-channel|--release_channel|--network-mode|--network_mode|--deployment-profile|--deployment_profile|--control-plane-vip|--edge-vip|--revision|--schema-phase)
        [[ $# -ge 2 ]] || die "Missing value for ${1}."
        case "$1" in
          --install-dir) INSTALL_DIR="$2" ;;
          --repo-url) REPO_URL="$2" ;;
          --release-channel|--release_channel) RELEASE_CHANNEL="$2" ;;
          --network-mode|--network_mode)
            ENGINE_NETWORK_MODE="$2"
            export BOREALIS_ENGINE_NETWORK_MODE="${ENGINE_NETWORK_MODE}"
            ;;
          --deployment-profile|--deployment_profile)
            ENGINE_DEPLOYMENT_PROFILE="$2"
            ENGINE_NETWORK_MODE="$(engine_network_mode_from_deployment_profile "${ENGINE_DEPLOYMENT_PROFILE}")"
            export BOREALIS_ENGINE_NETWORK_MODE="${ENGINE_NETWORK_MODE}"
            export BOREALIS_ENGINE_DEPLOYMENT_PROFILE="${ENGINE_DEPLOYMENT_PROFILE}"
            ;;
          --control-plane-vip) CLUSTER_CONTROL_PLANE_VIP="$2" ;;
          --edge-vip) CLUSTER_EDGE_VIP="$2" ;;
          --revision) CLUSTER_TARGET_REVISION="$2" ;;
          --schema-phase) CLUSTER_SCHEMA_PHASE="$2" ;;
          --ref|--branch|--repo-branch|--repo_branch)
            REPO_REF="$2"
            REPO_REF_EXPLICIT=1
            case "$1" in
              --branch|--repo-branch|--repo_branch) REPO_CHECKOUT_BRANCH="$2" ;;
            esac
            ;;
        esac
        case "$1" in
          --network-mode|--network_mode|--deployment-profile|--deployment_profile|--control-plane-vip|--edge-vip|--revision|--schema-phase) ;;
          *) SYNC_REQUESTED=1 ;;
        esac
        shift 2
        ;;
      --install-dir=*|--repo-url=*|--ref=*|--branch=*|--repo-branch=*|--repo_branch=*|--release-channel=*|--release_channel=*|--network-mode=*|--network_mode=*|--deployment-profile=*|--deployment_profile=*|--control-plane-vip=*|--edge-vip=*|--revision=*|--schema-phase=*)
        local key="${1%%=*}"
        local value="${1#*=}"
        case "${key}" in
          --install-dir) INSTALL_DIR="${value}" ;;
          --repo-url) REPO_URL="${value}" ;;
          --release-channel|--release_channel) RELEASE_CHANNEL="${value}" ;;
          --network-mode|--network_mode)
            ENGINE_NETWORK_MODE="${value}"
            export BOREALIS_ENGINE_NETWORK_MODE="${ENGINE_NETWORK_MODE}"
            ;;
          --deployment-profile|--deployment_profile)
            ENGINE_DEPLOYMENT_PROFILE="${value}"
            ENGINE_NETWORK_MODE="$(engine_network_mode_from_deployment_profile "${ENGINE_DEPLOYMENT_PROFILE}")"
            export BOREALIS_ENGINE_NETWORK_MODE="${ENGINE_NETWORK_MODE}"
            export BOREALIS_ENGINE_DEPLOYMENT_PROFILE="${ENGINE_DEPLOYMENT_PROFILE}"
            ;;
          --control-plane-vip) CLUSTER_CONTROL_PLANE_VIP="${value}" ;;
          --edge-vip) CLUSTER_EDGE_VIP="${value}" ;;
          --revision) CLUSTER_TARGET_REVISION="${value}" ;;
          --schema-phase) CLUSTER_SCHEMA_PHASE="${value}" ;;
          --ref|--branch|--repo-branch|--repo_branch)
            REPO_REF="${value}"
            REPO_REF_EXPLICIT=1
            case "${key}" in
              --branch|--repo-branch|--repo_branch) REPO_CHECKOUT_BRANCH="${value}" ;;
            esac
            ;;
        esac
        case "${key}" in
          --network-mode|--network_mode|--deployment-profile|--deployment_profile|--control-plane-vip|--edge-vip|--revision|--schema-phase) ;;
          *) SYNC_REQUESTED=1 ;;
        esac
        shift
        ;;
      -Engine|--engine|--Engine|-EngineProduction|--EngineProduction|--engine-production)
        LAUNCH_ARGS=(deploy prod)
        shift
        ;;
      -EngineDev|--EngineDev|--engine-dev)
        LAUNCH_ARGS=(deploy dev)
        shift
        ;;
      --acknowledge-cluster-non-ha)
        CLUSTER_NON_HA_ACKNOWLEDGED=1
        shift
        ;;
      --zip-url|--zip-path|--zip-url=*|--zip-path=*)
        die "ZIP-based bootstrap is no longer supported. Use --repo-url and --ref."
        ;;
      *)
        LAUNCH_ARGS+=("$1")
        shift
        ;;
    esac
  done
}

sync_and_reexec_if_needed() {
  if source_available && [[ "${SYNC_REQUESTED}" -eq 0 ]]; then
    return 0
  fi

  sync_repo
  log "Launching ${INSTALL_DIR}/Engine.sh ${LAUNCH_ARGS[*]:-deploy}."
  if [[ ! -t 0 && -r /dev/tty ]]; then
    exec "${INSTALL_DIR}/Engine.sh" "${LAUNCH_ARGS[@]}" < /dev/tty
  fi
  exec "${INSTALL_DIR}/Engine.sh" "${LAUNCH_ARGS[@]}"
}

require_docker() {
  command_exists docker || die "Docker Engine CLI missing. Run Engine.sh deploy after installing Docker Engine."
  local docker_info_output=""
  if docker_info_output="$(docker info 2>&1 >/dev/null)"; then
    return 0
  fi
  if printf '%s\n' "${docker_info_output}" | grep -qi "permission denied"; then
    die "Docker socket permission denied for user '${USER:-$(id -un)}'. Re-run Engine.sh with sudo, or add the user to the docker group and start a new login session."
  fi
  if command_exists systemctl && systemctl is-active --quiet docker 2>/dev/null; then
    die "Docker daemon is active, but Docker CLI cannot connect. Check Docker context and /var/run/docker.sock access. First error: $(printf '%s\n' "${docker_info_output}" | head -n 1)"
  fi
  die "Docker daemon unreachable. Start Docker Engine and retry. First error: $(printf '%s\n' "${docker_info_output}" | head -n 1)"
}

require_python() {
  command_exists python3 || die "python3 missing. Install python3 and rerun Engine.sh."
}

host_postgres_units_active() {
  command_exists systemctl || return 1
  if systemctl is-active --quiet postgresql.service 2>/dev/null; then
    return 0
  fi
  systemctl list-units 'postgresql@*.service' --state=active --no-legend --no-pager 2>/dev/null | grep -q .
}

container_running() {
  local container_name="$1"
  docker inspect -f '{{.State.Running}}' "${container_name}" 2>/dev/null | grep -qx true
}

container_health_status() {
  local container_name="$1"
  docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${container_name}" 2>/dev/null || true
}

refresh_legacy_postgres_container_status() {
  local container_status
  container_status="$(container_health_status "borealis-engine-postgres-db")"
  case "${container_status}" in
    healthy)
      log_status "postgres-db" "Running - Healthy" "${C_GREEN}"
      ;;
    running)
      log_status "postgres-db" "Running" "${C_GREEN}"
      ;;
    starting)
      log_status "postgres-db" "Starting" "${C_YELLOW}"
      ;;
    unhealthy)
      log_status "postgres-db" "Unhealthy" "${C_RED}"
      ;;
    exited|dead|removing|paused|restarting|created)
      log_status "postgres-db" "${container_status}" "${C_RED}"
      ;;
    "")
      log_status "postgres-db" "Missing" "${C_RED}"
      ;;
    *)
      log_status "postgres-db" "${container_status}" "${C_YELLOW}"
      ;;
  esac
}

wait_for_postgres_container() {
  local timeout_seconds="${1:-150}"
  local deadline=$((SECONDS + timeout_seconds))
  local status=""
  while ((SECONDS < deadline)); do
    status="$(container_health_status borealis-engine-postgres-db)"
    if [[ "${status}" == "healthy" ]]; then
      return 0
    fi
    sleep 2
  done
  printf '[%s] postgres-db did not become healthy, final status=%s\n' "$(date +%FT%T)" "${status:-unknown}" >> "${BUILD_LOG}"
  docker logs --tail 120 borealis-engine-postgres-db >> "${BUILD_LOG}" 2>&1 || true
  return 1
}

handle_database_schema_output_line() {
  local line
  local progress_prefix=$'BOREALIS_SCHEMA_PROGRESS\t'
  local table_name
  line="$1"
  if [[ "${line}" == "${progress_prefix}"* ]]; then
    table_name="${line#${progress_prefix}}"
    log_status "Database schema" "Ensuring Table \"${table_name}\" Exists" "${C_YELLOW}"
    printf '[%s] Database schema: [Ensuring Table "%s" Exists]\n' "$(date +%FT%T)" "${table_name}" >> "${BUILD_LOG}"
  else
    printf '%s\n' "${line}" >> "${BUILD_LOG}"
  fi
}

stream_database_schema_output() {
  local line
  while IFS= read -r line; do
    handle_database_schema_output_line "${line}"
  done
}

ensure_no_host_postgres_conflict() {
  if [[ "$(resolve_postgres_traffic_owner)" == "k3s" ]]; then
    return 0
  fi
  if host_postgres_units_active && ! container_running borealis-engine-postgres-db; then
    die "Host PostgreSQL is active on this machine and conflicts with container postgres-db on 127.0.0.1:5432. Stop and disable host PostgreSQL before deploying."
  fi
}

docker_apt_repo_os() {
  case "${DISTRO_ID}" in
    debian) printf '%s\n' "debian" ;;
    ubuntu|linuxmint|pop) printf '%s\n' "ubuntu" ;;
    *) printf '%s\n' "${DISTRO_ID}" ;;
  esac
}

docker_apt_codename() {
  local codename=""
  if [[ -f /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    codename="${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}"
  fi
  [[ -n "${codename}" ]] || die "Unable to determine Debian/Ubuntu codename for Docker apt repository."
  printf '%s\n' "${codename}"
}

install_engine_apt_dependencies() {
  local repo_os
  local codename
  local arch
  repo_os="$(docker_apt_repo_os)"
  codename="$(docker_apt_codename)"
  arch="$(dpkg --print-architecture)"

  run_privileged apt-get update -qq
  run_privileged apt-get install -y python3 ca-certificates curl gnupg
  run_privileged install -m 0755 -d /etc/apt/keyrings
  run_privileged curl -fsSL "https://download.docker.com/linux/${repo_os}/gpg" -o /etc/apt/keyrings/docker.asc
  run_privileged chmod a+r /etc/apt/keyrings/docker.asc

  local source_file="/etc/apt/sources.list.d/docker.sources"
  local temp_file
  temp_file="$(mktemp)"
  cat > "${temp_file}" <<EOF
Types: deb
URIs: https://download.docker.com/linux/${repo_os}
Suites: ${codename}
Components: stable
Architectures: ${arch}
Signed-By: /etc/apt/keyrings/docker.asc
EOF
  run_privileged install -m 0644 "${temp_file}" "${source_file}"
  rm -f "${temp_file}"

  run_privileged apt-get update -qq
  run_privileged apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin
}

ensure_engine_dependencies() {
  local needs_install=0
  command_exists python3 || needs_install=1
  command_exists docker || needs_install=1

  if [[ "${needs_install}" -eq 1 ]]; then
    detect_distro
    case "${DISTRO_ID}" in
      ubuntu|debian|linuxmint|pop)
        install_engine_apt_dependencies
        ;;
      rhel|centos|fedora|rocky|almalinux)
        if command_exists dnf; then
          run_privileged dnf install -y python3 docker
        else
          run_privileged yum install -y python3 docker
        fi
        ;;
      arch)
        run_privileged pacman -Sy --noconfirm python docker
        ;;
      opensuse*|sles)
        run_privileged zypper --non-interactive install python3 docker
        ;;
      *)
        die "Unsupported distro '${DISTRO_ID}'. Install Python 3 and Docker Engine manually."
        ;;
    esac
  fi

  if command_exists systemctl; then
    run_privileged systemctl enable --now docker >/dev/null 2>&1 || true
  fi

  require_python
  require_docker
}

validate_k3s_baseline_settings() {
  [[ "${K3S_API_PORT}" =~ ^[0-9]+$ && "${K3S_API_PORT}" -ge 1 && "${K3S_API_PORT}" -le 65535 ]] \
    || die "BOREALIS_K3S_API_PORT must be a numeric TCP port from 1 through 65535."
  [[ "${K3S_NAMESPACE}" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] \
    || die "BOREALIS_K3S_NAMESPACE must be a valid Kubernetes namespace name."
  [[ "${K3S_KUBECONFIG_MODE}" =~ ^0?[0-7]{3}$ ]] \
    || die "BOREALIS_K3S_KUBECONFIG_MODE must be an octal file mode like 0600."
  [[ "${K3S_CLUSTER_CIDR}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/([0-9]|[12][0-9]|3[0-2])$ ]] \
    || die "BOREALIS_K3S_CLUSTER_CIDR must be an IPv4 CIDR, for example 10.42.0.0/16."
  [[ "${K3S_SERVICE_CIDR}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/([0-9]|[12][0-9]|3[0-2])$ ]] \
    || die "BOREALIS_K3S_SERVICE_CIDR must be an IPv4 CIDR, for example 10.43.0.0/16."
  if [[ -n "${K3S_PEER_CIDRS}" ]]; then
    K3S_PEER_CIDRS="$(python3 - "${K3S_PEER_CIDRS}" <<'PY'
import ipaddress, sys
networks = []
for raw in sys.argv[1].split(","):
    network = ipaddress.ip_network(raw.strip(), strict=False)
    if network.version != 4 or not network.is_private:
        raise SystemExit(1)
    networks.append(str(network))
print(",".join(networks))
PY
    )" || die "BOREALIS_K3S_PEER_CIDRS must be comma-separated private IPv4 CIDRs."
  fi
  if [[ -n "${K3S_CONTAINER_LOG_MAX_SIZE}" ]]; then
    [[ "${K3S_CONTAINER_LOG_MAX_SIZE}" =~ ^[1-9][0-9]*([EPTGMK]i?|m)?$ ]] \
      || die "BOREALIS_K3S_CONTAINER_LOG_MAX_SIZE must be a Kubernetes quantity like 10Mi."
  fi
  if [[ -n "${K3S_CONTAINER_LOG_MAX_FILES}" ]]; then
    [[ "${K3S_CONTAINER_LOG_MAX_FILES}" =~ ^[0-9]+$ && "${K3S_CONTAINER_LOG_MAX_FILES}" -ge 2 ]] \
      || die "BOREALIS_K3S_CONTAINER_LOG_MAX_FILES must be a number greater than or equal to 2."
  fi
}

validate_engine_log_retention_settings() {
  [[ "${ENGINE_FILE_LOG_RETENTION_DAYS}" =~ ^[1-9][0-9]*$ && "${ENGINE_FILE_LOG_RETENTION_DAYS}" -le 3650 ]] \
    || die "BOREALIS_ENGINE_FILE_LOG_RETENTION_DAYS must be a number from 1 through 3650."
}

normalize_enabled_flag() {
  local label="$1"
  local raw="${2:-}"
  raw="$(printf '%s' "${raw}" | tr '[:upper:]' '[:lower:]')"
  case "${raw}" in
    1|true|yes|on|enabled)
      printf '%s\n' "1"
      ;;
    0|false|no|off|disabled)
      printf '%s\n' "0"
      ;;
    *)
      die "${label} must be one of 1, 0, true, false, yes, no, on, off, enabled, or disabled."
      ;;
  esac
}

validate_k3s_longhorn_settings() {
  normalize_enabled_flag "BOREALIS_K3S_LONGHORN_ENABLED" "${K3S_LONGHORN_ENABLED}" >/dev/null
  [[ "${K3S_LONGHORN_NAMESPACE}" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] \
    || die "BOREALIS_K3S_LONGHORN_NAMESPACE must be a valid Kubernetes namespace name."
  [[ "${K3S_LONGHORN_UPSTREAM_STORAGE_CLASS}" =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]] \
    || die "BOREALIS_K3S_LONGHORN_UPSTREAM_STORAGE_CLASS must be a valid Kubernetes StorageClass name."
  [[ "${K3S_BOREALIS_LONGHORN_STORAGE_CLASS}" =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]] \
    || die "BOREALIS_K3S_BOREALIS_LONGHORN_STORAGE_CLASS must be a valid Kubernetes StorageClass name."
  [[ "${K3S_BOREALIS_LONGHORN_REPLICA_COUNT}" =~ ^[1-9][0-9]*$ ]] \
    || die "BOREALIS_K3S_BOREALIS_LONGHORN_REPLICA_COUNT must be a positive integer."
  [[ "${K3S_PVC_STORAGE_CLASS}" =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]] \
    || die "BOREALIS_K3S_PVC_STORAGE_CLASS must be a valid Kubernetes StorageClass name."
  [[ -n "${K3S_LONGHORN_MANIFEST_URL}" ]] \
    || die "BOREALIS_K3S_LONGHORN_MANIFEST_URL must not be empty when Longhorn reconcile is enabled."
  [[ "${K3S_LONGHORN_ROLLOUT_TIMEOUT}" =~ ^[0-9]+(s|m|h)?$ ]] \
    || die "BOREALIS_K3S_LONGHORN_ROLLOUT_TIMEOUT must be a kubectl timeout like 300s or 5m."
}

k3s_longhorn_enabled() {
  [[ "$(normalize_enabled_flag "BOREALIS_K3S_LONGHORN_ENABLED" "${K3S_LONGHORN_ENABLED}")" == "1" ]]
}

validate_k3s_storage_quantity() {
  local label="$1"
  local raw="$2"
  [[ "${raw}" =~ ^[1-9][0-9]*([EPTGMK]i?|m)?$ ]] \
    || die "${label} must be a Kubernetes storage quantity like 20Gi."
}

validate_k3s_postgres_settings() {
  normalize_enabled_flag "BOREALIS_K3S_POSTGRES_ENABLED" "${K3S_POSTGRES_ENABLED}" >/dev/null
  validate_k3s_storage_quantity "BOREALIS_K3S_POSTGRES_STORAGE_SIZE" "${K3S_POSTGRES_STORAGE_SIZE}"
  [[ "${K3S_POSTGRES_ROLLOUT_TIMEOUT}" =~ ^[0-9]+(s|m|h)?$ ]] \
    || die "BOREALIS_K3S_POSTGRES_ROLLOUT_TIMEOUT must be a kubectl timeout like 180s or 3m."
}

k3s_postgres_enabled() {
  [[ "$(normalize_enabled_flag "BOREALIS_K3S_POSTGRES_ENABLED" "${K3S_POSTGRES_ENABLED}")" == "1" ]]
}

k3s_pvc_storage_class_explicitly_set() {
  [[ -n "${BOREALIS_K3S_PVC_STORAGE_CLASS:-}" || -n "${BOREALIS_K3S_STORAGE_CLASS:-}" ]]
}

current_k3s_postgres_pvc_storage_class() {
  k3s_cluster_installed || return 0
  [[ -s "${K3S_KUBECONFIG}" ]] || return 0
  k3s_kubectl -n "${K3S_NAMESPACE}" get pvc postgres-data-postgres-db-0 \
    -o jsonpath='{.spec.storageClassName}' 2>/dev/null || true
}

current_k3s_postgres_statefulset_storage_class() {
  k3s_cluster_installed || return 0
  [[ -s "${K3S_KUBECONFIG}" ]] || return 0
  k3s_kubectl -n "${K3S_NAMESPACE}" get statefulset postgres-db \
    -o jsonpath='{.spec.volumeClaimTemplates[0].spec.storageClassName}' 2>/dev/null || true
}

resolve_k3s_postgres_storage_class() {
  local storage_class=""
  storage_class="$(current_k3s_postgres_pvc_storage_class)"
  if [[ -n "${storage_class}" ]]; then
    printf '%s\n' "${storage_class}"
    return 0
  fi
  storage_class="$(current_k3s_postgres_statefulset_storage_class)"
  if [[ -n "${storage_class}" ]]; then
    printf '%s\n' "${storage_class}"
    return 0
  fi
  if k3s_pvc_storage_class_explicitly_set; then
    printf '%s\n' "${K3S_PVC_STORAGE_CLASS}"
    return 0
  fi
  printf '%s\n' "${K3S_BOREALIS_LONGHORN_STORAGE_CLASS}"
}

ensure_systemctl_for_k3s() {
  command_exists systemctl || die "Borealis-managed K3s baseline currently requires systemd/systemctl."
}

ensure_k3s_install_dependencies() {
  local needs_install=0
  command_exists python3 || needs_install=1
  command_exists curl || needs_install=1
  command_exists iptables || needs_install=1
  [[ "${needs_install}" -eq 1 ]] || return 0

  detect_distro
  case "${DISTRO_ID}" in
    ubuntu|debian|linuxmint|pop)
      run_privileged apt-get update -qq
      run_privileged apt-get install -y ca-certificates curl iptables python3
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        run_privileged dnf install -y ca-certificates curl iptables python3
      else
        run_privileged yum install -y ca-certificates curl iptables python3
      fi
      ;;
    arch)
      run_privileged pacman -Sy --noconfirm ca-certificates curl iptables python
      ;;
    opensuse*|sles)
      run_privileged zypper --non-interactive install ca-certificates curl iptables python3
      ;;
    *)
      die "Unsupported distro '${DISTRO_ID}'. Install Python 3, curl, ca-certificates, and iptables before K3s baseline reconcile."
      ;;
  esac
}

k3s_binary_path() {
  if command_exists k3s; then
    command -v k3s
    return 0
  fi
  if [[ -x /usr/local/bin/k3s ]]; then
    printf '%s\n' "/usr/local/bin/k3s"
    return 0
  fi
  if [[ -x /usr/bin/k3s ]]; then
    printf '%s\n' "/usr/bin/k3s"
    return 0
  fi
  return 1
}

k3s_service_unit_exists() {
  ensure_systemctl_for_k3s
  if systemctl list-unit-files "${K3S_SERVICE_NAME}.service" --no-legend --no-pager 2>/dev/null \
    | awk '{print $1}' \
    | grep -qx "${K3S_SERVICE_NAME}.service"; then
    return 0
  fi
  [[ -f "/etc/systemd/system/${K3S_SERVICE_NAME}.service" \
    || -f "/lib/systemd/system/${K3S_SERVICE_NAME}.service" \
    || -f "/usr/lib/systemd/system/${K3S_SERVICE_NAME}.service" ]]
}

k3s_cluster_installed() {
  k3s_binary_path >/dev/null 2>&1 || return 1
  k3s_service_unit_exists
}

k3s_kubectl() {
  local k3s_bin
  k3s_bin="$(k3s_binary_path)" || die "K3s binary missing after install."
  run_privileged "${k3s_bin}" kubectl --kubeconfig "${K3S_KUBECONFIG}" "$@"
}

stored_k3s_config_hash() {
  local hash_file="$1"
  awk '{print $1; exit}' "${hash_file}" 2>/dev/null || true
}

k3s_resource_annotation_matches() {
  local resource="$1"
  local annotation="$2"
  local expected_hash="$3"
  local namespace="${4:-${K3S_NAMESPACE}}"
  local actual_hash=""
  if [[ "${namespace}" == "-" ]]; then
    actual_hash="$(
      k3s_kubectl get "${resource}" -o json 2>>"${BUILD_LOG}" \
        | python3 -c 'import json, sys; data = json.load(sys.stdin); print(((data.get("metadata") or {}).get("annotations") or {}).get(sys.argv[1]) or "")' "${annotation}" 2>>"${BUILD_LOG}"
    )" || return 1
  else
    actual_hash="$(
      k3s_kubectl -n "${namespace}" get "${resource}" -o json 2>>"${BUILD_LOG}" \
        | python3 -c 'import json, sys; data = json.load(sys.stdin); print(((data.get("metadata") or {}).get("annotations") or {}).get(sys.argv[1]) or "")' "${annotation}" 2>>"${BUILD_LOG}"
    )" || return 1
  fi
  [[ "${actual_hash}" == "${expected_hash}" ]]
}

k3s_manifest_config_current() {
  local hash_file="$1"
  local expected_hash="$2"
  local annotation="$3"
  shift 3
  [[ "$(stored_k3s_config_hash "${hash_file}")" == "${expected_hash}" ]] || return 1
  local resource=""
  for resource in "$@"; do
    k3s_resource_annotation_matches "${resource}" "${annotation}" "${expected_hash}" || return 1
  done
  return 0
}

log_k3s_manifest_unchanged() {
  local row="$1"
  local hash="$2"
  log_status "${row}" "Unchanged" "${C_GREEN}"
  printf '[%s] %s K3s manifest unchanged as %s; apply and rollout skipped\n' "$(date +%FT%T)" "${row}" "${hash}" >> "${BUILD_LOG}"
}

render_k3s_borealis_config() {
  cat <<EOF
# Borealis-managed K3s baseline. Engine.sh owns this file.
write-kubeconfig-mode: "${K3S_KUBECONFIG_MODE}"
cluster-cidr: "${K3S_CLUSTER_CIDR}"
service-cidr: "${K3S_SERVICE_CIDR}"
disable:
  - "traefik"
  - "servicelb"
secrets-encryption: true
embedded-registry: true
node-label:
  - "borealis.io/engine-node=true"
EOF
  if [[ -n "${K3S_CONTAINER_LOG_MAX_SIZE}" || -n "${K3S_CONTAINER_LOG_MAX_FILES}" ]]; then
    printf 'kubelet-arg:\n'
    if [[ -n "${K3S_CONTAINER_LOG_MAX_SIZE}" ]]; then
      printf '  - "container-log-max-size=%s"\n' "${K3S_CONTAINER_LOG_MAX_SIZE}"
    fi
    if [[ -n "${K3S_CONTAINER_LOG_MAX_FILES}" ]]; then
      printf '  - "container-log-max-files=%s"\n' "${K3S_CONTAINER_LOG_MAX_FILES}"
    fi
  fi
}

render_k3s_registries_config() {
  cat <<'EOF'
# Borealis-managed Spegel registry sources. Engine.sh owns this file.
mirrors:
  docker.io:
  ghcr.io:
  quay.io:
  registry.k8s.io:
EOF
}

render_k3s_api_firewall_script() {
  cat <<'EOF'
#!/usr/bin/env sh
set -eu

CHAIN="BOREALIS-K3S-API"
PORT="${BOREALIS_K3S_API_PORT:-6443}"
CLUSTER_CIDR="${BOREALIS_K3S_CLUSTER_CIDR:-10.42.0.0/16}"
SERVICE_CIDR="${BOREALIS_K3S_SERVICE_CIDR:-10.43.0.0/16}"
PEER_CIDRS="${BOREALIS_K3S_PEER_CIDRS:-}"

iptables -N "${CHAIN}" 2>/dev/null || true
iptables -F "${CHAIN}"
iptables -A "${CHAIN}" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -A "${CHAIN}" -i lo -j ACCEPT
iptables -A "${CHAIN}" -s 127.0.0.0/8 -j ACCEPT
for cidr in "${CLUSTER_CIDR}" "${SERVICE_CIDR}"; do
  iptables -A "${CHAIN}" -i cni+ -s "${cidr}" -j ACCEPT
  iptables -A "${CHAIN}" -i flannel+ -s "${cidr}" -j ACCEPT
done
if [ -n "${PEER_CIDRS}" ]; then
  old_ifs="${IFS}"
  IFS=','
  for cidr in ${PEER_CIDRS}; do
    iptables -A "${CHAIN}" -s "${cidr}" -j ACCEPT
  done
  IFS="${old_ifs}"
fi
iptables -A "${CHAIN}" -j DROP

if ! iptables -C INPUT -p tcp --dport "${PORT}" -j "${CHAIN}" 2>/dev/null; then
  iptables -I INPUT 1 -p tcp --dport "${PORT}" -j "${CHAIN}"
fi
for tcp_ports in 2379:2381 10250 5001; do
  if ! iptables -C INPUT -p tcp --dport "${tcp_ports}" -j "${CHAIN}" 2>/dev/null; then
    iptables -I INPUT 1 -p tcp --dport "${tcp_ports}" -j "${CHAIN}"
  fi
done
for udp_port in 8472 51820 51821; do
  if ! iptables -C INPUT -p udp --dport "${udp_port}" -j "${CHAIN}" 2>/dev/null; then
    iptables -I INPUT 1 -p udp --dport "${udp_port}" -j "${CHAIN}"
  fi
done
EOF
}

render_k3s_api_firewall_unit() {
  cat <<EOF
[Unit]
Description=Borealis K3s API firewall
Wants=network-online.target
After=network-online.target
Before=${K3S_SERVICE_NAME}.service

[Service]
Type=oneshot
Environment=BOREALIS_K3S_API_PORT=${K3S_API_PORT}
Environment=BOREALIS_K3S_CLUSTER_CIDR=${K3S_CLUSTER_CIDR}
Environment=BOREALIS_K3S_SERVICE_CIDR=${K3S_SERVICE_CIDR}
Environment="BOREALIS_K3S_PEER_CIDRS=${K3S_PEER_CIDRS}"
ExecStart=${K3S_FIREWALL_SCRIPT}
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
}

install_temp_file_if_changed() {
  local temp_file="$1"
  local target="$2"
  local mode="$3"
  local desired_hash
  local current_hash=""
  desired_hash="$(sha256sum "${temp_file}" | awk '{print $1}')"
  if run_privileged test -f "${target}"; then
    current_hash="$(run_privileged sha256sum "${target}" 2>/dev/null | awk '{print $1}' || true)"
  fi
  if [[ "${current_hash}" == "${desired_hash}" ]]; then
    return 1
  fi
  run_privileged install -m "${mode}" -D "${temp_file}" "${target}"
  return 0
}

write_k3s_borealis_config() {
  mkdir -p "${DEPLOY_DIR}"
  local temp_file
  local desired_hash
  temp_file="$(mktemp)"
  render_k3s_borealis_config > "${temp_file}"
  desired_hash="$(sha256sum "${temp_file}" | awk '{print $1}')"

  local changed=0
  if install_temp_file_if_changed "${temp_file}" "${K3S_BOREALIS_CONFIG}" 0644; then
    changed=1
  fi
  rm -f "${temp_file}"
  printf '%s  %s\n' "${desired_hash}" "${K3S_BOREALIS_CONFIG}" > "${K3S_CONFIG_HASH_FILE}"
  if [[ "${changed}" -eq 1 ]]; then
    printf '[%s] K3s Borealis config reconciled as %s\n' "$(date +%FT%T)" "${K3S_BOREALIS_CONFIG}" >> "${BUILD_LOG}"
    return 0
  fi
  printf '[%s] K3s Borealis config unchanged as %s\n' "$(date +%FT%T)" "${K3S_BOREALIS_CONFIG}" >> "${BUILD_LOG}"
  return 1
}

write_k3s_registries_config() {
  local temp_file
  temp_file="$(mktemp)"
  render_k3s_registries_config > "${temp_file}"
  if install_temp_file_if_changed "${temp_file}" "${K3S_REGISTRIES_CONFIG}" 0644; then
    find "$(dirname -- "${temp_file}")" -maxdepth 1 -type f -name "$(basename -- "${temp_file}")" -delete
    printf '[%s] K3s registry mirror config reconciled as %s\n' "$(date +%FT%T)" "${K3S_REGISTRIES_CONFIG}" >> "${BUILD_LOG}"
    return 0
  fi
  find "$(dirname -- "${temp_file}")" -maxdepth 1 -type f -name "$(basename -- "${temp_file}")" -delete
  printf '[%s] K3s registry mirror config unchanged as %s\n' "$(date +%FT%T)" "${K3S_REGISTRIES_CONFIG}" >> "${BUILD_LOG}"
  return 1
}

write_k3s_api_firewall_files() {
  local changed=0
  local temp_file

  temp_file="$(mktemp)"
  render_k3s_api_firewall_script > "${temp_file}"
  if install_temp_file_if_changed "${temp_file}" "${K3S_FIREWALL_SCRIPT}" 0755; then
    changed=1
  fi
  rm -f "${temp_file}"

  temp_file="$(mktemp)"
  render_k3s_api_firewall_unit > "${temp_file}"
  if install_temp_file_if_changed "${temp_file}" "/etc/systemd/system/${K3S_FIREWALL_SERVICE}" 0644; then
    changed=1
  fi
  rm -f "${temp_file}"

  if [[ "${changed}" -eq 1 ]]; then
    return 0
  fi
  return 1
}

ensure_k3s_api_firewall() {
  log_status "Ensuring Cluster Exists" "Reconciling API Firewall" "${C_YELLOW}"
  if write_k3s_api_firewall_files; then
    run_privileged systemctl daemon-reload
  else
    run_privileged systemctl daemon-reload >/dev/null 2>&1 || true
  fi
  if ! run_privileged systemctl enable "${K3S_FIREWALL_SERVICE}" >> "${BUILD_LOG}" 2>&1; then
    log_status "Ensuring Cluster Exists" "Firewall Failed" "${C_RED}"
    die "Failed to enable ${K3S_FIREWALL_SERVICE}. See ${BUILD_LOG}."
  fi
  if ! run_privileged systemctl restart "${K3S_FIREWALL_SERVICE}" >> "${BUILD_LOG}" 2>&1; then
    log_status "Ensuring Cluster Exists" "Firewall Failed" "${C_RED}"
    die "Failed to apply K3s API firewall. See ${BUILD_LOG}."
  fi
  if ! run_privileged iptables -C INPUT -p tcp --dport "${K3S_API_PORT}" -j BOREALIS-K3S-API >/dev/null 2>&1; then
    log_status "Ensuring Cluster Exists" "Firewall Failed" "${C_RED}"
    die "K3s API firewall rule missing after reconcile."
  fi
}

install_k3s_if_missing() {
  if k3s_cluster_installed; then
    log_status "Ensuring Cluster Exists" "Already Installed" "${C_GREEN}"
    return 1
  fi

  log_status "Ensuring Cluster Exists" "Installing K3s" "${C_YELLOW}"
  local install_env=(INSTALL_K3S_EXEC="server")
  if [[ -n "${K3S_INSTALL_VERSION}" ]]; then
    install_env+=(INSTALL_K3S_VERSION="${K3S_INSTALL_VERSION}")
  else
    install_env+=(INSTALL_K3S_CHANNEL="${K3S_INSTALL_CHANNEL}")
  fi
  if ! run_privileged env "${install_env[@]}" bash -c 'set -o pipefail; curl -sfL "$1" | sh -' bash "${K3S_INSTALL_SCRIPT_URL}" >> "${BUILD_LOG}" 2>&1; then
    log_status "Ensuring Cluster Exists" "Install Failed" "${C_RED}"
    die "K3s install failed. See ${BUILD_LOG}."
  fi
  log_status "Ensuring Cluster Exists" "Installed" "${C_GREEN}"
  return 0
}

ensure_k3s_service_running() {
  if run_privileged systemctl is-active --quiet "${K3S_SERVICE_NAME}.service"; then
    log_status "Ensuring Cluster Exists" "Service Running" "${C_GREEN}"
  else
    log_status "Ensuring Cluster Exists" "Starting K3s Service" "${C_YELLOW}"
  fi
  if ! run_privileged systemctl enable --now "${K3S_SERVICE_NAME}.service" >> "${BUILD_LOG}" 2>&1; then
    log_status "Ensuring Cluster Exists" "Service Failed" "${C_RED}"
    die "Failed to enable/start ${K3S_SERVICE_NAME}.service. See ${BUILD_LOG}."
  fi
  if ! run_privileged systemctl is-active --quiet "${K3S_SERVICE_NAME}.service"; then
    log_status "Ensuring Cluster Exists" "Service Failed" "${C_RED}"
    die "${K3S_SERVICE_NAME}.service is not active after reconcile."
  fi
}

wait_for_k3s_nodes_ready() {
  log_status "Ensuring Cluster Exists" "Waiting For Node Readiness" "${C_YELLOW}"
  local attempt
  local output=""
  for attempt in {1..90}; do
    output="$(k3s_kubectl get nodes --no-headers 2>>"${BUILD_LOG}" || true)"
    if [[ -n "${output}" ]] && awk 'NF < 2 || $2 !~ /(^|,)Ready(,|$)/ {bad=1} END {exit bad}' <<< "${output}"; then
      return 0
    fi
    sleep 2
  done
  printf '[%s] K3s node readiness timed out. Last output:\n%s\n' "$(date +%FT%T)" "${output}" >> "${BUILD_LOG}"
  log_status "Ensuring Cluster Exists" "Node Not Ready" "${C_RED}"
  die "K3s node readiness timed out. See ${BUILD_LOG}."
}

verify_k3s_kubeconfig() {
  if ! run_privileged test -s "${K3S_KUBECONFIG}"; then
    log_status "Ensuring Cluster Exists" "Kubeconfig Missing" "${C_RED}"
    die "K3s kubeconfig missing at ${K3S_KUBECONFIG}."
  fi
  local mode
  mode="$(run_privileged stat -c '%a' "${K3S_KUBECONFIG}" 2>/dev/null || true)"
  [[ -n "${mode}" ]] || die "Unable to read K3s kubeconfig mode at ${K3S_KUBECONFIG}."
  local group_digit
  local other_digit
  group_digit="${mode: -2:1}"
  other_digit="${mode: -1}"
  if [[ "${group_digit}" != "0" || "${other_digit}" != "0" ]]; then
    log_status "Ensuring Cluster Exists" "Kubeconfig Too Open" "${C_RED}"
    die "K3s kubeconfig ${K3S_KUBECONFIG} must not be group/world readable; current mode is ${mode}."
  fi
}

verify_k3s_container_runtime() {
  local runtimes
  runtimes="$(k3s_kubectl get nodes -o jsonpath='{range .items[*]}{.status.nodeInfo.containerRuntimeVersion}{"\n"}{end}' 2>>"${BUILD_LOG}" || true)"
  if [[ -z "${runtimes}" ]]; then
    log_status "Ensuring Cluster Exists" "Runtime Unknown" "${C_RED}"
    die "K3s node container runtime is not reported."
  fi
  if ! grep -Eq '^containerd://' <<< "${runtimes}"; then
    log_status "Ensuring Cluster Exists" "Runtime Unexpected" "${C_RED}"
    die "K3s node container runtime must be containerd in Stage 1; saw: ${runtimes}"
  fi
}

verify_k3s_packaged_ingress_disabled() {
  log_status "Ensuring Cluster Exists" "Verifying Ingress Disabled" "${C_YELLOW}"
  local attempt
  local traefik_deployment
  local traefik_service
  for attempt in {1..30}; do
    traefik_deployment="$(k3s_kubectl -n kube-system get deployment traefik --ignore-not-found --no-headers 2>>"${BUILD_LOG}" || true)"
    traefik_service="$(k3s_kubectl -n kube-system get service traefik --ignore-not-found --no-headers 2>>"${BUILD_LOG}" || true)"
    if [[ -z "${traefik_deployment}" && -z "${traefik_service}" ]]; then
      return 0
    fi
    sleep 2
  done
  log_status "Ensuring Cluster Exists" "Bundled Ingress Active" "${C_RED}"
  die "K3s bundled Traefik remains active; Borealis keeps ingress disabled until cutover."
}

ensure_k3s_namespace() {
  local config_hash
  config_hash="$(awk '{print $1; exit}' "${K3S_CONFIG_HASH_FILE}" 2>/dev/null || true)"
  [[ -n "${config_hash}" ]] || config_hash="unknown"

  log_status "Ensuring Cluster Exists" "Reconciling Namespace" "${C_YELLOW}"
  if ! k3s_kubectl get namespace "${K3S_NAMESPACE}" >/dev/null 2>&1; then
    k3s_kubectl create namespace "${K3S_NAMESPACE}" >> "${BUILD_LOG}" 2>&1
  fi
  k3s_kubectl label namespace "${K3S_NAMESPACE}" \
    app.kubernetes.io/name=borealis \
    app.kubernetes.io/part-of=borealis \
    app.kubernetes.io/managed-by=Engine.sh \
    borealis.io/stage=k3s-baseline \
    --overwrite >> "${BUILD_LOG}" 2>&1
  k3s_kubectl annotate namespace "${K3S_NAMESPACE}" \
    borealis.io/k3s-baseline-version="${K3S_BASELINE_VERSION}" \
    borealis.io/k3s-config-hash="${config_hash}" \
    borealis.io/k3s-config-path="${K3S_BOREALIS_CONFIG}" \
    --overwrite >> "${BUILD_LOG}" 2>&1
}

label_k3s_nodes() {
  local config_hash
  local node_ref
  local -a k3s_node_refs=()
  config_hash="$(awk '{print $1; exit}' "${K3S_CONFIG_HASH_FILE}" 2>/dev/null || true)"
  [[ -n "${config_hash}" ]] || config_hash="unknown"

  mapfile -t k3s_node_refs < <(k3s_kubectl get nodes -o name 2>>"${BUILD_LOG}" || true)
  ((${#k3s_node_refs[@]} > 0)) || die "K3s reported no nodes after readiness check."
  for node_ref in "${k3s_node_refs[@]}"; do
    k3s_kubectl label "${node_ref}" \
      app.kubernetes.io/part-of=borealis \
      borealis.io/engine-node=true \
      --overwrite >> "${BUILD_LOG}" 2>&1
    if ! cluster_mode_enabled; then
      k3s_kubectl label "${node_ref}" \
        borealis.io/application-state=active \
        borealis.io/edge-eligible=true \
        borealis.io/scheduler-eligible=true \
        borealis.io/postgres-primary-eligible=true \
        --overwrite >> "${BUILD_LOG}" 2>&1
    fi
    k3s_kubectl annotate "${node_ref}" \
      borealis.io/k3s-baseline-version="${K3S_BASELINE_VERSION}" \
      borealis.io/k3s-config-hash="${config_hash}" \
      --overwrite >> "${BUILD_LOG}" 2>&1
  done
}

ensure_k3s_cluster_baseline() {
  log_section "k3s Cluster"
  validate_k3s_baseline_settings
  ensure_systemctl_for_k3s
  ensure_k3s_install_dependencies

  local config_changed=0
  if write_k3s_borealis_config; then
    config_changed=1
    log_status "Ensuring Cluster Exists" "Config Updated" "${C_YELLOW}"
  else
    log_status "Ensuring Cluster Exists" "Config Up-to-Date" "${C_GREEN}"
  fi
  if write_k3s_registries_config; then
    config_changed=1
    log_status "Ensuring Cluster Exists" "Registry Mirrors Updated" "${C_YELLOW}"
  fi

  ensure_k3s_api_firewall

  local installed_now=0
  if install_k3s_if_missing; then
    installed_now=1
  fi

  if [[ "${installed_now}" -eq 0 && "${config_changed}" -eq 1 ]]; then
    log_status "Ensuring Cluster Exists" "Restarting For Config" "${C_YELLOW}"
    if ! run_privileged systemctl restart "${K3S_SERVICE_NAME}.service" >> "${BUILD_LOG}" 2>&1; then
      log_status "Ensuring Cluster Exists" "Restart Failed" "${C_RED}"
      die "Failed to restart ${K3S_SERVICE_NAME}.service after config reconcile. See ${BUILD_LOG}."
    fi
  fi

  ensure_k3s_service_running
  verify_k3s_kubeconfig
  wait_for_k3s_nodes_ready
  verify_k3s_container_runtime
  verify_k3s_packaged_ingress_disabled
  ensure_k3s_namespace
  label_k3s_nodes
  log_status "Ensuring Cluster Exists" "Ready" "${C_GREEN}"
}

systemd_unit_file_exists() {
  local unit="$1"
  systemctl list-unit-files "${unit}" --no-legend --no-pager 2>/dev/null \
    | awk '{print $1}' \
    | grep -qx "${unit}"
}

ensure_longhorn_iscsi_package() {
  command_exists iscsiadm && return 0

  log_status "k3s-longhorn-storage" "Installing iSCSI Dependency" "${C_YELLOW}"
  detect_distro
  case "${DISTRO_ID}" in
    ubuntu|debian|linuxmint|pop)
      if ! run_privileged apt-get update -qq >> "${BUILD_LOG}" 2>&1 \
        || ! run_privileged apt-get install -y open-iscsi >> "${BUILD_LOG}" 2>&1; then
        log_status "k3s-longhorn-storage" "iSCSI Install Failed" "${C_RED}"
        die "Failed to install open-iscsi for Longhorn. See ${BUILD_LOG}."
      fi
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        if ! run_privileged dnf install -y iscsi-initiator-utils >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-longhorn-storage" "iSCSI Install Failed" "${C_RED}"
          die "Failed to install iscsi-initiator-utils for Longhorn. See ${BUILD_LOG}."
        fi
      elif ! run_privileged yum install -y iscsi-initiator-utils >> "${BUILD_LOG}" 2>&1; then
        log_status "k3s-longhorn-storage" "iSCSI Install Failed" "${C_RED}"
        die "Failed to install iscsi-initiator-utils for Longhorn. See ${BUILD_LOG}."
      fi
      ;;
    arch)
      if ! run_privileged pacman -Sy --noconfirm open-iscsi >> "${BUILD_LOG}" 2>&1; then
        log_status "k3s-longhorn-storage" "iSCSI Install Failed" "${C_RED}"
        die "Failed to install open-iscsi for Longhorn. See ${BUILD_LOG}."
      fi
      ;;
    opensuse*|sles)
      if ! run_privileged zypper --non-interactive install open-iscsi >> "${BUILD_LOG}" 2>&1; then
        log_status "k3s-longhorn-storage" "iSCSI Install Failed" "${C_RED}"
        die "Failed to install open-iscsi for Longhorn. See ${BUILD_LOG}."
      fi
      ;;
    *)
      die "Unsupported distro '${DISTRO_ID}'. Install open-iscsi or iscsi-initiator-utils before Longhorn reconcile."
      ;;
  esac

  command_exists iscsiadm || die "Longhorn iSCSI dependency install completed, but iscsiadm is still missing."
}

ensure_longhorn_nfs_package() {
  command_exists mount.nfs && return 0

  log_status "k3s-longhorn-storage" "Installing NFS Dependency" "${C_YELLOW}"
  detect_distro
  case "${DISTRO_ID}" in
    ubuntu|debian|linuxmint|pop)
      if ! run_privileged apt-get update -qq >> "${BUILD_LOG}" 2>&1 \
        || ! run_privileged apt-get install -y nfs-common >> "${BUILD_LOG}" 2>&1; then
        log_status "k3s-longhorn-storage" "NFS Install Failed" "${C_RED}"
        die "Failed to install nfs-common for Longhorn RWX volumes. See ${BUILD_LOG}."
      fi
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        if ! run_privileged dnf install -y nfs-utils >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-longhorn-storage" "NFS Install Failed" "${C_RED}"
          die "Failed to install nfs-utils for Longhorn RWX volumes. See ${BUILD_LOG}."
        fi
      elif ! run_privileged yum install -y nfs-utils >> "${BUILD_LOG}" 2>&1; then
        log_status "k3s-longhorn-storage" "NFS Install Failed" "${C_RED}"
        die "Failed to install nfs-utils for Longhorn RWX volumes. See ${BUILD_LOG}."
      fi
      ;;
    arch)
      if ! run_privileged pacman -Sy --noconfirm nfs-utils >> "${BUILD_LOG}" 2>&1; then
        log_status "k3s-longhorn-storage" "NFS Install Failed" "${C_RED}"
        die "Failed to install nfs-utils for Longhorn RWX volumes. See ${BUILD_LOG}."
      fi
      ;;
    opensuse*|sles)
      if ! run_privileged zypper --non-interactive install nfs-client >> "${BUILD_LOG}" 2>&1; then
        log_status "k3s-longhorn-storage" "NFS Install Failed" "${C_RED}"
        die "Failed to install nfs-client for Longhorn RWX volumes. See ${BUILD_LOG}."
      fi
      ;;
    *)
      die "Unsupported distro '${DISTRO_ID}'. Install NFSv4 client utilities before Longhorn reconcile."
      ;;
  esac

  command_exists mount.nfs || die "Longhorn NFS dependency install completed, but mount.nfs is still missing."
}

ensure_longhorn_iscsi_kernel_module() {
  if [[ -d /sys/module/iscsi_tcp ]]; then
    return 0
  fi
  if command_exists modprobe; then
    run_privileged modprobe iscsi_tcp >> "${BUILD_LOG}" 2>&1 || true
  fi
  if [[ -d /sys/module/iscsi_tcp ]]; then
    return 0
  fi
  log_status "k3s-longhorn-storage" "iSCSI Module Missing" "${C_RED}"
  die "Longhorn requires the iscsi_tcp kernel module. Install/enable iSCSI kernel support and redeploy."
}

ensure_longhorn_iscsid_running() {
  local started_unit=0
  if systemd_unit_file_exists "iscsid.service"; then
    started_unit=1
    if ! run_privileged systemctl enable --now iscsid.service >> "${BUILD_LOG}" 2>&1; then
      log_status "k3s-longhorn-storage" "iscsid Start Failed" "${C_RED}"
      die "Failed to enable/start iscsid.service for Longhorn. See ${BUILD_LOG}."
    fi
  fi
  if systemd_unit_file_exists "iscsid.socket"; then
    started_unit=1
    run_privileged systemctl enable --now iscsid.socket >> "${BUILD_LOG}" 2>&1 || true
  fi
  if systemd_unit_file_exists "open-iscsi.service"; then
    started_unit=1
    run_privileged systemctl enable --now open-iscsi.service >> "${BUILD_LOG}" 2>&1 || true
  fi
  if run_privileged systemctl is-active --quiet iscsid.service 2>/dev/null; then
    return 0
  fi
  if command_exists pgrep && pgrep -x iscsid >/dev/null 2>&1; then
    return 0
  fi
  log_status "k3s-longhorn-storage" "iscsid Not Running" "${C_RED}"
  if [[ "${started_unit}" -eq 0 ]]; then
    die "Longhorn requires iscsid, but no iscsid/open-iscsi systemd unit was found."
  fi
  die "Longhorn requires iscsid to be running. See ${BUILD_LOG}."
}

ensure_longhorn_node_dependencies() {
  log_status "k3s-longhorn-storage" "Reconciling Dependencies" "${C_YELLOW}"
  ensure_longhorn_iscsi_package
  ensure_longhorn_nfs_package
  ensure_longhorn_iscsi_kernel_module
  ensure_longhorn_iscsid_running
}

wait_for_longhorn_rollouts() {
  local deployment
  local daemonset
  local -a deployments=()
  local -a daemonsets=()

  mapfile -t deployments < <(k3s_kubectl -n "${K3S_LONGHORN_NAMESPACE}" get deployments -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>>"${BUILD_LOG}" || true)
  ((${#deployments[@]} > 0)) || die "Longhorn namespace has no Deployments after manifest apply."
  for deployment in "${deployments[@]}"; do
    [[ -n "${deployment}" ]] || continue
    log_status "k3s-longhorn-storage" "Waiting For ${deployment}" "${C_YELLOW}"
    if ! k3s_kubectl -n "${K3S_LONGHORN_NAMESPACE}" rollout status "deployment/${deployment}" --timeout="${K3S_LONGHORN_ROLLOUT_TIMEOUT}" >> "${BUILD_LOG}" 2>&1; then
      log_status "k3s-longhorn-storage" "Rollout Failed" "${C_RED}"
      die "Longhorn Deployment ${deployment} did not become ready. See ${BUILD_LOG}."
    fi
  done

  mapfile -t daemonsets < <(k3s_kubectl -n "${K3S_LONGHORN_NAMESPACE}" get daemonsets -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>>"${BUILD_LOG}" || true)
  for daemonset in "${daemonsets[@]}"; do
    [[ -n "${daemonset}" ]] || continue
    log_status "k3s-longhorn-storage" "Waiting For ${daemonset}" "${C_YELLOW}"
    if ! k3s_kubectl -n "${K3S_LONGHORN_NAMESPACE}" rollout status "daemonset/${daemonset}" --timeout="${K3S_LONGHORN_ROLLOUT_TIMEOUT}" >> "${BUILD_LOG}" 2>&1; then
      log_status "k3s-longhorn-storage" "Rollout Failed" "${C_RED}"
      die "Longhorn DaemonSet ${daemonset} did not become ready. See ${BUILD_LOG}."
    fi
  done
}

ensure_longhorn_csi_probe_guard() {
  local patch='{"spec":{"template":{"metadata":{"annotations":{"borealis.io/liveness-startup-guard":"200"}},"spec":{"containers":[{"name":"longhorn-csi-plugin","livenessProbe":{"initialDelaySeconds":200}}]}}}}'
  if ! k3s_kubectl -n "${K3S_LONGHORN_NAMESPACE}" patch daemonset/longhorn-csi-plugin --type=strategic -p "${patch}" >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-longhorn-storage" "Probe Guard Failed" "${C_RED}"
    die "Longhorn CSI liveness startup guard could not be applied. See ${BUILD_LOG}."
  fi
  if ! k3s_kubectl -n "${K3S_LONGHORN_NAMESPACE}" rollout status daemonset/longhorn-csi-plugin --timeout="${K3S_LONGHORN_ROLLOUT_TIMEOUT}" >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-longhorn-storage" "Probe Guard Rollout Failed" "${C_RED}"
    die "Longhorn CSI liveness startup guard did not become ready. See ${BUILD_LOG}."
  fi
}

wait_for_k3s_storage_class() {
  local storage_class="$1"
  local status_key="$2"
  local attempt
  for attempt in {1..60}; do
    if k3s_kubectl get storageclass "${storage_class}" >/dev/null 2>>"${BUILD_LOG}"; then
      return 0
    fi
    sleep 2
  done
  log_status "${status_key}" "StorageClass Missing" "${C_RED}"
  die "K3s StorageClass ${storage_class} was not available after reconcile. See ${BUILD_LOG}."
}

ensure_k3s_storage_class_explicit_only() {
  local storage_class="$1"
  log_status "k3s-longhorn-storage" "Reconciling StorageClass Policy" "${C_YELLOW}"
  if ! k3s_kubectl annotate storageclass "${storage_class}" \
    storageclass.kubernetes.io/is-default-class=false \
    storageclass.beta.kubernetes.io/is-default-class=false \
    --overwrite >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-longhorn-storage" "StorageClass Policy Failed" "${C_RED}"
    die "Failed to mark StorageClass ${storage_class} as explicit-use only. See ${BUILD_LOG}."
  fi
}

render_borealis_longhorn_storage_class_manifest() {
  local storage_class="$1"
  local replica_count="$2"
  cat <<EOF
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ${storage_class}
  labels:
    app.kubernetes.io/name: ${storage_class}
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: longhorn-storage
  annotations:
    storageclass.kubernetes.io/is-default-class: "false"
    storageclass.beta.kubernetes.io/is-default-class: "false"
    borealis.io/longhorn-replica-count: "${replica_count}"
provisioner: driver.longhorn.io
allowVolumeExpansion: true
reclaimPolicy: Delete
volumeBindingMode: Immediate
parameters:
  numberOfReplicas: "${replica_count}"
  staleReplicaTimeout: "30"
  fromBackup: ""
  fsType: ext4
  dataLocality: disabled
  unmapMarkSnapChainRemoved: ignored
  disableRevisionCounter: "true"
  dataEngine: v1
  backupTargetName: default
EOF
}

ensure_borealis_longhorn_storage_class() {
  local storage_class="${K3S_BOREALIS_LONGHORN_STORAGE_CLASS}"
  local replica_count="${K3S_BOREALIS_LONGHORN_REPLICA_COUNT}"

  if [[ "${storage_class}" == "${K3S_LONGHORN_UPSTREAM_STORAGE_CLASS}" ]]; then
    ensure_k3s_storage_class_explicit_only "${storage_class}"
    return 0
  fi

  local provisioner=""
  local current_replicas=""
  if k3s_kubectl get storageclass "${storage_class}" >/dev/null 2>>"${BUILD_LOG}"; then
    provisioner="$(k3s_kubectl get storageclass "${storage_class}" -o jsonpath='{.provisioner}' 2>>"${BUILD_LOG}" || true)"
    current_replicas="$(k3s_kubectl get storageclass "${storage_class}" -o jsonpath='{.parameters.numberOfReplicas}' 2>>"${BUILD_LOG}" || true)"
    [[ "${provisioner}" == "driver.longhorn.io" ]] \
      || die "StorageClass ${storage_class} exists but is not a Longhorn StorageClass."
    [[ "${current_replicas}" == "${replica_count}" ]] \
      || die "StorageClass ${storage_class} exists with numberOfReplicas=${current_replicas}; StorageClass parameters are immutable. Create a new class name or migrate/delete unused PVCs manually."
    k3s_kubectl label storageclass "${storage_class}" \
      app.kubernetes.io/part-of=borealis \
      app.kubernetes.io/managed-by=Engine.sh \
      borealis.io/stage=longhorn-storage \
      --overwrite >> "${BUILD_LOG}" 2>&1
    k3s_kubectl annotate storageclass "${storage_class}" \
      storageclass.kubernetes.io/is-default-class=false \
      storageclass.beta.kubernetes.io/is-default-class=false \
      borealis.io/longhorn-replica-count="${replica_count}" \
      --overwrite >> "${BUILD_LOG}" 2>&1
    return 0
  fi

  local manifest_file
  manifest_file="$(mktemp "${DEPLOY_DIR}/borealis-longhorn-storage-class.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_borealis_longhorn_storage_class_manifest "${storage_class}" "${replica_count}" > "${manifest_file}"
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "k3s-longhorn-storage" "StorageClass Failed" "${C_RED}"
    die "Failed to apply Borealis Longhorn StorageClass ${storage_class}. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"
  wait_for_k3s_storage_class "${storage_class}" "k3s-longhorn-storage"
}

ensure_longhorn_storage_baseline() {
  validate_k3s_longhorn_settings
  local config_hash
  config_hash="$(
    printf '%s\n' \
      "enabled=$(normalize_enabled_flag "BOREALIS_K3S_LONGHORN_ENABLED" "${K3S_LONGHORN_ENABLED}")" \
      "namespace=${K3S_LONGHORN_NAMESPACE}" \
      "version=${K3S_LONGHORN_VERSION}" \
      "manifest_url=${K3S_LONGHORN_MANIFEST_URL}" \
      "upstream_storage_class=${K3S_LONGHORN_UPSTREAM_STORAGE_CLASS}" \
      "borealis_storage_class=${K3S_BOREALIS_LONGHORN_STORAGE_CLASS}" \
      "borealis_storage_replicas=${K3S_BOREALIS_LONGHORN_REPLICA_COUNT}" \
      "storage_class=${K3S_PVC_STORAGE_CLASS}" \
      | sha256sum | awk '{print $1}'
  )"

  if ! k3s_longhorn_enabled; then
    printf '%s  k3s-longhorn-storage disabled\n' "${config_hash}" > "${K3S_LONGHORN_CONFIG_HASH_FILE}"
    log_status "k3s-longhorn-storage" "Skipped - Disabled" "${C_DIM}"
    return 0
  fi

  ensure_longhorn_node_dependencies
  if [[ "$(stored_k3s_config_hash "${K3S_LONGHORN_CONFIG_HASH_FILE}")" == "${config_hash}" ]] \
    && k3s_resource_annotation_matches "namespace/${K3S_LONGHORN_NAMESPACE}" "borealis.io/longhorn-config-hash" "${config_hash}" "-" \
    && k3s_kubectl get storageclass "${K3S_LONGHORN_UPSTREAM_STORAGE_CLASS}" >/dev/null 2>>"${BUILD_LOG}"; then
    if [[ "${K3S_BOREALIS_LONGHORN_STORAGE_CLASS}" == "${K3S_LONGHORN_UPSTREAM_STORAGE_CLASS}" ]] \
      || k3s_kubectl get storageclass "${K3S_BOREALIS_LONGHORN_STORAGE_CLASS}" >/dev/null 2>>"${BUILD_LOG}"; then
      ensure_longhorn_csi_probe_guard
      log_k3s_manifest_unchanged "k3s-longhorn-storage" "${config_hash}"
      return 0
    fi
  fi

  log_status "k3s-longhorn-storage" "Applying Manifests" "${C_YELLOW}"
  if ! k3s_kubectl apply -f "${K3S_LONGHORN_MANIFEST_URL}" >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-longhorn-storage" "Apply Failed" "${C_RED}"
    die "Failed to apply Longhorn manifest ${K3S_LONGHORN_MANIFEST_URL}. See ${BUILD_LOG}."
  fi

  if ! k3s_kubectl get namespace "${K3S_LONGHORN_NAMESPACE}" >/dev/null 2>&1; then
    log_status "k3s-longhorn-storage" "Namespace Missing" "${C_RED}"
    die "Longhorn namespace ${K3S_LONGHORN_NAMESPACE} missing after manifest apply."
  fi
  k3s_kubectl label namespace "${K3S_LONGHORN_NAMESPACE}" \
    app.kubernetes.io/part-of=borealis \
    app.kubernetes.io/managed-by=Engine.sh \
    borealis.io/stage=longhorn-storage \
    --overwrite >> "${BUILD_LOG}" 2>&1
  k3s_kubectl annotate namespace "${K3S_LONGHORN_NAMESPACE}" \
    borealis.io/longhorn-config-hash="${config_hash}" \
    borealis.io/longhorn-version="${K3S_LONGHORN_VERSION}" \
    borealis.io/longhorn-manifest-url="${K3S_LONGHORN_MANIFEST_URL}" \
    borealis.io/longhorn-upstream-storage-class="${K3S_LONGHORN_UPSTREAM_STORAGE_CLASS}" \
    borealis.io/borealis-longhorn-storage-class="${K3S_BOREALIS_LONGHORN_STORAGE_CLASS}" \
    borealis.io/borealis-longhorn-replica-count="${K3S_BOREALIS_LONGHORN_REPLICA_COUNT}" \
    borealis.io/pvc-storage-class="${K3S_PVC_STORAGE_CLASS}" \
    --overwrite >> "${BUILD_LOG}" 2>&1

  wait_for_longhorn_rollouts
  ensure_longhorn_csi_probe_guard
  wait_for_k3s_storage_class "${K3S_LONGHORN_UPSTREAM_STORAGE_CLASS}" "k3s-longhorn-storage"
  ensure_k3s_storage_class_explicit_only "${K3S_LONGHORN_UPSTREAM_STORAGE_CLASS}"
  ensure_borealis_longhorn_storage_class
  printf '%s  k3s-longhorn-storage\n' "${config_hash}" > "${K3S_LONGHORN_CONFIG_HASH_FILE}"
  log_status "k3s-longhorn-storage" "Ready - StorageClass" "${C_GREEN}"
}

k3s_ctr() {
  local k3s_bin
  k3s_bin="$(k3s_binary_path)" || die "K3s binary missing after install."
  run_privileged "${k3s_bin}" ctr -n k8s.io "$@"
}

k3s_containerd_image_present() {
  local image="$1"
  local normalized_image="${image}"
  if [[ "${image}" != */*/* && "${image}" != localhost/* ]]; then
    normalized_image="docker.io/${image}"
  fi
  k3s_ctr images ls -q 2>/dev/null \
    | awk -v exact="${image}" -v normalized="${normalized_image}" '$0 == exact || $0 == normalized {found=1} END {exit found ? 0 : 1}'
}

k3s_containerd_image_distribution_ready() {
  local image="$1"
  local normalized_image="${image}"
  if [[ "${image}" != */*/* && "${image}" != localhost/* ]]; then
    normalized_image="docker.io/${image}"
  fi
  local registry="${normalized_image%%/*}"
  local repository="${normalized_image#*/}"
  repository="${repository%%@*}"
  repository="${repository%:*}"
  local digest=""
  digest="$(k3s_ctr images ls 2>/dev/null | awk -v exact="${image}" -v normalized="${normalized_image}" '$1 == exact || $1 == normalized {print $3; exit}')"
  [[ "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]] || return 1
  k3s_ctr content ls 2>/dev/null \
    | awk -v exact="${digest}" -v source="containerd.io/distribution.source.${registry}=${repository}" \
      '$1 == exact && index($0, source) {found=1} END {exit found ? 0 : 1}'
}

import_k3s_local_image_into_k3s() {
  local service="$1"
  local image="$2"
  local status_row="${3:-}"
  local service_slug="${service//[^[:alnum:]_.-]/-}"
  local archive_id=""
  archive_id="$(printf '%s' "${image}" | sha256sum | awk '{print substr($1, 1, 16)}')"
  local image_archive=""
  local pinned_archive="${K3S_IMAGE_IMPORT_DIR}/borealis-${service_slug}-${archive_id}.tar"
  if k3s_containerd_image_present "${image}" \
    && k3s_containerd_image_distribution_ready "${image}" \
    && run_privileged test -s "${pinned_archive}"; then
    printf '[%s] %s image already pinned and Spegel-ready in K3s containerd: %s\n' "$(date +%FT%T)" "${service}" "${image}" >> "${BUILD_LOG}"
    return 0
  fi
  if ! docker image inspect "${image}" >/dev/null 2>&1; then
    if [[ -n "${status_row}" ]]; then
      log_status "${status_row}" "Image Missing" "${C_RED}"
    fi
    die "${service} image ${image} is missing from Docker before K3s import."
  fi
  if [[ -n "${status_row}" ]]; then
    log_status "${status_row}" "Importing Image" "${C_YELLOW}"
  fi
  image_archive="$(mktemp "${DEPLOY_DIR}/${service_slug}-image.XXXXXX.tar")"
  if ! docker image save "${image}" -o "${image_archive}" >> "${BUILD_LOG}" 2>&1; then
    find "${DEPLOY_DIR}" -maxdepth 1 -type f -name "$(basename -- "${image_archive}")" -delete
    if [[ -n "${status_row}" ]]; then
      log_status "${status_row}" "Image Export Failed" "${C_RED}"
    fi
    die "Failed to export ${image} before K3s import. See ${BUILD_LOG}."
  fi
  local pending_archive="${pinned_archive}.pending.$$"
  run_privileged install -d -m 0755 "${K3S_IMAGE_IMPORT_DIR}"
  if ! run_privileged install -m 0644 "${image_archive}" "${pending_archive}" >> "${BUILD_LOG}" 2>&1 \
    || ! run_privileged mv "${pending_archive}" "${pinned_archive}" >> "${BUILD_LOG}" 2>&1; then
    find "${DEPLOY_DIR}" -maxdepth 1 -type f -name "$(basename -- "${image_archive}")" -delete
    if [[ -n "${status_row}" ]]; then
      log_status "${status_row}" "Image Import Failed" "${C_RED}"
    fi
    die "Failed to stage ${image} for K3s pre-import. See ${BUILD_LOG}."
  fi
  find "${DEPLOY_DIR}" -maxdepth 1 -type f -name "$(basename -- "${image_archive}")" -delete
  local attempt=""
  for attempt in $(seq 1 60); do
    if k3s_containerd_image_present "${image}" && k3s_containerd_image_distribution_ready "${image}"; then
      printf '[%s] %s image pinned and Spegel-ready in K3s containerd: %s\n' "$(date +%FT%T)" "${service}" "${image}" >> "${BUILD_LOG}"
      return 0
    fi
    sleep 2
  done
  if [[ -n "${status_row}" ]]; then
    log_status "${status_row}" "Image Import Failed" "${C_RED}"
  fi
  die "K3s did not pre-import and label ${image} for Spegel within 120 seconds. See ${BUILD_LOG}."
}

import_borealis_operator_image_into_k3s() {
  import_k3s_local_image_into_k3s "borealis-operator" "$1" "borealis-operator"
}

borealis_operator_workload_image_allowlist() {
  local service=""
  local image=""
  local entries=()
  for service in \
    "api-backend" \
    "borealis-operator" \
    "job-scheduler" \
    "postgres-db" \
    "remote-desktop-guacd" \
    "traefik-edge" \
    "webui-frontend" \
    "wireguard-tunnel"; do
    image="${IMAGE_TAGS[${service}]:-}"
    [[ -n "${image}" ]] || image="$(previous_image_tag "${service}")"
    [[ -n "${image}" ]] || continue
    entries+=("${service}=${image}")
  done
  (IFS=,; printf '%s\n' "${entries[*]}")
}

borealis_operator_site_worker_image_allowlist() {
  local image="${IMAGE_TAGS[site-worker]:-}"
  [[ -n "${image}" ]] || image="$(previous_image_tag site-worker)"
  {
    if [[ -n "${image}" ]]; then
      printf '%s\n' "${image}"
    fi
    if [[ -n "${BOREALIS_SITE_WORKER_IMAGE_ALLOWLIST_EXTRA:-}" ]]; then
      printf '%s\n' "${BOREALIS_SITE_WORKER_IMAGE_ALLOWLIST_EXTRA}"
    fi
  } | awk 'NF && !seen[$0]++' | paste -sd, -
}

base64_inline() {
  local value="${1-}"
  printf '%s' "${value}" | base64 -w 0 2>/dev/null || printf '%s' "${value}" | base64 | tr -d '\n'
}

borealis_runtime_env_secret_data() {
  local file="${1:-${RUNTIME_ENV}}"
  local line=""
  local key=""
  local value=""
  [[ -f "${file}" ]] || return 0
  while IFS= read -r line || [[ -n "${line}" ]]; do
    [[ -n "${line}" && "${line}" != \#* && "${line}" == *=* ]] || continue
    key="${line%%=*}"
    value="${line#*=}"
    [[ "${key}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
    printf '  %s: "%s"\n' "${key}" "$(base64_inline "${value}")"
  done < "${file}"
}

borealis_runtime_env_secret_data_with_override() {
  local file="${1:-${RUNTIME_ENV}}"
  local override_key="${2:-}"
  local override_value="${3:-}"
  local line=""
  local key=""
  local value=""
  local override_written=0
  [[ -n "${override_key}" ]] || die "Runtime env Secret override key is required."
  [[ "${override_key}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || die "Invalid runtime env Secret override key '${override_key}'."
  [[ -f "${file}" ]] || return 0
  while IFS= read -r line || [[ -n "${line}" ]]; do
    [[ -n "${line}" && "${line}" != \#* && "${line}" == *=* ]] || continue
    key="${line%%=*}"
    value="${line#*=}"
    [[ "${key}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
    if [[ "${key}" == "${override_key}" ]]; then
      value="${override_value}"
      override_written=1
    fi
    printf '  %s: "%s"\n' "${key}" "$(base64_inline "${value}")"
  done < "${file}"
  if ((override_written == 0)); then
    printf '  %s: "%s"\n' "${override_key}" "$(base64_inline "${override_value}")"
  fi
}

borealis_postgres_runtime_secret_data() {
  local key
  for key in \
    POSTGRES_DB \
    POSTGRES_USER \
    POSTGRES_PASSWORD \
    BOREALIS_ENGINE_HOST_TIMEZONE \
    TZ; do
    printf '  %s: "%s"\n' "${key}" "$(base64_inline "$(read_env_value "${key}")")"
  done
}

borealis_traefik_runtime_secret_data() {
  local key
  for key in \
    BOREALIS_PROJECT_ROOT \
    BOREALIS_PUBLIC_HOSTNAME \
    BOREALIS_PUBLIC_HOSTNAME_ALIASES \
    BOREALIS_PUBLIC_BASE_URL \
    BOREALIS_PUBLIC_HTTP_PORT \
    BOREALIS_PUBLIC_HTTPS_PORT \
    BOREALIS_PUBLIC_VNC_PATH \
    BOREALIS_PUBLIC_WIREGUARD_HOST \
    BOREALIS_PUBLIC_WIREGUARD_PORT \
    BOREALIS_ENGINE_NETWORK_MODE \
    BOREALIS_ENGINE_NETWORK_MODE_LABEL \
    BOREALIS_ENGINE_DEPLOYMENT_PROFILE \
    BOREALIS_ENGINE_DEPLOYMENT_PROFILE_LABEL \
    BOREALIS_ACME_EMAIL \
    BOREALIS_LOCAL_CA_ENABLED \
    BOREALIS_LOCAL_CA_CERT_PATH \
    BOREALIS_LOCAL_TLS_CERT_PATH \
    BOREALIS_LOCAL_TLS_KEY_PATH \
    BOREALIS_WEBUI_TRAFFIC_OWNER \
    BOREALIS_WEBUI_UPSTREAM_HOST \
    BOREALIS_WEBUI_UPSTREAM_PORT \
    BOREALIS_API_BACKEND_TRAFFIC_OWNER \
    BOREALIS_API_BACKEND_UPSTREAM_HOST \
    BOREALIS_API_BACKEND_UPSTREAM_PORT \
    BOREALIS_VNC_WS_PORT \
    BOREALIS_TRAEFIK_HEALTH_PORT \
    BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS \
    BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS \
    BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS \
    BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR \
    BOREALIS_TRAEFIK_DYNAMIC_CONFIG_PATH \
    BOREALIS_ENGINE_RUNTIME_OWNER_UID \
    BOREALIS_ENGINE_RUNTIME_OWNER_GID \
    BOREALIS_ENGINE_HOST_TIMEZONE \
    TZ; do
    printf '  %s: "%s"\n' "${key}" "$(base64_inline "$(read_env_value "${key}")")"
  done
}

host_timezone_value() {
  local timezone
  timezone="$(read_env_value TZ)"
  if [[ -z "${timezone}" ]]; then
    timezone="$(resolve_host_timezone)"
  fi
  printf '%s\n' "${timezone}"
}

k3s_timezone_env_entries() {
  local timezone
  timezone="$(host_timezone_value)"
  cat <<EOF
            - name: TZ
              value: "${timezone}"
            - name: BOREALIS_ENGINE_HOST_TIMEZONE
              value: "${timezone}"
EOF
}

k3s_timezone_volume_mount_entries() {
  cat <<'EOF'
            - name: host-localtime
              mountPath: /etc/localtime
              readOnly: true
            - name: host-zoneinfo
              mountPath: /usr/share/zoneinfo
              readOnly: true
EOF
}

k3s_timezone_volume_entries() {
  cat <<'EOF'
        - name: host-localtime
          hostPath:
            path: /etc/localtime
            type: File
        - name: host-zoneinfo
          hostPath:
            path: /usr/share/zoneinfo
            type: Directory
EOF
}

k3s_postgres_database_url() {
  local db_name=""
  local db_user=""
  local db_password=""
  db_name="$(read_env_value POSTGRES_DB)"
  db_user="$(read_env_value POSTGRES_USER)"
  db_password="$(read_env_value POSTGRES_PASSWORD)"
  [[ -n "${db_name}" && -n "${db_user}" && -n "${db_password}" ]] || die "PostgreSQL runtime credentials unavailable for K3s API backend DB validation."
  printf 'postgresql://%s:%s@postgres-db.%s.svc:5432/%s\n' "${db_user}" "${db_password}" "${K3S_NAMESPACE}" "${db_name}"
}

borealis_site_worker_runtime_secret_keys() {
  cat <<'EOF'
BOREALIS_PROJECT_ROOT
BOREALIS_ENGINE_MODE
BOREALIS_ENGINE_CONTAINERIZED
BOREALIS_ENGINE_HOST_TIMEZONE
TZ
BOREALIS_INTERNAL_API_BASE_URL
BOREALIS_DATABASE_URL
BOREALIS_DB_SSLMODE
BOREALIS_DB_POOL_SIZE
BOREALIS_DB_MAX_OVERFLOW
BOREALIS_DB_CONNECT_TIMEOUT
BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS
BOREALIS_ENGINE_SECRET_PATH
BOREALIS_ENGINE_CERT_ROOT
BOREALIS_ENGINE_AUTH_TOKEN_ROOT
BOREALIS_ANSIBLE_RUNTIME_ROOT
BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH
BOREALIS_SITE_WORKER_SETTINGS_PATH
BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY
BOREALIS_SITE_WORKER_ANSIBLE_CONCURRENCY
BOREALIS_OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT
BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE
BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS
BOREALIS_GUACD_HOST
BOREALIS_GUACD_PORT
BOREALIS_GUACAMOLE_VNC_WS_PATH
EOF
}

borealis_site_worker_runtime_secret_data() {
  local key
  while IFS= read -r key; do
    [[ -n "${key}" ]] || continue
    printf '  %s: "%s"\n' "${key}" "$(base64_inline "$(read_env_value "${key}")")"
  done < <(borealis_site_worker_runtime_secret_keys)
}

borealis_site_worker_runtime_secret_hash() {
  local key
  {
    while IFS= read -r key; do
      [[ -n "${key}" ]] || continue
      printf '%s=%s\n' "${key}" "$(read_env_value "${key}")"
    done < <(borealis_site_worker_runtime_secret_keys)
    printf 'BOREALIS_SITE_WORKER_PROBE_CONTRACT=startup-budget-liveness-v1\n'
  } | sha256sum | awk '{print $1}'
}

generic_k3s_workload_replicas() {
  if cluster_mode_enabled; then
    printf '0\n'
    return 0
  fi
  printf '1\n'
}

render_borealis_operator_manifest() {
  local image="$1"
  local secret="$2"
  local config_hash="$3"
  local workload_image_allowlist="$4"
  local site_worker_image_allowlist="$5"
  local site_worker_runtime_secret_hash="$6"
  local secret_b64
  local runtime_uid
  local runtime_gid
  local replicas
  secret_b64="$(base64_inline "${secret}")"
  runtime_uid="$(resolve_runtime_owner_uid)"
  runtime_gid="$(resolve_runtime_owner_gid)"
  replicas="$(generic_k3s_workload_replicas)"
  cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${BOREALIS_OPERATOR_SECRET_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: borealis-operator
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: operator-bridge
  annotations:
    borealis.io/operator-config-hash: "${config_hash}"
type: Opaque
data:
  BOREALIS_OPERATOR_SECRET: "${secret_b64}"
---
apiVersion: v1
kind: Secret
metadata:
  name: ${BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: site-worker
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: site-worker-migration
  annotations:
    borealis.io/operator-config-hash: "${config_hash}"
    borealis.io/site-worker-runtime-config-hash: "${site_worker_runtime_secret_hash}"
type: Opaque
data:
$(borealis_site_worker_runtime_secret_data)
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${BOREALIS_OPERATOR_SERVICE_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: borealis-operator
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: operator-bridge
  annotations:
    borealis.io/operator-config-hash: "${config_hash}"
automountServiceAccountToken: true
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: ${BOREALIS_OPERATOR_SERVICE_NAME}-controller
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: borealis-operator
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: operator-bridge
  annotations:
    borealis.io/operator-config-hash: "${config_hash}"
rules:
  - apiGroups: [""]
    resources: ["pods", "services"]
    verbs: ["get", "list"]
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["create", "delete"]
  - apiGroups: [""]
    resources: ["services"]
    verbs: ["create", "delete"]
  - apiGroups: ["apps"]
    resources: ["deployments", "replicasets", "statefulsets"]
    verbs: ["get", "list"]
  - apiGroups: ["metrics.k8s.io"]
    resources: ["pods"]
    verbs: ["get", "list"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    resourceNames: ["api-backend", "borealis-operator", "job-scheduler", "remote-desktop-guacd", "traefik-edge", "webui-frontend", "wireguard-tunnel"]
    verbs: ["get", "patch"]
  - apiGroups: ["apps"]
    resources: ["statefulsets"]
    resourceNames: ["postgres-db"]
    verbs: ["get", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ${BOREALIS_OPERATOR_SERVICE_NAME}-controller
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: borealis-operator
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: operator-bridge
  annotations:
    borealis.io/operator-config-hash: "${config_hash}"
subjects:
  - kind: ServiceAccount
    name: ${BOREALIS_OPERATOR_SERVICE_NAME}
    namespace: ${K3S_NAMESPACE}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: ${BOREALIS_OPERATOR_SERVICE_NAME}-controller
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${BOREALIS_OPERATOR_SERVICE_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: borealis-operator
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: operator
    borealis.io/stage: operator-bridge
  annotations:
    borealis.io/operator-config-hash: "${config_hash}"
spec:
  replicas: ${replicas}
  revisionHistoryLimit: 2
  minReadySeconds: 15
  selector:
    matchLabels:
      app.kubernetes.io/name: borealis-operator
      app.kubernetes.io/part-of: borealis
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  template:
    metadata:
      labels:
        app.kubernetes.io/name: borealis-operator
        app.kubernetes.io/part-of: borealis
        app.kubernetes.io/managed-by: Engine.sh
        app.kubernetes.io/component: operator
        borealis.io/stage: operator-bridge
      annotations:
        borealis.io/operator-config-hash: "${config_hash}"
    spec:
      serviceAccountName: ${BOREALIS_OPERATOR_SERVICE_NAME}
      automountServiceAccountToken: true
      enableServiceLinks: false
      securityContext:
        runAsNonRoot: true
        runAsUser: ${runtime_uid}
        runAsGroup: ${runtime_gid}
        fsGroup: ${runtime_gid}
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: borealis-operator
          image: ${image}
          imagePullPolicy: IfNotPresent
          args: ["borealis-operator"]
          ports:
            - name: http
              containerPort: ${BOREALIS_OPERATOR_PORT}
              protocol: TCP
          env:
$(k3s_timezone_env_entries)
            - name: BOREALIS_PROCESS_ROLE
              value: "borealis-operator"
            - name: BOREALIS_OPERATOR_LISTEN_HOST
              value: "0.0.0.0"
            - name: BOREALIS_OPERATOR_LISTEN_PORT
              value: "${BOREALIS_OPERATOR_PORT}"
            - name: BOREALIS_OPERATOR_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
            - name: BOREALIS_OPERATOR_SECRET
              valueFrom:
                secretKeyRef:
                  name: ${BOREALIS_OPERATOR_SECRET_NAME}
                  key: BOREALIS_OPERATOR_SECRET
            - name: BOREALIS_OPERATOR_WORKLOAD_IMAGE_ALLOWLIST
              value: "${workload_image_allowlist}"
            - name: BOREALIS_OPERATOR_SITE_WORKER_IMAGE_ALLOWLIST
              value: "${site_worker_image_allowlist}"
            - name: BOREALIS_PROJECT_ROOT
              value: "${ENGINE_HOST_ROOT}"
            - name: BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME
              value: "${BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME}"
            - name: BOREALIS_SITE_WORKER_RUNTIME_CONFIG_HASH
              value: "${site_worker_runtime_secret_hash}"
            - name: BOREALIS_ENGINE_RUNTIME_OWNER_UID
              value: "${runtime_uid}"
            - name: BOREALIS_ENGINE_RUNTIME_OWNER_GID
              value: "${runtime_gid}"
            - name: BOREALIS_DRAIN_FILE
              value: "/tmp/borealis-draining"
          startupProbe:
            httpGet:
              path: /startup
              port: http
            periodSeconds: 2
            timeoutSeconds: 1
            failureThreshold: 60
          livenessProbe:
            httpGet:
              path: /live
              port: http
            initialDelaySeconds: 130
            periodSeconds: 10
            timeoutSeconds: 3
            failureThreshold: 3
          readinessProbe:
            httpGet:
              path: /ready
              port: http
            initialDelaySeconds: 3
            periodSeconds: 10
            timeoutSeconds: 3
            failureThreshold: 3
          lifecycle:
            preStop:
              exec:
                command: ["sh", "-c", "touch /tmp/borealis-draining; sleep 10"]
          resources:
            requests:
              cpu: 25m
              memory: 48Mi
            limits:
              cpu: 250m
              memory: 160Mi
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
$(k3s_timezone_volume_mount_entries)
            - name: tmp
              mountPath: /tmp
      volumes:
$(k3s_timezone_volume_entries)
        - name: tmp
          emptyDir:
            medium: Memory
            sizeLimit: 16Mi
---
apiVersion: v1
kind: Service
metadata:
  name: ${BOREALIS_OPERATOR_SERVICE_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: borealis-operator
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: operator
    borealis.io/stage: operator-bridge
  annotations:
    borealis.io/operator-config-hash: "${config_hash}"
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: borealis-operator
    app.kubernetes.io/part-of: borealis
  ports:
    - name: http
      port: ${BOREALIS_OPERATOR_PORT}
      targetPort: http
      protocol: TCP
EOF
}

retire_legacy_borealis_operator_readonly_rbac() {
  local legacy_name="${BOREALIS_OPERATOR_SERVICE_NAME}-readonly"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" delete rolebinding "${legacy_name}" --ignore-not-found=true >> "${BUILD_LOG}" 2>&1; then
    log_status "borealis-operator" "Legacy RBAC Cleanup Failed" "${C_RED}"
    die "Failed to remove legacy Borealis operator RoleBinding '${legacy_name}'. See ${BUILD_LOG}."
  fi
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" delete role "${legacy_name}" --ignore-not-found=true >> "${BUILD_LOG}" 2>&1; then
    log_status "borealis-operator" "Legacy RBAC Cleanup Failed" "${C_RED}"
    die "Failed to remove legacy Borealis operator Role '${legacy_name}'. See ${BUILD_LOG}."
  fi
}

ensure_borealis_operator_bridge() {
  local image="${IMAGE_TAGS[borealis-operator]:-}"
  [[ -n "${image}" ]] || image="$(previous_image_tag borealis-operator)"
  [[ -n "${image}" ]] || die "Borealis operator image tag unavailable."
  local secret
  secret="$(read_env_value BOREALIS_OPERATOR_SECRET)"
  [[ -n "${secret}" ]] || die "Borealis operator secret unavailable after runtime env render."
  local workload_image_allowlist
  local site_worker_image_allowlist
  local site_worker_runtime_secret_hash
  workload_image_allowlist="$(borealis_operator_workload_image_allowlist)"
  site_worker_image_allowlist="$(borealis_operator_site_worker_image_allowlist)"
  site_worker_runtime_secret_hash="$(borealis_site_worker_runtime_secret_hash)"
  local config_hash
  config_hash="$(printf '%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n%s\n' "${image}" "${K3S_NAMESPACE}" "${BOREALIS_OPERATOR_SERVICE_NAME}" "${BOREALIS_OPERATOR_PORT}" "${secret}" "${workload_image_allowlist}" "${site_worker_image_allowlist}" "${BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME}" "${site_worker_runtime_secret_hash}" "${ENGINE_HOST_ROOT}" "$(host_timezone_value)" "timezone-host-mounts-v1" "operator-rbac-controller-v2" | sha256sum | awk '{print $1}')"

  import_borealis_operator_image_into_k3s "${image}"
  if [[ -n "${site_worker_image_allowlist}" ]]; then
    local allowed_site_worker_image=""
    local -a allowed_site_worker_images=()
    IFS=',' read -r -a allowed_site_worker_images <<< "${site_worker_image_allowlist}"
    for allowed_site_worker_image in "${allowed_site_worker_images[@]}"; do
      [[ -n "${allowed_site_worker_image}" ]] || continue
      import_k3s_local_image_into_k3s "site-worker" "${allowed_site_worker_image}" ""
    done
  fi
  retire_legacy_borealis_operator_readonly_rbac
  if k3s_manifest_config_current \
    "${BOREALIS_OPERATOR_CONFIG_HASH_FILE}" \
    "${config_hash}" \
    "borealis.io/operator-config-hash" \
    "secret/${BOREALIS_OPERATOR_SECRET_NAME}" \
    "secret/${BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME}" \
    "serviceaccount/${BOREALIS_OPERATOR_SERVICE_NAME}" \
    "role/${BOREALIS_OPERATOR_SERVICE_NAME}-controller" \
    "rolebinding/${BOREALIS_OPERATOR_SERVICE_NAME}-controller" \
    "deployment/${BOREALIS_OPERATOR_SERVICE_NAME}" \
    "service/${BOREALIS_OPERATOR_SERVICE_NAME}"; then
    log_k3s_manifest_unchanged "borealis-operator" "${config_hash}"
    return 0
  fi
  log_status "borealis-operator" "Applying Manifests" "${C_YELLOW}"
  local manifest_file
  manifest_file="$(mktemp "${DEPLOY_DIR}/borealis-operator.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_borealis_operator_manifest "${image}" "${secret}" "${config_hash}" "${workload_image_allowlist}" "${site_worker_image_allowlist}" "${site_worker_runtime_secret_hash}" > "${manifest_file}"
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "borealis-operator" "Apply Failed" "${C_RED}"
    die "Failed to apply Borealis operator manifests. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"

  log_status "borealis-operator" "Waiting For Rollout" "${C_YELLOW}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/${BOREALIS_OPERATOR_SERVICE_NAME}" --timeout=90s >> "${BUILD_LOG}" 2>&1; then
    log_status "borealis-operator" "Rollout Failed" "${C_RED}"
    die "Borealis operator rollout failed. See ${BUILD_LOG}."
  fi
  printf '%s  borealis-operator\n' "${config_hash}" > "${BOREALIS_OPERATOR_CONFIG_HASH_FILE}"
  log_status "borealis-operator" "Ready" "${C_GREEN}"
}

format_k3s_memory_quantity() {
  local raw="${1:-}"
  raw="${raw//[[:space:]]/}"
  [[ -n "${raw}" ]] || raw="128Mi"
  local lower
  lower="$(printf '%s' "${raw}" | tr '[:upper:]' '[:lower:]')"
  if [[ "${lower}" =~ ^([0-9]+)m$ ]]; then
    printf '%sMi\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  if [[ "${lower}" =~ ^([0-9]+)g$ ]]; then
    printf '%sGi\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  if [[ "${lower}" =~ ^([0-9]+)k$ ]]; then
    printf '%sKi\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  if [[ "${raw}" =~ ^[0-9]+([.][0-9]+)?(Ki|Mi|Gi|Ti|K|M|G|T)?$ ]]; then
    printf '%s\n' "${raw}"
    return 0
  fi
  die "Unsupported Kubernetes memory quantity '${raw}'. Use values like 256m, 1g, 256Mi, or 1Gi."
}

format_k3s_cpu_quantity() {
  local raw="${1:-}"
  raw="${raw//[[:space:]]/}"
  [[ -n "${raw}" ]] || raw="100m"
  if [[ "${raw}" =~ ^[0-9]+m$ ]]; then
    printf '%s\n' "${raw}"
    return 0
  fi
  if [[ "${raw}" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    awk -v cpu="${raw}" 'BEGIN { printf "%dm\n", int(cpu * 1000 + 0.5) }'
    return 0
  fi
  die "Unsupported Kubernetes CPU quantity '${raw}'. Use values like 0.50, 1.00, or 500m."
}

format_k3s_tcp_port() {
  local raw="${1:-}"
  raw="${raw//[[:space:]]/}"
  local port_value=0
  if [[ "${raw}" =~ ^[0-9]+$ ]]; then
    port_value=$((10#${raw}))
  fi
  if [[ "${raw}" =~ ^[0-9]+$ ]] && ((port_value >= 1 && port_value <= 65535)); then
    printf '%s\n' "${raw}"
    return 0
  fi
  die "Unsupported TCP port '${raw}'. Use a number from 1 to 65535."
}

service_image_tag_or_previous() {
  local service="$1"
  local fallback="${2:-}"
  local image="${IMAGE_TAGS[${service}]:-}"
  [[ -n "${image}" ]] || image="$(previous_image_tag "${service}")"
  [[ -n "${image}" ]] || image="${fallback}"
  printf '%s\n' "${image}"
}

render_k3s_api_backend_bridge_manifest() {
  local image="$1"
  local config_hash="$2"
  local runtime_uid="$3"
  local runtime_gid="$4"
  local port="$5"
  local memory_limit="$6"
  local cpu_limit="$7"
  local traffic_owner="$8"
  local release_version="$9"
  local source_sha="${10}"
  local service_host
  local replicas
  service_host="$(api_backend_service_dns_name)"
  replicas="$(generic_k3s_workload_replicas)"
  cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${BOREALIS_API_BACKEND_RUNTIME_SECRET_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: api-backend
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: api-backend-cutover
  annotations:
    borealis.io/bridge-config-hash: "${config_hash}"
type: Opaque
data:
$(borealis_runtime_env_secret_data "${RUNTIME_ENV}")
---
apiVersion: v1
kind: Service
metadata:
  name: api-backend
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: api-backend
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: backend
    borealis.io/service-key: api-backend
    borealis.io/stage: api-backend-cutover
  annotations:
    borealis.io/bridge-config-hash: "${config_hash}"
    borealis.io/network-mode: "cluster-ip"
    borealis.io/traffic-owner: "${traffic_owner}"
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: api-backend
    app.kubernetes.io/part-of: borealis
  ports:
    - name: http
      port: ${port}
      targetPort: http
      protocol: TCP
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api-backend
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: api-backend
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: backend
    borealis.io/service-key: api-backend
    borealis.io/stage: api-backend-cutover
  annotations:
    borealis.io/bridge-config-hash: "${config_hash}"
    borealis.io/network-mode: "cluster-ip"
    borealis.io/traffic-owner: "${traffic_owner}"
spec:
  replicas: ${replicas}
  revisionHistoryLimit: 2
  selector:
    matchLabels:
      app.kubernetes.io/name: api-backend
      app.kubernetes.io/part-of: borealis
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
  template:
    metadata:
      labels:
        app.kubernetes.io/name: api-backend
        app.kubernetes.io/part-of: borealis
        app.kubernetes.io/managed-by: Engine.sh
        app.kubernetes.io/component: backend
        borealis.io/service-key: api-backend
        borealis.io/stage: api-backend-cutover
      annotations:
        borealis.io/bridge-config-hash: "${config_hash}"
        borealis.io/network-mode: "cluster-ip"
        borealis.io/traffic-owner: "${traffic_owner}"
        borealis.io/listen-host: "0.0.0.0"
        borealis.io/listen-port: "${port}"
        borealis.io/service-host: "${service_host}"
        borealis.io/pids-limit: "$(read_env_value BOREALIS_API_BACKEND_PIDS_LIMIT)"
    spec:
      automountServiceAccountToken: false
      enableServiceLinks: false
      hostNetwork: false
      dnsPolicy: ClusterFirst
      terminationGracePeriodSeconds: 45
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: borealis.io/application-state
                    operator: In
                    values: ["active"]
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              podAffinityTerm:
                topologyKey: kubernetes.io/hostname
                labelSelector:
                  matchLabels:
                    app.kubernetes.io/name: api-backend
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: api-backend
      securityContext:
        runAsNonRoot: true
        runAsUser: ${runtime_uid}
        runAsGroup: ${runtime_gid}
        fsGroup: ${runtime_gid}
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: api-backend
          image: ${image}
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: ${port}
              protocol: TCP
          envFrom:
            - secretRef:
                name: ${BOREALIS_API_BACKEND_RUNTIME_SECRET_NAME}
          env:
$(k3s_timezone_env_entries)
            - name: BOREALIS_GO_API_HOST
              value: "0.0.0.0"
            - name: BOREALIS_GO_API_PORT
              value: "${port}"
            - name: BOREALIS_API_HEALTH_HOST
              value: "127.0.0.1"
            - name: BOREALIS_API_HEALTH_PORT
              value: "${port}"
            - name: BOREALIS_INTERNAL_API_BASE_URL
              value: "http://127.0.0.1:${port}"
            - name: BOREALIS_API_BACKGROUND_LOOPS
              value: "1"
            - name: BOREALIS_K3S_API_BACKEND_BRIDGE
              value: "1"
            - name: BOREALIS_K3S_PROBE_CONFORMANCE
              value: "$(k3s_probe_conformance_status)"
            - name: BOREALIS_K3S_VERSION
              value: "$(k3s --version 2>/dev/null | awk 'NR == 1 {print $3}' || true)"
            - name: BOREALIS_K3S_UPGRADE_IMAGE
              value: "${K3S_UPGRADE_IMAGE}"
            - name: BOREALIS_ENGINE_RELEASE_VERSION
              value: "${release_version}"
            - name: BOREALIS_ENGINE_SOURCE_SHA
              value: "${source_sha}"
            - name: HOME
              value: "/tmp"
          startupProbe:
            httpGet:
              path: /startup
              port: http
            periodSeconds: 2
            timeoutSeconds: 2
            failureThreshold: 60
          readinessProbe:
            httpGet:
              path: /ready
              port: http
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 2
          livenessProbe:
            httpGet:
              path: /live
              port: http
            initialDelaySeconds: 130
            periodSeconds: 10
            timeoutSeconds: 3
            failureThreshold: 3
          lifecycle:
            preStop:
              exec:
                command: ["/bin/sh", "-c", "touch /tmp/borealis-draining; sleep 10"]
          resources:
            requests:
              cpu: 75m
              memory: 192Mi
            limits:
              cpu: ${cpu_limit}
              memory: ${memory_limit}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: tmp
              mountPath: /tmp
$(k3s_timezone_volume_mount_entries)
            - name: api-backend-root
              mountPath: /opt/Borealis/Engine/Services/api-backend
            - name: api-cache
              mountPath: /opt/Borealis/Engine/Services/api-backend/cache
            - name: api-config
              mountPath: /opt/Borealis/Engine/Services/api-backend/config
            - name: api-logs
              mountPath: /opt/Borealis/Engine/Services/api-backend/logs
            - name: api-secrets
              mountPath: /opt/Borealis/Engine/Services/api-backend/secrets
            - name: traefik-edge-config
              mountPath: /opt/Borealis/Engine/Services/traefik-edge/config
            - name: traefik-edge-env
              mountPath: /opt/Borealis/Engine/Services/traefik-edge/env
            - name: traefik-edge-logs
              mountPath: /opt/Borealis/Engine/Services/traefik-edge/logs
            - name: traefik-edge-state
              mountPath: /opt/Borealis/Engine/Services/traefik-edge/state
            - name: wireguard-config
              mountPath: /opt/Borealis/Engine/Services/wireguard-tunnel/config
            - name: wireguard-run
              mountPath: /opt/Borealis/Engine/Services/wireguard-tunnel/run
            - name: wireguard-logs
              mountPath: /opt/Borealis/Engine/Services/wireguard-tunnel/logs
            - name: wireguard-secrets
              mountPath: /opt/Borealis/Engine/Services/wireguard-tunnel/secrets
      volumes:
        - name: tmp
          emptyDir:
            medium: Memory
            sizeLimit: 128Mi
$(k3s_timezone_volume_entries)
        - name: api-backend-root
          emptyDir:
            sizeLimit: 16Mi
        - name: api-cache
          hostPath:
            path: ${RUNTIME_ROOT}/Services/api-backend/cache
            type: Directory
        - name: api-config
          hostPath:
            path: ${RUNTIME_ROOT}/Services/api-backend/config
            type: Directory
        - name: api-logs
          hostPath:
            path: ${RUNTIME_ROOT}/Services/api-backend/logs
            type: Directory
        - name: api-secrets
          hostPath:
            path: ${RUNTIME_ROOT}/Services/api-backend/secrets
            type: Directory
        - name: traefik-edge-config
          hostPath:
            path: ${RUNTIME_ROOT}/Services/traefik-edge/config
            type: Directory
        - name: traefik-edge-env
          hostPath:
            path: ${RUNTIME_ROOT}/Services/traefik-edge/env
            type: Directory
        - name: traefik-edge-logs
          hostPath:
            path: ${RUNTIME_ROOT}/Services/traefik-edge/logs
            type: Directory
        - name: traefik-edge-state
          hostPath:
            path: ${RUNTIME_ROOT}/Services/traefik-edge/state
            type: Directory
        - name: wireguard-config
          hostPath:
            path: ${RUNTIME_ROOT}/Services/wireguard-tunnel/config
            type: Directory
        - name: wireguard-run
          hostPath:
            path: ${RUNTIME_ROOT}/Services/wireguard-tunnel/run
            type: Directory
        - name: wireguard-logs
          hostPath:
            path: ${RUNTIME_ROOT}/Services/wireguard-tunnel/logs
            type: Directory
        - name: wireguard-secrets
          hostPath:
            path: ${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets
            type: Directory
EOF
}

ensure_k3s_api_backend_bridge() {
  local mode="$1"
  local image
  image="$(service_image_tag_or_previous api-backend borealis-engine/api-backend:local)"
  [[ -n "${image}" ]] || die "API backend image tag unavailable."

  local runtime_uid
  local runtime_gid
  local port
  local memory_limit
  local cpu_limit
  local runtime_env_hash
  local traffic_owner
  local service_host
  local pids_limit
  local release_version
  local source_sha
  runtime_uid="$(resolve_runtime_owner_uid)"
  runtime_gid="$(resolve_runtime_owner_gid)"
  port="$(format_k3s_tcp_port "${BOREALIS_API_BACKEND_K3S_BRIDGE_PORT}")"
  memory_limit="$(format_k3s_memory_quantity "$(read_env_value BOREALIS_API_BACKEND_MEMORY_LIMIT)")"
  cpu_limit="$(format_k3s_cpu_quantity "$(read_env_value BOREALIS_API_BACKEND_CPU_LIMIT)")"
  runtime_env_hash="$(sha256sum "${RUNTIME_ENV}" | awk '{print $1}')"
  traffic_owner="$(resolve_api_backend_traffic_owner)"
  service_host="$(api_backend_service_dns_name)"
  pids_limit="$(read_env_value BOREALIS_API_BACKEND_PIDS_LIMIT)"
  release_version="$(engine_release_version)"
  source_sha="$(git -C "${SCRIPT_DIR}" rev-parse HEAD 2>/dev/null || true)"

  local config_hash
  config_hash="$(
    printf '%s\n' \
      "schema=${K3S_API_BACKEND_BRIDGE_VERSION}" \
      "namespace=${K3S_NAMESPACE}" \
      "mode=${mode}" \
      "runtime_uid=${runtime_uid}" \
      "runtime_gid=${runtime_gid}" \
      "image=${image}" \
      "port=${port}" \
      "network_mode=cluster-ip" \
      "service=api-backend" \
      "service_host=${service_host}" \
      "listen_host=0.0.0.0" \
      "memory_limit=${memory_limit}" \
      "cpu_limit=${cpu_limit}" \
      "pids_limit=${pids_limit}" \
      "traffic_owner=${traffic_owner}" \
      "release_version=${release_version}" \
      "source_sha=${source_sha}" \
      "runtime_env_hash=${runtime_env_hash}" \
      "runtime_secret=${BOREALIS_API_BACKEND_RUNTIME_SECRET_NAME}" \
      "project_root=${ENGINE_HOST_ROOT}" \
      "log_retention_mounts=wireguard-logs-v1" \
      | sha256sum | awk '{print $1}'
  )"

  import_k3s_local_image_into_k3s "api-backend" "${image}" "k3s-api-backend"
  if k3s_manifest_config_current \
    "${K3S_API_BACKEND_CONFIG_HASH_FILE}" \
    "${config_hash}" \
    "borealis.io/bridge-config-hash" \
    "secret/${BOREALIS_API_BACKEND_RUNTIME_SECRET_NAME}" \
    "service/api-backend" \
    "deployment/api-backend"; then
    log_k3s_manifest_unchanged "k3s-api-backend" "${config_hash}"
    return 0
  fi

  log_status "k3s-api-backend" "Applying Manifests" "${C_YELLOW}"
  local manifest_file
  manifest_file="$(mktemp "${DEPLOY_DIR}/k3s-api-backend.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_k3s_api_backend_bridge_manifest \
    "${image}" \
    "${config_hash}" \
    "${runtime_uid}" \
    "${runtime_gid}" \
    "${port}" \
    "${memory_limit}" \
    "${cpu_limit}" \
    "${traffic_owner}" \
    "${release_version}" \
    "${source_sha}" \
    > "${manifest_file}"
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "k3s-api-backend" "Apply Failed" "${C_RED}"
    die "Failed to apply K3s API backend bridge manifests. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"

  log_status "k3s-api-backend" "Waiting For Rollout" "${C_YELLOW}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/api-backend" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-api-backend" "Rollout Failed" "${C_RED}"
    die "K3s API backend bridge rollout failed. See ${BUILD_LOG}."
  fi
  printf '%s  k3s-api-backend\n' "${config_hash}" > "${K3S_API_BACKEND_CONFIG_HASH_FILE}"
  log_status "k3s-api-backend" "Ready - Traffic Owner" "${C_GREEN}"
}

render_k3s_api_backend_shadow_db_validator_manifest() {
  local image="$1"
  local config_hash="$2"
  local runtime_uid="$3"
  local runtime_gid="$4"
  local memory_limit="$5"
  local cpu_limit="$6"
  local database_url="$7"
  cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${BOREALIS_API_BACKEND_SHADOW_DB_RUNTIME_SECRET_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: api-backend-shadow-db-validator
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: api-backend-shadow-db-validation
type: Opaque
data:
$(borealis_runtime_env_secret_data_with_override "${RUNTIME_ENV}" BOREALIS_DATABASE_URL "${database_url}")
---
apiVersion: batch/v1
kind: Job
metadata:
  name: ${K3S_API_BACKEND_DB_VALIDATOR_JOB_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: api-backend-shadow-db-validator
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: backend
    borealis.io/service-key: api-backend
    borealis.io/stage: api-backend-shadow-db-validation
  annotations:
    borealis.io/validation-config-hash: "${config_hash}"
    borealis.io/traffic-owner: "docker-compose"
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 300
  template:
    metadata:
      labels:
        app.kubernetes.io/name: api-backend-shadow-db-validator
        app.kubernetes.io/part-of: borealis
        app.kubernetes.io/managed-by: Engine.sh
        app.kubernetes.io/component: backend
        borealis.io/service-key: api-backend
        borealis.io/stage: api-backend-shadow-db-validation
      annotations:
        borealis.io/validation-config-hash: "${config_hash}"
        borealis.io/traffic-owner: "docker-compose"
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      enableServiceLinks: false
      securityContext:
        runAsNonRoot: true
        runAsUser: ${runtime_uid}
        runAsGroup: ${runtime_gid}
        fsGroup: ${runtime_gid}
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: api-backend-db-healthcheck
          image: ${image}
          imagePullPolicy: IfNotPresent
          command:
            - borealis-api-backend-go
            - api-db-healthcheck
          envFrom:
            - secretRef:
                name: ${BOREALIS_API_BACKEND_SHADOW_DB_RUNTIME_SECRET_NAME}
          env:
$(k3s_timezone_env_entries)
            - name: BOREALIS_API_BACKGROUND_LOOPS
              value: "0"
            - name: BOREALIS_K3S_API_BACKEND_DB_VALIDATION
              value: "1"
            - name: HOME
              value: "/tmp"
          resources:
            requests:
              cpu: 75m
              memory: 192Mi
            limits:
              cpu: ${cpu_limit}
              memory: ${memory_limit}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: tmp
              mountPath: /tmp
$(k3s_timezone_volume_mount_entries)
            - name: api-backend-root
              mountPath: /opt/Borealis/Engine/Services/api-backend
            - name: api-secrets
              mountPath: /opt/Borealis/Engine/Services/api-backend/secrets
              readOnly: true
      volumes:
        - name: tmp
          emptyDir:
            medium: Memory
            sizeLimit: 128Mi
$(k3s_timezone_volume_entries)
        - name: api-backend-root
          emptyDir:
            sizeLimit: 16Mi
        - name: api-secrets
          hostPath:
            path: ${RUNTIME_ROOT}/Services/api-backend/secrets
            type: Directory
EOF
}

validate_k3s_api_backend_shadow_db() {
  local mode="$1"
  local image=""
  local runtime_uid=""
  local runtime_gid=""
  local memory_limit=""
  local cpu_limit=""
  local runtime_env_hash=""
  local database_url=""
  local config_hash=""
  local manifest_file=""

  validate_k3s_postgres_shadow_import "${mode}"
  ensure_k3s_postgres_shadow_traffic_owner

  image="$(service_image_tag_or_previous api-backend borealis-engine/api-backend:local)"
  [[ -n "${image}" ]] || die "API backend image tag unavailable."
  runtime_uid="$(resolve_runtime_owner_uid)"
  runtime_gid="$(resolve_runtime_owner_gid)"
  memory_limit="$(format_k3s_memory_quantity "$(read_env_value BOREALIS_API_BACKEND_MEMORY_LIMIT)")"
  cpu_limit="$(format_k3s_cpu_quantity "$(read_env_value BOREALIS_API_BACKEND_CPU_LIMIT)")"
  runtime_env_hash="$(sha256sum "${RUNTIME_ENV}" | awk '{print $1}')"
  database_url="$(k3s_postgres_database_url)"

  config_hash="$(
    printf '%s\n' \
      "schema=${K3S_API_BACKEND_DB_VALIDATION_VERSION}" \
      "namespace=${K3S_NAMESPACE}" \
      "mode=${mode}" \
      "runtime_uid=${runtime_uid}" \
      "runtime_gid=${runtime_gid}" \
      "image=${image}" \
      "memory_limit=${memory_limit}" \
      "cpu_limit=${cpu_limit}" \
      "runtime_env_hash=${runtime_env_hash}" \
      "runtime_secret=${BOREALIS_API_BACKEND_SHADOW_DB_RUNTIME_SECRET_NAME}" \
      "job=${K3S_API_BACKEND_DB_VALIDATOR_JOB_NAME}" \
      "postgres_service=postgres-db.${K3S_NAMESPACE}.svc" \
      | sha256sum | awk '{print $1}'
  )"

  import_k3s_local_image_into_k3s "api-backend" "${image}" "k3s-api-backend"

  log_status "k3s-api-backend" "Preparing Shadow DB Validator" "${C_YELLOW}"
  k3s_kubectl -n "${K3S_NAMESPACE}" delete "job/${K3S_API_BACKEND_DB_VALIDATOR_JOB_NAME}" --ignore-not-found=true --wait=true >> "${BUILD_LOG}" 2>&1 || true

  manifest_file="$(mktemp "${DEPLOY_DIR}/k3s-api-backend-shadow-db-validator.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_k3s_api_backend_shadow_db_validator_manifest \
    "${image}" \
    "${config_hash}" \
    "${runtime_uid}" \
    "${runtime_gid}" \
    "${memory_limit}" \
    "${cpu_limit}" \
    "${database_url}" \
    > "${manifest_file}"
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "k3s-api-backend" "Shadow DB Validator Apply Failed" "${C_RED}"
    die "Failed to apply K3s API backend shadow DB validator Job. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"

  log_status "k3s-api-backend" "Validating Shadow DB" "${C_YELLOW}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" wait --for=condition=complete "job/${K3S_API_BACKEND_DB_VALIDATOR_JOB_NAME}" --timeout="${K3S_API_BACKEND_DB_VALIDATION_TIMEOUT}" >> "${BUILD_LOG}" 2>&1; then
    printf '[%s] K3s API backend shadow DB validator pods:\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
    k3s_kubectl -n "${K3S_NAMESPACE}" get pods -l "job-name=${K3S_API_BACKEND_DB_VALIDATOR_JOB_NAME}" -o wide >> "${BUILD_LOG}" 2>&1 || true
    printf '[%s] K3s API backend shadow DB validator logs:\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
    k3s_kubectl -n "${K3S_NAMESPACE}" logs "job/${K3S_API_BACKEND_DB_VALIDATOR_JOB_NAME}" --tail=120 >> "${BUILD_LOG}" 2>&1 || true
    log_status "k3s-api-backend" "Shadow DB Validation Failed" "${C_RED}"
    die "K3s API backend shadow DB validation failed. See ${BUILD_LOG}."
  fi

  printf '[%s] K3s API backend shadow DB validator logs:\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
  k3s_kubectl -n "${K3S_NAMESPACE}" logs "job/${K3S_API_BACKEND_DB_VALIDATOR_JOB_NAME}" --tail=120 >> "${BUILD_LOG}" 2>&1 || true
  printf '[%s] K3s API backend shadow DB validation complete; K3s API remains traffic owner and Compose PostgreSQL remains DB traffic owner.\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
  log_status "k3s-api-backend" "Ready - Shadow DB Validated" "${C_GREEN}"
}

render_k3s_job_scheduler_manifest() {
  local image="$1"
  local site_worker_image="$2"
  local config_hash="$3"
  local runtime_uid="$4"
  local runtime_gid="$5"
  local memory_limit="$6"
  local cpu_limit="$7"
  local internal_api_base="$8"
  local replicas="${BOREALIS_JOB_SCHEDULER_REPLICAS_OVERRIDE:-}"
  [[ -n "${replicas}" ]] || replicas="$(generic_k3s_workload_replicas)"
  [[ "${replicas}" =~ ^[01]$ ]] || die "BOREALIS_JOB_SCHEDULER_REPLICAS_OVERRIDE must be 0 or 1."
  cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${BOREALIS_JOB_SCHEDULER_RUNTIME_SECRET_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: job-scheduler
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: scheduler-cutover
  annotations:
    borealis.io/scheduler-config-hash: "${config_hash}"
type: Opaque
data:
$(borealis_runtime_env_secret_data "${RUNTIME_ENV}")
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: job-scheduler
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: job-scheduler
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: scheduler
    borealis.io/service-key: job-scheduler
    borealis.io/stage: scheduler-cutover
  annotations:
    borealis.io/scheduler-config-hash: "${config_hash}"
    borealis.io/network-mode: "cluster-ip"
    borealis.io/runtime-owner: "k3s"
spec:
  replicas: ${replicas}
  revisionHistoryLimit: 2
  minReadySeconds: 15
  selector:
    matchLabels:
      app.kubernetes.io/name: job-scheduler
      app.kubernetes.io/part-of: borealis
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
  template:
    metadata:
      labels:
        app.kubernetes.io/name: job-scheduler
        app.kubernetes.io/part-of: borealis
        app.kubernetes.io/managed-by: Engine.sh
        app.kubernetes.io/component: scheduler
        borealis.io/service-key: job-scheduler
        borealis.io/stage: scheduler-cutover
      annotations:
        borealis.io/scheduler-config-hash: "${config_hash}"
        borealis.io/network-mode: "cluster-ip"
        borealis.io/runtime-owner: "k3s"
        borealis.io/site-worker-image: "${site_worker_image}"
        borealis.io/pids-limit: "$(read_env_value BOREALIS_JOB_SCHEDULER_PIDS_LIMIT)"
    spec:
      automountServiceAccountToken: false
      enableServiceLinks: false
      hostNetwork: false
      dnsPolicy: ClusterFirst
      terminationGracePeriodSeconds: 60
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: borealis.io/application-state
                    operator: In
                    values: ["active"]
                  - key: borealis.io/scheduler-eligible
                    operator: In
                    values: ["true"]
      securityContext:
        runAsNonRoot: true
        runAsUser: ${runtime_uid}
        runAsGroup: ${runtime_gid}
        fsGroup: ${runtime_gid}
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: job-scheduler
          image: ${image}
          imagePullPolicy: IfNotPresent
          envFrom:
            - secretRef:
                name: ${BOREALIS_JOB_SCHEDULER_RUNTIME_SECRET_NAME}
          env:
$(k3s_timezone_env_entries)
            - name: BOREALIS_PROCESS_ROLE
              value: "job-scheduler"
            - name: BOREALIS_INTERNAL_API_BASE_URL
              value: "${internal_api_base}"
            - name: BOREALIS_SITE_WORKER_IMAGE
              value: "${site_worker_image}"
            - name: BOREALIS_SITE_WORKER_LIFECYCLE_MODE
              value: "k3s"
            - name: BOREALIS_JOB_SCHEDULER_CONTAINER_NAME
              value: "job-scheduler"
            - name: BOREALIS_JOB_SCHEDULER_RUNTIME_OWNER
              value: "k3s"
            - name: HOME
              value: "/tmp"
          startupProbe:
            exec:
              command:
                - borealis-job-scheduler-healthcheck
            periodSeconds: 2
            timeoutSeconds: 5
            failureThreshold: 90
          readinessProbe:
            exec:
              command:
                - borealis-job-scheduler-healthcheck
            periodSeconds: 5
            timeoutSeconds: 5
            failureThreshold: 2
          livenessProbe:
            exec:
              command:
                - /bin/sh
                - -c
                - "kill -0 1"
            initialDelaySeconds: 190
            periodSeconds: 10
            timeoutSeconds: 3
            failureThreshold: 3
          lifecycle:
            preStop:
              exec:
                command: ["/bin/sh", "-c", "touch /tmp/borealis-draining; sleep 10"]
          resources:
            requests:
              cpu: 50m
              memory: 128Mi
            limits:
              cpu: ${cpu_limit}
              memory: ${memory_limit}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: tmp
              mountPath: /tmp
$(k3s_timezone_volume_mount_entries)
            - name: engine-deploy
              mountPath: /opt/Borealis/Engine/Deploy
              readOnly: true
            - name: api-backend-root
              mountPath: /opt/Borealis/Engine/Services/api-backend
            - name: api-cache
              mountPath: /opt/Borealis/Engine/Services/api-backend/cache
            - name: api-config
              mountPath: /opt/Borealis/Engine/Services/api-backend/config
              readOnly: true
            - name: api-logs
              mountPath: /opt/Borealis/Engine/Services/api-backend/logs
            - name: api-secrets
              mountPath: /opt/Borealis/Engine/Services/api-backend/secrets
              readOnly: true
            - name: wireguard-run
              mountPath: /opt/Borealis/Engine/Services/wireguard-tunnel/run
            - name: traefik-config-root
              mountPath: /opt/Borealis/Engine/Services/traefik-edge/config
            - name: traefik-dynamic
              mountPath: /opt/Borealis/Engine/Services/traefik-edge/config/dynamic
      volumes:
        - name: tmp
          emptyDir:
            medium: Memory
            sizeLimit: 128Mi
$(k3s_timezone_volume_entries)
        - name: api-backend-root
          emptyDir:
            sizeLimit: 16Mi
        - name: traefik-config-root
          emptyDir:
            sizeLimit: 16Mi
        - name: engine-deploy
          hostPath:
            path: ${DEPLOY_DIR}
            type: Directory
        - name: api-cache
          hostPath:
            path: ${RUNTIME_ROOT}/Services/api-backend/cache
            type: Directory
        - name: api-config
          hostPath:
            path: ${RUNTIME_ROOT}/Services/api-backend/config
            type: Directory
        - name: api-logs
          hostPath:
            path: ${RUNTIME_ROOT}/Services/api-backend/logs
            type: Directory
        - name: api-secrets
          hostPath:
            path: ${RUNTIME_ROOT}/Services/api-backend/secrets
            type: Directory
        - name: wireguard-run
          hostPath:
            path: ${RUNTIME_ROOT}/Services/wireguard-tunnel/run
            type: Directory
        - name: traefik-dynamic
          hostPath:
            path: ${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic
            type: Directory
EOF
}

ensure_k3s_job_scheduler() {
  local mode="$1"
  local image
  local site_worker_image
  image="$(service_image_tag_or_previous job-scheduler borealis-engine/job-scheduler:local)"
  site_worker_image="$(service_image_tag_or_previous site-worker borealis-engine/site-worker:local)"
  [[ -n "${image}" ]] || die "Job scheduler image tag unavailable."
  [[ -n "${site_worker_image}" ]] || die "Site worker image tag unavailable for K3s job scheduler."

  local runtime_uid
  local runtime_gid
  local memory_limit
  local cpu_limit
  local runtime_env_hash
  local api_bridge_port
  local internal_api_base
  local pids_limit
  local replicas
  runtime_uid="$(resolve_runtime_owner_uid)"
  runtime_gid="$(resolve_runtime_owner_gid)"
  memory_limit="$(format_k3s_memory_quantity "$(read_env_value BOREALIS_JOB_SCHEDULER_MEMORY_LIMIT)")"
  cpu_limit="$(format_k3s_cpu_quantity "$(read_env_value BOREALIS_JOB_SCHEDULER_CPU_LIMIT)")"
  runtime_env_hash="$(sha256sum "${RUNTIME_ENV}" | awk '{print $1}')"
  api_bridge_port="$(format_k3s_tcp_port "${BOREALIS_API_BACKEND_K3S_BRIDGE_PORT}")"
  internal_api_base="http://$(api_backend_service_dns_name):${api_bridge_port}"
  pids_limit="$(read_env_value BOREALIS_JOB_SCHEDULER_PIDS_LIMIT)"
  replicas="${BOREALIS_JOB_SCHEDULER_REPLICAS_OVERRIDE:-}"
  [[ -n "${replicas}" ]] || replicas="$(generic_k3s_workload_replicas)"
  [[ "${replicas}" =~ ^[01]$ ]] || die "BOREALIS_JOB_SCHEDULER_REPLICAS_OVERRIDE must be 0 or 1."

  local config_hash
  config_hash="$(
    printf '%s\n' \
      "schema=${K3S_JOB_SCHEDULER_VERSION}" \
      "namespace=${K3S_NAMESPACE}" \
      "mode=${mode}" \
      "runtime_uid=${runtime_uid}" \
      "runtime_gid=${runtime_gid}" \
      "image=${image}" \
      "site_worker_image=${site_worker_image}" \
      "memory_limit=${memory_limit}" \
      "cpu_limit=${cpu_limit}" \
      "pids_limit=${pids_limit}" \
      "network_mode=cluster-ip" \
      "runtime_env_hash=${runtime_env_hash}" \
      "runtime_secret=${BOREALIS_JOB_SCHEDULER_RUNTIME_SECRET_NAME}" \
      "internal_api_base=${internal_api_base}" \
      "site_worker_lifecycle=k3s" \
      "replicas=${replicas}" \
      "project_root=${ENGINE_HOST_ROOT}" \
      "wireguard_run=${RUNTIME_ROOT}/Services/wireguard-tunnel/run" \
      | sha256sum | awk '{print $1}'
  )"

  import_k3s_local_image_into_k3s "job-scheduler" "${image}" "k3s-job-scheduler"
  if k3s_manifest_config_current \
    "${K3S_JOB_SCHEDULER_CONFIG_HASH_FILE}" \
    "${config_hash}" \
    "borealis.io/scheduler-config-hash" \
    "secret/${BOREALIS_JOB_SCHEDULER_RUNTIME_SECRET_NAME}" \
    "deployment/job-scheduler"; then
    log_k3s_manifest_unchanged "k3s-job-scheduler" "${config_hash}"
    return 0
  fi

  log_status "k3s-job-scheduler" "Applying Manifests" "${C_YELLOW}"
  local manifest_file
  manifest_file="$(mktemp "${DEPLOY_DIR}/k3s-job-scheduler.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_k3s_job_scheduler_manifest \
    "${image}" \
    "${site_worker_image}" \
    "${config_hash}" \
    "${runtime_uid}" \
    "${runtime_gid}" \
    "${memory_limit}" \
    "${cpu_limit}" \
    "${internal_api_base}" \
    > "${manifest_file}"
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "k3s-job-scheduler" "Apply Failed" "${C_RED}"
    die "Failed to apply K3s job scheduler manifests. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"

  log_status "k3s-job-scheduler" "Waiting For Rollout" "${C_YELLOW}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/job-scheduler" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-job-scheduler" "Rollout Failed" "${C_RED}"
    die "K3s job scheduler rollout failed. See ${BUILD_LOG}."
  fi
  printf '%s  k3s-job-scheduler\n' "${config_hash}" > "${K3S_JOB_SCHEDULER_CONFIG_HASH_FILE}"
  log_status "k3s-job-scheduler" "Ready - Traffic Owner" "${C_GREEN}"
}

render_k3s_postgres_statefulset_manifest() {
  local image="$1"
  local config_hash="$2"
  local postgres_uid="$3"
  local postgres_gid="$4"
  local memory_limit="$5"
  local cpu_limit="$6"
  local storage_class="$7"
  local storage_size="$8"
  local traffic_owner="$9"
  local runtime_owner
  runtime_owner="$(postgres_runtime_owner_label "${traffic_owner}")"
  local postgres_pgdata="/var/lib/postgresql/data/pgdata"
  local replicas
  replicas="$(generic_k3s_workload_replicas)"
  cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${BOREALIS_POSTGRES_RUNTIME_SECRET_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: postgres-db
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: postgres-cutover
    borealis.io/runtime-owner: ${runtime_owner}
  annotations:
    borealis.io/postgres-config-hash: "${config_hash}"
type: Opaque
data:
$(borealis_postgres_runtime_secret_data)
---
apiVersion: v1
kind: Service
metadata:
  name: postgres-db-headless
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: postgres-db
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: postgres-cutover
    borealis.io/runtime-owner: ${runtime_owner}
  annotations:
    borealis.io/postgres-config-hash: "${config_hash}"
spec:
  clusterIP: None
  selector:
    app.kubernetes.io/name: postgres-db
    app.kubernetes.io/part-of: borealis
  ports:
    - name: postgres
      port: 5432
      targetPort: postgres
      protocol: TCP
---
apiVersion: v1
kind: Service
metadata:
  name: postgres-db
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: postgres-db
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: postgres-cutover
    borealis.io/runtime-owner: ${runtime_owner}
  annotations:
    borealis.io/postgres-config-hash: "${config_hash}"
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: postgres-db
    app.kubernetes.io/part-of: borealis
  ports:
    - name: postgres
      port: 5432
      targetPort: postgres
      protocol: TCP
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres-db
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: postgres-db
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: database
    borealis.io/service-key: postgres-db
    borealis.io/stage: postgres-cutover
    borealis.io/runtime-owner: ${runtime_owner}
  annotations:
    borealis.io/postgres-config-hash: "${config_hash}"
    borealis.io/storage-class: "${storage_class}"
    borealis.io/storage-size: "${storage_size}"
    borealis.io/traffic-owner: "${traffic_owner}"
spec:
  serviceName: postgres-db-headless
  replicas: ${replicas}
  revisionHistoryLimit: 2
  selector:
    matchLabels:
      app.kubernetes.io/name: postgres-db
      app.kubernetes.io/part-of: borealis
  template:
    metadata:
      labels:
        app.kubernetes.io/name: postgres-db
        app.kubernetes.io/part-of: borealis
        app.kubernetes.io/managed-by: Engine.sh
        app.kubernetes.io/component: database
        borealis.io/service-key: postgres-db
        borealis.io/stage: postgres-cutover
        borealis.io/runtime-owner: ${runtime_owner}
      annotations:
        borealis.io/postgres-config-hash: "${config_hash}"
        borealis.io/storage-class: "${storage_class}"
        borealis.io/storage-size: "${storage_size}"
        borealis.io/traffic-owner: "${traffic_owner}"
        borealis.io/pids-limit: "$(read_env_value BOREALIS_POSTGRES_DB_PIDS_LIMIT)"
    spec:
      automountServiceAccountToken: false
      enableServiceLinks: false
      securityContext:
        fsGroup: ${postgres_gid}
        fsGroupChangePolicy: OnRootMismatch
        seccompProfile:
          type: RuntimeDefault
      initContainers:
        - name: postgres-data-permissions
          image: ${image}
          imagePullPolicy: IfNotPresent
          command:
            - sh
            - -c
            - |
              set -e
              install -d -m 0755 -o 0 -g 0 ${postgres_pgdata}
              chown -R ${postgres_uid}:${postgres_gid} ${postgres_pgdata}
              chmod 0700 ${postgres_pgdata}
          securityContext:
            runAsUser: 0
            runAsGroup: 0
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
              add: ["CHOWN", "DAC_OVERRIDE", "FOWNER"]
          volumeMounts:
            - name: postgres-data
              mountPath: /var/lib/postgresql/data
      containers:
        - name: postgres-db
          image: ${image}
          imagePullPolicy: IfNotPresent
          args:
            - postgres
            - -c
            - listen_addresses=*
            - -c
            - port=5432
            - -c
            - max_connections=$(read_env_value BOREALIS_POSTGRES_MAX_CONNECTIONS)
            - -c
            - shared_buffers=$(read_env_value BOREALIS_POSTGRES_SHARED_BUFFERS)
            - -c
            - effective_cache_size=$(read_env_value BOREALIS_POSTGRES_EFFECTIVE_CACHE_SIZE)
            - -c
            - work_mem=$(read_env_value BOREALIS_POSTGRES_WORK_MEM)
            - -c
            - maintenance_work_mem=$(read_env_value BOREALIS_POSTGRES_MAINTENANCE_WORK_MEM)
            - -c
            - max_worker_processes=$(read_env_value BOREALIS_POSTGRES_MAX_WORKER_PROCESSES)
            - -c
            - max_parallel_workers=$(read_env_value BOREALIS_POSTGRES_MAX_PARALLEL_WORKERS)
            - -c
            - max_parallel_workers_per_gather=$(read_env_value BOREALIS_POSTGRES_MAX_PARALLEL_WORKERS_PER_GATHER)
            - -c
            - autovacuum_max_workers=$(read_env_value BOREALIS_POSTGRES_AUTOVACUUM_MAX_WORKERS)
            - -c
            - autovacuum_vacuum_cost_limit=$(read_env_value BOREALIS_POSTGRES_AUTOVACUUM_VACUUM_COST_LIMIT)
            - -c
            - autovacuum_naptime=$(read_env_value BOREALIS_POSTGRES_AUTOVACUUM_NAPTIME)
            - -c
            - autovacuum_vacuum_scale_factor=$(read_env_value BOREALIS_POSTGRES_AUTOVACUUM_VACUUM_SCALE_FACTOR)
            - -c
            - autovacuum_analyze_scale_factor=$(read_env_value BOREALIS_POSTGRES_AUTOVACUUM_ANALYZE_SCALE_FACTOR)
            - -c
            - max_wal_size=$(read_env_value BOREALIS_POSTGRES_MAX_WAL_SIZE)
            - -c
            - min_wal_size=$(read_env_value BOREALIS_POSTGRES_MIN_WAL_SIZE)
            - -c
            - effective_io_concurrency=$(read_env_value BOREALIS_POSTGRES_EFFECTIVE_IO_CONCURRENCY)
            - -c
            - wal_compression=$(read_env_value BOREALIS_POSTGRES_WAL_COMPRESSION)
            - -c
            - checkpoint_timeout=$(read_env_value BOREALIS_POSTGRES_CHECKPOINT_TIMEOUT)
            - -c
            - checkpoint_completion_target=$(read_env_value BOREALIS_POSTGRES_CHECKPOINT_COMPLETION_TARGET)
            - -c
            - random_page_cost=$(read_env_value BOREALIS_POSTGRES_RANDOM_PAGE_COST)
          ports:
            - name: postgres
              containerPort: 5432
              protocol: TCP
          envFrom:
            - secretRef:
                name: ${BOREALIS_POSTGRES_RUNTIME_SECRET_NAME}
          env:
$(k3s_timezone_env_entries)
            - name: PGDATA
              value: ${postgres_pgdata}
            - name: HOME
              value: /tmp
          livenessProbe:
            exec:
              command:
                - sh
                - -c
                - pg_isready -h 127.0.0.1 -p 5432 -U "\${POSTGRES_USER}" -d "\${POSTGRES_DB}"
            initialDelaySeconds: 30
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 6
          readinessProbe:
            exec:
              command:
                - sh
                - -c
                - pg_isready -h 127.0.0.1 -p 5432 -U "\${POSTGRES_USER}" -d "\${POSTGRES_DB}"
            initialDelaySeconds: 5
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 12
          resources:
            requests:
              cpu: 100m
              memory: 256Mi
            limits:
              cpu: ${cpu_limit}
              memory: ${memory_limit}
          securityContext:
            runAsNonRoot: true
            runAsUser: ${postgres_uid}
            runAsGroup: ${postgres_gid}
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: postgres-data
              mountPath: /var/lib/postgresql/data
$(k3s_timezone_volume_mount_entries)
            - name: postgres-run
              mountPath: /var/run/postgresql
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: postgres-run
          emptyDir:
            medium: Memory
            sizeLimit: 32Mi
        - name: tmp
          emptyDir:
            medium: Memory
            sizeLimit: 128Mi
$(k3s_timezone_volume_entries)
  volumeClaimTemplates:
    - metadata:
        name: postgres-data
        labels:
          app.kubernetes.io/name: postgres-db
          app.kubernetes.io/part-of: borealis
          app.kubernetes.io/managed-by: Engine.sh
          borealis.io/stage: postgres-cutover
      spec:
        accessModes: ["ReadWriteOnce"]
        storageClassName: ${storage_class}
        resources:
          requests:
            storage: ${storage_size}
EOF
}

wait_for_k3s_postgres_pvc() {
  local pvc_name="postgres-data-postgres-db-0"
  local attempt
  local phase=""
  for attempt in {1..90}; do
    phase="$(k3s_kubectl -n "${K3S_NAMESPACE}" get pvc "${pvc_name}" -o jsonpath='{.status.phase}' 2>>"${BUILD_LOG}" || true)"
    if [[ "${phase}" == "Bound" ]]; then
      return 0
    fi
    sleep 2
  done
  log_status "k3s-postgres-db" "PVC Not Bound" "${C_RED}"
  die "K3s PostgreSQL PVC ${pvc_name} did not become Bound. See ${BUILD_LOG}."
}

ensure_k3s_postgres_statefulset() {
  local mode="$1"
  local traffic_owner="${2:-}"
  traffic_owner="$(normalize_postgres_traffic_owner "${traffic_owner:-$(resolve_postgres_traffic_owner)}")"
  local existing_cnpg_database_url=""
  existing_cnpg_database_url="$(runtime_cnpg_database_url || true)"
  if [[ -n "${existing_cnpg_database_url}" ]]; then
    verify_cnpg_cutover_runtime "${existing_cnpg_database_url}"
    local cnpg_config_hash=""
    cnpg_config_hash="$(printf '%s\n' \
      "schema=${K3S_POSTGRES_VERSION}" \
      "namespace=${K3S_NAMESPACE}" \
      "owner=cloudnativepg" \
      "runtime_env_hash=$(sha256sum "${RUNTIME_ENV}" | awk '{print $1}')" \
      | sha256sum | awk '{print $1}')"
    printf '%s  k3s-postgres-db-cnpg-owner\n' "${cnpg_config_hash}" > "${K3S_POSTGRES_CONFIG_HASH_FILE}"
    log_status "k3s-postgres-db" "Skipped - CNPG Owner" "${C_DIM}"
    return 0
  fi
  validate_k3s_postgres_settings

  local storage_class
  storage_class="$(resolve_k3s_postgres_storage_class)"
  [[ "${storage_class}" =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]] \
    || die "Resolved K3s PostgreSQL StorageClass ${storage_class} is not a valid Kubernetes StorageClass name."

  local config_hash
  config_hash="$(
    printf '%s\n' \
      "schema=${K3S_POSTGRES_VERSION}" \
      "namespace=${K3S_NAMESPACE}" \
      "enabled=$(normalize_enabled_flag "BOREALIS_K3S_POSTGRES_ENABLED" "${K3S_POSTGRES_ENABLED}")" \
      "traffic_owner=${traffic_owner}" \
      "mode=${mode}" \
      "storage_class=${storage_class}" \
      "storage_size=${K3S_POSTGRES_STORAGE_SIZE}" \
      "rollout_timeout=${K3S_POSTGRES_ROLLOUT_TIMEOUT}" \
      "runtime_secret=${BOREALIS_POSTGRES_RUNTIME_SECRET_NAME}" \
      "runtime_env_hash=$(sha256sum "${RUNTIME_ENV}" | awk '{print $1}')" \
      | sha256sum | awk '{print $1}'
  )"

  if ! k3s_postgres_enabled; then
    printf '%s  k3s-postgres-db disabled\n' "${config_hash}" > "${K3S_POSTGRES_CONFIG_HASH_FILE}"
    if k3s_kubectl -n "${K3S_NAMESPACE}" get statefulset postgres-db >/dev/null 2>&1; then
      log_status "k3s-postgres-db" "Skipped - Preserved" "${C_DIM}"
    else
      log_status "k3s-postgres-db" "Skipped - Compose Owner" "${C_DIM}"
    fi
    return 0
  fi

  if ! k3s_kubectl get storageclass "${storage_class}" >/dev/null 2>>"${BUILD_LOG}"; then
    log_status "k3s-postgres-db" "StorageClass Missing" "${C_RED}"
    die "K3s PostgreSQL requires StorageClass ${storage_class}. See ${BUILD_LOG}."
  fi

  local image
  local postgres_uid
  local postgres_gid
  local memory_limit
  local cpu_limit
  local pids_limit
  image="$(service_image_tag_or_previous postgres-db borealis-engine/postgres-db:local)"
  [[ -n "${image}" ]] || die "PostgreSQL image tag unavailable."
  postgres_uid="$(resolve_postgres_runtime_uid)"
  postgres_gid="$(resolve_postgres_runtime_gid)"
  memory_limit="$(format_k3s_memory_quantity "$(read_env_value BOREALIS_POSTGRES_DB_MEMORY_LIMIT)")"
  cpu_limit="$(format_k3s_cpu_quantity "$(read_env_value BOREALIS_POSTGRES_DB_CPU_LIMIT)")"
  pids_limit="$(read_env_value BOREALIS_POSTGRES_DB_PIDS_LIMIT)"

  config_hash="$(
    printf '%s\n' \
      "schema=${K3S_POSTGRES_VERSION}" \
      "namespace=${K3S_NAMESPACE}" \
      "enabled=1" \
      "traffic_owner=${traffic_owner}" \
      "mode=${mode}" \
      "image=${image}" \
      "postgres_uid=${postgres_uid}" \
      "postgres_gid=${postgres_gid}" \
      "memory_limit=${memory_limit}" \
      "cpu_limit=${cpu_limit}" \
      "pids_limit=${pids_limit}" \
      "storage_class=${storage_class}" \
      "storage_size=${K3S_POSTGRES_STORAGE_SIZE}" \
      "rollout_timeout=${K3S_POSTGRES_ROLLOUT_TIMEOUT}" \
      "runtime_secret=${BOREALIS_POSTGRES_RUNTIME_SECRET_NAME}" \
      "runtime_env_hash=$(sha256sum "${RUNTIME_ENV}" | awk '{print $1}')" \
      | sha256sum | awk '{print $1}'
  )"

  import_k3s_local_image_into_k3s "postgres-db" "${image}" "k3s-postgres-db"
  if k3s_manifest_config_current \
    "${K3S_POSTGRES_CONFIG_HASH_FILE}" \
    "${config_hash}" \
    "borealis.io/postgres-config-hash" \
    "secret/${BOREALIS_POSTGRES_RUNTIME_SECRET_NAME}" \
    "service/postgres-db-headless" \
    "service/postgres-db" \
    "statefulset/postgres-db"; then
    log_k3s_manifest_unchanged "k3s-postgres-db" "${config_hash}"
    return 0
  fi

  log_status "k3s-postgres-db" "Applying Manifests" "${C_YELLOW}"
  local manifest_file
  manifest_file="$(mktemp "${DEPLOY_DIR}/k3s-postgres-db.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_k3s_postgres_statefulset_manifest \
    "${image}" \
    "${config_hash}" \
    "${postgres_uid}" \
    "${postgres_gid}" \
    "${memory_limit}" \
    "${cpu_limit}" \
    "${storage_class}" \
    "${K3S_POSTGRES_STORAGE_SIZE}" \
    "${traffic_owner}" \
    > "${manifest_file}"
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "k3s-postgres-db" "Apply Failed" "${C_RED}"
    die "Failed to apply K3s PostgreSQL manifests. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"

  log_status "k3s-postgres-db" "Waiting For PVC" "${C_YELLOW}"
  wait_for_k3s_postgres_pvc

  log_status "k3s-postgres-db" "Waiting For Rollout" "${C_YELLOW}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "statefulset/postgres-db" --timeout="${K3S_POSTGRES_ROLLOUT_TIMEOUT}" >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-postgres-db" "Rollout Failed" "${C_RED}"
    die "K3s PostgreSQL rollout failed. See ${BUILD_LOG}."
  fi
  printf '%s  k3s-postgres-db\n' "${config_hash}" > "${K3S_POSTGRES_CONFIG_HASH_FILE}"
  if [[ "${traffic_owner}" == "k3s" ]]; then
    log_status "k3s-postgres-db" "Ready - Traffic Owner" "${C_GREEN}"
  else
    log_status "k3s-postgres-db" "Ready - Shadow" "${C_GREEN}"
  fi
}

postgres_shadow_import_summary_sql() {
  cat <<'SQL'
SELECT 'assemblies.tables=' || COUNT(*)
FROM information_schema.tables
WHERE table_schema = 'assemblies' AND table_type = 'BASE TABLE'
UNION ALL
SELECT 'engine.tables=' || COUNT(*)
FROM information_schema.tables
WHERE table_schema = 'engine' AND table_type = 'BASE TABLE'
UNION ALL
SELECT 'engine.sites=' || COUNT(*) FROM engine.sites
UNION ALL
SELECT 'engine.devices=' || COUNT(*) FROM engine.devices
UNION ALL
SELECT 'engine.users=' || COUNT(*) FROM engine.users
ORDER BY 1;
SQL
}

compose_postgres_psql() {
  local sql="$1"
  docker exec borealis-engine-postgres-db sh -lc \
    'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "$1"' \
    sh "${sql}"
}

k3s_postgres_psql() {
  local sql="$1"
  k3s_kubectl -n "${K3S_NAMESPACE}" exec -i postgres-db-0 -c postgres-db -- sh -lc \
    'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "$1"' \
    sh "${sql}"
}

postgres_shadow_import_summary() {
  local target="$1"
  local sql
  sql="$(postgres_shadow_import_summary_sql)"
  case "${target}" in
    compose)
      compose_postgres_psql "${sql}"
      ;;
    k3s)
      k3s_postgres_psql "${sql}"
      ;;
    *)
      die "Unknown PostgreSQL summary target '${target}'."
      ;;
  esac
}

current_k3s_postgres_traffic_owner() {
  k3s_cluster_installed || return 0
  [[ -s "${K3S_KUBECONFIG}" ]] || return 0
  k3s_kubectl -n "${K3S_NAMESPACE}" get statefulset postgres-db \
    -o jsonpath='{.metadata.annotations.borealis\.io/traffic-owner}' 2>/dev/null || true
}

ensure_k3s_postgres_shadow_traffic_owner() {
  local owner=""
  owner="$(current_k3s_postgres_traffic_owner)"
  if [[ "${owner}" != "docker-compose" ]]; then
    die "Refusing K3s PostgreSQL shadow import because StatefulSet traffic owner is '${owner:-unknown}', not docker-compose."
  fi
}

ensure_compose_postgres_container_for_cutover() {
  local container="borealis-engine-postgres-db"
  local state_dir="${RUNTIME_ROOT}/Services/postgres-db/state"
  if ! docker inspect "${container}" >/dev/null 2>&1; then
    if [[ -e "${state_dir}/PG_VERSION" ]]; then
      die "Compose PostgreSQL state exists at ${state_dir}, but ${container} is missing. Start the previous Compose PostgreSQL container or restore from backup before DB cutover."
    fi
    printf '[%s] No Compose PostgreSQL container/state found; treating K3s PostgreSQL as fresh install.\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
    return 1
  fi
  if ! container_running "${container}"; then
    log_status "postgres-db" "Starting Compose Snapshot Source" "${C_YELLOW}"
    docker start "${container}" >> "${BUILD_LOG}" 2>&1 \
      || die "Failed to start ${container} for PostgreSQL cutover snapshot. See ${BUILD_LOG}."
  fi
  if ! wait_for_postgres_container 150; then
    log_status "postgres-db" "Failed" "${C_RED}"
    die "Compose PostgreSQL did not become healthy for cutover snapshot. See ${BUILD_LOG}."
  fi
  refresh_legacy_postgres_container_status
  return 0
}

quiesce_k3s_postgres_cutover_writers() {
  K3S_POSTGRES_CUTOVER_SITE_WORKER_COUNT="$(k3s_site_worker_pod_count)"
  log_status "k3s-postgres-db" "Quiescing DB Writers" "${C_YELLOW}"
  printf '[%s] Quiescing K3s API backend, job scheduler, and %s site-worker pod(s) for PostgreSQL cutover snapshot.\n' "$(date +%FT%T)" "${K3S_POSTGRES_CUTOVER_SITE_WORKER_COUNT}" >> "${BUILD_LOG}"

  if k3s_kubectl -n "${K3S_NAMESPACE}" get deployment api-backend >/dev/null 2>&1; then
    k3s_kubectl -n "${K3S_NAMESPACE}" scale deployment/api-backend --replicas=0 >> "${BUILD_LOG}" 2>&1 \
      || die "Failed to scale K3s API backend down for PostgreSQL cutover. See ${BUILD_LOG}."
  fi
  if k3s_kubectl -n "${K3S_NAMESPACE}" get deployment job-scheduler >/dev/null 2>&1; then
    k3s_kubectl -n "${K3S_NAMESPACE}" scale deployment/job-scheduler --replicas=0 >> "${BUILD_LOG}" 2>&1 \
      || die "Failed to scale K3s job scheduler down for PostgreSQL cutover. See ${BUILD_LOG}."
  fi
  k3s_kubectl -n "${K3S_NAMESPACE}" wait --for=delete pod \
    -l 'app.kubernetes.io/name in (api-backend,job-scheduler)' \
    --timeout=120s >> "${BUILD_LOG}" 2>&1 || true

  if ((K3S_POSTGRES_CUTOVER_SITE_WORKER_COUNT > 0)); then
    k3s_kubectl -n "${K3S_NAMESPACE}" delete pods \
      -l 'app.kubernetes.io/name=site-worker,app.kubernetes.io/managed-by=borealis-operator' \
      --wait=true --timeout=120s >> "${BUILD_LOG}" 2>&1 \
      || die "Failed to quiesce K3s site workers for PostgreSQL cutover. See ${BUILD_LOG}."
  fi
}

wait_for_k3s_postgres_cutover_workers() {
  ((K3S_POSTGRES_CUTOVER_SITE_WORKER_COUNT > 0)) || return 0
  log_status "site-worker" "Waiting For K3s Workers" "${C_YELLOW}"
  wait_for_k3s_site_worker_ready_count "${K3S_POSTGRES_CUTOVER_SITE_WORKER_COUNT}" 300
  log_status "site-worker" "Ready - K3s DB" "${C_GREEN}"
}

import_compose_postgres_into_k3s_for_cutover() {
  local source_summary=""
  local target_summary=""

  ensure_compose_postgres_container_for_cutover || return 0
  quiesce_k3s_postgres_cutover_writers

  log_status "k3s-postgres-db" "Snapshotting Compose DB" "${C_YELLOW}"
  source_summary="$(postgres_shadow_import_summary compose)"
  printf '[%s] K3s PostgreSQL cutover source summary:\n%s\n' "$(date +%FT%T)" "${source_summary}" >> "${BUILD_LOG}"

  log_status "k3s-postgres-db" "Resetting K3s Schemas" "${C_YELLOW}"
  k3s_postgres_psql 'DROP SCHEMA IF EXISTS engine CASCADE; DROP SCHEMA IF EXISTS assemblies CASCADE;' >> "${BUILD_LOG}" 2>&1

  log_status "k3s-postgres-db" "Importing Cutover Data" "${C_YELLOW}"
  if ! {
    docker exec borealis-engine-postgres-db sh -lc \
      'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner --no-acl --schema=engine --schema=assemblies' \
      | k3s_kubectl -n "${K3S_NAMESPACE}" exec -i postgres-db-0 -c postgres-db -- sh -lc \
        'pg_restore --exit-on-error --no-owner --no-acl -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
  } >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-postgres-db" "Cutover Import Failed" "${C_RED}"
    die "K3s PostgreSQL cutover import failed. K3s API/scheduler remain quiesced so rerunning deploy can retry safely. See ${BUILD_LOG}."
  fi

  log_status "k3s-postgres-db" "Verifying Cutover Data" "${C_YELLOW}"
  target_summary="$(postgres_shadow_import_summary k3s)"
  printf '[%s] K3s PostgreSQL cutover target summary:\n%s\n' "$(date +%FT%T)" "${target_summary}" >> "${BUILD_LOG}"
  if [[ "${source_summary}" != "${target_summary}" ]]; then
    printf '[%s] K3s PostgreSQL cutover summary mismatch. Source:\n%s\nTarget:\n%s\n' "$(date +%FT%T)" "${source_summary}" "${target_summary}" >> "${BUILD_LOG}"
    log_status "k3s-postgres-db" "Cutover Import Mismatch" "${C_RED}"
    die "K3s PostgreSQL cutover import completed but row summaries differ. See ${BUILD_LOG}."
  fi

  log_status "k3s-postgres-db" "Cutover Data Imported" "${C_GREEN}"
  printf '[%s] K3s PostgreSQL cutover import complete; API and scheduler will restart against K3s PostgreSQL.\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
}

render_k3s_postgres_schema_job_manifest() {
  local image="$1"
  local config_hash="$2"
  local runtime_uid="$3"
  local runtime_gid="$4"
  cat <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: ${K3S_POSTGRES_SCHEMA_JOB_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: postgres-db-schema
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: database
    borealis.io/service-key: postgres-db
    borealis.io/stage: postgres-cutover
  annotations:
    borealis.io/schema-config-hash: "${config_hash}"
    borealis.io/traffic-owner: "k3s"
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 300
  template:
    metadata:
      labels:
        app.kubernetes.io/name: postgres-db-schema
        app.kubernetes.io/part-of: borealis
        app.kubernetes.io/managed-by: Engine.sh
        app.kubernetes.io/component: database
        borealis.io/service-key: postgres-db
        borealis.io/stage: postgres-cutover
      annotations:
        borealis.io/schema-config-hash: "${config_hash}"
        borealis.io/traffic-owner: "k3s"
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      enableServiceLinks: false
      securityContext:
        runAsNonRoot: true
        runAsUser: ${runtime_uid}
        runAsGroup: ${runtime_gid}
        fsGroup: ${runtime_gid}
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: postgres-db-schema
          image: ${image}
          imagePullPolicy: IfNotPresent
          command:
            - python
            - -u
            - -c
            - |
              import os
              from Data.Engine.database import initialise_engine_database
              initialise_engine_database(os.environ["BOREALIS_DATABASE_URL"], progress_callback=lambda table_name: print("BOREALIS_SCHEMA_PROGRESS\t" + str(table_name), flush=True))
          envFrom:
            - secretRef:
                name: ${BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME}
          env:
$(k3s_timezone_env_entries)
            - name: PYTHONPATH
              value: "/opt/Borealis:/opt/Borealis/Data/Engine:/opt/Borealis/Data/Agent"
            - name: HOME
              value: "/tmp"
          resources:
            requests:
              cpu: 75m
              memory: 192Mi
            limits:
              cpu: 1000m
              memory: 512Mi
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: tmp
              mountPath: /tmp
$(k3s_timezone_volume_mount_entries)
            - name: api-backend-root
              mountPath: /opt/Borealis/Engine/Services/api-backend
      volumes:
        - name: tmp
          emptyDir:
            medium: Memory
            sizeLimit: 128Mi
$(k3s_timezone_volume_entries)
        - name: api-backend-root
          emptyDir:
            sizeLimit: 16Mi
EOF
}

ensure_k3s_engine_database_schema() {
  local mode="$1"
  local image=""
  local runtime_uid=""
  local runtime_gid=""
  local config_hash=""
  local manifest_file=""

  image="$(service_image_tag_or_previous site-worker borealis-engine/site-worker:local)"
  [[ -n "${image}" ]] || die "Site worker image tag unavailable for K3s database schema initialization."
  runtime_uid="$(resolve_runtime_owner_uid)"
  runtime_gid="$(resolve_runtime_owner_gid)"
  config_hash="$(
    printf '%s\n' \
      "schema=${K3S_POSTGRES_VERSION}" \
      "namespace=${K3S_NAMESPACE}" \
      "mode=${mode}" \
      "image=${image}" \
      "runtime_uid=${runtime_uid}" \
      "runtime_gid=${runtime_gid}" \
      "runtime_secret=${BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME}" \
      "runtime_env_hash=$(sha256sum "${RUNTIME_ENV}" | awk '{print $1}')" \
      "job=${K3S_POSTGRES_SCHEMA_JOB_NAME}" \
      "postgres_service=postgres-db.${K3S_NAMESPACE}.svc" \
      | sha256sum | awk '{print $1}'
  )"

  local postgres_config_hash
  postgres_config_hash="$(stored_k3s_config_hash "${K3S_POSTGRES_CONFIG_HASH_FILE}")"
  if [[ "$(stored_k3s_config_hash "${K3S_POSTGRES_SCHEMA_CONFIG_HASH_FILE}")" == "${config_hash}" ]] \
    && [[ -n "${postgres_config_hash}" ]] \
    && k3s_resource_annotation_matches "statefulset/postgres-db" "borealis.io/postgres-config-hash" "${postgres_config_hash}"; then
    log_k3s_manifest_unchanged "Database schema" "${config_hash}"
    return 0
  fi

  import_k3s_local_image_into_k3s "site-worker" "${image}" "site-worker"
  log_status "Database schema" "Preparing K3s Engine Tables" "${C_YELLOW}"
  k3s_kubectl -n "${K3S_NAMESPACE}" delete "job/${K3S_POSTGRES_SCHEMA_JOB_NAME}" --ignore-not-found=true --wait=true >> "${BUILD_LOG}" 2>&1 || true

  manifest_file="$(mktemp "${DEPLOY_DIR}/k3s-postgres-schema.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_k3s_postgres_schema_job_manifest \
    "${image}" \
    "${config_hash}" \
    "${runtime_uid}" \
    "${runtime_gid}" \
    > "${manifest_file}"
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "Database schema" "K3s Job Apply Failed" "${C_RED}"
    die "Failed to apply K3s PostgreSQL schema initializer Job. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"

  log_status "Database schema" "Ensuring K3s Engine Tables" "${C_YELLOW}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" wait --for=condition=complete "job/${K3S_POSTGRES_SCHEMA_JOB_NAME}" --timeout="${K3S_POSTGRES_SCHEMA_TIMEOUT}" >> "${BUILD_LOG}" 2>&1; then
    printf '[%s] K3s PostgreSQL schema initializer pods:\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
    k3s_kubectl -n "${K3S_NAMESPACE}" get pods -l "job-name=${K3S_POSTGRES_SCHEMA_JOB_NAME}" -o wide >> "${BUILD_LOG}" 2>&1 || true
    printf '[%s] K3s PostgreSQL schema initializer logs:\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
    k3s_kubectl -n "${K3S_NAMESPACE}" logs "job/${K3S_POSTGRES_SCHEMA_JOB_NAME}" --tail=200 >> "${BUILD_LOG}" 2>&1 || true
    log_status "Database schema" "K3s Job Failed" "${C_RED}"
    die "K3s PostgreSQL schema initialization failed. See ${BUILD_LOG}."
  fi
  k3s_kubectl -n "${K3S_NAMESPACE}" logs "job/${K3S_POSTGRES_SCHEMA_JOB_NAME}" --tail=240 2>/dev/null | stream_database_schema_output || true
  printf '%s  k3s-postgres-schema\n' "${config_hash}" > "${K3S_POSTGRES_SCHEMA_CONFIG_HASH_FILE}"
  log_status "Database schema" "Ready - K3s DB" "${C_GREEN}"
}

validate_k3s_postgres_shadow_import() {
  local mode="$1"
  local source_summary=""
  local target_summary=""
  local current_owner=""

  ensure_k3s_cluster_baseline
  ensure_longhorn_storage_baseline
  current_owner="$(current_k3s_postgres_traffic_owner)"
  if [[ -n "${current_owner}" && "${current_owner}" != "docker-compose" ]]; then
    die "Refusing K3s PostgreSQL shadow import because StatefulSet traffic owner is '${current_owner}', not docker-compose."
  fi
  ensure_k3s_postgres_statefulset "${mode}" "docker-compose"
  ensure_k3s_postgres_shadow_traffic_owner

  ensure_compose_postgres_container_for_cutover \
    || die "Compose PostgreSQL source is unavailable for K3s shadow import validation."

  log_status "k3s-postgres-db" "Snapshotting Compose DB" "${C_YELLOW}"
  source_summary="$(postgres_shadow_import_summary compose)"
  printf '[%s] K3s PostgreSQL shadow import source summary:\n%s\n' "$(date +%FT%T)" "${source_summary}" >> "${BUILD_LOG}"

  log_status "k3s-postgres-db" "Resetting Shadow Schemas" "${C_YELLOW}"
  k3s_postgres_psql 'DROP SCHEMA IF EXISTS engine CASCADE; DROP SCHEMA IF EXISTS assemblies CASCADE;' >> "${BUILD_LOG}" 2>&1

  log_status "k3s-postgres-db" "Importing Shadow Data" "${C_YELLOW}"
  if ! {
    docker exec borealis-engine-postgres-db sh -lc \
      'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner --no-acl --schema=engine --schema=assemblies' \
      | k3s_kubectl -n "${K3S_NAMESPACE}" exec -i postgres-db-0 -c postgres-db -- sh -lc \
        'pg_restore --exit-on-error --no-owner --no-acl -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
  } >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-postgres-db" "Shadow Import Failed" "${C_RED}"
    die "K3s PostgreSQL shadow import failed. See ${BUILD_LOG}."
  fi

  log_status "k3s-postgres-db" "Verifying Shadow Import" "${C_YELLOW}"
  target_summary="$(postgres_shadow_import_summary k3s)"
  printf '[%s] K3s PostgreSQL shadow import target summary:\n%s\n' "$(date +%FT%T)" "${target_summary}" >> "${BUILD_LOG}"
  if [[ "${source_summary}" != "${target_summary}" ]]; then
    printf '[%s] K3s PostgreSQL shadow import summary mismatch. Source:\n%s\nTarget:\n%s\n' "$(date +%FT%T)" "${source_summary}" "${target_summary}" >> "${BUILD_LOG}"
    log_status "k3s-postgres-db" "Shadow Import Mismatch" "${C_RED}"
    die "K3s PostgreSQL shadow import completed but row summaries differ. See ${BUILD_LOG}."
  fi

  log_status "k3s-postgres-db" "Ready - Shadow Import Validated" "${C_GREEN}"
  printf '[%s] K3s PostgreSQL shadow import validation complete; Compose PostgreSQL remains traffic owner.\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
}

render_k3s_webui_frontend_bridge_manifest() {
  local image="$1"
  local mode="$2"
  local config_hash="$3"
  local runtime_uid="$4"
  local runtime_gid="$5"
  local port="$6"
  local memory_limit="$7"
  local cpu_limit="$8"
  local traffic_owner="$9"
  local workload_stage="workload-bridge"
  local replicas
  if [[ "${traffic_owner}" == "k3s" ]]; then
    workload_stage="workload-cutover"
  fi
  replicas="$(generic_k3s_workload_replicas)"
  cat <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: webui-frontend
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: webui-frontend
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: frontend
    borealis.io/service-key: webui-frontend
    borealis.io/stage: ${workload_stage}
  annotations:
    borealis.io/bridge-config-hash: "${config_hash}"
    borealis.io/traffic-owner: "${traffic_owner}"
spec:
  replicas: ${replicas}
  revisionHistoryLimit: 2
  minReadySeconds: 15
  selector:
    matchLabels:
      app.kubernetes.io/name: webui-frontend
      app.kubernetes.io/part-of: borealis
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  template:
    metadata:
      labels:
        app.kubernetes.io/name: webui-frontend
        app.kubernetes.io/part-of: borealis
        app.kubernetes.io/managed-by: Engine.sh
        app.kubernetes.io/component: frontend
        borealis.io/service-key: webui-frontend
        borealis.io/stage: ${workload_stage}
      annotations:
        borealis.io/bridge-config-hash: "${config_hash}"
        borealis.io/traffic-owner: "${traffic_owner}"
        borealis.io/pids-limit: "$(read_env_value BOREALIS_WEBUI_FRONTEND_PIDS_LIMIT)"
    spec:
      automountServiceAccountToken: false
      enableServiceLinks: false
      terminationGracePeriodSeconds: 45
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: borealis.io/application-state
                    operator: In
                    values: ["active"]
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              podAffinityTerm:
                topologyKey: kubernetes.io/hostname
                labelSelector:
                  matchLabels: {app.kubernetes.io/name: webui-frontend}
      securityContext:
        runAsNonRoot: true
        runAsUser: ${runtime_uid}
        runAsGroup: ${runtime_gid}
        fsGroup: ${runtime_gid}
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: webui-frontend
          image: ${image}
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: ${port}
              protocol: TCP
          env:
$(k3s_timezone_env_entries)
            - name: BOREALIS_WEBUI_MODE
              value: "${mode}"
            - name: BOREALIS_WEBUI_UPSTREAM_PORT
              value: "${port}"
            - name: BOREALIS_WEBUI_BIND_HOST
              value: "0.0.0.0"
            - name: BOREALIS_WEBUI_HEALTH_HOST
              value: "127.0.0.1"
            - name: BOREALIS_WEBUI_VITE_CACHE_DIR
              value: "/opt/Borealis/Data/Engine/web-interface/node_modules/.vite"
            - name: HOME
              value: "/tmp"
          startupProbe:
            exec:
              command:
                - borealis-webui-healthcheck
                - startup
            periodSeconds: 2
            timeoutSeconds: 5
            failureThreshold: 90
          readinessProbe:
            exec:
              command:
                - borealis-webui-healthcheck
                - ready
            periodSeconds: 5
            timeoutSeconds: 5
            failureThreshold: 3
          livenessProbe:
            exec:
              command:
                - borealis-webui-healthcheck
                - live
            initialDelaySeconds: 190
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 3
          lifecycle:
            preStop:
              exec:
                command: ["sh", "-c", "touch /tmp/borealis-draining; sleep 10"]
          resources:
            requests:
              cpu: 50m
              memory: 128Mi
            limits:
              cpu: ${cpu_limit}
              memory: ${memory_limit}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: tmp
              mountPath: /tmp
$(k3s_timezone_volume_mount_entries)
EOF
  if [[ "${mode}" == "dev" ]]; then
    cat <<EOF
            - name: webui-vite-cache
              mountPath: /opt/Borealis/Data/Engine/web-interface/node_modules/.vite
            - name: webui-vite-temp
              mountPath: /opt/Borealis/Data/Engine/web-interface/node_modules/.vite-temp
            - name: webui-src
              mountPath: /opt/Borealis/Data/Engine/web-interface/src
              readOnly: true
            - name: webui-public
              mountPath: /opt/Borealis/Data/Engine/web-interface/public
              readOnly: true
            - name: webui-unit-tests
              mountPath: /opt/Borealis/Data/Engine/web-interface/Unit_Tests
              readOnly: true
            - name: webui-index
              mountPath: /opt/Borealis/Data/Engine/web-interface/index.html
              readOnly: true
            - name: webui-package
              mountPath: /opt/Borealis/Data/Engine/web-interface/package.json
              readOnly: true
            - name: webui-tsconfig
              mountPath: /opt/Borealis/Data/Engine/web-interface/tsconfig.json
              readOnly: true
            - name: webui-vite-config
              mountPath: /opt/Borealis/Data/Engine/web-interface/vite.config.mts
              readOnly: true
EOF
  fi
  cat <<EOF
      volumes:
        - name: tmp
          emptyDir:
            medium: Memory
            sizeLimit: 256Mi
$(k3s_timezone_volume_entries)
EOF
  if [[ "${mode}" == "dev" ]]; then
    cat <<EOF
        - name: webui-vite-cache
          emptyDir:
            medium: Memory
            sizeLimit: 256Mi
        - name: webui-vite-temp
          emptyDir:
            medium: Memory
            sizeLimit: 128Mi
        - name: webui-src
          hostPath:
            path: ${WEBUI_RUNTIME_SOURCE_DIR}/src
            type: Directory
        - name: webui-public
          hostPath:
            path: ${WEBUI_RUNTIME_SOURCE_DIR}/public
            type: Directory
        - name: webui-unit-tests
          hostPath:
            path: ${WEBUI_RUNTIME_SOURCE_DIR}/Unit_Tests
            type: Directory
        - name: webui-index
          hostPath:
            path: ${WEBUI_RUNTIME_SOURCE_DIR}/index.html
            type: File
        - name: webui-package
          hostPath:
            path: ${WEBUI_RUNTIME_SOURCE_DIR}/package.json
            type: File
        - name: webui-tsconfig
          hostPath:
            path: ${WEBUI_RUNTIME_SOURCE_DIR}/tsconfig.json
            type: File
        - name: webui-vite-config
          hostPath:
            path: ${WEBUI_RUNTIME_SOURCE_DIR}/vite.config.mts
            type: File
EOF
  fi
  cat <<EOF
---
apiVersion: v1
kind: Service
metadata:
  name: webui-frontend
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: webui-frontend
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: frontend
    borealis.io/service-key: webui-frontend
    borealis.io/stage: ${workload_stage}
  annotations:
    borealis.io/bridge-config-hash: "${config_hash}"
    borealis.io/traffic-owner: "${traffic_owner}"
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: webui-frontend
    app.kubernetes.io/part-of: borealis
  ports:
    - name: http
      port: ${port}
      targetPort: http
      protocol: TCP
EOF
}

render_k3s_remote_desktop_guacd_bridge_manifest() {
  local image="$1"
  local config_hash="$2"
  local runtime_uid="$3"
  local runtime_gid="$4"
  local port="$5"
  local memory_limit="$6"
  local cpu_limit="$7"
  local replicas
  replicas="$(generic_k3s_workload_replicas)"
  cat <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: remote-desktop-guacd
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: remote-desktop-guacd
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: remote-desktop
    borealis.io/service-key: remote-desktop-guacd
    borealis.io/stage: workload-bridge
  annotations:
    borealis.io/bridge-config-hash: "${config_hash}"
    borealis.io/traffic-owner: "docker-compose"
spec:
  replicas: ${replicas}
  revisionHistoryLimit: 2
  minReadySeconds: 15
  selector:
    matchLabels:
      app.kubernetes.io/name: remote-desktop-guacd
      app.kubernetes.io/part-of: borealis
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  template:
    metadata:
      labels:
        app.kubernetes.io/name: remote-desktop-guacd
        app.kubernetes.io/part-of: borealis
        app.kubernetes.io/managed-by: Engine.sh
        app.kubernetes.io/component: remote-desktop
        borealis.io/service-key: remote-desktop-guacd
        borealis.io/stage: workload-bridge
      annotations:
        borealis.io/bridge-config-hash: "${config_hash}"
        borealis.io/traffic-owner: "docker-compose"
        borealis.io/pids-limit: "$(read_env_value BOREALIS_REMOTE_DESKTOP_GUACD_PIDS_LIMIT)"
    spec:
      automountServiceAccountToken: false
      enableServiceLinks: false
      terminationGracePeriodSeconds: 45
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: borealis.io/application-state
                    operator: In
                    values: ["active"]
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              podAffinityTerm:
                topologyKey: kubernetes.io/hostname
                labelSelector:
                  matchLabels: {app.kubernetes.io/name: remote-desktop-guacd}
      securityContext:
        runAsNonRoot: true
        runAsUser: ${runtime_uid}
        runAsGroup: ${runtime_gid}
        fsGroup: ${runtime_gid}
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: remote-desktop-guacd
          image: ${image}
          imagePullPolicy: IfNotPresent
          ports:
            - name: guacd
              containerPort: ${port}
              protocol: TCP
          env:
$(k3s_timezone_env_entries)
            - name: BOREALIS_GUACD_BIND_HOST
              value: "0.0.0.0"
            - name: BOREALIS_GUACD_HEALTH_HOST
              value: "127.0.0.1"
            - name: BOREALIS_GUACD_PORT
              value: "${port}"
            - name: BOREALIS_GUACD_LOG_DIR
              value: "/tmp/borealis-guacd-logs"
            - name: HOME
              value: "/tmp"
          startupProbe:
            exec:
              command:
                - borealis-guacd-healthcheck
                - startup
            periodSeconds: 2
            timeoutSeconds: 5
            failureThreshold: 60
          readinessProbe:
            exec:
              command:
                - borealis-guacd-healthcheck
                - ready
            periodSeconds: 5
            timeoutSeconds: 5
            failureThreshold: 3
          livenessProbe:
            exec:
              command:
                - borealis-guacd-healthcheck
                - live
            initialDelaySeconds: 130
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 3
          lifecycle:
            preStop:
              exec:
                command: ["sh", "-c", "touch /tmp/borealis-draining; sleep 10"]
          resources:
            requests:
              cpu: 25m
              memory: 64Mi
            limits:
              cpu: ${cpu_limit}
              memory: ${memory_limit}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: tmp
              mountPath: /tmp
$(k3s_timezone_volume_mount_entries)
      volumes:
        - name: tmp
          emptyDir:
            medium: Memory
            sizeLimit: 128Mi
$(k3s_timezone_volume_entries)
---
apiVersion: v1
kind: Service
metadata:
  name: remote-desktop-guacd
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: remote-desktop-guacd
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: remote-desktop
    borealis.io/service-key: remote-desktop-guacd
    borealis.io/stage: workload-bridge
  annotations:
    borealis.io/bridge-config-hash: "${config_hash}"
    borealis.io/traffic-owner: "docker-compose"
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: remote-desktop-guacd
    app.kubernetes.io/part-of: borealis
  ports:
    - name: guacd
      port: ${port}
      targetPort: guacd
      protocol: TCP
EOF
}

ensure_k3s_webui_frontend_workload() {
  local mode="$1"
  local image
  image="$(service_image_tag_or_previous webui-frontend borealis-engine/webui-frontend:local)"
  [[ -n "${image}" ]] || die "WebUI frontend image tag unavailable."

  local runtime_uid
  local runtime_gid
  local port
  local memory_limit
  local cpu_limit
  local pids_limit
  local traffic_owner
  runtime_uid="$(resolve_runtime_owner_uid)"
  runtime_gid="$(resolve_runtime_owner_gid)"
  port="$(read_env_value BOREALIS_WEBUI_UPSTREAM_PORT)"
  port="${port:-8000}"
  memory_limit="$(format_k3s_memory_quantity "$(read_env_value BOREALIS_WEBUI_FRONTEND_MEMORY_LIMIT)")"
  cpu_limit="$(format_k3s_cpu_quantity "$(read_env_value BOREALIS_WEBUI_FRONTEND_CPU_LIMIT)")"
  pids_limit="$(read_env_value BOREALIS_WEBUI_FRONTEND_PIDS_LIMIT)"
  traffic_owner="$(resolve_webui_traffic_owner "${mode}")"

  local config_hash
  config_hash="$(
    printf '%s\n' \
      "schema=${K3S_WEBUI_FRONTEND_VERSION}" \
      "namespace=${K3S_NAMESPACE}" \
      "mode=${mode}" \
      "runtime_uid=${runtime_uid}" \
      "runtime_gid=${runtime_gid}" \
      "image=${image}" \
      "port=${port}" \
      "memory_limit=${memory_limit}" \
      "cpu_limit=${cpu_limit}" \
      "pids_limit=${pids_limit}" \
      "traffic_owner=${traffic_owner}" \
      "runtime_source_dir=${WEBUI_RUNTIME_SOURCE_DIR}" \
      "vite_cache_dir=/opt/Borealis/Data/Engine/web-interface/node_modules/.vite" \
      "vite_temp_dir=/opt/Borealis/Data/Engine/web-interface/node_modules/.vite-temp" \
      "timezone=$(host_timezone_value)" \
      "timezone_host_mounts=host-zoneinfo-v1" \
      | sha256sum | awk '{print $1}'
  )"
  K3S_LAST_WEBUI_FRONTEND_CONFIG_HASH="${config_hash}"

  import_k3s_local_image_into_k3s "webui-frontend" "${image}" "k3s-webui-frontend"
  if k3s_manifest_config_current \
    "${K3S_WEBUI_FRONTEND_CONFIG_HASH_FILE}" \
    "${config_hash}" \
    "borealis.io/bridge-config-hash" \
    "deployment/webui-frontend" \
    "service/webui-frontend"; then
    log_k3s_manifest_unchanged "k3s-webui-frontend" "${config_hash}"
    return 0
  fi

  log_status "k3s-webui-frontend" "Applying Manifests" "${C_YELLOW}"
  local manifest_file
  manifest_file="$(mktemp "${DEPLOY_DIR}/k3s-webui-frontend.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_k3s_webui_frontend_bridge_manifest \
    "${image}" \
    "${mode}" \
    "${config_hash}" \
    "${runtime_uid}" \
    "${runtime_gid}" \
    "${port}" \
    "${memory_limit}" \
    "${cpu_limit}" \
    "${traffic_owner}" \
    > "${manifest_file}"
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "k3s-webui-frontend" "Apply Failed" "${C_RED}"
    die "Failed to apply K3s WebUI frontend manifest. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"

  log_status "k3s-webui-frontend" "Waiting For Rollout" "${C_YELLOW}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/webui-frontend" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-webui-frontend" "Rollout Failed" "${C_RED}"
    die "K3s WebUI bridge rollout failed. See ${BUILD_LOG}."
  fi
  printf '%s  k3s-webui-frontend\n' "${config_hash}" > "${K3S_WEBUI_FRONTEND_CONFIG_HASH_FILE}"
  if [[ "${traffic_owner}" == "k3s" ]]; then
    log_status "k3s-webui-frontend" "Ready - Traffic Owner" "${C_GREEN}"
  else
    log_status "k3s-webui-frontend" "Ready - Bridge" "${C_GREEN}"
  fi
}

ensure_k3s_remote_desktop_guacd_workload() {
  local image
  image="$(service_image_tag_or_previous remote-desktop-guacd borealis-engine/remote-desktop-guacd:local)"
  [[ -n "${image}" ]] || die "Remote desktop guacd image tag unavailable."

  local runtime_uid
  local runtime_gid
  local port
  local memory_limit
  local cpu_limit
  local pids_limit
  runtime_uid="$(resolve_runtime_owner_uid)"
  runtime_gid="$(resolve_runtime_owner_gid)"
  port="$(read_env_value BOREALIS_GUACD_PORT)"
  port="${port:-4822}"
  memory_limit="$(format_k3s_memory_quantity "$(read_env_value BOREALIS_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT)")"
  cpu_limit="$(format_k3s_cpu_quantity "$(read_env_value BOREALIS_REMOTE_DESKTOP_GUACD_CPU_LIMIT)")"
  pids_limit="$(read_env_value BOREALIS_REMOTE_DESKTOP_GUACD_PIDS_LIMIT)"

  local config_hash
  config_hash="$(
    printf '%s\n' \
      "schema=${K3S_REMOTE_DESKTOP_GUACD_VERSION}" \
      "namespace=${K3S_NAMESPACE}" \
      "runtime_uid=${runtime_uid}" \
      "runtime_gid=${runtime_gid}" \
      "image=${image}" \
      "port=${port}" \
      "memory_limit=${memory_limit}" \
      "cpu_limit=${cpu_limit}" \
      "pids_limit=${pids_limit}" \
      "timezone=$(host_timezone_value)" \
      "timezone_host_mounts=host-zoneinfo-v1" \
      | sha256sum | awk '{print $1}'
  )"
  K3S_LAST_REMOTE_DESKTOP_GUACD_CONFIG_HASH="${config_hash}"

  import_k3s_local_image_into_k3s "remote-desktop-guacd" "${image}" "k3s-remote-desktop-guacd"
  if k3s_manifest_config_current \
    "${K3S_REMOTE_DESKTOP_GUACD_CONFIG_HASH_FILE}" \
    "${config_hash}" \
    "borealis.io/bridge-config-hash" \
    "deployment/remote-desktop-guacd" \
    "service/remote-desktop-guacd"; then
    log_k3s_manifest_unchanged "k3s-remote-desktop-guacd" "${config_hash}"
    return 0
  fi

  log_status "k3s-remote-desktop-guacd" "Applying Manifests" "${C_YELLOW}"
  local manifest_file
  manifest_file="$(mktemp "${DEPLOY_DIR}/k3s-remote-desktop-guacd.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_k3s_remote_desktop_guacd_bridge_manifest \
    "${image}" \
    "${config_hash}" \
    "${runtime_uid}" \
    "${runtime_gid}" \
    "${port}" \
    "${memory_limit}" \
    "${cpu_limit}" \
    > "${manifest_file}"
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "k3s-remote-desktop-guacd" "Apply Failed" "${C_RED}"
    die "Failed to apply K3s guacd manifest. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"

  log_status "k3s-remote-desktop-guacd" "Waiting For Rollout" "${C_YELLOW}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/remote-desktop-guacd" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-remote-desktop-guacd" "Rollout Failed" "${C_RED}"
    die "K3s guacd bridge rollout failed. See ${BUILD_LOG}."
  fi
  printf '%s  k3s-remote-desktop-guacd\n' "${config_hash}" > "${K3S_REMOTE_DESKTOP_GUACD_CONFIG_HASH_FILE}"
  log_status "k3s-remote-desktop-guacd" "Ready" "${C_GREEN}"
}

ensure_k3s_bridge_workloads() {
  local mode="$1"
  K3S_LAST_WEBUI_FRONTEND_CONFIG_HASH=""
  K3S_LAST_REMOTE_DESKTOP_GUACD_CONFIG_HASH=""
  ensure_k3s_webui_frontend_workload "${mode}"
  ensure_k3s_remote_desktop_guacd_workload "${mode}"
  local aggregate_hash
  aggregate_hash="$(
    printf '%s\n' \
      "schema=${K3S_BRIDGE_WORKLOADS_VERSION}" \
      "webui=${K3S_LAST_WEBUI_FRONTEND_CONFIG_HASH}" \
      "remote_desktop_guacd=${K3S_LAST_REMOTE_DESKTOP_GUACD_CONFIG_HASH}" \
      | sha256sum | awk '{print $1}'
  )"
  printf '%s  k3s-bridge-workloads\n' "${aggregate_hash}" > "${K3S_BRIDGE_WORKLOADS_CONFIG_HASH_FILE}"
}

k3s_wireguard_tunnel_pod_name() {
  k3s_kubectl -n "${K3S_NAMESPACE}" get pods \
    -l 'app.kubernetes.io/name=wireguard-tunnel,app.kubernetes.io/part-of=borealis' \
    -o jsonpath='{range .items[?(@.status.phase=="Running")]}{.metadata.name}{"\n"}{end}' 2>/dev/null \
    | awk 'NF {print; exit}'
}

k3s_wireguard_control_client() {
  local command="$1"
  local pod_name
  pod_name="$(k3s_wireguard_tunnel_pod_name)"
  [[ -n "${pod_name}" ]] || return 1
  k3s_kubectl -n "${K3S_NAMESPACE}" exec "${pod_name}" -c wireguard-tunnel -- borealis-wireguard-control-client "${command}"
}

render_k3s_wireguard_tunnel_manifest() {
  local image="$1"
  local config_hash="$2"
  local runtime_gid="$3"
  local port="$4"
  local memory_limit="$5"
  local cpu_limit="$6"
  local replicas
  replicas="$(generic_k3s_workload_replicas)"
  cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${BOREALIS_WIREGUARD_TUNNEL_RUNTIME_SECRET_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: wireguard-tunnel
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: wireguard-cutover
  annotations:
    borealis.io/wireguard-config-hash: "${config_hash}"
type: Opaque
data:
$(borealis_runtime_env_secret_data "${RUNTIME_ENV}")
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: wireguard-tunnel
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: wireguard-tunnel
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: networking
    borealis.io/service-key: wireguard-tunnel
    borealis.io/stage: wireguard-cutover
  annotations:
    borealis.io/wireguard-config-hash: "${config_hash}"
    borealis.io/network-mode: "host-network"
    borealis.io/runtime-owner: "k3s"
spec:
  replicas: ${replicas}
  revisionHistoryLimit: 2
  selector:
    matchLabels:
      app.kubernetes.io/name: wireguard-tunnel
      app.kubernetes.io/part-of: borealis
  strategy:
    type: Recreate
  template:
    metadata:
      labels:
        app.kubernetes.io/name: wireguard-tunnel
        app.kubernetes.io/part-of: borealis
        app.kubernetes.io/managed-by: Engine.sh
        app.kubernetes.io/component: networking
        borealis.io/service-key: wireguard-tunnel
        borealis.io/stage: wireguard-cutover
      annotations:
        borealis.io/wireguard-config-hash: "${config_hash}"
        borealis.io/network-mode: "host-network"
        borealis.io/runtime-owner: "k3s"
        borealis.io/pids-limit: "$(read_env_value BOREALIS_WIREGUARD_TUNNEL_PIDS_LIMIT)"
    spec:
      automountServiceAccountToken: false
      enableServiceLinks: false
      hostNetwork: true
      dnsPolicy: ClusterFirstWithHostNet
      nodeSelector:
        borealis.io/engine-node: "true"
      terminationGracePeriodSeconds: 10
      securityContext:
        runAsNonRoot: false
        runAsUser: 0
        runAsGroup: ${runtime_gid}
        fsGroup: ${runtime_gid}
        fsGroupChangePolicy: OnRootMismatch
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: wireguard-tunnel
          image: ${image}
          imagePullPolicy: IfNotPresent
          ports:
            - name: wireguard
              containerPort: ${port}
              protocol: UDP
          envFrom:
            - secretRef:
                name: ${BOREALIS_WIREGUARD_TUNNEL_RUNTIME_SECRET_NAME}
          env:
$(k3s_timezone_env_entries)
            - name: HOME
              value: "/tmp"
          startupProbe:
            exec:
              command: ["sh", "-c", "test -S \"\${BOREALIS_WIREGUARD_CONTROL_SOCKET:-/opt/Borealis/Engine/Services/wireguard-tunnel/run/control.sock}\""]
            periodSeconds: 2
            timeoutSeconds: 2
            failureThreshold: 90
          readinessProbe:
            exec:
              command:
                - borealis-wireguard-healthcheck
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 3
          livenessProbe:
            exec:
              command: ["sh", "-c", "kill -0 1"]
            initialDelaySeconds: 190
            periodSeconds: 10
            timeoutSeconds: 2
            failureThreshold: 3
          lifecycle:
            preStop:
              exec:
                command: ["sh", "-c", "borealis-wireguard-control-client withdraw || true; sleep 5"]
          resources:
            requests:
              cpu: 25m
              memory: 48Mi
            limits:
              cpu: ${cpu_limit}
              memory: ${memory_limit}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
              add: ["NET_ADMIN", "NET_RAW"]
          volumeMounts:
            - name: tmp
              mountPath: /tmp
            - name: run-scratch
              mountPath: /run
$(k3s_timezone_volume_mount_entries)
            - name: wireguard-runtime
              mountPath: /opt/Borealis/Engine/Services/wireguard-tunnel
            - name: dev-net-tun
              mountPath: /dev/net/tun
      volumes:
        - name: tmp
          emptyDir:
            medium: Memory
            sizeLimit: 64Mi
        - name: run-scratch
          emptyDir:
            medium: Memory
            sizeLimit: 32Mi
$(k3s_timezone_volume_entries)
        - name: wireguard-runtime
          hostPath:
            path: ${RUNTIME_ROOT}/Services/wireguard-tunnel
            type: Directory
        - name: dev-net-tun
          hostPath:
            path: /dev/net/tun
            type: CharDevice
EOF
}

ensure_k3s_wireguard_tunnel() {
  local mode="$1"
  local image
  image="$(service_image_tag_or_previous wireguard-tunnel borealis-engine/wireguard-tunnel:local)"
  [[ -n "${image}" ]] || die "WireGuard tunnel image tag unavailable."
  [[ -c /dev/net/tun ]] || die "/dev/net/tun is missing; K3s WireGuard tunnel cannot start."

  local runtime_gid
  local port
  local memory_limit
  local cpu_limit
  local runtime_env_hash
  local pids_limit
  runtime_gid="$(resolve_runtime_owner_gid)"
  port="$(read_env_value BOREALIS_WIREGUARD_PORT)"
  port="$(format_k3s_tcp_port "${port:-30000}")"
  memory_limit="$(format_k3s_memory_quantity "$(read_env_value BOREALIS_WIREGUARD_TUNNEL_MEMORY_LIMIT)")"
  cpu_limit="$(format_k3s_cpu_quantity "$(read_env_value BOREALIS_WIREGUARD_TUNNEL_CPU_LIMIT)")"
  runtime_env_hash="$(sha256sum "${RUNTIME_ENV}" | awk '{print $1}')"
  pids_limit="$(read_env_value BOREALIS_WIREGUARD_TUNNEL_PIDS_LIMIT)"

  local config_hash
  config_hash="$(
    printf '%s\n' \
      "schema=${K3S_WIREGUARD_TUNNEL_VERSION}" \
      "namespace=${K3S_NAMESPACE}" \
      "mode=${mode}" \
      "image=${image}" \
      "runtime_gid=${runtime_gid}" \
      "port=${port}" \
      "memory_limit=${memory_limit}" \
      "cpu_limit=${cpu_limit}" \
      "pids_limit=${pids_limit}" \
      "runtime_env_hash=${runtime_env_hash}" \
      "runtime_secret=${BOREALIS_WIREGUARD_TUNNEL_RUNTIME_SECRET_NAME}" \
      "wireguard_runtime=${RUNTIME_ROOT}/Services/wireguard-tunnel" \
      "dev_net_tun=/dev/net/tun" \
      "timezone=$(host_timezone_value)" \
      "timezone_host_mounts=host-zoneinfo-v1" \
      "security=hostnetwork-root-netadmin-netraw-readonlyroot-v1" \
      | sha256sum | awk '{print $1}'
  )"

  import_k3s_local_image_into_k3s "wireguard-tunnel" "${image}" "k3s-wireguard-tunnel"
  retire_compose_wireguard_tunnel_container
  if k3s_manifest_config_current \
    "${K3S_WIREGUARD_TUNNEL_CONFIG_HASH_FILE}" \
    "${config_hash}" \
    "borealis.io/wireguard-config-hash" \
    "secret/${BOREALIS_WIREGUARD_TUNNEL_RUNTIME_SECRET_NAME}" \
    "deployment/wireguard-tunnel"; then
    log_k3s_manifest_unchanged "k3s-wireguard-tunnel" "${config_hash}"
    ensure_cluster_wireguard_routes
    return 0
  fi

  log_status "k3s-wireguard-tunnel" "Applying Manifests" "${C_YELLOW}"
  local manifest_file
  manifest_file="$(mktemp "${DEPLOY_DIR}/k3s-wireguard-tunnel.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_k3s_wireguard_tunnel_manifest \
    "${image}" \
    "${config_hash}" \
    "${runtime_gid}" \
    "${port}" \
    "${memory_limit}" \
    "${cpu_limit}" \
    > "${manifest_file}"
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "k3s-wireguard-tunnel" "Apply Failed" "${C_RED}"
    die "Failed to apply K3s WireGuard tunnel manifest. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"

  log_status "k3s-wireguard-tunnel" "Waiting For Rollout" "${C_YELLOW}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/wireguard-tunnel" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-wireguard-tunnel" "Rollout Failed" "${C_RED}"
    die "K3s WireGuard tunnel rollout failed. See ${BUILD_LOG}."
  fi

  log_status "k3s-wireguard-tunnel" "Verifying Control Socket" "${C_YELLOW}"
  if ! k3s_wireguard_control_client ping >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-wireguard-tunnel" "Control Socket Failed" "${C_RED}"
    die "K3s WireGuard control socket check failed. See ${BUILD_LOG}."
  fi

  printf '%s  k3s-wireguard-tunnel\n' "${config_hash}" > "${K3S_WIREGUARD_TUNNEL_CONFIG_HASH_FILE}"
  log_status "k3s-wireguard-tunnel" "Ready" "${C_GREEN}"
  ensure_cluster_wireguard_routes
}

k3s_traefik_edge_healthcheck() {
  local health_port
  health_port="$(read_env_value BOREALIS_TRAEFIK_HEALTH_PORT)"
  health_port="${health_port:-8082}"
  curl -fsS "http://127.0.0.1:${health_port}/ping" >/dev/null
}

render_k3s_traefik_edge_manifest() {
  local image="$1"
  local config_hash="$2"
  local runtime_gid="$3"
  local health_port="$4"
  local memory_limit="$5"
  local cpu_limit="$6"
  local replicas
  replicas="$(generic_k3s_workload_replicas)"
  cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${BOREALIS_TRAEFIK_EDGE_RUNTIME_SECRET_NAME}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: traefik-edge
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    borealis.io/stage: edge-cutover
  annotations:
    borealis.io/edge-config-hash: "${config_hash}"
type: Opaque
data:
$(borealis_traefik_runtime_secret_data)
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: traefik-edge
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: traefik-edge
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: edge
    borealis.io/service-key: traefik-edge
    borealis.io/stage: edge-cutover
  annotations:
    borealis.io/edge-config-hash: "${config_hash}"
    borealis.io/network-mode: "host-network"
    borealis.io/traffic-owner: "k3s"
spec:
  replicas: ${replicas}
  revisionHistoryLimit: 2
  minReadySeconds: 15
  selector:
    matchLabels:
      app.kubernetes.io/name: traefik-edge
      app.kubernetes.io/part-of: borealis
  strategy:
    type: Recreate
  template:
    metadata:
      labels:
        app.kubernetes.io/name: traefik-edge
        app.kubernetes.io/part-of: borealis
        app.kubernetes.io/managed-by: Engine.sh
        app.kubernetes.io/component: edge
        borealis.io/service-key: traefik-edge
        borealis.io/stage: edge-cutover
      annotations:
        borealis.io/edge-config-hash: "${config_hash}"
        borealis.io/network-mode: "host-network"
        borealis.io/traffic-owner: "k3s"
        borealis.io/pids-limit: "$(read_env_value BOREALIS_TRAEFIK_EDGE_PIDS_LIMIT)"
    spec:
      automountServiceAccountToken: false
      enableServiceLinks: false
      hostNetwork: true
      dnsPolicy: ClusterFirstWithHostNet
      terminationGracePeriodSeconds: 45
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
              - matchExpressions:
                  - key: borealis.io/application-state
                    operator: In
                    values: ["active"]
                  - key: borealis.io/edge-eligible
                    operator: In
                    values: ["true"]
      securityContext:
        runAsUser: 0
        runAsGroup: ${runtime_gid}
        fsGroup: ${runtime_gid}
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: traefik-edge
          image: ${image}
          imagePullPolicy: IfNotPresent
          ports:
            - name: web
              containerPort: 80
              protocol: TCP
            - name: websecure
              containerPort: 443
              protocol: TCP
            - name: health
              containerPort: ${health_port}
              protocol: TCP
          envFrom:
            - secretRef:
                name: ${BOREALIS_TRAEFIK_EDGE_RUNTIME_SECRET_NAME}
          env:
            - name: HOME
              value: "/tmp"
          startupProbe:
            exec:
              command:
                - sh
                - -c
                - "traefik healthcheck --ping=true --ping.entryPoint=borealis-health --entryPoints.borealis-health.address=127.0.0.1:${health_port}"
            periodSeconds: 2
            timeoutSeconds: 5
            failureThreshold: 60
          readinessProbe:
            exec:
              command:
                - sh
                - -c
                - "test ! -e /tmp/borealis-draining && traefik healthcheck --ping=true --ping.entryPoint=borealis-health --entryPoints.borealis-health.address=127.0.0.1:${health_port}"
            periodSeconds: 5
            timeoutSeconds: 5
            failureThreshold: 3
          livenessProbe:
            exec:
              command:
                - sh
                - -c
                - "traefik healthcheck --ping=true --ping.entryPoint=borealis-health --entryPoints.borealis-health.address=127.0.0.1:${health_port}"
            initialDelaySeconds: 130
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 3
          lifecycle:
            preStop:
              exec:
                command: ["sh", "-c", "touch /tmp/borealis-draining; sleep 10"]
          resources:
            requests:
              cpu: 25m
              memory: 96Mi
            limits:
              cpu: ${cpu_limit}
              memory: ${memory_limit}
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
              add: ["DAC_OVERRIDE", "NET_BIND_SERVICE"]
          volumeMounts:
            - name: tmp
              mountPath: /tmp
$(k3s_timezone_volume_mount_entries)
            - name: traefik-runtime
              mountPath: /opt/Borealis/Engine/Services/traefik-edge
      volumes:
        - name: tmp
          emptyDir:
            medium: Memory
            sizeLimit: 64Mi
$(k3s_timezone_volume_entries)
        - name: traefik-runtime
          hostPath:
            path: ${RUNTIME_ROOT}/Services/traefik-edge
            type: Directory
EOF
}

ensure_k3s_traefik_edge() {
  local mode="$1"
  local image
  image="$(service_image_tag_or_previous traefik-edge borealis-engine/traefik-edge:local)"
  [[ -n "${image}" ]] || die "Traefik edge image tag unavailable."

  local runtime_gid
  local health_port
  local memory_limit
  local cpu_limit
  local pids_limit
  runtime_gid="$(resolve_runtime_owner_gid)"
  health_port="$(read_env_value BOREALIS_TRAEFIK_HEALTH_PORT)"
  health_port="${health_port:-8082}"
  memory_limit="$(format_k3s_memory_quantity "$(read_env_value BOREALIS_TRAEFIK_EDGE_MEMORY_LIMIT)")"
  cpu_limit="$(format_k3s_cpu_quantity "$(read_env_value BOREALIS_TRAEFIK_EDGE_CPU_LIMIT)")"
  pids_limit="$(read_env_value BOREALIS_TRAEFIK_EDGE_PIDS_LIMIT)"

  local config_hash
  config_hash="$(
    printf '%s\n' \
      "schema=${K3S_TRAEFIK_EDGE_VERSION}" \
      "namespace=${K3S_NAMESPACE}" \
      "mode=${mode}" \
      "image=${image}" \
      "runtime_gid=${runtime_gid}" \
      "health_port=${health_port}" \
      "memory_limit=${memory_limit}" \
      "cpu_limit=${cpu_limit}" \
      "pids_limit=${pids_limit}" \
      "runtime=${RUNTIME_ROOT}/Services/traefik-edge" \
      "hostname=$(read_env_value BOREALIS_PUBLIC_HOSTNAME)" \
      "aliases=$(read_env_value BOREALIS_PUBLIC_HOSTNAME_ALIASES)" \
      "profile=$(read_env_value BOREALIS_ENGINE_DEPLOYMENT_PROFILE)" \
      "acme_email=$(read_env_value BOREALIS_ACME_EMAIL)" \
      "local_ca=$(read_env_value BOREALIS_LOCAL_CA_ENABLED)" \
      "api_owner=$(read_env_value BOREALIS_API_BACKEND_TRAFFIC_OWNER)" \
      "api_upstream=$(read_env_value BOREALIS_API_BACKEND_UPSTREAM_HOST):$(read_env_value BOREALIS_API_BACKEND_UPSTREAM_PORT)" \
      "webui_owner=$(read_env_value BOREALIS_WEBUI_TRAFFIC_OWNER)" \
      "webui_upstream=$(read_env_value BOREALIS_WEBUI_UPSTREAM_HOST):$(read_env_value BOREALIS_WEBUI_UPSTREAM_PORT)" \
      "trusted_proxy_ips=$(read_env_value BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS)" \
      "forwarded_headers=$(read_env_value BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS)" \
      "proxy_protocol=$(read_env_value BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS)" \
      "timezone=$(host_timezone_value)" \
      "timezone_host_mounts=host-zoneinfo-v1" \
      | sha256sum | awk '{print $1}'
  )"

  import_k3s_local_image_into_k3s "traefik-edge" "${image}" "k3s-traefik-edge"
  retire_compose_traefik_edge_container
  if k3s_manifest_config_current \
    "${K3S_TRAEFIK_EDGE_CONFIG_HASH_FILE}" \
    "${config_hash}" \
    "borealis.io/edge-config-hash" \
    "secret/${BOREALIS_TRAEFIK_EDGE_RUNTIME_SECRET_NAME}" \
    "deployment/traefik-edge"; then
    log_k3s_manifest_unchanged "k3s-traefik-edge" "${config_hash}"
    return 0
  fi

  log_status "k3s-traefik-edge" "Applying Manifests" "${C_YELLOW}"
  local manifest_file
  manifest_file="$(mktemp "${DEPLOY_DIR}/k3s-traefik-edge.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_k3s_traefik_edge_manifest \
    "${image}" \
    "${config_hash}" \
    "${runtime_gid}" \
    "${health_port}" \
    "${memory_limit}" \
    "${cpu_limit}" \
    > "${manifest_file}"
  if ! k3s_kubectl apply --dry-run=server -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "k3s-traefik-edge" "Apply Failed" "${C_RED}"
    die "K3s Traefik edge manifest validation failed. See ${BUILD_LOG}."
  fi
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    log_status "k3s-traefik-edge" "Apply Failed" "${C_RED}"
    die "Failed to apply K3s Traefik edge manifest. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"

  log_status "k3s-traefik-edge" "Waiting For Rollout" "${C_YELLOW}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/traefik-edge" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-traefik-edge" "Rollout Failed" "${C_RED}"
    die "K3s Traefik edge rollout failed. See ${BUILD_LOG}."
  fi
  log_status "k3s-traefik-edge" "Verifying Ping" "${C_YELLOW}"
  if ! k3s_traefik_edge_healthcheck >> "${BUILD_LOG}" 2>&1; then
    log_status "k3s-traefik-edge" "Healthcheck Failed" "${C_RED}"
    die "K3s Traefik edge healthcheck failed. See ${BUILD_LOG}."
  fi

  printf '%s  k3s-traefik-edge\n' "${config_hash}" > "${K3S_TRAEFIK_EDGE_CONFIG_HASH_FILE}"
  log_status "k3s-traefik-edge" "Ready - Traffic Owner" "${C_GREEN}"
}

service_has_k3s_bridge_workload() {
  case "$1" in
    api-backend|job-scheduler|postgres-db|webui-frontend|remote-desktop-guacd|wireguard-tunnel|traefik-edge)
      return 0
      ;;
  esac
  return 1
}

reconcile_k3s_bridge_for_scoped_rebuild() {
  local service="$1"
  local mode="$2"
  service_has_k3s_bridge_workload "${service}" || return 0

  printf '[%s] Reconciling K3s bridge workloads after scoped %s rebuild\n' "$(date +%FT%T)" "${service}" >> "${BUILD_LOG}"
  ensure_k3s_cluster_baseline
  BOREALIS_SUPPRESS_DEPLOYMENT_PROFILE_LOG=1 write_compose_env "${mode}" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME)" "$(read_env_value BOREALIS_ACME_EMAIL)" "$(read_env_value BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS)" "$(read_env_value BOREALIS_ENGINE_DEPLOYMENT_PROFILE)" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME_ALIASES)"
  ensure_borealis_operator_bridge
  if [[ "${service}" == "api-backend" ]]; then
    ensure_k3s_api_backend_bridge "${mode}"
    ensure_k3s_traefik_edge "${mode}"
    retire_compose_api_backend_container
  elif [[ "${service}" == "job-scheduler" ]]; then
    retire_compose_job_scheduler_container
    ensure_k3s_job_scheduler "${mode}"
    recycle_k3s_site_workers_for_timezone
  elif [[ "${service}" == "postgres-db" ]]; then
    ensure_longhorn_storage_baseline
    ensure_k3s_postgres_statefulset "${mode}"
  elif [[ "${service}" == "wireguard-tunnel" ]]; then
    ensure_k3s_wireguard_tunnel "${mode}"
  elif [[ "${service}" == "traefik-edge" ]]; then
    ensure_k3s_traefik_edge "${mode}"
  elif [[ "${service}" == "remote-desktop-guacd" ]]; then
    ensure_k3s_bridge_workloads "${mode}"
    retire_compose_remote_desktop_guacd_container
  else
    ensure_k3s_bridge_workloads "${mode}"
  fi
}

normalize_mode() {
  local raw="${1:-prod}"
  raw="$(printf '%s' "${raw}" | tr '[:upper:]' '[:lower:]')"
  case "${raw}" in
    ""|prod|production) printf '%s\n' "prod" ;;
    dev|developer) printf '%s\n' "dev" ;;
    *) die "Unsupported Engine mode '${1}'. Use prod or dev." ;;
  esac
}

service_env_prefix() {
  printf '%s' "$1" | tr '[:lower:]' '[:upper:]' | tr '-' '_'
}

read_env_value() {
  local key="$1"
  local file="${2:-${COMPOSE_ENV}}"
  [[ -f "${file}" ]] || return 0
  awk -F= -v key="${key}" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "${file}"
}

k3s_service_cluster_ip() {
  local service_name="$1"
  k3s_cluster_installed || return 0
  [[ -s "${K3S_KUBECONFIG}" ]] || return 0
  k3s_kubectl -n "${K3S_NAMESPACE}" get service "${service_name}" -o jsonpath='{.spec.clusterIP}' 2>/dev/null || true
}

borealis_operator_service_cluster_ip() {
  k3s_service_cluster_ip "${BOREALIS_OPERATOR_SERVICE_NAME}"
}

resolve_borealis_operator_base_url() {
  local override="${BOREALIS_OPERATOR_BASE_URL:-}"
  if [[ -n "${override}" ]]; then
    printf '%s\n' "${override}"
    return 0
  fi
  local cluster_ip
  cluster_ip="$(borealis_operator_service_cluster_ip)"
  if [[ -n "${cluster_ip}" && "${cluster_ip}" != "None" ]]; then
    printf 'http://%s:%s\n' "${cluster_ip}" "${BOREALIS_OPERATOR_PORT}"
    return 0
  fi
  read_env_value BOREALIS_OPERATOR_BASE_URL
}

normalize_webui_traffic_owner() {
  local raw="${1:-auto}"
  raw="$(printf '%s' "${raw}" | tr '[:upper:]' '[:lower:]' | tr '_' '-')"
  case "${raw}" in
    ""|auto|k3s|kubernetes)
      printf '%s\n' "k3s"
      ;;
    docker-compose|compose|docker)
      die "Compose WebUI runtime has been retired. Use K3s-owned WebUI."
      ;;
    *)
      die "Unsupported BOREALIS_WEBUI_TRAFFIC_OWNER '${1}'. Use k3s."
      ;;
  esac
}

resolve_webui_traffic_owner() {
  normalize_webui_traffic_owner "${BOREALIS_WEBUI_TRAFFIC_OWNER:-auto}"
}

normalize_api_backend_traffic_owner() {
  local raw="${1:-auto}"
  raw="$(printf '%s' "${raw}" | tr '[:upper:]' '[:lower:]' | tr '_' '-')"
  case "${raw}" in
    ""|auto|k3s|kubernetes)
      printf '%s\n' "k3s"
      ;;
    docker-compose|compose|docker)
      die "Compose API backend runtime has been retired. Use K3s-owned API backend."
      ;;
    *)
      die "Unsupported BOREALIS_API_BACKEND_TRAFFIC_OWNER '${1}'. Use k3s."
      ;;
  esac
}

resolve_api_backend_traffic_owner() {
  normalize_api_backend_traffic_owner "${BOREALIS_API_BACKEND_TRAFFIC_OWNER:-auto}"
}

normalize_postgres_traffic_owner() {
  local raw="${1:-auto}"
  raw="$(printf '%s' "${raw}" | tr '[:upper:]' '[:lower:]' | tr '_' '-')"
  case "${raw}" in
    ""|auto|k3s|kubernetes)
      printf '%s\n' "k3s"
      ;;
    docker-compose|compose|docker)
      printf '%s\n' "docker-compose"
      ;;
    *)
      die "Unsupported BOREALIS_POSTGRES_TRAFFIC_OWNER '${1}'. Use k3s."
      ;;
  esac
}

resolve_postgres_traffic_owner() {
  local owner
  owner="$(normalize_postgres_traffic_owner "${BOREALIS_POSTGRES_TRAFFIC_OWNER:-auto}")"
  if [[ "${owner}" != "k3s" ]]; then
    die "Compose PostgreSQL runtime has been retired. Use K3s-owned PostgreSQL."
  fi
  printf '%s\n' "${owner}"
}

postgres_runtime_owner_label() {
  case "$(normalize_postgres_traffic_owner "${1:-k3s}")" in
    k3s)
      printf '%s\n' "k3s"
      ;;
    *)
      printf '%s\n' "k3s-shadow"
      ;;
  esac
}

postgres_database_url_for_owner() {
  local owner="$1"
  local db_user="$2"
  local db_password="$3"
  local db_name="$4"
  case "$(normalize_postgres_traffic_owner "${owner}")" in
    k3s)
      printf 'postgresql://%s:%s@postgres-db.%s.svc:5432/%s\n' "${db_user}" "${db_password}" "${K3S_NAMESPACE}" "${db_name}"
      ;;
    docker-compose)
      printf 'postgresql://%s:%s@127.0.0.1:5432/%s\n' "${db_user}" "${db_password}" "${db_name}"
      ;;
  esac
}

runtime_cnpg_database_url() {
  local database_url=""
  database_url="$(read_env_value BOREALIS_DATABASE_URL "${RUNTIME_ENV}")"
  [[ -n "${database_url}" ]] || return 1
  python3 - "${database_url}" "${K3S_NAMESPACE}" <<'PY'
import sys
from urllib.parse import urlsplit

database_url, namespace = sys.argv[1:]
try:
    hostname = (urlsplit(database_url).hostname or "").lower().rstrip(".")
except ValueError:
    raise SystemExit(1)
allowed = {
    "borealis-postgres-rw",
    f"borealis-postgres-rw.{namespace}",
    f"borealis-postgres-rw.{namespace}.svc",
    f"borealis-postgres-rw.{namespace}.svc.cluster.local",
}
if hostname not in allowed:
    raise SystemExit(1)
print(database_url)
PY
}

verify_cnpg_cutover_runtime() {
  local database_url="$1"
  local expected_database_url=""
  expected_database_url="$(runtime_cnpg_database_url || true)"
  [[ -n "${expected_database_url}" && "${database_url}" == "${expected_database_url}" ]] \
    || die "CloudNativePG recovery guard received unexpected database endpoint."
  k3s_cluster_installed && [[ -s "${K3S_KUBECONFIG}" ]] \
    || die "Runtime points to CloudNativePG, but K3s is unavailable. Refusing to rewrite database ownership."

  local phase=""
  local ready_instances=""
  local primary_endpoint=""
  local standalone_replicas=""
  phase="$(k3s_kubectl -n "${K3S_NAMESPACE}" get cluster.postgresql.cnpg.io/borealis-postgres -o jsonpath='{.status.phase}' 2>>"${BUILD_LOG}" || true)"
  ready_instances="$(k3s_kubectl -n "${K3S_NAMESPACE}" get cluster.postgresql.cnpg.io/borealis-postgres -o jsonpath='{.status.readyInstances}' 2>>"${BUILD_LOG}" || true)"
  primary_endpoint="$(k3s_kubectl -n "${K3S_NAMESPACE}" get endpointslice \
    -l kubernetes.io/service-name=borealis-postgres-rw \
    -o jsonpath='{range .items[*].endpoints[?(@.conditions.ready==true)]}{.targetRef.name}{"\n"}{end}' 2>>"${BUILD_LOG}" \
    | awk 'NF {print; exit}' || true)"
  standalone_replicas="$(k3s_kubectl -n "${K3S_NAMESPACE}" get statefulset/postgres-db -o jsonpath='{.spec.replicas}' 2>>"${BUILD_LOG}" || true)"

  [[ "${phase}" == "Cluster in healthy state" && "${ready_instances}" =~ ^[1-9][0-9]*$ && -n "${primary_endpoint}" ]] \
    || die "Runtime points to CloudNativePG, but database cluster is not healthy. Refusing to rewrite database ownership."
  [[ "${standalone_replicas}" == "0" ]] \
    || die "Runtime points to CloudNativePG, but retained standalone PostgreSQL is not scaled to zero. Refusing unsafe deploy."
}

k3s_service_dns_name() {
  local service_name="$1"
  printf '%s.%s.svc.cluster.local\n' "${service_name}" "${K3S_NAMESPACE}"
}

api_backend_service_dns_name() {
  k3s_service_dns_name "api-backend"
}

resolve_api_backend_upstream_host() {
  local traffic_owner="$1"
  if [[ "${traffic_owner}" == "k3s" ]]; then
    api_backend_service_dns_name
    return 0
  fi
  printf '%s\n' "127.0.0.1"
}

resolve_api_backend_upstream_port() {
  local traffic_owner="$1"
  if [[ "${traffic_owner}" == "k3s" ]]; then
    format_k3s_tcp_port "${BOREALIS_API_BACKEND_K3S_BRIDGE_PORT}"
    return 0
  fi
  printf '%s\n' "5000"
}

resolve_webui_upstream_host() {
  local traffic_owner="$1"
  if [[ "${traffic_owner}" == "k3s" ]]; then
    local cluster_ip
    cluster_ip="$(k3s_service_cluster_ip "webui-frontend")"
    if [[ -n "${cluster_ip}" && "${cluster_ip}" != "None" ]]; then
      printf '%s\n' "${cluster_ip}"
      return 0
    fi
  fi
  printf '%s\n' "127.0.0.1"
}

retire_compose_container() {
  local service="$1"
  local container="$2"
  local display_name="$3"
  local retired_reason="$4"
  if docker inspect "${container}" >/dev/null 2>&1; then
    printf '[%s] %s Compose container retirement started\n' "$(date +%FT%T)" "${service}" >> "${BUILD_LOG}"
    if ! docker rm -f "${container}" >> "${BUILD_LOG}" 2>&1; then
      log_status "Docker Compose" "Retirement Failed" "${C_RED}"
      die "Failed to remove retired Compose ${display_name} container '${container}'. See ${BUILD_LOG}."
    fi
  fi
  printf '[%s] %s Compose container retired; %s\n' "$(date +%FT%T)" "${service}" "${retired_reason}" >> "${BUILD_LOG}"
}

retire_compose_webui_container() {
  retire_compose_container "webui-frontend" "borealis-engine-webui-frontend" "WebUI" "K3s owns WebUI traffic and lifecycle"
}

retire_compose_docker_proxy_container() {
  retire_compose_container "docker-proxy" "borealis-engine-docker-proxy" "Docker proxy" "Server Info no longer uses Docker proxy for K3s worker metrics or bridge service status"
}

retire_compose_job_scheduler_container() {
  retire_compose_container "job-scheduler" "borealis-engine-job-scheduler" "job scheduler" "K3s owns job-scheduler lifecycle"
}

retire_compose_api_backend_container() {
  retire_compose_container "api-backend" "borealis-engine-api-backend" "API backend" "K3s owns api-backend traffic and lifecycle"
}

retire_compose_postgres_container() {
  retire_compose_container "postgres-db" "borealis-engine-postgres-db" "PostgreSQL" "K3s owns PostgreSQL traffic and lifecycle"
}

retire_compose_wireguard_tunnel_container() {
  retire_compose_container "wireguard-tunnel" "borealis-engine-wireguard-tunnel" "WireGuard tunnel" "K3s owns WireGuard tunnel lifecycle"
}

retire_compose_remote_desktop_guacd_container() {
  retire_compose_container "remote-desktop-guacd" "borealis-engine-remote-desktop-guacd" "guacd" "K3s owns guacd lifecycle"
}

retire_compose_traefik_edge_container() {
  retire_compose_container "traefik-edge" "borealis-engine-traefik-edge" "Traefik edge" "K3s owns public edge lifecycle"
}

retire_compose_site_worker_orchestrator_container() {
  retire_compose_container "site-worker-orchestrator" "borealis-engine-site-worker-orchestrator" "site worker orchestrator" "K3s operator owns site-worker lifecycle and Traefik reload no longer uses Docker helper"
}

k3s_site_worker_pod_count() {
  k3s_kubectl -n "${K3S_NAMESPACE}" get pods \
    -l 'app.kubernetes.io/name=site-worker,app.kubernetes.io/managed-by=borealis-operator' \
    --no-headers 2>/dev/null | awk 'END {print NR+0}'
}

k3s_site_worker_ready_count() {
  k3s_kubectl -n "${K3S_NAMESPACE}" get pods \
    -l 'app.kubernetes.io/name=site-worker,app.kubernetes.io/managed-by=borealis-operator' \
    -o jsonpath='{range .items[*]}{.status.phase}{"\t"}{range .status.containerStatuses[*]}{.ready}{" "}{end}{"\n"}{end}' 2>/dev/null \
    | awk '$1 == "Running" && $2 ~ /true/ {count++} END {print count+0}'
}

wait_for_k3s_site_worker_ready_count() {
  local expected="$1"
  local timeout="${2:-300}"
  local deadline=$((SECONDS + timeout))
  local ready=0
  while ((SECONDS < deadline)); do
    ready="$(k3s_site_worker_ready_count)"
    if ((ready >= expected)); then
      return 0
    fi
    sleep 5
  done
  log_status "site-worker" "K3s Worker Recycle Timed Out" "${C_RED}"
  die "K3s site worker recycle did not restore ${expected} ready pod(s). See ${BUILD_LOG}."
}

recycle_k3s_site_workers_for_api_cutover() {
  local previous_base="${1:-}"
  local current_base="${2:-}"
  [[ -n "${previous_base}" && -n "${current_base}" && "${previous_base}" != "${current_base}" ]] || return 0
  k3s_cluster_installed || return 0
  [[ -s "${K3S_KUBECONFIG}" ]] || return 0

  local count
  count="$(k3s_site_worker_pod_count)"
  ((count > 0)) || return 0

  log_status "site-worker" "Recycling K3s Workers For API Cutover" "${C_YELLOW}"
  printf '[%s] Recycling %s K3s site worker pod(s) because BOREALIS_INTERNAL_API_BASE_URL changed from %s to %s\n' "$(date +%FT%T)" "${count}" "${previous_base}" "${current_base}" >> "${BUILD_LOG}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" delete pods \
    -l 'app.kubernetes.io/name=site-worker,app.kubernetes.io/managed-by=borealis-operator' \
    --wait=false >> "${BUILD_LOG}" 2>&1; then
    log_status "site-worker" "Recycle Failed" "${C_RED}"
    die "Failed to recycle K3s site worker pods for API cutover. See ${BUILD_LOG}."
  fi
  wait_for_k3s_site_worker_ready_count "${count}" 300
  log_status "site-worker" "Recycled - API Cutover" "${C_GREEN}"
}

recycle_k3s_site_workers_for_runtime_secret_change() {
  k3s_cluster_installed || return 0
  [[ -s "${K3S_KUBECONFIG}" ]] || return 0

  local current_hash
  local previous_hash
  current_hash="$(borealis_site_worker_runtime_secret_hash)"
  previous_hash="$(awk '{print $1; exit}' "${K3S_SITE_WORKER_RUNTIME_CONFIG_HASH_FILE}" 2>/dev/null || true)"
  if [[ -n "${previous_hash}" && "${previous_hash}" == "${current_hash}" ]]; then
    return 0
  fi

  local count
  count="$(k3s_site_worker_pod_count)"
  if ((count == 0)); then
    printf '%s  k3s-site-worker-runtime-env\n' "${current_hash}" > "${K3S_SITE_WORKER_RUNTIME_CONFIG_HASH_FILE}"
    return 0
  fi

  log_status "site-worker" "Recycling K3s Workers For Runtime Env" "${C_YELLOW}"
  printf '[%s] Recycling %s K3s site worker pod(s) because site-worker runtime env hash changed from %s to %s\n' "$(date +%FT%T)" "${count}" "${previous_hash:-missing}" "${current_hash}" >> "${BUILD_LOG}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" delete pods \
    -l 'app.kubernetes.io/name=site-worker,app.kubernetes.io/managed-by=borealis-operator' \
    --wait=false >> "${BUILD_LOG}" 2>&1; then
    log_status "site-worker" "Recycle Failed" "${C_RED}"
    die "Failed to recycle K3s site worker pods for runtime env propagation. See ${BUILD_LOG}."
  fi
  wait_for_k3s_site_worker_ready_count "${count}" 300
  printf '%s  k3s-site-worker-runtime-env\n' "${current_hash}" > "${K3S_SITE_WORKER_RUNTIME_CONFIG_HASH_FILE}"
  log_status "site-worker" "Recycled - Runtime Env" "${C_GREEN}"
}

recycle_k3s_site_workers_for_timezone() {
  k3s_cluster_installed || return 0
  [[ -s "${K3S_KUBECONFIG}" ]] || return 0

  local expected_timezone
  expected_timezone="$(host_timezone_value)"
  [[ -n "${expected_timezone}" ]] || return 0

  local pod_names=()
  mapfile -t pod_names < <(
    k3s_kubectl -n "${K3S_NAMESPACE}" get pods \
      -l 'app.kubernetes.io/name=site-worker,app.kubernetes.io/managed-by=borealis-operator' \
      -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true
  )
  ((${#pod_names[@]} > 0)) || return 0

  local pod
  local timezone
  local mismatch_count=0
  for pod in "${pod_names[@]}"; do
    [[ -n "${pod}" ]] || continue
    timezone="$(k3s_kubectl -n "${K3S_NAMESPACE}" exec "${pod}" -c site-worker -- sh -c 'printf "%s" "${TZ:-}"' 2>>"${BUILD_LOG}" || true)"
    if [[ "${timezone}" != "${expected_timezone}" ]]; then
      mismatch_count=$((mismatch_count + 1))
      printf '[%s] K3s site worker pod %s has timezone %s; expected %s\n' "$(date +%FT%T)" "${pod}" "${timezone:-unset}" "${expected_timezone}" >> "${BUILD_LOG}"
    fi
  done
  ((mismatch_count > 0)) || return 0

  log_status "site-worker" "Recycling K3s Workers For Timezone" "${C_YELLOW}"
  printf '[%s] Recycling %s K3s site worker pod(s) because %s pod(s) do not inherit host timezone %s\n' "$(date +%FT%T)" "${#pod_names[@]}" "${mismatch_count}" "${expected_timezone}" >> "${BUILD_LOG}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" delete pods \
    -l 'app.kubernetes.io/name=site-worker,app.kubernetes.io/managed-by=borealis-operator' \
    --wait=false >> "${BUILD_LOG}" 2>&1; then
    log_status "site-worker" "Recycle Failed" "${C_RED}"
    die "Failed to recycle K3s site worker pods for host timezone propagation. See ${BUILD_LOG}."
  fi
  wait_for_k3s_site_worker_ready_count "${#pod_names[@]}" 300
  log_status "site-worker" "Recycled - Timezone" "${C_GREEN}"
}

env_key_exists() {
  local key="$1"
  local file="${2:-${COMPOSE_ENV}}"
  [[ -f "${file}" ]] || return 1
  awk -F= -v key="${key}" '$1 == key { found = 1 } END { exit found ? 0 : 1 }' "${file}"
}

generate_secret() {
  if command_exists openssl; then
    openssl rand -hex 24
    return 0
  fi
  date +%s%N | sha256sum | awk '{print $1}'
}

resolve_runtime_owner_uid() {
  if [[ -n "${BOREALIS_ENGINE_RUNTIME_OWNER_UID:-}" ]]; then
    printf '%s\n' "${BOREALIS_ENGINE_RUNTIME_OWNER_UID}"
  elif getent passwd "${ENGINE_RUNTIME_USER}" >/dev/null 2>&1; then
    getent_field passwd "${ENGINE_RUNTIME_USER}" 3
  else
    printf '%s\n' "${ENGINE_RUNTIME_UID}"
  fi
}

resolve_runtime_owner_gid() {
  if [[ -n "${BOREALIS_ENGINE_RUNTIME_OWNER_GID:-}" ]]; then
    printf '%s\n' "${BOREALIS_ENGINE_RUNTIME_OWNER_GID}"
  elif getent group "${ENGINE_RUNTIME_GROUP}" >/dev/null 2>&1; then
    getent_field group "${ENGINE_RUNTIME_GROUP}" 3
  else
    printf '%s\n' "${ENGINE_RUNTIME_GID}"
  fi
}

resolve_postgres_runtime_uid() {
  if [[ -n "${POSTGRES_RUNTIME_UID}" ]]; then
    printf '%s\n' "${POSTGRES_RUNTIME_UID}"
    return 0
  fi
  local pg_version="${RUNTIME_ROOT}/Services/postgres-db/state/PG_VERSION"
  if [[ -e "${pg_version}" ]]; then
    stat -c '%u' "${pg_version}" 2>/dev/null && return 0
  fi
  printf '%s\n' "999"
}

resolve_postgres_runtime_gid() {
  if [[ -n "${POSTGRES_RUNTIME_GID}" ]]; then
    printf '%s\n' "${POSTGRES_RUNTIME_GID}"
    return 0
  fi
  resolve_runtime_owner_gid
}

apply_traefik_dynamic_config_permissions() {
  local dynamic_dir="${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic"
  local owner_uid
  local owner_gid
  owner_uid="$(resolve_runtime_owner_uid)"
  owner_gid="$(resolve_runtime_owner_gid)"
  if [[ "${EUID:-$(id -u)}" -eq 0 && "${owner_uid}" =~ ^[0-9]+$ && "${owner_gid}" =~ ^[0-9]+$ ]]; then
    chown "${owner_uid}:${owner_gid}" "${dynamic_dir}" 2>/dev/null || true
  fi
  chmod 0775 "${dynamic_dir}" 2>/dev/null || true
}

apply_runtime_service_ownership() {
  local owner_uid
  local owner_gid
  local postgres_uid
  local postgres_gid
  owner_uid="$(resolve_runtime_owner_uid)"
  owner_gid="$(resolve_runtime_owner_gid)"
  postgres_uid="$(resolve_postgres_runtime_uid)"
  postgres_gid="$(resolve_postgres_runtime_gid)"
  [[ "${owner_uid}" =~ ^[0-9]+$ && "${owner_gid}" =~ ^[0-9]+$ ]] || return 0
  [[ "${postgres_uid}" =~ ^[0-9]+$ && "${postgres_gid}" =~ ^[0-9]+$ ]] || return 0
  [[ "${EUID:-$(id -u)}" -eq 0 ]] || return 0

  local path
  for path in \
    "${RUNTIME_ROOT}/Services/api-backend" \
    "${RUNTIME_ROOT}/Services/traefik-edge" \
    "${RUNTIME_ROOT}/Services/webui-frontend" \
    "${RUNTIME_ROOT}/Services/remote-desktop-guacd" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel"; do
    [[ -e "${path}" ]] || continue
    chown -R "${owner_uid}:${owner_gid}" "${path}" 2>/dev/null || true
  done

  if [[ -d "${RUNTIME_ROOT}/Services/postgres-db" ]]; then
    chown "${owner_uid}:${owner_gid}" "${RUNTIME_ROOT}/Services/postgres-db" 2>/dev/null || true
  fi
  if [[ -d "${RUNTIME_ROOT}/Services/postgres-db/state" ]]; then
    if [[ -e "${RUNTIME_ROOT}/Services/postgres-db/state/PG_VERSION" ]]; then
      chown "${postgres_uid}:${postgres_gid}" "${RUNTIME_ROOT}/Services/postgres-db/state" 2>/dev/null || true
    else
      chown -R "${postgres_uid}:${postgres_gid}" "${RUNTIME_ROOT}/Services/postgres-db/state" 2>/dev/null || true
    fi
    chmod 0700 "${RUNTIME_ROOT}/Services/postgres-db/state" 2>/dev/null || true
  fi
  path="${RUNTIME_ROOT}/Services/postgres-db/run"
  if [[ -e "${path}" ]]; then
    chown -R "${postgres_uid}:${postgres_gid}" "${path}" 2>/dev/null || true
    chmod 0775 "${path}" 2>/dev/null || true
  fi

  chmod 0750 "${RUNTIME_ROOT}/Services/api-backend/secrets" 2>/dev/null || true
  chmod 0750 "${RUNTIME_ROOT}/Services/api-backend/secrets/Auth_Tokens" 2>/dev/null || true
  chmod 0750 "${RUNTIME_ROOT}/Services/api-backend/secrets/Certificates" 2>/dev/null || true
  chmod 0750 "${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets" 2>/dev/null || true
  chmod 0750 "${RUNTIME_ROOT}/Services/wireguard-tunnel/config" 2>/dev/null || true
  find "${RUNTIME_ROOT}/Services/api-backend/secrets" \
    -type f -exec chmod go-rwx {} + 2>/dev/null || true
  find "${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets" "${RUNTIME_ROOT}/Services/wireguard-tunnel/config" \
    -type f -exec chmod 0640 {} + 2>/dev/null || true
  chmod 0775 "${RUNTIME_ROOT}/Services/wireguard-tunnel/run" 2>/dev/null || true
  chmod 0775 "${RUNTIME_ROOT}/Services/wireguard-tunnel/logs" 2>/dev/null || true
  find "${RUNTIME_ROOT}/Services/wireguard-tunnel/logs" -type f -exec chmod 0664 {} + 2>/dev/null || true
  chmod 0775 "${RUNTIME_ROOT}/Services/api-backend/logs/site-workers" 2>/dev/null || true
  chmod 0775 "${RUNTIME_ROOT}/Services/traefik-edge/config" 2>/dev/null || true
  chmod 0775 "${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic" 2>/dev/null || true
  chmod 0775 "${RUNTIME_ROOT}/Services/traefik-edge/logs" 2>/dev/null || true
  chmod 0775 "${RUNTIME_ROOT}/Services/traefik-edge/state" 2>/dev/null || true
  chmod 0664 "${RUNTIME_ROOT}/Services/traefik-edge/config/traefik.yml" 2>/dev/null || true
  chmod 0664 "${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic/core.yml" 2>/dev/null || true
  chmod 0664 "${RUNTIME_ROOT}/Services/traefik-edge/state/Settings.json" 2>/dev/null || true
  find "${RUNTIME_ROOT}/Services/traefik-edge/logs" -type f -exec chmod 0664 {} + 2>/dev/null || true
  if [[ -e "${RUNTIME_ROOT}/Services/traefik-edge/state/acme.json" ]]; then
    # Keep Traefik ACME storage 0600 while allowing api-backend backup export to read it.
    chown "${owner_uid}:${owner_gid}" "${RUNTIME_ROOT}/Services/traefik-edge/state/acme.json" 2>/dev/null || true
    chmod 0600 "${RUNTIME_ROOT}/Services/traefik-edge/state/acme.json" 2>/dev/null || true
  fi
}

apply_deploy_env_file_permissions() {
  local owner_gid
  owner_gid="$(resolve_runtime_owner_gid)"
  [[ "${owner_gid}" =~ ^[0-9]+$ ]] || return 0
  [[ "${EUID:-$(id -u)}" -eq 0 ]] || return 0

  local path
  for path in "${RUNTIME_ENV}" "${COMPOSE_ENV}"; do
    [[ -e "${path}" ]] || continue
    chown "0:${owner_gid}" "${path}" 2>/dev/null || true
    chmod 0640 "${path}" 2>/dev/null || true
  done
  chmod 0600 "${WEBUI_ENV}" 2>/dev/null || true
}

ensure_service_tree() {
  mkdir -p "${DEPLOY_DIR}"
  mkdir -p \
    "${RUNTIME_ROOT}/Services/api-backend/config" \
    "${RUNTIME_ROOT}/Services/api-backend/logs" \
    "${RUNTIME_ROOT}/Services/api-backend/logs/site-workers" \
    "${RUNTIME_ROOT}/Services/api-backend/logs/VPN_Tunnel" \
    "${RUNTIME_ROOT}/Services/api-backend/secrets" \
    "${RUNTIME_ROOT}/Services/api-backend/secrets/Auth_Tokens" \
    "${RUNTIME_ROOT}/Services/api-backend/secrets/Certificates" \
    "${RUNTIME_ROOT}/Services/api-backend/cache" \
    "${RUNTIME_ROOT}/Services/api-backend/cache/Ansible/collections" \
    "${RUNTIME_ROOT}/Services/api-backend/cache/Ansible/Generated/Runtime" \
    "${RUNTIME_ROOT}/Services/api-backend/cache/Aurora" \
    "${RUNTIME_ROOT}/Services/postgres-db/state" \
    "${RUNTIME_ROOT}/Services/postgres-db/run" \
    "${RUNTIME_ROOT}/Services/traefik-edge/config" \
    "${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic" \
    "${RUNTIME_ROOT}/Services/traefik-edge/env" \
    "${RUNTIME_ROOT}/Services/traefik-edge/logs" \
    "${RUNTIME_ROOT}/Services/traefik-edge/state" \
    "${RUNTIME_ROOT}/Services/traefik-edge/state/local-ca" \
    "${RUNTIME_ROOT}/Services/traefik-edge/state/local-certs" \
    "${RUNTIME_ROOT}/Services/webui-frontend/data" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel/config" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel/logs" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel/run"
  apply_traefik_dynamic_config_permissions
  apply_runtime_service_ownership
}

seed_webui_runtime_source() {
  local mode="${1:-prod}"
  [[ -d "${WEBUI_STAGED_SOURCE_DIR}" ]] || die "WebUI staged source missing: ${WEBUI_STAGED_SOURCE_DIR}"
  local refresh_source="${BOREALIS_REFRESH_WEBUI_RUNTIME_SOURCE:-0}"
  local sync_existing=0
  if [[ "${mode}" == "dev" || "${refresh_source}" == "1" ]]; then
    sync_existing=1
  fi
  if [[ -f "${WEBUI_RUNTIME_SOURCE_DIR}/package.json" && "${sync_existing}" != "1" ]]; then
    return 0
  fi
  if [[ "${refresh_source}" == "1" && -d "${WEBUI_RUNTIME_SOURCE_DIR}" ]]; then
    rm -rf "${WEBUI_RUNTIME_SOURCE_DIR}"
  fi
  mkdir -p "${WEBUI_RUNTIME_SOURCE_DIR}"
  if command_exists rsync; then
    rsync -a --delete \
      --exclude node_modules \
      --exclude build \
      --exclude dist \
      --exclude .vite \
      --exclude coverage \
      --exclude .eslintcache \
      "${WEBUI_STAGED_SOURCE_DIR}/" \
      "${WEBUI_RUNTIME_SOURCE_DIR}/"
    printf '[%s] WebUI runtime source synced mode=%s staged=%s runtime=%s\n' "$(date +%FT%T)" "${mode}" "${WEBUI_STAGED_SOURCE_DIR}" "${WEBUI_RUNTIME_SOURCE_DIR}" >> "${BUILD_LOG}"
    return 0
  fi
  if [[ "${sync_existing}" == "1" ]]; then
    find "${WEBUI_RUNTIME_SOURCE_DIR}" -mindepth 1 -maxdepth 1 \
      ! -name node_modules \
      ! -name build \
      ! -name dist \
      ! -name .vite \
      ! -name coverage \
      ! -name .eslintcache \
      -exec rm -rf {} + 2>/dev/null || true
  fi
  (
    cd "${WEBUI_STAGED_SOURCE_DIR}"
    tar \
      --exclude='./node_modules' \
      --exclude='./build' \
      --exclude='./dist' \
      --exclude='./.vite' \
      --exclude='./coverage' \
      --exclude='./.eslintcache' \
      -cf - .
  ) | (
    cd "${WEBUI_RUNTIME_SOURCE_DIR}"
    tar -xf -
  )
  printf '[%s] WebUI runtime source seeded mode=%s staged=%s runtime=%s\n' "$(date +%FT%T)" "${mode}" "${WEBUI_STAGED_SOURCE_DIR}" "${WEBUI_RUNTIME_SOURCE_DIR}" >> "${BUILD_LOG}"
}

agent_source_digest() {
  python3 - "${AGENT_STAGED_SOURCE_DIR}" <<'PY'
import hashlib
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
if not root.is_dir():
    raise SystemExit(f"Agent source missing: {root}")

digest = hashlib.sha256()
for path in sorted(root.rglob("*"), key=lambda item: item.relative_to(root).as_posix()):
    relative = path.relative_to(root).as_posix().strip("/")
    if not relative:
        continue
    if relative == "dist" or relative.startswith("dist/"):
        continue
    if relative == "cmd/agent/agent_windows.syso":
        continue
    if path.is_dir():
        continue
    digest.update(relative.encode("utf-8"))
    digest.update(b"\0")
    digest.update(path.read_bytes())
    digest.update(b"\0")
print(digest.hexdigest())
PY
}

package_engine_agent_install_cache() {
  local build_id="$1"
  local dist_root="$2"
  local config_path="${RUNTIME_ROOT}/Services/api-backend/config/agent_artifact.json"
  python3 - "${AGENT_UPDATE_CACHE_ROOT}" "${config_path}" "${dist_root}" "${build_id}" <<'PY'
import hashlib
import json
import pathlib
import re
import shutil
import sys
import time
import zipfile
from datetime import datetime, timezone

cache_root = pathlib.Path(sys.argv[1])
config_path = pathlib.Path(sys.argv[2])
dist_root = pathlib.Path(sys.argv[3])
build_id = str(sys.argv[4]).strip().lower()

platform_artifacts = {
    "windows-amd64": "Data/Agent/dist/windows-amd64/Agent.exe",
    "linux-amd64": "Data/Agent/dist/linux-amd64/Agent",
}
compiled_at = int(time.time())
published_at = datetime.fromtimestamp(compiled_at, timezone.utc).isoformat().replace("+00:00", "Z")

def clean_artifact_id(value: str) -> str:
    cleaned = re.sub(r"[^a-z0-9._-]+", "-", value.strip().lower()).strip("-")
    return cleaned or "build"

def artifact_id() -> str:
    cleaned_build = clean_artifact_id(build_id)[:20] or "build"
    return f"engine-{cleaned_build}"

def sha256_file(path: pathlib.Path) -> str:
    hasher = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            hasher.update(chunk)
    return hasher.hexdigest()

def load_existing(path: pathlib.Path) -> dict:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return {}

def package_artifact() -> dict:
    artifact = artifact_id()
    artifact_path = cache_root / f"{artifact}.zip"
    manifest_path = cache_root / f"{artifact}.json"
    build_root = cache_root / "Builds" / artifact
    build_dist = build_root / "dist"
    if build_root.exists():
        shutil.rmtree(build_root)
    shutil.copytree(dist_root, build_dist)

    manifest = {
        "source": "engine",
        "artifact_format": "borealis-go-agent-v1",
        "platform_artifacts": platform_artifacts,
        "build_id": build_id,
        "artifact_id": artifact,
        "artifact_path": str(artifact_path),
        "artifact_sha256": "",
        "artifact_size": 0,
        "published_at": published_at,
        "compiled_at": compiled_at,
        "last_error": "",
    }

    temp_zip = artifact_path.with_suffix(artifact_path.suffix + ".tmp")
    with zipfile.ZipFile(temp_zip, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("manifest.json", json.dumps(manifest, indent=2, sort_keys=True) + "\n")
        for platform, archive_name in platform_artifacts.items():
            source_name = archive_name.removeprefix("Data/Agent/dist/")
            source_path = dist_root / source_name
            if not source_path.is_file() or source_path.stat().st_size <= 0:
                raise SystemExit(f"compiled Agent binary missing for {platform}: {source_path}")
            archive.write(source_path, archive_name)
    temp_zip.replace(artifact_path)
    artifact_path.chmod(0o600)
    manifest["artifact_sha256"] = sha256_file(artifact_path)
    manifest["artifact_size"] = artifact_path.stat().st_size
    manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    manifest_path.chmod(0o600)
    return manifest

cache_root.mkdir(parents=True, exist_ok=True)
(cache_root / "Builds").mkdir(parents=True, exist_ok=True)
config_path.parent.mkdir(parents=True, exist_ok=True)

existing = load_existing(config_path)
legacy_config_path = config_path.with_name("agent_release_channels.json")
legacy_settings = load_existing(legacy_config_path)
created_at = int(existing.get("created_at") or compiled_at)
artifact = package_artifact()
settings = {
    "version": 1,
    "source": "engine",
    "artifact": artifact,
    "created_at": created_at,
    "updated_at": compiled_at,
}
temp_config = config_path.with_suffix(config_path.suffix + ".tmp")
temp_config.write_text(json.dumps(settings, indent=2, sort_keys=True) + "\n", encoding="utf-8")
temp_config.chmod(0o600)
temp_config.replace(config_path)

# Remove retired per-channel metadata and only artifacts named by that metadata.
legacy_channels = legacy_settings.get("channels") if isinstance(legacy_settings.get("channels"), dict) else {}
for legacy_target in legacy_channels.values():
    if not isinstance(legacy_target, dict):
        continue
    raw_legacy_id = str(legacy_target.get("artifact_id") or "").strip().lower()
    legacy_id = clean_artifact_id(raw_legacy_id)
    if not raw_legacy_id or legacy_id != raw_legacy_id or legacy_id == artifact["artifact_id"]:
        continue
    (cache_root / f"{legacy_id}.zip").unlink(missing_ok=True)
    (cache_root / f"{legacy_id}.json").unlink(missing_ok=True)
    shutil.rmtree(cache_root / "Builds" / legacy_id, ignore_errors=True)
legacy_config_path.unlink(missing_ok=True)
print(f"{artifact['artifact_id']}\t{build_id}\t{compiled_at}")
PY
}

ensure_engine_agent_install_cache() {
  local build_script="${AGENT_STAGED_SOURCE_DIR}/build-agent.sh"
  [[ -f "${build_script}" ]] || die "Agent build script missing: ${build_script}"
  mkdir -p "${AGENT_UPDATE_CACHE_ROOT}/Builds" "${AGENT_UPDATE_CACHE_ROOT}/Go"

  local initial_build_id
  initial_build_id="$(agent_source_digest)"
  [[ -n "${initial_build_id}" ]] || die "Agent source digest unavailable."

  local temp_root
  temp_root="$(mktemp -d "${AGENT_UPDATE_CACHE_ROOT}/Builds/.deploy-agent.XXXXXX")"
  local dist_root="${temp_root}/dist"
  local go_install_root="${AGENT_UPDATE_CACHE_ROOT}/Go/go1.22.12"
  log_status "Agent Installer Cache" "Building Agent" "${C_YELLOW}"
  if ! BOREALIS_AGENT_VERSION="${initial_build_id}" \
    BOREALIS_GO_AGENT_OUTPUT_ROOT="${dist_root}" \
    BOREALIS_GO_INSTALL_ROOT="${go_install_root}" \
    bash "${build_script}" >> "${BUILD_LOG}" 2>&1; then
    rm -rf "${temp_root}"
    log_status "Agent Installer Cache" "Build Failed" "${C_RED}"
    die "Engine Agent build failed. See ${BUILD_LOG}."
  fi

  local final_build_id
  final_build_id="$(agent_source_digest)"
  [[ -n "${final_build_id}" ]] || die "Agent source digest unavailable after build."
  if [[ "${final_build_id}" != "${initial_build_id}" ]]; then
    printf '[%s] Agent source digest changed after first build; rebuilding with final build id %s\n' "$(date +%FT%T)" "${final_build_id}" >> "${BUILD_LOG}"
    rm -rf "${dist_root}"
    if ! BOREALIS_AGENT_VERSION="${final_build_id}" \
      BOREALIS_GO_AGENT_OUTPUT_ROOT="${dist_root}" \
      BOREALIS_GO_INSTALL_ROOT="${go_install_root}" \
      bash "${build_script}" >> "${BUILD_LOG}" 2>&1; then
      rm -rf "${temp_root}"
      log_status "Agent Installer Cache" "Build Failed" "${C_RED}"
      die "Engine Agent rebuild failed after source digest changed. See ${BUILD_LOG}."
    fi
  fi

  local metadata
  if ! metadata="$(package_engine_agent_install_cache "${final_build_id}" "${dist_root}")"; then
    rm -rf "${temp_root}"
    log_status "Agent Installer Cache" "Package Failed" "${C_RED}"
    die "Engine Agent install cache packaging failed. See ${BUILD_LOG}."
  fi
  rm -rf "${temp_root}"
  apply_runtime_service_ownership

  local artifact_id
  local packaged_build_id
  local compiled_at
  IFS=$'\t' read -r artifact_id packaged_build_id compiled_at <<< "${metadata}"
  printf '[%s] Engine Agent install cache ready artifact=%s build_id=%s compiled_at=%s\n' "$(date +%FT%T)" "${artifact_id}" "${packaged_build_id}" "${compiled_at}" >> "${BUILD_LOG}"
  log_status "Agent Installer Cache" "Ready - ${artifact_id}" "${C_GREEN}"
}

prune_empty_legacy_runtime_paths() {
  local name=""
  local path=""
  local legacy_paths=(
    "Logs"
    "Auth_Tokens"
    "Certificates"
    "Ansible"
    "Ansible/Generated/Runtime"
    "Ansible/Generated"
    "Ansible/collections"
    "Ansible"
    "WireGuard"
    "LetsEncrypt"
    "Traefik"
    "Shared"
    "Shared/tmp"
    "Shared/downloads"
    "Shared"
    "Cache/AgentUpdates"
    "Cache"
    "Config"
    "Aurora"
    "web-interface"
    "engine_secret.txt"
    "Services/api-backend/env"
    "Services/api-backend/run"
    "Services/api-backend/state"
    "Services/postgres-db/cache"
    "Services/postgres-db/config"
    "Services/postgres-db/env"
    "Services/postgres-db/secrets"
    "Services/remote-desktop-guacd/cache"
    "Services/remote-desktop-guacd/config"
    "Services/remote-desktop-guacd/env"
    "Services/remote-desktop-guacd/run"
    "Services/remote-desktop-guacd/secrets"
    "Services/remote-desktop-guacd/state"
    "Services/traefik-edge/cache"
    "Services/traefik-edge/run"
    "Services/traefik-edge/secrets"
    "Services/webui-frontend/cache"
    "Services/webui-frontend/config"
    "Services/webui-frontend/env"
    "Services/webui-frontend/logs"
    "Services/webui-frontend/run"
    "Services/webui-frontend/secrets"
    "Services/webui-frontend/state"
    "Services/wireguard-tunnel/cache"
    "Services/wireguard-tunnel/env"
    "Services/wireguard-tunnel/state"
  )
  for name in "${legacy_paths[@]}"; do
    path="${RUNTIME_ROOT}/${name}"
    if [[ -L "${path}" ]]; then
      rm -f "${path}"
    elif [[ -d "${path}" ]]; then
      rmdir "${path}" 2>/dev/null || true
    elif [[ -f "${path}" && ! -s "${path}" ]]; then
      rm -f "${path}"
    fi
  done
}

normalize_engine_network_mode() {
  local raw="${1:-}"
  raw="$(printf '%s' "${raw}" | tr '[:upper:]' '[:lower:]' | tr '_' '-')"
  case "${raw}" in
    public|internet|internet-edge|public-edge|externally-accessible|external|public-facing)
      printf '%s\n' "public"
      ;;
    local|local-edge|site-local|internal-only|internal|local-only|private|private-edge|private-network|on-prem|onprem)
      printf '%s\n' "local"
      ;;
    *)
      die "Unsupported Engine network mode '${1}'. Use public or local."
      ;;
  esac
}

engine_network_mode_display_label() {
  case "$(normalize_engine_network_mode "$1")" in
    local) printf '%s\n' "Local" ;;
    *) printf '%s\n' "Public" ;;
  esac
}

engine_deployment_profile_from_network_mode() {
  case "$(normalize_engine_network_mode "$1")" in
    local) printf '%s\n' "internal-only" ;;
    *) printf '%s\n' "externally-accessible" ;;
  esac
}

engine_network_mode_from_deployment_profile() {
  case "$(normalize_engine_deployment_profile "$1")" in
    internal-only) printf '%s\n' "local" ;;
    *) printf '%s\n' "public" ;;
  esac
}

normalize_engine_deployment_profile() {
  local raw="${1:-}"
  raw="$(printf '%s' "${raw}" | tr '[:upper:]' '[:lower:]' | tr '_' '-')"
  case "${raw}" in
    externally-accessible|external|public|public-facing|internet|internet-edge|public-edge)
      printf '%s\n' "externally-accessible"
      ;;
    internal-only|internal|local|local-only|local-edge|site-local|private|private-edge|private-network|on-prem|onprem)
      printf '%s\n' "internal-only"
      ;;
    *)
      die "Unsupported Engine network mode '${1}'. Use public or local."
      ;;
  esac
}

engine_deployment_profile_label() {
  case "$(normalize_engine_deployment_profile "$1")" in
    internal-only) printf '%s\n' "Internal-Only" ;;
    *) printf '%s\n' "Externally Accessible" ;;
  esac
}

resolve_engine_network_mode() {
  local candidate="${ENGINE_NETWORK_MODE:-}"
  if [[ -z "${candidate}" && -n "${BOREALIS_ENGINE_NETWORK_MODE:-}" ]]; then
    candidate="${BOREALIS_ENGINE_NETWORK_MODE}"
  fi
  if [[ -z "${candidate}" && -n "${ENGINE_DEPLOYMENT_PROFILE:-}" ]]; then
    engine_network_mode_from_deployment_profile "${ENGINE_DEPLOYMENT_PROFILE}"
    return 0
  fi
  if [[ -z "${candidate}" && -n "${BOREALIS_ENGINE_DEPLOYMENT_PROFILE:-}" ]]; then
    engine_network_mode_from_deployment_profile "${BOREALIS_ENGINE_DEPLOYMENT_PROFILE}"
    return 0
  fi
  [[ -n "${candidate}" ]] || return 1
  normalize_engine_network_mode "${candidate}"
}

require_explicit_engine_network_mode() {
  local network_mode
  if ! network_mode="$(resolve_engine_network_mode)"; then
    printf '[%s] %bWARNING:%b Engine network mode is required before deployment.\n' "$(date +%FT%T)" "${C_YELLOW}${C_BOLD}" "${C_RESET}" >&2
    printf 'Choose one explicit mode:\n' >&2
    printf '  sudo bash Engine.sh --network-mode public deploy prod\n' >&2
    printf '  sudo bash Engine.sh --network-mode local deploy prod\n' >&2
    printf 'Public mode uses public DNS and Let'\''s Encrypt. Local mode uses private DNS/VPN and Borealis local CA.\n' >&2
    return 2
  fi
  ENGINE_NETWORK_MODE="${network_mode}"
  ENGINE_DEPLOYMENT_PROFILE="$(engine_deployment_profile_from_network_mode "${network_mode}")"
  export BOREALIS_ENGINE_NETWORK_MODE="${ENGINE_NETWORK_MODE}"
  export BOREALIS_ENGINE_DEPLOYMENT_PROFILE="${ENGINE_DEPLOYMENT_PROFILE}"
}

launch_requires_engine_network_mode() {
  case "${1:-deploy}" in
    ""|deploy|prod|production|dev|developer|--service)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

normalize_engine_hostname() {
  python3 - "$1" <<'PY'
import sys
from urllib.parse import urlsplit

raw = (sys.argv[1] or "").strip()
if not raw:
    print("")
    raise SystemExit(0)
text = raw if "://" in raw else "https://" + raw
try:
    parsed = urlsplit(text)
    host = (parsed.hostname or raw).strip().lower().rstrip(".")
except Exception:
    host = raw.strip().lower().rstrip(".")
print(host)
PY
}

hostname_is_ip_literal() {
  python3 - "$1" <<'PY'
import ipaddress
import sys

try:
    ipaddress.ip_address((sys.argv[1] or "").strip())
except Exception:
    raise SystemExit(1)
raise SystemExit(0)
PY
}

normalize_engine_ip_fallback() {
  python3 - "$1" <<'PY'
import ipaddress
import sys

raw = (sys.argv[1] or "").strip()
if not raw or "://" in raw or "/" in raw:
    raise SystemExit(1)
try:
    ip = ipaddress.ip_address(raw)
except Exception:
    raise SystemExit(1)
if ip.is_unspecified or ip.is_loopback or ip.is_multicast:
    raise SystemExit(1)
print(str(ip))
PY
}

detect_engine_ip_fallback() {
  local candidate=""
  if command -v ip >/dev/null 2>&1; then
    candidate="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1; i<=NF; i++) if ($i == "src" && (i+1) <= NF) {print $(i+1); exit}}')"
    if [[ -n "${candidate}" ]] && normalize_engine_ip_fallback "${candidate}" >/dev/null 2>&1; then
      normalize_engine_ip_fallback "${candidate}"
      return 0
    fi
  fi
  if command -v hostname >/dev/null 2>&1; then
    while read -r candidate; do
      if [[ -n "${candidate}" ]] && normalize_engine_ip_fallback "${candidate}" >/dev/null 2>&1; then
        normalize_engine_ip_fallback "${candidate}"
        return 0
      fi
    done < <(hostname -I 2>/dev/null | tr ' ' '\n')
  fi
  printf '%s\n' ""
}

resolve_engine_ip_fallback() {
  local engine_profile="$1"
  normalize_engine_deployment_profile "${engine_profile}" >/dev/null
  local configured="${BOREALIS_ENGINE_IP_FALLBACK:-$(read_env_value BOREALIS_ENGINE_IP_FALLBACK)}"
  if [[ -n "${configured}" ]]; then
    normalize_engine_ip_fallback "${configured}" || die "BOREALIS_ENGINE_IP_FALLBACK must be a bare non-loopback IP address."
    return 0
  fi
  detect_engine_ip_fallback
}

validate_engine_fqdn() {
  local value="$1"
  local label="${2:-Engine FQDN}"
  local hostname
  hostname="$(normalize_engine_hostname "${value}")"
  [[ -n "${hostname}" ]] || die "${label} is required."
  [[ "${hostname}" != "localhost" ]] || die "${label} must be an FQDN, not localhost."
  hostname_is_ip_literal "${hostname}" && die "${label} must be an FQDN, not raw IP address '${hostname}'."
  [[ "${hostname}" == *.* ]] || die "${label} must be a fully qualified DNS name."
  [[ "${hostname}" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$ ]] || die "${label} '${hostname}' contains unsupported DNS characters."
  printf '%s\n' "${hostname}"
}

resolve_engine_hostname_aliases() {
  local primary="$1"
  local raw="${BOREALIS_PUBLIC_HOSTNAME_ALIASES:-${BOREALIS_ENGINE_FQDN_ALIASES:-$(read_env_value BOREALIS_PUBLIC_HOSTNAME_ALIASES)}}"
  python3 - "${primary}" "${raw}" <<'PY'
import sys

primary = (sys.argv[1] or "").strip().lower().rstrip(".")
raw = (sys.argv[2] or "").replace("\n", ",")
seen = set()
values = []
for item in [primary, *raw.split(",")]:
    text = item.strip().lower().rstrip(".")
    if not text or text in seen:
        continue
    seen.add(text)
    values.append(text)
print(",".join(values))
PY
}

validate_engine_hostname_aliases() {
  local aliases="$1"
  local alias=""
  local -a __borealis_aliases=()
  IFS=',' read -r -a __borealis_aliases <<< "${aliases}"
  for alias in "${__borealis_aliases[@]}"; do
    [[ -n "${alias}" ]] || continue
    validate_engine_fqdn "${alias}" "Engine FQDN alias" >/dev/null
  done
}

resolve_public_hostname() {
  local existing
  existing="$(read_env_value BOREALIS_PUBLIC_HOSTNAME)"
  if [[ -n "${BOREALIS_PUBLIC_HOSTNAME:-}" ]]; then
    validate_engine_fqdn "${BOREALIS_PUBLIC_HOSTNAME}" "Engine FQDN"
  elif [[ -n "${existing}" ]]; then
    validate_engine_fqdn "${existing}" "Engine FQDN"
  elif [[ -t 0 ]]; then
    local input=""
    read -r -p "Engine FQDN: " input || true
    validate_engine_fqdn "${input}" "Engine FQDN"
  else
    die "Engine FQDN is required. Set BOREALIS_PUBLIC_HOSTNAME or run Engine.sh interactively."
  fi
}

resolve_acme_email() {
  local existing
  existing="$(read_env_value BOREALIS_ACME_EMAIL)"
  if [[ -n "${BOREALIS_ACME_EMAIL:-}" ]]; then
    printf '%s\n' "${BOREALIS_ACME_EMAIL}"
  elif [[ -n "${existing}" ]]; then
    printf '%s\n' "${existing}"
  elif [[ -t 0 ]]; then
    local input=""
    read -r -p "Let's Encrypt email [blank for local/default certificate]: " input || true
    printf '%s\n' "${input}"
  else
    printf '%s\n' ""
  fi
}

local_ca_root_dir() {
  printf '%s\n' "${RUNTIME_ROOT}/Services/traefik-edge/state/local-ca"
}

local_cert_root_dir() {
  printf '%s\n' "${RUNTIME_ROOT}/Services/traefik-edge/state/local-certs"
}

local_ca_cert_path() {
  printf '%s\n' "$(local_ca_root_dir)/borealis-local-ca.pem"
}

local_ca_key_path() {
  printf '%s\n' "$(local_ca_root_dir)/borealis-local-ca.key"
}

local_tls_cert_path() {
  printf '%s\n' "$(local_cert_root_dir)/traefik-local-leaf.pem"
}

local_tls_key_path() {
  printf '%s\n' "$(local_cert_root_dir)/traefik-local-leaf.key"
}

local_tls_metadata_path() {
  printf '%s\n' "$(local_cert_root_dir)/traefik-local-leaf.metadata"
}

write_local_ca_config() {
  local path="$1"
  cat > "${path}" <<'EOF'
[req]
distinguished_name = req_distinguished_name
x509_extensions = v3_ca
prompt = no

[req_distinguished_name]
CN = Borealis Local Engine CA
O = Borealis

[v3_ca]
basicConstraints = critical,CA:true,pathlen:1
keyUsage = critical,keyCertSign,cRLSign
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always,issuer
EOF
}

write_local_leaf_config() {
  local path="$1"
  local primary="$2"
  local aliases="$3"
  cat > "${path}" <<EOF
[req]
distinguished_name = req_distinguished_name
req_extensions = v3_req
prompt = no

[req_distinguished_name]
CN = ${primary}
O = Borealis

[v3_req]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature,keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names

[alt_names]
EOF
  local index=1
  local alias=""
  local -a __borealis_leaf_aliases=()
  IFS=',' read -r -a __borealis_leaf_aliases <<< "${aliases}"
  for alias in "${__borealis_leaf_aliases[@]}"; do
    alias="$(printf '%s' "${alias}" | xargs)"
    [[ -n "${alias}" ]] || continue
    printf 'DNS.%d = %s\n' "${index}" "${alias}" >> "${path}"
    ((index += 1))
  done
}

ensure_local_ca_material() {
  local profile="$1"
  local primary="$2"
  local aliases="$3"
  [[ "${profile}" == "internal-only" ]] || return 0
  command_exists openssl || die "openssl is required for Internal-Only local CA generation. Install openssl and rerun Engine.sh."
  validate_engine_hostname_aliases "${aliases}"

  local ca_dir cert_dir ca_cert ca_key leaf_cert leaf_key leaf_meta
  ca_dir="$(local_ca_root_dir)"
  cert_dir="$(local_cert_root_dir)"
  ca_cert="$(local_ca_cert_path)"
  ca_key="$(local_ca_key_path)"
  leaf_cert="$(local_tls_cert_path)"
  leaf_key="$(local_tls_key_path)"
  leaf_meta="$(local_tls_metadata_path)"
  mkdir -p "${ca_dir}" "${cert_dir}"

  if [[ ! -s "${ca_cert}" || ! -s "${ca_key}" ]]; then
    local ca_conf
    ca_conf="$(mktemp)"
    write_local_ca_config "${ca_conf}"
    openssl genrsa -out "${ca_key}" 4096 >/dev/null 2>&1
    openssl req -x509 -new -nodes -key "${ca_key}" -sha256 -days 3650 -out "${ca_cert}" -config "${ca_conf}" >/dev/null 2>&1
    rm -f "${ca_conf}"
    chmod 600 "${ca_key}" 2>/dev/null || true
    chmod 644 "${ca_cert}" 2>/dev/null || true
    log_status "Local CA" "Generated" "${C_GREEN}"
  fi

  local current_aliases=""
  [[ -f "${leaf_meta}" ]] && current_aliases="$(read_env_value SAN_HOSTNAMES "${leaf_meta}")"
  local renew_leaf=0
  [[ -s "${leaf_cert}" && -s "${leaf_key}" ]] || renew_leaf=1
  [[ "${current_aliases}" == "${aliases}" ]] || renew_leaf=1
  if [[ -s "${leaf_cert}" ]] && ! openssl x509 -checkend 2592000 -noout -in "${leaf_cert}" >/dev/null 2>&1; then
    renew_leaf=1
  fi

  if [[ "${renew_leaf}" -eq 1 ]]; then
    local leaf_conf csr
    leaf_conf="$(mktemp)"
    csr="$(mktemp)"
    write_local_leaf_config "${leaf_conf}" "${primary}" "${aliases}"
    openssl genrsa -out "${leaf_key}" 2048 >/dev/null 2>&1
    openssl req -new -key "${leaf_key}" -out "${csr}" -config "${leaf_conf}" >/dev/null 2>&1
    openssl x509 -req -in "${csr}" -CA "${ca_cert}" -CAkey "${ca_key}" -CAcreateserial -out "${leaf_cert}" -days 397 -sha256 -extensions v3_req -extfile "${leaf_conf}" >/dev/null 2>&1
    rm -f "${leaf_conf}" "${csr}"
    chmod 600 "${leaf_key}" 2>/dev/null || true
    chmod 644 "${leaf_cert}" 2>/dev/null || true
    cat > "${leaf_meta}" <<EOF
SAN_HOSTNAMES=${aliases}
GENERATED_AT=$(date -u +%FT%TZ)
EOF
    chmod 600 "${leaf_meta}" 2>/dev/null || true
    log_status "Local TLS leaf" "Generated" "${C_GREEN}"
  fi
}

local_ca_cert_b64() {
  local ca_cert
  ca_cert="$(local_ca_cert_path)"
  [[ -s "${ca_cert}" ]] || return 0
  base64 -w 0 "${ca_cert}" 2>/dev/null || base64 "${ca_cert}" | tr -d '\n'
}

resolve_host_timezone() {
  local timezone=""
  if [[ -n "${BOREALIS_ENGINE_HOST_TIMEZONE:-}" ]]; then
    printf '%s\n' "${BOREALIS_ENGINE_HOST_TIMEZONE}"
    return 0
  fi
  if command_exists timedatectl; then
    timezone="$(timedatectl show --property=Timezone --value 2>/dev/null | head -n 1 || true)"
    timezone="$(printf '%s' "${timezone}" | tr -d '\r' | xargs)"
    if [[ -n "${timezone}" ]]; then
      printf '%s\n' "${timezone}"
      return 0
    fi
  fi
  local localtime_path=""
  localtime_path="$(readlink -f /etc/localtime 2>/dev/null || true)"
  if [[ "${localtime_path}" == *"/zoneinfo/"* ]]; then
    timezone="${localtime_path#*/zoneinfo/}"
    if [[ -n "${timezone}" ]]; then
      printf '%s\n' "${timezone}"
      return 0
    fi
  fi
  if [[ -n "${TZ:-}" ]]; then
    printf '%s\n' "${TZ}"
    return 0
  fi
  if [[ -r /etc/timezone ]]; then
    timezone="$(head -n 1 /etc/timezone 2>/dev/null | tr -d '\r' | xargs || true)"
    if [[ -n "${timezone}" ]]; then
      printf '%s\n' "${timezone}"
      return 0
    fi
  fi
  printf '%s\n' "Etc/UTC"
}

resolve_traefik_trusted_proxy_ips() {
  local engine_profile="${1:-${ENGINE_DEPLOYMENT_PROFILE:-}}"
  engine_profile="$(normalize_engine_deployment_profile "${engine_profile}")"
  local existing
  existing="$(read_env_value BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS)"
  if [[ -n "${BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS:-}" ]]; then
    printf '%s\n' "${BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS}"
  elif [[ "${engine_profile}" == "internal-only" ]]; then
    printf '%s\n' "${existing}"
  elif env_key_exists BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS; then
    printf '%s\n' "${existing}"
  elif [[ -t 0 ]]; then
    local input=""
    printf '%s\n' "External reverse proxy? Set BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS so client IPs survive Traefik." >&2
    printf '%s\n' "Use outer proxy IP/CIDR only. Leave blank when Borealis is directly internet-facing." >&2
    printf '%s\n' "HTTPS passthrough also needs PROXY protocol enabled on the outer TCP service." >&2
    read -r -p "Outer reverse proxy IP/CIDR [blank for none]: " input || true
    printf '%s\n' "${input}"
  else
    printf '%s\n' ""
  fi
}

write_webui_mode_env_file() {
  local target="$1"
  local mode="$2"
  awk -v mode="${mode}" '
    BEGIN { wrote = 0 }
    /^BOREALIS_WEBUI_MODE=/ {
      print "BOREALIS_WEBUI_MODE=" mode
      wrote = 1
      next
    }
    { print }
    END {
      if (!wrote) {
        print "BOREALIS_WEBUI_MODE=" mode
      }
    }
  ' "${RUNTIME_ENV}" > "${target}"
}

clamp_mib() {
  local value="$1"
  local minimum="$2"
  local maximum="$3"
  if (( value < minimum )); then
    printf '%s\n' "${minimum}"
    return 0
  fi
  if (( value > maximum )); then
    printf '%s\n' "${maximum}"
    return 0
  fi
  printf '%s\n' "${value}"
}

format_pg_memory_mib() {
  local mib="$1"
  if (( mib >= 1024 && mib % 1024 == 0 )); then
    printf '%sGB\n' "$((mib / 1024))"
    return 0
  fi
  printf '%sMB\n' "${mib}"
}

format_docker_memory_mib() {
  local mib="$1"
  if (( mib >= 1024 && mib % 1024 == 0 )); then
    printf '%sg\n' "$((mib / 1024))"
    return 0
  fi
  printf '%sm\n' "${mib}"
}

detect_host_vcpu() {
  local count=""
  if command_exists nproc; then
    count="$(nproc 2>/dev/null || true)"
  fi
  if [[ -z "${count}" ]] && command_exists getconf; then
    count="$(getconf _NPROCESSORS_ONLN 2>/dev/null || true)"
  fi
  [[ "${count}" =~ ^[0-9]+$ && "${count}" -gt 0 ]] || count="1"
  printf '%s\n' "${count}"
}

detect_host_memory_mib() {
  local mem_mib=""
  if [[ -r /proc/meminfo ]]; then
    mem_mib="$(awk '/^MemTotal:/ {printf "%d", $2 / 1024}' /proc/meminfo 2>/dev/null || true)"
  fi
  [[ "${mem_mib}" =~ ^[0-9]+$ && "${mem_mib}" -gt 0 ]] || mem_mib="1024"
  printf '%s\n' "${mem_mib}"
}

profile_rank_for_cpu() {
  local vcpu="$1"
  if (( vcpu >= 24 )); then
    printf '3\n'
  elif (( vcpu >= 16 )); then
    printf '2\n'
  elif (( vcpu >= 8 )); then
    printf '1\n'
  else
    printf '0\n'
  fi
}

profile_rank_for_memory() {
  local mem_mib="$1"
  if (( mem_mib >= 65536 )); then
    printf '3\n'
  elif (( mem_mib >= 32768 )); then
    printf '2\n'
  elif (( mem_mib >= 16384 )); then
    printf '1\n'
  else
    printf '0\n'
  fi
}

profile_name_for_rank() {
  case "$1" in
    3) printf '%s\n' "Enterprise" ;;
    2) printf '%s\n' "MSP / Production" ;;
    1) printf '%s\n' "Small Business" ;;
    *) printf '%s\n' "Homelab" ;;
  esac
}

load_profile_tuning() {
  local vcpu="$1"
  local mem_mib="$2"
  local cpu_rank
  local memory_rank
  local profile_rank
  cpu_rank="$(profile_rank_for_cpu "${vcpu}")"
  memory_rank="$(profile_rank_for_memory "${mem_mib}")"
  profile_rank="${cpu_rank}"
  if (( memory_rank < profile_rank )); then
    profile_rank="${memory_rank}"
  fi

  PROFILE_RANK="${profile_rank}"
  PROFILE_NAME="$(profile_name_for_rank "${profile_rank}")"
  PROFILE_CPU_RANK="${cpu_rank}"
  PROFILE_MEMORY_RANK="${memory_rank}"
  PROFILE_HOST_VCPU="${vcpu}"
  PROFILE_HOST_MEMORY_MIB="${mem_mib}"
  PROFILE_HOST_MEMORY_GIB="$(awk -v mib="${mem_mib}" 'BEGIN { printf "%.1f", mib / 1024 }')"

  local shared_mib
  local cache_mib
  case "${profile_rank}" in
    3)
      PROFILE_DB_POOL_SIZE=24
      PROFILE_DB_MAX_OVERFLOW=24
      PROFILE_SITE_WORKER_CONCURRENCY=16
      PROFILE_POSTGRES_MAX_CONNECTIONS=180
      shared_mib="$(clamp_mib "$((mem_mib * 25 / 100))" 12288 24576)"
      cache_mib="$(clamp_mib "$((mem_mib * 625 / 1000))" 32768 65536)"
      PROFILE_POSTGRES_WORK_MEM="16MB"
      PROFILE_POSTGRES_MAINTENANCE_WORK_MEM="1GB"
      PROFILE_POSTGRES_MAX_WORKER_PROCESSES=16
      PROFILE_POSTGRES_MAX_PARALLEL_WORKERS=16
      PROFILE_POSTGRES_MAX_PARALLEL_WORKERS_PER_GATHER=4
      PROFILE_POSTGRES_AUTOVACUUM_MAX_WORKERS=6
      PROFILE_POSTGRES_AUTOVACUUM_VACUUM_COST_LIMIT=2500
      PROFILE_POSTGRES_AUTOVACUUM_NAPTIME="15s"
      PROFILE_POSTGRES_MAX_WAL_SIZE="12GB"
      PROFILE_POSTGRES_MIN_WAL_SIZE="2GB"
      PROFILE_POSTGRES_EFFECTIVE_IO_CONCURRENCY=64
      ;;
    2)
      PROFILE_DB_POOL_SIZE=20
      PROFILE_DB_MAX_OVERFLOW=20
      PROFILE_SITE_WORKER_CONCURRENCY=12
      PROFILE_POSTGRES_MAX_CONNECTIONS=150
      shared_mib="$(clamp_mib "$((mem_mib * 25 / 100))" 8192 16384)"
      cache_mib="$(clamp_mib "$((mem_mib * 625 / 1000))" 20480 32768)"
      PROFILE_POSTGRES_WORK_MEM="8MB"
      PROFILE_POSTGRES_MAINTENANCE_WORK_MEM="512MB"
      PROFILE_POSTGRES_MAX_WORKER_PROCESSES=12
      PROFILE_POSTGRES_MAX_PARALLEL_WORKERS=12
      PROFILE_POSTGRES_MAX_PARALLEL_WORKERS_PER_GATHER=4
      PROFILE_POSTGRES_AUTOVACUUM_MAX_WORKERS=5
      PROFILE_POSTGRES_AUTOVACUUM_VACUUM_COST_LIMIT=2000
      PROFILE_POSTGRES_AUTOVACUUM_NAPTIME="15s"
      PROFILE_POSTGRES_MAX_WAL_SIZE="8GB"
      PROFILE_POSTGRES_MIN_WAL_SIZE="1GB"
      PROFILE_POSTGRES_EFFECTIVE_IO_CONCURRENCY=64
      ;;
    1)
      PROFILE_DB_POOL_SIZE=12
      PROFILE_DB_MAX_OVERFLOW=16
      PROFILE_SITE_WORKER_CONCURRENCY=8
      PROFILE_POSTGRES_MAX_CONNECTIONS=120
      shared_mib="$(clamp_mib "$((mem_mib * 25 / 100))" 4096 8192)"
      cache_mib="$(clamp_mib "$((mem_mib * 625 / 1000))" 8192 16384)"
      PROFILE_POSTGRES_WORK_MEM="8MB"
      PROFILE_POSTGRES_MAINTENANCE_WORK_MEM="512MB"
      PROFILE_POSTGRES_MAX_WORKER_PROCESSES=8
      PROFILE_POSTGRES_MAX_PARALLEL_WORKERS=8
      PROFILE_POSTGRES_MAX_PARALLEL_WORKERS_PER_GATHER=2
      PROFILE_POSTGRES_AUTOVACUUM_MAX_WORKERS=4
      PROFILE_POSTGRES_AUTOVACUUM_VACUUM_COST_LIMIT=1500
      PROFILE_POSTGRES_AUTOVACUUM_NAPTIME="20s"
      PROFILE_POSTGRES_MAX_WAL_SIZE="6GB"
      PROFILE_POSTGRES_MIN_WAL_SIZE="1GB"
      PROFILE_POSTGRES_EFFECTIVE_IO_CONCURRENCY=32
      ;;
    *)
      PROFILE_DB_POOL_SIZE=10
      PROFILE_DB_MAX_OVERFLOW=10
      PROFILE_SITE_WORKER_CONCURRENCY=5
      PROFILE_POSTGRES_MAX_CONNECTIONS=80
      shared_mib="$(clamp_mib "$((mem_mib * 25 / 100))" 1024 4096)"
      cache_mib="$(clamp_mib "$((mem_mib * 625 / 1000))" 4096 12288)"
      PROFILE_POSTGRES_WORK_MEM="4MB"
      PROFILE_POSTGRES_MAINTENANCE_WORK_MEM="256MB"
      PROFILE_POSTGRES_MAX_WORKER_PROCESSES=8
      PROFILE_POSTGRES_MAX_PARALLEL_WORKERS=8
      PROFILE_POSTGRES_MAX_PARALLEL_WORKERS_PER_GATHER=2
      PROFILE_POSTGRES_AUTOVACUUM_MAX_WORKERS=3
      PROFILE_POSTGRES_AUTOVACUUM_VACUUM_COST_LIMIT=1000
      PROFILE_POSTGRES_AUTOVACUUM_NAPTIME="30s"
      PROFILE_POSTGRES_MAX_WAL_SIZE="4GB"
      PROFILE_POSTGRES_MIN_WAL_SIZE="512MB"
      PROFILE_POSTGRES_EFFECTIVE_IO_CONCURRENCY=16
      ;;
  esac

  local postgres_extra_mib
  case "${profile_rank}" in
    3)
      postgres_extra_mib=8192
      PROFILE_API_BACKEND_MEMORY_LIMIT="3g"
      PROFILE_API_BACKEND_CPU_LIMIT="6.00"
      PROFILE_API_BACKEND_PIDS_LIMIT=512
      PROFILE_JOB_SCHEDULER_MEMORY_LIMIT="2g"
      PROFILE_JOB_SCHEDULER_CPU_LIMIT="3.00"
      PROFILE_JOB_SCHEDULER_PIDS_LIMIT=512
      PROFILE_SITE_WORKER_MEMORY_LIMIT="512m"
      PROFILE_SITE_WORKER_CPU_LIMIT="2.00"
      PROFILE_SITE_WORKER_PIDS_LIMIT=128
      PROFILE_WEBUI_FRONTEND_MEMORY_LIMIT="1g"
      PROFILE_WEBUI_FRONTEND_CPU_LIMIT="1.50"
      PROFILE_WEBUI_FRONTEND_DEV_MEMORY_LIMIT="3g"
      PROFILE_WEBUI_FRONTEND_DEV_CPU_LIMIT="4.00"
      PROFILE_WEBUI_FRONTEND_PIDS_LIMIT=384
      PROFILE_TRAEFIK_EDGE_MEMORY_LIMIT="2g"
      PROFILE_TRAEFIK_EDGE_CPU_LIMIT="4.00"
      PROFILE_TRAEFIK_EDGE_PIDS_LIMIT=384
      PROFILE_POSTGRES_DB_CPU_LIMIT="6.00"
      PROFILE_POSTGRES_DB_PIDS_LIMIT=512
      PROFILE_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT="2g"
      PROFILE_REMOTE_DESKTOP_GUACD_CPU_LIMIT="4.00"
      PROFILE_REMOTE_DESKTOP_GUACD_PIDS_LIMIT=384
      PROFILE_WIREGUARD_TUNNEL_MEMORY_LIMIT="1g"
      PROFILE_WIREGUARD_TUNNEL_CPU_LIMIT="2.00"
      PROFILE_WIREGUARD_TUNNEL_PIDS_LIMIT=64
      ;;
    2)
      postgres_extra_mib=4096
      PROFILE_API_BACKEND_MEMORY_LIMIT="2g"
      PROFILE_API_BACKEND_CPU_LIMIT="4.00"
      PROFILE_API_BACKEND_PIDS_LIMIT=384
      PROFILE_JOB_SCHEDULER_MEMORY_LIMIT="1g"
      PROFILE_JOB_SCHEDULER_CPU_LIMIT="2.00"
      PROFILE_JOB_SCHEDULER_PIDS_LIMIT=384
      PROFILE_SITE_WORKER_MEMORY_LIMIT="512m"
      PROFILE_SITE_WORKER_CPU_LIMIT="1.50"
      PROFILE_SITE_WORKER_PIDS_LIMIT=128
      PROFILE_WEBUI_FRONTEND_MEMORY_LIMIT="768m"
      PROFILE_WEBUI_FRONTEND_CPU_LIMIT="1.00"
      PROFILE_WEBUI_FRONTEND_DEV_MEMORY_LIMIT="2g"
      PROFILE_WEBUI_FRONTEND_DEV_CPU_LIMIT="3.00"
      PROFILE_WEBUI_FRONTEND_PIDS_LIMIT=256
      PROFILE_TRAEFIK_EDGE_MEMORY_LIMIT="1g"
      PROFILE_TRAEFIK_EDGE_CPU_LIMIT="2.00"
      PROFILE_TRAEFIK_EDGE_PIDS_LIMIT=256
      PROFILE_POSTGRES_DB_CPU_LIMIT="4.00"
      PROFILE_POSTGRES_DB_PIDS_LIMIT=384
      PROFILE_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT="1g"
      PROFILE_REMOTE_DESKTOP_GUACD_CPU_LIMIT="2.00"
      PROFILE_REMOTE_DESKTOP_GUACD_PIDS_LIMIT=256
      PROFILE_WIREGUARD_TUNNEL_MEMORY_LIMIT="512m"
      PROFILE_WIREGUARD_TUNNEL_CPU_LIMIT="2.00"
      PROFILE_WIREGUARD_TUNNEL_PIDS_LIMIT=64
      ;;
    1)
      postgres_extra_mib=2048
      PROFILE_API_BACKEND_MEMORY_LIMIT="1g"
      PROFILE_API_BACKEND_CPU_LIMIT="2.00"
      PROFILE_API_BACKEND_PIDS_LIMIT=256
      PROFILE_JOB_SCHEDULER_MEMORY_LIMIT="512m"
      PROFILE_JOB_SCHEDULER_CPU_LIMIT="1.50"
      PROFILE_JOB_SCHEDULER_PIDS_LIMIT=256
      PROFILE_SITE_WORKER_MEMORY_LIMIT="384m"
      PROFILE_SITE_WORKER_CPU_LIMIT="1.00"
      PROFILE_SITE_WORKER_PIDS_LIMIT=128
      PROFILE_WEBUI_FRONTEND_MEMORY_LIMIT="512m"
      PROFILE_WEBUI_FRONTEND_CPU_LIMIT="1.00"
      PROFILE_WEBUI_FRONTEND_DEV_MEMORY_LIMIT="1536m"
      PROFILE_WEBUI_FRONTEND_DEV_CPU_LIMIT="2.00"
      PROFILE_WEBUI_FRONTEND_PIDS_LIMIT=192
      PROFILE_TRAEFIK_EDGE_MEMORY_LIMIT="512m"
      PROFILE_TRAEFIK_EDGE_CPU_LIMIT="1.50"
      PROFILE_TRAEFIK_EDGE_PIDS_LIMIT=192
      PROFILE_POSTGRES_DB_CPU_LIMIT="2.00"
      PROFILE_POSTGRES_DB_PIDS_LIMIT=256
      PROFILE_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT="512m"
      PROFILE_REMOTE_DESKTOP_GUACD_CPU_LIMIT="1.00"
      PROFILE_REMOTE_DESKTOP_GUACD_PIDS_LIMIT=128
      PROFILE_WIREGUARD_TUNNEL_MEMORY_LIMIT="256m"
      PROFILE_WIREGUARD_TUNNEL_CPU_LIMIT="1.00"
      PROFILE_WIREGUARD_TUNNEL_PIDS_LIMIT=64
      ;;
    *)
      postgres_extra_mib=512
      PROFILE_API_BACKEND_MEMORY_LIMIT="512m"
      PROFILE_API_BACKEND_CPU_LIMIT="1.50"
      PROFILE_API_BACKEND_PIDS_LIMIT=160
      PROFILE_JOB_SCHEDULER_MEMORY_LIMIT="256m"
      PROFILE_JOB_SCHEDULER_CPU_LIMIT="1.00"
      PROFILE_JOB_SCHEDULER_PIDS_LIMIT=160
      PROFILE_SITE_WORKER_MEMORY_LIMIT="256m"
      PROFILE_SITE_WORKER_CPU_LIMIT="1.00"
      PROFILE_SITE_WORKER_PIDS_LIMIT=128
      PROFILE_WEBUI_FRONTEND_MEMORY_LIMIT="256m"
      PROFILE_WEBUI_FRONTEND_CPU_LIMIT="0.50"
      PROFILE_WEBUI_FRONTEND_DEV_MEMORY_LIMIT="1g"
      PROFILE_WEBUI_FRONTEND_DEV_CPU_LIMIT="2.00"
      PROFILE_WEBUI_FRONTEND_PIDS_LIMIT=128
      PROFILE_TRAEFIK_EDGE_MEMORY_LIMIT="256m"
      PROFILE_TRAEFIK_EDGE_CPU_LIMIT="1.00"
      PROFILE_TRAEFIK_EDGE_PIDS_LIMIT=128
      PROFILE_POSTGRES_DB_CPU_LIMIT="1.50"
      PROFILE_POSTGRES_DB_PIDS_LIMIT=256
      PROFILE_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT="256m"
      PROFILE_REMOTE_DESKTOP_GUACD_CPU_LIMIT="1.00"
      PROFILE_REMOTE_DESKTOP_GUACD_PIDS_LIMIT=64
      PROFILE_WIREGUARD_TUNNEL_MEMORY_LIMIT="128m"
      PROFILE_WIREGUARD_TUNNEL_CPU_LIMIT="1.00"
      PROFILE_WIREGUARD_TUNNEL_PIDS_LIMIT=64
      ;;
  esac

  PROFILE_POSTGRES_DB_MEMORY_LIMIT="$(format_docker_memory_mib "$((shared_mib + postgres_extra_mib))")"
  PROFILE_POSTGRES_SHARED_BUFFERS="$(format_pg_memory_mib "${shared_mib}")"
  PROFILE_POSTGRES_EFFECTIVE_CACHE_SIZE="$(format_pg_memory_mib "${cache_mib}")"
  PROFILE_POSTGRES_AUTOVACUUM_VACUUM_SCALE_FACTOR="0.02"
  PROFILE_POSTGRES_AUTOVACUUM_ANALYZE_SCALE_FACTOR="0.01"
  PROFILE_POSTGRES_WAL_COMPRESSION="on"
  PROFILE_POSTGRES_CHECKPOINT_TIMEOUT="15min"
  PROFILE_POSTGRES_CHECKPOINT_COMPLETION_TARGET="0.9"
  PROFILE_POSTGRES_RANDOM_PAGE_COST="1.1"
}

write_compose_env() {
  local mode="$1"
  local public_host="$2"
  local acme_email="$3"
  local trusted_proxy_ips_arg="${4-}"
  local engine_profile="${5:-${ENGINE_DEPLOYMENT_PROFILE}}"
  local fqdn_aliases="${6:-${public_host}}"
  engine_profile="$(normalize_engine_deployment_profile "${engine_profile}")"
  local engine_network_mode
  local engine_network_mode_label
  local engine_profile_label
  engine_network_mode="$(engine_network_mode_from_deployment_profile "${engine_profile}")"
  engine_network_mode_label="$(engine_network_mode_display_label "${engine_network_mode}")"
  engine_profile_label="$(engine_deployment_profile_label "${engine_profile}")"
  local postgres_password
  postgres_password="$(read_env_value POSTGRES_PASSWORD)"
  [[ -n "${postgres_password}" && "${postgres_password}" != "change-me" ]] || postgres_password="$(generate_secret)"
  local operator_secret
  operator_secret="$(read_env_value BOREALIS_OPERATOR_SECRET)"
  [[ -n "${operator_secret}" ]] || operator_secret="$(generate_secret)"
  local operator_base_url
  operator_base_url="$(resolve_borealis_operator_base_url)"

  local db_name
  local db_user
  db_name="$(read_env_value POSTGRES_DB)"
  db_user="$(read_env_value POSTGRES_USER)"
  db_name="${db_name:-borealis}"
  db_user="${db_user:-borealis}"
  local engine_source_build_id
  local engine_source_branch
  engine_source_build_id="$(git -C "${SCRIPT_DIR}" rev-parse HEAD 2>/dev/null || printf 'dev')"
  engine_source_branch="$(git -C "${SCRIPT_DIR}" branch --show-current 2>/dev/null || printf '')"
  engine_source_branch="${engine_source_branch:-main}"

  local public_base_url="https://${public_host}"
  if [[ "${public_host}" == *":443" ]]; then
    public_base_url="https://${public_host%:443}"
  fi
  local traefik_trusted_proxy_ips
  local traefik_forwarded_headers_trusted_ips
  local traefik_proxy_protocol_trusted_ips
  local runtime_owner_uid
  local runtime_owner_gid
  local postgres_runtime_uid
  local postgres_runtime_gid
  local host_timezone
  local webui_memory_limit
  local webui_cpu_limit
  local webui_traffic_owner
  local webui_upstream_host
  local api_backend_traffic_owner
  local api_backend_upstream_host
  local api_backend_upstream_port
  local postgres_traffic_owner
  local postgres_storage_class
  local postgres_database_url
  local internal_api_base_url
  local engine_ip_fallback=""
  local local_ca_enabled=0
  local local_ca_cert=""
  local local_ca_key=""
  local local_tls_cert=""
  local local_tls_key=""
  local local_ca_b64=""
  engine_ip_fallback="$(resolve_engine_ip_fallback "${engine_profile}")"
  if [[ "${engine_profile}" == "internal-only" ]]; then
    local_ca_enabled=1
    local_ca_cert="$(local_ca_cert_path)"
    local_ca_key="$(local_ca_key_path)"
    local_tls_cert="$(local_tls_cert_path)"
    local_tls_key="$(local_tls_key_path)"
    local_ca_b64="$(local_ca_cert_b64)"
  fi
  if (($# >= 4)); then
    traefik_trusted_proxy_ips="${trusted_proxy_ips_arg}"
  else
    traefik_trusted_proxy_ips="${BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS:-$(read_env_value BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS)}"
  fi
  traefik_forwarded_headers_trusted_ips="${BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS:-$(read_env_value BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS)}"
  traefik_proxy_protocol_trusted_ips="${BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS:-$(read_env_value BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS)}"
  host_timezone="$(resolve_host_timezone)"
  runtime_owner_uid="$(resolve_runtime_owner_uid)"
  runtime_owner_gid="$(resolve_runtime_owner_gid)"
  validate_numeric_id "BOREALIS_ENGINE_RUNTIME_OWNER_UID" "${runtime_owner_uid}"
  validate_numeric_id "BOREALIS_ENGINE_RUNTIME_OWNER_GID" "${runtime_owner_gid}"
  postgres_runtime_uid="$(resolve_postgres_runtime_uid)"
  postgres_runtime_gid="$(resolve_postgres_runtime_gid)"
  validate_numeric_id "BOREALIS_POSTGRES_RUNTIME_UID" "${postgres_runtime_uid}"
  validate_numeric_id "BOREALIS_POSTGRES_RUNTIME_GID" "${postgres_runtime_gid}"
  load_profile_tuning "$(detect_host_vcpu)" "$(detect_host_memory_mib)"
  webui_memory_limit="${PROFILE_WEBUI_FRONTEND_MEMORY_LIMIT}"
  webui_cpu_limit="${PROFILE_WEBUI_FRONTEND_CPU_LIMIT}"
  if [[ "${mode}" == "dev" ]]; then
    webui_memory_limit="${PROFILE_WEBUI_FRONTEND_DEV_MEMORY_LIMIT}"
    webui_cpu_limit="${PROFILE_WEBUI_FRONTEND_DEV_CPU_LIMIT}"
  fi
  webui_traffic_owner="$(resolve_webui_traffic_owner "${mode}")"
  webui_upstream_host="$(resolve_webui_upstream_host "${webui_traffic_owner}")"
  api_backend_traffic_owner="$(resolve_api_backend_traffic_owner)"
  api_backend_upstream_host="$(resolve_api_backend_upstream_host "${api_backend_traffic_owner}")"
  api_backend_upstream_port="$(resolve_api_backend_upstream_port "${api_backend_traffic_owner}")"
  postgres_traffic_owner="$(resolve_postgres_traffic_owner)"
  postgres_storage_class="$(resolve_k3s_postgres_storage_class)"
  local existing_cnpg_database_url=""
  existing_cnpg_database_url="$(runtime_cnpg_database_url || true)"
  if [[ -n "${existing_cnpg_database_url}" ]]; then
    verify_cnpg_cutover_runtime "${existing_cnpg_database_url}"
    postgres_database_url="${existing_cnpg_database_url}"
  else
    postgres_database_url="$(postgres_database_url_for_owner "${postgres_traffic_owner}" "${db_user}" "${postgres_password}" "${db_name}")"
  fi
  internal_api_base_url="http://${api_backend_upstream_host}:${api_backend_upstream_port}"
  if [[ "${BOREALIS_SUPPRESS_DEPLOYMENT_PROFILE_LOG:-0}" != "1" ]]; then
    log_status "Profile" "${PROFILE_NAME} (${PROFILE_HOST_VCPU} vCPU, ${PROFILE_HOST_MEMORY_GIB} GiB RAM, ${PROFILE_SITE_WORKER_CONCURRENCY} site-worker tasks)" "${C_BLUE}"
  fi

  cat > "${RUNTIME_ENV}" <<EOF
BOREALIS_PROJECT_ROOT=${ENGINE_HOST_ROOT}
BOREALIS_ENGINE_SOURCE_BUILD_ID=${engine_source_build_id}
BOREALIS_ENGINE_SOURCE_BRANCH=${engine_source_branch}
BOREALIS_COMPOSE_PROJECT_NAME=${PROJECT_NAME}
BOREALIS_RUNTIME_ENV_FILE=${RUNTIME_ENV}
BOREALIS_WEBUI_ENV_FILE=${WEBUI_ENV}
BOREALIS_ENGINE_RUNTIME_USER=${ENGINE_RUNTIME_USER}
BOREALIS_ENGINE_RUNTIME_GROUP=${ENGINE_RUNTIME_GROUP}
BOREALIS_ENGINE_RUNTIME_OWNER_UID=${runtime_owner_uid}
BOREALIS_ENGINE_RUNTIME_OWNER_GID=${runtime_owner_gid}
BOREALIS_POSTGRES_RUNTIME_UID=${postgres_runtime_uid}
BOREALIS_POSTGRES_RUNTIME_GID=${postgres_runtime_gid}
BOREALIS_ENGINE_MODE=production
BOREALIS_WEBUI_MODE=prod
BOREALIS_ENGINE_HOST_TIMEZONE=${host_timezone}
TZ=${host_timezone}
BOREALIS_WEBUI_TRAFFIC_OWNER=${webui_traffic_owner}
BOREALIS_WEBUI_RUNTIME_OWNER=k3s
BOREALIS_WEBUI_UPSTREAM_HOST=${webui_upstream_host}
BOREALIS_WEBUI_UPSTREAM_PORT=${BOREALIS_WEBUI_UPSTREAM_PORT:-8000}
BOREALIS_WEBUI_RUNTIME_SOURCE_DIR=${WEBUI_RUNTIME_SOURCE_DIR}
BOREALIS_PUBLIC_HOSTNAME=${public_host}
BOREALIS_PUBLIC_EDGE_ENABLED=1
BOREALIS_PUBLIC_BASE_URL=${public_base_url}
BOREALIS_PUBLIC_HTTPS_PORT=443
BOREALIS_PUBLIC_HTTP_PORT=80
BOREALIS_PUBLIC_VNC_PATH=/remote-desktop/vnc
BOREALIS_PUBLIC_WIREGUARD_HOST=${public_host}
BOREALIS_PUBLIC_WIREGUARD_PORT=30000
BOREALIS_PUBLIC_HOSTNAME_ALIASES=${fqdn_aliases}
BOREALIS_ENGINE_IP_FALLBACK=${engine_ip_fallback}
BOREALIS_ENGINE_NETWORK_MODE=${engine_network_mode}
BOREALIS_ENGINE_NETWORK_MODE_LABEL=${engine_network_mode_label}
BOREALIS_ENGINE_DEPLOYMENT_PROFILE=${engine_profile}
BOREALIS_ENGINE_DEPLOYMENT_PROFILE_LABEL=${engine_profile_label}
BOREALIS_OPERATOR_NAMESPACE=${K3S_NAMESPACE}
BOREALIS_OPERATOR_SERVICE_NAME=${BOREALIS_OPERATOR_SERVICE_NAME}
BOREALIS_OPERATOR_PORT=${BOREALIS_OPERATOR_PORT}
BOREALIS_OPERATOR_BASE_URL=${operator_base_url}
BOREALIS_OPERATOR_SECRET=${operator_secret}
BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME=${BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME}
BOREALIS_POSTGRES_RUNTIME_SECRET_NAME=${BOREALIS_POSTGRES_RUNTIME_SECRET_NAME}
BOREALIS_WIREGUARD_TUNNEL_RUNTIME_SECRET_NAME=${BOREALIS_WIREGUARD_TUNNEL_RUNTIME_SECRET_NAME}
BOREALIS_K3S_PVC_STORAGE_CLASS=${postgres_storage_class}
BOREALIS_K3S_POSTGRES_ENABLED=$(normalize_enabled_flag "BOREALIS_K3S_POSTGRES_ENABLED" "${K3S_POSTGRES_ENABLED}")
BOREALIS_K3S_POSTGRES_STORAGE_SIZE=${K3S_POSTGRES_STORAGE_SIZE}
BOREALIS_K3S_POSTGRES_ROLLOUT_TIMEOUT=${K3S_POSTGRES_ROLLOUT_TIMEOUT}
BOREALIS_K3S_PEER_CIDRS=${K3S_PEER_CIDRS}
BOREALIS_K3S_CONTAINER_LOG_MAX_SIZE=${K3S_CONTAINER_LOG_MAX_SIZE}
BOREALIS_K3S_CONTAINER_LOG_MAX_FILES=${K3S_CONTAINER_LOG_MAX_FILES}
BOREALIS_ACME_EMAIL=${acme_email}
BOREALIS_LETSENCRYPT_SETTINGS_PATH=${RUNTIME_ROOT}/Services/traefik-edge/state/Settings.json
BOREALIS_TRAEFIK_ACME_STORAGE_PATH=${RUNTIME_ROOT}/Services/traefik-edge/state/acme.json
BOREALIS_TRAEFIK_RUNTIME_ENV_PATH=${RUNTIME_ROOT}/Services/traefik-edge/env/runtime.env
BOREALIS_TRAEFIK_STATIC_CONFIG_PATH=${RUNTIME_ROOT}/Services/traefik-edge/config/traefik.yml
BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR=${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic
BOREALIS_TRAEFIK_DYNAMIC_CONFIG_PATH=${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic/core.yml
BOREALIS_TRAEFIK_HEALTH_PORT=${BOREALIS_TRAEFIK_HEALTH_PORT:-8082}
BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS=${traefik_trusted_proxy_ips}
BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS=${traefik_forwarded_headers_trusted_ips}
BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS=${traefik_proxy_protocol_trusted_ips}
BOREALIS_LOCAL_CA_ENABLED=${local_ca_enabled}
BOREALIS_LOCAL_CA_CERT_PATH=${local_ca_cert}
BOREALIS_LOCAL_CA_KEY_PATH=${local_ca_key}
BOREALIS_LOCAL_TLS_CERT_PATH=${local_tls_cert}
BOREALIS_LOCAL_TLS_KEY_PATH=${local_tls_key}
BOREALIS_AGENT_ENGINE_CA_PEM_B64=${local_ca_b64}

BOREALIS_DEPLOYMENT_PROFILE=${PROFILE_NAME}
BOREALIS_DEPLOYMENT_PROFILE_RANK=${PROFILE_RANK}
BOREALIS_DEPLOYMENT_CPU_RANK=${PROFILE_CPU_RANK}
BOREALIS_DEPLOYMENT_MEMORY_RANK=${PROFILE_MEMORY_RANK}
BOREALIS_DEPLOYMENT_HOST_VCPU=${PROFILE_HOST_VCPU}
BOREALIS_DEPLOYMENT_HOST_MEMORY_MIB=${PROFILE_HOST_MEMORY_MIB}
BOREALIS_DEPLOYMENT_HOST_MEMORY_GIB=${PROFILE_HOST_MEMORY_GIB}

BOREALIS_API_BACKEND_MEMORY_LIMIT=${BOREALIS_API_BACKEND_MEMORY_LIMIT:-${PROFILE_API_BACKEND_MEMORY_LIMIT}}
BOREALIS_API_BACKEND_CPU_LIMIT=${BOREALIS_API_BACKEND_CPU_LIMIT:-${PROFILE_API_BACKEND_CPU_LIMIT}}
BOREALIS_API_BACKEND_PIDS_LIMIT=${BOREALIS_API_BACKEND_PIDS_LIMIT:-${PROFILE_API_BACKEND_PIDS_LIMIT}}
BOREALIS_API_BACKEND_K3S_BRIDGE_PORT=${BOREALIS_API_BACKEND_K3S_BRIDGE_PORT}
BOREALIS_API_BACKEND_TRAFFIC_OWNER=${api_backend_traffic_owner}
BOREALIS_API_BACKEND_RUNTIME_OWNER=k3s
BOREALIS_API_BACKEND_UPSTREAM_HOST=${api_backend_upstream_host}
BOREALIS_API_BACKEND_UPSTREAM_PORT=${api_backend_upstream_port}
BOREALIS_JOB_SCHEDULER_MEMORY_LIMIT=${BOREALIS_JOB_SCHEDULER_MEMORY_LIMIT:-${PROFILE_JOB_SCHEDULER_MEMORY_LIMIT}}
BOREALIS_JOB_SCHEDULER_CPU_LIMIT=${BOREALIS_JOB_SCHEDULER_CPU_LIMIT:-${PROFILE_JOB_SCHEDULER_CPU_LIMIT}}
BOREALIS_JOB_SCHEDULER_PIDS_LIMIT=${BOREALIS_JOB_SCHEDULER_PIDS_LIMIT:-${PROFILE_JOB_SCHEDULER_PIDS_LIMIT}}
BOREALIS_JOB_SCHEDULER_RUNTIME_OWNER=k3s
BOREALIS_SITE_WORKER_MEMORY_LIMIT=${BOREALIS_SITE_WORKER_MEMORY_LIMIT:-${PROFILE_SITE_WORKER_MEMORY_LIMIT}}
BOREALIS_SITE_WORKER_CPU_LIMIT=${BOREALIS_SITE_WORKER_CPU_LIMIT:-${PROFILE_SITE_WORKER_CPU_LIMIT}}
BOREALIS_SITE_WORKER_PIDS_LIMIT=${BOREALIS_SITE_WORKER_PIDS_LIMIT:-${PROFILE_SITE_WORKER_PIDS_LIMIT}}
BOREALIS_WEBUI_FRONTEND_MEMORY_LIMIT=${BOREALIS_WEBUI_FRONTEND_MEMORY_LIMIT:-${webui_memory_limit}}
BOREALIS_WEBUI_FRONTEND_CPU_LIMIT=${BOREALIS_WEBUI_FRONTEND_CPU_LIMIT:-${webui_cpu_limit}}
BOREALIS_WEBUI_FRONTEND_PIDS_LIMIT=${BOREALIS_WEBUI_FRONTEND_PIDS_LIMIT:-${PROFILE_WEBUI_FRONTEND_PIDS_LIMIT}}
BOREALIS_TRAEFIK_EDGE_MEMORY_LIMIT=${BOREALIS_TRAEFIK_EDGE_MEMORY_LIMIT:-${PROFILE_TRAEFIK_EDGE_MEMORY_LIMIT}}
BOREALIS_TRAEFIK_EDGE_CPU_LIMIT=${BOREALIS_TRAEFIK_EDGE_CPU_LIMIT:-${PROFILE_TRAEFIK_EDGE_CPU_LIMIT}}
BOREALIS_TRAEFIK_EDGE_PIDS_LIMIT=${BOREALIS_TRAEFIK_EDGE_PIDS_LIMIT:-${PROFILE_TRAEFIK_EDGE_PIDS_LIMIT}}
BOREALIS_TRAEFIK_EDGE_RUNTIME_OWNER=k3s
BOREALIS_POSTGRES_DB_MEMORY_LIMIT=${BOREALIS_POSTGRES_DB_MEMORY_LIMIT:-${PROFILE_POSTGRES_DB_MEMORY_LIMIT}}
BOREALIS_POSTGRES_DB_CPU_LIMIT=${BOREALIS_POSTGRES_DB_CPU_LIMIT:-${PROFILE_POSTGRES_DB_CPU_LIMIT}}
BOREALIS_POSTGRES_DB_PIDS_LIMIT=${BOREALIS_POSTGRES_DB_PIDS_LIMIT:-${PROFILE_POSTGRES_DB_PIDS_LIMIT}}
BOREALIS_POSTGRES_TRAFFIC_OWNER=${postgres_traffic_owner}
BOREALIS_POSTGRES_RUNTIME_OWNER=k3s
BOREALIS_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT=${BOREALIS_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT:-${PROFILE_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT}}
BOREALIS_REMOTE_DESKTOP_GUACD_CPU_LIMIT=${BOREALIS_REMOTE_DESKTOP_GUACD_CPU_LIMIT:-${PROFILE_REMOTE_DESKTOP_GUACD_CPU_LIMIT}}
BOREALIS_REMOTE_DESKTOP_GUACD_PIDS_LIMIT=${BOREALIS_REMOTE_DESKTOP_GUACD_PIDS_LIMIT:-${PROFILE_REMOTE_DESKTOP_GUACD_PIDS_LIMIT}}
BOREALIS_REMOTE_DESKTOP_GUACD_RUNTIME_OWNER=k3s
BOREALIS_WIREGUARD_TUNNEL_MEMORY_LIMIT=${BOREALIS_WIREGUARD_TUNNEL_MEMORY_LIMIT:-${PROFILE_WIREGUARD_TUNNEL_MEMORY_LIMIT}}
BOREALIS_WIREGUARD_TUNNEL_CPU_LIMIT=${BOREALIS_WIREGUARD_TUNNEL_CPU_LIMIT:-${PROFILE_WIREGUARD_TUNNEL_CPU_LIMIT}}
BOREALIS_WIREGUARD_TUNNEL_PIDS_LIMIT=${BOREALIS_WIREGUARD_TUNNEL_PIDS_LIMIT:-${PROFILE_WIREGUARD_TUNNEL_PIDS_LIMIT}}
BOREALIS_WIREGUARD_TUNNEL_RUNTIME_OWNER=k3s

POSTGRES_DB=${db_name}
POSTGRES_USER=${db_user}
POSTGRES_PASSWORD=${postgres_password}
BOREALIS_DATABASE_URL=${postgres_database_url}
BOREALIS_DB_SSLMODE=disable
BOREALIS_DB_POOL_SIZE=${PROFILE_DB_POOL_SIZE}
BOREALIS_DB_MAX_OVERFLOW=${PROFILE_DB_MAX_OVERFLOW}
BOREALIS_DB_CONNECT_TIMEOUT=${BOREALIS_DB_CONNECT_TIMEOUT:-15}
BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS=${BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS:-60000}
BOREALIS_POSTGRES_MAX_CONNECTIONS=${PROFILE_POSTGRES_MAX_CONNECTIONS}
BOREALIS_POSTGRES_SHARED_BUFFERS=${PROFILE_POSTGRES_SHARED_BUFFERS}
BOREALIS_POSTGRES_EFFECTIVE_CACHE_SIZE=${PROFILE_POSTGRES_EFFECTIVE_CACHE_SIZE}
BOREALIS_POSTGRES_WORK_MEM=${PROFILE_POSTGRES_WORK_MEM}
BOREALIS_POSTGRES_MAINTENANCE_WORK_MEM=${PROFILE_POSTGRES_MAINTENANCE_WORK_MEM}
BOREALIS_POSTGRES_MAX_WORKER_PROCESSES=${PROFILE_POSTGRES_MAX_WORKER_PROCESSES}
BOREALIS_POSTGRES_MAX_PARALLEL_WORKERS=${PROFILE_POSTGRES_MAX_PARALLEL_WORKERS}
BOREALIS_POSTGRES_MAX_PARALLEL_WORKERS_PER_GATHER=${PROFILE_POSTGRES_MAX_PARALLEL_WORKERS_PER_GATHER}
BOREALIS_POSTGRES_AUTOVACUUM_MAX_WORKERS=${PROFILE_POSTGRES_AUTOVACUUM_MAX_WORKERS}
BOREALIS_POSTGRES_AUTOVACUUM_VACUUM_COST_LIMIT=${PROFILE_POSTGRES_AUTOVACUUM_VACUUM_COST_LIMIT}
BOREALIS_POSTGRES_AUTOVACUUM_NAPTIME=${PROFILE_POSTGRES_AUTOVACUUM_NAPTIME}
BOREALIS_POSTGRES_AUTOVACUUM_VACUUM_SCALE_FACTOR=${PROFILE_POSTGRES_AUTOVACUUM_VACUUM_SCALE_FACTOR}
BOREALIS_POSTGRES_AUTOVACUUM_ANALYZE_SCALE_FACTOR=${PROFILE_POSTGRES_AUTOVACUUM_ANALYZE_SCALE_FACTOR}
BOREALIS_POSTGRES_MAX_WAL_SIZE=${PROFILE_POSTGRES_MAX_WAL_SIZE}
BOREALIS_POSTGRES_MIN_WAL_SIZE=${PROFILE_POSTGRES_MIN_WAL_SIZE}
BOREALIS_POSTGRES_EFFECTIVE_IO_CONCURRENCY=${PROFILE_POSTGRES_EFFECTIVE_IO_CONCURRENCY}
BOREALIS_POSTGRES_WAL_COMPRESSION=${PROFILE_POSTGRES_WAL_COMPRESSION}
BOREALIS_POSTGRES_CHECKPOINT_TIMEOUT=${PROFILE_POSTGRES_CHECKPOINT_TIMEOUT}
BOREALIS_POSTGRES_CHECKPOINT_COMPLETION_TARGET=${PROFILE_POSTGRES_CHECKPOINT_COMPLETION_TARGET}
BOREALIS_POSTGRES_RANDOM_PAGE_COST=${PROFILE_POSTGRES_RANDOM_PAGE_COST}

BOREALIS_WEBUI_EXTERNAL=1
BOREALIS_ENGINE_CONTAINERIZED=1
BOREALIS_SCHEDULED_JOBS_START_LOOP=0
BOREALIS_INTERNAL_API_BASE_URL=${internal_api_base_url}
BOREALIS_COOKIE_SECURE=1
BOREALIS_GUACAMOLE_ENABLED=1
BOREALIS_GUACD_HOST=remote-desktop-guacd.${K3S_NAMESPACE}.svc.cluster.local
BOREALIS_GUACD_PORT=4822
BOREALIS_GUACAMOLE_VNC_WS_PATH=/remote-desktop/vnc/guacamole
BOREALIS_VNC_AUTH_PROBE=0
BOREALIS_VNC_WS_HOST=127.0.0.1
BOREALIS_VNC_WS_PORT=4823
BOREALIS_WIREGUARD_PORT=30000
BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP=10.255.0.1/32
BOREALIS_WIREGUARD_PEER_NETWORK=10.255.0.0/16
BOREALIS_WIREGUARD_PORT_ALLOWLIST=47002,5900,22
BOREALIS_WIREGUARD_CONFIG_ROOT=${RUNTIME_ROOT}/Services/wireguard-tunnel/config
BOREALIS_WIREGUARD_KEY_ROOT=${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets
BOREALIS_WIREGUARD_CONTROL_SOCKET=${RUNTIME_ROOT}/Services/wireguard-tunnel/run/control.sock
BOREALIS_SITE_WORKER_LIFECYCLE_MODE=${BOREALIS_SITE_WORKER_LIFECYCLE_MODE:-auto}
BOREALIS_ENGINE_SECRET_PATH=${RUNTIME_ROOT}/Services/api-backend/secrets/engine_secret.txt
BOREALIS_ENGINE_CERT_ROOT=${RUNTIME_ROOT}/Services/api-backend/secrets/Certificates
BOREALIS_ENGINE_AUTH_TOKEN_ROOT=${RUNTIME_ROOT}/Services/api-backend/secrets/Auth_Tokens
BOREALIS_ANSIBLE_RUNTIME_ROOT=${RUNTIME_ROOT}/Services/api-backend/cache/Ansible
BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH=${RUNTIME_ROOT}/Services/api-backend/config/ansible_runner_settings.json
BOREALIS_SITE_WORKER_SETTINGS_PATH=${RUNTIME_ROOT}/Services/api-backend/config/site_worker_settings.json
BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY=${PROFILE_SITE_WORKER_CONCURRENCY}
BOREALIS_SITE_WORKER_ANSIBLE_CONCURRENCY=${BOREALIS_SITE_WORKER_ANSIBLE_CONCURRENCY:-2}
BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE=${BOREALIS_SITE_WORKER_SOCKETIO_ASYNC_MODE:-eventlet}
BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS=${BOREALIS_SITE_WORKER_IDLE_TTL_SECONDS:-300}
BOREALIS_OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT=${RUNTIME_ROOT}/Services/api-backend/cache
BOREALIS_ENGINE_FILE_LOG_RETENTION_DAYS=${ENGINE_FILE_LOG_RETENTION_DAYS}
BOREALIS_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/engine.log
BOREALIS_ERROR_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/error.log
BOREALIS_API_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/api.log
BOREALIS_VPN_TUNNEL_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/VPN_Tunnel/tunnel.log
BOREALIS_WIREGUARD_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/VPN_Tunnel/tunnel.log
EOF

  write_webui_mode_env_file "${WEBUI_ENV}" "${mode}"

  cp "${RUNTIME_ENV}" "${COMPOSE_ENV}"
  cat >> "${COMPOSE_ENV}" <<EOF
BOREALIS_OPERATOR_IMAGE=${IMAGE_TAGS[borealis-operator]:-borealis-engine/borealis-operator:local}
BOREALIS_API_BACKEND_IMAGE=${IMAGE_TAGS[api-backend]:-borealis-engine/api-backend:local}
BOREALIS_JOB_SCHEDULER_IMAGE=${IMAGE_TAGS[job-scheduler]:-borealis-engine/job-scheduler:local}
BOREALIS_SITE_WORKER_IMAGE=${IMAGE_TAGS[site-worker]:-borealis-engine/site-worker:local}
BOREALIS_WEBUI_FRONTEND_IMAGE=${IMAGE_TAGS[webui-frontend]:-borealis-engine/webui-frontend:local}
BOREALIS_TRAEFIK_EDGE_IMAGE=${IMAGE_TAGS[traefik-edge]:-borealis-engine/traefik-edge:local}
BOREALIS_POSTGRES_DB_IMAGE=${IMAGE_TAGS[postgres-db]:-borealis-engine/postgres-db:local}
BOREALIS_REMOTE_DESKTOP_GUACD_IMAGE=${IMAGE_TAGS[remote-desktop-guacd]:-borealis-engine/remote-desktop-guacd:local}
BOREALIS_WIREGUARD_TUNNEL_IMAGE=${IMAGE_TAGS[wireguard-tunnel]:-borealis-engine/wireguard-tunnel:local}
EOF
  chmod 600 "${COMPOSE_ENV}" "${RUNTIME_ENV}" "${WEBUI_ENV}"
  apply_deploy_env_file_permissions
}

compute_service_hash() {
  local service="$1"
  local mode="$2"
  local legacy_mode_sensitive="${3:-0}"
  python3 - "${SCRIPT_DIR}" "${BUILD_MANIFEST}" "${service}" "${mode}" "${legacy_mode_sensitive}" <<'PY'
import hashlib
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
manifest = json.loads(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"))
service = sys.argv[3]
mode = sys.argv[4]
legacy_mode_sensitive = sys.argv[5] in {"1", "true", "legacy"}
entry = manifest["services"][service]
excluded_parts = {
    "__pycache__",
    ".pytest_cache",
    "node_modules",
    "build",
    "dist",
    "Unit_Test_Results",
}
excluded_suffixes = {".pyc", ".pyo", ".log"}
files = set()

def add_candidate(candidate):
    rel = candidate.relative_to(root)
    if any(part in excluded_parts for part in rel.parts):
        return
    if candidate.is_dir():
        for child in candidate.rglob("*"):
            add_candidate(child)
        return
    if not candidate.is_file():
        return
    if candidate.suffix in excluded_suffixes:
        return
    files.add(rel)

for pattern in entry.get("inputs", []):
    if pattern.endswith("/**"):
        base = root / pattern[:-3]
        if base.exists():
            add_candidate(base)
        continue
    for candidate in root.glob(pattern):
        add_candidate(candidate)
dockerfile = pathlib.Path(entry["dockerfile"])
files.add(dockerfile)
digest = hashlib.sha256()
targets = entry.get("targets") or {}
hash_mode = mode if (targets or legacy_mode_sensitive) else ""
digest.update(
    f"service={service}\nmode={hash_mode}\n"
    f"dockerfile={entry.get('dockerfile')}\n"
    f"context={entry.get('context')}\n"
    f"target={targets.get(mode) or ''}\n".encode("utf-8")
)
for rel in sorted(files, key=lambda p: str(p)):
    path = root / rel
    digest.update(str(rel).encode("utf-8") + b"\0")
    try:
        digest.update(path.read_bytes())
    except FileNotFoundError:
        continue
    digest.update(b"\0")
print(digest.hexdigest())
PY
}

manifest_field() {
  local service="$1"
  local field="$2"
  python3 - "${BUILD_MANIFEST}" "${service}" "${field}" <<'PY'
import json
import pathlib
import sys
manifest = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
print(manifest["services"][sys.argv[2]][sys.argv[3]])
PY
}

manifest_target() {
  local service="$1"
  local mode="$2"
  python3 - "${BUILD_MANIFEST}" "${service}" "${mode}" <<'PY'
import json
import pathlib
import sys
manifest = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
entry = manifest["services"][sys.argv[2]]
targets = entry.get("targets") or {}
print(targets.get(sys.argv[3]) or "")
PY
}

previous_image_hash() {
  local service="$1"
  [[ -f "${IMAGE_MANIFEST}" ]] || return 0
  python3 - "${IMAGE_MANIFEST}" "${service}" <<'PY'
import json
import pathlib
import sys
try:
    data = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
    print(((data.get("services") or {}).get(sys.argv[2]) or {}).get("hash") or "")
except Exception:
    print("")
PY
}

previous_image_tag() {
  local service="$1"
  [[ -f "${IMAGE_MANIFEST}" ]] || return 0
  python3 - "${IMAGE_MANIFEST}" "${service}" <<'PY'
import json
import pathlib
import sys
try:
    data = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
    print(((data.get("services") or {}).get(sys.argv[2]) or {}).get("image") or "")
except Exception:
    print("")
PY
}

is_build_role() {
  local candidate="$1"
  local service=""
  for service in "${BUILD_ROLES[@]}"; do
    if [[ "${candidate}" == "${service}" ]]; then
      return 0
    fi
  done
  return 1
}

build_cache_export_epoch() {
  local export_name="$1"
  local stamp="${export_name%%-*}"
  [[ "${stamp}" =~ ^[0-9]{8}T[0-9]{6}Z$ ]] || return 1
  date -u -d "${stamp:0:4}-${stamp:4:2}-${stamp:6:2} ${stamp:9:2}:${stamp:11:2}:${stamp:13:2} UTC" +%s 2>/dev/null
}

collect_build_cache_exports() {
  local service="$1"
  local cache_root="${DEPLOY_DIR}/cache/buildkit/${service}"
  [[ -d "${cache_root}" ]] || return 0
  find "${cache_root}" -mindepth 1 -maxdepth 1 -type d -name '????????T??????Z-*' -print | sort
  if [[ -d "${cache_root}/current" ]]; then
    printf '%s\n' "${cache_root}/current"
  fi
}

prepare_service_build_artifacts() {
  local service="$1"
  case "${service}" in
    api-backend|job-scheduler|borealis-operator|wireguard-tunnel)
      if [[ "${GO_API_BACKEND_BINARY_PREPARED}" == "1" ]]; then
        printf '[%s] %s reusing prepared Go api-backend binary\n' "$(date +%FT%T)" "${service}" >> "${BUILD_LOG}"
        return 0
      fi
      log_build_status "${service}" "Building Go binary" "${C_YELLOW}"
      BOREALIS_GO_API_BACKEND_OUTPUT_ROOT="${SCRIPT_DIR}/Data/Engine/Containers/api-backend/dist" \
        "${SCRIPT_DIR}/Data/Engine/Containers/api-backend/build-api-backend.sh" >> "${BUILD_LOG}" 2>&1
      GO_API_BACKEND_BINARY_PREPARED=1
      ;;
  esac
}

docker_build_with_cache_repair() {
  local service="$1"
  shift
  local -a docker_build_args=("$@")
  if DOCKER_BUILDKIT=1 docker build "${docker_build_args[@]}"; then
    return 0
  fi

  printf '[%s] Docker cached build failed for %s; pruning builder cache and retrying without cache\n' "$(date +%FT%T)" "${service}"
  if ! docker builder prune --all --force; then
    printf '[%s] Docker builder cache prune failed for %s; retrying without cache anyway\n' "$(date +%FT%T)" "${service}"
  fi
  DOCKER_BUILDKIT=1 docker build --no-cache "${docker_build_args[@]}"
}

build_service_image() {
  local service="$1"
  local mode="$2"
  local image_hash
  image_hash="$(compute_service_hash "${service}" "${mode}")"
  local tag="borealis-engine/${service}:sha-${image_hash:0:12}"
  local dockerfile
  local context
  local target
  dockerfile="$(manifest_field "${service}" dockerfile)"
  context="$(manifest_field "${service}" context)"
  target="$(manifest_target "${service}" "${mode}")"
  IMAGE_HASHES["${service}"]="${image_hash}"
  IMAGE_TAGS["${service}"]="${tag}"
  DOCKERFILES["${service}"]="${dockerfile}"
  BUILD_CONTEXTS["${service}"]="${context}"
  BUILD_STATUSES["${service}"]="unchanged"

  local previous
  local previous_tag
  previous="$(previous_image_hash "${service}")"
  previous_tag="$(previous_image_tag "${service}")"
  if [[ "${previous}" == "${image_hash}" ]] && docker image inspect "${tag}" >/dev/null 2>&1; then
    log_build_status "${service}" "Up-to-Date" "${C_GREEN}"
    printf '[%s] %s unchanged as %s; build skipped\n' "$(date +%FT%T)" "${service}" "${tag}" >> "${BUILD_LOG}"
    return 0
  fi
  if [[ -n "${previous}" && -n "${previous_tag}" && "${previous_tag}" != "${tag}" ]]; then
    local legacy_hash
    legacy_hash="$(compute_service_hash "${service}" "${mode}" "legacy")"
    if [[ "${previous}" == "${legacy_hash}" ]] && docker image inspect "${previous_tag}" >/dev/null 2>&1; then
      docker tag "${previous_tag}" "${tag}"
      log_build_status "${service}" "Up-to-Date" "${C_GREEN}"
      printf '[%s] %s unchanged after hash normalization; retagged %s as %s\n' "$(date +%FT%T)" "${service}" "${previous_tag}" "${tag}" >> "${BUILD_LOG}"
      return 0
    fi
  fi

  prepare_service_build_artifacts "${service}"
  local prepared_hash
  prepared_hash="$(compute_service_hash "${service}" "${mode}")"
  if [[ "${prepared_hash}" != "${image_hash}" ]]; then
    image_hash="${prepared_hash}"
    tag="borealis-engine/${service}:sha-${image_hash:0:12}"
    IMAGE_HASHES["${service}"]="${image_hash}"
    IMAGE_TAGS["${service}"]="${tag}"
    if [[ "${previous}" == "${image_hash}" ]] && docker image inspect "${tag}" >/dev/null 2>&1; then
      log_build_status "${service}" "Up-to-Date" "${C_GREEN}"
      printf '[%s] %s unchanged after artifact preparation as %s; build skipped\n' "$(date +%FT%T)" "${service}" "${tag}" >> "${BUILD_LOG}"
      return 0
    fi
  fi
  log_build_status "${service}" "(Re)Building Container Image" "${C_YELLOW}"
  {
    printf '[%s] Building %s as %s\n' "$(date +%FT%T)" "${service}" "${tag}"
    local build_args=(
      --label "org.opencontainers.image.title=Borealis ${service}" \
      --label "io.borealis.service=${service}" \
      --label "io.borealis.input-sha=${image_hash}" \
      -t "${tag}" \
      -f "${SCRIPT_DIR}/${dockerfile}"
    )
    if [[ -n "${target}" ]]; then
      build_args+=(--target "${target}")
    fi
    if docker buildx version >/dev/null 2>&1; then
      local cache_root="${DEPLOY_DIR}/cache/buildkit/${service}"
      mkdir -p "${cache_root}"
      local cache_stamp
      cache_stamp="$(date -u +%Y%m%dT%H%M%SZ)"
      local cache_export_name="${cache_stamp}-${image_hash:0:12}"
      local cache_next="${cache_root}/next-${cache_export_name}-$$"
      local cache_final="${cache_root}/${cache_export_name}"
      if [[ -e "${cache_final}" ]]; then
        cache_final="${cache_root}/${cache_export_name}-$$"
      fi
      rm -rf "${cache_next}"
      local buildx_args=(buildx build --load --progress=plain)
      local cache_sources=()
      mapfile -t cache_sources < <(collect_build_cache_exports "${service}")
      if ((${#cache_sources[@]} > 0)); then
        printf '[%s] %s using %d retained Buildx cache export(s)\n' "$(date +%FT%T)" "${service}" "${#cache_sources[@]}"
        local cache_source=""
        for cache_source in "${cache_sources[@]}"; do
          printf '[%s] %s cache-from %s\n' "$(date +%FT%T)" "${service}" "${cache_source}"
          buildx_args+=(--cache-from "type=local,src=${cache_source}")
        done
      else
        printf '[%s] %s has no retained Buildx cache exports\n' "$(date +%FT%T)" "${service}"
      fi
      buildx_args+=(--cache-to "type=local,dest=${cache_next},mode=max")
      if DOCKER_BUILDKIT=1 docker "${buildx_args[@]}" "${build_args[@]}" "${SCRIPT_DIR}/${context}"; then
        if [[ -d "${cache_next}" ]]; then
          rm -rf "${cache_final}"
          mv "${cache_next}" "${cache_final}"
          printf '[%s] %s stored full Buildx cache export %s\n' "$(date +%FT%T)" "${service}" "${cache_final}"
        fi
      else
        rm -rf "${cache_next}"
        printf '[%s] Buildx cache build failed for %s; falling back to docker build\n' "$(date +%FT%T)" "${service}"
        docker_build_with_cache_repair "${service}" "${build_args[@]}" "${SCRIPT_DIR}/${context}"
      fi
    else
      docker_build_with_cache_repair "${service}" "${build_args[@]}" "${SCRIPT_DIR}/${context}"
    fi
  } >> "${BUILD_LOG}" 2>&1
  BUILD_STATUSES["${service}"]="built"
  log_build_status "${service}" "Ready - Image (Re)Built" "${C_GREEN}"
}

build_images() {
  local mode="$1"
  shift || true
  local selected=("$@")
  if [[ "${#selected[@]}" -eq 0 ]]; then
    selected=("${BUILD_ROLES[@]}")
  fi
  local service=""
  if [[ "${BOREALIS_APPEND_BUILD_LOG:-0}" != "1" ]]; then
    : > "${BUILD_LOG}"
  fi
  GO_API_BACKEND_BINARY_PREPARED=0
  for service in "${selected[@]}"; do
    validate_build_role "${service}"
  done
  CURRENT_BUILD_SELECTION=("${selected[@]}")
  build_section_images "${mode}" "K3s Cluster Services" "${BUILD_SECTION_K3S_CLUSTER[@]}"
  build_section_images "${mode}" "Frontend Services" "${BUILD_SECTION_FRONTEND[@]}"
  build_section_images "${mode}" "Backend Services" "${BUILD_SECTION_BACKEND[@]}"
  build_section_images "${mode}" "Networking Services" "${BUILD_SECTION_NETWORKING[@]}"
  build_section_images "${mode}" "Database Services" "${BUILD_SECTION_DATABASE[@]}"
  CURRENT_BUILD_SELECTION=()
}

engine_docker_cleanup_enabled() {
  case "${BOREALIS_SKIP_DOCKER_PRUNE:-0}" in
    1|true|TRUE|yes|YES|on|ON)
      return 1
      ;;
  esac
  return 0
}

prune_stale_borealis_service_images_for_service() {
  local service="$1"
  local active_tag="${IMAGE_TAGS[${service}]:-}"
  [[ -n "${active_tag}" ]] || return 0
  local stale_images=()
  mapfile -t stale_images < <(
    docker image ls \
      --filter "label=io.borealis.service=${service}" \
      --format '{{.Repository}}:{{.Tag}}' \
      | awk -v active="${active_tag}" 'NF && $0 != active && $0 != "<none>:<none>" {print}'
  )
  ((${#stale_images[@]} > 0)) || return 0
  log_status "Docker Cleanup" "Pruning Stale ${service} Images" "${C_YELLOW}"
  local failed=0
  local image=""
  for image in "${stale_images[@]}"; do
    local referencing_containers=""
    referencing_containers="$(docker container ls -a --filter "ancestor=${image}" --format '{{.ID}}' 2>>"${BUILD_LOG}" || true)"
    if [[ -n "${referencing_containers}" ]]; then
      printf '[%s] Retaining stale %s image %s because container(s) still reference it: %s\n' \
        "$(date +%FT%T)" \
        "${service}" \
        "${image}" \
        "$(printf '%s' "${referencing_containers}" | tr '\n' ' ' | sed 's/[[:space:]]*$//')" \
        >> "${BUILD_LOG}"
      continue
    fi
    if ! docker image rm "${image}" >> "${BUILD_LOG}" 2>&1; then
      failed=1
      printf '[%s] Failed to remove stale %s image %s\n' "$(date +%FT%T)" "${service}" "${image}" >> "${BUILD_LOG}"
    fi
  done
  return "${failed}"
}

prune_stale_borealis_service_images() {
  local failed=0
  local service=""
  for service in "${BUILD_ROLES[@]}"; do
    if ! prune_stale_borealis_service_images_for_service "${service}"; then
      failed=1
    fi
  done
  return "${failed}"
}

restore_required_engine_images() {
  local mode="$1"
  local missing_services=()
  local service=""
  for service in "${BUILD_ROLES[@]}"; do
    local tag="${IMAGE_TAGS[${service}]:-}"
    [[ -n "${tag}" ]] || continue
    if ! docker image inspect "${tag}" >/dev/null 2>&1; then
      missing_services+=("${service}")
    fi
  done
  ((${#missing_services[@]} > 0)) || return 0

  log_status "Docker Cleanup" "Restoring Required Container Images" "${C_YELLOW}"
  GO_API_BACKEND_BINARY_PREPARED=0
  for service in "${missing_services[@]}"; do
    build_service_image "${service}" "${mode}"
  done
}

prune_expired_engine_build_cache_exports() {
  local cache_root="${DEPLOY_DIR}/cache/buildkit"
  [[ -d "${cache_root}" ]] || return 0
  log_status "Docker Cleanup" "Pruning Engine Build Cache >${BUILD_CACHE_RETENTION_DAYS}d" "${C_YELLOW}"
  printf '[%s] Pruning Engine Buildx cache exports older than %d days from %s\n' "$(date +%FT%T)" "${BUILD_CACHE_RETENTION_DAYS}" "${cache_root}" >> "${BUILD_LOG}"
  local cutoff_epoch
  if ! cutoff_epoch="$(date -u -d "${BUILD_CACHE_RETENTION_DAYS} days ago" +%s 2>/dev/null)"; then
    log_status "Docker Cleanup" "Engine Build Cache Retention Failed" "${C_RED}"
    printf '[%s] Failed to compute Engine Buildx cache retention cutoff\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
    return 1
  fi
  local cleanup_failed=0
  local removed=0
  local retained=0
  local service_dir=""
  for service_dir in "${cache_root}"/*; do
    [[ -d "${service_dir}" ]] || continue
    local service_name
    service_name="$(basename "${service_dir}")"
    if ! is_build_role "${service_name}"; then
      printf '[%s] Removing unknown Engine Buildx cache service directory %s\n' "$(date +%FT%T)" "${service_dir}" >> "${BUILD_LOG}"
      if rm -rf "${service_dir}" >> "${BUILD_LOG}" 2>&1; then
        ((removed += 1))
      else
        cleanup_failed=1
      fi
      continue
    fi

    local cache_dir=""
    for cache_dir in "${service_dir}"/*; do
      [[ -d "${cache_dir}" ]] || continue
      local export_name
      export_name="$(basename "${cache_dir}")"
      local should_remove=0
      local reason=""
      if [[ "${export_name}" == next-* ]]; then
        should_remove=1
        reason="incomplete temporary export"
      elif [[ "${export_name}" == "current" ]]; then
        local legacy_epoch
        legacy_epoch="$(stat -c %Y "${cache_dir}" 2>/dev/null || printf '0')"
        if ((legacy_epoch < cutoff_epoch)); then
          should_remove=1
          reason="legacy current export older than ${BUILD_CACHE_RETENTION_DAYS} days"
        fi
      else
        local export_epoch=""
        if export_epoch="$(build_cache_export_epoch "${export_name}")"; then
          if ((export_epoch < cutoff_epoch)); then
            should_remove=1
            reason="timestamp older than ${BUILD_CACHE_RETENTION_DAYS} days"
          fi
        else
          should_remove=1
          reason="unrecognized export name"
        fi
      fi

      if [[ "${should_remove}" -eq 1 ]]; then
        printf '[%s] Removing Engine Buildx cache export %s (%s)\n' "$(date +%FT%T)" "${cache_dir}" "${reason}" >> "${BUILD_LOG}"
        if rm -rf "${cache_dir}" >> "${BUILD_LOG}" 2>&1; then
          ((removed += 1))
        else
          cleanup_failed=1
        fi
      else
        ((retained += 1))
      fi
    done
  done
  if [[ "${cleanup_failed}" -ne 0 ]]; then
    log_status "Docker Cleanup" "Engine Build Cache Prune Failed" "${C_RED}"
    printf '[%s] Failed to prune one or more Engine Buildx cache exports under %s\n' "$(date +%FT%T)" "${cache_root}" >> "${BUILD_LOG}"
    return 1
  fi
  log_status "Docker Cleanup" "Engine Build Cache: Removed ${removed} cache export(s), retained ${retained} cache export(s)" "${C_GREEN}"
  printf '[%s] Engine Buildx cache retention complete: removed=%d retained=%d\n' "$(date +%FT%T)" "${removed}" "${retained}" >> "${BUILD_LOG}"
  return 0
}

prune_engine_docker_storage() {
  local mode="$1"
  if ! engine_docker_cleanup_enabled; then
    log_status "Docker Cleanup" "Skipped" "${C_DIM}"
    printf '[%s] Docker cleanup skipped because BOREALIS_SKIP_DOCKER_PRUNE is set\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
    return 0
  fi

  local cleanup_failed=0
  log_status "Docker Cleanup" "Pruning Inactive Container Images" "${C_YELLOW}"
  if ! docker image prune -a --force --filter "label!=io.borealis.service" >> "${BUILD_LOG}" 2>&1; then
    cleanup_failed=1
    log_status "Docker Cleanup" "Image Prune Failed" "${C_RED}"
  fi

  if ! prune_stale_borealis_service_images; then
    cleanup_failed=1
  fi
  restore_required_engine_images "${mode}"

  if ! docker builder prune --all --force >> "${BUILD_LOG}" 2>&1; then
    cleanup_failed=1
    log_status "Docker Cleanup" "Builder Cache Prune Failed" "${C_RED}"
  fi

  if ! prune_expired_engine_build_cache_exports; then
    cleanup_failed=1
  fi

  if [[ "${cleanup_failed}" -eq 0 ]]; then
    log_status "Docker Cleanup" "Complete" "${C_GREEN}"
  else
    log_status "Docker Cleanup" "Completed With Warnings" "${C_YELLOW}"
  fi
}

write_image_manifest() {
  local mode="$1"
  python3 - "${IMAGE_MANIFEST}" "${mode}" "${BUILD_ROLES[@]}" <<PY
import json
import os
import pathlib
import sys
from datetime import datetime, timezone

path = pathlib.Path(sys.argv[1])
mode = sys.argv[2]
services = sys.argv[3:]
existing = {}
if path.is_file():
    try:
        existing = json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        existing = {}
records = dict((existing.get("services") or {}))
for service in services:
    env_prefix = service.upper().replace("-", "_")
    image = os.environ.get(f"IMAGE_TAG_{env_prefix}")
    digest = os.environ.get(f"IMAGE_HASH_{env_prefix}")
    dockerfile = os.environ.get(f"DOCKERFILE_{env_prefix}")
    context = os.environ.get(f"BUILD_CONTEXT_{env_prefix}")
    if not image or not digest:
        continue
    records[service] = {
        "image": image,
        "hash": digest,
        "dockerfile": dockerfile,
        "context": context,
        "mode": mode,
        "updated_at": datetime.now(timezone.utc).isoformat(),
    }
payload = {
    "schema_version": 1,
    "project": "borealis-engine",
    "mode": mode,
    "updated_at": datetime.now(timezone.utc).isoformat(),
    "services": records,
}
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

export_image_manifest_env() {
  local service=""
  for service in "${BUILD_ROLES[@]}"; do
    if [[ -n "${IMAGE_TAGS[${service}]:-}" && -n "${IMAGE_HASHES[${service}]:-}" ]]; then
      local prefix
      prefix="$(service_env_prefix "${service}")"
      export "IMAGE_TAG_${prefix}=${IMAGE_TAGS[${service}]}"
      export "IMAGE_HASH_${prefix}=${IMAGE_HASHES[${service}]}"
      export "DOCKERFILE_${prefix}=${DOCKERFILES[${service}]}"
      export "BUILD_CONTEXT_${prefix}=${BUILD_CONTEXTS[${service}]}"
    fi
  done
}

load_existing_image_tags() {
  [[ -f "${IMAGE_MANIFEST}" ]] || return 0
  local service=""
  for service in "${BUILD_ROLES[@]}"; do
    local image
    image="$(python3 - "${IMAGE_MANIFEST}" "${service}" <<'PY'
import json
import pathlib
import sys
try:
    data = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
    print(((data.get("services") or {}).get(sys.argv[2]) or {}).get("image") or "")
except Exception:
    print("")
PY
)"
    if [[ -n "${image}" ]]; then
      IMAGE_TAGS["${service}"]="${image}"
    fi
  done
}

changed_build_services() {
  local service=""
  for service in "${SERVICE_ROLES[@]}"; do
    if [[ "${BUILD_STATUSES[${service}]:-}" == "built" ]]; then
      printf '%s\n' "${service}"
    fi
  done
  if [[ "${BUILD_STATUSES[api-backend]:-}" == "built" ]]; then
    printf '%s\n' api-backend
  fi
  if [[ "${BUILD_STATUSES[webui-frontend]:-}" == "built" ]]; then
    printf '%s\n' webui-frontend
  fi
  if [[ "${BUILD_STATUSES[job-scheduler]:-}" == "built" ]]; then
    printf '%s\n' job-scheduler
  fi
  if [[ "${BUILD_STATUSES[site-worker]:-}" == "built" ]]; then
    printf '%s\n' job-scheduler
  fi
}

previous_deploy_mode() {
  [[ -f "${DEPLOY_MANIFEST}" ]] || return 0
  python3 - "${DEPLOY_MANIFEST}" <<'PY'
import json
import pathlib
import sys

try:
    data = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
except Exception:
    raise SystemExit(0)
print(str(data.get("mode") or ""))
PY
}

write_deploy_manifest() {
  local mode="$1"
  local compose_action="${2:-up}"
  if (($# >= 2)); then
    shift 2
  else
    shift "$#"
  fi
  python3 - "${DEPLOY_MANIFEST}" "${IMAGE_MANIFEST}" "${mode}" "${COMPOSE_FILE}" "${COMPOSE_ENV}" "${PROJECT_NAME}" "${compose_action}" "$@" <<'PY'
import hashlib
import json
import pathlib
import sys
from datetime import datetime, timezone

deploy_path = pathlib.Path(sys.argv[1])
image_path = pathlib.Path(sys.argv[2])
mode = sys.argv[3]
compose_file = pathlib.Path(sys.argv[4])
env_file = pathlib.Path(sys.argv[5])
project = sys.argv[6]
compose_action = sys.argv[7]
changed_services = sys.argv[8:]

def file_hash(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()

def env_settings_hash(path: pathlib.Path) -> str:
    lines = []
    image_keys = {
        "BOREALIS_API_BACKEND_IMAGE",
        "BOREALIS_OPERATOR_IMAGE",
        "BOREALIS_JOB_SCHEDULER_IMAGE",
        "BOREALIS_SITE_WORKER_IMAGE",
        "BOREALIS_WEBUI_FRONTEND_IMAGE",
        "BOREALIS_TRAEFIK_EDGE_IMAGE",
        "BOREALIS_POSTGRES_DB_IMAGE",
        "BOREALIS_REMOTE_DESKTOP_GUACD_IMAGE",
        "BOREALIS_WIREGUARD_TUNNEL_IMAGE",
        "BOREALIS_ENGINE_MODE",
        "BOREALIS_WEBUI_MODE",
    }
    for raw in path.read_text(encoding="utf-8").splitlines():
        key = raw.split("=", 1)[0]
        if key in image_keys:
            continue
        lines.append(raw)
    return hashlib.sha256(("\n".join(lines) + "\n").encode("utf-8")).hexdigest()

def env_values(path: pathlib.Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw in path.read_text(encoding="utf-8").splitlines():
        if not raw or raw.lstrip().startswith("#") or "=" not in raw:
            continue
        key, value = raw.split("=", 1)
        values[key.strip()] = value.strip()
    return values

def env_int(values: dict[str, str], key: str, default: int = 0) -> int:
    try:
        return int(str(values.get(key, "")).strip())
    except Exception:
        return default

services = []
service_images = {}

env = env_values(env_file)
payload = {
    "schema_version": 2,
    "project": project,
    "mode": mode,
    "compose_file": str(compose_file),
    "compose_file_hash": file_hash(compose_file),
    "env_file": str(env_file),
    "env_file_hash": file_hash(env_file),
    "env_settings_hash": env_settings_hash(env_file),
    "image_manifest": str(image_path),
    "service_images": service_images,
    "changed_services": changed_services,
    "compose_action": compose_action,
    "deployment_profile": {
        "name": env.get("BOREALIS_DEPLOYMENT_PROFILE", ""),
        "rank": env_int(env, "BOREALIS_DEPLOYMENT_PROFILE_RANK"),
        "cpu_rank": env_int(env, "BOREALIS_DEPLOYMENT_CPU_RANK"),
        "memory_rank": env_int(env, "BOREALIS_DEPLOYMENT_MEMORY_RANK"),
        "host_vcpu": env_int(env, "BOREALIS_DEPLOYMENT_HOST_VCPU"),
        "host_memory_mib": env_int(env, "BOREALIS_DEPLOYMENT_HOST_MEMORY_MIB"),
        "host_memory_gib": env.get("BOREALIS_DEPLOYMENT_HOST_MEMORY_GIB", ""),
        "site_worker_scheduled_concurrency": env_int(env, "BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY"),
        "db_pool_size": env_int(env, "BOREALIS_DB_POOL_SIZE"),
        "db_max_overflow": env_int(env, "BOREALIS_DB_MAX_OVERFLOW"),
        "postgres": {
            "max_connections": env_int(env, "BOREALIS_POSTGRES_MAX_CONNECTIONS"),
            "shared_buffers": env.get("BOREALIS_POSTGRES_SHARED_BUFFERS", ""),
            "effective_cache_size": env.get("BOREALIS_POSTGRES_EFFECTIVE_CACHE_SIZE", ""),
            "work_mem": env.get("BOREALIS_POSTGRES_WORK_MEM", ""),
            "maintenance_work_mem": env.get("BOREALIS_POSTGRES_MAINTENANCE_WORK_MEM", ""),
            "max_worker_processes": env_int(env, "BOREALIS_POSTGRES_MAX_WORKER_PROCESSES"),
            "max_parallel_workers": env_int(env, "BOREALIS_POSTGRES_MAX_PARALLEL_WORKERS"),
            "max_parallel_workers_per_gather": env_int(env, "BOREALIS_POSTGRES_MAX_PARALLEL_WORKERS_PER_GATHER"),
            "autovacuum_max_workers": env_int(env, "BOREALIS_POSTGRES_AUTOVACUUM_MAX_WORKERS"),
            "autovacuum_vacuum_cost_limit": env_int(env, "BOREALIS_POSTGRES_AUTOVACUUM_VACUUM_COST_LIMIT"),
            "autovacuum_naptime": env.get("BOREALIS_POSTGRES_AUTOVACUUM_NAPTIME", ""),
            "autovacuum_vacuum_scale_factor": env.get("BOREALIS_POSTGRES_AUTOVACUUM_VACUUM_SCALE_FACTOR", ""),
            "autovacuum_analyze_scale_factor": env.get("BOREALIS_POSTGRES_AUTOVACUUM_ANALYZE_SCALE_FACTOR", ""),
            "max_wal_size": env.get("BOREALIS_POSTGRES_MAX_WAL_SIZE", ""),
            "min_wal_size": env.get("BOREALIS_POSTGRES_MIN_WAL_SIZE", ""),
            "effective_io_concurrency": env_int(env, "BOREALIS_POSTGRES_EFFECTIVE_IO_CONCURRENCY"),
            "wal_compression": env.get("BOREALIS_POSTGRES_WAL_COMPRESSION", ""),
            "checkpoint_timeout": env.get("BOREALIS_POSTGRES_CHECKPOINT_TIMEOUT", ""),
            "checkpoint_completion_target": env.get("BOREALIS_POSTGRES_CHECKPOINT_COMPLETION_TARGET", ""),
            "random_page_cost": env.get("BOREALIS_POSTGRES_RANDOM_PAGE_COST", ""),
        },
    },
    "engine_deployment_profile": {
        "id": env.get("BOREALIS_ENGINE_DEPLOYMENT_PROFILE", ""),
        "label": env.get("BOREALIS_ENGINE_DEPLOYMENT_PROFILE_LABEL", ""),
        "network_mode": env.get("BOREALIS_ENGINE_NETWORK_MODE", ""),
        "network_mode_label": env.get("BOREALIS_ENGINE_NETWORK_MODE_LABEL", ""),
        "fqdn_aliases": env.get("BOREALIS_PUBLIC_HOSTNAME_ALIASES", ""),
        "local_ca_enabled": env.get("BOREALIS_LOCAL_CA_ENABLED", "") in {"1", "true", "TRUE", "yes", "YES", "on", "ON"},
    },
    "deployed_at": datetime.now(timezone.utc).isoformat(),
    "services": services,
}
deploy_path.parent.mkdir(parents=True, exist_ok=True)
deploy_path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
}

validate_service() {
  local service="$1"
  local candidate=""
  for candidate in "${SERVICE_ACTION_ROLES[@]}"; do
    [[ "${candidate}" == "${service}" ]] && return 0
  done
  die "Unknown Engine service '${service}'."
}

validate_build_role() {
  local service="$1"
  local candidate=""
  for candidate in "${BUILD_ROLES[@]}"; do
    [[ "${candidate}" == "${service}" ]] && return 0
  done
  die "Unknown Engine build role '${service}'."
}

prepare_runtime() {
  local mode="$1"
  ensure_engine_runtime_identity
  ensure_service_tree
  seed_webui_runtime_source "${mode}"
  prune_empty_legacy_runtime_paths
  load_existing_image_tags
  local public_host
  local acme_email
  local traefik_trusted_proxy_ips
  local engine_profile
  local engine_network_mode
  local fqdn_aliases
  engine_network_mode="$(resolve_engine_network_mode)"
  ENGINE_NETWORK_MODE="${engine_network_mode}"
  export BOREALIS_ENGINE_NETWORK_MODE="${ENGINE_NETWORK_MODE}"
  engine_profile="$(engine_deployment_profile_from_network_mode "${engine_network_mode}")"
  ENGINE_DEPLOYMENT_PROFILE="${engine_profile}"
  export BOREALIS_ENGINE_DEPLOYMENT_PROFILE="${ENGINE_DEPLOYMENT_PROFILE}"
  public_host="$(resolve_public_hostname)"
  fqdn_aliases="$(resolve_engine_hostname_aliases "${public_host}")"
  validate_engine_hostname_aliases "${fqdn_aliases}"
  validate_engine_log_retention_settings
  if [[ "${engine_profile}" == "internal-only" ]]; then
    acme_email=""
    ensure_local_ca_material "${engine_profile}" "${public_host}" "${fqdn_aliases}"
  else
    acme_email="$(resolve_acme_email)"
  fi
  traefik_trusted_proxy_ips="$(resolve_traefik_trusted_proxy_ips "${engine_profile}")"
  write_compose_env "${mode}" "${public_host}" "${acme_email}" "${traefik_trusted_proxy_ips}" "${engine_profile}" "${fqdn_aliases}"
}

deploy_engine() {
  local mode
  mode="$(normalize_mode "${1:-prod}")"
  local hmr_guard_status=0
  cluster_hmr_guard "${mode}" all || hmr_guard_status=$?
  if [[ "${hmr_guard_status}" -eq 10 ]]; then
    return 0
  fi
  [[ "${hmr_guard_status}" -eq 0 ]] || return "${hmr_guard_status}"
  local network_mode
  network_mode="$(resolve_engine_network_mode)"
  log_deploy_header "${mode}" "${network_mode}"
  ensure_engine_dependencies
  ensure_k3s_cluster_baseline
  ensure_longhorn_storage_baseline
  ensure_no_host_postgres_conflict
  local previous_internal_api_base_url=""
  previous_internal_api_base_url="$(read_env_value BOREALIS_INTERNAL_API_BASE_URL)"
  prepare_runtime "${mode}"
  ensure_engine_agent_install_cache
  build_images "${mode}"
  ensure_borealis_node_manager
  export_image_manifest_env
  write_image_manifest "${mode}"
  local desired_postgres_traffic_owner=""
  local previous_postgres_traffic_owner=""
  local postgres_cutover_pending=0
  desired_postgres_traffic_owner="$(resolve_postgres_traffic_owner)"
  if [[ "${desired_postgres_traffic_owner}" == "k3s" ]] && ! k3s_postgres_enabled; then
    die "K3s PostgreSQL is the only supported PostgreSQL traffic owner. Do not disable BOREALIS_K3S_POSTGRES_ENABLED after Stage 9 cutover."
  fi
  previous_postgres_traffic_owner="$(current_k3s_postgres_traffic_owner)"
  if [[ "${desired_postgres_traffic_owner}" == "k3s" && "${previous_postgres_traffic_owner}" != "k3s" ]]; then
    postgres_cutover_pending=1
    ensure_k3s_postgres_statefulset "${mode}" "docker-compose"
    import_compose_postgres_into_k3s_for_cutover
  fi
  ensure_borealis_operator_bridge
  ensure_k3s_bridge_workloads "${mode}"
  BOREALIS_SUPPRESS_DEPLOYMENT_PROFILE_LOG=1 write_compose_env "${mode}" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME)" "$(read_env_value BOREALIS_ACME_EMAIL)" "$(read_env_value BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS)" "$(read_env_value BOREALIS_ENGINE_DEPLOYMENT_PROFILE)" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME_ALIASES)"
  ensure_borealis_operator_bridge
  ensure_k3s_wireguard_tunnel "${mode}"
  if ((postgres_cutover_pending == 1)); then
    ensure_k3s_postgres_statefulset "${mode}" "k3s"
  else
    ensure_k3s_postgres_statefulset "${mode}" "${desired_postgres_traffic_owner}"
  fi
  ensure_k3s_engine_database_schema "${mode}"
  ensure_k3s_api_backend_bridge "${mode}"
  ensure_cluster_controller_baseline
  ensure_cluster_dependency_probe_guards
  ensure_k3s_traefik_edge "${mode}"
  retire_compose_job_scheduler_container
  ensure_k3s_job_scheduler "${mode}"
  wait_for_k3s_postgres_cutover_workers
  local current_internal_api_base_url=""
  current_internal_api_base_url="$(read_env_value BOREALIS_INTERNAL_API_BASE_URL)"
  log_section "Service Reconciliation"
  local changed_services=()
  mapfile -t changed_services < <(changed_build_services)
  local previous_mode=""
  previous_mode="$(previous_deploy_mode)"
  local target_service_lines=""
  target_service_lines="$(
    {
      printf '%s\n' "${changed_services[@]}"
      if [[ -n "${previous_mode}" && "${previous_mode}" != "${mode}" ]]; then
        printf '%s\n' webui-frontend
      fi
    } | awk 'NF && !seen[$0]++'
  )"
  local target_services=()
  if [[ -n "${target_service_lines}" ]]; then
    mapfile -t target_services <<< "${target_service_lines}"
  fi
  local requested_target_services=("${target_services[@]}")
  if ((${#SERVICE_ROLES[@]} == 0)); then
    log_status "Docker Compose" "Retired" "${C_DIM}"
    retire_compose_webui_container
    recycle_k3s_site_workers_for_api_cutover "${previous_internal_api_base_url}" "${current_internal_api_base_url}"
    recycle_k3s_site_workers_for_runtime_secret_change
    recycle_k3s_site_workers_for_timezone
    retire_compose_api_backend_container
    retire_compose_postgres_container
    retire_compose_wireguard_tunnel_container
    retire_compose_remote_desktop_guacd_container
    retire_compose_traefik_edge_container
    retire_compose_site_worker_orchestrator_container
    retire_compose_docker_proxy_container
    if cluster_mode_enabled; then
      reconcile_cluster_node_workloads
    fi
    write_deploy_manifest "${mode}" "retired" "${requested_target_services[@]}"
    log_section "Docker Housekeeping"
    prune_engine_docker_storage "${mode}"
    log_section "Engine Deployment Complete"
    log_webui_url
    return 0
  fi
  die "Docker Compose service reconciliation is retired. SERVICE_ROLES must remain empty."
}

service_action() {
  local service="${1:-}"
  local action="${2:-}"
  local mode="${3:-prod}"
  [[ -n "${service}" && -n "${action}" ]] || die "Usage: Engine.sh --service <service> <restart|rebuild|reload|reconcile|shadow-import|shadow-db-validate> [dev|prod]"
  validate_service "${service}"
  mode="$(normalize_mode "${mode}")"
  if [[ "${service}" == "webui-frontend" && "${action}" == "rebuild" ]]; then
    local hmr_guard_status=0
    cluster_hmr_guard "${mode}" webui-frontend || hmr_guard_status=$?
    if [[ "${hmr_guard_status}" -eq 10 ]]; then
      return 0
    fi
    [[ "${hmr_guard_status}" -eq 0 ]] || return "${hmr_guard_status}"
  fi
  ensure_engine_dependencies
  ensure_no_host_postgres_conflict
  prepare_runtime "${mode}"
  case "${action}" in
    shadow-import|validate-shadow-import)
      [[ "${service}" == "postgres-db" ]] || die "shadow-import supported for postgres-db only."
      validate_k3s_postgres_shadow_import "${mode}"
      ;;
    shadow-db-validate|validate-shadow-db)
      [[ "${service}" == "api-backend" ]] || die "shadow-db-validate supported for api-backend only."
      validate_k3s_api_backend_shadow_db "${mode}"
      ;;
    restart)
      if [[ "${service}" == "webui-frontend" ]]; then
        ensure_k3s_cluster_baseline
        ensure_borealis_operator_bridge
        ensure_k3s_bridge_workloads "${mode}"
        retire_compose_webui_container
        log_status "k3s-webui-frontend" "Restarting" "${C_YELLOW}"
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout restart "deployment/webui-frontend" >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-webui-frontend" "Restart Failed" "${C_RED}"
          die "K3s WebUI frontend restart failed. See ${BUILD_LOG}."
        fi
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/webui-frontend" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-webui-frontend" "Rollout Failed" "${C_RED}"
          die "K3s WebUI frontend rollout failed after restart. See ${BUILD_LOG}."
        fi
        log_status "k3s-webui-frontend" "Ready - Traffic Owner" "${C_GREEN}"
        return 0
      fi
      if [[ "${service}" == "api-backend" ]]; then
        ensure_k3s_cluster_baseline
        ensure_borealis_operator_bridge
        ensure_k3s_api_backend_bridge "${mode}"
        ensure_k3s_traefik_edge "${mode}"
        log_status "k3s-api-backend" "Restarting" "${C_YELLOW}"
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout restart "deployment/api-backend" >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-api-backend" "Restart Failed" "${C_RED}"
          die "K3s API backend restart failed. See ${BUILD_LOG}."
        fi
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/api-backend" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-api-backend" "Rollout Failed" "${C_RED}"
          die "K3s API backend rollout failed after restart. See ${BUILD_LOG}."
        fi
        retire_compose_api_backend_container
        log_status "k3s-api-backend" "Ready - Traffic Owner" "${C_GREEN}"
        return 0
      fi
      if [[ "${service}" == "job-scheduler" ]]; then
        ensure_k3s_cluster_baseline
        ensure_borealis_operator_bridge
        retire_compose_job_scheduler_container
        log_status "k3s-job-scheduler" "Restarting" "${C_YELLOW}"
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout restart "deployment/job-scheduler" >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-job-scheduler" "Restart Failed" "${C_RED}"
          die "K3s job scheduler restart failed. See ${BUILD_LOG}."
        fi
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/job-scheduler" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-job-scheduler" "Rollout Failed" "${C_RED}"
          die "K3s job scheduler rollout failed after restart. See ${BUILD_LOG}."
        fi
        log_status "k3s-job-scheduler" "Ready - Traffic Owner" "${C_GREEN}"
        return 0
      fi
      if [[ "${service}" == "postgres-db" ]]; then
        ensure_k3s_cluster_baseline
        ensure_longhorn_storage_baseline
        ensure_k3s_postgres_statefulset "${mode}" "k3s"
        log_status "k3s-postgres-db" "Restarting" "${C_YELLOW}"
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout restart "statefulset/postgres-db" >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-postgres-db" "Restart Failed" "${C_RED}"
          die "K3s PostgreSQL restart failed. See ${BUILD_LOG}."
        fi
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "statefulset/postgres-db" --timeout="${K3S_POSTGRES_ROLLOUT_TIMEOUT}" >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-postgres-db" "Rollout Failed" "${C_RED}"
          die "K3s PostgreSQL rollout failed after restart. See ${BUILD_LOG}."
        fi
        log_status "k3s-postgres-db" "Ready - Traffic Owner" "${C_GREEN}"
        return 0
      fi
      if [[ "${service}" == "wireguard-tunnel" ]]; then
        ensure_k3s_cluster_baseline
        ensure_k3s_wireguard_tunnel "${mode}"
        log_status "k3s-wireguard-tunnel" "Restarting" "${C_YELLOW}"
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout restart "deployment/wireguard-tunnel" >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-wireguard-tunnel" "Restart Failed" "${C_RED}"
          die "K3s WireGuard tunnel restart failed. See ${BUILD_LOG}."
        fi
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/wireguard-tunnel" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-wireguard-tunnel" "Rollout Failed" "${C_RED}"
          die "K3s WireGuard tunnel rollout failed after restart. See ${BUILD_LOG}."
        fi
        if ! k3s_wireguard_control_client ping >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-wireguard-tunnel" "Control Socket Failed" "${C_RED}"
          die "K3s WireGuard control socket check failed after restart. See ${BUILD_LOG}."
        fi
        log_status "k3s-wireguard-tunnel" "Ready" "${C_GREEN}"
        return 0
      fi
      if [[ "${service}" == "remote-desktop-guacd" ]]; then
        ensure_k3s_cluster_baseline
        ensure_borealis_operator_bridge
        ensure_k3s_bridge_workloads "${mode}"
        retire_compose_remote_desktop_guacd_container
        log_status "k3s-remote-desktop-guacd" "Restarting" "${C_YELLOW}"
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout restart "deployment/remote-desktop-guacd" >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-remote-desktop-guacd" "Restart Failed" "${C_RED}"
          die "K3s guacd restart failed. See ${BUILD_LOG}."
        fi
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/remote-desktop-guacd" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-remote-desktop-guacd" "Rollout Failed" "${C_RED}"
          die "K3s guacd rollout failed after restart. See ${BUILD_LOG}."
        fi
        log_status "k3s-remote-desktop-guacd" "Ready" "${C_GREEN}"
        return 0
      fi
      if [[ "${service}" == "traefik-edge" ]]; then
        ensure_k3s_cluster_baseline
        ensure_k3s_traefik_edge "${mode}"
        log_status "k3s-traefik-edge" "Restarting" "${C_YELLOW}"
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout restart "deployment/traefik-edge" >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-traefik-edge" "Restart Failed" "${C_RED}"
          die "K3s Traefik edge restart failed. See ${BUILD_LOG}."
        fi
        if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/traefik-edge" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-traefik-edge" "Rollout Failed" "${C_RED}"
          die "K3s Traefik edge rollout failed after restart. See ${BUILD_LOG}."
        fi
        if ! k3s_traefik_edge_healthcheck >> "${BUILD_LOG}" 2>&1; then
          log_status "k3s-traefik-edge" "Healthcheck Failed" "${C_RED}"
          die "K3s Traefik edge healthcheck failed after restart. See ${BUILD_LOG}."
        fi
        log_status "k3s-traefik-edge" "Ready - Traffic Owner" "${C_GREEN}"
        return 0
      fi
      die "restart for '${service}' has no retired Docker Compose fallback. Add an explicit K3s handler before enabling this service action."
      ;;
    rebuild)
      local build_service="${service}"
      if [[ "${build_service}" == "api-backend" ]]; then
        ensure_engine_agent_install_cache
      fi
      build_images "${mode}" "${build_service}"
      export_image_manifest_env
      write_image_manifest "${mode}"
      BOREALIS_SUPPRESS_DEPLOYMENT_PROFILE_LOG=1 write_compose_env "${mode}" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME)" "$(read_env_value BOREALIS_ACME_EMAIL)" "$(read_env_value BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS)" "$(read_env_value BOREALIS_ENGINE_DEPLOYMENT_PROFILE)" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME_ALIASES)"
      log_status "${service}" "Skipping Compose - Retired" "${C_DIM}"
      reconcile_k3s_bridge_for_scoped_rebuild "${service}" "${mode}"
      if cluster_mode_enabled; then
        reconcile_cluster_node_workloads "${service}"
      fi
      retire_compose_webui_container
      retire_compose_api_backend_container
      retire_compose_job_scheduler_container
      retire_compose_postgres_container
      retire_compose_wireguard_tunnel_container
      retire_compose_remote_desktop_guacd_container
      retire_compose_traefik_edge_container
      retire_compose_site_worker_orchestrator_container
      retire_compose_docker_proxy_container
      write_deploy_manifest "${mode}" "up-scoped" "${service}"
      prune_engine_docker_storage "${mode}"
      log_webui_url
      ;;
    reload)
      [[ "${service}" == "traefik-edge" ]] || die "reload supported for traefik-edge only."
      ensure_k3s_cluster_baseline
      ensure_k3s_traefik_edge "${mode}"
      log_status "k3s-traefik-edge" "Reloading" "${C_YELLOW}"
      if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout restart "deployment/traefik-edge" >> "${BUILD_LOG}" 2>&1; then
        log_status "k3s-traefik-edge" "Reload Failed" "${C_RED}"
        die "K3s Traefik edge reload failed. See ${BUILD_LOG}."
      fi
      if ! k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/traefik-edge" --timeout=120s >> "${BUILD_LOG}" 2>&1; then
        log_status "k3s-traefik-edge" "Rollout Failed" "${C_RED}"
        die "K3s Traefik edge rollout failed after reload. See ${BUILD_LOG}."
      fi
      if ! k3s_traefik_edge_healthcheck >> "${BUILD_LOG}" 2>&1; then
        log_status "k3s-traefik-edge" "Healthcheck Failed" "${C_RED}"
        die "K3s Traefik edge healthcheck failed after reload. See ${BUILD_LOG}."
      fi
      log_status "k3s-traefik-edge" "Ready - Traffic Owner" "${C_GREEN}"
      ;;
    reconcile)
      [[ "${service}" == "wireguard-tunnel" ]] || die "reconcile supported for wireguard-tunnel only."
      ensure_k3s_cluster_baseline
      ensure_k3s_wireguard_tunnel "${mode}"
      log_status "k3s-wireguard-tunnel" "Reconciling Control Socket" "${C_YELLOW}"
      if ! k3s_wireguard_control_client reconcile >> "${BUILD_LOG}" 2>&1; then
        printf '[%s] WireGuard reconcile returned nonzero; keeping K3s tunnel pod running for API-driven peer repair.\n' "$(date +%FT%T)" >> "${BUILD_LOG}"
      fi
      log_status "k3s-wireguard-tunnel" "Ready" "${C_GREEN}"
      ;;
    *)
      die "Unsupported service action '${action}'."
      ;;
  esac
}

agent_redeploy_runtime_mode() {
  local mode=""
  if [[ -f "${IMAGE_MANIFEST}" ]]; then
    mode="$(python3 - "${IMAGE_MANIFEST}" <<'PY'
import json
import pathlib
import sys

try:
    payload = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
except Exception:
    payload = {}
print(str(payload.get("mode") or "prod"))
PY
)"
  fi
  normalize_mode "${mode:-prod}"
}

agent_redeploy_artifact_metadata() {
  local config_path="${RUNTIME_ROOT}/Services/api-backend/config/agent_artifact.json"
  [[ -f "${config_path}" ]] || die "Agent artifact config missing after build: ${config_path}"
  python3 - "${config_path}" <<'PY'
import json
import pathlib
import sys

payload = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
artifact = payload.get("artifact") or {}
artifact_id = str(artifact.get("artifact_id") or "").strip()
build_id = str(artifact.get("build_id") or "").strip()
compiled_at = int(artifact.get("compiled_at") or 0)
artifact_path = str(artifact.get("artifact_path") or "").strip()
if not artifact_id or not build_id or compiled_at <= 0 or not artifact_path:
    raise SystemExit("Agent artifact metadata is incomplete")
print(f"{artifact_id}\t{build_id}\t{compiled_at}\t{artifact_path}")
PY
}

agent_redeploy_verify_api_cache_mount() {
  local config_path="${RUNTIME_ROOT}/Services/api-backend/config/agent_artifact.json"
  local artifact_path="$1"
  local build_id="$2"
  k3s_kubectl -n "${K3S_NAMESPACE}" exec deployment/api-backend -c api-backend -- sh -c \
    'test -s "$1" && test -s "$2" && grep -Fq "$3" "$1"' \
    _ "${config_path}" "${artifact_path}" "${build_id}" >>"${BUILD_LOG}" 2>&1
}

agent_redeploy_cnpg_primary_pod() {
  k3s_kubectl -n "${K3S_NAMESPACE}" get endpointslice \
    -l kubernetes.io/service-name=borealis-postgres-rw \
    -o jsonpath='{range .items[*].endpoints[?(@.conditions.ready==true)]}{.targetRef.name}{"\n"}{end}' \
    2>>"${BUILD_LOG}" | awk 'NF {print; exit}'
}

agent_redeploy_active_work_count() {
  local count=""
  if cluster_mode_enabled; then
    local primary_pod=""
    local database=""
    primary_pod="$(agent_redeploy_cnpg_primary_pod)"
    database="$(read_env_value POSTGRES_DB "${RUNTIME_ENV}")"
    [[ -n "${primary_pod}" && -n "${database}" ]] || return 1
    count="$(
      k3s_kubectl -n "${K3S_NAMESPACE}" exec "${primary_pod}" -c postgres -- \
        psql "--dbname=${database}" -Atqc \
        "SELECT COUNT(*) FROM engine.job_scheduler_work_items WHERE status = 'running';" \
        2>>"${BUILD_LOG}" | tr -d '[:space:]'
    )"
  else
    count="$(
      k3s_kubectl -n "${K3S_NAMESPACE}" exec postgres-db-0 -c postgres-db -- sh -c \
        'PGPASSWORD="$POSTGRES_PASSWORD" psql -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atqc "SELECT COUNT(*) FROM engine.job_scheduler_work_items WHERE status = '\''running'\'';"' \
        2>>"${BUILD_LOG}" | tr -d '[:space:]'
    )"
  fi
  [[ "${count}" =~ ^[0-9]+$ ]] || return 1
  printf '%s\n' "${count}"
}

agent_redeploy_wait_for_work_drain() {
  local timeout="${BOREALIS_AGENT_REDEPLOY_DRAIN_TIMEOUT_SECONDS:-300}"
  [[ "${timeout}" =~ ^[0-9]+$ && "${timeout}" -ge 30 && "${timeout}" -le 3600 ]] \
    || die "BOREALIS_AGENT_REDEPLOY_DRAIN_TIMEOUT_SECONDS must be 30 through 3600."
  local deadline=$((SECONDS + timeout))
  local count=""
  while ((SECONDS < deadline)); do
    count="$(agent_redeploy_active_work_count)" \
      || die "Unable to read running scheduler work count before worker rotation."
    if ((count == 0)); then
      log_status "k3s-job-scheduler" "Work Queue Drained" "${C_GREEN}"
      return 0
    fi
    log_status "k3s-job-scheduler" "Waiting For ${count} Running Work Item(s)" "${C_YELLOW}"
    sleep 3
  done
  die "Timed out waiting for running scheduler work to drain. No workers were changed."
}

agent_redeploy_worker_records() {
  local target_image="$1"
  local inventory_file=""
  inventory_file="$(mktemp "${DEPLOY_DIR}/agent-redeploy-workers.XXXXXX.json")"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" get pods \
    -l 'app.kubernetes.io/name=site-worker,app.kubernetes.io/managed-by=borealis-operator' \
    -o json > "${inventory_file}" 2>>"${BUILD_LOG}"; then
    rm -f "${inventory_file}"
    return 1
  fi
  python3 - "${inventory_file}" "${target_image}" <<'PY'
import json
import pathlib
import sys

payload = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
target_image = sys.argv[2]
errors = []
records = []
for pod in payload.get("items") or []:
    metadata = pod.get("metadata") or {}
    labels = metadata.get("labels") or {}
    annotations = metadata.get("annotations") or {}
    status = pod.get("status") or {}
    spec = pod.get("spec") or {}
    name = str(metadata.get("name") or "").strip()
    if str(labels.get("borealis.io/redeploy-agent-candidate") or "").lower() == "true":
        errors.append(f"unfinished candidate pod exists: {name}")
        continue
    configured_image = str(annotations.get("borealis.io/image-ref") or "").strip()
    if not configured_image:
        for container in spec.get("containers") or []:
            if str(container.get("name") or "") == "site-worker":
                configured_image = str(container.get("image") or "").strip()
                break
    if configured_image == target_image:
        continue
    ready = any(
        str(condition.get("type") or "") == "Ready"
        and str(condition.get("status") or "") == "True"
        for condition in status.get("conditions") or []
    )
    if str(status.get("phase") or "") != "Running" or not ready:
        errors.append(f"outdated worker is not ready: {name}")
        continue
    values = {
        "pod": name,
        "service": str(annotations.get("borealis.io/service-name") or name).strip(),
        "guid": str(labels.get("borealis.io/worker-guid") or "").strip(),
        "site_id": str(labels.get("borealis.io/site-id") or "").strip(),
        "remote_ops_port": str(annotations.get("borealis.io/remote-ops-port") or "").strip(),
        "remote_desktop_port": str(annotations.get("borealis.io/remote-desktop-port") or "").strip(),
        "configured_image": configured_image,
        "uid": str(metadata.get("uid") or "").strip(),
        "pod_revision": str(labels.get("borealis.io/redeploy-agent-revision") or "").strip(),
    }
    required = ("pod", "service", "guid", "site_id", "remote_ops_port", "remote_desktop_port", "configured_image", "uid")
    missing = [key for key in required if not values[key]]
    if missing:
        errors.append(f"worker {name} missing rotation metadata: {','.join(missing)}")
        continue
    records.append("\t".join(values[key] for key in (*required, "pod_revision")))
if errors:
    print("\n".join(errors), file=sys.stderr)
    raise SystemExit(2)
print("\n".join(records))
PY
  local status=$?
  rm -f "${inventory_file}"
  return "${status}"
}

agent_redeploy_service_revision() {
  local service="$1"
  k3s_kubectl -n "${K3S_NAMESPACE}" get "service/${service}" -o json 2>>"${BUILD_LOG}" \
    | python3 -c 'import json,sys; print(str((((json.load(sys.stdin).get("spec") or {}).get("selector") or {}).get("borealis.io/redeploy-agent-revision") or "")))'
}

agent_redeploy_service_cluster_ip() {
  local service="$1"
  k3s_kubectl -n "${K3S_NAMESPACE}" get "service/${service}" \
    -o jsonpath='{.spec.clusterIP}' 2>>"${BUILD_LOG}"
}

agent_redeploy_patch_service_revision() {
  local service="$1"
  local revision="$2"
  k3s_kubectl -n "${K3S_NAMESPACE}" patch "service/${service}" --type=merge \
    -p "{\"spec\":{\"selector\":{\"borealis.io/redeploy-agent-revision\":\"${revision}\"}}}" \
    >>"${BUILD_LOG}" 2>&1
}

agent_redeploy_remove_service_revision() {
  local service="$1"
  k3s_kubectl -n "${K3S_NAMESPACE}" patch "service/${service}" --type=json \
    -p='[{"op":"remove","path":"/spec/selector/borealis.io~1redeploy-agent-revision"}]' \
    >>"${BUILD_LOG}" 2>&1
}

agent_redeploy_wait_for_service_endpoint() {
  local service="$1"
  local pod="$2"
  local timeout="${3:-60}"
  local deadline=$((SECONDS + timeout))
  local endpoints=""
  while ((SECONDS < deadline)); do
    endpoints="$(
      k3s_kubectl -n "${K3S_NAMESPACE}" get endpointslices \
        -l "kubernetes.io/service-name=${service}" \
        -o jsonpath='{range .items[*].endpoints[*]}{.conditions.ready}{"\t"}{.targetRef.name}{"\n"}{end}' \
        2>>"${BUILD_LOG}" || true
    )"
    if [[ "$(printf '%s\n' "${endpoints}" | awk -v pod="${pod}" '$1 == "true" {ready++; if ($2 == pod) target++} END {print (ready == 1 && target == 1) ? "yes" : "no"}')" == "yes" ]]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

agent_redeploy_probe_pod() {
  local pod="$1"
  local remote_ops_port="$2"
  local worker_guid="$3"
  local site_id="$4"
  local url="http://127.0.0.1:${remote_ops_port}/health"
  local payload=""
  payload="$(k3s_kubectl -n "${K3S_NAMESPACE}" exec "${pod}" -c site-worker -- \
    curl --fail --silent --show-error --max-time 5 "${url}" 2>>"${BUILD_LOG}")" || return 1
  printf '%s' "${payload}" | python3 -c '
import json, sys
worker_guid, site_id = sys.argv[1], int(sys.argv[2])
payload = json.load(sys.stdin)
if payload.get("status") != "ok" or str(payload.get("worker_guid") or "") != worker_guid or int(payload.get("site_id") or 0) != site_id:
    raise SystemExit(1)
' "${worker_guid}" "${site_id}"
}

agent_redeploy_wait_for_pod_health() {
  local pod="$1"
  local remote_ops_port="$2"
  local worker_guid="$3"
  local site_id="$4"
  local timeout="${5:-30}"
  local attempt=0
  [[ "${timeout}" =~ ^[0-9]+$ && "${timeout}" -ge 1 ]] || return 1
  while ((attempt < timeout)); do
    if agent_redeploy_probe_pod "${pod}" "${remote_ops_port}" "${worker_guid}" "${site_id}"; then
      return 0
    fi
    attempt=$((attempt + 1))
    ((attempt < timeout)) && sleep 1
  done
  return 1
}

agent_redeploy_probe_service() {
  local pod="$1"
  local service="$2"
  local remote_ops_port="$3"
  local worker_guid="$4"
  local site_id="$5"
  local url="http://${service}.${K3S_NAMESPACE}.svc.cluster.local:${remote_ops_port}/health"
  local payload=""
  payload="$(k3s_kubectl -n "${K3S_NAMESPACE}" exec "${pod}" -c site-worker -- \
    curl --fail --silent --show-error --max-time 5 "${url}" 2>>"${BUILD_LOG}")" || return 1
  printf '%s' "${payload}" | python3 -c '
import json, sys
worker_guid, site_id = sys.argv[1], int(sys.argv[2])
payload = json.load(sys.stdin)
if payload.get("status") != "ok" or str(payload.get("worker_guid") or "") != worker_guid or int(payload.get("site_id") or 0) != site_id:
    raise SystemExit(1)
' "${worker_guid}" "${site_id}"
}

agent_redeploy_wait_for_worker_registration() {
  local worker_guid="$1"
  local container_name="$2"
  local timeout="${3:-30}"
  local deadline=$((SECONDS + timeout))
  local count=""
  local query='SELECT COUNT(*)
FROM engine.job_scheduler_workers AS workers
JOIN engine.job_scheduler_worker_routes AS routes USING (worker_guid)
WHERE workers.worker_guid = :'"'"'worker_guid'"'"'
  AND workers.container_name = :'"'"'container_name'"'"'
  AND workers.status IN ('"'"'starting'"'"', '"'"'running'"'"', '"'"'idle'"'"')
  AND routes.container_name = :'"'"'container_name'"'"'
  AND routes.status = '"'"'active'"'"';'
  local primary_pod=""
  local database=""
  if cluster_mode_enabled; then
    primary_pod="$(agent_redeploy_cnpg_primary_pod)"
    database="$(read_env_value POSTGRES_DB "${RUNTIME_ENV}")"
    [[ -n "${primary_pod}" && -n "${database}" ]] || return 1
  fi
  while ((SECONDS < deadline)); do
    if [[ -n "${primary_pod}" ]]; then
      count="$(
        printf '%s\n' "${query}" | k3s_kubectl -n "${K3S_NAMESPACE}" exec -i "${primary_pod}" -c postgres -- \
          psql "--dbname=${database}" -v "worker_guid=${worker_guid}" -v "container_name=${container_name}" -Atq \
          2>>"${BUILD_LOG}" | tr -d '[:space:]'
      )" || true
    else
      count="$(
        printf '%s\n' "${query}" | k3s_kubectl -n "${K3S_NAMESPACE}" exec -i postgres-db-0 -c postgres-db -- sh -c \
          'PGPASSWORD="$POSTGRES_PASSWORD" psql -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v "worker_guid=$1" -v "container_name=$2" -Atq' \
          _ "${worker_guid}" "${container_name}" 2>>"${BUILD_LOG}" | tr -d '[:space:]'
      )" || true
    fi
    [[ "${count}" == "1" ]] && return 0
    sleep 1
  done
  return 1
}

agent_redeploy_candidate_name() {
  local old_pod="$1"
  local target_image="$2"
  local digest="${target_image##*:sha-}"
  local prefix="${old_pod:0:47}"
  prefix="${prefix%-}"
  printf '%s-next-%s\n' "${prefix}" "${digest:0:8}"
}

agent_redeploy_render_candidate() {
  local old_pod="$1"
  local candidate_pod="$2"
  local revision="$3"
  local target_image="$4"
  local artifact_id="$5"
  local build_id="$6"
  local compiled_at="$7"
  local source_file=""
  source_file="$(mktemp "${DEPLOY_DIR}/agent-redeploy-source.XXXXXX.json")"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" get "pod/${old_pod}" -o json > "${source_file}" 2>>"${BUILD_LOG}"; then
    rm -f "${source_file}"
    return 1
  fi
  python3 - "${source_file}" "${candidate_pod}" "${revision}" "${target_image}" "${artifact_id}" "${build_id}" "${compiled_at}" <<'PY'
import copy
import json
import pathlib
import sys

source = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
candidate_name, revision, image, artifact_id, build_id, compiled_at = sys.argv[2:]
metadata = source.get("metadata") or {}
labels = copy.deepcopy(metadata.get("labels") or {})
annotations = copy.deepcopy(metadata.get("annotations") or {})
labels["borealis.io/redeploy-agent-candidate"] = "true"
labels["borealis.io/redeploy-agent-revision"] = revision
annotations["borealis.io/redeploy-agent-source-pod"] = str(metadata.get("name") or "")
annotations["borealis.io/redeploy-agent-artifact-id"] = artifact_id
annotations["borealis.io/redeploy-agent-build-id"] = build_id
annotations["borealis.io/redeploy-agent-compiled-at"] = compiled_at
annotations["borealis.io/image-ref"] = image
spec = copy.deepcopy(source.get("spec") or {})
for key in ("nodeName", "serviceAccount"):
    spec.pop(key, None)
for container in spec.get("initContainers") or []:
    container["image"] = image
for container in spec.get("containers") or []:
    if str(container.get("name") or "") != "site-worker":
        continue
    container["image"] = image
    for env in container.get("env") or []:
        if str(env.get("name") or "") == "BOREALIS_SITE_WORKER_CONTAINER_NAME":
            env["value"] = candidate_name
    container["readinessProbe"] = {
        "httpGet": {"path": "/ready", "port": "remote-ops"},
        "periodSeconds": 2,
        "timeoutSeconds": 1,
        "failureThreshold": 3,
    }
    container["livenessProbe"] = {
        "httpGet": {"path": "/live", "port": "remote-ops"},
        "initialDelaySeconds": 130,
        "periodSeconds": 10,
        "timeoutSeconds": 2,
        "failureThreshold": 3,
    }
    container["startupProbe"] = {
        "httpGet": {"path": "/startup", "port": "remote-ops"},
        "periodSeconds": 2,
        "timeoutSeconds": 1,
        "failureThreshold": 60,
    }
candidate = {
    "apiVersion": "v1",
    "kind": "Pod",
    "metadata": {
        "name": candidate_name,
        "namespace": str(metadata.get("namespace") or "borealis"),
        "labels": labels,
        "annotations": annotations,
    },
    "spec": spec,
}
print(json.dumps(candidate, separators=(",", ":")))
PY
  local status=$?
  rm -f "${source_file}"
  return "${status}"
}

agent_redeploy_scheduler_deployments() {
  if cluster_mode_enabled; then
    local names=""
    names="$(
      k3s_kubectl -n "${K3S_NAMESPACE}" get deployments \
        -l 'app.kubernetes.io/name=job-scheduler,borealis.io/node-workload=true,borealis.io/update-candidate=false' \
        -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>>"${BUILD_LOG}"
    )" || return 1
    [[ -n "${names}" ]] || return 1
    printf '%s\n' "${names}" | awk 'NF && !seen[$0]++'
    return 0
  fi
  printf '%s\n' job-scheduler
}

agent_redeploy_scheduler_desired_image() {
  local deployments=()
  local deployment_output=""
  deployment_output="$(agent_redeploy_scheduler_deployments)" || return 1
  mapfile -t deployments <<<"${deployment_output}"
  ((${#deployments[@]} > 0)) || return 1
  local deployment=""
  local image=""
  local expected=""
  for deployment in "${deployments[@]}"; do
    image="$(
      k3s_kubectl -n "${K3S_NAMESPACE}" get "deployment/${deployment}" \
        -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="BOREALIS_SITE_WORKER_IMAGE")].value}' \
        2>>"${BUILD_LOG}"
    )" || return 1
    [[ -n "${image}" ]] || return 1
    if [[ -n "${expected}" && "${image}" != "${expected}" ]]; then
      printf '[%s] Scheduler worker-image mismatch: %s has %s; expected %s.\n' \
        "$(date +%FT%T)" "${deployment}" "${image}" "${expected}" >>"${BUILD_LOG}"
      return 1
    fi
    expected="${image}"
  done
  printf '%s\n' "${expected}"
}

agent_redeploy_pause_schedulers() {
  AGENT_REDEPLOY_SCHEDULERS=()
  AGENT_REDEPLOY_SCHEDULER_REPLICAS=()
  local deployment_output=""
  deployment_output="$(agent_redeploy_scheduler_deployments)" || return 1
  mapfile -t AGENT_REDEPLOY_SCHEDULERS <<<"${deployment_output}"
  ((${#AGENT_REDEPLOY_SCHEDULERS[@]} > 0)) || return 1
  local deployment=""
  local replicas=""
  for deployment in "${AGENT_REDEPLOY_SCHEDULERS[@]}"; do
    replicas="$(
      k3s_kubectl -n "${K3S_NAMESPACE}" get "deployment/${deployment}" -o jsonpath='{.spec.replicas}' \
        2>>"${BUILD_LOG}"
    )" || return 1
    [[ "${replicas}" =~ ^[0-9]+$ ]] || return 1
    AGENT_REDEPLOY_SCHEDULER_REPLICAS["${deployment}"]="${replicas}"
  done
  AGENT_REDEPLOY_SCHEDULER_PAUSED=1
  for deployment in "${AGENT_REDEPLOY_SCHEDULERS[@]}"; do
    k3s_kubectl -n "${K3S_NAMESPACE}" scale "deployment/${deployment}" --replicas=0 >>"${BUILD_LOG}" 2>&1 \
      || return 1
    k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/${deployment}" --timeout=120s >>"${BUILD_LOG}" 2>&1 \
      || return 1
  done
}

agent_redeploy_set_scheduler_worker_image() {
  local image="$1"
  local deployments=("${AGENT_REDEPLOY_SCHEDULERS[@]}")
  if ((${#deployments[@]} == 0)); then
    local deployment_output=""
    deployment_output="$(agent_redeploy_scheduler_deployments)" || return 1
    mapfile -t deployments <<<"${deployment_output}"
  fi
  ((${#deployments[@]} > 0)) || return 1
  local deployment=""
  local replicas=""
  for deployment in "${deployments[@]}"; do
    k3s_kubectl -n "${K3S_NAMESPACE}" set env "deployment/${deployment}" \
      "BOREALIS_SITE_WORKER_IMAGE=${image}" >>"${BUILD_LOG}" 2>&1 || return 1
    replicas="$(
      k3s_kubectl -n "${K3S_NAMESPACE}" get "deployment/${deployment}" -o jsonpath='{.spec.replicas}' \
        2>>"${BUILD_LOG}"
    )" || return 1
    [[ "${replicas}" =~ ^[0-9]+$ ]] || return 1
    if ((replicas > 0)); then
      k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/${deployment}" --timeout=120s >>"${BUILD_LOG}" 2>&1 \
        || return 1
    fi
  done
}

agent_redeploy_restore_schedulers() {
  ((${#AGENT_REDEPLOY_SCHEDULERS[@]} > 0)) || return 1
  local deployment=""
  local replicas=""
  for deployment in "${AGENT_REDEPLOY_SCHEDULERS[@]}"; do
    replicas="${AGENT_REDEPLOY_SCHEDULER_REPLICAS[${deployment}]:-}"
    [[ "${replicas}" =~ ^[0-9]+$ ]] || return 1
    k3s_kubectl -n "${K3S_NAMESPACE}" scale "deployment/${deployment}" "--replicas=${replicas}" >>"${BUILD_LOG}" 2>&1 \
      || return 1
    if ((replicas > 0)); then
      k3s_kubectl -n "${K3S_NAMESPACE}" rollout status "deployment/${deployment}" --timeout=120s >>"${BUILD_LOG}" 2>&1 \
        || return 1
    fi
  done
  AGENT_REDEPLOY_SCHEDULER_PAUSED=0
}

agent_redeploy_exit_trap() {
  local original_status="${1:-1}"
  trap - EXIT
  set +o errexit
  if [[ "${AGENT_REDEPLOY_ACTIVE}" -eq 1 && "${original_status}" -ne 0 ]]; then
    printf '[%s] Agent binary redeploy failed; entering readiness-first recovery.\n' "$(date +%FT%T)" >>"${BUILD_LOG}"
    if [[ "${AGENT_REDEPLOY_COMMIT_STARTED}" -eq 0 ]]; then
      local service=""
      local candidate=""
      local old_pod=""
      local old_revision=""
      local candidates_removed=1
      for service in "${AGENT_REDEPLOY_SERVICES[@]}"; do
        old_pod="${AGENT_REDEPLOY_OLD_POD_BY_SERVICE[${service}]:-}"
        old_revision="${AGENT_REDEPLOY_OLD_REVISION_BY_SERVICE[${service}]:-}"
        if [[ "${AGENT_REDEPLOY_CUTOVER_BY_SERVICE[${service}]:-0}" -eq 1 && -n "${old_revision}" ]]; then
          agent_redeploy_patch_service_revision "${service}" "${old_revision}" || true
          agent_redeploy_wait_for_service_endpoint "${service}" "${old_pod}" 30 || true
        fi
      done
      for service in "${AGENT_REDEPLOY_SERVICES[@]}"; do
        candidate="${AGENT_REDEPLOY_CANDIDATE_BY_SERVICE[${service}]:-}"
        if [[ -n "${candidate}" ]] \
          && ! k3s_kubectl -n "${K3S_NAMESPACE}" delete "pod/${candidate}" \
            --ignore-not-found=true --wait=true --timeout=90s >>"${BUILD_LOG}" 2>&1; then
          candidates_removed=0
        fi
      done
      if [[ "${candidates_removed}" -eq 1 ]]; then
        for service in "${AGENT_REDEPLOY_SERVICES[@]}"; do
          if [[ -z "${AGENT_REDEPLOY_ORIGINAL_REVISION_BY_SERVICE[${service}]:-}" ]]; then
            agent_redeploy_remove_service_revision "${service}" || true
            old_pod="${AGENT_REDEPLOY_OLD_POD_BY_SERVICE[${service}]:-}"
            [[ "${AGENT_REDEPLOY_OLD_LABEL_ADDED_BY_SERVICE[${service}]:-0}" -eq 1 ]] \
              && k3s_kubectl -n "${K3S_NAMESPACE}" label "pod/${old_pod}" borealis.io/redeploy-agent-revision- >>"${BUILD_LOG}" 2>&1 || true
          fi
        done
      else
        printf '[%s] Candidate deletion incomplete; retaining old-revision Service selectors for traffic safety.\n' \
          "$(date +%FT%T)" >>"${BUILD_LOG}"
      fi
      IMAGE_TAGS[site-worker]="${AGENT_REDEPLOY_PREVIOUS_IMAGE}"
      BOREALIS_SITE_WORKER_IMAGE_ALLOWLIST_EXTRA=""
      ensure_borealis_operator_bridge || true
      if [[ "${AGENT_REDEPLOY_SCHEDULER_PAUSED}" -eq 1 ]]; then
        BOREALIS_JOB_SCHEDULER_REPLICAS_OVERRIDE=0
        ensure_k3s_job_scheduler "${AGENT_REDEPLOY_MODE}" || true
        unset BOREALIS_JOB_SCHEDULER_REPLICAS_OVERRIDE
        agent_redeploy_set_scheduler_worker_image "${AGENT_REDEPLOY_PREVIOUS_IMAGE}" || true
        agent_redeploy_restore_schedulers || true
      fi
      log_status "site-worker" "Rolled Back - Old Workers Retained" "${C_YELLOW}"
    else
      local desired_image=""
      desired_image="$(agent_redeploy_scheduler_desired_image || true)"
      if [[ "${desired_image}" == "${AGENT_REDEPLOY_TARGET_IMAGE}" ]]; then
        agent_redeploy_restore_schedulers || true
      else
        printf '[%s] Scheduler remains paused because desired worker image is %s, expected %s.\n' \
          "$(date +%FT%T)" "${desired_image:-unknown}" "${AGENT_REDEPLOY_TARGET_IMAGE}" >>"${BUILD_LOG}"
      fi
      log_status "site-worker" "Cutover Committed - Inspect Recovery Log" "${C_YELLOW}"
    fi
  fi
  dashboard_finish
  return 0
}

redeploy_agent_binaries() {
  if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    command_exists sudo || die "Agent binary redeploy requires root access. Run sudo bash Engine.sh --redeploy-agent-binaries."
    printf '[%s] Elevating Agent binary redeploy through sudo.\n' "$(date +%FT%T)"
    exec sudo bash "${SCRIPT_DIR}/Engine.sh" --redeploy-agent-binaries
  fi
  [[ -f "${RUNTIME_ENV}" && -f "${COMPOSE_ENV}" && -f "${IMAGE_MANIFEST}" ]] \
    || die "Existing Engine deployment state missing. Run normal Engine deploy before Agent binary redeploy."
  [[ -s "${K3S_KUBECONFIG}" ]] || die "K3s kubeconfig missing: ${K3S_KUBECONFIG}"
  AGENT_REDEPLOY_MODE="$(agent_redeploy_runtime_mode)"
  local network_mode=""
  network_mode="$(read_env_value BOREALIS_ENGINE_NETWORK_MODE)"
  network_mode="$(normalize_engine_network_mode "${network_mode:-local}")"
  DASHBOARD_TITLE="Borealis Agent Binary Redeployment"
  log_deploy_header "${AGENT_REDEPLOY_MODE}" "${network_mode}"
  AGENT_REDEPLOY_ACTIVE=1
  trap 'agent_redeploy_exit_trap "$?"' EXIT

  ensure_engine_dependencies
  mkdir -p "${DEPLOY_DIR}"
  : >"${BUILD_LOG}"
  load_existing_image_tags
  AGENT_REDEPLOY_PREVIOUS_IMAGE="$(agent_redeploy_scheduler_desired_image)" \
    || die "Current site-worker image unavailable from job-scheduler Deployment."
  [[ -n "${AGENT_REDEPLOY_PREVIOUS_IMAGE}" ]] || die "Current site-worker image missing from job-scheduler Deployment."

  log_section "Agent Binary Build"
  ensure_engine_agent_install_cache
  BOREALIS_APPEND_BUILD_LOG=1 build_images "${AGENT_REDEPLOY_MODE}" site-worker
  AGENT_REDEPLOY_TARGET_IMAGE="${IMAGE_TAGS[site-worker]:-}"
  [[ -n "${AGENT_REDEPLOY_TARGET_IMAGE}" ]] || die "New site-worker image tag unavailable after build."
  IFS=$'\t' read -r AGENT_REDEPLOY_ARTIFACT_ID AGENT_REDEPLOY_BUILD_ID AGENT_REDEPLOY_COMPILED_AT AGENT_REDEPLOY_ARTIFACT_PATH \
    <<<"$(agent_redeploy_artifact_metadata)"
  agent_redeploy_verify_api_cache_mount "${AGENT_REDEPLOY_ARTIFACT_PATH}" "${AGENT_REDEPLOY_BUILD_ID}" \
    || die "Running API backend cannot see newly published Agent artifact ${AGENT_REDEPLOY_ARTIFACT_ID}."
  log_status "Agent Installer Cache" "Hot-Loaded By API - ${AGENT_REDEPLOY_ARTIFACT_ID}" "${C_GREEN}"
  import_k3s_local_image_into_k3s "site-worker" "${AGENT_REDEPLOY_TARGET_IMAGE}" "site-worker"

  local record_output=""
  if ! record_output="$(agent_redeploy_worker_records "${AGENT_REDEPLOY_TARGET_IMAGE}")"; then
    die "Site-worker inventory is not safe for Agent binary rotation. See preceding error."
  fi
  local records=()
  if [[ -n "${record_output}" ]]; then
    mapfile -t records <<<"${record_output}"
  fi
  if ((${#records[@]} == 0)); then
    BOREALIS_SITE_WORKER_IMAGE_ALLOWLIST_EXTRA="${AGENT_REDEPLOY_PREVIOUS_IMAGE}"
    ensure_borealis_operator_bridge
    ensure_k3s_job_scheduler "${AGENT_REDEPLOY_MODE}"
    agent_redeploy_set_scheduler_worker_image "${AGENT_REDEPLOY_TARGET_IMAGE}" \
      || die "Job scheduler worker image did not update cleanly."
    export_image_manifest_env
    write_image_manifest "${AGENT_REDEPLOY_MODE}"
    BOREALIS_SITE_WORKER_IMAGE_ALLOWLIST_EXTRA=""
    ensure_borealis_operator_bridge
    log_status "site-worker" "Up-to-Date - No Worker Rotation Needed" "${C_GREEN}"
    log_status "Agent Installer Cache" "Published - ${AGENT_REDEPLOY_ARTIFACT_ID}" "${C_GREEN}"
    AGENT_REDEPLOY_ACTIVE=0
    dashboard_finish
    trap dashboard_finish EXIT
    return 0
  fi

  log_section "Safe Worker Rotation"
  agent_redeploy_wait_for_work_drain
  log_status "k3s-job-scheduler" "Pausing Reconciliation" "${C_YELLOW}"
  agent_redeploy_pause_schedulers \
    || die "Job scheduler Deployments did not pause cleanly before worker preparation."
  log_status "k3s-job-scheduler" "Paused - Workers Remain Live" "${C_GREEN}"

  BOREALIS_SITE_WORKER_IMAGE_ALLOWLIST_EXTRA="${AGENT_REDEPLOY_PREVIOUS_IMAGE}"
  ensure_borealis_operator_bridge

  local pod=""
  local service=""
  local worker_guid=""
  local site_id=""
  local remote_ops_port=""
  local remote_desktop_port=""
  local configured_image=""
  local pod_uid=""
  local pod_revision=""
  local original_revision=""
  local old_revision=""
  local candidate_revision="agent-${AGENT_REDEPLOY_TARGET_IMAGE##*:sha-}"
  candidate_revision="${candidate_revision:0:63}"
  local candidate=""
  local candidate_manifest=""
  local cluster_ip=""
  for record in "${records[@]}"; do
    IFS=$'\t' read -r pod service worker_guid site_id remote_ops_port remote_desktop_port configured_image pod_uid pod_revision <<<"${record}"
    AGENT_REDEPLOY_SERVICES+=("${service}")
    AGENT_REDEPLOY_OLD_POD_BY_SERVICE["${service}"]="${pod}"
    AGENT_REDEPLOY_CUTOVER_BY_SERVICE["${service}"]=0
    original_revision="$(agent_redeploy_service_revision "${service}")" \
      || die "Failed to read selector for ${service}."
    AGENT_REDEPLOY_ORIGINAL_REVISION_BY_SERVICE["${service}"]="${original_revision}"
    if [[ -n "${original_revision}" ]]; then
      [[ "${pod_revision}" == "${original_revision}" ]] \
        || die "Service ${service} selector revision does not match active pod ${pod}."
      old_revision="${original_revision}"
      AGENT_REDEPLOY_OLD_LABEL_ADDED_BY_SERVICE["${service}"]=0
    else
      old_revision="legacy-${pod_uid:0:12}"
      k3s_kubectl -n "${K3S_NAMESPACE}" label "pod/${pod}" \
        "borealis.io/redeploy-agent-revision=${old_revision}" --overwrite >>"${BUILD_LOG}" 2>&1 \
        || die "Failed to mark active worker ${pod} before candidate creation."
      AGENT_REDEPLOY_OLD_LABEL_ADDED_BY_SERVICE["${service}"]=1
      agent_redeploy_patch_service_revision "${service}" "${old_revision}" \
        || die "Failed to pin ${service} to active worker ${pod}."
      agent_redeploy_wait_for_service_endpoint "${service}" "${pod}" 30 \
        || die "Service ${service} lost active endpoint while pinning old worker."
    fi
    AGENT_REDEPLOY_OLD_REVISION_BY_SERVICE["${service}"]="${old_revision}"
    cluster_ip="$(agent_redeploy_service_cluster_ip "${service}")"
    [[ -n "${cluster_ip}" && "${cluster_ip}" != "None" ]] || die "Service ${service} has no stable ClusterIP."
    local route_file="${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic/site-worker-${worker_guid}.yml"
    [[ -f "${route_file}" ]] || die "Traefik route file missing for worker ${worker_guid}: ${route_file}"
    grep -Fq "http://${cluster_ip}:${remote_ops_port}" "${route_file}" \
      || die "Traefik route for ${worker_guid} does not target stable Service ${cluster_ip}:${remote_ops_port}."
    grep -Fq "http://${cluster_ip}:${remote_desktop_port}" "${route_file}" \
      || die "Traefik desktop route for ${worker_guid} does not target stable Service ${cluster_ip}:${remote_desktop_port}."

    candidate="$(agent_redeploy_candidate_name "${pod}" "${AGENT_REDEPLOY_TARGET_IMAGE}")"
    AGENT_REDEPLOY_CANDIDATE_BY_SERVICE["${service}"]="${candidate}"
    k3s_kubectl -n "${K3S_NAMESPACE}" get "pod/${candidate}" >/dev/null 2>&1 \
      && die "Candidate pod already exists: ${candidate}"
    candidate_manifest="$(agent_redeploy_render_candidate \
      "${pod}" "${candidate}" "${candidate_revision}" "${AGENT_REDEPLOY_TARGET_IMAGE}" \
      "${AGENT_REDEPLOY_ARTIFACT_ID}" "${AGENT_REDEPLOY_BUILD_ID}" "${AGENT_REDEPLOY_COMPILED_AT}")" \
      || die "Failed to render replacement for ${pod}."
    log_status "${service}" "Creating Candidate ${candidate}" "${C_YELLOW}"
    printf '%s\n' "${candidate_manifest}" | k3s_kubectl create -f - >>"${BUILD_LOG}" 2>&1 \
      || die "Failed to create replacement worker ${candidate}."
    k3s_kubectl -n "${K3S_NAMESPACE}" wait --for=condition=Ready "pod/${candidate}" --timeout=120s >>"${BUILD_LOG}" 2>&1 \
      || die "Replacement worker ${candidate} did not become Ready. Old worker remains live."
    agent_redeploy_wait_for_pod_health "${candidate}" "${remote_ops_port}" "${worker_guid}" "${site_id}" 30 \
      || die "Replacement worker ${candidate} failed direct health check. Old worker remains live."
    log_status "${service}" "Candidate Ready - Old Worker Live" "${C_GREEN}"
  done

  for record in "${records[@]}"; do
    IFS=$'\t' read -r pod service worker_guid site_id remote_ops_port remote_desktop_port configured_image pod_uid pod_revision <<<"${record}"
    candidate="${AGENT_REDEPLOY_CANDIDATE_BY_SERVICE[${service}]}"
    log_status "${service}" "Cutting Over Stable Service" "${C_YELLOW}"
    agent_redeploy_patch_service_revision "${service}" "${candidate_revision}" \
      || die "Failed to point ${service} at ${candidate}. Old worker remains available for rollback."
    AGENT_REDEPLOY_CUTOVER_BY_SERVICE["${service}"]=1
    agent_redeploy_wait_for_service_endpoint "${service}" "${candidate}" 60 \
      || die "Service ${service} did not converge on candidate ${candidate}."
    agent_redeploy_probe_service "${candidate}" "${service}" "${remote_ops_port}" "${worker_guid}" "${site_id}" \
      || die "Service ${service} failed post-cutover HTTP health check."
    log_status "${service}" "Live - Health Check Passed" "${C_GREEN}"
    printf '[%s] %s cut over to %s through unchanged ClusterIP; Traefik route file unchanged.\n' \
      "$(date +%FT%T)" "${service}" "${candidate}" >>"${BUILD_LOG}"
  done

  AGENT_REDEPLOY_COMMIT_STARTED=1
  BOREALIS_JOB_SCHEDULER_REPLICAS_OVERRIDE=0
  ensure_k3s_job_scheduler "${AGENT_REDEPLOY_MODE}"
  unset BOREALIS_JOB_SCHEDULER_REPLICAS_OVERRIDE
  agent_redeploy_set_scheduler_worker_image "${AGENT_REDEPLOY_TARGET_IMAGE}" \
    || die "Paused job scheduler Deployments did not accept new worker image."

  for record in "${records[@]}"; do
    IFS=$'\t' read -r pod service worker_guid site_id remote_ops_port remote_desktop_port configured_image pod_uid pod_revision <<<"${record}"
    candidate="${AGENT_REDEPLOY_CANDIDATE_BY_SERVICE[${service}]}"
    log_status "${service}" "Retiring Old Worker ${pod}" "${C_YELLOW}"
    k3s_kubectl -n "${K3S_NAMESPACE}" delete "pod/${pod}" --wait=true --timeout=90s >>"${BUILD_LOG}" 2>&1 \
      || die "New worker ${candidate} is live, but old worker ${pod} did not retire cleanly."
    agent_redeploy_wait_for_worker_registration "${worker_guid}" "${candidate}" 30 \
      || die "Replacement worker ${candidate} did not restore active scheduler registration after old worker retirement."
    k3s_kubectl -n "${K3S_NAMESPACE}" label "pod/${candidate}" borealis.io/redeploy-agent-candidate- >>"${BUILD_LOG}" 2>&1 \
      || die "Failed to finalize replacement worker label on ${candidate}."
    log_status "${service}" "Complete - Old Worker Removed" "${C_GREEN}"
  done

  export_image_manifest_env
  write_image_manifest "${AGENT_REDEPLOY_MODE}"
  BOREALIS_SITE_WORKER_IMAGE_ALLOWLIST_EXTRA=""
  ensure_borealis_operator_bridge
  ensure_k3s_job_scheduler "${AGENT_REDEPLOY_MODE}"
  agent_redeploy_set_scheduler_worker_image "${AGENT_REDEPLOY_TARGET_IMAGE}" \
    || die "Job scheduler worker image did not remain pinned to new release."
  agent_redeploy_restore_schedulers \
    || die "Job scheduler Deployments did not restore previous replica counts."
  log_status "k3s-job-scheduler" "Ready - New Worker Image Active" "${C_GREEN}"
  log_status "Agent Installer Cache" "Published - ${AGENT_REDEPLOY_ARTIFACT_ID}" "${C_GREEN}"
  log_status "site-worker" "Ready - ${#records[@]} Worker(s) Replaced" "${C_GREEN}"
  AGENT_REDEPLOY_ACTIVE=0
  dashboard_finish
  trap dashboard_finish EXIT
}

k3s_probe_conformance_status() {
  [[ -s "${K3S_PROBE_CONFORMANCE_FILE}" ]] || {
    printf '%s\n' "failed"
    return 0
  }
  local running_version=""
  running_version="$(k3s --version 2>/dev/null | awk 'NR == 1 {print $3}' || true)"
  python3 - "${K3S_PROBE_CONFORMANCE_FILE}" "${running_version}" <<'PY' 2>/dev/null || printf '%s\n' "failed"
import json, pathlib, sys
try:
    value = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
except Exception:
    raise SystemExit(1)
if value.get("id") == "pod-restart-policy-liveness-delay-guard-v1" and value.get("status") == "passed" and value.get("k3s_version") == sys.argv[2] and value.get("trials") == 10:
    print("passed")
else:
    print("failed")
PY
}

engine_release_version() {
  git -C "${SCRIPT_DIR}" tag --points-at HEAD 2>/dev/null \
    | grep -E '^[0-9]{4}\.[0-9]{1,2}\.[0-9]+(\.[0-9]+)?$' \
    | sort -V \
    | tail -n 1 \
    || true
}

ensure_borealis_node_manager() {
  local staged_binary="${CONTAINER_SOURCE_DIR}/api-backend/dist/borealis-node-manager"
  local service_source="${K3S_CLUSTER_ASSET_DIR}/node-manager.service"
  [[ -x "${staged_binary}" ]] || die "Node-manager binary missing after API backend build: ${staged_binary}"
  [[ -f "${service_source}" ]] || die "Node-manager systemd unit missing: ${service_source}"
  run_privileged install -d -m 0750 -o root -g root "$(dirname -- "${BOREALIS_NODE_MANAGER_TOKEN_FILE}")"
  # systemd rejects a missing ReadWritePaths target before node-manager starts.
  # Joined K3s servers do not create the image pre-import leaf until first use.
  run_privileged install -d -m 0755 -o root -g root "${K3S_IMAGE_IMPORT_DIR}"
  run_privileged install -m 0750 -o root -g root "${staged_binary}" "${BOREALIS_NODE_MANAGER_BINARY}"
  if ! run_privileged test -s "${BOREALIS_NODE_MANAGER_TOKEN_FILE}"; then
    local token_file=""
    token_file="$(mktemp)"
    umask 077
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "${token_file}"
    run_privileged install -m 0640 -o root -g 64646 -D "${token_file}" "${BOREALIS_NODE_MANAGER_TOKEN_FILE}"
    find "$(dirname -- "${token_file}")" -maxdepth 1 -type f -name "$(basename -- "${token_file}")" -delete
  fi
  run_privileged chown root:64646 "${BOREALIS_NODE_MANAGER_TOKEN_FILE}"
  run_privileged chmod 0640 "${BOREALIS_NODE_MANAGER_TOKEN_FILE}"
  run_privileged install -m 0644 -o root -g root "${service_source}" "/etc/systemd/system/${BOREALIS_NODE_MANAGER_SERVICE}"
  run_privileged systemctl daemon-reload
  run_privileged systemctl enable "${BOREALIS_NODE_MANAGER_SERVICE}"
  run_privileged systemctl restart "${BOREALIS_NODE_MANAGER_SERVICE}"
  sleep 2
  if ! run_privileged systemctl is-active --quiet "${BOREALIS_NODE_MANAGER_SERVICE}"; then
    run_privileged systemctl status "${BOREALIS_NODE_MANAGER_SERVICE}" --no-pager >> "${BUILD_LOG}" 2>&1 || true
    die "Node-manager service did not remain active. See ${BUILD_LOG}."
  fi
}

ensure_cluster_wireguard_routes() {
  cluster_mode_enabled || return 0
  local route_image=""
  route_image="$(service_image_tag_or_previous wireguard-tunnel borealis-engine/wireguard-tunnel:local)"
  [[ "${route_image}" =~ :sha-[0-9a-f]{12,64}$ || "${route_image}" =~ @sha256:[0-9a-f]{64}$ ]] \
    || die "Cluster WireGuard route DaemonSet requires immutable image; saw ${route_image}."
  local edge_vip=""
  edge_vip="$(k3s_kubectl -n "${K3S_NAMESPACE}" get borealiscluster/borealis -o jsonpath='{.spec.edgeVIP}')"
  [[ "${edge_vip}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] \
    || die "Enabled cluster has invalid edge VIP for WireGuard route reconciliation."
  local route_manifest=""
  route_manifest="$(mktemp "${DEPLOY_DIR}/wireguard-route-daemon.XXXXXX.yaml")"
  sed \
    -e "s|borealis-engine/wireguard-tunnel:sha-000000000000|${route_image}|g" \
    -e "s|192.0.2.2|${edge_vip}|g" \
    "${K3S_CLUSTER_ASSET_DIR}/wireguard-route-daemonset.yaml" > "${route_manifest}"
  k3s_kubectl apply --server-side --field-manager=borealis-engine -f "${route_manifest}" >> "${BUILD_LOG}" 2>&1
  find "$(dirname -- "${route_manifest}")" -maxdepth 1 -type f -name "$(basename -- "${route_manifest}")" -delete
  k3s_kubectl -n "${K3S_NAMESPACE}" rollout status daemonset/borealis-wireguard-routes --timeout=180s >> "${BUILD_LOG}" 2>&1 \
    || die "Cluster WireGuard route DaemonSet did not become ready. See ${BUILD_LOG}."
}

ensure_cluster_controller_baseline() {
  local api_image=""
  api_image="$(service_image_tag_or_previous api-backend borealis-engine/api-backend:local)"
  [[ "${api_image}" =~ :sha-[0-9a-f]{12,64}$ || "${api_image}" =~ @sha256:[0-9a-f]{64}$ ]] \
    || die "Cluster controller requires immutable API image; saw ${api_image}."
  k3s_kubectl apply --server-side --field-manager=borealis-engine -f "${K3S_CLUSTER_ASSET_DIR}/crds.yaml" >> "${BUILD_LOG}" 2>&1
  local manifest_file=""
  manifest_file="$(mktemp "${DEPLOY_DIR}/cluster-controller.XXXXXX.yaml")"
  sed "s|borealis-engine/api-backend:sha-000000000000|${api_image}|g" "${K3S_CLUSTER_ASSET_DIR}/controller.yaml" > "${manifest_file}"
  k3s_kubectl apply --server-side --field-manager=borealis-engine -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1
  find "$(dirname -- "${manifest_file}")" -maxdepth 1 -type f -name "$(basename -- "${manifest_file}")" -delete
  k3s_kubectl apply --server-side --field-manager=borealis-engine -f "${K3S_CLUSTER_ASSET_DIR}/application-availability.yaml" >> "${BUILD_LOG}" 2>&1
  if cluster_mode_enabled; then
    ensure_cluster_wireguard_routes
    k3s_kubectl -n "${K3S_NAMESPACE}" scale deployment/borealis-cluster-controller --replicas=0 >> "${BUILD_LOG}" 2>&1
    local controller_clones=""
    controller_clones="$(k3s_kubectl -n "${K3S_NAMESPACE}" get deployment \
      -l 'borealis.io/node-workload=true,app.kubernetes.io/name=borealis-cluster-controller' \
      -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.availableReplicas}{"\n"}{end}' 2>>"${BUILD_LOG}" || true)"
    awk '$2 + 0 >= 1 { ready = 1 } END { exit !ready }' <<< "${controller_clones}" \
      || die "Enabled cluster has no available per-node controller replica. See ${BUILD_LOG}."
    return 0
  fi
  k3s_kubectl -n "${K3S_NAMESPACE}" rollout status deployment/borealis-cluster-controller --timeout=180s >> "${BUILD_LOG}" 2>&1 \
    || die "Cluster controller did not become ready. See ${BUILD_LOG}."
}

ensure_cluster_dependency_probe_guards() {
  cluster_mode_enabled || return 0
  local cnpg_patch='{"spec":{"template":{"metadata":{"annotations":{"borealis.io/liveness-startup-guard":"40"}},"spec":{"containers":[{"name":"manager","livenessProbe":{"initialDelaySeconds":40}}]}}}}'
  local snapshot_patch='{"spec":{"template":{"metadata":{"annotations":{"borealis.io/liveness-startup-guard":"70"}},"spec":{"containers":[{"name":"snapshot-controller","livenessProbe":{"initialDelaySeconds":70}}]}}}}'
  local vip_patch='{"spec":{"template":{"metadata":{"annotations":{"borealis.io/liveness-startup-guard":"40"}},"spec":{"containers":[{"name":"kube-vip","livenessProbe":{"initialDelaySeconds":40}}]}}}}'
  local postgres_patch='{"spec":{"probes":{"liveness":{"initialDelaySeconds":330,"isolationCheck":{"enabled":true}}}}}'
  local resource=""

  k3s_kubectl -n cnpg-system patch deployment/cnpg-controller-manager --type=strategic -p "${cnpg_patch}" >> "${BUILD_LOG}" 2>&1 \
    || die "CloudNativePG operator liveness startup guard could not be applied. See ${BUILD_LOG}."
  k3s_kubectl -n kube-system patch deployment/snapshot-controller --type=strategic -p "${snapshot_patch}" >> "${BUILD_LOG}" 2>&1 \
    || die "Snapshot controller liveness startup guard could not be applied. See ${BUILD_LOG}."
  for resource in kube-vip-borealis-control kube-vip-borealis-edge; do
    k3s_kubectl -n kube-system patch "daemonset/${resource}" --type=strategic -p "${vip_patch}" >> "${BUILD_LOG}" 2>&1 \
      || die "${resource} liveness startup guard could not be applied. See ${BUILD_LOG}."
  done
  k3s_kubectl -n "${K3S_NAMESPACE}" patch cluster.postgresql.cnpg.io/borealis-postgres --type=merge -p "${postgres_patch}" >> "${BUILD_LOG}" 2>&1 \
    || die "CloudNativePG instance liveness startup guard could not be applied. See ${BUILD_LOG}."

  k3s_kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=5m >> "${BUILD_LOG}" 2>&1 \
    || die "CloudNativePG operator probe-guard rollout failed. See ${BUILD_LOG}."
  k3s_kubectl -n kube-system rollout status deployment/snapshot-controller --timeout=5m >> "${BUILD_LOG}" 2>&1 \
    || die "Snapshot controller probe-guard rollout failed. See ${BUILD_LOG}."
  for resource in kube-vip-borealis-control kube-vip-borealis-edge; do
    k3s_kubectl -n kube-system rollout status "daemonset/${resource}" --timeout=5m >> "${BUILD_LOG}" 2>&1 \
      || die "${resource} probe-guard rollout failed. See ${BUILD_LOG}."
  done

  local desired_instances=""
  local deadline=$((SECONDS + 900))
  desired_instances="$(k3s_kubectl -n "${K3S_NAMESPACE}" get cluster.postgresql.cnpg.io/borealis-postgres -o jsonpath='{.spec.instances}')"
  while ((SECONDS < deadline)); do
    if k3s_kubectl -n "${K3S_NAMESPACE}" get pods -l cnpg.io/cluster=borealis-postgres -o json 2>>"${BUILD_LOG}" \
      | python3 -c '
import json, sys
expected = int(sys.argv[1])
pods = json.load(sys.stdin).get("items") or []
valid = 0
for pod in pods:
    containers = (pod.get("spec") or {}).get("containers") or []
    postgres = next((item for item in containers if item.get("name") == "postgres"), {})
    delay = ((postgres.get("livenessProbe") or {}).get("initialDelaySeconds"))
    ready = any(item.get("type") == "Ready" and item.get("status") == "True" for item in ((pod.get("status") or {}).get("conditions") or []))
    valid += int(delay == 330 and ready)
raise SystemExit(0 if len(pods) == expected and valid == expected else 1)
' "${desired_instances}"; then
      return 0
    fi
    sleep 3
  done
  die "CloudNativePG instance probe-guard rollout did not become ready within 15 minutes. See ${BUILD_LOG}."
}

cluster_mode_enabled() {
  k3s_cluster_installed || return 1
  [[ -s "${K3S_KUBECONFIG}" ]] || return 1
  [[ "$(k3s_kubectl -n "${K3S_NAMESPACE}" get borealiscluster/borealis -o jsonpath='{.spec.activeSize}' 2>/dev/null || true)" =~ ^(1|3|5)$ ]]
}

cluster_api_request() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  local api_url="${BOREALIS_CLUSTER_API_URL:-}"
  local token="${BOREALIS_CLUSTER_ADMIN_TOKEN:-}"
  [[ -n "${api_url}" && -n "${token}" ]] \
    || die "Clustered CLI operation requires BOREALIS_CLUSTER_API_URL and recent BOREALIS_CLUSTER_ADMIN_TOKEN."
  local auth_file=""
  auth_file="$(mktemp)"
  chmod 0600 "${auth_file}"
  printf 'Authorization: Bearer %s\n' "${token}" > "${auth_file}"
  local curl_args=(-fsS --proto '=https' --tlsv1.2 -X "${method}" -H "@${auth_file}" -H 'Accept: application/json')
  if [[ -n "${body}" ]]; then
    curl_args+=(-H 'Content-Type: application/json' --data-binary "${body}")
  fi
  if [[ -n "${BOREALIS_CLUSTER_CA_FILE:-}" ]]; then
    curl_args+=(--cacert "${BOREALIS_CLUSTER_CA_FILE}")
  fi
  curl "${curl_args[@]}" "${api_url%/}${path}"
  local status=$?
  find "$(dirname -- "${auth_file}")" -maxdepth 1 -type f -name "$(basename -- "${auth_file}")" -delete
  return "${status}"
}

cluster_wait_for_operation() {
  local operation_id="$1"
  local attempt=""
  local snapshot=""
  local state=""
  local transient_failures=0
  for attempt in {1..1800}; do
    if ! snapshot="$(cluster_api_request GET /api/server/cluster)"; then
      transient_failures=$((transient_failures + 1))
      if [[ "${transient_failures}" -eq 1 ]]; then
        printf 'Cluster API temporarily unavailable while operation %s continues; reconnecting.\n' "${operation_id}" >&2
      fi
      if [[ "${transient_failures}" -ge 30 ]]; then
        die "Lost Cluster API for 60 seconds while operation ${operation_id} continues server-side. Inspect Cluster Management before retrying anything."
      fi
      sleep 2
      continue
    fi
    transient_failures=0
    state="$(python3 -c 'import json,sys
operation_id=sys.argv[1]
payload=json.load(sys.stdin)
print(next((str(item.get("state") or "unknown") for item in (payload.get("operations") or []) if item.get("id") == operation_id), "missing"))' "${operation_id}" <<< "${snapshot}")"
    case "${state}" in
      succeeded) return 0 ;;
      failed|cancelled|missing) die "Cluster operation ${operation_id} ended as ${state}." ;;
    esac
    sleep 2
  done
  die "Cluster operation ${operation_id} did not complete within 60 minutes."
}

cluster_hmr_guard() {
  local mode="$1"
  local service="${2:-all}"
  cluster_mode_enabled || return 0
  [[ "${mode}" == "dev" || "${mode}" == "prod" ]] || return 0
  local snapshot=""
  snapshot="$(cluster_api_request GET /api/server/cluster)"
  local node_name="${BOREALIS_CLUSTER_NODE_NAME:-$(hostname -s | tr '[:upper:]' '[:lower:]')}"
  local state_line=""
  state_line="$(python3 -c 'import json,sys
payload=json.load(sys.stdin)
node_name=sys.argv[1]
node_id=next((str(node.get("id") or "") for node in (payload.get("nodes") or []) if node.get("node_name") == node_name), "")
hmr=payload.get("hmr") or {}
print("\t".join((node_id,str(hmr.get("state") or "inactive"),str(hmr.get("node_id") or ""))))' "${node_name}" <<< "${snapshot}")"
  local node_id=""
  local hmr_state=""
  local hmr_node_id=""
  IFS=$'\t' read -r node_id hmr_state hmr_node_id <<< "${state_line}"
  [[ -n "${node_id}" ]] || die "Current node ${node_name} is not an active Engine cluster node."
  if [[ "${mode}" == "dev" && "${hmr_state}" == "active" && "${hmr_node_id}" == "${node_id}" ]]; then
    return 0
  fi
  if [[ "${mode}" == "prod" && "${hmr_state}" != "active" ]]; then
    die "Cluster production deploys require Cluster Management Update Node/Update All. Direct deploy prod is blocked."
  fi

  local confirmation=""
  local endpoint=""
  local body=""
  if [[ "${mode}" == "dev" ]]; then
    printf '%s\n' "This moves all Borealis application traffic to this node and places every other Engine node in drained standby. Cluster loses application HA until production mode is restored." >&2
    if [[ -t 0 ]]; then
      read -r -p "Type ENABLE HMR to continue: " confirmation
      [[ "${confirmation}" == "ENABLE HMR" ]] || die "HMR activation cancelled."
    else
      [[ "${CLUSTER_NON_HA_ACKNOWLEDGED}" -eq 1 ]] || die "Non-interactive clustered DEV mode requires --acknowledge-cluster-non-ha."
    fi
    endpoint="/api/server/cluster/hmr/start"
    body="$(printf '{\"node_id\":\"%s\",\"confirmation\":\"ENABLE HMR\"}' "${node_id}")"
  else
    endpoint="/api/server/cluster/hmr/exit"
    body='{"confirmation":"EXIT HMR"}'
  fi
  local response=""
  response="$(cluster_api_request POST "${endpoint}" "${body}")"
  local operation_id=""
  operation_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("operation_id") or "")' <<< "${response}")"
  [[ "${operation_id}" =~ ^[0-9a-f-]{36}$ ]] || die "Cluster HMR request did not return operation ID."
  cluster_wait_for_operation "${operation_id}"
  if [[ "${mode}" == "prod" ]]; then
    printf 'Pinned production release restored cluster-wide; local HMR source preserved.\n'
    return 10
  fi
  printf 'Cluster entered non-HA HMR mode on %s; starting %s DEV workload.\n' "${node_name}" "${service}"
}

cluster_enable_engine() {
  [[ "${EUID:-$(id -u)}" -eq 0 ]] || exec sudo --preserve-env=BOREALIS_CLUSTER_API_URL,BOREALIS_CLUSTER_ADMIN_TOKEN,BOREALIS_CLUSTER_CA_FILE bash "${SCRIPT_DIR}/Engine.sh" --cluster-enable --control-plane-vip "${CLUSTER_CONTROL_PLANE_VIP}" --edge-vip "${CLUSTER_EDGE_VIP}"
  [[ "$(k3s_probe_conformance_status)" == "passed" ]] || die "Stable K3s probe conformance missing or stale. Run ${K3S_CLUSTER_ASSET_DIR}/run-probe-conformance.sh first."
  [[ -n "${CLUSTER_CONTROL_PLANE_VIP}" && -n "${CLUSTER_EDGE_VIP}" ]] || die "Cluster enable requires --control-plane-vip and --edge-vip."
  [[ -f "${IMAGE_MANIFEST}" ]] || die "Existing Engine deployment missing image manifest."
  load_existing_image_tags
  local api_image="${IMAGE_TAGS[api-backend]:-}"
  [[ -n "${api_image}" ]] || die "Existing immutable API image unavailable."
  if [[ "${BOREALIS_CLUSTER_ENROLL_OPERATION:-0}" != "1" ]]; then
    local node_name="${BOREALIS_CLUSTER_NODE_NAME:-$(hostname -s | tr '[:upper:]' '[:lower:]')}"
    local interface=""
    local management_ip=""
    local architecture=""
    interface="$(ip -4 route show default | awk 'NR == 1 {print $5}')"
    management_ip="$(ip -o -4 address show dev "${interface}" scope global | awk 'NR == 1 {sub(/\/.*/, "", $4); print $4}')"
    case "$(uname -m)" in
      x86_64|amd64) architecture="amd64" ;;
      aarch64|arm64) architecture="arm64" ;;
      *) die "Cluster mode supports amd64 or arm64 only." ;;
    esac
    local body=""
    body="$(python3 - "${CLUSTER_CONTROL_PLANE_VIP}" "${CLUSTER_EDGE_VIP}" "${node_name}" "${management_ip}" "${architecture}" <<'PY'
import json, sys
print(json.dumps({
    "control_plane_vip": sys.argv[1],
    "edge_vip": sys.argv[2],
    "node_name": sys.argv[3],
    "management_ip": sys.argv[4],
    "architecture": sys.argv[5],
    "confirmation": "ENABLE CLUSTER",
}, separators=(",", ":")))
PY
)"
    local response=""
    response="$(cluster_api_request POST /api/server/cluster/enable "${body}")"
    local operation_id=""
    operation_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("operation_id") or "")' <<< "${response}")"
    [[ "${operation_id}" =~ ^[0-9a-f-]{36}$ ]] || die "Cluster enable request did not return operation ID."
    cluster_wait_for_operation "${operation_id}"
    return 0
  fi
  BOREALIS_CLUSTER_API_IMAGE="${api_image}" BOREALIS_CLUSTER_ACTIVE_SIZE=1 "${K3S_CLUSTER_ASSET_DIR}/cluster-node-workflow.sh" enable "${CLUSTER_CONTROL_PLANE_VIP}" "${CLUSTER_EDGE_VIP}"
  ensure_cluster_controller_baseline
}

cluster_prepare_node() {
  [[ "${EUID:-$(id -u)}" -eq 0 ]] || die "Cluster node preparation requires root."
  [[ -n "${K3S_PEER_CIDRS}" ]] || die "Cluster node preparation requires BOREALIS_K3S_PEER_CIDRS covering every current and planned Engine node."
  mkdir -p "${DEPLOY_DIR}"
  touch "${BUILD_LOG}"
  validate_k3s_baseline_settings
  ensure_systemctl_for_k3s
  ensure_k3s_install_dependencies
  ensure_longhorn_node_dependencies
  write_k3s_borealis_config >/dev/null || true
  write_k3s_registries_config >/dev/null || true
  ensure_k3s_api_firewall
  printf 'Cluster node host preparation complete. K3s has not been installed or joined.\n'
}

reconcile_cluster_node_workloads() {
  local service="${1:-}"
  cluster_mode_enabled || return 0
  local node_name="${BOREALIS_CLUSTER_NODE_NAME:-$(hostname -s | tr '[:upper:]' '[:lower:]')}"
  local revision=""
  revision="$(git -c "safe.directory=${SCRIPT_DIR}" -C "${SCRIPT_DIR}" rev-parse HEAD)"
  local args=(
    --node "${node_name}"
    --revision "${revision}"
    --image-manifest "${IMAGE_MANIFEST}"
    --initialize
  )
  [[ -n "${service}" ]] && args+=(--service "${service}")
  python3 "${K3S_CLUSTER_ASSET_DIR}/reconcile-node-workloads.py" "${args[@]}"
}

validate_cluster_node_revision() {
  local action="$1"
  [[ "${CLUSTER_TARGET_REVISION}" =~ ^[0-9a-f]{40}$ ]] || die "Cluster node ${action} requires --revision with lowercase 40-character commit SHA."
  [[ "$(git -c "safe.directory=${SCRIPT_DIR}" -C "${SCRIPT_DIR}" rev-parse HEAD)" == "${CLUSTER_TARGET_REVISION}" ]] || die "Cluster node ${action} revision does not match worktree HEAD."
  cluster_mode_enabled || die "Cluster node ${action} requires enabled Borealis cluster."
}

stage_cluster_node_revision_images() {
  local cluster_runtime_env=""
  cluster_runtime_env="$(mktemp)"
  k3s_kubectl -n "${K3S_NAMESPACE}" get "secret/${BOREALIS_API_BACKEND_RUNTIME_SECRET_NAME}" -o json \
    | python3 -c 'import base64,json,re,sys
payload=json.load(sys.stdin)
for key,value in sorted((payload.get("data") or {}).items()):
    if not re.fullmatch(r"[A-Z][A-Z0-9_]{0,127}", key):
        raise SystemExit("invalid runtime environment key")
    decoded=base64.b64decode(value, validate=True).decode("utf-8")
    if any(char in decoded for char in "\r\n\x00"):
        raise SystemExit("runtime environment value is not single-line")
    print(f"{key}={decoded}")' > "${cluster_runtime_env}"
  [[ -s "${cluster_runtime_env}" ]] || die "Cluster runtime Secret could not hydrate node deployment environment."
  local network_mode=""
  network_mode="$(awk -F= '$1 == "BOREALIS_ENGINE_NETWORK_MODE" {print substr($0, index($0, "=") + 1); exit}' "${cluster_runtime_env}")"
  K3S_PEER_CIDRS="$(awk -F= '$1 == "BOREALIS_K3S_PEER_CIDRS" {print substr($0, index($0, "=") + 1); exit}' "${cluster_runtime_env}")"
  [[ -n "${K3S_PEER_CIDRS}" ]] || die "Cluster runtime environment is missing BOREALIS_K3S_PEER_CIDRS."
  validate_k3s_baseline_settings
  ENGINE_NETWORK_MODE="$(normalize_engine_network_mode "${network_mode}")"
  export BOREALIS_ENGINE_NETWORK_MODE="${ENGINE_NETWORK_MODE}"
  prepare_runtime prod
  run_privileged install -m 0600 -D "${cluster_runtime_env}" "${RUNTIME_ENV}"
  find "$(dirname -- "${cluster_runtime_env}")" -maxdepth 1 -type f -name "$(basename -- "${cluster_runtime_env}")" -delete
  build_images prod
  export_image_manifest_env
  write_image_manifest prod
  local service=""
  for service in "${BUILD_ROLES[@]}"; do
    [[ -n "${IMAGE_TAGS[${service}]:-}" ]] || continue
    import_k3s_local_image_into_k3s "${service}" "${IMAGE_TAGS[${service}]}" "cluster-node-redeploy"
  done
  local marker_temp=""
  marker_temp="$(mktemp "${DEPLOY_DIR}/cluster-staged-revision.XXXXXX")"
  printf '%s\n' "${CLUSTER_TARGET_REVISION}" > "${marker_temp}"
  run_privileged install -m 0600 "${marker_temp}" "${CLUSTER_STAGED_REVISION_FILE}"
  find "$(dirname -- "${marker_temp}")" -maxdepth 1 -type f -name "$(basename -- "${marker_temp}")" -delete
}

cluster_node_stage_revision() {
  validate_cluster_node_revision "image staging"
  stage_cluster_node_revision_images
  BOREALIS_CLUSTER_API_IMAGE="${IMAGE_TAGS[api-backend]}" "${K3S_CLUSTER_ASSET_DIR}/cluster-node-workflow.sh" redeploy
}

cluster_node_redeploy() {
  validate_cluster_node_revision "redeploy"
  local staged_revision=""
  [[ -r "${CLUSTER_STAGED_REVISION_FILE}" ]] && staged_revision="$(tr -d '[:space:]' < "${CLUSTER_STAGED_REVISION_FILE}")"
  if [[ "${staged_revision}" == "${CLUSTER_TARGET_REVISION}" && -f "${IMAGE_MANIFEST}" ]]; then
    load_existing_image_tags
  else
    stage_cluster_node_revision_images
  fi
  [[ -n "${IMAGE_TAGS[api-backend]:-}" ]] || die "Cluster node redeploy staged API image is unavailable."
  BOREALIS_CLUSTER_API_IMAGE="${IMAGE_TAGS[api-backend]}" "${K3S_CLUSTER_ASSET_DIR}/cluster-node-workflow.sh" redeploy
  local node_name="${BOREALIS_CLUSTER_NODE_NAME:-$(hostname -s | tr '[:upper:]' '[:lower:]')}"
  local reconcile_args=(
    --node "${node_name}"
    --revision "${CLUSTER_TARGET_REVISION}"
    --image-manifest "${IMAGE_MANIFEST}"
  )
  case "${BOREALIS_CLUSTER_DEPLOYMENT_MODE:-active}" in
    active) ;;
    candidate) reconcile_args+=(--candidate) ;;
    *) die "BOREALIS_CLUSTER_DEPLOYMENT_MODE must be active or candidate." ;;
  esac
  python3 "${K3S_CLUSTER_ASSET_DIR}/reconcile-node-workloads.py" "${reconcile_args[@]}"
}

render_cluster_schema_phase_job_manifest() {
  local job_name="$1"
  local image="$2"
  local node_name="$3"
  local phase="$4"
  local revision="$5"
  local runtime_uid="$6"
  local runtime_gid="$7"
  cat <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: ${job_name}
  namespace: ${K3S_NAMESPACE}
  labels:
    app.kubernetes.io/name: borealis-cluster-schema
    app.kubernetes.io/part-of: borealis
    app.kubernetes.io/managed-by: Engine.sh
    app.kubernetes.io/component: database
    borealis.io/schema-phase: "${phase}"
  annotations:
    borealis.io/revision: "${revision}"
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 86400
  template:
    metadata:
      labels:
        app.kubernetes.io/name: borealis-cluster-schema
        app.kubernetes.io/part-of: borealis
        app.kubernetes.io/managed-by: Engine.sh
        app.kubernetes.io/component: database
        borealis.io/schema-phase: "${phase}"
      annotations:
        borealis.io/revision: "${revision}"
    spec:
      nodeName: ${node_name}
      restartPolicy: Never
      automountServiceAccountToken: false
      enableServiceLinks: false
      securityContext:
        runAsNonRoot: true
        runAsUser: ${runtime_uid}
        runAsGroup: ${runtime_gid}
        fsGroup: ${runtime_gid}
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: cluster-schema
          image: ${image}
          imagePullPolicy: IfNotPresent
          command:
            - python
            - -u
            - -c
            - |
              import os
              from Data.Engine.database import run_cluster_schema_phase
              changed = run_cluster_schema_phase(
                  os.environ["BOREALIS_DATABASE_URL"],
                  os.environ["BOREALIS_CLUSTER_SCHEMA_PHASE"],
                  os.environ["BOREALIS_CLUSTER_TARGET_REVISION"],
                  progress_callback=lambda table_name: print("BOREALIS_SCHEMA_PROGRESS\t" + str(table_name), flush=True),
              )
              print("BOREALIS_CLUSTER_SCHEMA_PHASE\t" + ("applied" if changed else "already-complete"), flush=True)
          envFrom:
            - secretRef:
                name: ${BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME}
          env:
$(k3s_timezone_env_entries)
            - name: BOREALIS_CLUSTER_SCHEMA_PHASE
              value: "${phase}"
            - name: BOREALIS_CLUSTER_TARGET_REVISION
              value: "${revision}"
            - name: PYTHONPATH
              value: "/opt/Borealis:/opt/Borealis/Data/Engine:/opt/Borealis/Data/Agent"
            - name: HOME
              value: "/tmp"
          resources:
            requests:
              cpu: 75m
              memory: 192Mi
            limits:
              cpu: 1000m
              memory: 512Mi
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: tmp
              mountPath: /tmp
$(k3s_timezone_volume_mount_entries)
            - name: api-backend-root
              mountPath: /opt/Borealis/Engine/Services/api-backend
      volumes:
        - name: tmp
          emptyDir:
            medium: Memory
            sizeLimit: 128Mi
$(k3s_timezone_volume_entries)
        - name: api-backend-root
          emptyDir:
            sizeLimit: 16Mi
EOF
}

cluster_schema_phase() {
  [[ "${EUID:-$(id -u)}" -eq 0 ]] || die "Cluster schema phase requires root."
  [[ "${CLUSTER_SCHEMA_PHASE}" == "expand" || "${CLUSTER_SCHEMA_PHASE}" == "finalize" ]] || die "Cluster schema phase requires --schema-phase expand or finalize."
  [[ "${CLUSTER_TARGET_REVISION}" =~ ^[0-9a-f]{40}$ ]] || die "Cluster schema phase requires --revision with lowercase 40-character commit SHA."
  [[ "$(git -c "safe.directory=${SCRIPT_DIR}" -C "${SCRIPT_DIR}" rev-parse HEAD)" == "${CLUSTER_TARGET_REVISION}" ]] || die "Cluster schema phase revision does not match worktree HEAD."
  cluster_mode_enabled || die "Cluster schema phase requires enabled Borealis cluster."
  local staged_revision=""
  [[ -r "${CLUSTER_STAGED_REVISION_FILE}" ]] && staged_revision="$(tr -d '[:space:]' < "${CLUSTER_STAGED_REVISION_FILE}")"
  [[ "${staged_revision}" == "${CLUSTER_TARGET_REVISION}" ]] || die "Cluster schema phase requires matching staged target images."
  [[ -f "${IMAGE_MANIFEST}" ]] || die "Cluster schema phase requires target image manifest."
  mkdir -p "${DEPLOY_DIR}"
  touch "${BUILD_LOG}"

  local image=""
  image="$(python3 - "${IMAGE_MANIFEST}" <<'PY'
import json
import pathlib
import re
import sys

try:
    payload = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
    image = str((((payload.get("services") or {}).get("site-worker") or {}).get("image") or "")).strip()
except Exception as exc:
    raise SystemExit(f"invalid target image manifest: {exc}")
if len(image) > 255 or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._:/@-]*", image):
    raise SystemExit("invalid target site-worker image reference")
print(image)
PY
)"
  [[ -n "${image}" ]] || die "Cluster schema phase target site-worker image is unavailable."

  local node_name=""
  node_name="${BOREALIS_CLUSTER_NODE_NAME:-$(hostname -s | tr '[:upper:]' '[:lower:]')}"
  [[ "${node_name}" =~ ^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$ ]] || die "Cluster schema phase node name is invalid."
  local job_name="borealis-schema-${CLUSTER_SCHEMA_PHASE}-${CLUSTER_TARGET_REVISION:0:12}"
  local manifest_file=""
  manifest_file="$(mktemp "${DEPLOY_DIR}/cluster-schema-phase.XXXXXX.yaml")"
  chmod 0600 "${manifest_file}" 2>/dev/null || true
  render_cluster_schema_phase_job_manifest \
    "${job_name}" \
    "${image}" \
    "${node_name}" \
    "${CLUSTER_SCHEMA_PHASE}" \
    "${CLUSTER_TARGET_REVISION}" \
    "$(resolve_runtime_owner_uid)" \
    "$(resolve_runtime_owner_gid)" \
    > "${manifest_file}"

  k3s_kubectl -n "${K3S_NAMESPACE}" delete "job/${job_name}" --ignore-not-found=true --wait=true >> "${BUILD_LOG}" 2>&1 || true
  if ! k3s_kubectl apply -f "${manifest_file}" >> "${BUILD_LOG}" 2>&1; then
    rm -f "${manifest_file}"
    die "Failed to create clustered ${CLUSTER_SCHEMA_PHASE} schema Job. See ${BUILD_LOG}."
  fi
  rm -f "${manifest_file}"
  if ! k3s_kubectl -n "${K3S_NAMESPACE}" wait --for=condition=complete "job/${job_name}" --timeout=15m >> "${BUILD_LOG}" 2>&1; then
    k3s_kubectl -n "${K3S_NAMESPACE}" get pods -l "job-name=${job_name}" -o wide >> "${BUILD_LOG}" 2>&1 || true
    k3s_kubectl -n "${K3S_NAMESPACE}" logs "job/${job_name}" --tail=240 >> "${BUILD_LOG}" 2>&1 || true
    die "Clustered ${CLUSTER_SCHEMA_PHASE} schema phase failed. See ${BUILD_LOG}."
  fi
  k3s_kubectl -n "${K3S_NAMESPACE}" logs "job/${job_name}" --tail=240
}

usage() {
  cat <<'EOF'
Usage:
  Engine.sh --network-mode <public|local> deploy [prod|dev]
  Engine.sh --network-mode <public|local> --service <api-backend|job-scheduler|webui-frontend|traefik-edge|postgres-db|remote-desktop-guacd|wireguard-tunnel> <restart|rebuild|reload|reconcile|shadow-import|shadow-db-validate> [prod|dev]
  Engine.sh --network-mode <public|local> [--install-dir PATH] [--repo-url URL] [--release-channel stable|unstable] [--repo-branch REF] deploy [prod|dev]
  Engine.sh --redeploy-agent-binaries
  Engine.sh --cluster-prepare-node
  Engine.sh --cluster-enable --control-plane-vip IPv4 --edge-vip IPv4
  Engine.sh --cluster-stage-revision --revision COMMIT_SHA
  Engine.sh --cluster-node-redeploy --revision COMMIT_SHA
  Engine.sh --cluster-schema-phase --schema-phase <expand|finalize> --revision COMMIT_SHA
EOF
}

main() {
  parse_launch_options "$@"
  local pending_command="${LAUNCH_ARGS[0]:-deploy}"
  case "${pending_command}" in
    -h|--help|help)
      ;;
    *)
      printf 'Starting Borealis Engine Bootstrap\n'
      ensure_gum_bootstrap
      ;;
  esac
  if launch_requires_engine_network_mode "${pending_command}"; then
    require_explicit_engine_network_mode || exit $?
  fi
  sync_and_reexec_if_needed
  set -- "${LAUNCH_ARGS[@]}"
  local command="${1:-deploy}"
  case "${command}" in
    deploy)
      deploy_engine "${2:-prod}"
      ;;
    --service)
      service_action "${2:-}" "${3:-}" "${4:-prod}"
      ;;
    --redeploy-agent-binaries)
      [[ "$#" -eq 1 ]] || die "Usage: Engine.sh --redeploy-agent-binaries"
      redeploy_agent_binaries
      ;;
    --cluster-prepare-node)
      [[ "$#" -eq 1 ]] || die "Usage: Engine.sh --cluster-prepare-node"
      cluster_prepare_node
      ;;
    --cluster-enable)
      [[ "$#" -eq 1 ]] || die "Usage: Engine.sh --cluster-enable --control-plane-vip IPv4 --edge-vip IPv4"
      cluster_enable_engine
      ;;
    --cluster-node-redeploy)
      [[ "$#" -eq 1 ]] || die "Usage: Engine.sh --cluster-node-redeploy --revision COMMIT_SHA"
      cluster_node_redeploy
      ;;
    --cluster-stage-revision)
      [[ "$#" -eq 1 ]] || die "Usage: Engine.sh --cluster-stage-revision --revision COMMIT_SHA"
      cluster_node_stage_revision
      ;;
    --cluster-schema-phase)
      [[ "$#" -eq 1 ]] || die "Usage: Engine.sh --cluster-schema-phase --schema-phase <expand|finalize> --revision COMMIT_SHA"
      cluster_schema_phase
      ;;
    -h|--help|help)
      usage
      ;;
    prod|production|dev|developer)
      deploy_engine "${command}"
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
}

if [[ "${BOREALIS_ENGINE_LIBRARY_MODE:-0}" != "1" ]]; then
  main "$@"
  exit $?
fi
