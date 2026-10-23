package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"
)

type clusterSSHVIPLeaseRead func(context.Context) (clusterbootstrap.VIPLease, error)

func (c *kubernetesAPIClient) readClusterSSHVIPLease(parent context.Context) (clusterbootstrap.VIPLease, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	var raw json.RawMessage
	if c.clusterSSHPrivateJSON(ctx, "GET", clusterbootstrap.VIPLeasePath, nil, &raw) != nil {
		return clusterbootstrap.VIPLease{}, clusterbootstrap.ErrPreparationConfig
	}
	return clusterbootstrap.ParseVIPLease(raw)
}

// Public identity only. This value contains no portable expiry/fencing token.
// Only checks inside the joined consumption scope confer current observation.
type clusterSSHVIPOwner struct {
	Address               string
	Owner                 clusterSSHManagementPeer
	LeaseUID, AcquireTime string
	Transitions           int64
}

// This controller-owned scope requires freshly observed source peer inputs.
// The caller retains every required worker/credential lease; this read-only
// scope neither claims work nor renews credentials. API workers cannot directly
// construct its Kubernetes readers. No result is a durable qualification grant.
func withClusterSSHVIPOwner(parent context.Context, authority clusterSSHPreparationAuthorityRead,
	networks []clusterbootstrap.SourceNetwork, readVIP clusterSSHSourceVIPRead, readLease clusterSSHVIPLeaseRead,
	consume func(context.Context, clusterSSHVIPOwner, clusterSSHPreparationChecks) error) error {
	if authority == nil || readVIP == nil || readLease == nil || consume == nil || parent.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	initial, err := authority(ctx)
	if err != nil || initial.Baseline.Validate() != nil || !validClusterSSHObservationLease(initial.Lease) || validateClusterSSHSourceNetworks(initial, networks) != nil ||
		initial.Source.ControlPlaneVIP != initial.Source.EdgeVIP {
		return clusterbootstrap.ErrPreparationConfig
	}
	networks = slices.Clone(networks)
	// Own the complete ordered authority, including nested target key bytes.
	raw, err := json.Marshal(initial)
	var expected clusterSSHPreparationAuthority
	if err != nil || json.Unmarshal(raw, &expected) != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	expected.Cohort.ObservedAt = 0
	frozen, _ := json.Marshal(expected)
	checkAuthority := func(ctx context.Context) error {
		current, err := authority(ctx)
		if err != nil || validateClusterSSHInspectionCohort(current.Cohort, current.Source) != nil || ctx.Err() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		current.Cohort.ObservedAt = 0
		wire, err := json.Marshal(current)
		if err != nil || !bytes.Equal(wire, frozen) {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
	gate := make(chan struct{}, 1)
	var progress clusterbootstrap.VIPLeaseProgress
	// Expiry cancels consumers even while a lease GET or source Job is waiting.
	// A timer race may fail early, but can never resurrect an expired scope.
	expiry := time.AfterFunc(5*time.Second, cancel)
	defer expiry.Stop()
	poll := func(ctx context.Context) (clusterbootstrap.VIPLease, bool, error) {
		fail := func() (clusterbootstrap.VIPLease, bool, error) {
			cancel()
			return clusterbootstrap.VIPLease{}, false, clusterbootstrap.ErrPreparationConfig
		}
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return fail()
		}
		if checkAuthority(ctx) != nil {
			return fail()
		}
		started := time.Now()
		readCtx, stop := context.WithTimeout(ctx, time.Second)
		lease, err := readLease(readCtx)
		readErr := readCtx.Err()
		stop()
		if err != nil || readErr != nil || checkAuthority(ctx) != nil || ctx.Err() != nil {
			return fail()
		}
		found := false
		for _, member := range expected.Source.Members {
			found = found || member.Name == lease.Holder
		}
		if !found {
			return fail()
		}
		ready, deadline, err := progress.Observe(lease, started, time.Now())
		if err != nil || ctx.Err() != nil {
			return fail()
		}
		expiry.Reset(time.Until(deadline))
		return lease, ready, nil
	}
	var epoch clusterbootstrap.VIPLease
	for {
		lease, ready, err := poll(ctx)
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		if ready {
			epoch = lease
			break
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return clusterbootstrap.ErrPreparationConfig
		case <-timer.C:
		}
	}
	check := func(ctx context.Context) error {
		_, ready, err := poll(ctx)
		if err != nil || !ready {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
	return runClusterSSHPreparationScope(ctx, 500*time.Millisecond, check, func(ctx context.Context) error {
		var retained []clusterbootstrap.SourceVIPNetwork
		var owner clusterSSHVIPOwner
		inputGate := make(chan struct{}, 1)
		read := func(ctx context.Context) error {
			fail := func() error { cancel(); return clusterbootstrap.ErrPreparationConfig }
			select {
			case inputGate <- struct{}{}:
				defer func() { <-inputGate }()
			case <-ctx.Done():
				return clusterbootstrap.ErrPreparationConfig
			}
			var observed []clusterbootstrap.SourceVIPNetwork
			owners := 0
			for i, member := range expected.Source.Members {
				if check(ctx) != nil {
					return fail()
				}
				started := time.Now()
				observation, err := readVIP(ctx, member)
				value := observation.value
				if err != nil || observation.started.Before(started) || observation.started.After(time.Now()) || value.Validate() != nil || value.Network != networks[i] || value.VIP.Address != expected.Source.ControlPlaneVIP || check(ctx) != nil {
					return fail()
				}
				if value.VIP.Present {
					owners++
					if member.Name != epoch.Holder {
						return fail()
					}
					owner = clusterSSHVIPOwner{Address: value.VIP.Address, Owner: clusterSSHManagementPeer{ID: member.NodeID, NodeUID: member.NodeUID, Hostname: member.Name,
						MachineID: member.MachineID, BootID: member.BootID, SSHFingerprint: member.SSHFingerprint, Link: value.Network.ManagementLink}, LeaseUID: epoch.UID, AcquireTime: epoch.AcquireTime, Transitions: epoch.Transitions}
				}
				observed = append(observed, value)
			}
			if owners != 1 || (retained != nil && !slices.Equal(retained, observed)) || check(ctx) != nil || ctx.Err() != nil {
				return fail()
			}
			retained = observed
			return nil
		}
		if read(ctx) != nil || read(ctx) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		checks := clusterSSHPreparationChecks{Inputs: clusterSSHPreparationBoundary(ctx, check, read), Authority: clusterSSHPreparationBoundary(ctx, check, func(context.Context) error { return nil })}
		if consume(ctx, owner, checks) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		return checks.Inputs(ctx)
	})
}
