package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sshARPFixturePeers(a clusterSSHPreparationAuthority) []clusterSSHManagementPeer {
	var peers []clusterSSHManagementPeer
	for _, m := range a.Source.Members {
		peers = append(peers, clusterSSHManagementPeer{ID: m.NodeID, NodeUID: m.NodeUID, Hostname: m.Name, MachineID: m.MachineID, BootID: m.BootID, SSHFingerprint: m.SSHFingerprint, Link: sshSourceNetworkFixture(m, a.K3sVersion).ManagementLink})
	}
	for _, m := range a.Cohort.Targets {
		peers = append(peers, clusterSSHManagementPeer{ID: m.Binding.TargetID, Hostname: m.Report.Hostname, MachineID: m.Report.MachineID, BootID: m.Report.BootID, SSHFingerprint: m.Key.Fingerprint, Link: sshSourceNetworkFixture(clusterSSHSourceMember{Address: m.Binding.Address}, a.K3sVersion).ManagementLink})
	}
	return peers
}

func TestClusterSSHARPCompleteIndependentRequests(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "missing", "order", "source identity", "target identity", "duplicate MAC", "duplicate address", "wrong prefix", "foreign owner", "target owner", "VIP MAC", "distinct VIPs", "VIP epoch", "VIP address", "source address", "target fingerprint"} {
		t.Run(mode, func(t *testing.T) {
			a, _, lease := sshVIPFixture(t, mode != "expansion")
			peers := sshARPFixturePeers(a)
			owner := clusterSSHVIPOwner{Address: a.Source.ControlPlaneVIP, Owner: peers[len(a.Source.Members)-1], LeaseUID: lease.UID, AcquireTime: lease.AcquireTime, Transitions: lease.Transitions}
			switch mode {
			case "missing":
				peers = peers[:2]
			case "order":
				peers[0], peers[2] = peers[2], peers[0]
			case "source identity":
				peers[0].NodeUID = newClusterUUID()
			case "target identity":
				peers[2].BootID = newClusterUUID()
			case "duplicate MAC":
				peers[2].Link.MAC = peers[0].Link.MAC
			case "duplicate address":
				peers[2].Link.Address = peers[0].Link.Address
			case "wrong prefix":
				peers[2].Link.Address = strings.Replace(peers[2].Link.Address, "/24", "/25", 1)
			case "foreign owner":
				owner.Owner.NodeUID = newClusterUUID()
			case "target owner":
				owner.Owner = peers[2]
			case "VIP MAC":
				owner.Owner.Link.MAC = "02:00:00:00:00:09"
			case "distinct VIPs":
				a.Source.EdgeVIP = "192.168.90.249"
			case "VIP epoch":
				owner.LeaseUID = ""
			case "VIP address":
				owner.Address = "192.168.90.248"
			case "source address":
				peers[0].Link.Address = "192.168.90.247/24"
			case "target fingerprint":
				peers[2].SSHFingerprint = peers[0].SSHFingerprint
			}
			requests, err := clusterSSHARPRequests(a, peers, owner)
			if mode != "expansion" && mode != "replacement" {
				if err == nil || requests != nil {
					t.Fatal("unbound request accepted")
				}
				return
			}
			if err != nil || len(requests) != len(a.Cohort.Targets) {
				t.Fatal("valid requests", err)
			}
			for i, r := range requests {
				if r.Validate() != nil || !r.Link.MatchesAddress(a.Cohort.Targets[i].Binding.Address) {
					t.Fatal("local link")
				}
				vip := false
				for _, p := range r.Peers {
					if p.Address == owner.Address {
						vip = p.MAC == owner.Owner.Link.MAC
					}
				}
				if !vip {
					t.Fatal("actual owner MAC absent")
				}
			}
		})
	}
}

