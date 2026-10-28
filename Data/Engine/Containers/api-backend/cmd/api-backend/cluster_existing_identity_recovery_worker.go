package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"os"
	"time"
)

type clusterExistingIdentityRecoveryStore interface {
	loadClusterExistingIdentityRecovery(context.Context, clusterExistingIdentityRecoveryClaim) (string, error)
	renewClusterExistingIdentityRecovery(context.Context, clusterExistingIdentityRecoveryClaim) error
	completeClusterExistingIdentityRecovery(context.Context, clusterExistingIdentityRecoveryClaim) error
	requireClusterExistingIdentityRecovery(context.Context, clusterExistingIdentityRecoveryClaim) error
}

type clusterExistingIdentityRecoveryWorker struct {
	store     clusterExistingIdentityRecoveryStore
	aegis     clusterSSHWorkerAegis
	consumer  *engineidentity.Consumer
	root      string
	custody   func(context.Context, clusterExistingIdentityRecoveryClaim, clusterExistingIdentitySourceProcess, func(context.Context) (string, error)) (clusterExistingIdentitySourceCustody, error)
	download  clusterSSHIdentityRecoveryDownload
	proof     func(*clusterbootstrap.Bundle) (clusterbootstrap.Expected, string, error)
	cleanup   func(*clusterbootstrap.Bundle) error
	connect   func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHIdentityRecoveryClient, error)
	heartbeat time.Duration
}

func (w *clusterExistingIdentityRecoveryWorker) recover(parent context.Context, claim clusterExistingIdentityRecoveryClaim, process clusterExistingIdentitySourceProcess) error {
	if w == nil || w.store == nil || w.aegis == nil || w.consumer == nil || w.custody == nil || w.download == nil || w.proof == nil || w.cleanup == nil || w.connect == nil ||
		w.heartbeat <= 0 || !claim.valid() || !process.valid(claim.Inspection.Plan) || parent.Err() != nil {
		return errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	defer cancel()
	sealed := claim.Inspection.Sealed
	authority := func(ctx context.Context) (string, error) {
		if w.aegis.verifyClusterSSHGeneration(ctx, sealed.generation) != nil {
			return "", errClusterConflict
		}
		return w.store.loadClusterExistingIdentityRecovery(ctx, claim)
	}
	controller := func(ctx context.Context) error {
		bound, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		if w.aegis.verifyClusterSSHGeneration(bound, sealed.generation) != nil || w.store.renewClusterExistingIdentityRecovery(bound, claim) != nil {
			return errClusterConflict
		}
		if _, err := authority(bound); err != nil || bound.Err() != nil {
			return errClusterConflict
		}
		return nil
	}
	failed := true
	defer func() {
		if failed {
			settle, stop := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
			defer stop()
			_ = w.store.requireClusterExistingIdentityRecovery(settle, claim)
		}
	}()
	if controller(ctx) != nil {
		return errClusterConflict
	}
	initial, err := w.custody(ctx, claim, process, authority)
	if err != nil || !initial.valid(claim.Inspection.Plan) || initial.Process != process {
		return errClusterConflict
	}
	custody := func(ctx context.Context) (clusterExistingIdentitySourceCustody, error) {
		proof, err := w.custody(ctx, claim, process, authority)
		if err != nil || proof != initial || ctx.Err() != nil {
			return clusterExistingIdentitySourceCustody{}, errClusterConflict
		}
		return proof, nil
	}
	source := func(ctx context.Context) error { _, err := custody(ctx); return err }
	done, stopHeartbeat := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(w.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopHeartbeat:
				return
			case <-ticker.C:
				if controller(ctx) != nil {
					cancel()
					return
				}
			}
		}
	}()
	joined := false
	join := func() {
		if !joined {
			close(stopHeartbeat)
			<-done
			joined = true
		}
	}
	defer join()
	bundle, err := w.download(ctx, claim.Inspection.Plan.Baseline, os.TempDir())
	closed := false
	cleanup := func() error {
		if closed || bundle == nil {
			return nil
		}
		if err := w.cleanup(bundle); err != nil {
			return err
		}
		closed = true
		return nil
	}
	defer cleanup()
	if err != nil || bundle == nil || ctx.Err() != nil {
		return errClusterConflict
	}
	expected, managerSHA, err := w.proof(bundle)
	if err != nil || expected != claim.Inspection.Plan.Baseline || managerSHA != claim.Result.Session.ManagerSHA256 || controller(ctx) != nil || source(ctx) != nil {
		return errClusterConflict
	}
	envelope, err := w.aegis.openClusterSSHCredentials(ctx, sealed, sealed.binding)
	if err != nil {
		return errClusterConflict
	}
	var credential *clusterremote.Credential
	switch envelope.Method {
	case "password":
		credential, err = clusterremote.PasswordCredential(envelope.Username, []byte(envelope.Password))
	case "private_key":
		credential, err = clusterremote.KeyCredential(envelope.Username, []byte(envelope.PrivateKey), []byte(envelope.Passphrase))
	default:
		return errClusterConflict
	}
	if err != nil {
		return errClusterConflict
	}
	defer credential.Destroy()
	sudo := []byte(envelope.SudoPassword)
	defer clear(sudo)
	envelope = clusterSSHCredentialEnvelope{}
	binding, recovery := claim.session()
	_, _, key := claim.Inspection.session()
	if binding.Validate() != nil || recovery.Validate(binding) != nil {
		return errClusterConflict
	}
	connect := func(ctx context.Context) (clusterSSHIdentityRecoveryClient, error) {
		if controller(ctx) != nil || source(ctx) != nil {
			return nil, errClusterConflict
		}
		return w.connect(ctx, clusterremote.Target{Address: binding.Address, Port: binding.Port}, key, credential)
	}
	if withClusterExistingIdentitySourceMaterial(ctx, w.consumer, w.root, claim.Inspection.Plan, process, authority, custody,
		func(ctx context.Context, material *engineidentity.Material) error {
			return dispatchClusterSSHIdentityRecovery(ctx, connect, bundle, binding, recovery, material, sudo, controller, source)
		}) != nil || ctx.Err() != nil || cleanup() != nil {
		return errClusterConflict
	}
	join()
	if controller(ctx) != nil || w.store.completeClusterExistingIdentityRecovery(ctx, claim) != nil || ctx.Err() != nil {
		return errClusterConflict
	}
	failed = false
	return nil
}

