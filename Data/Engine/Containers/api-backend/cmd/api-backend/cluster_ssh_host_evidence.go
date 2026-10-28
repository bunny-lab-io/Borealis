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

// Public scalar observations only. These values are not durable qualification
// receipts. The consumer owns sizing, storage and remaining clean-host policy.
type clusterSSHTargetHostEvidence struct {
	TargetID string
	Host     clusterremote.HostFacts
}

// Use only inside withClusterSSHNetworkTargets. Its original-claim heartbeat
// and joined readers remain active through this synchronous consumer. No
// database state or remote installation is changed by evidence collection.
func withClusterSSHTargetHostEvidence(parent context.Context, readers clusterSSHNetworkTargetReaders,
	consume func(context.Context, []clusterSSHTargetHostEvidence) error) error {
	if readers.within == nil || readers.Authority == nil || readers.Refresh == nil || readers.Peer == nil || consume == nil || parent.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	return readers.within(parent, func(parent context.Context) error {
		ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
		defer cancel()
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
		read := func() ([]clusterSSHTargetHostEvidence, error) {
			values := make([]clusterSSHTargetHostEvidence, 0, len(frozen.Cohort.Targets))
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
				values = append(values, clusterSSHTargetHostEvidence{TargetID: item.Binding.TargetID, Host: host})
			}
			return values, current()
		}
		first, err := read()
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		second, err := read()
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		for i := range first {
			free := min(first[i].Host.DiskFreeKiB, second[i].Host.DiskFreeKiB)
			first[i].Host.DiskFreeKiB, second[i].Host.DiskFreeKiB = 0, 0
			if first[i] != second[i] {
				return clusterbootstrap.ErrPreparationConfig
			}
			first[i].Host.DiskFreeKiB = free
		}
		if current() != nil || consume(ctx, slices.Clone(first)) != nil || current() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		final, err := read()
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		for i := range first {
			// A lower final reading invalidates the conservative availability
			// offered to the consumer. This still reserves no filesystem space.
			if final[i].Host.DiskFreeKiB < first[i].Host.DiskFreeKiB {
				return clusterbootstrap.ErrPreparationConfig
			}
			final[i].Host.DiskFreeKiB = first[i].Host.DiskFreeKiB
			if final[i] != first[i] {
				return clusterbootstrap.ErrPreparationConfig
			}
		}
		return current()
	})
}
