#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

BASE=""
HEAD=""
while (($#)); do
  case "$1" in
    --base) BASE="$2"; shift 2 ;;
    --head) HEAD="$2"; shift 2 ;;
    -h|--help) printf 'Usage: ./Tests/run-repository-policy.sh [--base REF --head REF]\n'; exit 0 ;;
    *) printf 'REPOSITORY POLICY FAIL: unsupported argument %s\n' "$1" >&2; exit 2 ;;
  esac
done
if [[ (-n "${BASE}" && -z "${HEAD}") || (-z "${BASE}" && -n "${HEAD}") ]]; then
  printf 'REPOSITORY POLICY FAIL: --base and --head must be paired.\n' >&2
  exit 2
fi

require_command git
require_command python3
require_command node
require_command pwsh
require_command actionlint

if [[ -n "${BASE}" ]]; then
  git -C "${REPO_ROOT}" diff --check "${BASE}" "${HEAD}" --
else
  git -C "${REPO_ROOT}" diff --check HEAD --
fi

"${REPO_ROOT}/Tests/run-shell.sh"
python3 "${REPO_ROOT}/Tests/policy/check_python_syntax.py"

mapfile -d '' POWERSHELL_FILES < <(git -C "${REPO_ROOT}" ls-files --cached --others --exclude-standard -z -- '*.ps1')
for file in "${POWERSHELL_FILES[@]}"; do
  BOREALIS_PARSE_FILE="${REPO_ROOT}/${file}" pwsh -NoProfile -NonInteractive -Command '
    $tokens = $null
    $errors = $null
    [System.Management.Automation.Language.Parser]::ParseFile($env:BOREALIS_PARSE_FILE, [ref]$tokens, [ref]$errors) | Out-Null
    if ($errors.Count -gt 0) { $errors | ForEach-Object { Write-Error $_ }; exit 1 }
  '
done

for file in \
  Data/Engine/Containers/webui-frontend/static-server.js \
  Data/Engine/Containers/webui-frontend/healthcheck.js \
  Tests/webui/runtime-scripts.test.js; do
  node --check "${REPO_ROOT}/${file}"
done

python3 "${REPO_ROOT}/Tests/policy/check_data_files.py"
actionlint -color
python3 "${REPO_ROOT}/Data/Engine/Containers/check-compose-policy.py"
python3 "${REPO_ROOT}/Tests/policy/check_agent_runner_parity.py"
python3 "${REPO_ROOT}/Tests/policy/check_test_inventory.py"
python3 "${REPO_ROOT}/Tests/policy/check_build_manifest.py"
python3 -m unittest discover -s "${REPO_ROOT}/Tests/Unit_Tests" -p 'test_*.py'
python3 "${REPO_ROOT}/Tests/policy/check_docs_references.py"
python3 "${REPO_ROOT}/Tests/policy/check_sbom.py"
python3 "${REPO_ROOT}/Tests/policy/check_api_cutover.py"
python3 "${REPO_ROOT}/Tests/policy/check_api_routes.py"

printf 'Repository policy passed.\n'
