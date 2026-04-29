#!/usr/bin/env bash
#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Borealis.sh
# Linux parity for Borealis.ps1.
# - Installs Linux dependencies for Engine and Agent paths
# - Mirrors Engine flow: venv + staging + Vite + Flask launch
# - Mirrors Agent flow: venv + Data/Agent staging + dependency install + settings + supervision
# - Supports parity flags: --server/--agent, --vite/--flask, --quick,
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
WARNING="[!]"
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
ENGINE_PROD_FLAG=0
ENGINE_DEV_FLAG=0
REFRESH_AGENT_RUNTIME_FLAG=0
NEW_ENGINE_FLAG=0
VERBOSE_FLAG=0
ENROLLMENT_CODE=""
SERVER_URL=""
BOOTSTRAP_NEW_ENGINE_DEFAULT="${BOREALIS_BOOTSTRAP_NEW_ENGINE:-}"
BOOTSTRAP_VERBOSE_DEFAULT="${BOREALIS_VERBOSE:-}"

CHOICE=""
ENGINE_MODE_CHOICE=""
ENGINE_USE_SYSTEMD_SUPERVISION=0
POSTGRES_VERSION="${BOREALIS_PG_VERSION:-17}"
ENGINE_DB_ENV_FILE="${SCRIPT_DIR}/Engine/database.env"
ENGINE_DB_PASSWORD_FILE="${SCRIPT_DIR}/Engine/.postgres-password"
ENGINE_PROFILE_KEY=""
ENGINE_PROFILE_NAME=""
ENGINE_PROFILE_VCPU=0
ENGINE_PROFILE_RAM_MIB=0
ENGINE_PROFILE_STORAGE_KIB=0
ENGINE_PROFILE_PREVIOUS_NAME=""
ENGINE_PROFILE_STORAGE_GUIDANCE=""
ENGINE_PROFILE_STORAGE_MIN_GIB=0
ENGINE_PROFILE_DB_POOL_SIZE=10
ENGINE_PROFILE_DB_MAX_OVERFLOW=20
ENGINE_PROFILE_DB_CONNECT_TIMEOUT=15
ENGINE_PROFILE_DB_IDLE_IN_TXN_TIMEOUT_MS=60000
ENGINE_PROFILE_PG_MAX_CONNECTIONS=100
ENGINE_PROFILE_PG_SHARED_BUFFERS_MB=2048
ENGINE_PROFILE_PG_EFFECTIVE_CACHE_SIZE_MB=8192
ENGINE_PROFILE_PG_WORK_MEM_MB=4
ENGINE_PROFILE_PG_MAINTENANCE_WORK_MEM_MB=256
ENGINE_PROFILE_PG_MAX_WORKER_PROCESSES=8
ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS=8
ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS_PER_GATHER=2
ENGINE_PROFILE_PG_AUTOVACUUM_MAX_WORKERS=3
ENGINE_PROFILE_PG_AUTOVACUUM_VACUUM_COST_LIMIT=1000
ENGINE_PROFILE_PG_AUTOVACUUM_NAPTIME="30s"
ENGINE_PROFILE_PG_AUTOVACUUM_VACUUM_SCALE_FACTOR="0.02"
ENGINE_PROFILE_PG_AUTOVACUUM_ANALYZE_SCALE_FACTOR="0.01"
ENGINE_PROFILE_PG_MAX_WAL_SIZE="4GB"
ENGINE_PROFILE_PG_MIN_WAL_SIZE="512MB"
ENGINE_PROFILE_PG_WAL_COMPRESSION="on"
ENGINE_PROFILE_PG_CHECKPOINT_TIMEOUT="15min"
ENGINE_PROFILE_PG_RANDOM_PAGE_COST="1.1"
ENGINE_PROFILE_PG_EFFECTIVE_IO_CONCURRENCY=32

while (( "$#" )); do
  case "$1" in
    -Server|--server) SERVER_FLAG=1 ;;
    -Agent|--agent|--Agent) AGENT_FLAG=1 ;;
    -Vite|--vite) VITE_FLAG=1 ;;
    -Flask|--flask) FLASK_FLAG=1 ;;
    -Quick|--quick) QUICK_FLAG=1 ;;
    -EngineProduction|--engine-production) ENGINE_PROD_FLAG=1 ;;
    -EngineDev|--engine-dev) ENGINE_DEV_FLAG=1 ;;
    -Verbose|--verbose) VERBOSE_FLAG=1 ;;
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
command_exists() {
  command -v "$1" >/dev/null 2>&1
}

env_flag_enabled() {
  local value="${1:-}"
  case "${value,,}" in
    1|true|yes|on) return 0 ;;
    *) return 1 ;;
  esac
}

allow_system_package_install() {
  env_flag_enabled "${BOREALIS_ALLOW_SYSTEM_PACKAGE_INSTALL:-0}"
}

if [[ "${VERBOSE_FLAG}" -eq 0 ]] && env_flag_enabled "${BOOTSTRAP_VERBOSE_DEFAULT}"; then
  VERBOSE_FLAG=1
fi

is_stdout_tty() {
  [[ -t 1 ]]
}

is_interactive_terminal() {
  [[ -t 0 && -t 1 ]]
}

terminal_supports_color() {
  is_stdout_tty || return 1
  [[ -z "${NO_COLOR:-}" ]] || return 1
  [[ "${TERM:-}" != "dumb" ]] || return 1
  return 0
}

terminal_supports_unicode() {
  local locale_hint="${LC_ALL:-${LC_CTYPE:-${LANG:-}}}"
  if [[ "${locale_hint,,}" == *"utf-8"* || "${locale_hint,,}" == *"utf8"* ]]; then
    return 0
  fi
  if command_exists locale; then
    local charmap
    charmap="$(locale charmap 2>/dev/null || true)"
    [[ "${charmap,,}" == "utf-8" || "${charmap,,}" == "utf8" ]]
    return $?
  fi
  return 1
}

configure_terminal_appearance() {
  if ! terminal_supports_color; then
    BOREALIS_BLUE=""
    DARK_GRAY=""
    GREEN=""
    YELLOW=""
    RED=""
    RESET=""
  fi

  if terminal_supports_unicode; then
    CHECKMARK=$'☑'
    HOURGLASS=$'⏳'
    CROSSMARK=$'☒'
    INFO=$'ℹ'
    WARNING=$'⚠'
  fi
}

configure_terminal_appearance

STEP_TOTAL=0
STEP_CURRENT=0
STEP_LINE_ACTIVE=0
CURRENT_INSTALL_LOG_FILE=""
CURRENT_STEP_CAPTURE_FILE=""
CURRENT_STEP_MESSAGE=""
CURRENT_STEP_CONTEXT=""
SUDO_SESSION_READY=0

step_output_break() {
  if [[ "${STEP_LINE_ACTIVE}" -eq 1 ]]; then
    printf "\n"
    STEP_LINE_ACTIVE=0
  fi
}

step_line_reset_prefix() {
  if is_stdout_tty; then
    printf '\r\033[2K'
  else
    printf '\r'
  fi
}

log_ui_message() {
  local level="$1"
  local message="$2"
  if [[ -n "${CURRENT_INSTALL_LOG_FILE}" ]]; then
    printf "[%s] [%s] %s\n" "$(date +%FT%T)" "${level}" "${message}" >> "${CURRENT_INSTALL_LOG_FILE}"
  fi
}

ui_plain() {
  step_output_break
  printf "%s\n" "$1"
}

ui_info() {
  step_output_break
  printf "%b%s%b %s\n" "${BOREALIS_BLUE}" "${INFO}" "${RESET}" "$1"
  log_ui_message "INFO" "$1"
}

ui_verbose() {
  if [[ "${VERBOSE_FLAG}" -eq 1 ]]; then
    ui_info "$1"
  fi
}

ui_success() {
  step_output_break
  printf "%b%s%b %s\n" "${GREEN}" "${CHECKMARK}" "${RESET}" "$1"
  log_ui_message "SUCCESS" "$1"
}

ui_warn() {
  step_output_break
  printf "%b%s%b %s\n" "${YELLOW}" "${WARNING}" "${RESET}" "$1"
  log_ui_message "WARN" "$1"
}

ui_error() {
  step_output_break
  printf "%b%s%b %s\n" "${RED}" "${CROSSMARK}" "${RESET}" "$1" >&2
  log_ui_message "ERROR" "$1"
}

set_output_context() {
  local context="${1:-}"
  CURRENT_STEP_CONTEXT="${context}"
  case "${context}" in
    engine) CURRENT_INSTALL_LOG_FILE="$(ensure_engine_log_dir)/install.log" ;;
    agent) CURRENT_INSTALL_LOG_FILE="$(ensure_agent_log_dir)/install.log" ;;
    *) CURRENT_INSTALL_LOG_FILE="" ;;
  esac
}

set_step_plan() {
  STEP_TOTAL="${1:-0}"
  STEP_CURRENT=0
}

