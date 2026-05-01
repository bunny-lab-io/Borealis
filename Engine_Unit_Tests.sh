#!/usr/bin/env bash
# ======================================================
# Engine_Unit_Tests.sh
# Description: Runs Borealis Engine unit tests.
#
# API Endpoints (if applicable): None
# ======================================================

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="${BOREALIS_PROJECT_ROOT:-${SCRIPT_DIR}}"
TIMESTAMP="$(date -u +"%Y%m%dT%H%M%SZ")"
RESULT_DIR="${BOREALIS_UNIT_TEST_RESULTS_DIR:-${PROJECT_ROOT}/Unit_Test_Results/engine-${TIMESTAMP}}"
PYTHON_TIMEOUT_SECONDS="${BOREALIS_ENGINE_UNIT_TEST_TIMEOUT_SECONDS:-3600}"
PYTHON_FILE_TIMEOUT_SECONDS="${BOREALIS_ENGINE_UNIT_TEST_FILE_TIMEOUT_SECONDS:-900}"
WEBUI_TIMEOUT_SECONDS="${BOREALIS_WEBUI_UNIT_TEST_TIMEOUT_SECONDS:-240}"
REQUESTED_DOMAIN="${BOREALIS_ENGINE_UNIT_TEST_DOMAIN:-all}"
LIST_DOMAINS=0

usage() {
  cat <<'USAGE'
Usage: ./Engine_Unit_Tests.sh [--domain DOMAIN] [--list-domains]

Runs all Engine Python unit tests and the staged Engine WebUI unit tests.

Default domain:
  all

Examples:
  ./Engine_Unit_Tests.sh
  ./Engine_Unit_Tests.sh --domain devices
  ./Engine_Unit_Tests.sh --domain webui
  ./Engine_Unit_Tests.sh --list-domains

Environment overrides:
  BOREALIS_PROJECT_ROOT
  BOREALIS_ENGINE_TEST_PYTHON
  BOREALIS_ENGINE_UNIT_TEST_DOMAIN
  BOREALIS_UNIT_TEST_RESULTS_DIR
  BOREALIS_ENGINE_UNIT_TEST_TIMEOUT_SECONDS
  BOREALIS_ENGINE_UNIT_TEST_FILE_TIMEOUT_SECONDS
  BOREALIS_WEBUI_UNIT_TEST_TIMEOUT_SECONDS
USAGE
}

print_domains() {
  cat <<'DOMAINS'
all
access
agent-role
ansible
assemblies
core
devices
enrollment
files
rbac
remote-access
runtime-overrides
scheduler
server
watchdogs
webui
workflows
DOMAINS
}

valid_domain() {
  local domain="$1"
  case "$domain" in
    all|access|agent-role|ansible|assemblies|core|devices|enrollment|files|rbac|remote-access|runtime-overrides|scheduler|server|watchdogs|webui|workflows)
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
  echo "Unknown Engine test domain: ${REQUESTED_DOMAIN}" >&2
  echo "Available domains:" >&2
  print_domains >&2
  exit 2
fi

mkdir -p "$RESULT_DIR"

resolve_python() {
  local candidate
  for candidate in \
    "${BOREALIS_ENGINE_TEST_PYTHON:-}" \
    "${PROJECT_ROOT}/Engine/bin/python3" \
    "${PROJECT_ROOT}/Engine/bin/python" \
    "$(command -v python3 2>/dev/null || true)" \
    "$(command -v python 2>/dev/null || true)"; do
    if [[ -n "$candidate" && -x "$candidate" ]]; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}

run_with_timeout() {
  local label="$1"
  local timeout_seconds="$2"
  local log_path="$3"
  shift 3

  echo "==> ${label}"
  if command -v timeout >/dev/null 2>&1; then
    timeout "$timeout_seconds" "$@" >"$log_path" 2>&1
  else
    "$@" >"$log_path" 2>&1
  fi
  local status=$?

  if [[ "$status" -ne 0 ]]; then
    echo "${label} failed with status ${status}. Log: ${log_path}" >&2
    tail -n 60 "$log_path" >&2 || true
  else
    echo "${label} passed. Log: ${log_path}"
  fi

  return "$status"
}

