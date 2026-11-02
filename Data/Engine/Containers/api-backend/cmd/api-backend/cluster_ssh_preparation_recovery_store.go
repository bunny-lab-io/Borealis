package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
)

// Read and parse outside a transaction. Historical timestamps are retained, not
// renewed or interpreted as fresh inspection. Recovery does not authorize any
// native effect; a future consumer must supply fresh phase-specific proof.
func (c *clusterController) readClusterSSHPreparationRecovery(ctx context.Context, operation clusterControllerOperation) (clusterSSHPreparationRecoverySnapshot, error) {
	fail := func() (clusterSSHPreparationRecoverySnapshot, error) {
		return clusterSSHPreparationRecoverySnapshot{}, errClusterConflict
	}
	if c == nil || c.store == nil || c.store.db == nil || ctx.Err() != nil || !clusterUUIDRE.MatchString(operation.ID) ||
		operation.Kind != "ssh_onboarding" || operation.State != "running" || operation.Attempt < 1 || c.holder == "" || len(c.holder) > 1024 ||
		(operation.CurrentStep != clusterSSHPreparationOperationStep && operation.CurrentStep != clusterSSHPreparationRecoveryOperationStep) {
		return fail()
	}
	snapshot := clusterSSHPreparationRecoverySnapshot{step: operation.CurrentStep}
	work := &snapshot.work
	if c.store.db.QueryRowContext(ctx, `SELECT o.payload_json,c.baseline_release,c.baseline_sha
 FROM engine.cluster_operations o,engine.cluster_state c,engine.cluster_application_leases l
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$2 AND o.attempt=$3
 AND octet_length(o.payload_json)<=262144 AND c.id=1 AND c.active_operation_id=o.id
 AND l.name=$4 AND l.holder=$5 AND l.expires_at>extract(epoch FROM clock_timestamp())`, operation.ID,
		operation.CurrentStep, operation.Attempt, clusterControllerLeaseName, c.holder).
		Scan(&snapshot.payload, &work.Baseline.Release, &work.Baseline.SourceSHA) != nil {
		return fail()
	}
	work.Baseline.Repository = clusterGitHubRepo()
	work.Baseline.AllowQualification = strings.Contains(work.Baseline.Release, "-rc.")
	base, recovery, err := clusterSSHPreparationRecoveryPayload(snapshot.payload)
	if err != nil || work.Baseline.Validate() != nil || (len(recovery) != 0) != (snapshot.step == clusterSSHPreparationRecoveryOperationStep) {
		return fail()
	}
	// Only the recovery field is stripped. The complete original authorization
	// parser still rejects preparation completion and legacy identity recovery.
	work.Payload = string(base)
	anchor := clusterSSHTargetLease{OperationID: operation.ID, OperationAttempt: operation.Attempt, ControllerHolder: c.holder,
		OperationKind: "ssh_onboarding", OperationStep: clusterSSHPreparationOperationStep, Step: "stage_source"}
	var version string
	work.Proof, version, err = parseClusterSSHPreparationInspection(base, anchor, work.Baseline)
	if err != nil {
		return fail()
	}
	work.Source, err = readClusterSSHQueueSource(ctx, c.store.db)
	if err != nil {
		return fail()
	}
	source := work.Source
	if source.ActiveOperationID != operation.ID || source.ClusterID != work.Proof.Source.ClusterID ||
		source.ActiveSize != work.Proof.Source.ActiveSize || source.DesiredSize != work.Proof.Source.DesiredSize ||
		source.Status != work.Proof.Source.Status || source.HMRState != work.Proof.Source.HMRState ||
		source.ControlVIP != work.Proof.Source.ControlPlaneVIP || source.EdgeVIP != work.Proof.Source.EdgeVIP ||
		source.Release != work.Baseline.Release || source.SHA != work.Baseline.SourceSHA {
		return fail()
	}
	source.ActiveOperationID = ""
	count, err := source.validate()
	var configured string
	if err != nil || count != len(work.Proof.Cohort.Targets) ||
		clusterBootstrapObject([]byte(source.ConfigJSON), map[string]any{"k3s_version": &configured}) != nil || configured != version {
		return fail()
	}
	work.Authorization, _, err = parseClusterSSHPreparationAuthorization(work.Payload, work.Proof, work.Source)
	if err != nil {
		return fail()
	}
	var confirmationRaw json.RawMessage
	if clusterBootstrapObject(base, map[string]any{"ssh_preparation_confirmation": &confirmationRaw}) != nil {
		return fail()
	}
	confirmation, err := parseClusterSSHPreparationConfirmation(confirmationRaw, work.Authorization, work.Proof.Source)
	if err != nil {
		return fail()
	}
	work.IdentitySource = confirmation.IdentitySource
	for i, target := range work.Proof.Cohort.Targets {
		credential := sealedClusterSSHCredentials{binding: target.Binding}
		if c.store.db.QueryRowContext(ctx, `SELECT aegis_generation,ciphertext FROM engine.cluster_onboarding_credentials
 WHERE target_id=$1 AND octet_length(aegis_generation) BETWEEN 1 AND 16384 AND octet_length(ciphertext) BETWEEN 1 AND 262144`, target.Binding.TargetID).
			Scan(&credential.generation, &credential.ciphertext) != nil || !strings.HasPrefix(credential.ciphertext, aegisEnvelopePrefix) {
			return fail()
		}
		digest, err := clusterSSHPreparationDigest([]string{credential.generation, credential.ciphertext})
		if err != nil || digest != work.Authorization.Targets[i].CredentialSHA256 {
			return fail()
		}
		snapshot.credentials = append(snapshot.credentials, credential)
	}
	if len(recovery) != 0 {
		snapshot.record, err = parseClusterSSHPreparationRecovery(snapshot.payload, *work)
		if err != nil {
			return fail()
		}
		return snapshot, nil
	}
	plan, err := clusterSSHPreparationPlanDigest(work.Authorization)
	if err != nil {
		return fail()
	}
	snapshot.record = clusterSSHPreparationRecoveryRecord{Version: 1, ID: newClusterUUID(), OperationID: operation.ID,
		Attempt: operation.Attempt, ControllerHolder: c.holder, PlanSHA256: plan, PayloadSHA256: clusterSSHPreparationRecoveryPayloadDigest(base)}
	for _, target := range work.Proof.Cohort.Targets {
		prior := clusterSSHPreparationRecoveryTarget{TargetID: target.Binding.TargetID}
		if c.store.db.QueryRowContext(ctx, `SELECT lease_holder,lease_generation,lease_expires_at FROM engine.cluster_onboarding_targets
 WHERE id=$1 AND operation_id=$2 AND state='running' AND current_step='stage_source'
 AND lease_expires_at>0 AND lease_expires_at<=extract(epoch FROM clock_timestamp())`, target.Binding.TargetID, operation.ID).
			Scan(&prior.PreviousHolder, &prior.PreviousGeneration, &prior.PreviousExpiresAt) != nil {
			return fail()
		}
		prior.Generation = prior.PreviousGeneration + 1
		snapshot.record.Targets = append(snapshot.record.Targets, prior)
	}
	if !snapshot.record.valid(*work, base) {
		return fail()
	}
	return snapshot, nil
}

