package clusterbootstrap

import (
	"bytes"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"
)

func TestPreparationHostFileDemands(t *testing.T) {
	key, err := os.ReadFile("../../cmd/borealis-node-manager/testdata/docker-archive-key.asc")
	if err != nil || !ValidPreparationDockerKey(key) {
		t.Fatal("reviewed key fixture", err)
	}
	for _, alternate := range []bool{false, true} {
		expected, _ := preparationFixture()
		suite := "noble"
		if alternate {
			expected.PodCIDR, expected.ServiceCIDR = "172.20.0.0/16", "172.21.0.0/16"
			expected.ManagementCIDR = "10.1.0.0/16"
			expected.Target.Address, expected.ControlPlaneVIP, expected.EdgeVIP = "10.1.0.10", "10.1.0.1", "10.1.0.2"
			expected.PeerAddresses = []string{"10.1.0.10", "10.1.0.11", "10.1.0.9"}
			suite = strings.Repeat("n", 32)
		}
		files, err := RenderPreparationHostFiles(expected)
		if err != nil {
			t.Fatal(err)
		}
		docker, err := RenderPreparationDockerFiles(expected, suite, key)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, docker...)
		demands, err := PreparationHostFileDemands(expected, suite)
		if err != nil {
			t.Fatal(err)
		}
		fileRows, parentRows := map[string]PreparationHostFileDemand{}, map[string]bool{}
		for _, d := range demands {
			if d.Path == "/" || path.Clean(d.Path) != d.Path || !path.IsAbs(d.Path) || d.Entries != 1 {
				t.Fatal("invalid fixed directory demand", d)
			}
			if d.Bytes == 0 {
				if parentRows[d.Path] {
					t.Fatal("duplicate parent", d)
				}
				parentRows[d.Path] = true
			} else {
				if _, exists := fileRows[d.Path]; exists {
					t.Fatal("duplicate file copy", d)
				}
				fileRows[d.Path] = d
			}
		}
		if len(fileRows) != 6 {
			t.Fatal("generated file family omitted", fileRows)
		}
		wantParents := map[string]bool{}
		for _, f := range files {
			directory := path.Dir("/" + f.Path)
			want := uint64(len(f.Data))
			if f.Path == preparationDockerKeyPath {
				want = PreparationDockerKeyLimit
			}
			if fileRows[directory].Bytes != want {
				t.Fatal("renderer length or existing key bound differs", f.Path)
			}
			for parent := directory; parent != "/"; parent = path.Dir(parent) {
				wantParents[parent] = true
			}
		}
		if !reflect.DeepEqual(parentRows, wantParents) {
			t.Fatal("parent creation bound differs", parentRows, wantParents)
		}
		original := append([]PreparationHostFileDemand(nil), demands...)
		demands[0].Entries = 99
		again, err := PreparationHostFileDemands(expected, suite)
		if err != nil || !reflect.DeepEqual(again, original) {
			t.Fatal("demand aliases caller state", err)
		}
		first := bytes.Clone(files[0].Data)
		files[0].Data[0] ^= 1
		rendered, err := RenderPreparationHostFiles(expected)
		if err != nil || !bytes.Equal(first, rendered[0].Data) {
			t.Fatal("rendered bytes alias previous call", err)
		}
	}
}

func TestPreparationHostFileRenderingRejectsUnprovenInputs(t *testing.T) {
	expected, _ := preparationFixture()
	for _, suite := range []string{"", "Noble", "noble\nSuites: foreign", "noble stable", strings.Repeat("n", 33)} {
		if got, err := PreparationHostFileDemands(expected, suite); err == nil || got != nil {
			t.Fatal("invalid suite created demand", suite)
		}
	}
	for _, changed := range []string{"empty", "CIDR", "peers", "binding"} {
		invalid := expected
		switch changed {
		case "empty":
			invalid = PreparationExpected{}
		case "CIDR":
			invalid.PodCIDR += "\nservice-cidr: invalid"
		case "peers":
			invalid.PeerAddresses = invalid.PeerAddresses[:2]
		case "binding":
			invalid.Target.Generation = 0
		}
		if got, err := PreparationHostFileDemands(invalid, "noble"); err == nil || got != nil {
			t.Fatal("invalid source/target created demand", changed)
		}
	}
	for _, key := range [][]byte{nil, []byte("unproved key"), bytes.Repeat([]byte("x"), PreparationDockerKeyLimit), bytes.Repeat([]byte("x"), PreparationDockerKeyLimit+1)} {
		if got, err := RenderPreparationDockerFiles(expected, "noble", key); err == nil || got != nil {
			t.Fatal("demand/key size substituted for pinned key proof")
		}
	}
}

func TestPreparationPlanHostFileDemandsWithoutClaim(t *testing.T) {
	expected, _ := preparationFixture()
	plan := PreparationPlan{Source: expected.Source, PlanSHA256: strings.Repeat("a", 64), KubeSystemUID: expected.KubeSystemUID, K3sVersion: expected.K3sVersion,
		ManagementCIDR: expected.ManagementCIDR, PodCIDR: expected.PodCIDR, ServiceCIDR: expected.ServiceCIDR, ControlPlaneVIP: expected.ControlPlaneVIP, EdgeVIP: expected.EdgeVIP,
		PeerAddresses: append([]string(nil), expected.PeerAddresses...), TargetAddresses: []string{expected.Target.Address}}
	want, err := PreparationHostFileDemands(expected, "noble")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "two targets", "empty", "missing source", "missing plan", "missing namespace", "version", "missing targets", "duplicate targets", "foreign target", "CIDR", "unordered peers", "invalid suite"} {
		t.Run(mode, func(t *testing.T) {
			p := plan
			suite := "noble"
			switch mode {
			case "two targets":
				for _, peer := range p.PeerAddresses {
					if peer != p.TargetAddresses[0] {
						p.TargetAddresses = []string{p.TargetAddresses[0], peer}
						break
					}
				}
			case "empty":
				p = PreparationPlan{}
			case "missing source":
				p.Source = Expected{}
			case "missing plan":
				p.PlanSHA256 = ""
			case "missing namespace":
				p.KubeSystemUID = ""
			case "version":
				p.K3sVersion = "v1.1.0+k3s1"
			case "missing targets":
				p.TargetAddresses = nil
			case "duplicate targets":
				p.TargetAddresses = []string{p.TargetAddresses[0], p.TargetAddresses[0]}
			case "foreign target":
				p.TargetAddresses = []string{"192.168.3.240"}
			case "CIDR":
				p.PodCIDR += "\ninvalid"
			case "unordered peers":
				p.PeerAddresses = []string{p.PeerAddresses[2], p.PeerAddresses[1], p.PeerAddresses[0]}
			case "invalid suite":
				suite = "noble\nforeign"
			}
			got, err := PreparationPlanHostFileDemands(p, suite)
			positive := mode == "valid" || mode == "two targets"
			if positive && (err != nil || !reflect.DeepEqual(got, want)) || !positive && (err == nil || got != nil) {
				t.Fatal("proposal renderer crossed validation/claim boundary", got, err)
			}
		})
	}
	// A plan's successful byte calculation does not supply a future claim to
	// native rendering; its ordinary claimed contract still rejects zero binding.
	expected.Target = SessionBinding{}
	if files, err := RenderPreparationHostFiles(expected); err == nil || files != nil {
		t.Fatal("proposal demand authorized native files")
	}
}
