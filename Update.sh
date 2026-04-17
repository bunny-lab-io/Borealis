#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

DEFAULT_REPO_URL="https://github.com/bunny-lab-io/Borealis.git"
BOREALIS_AGENT_SYSTEMD_UNIT="${BOREALIS_AGENT_SYSTEMD_UNIT:-borealis-agent.service}"

if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then
  SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
else
  SCRIPT_DIR="$(pwd)"
fi
cd "${SCRIPT_DIR}"

LOG_FILE="${SCRIPT_DIR}/Updater.log"
LOG_FILE_READY="0"
XTRACE_FD_OPEN="0"

setup_logging() {
  local timestamp
  timestamp="$(date +%FT%T)"

  if ! : > "${LOG_FILE}" 2>/dev/null; then
    printf "[%s] [WARN] Unable to reset updater log at %s; continuing without file logging.\n" "${timestamp}" "${LOG_FILE}" >&2
    return 0
  fi

  exec 3>>"${LOG_FILE}"
  XTRACE_FD_OPEN="1"
  export BASH_XTRACEFD=3
  export PS4='+ [${BASH_SOURCE##*/}:${LINENO}] '

  exec > >(tee -a "${LOG_FILE}") 2>&1
  LOG_FILE_READY="1"

  printf "[%s] [STEP] ===== Starting Update.sh session =====\n" "${timestamp}"
  # Keep command traces in Updater.log only so console output stays readable.
  set -x
}

log_unhandled_error() {
  local exit_code=$?
  local line_no="${1:-unknown}"
  local command="${2:-unknown}"
  command="${command//$'\n'/ }"
  printf "[%s] [ERROR] Update.sh failed near line %s while running: %s\n" "$(date +%FT%T)" "${line_no}" "${command}"
  return "${exit_code}"
}

finish_logging() {
  local exit_code="${1:-0}"

  if [[ "${LOG_FILE_READY}" == "1" ]]; then
    set +x 2>/dev/null || true
    if [[ "${exit_code}" == "0" ]]; then
      printf "[%s] [SUCCESS] ===== Update.sh session finished successfully =====\n" "$(date +%FT%T)"
    else
      printf "[%s] [ERROR] ===== Update.sh session failed with exit code %s =====\n" "$(date +%FT%T)" "${exit_code}"
    fi
  fi

  if [[ "${XTRACE_FD_OPEN}" == "1" ]]; then
    exec 3>&-
    XTRACE_FD_OPEN="0"
  fi
}

setup_logging
trap 'log_unhandled_error "${LINENO}" "${BASH_COMMAND}"' ERR
trap 'finish_logging "$?"' EXIT

log_line() {
  local level="$1"
  local message="$2"
  printf "[%s] [%s] %s\n" "$(date +%FT%T)" "${level}" "${message}"
}

normalize_hash() {
  printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]' | tr -d '\r\n[:space:]'
}

resolve_agent_python() {
  local candidates=()
  if [[ -n "${BOREALIS_AGENT_VENV:-}" ]]; then
    candidates+=("${BOREALIS_AGENT_VENV}/bin/python3" "${BOREALIS_AGENT_VENV}/bin/python")
  fi
  candidates+=(
    "/opt/Borealis/Agent/bin/python3"
    "/opt/Borealis/Agent/bin/python"
    "${SCRIPT_DIR}/Agent/bin/python3"
    "${SCRIPT_DIR}/Agent/bin/python"
  )

  local candidate=""
  for candidate in "${candidates[@]}"; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done
  return 1
}

helper_script() {
  printf '%s\n' "${SCRIPT_DIR}/Data/Agent/update_helper.py"
}

run_helper() {
  local python_bin="$1"
  shift
  BOREALIS_PROJECT_ROOT="${SCRIPT_DIR}" "${python_bin}" "$(helper_script)" "$@"
}

json_bool_busy() {
  local python_bin="$1"
  printf '%s' "${2:-}" | "${python_bin}" -c 'import json,sys; data=json.load(sys.stdin); print("1" if data.get("busy") else "0")'
}

json_installed_hash() {
  local python_bin="$1"
  printf '%s' "${2:-}" | "${python_bin}" -c 'import json,sys; data=json.load(sys.stdin); print((data.get("installed_build_id") or "").strip())'
}

json_repo_hash() {
  local python_bin="$1"
  printf '%s' "${2:-}" | "${python_bin}" -c 'import json,sys; data=json.load(sys.stdin); print((data.get("repo_build_id") or "").strip())'
}

