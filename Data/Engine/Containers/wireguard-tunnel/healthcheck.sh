#!/bin/sh
set -eu

socket_path="${BOREALIS_WIREGUARD_CONTROL_SOCKET:-/opt/Borealis/Engine/Services/wireguard-tunnel/run/control.sock}"
interface="${BOREALIS_WIREGUARD_INTERFACE:-borealis-wg}"
peer_network="${BOREALIS_WIREGUARD_PEER_NETWORK:-10.255.0.0/16}"
edge_vip="${BOREALIS_CLUSTER_EDGE_VIP:-}"

test -S "${socket_path}"
status="$(borealis-wireguard-control-client status)"
if [ -n "${edge_vip}" ] \
  && ! ip -o -4 address show | awk '{sub(/\/.*/, "", $4); print $4}' | grep -Fxq "${edge_vip}"; then
  printf '%s\n' "${status}" | grep -Fq '"stdout":"standby"'
  if ip link show dev "${interface}" >/dev/null 2>&1; then
    printf 'Standby WireGuard interface %s remains active.\n' "${interface}" >&2
    exit 1
  fi
  exit 0
fi
ip link show dev "${interface}" >/dev/null
wg show "${interface}" listen-port | grep -Eq '^[1-9][0-9]{0,4}$'
ip route show "${peer_network}" dev "${interface}" | grep -Fq "${peer_network}"