PYTHON_BIN=""
if [[ "$REQUESTED_DOMAIN" != "webui" ]]; then
  PYTHON_BIN="$(resolve_python)" || {
    echo "Python executable not found." >&2
    exit 2
  }
fi

emit_existing_paths() {
  local path
  for path in "$@"; do
    if [[ -e "${PROJECT_ROOT}/${path}" ]]; then
      echo "$path"
    fi
  done
}

engine_python_files_for_domain() {
  local domain="$1"
  local test_root="Data/Engine/Unit_Tests"

  case "$domain" in
    all)
      if [[ -d "${PROJECT_ROOT}/${test_root}" ]]; then
        cd "$PROJECT_ROOT" && find "$test_root" -type f \( -name 'test_*.py' -o -name '*_test.py' \) | sort
      fi
      ;;
    access)
      emit_existing_paths \
        "${test_root}/test_access_management_api.py"
      ;;
    agent-role)
      emit_existing_paths \
        "${test_root}/test_agent_role_health.py" \
        "${test_root}/test_agent_role_manager.py"
      ;;
    ansible)
      emit_existing_paths \
        "${test_root}/test_ansible_runner.py"
      ;;
    assemblies)
      if [[ -d "${PROJECT_ROOT}/${test_root}/assemblies" ]]; then
        cd "$PROJECT_ROOT" && find "${test_root}/assemblies" -type f \( -name 'test_*.py' -o -name '*_test.py' \) | sort
      fi
      ;;
    core)
      emit_existing_paths \
        "${test_root}/test_core_api.py" \
        "${test_root}/test_edge_runtime.py" \
        "${test_root}/test_engine_secret_config.py" \
        "${test_root}/test_web_ui.py"
      ;;
    devices)
      emit_existing_paths \
        "${test_root}/test_device_filters_api.py" \
        "${test_root}/test_device_purge_api.py" \
        "${test_root}/test_devices_api.py" \
        "${test_root}/test_session_inventory.py"
      ;;
    enrollment)
      emit_existing_paths \
        "${test_root}/test_enrollment_api.py" \
        "${test_root}/test_tokens_api.py"
      ;;
    files)
      emit_existing_paths \
        "${test_root}/test_file_management_api.py"
      ;;
    rbac)
      emit_existing_paths \
        "${test_root}/test_rbac_api.py"
      ;;
    remote-access)
      emit_existing_paths \
        "${test_root}/test_guacamole_proxy.py" \
        "${test_root}/test_vnc_api.py" \
        "${test_root}/test_vnc_proxy.py" \
        "${test_root}/test_vnc_sessions.py" \
        "${test_root}/test_vpn_shell.py" \
        "${test_root}/test_vpn_tunnel_service.py" \
        "${test_root}/test_websocket_registry.py" \
        "${test_root}/test_wireguard_server.py"
      ;;
    runtime-overrides)
      emit_existing_paths \
        "${test_root}/test_runtime_override_merge.py"
      ;;
    scheduler)
      emit_existing_paths \
        "${test_root}/test_scheduled_jobs_api.py" \
        "${test_root}/test_scheduler_timing.py"
      ;;
    server)
      emit_existing_paths \
        "${test_root}/test_server_info_api.py"
      ;;
    watchdogs)
      emit_existing_paths \
        "${test_root}/test_watchdogs_api.py"
      ;;
    webui)
      return 0
      ;;
    workflows)
      emit_existing_paths \
        "${test_root}/test_workflow_runtime.py"
      ;;
  esac
}

