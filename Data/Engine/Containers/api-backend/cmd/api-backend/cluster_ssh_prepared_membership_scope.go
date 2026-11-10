package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"reflect"
	"time"
)

// Read checks only. No worker lease, join dispatch, admission token or durable
// quorum flag is exposed. Inputs acquires fresh native evidence; Authority only
// rechecks the original settled controller's SQL custody.
type clusterSSHPreparedMembershipChecks struct {
	Inputs    func(context.Context) error
	Authority func(context.Context) error
}

func (c *clusterController) withClusterSSHPreparedMembership(parent context.Context, operation clusterControllerOperation,
	consume func(context.Context, clusterSSHPreparedCohortMembershipObservation, clusterSSHPreparedMembershipChecks) error) error {
	if c == nil || parent.Err() != nil {
		return errClusterConflict
	}
	runner, ok := c.runner.(*kubernetesClusterStepRunner)
	if !ok || runner == nil || runner.controllerHolder != c.holder {
		return errClusterConflict
	}
	holder := c.holder
	read := func(ctx context.Context) (clusterSSHPreparedAuthority, int64, error) {
		if c.holder != holder || c.runner != runner || runner.controllerHolder != holder {
			return clusterSSHPreparedAuthority{}, 0, errClusterConflict
		}
		return c.loadClusterSSHPreparedAuthority(ctx, operation)
	}
	return withClusterSSHPreparedMembershipScope(parent, operation, holder, runner, read, consume)
}