json_busy_reasons() {
  local python_bin="$1"
  printf '%s' "${2:-}" | "${python_bin}" -c 'import json,sys; data=json.load(sys.stdin); reasons=data.get("reasons") or []; print(", ".join(str(item).strip() for item in reasons if str(item).strip()))'
}

json_repo_target_hash() {
  local python_bin="$1"
  printf '%s' "${2:-}" | "${python_bin}" -c 'import json,sys; data=json.load(sys.stdin); print((data.get("sha") or "").strip())'
}

json_repo_target_branch() {
  local python_bin="$1"
  printf '%s' "${2:-}" | "${python_bin}" -c 'import json,sys; data=json.load(sys.stdin); print((data.get("branch") or "main").strip() or "main")'
}

resolve_repository_url() {
  local configured_repo_url="${BOREALIS_UPDATE_REPO_URL:-}"
  configured_repo_url="$(printf '%s' "${configured_repo_url}" | tr -d '\r\n')"
  if [[ -n "${configured_repo_url}" ]]; then
    printf '%s\n' "${configured_repo_url}"
    return 0
  fi

  local configured_repo="${BOREALIS_UPDATE_REPO:-}"
  configured_repo="$(printf '%s' "${configured_repo}" | tr -d '\r\n')"
  if [[ -n "${configured_repo}" ]]; then
    if [[ "${configured_repo}" == *"://"* || "${configured_repo}" == git@* ]]; then
      printf '%s\n' "${configured_repo}"
    else
      printf 'https://github.com/%s.git\n' "${configured_repo}"
    fi
    return 0
  fi

  if git -C "${SCRIPT_DIR}" config --get remote.origin.url >/dev/null 2>&1; then
    git -C "${SCRIPT_DIR}" config --get remote.origin.url
    return 0
  fi

  printf '%s\n' "${DEFAULT_REPO_URL}"
}

sync_repository() {
  local repo_url="$1"
  local target_hash="$2"
  local branch_name="$3"

  if [[ ! -e "${SCRIPT_DIR}/.git" ]]; then
    log_line "ERROR" "Project root is not a git checkout; automatic update cannot continue."
    return 1
  fi

  git -C "${SCRIPT_DIR}" remote set-url origin "${repo_url}" >/dev/null 2>&1 || \
    git -C "${SCRIPT_DIR}" remote add origin "${repo_url}" >/dev/null 2>&1

  if [[ -n "${branch_name}" ]]; then
    git -C "${SCRIPT_DIR}" fetch --force --prune origin "${branch_name}"
  else
    git -C "${SCRIPT_DIR}" fetch --force --prune origin
  fi

  if ! git -C "${SCRIPT_DIR}" rev-parse --verify --quiet "${target_hash}^{commit}" >/dev/null; then
    git -C "${SCRIPT_DIR}" fetch --force origin "${target_hash}"
  fi

  if [[ -n "${branch_name}" ]]; then
    git -C "${SCRIPT_DIR}" checkout --force -B "${branch_name}" "${target_hash}"
  else
    git -C "${SCRIPT_DIR}" checkout --force "${target_hash}"
  fi

  git -C "${SCRIPT_DIR}" reset --hard "${target_hash}"
  # Preserve ignored runtime directories such as Agent/, Engine/, and Dependencies/.
  git -C "${SCRIPT_DIR}" clean -fd
}

ensure_lock() {
  local lock_root="${SCRIPT_DIR}/Agent/Borealis/Settings/Updater"
  mkdir -p "${lock_root}"

  if command -v flock >/dev/null 2>&1; then
    exec 9>"${lock_root}/update.lock"
    if ! flock -n 9; then
      log_line "INFO" "Another Borealis agent update is already in progress."
      exit 0
    fi
    return 0
  fi

  local lock_dir="${lock_root}/update.lock.d"
  if ! mkdir "${lock_dir}" 2>/dev/null; then
    log_line "INFO" "Another Borealis agent update is already in progress."
    exit 0
  fi
  trap 'rmdir "${lock_dir}" >/dev/null 2>&1 || true' EXIT
}

