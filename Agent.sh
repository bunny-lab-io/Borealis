#!/usr/bin/env bash
# Borealis Agent local deploy controller.

set -o errexit
set -o nounset
set -o pipefail

if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then
  SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
else
  SCRIPT_DIR="$(pwd)"
fi
cd "${SCRIPT_DIR}"

DEFAULT_AGENT_RUNTIME_ROOT="/opt/Borealis/Agent"
DEFAULT_INSTALL_DIR="/opt/Borealis"
DEFAULT_REPO_URL="https://github.com/bunny-lab-io/Borealis.git"
DEFAULT_REPO_REF="main"
DEFAULT_RELEASE_CHANNEL="${BOREALIS_AGENT_RELEASE_CHANNEL:-unstable}"
DEFAULT_STABLE_REF="${BOREALIS_AGENT_STABLE_REF:-}"
DEFAULT_UNSTABLE_REF="${BOREALIS_AGENT_UNSTABLE_REF:-${DEFAULT_REPO_REF}}"
INSTALL_DIR="${BOREALIS_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"
REPO_URL="${BOREALIS_AGENT_REPO_URL:-${DEFAULT_REPO_URL}}"
REPO_REF="${BOREALIS_AGENT_REF:-}"
REPO_CHECKOUT_BRANCH="${BOREALIS_AGENT_CHECKOUT_BRANCH:-}"
REPO_REF_EXPLICIT=0
RELEASE_CHANNEL="${DEFAULT_RELEASE_CHANNEL}"
SYNC_REQUESTED=0
DISTRO_ID="unknown"
LAUNCH_ARGS=()
ORIGINAL_ARGS=()
SERVER_URL=""
ENROLLMENT_CODE=""
NEW_ENGINE_FLAG=0
REFRESH_AGENT_RUNTIME_FLAG=0
if [[ -n "${REPO_REF}" ]]; then
  REPO_REF_EXPLICIT=1
fi

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

privilege_available() {
  [[ "${EUID:-$(id -u)}" -eq 0 ]] && return 0
  command_exists sudo
}

ensure_privilege_available() {
  privilege_available && return 0
  die "Root privileges are required. Run Agent.sh as root, or install sudo and rerun as a sudo-enabled user."
}

exec_with_optional_tty() {
  if [[ ! -t 0 && -r /dev/tty ]]; then
    exec "$@" < /dev/tty
  fi
  exec "$@"
}

exec_agent_script() {
  local script_path="$1"
  shift || true
  if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
    exec_with_optional_tty bash "${script_path}" "$@"
  fi
  command_exists sudo || die "Root privileges are required. Run Agent.sh as root, or install sudo and rerun as a sudo-enabled user."
  exec_with_optional_tty sudo -E bash "${script_path}" "$@"
}

ensure_root_execution() {
  [[ "${EUID:-$(id -u)}" -eq 0 ]] && return 0
  command_exists sudo || die "Root privileges are required. Run Agent.sh as root, or install sudo and rerun as a sudo-enabled user."
  local script_path="${BASH_SOURCE[0]:-}"
  [[ -n "${script_path}" && -f "${script_path}" ]] || die "Cannot self-escalate from a non-file Agent.sh invocation. Run the installer as root."
  exec_with_optional_tty sudo -E bash "${script_path}" "${ORIGINAL_ARGS[@]}"
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

running_in_agent_updater_service() {
  [[ "${BOREALIS_AGENT_UPDATER_SERVICE:-0}" == "1" ]] && return 0
  if [[ -r /proc/self/cgroup ]] && grep -q "borealis-agent-updater\\.service" /proc/self/cgroup 2>/dev/null; then
    return 0
  fi
  return 1
}

engine_present_in_install_root() {
  [[ -d "${SCRIPT_DIR}/Engine/Deploy" ]] && return 0
  [[ -f "${SCRIPT_DIR}/Engine/Deploy/deploy-manifest.json" ]] && return 0
  [[ -f "${SCRIPT_DIR}/Engine/Deploy/compose.env" ]] && return 0
  [[ -d "${SCRIPT_DIR}/Engine/Services/api-backend" ]] && return 0
  return 1
}

ensure_not_engine_host() {
  engine_present_in_install_root || return 0
  die "Refusing to install the Linux Agent in an Engine install root. Use a separate host for the Agent; Agent auto-update mutates the shared checkout and can break Engine source/runtime expectations."
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
  command_exists git || die "Git is required. Install git and rerun Agent.sh."
}

sync_repo() {
  [[ -n "${INSTALL_DIR}" && "${INSTALL_DIR}" != "/" ]] || die "Refusing to install into empty path or '/'."
  [[ -n "${REPO_URL}" ]] || die "Repository URL cannot be empty."
  ensure_privilege_available
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
  run_privileged chmod +x "${INSTALL_DIR}/Engine.sh" "${INSTALL_DIR}/Agent.sh" "${INSTALL_DIR}/Update.sh" >/dev/null 2>&1 || true
  restore_selinux_context_if_needed "${INSTALL_DIR}"
}

source_available() {
  [[ -d "${SCRIPT_DIR}/Data/Agent" && -f "${SCRIPT_DIR}/Data/Agent/agent.py" ]]
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
      -Agent|--agent|--Agent)
        if [[ "${#LAUNCH_ARGS[@]}" -eq 0 ]]; then
          LAUNCH_ARGS=(deploy)
        fi
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
  log "Launching ${INSTALL_DIR}/Agent.sh ${LAUNCH_ARGS[*]:-deploy}."
  export BOREALIS_BOOTSTRAP_NEW_ENGINE="${BOREALIS_BOOTSTRAP_NEW_ENGINE:-1}"
  exec_agent_script "${INSTALL_DIR}/Agent.sh" "${LAUNCH_ARGS[@]}"
}

