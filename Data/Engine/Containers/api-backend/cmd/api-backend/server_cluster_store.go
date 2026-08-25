package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *postgresOperatorStore) ensureClusterSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errClusterUnavailable
	}
	s.clusterSchemaMu.Lock()
	defer s.clusterSchemaMu.Unlock()
	if s.clusterSchemaOK {
		return nil
	}
	statements := []string{
		`CREATE SCHEMA IF NOT EXISTS engine`,
		`CREATE TABLE IF NOT EXISTS engine.cluster_state (
			id BIGINT PRIMARY KEY,
			cluster_id TEXT UNIQUE NOT NULL,
			enabled BIGINT NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'Standalone',
			active_size BIGINT NOT NULL DEFAULT 1,
			desired_size BIGINT NOT NULL DEFAULT 1,
			control_plane_vip TEXT,
			edge_vip TEXT,
			baseline_release TEXT,
			baseline_sha TEXT,
			hmr_state TEXT NOT NULL DEFAULT 'inactive',
			hmr_node_id TEXT,
			active_operation_id TEXT,
			config_json TEXT NOT NULL DEFAULT '{}',
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS engine.cluster_nodes (
			id TEXT PRIMARY KEY,
			node_name TEXT UNIQUE NOT NULL,
			hostname TEXT NOT NULL,
			management_ip TEXT NOT NULL,
			architecture TEXT NOT NULL,
			os_version TEXT NOT NULL,
			membership_state TEXT NOT NULL DEFAULT 'Pending Quorum',
			application_state TEXT NOT NULL DEFAULT 'standby',
			release_tag TEXT,
			release_sha TEXT,
			drain_reason TEXT,
			roles_json TEXT NOT NULL DEFAULT '{}',
			probe_health_json TEXT NOT NULL DEFAULT '{}',
			last_seen_at BIGINT,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cluster_nodes_membership ON engine.cluster_nodes(membership_state, node_name)`,
		`CREATE TABLE IF NOT EXISTS engine.cluster_invitations (
			id TEXT PRIMARY KEY,
			cluster_id TEXT NOT NULL,
			node_name TEXT NOT NULL,
			token_hash TEXT UNIQUE NOT NULL,
			created_by TEXT NOT NULL,
			expires_at BIGINT NOT NULL,
			consumed_at BIGINT,
			created_at BIGINT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cluster_invitations_expiry ON engine.cluster_invitations(expires_at, consumed_at)`,
		`CREATE TABLE IF NOT EXISTS engine.cluster_admissions (
			id TEXT PRIMARY KEY,
			invitation_id TEXT UNIQUE NOT NULL,
			cluster_id TEXT NOT NULL,
			node_name TEXT NOT NULL,
			hostname TEXT NOT NULL,
			management_ip TEXT NOT NULL,
			architecture TEXT NOT NULL,
			os_version TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'Pending Quorum',
			approved_by TEXT,
			approved_at BIGINT,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cluster_admissions_state ON engine.cluster_admissions(state, created_at)`,
		`CREATE TABLE IF NOT EXISTS engine.cluster_operations (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			state TEXT NOT NULL,
			current_step TEXT NOT NULL,
			target_node_id TEXT,
			target_release TEXT,
			target_sha TEXT,
			requested_by TEXT NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}',
			error_text TEXT,
			attempt BIGINT NOT NULL DEFAULT 1,
			created_at BIGINT NOT NULL,
			started_at BIGINT,
			finished_at BIGINT,
			updated_at BIGINT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cluster_operations_state ON engine.cluster_operations(state, created_at)`,
		`CREATE TABLE IF NOT EXISTS engine.cluster_operation_events (
			id BIGSERIAL PRIMARY KEY,
			operation_id TEXT,
			admission_id TEXT,
			cluster_id TEXT,
			event_type TEXT NOT NULL,
			state TEXT NOT NULL,
			message TEXT NOT NULL,
			details_json TEXT NOT NULL DEFAULT '{}',
			created_at BIGINT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cluster_operation_events_operation ON engine.cluster_operation_events(operation_id, id)`,
		`CREATE TABLE IF NOT EXISTS engine.cluster_audit_events (
			id BIGSERIAL PRIMARY KEY,
			actor TEXT NOT NULL,
			action TEXT NOT NULL,
			target_id TEXT,
			result TEXT NOT NULL,
			details_json TEXT NOT NULL DEFAULT '{}',
			created_at BIGINT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cluster_audit_events_created ON engine.cluster_audit_events(created_at)`,
		`CREATE TABLE IF NOT EXISTS engine.realtime_outbox (
			id BIGSERIAL PRIMARY KEY,
			event_name TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			created_at BIGINT NOT NULL,
			published_at BIGINT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_realtime_outbox_pending ON engine.realtime_outbox(published_at, id)`,
		`CREATE TABLE IF NOT EXISTS engine.cluster_application_leases (
			name TEXT PRIMARY KEY,
			holder TEXT NOT NULL,
			expires_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS engine.cluster_schema_phases (
			release_sha TEXT NOT NULL,
			phase TEXT NOT NULL CHECK(phase IN ('expand','finalize')),
			completed_at BIGINT NOT NULL,
			PRIMARY KEY (release_sha, phase)
		)`,
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errClusterUnavailable, err)
	}
	defer conn.Close()
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	s.clusterSchemaOK = true
	return nil
}

