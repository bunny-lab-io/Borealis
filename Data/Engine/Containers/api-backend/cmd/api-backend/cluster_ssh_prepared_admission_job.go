package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Prepared admission never adopts an earlier name-only Job. Exact Job/Pod
// custody and bounded host reconciliation keep cancellation from becoming an
// unobserved native action after original controller authority is lost.
func configureClusterSSHPreparedAdmissionJob(manifest map[string]any) {
	spec := clusterSSHStorageMap(manifest, "spec")
	spec["completions"], spec["parallelism"] = 1, 1
	spec["activeDeadlineSeconds"], spec["ttlSecondsAfterFinished"] = 3660, 86400
	spec["podReplacementPolicy"] = "Failed"
	pod := clusterSSHStorageMap(clusterSSHStorageMap(spec, "template"), "spec")
	pod["activeDeadlineSeconds"], pod["terminationGracePeriodSeconds"] = 3600, 1
	pod["securityContext"] = map[string]any{"seccompProfile": map[string]any{"type": "RuntimeDefault"}}
	container := anySlice(pod["containers"])[0].(map[string]any)
	container["terminationMessagePath"], container["terminationMessagePolicy"] = "/dev/termination-log", "File"
	container["resources"] = map[string]any{"requests": map[string]string{"cpu": "10m", "memory": "32Mi"}, "limits": map[string]string{"cpu": "100m", "memory": "64Mi"}}
}

