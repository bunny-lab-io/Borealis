#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

TESTS_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${TESTS_DIR}/.." && pwd)"

timestamp_utc() {
  date -u +"%Y%m%dT%H%M%SZ"
}

result_dir_for() {
  local lane="$1"
  printf '%s\n' "${BOREALIS_UNIT_TEST_RESULTS_DIR:-${REPO_ROOT}/Unit_Test_Results/${lane}-$(timestamp_utc)}"
}

require_command() {
  local command_name="$1"
  command -v "${command_name}" >/dev/null 2>&1 || {
    printf 'VALIDATION DEPENDENCY FAIL: required command missing: %s\n' "${command_name}" >&2
    return 127
  }
}

run_timed() {
  local seconds="$1"
  shift
  if command -v timeout >/dev/null 2>&1; then
    timeout --signal=TERM --kill-after=15 "${seconds}" "$@"
  else
    "$@"
  fi
}

resolve_go() {
  local required_version="$1"
  local candidate="" discovered=""
  if [[ -n "${BOREALIS_GO_BIN:-}" ]]; then
    [[ -x "${BOREALIS_GO_BIN}" ]] || {
      printf 'GO TOOLCHAIN FAIL: BOREALIS_GO_BIN is not executable: %s\n' "${BOREALIS_GO_BIN}" >&2
      return 127
    }
    discovered="$("${BOREALIS_GO_BIN}" env GOVERSION 2>/dev/null || true)"
    [[ "${discovered}" == "go${required_version}" ]] || {
      printf 'GO TOOLCHAIN FAIL: %s required, BOREALIS_GO_BIN reports %s.\n' "${required_version}" "${discovered:-unknown}" >&2
      return 1
    }
    printf '%s\n' "${BOREALIS_GO_BIN}"
    return 0
  fi
  for candidate in \
    "${REPO_ROOT}/Dependencies/Go/go${required_version}/bin/go" \
    "$(command -v go 2>/dev/null || true)"; do
    if [[ -n "${candidate}" && -x "${candidate}" ]]; then
      discovered="$("${candidate}" env GOVERSION 2>/dev/null || true)"
      if [[ "${discovered}" == "go${required_version}" ]]; then
        printf '%s\n' "${candidate}"
        return 0
      fi
    fi
  done
  printf 'GO TOOLCHAIN FAIL: Go %s not found on PATH or under Dependencies/Go.\n' "${required_version}" >&2
  return 127
}

check_gofmt() {
  local go_bin="$1"
  local prefix="$2"
  local gofmt_bin
  gofmt_bin="$(dirname -- "${go_bin}")/gofmt"
  [[ -x "${gofmt_bin}" ]] || {
    printf 'GO FORMAT FAIL: gofmt missing beside %s\n' "${go_bin}" >&2
    return 127
  }
  local files=()
  mapfile -d '' files < <(git -C "${REPO_ROOT}" ls-files -z -- "${prefix}/*.go")
  ((${#files[@]} > 0)) || {
    printf 'GO FORMAT FAIL: no tracked Go files under %s\n' "${prefix}" >&2
    return 1
  }
  local unformatted
  unformatted="$(cd "${REPO_ROOT}" && "${gofmt_bin}" -l "${files[@]}")"
  if [[ -n "${unformatted}" ]]; then
    printf 'GO FORMAT FAIL: gofmt required:\n%s\n' "${unformatted}" >&2
    return 1
  fi
}

check_go_mod_tidy() {
  local go_bin="$1"
  local module_relative="$2"
  local timeout_seconds="$3"
  local workspace
  workspace="$(mktemp -d)"
  trap 'rm -rf "${workspace}"' RETURN
  cp -a "${REPO_ROOT}/${module_relative}/." "${workspace}/"
  run_timed "${timeout_seconds}" env GOWORK=off "${go_bin}" -C "${workspace}" mod tidy
  cmp -s "${REPO_ROOT}/${module_relative}/go.mod" "${workspace}/go.mod" || {
    diff -u "${REPO_ROOT}/${module_relative}/go.mod" "${workspace}/go.mod" >&2 || true
    printf 'GO MODULE FAIL: go mod tidy changes %s/go.mod\n' "${module_relative}" >&2
    return 1
  }
  cmp -s "${REPO_ROOT}/${module_relative}/go.sum" "${workspace}/go.sum" || {
    diff -u "${REPO_ROOT}/${module_relative}/go.sum" "${workspace}/go.sum" >&2 || true
    printf 'GO MODULE FAIL: go mod tidy changes %s/go.sum\n' "${module_relative}" >&2
    return 1
  }
  rm -rf "${workspace}"
  trap - RETURN
}