resolve_python_bin() {
  if command_exists python3; then
    printf '%s\n' "python3"
    return 0
  fi
  if command_exists python; then
    printf '%s\n' "python"
    return 0
  fi
  return 1
}

agent_runtime_dir() {
  printf '%s\n' "${BOREALIS_AGENT_VENV:-${DEFAULT_AGENT_RUNTIME_ROOT}}"
}

agent_python_bin() {
  local runtime_dir
  runtime_dir="$(agent_runtime_dir)"
  if [[ -x "${runtime_dir}/bin/python3" ]]; then
    printf '%s\n' "${runtime_dir}/bin/python3"
  elif [[ -x "${runtime_dir}/bin/python" ]]; then
    printf '%s\n' "${runtime_dir}/bin/python"
  fi
}

agent_script_path() {
  printf '%s\n' "$(agent_runtime_dir)/Borealis/agent.py"
}

ensure_agent_log_dir() {
  mkdir -p "${SCRIPT_DIR}/Agent/Logs"
  printf '%s\n' "${SCRIPT_DIR}/Agent/Logs"
}

write_agent_log() {
  local log_dir
  log_dir="$(ensure_agent_log_dir)"
  printf '[%s] %s\n' "$(date +%FT%T)" "$*" >> "${log_dir}/install.log"
}

mask_enrollment_code() {
  local code="${1:-}"
  if [[ "${#code}" -le 8 ]]; then
    printf '%s\n' "***"
    return 0
  fi
  printf '%s***%s\n' "${code:0:4}" "${code: -4}"
}

install_agent_dependencies() {
  if command_exists python3 && python3 -m venv --help >/dev/null 2>&1 && { command_exists pip3 || python3 -m pip --version >/dev/null 2>&1; }; then
    return 0
  fi
  detect_distro
  case "${DISTRO_ID}" in
    ubuntu|debian|linuxmint|pop)
      run_privileged apt update -qq
      run_privileged apt install -y python3 python3-venv python3-pip ca-certificates
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        run_privileged dnf install -y python3 python3-pip ca-certificates
      else
        run_privileged yum install -y python3 python3-pip ca-certificates
      fi
      ;;
    arch)
      run_privileged pacman -Sy --noconfirm python python-pip python-virtualenv ca-certificates
      ;;
    *)
      die "Unsupported distro '${DISTRO_ID}'. Install python3, python3-venv, python3-pip manually."
      ;;
  esac

  command_exists python3 || die "python3 missing after package installation."
  python3 -m venv --help >/dev/null 2>&1 || die "python3 venv support missing after package installation."
}

capture_existing_server_url() {
  local path="${SCRIPT_DIR}/Agent/Borealis/Settings/server_url.txt"
  local legacy="${SCRIPT_DIR}/Agent/Settings/server_url.txt"
  if [[ -f "${path}" ]]; then
    head -n 1 "${path}" 2>/dev/null || true
  elif [[ -f "${legacy}" ]]; then
    head -n 1 "${legacy}" 2>/dev/null || true
  fi
}

