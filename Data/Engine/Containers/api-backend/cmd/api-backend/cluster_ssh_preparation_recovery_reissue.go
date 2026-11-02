package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/base64"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Explicit new proof intent, bound to the exact retained tip and raw SQL
// payload. Repeating a request never reconstructs its issued local handle.
type clusterSSHPreparationRecoveryReissueRequest struct {
	ID, PreviousID, PayloadSHA256, Holder string
}

// Version two wraps the complete initial record unchanged. Every later proof
// retains the previous complete cohort before replacing its SQL lease. The
// existing 8KiB record bound is a fail-closed storage bound, never a pruning cap.
type clusterSSHPreparationRecoveryLineage struct {
	Version  int                                        `json:"version"`
	Original clusterSSHPreparationRecoveryRecord        `json:"original"`
	Requests []clusterSSHPreparationRecoveryProofRecord `json:"requests"`
}

type clusterSSHPreparationRecoveryProofRecord struct {
	ID                   string                                `json:"id"`
	PreviousID           string                                `json:"previous_id"`
	PreviousRecordSHA256 string                                `json:"previous_record_sha256"`
	PayloadSHA256        string                                `json:"payload_sha256"`
	Holder               string                                `json:"holder"`
	Targets              []clusterSSHPreparationRecoveryTarget `json:"targets"`
}

// Separate type: no initial preparation, source reconciliation, delivery,
// settlement or admission entry accepts this handle.
type clusterSSHPreparationRecoveryReissuedProofClaim struct {
	Lineage  clusterSSHPreparationRecoveryLineage
	Claims   []clusterSSHPreparationRecoveryTargetClaim
	Baseline clusterbootstrap.Expected
	payload  string
	custody  *clusterSSHPreparationRecoveryCustody
}

type clusterSSHPreparationRecoveryReissuedProofAuthority struct {
	Lineage        clusterSSHPreparationRecoveryLineage
	Baseline       clusterbootstrap.Expected
	Cohort         clusterSSHInspectionCohort
	Source         clusterSSHSourceCohort
	Authorization  clusterSSHPreparationAuthorization
	IdentitySource engineidentity.Binding
	Leases         []clusterSSHTargetLease
	ExpiresAt      map[string]int64
}

func (clusterSSHPreparationRecoveryReissuedProofClaim) String() string {
	return "SSH preparation reissued proof [redacted]"
}
func (c clusterSSHPreparationRecoveryReissuedProofClaim) GoString() string { return c.String() }
func (clusterSSHPreparationRecoveryReissuedProofAuthority) String() string {
	return "SSH preparation reissued proof authority [redacted]"
}
func (a clusterSSHPreparationRecoveryReissuedProofAuthority) GoString() string { return a.String() }
func (a clusterSSHPreparationRecoveryReissuedProofAuthority) recoveryExpiries() map[string]int64 {
	return a.ExpiresAt
}
func (a clusterSSHPreparationRecoveryReissuedProofAuthority) recoveryStable() any {
	a.ExpiresAt = nil
	return a
}

func clusterSSHPreparationRecoveryNonzeroUUID(s string) bool {
	return clusterUUIDRE.MatchString(s) && s != "00000000-0000-0000-0000-000000000000"
}

func (l clusterSSHPreparationRecoveryLineage) tip() (string, string, []clusterSSHPreparationRecoveryTarget) {
	if len(l.Requests) == 0 {
		return l.Original.ID, "", l.Original.Targets
	}
	tip := l.Requests[len(l.Requests)-1]
	return tip.ID, tip.Holder, tip.Targets
}

func (l clusterSSHPreparationRecoveryLineage) canonical() ([]byte, error) {
	if len(l.Requests) == 0 {
		return json.Marshal(l.Original)
	}
	return json.Marshal(l)
}

// The history digest is reconstructible from the immutable canonical original
// payload and exact prior recovery record. The incoming request separately
// binds the stored raw string; neither encoding is treated as the other.
func clusterSSHPreparationRecoveryLineagePayload(base []byte, lineage clusterSSHPreparationRecoveryLineage) ([]byte, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(base, &object) != nil || object == nil {
		return nil, errClusterConflict
	}
	wire, err := lineage.canonical()
	if err != nil {
		return nil, errClusterConflict
	}
	object[clusterSSHPreparationRecoveryField] = wire
	return json.Marshal(object)
}

