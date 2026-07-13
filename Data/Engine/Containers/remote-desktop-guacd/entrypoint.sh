#!/bin/sh
set -eu

bind_host="${BOREALIS_GUACD_BIND_HOST:-127.0.0.1}"
port="${BOREALIS_GUACD_PORT:-4822}"
log_level="${BOREALIS_GUACD_LOG_LEVEL:-info}"
log_dir="${BOREALIS_GUACD_LOG_DIR:-/opt/borealis/logs}"
log_file="${BOREALIS_GUACD_LOG_FILE:-${log_dir}/guacd.log}"
guacd_bin="${BOREALIS_GUACD_BIN:-/opt/guacamole/sbin/guacd}"

mkdir -p "${log_dir}" 2>/dev/null || true

if command -v tee >/dev/null 2>&1 && touch "${log_file}" 2>/dev/null; then
  {
    printf '[%s] starting guacd bind=%s port=%s level=%s\n' "$(date -Iseconds)" "${bind_host}" "${port}" "${log_level}"
  } >>"${log_file}" 2>/dev/null || true
  exec sh -c '"$1" -b "$2" -l "$3" -f -L "$4" 2>&1 | tee -a "$5"' sh "${guacd_bin}" "${bind_host}" "${port}" "${log_level}" "${log_file}"
fi

exec "${guacd_bin}" -b "${bind_host}" -l "${port}" -f -L "${log_level}"
