#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

PROJECT_ROOT="${BOREALIS_PROJECT_ROOT:-/opt/Borealis}"
API_ROOT="${PROJECT_ROOT}/Engine/Services/api-backend"

mkdir -p \
  "${API_ROOT}/config" \
  "${API_ROOT}/env" \
  "${API_ROOT}/logs/VPN_Tunnel" \
  "${API_ROOT}/state" \
  "${API_ROOT}/secrets" \
  "${API_ROOT}/cache" \
  "${API_ROOT}/run" \
  "${PROJECT_ROOT}/Engine/Ansible/collections" \
  "${PROJECT_ROOT}/Engine/Ansible/Generated/Runtime"

export BOREALIS_PROJECT_ROOT="${PROJECT_ROOT}"
export BOREALIS_ENGINE_MODE="${BOREALIS_ENGINE_MODE:-production}"
export BOREALIS_WEBUI_EXTERNAL="${BOREALIS_WEBUI_EXTERNAL:-1}"
export BOREALIS_ENGINE_CONTAINERIZED="${BOREALIS_ENGINE_CONTAINERIZED:-1}"
export BOREALIS_SOCKETIO_ASYNC_MODE="${BOREALIS_SOCKETIO_ASYNC_MODE:-eventlet}"
export BOREALIS_ENGINE_CONSOLE_LOG="${BOREALIS_ENGINE_CONSOLE_LOG:-1}"
export BOREALIS_ENGINE_SECRET_PATH="${BOREALIS_ENGINE_SECRET_PATH:-${API_ROOT}/secrets/engine_secret.txt}"
export BOREALIS_LOG_FILE="${BOREALIS_LOG_FILE:-${API_ROOT}/logs/engine.log}"
export BOREALIS_ERROR_LOG_FILE="${BOREALIS_ERROR_LOG_FILE:-${API_ROOT}/logs/error.log}"
export BOREALIS_API_LOG_FILE="${BOREALIS_API_LOG_FILE:-${API_ROOT}/logs/api.log}"
export BOREALIS_VPN_TUNNEL_LOG_FILE="${BOREALIS_VPN_TUNNEL_LOG_FILE:-${API_ROOT}/logs/VPN_Tunnel/tunnel.log}"
export BOREALIS_WIREGUARD_LOG_FILE="${BOREALIS_WIREGUARD_LOG_FILE:-${BOREALIS_VPN_TUNNEL_LOG_FILE}}"
export BOREALIS_GUACD_HOST="${BOREALIS_GUACD_HOST:-127.0.0.1}"
export BOREALIS_GUACD_PORT="${BOREALIS_GUACD_PORT:-4822}"
export BOREALIS_GUACAMOLE_ENABLED="${BOREALIS_GUACAMOLE_ENABLED:-1}"
export ANSIBLE_COLLECTIONS_PATH="${PROJECT_ROOT}/Engine/Ansible/collections"
export ANSIBLE_COLLECTIONS_PATHS="${ANSIBLE_COLLECTIONS_PATH}"
export PYTHONPATH="/opt/Borealis:/opt/Borealis/Data/Engine:/opt/Borealis/Data/Agent:${PYTHONPATH:-}"

cd /opt/Borealis
exec python -m Data.Engine.bootstrapper
