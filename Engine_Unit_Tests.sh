#!/usr/bin/env bash
# ======================================================
# Engine_Unit_Tests.sh
# Description: Runs the full Borealis Engine unit test lane.
#
# API Endpoints (if applicable): None
# ======================================================

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="${BOREALIS_PROJECT_ROOT:-${SCRIPT_DIR}}"
TIMESTAMP="$(date -u +"%Y%m%dT%H%M%SZ")"
RESULT_DIR="${BOREALIS_UNIT_TEST_RESULTS_DIR:-${PROJECT_ROOT}/Unit_Test_Results/engine-${TIMESTAMP}}"
PYTHON_TIMEOUT_SECONDS="${BOREALIS_ENGINE_UNIT_TEST_TIMEOUT_SECONDS:-900}"
WEBUI_TIMEOUT_SECONDS="${BOREALIS_WEBUI_UNIT_TEST_TIMEOUT_SECONDS:-240}"

usage() {
  cat <<'USAGE'
Usage: ./Engine_Unit_Tests.sh

Runs all Engine Python unit tests and the staged Engine WebUI unit tests.
No domain-level selection is supported.

Environment overrides:
  BOREALIS_PROJECT_ROOT
  BOREALIS_ENGINE_TEST_PYTHON
  BOREALIS_UNIT_TEST_RESULTS_DIR
  BOREALIS_ENGINE_UNIT_TEST_TIMEOUT_SECONDS
  BOREALIS_WEBUI_UNIT_TEST_TIMEOUT_SECONDS
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ "$#" -gt 0 ]]; then
  echo "Unsupported arguments: $*" >&2
  usage >&2
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

PYTHON_BIN="$(resolve_python)" || {
  echo "Python executable not found." >&2
  exit 2
}

ENGINE_PYTEST_LOG="${RESULT_DIR}/engine-pytest.log"
ENGINE_PYTEST_XML="${RESULT_DIR}/engine-pytest.xml"
WEBUI_LOG="${RESULT_DIR}/engine-webui-vitest.log"
WEBUI_XML="${RESULT_DIR}/engine-webui-vitest.xml"
SUMMARY_PATH="${RESULT_DIR}/summary.txt"

overall_status=0

run_with_timeout \
  "Engine Python unit tests" \
  "$PYTHON_TIMEOUT_SECONDS" \
  "$ENGINE_PYTEST_LOG" \
  env PYTHONDONTWRITEBYTECODE=1 BOREALIS_PROJECT_ROOT="$PROJECT_ROOT" \
    "$PYTHON_BIN" -m pytest -q Data/Engine/Unit_Tests --junitxml "$ENGINE_PYTEST_XML"
python_status=$?
if [[ "$python_status" -ne 0 ]]; then
  overall_status="$python_status"
fi

WEBUI_RUNTIME="${PROJECT_ROOT}/Engine/web-interface"
WEBUI_UNIT_TESTS="${WEBUI_RUNTIME}/Unit_Tests"
NODE_PATH_PREFIX=""
NPM_BIN="$(command -v npm 2>/dev/null || true)"
if [[ -z "$NPM_BIN" && -x "${PROJECT_ROOT}/Dependencies/NodeJS/bin/npm" ]]; then
  NODE_PATH_PREFIX="${PROJECT_ROOT}/Dependencies/NodeJS/bin"
  NPM_BIN="${PROJECT_ROOT}/Dependencies/NodeJS/bin/npm"
fi

if [[ ! -d "$WEBUI_UNIT_TESTS" ]]; then
  {
    echo "Engine WebUI runtime unit tests missing at ${WEBUI_UNIT_TESTS}."
    echo "Redeploy the Engine so Data/Engine/web-interface/Unit_Tests is staged into Engine/web-interface/Unit_Tests, then rerun this script."
  } >"$WEBUI_LOG"
  echo "Engine WebUI unit tests failed with status 2. Log: ${WEBUI_LOG}" >&2
  cat "$WEBUI_LOG" >&2
  overall_status=2
elif [[ ! -d "${WEBUI_RUNTIME}/node_modules" ]]; then
  {
    echo "Engine WebUI runtime dependencies missing at ${WEBUI_RUNTIME}/node_modules."
    echo "Redeploy or install WebUI runtime dependencies, then rerun this script."
  } >"$WEBUI_LOG"
  echo "Engine WebUI unit tests failed with status 2. Log: ${WEBUI_LOG}" >&2
  cat "$WEBUI_LOG" >&2
  overall_status=2
elif [[ -z "$NPM_BIN" ]]; then
  {
    echo "npm not found on PATH and portable NodeJS npm was not found under Dependencies/NodeJS/bin."
  } >"$WEBUI_LOG"
  echo "Engine WebUI unit tests failed with status 2. Log: ${WEBUI_LOG}" >&2
  cat "$WEBUI_LOG" >&2
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
  echo "Results: ${RESULT_DIR}"
  echo "Python status: ${python_status}"
  if [[ -f "$WEBUI_XML" ]]; then
    echo "WebUI status: 0"
  elif [[ "$overall_status" -ne 0 ]]; then
    echo "WebUI status: see ${WEBUI_LOG}"
  fi
  echo "Overall status: ${overall_status}"
} >"$SUMMARY_PATH"

echo "Results written to ${RESULT_DIR}"
exit "$overall_status"
