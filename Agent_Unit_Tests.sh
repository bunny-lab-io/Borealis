#!/usr/bin/env bash
# ======================================================
# Agent_Unit_Tests.sh
# Description: Runs Borealis Agent unit tests on Linux/macOS shells.
#
# API Endpoints (if applicable): None
# ======================================================

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="${BOREALIS_PROJECT_ROOT:-${SCRIPT_DIR}}"
TIMESTAMP="$(date -u +"%Y%m%dT%H%M%SZ")"
RESULT_DIR="${BOREALIS_UNIT_TEST_RESULTS_DIR:-${PROJECT_ROOT}/Unit_Test_Results/agent-${TIMESTAMP}}"
PYTHON_TIMEOUT_SECONDS="${BOREALIS_AGENT_UNIT_TEST_TIMEOUT_SECONDS:-900}"
REQUESTED_DOMAIN="${BOREALIS_AGENT_UNIT_TEST_DOMAIN:-all}"
LIST_DOMAINS=0

usage() {
  cat <<'USAGE'
Usage: ./Agent_Unit_Tests.sh [--domain DOMAIN] [--list-domains]

Runs all Agent Python unit tests.

Default domain:
  all

Examples:
  ./Agent_Unit_Tests.sh
  ./Agent_Unit_Tests.sh --domain roles
  ./Agent_Unit_Tests.sh --domain wireguard
  ./Agent_Unit_Tests.sh --list-domains

Environment overrides:
  BOREALIS_PROJECT_ROOT
  BOREALIS_AGENT_TEST_PYTHON
  BOREALIS_AGENT_UNIT_TEST_DOMAIN
  BOREALIS_UNIT_TEST_RESULTS_DIR
  BOREALIS_AGENT_UNIT_TEST_TIMEOUT_SECONDS
USAGE
}

print_domains() {
  cat <<'DOMAINS'
all
device-audit
file-management
heartbeat
remote-shell
roles
runtime
scripts
software
tokens
tray
updates
vnc
wireguard
DOMAINS
}

valid_domain() {
  local domain="$1"
  case "$domain" in
    all|device-audit|file-management|heartbeat|remote-shell|roles|runtime|scripts|software|tokens|tray|updates|vnc|wireguard)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --list-domains)
      LIST_DOMAINS=1
      shift
      ;;
    -d|--domain)
      if [[ "$#" -lt 2 ]]; then
        echo "--domain requires a value." >&2
        usage >&2
        exit 2
      fi
      REQUESTED_DOMAIN="$2"
      shift 2
      ;;
    --domain=*)
      REQUESTED_DOMAIN="${1#*=}"
      shift
      ;;
    *)
      echo "Unsupported arguments: $*" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "$LIST_DOMAINS" -eq 1 ]]; then
  print_domains
  exit 0
fi

if ! valid_domain "$REQUESTED_DOMAIN"; then
  echo "Unknown Agent test domain: ${REQUESTED_DOMAIN}" >&2
  echo "Available domains:" >&2
  print_domains >&2
  exit 2
fi

mkdir -p "$RESULT_DIR"

python_has_pytest() {
  local candidate="$1"
  env PYTHONDONTWRITEBYTECODE=1 "$candidate" -c 'import pytest' >/dev/null 2>&1
}

resolve_python() {
  local candidate
  for candidate in \
    "${BOREALIS_AGENT_TEST_PYTHON:-}" \
    "${PROJECT_ROOT}/Agent/bin/python3" \
    "${PROJECT_ROOT}/Agent/bin/python" \
    "${PROJECT_ROOT}/Engine/bin/python3" \
    "${PROJECT_ROOT}/Engine/bin/python" \
    "$(command -v python3 2>/dev/null || true)" \
    "$(command -v python 2>/dev/null || true)"; do
    if [[ -n "$candidate" && -x "$candidate" ]] && python_has_pytest "$candidate"; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}

PYTHON_BIN="$(resolve_python)" || {
  echo "Python executable with pytest not found. Install pytest or set BOREALIS_AGENT_TEST_PYTHON." >&2
  exit 2
}

emit_existing_paths() {
  local path
  for path in "$@"; do
    if [[ -e "${PROJECT_ROOT}/${path}" ]]; then
      echo "$path"
    fi
  done
}

