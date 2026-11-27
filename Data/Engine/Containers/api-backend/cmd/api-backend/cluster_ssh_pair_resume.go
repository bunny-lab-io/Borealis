package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// An explicit resume authorizes new credential envelopes for the two original
// target identities. It does not replace the preparation authorization or its
// confirmation, which bind the original installation inputs.
type clusterSSHPairResume struct {
	Boot         *clusterbootstrap.PairBootHandoff `json:"source_boot,omitempty"`
	Plan         string                            `json:"plan_sha256"`
	AuthorizedAt int64                             `json:"authorized_at"`
	Credentials  map[string]string                 `json:"credential_digests"`
}

// A new operator grant has one bounded lifetime, including queue and worker
// restart time. Automatic reconciliation continues to use the original grant.
func pairExecutionDeadline(payload string, confirmation clusterSSHPreparationConfirmation, maximum time.Duration, now time.Time) (time.Time, error) {
	start := confirmation.ConfirmedAt
	if pairRecoveryOffset(payload) == 2 {
		var record struct {
			Resume clusterSSHPairResume `json:"ssh_pair_resume"`
		}
		if json.Unmarshal([]byte(payload), &record) != nil || record.Resume.Plan != confirmation.PlanSHA256 || record.Resume.AuthorizedAt < start {
			return time.Time{}, errClusterConflict
		}
		start = record.Resume.AuthorizedAt
	}
	deadline := time.Unix(start, 0).Add(maximum)
	if start < 1 || start > now.Unix() || !now.Before(deadline) {
		return time.Time{}, errClusterConflict
	}
	return deadline, nil
}

func pairCredentialMatches(payload string, approved clusterSSHPreparationAuthorization, index int, digest string) bool {
	if index < 0 || index >= len(approved.Targets) || !validClusterSSHPreparationDigest(digest) {
		return false
	}
	t := approved.Targets[index]
	if pairRecoveryOffset(payload) != 2 {
		return digest == t.CredentialSHA256
	}
	var record struct {
		Resume clusterSSHPairResume `json:"ssh_pair_resume"`
	}
	plan, err := clusterSSHPreparationPlanDigest(approved)
	return err == nil && json.Unmarshal([]byte(payload), &record) == nil && record.Resume.Plan == plan &&
		len(approved.Targets) == 2 && len(record.Resume.Credentials) == 2 && record.Resume.Credentials[t.TargetID] == digest
}

type stoppedSSHPair struct {
	payload  string
	source   clusterSSHQueueSource
	proof    clusterSSHParentProof
	approval clusterSSHPreparationAuthorization
}

// No credential or lease is created by this read. The transaction compares the
// entire payload and source again after encryption has released its connection.
func (s *postgresOperatorStore) stoppedPair(ctx context.Context, id, plan string) (stoppedSSHPair, error) {
	var p stoppedSSHPair
	if clusterbootstrap.CheckPairExecutorPlan(id, plan) != nil {
		return p, errClusterConflict
	}
	var holder string
	var attempt int64
	if err := s.db.QueryRowContext(ctx, `SELECT o.payload_json,o.attempt,l.holder FROM engine.cluster_operations o,
 engine.cluster_state c,engine.cluster_application_leases l WHERE o.id=$1 AND o.kind='ssh_onboarding'
 AND o.state='running' AND o.current_step=$2 AND o.error_text=$3 AND c.id=1 AND c.active_operation_id=o.id
 AND octet_length(o.payload_json)<=262144 AND NOT (o.payload_json::jsonb ?| ARRAY['ssh_pair_resume','ssh_pair_reconciliation','ssh_preparation'])
 AND l.name=$4 AND l.expires_at>extract(epoch FROM clock_timestamp())`, id, clusterSSHPreparationOperationStep, clusterSSHPairStopReason, clusterControllerLeaseName).Scan(&p.payload, &attempt, &holder); err != nil {
		return p, errClusterConflict
	}
	var err error
	p.source, err = readClusterSSHQueueSource(ctx, s.db)
	if err != nil || p.source.ActiveOperationID != id || p.source.ActiveSize != 1 {
		return p, errClusterConflict
	}
	anchor := clusterSSHTargetLease{OperationID: id, OperationAttempt: attempt, ControllerHolder: holder}
	p.proof, _, err = parseClusterSSHPreparationInspection([]byte(p.payload), anchor, clusterBaselineExpected(p.source.Release, p.source.SHA))
	if err != nil {
		return p, errClusterConflict
	}
	var report *clusterSSHQualificationReport
	p.approval, report, err = parseClusterSSHPreparationAuthorization(p.payload, p.proof, p.source)
	if err != nil || report.Version != 2 || !report.Ready || len(p.approval.Targets) != 2 {
		return p, errClusterConflict
	}
	digest, err := clusterSSHPreparationPlanDigest(p.approval)
	if err != nil || digest != plan {
		return p, errClusterConflict
	}
	return p, nil
}

