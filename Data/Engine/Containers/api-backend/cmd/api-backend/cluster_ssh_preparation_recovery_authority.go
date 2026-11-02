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
	"sync/atomic"
)

// Issued only after the original claim commits. Copies share this extra local
// one-use fence. It is not durable authority and cannot reconstruct a lost
// acknowledgement or a claim after restart; SQL remains authoritative.
type clusterSSHPreparationRecoveryCustody struct {
	assigned map[string]int64
	started  atomic.Bool
	failed   atomic.Bool
}

// Proof-only snapshot: no generic PreparationAuthority, native mutation or join
// capability and no decrypted or sealed credential material is returned.
type clusterSSHPreparationRecoveryAuthority struct {
	Reservation    clusterSSHPreparationRecoveryRecord
	Baseline       clusterbootstrap.Expected
	Cohort         clusterSSHInspectionCohort
	Source         clusterSSHSourceCohort
	Authorization  clusterSSHPreparationAuthorization
	IdentitySource engineidentity.Binding
	Leases         []clusterSSHTargetLease
	ExpiresAt      map[string]int64
}

func (clusterSSHPreparationRecoveryAuthority) String() string {
	return "SSH preparation recovery proof authority [redacted]"
}
func (clusterSSHPreparationRecoveryAuthority) GoString() string {
	return "SSH preparation recovery proof authority [redacted]"
}

func freezeClusterSSHPreparationRecoveryClaim(claim clusterSSHPreparationRecoveryClaim) (clusterSSHPreparationRecoveryClaim, map[string]int64, error) {
	fail := func() (clusterSSHPreparationRecoveryClaim, map[string]int64, error) {
		return clusterSSHPreparationRecoveryClaim{}, nil, clusterbootstrap.ErrSessionAuthority
	}
	if claim.custody == nil || claim.Baseline.Validate() != nil || len(claim.Claims) < 1 || len(claim.Claims) > 2 ||
		len(claim.Claims) != len(claim.Reservation.Targets) || len(claim.custody.assigned) != len(claim.Claims) || claim.payload == "" {
		return fail()
	}
	claim.Reservation.Targets = slices.Clone(claim.Reservation.Targets)
	claim.Claims = slices.Clone(claim.Claims)
	expires := maps.Clone(claim.custody.assigned)
	holder := claim.Claims[0].Lease.Holder
	if !clusterUUIDRE.MatchString(holder) || holder == "00000000-0000-0000-0000-000000000000" {
		return fail()
	}
	for i, target := range claim.Claims {
		lease, prior := target.Lease, claim.Reservation.Targets[i]
		if lease.Holder != holder || lease.Holder == prior.PreviousHolder || target.Previous != prior ||
			lease.TargetID != prior.TargetID || lease.Generation != prior.Generation || expires[prior.TargetID] < 1 ||
			lease.OperationID != claim.Reservation.OperationID || lease.OperationAttempt != claim.Reservation.Attempt ||
			lease.ControllerHolder != claim.Reservation.ControllerHolder || lease.OperationKind != "ssh_onboarding" ||
			lease.OperationStep != clusterSSHPreparationRecoveryOperationStep || lease.Step != clusterSSHPreparationRecoveryClaimStep ||
			!target.Sealed.binding.valid() || target.Sealed.binding.TargetID != prior.TargetID || target.Sealed.binding.OperationID != lease.OperationID ||
			target.Sealed.generation == "" || !strings.HasPrefix(target.Sealed.ciphertext, aegisEnvelopePrefix) {
			return fail()
		}
	}
	return claim, expires, nil
}

