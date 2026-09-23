package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"regexp"
	"time"
)

type clusterSSHQualificationCheck struct {
	Code  string `json:"code"`
	State string `json:"state"`
}
type clusterSSHQualificationReport struct {
	Version    int                               `json:"version"`
	Attempt    int64                             `json:"attempt"`
	ObservedAt int64                             `json:"observed_at"`
	Ready      bool                              `json:"ready"`
	Checks     []clusterSSHQualificationCheck    `json:"checks"`
	Storage    []clusterSSHTargetStorageCapacity `json:"storage"`
}

var clusterSSHQualificationCodes = []string{"source_inputs", "host_profile", "replica_capacity", "network_boot", "network_arp", "remaining_prerequisites"}
var clusterSSHCapacityFilesystemRE = regexp.MustCompile(`^[0-9a-f]{16}$`)

func newClusterSSHQualificationReport(attempt int64) clusterSSHQualificationReport {
	r := clusterSSHQualificationReport{Version: 1, Attempt: attempt, ObservedAt: time.Now().Unix(), Storage: []clusterSSHTargetStorageCapacity{}}
	for _, code := range clusterSSHQualificationCodes {
		r.Checks = append(r.Checks, clusterSSHQualificationCheck{code, "pending"})
	}
	return r
}

func (r clusterSSHQualificationReport) valid(attempt int64) bool {
	if r.Version != 1 || r.Attempt != attempt || attempt < 1 || r.ObservedAt < 1 || r.ObservedAt > time.Now().Unix()+5 || r.Ready || len(r.Checks) != len(clusterSSHQualificationCodes) || r.Storage == nil || len(r.Storage) > 2 {
		return false
	}
	for i, c := range r.Checks {
		if c.Code != clusterSSHQualificationCodes[i] || !textInSet(c.State, "pending", "passed", "blocked") || (i == len(r.Checks)-1 && c.State != "pending") {
			return false
		}
	}
	seen := map[string]bool{}
	for _, target := range r.Storage {
		p, ok := clusterSSHStoragePath(target.StoragePath)
		if !clusterUUIDRE.MatchString(target.TargetID) || seen[target.TargetID] || !ok || p != target.StoragePath || len(target.Budgets) < 1 || len(target.Budgets) > 8 {
			return false
		}
		seen[target.TargetID] = true
		filesystems := map[string]bool{}
		fits := true
		for _, b := range target.Budgets {
			if !clusterSSHCapacityFilesystemRE.MatchString(b.Filesystem) || filesystems[b.Filesystem] {
				return false
			}
			filesystems[b.Filesystem] = true
			for _, v := range []uint64{b.AvailableBytes, b.OtherBytes, b.ReplicaBytes, b.RebuildBytes, b.MinimumFreeBytes, b.LogicalLimitBytes} {
				if v > clusterSSHCapacityLimit {
					return false
				}
			}
			required, ok := clusterSSHCapacitySum(b.OtherBytes, b.ReplicaBytes)
			if !ok {
				return false
			}
			required, ok = clusterSSHCapacitySum(required, b.RebuildBytes)
			if !ok {
				return false
			}
			required, ok = clusterSSHCapacitySum(required, b.MinimumFreeBytes)
			if !ok {
				return false
			}
			logical, ok := clusterSSHCapacitySum(b.ReplicaBytes, b.RebuildBytes)
			if !ok {
				return false
			}
			if b.Fits != (b.AvailableBytes > required && (logical == 0 || logical <= b.LogicalLimitBytes)) {
				return false
			}
			fits = fits && b.Fits
		}
		if target.Fits != fits {
			return false
		}
	}
	return true
}

func parseClusterSSHQualificationReport(raw string, attempt int64) (*clusterSSHQualificationReport, error) {
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var r clusterSSHQualificationReport
	if len(raw) > 16<<10 || json.Unmarshal([]byte(raw), &r) != nil || !r.valid(attempt) {
		return nil, errClusterUnavailable
	}
	canonical, err := json.Marshal(r)
	if err != nil || !sameClusterSSHStoredJSON([]byte(raw), canonical, 0) {
		return nil, errClusterUnavailable
	}
	return &r, nil
}