func (s *postgresOperatorStore) pendingClusterExistingIdentityRecoveries(ctx context.Context) ([]clusterSSHPendingIdentityRecovery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id,t.id FROM engine.cluster_operations o JOIN engine.cluster_state c ON c.id=1 AND c.active_operation_id=o.id
 JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id AND t.cluster_id=c.cluster_id AND t.operation_attempt=o.attempt
 JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
 WHERE o.kind=$1 AND o.state='running' AND o.current_step=$2 AND o.attempt=1 AND t.current_step=$2 AND t.state IN ('prepared','running','recovery_required')
 AND t.credential_state='available' AND t.lease_expires_at<=extract(epoch FROM clock_timestamp()) AND p.expires_at>extract(epoch FROM clock_timestamp())
 ORDER BY o.created_at,t.id LIMIT 1`, clusterExistingIdentityKind, clusterExistingIdentityRecover)
	if err != nil {
		return nil, errClusterConflict
	}
	defer rows.Close()
	var result []clusterSSHPendingIdentityRecovery
	for rows.Next() {
		var p clusterSSHPendingIdentityRecovery
		if rows.Scan(&p.OperationID, &p.TargetID) != nil {
			return nil, errClusterConflict
		}
		result = append(result, p)
	}
	if rows.Err() != nil {
		return nil, errClusterConflict
	}
	return result, nil
}

func startClusterExistingIdentityRecoveryRuntime(parent context.Context, auth *authService) func() {
	if auth == nil {
		return func() {}
	}
	store, ok := auth.store.(*postgresOperatorStore)
	aegis, aegisOK := auth.aegis.(*goAegisService)
	broker, err := newClusterExistingIdentityCustodyClient()
	if !ok || store == nil || !aegisOK || aegis == nil || err != nil || processIdentityConsumer == nil {
		return func() {}
	}
	w := &clusterExistingIdentityRecoveryWorker{store: store, aegis: aegis, consumer: processIdentityConsumer, root: clusterExistingIdentitySourceRoot,
		custody: broker.observeRecovery, download: prepareClusterSSHBootstrap, heartbeat: 10 * time.Second,
		proof: func(b *clusterbootstrap.Bundle) (clusterbootstrap.Expected, string, error) {
			e, digest, _, err := b.ManagerProof()
			return e, digest, err
		},
		cleanup: func(b *clusterbootstrap.Bundle) error { return b.Close() },
		connect: func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHIdentityRecoveryClient, error) {
			return (clusterremote.Transport{}).Connect(ctx, target, key, credential)
		},
	}
	runOnce := func(ctx context.Context) {
		key, err := aegis.activeKey()
		clear(key)
		if err != nil {
			return
		}
		lookup, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		pending, err := store.pendingClusterExistingIdentityRecoveries(lookup)
		if err != nil || len(pending) == 0 {
			return
		}
		p := pending[0]
		work, err := readClusterExistingIdentityRecoveryRow(lookup, store.db, p.OperationID, p.TargetID)
		if err != nil {
			return
		}
		candidate, err := parseClusterExistingIdentityRecoveryWork(work, p.OperationID, p.TargetID)
		if err != nil {
			return
		}
		process, err := clusterExistingIdentityLocalSource(candidate.Inspection.Plan)
		if err != nil {
			return
		}
		claim, err := store.claimClusterExistingIdentityRecovery(lookup, p.OperationID, p.TargetID, newClusterUUID(), process)
		if err != nil {
			return
		}
		stop()
		_ = w.recover(ctx, claim, process)
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			runOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
