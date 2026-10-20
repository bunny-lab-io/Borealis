#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

# Separate test toolchain; Engine application builds keep their own pinned Go.
if [[ -n "${BOREALIS_ADMISSION_GO_BIN:-}" ]]; then
  BOREALIS_GO_BIN="${BOREALIS_ADMISSION_GO_BIN}"
fi
GO_BIN="$(resolve_go 1.26.8)"
RESULT_DIR="$(result_dir_for admission-policy)"
TIMEOUT_SECONDS="${BOREALIS_ADMISSION_POLICY_TIMEOUT_SECONDS:-900}"
MODULE_RELATIVE="Tests/tools/admissionpolicy"
require_command python3
mkdir -p "${RESULT_DIR}"
check_gofmt "${GO_BIN}" "${MODULE_RELATIVE}"
check_go_mod_tidy "${GO_BIN}" "${MODULE_RELATIVE}" "${TIMEOUT_SECONDS}" \
  >"${RESULT_DIR}/module-tidy.log" 2>&1
run_timed "${TIMEOUT_SECONDS}" env GOWORK=off CGO_ENABLED=0 "${GO_BIN}" \
  -C "${REPO_ROOT}/${MODULE_RELATIVE}" test -mod=readonly -count=1 -timeout=5m -json ./... \
  >"${RESULT_DIR}/evaluator-results.jsonl" 2>&1
python3 - "${RESULT_DIR}/evaluator-results.jsonl" <<'PY'
import json
import pathlib
import sys

rows = [json.loads(line) for line in pathlib.Path(sys.argv[1]).read_text().splitlines()]
required = {"TestInstanceManagerProbe", "TestInstanceManagerBounds", "TestInstanceManagerRejectsUnsupported",
            "TestInstanceManagerExcludesOtherRequests", "TestFailedJSONPatchTestDoesNotReject"}
passed = {row.get("Test") for row in rows if row.get("Action") == "pass"}
if required - passed or any(row.get("Action") in {"skip", "fail"} for row in rows):
    raise SystemExit("ADMISSION POLICY FAIL: required evaluator tests missing, skipped or failed")
PY
printf 'Pinned Kubernetes admission evaluator passed. Results: %s\n' "${RESULT_DIR}"
