#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

TIMEOUT_SECONDS="${BOREALIS_CONTAINER_BUILD_TIMEOUT_SECONDS:-1800}"
RESULT_DIR="$(result_dir_for containers)"
BASE=""
HEAD=""
declare -a REQUESTED_SERVICES=()
declare -a CHANGED_FILES=()

usage() {
  cat <<'EOF'
Usage: ./Tests/run-containers.sh [--base REF --head REF] [--file PATH] [--service NAME]

Without arguments, resolves current tracked and untracked worktree changes.
Set BOREALIS_DOCKER_USE_SUDO=1 when local Docker socket requires sudo.
EOF
}

while (($#)); do
  case "$1" in
    --base) BASE="$2"; shift 2 ;;
    --head) HEAD="$2"; shift 2 ;;
    --file) CHANGED_FILES+=("$2"); shift 2 ;;
    --service) REQUESTED_SERVICES+=("$2"); shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
if [[ (-n "${BASE}" && -z "${HEAD}") || (-z "${BASE}" && -n "${HEAD}") ]]; then
  printf 'CONTAINER FAIL: --base and --head must be paired.\n' >&2
  exit 2
fi

require_command git
require_command python3
require_command tar
if [[ "${BOREALIS_DOCKER_USE_SUDO:-0}" == "1" ]]; then
  DOCKER=(sudo docker)
else
  DOCKER=(docker)
fi
"${DOCKER[@]}" version >/dev/null
python3 "${REPO_ROOT}/Tests/policy/check_build_manifest.py"

mapfile -t ALL_SERVICES < <(python3 -c 'import json; print(*sorted(json.load(open("Data/Engine/Containers/build-manifest.json"))["services"]), sep="\n")')
if ((${#REQUESTED_SERVICES[@]})); then
  SERVICES=("${REQUESTED_SERVICES[@]}")
else
  resolver=(python3 "${REPO_ROOT}/Tests/helpers/affected_services.py")
  if [[ -n "${BASE}" ]]; then
    resolver+=(--base "${BASE}" --head "${HEAD}")
  else
    mapfile -t WORKTREE_FILES < <({ git -C "${REPO_ROOT}" diff --name-only HEAD --; git -C "${REPO_ROOT}" ls-files --others --exclude-standard; } | sort -u)
    CHANGED_FILES+=("${WORKTREE_FILES[@]}")
  fi
  for changed_file in "${CHANGED_FILES[@]}"; do
    resolver+=(--file "${changed_file}")
  done
  mapfile -t SERVICES < <("${resolver[@]}")
fi

for service in "${SERVICES[@]}"; do
  [[ " ${ALL_SERVICES[*]} " == *" ${service} "* ]] || {
    printf 'CONTAINER FAIL: unknown service %s.\n' "${service}" >&2
    exit 2
  }
done
if ((${#SERVICES[@]} == 0)); then
  printf 'No container images affected.\n'
  exit 0
fi

mkdir -p "${RESULT_DIR}"
WORKSPACE="$(mktemp -d)"
trap 'rm -rf "${WORKSPACE}"' EXIT
git -C "${REPO_ROOT}" ls-files --cached --others --exclude-standard -z \
  | tar -C "${REPO_ROOT}" --null --files-from=- -cf - \
  | tar -C "${WORKSPACE}" -xf -

needs_go_binary=0
for service in "${SERVICES[@]}"; do
  case "${service}" in api-backend|borealis-operator|job-scheduler) needs_go_binary=1 ;; esac
done
if ((needs_go_binary)); then
  GO_BIN="$(resolve_go 1.25.12)"
  mkdir -p "${WORKSPACE}/Data/Engine/Containers/api-backend/dist"
  printf '==> Prebuild shared Engine Go binary\n'
  run_timed "${TIMEOUT_SECONDS}" env GOWORK=off GOOS=linux CGO_ENABLED=0 \
    "${GO_BIN}" -C "${WORKSPACE}/Data/Engine/Containers/api-backend" build \
    -trimpath -buildvcs=false -mod=readonly \
    -o "${WORKSPACE}/Data/Engine/Containers/api-backend/dist/api-backend" ./cmd/api-backend \
    >"${RESULT_DIR}/engine-go-prebuild.log" 2>&1
fi

build_image() {
  local service="$1"
  local target="$2"
  local dockerfile context suffix tag log inspect
  readarray -t manifest_fields < <(python3 - "${service}" <<'PY'
import json, sys
entry = json.load(open("Data/Engine/Containers/build-manifest.json"))["services"][sys.argv[1]]
print(entry["dockerfile"])
print(entry["context"])
PY
  )
  dockerfile="${manifest_fields[0]}"
  context="${manifest_fields[1]}"
  suffix="${target:+-${target}}"
  tag="borealis-ci/${service}${suffix}:local"
  log="${RESULT_DIR}/${service}${suffix}.log"
  inspect="${RESULT_DIR}/${service}${suffix}-inspect.json"
  printf '==> Build %s%s\n' "${service}" "${target:+ target ${target}}"
  build_args=(build --pull=false --file "${WORKSPACE}/${dockerfile}" --tag "${tag}" --label "io.borealis.ci=true")
  [[ -z "${target}" ]] || build_args+=(--target "${target}")
  build_args+=("${WORKSPACE}/${context}")
  if ! run_timed "${TIMEOUT_SECONDS}" "${DOCKER[@]}" "${build_args[@]}" >"${log}" 2>&1; then
    tail -n 120 "${log}" >&2 || true
    return 1
  fi
  "${DOCKER[@]}" image inspect "${tag}" >"${inspect}"
  policy_args=(--service "${service}" --inspect "${inspect}")
  [[ -z "${target}" ]] || policy_args+=(--target "${target}")
  python3 "${REPO_ROOT}/Tests/policy/check_image_metadata.py" "${policy_args[@]}"
}

for service in "${SERVICES[@]}"; do
  if [[ "${service}" == "webui-frontend" ]]; then
    build_image "${service}" dev
    build_image "${service}" prod
  else
    build_image "${service}" ""
  fi
done

printf 'Container validation passed: %s. Results: %s\n' "${SERVICES[*]}" "${RESULT_DIR}"
