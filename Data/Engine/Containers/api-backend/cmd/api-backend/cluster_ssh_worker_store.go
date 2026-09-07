package main

import (
	"borealis/api-backend/internal/clusterremote"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
)

func (s *postgresOperatorStore) loadClusterSSHTargetKey(ctx context.Context, lease clusterSSHTargetLease) (clusterremote.HostKey, error) {
	var key clusterremote.HostKey
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT host_key_algorithm,host_key_fingerprint,host_key_base64
		FROM engine.cluster_onboarding_targets WHERE id=$1 AND operation_id=$2`, lease.TargetID, lease.OperationID).
		Scan(&key.Algorithm, &key.Fingerprint, &encoded)
	// The following public key parsing is outside the database connection.
	if err != nil || len(encoded) > 16<<10 {
		return clusterremote.HostKey{}, errClusterSSHCredentials
	}
	key.PublicKey, err = base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.StdEncoding.EncodeToString(key.PublicKey) != encoded || key.Validate() != nil {
		return clusterremote.HostKey{}, errClusterSSHCredentials
	}
	return key, nil
}

// Report is typed public metadata, never remote stdout/error or credentials.
// Atomic completion repeats every authority fence after SSH/crypto returns.
func (s *postgresOperatorStore) completeClusterSSHInspection(ctx context.Context, lease clusterSSHTargetLease, generation string, report clusterSSHInspectionReport) error {
	if lease.Step != "inspect" || generation == "" || report.valid() != nil {
		return errClusterConflict
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > clusterremote.MaxOutputBytes {
		return errClusterConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterUnavailable
	}
	defer tx.Rollback()
	var clusterID string
	var now int64
	err = tx.QueryRowContext(ctx, `WITH authority AS MATERIALIZED (
		SELECT c.cluster_id,t.id AS target_id
		FROM engine.cluster_operations o
		JOIN engine.cluster_state c ON c.active_operation_id=o.id
		JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id AND t.cluster_id=c.cluster_id
		JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
		JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation AND p.aegis_generation=$11
		JOIN engine.cluster_application_leases l ON l.name=$9 AND l.holder=$6
		WHERE t.id=$1 AND o.id=$2 AND o.state='running' AND o.attempt=$7 AND o.current_step=$8 AND o.kind=$12
		  AND l.expires_at>extract(epoch FROM clock_timestamp()) AND p.expires_at>extract(epoch FROM clock_timestamp())
		FOR SHARE OF o,c,p,a,l
	), moment AS (SELECT floor(extract(epoch FROM clock_timestamp()))::bigint AS now)
		UPDATE engine.cluster_onboarding_targets t
		SET inspection_json=$10,inspected_at=moment.now,current_step='inspection_complete',state='queued',
		    lease_holder='',lease_expires_at=0,updated_at=moment.now
		FROM moment,authority
		WHERE t.id=$1 AND t.operation_id=$2 AND t.lease_holder=$3 AND t.lease_generation=$4 AND t.current_step=$5
		  AND t.lease_expires_at>moment.now AND t.state='running' AND t.credential_state='available'
		  AND authority.target_id=t.id AND authority.cluster_id=t.cluster_id AND t.operation_attempt=$7
		RETURNING t.cluster_id,moment.now`, lease.TargetID, lease.OperationID, lease.Holder, lease.Generation, lease.Step,
		lease.ControllerHolder, lease.OperationAttempt, lease.OperationStep, clusterControllerLeaseName, string(raw), generation, lease.OperationKind).Scan(&clusterID, &now)
	if errors.Is(err, sql.ErrNoRows) {
		return errClusterConflict
	}
	if err != nil {
		return errClusterUnavailable
	}
	if err := insertClusterEvent(ctx, tx, lease.OperationID, "", clusterID, "ssh_target_inspected", "inspection_complete", "SSH target inspection recorded; preparation not authorized", map[string]any{"target_id": lease.TargetID, "generation": lease.Generation, "hostname": report.Hostname}, now); err != nil {
		return errClusterUnavailable
	}
	if tx.Commit() != nil {
		return errClusterUnavailable
	}
	return nil
}