func (s *postgresOperatorStore) clusterSnapshot(ctx context.Context) (map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errClusterUnavailable, err)
	}
	defer conn.Close()
	payload := map[string]any{
		"enabled": false, "status": "Standalone", "active_size": int64(1), "desired_size": int64(1),
		"hmr": map[string]any{"state": "inactive"}, "nodes": []map[string]any{}, "admissions": []map[string]any{}, "operations": []map[string]any{},
	}
	var clusterID, status, controlVIP, edgeVIP, baselineRelease, baselineSHA, hmrState, hmrNodeID, activeOperationID, configJSON string
	var enabled, activeSize, desiredSize, createdAt, updatedAt int64
	err = conn.QueryRowContext(ctx, `
		SELECT cluster_id, enabled, status, active_size, desired_size,
		       COALESCE(control_plane_vip,''), COALESCE(edge_vip,''),
		       COALESCE(baseline_release,''), COALESCE(baseline_sha,''),
		       hmr_state, COALESCE(hmr_node_id,''), COALESCE(active_operation_id,''),
		       config_json, created_at, updated_at
		  FROM engine.cluster_state WHERE id=1
	`).Scan(&clusterID, &enabled, &status, &activeSize, &desiredSize, &controlVIP, &edgeVIP, &baselineRelease, &baselineSHA, &hmrState, &hmrNodeID, &activeOperationID, &configJSON, &createdAt, &updatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		config := parseClusterJSON(configJSON)
		payload["cluster_id"] = clusterID
		payload["enabled"] = enabled != 0
		payload["status"] = status
		payload["active_size"] = activeSize
		payload["desired_size"] = desiredSize
		payload["control_plane_vip"] = nilIfEmpty(controlVIP)
		payload["edge_vip"] = nilIfEmpty(edgeVIP)
		payload["baseline_release"] = nilIfEmpty(baselineRelease)
		payload["baseline_sha"] = nilIfEmpty(baselineSHA)
		payload["active_operation_id"] = nilIfEmpty(activeOperationID)
		payload["hmr"] = map[string]any{"state": hmrState, "node_id": nilIfEmpty(hmrNodeID)}
		payload["config"] = config
		payload["database"] = mapStringAny(config["database_runtime"])
		payload["created_at"] = createdAt
		payload["updated_at"] = updatedAt
	}
	nodes, err := queryClusterRows(ctx, conn, `
		SELECT id, node_name, hostname, management_ip, architecture, os_version,
		       membership_state, application_state, COALESCE(release_tag,''), COALESCE(release_sha,''),
		       COALESCE(drain_reason,''), roles_json, probe_health_json, COALESCE(last_seen_at,0), created_at, updated_at
		  FROM engine.cluster_nodes ORDER BY node_name
	`, func(rows *sql.Rows) (map[string]any, error) {
		var id, nodeName, hostname, managementIP, architecture, osVersion, membershipState, applicationState, releaseTag, releaseSHA, drainReason, rolesJSON, probesJSON string
		var lastSeenAt, rowCreatedAt, rowUpdatedAt int64
		if err := rows.Scan(&id, &nodeName, &hostname, &managementIP, &architecture, &osVersion, &membershipState, &applicationState, &releaseTag, &releaseSHA, &drainReason, &rolesJSON, &probesJSON, &lastSeenAt, &rowCreatedAt, &rowUpdatedAt); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "node_name": nodeName, "hostname": hostname, "management_ip": managementIP, "architecture": architecture, "os_version": osVersion, "membership_state": membershipState, "application_state": applicationState, "release_tag": nilIfEmpty(releaseTag), "release_sha": nilIfEmpty(releaseSHA), "drain_reason": nilIfEmpty(drainReason), "roles": parseClusterJSON(rolesJSON), "probe_health": parseClusterJSON(probesJSON), "last_seen_at": nilIfZero(lastSeenAt), "created_at": rowCreatedAt, "updated_at": rowUpdatedAt}, nil
	})
	if err != nil {
		return nil, err
	}
	payload["nodes"] = nodes
	admissions, err := queryClusterRows(ctx, conn, `
		SELECT id, node_name, hostname, management_ip, architecture, os_version, state, COALESCE(approved_by,''), COALESCE(approved_at,0), created_at, updated_at
		  FROM engine.cluster_admissions WHERE state IN ('Pending Quorum','Approved') ORDER BY created_at
	`, func(rows *sql.Rows) (map[string]any, error) {
		var id, nodeName, hostname, managementIP, architecture, osVersion, state, approvedBy string
		var approvedAt, rowCreatedAt, rowUpdatedAt int64
		if err := rows.Scan(&id, &nodeName, &hostname, &managementIP, &architecture, &osVersion, &state, &approvedBy, &approvedAt, &rowCreatedAt, &rowUpdatedAt); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "node_name": nodeName, "hostname": hostname, "management_ip": managementIP, "architecture": architecture, "os_version": osVersion, "state": state, "approved_by": nilIfEmpty(approvedBy), "approved_at": nilIfZero(approvedAt), "created_at": rowCreatedAt, "updated_at": rowUpdatedAt}, nil
	})
	if err != nil {
		return nil, err
	}
	payload["admissions"] = admissions
	operations, err := queryClusterRows(ctx, conn, `
		SELECT id, kind, state, current_step, COALESCE(target_node_id,''), COALESCE(target_release,''), COALESCE(target_sha,''), requested_by,
		       payload_json, COALESCE(error_text,''), attempt, created_at, COALESCE(started_at,0), COALESCE(finished_at,0), updated_at
		  FROM engine.cluster_operations ORDER BY created_at DESC LIMIT 100
	`, func(rows *sql.Rows) (map[string]any, error) {
		var id, kind, state, step, targetNodeID, targetRelease, targetSHA, requestedBy, payloadJSON, errorText string
		var attempt, rowCreatedAt, startedAt, finishedAt, rowUpdatedAt int64
		if err := rows.Scan(&id, &kind, &state, &step, &targetNodeID, &targetRelease, &targetSHA, &requestedBy, &payloadJSON, &errorText, &attempt, &rowCreatedAt, &startedAt, &finishedAt, &rowUpdatedAt); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "kind": kind, "state": state, "current_step": step, "target_node_id": nilIfEmpty(targetNodeID), "target_release": nilIfEmpty(targetRelease), "target_sha": nilIfEmpty(targetSHA), "requested_by": requestedBy, "payload": parseClusterJSON(payloadJSON), "error": nilIfEmpty(errorText), "attempt": attempt, "created_at": rowCreatedAt, "started_at": nilIfZero(startedAt), "finished_at": nilIfZero(finishedAt), "updated_at": rowUpdatedAt}, nil
	})
	if err != nil {
		return nil, err
	}
	payload["operations"] = annotateSupersededClusterOperations(operations)
	payload["leaders"] = collectClusterLeaders(nodes)
	return payload, nil
}

