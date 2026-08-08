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
GO_TIMEOUT_SECONDS="${BOREALIS_ENGINE_GO_TEST_TIMEOUT_SECONDS:-900}"
WEBUI_TIMEOUT_SECONDS="${BOREALIS_WEBUI_UNIT_TEST_TIMEOUT_SECONDS:-240}"
REQUESTED_DOMAIN="${BOREALIS_ENGINE_UNIT_TEST_DOMAIN:-all}"
LIST_DOMAINS=0

usage() {
  cat <<'USAGE'
Usage: ./Engine_Unit_Tests.sh [--domain DOMAIN] [--list-domains]

Runs Engine Go API tests, retained Engine Python unit tests, and the staged Engine WebUI unit tests.

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
  BOREALIS_ENGINE_TEST_GO
  BOREALIS_ENGINE_UNIT_TEST_DOMAIN
  BOREALIS_UNIT_TEST_RESULTS_DIR
  BOREALIS_ENGINE_UNIT_TEST_TIMEOUT_SECONDS
  BOREALIS_ENGINE_UNIT_TEST_FILE_TIMEOUT_SECONDS
  BOREALIS_ENGINE_GO_TEST_TIMEOUT_SECONDS
  BOREALIS_WEBUI_UNIT_TEST_TIMEOUT_SECONDS
USAGE
}

print_domains() {
  cat <<'DOMAINS'
all
access
ansible
assemblies
core
devices
enrollment
files
metadata
rbac
remote-access
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
    all|access|ansible|assemblies|core|devices|enrollment|files|metadata|rbac|remote-access|scheduler|server|watchdogs|webui|workflows)
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

go_version_ok() {
  local version="$1"
  local major minor
  major="${version%%.*}"
  minor="${version#*.}"
  minor="${minor%%.*}"
  [[ "$major" =~ ^[0-9]+$ ]] || major=0
  [[ "$minor" =~ ^[0-9]+$ ]] || minor=0
  [[ "$major" -gt 1 || ( "$major" -eq 1 && "$minor" -ge 25 ) ]]
}

installed_go_version() {
  "$1" version 2>/dev/null | sed -n 's/.* go\([0-9][0-9.]*\).*/\1/p'
}

resolve_go() {
  local candidate version
  for candidate in \
    "${BOREALIS_ENGINE_TEST_GO:-}" \
    "${PROJECT_ROOT}/Dependencies/Go/go1.25.12/bin/go" \
    "$(find "${PROJECT_ROOT}/Dependencies/Go" -path '*/bin/go' -type f -perm -u+x 2>/dev/null | sort | tail -n 1 || true)" \
    "$(command -v go 2>/dev/null || true)"; do
    if [[ -n "$candidate" && -x "$candidate" ]]; then
      version="$(installed_go_version "$candidate")"
      if go_version_ok "${version:-0.0}"; then
        echo "$candidate"
        return 0
      fi
    fi
  done
  return 1
}

PYTHON_BIN=""

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
        "${test_root}/test_database_schema.py" \
        "${test_root}/test_edge_runtime.py" \
        "${test_root}/test_engine_secret_config.py"
      ;;
    devices)
      return 0
      ;;
    enrollment)
      return 0
      ;;
    files)
      emit_existing_paths \
        "${test_root}/test_file_management_api.py"
      ;;
    metadata)
      emit_existing_paths \
        "${test_root}/test_metadata_fields.py"
      ;;
    rbac)
      return 0
      ;;
    remote-access)
      emit_existing_paths \
        "${test_root}/test_guacamole_proxy.py" \
        "${test_root}/test_site_worker_socket.py" \
        "${test_root}/test_vnc_proxy.py" \
        "${test_root}/test_vpn_shell.py" \
        "${test_root}/test_wireguard_control_server.py"
      ;;
    scheduler)
      emit_existing_paths \
        "${test_root}/test_job_scheduler_queue.py"
      ;;
    server)
      return 0
      ;;
    watchdogs)
      return 0
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

