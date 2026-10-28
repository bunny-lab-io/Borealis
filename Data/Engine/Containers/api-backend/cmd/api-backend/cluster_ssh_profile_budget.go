package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"slices"
)

// Fresh values prevent callers from mutating the qualified contract. Production
// renderer parity tests own evidence for these declared requests/caps.
func clusterSSHSmallBusinessApplications() []clusterSSHApplicationResource {
	return []clusterSSHApplicationResource{
		{"api-backend", 75, 2000, 192 << 20, 1024 << 20},
		{"borealis-cluster-controller", 50, 500, 64 << 20, 256 << 20},
		{"borealis-operator", 25, 250, 48 << 20, 160 << 20},
		{"job-scheduler", 50, 1500, 128 << 20, 512 << 20},
		{"remote-desktop-guacd", 25, 1000, 64 << 20, 512 << 20},
		{"traefik-edge", 25, 1500, 96 << 20, 512 << 20},
		{"webui-frontend", 50, 1000, 128 << 20, 512 << 20},
		{"wireguard-tunnel", 25, 1000, 48 << 20, 256 << 20},
	}
}

func clusterSSHSmallBusinessPostgres() clusterSSHApplicationResource {
	// Existing CNPG source projection names the container, not the standalone StatefulSet.
	return clusterSSHApplicationResource{"postgres", 100, 2000, 256 << 20, 6144 << 20}
}

// Add profile compatibility to every source read inside the existing native
// sizing scope. Keep all observed demand, receipts, source/release identity,
// controller authority and before/after rechecks; replace no mandatory check.
func withClusterSSHTargetProfileSizing(ctx context.Context, readers clusterSSHNetworkTargetReaders,
	source clusterSSHPreparationSnapshotRead, consume func(context.Context, []clusterSSHTargetHostEvidence) error) error {
	return withClusterSSHTargetApplicationSizing(ctx, readers, func(ctx context.Context) (clusterSSHPreparationSnapshot, error) {
		if source == nil {
			return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrPreparationConfig
		}
		value, err := source(ctx)
		if err != nil {
			return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrPreparationConfig
		}
		configuration, err := clusterbootstrap.NewPreparationConfiguration(value.Expected, value.Settings)
		if err != nil || configuration.SmallBusinessApplicationLimits() != nil ||
			!slices.Equal(value.Storage.Requirements.Applications, clusterSSHSmallBusinessApplications()) ||
			value.Storage.Requirements.PostgresResources != clusterSSHSmallBusinessPostgres() {
			return clusterSSHPreparationSnapshot{}, clusterbootstrap.ErrPreparationConfig
		}
		return value, nil
	}, consume)
}
