#!/usr/bin/env bash
#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Borealis.sh
# Linux parity for Borealis.ps1.
# - Installs Linux dependencies for Engine and Agent paths
# - Mirrors Engine flow: venv + staging + Vite + Flask launch
# - Mirrors Agent flow: venv + Data/Agent staging + dependency install + settings + supervision
# - Supports parity flags: --server/--agent, --vite/--flask, --quick, --engine-tests,
#   --engine-production/--engine-dev, --enrollmentcode, --serverurl, --newEngine

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
REFRESH_AGENT_RUNTIME_FLAG=0
NEW_ENGINE_FLAG=0
ENROLLMENT_CODE=""
SERVER_URL=""
BOOTSTRAP_NEW_ENGINE_DEFAULT="${BOREALIS_BOOTSTRAP_NEW_ENGINE:-}"

CHOICE=""
ENGINE_MODE_CHOICE=""
ENGINE_USE_SYSTEMD_SUPERVISION=0
POSTGRES_VERSION="${BOREALIS_PG_VERSION:-17}"
ENGINE_DB_ENV_FILE="${SCRIPT_DIR}/Engine/database.env"
ENGINE_DB_PASSWORD_FILE="${SCRIPT_DIR}/Engine/.postgres-password"

while (( "$#" )); do
  case "$1" in
    -Server|--server) SERVER_FLAG=1 ;;
    -Agent|--agent|--Agent) AGENT_FLAG=1 ;;
    -Vite|--vite) VITE_FLAG=1 ;;
    -Flask|--flask) FLASK_FLAG=1 ;;
    -Quick|--quick) QUICK_FLAG=1 ;;
    -EngineTests|--EngineTests|--engine-tests) ENGINE_TESTS_FLAG=1 ;;
    -EngineProduction|--engine-production) ENGINE_PROD_FLAG=1 ;;
    -EngineDev|--engine-dev) ENGINE_DEV_FLAG=1 ;;
    --refresh-agent-runtime) REFRESH_AGENT_RUNTIME_FLAG=1 ;;
    -NewEngine|--newEngine|--newengine|-DeleteServerTrust|--delete-servertrust|--deleteservertrust|-ForceReEnroll|--force-reenroll|--forcereenroll) NEW_ENGINE_FLAG=1 ;;
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

env_flag_enabled() {
  local value="${1:-}"
  case "${value,,}" in
    1|true|yes|on) return 0 ;;
    *) return 1 ;;
  esac
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

resolve_engine_database_url() {
  if [[ -n "${BOREALIS_DATABASE_URL:-}" ]]; then
    echo "${BOREALIS_DATABASE_URL}"
    return 0
  fi
  if [[ -f "${ENGINE_DB_ENV_FILE}" ]]; then
    # shellcheck disable=SC1090
    . "${ENGINE_DB_ENV_FILE}"
    if [[ -n "${BOREALIS_DATABASE_URL:-}" ]]; then
      echo "${BOREALIS_DATABASE_URL}"
      return 0
    fi
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

run_as_postgres() {
  local command="$1"
  if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
    su - postgres -s /bin/bash -c "${command}"
    return $?
  fi
  if command_exists sudo; then
    sudo -u postgres bash -lc "${command}"
    return $?
  fi
  echo -e "${RED}PostgreSQL setup requires sudo or root access.${RESET}" >&2
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

clear_agent_enrollment_state() {
  local settings_dirs=(
    "${SCRIPT_DIR}/Agent/Borealis/Settings"
    "${SCRIPT_DIR}/Agent/Settings"
  )
  local files_to_remove=(
    "Agent_GUID.txt"
    "access.jwt"
    "access.meta.json"
    "refresh.token"
    "server_signing_key.pub"
  )

  write_agent_log "Force reenroll requested; clearing persisted enrollment state while preserving the device identity keypair."

  local settings_dir=""
  local rel_path=""
  local target=""
  for settings_dir in "${settings_dirs[@]}"; do
    for rel_path in "${files_to_remove[@]}"; do
      target="${settings_dir}/${rel_path}"
      if [[ -e "${target}" ]]; then
        rm -f "${target}" 2>/dev/null || true
        write_agent_log "Removed persisted enrollment artifact '${target}'."
      fi
    done
  done

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
  local noninteractive_mode="${2:-0}"
  echo -e "${GREEN}Configuring Borealis Agent settings...${RESET}"
  local settings_dir="${SCRIPT_DIR}/Agent/Borealis/Settings"
  local legacy_settings_dir="${SCRIPT_DIR}/Agent/Settings"
  local server_url_path="${settings_dir}/server_url.txt"
  local config_path="${settings_dir}/agent_settings.json"

  mkdir -p "${settings_dir}"
  if [[ ! -f "${server_url_path}" && -f "${legacy_settings_dir}/server_url.txt" ]]; then
    cp -f "${legacy_settings_dir}/server_url.txt" "${server_url_path}" 2>/dev/null || true
  fi

  local default_url="https://$(resolve_engine_public_fqdn)"
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
  elif [[ "${noninteractive_mode}" -ne 1 && -t 0 ]]; then
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

    if [[ "${noninteractive_mode}" -ne 1 && -t 0 ]]; then
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

postgres_service_name() {
  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      echo "postgresql"
      ;;
    rhel|centos|fedora|rocky|almalinux)
      echo "postgresql-${POSTGRES_VERSION}"
      ;;
    *)
      echo "postgresql"
      ;;
  esac
}

install_postgresql_best_effort() {
  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      run_privileged apt update -qq
      run_privileged apt install -y ca-certificates curl postgresql-common
      run_privileged install -d -m 0755 /usr/share/postgresql-common/pgdg
      run_privileged curl -fsSL -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc \
        https://www.postgresql.org/media/keys/ACCC4CF8.asc
      local codename=""
      if [[ -f /etc/os-release ]]; then
        # shellcheck disable=SC1091
        . /etc/os-release
        codename="${VERSION_CODENAME:-}"
      fi
      [[ -n "${codename}" ]] || { echo -e "${RED}Unable to determine Debian/Ubuntu codename for PGDG setup.${RESET}" >&2; return 1; }
      printf 'deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt %s-pgdg main\n' "${codename}" | \
        run_privileged tee /etc/apt/sources.list.d/pgdg.list >/dev/null
      run_privileged apt update -qq
      if ! run_privileged apt install -y "postgresql-${POSTGRES_VERSION}" "postgresql-client-${POSTGRES_VERSION}"; then
        echo -e "${YELLOW}PGDG versioned packages were unavailable; falling back to distro PostgreSQL packages.${RESET}"
        run_privileged apt install -y postgresql postgresql-client
      fi
      ;;
    rhel|centos|fedora|rocky|almalinux)
      local releasever=""
      if [[ -f /etc/os-release ]]; then
        # shellcheck disable=SC1091
        . /etc/os-release
        releasever="${VERSION_ID%%.*}"
      fi
      [[ -n "${releasever}" ]] || { echo -e "${RED}Unable to determine EL release for PGDG setup.${RESET}" >&2; return 1; }
      local arch
      arch="$(uname -m)"
      run_privileged rpm -Uvh --force "https://download.postgresql.org/pub/repos/yum/reporpms/EL-${releasever}-${arch}/pgdg-redhat-repo-latest.noarch.rpm"
      if command_exists dnf; then
        run_privileged dnf -qy module disable postgresql || true
        run_privileged dnf install -y "postgresql${POSTGRES_VERSION}-server" "postgresql${POSTGRES_VERSION}" || \
          run_privileged dnf install -y "postgresql${POSTGRES_VERSION//./}-server" "postgresql${POSTGRES_VERSION//./}"
      else
        run_privileged yum -y module disable postgresql || true
        run_privileged yum install -y "postgresql${POSTGRES_VERSION}-server" "postgresql${POSTGRES_VERSION}" || \
          run_privileged yum install -y "postgresql${POSTGRES_VERSION//./}-server" "postgresql${POSTGRES_VERSION//./}"
      fi
      ;;
    *)
      echo -e "${YELLOW}Unsupported distro '${DISTRO_ID}' for automated PostgreSQL install.${RESET}"
      return 1
      ;;
  esac
}