engine_go_regex_for_domain() {
  local domain="$1"
  case "$domain" in
    all)
      echo ""
      ;;
    access)
      echo 'Test(Aegis|Auth|Bootstrap|RequireUser|Passkey|Users|UserSubtree|OwnMFA|OwnPassword|Directory|Credential|Github|DPoP|TokenVerifier|SiteAssign|UserSiteAssignment)'
      ;;
    ansible)
      echo 'Test(ApplyScheduledSSH|AnsibleSSH|NormalizeSSH|ScheduledSSH)'
      ;;
    assemblies)
      echo 'TestAssembly'
      ;;
    core)
      echo 'Test(Activity|APIBackground|CurrentTimezone|EnvDuration|Health|HostValidation|InputValidation|Notification|OperatorRealtime|ProcessMode|PublicInputValidation|ReadJSON|RunGoAPI|SanitizeNotification|SerializeServerTime|ServerTime|SetEnv|Status|WriteSSE)'
      ;;
    devices)
      echo 'Test(AgentDetails|AgentHeartbeat|AgentMetadata|AgentSoftware|AgentStatus|Device|NormalizeAgentPatch|Patch|RemoteRegistry|Software)'
      ;;
    enrollment)
      echo 'Test(AgentEnrollment|AgentHash|AgentInstall|AgentRelease|AgentScript|AgentUpdate|AttachAgentInstall|RepoHash|ResolveEffectiveAgentRelease|SiteAgentInstall)'
      ;;
    files)
      echo 'TestRemoteFile'
      ;;
    metadata)
      echo 'Test(AgentMetadata|BuildMetadata|DeviceMetadata|Metadata)'
      ;;
    rbac)
      echo 'Test(DeviceFilterSiteMode|DirectorySites|FilterUsageTargetsFitScope|UserSiteAssignment|Users|UserSubtree)'
      ;;
    remote-access)
      echo 'Test(AgentVPNTunnel|ProbeRFB|RemoteShell|VNC|VPN|WireGuard)'
      ;;
    scheduler)
      echo 'Test(AgentMaintenance|AnsibleSSH|ApplyScheduled|DurationForOperator|InternalScheduler|NormalizeSSH|Onboarding|QuickRun|Scheduled|Scheduler)'
      ;;
    server)
      echo 'Test(AttachWorkerContainerMetadata|BorealisOperator|CollectOverview|DockerContainerStats|DockerInspect|EngineBackup|FilterScheduledDispatchWork|MergeK3sSiteWorker|NormalizeDockerContainerStats|Overview|ScheduledRunWorkPayload|Server|SiteWorkerName|ValidateSiteWorker)'
      ;;
    watchdogs)
      echo 'TestWatchdog'
      ;;
    workflows)
      echo 'Test(InternalWorkflow|Workflow)'
      ;;
    webui)
      return 1
      ;;
    *)
      return 1
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

run_engine_go_tests() {
  local api_backend_root="${PROJECT_ROOT}/Data/Engine/Containers/api-backend"
  local regex="${1:-}"

  if [[ ! -d "$api_backend_root" ]]; then
    echo "Engine Go API root missing: ${api_backend_root}" >"$ENGINE_GO_LOG"
    return 2
  fi

  local go_bin
  go_bin="$(resolve_go)" || {
    echo "Go 1.25+ executable not found. Set BOREALIS_ENGINE_TEST_GO or install Dependencies/Go/go1.25.12." >"$ENGINE_GO_LOG"
    return 2
  }

  if [[ -n "$regex" ]]; then
    run_with_timeout \
      "Engine Go API tests" \
      "$GO_TIMEOUT_SECONDS" \
      "$ENGINE_GO_LOG" \
      bash -c 'cd "$1" && export GOFLAGS="${GOFLAGS:+${GOFLAGS} }-mod=readonly" && "$2" test ./cmd/api-backend -run "$3"' \
        _ "$api_backend_root" "$go_bin" "$regex"
  else
    run_with_timeout \
      "Engine Go API tests" \
      "$GO_TIMEOUT_SECONDS" \
      "$ENGINE_GO_LOG" \
      bash -c 'cd "$1" && export GOFLAGS="${GOFLAGS:+${GOFLAGS} }-mod=readonly" && "$2" test ./cmd/api-backend' \
        _ "$api_backend_root" "$go_bin"
  fi
}

ENGINE_PYTEST_LOG="${RESULT_DIR}/engine-pytest.log"
ENGINE_PYTEST_XML="${RESULT_DIR}/engine-pytest.xml"
ENGINE_GO_LOG="${RESULT_DIR}/engine-go-api.log"
WEBUI_LOG="${RESULT_DIR}/engine-webui-vitest.log"
WEBUI_XML="${RESULT_DIR}/engine-webui-vitest.xml"
SUMMARY_PATH="${RESULT_DIR}/summary.txt"