format_step_index() {
  local current="${1:-0}"
  local total="${2:-0}"
  if (( total <= 0 )); then
    printf ""
    return 0
  fi
  local width=${#total}
  printf "[%0*d/%d] " "${width}" "${current}" "${total}"
}

record_command_heading() {
  local command_line="$1"
  local heading="[${CURRENT_STEP_MESSAGE:-command}] ${command_line}"
  if [[ -n "${CURRENT_INSTALL_LOG_FILE}" ]]; then
    printf "[%s] %s\n" "$(date +%FT%T)" "${heading}" >> "${CURRENT_INSTALL_LOG_FILE}"
  fi
  if [[ -n "${CURRENT_STEP_CAPTURE_FILE}" ]]; then
    printf "%s\n" "${heading}" >> "${CURRENT_STEP_CAPTURE_FILE}"
  fi
}

run_logged_command() {
  local command_line=""
  printf -v command_line '%q ' "$@"
  command_line="${command_line% }"
  record_command_heading "${command_line}"

  local capture_file="${CURRENT_STEP_CAPTURE_FILE:-}"
  local install_log="${CURRENT_INSTALL_LOG_FILE:-}"
  local output_file=""
  output_file="$(mktemp)"

  local had_errexit=0
  if [[ $- == *e* ]]; then
    had_errexit=1
    set +e
  fi

  local exit_code=0
  if [[ "${VERBOSE_FLAG}" -eq 1 ]]; then
    if [[ -n "${install_log}" || -n "${capture_file}" ]]; then
      local -a tee_targets=()
      [[ -n "${install_log}" ]] && tee_targets+=("${install_log}")
      [[ -n "${capture_file}" ]] && tee_targets+=("${capture_file}")
      "$@" 2>&1 | tee -a "${tee_targets[@]}"
      exit_code=${PIPESTATUS[0]}
    else
      "$@"
      exit_code=$?
    fi
  else
    "$@" >"${output_file}" 2>&1
    exit_code=$?
    if [[ -s "${output_file}" ]]; then
      [[ -n "${install_log}" ]] && cat "${output_file}" >> "${install_log}"
      [[ -n "${capture_file}" ]] && cat "${output_file}" >> "${capture_file}"
    fi
  fi

  (( had_errexit )) && set -e
  rm -f "${output_file}"
  return "${exit_code}"
}

append_file_tail_to_current_step_logs() {
  local label="$1"
  local file_path="$2"
  local line_count="${3:-40}"
  [[ -f "${file_path}" && -s "${file_path}" ]] || return 0

  local heading="${label}: ${file_path}"
  if [[ -n "${CURRENT_INSTALL_LOG_FILE:-}" ]]; then
    printf "[%s] %s\n" "$(date +%FT%T)" "${heading}" >> "${CURRENT_INSTALL_LOG_FILE}"
    tail -n "${line_count}" "${file_path}" >> "${CURRENT_INSTALL_LOG_FILE}"
  fi
  if [[ -n "${CURRENT_STEP_CAPTURE_FILE:-}" ]]; then
    printf "%s\n" "${heading}" >> "${CURRENT_STEP_CAPTURE_FILE}"
    tail -n "${line_count}" "${file_path}" >> "${CURRENT_STEP_CAPTURE_FILE}"
  fi
}

ensure_visible_sudo_session() {
  if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
    SUDO_SESSION_READY=1
    return 0
  fi
  if ! command_exists sudo; then
    ui_error "This action requires root privileges and sudo is not available."
    return 1
  fi
  if [[ "${SUDO_SESSION_READY}" -eq 1 ]] && sudo -n true >/dev/null 2>&1; then
    return 0
  fi

  step_output_break
  ui_info "Elevated access is required. You may be prompted for your sudo password."
  if [[ -r /dev/tty ]]; then
    sudo -v < /dev/tty
  else
    sudo -v
  fi
  SUDO_SESSION_READY=1
}

run_step() {
  local message="$1"
  shift

  local step_number=$((STEP_CURRENT + 1))
  local step_index=""
  step_index="$(format_step_index "${step_number}" "${STEP_TOTAL}")"
  local capture_file=""
  capture_file="$(mktemp)"
  CURRENT_STEP_CAPTURE_FILE="${capture_file}"
  CURRENT_STEP_MESSAGE="${message}"

  printf "%b%s%b %s%s... " "${BOREALIS_BLUE}" "${HOURGLASS}" "${RESET}" "${step_index}" "${message}"
  STEP_LINE_ACTIVE=1

  local exit_code=0
  if "$@"; then
    STEP_CURRENT="${step_number}"
    if [[ "${STEP_LINE_ACTIVE}" -eq 1 ]]; then
      step_line_reset_prefix
      printf "%b%s%b %s%s\n" "${GREEN}" "${CHECKMARK}" "${RESET}" "${step_index}" "${message}"
    else
      printf "%b%s%b %s%s\n" "${GREEN}" "${CHECKMARK}" "${RESET}" "${step_index}" "${message}"
    fi
  else
    exit_code=$?
    STEP_CURRENT="${step_number}"
    if [[ "${STEP_LINE_ACTIVE}" -eq 1 ]]; then
      step_line_reset_prefix
      printf "%b%s%b %s%s\n" "${RED}" "${CROSSMARK}" "${RESET}" "${step_index}" "${message}" >&2
    else
      printf "%b%s%b %s%s\n" "${RED}" "${CROSSMARK}" "${RESET}" "${step_index}" "${message}" >&2
    fi
    STEP_LINE_ACTIVE=0
    if [[ -s "${capture_file}" ]]; then
      ui_warn "Recent output for failed step:"
      while IFS= read -r line; do
        printf "  %s\n" "${line}" >&2
      done < <(tail -n 20 "${capture_file}")
    fi
    if [[ -n "${CURRENT_INSTALL_LOG_FILE}" ]]; then
      ui_info "Full log: ${CURRENT_INSTALL_LOG_FILE}"
    fi
    CURRENT_STEP_CAPTURE_FILE=""
    CURRENT_STEP_MESSAGE=""
    rm -f "${capture_file}"
    return "${exit_code}"
  fi

  STEP_LINE_ACTIVE=0
  CURRENT_STEP_CAPTURE_FILE=""
  CURRENT_STEP_MESSAGE=""
  rm -f "${capture_file}"
}

prompt_input() {
  local prompt="$1"
  local value=""
  step_output_break
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

clamp_int() {
  local value="${1:-0}"
  local minimum="${2:-0}"
  local maximum="${3:-0}"
  (( value < minimum )) && value="${minimum}"
  if (( maximum > 0 && value > maximum )); then
    value="${maximum}"
  fi
  printf '%s' "${value}"
}

format_mib_to_gib() {
  local mib="${1:-0}"
  awk -v mib="${mib}" 'BEGIN { printf "%.1f", mib / 1024 }'
}

format_kib_to_gib() {
  local kib="${1:-0}"
  awk -v kib="${kib}" 'BEGIN { printf "%.1f", kib / 1048576 }'
}

load_existing_engine_profile_name() {
  if [[ ! -f "${ENGINE_DB_ENV_FILE}" ]]; then
    echo ""
    return 0
  fi
  bash -lc "source '${ENGINE_DB_ENV_FILE}' >/dev/null 2>&1 || exit 0; printf '%s' \"\${BOREALIS_ENGINE_PROFILE_NAME:-}\"" 2>/dev/null || true
}

detect_engine_host_specs() {
  local vcpu_count=0
  local ram_kib=0
  local storage_kib=0

  if command_exists nproc; then
    vcpu_count="$(nproc 2>/dev/null || echo 0)"
  elif command_exists getconf; then
    vcpu_count="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 0)"
  fi
  [[ "${vcpu_count}" =~ ^[0-9]+$ ]] || vcpu_count=0
  (( vcpu_count > 0 )) || vcpu_count=1

  if [[ -r /proc/meminfo ]]; then
    ram_kib="$(awk '/MemTotal:/ { print $2; exit }' /proc/meminfo 2>/dev/null || echo 0)"
  fi
  [[ "${ram_kib}" =~ ^[0-9]+$ ]] || ram_kib=0
  (( ram_kib > 0 )) || ram_kib=$((8 * 1024 * 1024))

  storage_kib="$(df -Pk "${SCRIPT_DIR}" 2>/dev/null | awk 'NR==2 { print $2; exit }' || echo 0)"
  [[ "${storage_kib}" =~ ^[0-9]+$ ]] || storage_kib=0

  ENGINE_PROFILE_VCPU="${vcpu_count}"
  ENGINE_PROFILE_RAM_MIB=$(( (ram_kib + 1023) / 1024 ))
  ENGINE_PROFILE_STORAGE_KIB="${storage_kib}"
}

resolve_engine_profile_rank() {
  local vcpu="${1:-1}"
  local ram_mib="${2:-8192}"
  local cpu_rank=0
  local ram_rank=0

  if (( vcpu >= 24 )); then
    cpu_rank=3
  elif (( vcpu >= 16 )); then
    cpu_rank=2
  elif (( vcpu >= 8 )); then
    cpu_rank=1
  fi

  if (( ram_mib >= 65536 )); then
    ram_rank=3
  elif (( ram_mib >= 32768 )); then
    ram_rank=2
  elif (( ram_mib >= 16384 )); then
    ram_rank=1
  fi

  if (( cpu_rank < ram_rank )); then
    printf '%s' "${cpu_rank}"
  else
    printf '%s' "${ram_rank}"
  fi
}

configure_engine_profile_from_specs() {
  local rank="${1:-0}"
  local quarter_ram_mb=$(( ENGINE_PROFILE_RAM_MIB / 4 ))
  local effective_cache_mb=$(( (ENGINE_PROFILE_RAM_MIB * 5) / 8 ))

  case "${rank}" in
    0)
      ENGINE_PROFILE_KEY="homelab"
      ENGINE_PROFILE_NAME="Homelab"
      ENGINE_PROFILE_STORAGE_GUIDANCE="80-150 GiB"
      ENGINE_PROFILE_STORAGE_MIN_GIB=80
      ENGINE_PROFILE_DB_POOL_SIZE=10
      ENGINE_PROFILE_DB_MAX_OVERFLOW=10
      ENGINE_PROFILE_PG_MAX_CONNECTIONS=80
      ENGINE_PROFILE_PG_SHARED_BUFFERS_MB="$(clamp_int "${quarter_ram_mb}" 1024 4096)"
      ENGINE_PROFILE_PG_EFFECTIVE_CACHE_SIZE_MB="$(clamp_int "${effective_cache_mb}" 4096 12288)"
      ENGINE_PROFILE_PG_WORK_MEM_MB=4
      ENGINE_PROFILE_PG_MAINTENANCE_WORK_MEM_MB=256
      ENGINE_PROFILE_PG_MAX_WORKER_PROCESSES=8
      ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS=8
      ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS_PER_GATHER=2
      ENGINE_PROFILE_PG_AUTOVACUUM_MAX_WORKERS=3
      ENGINE_PROFILE_PG_AUTOVACUUM_VACUUM_COST_LIMIT=1000
      ENGINE_PROFILE_PG_AUTOVACUUM_NAPTIME="30s"
      ENGINE_PROFILE_PG_MAX_WAL_SIZE="4GB"
      ENGINE_PROFILE_PG_MIN_WAL_SIZE="512MB"
      ENGINE_PROFILE_PG_EFFECTIVE_IO_CONCURRENCY=16
      ;;
    1)
      ENGINE_PROFILE_KEY="small_business"
      ENGINE_PROFILE_NAME="Small Business"
      ENGINE_PROFILE_STORAGE_GUIDANCE="150-250 GiB"
      ENGINE_PROFILE_STORAGE_MIN_GIB=150
      ENGINE_PROFILE_DB_POOL_SIZE=12
      ENGINE_PROFILE_DB_MAX_OVERFLOW=16
      ENGINE_PROFILE_PG_MAX_CONNECTIONS=120
      ENGINE_PROFILE_PG_SHARED_BUFFERS_MB="$(clamp_int "${quarter_ram_mb}" 4096 8192)"
      ENGINE_PROFILE_PG_EFFECTIVE_CACHE_SIZE_MB="$(clamp_int "${effective_cache_mb}" 8192 16384)"
      ENGINE_PROFILE_PG_WORK_MEM_MB=8
      ENGINE_PROFILE_PG_MAINTENANCE_WORK_MEM_MB=512
      ENGINE_PROFILE_PG_MAX_WORKER_PROCESSES=8
      ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS=8
      ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS_PER_GATHER=2
      ENGINE_PROFILE_PG_AUTOVACUUM_MAX_WORKERS=4
      ENGINE_PROFILE_PG_AUTOVACUUM_VACUUM_COST_LIMIT=1500
      ENGINE_PROFILE_PG_AUTOVACUUM_NAPTIME="20s"
      ENGINE_PROFILE_PG_MAX_WAL_SIZE="6GB"
      ENGINE_PROFILE_PG_MIN_WAL_SIZE="1GB"
      ENGINE_PROFILE_PG_EFFECTIVE_IO_CONCURRENCY=32
      ;;
    2)
      ENGINE_PROFILE_KEY="msp_production"
      ENGINE_PROFILE_NAME="MSP / Production"
      ENGINE_PROFILE_STORAGE_GUIDANCE="500 GiB"
      ENGINE_PROFILE_STORAGE_MIN_GIB=500
      ENGINE_PROFILE_DB_POOL_SIZE=20
      ENGINE_PROFILE_DB_MAX_OVERFLOW=20
      ENGINE_PROFILE_PG_MAX_CONNECTIONS=150
      ENGINE_PROFILE_PG_SHARED_BUFFERS_MB="$(clamp_int "${quarter_ram_mb}" 8192 16384)"
      ENGINE_PROFILE_PG_EFFECTIVE_CACHE_SIZE_MB="$(clamp_int "${effective_cache_mb}" 20480 32768)"
      ENGINE_PROFILE_PG_WORK_MEM_MB=8
      ENGINE_PROFILE_PG_MAINTENANCE_WORK_MEM_MB=512
      ENGINE_PROFILE_PG_MAX_WORKER_PROCESSES=12
      ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS=12
      ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS_PER_GATHER=4
      ENGINE_PROFILE_PG_AUTOVACUUM_MAX_WORKERS=5
      ENGINE_PROFILE_PG_AUTOVACUUM_VACUUM_COST_LIMIT=2000
      ENGINE_PROFILE_PG_AUTOVACUUM_NAPTIME="15s"
      ENGINE_PROFILE_PG_MAX_WAL_SIZE="8GB"
      ENGINE_PROFILE_PG_MIN_WAL_SIZE="1GB"
      ENGINE_PROFILE_PG_EFFECTIVE_IO_CONCURRENCY=64
      ;;
    *)
      ENGINE_PROFILE_KEY="enterprise"
      ENGINE_PROFILE_NAME="Enterprise"
      ENGINE_PROFILE_STORAGE_GUIDANCE="500 GiB-1 TiB"
      ENGINE_PROFILE_STORAGE_MIN_GIB=500
      ENGINE_PROFILE_DB_POOL_SIZE=24
      ENGINE_PROFILE_DB_MAX_OVERFLOW=24
      ENGINE_PROFILE_PG_MAX_CONNECTIONS=180
      ENGINE_PROFILE_PG_SHARED_BUFFERS_MB="$(clamp_int "${quarter_ram_mb}" 12288 24576)"
      ENGINE_PROFILE_PG_EFFECTIVE_CACHE_SIZE_MB="$(clamp_int "${effective_cache_mb}" 32768 65536)"
      ENGINE_PROFILE_PG_WORK_MEM_MB=16
      ENGINE_PROFILE_PG_MAINTENANCE_WORK_MEM_MB=1024
      ENGINE_PROFILE_PG_MAX_WORKER_PROCESSES=16
      ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS=16
      ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS_PER_GATHER=4
      ENGINE_PROFILE_PG_AUTOVACUUM_MAX_WORKERS=6
      ENGINE_PROFILE_PG_AUTOVACUUM_VACUUM_COST_LIMIT=2500
      ENGINE_PROFILE_PG_AUTOVACUUM_NAPTIME="15s"
      ENGINE_PROFILE_PG_MAX_WAL_SIZE="12GB"
      ENGINE_PROFILE_PG_MIN_WAL_SIZE="2GB"
      ENGINE_PROFILE_PG_EFFECTIVE_IO_CONCURRENCY=64
      ;;
  esac
}

