#!/bin/sh
set -eu

PROJECT_ROOT="${BOREALIS_PROJECT_ROOT:-/opt/Borealis}"
API_ROOT="${PROJECT_ROOT}/Engine/Services/api-backend"
ROLE="${BOREALIS_PROCESS_ROLE:-api-backend}"

case "${ROLE}" in
  site-worker-orchestrator|worker-orchestrator|site-worker-orchestrator-healthcheck|worker-orchestrator-healthcheck)
    echo "site-worker-orchestrator runtime mode is retired; use borealis-operator managed K3s site-worker lifecycle" >&2
    exit 64
    ;;
esac

mkdir -p \
  "${API_ROOT}/config" \
  "${API_ROOT}/logs/VPN_Tunnel" \
  "${API_ROOT}/secrets" \
  "${API_ROOT}/secrets/Auth_Tokens" \
  "${API_ROOT}/secrets/Certificates" \
  "${API_ROOT}/cache/Aurora"

export BOREALIS_PROJECT_ROOT="${PROJECT_ROOT}"
export BOREALIS_ENGINE_MODE="${BOREALIS_ENGINE_MODE:-production}"
export BOREALIS_WEBUI_EXTERNAL="${BOREALIS_WEBUI_EXTERNAL:-1}"
export BOREALIS_ENGINE_CONTAINERIZED="${BOREALIS_ENGINE_CONTAINERIZED:-1}"
export BOREALIS_SOCKETIO_ASYNC_MODE="${BOREALIS_SOCKETIO_ASYNC_MODE:-eventlet}"
export BOREALIS_ENGINE_CONSOLE_LOG="${BOREALIS_ENGINE_CONSOLE_LOG:-1}"
export BOREALIS_ENGINE_SECRET_PATH="${BOREALIS_ENGINE_SECRET_PATH:-${API_ROOT}/secrets/engine_secret.txt}"
export BOREALIS_ENGINE_CERT_ROOT="${BOREALIS_ENGINE_CERT_ROOT:-${API_ROOT}/secrets/Certificates}"
export BOREALIS_ENGINE_AUTH_TOKEN_ROOT="${BOREALIS_ENGINE_AUTH_TOKEN_ROOT:-${API_ROOT}/secrets/Auth_Tokens}"
export BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH="${BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH:-${API_ROOT}/config/ansible_runner_settings.json}"
export BOREALIS_SITE_WORKER_SETTINGS_PATH="${BOREALIS_SITE_WORKER_SETTINGS_PATH:-${API_ROOT}/config/site_worker_settings.json}"
export BOREALIS_OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT="${BOREALIS_OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT:-${API_ROOT}/cache}"
export BOREALIS_LOG_FILE="${BOREALIS_LOG_FILE:-${API_ROOT}/logs/engine.log}"
export BOREALIS_ERROR_LOG_FILE="${BOREALIS_ERROR_LOG_FILE:-${API_ROOT}/logs/error.log}"
export BOREALIS_API_LOG_FILE="${BOREALIS_API_LOG_FILE:-${API_ROOT}/logs/api.log}"
export BOREALIS_VPN_TUNNEL_LOG_FILE="${BOREALIS_VPN_TUNNEL_LOG_FILE:-${API_ROOT}/logs/VPN_Tunnel/tunnel.log}"
export BOREALIS_WIREGUARD_LOG_FILE="${BOREALIS_WIREGUARD_LOG_FILE:-${BOREALIS_VPN_TUNNEL_LOG_FILE}}"
export BOREALIS_GUACD_HOST="${BOREALIS_GUACD_HOST:-127.0.0.1}"
export BOREALIS_GUACD_PORT="${BOREALIS_GUACD_PORT:-4822}"
export BOREALIS_GUACAMOLE_ENABLED="${BOREALIS_GUACAMOLE_ENABLED:-1}"
export BOREALIS_GO_API_HOST="${BOREALIS_GO_API_HOST:-127.0.0.1}"
export BOREALIS_GO_API_PORT="${BOREALIS_GO_API_PORT:-5000}"

cd /opt/Borealis
exec /usr/local/bin/borealis-api-backend-go
