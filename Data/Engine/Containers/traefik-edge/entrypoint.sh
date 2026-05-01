#!/usr/bin/env sh
set -eu

PROJECT_ROOT="${BOREALIS_PROJECT_ROOT:-/opt/Borealis}"
SERVICE_ROOT="${PROJECT_ROOT}/Engine/Services/traefik-edge"
CONFIG_DIR="${SERVICE_ROOT}/config"
LOG_DIR="${SERVICE_ROOT}/logs"
STATE_DIR="${SERVICE_ROOT}/state"
HOSTNAME="${BOREALIS_PUBLIC_HOSTNAME:-localhost}"
ACME_EMAIL="${BOREALIS_ACME_EMAIL:-}"
WEBUI_MODE="${BOREALIS_WEBUI_MODE:-prod}"
HEALTH_PORT="${BOREALIS_TRAEFIK_HEALTH_PORT:-8082}"

mkdir -p "${CONFIG_DIR}" "${LOG_DIR}" "${STATE_DIR}"
touch "${STATE_DIR}/acme.json"
chmod 600 "${STATE_DIR}/acme.json" 2>/dev/null || true

cat > "${STATE_DIR}/Settings.json" <<EOF
{
  "enabled": true,
  "fqdn": "${HOSTNAME}",
  "acme_email": "${ACME_EMAIL}",
  "public_base_url": "${BOREALIS_PUBLIC_BASE_URL:-https://${HOSTNAME}}",
  "public_vnc_path": "${BOREALIS_PUBLIC_VNC_PATH:-/remote-desktop/vnc}",
  "public_wireguard_host": "${BOREALIS_PUBLIC_WIREGUARD_HOST:-${HOSTNAME}}",
  "public_wireguard_port": ${BOREALIS_PUBLIC_WIREGUARD_PORT:-30000},
  "health_port": ${HEALTH_PORT},
  "http_port": ${BOREALIS_PUBLIC_HTTP_PORT:-80},
  "https_port": ${BOREALIS_PUBLIC_HTTPS_PORT:-443},
  "engine_upstream_host": "127.0.0.1",
  "engine_upstream_port": 5000,
  "vnc_upstream_host": "127.0.0.1",
  "vnc_upstream_port": ${BOREALIS_VNC_WS_PORT:-4823},
  "settings_path": "${STATE_DIR}/Settings.json",
  "runtime_env_path": "${SERVICE_ROOT}/env/runtime.env",
  "acme_storage_path": "${STATE_DIR}/acme.json",
  "traefik_static_config_path": "${CONFIG_DIR}/traefik.yml",
  "traefik_dynamic_config_path": "${CONFIG_DIR}/dynamic.yml",
  "logs_directory": "${LOG_DIR}"
}
EOF

cat > "${CONFIG_DIR}/traefik.yml" <<EOF
entryPoints:
  borealis-health:
    address: "127.0.0.1:${HEALTH_PORT}"
  web:
    address: ":${BOREALIS_PUBLIC_HTTP_PORT:-80}"
  websecure:
    address: ":${BOREALIS_PUBLIC_HTTPS_PORT:-443}"
ping:
  entryPoint: borealis-health
providers:
  file:
    filename: "${CONFIG_DIR}/dynamic.yml"
    watch: true
log:
  level: INFO
  filePath: "${LOG_DIR}/traefik.log"
accessLog:
  filePath: "${LOG_DIR}/traefik-access.log"
EOF

if [ -n "${ACME_EMAIL}" ] && [ "${HOSTNAME}" != "localhost" ]; then
  cat >> "${CONFIG_DIR}/traefik.yml" <<EOF
certificatesResolvers:
  letsencrypt:
    acme:
      email: "${ACME_EMAIL}"
      storage: "${STATE_DIR}/acme.json"
      httpChallenge:
        entryPoint: web
EOF
  TLS_BLOCK="      tls:\n        certResolver: letsencrypt"
else
  TLS_BLOCK="      tls: {}"
fi

UI_PORT="8080"
if [ "${WEBUI_MODE}" = "dev" ] || [ "${WEBUI_MODE}" = "developer" ]; then
  UI_PORT="5173"
fi

cat > "${CONFIG_DIR}/dynamic.yml" <<EOF
http:
  middlewares:
    redirect-to-https:
      redirectScheme:
        scheme: https
        permanent: true
  routers:
    borealis-http:
      entryPoints:
        - web
      rule: "Host(\`${HOSTNAME}\`) && !PathPrefix(\`/.well-known/acme-challenge/\`)"
      middlewares:
        - redirect-to-https
      service: noop@internal
    borealis-api:
      entryPoints:
        - websecure
      rule: "Host(\`${HOSTNAME}\`) && (PathPrefix(\`/api\`) || PathPrefix(\`/socket.io\`))"
      service: borealis-api
      priority: 100
$(printf "%b" "${TLS_BLOCK}")
    borealis-vnc:
      entryPoints:
        - websecure
      rule: "Host(\`${HOSTNAME}\`) && PathPrefix(\`${BOREALIS_PUBLIC_VNC_PATH:-/remote-desktop/vnc}\`)"
      service: borealis-vnc
      priority: 90
$(printf "%b" "${TLS_BLOCK}")
    borealis-webui:
      entryPoints:
        - websecure
      rule: "Host(\`${HOSTNAME}\`)"
      service: borealis-webui
      priority: 10
$(printf "%b" "${TLS_BLOCK}")
  services:
    borealis-api:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:5000"
    borealis-webui:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:${UI_PORT}"
    borealis-vnc:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:${BOREALIS_VNC_WS_PORT:-4823}"
EOF

exec traefik --configFile="${CONFIG_DIR}/traefik.yml"
