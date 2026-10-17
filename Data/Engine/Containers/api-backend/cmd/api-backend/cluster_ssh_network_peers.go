package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"encoding/json"
	"net/netip"
	"reflect"
	"slices"
	"time"
)

// These are freshly observed host identities, separate from preparation
// settings and historical inspection reports. Equal namespace/index values on
// different hosts are expected and are never treated as global identities.
func validateClusterSSHSourceNetworks(authority clusterSSHPreparationAuthority, networks []clusterbootstrap.SourceNetwork) error {
	if validateClusterSSHInspectionCohort(authority.Cohort, authority.Source) != nil || len(networks) != len(authority.Source.Members) {
		return clusterbootstrap.ErrPreparationConfig
	}
	macs := map[string]bool{}
	for i, network := range networks {
		member := authority.Source.Members[i]
		prefix, err := netip.ParsePrefix(network.ManagementLink.Address)
		if network.Validate() != nil || network.NodeUID != member.NodeUID || network.Hostname != member.Name ||
			network.MachineID != member.MachineID || network.BootID != member.BootID || !network.ManagementLink.MatchesAddress(member.Address) ||
			network.K3sVersion != authority.K3sVersion || err != nil || prefix.Masked().String() != authority.Cohort.Targets[0].Report.ConnectedPrefix ||
			macs[network.ManagementLink.MAC] {
			return clusterbootstrap.ErrPreparationConfig
		}
		if i > 0 && (network.PodCIDR != networks[0].PodCIDR || network.ServiceCIDR != networks[0].ServiceCIDR) {
			return clusterbootstrap.ErrPreparationConfig
		}
		macs[network.ManagementLink.MAC] = true
	}
	return nil
}

type clusterSSHManagementPeer struct {
	ID, NodeUID, Hostname, MachineID, BootID, SSHFingerprint string
	Link                                                     clusterbootstrap.ManagementLink
}

// Only the pinned SSH client can construct this opaque result. The collector
// independently calls its time/host/key-bound accessor. Each target needs its own held credential
// and live lease checks; the anchor preparation claim grants no sibling access.
type clusterSSHTargetNetworkRead func(context.Context, clusterSSHInspectedTarget, []string) (clusterremote.TargetManagementPeer, error)

func readClusterSSHTargetNetwork(ctx context.Context, client *clusterremote.Client, sudoPassword []byte,
	expected clusterSSHInspectedTarget, peers []string, check func(context.Context) error) (clusterremote.TargetManagementPeer, error) {
	observed, err := client.InspectManagementPeer(ctx, sudoPassword, clusterremote.RouteTargets{Management: expected.Binding.Address, Peers: peers},
		clusterremote.Target{Address: expected.Binding.Address, Port: expected.Binding.Port}, expected.Key, check)
	if err != nil || ctx.Err() != nil {
		return clusterremote.TargetManagementPeer{}, clusterbootstrap.ErrPreparationConfig
	}
	return observed, nil
}

// This synchronous scope is a peer-input boundary, never qualification success
// or a queue transition. refresh must check/renew every participating target's
// held lease and credential, under the same original controller and attempt.
// It cannot open sibling credentials under the anchor lease. Production claims
// remain disabled until whole-cohort qualification/admission wiring is complete.
func withClusterSSHNetworkPeers(parent context.Context, authority clusterSSHPreparationAuthorityRead,
	source clusterSSHPreparationSnapshotRead, target clusterSSHTargetNetworkRead, refresh func(context.Context) error,
	consume func(context.Context, []clusterSSHManagementPeer, clusterSSHPreparationChecks) error) error {
	if consume == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return withClusterSSHNetworkInputs(parent, authority, source, target, refresh, func(ctx context.Context, peers []clusterSSHManagementPeer, _ []clusterbootstrap.SourceNetwork, checks clusterSSHPreparationChecks) error {
		return consume(ctx, peers, checks)
	})
}

