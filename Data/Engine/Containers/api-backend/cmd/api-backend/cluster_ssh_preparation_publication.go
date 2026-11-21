package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"reflect"
	"slices"
	"time"
)

// Read every static publication required for preparation using native release
// resolvers, not picker caches or caller-supplied receipts. This proves published
// immutable identities and authenticated inventories, not staged bytes, dynamic
// PostgreSQL image identity, capacity, target ownership or mutation permission.
func readClusterSSHPreparationPublication(ctx context.Context, expected clusterbootstrap.Expected, version string) (string, error) {
	bound, err := bindClusterSSHPreparationPublication(ctx, expected, version)
	return bound.digest, err
}

type clusterSSHPreparationBoundPublication struct {
	digest  string
	recheck func(context.Context) error
}

// Authenticate the static inventory bytes at acquisition. Later observations
// resolve the immutable release and tag again and match every selected asset,
// including its content digest, against this private copy.
func bindClusterSSHPreparationPublication(ctx context.Context, expected clusterbootstrap.Expected, version string) (clusterSSHPreparationBoundPublication, error) {
	fail := func() (clusterSSHPreparationBoundPublication, error) {
		return clusterSSHPreparationBoundPublication{}, clusterbootstrap.ErrPreparationConfig
	}
	if ctx.Err() != nil || expected.Validate() != nil || version != clusterbootstrap.K3sPins().Version {
		return fail()
	}
	_, bootstrap, err := readClusterSSHBootstrapPublication(ctx, expected)
	if err != nil {
		return fail()
	}
	application, err := resolveClusterSSHImageInventory(ctx, expected)
	if err != nil {
		return fail()
	}
	external, err := resolveClusterSSHExternalInventory(ctx, expected)
	if err != nil {
		return fail()
	}
	k3s, err := resolveClusterSSHK3sInventory(ctx, expected, version)
	if err != nil {
		return fail()
	}
	bound, err := clusterSSHPreparationGroupPublication(expected, version, bootstrap, application.assets, external.assets, k3s.assets)
	if err != nil || bound.recheck(ctx) != nil {
		return fail()
	}
	return bound, nil
}

// Bind the static inventories or the complete set including host packages.
// Each recheck compares fresh release and tag data against the same private asset map.
func clusterSSHPreparationGroupPublication(expected clusterbootstrap.Expected, version string, groups ...map[string]clusterBootstrapAsset) (clusterSSHPreparationBoundPublication, error) {
	fail := func() (clusterSSHPreparationBoundPublication, error) {
		return clusterSSHPreparationBoundPublication{}, clusterbootstrap.ErrPreparationConfig
	}
	if expected.Validate() != nil || version != clusterbootstrap.K3sPins().Version || (len(groups) != 4 && len(groups) != 5) {
		return fail()
	}
	assets := map[string]clusterBootstrapAsset{}
	limits := map[string]int64{}
	ids := map[int64]bool{}
	for _, group := range groups {
		if len(group) == 0 {
			return fail()
		}
		for name, asset := range group {
			if _, exists := assets[name]; exists || ids[asset.ID] {
				return fail()
			}
			assets[name], limits[name], ids[asset.ID] = asset, asset.Size, true
		}
	}
	recheck := func(ctx context.Context) error {
		fresh, err := resolveClusterPackagedAssets(ctx, expected, limits)
		if err != nil || ctx.Err() != nil || !reflect.DeepEqual(fresh, assets) {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
	digest, err := clusterSSHPreparationDigest(struct {
		Version    int
		Source     clusterbootstrap.Expected
		K3sVersion string
		Assets     map[string]clusterBootstrapAsset
	}{1, expected, version, assets})
	if err != nil {
		return fail()
	}
	return clusterSSHPreparationBoundPublication{digest, recheck}, nil
}

// Bind native publication proof to a waiting proposal owned by this controller.
// All SQL reads return their connections before GitHub IO. Reloading both sides
// fences cancellation, changed credentials, report expiry and changed proposal.
// This is only the static-publication component of immutable-artifact readiness:
// it is deliberately not installed as the complete readiness gate.
func (c *clusterController) preparationPublicationRead(snapshot clusterSSHPreparationApprovalSnapshot) func(context.Context, string) (string, error) {
	plan, planErr := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	baseline, source, payload := snapshot.work.Baseline, snapshot.work.Source, snapshot.work.Payload
	credentials := slices.Clone(snapshot.credentials)
	operation := clusterControllerOperation{ID: snapshot.work.Authorization.OperationID, Attempt: snapshot.work.Authorization.Attempt,
		Kind: "ssh_onboarding", State: "waiting", CurrentStep: clusterSSHQualificationStep}
	var version string
	versionErr := clusterBootstrapObject([]byte(payload), map[string]any{"source_k3s_version": &version})
	return func(parent context.Context, requestedPlan string) (string, error) {
		fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
		if c == nil || planErr != nil || versionErr != nil || requestedPlan != plan || parent.Err() != nil {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		check := func() bool {
			current, _, err := c.loadClusterSSHPreparationApproval(ctx, operation)
			if err != nil {
				return false
			}
			currentPlan, err := clusterSSHPreparationPlanDigest(current.work.Authorization)
			return err == nil && currentPlan == plan && current.work.Baseline == baseline && current.work.Source == source &&
				current.work.Payload == payload && slices.Equal(current.credentials, credentials) && ctx.Err() == nil
		}
		if !check() {
			return fail()
		}
		publication, err := readClusterSSHPreparationPublication(ctx, baseline, version)
		if err != nil || !check() || ctx.Err() != nil {
			return fail()
		}
		return clusterSSHPreparationDigest([]string{"preparation-static-publication-v1", plan, publication})
	}
}
