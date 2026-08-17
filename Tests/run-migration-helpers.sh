#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

RESULT_DIR="$(result_dir_for migration-helpers)"
mkdir -p "${RESULT_DIR}"
python3 -m unittest discover -s "${REPO_ROOT}/Tests/integration/migration_helpers" -p 'test_*.py' -v \
  >"${RESULT_DIR}/migration-helper-tests.log" 2>&1 || {
    tail -n 160 "${RESULT_DIR}/migration-helper-tests.log" >&2 || true
    exit 1
  }
printf 'Migration helper contracts passed. Results: %s\n' "${RESULT_DIR}"
