package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"time"
)

type clusterSSHPreparationChecks struct {
	Inputs    func(context.Context) error
	Authority func(context.Context) error
}

// One scope owns acquisition and synchronous consumption. Callers must honor
// ctx and join their descendants before returning; the scope never abandons a
// work goroutine or reports success while cleanup is still running.
func runClusterSSHPreparationScope(parent context.Context, interval time.Duration, check func(context.Context) error, work func(context.Context) error) (result error) {
	if parent.Err() != nil || check == nil || work == nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	lifetime := ctx
	if check(ctx) != nil || ctx.Err() != nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	guard := startClusterControllerLeaseGuardWithGrace(ctx, interval, time.Millisecond, func(ctx context.Context) (bool, error) {
		return check(ctx) == nil, nil
	})
	defer func() {
		guard.Close()
		if guard.Err() != nil || lifetime.Err() != nil {
			result = clusterbootstrap.ErrSessionAuthority
		}
	}()
	ctx = guard.Context()
	// Work is synchronous: resource Close and child joins inside it finish
	// before guard.Close stops/joins its heartbeat. No raw input is returned.
	err := work(ctx)
	if ctx.Err() != nil || guard.Err() != nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	if err != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	if check(ctx) != nil || ctx.Err() != nil {
		return clusterbootstrap.ErrSessionAuthority
	}
	return nil
}

// This checker serializes heartbeat and explicit boundaries with a bounded
// wait, freezes the complete initial authority, and keeps expiry/crypto checks
// on both sides of the exact-envelope SQL renewal. Only observation time may
// advance. JSON comparison owns its bytes, avoiding aliases into later reads.
func newClusterSSHPreparationLeaseCheck(authority clusterSSHPreparationAuthorityRead, renew func(context.Context) error) func(context.Context) error {
	gate := make(chan struct{}, 1)
	var retained []byte
	return func(parent context.Context) error {
		if authority == nil || renew == nil || parent.Err() != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return clusterbootstrap.ErrSessionAuthority
		}
		observe := func() ([]byte, error) {
			value, err := authority(ctx)
			if err != nil || value.Baseline.Validate() != nil || !validClusterSSHPreparationLease(value.Lease) ||
				validateClusterSSHInspectionCohort(value.Cohort, value.Source) != nil || ctx.Err() != nil {
				return nil, clusterbootstrap.ErrSessionAuthority
			}
			value.Cohort.ObservedAt = 0
			raw, err := json.Marshal(value)
			if err != nil || len(raw) > 128<<10 {
				return nil, clusterbootstrap.ErrSessionAuthority
			}
			return raw, nil
		}
		before, err := observe()
		if err != nil || (retained != nil && !bytes.Equal(retained, before)) || renew(ctx) != nil || ctx.Err() != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		after, err := observe()
		if err != nil || !bytes.Equal(before, after) || ctx.Err() != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		if retained == nil {
			retained = before
		}
		return nil
	}
}

func clusterSSHPreparationBoundary(scope context.Context, leaseCheck, inputCheck func(context.Context) error) func(context.Context) error {
	return func(caller context.Context) error {
		if scope.Err() != nil || caller.Err() != nil || leaseCheck == nil || inputCheck == nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		bound, cancel := context.WithCancel(caller)
		defer cancel()
		stop := context.AfterFunc(scope, cancel)
		defer stop()
		if leaseCheck(bound) != nil || inputCheck(bound) != nil || bound.Err() != nil || scope.Err() != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		return nil
	}
}
