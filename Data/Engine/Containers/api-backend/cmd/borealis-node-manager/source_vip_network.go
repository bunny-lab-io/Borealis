package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"time"
)

func (m *manager) inspectSourceVIPNetwork(parent context.Context, address string) (map[string]any, error) {
	if !clusterbootstrap.ValidVIPRequest(address) {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	read := func(ctx context.Context) (clusterbootstrap.SourceNetwork, error) {
		value, err := m.inspectSourceNetwork(ctx)
		if err != nil {
			return clusterbootstrap.SourceNetwork{}, clusterbootstrap.ErrPreparationConfig
		}
		network, ok := value["source_network"].(clusterbootstrap.SourceNetwork)
		if !ok {
			return clusterbootstrap.SourceNetwork{}, clusterbootstrap.ErrPreparationConfig
		}
		return network, nil
	}
	value, err := observeSourceVIPNetwork(ctx, address, read, sourceNetworkNamespace, func(ctx context.Context) ([]byte, error) {
		return sourceLinkCommand(ctx, "/usr/sbin/ip", "-j", "-d", "-4", "address", "show")
	})
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return map[string]any{"source_vip_network": value}, nil
}

func observeSourceVIPNetwork(ctx context.Context, address string, source func(context.Context) (clusterbootstrap.SourceNetwork, error),
	namespace func() (uint64, error), read func(context.Context) ([]byte, error)) (clusterbootstrap.SourceVIPNetwork, error) {
	fail := func() (clusterbootstrap.SourceVIPNetwork, error) {
		return clusterbootstrap.SourceVIPNetwork{}, clusterbootstrap.ErrPreparationConfig
	}
	if !clusterbootstrap.ValidVIPRequest(address) || source == nil || namespace == nil || read == nil || ctx.Err() != nil {
		return fail()
	}
	before, err := source(ctx)
	if err != nil || before.Validate() != nil || ctx.Err() != nil {
		return fail()
	}
	observe := func() (clusterbootstrap.VIPAddress, error) {
		beforeNS, err := namespace()
		if err != nil || beforeNS != before.ManagementLink.NetworkNamespace || ctx.Err() != nil {
			return clusterbootstrap.VIPAddress{}, clusterbootstrap.ErrPreparationConfig
		}
		raw, err := read(ctx)
		if err != nil {
			return clusterbootstrap.VIPAddress{}, clusterbootstrap.ErrPreparationConfig
		}
		value, err := clusterbootstrap.ParseVIPAddress(raw, before.ManagementLink, address)
		afterNS, other := namespace()
		if err != nil || other != nil || beforeNS != afterNS || ctx.Err() != nil {
			return clusterbootstrap.VIPAddress{}, clusterbootstrap.ErrPreparationConfig
		}
		return value, nil
	}
	vip, err := observe()
	if err != nil {
		return fail()
	}
	after, err := source(ctx)
	if err != nil || before != after || ctx.Err() != nil {
		return fail()
	}
	current, err := observe()
	if err != nil || vip != current || ctx.Err() != nil {
		return fail()
	}
	value := clusterbootstrap.SourceVIPNetwork{Network: before, VIP: vip}
	if value.Validate() != nil {
		return fail()
	}
	return value, nil
}
