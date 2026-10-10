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
type clusterSSHTargetNetworkBootRead func(context.Context, clusterSSHInspectedTarget, clusterremote.NetworkRenderRequest, func(context.Context) error) (clusterremote.TargetNetworkBoot, error)

// observeNetworkBoot consumes bounded persistent startup wiring and native
// render correspondence under complete current cohort/VIP authority. Future
// naming, reboot survival, aggregate qualification and production claims remain gated.
func (c *clusterSSHVIPBrokerClient) observeNetworkBoot(parent context.Context, authority clusterSSHPreparationAuthorityRead,
	source clusterSSHPreparationSnapshotRead, target clusterSSHTargetNetworkRead, boot clusterSSHTargetNetworkBootRead, refresh func(context.Context) error,
	lease clusterSSHTargetLease, baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials) error {
	if boot == nil {
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
				value, err := boot(ctx, input, request, checks.Authority)
				if err != nil || checks.Authority(ctx) != nil || value.Matches(started, clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, requests[i]) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
			}
			return nil
		})
}