ensure_engine_database_env_file() {
  mkdir -p "$(dirname "${ENGINE_DB_ENV_FILE}")"
  local password=""
  if [[ -f "${ENGINE_DB_PASSWORD_FILE}" ]]; then
    password="$(cat "${ENGINE_DB_PASSWORD_FILE}")"
  fi
  if [[ -z "${password}" ]]; then
    password="$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32)"
    printf '%s' "${password}" > "${ENGINE_DB_PASSWORD_FILE}"
    chmod 600 "${ENGINE_DB_PASSWORD_FILE}"
  fi
  cat > "${ENGINE_DB_ENV_FILE}" <<EOF
export BOREALIS_DATABASE_URL="postgresql+psycopg://borealis_engine:${password}@127.0.0.1:5432/borealis"
export BOREALIS_DB_SSLMODE="${BOREALIS_DB_SSLMODE:-prefer}"
export BOREALIS_DB_POOL_SIZE="${BOREALIS_DB_POOL_SIZE:-10}"
export BOREALIS_DB_MAX_OVERFLOW="${BOREALIS_DB_MAX_OVERFLOW:-20}"
export BOREALIS_DB_CONNECT_TIMEOUT="${BOREALIS_DB_CONNECT_TIMEOUT:-15}"
EOF
  chmod 600 "${ENGINE_DB_ENV_FILE}"
}

ensure_engine_postgresql_ready() {
  ensure_engine_database_env_file || return 1
  local service_name
  service_name="$(postgres_service_name)"
  detect_distro
  case "$DISTRO_ID" in
    rhel|centos|fedora|rocky|almalinux)
      local setup_bin="/usr/pgsql-${POSTGRES_VERSION}/bin/postgresql-${POSTGRES_VERSION}-setup"
      if [[ -x "${setup_bin}" ]]; then
        run_privileged "${setup_bin}" initdb >/dev/null 2>&1 || true
      fi
      ;;
    *)
      ;;
  esac
  if command_exists systemctl; then
    run_privileged systemctl enable "${service_name}" || true
    run_privileged systemctl restart "${service_name}" || true
  fi

  # shellcheck disable=SC1090
  . "${ENGINE_DB_ENV_FILE}"
  local engine_password="${BOREALIS_DATABASE_URL#*://borealis_engine:}"
  engine_password="${engine_password%@127.0.0.1:5432/borealis}"

  if command_exists psql; then
    run_as_postgres "psql postgres -v ON_ERROR_STOP=1 <<'EOF'
DO \$\$
BEGIN
   IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'borealis_engine') THEN
      CREATE ROLE borealis_engine LOGIN PASSWORD '${engine_password}';
   ELSE
      ALTER ROLE borealis_engine WITH LOGIN PASSWORD '${engine_password}';
   END IF;
END
\$\$;
EOF
"
    run_as_postgres "psql postgres -tAc \"SELECT 1 FROM pg_database WHERE datname='borealis'\"" | grep -q 1 || \
      run_as_postgres "createdb -O borealis_engine borealis"
  fi
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
  run_step "Dependency: PostgreSQL (system)" install_postgresql_best_effort
  run_step "Dependency: Tesseract-OCR (system)" install_tesseract
  run_step "Dependency: WireGuard tools (system)" install_wireguard_tools_best_effort engine
  run_step "Dependency: Traefik (system)" install_traefik_best_effort
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
      mkdir -p "${venv_dir}"
      rm -rf \
        "${venv_dir}/bin" \
        "${venv_dir}/include" \
        "${venv_dir}/lib" \
        "${venv_dir}/lib64" \
        "${venv_dir}/share" \
        "${venv_dir}/pyvenv.cfg" 2>/dev/null || true
      "${py_bin}" -m venv "${venv_dir}"
    fi
  fi

  mkdir -p "${destination}"

  local core_items=(
    "Python_API_Endpoints"
    "Roles"
    "Scripts"
    "agent_deployment.py"
    "agent.py"
    "Borealis.ico"
    "fcntl_stub.py"
    "launch_service.ps1"
    "role_health.py"
    "role_manager.py"
    "security.py"
    "signature_utils.py"
    "sitecustomize.py"
    "termios_stub.py"
    "update_helper.py"
    "update_state.py"
  )

  local item=""
  for item in "${core_items[@]}"; do
    rm -rf "${destination}/${item}" 2>/dev/null || true
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