clear_agent_enrollment_state() {
  local settings_dir=""
  local file=""
  for settings_dir in "${SCRIPT_DIR}/Agent/Borealis/Settings" "${SCRIPT_DIR}/Agent/Settings"; do
    for file in Agent_GUID.txt access.jwt access.meta.json refresh.token server_signing_key.pub installer_code.shared.json; do
      rm -f "${settings_dir}/${file}" 2>/dev/null || true
    done
  done
  write_agent_log "Cleared Agent enrollment state for new Engine trust."
}

stage_agent_runtime() {
  local runtime_dir
  local source_root="${SCRIPT_DIR}/Data/Agent"
  runtime_dir="$(agent_runtime_dir)"
  [[ -d "${source_root}" ]] || die "Agent source missing at ${source_root}."
  local python_bin
  python_bin="$(resolve_python_bin)" || die "Python interpreter missing."

  mkdir -p "${runtime_dir}"
  if [[ ! -x "${runtime_dir}/bin/python" && ! -x "${runtime_dir}/bin/python3" ]]; then
    "${python_bin}" -m venv "${runtime_dir}"
  fi

  local destination="${runtime_dir}/Borealis"
  mkdir -p "${destination}"
  local item=""
  local core_items=(
    Python_API_Endpoints
    Roles
    Scripts
    agent_deployment.py
    agent.py
    Borealis.ico
    desktop_environment.py
    fcntl_stub.py
    launch_service.ps1
    qt_compat.py
    role_health.py
    role_manager.py
    runtime_paths.py
    security.py
    session_runtime.py
    signature_utils.py
    sitecustomize.py
    termios_stub.py
    tray_state.py
    update_helper.py
    update_state.py
  )
  for item in "${core_items[@]}"; do
    rm -rf "${destination:?}/${item}" 2>/dev/null || true
    if [[ -e "${source_root}/${item}" ]]; then
      cp -a "${source_root}/${item}" "${destination}/"
    fi
  done
}

install_agent_python_deps() {
  local python_bin
  python_bin="$(agent_python_bin)"
  [[ -n "${python_bin}" ]] || die "Agent runtime Python missing."
  if [[ -f "${SCRIPT_DIR}/Data/Agent/agent-requirements.txt" ]]; then
    "${python_bin}" -m pip install --disable-pip-version-check -q -r "${SCRIPT_DIR}/Data/Agent/agent-requirements.txt"
  fi
}