func (l clusterSSHPreparationRecoveryLineage) valid(work clusterSSHPreparationWork, base []byte) bool {
	if l.Version != 2 || !l.Original.valid(work, base) {
		return false
	}
	ids := map[string]bool{l.Original.ID: true}
	holders := map[string]bool{l.Original.Targets[0].PreviousHolder: true}
	prefix := clusterSSHPreparationRecoveryLineage{Version: 2, Original: l.Original}
	for _, entry := range l.Requests {
		priorID, priorHolder, priorTargets := prefix.tip()
		wire, err := prefix.canonical()
		priorPayload, payloadErr := clusterSSHPreparationRecoveryLineagePayload(base, prefix)
		if err != nil || payloadErr != nil || !clusterSSHPreparationRecoveryNonzeroUUID(entry.ID) || ids[entry.ID] || entry.PreviousID != priorID ||
			entry.PreviousRecordSHA256 != clusterSSHPreparationRecoveryPayloadDigest(wire) || entry.PayloadSHA256 != clusterSSHPreparationRecoveryPayloadDigest(priorPayload) ||
			!clusterSSHPreparationRecoveryNonzeroUUID(entry.Holder) || holders[entry.Holder] || len(entry.Targets) != len(priorTargets) {
			return false
		}
		previous := entry.Targets[0].PreviousHolder
		if !clusterSSHPreparationRecoveryNonzeroUUID(previous) || previous == entry.Holder ||
			(priorHolder != "" && previous != priorHolder) || (priorHolder == "" && holders[previous]) {
			return false
		}
		for i, target := range entry.Targets {
			if target.TargetID != priorTargets[i].TargetID || target.PreviousHolder != previous || target.PreviousExpiresAt <= 0 ||
				target.PreviousGeneration != priorTargets[i].Generation || target.PreviousGeneration >= 1<<53-1 ||
				target.Generation != target.PreviousGeneration+1 {
				return false
			}
		}
		ids[entry.ID], holders[previous], holders[entry.Holder] = true, true, true
		prefix.Requests = append(prefix.Requests, entry)
	}
	return true
}

func parseClusterSSHPreparationRecoveryLineage(payload string, work clusterSSHPreparationWork) (clusterSSHPreparationRecoveryLineage, error) {
	fail := func() (clusterSSHPreparationRecoveryLineage, error) {
		return clusterSSHPreparationRecoveryLineage{}, errClusterConflict
	}
	base, raw, err := clusterSSHPreparationRecoveryPayload(payload)
	if err != nil || len(raw) == 0 || len(raw) > 8<<10 {
		return fail()
	}
	// Retained v1 is a genesis observation, never a new v1 issuance.
	if original, err := parseClusterSSHPreparationRecovery(payload, work); err == nil {
		return clusterSSHPreparationRecoveryLineage{Version: 2, Original: original}, nil
	}
	var lineage clusterSSHPreparationRecoveryLineage
	if json.Unmarshal(raw, &lineage) != nil || len(lineage.Requests) == 0 || !lineage.valid(work, base) {
		return fail()
	}
	canonical, err := json.Marshal(lineage)
	if err != nil || !sameClusterSSHStoredJSON(raw, canonical, 0) {
		return fail()
	}
	return lineage, nil
}

func clusterSSHPreparationRecoveryRowInputs(snapshot clusterSSHPreparationRecoverySnapshot) (map[string]string, map[string]string, []clusterSSHInspectedTarget, error) {
	reports, keys := map[string]string{}, map[string]string{}
	if len(snapshot.credentials) != len(snapshot.work.Proof.Cohort.Targets) {
		return nil, nil, nil, errClusterConflict
	}
	for i, target := range snapshot.work.Proof.Cohort.Targets {
		wire, err := json.Marshal(target.Report)
		credential := snapshot.credentials[i]
		digest, digestErr := clusterSSHPreparationDigest([]string{credential.generation, credential.ciphertext})
		if err != nil || digestErr != nil || credential.binding != target.Binding || digest != snapshot.work.Authorization.Targets[i].CredentialSHA256 {
			return nil, nil, nil, errClusterConflict
		}
		reports[target.Binding.TargetID], keys[target.Binding.TargetID] = string(wire), base64.StdEncoding.EncodeToString(target.Key.PublicKey)
	}
	targets := slices.Clone(snapshot.work.Proof.Cohort.Targets)
	slices.SortFunc(targets, func(a, b clusterSSHInspectedTarget) int {
		return strings.Compare(a.Binding.TargetID, b.Binding.TargetID)
	})
	return reports, keys, targets, nil
}

