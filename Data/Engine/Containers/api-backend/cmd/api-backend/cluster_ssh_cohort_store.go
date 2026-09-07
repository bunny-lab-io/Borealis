package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
)

// Read every planned target in one database snapshot. Ineligible rows must not
// disappear through an inner credential join: a partial expansion is unsafe.
// This read grants no preparation authority and never advances the operation.
// Consumers must recheck the snapshot when committing a later controller step.
func (s *postgresOperatorStore) loadClusterSSHInspectionCohort(ctx context.Context, operationID, controllerHolder string, attempt int64) (clusterSSHInspectionCohort, error) {
	if !clusterUUIDRE.MatchString(operationID) || !clusterUUIDRE.MatchString(controllerHolder) || attempt < 1 {
		return clusterSSHInspectionCohort{}, errClusterSSHCohort
	}
	type storedTarget struct {
		target    clusterSSHInspectedTarget
		raw       string
		keyBase64 string
		eligible  bool
		now       int64
	}
	rows, err := s.db.QueryContext(ctx, `WITH moment AS (SELECT floor(extract(epoch FROM statement_timestamp()))::bigint AS now)
  SELECT t.cluster_id,t.operation_id,t.id,t.management_ip,t.ssh_port,t.host_key_fingerprint,
   t.host_key_algorithm,t.host_key_base64,t.ordinal,t.inspected_at,t.inspected_attempt,t.inspected_generation,
   t.inspection_json,moment.now,
   (t.cluster_id=c.cluster_id AND t.state='queued' AND t.current_step='inspection_complete' AND t.credential_state='available'
    AND t.lease_holder='' AND t.lease_expires_at=0 AND t.operation_attempt=o.attempt
    AND t.inspected_attempt=o.attempt AND t.inspected_generation=t.lease_generation AND t.inspected_generation>0
    AND t.inspected_at>moment.now-$5 AND t.inspected_at<=moment.now
    AND EXISTS(SELECT 1 FROM engine.cluster_onboarding_credentials p JOIN engine.aegis_cipher_state a
      ON a.id=1 AND a.verification_token=p.aegis_generation WHERE p.target_id=t.id AND p.expires_at>moment.now))
  FROM engine.cluster_operations o
  JOIN engine.cluster_state c ON c.active_operation_id=o.id
  JOIN engine.cluster_application_leases l ON l.name=$4 AND l.holder=$2
  JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id
  CROSS JOIN moment
  WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.attempt=$3
    AND o.current_step='inspect_ssh_targets' AND l.expires_at>moment.now
  ORDER BY t.ordinal LIMIT 3`, operationID, controllerHolder, attempt, clusterControllerLeaseName, clusterSSHInspectionLifetimeSeconds)
	if err != nil {
		return clusterSSHInspectionCohort{}, errClusterUnavailable
	}
	var stored []storedTarget
	for rows.Next() {
		var entry storedTarget
		t := &entry.target
		if err := rows.Scan(&t.Binding.ClusterID, &t.Binding.OperationID, &t.Binding.TargetID, &t.Binding.Address, &t.Binding.Port, &t.Binding.Fingerprint,
			&t.Key.Algorithm, &entry.keyBase64, &t.Ordinal, &t.InspectedAt, &t.Attempt, &t.Generation, &entry.raw, &entry.now, &entry.eligible); err != nil {
			rows.Close()
			return clusterSSHInspectionCohort{}, errClusterUnavailable
		}
		stored = append(stored, entry)
	}
	scanErr := rows.Err()
	closeErr := rows.Close()
	if scanErr != nil || closeErr != nil {
		return clusterSSHInspectionCohort{}, errClusterUnavailable
	}
	// Parsing and shaping begin only after the pooled connection returns.
	if len(stored) < 1 || len(stored) > 2 {
		return clusterSSHInspectionCohort{}, errClusterSSHCohort
	}
	cohort := clusterSSHInspectionCohort{ClusterID: stored[0].target.Binding.ClusterID, OperationID: operationID, ControllerHolder: controllerHolder, Attempt: attempt, ObservedAt: stored[0].now}
	for _, entry := range stored {
		t := entry.target
		if !entry.eligible || len(entry.raw) > 16<<10 || len(entry.keyBase64) > 16<<10 || json.Unmarshal([]byte(entry.raw), &t.Report) != nil || t.Report.valid() != nil {
			return clusterSSHInspectionCohort{}, errClusterSSHCohort
		}
		// Only the canonical typed serializer writes reports. Reject unknown fields,
		// duplicate/case-aliased keys or trailing data even in a corrupted DB record.
		canonical, err := json.Marshal(t.Report)
		if err != nil || string(canonical) != entry.raw {
			return clusterSSHInspectionCohort{}, errClusterSSHCohort
		}
		t.Key.Fingerprint = t.Binding.Fingerprint
		t.Key.PublicKey, err = base64.StdEncoding.Strict().DecodeString(entry.keyBase64)
		if err != nil || base64.StdEncoding.EncodeToString(t.Key.PublicKey) != entry.keyBase64 || t.Key.Validate() != nil || !t.Binding.valid() {
			return clusterSSHInspectionCohort{}, errClusterSSHCohort
		}
		cohort.Targets = append(cohort.Targets, t)
	}
	return cohort, nil
}
