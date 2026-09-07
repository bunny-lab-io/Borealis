package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"

	"borealis/api-backend/internal/clusterremote"
)

const clusterSSHCredentialLifetimeSeconds = 2 * 60 * 60
const clusterSSHTargetLeaseSeconds = 45

// The existing cluster operation remains the membership authority. Target
// leases authorize its Aegis-capable workers to execute one requested SSH step.
var clusterSSHSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS engine.cluster_onboarding_targets (
		id TEXT PRIMARY KEY,
		operation_id TEXT NOT NULL REFERENCES engine.cluster_operations(id) ON DELETE CASCADE,
		cluster_id TEXT NOT NULL,
		ordinal BIGINT NOT NULL CHECK (ordinal BETWEEN 1 AND 2),
		management_ip TEXT NOT NULL,
		ssh_port BIGINT NOT NULL CHECK (ssh_port BETWEEN 1 AND 65535),
		host_key_algorithm TEXT NOT NULL,
		host_key_fingerprint TEXT NOT NULL,
		host_key_base64 TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','prepared','joined','recovery_required','completed','failed','cancelled')),
		credential_state TEXT NOT NULL DEFAULT 'available' CHECK (credential_state IN ('available','required')),
		current_step TEXT NOT NULL DEFAULT 'inspect',
		inspection_json TEXT NOT NULL DEFAULT '{}',
		inspected_at BIGINT NOT NULL DEFAULT 0,
		inspected_attempt BIGINT NOT NULL DEFAULT 0,
		inspected_generation BIGINT NOT NULL DEFAULT 0,
		operation_attempt BIGINT NOT NULL DEFAULT 1,
		lease_holder TEXT NOT NULL DEFAULT '',
		lease_generation BIGINT NOT NULL DEFAULT 0,
		lease_expires_at BIGINT NOT NULL DEFAULT 0,
		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,
		UNIQUE(operation_id, ordinal),
		UNIQUE(operation_id, management_ip),
		UNIQUE(operation_id, host_key_fingerprint)
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_cluster_onboarding_active_address
		ON engine.cluster_onboarding_targets(management_ip)
		WHERE state NOT IN ('completed','failed','cancelled')`,
	`CREATE TABLE IF NOT EXISTS engine.cluster_onboarding_credentials (
		target_id TEXT PRIMARY KEY REFERENCES engine.cluster_onboarding_targets(id) ON DELETE CASCADE,
		aegis_state_id BIGINT NOT NULL DEFAULT 1 CHECK (aegis_state_id=1)
			REFERENCES engine.aegis_cipher_state(id) ON DELETE CASCADE,
		aegis_generation TEXT NOT NULL,
		ciphertext TEXT NOT NULL CHECK (ciphertext LIKE 'aegis:v1:%' AND octet_length(ciphertext)<=262144),
		expires_at BIGINT NOT NULL,
		created_at BIGINT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_cluster_onboarding_credentials_expiry
		ON engine.cluster_onboarding_credentials(expires_at)`,
}

type clusterSSHPlannedTarget struct {
	Binding   clusterSSHCredentialBinding
	Key       clusterremote.HostKey
	KeyBase64 string
	Sealed    sealedClusterSSHCredentials
}

func validateClusterSSHPlannedTargets(targets []clusterSSHPlannedTarget, expected int) error {
	if (expected != 1 && expected != 2) || len(targets) != expected {
		return errClusterSSHCredentials
	}
	addresses, fingerprints, ids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, target := range targets {
		if !target.Binding.valid() || target.Key.Validate() != nil || target.Key.Fingerprint != target.Binding.Fingerprint ||
			target.KeyBase64 != base64.StdEncoding.EncodeToString(target.Key.PublicKey) ||
			target.Sealed.binding != target.Binding || !strings.HasPrefix(target.Sealed.ciphertext, aegisEnvelopePrefix) ||
			len(target.Sealed.ciphertext) > 2*clusterSSHBodyMaxBytes || target.Sealed.generation == "" ||
			addresses[target.Binding.Address] || fingerprints[target.Binding.Fingerprint] || ids[target.Binding.TargetID] {
			return errClusterSSHCredentials
		}
		addresses[target.Binding.Address], fingerprints[target.Binding.Fingerprint], ids[target.Binding.TargetID] = true, true, true
	}
	return nil
}

// insertClusterSSHPlannedTargets is called only inside the operation-queue
// transaction, after ordinary cluster/release/admission gates. Validation and
// Aegis encryption must finish before acquiring that database connection.
func insertClusterSSHPlannedTargets(ctx context.Context, tx *sql.Tx, operationID, clusterID string, targets []clusterSSHPlannedTarget) error {
	var generation string
	if err := tx.QueryRowContext(ctx, `SELECT verification_token FROM engine.aegis_cipher_state WHERE id=1 FOR SHARE`).Scan(&generation); err != nil {
		return errClusterSSHCredentials
	}
	var now int64
	if err := tx.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp()))::bigint`).Scan(&now); err != nil {
		return errClusterUnavailable
	}
	for index, target := range targets {
		if target.Binding.OperationID != operationID || target.Binding.ClusterID != clusterID || target.Sealed.generation != generation {
			return errClusterSSHCredentials
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_onboarding_targets
			(id,operation_id,cluster_id,ordinal,management_ip,ssh_port,host_key_algorithm,host_key_fingerprint,host_key_base64,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`, target.Binding.TargetID, operationID, clusterID, index+1,
			target.Binding.Address, target.Binding.Port, target.Key.Algorithm, target.Key.Fingerprint, target.KeyBase64, now); err != nil {
			return errClusterConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_onboarding_credentials
			(target_id,aegis_generation,ciphertext,expires_at,created_at) VALUES($1,$2,$3,$4,$5)`,
			target.Binding.TargetID, generation, target.Sealed.ciphertext, now+clusterSSHCredentialLifetimeSeconds, now); err != nil {
			// Constraint diagnostics can contain complete rows. Never return them.
			return errClusterSSHCredentials
		}
	}
	return nil
}

type clusterSSHTargetLease struct {
	TargetID         string
	OperationID      string
	Holder           string
	Generation       int64
	Step             string
	ControllerHolder string
	OperationAttempt int64
	OperationStep    string
	OperationKind    string
}

// claimClusterSSHTarget uses the database clock. It never renews or overwrites an
// unexpired owner and cannot claim outside the sole active controller operation.
func (s *postgresOperatorStore) claimClusterSSHTarget(ctx context.Context, operationID, targetID, holder string) (clusterSSHTargetLease, error) {
	if !clusterUUIDRE.MatchString(operationID) || !clusterUUIDRE.MatchString(targetID) || !clusterUUIDRE.MatchString(holder) {
		return clusterSSHTargetLease{}, errClusterConflict
	}
	var lease clusterSSHTargetLease
	err := s.db.QueryRowContext(ctx, `WITH moment AS (SELECT floor(extract(epoch FROM clock_timestamp()))::bigint AS now)
		UPDATE engine.cluster_onboarding_targets t
		SET lease_holder=$3,lease_generation=t.lease_generation+1,lease_expires_at=moment.now+$4,state='running',updated_at=moment.now
		FROM moment,engine.cluster_operations o,engine.cluster_state c,engine.cluster_onboarding_credentials p,engine.aegis_cipher_state a,engine.cluster_application_leases l
		WHERE t.id=$2 AND t.operation_id=$1 AND o.id=t.operation_id AND c.active_operation_id=o.id
		  AND c.cluster_id=t.cluster_id AND o.state='running' AND o.attempt=t.operation_attempt
		  AND l.name=$5 AND l.holder<>'' AND l.expires_at>moment.now
		  AND t.state IN ('queued','running','recovery_required') AND t.credential_state='available' AND t.lease_expires_at<=moment.now
		  AND p.target_id=t.id AND p.expires_at>moment.now AND a.id=1 AND p.aegis_generation=a.verification_token
		RETURNING t.id,t.operation_id,t.lease_holder,t.lease_generation,t.current_step,l.holder,o.attempt,o.current_step,o.kind`, operationID, targetID, holder, clusterSSHTargetLeaseSeconds, clusterControllerLeaseName).
		Scan(&lease.TargetID, &lease.OperationID, &lease.Holder, &lease.Generation, &lease.Step, &lease.ControllerHolder, &lease.OperationAttempt, &lease.OperationStep, &lease.OperationKind)
	if errors.Is(err, sql.ErrNoRows) {
		return clusterSSHTargetLease{}, errClusterConflict
	}
	if err != nil {
		return clusterSSHTargetLease{}, errClusterUnavailable
	}
	return lease, nil
}

func (s *postgresOperatorStore) renewClusterSSHTarget(ctx context.Context, lease clusterSSHTargetLease) error {
	return s.renewClusterSSHTargetGeneration(ctx, lease, "")
}

// Active workers also bind the exact generation they decrypted. A concurrent
// replacement of both Aegis state and credential record must not renew them.
func (s *postgresOperatorStore) renewClusterSSHTargetGeneration(ctx context.Context, lease clusterSSHTargetLease, generation string) error {
	result, err := s.db.ExecContext(ctx, `WITH moment AS (SELECT floor(extract(epoch FROM clock_timestamp()))::bigint AS now)
		UPDATE engine.cluster_onboarding_targets t SET lease_expires_at=moment.now+$6,updated_at=moment.now
		FROM moment,engine.cluster_operations o,engine.cluster_state c,engine.cluster_onboarding_credentials p,engine.aegis_cipher_state a,engine.cluster_application_leases l
		WHERE t.id=$1 AND t.operation_id=$2 AND t.lease_holder=$3 AND t.lease_generation=$4 AND t.current_step=$5
		  AND t.lease_expires_at>moment.now AND t.state='running' AND t.credential_state='available' AND o.id=t.operation_id AND o.state='running'
		  AND c.active_operation_id=o.id AND c.cluster_id=t.cluster_id
		  AND l.name=$10 AND l.holder=$7 AND l.expires_at>moment.now
		  AND o.attempt=$8 AND t.operation_attempt=o.attempt AND o.current_step=$9
		  AND p.target_id=t.id AND p.expires_at>moment.now AND a.id=1 AND p.aegis_generation=a.verification_token
		  AND ($11='' OR p.aegis_generation=$11) AND o.kind=$12`,
		lease.TargetID, lease.OperationID, lease.Holder, lease.Generation, lease.Step, clusterSSHTargetLeaseSeconds, lease.ControllerHolder, lease.OperationAttempt, lease.OperationStep, clusterControllerLeaseName, generation, lease.OperationKind)
	if err != nil {
		return errClusterUnavailable
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return errClusterConflict
	}
	return nil
}

func (s *postgresOperatorStore) loadClusterSSHTargetCredentials(ctx context.Context, lease clusterSSHTargetLease) (sealedClusterSSHCredentials, error) {
	var sealed sealedClusterSSHCredentials
	err := s.db.QueryRowContext(ctx, `SELECT t.cluster_id,t.operation_id,t.id,t.management_ip,t.ssh_port,t.host_key_fingerprint,p.aegis_generation,p.ciphertext
		FROM engine.cluster_onboarding_targets t JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
		JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
		JOIN engine.cluster_operations o ON o.id=t.operation_id AND o.state='running'
		JOIN engine.cluster_state c ON c.active_operation_id=o.id AND c.cluster_id=t.cluster_id
		JOIN engine.cluster_application_leases l ON l.name=$9 AND l.holder=$6 AND l.expires_at>extract(epoch FROM clock_timestamp())
		WHERE t.id=$1 AND t.operation_id=$2 AND t.lease_holder=$3 AND t.lease_generation=$4 AND t.current_step=$5
		  AND t.state='running' AND t.credential_state='available' AND t.lease_expires_at>extract(epoch FROM clock_timestamp())
		  AND o.attempt=$7 AND t.operation_attempt=o.attempt AND o.current_step=$8
		  AND p.expires_at>extract(epoch FROM clock_timestamp()) AND o.kind=$10`, lease.TargetID, lease.OperationID, lease.Holder, lease.Generation, lease.Step, lease.ControllerHolder, lease.OperationAttempt, lease.OperationStep, clusterControllerLeaseName, lease.OperationKind).
		Scan(&sealed.binding.ClusterID, &sealed.binding.OperationID, &sealed.binding.TargetID, &sealed.binding.Address, &sealed.binding.Port, &sealed.binding.Fingerprint, &sealed.generation, &sealed.ciphertext)
	if err != nil {
		return sealedClusterSSHCredentials{}, errClusterSSHCredentials
	}
	// QueryRow.Scan releases its connection before the caller performs crypto/SSH.
	return sealed, nil
}

// Cleanup removes only active credential records. Target evidence is retained;
// expiry, rotation or reset cannot label an uncertain remote action completed.
func (s *postgresOperatorStore) cleanupClusterSSHCredentials(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterUnavailable
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM engine.cluster_onboarding_credentials p
		USING engine.cluster_onboarding_targets t,engine.cluster_operations o
		WHERE p.target_id=t.id AND o.id=t.operation_id AND
		(p.expires_at<=extract(epoch FROM clock_timestamp()) OR o.state IN ('succeeded','failed','cancelled')
		 OR t.state IN ('completed','failed','cancelled') OR NOT EXISTS
		 (SELECT 1 FROM engine.aegis_cipher_state a WHERE a.id=1 AND a.verification_token=p.aegis_generation))`); err != nil {
		return errClusterUnavailable
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets t
		SET credential_state='required',lease_holder='',lease_generation=lease_generation+1,lease_expires_at=0,
		updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint
		WHERE t.credential_state<>'required'
		AND NOT EXISTS (SELECT 1 FROM engine.cluster_onboarding_credentials p WHERE p.target_id=t.id)`); err != nil {
		return errClusterUnavailable
	}
	if tx.Commit() != nil {
		return errClusterUnavailable
	}
	return nil
}
