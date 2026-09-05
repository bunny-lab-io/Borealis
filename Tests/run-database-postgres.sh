#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=Tests/lib.sh
source "${SCRIPT_DIR}/lib.sh"

TIMEOUT_SECONDS="${BOREALIS_DATABASE_TEST_TIMEOUT_SECONDS:-1200}"
RESULT_DIR="$(result_dir_for database-postgres)"
GO_BIN="$(resolve_go 1.25.12)"
require_command python3
TEST_PATTERN="$(BOREALIS_GO_BIN="${GO_BIN}" python3 "${SCRIPT_DIR}/policy/check_postgres_inventory.py" --run-pattern)"
CONTAINER_NAME="borealis-ci-postgres-${$}"
POSTGRES_PASSWORD="borealis-ci-password"
DOCKER=(docker)
if [[ "${BOREALIS_DOCKER_USE_SUDO:-0}" == "1" ]]; then
  require_command sudo
  DOCKER=(sudo docker)
fi
require_command "${DOCKER[-1]}"
mkdir -p "${RESULT_DIR}/runtime/secrets/Certificates" "${RESULT_DIR}/runtime/secrets/Auth_Tokens"

cleanup() {
  if [[ "${CONTAINER_NAME}" == borealis-ci-postgres-* ]]; then
    "${DOCKER[@]}" rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

printf '==> PostgreSQL 17 ephemeral container\n'
"${DOCKER[@]}" run -d --name "${CONTAINER_NAME}" \
  -e POSTGRES_DB=borealis \
  -e POSTGRES_USER=borealis \
  -e POSTGRES_PASSWORD="${POSTGRES_PASSWORD}" \
  -p 127.0.0.1::5432 \
  postgres:17-bookworm \
  >"${RESULT_DIR}/container-id.txt"

ready=0
for _attempt in $(seq 1 60); do
  if "${DOCKER[@]}" exec "${CONTAINER_NAME}" pg_isready -U borealis -d borealis >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
if ((ready == 0)); then
  "${DOCKER[@]}" logs "${CONTAINER_NAME}" >"${RESULT_DIR}/postgres-container.log" 2>&1 || true
  printf 'POSTGRES INTEGRATION FAIL: PostgreSQL did not become ready.\n' >&2
  exit 1
fi

PORT_OUTPUT="$("${DOCKER[@]}" port "${CONTAINER_NAME}" 5432/tcp)"
PORT="${PORT_OUTPUT##*:}"
[[ "${PORT}" =~ ^[0-9]+$ ]] || {
  printf 'POSTGRES INTEGRATION FAIL: cannot resolve published port from %s\n' "${PORT_OUTPUT}" >&2
  exit 1
}
DATABASE_URL="postgresql://borealis:${POSTGRES_PASSWORD}@127.0.0.1:${PORT}/borealis"

PYTHON_BIN="${BOREALIS_ENGINE_TEST_PYTHON:-}"
if [[ -z "${PYTHON_BIN}" ]]; then
  python3 -m venv "${RESULT_DIR}/venv"
  PYTHON_BIN="${RESULT_DIR}/venv/bin/python"
  run_timed "${TIMEOUT_SECONDS}" "${PYTHON_BIN}" -m pip install \
    --disable-pip-version-check --no-input \
    -r "${REPO_ROOT}/Data/Engine/Containers/site-worker/data/engine-requirements.txt" \
    >"${RESULT_DIR}/dependency-install.log" 2>&1
fi

if ! run_timed "${TIMEOUT_SECONDS}" env \
  BOREALIS_TEST_DATABASE_URL="${DATABASE_URL}" \
  BOREALIS_DATABASE_URL="${DATABASE_URL}" \
  BOREALIS_DB_SSLMODE=disable \
  BOREALIS_PROJECT_ROOT="${REPO_ROOT}" \
  BOREALIS_ENGINE_CERT_ROOT="${RESULT_DIR}/runtime/secrets/Certificates" \
  BOREALIS_ENGINE_SECRET_PATH="${RESULT_DIR}/runtime/secrets/engine_secret.txt" \
  BOREALIS_ENGINE_AUTH_TOKEN_ROOT="${RESULT_DIR}/runtime/secrets/Auth_Tokens" \
  PYTHONPATH="${REPO_ROOT}" \
  PYTHONDONTWRITEBYTECODE=1 \
  "${PYTHON_BIN}" "${REPO_ROOT}/Tests/integration/database/test_postgres_integration.py" \
  >"${RESULT_DIR}/postgres-integration.log" 2>&1; then
  "${DOCKER[@]}" logs "${CONTAINER_NAME}" >"${RESULT_DIR}/postgres-container.log" 2>&1 || true
  tail -n 160 "${RESULT_DIR}/postgres-integration.log" >&2 || true
  exit 1
fi

if ! run_timed "${TIMEOUT_SECONDS}" env \
  BOREALIS_TEST_DATABASE_URL="${DATABASE_URL}?sslmode=disable" \
  GOWORK=off \
  "${GO_BIN}" -C "${REPO_ROOT}/Data/Engine/Containers/api-backend" test -json ./cmd/api-backend \
  -run "${TEST_PATTERN}" -count=1 \
  >"${RESULT_DIR}/postgres-go-results.jsonl" 2>"${RESULT_DIR}/postgres-go-stderr.log"; then
  tail -n 160 "${RESULT_DIR}/postgres-go-results.jsonl" "${RESULT_DIR}/postgres-go-stderr.log" >&2 || true
  exit 1
fi

"${DOCKER[@]}" logs "${CONTAINER_NAME}" >"${RESULT_DIR}/postgres-container.log" 2>&1 || true
BOREALIS_GO_BIN="${GO_BIN}" python3 "${SCRIPT_DIR}/policy/check_postgres_inventory.py" \
  --results "${RESULT_DIR}/postgres-go-results.jsonl"

printf 'PostgreSQL 17 integration passed. Results: %s\n' "${RESULT_DIR}"