auto_configure_engine_profile() {
  detect_engine_host_specs
  ENGINE_PROFILE_PREVIOUS_NAME="$(load_existing_engine_profile_name)"
  configure_engine_profile_from_specs "$(resolve_engine_profile_rank "${ENGINE_PROFILE_VCPU}" "${ENGINE_PROFILE_RAM_MIB}")"
  ensure_engine_database_env_file || return 1

  local detected_ram_gib detected_storage_gib
  detected_ram_gib="$(format_mib_to_gib "${ENGINE_PROFILE_RAM_MIB}")"
  detected_storage_gib="$(format_kib_to_gib "${ENGINE_PROFILE_STORAGE_KIB}")"

  ui_info "Detected System Specs: ${ENGINE_PROFILE_VCPU} vCPU, ${detected_ram_gib} GiB RAM, ${detected_storage_gib} GiB storage"
  if [[ -n "${ENGINE_PROFILE_PREVIOUS_NAME}" ]]; then
    ui_info "Previous Engine Profile: ${ENGINE_PROFILE_PREVIOUS_NAME}"
    if [[ "${ENGINE_PROFILE_PREVIOUS_NAME}" == "${ENGINE_PROFILE_NAME}" ]]; then
      ui_info "Engine Profile Status: unchanged on re-deployment"
    else
      ui_info "Engine Profile Status: updated on re-deployment to ${ENGINE_PROFILE_NAME}"
    fi
  fi
  ui_info "Engine Profile (Auto-Configured from System Specs): ${ENGINE_PROFILE_NAME}"
  ui_verbose "Auto-configured DB pool: ${ENGINE_PROFILE_DB_POOL_SIZE} base / ${ENGINE_PROFILE_DB_MAX_OVERFLOW} overflow"
  ui_verbose "Auto-configured PostgreSQL shared_buffers: ${ENGINE_PROFILE_PG_SHARED_BUFFERS_MB}MB"

  if (( ENGINE_PROFILE_STORAGE_KIB > 0 )); then
    local detected_storage_gib_int=$(( (ENGINE_PROFILE_STORAGE_KIB + 1048575) / 1048576 ))
    if (( detected_storage_gib_int < ENGINE_PROFILE_STORAGE_MIN_GIB )); then
      ui_warn "Storage guidance for ${ENGINE_PROFILE_NAME}: recommended ${ENGINE_PROFILE_STORAGE_GUIDANCE}. Detected ${detected_storage_gib} GiB. Storage does not change the selected Engine profile."
    else
      ui_info "Storage guidance for ${ENGINE_PROFILE_NAME}: ${ENGINE_PROFILE_STORAGE_GUIDANCE} (detected ${detected_storage_gib} GiB). Storage is advisory only and does not change the selected Engine profile."
    fi
  fi

  write_engine_log "Auto-configured engine profile '${ENGINE_PROFILE_NAME}' from ${ENGINE_PROFILE_VCPU} vCPU / ${detected_ram_gib} GiB RAM." "engine-supervision.log"
}

apply_engine_postgresql_profile() {
  # shellcheck disable=SC1090
  . "${ENGINE_DB_ENV_FILE}"

  run_as_postgres_quiet "psql postgres -v ON_ERROR_STOP=1 <<EOF
ALTER SYSTEM SET max_connections = '${BOREALIS_PG_MAX_CONNECTIONS}';
ALTER SYSTEM SET shared_buffers = '${BOREALIS_PG_SHARED_BUFFERS_MB}MB';
ALTER SYSTEM SET effective_cache_size = '${BOREALIS_PG_EFFECTIVE_CACHE_SIZE_MB}MB';
ALTER SYSTEM SET work_mem = '${BOREALIS_PG_WORK_MEM_MB}MB';
ALTER SYSTEM SET maintenance_work_mem = '${BOREALIS_PG_MAINTENANCE_WORK_MEM_MB}MB';
ALTER SYSTEM SET max_worker_processes = '${BOREALIS_PG_MAX_WORKER_PROCESSES}';
ALTER SYSTEM SET max_parallel_workers = '${BOREALIS_PG_MAX_PARALLEL_WORKERS}';
ALTER SYSTEM SET max_parallel_workers_per_gather = '${BOREALIS_PG_MAX_PARALLEL_WORKERS_PER_GATHER}';
ALTER SYSTEM SET autovacuum_max_workers = '${BOREALIS_PG_AUTOVACUUM_MAX_WORKERS}';
ALTER SYSTEM SET autovacuum_vacuum_cost_limit = '${BOREALIS_PG_AUTOVACUUM_VACUUM_COST_LIMIT}';
ALTER SYSTEM SET autovacuum_naptime = '${BOREALIS_PG_AUTOVACUUM_NAPTIME}';
ALTER SYSTEM SET autovacuum_vacuum_scale_factor = '${BOREALIS_PG_AUTOVACUUM_VACUUM_SCALE_FACTOR}';
ALTER SYSTEM SET autovacuum_analyze_scale_factor = '${BOREALIS_PG_AUTOVACUUM_ANALYZE_SCALE_FACTOR}';
ALTER SYSTEM SET max_wal_size = '${BOREALIS_PG_MAX_WAL_SIZE}';
ALTER SYSTEM SET min_wal_size = '${BOREALIS_PG_MIN_WAL_SIZE}';
ALTER SYSTEM SET wal_compression = '${BOREALIS_PG_WAL_COMPRESSION}';
ALTER SYSTEM SET checkpoint_timeout = '${BOREALIS_PG_CHECKPOINT_TIMEOUT}';
ALTER SYSTEM SET checkpoint_completion_target = '0.9';
ALTER SYSTEM SET random_page_cost = '${BOREALIS_PG_RANDOM_PAGE_COST}';
ALTER SYSTEM SET effective_io_concurrency = '${BOREALIS_PG_EFFECTIVE_IO_CONCURRENCY}';
ALTER SYSTEM SET idle_in_transaction_session_timeout = '${BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS}';
SELECT pg_reload_conf();
EOF
"
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
  ui_error "This action requires root privileges and sudo is not available."
  return 1
}

run_privileged_quiet() {
  ensure_visible_sudo_session || return 1
  run_logged_command run_privileged "$@"
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
  ui_error "PostgreSQL setup requires sudo or root access."
  return 1
}

run_as_postgres_quiet() {
  ensure_visible_sudo_session || return 1
  run_logged_command run_as_postgres "$1"
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

  ui_error "No supported downloader found. Install Python 3, curl, or wget."
  return 1
}

ensure_project_layout() {
  if [[ -d "${SCRIPT_DIR}/Data/Agent" && -d "${SCRIPT_DIR}/Data/Engine" ]]; then
    return 0
  fi

  ui_error "Missing repository content under ${SCRIPT_DIR}."
  ui_warn "Run bootstrap.sh first so Borealis is installed to /opt/Borealis, then re-run Borealis.sh."
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
      ui_error "Engine runtime staging mismatch: missing ${runtime_marker}."
      return 1
    fi
    if ! cmp -s "${source_marker}" "${runtime_marker}"; then
      ui_error "Engine runtime staging mismatch for security/session_secret.py."
      return 1
    fi
  fi

  if [[ -f "${source_config}" ]]; then
    if [[ ! -f "${runtime_config}" ]]; then
      ui_error "Engine runtime staging mismatch: missing ${runtime_config}."
      return 1
    fi
    if ! cmp -s "${source_config}" "${runtime_config}"; then
      ui_error "Engine runtime staging mismatch for config.py."
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
ui_verbose "Borealis source root: ${SCRIPT_DIR}"

# ---- Agent configuration ----
configure_agent_settings() {
  local preserved_server_url="${1:-}"
  local noninteractive_mode="${2:-0}"
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
    ui_success "Enrollment code saved to agent_settings.json."
  else
    ui_warn "Enrollment code cleared in agent_settings.json."
  fi
}

# ---- Dependency Installation (Linux) ----
install_shared_dependencies() {
  detect_distro
  if command_exists python3 && command_exists pip3; then
    return 0
  fi

  if ! allow_system_package_install; then
    ui_error "Missing required system dependencies (python3/pip3). Run bootstrap.sh or install them manually before rerunning Borealis.sh."
    return 1
  fi

  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      run_privileged_quiet apt update -qq
      run_privileged_quiet apt install -y python3 python3-venv python3-pip ca-certificates
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        run_privileged_quiet dnf install -y python3 python3-pip ca-certificates
      else
        run_privileged_quiet yum install -y python3 python3-pip ca-certificates
      fi
      ;;
    arch)
      run_privileged_quiet pacman -Sy --noconfirm python python-pip python-virtualenv ca-certificates
      ;;
    *)
      ui_warn "Unsupported distro '${DISTRO_ID}'. Install python3, python3-venv, python3-pip manually."
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
  if command_exists psql; then
    return 0
  fi

  if ! allow_system_package_install; then
    ui_error "PostgreSQL is not installed. Run bootstrap.sh or install PostgreSQL manually before rerunning Borealis.sh."
    return 1
  fi

  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      run_privileged_quiet apt update -qq
      run_privileged_quiet apt install -y ca-certificates curl postgresql-common
      run_privileged_quiet install -d -m 0755 /usr/share/postgresql-common/pgdg
      run_privileged_quiet curl -fsSL -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc \
        https://www.postgresql.org/media/keys/ACCC4CF8.asc
      local codename=""
      if [[ -f /etc/os-release ]]; then
        # shellcheck disable=SC1091
        . /etc/os-release
        codename="${VERSION_CODENAME:-}"
      fi
      [[ -n "${codename}" ]] || { ui_error "Unable to determine Debian/Ubuntu codename for PGDG setup."; return 1; }
      printf 'deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt %s-pgdg main\n' "${codename}" | \
        run_privileged_quiet tee /etc/apt/sources.list.d/pgdg.list >/dev/null
      run_privileged_quiet apt update -qq
      if ! run_privileged_quiet apt install -y "postgresql-${POSTGRES_VERSION}" "postgresql-client-${POSTGRES_VERSION}"; then
        ui_warn "PGDG versioned packages were unavailable; falling back to distro PostgreSQL packages."
        run_privileged_quiet apt install -y postgresql postgresql-client
      fi
      ;;
    rhel|centos|fedora|rocky|almalinux)
      local releasever=""
      if [[ -f /etc/os-release ]]; then
        # shellcheck disable=SC1091
        . /etc/os-release
        releasever="${VERSION_ID%%.*}"
      fi
      [[ -n "${releasever}" ]] || { ui_error "Unable to determine EL release for PGDG setup."; return 1; }
      local arch
      arch="$(uname -m)"
      run_privileged_quiet rpm -Uvh --force "https://download.postgresql.org/pub/repos/yum/reporpms/EL-${releasever}-${arch}/pgdg-redhat-repo-latest.noarch.rpm"
      if command_exists dnf; then
        run_privileged_quiet dnf -qy module disable postgresql || true
        run_privileged_quiet dnf install -y "postgresql${POSTGRES_VERSION}-server" "postgresql${POSTGRES_VERSION}" || \
          run_privileged_quiet dnf install -y "postgresql${POSTGRES_VERSION//./}-server" "postgresql${POSTGRES_VERSION//./}"
      else
        run_privileged_quiet yum -y module disable postgresql || true
        run_privileged_quiet yum install -y "postgresql${POSTGRES_VERSION}-server" "postgresql${POSTGRES_VERSION}" || \
          run_privileged_quiet yum install -y "postgresql${POSTGRES_VERSION//./}-server" "postgresql${POSTGRES_VERSION//./}"
      fi
      ;;
    *)
      ui_warn "Unsupported distro '${DISTRO_ID}' for automated PostgreSQL install."
      return 1
      ;;
  esac
}