// One complete collection precedes synchronous consumption; another follows
// joined consumer/check cleanup. Both share the collector's existing 60-second
// ceiling. Successful reads describe their observation instants, not continuous
// quorum, independent local-etcd routing, or permission to mutate membership.
func withClusterSSHPreparedMembershipScope(parent context.Context, operation clusterControllerOperation, holder string,
	runner *kubernetesClusterStepRunner, read func(context.Context) (clusterSSHPreparedAuthority, int64, error),
	consume func(context.Context, clusterSSHPreparedCohortMembershipObservation, clusterSSHPreparedMembershipChecks) error) error {
	if parent.Err() != nil || holder == "" || runner == nil || runner.kube == nil || runner.kube.httpClient == nil || runner.controllerHolder != holder || read == nil || consume == nil {
		return errClusterConflict
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	kube, client, namespace, image := runner.kube, runner.kube.httpClient, runner.namespace, runner.actionImage
	endpoint, token := kube.baseURL, kube.token
	// These structures contain private immutable source/credential receipt
	// bindings. JSON clone their exported nested components, never marshal the
	// enclosing unexported struct into an empty or partial authority fingerprint.
	copyAuthority := func(value clusterSSHPreparedAuthority) (clusterSSHPreparedAuthority, error) {
		owned := clusterSSHPreparedAuthority{payload: value.payload, baseline: value.baseline, queue: value.queue, version: value.version}
		for _, field := range []struct{ from, to any }{{value.proof, &owned.proof}, {value.receipt, &owned.receipt}, {value.authorization, &owned.authorization}} {
			raw, err := json.Marshal(field.from)
			if err != nil || json.Unmarshal(raw, field.to) != nil {
				return clusterSSHPreparedAuthority{}, errClusterConflict
			}
		}
		return owned, nil
	}
	var expected *clusterSSHPreparedAuthority
	var observed int64
	authorityGate := make(chan struct{}, 1)
	current := func(parent context.Context) (clusterSSHPreparedAuthority, int64, error) {
		fail := func() (clusterSSHPreparedAuthority, int64, error) {
			return clusterSSHPreparedAuthority{}, 0, errClusterConflict
		}
		checkCtx, stop := context.WithTimeout(parent, 3*time.Second)
		defer stop()
		select {
		case authorityGate <- struct{}{}:
			defer func() { <-authorityGate }()
		case <-checkCtx.Done():
			return fail()
		}
		value, now, err := read(checkCtx)
		if err != nil || checkCtx.Err() != nil || ctx.Err() != nil || runner.controllerHolder != holder || runner.kube != kube || kube.httpClient != client || kube.baseURL != endpoint || kube.token != token || runner.namespace != namespace || runner.actionImage != image || now < observed ||
			expected != nil && !reflect.DeepEqual(*expected, value) {
			return fail()
		}
		owned, err := copyAuthority(value)
		if err != nil || checkCtx.Err() != nil || ctx.Err() != nil {
			return fail()
		}
		if expected == nil {
			frozen, err := copyAuthority(owned)
			if err != nil {
				return fail()
			}
			expected = &frozen
		}
		observed = now
		return owned, now, nil
	}
	checkAuthority := func(ctx context.Context) error { _, _, err := current(ctx); return err }
	err := runClusterSSHPreparationScopeFor(ctx, time.Second, 60*time.Second, checkAuthority, func(scope context.Context) (result error) {
		readerCtx, stopReaders := context.WithCancel(scope)
		lifetime := &clusterSSHNetworkReaderLifetime{ctx: readerCtx, cancel: stopReaders}
		defer func() {
			if lifetime.close() != nil {
				result = errClusterConflict
			}
		}()
		inputGate := make(chan struct{}, 1)
		seen := [3]map[string]bool{{}, {}, {}}
		var retained *clusterSSHPreparedCohortMembershipObservation
		checkInputs := func(ctx context.Context) error {
			select {
			case inputGate <- struct{}{}:
				defer func() { <-inputGate }()
			case <-ctx.Done():
				return errClusterConflict
			}
			value, err := readClusterSSHPreparedCohortMembershipWithJobs(ctx, operation, holder, runner, current, seen, clusterbootstrap.MembershipLocalEtcdProofVersion)
			if err != nil || ctx.Err() != nil || len(value.Views) != 3 {
				return errClusterConflict
			}
			if retained != nil {
				before, after := retained.Prepared, value.Prepared
				before.ObservedAt, after.ObservedAt = 0, 0
				if value.Prepared.ObservedAt < retained.Prepared.ObservedAt || !reflect.DeepEqual(before, after) {
					return errClusterConflict
				}
			}
			for i, view := range value.Views {
				if retained != nil && !reflect.DeepEqual(retained.Views[i].Observation, view.Observation) {
					return errClusterConflict
				}
			}
			if retained == nil {
				owned, err := copyClusterSSHPreparedMembershipObservation(value)
				if err != nil {
					return errClusterConflict
				}
				retained = &owned
			}
			return nil
		}
		checks := clusterSSHPreparedMembershipChecks{
			Inputs: func(ctx context.Context) error {
				deadline, _ := scope.Deadline()
				bound, stop := context.WithDeadline(ctx, deadline)
				defer stop()
				return lifetime.run(bound, checkInputs)
			},
			Authority: func(ctx context.Context) error {
				deadline, _ := scope.Deadline()
				bound, stop := context.WithDeadline(ctx, deadline)
				defer stop()
				return lifetime.run(bound, checkAuthority)
			},
		}
		if checks.Inputs(readerCtx) != nil {
			return errClusterConflict
		}
		value, err := copyClusterSSHPreparedMembershipObservation(*retained)
		if err != nil || consume(readerCtx, value, checks) != nil || readerCtx.Err() != nil {
			return errClusterConflict
		}
		// Cancel/join escaped explicit checks before the final full collection.
		// A consumer cannot swallow a failed check and gain success afterward.
		if lifetime.close() != nil {
			return errClusterConflict
		}
		return checkInputs(scope)
	})
	if err != nil || ctx.Err() != nil {
		return errClusterConflict
	}
	return nil
}

func copyClusterSSHPreparedMembershipObservation(value clusterSSHPreparedCohortMembershipObservation) (clusterSSHPreparedCohortMembershipObservation, error) {
	var owned clusterSSHPreparedCohortMembershipObservation
	raw, err := json.Marshal(value)
	if err != nil || json.Unmarshal(raw, &owned) != nil {
		return owned, errClusterConflict
	}
	return owned, nil
}
