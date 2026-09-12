package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"
)

func sshSourceNetworkFixture(member clusterSSHSourceMember, version string) clusterbootstrap.SourceNetwork {
	ip := netip.MustParseAddr(member.Address).As4()
	return clusterbootstrap.SourceNetwork{NodeUID: member.NodeUID, Hostname: member.Name, MachineID: member.MachineID, BootID: member.BootID,
		K3sVersion: version, PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16", ManagementLink: clusterbootstrap.ManagementLink{
			Interface: "ens18", Index: 2, Address: member.Address + "/24", MAC: fmt.Sprintf("02:00:%02x:%02x:%02x:%02x", ip[0], ip[1], ip[2], ip[3]), NetworkNamespace: 1234}}
}

func TestClusterSSHNetworkPeersCompleteFreshScope(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "same VIP", "source cached", "source missing", "source wrong binding", "source drift", "source ranges drift", "target cached", "target duplicate MAC", "source target duplicate MAC", "target drift", "target changed during consume", "target wrong subnet", "target missing", "source Secret drift", "authority drift", "authority lost", "cancel source", "cancel target", "cancel consume", "consume error", "consume alias", "closed scope"} {
		t.Run(mode, func(t *testing.T) {
			_, baseline, _ := sshBrokerFixture(t)
			if mode == "replacement" {
				item := baseline.Cohort.Targets[1]
				baseline.Source.Members = append(baseline.Source.Members, clusterSSHSourceMember{NodeID: item.Binding.TargetID, NodeUID: newClusterUUID(),
					Name: item.Report.Hostname, Address: item.Binding.Address, MachineID: item.Report.MachineID, BootID: item.Report.BootID, SSHFingerprint: item.Key.Fingerprint})
				baseline.Cohort.Targets = baseline.Cohort.Targets[:1]
				baseline.Source.ActiveSize, baseline.Source.DesiredSize, baseline.Source.Status = 2, 3, "Degraded Quorum"
			}
			if mode == "same VIP" {
				baseline.Source.EdgeVIP = baseline.Source.ControlPlaneVIP
			}
			keys := sshNetworkFixtureKeys(t, &baseline.Cohort)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rounds, targets, consumed, renewals := 0, 0, 0, 0
			lost := false
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
				// Reuse nested allocations to prove the scope owns its frozen copy.
				if mode == "authority drift" && rounds > 0 {
					baseline.Cohort.Targets[1].Report.BootID = newClusterUUID()
				}
				return baseline, nil
			}
			refresh := func(context.Context) error {
				renewals++
				if lost {
					return errors.New("private-credential")
				}
				return nil
			}
			source := func(ctx context.Context) (clusterSSHPreparationSnapshot, error) {
				rounds++
				if mode == "cancel source" {
					cancel()
				}
				expected, err := buildClusterSSHPreparationExpected(baseline.Cohort, baseline.Source, baseline.Lease, baseline.Baseline, baseline.K3sVersion, "10.42.0.0/16", "10.43.0.0/16")
				if err != nil {
					return clusterSSHPreparationSnapshot{}, err
				}
				value := clusterSSHPreparationSnapshot{Expected: expected, Observation: fmt.Sprintf("%064x", 1), started: time.Now()}
				for _, member := range baseline.Source.Members {
					value.Sources = append(value.Sources, sshSourceNetworkFixture(member, baseline.K3sVersion))
				}
				switch mode {
				case "source cached":
					value.started = time.Now().Add(-time.Second)
				case "source missing":
					value.Sources = nil
				case "source wrong binding":
					value.Expected.Target.HolderID = newClusterUUID()
				case "source drift":
					if rounds > 1 {
						value.Sources[0].ManagementLink.Index++
					}
				case "source ranges drift":
					if rounds > 1 {
						value.Sources[0].PodCIDR = "10.44.0.0/16"
						value.Expected.PodCIDR = "10.44.0.0/16"
					}
				case "source Secret drift":
					if rounds > 1 {
						value.Observation = fmt.Sprintf("%064x", 2)
					}
				}
				return value, nil
			}
			cached := map[string]clusterremote.TargetManagementPeer{}
			target := func(ctx context.Context, item clusterSSHInspectedTarget, peers []string) (clusterremote.TargetManagementPeer, error) {
				targets++
				wanted := []string{baseline.Source.ControlPlaneVIP, baseline.Source.EdgeVIP}
				for _, member := range baseline.Source.Members {
					wanted = append(wanted, member.Address)
				}
				for _, other := range baseline.Cohort.Targets {
					if other.Binding.TargetID != item.Binding.TargetID {
						wanted = append(wanted, other.Binding.Address)
					}
				}
				slices.Sort(wanted)
				wanted = slices.Compact(wanted)
				if !slices.Equal(wanted, peers) {
					t.Fatal("incomplete target peer/VIP request")
				}
				member := clusterSSHSourceMember{Address: item.Binding.Address}
				link := sshSourceNetworkFixture(member, baseline.K3sVersion).ManagementLink
				switch mode {
				case "target duplicate MAC":
					link.MAC = "02:00:00:00:00:ff"
				case "source target duplicate MAC":
					link.MAC = sshSourceNetworkFixture(baseline.Source.Members[0], baseline.K3sVersion).ManagementLink.MAC
				case "target drift":
					if rounds > 1 {
						link.NetworkNamespace++
					}
				case "target changed during consume":
					if rounds > 2 {
						link.NetworkNamespace++
					}
				case "target wrong subnet":
					link.Address = item.Binding.Address + "/25"
				case "target missing":
					return clusterremote.TargetManagementPeer{}, nil
				case "authority lost":
					lost = true
				case "cancel target":
					cancel()
				}
				if mode == "target cached" && rounds > 1 {
					return cached[item.Binding.TargetID], nil
				}
				value, err := sshNetworkFixturePeer(t, ctx, item, keys[item.Binding.TargetID], link, peers)
				cached[item.Binding.TargetID] = value
				return value, err
			}
			var retainedChecks clusterSSHPreparationChecks
			consume := func(ctx context.Context, peers []clusterSSHManagementPeer, checks clusterSSHPreparationChecks) error {
				consumed++
				if rounds != 2 || targets != 2*len(baseline.Cohort.Targets) || len(peers) != 3 {
					t.Fatal("incomplete acquisition reached consumer")
				}
				if checks.Authority(ctx) != nil {
					t.Fatal("current authority rejected")
				}
				retainedChecks = checks
				if mode == "cancel consume" {
					cancel()
				}
				if mode == "consume error" {
					return errors.New("private-consumer")
				}
				if mode == "consume alias" {
					peers[0].Link.MAC = "02:00:00:00:00:ff"
				}
				return nil
			}
			err := withClusterSSHNetworkPeers(ctx, authority, source, target, refresh, consume)
			wantOK := slices.Contains([]string{"expansion", "replacement", "same VIP", "consume alias", "closed scope"}, mode)
			if (err == nil) != wantOK {
				t.Fatal("peer scope outcome", err)
			}
			if wantOK && (rounds != 3 || targets != 3*len(baseline.Cohort.Targets) || consumed != 1) {
				t.Fatal("post-consumption peer recheck missing")
			}
			if !wantOK && mode != "cancel consume" && mode != "consume error" && mode != "target changed during consume" && consumed != 0 {
				t.Fatal("failed acquisition reached consumer")
			}
			if mode == "closed scope" {
				before := renewals
				if retainedChecks.Inputs(context.Background()) == nil || retainedChecks.Authority(context.Background()) == nil || renewals != before {
					t.Fatal("peer authority survived scope")
				}
			}
		})
	}
}

