package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"sync"
)

type clusterSSHPrejoinWorkerStorageCheck func(context.Context, string, clusterbootstrap.PreparationExpected, clusterSSHPreparedImageCapacity, clusterSSHPreparationChecks) error

// Lend only copied storage from the already verified source acquisition. The
// worker brackets consumption with its existing full Inputs proof; this accessor
// retains that scope's authority and lifetime without another inventory/Job.
func newClusterSSHPrejoinWorkerStorageRead(parent context.Context, value clusterSSHStorageSnapshot, check func(context.Context) error) func(context.Context) (clusterSSHStorageSnapshot, error) {
	raw, encodeErr := json.Marshal(value)
	collector := value.Requirements.observation
	return func(ctx context.Context) (clusterSSHStorageSnapshot, error) {
		var owned clusterSSHStorageSnapshot
		if encodeErr != nil || check == nil || parent.Err() != nil || ctx.Err() != nil || check(ctx) != nil || json.Unmarshal(raw, &owned) != nil {
			return owned, clusterbootstrap.ErrPreparationConfig
		}
		owned.Requirements.observation = collector
		if parent.Err() != nil || ctx.Err() != nil || check(ctx) != nil {
			return clusterSSHStorageSnapshot{}, clusterbootstrap.ErrPreparationConfig
		}
		return owned, nil
	}
}

func validClusterSSHPrejoinWorkerCapacity(value clusterSSHPreparedImageCapacity) bool {
	if value.WorkerScratchBytes == 0 || value.WorkerScratchEntries == 0 || value.PostgresWorkerPeakBytes == 0 || value.PostgresWorkerPeakEntries == 0 || len(value.Target) == 0 {
		return false
	}
	_, ok := clusterSSHStorageDemandPaths(clusterSSHStoragePolicy{DefaultPath: value.Target[0].Path}, value.Target)
	return ok
}

// One checker belongs to one cohort dispatch. Each target's exact acquired
// demand is checked before its first prepare call. Later configured/startup
// scopes must retain that same demand and source, but do not charge installation
// again after our own writes. A restarted dispatch gets no retained exemption:
// its full original demand remains conservative, never recovery authority.
func newClusterSSHPrejoinWorkerStorageCheck(baseline clusterbootstrap.Expected, claims []clusterSSHNetworkTargetClaim,
	within func(context.Context, func(context.Context, clusterSSHNetworkTargetReaders) error) error) clusterSSHPrejoinWorkerStorageCheck {
	claims = slices.Clone(claims)
	var mu sync.Mutex
	retained := map[string]string{}
	failed := false
	return func(ctx context.Context, payload string, expected clusterbootstrap.PreparationExpected, capacity clusterSSHPreparedImageCapacity, checks clusterSSHPreparationChecks) (result error) {
		mu.Lock()
		defer mu.Unlock()
		defer func() { failed = failed || result != nil }()
		fail := clusterbootstrap.ErrPreparationConfig
		if failed || ctx.Err() != nil || len(payload) == 0 || len(payload) > 256<<10 || !json.Valid([]byte(payload)) || within == nil || checks.Storage == nil || checks.Authority == nil || expected.Validate() != nil || expected.Source != baseline || !validClusterSSHPrejoinWorkerCapacity(capacity) {
			return fail
		}
		index := slices.IndexFunc(claims, func(c clusterSSHNetworkTargetClaim) bool { return c.Lease.TargetID == expected.Target.TargetID })
		if index < 0 || expected.Target.Generation != claims[index].Lease.Generation || expected.Target.HolderID != claims[index].Lease.Holder || checks.Authority(ctx) != nil {
			return fail
		}
		storage, err := checks.Storage(ctx)
		if err != nil {
			return fail
		}
		paths, ok := clusterSSHStorageDemandPaths(storage.Requirements.Policy, capacity.Target)
		if !ok {
			return fail
		}
		// The worker supplies its already-fenced private payload. Claimed
		// filesystem readers intentionally retain only its public digest; do not
		// reconstruct approved target CPU from that payload-free projection.
		payloadDigest := sha256.Sum256([]byte(payload))
		payloadSHA256 := hex.EncodeToString(payloadDigest[:])
		raw, err := json.Marshal(struct {
			PayloadSHA256 string
			Expected      clusterbootstrap.PreparationExpected
			Capacity      clusterSSHPreparedImageCapacity
			Storage       clusterSSHStorageSnapshot
		}{payloadSHA256, expected, capacity, storage})
		if err != nil || storage.Requirements.observation != "" {
			return fail
		}
		if old, exists := retained[expected.Target.TargetID]; exists {
			if old != string(raw) || ctx.Err() != nil || checks.Authority(ctx) != nil {
				return fail
			}
			return nil
		}
		called := false
		err = within(ctx, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
			if called || readers.Authority == nil || readers.Refresh == nil || checks.Authority(ctx) != nil {
				return fail
			}
			called = true
			a, err := readers.Authority(ctx)
			if err != nil || a.PayloadSHA256 != payloadSHA256 || validateClusterSSHNetworkClaims(a, baseline, claims) != nil || !validClusterSSHStorageSnapshot(storage, a.Source) {
				return fail
			}
			selected, err := buildClusterSSHPreparationExpected(a.Cohort, a.Source, claims[index].Lease, baseline, a.K3sVersion, expected.PodCIDR, expected.ServiceCIDR, payload)
			if err != nil || !reflect.DeepEqual(selected, expected) {
				return fail
			}
			selection := make([]clusterSSHTargetFilesystemSelection, len(claims))
			for i, claim := range claims {
				selection[i] = clusterSSHTargetFilesystemSelection{TargetID: claim.Lease.TargetID, Paths: slices.Clone(paths), RequirePersistent: true}
			}
			proved := false
			if withClusterSSHTargetFilesystemEvidence(ctx, readers, selection, func(ctx context.Context, targets []clusterSSHTargetFilesystemEvidence) error {
				if proved || len(targets) != len(claims) || checks.Authority(ctx) != nil {
					return fail
				}
				for i, target := range targets {
					if target.TargetID != claims[i].Lease.TargetID {
						return fail
					}
				}
				// The other target may already contain our prepared prefix. Its
				// identity remains observed, but its original demand is not charged
				// a second time while preparing this selected target.
				budgets, err := calculateClusterSSHStorageCapacity(storage.Requirements, targets[index].Storage, capacity.Target)
				if err != nil || len(budgets) == 0 {
					return fail
				}
				for _, budget := range budgets {
					if !budget.Fits {
						return fail
					}
				}
				proved = true
				return checks.Authority(ctx)
			}) != nil || !proved || readers.Refresh(ctx) != nil || checks.Authority(ctx) != nil {
				return fail
			}
			return nil
		})
		if err != nil || !called || ctx.Err() != nil || checks.Authority(ctx) != nil {
			return fail
		}
		retained[expected.Target.TargetID] = string(raw)
		return nil
	}
}
