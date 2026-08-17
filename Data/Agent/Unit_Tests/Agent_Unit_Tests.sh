#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../../.." && pwd)"
DOMAIN="${BOREALIS_AGENT_UNIT_TEST_DOMAIN:-all}"

usage() {
  cat <<'EOF'
Usage: ./Data/Agent/Unit_Tests/Agent_Unit_Tests.sh [--domain all|go-agent] [--list-domains]
EOF
}

while (($#)); do
  case "$1" in
    --list-domains)
      printf 'all\ngo-agent\n'
      exit 0
      ;;
    -d|--domain)
      (($# >= 2)) || { usage >&2; exit 2; }
      DOMAIN="$2"
      shift 2
      ;;
    --domain=*)
      DOMAIN="${1#*=}"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
done

case "${DOMAIN}" in
  all|go-agent) ;;
  *) printf 'Unknown Agent test domain: %s\n' "${DOMAIN}" >&2; exit 2 ;;
esac

exec "${REPO_ROOT}/Tests/run-agent.sh"
