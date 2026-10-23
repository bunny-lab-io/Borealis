package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"errors"
	"time"
)

var errClusterSSHIdentityRecoveryWorker = errors.New("SSH identity recovery worker lost exact controller or target authority")

type clusterSSHIdentityRecoveryWorkerStore interface {
	renewClusterSSHIdentityRecoveryTarget(context.Context, clusterSSHIdentityRecoveryClaim) error
}

type clusterSSHIdentityRecoveryWorker struct {
	store       clusterSSHIdentityRecoveryWorkerStore
	aegis       clusterSSHWorkerAegis
	consumer    *engineidentity.Consumer
	connect     func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHIdentityRecoveryClient, error)
	bundleProof func(*clusterbootstrap.Bundle) (clusterbootstrap.Expected, error)
}

func newClusterSSHIdentityRecoveryWorker(store *postgresOperatorStore, aegis *goAegisService) *clusterSSHIdentityRecoveryWorker {
	return &clusterSSHIdentityRecoveryWorker{store: store, aegis: aegis, consumer: processIdentityConsumer,
		connect: func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHIdentityRecoveryClient, error) {
			return (clusterremote.Transport{}).Connect(ctx, target, key, credential)
		},
		bundleProof: func(bundle *clusterbootstrap.Bundle) (clusterbootstrap.Expected, error) {
			expected, _, _, err := bundle.ManagerProof()
			return expected, err
		}}
}

func clusterSSHIdentityRecoveryAuthorityMatches(value clusterSSHPreparationAuthority, claim clusterSSHIdentityRecoveryClaim) bool {
	if !claim.valid() || value.Lease != claim.Lease || value.Baseline != claim.Baseline || validateClusterSSHInspectionCohort(value.Cohort, value.Source) != nil {
		return false
	}
	sourceOK, targetOK := false, false
	for _, member := range value.Source.Members {
		if member.NodeID == claim.Source.SourceID && member.NodeUID == claim.Source.SourceUID {
			sourceOK = true
		}
	}
	for _, target := range value.Cohort.Targets {
		if target.Binding == claim.Sealed.binding && target.Report.Hostname == claim.Binding.Hostname && target.Report.MachineID == claim.Binding.MachineID &&
			target.Key.Algorithm == claim.Key.Algorithm && target.Key.Fingerprint == claim.Key.Fingerprint && bytes.Equal(target.Key.PublicKey, claim.Key.PublicKey) &&
			claim.Lease.Generation > target.Generation {
			targetOK = true
		}
	}
	return sourceOK && targetOK
}

// recover composes an already-issued durable claim with private credentials,
// source-key custody and remote journal reconciliation. Runtime queue polling,
// result completion, consumer restart and admission intentionally remain absent.
func (w *clusterSSHIdentityRecoveryWorker) recover(parent context.Context, claim clusterSSHIdentityRecoveryClaim,
	bundle *clusterbootstrap.Bundle, authority clusterSSHPreparationAuthorityRead, targetNode func(context.Context, clusterSSHIdentityRecoveryClaim) error) error {
	if w == nil || w.store == nil || w.aegis == nil || w.consumer == nil || w.connect == nil || w.bundleProof == nil ||
		!claim.valid() || bundle == nil || authority == nil || targetNode == nil || parent.Err() != nil {
		return errClusterSSHIdentityRecoveryWorker
	}
	expected, err := w.bundleProof(bundle)
	if err != nil || expected != claim.Baseline {
		return errClusterSSHIdentityRecoveryWorker
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
		return errClusterSSHIdentityRecoveryWorker
	}
	envelope, err := w.aegis.openClusterSSHCredentials(ctx, claim.Sealed, claim.Sealed.binding)
	if err != nil {
		return errClusterSSHIdentityRecoveryWorker
	}
	var credential *clusterremote.Credential
	switch envelope.Method {
	case "password":
		credential, err = clusterremote.PasswordCredential(envelope.Username, []byte(envelope.Password))
	case "private_key":
		credential, err = clusterremote.KeyCredential(envelope.Username, []byte(envelope.PrivateKey), []byte(envelope.Passphrase))
	default:
		return errClusterSSHIdentityRecoveryWorker
	}
	if err != nil {
		return errClusterSSHIdentityRecoveryWorker
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
		return errClusterSSHIdentityRecoveryWorker
	}
	return nil
}
