package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func bootstrapHostFilesFixture() clusterbootstrap.PreparationExpected {
	r := identityRuntimeRequestFixture()
	return clusterbootstrap.PreparationExpected{
		Source: clusterbootstrap.Expected{Repository: r.Repository, Release: r.Release, SourceSHA: r.SourceSHA, AllowQualification: true}, Target: r.Binding,
		CohortSHA256: strings.Repeat("c", 64), TargetBootID: "55555555-5555-4555-8555-555555555555", KubeSystemUID: "66666666-6666-4666-8666-666666666666",
		ControlPlaneVIP: "192.168.3.248", EdgeVIP: "192.168.3.249", ManagementCIDR: "192.168.3.0/24", K3sVersion: "v1.36.3+k3s1", PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16",
		PeerAddresses: []string{"192.168.3.250", "192.168.3.251", "192.168.3.252"},
	}
}

// Execute only the four literal-output rendering functions, never installer
// startup, generated firewall script, service commands or host effects.
func bootstrapHostRendererFixture(t *testing.T, name string, expected clusterbootstrap.PreparationExpected) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../../../../../Engine.sh")
	if err != nil {
		t.Fatal(err)
	}
	marker := name + "() {\n"
	if strings.Count(string(raw), marker) != 1 {
		t.Fatal("missing or ambiguous baseline renderer", name)
	}
	_, tail, _ := strings.Cut(string(raw), marker)
	body, _, ok := strings.Cut(tail, "\n}\n")
	if !ok {
		t.Fatal("renderer boundary missing", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	script := "set -euo pipefail\n" + marker + body + "\n}\n" + name
	command := exec.CommandContext(ctx, "/usr/bin/bash", "-c", script)
	peers := make([]string, len(expected.PeerAddresses))
	for i, address := range expected.PeerAddresses {
		peers[i] = address + "/32"
	}
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "K3S_KUBECONFIG_MODE=0600", "K3S_CLUSTER_CIDR=" + expected.PodCIDR,
		"K3S_SERVICE_CIDR=" + expected.ServiceCIDR, "K3S_CONTAINER_LOG_MAX_SIZE=", "K3S_CONTAINER_LOG_MAX_FILES=", "K3S_SERVICE_NAME=k3s",
		"K3S_API_PORT=6443", "K3S_PEER_CIDRS=" + strings.Join(peers, ","), "K3S_FIREWALL_SCRIPT=/usr/local/lib/borealis/k3s-api-firewall.sh"}
	output, err := command.Output()
	if err != nil {
		t.Fatal("isolated renderer failed", name, err)
	}
	return output
}

func TestBootstrapHostFilesInstallerParity(t *testing.T) {
	for _, alternate := range []bool{false, true} {
		expected := bootstrapHostFilesFixture()
		if alternate {
			expected.PodCIDR, expected.ServiceCIDR = "172.20.0.0/16", "172.21.0.0/16"
		}
		files, err := bootstrapPrejoinHostFiles(expected)
		if err != nil || len(files) != 4 {
			t.Fatal("fixed host inventory missing", err)
		}
		for i, function := range []string{"render_k3s_borealis_config", "render_k3s_registries_config", "render_k3s_api_firewall_script", "render_k3s_api_firewall_unit"} {
			want := bootstrapHostRendererFixture(t, function, expected)
			if !bytes.Equal(files[i].data, want) {
				t.Fatal("native host renderer diverged from installer", function)
			}
			if files[i].mode != []os.FileMode{0644, 0644, 0755, 0644}[i] || files[i].path != []string{
				"etc/rancher/k3s/config.yaml.d/10-borealis.yaml", "etc/rancher/k3s/registries.yaml",
				"usr/local/lib/borealis/k3s-api-firewall.sh", "etc/systemd/system/borealis-k3s-api-firewall.service"}[i] {
				t.Fatal("fixed file path/mode changed")
			}
		}
	}
}

func TestBootstrapHostFilesBindingAndOwnership(t *testing.T) {
	for _, mode := range []string{"valid", "missing peer", "foreign peer", "CIDR injection", "overlap", "invalid claim", "zero expected"} {
		t.Run(mode, func(t *testing.T) {
			expected := bootstrapHostFilesFixture()
			switch mode {
			case "missing peer":
				expected.PeerAddresses = expected.PeerAddresses[:2]
			case "foreign peer":
				expected.PeerAddresses[0] = "192.168.4.250"
			case "CIDR injection":
				expected.PodCIDR += "\nExecStart=/bin/false"
			case "overlap":
				expected.PodCIDR = expected.ServiceCIDR
			case "invalid claim":
				expected.Target.Generation = 0
			case "zero expected":
				expected = clusterbootstrap.PreparationExpected{}
			}
			files, err := bootstrapPrejoinHostFiles(expected)
			if (err == nil) != (mode == "valid") || err != nil && files != nil {
				t.Fatal("invalid network/claim generated host files", err)
			}
			if err != nil {
				return
			}
			first := bytes.Clone(files[3].data)
			t.Setenv("BOREALIS_K3S_PEER_CIDRS", "0.0.0.0/0")
			t.Setenv("BOREALIS_K3S_CLUSTER_CIDR", "10.99.0.0/16")
			files[3].data[0] = '!'
			again, err := bootstrapPrejoinHostFiles(expected)
			if err != nil || !bytes.Equal(again[3].data, first) {
				t.Fatal("ambient environment or returned alias changed plan", err)
			}
			expected.Target.Generation++
			expected.Target.OperationAttempt++
			takeover, err := bootstrapPrejoinHostFiles(expected)
			if err != nil || takeover[3].pending != again[3].pending {
				t.Fatal("worker takeover lost deterministic scratch binding", err)
			}
			expected.Target.TargetID = "99999999-9999-4999-8999-999999999999"
			foreign, err := bootstrapPrejoinHostFiles(expected)
			if err != nil || foreign[3].pending == again[3].pending {
				t.Fatal("scratch binding ignored target identity", err)
			}
			expected.PeerAddresses[0] = "192.168.3.247"
			if !bytes.Equal(again[3].data, first) {
				t.Fatal("caller mutation changed rendered bytes")
			}
		})
	}
}
