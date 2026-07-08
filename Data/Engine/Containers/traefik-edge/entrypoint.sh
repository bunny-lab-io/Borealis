#!/usr/bin/env sh
set -eu

PROJECT_ROOT="${BOREALIS_PROJECT_ROOT:-/opt/Borealis}"
SERVICE_ROOT="${PROJECT_ROOT}/Engine/Services/traefik-edge"
CONFIG_DIR="${SERVICE_ROOT}/config"
DYNAMIC_CONFIG_DIR="${BOREALIS_TRAEFIK_DYNAMIC_CONFIG_DIR:-${CONFIG_DIR}/dynamic}"
CORE_DYNAMIC_CONFIG_PATH="${BOREALIS_TRAEFIK_DYNAMIC_CONFIG_PATH:-${DYNAMIC_CONFIG_DIR}/core.yml}"
LOG_DIR="${SERVICE_ROOT}/logs"
STATE_DIR="${SERVICE_ROOT}/state"
HOSTNAME="${BOREALIS_PUBLIC_HOSTNAME:-localhost}"
HOSTNAME_ALIASES="${BOREALIS_PUBLIC_HOSTNAME_ALIASES:-${HOSTNAME}}"
DEPLOYMENT_PROFILE="${BOREALIS_ENGINE_DEPLOYMENT_PROFILE:-externally-accessible}"
DEPLOYMENT_PROFILE_LABEL="${BOREALIS_ENGINE_DEPLOYMENT_PROFILE_LABEL:-Externally Accessible}"
ACME_EMAIL="${BOREALIS_ACME_EMAIL:-}"
LOCAL_CA_ENABLED="${BOREALIS_LOCAL_CA_ENABLED:-0}"
LOCAL_CA_CERT_PATH="${BOREALIS_LOCAL_CA_CERT_PATH:-}"
LOCAL_TLS_CERT_PATH="${BOREALIS_LOCAL_TLS_CERT_PATH:-}"
LOCAL_TLS_KEY_PATH="${BOREALIS_LOCAL_TLS_KEY_PATH:-}"
WEBUI_UPSTREAM_PORT="${BOREALIS_WEBUI_UPSTREAM_PORT:-8000}"
HEALTH_PORT="${BOREALIS_TRAEFIK_HEALTH_PORT:-8082}"
TRUSTED_PROXY_IPS="${BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS:-}"
FORWARDED_HEADERS_TRUSTED_IPS="${BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS:-${TRUSTED_PROXY_IPS}}"
PROXY_PROTOCOL_TRUSTED_IPS="${BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS:-${TRUSTED_PROXY_IPS}}"
RUNTIME_OWNER_UID="${BOREALIS_ENGINE_RUNTIME_OWNER_UID:-}"
RUNTIME_OWNER_GID="${BOREALIS_ENGINE_RUNTIME_OWNER_GID:-}"

