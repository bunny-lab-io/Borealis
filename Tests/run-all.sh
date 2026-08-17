#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

RESULT_ROOT="${BOREALIS_UNIT_TEST_RESULTS_DIR:-${REPO_ROOT}/Unit_Test_Results/all-$(timestamp_utc)}"

run_lane() {
  local name="$1"
  shift
  printf '\n==> Validation lane: %s\n' "${name}"
  BOREALIS_UNIT_TEST_RESULTS_DIR="${RESULT_ROOT}/${name}" "$@"
}

run_lane repository-policy "${REPO_ROOT}/Tests/run-repository-policy.sh"
run_lane agent "${REPO_ROOT}/Tests/run-agent.sh"
run_lane engine-go "${REPO_ROOT}/Tests/run-engine-go.sh"
run_lane engine-python "${REPO_ROOT}/Tests/run-engine-python.sh"
run_lane webui "${REPO_ROOT}/Tests/run-webui.sh"
run_lane k3s-policy "${REPO_ROOT}/Tests/run-k3s-policy.sh"
run_lane migration "${REPO_ROOT}/Tests/run-migration-helpers.sh"
run_lane database "${REPO_ROOT}/Tests/run-database-postgres.sh"
run_lane containers "${REPO_ROOT}/Tests/run-containers.sh"
run_lane docs "${REPO_ROOT}/Tests/run-docs.sh"

printf 'All portable validation passed. Results: %s\n' "${RESULT_ROOT}"
