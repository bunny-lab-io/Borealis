package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// One adapter belongs to the exact lease and credential envelope already read
// by its Aegis-capable worker. It neither claims a target nor advances a parent.
// Qualification/confirmation and remote domain proofs remain separate gates.
func newClusterSSHPreparationAuthorityRead(store *postgresOperatorStore, aegis *goAegisService,
	lease clusterSSHTargetLease, baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials) clusterSSHPreparationAuthorityRead {
	return func(parent context.Context) (clusterSSHPreparationAuthority, error) {
		fail := func() (clusterSSHPreparationAuthority, error) {
			return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
		}
		if store == nil || store.db == nil || aegis == nil || !validClusterSSHPreparationLease(lease) || baseline.Validate() != nil ||
			baseline.Repository != clusterGitHubRepo() || !sealed.binding.valid() || sealed.binding.OperationID != lease.OperationID || sealed.binding.TargetID != lease.TargetID ||
			sealed.generation == "" || len(sealed.generation) > 16<<10 || !strings.HasPrefix(sealed.ciphertext, aegisEnvelopePrefix) || len(sealed.ciphertext) > 256<<10 {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		// Both checks return their own connection before verifying the memory key.
		// A correct persisted token alone cannot keep a locked API worker active.
		if aegis.verifyClusterSSHGeneration(ctx, sealed.generation) != nil {
			return fail()
		}
		authority, err := store.loadClusterSSHPreparationAuthority(ctx, lease, baseline, sealed)
		if err != nil || aegis.verifyClusterSSHGeneration(ctx, sealed.generation) != nil || ctx.Err() != nil {
			return fail()
		}
		return authority, nil
	}
}

func validClusterSSHPreparationLease(lease clusterSSHTargetLease) bool {
	return clusterUUIDRE.MatchString(lease.OperationID) && clusterUUIDRE.MatchString(lease.TargetID) && clusterUUIDRE.MatchString(lease.Holder) &&
		lease.ControllerHolder != "" && len(lease.ControllerHolder) <= 1024 && lease.Generation > 0 && lease.OperationAttempt > 0 &&
		lease.OperationKind == "ssh_onboarding" && lease.OperationStep == clusterSSHPreparationOperationStep && lease.Step == "stage_source"
}

// The first short read obtains bounded historical proof for parsing outside a
// transaction. The SQL-only transaction then locks current authority and every
// cohort credential/target, comparing the exact payload again after all waits.
// Original inspected_generation remains distinct from each staging claim.
func (s *postgresOperatorStore) loadClusterSSHPreparationAuthority(ctx context.Context, lease clusterSSHTargetLease,
	baseline clusterbootstrap.Expected, sealed sealedClusterSSHCredentials) (clusterSSHPreparationAuthority, error) {
	fail := func() (clusterSSHPreparationAuthority, error) {
		return clusterSSHPreparationAuthority{}, clusterbootstrap.ErrPreparationConfig
	}
	if s == nil || s.db == nil || !validClusterSSHPreparationLease(lease) || baseline.Validate() != nil || ctx.Err() != nil {
		return fail()
	}
	var payload string
	if s.db.QueryRowContext(ctx, `SELECT payload_json FROM engine.cluster_operations WHERE id=$1
 AND kind='ssh_onboarding' AND state='running' AND current_step='prepare_ssh_targets' AND attempt=$2
 AND octet_length(payload_json)<=262144`, lease.OperationID, lease.OperationAttempt).Scan(&payload) != nil {
		return fail()
	}
	proof, version, err := parseClusterSSHPreparationInspection([]byte(payload), lease, baseline)
	if err != nil {
		return fail()
	}
	targets := append([]clusterSSHInspectedTarget(nil), proof.Cohort.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].Binding.TargetID < targets[j].Binding.TargetID })
	members := append([]clusterSSHSourceMember(nil), proof.Source.Members...)
	sort.Slice(members, func(i, j int) bool { return members[i].NodeID < members[j].NodeID })
	reports, keys := make([]string, len(targets)), make([]string, len(targets))
	own := false
	for i, target := range targets {
		if target.Binding.TargetID == lease.TargetID {
			own = target.Binding == sealed.binding && lease.Generation > target.Generation
		}
		raw, err := json.Marshal(target.Report)
		if err != nil {
			return fail()
		}
		reports[i], keys[i] = string(raw), base64.StdEncoding.EncodeToString(target.Key.PublicKey)
	}
	if !own {
		return fail()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	// Existing helper locks controller, cluster, operation, Aegis, then first
	// credential. Continue in target-ID order, shared with parent/cleanup paths.
	if lockClusterSSHWorkerAuthority(ctx, tx, lease.OperationID, targets[0].Binding.TargetID) != nil {
		return fail()
	}
	var present int
	for _, target := range targets[1:] {
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_credentials WHERE target_id=$1 FOR SHARE`, target.Binding.TargetID).Scan(&present) != nil {
			return fail()
		}
	}
	for i, target := range targets {
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_onboarding_targets WHERE id=$1 AND operation_id=$2 AND cluster_id=$3
 AND management_ip=$4 AND ssh_port=$5 AND host_key_algorithm=$6 AND host_key_fingerprint=$7 AND host_key_base64=$8
 AND ordinal=$9 AND inspected_at=$10 AND inspected_attempt=$11 AND inspected_generation=$12 AND inspection_json=$13
 FOR SHARE`, target.Binding.TargetID, lease.OperationID, proof.Source.ClusterID, target.Binding.Address, target.Binding.Port,
			target.Key.Algorithm, target.Binding.Fingerprint, keys[i], target.Ordinal, target.InspectedAt, target.Attempt, target.Generation, reports[i]).Scan(&present) != nil {
			return fail()
		}
	}
	for _, member := range members {
		if tx.QueryRowContext(ctx, `SELECT 1 FROM engine.cluster_nodes WHERE id=$1 AND node_name=$2 AND management_ip=$3
 AND membership_state='Active' AND application_state='active' FOR SHARE`, member.NodeID, member.Name, member.Address).Scan(&present) != nil {
			return fail()
		}
	}
	source, err := readClusterSSHQueueSource(ctx, tx)
	if err != nil {
		return fail()
	}
	var now int64
	// Every expiry is evaluated after every lock wait, using one fresh DB time.
	// No inner credential join may hide a missing/expired second target.
	err = tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED (SELECT extract(epoch FROM clock_timestamp()) AS now)
 SELECT floor(moment.now)::bigint
 FROM moment,engine.cluster_operations o,engine.cluster_state c,engine.cluster_application_leases l,
 engine.aegis_cipher_state a,engine.cluster_onboarding_targets own,engine.cluster_onboarding_credentials credential
 WHERE o.id=$1 AND o.kind='ssh_onboarding' AND o.state='running' AND o.current_step='prepare_ssh_targets' AND o.attempt=$2 AND o.payload_json=$3
 AND c.id=1 AND c.active_operation_id=o.id AND c.cluster_id=$4
 AND l.name=$5 AND l.holder=$6 AND l.expires_at>moment.now AND a.id=1 AND a.verification_token=$7
 AND own.id=$8 AND own.operation_id=o.id AND own.state='running' AND own.current_step='stage_source'
 AND own.lease_holder=$9 AND own.lease_generation=$10 AND own.lease_expires_at>moment.now
 AND credential.target_id=own.id AND credential.ciphertext=$11
 AND (SELECT count(*) FROM engine.cluster_onboarding_targets WHERE operation_id=o.id)=$12
 AND NOT EXISTS (
  SELECT 1 FROM engine.cluster_onboarding_targets t LEFT JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
  WHERE t.operation_id=o.id AND (
   t.cluster_id=c.cluster_id AND t.operation_attempt=o.attempt AND t.credential_state='available'
   AND t.inspected_attempt=o.attempt AND t.inspected_generation>0 AND t.inspected_at>moment.now-$13 AND t.inspected_at<=moment.now
   AND p.aegis_generation=a.verification_token AND p.expires_at>moment.now
   AND ((t.state='queued' AND t.current_step IN ('inspection_complete','stage_source') AND t.lease_holder='' AND t.lease_expires_at=0 AND t.lease_generation=t.inspected_generation)
    OR (t.state='running' AND t.current_step='stage_source' AND t.lease_holder ~ $14 AND t.lease_expires_at>moment.now AND t.lease_generation>t.inspected_generation))
  ) IS NOT TRUE)`, lease.OperationID, lease.OperationAttempt, payload, proof.Source.ClusterID, clusterControllerLeaseName, lease.ControllerHolder, sealed.generation,
		lease.TargetID, lease.Holder, lease.Generation, sealed.ciphertext, len(targets), clusterSSHInspectionLifetimeSeconds, clusterUUIDRE.String()).Scan(&now)
	if err != nil || tx.Commit() != nil {
		return fail()
	}
	// Parsing, hashing, crypto and payload shaping happen only after commit.
	if source.ActiveOperationID != lease.OperationID || source.Release != baseline.Release || source.SHA != baseline.SourceSHA ||
		source.ClusterID != proof.Source.ClusterID || source.ActiveSize != proof.Source.ActiveSize || source.DesiredSize != proof.Source.DesiredSize ||
		source.Status != proof.Source.Status || source.HMRState != proof.Source.HMRState || source.ControlVIP != proof.Source.ControlPlaneVIP || source.EdgeVIP != proof.Source.EdgeVIP {
		return fail()
	}
	source.ActiveOperationID = ""
	if count, err := source.validate(); err != nil || count != len(targets) {
		return fail()
	}
	var configured string
	if clusterBootstrapObject([]byte(source.ConfigJSON), map[string]any{"k3s_version": &configured}) != nil || configured != version {
		return fail()
	}
	proof.Cohort.ObservedAt = now
	if validateClusterSSHInspectionCohort(proof.Cohort, proof.Source) != nil || ctx.Err() != nil {
		return fail()
	}
	return clusterSSHPreparationAuthority{Cohort: proof.Cohort, Source: proof.Source, Lease: lease, Baseline: baseline, K3sVersion: version}, nil
}

func parseClusterSSHPreparationInspection(payload []byte, lease clusterSSHTargetLease, baseline clusterbootstrap.Expected) (clusterSSHParentProof, string, error) {
	fail := func() (clusterSSHParentProof, string, error) {
		return clusterSSHParentProof{}, "", clusterbootstrap.ErrPreparationConfig
	}
	if len(payload) > 256<<10 || !utf8.Valid(payload) {
		return fail()
	}
	var inspection json.RawMessage
	var release, sha, version string
	var count int
	if clusterBootstrapObject(payload, map[string]any{"ssh_inspection": &inspection, "baseline_release": &release, "baseline_sha": &sha, "source_k3s_version": &version, "target_count": &count}) != nil ||
		release != baseline.Release || sha != baseline.SourceSHA || !clusterK3sRE.MatchString(version) {
		return fail()
	}
	object, err := decodeClusterSSHObject(inspection, map[string]bool{"sha256": true, "proof": true, "qualification_required": true})
	if err != nil || len(object) != 3 || len(object["proof"]) > 128<<10 {
		return fail()
	}
	var proof clusterSSHParentProof
	var digest string
	var qualificationRequired bool
	if json.Unmarshal(object["proof"], &proof) != nil || json.Unmarshal(object["sha256"], &digest) != nil || json.Unmarshal(object["qualification_required"], &qualificationRequired) != nil || !qualificationRequired {
		return fail()
	}
	canonical, err := json.Marshal(proof)
	if err != nil || !sameClusterSSHStoredJSON(object["proof"], canonical, 0) {
		return fail()
	}
	hash := sha256.Sum256(canonical)
	if digest != hex.EncodeToString(hash[:]) || proof.Version != 1 || proof.Cohort.OperationID != lease.OperationID || proof.Cohort.Attempt != lease.OperationAttempt ||
		proof.Cohort.ControllerHolder != lease.ControllerHolder || count != len(proof.Cohort.Targets) || validateClusterSSHInspectionCohort(proof.Cohort, proof.Source) != nil {
		return fail()
	}
	return proof, version, nil
}

// Payload maps may reorder persisted struct fields. Compare against the typed
// serializer structurally, preserving exact numbers and rejecting omitted,
// duplicate, case-aliased or unknown keys recursively before trusting the hash.
func sameClusterSSHStoredJSON(raw, canonical []byte, depth int) bool {
	if depth > 32 {
		return false
	}
	raw, canonical = bytes.TrimSpace(raw), bytes.TrimSpace(canonical)
	if len(raw) == 0 || len(canonical) == 0 {
		return false
	}
	if canonical[0] == '{' {
		var expected map[string]json.RawMessage
		if json.Unmarshal(canonical, &expected) != nil {
			return false
		}
		allowed := make(map[string]bool, len(expected))
		for key := range expected {
			allowed[key] = true
		}
		actual, err := decodeClusterSSHObject(raw, allowed)
		if err != nil || len(actual) != len(expected) {
			return false
		}
		for key, value := range expected {
			if !sameClusterSSHStoredJSON(actual[key], value, depth+1) {
				return false
			}
		}
		return true
	}
	if canonical[0] == '[' {
		var actual, expected []json.RawMessage
		if raw[0] != '[' || json.Unmarshal(raw, &actual) != nil || json.Unmarshal(canonical, &expected) != nil || len(actual) != len(expected) {
			return false
		}
		for i := range expected {
			if !sameClusterSSHStoredJSON(actual[i], expected[i], depth+1) {
				return false
			}
		}
		return true
	}
	decode := func(value []byte) (any, bool) {
		d := json.NewDecoder(bytes.NewReader(value))
		d.UseNumber()
		var out any
		err := d.Decode(&out)
		return out, err == nil && d.Decode(&struct{}{}) == io.EOF
	}
	actual, ok := decode(raw)
	expected, expectedOK := decode(canonical)
	return ok && expectedOK && reflect.DeepEqual(actual, expected)
}
