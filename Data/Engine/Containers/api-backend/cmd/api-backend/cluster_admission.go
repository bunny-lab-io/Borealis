package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Bind every admission read to the exact consumed invitation, including its
// bearer hash and target. A valid invite for the same cluster is insufficient.
func (s *postgresOperatorStore) clusterAdmissionStatus(ctx context.Context, admissionID string, claims map[string]any) (map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, errors.Join(errClusterUnavailable, err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var state string
	var expiresAt, approvedAt, invitationCreatedAt int64
	err = tx.QueryRowContext(ctx, `SELECT a.state,i.expires_at,COALESCE(a.approved_at,0),i.created_at
		FROM engine.cluster_admissions a JOIN engine.cluster_invitations i ON i.id=a.invitation_id
		JOIN engine.cluster_state c ON c.id=1 AND c.enabled=1 AND c.cluster_id=a.cluster_id
		WHERE a.id=$1 AND a.invitation_id=$2 AND a.cluster_id=$3 AND LOWER(a.node_name)=LOWER($4)
		AND i.cluster_id=a.cluster_id AND LOWER(i.node_name)=LOWER(a.node_name) AND i.token_hash=$5 AND i.consumed_at IS NOT NULL`,
		admissionID, cleanText(claims["invitation_id"]), cleanText(claims["cluster_id"]), cleanText(claims["node_name"]), clusterTokenHash(cleanText(claims["token"]))).Scan(&state, &expiresAt, &approvedAt, &invitationCreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: admission does not match invitation", errClusterNotFound)
	}
	if err != nil {
		return nil, err
	}
	expiresAt = clusterAdmissionAuthorizationExpiry(state, invitationCreatedAt, expiresAt)
	if expiresAt < time.Now().Unix() {
		return nil, fmt.Errorf("%w: admission invitation expired", errClusterConflict)
	}
	var joinConfigRaw sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT payload_json::jsonb->'join_config' FROM engine.cluster_operations
		WHERE kind='membership_admit' AND payload_json::jsonb->'admission_ids' ? $1 ORDER BY created_at DESC LIMIT 1`, admissionID).Scan(&joinConfigRaw); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(operation_id,''),event_type,state,message,details_json,created_at
		FROM (SELECT * FROM engine.cluster_operation_events WHERE admission_id=$1 AND cluster_id=$2 ORDER BY id DESC LIMIT 500) scoped ORDER BY id`, admissionID, cleanText(claims["cluster_id"]))
	if err != nil {
		return nil, err
	}
	type admissionEvent struct {
		id, createdAt                                   int64
		operationID, eventType, state, message, details string
	}
	storedEvents := make([]admissionEvent, 0)
	for rows.Next() {
		var event admissionEvent
		if err := rows.Scan(&event.id, &event.operationID, &event.eventType, &event.state, &event.message, &event.details, &event.createdAt); err != nil {
			rows.Close()
			return nil, err
		}
		storedEvents = append(storedEvents, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	conn.Close()
	events := make([]map[string]any, 0, len(storedEvents))
	for _, event := range storedEvents {
		events = append(events, map[string]any{"id": event.id, "operation_id": nilIfEmpty(event.operationID), "admission_id": admissionID,
			"cluster_id": claims["cluster_id"], "event_type": event.eventType, "state": event.state, "message": event.message, "details": parseClusterJSON(event.details), "created_at": event.createdAt})
	}
	return map[string]any{"admission_id": admissionID, "state": state, "approved_at": nilIfZero(approvedAt), "expires_at": expiresAt, "events": events, "join_config": parseClusterJSON(joinConfigRaw.String)}, nil
}

func existingClusterAdmission(ctx context.Context, tx *sql.Tx, request map[string]any) (map[string]any, error) {
	var id, state, nodeName, hostname, managementIP, architecture, osVersion string
	err := tx.QueryRowContext(ctx, `SELECT id,state,node_name,hostname,management_ip,architecture,os_version
		FROM engine.cluster_admissions WHERE invitation_id=$1 AND cluster_id=$2 FOR UPDATE`,
		cleanText(request["invitation_id"]), cleanText(request["cluster_id"])).Scan(&id, &state, &nodeName, &hostname, &managementIP, &architecture, &osVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: accepted invitation binding was revoked", errClusterNotFound)
	}
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(nodeName, cleanText(request["node_name"])) || !strings.EqualFold(hostname, cleanText(request["hostname"])) ||
		managementIP != cleanText(request["management_ip"]) || architecture != cleanText(request["architecture"]) || osVersion != cleanText(request["os_version"]) {
		return nil, fmt.Errorf("%w: accepted admission target identity cannot change", errClusterConflict)
	}
	if !textInSet(state, "Pending Quorum", "Approved", "Admitted") {
		return nil, fmt.Errorf("%w: admission is %s", errClusterConflict, state)
	}
	return map[string]any{"admission_id": id, "state": state, "message": "Resuming previously accepted admission."}, nil
}
