#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

TIMEOUT_SECONDS="${BOREALIS_AGENT_TEST_TIMEOUT_SECONDS:-900}"
RESULT_DIR="$(result_dir_for agent-linux)"
MODULE_RELATIVE="Data/Agent"
MODULE_ROOT="${REPO_ROOT}/${MODULE_RELATIVE}"
GO_BIN="$(resolve_go 1.22.12)"
mkdir -p "${RESULT_DIR}"

printf '==> Agent Go format\n'
check_gofmt "${GO_BIN}" "${MODULE_RELATIVE}"

printf '==> Agent Go module tidy check\n'
check_go_mod_tidy "${GO_BIN}" "${MODULE_RELATIVE}" "${TIMEOUT_SECONDS}"

printf '==> Agent Go vet\n'
run_timed "${TIMEOUT_SECONDS}" env GOWORK=off "${GO_BIN}" -C "${MODULE_ROOT}" vet ./... \
  >"${RESULT_DIR}/agent-go-vet.log" 2>&1

printf '==> Agent Linux tests\n'
run_timed "${TIMEOUT_SECONDS}" env GOWORK=off "${GO_BIN}" -C "${MODULE_ROOT}" test ./... \
  >"${RESULT_DIR}/agent-go-test.log" 2>&1

printf '==> Agent Windows/Linux builds\n'
BUILD_WORKSPACE="$(mktemp -d)"
trap 'rm -rf "${BUILD_WORKSPACE}"' EXIT
cp -a "${MODULE_ROOT}/." "${BUILD_WORKSPACE}/"
if [[ -f "${BUILD_WORKSPACE}/Agent.syso" ]]; then
  cp "${BUILD_WORKSPACE}/Agent.syso" "${BUILD_WORKSPACE}/cmd/agent/agent_windows.syso"
fi
run_timed "${TIMEOUT_SECONDS}" env GOWORK=off GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  "${GO_BIN}" -C "${BUILD_WORKSPACE}" build -trimpath -buildvcs=false \
  -o "${RESULT_DIR}/Agent-windows-amd64.exe" ./cmd/agent \
  >"${RESULT_DIR}/agent-windows-build.log" 2>&1
run_timed "${TIMEOUT_SECONDS}" env GOWORK=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  "${GO_BIN}" -C "${BUILD_WORKSPACE}" build -trimpath -buildvcs=false \
  -o "${RESULT_DIR}/Agent-linux-amd64" ./cmd/agent \
  >"${RESULT_DIR}/agent-linux-build.log" 2>&1

printf 'Agent validation passed. Results: %s\n' "${RESULT_DIR}"