func grantPairRecovery(r *http.Request, auth *authService, store *postgresOperatorStore, sealer clusterSSHCredentialSealer, pin clusterbootstrap.PairExecutorPin) error {
	admin, failure := requireAdmin(r.Context(), auth, r)
	if failure != nil {
		return fmt.Errorf("existing WebUI administrator session rejected (HTTP %d)", failure.status)
	}
	id, submitted, err := readClusterSSHSubmissionFor(r, "RESUME ENGINE NODES")
	if err != nil || id != pin.Operation || len(submitted) != 2 {
		return fmt.Errorf("expected the reserved pair and RESUME ENGINE NODES confirmation")
	}
	defer func() {
		for i := range submitted {
			submitted[i].envelope = clusterSSHCredentialEnvelope{}
		}
	}()
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	p, err := store.stoppedPair(ctx, id, pin.Plan)
	if err != nil {
		return fmt.Errorf("stopped pair, original controller or frozen plan: %w", err)
	}
	var targets []clusterSSHPlannedTarget
	for i, t := range submitted {
		original := p.proof.Cohort.Targets[i]
		if pin.Targets[i] != original.Binding.TargetID || t.target.Address != original.Binding.Address || t.target.Port != original.Binding.Port || t.key.Fingerprint != original.Key.Fingerprint || t.key.Algorithm != original.Key.Algorithm || !bytes.Equal(t.key.PublicKey, original.Key.PublicKey) {
			return fmt.Errorf("target %d: approved SSH identity differs", i+1)
		}
		t.envelope.Binding = original.Binding
		journal := "absent"
		if i == 0 {
			journal = pin.Boot.Journal
		}
		if err := verifyStoppedPairTarget(ctx, t, original.Report.MachineID, original.Report.BootID, journal); err != nil {
			return fmt.Errorf("target %d: SSH access, quiescence or retained journal: %w", i+1, err)
		}
		sealed, err := sealer.sealClusterSSHCredentials(ctx, t.envelope)
		if err != nil {
			return fmt.Errorf("target %d: fresh credential sealing rejected", i+1)
		}
		targets = append(targets, clusterSSHPlannedTarget{original.Binding, t.key, base64.StdEncoding.EncodeToString(t.key.PublicKey), sealed})
	}
	return store.resumeStoppedPair(ctx, admin.Username, pin.Plan, p, targets)
}

