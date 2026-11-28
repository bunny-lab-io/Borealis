package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (r *kubernetesClusterStepRunner) verifyEngineUpdateK3s(ctx context.Context, operation clusterControllerOperation, nodes []clusterControllerNode) error {
	expected := cleanText(operation.Payload["source_k3s_version"])
	for _, node := range nodes {
		var observed map[string]any
		if err := r.kube.getJSON(ctx, "/api/v1/nodes/"+node.Name, &observed); err != nil {
			return fmt.Errorf("observe K3s version on %s: %w", node.Name, err)
		}
		actual := cleanText(mapStringAny(mapStringAny(observed["status"])["nodeInfo"])["kubeletVersion"])
		if !clusterK3sRE.MatchString(actual) || actual != expected {
			return fmt.Errorf("observed K3s version on %s does not match pinned release baseline", node.Name)
		}
	}
	return nil
}

// Config is advanced only after the ordered K3s operation succeeds. Observed
// node versions can veto it, but a stale process environment cannot replace it.
func clusterConfiguredK3sVersion(snapshot map[string]any) (string, error) {
	configured := cleanText(mapStringAny(snapshot["config"])["k3s_version"])
	if !clusterK3sRE.MatchString(configured) {
		return "", errors.New("authoritative cluster K3s version is unavailable")
	}
	for _, raw := range mapSliceToAny(snapshot["nodes"]) {
		node := mapStringAny(raw)
		if cleanText(node["membership_state"]) != "Active" {
			continue
		}
		observed := cleanText(mapStringAny(node["roles"])["k3s_version"])
		if observed != "" && observed != configured {
			return "", fmt.Errorf("observed K3s version on %s disagrees with configured cluster baseline", cleanText(node["node_name"]))
		}
	}
	return configured, nil
}

func clusterReleaseState(snapshot map[string]any) (string, string, string, error) {
	release := cleanText(snapshot["baseline_release"])
	sha := strings.ToLower(cleanText(snapshot["baseline_sha"]))
	// A previously recorded snapshot remains identifiable if its local cache is
	// offline. Each subsequent development target still needs its own catalog.
	if !(clusterSnapshotReleaseRE.MatchString(release) && clusterControllerSHARegex.MatchString(sha)) && !validClusterBaselineRelease(release, sha) {
		return "", "", "", errors.New("authoritative cluster release baseline is unavailable")
	}
	k3s, err := clusterConfiguredK3sVersion(snapshot)
	return release, sha, k3s, err
}

func validateClusterEngineUpdateIdentity(release, sha string, payload map[string]any) error {
	immutable, _ := payload["release_immutable"].(bool)
	if clusterSnapshotReleaseRE.MatchString(release) {
		item, err := localSnapshotRelease(release, cleanText(payload["source_sha"]), cleanText(payload["source_k3s_version"]), cleanText(payload["development_version_floor"]))
		if err != nil || !immutable || cleanText(item["commit_sha"]) != sha || cleanText(payload["scope"]) != "all" || cleanText(payload["release_origin"]) != "engine_snapshot" || cleanText(payload["release_channel"]) != "development" || cleanText(payload["validation_status"]) != "pending" {
			return errors.New("development update requires the confirmed local snapshot and its exact baseline")
		}
		// Recheck compatibility as serialized through the durable operation.
		a, _ := json.Marshal(item["compatibility"])
		b, err := json.Marshal(payload["compatibility"])
		var actual clusterReleaseManifest
		if err != nil || json.Unmarshal(b, &actual) != nil {
			return errors.New("development update compatibility is invalid")
		}
		b, _ = json.Marshal(actual)
		if !bytes.Equal(a, b) {
			return errors.New("development update compatibility changed")
		}
	} else if !immutable || !validClusterBaselineRelease(release, sha) || !textInSet(clusterReleaseChannel(release), "stable", "qualification") {
		return errors.New("Engine update requires verified immutable release and pinned SHA")
	}
	source := cleanText(payload["source_k3s_version"])
	required := cleanText(clusterCompatibilityMap(payload["compatibility"])["required_k3s_baseline"])
	if !clusterK3sRE.MatchString(source) || required != source {
		return errors.New("Engine update manifest must match authoritative source K3s baseline")
	}
	return nil
}