func annotateSupersededClusterOperations(operations []map[string]any) []map[string]any {
	for _, operation := range operations {
		kind := cleanText(operation["kind"])
		if !clusterOperationKindSupportsSupersession(kind) || cleanText(operation["state"]) != "failed" {
			continue
		}
		failedAt := coerceInt64(operation["finished_at"])
		var supersedingID string
		var supersedingAt int64
		for _, candidate := range operations {
			candidateAt := coerceInt64(candidate["finished_at"])
			if cleanText(candidate["kind"]) == kind && cleanText(candidate["state"]) == "succeeded" && candidateAt > failedAt && candidateAt > supersedingAt {
				supersedingID = cleanText(candidate["id"])
				supersedingAt = candidateAt
			}
		}
		if supersedingID != "" {
			operation["superseded_by"] = supersedingID
		}
	}
	return operations
}

func clusterOperationKindSupportsSupersession(kind string) bool {
	return textInSet(kind, "cluster_enable", "membership_admit", "membership_scale")
}

func clusterMutationSupportsDatabaseRecovery(mutation clusterMutation) bool {
	switch mutation.Kind {
	case "hmr_exit", "postgres_emergency_failover":
		return true
	case "node_maintenance":
		return cleanText(mutation.Payload["action"]) == "exit"
	case "node_remove":
		emergency, _ := mutation.Payload["emergency"].(bool)
		return emergency
	default:
		return false
	}
}

func clusterMutationSupportsQuorumRecovery(mutation clusterMutation) bool {
	if mutation.Kind == "postgres_emergency_failover" {
		return true
	}
	return mutation.Kind == "node_maintenance" && cleanText(mutation.Payload["action"]) == "exit"
}

func (s *postgresOperatorStore) clusterEvents(ctx context.Context, afterID int64) ([]map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errClusterUnavailable, err)
	}
	defer conn.Close()
	return queryClusterRows(ctx, conn, `
		SELECT id, COALESCE(operation_id,''), COALESCE(admission_id,''), COALESCE(cluster_id,''), event_type, state, message, details_json, created_at
		  FROM engine.cluster_operation_events WHERE id > $1 ORDER BY id LIMIT 500
	`, func(rows *sql.Rows) (map[string]any, error) {
		var id, createdAt int64
		var operationID, admissionID, clusterID, eventType, state, message, detailsJSON string
		if err := rows.Scan(&id, &operationID, &admissionID, &clusterID, &eventType, &state, &message, &detailsJSON, &createdAt); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "operation_id": nilIfEmpty(operationID), "admission_id": nilIfEmpty(admissionID), "cluster_id": nilIfEmpty(clusterID), "event_type": eventType, "state": state, "message": message, "details": parseClusterJSON(detailsJSON), "created_at": createdAt}, nil
	}, afterID)
}

