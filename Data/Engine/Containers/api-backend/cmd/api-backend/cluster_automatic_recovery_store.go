package main

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"time"
)

type clusterAutomaticRecoveryReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type clusterAutomaticRecoverySnapshot struct {
	ClusterID string
	Nodes     []clusterControllerNode
}

func readClusterAutomaticRecoverySnapshot(ctx context.Context, db clusterAutomaticRecoveryReader, holder string) (clusterAutomaticRecoverySnapshot, int64, error) {
	var s clusterAutomaticRecoverySnapshot
	var now int64
	err := db.QueryRowContext(ctx, `SELECT c.cluster_id,floor(extract(epoch FROM clock_timestamp()))::bigint
 FROM engine.cluster_state c JOIN engine.cluster_application_leases l ON l.name=$1
 WHERE c.id=1 AND c.enabled=1 AND c.active_size=3 AND c.desired_size=3 AND c.hmr_state='inactive'
 AND c.status IN ('Healthy','Degraded Quorum','Degraded Database') AND COALESCE(c.active_operation_id,'')=''
 AND l.holder=$2 AND l.expires_at>floor(extract(epoch FROM clock_timestamp()))::bigint`, clusterControllerLeaseName, holder).Scan(&s.ClusterID, &now)
	if err != nil || !clusterUUIDRE.MatchString(s.ClusterID) {
		return s, 0, errClusterConflict
	}
	rows, err := db.QueryContext(ctx, `SELECT id,node_name,application_state,COALESCE(drain_reason,'')
 FROM engine.cluster_nodes WHERE membership_state='Active' ORDER BY id`)
	if err != nil {
		return s, 0, errClusterConflict
	}
	defer rows.Close()
	for rows.Next() {
		var n clusterControllerNode
		if rows.Scan(&n.ID, &n.Name, &n.ApplicationState, &n.DrainReason) != nil || !clusterUUIDRE.MatchString(n.ID) ||
			!clusterControllerNodeRegex.MatchString(n.Name) || n.ApplicationState != "active" || n.DrainReason != "" {
			return s, 0, errClusterConflict
		}
		s.Nodes = append(s.Nodes, n)
	}
	if rows.Err() != nil || len(s.Nodes) != 3 {
		return s, 0, errClusterConflict
	}
	return s, now, nil
}

func lockClusterAutomaticRecovery(ctx context.Context, tx *sql.Tx, operationID string) error {
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 FOR UPDATE`, clusterControllerLeaseName).Scan(&present) != nil ||
		tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_state WHERE id=1 FOR UPDATE`).Scan(&present) != nil {
		return errClusterConflict
	}
	if operationID != "" && tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations WHERE id=$1 FOR UPDATE`, operationID).Scan(&present) != nil {
		return errClusterConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM engine.cluster_nodes WHERE membership_state='Active' ORDER BY id FOR UPDATE`)
	if err != nil {
		return errClusterConflict
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			return errClusterConflict
		}
	}
	return rows.Err()
}

