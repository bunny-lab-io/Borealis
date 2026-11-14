package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

type clusterSSHIdentityDispatchClient struct {
	deliver func(context.Context, *engineidentity.Material) error
	release func(context.Context, *engineidentity.Material) error
	closed  atomic.Bool
}

func (c *clusterSSHIdentityDispatchClient) DeliverIdentityRecovery(ctx context.Context, _ *clusterbootstrap.Bundle, _ clusterbootstrap.SessionBinding,
	_ clusterbootstrap.IdentityRecoveryRequest, material *engineidentity.Material, _ []byte, controller, source func(context.Context) error) (clusterbootstrap.SessionMessage, error) {
	if controller(ctx) != nil || source(ctx) != nil || c.deliver == nil {
		return clusterbootstrap.SessionMessage{}, errors.New("private delivery authority")
	}
	return clusterbootstrap.SessionMessage{}, c.deliver(ctx, material)
}

func (c *clusterSSHIdentityDispatchClient) ReleaseIdentityRecovery(ctx context.Context, _ *clusterbootstrap.Bundle, _ clusterbootstrap.SessionBinding,
	_ clusterbootstrap.IdentityRecoveryRequest, material *engineidentity.Material, _ []byte, controller, source func(context.Context) error) (clusterbootstrap.SessionMessage, error) {
	if controller(ctx) != nil || source(ctx) != nil || c.release == nil {
		return clusterbootstrap.SessionMessage{}, errors.New("private release authority")
	}
	return clusterbootstrap.SessionMessage{}, c.release(ctx, material)
}

func (c *clusterSSHIdentityDispatchClient) Close() error { c.closed.Store(true); return nil }

func TestClusterSSHIdentityDispatchReconcilesUncertainDelivery(t *testing.T) {
	material := sshIdentitySourceMaterial(t, t.TempDir())
	for _, mode := range []string{"delivered", "ambiguous delivery", "delivery connection failed"} {
		t.Run(mode, func(t *testing.T) {
			var opened, delivered, released atomic.Int32
			clients := []*clusterSSHIdentityDispatchClient{}
			connect := func(context.Context) (clusterSSHIdentityRecoveryClient, error) {
				call := opened.Add(1)
				if mode == "delivery connection failed" && call == 1 {
					return nil, errors.New("private connection detail")
				}
				client := &clusterSSHIdentityDispatchClient{}
				if call == 1 {
					client.deliver = func(_ context.Context, got *engineidentity.Material) error {
						delivered.Add(1)
						if !got.Equal(material) {
							t.Fatal("delivery material changed")
						}
						if mode == "ambiguous delivery" {
							return errors.New("private lost acknowledgement")
						}
						return nil
					}
				} else {
					client.release = func(_ context.Context, got *engineidentity.Material) error {
						released.Add(1)
						if !got.Equal(material) {
							t.Fatal("release material changed")
						}
						return nil
					}
				}
				clients = append(clients, client)
				return client, nil
			}
			check := func(context.Context) error { return nil }
			if err := dispatchClusterSSHIdentityRecovery(context.Background(), connect, &clusterbootstrap.Bundle{}, clusterbootstrap.SessionBinding{}, clusterbootstrap.IdentityRecoveryRequest{}, material, nil, check, check); err != nil {
				t.Fatalf("safe reconciliation failed: %v", err)
			}
			if opened.Load() != 2 || released.Load() != 1 || (mode == "delivery connection failed") != (delivered.Load() == 0) {
				t.Fatalf("wrong dispatch sequence: opened=%d delivered=%d released=%d", opened.Load(), delivered.Load(), released.Load())
			}
			for _, client := range clients {
				if !client.closed.Load() {
					t.Fatal("identity SSH client not closed")
				}
			}
		})
	}
}

func TestClusterSSHIdentityDispatchFailsClosed(t *testing.T) {
	material := sshIdentitySourceMaterial(t, t.TempDir())
	for _, mode := range []string{"nil connect", "cancelled", "release connection", "release proof"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var opened atomic.Int32
			var connect clusterSSHIdentityRecoveryConnect
			if mode != "nil connect" {
				connect = func(context.Context) (clusterSSHIdentityRecoveryClient, error) {
					call := opened.Add(1)
					if call == 2 && mode == "release connection" {
						return nil, errors.New("private release connection")
					}
					client := &clusterSSHIdentityDispatchClient{}
					if call == 1 {
						client.deliver = func(context.Context, *engineidentity.Material) error {
							if mode == "cancelled" {
								cancel()
							}
							return nil
						}
					} else {
						client.release = func(context.Context, *engineidentity.Material) error {
							if mode == "release proof" {
								return errors.New("private release proof")
							}
							return nil
						}
					}
					return client, nil
				}
			}
			check := func(context.Context) error { return nil }
			err := dispatchClusterSSHIdentityRecovery(ctx, connect, &clusterbootstrap.Bundle{}, clusterbootstrap.SessionBinding{}, clusterbootstrap.IdentityRecoveryRequest{}, material, nil, check, check)
			if !errors.Is(err, errClusterSSHIdentityDispatch) || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe failure: %v", err)
			}
			if mode == "cancelled" && opened.Load() != 1 {
				t.Fatalf("cancelled delivery opened release: %d", opened.Load())
			}
		})
	}
}

func TestClusterSSHIdentityDispatchKeepsSourceAuthorityAcrossBothActions(t *testing.T) {
	cohort, source, lease, baseline := sshPreparationFixture(t)
	current := clusterSSHPreparationAuthority{Cohort: cohort, Source: source, Lease: lease, Baseline: baseline, K3sVersion: "v1.36.3+k3s1"}
	expected := engineidentity.Binding{ClusterID: source.ClusterID, SourceID: source.Members[0].NodeID, SourceUID: source.Members[0].NodeUID}
	root := t.TempDir()
	want := sshIdentitySourceMaterial(t, root)
	consumer, err := engineidentity.AcquireConsumer(root)
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	var authorityCalls, opened atomic.Int32
	authority := func(context.Context) (clusterSSHPreparationAuthority, error) {
		call := authorityCalls.Add(1)
		value := current
		value.Cohort.ObservedAt += int64(call)
		return value, nil
	}
	connect := func(context.Context) (clusterSSHIdentityRecoveryClient, error) {
		call := opened.Add(1)
		client := &clusterSSHIdentityDispatchClient{}
		use := func(_ context.Context, got *engineidentity.Material) error {
			if !got.Equal(want) {
				t.Fatal("source identity changed during dispatch")
			}
			return nil
		}
		if call == 1 {
			client.deliver = use
		} else {
			client.release = use
		}
		return client, nil
	}
	check := func(context.Context) error { return nil }
	if err := withClusterSSHIdentityRecoveryDispatch(context.Background(), consumer, expected, authority, connect, &clusterbootstrap.Bundle{}, clusterbootstrap.SessionBinding{}, clusterbootstrap.IdentityRecoveryRequest{}, nil, check, check); err != nil {
		t.Fatal(err)
	}
	if authorityCalls.Load() != 3 || opened.Load() != 2 {
		t.Fatalf("authority/actions not bracketed: authority=%d opened=%d", authorityCalls.Load(), opened.Load())
	}
}
