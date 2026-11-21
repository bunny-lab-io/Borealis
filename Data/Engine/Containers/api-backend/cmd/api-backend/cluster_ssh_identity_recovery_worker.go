package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"errors"
	"reflect"
	"time"
)

var errClusterSSHIdentityRecoveryWorker = errors.New("SSH identity recovery worker lost exact controller or target authority")

type clusterSSHIdentityRecoveryWorkerStore interface {
	renewClusterSSHIdentityRecoveryTarget(context.Context, clusterSSHIdentityRecoveryClaim) error
	completeClusterSSHIdentityRecovery(context.Context, clusterSSHIdentityRecoveryClaim) error
	requireClusterSSHIdentityRecovery(context.Context, clusterSSHIdentityRecoveryClaim) error
}

type clusterSSHIdentityRecoveryWorker struct {
	store       clusterSSHIdentityRecoveryWorkerStore
	aegis       clusterSSHWorkerAegis
	consumer    *engineidentity.Consumer
	connect     func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHIdentityRecoveryClient, error)
	bundleProof func(*clusterbootstrap.Bundle) (clusterbootstrap.Expected, error)
	bundleClose func(*clusterbootstrap.Bundle) error
}

func newClusterSSHIdentityRecoveryWorker(store *postgresOperatorStore, aegis *goAegisService) *clusterSSHIdentityRecoveryWorker {
	return &clusterSSHIdentityRecoveryWorker{store: store, aegis: aegis, consumer: processIdentityConsumer,
		connect: func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHIdentityRecoveryClient, error) {
			return (clusterremote.Transport{}).Connect(ctx, target, key, credential)
		},
		bundleProof: func(bundle *clusterbootstrap.Bundle) (clusterbootstrap.Expected, error) {
			expected, _, _, err := bundle.ManagerProof()
			return expected, err
		},
		bundleClose: func(bundle *clusterbootstrap.Bundle) error { return bundle.Close() }}
}

func clusterSSHIdentityRecoveryAuthorityMatches(value clusterSSHPreparationAuthority, claim clusterSSHIdentityRecoveryClaim) bool {
	if !claim.valid() || value.Lease != claim.Lease || value.Baseline != claim.Baseline || value.Recovery == nil || !value.Recovery.matches(claim) ||
		validateClusterSSHInspectionCohort(value.Cohort, value.Source) != nil {
		return false
	}
	value.Cohort.ObservedAt, claim.Cohort.ObservedAt = 0, 0
	return reflect.DeepEqual(value.Cohort, claim.Cohort) && reflect.DeepEqual(value.Source, claim.Topology) &&
		claim.Inspected.Binding == claim.Sealed.binding && claim.Inspected.Report.Hostname == claim.Binding.Hostname &&
		claim.Inspected.Report.MachineID == claim.Binding.MachineID && claim.Inspected.Key.Algorithm == claim.Key.Algorithm &&
		claim.Inspected.Key.Fingerprint == claim.Key.Fingerprint && bytes.Equal(claim.Inspected.Key.PublicKey, claim.Key.PublicKey) &&
		claim.Lease.Generation > claim.Inspected.Generation
}

// recover consumes one verified bundle while composing an already-issued
// durable claim with private credentials, source-key custody and remote journal
// reconciliation. Private scratch must close before success; cleanup failure
// enters the same conservative recovery-required outcome.
// Controller transition, consumer restart and admission remain separate.
func (w *clusterSSHIdentityRecoveryWorker) recover(parent context.Context, claim clusterSSHIdentityRecoveryClaim,
	bundle *clusterbootstrap.Bundle, authority clusterSSHPreparationAuthorityRead, targetNode func(context.Context, clusterSSHIdentityRecoveryClaim) error) error {
	if w == nil || w.store == nil || w.aegis == nil || w.consumer == nil || w.connect == nil || w.bundleProof == nil ||
		!claim.valid() || bundle == nil || authority == nil || targetNode == nil {
		return errClusterSSHIdentityRecoveryWorker
	}
	closed := false
	closeBundle := w.bundleClose
	if closeBundle == nil {
		closeBundle = func(bundle *clusterbootstrap.Bundle) error { return bundle.Close() }
	}
	cleanup := func() error {
		if closed {
			return nil
		}
		err := closeBundle(bundle)
		if err == nil {
			closed = true
		}
		return err
	}
	fail := func() error {
		_ = cleanup()
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
		defer cancel()
		_ = w.store.requireClusterSSHIdentityRecovery(settleCtx, claim)
		return errClusterSSHIdentityRecoveryWorker
	}
	if parent.Err() != nil {
		return fail()
	}
	expected, err := w.bundleProof(bundle)
	if err != nil || expected != claim.Baseline {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, 11*time.Minute)
	defer cancel()
	controller := func(parent context.Context) error {
		checkCtx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		if w.aegis.verifyClusterSSHGeneration(checkCtx, claim.Sealed.generation) != nil ||
			w.store.renewClusterSSHIdentityRecoveryTarget(checkCtx, claim) != nil || checkCtx.Err() != nil {
			return errClusterSSHIdentityRecoveryWorker
		}
		value, err := authority(checkCtx)
		if err != nil || !clusterSSHIdentityRecoveryAuthorityMatches(value, claim) ||
			w.aegis.verifyClusterSSHGeneration(checkCtx, claim.Sealed.generation) != nil || checkCtx.Err() != nil {
			return errClusterSSHIdentityRecoveryWorker
		}
		return nil
	}
	node := func(checkCtx context.Context) error {
		if checkCtx.Err() != nil || targetNode(checkCtx, claim) != nil || checkCtx.Err() != nil {
			return errClusterSSHIdentityRecoveryWorker
		}
		return nil
	}
	if controller(ctx) != nil || node(ctx) != nil || controller(ctx) != nil {
		return fail()
	}
	envelope, err := w.aegis.openClusterSSHCredentials(ctx, claim.Sealed, claim.Sealed.binding)
	if err != nil {
		return fail()
	}
	var credential *clusterremote.Credential
	switch envelope.Method {
	case "password":
		credential, err = clusterremote.PasswordCredential(envelope.Username, []byte(envelope.Password))
	case "private_key":
		credential, err = clusterremote.KeyCredential(envelope.Username, []byte(envelope.PrivateKey), []byte(envelope.Passphrase))
	default:
		return fail()
	}
	if err != nil {
		return fail()
	}
	defer credential.Destroy()
	sudo := []byte(envelope.SudoPassword)
	defer clear(sudo)
	envelope = clusterSSHCredentialEnvelope{}
	connect := func(connectCtx context.Context) (clusterSSHIdentityRecoveryClient, error) {
		if controller(connectCtx) != nil {
			return nil, errClusterSSHIdentityRecoveryWorker
		}
		return w.connect(connectCtx, clusterremote.Target{Address: claim.Binding.Address, Port: claim.Binding.Port}, claim.Key, credential)
	}
	if withClusterSSHIdentityRecoveryDispatch(ctx, w.consumer, claim.Source, authority, connect, bundle, claim.Binding, claim.Recovery, sudo, controller, node) != nil || ctx.Err() != nil {
		return fail()
	}
	if cleanup() != nil {
		return fail()
	}
	if w.store.completeClusterSSHIdentityRecovery(ctx, claim) != nil || ctx.Err() != nil {
		return fail()
	}
	return nil
}
