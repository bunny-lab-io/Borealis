package clusterbootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
)

// PreparationPlan describes proposed inherited configuration, without a future
// worker identity. It cannot be imported as PreparationExpected or used to
// render an environment, authorize host writes or prove target capacity.
type PreparationPlan struct {
	Source                                Expected
	PlanSHA256, KubeSystemUID, K3sVersion string
	ManagementCIDR, PodCIDR, ServiceCIDR  string
	ControlPlaneVIP, EdgeVIP              string
	PeerAddresses, TargetAddresses        []string
}

// PreparationPlanProfileLimits projects the same inherited profile used after
// claim, without inventing a future worker identity. These are workload bounds;
// target hardware, infrastructure and resident/overlap demand remain required.
func PreparationPlanProfileLimits(plan PreparationPlan, runtime map[string]string) (PreparationSizing, []PreparationApplicationLimit, PreparationWorkerLimits, error) {
	fail := func() (PreparationSizing, []PreparationApplicationLimit, PreparationWorkerLimits, error) {
		return PreparationSizing{}, nil, PreparationWorkerLimits{}, ErrPreparationConfig
	}
	if _, err := PreparationPlanConfigurationDigest(plan, runtime); err != nil {
		return fail()
	}
	sizing, err := preparationSizingFromRuntime(runtime)
	if err != nil {
		return fail()
	}
	applications, err := preparationProfileApplicationLimits(sizing, runtime)
	if err != nil {
		return fail()
	}
	workers, err := preparationProfileWorkerLimits(runtime)
	if err != nil {
		return fail()
	}
	return sizing, applications, workers, nil
}

// PreparationPlanConfigurationDigest validates current source settings and
// the whole planned network. Private runtime values remain local. The digest
// is an equality receipt, never a substitute for fresh source authority or the
// later exact claimed PreparationConfiguration and its complete wire bound.
func PreparationPlanConfigurationDigest(plan PreparationPlan, runtime map[string]string) (string, error) {
	if plan.Source.Validate() != nil || !digestPattern.MatchString(plan.PlanSHA256) || !nonzeroPreparationUUID(plan.KubeSystemUID) ||
		plan.K3sVersion != K3sPins().Version || len(plan.TargetAddresses) < 1 || len(plan.TargetAddresses) > 2 || validatePreparationRuntime(runtime) != nil {
		return "", ErrPreparationConfig
	}
	if _, err := preparationSizingFromRuntime(runtime); err != nil {
		return "", ErrPreparationConfig
	}
	seen := map[string]bool{}
	for _, address := range plan.TargetAddresses {
		if seen[address] || validatePreparationNetwork(plan.ManagementCIDR, plan.PodCIDR, plan.ServiceCIDR, plan.ControlPlaneVIP, plan.EdgeVIP, address, plan.PeerAddresses) != nil {
			return "", ErrPreparationConfig
		}
		seen[address] = true
	}
	plan.PeerAddresses, plan.TargetAddresses = slices.Clone(plan.PeerAddresses), slices.Clone(plan.TargetAddresses)
	settings := make(map[string]string, len(runtime))
	for key, value := range runtime {
		settings[key] = value
	}
	raw, err := json.Marshal(struct {
		Version int
		Plan    PreparationPlan
		Runtime map[string]string
	}{1, plan, settings})
	defer clear(raw)
	if err != nil || len(raw) > MaxPreparationConfigBytes {
		return "", ErrPreparationConfig
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
