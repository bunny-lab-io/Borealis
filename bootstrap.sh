#!/usr/bin/env bash
# Borealis Linux bootstrapper:
# - Ensures Git is installed
# - Uses Git to fetch/checkout Borealis into /opt/Borealis (or --install-dir)
# - Executes Borealis.sh, forwarding all Borealis arguments

set -o errexit
set -o nounset
set -o pipefail

GREEN="\033[0;32m"
RED="\033[0;31m"
RESET="\033[0m"
INFO="[i]"

DEFAULT_INSTALL_DIR="/opt/Borealis"
DEFAULT_REPO_URL="https://github.com/bunny-lab-io/Borealis.git"
DEFAULT_REPO_REF="main"

INSTALL_DIR="${BOREALIS_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"
REPO_URL="${BOREALIS_BOOTSTRAP_REPO_URL:-${DEFAULT_REPO_URL}}"
REPO_REF="${BOREALIS_BOOTSTRAP_REF:-${DEFAULT_REPO_REF}}"

FORWARD_ARGS=()
FORWARD_AGENT=0
FORWARDED_NEW_ENGINE=0

usage() {
  cat <<'EOF'
Usage: bootstrap.sh [bootstrap options] [Borealis.sh options]

Bootstrap options:
  --install-dir <path>   Install location (default: /opt/Borealis)
  --repo-url <url>       Git repository URL (default: https://github.com/bunny-lab-io/Borealis.git)
  --ref <name>           Git ref/branch/tag/commit to deploy (default: main)
  -h, --help             Show this help

Any other arguments are forwarded to Borealis.sh, for example:
  bootstrap.sh --agent --serverurl https://10.0.0.54:5000 --enrollmentcode XXXX-XXXX

Agent bootstrap always forwards --newEngine so rerunning bootstrap clears
persisted Engine trust and enrollment tokens before Borealis.sh starts.

bootstrap.sh is the supported Linux first-run path for installing missing
system packages. Direct Borealis.sh redeploys assume core OS dependencies
already exist and focus on staging / verification instead of apt/yum/dnf
checks on every run.
EOF
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
  echo -e "${RED}This action requires root privileges and sudo is not available.${RESET}" >&2
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
  if ! selinux_enforcing; then
    return 0
  fi
  if ! command_exists restorecon; then
    return 0
  fi
  run_privileged restorecon -RF "${target}" >/dev/null 2>&1 || true
}

parse_args() {
  local key=""
  local value=""
  while (( "$#" )); do
    case "$1" in
      --install-dir|--repo-url|--ref|--branch)
        if [[ $# -lt 2 ]]; then
          echo -e "${RED}Missing value for ${1}.${RESET}" >&2
          exit 1
        fi
        case "$1" in
          --install-dir) INSTALL_DIR="$2" ;;
          --repo-url) REPO_URL="$2" ;;
          --ref|--branch) REPO_REF="$2" ;;
        esac
        shift
        ;;
      --install-dir=*|--repo-url=*|--ref=*|--branch=*)
        key="${1%%=*}"
        value="${1#*=}"
        case "${key}" in
          --install-dir) INSTALL_DIR="${value}" ;;
          --repo-url) REPO_URL="${value}" ;;
          --ref|--branch) REPO_REF="${value}" ;;
        esac
        ;;
      --zip-url|--zip-path|--zip-url=*|--zip-path=*)
        echo -e "${RED}ZIP-based bootstrap is no longer supported. Use --repo-url and --ref.${RESET}" >&2
        exit 1
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      -Agent|--agent|--Agent)
        FORWARD_AGENT=1
        FORWARD_ARGS+=("$1")
        ;;
      -NewEngine|--newEngine|--newengine|-DeleteServerTrust|--delete-servertrust|--deleteservertrust|-ForceReEnroll|--force-reenroll|--forcereenroll)
        FORWARDED_NEW_ENGINE=1
        FORWARD_ARGS+=("$1")
        ;;
      *)
        FORWARD_ARGS+=("$1")
        ;;
    esac
    shift
  done
}

validate_paths() {
  if [[ -z "${INSTALL_DIR}" || "${INSTALL_DIR}" == "/" ]]; then
    echo -e "${RED}Refusing to install into an empty path or '/'.${RESET}" >&2
    return 1
  fi
  if [[ -z "${REPO_URL}" ]]; then
    echo -e "${RED}Repository URL cannot be empty.${RESET}" >&2
    return 1
  fi
  if [[ -z "${REPO_REF}" ]]; then
    echo -e "${RED}Repository ref cannot be empty.${RESET}" >&2
    return 1
  fi
}

ensure_bootstrap_dependencies() {
  if command_exists git; then
    return 0
  fi

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

  if ! command_exists git; then
    echo -e "${RED}Git is required but was not found. Install git and rerun bootstrap.${RESET}" >&2
    return 1
  fi
}

sync_repo() {
  echo -e "${INFO} Syncing Borealis ref '${REPO_REF}' from ${REPO_URL}"
  run_privileged mkdir -p "${INSTALL_DIR}"

  if [[ ! -d "${INSTALL_DIR}/.git" ]]; then
    run_privileged find "${INSTALL_DIR}" -mindepth 1 -maxdepth 1 \
      ! -name "Engine" \
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

  run_privileged git -C "${INSTALL_DIR}" fetch --depth 1 --force origin "${REPO_REF}"
  run_privileged git -C "${INSTALL_DIR}" checkout --force -B main FETCH_HEAD
  run_privileged git -C "${INSTALL_DIR}" reset --hard FETCH_HEAD
  run_privileged git -C "${INSTALL_DIR}" clean -fdx -e Engine -e Agent

  run_privileged chmod +x "${INSTALL_DIR}/Borealis.sh" || true
  run_privileged chmod +x "${INSTALL_DIR}/bootstrap.sh" || true
  restore_selinux_context_if_needed "${INSTALL_DIR}"
}

main() {
  parse_args "$@"
  validate_paths
  ensure_bootstrap_dependencies
  sync_repo

  if [[ "${FORWARD_AGENT}" -eq 1 && "${FORWARDED_NEW_ENGINE}" -eq 0 ]]; then
    echo -e "${GREEN}Agent bootstrap always runs with --newEngine to clear persisted Engine trust and enrollment state.${RESET}"
    FORWARD_ARGS+=("--newEngine")
  fi

  export BOREALIS_BOOTSTRAP_NEW_ENGINE=1
  export BOREALIS_ALLOW_SYSTEM_PACKAGE_INSTALL=1
  echo -e "${GREEN}Launching ${INSTALL_DIR}/Borealis.sh${RESET}"
  if [[ ! -t 0 && -r /dev/tty ]]; then
    # When bootstrap is piped to bash, stdin is consumed by the script stream.
    # Reattach Borealis.sh stdin to the controlling terminal for interactive menus.
    exec "${INSTALL_DIR}/Borealis.sh" "${FORWARD_ARGS[@]}" < /dev/tty
  fi
  exec "${INSTALL_DIR}/Borealis.sh" "${FORWARD_ARGS[@]}"
}

main "$@"
