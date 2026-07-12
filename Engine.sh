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
RUNTIME_ROOT="${SCRIPT_DIR}/Engine"
WEBUI_STAGED_SOURCE_DIR="${CONTAINER_SOURCE_DIR}/webui-frontend/data/web-interface"
WEBUI_RUNTIME_SOURCE_DIR="${RUNTIME_ROOT}/Services/webui-frontend/data/web-interface"
DEPLOY_DIR="${RUNTIME_ROOT}/Deploy"
COMPOSE_ENV="${DEPLOY_DIR}/compose.env"
RUNTIME_ENV="${DEPLOY_DIR}/runtime.env"
WEBUI_ENV="${DEPLOY_DIR}/webui-frontend.env"
IMAGE_MANIFEST="${DEPLOY_DIR}/image-manifest.json"
DEPLOY_MANIFEST="${DEPLOY_DIR}/deploy-manifest.json"
BUILD_LOG="${DEPLOY_DIR}/build.log"
BUILD_CACHE_RETENTION_DAYS=7
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
if [[ -n "${REPO_REF}" ]]; then
  REPO_REF_EXPLICIT=1
fi
SERVICE_ROLES=(
  "docker-proxy"
  "api-backend"
  "site-worker-orchestrator"
  "job-scheduler"
  "webui-frontend"
  "traefik-edge"
  "postgres-db"
  "remote-desktop-guacd"
  "wireguard-tunnel"
)
BUILD_ROLES=(
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
GO_API_BACKEND_BINARY_PREPARED=0

log() {
  printf '[%s] %s\n' "$(date +%FT%T)" "$*"
}

dashboard_static_row() {
  case "$1" in
    "webui-frontend"|"api-backend"|"api-backend > job-scheduler"|"api-backend > job-scheduler > site-worker-orchestrator"|"site-worker"|"remote-desktop-guacd"|"docker-proxy"|"traefik-edge"|"wireguard-tunnel"|"postgres-db"|"Docker Compose"|"Docker Cleanup"|"WebUI Accessible")
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
    "api-backend"|"api-backend > job-scheduler"|"api-backend > job-scheduler > site-worker-orchestrator"|"site-worker"|"remote-desktop-guacd")
      printf '%s\n' "Backend"
      ;;
    "docker-proxy"|"traefik-edge"|"wireguard-tunnel"|"Local CA"|"Local TLS leaf")
      printf '%s\n' "Networking"
      ;;
    "postgres-db"|"Profile")
      printf '%s\n' "Database"
      ;;
    "Docker Compose")
      printf '%s\n' "Reconciliation"
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
    "webui-frontend" \
    "api-backend" \
    "api-backend > job-scheduler" \
    "api-backend > job-scheduler > site-worker-orchestrator" \
    "site-worker" \
    "remote-desktop-guacd" \
    "docker-proxy" \
    "traefik-edge" \
    "wireguard-tunnel" \
    "postgres-db" \
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
    "api-backend > job-scheduler > site-worker-orchestrator")
      printf '%s\n' "Site Worker Orchestrator"
      ;;
    "site-worker")
      printf '%s\n' "Site Worker"
      ;;
    "remote-desktop-guacd")
      printf '%s\n' "Guacamole Remote Desktop"
      ;;
    "docker-proxy")
      printf '%s\n' "Docker Proxy"
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
    "Docker Compose")
      printf '%s\n' "Docker Compose"
      ;;
    "Docker Cleanup")
      printf '%s\n' "Docker Cleanup"
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
  for row in \
    "webui-frontend" \
    "api-backend" \
    "api-backend > job-scheduler" \
    "api-backend > job-scheduler > site-worker-orchestrator" \
    "site-worker" \
    "remote-desktop-guacd" \
    "docker-proxy" \
    "traefik-edge" \
    "wireguard-tunnel" \
    "postgres-db" \
    "Docker Compose" \
    "Docker Cleanup" \
    "WebUI Accessible"; do
    dashboard_render_row "${row}"
  done
  for row in "${DASHBOARD_DYNAMIC_ROWS[@]}"; do
    dashboard_render_row "${row}"
  done
}

dashboard_render() {
  [[ "${DASHBOARD_ACTIVE}" -eq 1 ]] || return 0
  printf '\033[H'
  printf '%bBorealis Engine Deployment%b\n' "${C_BLUE}${C_BOLD}" "${C_RESET}"
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

dashboard_update_status() {
  local subject="$1"
  local status="$2"
  local color="$3"
  if [[ "${subject}" == "Database schema" ]]; then
    subject="postgres-db"
  fi
  dashboard_ensure_row "${subject}"
  if [[ "${DASHBOARD_STATUS[${subject}]:-}" == "${status}" && "${DASHBOARD_COLOR[${subject}]:-}" == "${color}" ]]; then
    return 0
  fi
  DASHBOARD_STATUS["${subject}"]="${status}"
  DASHBOARD_COLOR["${subject}"]="${color}"
  DASHBOARD_UPDATED["${subject}"]="$(dashboard_human_timestamp)"
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
    site-worker-orchestrator)
      printf '%s\n' "api-backend > job-scheduler > site-worker-orchestrator"
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
    job-scheduler)
      status="[Shares API Backend Image] -> ${status}"
      ;;
    site-worker-orchestrator)
      status="[Shares Job Scheduler Image] -> ${status}"
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
    if [[ "${service}" == "job-scheduler" ]]; then
      log_build_status "site-worker-orchestrator" "Ready - Shared Image" "${C_GREEN}"
      printf '[%s] site-worker-orchestrator uses shared image %s\n' "$(date +%FT%T)" "${IMAGE_TAGS[job-scheduler]:-borealis-engine/job-scheduler:local}" >> "${BUILD_LOG}"
    fi
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
  getent "${database}" "${key}" 2>/dev/null | awk -F: -v field="${field}" '{print $field; exit}'
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
    run_privileged groupadd --system --gid "${ENGINE_RUNTIME_GID}" "${ENGINE_RUNTIME_GROUP}" \
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
    run_privileged useradd \
      --system \
      --uid "${ENGINE_RUNTIME_UID}" \
      --gid "${ENGINE_RUNTIME_GROUP}" \
      --home-dir /nonexistent \
      --no-create-home \
      --shell "${nologin_shell}" \
      "${ENGINE_RUNTIME_USER}" \
      || die "Failed to create ${ENGINE_RUNTIME_USER} runtime user."
  fi
}

resolve_docker_socket_gid() {
  local docker_socket="${BOREALIS_DOCKER_SOCKET_PATH:-/var/run/docker.sock}"
  if [[ -S "${docker_socket}" || -e "${docker_socket}" ]]; then
    stat -c '%g' "${docker_socket}" 2>/dev/null || printf '0\n'
    return 0
  fi
  printf '0\n'
}

