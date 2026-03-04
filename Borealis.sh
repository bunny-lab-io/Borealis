#!/usr/bin/env bash
#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Borealis.sh
# Linux parity for Borealis.ps1.
# - Installs Linux dependencies for Engine and Agent paths
# - Mirrors Engine flow: venv + staging + Vite + Flask launch
# - Mirrors Agent flow: venv + Data/Agent staging + dependency install + settings + supervision
# - Supports parity flags: --server/--agent, --vite/--flask, --quick, --engine-tests,
#   --engine-production/--engine-dev, --enrollmentcode, --serverurl

set -o errexit
set -o nounset
set -o pipefail

# ---- Colors / Icons ----
BOREALIS_BLUE="\033[38;5;39m"
DARK_GRAY="\033[1;30m"
GREEN="\033[0;32m"
YELLOW="\033[1;33m"
RED="\033[0;31m"
RESET="\033[0m"
CHECKMARK="[OK]"; HOURGLASS="[WAIT]"; CROSSMARK="[X]"; INFO="[i]"
DEFAULT_AGENT_RUNTIME_ROOT="/opt/Borealis/Agent"

if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then
  SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
else
  SCRIPT_DIR="$(pwd)"
fi
cd "$SCRIPT_DIR"

# ---- CLI flags (parity with Borealis.ps1) ----
SERVER_FLAG=0
AGENT_FLAG=0
VITE_FLAG=0
FLASK_FLAG=0
QUICK_FLAG=0
ENGINE_TESTS_FLAG=0
ENGINE_PROD_FLAG=0
ENGINE_DEV_FLAG=0
ENROLLMENT_CODE=""
SERVER_URL=""

CHOICE=""
ENGINE_MODE_CHOICE=""
ENGINE_USE_SYSTEMD_SUPERVISION=0

while (( "$#" )); do
  case "$1" in
    -Server|--server) SERVER_FLAG=1 ;;
    -Agent|--agent|--Agent) AGENT_FLAG=1 ;;
    -Vite|--vite) VITE_FLAG=1 ;;
    -Flask|--flask) FLASK_FLAG=1 ;;
    -Quick|--quick) QUICK_FLAG=1 ;;
    -EngineTests|--engine-tests) ENGINE_TESTS_FLAG=1 ;;
    -EngineProduction|--engine-production) ENGINE_PROD_FLAG=1 ;;
    -EngineDev|--engine-dev) ENGINE_DEV_FLAG=1 ;;
    -EnrollmentCode|--EnrollmentCode|--enrollmentcode|--enrollment-code)
      shift
      ENROLLMENT_CODE="${1:-}"
      ;;
    -ServerUrl|--ServerUrl|--serverurl|--server-url)
      shift
      SERVER_URL="${1:-}"
      ;;
    *) ;; # ignore unknown for flexibility
  esac
  shift || true
done

# ---- Helpers ----
run_step() {
  local message="$1"; shift
  printf "%s %s... " "${HOURGLASS}" "$message"
  if "$@"; then
    printf "\r%s %s\n" "${CHECKMARK}" "$message"
  else
    printf "\r%s %s - Failed\n" "${CROSSMARK}" "$message" 1>&2
    exit 1
  fi
}

prompt_input() {
  local prompt="$1"
  local value=""
  if [[ -r /dev/tty ]]; then
    IFS= read -r -p "${prompt}" value < /dev/tty || true
  else
    IFS= read -r -p "${prompt}" value || true
  fi
  printf '%s' "${value}"
}

