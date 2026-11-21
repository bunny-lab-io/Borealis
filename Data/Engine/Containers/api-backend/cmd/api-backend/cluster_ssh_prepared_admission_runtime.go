package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"time"
)

// Admission borrows existing operation/Job custody. This context binds the
// existing runner to settled target identities; it creates no preparation lease.
type clusterSSHPreparedAdmissionRuntime struct {
	operation            clusterControllerOperation
	authority            clusterSSHPreparedAuthority
	check                func(context.Context) error
	startBefore          func(context.Context) (int64, error)
	cleanupKube          *kubernetesAPIClient
	mutated              bool
	containmentAttempted bool
}

type clusterSSHPreparedAdmissionRuntimeKey struct{}

func clusterSSHPreparedAdmissionRuntimeFrom(ctx context.Context) *clusterSSHPreparedAdmissionRuntime {
	value, _ := ctx.Value(clusterSSHPreparedAdmissionRuntimeKey{}).(*clusterSSHPreparedAdmissionRuntime)
	return value
}

// Reads between runner steps can spend the remaining credential/lease life.
// Reuse the same authority checker immediately before every Kubernetes effect.
type clusterSSHPreparedAdmissionHTTP struct {
	transport http.RoundTripper
	check     func(context.Context) error
}

func (t clusterSSHPreparedAdmissionHTTP) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		if t.check == nil || t.check(request.Context()) != nil || request.Context().Err() != nil {
			return nil, errClusterConflict
		}
	}
	return t.transport.RoundTrip(request)
}

func (a *clusterSSHPreparedAdmissionRuntime) request(ctx context.Context, node clusterControllerNode, action string) (clusterbootstrap.PreparedAdmissionRequest, error) {
	if a == nil || a.check == nil || a.startBefore == nil || a.check(ctx) != nil || ctx.Err() != nil {
		return clusterbootstrap.PreparedAdmissionRequest{}, errClusterConflict
	}
	startBefore, err := a.startBefore(ctx)
	now := time.Now()
	if err != nil || startBefore <= now.Unix() || a.check(ctx) != nil {
		return clusterbootstrap.PreparedAdmissionRequest{}, errClusterConflict
	}
	if bounded := now.Add(5 * time.Minute).Unix(); startBefore > bounded {
		startBefore = bounded
	}
	for _, target := range a.authority.receipt.Targets {
		if target.TargetID == node.ID && target.Prejoin != nil && target.Prejoin.Receipt.Node.Hostname == node.Name {
			return clusterbootstrap.PreparedAdmissionRequest{Version: 1, Action: action, StartBefore: startBefore, Expected: target.Prejoin.Expected, Receipt: target.Prejoin.Receipt}, nil
		}
	}
	return clusterbootstrap.PreparedAdmissionRequest{}, errClusterConflict
}