func TestClusterSSHARPLivePeerVIPComposition(t *testing.T) {
	for _, mode := range []string{"expansion", "replacement", "VIP changed", "target changed", "credential lost", "cancel", "cached", "zero", "wrong result", "changed input", "key alias", "probe error"} {
		t.Run(mode, func(t *testing.T) {
			a, sources, lease := sshVIPFixture(t, mode == "replacement")
			keys := sshNetworkFixtureKeys(t, &a.Cohort)
			r, _, _ := sshBrokerFixture(t)
			r.Lease, r.Baseline, r.Binding = a.Lease, a.Baseline, a.Cohort.Targets[0].Binding
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var vipChanged, targetChanged, lost atomic.Bool
			var reads atomic.Int64
			broker := newClusterSSHVIPBroker(nil, nil, a.Lease.ControllerHolder, sshBrokerTestSecret)
			broker.run = sshVIPBrokerRun(a, lease, &vipChanged, &reads)
			server := sshVIPBrokerServer(t, broker)
			client := sshVIPBrokerClient(t, server.URL)
			authority := func(context.Context) (clusterSSHPreparationAuthority, error) { return a, nil }
			source := func(context.Context) (clusterSSHPreparationSnapshot, error) {
				expected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, sources[0].PodCIDR, sources[0].ServiceCIDR)
				return clusterSSHPreparationSnapshot{Expected: expected, Observation: strings.Repeat("a", 64), Sources: slices.Clone(sources), started: time.Now()}, err
			}
			target := func(ctx context.Context, item clusterSSHInspectedTarget, peers []string) (clusterremote.TargetManagementPeer, error) {
				link := sshSourceNetworkFixture(clusterSSHSourceMember{Address: item.Binding.Address}, a.K3sVersion).ManagementLink
				if targetChanged.Load() {
					link.NetworkNamespace++
				}
				return sshNetworkFixturePeer(t, ctx, item, keys[item.Binding.TargetID], link, peers)
			}
			refresh := func(context.Context) error {
				if lost.Load() {
					return errors.New("private credential")
				}
				return nil
			}
			var cached clusterremote.TargetARPObservation
			probes := 0
			probe := func(ctx context.Context, item clusterSSHInspectedTarget, request clusterremote.ARPRequest, check func(context.Context) error) (clusterremote.TargetARPObservation, error) {
				probes++
				if mode == "cached" && probes > 1 {
					return cached, nil
				}
				if mode == "zero" {
					return clusterremote.TargetARPObservation{}, nil
				}
				if mode == "probe error" {
					return clusterremote.TargetARPObservation{}, errors.New("private SSH")
				}
				if mode == "key alias" {
					item.Key.PublicKey[0] ^= 1
				}
				if mode == "changed input" {
					request.Peers[0].MAC = "02:00:00:00:00:09"
				}
				resultRequest := request
				if mode == "wrong result" {
					resultRequest.Link.Index++
				}
				wire, _ := json.Marshal(struct {
					Version int                      `json:"version"`
					Request clusterremote.ARPRequest `json:"request"`
					Rounds  int                      `json:"rounds"`
				}{1, resultRequest, 2})
				var value clusterremote.TargetARPObservation
				err := sshNetworkFixtureObserve(t, ctx, item, keys[item.Binding.TargetID], request.Link, nil, wire, func(ssh *clusterremote.Client) error {
					var err error
					value, err = ssh.ProbeARP(ctx, []byte("fixture-sudo"), clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, request, check)
					return err
				})
				if mode == "VIP changed" {
					vipChanged.Store(true)
				}
				if mode == "target changed" {
					targetChanged.Store(true)
				}
				if mode == "credential lost" {
					lost.Store(true)
				}
				if mode == "cancel" {
					cancel()
				}
				cached = value
				return value, err
			}
			err := client.observeNetworkARP(ctx, authority, source, target, probe, refresh, a.Lease, a.Baseline, r.sealed())
			if (err == nil) != (mode == "expansion" || mode == "replacement") {
				t.Fatal("unsafe composition", err)
			}
			if err == nil && probes != len(a.Cohort.Targets) {
				t.Fatal("incomplete cohort")
			}
			if mode == "key alias" && a.Cohort.Targets[0].Key.Validate() != nil {
				t.Fatal("caller key mutated")
			}
		})
	}
}
