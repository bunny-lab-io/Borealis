#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

TIMEOUT_SECONDS="${BOREALIS_ENGINE_GO_TIMEOUT_SECONDS:-1800}"
RESULT_DIR="$(result_dir_for engine-go)"
MODULE_RELATIVE="Data/Engine/Containers/api-backend"
MODULE_ROOT="${REPO_ROOT}/${MODULE_RELATIVE}"
GO_BIN="$(resolve_go 1.25.12)"
mkdir -p "${RESULT_DIR}"

LANE="${BOREALIS_ENGINE_GO_LANE:-all}"
case "${LANE}" in all|static|packages|api[1-4]) ;; *) echo "Unknown Engine Go lane: ${LANE}" >&2; exit 2 ;; esac
cd "${REPO_ROOT}"
if [[ "${LANE}" == static || "${LANE}" == all ]]; then
  printf '==> Engine Go format\n'
  check_gofmt "${GO_BIN}" "${MODULE_RELATIVE}"

  printf '==> Engine Go module tidy check\n'
  check_go_mod_tidy "${GO_BIN}" "${MODULE_RELATIVE}" "${TIMEOUT_SECONDS}"

  printf '==> Engine Go vet\n'
  run_timed "${TIMEOUT_SECONDS}" env GOWORK=off "${GO_BIN}" -C "${MODULE_ROOT}" vet ./... \
    >"${RESULT_DIR}/engine-go-vet.log" 2>&1

  # One build graph shares dependencies and links independent commands in parallel.
  run_timed "${TIMEOUT_SECONDS}" env GOWORK=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    "${GO_BIN}" -C "${MODULE_ROOT}" build -trimpath -buildvcs=false -o "${RESULT_DIR}/" \
    ./cmd/{api-backend,borealis-node-manager,wireguard-control,wireguard-control-client,wireguard-route-daemon} \
    >"${RESULT_DIR}/engine-go-build.log" 2>&1
  mv "${RESULT_DIR}/api-backend" "${RESULT_DIR}/borealis-api-backend-go"
  cp "${RESULT_DIR}/borealis-api-backend-go" "${RESULT_DIR}/borealis-cluster-controller"
  for cmd in wireguard-control wireguard-control-client wireguard-route-daemon; do
    mv "${RESULT_DIR}/${cmd}" "${RESULT_DIR}/borealis-${cmd}"
  done
fi
printf '==> Engine Go tests\n'
# The complete API race suite exceeds Go's implicit 10-minute package limit.
# Keep package timeout inside the command budget so failures retain Go stacks.
# JSON streams across parallel packages; verbose text alone can stay buffered
# until a package exits, losing unfinished-test evidence on command timeout.
TEST_FLAGS=(-json -count=1)
case "${BOREALIS_ENGINE_GO_RACE:-0}" in
  0) TEST_FLAGS+=(-timeout=20m) ;;
  1) TEST_FLAGS+=(-race -timeout=25m) ;;
  *) printf 'ENGINE GO FAIL: BOREALIS_ENGINE_GO_RACE must be 0 or 1.\n' >&2; exit 2 ;;
esac
run_tests() {
  run_timed "${TIMEOUT_SECONDS}" env GOWORK=off "${GO_BIN}" -C "${MODULE_ROOT}" test "${TEST_FLAGS[@]}" "$@" \
    >"${RESULT_DIR}/engine-go-results.jsonl" 2>"${RESULT_DIR}/engine-go-stderr.log"
}
case "${LANE}" in
  all) run_tests ./... ;;
  packages)
    PACKAGES="$(GOWORK=off "${GO_BIN}" -C "${MODULE_ROOT}" list ./...)"
    mapfile -t REST < <(grep -vx 'borealis/api-backend/cmd/api-backend' <<<"${PACKAGES}")
    (("${#REST[@]}" > 0))
    run_tests "${REST[@]}" ;;
  api*)
    LIST_FLAGS=()
    if [[ "${BOREALIS_ENGINE_GO_RACE:-0}" == 1 ]]; then LIST_FLAGS+=(-race); fi
    run_timed "${TIMEOUT_SECONDS}" env GOWORK=off "${GO_BIN}" -C "${MODULE_ROOT}" test "${LIST_FLAGS[@]}" -list . ./cmd/api-backend \
      >"${RESULT_DIR}/api-inventory.txt"
    SHARD=(python3 Tests/helpers/engine_go_shard.py --listing "${RESULT_DIR}/api-inventory.txt" --shard "${LANE#api}")
    PATTERN="$("${SHARD[@]}")"
    run_tests ./cmd/api-backend -run "${PATTERN}"
    "${SHARD[@]}" --results "${RESULT_DIR}/engine-go-results.jsonl" ;;
esac
if [[ "${GITHUB_ACTIONS:-}" == true && "${LANE}" != all ]]; then
  python3 - "${LANE}" >>"${GITHUB_OUTPUT}" <<'PYCODE'
import sys
from Tests.helpers.engine_go_shard import receipt
from Tests.policy.check_postgres_inventory import ci_identity
print(f"{sys.argv[1]}={receipt(ci_identity(), sys.argv[1])}")
PYCODE
fi
# The required repository-policy runner owns the API cutover and route checks.
printf 'Engine Go lane %s passed. Results: %s\n' "${LANE}" "${RESULT_DIR}"
