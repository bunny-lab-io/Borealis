package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

var clusterEtcdMemberNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

type clusterRemovalFence struct {
	OperationID    string `json:"operation_id"`
	NodeID         string `json:"node_id"`
	NodeName       string `json:"node_name"`
	NodeUID        string `json:"node_uid"`
	EtcdMemberName string `json:"etcd_member_name"`
	ActionAttempt  int64  `json:"action_attempt"`
	ActionImage    string `json:"action_image"`
	AcknowledgedAt int64  `json:"acknowledged_at"`
}

func plannedClusterRemoval(operation clusterControllerOperation) bool {
	emergency, _ := operation.Payload["emergency"].(bool)
	return operation.Kind == "node_remove" && !emergency
}

func removalFenceRequired(nodeName string) error {
	return fmt.Errorf("node %s lacks matching durable removal fence acknowledgement; restore the same target and retry, or externally power it off and use emergency removal with TARGET IS POWERED OFF and EMERGENCY REMOVE NODE", nodeName)
}

func removalFenceRecord(operation clusterControllerOperation, node clusterControllerNode) (clusterRemovalFence, bool, error) {
	raw, exists := mapStringAny(operation.Payload["removal_fences"])[node.ID]
	if !exists {
		return clusterRemovalFence{}, false, nil
	}
	var fence clusterRemovalFence
	encoded, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(encoded, &fence) != nil {
		return fence, true, errors.New("invalid durable removal fence record")
	}
	if !plannedClusterRemoval(operation) || !clusterUUIDRE.MatchString(fence.OperationID) ||
		fence.OperationID != operation.ID || fence.NodeID != node.ID || !clusterUUIDRE.MatchString(fence.NodeID) ||
		fence.NodeName != node.Name || !clusterControllerNodeRegex.MatchString(fence.NodeName) ||
		!clusterUUIDRE.MatchString(fence.NodeUID) || !clusterEtcdMemberNameRE.MatchString(fence.EtcdMemberName) ||
		fence.ActionAttempt < 1 || fence.ActionAttempt > operation.Attempt || fence.AcknowledgedAt < 0 ||
		fence.ActionImage != clusterOperationActionImage(operation, "") || !borealisOperatorImmutableImageRefPattern.MatchString(fence.ActionImage) {
		return fence, true, errors.New("durable removal fence operation or target identity mismatch")
	}
	return fence, true, nil
}

func setRemovalFenceRecord(operation clusterControllerOperation, fence clusterRemovalFence) {
	records := mapStringAny(operation.Payload["removal_fences"])
	records[fence.NodeID] = fence
	operation.Payload["removal_fences"] = records
}

func validateRemovalNodeIdentity(observed map[string]any, fence clusterRemovalFence) error {
	metadata := nestedMap(observed, "metadata")
	annotations := clusterStringMap(metadata["annotations"])
	member, removed := annotations[clusterK3sEtcdNodeNameAnnotation], annotations[clusterK3sEtcdRemovedNameAnnotation]
	if cleanText(metadata["name"]) != fence.NodeName || cleanText(metadata["uid"]) != fence.NodeUID ||
		(member != "" && member != fence.EtcdMemberName) || (removed != "" && removed != fence.EtcdMemberName) ||
		(member == "" && removed == "") {
		return errors.New("Kubernetes UID or etcd member identity differs from durable removal fence")
	}
	return nil
}

func removalNodeReady(observed map[string]any) bool {
	for _, raw := range anySlice(nestedMap(observed, "status")["conditions"]) {
		condition := mapStringAny(raw)
		if cleanText(condition["type"]) == "Ready" {
			return cleanText(condition["status"]) == "True"
		}
	}
	return false
}

func removalFenceAction(operation clusterControllerOperation, fence clusterRemovalFence) (clusterControllerOperation, clusterControllerStep) {
	operation.Attempt = fence.ActionAttempt
	return operation, clusterControllerStep{Name: "node:" + fence.NodeID + ":prepare_member_removal", NodeID: fence.NodeID}
}

func removalFenceArgs(fence clusterRemovalFence) []string {
	return []string{"--operation-id", fence.OperationID, "--node-id", fence.NodeID, "--node-name", fence.NodeName, "--node-uid", fence.NodeUID, "--etcd-member-name", fence.EtcdMemberName}
}