func checkClusterAutomaticRecoveryLease(ctx context.Context, tx *sql.Tx, holder string) error {
	var present int
	if ctx.Err() != nil || tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases
 WHERE name=$1 AND holder=$2 AND expires_at>floor(extract(epoch FROM clock_timestamp()))::bigint`, clusterControllerLeaseName, holder).Scan(&present) != nil {
		return errClusterControllerLeaseLost
	}
	return nil
}

func (c *clusterAutomaticRecoveryCoordinator) reserve(ctx context.Context, nodeID string, observe clusterAutomaticRecoveryObserve) (clusterAutomaticRecoveryRecord, error) {
	fail := func() (clusterAutomaticRecoveryRecord, error) {
		return clusterAutomaticRecoveryRecord{}, errClusterConflict
	}
	db := c.controller.store.db
	snapshot, detectedAt, err := readClusterAutomaticRecoverySnapshot(ctx, db, c.controller.holder)
	if err != nil {
		return fail()
	}
	node := clusterNodeByID(snapshot.Nodes, nodeID)
	if node.ID == "" {
		return fail()
	}
	// The pool is free throughout observation. This boundary grants no host action.
	health, err := observe(ctx, node)
	if err != nil || health.NodeName != node.Name || !clusterUUIDRE.MatchString(health.NodeUID) {
		return fail()
	}
	if health.healthy() {
		return clusterAutomaticRecoveryRecord{}, errClusterAutomaticRecoveryHealthy
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	if lockClusterAutomaticRecovery(ctx, tx, "") != nil {
		return fail()
	}
	current, now, err := readClusterAutomaticRecoverySnapshot(ctx, tx, c.controller.holder)
	if err != nil || !reflect.DeepEqual(snapshot, current) || now < detectedAt {
		return fail()
	}
	var pending int
	if tx.QueryRowContext(ctx, `SELECT count(*) FROM engine.cluster_operations
 WHERE kind='node_maintenance' AND state NOT IN ('cancelled','succeeded') AND payload_json::jsonb ? $1`, clusterAutomaticRecoveryField).Scan(&pending) != nil || pending != 0 {
		return fail()
	}
	r := clusterAutomaticRecoveryRecord{
		Version: 1, Origin: "automatic", ClusterID: snapshot.ClusterID, OperationID: newClusterUUID(), NodeID: node.ID, NodeName: node.Name, NodeUID: health.NodeUID,
		OriginController: c.controller.holder, Controller: c.controller.holder, Session: c.session, Attempt: 1, Sequence: 1,
		DetectedAt: detectedAt, RecoveryDeadline: detectedAt + int64(clusterAutomaticRecoveryTime/time.Second), LastObservedAt: now,
		Phase: "recovering", Reason: "required_health_unavailable",
	}
	if !r.valid() || now > r.RecoveryDeadline {
		return fail()
	}
	payload := map[string]any{"action": "enter", "reason": clusterAutomaticRecoveryField, clusterAutomaticRecoveryField: r}
	if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operations(id,kind,state,current_step,target_node_id,requested_by,payload_json,attempt,created_at,updated_at)
 VALUES($1,'node_maintenance','waiting',$2,$3,$4,$5,$6,$7,$7)`, r.OperationID, clusterAutomaticRecoveryStep, r.NodeID, r.OriginController, marshalClusterJSON(payload), r.Attempt, now); err != nil {
		return fail()
	}
	if err := insertClusterEvent(ctx, tx, r.OperationID, "", r.ClusterID, "automatic_recovery_observation_started", "waiting", "Automatic recovery observation recorded; maintenance dispatch remains gated.", automaticRecoveryEventDetails(r), now); err != nil {
		return fail()
	}
	if checkClusterAutomaticRecoveryLease(ctx, tx, c.controller.holder) != nil || tx.Commit() != nil {
		return fail()
	}
	return r, nil
}

type clusterAutomaticRecoveryRow struct {
	Record  clusterAutomaticRecoveryRecord
	Payload string
}

func readClusterAutomaticRecoveryRow(ctx context.Context, db clusterAutomaticRecoveryReader, operationID string) (clusterAutomaticRecoveryRow, error) {
	var row clusterAutomaticRecoveryRow
	var target string
	var attempt int64
	err := db.QueryRowContext(ctx, `SELECT target_node_id,attempt,payload_json FROM engine.cluster_operations
 WHERE id=$1 AND kind='node_maintenance' AND state='waiting' AND current_step=$2`, operationID, clusterAutomaticRecoveryStep).Scan(&target, &attempt, &row.Payload)
	if err != nil {
		return row, errClusterConflict
	}
	payload := parseClusterJSON(row.Payload)
	r, err := parseClusterAutomaticRecovery(payload)
	if err != nil || r.OperationID != operationID || r.NodeID != target || r.Attempt != attempt || r.Phase == "blocked" ||
		cleanText(payload["action"]) != "enter" || cleanText(payload["reason"]) != clusterAutomaticRecoveryField {
		return row, errClusterConflict
	}
	row.Record = r
	return row, nil
}

