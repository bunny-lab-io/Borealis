#!/usr/bin/env bash
# Borealis Linux bootstrapper:
# - Installs minimal bootstrap prerequisites (downloader + unzip + certs)
# - Downloads Borealis ZIP from GitHub
# - Extracts to /opt/Borealis (or --install-dir)
# - Executes Borealis.sh, forwarding all Borealis arguments

set -o errexit
set -o nounset
set -o pipefail

GREEN="\033[0;32m"
YELLOW="\033[1;33m"
RED="\033[0;31m"
RESET="\033[0m"
INFO="[i]"

DEFAULT_INSTALL_DIR="/opt/Borealis"
DEFAULT_ZIP_URL="https://github.com/bunny-lab-io/Borealis/archive/refs/heads/main.zip"
DEFAULT_ZIP_PATH="/tmp/BorealisBootstrap.zip"

INSTALL_DIR="${BOREALIS_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"
ZIP_URL="${BOREALIS_BOOTSTRAP_ZIP_URL:-${DEFAULT_ZIP_URL}}"
ZIP_PATH="${BOREALIS_BOOTSTRAP_ZIP_PATH:-${DEFAULT_ZIP_PATH}}"

FORWARD_ARGS=()
EXTRACT_DIR=""

usage() {
  cat <<'EOF'
Usage: bootstrap.sh [bootstrap options] [Borealis.sh options]

Bootstrap options:
  --install-dir <path>   Install location (default: /opt/Borealis)
  --zip-url <url>        ZIP source URL
  --zip-path <path>      ZIP destination path (default: /tmp/BorealisBootstrap.zip)
  -h, --help             Show this help

Any other arguments are forwarded to Borealis.sh, for example:
  bootstrap.sh --agent --serverurl https://10.0.0.54:5000 --enrollmentcode XXXX-XXXX
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
      --install-dir|--zip-url|--zip-path)
        if [[ $# -lt 2 ]]; then
          echo -e "${RED}Missing value for ${1}.${RESET}" >&2
          exit 1
        fi
        case "$1" in
          --install-dir) INSTALL_DIR="$2" ;;
          --zip-url) ZIP_URL="$2" ;;
          --zip-path) ZIP_PATH="$2" ;;
        esac
        shift
        ;;
      --install-dir=*|--zip-url=*|--zip-path=*)
        key="${1%%=*}"
        value="${1#*=}"
        case "${key}" in
          --install-dir) INSTALL_DIR="${value}" ;;
          --zip-url) ZIP_URL="${value}" ;;
          --zip-path) ZIP_PATH="${value}" ;;
        esac
        ;;
      -h|--help)
        usage
        exit 0
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
  if [[ -z "${ZIP_PATH}" ]]; then
    echo -e "${RED}ZIP path cannot be empty.${RESET}" >&2
    return 1
  fi
}

ensure_bootstrap_dependencies() {
  if command_exists unzip && (command_exists curl || command_exists wget); then
    return 0
  fi

  detect_distro
  case "${DISTRO_ID}" in
    ubuntu|debian|linuxmint|pop)
      run_privileged apt update -qq
      run_privileged apt install -y curl unzip ca-certificates
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        run_privileged dnf install -y curl unzip ca-certificates
      else
        run_privileged yum install -y curl unzip ca-certificates
      fi
      ;;
    arch)
      run_privileged pacman -Sy --noconfirm curl unzip ca-certificates
      ;;
    *)
      ;;
  esac

  if ! command_exists unzip || { ! command_exists curl && ! command_exists wget; }; then
    echo -e "${RED}Bootstrap prerequisites missing. Install unzip and either curl or wget.${RESET}" >&2
    return 1
  fi
}

download_repo_zip() {
  mkdir -p "$(dirname "${ZIP_PATH}")"
  rm -f "${ZIP_PATH}"

  echo -e "${INFO} Downloading Borealis ZIP from ${ZIP_URL}"
  if command_exists curl; then
    curl -fL "${ZIP_URL}" -o "${ZIP_PATH}"
  elif command_exists wget; then
    wget -O "${ZIP_PATH}" "${ZIP_URL}"
  else
    echo -e "${RED}No supported downloader found (curl/wget).${RESET}" >&2
    return 1
  fi
}

extract_and_install() {
  EXTRACT_DIR="$(mktemp -d /tmp/BorealisBootstrap.XXXXXX)"
  unzip -q "${ZIP_PATH}" -d "${EXTRACT_DIR}"

  local extracted_root
  extracted_root="$(find "${EXTRACT_DIR}" -maxdepth 1 -mindepth 1 -type d -name 'Borealis-*' | head -n 1)"
  if [[ -z "${extracted_root}" || ! -d "${extracted_root}" ]]; then
    echo -e "${RED}Could not locate extracted Borealis directory.${RESET}" >&2
    return 1
  fi

  echo -e "${INFO} Installing Borealis into ${INSTALL_DIR}"
  run_privileged rm -rf "${INSTALL_DIR}"
  run_privileged mkdir -p "${INSTALL_DIR}"
  run_privileged cp -a "${extracted_root}/." "${INSTALL_DIR}/"
  run_privileged chmod +x "${INSTALL_DIR}/Borealis.sh" || true
  run_privileged chmod +x "${INSTALL_DIR}/bootstrap.sh" || true
  restore_selinux_context_if_needed "${INSTALL_DIR}"
}

cleanup() {
  if [[ -n "${EXTRACT_DIR}" && -d "${EXTRACT_DIR}" ]]; then
    rm -rf "${EXTRACT_DIR}" 2>/dev/null || true
  fi
}

main() {
  trap cleanup EXIT
  parse_args "$@"
  validate_paths
  ensure_bootstrap_dependencies
  download_repo_zip
  extract_and_install

  echo -e "${GREEN}Launching ${INSTALL_DIR}/Borealis.sh${RESET}"
  if [[ ! -t 0 && -r /dev/tty ]]; then
    # When bootstrap is piped to bash, stdin is consumed by the script stream.
    # Reattach Borealis.sh stdin to the controlling terminal for interactive menus.
    exec "${INSTALL_DIR}/Borealis.sh" "${FORWARD_ARGS[@]}" < /dev/tty
  fi
  exec "${INSTALL_DIR}/Borealis.sh" "${FORWARD_ARGS[@]}"
}

main "$@"
