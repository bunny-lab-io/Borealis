package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"os"
	"time"
)

type clusterSSHPendingIdentityRecovery struct{ OperationID, TargetID string }

// Only the controller-reserved recovery phase is executable. Normal
// inspection/preparation/qualification targets can never enter this queue.
func (s *postgresOperatorStore) pendingClusterSSHIdentityRecoveries(ctx context.Context) ([]clusterSSHPendingIdentityRecovery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.operation_id,t.id
		FROM engine.cluster_onboarding_targets t
		JOIN engine.cluster_operations o ON o.id=t.operation_id AND o.state='running' AND o.current_step=$1 AND o.attempt=t.operation_attempt
		JOIN engine.cluster_state c ON c.active_operation_id=o.id AND c.cluster_id=t.cluster_id
		JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
		JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
		JOIN engine.cluster_application_leases l ON l.name=$2 AND l.holder<>''
		WHERE o.kind='ssh_onboarding' AND t.current_step=$3 AND t.state IN ('prepared','running','recovery_required')
		  AND t.credential_state='available' AND t.lease_expires_at<=extract(epoch FROM clock_timestamp())
		  AND p.expires_at>extract(epoch FROM clock_timestamp()) AND l.expires_at>extract(epoch FROM clock_timestamp())
		ORDER BY t.ordinal,t.id LIMIT 1`, clusterSSHIdentityRecoveryOperationStep, clusterControllerLeaseName, clusterSSHIdentityRecoveryTargetStep)
	if err != nil {
		return nil, errClusterUnavailable
	}
	defer rows.Close()
	var pending []clusterSSHPendingIdentityRecovery
	for rows.Next() {
		var item clusterSSHPendingIdentityRecovery
		if rows.Scan(&item.OperationID, &item.TargetID) != nil {
			return nil, errClusterUnavailable
		}
		pending = append(pending, item)
	}
	if rows.Err() != nil {
		return nil, errClusterUnavailable
	}
	return pending, nil
}

type clusterSSHIdentityRecoveryRuntime struct {
	ready   func() bool
	pending func(context.Context) ([]clusterSSHPendingIdentityRecovery, error)
	claim   func(context.Context, string, string, string) (clusterSSHIdentityRecoveryClaim, error)
	execute func(context.Context, clusterSSHIdentityRecoveryClaim) error
}

func (r *clusterSSHIdentityRecoveryRuntime) runOnce(ctx context.Context) error {
	if r == nil || r.ready == nil || r.pending == nil || r.claim == nil || r.execute == nil || !r.ready() || ctx.Err() != nil {
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	pending, err := r.pending(lookupCtx)
	cancel()
	if err != nil || len(pending) == 0 {
		return err
	}
	claimCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	claim, err := r.claim(claimCtx, pending[0].OperationID, pending[0].TargetID, newClusterUUID())
	cancel()
	if err != nil {
		return nil
	}
	if !claim.valid() {
		return errClusterSSHIdentityRecoveryWorker
	}
	return r.execute(ctx, claim)
}

type clusterSSHIdentityRecoveryDownload func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error)

// Bundle acquisition may outlive one 45-second target lease. Keep exact SQL,
// controller and Aegis authority alive while immutable release bytes arrive;
// cancellation joins the heartbeat before scratch can escape.
func acquireClusterSSHIdentityRecoveryBundle(parent context.Context, store clusterSSHIdentityRecoveryWorkerStore, aegis clusterSSHWorkerAegis,
	claim clusterSSHIdentityRecoveryClaim, authority clusterSSHPreparationAuthorityRead, scratchParent string,
	download clusterSSHIdentityRecoveryDownload) (*clusterbootstrap.Bundle, error) {
	return acquireClusterSSHIdentityRecoveryBundleWithInterval(parent, store, aegis, claim, authority, scratchParent, download, 10*time.Second)
}

func acquireClusterSSHIdentityRecoveryBundleWithInterval(parent context.Context, store clusterSSHIdentityRecoveryWorkerStore, aegis clusterSSHWorkerAegis,
	claim clusterSSHIdentityRecoveryClaim, authority clusterSSHPreparationAuthorityRead, scratchParent string,
	download clusterSSHIdentityRecoveryDownload, heartbeatInterval time.Duration) (*clusterbootstrap.Bundle, error) {
	fail := func(bundle *clusterbootstrap.Bundle) (*clusterbootstrap.Bundle, error) {
		if bundle != nil {
			_ = bundle.Close()
		}
		return nil, errClusterSSHIdentityRecoveryWorker
	}
	if store == nil || aegis == nil || !claim.valid() || authority == nil || download == nil || heartbeatInterval <= 0 || parent.Err() != nil {
		return fail(nil)
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(context.Canceled)
	check := func(checkCtx context.Context) error {
		bound, stop := context.WithTimeout(checkCtx, 3*time.Second)
		defer stop()
		if aegis.verifyClusterSSHGeneration(bound, claim.Sealed.generation) != nil ||
			store.renewClusterSSHIdentityRecoveryTarget(bound, claim) != nil {
			return errClusterSSHIdentityRecoveryWorker
		}
		value, err := authority(bound)
		if err != nil || !clusterSSHIdentityRecoveryAuthorityMatches(value, claim) || bound.Err() != nil {
			return errClusterSSHIdentityRecoveryWorker
		}
		return nil
	}
	if check(ctx) != nil {
		return fail(nil)
	}
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopHeartbeat:
				return
			case <-ticker.C:
				if check(ctx) != nil {
					cancel(errClusterSSHIdentityRecoveryWorker)
					return
				}
			}
		}
	}()
	bundle, err := download(ctx, claim.Baseline, scratchParent)
	close(stopHeartbeat)
	<-heartbeatDone
	if err != nil || context.Cause(ctx) != nil || parent.Err() != nil || check(ctx) != nil {
		return fail(bundle)
	}
	return bundle, nil
}

func startClusterSSHIdentityRecoveryRuntime(parent context.Context, auth *authService) func() {
	if auth == nil {
		return func() {}
	}
	store, storeOK := auth.store.(*postgresOperatorStore)
	aegis, aegisOK := auth.aegis.(*goAegisService)
	client, clientErr := newClusterSSHSourceBrokerClientFromEnv()
	if !storeOK || store == nil || !aegisOK || aegis == nil || clientErr != nil {
		return func() {}
	}
	worker := newClusterSSHIdentityRecoveryWorker(store, aegis)
	runtime := &clusterSSHIdentityRecoveryRuntime{
		ready: func() bool {
			key, err := aegis.activeKey()
			clear(key)
			return err == nil
		},
		pending: store.pendingClusterSSHIdentityRecoveries,
		claim:   store.claimClusterSSHIdentityRecoveryTarget,
		execute: func(ctx context.Context, claim clusterSSHIdentityRecoveryClaim) error {
			authority := newClusterSSHIdentityRecoveryAuthorityRead(store, aegis, claim)
			node := client.recoveryNodeRead(authority, claim)
			bundle, err := acquireClusterSSHIdentityRecoveryBundle(ctx, store, aegis, claim, authority, os.TempDir(), prepareClusterSSHBootstrap)
			if err != nil {
				settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				defer cancel()
				_ = store.requireClusterSSHIdentityRecovery(settleCtx, claim)
				return errClusterSSHIdentityRecoveryWorker
			}
			if worker.recover(ctx, claim, bundle, authority, node) != nil {
				return errClusterSSHIdentityRecoveryWorker
			}
			return nil
		},
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			_ = runtime.runOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
