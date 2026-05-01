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
DEPLOY_DIR="${RUNTIME_ROOT}/Deploy"
COMPOSE_ENV="${DEPLOY_DIR}/compose.env"
IMAGE_MANIFEST="${DEPLOY_DIR}/image-manifest.json"
DEPLOY_MANIFEST="${DEPLOY_DIR}/deploy-manifest.json"
BUILD_LOG="${DEPLOY_DIR}/build.log"
DEFAULT_INSTALL_DIR="/opt/Borealis"
DEFAULT_REPO_URL="https://github.com/bunny-lab-io/Borealis.git"
DEFAULT_REPO_REF="main"
DEFAULT_RELEASE_CHANNEL="${BOREALIS_ENGINE_RELEASE_CHANNEL:-unstable}"
DEFAULT_STABLE_REF="${BOREALIS_ENGINE_STABLE_REF:-}"
DEFAULT_UNSTABLE_REF="${BOREALIS_ENGINE_UNSTABLE_REF:-${DEFAULT_REPO_REF}}"
INSTALL_DIR="${BOREALIS_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"
REPO_URL="${BOREALIS_ENGINE_REPO_URL:-${DEFAULT_REPO_URL}}"
REPO_REF="${BOREALIS_ENGINE_REF:-}"
REPO_CHECKOUT_BRANCH="${BOREALIS_ENGINE_CHECKOUT_BRANCH:-}"
REPO_REF_EXPLICIT=0
RELEASE_CHANNEL="${DEFAULT_RELEASE_CHANNEL}"
SYNC_REQUESTED=0
DISTRO_ID="unknown"
LAUNCH_ARGS=()
if [[ -n "${REPO_REF}" ]]; then
  REPO_REF_EXPLICIT=1
fi
SERVICE_ROLES=(
  "api-backend"
  "webui-frontend"
  "traefik-edge"
  "postgres-db"
  "remote-desktop-guacd"
  "wireguard-tunnel"
)

declare -A IMAGE_TAGS
declare -A IMAGE_HASHES
declare -A DOCKERFILES
declare -A BUILD_CONTEXTS

log() {
  printf '[%s] %s\n' "$(date +%FT%T)" "$*"
}

die() {
  printf '[%s] ERROR: %s\n' "$(date +%FT%T)" "$*" >&2
  exit 1
}

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
  run_privileged chmod +x "${INSTALL_DIR}/Engine.sh" "${INSTALL_DIR}/Agent.sh" "${INSTALL_DIR}/Borealis.sh" "${INSTALL_DIR}/Update.sh" >/dev/null 2>&1 || true
  restore_selinux_context_if_needed "${INSTALL_DIR}"
}

source_available() {
  [[ -f "${COMPOSE_FILE}" && -f "${BUILD_MANIFEST}" && -d "${CONTAINER_SOURCE_DIR}" ]]
}

parse_launch_options() {
  LAUNCH_ARGS=()
  while (($#)); do
    case "$1" in
      --install-dir|--repo-url|--ref|--branch|--repo-branch|--repo_branch|--release-channel|--release_channel)
        [[ $# -ge 2 ]] || die "Missing value for ${1}."
        case "$1" in
          --install-dir) INSTALL_DIR="$2" ;;
          --repo-url) REPO_URL="$2" ;;
          --release-channel|--release_channel) RELEASE_CHANNEL="$2" ;;
          --ref|--branch|--repo-branch|--repo_branch)
            REPO_REF="$2"
            REPO_REF_EXPLICIT=1
            case "$1" in
              --branch|--repo-branch|--repo_branch) REPO_CHECKOUT_BRANCH="$2" ;;
            esac
            ;;
        esac
        SYNC_REQUESTED=1
        shift 2
        ;;
      --install-dir=*|--repo-url=*|--ref=*|--branch=*|--repo-branch=*|--repo_branch=*|--release-channel=*|--release_channel=*)
        local key="${1%%=*}"
        local value="${1#*=}"
        case "${key}" in
          --install-dir) INSTALL_DIR="${value}" ;;
          --repo-url) REPO_URL="${value}" ;;
          --release-channel|--release_channel) RELEASE_CHANNEL="${value}" ;;
          --ref|--branch|--repo-branch|--repo_branch)
            REPO_REF="${value}"
            REPO_REF_EXPLICIT=1
            case "${key}" in
              --branch|--repo-branch|--repo_branch) REPO_CHECKOUT_BRANCH="${value}" ;;
            esac
            ;;
        esac
        SYNC_REQUESTED=1
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
    -f "${COMPOSE_FILE}"
}

