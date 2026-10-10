package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"
)

// Each reader owns its target's original credential/claim independently.
type clusterSSHTargetNetworkRenderRead func(context.Context, clusterSSHInspectedTarget, clusterremote.NetworkRenderRequest, func(context.Context) error) (clusterremote.TargetNetworkRender, error)

func clusterSSHNetworkRenderRequests(a clusterSSHPreparationAuthority, peers []clusterSSHManagementPeer, owner clusterSSHVIPOwner) ([]clusterremote.NetworkRenderRequest, error) {
	// Reuse complete ordered cohort and independent owner validation. Render
	// observation sends no packets and gets no MAC expectations from ARP replies.
	bound, err := clusterSSHARPRequests(a, peers, owner)
	if err != nil {
		return nil, err
	}
	requests := make([]clusterremote.NetworkRenderRequest, 0, len(bound))
	for i, item := range bound {
		r := clusterremote.NetworkRenderRequest{MachineID: item.MachineID, BootID: item.BootID, Link: item.Link,
			Targets: clusterremote.RouteTargets{Management: a.Cohort.Targets[i].Binding.Address}}
		for _, peer := range item.Peers {
			r.Targets.Peers = append(r.Targets.Peers, peer.Address)
		}
		if r.Validate() != nil {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		requests = append(requests, r)
	}
	return requests, nil
}

// observeNetworkRender consumes fresh native render correspondence inside the
// live complete peer/VIP scope, with full post-observation rechecks. Boot/service
// activation, aggregate qualification and production claims remain separate.
func (c *clusterSSHVIPBrokerClient) observeNetworkRender(parent context.Context, authority clusterSSHPreparationAuthorityRead,
	source clusterSSHPreparationSnapshotRead, target clusterSSHTargetNetworkRead, render clusterSSHTargetNetworkRenderRead, refresh func(context.Context) error,
	lease clusterSSHTargetLease, baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials) error {
	if render == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return c.withNetworkOwner(parent, authority, source, target, refresh, lease, baseline, sealed,
		func(ctx context.Context, peers []clusterSSHManagementPeer, owner clusterSSHVIPOwner, checks clusterSSHPreparationChecks) error {
			a, err := authority(ctx)
			if err != nil || checks.Authority(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			raw, err := json.Marshal(a)
			var frozen clusterSSHPreparationAuthority
			if err != nil || json.Unmarshal(raw, &frozen) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			requests, err := clusterSSHNetworkRenderRequests(frozen, peers, owner)
			if err != nil {
				return err
			}
			for i, item := range frozen.Cohort.Targets {
				if checks.Authority(ctx) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
				request := requests[i]
				request.Targets.Peers = slices.Clone(request.Targets.Peers)
				input := item
				input.Key.PublicKey = bytes.Clone(item.Key.PublicKey)
				input.Report.Nodes = slices.Clone(item.Report.Nodes)
				started := time.Now()
				value, err := render(ctx, input, request, checks.Authority)
				if err != nil || checks.Authority(ctx) != nil || value.Matches(started, clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, requests[i]) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
			}
			return nil
		})
}
