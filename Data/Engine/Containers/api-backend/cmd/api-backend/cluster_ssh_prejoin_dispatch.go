package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"errors"
	"net"
	"reflect"
	"slices"
	"time"
)

type clusterSSHPrejoinClient interface {
	DeliverPreparation(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	ReconcilePreparation(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	PrepareRuntimeIdentity(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	ReconcileRuntimeIdentity(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	PreparePrejoinIdentityRoot(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	ReconcilePrejoinIdentityRoot(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	InstallPrejoinIdentity(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	ReconcilePrejoinIdentity(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	PreparePrejoinHost(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	ReconcilePrejoinHost(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	Close() error
}

var _ clusterSSHPrejoinClient = (*clusterremote.Client)(nil)

func clusterSSHPrejoinDispatchKey(authority clusterSSHPreparationAuthority, source clusterSSHPrejoinSource,
	claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected, expected clusterbootstrap.PreparationExpected) (clusterremote.HostKey, error) {
	invalid := clusterremote.HostKey{}
	if !source.valid(authority) || authority.Lease != claim.Lease || authority.Baseline != baseline {
		return invalid, clusterbootstrap.ErrSessionAuthority
	}
	current, err := buildClusterSSHPreparationExpected(authority.Cohort, authority.Source, claim.Lease, baseline,
		authority.K3sVersion, expected.PodCIDR, expected.ServiceCIDR)
	if err != nil || !reflect.DeepEqual(current, expected) {
		return invalid, clusterbootstrap.ErrSessionAuthority
	}
	for _, target := range authority.Cohort.Targets {
		if target.Binding.TargetID == claim.Lease.TargetID {
			if target.Binding != claim.Sealed.binding || target.Key.Validate() != nil {
				return invalid, clusterbootstrap.ErrSessionAuthority
			}
			key := target.Key
			key.PublicKey = bytes.Clone(key.PublicKey)
			return key, nil
		}
	}
	return invalid, clusterbootstrap.ErrSessionAuthority
}

// Production prefix assembly selects no source and accepts no alternate path,
// command, transport or SSH pin. Full preparation caller owns the inputs and
// readiness/lease scope; prefix success is not a completion receipt. This has
// no polling caller until complete delivery and failure recovery are integrated.
func withClusterSSHPrejoinIdentityDispatch(parent context.Context, store *postgresOperatorStore, aegis *goAegisService,
	source clusterSSHPrejoinSource, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, preparation func(context.Context) error) error {
	if store == nil || store.db == nil || aegis == nil || inputs == nil || preparation == nil || !validClusterSSHPreparationLease(claim.Lease) ||
		expected.Validate() != nil || expected.Source != baseline || parent.Err() != nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	expected.PeerAddresses = slices.Clone(expected.PeerAddresses)
	authority := newClusterSSHPreparationAuthorityRead(store, aegis, claim.Lease, baseline, claim.Sealed)
	return withClusterSSHPrejoinProcessIdentity(parent, store, aegis, source, claim, baseline,
		func(ctx context.Context, material *engineidentity.Material, sourceRefresh func(context.Context) error) error {
			readKey := func(ctx context.Context) (clusterremote.HostKey, error) {
				if sourceRefresh(ctx) != nil || preparation(ctx) != nil || ctx.Err() != nil {
					return clusterremote.HostKey{}, clusterbootstrap.ErrSessionAuthority
				}
				current, err := authority(ctx)
				if err != nil {
					return clusterremote.HostKey{}, clusterbootstrap.ErrSessionAuthority
				}
				return clusterSSHPrejoinDispatchKey(current, source, claim, baseline, expected)
			}
			key, err := readKey(ctx)
			if err != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			check := func(ctx context.Context) error {
				current, err := readKey(ctx)
				if err != nil || current.Algorithm != key.Algorithm || current.Fingerprint != key.Fingerprint || !bytes.Equal(current.PublicKey, key.PublicKey) || ctx.Err() != nil {
					return clusterbootstrap.ErrSessionAuthority
				}
				return nil
			}
			if inputs.Verify(ctx, expected, check) != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			envelope, err := aegis.openClusterSSHCredentials(ctx, claim.Sealed, claim.Sealed.binding)
			if err != nil || envelope.Binding != claim.Sealed.binding || envelope.validate() != nil || check(ctx) != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			credential, sudo, err := clusterSSHNetworkCredential(envelope)
			envelope = clusterSSHCredentialEnvelope{}
			if err != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			defer credential.Destroy()
			defer clear(sudo)
			connect := func(ctx context.Context) (clusterSSHPrejoinClient, error) {
				client, err := (clusterremote.Transport{}).Connect(ctx,
					clusterremote.Target{Address: expected.Target.Address, Port: expected.Target.Port}, key, credential)
				if client == nil {
					return nil, err // Do not wrap a nil *Client in a non-nil interface.
				}
				return client, err
			}
			return dispatchClusterSSHPrejoinIdentity(ctx, connect, inputs, expected, source.Binding, material, sudo, check)
		})
}

// This is the source/account/identity/host prefix, not complete image delivery. It creates
// no settlement receipt, releases no marker and activates no worker/poller.
// Caller must retain verified inputs and source material inside their owned
// scopes, supply exact current preparation/domain/custody checks, and connect
// through the approved host-key/credential binding. Native methods independently
// reverify input bytes, helper, host, executor and both journals.
//
// Every write is followed by a separate proof-only session, even on success or
// lost connection. Never replay a write to resolve uncertainty. A partial effect
// that cannot be proven complete stops the prefix with the target still fenced.
func dispatchClusterSSHPrejoinIdentity(parent context.Context, connect func(context.Context) (clusterSSHPrejoinClient, error),
	inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding,
	material *engineidentity.Material, sudoPassword []byte, current func(context.Context) error) (result error) {
	fail := clusterbootstrap.ErrSessionAuthority
	if connect == nil || inputs == nil || expected.Validate() != nil || material == nil || material.Digest() == "" || current == nil ||
		clusterremote.ValidateSudoPassword(sudoPassword) != nil || source.ClusterID != expected.Target.ClusterID || source.SourceID == expected.Target.TargetID || parent.Err() != nil {
		return fail
	}
	for _, id := range []string{source.ClusterID, source.SourceID, source.SourceUID} {
		if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return fail
		}
	}
	expected.PeerAddresses = slices.Clone(expected.PeerAddresses)
	sudoPassword = bytes.Clone(sudoPassword)
	defer clear(sudoPassword)
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	checkCtx, stopChecks := context.WithCancel(ctx)
	lifetime := &clusterSSHNetworkReaderLifetime{ctx: checkCtx, cancel: stopChecks}
	defer func() {
		if lifetime.close() != nil {
			result = fail
		}
	}()
	gate := make(chan struct{}, 1)
	check := func(caller context.Context) error {
		if lifetime.run(caller, func(bound context.Context) error {
			bound, stop := context.WithTimeout(bound, 3*time.Second)
			defer stop()
			select {
			case gate <- struct{}{}:
				defer func() { <-gate }()
			case <-bound.Done():
				return fail
			}
			return current(bound)
		}) != nil {
			return fail
		}
		return nil
	}
	type action func(clusterSSHPrejoinClient, clusterbootstrap.PreparationExpected, []byte) error
	steps := [][2]action{
		{
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.DeliverPreparation(checkCtx, inputs, e, sudo, check)
			},
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.ReconcilePreparation(checkCtx, inputs, e, sudo, check)
			},
		},
		{
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.PrepareRuntimeIdentity(checkCtx, inputs, e, sudo, check)
			},
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.ReconcileRuntimeIdentity(checkCtx, inputs, e, sudo, check)
			},
		},
		{
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.PreparePrejoinIdentityRoot(checkCtx, inputs, e, source, material, sudo, check)
			},
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.ReconcilePrejoinIdentityRoot(checkCtx, inputs, e, source, material, sudo, check)
			},
		},
		{
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.InstallPrejoinIdentity(checkCtx, inputs, e, source, material, sudo, check)
			},
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.ReconcilePrejoinIdentity(checkCtx, inputs, e, source, material, sudo, check)
			},
		},
		{
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.PreparePrejoinHost(checkCtx, inputs, e, source, material, sudo, check)
			},
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.ReconcilePrejoinHost(checkCtx, inputs, e, source, material, sudo, check)
			},
		},
	}
	// Return action uncertainty separately from failed authority/cleanup. Only
	// the former permits the next distinct proof session, never another write.
	closeClient := func(client clusterSSHPrejoinClient) error {
		err := client.Close()
		// Failed protocol sessions already close their underlying connection.
		// Known closed is joined cleanup, not permission to repeat the action.
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
	run := func(work action) (actionErr, boundaryErr error) {
		if check(checkCtx) != nil {
			return fail, fail
		}
		client, err := connect(checkCtx)
		if err != nil || client == nil {
			if client != nil && closeClient(client) != nil {
				return fail, fail
			}
			return fail, nil
		}
		if check(checkCtx) != nil {
			_ = closeClient(client)
			return fail, fail
		}
		owned := expected
		owned.PeerAddresses = slices.Clone(expected.PeerAddresses)
		private := bytes.Clone(sudoPassword)
		err = work(client, owned, private)
		clear(private)
		if closeClient(client) != nil || check(checkCtx) != nil || checkCtx.Err() != nil {
			return fail, fail
		}
		return err, nil
	}
	for _, step := range steps {
		if _, boundaryErr := run(step[0]); boundaryErr != nil {
			return fail
		}
		if err, boundaryErr := run(step[1]); err != nil || boundaryErr != nil {
			return fail
		}
	}
	if check(checkCtx) != nil {
		return fail
	}
	return nil
}