require_docker() {
  command_exists docker || die "Docker Engine CLI missing. Run Engine.sh deploy after installing Docker Engine."
  docker info >/dev/null 2>&1 || die "Docker daemon unreachable. Start Docker Engine and retry."
  docker compose version >/dev/null 2>&1 || die "Docker Compose plugin missing. Install docker compose plugin and retry."
}

require_python() {
  command_exists python3 || die "python3 missing. Install python3 and rerun Engine.sh."
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

engine_mode_env() {
  case "$(normalize_mode "${1:-prod}")" in
    dev) printf '%s\n' "developer" ;;
    *) printf '%s\n' "production" ;;
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

generate_secret() {
  if command_exists openssl; then
    openssl rand -hex 24
    return 0
  fi
  date +%s%N | sha256sum | awk '{print $1}'
}

ensure_service_tree() {
  local role=""
  mkdir -p "${DEPLOY_DIR}" "${RUNTIME_ROOT}/Shared/tmp" "${RUNTIME_ROOT}/Shared/downloads"
  for role in "${SERVICE_ROLES[@]}"; do
    mkdir -p \
      "${RUNTIME_ROOT}/Services/${role}/config" \
      "${RUNTIME_ROOT}/Services/${role}/env" \
      "${RUNTIME_ROOT}/Services/${role}/logs" \
      "${RUNTIME_ROOT}/Services/${role}/state" \
      "${RUNTIME_ROOT}/Services/${role}/secrets" \
      "${RUNTIME_ROOT}/Services/${role}/cache" \
      "${RUNTIME_ROOT}/Services/${role}/run"
  done
  mkdir -p \
    "${RUNTIME_ROOT}/Services/api-backend/logs/VPN_Tunnel" \
    "${RUNTIME_ROOT}/Services/api-backend/secrets/Auth_Tokens" \
    "${RUNTIME_ROOT}/Services/api-backend/secrets/Certificates" \
    "${RUNTIME_ROOT}/Services/api-backend/cache/Ansible/collections" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets" \
    "${RUNTIME_ROOT}/Services/wireguard-tunnel/run"
}

link_runtime_compat_path() {
  local link_path="$1"
  local target_path="$2"
  if [[ -L "${link_path}" ]]; then
    return 0
  fi
  if [[ -e "${link_path}" ]]; then
    return 0
  fi
  ln -s "${target_path}" "${link_path}"
}

ensure_legacy_compat_paths() {
  mkdir -p "${RUNTIME_ROOT}"
  link_runtime_compat_path "${RUNTIME_ROOT}/Logs" "Services/api-backend/logs"
  link_runtime_compat_path "${RUNTIME_ROOT}/Auth_Tokens" "Services/api-backend/secrets/Auth_Tokens"
  link_runtime_compat_path "${RUNTIME_ROOT}/Certificates" "Services/api-backend/secrets/Certificates"
  link_runtime_compat_path "${RUNTIME_ROOT}/Ansible" "Services/api-backend/cache/Ansible"
  link_runtime_compat_path "${RUNTIME_ROOT}/WireGuard" "Services/wireguard-tunnel/config"
  link_runtime_compat_path "${RUNTIME_ROOT}/LetsEncrypt" "Services/traefik-edge/state"
  link_runtime_compat_path "${RUNTIME_ROOT}/Traefik" "Services/traefik-edge/config"
}

