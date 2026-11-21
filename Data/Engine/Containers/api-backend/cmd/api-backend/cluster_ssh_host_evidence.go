package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"time"
)

// The profile consumer needs CPU and total RAM. Storage admission obtains its
// own fresh, path-specific capacity evidence.
type clusterSSHTargetHostEvidence struct {
	TargetID  string
	CPUCount  uint32
	MemoryKiB uint64
}

// Use only inside withClusterSSHNetworkTargets. Its original-claim heartbeat
// and joined readers remain active through this synchronous consumer. No
// database state or remote installation is changed by evidence collection.
func withClusterSSHTargetHostEvidence(parent context.Context, readers clusterSSHNetworkTargetReaders,
	consume func(context.Context, []clusterSSHTargetHostEvidence) error) error {
	if readers.within == nil || readers.Authority == nil || readers.Refresh == nil || readers.Peer == nil || consume == nil || parent.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return readers.within(parent, func(ctx context.Context) error {
		frozen, err := readers.Authority(ctx)
		if err != nil || validateClusterSSHInspectionCohort(frozen.Cohort, frozen.Source) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		raw, err := json.Marshal(frozen)
		var owned clusterSSHPreparationAuthority
		if err != nil || json.Unmarshal(raw, &owned) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		frozen = owned
		frozen.Cohort.ObservedAt = 0
		current := func() error {
			if readers.Refresh(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			a, err := readers.Authority(ctx)
			a.Cohort.ObservedAt = 0
			if err != nil || !reflect.DeepEqual(a, frozen) || ctx.Err() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		}
		read := func() ([]clusterremote.HostFacts, error) {
			values := make([]clusterremote.HostFacts, 0, len(frozen.Cohort.Targets))
			for _, item := range frozen.Cohort.Targets {
				if current() != nil {
					return nil, clusterbootstrap.ErrPreparationConfig
				}
				peers := clusterSSHNetworkTargetPeers(frozen, item.Binding.Address)
				started := time.Now()
				input := item
				input.Key.PublicKey = bytes.Clone(item.Key.PublicKey)
				input.Report.Nodes = slices.Clone(item.Report.Nodes)
				observation, err := readers.Peer(ctx, input, slices.Clone(peers))
				if err != nil || current() != nil {
					return nil, clusterbootstrap.ErrPreparationConfig
				}
				host, err := observation.HostFacts(started, item.Report.Hostname, item.Report.MachineID, item.Report.BootID,
					clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, peers)
				if err != nil {
					return nil, clusterbootstrap.ErrPreparationConfig
				}
				// Free space can change during SSH inspection itself. It is
				// neither consumed nor certified by a CPU/RAM profile.
				host.DiskFreeKiB = 0
				values = append(values, host)
			}
			return values, current()
		}
		first, err := read()
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		second, err := read()
		if err != nil || !slices.Equal(first, second) {
			return clusterbootstrap.ErrPreparationConfig
		}
		profile := make([]clusterSSHTargetHostEvidence, len(first))
		for i, host := range first {
			profile[i] = clusterSSHTargetHostEvidence{
				TargetID:  frozen.Cohort.Targets[i].Binding.TargetID,
				CPUCount:  host.CPUCount,
				MemoryKiB: host.MemoryKiB,
			}
		}
		if current() != nil || consume(ctx, profile) != nil || current() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		final, err := read()
		if err != nil || !slices.Equal(first, final) {
			return clusterbootstrap.ErrPreparationConfig
		}
		return current()
	})
}