ensure_engine_database_env_file() {
  mkdir -p "$(dirname "${ENGINE_DB_ENV_FILE}")"
  local password=""
  local db_sslmode="${BOREALIS_DB_SSLMODE:-prefer}"
  local db_pool_size="${BOREALIS_DB_POOL_SIZE:-${ENGINE_PROFILE_DB_POOL_SIZE}}"
  local db_max_overflow="${BOREALIS_DB_MAX_OVERFLOW:-${ENGINE_PROFILE_DB_MAX_OVERFLOW}}"
  local db_connect_timeout="${BOREALIS_DB_CONNECT_TIMEOUT:-${ENGINE_PROFILE_DB_CONNECT_TIMEOUT}}"
  local db_idle_in_txn_timeout_ms="${BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS:-${ENGINE_PROFILE_DB_IDLE_IN_TXN_TIMEOUT_MS}}"
  local pg_max_connections="${BOREALIS_PG_MAX_CONNECTIONS:-${ENGINE_PROFILE_PG_MAX_CONNECTIONS}}"
  local pg_shared_buffers_mb="${BOREALIS_PG_SHARED_BUFFERS_MB:-${ENGINE_PROFILE_PG_SHARED_BUFFERS_MB}}"
  local pg_effective_cache_size_mb="${BOREALIS_PG_EFFECTIVE_CACHE_SIZE_MB:-${ENGINE_PROFILE_PG_EFFECTIVE_CACHE_SIZE_MB}}"
  local pg_work_mem_mb="${BOREALIS_PG_WORK_MEM_MB:-${ENGINE_PROFILE_PG_WORK_MEM_MB}}"
  local pg_maintenance_work_mem_mb="${BOREALIS_PG_MAINTENANCE_WORK_MEM_MB:-${ENGINE_PROFILE_PG_MAINTENANCE_WORK_MEM_MB}}"
  local pg_max_worker_processes="${BOREALIS_PG_MAX_WORKER_PROCESSES:-${ENGINE_PROFILE_PG_MAX_WORKER_PROCESSES}}"
  local pg_max_parallel_workers="${BOREALIS_PG_MAX_PARALLEL_WORKERS:-${ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS}}"
  local pg_max_parallel_workers_per_gather="${BOREALIS_PG_MAX_PARALLEL_WORKERS_PER_GATHER:-${ENGINE_PROFILE_PG_MAX_PARALLEL_WORKERS_PER_GATHER}}"
  local pg_autovacuum_max_workers="${BOREALIS_PG_AUTOVACUUM_MAX_WORKERS:-${ENGINE_PROFILE_PG_AUTOVACUUM_MAX_WORKERS}}"
  local pg_autovacuum_vacuum_cost_limit="${BOREALIS_PG_AUTOVACUUM_VACUUM_COST_LIMIT:-${ENGINE_PROFILE_PG_AUTOVACUUM_VACUUM_COST_LIMIT}}"
  local pg_autovacuum_naptime="${BOREALIS_PG_AUTOVACUUM_NAPTIME:-${ENGINE_PROFILE_PG_AUTOVACUUM_NAPTIME}}"
  local pg_autovacuum_vacuum_scale_factor="${BOREALIS_PG_AUTOVACUUM_VACUUM_SCALE_FACTOR:-${ENGINE_PROFILE_PG_AUTOVACUUM_VACUUM_SCALE_FACTOR}}"
  local pg_autovacuum_analyze_scale_factor="${BOREALIS_PG_AUTOVACUUM_ANALYZE_SCALE_FACTOR:-${ENGINE_PROFILE_PG_AUTOVACUUM_ANALYZE_SCALE_FACTOR}}"
  local pg_max_wal_size="${BOREALIS_PG_MAX_WAL_SIZE:-${ENGINE_PROFILE_PG_MAX_WAL_SIZE}}"
  local pg_min_wal_size="${BOREALIS_PG_MIN_WAL_SIZE:-${ENGINE_PROFILE_PG_MIN_WAL_SIZE}}"
  local pg_wal_compression="${BOREALIS_PG_WAL_COMPRESSION:-${ENGINE_PROFILE_PG_WAL_COMPRESSION}}"
  local pg_checkpoint_timeout="${BOREALIS_PG_CHECKPOINT_TIMEOUT:-${ENGINE_PROFILE_PG_CHECKPOINT_TIMEOUT}}"
  local pg_random_page_cost="${BOREALIS_PG_RANDOM_PAGE_COST:-${ENGINE_PROFILE_PG_RANDOM_PAGE_COST}}"
  local pg_effective_io_concurrency="${BOREALIS_PG_EFFECTIVE_IO_CONCURRENCY:-${ENGINE_PROFILE_PG_EFFECTIVE_IO_CONCURRENCY}}"
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
export BOREALIS_DB_SSLMODE="${db_sslmode}"
export BOREALIS_DB_POOL_SIZE="${db_pool_size}"
export BOREALIS_DB_MAX_OVERFLOW="${db_max_overflow}"
export BOREALIS_DB_CONNECT_TIMEOUT="${db_connect_timeout}"
export BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS="${db_idle_in_txn_timeout_ms}"
export BOREALIS_ENGINE_PROFILE_KEY="${ENGINE_PROFILE_KEY:-manual}"
export BOREALIS_ENGINE_PROFILE_NAME="${ENGINE_PROFILE_NAME:-Manual Override}"
export BOREALIS_ENGINE_PROFILE_VCPU="${ENGINE_PROFILE_VCPU:-0}"
export BOREALIS_ENGINE_PROFILE_RAM_MIB="${ENGINE_PROFILE_RAM_MIB:-0}"
export BOREALIS_ENGINE_PROFILE_STORAGE_KIB="${ENGINE_PROFILE_STORAGE_KIB:-0}"
export BOREALIS_PG_MAX_CONNECTIONS="${pg_max_connections}"
export BOREALIS_PG_SHARED_BUFFERS_MB="${pg_shared_buffers_mb}"
export BOREALIS_PG_EFFECTIVE_CACHE_SIZE_MB="${pg_effective_cache_size_mb}"
export BOREALIS_PG_WORK_MEM_MB="${pg_work_mem_mb}"
export BOREALIS_PG_MAINTENANCE_WORK_MEM_MB="${pg_maintenance_work_mem_mb}"
export BOREALIS_PG_MAX_WORKER_PROCESSES="${pg_max_worker_processes}"
export BOREALIS_PG_MAX_PARALLEL_WORKERS="${pg_max_parallel_workers}"
export BOREALIS_PG_MAX_PARALLEL_WORKERS_PER_GATHER="${pg_max_parallel_workers_per_gather}"
export BOREALIS_PG_AUTOVACUUM_MAX_WORKERS="${pg_autovacuum_max_workers}"
export BOREALIS_PG_AUTOVACUUM_VACUUM_COST_LIMIT="${pg_autovacuum_vacuum_cost_limit}"
export BOREALIS_PG_AUTOVACUUM_NAPTIME="${pg_autovacuum_naptime}"
export BOREALIS_PG_AUTOVACUUM_VACUUM_SCALE_FACTOR="${pg_autovacuum_vacuum_scale_factor}"
export BOREALIS_PG_AUTOVACUUM_ANALYZE_SCALE_FACTOR="${pg_autovacuum_analyze_scale_factor}"
export BOREALIS_PG_MAX_WAL_SIZE="${pg_max_wal_size}"
export BOREALIS_PG_MIN_WAL_SIZE="${pg_min_wal_size}"
export BOREALIS_PG_WAL_COMPRESSION="${pg_wal_compression}"
export BOREALIS_PG_CHECKPOINT_TIMEOUT="${pg_checkpoint_timeout}"
export BOREALIS_PG_RANDOM_PAGE_COST="${pg_random_page_cost}"
export BOREALIS_PG_EFFECTIVE_IO_CONCURRENCY="${pg_effective_io_concurrency}"
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
    run_privileged_quiet systemctl enable "${service_name}" || true
    run_privileged_quiet systemctl restart "${service_name}" || true
  fi

  # shellcheck disable=SC1090
  . "${ENGINE_DB_ENV_FILE}"
  local engine_password="${BOREALIS_DATABASE_URL#*://borealis_engine:}"
  engine_password="${engine_password%@127.0.0.1:5432/borealis}"

  if command_exists psql; then
    run_as_postgres_quiet "psql postgres -v ON_ERROR_STOP=1 <<'EOF'
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
    run_as_postgres_quiet "psql postgres -tAc \"SELECT 1 FROM pg_database WHERE datname='borealis'\" | grep -q 1" || \
      run_as_postgres_quiet "createdb -O borealis_engine borealis"
    apply_engine_postgresql_profile
    if command_exists systemctl; then
      run_privileged_quiet systemctl restart "${service_name}" || true
    fi
  fi
}

install_tesseract() {
  if command_exists tesseract; then
    return 0
  fi

  if ! allow_system_package_install; then
    ui_warn "Tesseract is not installed. OCR features will remain unavailable until it is installed separately."
    return 0
  fi

  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      run_privileged_quiet apt update -qq
      run_privileged_quiet apt install -y tesseract-ocr
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then run_privileged_quiet dnf install -y tesseract; else run_privileged_quiet yum install -y tesseract; fi
      ;;
    arch)
      run_privileged_quiet pacman -Sy --noconfirm tesseract
      ;;
    *) : ;;
  esac
}

kerberos_python_build_dependencies_available() {
  command_exists krb5-config && command_exists gcc
}

kerberos_system_dependencies_available() {
  command_exists kinit && kerberos_python_build_dependencies_available
}

kerberos_dependency_hint() {
  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      printf '%s' 'apt install -y krb5-user libkrb5-dev gcc'
      ;;
    rhel|centos|fedora|rocky|almalinux)
      printf '%s' 'dnf install -y krb5-workstation krb5-devel gcc'
      ;;
    arch)
      printf '%s' 'pacman -Sy --noconfirm krb5 gcc'
      ;;
    *)
      printf '%s' 'install krb5 tools, krb5 development headers, and gcc'
      ;;
  esac
}

