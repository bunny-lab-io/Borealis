package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Retain the source snapshot across credential encryption. The queue transaction
// compares it again; neither crypto nor configuration parsing holds a connection.
type clusterSSHQueueSource struct {
	ClusterID, Status, HMRState, ActiveOperationID string
	Release, SHA, ConfigJSON, ControlVIP, EdgeVIP  string
	Enabled, ActiveSize, DesiredSize               int64
	Members, Drained, PendingAdmissions            int64
}

func readClusterSSHQueueSource(ctx context.Context, reader clusterSSHParentReader) (clusterSSHQueueSource, error) {
	var source clusterSSHQueueSource
	err := reader.QueryRowContext(ctx, `SELECT cluster_id,enabled,status,active_size,desired_size,hmr_state,
 COALESCE(active_operation_id,''),COALESCE(baseline_release,''),COALESCE(baseline_sha,''),config_json,
 COALESCE(control_plane_vip,''),COALESCE(edge_vip,''),
 (SELECT count(*) FROM engine.cluster_nodes WHERE membership_state='Active'),
 (SELECT count(*) FROM engine.cluster_nodes WHERE membership_state='Active' AND application_state<>'active'),
 (SELECT count(*) FROM engine.cluster_admissions WHERE cluster_id=c.cluster_id AND state IN ('Pending Quorum','Approved','Recovery Required'))
 FROM engine.cluster_state c WHERE id=1`).Scan(&source.ClusterID, &source.Enabled, &source.Status, &source.ActiveSize, &source.DesiredSize,
		&source.HMRState, &source.ActiveOperationID, &source.Release, &source.SHA, &source.ConfigJSON, &source.ControlVIP, &source.EdgeVIP,
		&source.Members, &source.Drained, &source.PendingAdmissions)
	if errors.Is(err, sql.ErrNoRows) {
		return source, errClusterConflict
	}
	if err != nil {
		return source, errClusterUnavailable
	}
	return source, nil
}

func (source clusterSSHQueueSource) validate() (int, error) {
	expected, err := currentReleaseAdmissionBatchSize(source.ActiveSize, source.DesiredSize, source.Status)
	if err != nil || source.Enabled != 1 || !clusterUUIDRE.MatchString(source.ClusterID) || source.ActiveOperationID != "" ||
		source.HMRState != "inactive" || source.Members != source.ActiveSize || source.Drained != 0 || source.PendingAdmissions != 0 ||
		(source.ActiveSize == 1 && source.Status != "Healthy") || !validClusterBaselineRelease(source.Release, source.SHA) ||
		len(source.ConfigJSON) > 64<<10 || clusterDatabaseRuntimeRequiresRecovery(source.ConfigJSON) {
		return 0, errClusterConflict
	}
	var config map[string]any
	if json.Unmarshal([]byte(source.ConfigJSON), &config) != nil || !clusterK3sRE.MatchString(cleanText(config["k3s_version"])) {
		return 0, errClusterConflict
	}
	// Recorded identity need not already be a published bootstrap release for
	// read-only inspection. Immutable publication is a later preparation gate.
	return expected, nil
}