func (r *kubernetesClusterStepRunner) persistFence(ctx context.Context, operation clusterControllerOperation, fence clusterRemovalFence) error {
	if r.persistRemovalFence == nil {
		return errors.New("durable removal fence store is unavailable")
	}
	if err := r.persistRemovalFence(ctx, operation, fence); err != nil {
		return err
	}
	setRemovalFenceRecord(operation, fence)
	return nil
}

// A completed, identity-checked action can recover an acknowledgement lost before
// its database commit. The durable intent predates the Job and pins its arguments.
func (r *kubernetesClusterStepRunner) completedFenceAction(ctx context.Context, operation clusterControllerOperation, fence clusterRemovalFence) (bool, error) {
	action, step := removalFenceAction(operation, fence)
	jobName := clusterActionJobName(action.ID, fmt.Sprintf("attempt:%d:%s", action.Attempt, step.Name))
	args := append(clusterNodeActionArgs(action, "PrepareMemberRemoval"), removalFenceArgs(fence)...)
	expected := clusterActionJobManifest(jobName, r.namespace, fence.NodeName, fence.ActionImage, args, operation.ID, step.Name)
	var job map[string]any
	if err := r.kube.getJSON(ctx, fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", r.namespace, jobName), &job); err != nil {
		if kubernetesAPIErrorHasStatus(err, http.StatusNotFound) {
			return false, nil
		}
		return false, err
	}
	if err := validateClusterActionJobIdentity(job, expected); err != nil {
		return false, fmt.Errorf("removal fence acknowledgement Job mismatch: %w", err)
	}
	return coerceInt64(nestedMap(job, "status")["succeeded"]) == 1, nil
}

func (r *kubernetesClusterStepRunner) removalFenceStatus(ctx context.Context, operation clusterControllerOperation, node clusterControllerNode) (clusterRemovalFence, map[string]any, error) {
	fence, exists, err := removalFenceRecord(operation, node)
	if err != nil {
		return fence, nil, err
	}
	if !clusterControllerNodeRegex.MatchString(node.Name) {
		return fence, nil, errors.New("invalid removal target name")
	}
	var observed map[string]any
	if err := r.kube.getJSON(ctx, "/api/v1/nodes/"+node.Name, &observed); err != nil {
		if !kubernetesAPIErrorHasStatus(err, http.StatusNotFound) {
			return fence, nil, err
		}
		if !exists || fence.AcknowledgedAt == 0 {
			return fence, nil, removalFenceRequired(node.Name)
		}
		return fence, nil, nil
	}
	if exists {
		if err := validateRemovalNodeIdentity(observed, fence); err != nil {
			return fence, observed, err
		}
		if fence.AcknowledgedAt == 0 {
			completed, err := r.completedFenceAction(ctx, operation, fence)
			if err != nil {
				return fence, observed, err
			}
			if completed {
				fence.AcknowledgedAt = time.Now().UTC().Unix()
				if err := r.persistFence(ctx, operation, fence); err != nil {
					return fence, observed, err
				}
			}
		}
	}
	if fence.AcknowledgedAt == 0 && !removalNodeReady(observed) {
		return fence, observed, removalFenceRequired(node.Name)
	}
	return fence, observed, nil
}

func (r *kubernetesClusterStepRunner) prepareRemovalFence(ctx context.Context, operation clusterControllerOperation, node clusterControllerNode) error {
	fence, observed, err := r.removalFenceStatus(ctx, operation, node)
	if err != nil || fence.AcknowledgedAt > 0 {
		return err
	}
	if operation.Payload == nil {
		return errors.New("removal operation payload is unavailable")
	}
	if fence.NodeUID == "" {
		fence = clusterRemovalFence{
			OperationID: operation.ID, NodeID: node.ID, NodeName: node.Name,
			NodeUID:        cleanText(nestedMap(observed, "metadata")["uid"]),
			EtcdMemberName: clusterStringMap(nestedMap(observed, "metadata")["annotations"])[clusterK3sEtcdNodeNameAnnotation],
			ActionImage:    clusterOperationActionImage(operation, r.actionImage),
		}
	}
	fence.ActionAttempt = operation.Attempt
	if err := r.persistFence(ctx, operation, fence); err != nil {
		return err
	}
	action, step := removalFenceAction(operation, fence)
	if err := r.nodeActionJob(ctx, action, step, node, "PrepareMemberRemoval"); err != nil {
		return err
	}
	// Recheck the Node and exact successful Job before committing acknowledgement.
	fence, _, err = r.removalFenceStatus(ctx, operation, node)
	if err != nil {
		return err
	}
	if fence.AcknowledgedAt == 0 {
		return removalFenceRequired(node.Name)
	}
	return nil
}