install_kerberos_dependencies() {
  if kerberos_system_dependencies_available; then
    return 0
  fi

  if ! allow_system_package_install; then
    ui_warn "Kerberos packages are incomplete. Active Directory password authentication will remain unavailable until installed: $(kerberos_dependency_hint)"
    return 0
  fi

  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      run_privileged_quiet apt update -qq
      run_privileged_quiet env DEBIAN_FRONTEND=noninteractive apt install -y krb5-user libkrb5-dev gcc
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        run_privileged_quiet dnf install -y krb5-workstation krb5-devel gcc
      else
        run_privileged_quiet yum install -y krb5-workstation krb5-devel gcc
      fi
      ;;
    arch)
      run_privileged_quiet pacman -Sy --noconfirm krb5 gcc
      ;;
    *)
      ui_warn "Unsupported distro '${DISTRO_ID}' for automated Kerberos package install."
      ;;
  esac

  if ! kerberos_system_dependencies_available; then
    ui_warn "Kerberos packages are still incomplete. Active Directory password authentication will remain unavailable until installed: $(kerberos_dependency_hint)"
  fi
}

NODE_VERSION="v23.11.0"
NODE_DIR="${SCRIPT_DIR}/Dependencies/NodeJS"
NODE_BIN="${NODE_DIR}/bin/node"
NPM_BIN="${NODE_DIR}/bin/npm"
NPX_BIN="${NODE_DIR}/bin/npx"
GUACAMOLE_VERSION="${BOREALIS_GUACAMOLE_VERSION:-1.6.0}"
GUACAMOLE_BASE_URL="https://downloads.apache.org/guacamole/${GUACAMOLE_VERSION}/source"
GUACAMOLE_ROOT="${SCRIPT_DIR}/Dependencies/ApacheGuacamole/${GUACAMOLE_VERSION}"
GUACAMOLE_PREFIX="${GUACAMOLE_ROOT}/install"
GUACD_HOST="${BOREALIS_GUACD_HOST:-127.0.0.1}"
GUACD_PORT="${BOREALIS_GUACD_PORT:-4822}"

guacamole_dependency_hint() {
  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      echo "apt install -y build-essential autoconf automake libtool pkg-config libcairo2-dev libjpeg-turbo8-dev libpng-dev uuid-dev libvncserver-dev libssl-dev libwebp-dev libgcrypt20-dev"
      ;;
    rhel|centos|fedora|rocky|almalinux)
      echo "dnf install -y gcc gcc-c++ make autoconf automake libtool pkgconfig cairo-devel libjpeg-turbo-devel libpng-devel libuuid-devel libvncserver-devel openssl-devel libwebp-devel libgcrypt-devel"
      ;;
    arch)
      echo "pacman -Sy --noconfirm base-devel autoconf automake libtool pkgconf cairo libjpeg-turbo libpng util-linux-libs libvncserver openssl libwebp libgcrypt"
      ;;
    *)
      echo "install Guacamole server build tools plus LibVNCServer/LibVNCClient development headers"
      ;;
  esac
}

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
  ui_warn "npm not found on PATH; installing portable NodeJS..."
  install_node_portable
  export PATH="${NODE_DIR}/bin:${PATH}"
}

install_server_dependencies() {
  install_shared_dependencies
  install_postgresql_best_effort
  install_kerberos_dependencies
  install_tesseract
  install_wireguard_tools_best_effort engine
  install_traefik_best_effort
  install_node_portable
}

install_agent_dependencies() {
  install_shared_dependencies
  install_wireguard_tools_best_effort agent
}

resolve_guacd_binary() {
  if [[ -n "${BOREALIS_GUACD_BIN:-}" && -x "${BOREALIS_GUACD_BIN}" ]]; then
    echo "${BOREALIS_GUACD_BIN}"
    return 0
  fi
  if [[ -x "${GUACAMOLE_PREFIX}/sbin/guacd" ]]; then
    echo "${GUACAMOLE_PREFIX}/sbin/guacd"
    return 0
  fi
  if [[ -x "${GUACAMOLE_PREFIX}/bin/guacd" ]]; then
    echo "${GUACAMOLE_PREFIX}/bin/guacd"
    return 0
  fi
  if command_exists guacd; then
    command -v guacd
    return 0
  fi
  echo ""
}

verify_sha256_file() {
  local artifact="$1"
  local sha_file="$2"
  [[ -f "${artifact}" && -f "${sha_file}" ]] || return 1
  local expected actual
  expected="$(awk '{print $1; exit}' "${sha_file}" 2>/dev/null || true)"
  [[ -n "${expected}" ]] || return 1
  if command_exists sha256sum; then
    actual="$(sha256sum "${artifact}" | awk '{print $1}')"
  else
    local py_bin
    py_bin="$(resolve_python_bin)"
    [[ -n "${py_bin}" ]] || return 1
    actual="$("${py_bin}" - "${artifact}" <<'PY'
import hashlib
import sys

digest = hashlib.sha256()
with open(sys.argv[1], "rb") as fh:
    for chunk in iter(lambda: fh.read(1024 * 1024), b""):
        digest.update(chunk)
print(digest.hexdigest())
PY
)"
  fi
  [[ "${actual}" == "${expected}" ]]
}

install_guacamole_build_dependencies_best_effort() {
  if [[ -x "${GUACAMOLE_PREFIX}/sbin/guacd" || -x "${GUACAMOLE_PREFIX}/bin/guacd" ]]; then
    return 0
  fi
  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      ui_info "Installing Apache Guacamole build dependencies with apt..."
      run_privileged_quiet apt update -qq || return 1
      run_privileged_quiet env DEBIAN_FRONTEND=noninteractive apt install -y \
        build-essential autoconf automake libtool pkg-config \
        libcairo2-dev libjpeg-turbo8-dev libpng-dev uuid-dev \
        libvncserver-dev libssl-dev libwebp-dev libgcrypt20-dev || return 1
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        ui_info "Installing Apache Guacamole build dependencies with dnf..."
        run_privileged_quiet dnf install -y \
          gcc gcc-c++ make autoconf automake libtool pkgconfig \
          cairo-devel libjpeg-turbo-devel libpng-devel libuuid-devel \
          libvncserver-devel openssl-devel libwebp-devel libgcrypt-devel || return 1
      else
        ui_info "Installing Apache Guacamole build dependencies with yum..."
        run_privileged_quiet yum install -y \
          gcc gcc-c++ make autoconf automake libtool pkgconfig \
          cairo-devel libjpeg-turbo-devel libpng-devel libuuid-devel \
          libvncserver-devel openssl-devel libwebp-devel libgcrypt-devel || return 1
      fi
      ;;
    arch)
      ui_info "Installing Apache Guacamole build dependencies with pacman..."
      run_privileged_quiet pacman -Sy --noconfirm \
        base-devel autoconf automake libtool pkgconf cairo libjpeg-turbo \
        libpng util-linux-libs libvncserver openssl libwebp libgcrypt || return 1
      ;;
    *)
      ui_warn "Unsupported distro '${DISTRO_ID}' for automated Apache Guacamole build dependency install."
      return 1
      ;;
  esac
}

install_guacamole_source_build() {
  if [[ -x "${GUACAMOLE_PREFIX}/sbin/guacd" || -x "${GUACAMOLE_PREFIX}/bin/guacd" ]]; then
    ui_info "Apache Guacamole guacd already installed at $(resolve_guacd_binary)."
    write_engine_log "Apache Guacamole guacd available." "engine-supervision.log"
    return 0
  fi
  mkdir -p "${GUACAMOLE_ROOT}/source" "${GUACAMOLE_ROOT}/licenses"
  local server_archive="guacamole-server-${GUACAMOLE_VERSION}.tar.gz"
  local client_archive="guacamole-client-${GUACAMOLE_VERSION}.tar.gz"
  local server_path="${GUACAMOLE_ROOT}/source/${server_archive}"
  local client_path="${GUACAMOLE_ROOT}/source/${client_archive}"
  local server_sha="${server_path}.sha256"
  local client_sha="${client_path}.sha256"

  ui_info "Downloading Apache Guacamole ${GUACAMOLE_VERSION} server source..."
  download_file "${GUACAMOLE_BASE_URL}/${server_archive}" "${server_path}" || return 1
  ui_info "Downloading Apache Guacamole ${GUACAMOLE_VERSION} server checksum..."
  download_file "${GUACAMOLE_BASE_URL}/${server_archive}.sha256" "${server_sha}" || return 1
  ui_info "Downloading Apache Guacamole ${GUACAMOLE_VERSION} client source for LICENSE/NOTICE..."
  download_file "${GUACAMOLE_BASE_URL}/${client_archive}" "${client_path}" || return 1
  ui_info "Downloading Apache Guacamole ${GUACAMOLE_VERSION} client checksum..."
  download_file "${GUACAMOLE_BASE_URL}/${client_archive}.sha256" "${client_sha}" || return 1
  ui_info "Verifying Apache Guacamole source checksums..."
  verify_sha256_file "${server_path}" "${server_sha}" || return 1
  verify_sha256_file "${client_path}" "${client_sha}" || return 1

  ui_info "Installing Apache Guacamole build dependencies..."
  install_guacamole_build_dependencies_best_effort || {
    ui_warn "Apache Guacamole build dependencies missing. Install with: $(guacamole_dependency_hint)"
    return 1
  }

  local build_root="${GUACAMOLE_ROOT}/build"
  rm -rf "${build_root}"
  mkdir -p "${build_root}"
  ui_info "Extracting Apache Guacamole source archives..."
  tar -xzf "${server_path}" -C "${build_root}"
  tar -xzf "${client_path}" -C "${build_root}"
  ui_info "Preserving Apache Guacamole LICENSE and NOTICE files..."
  cp "${build_root}/guacamole-server-${GUACAMOLE_VERSION}/LICENSE" "${GUACAMOLE_ROOT}/licenses/guacamole-server-LICENSE" 2>/dev/null || true
  cp "${build_root}/guacamole-server-${GUACAMOLE_VERSION}/NOTICE" "${GUACAMOLE_ROOT}/licenses/guacamole-server-NOTICE" 2>/dev/null || true
  cp "${build_root}/guacamole-client-${GUACAMOLE_VERSION}/LICENSE" "${GUACAMOLE_ROOT}/licenses/guacamole-client-LICENSE" 2>/dev/null || true
  cp "${build_root}/guacamole-client-${GUACAMOLE_VERSION}/NOTICE" "${GUACAMOLE_ROOT}/licenses/guacamole-client-NOTICE" 2>/dev/null || true

  (
    cd "${build_root}/guacamole-server-${GUACAMOLE_VERSION}"
    ui_info "Configuring Apache Guacamole Server for VNC-only guacd..."
    run_logged_command ./configure \
      --prefix="${GUACAMOLE_PREFIX}" \
      --with-vnc \
      --with-rdp=no \
      --with-ssh=no \
      --with-telnet=no \
      --disable-kubernetes \
      --disable-guacenc \
      --disable-guaclog
    ui_info "Building Apache Guacamole Server. This can take several minutes..."
    run_logged_command make -j"$(nproc 2>/dev/null || echo 2)"
    ui_info "Installing Apache Guacamole Server into ${GUACAMOLE_PREFIX}..."
    run_logged_command make install
  )

  if [[ ! -x "$(resolve_guacd_binary)" ]]; then
    return 1
  fi
  ui_success "Apache Guacamole guacd installed at $(resolve_guacd_binary)."
  write_engine_log "Apache Guacamole ${GUACAMOLE_VERSION} built from source with VNC support." "engine-supervision.log"
  return 0
}

