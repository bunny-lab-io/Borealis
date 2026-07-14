#!/bin/sh
set -eu

bind_host="${BOREALIS_GUACD_BIND_HOST:-127.0.0.1}"
port="${BOREALIS_GUACD_PORT:-4822}"
log_level="${BOREALIS_GUACD_LOG_LEVEL:-info}"
log_dir="${BOREALIS_GUACD_LOG_DIR:-/opt/borealis/logs}"
log_file="${BOREALIS_GUACD_LOG_FILE:-${log_dir}/guacd.log}"

mkdir -p "${log_dir}" 2>/dev/null || true

if command -v tee >/dev/null 2>&1 && touch "${log_file}" 2>/dev/null; then
  {
    printf '[%s] starting guacd bind=%s port=%s level=%s\n' "$(date -Iseconds)" "${bind_host}" "${port}" "${log_level}"
  } >>"${log_file}" 2>/dev/null || true
  exec sh -c 'guacd -b "$1" -l "$2" -f -L "$3" 2>&1 | tee -a "$4"' sh "${bind_host}" "${port}" "${log_level}" "${log_file}"
fi

exec guacd -b "${bind_host}" -l "${port}" -f -L "${log_level}"