ensure_agent_updater_systemd_units() {
  if ! command_exists systemctl; then
    write_agent_log "systemctl unavailable; skipping Borealis agent updater timer."
    return 0
  fi

  local updater_script="${SCRIPT_DIR}/Update.sh"
  if [[ ! -f "${updater_script}" ]]; then
    write_agent_log "Update.sh not found; skipping Borealis agent updater timer."
    return 1
  fi

  local service_name="borealis-agent-updater.service"
  local timer_name="borealis-agent-updater.timer"
  local service_path="/etc/systemd/system/${service_name}"
  local timer_path="/etc/systemd/system/${timer_name}"
  local logdir
  logdir="$(ensure_agent_log_dir)"
  local service_tmp="${logdir}/${service_name}"
  local timer_tmp="${logdir}/${timer_name}"

  cat > "${service_tmp}" <<EOF
[Unit]
Description=Borealis Agent Auto Updater
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
WorkingDirectory=${SCRIPT_DIR}
Environment=BOREALIS_PROJECT_ROOT=${SCRIPT_DIR}
ExecStart=/usr/bin/env bash ${updater_script}
EOF

  cat > "${timer_tmp}" <<EOF
[Unit]
Description=Borealis Agent Auto Updater Timer

[Timer]
OnCalendar=*-*-* *:00:00
OnBootSec=5min
Persistent=true
AccuracySec=1s
Unit=${service_name}

[Install]
WantedBy=timers.target
EOF

  run_privileged cp "${service_tmp}" "${service_path}" || return 1
  run_privileged cp "${timer_tmp}" "${timer_path}" || return 1
  run_privileged chmod 644 "${service_path}" "${timer_path}" || true
  run_privileged chmod +x "${updater_script}" || true
  run_privileged systemctl daemon-reload || return 1
  run_privileged systemctl enable "${timer_name}" || return 1
  run_privileged systemctl restart "${timer_name}" || return 1
  write_agent_log "Systemd updater timer '${timer_name}' installed/restarted."
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
      ensure_agent_updater_systemd_units || write_agent_log "Agent updater timer setup failed."
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
  local noninteractive_mode="${1:-0}"
  if [[ "${NEW_ENGINE_FLAG}" -eq 0 ]] && env_flag_enabled "${BOOTSTRAP_NEW_ENGINE_DEFAULT}"; then
    NEW_ENGINE_FLAG=1
    echo -e "${INFO} Bootstrap-invoked agent install detected; enabling --newEngine behavior."
  fi

  echo -e "${GREEN}Ensuring Agent dependencies exist...${RESET}"
  install_agent_dependencies

  local runtime_dir
  runtime_dir="$(agent_runtime_dir)"
  echo -e "${INFO} Agent runtime directory: ${runtime_dir}"
  write_agent_log "Agent runtime directory resolved to '${runtime_dir}'."

  stop_agent_supervision_if_running

  local preserved_url
  preserved_url="$(capture_existing_server_url)"

  if [[ "${NEW_ENGINE_FLAG}" -eq 1 ]]; then
    run_step "Clear persisted Borealis Agent enrollment state" clear_agent_enrollment_state
  fi

  run_step "Create Borealis Agent virtual environment & stage runtime" create_agent_venv_and_stage_data
  run_step "Install Agent Python dependencies" install_agent_python_deps
  run_step "Configure Agent settings" configure_agent_settings "$preserved_url" "$noninteractive_mode"
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

engine_ansible_root_dir() {
  echo "${SCRIPT_DIR}/Engine/Ansible"
}

engine_ansible_source_dir() {
  echo "${SCRIPT_DIR}/Data/Engine/Ansible"
}

engine_ansible_collections_dir() {
  echo "$(engine_ansible_root_dir)/collections"
}

engine_ansible_requirements_path() {
  echo "$(engine_ansible_root_dir)/collections.yml"
}

migrate_engine_ansible_layout() {
  local ansible_root
  ansible_root="$(engine_ansible_root_dir)"
  mkdir -p "${ansible_root}"

  local legacy_collections="${SCRIPT_DIR}/Engine/collections"
  local target_collections
  target_collections="$(engine_ansible_collections_dir)"
  if [[ -d "${legacy_collections}" ]]; then
    mkdir -p "${target_collections}"
    cp -a "${legacy_collections}/." "${target_collections}/"
    rm -rf "${legacy_collections}"
  fi

  local legacy_generated="${SCRIPT_DIR}/Engine/Generated/Ansible_Runtime"
  local target_generated="${ansible_root}/Generated/Runtime"
  if [[ -d "${legacy_generated}" ]]; then
    mkdir -p "${target_generated}"
    cp -a "${legacy_generated}/." "${target_generated}/"
    rm -rf "${legacy_generated}"
  fi
}

stage_engine_ansible_assets() {
  local source_dir
  source_dir="$(engine_ansible_source_dir)"
  local runtime_dir
  runtime_dir="$(engine_ansible_root_dir)"
  local runtime_manifest
  runtime_manifest="$(engine_ansible_requirements_path)"
  mkdir -p "${runtime_dir}"

  if [[ -d "${source_dir}" ]]; then
    shopt -s dotglob nullglob
    local item=""
    for item in "${source_dir}"/*; do
      cp -a "${item}" "${runtime_dir}/"
    done
    shopt -u dotglob nullglob
  fi

  local legacy_manifest="${SCRIPT_DIR}/Data/Engine/ansible-collections.yml"
  if [[ -f "${legacy_manifest}" && ! -f "${runtime_manifest}" ]]; then
    cp -a "${legacy_manifest}" "${runtime_manifest}"
  fi
}

prepare_engine_ansible_layout() {
  migrate_engine_ansible_layout || return 1
  stage_engine_ansible_assets || return 1
}

engine_site_packages_dir() {
  local venv_py
  venv_py="$(engine_python_bin)"
  [[ -n "${venv_py}" ]] || {
    echo ""
    return 0
  }
  "${venv_py}" - <<'PY'
import sysconfig
print(sysconfig.get_path("purelib") or "")
PY
}

verify_engine_venv_writable() {
  local site_packages_dir
  site_packages_dir="$(engine_site_packages_dir)"
  [[ -n "${site_packages_dir}" ]] || return 1
  if [[ -w "${site_packages_dir}" ]]; then
    return 0
  fi
  local venv_dir="${SCRIPT_DIR}/Engine"
  local py_bin
  py_bin="$(resolve_python_bin)"
  if [[ -n "${py_bin}" && -w "${venv_dir}" ]]; then
    echo -e "${YELLOW}Repairing stale Engine virtual environment ownership under '${venv_dir}'.${RESET}" >&2
    local repair_stamp
    repair_stamp="$(date +%s)"
    local stale_path=""
    for stale_path in "${venv_dir}/bin" "${venv_dir}/include" "${venv_dir}/lib" "${venv_dir}/lib64" "${venv_dir}/share"; do
      if [[ -e "${stale_path}" ]]; then
        mv "${stale_path}" "${stale_path}.stale.${repair_stamp}" 2>/dev/null || true
      fi
    done
    rm -f "${venv_dir}/pyvenv.cfg" 2>/dev/null || true
    if "${py_bin}" -m venv "${venv_dir}" >/dev/null 2>&1; then
      site_packages_dir="$(engine_site_packages_dir)"
      if [[ -n "${site_packages_dir}" && -w "${site_packages_dir}" ]]; then
        return 0
      fi
    fi
  fi
  echo -e "${RED}Engine virtual environment is not writable at '${site_packages_dir}'.${RESET}" >&2
  echo -e "${YELLOW}Borealis could not repair the Engine venv automatically; recreate '${venv_dir}' with the current user before installing Python packages.${RESET}" >&2
  return 1
}

engine_ansible_galaxy_bin() {
  if [[ -x "${SCRIPT_DIR}/Engine/bin/ansible-galaxy" ]]; then
    echo "${SCRIPT_DIR}/Engine/bin/ansible-galaxy"
  else
    echo ""
  fi
}

engine_ansible_playbook_bin() {
  if [[ -x "${SCRIPT_DIR}/Engine/bin/ansible-playbook" ]]; then
    echo "${SCRIPT_DIR}/Engine/bin/ansible-playbook"
  else
    echo ""
  fi
}

install_engine_ansible_collections() {
  local venv_py
  venv_py="$(engine_python_bin)"
  [[ -n "$venv_py" ]] || {
    echo -e "${RED}Engine virtual environment is missing Python.${RESET}" >&2
    return 1
  }
  verify_engine_venv_writable || return 1
  prepare_engine_ansible_layout || return 1

  local collections_req
  collections_req="$(engine_ansible_requirements_path)"
  [[ -f "${collections_req}" ]] || return 0

  local collections_dir
  collections_dir="$(engine_ansible_collections_dir)"
  mkdir -p "${collections_dir}"

  local galaxy_bin
  galaxy_bin="$(engine_ansible_galaxy_bin)"
  local -a cmd
  if [[ -n "${galaxy_bin}" ]]; then
    cmd=("${galaxy_bin}" collection install -r "${collections_req}" -p "${collections_dir}" --upgrade)
  else
    cmd=("${venv_py}" -m ansible.cli.galaxy collection install -r "${collections_req}" -p "${collections_dir}" --upgrade)
  fi

  ANSIBLE_COLLECTIONS_PATH="${collections_dir}" \
  ANSIBLE_COLLECTIONS_PATHS="${collections_dir}" \
    "${cmd[@]}"
}

verify_engine_ansible_runtime() {
  local venv_py
  venv_py="$(engine_python_bin)"
  [[ -n "$venv_py" ]] || return 1
  prepare_engine_ansible_layout || return 1

  local playbook_bin
  playbook_bin="$(engine_ansible_playbook_bin)"
  local collections_dir
  collections_dir="$(engine_ansible_collections_dir)"
  local collections_req
  collections_req="$(engine_ansible_requirements_path)"
  local -a version_cmd
  if [[ -n "${playbook_bin}" ]]; then
    version_cmd=("${playbook_bin}" --version)
  else
    version_cmd=("${venv_py}" -m ansible.cli.playbook --version)
  fi

  "${venv_py}" - <<'PY'
import importlib
required = ("ansible", "ansible_runner", "winrm", "pypsrp")
missing = [name for name in required if importlib.util.find_spec(name) is None]
if missing:
    raise SystemExit("Missing Python modules: " + ", ".join(missing))
PY

  if [[ -f "${collections_req}" ]]; then
    ANSIBLE_COLLECTIONS_PATH="${collections_dir}" \
    ANSIBLE_COLLECTIONS_PATHS="${collections_dir}" \
      "${version_cmd[@]}" >/dev/null
  fi
}

# ---- Engine TLS material ----
ensure_engine_tls_material() {
  unset BOREALIS_CERT_DIR
  return 0
}

engine_letsencrypt_settings_path() {
  echo "${SCRIPT_DIR}/Engine/LetsEncrypt/Settings.json"
}

engine_letsencrypt_runtime_env_path() {
  echo "${SCRIPT_DIR}/Engine/LetsEncrypt/runtime.env"
}

engine_letsencrypt_acme_storage_path() {
  echo "${SCRIPT_DIR}/Engine/LetsEncrypt/acme.json"
}

engine_traefik_static_config_path() {
  echo "${SCRIPT_DIR}/Engine/Traefik/traefik.yml"
}

engine_traefik_dynamic_config_path() {
  echo "${SCRIPT_DIR}/Engine/Traefik/dynamic.yml"
}

parse_url_hostname() {
  local raw="${1:-}"
  local py_bin=""
  py_bin="$(resolve_python_bin)"
  if [[ -z "${py_bin}" ]]; then
    printf '%s' "${raw}"
    return 0
  fi
  INPUT_URL="${raw}" "${py_bin}" - <<'PY'
from urllib.parse import urlsplit
import os

raw = (os.environ.get("INPUT_URL") or "").strip()
if not raw:
    raise SystemExit(0)
if "://" not in raw:
    raw = f"https://{raw}"
try:
    parsed = urlsplit(raw)
except Exception:
    print(raw)
else:
    print((parsed.hostname or "").strip())
PY
}

resolve_engine_public_fqdn() {
  local explicit="${BOREALIS_PUBLIC_FQDN:-}"
  if [[ -n "${explicit}" ]]; then
    parse_url_hostname "${explicit}"
    return 0
  fi

  local settings_path
  settings_path="$(engine_letsencrypt_settings_path)"
  if [[ -f "${settings_path}" ]]; then
    local py_bin
    py_bin="$(resolve_python_bin)"
    if [[ -n "${py_bin}" ]]; then
      local configured_fqdn
      configured_fqdn="$(INPUT_SETTINGS_PATH="${settings_path}" "${py_bin}" - <<'PY'
import json
import os
from urllib.parse import urlsplit

path = (os.environ.get("INPUT_SETTINGS_PATH") or "").strip()
if not path or not os.path.isfile(path):
    raise SystemExit(0)

try:
    with open(path, "r", encoding="utf-8") as handle:
        raw = json.load(handle)
except Exception:
    raise SystemExit(0)

value = ""
if isinstance(raw, dict):
    value = str(raw.get("fqdn") or "").strip()
if not value:
    raise SystemExit(0)
if "://" not in value:
    value = f"https://{value}"
try:
    parsed = urlsplit(value)
except Exception:
    raise SystemExit(0)
host = (parsed.hostname or "").strip()
if host:
    print(host)
PY
)"
      if [[ -n "${configured_fqdn}" ]]; then
        echo "${configured_fqdn}"
        return 0
      fi
    fi
  fi

  if [[ -n "${SERVER_URL:-}" ]]; then
    local host
    host="$(parse_url_hostname "${SERVER_URL}")"
    if [[ -n "${host}" ]]; then
      echo "${host}"
      return 0
    fi
  fi

  if [[ -n "${BOREALIS_SERVER_URL:-}" ]]; then
    local host
    host="$(parse_url_hostname "${BOREALIS_SERVER_URL}")"
    if [[ -n "${host}" ]]; then
      echo "${host}"
      return 0
    fi
  fi

  if command_exists hostname; then
    local fqdn
    fqdn="$(hostname -f 2>/dev/null || hostname 2>/dev/null || true)"
    fqdn="${fqdn%%[[:space:]]*}"
    if [[ -n "${fqdn}" ]]; then
      echo "${fqdn}"
      return 0
    fi
  fi

  echo "localhost"
}

looks_like_public_fqdn() {
  local raw="${1:-}"
  local host
  host="$(parse_url_hostname "${raw}")"
  [[ -n "${host}" ]] || return 1
  [[ "${host}" == *.* ]] || return 1
  [[ "${host}" != "localhost" ]] || return 1
  [[ "${host}" != *:* ]] || return 1
  [[ ! "${host}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  [[ "${host}" =~ ^[A-Za-z0-9.-]+$ ]] || return 1
  return 0
}

looks_like_acme_email() {
  local email="${1:-}"
  [[ "${email}" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]
}

can_prompt_user() {
  [[ -r /dev/tty || -t 0 ]]
}

read_engine_public_edge_status() {
  local py_bin
  py_bin="$(resolve_python_bin)"
  [[ -n "${py_bin}" ]] || return 1

  INPUT_SETTINGS_PATH="$(engine_letsencrypt_settings_path)" "${py_bin}" - <<'PY'
import json
import os
from urllib.parse import urlsplit

path = (os.environ.get("INPUT_SETTINGS_PATH") or "").strip()
if not path or not os.path.isfile(path):
    print("0|0||")
    raise SystemExit(0)

try:
    with open(path, "r", encoding="utf-8") as handle:
        raw = json.load(handle)
except Exception:
    print("1|0||")
    raise SystemExit(0)

if not isinstance(raw, dict):
    print("1|0||")
    raise SystemExit(0)

def normalize_host(value: object) -> str:
    text = str(value or "").strip().lower()
    if not text:
        return ""
    if "://" not in text:
        text = f"https://{text}"
    try:
        parsed = urlsplit(text)
        return (parsed.hostname or "").strip().lower()
    except Exception:
        return ""

def valid_host(value: object) -> bool:
    host = normalize_host(value)
    if not host or "." not in host or host == "localhost" or ":" in host:
        return False
    parts = host.split(".")
    if len(parts) == 4 and all(part.isdigit() for part in parts):
        return False
    return True

def valid_email(value: object) -> bool:
    text = str(value or "").strip()
    if "@" not in text or "." not in text.rsplit("@", 1)[-1]:
        return False
    return " " not in text

fqdn = normalize_host(raw.get("fqdn"))
email = str(raw.get("acme_email") or "").strip()
configured = bool(raw.get("enabled", True)) and valid_host(fqdn) and valid_email(email)
print(f"1|{1 if configured else 0}|{fqdn}|{email}")
PY
}

reset_engine_public_edge_runtime() {
  run_privileged rm -f \
    "$(engine_letsencrypt_settings_path)" \
    "$(engine_letsencrypt_runtime_env_path)" \
    "$(engine_letsencrypt_acme_storage_path)" \
    "$(engine_traefik_static_config_path)" \
    "$(engine_traefik_dynamic_config_path)"
}

prompt_engine_public_edge_configuration() {
  local current_fqdn="${1:-}"
  local current_email="${2:-}"

  local suggested_fqdn="${BOREALIS_PUBLIC_FQDN:-}"
  if [[ -z "${suggested_fqdn}" && -n "${SERVER_URL:-}" ]]; then
    suggested_fqdn="$(parse_url_hostname "${SERVER_URL}")"
  fi
  if [[ -z "${suggested_fqdn}" && -n "${BOREALIS_SERVER_URL:-}" ]]; then
    suggested_fqdn="$(parse_url_hostname "${BOREALIS_SERVER_URL}")"
  fi
  if [[ -z "${suggested_fqdn}" && -n "${current_fqdn}" ]]; then
    suggested_fqdn="${current_fqdn}"
  fi
  if [[ -z "${suggested_fqdn}" ]]; then
    local resolved_fqdn
    resolved_fqdn="$(resolve_engine_public_fqdn)"
    if looks_like_public_fqdn "${resolved_fqdn}"; then
      suggested_fqdn="${resolved_fqdn}"
    fi
  fi
  if ! looks_like_public_fqdn "${suggested_fqdn}"; then
    suggested_fqdn=""
  fi

  local suggested_email="${BOREALIS_ACME_EMAIL:-${current_email}}"
  if ! looks_like_acme_email "${suggested_email}"; then
    suggested_email=""
  fi

  if ! can_prompt_user; then
    if looks_like_public_fqdn "${suggested_fqdn}" && looks_like_acme_email "${suggested_email}"; then
      printf '%s|%s\n' "${suggested_fqdn}" "${suggested_email}"
      return 0
    fi
    echo -e "${RED}Borealis needs a public FQDN and Let's Encrypt email, but no interactive terminal is available.${RESET}" >&2
    echo -e "${YELLOW}Set BOREALIS_PUBLIC_FQDN and BOREALIS_ACME_EMAIL, or rerun Borealis.sh interactively.${RESET}" >&2
    return 1
  fi

  local fqdn="${suggested_fqdn}"
  while true; do
    local prompt="Public Borealis FQDN (example: borealis.bunny-lab.io)"
    if [[ -n "${fqdn}" ]]; then
      prompt+=" [${fqdn}]"
    fi
    prompt+=": "
    local input
    input="$(prompt_input "${prompt}")"
    if [[ -z "${input}" ]]; then
      input="${fqdn}"
    fi
    input="$(parse_url_hostname "${input}")"
    if looks_like_public_fqdn "${input}"; then
      fqdn="${input}"
      break
    fi
    echo -e "${YELLOW}Enter a public FQDN with at least one dot, such as borealis.bunny-lab.io.${RESET}"
  done

  local email="${suggested_email}"
  while true; do
    local prompt="Let's Encrypt notification email"
    if [[ -n "${email}" ]]; then
      prompt+=" [${email}]"
    fi
    prompt+=": "
    local input
    input="$(prompt_input "${prompt}")"
    if [[ -z "${input}" ]]; then
      input="${email}"
    fi
    input="${input## }"
    input="${input%% }"
    if looks_like_acme_email "${input}"; then
      email="${input}"
      break
    fi
    echo -e "${YELLOW}Enter a valid email address for Let's Encrypt expiration notices.${RESET}"
  done

  printf '%s|%s\n' "${fqdn}" "${email}"
}

fetch_url_response_headers() {
  local url="${1:-}"
  [[ -n "${url}" ]] || return 0

  if command_exists curl; then
    curl -sS --max-time 10 -o /dev/null -D - "${url}" 2>/dev/null || true
    return 0
  fi

  if command_exists wget; then
    wget --server-response --spider --timeout=10 "${url}" 2>&1 || true
    return 0
  fi

  local py_bin=""
  py_bin="$(resolve_python_bin)"
  if [[ -n "${py_bin}" ]]; then
    INPUT_URL="${url}" "${py_bin}" - <<'PY' || true
import os
import urllib.request

url = (os.environ.get("INPUT_URL") or "").strip()
if not url:
    raise SystemExit(0)

request = urllib.request.Request(url, method="HEAD", headers={"User-Agent": "Borealis.sh"})
try:
    with urllib.request.urlopen(request, timeout=10) as response:
        for key, value in response.headers.items():
            print(f"{key}: {value}")
except Exception:
    raise SystemExit(0)
PY
  fi
}

warn_about_unsupported_cloudflare_proxying() {
  local fqdn="${1:-}"
  echo -e "${YELLOW}Notice: Borealis embedded Traefik does not support Cloudflare orange-cloud or other CDN-style TLS proxying in front of the engine.${RESET}"
  echo -e "${YELLOW}Use DNS-only records or a transparent reverse proxy that forwards 80/tcp and TCP-passthrough 443/tcp to the engine host.${RESET}"

  [[ -n "${fqdn}" ]] || return 0

  local headers
  headers="$(fetch_url_response_headers "http://${fqdn}/.well-known/acme-challenge/borealis-probe")"
  if [[ -z "${headers}" ]]; then
    headers="$(fetch_url_response_headers "https://${fqdn}/")"
  fi

  local lowered
  lowered="$(printf '%s' "${headers}" | tr '[:upper:]' '[:lower:]')"
  if printf '%s\n' "${lowered}" | grep -qE '(^server:\s*cloudflare|^cf-ray:|^cf-cache-status:)'; then
    echo -e "${RED}Detected Cloudflare proxy headers for ${fqdn}.${RESET}"
    echo -e "${RED}Disable Cloudflare proxying and switch the DNS record to DNS only before using Borealis embedded Traefik + Let's Encrypt.${RESET}"
  fi
}

resolve_engine_acme_email() {
  local fqdn="${1:-localhost}"
  if [[ -n "${BOREALIS_ACME_EMAIL:-}" ]]; then
    echo "${BOREALIS_ACME_EMAIL}"
    return 0
  fi
  echo "root@${fqdn}"
}

ensure_engine_public_edge_runtime() {
  local venv_py
  venv_py="$(engine_python_bin)"
  [[ -n "${venv_py}" ]] || return 1

  local status
  status="$(read_engine_public_edge_status)" || return 1

  local settings_exist configured current_fqdn current_email
  IFS='|' read -r settings_exist configured current_fqdn current_email <<<"${status}"

  local fqdn=""
  local email=""
  if [[ "${configured}" == "1" ]]; then
    fqdn="${current_fqdn}"
    email="${current_email}"
  else
    echo -e "${INFO} Configuring Borealis embedded Traefik + Let's Encrypt..."
    if [[ "${settings_exist}" == "1" ]]; then
      echo -e "${YELLOW}Existing Let's Encrypt settings are incomplete or invalid and will be regenerated.${RESET}"
    fi
    local prompt_result
    prompt_result="$(prompt_engine_public_edge_configuration "${current_fqdn}" "${current_email}")" || return 1
    IFS='|' read -r fqdn email <<<"${prompt_result}"
    reset_engine_public_edge_runtime || return 1
  fi

  warn_about_unsupported_cloudflare_proxying "${fqdn}"

  BOREALIS_PROJECT_ROOT="${SCRIPT_DIR}" "${venv_py}" -m Data.Engine.edge_runtime ensure-files \
    --settings-path "$(engine_letsencrypt_settings_path)" \
    --fqdn "${fqdn}" \
    --email "${email}" >/dev/null
}

resolve_traefik_binary() {
  if [[ -n "${BOREALIS_TRAEFIK_BIN:-}" && -x "${BOREALIS_TRAEFIK_BIN}" ]]; then
    echo "${BOREALIS_TRAEFIK_BIN}"
    return 0
  fi
  if command_exists traefik; then
    command -v traefik
    return 0
  fi
  if [[ -x "/usr/local/bin/traefik" ]]; then
    echo "/usr/local/bin/traefik"
    return 0
  fi
  if [[ -x "/usr/bin/traefik" ]]; then
    echo "/usr/bin/traefik"
    return 0
  fi
  echo ""
}

resolve_traefik_release_tag() {
  if [[ -n "${BOREALIS_TRAEFIK_VERSION:-}" ]]; then
    echo "${BOREALIS_TRAEFIK_VERSION}"
    return 0
  fi

  local py_bin=""
  py_bin="$(resolve_python_bin)"
  if [[ -n "${py_bin}" ]]; then
    local latest_tag=""
    set +e
    latest_tag="$("${py_bin}" - <<'PY'
import json
import urllib.request

request = urllib.request.Request(
    "https://api.github.com/repos/traefik/traefik/releases/latest",
    headers={"Accept": "application/vnd.github+json", "User-Agent": "Borealis.sh"},
)
with urllib.request.urlopen(request, timeout=30) as response:
    payload = json.load(response)
print((payload.get("tag_name") or "").strip())
PY
)"
    set -e
    if [[ -n "${latest_tag}" ]]; then
      echo "${latest_tag}"
      return 0
    fi
  fi

  echo "v3.3.0"
}

traefik_download_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "linux_amd64" ;;
    aarch64|arm64) echo "linux_arm64" ;;
    *)
      echo ""
      return 1
      ;;
  esac
}

download_traefik_binary() {
  local tag
  tag="$(resolve_traefik_release_tag)"
  [[ -n "${tag}" ]] || return 1

  local suffix
  suffix="$(traefik_download_arch)"
  [[ -n "${suffix}" ]] || return 1

  local archive="traefik_${tag}_${suffix}.tar.gz"
  local url="https://github.com/traefik/traefik/releases/download/${tag}/${archive}"
  local tmp_dir
  tmp_dir="$(mktemp -d)"
  local archive_path="${tmp_dir}/${archive}"

  download_file "${url}" "${archive_path}" || {
    rm -rf "${tmp_dir}"
    return 1
  }

  tar -xzf "${archive_path}" -C "${tmp_dir}" || {
    rm -rf "${tmp_dir}"
    return 1
  }

  [[ -x "${tmp_dir}/traefik" ]] || {
    rm -rf "${tmp_dir}"
    return 1
  }

  run_privileged install -m 0755 "${tmp_dir}/traefik" /usr/local/bin/traefik || {
    rm -rf "${tmp_dir}"
    return 1
  }
  rm -rf "${tmp_dir}"
}

install_traefik_best_effort() {
  if [[ -n "$(resolve_traefik_binary)" ]]; then
    return 0
  fi

  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      run_privileged apt update -qq || true
      run_privileged apt install -y traefik || true
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        run_privileged dnf install -y traefik || true
      else
        run_privileged yum install -y traefik || true
      fi
      ;;
    arch)
      run_privileged pacman -Sy --noconfirm traefik || true
      ;;
    *)
      ;;
  esac

  if [[ -n "$(resolve_traefik_binary)" ]]; then
    return 0
  fi

  download_traefik_binary
}

traefik_service_runner_path() {
  echo "${SCRIPT_DIR}/Engine/run-traefik-service.sh"
}

ensure_traefik_service_runner() {
  local traefik_bin
  traefik_bin="$(resolve_traefik_binary)"
  [[ -n "${traefik_bin}" && -x "${traefik_bin}" ]] || return 1

  local runner_path
  runner_path="$(traefik_service_runner_path)"
  mkdir -p "$(dirname "${runner_path}")"
  cat > "${runner_path}" <<EOF
#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

PROJECT_ROOT="${SCRIPT_DIR}"
EDGE_ENV_FILE="\${PROJECT_ROOT}/Engine/LetsEncrypt/runtime.env"
STATIC_CONFIG_PATH="\${PROJECT_ROOT}/Engine/Traefik/traefik.yml"
TRAEFIK_BIN="${traefik_bin}"

if [[ -f "\${EDGE_ENV_FILE}" ]]; then
  # shellcheck disable=SC1090
  . "\${EDGE_ENV_FILE}"
fi

exec "\${TRAEFIK_BIN}" --configFile="\${BOREALIS_TRAEFIK_STATIC_CONFIG_PATH:-\${STATIC_CONFIG_PATH}}"
EOF
  chmod +x "${runner_path}"
  echo "${runner_path}"
}

ensure_traefik_systemd_service() {
  local unit_name="borealis-traefik.service"
  local unit_path="/etc/systemd/system/${unit_name}"
  local tmp_unit
  tmp_unit="$(ensure_engine_log_dir)/${unit_name}"

  local runner_path
  runner_path="$(ensure_traefik_service_runner)"
  [[ -x "${runner_path}" ]] || return 1

  cat > "${tmp_unit}" <<EOF
[Unit]
Description=Borealis Traefik Edge Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${SCRIPT_DIR}/Engine
ExecStart=/usr/bin/env bash ${runner_path}
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
  write_engine_log "Systemd service '${unit_name}' installed/restarted."
}

print_traefik_service_status() {
  local unit_name="borealis-traefik.service"
  if ! command_exists systemctl; then
    return 0
  fi

  echo -e "${GREEN}Traefik edge service status (${unit_name}):${RESET}"
  run_privileged systemctl --no-pager --full status "${unit_name}" || true
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
    cp -a "$item" "${destination_root}/"
  done
  shopt -u dotglob nullglob

  prepare_engine_ansible_layout || return 1
  verify_engine_runtime_staging
}

purge_engine_runtime_for_deploy() {
  local engine_root="${SCRIPT_DIR}/Engine"
  mkdir -p "${engine_root}"

  # Preserve only explicit runtime artifacts; purge everything else.
  shopt -s dotglob nullglob
  local entry=""
  for entry in "${engine_root}"/*; do
    local base
    base="$(basename "${entry}")"
    case "${base}" in
      Auth_Tokens|Certificates|Logs|WireGuard|database.env|.postgres-password|engine_secret.txt|Ansible|collections|LetsEncrypt|Traefik)
        continue
        ;;
      *)
        rm -rf "${entry}" 2>/dev/null || true
        ;;
    esac
  done
  shopt -u dotglob nullglob

  mkdir -p "${engine_root}/Auth_Tokens" "${engine_root}/Certificates" "${engine_root}/Logs" "${engine_root}/WireGuard" "${engine_root}/LetsEncrypt" "${engine_root}/Traefik" "$(engine_ansible_root_dir)"
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
ENGINE_DB_ENV_FILE="${ENGINE_DB_ENV_FILE}"
EDGE_ENV_FILE="\${PROJECT_ROOT}/Engine/LetsEncrypt/runtime.env"

mkdir -p "\${ENGINE_LOG_DIR}"
cd "\${ENGINE_DIR}"

export BOREALIS_PROJECT_ROOT="\${PROJECT_ROOT}"
export BOREALIS_ENGINE_MODE="\${MODE}"
export ANSIBLE_COLLECTIONS_PATH="\${PROJECT_ROOT}/Engine/Ansible/collections"
export ANSIBLE_COLLECTIONS_PATHS="\${ANSIBLE_COLLECTIONS_PATH}"
export PATH="\${NODE_BIN_DIR}:\${PATH}"
if [[ -f "\${ENGINE_DB_ENV_FILE}" ]]; then
  # shellcheck disable=SC1090
  . "\${ENGINE_DB_ENV_FILE}"
fi
if [[ -f "\${EDGE_ENV_FILE}" ]]; then
  # shellcheck disable=SC1090
  . "\${EDGE_ENV_FILE}"
fi

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
  local pg_service
  pg_service="$(postgres_service_name)"

  local runner_path
  runner_path="$(ensure_engine_service_runner)"
  [[ -x "${runner_path}" ]] || {
    write_engine_log "Engine systemd unit generation failed: runner script missing."
    return 1
  }

  cat > "${tmp_unit}" <<EOF
[Unit]
Description=Borealis Engine Service
After=network-online.target ${pg_service}.service borealis-traefik.service
Wants=network-online.target ${pg_service}.service borealis-traefik.service

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
    ensure_traefik_systemd_service
    ensure_engine_systemd_service "${mode}"
    print_traefik_service_status
    print_engine_service_status
    return 0
  fi

  echo -e "${YELLOW}systemctl not available; launching Engine in the current shell instead.${RESET}"
  local traefik_runner=""
  local traefik_pid=""
  traefik_runner="$(ensure_traefik_service_runner 2>/dev/null || true)"
  if [[ -x "${traefik_runner}" ]]; then
    local logdir; logdir=$(ensure_engine_log_dir)
    /usr/bin/env bash "${traefik_runner}" >>"${logdir}/traefik.stdout.log" 2>>"${logdir}/traefik.stderr.log" &
    traefik_pid=$!
    trap 'if [[ -n "${traefik_pid}" ]]; then kill "${traefik_pid}" >/dev/null 2>&1 || true; wait "${traefik_pid}" 2>/dev/null || true; fi' RETURN
  fi
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

  # Copy the Engine source tree into the runtime venv staging area.
  shopt -s dotglob nullglob
  for item in "${engine_src}"/*; do
    cp -R "$item" "$data_dest/"
  done
  shopt -u dotglob nullglob

  # Runtime directories preserved outside the staged source tree.
  mkdir -p "${SCRIPT_DIR}/Engine/Auth_Tokens"
  prepare_engine_ansible_layout || return 1
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
  verify_engine_venv_writable || return 1
  local engine_src="${SCRIPT_DIR}/Data/Engine"
  local reqs=( "${engine_src}/engine-requirements.txt" "${engine_src}/requirements.txt" )
  local site_packages_dir
  site_packages_dir="$(engine_site_packages_dir)"
  local pth_paths=(
    "${SCRIPT_DIR}"
    "${SCRIPT_DIR}/Data/Agent"
    "${SCRIPT_DIR}/Data/Engine"
  )
  for r in "${reqs[@]}"; do
    if [[ -f "$r" && -n "$venv_py" ]]; then
      "$venv_py" -m pip install --disable-pip-version-check -q -r "$r"
      if [[ -n "${site_packages_dir}" ]]; then
        printf '%s\n' "${pth_paths[@]}" > "${site_packages_dir}/borealis-project-root.pth"
      fi
      return 0
    fi
  done
  if [[ -n "${site_packages_dir}" ]]; then
    printf '%s\n' "${pth_paths[@]}" > "${site_packages_dir}/borealis-project-root.pth"
  fi
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
    write_vite_log "Starting Vite dev server with loopback Engine proxying." "vite-dev"
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
  local prev_root="${BOREALIS_PROJECT_ROOT:-}"
  export BOREALIS_ENGINE_MODE="$mode"
  export BOREALIS_PROJECT_ROOT="$SCRIPT_DIR"
  export ANSIBLE_COLLECTIONS_PATH="${SCRIPT_DIR}/Engine/Ansible/collections"
  export ANSIBLE_COLLECTIONS_PATHS="${ANSIBLE_COLLECTIONS_PATH}"
  if [[ -f "${ENGINE_DB_ENV_FILE}" ]]; then
    # shellcheck disable=SC1090
    . "${ENGINE_DB_ENV_FILE}"
  fi
  if [[ -f "$(engine_letsencrypt_runtime_env_path)" ]]; then
    # shellcheck disable=SC1090
    . "$(engine_letsencrypt_runtime_env_path)"
  fi
  echo -e "\n${GREEN}Launching Borealis Engine...${RESET}"
  echo "===================================================================================="
  local start_label
  if [[ "$mode" == "developer" ]]; then
    start_label="(Dev) Engine Started on http://localhost:5173"
  else
    start_label="(Production) Borealis Edge Started on https://$(resolve_engine_public_fqdn)"
  fi
  echo "${HOURGLASS} ${start_label}"
  local logdir; logdir=$(ensure_engine_log_dir)
  local stdout_log="${logdir}/engine-launch.stdout.log"
  local stderr_log="${logdir}/engine-launch.stderr.log"
  "$py" -m Data.Engine.bootstrapper >>"$stdout_log" 2>>"$stderr_log" || true
  # restore env
  if [[ -n "$prev_mode" ]]; then export BOREALIS_ENGINE_MODE="$prev_mode"; else unset BOREALIS_ENGINE_MODE; fi
  if [[ -n "$prev_root" ]]; then export BOREALIS_PROJECT_ROOT="$prev_root"; else unset BOREALIS_PROJECT_ROOT; fi
  popd >/dev/null
}

# ---- Tests parity ----
if (( ENGINE_TESTS_FLAG )); then
  export BOREALIS_PROJECT_ROOT="${SCRIPT_DIR}"
  export PYTHONPATH="${SCRIPT_DIR}${PYTHONPATH:+:${PYTHONPATH}}"
  PYTHON_BIN="$(resolve_python_bin)"
  if [[ -z "${PYTHON_BIN}" ]]; then
    echo -e "${RED}Python interpreter not found. Install Python 3 to run Engine tests.${RESET}" >&2
    exit 1
  fi
  create_engine_venv_and_stage_data || exit 1
  install_engine_python_deps || exit 1
  PYTEST_BIN="${SCRIPT_DIR}/Engine/bin/pytest"
  if [[ ! -x "${PYTEST_BIN}" ]]; then
    echo -e "${RED}Engine pytest binary not found at ${PYTEST_BIN}.${RESET}" >&2
    exit 1
  fi
  cd "${SCRIPT_DIR}" || exit 1
  "${PYTEST_BIN}" 'Data/Engine/Unit_Tests'
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
    echo -e " 1) Build & Launch > Borealis Traefik Edge + Engine"
    echo -e " 2) [Skip Build] & Immediately Launch > Borealis Traefik Edge + Engine"
    echo -e " 3) Launch > [Hotload-Ready] Vite Dev Server @ http://localhost:5173"
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
  run_step "Database: Configure PostgreSQL" ensure_engine_postgresql_ready
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
    run_step "Restore Engine database environment" ensure_engine_database_env_file
    run_step "Render Borealis Let's Encrypt and Traefik runtime files" ensure_engine_public_edge_runtime
    run_step "Verify Engine Ansible Runtime" verify_engine_ansible_runtime
    if [[ "${ENGINE_USE_SYSTEMD_SUPERVISION}" -eq 1 ]]; then
      run_step "Configure Borealis Engine systemd service (${borealis_operation_mode})" configure_engine_supervision "$borealis_operation_mode"
    else
      run_step "Borealis Engine: Launch Flask Server" flask_engine_launch "$borealis_operation_mode"
    fi
    return 0
  fi

  run_step "Create Borealis Engine Virtual Python Environment & Stage Data" create_engine_venv_and_stage_data
  run_step "Restore Engine database environment" ensure_engine_database_env_file
  run_step "Install Engine Python Dependencies" install_engine_python_deps
  run_step "Render Borealis Let's Encrypt and Traefik runtime files" ensure_engine_public_edge_runtime
  run_step "Install Engine Ansible Collections" install_engine_ansible_collections
  run_step "Verify Engine Ansible Runtime" verify_engine_ansible_runtime
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
  if [[ "${REFRESH_AGENT_RUNTIME_FLAG}" -eq 1 ]]; then
    install_or_update_borealis_agent 1
    return $?
  fi
  install_or_update_borealis_agent 0
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
