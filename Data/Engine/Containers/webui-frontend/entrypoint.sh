#!/bin/sh
set -eu

workdir="${BOREALIS_WEBUI_WORKDIR:-/opt/Borealis/Data/Engine/web-interface}"
static_server="${BOREALIS_WEBUI_STATIC_SERVER_BIN:-/usr/local/bin/borealis-webui-static-server.js}"
cd "${workdir}"

mode="${BOREALIS_WEBUI_MODE:-prod}"
port="${BOREALIS_WEBUI_UPSTREAM_PORT:-8000}"
bind_host="${BOREALIS_WEBUI_BIND_HOST:-127.0.0.1}"
case "${mode}" in
  dev|developer)
    export NODE_ENV=development
    export BOREALIS_DEV_UI_PROXY_ENABLED=1
    exec npm run dev -- --host "${bind_host}" --port "${port}" --strictPort --no-open
    ;;
  prod|production|"")
    export NODE_ENV=production
    exec node "${static_server}"
    ;;
  *)
    echo "Unsupported BOREALIS_WEBUI_MODE '${mode}'. Use prod or dev." >&2
    exit 2
    ;;
esac
