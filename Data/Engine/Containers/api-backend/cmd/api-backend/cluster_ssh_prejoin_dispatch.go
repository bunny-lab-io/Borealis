package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

type clusterSSHPrejoinClient interface {
	InspectPreparationProgress(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) (clusterbootstrap.PreparationProgress, error)
	DeliverPreparation(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	ReconcilePreparationAbsent(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	ReconcilePreparation(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	PrepareRuntimeIdentity(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	CompleteRuntimeIdentity(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	ReconcileRuntimeIdentity(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, []byte, func(context.Context) error) error
	PreparePrejoinIdentityRoot(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	ReconcilePrejoinIdentityRoot(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	InstallPrejoinIdentity(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	ReconcilePrejoinIdentity(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	PreparePrejoinHost(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	CompletePrejoinHost(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	CompletePrejoinFirewall(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	ResumeFreshPrejoinHost(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	ResumeRepositoryPrejoinHost(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	ReconcilePrejoinHost(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, []byte, func(context.Context) error) error
	PublishPrejoinArtifacts(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, *clusterbootstrap.PreparationArtifacts, string, []byte, func(context.Context) error) error
	ReconcilePrejoinArtifacts(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, *clusterbootstrap.PreparationArtifacts, string, []byte, func(context.Context) error) error
	InstallPrejoinK3s(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, *clusterbootstrap.PreparationArtifacts, string, []byte, func(context.Context) error) error
	ReconcilePrejoinK3s(context.Context, *clusterbootstrap.PreparationInputs, clusterbootstrap.PreparationExpected, engineidentity.Binding, *engineidentity.Material, *clusterbootstrap.PreparationArtifacts, string, []byte, func(context.Context) error) error
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
		authority.K3sVersion, expected.PodCIDR, expected.ServiceCIDR, authority.payload)
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
	return withClusterSSHPrejoinDispatch(parent, store, aegis, source, claim, baseline, inputs, nil, "", expected, preparation)
}

func withClusterSSHPrejoinArtifactDispatch(parent context.Context, store *postgresOperatorStore, aegis *goAegisService,
	source clusterSSHPrejoinSource, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, expected clusterbootstrap.PreparationExpected, preparation func(context.Context) error) error {
	if artifacts == nil || !clusterbootstrap.ValidPostgresImageReference(postgresReference) {
		return clusterbootstrap.ErrSessionAuthority
	}
	return withClusterSSHPrejoinDispatch(parent, store, aegis, source, claim, baseline, inputs, artifacts, postgresReference, expected, preparation)
}

func withClusterSSHPrejoinDispatch(parent context.Context, store *postgresOperatorStore, aegis *goAegisService,
	source clusterSSHPrejoinSource, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, expected clusterbootstrap.PreparationExpected, preparation func(context.Context) error) error {
	return withClusterSSHPrejoinConfigurationDispatch(parent, store, aegis, source, claim, baseline, inputs, artifacts, postgresReference, expected, preparation, nil)
}

// Optional private join acquisition runs only at configuration, after installed
// service proof. Existing prefix entrypoints retain their seven-step boundary.
func withClusterSSHPrejoinConfigurationDispatch(parent context.Context, store *postgresOperatorStore, aegis *goAegisService,
	source clusterSSHPrejoinSource, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, expected clusterbootstrap.PreparationExpected, preparation func(context.Context) error,
	joinRead func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error) error {
	return withClusterSSHPrejoinStartupDispatch(parent, store, aegis, source, claim, baseline, inputs, artifacts, postgresReference, expected, preparation, joinRead, false)
}

func withClusterSSHPrejoinStartupDispatch(parent context.Context, store *postgresOperatorStore, aegis *goAegisService,
	source clusterSSHPrejoinSource, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, expected clusterbootstrap.PreparationExpected, preparation func(context.Context) error,
	joinRead func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error, startup bool) error {
	return withClusterSSHPrejoinCompletionDispatch(parent, store, aegis, source, claim, baseline, inputs, artifacts, postgresReference, expected, preparation, joinRead, startup, nil)
}

func withClusterSSHPrejoinCompletionDispatch(parent context.Context, store *postgresOperatorStore, aegis *goAegisService,
	source clusterSSHPrejoinSource, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, expected clusterbootstrap.PreparationExpected, preparation func(context.Context) error,
	joinRead func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error, startup bool, completion *clusterbootstrap.PrejoinPreparationReceipt) error {
	var proved clusterbootstrap.PrejoinPreparationReceipt
	var output *clusterbootstrap.PrejoinPreparationReceipt
	if startup {
		output = &proved
	}
	err := withClusterSSHPrejoinNativeScope(parent, store, aegis, source, claim, baseline, inputs, artifacts, postgresReference, expected, preparation, joinRead, startup,
		func(ctx context.Context, target clusterSSHPrejoinNativeTarget) error {
			return target.run(ctx, installAllSteps, output)
		})
	if err == nil && completion != nil {
		*completion = proved
	}
	return err
}

// Every action closes its native session before returning. The surrounding
// source-identity scope remains live across a cohort barrier and its mutation.
type clusterSSHPrejoinNativeTarget struct {
	current func(context.Context) error
	run     func(context.Context, clusterSSHInstallPhase, *clusterbootstrap.PrejoinPreparationReceipt) error
}

func (s *postgresOperatorStore) recordSSHPreparationCheckpoint(ctx context.Context, lease clusterSSHTargetLease, line string) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO engine.cluster_operation_events(operation_id,cluster_id,event_type,state,message,details_json,created_at)
SELECT o.id,t.cluster_id,'ssh_preparation_checkpoint',o.state,$6,json_build_object('attempt',o.attempt,'target_id',t.id,'claim_generation',t.lease_generation,'effects',CASE WHEN t.current_step='qualify' THEN 'none_read_only_qualification' ELSE 'consult_target_journal' END)::text,floor(extract(epoch FROM clock_timestamp()))
FROM engine.cluster_operations o JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id
WHERE o.id=$1 AND o.attempt=$2 AND t.id=$3 AND t.lease_generation=$4 AND t.lease_holder=$5`, lease.OperationID, lease.OperationAttempt, lease.TargetID, lease.Generation, lease.Holder, line)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errClusterConflict
	}
	return nil
}

// Stream already-redacted transport checkpoints outside the SSH/watchdog path.
// They are observational events and never serve as execution or settlement proof.
type clusterSSHPreparationObserverKey struct{}

func (s *postgresOperatorStore) observeSSHPreparationStages(ctx context.Context, lease clusterSSHTargetLease, run func(context.Context) error) error {
	lines := make(chan string, 64)
	done := make(chan struct{})
	var missed atomic.Int64
	go func() {
		defer close(done)
		var retryAfter time.Time
		for line := range lines {
			if time.Now().Before(retryAfter) && !strings.Contains(line, "status=failed") {
				missed.Add(1)
				continue
			}
			write, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			err := s.recordSSHPreparationCheckpoint(write, lease, line)
			cancel()
			if err == nil {
				continue
			}
			missed.Add(1)
			// Bound repeated storage waits. A later failure still gets its own
			// write; never replay the event with an uncertain write outcome.
			retryAfter = time.Now().Add(time.Second)
		}
	}()
	defer func() {
		close(lines)
		<-done
		if n := missed.Load(); n != 0 {
			log.Printf("SSH preparation checkpoint persistence incomplete operation=%s target=%s attempt=%d generation=%d missing=%d", lease.OperationID, lease.TargetID, lease.OperationAttempt, lease.Generation, n)
		}
	}()
	send := func(line string) {
		select {
		case lines <- line:
		default:
			missed.Add(1)
		}
	}
	ctx = context.WithValue(ctx, clusterSSHPreparationObserverKey{}, func(line string) {
		log.Printf("operation=%s target=%s attempt=%d generation=%d %s", lease.OperationID, lease.TargetID, lease.OperationAttempt, lease.Generation, line)
		send(line)
	})
	return run(clusterremote.WithPreparationObserver(ctx, send))
}

func withClusterSSHPrejoinNativeScope(parent context.Context, store *postgresOperatorStore, aegis *goAegisService,
	source clusterSSHPrejoinSource, claim clusterSSHNetworkTargetClaim, baseline clusterbootstrap.Expected,
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, expected clusterbootstrap.PreparationExpected, preparation func(context.Context) error,
	joinRead func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error, startup bool,
	consume func(context.Context, clusterSSHPrejoinNativeTarget) error) error {
	if store == nil || store.db == nil || aegis == nil || inputs == nil || preparation == nil || !validClusterSSHPreparationLease(claim.Lease) ||
		expected.Validate() != nil || expected.Source != baseline || parent.Err() != nil || startup && (joinRead == nil || artifacts == nil) || consume == nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	expected.PeerAddresses = slices.Clone(expected.PeerAddresses)
	authority := newClusterSSHPreparationAuthorityRead(store, aegis, claim.Lease, baseline, claim.Sealed)
	return withClusterSSHProcessIdentityPhase(parent, store, aegis, source, claim, baseline, clusterSSHStartupDispatchBudget(artifacts != nil, joinRead != nil, startup), startup,
		func(ctx context.Context, material *engineidentity.Material, sourceRefresh func(context.Context) error) error {
			readKey := func(ctx context.Context) (clusterremote.HostKey, error) {
				began := time.Now()
				failed := func(step string) (clusterremote.HostKey, error) {
					// Only trusted stage names and the local context status reach the log.
					log.Printf("native preparation authority rejected: step=%s elapsed=%s context=%v", step, time.Since(began).Round(time.Millisecond), ctx.Err())
					return clusterremote.HostKey{}, clusterbootstrap.ErrSessionAuthority
				}
				if sourceRefresh(ctx) != nil {
					return failed("source_custody")
				}
				if preparation(ctx) != nil || ctx.Err() != nil {
					return failed("preparation_authority")
				}
				current, err := authority(ctx)
				if err != nil {
					return failed("target_authority")
				}
				key, err := clusterSSHPrejoinDispatchKey(current, source, claim, baseline, expected)
				if err != nil {
					return failed("target_binding")
				}
				return key, nil
			}
			key, err := readKey(ctx)
			if err != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			actionBoundary := func(ctx context.Context) error {
				current, err := readKey(ctx)
				if err != nil || current.Algorithm != key.Algorithm || current.Fingerprint != key.Fingerprint || !bytes.Equal(current.PublicKey, key.PublicKey) || ctx.Err() != nil {
					return clusterbootstrap.ErrSessionAuthority
				}
				return nil
			}
			if _, pair := ctx.Value(clusterSSHPairManifestKey{}).(map[string]string); pair {
				// Host-key bytes and target identity came from the exact frozen
				// approval. The live pair fence includes both endpoints.
				actionBoundary = func(ctx context.Context) error {
					if preparation(ctx) != nil || sourceRefresh(ctx) != nil {
						return clusterbootstrap.ErrSessionAuthority
					}
					return ctx.Err()
				}
			}
			check := actionBoundary
			if _, bounded := clusterbootstrap.PreparationTimeRemaining(ctx); bounded {
				check = clusterbootstrap.CheckPreparationDeadline
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
				if actionBoundary(ctx) != nil {
					return nil, clusterbootstrap.ErrSessionAuthority
				}
				client, err := (clusterremote.Transport{}).Connect(ctx,
					clusterremote.Target{Address: expected.Target.Address, Port: expected.Target.Port}, key, credential)
				if client == nil {
					return nil, err // Do not wrap a nil *Client in a non-nil interface.
				}
				return client, err
			}
			runCtx, cancelRuns := context.WithCancel(ctx)
			lifetime := &clusterSSHNetworkReaderLifetime{ctx: runCtx, cancel: cancelRuns}
			defer lifetime.close()
			target := clusterSSHPrejoinNativeTarget{current: actionBoundary}
			target.run = func(caller context.Context, phase clusterSSHInstallPhase, completion *clusterbootstrap.PrejoinPreparationReceipt) error {
				return lifetime.run(caller, func(bound context.Context) error {
					return dispatchClusterSSHPrejoinPhase(bound, connect, inputs, artifacts, postgresReference, expected, source.Binding, material, sudo, check, joinRead, startup, completion, phase)
				})
			}
			consumeErr := consume(runCtx, target)
			if lifetime.close() != nil || consumeErr != nil || ctx.Err() != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			return nil
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
	return dispatchClusterSSHPrejoinArtifacts(parent, connect, inputs, nil, "", expected, source, material, sudoPassword, current)
}

func dispatchClusterSSHPrejoinArtifacts(parent context.Context, connect func(context.Context) (clusterSSHPrejoinClient, error),
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding,
	material *engineidentity.Material, sudoPassword []byte, current func(context.Context) error) (result error) {
	return dispatchClusterSSHPrejoinConfiguration(parent, connect, inputs, artifacts, postgresReference, expected, source, material, sudoPassword, current, nil)
}

func dispatchClusterSSHPrejoinConfiguration(parent context.Context, connect func(context.Context) (clusterSSHPrejoinClient, error),
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding,
	material *engineidentity.Material, sudoPassword []byte, current func(context.Context) error,
	joinRead func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error) (result error) {
	return dispatchClusterSSHPrejoinStartup(parent, connect, inputs, artifacts, postgresReference, expected, source, material, sudoPassword, current, joinRead, false)
}

func dispatchClusterSSHPrejoinStartup(parent context.Context, connect func(context.Context) (clusterSSHPrejoinClient, error),
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding,
	material *engineidentity.Material, sudoPassword []byte, current func(context.Context) error,
	joinRead func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error, startup bool) (result error) {
	return dispatchClusterSSHPrejoinCompletion(parent, connect, inputs, artifacts, postgresReference, expected, source, material, sudoPassword, current, joinRead, startup, nil)
}

func dispatchClusterSSHPrejoinCompletion(parent context.Context, connect func(context.Context) (clusterSSHPrejoinClient, error),
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding,
	material *engineidentity.Material, sudoPassword []byte, current func(context.Context) error,
	joinRead func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error, startup bool, completion *clusterbootstrap.PrejoinPreparationReceipt) (result error) {
	return dispatchClusterSSHPrejoinPhase(parent, connect, inputs, artifacts, postgresReference, expected, source, material, sudoPassword, current, joinRead, startup, completion, installAllSteps)
}

func dispatchClusterSSHPrejoinPhase(parent context.Context, connect func(context.Context) (clusterSSHPrejoinClient, error),
	inputs *clusterbootstrap.PreparationInputs, artifacts *clusterbootstrap.PreparationArtifacts, postgresReference string, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding,
	material *engineidentity.Material, sudoPassword []byte, current func(context.Context) error,
	joinRead func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error, startup bool, completion *clusterbootstrap.PrejoinPreparationReceipt,
	phase clusterSSHInstallPhase) (result error) {
	fail := clusterbootstrap.ErrSessionAuthority
	var receipt clusterbootstrap.PrejoinPreparationReceipt
	if phase < installAllSteps || phase > finishJoinedServer || phase != installAllSteps && !startup {
		return fail
	}
	if completion != nil && !startup {
		return fail
	}
	if joinRead != nil && artifacts == nil || startup && joinRead == nil {
		return fail
	}
	if artifacts != nil && !clusterbootstrap.ValidPostgresImageReference(postgresReference) || artifacts == nil && postgresReference != "" {
		return fail
	}
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
	maximum := clusterSSHStartupDispatchBudget(artifacts != nil, joinRead != nil, startup)
	ctx, cancel := context.WithTimeout(parent, maximum)
	defer cancel()
	checkCtx, stopChecks := context.WithCancel(ctx)
	lifetime := &clusterSSHNetworkReaderLifetime{ctx: checkCtx, cancel: stopChecks}
	defer func() {
		if lifetime.close() != nil {
			result = fail
		}
		if result == nil && completion != nil {
			*completion = receipt
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
	if artifacts != nil {
		steps = append(steps, [2]action{
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.PublishPrejoinArtifacts(checkCtx, inputs, e, source, material, artifacts, postgresReference, sudo, check)
			},
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.ReconcilePrejoinArtifacts(checkCtx, inputs, e, source, material, artifacts, postgresReference, sudo, check)
			},
		})
		steps = append(steps, [2]action{
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.InstallPrejoinK3s(checkCtx, inputs, e, source, material, artifacts, postgresReference, sudo, check)
			},
			func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.ReconcilePrejoinK3s(checkCtx, inputs, e, source, material, artifacts, postgresReference, sudo, check)
			},
		})
	}
	if joinRead != nil {
		configuration := func(reconcile, starting, verifying, activating bool) action {
			return func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				target, ok := c.(clusterSSHK3sConfigurationClient)
				if !ok {
					return fail
				}
				return joinRead(checkCtx, source, func(ctx context.Context, join *clusterbootstrap.K3sJoinMaterial, refresh func(context.Context) error) error {
					bound := func(ctx context.Context) error {
						if check(ctx) != nil || refresh == nil || refresh(ctx) != nil {
							return fail
						}
						return nil
					}
					if join.Verify(ctx, e, source, bound) != nil {
						return fail
					}
					operation := target.ConfigurePrejoinK3s
					if reconcile {
						operation = target.ReconcilePrejoinK3sConfiguration
					}
					if starting {
						starter, ok := c.(clusterSSHK3sStartupClient)
						if !ok {
							return fail
						}
						operation = starter.StartPreparedK3s
						if reconcile {
							operation = starter.ReconcilePreparedK3sStart
						}
					}
					if verifying {
						verifier, ok := c.(clusterSSHK3sStartupClient)
						if !ok {
							return fail
						}
						operation = verifier.VerifyPreparedImages
						if reconcile {
							operation = verifier.ReconcilePreparedImages
						}
					}
					if activating {
						activator, ok := c.(clusterSSHK3sStartupClient)
						if !ok {
							return fail
						}
						operation = activator.ActivatePrejoinIdentity
						if reconcile {
							receiver, ok := c.(clusterSSHPrejoinReceiptClient)
							if !ok {
								return fail
							}
							value, err := receiver.ReconcilePrejoinPreparation(ctx, inputs, e, source, material, artifacts, postgresReference, join, sudo, bound)
							if err != nil || value.Validate(e, source) != nil || bound(ctx) != nil {
								return fail
							}
							receipt = value
							return nil
						}
					}
					if operation(ctx, inputs, e, source, material, artifacts, postgresReference, join, sudo, bound) != nil || bound(ctx) != nil {
						return fail
					}
					return nil
				})
			}
		}
		steps = append(steps, [2]action{configuration(false, false, false, false), configuration(true, false, false, false)})
		if startup {
			steps = append(steps, [2]action{configuration(false, true, false, false), configuration(true, true, false, false)})
			steps = append(steps, [2]action{configuration(false, false, true, false), configuration(true, false, true, false)})
			steps = append(steps, [2]action{configuration(false, false, false, true), configuration(true, false, false, true)})
		}
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
	// Inspection is a cursor, not completion proof. A failed query never becomes
	// an empty journal. Reconcile exactly its retained stage in a new executor
	// before any later write; actual source/account/key/host proof remains there.
	var progress clusterbootstrap.PreparationProgress
	if err, boundaryErr := run(func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
		var err error
		progress, err = c.InspectPreparationProgress(checkCtx, inputs, e, sudo, check)
		return err
	}); err != nil || boundaryErr != nil || progress.Validate() != nil {
		return fail
	}
	absent := func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
		return c.ReconcilePreparationAbsent(checkCtx, inputs, e, sudo, check)
	}
	if phase == prepareBeforeJoin && clusterbootstrap.CheckPairExecutorCursor(expected.Target.TargetID, progress.Step) != nil {
		reportClusterSSHPrejoinCheck(checkCtx, "retained pair journal checkpoint differs from recovery authorization")
		return clusterbootstrap.ErrMutationUnknown
	}
	if progress.Step == "source_absent" {
		// The receiver admits this cursor only for a strictly newer claim.
		// Repeat actual absence proof before clean no-replace publication.
		if err, boundaryErr := run(absent); err != nil || boundaryErr != nil {
			return fail
		}
	}
	proveStep := func(index int) error {
		err, boundaryErr := run(steps[index][1])
		if err == nil && boundaryErr == nil {
			return nil
		}
		if boundaryErr == nil && index == 1 {
			// Only the native fresh-claim/group-only contract can transfer
			// intent before completing the missing user. Ambiguous completion
			// still requires an independent complete-account proof.
			if _, boundaryErr := run(func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.CompleteRuntimeIdentity(checkCtx, inputs, e, sudo, check)
			}); boundaryErr != nil {
				return fail
			}
			if err, boundaryErr := run(steps[index][1]); err == nil && boundaryErr == nil {
				return nil
			}
		}
		if boundaryErr == nil && index == 4 {
			// Fixed native continuation requires a fresh claim and independent
			// configured-host proof. Delivery ambiguity still needs complete proof.
			if _, boundaryErr := run(func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.CompletePrejoinHost(checkCtx, inputs, e, source, material, sudo, check)
			}); boundaryErr != nil {
				return fail
			}
			err, boundaryErr := run(steps[index][1])
			if boundaryErr != nil {
				return fail
			}
			if err == nil {
				return nil
			}
			// This class requires complete non-firewall state and a stopped,
			// canonical unit with exactly owned interrupted rules. The journal
			// still requires a newer claim before any continuation effect.
			if _, boundaryErr := run(func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.CompletePrejoinFirewall(checkCtx, inputs, e, source, material, sudo, check)
			}); boundaryErr != nil {
				return fail
			}
			err, boundaryErr = run(steps[index][1])
			if boundaryErr != nil {
				return fail
			}
			if err == nil {
				return nil
			}
			// No uncertain package repair: native fresh proof must show no
			// Docker/repository/host-file effects and a configured dpkg inventory.
			if _, boundaryErr := run(func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.ResumeFreshPrejoinHost(checkCtx, inputs, e, source, material, sudo, check)
			}); boundaryErr != nil {
				return fail
			}
			err, boundaryErr = run(steps[index][1])
			if boundaryErr != nil {
				return fail
			}
			if err == nil {
				return nil
			}
			// Exact retained repository prefix, configured base inventory and
			// absent Docker only. Native proof rejects unknown package state.
			if _, boundaryErr := run(func(c clusterSSHPrejoinClient, e clusterbootstrap.PreparationExpected, sudo []byte) error {
				return c.ResumeRepositoryPrejoinHost(checkCtx, inputs, e, source, material, sudo, check)
			}); boundaryErr != nil {
				return fail
			}
			if err, boundaryErr := run(steps[index][1]); err == nil && boundaryErr == nil {
				return nil
			}
		}
		if boundaryErr == nil && index == 0 {
			// Failed completion is not absence. A separate fixed proof may
			// record absence for a future fresh claim; this dispatch still
			// stops and never retries its uncertain source write.
			_, _ = run(absent)
		}
		return fail
	}
	retained := -1
	switch progress.Step {
	case "stage_source":
		retained = 0
	case "prepare_runtime_identity":
		retained = 1
	case "prepare_identity_root":
		retained = 2
	case "install_identity":
		retained = 3
	case "prepare_host":
		retained = 4
	case "stage_images":
		if artifacts == nil {
			return fail
		}
		retained = 5
	case "install_k3s":
		if artifacts == nil {
			return fail
		}
		retained = 6
	case "configure_k3s":
		if joinRead == nil {
			return fail
		}
		retained = 7
	case "start_k3s":
		if !startup {
			return fail
		}
		retained = 8
	case "verify_images":
		if !startup {
			return fail
		}
		retained = 9
	case "activate_identity":
		if !startup {
			return fail
		}
		retained = 10
	}
	// A cursor selects a read-only reconciliation. Only that proof can skip
	// an effect, including an interrupted start that may already have joined.
	end := len(steps)
	switch phase {
	case prepareBeforeJoin:
		end = 8
	case joinDrainedServer:
		if retained < 7 {
			return fail
		}
		end = 9
	case finishJoinedServer:
		if retained < 8 {
			return fail
		}
	}
	if retained >= 0 && proveStep(retained) != nil {
		return fail
	}
	for index := retained + 1; index < end; index++ {
		step := steps[index]
		actionErr, boundaryErr := run(step[0])
		if boundaryErr != nil {
			return fail
		}
		// Each successful native action proves its effect before recording
		// applied. Only ambiguous delivery and the final admission receipt
		// need a separate executor to reconcile on an approved pair.
		_, pair := parent.Value(clusterSSHPairManifestKey{}).(map[string]string)
		if pair && actionErr == nil && index != 10 {
			continue
		}
		if proveStep(index) != nil {
			return fail
		}
	}
	if check(checkCtx) != nil {
		return fail
	}
	return nil
}

// The identity-material scope and dispatcher must share this ceiling; a hidden
// ten-minute parent must not truncate otherwise bounded bulk sessions. This
// grants no extension to credential expiry, leases or per-command deadlines.
func clusterSSHPrejoinDispatchBudget(artifacts, configuration bool) time.Duration {
	// Host packages use the existing bulk transport for initial preparation
	// and the two separately guarded fresh/repository continuations. Include
	// those bounded sessions; credential expiry and every lease still govern.
	maximum := 10*time.Minute + 3*clusterbootstrap.ArtifactPreparationLifetime
	if artifacts {
		maximum += 4 * clusterbootstrap.ArtifactPreparationLifetime
		if configuration {
			maximum += 2 * clusterbootstrap.ArtifactPreparationLifetime
		}
	}
	return maximum
}

func clusterSSHStartupDispatchBudget(artifacts, configuration, startup bool) time.Duration {
	maximum := clusterSSHPrejoinDispatchBudget(artifacts, configuration)
	if startup && artifacts && configuration {
		// Startup, image verification and identity activation each use two
		// separately bounded delivery/proof sessions.
		maximum += 6 * clusterbootstrap.ArtifactPreparationLifetime
	}
	return maximum
}