configure_agent_settings() {
  local preserved_url="${1:-}"
  local settings_dir="${SCRIPT_DIR}/Agent/Borealis/Settings"
  local legacy_settings_dir="${SCRIPT_DIR}/Agent/Settings"
  local server_url_path="${settings_dir}/server_url.txt"
  local config_path="${settings_dir}/agent_settings.json"
  local system_config_path="${settings_dir}/agent_settings_SYSTEM.json"
  mkdir -p "${settings_dir}"
  if [[ ! -f "${server_url_path}" && -f "${legacy_settings_dir}/server_url.txt" ]]; then
    cp -f "${legacy_settings_dir}/server_url.txt" "${server_url_path}" 2>/dev/null || true
  fi

  local current_url="${SERVER_URL:-${BOREALIS_SERVER_URL:-${preserved_url}}}"
  if [[ -z "${current_url}" && -f "${server_url_path}" ]]; then
    current_url="$(head -n 1 "${server_url_path}" 2>/dev/null || true)"
  fi
  if [[ -z "${current_url}" && -t 0 ]]; then
    read -r -p "Server URL: " current_url || true
  fi
  [[ -n "${current_url}" ]] && printf '%s' "${current_url}" > "${server_url_path}"

  local provided_code="${ENROLLMENT_CODE:-${BOREALIS_ENROLLMENT_CODE:-}}"
  if [[ -z "${provided_code}" && -t 0 ]]; then
    read -r -p "Enrollment Code [blank to preserve/clear]: " provided_code || true
  fi

  local python_bin
  python_bin="$(resolve_python_bin)" || die "Python interpreter missing."
CONFIG_PATHS="${config_path}:${system_config_path}" ENROLLMENT_CODE_VALUE="${provided_code}" "${python_bin}" - <<'PY'
import json
import os
paths = [path for path in os.environ["CONFIG_PATHS"].split(":") if path]
code = os.environ.get("ENROLLMENT_CODE_VALUE", "")
for path in paths:
    data = {
        "config_file_watcher_interval": 2,
        "agent_id": "",
        "regions": {},
        "enrollment_code": "",
        "installer_code": "",
    }
    if os.path.exists(path):
        try:
            with open(path, "r", encoding="utf-8") as handle:
                existing = json.load(handle)
            if isinstance(existing, dict):
                data.update(existing)
        except Exception:
            pass
    if code:
        data["enrollment_code"] = code
        data["installer_code"] = code
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(data, handle)
PY
  if [[ -n "${BOREALIS_ONBOARDING_JOB_ID:-}" || -n "${BOREALIS_ONBOARDING_RUN_ID:-}" || -n "${BOREALIS_ONBOARDING_TARGET:-}" ]]; then
    local onboarding_path="${SCRIPT_DIR}/Agent/Borealis/Settings/onboarding_context.json"
    run_privileged mkdir -p "$(dirname "${onboarding_path}")"
    BOREALIS_ONBOARDING_CONTEXT_PATH="${onboarding_path}" "${python_bin}" - <<'PY'
import json
import os

def clean_int(value):
    try:
        parsed = int(str(value or "").strip())
        return parsed if parsed > 0 else None
    except Exception:
        return None

payload = {
    "job_id": clean_int(os.environ.get("BOREALIS_ONBOARDING_JOB_ID")),
    "run_id": clean_int(os.environ.get("BOREALIS_ONBOARDING_RUN_ID")),
    "target": str(os.environ.get("BOREALIS_ONBOARDING_TARGET") or "").strip()[:253],
}
payload = {key: value for key, value in payload.items() if value not in (None, "")}
path = os.environ.get("BOREALIS_ONBOARDING_CONTEXT_PATH") or ""
if payload and path:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(payload, handle)
PY
  fi
}

stop_agent_supervision() {
  local unit_name="${BOREALIS_AGENT_SYSTEMD_UNIT:-borealis-agent.service}"
  local in_updater_service=0
  if running_in_agent_updater_service; then
    in_updater_service=1
  fi
  if command_exists systemctl; then
    run_privileged systemctl stop "${unit_name}" >/dev/null 2>&1 || true
    if [[ "${in_updater_service}" -eq 0 ]]; then
      run_privileged systemctl stop borealis-agent-updater.service >/dev/null 2>&1 || true
      run_privileged systemctl stop borealis-agent-updater.timer >/dev/null 2>&1 || true
    fi
  fi

  local pid_file="${SCRIPT_DIR}/Agent/Logs/agent.pid"
  if [[ -f "${pid_file}" ]]; then
    local pid=""
    pid="$(head -n 1 "${pid_file}" 2>/dev/null || true)"
    if [[ "${pid}" =~ ^[0-9]+$ ]] && kill -0 "${pid}" >/dev/null 2>&1; then
      local cmdline=""
      if [[ -r "/proc/${pid}/cmdline" ]]; then
        cmdline="$(tr '\0' ' ' < "/proc/${pid}/cmdline" 2>/dev/null || true)"
      fi
      if [[ "${cmdline}" == *"Borealis/agent.py"* || "${cmdline}" == *"Data/Agent/agent.py"* ]]; then
        run_privileged kill "${pid}" >/dev/null 2>&1 || true
        sleep 1
        kill -0 "${pid}" >/dev/null 2>&1 && run_privileged kill -9 "${pid}" >/dev/null 2>&1 || true
      fi
    fi
    rm -f "${pid_file}" 2>/dev/null || true
  fi

  command_exists pgrep || return 0
  local runtime_dir
  runtime_dir="$(agent_runtime_dir)"
  local current_pid="${BASHPID:-$$}"
  while IFS= read -r pid; do
    [[ "${pid}" =~ ^[0-9]+$ ]] || continue
    [[ "${pid}" != "${current_pid}" && "${pid}" != "$$" ]] || continue
    kill -0 "${pid}" >/dev/null 2>&1 || continue
    local cmdline=""
    if [[ -r "/proc/${pid}/cmdline" ]]; then
      cmdline="$(tr '\0' ' ' < "/proc/${pid}/cmdline" 2>/dev/null || true)"
    fi
    if [[ "${cmdline}" == *"${SCRIPT_DIR}/Agent/Borealis/agent.py"* \
       || "${cmdline}" == *"${runtime_dir}/Borealis/agent.py"* \
       || "${cmdline}" == *"${SCRIPT_DIR}/Data/Agent/agent.py"* ]]; then
      write_agent_log "Stopping orphaned Agent process pid=${pid}."
      run_privileged kill "${pid}" >/dev/null 2>&1 || true
    fi
  done < <(pgrep -f 'agent.py' 2>/dev/null || true)
  sleep 1
  while IFS= read -r pid; do
    [[ "${pid}" =~ ^[0-9]+$ ]] || continue
    [[ "${pid}" != "${current_pid}" && "${pid}" != "$$" ]] || continue
    kill -0 "${pid}" >/dev/null 2>&1 || continue
    local cmdline=""
    if [[ -r "/proc/${pid}/cmdline" ]]; then
      cmdline="$(tr '\0' ' ' < "/proc/${pid}/cmdline" 2>/dev/null || true)"
    fi
    if [[ "${cmdline}" == *"${SCRIPT_DIR}/Agent/Borealis/agent.py"* \
       || "${cmdline}" == *"${runtime_dir}/Borealis/agent.py"* \
       || "${cmdline}" == *"${SCRIPT_DIR}/Data/Agent/agent.py"* ]]; then
      write_agent_log "Force-stopping orphaned Agent process pid=${pid}."
      run_privileged kill -9 "${pid}" >/dev/null 2>&1 || true
    fi
  done < <(pgrep -f 'agent.py' 2>/dev/null || true)
}

