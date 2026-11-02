package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"reflect"
	"sync"
)

type clusterSSHPreparationRecoveryProgressClient interface {
	InspectPreparationProgress(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) (clusterbootstrap.PreparationProgress, error)
	Close() error
}
type clusterSSHPreparationRecoveryProgressConnect func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error)
type clusterSSHPreparationRecoveryProgressDependencies struct {
	source      func(clusterSSHPreparationRecoveryAuthority, string, func(context.Context) error) clusterSSHPreparationRecoverySourceRead
	download    func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error)
	connect     clusterSSHPreparationRecoveryProgressConnect
	closeInputs func(*clusterbootstrap.PreparationInputs) error
}
type clusterSSHPreparationRecoveryTargetProgress struct {
	TargetID string
	Progress clusterbootstrap.PreparationProgress
}

func connectClusterSSHPreparationRecoveryProgress(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationRecoveryProgressClient, error) {
	client, err := (clusterremote.Transport{}).Connect(ctx, target, key, credential)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// Callable original-controller assembly only. Existing fixed progress command
// may create/adopt journal owner metadata. It cannot write intents/history,
// continue effects, settle targets or admit membership. Returned cursors carry
// no live authority and remain private until all cleanup and SQL fences finish.
func (c *clusterController) observeClusterSSHPreparationRecoveryProgress(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryClaim, scratchParent string) ([]clusterSSHPreparationRecoveryTargetProgress, error) {
	return c.observeClusterSSHPreparationRecoveryProgressWith(parent, aegis, issued, scratchParent, clusterSSHPreparationRecoveryProgressDependencies{
		source: c.preparationRecoverySourceRead, download: prepareClusterSSHBootstrap, connect: connectClusterSSHPreparationRecoveryProgress,
	})
}

func (c *clusterController) observeClusterSSHPreparationRecoveryProgressWith(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryClaim, scratchParent string, dependencies clusterSSHPreparationRecoveryProgressDependencies) ([]clusterSSHPreparationRecoveryTargetProgress, error) {
	// Copy caller-owned slices before the scope starts. Custody remains shared and
	// one-use; neither this copy nor a retained cursor can reconstruct a lost ACK.
	claim, _, err := freezeClusterSSHPreparationRecoveryClaim(issued)
	if err != nil {
		// Preserve the scope's consume-before-validation rule even when this
		// adapter cannot make its detached credential copy.
		_ = c.withClusterSSHPreparationRecoveryProof(parent, aegis, issued, nil)
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	var result []clusterSSHPreparationRecoveryTargetProgress
	err = c.withClusterSSHPreparationRecoveryProof(parent, aegis, claim, func(ctx context.Context, authority clusterSSHPreparationRecoveryAuthority, check func(context.Context) error) error {
		if dependencies.source == nil || dependencies.download == nil || dependencies.connect == nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		read := dependencies.source(authority, claim.payload, check)
		if read == nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		for i, target := range claim.Claims {
			progress, err := inspectClusterSSHPreparationRecoveryTarget(ctx, authority, i, target.Sealed, aegis, scratchParent, read, check, dependencies)
			if err != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			result = append(result, clusterSSHPreparationRecoveryTargetProgress{TargetID: target.Lease.TargetID, Progress: progress})
		}
		// Recheck full original configuration after the complete cohort and after
		// target SSH/input cleanup, before the proof scope's final joined fence.
		if _, err := read(ctx); err != nil || check(ctx) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	})
	if err != nil {
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	return result, nil
}

func inspectClusterSSHPreparationRecoveryTarget(ctx context.Context, authority clusterSSHPreparationRecoveryAuthority, index int,
	sealed sealedClusterSSHCredentials, aegis clusterSSHWorkerAegis, scratchParent string, source clusterSSHPreparationRecoverySourceRead,
	check func(context.Context) error, dependencies clusterSSHPreparationRecoveryProgressDependencies) (progress clusterbootstrap.PreparationProgress, result error) {
	fail := func() (clusterbootstrap.PreparationProgress, error) {
		return clusterbootstrap.PreparationProgress{}, clusterbootstrap.ErrPreparationConfig
	}
	if aegis == nil || source == nil || check == nil || dependencies.download == nil || dependencies.connect == nil || index < 0 || index >= len(authority.Cohort.Targets) {
		return fail()
	}
	item := authority.Cohort.Targets[index]
	current := func(ctx context.Context) error {
		if ctx.Err() != nil || check(ctx) != nil || aegis.verifyClusterSSHGeneration(ctx, sealed.generation) != nil || ctx.Err() != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		return nil
	}
	if sealed.binding != item.Binding || current(ctx) != nil {
		return fail()
	}
	read := func(ctx context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error) {
		if current(ctx) != nil {
			return clusterbootstrap.PreparationExpected{}, nil, clusterbootstrap.ErrSessionAuthority
		}
		value, err := source(ctx)
		if err != nil || len(value.Expected) != len(authority.Leases) || index >= len(value.Expected) || current(ctx) != nil {
			return clusterbootstrap.PreparationExpected{}, nil, clusterbootstrap.ErrPreparationConfig
		}
		actual := value.Expected[index]
		bound, bindErr := buildClusterSSHPreparationRecoveryExpected(authority, index, actual.K3sVersion, actual.PodCIDR, actual.ServiceCIDR)
		if bindErr != nil || !reflect.DeepEqual(bound, actual) {
			return clusterbootstrap.PreparationExpected{}, nil, clusterbootstrap.ErrPreparationConfig
		}
		return value.Expected[index], value.Settings, nil
	}
	expected, runtime, err := read(ctx)
	if err != nil {
		return fail()
	}
	inputs, err := prepareClusterSSHTargetInputsWith(ctx, expected, runtime, scratchParent, read, dependencies.download)
	if inputs != nil {
		defer func() {
			closeInputs := dependencies.closeInputs
			if closeInputs == nil {
				closeInputs = (*clusterbootstrap.PreparationInputs).Close
			}
			if closeInputs(inputs) != nil {
				progress, result = fail()
			}
		}()
	}
	if err != nil || inputs == nil || current(ctx) != nil {
		return fail()
	}
	config, err := clusterbootstrap.NewPreparationConfiguration(expected, runtime)
	if err != nil {
		return fail()
	}
	inputCheck := newClusterSSHPreparationInputCheck(config, read)
	envelope, err := aegis.openClusterSSHCredentials(ctx, sealed, item.Binding)
	if err != nil || envelope.Binding != item.Binding || envelope.validate() != nil || current(ctx) != nil {
		return fail()
	}
	credential, sudo, err := clusterSSHNetworkCredential(envelope)
	envelope = clusterSSHCredentialEnvelope{}
	if err != nil {
		return fail()
	}
	defer credential.Destroy()
	defer clear(sudo)
	key := item.Key
	key.PublicKey = bytes.Clone(key.PublicKey)
	client, err := dependencies.connect(ctx, clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, key, credential)
	if client != nil {
		var closeOnce sync.Once
		closed := make(chan struct{})
		var closeErr error
		closeClient := func() { closeOnce.Do(func() { closeErr = client.Close(); close(closed) }) }
		stop := context.AfterFunc(ctx, closeClient)
		defer func() {
			stop()
			closeClient()
			<-closed
			if closeErr != nil {
				progress, result = fail()
			}
		}()
	}
	if err != nil || client == nil || current(ctx) != nil || inputCheck(ctx) != nil {
		return fail()
	}
	progress, err = client.InspectPreparationProgress(ctx, inputs, expected, sudo, current)
	if err != nil || progress.Validate() != nil || inputCheck(ctx) != nil || current(ctx) != nil {
		return fail()
	}
	return progress, nil
}