main() {
  log_line "STEP" "Starting Borealis agent auto-update check."

  local python_bin=""
  python_bin="$(resolve_agent_python)" || {
    log_line "ERROR" "Agent runtime Python was not found."
    return 1
  }

  local helper
  helper="$(helper_script)"
  if [[ ! -f "${helper}" ]]; then
    log_line "ERROR" "Agent update helper is missing at ${helper}."
    return 1
  fi

  local status_json=""
  status_json="$(run_helper "${python_bin}" status)"
  local installed_hash=""
  local repo_hash=""
  local busy="0"
  local busy_reasons=""
  installed_hash="$(json_installed_hash "${python_bin}" "${status_json}")"
  repo_hash="$(json_repo_hash "${python_bin}" "${status_json}")"
  busy="$(json_bool_busy "${python_bin}" "${status_json}")"
  busy_reasons="$(json_busy_reasons "${python_bin}" "${status_json}")"

  local repo_info_json=""
  repo_info_json="$(run_helper "${python_bin}" repo-hash --refresh)"
  local target_hash=""
  local target_branch=""
  target_hash="$(json_repo_target_hash "${python_bin}" "${repo_info_json}")"
  target_branch="$(json_repo_target_branch "${python_bin}" "${repo_info_json}")"

  local normalized_installed=""
  local normalized_repo=""
  local normalized_target=""
  normalized_installed="$(normalize_hash "${installed_hash}")"
  normalized_repo="$(normalize_hash "${repo_hash}")"
  normalized_target="$(normalize_hash "${target_hash}")"

  if [[ -z "${normalized_target}" ]]; then
    log_line "ERROR" "Engine did not return a target repository hash."
    return 1
  fi

  local update_mode="${update_mode:-update}"
  update_mode="$(printf '%s' "${update_mode}" | tr '[:upper:]' '[:lower:]')"
  local force_update="0"
  if [[ "${update_mode}" == "force_update" ]]; then
    force_update="1"
  fi

  local runtime_needs_update="0"
  local repo_needs_sync="0"
  if [[ -z "${normalized_installed}" || "${normalized_installed}" != "${normalized_target}" ]]; then
    runtime_needs_update="1"
  fi
  if [[ -z "${normalized_repo}" || "${normalized_repo}" != "${normalized_target}" ]]; then
    repo_needs_sync="1"
  fi

  if [[ "${force_update}" != "1" && "${runtime_needs_update}" != "1" && "${repo_needs_sync}" != "1" ]]; then
    run_helper "${python_bin}" sync-build-id >/dev/null || true
    log_line "SUCCESS" "Borealis agent is already up to date."
    return 0
  fi

  if [[ "${busy}" == "1" ]]; then
    if [[ -z "${busy_reasons}" ]]; then
      busy_reasons="unspecified activity"
    fi
    log_line "WARN" "Agent update deferred because the device is busy: ${busy_reasons}"
    return 0
  fi

  ensure_lock

  status_json="$(run_helper "${python_bin}" status)"
  busy="$(json_bool_busy "${python_bin}" "${status_json}")"
  busy_reasons="$(json_busy_reasons "${python_bin}" "${status_json}")"
  if [[ "${busy}" == "1" ]]; then
    if [[ -z "${busy_reasons}" ]]; then
      busy_reasons="unspecified activity"
    fi
    log_line "WARN" "Agent update deferred after lock acquisition because the device is busy: ${busy_reasons}"
    return 0
  fi

  local repo_url=""
  repo_url="$(resolve_repository_url)"
  log_line "STEP" "Syncing Borealis repository to ${target_hash} on branch ${target_branch}."
  sync_repository "${repo_url}" "${target_hash}" "${target_branch}"

  chmod +x "${SCRIPT_DIR}/Borealis.sh" "${SCRIPT_DIR}/Update.sh" "${SCRIPT_DIR}/bootstrap.sh" >/dev/null 2>&1 || true

  log_line "STEP" "Refreshing Borealis agent runtime in place."
  "${SCRIPT_DIR}/Borealis.sh" --agent --refresh-agent-runtime

  local synced_build_id=""
  synced_build_id="$(run_helper "${python_bin}" sync-build-id || true)"
  if [[ -n "${synced_build_id}" ]]; then
    log_line "INFO" "Installed build id synced to ${synced_build_id}."
  fi

  status_json="$(run_helper "${python_bin}" status)"
  installed_hash="$(json_installed_hash "${python_bin}" "${status_json}")"
  normalized_installed="$(normalize_hash "${installed_hash}")"
  if [[ -n "${normalized_installed}" && "${normalized_installed}" != "${normalized_target}" ]]; then
    log_line "WARN" "Installed build id ${installed_hash} does not yet match target ${target_hash}."
  fi

  log_line "SUCCESS" "Borealis agent auto-update completed successfully."
}

main "$@"
