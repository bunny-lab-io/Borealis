package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type bootstrapHostFile struct {
	path    string // Fixed root-relative path, never supplied by a request.
	mode    os.FileMode
	data    []byte
	pending string // Deterministic name bound to the durable request, not a glob.
}

// This is the fixed central-SSH baseline, not Engine.sh startup or a target's
// ambient configuration. Source-owned validated network settings are the only
// variable bytes. Pure renderer parity tests keep the existing install contract
// synchronized. Rendering alone grants no file/service mutation authority.
func bootstrapPrejoinHostFiles(expected clusterbootstrap.PreparationExpected) ([]bootstrapHostFile, error) {
	if expected.Validate() != nil {
		return nil, errBootstrapSource
	}
	peers := make([]string, len(expected.PeerAddresses))
	for i, peer := range expected.PeerAddresses {
		peers[i] = peer + "/32"
	}
	baseline := fmt.Sprintf(`# Borealis-managed K3s baseline. Engine.sh owns this file.
write-kubeconfig-mode: "0600"
cluster-cidr: "%s"
service-cidr: "%s"
disable:
  - "traefik"
  - "servicelb"
secrets-encryption: true
embedded-registry: true
node-label:
  - "borealis.io/engine-node=true"
`, expected.PodCIDR, expected.ServiceCIDR)
	unit := fmt.Sprintf(`[Unit]
Description=Borealis K3s API firewall
Wants=network-online.target
After=network-online.target
Before=k3s.service

[Service]
Type=oneshot
Environment=BOREALIS_K3S_API_PORT=6443
Environment=BOREALIS_K3S_CLUSTER_CIDR=%s
Environment=BOREALIS_K3S_SERVICE_CIDR=%s
Environment="BOREALIS_K3S_PEER_CIDRS=%s"
ExecStart=/usr/local/lib/borealis/k3s-api-firewall.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`, expected.PodCIDR, expected.ServiceCIDR, strings.Join(peers, ","))
	files := []bootstrapHostFile{
		{path: "etc/rancher/k3s/config.yaml.d/10-borealis.yaml", mode: 0644, data: []byte(baseline)},
		{path: "etc/rancher/k3s/registries.yaml", mode: 0644, data: []byte(bootstrapPrejoinRegistries)},
		{path: "usr/local/lib/borealis/k3s-api-firewall.sh", mode: 0755, data: []byte(bootstrapPrejoinFirewall)},
		{path: "etc/systemd/system/borealis-k3s-api-firewall.service", mode: 0644, data: []byte(unit)},
	}
	// Match the outer journal's stable target identity across worker takeover.
	// A crash leaves a precisely discoverable request-owned scratch name, never
	// authority to delete arbitrary similarly prefixed files. Fresh writes still
	// refuse any existing scratch; cleanup/recovery needs distinct executor proof.
	target := expected.Target
	target.HolderID, target.Generation, target.OperationAttempt = "", 0, 0
	for i := range files {
		raw, err := json.Marshal(struct {
			Domain, Source, Path string
			Target               clusterbootstrap.SessionBinding
			Mode                 os.FileMode
			Data                 []byte
		}{"prejoin-host-file-v1", expected.Source.SourceSHA, files[i].path, target, files[i].mode, files[i].data})
		if err != nil {
			return nil, errBootstrapSource
		}
		digest := sha256.Sum256(raw)
		files[i].pending = ".borealis-host-" + hex.EncodeToString(digest[:])
	}
	return files, nil
}

const bootstrapPrejoinRegistries = `# Borealis-managed Spegel registry sources. Engine.sh owns this file.
mirrors:
  docker.io:
  ghcr.io:
  quay.io:
  registry.k8s.io:
`

const bootstrapPrejoinFirewall = `#!/usr/bin/env sh
set -eu

CHAIN="BOREALIS-K3S-API"
PORT="${BOREALIS_K3S_API_PORT:-6443}"
CLUSTER_CIDR="${BOREALIS_K3S_CLUSTER_CIDR:-10.42.0.0/16}"
SERVICE_CIDR="${BOREALIS_K3S_SERVICE_CIDR:-10.43.0.0/16}"
PEER_CIDRS="${BOREALIS_K3S_PEER_CIDRS:-}"

# Restore changes only this chain in one commit. Existing jumps never observe
# an empty allowlist during a retry; unrelated chains/rules remain untouched.
case "${PORT}" in ''|*[!0-9]*) exit 1 ;; esac
case "${CLUSTER_CIDR},${SERVICE_CIDR},${PEER_CIDRS}" in *[!0-9.,/]*) exit 1 ;; esac
{
  printf '*filter\n:%s - [0:0]\n' "${CHAIN}"
  printf '%s\n' "-A ${CHAIN} -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT"
  printf '%s\n' "-A ${CHAIN} -i lo -j ACCEPT"
  printf '%s\n' "-A ${CHAIN} -s 127.0.0.0/8 -j ACCEPT"
  for cidr in "${CLUSTER_CIDR}" "${SERVICE_CIDR}"; do
    printf '%s\n' "-A ${CHAIN} -i cni+ -s ${cidr} -j ACCEPT"
    printf '%s\n' "-A ${CHAIN} -i flannel+ -s ${cidr} -j ACCEPT"
  done
  if [ -n "${PEER_CIDRS}" ]; then
    old_ifs="${IFS}"
    IFS=','
    for cidr in ${PEER_CIDRS}; do
      printf '%s\n' "-A ${CHAIN} -s ${cidr} -j ACCEPT"
    done
    IFS="${old_ifs}"
  fi
  printf '%s\n' "-A ${CHAIN} -j DROP" COMMIT
} | iptables-restore --noflush --wait 5

if ! iptables -C INPUT -p tcp --dport "${PORT}" -j "${CHAIN}" 2>/dev/null; then
  iptables -I INPUT 1 -p tcp --dport "${PORT}" -j "${CHAIN}"
fi
for tcp_ports in 2379:2381 10250 5001; do
  if ! iptables -C INPUT -p tcp --dport "${tcp_ports}" -j "${CHAIN}" 2>/dev/null; then
    iptables -I INPUT 1 -p tcp --dport "${tcp_ports}" -j "${CHAIN}"
  fi
done
for udp_port in 8472 51820 51821; do
  if ! iptables -C INPUT -p udp --dport "${udp_port}" -j "${CHAIN}" 2>/dev/null; then
    iptables -I INPUT 1 -p udp --dport "${udp_port}" -j "${CHAIN}"
  fi
done
`
