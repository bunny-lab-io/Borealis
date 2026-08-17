#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

TIMEOUT_SECONDS="${BOREALIS_K3S_POLICY_TIMEOUT_SECONDS:-600}"
RESULT_DIR="$(result_dir_for k3s-policy)"
GO_BIN="$(resolve_go 1.25.12)"
MODULE_ROOT="${REPO_ROOT}/Data/Engine/Containers/api-backend"
mkdir -p "${RESULT_DIR}"

python3 -c 'import yaml' 2>/dev/null || {
  printf 'K3S POLICY FAIL: PyYAML missing; install Tests/requirements-policy.txt.\n' >&2
  exit 127
}

python3 "${REPO_ROOT}/Tests/policy/check_k3s_manifests.py" \
  >"${RESULT_DIR}/k3s-manifest-policy.log" 2>&1
python3 "${REPO_ROOT}/Tests/policy/check_api_cutover.py" \
  >"${RESULT_DIR}/architecture-cutover-policy.log" 2>&1
run_timed "${TIMEOUT_SECONDS}" env GOWORK=off "${GO_BIN}" -C "${MODULE_ROOT}" test ./cmd/api-backend \
  -run 'TestBorealisOperator|TestScheduler.*(Operator|SiteWorkerLifecycleMode)' \
  >"${RESULT_DIR}/operator-contract-tests.log" 2>&1

printf 'K3s and architecture policy passed. Results: %s\n' "${RESULT_DIR}"
