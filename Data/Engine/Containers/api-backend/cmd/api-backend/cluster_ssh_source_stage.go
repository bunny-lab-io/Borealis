package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const (
	clusterSSHStagePending   = "Staging the inspected release on the original controller."
	clusterSSHStageReady     = "Release staged on the original controller. Read-only host qualification is in progress."
	clusterSSHStageFailed    = "The original controller no longer holds the staged release. Start a fresh inspection."
	clusterSSHStageDirectory = "inspection-release"
)

// Only the controller reconciliation loop owns this directory. The qualification
// worker observes the phase in PostgreSQL and never receives a path or handle.
type clusterSSHSourceStage struct {
	clusterSSHPreparationArtifactBytes
	binding   clusterPreparationReleaseRequest
	postgres  string
	directory string
}

func (v *clusterSSHSourceStage) Close() error {
	var err error
	if v.clusterSSHPreparationArtifactBytes != nil {
		err = v.clusterSSHPreparationArtifactBytes.Close()
		v.clusterSSHPreparationArtifactBytes = nil
	}
	return errors.Join(err, os.RemoveAll(v.directory))
}

func (c *clusterController) takeSourceStage(q clusterPreparationReleaseRequest, postgres string) (clusterSSHPreparationArtifactBytes, error) {
	stage := c.sourceStage
	if stage == nil || stage.binding != q || stage.postgres != postgres || stage.clusterSSHPreparationArtifactBytes == nil {
		return nil, errClusterConflict
	}
	c.sourceStage = nil
	return stage, nil
}

func (c *clusterController) discardSourceStage() error {
	if c.sourceStage == nil {
		return nil
	}
	err := c.sourceStage.Close()
	if err == nil {
		c.sourceStage = nil
	}
	return err
}

// Read-only authority ends on cancellation, original inspection expiry, target
// custody loss or a changed source baseline. A completed report has its own
// shorter lifetime. The ready marker cannot authorize further release downloads.
const clusterSSHStageAuthority = `
 o.id=$1 AND o.attempt=$2 AND o.kind='ssh_onboarding' AND o.state='waiting' AND o.current_step='qualify_ssh_targets'
 AND s.id=1 AND s.active_operation_id=o.id AND s.baseline_release=$3 AND s.baseline_sha=$4
 AND l.name=$5 AND l.holder=$6 AND l.expires_at>extract(epoch FROM clock_timestamp())
 AND o.payload_json::jsonb #>> '{ssh_inspection,proof,cohort,ControllerHolder}'=$6
 AND o.payload_json::jsonb #>> '{ssh_inspection,sha256}'=$7
 AND o.payload_json::jsonb->'ssh_source_staging_required'='true'::jsonb
 AND NOT(o.payload_json::jsonb ? 'ssh_preparation_authorization')
 AND o.payload_json::jsonb->>'baseline_release'=$3 AND o.payload_json::jsonb->>'baseline_sha'=$4
 AND (o.payload_json::jsonb->>'target_count')::int IN (1,2)
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=(o.payload_json::jsonb->>'target_count')::int
 AND NOT EXISTS (SELECT 1 FROM engine.cluster_onboarding_targets t
 LEFT JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 LEFT JOIN engine.aegis_cipher_state a ON a.id=1 AND a.verification_token=p.aegis_generation
 WHERE t.operation_id=o.id AND (t.operation_attempt<>o.attempt OR t.inspected_attempt<>o.attempt
 OR t.state NOT IN ('queued','running') OR t.current_step NOT IN ('inspection_complete','qualify','qualification_complete') OR t.credential_state<>'available'
 OR t.inspected_at<=extract(epoch FROM clock_timestamp())-$8 OR t.inspected_at>extract(epoch FROM clock_timestamp())
 OR a.id IS NULL OR p.expires_at<=extract(epoch FROM clock_timestamp())
 OR NOT EXISTS (SELECT 1 FROM jsonb_array_elements(o.payload_json::jsonb #> '{ssh_inspection,proof,cohort,Targets}') inspected
 WHERE inspected #>> '{Binding,target_id}'=t.id AND inspected #>> '{Binding,host_key_fingerprint}'=t.host_key_fingerprint
 AND (inspected->>'InspectedAt')::bigint=t.inspected_at AND (inspected->>'Generation')::bigint=t.inspected_generation)))
 AND (NOT (o.payload_json::jsonb ? 'ssh_qualification') OR
 (o.payload_json::jsonb #>> '{ssh_qualification,observed_at}')::bigint BETWEEN floor(extract(epoch FROM clock_timestamp()))::bigint-$9+1 AND floor(extract(epoch FROM clock_timestamp()))::bigint)`