func withClusterSSHNetworkInputs(parent context.Context, authority clusterSSHPreparationAuthorityRead,
	source clusterSSHPreparationSnapshotRead, target clusterSSHTargetNetworkRead, refresh func(context.Context) error,
	consume func(context.Context, []clusterSSHManagementPeer, []clusterbootstrap.SourceNetwork, clusterSSHPreparationChecks) error) error {
	if authority == nil || source == nil || target == nil || refresh == nil || consume == nil || parent.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	check := newClusterSSHPreparationLeaseCheck(authority, refresh)
	return runClusterSSHPreparationScope(ctx, 5*time.Second, check, func(ctx context.Context) error {
		// Freeze an owned copy, including keys and ordered cohort, before reads.
		value, err := authority(ctx)
		if err != nil || check(ctx) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		raw, err := json.Marshal(value)
		var baseline clusterSSHPreparationAuthority
		if err != nil || json.Unmarshal(raw, &baseline) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		var retained []clusterSSHManagementPeer
		var retainedSources []clusterbootstrap.SourceNetwork
		var observation string
		gate := make(chan struct{}, 1)
		read := func(ctx context.Context) error {
			select {
			case gate <- struct{}{}:
				defer func() { <-gate }()
			case <-ctx.Done():
				return clusterbootstrap.ErrPreparationConfig
			}
			if check(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			current, err := authority(ctx)
			if err != nil || validateClusterSSHInspectionCohort(current.Cohort, current.Source) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			before, after := baseline, current
			before.Cohort.ObservedAt, after.Cohort.ObservedAt = 0, 0
			if !reflect.DeepEqual(before, after) {
				return clusterbootstrap.ErrPreparationConfig
			}
			started := time.Now()
			snapshot, err := source(ctx)
			if err != nil || snapshot.started.Before(started) || snapshot.started.After(time.Now()) || check(ctx) != nil ||
				validateClusterSSHSourceNetworks(baseline, snapshot.Sources) != nil || !clusterSSHSourceObservationRE.MatchString(snapshot.Observation) ||
				(observation != "" && (observation != snapshot.Observation || !slices.Equal(retainedSources, snapshot.Sources))) {
				return clusterbootstrap.ErrPreparationConfig
			}
			expected, err := buildClusterSSHPreparationExpected(baseline.Cohort, baseline.Source, baseline.Lease, baseline.Baseline,
				baseline.K3sVersion, snapshot.Sources[0].PodCIDR, snapshot.Sources[0].ServiceCIDR)
			if err != nil || !reflect.DeepEqual(expected, snapshot.Expected) {
				return clusterbootstrap.ErrPreparationConfig
			}
			peers := make([]clusterSSHManagementPeer, 0, 3)
			addresses := []string{baseline.Source.ControlPlaneVIP, baseline.Source.EdgeVIP}
			for i, network := range snapshot.Sources {
				member := baseline.Source.Members[i]
				peers = append(peers, clusterSSHManagementPeer{ID: member.NodeID, NodeUID: network.NodeUID, Hostname: network.Hostname,
					MachineID: network.MachineID, BootID: network.BootID, SSHFingerprint: member.SSHFingerprint, Link: network.ManagementLink})
				addresses = append(addresses, member.Address)
			}
			for _, item := range baseline.Cohort.Targets {
				addresses = append(addresses, item.Binding.Address)
			}
			slices.Sort(addresses)
			addresses = slices.Compact(addresses)
			for _, item := range baseline.Cohort.Targets {
				if check(ctx) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				requested := slices.DeleteFunc(slices.Clone(addresses), func(address string) bool { return address == item.Binding.Address })
				started := time.Now()
				observed, err := target(ctx, item, slices.Clone(requested))
				if err != nil || check(ctx) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				link, err := observed.ManagementLink(started, item.Report.Hostname, item.Report.MachineID, item.Report.BootID,
					clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, requested)
				prefix, parseErr := netip.ParsePrefix(link.Address)
				if err != nil || check(ctx) != nil || !link.MatchesAddress(item.Binding.Address) || parseErr != nil || prefix.Masked().String() != item.Report.ConnectedPrefix {
					return clusterbootstrap.ErrPreparationConfig
				}
				peers = append(peers, clusterSSHManagementPeer{ID: item.Binding.TargetID, Hostname: item.Report.Hostname,
					MachineID: item.Report.MachineID, BootID: item.Report.BootID, SSHFingerprint: item.Key.Fingerprint, Link: link})
			}
			macs := map[string]bool{}
			for _, peer := range peers {
				if macs[peer.Link.MAC] {
					return clusterbootstrap.ErrPreparationConfig
				}
				macs[peer.Link.MAC] = true
			}
			if len(peers) != 3 || (retained != nil && !slices.Equal(retained, peers)) || check(ctx) != nil || ctx.Err() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			retained, observation = peers, snapshot.Observation
			retainedSources = slices.Clone(snapshot.Sources)
			return nil
		}
		// Independent complete rounds catch changes while sibling hosts were
		// observed. No callback result from an earlier round may be substituted.
		if read(ctx) != nil || read(ctx) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		checks := clusterSSHPreparationChecks{
			Inputs:    clusterSSHPreparationBoundary(ctx, check, read),
			Authority: clusterSSHPreparationBoundary(ctx, check, func(context.Context) error { return nil }),
		}
		if consume(ctx, slices.Clone(retained), slices.Clone(retainedSources), checks) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return checks.Inputs(ctx)
	})
}

func observeClusterSSHSourceNetworks(ctx context.Context, authority clusterSSHPreparationAuthority, read clusterSSHSourceNetworkRead) ([]clusterbootstrap.SourceNetwork, error) {
	if read == nil || ctx.Err() != nil || validateClusterSSHInspectionCohort(authority.Cohort, authority.Source) != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	networks := make([]clusterbootstrap.SourceNetwork, 0, len(authority.Source.Members))
	for _, member := range authority.Source.Members {
		if ctx.Err() != nil {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		network, err := read(ctx, member)
		if err != nil || ctx.Err() != nil {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		networks = append(networks, network)
	}
	if validateClusterSSHSourceNetworks(authority, networks) != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return networks, nil
}