overall_status=0
python_status="skipped"
go_status="skipped"
webui_status="skipped"

if [[ "$REQUESTED_DOMAIN" == "webui" ]]; then
  echo "==> Engine Python unit tests skipped for webui domain"
  echo "Engine Python unit tests skipped for webui domain." >"$ENGINE_PYTEST_LOG"
  echo "Engine Python unit tests skipped for webui domain." >"${RESULT_DIR}/engine-pytest-runner.log"
elif [[ -z "$(engine_python_files_for_domain "$REQUESTED_DOMAIN")" ]]; then
  echo "==> Engine Python unit tests skipped for ${REQUESTED_DOMAIN} domain"
  echo "No Engine Python unit tests mapped for domain ${REQUESTED_DOMAIN}; Go API lane carries current coverage where available." >"$ENGINE_PYTEST_LOG"
  echo "No Engine Python unit tests mapped for domain ${REQUESTED_DOMAIN}; Go API lane carries current coverage where available." >"${RESULT_DIR}/engine-pytest-runner.log"
else
  PYTHON_BIN="$(resolve_python)" || {
    echo "Python executable not found." >&2
    exit 2
  }
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

if [[ "$REQUESTED_DOMAIN" == "webui" ]]; then
  echo "==> Engine Go API tests skipped for webui domain"
  echo "Engine Go API tests skipped for webui domain." >"$ENGINE_GO_LOG"
else
  go_regex=""
  if go_regex="$(engine_go_regex_for_domain "$REQUESTED_DOMAIN")"; then
    if [[ "$REQUESTED_DOMAIN" != "all" && -z "$go_regex" ]]; then
      echo "==> Engine Go API tests skipped for ${REQUESTED_DOMAIN} domain"
      echo "No Engine Go API tests mapped for domain ${REQUESTED_DOMAIN}." >"$ENGINE_GO_LOG"
    else
      run_engine_go_tests "$go_regex"
      go_status=$?
      if [[ "$go_status" -ne 0 ]]; then
        overall_status="$go_status"
      fi
    fi
  else
    echo "==> Engine Go API tests skipped for ${REQUESTED_DOMAIN} domain"
    echo "No Engine Go API tests mapped for domain ${REQUESTED_DOMAIN}." >"$ENGINE_GO_LOG"
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
  if [[ "$REQUESTED_DOMAIN" == "webui" ]]; then
    echo "Engine WebUI unit tests failed with status 2. Log: ${WEBUI_LOG}" >&2
    cat "$WEBUI_LOG" >&2
    webui_status=2
    overall_status=2
  else
    echo "Engine WebUI unit tests skipped; runtime cache missing. Log: ${WEBUI_LOG}"
  fi
elif [[ ! -d "${WEBUI_RUNTIME}/node_modules" ]]; then
  {
    echo "Engine WebUI runtime dependencies missing at ${WEBUI_RUNTIME}/node_modules."
    echo "Redeploy or install WebUI runtime dependencies, then rerun this script."
  } >"$WEBUI_LOG"
  if [[ "$REQUESTED_DOMAIN" == "webui" ]]; then
    echo "Engine WebUI unit tests failed with status 2. Log: ${WEBUI_LOG}" >&2
    cat "$WEBUI_LOG" >&2
    webui_status=2
    overall_status=2
  else
    echo "Engine WebUI unit tests skipped; runtime dependencies missing. Log: ${WEBUI_LOG}"
  fi
elif [[ -z "$NPM_BIN" ]]; then
  {
    echo "npm not found on PATH and portable NodeJS npm was not found under Dependencies/NodeJS/bin."
  } >"$WEBUI_LOG"
  if [[ "$REQUESTED_DOMAIN" == "webui" ]]; then
    echo "Engine WebUI unit tests failed with status 2. Log: ${WEBUI_LOG}" >&2
    cat "$WEBUI_LOG" >&2
    webui_status=2
    overall_status=2
  else
    echo "Engine WebUI unit tests skipped; npm missing. Log: ${WEBUI_LOG}"
  fi
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
  echo "Go API status: ${go_status}"
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
