#!/bin/sh
set -eu

bind_host="${BOREALIS_GUACD_BIND_HOST:-127.0.0.1}"
port="${BOREALIS_GUACD_PORT:-4822}"
log_level="${BOREALIS_GUACD_LOG_LEVEL:-info}"
log_dir="${BOREALIS_GUACD_LOG_DIR:-/tmp/borealis-guacd-logs}"
log_file="${BOREALIS_GUACD_LOG_FILE:-${log_dir}/guacd.log}"
syslog_file="${BOREALIS_GUACD_SYSLOG_FILE:-${log_dir}/guacd-syslog.log}"
guacd_bin="${BOREALIS_GUACD_BIN:-/opt/guacamole/sbin/guacd}"
syslog_started=0

mkdir -p "${log_dir}" 2>/dev/null || true

cleanup() {
  if [ "${syslog_started}" = "1" ]; then
    killall syslogd >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

start_syslog_sink() {
  if [ "$(id -u)" != "0" ] || ! command -v syslogd >/dev/null 2>&1; then
    printf '[%s] guacd syslog sink unavailable user=%s syslogd=%s\n' "$(date -Iseconds)" "$(id -u)" "$(command -v syslogd 2>/dev/null || true)" >>"${log_file}" 2>/dev/null || true
    return 0
  fi

  rm -f /dev/log 2>/dev/null || true
  touch "${syslog_file}" 2>/dev/null || true
  chown guacd:guacd "${syslog_file}" 2>/dev/null || true
  syslogd -O "${syslog_file}" -s "${BOREALIS_GUACD_SYSLOG_SIZE_KB:-1024}" -b "${BOREALIS_GUACD_SYSLOG_ROTATIONS:-3}" >/dev/null 2>&1 || return 0
  chmod 0666 /dev/log 2>/dev/null || true
  syslog_started=1
  printf '[%s] guacd syslog sink started path=%s\n' "$(date -Iseconds)" "${syslog_file}" >>"${log_file}" 2>/dev/null || true
}

run_guacd() {
  if [ "$(id -u)" = "0" ] && command -v su >/dev/null 2>&1 && id guacd >/dev/null 2>&1; then
    GUACD_BIN="${guacd_bin}" GUACD_BIND_HOST_VALUE="${bind_host}" GUACD_PORT_VALUE="${port}" GUACD_LOG_LEVEL_VALUE="${log_level}" \
      exec su -s /bin/sh guacd -c 'exec "$GUACD_BIN" -b "$GUACD_BIND_HOST_VALUE" -l "$GUACD_PORT_VALUE" -f -L "$GUACD_LOG_LEVEL_VALUE"'
  fi
  exec "${guacd_bin}" -b "${bind_host}" -l "${port}" -f -L "${log_level}"
}

if command -v tee >/dev/null 2>&1 && touch "${log_file}" 2>/dev/null; then
  chown guacd:guacd "${log_file}" 2>/dev/null || true
  printf '[%s] starting guacd bind=%s port=%s level=%s user=%s\n' "$(date -Iseconds)" "${bind_host}" "${port}" "${log_level}" "$(id -u)" >>"${log_file}" 2>/dev/null || true
  start_syslog_sink
  run_guacd 2>&1 | tee -a "${log_file}"
  exit $?
fi

start_syslog_sink
run_guacd