func (c *clusterAutomaticRecoveryCoordinator) observe(ctx context.Context, operationID string, observe clusterAutomaticRecoveryObserve) (clusterAutomaticRecoveryRecord, error) {
	fail := func() (clusterAutomaticRecoveryRecord, error) {
		return clusterAutomaticRecoveryRecord{}, errClusterConflict
	}
	db := c.controller.store.db
	before, err := readClusterAutomaticRecoveryRow(ctx, db, operationID)
	if err != nil {
		return fail()
	}
	snapshot, _, err := readClusterAutomaticRecoverySnapshot(ctx, db, c.controller.holder)
	node := clusterNodeByID(snapshot.Nodes, before.Record.NodeID)
	if err != nil || snapshot.ClusterID != before.Record.ClusterID || node.Name != before.Record.NodeName {
		return fail()
	}
	health, observationErr := observe(ctx, node)
	if ctx.Err() != nil {
		return fail()
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	if lockClusterAutomaticRecovery(ctx, tx, operationID) != nil {
		return fail()
	}
	current, now, err := readClusterAutomaticRecoverySnapshot(ctx, tx, c.controller.holder)
	if err != nil || !reflect.DeepEqual(snapshot, current) {
		return fail()
	}
	row, err := readClusterAutomaticRecoveryRow(ctx, tx, operationID)
	if err != nil || row != before {
		return fail()
	}
	next, err := advanceClusterAutomaticRecovery(row.Record, c.controller.holder, c.session, now, health, observationErr)
	if err != nil || !next.valid() {
		return fail()
	}
	payload := parseClusterJSON(row.Payload)
	payload[clusterAutomaticRecoveryField] = next
	state := "waiting"
	if next.Phase == "blocked" {
		state = "failed"
	}
	// Python bootstrap uses INTEGER timestamps; Go bootstrap uses BIGINT.
	// Type both occurrences explicitly so PostgreSQL infers one parameter type.
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state=$2,payload_json=$3,attempt=$4,updated_at=$5::bigint,
 finished_at=CASE WHEN $2='failed' THEN $5::bigint ELSE NULL END WHERE id=$1`, operationID, state, marshalClusterJSON(payload), next.Attempt, now); err != nil {
		return fail()
	}
	if next.Phase != row.Record.Phase || next.Reason != row.Record.Reason || next.HealthySince != row.Record.HealthySince || next.Attempt != row.Record.Attempt {
		if err := insertClusterEvent(ctx, tx, operationID, "", next.ClusterID, "automatic_recovery_observation_changed", state, "Automatic recovery observation changed; runtime authority is unchanged.", automaticRecoveryEventDetails(next), now); err != nil {
			return fail()
		}
	}
	if checkClusterAutomaticRecoveryLease(ctx, tx, c.controller.holder) != nil || tx.Commit() != nil {
		return fail()
	}
	return next, nil
}

func automaticRecoveryEventDetails(r clusterAutomaticRecoveryRecord) map[string]any {
	return map[string]any{"node_id": r.NodeID, "phase": r.Phase, "reason": r.Reason, "attempt": r.Attempt,
		"sequence": r.Sequence, "recovery_deadline": r.RecoveryDeadline, "healthy_since": r.HealthySince,
		"healthy_required_seconds": int64(clusterAutomaticHealthyTime / time.Second), "runtime_dispatched": false}
}

// An automatic observation cannot be converted into a mutating retry through
// the generic operator endpoint. Cancelling an untouched waiting observation is
// safe: it never reserved the cluster, changed a drain or dispatched work.
func rejectClusterAutomaticRecoveryRetry(kind, payloadJSON string) error {
	if kind == "node_maintenance" {
		if _, exists := parseClusterJSON(payloadJSON)[clusterAutomaticRecoveryField]; exists {
			return errors.New("automatic recovery observation cannot authorize a maintenance retry")
		}
	}
	return nil
}