func (s *postgresOperatorStore) resumeStoppedPair(ctx context.Context, actor, plan string, p stoppedSSHPair, targets []clusterSSHPlannedTarget) error {
	if err := validateClusterSSHPlannedTargets(targets, 2); err != nil {
		return err
	}
	approved, err := clusterSSHPreparationPlanDigest(p.approval)
	if err != nil || approved != plan || actor == "" || len(actor) > 128 || len(p.proof.Cohort.Targets) != 2 || len(p.approval.Targets) != 2 {
		return errClusterConflict
	}
	resume := clusterSSHPairResume{Plan: plan, Credentials: map[string]string{}}
	if clusterbootstrap.IsPairExecutor() {
		pin, err := clusterbootstrap.RecoveryPair()
		if err != nil || pin.Operation != p.approval.OperationID || pin.Plan != plan || pin.Release != p.source.Release || len(p.proof.Source.Members) != 1 || p.proof.Source.Members[0].BootID != pin.Boot.Before || p.proof.Source.Members[0].NodeUID != pin.Boot.SourceUID {
			return errClusterConflict
		}
		current, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if err != nil || strings.TrimSpace(string(current)) != pin.Boot.After {
			return errClusterConflict
		}
		for i, t := range targets {
			if pin.Targets[i] != t.Binding.TargetID || p.approval.Targets[i].QualificationGeneration+3 != pin.Generation {
				return errClusterConflict
			}
		}
		resume.Boot = &pin.Boot
	}
	for i, t := range targets {
		if t.Binding != p.proof.Cohort.Targets[i].Binding || t.Binding.TargetID != p.approval.Targets[i].TargetID {
			return errClusterConflict
		}
		digest, err := clusterSSHPreparationDigest([]string{t.Sealed.generation, t.Sealed.ciphertext})
		if err != nil || digest == p.approval.Targets[i].CredentialSHA256 {
			return errClusterConflict
		}
		resume.Credentials[t.Binding.TargetID] = digest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errClusterUnavailable
	}
	defer tx.Rollback()
	id := p.approval.OperationID
	var one int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_application_leases WHERE name=$1 AND holder=$2 AND expires_at>extract(epoch FROM clock_timestamp()) FOR SHARE`, clusterControllerLeaseName, p.approval.ControllerHolder).Scan(&one) != nil ||
		tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_state WHERE id=1 AND active_operation_id=$1 FOR UPDATE`, id).Scan(&one) != nil ||
		tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations WHERE id=$1 AND payload_json=$2 AND state='running' AND current_step=$3 AND error_text=$4 FOR UPDATE`, id, p.payload, clusterSSHPreparationOperationStep, clusterSSHPairStopReason).Scan(&one) != nil {
		return errClusterConflict
	}
	current, err := readClusterSSHQueueSource(ctx, tx)
	if err != nil || current != p.source {
		return errClusterConflict
	}
	if resume.Boot != nil {
		var system string
		if err := tx.QueryRowContext(ctx, "SELECT system_identifier::text FROM pg_control_system() WHERE NOT pg_is_in_recovery()").Scan(&system); err != nil || system != resume.Boot.Database {
			return errClusterConflict
		}
	}
	var generation string
	if tx.QueryRowContext(ctx, `SELECT verification_token FROM engine.aegis_cipher_state WHERE id=1 FOR SHARE`).Scan(&generation) != nil {
		return errClusterSSHCredentials
	}
	for i, t := range targets {
		if generation != t.Sealed.generation {
			return errClusterSSHCredentials
		}
		result, err := tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state='queued',current_step='pair_reconcile',
 credential_state='available',updated_at=extract(epoch FROM clock_timestamp())::bigint
 WHERE id=$1 AND operation_id=$2 AND cluster_id=$3 AND operation_attempt=$4 AND ordinal=$5 AND state='running' AND current_step='stage_source'
 AND credential_state='required' AND lease_holder='' AND lease_expires_at=0 AND lease_generation=$6 AND management_ip=$7 AND ssh_port=$8
 AND host_key_fingerprint=$9 AND host_key_algorithm=$10 AND host_key_base64=$11`, t.Binding.TargetID, id, t.Binding.ClusterID, p.approval.Attempt, i+1,
			p.approval.Targets[i].QualificationGeneration+2, t.Binding.Address, t.Binding.Port, t.Key.Fingerprint, t.Key.Algorithm, t.KeyBase64)
		if err != nil {
			return errClusterConflict
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return errClusterConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO engine.cluster_onboarding_credentials(target_id,aegis_generation,ciphertext,created_at,expires_at)
 SELECT $1,$2,$3,now,now+$4 FROM (SELECT floor(extract(epoch FROM clock_timestamp()))::bigint AS now) t`, t.Binding.TargetID, generation, t.Sealed.ciphertext, clusterSSHCredentialLifetimeSeconds)
		if err != nil {
			return errClusterSSHCredentials
		}
	}
	var now int64
	if tx.QueryRowContext(ctx, `WITH grant_time AS MATERIALIZED (SELECT floor(extract(epoch FROM clock_timestamp()))::bigint AS authorized_at)
 UPDATE engine.cluster_operations o SET error_text=NULL,payload_json=(o.payload_json::jsonb ||
 jsonb_build_object('ssh_pair_resume',$2::jsonb || jsonb_build_object('authorized_at',g.authorized_at),'ssh_pair_reconciliation',2))::text,updated_at=g.authorized_at FROM grant_time g
 WHERE o.id=$1 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=2
 AND EXISTS(SELECT 1 FROM engine.cluster_application_leases WHERE name=$3 AND holder=$4 AND expires_at>extract(epoch FROM clock_timestamp()))
 RETURNING updated_at`, id, marshalClusterJSON(resume), clusterControllerLeaseName, p.approval.ControllerHolder).Scan(&now) != nil {
		return errClusterConflict
	}
	details := map[string]any{"plan_sha256": plan, "attempt": p.approval.Attempt}
	if insertClusterEvent(ctx, tx, id, "", p.source.ClusterID, "ssh_pair_resumed", "running", "Operator authorized fresh access for retained journal reconciliation.", details, now) != nil ||
		insertClusterAudit(ctx, tx, actor, "ssh_pair_resumed", id, "running", details, now) != nil {
		return errClusterUnavailable
	}
	return tx.Commit()
}

// Make a copy for either live source observation or original input hashing.
// The archived plan can only be used with the separately confirmed exact boot.
func pairSourceBoot(payload string, src clusterSSHSourceCohort, forObservation bool) (clusterSSHSourceCohort, error) {
	if !clusterbootstrap.IsPairExecutor() {
		return src, nil
	}
	pin, err := clusterbootstrap.RecoveryPair()
	if err != nil {
		return src, err
	}
	return projectPairSourceBoot(pin, payload, src, forObservation)
}

func projectPairSourceBoot(pin clusterbootstrap.PairExecutorPin, payload string, src clusterSSHSourceCohort, forObservation bool) (clusterSSHSourceCohort, error) {
	var record struct {
		Resume clusterSSHPairResume `json:"ssh_pair_resume"`
	}
	if json.Unmarshal([]byte(payload), &record) != nil || record.Resume.Boot == nil || *record.Resume.Boot != pin.Boot || record.Resume.Plan != pin.Plan || len(src.Members) != 1 {
		return src, errClusterConflict
	}
	from, to := pin.Boot.Before, pin.Boot.After
	if !forObservation {
		from, to = to, from
	}
	m := src.Members[0]
	if m.NodeUID != pin.Boot.SourceUID || m.BootID != from {
		return src, errClusterConflict
	}
	src.Members = slices.Clone(src.Members)
	src.Members[0].BootID = to
	return src, nil
}

func verifyStoppedPairTarget(ctx context.Context, t clusterSSHSubmissionTarget, machine, boot, journal string) error {
	auth, sudo, err := clusterSSHNetworkCredential(t.envelope)
	if err != nil {
		return errClusterSSHCredentials
	}
	defer auth.Destroy()
	defer clear(sudo)
	client, err := (clusterremote.Transport{}).Connect(ctx, t.target, t.key, auth)
	if err != nil {
		return errClusterSSHCredentials
	}
	defer client.Close()
	if err := client.CheckStoppedPairTarget(ctx, sudo, machine, boot, journal); err != nil {
		return errClusterConflict
	}
	return nil
}