ensure_agent_systemd_service() {
  command_exists systemctl || return 1
  local python_bin
  local agent_script
  local runtime_dir
  python_bin="$(agent_python_bin)"
  agent_script="$(agent_script_path)"
  runtime_dir="$(agent_runtime_dir)"
  [[ -n "${python_bin}" && -f "${agent_script}" ]] || return 1
  local unit_name="${BOREALIS_AGENT_SYSTEMD_UNIT:-borealis-agent.service}"
  local unit_file="${SCRIPT_DIR}/Agent/Logs/${unit_name}"
  cat > "${unit_file}" <<EOF
[Unit]
Description=Borealis Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${SCRIPT_DIR}
Environment=BOREALIS_PROJECT_ROOT=${SCRIPT_DIR}
Environment=BOREALIS_AGENT_MODE=system
Environment=BOREALIS_AGENT_RUNTIME=${runtime_dir}
ExecStart=${python_bin} ${agent_script} --system-service --config SYSTEM
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
  run_privileged cp "${unit_file}" "/etc/systemd/system/${unit_name}"
  run_privileged systemctl daemon-reload
  run_privileged systemctl enable "${unit_name}"
  run_privileged systemctl restart "${unit_name}"
}

ensure_agent_updater_timer() {
  command_exists systemctl || return 0
  local service_file="${SCRIPT_DIR}/Agent/Logs/borealis-agent-updater.service"
  local timer_file="${SCRIPT_DIR}/Agent/Logs/borealis-agent-updater.timer"
  cat > "${service_file}" <<EOF
[Unit]
Description=Borealis Agent Auto Updater
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
WorkingDirectory=${SCRIPT_DIR}
Environment=BOREALIS_PROJECT_ROOT=${SCRIPT_DIR}
Environment=BOREALIS_AGENT_UPDATER_SERVICE=1
ExecStart=/usr/bin/env bash ${SCRIPT_DIR}/Update.sh -Agent
EOF
  cat > "${timer_file}" <<'EOF'
[Unit]
Description=Borealis Agent Auto Updater Timer

[Timer]
OnCalendar=hourly
RandomizedDelaySec=15min
Persistent=true
AccuracySec=1s
Unit=borealis-agent-updater.service

[Install]
WantedBy=timers.target
EOF
  run_privileged cp "${service_file}" /etc/systemd/system/borealis-agent-updater.service
  run_privileged cp "${timer_file}" /etc/systemd/system/borealis-agent-updater.timer
  run_privileged systemctl daemon-reload
  run_privileged systemctl enable borealis-agent-updater.timer
  run_privileged systemctl restart borealis-agent-updater.timer
}

