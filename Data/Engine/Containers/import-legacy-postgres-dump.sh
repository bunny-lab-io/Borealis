#!/usr/bin/env bash
# One-shot helper for importing a preserved legacy Engine PostgreSQL dump.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_PATH="${BASH_SOURCE[0]:-$0}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${SCRIPT_PATH}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../../.." && pwd)"
PROJECT_NAME="borealis-engine"
COMPOSE_FILE="${REPO_ROOT}/Data/Engine/Containers/compose.yaml"
COMPOSE_ENV="${REPO_ROOT}/Engine/Deploy/compose.env"

die() {
  printf '[%s] ERROR: %s\n' "$(date +%FT%T)" "$*" >&2
  exit 1
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

read_env_value() {
  local key="$1"
  awk -F= -v key="${key}" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "${COMPOSE_ENV}"
}

compose_base() {
  docker compose \
    --project-name "${PROJECT_NAME}" \
    --env-file "${COMPOSE_ENV}" \
    -f "${COMPOSE_FILE}"
}

main() {
  local dump_path="${1:-}"
  [[ -n "${dump_path}" && -f "${dump_path}" ]] || die "Usage: import-legacy-postgres-dump.sh <dump.sql>"
  [[ -f "${COMPOSE_ENV}" ]] || die "Compose env missing. Run Engine.sh deploy first."
  command_exists docker || die "Docker Engine CLI missing."
  docker compose version >/dev/null 2>&1 || die "Docker Compose plugin missing."
  compose_base exec -T postgres-db psql -v ON_ERROR_STOP=1 -U "$(read_env_value POSTGRES_USER)" -d "$(read_env_value POSTGRES_DB)" < "${dump_path}"
}

main "$@"