run_engine_python_tests() {
  local test_root="Data/Engine/Unit_Tests"
  local junit_dir="${RESULT_DIR}/engine-pytest-junit"
  local lane_status=0
  local total_count=0
  local failed_count=0
  local passed_count=0

  mkdir -p "$junit_dir"
  : >"$ENGINE_PYTEST_LOG"

  if [[ ! -d "${PROJECT_ROOT}/${test_root}" ]]; then
    echo "Engine Python unit test root missing: ${PROJECT_ROOT}/${test_root}" >"$ENGINE_PYTEST_LOG"
    return 2
  fi

  while IFS= read -r test_file; do
    [[ -n "$test_file" ]] || continue
    total_count=$((total_count + 1))
    local safe_name="${test_file//\//__}"
    safe_name="${safe_name// /_}"
    local junit_path="${junit_dir}/${safe_name}.xml"

    {
      echo "==> ${test_file}"
    } >>"$ENGINE_PYTEST_LOG"

    if command -v timeout >/dev/null 2>&1; then
      timeout "$PYTHON_FILE_TIMEOUT_SECONDS" \
        env PYTHONDONTWRITEBYTECODE=1 BOREALIS_PROJECT_ROOT="$PROJECT_ROOT" \
          "$PYTHON_BIN" -m pytest -q "$test_file" --junitxml "$junit_path" \
          >>"$ENGINE_PYTEST_LOG" 2>&1
    else
      env PYTHONDONTWRITEBYTECODE=1 BOREALIS_PROJECT_ROOT="$PROJECT_ROOT" \
        "$PYTHON_BIN" -m pytest -q "$test_file" --junitxml "$junit_path" \
        >>"$ENGINE_PYTEST_LOG" 2>&1
    fi
    local file_status=$?

    if [[ "$file_status" -ne 0 ]]; then
      failed_count=$((failed_count + 1))
      lane_status="$file_status"
      echo "FAILED ${test_file} status=${file_status}" >>"$ENGINE_PYTEST_LOG"
    else
      passed_count=$((passed_count + 1))
      echo "PASSED ${test_file}" >>"$ENGINE_PYTEST_LOG"
    fi
    echo >>"$ENGINE_PYTEST_LOG"
  done < <(engine_python_files_for_domain "$REQUESTED_DOMAIN")

  if [[ "$total_count" -eq 0 ]]; then
    if [[ "$REQUESTED_DOMAIN" == "webui" ]]; then
      echo "Engine Python unit tests skipped for webui domain." >>"$ENGINE_PYTEST_LOG"
      lane_status=0
    else
      echo "No Engine Python unit tests found for domain ${REQUESTED_DOMAIN} under ${test_root}." >>"$ENGINE_PYTEST_LOG"
      lane_status=2
    fi
  fi

  {
    echo '<?xml version="1.0" encoding="UTF-8"?>'
    echo "<testsuite name=\"engine-python-files\" tests=\"${total_count}\" failures=\"${failed_count}\" errors=\"0\" skipped=\"0\">"
    echo "  <properties>"
    echo "    <property name=\"passed_files\" value=\"${passed_count}\" />"
    echo "    <property name=\"failed_files\" value=\"${failed_count}\" />"
    echo "  </properties>"
    echo "</testsuite>"
  } >"$ENGINE_PYTEST_XML"

  return "$lane_status"
}

ENGINE_PYTEST_LOG="${RESULT_DIR}/engine-pytest.log"
ENGINE_PYTEST_XML="${RESULT_DIR}/engine-pytest.xml"
WEBUI_LOG="${RESULT_DIR}/engine-webui-vitest.log"
WEBUI_XML="${RESULT_DIR}/engine-webui-vitest.xml"
SUMMARY_PATH="${RESULT_DIR}/summary.txt"

overall_status=0
python_status="skipped"
webui_status="skipped"

if [[ "$REQUESTED_DOMAIN" == "webui" ]]; then
  echo "==> Engine Python unit tests skipped for webui domain"
  echo "Engine Python unit tests skipped for webui domain." >"$ENGINE_PYTEST_LOG"
  echo "Engine Python unit tests skipped for webui domain." >"${RESULT_DIR}/engine-pytest-runner.log"
else
  echo "==> Engine Python unit tests"
  export PROJECT_ROOT RESULT_DIR ENGINE_PYTEST_LOG ENGINE_PYTEST_XML PYTHON_FILE_TIMEOUT_SECONDS PYTHON_BIN REQUESTED_DOMAIN
  export -f run_engine_python_tests engine_python_files_for_domain emit_existing_paths
  if command -v timeout >/dev/null 2>&1; then
    timeout "$PYTHON_TIMEOUT_SECONDS" bash -c 'run_engine_python_tests' \
      >"${RESULT_DIR}/engine-pytest-runner.log" 2>&1
  else
    run_engine_python_tests >"${RESULT_DIR}/engine-pytest-runner.log" 2>&1
  fi
  python_status=$?
  if [[ "$python_status" -ne 0 ]]; then
    echo "Engine Python unit tests failed with status ${python_status}. Log: ${ENGINE_PYTEST_LOG}" >&2
    tail -n 80 "$ENGINE_PYTEST_LOG" >&2 || true
    overall_status="$python_status"
  else
    echo "Engine Python unit tests passed. Log: ${ENGINE_PYTEST_LOG}"
  fi
