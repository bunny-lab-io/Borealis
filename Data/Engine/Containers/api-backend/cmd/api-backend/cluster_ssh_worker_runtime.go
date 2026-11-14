package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
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
	store       *postgresOperatorStore
	aegis       *goAegisService
	worker      *clusterSSHInspectionWorker
	preparation *clusterSSHPreparationRuntime
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
	if err := r.qualifyOnce(ctx); err != nil {
		return err
	}
	return r.preparation.runOnce(ctx)
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
	r.preparation = newClusterSSHPreparationRuntime(store, aegis)
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

type clusterSSHPendingPreparation struct {
	Operation  clusterControllerOperation
	Controller string
}

// Only the selected original-source API may consume the entire approved cohort.
// No uncertain claim is eligible again, even after expiry or process restart.
func (s *postgresOperatorStore) pendingClusterSSHPreparation(ctx context.Context, sourceNode string) (clusterSSHPendingPreparation, error) {
	var pending clusterSSHPendingPreparation
	if s == nil || s.db == nil || len(validateClusterNodeName("node", sourceNode)) != 0 || ctx.Err() != nil {
		return pending, errClusterConflict
	}
	err := s.db.QueryRowContext(ctx, `SELECT o.id,o.kind,o.state,o.current_step,o.attempt,l.holder
 FROM engine.cluster_operations o,engine.cluster_state c,engine.cluster_application_leases l,engine.cluster_nodes n
 WHERE o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$1 AND COALESCE(o.error_text,'')=''
 AND c.id=1 AND c.active_operation_id=o.id AND l.name=$2 AND l.holder<>'' AND l.expires_at>extract(epoch FROM clock_timestamp())
 AND o.payload_json::jsonb->'ssh_preparation_authorization'->>'controller_holder'=l.holder
 AND n.id=o.payload_json::jsonb->'ssh_preparation_confirmation'->'identity_source'->>'source_node_id'
 AND n.node_name=$3 AND n.membership_state='Active'
 AND EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets t WHERE t.operation_id=o.id)
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets t WHERE t.operation_id=o.id AND
 (t.state='queued' AND t.current_step='qualification_complete' AND t.lease_holder='' AND t.lease_expires_at=0) IS NOT TRUE)
 LIMIT 1`, clusterSSHPreparationOperationStep, clusterControllerLeaseName, sourceNode).Scan(&pending.Operation.ID,
		&pending.Operation.Kind, &pending.Operation.State, &pending.Operation.CurrentStep, &pending.Operation.Attempt, &pending.Controller)
	if errors.Is(err, sql.ErrNoRows) {
		return clusterSSHPendingPreparation{}, nil
	}
	if err != nil {
		return clusterSSHPendingPreparation{}, errClusterUnavailable
	}
	return pending, nil
}

type clusterSSHPreparationRuntime struct {
	ready   func() bool
	pending func(context.Context) (clusterSSHPendingPreparation, error)
	claim   func(context.Context, clusterControllerOperation, string, string) (clusterSSHPreparationWork, error)
	execute func(context.Context, clusterSSHPreparationWork) error
}

func (r *clusterSSHPreparationRuntime) runOnce(ctx context.Context) error {
	if r == nil || r.ready == nil || r.pending == nil || r.claim == nil || r.execute == nil || ctx.Err() != nil || !r.ready() {
		return nil
	}
	lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
	pending, err := r.pending(lookup)
	cancel()
	if err != nil || pending.Operation.ID == "" {
		return err
	}
	claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	work, err := r.claim(claimCtx, pending.Operation, pending.Controller, newClusterUUID())
	cancel()
	if err != nil {
		return err
	}
	return r.execute(ctx, work)
}

func newClusterSSHPreparationRuntime(store *postgresOperatorStore, aegis *goAegisService) *clusterSSHPreparationRuntime {
	client, err := newClusterSSHSourceBrokerClientFromEnv()
	if store == nil || store.db == nil || aegis == nil || err != nil {
		return nil
	}
	process := clusterExistingIdentitySourceProcess{PodName: os.Getenv("BOREALIS_ENGINE_POD_NAME"),
		PodUID: os.Getenv("BOREALIS_ENGINE_POD_UID"), NodeName: os.Getenv("BOREALIS_ENGINE_NODE_NAME")}
	worker := newClusterSSHNativePrejoinPreparationWorkerRead(store, aegis, os.TempDir(), process, client.joinedTargetsRead)
	if worker == nil {
		return nil
	}
	return &clusterSSHPreparationRuntime{
		ready: func() bool { key, err := aegis.activeKey(); clear(key); return err == nil },
		pending: func(ctx context.Context) (clusterSSHPendingPreparation, error) {
			return store.pendingClusterSSHPreparation(ctx, process.NodeName)
		},
		claim:   store.claimClusterSSHPreparation,
		execute: worker.prepare,
	}
}