agent_python_targets_for_domain() {
  local domain="$1"
  local test_root="Data/Agent/Unit_Tests"

  case "$domain" in
    all)
      emit_existing_paths "$test_root"
      ;;
    device-audit)
      emit_existing_paths "${test_root}/test_role_device_audit.py"
      ;;
    file-management)
      emit_existing_paths "${test_root}/test_role_system_file_management.py"
      ;;
    heartbeat)
      emit_existing_paths "${test_root}/test_role_system_heartbeat.py"
      ;;
    remote-shell)
      emit_existing_paths "${test_root}/test_role_remote_shell.py"
      ;;
    roles)
      emit_existing_paths \
        "${test_root}/test_role_device_audit.py" \
        "${test_root}/test_role_remote_shell.py" \
        "${test_root}/test_role_script_exec_currentuser.py" \
        "${test_root}/test_role_script_exec_system.py" \
        "${test_root}/test_role_system_file_management.py" \
        "${test_root}/test_role_system_heartbeat.py" \
        "${test_root}/test_role_system_software_management.py" \
        "${test_root}/test_role_vnc.py" \
        "${test_root}/test_role_wireguard_tunnel.py" \
        "${test_root}/test_system_script_execution.py" \
        "${test_root}/test_system_software_management.py"
      ;;
    runtime)
      emit_existing_paths \
        "${test_root}/test_agent_runtime_copy.py" \
        "${test_root}/test_agent_socket_supervisor.py" \
        "${test_root}/test_runtime_paths.py" \
        "${test_root}/test_session_runtime.py"
      ;;
    scripts)
      emit_existing_paths \
        "${test_root}/test_role_script_exec_currentuser.py" \
        "${test_root}/test_role_script_exec_system.py" \
        "${test_root}/test_system_script_execution.py"
      ;;
    software)
      emit_existing_paths \
        "${test_root}/test_role_system_software_management.py" \
        "${test_root}/test_system_software_management.py"
      ;;
    tokens)
      emit_existing_paths \
        "${test_root}/test_refresh_token_storage.py" \
        "${test_root}/test_token_refresh.py"
      ;;
    tray)
      emit_existing_paths \
        "${test_root}/test_agent_tray_restart.py" \
        "${test_root}/test_tray_state.py"
      ;;
    updates)
      emit_existing_paths "${test_root}/test_update_helper.py"
      ;;
    vnc)
      emit_existing_paths "${test_root}/test_role_vnc.py"
      ;;
    wireguard)
      emit_existing_paths "${test_root}/test_role_wireguard_tunnel.py"
      ;;
  esac
}

PYTEST_LOG="${RESULT_DIR}/agent-pytest.log"
PYTEST_XML="${RESULT_DIR}/agent-pytest.xml"
SUMMARY_PATH="${RESULT_DIR}/summary.txt"
PYTEST_TARGETS=()
while IFS= read -r target; do
  [[ -n "$target" ]] || continue
  PYTEST_TARGETS+=("$target")
done < <(agent_python_targets_for_domain "$REQUESTED_DOMAIN")

if [[ "${#PYTEST_TARGETS[@]}" -eq 0 ]]; then
  echo "No Agent Python unit tests found for domain ${REQUESTED_DOMAIN}." >&2
  exit 2
fi

echo "==> Agent Python unit tests (${REQUESTED_DOMAIN})"
if command -v timeout >/dev/null 2>&1; then
  timeout "$PYTHON_TIMEOUT_SECONDS" \
    env PYTHONDONTWRITEBYTECODE=1 BOREALIS_PROJECT_ROOT="$PROJECT_ROOT" \
      "$PYTHON_BIN" -m pytest -q "${PYTEST_TARGETS[@]}" --junitxml "$PYTEST_XML" \
      >"$PYTEST_LOG" 2>&1
else
  env PYTHONDONTWRITEBYTECODE=1 BOREALIS_PROJECT_ROOT="$PROJECT_ROOT" \
    "$PYTHON_BIN" -m pytest -q "${PYTEST_TARGETS[@]}" --junitxml "$PYTEST_XML" \
    >"$PYTEST_LOG" 2>&1
fi
status=$?

if [[ "$status" -ne 0 ]]; then
  echo "Agent Python unit tests failed with status ${status}. Log: ${PYTEST_LOG}" >&2
  tail -n 60 "$PYTEST_LOG" >&2 || true
else
  echo "Agent Python unit tests passed. Log: ${PYTEST_LOG}"
fi

{
  echo "Borealis Agent unit test run"
  echo "Domain: ${REQUESTED_DOMAIN}"
  echo "Results: ${RESULT_DIR}"
  echo "Python status: ${status}"
  echo "Overall status: ${status}"
} >"$SUMMARY_PATH"

echo "Results written to ${RESULT_DIR}"
exit "$status"