func (s *postgresOperatorStore) createClusterOperation(ctx context.Context, actor string, mutation clusterMutation) (map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errClusterUnavailable, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var enabled int64
	var activeSize int64
	var activeOperationID string
	var clusterID string
	var clusterStatus, hmrState, hmrNodeID, baselineRelease, baselineSHA string
	err = tx.QueryRowContext(ctx, `SELECT enabled, active_size, COALESCE(active_operation_id,''), cluster_id, status, hmr_state, COALESCE(hmr_node_id,''), COALESCE(baseline_release,''), COALESCE(baseline_sha,'') FROM engine.cluster_state WHERE id=1 FOR UPDATE`).Scan(&enabled, &activeSize, &activeOperationID, &clusterID, &clusterStatus, &hmrState, &hmrNodeID, &baselineRelease, &baselineSHA)
	if errors.Is(err, sql.ErrNoRows) {
		if mutation.Kind != "cluster_enable" {
			return nil, fmt.Errorf("%w: cluster is not enabled", errClusterConflict)
		}
		clusterID = newClusterUUID()
		activeSize = 1
		clusterStatus = "Enabling"
		hmrState = "inactive"
		now := time.Now().UTC().Unix()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO engine.cluster_state(id, cluster_id, enabled, status, active_size, desired_size, hmr_state, config_json, created_at, updated_at)
			VALUES(1,$1,0,'Enabling',1,1,'inactive','{}',$2,$2)
		`, clusterID, now); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if mutation.Kind == "cluster_enable" && enabled != 0 {
		return nil, fmt.Errorf("%w: cluster is already enabled", errClusterConflict)
	} else if mutation.Kind != "cluster_enable" && enabled == 0 {
		return nil, fmt.Errorf("%w: cluster is not enabled", errClusterConflict)
	}
	if activeOperationID != "" {
		var activeState string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM engine.cluster_operations WHERE id=$1`, activeOperationID).Scan(&activeState); err == nil && textInSet(activeState, "queued", "running", "waiting") {
			return nil, fmt.Errorf("%w: operation %s is %s", errClusterConflict, activeOperationID, activeState)
		}
	}
	if mutation.Kind != "hmr_exit" && mutation.Kind != "cluster_enable" && hmrState != "inactive" {
		return nil, fmt.Errorf("%w: %s is blocked while HMR state is %s", errClusterConflict, mutation.Kind, hmrState)
	}
	if clusterStatus == "Degraded Database" && !clusterMutationSupportsDatabaseRecovery(mutation) {
		return nil, fmt.Errorf("%w: %s is blocked until PostgreSQL instances recover", errClusterConflict, mutation.Kind)
	}
	if clusterStatus == "Degraded Quorum" && !clusterMutationSupportsQuorumRecovery(mutation) {
		return nil, fmt.Errorf("%w: %s is blocked until three-node membership is restored", errClusterConflict, mutation.Kind)
	}
	if mutation.Kind == "hmr_start" {
		if hmrState != "inactive" {
			return nil, fmt.Errorf("%w: HMR state is %s", errClusterConflict, hmrState)
		}
		if clusterStatus != "Healthy" {
			return nil, fmt.Errorf("%w: HMR requires Healthy cluster, current status is %s", errClusterConflict, clusterStatus)
		}
		var membershipState, applicationState, releaseTag, probesJSON string
		if err := tx.QueryRowContext(ctx, `SELECT membership_state,application_state,COALESCE(release_tag,''),probe_health_json FROM engine.cluster_nodes WHERE id=$1`, mutation.TargetNodeID).Scan(&membershipState, &applicationState, &releaseTag, &probesJSON); errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: HMR target node is not active", errClusterNotFound)
		} else if err != nil {
			return nil, err
		}
		if membershipState != "Active" || applicationState != "active" || (baselineRelease != "" && releaseTag != baselineRelease) || !clusterProbeSetHealthy(parseClusterJSON(probesJSON)) {
			return nil, fmt.Errorf("%w: HMR target lacks healthy pinned production candidate", errClusterConflict)
		}
		mutation.Payload["baseline_release"] = baselineRelease
		mutation.Payload["baseline_sha"] = baselineSHA
		mutation.Payload["started_at"] = time.Now().UTC().Unix()
		mutation.Payload["prior_drain_state"] = "active"
	}
	if mutation.Kind == "hmr_exit" {
		if hmrState != "active" && hmrState != "restore_failed" {
			return nil, fmt.Errorf("%w: HMR is not active", errClusterConflict)
		}
		mutation.TargetNodeID = hmrNodeID
		mutation.TargetRelease = baselineRelease
		mutation.TargetSHA = baselineSHA
		mutation.Payload["baseline_release"] = baselineRelease
		mutation.Payload["baseline_sha"] = baselineSHA
	}
	if mutation.Kind == "membership_scale" {
		desiredSize := coerceInt64(mutation.Payload["desired_size"])
		if err := validateCurrentReleaseClusterExpansion(activeSize, desiredSize); err != nil {
			return nil, fmt.Errorf("%w: %v", errClusterConflict, err)
		}
		if clusterStatus != "Healthy" && clusterStatus != "Pending Quorum" {
			return nil, fmt.Errorf("%w: membership expansion requires healthy quorum", errClusterConflict)
		}
	}
	if mutation.Kind == "node_remove" {
		emergency, _ := mutation.Payload["emergency"].(bool)
		ids := clusterRemovalNodeIDs(mutation.TargetNodeID, mutation.Payload)
		if emergency {
			if len(ids) != 1 || cleanText(mutation.Payload["fencing_confirmation"]) != "TARGET IS POWERED OFF" {
				return nil, fmt.Errorf("%w: emergency removal requires one externally fenced node", errClusterConflict)
			}
			if !currentReleaseClusterRemovalSupported(activeSize) {
				return nil, fmt.Errorf("%w: emergency removal requires three recorded active nodes in current release", errClusterConflict)
			}
		} else {
			if len(ids) != 2 || ids[0] == ids[1] || !currentReleaseClusterRemovalSupported(activeSize) {
				return nil, fmt.Errorf("%w: safe removal requires distinct pair from three-node cluster in current release", errClusterConflict)
			}
			if clusterStatus != "Healthy" {
				return nil, fmt.Errorf("%w: safe paired removal requires Healthy cluster", errClusterConflict)
			}
		}
		var recordedActive int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.cluster_nodes WHERE membership_state='Active'`).Scan(&recordedActive); err != nil {
			return nil, err
		}
		if recordedActive != activeSize {
			return nil, fmt.Errorf("%w: recorded active membership does not match cluster state", errClusterConflict)
		}
		for _, id := range ids {
			var count int64
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.cluster_nodes WHERE id=$1 AND membership_state='Active'`, id).Scan(&count); err != nil {
				return nil, err
			}
			if count != 1 {
				return nil, fmt.Errorf("%w: removal target %s is not active", errClusterNotFound, id)
			}
		}
		mutation.Payload["removal_node_ids"] = ids
		mutation.Payload["target_size"] = activeSize - int64(len(ids))
	}
	if mutation.Kind == "k3s_update" {
		if clusterStatus != "Healthy" || (activeSize != 1 && activeSize != 3) {
			return nil, fmt.Errorf("%w: K3s update requires healthy supported membership", errClusterConflict)
		}
		if err := validateK3sUpgradePath(cleanText(mutation.Payload["source_k3s_version"]), mutation.TargetRelease); err != nil {
			return nil, fmt.Errorf("%w: %v", errClusterConflict, err)
		}
		if !borealisOperatorImmutableImageRefPattern.MatchString(cleanText(mutation.Payload["upgrade_image"])) {
			return nil, fmt.Errorf("%w: K3s upgrade image is not content-addressed", errClusterConflict)
		}
		var healthyCount int64
		rows, queryErr := tx.QueryContext(ctx, `SELECT probe_health_json FROM engine.cluster_nodes WHERE membership_state='Active'`)
		if queryErr != nil {
			return nil, queryErr
		}
		for rows.Next() {
			var probesJSON string
			if err := rows.Scan(&probesJSON); err != nil {
				rows.Close()
				return nil, err
			}
			if clusterProbeSetHealthy(parseClusterJSON(probesJSON)) {
				healthyCount++
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if healthyCount != activeSize {
			return nil, fmt.Errorf("%w: every K3s server must pass Engine probes before upgrade", errClusterConflict)
		}
	}
	if mutation.Kind == "engine_update" {
		compatibility := clusterCompatibilityMap(mutation.Payload["compatibility"])
		if activeSize > 1 && (!textInSet(cleanText(compatibility["database_migration"]), "none", "expand-contract") || coerceInt64(compatibility["maximum_version_skew_releases"]) < 1) {
			return nil, fmt.Errorf("%w: release does not permit safe mixed-version rolling update", errClusterConflict)
		}
		if cleanText(mutation.Payload["scope"]) == "node" {
			var count int64
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.cluster_nodes WHERE id=$1 AND membership_state='Active'`, mutation.TargetNodeID).Scan(&count); err != nil || count != 1 {
				return nil, fmt.Errorf("%w: selected update node is not active", errClusterNotFound)
			}
		}
		rows, queryErr := tx.QueryContext(ctx, `SELECT COALESCE(release_tag,''),probe_health_json FROM engine.cluster_nodes WHERE membership_state='Active'`)
		if queryErr != nil {
			return nil, queryErr
		}
		healthyCount := int64(0)
		for rows.Next() {
			var releaseTag, probesJSON string
			if scanErr := rows.Scan(&releaseTag, &probesJSON); scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			if baselineRelease != "" && releaseTag != baselineRelease && releaseTag != mutation.TargetRelease {
				rows.Close()
				return nil, fmt.Errorf("%w: active node version is outside permitted rolling window", errClusterConflict)
			}
			if clusterProbeSetHealthy(parseClusterJSON(probesJSON)) {
				healthyCount++
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return nil, rowsErr
		}
		if rowsErr := rows.Close(); rowsErr != nil {
			return nil, rowsErr
		}
		minimumHealthy := activeSize
		if activeSize > 1 {
			minimumHealthy = activeSize - 1
		}
		if (clusterStatus != "Healthy" && clusterStatus != "Mixed Version") || healthyCount < minimumHealthy {
			return nil, fmt.Errorf("%w: rolling update requires healthy quorum and spare application capacity", errClusterConflict)
		}
	}
	if mutation.Kind == "engine_update" || mutation.Kind == "k3s_update" {
		rows, queryErr := tx.QueryContext(ctx, `SELECT id,node_name,roles_json FROM engine.cluster_nodes WHERE membership_state='Active' ORDER BY node_name`)
		if queryErr != nil {
			return nil, queryErr
		}
		nodes := make([]clusterControllerNode, 0, 3)
		for rows.Next() {
			var node clusterControllerNode
			var rolesJSON string
			if scanErr := rows.Scan(&node.ID, &node.Name, &rolesJSON); scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			node.Roles = parseClusterJSON(rolesJSON)
			nodes = append(nodes, node)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return nil, rowsErr
		}
		if rowsErr := rows.Close(); rowsErr != nil {
			return nil, rowsErr
		}
		ordered, orderErr := clusterUpdateNodes(clusterControllerOperation{TargetNodeID: mutation.TargetNodeID, Payload: mutation.Payload}, nodes)
		if orderErr != nil {
			return nil, fmt.Errorf("%w: %v", errClusterConflict, orderErr)
		}
		pinnedOrder := make([]string, 0, len(ordered))
		for _, node := range ordered {
			pinnedOrder = append(pinnedOrder, node.ID)
		}
		mutation.Payload["update_node_ids"] = pinnedOrder
	}
	operationID := newClusterUUID()
	now := time.Now().UTC().Unix()
	payloadJSON := marshalClusterJSON(mutation.Payload)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engine.cluster_operations(id, kind, state, current_step, target_node_id, target_release, target_sha, requested_by, payload_json, created_at, updated_at)
		VALUES($1,$2,'queued','preflight',$3,$4,$5,$6,$7,$8,$8)
	`, operationID, mutation.Kind, nullClusterString(mutation.TargetNodeID), nullClusterString(mutation.TargetRelease), nullClusterString(mutation.TargetSHA), actor, payloadJSON, now); err != nil {
		return nil, err
	}
	if mutation.Kind == "cluster_enable" {
		controlVIP := cleanText(mutation.Payload["control_plane_vip"])
		edgeVIP := cleanText(mutation.Payload["edge_vip"])
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET status='Enabling', control_plane_vip=$1, edge_vip=$2, active_operation_id=$3, updated_at=$4 WHERE id=1`, controlVIP, edgeVIP, operationID, now); err != nil {
			return nil, err
		}
	} else if mutation.Kind == "hmr_start" {
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=$1,hmr_state='activating',hmr_node_id=$2,status='HMR Transition',updated_at=$3 WHERE id=1`, operationID, mutation.TargetNodeID, now); err != nil {
			return nil, err
		}
	} else if mutation.Kind == "hmr_exit" {
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=$1,hmr_state='restoring',status='HMR Transition',updated_at=$2 WHERE id=1`, operationID, now); err != nil {
			return nil, err
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=$1, updated_at=$2 WHERE id=1`, operationID, now); err != nil {
		return nil, err
	}
	if err := insertClusterEvent(ctx, tx, operationID, "", clusterID, "operation_queued", "queued", "Cluster operation queued.", mutation.Payload, now); err != nil {
		return nil, err
	}
	if err := insertClusterAudit(ctx, tx, actor, mutation.Kind, firstText(mutation.TargetNodeID, clusterID), "queued", mutation.Payload, now); err != nil {
		return nil, err
	}
	if err := insertClusterOutbox(ctx, tx, "cluster_operation_changed", map[string]any{"operation_id": operationID, "kind": mutation.Kind, "state": "queued"}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"operation_id": operationID, "kind": mutation.Kind, "state": "queued", "current_step": "preflight", "target_node_id": nilIfEmpty(mutation.TargetNodeID), "target_release": nilIfEmpty(mutation.TargetRelease), "target_sha": nilIfEmpty(mutation.TargetSHA)}, nil
}

func validateCurrentReleaseClusterExpansion(activeSize, desiredSize int64) error {
	if activeSize != 1 || desiredSize != 3 {
		return errors.New("current release supports only one-to-three membership expansion; odd membership changes beyond three nodes are future roadmap work")
	}
	return nil
}

func currentReleaseClusterRemovalSupported(activeSize int64) bool {
	return activeSize == 3
}

func currentReleaseAdmissionBatchSize(activeSize, desiredSize int64, status string) (int, error) {
	switch {
	case activeSize == 1 && (desiredSize == 1 || desiredSize == 3):
		return 2, nil
	case activeSize == 2 && desiredSize == 3 && status == "Degraded Quorum":
		return 1, nil
	default:
		return 0, errors.New("current release admits either a pair for one-to-three expansion or one replacement into a degraded two-of-three cluster")
	}
}

func clusterProbeSetHealthy(probes map[string]any) bool {
	for _, key := range []string{"startup", "readiness", "liveness", "direct_endpoint", "service", "database", "scheduler", "agent_path", "wireguard"} {
		if !textInSet(strings.ToLower(cleanText(probes[key])), "passed", "healthy") {
			return false
		}
	}
	return true
}

func clusterCompatibilityMap(value any) map[string]any {
	switch typed := value.(type) {
	case clusterReleaseManifest:
		return map[string]any{
			"database_migration":            typed.DatabaseMigration,
			"maximum_version_skew_releases": typed.MaximumVersionSkewReleases,
		}
	case *clusterReleaseManifest:
		if typed != nil {
			return clusterCompatibilityMap(*typed)
		}
	}
	return mapStringAny(value)
}

func (s *postgresOperatorStore) createClusterInvitation(ctx context.Context, actor string, invitation map[string]any) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.Join(errClusterUnavailable, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var enabled, activeSize, desiredSize int64
	var clusterID, clusterStatus string
	if err := tx.QueryRowContext(ctx, `SELECT enabled,active_size,desired_size,cluster_id,status FROM engine.cluster_state WHERE id=1 FOR UPDATE`).Scan(&enabled, &activeSize, &desiredSize, &clusterID, &clusterStatus); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: cluster is not enabled", errClusterConflict)
	} else if err != nil {
		return err
	}
	if enabled != 1 || clusterID != cleanText(invitation["cluster_id"]) {
		return fmt.Errorf("%w: node invitation does not match enabled cluster", errClusterConflict)
	}
	if _, err := currentReleaseAdmissionBatchSize(activeSize, desiredSize, clusterStatus); err != nil {
		return fmt.Errorf("%w: %v", errClusterConflict, err)
	}
	now := time.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engine.cluster_invitations(id, cluster_id, node_name, token_hash, created_by, expires_at, created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)
	`, cleanText(invitation["id"]), clusterID, cleanText(invitation["node_name"]), cleanText(invitation["token_hash"]), actor, coerceInt64(invitation["expires_at"]), now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *postgresOperatorStore) consumeClusterInvitation(ctx context.Context, admission map[string]any) (map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errClusterUnavailable, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Unix()
	var enabled, activeSize, desiredSize int64
	var clusterStatus, activeOperationID string
	if err := tx.QueryRowContext(ctx, `SELECT enabled,active_size,desired_size,status,COALESCE(active_operation_id,'') FROM engine.cluster_state WHERE id=1 FOR UPDATE`).Scan(&enabled, &activeSize, &desiredSize, &clusterStatus, &activeOperationID); err != nil {
		return nil, err
	}
	admissionBatchSize, admissionModeErr := currentReleaseAdmissionBatchSize(activeSize, desiredSize, clusterStatus)
	if enabled != 1 || admissionModeErr != nil {
		return nil, fmt.Errorf("%w: node invitation cannot be consumed for current membership state", errClusterConflict)
	}
	if activeOperationID != "" {
		return nil, fmt.Errorf("%w: cluster operation %s is active", errClusterConflict, activeOperationID)
	}
	var pendingAdmissions int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM engine.cluster_admissions WHERE state IN ('Pending Quorum','Approved')`).Scan(&pendingAdmissions); err != nil {
		return nil, err
	}
	if pendingAdmissions >= admissionBatchSize {
		return nil, fmt.Errorf("%w: current membership recovery already has required pending admissions", errClusterConflict)
	}
	var invitationNodeName, invitationClusterID string
	var expiresAt int64
	var consumedAt sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT node_name, cluster_id, expires_at, consumed_at
		  FROM engine.cluster_invitations
		 WHERE id=$1 AND token_hash=$2 FOR UPDATE
	`, cleanText(admission["invitation_id"]), cleanText(admission["token_hash"])).Scan(&invitationNodeName, &invitationClusterID, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: invitation was not found", errClusterNotFound)
	}
	if err != nil {
		return nil, err
	}
	if consumedAt.Valid || expiresAt < now || invitationClusterID != cleanText(admission["cluster_id"]) || !strings.EqualFold(invitationNodeName, cleanText(admission["node_name"])) {
		return nil, fmt.Errorf("%w: invitation is expired, consumed, or mismatched", errClusterConflict)
	}
	var activeArchitecture string
	architectureErr := tx.QueryRowContext(ctx, `SELECT architecture FROM engine.cluster_nodes WHERE membership_state='Active' ORDER BY created_at LIMIT 1`).Scan(&activeArchitecture)
	if architectureErr != nil && !errors.Is(architectureErr, sql.ErrNoRows) {
		return nil, architectureErr
	}
	if activeArchitecture != "" && activeArchitecture != cleanText(admission["architecture"]) {
		return nil, fmt.Errorf("%w: node architecture %s does not match active cluster architecture %s", errClusterConflict, cleanText(admission["architecture"]), activeArchitecture)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_invitations SET consumed_at=$1 WHERE id=$2`, now, cleanText(admission["invitation_id"])); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engine.cluster_admissions(id, invitation_id, cluster_id, node_name, hostname, management_ip, architecture, os_version, state, created_at, updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'Pending Quorum',$9,$9)
	`, cleanText(admission["id"]), cleanText(admission["invitation_id"]), invitationClusterID, cleanText(admission["node_name"]), cleanText(admission["hostname"]), cleanText(admission["management_ip"]), cleanText(admission["architecture"]), cleanText(admission["os_version"]), now); err != nil {
		return nil, err
	}
	message := "Node is waiting for paired quorum admission."
	responseMessage := "Node recorded. Add second node, then approve pair from Cluster Management."
	if admissionBatchSize == 1 {
		message = "Replacement node is waiting for degraded-quorum admission."
		responseMessage = "Replacement node recorded. Approve replacement from Cluster Management."
	}
	if err := insertClusterEvent(ctx, tx, "", cleanText(admission["id"]), invitationClusterID, "admission_pending", "Pending Quorum", message, map[string]any{"node_name": admission["node_name"]}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"admission_id": admission["id"], "state": "Pending Quorum", "message": responseMessage}, nil
}

func (s *postgresOperatorStore) approveClusterAdmission(ctx context.Context, actor string, admissionID string) (map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errClusterUnavailable, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var enabled, activeSize, desiredSize int64
	var clusterID, clusterStatus, baselineRelease, baselineSHA, configJSON string
	if err := tx.QueryRowContext(ctx, `SELECT enabled,active_size,desired_size,cluster_id,status,COALESCE(baseline_release,''),COALESCE(baseline_sha,''),config_json FROM engine.cluster_state WHERE id=1 FOR UPDATE`).Scan(&enabled, &activeSize, &desiredSize, &clusterID, &clusterStatus, &baselineRelease, &baselineSHA, &configJSON); err != nil {
		return nil, err
	}
	admissionBatchSize, admissionModeErr := currentReleaseAdmissionBatchSize(activeSize, desiredSize, clusterStatus)
	if enabled != 1 || admissionModeErr != nil {
		return nil, fmt.Errorf("%w: pending admissions cannot be approved for current membership state", errClusterConflict)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,node_name FROM engine.cluster_admissions WHERE state='Pending Quorum' ORDER BY CASE WHEN id=$1 THEN 0 ELSE 1 END, created_at FOR UPDATE`, admissionID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	nodeNames := make([]string, 0)
	for rows.Next() {
		var id, nodeName string
		if err := rows.Scan(&id, &nodeName); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		nodeNames = append(nodeNames, nodeName)
	}
	rows.Close()
	if len(ids) < admissionBatchSize || ids[0] != admissionID {
		return nil, fmt.Errorf("%w: admission requires %d Pending Quorum node(s), including selected node", errClusterConflict, admissionBatchSize)
	}
	ids = ids[:admissionBatchSize]
	nodeNames = nodeNames[:admissionBatchSize]
	now := time.Now().UTC().Unix()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_admissions SET state='Approved', approved_by=$1, approved_at=$2, updated_at=$2 WHERE id=$3`, actor, now, id); err != nil {
			return nil, err
		}
	}
	payload := map[string]any{"admission_ids": ids, "node_names": nodeNames, "baseline_release": baselineRelease, "baseline_sha": baselineSHA, "k3s_version": cleanText(parseClusterJSON(configJSON)["k3s_version"])}
	operationID := newClusterUUID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engine.cluster_operations(id, kind, state, current_step, requested_by, payload_json, created_at, updated_at)
		VALUES($1,'membership_admit','queued','preflight',$2,$3,$4,$4)
	`, operationID, actor, marshalClusterJSON(payload), now); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=$1, updated_at=$2 WHERE id=1 AND COALESCE(active_operation_id,'')=''`, operationID, now)
	if err != nil {
		return nil, err
	}
	locked, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if locked != 1 {
		return nil, fmt.Errorf("%w: another cluster operation is active", errClusterConflict)
	}
	approvalMessage := "Pending node pair approved for admission."
	if admissionBatchSize == 1 {
		approvalMessage = "Replacement node approved for degraded-quorum recovery."
	}
	for _, approvedAdmissionID := range ids {
		if err := insertClusterEvent(ctx, tx, operationID, approvedAdmissionID, clusterID, "admission_pair_approved", "queued", approvalMessage, payload, now); err != nil {
			return nil, err
		}
	}
	if err := insertClusterAudit(ctx, tx, actor, "membership_admit", admissionID, "queued", payload, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"operation_id": operationID, "admission_ids": ids, "state": "queued"}, nil
}

func (s *postgresOperatorStore) retryClusterOperation(ctx context.Context, actor string, operationID string) (map[string]any, error) {
	return s.transitionClusterOperation(ctx, actor, operationID, "retry")
}

func (s *postgresOperatorStore) cancelClusterOperation(ctx context.Context, actor string, operationID string) (map[string]any, error) {
	return s.transitionClusterOperation(ctx, actor, operationID, "cancel")
}

func (s *postgresOperatorStore) transitionClusterOperation(ctx context.Context, actor string, operationID string, action string) (map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errClusterUnavailable, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var state, kind string
	var attempt int64
	if err := tx.QueryRowContext(ctx, `SELECT state, kind, attempt FROM engine.cluster_operations WHERE id=$1 FOR UPDATE`, operationID).Scan(&state, &kind, &attempt); errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: operation does not exist", errClusterNotFound)
	} else if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Unix()
	nextState, step, message := "", "", ""
	if action == "retry" {
		if state != "failed" {
			return nil, fmt.Errorf("%w: only failed operation can be retried", errClusterConflict)
		}
		if clusterOperationKindSupportsSupersession(kind) {
			var supersedingID string
			err := tx.QueryRowContext(ctx, `
				SELECT newer.id
				  FROM engine.cluster_operations current
				  JOIN engine.cluster_operations newer
				    ON newer.kind=current.kind
				   AND newer.state='succeeded'
				   AND COALESCE(newer.finished_at,newer.updated_at) > COALESCE(current.finished_at,current.updated_at)
				 WHERE current.id=$1
				 ORDER BY COALESCE(newer.finished_at,newer.updated_at) DESC
				 LIMIT 1
			`, operationID).Scan(&supersedingID)
			if err == nil {
				return nil, fmt.Errorf("%w: operation was superseded by successful operation %s", errClusterConflict, supersedingID)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
		nextState, step, message = "queued", "preflight", "Cluster operation queued for retry."
		attempt++
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state=$1, current_step=$2, error_text=NULL, finished_at=NULL, attempt=$3, updated_at=$4 WHERE id=$5`, nextState, step, attempt, now, operationID); err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=$1,hmr_state=CASE WHEN $3='hmr_start' THEN 'activating' WHEN $3='hmr_exit' THEN 'restoring' ELSE hmr_state END,updated_at=$2 WHERE id=1 AND COALESCE(active_operation_id,'') IN ('',$1)`, operationID, now, kind)
		if err != nil {
			return nil, err
		}
		locked, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if locked != 1 {
			return nil, fmt.Errorf("%w: another cluster operation is active", errClusterConflict)
		}
	} else {
		if state != "queued" && state != "waiting" {
			return nil, fmt.Errorf("%w: running or completed operation cannot be cancelled", errClusterConflict)
		}
		nextState, step, message = "cancelled", "cancelled", "Cluster operation cancelled at safe boundary."
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET state=$1, current_step=$2, finished_at=$3, updated_at=$3 WHERE id=$4`, nextState, step, now, operationID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=NULL, updated_at=$1 WHERE id=1 AND active_operation_id=$2`, now, operationID); err != nil {
			return nil, err
		}
		if kind == "hmr_start" || kind == "hmr_exit" {
			if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET hmr_state='inactive',hmr_node_id=NULL,status='Healthy',updated_at=$1 WHERE id=1`, now); err != nil {
				return nil, err
			}
		}
	}
	var clusterID string
	_ = tx.QueryRowContext(ctx, `SELECT cluster_id FROM engine.cluster_state WHERE id=1`).Scan(&clusterID)
	if err := insertClusterEvent(ctx, tx, operationID, "", clusterID, "operation_"+action, nextState, message, map[string]any{"attempt": attempt}, now); err != nil {
		return nil, err
	}
	if err := insertClusterAudit(ctx, tx, actor, "operation_"+action, operationID, nextState, map[string]any{"kind": kind, "attempt": attempt}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"operation_id": operationID, "kind": kind, "state": nextState, "current_step": step, "attempt": attempt}, nil
}