start_agent_background() {
  local python_bin
  local agent_script
  local log_dir
  python_bin="$(agent_python_bin)"
  agent_script="$(agent_script_path)"
  log_dir="$(ensure_agent_log_dir)"
  [[ -n "${python_bin}" && -f "${agent_script}" ]] || die "Agent runtime missing."
  (
    cd "${SCRIPT_DIR}"
    nohup "${python_bin}" "${agent_script}" --system-service --config SYSTEM >>"${log_dir}/agent-launch.stdout.log" 2>>"${log_dir}/agent-launch.stderr.log" &
    echo $! > "${log_dir}/agent.pid"
  )
}

configure_supervision() {
  if ensure_agent_systemd_service; then
    ensure_agent_updater_timer || true
    return 0
  fi
  start_agent_background
}

deploy_agent() {
  ensure_root_execution
  ensure_not_engine_host
  stop_agent_supervision
  install_agent_dependencies
  local preserved_url
  preserved_url="$(capture_existing_server_url)"
  local explicit_enrollment_code="${ENROLLMENT_CODE:-${BOREALIS_ENROLLMENT_CODE:-}}"
  if [[ -n "${explicit_enrollment_code}" ]]; then
    NEW_ENGINE_FLAG=1
    write_agent_log "Explicit enrollment code supplied; refreshing Agent enrollment state code_mask=$(mask_enrollment_code "${explicit_enrollment_code}")."
  fi
  if [[ "${NEW_ENGINE_FLAG}" -eq 1 || "${BOREALIS_BOOTSTRAP_NEW_ENGINE:-0}" == "1" ]]; then
    clear_agent_enrollment_state
  fi
  stage_agent_runtime
  install_agent_python_deps
  configure_agent_settings "${preserved_url}"
  configure_supervision
  log "Agent deployed from local source."
}

usage() {
  cat <<'EOF'
Usage:
  Agent.sh deploy [--serverurl URL] [--enrollmentcode CODE] [--newEngine]
  Agent.sh [--install-dir PATH] [--repo-url URL] [--release-channel stable|unstable] [--repo-branch REF] deploy [--serverurl URL] [--enrollmentcode CODE] [--newEngine]
  # Root shell:
  curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Agent.sh | bash -s -- deploy [--serverurl URL] [--enrollmentcode CODE]
  # Sudo-enabled non-root shell:
  curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Agent.sh | sudo bash -s -- deploy [--serverurl URL] [--enrollmentcode CODE]
EOF
}

parse_and_run() {
  parse_launch_options "$@"
  sync_and_reexec_if_needed
  set -- "${LAUNCH_ARGS[@]}"
  if [[ $# -gt 0 && "${1}" == -* && "${1}" != "-h" && "${1}" != "--help" ]]; then
    set -- deploy "$@"
  fi
  local command="${1:-deploy}"
  shift || true
  while (($#)); do
    case "$1" in
      -ServerUrl|--ServerUrl|--serverurl|--server-url)
        shift
        SERVER_URL="${1:-}"
        ;;
      -ServerUrl=*|--ServerUrl=*|--serverurl=*|--server-url=*)
        SERVER_URL="${1#*=}"
        ;;
      -EnrollmentCode|--EnrollmentCode|--enrollmentcode|--enrollment-code)
        shift
        ENROLLMENT_CODE="${1:-}"
        ;;
      -EnrollmentCode=*|--EnrollmentCode=*|--enrollmentcode=*|--enrollment-code=*)
        ENROLLMENT_CODE="${1#*=}"
        ;;
      -NewEngine|--newEngine|--newengine|-DeleteServerTrust|--delete-servertrust|--deleteservertrust|-ForceReEnroll|--force-reenroll|--forcereenroll)
        NEW_ENGINE_FLAG=1
        ;;
      --refresh-agent-runtime)
        REFRESH_AGENT_RUNTIME_FLAG=1
        ;;
      -h|--help)
        usage
        return 0
        ;;
      *)
        ;;
    esac
    shift || true
  done

  case "${command}" in
    deploy|"")
      deploy_agent
      ;;
    -h|--help|help)
      usage
      ;;
    *)
      usage >&2
      return 2
      ;;
  esac
}

ORIGINAL_ARGS=("$@")
parse_and_run "$@"
