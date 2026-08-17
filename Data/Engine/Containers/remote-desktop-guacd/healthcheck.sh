#!/bin/sh
set -eu

host="${BOREALIS_GUACD_HEALTH_HOST:-127.0.0.1}"
port="${BOREALIS_GUACD_PORT:-4822}"

if command -v nc >/dev/null 2>&1; then
  nc -z "${host}" "${port}"
  exit $?
fi

hex_port="$(printf '%04X' "${port}")"
awk -v port="${hex_port}" '
  toupper($2) ~ ":" port "$" && $4 == "0A" {
    found = 1
  }
  END {
    exit found ? 0 : 1
  }
' /proc/net/tcp /proc/net/tcp6 2>/dev/null
