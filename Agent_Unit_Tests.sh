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
REQUESTED_DOMAIN="${BOREALIS_AGENT_UNIT_TEST_DOMAIN:-all}"
LIST_DOMAINS=0

usage() {
  cat <<'USAGE'
Usage: ./Agent_Unit_Tests.sh [--domain DOMAIN] [--list-domains]

Runs Agent Go unit/build checks. Legacy Python tests remain under Data/Agent_Old.

Default domain:
  all

Examples:
  ./Agent_Unit_Tests.sh
  ./Agent_Unit_Tests.sh --domain go-agent
  ./Agent_Unit_Tests.sh --list-domains

Environment overrides:
  BOREALIS_PROJECT_ROOT
  BOREALIS_AGENT_UNIT_TEST_DOMAIN
  BOREALIS_UNIT_TEST_RESULTS_DIR
USAGE
}

print_domains() {
  cat <<'DOMAINS'
all
go-agent
DOMAINS
}

valid_domain() {
  local domain="$1"
  case "$domain" in
    all|go-agent)
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

SUMMARY_PATH="${RESULT_DIR}/summary.txt"

resolve_go() {
  if command -v go >/dev/null 2>&1; then
    command -v go
    return 0
  fi
  local candidate
  candidate="$(find "${PROJECT_ROOT}/Dependencies/Go" -path '*/bin/go' -type f -perm -u+x 2>/dev/null | sort | tail -n 1 || true)"
  if [[ -n "$candidate" ]]; then
    echo "$candidate"
    return 0
  fi
  return 1
}

GO_AGENT_LOG="${RESULT_DIR}/agent-go.log"
echo "==> Go Agent unit/build checks (${REQUESTED_DOMAIN})"
GO_BIN="$(resolve_go || true)"
if [[ -z "$GO_BIN" ]]; then
  "${PROJECT_ROOT}/Data/Agent/build-agent.sh" >"$GO_AGENT_LOG" 2>&1
  GO_BIN="$(resolve_go || true)"
fi
if [[ -z "$GO_BIN" ]]; then
  echo "Go executable not found after build helper. Log: ${GO_AGENT_LOG}" >&2
  status=2
else
  {
    set -e
    cd "${PROJECT_ROOT}/Data/Agent"
    "$GO_BIN" mod tidy
    "$GO_BIN" test ./...
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "$GO_BIN" build -trimpath -o "${RESULT_DIR}/Agent-windows-amd64.exe" ./cmd/agent
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "$GO_BIN" build -trimpath -o "${RESULT_DIR}/Agent-linux-amd64.exe" ./cmd/agent
  } >"$GO_AGENT_LOG" 2>&1
  status=$?
fi

if [[ "$status" -ne 0 ]]; then
  echo "Go Agent checks failed with status ${status}. Log: ${GO_AGENT_LOG}" >&2
  tail -n 80 "$GO_AGENT_LOG" >&2 || true
else
  echo "Go Agent checks passed. Log: ${GO_AGENT_LOG}"
fi

{
  echo "Borealis Agent unit test run"
  echo "Domain: ${REQUESTED_DOMAIN}"
  echo "Results: ${RESULT_DIR}"
  echo "Python status: skipped (legacy source moved to Data/Agent_Old)"
  echo "Go Agent status: ${status}"
  echo "Overall status: ${status}"
} >"$SUMMARY_PATH"

echo "Results written to ${RESULT_DIR}"
exit "$status"
