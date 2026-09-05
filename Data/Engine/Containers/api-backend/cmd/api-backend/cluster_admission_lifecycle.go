package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func clusterAdmissionCancelHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, failure := requireAdmin(r.Context(), auth, r)
		if failure != nil {
			failure.write(w)
			return
		}
		id := strings.ToLower(cleanText(r.PathValue("id")))
		body, err := readJSONMapWithLimit(r, clusterJSONMaxBytes)
		if err != nil {
			invalidJSONOrValidation(w, err)
			return
		}
		errs := validateClusterUUID("id", id)
		errs = append(errs, rejectUnknownClusterFields(body, map[string]bool{"confirmation": true})...)
		if cleanText(body["confirmation"]) != "CANCEL ADMISSION" {
			errs = append(errs, publicValidationError{Field: "confirmation", Message: "must equal CANCEL ADMISSION"})
		}
		if len(errs) > 0 {
			writePublicValidationErrors(w, errs)
			return
		}
		store, ok := auth.store.(clusterStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster_control_unavailable"})
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		result, err := store.cancelClusterAdmission(ctx, identity.Username, id)
		if err != nil {
			writeClusterError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// Pending means no join authorization was issued. Only that state may release
// a slot automatically. Any operation/member evidence keeps the identity held.
func reconcileClusterAdmissionsTx(ctx context.Context, tx *sql.Tx, now int64) error {
	rows, err := tx.QueryContext(ctx, `UPDATE engine.cluster_admissions a SET
		state=CASE WHEN a.approved_at IS NOT NULL OR EXISTS(
			SELECT 1 FROM engine.cluster_nodes n WHERE n.membership_state<>'Removed' AND (n.node_name=a.node_name OR n.management_ip=a.management_ip)
		) OR EXISTS(SELECT 1 FROM engine.cluster_operations o WHERE o.kind='membership_admit' AND o.payload_json::jsonb->'admission_ids' ? a.id)
		THEN 'Recovery Required' ELSE 'Expired' END,updated_at=$1
		FROM engine.cluster_invitations i WHERE i.id=a.invitation_id AND a.state='Pending Quorum' AND i.expires_at<$1
		RETURNING a.id,a.cluster_id,a.state`, now)
	if err != nil {
		return err
	}
	type transition struct{ id, clusterID, state string }
	var changed []transition
	for rows.Next() {
		var item transition
		if err := rows.Scan(&item.id, &item.clusterID, &item.state); err != nil {
			rows.Close()
			return err
		}
		changed = append(changed, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range changed {
		message := "Invitation expired before join authorization; unused admission capacity released."
		if item.state == "Recovery Required" {
			message = "Admission expiry found membership or operation evidence; capacity retained for recovery."
		}
		if err := insertClusterEvent(ctx, tx, "", item.id, item.clusterID, "admission_expired", item.state, message, nil, now); err != nil {
			return err
		}
	}
	rows, err = tx.QueryContext(ctx, `UPDATE engine.cluster_admissions a SET state='Recovery Required',updated_at=$1
		WHERE a.state='Approved' AND EXISTS(SELECT 1 FROM engine.cluster_operations o
		WHERE o.kind='membership_admit' AND o.state IN ('failed','cancelled') AND o.payload_json::jsonb->'admission_ids' ? a.id)
		RETURNING a.id,a.cluster_id,a.state`, now)
	if err != nil {
		return err
	}
	changed = nil
	for rows.Next() {
		var item transition
		if err := rows.Scan(&item.id, &item.clusterID, &item.state); err != nil {
			rows.Close()
			return err
		}
		changed = append(changed, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range changed {
		if err := insertClusterEvent(ctx, tx, "", item.id, item.clusterID, "admission_recovery_required", item.state, "Original membership operation stopped; retain target and retry original operation before resuming join.", nil, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *clusterController) reconcileAdmissions(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := c.requireLeaseOwnership(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM engine.cluster_state WHERE id=1 FOR UPDATE`); err != nil {
		return err
	}
	if err := reconcileClusterAdmissionsTx(ctx, tx, c.now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *postgresOperatorStore) cancelClusterAdmission(ctx context.Context, actor, admissionID string) (map[string]any, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var clusterID string
	if err := tx.QueryRowContext(ctx, `SELECT cluster_id FROM engine.cluster_state WHERE id=1 AND enabled=1 FOR UPDATE`).Scan(&clusterID); err != nil {
		return nil, err
	}
	var state string
	var uncertain bool
	err = tx.QueryRowContext(ctx, `SELECT a.state,a.approved_at IS NOT NULL OR EXISTS(
		SELECT 1 FROM engine.cluster_nodes n WHERE n.membership_state<>'Removed' AND (n.node_name=a.node_name OR n.management_ip=a.management_ip)
	) OR EXISTS(SELECT 1 FROM engine.cluster_operations o WHERE o.kind='membership_admit' AND o.payload_json::jsonb->'admission_ids' ? a.id)
	FROM engine.cluster_admissions a WHERE a.id=$1 AND a.cluster_id=$2 FOR UPDATE`, admissionID, clusterID).Scan(&state, &uncertain)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errClusterNotFound
	}
	if err != nil {
		return nil, err
	}
	if state == "Cancelled" || state == "Expired" {
		return map[string]any{"admission_id": admissionID, "state": state}, tx.Commit()
	}
	if state != "Pending Quorum" || uncertain {
		return nil, fmt.Errorf("%w: admission may have joined; retain identity and retry membership recovery instead of releasing capacity", errClusterConflict)
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_admissions SET state='Cancelled',updated_at=$1 WHERE id=$2`, now, admissionID); err != nil {
		return nil, err
	}
	if err := insertClusterEvent(ctx, tx, "", admissionID, clusterID, "admission_cancelled", "Cancelled", "Admission cancelled before join authorization; unused capacity released.", nil, now); err != nil {
		return nil, err
	}
	if err := insertClusterAudit(ctx, tx, actor, "admission_cancel", admissionID, "Cancelled", nil, now); err != nil {
		return nil, err
	}
	return map[string]any{"admission_id": admissionID, "state": "Cancelled"}, tx.Commit()
}
