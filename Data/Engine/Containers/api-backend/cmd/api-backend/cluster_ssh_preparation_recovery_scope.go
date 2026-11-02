package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"time"
)

type clusterSSHPreparationRecoveryProofConsumer func(context.Context, clusterSSHPreparationRecoveryAuthority, func(context.Context) error) error

// Only this synchronous proof scope consumes an issued claim. It provides no
// native effect, settlement, generic PreparationAuthority or join capability.
// The existing preparation scope supplies its 10-minute outer ceiling and a
// joined heartbeat; neither ceiling nor heartbeat extends credential deadlines.
func (c *clusterController) withClusterSSHPreparationRecoveryProof(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryClaim, consume clusterSSHPreparationRecoveryProofConsumer) (result error) {
	return c.withClusterSSHPreparationRecoveryProofEvery(parent, aegis, issued, time.Second, consume)
}

func (c *clusterController) withClusterSSHPreparationRecoveryProofEvery(parent context.Context, aegis *goAegisService,
	issued clusterSSHPreparationRecoveryClaim, interval time.Duration, consume clusterSSHPreparationRecoveryProofConsumer) (result error) {
	// Consume before any fallible check. Even failed/canceled entry cannot replay
	// the same issued handle. Copies share the latch but never mutable expectations.
	if issued.custody == nil || !issued.custody.started.CompareAndSwap(false, true) {
		return clusterbootstrap.ErrSessionAuthority
	}
	defer func() {
		if result != nil {
			issued.custody.failed.Store(true)
		}
	}()
	claim, expires, err := freezeClusterSSHPreparationRecoveryClaim(issued)
	if err != nil || parent.Err() != nil || c == nil || aegis == nil || consume == nil || interval <= 0 || interval > 15*time.Second {
		return clusterbootstrap.ErrSessionAuthority
	}
	check, snapshot := newClusterSSHPreparationRecoveryLeaseCheck(claim.custody, expires,
		func(ctx context.Context) error {
			for _, target := range claim.Claims {
				if aegis.verifyClusterSSHGeneration(ctx, target.Sealed.generation) != nil {
					return clusterbootstrap.ErrSessionAuthority
				}
			}
			return nil
		}, func(ctx context.Context, expected map[string]int64, renew bool) (clusterSSHPreparationRecoveryAuthority, map[string]int64, error) {
			return c.observeClusterSSHPreparationRecovery(ctx, claim, expected, renew)
		})
	return runClusterSSHPreparationScope(parent, interval, check, func(ctx context.Context) (result error) {
		readerCtx, cancel := context.WithCancel(ctx)
		lifetime := &clusterSSHNetworkReaderLifetime{ctx: readerCtx, cancel: cancel}
		defer func() {
			if lifetime.close() != nil {
				result = clusterbootstrap.ErrSessionAuthority
			}
		}()
		boundCheck := func(caller context.Context) error { return lifetime.run(caller, check) }
		value, err := snapshot()
		if err != nil || consume(readerCtx, value, boundCheck) != nil || readerCtx.Err() != nil {
			return clusterbootstrap.ErrSessionAuthority
		}
		// Close and join escaped explicit checks before the outer scope performs
		// its final authority check and joins any already-admitted heartbeat.
		return lifetime.close()
	})
}

type clusterSSHPreparationRecoveryObserve func(context.Context, map[string]int64, bool) (clusterSSHPreparationRecoveryAuthority, map[string]int64, error)

// One owner serializes reads, renewal and expiry advancement. Any ambiguous
// failure poisons its issued handle, including an ignored explicit-check error.
// Expected state is never repaired by reading a different current SQL expiry.
func newClusterSSHPreparationRecoveryLeaseCheck(custody *clusterSSHPreparationRecoveryCustody, assigned map[string]int64,
	verify func(context.Context) error, observe clusterSSHPreparationRecoveryObserve) (func(context.Context) error, func() (clusterSSHPreparationRecoveryAuthority, error)) {
	gate := make(chan struct{}, 1)
	expected := maps.Clone(assigned)
	var frozen, latest []byte
	fail := func() error {
		if custody != nil {
			custody.failed.Store(true)
		}
		return clusterbootstrap.ErrSessionAuthority
	}
	check := func(parent context.Context) (result error) {
		if custody == nil || custody.failed.Load() || verify == nil || observe == nil || parent.Err() != nil {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return fail()
		}
		defer func() {
			if result != nil {
				fail()
			}
		}()
		if custody.failed.Load() || verify(ctx) != nil {
			return fail()
		}
		before, observedExpiry, err := observe(ctx, maps.Clone(expected), false)
		if err != nil || !maps.Equal(observedExpiry, expected) || !maps.Equal(before.ExpiresAt, expected) || ctx.Err() != nil {
			return fail()
		}
		before.ExpiresAt = nil
		raw, err := json.Marshal(before)
		if err != nil || len(raw) > 256<<10 || frozen != nil && !bytes.Equal(raw, frozen) {
			return fail()
		}
		after, next, err := observe(ctx, maps.Clone(expected), true)
		if err != nil || !maps.Equal(after.ExpiresAt, next) || len(next) != len(expected) || ctx.Err() != nil {
			return fail()
		}
		for id := range expected {
			if next[id] <= 0 {
				return fail()
			}
		}
		stored, err := json.Marshal(after)
		if err != nil || len(stored) > 256<<10 {
			return fail()
		}
		after.ExpiresAt = nil
		afterRaw, err := json.Marshal(after)
		if err != nil || !bytes.Equal(raw, afterRaw) || verify(ctx) != nil || custody.failed.Load() || ctx.Err() != nil {
			return fail()
		}
		expected, frozen, latest = maps.Clone(next), raw, stored
		return nil
	}
	snapshot := func() (clusterSSHPreparationRecoveryAuthority, error) {
		// Snapshot is called only by synchronous scope entry, after its first
		// check. The same gate prevents a heartbeat racing this detached copy.
		gate <- struct{}{}
		defer func() { <-gate }()
		var value clusterSSHPreparationRecoveryAuthority
		if custody == nil || custody.failed.Load() || len(latest) == 0 || json.Unmarshal(latest, &value) != nil {
			return value, fail()
		}
		return value, nil
	}
	return check, snapshot
}