detect_distro() {
  DISTRO_ID="unknown"
  if [[ -f /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    DISTRO_ID="${ID:-unknown}"
  fi
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
      --install-dir|--repo-url|--ref|--branch|--repo-branch|--repo_branch|--release-channel|--release_channel|--network-mode|--network_mode|--deployment-profile|--deployment_profile)
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
          --ref|--branch|--repo-branch|--repo_branch)
            REPO_REF="$2"
            REPO_REF_EXPLICIT=1
            case "$1" in
              --branch|--repo-branch|--repo_branch) REPO_CHECKOUT_BRANCH="$2" ;;
            esac
            ;;
        esac
        case "$1" in
          --network-mode|--network_mode|--deployment-profile|--deployment_profile) ;;
          *) SYNC_REQUESTED=1 ;;
        esac
        shift 2
        ;;
      --install-dir=*|--repo-url=*|--ref=*|--branch=*|--repo-branch=*|--repo_branch=*|--release-channel=*|--release_channel=*|--network-mode=*|--network_mode=*|--deployment-profile=*|--deployment_profile=*)
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
          --ref|--branch|--repo-branch|--repo_branch)
            REPO_REF="${value}"
            REPO_REF_EXPLICIT=1
            case "${key}" in
              --branch|--repo-branch|--repo_branch) REPO_CHECKOUT_BRANCH="${value}" ;;
            esac
            ;;
        esac
        case "${key}" in
          --network-mode|--network_mode|--deployment-profile|--deployment_profile) ;;
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

compose_base() {
  docker compose \
    --project-name "${PROJECT_NAME}" \
    --env-file "${COMPOSE_ENV}" \
    -f "${COMPOSE_FILE}" \
    "$@"
}

require_docker() {
  command_exists docker || die "Docker Engine CLI missing. Run Engine.sh deploy after installing Docker Engine."
  docker info >/dev/null 2>&1 || die "Docker daemon unreachable. Start Docker Engine and retry."
  docker compose version >/dev/null 2>&1 || die "Docker Compose plugin missing. Install docker compose plugin and retry."
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

dashboard_subject_for_service() {
  case "$1" in
    job-scheduler)
      printf '%s\n' "api-backend > job-scheduler"
      ;;
    site-worker-orchestrator)
      printf '%s\n' "api-backend > job-scheduler > site-worker-orchestrator"
      ;;
    *)
      printf '%s\n' "$1"
      ;;
  esac
}

refresh_compose_service_statuses() {
  local services=("$@")
  if [[ "${#services[@]}" -eq 0 ]]; then
    services=("${SERVICE_ROLES[@]}")
  fi
  local service=""
  for service in "${services[@]}"; do
    local subject
    local container_status
    subject="$(dashboard_subject_for_service "${service}")"
    container_status="$(container_health_status "borealis-engine-${service}")"
    case "${container_status}" in
      healthy)
        log_status "${subject}" "Running - Healthy" "${C_GREEN}"
        ;;
      running)
        log_status "${subject}" "Running" "${C_GREEN}"
        ;;
      starting)
        log_status "${subject}" "Starting" "${C_YELLOW}"
        ;;
      unhealthy)
        log_status "${subject}" "Unhealthy" "${C_RED}"
        ;;
      exited|dead|removing|paused|restarting|created)
        log_status "${subject}" "${container_status}" "${C_RED}"
        ;;
      "")
        log_status "${subject}" "Missing" "${C_RED}"
        ;;
      *)
        log_status "${subject}" "${container_status}" "${C_YELLOW}"
        ;;
    esac
  done
}

compose_service_status_is_transient() {
  case "$1" in
    ""|created|restarting|starting)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

wait_for_compose_services_to_settle() {
  local timeout_seconds="$1"
  shift
  local services=("$@")
  if [[ "${#services[@]}" -eq 0 ]]; then
    services=("${SERVICE_ROLES[@]}")
  fi
  local deadline=$((SECONDS + timeout_seconds))
  local service=""
  local status=""
  local pending=0
  while true; do
    refresh_compose_service_statuses "${services[@]}"
    pending=0
    for service in "${services[@]}"; do
      status="$(container_health_status "borealis-engine-${service}")"
      if compose_service_status_is_transient "${status}"; then
        pending=1
      fi
    done
    if [[ "${pending}" -eq 0 ]]; then
      refresh_compose_service_statuses "${services[@]}"
      return 0
    fi
    if ((SECONDS >= deadline)); then
      printf '[%s] Compose service status settle timed out after %ss for services: %s\n' "$(date +%FT%T)" "${timeout_seconds}" "${services[*]}" >> "${BUILD_LOG}"
      return 1
    fi
    sleep 2
  done
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

ensure_engine_database_schema() {
  local site_worker_image="${IMAGE_TAGS[site-worker]:-}"
  local schema_fifo=""
  local schema_fifo_dir=""
  local schema_exit=0
  local schema_line
  local schema_pid=""
  if [[ -z "${site_worker_image}" ]]; then
    site_worker_image="$(read_env_value BOREALIS_SITE_WORKER_IMAGE)"
  fi
  [[ -n "${site_worker_image}" ]] || die "Unable to resolve site-worker image for Engine database schema initialization."

  log_status "Database schema" "Starting PostgreSQL" "${C_YELLOW}"
  compose_base up -d --no-deps --no-build postgres-db >> "${BUILD_LOG}" 2>&1
  if ! wait_for_postgres_container 150; then
    log_status "postgres-db" "Failed" "${C_RED}"
    die "postgres-db did not become healthy for Engine database schema initialization. See ${BUILD_LOG}."
  fi
  refresh_compose_service_statuses postgres-db

  log_status "Database schema" "Preparing Engine tables" "${C_YELLOW}"
  schema_fifo_dir="$(mktemp -d)"
  schema_fifo="${schema_fifo_dir}/schema-output"
  mkfifo "${schema_fifo}"
  set +o errexit
  docker run --rm \
    --network host \
    --env-file "${COMPOSE_ENV}" \
    -e PYTHONPATH="/opt/Borealis:/opt/Borealis/Data/Engine:/opt/Borealis/Data/Agent" \
    -v "${RUNTIME_ROOT}:/opt/Borealis/Engine" \
    --entrypoint python \
    "${site_worker_image}" \
    -u \
    -c 'import os; from Data.Engine.database import initialise_engine_database; initialise_engine_database(os.environ["BOREALIS_DATABASE_URL"], progress_callback=lambda table_name: print("BOREALIS_SCHEMA_PROGRESS\t" + str(table_name), flush=True))' \
    > "${schema_fifo}" 2>&1 &
  schema_pid="$!"
  while IFS= read -r schema_line; do
    handle_database_schema_output_line "${schema_line}"
  done < "${schema_fifo}"
  wait "${schema_pid}"
  schema_exit="$?"
  rm -rf "${schema_fifo_dir}" || true
  set -o errexit
  if [[ "${schema_exit}" -ne 0 ]]; then
    log_status "Database schema" "Failed" "${C_RED}"
    die "Engine database schema initialization failed. See ${BUILD_LOG}."
  fi
  log_status "Database schema" "Ready" "${C_GREEN}"
  refresh_compose_service_statuses postgres-db
}

ensure_no_host_postgres_conflict() {
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
  run_privileged apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

ensure_engine_dependencies() {
  local needs_install=0
  command_exists python3 || needs_install=1
  if command_exists docker; then
    docker compose version >/dev/null 2>&1 || needs_install=1
  else
    needs_install=1
  fi

  if [[ "${needs_install}" -eq 1 ]]; then
    detect_distro
    case "${DISTRO_ID}" in
      ubuntu|debian|linuxmint|pop)
        install_engine_apt_dependencies
        ;;
      rhel|centos|fedora|rocky|almalinux)
        if command_exists dnf; then
          run_privileged dnf install -y python3 docker docker-compose-plugin
        else
          run_privileged yum install -y python3 docker docker-compose-plugin
        fi
        ;;
      arch)
        run_privileged pacman -Sy --noconfirm python docker docker-compose
        ;;
      opensuse*|sles)
        run_privileged zypper --non-interactive install python3 docker docker-compose
        ;;
      *)
        die "Unsupported distro '${DISTRO_ID}'. Install Python 3, Docker Engine, and Docker Compose plugin manually."
        ;;
    esac
  fi

  if command_exists systemctl; then
    run_privileged systemctl enable --now docker >/dev/null 2>&1 || true
  fi

  require_python
  require_docker
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
  owner_uid="$(resolve_runtime_owner_uid)"
  owner_gid="$(resolve_runtime_owner_gid)"
  [[ "${owner_uid}" =~ ^[0-9]+$ && "${owner_gid}" =~ ^[0-9]+$ ]] || return 0
  [[ "${EUID:-$(id -u)}" -eq 0 ]] || return 0

  local path
  for path in \
    "${RUNTIME_ROOT}/Services/api-backend" \
    "${RUNTIME_ROOT}/Services/postgres-db" \
    "${RUNTIME_ROOT}/Services/traefik-edge" \
    "${RUNTIME_ROOT}/Services/webui-frontend" \
    "${RUNTIME_ROOT}/Services/remote-desktop-guacd" \
    "${RUNTIME_ROOT}/Services/site-worker-orchestrator" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel"; do
    [[ -e "${path}" ]] || continue
    chown -R "${owner_uid}:${owner_gid}" "${path}" 2>/dev/null || true
  done

  chmod 0750 "${RUNTIME_ROOT}/Services/api-backend/secrets" 2>/dev/null || true
  chmod 0750 "${RUNTIME_ROOT}/Services/api-backend/secrets/Auth_Tokens" 2>/dev/null || true
  chmod 0750 "${RUNTIME_ROOT}/Services/api-backend/secrets/Certificates" 2>/dev/null || true
  chmod 0750 "${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets" 2>/dev/null || true
  find "${RUNTIME_ROOT}/Services/api-backend/secrets" "${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets" \
    -type f -exec chmod go-rwx {} + 2>/dev/null || true
  chmod 0775 "${RUNTIME_ROOT}/Services/site-worker-orchestrator/run" 2>/dev/null || true
  chmod 0775 "${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic" 2>/dev/null || true
  chmod 0775 "${RUNTIME_ROOT}/Services/remote-desktop-guacd/logs" 2>/dev/null || true
}

