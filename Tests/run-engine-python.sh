#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

DOMAIN="${BOREALIS_ENGINE_UNIT_TEST_DOMAIN:-all}"
FILE_TIMEOUT_SECONDS="${BOREALIS_ENGINE_UNIT_TEST_FILE_TIMEOUT_SECONDS:-900}"
INSTALL_TIMEOUT_SECONDS="${BOREALIS_ENGINE_DEPENDENCY_TIMEOUT_SECONDS:-1200}"
RESULT_DIR="$(result_dir_for engine-python)"
INVENTORY="${REPO_ROOT}/Tests/policy/check_test_inventory.py"
REQUIREMENTS="${REPO_ROOT}/Data/Engine/Containers/api-backend/data/engine-worker-requirements.txt"

usage() {
  cat <<'EOF'
Usage: ./Tests/run-engine-python.sh [--domain DOMAIN]

Environment:
  BOREALIS_ENGINE_TEST_PYTHON        Use existing Python with test dependencies.
  BOREALIS_ENGINE_UNIT_TEST_DOMAIN   Select machine-readable test domain.
  BOREALIS_UNIT_TEST_RESULTS_DIR     Override ignored result directory.
EOF
}

while (($#)); do
  case "$1" in
    -d|--domain)
      (($# >= 2)) || { usage >&2; exit 2; }
      DOMAIN="$2"
      shift 2
      ;;
    --domain=*) DOMAIN="${1#*=}"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

mkdir -p "${RESULT_DIR}/junit" "${RESULT_DIR}/tmp"
python3 "${INVENTORY}" >/dev/null
mapfile -t TEST_FILES < <(python3 "${INVENTORY}" --list-files "${DOMAIN}")
((${#TEST_FILES[@]} > 0)) || {
  printf 'ENGINE PYTHON FAIL: domain %s resolved to zero tests.\n' "${DOMAIN}" >&2
  exit 2
}

python3 "${REPO_ROOT}/Tests/policy/check_python_syntax.py"

PYTHON_BIN="${BOREALIS_ENGINE_TEST_PYTHON:-}"
if [[ -z "${PYTHON_BIN}" ]]; then
  require_command python3
  VENV_DIR="${RESULT_DIR}/venv"
  python3 -m venv "${VENV_DIR}"
  PYTHON_BIN="${VENV_DIR}/bin/python"
  printf '==> Engine Python dependency install\n'
  run_timed "${INSTALL_TIMEOUT_SECONDS}" "${PYTHON_BIN}" -m pip install \
    --disable-pip-version-check --no-input -r "${REQUIREMENTS}" \
    >"${RESULT_DIR}/dependency-install.log" 2>&1
fi
[[ -x "${PYTHON_BIN}" ]] || {
  printf 'ENGINE PYTHON FAIL: Python executable unavailable: %s\n' "${PYTHON_BIN}" >&2
  exit 127
}
"${PYTHON_BIN}" -c 'import pytest' || {
  printf 'ENGINE PYTHON FAIL: pytest missing from %s\n' "${PYTHON_BIN}" >&2
  exit 127
}

status=0
: >"${RESULT_DIR}/engine-python.log"
for test_file in "${TEST_FILES[@]}"; do
  safe_name="${test_file//\//__}"
  printf '==> %s\n' "${test_file}" | tee -a "${RESULT_DIR}/engine-python.log"
  if ! run_timed "${FILE_TIMEOUT_SECONDS}" env \
    BOREALIS_PROJECT_ROOT="${REPO_ROOT}" \
    PYTHONPATH="${REPO_ROOT}" \
    PYTHONDONTWRITEBYTECODE=1 \
    TMPDIR="${RESULT_DIR}/tmp" \
    "${PYTHON_BIN}" -m pytest -q -p no:cacheprovider "${REPO_ROOT}/${test_file}" \
      --junitxml "${RESULT_DIR}/junit/${safe_name}.xml" \
      >>"${RESULT_DIR}/engine-python.log" 2>&1; then
    printf 'ENGINE PYTHON FAIL: %s. See %s\n' "${test_file}" "${RESULT_DIR}/engine-python.log" >&2
    status=1
  fi
done

if ((status != 0)); then
  tail -n 120 "${RESULT_DIR}/engine-python.log" >&2 || true
  exit "${status}"
fi
printf 'Engine Python validation passed: %s files. Results: %s\n' "${#TEST_FILES[@]}" "${RESULT_DIR}"
