package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"os"
	"time"
)

type clusterExistingIdentityInspectionStore interface {
	loadClusterExistingIdentityInspection(context.Context, clusterExistingIdentityInspectionClaim) (string, error)
	renewClusterExistingIdentityInspection(context.Context, clusterExistingIdentityInspectionClaim) error
	completeClusterExistingIdentityInspection(context.Context, clusterExistingIdentityInspectionClaim, clusterExistingIdentityInspectionResult) error
}

type clusterExistingIdentityInspectionClient interface {
	VerifyIdentityRecovery(context.Context, *clusterbootstrap.Bundle, clusterbootstrap.SessionBinding, clusterbootstrap.IdentityRecoveryRequest, []byte, func(context.Context) error, func(context.Context) error) (clusterbootstrap.SessionMessage, error)
	Close() error
}

type clusterExistingIdentityInspectionWorker struct {
	store     clusterExistingIdentityInspectionStore
	aegis     clusterSSHWorkerAegis
	custody   func(context.Context, clusterExistingIdentityInspectionClaim, clusterExistingIdentitySourceProcess, func(context.Context) (string, error)) (clusterExistingIdentitySourceCustody, error)
	download  clusterSSHIdentityRecoveryDownload
	proof     func(*clusterbootstrap.Bundle) (clusterbootstrap.Expected, string, error)
	cleanup   func(*clusterbootstrap.Bundle) error
	connect   func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterExistingIdentityInspectionClient, error)
	heartbeat time.Duration
}

func (w *clusterExistingIdentityInspectionWorker) inspect(parent context.Context, claim clusterExistingIdentityInspectionClaim, process clusterExistingIdentitySourceProcess) error {
	if w == nil || w.store == nil || w.aegis == nil || w.custody == nil || w.download == nil || w.proof == nil || w.cleanup == nil || w.connect == nil ||
		w.heartbeat <= 0 || !claim.valid() || !process.valid(claim.Plan) || parent.Err() != nil {
		return errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	authority := func(ctx context.Context) (string, error) {
		if w.aegis.verifyClusterSSHGeneration(ctx, claim.Sealed.generation) != nil {
			return "", errClusterConflict
		}
		return w.store.loadClusterExistingIdentityInspection(ctx, claim)
	}
	controller := func(ctx context.Context) error {
		bound, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		if w.aegis.verifyClusterSSHGeneration(bound, claim.Sealed.generation) != nil || w.store.renewClusterExistingIdentityInspection(bound, claim) != nil {
			return errClusterConflict
		}
		if _, err := authority(bound); err != nil || bound.Err() != nil {
			return errClusterConflict
		}
		return nil
	}
	if controller(ctx) != nil {
		return errClusterConflict
	}
	initial, err := w.custody(ctx, claim, process, authority)
	if err != nil || !initial.valid(claim.Plan) || initial.Process != process {
		return errClusterConflict
	}
	source := func(ctx context.Context) error {
		proof, err := w.custody(ctx, claim, process, authority)
		if err != nil || proof != initial || ctx.Err() != nil {
			return errClusterConflict
		}
		return nil
	}
	// Download and SSH can each outlive one lease. Joined heartbeat owns no
	// credentials and cannot outlive this synchronous worker scope.
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
	bundle, err := w.download(ctx, claim.Plan.Baseline, os.TempDir())
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
	if err != nil || expected != claim.Plan.Baseline || !validClusterSSHPreparationDigest(managerSHA) || controller(ctx) != nil || source(ctx) != nil {
		return errClusterConflict
	}
	envelope, err := w.aegis.openClusterSSHCredentials(ctx, claim.Sealed, claim.Sealed.binding)
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
	binding, recovery, key := claim.session()
	if binding.Validate() != nil || recovery.Validate(binding) != nil || controller(ctx) != nil || source(ctx) != nil {
		return errClusterConflict
	}
	client, err := w.connect(ctx, clusterremote.Target{Address: binding.Address, Port: binding.Port}, key, credential)
	if err != nil || client == nil {
		if client != nil {
			_ = client.Close()
		}
		return errClusterConflict
	}
	message, err := client.VerifyIdentityRecovery(ctx, bundle, binding, recovery, sudo, controller, source)
	closeErr := client.Close()
	result := clusterExistingIdentityInspectionResult{Version: 1, PlanSHA256: claim.Confirmation.PlanSHA256, Source: initial, Session: message}
	if err != nil || closeErr != nil || !result.valid(claim) || message.ManagerSHA256 != managerSHA || controller(ctx) != nil || source(ctx) != nil || cleanup() != nil || ctx.Err() != nil {
		return errClusterConflict
	}
	join() // No renewal may race successful settlement into cancellation.
	if controller(ctx) != nil || w.store.completeClusterExistingIdentityInspection(ctx, claim, result) != nil || ctx.Err() != nil {
		return errClusterConflict
	}
	return nil
}

func (s *postgresOperatorStore) pendingClusterExistingIdentityInspections(ctx context.Context) ([]clusterSSHPendingIdentityRecovery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id,t.id FROM engine.cluster_operations o JOIN engine.cluster_state c ON c.id=1 AND c.active_operation_id=o.id
 JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id AND t.cluster_id=c.cluster_id AND t.operation_attempt=o.attempt
 JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
 WHERE o.kind=$1 AND o.state='waiting' AND o.current_step=$2 AND o.attempt=1 AND t.current_step=$3 AND t.state IN ('queued','running')
 AND t.credential_state='available' AND t.lease_expires_at<=extract(epoch FROM clock_timestamp()) AND p.expires_at>extract(epoch FROM clock_timestamp())
 ORDER BY o.created_at,t.id LIMIT 1`, clusterExistingIdentityKind, clusterExistingIdentityConfirmed, clusterExistingIdentityInspect)
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

func startClusterExistingIdentityInspectionRuntime(parent context.Context, auth *authService) func() {
	if auth == nil {
		return func() {}
	}
	store, ok := auth.store.(*postgresOperatorStore)
	aegis, aegisOK := auth.aegis.(*goAegisService)
	broker, err := newClusterExistingIdentityCustodyClient()
	if !ok || store == nil || !aegisOK || aegis == nil || err != nil {
		return func() {}
	}
	w := &clusterExistingIdentityInspectionWorker{store: store, aegis: aegis, custody: broker.observe, download: prepareClusterSSHBootstrap, heartbeat: 10 * time.Second,
		proof: func(b *clusterbootstrap.Bundle) (clusterbootstrap.Expected, string, error) {
			e, digest, _, err := b.ManagerProof()
			return e, digest, err
		},
		cleanup: func(b *clusterbootstrap.Bundle) error { return b.Close() },
		connect: func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterExistingIdentityInspectionClient, error) {
			return (clusterremote.Transport{}).Connect(ctx, target, key, credential)
		},
	}
	runOnce := func(ctx context.Context) {
		key, err := aegis.activeKey()
		clear(key)
		if err != nil {
			return
		}
		lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
		pending, err := store.pendingClusterExistingIdentityInspections(lookup)
		cancel()
		if err != nil || len(pending) == 0 {
			return
		}
		claimCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		p := pending[0]
		work, err := readClusterExistingIdentityWork(claimCtx, store.db, p.OperationID, p.TargetID)
		if err != nil {
			return
		}
		process, err := clusterExistingIdentityLocalSource(work.claim.Plan)
		if err != nil {
			return
		}
		claim, err := store.claimClusterExistingIdentityInspection(claimCtx, p.OperationID, p.TargetID, newClusterUUID(), process)
		if err != nil {
			return
		}
		stop()
		_ = w.inspect(ctx, claim, process)
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