// One exact live read or renewal. Caller serializes its own expected-expiry
// lineage. SQL lock waits cannot refresh an expired claim; every sibling must
// still own its exact prior expiry before any renewal writes are made.
func (c *clusterController) observeClusterSSHPreparationRecovery(ctx context.Context, claim clusterSSHPreparationRecoveryClaim,
	expected map[string]int64, renew bool) (clusterSSHPreparationRecoveryAuthority, map[string]int64, error) {
	fail := func() (clusterSSHPreparationRecoveryAuthority, map[string]int64, error) {
		return clusterSSHPreparationRecoveryAuthority{}, nil, clusterbootstrap.ErrSessionAuthority
	}
	if c == nil || c.store == nil || c.store.db == nil || ctx.Err() != nil || claim.custody == nil || claim.custody.failed.Load() ||
		!claim.custody.started.Load() || len(expected) != len(claim.Claims) || len(claim.Claims) < 1 || len(claim.Claims) > 2 {
		return fail()
	}
	operation := clusterControllerOperation{ID: claim.Reservation.OperationID, Kind: "ssh_onboarding", State: "running",
		CurrentStep: clusterSSHPreparationRecoveryOperationStep, Attempt: claim.Reservation.Attempt}
	snapshot, err := c.readClusterSSHPreparationRecovery(ctx, operation)
	if err != nil || snapshot.payload != claim.payload || snapshot.work.Baseline != claim.Baseline ||
		!reflect.DeepEqual(snapshot.record, claim.Reservation) || len(snapshot.credentials) != len(claim.Claims) {
		return fail()
	}
	reports, keys := map[string]string{}, map[string]string{}
	holder := claim.Claims[0].Lease.Holder
	if !clusterUUIDRE.MatchString(holder) || holder == "00000000-0000-0000-0000-000000000000" {
		return fail()
	}
	for i, target := range snapshot.work.Proof.Cohort.Targets {
		prior := snapshot.record.Targets[i]
		lease := clusterSSHTargetLease{TargetID: prior.TargetID, OperationID: operation.ID, OperationAttempt: operation.Attempt,
			OperationKind: "ssh_onboarding", OperationStep: clusterSSHPreparationRecoveryOperationStep, ControllerHolder: c.holder,
			Step: clusterSSHPreparationRecoveryClaimStep, Holder: holder, Generation: prior.Generation}
		if claim.Claims[i].Sealed != snapshot.credentials[i] || claim.Claims[i].Previous != prior || claim.Claims[i].Lease != lease ||
			holder == prior.PreviousHolder || expected[target.Binding.TargetID] < 1 {
			return fail()
		}
		raw, err := json.Marshal(target.Report)
		if err != nil {
			return fail()
		}
		reports[target.Binding.TargetID], keys[target.Binding.TargetID] = string(raw), base64.StdEncoding.EncodeToString(target.Key.PublicKey)
	}
	targets := slices.Clone(snapshot.work.Proof.Cohort.Targets)
	slices.SortFunc(targets, func(a, b clusterSSHInspectedTarget) int {
		return strings.Compare(a.Binding.TargetID, b.Binding.TargetID)
	})
	expiries := maps.Clone(expected)
	qualification := clusterSSHQualificationWork{Proof: snapshot.work.Proof, Source: snapshot.work.Source}
	tx, err := c.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fail()
	}
	defer tx.Rollback()
	if lockClusterSSHQualification(ctx, tx, operation.ID, targets) != nil || checkClusterSSHQualificationSource(ctx, tx, qualification) != nil ||
		checkClusterSSHPreparationRecoveryClock(ctx, tx, snapshot, claim.payload, "running", clusterSSHPreparationRecoveryClaimStep, holder, reports, keys, expiries) != nil {
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
	}
	if checkClusterSSHQualificationSource(ctx, tx, qualification) != nil ||
		checkClusterSSHPreparationRecoveryClock(ctx, tx, snapshot, claim.payload, "running", clusterSSHPreparationRecoveryClaimStep, holder, reports, keys, expiries) != nil ||
		ctx.Err() != nil || claim.custody.failed.Load() ||
		tx.Commit() != nil {
		return fail()
	}
	// Connection returned before shaping the value handed to proof code.
	value := clusterSSHPreparationRecoveryAuthority{Reservation: snapshot.record, Baseline: snapshot.work.Baseline,
		Cohort: snapshot.work.Proof.Cohort, Source: snapshot.work.Proof.Source, Authorization: snapshot.work.Authorization,
		IdentitySource: snapshot.work.IdentitySource, ExpiresAt: maps.Clone(expiries)}
	for _, target := range claim.Claims {
		value.Leases = append(value.Leases, target.Lease)
	}
	return value, expiries, nil
}
