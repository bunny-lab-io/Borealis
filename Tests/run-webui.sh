#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

TIMEOUT_SECONDS="${BOREALIS_WEBUI_TIMEOUT_SECONDS:-900}"
RESULT_DIR="$(result_dir_for webui)"
SOURCE_ROOT="${REPO_ROOT}/Data/Engine/Containers/webui-frontend/data/web-interface"
require_command node
require_command npm

NODE_MAJOR="$(node -p 'process.versions.node.split(".")[0]')"
[[ "${NODE_MAJOR}" == "22" ]] || {
  printf 'WEBUI DEPENDENCY FAIL: Node 22 required, found %s.\n' "$(node --version)" >&2
  exit 2
}
[[ -f "${SOURCE_ROOT}/package-lock.json" ]] || {
  printf 'WEBUI DEPENDENCY FAIL: committed package-lock.json missing.\n' >&2
  exit 2
}

NPM_CACHE="${NPM_CONFIG_CACHE:-${RESULT_DIR}/npm-cache}"
mkdir -p "${RESULT_DIR}" "${NPM_CACHE}"
WORKSPACE="$(mktemp -d)"
trap 'rm -rf "${WORKSPACE}"' EXIT
cp -a "${SOURCE_ROOT}/." "${WORKSPACE}/"

printf '==> WebUI runtime script contracts\n'
if ! run_timed "${TIMEOUT_SECONDS}" node --test "${REPO_ROOT}/Tests/webui/runtime-scripts.test.js" \
  >"${RESULT_DIR}/webui-runtime-scripts.log" 2>&1; then
  tail -n 120 "${RESULT_DIR}/webui-runtime-scripts.log" >&2 || true
  exit 1
fi

printf '==> WebUI reproducible dependency install\n'
if ! run_timed "${TIMEOUT_SECONDS}" env NPM_CONFIG_CACHE="${NPM_CACHE}" \
  npm --prefix "${WORKSPACE}" ci --include=dev --no-fund --audit=false \
  >"${RESULT_DIR}/webui-npm-ci.log" 2>&1; then
  tail -n 120 "${RESULT_DIR}/webui-npm-ci.log" >&2 || true
  exit 1
fi

printf '==> WebUI Vitest\n'
if ! run_timed "${TIMEOUT_SECONDS}" env NPM_CONFIG_CACHE="${NPM_CACHE}" \
  npm --prefix "${WORKSPACE}" test -- --reporter=default --reporter=junit \
  --outputFile="${RESULT_DIR}/webui-vitest.xml" \
  >"${RESULT_DIR}/webui-vitest.log" 2>&1; then
  tail -n 160 "${RESULT_DIR}/webui-vitest.log" >&2 || true
  exit 1
fi

printf '==> WebUI production build\n'
if ! run_timed "${TIMEOUT_SECONDS}" env NPM_CONFIG_CACHE="${NPM_CACHE}" \
  npm --prefix "${WORKSPACE}" run build \
  >"${RESULT_DIR}/webui-build.log" 2>&1; then
  tail -n 160 "${RESULT_DIR}/webui-build.log" >&2 || true
  exit 1
fi
[[ -f "${WORKSPACE}/build/index.html" ]] || {
  printf 'WEBUI BUILD FAIL: production build lacks build/index.html.\n' >&2
  exit 1
}

printf 'WebUI validation passed. Results: %s\n' "${RESULT_DIR}"
