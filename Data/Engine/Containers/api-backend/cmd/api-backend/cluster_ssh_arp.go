package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// The credential-owning caller constructs this reader independently for every
// target. No sibling credential can be loaded using the anchor claim.
type clusterSSHTargetARPRead func(context.Context, clusterSSHInspectedTarget, clusterremote.ARPRequest, func(context.Context) error) (clusterremote.TargetARPObservation, error)

func clusterSSHARPRequests(a clusterSSHPreparationAuthority, peers []clusterSSHManagementPeer, owner clusterSSHVIPOwner) ([]clusterremote.ARPRequest, error) {
	fail := func() ([]clusterremote.ARPRequest, error) { return nil, clusterbootstrap.ErrPreparationConfig }
	if validateClusterSSHInspectionCohort(a.Cohort, a.Source) != nil || len(peers) != 3 ||
		owner.Address != a.Source.ControlPlaneVIP || owner.Address != a.Source.EdgeVIP {
		return fail()
	}
	epoch := clusterbootstrap.VIPLease{UID: owner.LeaseUID, ResourceVersion: "projection", Holder: owner.Owner.Hostname, AcquireTime: owner.AcquireTime, RenewTime: owner.AcquireTime, Transitions: owner.Transitions, DurationSeconds: 10}
	if epoch.Validate() != nil || !slices.Contains(peers[:len(a.Source.Members)], owner.Owner) {
		return fail()
	}
	macs, addresses := map[string]bool{}, map[string]bool{}
	for i, peer := range peers {
		prefix, err := netip.ParsePrefix(peer.Link.Address)
		if err != nil || peer.Link.Validate() != nil || prefix.Masked().String() != a.Cohort.Targets[0].Report.ConnectedPrefix || macs[peer.Link.MAC] || addresses[prefix.Addr().String()] {
			return fail()
		}
		macs[peer.Link.MAC], addresses[prefix.Addr().String()] = true, true
		var expected clusterSSHManagementPeer
		var address string
		if i < len(a.Source.Members) {
			m := a.Source.Members[i]
			expected = clusterSSHManagementPeer{ID: m.NodeID, NodeUID: m.NodeUID, Hostname: m.Name, MachineID: m.MachineID, BootID: m.BootID, SSHFingerprint: m.SSHFingerprint, Link: peer.Link}
			address = m.Address
		} else {
			m := a.Cohort.Targets[i-len(a.Source.Members)]
			expected = clusterSSHManagementPeer{ID: m.Binding.TargetID, Hostname: m.Report.Hostname, MachineID: m.Report.MachineID, BootID: m.Report.BootID, SSHFingerprint: m.Key.Fingerprint, Link: peer.Link}
			address = m.Binding.Address
		}
		if peer != expected || !peer.Link.MatchesAddress(address) {
			return fail()
		}
	}
	requests := make([]clusterremote.ARPRequest, 0, len(a.Cohort.Targets))
	for i, target := range a.Cohort.Targets {
		r := clusterremote.ARPRequest{Version: 1, MachineID: target.Report.MachineID, BootID: target.Report.BootID, Link: peers[len(a.Source.Members)+i].Link}
		for _, peer := range peers {
			if peer.ID != target.Binding.TargetID {
				prefix, _ := netip.ParsePrefix(peer.Link.Address)
				r.Peers = append(r.Peers, clusterremote.ARPPeer{Address: prefix.Addr().String(), MAC: peer.Link.MAC})
			}
		}
		r.Peers = append(r.Peers, clusterremote.ARPPeer{Address: owner.Address, MAC: owner.Owner.Link.MAC})
		slices.SortFunc(r.Peers, func(x, y clusterremote.ARPPeer) int { return strings.Compare(x.Address, y.Address) })
		if r.Validate() != nil {
			return fail()
		}
		requests = append(requests, r)
	}
	return requests, nil
}

// observeNetworkARP performs current target-to-cohort/VIP exchanges inside the
// live peer/VIP scope. Success is an observation only: no durable qualification,
// public confirmation or production preparation claim is enabled here.
func (c *clusterSSHVIPBrokerClient) observeNetworkARP(parent context.Context, authority clusterSSHPreparationAuthorityRead,
	source clusterSSHPreparationSnapshotRead, target clusterSSHTargetNetworkRead, probe clusterSSHTargetARPRead, refresh func(context.Context) error,
	lease clusterSSHTargetLease, baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials) error {
	if probe == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return c.withNetworkOwner(parent, authority, source, target, refresh, lease, baseline, sealed,
		func(ctx context.Context, peers []clusterSSHManagementPeer, owner clusterSSHVIPOwner, checks clusterSSHPreparationChecks) error {
			a, err := authority(ctx)
			if err != nil || checks.Authority(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			// Retain owned target/key copies across independently supplied readers.
			raw, err := json.Marshal(a)
			var frozen clusterSSHPreparationAuthority
			if err != nil || json.Unmarshal(raw, &frozen) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			requests, err := clusterSSHARPRequests(frozen, peers, owner)
			if err != nil {
				return err
			}
			for i, item := range frozen.Cohort.Targets {
				if checks.Authority(ctx) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				request := requests[i]
				request.Peers = slices.Clone(request.Peers)
				input := item
				input.Key.PublicKey = bytes.Clone(item.Key.PublicKey)
				input.Report.Nodes = slices.Clone(item.Report.Nodes)
				started := time.Now()
				value, err := probe(ctx, input, request, checks.Authority)
				if err != nil || checks.Authority(ctx) != nil || value.Matches(started, clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, requests[i]) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
			}
			return nil // withNetworkOwner still rechecks complete peers and VIP.
		})
}