configure_guacamole_vnc_runtime() {
  if install_guacamole_source_build; then
    return 0
  fi
  ui_warn "Apache Guacamole VNC support unavailable; Remote Desktop remains unavailable until guacd is installed."
  return 0
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

  if ! allow_system_package_install; then
    if [[ "${install_target}" == "engine" ]]; then
      write_engine_log "WireGuard tools not found and package-manager installation is disabled for this Borealis.sh run." "engine-supervision.log"
    else
      write_agent_log "WireGuard tools not found and package-manager installation is disabled for this Borealis.sh run."
    fi
    ui_warn "WireGuard tools not found. Run bootstrap.sh or install 'wireguard-tools' manually for VPN tunnel support."
    return 0
  fi

  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      run_privileged_quiet apt update -qq || true
      run_privileged_quiet apt install -y wireguard-tools || true
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        run_privileged_quiet dnf install -y wireguard-tools || true
      else
        run_privileged_quiet yum install -y wireguard-tools || true
      fi
      ;;
    arch)
      run_privileged_quiet pacman -Sy --noconfirm wireguard-tools || true
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
    ui_warn "WireGuard tools not found. Install 'wireguard-tools' for VPN tunnel support."
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
    ui_error "Python interpreter not found. Install Python 3 first."
    return 1
  }
  [[ -d "$source_root" ]] || {
    ui_error "Agent source directory '${source_root}' was not found."
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
    "qt_compat.py"
    "role_health.py"
    "role_manager.py"
    "runtime_paths.py"
    "security.py"
    "session_runtime.py"
    "signature_utils.py"
    "sitecustomize.py"
    "termios_stub.py"
    "tray_state.py"
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
    ui_error "Agent virtual environment is missing Python."
    return 1
  }

  local req_path="${SCRIPT_DIR}/Data/Agent/agent-requirements.txt"
  if [[ -f "$req_path" ]]; then
    run_logged_command "$venv_py" -m pip install --disable-pip-version-check -q -r "$req_path"
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

  run_privileged_quiet cp "$tmp_unit" "$unit_path" || return 1
  run_privileged_quiet systemctl daemon-reload || return 1
  run_privileged_quiet systemctl enable "$unit_name" || return 1
  run_privileged_quiet systemctl restart "$unit_name" || return 1
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
OnCalendar=hourly
RandomizedDelaySec=15min
Persistent=true
AccuracySec=1s
Unit=${service_name}

[Install]
WantedBy=timers.target
EOF

  run_privileged_quiet cp "${service_tmp}" "${service_path}" || return 1
  run_privileged_quiet cp "${timer_tmp}" "${timer_path}" || return 1
  run_privileged_quiet chmod 644 "${service_path}" "${timer_path}" || true
  run_privileged_quiet chmod +x "${updater_script}" || true
  run_privileged_quiet systemctl daemon-reload || return 1
  run_privileged_quiet systemctl enable "${timer_name}" || return 1
  run_privileged_quiet systemctl restart "${timer_name}" || return 1
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
  run_privileged_quiet systemctl stop "${unit_name}" || true
}

print_systemd_unit_diagnostics() {
  local unit_name="$1"
  if ! command_exists systemctl; then
    return 0
  fi

  ui_warn "Detailed systemd diagnostics for ${unit_name}:"
  run_privileged systemctl --no-pager --full status "${unit_name}" || true
  if command_exists journalctl; then
    ui_warn "Recent journal entries for ${unit_name}:"
    run_privileged journalctl -u "${unit_name}" -n 40 --no-pager || true
  fi
}

print_systemd_unit_summary() {
  local unit_name="$1"
  local label="$2"
  if ! command_exists systemctl; then
    ui_warn "systemctl not available; skipping ${label} service summary."
    return 0
  fi

  local active_state
  active_state="$(run_privileged systemctl is-active "${unit_name}" 2>/dev/null || true)"
  local enabled_state
  enabled_state="$(run_privileged systemctl is-enabled "${unit_name}" 2>/dev/null || true)"
  local sub_state
  sub_state="$(run_privileged systemctl show -p SubState --value "${unit_name}" 2>/dev/null || true)"

  if [[ "${active_state}" == "active" || "${active_state}" == "activating" ]]; then
    if [[ -n "${sub_state}" ]]; then
      ui_success "${label}: ${active_state}/${sub_state} (${enabled_state}; ${unit_name})"
    else
      ui_success "${label}: ${active_state} (${enabled_state}; ${unit_name})"
    fi
    if [[ "${VERBOSE_FLAG}" -eq 1 ]]; then
      print_systemd_unit_diagnostics "${unit_name}"
    fi
    return 0
  fi

  if [[ -n "${sub_state}" ]]; then
    ui_error "${label}: ${active_state:-unknown}/${sub_state} (${enabled_state}; ${unit_name})"
  else
    ui_error "${label}: ${active_state:-unknown} (${enabled_state}; ${unit_name})"
  fi
  print_systemd_unit_diagnostics "${unit_name}"
  return 1
}

print_agent_service_status() {
  local unit_name="${BOREALIS_AGENT_SYSTEMD_UNIT:-borealis-agent.service}"
  print_systemd_unit_summary "${unit_name}" "Agent service"
}

print_agent_launch_summary() {
  if [[ "${VERBOSE_FLAG}" -ne 1 ]]; then
    return 0
  fi
  local runtime_dir
  runtime_dir="$(agent_runtime_dir)"
  ui_info "Agent runtime directory: ${runtime_dir}"
  ui_info "Agent install log: $(ensure_agent_log_dir)/install.log"
  ui_info "Agent runtime logs: ${SCRIPT_DIR}/Agent/Logs/agent.log"
}

install_or_update_borealis_agent() {
  local noninteractive_mode="${1:-0}"
  set_output_context agent
  if [[ "${NEW_ENGINE_FLAG}" -eq 0 ]] && env_flag_enabled "${BOOTSTRAP_NEW_ENGINE_DEFAULT}"; then
    NEW_ENGINE_FLAG=1
    ui_verbose "Bootstrap-invoked agent install detected; enabling --newEngine behavior."
  fi

  local total_steps=5
  if [[ "${NEW_ENGINE_FLAG}" -eq 1 ]]; then
    total_steps=$((total_steps + 1))
  fi
  set_step_plan "${total_steps}"

  run_step "Verifying Runtime Dependencies" install_agent_dependencies

  local runtime_dir
  runtime_dir="$(agent_runtime_dir)"
  ui_verbose "Agent runtime directory: ${runtime_dir}"
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
    ui_error "Agent runtime path is on a noexec mount. Set BOREALIS_AGENT_VENV to an executable path (for example /opt/Borealis/Agent)."
    return 1
  fi
  run_step "Configure Agent supervision" configure_agent_supervision
  print_agent_service_status
  print_agent_launch_summary
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
    ui_warn "Repairing stale Engine virtual environment ownership under '${venv_dir}'."
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
  ui_error "Engine virtual environment is not writable at '${site_packages_dir}'."
  ui_warn "Borealis could not repair the Engine venv automatically; recreate '${venv_dir}' with the current user before installing Python packages."
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
    ui_error "Engine virtual environment is missing Python."
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

  run_logged_command env \
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

  run_logged_command "${venv_py}" -c $'import importlib.util\nrequired = ("ansible", "ansible_runner", "winrm", "pypsrp")\nmissing = [name for name in required if importlib.util.find_spec(name) is None]\nif missing:\n    raise SystemExit("Missing Python modules: " + ", ".join(missing))' || return 1

  if [[ -f "${collections_req}" ]]; then
    run_logged_command env \
      ANSIBLE_COLLECTIONS_PATH="${collections_dir}" \
      ANSIBLE_COLLECTIONS_PATHS="${collections_dir}" \
      "${version_cmd[@]}" || return 1
  fi
}

configure_engine_ansible_runtime() {
  install_engine_ansible_collections || return 1
  verify_engine_ansible_runtime
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

engine_public_url() {
  printf 'https://%s' "$(resolve_engine_public_fqdn)"
}

engine_mode_display_label() {
  local mode="${1:-production}"
  if [[ "${mode}" == "developer" ]]; then
    echo "Dev"
  else
    echo "Production"
  fi
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
    ui_error "Borealis needs a public FQDN and Let's Encrypt email, but no interactive terminal is available."
    ui_warn "Set BOREALIS_PUBLIC_FQDN and BOREALIS_ACME_EMAIL, or rerun Borealis.sh interactively."
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
    ui_warn "Enter a public FQDN with at least one dot, such as borealis.bunny-lab.io."
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
    ui_warn "Enter a valid email address for Let's Encrypt expiration notices."
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
  ui_warn "Borealis embedded Traefik does not support Cloudflare orange-cloud or other CDN-style TLS proxying in front of the engine."
  ui_warn "Use DNS-only records or a transparent reverse proxy that forwards 80/tcp and TCP-passthrough 443/tcp to the engine host."

  [[ -n "${fqdn}" ]] || return 0

  local headers
  headers="$(fetch_url_response_headers "http://${fqdn}/.well-known/acme-challenge/borealis-probe")"
  if [[ -z "${headers}" ]]; then
    headers="$(fetch_url_response_headers "https://${fqdn}/")"
  fi

  local lowered
  lowered="$(printf '%s' "${headers}" | tr '[:upper:]' '[:lower:]')"
  if printf '%s\n' "${lowered}" | grep -qE '(^server:\s*cloudflare|^cf-ray:|^cf-cache-status:)'; then
    ui_error "Detected Cloudflare proxy headers for ${fqdn}."
    ui_error "Disable Cloudflare proxying and switch the DNS record to DNS only before using Borealis embedded Traefik + Let's Encrypt."
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
  local mode="${1:-production}"
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
    if [[ "${settings_exist}" == "1" ]]; then
      ui_warn "Existing Let's Encrypt settings are incomplete or invalid and will be regenerated."
    fi
    local prompt_result
    prompt_result="$(prompt_engine_public_edge_configuration "${current_fqdn}" "${current_email}")" || return 1
    IFS='|' read -r fqdn email <<<"${prompt_result}"
    reset_engine_public_edge_runtime || return 1
    warn_about_unsupported_cloudflare_proxying "${fqdn}"
  fi

  run_logged_command env \
    BOREALIS_PROJECT_ROOT="${SCRIPT_DIR}" \
    BOREALIS_DEV_UI_PROXY_ENABLED="$([[ "${mode}" == "developer" ]] && echo 1 || echo 0)" \
    "${venv_py}" -m Data.Engine.edge_runtime ensure-files \
    --settings-path "$(engine_letsencrypt_settings_path)" \
    --fqdn "${fqdn}" \
    --email "${email}"
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

  run_privileged_quiet install -m 0755 "${tmp_dir}/traefik" /usr/local/bin/traefik || {
    rm -rf "${tmp_dir}"
    return 1
  }
  rm -rf "${tmp_dir}"
}

install_traefik_best_effort() {
  if [[ -n "$(resolve_traefik_binary)" ]]; then
    return 0
  fi

  if ! allow_system_package_install; then
    download_traefik_binary
    return $?
  fi

  detect_distro
  case "$DISTRO_ID" in
    ubuntu|debian|linuxmint|pop)
      run_privileged_quiet apt update -qq || true
      run_privileged_quiet apt install -y traefik || true
      ;;
    rhel|centos|fedora|rocky|almalinux)
      if command_exists dnf; then
        run_privileged_quiet dnf install -y traefik || true
      else
        run_privileged_quiet yum install -y traefik || true
      fi
      ;;
    arch)
      run_privileged_quiet pacman -Sy --noconfirm traefik || true
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

  run_privileged_quiet cp "${tmp_unit}" "${unit_path}" || return 1
  run_privileged_quiet systemctl daemon-reload || return 1
  run_privileged_quiet systemctl enable "${unit_name}" || return 1
  run_privileged_quiet systemctl restart "${unit_name}" || return 1
  write_engine_log "Systemd service '${unit_name}' installed/restarted."
}

print_traefik_service_status() {
  local unit_name="borealis-traefik.service"
  print_systemd_unit_summary "${unit_name}" "Traefik edge"
}

