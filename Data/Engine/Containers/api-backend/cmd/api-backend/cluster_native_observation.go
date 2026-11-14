package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Decode before projection: encoding/json's typed structs otherwise hide
// duplicate UID/status fields. Reuse bounded Kubernetes object validation,
// then reject case aliases at every depth; policy uses exact map keys only.
type clusterNativeObservationObject map[string]any

func (o *clusterNativeObservationObject) UnmarshalJSON(raw []byte) error {
	object, _, err := clusterSSHStorageObject(raw)
	metadata, status := clusterSSHStorageMap(object, "metadata"), clusterSSHStorageMap(object, "status")
	if err != nil || !clusterNativeObservationExactKeys(object) ||
		!clusterNativeObservationFieldNames(object, "apiVersion", "kind", "metadata", "status") ||
		!clusterNativeObservationFieldNames(metadata, "name", "uid", "resourceVersion", "namespace", "deletionTimestamp") ||
		!clusterNativeObservationFieldNames(status, "conditions") {
		return errClusterConflict
	}
	for _, item := range anySlice(status["conditions"]) {
		condition, _ := item.(map[string]any)
		if !clusterNativeObservationFieldNames(condition, "type", "status") {
			return errClusterConflict
		}
	}
	*o = object
	return nil
}

// Optional fields need exact spelling even when their canonical key is absent:
// an aliased deletion marker must not look like a non-deleting object.
func clusterNativeObservationFieldNames(object map[string]any, names ...string) bool {
	for key := range object {
		for _, name := range names {
			if key != name && strings.EqualFold(key, name) {
				return false
			}
		}
	}
	return true
}

func clusterNativeObservationExactKeys(value any) bool {
	switch object := value.(type) {
	case map[string]any:
		seen := map[string]bool{}
		for key, child := range object {
			folded := strings.ToLower(key)
			if seen[folded] || !clusterNativeObservationExactKeys(child) {
				return false
			}
			seen[folded] = true
		}
	case []any:
		for _, child := range object {
			if !clusterNativeObservationExactKeys(child) {
				return false
			}
		}
	}
	return true
}

// Validate complete native bodies before shared readers project typed identity.
// Only these two fixed read paths are available; no admission capability exists.
func (c *kubernetesAPIClient) getClusterSSHNodeCohortJSON(ctx context.Context, path string, out any) error {
	if path != "/api/v1/nodes" && path != "/api/v1/namespaces/kube-system" {
		return errClusterConflict
	}
	var raw json.RawMessage
	if c.clusterSSHPrivateJSON(ctx, http.MethodGet, path, nil, &raw) != nil {
		return errClusterConflict
	}
	var exact clusterNativeObservationObject
	if json.Unmarshal(raw, &exact) != nil {
		return errClusterConflict
	}
	if path == "/api/v1/namespaces/kube-system" {
		id, valid := clusterSSHStorageMetadata(exact, "v1", "Namespace", "")
		if !valid || id.Name != "kube-system" {
			return errClusterConflict
		}
	} else {
		if !clusterNativeObservationFieldNames(exact, "items") ||
			!clusterNativeObservationFieldNames(clusterSSHStorageMap(exact, "metadata"), "continue", "remainingItemCount") {
			return errClusterConflict
		}
		items, valid := clusterSSHStorageList(exact, "Node", "", 3)
		if !valid {
			return errClusterConflict
		}
		for _, item := range items {
			encoded, err := json.Marshal(item)
			var node clusterNativeObservationObject
			if err != nil || json.Unmarshal(encoded, &node) != nil {
				return errClusterConflict
			}
			metadata, status := clusterSSHStorageMap(node, "metadata"), clusterSSHStorageMap(node, "status")
			if !validSourceConditions(status) || !clusterNativeObservationFieldNames(metadata, "labels") ||
				!clusterNativeObservationFieldNames(status, "nodeInfo", "addresses") ||
				!clusterNativeObservationFieldNames(clusterSSHStorageMap(status, "nodeInfo"), "machineID", "bootID", "kubeletVersion") {
				return errClusterConflict
			}
			for _, value := range anySlice(status["addresses"]) {
				address, _ := value.(map[string]any)
				if !clusterNativeObservationFieldNames(address, "type", "address") {
					return errClusterConflict
				}
			}
		}
	}
	return json.Unmarshal(raw, out)
}
