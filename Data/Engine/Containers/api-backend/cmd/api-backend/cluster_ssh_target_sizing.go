package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"reflect"
)

// Source is the existing fresh preparation/source broker reader, not caller
// settings or target-local defaults. Only complete original target scopes may
// consume this bounded per-host policy. No qualification/phase is persisted.
func withClusterSSHTargetSizing(parent context.Context, readers clusterSSHNetworkTargetReaders,
	source clusterSSHPreparationRead, consume func(context.Context, []clusterSSHTargetHostEvidence) error) error {
	if readers.within == nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return readers.within(parent, func(ctx context.Context) error {
		if source == nil || consume == nil || readers.Authority == nil || readers.Refresh == nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		a, err := readers.Authority(ctx)
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		expected, settings, err := source(ctx)
		if err != nil || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		bound, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, a.Lease, a.Baseline, a.K3sVersion, expected.PodCIDR, expected.ServiceCIDR)
		if err != nil || !reflect.DeepEqual(bound, expected) {
			return clusterbootstrap.ErrPreparationConfig
		}
		configuration, err := clusterbootstrap.NewPreparationConfiguration(expected, settings)
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		sizing, err := configuration.Sizing()
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		inputs := newClusterSSHPreparationInputCheck(configuration, source)
		current := func() error {
			if readers.Refresh(ctx) != nil || inputs(ctx) != nil || readers.Refresh(ctx) != nil || ctx.Err() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		}
		if current() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		if withClusterSSHTargetHostEvidence(ctx, readers, func(ctx context.Context, targets []clusterSSHTargetHostEvidence) error {
			if current() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			for _, target := range targets {
				if sizing.Fits(target.Host.CPUCount, target.Host.MemoryKiB) != nil {
					return clusterbootstrap.ErrPreparationConfig
				}
			}
			if consume(ctx, targets) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return current()
		}) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return current()
	})
}
