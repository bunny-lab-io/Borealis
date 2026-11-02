package main

import (
	"context"
	"net/http"
	"reflect"
	"strings"
)

// The caller must retain these identities from original trusted membership
// evidence, never discover replacements during recovery. ClusterID identifies
// the logical SQL cluster; KubeSystemUID independently identifies Kubernetes.
// Neither this binding nor its partial result grants maintenance/return rights.
type clusterAutomaticRecoveryNodeBinding struct {
	ClusterID, KubeSystemUID, NodeID, NodeName, NodeUID, Controller string
}

func (b clusterAutomaticRecoveryNodeBinding) valid() bool {
	for _, id := range []string{b.ClusterID, b.KubeSystemUID, b.NodeID, b.NodeUID} {
		if !clusterUUIDRE.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return false
		}
	}
	return clusterControllerNodeRegex.MatchString(b.NodeName) && b.Controller != "" &&
		strings.TrimSpace(b.Controller) == b.Controller && len(b.Controller) <= 1024
}

// Callable composition for the observation-only coordinator. There is no
// production caller. Existing SQL authority reads bracket native GETs without
// retaining a pool connection; the coordinator separately fences persistence.
func (c *clusterAutomaticRecoveryCoordinator) nodeHealthObserver(binding clusterAutomaticRecoveryNodeBinding) clusterAutomaticRecoveryObserve {
	return c.nativeHealthObserver(binding, func(ctx context.Context, kube *kubernetesAPIClient) (clusterAutomaticRecoveryHealth, error) {
		return readClusterAutomaticRecoveryNodeHealth(ctx, kube.getClusterAutomaticRecoveryJSON, binding)
	})
}

func (c *clusterAutomaticRecoveryCoordinator) nativeHealthObserver(binding clusterAutomaticRecoveryNodeBinding, read func(context.Context, *kubernetesAPIClient) (clusterAutomaticRecoveryHealth, error)) clusterAutomaticRecoveryObserve {
	return func(parent context.Context, node clusterControllerNode) (clusterAutomaticRecoveryHealth, error) {
		fail := func() (clusterAutomaticRecoveryHealth, error) {
			return clusterAutomaticRecoveryHealth{}, errClusterConflict
		}
		if c == nil || c.controller == nil || c.controller.store == nil || c.controller.store.db == nil ||
			read == nil || !binding.valid() || binding.Controller != c.controller.holder || node.ID != binding.NodeID || node.Name != binding.NodeName {
			return fail()
		}
		runner, ok := c.controller.runner.(*kubernetesClusterStepRunner)
		if !ok || runner == nil || runner.kube == nil || runner.controllerHolder != binding.Controller {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, clusterControllerLeaseAcquireTimeout)
		defer cancel()
		before, started, err := readClusterAutomaticRecoverySnapshot(ctx, c.controller.store.db, binding.Controller)
		if err != nil || before.ClusterID != binding.ClusterID || !reflect.DeepEqual(clusterNodeByID(before.Nodes, binding.NodeID), node) {
			return fail()
		}
		health, err := read(ctx, runner.kube)
		if err != nil {
			return fail()
		}
		after, finished, err := readClusterAutomaticRecoverySnapshot(ctx, c.controller.store.db, binding.Controller)
		if err != nil || ctx.Err() != nil || finished < started || !reflect.DeepEqual(before, after) ||
			c.controller.holder != binding.Controller || runner.controllerHolder != binding.Controller {
			return fail()
		}
		return health, nil
	}
}

// The generic GET decoder accepts a first JSON value and follows redirects.
// Reuse the existing full-body, bounded, no-redirect HTTPS transport while
// limiting this capability to namespace identity and a validated Node name.
func (c *kubernetesAPIClient) getClusterAutomaticRecoveryJSON(ctx context.Context, path string, out any) error {
	const nodes = "/api/v1/nodes/"
	if path != "/api/v1/namespaces/kube-system" &&
		(!strings.HasPrefix(path, nodes) || !clusterControllerNodeRegex.MatchString(strings.TrimPrefix(path, nodes))) {
		return errClusterConflict
	}
	return c.clusterSSHPrivateJSON(ctx, http.MethodGet, path, nil, out)
}

func readClusterAutomaticRecoveryNodeHealth(ctx context.Context, getJSON func(context.Context, string, any) error, binding clusterAutomaticRecoveryNodeBinding) (clusterAutomaticRecoveryHealth, error) {
	fail := func() (clusterAutomaticRecoveryHealth, error) {
		return clusterAutomaticRecoveryHealth{}, errClusterConflict
	}
	if !binding.valid() || getJSON == nil || ctx.Err() != nil {
		return fail()
	}
	read := func(path, kind, name, uid string) (map[string]any, error) {
		var object clusterNativeObservationObject
		if getJSON(ctx, path, &object) != nil || ctx.Err() != nil {
			return nil, errClusterConflict
		}
		identity, ok := clusterSSHStorageMetadata(object, "v1", kind, "")
		if !ok || identity.Name != name || identity.UID != uid {
			return nil, errClusterConflict
		}
		return object, nil
	}
	namespace := func() error {
		_, err := read("/api/v1/namespaces/kube-system", "Namespace", "kube-system", binding.KubeSystemUID)
		return err
	}
	node := func() error {
		object, err := read("/api/v1/nodes/"+binding.NodeName, "Node", binding.NodeName, binding.NodeUID)
		status := clusterSSHStorageMap(object, "status")
		if err != nil || !validSourceConditions(status) || !sourceConditionTrue(status, "Ready") {
			return errClusterConflict
		}
		return nil
	}
	// Repeated exact identity reads detect replacements during observation.
	if namespace() != nil || node() != nil || node() != nil || namespace() != nil || ctx.Err() != nil {
		return fail()
	}
	// This proves current API-reported Ready only, not independent kubelet or
	// application liveness. Every unavailable downstream surface stays unknown.
	return clusterAutomaticRecoveryHealth{NodeName: binding.NodeName, NodeUID: binding.NodeUID, Node: true}, nil
}