// Callable only. Expiry permits a new SQL proof lease, never establishes that
// any native producer stopped or that any effect is absent.
func (c *clusterController) reissueClusterSSHPreparationRecoveryProof(ctx context.Context, operation clusterControllerOperation,
	request clusterSSHPreparationRecoveryReissueRequest) (clusterSSHPreparationRecoveryReissuedProofClaim, error) {
	fail := func() (clusterSSHPreparationRecoveryReissuedProofClaim, error) {
		return clusterSSHPreparationRecoveryReissuedProofClaim{}, errClusterConflict
	}
	if !clusterSSHPreparationRecoveryNonzeroUUID(request.ID) || !clusterSSHPreparationRecoveryNonzeroUUID(request.PreviousID) ||
		!clusterSSHPreparationRecoveryNonzeroUUID(request.Holder) || !validClusterSSHPreparationDigest(request.PayloadSHA256) {
		return fail()
	}
	snapshot, err := c.readClusterSSHPreparationRecoveryWork(ctx, operation)
	if err != nil || snapshot.step != clusterSSHPreparationRecoveryOperationStep || request.PayloadSHA256 != clusterSSHPreparationRecoveryPayloadDigest([]byte(snapshot.payload)) {
		return fail()
	}
	lineage, err := parseClusterSSHPreparationRecoveryLineage(snapshot.payload, snapshot.work)
	priorID, priorHolder, priorTargets := lineage.tip()
	if err != nil || request.PreviousID != priorID {
		return fail()
	}
	priorWire, err := lineage.canonical()
	if err != nil {
		return fail()
	}
	base, _, err := clusterSSHPreparationRecoveryPayload(snapshot.payload)
	priorPayload, payloadErr := clusterSSHPreparationRecoveryLineagePayload(base, lineage)
	if err != nil || payloadErr != nil {
		return fail()
	}
	entry := clusterSSHPreparationRecoveryProofRecord{ID: request.ID, PreviousID: request.PreviousID,
		PreviousRecordSHA256: clusterSSHPreparationRecoveryPayloadDigest(priorWire), PayloadSHA256: clusterSSHPreparationRecoveryPayloadDigest(priorPayload), Holder: request.Holder}
	previousRows := make([]clusterSSHPreparationRecoveryRowLease, 0, len(priorTargets))
	for _, prior := range priorTargets {
		row := clusterSSHPreparationRecoveryRowLease{TargetID: prior.TargetID}
		if c.store.db.QueryRowContext(ctx, `SELECT lease_holder,lease_generation,lease_expires_at FROM engine.cluster_onboarding_targets
 WHERE id=$1 AND operation_id=$2 AND state='running' AND current_step=$3
 AND lease_expires_at>0 AND lease_expires_at<=extract(epoch FROM clock_timestamp())`, prior.TargetID, operation.ID, clusterSSHPreparationRecoveryClaimStep).
			Scan(&row.Holder, &row.Generation, &row.ExpiresAt) != nil || row.Generation != prior.Generation || priorHolder != "" && row.Holder != priorHolder {
			return fail()
		}
		entry.Targets = append(entry.Targets, clusterSSHPreparationRecoveryTarget{TargetID: row.TargetID, PreviousHolder: row.Holder,
			PreviousGeneration: row.Generation, PreviousExpiresAt: row.ExpiresAt, Generation: row.Generation + 1})
		previousRows = append(previousRows, row)
	}
	lineage.Requests = append(lineage.Requests, entry)
	if !lineage.valid(snapshot.work, base) {
		return fail()
	}
	wire, err := json.Marshal(lineage)
	if err != nil || len(wire) > 8<<10 {
		return fail()
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(snapshot.payload), &object) != nil {
		return fail()
	}
	object[clusterSSHPreparationRecoveryField] = wire
	updated, err := json.Marshal(object)
	if err != nil || len(updated) > 256<<10 {
		return fail()
	}
	reports, keys, targets, err := clusterSSHPreparationRecoveryRowInputs(snapshot)
	if err != nil {
		return fail()
	}
	qualification := clusterSSHQualificationWork{Proof: snapshot.work.Proof, Source: snapshot.work.Source}
	rows := make([]clusterSSHPreparationRecoveryRowLease, len(entry.Targets))
	indices := map[string]int{}
	for i, target := range entry.Targets {
		indices[target.TargetID] = i
		rows[i] = clusterSSHPreparationRecoveryRowLease{TargetID: target.TargetID, Holder: request.Holder, Generation: target.Generation}
	}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	if lockClusterSSHQualification(ctx, tx, operation.ID, targets) != nil || checkClusterSSHQualificationSource(ctx, tx, qualification) != nil ||
		checkClusterSSHPreparationRecoveryRows(ctx, tx, snapshot, snapshot.payload, "running", clusterSSHPreparationRecoveryClaimStep, "", reports, keys, previousRows) != nil {
		return fail()
	}
	for _, target := range targets {
		i := indices[target.Binding.TargetID]
		var assigned, actual int64
		if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT floor(extract(epoch FROM clock_timestamp()))::bigint AS now)
 UPDATE engine.cluster_onboarding_targets SET lease_holder=$2,lease_generation=$3,
 lease_expires_at=moment.now+$4,updated_at=moment.now FROM moment WHERE id=$1
 RETURNING moment.now+$4,lease_expires_at`, target.Binding.TargetID, request.Holder, rows[i].Generation, clusterSSHTargetLeaseSeconds).
			Scan(&assigned, &actual) != nil || assigned != actual {
			return fail()
		}
		rows[i].ExpiresAt = assigned
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations SET payload_json=$2,
 updated_at=floor(extract(epoch FROM clock_timestamp()))::bigint WHERE id=$1`, operation.ID, string(updated)); err != nil {
		return fail()
	}
	// No event or public projection is needed to issue this private proof-only
	// handle. All exact source/row/payload clocks are repeated after every write.
	if checkClusterSSHQualificationSource(ctx, tx, qualification) != nil ||
		checkClusterSSHPreparationRecoveryRows(ctx, tx, snapshot, string(updated), "running", clusterSSHPreparationRecoveryClaimStep, request.Holder, reports, keys, rows) != nil ||
		ctx.Err() != nil || tx.Commit() != nil {
		return fail()
	}
	claim := clusterSSHPreparationRecoveryReissuedProofClaim{Lineage: lineage, Baseline: snapshot.work.Baseline, payload: string(updated),
		custody: &clusterSSHPreparationRecoveryCustody{assigned: map[string]int64{}}}
	for i, target := range entry.Targets {
		claim.custody.assigned[target.TargetID] = rows[i].ExpiresAt
		claim.Claims = append(claim.Claims, clusterSSHPreparationRecoveryTargetClaim{Previous: target, Sealed: snapshot.credentials[i],
			Lease: clusterSSHTargetLease{TargetID: target.TargetID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
				ControllerHolder: c.holder, OperationKind: "ssh_onboarding", OperationStep: clusterSSHPreparationRecoveryOperationStep,
				Step: clusterSSHPreparationRecoveryClaimStep, Holder: request.Holder, Generation: target.Generation}})
	}
	return claim, nil
}