resolve_public_hostname() {
  local existing
  existing="$(read_env_value BOREALIS_PUBLIC_HOSTNAME)"
  if [[ -n "${BOREALIS_PUBLIC_HOSTNAME:-}" ]]; then
    printf '%s\n' "${BOREALIS_PUBLIC_HOSTNAME}"
  elif [[ -n "${existing}" ]]; then
    printf '%s\n' "${existing}"
  elif [[ -t 0 ]]; then
    local input=""
    read -r -p "Public Engine FQDN [localhost]: " input || true
    printf '%s\n' "${input:-localhost}"
  else
    printf '%s\n' "localhost"
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

write_compose_env() {
  local mode="$1"
  local public_host="$2"
  local acme_email="$3"
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

  local engine_mode
  engine_mode="$(engine_mode_env "${mode}")"

  cat > "${COMPOSE_ENV}" <<EOF
BOREALIS_PROJECT_ROOT=${SCRIPT_DIR}
BOREALIS_COMPOSE_PROJECT_NAME=${PROJECT_NAME}
BOREALIS_RUNTIME_ENV_FILE=${COMPOSE_ENV}

BOREALIS_ENGINE_MODE=${engine_mode}
BOREALIS_WEBUI_MODE=${mode}
BOREALIS_PUBLIC_HOSTNAME=${public_host}
BOREALIS_PUBLIC_EDGE_ENABLED=1
BOREALIS_PUBLIC_BASE_URL=${public_base_url}
BOREALIS_PUBLIC_HTTPS_PORT=443
BOREALIS_PUBLIC_HTTP_PORT=80
BOREALIS_PUBLIC_VNC_PATH=/remote-desktop/vnc
BOREALIS_PUBLIC_WIREGUARD_HOST=${public_host}
BOREALIS_PUBLIC_WIREGUARD_PORT=30000
BOREALIS_ACME_EMAIL=${acme_email}
BOREALIS_LETSENCRYPT_SETTINGS_PATH=${RUNTIME_ROOT}/LetsEncrypt/Settings.json
BOREALIS_TRAEFIK_ACME_STORAGE_PATH=${RUNTIME_ROOT}/LetsEncrypt/acme.json

POSTGRES_DB=${db_name}
POSTGRES_USER=${db_user}
POSTGRES_PASSWORD=${postgres_password}
BOREALIS_DATABASE_URL=postgresql://${db_user}:${postgres_password}@127.0.0.1:5432/${db_name}
BOREALIS_DB_SSLMODE=disable
BOREALIS_DB_POOL_SIZE=${BOREALIS_DB_POOL_SIZE:-10}
BOREALIS_DB_MAX_OVERFLOW=${BOREALIS_DB_MAX_OVERFLOW:-10}
BOREALIS_DB_CONNECT_TIMEOUT=${BOREALIS_DB_CONNECT_TIMEOUT:-15}
BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS=${BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS:-60000}

BOREALIS_WEBUI_EXTERNAL=1
BOREALIS_ENGINE_CONTAINERIZED=1
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
BOREALIS_WIREGUARD_KEY_ROOT=${RUNTIME_ROOT}/Services/wireguard-tunnel/secrets
BOREALIS_WIREGUARD_CONTROL_SOCKET=${RUNTIME_ROOT}/Services/wireguard-tunnel/run/control.sock
BOREALIS_ENGINE_SECRET_PATH=${RUNTIME_ROOT}/Services/api-backend/secrets/engine_secret.txt
BOREALIS_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/engine.log
BOREALIS_ERROR_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/error.log
BOREALIS_API_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/api.log
BOREALIS_VPN_TUNNEL_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/VPN_Tunnel/tunnel.log
BOREALIS_WIREGUARD_LOG_FILE=${RUNTIME_ROOT}/Services/api-backend/logs/VPN_Tunnel/tunnel.log

BOREALIS_API_BACKEND_IMAGE=${IMAGE_TAGS[api-backend]:-borealis-engine/api-backend:local}
BOREALIS_WEBUI_FRONTEND_IMAGE=${IMAGE_TAGS[webui-frontend]:-borealis-engine/webui-frontend:local}
BOREALIS_TRAEFIK_EDGE_IMAGE=${IMAGE_TAGS[traefik-edge]:-borealis-engine/traefik-edge:local}
BOREALIS_POSTGRES_DB_IMAGE=${IMAGE_TAGS[postgres-db]:-borealis-engine/postgres-db:local}
BOREALIS_REMOTE_DESKTOP_GUACD_IMAGE=${IMAGE_TAGS[remote-desktop-guacd]:-borealis-engine/remote-desktop-guacd:local}
BOREALIS_WIREGUARD_TUNNEL_IMAGE=${IMAGE_TAGS[wireguard-tunnel]:-borealis-engine/wireguard-tunnel:local}
EOF
  chmod 600 "${COMPOSE_ENV}"
}

compute_service_hash() {
  local service="$1"
  local mode="$2"
  python3 - "${SCRIPT_DIR}" "${BUILD_MANIFEST}" "${service}" "${mode}" <<'PY'
import hashlib
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
manifest = json.loads(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"))
service = sys.argv[3]
mode = sys.argv[4]
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
for pattern in entry.get("inputs", []):
    for candidate in root.glob(pattern):
        if not candidate.is_file():
            continue
        rel = candidate.relative_to(root)
        if any(part in excluded_parts for part in rel.parts):
            continue
        if candidate.suffix in excluded_suffixes:
            continue
        files.add(rel)
dockerfile = pathlib.Path(entry["dockerfile"])
files.add(dockerfile)
digest = hashlib.sha256()
digest.update(f"service={service}\nmode={mode}\n".encode("utf-8"))
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

build_service_image() {
  local service="$1"
  local mode="$2"
  local image_hash
  image_hash="$(compute_service_hash "${service}" "${mode}")"
  local tag="borealis-engine/${service}:sha-${image_hash:0:12}"
  local dockerfile
  local context
  dockerfile="$(manifest_field "${service}" dockerfile)"
  context="$(manifest_field "${service}" context)"
  IMAGE_HASHES["${service}"]="${image_hash}"
  IMAGE_TAGS["${service}"]="${tag}"
  DOCKERFILES["${service}"]="${dockerfile}"
  BUILD_CONTEXTS["${service}"]="${context}"

  local previous
  previous="$(previous_image_hash "${service}")"
  if [[ "${previous}" == "${image_hash}" ]] && docker image inspect "${tag}" >/dev/null 2>&1; then
    log "${service} image unchanged (${tag}); build skipped."
    return 0
  fi

  log "Building ${service} image ${tag}."
  {
    printf '[%s] Building %s as %s\n' "$(date +%FT%T)" "${service}" "${tag}"
    docker build \
      --label "org.opencontainers.image.title=Borealis ${service}" \
      --label "io.borealis.service=${service}" \
      --label "io.borealis.input-sha=${image_hash}" \
      -t "${tag}" \
      -f "${SCRIPT_DIR}/${dockerfile}" \
      "${SCRIPT_DIR}/${context}"
  } >> "${BUILD_LOG}" 2>&1
}

build_images() {
  local mode="$1"
  shift || true
  local selected=("$@")
  if [[ "${#selected[@]}" -eq 0 ]]; then
    selected=("${SERVICE_ROLES[@]}")
  fi
  local service=""
  : > "${BUILD_LOG}"
  for service in "${selected[@]}"; do
    validate_service "${service}"
    build_service_image "${service}" "${mode}"
  done
}

write_image_manifest() {
  local mode="$1"
  python3 - "${IMAGE_MANIFEST}" "${mode}" "${SERVICE_ROLES[@]}" <<PY
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
  for service in "${SERVICE_ROLES[@]}"; do
    if [[ -n "${IMAGE_TAGS[${service}]:-}" ]]; then
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
  for service in "${SERVICE_ROLES[@]}"; do
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

write_deploy_manifest() {
  local mode="$1"
  python3 - "${DEPLOY_MANIFEST}" "${mode}" "${COMPOSE_FILE}" "${COMPOSE_ENV}" "${PROJECT_NAME}" <<'PY'
import json
import pathlib
import sys
from datetime import datetime, timezone

payload = {
    "schema_version": 1,
    "project": sys.argv[5],
    "mode": sys.argv[2],
    "compose_file": sys.argv[3],
    "env_file": sys.argv[4],
    "deployed_at": datetime.now(timezone.utc).isoformat(),
    "services": [
        "api-backend",
        "webui-frontend",
        "traefik-edge",
        "postgres-db",
        "remote-desktop-guacd",
        "wireguard-tunnel",
    ],
}
path = pathlib.Path(sys.argv[1])
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
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

prepare_runtime() {
  local mode="$1"
  ensure_service_tree
  ensure_legacy_compat_paths
  load_existing_image_tags
  local public_host
  local acme_email
  public_host="$(resolve_public_hostname)"
  acme_email="$(resolve_acme_email)"
  write_compose_env "${mode}" "${public_host}" "${acme_email}"
}

deploy_engine() {
  local mode
  mode="$(normalize_mode "${1:-prod}")"
  ensure_engine_dependencies
  prepare_runtime "${mode}"
  build_images "${mode}"
  export_image_manifest_env
  write_image_manifest "${mode}"
  write_compose_env "${mode}" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME)" "$(read_env_value BOREALIS_ACME_EMAIL)"
  log "Starting Compose project ${PROJECT_NAME} (${mode})."
  compose_base up -d
  write_deploy_manifest "${mode}"
  log "Engine deployed through Docker Compose. Env: ${COMPOSE_ENV}"
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
  prepare_runtime "${mode}"
  case "${action}" in
    restart)
      compose_base restart "$(service_compose_name "${service}")"
      ;;
    rebuild)
      build_images "${mode}" "${service}"
      export_image_manifest_env
      write_image_manifest "${mode}"
      write_compose_env "${mode}" "$(read_env_value BOREALIS_PUBLIC_HOSTNAME)" "$(read_env_value BOREALIS_ACME_EMAIL)"
      compose_base up -d --no-deps "$(service_compose_name "${service}")"
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
  Engine.sh deploy [prod|dev]
  Engine.sh --service <api-backend|webui-frontend|traefik-edge|postgres-db|remote-desktop-guacd|wireguard-tunnel> <restart|rebuild|reload|reconcile> [prod|dev]
  Engine.sh [--install-dir PATH] [--repo-url URL] [--release-channel stable|unstable] [--repo-branch REF] deploy [prod|dev]
EOF
}

main() {
  parse_launch_options "$@"
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
