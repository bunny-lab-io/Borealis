package main

import (
	"context"
	"errors"

	"github.com/lib/pq"
)

var errSchedulerWorkerInventory = errors.New("site-worker inventory is ambiguous or Agent rotation is incomplete")

// Engine.sh deliberately overlaps two Pods with the same site/GUID while the
// scheduler is paused. Its failure trap can resume the scheduler before cleanup
// finishes. Never choose a winner or retire by shared GUID in that state. A
// completed replacement keeps the GUID, changes Pod name and removes this label.
func validateSchedulerWorkerInventory(snapshots []map[string]any) error {
	sites := make(map[int64]bool, len(snapshots))
	guids := make(map[string]bool, len(snapshots))
	for _, snapshot := range snapshots {
		guid, site := cleanText(snapshot["worker_guid"]), coerceInt64(snapshot["site_id"])
		labels, labelsKnown := snapshot["labels"].(map[string]any)
		_, candidate := labels["borealis.io/redeploy-agent-candidate"]
		if guid == "" || site <= 0 || sites[site] || guids[guid] || !labelsKnown || labels == nil || candidate || cleanText(snapshot["deletion_timestamp"]) != "" {
			return errSchedulerWorkerInventory
		}
		sites[site], guids[guid] = true, true
	}
	return nil
}

// DELETE acknowledgement is not native absence. A failed or still-present
// retirement retains SQL ownership; the next periodic inventory can reconcile
// an independently observed disappearance without launching through uncertainty.
func retireSchedulerSiteWorker(ctx context.Context, client *borealisOperatorClient, siteID int64, guid, reason string) error {
	if _, err := client.retireSiteWorker(ctx, guid, reason); err != nil {
		return err
	}
	snapshots, err := client.listSiteWorkers(ctx)
	if err != nil {
		return err
	}
	if err := validateSchedulerWorkerInventory(snapshots); err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		if cleanText(snapshot["worker_guid"]) == guid || coerceInt64(snapshot["site_id"]) == siteID {
			return errSchedulerWorkerInventory
		}
	}
	return nil
}

// A completed Pod deletion can leave its Service after a lost response. Use
// original active SQL identities to retry the existing retirement protocol before
// releasing those rows. Missing Pod inventory alone is not Service cleanup proof.
func (m *goSchedulerManager) retireMissingSchedulerWorkers(ctx context.Context, client *borealisOperatorClient, live []string, liveSites map[int64]string) error {
	conn, err := m.store.db.Conn(ctx)
	if err != nil {
		return errors.Join(errOperatorStoreDown, err)
	}
	rows, err := conn.QueryContext(ctx, `SELECT worker_guid,site_id
		FROM engine.job_scheduler_workers
		WHERE site_id>0 AND status IN ($1,$2,$3) AND NOT (worker_guid=ANY($4))
		ORDER BY site_id,worker_guid`, schedulerWorkerStatusStarting, schedulerWorkerStatusRunning, schedulerWorkerStatusIdle, pq.Array(live))
	if err != nil {
		conn.Close()
		return err
	}
	type missingWorker struct {
		guid string
		site int64
	}
	var missing []missingWorker
	for rows.Next() {
		var worker missingWorker
		if err := rows.Scan(&worker.guid, &worker.site); err != nil {
			rows.Close()
			conn.Close()
			return err
		}
		missing = append(missing, worker)
	}
	err = rows.Err()
	closeErr := rows.Close()
	connErr := conn.Close()
	if err != nil || closeErr != nil || connErr != nil {
		return errors.Join(err, closeErr, connErr)
	}
	for _, worker := range missing {
		if worker.guid == "" || liveSites[worker.site] != "" {
			return errSchedulerWorkerInventory
		}
	}
	for _, worker := range missing {
		if err := retireSchedulerSiteWorker(ctx, client, worker.site, worker.guid, "missing_pod_cleanup"); err != nil {
			return err
		}
	}
	return nil
}

// Unknown Kubernetes inventory or retirement cannot release durable worker
// ownership or authorize a replacement. Retain this state until the existing
// periodic inventory succeeds; unrelated scheduler dispatch keeps its own
// leadership/work fences and is not gated by worker reconciliation failure.
func (m *goSchedulerManager) reconcileSiteWorkerLifecycle(ctx context.Context, refresh, observed bool, historySeconds int) (bool, error) {
	if refresh {
		if err := m.reconcileSiteWorkers(ctx); err != nil {
			return false, err
		}
		observed = true
	}
	if !observed {
		return false, nil
	}
	if err := m.markLostWorkers(ctx); err != nil {
		return false, err
	}
	if err := m.pruneWorkerHistory(ctx, historySeconds); err != nil {
		return false, err
	}
	siteIDs, err := m.siteIDsNeedingWorkers(ctx)
	if err != nil {
		return false, err
	}
	for _, siteID := range siteIDs {
		if err := m.spawnSiteWorker(ctx, siteID); err != nil {
			return false, err
		}
	}
	return true, nil
}
