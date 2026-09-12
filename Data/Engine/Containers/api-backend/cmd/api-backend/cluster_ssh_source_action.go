package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

// This capability belongs behind the existing controller boundary. API
// workers must not acquire a Kubernetes token to call it. The future guarded
// broker supplies current Aegis/worker authority; no preparation phase is
// activated by constructing this reader.
func (r *kubernetesClusterStepRunner) newSSHSourceNetworkRead(authority clusterSSHPreparationAuthorityRead) clusterSSHSourceNetworkRead {
	return func(parent context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
		fail := func() (clusterbootstrap.SourceNetwork, error) {
			return clusterbootstrap.SourceNetwork{}, clusterbootstrap.ErrPreparationConfig
		}
		if r == nil || r.kube == nil || r.namespace != "borealis" || r.controllerHolder == "" || authority == nil ||
			!borealisOperatorImmutableImageRefPattern.MatchString(r.actionImage) {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		before, err := authority(ctx)
		if err != nil || before.Baseline.Validate() != nil || !validClusterSSHPreparationLease(before.Lease) ||
			before.Lease.ControllerHolder != r.controllerHolder || validateClusterSSHInspectionCohort(before.Cohort, before.Source) != nil {
			return fail()
		}
		found := false
		for _, source := range before.Source.Members {
			if source == member {
				found = true
			}
		}
		if !found {
			return fail()
		}
		check := func() bool {
			after, err := authority(ctx)
			original := before
			original.Cohort.ObservedAt, after.Cohort.ObservedAt = 0, 0
			return err == nil && ctx.Err() == nil && reflect.DeepEqual(original, after)
		}
		nonce := newClusterUUID()
		name := "borealis-source-" + strings.ReplaceAll(nonce, "-", "")
		manifest := clusterSSHSourceJobManifest(name, nonce, r.actionImage, member.Name, before.Lease.OperationID)
		var job map[string]any
		// One POST only. Conflict or an uncertain POST receipt fails this read;
		// neither a name lookup nor old successful Job can substitute for it.
		if !check() || r.kube.doClusterSSHSourceJSON(ctx, http.MethodPost, "/apis/batch/v1/namespaces/borealis/jobs", manifest, &job) != nil {
			return fail()
		}
		uid, _ := nestedMap(job, "metadata")["uid"].(string)
		if !clusterbootstrap.ValidSourceReceiptIdentity(nonce, uid, uid) || !validClusterSSHSourceJob(job, manifest, uid) {
			return fail()
		}
		path := "/apis/batch/v1/namespaces/borealis/jobs/" + name
		podsPath := "/api/v1/namespaces/borealis/pods?labelSelector=" + url.QueryEscape("batch.kubernetes.io/controller-uid="+uid)
		interval := r.jobPollInterval
		if interval <= 0 || interval > time.Second {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		nextCheck := time.Now().Add(5 * time.Second)
		observedPodUID := ""
		for {
			if !time.Now().Before(nextCheck) {
				if !check() {
					return fail()
				}
				nextCheck = time.Now().Add(5 * time.Second)
			}
			// JSON decoding into a reused map retains omitted keys. Each read
			// must stand alone, including metadata and completion state.
			job = nil
			if r.kube.doClusterSSHSourceJSON(ctx, http.MethodGet, path, nil, &job) != nil || !validClusterSSHSourceJob(job, manifest, uid) {
				return fail()
			}
			status := nestedMap(job, "status")
			if !sourceCount(status["failed"], 0) || !validSourceConditions(status) || sourceConditionTrue(status, "Failed") || sourceConditionTrue(status, "FailureTarget") {
				return fail()
			}
			var pods struct {
				Metadata struct{ Continue string }
				Items    []map[string]any
			}
			if r.kube.doClusterSSHSourceJSON(ctx, http.MethodGet, podsPath, nil, &pods) != nil || pods.Metadata.Continue != "" || len(pods.Items) > 1 {
				return fail()
			}
			if len(pods.Items) == 1 {
				podUID, _ := nestedMap(pods.Items[0], "metadata")["uid"].(string)
				if observedPodUID != "" && podUID != observedPodUID {
					return fail()
				}
				observedPodUID = podUID
				network, complete, err := clusterSSHSourcePodResult(pods.Items[0], manifest, nonce, uid)
				if err != nil {
					return fail()
				}
				if complete && sourceConditionTrue(status, "Complete") && sourceCount(status["succeeded"], 1) && sourceCount(status["active"], 0) {
					if network.NodeUID != member.NodeUID || network.Hostname != member.Name || network.MachineID != member.MachineID ||
						network.BootID != member.BootID || !network.ManagementLink.MatchesAddress(member.Address) || network.K3sVersion != before.K3sVersion || !check() {
						return fail()
					}
					return network, nil
				}
			}
			select {
			case <-ctx.Done():
				return fail()
			case <-ticker.C:
			}
		}
	}
}

func clusterSSHSourceJobManifest(name, nonce, image, node, operationID string) map[string]any {
	m := clusterActionJobManifest(name, "borealis", node, image, []string{nonce}, operationID, "source_network:"+nonce)
	spec := m["spec"].(map[string]any)
	spec["completions"], spec["parallelism"], spec["activeDeadlineSeconds"], spec["ttlSecondsAfterFinished"] = 1, 1, 25, 60
	spec["podReplacementPolicy"] = "Failed"
	pod := spec["template"].(map[string]any)["spec"].(map[string]any)
	pod["activeDeadlineSeconds"], pod["terminationGracePeriodSeconds"] = 20, 1
	pod["securityContext"] = map[string]any{"seccompProfile": map[string]any{"type": "RuntimeDefault"}}
	c := anySlice(pod["containers"])[0].(map[string]any)
	c["command"] = []string{"/usr/local/bin/borealis-node-manager", "source-network-client"}
	c["terminationMessagePath"], c["terminationMessagePolicy"] = "/dev/termination-log", "File"
	c["resources"] = map[string]any{"requests": map[string]string{"cpu": "10m", "memory": "32Mi"}, "limits": map[string]string{"cpu": "100m", "memory": "64Mi"}}
	c["env"] = []any{
		map[string]any{"name": "BOREALIS_SOURCE_JOB_UID", "valueFrom": map[string]any{"fieldRef": map[string]any{"apiVersion": "v1", "fieldPath": "metadata.labels['batch.kubernetes.io/controller-uid']"}}},
		map[string]any{"name": "BOREALIS_SOURCE_POD_UID", "valueFrom": map[string]any{"fieldRef": map[string]any{"apiVersion": "v1", "fieldPath": "metadata.uid"}}},
	}
	return m
}

func sourceJSONEqual(a, b any) bool {
	left, err := json.Marshal(a)
	right, other := json.Marshal(b)
	return err == nil && other == nil && bytes.Equal(left, right)
}

func validClusterSSHSourcePodSpec(actual, expected map[string]any) bool {
	for _, key := range []string{"nodeName", "restartPolicy", "serviceAccountName", "automountServiceAccountToken", "activeDeadlineSeconds", "terminationGracePeriodSeconds", "securityContext", "volumes"} {
		if !sourceJSONEqual(actual[key], expected[key]) {
			return false
		}
	}
	for _, key := range []string{"hostNetwork", "hostPID", "hostIPC", "shareProcessNamespace"} {
		if value, exists := actual[key]; exists && value != false {
			return false
		}
	}
	if len(anySlice(actual["initContainers"])) != 0 || len(anySlice(actual["ephemeralContainers"])) != 0 || actual["runtimeClassName"] != nil {
		return false
	}
	containers, wanted := anySlice(actual["containers"]), anySlice(expected["containers"])
	if len(containers) != 1 || len(wanted) != 1 {
		return false
	}
	c, ok := containers[0].(map[string]any)
	w, _ := wanted[0].(map[string]any)
	if !ok || len(anySlice(c["envFrom"])) != 0 || c["lifecycle"] != nil {
		return false
	}
	for _, key := range []string{"livenessProbe", "readinessProbe", "startupProbe", "volumeDevices"} {
		if c[key] != nil {
			return false
		}
	}
	for _, key := range []string{"name", "image", "imagePullPolicy", "command", "args", "securityContext", "resources", "volumeMounts", "env", "terminationMessagePath", "terminationMessagePolicy"} {
		if !sourceJSONEqual(c[key], w[key]) {
			return false
		}
	}
	return true
}

func validClusterSSHSourceJob(actual, expected map[string]any, uid string) bool {
	metadata := nestedMap(actual, "metadata")
	if actual["apiVersion"] != "batch/v1" || actual["kind"] != "Job" || metadata["uid"] != uid || metadata["deletionTimestamp"] != nil ||
		validateClusterActionJobIdentity(actual, expected) != nil {
		return false
	}
	spec, wanted := nestedMap(actual, "spec"), nestedMap(expected, "spec")
	for _, key := range []string{"backoffLimit", "completions", "parallelism", "activeDeadlineSeconds", "ttlSecondsAfterFinished", "podReplacementPolicy"} {
		if !sourceJSONEqual(spec[key], wanted[key]) {
			return false
		}
	}
	for _, key := range []string{"podFailurePolicy", "successPolicy", "backoffLimitPerIndex", "maxFailedIndexes"} {
		if spec[key] != nil {
			return false
		}
	}
	if (spec["completionMode"] != nil && spec["completionMode"] != "NonIndexed") || (spec["managedBy"] != nil && spec["managedBy"] != "kubernetes.io/job-controller") {
		return false
	}
	for _, key := range []string{"manualSelector", "suspend"} {
		if value, exists := spec[key]; exists && value != false {
			return false
		}
	}
	return validClusterSSHSourcePodSpec(nestedMap(nestedMap(spec, "template"), "spec"), nestedMap(nestedMap(wanted, "template"), "spec"))
}

func sourceConditionTrue(status map[string]any, name string) bool {
	count := 0
	for _, value := range anySlice(status["conditions"]) {
		condition, _ := value.(map[string]any)
		if condition["type"] == name && condition["status"] == "True" {
			count++
		}
	}
	return count == 1
}

func sourceCount(value any, expected int) bool {
	return (value == nil && expected == 0) || sourceJSONEqual(value, expected)
}

func validSourceConditions(status map[string]any) bool {
	seen := map[string]bool{}
	for _, value := range anySlice(status["conditions"]) {
		condition, _ := value.(map[string]any)
		name, ok := condition["type"].(string)
		state := condition["status"]
		if !ok || name == "" || seen[name] || (state != "True" && state != "False" && state != "Unknown") {
			return false
		}
		seen[name] = true
	}
	return true
}

func clusterSSHSourcePodResult(pod, manifest map[string]any, nonce, jobUID string) (clusterbootstrap.SourceNetwork, bool, error) {
	fail := func() (clusterbootstrap.SourceNetwork, bool, error) {
		return clusterbootstrap.SourceNetwork{}, false, clusterbootstrap.ErrPreparationConfig
	}
	metadata, spec, status := nestedMap(pod, "metadata"), nestedMap(pod, "spec"), nestedMap(pod, "status")
	podUID, _ := metadata["uid"].(string)
	owners := anySlice(metadata["ownerReferences"])
	if pod["apiVersion"] != "v1" || pod["kind"] != "Pod" || metadata["namespace"] != "borealis" || metadata["deletionTimestamp"] != nil ||
		!clusterbootstrap.ValidSourceReceiptIdentity(nonce, jobUID, podUID) || len(owners) != 1 ||
		nestedMap(metadata, "labels")["batch.kubernetes.io/controller-uid"] != jobUID ||
		!validClusterSSHSourcePodSpec(spec, nestedMap(nestedMap(nestedMap(manifest, "spec"), "template"), "spec")) {
		return fail()
	}
	owner, _ := owners[0].(map[string]any)
	if owner["apiVersion"] != "batch/v1" || owner["kind"] != "Job" || owner["name"] != nestedMap(manifest, "metadata")["name"] || owner["uid"] != jobUID || owner["controller"] != true {
		return fail()
	}
	phase, _ := status["phase"].(string)
	if phase != "Pending" && phase != "Running" && phase != "Succeeded" {
		return fail()
	}
	statuses := anySlice(status["containerStatuses"])
	if len(statuses) == 0 && phase == "Pending" {
		return clusterbootstrap.SourceNetwork{}, false, nil
	}
	if len(statuses) != 1 {
		return fail()
	}
	c, _ := statuses[0].(map[string]any)
	if c["name"] != "action" || c["restartCount"] == nil || !sourceCount(c["restartCount"], 0) || len(nestedMap(c, "lastState")) != 0 || len(nestedMap(c, "state")) != 1 {
		return fail()
	}
	terminated := nestedMap(nestedMap(c, "state"), "terminated")
	if len(terminated) == 0 && phase != "Succeeded" {
		return clusterbootstrap.SourceNetwork{}, false, nil
	}
	if !sourceCount(terminated["exitCode"], 0) || terminated["exitCode"] == nil || terminated["reason"] != "Completed" {
		return fail()
	}
	message, _ := terminated["message"].(string)
	network, err := clusterbootstrap.ParseSourceNetworkReceipt([]byte(message), nonce, jobUID, podUID)
	if err != nil {
		return fail()
	}
	return network, phase == "Succeeded", nil
}