ensure_service_tree() {
  mkdir -p "${DEPLOY_DIR}"
  mkdir -p \
    "${RUNTIME_ROOT}/Services/api-backend/config" \
    "${RUNTIME_ROOT}/Services/api-backend/logs" \
    "${RUNTIME_ROOT}/Services/api-backend/logs/VPN_Tunnel" \
    "${RUNTIME_ROOT}/Services/api-backend/secrets" \
    "${RUNTIME_ROOT}/Services/api-backend/secrets/Auth_Tokens" \
    "${RUNTIME_ROOT}/Services/api-backend/secrets/Certificates" \
    "${RUNTIME_ROOT}/Services/api-backend/cache" \
    "${RUNTIME_ROOT}/Services/api-backend/cache/Ansible/collections" \
    "${RUNTIME_ROOT}/Services/api-backend/cache/Ansible/Generated/Runtime" \
    "${RUNTIME_ROOT}/Services/api-backend/cache/Aurora" \
    "${RUNTIME_ROOT}/Services/postgres-db/state" \
    "${RUNTIME_ROOT}/Services/postgres-db/logs" \
    "${RUNTIME_ROOT}/Services/postgres-db/run" \
    "${RUNTIME_ROOT}/Services/traefik-edge/config" \
    "${RUNTIME_ROOT}/Services/traefik-edge/config/dynamic" \
    "${RUNTIME_ROOT}/Services/traefik-edge/env" \
    "${RUNTIME_ROOT}/Services/traefik-edge/logs" \
    "${RUNTIME_ROOT}/Services/traefik-edge/state" \
    "${RUNTIME_ROOT}/Services/traefik-edge/state/local-ca" \
    "${RUNTIME_ROOT}/Services/traefik-edge/state/local-certs" \
    "${RUNTIME_ROOT}/Services/webui-frontend/data" \
    "${RUNTIME_ROOT}/Services/remote-desktop-guacd/logs" \
    "${RUNTIME_ROOT}/Services/site-worker-orchestrator/run" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel/config" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel/logs" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel/run"
  apply_traefik_dynamic_config_permissions
  chmod 0775 "${RUNTIME_ROOT}/Services/remote-desktop-guacd/logs" 2>/dev/null || true
  apply_runtime_service_ownership
}

