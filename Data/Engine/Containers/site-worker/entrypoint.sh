#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

PROJECT_ROOT="${BOREALIS_PROJECT_ROOT:-/opt/Borealis}"
API_ROOT="${PROJECT_ROOT}/Engine/Services/api-backend"

mkdir -p \
  "${API_ROOT}/config" \
  "${API_ROOT}/cache/Ansible/collections" \
  "${API_ROOT}/cache/Ansible/Generated/Runtime"

export BOREALIS_PROJECT_ROOT="${PROJECT_ROOT}"
export BOREALIS_ENGINE_MODE="${BOREALIS_ENGINE_MODE:-production}"
export BOREALIS_ENGINE_CONTAINERIZED="${BOREALIS_ENGINE_CONTAINERIZED:-1}"
export BOREALIS_ENGINE_CONSOLE_LOG="${BOREALIS_ENGINE_CONSOLE_LOG:-1}"
export BOREALIS_INTERNAL_API_BASE_URL="${BOREALIS_INTERNAL_API_BASE_URL:-http://127.0.0.1:5000}"
export BOREALIS_LOG_FILE="${BOREALIS_LOG_FILE:-/tmp/borealis-site-worker.log}"
export BOREALIS_ERROR_LOG_FILE="${BOREALIS_ERROR_LOG_FILE:-/tmp/borealis-site-worker-error.log}"
export BOREALIS_API_LOG_FILE="${BOREALIS_API_LOG_FILE:-/tmp/borealis-site-worker-api.log}"
export BOREALIS_VPN_TUNNEL_LOG_FILE="${BOREALIS_VPN_TUNNEL_LOG_FILE:-/tmp/borealis-site-worker-vpn.log}"
export BOREALIS_WIREGUARD_LOG_FILE="${BOREALIS_WIREGUARD_LOG_FILE:-${BOREALIS_VPN_TUNNEL_LOG_FILE}}"
export BOREALIS_ANSIBLE_RUNTIME_ROOT="${BOREALIS_ANSIBLE_RUNTIME_ROOT:-${API_ROOT}/cache/Ansible}"
export BOREALIS_SITE_WORKER_SETTINGS_PATH="${BOREALIS_SITE_WORKER_SETTINGS_PATH:-${API_ROOT}/config/site_worker_settings.json}"
export ANSIBLE_COLLECTIONS_PATH="${BOREALIS_ANSIBLE_RUNTIME_ROOT}/collections"
export ANSIBLE_COLLECTIONS_PATHS="${ANSIBLE_COLLECTIONS_PATH}"
export PYTHONPATH="/opt/Borealis:/opt/Borealis/Data/Engine:/opt/Borealis/Data/Agent:${PYTHONPATH:-}"

cd /opt/Borealis
python -m Data.Engine.services.ansible.collections
exec python -m Data.Engine.services.job_scheduler.worker
