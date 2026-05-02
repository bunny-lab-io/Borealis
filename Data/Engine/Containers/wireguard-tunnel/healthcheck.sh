#!/bin/sh
set -eu

socket_path="${BOREALIS_WIREGUARD_CONTROL_SOCKET:-/opt/Borealis/Engine/Services/wireguard-tunnel/run/control.sock}"

test -S "${socket_path}"
