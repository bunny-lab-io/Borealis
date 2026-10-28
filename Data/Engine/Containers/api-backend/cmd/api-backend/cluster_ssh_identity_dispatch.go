package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"errors"
)

var errClusterSSHIdentityDispatch = errors.New("SSH identity recovery outcome requires controller reconciliation")

type clusterSSHIdentityRecoveryClient interface {
	DeliverIdentityRecovery(context.Context, *clusterbootstrap.Bundle, clusterbootstrap.SessionBinding, clusterbootstrap.IdentityRecoveryRequest, *engineidentity.Material, []byte, func(context.Context) error, func(context.Context) error) (clusterbootstrap.SessionMessage, error)
	ReleaseIdentityRecovery(context.Context, *clusterbootstrap.Bundle, clusterbootstrap.SessionBinding, clusterbootstrap.IdentityRecoveryRequest, *engineidentity.Material, []byte, func(context.Context) error, func(context.Context) error) (clusterbootstrap.SessionMessage, error)
	Close() error
}

type clusterSSHIdentityRecoveryConnect func(context.Context) (clusterSSHIdentityRecoveryClient, error)

// dispatchClusterSSHIdentityRecovery treats every failed delivery as an
// uncertain remote outcome. It never repeats key writes based on that failure.
// Instead, a separately connected supervised session re-delivers the exact
// source material to prove/reconcile committed journals and release only the
// matching restart marker. A failed release leaves consumers fenced.
func dispatchClusterSSHIdentityRecovery(parent context.Context, connect clusterSSHIdentityRecoveryConnect,
	bundle *clusterbootstrap.Bundle, binding clusterbootstrap.SessionBinding, recovery clusterbootstrap.IdentityRecoveryRequest,
	material *engineidentity.Material, sudoPassword []byte, controller, sourceNode func(context.Context) error) error {
	if connect == nil || bundle == nil || material == nil || controller == nil || sourceNode == nil || parent.Err() != nil {
		return errClusterSSHIdentityDispatch
	}
	delivery, err := connect(parent)
	if err == nil && delivery != nil {
		_, _ = delivery.DeliverIdentityRecovery(parent, bundle, binding, recovery, material, sudoPassword, controller, sourceNode)
		_ = delivery.Close()
	} else if delivery != nil {
		_ = delivery.Close()
	}
	if parent.Err() != nil {
		return errClusterSSHIdentityDispatch
	}

	// Release is safe after success, ambiguous failure, or a failed delivery
	// connection: target proof decides whether exact committed work exists.
	release, err := connect(parent)
	if err != nil || release == nil {
		if release != nil {
			_ = release.Close()
		}
		return errClusterSSHIdentityDispatch
	}
	defer release.Close()
	if _, err := release.ReleaseIdentityRecovery(parent, bundle, binding, recovery, material, sudoPassword, controller, sourceNode); err != nil || parent.Err() != nil {
		return errClusterSSHIdentityDispatch
	}
	return nil
}

// withClusterSSHIdentityRecoveryDispatch keeps source keys under the API
// process consumer fence and exact controller/source-member authority across
// delivery plus reconciliation. Material is never returned or persisted here.
func withClusterSSHIdentityRecoveryDispatch(parent context.Context, consumer *engineidentity.Consumer,
	expected engineidentity.Binding, authority clusterSSHPreparationAuthorityRead, connect clusterSSHIdentityRecoveryConnect,
	bundle *clusterbootstrap.Bundle, binding clusterbootstrap.SessionBinding, recovery clusterbootstrap.IdentityRecoveryRequest,
	sudoPassword []byte, controller, sourceNode func(context.Context) error) error {
	return withClusterSSHSourceIdentityMaterial(parent, consumer, expected, authority, func(ctx context.Context, material *engineidentity.Material) error {
		return dispatchClusterSSHIdentityRecovery(ctx, connect, bundle, binding, recovery, material, sudoPassword, controller, sourceNode)
	})
}
