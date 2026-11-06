package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"
)

// A single source's native membership view, bracketed by original prepared SQL
// authority and controller Node/namespace reads. Never an admission capability.
type clusterSSHPreparedMembershipObservation struct {
	Prepared              clusterSSHPreparedObservation
	Native                clusterbootstrap.MembershipObservation
	Nonce, JobUID, PodUID string
}

type clusterSSHMembershipReceipt struct {
	Observation           clusterbootstrap.MembershipObservation
	Nonce, JobUID, PodUID string
}

// Only original controller can call this composed adapter. No runtime polling,
// target lease, preparation retry or cluster state transition is installed.
func (c *clusterController) observeClusterSSHPreparedMembership(parent context.Context, operation clusterControllerOperation) (clusterSSHPreparedMembershipObservation, error) {
	fail := func() (clusterSSHPreparedMembershipObservation, error) {
		return clusterSSHPreparedMembershipObservation{}, errClusterConflict
	}
	if c == nil || parent.Err() != nil {
		return fail()
	}
	runner, ok := c.runner.(*kubernetesClusterStepRunner)
	if !ok || runner == nil || runner.kube == nil || runner.controllerHolder != c.holder {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	before, started, err := c.loadClusterSSHPreparedAuthority(ctx, operation)
	if err != nil {
		return fail()
	}
	check := func(checkCtx context.Context) bool {
		current, _, err := c.loadClusterSSHPreparedAuthority(checkCtx, operation)
		return err == nil && checkCtx.Err() == nil && runner.controllerHolder == c.holder && reflect.DeepEqual(before, current)
	}
	nodes, targets, err := readClusterSSHPreparedMembershipNodes(ctx, runner.kube.getClusterSSHNodeCohortJSON, before)
	if err != nil {
		return fail()
	}
	selected := before.receipt.Targets[0].Prejoin.Receipt.Source
	var member clusterSSHSourceMember
	for _, source := range before.proof.Source.Members {
		if source.NodeID == selected.SourceID && source.NodeUID == selected.SourceUID {
			member = source
		}
	}
	if member.NodeID == "" {
		return fail()
	}
	sourceCtx, sourceCancel := context.WithTimeout(ctx, 30*time.Second)
	network, err := runner.readSSHSourceObservation(sourceCtx, member, operation.ID, before.version, "", false, func() bool {
		return check(sourceCtx)
	})
	sourceCancel()
	if err != nil {
		return fail()
	}
	request := clusterbootstrap.MembershipObservationRequest{Source: selected, Network: network.Network, KubeSystemUID: before.proof.Source.KubeSystemUID, Nodes: nodes}
	for _, target := range before.receipt.Targets {
		request.Prepared = append(request.Prepared, clusterbootstrap.MembershipPreparedTarget{Expected: target.Prejoin.Expected, Receipt: target.Prejoin.Receipt})
	}
	membershipCtx, membershipCancel := context.WithTimeout(ctx, 30*time.Second)
	receipt, err := runner.readSSHPreparedMembership(membershipCtx, operation.ID, request, func() bool { return check(membershipCtx) })
	membershipCancel()
	if err != nil {
		return fail()
	}
	afterNodes, afterTargets, err := readClusterSSHPreparedMembershipNodes(ctx, runner.kube.getClusterSSHNodeCohortJSON, before)
	if err != nil || !reflect.DeepEqual(nodes, afterNodes) || !reflect.DeepEqual(targets, afterTargets) {
		return fail()
	}
	after, finished, err := c.loadClusterSSHPreparedAuthority(ctx, operation)
	if err != nil || ctx.Err() != nil || finished < started || runner.controllerHolder != c.holder || !reflect.DeepEqual(before, after) || !receipt.Observation.Valid(request) {
		return fail()
	}
	sum := sha256.Sum256([]byte(before.payload))
	prepared := clusterSSHPreparedObservation{operation.ID, c.holder, hex.EncodeToString(sum[:]), before.queue.ClusterID, before.proof.Source.KubeSystemUID, operation.Attempt, finished, targets}
	return clusterSSHPreparedMembershipObservation{prepared, receipt.Observation, receipt.Nonce, receipt.JobUID, receipt.PodUID}, nil
}

// Capture the same complete strict Node body used by existing source/target
// identity proof. Additional annotations never come from an unrelated read.
func readClusterSSHPreparedMembershipNodes(ctx context.Context, getJSON func(context.Context, string, any) error, a clusterSSHPreparedAuthority) ([]clusterbootstrap.MembershipNodeExpectation, map[string]clusterbootstrap.K3sJoinedNode, error) {
	fail := func() ([]clusterbootstrap.MembershipNodeExpectation, map[string]clusterbootstrap.K3sJoinedNode, error) {
		return nil, nil, errClusterConflict
	}
	if getJSON == nil {
		return fail()
	}
	var raw json.RawMessage
	targets, err := observeClusterSSHJoinedNodes(ctx, func(ctx context.Context, path string, out any) error {
		if path != "/api/v1/nodes" {
			return getJSON(ctx, path, out)
		}
		if err := getJSON(ctx, path, &raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, out)
	}, a.proof.Cohort, a.proof.Source, a.version)
	if err != nil || len(targets) != len(a.receipt.Targets) {
		return fail()
	}
	for _, target := range a.receipt.Targets {
		if targets[target.TargetID] != target.Prejoin.Receipt.Node {
			return fail()
		}
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if json.Unmarshal(raw, &list) != nil || len(list.Items) != 3 {
		return fail()
	}
	nodes := []clusterbootstrap.MembershipNodeExpectation{}
	for _, item := range list.Items {
		var object clusterNativeObservationObject
		if json.Unmarshal(item, &object) != nil {
			return fail()
		}
		metadata, status := clusterSSHStorageMap(object, "metadata"), clusterSSHStorageMap(object, "status")
		if !clusterNativeObservationFieldNames(metadata, "annotations") {
			return fail()
		}
		annotations := clusterSSHStorageMap(metadata, "annotations")
		if !clusterNativeObservationFieldNames(annotations, "etcd.k3s.cattle.io/node-name", "etcd.k3s.cattle.io/removed-node-name") {
			return fail()
		}
		name, _ := metadata["name"].(string)
		memberName, _ := annotations["etcd.k3s.cattle.io/node-name"].(string)
		if !clusterbootstrap.ValidK3sMemberName(memberName, name) {
			return fail()
		}
		if removed, present := annotations["etcd.k3s.cattle.io/removed-node-name"]; present && removed != "" {
			return fail()
		}
		var node clusterbootstrap.K3sJoinedNode
		for _, value := range targets {
			if value.Hostname == name {
				node = value
			}
		}
		for _, source := range a.proof.Source.Members {
			if source.Name == name {
				info := clusterSSHStorageMap(status, "nodeInfo")
				if !clusterNativeObservationFieldNames(info, "kubeletVersion", "architecture", "operatingSystem") || info["kubeletVersion"] != a.version || info["architecture"] != "amd64" || info["operatingSystem"] != "linux" {
					return fail()
				}
				node = clusterbootstrap.K3sJoinedNode{NodeUID: source.NodeUID, Hostname: source.Name, MachineID: source.MachineID, BootID: source.BootID, Address: source.Address, K3sVersion: a.version, Ready: true}
			}
		}
		if node.Hostname == "" || !node.Ready {
			return fail()
		}
		for _, target := range a.receipt.Targets {
			if node.NodeUID == target.Prejoin.Receipt.Node.NodeUID && memberName != target.Prejoin.Receipt.MemberName {
				return fail()
			}
		}
		nodes = append(nodes, clusterbootstrap.MembershipNodeExpectation{Node: node, MemberName: memberName})
	}
	slices.SortFunc(nodes, func(a, b clusterbootstrap.MembershipNodeExpectation) int {
		return strings.Compare(a.Node.Hostname, b.Node.Hostname)
	})
	return nodes, targets, nil
}

// Fixed fresh Job only. Existing security, owner-chain, nonce and immutable
// manifest validators remain shared; no prior completed Job can be adopted.
func (r *kubernetesClusterStepRunner) readSSHPreparedMembership(ctx context.Context, operationID string, request clusterbootstrap.MembershipObservationRequest, check func() bool) (clusterSSHMembershipReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	fail := func() (clusterSSHMembershipReceipt, error) { return clusterSSHMembershipReceipt{}, errClusterConflict }
	payload, err := clusterbootstrap.EncodeMembershipObservationRequest(request)
	if err != nil || operationID != request.Prepared[0].Expected.Target.OperationID || r == nil || r.kube == nil || r.namespace != "borealis" || r.controllerHolder == "" || !clusterUUIDRE.MatchString(operationID) || !borealisOperatorImmutableImageRefPattern.MatchString(r.actionImage) || check == nil || ctx.Err() != nil {
		return fail()
	}
	request, err = clusterbootstrap.DecodeMembershipObservationRequest(payload)
	if err != nil {
		return fail()
	}
	nonce := newClusterUUID()
	name := "borealis-source-" + strings.ReplaceAll(nonce, "-", "")
	manifest := clusterSSHSourceJobManifest(name, nonce, r.actionImage, request.Network.Hostname, operationID)
	container := anySlice(nestedMap(nestedMap(nestedMap(manifest, "spec"), "template"), "spec")["containers"])[0].(map[string]any)
	container["command"] = []string{"/usr/local/bin/borealis-node-manager", "source-membership-client"}
	container["args"] = []string{nonce, payload}
	var job map[string]any
	if !check() || r.kube.preparedMembershipJobJSON(ctx, http.MethodPost, "/apis/batch/v1/namespaces/borealis/jobs", manifest, &job) != nil {
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
		job = nil
		if r.kube.preparedMembershipJobJSON(ctx, http.MethodGet, path, nil, &job) != nil || !validClusterSSHSourceJob(job, manifest, uid) {
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
		if r.kube.preparedMembershipJobJSON(ctx, http.MethodGet, podsPath, nil, &pods) != nil || pods.Metadata.Continue != "" || len(pods.Items) > 1 {
			return fail()
		}
		if len(pods.Items) == 1 {
			podUID, _ := nestedMap(pods.Items[0], "metadata")["uid"].(string)
			if observedPodUID != "" && observedPodUID != podUID {
				return fail()
			}
			observedPodUID = podUID
			message, complete, err := clusterSSHSourcePodMessage(pods.Items[0], manifest, nonce, uid)
			if err != nil {
				return fail()
			}
			var value clusterbootstrap.MembershipObservation
			if message != "" {
				value, err = clusterbootstrap.ParseMembershipObservationReceipt([]byte(message), request, nonce, uid, podUID)
				if err != nil {
					return fail()
				}
			}
			if complete && sourceConditionTrue(status, "Complete") && sourceCount(status["succeeded"], 1) && sourceCount(status["active"], 0) {
				if !value.Valid(request) || !check() || ctx.Err() != nil {
					return fail()
				}
				return clusterSSHMembershipReceipt{value, nonce, uid, podUID}, nil
			}
		}
		select {
		case <-ctx.Done():
			return fail()
		case <-ticker.C:
		}
	}
}

// Validate every raw Job/Pod field before typed projection can hide duplicate
// keys or aliases. Existing fixed transport retains paths, bounds and auth.
func (c *kubernetesAPIClient) preparedMembershipJobJSON(ctx context.Context, method, path string, body, out any) error {
	var raw json.RawMessage
	if c.doClusterSSHSourceJSON(ctx, method, path, body, &raw) != nil {
		return errClusterConflict
	}
	var object clusterNativeObservationObject
	if json.Unmarshal(raw, &object) != nil || !clusterNativeObservationFieldNames(clusterSSHStorageMap(object, "status"), "active", "failed", "succeeded", "conditions") || !clusterNativeObservationFieldNames(object, "items") || !clusterNativeObservationFieldNames(clusterSSHStorageMap(object, "metadata"), "continue", "remainingItemCount") {
		return errClusterConflict
	}
	objects := []map[string]any{object}
	for _, item := range anySlice(object["items"]) {
		child, ok := item.(map[string]any)
		if !ok {
			return errClusterConflict
		}
		objects = append(objects, child)
	}
	for _, value := range objects {
		// Optional fields cannot be hidden by case aliases while required canonical
		// fields remain present. Compare their spelling before shared map validators.
		metadata, status, spec := clusterSSHStorageMap(value, "metadata"), clusterSSHStorageMap(value, "status"), clusterSSHStorageMap(value, "spec")
		if !clusterNativeObservationFieldNames(value, "apiVersion", "kind", "metadata", "status", "spec") || !clusterNativeObservationFieldNames(metadata, "deletionTimestamp", "ownerReferences") || !clusterNativeObservationFieldNames(status, "active", "failed", "succeeded", "conditions", "phase", "containerStatuses") || !clusterNativeObservationFieldNames(spec, "podFailurePolicy", "successPolicy", "backoffLimitPerIndex", "maxFailedIndexes", "completionMode", "managedBy", "manualSelector", "suspend") {
			return errClusterConflict
		}
		pod := spec
		if value["kind"] == "Job" {
			pod = clusterSSHStorageMap(clusterSSHStorageMap(spec, "template"), "spec")
		}
		if !clusterNativeObservationFieldNames(pod, "hostNetwork", "hostPID", "hostIPC", "shareProcessNamespace", "initContainers", "ephemeralContainers", "runtimeClassName") {
			return errClusterConflict
		}
		for _, item := range anySlice(status["containerStatuses"]) {
			entry, ok := item.(map[string]any)
			if !ok || !clusterNativeObservationFieldNames(entry, "name", "restartCount", "lastState", "state") {
				return errClusterConflict
			}
		}
	}
	return json.Unmarshal(raw, out)
}