// This first runtime qualification pass records supported observations and
// explicit remaining gates. It cannot mark ready, confirm, stage or admit.
// Complete runtime/image disk demands, aggregate workload fit, mount/reboot
// persistence and immutable recovery/preparation proofs remain required.
func runClusterSSHQualification(ctx context.Context, store *postgresOperatorStore, aegis *goAegisService, work clusterSSHQualificationWork) clusterSSHQualificationReport {
	report := newClusterSSHQualificationReport(work.Proof.Cohort.Attempt)
	stage := 0
	if work.Baseline.Validate() != nil || len(work.Claims) == 0 {
		report.Checks[stage].State = "blocked"
		return report
	}
	sourceClient, err := newClusterSSHSourceBrokerClientFromEnv()
	if err != nil {
		report.Checks[stage].State = "blocked"
		return report
	}
	vipClient, err := newClusterSSHVIPBrokerClientFromEnv()
	if err != nil {
		report.Checks[stage].State = "blocked"
		return report
	}
	err = withClusterSSHNetworkTargets(ctx, store, aegis, work.Baseline, work.Claims, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
		anchor := work.Claims[0]
		source := sourceClient.snapshotRead(readers.Authority, anchor.Lease, work.Baseline, anchor.Sealed)
		if _, err := source(ctx); err != nil {
			return err
		}
		report.Checks[stage].State = "passed"
		stage = 1
		settings := func(ctx context.Context) (clusterbootstrap.PreparationExpected, map[string]string, error) {
			v, err := source(ctx)
			return v.Expected, v.Settings, err
		}
		if err := withClusterSSHTargetSizing(ctx, readers, settings, func(context.Context, []clusterSSHTargetHostEvidence) error { return nil }); err != nil {
			return err
		}
		report.Checks[stage].State = "passed"
		stage = 2
		// Bound only bootstrap copies here. Runtime images/build caches need a
		// complete immutable demand plan; remaining_prerequisites stays pending.
		demands := []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 2*clusterbootstrap.MaxExpandedBytes + clusterbootstrap.MaxBundleBytes}}
		if err := withClusterSSHTargetStorageCapacity(ctx, readers, source, demands, func(ctx context.Context, capacity []clusterSSHTargetStorageCapacity) error {
			report.Storage = capacity
			report.Checks[stage].State = "passed"
			for _, target := range capacity {
				if !target.Fits {
					report.Checks[stage].State = "blocked"
				}
			}
			return nil
		}); err != nil {
			return err
		}
		stage = 3
		if err := vipClient.observeNetworkBoot(ctx, readers.Authority, source, readers.Peer, readers.Boot, readers.Refresh, anchor.Lease, work.Baseline, anchor.Sealed); err != nil {
			return err
		}
		report.Checks[stage].State = "passed"
		stage = 4
		if err := vipClient.observeNetworkARP(ctx, readers.Authority, source, readers.Peer, readers.ARP, readers.Refresh, anchor.Lease, work.Baseline, anchor.Sealed); err != nil {
			return err
		}
		report.Checks[stage].State = "passed"
		return nil
	})
	if err != nil {
		// A failed/closed scope cannot publish earlier readings as current proof.
		for i := range report.Checks {
			report.Checks[i].State = "pending"
		}
		report.Checks[stage].State = "blocked"
		report.Storage = []clusterSSHTargetStorageCapacity{}
	}
	report.ObservedAt = time.Now().Unix()
	return report
}

func (r *clusterSSHInspectionRuntime) qualifyOnce(ctx context.Context) error {
	lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
	id, err := r.store.pendingClusterSSHQualification(lookup)
	cancel()
	if err != nil || id == "" {
		return err
	}
	claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	work, err := r.store.claimClusterSSHQualification(claimCtx, id, newClusterUUID())
	cancel()
	if err != nil {
		return err
	}
	for _, claim := range work.Claims {
		if r.aegis.verifyClusterSSHGeneration(ctx, claim.Sealed.generation) != nil {
			return errClusterSSHCredentials
		}
	}
	qualificationCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	report := runClusterSSHQualification(qualificationCtx, r.store, r.aegis, work)
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, claim := range work.Claims {
		if r.aegis.verifyClusterSSHGeneration(ctx, claim.Sealed.generation) != nil {
			return errClusterSSHCredentials
		}
	}
	completeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.store.completeClusterSSHQualification(completeCtx, work, report)
}
