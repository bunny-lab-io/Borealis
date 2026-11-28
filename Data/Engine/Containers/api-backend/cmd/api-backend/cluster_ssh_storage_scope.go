package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Controller-only read capability. The caller supplies fresh DB/Aegis authority
// and retains all original worker leases; this observer cannot claim or renew
// work. No storage result crosses the existing source broker in this component.
func (r *kubernetesClusterStepRunner) withSSHSourceStorage(ctx context.Context, authority clusterSSHPreparationAuthorityRead,
	consume func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error) error {
	if r == nil || r.kube == nil || r.namespace != "borealis" {
		return clusterbootstrap.ErrPreparationConfig
	}
	return withClusterSSHSourceStorage(ctx, authority, r.kube.getClusterSSHStorageJSON, consume)
}

// Two initial complete observations and a final observation surround synchronous
// consumption. Explicit checks join the same lifetime, including callers using
// an independent context. Failure is latched even when the consumer ignores it.
func withClusterSSHSourceStorage(parent context.Context, authority clusterSSHPreparationAuthorityRead,
	getJSON func(context.Context, string, any) error,
	consume func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error) error {
	return withClusterSSHSourceStorageFor(parent, clusterSSHSourceSnapshotTimeout, authority, getJSON, nil, consume)
}

// Only the final synchronous inventory uses this recorder. By then all borrowed
// input readers have closed and joined. Keep request bodies, selectors, error
// strings and credentials out of both controller logs and the event timeline.
type clusterSSHFinalInventoryTiming struct {
	started, finished                                    time.Time
	reads                                                int
	last, slowest                                        string
	lastElapsed, slowestElapsed, authorityTime, readTime time.Duration
}

func (t *clusterSSHFinalInventoryTiming) measure(path, step string, read func() error) error {
	if t == nil {
		return read()
	}
	start := time.Now()
	err := read()
	elapsed := time.Since(start)
	path, _, _ = strings.Cut(path, "?") // inventory readers construct validated Kubernetes paths
	t.last, t.lastElapsed = step+" "+path, elapsed
	if elapsed > t.slowestElapsed {
		t.slowest, t.slowestElapsed = t.last, elapsed
	}
	if step == "GET" {
		t.reads++
		t.readTime += elapsed
	} else {
		t.authorityTime += elapsed
	}
	return err
}

func (t *clusterSSHFinalInventoryTiming) summary(snapshotStarted time.Time) string {
	if t == nil || t.finished.IsZero() {
		return ""
	}
	return fmt.Sprintf(" (final inventory began at %s, took %s, %d GETs totaling %s, surrounding authority checks %s, last %q %s, slowest %q %s)",
		t.started.Sub(snapshotStarted).Round(time.Millisecond), t.finished.Sub(t.started).Round(time.Millisecond),
		t.reads, t.readTime.Round(time.Millisecond), t.authorityTime.Round(time.Millisecond),
		t.last, t.lastElapsed.Round(time.Millisecond), t.slowest, t.slowestElapsed.Round(time.Millisecond))
}