// Reservation is callable but has no production caller. All initial claims
// must be expired and still at stage_source; partial settlement is unsupported.
// Retain old holders/generations/expiries in both target rows and the immutable
// record. Expiration alone never proves the old producer stopped.
func (c *clusterController) reserveClusterSSHPreparationRecovery(ctx context.Context, operation clusterControllerOperation) (clusterSSHPreparationRecoveryRecord, error) {
	snapshot, err := c.readClusterSSHPreparationRecovery(ctx, operation)
	if err != nil || snapshot.step != clusterSSHPreparationOperationStep {
		return clusterSSHPreparationRecoveryRecord{}, errClusterConflict
	}
	if err := c.commitClusterSSHPreparationRecovery(ctx, snapshot, ""); err != nil {
		return clusterSSHPreparationRecoveryRecord{}, err
	}
	return snapshot.record, nil
}

// A separate one-shot claim only reserves future reconciliation. It cannot be
// supplied to existing preparation/native workers. A lost reply never permits
// reclaim: the retained payload is readable, but its target phase is consumed.
func (c *clusterController) claimClusterSSHPreparationRecovery(ctx context.Context, operation clusterControllerOperation, reservationID, holder string) (clusterSSHPreparationRecoveryClaim, error) {
	fail := func() (clusterSSHPreparationRecoveryClaim, error) {
		return clusterSSHPreparationRecoveryClaim{}, errClusterConflict
	}
	if !clusterUUIDRE.MatchString(holder) || holder == "00000000-0000-0000-0000-000000000000" {
		return fail()
	}
	snapshot, err := c.readClusterSSHPreparationRecovery(ctx, operation)
	if err != nil || snapshot.step != clusterSSHPreparationRecoveryOperationStep || snapshot.record.ID != reservationID {
		return fail()
	}
	for _, prior := range snapshot.record.Targets {
		if holder == prior.PreviousHolder {
			return fail()
		}
	}
	expiries, err := c.transitionClusterSSHPreparationRecovery(ctx, snapshot, holder)
	if err != nil {
		return fail()
	}
	claim := clusterSSHPreparationRecoveryClaim{Reservation: snapshot.record, Baseline: snapshot.work.Baseline, payload: snapshot.payload,
		custody: &clusterSSHPreparationRecoveryCustody{assigned: expiries}}
	for i, prior := range snapshot.record.Targets {
		claim.Claims = append(claim.Claims, clusterSSHPreparationRecoveryTargetClaim{Previous: prior, Sealed: snapshot.credentials[i],
			Lease: clusterSSHTargetLease{OperationID: operation.ID, OperationAttempt: operation.Attempt, OperationKind: "ssh_onboarding",
				OperationStep: clusterSSHPreparationRecoveryOperationStep, ControllerHolder: c.holder, TargetID: prior.TargetID,
				Step: clusterSSHPreparationRecoveryClaimStep, Holder: holder, Generation: prior.Generation}})
	}
	return claim, nil
}