ensure_guacd_systemd_service() {
  local guacd_bin
  guacd_bin="$(resolve_guacd_binary)"
  if [[ -z "${guacd_bin}" || ! -x "${guacd_bin}" ]]; then
    write_engine_log "Apache Guacamole guacd not installed; skipping guacd systemd service." "engine-supervision.log"
    return 0
  fi
  local unit_name="borealis-guacd.service"
  local unit_path="/etc/systemd/system/${unit_name}"
  local tmp_unit
  tmp_unit="$(ensure_engine_log_dir)/${unit_name}"
  mkdir -p "${SCRIPT_DIR}/Engine/Logs"
  cat > "${tmp_unit}" <<EOF
[Unit]
Description=Borealis Apache Guacamole Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${SCRIPT_DIR}
Environment=LD_LIBRARY_PATH=${GUACAMOLE_PREFIX}/lib:${GUACAMOLE_PREFIX}/lib64
ExecStart=/usr/bin/env bash -lc 'exec "${guacd_bin}" -f -b "${GUACD_HOST}" -l "${GUACD_PORT}" -L info >>"${SCRIPT_DIR}/Engine/Logs/guacd.log" 2>&1'
Restart=always
RestartSec=5
KillMode=process
TimeoutStopSec=20

[Install]
WantedBy=multi-user.target
EOF
  run_privileged_quiet cp "${tmp_unit}" "${unit_path}" || return 1
  run_privileged_quiet systemctl daemon-reload || return 1
  run_privileged_quiet systemctl enable "${unit_name}" || return 1
  run_privileged_quiet systemctl restart "${unit_name}" || return 1
  write_engine_log "Systemd service '${unit_name}' installed/restarted."
  return 0
}

print_guacd_service_status() {
  local guacd_bin
  guacd_bin="$(resolve_guacd_binary)"
  if [[ -z "${guacd_bin}" || ! -x "${guacd_bin}" ]]; then
    ui_warn "Apache Guacamole: unavailable (guacd not installed; Remote Desktop unavailable)"
    return 0
  fi
  local unit_name="borealis-guacd.service"
  print_systemd_unit_summary "${unit_name}" "Apache Guacamole"
}

# ---- Engine web interface staging (parity with Ensure-EngineWebInterface) ----
ensure_engine_web_interface() {
  local project_root="$1"
  local dest="${project_root}/Engine/web-interface"
  local stage="${project_root}/Data/Engine/web-interface"
  [[ -d "$stage" ]] || { ui_error "Engine web interface source missing at '$stage'."; return 1; }
  rm -rf "$dest" 2>/dev/null || true
  mkdir -p "$dest"
  cp -a "${stage}/." "$dest/"
  [[ -f "${dest}/package.json" ]] || { ui_error "Failed to stage Engine web interface into '$dest'."; return 1; }
}

engine_override_merge_helper_path() {
  echo "${SCRIPT_DIR}/Data/Engine/services/API/devices/runtime_override_merge.py"
}

capture_engine_runtime_override_snapshot() {
  local runtime_root="$1"
  local snapshot_root="$2"
  mkdir -p "${snapshot_root}"

  local spec=""
  while IFS='|' read -r relative_path _merge_kind; do
    [[ -n "${relative_path}" ]] || continue
    local runtime_file="${runtime_root}/${relative_path}"
    local snapshot_file="${snapshot_root}/${relative_path}"
    if [[ -f "${runtime_file}" ]]; then
      mkdir -p "$(dirname "${snapshot_file}")"
      cp -a "${runtime_file}" "${snapshot_file}"
    fi
  done <<'EOF'
services/API/devices/software_icons_overrides.json|software_icons
services/API/devices/software_uninstall_overrides.json|software_uninstall_overrides
services/API/devices/software_uninstall_blocklist.json|software_uninstall_blocklist
EOF
}

merge_engine_runtime_override_snapshot() {
  local source_root="$1"
  local destination_root="$2"
  local snapshot_root="$3"
  local py_bin
  py_bin="$(resolve_python_bin)"
  [[ -n "${py_bin}" ]] || {
    ui_error "Python interpreter not found. Cannot merge Engine override files during restage."
    return 1
  }

  local helper_path
  helper_path="$(engine_override_merge_helper_path)"
  [[ -f "${helper_path}" ]] || {
    ui_error "Engine override merge helper missing at '${helper_path}'."
    return 1
  }

  local spec=""
  while IFS='|' read -r relative_path merge_kind; do
    [[ -n "${relative_path}" ]] || continue
    local source_file="${source_root}/${relative_path}"
    local destination_file="${destination_root}/${relative_path}"
    local runtime_snapshot_file="${snapshot_root}/${relative_path}"
    if [[ ! -f "${runtime_snapshot_file}" ]]; then
      continue
    fi
    if ! "${py_bin}" "${helper_path}" \
      --kind "${merge_kind}" \
      --source "${source_file}" \
      --runtime "${runtime_snapshot_file}" \
      --output "${destination_file}"; then
      ui_warn "Failed to merge Engine override file '${relative_path}' during restage; preserving runtime snapshot."
      mkdir -p "$(dirname "${destination_file}")"
      cp -a "${runtime_snapshot_file}" "${destination_file}"
    fi
  done <<'EOF'
services/API/devices/software_icons_overrides.json|software_icons
services/API/devices/software_uninstall_overrides.json|software_uninstall_overrides
services/API/devices/software_uninstall_blocklist.json|software_uninstall_blocklist
EOF
}

