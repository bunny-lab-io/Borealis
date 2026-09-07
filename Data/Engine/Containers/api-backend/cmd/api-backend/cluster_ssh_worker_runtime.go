package main

import (
	"context"
	"sync"
	"time"
)

const clusterSSHInspectionOperationStep = "inspect_ssh_targets"

type clusterSSHPendingInspection struct{ OperationID, TargetID string }

// This process supplies Aegis/SSH execution, never membership ownership. Only
// an explicit step of the sole active controller operation can expose targets.
func (s *postgresOperatorStore) pendingClusterSSHInspections(ctx context.Context) ([]clusterSSHPendingInspection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.operation_id,t.id
		FROM engine.cluster_onboarding_targets t
		JOIN engine.cluster_operations o ON o.id=t.operation_id AND o.state='running' AND o.current_step=$1 AND o.attempt=t.operation_attempt
		JOIN engine.cluster_state c ON c.active_operation_id=o.id AND c.cluster_id=t.cluster_id
		JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
		JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
		JOIN engine.cluster_application_leases l ON l.name=$2 AND l.holder<>''
		WHERE o.kind='ssh_onboarding' AND t.current_step='inspect' AND t.state IN ('queued','running','recovery_required')
		  AND t.credential_state='available' AND t.lease_expires_at<=extract(epoch FROM clock_timestamp())
		  AND p.expires_at>extract(epoch FROM clock_timestamp()) AND l.expires_at>extract(epoch FROM clock_timestamp())
		ORDER BY t.ordinal,t.id LIMIT 2`, clusterSSHInspectionOperationStep, clusterControllerLeaseName)
	if err != nil {
		return nil, errClusterUnavailable
	}
	defer rows.Close()
	var pending []clusterSSHPendingInspection
	for rows.Next() {
		var item clusterSSHPendingInspection
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

type clusterSSHInspectionRuntime struct {
	store  *postgresOperatorStore
	aegis  *goAegisService
	worker *clusterSSHInspectionWorker
}

func (r *clusterSSHInspectionRuntime) runOnce(ctx context.Context) error {
	key, err := r.aegis.activeKey()
	if err != nil {
		return nil
	}
	clear(key)
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	pending, err := r.store.pendingClusterSSHInspections(lookupCtx)
	cancel()
	if err != nil {
		return err
	}
	// pending query/rows are closed before any worker does crypto or SSH.
	var joined sync.WaitGroup
	for _, item := range pending {
		joined.Add(1)
		go func() {
			defer joined.Done()
			claimCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			lease, err := r.store.claimClusterSSHTarget(claimCtx, item.OperationID, item.TargetID, newClusterUUID())
			cancel()
			if err != nil || lease.Step != "inspect" || lease.OperationStep != clusterSSHInspectionOperationStep || lease.OperationKind != "ssh_onboarding" {
				return
			}
			// Failures retain lease/outcome until normal expiry/reconciliation. This
			// step is read-only, so a later claim may inspect again; no write replay.
			_ = r.worker.inspect(ctx, lease)
		}()
	}
	joined.Wait()
	return nil
}

func startClusterSSHInspectionRuntime(parent context.Context, auth *authService) func() {
	if auth == nil {
		return func() {}
	}
	store, storeOK := auth.store.(*postgresOperatorStore)
	aegis, aegisOK := auth.aegis.(*goAegisService)
	if !storeOK || store == nil || !aegisOK || aegis == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	r := &clusterSSHInspectionRuntime{store: store, aegis: aegis, worker: newClusterSSHInspectionWorker(store, aegis)}
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			_ = r.runOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
