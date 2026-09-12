package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// The inspection claim cannot authorize preparation. These names are reserved
// for the future guarded transition/dispatcher; current DB claim paths still
// reject them and bootstrap-session still accepts verify only.
const clusterSSHPreparationOperationStep = "prepare_ssh_targets"

// buildClusterSSHPreparationExpected binds source configuration to the exact
// planned cohort, fresh stage_source worker generation and inspected host.
// Pod/service CIDRs come from a fixed source observer, never installer defaults.
// Caller must compare baseline and all observations under current authority.
func buildClusterSSHPreparationExpected(cohort clusterSSHInspectionCohort, source clusterSSHSourceCohort, lease clusterSSHTargetLease,
	baseline clusterbootstrap.Expected, k3sVersion, podCIDR, serviceCIDR string) (clusterbootstrap.PreparationExpected, error) {
	fail := func() (clusterbootstrap.PreparationExpected, error) {
		return clusterbootstrap.PreparationExpected{}, clusterbootstrap.ErrPreparationConfig
	}
	if baseline.Validate() != nil || validateClusterSSHInspectionCohort(cohort, source) != nil ||
		lease.ControllerHolder != cohort.ControllerHolder || lease.OperationID != cohort.OperationID || lease.OperationAttempt != cohort.Attempt ||
		lease.OperationKind != "ssh_onboarding" || lease.OperationStep != clusterSSHPreparationOperationStep || lease.Step != "stage_source" {
		return fail()
	}
	peers := make([]string, 0, 3)
	for _, member := range source.Members {
		peers = append(peers, member.Address)
	}
	for _, target := range cohort.Targets {
		peers = append(peers, target.Binding.Address)
	}
	sort.Strings(peers)
	// Bind every original report, approved wire key and source member identity.
	// Only the read's clock advances; inspected_at remains part of the proof.
	stableCohort := cohort
	stableCohort.ObservedAt = 0
	raw, err := json.Marshal(clusterSSHParentProof{Version: 1, Cohort: stableCohort, Source: source})
	if err != nil || len(raw) > 128<<10 {
		return fail()
	}
	digest := sha256.Sum256(raw)
	for _, target := range cohort.Targets {
		if target.Binding.TargetID != lease.TargetID {
			continue
		}
		if lease.Generation <= target.Generation {
			return fail()
		}
		binding := clusterbootstrap.SessionBinding{ClusterID: source.ClusterID, OperationID: cohort.OperationID, TargetID: target.Binding.TargetID,
			HolderID: lease.Holder, Generation: lease.Generation, OperationAttempt: cohort.Attempt, Address: target.Binding.Address, Port: target.Binding.Port,
			Hostname: target.Report.Hostname, MachineID: target.Report.MachineID, HostKeyAlgorithm: target.Key.Algorithm, HostKeyFingerprint: target.Key.Fingerprint}
		expected := clusterbootstrap.PreparationExpected{Source: baseline, Target: binding, TargetBootID: target.Report.BootID, KubeSystemUID: source.KubeSystemUID,
			CohortSHA256:    hex.EncodeToString(digest[:]),
			ControlPlaneVIP: source.ControlPlaneVIP, EdgeVIP: source.EdgeVIP, ManagementCIDR: target.Report.ConnectedPrefix, K3sVersion: k3sVersion,
			PodCIDR: podCIDR, ServiceCIDR: serviceCIDR, PeerAddresses: peers}
		if expected.Validate() != nil {
			return fail()
		}
		return expected, nil
	}
	return fail()
}

// prepareClusterSSHTargetInputs joins immutable archive verification with the
// private pre-join configuration contract. Runtime contains only explicit
// shared settings selected by the source reader, never arbitrary environment.
// read must freshly verify source, cohort, Aegis and exact target claim each
// time, then return current binding/settings after releasing its DB connection.
// The assembler compares the complete canonical configuration across reads.
// No callback invokes SSH or starts a host mutation. The guarded source reader,
// qualification transition and remote dispatcher remain separate integration.
type clusterSSHPreparationRead func(context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error)

func prepareClusterSSHTargetInputs(parent context.Context, expected clusterbootstrap.PreparationExpected, runtime map[string]string,
	scratchParent string, read clusterSSHPreparationRead) (*clusterbootstrap.PreparationInputs, error) {
	return prepareClusterSSHTargetInputsWith(parent, expected, runtime, scratchParent, read, prepareClusterSSHBootstrap)
}

// Package-private downloader seam permits deterministic ownership-loss tests.
// Production always uses the fresh GitHub publication/asset verifier above.
func prepareClusterSSHTargetInputsWith(parent context.Context, expected clusterbootstrap.PreparationExpected, runtime map[string]string,
	scratchParent string, read clusterSSHPreparationRead,
	download func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error)) (*clusterbootstrap.PreparationInputs, error) {
	if expected.Validate() != nil || read == nil || download == nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	config, err := clusterbootstrap.NewPreparationConfiguration(expected, runtime)
	if err != nil {
		return nil, err
	}
	check := newClusterSSHPreparationInputCheck(config, read)
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	bundle, err := download(ctx, expected.Source, scratchParent)
	if err != nil {
		if bundle != nil {
			_ = bundle.Close()
		}
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
		if bundle != nil {
			_ = bundle.Close()
		}
		return nil, clusterbootstrap.ErrSessionAuthority
	}
	inputs, err := clusterbootstrap.BindPreparationInputs(ctx, bundle, config, check)
	if err != nil {
		if bundle != nil {
			_ = bundle.Close()
		}
		return nil, err
	}
	return inputs, nil
}

func newClusterSSHPreparationInputCheck(config *clusterbootstrap.PreparationConfiguration, read clusterSSHPreparationRead) func(context.Context) error {
	originalDigest, originalErr := config.Digest()
	return func(ctx context.Context) error {
		if originalErr != nil || read == nil || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		current, settings, err := read(ctx)
		if err != nil || ctx.Err() != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		observed, err := clusterbootstrap.NewPreparationConfiguration(current, settings)
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		digest, err := observed.Digest()
		if err != nil || digest != originalDigest {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
}
