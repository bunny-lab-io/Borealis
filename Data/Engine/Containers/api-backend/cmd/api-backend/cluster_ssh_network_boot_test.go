package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClusterSSHNetworkBootLivePeerVIPComposition(t *testing.T) {
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
			var cached clusterremote.TargetNetworkBoot
			probes := 0
			probe := func(ctx context.Context, item clusterSSHInspectedTarget, request clusterremote.NetworkRenderRequest, check func(context.Context) error) (clusterremote.TargetNetworkBoot, error) {
				probes++
				if mode == "cached" && probes > 1 {
					return cached, nil
				}
				if mode == "zero" {
					return clusterremote.TargetNetworkBoot{}, nil
				}
				if mode == "probe error" {
					return clusterremote.TargetNetworkBoot{}, errors.New("private SSH")
				}
				if mode == "key alias" {
					item.Key.PublicKey[0] ^= 1
				}
				if mode == "changed input" {
					request.Targets.Peers[0] = "192.168.90.249"
				}
				resultLink := request.Link
				if mode == "wrong result" {
					resultLink.Index++
				}
				var value clusterremote.TargetNetworkBoot
				err := sshNetworkFixtureObserve(t, ctx, item, keys[item.Binding.TargetID], resultLink, request.Targets.Peers, nil, func(ssh *clusterremote.Client) error {
					var err error
					value, err = ssh.InspectNetworkBoot(ctx, []byte("fixture-sudo"), clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, request, check)
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
			err := client.observeNetworkBoot(ctx, authority, source, target, probe, refresh, a.Lease, a.Baseline, r.sealed())
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