fi

WEBUI_RUNTIME="${PROJECT_ROOT}/Engine/Services/webui-frontend/cache/web-interface"
WEBUI_UNIT_TESTS="${WEBUI_RUNTIME}/Unit_Tests"
NODE_PATH_PREFIX=""
NPM_BIN="$(command -v npm 2>/dev/null || true)"
if [[ -z "$NPM_BIN" && -x "${PROJECT_ROOT}/Dependencies/NodeJS/bin/npm" ]]; then
  NODE_PATH_PREFIX="${PROJECT_ROOT}/Dependencies/NodeJS/bin"
  NPM_BIN="${PROJECT_ROOT}/Dependencies/NodeJS/bin/npm"
fi

if [[ "$REQUESTED_DOMAIN" != "all" && "$REQUESTED_DOMAIN" != "webui" ]]; then
  {
    echo "Engine WebUI unit tests skipped for domain ${REQUESTED_DOMAIN}."
  } >"$WEBUI_LOG"
elif [[ ! -d "$WEBUI_UNIT_TESTS" ]]; then
  {
    echo "Engine WebUI runtime unit tests missing at ${WEBUI_UNIT_TESTS}."
    echo "Redeploy the Engine so container-owned WebUI source is staged into Engine/Services/webui-frontend/cache/web-interface, then rerun this script."
  } >"$WEBUI_LOG"
  echo "Engine WebUI unit tests failed with status 2. Log: ${WEBUI_LOG}" >&2
  cat "$WEBUI_LOG" >&2
  webui_status=2
  overall_status=2
elif [[ ! -d "${WEBUI_RUNTIME}/node_modules" ]]; then
  {
    echo "Engine WebUI runtime dependencies missing at ${WEBUI_RUNTIME}/node_modules."
    echo "Redeploy or install WebUI runtime dependencies, then rerun this script."
  } >"$WEBUI_LOG"
  echo "Engine WebUI unit tests failed with status 2. Log: ${WEBUI_LOG}" >&2
  cat "$WEBUI_LOG" >&2
  webui_status=2
  overall_status=2
elif [[ -z "$NPM_BIN" ]]; then
  {
    echo "npm not found on PATH and portable NodeJS npm was not found under Dependencies/NodeJS/bin."
  } >"$WEBUI_LOG"
  echo "Engine WebUI unit tests failed with status 2. Log: ${WEBUI_LOG}" >&2
  cat "$WEBUI_LOG" >&2
  webui_status=2
  overall_status=2
else
  run_with_timeout \
    "Engine WebUI unit tests" \
    "$WEBUI_TIMEOUT_SECONDS" \
    "$WEBUI_LOG" \
    bash -c 'cd "$1" && PATH="$2:${PATH}" "$3" test -- --run --reporter=dot --reporter=junit --outputFile="$4"' \
      _ "$WEBUI_RUNTIME" "$NODE_PATH_PREFIX" "$NPM_BIN" "$WEBUI_XML"
  webui_status=$?
  if [[ "$webui_status" -ne 0 ]]; then
    overall_status="$webui_status"
  fi
fi

{
  echo "Borealis Engine unit test run"
  echo "Domain: ${REQUESTED_DOMAIN}"
  echo "Results: ${RESULT_DIR}"
  echo "Python status: ${python_status}"
  if [[ "$webui_status" == "skipped" ]]; then
    echo "WebUI status: skipped"
  elif [[ "$webui_status" -eq 0 ]]; then
    echo "WebUI status: 0"
  else
    echo "WebUI status: see ${WEBUI_LOG}"
  fi
  echo "Overall status: ${overall_status}"
} >"$SUMMARY_PATH"

echo "Results written to ${RESULT_DIR}"
exit "$overall_status"