func (c *clusterController) persistRemovalFence(ctx context.Context, operation clusterControllerOperation, fence clusterRemovalFence) error {
	// Validate/encode the narrow payload before acquiring a database connection.
	candidate := operation
	candidate.Payload = map[string]any{"action_image": fence.ActionImage, "removal_fences": map[string]any{fence.NodeID: fence}}
	node := clusterControllerNode{ID: fence.NodeID, Name: fence.NodeName}
	if _, _, err := removalFenceRecord(candidate, node); err != nil {
		return err
	}
	encoded, err := json.Marshal(fence)
	if err != nil {
		return err
	}
	conn, err := c.store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := c.requireLeaseOwnership(ctx, tx); err != nil {
		return err
	}
	var payloadJSON string
	err = tx.QueryRowContext(ctx, `
		SELECT o.payload_json FROM engine.cluster_operations o
		JOIN engine.cluster_state s ON s.active_operation_id=o.id
		JOIN engine.cluster_nodes n ON n.id=$4 AND n.node_name=$5 AND n.membership_state='Active'
		WHERE o.id=$1 AND o.kind='node_remove' AND o.state='running' AND o.attempt=$2 AND o.current_step=$3 AND o.target_node_id=$6
		FOR UPDATE OF o
	`, operation.ID, operation.Attempt, operation.CurrentStep, node.ID, node.Name, operation.TargetNodeID).Scan(&payloadJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("removal fence operation is no longer current")
	}
	if err != nil {
		return err
	}
	durable := operation
	durable.Payload = parseClusterJSON(payloadJSON)
	if !plannedClusterRemoval(durable) || clusterOperationActionImage(durable, "") != fence.ActionImage {
		return errors.New("removal fence operation contract changed")
	}
	target := false
	for _, id := range clusterRemovalNodeIDs(durable.TargetNodeID, durable.Payload) {
		target = target || id == node.ID
	}
	if !target {
		return errors.New("removal fence target is outside operation")
	}
	previous, exists, err := removalFenceRecord(durable, node)
	if err != nil {
		return err
	}
	if !exists && (fence.AcknowledgedAt != 0 || fence.ActionAttempt != operation.Attempt) {
		return errors.New("removal fence acknowledgement requires prior durable intent")
	}
	if exists {
		if previous == fence {
			return tx.Commit()
		}
		comparison := fence
		comparison.AcknowledgedAt, comparison.ActionAttempt = previous.AcknowledgedAt, previous.ActionAttempt
		if comparison != previous || previous.AcknowledgedAt != 0 ||
			(fence.AcknowledgedAt != 0 && fence.ActionAttempt != previous.ActionAttempt) ||
			(fence.AcknowledgedAt == 0 && fence.ActionAttempt != operation.Attempt) {
			return errors.New("removal fence identity or acknowledged action cannot change")
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engine.cluster_operations
		SET payload_json=jsonb_set(payload_json::jsonb, '{removal_fences}',
		COALESCE(payload_json::jsonb->'removal_fences','{}'::jsonb) || jsonb_build_object($2::text,$3::jsonb),true)::text,
		updated_at=$4 WHERE id=$1`, operation.ID, node.ID, string(encoded), c.now().UTC().Unix()); err != nil {
		return err
	}
	event := "member_fence_intent"
	if fence.AcknowledgedAt > 0 {
		event = "member_fence_acknowledged"
	}
	if err := insertClusterEvent(ctx, tx, operation.ID, "", "", event, "running", "Member removal fence evidence recorded.", map[string]any{"fence": fence}, c.now().UTC().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