// All payload shaping, hashing and report encoding precede SQL acquisition.
// Private snapshots are derived by the reader; the exact raw payload and every
// source/target/credential byte are compared again under the original lock order.
func (c *clusterController) commitClusterSSHPreparationRecovery(ctx context.Context, snapshot clusterSSHPreparationRecoverySnapshot, holder string) error {
	_, err := c.transitionClusterSSHPreparationRecovery(ctx, snapshot, holder)
	return err
}

func (c *clusterController) transitionClusterSSHPreparationRecovery(ctx context.Context, snapshot clusterSSHPreparationRecoverySnapshot, holder string) (map[string]int64, error) {
	if c == nil || c.store == nil || c.store.db == nil || ctx.Err() != nil || snapshot.record.ControllerHolder != c.holder {
		return nil, errClusterConflict
	}
	base, recovery, err := clusterSSHPreparationRecoveryPayload(snapshot.payload)
	claiming := holder != ""
	if err != nil || !snapshot.record.valid(snapshot.work, base) || len(snapshot.credentials) != len(snapshot.record.Targets) ||
		(claiming && snapshot.step != clusterSSHPreparationRecoveryOperationStep) || (!claiming && snapshot.step != clusterSSHPreparationOperationStep) ||
		(len(recovery) != 0) != claiming {
		return nil, errClusterConflict
	}
	anchor := clusterSSHTargetLease{OperationID: snapshot.record.OperationID, OperationAttempt: snapshot.record.Attempt,
		ControllerHolder: c.holder, OperationKind: "ssh_onboarding", OperationStep: clusterSSHPreparationOperationStep, Step: "stage_source"}
	proof, _, err := parseClusterSSHPreparationInspection(base, anchor, snapshot.work.Baseline)
	if err != nil || !reflect.DeepEqual(proof, snapshot.work.Proof) {
		return nil, errClusterConflict
	}
	authorization, _, err := parseClusterSSHPreparationAuthorization(string(base), proof, snapshot.work.Source)
	if err != nil || !reflect.DeepEqual(authorization, snapshot.work.Authorization) {
		return nil, errClusterConflict
	}
	if claiming {
		recorded, err := parseClusterSSHPreparationRecovery(snapshot.payload, snapshot.work)
		if err != nil || !reflect.DeepEqual(recorded, snapshot.record) || !clusterUUIDRE.MatchString(holder) ||
			holder == "00000000-0000-0000-0000-000000000000" || holder == snapshot.record.Targets[0].PreviousHolder {
			return nil, errClusterConflict
		}
	}
	updated := snapshot.payload
	if !claiming {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(snapshot.payload), &object) != nil {
			return nil, errClusterConflict
		}
		object[clusterSSHPreparationRecoveryField], err = json.Marshal(snapshot.record)
		if err != nil {
			return nil, errClusterConflict
		}
		wire, err := json.Marshal(object)
		if err != nil || len(wire) > 256<<10 {
			return nil, errClusterConflict
		}
		updated = string(wire)
	}
	targets := slices.Clone(snapshot.work.Proof.Cohort.Targets)
	slices.SortFunc(targets, func(a, b clusterSSHInspectedTarget) int {
		return strings.Compare(a.Binding.TargetID, b.Binding.TargetID)
	})
	reports, keys := map[string]string{}, map[string]string{}
	indices := map[string]int{}
	expiries := map[string]int64{}
	for i, target := range snapshot.work.Proof.Cohort.Targets {
		raw, err := json.Marshal(target.Report)
		digest, digestErr := clusterSSHPreparationDigest([]string{snapshot.credentials[i].generation, snapshot.credentials[i].ciphertext})
		if err != nil || digestErr != nil || digest != authorization.Targets[i].CredentialSHA256 || snapshot.credentials[i].binding != target.Binding {
			return nil, errClusterConflict
		}
		reports[target.Binding.TargetID], keys[target.Binding.TargetID] = string(raw), base64.StdEncoding.EncodeToString(target.Key.PublicKey)
		indices[target.Binding.TargetID] = i
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, errClusterConflict
	}
	defer tx.Rollback()
	if lockClusterSSHQualification(ctx, tx, snapshot.record.OperationID, targets) != nil ||
		checkClusterSSHQualificationSource(ctx, tx, clusterSSHQualificationWork{Proof: snapshot.work.Proof, Source: snapshot.work.Source}) != nil {
		return nil, errClusterConflict
	}
	var present int
	if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_operations o,engine.cluster_state c
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$2 AND o.attempt=$3 AND o.payload_json=$4
 AND c.id=1 AND c.active_operation_id=o.id
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$5`, snapshot.record.OperationID,
		snapshot.step, snapshot.record.Attempt, snapshot.payload, len(targets)).Scan(&present) != nil {
		return nil, errClusterConflict
	}
	oldState, oldStep, newState, newStep := "running", "stage_source", "recovery_required", clusterSSHPreparationRecoveryReservedStep
	if claiming {
		oldState, oldStep, newState, newStep = newState, newStep, "running", clusterSSHPreparationRecoveryClaimStep
	}
	for _, target := range targets {
		i := indices[target.Binding.TargetID]
		prior, credential := snapshot.record.Targets[i], snapshot.credentials[i]
		if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 SELECT 1 FROM engine.cluster_onboarding_targets t,engine.cluster_onboarding_credentials p,engine.aegis_cipher_state a,moment
 WHERE t.id=$1 AND t.operation_id=$2 AND t.cluster_id=$3 AND t.operation_attempt=$4
 AND t.management_ip=$5 AND t.ssh_port=$6 AND t.host_key_algorithm=$7 AND t.host_key_fingerprint=$8 AND t.host_key_base64=$9 AND t.ordinal=$10
 AND t.inspected_attempt=$4 AND t.inspected_generation=$11 AND t.inspected_at=$12 AND t.inspection_json=$13
 AND t.credential_state='available' AND t.state=$14 AND t.current_step=$15
 AND t.lease_holder=$16 AND t.lease_generation=$17 AND t.lease_expires_at=$18 AND t.lease_expires_at<=moment.now
 AND p.target_id=t.id AND p.expires_at>moment.now AND a.id=1 AND p.aegis_generation=a.verification_token
 AND p.aegis_generation=$19 AND p.ciphertext=$20`, target.Binding.TargetID, snapshot.record.OperationID,
			target.Binding.ClusterID, snapshot.record.Attempt, target.Binding.Address, target.Binding.Port, target.Key.Algorithm,
			target.Key.Fingerprint, keys[target.Binding.TargetID], target.Ordinal, target.Generation, target.InspectedAt, reports[target.Binding.TargetID],
			oldState, oldStep, prior.PreviousHolder, prior.PreviousGeneration, prior.PreviousExpiresAt, credential.generation, credential.ciphertext).Scan(&present) != nil {
			return nil, errClusterConflict
		}
		if claiming {
			var expectedExpiry, actualExpiry int64
			err = tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT floor(extract(epoch FROM clock_timestamp()))::bigint AS now)
 UPDATE engine.cluster_onboarding_targets SET state=$2,current_step=$3,
 lease_holder=$4,lease_generation=$5,lease_expires_at=moment.now+$6,updated_at=moment.now
 FROM moment WHERE id=$1 RETURNING moment.now+$6,lease_expires_at`, target.Binding.TargetID,
				newState, newStep, holder, prior.Generation, clusterSSHTargetLeaseSeconds).Scan(&expectedExpiry, &actualExpiry)
			if expectedExpiry != actualExpiry {
				return nil, errClusterConflict
			}
			expiries[target.Binding.TargetID] = expectedExpiry
		} else {
			expiries[target.Binding.TargetID] = prior.PreviousExpiresAt
			_, err = tx.ExecContext(ctx, `UPDATE engine.cluster_onboarding_targets SET state=$2,current_step=$3,
 updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, target.Binding.TargetID, newState, newStep)
		}
		if err != nil {
			return nil, errClusterConflict
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET current_step=$2,payload_json=$3,
 updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, snapshot.record.OperationID,
		clusterSSHPreparationRecoveryOperationStep, updated); err != nil {
		return nil, errClusterConflict
	}
	event, message := "ssh_preparation_recovery_reserved", "Interrupted preparation retained for recovery."
	if claiming {
		event, message = "ssh_preparation_recovery_claimed", "Preparation recovery verification pending."
	}
	var now int64
	if tx.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp()))::bigint`).Scan(&now) != nil ||
		insertClusterEvent(ctx, tx, snapshot.record.OperationID, "", snapshot.work.Source.ClusterID, event, "running", message,
			map[string]any{"attempt": snapshot.record.Attempt, "step": clusterSSHPreparationRecoveryOperationStep,
				"target_count": len(snapshot.record.Targets), "recovery_id": snapshot.record.ID}, now) != nil {
		return nil, errClusterConflict
	}
	// Event insertion can wait on locks/triggers too. Publish only if the entire
	// immutable lineage and fresh database clock still pass after that wait.
	if checkClusterSSHQualificationSource(ctx, tx, clusterSSHQualificationWork{Proof: snapshot.work.Proof, Source: snapshot.work.Source}) != nil ||
		checkClusterSSHPreparationRecoveryClock(ctx, tx, snapshot, updated, newState, newStep, holder, reports, keys, expiries) != nil || tx.Commit() != nil {
		return nil, errClusterConflict
	}
	return expiries, nil
}

// Clock predicates are repeated after every lock and write, including waits on
// target/operation triggers. Expiry cannot be upgraded into stopped-executor proof.
func checkClusterSSHPreparationRecoveryClock(ctx context.Context, tx *sql.Tx, snapshot clusterSSHPreparationRecoverySnapshot, payload, state, step, holder string, reports, keys map[string]string, expiries map[string]int64) error {
	record := snapshot.record
	var n int
	// Triggers can change rows written by this transaction despite its locks.
	// Repeat exact identity and custody after all writes; neither an empty census
	// nor a same-count replacement can stand in for the approved original cohort.
	for i, target := range snapshot.work.Proof.Cohort.Targets {
		prior, credential := record.Targets[i], snapshot.credentials[i]
		expectedHolder, expectedGeneration := prior.PreviousHolder, prior.PreviousGeneration
		if holder != "" {
			expectedHolder, expectedGeneration = holder, prior.Generation
		}
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_targets t,engine.cluster_onboarding_credentials p
 WHERE t.id=$1 AND t.operation_id=$2 AND t.cluster_id=$3 AND t.operation_attempt=$4
 AND t.management_ip=$5 AND t.ssh_port=$6 AND t.host_key_algorithm=$7 AND t.host_key_fingerprint=$8 AND t.host_key_base64=$9 AND t.ordinal=$10
 AND t.inspected_attempt=$4 AND t.inspected_generation=$11 AND t.inspected_at=$12 AND t.inspection_json=$13
 AND t.credential_state='available' AND t.state=$14 AND t.current_step=$15
 AND t.lease_holder=$16 AND t.lease_generation=$17 AND t.lease_expires_at=$18
 AND p.target_id=t.id AND p.aegis_generation=$19 AND p.ciphertext=$20`, target.Binding.TargetID, record.OperationID,
			target.Binding.ClusterID, record.Attempt, target.Binding.Address, target.Binding.Port, target.Key.Algorithm,
			target.Key.Fingerprint, keys[target.Binding.TargetID], target.Ordinal, target.Generation, target.InspectedAt, reports[target.Binding.TargetID],
			state, step, expectedHolder, expectedGeneration, expiries[target.Binding.TargetID], credential.generation, credential.ciphertext).Scan(&n) != nil {
			return errClusterConflict
		}
	}
	if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 SELECT 1 FROM moment,engine.cluster_application_leases l,engine.aegis_cipher_state a,engine.cluster_operations o,engine.cluster_state c
 WHERE l.name=$1 AND l.holder=$2 AND l.expires_at>moment.now AND a.id=1
 AND o.id=$3 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step=$7 AND o.attempt=$8 AND o.payload_json=$9
 AND c.id=1 AND c.active_operation_id=o.id
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$10
 AND NOT EXISTS(SELECT 1 FROM engine.cluster_onboarding_targets t
 LEFT JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 WHERE t.operation_id=$3 AND (t.state=$4 AND t.current_step=$5
 AND (($6::text='' AND t.lease_expires_at<=moment.now) OR (t.lease_holder=$6 AND t.lease_expires_at>moment.now))
 AND p.aegis_generation=a.verification_token AND p.expires_at>moment.now) IS NOT TRUE)`, clusterControllerLeaseName,
		record.ControllerHolder, record.OperationID, state, step, holder, clusterSSHPreparationRecoveryOperationStep,
		record.Attempt, payload, len(record.Targets)).Scan(&n) != nil {
		return errClusterConflict
	}
	return nil
}
