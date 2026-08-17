#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DOMAIN="${BOREALIS_ENGINE_UNIT_TEST_DOMAIN:-all}"
LIST_DOMAINS=0

usage() {
  cat <<'EOF'
Usage: ./Engine_Unit_Tests.sh [--domain DOMAIN] [--list-domains]

Compatibility entrypoint for Engine Go, retained Python, and WebUI validation.
Use Tests/run-engine-go.sh, Tests/run-engine-python.sh, or Tests/run-webui.sh
when one lane is sufficient.
EOF
}

list_domains() {
  python3 "${SCRIPT_DIR}/Tests/policy/check_test_inventory.py" --list-domains
}

while (($#)); do
  case "$1" in
    -d|--domain)
      (($# >= 2)) || { usage >&2; exit 2; }
      DOMAIN="$2"
      shift 2
      ;;
    --domain=*) DOMAIN="${1#*=}"; shift ;;
    --list-domains) LIST_DOMAINS=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

if ((LIST_DOMAINS)); then
  list_domains
  exit 0
fi

case "${DOMAIN}" in
  all)
    AGGREGATE_RESULT_ROOT="${BOREALIS_UNIT_TEST_RESULTS_DIR:-${SCRIPT_DIR}/Unit_Test_Results/engine-$(date -u +%Y%m%dT%H%M%SZ)}"
    BOREALIS_UNIT_TEST_RESULTS_DIR="${AGGREGATE_RESULT_ROOT}/go" \
      "${SCRIPT_DIR}/Tests/run-engine-go.sh"
    BOREALIS_UNIT_TEST_RESULTS_DIR="${AGGREGATE_RESULT_ROOT}/python" \
      "${SCRIPT_DIR}/Tests/run-engine-python.sh" --domain all
    BOREALIS_UNIT_TEST_RESULTS_DIR="${AGGREGATE_RESULT_ROOT}/webui" \
      "${SCRIPT_DIR}/Tests/run-webui.sh"
    ;;
  go) "${SCRIPT_DIR}/Tests/run-engine-go.sh" ;;
  webui) "${SCRIPT_DIR}/Tests/run-webui.sh" ;;
  *) "${SCRIPT_DIR}/Tests/run-engine-python.sh" --domain "${DOMAIN}" ;;
esac