func TestClusterSSHNetworkPeersSourceTimestampNotSerialized(t *testing.T) {
	_, _, value := sshBrokerFixture(t)
	value.started = time.Now()
	raw, err := json.Marshal(value)
	var decoded clusterSSHPreparationSnapshot
	if err != nil || json.Unmarshal(raw, &decoded) != nil || !decoded.started.IsZero() {
		t.Fatal("stored snapshot retained local freshness")
	}
}

func TestClusterSSHSourceNetworksCompleteDistinctPeers(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "duplicate MAC", "missing", "extra", "swapped", "wrong Node", "wrong machine", "wrong boot", "wrong address", "wrong subnet", "wrong version", "different ranges"} {
		t.Run(mode, func(t *testing.T) {
			cohort, source, lease, baseline := sshPreparationFixture(t)
			if mode != "expansion" {
				target := cohort.Targets[1]
				source.Members = append(source.Members, clusterSSHSourceMember{NodeID: target.Binding.TargetID, NodeUID: newClusterUUID(),
					Name: target.Report.Hostname, Address: target.Binding.Address, MachineID: target.Report.MachineID, BootID: target.Report.BootID, SSHFingerprint: target.Key.Fingerprint})
				cohort.Targets = cohort.Targets[:1]
				source.ActiveSize, source.DesiredSize, source.Status = 2, 3, "Degraded Quorum"
			}
			authority := clusterSSHPreparationAuthority{Cohort: cohort, Source: source, Lease: lease, Baseline: baseline, K3sVersion: "v1.36.3+k3s1"}
			var networks []clusterbootstrap.SourceNetwork
			for _, member := range source.Members {
				networks = append(networks, sshSourceNetworkFixture(member, authority.K3sVersion))
			}
			switch mode {
			case "duplicate MAC":
				networks[1].ManagementLink.MAC = networks[0].ManagementLink.MAC
			case "missing":
				networks = networks[:1]
			case "extra":
				networks = append(networks, networks[0])
			case "swapped":
				networks[0], networks[1] = networks[1], networks[0]
			case "wrong Node":
				networks[0].NodeUID = newClusterUUID()
			case "wrong machine":
				networks[0].MachineID = networks[1].MachineID
			case "wrong boot":
				networks[0].BootID = newClusterUUID()
			case "wrong address":
				networks[0].ManagementLink.Address = "192.168.90.99/24"
			case "wrong subnet":
				networks[0].ManagementLink.Address = source.Members[0].Address + "/25"
			case "wrong version":
				networks[0].K3sVersion = "v1.36.4+k3s1"
			case "different ranges":
				networks[1].PodCIDR = "10.44.0.0/16"
			}
			if err := validateClusterSSHSourceNetworks(authority, networks); (err == nil) != (mode == "expansion" || mode == "replacement") {
				t.Fatal("source peer binding", err)
			}
		})
	}
}
