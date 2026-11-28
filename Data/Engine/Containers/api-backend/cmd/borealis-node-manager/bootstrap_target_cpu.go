package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
)

// Called by the normal native source verifier before journal-root creation or
// first target effect, and again by each held authority check. A retained legacy
// recovery may keep its old binding; a fresh publication cannot invent CPU proof.
func verifyBootstrapTargetCPU(ctx context.Context, expected clusterbootstrap.PreparationExpected, action string,
	read func(context.Context, string, func(context.Context) error) (clusterbootstrap.PreparationTargetCPU, error)) error {
	if ctx.Err() != nil || expected.Validate() != nil {
		return errBootstrapSource
	}
	if expected.TargetCPU == nil {
		if action == "stage_source" {
			return errBootstrapSource
		}
		return nil
	}
	if read == nil {
		return errBootstrapSource
	}
	actual, err := read(ctx, expected.K3sVersion, func(ctx context.Context) error { return ctx.Err() })
	if err != nil || ctx.Err() != nil || actual != *expected.TargetCPU || actual.Validate(expected.K3sVersion) != nil {
		return errBootstrapSource
	}
	return nil
}
