package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"slices"
)

// Original inventory belongs to fresh source publication. Later authority
// checks run after owned package effects and retain their separate journal and
// host-prefix proofs; this selection never supplies recovery authority.
func verifyBootstrapTargetPackages(ctx context.Context, expected clusterbootstrap.PreparationExpected, action string,
	read func(context.Context, string) ([]byte, error)) error {
	if ctx.Err() != nil || expected.Validate() != nil {
		return errBootstrapSource
	}
	if action != "stage_source" {
		return nil
	}
	if expected.TargetPackages == nil || read == nil {
		return errBootstrapSource
	}
	raw, err := read(ctx, "packages")
	if err != nil || ctx.Err() != nil {
		return errBootstrapSource
	}
	return validateBootstrapTargetPackages(expected.TargetPackages, raw)
}

func validateBootstrapTargetPackages(approved *clusterbootstrap.HostPackageSelection, raw []byte) error {
	if approved == nil || approved.Validate() != nil {
		return errBootstrapSource
	}
	plan, _, err := approved.Plan()
	if err != nil {
		return errBootstrapSource
	}
	observed, err := clusterbootstrap.SelectHostPackagesForSuite(raw, plan.Suite)
	if err != nil || observed.PlanSHA256 != approved.PlanSHA256 || observed.InventorySHA256 != approved.InventorySHA256 ||
		!slices.Equal(observed.Missing, approved.Missing) {
		return errBootstrapSource
	}
	return nil
}

// Used only by fresh host preparation. Docker's original inventory is obsolete
// after selected base packages install; when no base package is selected,
// Docker is the first package effect and must still match that approval.
func bootstrapTargetPackageInventoryCheck(approved *clusterbootstrap.HostPackageSelection, phase bootstrapHostCommand) func([]byte) error {
	if approved != nil && phase == bootstrapHostInstallDocker {
		pins, err := approved.Pins()
		if err == nil {
			for _, pin := range pins {
				if pin.Phase == "base" {
					return nil
				}
			}
		}
	}
	return func(raw []byte) error { return validateBootstrapTargetPackages(approved, raw) }
}
