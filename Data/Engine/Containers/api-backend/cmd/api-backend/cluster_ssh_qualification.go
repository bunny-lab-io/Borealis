package main

import (
	"context"
	"encoding/json"
	"log"
	"regexp"
	"sync/atomic"
	"time"
)

// Includes both bounded package selections in a version 2 report.
const maxClusterSSHQualificationBytes = 128 << 10

type clusterSSHQualificationCheck struct {
	Code  string `json:"code"`
	State string `json:"state"`
}
type clusterSSHQualificationReport struct {
	Installation *clusterSSHPairInstallation `json:"installation,omitempty"`
	// Historical diagnostics enter the existing fenced completion event only.
	// They are never serialized into the signed readiness report.
	vipFailure   clusterSSHVIPFailure
	vipElapsedMS int64
	Version      int                               `json:"version"`
	Attempt      int64                             `json:"attempt"`
	ObservedAt   int64                             `json:"observed_at"`
	Ready        bool                              `json:"ready"`
	Checks       []clusterSSHQualificationCheck    `json:"checks"`
	Storage      []clusterSSHTargetStorageCapacity `json:"storage"`
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
	codes := clusterSSHQualificationCodes
	if r.Version == 2 {
		codes = clusterSSHPairCheckCodes
	}
	if (r.Version != 1 && r.Version != 2) || r.Version == 1 && r.Installation != nil || r.Version == 2 && ((r.Installation != nil) != r.Ready || r.Ready && (!r.Installation.valid() || len(r.Storage) != 2)) || r.Attempt != attempt || attempt < 1 || r.ObservedAt < 1 || r.ObservedAt > time.Now().Unix()+5 || r.Ready && r.Version != 2 || len(r.Checks) != len(codes) || r.Storage == nil || len(r.Storage) > 2 {
		return false
	}
	for i, c := range r.Checks {
		if c.Code != codes[i] || !textInSet(c.State, "pending", "passed", "blocked") || (i == len(r.Checks)-1 && r.Version == 1 && c.State != "pending") || r.Version == 2 && r.Ready && c.State != "passed" {
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
		if target.Fits != fits || r.Ready && !fits {
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
	if len(raw) > maxClusterSSHQualificationBytes || json.Unmarshal([]byte(raw), &r) != nil || !r.valid(attempt) {
		return nil, errClusterUnavailable
	}
	canonical, err := json.Marshal(r)
	if err != nil || !sameClusterSSHStoredJSON([]byte(raw), canonical, 0) {
		return nil, errClusterUnavailable
	}
	return &r, nil
}

// The first complete source snapshot used by profile sizing also establishes
// the source gate. Every subsequent profile check still acquires fresh proof.
func qualifyClusterSSHHostProfile(ctx context.Context, readers clusterSSHNetworkTargetReaders, source clusterSSHPreparationSnapshotRead) (bool, error) {
	sourceRead := false
	err := withClusterSSHTargetProfileSizing(ctx, readers, func(ctx context.Context) (clusterSSHPreparationSnapshot, error) {
		value, err := source(ctx)
		if err == nil {
			sourceRead = true
		}
		return value, err
	}, func(context.Context, []clusterSSHTargetHostEvidence) error { return nil })
	return sourceRead, err
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
	sourceClient, err := newClusterSSHSourceBrokerClientWithTimeout(clusterSSHReadOnlySnapshotTimeout)
	if err != nil {
		report.Checks[stage].State = "blocked"
		return report
	}
	vipClient, err := newClusterSSHVIPBrokerClientFromEnv()
	if err != nil && len(work.Claims) != 2 {
		report.Checks[stage].State = "blocked"
		return report
	}
	consume := func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
		anchor := work.Claims[0]
		observe := func(check string) {
			store.recordSSHCheckStarted(ctx, work.Proof.Cohort, work.Payload, check, anchor.Lease)
		}
		observe("source_inputs_and_host_profile")
		fetch := sourceClient.snapshotRead(readers.Authority, anchor.Lease, work.Baseline, anchor.Sealed)
		if len(work.Claims) == 2 {
			return inspectClusterSSHPair(ctx, work, readers, fetch, &report, &stage, observe)
		}
		source := fetch
		profileSourceRead, err := qualifyClusterSSHHostProfile(ctx, readers, source)
		if profileSourceRead {
			report.Checks[0].State = "passed"
			stage = 1
		}
		if err != nil {
			return err
		}
		report.Checks[stage].State = "passed"
		stage = 2
		// Published application/K3s/external inventory and an owned, measured
		// PostgreSQL archive supply conditional target demand. Aggregate worker,
		// OS/runtime headroom and workload fit remain separate readiness gates.
		observe("replica_capacity")
		if err := withClusterSSHImageStorageCapacity(ctx, readers, source, func(ctx context.Context, capacity []clusterSSHTargetStorageCapacity) error {
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
		observe("network_boot_and_arp")
		began := time.Now()
		var failure atomic.Pointer[clusterSSHVIPFailure]
		networkCtx := context.WithValue(ctx, clusterSSHVIPFailureSinkKey{}, func(code clusterSSHVIPFailure) { failure.CompareAndSwap(nil, &code) })
		completed, err := vipClient.observeNetworks(networkCtx, readers, source, anchor.Lease, work.Baseline, anchor.Sealed, readers.observeNetworkBoot, readers.observeNetworkARP)
		if completed > 0 {
			report.Checks[stage].State = "passed"
			stage = 4
		}
		if err != nil {
			if code := failure.Load(); code != nil {
				report.vipFailure, report.vipElapsedMS = *code, time.Since(began).Milliseconds()
				log.Printf("Network qualification controller_check=%s accepted_probes=%d elapsed_ms=%d operation=%s target=%s attempt=%d generation=%d", *code, completed, report.vipElapsedMS, anchor.Lease.OperationID, anchor.Lease.TargetID, anchor.Lease.OperationAttempt, anchor.Lease.Generation)
			}
			return err
		}
		report.Checks[stage].State = "passed"
		return nil
	}
	inspect := func(ctx context.Context) error {
		return withClusterSSHNetworkTargets(ctx, store, aegis, work.Baseline, work.Claims, consume)
	}
	if len(work.Claims) == 2 {
		operation := clusterSSHPreparationWork{Claims: work.Claims, Proof: work.Proof, Payload: work.Payload, Baseline: work.Baseline}
		err = store.withSSHOperationOwnership(ctx, aegis, operation, 5*time.Second, clusterSSHQualificationBudget, func(ctx context.Context, _ func(context.Context) error) error { return inspect(ctx) })
	} else {
		err = inspect(ctx)
	}
	if err != nil {
		// A failed/closed scope cannot publish earlier readings as current proof.
		if report.Version != 2 {
			for i := range report.Checks {
				report.Checks[i].State = "pending"
			}
			report.Storage = []clusterSSHTargetStorageCapacity{}
		}
		report.Checks[stage].State = "blocked"
		report.Installation, report.Ready = nil, false
	}
	report.ObservedAt = time.Now().Unix()
	return report
}

// The original inspection times also bound a reclaimed attempt. A sibling
// target or a worker restart cannot restart the qualification clock.
func (w clusterSSHQualificationWork) deadline() time.Time {
	var oldest time.Time
	for _, target := range w.Proof.Cohort.Targets {
		observed := time.Unix(target.InspectedAt, 0)
		if oldest.IsZero() || observed.Before(oldest) {
			oldest = observed
		}
	}
	return oldest.Add(clusterSSHQualificationBudget)
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
	// Reserve the fresh cohort before its initial claim window closes. While
	// the controller stages locally, no target reader or renewal is needed.
	// The existing expired read-only claim path resumes after the ready event.
	if ready, err := r.store.inspectionReleaseReady(ctx, work); err != nil || !ready {
		return err
	}
	for _, claim := range work.Claims {
		if r.aegis.verifyClusterSSHGeneration(ctx, claim.Sealed.generation) != nil {
			return errClusterSSHCredentials
		}
	}
	qualificationCtx, cancel := context.WithDeadline(ctx, work.deadline())
	releaseCtx := clusterContextWithGitHubToken(qualificationCtx, r.store, r.aegis)
	report := runClusterSSHQualification(releaseCtx, r.store, r.aegis, work)
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