sync_engine_runtime() {
  local source_root="${SCRIPT_DIR}/Data/Engine"
  local destination_root="${SCRIPT_DIR}/Engine/Data/Engine"
  local override_snapshot_dir=""
  [[ -d "$source_root" ]] || return 1

  override_snapshot_dir="$(mktemp -d)"
  capture_engine_runtime_override_snapshot "${destination_root}" "${override_snapshot_dir}"

  rm -rf "$destination_root" 2>/dev/null || true
  mkdir -p "$destination_root"

  shopt -s dotglob nullglob
  local item=""
  for item in "${source_root}"/*; do
    cp -a "$item" "${destination_root}/"
  done
  shopt -u dotglob nullglob

  merge_engine_runtime_override_snapshot "${source_root}" "${destination_root}" "${override_snapshot_dir}" || {
    rm -rf "${override_snapshot_dir}" 2>/dev/null || true
    return 1
  }
  rm -rf "${override_snapshot_dir}" 2>/dev/null || true

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
  local guacamole_enabled_default=0
  local guacd_bin
  guacd_bin="$(resolve_guacd_binary)"
  if [[ -n "${guacd_bin}" && -x "${guacd_bin}" ]]; then
    guacamole_enabled_default=1
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
export BOREALIS_GUACAMOLE_ENABLED="\${BOREALIS_GUACAMOLE_ENABLED:-${guacamole_enabled_default}}"
export BOREALIS_GUACD_HOST="\${BOREALIS_GUACD_HOST:-${GUACD_HOST}}"
export BOREALIS_GUACD_PORT="\${BOREALIS_GUACD_PORT:-${GUACD_PORT}}"
export BOREALIS_GUACAMOLE_VNC_WS_PATH="\${BOREALIS_GUACAMOLE_VNC_WS_PATH:-/remote-desktop/vnc/guacamole}"
export ANSIBLE_COLLECTIONS_PATH="\${PROJECT_ROOT}/Engine/Ansible/collections"
export ANSIBLE_COLLECTIONS_PATHS="\${ANSIBLE_COLLECTIONS_PATH}"
export PATH="\${NODE_BIN_DIR}:${GUACAMOLE_PREFIX}/sbin:${GUACAMOLE_PREFIX}/bin:\${PATH}"
export LD_LIBRARY_PATH="${GUACAMOLE_PREFIX}/lib:${GUACAMOLE_PREFIX}/lib64:\${LD_LIBRARY_PATH:-}"
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
  export BOREALIS_DEV_UI_PROXY_ENABLED=1
  PATH="\${NODE_BIN_DIR}:\${PATH}" "\${NPM_CMD}" run dev -- --open false >>"\${ENGINE_LOG_DIR}/vite-dev.stdout.log" 2>>"\${ENGINE_LOG_DIR}/vite-dev.stderr.log" &
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
  local guacd_unit_dependency=""
  local guacd_bin
  guacd_bin="$(resolve_guacd_binary)"
  if [[ -n "${guacd_bin}" && -x "${guacd_bin}" ]]; then
    guacd_unit_dependency=" borealis-guacd.service"
  fi

  cat > "${tmp_unit}" <<EOF
[Unit]
Description=Borealis Engine Service
After=network-online.target ${pg_service}.service borealis-traefik.service${guacd_unit_dependency}
Wants=network-online.target ${pg_service}.service borealis-traefik.service${guacd_unit_dependency}

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

  run_privileged_quiet cp "${tmp_unit}" "${unit_path}" || return 1
  run_privileged_quiet systemctl daemon-reload || return 1
  run_privileged_quiet systemctl enable "${unit_name}" || return 1
  run_privileged_quiet systemctl restart "${unit_name}" || return 1
  write_engine_log "Systemd service '${unit_name}' installed/restarted in ${mode} mode."
  return 0
}

print_engine_service_status() {
  local unit_name="borealis-engine.service"
  print_systemd_unit_summary "${unit_name}" "Engine service"
}

print_engine_runtime_status_summary() {
  print_traefik_service_status
  print_guacd_service_status
  print_engine_service_status
  print_wireguard_listener_status
}

format_wireguard_listener_state() {
  local interface_name="$1"
  local oper_state="${2:-}"
  local link_summary=""

  if command_exists ip; then
    link_summary="$(ip link show dev "${interface_name}" 2>/dev/null | head -n 1 || true)"
  fi

  if [[ "${oper_state}" == "up" ]]; then
    echo "active/running"
    return 0
  fi

  if [[ -z "${oper_state}" || "${oper_state}" == "unknown" ]]; then
    if [[ "${link_summary}" == *"<UP,"* || "${link_summary}" == *",UP,"* || "${link_summary}" == *",UP>"* || "${link_summary}" == *" state UP "* ]]; then
      echo "active/running"
      return 0
    fi
  fi

  echo "${oper_state:-present}"
}

print_wireguard_listener_status() {
  local interface_name="${BOREALIS_WIREGUARD_INTERFACE:-borealis-wg}"
  local config_path="${SCRIPT_DIR}/Engine/WireGuard/${interface_name}.conf"

  if ! command_exists ip; then
    [[ "${VERBOSE_FLAG}" -eq 1 ]] && ui_warn "WireGuard listener summary unavailable; 'ip' is not installed."
    return 0
  fi

  if ip link show dev "${interface_name}" >/dev/null 2>&1; then
    local oper_state=""
    if [[ -r "/sys/class/net/${interface_name}/operstate" ]]; then
      oper_state="$(cat "/sys/class/net/${interface_name}/operstate" 2>/dev/null || true)"
    fi
    ui_success "WireGuard listener: $(format_wireguard_listener_state "${interface_name}" "${oper_state}") (${interface_name})"
    return 0
  fi

  if [[ -f "${config_path}" ]]; then
    ui_info "WireGuard listener: configured/idle (${interface_name})"
    return 0
  fi

  [[ "${VERBOSE_FLAG}" -eq 1 ]] && ui_info "WireGuard listener: not provisioned (${interface_name})"
  return 0
}

print_engine_ready_message() {
  local mode="${1:-production}"
  ui_success "Borealis Engine ($(engine_mode_display_label "${mode}")) Started Successfully @ $(engine_public_url)"
}

print_engine_launch_summary() {
  if [[ "${VERBOSE_FLAG}" -ne 1 ]]; then
    return 0
  fi
  local mode="${1:-production}"
  ui_info "Engine URL: $(engine_public_url)"
  ui_info "Engine install log: $(ensure_engine_log_dir)/install.log"
  ui_info "Engine runtime log: ${SCRIPT_DIR}/Engine/Logs/engine.log"
  if [[ "${mode}" == "developer" ]]; then
    ui_info "Vite logs: ${SCRIPT_DIR}/Engine/Logs/vite-dev.stdout.log and ${SCRIPT_DIR}/Engine/Logs/vite-dev.stderr.log"
  fi
}

configure_engine_supervision() {
  local mode="$1" # production|developer
  if command_exists systemctl; then
    ensure_traefik_systemd_service
    ensure_guacd_systemd_service
    ensure_engine_systemd_service "${mode}"
    return 0
  fi

  ui_warn "systemctl not available; launching Engine in the current shell instead."
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
  local override_snapshot_dir=""
  local py_bin
  py_bin="$(resolve_python_bin)"
  [[ -n "$py_bin" ]] || {
    ui_error "Python interpreter not found. Install Python 3 first."
    return 1
  }

  override_snapshot_dir="$(mktemp -d)"
  capture_engine_runtime_override_snapshot "${venv_dir}/Data/Engine" "${override_snapshot_dir}"

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

  merge_engine_runtime_override_snapshot "${engine_src}" "${data_dest}" "${override_snapshot_dir}" || {
    rm -rf "${override_snapshot_dir}" 2>/dev/null || true
    return 1
  }
  rm -rf "${override_snapshot_dir}" 2>/dev/null || true

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
  local kerberos_reqs="${engine_src}/engine-kerberos-requirements.txt"
  local site_packages_dir
  site_packages_dir="$(engine_site_packages_dir)"
  local pth_paths=(
    "${SCRIPT_DIR}"
    "${SCRIPT_DIR}/Data/Agent"
    "${SCRIPT_DIR}/Data/Engine"
  )
  for r in "${reqs[@]}"; do
    if [[ -f "$r" && -n "$venv_py" ]]; then
      run_logged_command "$venv_py" -m pip install --disable-pip-version-check -q -r "$r" || return 1
      if [[ -n "${site_packages_dir}" ]]; then
        printf '%s\n' "${pth_paths[@]}" > "${site_packages_dir}/borealis-project-root.pth"
      fi
      if [[ -f "$kerberos_reqs" ]]; then
        if kerberos_python_build_dependencies_available; then
          run_logged_command "$venv_py" -m pip install --disable-pip-version-check -q -r "$kerberos_reqs" || \
            ui_warn "python-gssapi installation failed. Active Directory password authentication remains unavailable until Kerberos Python dependencies install successfully."
        else
          ui_warn "Skipping python-gssapi install because Kerberos packages are incomplete. Active Directory password authentication remains unavailable until installed: $(kerberos_dependency_hint)"
        fi
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
  run_logged_command bash -lc "cd \"$engine_ui_dest\" && PATH=\"${NODE_DIR}/bin:\$PATH\" \"${NPM_BIN}\" install --loglevel=error --no-fund --audit=false"
}

vite_web_frontend_start() {
  local mode="$1" # developer|production
  local engine_ui_dest="${SCRIPT_DIR}/Engine/web-interface"
  ensure_node_bins

  if [[ "$mode" == "developer" ]]; then
    if [[ "${ENGINE_USE_SYSTEMD_SUPERVISION:-0}" -eq 1 ]]; then
      write_vite_log "Skipping direct Vite dev launch; borealis-engine.service will manage same-origin developer-mode processes behind the Borealis edge." "vite-dev"
      return 0
    fi
    local logdir; logdir=$(ensure_engine_log_dir)
    local stdout_log="${logdir}/vite-dev.stdout.log"
    local stderr_log="${logdir}/vite-dev.stderr.log"
    mv -f "$stdout_log" "${stdout_log}.$(date +%Y%m%d%H%M%S)" 2>/dev/null || true
    mv -f "$stderr_log" "${stderr_log}.$(date +%Y%m%d%H%M%S)" 2>/dev/null || true
    write_vite_log "Starting Vite dev server for same-origin Traefik development mode." "vite-dev"
    (
      cd "$engine_ui_dest"
      export BOREALIS_ENGINE_MODE="$mode"
      export BOREALIS_DEV_UI_PROXY_ENABLED=1
      if [[ -f "$(engine_letsencrypt_runtime_env_path)" ]]; then
        # shellcheck disable=SC1090
        . "$(engine_letsencrypt_runtime_env_path)"
      fi
      PATH="${NODE_DIR}/bin:${PATH}" nohup "$NPM_BIN" run dev >"$stdout_log" 2>"$stderr_log" &
    )
  else
    write_vite_log "Executing npm run build for production WebUI assets." "vite-build"
    local logdir; logdir=$(ensure_engine_log_dir)
    local stdout_log="${logdir}/vite-build.stdout.log"
    local stderr_log="${logdir}/vite-build.stderr.log"
    mv -f "$stdout_log" "${stdout_log}.$(date +%Y%m%d%H%M%S)" 2>/dev/null || true
    mv -f "$stderr_log" "${stderr_log}.$(date +%Y%m%d%H%M%S)" 2>/dev/null || true
    if ! (
      cd "$engine_ui_dest" &&
      PATH="${NODE_DIR}/bin:${PATH}" "$NPM_BIN" run build >>"$stdout_log" 2>>"$stderr_log"
    ); then
      append_file_tail_to_current_step_logs "vite build stderr tail" "$stderr_log" 40
      append_file_tail_to_current_step_logs "vite build stdout tail" "$stdout_log" 20
      write_vite_log "npm run build failed. stderr log: ${stderr_log}" "vite-build"
      return 1
    fi
    write_vite_log "npm run build completed successfully." "vite-build"
  fi
}

configure_engine_frontend() {
  local mode="$1"
  ensure_engine_web_interface "$SCRIPT_DIR" || return 1
  vite_web_frontend_install || return 1
  vite_web_frontend_start "$mode"
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
  ui_info "Launching Borealis Engine..."
  ui_info "Bootstrapping Borealis Engine ($(engine_mode_display_label "${mode}")) @ $(engine_public_url)"
  local logdir; logdir=$(ensure_engine_log_dir)
  local stdout_log="${logdir}/engine-launch.stdout.log"
  local stderr_log="${logdir}/engine-launch.stderr.log"
  "$py" -m Data.Engine.bootstrapper >>"$stdout_log" 2>>"$stderr_log" || true
  # restore env
  if [[ -n "$prev_mode" ]]; then export BOREALIS_ENGINE_MODE="$prev_mode"; else unset BOREALIS_ENGINE_MODE; fi
  if [[ -n "$prev_root" ]]; then export BOREALIS_PROJECT_ROOT="$prev_root"; else unset BOREALIS_PROJECT_ROOT; fi
  popd >/dev/null
}

# ---- Banner ----
show_borealis_banner() {
  if [[ -n "${CHOICE}" ]] || ! is_interactive_terminal; then
    return 0
  fi
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
}

# ---- Menus ----
server_menu() {
  local mode_choice="${1:-}"
  local borealis_operation_mode="production"
  local engine_immediate_launch=0

  if [[ -z "${mode_choice}" ]]; then
    printf "\nConfigure Borealis Engine Mode:\n"
    printf " 1) Build & Launch > Borealis Traefik Edge + Engine\n"
    printf " 2) [Skip Build] & Immediately Launch > Borealis Traefik Edge + Engine\n"
    printf " 3) Launch > [Hotload-Ready] Vite Dev UI via Borealis Edge\n"
    mode_choice="$(prompt_input "Enter choice [1/2/3]: ")"
  else
    ui_verbose "Auto-selecting Borealis Engine mode option ${mode_choice}."
  fi

  case "$mode_choice" in
    1) borealis_operation_mode="production" ;;
    2) borealis_operation_mode="production"; engine_immediate_launch=1 ;;
    3) borealis_operation_mode="developer" ;;
    *) ui_error "Invalid mode choice."; return 1 ;;
  esac

  if [[ "$engine_immediate_launch" -eq 1 ]]; then
    if ! test_webui_build_fresh "${SCRIPT_DIR}/Data/Engine/web-interface" "${SCRIPT_DIR}/Engine/web-interface/build"; then
      ui_warn "Detected newer WebUI source than production build. Running full build instead of Quick/Skip."
      engine_immediate_launch=0
    fi
  fi

  set_output_context engine
  if [[ "${engine_immediate_launch}" -eq 1 ]]; then
    set_step_plan 7
  else
    set_step_plan 10
  fi

  run_step "Verifying Runtime Dependencies" install_server_dependencies
  run_step "Auto-Configure Engine Profile" auto_configure_engine_profile
  run_step "Configure PostgreSQL" ensure_engine_postgresql_ready
  export PATH="${NODE_DIR}/bin:${PATH}"
  ENGINE_USE_SYSTEMD_SUPERVISION=0
  if command_exists systemctl; then
    ENGINE_USE_SYSTEMD_SUPERVISION=1
  fi

  if [[ "$engine_immediate_launch" -eq 1 ]]; then
    run_step "Sync Engine Runtime" sync_engine_runtime
    run_step "Configure Borealis Traefik Edge" ensure_engine_public_edge_runtime "$borealis_operation_mode"
    run_step "Verify Engine Ansible Runtime" verify_engine_ansible_runtime
    run_step "Configure Borealis Engine supervision (${borealis_operation_mode})" configure_engine_supervision "$borealis_operation_mode"
    print_engine_runtime_status_summary
    print_engine_ready_message "$borealis_operation_mode"
    print_engine_launch_summary "$borealis_operation_mode"
    return 0
  fi

  run_step "Prepare Engine Python Environment" create_engine_venv_and_stage_data
  run_step "Install Engine Python Dependencies" install_engine_python_deps
  run_step "Configure Apache Guacamole VNC Runtime" configure_guacamole_vnc_runtime
  run_step "Configure Borealis Traefik Edge" ensure_engine_public_edge_runtime "$borealis_operation_mode"
  run_step "Configure Engine Ansible Runtime" configure_engine_ansible_runtime
  run_step "Configure Vite Engine Frontend" configure_engine_frontend "$borealis_operation_mode"
  run_step "Configure Borealis Engine supervision (${borealis_operation_mode})" configure_engine_supervision "$borealis_operation_mode"
  print_engine_runtime_status_summary
  print_engine_ready_message "$borealis_operation_mode"
  print_engine_launch_summary "$borealis_operation_mode"
}

agent_menu() {
  printf "\n"
  ui_info "Deploying Borealis Agent..."
  if [[ "${REFRESH_AGENT_RUNTIME_FLAG}" -eq 1 ]]; then
    install_or_update_borealis_agent 1
    return $?
  fi
  install_or_update_borealis_agent 0
}

main_menu() {
  printf "\nPlease choose which function you want to launch:\n"
  printf " 1) Borealis Engine\n"
  printf " 2) Borealis Agent\n"
  printf " 3) Exit\n"
  choice="$(prompt_input "Enter a number: ")"
  case "$choice" in
    1) server_menu ;;
    2) agent_menu ;;
    3) exit 0 ;;
    *) ui_error "Invalid selection. Exiting."; exit 1 ;;
  esac
}

# ---- Flag validation parity ----
if [[ $SERVER_FLAG -eq 1 && $AGENT_FLAG -eq 1 ]]; then
  ui_error "Cannot use --server and --agent together."
  exit 1
fi

if [[ $VITE_FLAG -eq 1 && $FLASK_FLAG -eq 1 ]]; then
  ui_error "Cannot combine --vite and --flask."
  exit 1
fi

if [[ $ENGINE_PROD_FLAG -eq 1 && $ENGINE_DEV_FLAG -eq 1 ]]; then
  ui_error "Cannot combine --engine-production and --engine-dev."
  exit 1
fi

if [[ ($ENGINE_PROD_FLAG -eq 1 || $ENGINE_DEV_FLAG -eq 1) && ($SERVER_FLAG -eq 1 || $AGENT_FLAG -eq 1) ]]; then
  ui_error "Engine automation switches cannot be combined with --server or --agent."
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

if [[ -z "${CHOICE}" ]] && ! is_interactive_terminal; then
  ui_error "No launcher mode selected and no interactive terminal is available. Use --EngineProduction, --EngineDev, --server, or --agent."
  exit 1
fi

show_borealis_banner

if [[ -n "${CHOICE}" ]]; then
  case "${CHOICE}" in
    1) server_menu "${ENGINE_MODE_CHOICE}" ; exit $? ;;
    2) agent_menu ; exit $? ;;
    *) ui_error "Invalid selection. Exiting." ; exit 1 ;;
  esac
fi

# Default to interactive menu
main_menu
