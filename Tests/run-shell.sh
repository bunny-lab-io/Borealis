#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

require_command shellcheck
mapfile -d '' SHELL_FILES < <(git -C "${REPO_ROOT}" ls-files --cached --others --exclude-standard -z -- '*.sh')
((${#SHELL_FILES[@]})) || { printf 'SHELL POLICY FAIL: no shell files found.\n' >&2; exit 1; }

for file in "${SHELL_FILES[@]}"; do
  first_line="$(head -n 1 "${REPO_ROOT}/${file}")"
  if [[ "${first_line}" == *'/bin/sh'* && "${first_line}" != *bash* ]]; then
    sh -n "${REPO_ROOT}/${file}"
  else
    bash -n "${REPO_ROOT}/${file}"
  fi
done
shellcheck --severity=error "${SHELL_FILES[@]/#/${REPO_ROOT}/}"
printf 'Shell policy passed: %s files.\n' "${#SHELL_FILES[@]}"