seed_webui_runtime_source() {
  [[ -d "${WEBUI_STAGED_SOURCE_DIR}" ]] || die "WebUI staged source missing: ${WEBUI_STAGED_SOURCE_DIR}"
  if [[ -f "${WEBUI_RUNTIME_SOURCE_DIR}/package.json" && "${BOREALIS_REFRESH_WEBUI_RUNTIME_SOURCE:-0}" != "1" ]]; then
    return 0
  fi
  if [[ "${BOREALIS_REFRESH_WEBUI_RUNTIME_SOURCE:-0}" == "1" && -d "${WEBUI_RUNTIME_SOURCE_DIR}" ]]; then
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
    return 0
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
  [[ "$(normalize_engine_deployment_profile "${engine_profile}")" == "internal-only" ]] || {
    printf '%s\n' ""
    return 0
  }
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
      PROFILE_DOCKER_PROXY_MEMORY_LIMIT="256m"
      PROFILE_DOCKER_PROXY_CPU_LIMIT="1.00"
      PROFILE_DOCKER_PROXY_PIDS_LIMIT=128
      PROFILE_API_BACKEND_MEMORY_LIMIT="3g"
      PROFILE_API_BACKEND_CPU_LIMIT="6.00"
      PROFILE_API_BACKEND_PIDS_LIMIT=512
      PROFILE_JOB_SCHEDULER_MEMORY_LIMIT="2g"
      PROFILE_JOB_SCHEDULER_CPU_LIMIT="3.00"
      PROFILE_JOB_SCHEDULER_PIDS_LIMIT=512
      PROFILE_SITE_WORKER_ORCHESTRATOR_MEMORY_LIMIT="2g"
      PROFILE_SITE_WORKER_ORCHESTRATOR_CPU_LIMIT="3.00"
      PROFILE_SITE_WORKER_ORCHESTRATOR_PIDS_LIMIT=512
      PROFILE_SITE_WORKER_MEMORY_LIMIT="512m"
      PROFILE_SITE_WORKER_CPU_LIMIT="2.00"
      PROFILE_SITE_WORKER_PIDS_LIMIT=128
      PROFILE_SERVICE_ACTION_HELPER_MEMORY_LIMIT="3g"
      PROFILE_SERVICE_ACTION_HELPER_CPU_LIMIT="6.00"
      PROFILE_SERVICE_ACTION_HELPER_PIDS_LIMIT=512
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
      PROFILE_DOCKER_PROXY_MEMORY_LIMIT="192m"
      PROFILE_DOCKER_PROXY_CPU_LIMIT="0.75"
      PROFILE_DOCKER_PROXY_PIDS_LIMIT=96
      PROFILE_API_BACKEND_MEMORY_LIMIT="2g"
      PROFILE_API_BACKEND_CPU_LIMIT="4.00"
      PROFILE_API_BACKEND_PIDS_LIMIT=384
      PROFILE_JOB_SCHEDULER_MEMORY_LIMIT="1g"
      PROFILE_JOB_SCHEDULER_CPU_LIMIT="2.00"
      PROFILE_JOB_SCHEDULER_PIDS_LIMIT=384
      PROFILE_SITE_WORKER_ORCHESTRATOR_MEMORY_LIMIT="1g"
      PROFILE_SITE_WORKER_ORCHESTRATOR_CPU_LIMIT="2.00"
      PROFILE_SITE_WORKER_ORCHESTRATOR_PIDS_LIMIT=384
      PROFILE_SITE_WORKER_MEMORY_LIMIT="512m"
      PROFILE_SITE_WORKER_CPU_LIMIT="1.50"
      PROFILE_SITE_WORKER_PIDS_LIMIT=128
      PROFILE_SERVICE_ACTION_HELPER_MEMORY_LIMIT="2g"
      PROFILE_SERVICE_ACTION_HELPER_CPU_LIMIT="4.00"
      PROFILE_SERVICE_ACTION_HELPER_PIDS_LIMIT=384
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
      PROFILE_DOCKER_PROXY_MEMORY_LIMIT="128m"
      PROFILE_DOCKER_PROXY_CPU_LIMIT="0.50"
      PROFILE_DOCKER_PROXY_PIDS_LIMIT=96
      PROFILE_API_BACKEND_MEMORY_LIMIT="1g"
      PROFILE_API_BACKEND_CPU_LIMIT="2.00"
      PROFILE_API_BACKEND_PIDS_LIMIT=256
      PROFILE_JOB_SCHEDULER_MEMORY_LIMIT="512m"
      PROFILE_JOB_SCHEDULER_CPU_LIMIT="1.50"
      PROFILE_JOB_SCHEDULER_PIDS_LIMIT=256
      PROFILE_SITE_WORKER_ORCHESTRATOR_MEMORY_LIMIT="512m"
      PROFILE_SITE_WORKER_ORCHESTRATOR_CPU_LIMIT="1.50"
      PROFILE_SITE_WORKER_ORCHESTRATOR_PIDS_LIMIT=256
      PROFILE_SITE_WORKER_MEMORY_LIMIT="384m"
      PROFILE_SITE_WORKER_CPU_LIMIT="1.00"
      PROFILE_SITE_WORKER_PIDS_LIMIT=128
      PROFILE_SERVICE_ACTION_HELPER_MEMORY_LIMIT="1g"
      PROFILE_SERVICE_ACTION_HELPER_CPU_LIMIT="2.00"
      PROFILE_SERVICE_ACTION_HELPER_PIDS_LIMIT=256
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
      PROFILE_DOCKER_PROXY_MEMORY_LIMIT="128m"
      PROFILE_DOCKER_PROXY_CPU_LIMIT="0.50"
      PROFILE_DOCKER_PROXY_PIDS_LIMIT=64
      PROFILE_API_BACKEND_MEMORY_LIMIT="512m"
      PROFILE_API_BACKEND_CPU_LIMIT="1.50"
      PROFILE_API_BACKEND_PIDS_LIMIT=160
      PROFILE_JOB_SCHEDULER_MEMORY_LIMIT="256m"
      PROFILE_JOB_SCHEDULER_CPU_LIMIT="1.00"
      PROFILE_JOB_SCHEDULER_PIDS_LIMIT=160
      PROFILE_SITE_WORKER_ORCHESTRATOR_MEMORY_LIMIT="256m"
      PROFILE_SITE_WORKER_ORCHESTRATOR_CPU_LIMIT="1.00"
      PROFILE_SITE_WORKER_ORCHESTRATOR_PIDS_LIMIT=160
      PROFILE_SITE_WORKER_MEMORY_LIMIT="256m"
      PROFILE_SITE_WORKER_CPU_LIMIT="1.00"
      PROFILE_SITE_WORKER_PIDS_LIMIT=128
      PROFILE_SERVICE_ACTION_HELPER_MEMORY_LIMIT="512m"
      PROFILE_SERVICE_ACTION_HELPER_CPU_LIMIT="1.00"
      PROFILE_SERVICE_ACTION_HELPER_PIDS_LIMIT=160
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

  local db_name
  local db_user
  db_name="$(read_env_value POSTGRES_DB)"
  db_user="$(read_env_value POSTGRES_USER)"
  db_name="${db_name:-borealis}"
  db_user="${db_user:-borealis}"

  local public_base_url="https://${public_host}"
  if [[ "${public_host}" == *":443" ]]; then
    public_base_url="https://${public_host%:443}"
  fi
  local traefik_trusted_proxy_ips
  local traefik_forwarded_headers_trusted_ips
  local traefik_proxy_protocol_trusted_ips
  local runtime_owner_uid
  local runtime_owner_gid
  local docker_socket_gid
  local host_timezone
  local webui_memory_limit
  local webui_cpu_limit
  local engine_ip_fallback=""
  local local_ca_enabled=0
  local local_ca_cert=""
  local local_ca_key=""
  local local_tls_cert=""
  local local_tls_key=""
  local local_ca_b64=""
  if [[ "${engine_profile}" == "internal-only" ]]; then
    engine_ip_fallback="$(resolve_engine_ip_fallback "${engine_profile}")"
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
  docker_socket_gid="$(resolve_docker_socket_gid)"
  load_profile_tuning "$(detect_host_vcpu)" "$(detect_host_memory_mib)"
  webui_memory_limit="${PROFILE_WEBUI_FRONTEND_MEMORY_LIMIT}"
  webui_cpu_limit="${PROFILE_WEBUI_FRONTEND_CPU_LIMIT}"
  if [[ "${mode}" == "dev" ]]; then
    webui_memory_limit="${PROFILE_WEBUI_FRONTEND_DEV_MEMORY_LIMIT}"
    webui_cpu_limit="${PROFILE_WEBUI_FRONTEND_DEV_CPU_LIMIT}"
  fi
  if [[ "${BOREALIS_SUPPRESS_DEPLOYMENT_PROFILE_LOG:-0}" != "1" ]]; then
    log_status "Profile" "${PROFILE_NAME} (${PROFILE_HOST_VCPU} vCPU, ${PROFILE_HOST_MEMORY_GIB} GiB RAM, ${PROFILE_SITE_WORKER_CONCURRENCY} site-worker tasks)" "${C_BLUE}"
  fi

  cat > "${RUNTIME_ENV}" <<EOF
BOREALIS_PROJECT_ROOT=${SCRIPT_DIR}
BOREALIS_COMPOSE_PROJECT_NAME=${PROJECT_NAME}
BOREALIS_RUNTIME_ENV_FILE=${RUNTIME_ENV}
BOREALIS_WEBUI_ENV_FILE=${WEBUI_ENV}
BOREALIS_ENGINE_RUNTIME_USER=${ENGINE_RUNTIME_USER}
BOREALIS_ENGINE_RUNTIME_GROUP=${ENGINE_RUNTIME_GROUP}
BOREALIS_ENGINE_RUNTIME_OWNER_UID=${runtime_owner_uid}
BOREALIS_ENGINE_RUNTIME_OWNER_GID=${runtime_owner_gid}
BOREALIS_DOCKER_SOCKET_PATH=${BOREALIS_DOCKER_SOCKET_PATH:-/var/run/docker.sock}
BOREALIS_DOCKER_SOCKET_GID=${docker_socket_gid}
BOREALIS_ENGINE_MODE=production
BOREALIS_WEBUI_MODE=prod
BOREALIS_ENGINE_HOST_TIMEZONE=${host_timezone}
TZ=${host_timezone}
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

BOREALIS_DOCKER_PROXY_MEMORY_LIMIT=${BOREALIS_DOCKER_PROXY_MEMORY_LIMIT:-${PROFILE_DOCKER_PROXY_MEMORY_LIMIT}}
BOREALIS_DOCKER_PROXY_CPU_LIMIT=${BOREALIS_DOCKER_PROXY_CPU_LIMIT:-${PROFILE_DOCKER_PROXY_CPU_LIMIT}}
BOREALIS_DOCKER_PROXY_PIDS_LIMIT=${BOREALIS_DOCKER_PROXY_PIDS_LIMIT:-${PROFILE_DOCKER_PROXY_PIDS_LIMIT}}
BOREALIS_API_BACKEND_MEMORY_LIMIT=${BOREALIS_API_BACKEND_MEMORY_LIMIT:-${PROFILE_API_BACKEND_MEMORY_LIMIT}}
BOREALIS_API_BACKEND_CPU_LIMIT=${BOREALIS_API_BACKEND_CPU_LIMIT:-${PROFILE_API_BACKEND_CPU_LIMIT}}
BOREALIS_API_BACKEND_PIDS_LIMIT=${BOREALIS_API_BACKEND_PIDS_LIMIT:-${PROFILE_API_BACKEND_PIDS_LIMIT}}
BOREALIS_JOB_SCHEDULER_MEMORY_LIMIT=${BOREALIS_JOB_SCHEDULER_MEMORY_LIMIT:-${PROFILE_JOB_SCHEDULER_MEMORY_LIMIT}}
BOREALIS_JOB_SCHEDULER_CPU_LIMIT=${BOREALIS_JOB_SCHEDULER_CPU_LIMIT:-${PROFILE_JOB_SCHEDULER_CPU_LIMIT}}
BOREALIS_JOB_SCHEDULER_PIDS_LIMIT=${BOREALIS_JOB_SCHEDULER_PIDS_LIMIT:-${PROFILE_JOB_SCHEDULER_PIDS_LIMIT}}
BOREALIS_SITE_WORKER_ORCHESTRATOR_MEMORY_LIMIT=${BOREALIS_SITE_WORKER_ORCHESTRATOR_MEMORY_LIMIT:-${PROFILE_SITE_WORKER_ORCHESTRATOR_MEMORY_LIMIT}}
BOREALIS_SITE_WORKER_ORCHESTRATOR_CPU_LIMIT=${BOREALIS_SITE_WORKER_ORCHESTRATOR_CPU_LIMIT:-${PROFILE_SITE_WORKER_ORCHESTRATOR_CPU_LIMIT}}
BOREALIS_SITE_WORKER_ORCHESTRATOR_PIDS_LIMIT=${BOREALIS_SITE_WORKER_ORCHESTRATOR_PIDS_LIMIT:-${PROFILE_SITE_WORKER_ORCHESTRATOR_PIDS_LIMIT}}
BOREALIS_SITE_WORKER_MEMORY_LIMIT=${BOREALIS_SITE_WORKER_MEMORY_LIMIT:-${PROFILE_SITE_WORKER_MEMORY_LIMIT}}
BOREALIS_SITE_WORKER_CPU_LIMIT=${BOREALIS_SITE_WORKER_CPU_LIMIT:-${PROFILE_SITE_WORKER_CPU_LIMIT}}
BOREALIS_SITE_WORKER_PIDS_LIMIT=${BOREALIS_SITE_WORKER_PIDS_LIMIT:-${PROFILE_SITE_WORKER_PIDS_LIMIT}}
BOREALIS_SERVICE_ACTION_HELPER_MEMORY_LIMIT=${BOREALIS_SERVICE_ACTION_HELPER_MEMORY_LIMIT:-${PROFILE_SERVICE_ACTION_HELPER_MEMORY_LIMIT}}
BOREALIS_SERVICE_ACTION_HELPER_CPU_LIMIT=${BOREALIS_SERVICE_ACTION_HELPER_CPU_LIMIT:-${PROFILE_SERVICE_ACTION_HELPER_CPU_LIMIT}}
BOREALIS_SERVICE_ACTION_HELPER_PIDS_LIMIT=${BOREALIS_SERVICE_ACTION_HELPER_PIDS_LIMIT:-${PROFILE_SERVICE_ACTION_HELPER_PIDS_LIMIT}}
BOREALIS_WEBUI_FRONTEND_MEMORY_LIMIT=${BOREALIS_WEBUI_FRONTEND_MEMORY_LIMIT:-${webui_memory_limit}}
BOREALIS_WEBUI_FRONTEND_CPU_LIMIT=${BOREALIS_WEBUI_FRONTEND_CPU_LIMIT:-${webui_cpu_limit}}
BOREALIS_WEBUI_FRONTEND_PIDS_LIMIT=${BOREALIS_WEBUI_FRONTEND_PIDS_LIMIT:-${PROFILE_WEBUI_FRONTEND_PIDS_LIMIT}}
BOREALIS_TRAEFIK_EDGE_MEMORY_LIMIT=${BOREALIS_TRAEFIK_EDGE_MEMORY_LIMIT:-${PROFILE_TRAEFIK_EDGE_MEMORY_LIMIT}}
BOREALIS_TRAEFIK_EDGE_CPU_LIMIT=${BOREALIS_TRAEFIK_EDGE_CPU_LIMIT:-${PROFILE_TRAEFIK_EDGE_CPU_LIMIT}}
BOREALIS_TRAEFIK_EDGE_PIDS_LIMIT=${BOREALIS_TRAEFIK_EDGE_PIDS_LIMIT:-${PROFILE_TRAEFIK_EDGE_PIDS_LIMIT}}
BOREALIS_POSTGRES_DB_MEMORY_LIMIT=${BOREALIS_POSTGRES_DB_MEMORY_LIMIT:-${PROFILE_POSTGRES_DB_MEMORY_LIMIT}}
BOREALIS_POSTGRES_DB_CPU_LIMIT=${BOREALIS_POSTGRES_DB_CPU_LIMIT:-${PROFILE_POSTGRES_DB_CPU_LIMIT}}
BOREALIS_POSTGRES_DB_PIDS_LIMIT=${BOREALIS_POSTGRES_DB_PIDS_LIMIT:-${PROFILE_POSTGRES_DB_PIDS_LIMIT}}
BOREALIS_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT=${BOREALIS_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT:-${PROFILE_REMOTE_DESKTOP_GUACD_MEMORY_LIMIT}}
BOREALIS_REMOTE_DESKTOP_GUACD_CPU_LIMIT=${BOREALIS_REMOTE_DESKTOP_GUACD_CPU_LIMIT:-${PROFILE_REMOTE_DESKTOP_GUACD_CPU_LIMIT}}
BOREALIS_REMOTE_DESKTOP_GUACD_PIDS_LIMIT=${BOREALIS_REMOTE_DESKTOP_GUACD_PIDS_LIMIT:-${PROFILE_REMOTE_DESKTOP_GUACD_PIDS_LIMIT}}
BOREALIS_WIREGUARD_TUNNEL_MEMORY_LIMIT=${BOREALIS_WIREGUARD_TUNNEL_MEMORY_LIMIT:-${PROFILE_WIREGUARD_TUNNEL_MEMORY_LIMIT}}
BOREALIS_WIREGUARD_TUNNEL_CPU_LIMIT=${BOREALIS_WIREGUARD_TUNNEL_CPU_LIMIT:-${PROFILE_WIREGUARD_TUNNEL_CPU_LIMIT}}
BOREALIS_WIREGUARD_TUNNEL_PIDS_LIMIT=${BOREALIS_WIREGUARD_TUNNEL_PIDS_LIMIT:-${PROFILE_WIREGUARD_TUNNEL_PIDS_LIMIT}}

POSTGRES_DB=${db_name}
POSTGRES_USER=${db_user}
POSTGRES_PASSWORD=${postgres_password}
BOREALIS_DATABASE_URL=postgresql://${db_user}:${postgres_password}@127.0.0.1:5432/${db_name}
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
BOREALIS_INTERNAL_API_BASE_URL=http://127.0.0.1:5000
BOREALIS_COOKIE_SECURE=1
BOREALIS_GUACAMOLE_ENABLED=1
BOREALIS_GUACD_HOST=127.0.0.1
BOREALIS_GUACD_PORT=4822
BOREALIS_GUACAMOLE_VNC_WS_PATH=/remote-desktop/vnc/guacamole
BOREALIS_VNC_WS_HOST=127.0.0.1
BOREALIS_VNC_WS_PORT=4823
BOREALIS_WIREGUARD_PORT=30000
BOREALIS_WIREGUARD_ENGINE_VIRTUAL_IP=10.255.0.1/32
BOREALIS_WIREGUARD_PEER_NETWORK=10.255.0.0/16
BOREALIS_WIREGUARD_PORT_ALLOWLIST=47002,5900,22
BOREALIS_WIREGUARD_CONFIG_ROOT=${RUNTIME_ROOT}/Services/wireguard-tunnel/config
BOREALIS_WIREGUARD_KEY_ROOT=${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets
BOREALIS_WIREGUARD_CONTROL_SOCKET=${RUNTIME_ROOT}/Services/wireguard-tunnel/run/control.sock
BOREALIS_DOCKER_PROXY_URL=http://127.0.0.1:2375
BOREALIS_SITE_WORKER_ORCHESTRATOR_SOCKET=${RUNTIME_ROOT}/Services/site-worker-orchestrator/run/orchestrator.sock
BOREALIS_ENGINE_SECRET_PATH=${RUNTIME_ROOT}/Services/api-backend/secrets/engine_secret.txt
BOREALIS_ENGINE_CERT_ROOT=${RUNTIME_ROOT}/Services/api-backend/secrets/Certificates
BOREALIS_ENGINE_AUTH_TOKEN_ROOT=${RUNTIME_ROOT}/Services/api-backend/secrets/Auth_Tokens
BOREALIS_ANSIBLE_RUNTIME_ROOT=${RUNTIME_ROOT}/Services/api-backend/cache/Ansible
BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH=${RUNTIME_ROOT}/Services/api-backend/config/ansible_runner_settings.json
BOREALIS_SITE_WORKER_SETTINGS_PATH=${RUNTIME_ROOT}/Services/api-backend/config/site_worker_settings.json
BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY=${PROFILE_SITE_WORKER_CONCURRENCY}
BOREALIS_OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT=${RUNTIME_ROOT}/Services/api-backend/cache
BOREALIS_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/engine.log
BOREALIS_ERROR_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/error.log
BOREALIS_API_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/api.log
BOREALIS_VPN_TUNNEL_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/VPN_Tunnel/tunnel.log
BOREALIS_WIREGUARD_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/VPN_Tunnel/tunnel.log
EOF

  write_webui_mode_env_file "${WEBUI_ENV}" "${mode}"

  cp "${RUNTIME_ENV}" "${COMPOSE_ENV}"
  cat >> "${COMPOSE_ENV}" <<EOF
BOREALIS_API_BACKEND_IMAGE=${IMAGE_TAGS[api-backend]:-borealis-engine/api-backend:local}
BOREALIS_JOB_SCHEDULER_IMAGE=${IMAGE_TAGS[job-scheduler]:-borealis-engine/job-scheduler:local}
BOREALIS_SITE_WORKER_IMAGE=${IMAGE_TAGS[site-worker]:-borealis-engine/site-worker:local}
BOREALIS_WEBUI_FRONTEND_IMAGE=${IMAGE_TAGS[webui-frontend]:-borealis-engine/webui-frontend:local}
BOREALIS_TRAEFIK_EDGE_IMAGE=${IMAGE_TAGS[traefik-edge]:-borealis-engine/traefik-edge:local}
BOREALIS_POSTGRES_DB_IMAGE=${IMAGE_TAGS[postgres-db]:-borealis-engine/postgres-db:local}
BOREALIS_REMOTE_DESKTOP_GUACD_IMAGE=${IMAGE_TAGS[remote-desktop-guacd]:-borealis-engine/remote-desktop-guacd:local}
BOREALIS_WIREGUARD_TUNNEL_IMAGE=${IMAGE_TAGS[wireguard-tunnel]:-borealis-engine/wireguard-tunnel:local}
BOREALIS_DOCKER_PROXY_IMAGE=${BOREALIS_DOCKER_PROXY_IMAGE:-ghcr.io/tecnativa/docker-socket-proxy:v0.4.2}
EOF
  chmod 600 "${COMPOSE_ENV}" "${RUNTIME_ENV}" "${WEBUI_ENV}"
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
    api-backend|job-scheduler)
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
        DOCKER_BUILDKIT=1 docker build "${build_args[@]}" "${SCRIPT_DIR}/${context}"
      fi
    else
      DOCKER_BUILDKIT=1 docker build "${build_args[@]}" "${SCRIPT_DIR}/${context}"
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
  : > "${BUILD_LOG}"
  GO_API_BACKEND_BINARY_PREPARED=0
  for service in "${selected[@]}"; do
    validate_build_role "${service}"
  done
  CURRENT_BUILD_SELECTION=("${selected[@]}")
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

prune_stale_site_worker_images() {
  local active_tag="${IMAGE_TAGS[site-worker]:-}"
  [[ -n "${active_tag}" ]] || return 0
  local stale_images=()
  mapfile -t stale_images < <(
    docker image ls \
      --filter "label=io.borealis.service=site-worker" \
      --format '{{.Repository}}:{{.Tag}}' \
      | awk -v active="${active_tag}" 'NF && $0 != active && $0 != "<none>:<none>" {print}'
  )
  ((${#stale_images[@]} > 0)) || return 0
  log_status "Docker Cleanup" "Pruning Stale Site Worker Images" "${C_YELLOW}"
  local image=""
  for image in "${stale_images[@]}"; do
    if ! docker image rm "${image}" >> "${BUILD_LOG}" 2>&1; then
      printf '[%s] Failed to remove stale site-worker image %s\n' "$(date +%FT%T)" "${image}" >> "${BUILD_LOG}"
    fi
  done
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
  if ! docker image prune -a --force --filter "label!=io.borealis.service=site-worker" >> "${BUILD_LOG}" 2>&1; then
    cleanup_failed=1
    log_status "Docker Cleanup" "Image Prune Failed" "${C_RED}"
  fi

  prune_stale_site_worker_images
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
  if [[ "${BUILD_STATUSES[job-scheduler]:-}" == "built" ]]; then
    printf '%s\n' site-worker-orchestrator
  fi
  if [[ "${BUILD_STATUSES[site-worker]:-}" == "built" ]]; then
    printf '%s\n' job-scheduler
    printf '%s\n' site-worker-orchestrator
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

all_engine_containers_running() {
  local service=""
  for service in "${SERVICE_ROLES[@]}"; do
    container_running "borealis-engine-${service}" || return 1
  done
}

deploy_state_matches() {
  local mode="$1"
  python3 - "${DEPLOY_MANIFEST}" "${IMAGE_MANIFEST}" "${COMPOSE_FILE}" "${COMPOSE_ENV}" "${mode}" "${PROJECT_NAME}" "${SERVICE_ROLES[@]}" <<'PY'
import hashlib
import json
import pathlib
import sys

deploy_path = pathlib.Path(sys.argv[1])
image_path = pathlib.Path(sys.argv[2])
compose_path = pathlib.Path(sys.argv[3])
env_path = pathlib.Path(sys.argv[4])
mode = sys.argv[5]
project = sys.argv[6]
services = sys.argv[7:]

def file_hash(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()

def env_settings_hash(path: pathlib.Path, legacy_scheduler_worker_images: bool = False) -> str:
    lines = []
    image_keys = {
        "BOREALIS_API_BACKEND_IMAGE",
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
        if key in image_keys and not (
            legacy_scheduler_worker_images
            and key in {"BOREALIS_JOB_SCHEDULER_IMAGE", "BOREALIS_SITE_WORKER_IMAGE"}
        ):
            continue
        lines.append(raw)
    return hashlib.sha256(("\n".join(lines) + "\n").encode("utf-8")).hexdigest()

def image_records() -> dict[str, dict[str, str]]:
    data = json.loads(image_path.read_text(encoding="utf-8"))
    records = {}
    for service in services:
        manifest_service = "job-scheduler" if service == "site-worker-orchestrator" else service
        record = (data.get("services") or {}).get(manifest_service) or {}
        records[service] = {
            "image": record.get("image") or "",
            "hash": record.get("hash") or "",
        }
    return records

if not deploy_path.is_file() or not image_path.is_file() or not compose_path.is_file() or not env_path.is_file():
    raise SystemExit(1)
try:
    existing = json.loads(deploy_path.read_text(encoding="utf-8"))
except Exception:
    raise SystemExit(1)

expected = {
    "schema_version": 2,
    "project": project,
    "mode": mode,
    "compose_file_hash": file_hash(compose_path),
    "env_file_hash": file_hash(env_path),
    "env_settings_hash": env_settings_hash(env_path),
    "service_images": image_records(),
}
for key, value in expected.items():
    if key == "env_settings_hash":
        acceptable = {value, env_settings_hash(env_path, legacy_scheduler_worker_images=True)}
        if existing.get(key) not in acceptable:
            raise SystemExit(1)
        continue
    if existing.get(key) != value:
        raise SystemExit(1)
PY
}

deploy_non_image_state_matches() {
  local mode="$1"
  python3 - "${DEPLOY_MANIFEST}" "${COMPOSE_FILE}" "${COMPOSE_ENV}" "${mode}" "${PROJECT_NAME}" <<'PY'
import hashlib
import json
import pathlib
import sys

deploy_path = pathlib.Path(sys.argv[1])
compose_path = pathlib.Path(sys.argv[2])
env_path = pathlib.Path(sys.argv[3])
project = sys.argv[5]

def file_hash(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()

def env_settings_hash(path: pathlib.Path, legacy_scheduler_worker_images: bool = False) -> str:
    lines = []
    image_keys = {
        "BOREALIS_API_BACKEND_IMAGE",
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
        if key in image_keys and not (
            legacy_scheduler_worker_images
            and key in {"BOREALIS_JOB_SCHEDULER_IMAGE", "BOREALIS_SITE_WORKER_IMAGE"}
        ):
            continue
        lines.append(raw)
    return hashlib.sha256(("\n".join(lines) + "\n").encode("utf-8")).hexdigest()

if not deploy_path.is_file() or not compose_path.is_file() or not env_path.is_file():
    raise SystemExit(1)
try:
    existing = json.loads(deploy_path.read_text(encoding="utf-8"))
except Exception:
    raise SystemExit(1)
if existing.get("schema_version") != 2:
    raise SystemExit(1)
expected = {
    "project": project,
    "compose_file_hash": file_hash(compose_path),
    "env_settings_hash": env_settings_hash(env_path),
}
for key, value in expected.items():
    if key == "env_settings_hash":
        acceptable = {value, env_settings_hash(env_path, legacy_scheduler_worker_images=True)}
        if existing.get(key) not in acceptable:
            raise SystemExit(1)
        continue
    if existing.get(key) != value:
        raise SystemExit(1)
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

services = [
    "docker-proxy",
    "api-backend",
    "site-worker-orchestrator",
    "job-scheduler",
    "webui-frontend",
    "traefik-edge",
    "postgres-db",
    "remote-desktop-guacd",
    "wireguard-tunnel",
]
allowed_services = set(services)
service_images = {service: {"image": "", "hash": ""} for service in services}
if image_path.is_file():
    try:
        image_data = json.loads(image_path.read_text(encoding="utf-8"))
    except Exception:
        image_data = {}
    for service, record in sorted((image_data.get("services") or {}).items()):
        if service not in allowed_services:
            continue
        service_images[service] = {
            "image": record.get("image") or "",
            "hash": record.get("hash") or "",
        }
    if "site-worker-orchestrator" in allowed_services:
        scheduler_record = (image_data.get("services") or {}).get("job-scheduler") or {}
        service_images["site-worker-orchestrator"] = {
            "image": scheduler_record.get("image") or "",
            "hash": scheduler_record.get("hash") or "",
        }

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
  for candidate in "${SERVICE_ROLES[@]}"; do
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
  seed_webui_runtime_source
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
  local network_mode
  network_mode="$(resolve_engine_network_mode)"
  log_deploy_header "${mode}" "${network_mode}"
  ensure_engine_dependencies
  ensure_no_host_postgres_conflict
  prepare_runtime "${mode}"
  build_images "${mode}"
  export_image_manifest_env
  write_image_manifest "${mode}"
  BOREALIS_SUPPRESS_DEPLOYMENT_PROFILE_LOG=1 write_compose_env "${mode}" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME)" "$(read_env_value BOREALIS_ACME_EMAIL)" "$(read_env_value BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS)" "$(read_env_value BOREALIS_ENGINE_DEPLOYMENT_PROFILE)" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME_ALIASES)"
  ensure_engine_database_schema
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
  if deploy_state_matches "${mode}" && all_engine_containers_running; then
    log_status "Docker Compose" "Up-to-Date" "${C_GREEN}"
    refresh_compose_service_statuses
    write_deploy_manifest "${mode}" "skipped"
    log_section "Docker Housekeeping"
    prune_engine_docker_storage "${mode}"
    log_section "Engine Deployment Complete"
    log_webui_url
    return 0
  fi
  if ((${#target_services[@]} > 0)) && deploy_non_image_state_matches "${mode}" && all_engine_containers_running; then
    log_status "Docker Compose" "Reconciling ${target_services[*]}" "${C_YELLOW}"
    if ! compose_base up -d --no-deps --no-build "${target_services[@]}" >> "${BUILD_LOG}" 2>&1; then
      log_status "Docker Compose" "Failed" "${C_RED}"
      die "Docker Compose scoped reconciliation failed. See ${BUILD_LOG}."
    fi
    wait_for_compose_services_to_settle 90 "${target_services[@]}" || true
    log_status "Docker Compose" "Reconciled ${target_services[*]}" "${C_GREEN}"
    write_deploy_manifest "${mode}" "up-scoped" "${target_services[@]}"
    log_section "Docker Housekeeping"
    prune_engine_docker_storage "${mode}"
    log_section "Engine Deployment Complete"
    log_webui_url
    return 0
  fi
  log_status "Docker Compose" "Reconciling Stack" "${C_YELLOW}"
  if ! compose_base up -d --no-build >> "${BUILD_LOG}" 2>&1; then
    log_status "Docker Compose" "Failed" "${C_RED}"
    die "Docker Compose stack reconciliation failed. See ${BUILD_LOG}."
  fi
  wait_for_compose_services_to_settle 90 "${SERVICE_ROLES[@]}" || true
  log_status "Docker Compose" "Stack Reconciled" "${C_GREEN}"
  write_deploy_manifest "${mode}" "up" "${changed_services[@]}"
  log_section "Docker Housekeeping"
  prune_engine_docker_storage "${mode}"
  log_section "Engine Deployment Complete"
  log_webui_url
}

service_compose_name() {
  local service="$1"
  validate_service "${service}"
  printf '%s\n' "${service}"
}

service_action() {
  local service="${1:-}"
  local action="${2:-}"
  local mode="${3:-prod}"
  [[ -n "${service}" && -n "${action}" ]] || die "Usage: Engine.sh --service <service> <restart|rebuild|reload|reconcile> [dev|prod]"
  validate_service "${service}"
  mode="$(normalize_mode "${mode}")"
  ensure_engine_dependencies
  ensure_no_host_postgres_conflict
  prepare_runtime "${mode}"
  case "${action}" in
    restart)
      compose_base restart "$(service_compose_name "${service}")"
      ;;
    rebuild)
      local build_service="${service}"
      if [[ "${service}" == "site-worker-orchestrator" ]]; then
        build_service="job-scheduler"
      fi
      build_images "${mode}" "${build_service}"
      export_image_manifest_env
      write_image_manifest "${mode}"
      BOREALIS_SUPPRESS_DEPLOYMENT_PROFILE_LOG=1 write_compose_env "${mode}" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME)" "$(read_env_value BOREALIS_ACME_EMAIL)" "$(read_env_value BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS)" "$(read_env_value BOREALIS_ENGINE_DEPLOYMENT_PROFILE)" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME_ALIASES)"
      log_status "${service}" "Recreating Container" "${C_YELLOW}"
      compose_base up -d --no-deps --no-build "$(service_compose_name "${service}")"
      write_deploy_manifest "${mode}" "up-scoped" "${service}"
      prune_engine_docker_storage "${mode}"
      log_webui_url
      ;;
    reload)
      [[ "${service}" == "traefik-edge" ]] || die "reload supported for traefik-edge only."
      compose_base restart traefik-edge
      ;;
    reconcile)
      [[ "${service}" == "wireguard-tunnel" ]] || die "reconcile supported for wireguard-tunnel only."
      compose_base exec -T wireguard-tunnel borealis-wireguard-control-client reconcile || true
      ;;
    *)
      die "Unsupported service action '${action}'."
      ;;
  esac
}

usage() {
  cat <<'EOF'
Usage:
  Engine.sh --network-mode <public|local> deploy [prod|dev]
  Engine.sh --network-mode <public|local> --service <docker-proxy|api-backend|site-worker-orchestrator|job-scheduler|webui-frontend|traefik-edge|postgres-db|remote-desktop-guacd|wireguard-tunnel> <restart|rebuild|reload|reconcile> [prod|dev]
  Engine.sh --network-mode <public|local> [--install-dir PATH] [--repo-url URL] [--release-channel stable|unstable] [--repo-branch REF] deploy [prod|dev]
EOF
}

main() {
  parse_launch_options "$@"
  local pending_command="${LAUNCH_ARGS[0]:-deploy}"
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

main "$@"
exit $?