func queryClusterRows(ctx context.Context, conn *sql.Conn, query string, scan func(*sql.Rows) (map[string]any, error), args ...any) ([]map[string]any, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func collectClusterLeaders(nodes []map[string]any) map[string]any {
	leaders := map[string]any{"etcd_leader": nil, "control_vip_owner": nil, "edge_vip_owner": nil, "postgres_primary": nil, "scheduler_leader": nil, "wireguard_owner": nil}
	for _, node := range nodes {
		roles, _ := node["roles"].(map[string]any)
		for role := range leaders {
			if coerceClusterBool(roles[role]) {
				leaders[role] = node["id"]
			}
		}
	}
	return leaders
}

func insertClusterEvent(ctx context.Context, tx *sql.Tx, operationID, admissionID, clusterID, eventType, state, message string, details map[string]any, now int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO engine.cluster_operation_events(operation_id, admission_id, cluster_id, event_type, state, message, details_json, created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)
	`, nullClusterString(operationID), nullClusterString(admissionID), nullClusterString(clusterID), eventType, state, message, marshalClusterJSON(details), now)
	return err
}

func insertClusterAudit(ctx context.Context, tx *sql.Tx, actor, action, targetID, result string, details map[string]any, now int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO engine.cluster_audit_events(actor, action, target_id, result, details_json, created_at)
		VALUES($1,$2,$3,$4,$5,$6)
	`, actor, action, nullClusterString(targetID), result, marshalClusterJSON(details), now)
	return err
}

func insertClusterOutbox(ctx context.Context, tx *sql.Tx, name string, payload map[string]any, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO engine.realtime_outbox(event_name, payload_json, created_at) VALUES($1,$2,$3)`, name, marshalClusterJSON(payload), now)
	return err
}

func marshalClusterJSON(value any) string {
	if value == nil {
		return "{}"
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func parseClusterJSON(value string) map[string]any {
	result := map[string]any{}
	_ = json.Unmarshal([]byte(firstText(strings.TrimSpace(value), "{}")), &result)
	return result
}

func nullClusterString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func nilIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nilIfZero(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func coerceClusterBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	case string:
		return parseTruthy(typed)
	default:
		return false
	}
}

func sortedClusterStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

var _ clusterStore = (*postgresOperatorStore)(nil)