case "${CORE_DYNAMIC_CONFIG_PATH}" in
  "${DYNAMIC_CONFIG_DIR}"/*) ;;
  *) CORE_DYNAMIC_CONFIG_PATH="${DYNAMIC_CONFIG_DIR}/core.yml" ;;
esac

append_trusted_ips_section() {
  section_name="$1"
  trusted_ips="$2"
  output_path="$3"
  wrote_header=0
  old_ifs="${IFS}"
  IFS=','
  for raw_ip in ${trusted_ips}; do
    ip="$(printf '%s' "${raw_ip}" | tr -d '[:space:]')"
    [ -n "${ip}" ] || continue
    if [ "${wrote_header}" -eq 0 ]; then
      cat >> "${output_path}" <<EOF
    ${section_name}:
      trustedIPs:
EOF
      wrote_header=1
    fi
    printf '        - "%s"\n' "${ip}" >> "${output_path}"
  done
  IFS="${old_ifs}"
}

apply_dynamic_config_permissions() {
  if [ "$(id -u 2>/dev/null || printf '1')" = "0" ] \
    && printf '%s' "${RUNTIME_OWNER_UID}" | grep -Eq '^[0-9]+$' \
    && printf '%s' "${RUNTIME_OWNER_GID}" | grep -Eq '^[0-9]+$'; then
    chown "${RUNTIME_OWNER_UID}:${RUNTIME_OWNER_GID}" "${DYNAMIC_CONFIG_DIR}" 2>/dev/null || true
  fi
  chmod 0775 "${DYNAMIC_CONFIG_DIR}" 2>/dev/null || true
}

mkdir -p "${CONFIG_DIR}" "${DYNAMIC_CONFIG_DIR}" "${LOG_DIR}" "${STATE_DIR}"
apply_dynamic_config_permissions
touch "${STATE_DIR}/acme.json"
chmod 600 "${STATE_DIR}/acme.json" 2>/dev/null || true

cat > "${STATE_DIR}/Settings.json" <<EOF
{
  "enabled": true,
  "fqdn": "${HOSTNAME}",
  "fqdn_aliases": "${HOSTNAME_ALIASES}",
  "deployment_profile": "${DEPLOYMENT_PROFILE}",
  "deployment_profile_label": "${DEPLOYMENT_PROFILE_LABEL}",
  "acme_email": "${ACME_EMAIL}",
  "local_ca_enabled": ${LOCAL_CA_ENABLED},
  "local_ca_cert_path": "${LOCAL_CA_CERT_PATH}",
  "local_tls_cert_path": "${LOCAL_TLS_CERT_PATH}",
  "local_tls_key_path": "${LOCAL_TLS_KEY_PATH}",
  "public_base_url": "${BOREALIS_PUBLIC_BASE_URL:-https://${HOSTNAME}}",
  "public_vnc_path": "${BOREALIS_PUBLIC_VNC_PATH:-/remote-desktop/vnc}",
  "public_wireguard_host": "${BOREALIS_PUBLIC_WIREGUARD_HOST:-${HOSTNAME}}",
  "public_wireguard_port": ${BOREALIS_PUBLIC_WIREGUARD_PORT:-30000},
  "health_port": ${HEALTH_PORT},
  "http_port": ${BOREALIS_PUBLIC_HTTP_PORT:-80},
  "https_port": ${BOREALIS_PUBLIC_HTTPS_PORT:-443},
  "engine_upstream_host": "127.0.0.1",
  "engine_upstream_port": 5000,
  "webui_upstream_host": "127.0.0.1",
  "webui_upstream_port": ${WEBUI_UPSTREAM_PORT},
  "vnc_upstream_host": "127.0.0.1",
  "vnc_upstream_port": ${BOREALIS_VNC_WS_PORT:-4823},
  "settings_path": "${STATE_DIR}/Settings.json",
  "runtime_env_path": "${SERVICE_ROOT}/env/runtime.env",
  "acme_storage_path": "${STATE_DIR}/acme.json",
  "traefik_static_config_path": "${CONFIG_DIR}/traefik.yml",
  "traefik_dynamic_config_path": "${CORE_DYNAMIC_CONFIG_PATH}",
  "traefik_dynamic_config_directory": "${DYNAMIC_CONFIG_DIR}",
  "traefik_site_worker_route_pattern": "${DYNAMIC_CONFIG_DIR}/site-worker-<worker_guid>.yml",
  "logs_directory": "${LOG_DIR}"
}
EOF

cat > "${CONFIG_DIR}/traefik.yml" <<EOF
entryPoints:
  borealis-health:
    address: "127.0.0.1:${HEALTH_PORT}"
  web:
    address: ":${BOREALIS_PUBLIC_HTTP_PORT:-80}"
EOF
append_trusted_ips_section "forwardedHeaders" "${FORWARDED_HEADERS_TRUSTED_IPS}" "${CONFIG_DIR}/traefik.yml"
cat >> "${CONFIG_DIR}/traefik.yml" <<EOF
  websecure:
    address: ":${BOREALIS_PUBLIC_HTTPS_PORT:-443}"
EOF
append_trusted_ips_section "forwardedHeaders" "${FORWARDED_HEADERS_TRUSTED_IPS}" "${CONFIG_DIR}/traefik.yml"
append_trusted_ips_section "proxyProtocol" "${PROXY_PROTOCOL_TRUSTED_IPS}" "${CONFIG_DIR}/traefik.yml"
cat >> "${CONFIG_DIR}/traefik.yml" <<EOF
ping:
  entryPoint: borealis-health
providers:
  file:
    directory: "${DYNAMIC_CONFIG_DIR}"
    watch: true
log:
  level: INFO
  filePath: "${LOG_DIR}/traefik.log"
accessLog:
  filePath: "${LOG_DIR}/traefik-access.log"
EOF

if [ "${DEPLOYMENT_PROFILE}" = "internal-only" ] && [ "${LOCAL_CA_ENABLED}" = "1" ]; then
  if [ ! -s "${LOCAL_TLS_CERT_PATH}" ] || [ ! -s "${LOCAL_TLS_KEY_PATH}" ]; then
    echo "Internal-Only profile requires local TLS certificate and key." >&2
    exit 1
  fi
  TLS_BLOCK="      tls: {}"
  TLS_STATIC_BLOCK="tls:\n  stores:\n    default:\n      defaultCertificate:\n        certFile: \"${LOCAL_TLS_CERT_PATH}\"\n        keyFile: \"${LOCAL_TLS_KEY_PATH}\""
elif [ -n "${ACME_EMAIL}" ] && [ "${HOSTNAME}" != "localhost" ]; then
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
  TLS_STATIC_BLOCK=""
else
  TLS_BLOCK="      tls: {}"
  TLS_STATIC_BLOCK=""
fi

cat > "${CORE_DYNAMIC_CONFIG_PATH}" <<EOF
$(printf "%b\n" "${TLS_STATIC_BLOCK}")
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
          - url: "http://127.0.0.1:${WEBUI_UPSTREAM_PORT}"
    borealis-vnc:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:${BOREALIS_VNC_WS_PORT:-4823}"
EOF

exec traefik --configFile="${CONFIG_DIR}/traefik.yml"