func freezeClusterSSHPreparationRecoveryReissuedProof(claim clusterSSHPreparationRecoveryReissuedProofClaim) (clusterSSHPreparationRecoveryReissuedProofClaim, map[string]int64, error) {
	fail := func() (clusterSSHPreparationRecoveryReissuedProofClaim, map[string]int64, error) {
		return clusterSSHPreparationRecoveryReissuedProofClaim{}, nil, clusterbootstrap.ErrSessionAuthority
	}
	_, raw, err := clusterSSHPreparationRecoveryPayload(claim.payload)
	wire, wireErr := json.Marshal(claim.Lineage)
	var owned clusterSSHPreparationRecoveryLineage
	if err != nil || wireErr != nil || len(wire) > 8<<10 || !sameClusterSSHStoredJSON(raw, wire, 0) || json.Unmarshal(wire, &owned) != nil ||
		owned.Version != 2 || len(owned.Requests) == 0 || claim.Baseline.Validate() != nil || claim.custody == nil ||
		len(claim.Claims) < 1 || len(claim.Claims) > 2 || len(claim.custody.assigned) != len(claim.Claims) {
		return fail()
	}
	claim.Lineage, claim.Claims = owned, slices.Clone(claim.Claims)
	_, holder, targets := owned.tip()
	if !clusterSSHPreparationRecoveryNonzeroUUID(holder) || len(targets) != len(claim.Claims) {
		return fail()
	}
	expires := maps.Clone(claim.custody.assigned)
	for i, target := range claim.Claims {
		lease, prior := target.Lease, targets[i]
		if target.Previous != prior || lease.TargetID != prior.TargetID || lease.Generation != prior.Generation || lease.Holder != holder || lease.Holder == prior.PreviousHolder ||
			lease.OperationID != owned.Original.OperationID || lease.OperationAttempt != owned.Original.Attempt || lease.ControllerHolder != owned.Original.ControllerHolder ||
			lease.OperationKind != "ssh_onboarding" || lease.OperationStep != clusterSSHPreparationRecoveryOperationStep || lease.Step != clusterSSHPreparationRecoveryClaimStep ||
			expires[prior.TargetID] < 1 || !target.Sealed.binding.valid() || target.Sealed.binding.TargetID != prior.TargetID || target.Sealed.binding.OperationID != lease.OperationID ||
			target.Sealed.generation == "" || !strings.HasPrefix(target.Sealed.ciphertext, aegisEnvelopePrefix) {
			return fail()
		}
	}
	return claim, expires, nil
}

