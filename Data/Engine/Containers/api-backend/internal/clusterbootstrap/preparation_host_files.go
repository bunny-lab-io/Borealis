package clusterbootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
)

// PreparationHostFile is fixed renderer output only. It carries no mutation,
// source custody, installed-state or admission authority.
type PreparationHostFile struct {
	Path string // Fixed root-relative destination.
	Mode os.FileMode
	Data []byte
}

// RenderPreparationHostFiles is shared by native publication and storage
// accounting. Network inputs remain validated source-owned cohort metadata.
func RenderPreparationHostFiles(expected PreparationExpected) ([]PreparationHostFile, error) {
	if expected.Validate() != nil {
		return nil, ErrPreparationConfig
	}
	return renderPreparationHostFiles(expected.PodCIDR, expected.ServiceCIDR, expected.PeerAddresses), nil
}

func renderPreparationHostFiles(podCIDR, serviceCIDR string, peerAddresses []string) []PreparationHostFile {
	peers := make([]string, len(peerAddresses))
	for i, peer := range peerAddresses {
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
`, podCIDR, serviceCIDR)
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
`, podCIDR, serviceCIDR, strings.Join(peers, ","))
	files := []PreparationHostFile{
		{Path: "etc/rancher/k3s/config.yaml.d/10-borealis.yaml", Mode: 0644, Data: []byte(baseline)},
		{Path: "etc/rancher/k3s/registries.yaml", Mode: 0644, Data: []byte(preparationPrejoinRegistries)},
		{Path: "usr/local/lib/borealis/k3s-api-firewall.sh", Mode: 0755, Data: []byte(PreparationHostFirewall)},
		{Path: "etc/systemd/system/borealis-k3s-api-firewall.service", Mode: 0644, Data: []byte(unit)},
	}
	return files
}

const PreparationDockerKeySHA256 = "1500c1f56fa9e26b9b8f42452a553675796ade0807cdce11975eb98170b3a570"
const PreparationDockerKeyLimit = 16 << 10
const preparationDockerKeyPath = "etc/apt/keyrings/docker.asc"
const preparationDockerSourcePath = "etc/apt/sources.list.d/docker.sources"

var preparationDockerSuite = regexp.MustCompile(`^[a-z][a-z0-9]{0,31}$`)

func ValidPreparationDockerKey(raw []byte) bool {
	digest := sha256.Sum256(raw)
	return len(raw) > 0 && len(raw) <= PreparationDockerKeyLimit && hex.EncodeToString(digest[:]) == PreparationDockerKeySHA256
}

func preparationDockerSource(suite string) ([]byte, error) {
	if !preparationDockerSuite.MatchString(suite) {
		return nil, ErrPreparationConfig
	}
	return []byte("Types: deb\nURIs: https://download.docker.com/linux/ubuntu\nSuites: " + suite + "\nComponents: stable\nArchitectures: amd64\nSigned-By: /etc/apt/keyrings/docker.asc\n"), nil
}

// RenderPreparationDockerFiles requires the exact pinned public key. Rendering
// cannot download a key, publish files, select packages or start services.
func RenderPreparationDockerFiles(expected PreparationExpected, suite string, key []byte) ([]PreparationHostFile, error) {
	if expected.Validate() != nil || !ValidPreparationDockerKey(key) {
		return nil, ErrPreparationConfig
	}
	source, err := preparationDockerSource(suite)
	if err != nil {
		return nil, err
	}
	return []PreparationHostFile{
		{Path: preparationDockerKeyPath, Mode: 0644, Data: bytes.Clone(key)},
		{Path: preparationDockerSourcePath, Mode: 0644, Data: source},
	}, nil
}

// PreparationHostFileDemand identifies a destination directory for native
// filesystem mapping, never a regular file. File rows contain exact rendered
// lengths except the key's existing transport bound. Separate zero-byte rows
// bound every potentially created ancestor once; existing parents may therefore
// be conservatively overcounted. No arbitrary byte reserve or second rename
// copy is added. This content calculation grants no observation or authority.
type PreparationHostFileDemand struct {
	Path    string
	Bytes   uint64
	Entries uint64
}

func PreparationHostFileDemands(expected PreparationExpected, suite string) ([]PreparationHostFileDemand, error) {
	files, err := RenderPreparationHostFiles(expected)
	if err != nil {
		return nil, err
	}
	return preparationHostFileDemands(files, suite)
}

// PreparationPlanHostFileDemands computes proposed content bounds using the
// same renderer without inventing a future worker identity. A plan cannot be
// used by native publication and these rows grant no file mutation authority.
func PreparationPlanHostFileDemands(plan PreparationPlan, suite string) ([]PreparationHostFileDemand, error) {
	if plan.Source.Validate() != nil || !digestPattern.MatchString(plan.PlanSHA256) || !nonzeroPreparationUUID(plan.KubeSystemUID) ||
		plan.K3sVersion != K3sPins().Version || len(plan.TargetAddresses) < 1 || len(plan.TargetAddresses) > 2 {
		return nil, ErrPreparationConfig
	}
	seen := make(map[string]bool)
	for _, target := range plan.TargetAddresses {
		if seen[target] || validatePreparationNetwork(plan.ManagementCIDR, plan.PodCIDR, plan.ServiceCIDR, plan.ControlPlaneVIP, plan.EdgeVIP, target, plan.PeerAddresses) != nil {
			return nil, ErrPreparationConfig
		}
		seen[target] = true
	}
	return preparationHostFileDemands(renderPreparationHostFiles(plan.PodCIDR, plan.ServiceCIDR, plan.PeerAddresses), suite)
}

func preparationHostFileDemands(files []PreparationHostFile, suite string) ([]PreparationHostFileDemand, error) {
	source, err := preparationDockerSource(suite)
	if err != nil {
		return nil, err
	}
	files = append(files, PreparationHostFile{Path: preparationDockerSourcePath, Data: source})
	var result []PreparationHostFileDemand
	parents := make(map[string]bool)
	add := func(name string, size uint64) {
		directory := path.Dir("/" + name)
		result = append(result, PreparationHostFileDemand{Path: directory, Bytes: size, Entries: 1})
		for parent := directory; parent != "/"; parent = path.Dir(parent) {
			parents[parent] = true
		}
	}
	for _, file := range files {
		add(file.Path, uint64(len(file.Data)))
	}
	// A hash identifies bytes, not their length. Use the established 16KiB key
	// transport limit without pretending the controller acquired or proved a key.
	add(preparationDockerKeyPath, PreparationDockerKeyLimit)
	for parent := range parents {
		result = append(result, PreparationHostFileDemand{Path: parent, Entries: 1})
	}
	slices.SortStableFunc(result, func(a, b PreparationHostFileDemand) int {
		if order := strings.Compare(a.Path, b.Path); order != 0 {
			return order
		}
		if a.Bytes < b.Bytes {
			return -1
		}
		if a.Bytes > b.Bytes {
			return 1
		}
		return 0
	})
	return result, nil
}

const preparationPrejoinRegistries = `# Borealis-managed Spegel registry sources. Engine.sh owns this file.
mirrors:
  docker.io:
  ghcr.io:
  quay.io:
  registry.k8s.io:
`

const PreparationHostFirewall = `#!/usr/bin/env sh
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
