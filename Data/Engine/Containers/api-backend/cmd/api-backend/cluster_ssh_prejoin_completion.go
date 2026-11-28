package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"reflect"
	"slices"
	"time"
)

// Caller-owned expected inputs and native evidence survive successful source
// scope cleanup. They do not carry authority to settle themselves.
type clusterSSHPrejoinCompletion struct {
	PayloadSHA256 string
	Claim         clusterSSHNetworkTargetClaim
	Expected      clusterbootstrap.PreparationExpected
	Receipt       clusterbootstrap.PrejoinPreparationReceipt
}

func verifyClusterSSHPrejoinCompletion(ctx context.Context, read clusterSSHPreparationAuthorityRead,
	expected clusterbootstrap.PreparationExpected, receipt clusterbootstrap.PrejoinPreparationReceipt,
	getJSON func(context.Context, string, any) error, cohortNodes map[string]clusterbootstrap.K3sJoinedNode) (clusterSSHPreparationCompletion, error) {
	if getJSON == nil {
		return clusterSSHPreparationCompletion{}, errClusterConflict
	}
	return verifyClusterSSHPrejoinCompletionRead(ctx, read, expected, receipt, func(ctx context.Context, read clusterSSHPreparationAuthorityRead) (clusterSSHJoinedTargets, error) {
		return readClusterSSHJoinedTargets(ctx, read, getJSON)
	}, cohortNodes)
}

// A typed broker reader carries only independently observed joined identities,
// never a generic Kubernetes proxy or permission to accept caller-owned Nodes.
type clusterSSHPrejoinJoinedRead func(context.Context, clusterSSHPreparationAuthorityRead, clusterSSHNetworkTargetClaim, clusterbootstrap.Expected) (clusterSSHJoinedTargets, error)

func verifyClusterSSHPrejoinCompletionRead(ctx context.Context, read clusterSSHPreparationAuthorityRead,
	expected clusterbootstrap.PreparationExpected, receipt clusterbootstrap.PrejoinPreparationReceipt,
	joined func(context.Context, clusterSSHPreparationAuthorityRead) (clusterSSHJoinedTargets, error), cohortNodes map[string]clusterbootstrap.K3sJoinedNode) (clusterSSHPreparationCompletion, error) {
	fail := func() (clusterSSHPreparationCompletion, error) {
		return clusterSSHPreparationCompletion{}, errClusterConflict
	}
	if read == nil || joined == nil || len(cohortNodes) < 1 || len(cohortNodes) > 2 || ctx.Err() != nil {
		return fail()
	}
	authority, err := read(ctx)
	if err != nil || authority.IdentitySource == nil || receipt.Validate(expected, *authority.IdentitySource) != nil {
		return fail()
	}
	owned, err := buildClusterSSHPreparationExpected(authority.Cohort, authority.Source, authority.Lease, authority.Baseline, authority.K3sVersion, expected.PodCIDR, expected.ServiceCIDR, authority.payload)
	if err != nil || !reflect.DeepEqual(owned, expected) {
		return fail()
	}
	inspection, _, err := parseClusterSSHPreparationInspection([]byte(authority.payload), authority.Lease, authority.Baseline)
	if err != nil {
		return fail()
	}
	authorization, _, err := parseClusterSSHPreparationAuthorization(authority.payload, inspection, authority.Queue)
	if err != nil {
		return fail()
	}
	stable := authority
	stable.Cohort.ObservedAt = 0
	digest, err := clusterSSHPreparationDigest(stable)
	if err != nil {
		return fail()
	}
	observed, err := joined(ctx, read)
	if err != nil || observed.AuthoritySHA256 != digest || len(observed.Nodes) != len(authority.Cohort.Targets) || observed.Nodes[expected.Target.TargetID] != receipt.Node {
		return fail()
	}
	if len(cohortNodes) != len(observed.Nodes) {
		return fail()
	}
	for id, node := range cohortNodes {
		if !node.Ready || observed.Nodes[id] != node {
			return fail()
		}
	}
	result := clusterSSHPreparationResult{Version: 2, Attempt: authority.Lease.OperationAttempt, TargetID: authority.Lease.TargetID, Generation: authority.Lease.Generation,
		ConfirmationSHA256: authorization.ConfirmationSHA256, StageSourceSHA256: receipt.Steps[0].ResultSHA256, PrepareHostSHA256: receipt.Steps[4].ResultSHA256, StageImagesSHA256: receipt.Steps[5].ResultSHA256,
		Prejoin: &clusterSSHPrejoinResult{Expected: owned, Receipt: receipt, ControllerSHA256: digest}}
	if !result.valid(authority.Lease) || ctx.Err() != nil {
		return fail()
	}
	return clusterSSHPreparationCompletion{Authority: authority, Result: result}, nil
}