func (r *kubernetesClusterStepRunner) runClusterSSHPreparedAdmissionJob(ctx context.Context, operation clusterControllerOperation,
	step clusterControllerStep, node clusterControllerNode, manifest map[string]any, request clusterbootstrap.PreparedAdmissionRequest) error {
	bound := clusterSSHPreparedAdmissionRuntimeFrom(ctx)
	if bound == nil || bound.cleanupKube == nil || request.StartBefore <= time.Now().Unix() {
		return errClusterConflict
	}
	configureClusterSSHPreparedAdmissionJob(manifest)
	var job map[string]any
	err := r.kube.doJSON(ctx, http.MethodPost, "/apis/batch/v1/namespaces/"+r.namespace+"/jobs", manifest, "application/json", &job, 30*time.Second)
	uid := cleanText(nestedMap(job, "metadata")["uid"])
	if err != nil || !clusterUUIDRE.MatchString(uid) || uid == "00000000-0000-0000-0000-000000000000" || !validClusterSSHSourceJob(job, manifest, uid) {
		// POST failure may be lost acknowledgement. Inspect exact deterministic
		// name only for containment; never adopt it as successful execution.
		path := "/apis/batch/v1/namespaces/" + r.namespace + "/jobs/" + cleanText(nestedMap(manifest, "metadata")["name"])
		job = nil
		if bound.cleanupKube.getJSON(context.WithoutCancel(ctx), path, &job) != nil {
			return errClusterConflict
		}
		uid = cleanText(nestedMap(job, "metadata")["uid"])
		if !clusterUUIDRE.MatchString(uid) || uid == "00000000-0000-0000-0000-000000000000" || !validClusterSSHSourceJob(job, manifest, uid) {
			return errClusterConflict
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		if r.containClusterSSHPreparedAdmissionJob(cleanupCtx, bound.cleanupKube, operation, step, node, manifest, uid, request) != nil {
			return errClusterConflict
		}
		return errClusterConflict
	}
	err = r.waitClusterSSHPreparedAdmissionJob(ctx, r.kube, manifest, uid)
	if err == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancel()
	if containErr := r.containClusterSSHPreparedAdmissionJob(cleanupCtx, bound.cleanupKube, operation, step, node, manifest, uid, request); containErr != nil {
		return errors.Join(err, containErr)
	}
	return err
}

func (r *kubernetesClusterStepRunner) waitClusterSSHPreparedAdmissionJob(ctx context.Context, kube *kubernetesAPIClient,
	manifest map[string]any, uid string) error {
	name := cleanText(nestedMap(manifest, "metadata")["name"])
	path := "/apis/batch/v1/namespaces/" + r.namespace + "/jobs/" + name
	podsPath := "/api/v1/namespaces/" + r.namespace + "/pods?labelSelector=" + url.QueryEscape("batch.kubernetes.io/controller-uid="+uid)
	interval := r.jobPollInterval
	if interval <= 0 || interval > time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	observedPodUID := ""
	for {
		var job map[string]any
		if kube.getJSON(ctx, path, &job) != nil || !validClusterSSHSourceJob(job, manifest, uid) {
			return errClusterConflict
		}
		status := nestedMap(job, "status")
		if !sourceCount(status["failed"], 0) || !validSourceConditions(status) || sourceConditionTrue(status, "Failed") || sourceConditionTrue(status, "FailureTarget") {
			return errClusterConflict
		}
		var pods struct {
			Metadata struct{ Continue string }
			Items    []map[string]any
		}
		if kube.getJSON(ctx, podsPath, &pods) != nil || pods.Metadata.Continue != "" || len(pods.Items) > 1 {
			return errClusterConflict
		}
		if len(pods.Items) == 1 {
			podUID := cleanText(nestedMap(pods.Items[0], "metadata")["uid"])
			if observedPodUID != "" && podUID != observedPodUID {
				return errClusterConflict
			}
			observedPodUID = podUID
			complete, err := validClusterSSHPreparedAdmissionPod(pods.Items[0], manifest, uid)
			if err != nil {
				return err
			}
			if complete && sourceConditionTrue(status, "Complete") && sourceCount(status["succeeded"], 1) && sourceCount(status["active"], 0) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func validClusterSSHPreparedAdmissionPod(pod, manifest map[string]any, jobUID string) (bool, error) {
	metadata, spec, status := nestedMap(pod, "metadata"), nestedMap(pod, "spec"), nestedMap(pod, "status")
	podUID := cleanText(metadata["uid"])
	namespace := cleanText(nestedMap(manifest, "metadata")["namespace"])
	owners := anySlice(metadata["ownerReferences"])
	if pod["apiVersion"] != "v1" || pod["kind"] != "Pod" || namespace == "" || metadata["namespace"] != namespace || metadata["deletionTimestamp"] != nil ||
		!clusterUUIDRE.MatchString(podUID) || podUID == "00000000-0000-0000-0000-000000000000" || len(owners) != 1 ||
		nestedMap(metadata, "labels")["batch.kubernetes.io/controller-uid"] != jobUID ||
		!validClusterSSHSourcePodSpec(spec, nestedMap(nestedMap(nestedMap(manifest, "spec"), "template"), "spec")) {
		return false, errClusterConflict
	}
	owner, _ := owners[0].(map[string]any)
	if owner["apiVersion"] != "batch/v1" || owner["kind"] != "Job" || owner["name"] != nestedMap(manifest, "metadata")["name"] || owner["uid"] != jobUID || owner["controller"] != true {
		return false, errClusterConflict
	}
	phase := cleanText(status["phase"])
	if phase != "Pending" && phase != "Running" && phase != "Succeeded" {
		return false, errClusterConflict
	}
	statuses := anySlice(status["containerStatuses"])
	if len(statuses) == 0 && phase == "Pending" {
		return false, nil
	}
	if len(statuses) != 1 {
		return false, errClusterConflict
	}
	container, _ := statuses[0].(map[string]any)
	if container["name"] != "action" || !sourceCount(container["restartCount"], 0) || len(nestedMap(container, "lastState")) != 0 || len(nestedMap(container, "state")) != 1 {
		return false, errClusterConflict
	}
	terminated := nestedMap(nestedMap(container, "state"), "terminated")
	if len(terminated) == 0 && phase != "Succeeded" {
		return false, nil
	}
	if !sourceCount(terminated["exitCode"], 0) || terminated["exitCode"] == nil || terminated["reason"] != "Completed" || phase != "Succeeded" {
		return false, errClusterConflict
	}
	return true, nil
}

func (r *kubernetesClusterStepRunner) containClusterSSHPreparedAdmissionJob(ctx context.Context, kube *kubernetesAPIClient,
	operation clusterControllerOperation, step clusterControllerStep, node clusterControllerNode, manifest map[string]any, uid string,
	request clusterbootstrap.PreparedAdmissionRequest) error {
	name := cleanText(nestedMap(manifest, "metadata")["name"])
	path := "/apis/batch/v1/namespaces/" + r.namespace + "/jobs/" + name
	podsPath := "/api/v1/namespaces/" + r.namespace + "/pods?labelSelector=" + url.QueryEscape("batch.kubernetes.io/controller-uid="+uid)
	deleteOptions := map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground", "preconditions": map[string]any{"uid": uid}}
	var deleted map[string]any
	if err := kube.doJSON(ctx, http.MethodDelete, path, deleteOptions, "application/json", &deleted, 10*time.Second); err != nil && !kubernetesAPIErrorHasStatus(err, http.StatusNotFound) {
		return errClusterConflict
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var job map[string]any
		jobErr := kube.getJSON(ctx, path, &job)
		var pods struct {
			Metadata struct{ Continue string }
			Items    []map[string]any
		}
		podsErr := kube.getJSON(ctx, podsPath, &pods)
		if kubernetesAPIErrorHasStatus(jobErr, http.StatusNotFound) && podsErr == nil && pods.Metadata.Continue == "" && len(pods.Items) == 0 {
			break
		}
		if jobErr != nil && !kubernetesAPIErrorHasStatus(jobErr, http.StatusNotFound) || podsErr != nil || pods.Metadata.Continue != "" {
			return errClusterConflict
		}
		select {
		case <-ctx.Done():
			return errClusterConflict
		case <-ticker.C:
		}
	}
	request.Action = "ReconcilePreparedAdmissionStopped"
	request.StartBefore = time.Now().Add(30 * time.Second).Unix()
	encoded, err := clusterbootstrap.EncodePreparedAdmissionRequest(request)
	if err != nil {
		return errClusterConflict
	}
	reconcileName := "borealis-reconcile-" + strings.ReplaceAll(newClusterUUID(), "-", "")
	reconcile := clusterActionJobManifest(reconcileName, r.namespace, node.Name, clusterOperationActionImage(operation, r.actionImage),
		[]string{"client", "--verb", clusterbootstrap.PreparedAdmissionVerb, "--prepared-admission", encoded}, operation.ID, step.Name+":reconcile_stopped")
	configureClusterSSHPreparedAdmissionJob(reconcile)
	var job map[string]any
	if kube.doJSON(ctx, http.MethodPost, "/apis/batch/v1/namespaces/"+r.namespace+"/jobs", reconcile, "application/json", &job, 10*time.Second) != nil {
		return errClusterConflict
	}
	reconcileUID := cleanText(nestedMap(job, "metadata")["uid"])
	if !clusterUUIDRE.MatchString(reconcileUID) || !validClusterSSHSourceJob(job, reconcile, reconcileUID) {
		return errClusterConflict
	}
	if err := r.waitClusterSSHPreparedAdmissionJob(ctx, kube, reconcile, reconcileUID); err != nil {
		return fmt.Errorf("prepared admission producer reconciliation failed: %w", err)
	}
	return nil
}

// A paired expansion cannot leave an earlier target active when a later target
// fails. Operation's durable running fence prevents another controller from
// starting admission while this exact-receipt containment finishes without the
// cancelled effects transport.
func (r *kubernetesClusterStepRunner) containClusterSSHPreparedAdmissionTargets(parent context.Context,
	operation clusterControllerOperation, nodes []clusterControllerNode) error {
	bound := clusterSSHPreparedAdmissionRuntimeFrom(parent)
	if bound == nil || bound.cleanupKube == nil || len(nodes) == 0 {
		return errClusterConflict
	}
	// A failed cleanup remains blocked; a second caller cannot replay native
	// drain Jobs or reinterpret uncertain cleanup as fresh authority.
	bound.containmentAttempted = true
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Minute)
	defer cancel()
	cleanupRunner := *r
	cleanupRunner.kube = bound.cleanupKube
	var result error
	for _, node := range nodes {
		var request clusterbootstrap.PreparedAdmissionRequest
		for _, target := range bound.authority.receipt.Targets {
			if target.TargetID == node.ID && target.Prejoin != nil && target.Prejoin.Receipt.Node.Hostname == node.Name {
				request = clusterbootstrap.PreparedAdmissionRequest{Version: 1, Action: "EnterApplicationDrain",
					StartBefore: time.Now().Add(5 * time.Minute).Unix(), Expected: target.Prejoin.Expected, Receipt: target.Prejoin.Receipt}
			}
		}
		if request.Validate() != nil {
			result = errors.Join(result, errClusterConflict)
			continue
		}
		var object map[string]any
		nodeFenced := false
		path := "/api/v1/nodes/" + node.Name
		if err := bound.cleanupKube.getJSON(ctx, path, &object); err != nil {
			result = errors.Join(result, errClusterConflict)
		} else {
			raw, err := json.Marshal(object)
			parsed, parseErr := clusterbootstrap.ParsePreparedAdmissionNode(raw, request)
			metadata := nestedMap(object, "metadata")
			if err != nil || parseErr != nil || parsed.NodeUID != request.Receipt.Node.NodeUID || cleanText(metadata["resourceVersion"]) == "" {
				result = errors.Join(result, errClusterConflict)
			} else {
				patch := map[string]any{"metadata": map[string]any{"uid": parsed.NodeUID, "resourceVersion": metadata["resourceVersion"], "labels": map[string]string{
					"borealis.io/application-state": "drained", "borealis.io/control-plane-eligible": "false", "borealis.io/edge-eligible": "false",
					"borealis.io/scheduler-eligible": "false", "borealis.io/postgres-primary-eligible": "false",
				}}}
				var output map[string]any
				if err := bound.cleanupKube.doJSON(ctx, http.MethodPatch, path, patch, "application/strategic-merge-patch+json", &output, 10*time.Second); err != nil {
					result = errors.Join(result, errClusterConflict)
				} else {
					nodeFenced = true
				}
			}
		}
		// A same-name replacement is outside this receipt's authority. Only
		// discover its workloads after the exact original Node was fenced.
		if nodeFenced {
			if err := cleanupRunner.stopClusterSSHPreparedWireGuard(ctx, node.Name); err != nil {
				result = errors.Join(result, err)
			}
		}
		encoded, err := clusterbootstrap.EncodePreparedAdmissionRequest(request)
		if err != nil {
			result = errors.Join(result, errClusterConflict)
			continue
		}
		step := clusterControllerStep{Name: "admit:" + node.ID + ":contain_drain", NodeID: node.ID}
		manifest := clusterActionJobManifest(clusterActionJobName(operation.ID, "attempt:"+fmt.Sprint(operation.Attempt)+":"+step.Name), r.namespace,
			node.Name, clusterOperationActionImage(operation, r.actionImage),
			[]string{"client", "--verb", clusterbootstrap.PreparedAdmissionVerb, "--prepared-admission", encoded}, operation.ID, step.Name)
		if err := cleanupRunner.runClusterSSHPreparedAdmissionJob(ctx, operation, step, node, manifest, request); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (r *kubernetesClusterStepRunner) stopClusterSSHPreparedWireGuard(ctx context.Context, nodeName string) error {
	if r == nil || r.kube == nil || !clusterControllerNodeRegex.MatchString(nodeName) {
		return errClusterConflict
	}
	selector := "borealis.io/engine-node=" + nodeName + ",borealis.io/node-workload=true,app.kubernetes.io/name=wireguard-tunnel"
	collection := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments?labelSelector=%s", r.namespace, url.QueryEscape(selector))
	var deployments map[string]any
	if r.kube.getJSON(ctx, collection, &deployments) != nil || cleanText(nestedMap(deployments, "metadata")["continue"]) != "" {
		return errClusterConflict
	}
	items := anySlice(deployments["items"])
	if len(items) != 1 {
		return errClusterConflict
	}
	deployment, ok := items[0].(map[string]any)
	metadata := nestedMap(deployment, "metadata")
	labels := clusterStringMap(metadata["labels"])
	uid, resourceVersion := cleanText(metadata["uid"]), cleanText(metadata["resourceVersion"])
	name := "wireguard-tunnel-" + nodeName
	if !ok || deployment["apiVersion"] != "apps/v1" || deployment["kind"] != "Deployment" || cleanText(metadata["name"]) != name ||
		cleanText(metadata["namespace"]) != r.namespace || metadata["deletionTimestamp"] != nil || !clusterUUIDRE.MatchString(uid) ||
		uid == "00000000-0000-0000-0000-000000000000" || resourceVersion == "" || labels["app.kubernetes.io/name"] != "wireguard-tunnel" ||
		labels["borealis.io/engine-node"] != nodeName || labels["borealis.io/node-workload"] != "true" {
		return errClusterConflict
	}
	patch := map[string]any{"metadata": map[string]any{"uid": uid, "resourceVersion": resourceVersion}, "spec": map[string]any{"replicas": int64(0)}}
	var output map[string]any
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", r.namespace, name)
	if r.kube.doJSON(ctx, http.MethodPatch, path, patch, "application/merge-patch+json", &output, 10*time.Second) != nil {
		return errClusterConflict
	}
	return nil
}
