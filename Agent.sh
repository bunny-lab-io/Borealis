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
SERVER_URL=""
ENROLLMENT_CODE=""
NEW_ENGINE_FLAG=0
REFRESH_AGENT_RUNTIME_FLAG=0

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

env_flag_enabled() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON) return 0 ;;
    *) return 1 ;;
  esac
}

detect_distro() {
  DISTRO_ID="unknown"
  if [[ -f /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    DISTRO_ID="${ID:-unknown}"
  fi
}

allow_system_package_install() {
  env_flag_enabled "${BOREALIS_ALLOW_SYSTEM_PACKAGE_INSTALL:-0}"
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

install_agent_dependencies() {
  if command_exists python3 && command_exists pip3; then
    return 0
  fi
  allow_system_package_install || die "Missing python3/pip3. Run bootstrap.sh -Agent first."
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
    for file in Agent_GUID.txt access.jwt access.meta.json refresh.token server_signing_key.pub; do
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
  CONFIG_PATH="${config_path}" ENROLLMENT_CODE_VALUE="${provided_code}" "${python_bin}" - <<'PY'
import json
import os
path = os.environ["CONFIG_PATH"]
code = os.environ.get("ENROLLMENT_CODE_VALUE", "")
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
}

stop_agent_supervision() {
  local unit_name="${BOREALIS_AGENT_SYSTEMD_UNIT:-borealis-agent.service}"
  if command_exists systemctl; then
    run_privileged systemctl stop "${unit_name}" >/dev/null 2>&1 || true
  fi
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
  install_agent_dependencies
  stop_agent_supervision
  local preserved_url
  preserved_url="$(capture_existing_server_url)"
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
EOF
}

parse_and_run() {
  local command="${1:-deploy}"
  shift || true
  while (($#)); do
    case "$1" in
      -ServerUrl|--ServerUrl|--serverurl|--server-url)
        shift
        SERVER_URL="${1:-}"
        ;;
      -EnrollmentCode|--EnrollmentCode|--enrollmentcode|--enrollment-code)
        shift
        ENROLLMENT_CODE="${1:-}"
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

parse_and_run "$@"
