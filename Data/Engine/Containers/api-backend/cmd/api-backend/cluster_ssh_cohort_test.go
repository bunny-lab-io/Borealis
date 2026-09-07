package main

import (
	"borealis/api-backend/internal/clusterremote"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"golang.org/x/crypto/ssh"
	"strings"
	"testing"
)

func sshInspectionCohortFixture(t *testing.T) (clusterSSHInspectionCohort, clusterSSHSourceCohort) {
	t.Helper()
	cohort := clusterSSHInspectionCohort{ClusterID: newClusterUUID(), OperationID: newClusterUUID(), ControllerHolder: newClusterUUID(), Attempt: 1, ObservedAt: 1000}
	source := clusterSSHSourceCohort{ClusterID: cohort.ClusterID, KubeSystemUID: newClusterUUID(), ActiveSize: 1, DesiredSize: 3, Status: "Healthy", HMRState: "inactive", ControlPlaneVIP: "192.168.90.10", EdgeVIP: "192.168.90.10",
		Members: []clusterSSHSourceMember{{NodeID: newClusterUUID(), NodeUID: newClusterUUID(), Name: "engine-01", Address: "192.168.90.20", MachineID: strings.Repeat("b", 32), BootID: newClusterUUID()}}}
	for n := 0; n < 2; n++ {
		public, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		key, err := ssh.NewPublicKey(public)
		if err != nil {
			t.Fatal(err)
		}
		wire := clusterremote.HostKey{Algorithm: key.Type(), Fingerprint: ssh.FingerprintSHA256(key), PublicKey: key.Marshal()}
		host, privileged := sshWorkerFacts()
		host.Hostname = fmt.Sprintf("engine-%02d", n+2)
		privileged.MachineID = strings.Repeat(fmt.Sprintf("%x", n+12), 32)
		privileged.BootID = newClusterUUID()
		address := fmt.Sprintf("192.168.90.%d", n+21)
		report, err := clusterSSHReport(host, privileged, address)
		if err != nil {
			t.Fatal(err)
		}
		report.ConnectedPrefix = "192.168.90.0/24"
		cohort.Targets = append(cohort.Targets, clusterSSHInspectedTarget{Binding: clusterSSHCredentialBinding{ClusterID: cohort.ClusterID, OperationID: cohort.OperationID, TargetID: newClusterUUID(), Address: address, Port: 22, Fingerprint: wire.Fingerprint}, Key: wire, Ordinal: int64(n + 1), InspectedAt: 990, Attempt: 1, Generation: 1, Report: report})
	}
	return cohort, source
}

func TestClusterSSHInspectionCohortExpansionAndReplacement(t *testing.T) {
	for _, mode := range []string{"expansion desired three", "expansion desired one", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			cohort, source := sshInspectionCohortFixture(t)
			if mode == "expansion desired one" {
				source.DesiredSize = 1
			}
			if mode == "replacement" {
				source.ActiveSize, source.Status = 2, "Degraded Quorum"
				member := cohort.Targets[1]
				source.Members = append(source.Members, clusterSSHSourceMember{NodeID: newClusterUUID(), NodeUID: newClusterUUID(), Name: member.Report.Hostname, Address: member.Binding.Address, MachineID: member.Report.MachineID, BootID: member.Report.BootID, SSHFingerprint: member.Binding.Fingerprint})
				cohort.Targets = cohort.Targets[:1]
			}
			// Targets can have more physical capacity than the source. This check does
			// not choose their profile or mistake /opt free space for storage proof.
			if err := validateClusterSSHInspectionCohort(cohort, source); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClusterSSHInspectionCohortRejectsUnsafeObservations(t *testing.T) {
	cases := map[string]func(*clusterSSHInspectionCohort, *clusterSSHSourceCohort){
		"partial expansion":       func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { c.Targets = c.Targets[:1] },
		"unrecorded replacement":  func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { s.ActiveSize = 2 },
		"HMR":                     func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { s.HMRState = "isolated" },
		"wrong cluster":           func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { s.ClusterID = newClusterUUID() },
		"unknown source identity": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { s.Members[0].MachineID = "unknown" },
		"duplicate target ID": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[1].Binding.TargetID = c.Targets[0].Binding.TargetID
		},
		"duplicate machine ID": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[1].Report.MachineID = c.Targets[0].Report.MachineID
		},
		"source machine clone": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[1].Report.MachineID = s.Members[0].MachineID
		},
		"duplicate boot ID": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[1].Report.BootID = c.Targets[0].Report.BootID
		},
		"duplicate host key": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[1].Key = c.Targets[0].Key
			c.Targets[1].Binding.Fingerprint = c.Targets[0].Binding.Fingerprint
		},
		"source SSH clone": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			s.Members[0].SSHFingerprint = c.Targets[0].Binding.Fingerprint
		},
		"changed wire key": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { c.Targets[1].Key = c.Targets[0].Key },
		"duplicate address": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[1].Binding.Address = c.Targets[0].Binding.Address
		},
		"source address": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[1].Binding.Address = s.Members[0].Address
		},
		"duplicate name": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[1].Report.Hostname = c.Targets[0].Report.Hostname
		},
		"VIP collision": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			s.ControlPlaneVIP = c.Targets[0].Binding.Address
		},
		"VIP network":   func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { s.EdgeVIP = "192.168.90.0" },
		"VIP broadcast": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { s.EdgeVIP = "192.168.90.255" },
		"source broadcast": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			s.Members[0].Address = "192.168.90.255"
		},
		"target network": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[0].Binding.Address = "192.168.90.0"
		},
		"target broadcast": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[0].Binding.Address = "192.168.90.255"
		},
		"prefix mismatch": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[0].Report.ConnectedPrefix = "192.168.90.0/25"
		},
		"missing route": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[0].Report.ConnectedPrefix = ""
		},
		"outside subnet": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { s.Members[0].Address = "192.168.91.20" },
		"prior attempt":  func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { c.Attempt++ },
		"no generation":  func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { c.Targets[0].Generation = 0 },
		"expired report": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[0].InspectedAt = c.ObservedAt - clusterSSHInspectionLifetimeSeconds
		},
		"future report": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[0].InspectedAt = c.ObservedAt + 1
		},
		"wrong ordinal": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) { c.Targets[0].Ordinal = 2 },
		"unsupported host": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[0].Report.OSID = "debian"
			c.Targets[0].Report.SupportedPlatform = false
		},
		"existing installation": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[0].Report.NoExistingInstallation = false
			c.Targets[0].Report.BorealisPath = "directory"
		},
		"unknown machine": func(c *clusterSSHInspectionCohort, s *clusterSSHSourceCohort) {
			c.Targets[0].Report.NoExistingInstallation = false
			c.Targets[0].Report.MachineID = "unknown"
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c, s := sshInspectionCohortFixture(t)
			change(&c, &s)
			if validateClusterSSHInspectionCohort(c, s) == nil {
				t.Fatal("unsafe cohort accepted")
			}
		})
	}
}
