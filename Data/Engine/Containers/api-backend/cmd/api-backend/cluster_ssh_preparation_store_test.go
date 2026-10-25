package main

import (
	"strings"
	"testing"
)

func clusterSSHPreparationResultFixture(lease clusterSSHTargetLease, confirmation string) clusterSSHPreparationResult {
	return clusterSSHPreparationResult{Version: 1, Attempt: lease.OperationAttempt, TargetID: lease.TargetID, Generation: lease.Generation,
		ConfirmationSHA256: confirmation, StageSourceSHA256: strings.Repeat("1", 64), PrepareHostSHA256: strings.Repeat("2", 64),
		StageImagesSHA256: strings.Repeat("3", 64), JoinClusterSHA256: strings.Repeat("4", 64)}
}

func TestClusterSSHPreparationResultRequiresExactClaimAndJournalProofs(t *testing.T) {
	_, _, lease, _ := sshPreparationFixture(t)
	result := clusterSSHPreparationResultFixture(lease, strings.Repeat("a", 64))
	if !result.valid(lease) {
		t.Fatal("valid preparation result rejected")
	}
	for _, mode := range []string{"version", "attempt", "target", "generation", "confirmation", "source", "host", "images", "join", "inspection lease"} {
		t.Run(mode, func(t *testing.T) {
			changed, changedLease := result, lease
			switch mode {
			case "version":
				changed.Version++
			case "attempt":
				changed.Attempt++
			case "target":
				changed.TargetID = newClusterUUID()
			case "generation":
				changed.Generation++
			case "confirmation":
				changed.ConfirmationSHA256 = strings.Repeat("0", 64)
			case "source":
				changed.StageSourceSHA256 = "bad"
			case "host":
				changed.PrepareHostSHA256 = "bad"
			case "images":
				changed.StageImagesSHA256 = "bad"
			case "join":
				changed.JoinClusterSHA256 = "bad"
			case "inspection lease":
				changedLease.OperationStep = clusterSSHInspectionOperationStep
			}
			if changed.valid(changedLease) {
				t.Fatal("changed preparation result accepted")
			}
		})
	}
}

func TestClusterSSHPreparationReceiptRequiresExactCohortProof(t *testing.T) {
	cohort, source, lease, _ := sshPreparationFixture(t)
	proof := clusterSSHParentProof{Version: 1, Cohort: cohort, Source: source}
	confirmation := strings.Repeat("a", 64)
	receipt := clusterSSHPreparationReceipt{Version: 1, Attempt: cohort.Attempt, ConfirmationSHA256: confirmation}
	for _, target := range cohort.Targets {
		targetLease := lease
		targetLease.TargetID = target.Binding.TargetID
		targetLease.Generation = target.Generation + 1
		receipt.Targets = append(receipt.Targets, clusterSSHPreparationResultFixture(targetLease, confirmation))
	}
	raw := marshalClusterJSON(map[string]any{"ssh_preparation": receipt})
	if parsed, err := parseClusterSSHPreparationReceipt(raw, proof); err != nil || len(parsed.Targets) != len(cohort.Targets) {
		t.Fatal("valid preparation receipt rejected", err)
	}
	changedGeneration := receipt
	changedGeneration.Targets = append([]clusterSSHPreparationResult(nil), receipt.Targets...)
	changedGeneration.Targets[0].Generation = cohort.Targets[0].Generation
	mixedConfirmation := receipt
	mixedConfirmation.Targets = append([]clusterSSHPreparationResult(nil), receipt.Targets...)
	mixedConfirmation.Targets[0].ConfirmationSHA256 = strings.Repeat("b", 64)
	reordered := receipt
	reordered.Targets = append([]clusterSSHPreparationResult(nil), receipt.Targets...)
	reordered.Targets[0], reordered.Targets[1] = reordered.Targets[1], reordered.Targets[0]
	for mode, invalid := range map[string]string{
		"missing":            `{}`,
		"unknown":            strings.Replace(raw, `"version":1`, `"version":1,"unknown":true`, 1),
		"duplicate":          strings.Replace(raw, `"version":1`, `"version":1,"version":1`, 1),
		"old generation":     marshalClusterJSON(map[string]any{"ssh_preparation": changedGeneration}),
		"mixed confirmation": marshalClusterJSON(map[string]any{"ssh_preparation": mixedConfirmation}),
		"reordered":          marshalClusterJSON(map[string]any{"ssh_preparation": reordered}),
	} {
		t.Run(mode, func(t *testing.T) {
			if _, err := parseClusterSSHPreparationReceipt(invalid, proof); err == nil {
				t.Fatal("changed preparation receipt accepted")
			}
		})
	}
}
