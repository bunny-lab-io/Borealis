package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

func clusterSSHJoinSourceJobManifest(name, image string, request clusterbootstrap.K3sJoinSourceRequest) (map[string]any, error) {
	if request.Validate(time.Now()) != nil || name != "borealis-source-"+strings.ReplaceAll(request.ID, "-", "") || !borealisOperatorImmutableImageRefPattern.MatchString(image) {
		return nil, clusterbootstrap.ErrK3sJoinMaterial
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, clusterbootstrap.ErrK3sJoinMaterial
	}
	m := clusterSSHSourceJobManifest(name, request.ID, image, request.Network.Hostname, request.Expected.Target.OperationID)
	spec := m["spec"].(map[string]any)
	spec["activeDeadlineSeconds"] = 60
	pod := spec["template"].(map[string]any)["spec"].(map[string]any)
	pod["activeDeadlineSeconds"] = 50
	c := anySlice(pod["containers"])[0].(map[string]any)
	c["command"] = []string{"/usr/local/bin/borealis-node-manager", "source-join-client"}
	c["args"] = []string{string(raw)}
	c["env"] = append(anySlice(c["env"]), map[string]any{"name": "BOREALIS_OPERATOR_SECRET", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "borealis-api-backend-runtime-env", "key": "BOREALIS_OPERATOR_SECRET"}}})
	return m, nil
}

// Original controller performs one fresh immutable Job read. A callback is
// consumed only after Kubernetes independently proves source placement, full
// immutable Job/Pod contract, successful termination and exact ciphertext hash.
// No Kubernetes identity is made available to API workers.
func (r *kubernetesClusterStepRunner) readSSHSourceK3sJoin(parent context.Context, deliveries *clusterSSHJoinSourceDeliveries,
	authority clusterSSHPreparationAuthorityRead, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding,
	network clusterbootstrap.SourceNetwork) (*clusterbootstrap.K3sJoinMaterial, error) {
	fail := clusterbootstrap.ErrK3sJoinMaterial
	if r == nil || r.kube == nil || r.namespace != "borealis" || r.controllerHolder == "" || authority == nil || deliveries == nil ||
		!borealisOperatorImmutableImageRefPattern.MatchString(r.actionImage) {
		return nil, fail
	}
	ctx, cancel := context.WithTimeout(parent, 70*time.Second)
	defer cancel()
	before, err := authority(ctx)
	if err != nil || !validClusterSSHPreparationLease(before.Lease) || before.Lease.ControllerHolder != r.controllerHolder ||
		before.IdentitySource == nil || *before.IdentitySource != source || before.Recovery != nil || validateClusterSSHInspectionCohort(before.Cohort, before.Source) != nil {
		return nil, fail
	}
	built, err := buildClusterSSHPreparationExpected(before.Cohort, before.Source, before.Lease, before.Baseline, before.K3sVersion, network.PodCIDR, network.ServiceCIDR)
	if err != nil || !reflect.DeepEqual(built, expected) || source.ClusterID != before.Source.ClusterID {
		return nil, fail
	}
	found := false
	for _, member := range before.Source.Members {
		if member.NodeID == source.SourceID && member.NodeUID == source.SourceUID && member.NodeUID == network.NodeUID && member.Name == network.Hostname &&
			member.MachineID == network.MachineID && member.BootID == network.BootID && network.ManagementLink.MatchesAddress(member.Address) {
			found = true
		}
	}
	if !found {
		return nil, fail
	}
	before.Cohort.ObservedAt = 0
	frozen, err := json.Marshal(before)
	if err != nil {
		return nil, fail
	}
	check := func(ctx context.Context) error {
		after, err := authority(ctx)
		if err != nil || ctx.Err() != nil || validateClusterSSHInspectionCohort(after.Cohort, after.Source) != nil {
			return fail
		}
		after.Cohort.ObservedAt = 0
		raw, err := json.Marshal(after)
		if err != nil || !bytes.Equal(raw, frozen) {
			return fail
		}
		return nil
	}
	request := clusterbootstrap.K3sJoinSourceRequest{Version: 1, ID: newClusterUUID(), ExpiresAt: time.Now().Unix() + 80, Expected: expected, Source: source, Network: network}
	name := "borealis-source-" + strings.ReplaceAll(request.ID, "-", "")
	manifest, err := clusterSSHJoinSourceJobManifest(name, r.actionImage, request)
	if err != nil {
		return nil, fail
	}
	closePending, err := deliveries.register(request)
	if err != nil {
		return nil, fail
	}
	defer closePending()
	var job map[string]any
	if check(ctx) != nil || r.kube.doClusterSSHSourceJSON(ctx, http.MethodPost, "/apis/batch/v1/namespaces/borealis/jobs", manifest, &job) != nil {
		return nil, fail
	}
	uid, _ := nestedMap(job, "metadata")["uid"].(string)
	if !clusterbootstrap.ValidSourceReceiptIdentity(request.ID, uid, uid) || !validClusterSSHSourceJob(job, manifest, uid) {
		return nil, fail
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
	observedPod := ""
	for {
		if !time.Now().Before(nextCheck) {
			if check(ctx) != nil {
				return nil, fail
			}
			nextCheck = time.Now().Add(5 * time.Second)
		}
		job = nil
		if r.kube.doClusterSSHSourceJSON(ctx, http.MethodGet, path, nil, &job) != nil || !validClusterSSHSourceJob(job, manifest, uid) {
			return nil, fail
		}
		status := nestedMap(job, "status")
		if !sourceCount(status["failed"], 0) || !validSourceConditions(status) || sourceConditionTrue(status, "Failed") || sourceConditionTrue(status, "FailureTarget") {
			return nil, fail
		}
		var pods struct {
			Metadata struct{ Continue string }
			Items    []map[string]any
		}
		if r.kube.doClusterSSHSourceJSON(ctx, http.MethodGet, podsPath, nil, &pods) != nil || pods.Metadata.Continue != "" || len(pods.Items) > 1 {
			return nil, fail
		}
		if len(pods.Items) == 1 {
			podUID, _ := nestedMap(pods.Items[0], "metadata")["uid"].(string)
			if observedPod != "" && observedPod != podUID {
				return nil, fail
			}
			observedPod = podUID
			message, complete, err := clusterSSHSourcePodMessage(pods.Items[0], manifest, request.ID, uid)
			if err != nil {
				return nil, fail
			}
			var receipt clusterbootstrap.K3sJoinSourceReceipt
			if message != "" {
				receipt, err = clusterbootstrap.ParseK3sJoinSourceReceipt([]byte(message), request, uid, podUID)
				if err != nil {
					return nil, fail
				}
			}
			if complete && sourceConditionTrue(status, "Complete") && sourceCount(status["succeeded"], 1) && sourceCount(status["active"], 0) {
				return deliveries.finish(ctx, request, receipt, check)
			}
		}
		select {
		case <-ctx.Done():
			return nil, fail
		case <-ticker.C:
		}
	}
}
