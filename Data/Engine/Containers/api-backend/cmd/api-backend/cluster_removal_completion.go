package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type clusterRemovalIdentity struct {
	id   string
	name string
}

type clusterRemovalCompletion struct {
	targets []clusterControllerNode
	census  []clusterRemovalIdentity
	payload string
}

// Shape the original observation before borrowing a SQL connection. Role,
// application and probe observations may change without replacing a member.
func prepareClusterRemovalCompletion(operation clusterControllerOperation, nodes []clusterControllerNode) (clusterRemovalCompletion, error) {
	var completion clusterRemovalCompletion
	if operation.CurrentStep != "verify_quorum" || operation.Attempt < 1 {
		return completion, fmt.Errorf("%w: removal is not at final quorum verification", errClusterConflict)
	}
	targets, err := clusterRemovalNodes(operation, nodes)
	if err != nil {
		return completion, err
	}
	if !currentReleaseClusterRemovalSupported(int64(len(nodes))) || coerceInt64(operation.Payload["target_size"]) != int64(len(nodes)-len(targets)) {
		return completion, fmt.Errorf("%w: removal size does not match original active census", errClusterConflict)
	}
	payload, err := json.Marshal(operation.Payload)
	if err != nil {
		return completion, err
	}
	completion.targets, completion.payload = targets, string(payload)
	for _, node := range nodes {
		completion.census = append(completion.census, clusterRemovalIdentity{id: node.ID, name: node.Name})
	}
	sort.Slice(completion.census, func(i, j int) bool { return completion.census[i].id < completion.census[j].id })
	return completion, nil
}

// Caller holds controller lease and cluster_state locks, in that order. Lock
// the operation and whole Active identity census before any settlement writes.
// The cluster lock serializes membership changes through the existing store.
func (c *clusterController) requireRemovalCompletion(ctx context.Context, tx *sql.Tx, operation clusterControllerOperation, completion clusterRemovalCompletion, activeSize int64) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT o.id FROM engine.cluster_operations o
		JOIN engine.cluster_state s ON s.id=1 AND s.active_operation_id=o.id
		WHERE o.id=$1 AND o.kind='node_remove' AND o.state='running'
		AND o.current_step='verify_quorum' AND o.attempt=$2
		AND o.target_node_id=$3 AND COALESCE(o.target_release,'')=$4 AND COALESCE(o.target_sha,'')=$5
		AND o.payload_json::jsonb=$6::jsonb FOR UPDATE OF o`, operation.ID, operation.Attempt, operation.TargetNodeID, operation.TargetRelease, operation.TargetSHA, completion.payload).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: removal operation reservation changed", errClusterConflict)
	}
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,node_name FROM engine.cluster_nodes WHERE membership_state='Active' ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var identity clusterRemovalIdentity
		if err := rows.Scan(&identity.id, &identity.name); err != nil {
			return err
		}
		if count >= len(completion.census) || identity != completion.census[count] {
			return fmt.Errorf("%w: removal active member identity changed", errClusterConflict)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if count != len(completion.census) || activeSize != int64(count) {
		return fmt.Errorf("%w: removal active census changed", errClusterConflict)
	}
	// Initial ownership was checked before waiting for cluster/operation/member
	// locks. Recheck current time only after every wait, before the first write.
	return c.requireLeaseOwnership(ctx, tx)
}