func withClusterSSHSourceStorageFor(parent context.Context, budget time.Duration, authority clusterSSHPreparationAuthorityRead,
	getJSON func(context.Context, string, any) error, finalTiming *clusterSSHFinalInventoryTiming,
	consume func(context.Context, clusterSSHStorageRequirements, clusterSSHPreparationChecks) error) error {
	if authority == nil || getJSON == nil || consume == nil || parent.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	var frozen []byte
	var expected clusterSSHPreparationAuthority
	authorityGate := make(chan struct{}, 1)
	checkAuthority := func(parent context.Context) error {
		ctx, stop := context.WithTimeout(parent, 3*time.Second)
		defer stop()
		select {
		case authorityGate <- struct{}{}:
			defer func() { <-authorityGate }()
		case <-ctx.Done():
			return clusterbootstrap.ErrSessionAuthority
		}
		if ctx.Err() != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		value, err := authority(ctx)
		if err != nil || ctx.Err() != nil || value.Baseline.Validate() != nil || !validClusterSSHObservationLease(value.Lease) ||
			validateClusterSSHInspectionCohort(value.Cohort, value.Source) != nil ||
			value.Lease.OperationID != value.Cohort.OperationID || value.Lease.ControllerHolder != value.Cohort.ControllerHolder || value.Lease.OperationAttempt != value.Cohort.Attempt ||
			!slices.ContainsFunc(value.Cohort.Targets, func(target clusterSSHInspectedTarget) bool {
				return target.Binding.TargetID == value.Lease.TargetID && value.Lease.Generation > target.Generation
			}) {
			return clusterbootstrap.ErrSessionAuthority
		}
		value.Cohort.ObservedAt = 0
		raw, err := json.Marshal(value)
		if err != nil || len(raw) > 128<<10 || (frozen != nil && !bytes.Equal(raw, frozen)) {
			return clusterbootstrap.ErrSessionAuthority
		}
		if frozen == nil {
			if json.Unmarshal(raw, &expected) != nil {
				return clusterbootstrap.ErrSessionAuthority
			}
			frozen = raw
		}
		return nil
	}
	return runClusterSSHPreparationScope(ctx, time.Second, checkAuthority, func(ctx context.Context) (result error) {
		readerCtx, cancelReaders := context.WithCancel(ctx)
		lifetime := &clusterSSHNetworkReaderLifetime{ctx: readerCtx, cancel: cancelReaders}
		defer func() {
			if lifetime.close() != nil {
				result = clusterbootstrap.ErrPreparationConfig
			}
		}()
		var timing *clusterSSHFinalInventoryTiming
		get := func(ctx context.Context, path string, out any) error {
			check := func() error { return checkAuthority(ctx) }
			if timing.measure(path, "before GET", check) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			err := timing.measure(path, "GET", func() error { return getJSON(ctx, path, out) })
			if (err != nil && !(err == errClusterSSHCNPGConfigAbsent && clusterSSHCNPGOptionalConfigPath(path))) || ctx.Err() != nil || timing.measure(path, "after GET", check) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return err
		}
		var retained *clusterSSHStorageObservation
		inputGate := make(chan struct{}, 1)
		checkInputs := func(ctx context.Context) error {
			select {
			case inputGate <- struct{}{}:
				defer func() { <-inputGate }()
			case <-ctx.Done():
				return clusterbootstrap.ErrPreparationConfig
			}
			checkSource := func() error {
				observed, err := observeClusterSSHSourceCohort(ctx, get, expected.Source)
				if err != nil || !reflect.DeepEqual(observed, expected.Source) {
					return clusterbootstrap.ErrPreparationConfig
				}
				return nil
			}
			if checkSource() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			value, err := observeClusterSSHStorage(ctx, expected.Source, get)
			if err != nil || checkSource() != nil || ctx.Err() != nil || (retained != nil && !value.sameInputs(*retained)) {
				return clusterbootstrap.ErrPreparationConfig
			}
			retained = &value
			return nil
		}
		checks := clusterSSHPreparationChecks{
			Inputs:    func(ctx context.Context) error { return lifetime.run(ctx, checkInputs) },
			Authority: func(ctx context.Context) error { return lifetime.run(ctx, checkAuthority) },
		}
		if checks.Inputs(readerCtx) != nil || checks.Inputs(readerCtx) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		value := retained.Requirements
		value.Applications = slices.Clone(value.Applications)
		value.SiteWorkers = slices.Clone(value.SiteWorkers)
		value.Volumes = slices.Clone(value.Volumes)
		value.ReplicaImages = slices.Clone(value.ReplicaImages)
		value.LonghornPlugin = slices.Clone(value.LonghornPlugin)
		value.Policy.Classes = slices.Clone(value.Policy.Classes)
		if consume(readerCtx, value, checks) != nil || readerCtx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		// Close exposed checks before the final read: escaped callbacks must be
		// cancelled and joined, not allowed to delay closure behind the input gate.
		if lifetime.close() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		timing = finalTiming
		if timing != nil {
			timing.started = time.Now()
			defer func() { timing.finished = time.Now() }()
		}
		return checkInputs(ctx)
	})
}
