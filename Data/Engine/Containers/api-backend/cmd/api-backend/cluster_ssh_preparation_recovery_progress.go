package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"reflect"
	"sync"
	"sync/atomic"
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

// Private borrowed inputs live only inside consume. The caller selects its
// separately typed transport; this helper grants no native action capability.
type clusterSSHPreparationRecoveryTargetInputs struct {
	inputs              *clusterbootstrap.PreparationInputs
	expected            clusterbootstrap.PreparationExpected
	target              clusterremote.Target
	key                 clusterremote.HostKey
	credential          *clusterremote.Credential
	sudo                []byte
	current, inputCheck func(context.Context) error
}

func inspectClusterSSHPreparationRecoveryTarget(ctx context.Context, authority clusterSSHPreparationRecoveryAuthority, index int,
	sealed sealedClusterSSHCredentials, aegis clusterSSHWorkerAegis, scratchParent string, source clusterSSHPreparationRecoverySourceRead,
	check func(context.Context) error, dependencies clusterSSHPreparationRecoveryProgressDependencies) (clusterbootstrap.PreparationProgress, error) {
	var progress clusterbootstrap.PreparationProgress
	err := withClusterSSHPreparationRecoveryTargetInputs(ctx, authority, index, sealed, aegis, scratchParent, source, check,
		dependencies.download, dependencies.closeInputs, func(target clusterSSHPreparationRecoveryTargetInputs) error {
			if dependencies.connect == nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			client, connectErr := dependencies.connect(ctx, target.target, target.key, target.credential)
			if client == nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			actionErr, boundaryErr := runClusterSSHPreparationRecoverySession(ctx, target, client.Close, func() error {
				if connectErr != nil {
					return connectErr
				}
				var err error
				progress, err = client.InspectPreparationProgress(ctx, target.inputs, target.expected, target.sudo, target.current)
				return err
			})
			if actionErr != nil || boundaryErr != nil || progress.Validate() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		})
	if err != nil {
		return clusterbootstrap.PreparationProgress{}, err
	}
	return progress, nil
}

// Return action and boundary failures separately. Only an independently joined
// action failure may be followed by another proof; failed cleanup/authority
// never becomes absence or a reason to repeat an uncertain effect.
func runClusterSSHPreparationRecoverySession(ctx context.Context, target clusterSSHPreparationRecoveryTargetInputs,
	closeClient func() error, action func() error) (actionErr, boundaryErr error) {
	if closeClient == nil || action == nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	var once sync.Once
	closed := make(chan struct{})
	var closeErr error
	closeOwned := func() { once.Do(func() { closeErr = closeClient(); close(closed) }) }
	stop := context.AfterFunc(ctx, closeOwned)
	defer func() {
		stop()
		closeOwned()
		<-closed
		if closeErr != nil || target.current(ctx) != nil || target.inputCheck(ctx) != nil || ctx.Err() != nil {
			boundaryErr = clusterbootstrap.ErrPreparationConfig
		}
	}()
	if target.current(ctx) != nil || target.inputCheck(ctx) != nil || ctx.Err() != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return action(), nil
}

func withClusterSSHPreparationRecoveryTargetInputs(ctx context.Context, authority clusterSSHPreparationRecoveryAuthority, index int,
	sealed sealedClusterSSHCredentials, aegis clusterSSHWorkerAegis, scratchParent string, source clusterSSHPreparationRecoverySourceRead,
	check func(context.Context) error, download func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error),
	closeInputs func(*clusterbootstrap.PreparationInputs) error, consume func(clusterSSHPreparationRecoveryTargetInputs) error) (result error) {
	if index < 0 || index >= len(authority.Cohort.Targets) {
		return clusterbootstrap.ErrPreparationConfig
	}
	return withClusterSSHPreparationRecoveryProjectedInputs(ctx,
		clusterSSHPreparationRecoveryInputProjection{authority.Cohort.Targets[index], len(authority.Leases)},
		func(version, pods, services string) (clusterbootstrap.PreparationExpected, error) {
			return buildClusterSSHPreparationRecoveryExpected(authority, index, version, pods, services)
		}, index, sealed, aegis, scratchParent, source, check, download, closeInputs, consume)
}

// Immutable transport data only. Each typed wrapper retains its own exact
// authority builder; this projection cannot authorize another native action.
type clusterSSHPreparationRecoveryInputProjection struct {
	Item        clusterSSHInspectedTarget
	TargetCount int
}

func withClusterSSHPreparationRecoveryProjectedInputs(ctx context.Context, projection clusterSSHPreparationRecoveryInputProjection,
	build func(string, string, string) (clusterbootstrap.PreparationExpected, error), index int,
	sealed sealedClusterSSHCredentials, aegis clusterSSHWorkerAegis, scratchParent string, source clusterSSHPreparationRecoverySourceRead,
	check func(context.Context) error, download func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error),
	closeInputs func(*clusterbootstrap.PreparationInputs) error, consume func(clusterSSHPreparationRecoveryTargetInputs) error) (result error) {
	fail := func() error { return clusterbootstrap.ErrPreparationConfig }
	if aegis == nil || source == nil || check == nil || download == nil || consume == nil || build == nil || index < 0 || index >= projection.TargetCount {
		return fail()
	}
	item := projection.Item
	var failed atomic.Bool
	current := func(ctx context.Context) error {
		if failed.Load() || ctx.Err() != nil || check(ctx) != nil || aegis.verifyClusterSSHGeneration(ctx, sealed.generation) != nil || ctx.Err() != nil {
			failed.Store(true)
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
		if err != nil || len(value.Expected) != projection.TargetCount || index >= len(value.Expected) || current(ctx) != nil {
			return clusterbootstrap.PreparationExpected{}, nil, clusterbootstrap.ErrPreparationConfig
		}
		actual := value.Expected[index]
		bound, bindErr := build(actual.K3sVersion, actual.PodCIDR, actual.ServiceCIDR)
		if bindErr != nil || !reflect.DeepEqual(bound, actual) {
			return clusterbootstrap.PreparationExpected{}, nil, clusterbootstrap.ErrPreparationConfig
		}
		return value.Expected[index], value.Settings, nil
	}
	expected, runtime, err := read(ctx)
	if err != nil {
		return fail()
	}
	inputs, err := prepareClusterSSHTargetInputsWith(ctx, expected, runtime, scratchParent, read, download)
	if inputs != nil {
		defer func() {
			if closeInputs == nil {
				closeInputs = (*clusterbootstrap.PreparationInputs).Close
			}
			if closeInputs(inputs) != nil {
				result = fail()
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
	if current(ctx) != nil {
		return fail()
	}
	if consume(clusterSSHPreparationRecoveryTargetInputs{inputs, expected,
		clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, key, credential, sudo, current, inputCheck}) != nil ||
		current(ctx) != nil {
		return fail()
	}
	return nil
}