// Original-controller evidence, read locally or through its fixed authenticated
// broker. Independent Node observations repeat for the whole exact cohort before
// the existing short atomic SQL settlement. No DB connection spans network IO.
// Native voting-member identity is retained; admission still needs independent
// full membership, health and consumer proof and is deliberately not enabled.
func (s *postgresOperatorStore) completeClusterSSHPrejoinPreparation(parent context.Context, aegis *goAegisService,
	targets []clusterSSHPrejoinCompletion, getJSON func(context.Context, string, any) error) error {
	if getJSON == nil {
		return errClusterConflict
	}
	return s.completeClusterSSHPrejoinPreparationRead(parent, aegis, targets,
		func(ctx context.Context, read clusterSSHPreparationAuthorityRead, _ clusterSSHNetworkTargetClaim, _ clusterbootstrap.Expected) (clusterSSHJoinedTargets, error) {
			return readClusterSSHJoinedTargets(ctx, read, getJSON)
		})
}

func (s *postgresOperatorStore) completeClusterSSHPrejoinPreparationRead(parent context.Context, aegis *goAegisService,
	targets []clusterSSHPrejoinCompletion, joined clusterSSHPrejoinJoinedRead) error {
	if s == nil || s.db == nil || aegis == nil || joined == nil || len(targets) < 1 || len(targets) > 2 || parent.Err() != nil {
		return errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	targets = slices.Clone(targets)
	cohortNodes := map[string]clusterbootstrap.K3sJoinedNode{}
	ids, nodes, members := map[string]bool{}, map[string]bool{}, map[uint64]bool{}
	for i := range targets {
		target := &targets[i]
		target.Expected.PeerAddresses = slices.Clone(target.Expected.PeerAddresses)
		id := target.Claim.Lease.TargetID
		if !validClusterSSHPreparationDigest(target.PayloadSHA256) || ids[id] || nodes[target.Receipt.Node.NodeUID] || members[target.Receipt.MemberID] {
			return errClusterConflict
		}
		ids[id], nodes[target.Receipt.Node.NodeUID], members[target.Receipt.MemberID] = true, true, true
		cohortNodes[id] = target.Receipt.Node
	}
	var completions []clusterSSHPreparationCompletion
	for round := 0; round < 2; round++ {
		current := make([]clusterSSHPreparationCompletion, 0, len(targets))
		for i, target := range targets {
			read := newClusterSSHPreparationAuthorityRead(s, aegis, target.Claim.Lease, target.Expected.Source, target.Claim.Sealed)
			value, err := verifyClusterSSHPrejoinCompletionRead(ctx, read, target.Expected, target.Receipt, func(ctx context.Context, read clusterSSHPreparationAuthorityRead) (clusterSSHJoinedTargets, error) {
				return joined(ctx, read, target.Claim, target.Expected.Source)
			}, cohortNodes)
			if err != nil || value.Authority.PayloadSHA256 != target.PayloadSHA256 || len(value.Authority.Cohort.Targets) != len(targets) {
				return errClusterConflict
			}
			if round == 1 && !reflect.DeepEqual(value.Result, completions[i].Result) {
				return errClusterConflict
			}
			value.Sealed = target.Claim.Sealed
			current = append(current, value)
		}
		completions = current
	}
	if ctx.Err() != nil {
		return errClusterConflict
	}
	return s.completeClusterSSHPreparationVersion(ctx, completions, 2)
}
