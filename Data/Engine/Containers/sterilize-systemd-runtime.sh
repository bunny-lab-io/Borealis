#!/usr/bin/env bash
# One-shot migration helper for removing the pre-container Linux Engine runtime.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_PATH="${BASH_SOURCE[0]:-$0}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${SCRIPT_PATH}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../../.." && pwd)"
RUNTIME_ROOT="${REPO_ROOT}/Engine"
BACKUP_ROOT="${REPO_ROOT}/Engine.old"
DEPLOY_DIR="${RUNTIME_ROOT}/Deploy"

log() {
  printf '[%s] %s\n' "$(date +%FT%T)" "$*"
}

die() {
  printf '[%s] ERROR: %s\n' "$(date +%FT%T)" "$*" >&2
  exit 1
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

run_privileged() {
  if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
    "$@"
    return $?
  fi
  if command_exists sudo; then
    sudo -n "$@"
    return $?
  fi
  return 1
}

append_engine_old_gitignore() {
  if [[ -f "${REPO_ROOT}/.gitignore" ]] && ! grep -qxF "/Engine.old/" "${REPO_ROOT}/.gitignore"; then
    printf '\n/Engine.old/\n' >> "${REPO_ROOT}/.gitignore"
  fi
}

postgres_units() {
  if ! command_exists systemctl; then
    return 0
  fi
  {
    systemctl list-unit-files 'postgresql*.service' --no-legend --no-pager 2>/dev/null || true
    systemctl list-units 'postgresql*.service' --all --no-legend --no-pager 2>/dev/null || true
  } | awk '{print $1}' | grep -E '^postgresql(@.*|-.*)?\.service$|^postgresql\.service$' | sort -u
}

disable_debian_postgres_clusters() {
  if ! command_exists pg_lsclusters; then
    return 0
  fi

  local version=""
  local cluster=""
  local port=""
  local status=""
  local owner=""
  local data_dir=""
  local log_file=""
  while read -r version cluster port status owner data_dir log_file; do
    [[ -n "${version}" && -n "${cluster}" ]] || continue
    run_privileged pg_ctlcluster "${version}" "${cluster}" stop >/dev/null 2>&1 || true
    local start_conf="/etc/postgresql/${version}/${cluster}/start.conf"
    if [[ -f "${start_conf}" ]]; then
      run_privileged sed -i -E 's/^[[:space:]]*(auto|manual|disabled)[[:space:]]*$/disabled/' "${start_conf}" >/dev/null 2>&1 || true
    fi
  done < <(pg_lsclusters -h 2>/dev/null || true)
}

dump_legacy_database() {
  if ! command_exists pg_dump; then
    log "pg_dump missing; legacy PostgreSQL dump skipped."
    return 0
  fi

  mkdir -p "${DEPLOY_DIR}"
  local dump_path="${DEPLOY_DIR}/legacy-postgres-borealis-$(date +%Y%m%d%H%M%S).sql"
  if pg_dump -h 127.0.0.1 -U borealis -d borealis -f "${dump_path}" >/dev/null 2>&1; then
    log "Legacy PostgreSQL dump written to ${dump_path}."
  elif run_privileged runuser -u postgres -- pg_dump -d borealis -f "${dump_path}" >/dev/null 2>&1; then
    log "Legacy PostgreSQL dump written to ${dump_path}."
  else
    rm -f "${dump_path}" 2>/dev/null || true
    log "Host PostgreSQL dump skipped; borealis database was not reachable."
  fi
}

stop_borealis_units() {
  if ! command_exists systemctl; then
    log "systemctl missing; unit cleanup skipped."
    return 0
  fi

  local unit=""
  for unit in borealis-engine.service borealis-traefik.service borealis-guacd.service; do
    run_privileged systemctl stop "${unit}" >/dev/null 2>&1 || true
    run_privileged systemctl disable "${unit}" >/dev/null 2>&1 || true
    run_privileged rm -f "/etc/systemd/system/${unit}" >/dev/null 2>&1 || true
  done

  while IFS= read -r unit; do
    [[ -n "${unit}" ]] || continue
    run_privileged systemctl stop "${unit}" >/dev/null 2>&1 || true
    run_privileged systemctl disable "${unit}" >/dev/null 2>&1 || true
    run_privileged systemctl disable --runtime "${unit}" >/dev/null 2>&1 || true
  done < <(postgres_units)

  disable_debian_postgres_clusters
  run_privileged systemctl daemon-reload >/dev/null 2>&1 || true
  run_privileged systemctl reset-failed borealis-engine.service borealis-traefik.service borealis-guacd.service >/dev/null 2>&1 || true
}

remove_wireguard_runtime() {
  if command_exists ip; then
    run_privileged ip link delete dev borealis-wg >/dev/null 2>&1 || true
  fi

  if command_exists iptables; then
    while run_privileged iptables -S 2>/dev/null | grep -q 'Borealis-WG'; do
      run_privileged iptables -S 2>/dev/null \
        | grep 'Borealis-WG' \
        | sed 's/^-A /-D /' \
        | while IFS= read -r rule; do run_privileged iptables ${rule} >/dev/null 2>&1 || true; done
      break
    done
  fi
}

rename_runtime() {
  [[ ! -e "${BACKUP_ROOT}" ]] || die "Engine.old already exists; refusing to overwrite legacy backup."
  if [[ -e "${RUNTIME_ROOT}" ]]; then
    mv "${RUNTIME_ROOT}" "${BACKUP_ROOT}"
    log "Legacy Engine runtime moved to ${BACKUP_ROOT}."
  else
    log "Legacy Engine runtime not present; no rename needed."
  fi
}

main() {
  [[ ! -e "${BACKUP_ROOT}" ]] || die "Engine.old already exists; refusing to overwrite legacy backup."
  dump_legacy_database
  stop_borealis_units
  remove_wireguard_runtime
  rename_runtime
  append_engine_old_gitignore
}

main "$@"
