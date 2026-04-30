#!/usr/bin/env bash
# ======================================================
# Agent_Unit_Tests.sh
# Description: Runs the full Borealis Agent unit test lane on Linux/macOS shells.
#
# API Endpoints (if applicable): None
# ======================================================

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="${BOREALIS_PROJECT_ROOT:-${SCRIPT_DIR}}"
TIMESTAMP="$(date -u +"%Y%m%dT%H%M%SZ")"
RESULT_DIR="${BOREALIS_UNIT_TEST_RESULTS_DIR:-${PROJECT_ROOT}/Unit_Test_Results/agent-${TIMESTAMP}}"
PYTHON_TIMEOUT_SECONDS="${BOREALIS_AGENT_UNIT_TEST_TIMEOUT_SECONDS:-900}"

usage() {
  cat <<'USAGE'
Usage: ./Agent_Unit_Tests.sh

Runs all Agent Python unit tests.
No domain-level selection is supported.

Environment overrides:
  BOREALIS_PROJECT_ROOT
  BOREALIS_AGENT_TEST_PYTHON
  BOREALIS_UNIT_TEST_RESULTS_DIR
  BOREALIS_AGENT_UNIT_TEST_TIMEOUT_SECONDS
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

PYTEST_LOG="${RESULT_DIR}/agent-pytest.log"
PYTEST_XML="${RESULT_DIR}/agent-pytest.xml"
SUMMARY_PATH="${RESULT_DIR}/summary.txt"

echo "==> Agent Python unit tests"
if command -v timeout >/dev/null 2>&1; then
  timeout "$PYTHON_TIMEOUT_SECONDS" \
    env PYTHONDONTWRITEBYTECODE=1 BOREALIS_PROJECT_ROOT="$PROJECT_ROOT" \
      "$PYTHON_BIN" -m pytest -q Data/Agent/Unit_Tests --junitxml "$PYTEST_XML" \
      >"$PYTEST_LOG" 2>&1
else
  env PYTHONDONTWRITEBYTECODE=1 BOREALIS_PROJECT_ROOT="$PROJECT_ROOT" \
    "$PYTHON_BIN" -m pytest -q Data/Agent/Unit_Tests --junitxml "$PYTEST_XML" \
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
  echo "Results: ${RESULT_DIR}"
  echo "Python status: ${status}"
  echo "Overall status: ${status}"
} >"$SUMMARY_PATH"

echo "Results written to ${RESULT_DIR}"
exit "$status"
