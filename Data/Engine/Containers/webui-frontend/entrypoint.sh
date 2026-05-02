#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

cd /opt/Borealis/Data/Engine/web-interface

mode="${BOREALIS_WEBUI_MODE:-prod}"
port="${BOREALIS_WEBUI_UPSTREAM_PORT:-8000}"
case "${mode}" in
  dev|developer)
    export NODE_ENV=development
    export BOREALIS_DEV_UI_PROXY_ENABLED=1
    exec npm run dev -- --host 127.0.0.1 --port "${port}" --strictPort --open false
    ;;
  prod|production|"")
    export NODE_ENV=production
    exec npm exec -- vite preview --host 127.0.0.1 --port "${port}" --strictPort
    ;;
  *)
    echo "Unsupported BOREALIS_WEBUI_MODE '${mode}'. Use prod or dev." >&2
    exit 2
    ;;
esac