func stageAuthorityArgs(q clusterPreparationReleaseRequest) []any {
	return []any{q.Operation, q.Attempt, q.Expected.Release, q.Expected.SourceSHA, clusterControllerLeaseName, q.Holder, q.Inspection, clusterSSHInspectionLifetimeSeconds, int64(clusterSSHFreshEvidenceSeconds)}
}

func (s *postgresOperatorStore) sourceStageStatus(parent context.Context, q clusterPreparationReleaseRequest) (string, error) {
	if s == nil || s.db == nil || q.Expected.Repository != clusterGitHubRepo() || q.Expected.AllowQualification != strings.Contains(q.Expected.Release, "-rc.") || q.Expected.Validate() != nil {
		return "", errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT CASE WHEN o.payload_json::jsonb ? 'ssh_qualification' OR o.payload_json::jsonb ? 'ssh_preparation_request' THEN '' ELSE COALESCE(o.error_text,'') END
 FROM engine.cluster_operations o,engine.cluster_state s,engine.cluster_application_leases l WHERE `+clusterSSHStageAuthority, stageAuthorityArgs(q)...).Scan(&status)
	return status, err
}

func (c *clusterController) setStageStatus(ctx context.Context, q clusterPreparationReleaseRequest, from, to string) error {
	bound, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	event := "ssh_source_stage_blocked"
	if to == clusterSSHStageReady {
		event = "ssh_source_stage_ready"
	}
	res, err := c.store.db.ExecContext(bound, `WITH updated AS (UPDATE engine.cluster_operations o SET error_text=$11,updated_at=floor(extract(epoch FROM clock_timestamp()))
 FROM engine.cluster_state s,engine.cluster_application_leases l WHERE `+clusterSSHStageAuthority+` AND o.error_text=$10
 AND NOT(o.payload_json::jsonb ?| ARRAY['ssh_qualification','ssh_preparation_request'])
 RETURNING o.id,o.attempt,o.updated_at,s.cluster_id)
 INSERT INTO engine.cluster_operation_events(operation_id,cluster_id,event_type,state,message,details_json,created_at)
 SELECT id,cluster_id,$12,'waiting',$11,json_build_object('attempt',attempt)::text,updated_at FROM updated`, append(stageAuthorityArgs(q), from, to, event)...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return errClusterConflict
	}
	return nil
}

func (c *clusterController) pruneSourceStage(ctx context.Context) error {
	if c.sourceStage != nil {
		status, err := c.store.sourceStageStatus(ctx, c.sourceStage.binding)
		if err != nil || (status != clusterSSHStageReady && status != "") {
			return c.discardSourceStage()
		}
	}
	return nil
}

func (s *postgresOperatorStore) inspectionReleaseReady(ctx context.Context, work clusterSSHQualificationWork) (bool, error) {
	var required bool
	if clusterBootstrapObject([]byte(work.Payload), map[string]any{"ssh_source_staging_required": &required}) != nil {
		return false, errClusterConflict
	}
	if !required {
		return true, nil
	}
	digest, err := clusterSSHPreparationDigest(work.Proof)
	if err != nil {
		return false, err
	}
	q := clusterPreparationReleaseRequest{Operation: work.Proof.Cohort.OperationID, Attempt: work.Proof.Cohort.Attempt, Holder: work.Proof.Cohort.ControllerHolder, Inspection: digest, Expected: work.Baseline}
	status, err := s.sourceStageStatus(ctx, q)
	return status == clusterSSHStageReady, err
}

func (c *clusterController) stageInspectionRelease(ctx context.Context, op clusterControllerOperation) (result error) {
	var payload []byte
	if err := c.store.db.QueryRowContext(ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1 AND attempt=$2 AND state='waiting' AND current_step=$3 AND octet_length(payload_json)<=262144`, op.ID, op.Attempt, clusterSSHQualificationStep).Scan(&payload); err != nil {
		return err
	}
	release := cleanText(op.Payload["baseline_release"])
	expected := clusterBaselineExpected(release, cleanText(op.Payload["baseline_sha"]))
	proof, version, err := parseClusterSSHPreparationInspection(payload, clusterSSHTargetLease{OperationID: op.ID, OperationAttempt: op.Attempt, ControllerHolder: c.holder}, expected)
	if err != nil || version != clusterbootstrap.K3sPins().Version {
		return errClusterConflict
	}
	inspection, err := clusterSSHPreparationDigest(proof)
	if err != nil {
		return err
	}
	q := clusterPreparationReleaseRequest{Operation: op.ID, Attempt: op.Attempt, Holder: c.holder, Expected: expected, Inspection: inspection}
	status, err := c.store.sourceStageStatus(ctx, q)
	if err != nil {
		return err
	}
	switch status {
	case clusterSSHStageReady:
		if c.sourceStage == nil || c.sourceStage.binding != q {
			return c.setStageStatus(ctx, q, status, clusterSSHStageFailed)
		}
		return nil
	case clusterSSHStagePending:
	default:
		return nil
	}
	phase := "controller setup"
	var authorityLost atomic.Bool
	defer func() {
		if result != nil {
			message := clusterSSHReleaseStageFailure{phase, result}.Error()
			if authorityLost.Load() {
				message = "The controller could not confirm source staging authority during " + phase + ". Start a fresh inspection."
			}
			log.Printf("cluster operation %s: %s", op.ID, message)
			result = errors.Join(result, c.setStageStatus(ctx, q, clusterSSHStagePending, message))
		}
	}()
	runner, ok := c.runner.(*kubernetesClusterStepRunner)
	if !ok || runner == nil || runner.kube == nil || runner.namespace != "borealis" || runner.controllerHolder != c.holder || c.sourceStage != nil {
		return errClusterConflict
	}
	client, err := newClusterSSHPreparationCleanBrokerClientFromEnv()
	if err != nil {
		return err
	}
	bound, cancel := context.WithDeadline(ctx, (clusterSSHQualificationWork{Proof: proof}).deadline())
	defer cancel()
	bound, err = clusterBoundReleaseContext(bound, client, expected, q)
	if err != nil {
		return err
	}
	phase = "scratch directory setup"
	dir := filepath.Join(clusterSSHScratchParent(), clusterSSHStageDirectory)
	if os.Getenv("BOREALIS_CLUSTER_SCRATCH_DIR") == "" {
		dir, err = os.MkdirTemp(os.TempDir(), "borealis-inspection-release-")
	} else {
		err = os.Mkdir(dir, 0700)
	}
	if err != nil {
		return err
	}
	stage := &clusterSSHSourceStage{binding: q, directory: dir}
	defer func() {
		if result != nil {
			result = errors.Join(result, stage.Close())
		}
	}()
	check := func(ctx context.Context) error {
		if !c.store.preparationReleaseCurrent(ctx, q) {
			// A cancelled reader during cleanup cannot establish that ownership changed.
			if ctx.Err() == nil {
				authorityLost.Store(true)
			}
			return errClusterConflict
		}
		return nil
	}
	phase = "source profile observation"
	err = runClusterSSHArtifactPreparationScope(bound, 5*time.Second, check, func(ctx context.Context) error {
		source, err := readClusterSSHPreparationArtifactSource(ctx, proof.Source, runner.kube.getClusterSSHStorageJSON, check, nil)
		if err != nil {
			return err
		}
		phase = "release acquisition"
		suite, err := preparationCohortPackageSuite(proof.Cohort)
		if err != nil {
			return err
		}
		stage.postgres = source.Postgres
		stage.clusterSSHPreparationArtifactBytes, err = acquireClusterSSHPreparationArtifacts(ctx, dir, expected, version, source.Postgres, suite, check)
		var failure clusterSSHReleaseStageFailure
		if errors.As(err, &failure) {
			phase = failure.phase
		}
		return err
	})
	if err != nil {
		return err
	}
	phase = "publication of the staged release"
	if err = c.setStageStatus(bound, q, status, clusterSSHStageReady); err != nil {
		return err
	}
	c.sourceStage = stage
	return nil
}

// The directory is exclusively mounted in the controller Pod. Remove abandoned
// attempts on container restart. Standalone runs must not sweep shared /tmp.
func initializeSSHSourceScratch() error {
	root := os.Getenv("BOREALIS_CLUSTER_SCRATCH_DIR")
	if root == "" {
		return nil
	}
	path := filepath.Join(root, clusterSSHStageDirectory)
	return os.RemoveAll(path)
}