detect_distro() {
  DISTRO_ID="unknown"; DISTRO_LIKE=""
  if [[ -f /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    DISTRO_ID=${ID:-unknown}
    DISTRO_LIKE=${ID_LIKE:-}
  fi
}

need_sudo() { [ "${EUID:-$(id -u)}" -ne 0 ]; }

ensure_engine_log_dir() {
  mkdir -p "${SCRIPT_DIR}/Engine/Logs"
  echo "${SCRIPT_DIR}/Engine/Logs"
}

write_vite_log() {
  local msg="$1"; local svc="${2:-vite-dev}"
  local logdir; logdir=$(ensure_engine_log_dir)
  printf "%s-%s-%s\n" "$(date +%FT%T)" "$svc" "$msg" >> "${logdir}/vite.log"
}

write_engine_log() {
  local message="$1"
  local file_name="${2:-engine-supervision.log}"
  local logdir; logdir=$(ensure_engine_log_dir)
  printf "[%s] %s\n" "$(date +%FT%T)" "$message" >> "${logdir}/${file_name}"
}

ensure_agent_log_dir() {
  mkdir -p "${SCRIPT_DIR}/Agent/Logs"
  echo "${SCRIPT_DIR}/Agent/Logs"
}

write_agent_log() {
  local message="$1"
  local file_name="${2:-install.log}"
  local logdir; logdir=$(ensure_agent_log_dir)
  printf "[%s] %s\n" "$(date +%FT%T)" "$message" >> "${logdir}/${file_name}"
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
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

mount_options_for_target() {
  local target="$1"
  if command_exists findmnt; then
    findmnt -no OPTIONS --target "$target" 2>/dev/null || true
    return 0
  fi
  if [[ -r /proc/mounts ]]; then
    awk -v t="$target" '
      BEGIN { best_len = 0; best_opts = "" }
      {
        mp = $2
        opts = $4
        gsub("\\\\040", " ", mp)
        if (index(t, mp) == 1) {
          l = length(mp)
          if (l > best_len) {
            best_len = l
            best_opts = opts
          }
        }
      }
      END { print best_opts }
    ' /proc/mounts 2>/dev/null || true
  fi
}

target_is_noexec() {
  local target="$1"
  local opts
  opts="$(mount_options_for_target "$target")"
  [[ ",${opts}," == *,noexec,* ]]
}

resolve_agent_venv_dir() {
  if [[ -n "${BOREALIS_AGENT_VENV:-}" ]]; then
    echo "${BOREALIS_AGENT_VENV}"
    return 0
  fi

  local preferred_dir="${DEFAULT_AGENT_RUNTIME_ROOT}"
  local fallback_dir="${SCRIPT_DIR}/Agent"

  if target_is_noexec "${preferred_dir}"; then
    if ! target_is_noexec "${fallback_dir}"; then
      echo "${fallback_dir}"
      return 0
    fi
    echo "${preferred_dir}"
    return 0
  fi

  echo "${preferred_dir}"
}

resolve_python_bin() {
  if command_exists python3; then
    echo "python3"
    return 0
  fi
  if command_exists python; then
    echo "python"
    return 0
  fi
  echo ""
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

download_file() {
  local url="$1"
  local destination="$2"
  local py_bin=""

  py_bin="$(resolve_python_bin)"
  if [[ -n "${py_bin}" ]]; then
    "${py_bin}" - "${url}" "${destination}" <<'PY'
import sys
import urllib.request

url = sys.argv[1]
destination = sys.argv[2]

with urllib.request.urlopen(url, timeout=120) as response:
    with open(destination, "wb") as output:
        output.write(response.read())
PY
    return 0
  fi

  if command_exists curl; then
    curl -fsSL -o "${destination}" "${url}"
    return 0
  fi

  if command_exists wget; then
    wget -q -O "${destination}" "${url}"
    return 0
  fi

  echo -e "${RED}No supported downloader found. Install Python 3, curl, or wget.${RESET}" >&2
  return 1
}

ensure_project_layout() {
  if [[ -d "${SCRIPT_DIR}/Data/Agent" && -d "${SCRIPT_DIR}/Data/Engine" ]]; then
    return 0
  fi

  echo -e "${RED}Missing repository content under ${SCRIPT_DIR}.${RESET}" >&2
  echo -e "${YELLOW}Run bootstrap.sh first so Borealis is installed to /opt/Borealis, then re-run Borealis.sh.${RESET}" >&2
  return 1
}

verify_engine_runtime_staging() {
  local source_root="${SCRIPT_DIR}/Data/Engine"
  local runtime_root="${SCRIPT_DIR}/Engine/Data/Engine"
  local source_marker="${source_root}/security/session_secret.py"
  local runtime_marker="${runtime_root}/security/session_secret.py"
  local source_config="${source_root}/config.py"
  local runtime_config="${runtime_root}/config.py"

  if [[ -f "${source_marker}" ]]; then
    if [[ ! -f "${runtime_marker}" ]]; then
      echo -e "${RED}Engine runtime staging mismatch: missing ${runtime_marker}.${RESET}" >&2
      return 1
    fi
    if ! cmp -s "${source_marker}" "${runtime_marker}"; then
      echo -e "${RED}Engine runtime staging mismatch for security/session_secret.py.${RESET}" >&2
      return 1
    fi
  fi

  if [[ -f "${source_config}" ]]; then
    if [[ ! -f "${runtime_config}" ]]; then
      echo -e "${RED}Engine runtime staging mismatch: missing ${runtime_config}.${RESET}" >&2
      return 1
    fi
    if ! cmp -s "${source_config}" "${runtime_config}"; then
      echo -e "${RED}Engine runtime staging mismatch for config.py.${RESET}" >&2
      return 1
    fi
  fi

  return 0
}

capture_existing_server_url() {
  local settings_dir="${SCRIPT_DIR}/Agent/Borealis/Settings"
  local old_settings_dir="${SCRIPT_DIR}/Agent/Settings"
  local current_url=""
  if [[ -f "${settings_dir}/server_url.txt" ]]; then
    current_url="$(head -n 1 "${settings_dir}/server_url.txt" 2>/dev/null || true)"
  elif [[ -f "${old_settings_dir}/server_url.txt" ]]; then
    current_url="$(head -n 1 "${old_settings_dir}/server_url.txt" 2>/dev/null || true)"
  fi
  echo "${current_url:-}"
}

agent_python_bin() {
  local venv_dir
  venv_dir="$(resolve_agent_venv_dir)"
  if [[ -x "${venv_dir}/bin/python3" ]]; then
    echo "${venv_dir}/bin/python3"
  elif [[ -x "${venv_dir}/bin/python" ]]; then
    echo "${venv_dir}/bin/python"
  else
    echo ""
  fi
}

agent_runtime_script() {
  local venv_dir
  venv_dir="$(resolve_agent_venv_dir)"
  echo "${venv_dir}/Borealis/agent.py"
}

agent_runtime_dir() {
  local venv_dir
  venv_dir="$(resolve_agent_venv_dir)"
  echo "${venv_dir}"
}

verify_agent_runtime_exec_path() {
  local venv_dir
  venv_dir="$(resolve_agent_venv_dir)"
  if target_is_noexec "$venv_dir"; then
    write_agent_log "Agent runtime path '${venv_dir}' is on a noexec mount."
    return 1
  else
    return 0
  fi
}

test_webui_build_fresh() {
  local source_root="$1"
  local build_root="$2"
  local build_index="${build_root}/index.html"

  [[ -d "$source_root" ]] || return 1
  [[ -f "$build_index" ]] || return 1

  if find "$source_root" \
    \( -path "*/node_modules/*" -o -path "*/build/*" -o -path "*/dist/*" \) -prune -o \
    -type f -newer "$build_index" -print -quit | grep -q .; then
    return 1
  fi
  return 0
}

ensure_project_layout
echo -e "${INFO} Borealis source root: ${SCRIPT_DIR}"

# ---- Agent configuration ----
configure_agent_settings() {
  local preserved_server_url="${1:-}"
  echo -e "${GREEN}Configuring Borealis Agent settings...${RESET}"
  local settings_dir="${SCRIPT_DIR}/Agent/Borealis/Settings"
  local legacy_settings_dir="${SCRIPT_DIR}/Agent/Settings"
  local server_url_path="${settings_dir}/server_url.txt"
  local config_path="${settings_dir}/agent_settings.json"

  mkdir -p "${settings_dir}"
  if [[ ! -f "${server_url_path}" && -f "${legacy_settings_dir}/server_url.txt" ]]; then
    cp -f "${legacy_settings_dir}/server_url.txt" "${server_url_path}" 2>/dev/null || true
  fi

  local default_url="https://localhost:5000"
  local current_url="${default_url}"
  if [[ -n "${SERVER_URL:-}" ]]; then
    current_url="${SERVER_URL}"
  elif [[ -n "${BOREALIS_SERVER_URL:-}" ]]; then
    current_url="${BOREALIS_SERVER_URL}"
  elif [[ -n "${preserved_server_url}" ]]; then
    current_url="${preserved_server_url}"
  elif [[ -f "${server_url_path}" ]]; then
    current_url="$(head -n 1 "${server_url_path}" || echo "${default_url}")"
  fi

  local input_url=""
  if [[ -n "${SERVER_URL:-}" ]]; then
    input_url="${SERVER_URL}"
  elif [[ -n "${BOREALIS_SERVER_URL:-}" ]]; then
    input_url="${BOREALIS_SERVER_URL}"
  elif [[ -t 0 ]]; then
    input_url="$(prompt_input "Server URL [${current_url}]: ")"
  fi

  input_url="${input_url:-${current_url}}"
  input_url="$(echo -n "${input_url}" | tr -d '\r' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
  if [[ -z "${input_url}" ]]; then input_url="${default_url}"; fi
  printf "%s" "${input_url}" > "${server_url_path}"

  local provided_code="${ENROLLMENT_CODE:-}"
  if [[ -z "${provided_code}" && -n "${BOREALIS_ENROLLMENT_CODE:-}" ]]; then
    provided_code="${BOREALIS_ENROLLMENT_CODE}"
  fi

  if [[ -z "${provided_code}" ]]; then
    local existing_code=""
    local py_bin_read
    py_bin_read="$(resolve_python_bin)"
    if [[ -f "${config_path}" && -n "${py_bin_read}" ]]; then
      existing_code="$(CONFIG_PATH="${config_path}" "${py_bin_read}" - <<'PY' 2>/dev/null || true
import json
import os

path = os.environ.get("CONFIG_PATH")
try:
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    if isinstance(data, dict):
        print(data.get("enrollment_code") or data.get("installer_code") or "")
except Exception:
    pass
PY
      )"
    fi

    if [[ -t 0 ]]; then
      input_code="$(prompt_input "Enrollment Code [${existing_code}]: ")"
    else
      input_code=""
    fi

    if [[ -n "${input_code// }" ]]; then
      provided_code="${input_code}"
    elif [[ -n "${existing_code}" ]]; then
      provided_code="${existing_code}"
    else
      provided_code=""
    fi
  fi

  local py_bin
  py_bin="$(resolve_python_bin)"

  if [[ -n "${py_bin}" ]]; then
    CONFIG_PATH="${config_path}" ENROLLMENT_CODE_VALUE="${provided_code}" "${py_bin}" - <<'PY'
import json
import os

path = os.environ["CONFIG_PATH"]
code = os.environ.get("ENROLLMENT_CODE_VALUE", "")
defaults = {
    "config_file_watcher_interval": 2,
    "agent_id": "",
    "regions": {},
    "enrollment_code": "",
    "installer_code": "",
}
data = defaults.copy()
if os.path.exists(path):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            existing = json.load(fh)
        if isinstance(existing, dict):
            data.update(existing)
    except Exception:
        pass
data["enrollment_code"] = code
data["installer_code"] = code
os.makedirs(os.path.dirname(path), exist_ok=True)
with open(path, "w", encoding="utf-8") as fh:
    json.dump(data, fh)
PY
  else
    cat > "${config_path}" <<EOF
{
  "config_file_watcher_interval": 2,
  "agent_id": "",
  "regions": {},
  "enrollment_code": "${provided_code}",
  "installer_code": "${provided_code}"
}
EOF
  fi

  if [[ -n "${provided_code}" ]]; then
    echo -e "${GREEN}Enrollment code saved to agent_settings.json.${RESET}"
  else
    echo -e "${YELLOW}Enrollment code cleared in agent_settings.json.${RESET}"
  fi
}

# ---- Dependency Installation (Linux) ----
install_shared_dependencies() {
  detect_distro
  if command_exists python3 && command_exists pip3; then
    return 0
  fi

  case "$DISTRO_ID" in
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
      echo -e "${YELLOW}Unsupported distro '${DISTRO_ID}'. Install python3, python3-venv, python3-pip manually.${RESET}"
      return 1
      ;;
  esac
}

install_tesseract() {
  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      run_privileged apt update -qq
      run_privileged apt install -y tesseract-ocr
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then run_privileged dnf install -y tesseract; else run_privileged yum install -y tesseract; fi
      ;;
    arch)
      run_privileged pacman -Sy --noconfirm tesseract
      ;;
    *) : ;;
  esac
}

NODE_VERSION="v23.11.0"
NODE_DIR="${SCRIPT_DIR}/Dependencies/NodeJS"
NODE_BIN="${NODE_DIR}/bin/node"
NPM_BIN="${NODE_DIR}/bin/npm"
NPX_BIN="${NODE_DIR}/bin/npx"

install_node_portable() {
  if [[ -x "$NPM_BIN" ]]; then return 0; fi
  mkdir -p "$NODE_DIR"
  local tarball="node-${NODE_VERSION}-linux-x64.tar.xz"
  local url="https://nodejs.org/dist/${NODE_VERSION}/${tarball}"
  local dl_path="${SCRIPT_DIR}/Dependencies/${tarball}"
  write_vite_log "Downloading NodeJS ${NODE_VERSION} from ${url}" "bootstrap"
  download_file "$url" "$dl_path"
  rm -rf "${NODE_DIR:?}"/*
  tar -xJf "$dl_path" -C "$NODE_DIR" --strip-components=1
  rm -f "$dl_path"
}

ensure_node_bins() {
  if [[ -x "$NPM_BIN" ]]; then export PATH="${NODE_DIR}/bin:${PATH}"; return 0; fi
  if command -v npm >/dev/null 2>&1; then
    NPM_BIN="$(command -v npm)"; NPX_BIN="$(command -v npx || echo npx)"; return 0
  fi
  echo -e "${YELLOW}npm not found on PATH; installing portable NodeJS...${RESET}"
  install_node_portable
  export PATH="${NODE_DIR}/bin:${PATH}"
}

install_server_dependencies() {
  run_step "Dependency: Python (system)" install_shared_dependencies
  run_step "Dependency: Tesseract-OCR (system)" install_tesseract
  run_step "Dependency: WireGuard tools (system)" install_wireguard_tools_best_effort engine
  run_step "Dependency: NodeJS (portable)" install_node_portable
}

install_agent_dependencies() {
  run_step "Dependency: Python (system)" install_shared_dependencies
  run_step "Dependency: WireGuard tools (system)" install_wireguard_tools_best_effort agent
}

install_wireguard_tools_best_effort() {
  local install_target="${1:-agent}"

  if command_exists wg && command_exists wg-quick; then
    if [[ "${install_target}" == "engine" ]]; then
      write_engine_log "WireGuard tools available (wg/wg-quick)." "engine-supervision.log"
    else
      write_agent_log "WireGuard tools available (wg/wg-quick)."
    fi
    return 0
  fi

  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      run_privileged apt update -qq || true
      run_privileged apt install -y wireguard-tools || true
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        run_privileged dnf install -y wireguard-tools || true
      else
        run_privileged yum install -y wireguard-tools || true
      fi
      ;;
    arch)
      run_privileged pacman -Sy --noconfirm wireguard-tools || true
      ;;
    *)
      ;;
  esac

  if command_exists wg && command_exists wg-quick; then
    if [[ "${install_target}" == "engine" ]]; then
      write_engine_log "WireGuard tools available (wg/wg-quick)." "engine-supervision.log"
    else
      write_agent_log "WireGuard tools available (wg/wg-quick)."
    fi
  else
    if [[ "${install_target}" == "engine" ]]; then
      write_engine_log "WireGuard tools not found; VPN tunnel workflows may fail until wireguard-tools is installed." "engine-supervision.log"
    else
      write_agent_log "WireGuard tools not found; VPN tunnel workflows may fail until wireguard-tools is installed."
    fi
    echo -e "${YELLOW}WireGuard tools not found. Install 'wireguard-tools' for VPN tunnel support.${RESET}"
  fi
}

create_agent_venv_and_stage_data() {
  local venv_dir
  venv_dir="$(agent_runtime_dir)"
  local source_root="${SCRIPT_DIR}/Data/Agent"
  local destination="${venv_dir}/Borealis"
  local py_bin
  py_bin="$(resolve_python_bin)"

  [[ -n "$py_bin" ]] || {
    echo -e "${RED}Python interpreter not found. Install Python 3 first.${RESET}" >&2
    return 1
  }
  [[ -d "$source_root" ]] || {
    echo -e "${RED}Agent source directory '${source_root}' was not found.${RESET}" >&2
    return 1
  }

  mkdir -p "${venv_dir}"

  if [[ ! -x "${venv_dir}/bin/python" && ! -x "${venv_dir}/bin/python3" ]]; then
    "${py_bin}" -m venv "${venv_dir}"
  else
    local existing_py
    existing_py="$(agent_python_bin)"
    if [[ -z "${existing_py}" ]] || ! "${existing_py}" -c "import sys" >/dev/null 2>&1; then
      rm -rf "${venv_dir}" 2>/dev/null || true
      mkdir -p "${venv_dir}"
      "${py_bin}" -m venv "${venv_dir}"
    fi
  fi

  rm -rf "${destination}" 2>/dev/null || true
  mkdir -p "${destination}"

  local core_items=(
    "Python_API_Endpoints"
    "Roles"
    "Scripts"
    "agent_deployment.py"
    "agent.py"
    "ansible-ee-version.txt"
    "Borealis.ico"
    "fcntl_stub.py"
    "launch_service.ps1"
    "role_manager.py"
    "security.py"
    "signature_utils.py"
    "sitecustomize.py"
    "termios_stub.py"
  )

  local item=""
  for item in "${core_items[@]}"; do
    if [[ -e "${source_root}/${item}" ]]; then
      cp -a "${source_root}/${item}" "${destination}/"
    fi
  done

  restore_selinux_context_if_needed "${venv_dir}"
}

install_agent_python_deps() {
  local venv_py
  venv_py="$(agent_python_bin)"
  [[ -n "$venv_py" ]] || {
    echo -e "${RED}Agent virtual environment is missing Python.${RESET}" >&2
    return 1
  }

  local req_path="${SCRIPT_DIR}/Data/Agent/agent-requirements.txt"
  if [[ -f "$req_path" ]]; then
    "$venv_py" -m pip install --disable-pip-version-check -q -r "$req_path"
  fi
}

ensure_agent_systemd_service() {
  local venv_py
  venv_py="$(agent_python_bin)"
  local agent_script
  agent_script="$(agent_runtime_script)"
  local venv_dir
  venv_dir="$(agent_runtime_dir)"
  [[ -n "$venv_py" ]] || return 1
  [[ -f "${agent_script}" ]] || return 1

  local unit_name="${BOREALIS_AGENT_SYSTEMD_UNIT:-borealis-agent.service}"
  local unit_path="/etc/systemd/system/${unit_name}"
  local tmp_unit
  tmp_unit="$(ensure_agent_log_dir)/${unit_name}"
  mkdir -p "$(dirname "$tmp_unit")"

  cat > "$tmp_unit" <<EOF
[Unit]
Description=Borealis Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${SCRIPT_DIR}
Environment=BOREALIS_PROJECT_ROOT=${SCRIPT_DIR}
Environment=BOREALIS_AGENT_MODE=system
Environment=BOREALIS_AGENT_RUNTIME=${venv_dir}
ExecStart=${venv_py} ${agent_script} --system-service --config SYSTEM
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

  run_privileged cp "$tmp_unit" "$unit_path" || return 1
  run_privileged systemctl daemon-reload || return 1
  run_privileged systemctl enable "$unit_name" || return 1
  run_privileged systemctl restart "$unit_name" || return 1
  write_agent_log "Systemd service '${unit_name}' installed/restarted."
  return 0
}

start_agent_background_fallback() {
  local venv_py
  venv_py="$(agent_python_bin)"
  local agent_script
  agent_script="$(agent_runtime_script)"
  [[ -n "$venv_py" ]] || return 1
  [[ -f "$agent_script" ]] || return 1

  local logdir
  logdir="$(ensure_agent_log_dir)"
  local pid_file="${logdir}/agent.pid"
  local stdout_log="${logdir}/agent-launch.stdout.log"
  local stderr_log="${logdir}/agent-launch.stderr.log"

  if [[ -f "$pid_file" ]]; then
    local old_pid
    old_pid="$(cat "$pid_file" 2>/dev/null || true)"
    if [[ -n "${old_pid}" ]] && kill -0 "${old_pid}" >/dev/null 2>&1; then
      kill "${old_pid}" >/dev/null 2>&1 || true
      sleep 1
    fi
  fi

  (
    cd "${SCRIPT_DIR}"
    nohup "$venv_py" "$agent_script" --system-service --config SYSTEM >>"$stdout_log" 2>>"$stderr_log" &
    echo $! > "$pid_file"
  )

  local pid
  pid="$(cat "$pid_file" 2>/dev/null || true)"
  write_agent_log "Started background Borealis agent (pid=${pid:-unknown})."
  return 0
}

configure_agent_supervision() {
  if command_exists systemctl; then
    if ensure_agent_systemd_service; then
      return 0
    fi
    write_agent_log "Systemd supervision setup failed; falling back to background launch."
  fi
  start_agent_background_fallback
}

stop_agent_supervision_if_running() {
  local unit_name="${BOREALIS_AGENT_SYSTEMD_UNIT:-borealis-agent.service}"
  if ! command_exists systemctl; then
    return 0
  fi
  run_privileged systemctl stop "${unit_name}" >/dev/null 2>&1 || true
}

print_agent_service_status() {
  local unit_name="${BOREALIS_AGENT_SYSTEMD_UNIT:-borealis-agent.service}"
  if ! command_exists systemctl; then
    echo -e "${YELLOW}systemctl not available; skipping service status check.${RESET}"
    return 0
  fi

  echo -e "${GREEN}Agent service status (${unit_name}):${RESET}"
  run_privileged systemctl --no-pager --full status "${unit_name}" || true

  local active_state
  active_state="$(run_privileged systemctl is-active "${unit_name}" 2>/dev/null || true)"
  if [[ "${active_state}" != "active" && "${active_state}" != "activating" ]]; then
    if command_exists journalctl; then
      echo -e "${YELLOW}Recent ${unit_name} logs:${RESET}"
      run_privileged journalctl -u "${unit_name}" -n 40 --no-pager || true
    fi
  fi
}

install_or_update_borealis_agent() {
  echo -e "${GREEN}Ensuring Agent dependencies exist...${RESET}"
  install_agent_dependencies

  local runtime_dir
  runtime_dir="$(agent_runtime_dir)"
  echo -e "${INFO} Agent runtime directory: ${runtime_dir}"
  write_agent_log "Agent runtime directory resolved to '${runtime_dir}'."

  stop_agent_supervision_if_running

  local preserved_url
  preserved_url="$(capture_existing_server_url)"

  run_step "Create Borealis Agent virtual environment & stage runtime" create_agent_venv_and_stage_data
  run_step "Install Agent Python dependencies" install_agent_python_deps
  run_step "Configure Agent settings" configure_agent_settings "$preserved_url"
  if ! verify_agent_runtime_exec_path; then
    echo -e "${RED}Agent runtime path is on a noexec mount. Set BOREALIS_AGENT_VENV to an executable path (for example /opt/Borealis/Agent).${RESET}"
    return 1
  fi
  run_step "Configure Agent supervision" configure_agent_supervision
  print_agent_service_status
}

# Prefer a resilient resolver for the Engine venv interpreter (some venvs only have 'python')
engine_python_bin() {
  if [[ -x "${SCRIPT_DIR}/Engine/bin/python3" ]]; then
    echo "${SCRIPT_DIR}/Engine/bin/python3"
  elif [[ -x "${SCRIPT_DIR}/Engine/bin/python" ]]; then
    echo "${SCRIPT_DIR}/Engine/bin/python"
  else
    echo ""
  fi
}

# ---- Engine TLS material (parity with Ensure-EngineTlsMaterial) ----
ensure_engine_tls_material() {
  local py="$1" # engine venv python
  local cert_root_arg="$2" # optional path with pre-provided certs
  local effective_root=""

  if [[ -x "$py" ]]; then
    local code='from Data.Engine.security import certificates; certificates.ensure_certificate(); print(certificates.engine_certificates_root())'
    set +e
    effective_root="$("$py" -c "$code" 2>/dev/null | tail -n 1 | tr -d '\r' || true)"
    set -e
  fi

  if [[ -z "$effective_root" && -n "${cert_root_arg}" ]]; then
    if [[ -f "${cert_root_arg}/borealis-server-cert.pem" && -f "${cert_root_arg}/borealis-server-key.pem" ]]; then
      effective_root="${cert_root_arg}"
    else
      write_vite_log "Provided certificate root '${cert_root_arg}' missing expected TLS material; using Engine runtime certificates instead." "tls"
    fi
  fi

  if [[ -z "$effective_root" ]]; then
    effective_root="${SCRIPT_DIR}/Engine/Certificates"
    mkdir -p "$effective_root"
  fi

  export BOREALIS_CERT_DIR="$effective_root"
  export BOREALIS_TLS_CERT="${effective_root}/borealis-server-cert.pem"
  export BOREALIS_TLS_KEY="${effective_root}/borealis-server-key.pem"
  export BOREALIS_TLS_BUNDLE="${effective_root}/borealis-server-bundle.pem"
}

# ---- Engine web interface staging (parity with Ensure-EngineWebInterface) ----
ensure_engine_web_interface() {
  local project_root="$1"
  local dest="${project_root}/Engine/web-interface"
  local stage="${project_root}/Data/Engine/web-interface"
  [[ -d "$stage" ]] || { echo -e "${RED}Engine web interface source missing at '$stage'.${RESET}" >&2; return 1; }
  rm -rf "$dest" 2>/dev/null || true
  mkdir -p "$dest"
  cp -a "${stage}/." "$dest/"
  [[ -f "${dest}/package.json" ]] || { echo -e "${RED}Failed to stage Engine web interface into '$dest'.${RESET}" >&2; return 1; }
}

sync_engine_runtime() {
  local source_root="${SCRIPT_DIR}/Data/Engine"
  local destination_root="${SCRIPT_DIR}/Engine/Data/Engine"
  [[ -d "$source_root" ]] || return 1

  rm -rf "$destination_root" 2>/dev/null || true
  mkdir -p "$destination_root"

  shopt -s dotglob nullglob
  local item=""
  for item in "${source_root}"/*; do
    local base
    base="$(basename "$item")"
    if [[ "$base" == "Assemblies" ]]; then
      continue
    fi
    cp -a "$item" "${destination_root}/"
  done
  shopt -u dotglob nullglob

  sync_engine_assemblies_runtime
  verify_engine_runtime_staging
}

sync_engine_assemblies_runtime() {
  local source_assemblies="${SCRIPT_DIR}/Data/Engine/Assemblies"
  local runtime_assemblies="${SCRIPT_DIR}/Engine/Assemblies"
  local db_name=""
  mkdir -p "${runtime_assemblies}"

  if [[ ! -d "${source_assemblies}" ]]; then
    echo -e "${YELLOW}Engine assemblies source directory '${source_assemblies}' was not found. Skipping assemblies sync.${RESET}"
    write_engine_log "Engine assemblies source directory '${source_assemblies}' missing; skipped assemblies sync."
    return 0
  fi

  for db_name in official.db community.db; do
    local source_db="${source_assemblies}/${db_name}"
    local runtime_db="${runtime_assemblies}/${db_name}"
    local suffix=""

    if [[ ! -f "${source_db}" ]]; then
      echo -e "${YELLOW}Engine assemblies source database '${source_db}' was not found. Skipping ${db_name}.${RESET}"
      write_engine_log "Engine assemblies source database '${source_db}' missing; skipped ${db_name} sync."
      continue
    fi

    rm -f "${runtime_db}" "${runtime_db}-wal" "${runtime_db}-shm" 2>/dev/null || true
    cp -f "${source_db}" "${runtime_db}"
    for suffix in -wal -shm; do
      if [[ -f "${source_db}${suffix}" ]]; then
        cp -f "${source_db}${suffix}" "${runtime_db}${suffix}"
      fi
    done
  done

  local source_user_db="${source_assemblies}/user_created.db"
  local runtime_user_db="${runtime_assemblies}/user_created.db"
  if [[ ! -f "${runtime_user_db}" && -f "${source_user_db}" ]]; then
    local suffix=""
    cp -f "${source_user_db}" "${runtime_user_db}"
    for suffix in -wal -shm; do
      if [[ -f "${source_user_db}${suffix}" ]]; then
        cp -f "${source_user_db}${suffix}" "${runtime_user_db}${suffix}"
      fi
    done
  fi
}

purge_engine_runtime_for_deploy() {
  local engine_root="${SCRIPT_DIR}/Engine"
  local assemblies_root="${engine_root}/Assemblies"
  mkdir -p "${engine_root}"

  # Preserve only explicit runtime artifacts; purge everything else.
  shopt -s dotglob nullglob
  local entry=""
  for entry in "${engine_root}"/*; do
    local base
    base="$(basename "${entry}")"
    case "${base}" in
      Assemblies|Auth_Tokens|Certificates|Logs|WireGuard|database.db|engine_secret.txt)
        continue
        ;;
      *)
        rm -rf "${entry}" 2>/dev/null || true
        ;;
    esac
  done
  shopt -u dotglob nullglob

  # Inside Assemblies, preserve only user DB artifacts.
  if [[ -d "${assemblies_root}" ]]; then
    shopt -s dotglob nullglob
    local asm_entry=""
    for asm_entry in "${assemblies_root}"/*; do
      local asm_base
      asm_base="$(basename "${asm_entry}")"
      case "${asm_base}" in
        user_created.db|user_created.db-wal|user_created.db-shm)
          continue
          ;;
        *)
          rm -rf "${asm_entry}" 2>/dev/null || true
          ;;
      esac
    done
    shopt -u dotglob nullglob
  fi

  mkdir -p "${assemblies_root}" "${engine_root}/Auth_Tokens" "${engine_root}/Certificates" "${engine_root}/Logs" "${engine_root}/WireGuard"
}

engine_service_runner_path() {
  echo "${SCRIPT_DIR}/Engine/run-engine-service.sh"
}

ensure_engine_service_runner() {
  local venv_py
  venv_py="$(engine_python_bin)"
  [[ -n "${venv_py}" ]] || {
    write_engine_log "Engine service runner generation failed: engine python missing."
    return 1
  }

  local runner_path
  runner_path="$(engine_service_runner_path)"
  local npm_cmd=""
  if [[ -x "${NPM_BIN:-}" ]]; then
    npm_cmd="${NPM_BIN}"
  elif command_exists npm; then
    npm_cmd="$(command -v npm)"
  fi

  mkdir -p "$(dirname "${runner_path}")"
  cat > "${runner_path}" <<EOF
#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

MODE="\${1:-production}"
PROJECT_ROOT="${SCRIPT_DIR}"
ENGINE_DIR="\${PROJECT_ROOT}/Engine"
ENGINE_LOG_DIR="\${PROJECT_ROOT}/Engine/Logs"
ENGINE_UI_DIR="\${PROJECT_ROOT}/Engine/web-interface"
NODE_BIN_DIR="${NODE_DIR}/bin"
VENV_PY="${venv_py}"
NPM_CMD="${npm_cmd}"

mkdir -p "\${ENGINE_LOG_DIR}"
cd "\${ENGINE_DIR}"

export BOREALIS_PROJECT_ROOT="\${PROJECT_ROOT}"
export BOREALIS_ENGINE_PORT="5000"
export BOREALIS_ENGINE_MODE="\${MODE}"
export BOREALIS_CERT_DIR="\${PROJECT_ROOT}/Engine/Certificates"
export BOREALIS_TLS_CERT="\${BOREALIS_CERT_DIR}/borealis-server-cert.pem"
export BOREALIS_TLS_KEY="\${BOREALIS_CERT_DIR}/borealis-server-key.pem"
export BOREALIS_TLS_BUNDLE="\${BOREALIS_CERT_DIR}/borealis-server-bundle.pem"
export PATH="\${NODE_BIN_DIR}:\${PATH}"

if [[ ! -x "\${VENV_PY}" ]]; then
  echo "Engine python not found at \${VENV_PY}" >&2
  exit 1
fi

VITE_PID=""
cleanup() {
  if [[ -n "\${VITE_PID}" ]]; then
    kill "\${VITE_PID}" >/dev/null 2>&1 || true
    wait "\${VITE_PID}" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

if [[ "\${MODE}" == "developer" ]]; then
  if [[ -z "\${NPM_CMD}" || ! -x "\${NPM_CMD}" ]]; then
    if command -v npm >/dev/null 2>&1; then
      NPM_CMD="\$(command -v npm)"
    fi
  fi
  if [[ -z "\${NPM_CMD}" ]]; then
    echo "npm not found; cannot launch developer-mode Vite service." >&2
    exit 1
  fi
  cd "\${ENGINE_UI_DIR}"
  "\${NPM_CMD}" run dev -- --open false >>"\${ENGINE_LOG_DIR}/vite-dev.stdout.log" 2>>"\${ENGINE_LOG_DIR}/vite-dev.stderr.log" &
  VITE_PID=\$!
  cd "\${ENGINE_DIR}"
fi

"\${VENV_PY}" -m Data.Engine.bootstrapper >>"\${ENGINE_LOG_DIR}/engine-launch.stdout.log" 2>>"\${ENGINE_LOG_DIR}/engine-launch.stderr.log"
EOF

  chmod +x "${runner_path}"
  echo "${runner_path}"
}

ensure_engine_systemd_service() {
  local mode="$1" # production|developer
  local unit_name="borealis-engine.service"
  local unit_path="/etc/systemd/system/${unit_name}"
  local tmp_unit
  tmp_unit="$(ensure_engine_log_dir)/${unit_name}"

  local runner_path
  runner_path="$(ensure_engine_service_runner)"
  [[ -x "${runner_path}" ]] || {
    write_engine_log "Engine systemd unit generation failed: runner script missing."
    return 1
  }

  cat > "${tmp_unit}" <<EOF
[Unit]
Description=Borealis Engine Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${SCRIPT_DIR}/Engine
ExecStart=/usr/bin/env bash ${runner_path} ${mode}
Restart=always
RestartSec=5
KillMode=control-group
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
EOF

  run_privileged cp "${tmp_unit}" "${unit_path}" || return 1
  run_privileged systemctl daemon-reload || return 1
  run_privileged systemctl enable "${unit_name}" || return 1
  run_privileged systemctl restart "${unit_name}" || return 1
  write_engine_log "Systemd service '${unit_name}' installed/restarted in ${mode} mode."
  return 0
}

print_engine_service_status() {
  local unit_name="borealis-engine.service"
  if ! command_exists systemctl; then
    echo -e "${YELLOW}systemctl not available; skipping engine service status check.${RESET}"
    return 0
  fi

  echo -e "${GREEN}Engine service status (${unit_name}):${RESET}"
  run_privileged systemctl --no-pager --full status "${unit_name}" || true

  local active_state
  active_state="$(run_privileged systemctl is-active "${unit_name}" 2>/dev/null || true)"
  if [[ "${active_state}" != "active" && "${active_state}" != "activating" ]]; then
    if command_exists journalctl; then
      echo -e "${YELLOW}Recent ${unit_name} logs:${RESET}"
      run_privileged journalctl -u "${unit_name}" -n 40 --no-pager || true
    fi
  fi
}

configure_engine_supervision() {
  local mode="$1" # production|developer
  if command_exists systemctl; then
    ensure_engine_systemd_service "${mode}"
    print_engine_service_status
    return 0
  fi

  echo -e "${YELLOW}systemctl not available; launching Engine in the current shell instead.${RESET}"
  flask_engine_launch "${mode}"
}

# ---- Engine build+launch flow ----
create_engine_venv_and_stage_data() {
  local venv_dir="${SCRIPT_DIR}/Engine"
  local engine_src="${SCRIPT_DIR}/Data/Engine"
  local data_dest="${venv_dir}/Data/Engine"
  local py_bin
  py_bin="$(resolve_python_bin)"
  [[ -n "$py_bin" ]] || {
    echo -e "${RED}Python interpreter not found. Install Python 3 first.${RESET}" >&2
    return 1
  }

  purge_engine_runtime_for_deploy
  if [[ ! -x "${venv_dir}/bin/python" && ! -x "${venv_dir}/bin/python3" ]]; then
    "${py_bin}" -m venv "$venv_dir"
  fi
  mkdir -p "${venv_dir}/Data"

  rm -rf "$data_dest" 2>/dev/null || true
  mkdir -p "$data_dest"

  # Copy everything except Assemblies (handled separately)
  shopt -s dotglob nullglob
  for item in "${engine_src}"/*; do
    base="$(basename "$item")"
    if [[ "$base" == "Assemblies" ]]; then continue; fi
    cp -a "$item" "$data_dest/"
  done
  shopt -u dotglob nullglob

  # Assemblies runtime databases:
  # - official/community are always refreshed from staging
  # - user_created is seeded only when missing
  sync_engine_assemblies_runtime

  # Auth_Tokens and database
  mkdir -p "${SCRIPT_DIR}/Engine/Auth_Tokens"
  # database.db will be created by the app if not present; ensure dir exists
  verify_engine_runtime_staging
}

install_engine_python_deps() {
  local venv_py
  venv_py="$(engine_python_bin)"
  if [[ -z "$venv_py" ]]; then
    # Try to create the venv if it doesn't exist yet
    local py_bin
    py_bin="$(resolve_python_bin)"
    if [[ -n "$py_bin" ]]; then
      "$py_bin" -m venv "${SCRIPT_DIR}/Engine" || true
    fi
    venv_py="$(engine_python_bin)"
  fi
  local engine_src="${SCRIPT_DIR}/Data/Engine"
  local reqs=( "${engine_src}/engine-requirements.txt" "${engine_src}/requirements.txt" )
  for r in "${reqs[@]}"; do
    if [[ -f "$r" && -n "$venv_py" ]]; then
      "$venv_py" -m pip install --disable-pip-version-check -q -r "$r"
      return 0
    fi
  done
  return 0
}

vite_web_frontend_install() {
  local engine_ui_dest="${SCRIPT_DIR}/Engine/web-interface"
  ensure_node_bins
  ( cd "$engine_ui_dest" && "$NPM_BIN" install --silent --no-fund --audit=false >/dev/null )
}

vite_web_frontend_start() {
  local mode="$1" # developer|production
  local engine_ui_dest="${SCRIPT_DIR}/Engine/web-interface"
  ensure_node_bins
  ensure_engine_tls_material "$(engine_python_bin)" ""

  if [[ "$mode" == "developer" ]]; then
    if [[ "${ENGINE_USE_SYSTEMD_SUPERVISION:-0}" -eq 1 ]]; then
      write_vite_log "Skipping direct Vite dev launch; borealis-engine.service will manage developer-mode processes." "vite-dev"
      return 0
    fi
    local logdir; logdir=$(ensure_engine_log_dir)
    local stdout_log="${logdir}/vite-dev.stdout.log"
    local stderr_log="${logdir}/vite-dev.stderr.log"
    mv -f "$stdout_log" "${stdout_log}.$(date +%Y%m%d%H%M%S)" 2>/dev/null || true
    mv -f "$stderr_log" "${stderr_log}.$(date +%Y%m%d%H%M%S)" 2>/dev/null || true
    write_vite_log "Starting Vite dev server using TLS (cert=$BOREALIS_TLS_CERT bundle=$BOREALIS_TLS_BUNDLE)" "vite-dev"
    (
      cd "$engine_ui_dest"
      PATH="${NODE_DIR}/bin:${PATH}" nohup "$NPM_BIN" run dev >"$stdout_log" 2>"$stderr_log" &
    )
  else
    write_vite_log "Executing npm run build for production WebUI assets." "vite-build"
    local logdir; logdir=$(ensure_engine_log_dir)
    local stdout_log="${logdir}/vite-build.stdout.log"
    local stderr_log="${logdir}/vite-build.stderr.log"
    if ! ( cd "$engine_ui_dest" && "$NPM_BIN" run build >>"$stdout_log" 2>>"$stderr_log" ); then
      write_vite_log "npm run build failed. stderr log: ${stderr_log}" "vite-build"
      return 1
    fi
    write_vite_log "npm run build completed successfully." "vite-build"
  fi
}

flask_engine_launch() {
  local mode="$1" # production|developer
  pushd "${SCRIPT_DIR}/Engine" >/dev/null
  local py
  py="$(engine_python_bin)"
  if [[ -z "$py" ]]; then
    local py_bin
    py_bin="$(resolve_python_bin)"
    if [[ -n "$py_bin" ]]; then
      "$py_bin" -m venv "${SCRIPT_DIR}/Engine" || true
    fi
    py="$(engine_python_bin)"
  fi
  local prev_mode="${BOREALIS_ENGINE_MODE:-}"
  local prev_port="${BOREALIS_ENGINE_PORT:-}"
  local prev_root="${BOREALIS_PROJECT_ROOT:-}"
  export BOREALIS_ENGINE_MODE="$mode"
  export BOREALIS_ENGINE_PORT="5000"
  export BOREALIS_PROJECT_ROOT="$SCRIPT_DIR"
  echo -e "\n${GREEN}Launching Borealis Engine...${RESET}"
  echo "===================================================================================="
  local start_label
  if [[ "$mode" == "developer" ]]; then
    start_label="(Dev) Engine Started on https://localhost:5173"
  else
    start_label="(Production) Engine Started on https://localhost:5000"
  fi
  echo "${HOURGLASS} ${start_label}"
  local logdir; logdir=$(ensure_engine_log_dir)
  local stdout_log="${logdir}/engine-launch.stdout.log"
  local stderr_log="${logdir}/engine-launch.stderr.log"
  "$py" -m Data.Engine.bootstrapper >>"$stdout_log" 2>>"$stderr_log" || true
  # restore env
  if [[ -n "$prev_mode" ]]; then export BOREALIS_ENGINE_MODE="$prev_mode"; else unset BOREALIS_ENGINE_MODE; fi
  if [[ -n "$prev_port" ]]; then export BOREALIS_ENGINE_PORT="$prev_port"; else unset BOREALIS_ENGINE_PORT; fi
  if [[ -n "$prev_root" ]]; then export BOREALIS_PROJECT_ROOT="$prev_root"; else unset BOREALIS_PROJECT_ROOT; fi
  popd >/dev/null
}

# ---- Tests parity ----
if (( ENGINE_TESTS_FLAG )); then
  export BOREALIS_PROJECT_ROOT="${SCRIPT_DIR}"
  PYTHON_BIN="$(command -v python3 || command -v python || true)"
  if [[ -z "${PYTHON_BIN}" ]]; then
    echo -e "${RED}Python interpreter not found. Install Python 3 to run Engine tests.${RESET}" >&2
    exit 1
  fi
  "${PYTHON_BIN}" -m pytest 'Data/Engine/Unit_Tests'
  exit $?
fi

# ---- Banner ----
clear || true
printf "%b" "${BOREALIS_BLUE}"
cat << 'EOF'
:::::::::   ::::::::  :::::::::  ::::::::::     :::     :::        ::::::::::: :::::::: 
:+:    :+: :+:    :+: :+:    :+: :+:          :+: :+:   :+:            :+:    :+:    :+:
+:+    +:+ +:+    +:+ +:+    +:+ +:+         +:+   +:+  +:+            +:+    +:+       
+#++:++#+  +#+    +:+ +#++:++#:  +#++:++#   +#++:++#++: +#+            +#+    +#++:++#++
+#+    +#+ +#+    +#+ +#+    +#+ +#+        +#+     +#+ +#+            +#+           +#+
#+#    #+# #+#    #+# #+#    #+# #+#        #+#     #+# #+#            #+#    #+#    #+#
#########   ########  ###    ### ########## ###     ### ########## ########### ######## 
EOF
printf "%b" "${RESET}"
printf "%b\n" "${DARK_GRAY}Automation Platform${RESET}"

# ---- Menus ----
server_menu() {
  local mode_choice="${1:-}"
  local borealis_operation_mode="production"
  local engine_immediate_launch=0

  if [[ -z "${mode_choice}" ]]; then
    echo -e "\nConfigure Borealis Engine Mode:"
    echo -e " 1) Build & Launch > Production Flask Server @ https://localhost:5000"
    echo -e " 2) [Skip Build] & Immediately Launch > Production Flask Server @ https://localhost:5000"
    echo -e " 3) Launch > [Hotload-Ready] Vite Dev Server @ https://localhost:5173"
    mode_choice="$(prompt_input "Enter choice [1/2/3]: ")"
  else
    echo -e "${YELLOW}Auto-selecting Borealis Engine mode option ${mode_choice}.${RESET}"
  fi

  case "$mode_choice" in
    1) borealis_operation_mode="production" ;;
    2) borealis_operation_mode="production"; engine_immediate_launch=1 ;;
    3) borealis_operation_mode="developer" ;;
    *) echo -e "${RED}Invalid mode choice${RESET}"; return 1 ;;
  esac

  echo -e "${GREEN}Ensuring Engine Dependencies Exist...${RESET}"
  echo -e "${INFO} Engine source path: ${SCRIPT_DIR}/Data/Engine"
  echo -e "${INFO} Engine runtime path: ${SCRIPT_DIR}/Engine/Data/Engine"
  install_server_dependencies
  export PATH="${NODE_DIR}/bin:${PATH}"
  ENGINE_USE_SYSTEMD_SUPERVISION=0
  if command_exists systemctl; then
    ENGINE_USE_SYSTEMD_SUPERVISION=1
  fi

  if [[ "$engine_immediate_launch" -eq 1 ]]; then
    if ! test_webui_build_fresh "${SCRIPT_DIR}/Data/Engine/web-interface" "${SCRIPT_DIR}/Engine/web-interface/build"; then
      echo -e "${YELLOW}Detected newer WebUI source than production build. Running full build instead of Quick/Skip.${RESET}"
      engine_immediate_launch=0
    fi
  fi

  if [[ "$engine_immediate_launch" -eq 1 ]]; then
    run_step "Sync Engine runtime code from Data/Engine" sync_engine_runtime
    if [[ "${ENGINE_USE_SYSTEMD_SUPERVISION}" -eq 1 ]]; then
      run_step "Configure Borealis Engine systemd service (${borealis_operation_mode})" configure_engine_supervision "$borealis_operation_mode"
    else
      run_step "Borealis Engine: Launch Flask Server" flask_engine_launch "$borealis_operation_mode"
    fi
    return 0
  fi

  run_step "Create Borealis Engine Virtual Python Environment & Stage Data" create_engine_venv_and_stage_data
  run_step "Install Engine Python Dependencies" install_engine_python_deps
  run_step "Copy Engine WebUI Files" ensure_engine_web_interface "$SCRIPT_DIR"
  run_step "Vite Web Frontend: Install NPM Packages" vite_web_frontend_install
  run_step "Vite Web Frontend: Start (${borealis_operation_mode})" vite_web_frontend_start "$borealis_operation_mode"
  if [[ "${ENGINE_USE_SYSTEMD_SUPERVISION}" -eq 1 ]]; then
    run_step "Configure Borealis Engine systemd service (${borealis_operation_mode})" configure_engine_supervision "$borealis_operation_mode"
  else
    run_step "Borealis Engine: Launch Flask Server" flask_engine_launch "$borealis_operation_mode"
  fi
}

agent_menu() {
  echo -e "\nDeploying Borealis Agent..."
  install_or_update_borealis_agent
}

main_menu() {
  echo -e "\nPlease choose which function you want to launch:"
  echo -e " 1) Borealis Engine"
  echo -e " 2) Borealis Agent"
  echo -e " 3) Exit"
  choice="$(prompt_input "Enter a number: ")"
  case "$choice" in
    1) server_menu ;;
    2) agent_menu ;;
    3) exit 0 ;;
    *) echo -e "${RED}Invalid selection. Exiting...${RESET}"; exit 1 ;;
  esac
}

# ---- Flag validation parity ----
if [[ $SERVER_FLAG -eq 1 && $AGENT_FLAG -eq 1 ]]; then
  echo -e "${RED}Cannot use --server and --agent together.${RESET}"
  exit 1
fi

if [[ $VITE_FLAG -eq 1 && $FLASK_FLAG -eq 1 ]]; then
  echo -e "${RED}Cannot combine --vite and --flask.${RESET}"
  exit 1
fi

if [[ $ENGINE_PROD_FLAG -eq 1 && $ENGINE_DEV_FLAG -eq 1 ]]; then
  echo -e "${RED}Cannot combine --engine-production and --engine-dev.${RESET}"
  exit 1
fi

if [[ ($ENGINE_PROD_FLAG -eq 1 || $ENGINE_DEV_FLAG -eq 1) && ($SERVER_FLAG -eq 1 || $AGENT_FLAG -eq 1) ]]; then
  echo -e "${RED}Engine automation switches cannot be combined with --server or --agent.${RESET}"
  exit 1
fi

# ---- Flag-driven auto-select (matches Borealis.ps1 behavior) ----
if [[ $SERVER_FLAG -eq 1 ]]; then
  CHOICE="1"
fi
if [[ $AGENT_FLAG -eq 1 ]]; then
  CHOICE="2"
fi
if [[ $ENGINE_PROD_FLAG -eq 1 || $ENGINE_DEV_FLAG -eq 1 ]]; then
  CHOICE="1"
  if [[ $ENGINE_PROD_FLAG -eq 1 ]]; then
    ENGINE_MODE_CHOICE="1"
    if [[ $QUICK_FLAG -eq 1 ]]; then
      ENGINE_MODE_CHOICE="2"
    fi
  fi
  if [[ $ENGINE_DEV_FLAG -eq 1 ]]; then
    ENGINE_MODE_CHOICE="3"
  fi
fi

# Preserve pre-existing server flow behavior for explicit --server use
if [[ $SERVER_FLAG -eq 1 && -z "${ENGINE_MODE_CHOICE}" ]]; then
  if [[ $VITE_FLAG -eq 1 ]]; then
    ENGINE_MODE_CHOICE="3"
  elif [[ $FLASK_FLAG -eq 1 && $QUICK_FLAG -eq 1 ]]; then
    ENGINE_MODE_CHOICE="2"
  else
    ENGINE_MODE_CHOICE="1"
  fi
fi

if [[ -n "${CHOICE}" ]]; then
  case "${CHOICE}" in
    1) server_menu "${ENGINE_MODE_CHOICE}" ; exit $? ;;
    2) agent_menu ; exit $? ;;
    *) echo -e "${RED}Invalid selection. Exiting...${RESET}" ; exit 1 ;;
  esac
fi

# Default to interactive menu
main_menu
