#!/usr/bin/env bash
# One-shot helper for importing a preserved legacy Engine PostgreSQL dump.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_PATH="${BASH_SOURCE[0]:-$0}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${SCRIPT_PATH}")" && pwd)"
K3S_NAMESPACE="${BOREALIS_K3S_NAMESPACE:-borealis}"
K3S_KUBECONFIG="${K3S_KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"

die() {
  printf '[%s] ERROR: %s\n' "$(date +%FT%T)" "$*" >&2
  exit 1
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

k3s_kubectl() {
  if command_exists k3s; then
    k3s kubectl --kubeconfig "${K3S_KUBECONFIG}" "$@"
    return $?
  fi
  command_exists kubectl || die "k3s or kubectl missing."
  kubectl --kubeconfig "${K3S_KUBECONFIG}" "$@"
}

main() {
  local dump_path="${1:-}"
  [[ -n "${dump_path}" && -f "${dump_path}" ]] || die "Usage: import-legacy-postgres-dump.sh <dump.sql>"
  [[ -f "${K3S_KUBECONFIG}" ]] || die "K3s kubeconfig missing at ${K3S_KUBECONFIG}. Run Engine.sh deploy first."
  command_exists k3s || command_exists kubectl || die "k3s or kubectl missing."
  k3s_kubectl -n "${K3S_NAMESPACE}" get pod postgres-db-0 >/dev/null 2>&1 \
    || die "K3s PostgreSQL pod postgres-db-0 missing in namespace ${K3S_NAMESPACE}."
  k3s_kubectl -n "${K3S_NAMESPACE}" exec -i postgres-db-0 -c postgres-db -- sh -lc \
    'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < "${dump_path}"
}

main "$@"
