package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func sourceNetworkFixture(t *testing.T) ([]byte, SourceNetwork) {
	t.Helper()
	raw, err := os.ReadFile("testdata/source-supervisor.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw, SourceNetwork{NodeUID: "33333333-3333-4333-8333-333333333333", Hostname: "engine-01", MachineID: strings.Repeat("b", 32), BootID: "22222222-2222-4222-8222-222222222222", K3sVersion: "v1.36.3+k3s1", ManagementLink: ManagementLink{Interface: "ens18", Index: 2, Address: "192.168.90.20/24", MAC: "02:00:00:00:00:01", NetworkNamespace: 1234}}
}

func TestSourceNetworkUsesSupervisorRangeAndCurrentNode(t *testing.T) {
	raw, identity := sourceNetworkFixture(t)
	network, err := ParseSourceNetwork(raw, identity)
	if err != nil || network.PodCIDR != "10.42.0.0/16" || network.ServiceCIDR != "10.43.0.0/16" {
		t.Fatalf("network projection: %v", err)
	}
	node, err := os.ReadFile("testdata/source-node.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSourceNode(node, network, "192.168.90.20"); err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(network)
	if bytes.Contains(public, []byte("synthetic-private")) || bytes.Contains(public, []byte("IPSEC")) {
		t.Fatal("supervisor fields escaped projection")
	}
	for _, replacement := range [][2]string{{"10.42.0.0/24", "10.44.0.0/24"}, {"10.42.0.0/24", "10.0.0.0/8"}, {"v1.36.3+k3s1", "v1.36.4+k3s1"}, {"192.168.90.20", "192.168.90.21"}, {"\"True\"", "\"False\""}, {"22222222-2222-4222-8222-222222222222", "22222222-2222-4222-8222-222222222223"}, {"\"podCIDRs\": [\"10.42.0.0/24\"]", "\"podCIDRs\": [\"10.42.0.0/24\",\"fd00::/64\"]"}} {
		bad := bytes.ReplaceAll(node, []byte(replacement[0]), []byte(replacement[1]))
		if bytes.Equal(bad, node) || ValidateSourceNode(bad, network, "192.168.90.20") == nil {
			t.Fatalf("node mismatch accepted: %s", replacement[0])
		}
	}
	if ValidateSourceVersion([]byte(`{"gitVersion":"v1.36.3+k3s1"}`), network.K3sVersion) != nil || ValidateSourceVersion([]byte(`{"gitVersion":"v1.36.4+k3s1"}`), network.K3sVersion) == nil {
		t.Fatal("running version not checked")
	}
}

func TestSourceNetworkRejectsAmbiguousOrUnsupportedSupervisor(t *testing.T) {
	original, identity := sourceNetworkFixture(t)
	for name, mutate := range map[string]func(map[string]any){
		"missing network": func(m map[string]any) { delete(m, "ClusterIPRanges") },
		"second stack": func(m map[string]any) {
			m["ClusterIPRanges"] = []any{map[string]any{"IP": "10.42.0.0", "Mask": "//8AAA=="}, map[string]any{"IP": "fd00::", "Mask": "//////////8AAAAAAAAAAA=="}}
		},
		"different singular": func(m map[string]any) { m["ClusterIPRange"] = map[string]any{"IP": "10.44.0.0", "Mask": "//8AAA=="} },
		"host bits":          func(m map[string]any) { m["ClusterIPRange"] = map[string]any{"IP": "10.42.0.1", "Mask": "//8AAA=="} },
		"invalid mask":       func(m map[string]any) { m["ClusterIPRange"] = map[string]any{"IP": "10.42.0.0", "Mask": "/wD/AA=="} },
		"overlap": func(m map[string]any) {
			m["ServiceIPRange"] = m["ClusterIPRange"]
			m["ServiceIPRanges"] = m["ClusterIPRanges"]
		},
		"domain":              func(m map[string]any) { m["ClusterDomain"] = "custom.local" },
		"dns":                 func(m map[string]any) { m["ClusterDNS"] = "10.43.0.11"; m["ClusterDNSs"] = []string{"10.43.0.11"} },
		"flannel":             func(m map[string]any) { m["FlannelBackend"] = "wireguard-native" },
		"missing boolean":     func(m map[string]any) { delete(m, "DisableNPC") },
		"null boolean":        func(m map[string]any) { m["DisableNPC"] = nil },
		"node":                func(m map[string]any) { m["ServerNodeName"] = "engine-02" },
		"controller override": func(m map[string]any) { m["ExtraControllerArgs"] = []string{"cluster-cidr=10.44.0.0/16"} },
		"service override":    func(m map[string]any) { m["ExtraAPIArgs"] = []string{"service-cluster-ip-range=10.44.0.0/16"} },
		"port":                func(m map[string]any) { m["SupervisorPort"] = 7443 },
	} {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal(original, &m)
			mutate(m)
			raw, _ := json.Marshal(m)
			if _, err := ParseSourceNetwork(raw, identity); err == nil {
				t.Fatal("unsupported source accepted")
			}
		})
	}
	for _, bad := range [][]byte{nil, []byte("null"), append(append([]byte{}, original...), []byte("{}")...), bytes.Replace(original, []byte(`"IP": "10.42.0.0"`), []byte(`"IP": "10.42.0.0", "ip": "10.44.0.0"`), 1), bytes.Replace(original, []byte(`"ClusterDomain": "cluster.local"`), []byte(`"ClusterDomain": "cluster.local", "clusterdomain": "other"`), 1), []byte(strings.Repeat(" ", 128<<10) + string(original)), append([]byte{0xff}, original...)} {
		if _, err := ParseSourceNetwork(bad, identity); err == nil {
			t.Fatal("ambiguous JSON accepted")
		}
	}
}