// The caller reads source, binds newly generated operation/target IDs and seals
// credentials with Aegis before calling here. No plaintext credential is accepted.
func (s *postgresOperatorStore) queueClusterSSHInspections(ctx context.Context, actor string, source clusterSSHQueueSource, targets []clusterSSHPlannedTarget) (map[string]any, error) {
	expected, err := source.validate()
	if err != nil || strings.TrimSpace(actor) == "" || len(actor) > 128 {
		return nil, errClusterConflict
	}
	if err := validateClusterSSHPlannedTargets(targets, expected); err != nil {
		return nil, err
	}
	operationID := targets[0].Binding.OperationID
	for _, target := range targets {
		if target.Binding.ClusterID != source.ClusterID || target.Binding.OperationID != operationID ||
			target.Binding.Address == source.ControlVIP || target.Binding.Address == source.EdgeVIP {
			return nil, errClusterConflict
		}
	}
	payload := marshalClusterJSON(map[string]any{"inspection_only": true, "baseline_release": source.Release, "baseline_sha": source.SHA,
		"source_k3s_version": cleanText(parseClusterJSON(source.ConfigJSON)["k3s_version"]), "target_count": len(targets)})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, errClusterUnavailable
	}
	defer tx.Rollback()
	// All membership/queue mutations serialize on cluster state. Read again only
	// after its lock is held so a waiter cannot commit an earlier source snapshot.
	var present int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_state WHERE id=1 FOR UPDATE`).Scan(&present); err != nil {
		return nil, errClusterConflict
	}
	current, err := readClusterSSHQueueSource(ctx, tx)
	if err != nil || current != source {
		return nil, errClusterConflict
	}
	for _, target := range targets {
		var conflicts int
		if err := tx.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM engine.cluster_nodes WHERE membership_state<>'Removed' AND management_ip=$1) +
 (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE state NOT IN ('completed','failed','cancelled')
  AND (management_ip=$1 OR host_key_fingerprint=$2))`, target.Binding.Address, target.Binding.Fingerprint).Scan(&conflicts); err != nil {
			return nil, errClusterUnavailable
		}
		if conflicts != 0 {
			return nil, errClusterConflict
		}
	}
	var now int64
	if err := tx.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp()))::bigint`).Scan(&now); err != nil {
		return nil, errClusterUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO engine.cluster_operations
 (id,kind,state,current_step,requested_by,payload_json,created_at,updated_at)
 VALUES($1,'ssh_onboarding','queued','preflight',$2,$3,$4,$4)`, operationID, actor, payload, now); err != nil {
		return nil, errClusterConflict
	}
	if err := insertClusterSSHPlannedTargets(ctx, tx, operationID, source.ClusterID, targets); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_state SET active_operation_id=$1,updated_at=$2 WHERE id=1`, operationID, now); err != nil {
		return nil, errClusterUnavailable
	}
	details := map[string]any{"target_count": len(targets), "inspection_only": true}
	if insertClusterEvent(ctx, tx, operationID, "", source.ClusterID, "ssh_inspection_queued", "queued", "SSH target inspection queued.", details, now) != nil ||
		insertClusterAudit(ctx, tx, actor, "ssh_inspection_queued", operationID, "queued", details, now) != nil || tx.Commit() != nil {
		return nil, errClusterUnavailable
	}
	return map[string]any{"operation_id": operationID, "state": "queued", "current_step": "preflight", "attempt": int64(1), "target_count": len(targets)}, nil
}

type clusterSSHInspectionProgress struct {
	OperationID string                               `json:"operation_id"`
	State       string                               `json:"state"`
	Step        string                               `json:"current_step"`
	Attempt     int64                                `json:"attempt"`
	Targets     []clusterSSHInspectionProgressTarget `json:"targets"`
}

type clusterSSHInspectionProgressTarget struct {
	ID                  string                      `json:"id"`
	Address             string                      `json:"address"`
	Port                int                         `json:"port"`
	Fingerprint         string                      `json:"host_key_fingerprint"`
	State               string                      `json:"state"`
	Step                string                      `json:"current_step"`
	CredentialsReady    bool                        `json:"credentials_available"`
	InspectedAt         int64                       `json:"inspected_at"`
	InspectedAttempt    int64                       `json:"inspected_attempt"`
	InspectedGeneration int64                       `json:"inspected_generation"`
	Report              *clusterSSHInspectionReport `json:"report,omitempty"`
}

// Explicit public projection: never select ciphertext, username, envelope,
// operation payload, raw errors or an Aegis verification token. Historical
// report ownership remains visible even after credential cleanup/cancellation.
func (s *postgresOperatorStore) clusterSSHInspectionProgress(ctx context.Context, operationID string) (clusterSSHInspectionProgress, error) {
	fail := func(err error) (clusterSSHInspectionProgress, error) { return clusterSSHInspectionProgress{}, err }
	if !clusterUUIDRE.MatchString(operationID) {
		return fail(errClusterNotFound)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT o.state,o.current_step,o.attempt,t.id,t.management_ip,t.ssh_port,t.host_key_fingerprint,
 t.state,t.current_step,t.inspected_at,t.inspected_attempt,t.inspected_generation,t.inspection_json,
 (o.state IN ('queued','running','waiting') AND t.state IN ('queued','running','recovery_required')
 AND t.operation_attempt=o.attempt AND t.credential_state='available' AND EXISTS (SELECT 1 FROM engine.cluster_onboarding_credentials p JOIN engine.aegis_cipher_state a
 ON a.id=1 AND a.verification_token=p.aegis_generation WHERE p.target_id=t.id AND p.expires_at>extract(epoch FROM statement_timestamp())))
 FROM engine.cluster_operations o JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id
 WHERE o.id=$1 AND o.kind='ssh_onboarding' ORDER BY t.ordinal LIMIT 3`, operationID)
	if err != nil {
		return fail(errClusterUnavailable)
	}
	progress := clusterSSHInspectionProgress{OperationID: operationID}
	var reports []string
	for rows.Next() {
		var target clusterSSHInspectionProgressTarget
		var report string
		if err := rows.Scan(&progress.State, &progress.Step, &progress.Attempt, &target.ID, &target.Address, &target.Port, &target.Fingerprint,
			&target.State, &target.Step, &target.InspectedAt, &target.InspectedAttempt, &target.InspectedGeneration, &report, &target.CredentialsReady); err != nil {
			rows.Close()
			return fail(errClusterUnavailable)
		}
		progress.Targets = append(progress.Targets, target)
		reports = append(reports, report)
	}
	scanErr, closeErr := rows.Err(), rows.Close()
	if scanErr != nil || closeErr != nil || len(reports) > 2 {
		return fail(errClusterUnavailable)
	}
	if len(reports) == 0 {
		return fail(errClusterNotFound)
	}
	for i, raw := range reports {
		if raw == "{}" && progress.Targets[i].InspectedAt == 0 {
			continue
		}
		var report clusterSSHInspectionReport
		if len(raw) > 16<<10 || json.Unmarshal([]byte(raw), &report) != nil || report.valid() != nil {
			return fail(errClusterUnavailable)
		}
		canonical, err := json.Marshal(report)
		if err != nil || string(canonical) != raw {
			return fail(errClusterUnavailable)
		}
		progress.Targets[i].Report = &report
	}
	return progress, nil
}
