#!/bin/sh
set -eu

socket_path="${BOREALIS_WIREGUARD_CONTROL_SOCKET:-/opt/Borealis/Engine/Services/wireguard-tunnel/run/control.sock}"
interface="${BOREALIS_WIREGUARD_INTERFACE:-borealis-wg}"
peer_network="${BOREALIS_WIREGUARD_PEER_NETWORK:-10.255.0.0/16}"

test -S "${socket_path}"
borealis-wireguard-control-client status >/dev/null
ip link show dev "${interface}" >/dev/null
wg show "${interface}" listen-port | grep -Eq '^[1-9][0-9]{0,4}$'
ip route show "${peer_network}" dev "${interface}" | grep -Fq "${peer_network}"
