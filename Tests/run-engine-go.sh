#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

TIMEOUT_SECONDS="${BOREALIS_ENGINE_GO_TIMEOUT_SECONDS:-900}"
RESULT_DIR="$(result_dir_for engine-go)"
MODULE_RELATIVE="Data/Engine/Containers/api-backend"
MODULE_ROOT="${REPO_ROOT}/${MODULE_RELATIVE}"
GO_BIN="$(resolve_go 1.25.12)"
mkdir -p "${RESULT_DIR}"

printf '==> Engine Go format\n'
check_gofmt "${GO_BIN}" "${MODULE_RELATIVE}"

printf '==> Engine Go module tidy check\n'
check_go_mod_tidy "${GO_BIN}" "${MODULE_RELATIVE}" "${TIMEOUT_SECONDS}"

printf '==> Engine Go vet\n'
run_timed "${TIMEOUT_SECONDS}" env GOWORK=off "${GO_BIN}" -C "${MODULE_ROOT}" vet ./... \
  >"${RESULT_DIR}/engine-go-vet.log" 2>&1

printf '==> Engine Go tests\n'
run_timed "${TIMEOUT_SECONDS}" env GOWORK=off "${GO_BIN}" -C "${MODULE_ROOT}" test ./... \
  >"${RESULT_DIR}/engine-go-test.log" 2>&1

printf '==> Engine Go runtime binary builds\n'
run_timed "${TIMEOUT_SECONDS}" env GOWORK=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  "${GO_BIN}" -C "${MODULE_ROOT}" build -trimpath -buildvcs=false \
  -o "${RESULT_DIR}/borealis-api-backend-go" ./cmd/api-backend \
  >"${RESULT_DIR}/engine-go-build.log" 2>&1
run_timed "${TIMEOUT_SECONDS}" env GOWORK=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  "${GO_BIN}" -C "${MODULE_ROOT}" build -trimpath -buildvcs=false \
  -o "${RESULT_DIR}/borealis-wireguard-control" ./cmd/wireguard-control \
  >>"${RESULT_DIR}/engine-go-build.log" 2>&1
run_timed "${TIMEOUT_SECONDS}" env GOWORK=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  "${GO_BIN}" -C "${MODULE_ROOT}" build -trimpath -buildvcs=false \
  -o "${RESULT_DIR}/borealis-wireguard-control-client" ./cmd/wireguard-control-client \
  >>"${RESULT_DIR}/engine-go-build.log" 2>&1

python3 "${REPO_ROOT}/Tests/policy/check_api_cutover.py"
python3 "${REPO_ROOT}/Tests/policy/check_api_routes.py"

printf 'Engine Go validation passed. Results: %s\n' "${RESULT_DIR}"