// SQL heartbeats retain original operation/source/credentials. Native action
// guards retain target journal, machine, Node and shared identity during work.
// The existing three-hour admission ceiling extends none of those authorities.
func (c *clusterController) withClusterSSHPreparedAdmissionRuntime(parent context.Context, operation clusterControllerOperation,
	original clusterSSHPreparedAuthority, consume func(context.Context, *kubernetesClusterStepRunner) error) error {
	if c == nil || c.store == nil || c.store.db == nil || parent.Err() != nil {
		return errClusterConflict
	}
	runner, ok := c.runner.(*kubernetesClusterStepRunner)
	if !ok || runner == nil || runner.kube == nil || runner.kube.httpClient == nil || runner.controllerHolder != c.holder ||
		operation.Kind != "ssh_onboarding" || !textInSet(operation.CurrentStep, "apply_membership", "verify_quorum") || consume == nil {
		return errClusterConflict
	}
	holder := c.holder
	transport := runner.kube.httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	effects := &clusterSSHMembershipHTTP{transport: clusterSSHPreparationConfigurationHTTP{transport}}
	cleanup := &clusterSSHMembershipHTTP{transport: clusterSSHPreparationConfigurationHTTP{transport}}
	client, cleanupClient, kube, cleanupKube, ownedRunner := *runner.kube.httpClient, *runner.kube.httpClient, *runner.kube, *runner.kube, *runner
	client.Transport = effects
	cleanupClient.Transport = cleanup
	kube.httpClient, ownedRunner.kube = &client, &kube
	cleanupKube.httpClient = &cleanupClient
	serial := make(chan struct{}, 1)
	check := func(parent context.Context) error {
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		select {
		case serial <- struct{}{}:
			defer func() { <-serial }()
		case <-ctx.Done():
			return errClusterConflict
		}
		if ctx.Err() != nil || effects.failed.Load() || cleanup.failed.Load() || c.holder != holder || c.runner != runner || runner.controllerHolder != holder {
			return errClusterConflict
		}
		current, _, err := c.loadClusterSSHPreparedAuthority(ctx, operation)
		if err != nil || ctx.Err() != nil || !reflect.DeepEqual(current, original) || effects.failed.Load() || cleanup.failed.Load() {
			return errClusterConflict
		}
		return nil
	}
	effects.transport = clusterSSHPreparationConfigurationHTTP{clusterSSHPreparedAdmissionHTTP{transport, check}}
	startBefore := func(parent context.Context) (int64, error) {
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		var expiry int64
		err := c.store.db.QueryRowContext(ctx, `WITH moment AS MATERIALIZED(SELECT extract(epoch FROM clock_timestamp()) AS now)
 SELECT LEAST(l.expires_at,MIN(p.expires_at)) FROM moment,engine.cluster_application_leases l,
 engine.cluster_operations o JOIN engine.cluster_state s ON s.id=1 AND s.active_operation_id=o.id
 JOIN engine.cluster_onboarding_targets t ON t.operation_id=o.id
 JOIN engine.cluster_onboarding_credentials p ON p.target_id=t.id
 WHERE l.name=$1 AND l.holder=$2 AND l.expires_at>moment.now AND o.id=$3 AND o.kind='ssh_onboarding'
 AND o.state='running' AND o.current_step=$4 AND o.attempt=$5 AND o.payload_json=$6 AND p.expires_at>moment.now
 GROUP BY l.expires_at HAVING count(*)=$7`, clusterControllerLeaseName, holder, operation.ID, operation.CurrentStep,
			operation.Attempt, original.payload, len(original.receipt.Targets)).Scan(&expiry)
		if err != nil || ctx.Err() != nil {
			return 0, errClusterConflict
		}
		return expiry, nil
	}
	bound := &clusterSSHPreparedAdmissionRuntime{operation: operation, authority: original, check: check, startBefore: startBefore, cleanupKube: &cleanupKube}
	err := runClusterSSHPreparationScopeFor(parent, 5*time.Second, clusterControllerStepTimeout(operation.CurrentStep), check,
		func(ctx context.Context) error {
			ctx = context.WithValue(ctx, clusterSSHPreparedAdmissionRuntimeKey{}, bound)
			return consume(ctx, &ownedRunner)
		})
	if err != nil || parent.Err() != nil || !effects.clean() || !cleanup.clean() {
		// Endpoint verification and the scope's final authority/cleanup check
		// occur after the admission runner returns. Their failure must contain
		// the same pair, including when every native activation already passed.
		if bound.mutated && !bound.containmentAttempted {
			nodes := make([]clusterControllerNode, 0, len(original.receipt.Targets))
			for _, target := range original.receipt.Targets {
				if target.Prejoin != nil {
					nodes = append(nodes, clusterControllerNode{ID: target.TargetID, Name: target.Prejoin.Receipt.Node.Hostname})
				}
			}
			cleanupContext := context.WithValue(context.WithoutCancel(parent), clusterSSHPreparedAdmissionRuntimeKey{}, bound)
			_ = ownedRunner.containClusterSSHPreparedAdmissionTargets(cleanupContext, operation, nodes)
		}
		return errClusterConflict
	}
	return nil
}

func (r *kubernetesClusterStepRunner) preparedAdmissionNodeMetadata(ctx context.Context, name string) (map[string]any, error) {
	bound := clusterSSHPreparedAdmissionRuntimeFrom(ctx)
	if bound == nil {
		return map[string]any{}, nil
	}
	var target clusterControllerNode
	for _, prepared := range bound.authority.receipt.Targets {
		if prepared.Prejoin != nil && prepared.Prejoin.Receipt.Node.Hostname == name {
			target = clusterControllerNode{ID: prepared.TargetID, Name: name}
		}
	}
	request, err := bound.request(ctx, target, "InspectHealth")
	if err != nil {
		return nil, err
	}
	var list struct{ Items []json.RawMessage }
	if r.kube.getClusterSSHNodeCohortJSON(ctx, "/api/v1/nodes", &list) != nil {
		return nil, errClusterConflict
	}
	var raw json.RawMessage
	for _, item := range list.Items {
		var node struct{ Metadata struct{ Name string } }
		if json.Unmarshal(item, &node) != nil {
			return nil, errClusterConflict
		}
		if node.Metadata.Name == name {
			if raw != nil {
				return nil, errClusterConflict
			}
			raw = item
		}
	}
	if _, err := clusterbootstrap.ParsePreparedAdmissionNode(raw, request); err != nil {
		return nil, errClusterConflict
	}
	var node struct {
		Metadata struct {
			UID, ResourceVersion string
		}
	}
	if json.Unmarshal(raw, &node) != nil || node.Metadata.UID != request.Receipt.Node.NodeUID || node.Metadata.ResourceVersion == "" || bound.check(ctx) != nil {
		return nil, errClusterConflict
	}
	// Kubernetes rejects a replacement UID or stale resourceVersion atomically.
	return map[string]any{"uid": node.Metadata.UID, "resourceVersion": node.Metadata.ResourceVersion}, nil
}