func (c *clusterController) observeClusterSSHPreparationRecoveryReissuedProof(ctx context.Context, claim clusterSSHPreparationRecoveryReissuedProofClaim,
	expected map[string]int64, renew bool) (clusterSSHPreparationRecoveryReissuedProofAuthority, map[string]int64, error) {
	fail := func() (clusterSSHPreparationRecoveryReissuedProofAuthority, map[string]int64, error) {
		return clusterSSHPreparationRecoveryReissuedProofAuthority{}, nil, clusterbootstrap.ErrSessionAuthority
	}
	if c == nil || c.store == nil || c.store.db == nil || ctx.Err() != nil || claim.custody == nil || claim.custody.failed.Load() ||
		!claim.custody.started.Load() || len(claim.Claims) == 0 || len(claim.Claims) != len(expected) {
		return fail()
	}
	original := claim.Lineage.Original
	operation := clusterControllerOperation{ID: original.OperationID, Kind: "ssh_onboarding", State: "running", CurrentStep: clusterSSHPreparationRecoveryOperationStep, Attempt: original.Attempt}
	snapshot, err := c.readClusterSSHPreparationRecoveryWork(ctx, operation)
	if err != nil || snapshot.payload != claim.payload || snapshot.work.Baseline != claim.Baseline {
		return fail()
	}
	lineage, err := parseClusterSSHPreparationRecoveryLineage(snapshot.payload, snapshot.work)
	if err != nil || !reflect.DeepEqual(lineage, claim.Lineage) || len(snapshot.credentials) != len(claim.Claims) {
		return fail()
	}
	_, holder, tip := lineage.tip()
	if len(tip) != len(claim.Claims) || holder == "" {
		return fail()
	}
	rows := make([]clusterSSHPreparationRecoveryRowLease, len(tip))
	for i, target := range claim.Claims {
		lease := clusterSSHTargetLease{TargetID: tip[i].TargetID, OperationID: operation.ID, OperationAttempt: operation.Attempt, ControllerHolder: c.holder,
			OperationKind: "ssh_onboarding", OperationStep: clusterSSHPreparationRecoveryOperationStep, Step: clusterSSHPreparationRecoveryClaimStep, Holder: holder, Generation: tip[i].Generation}
		if target.Lease != lease || target.Previous != tip[i] || target.Sealed != snapshot.credentials[i] || expected[tip[i].TargetID] < 1 {
			return fail()
		}
		rows[i] = clusterSSHPreparationRecoveryRowLease{TargetID: tip[i].TargetID, Holder: holder, Generation: tip[i].Generation, ExpiresAt: expected[tip[i].TargetID]}
	}
	reports, keys, targets, err := clusterSSHPreparationRecoveryRowInputs(snapshot)
	if err != nil {
		return fail()
	}
	expiries := maps.Clone(expected)
	qualification := clusterSSHQualificationWork{Proof: snapshot.work.Proof, Source: snapshot.work.Source}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	if lockClusterSSHQualification(ctx, tx, operation.ID, targets) != nil || checkClusterSSHQualificationSource(ctx, tx, qualification) != nil ||
		checkClusterSSHPreparationRecoveryRows(ctx, tx, snapshot, claim.payload, "running", clusterSSHPreparationRecoveryClaimStep, holder, reports, keys, rows) != nil {
		return fail()
	}
	if renew {
		for _, target := range targets {
			var assigned, actual int64
			if tx.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 UPDATE engine.cluster_onboarding_targets t SET lease_expires_at=floor(moment.now)::bigint+$3
 FROM moment WHERE t.id=$1 AND t.lease_expires_at=$2 AND t.lease_expires_at>moment.now
 RETURNING floor(moment.now)::bigint+$3,t.lease_expires_at`, target.Binding.TargetID, expected[target.Binding.TargetID], clusterSSHTargetLeaseSeconds).
				Scan(&assigned, &actual) != nil || assigned != actual {
				return fail()
			}
			expiries[target.Binding.TargetID] = assigned
		}
		for i := range rows {
			rows[i].ExpiresAt = expiries[rows[i].TargetID]
		}
	}
	if checkClusterSSHQualificationSource(ctx, tx, qualification) != nil ||
		checkClusterSSHPreparationRecoveryRows(ctx, tx, snapshot, claim.payload, "running", clusterSSHPreparationRecoveryClaimStep, holder, reports, keys, rows) != nil ||
		ctx.Err() != nil || claim.custody.failed.Load() || tx.Commit() != nil {
		return fail()
	}
	value := clusterSSHPreparationRecoveryReissuedProofAuthority{Lineage: lineage, Baseline: snapshot.work.Baseline,
		Cohort: snapshot.work.Proof.Cohort, Source: snapshot.work.Proof.Source, Authorization: snapshot.work.Authorization, IdentitySource: snapshot.work.IdentitySource, ExpiresAt: maps.Clone(expiries)}
	for _, target := range claim.Claims {
		value.Leases = append(value.Leases, target.Lease)
	}
	return value, expiries, nil
}

type clusterSSHPreparationRecoveryReissuedProofConsumer func(context.Context, clusterSSHPreparationRecoveryReissuedProofAuthority, func(context.Context) error) error

func (c *clusterController) withClusterSSHPreparationRecoveryReissuedProof(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryReissuedProofClaim, consume clusterSSHPreparationRecoveryReissuedProofConsumer) error {
	return c.withClusterSSHPreparationRecoveryReissuedProofEvery(parent, aegis, issued, time.Second, consume)
}

func (c *clusterController) withClusterSSHPreparationRecoveryReissuedProofEvery(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryReissuedProofClaim, interval time.Duration, consume clusterSSHPreparationRecoveryReissuedProofConsumer) (result error) {
	if issued.custody == nil || !issued.custody.started.CompareAndSwap(false, true) {
		return clusterbootstrap.ErrSessionAuthority
	}
	defer func() {
		if result != nil {
			issued.custody.failed.Store(true)
		}
	}()
	claim, expires, err := freezeClusterSSHPreparationRecoveryReissuedProof(issued)
	if err != nil || parent.Err() != nil || c == nil || aegis == nil || consume == nil || interval <= 0 || interval > 15*time.Second {
		return clusterbootstrap.ErrSessionAuthority
	}
	check, snapshot := newClusterSSHPreparationRecoveryTypedLeaseCheck(claim.custody, expires, func(ctx context.Context) error {
		for _, target := range claim.Claims {
			if aegis.verifyClusterSSHGeneration(ctx, target.Sealed.generation) != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
		}
		return nil
	}, func(ctx context.Context, expected map[string]int64, renew bool) (clusterSSHPreparationRecoveryReissuedProofAuthority, map[string]int64, error) {
		return c.observeClusterSSHPreparationRecoveryReissuedProof(ctx, claim, expected, renew)
	})
	return runClusterSSHPreparationScope(parent, interval, check, func(ctx context.Context) (result error) {
		readerCtx, cancel := context.WithCancel(ctx)
		lifetime := &clusterSSHNetworkReaderLifetime{ctx: readerCtx, cancel: cancel}
		defer func() {
			if lifetime.close() != nil {
				result = clusterbootstrap.ErrSessionAuthority
			}
		}()
		boundCheck := func(caller context.Context) error { return lifetime.run(caller, check) }
		value, err := snapshot()
		if err != nil || consume(readerCtx, value, boundCheck) != nil || readerCtx.Err() != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		return lifetime.close()
	})
}
