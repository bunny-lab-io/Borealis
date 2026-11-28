package main

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestClusterSSHPreparationPackageReadinessBinding(t *testing.T) {
	snapshot, _ := sshPreparationCleanTargetsFixture(t, false)
	plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	proof := clusterSSHPreparationApprovalProof{plan: plan}
	for i := range proof.receipts {
		proof.receipts[i] = strings.Repeat("a", 64)
	}
	sshPreparationApprovalCapacityProof(t, snapshot, &proof)
	readiness := clusterSSHPreparationReadiness(plan, proof.receipts)
	readiness.Version, readiness.Targets, readiness.Capacity, readiness.Packages = 4, proof.targetCPU, proof.capacity, proof.packages
	raw, _ := json.Marshal(readiness)
	if len(raw) > 8<<10 || parseClusterSSHPreparationReadiness(raw, snapshot.work.Authorization) != nil {
		t.Fatalf("complete paired maximum-selection receipt rejected (%d bytes)", len(raw))
	}
	payload := func(r clusterSSHPreparationReadinessReceipt) string {
		return marshalClusterJSON(map[string]any{"ssh_preparation_readiness": r, "ssh_preparation_authorization": snapshot.work.Authorization})
	}
	for i, target := range readiness.Targets {
		got, err := clusterSSHPreparationExpectedPackages(payload(readiness), target.TargetID)
		if err != nil || got == nil || !reflect.DeepEqual(*got, readiness.Packages[i].Selection) {
			t.Fatal("approved selection not restored for exact target", err)
		}
		got.Missing[0] = "foreign"
		again, err := clusterSSHPreparationExpectedPackages(payload(readiness), target.TargetID)
		if err != nil || !reflect.DeepEqual(*again, readiness.Packages[i].Selection) {
			t.Fatal("caller changed retained selection", err)
		}
	}
	if _, err := clusterSSHPreparationExpectedPackages(payload(readiness), newClusterUUID()); err == nil {
		t.Fatal("foreign target obtained selection")
	}
	for _, mode := range []string{"missing", "inventory", "missing list", "target", "observation", "swap", "digest", "null missing", "downgrade"} {
		t.Run(mode, func(t *testing.T) {
			changed := readiness
			changed.Targets = slices.Clone(readiness.Targets)
			changed.Packages = cloneClusterSSHPreparationPackages(readiness.Packages)
			switch mode {
			case "missing":
				changed.Packages = nil
			case "inventory":
				changed.Packages[0].Selection.InventorySHA256 = strings.Repeat("f", 64)
			case "missing list":
				changed.Packages[0].Selection.Missing = changed.Packages[0].Selection.Missing[1:]
			case "target":
				changed.Packages[0].TargetID = newClusterUUID()
			case "observation":
				changed.Packages[0].ObservationSHA256 = strings.Repeat("f", 64)
			case "swap":
				changed.Packages[0], changed.Packages[1] = changed.Packages[1], changed.Packages[0]
			case "digest":
				changed.Targets[0].PackagesSHA256 = strings.Repeat("f", 64)
			case "null missing":
				changed.Packages[0].Selection.Missing = nil
			case "downgrade":
				changed.Version = 3
			}
			if _, err := clusterSSHPreparationExpectedPackages(payload(changed), readiness.Targets[0].TargetID); err == nil {
				t.Fatal("unapproved selection accepted")
			}
		})
	}
	// Retained receipts remain readable, but cannot acquire fresh mutation proof.
	for _, version := range []int{1, 2, 3} {
		legacy := readiness
		legacy.Version, legacy.Packages = version, nil
		legacy.Targets = slices.Clone(readiness.Targets)
		legacy.Checks = slices.Clone(readiness.Checks)
		for i := range legacy.Targets {
			legacy.Targets[i].PackagesSHA256 = ""
		}
		legacy.Checks[1].SHA256, _ = clusterSSHPreparationTargetCPUDigest(snapshot, legacy.Targets)
		capacity := readiness.Capacity.clone()
		capacity.CleanSHA256 = legacy.Checks[1].SHA256
		legacy.Capacity = &capacity
		legacy.Checks[2].SHA256, _ = capacity.digest(plan, legacy.Targets)
		if version < 3 {
			legacy.Capacity = nil
		}
		if version == 1 {
			legacy.Targets = nil
		}
		got, err := clusterSSHPreparationExpectedPackages(payload(legacy), readiness.Targets[0].TargetID)
		if err != nil || got != nil {
			t.Fatalf("legacy v%d gained package proof or lost reconciliation: %v", version, err)
		}
	}
}
