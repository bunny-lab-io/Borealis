#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

TIMEOUT_SECONDS="${BOREALIS_DOCS_TIMEOUT_SECONDS:-900}"
RESULT_DIR="$(result_dir_for docs)"
mkdir -p "${RESULT_DIR}"
require_command python3

VENV_DIR="${RESULT_DIR}/venv"
python3 -m venv "${VENV_DIR}"
run_timed "${TIMEOUT_SECONDS}" "${VENV_DIR}/bin/python" -m pip install \
  --disable-pip-version-check --no-input -r "${REPO_ROOT}/Tests/requirements-docs.txt" \
  >"${RESULT_DIR}/dependency-install.log" 2>&1

printf '==> Strict Zensical build\n'
run_timed "${TIMEOUT_SECONDS}" "${VENV_DIR}/bin/zensical" build --clean --strict \
  >"${RESULT_DIR}/zensical-build.log" 2>&1

find "${REPO_ROOT}/site" -type f \( \
  -iname '*.psd' -o -iname '*.psb' -o -iname '*.xcf' -o \
  -iname '*.ai' -o -iname '*.sketch' \
\) -delete

[[ -f "${REPO_ROOT}/site/index.html" ]] || {
  printf 'DOCS FAIL: strict build lacks site/index.html.\n' >&2
  exit 1
}
printf 'Documentation validation passed. Results: %s\n' "${RESULT_DIR}"
