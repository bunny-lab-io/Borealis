package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const identityConsumerObservationLimit = 4 << 20

var errIdentityConsumerObservation = errors.New("identity consumer observation failed; require exact drained Node UID and complete Pod inventory")
var errIdentityConsumersPresent = errors.New("identity consumer Pods remain; stop active, candidate and worker consumers before identity recovery")

type identityConsumerTarget struct {
	NodeName string `json:"node_name"`
	NodeUID  string `json:"node_uid"`
}

func parseIdentityConsumerTarget(params map[string]any, localName string) (identityConsumerTarget, error) {
	name, nameOK := params["node_name"].(string)
	uid, uidOK := params["node_uid"].(string)
	if len(params) != 2 || !nameOK || !uidOK || name != localName || !nodePattern.MatchString(name) || !clusterUUIDPattern.MatchString(uid) {
		return identityConsumerTarget{}, errIdentityConsumerObservation
	}
	return identityConsumerTarget{name, uid}, nil
}

// This is an observation, NOT a host lock, runtime-process fence, controller
// lease or permission to install keys. Ordinary drain intentionally permits
// isolated candidates; identity recovery must not mistake it for quiescence.
func (m *manager) inspectIdentityConsumers(ctx context.Context, params map[string]any) (map[string]any, error) {
	target, err := parseIdentityConsumerTarget(params, m.nodeName)
	if err != nil {
		return nil, err
	}
	return observeIdentityConsumers(ctx, target, identityConsumerCommand)
}

func observeIdentityConsumers(parent context.Context, target identityConsumerTarget, read func(context.Context, ...string) ([]byte, error)) (map[string]any, error) {
	if _, err := parseIdentityConsumerTarget(map[string]any{"node_name": target.NodeName, "node_uid": target.NodeUID}, target.NodeName); err != nil || read == nil {
		return nil, errIdentityConsumerObservation
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	checkNode := func() error {
		if ctx.Err() != nil {
			return errIdentityConsumerObservation
		}
		raw, err := read(ctx, "kubectl", "--request-timeout=5s", "get", "node", target.NodeName, "-o", "json")
		var node struct {
			Metadata struct {
				Name              string            `json:"name"`
				UID               string            `json:"uid"`
				DeletionTimestamp json.RawMessage   `json:"deletionTimestamp"`
				Labels            map[string]string `json:"labels"`
			} `json:"metadata"`
		}
		if err != nil || ctx.Err() != nil || len(raw) > identityConsumerObservationLimit || json.Unmarshal(raw, &node) != nil ||
			node.Metadata.Name != target.NodeName || node.Metadata.UID != target.NodeUID ||
			(len(node.Metadata.DeletionTimestamp) > 0 && string(node.Metadata.DeletionTimestamp) != "null") ||
			node.Metadata.Labels["borealis.io/engine-node"] != "true" || node.Metadata.Labels["borealis.io/application-state"] != "drained" {
			return errIdentityConsumerObservation
		}
		return nil
	}
	if err := checkNode(); err != nil {
		return nil, err
	}
	// Do not select by app labels: an unlabelled helper can mount the same keys.
	raw, err := read(ctx, "kubectl", "--request-timeout=5s", "get", "pods", "--all-namespaces", "--field-selector", "spec.nodeName="+target.NodeName, "--chunk-size=0", "-o", "json")
	if err != nil || ctx.Err() != nil || len(raw) > identityConsumerObservationLimit {
		return nil, errIdentityConsumerObservation
	}
	var pods struct {
		Metadata struct {
			Continue string `json:"continue"`
		} `json:"metadata"`
		Items *[]struct {
			Metadata struct {
				Name      string            `json:"name"`
				Namespace string            `json:"namespace"`
				UID       string            `json:"uid"`
				Labels    map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				NodeName string `json:"nodeName"`
				Volumes  []struct {
					HostPath *struct {
						Path string `json:"path"`
					} `json:"hostPath"`
				} `json:"volumes"`
			} `json:"spec"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &pods) != nil || pods.Items == nil || pods.Metadata.Continue != "" {
		return nil, errIdentityConsumerObservation
	}
	seen := map[string]bool{}
	for _, pod := range *pods.Items {
		if pod.Spec.NodeName != target.NodeName || pod.Metadata.Name == "" || pod.Metadata.Namespace == "" || pod.Metadata.UID == "" || seen[pod.Metadata.UID] {
			return nil, errIdentityConsumerObservation
		}
		seen[pod.Metadata.UID] = true
		role := strings.TrimSuffix(pod.Metadata.Labels["app.kubernetes.io/name"], "-candidate")
		if role == "api-backend" || role == "job-scheduler" || role == "site-worker" {
			// Readiness, phase and deletionTimestamp never establish process exit.
			return nil, errIdentityConsumersPresent
		}
		for _, volume := range pod.Spec.Volumes {
			if volume.HostPath == nil {
				continue
			}
			path := volume.HostPath.Path
			if !filepath.IsAbs(path) || filepath.Clean(path) != path {
				return nil, errIdentityConsumerObservation
			}
			const keys = "/opt/Borealis/Engine/Services/api-backend/secrets"
			if path == "/" || path == keys || strings.HasPrefix(path, keys+"/") || strings.HasPrefix(keys, path+"/") {
				return nil, errIdentityConsumersPresent
			}
		}
	}
	if err := checkNode(); err != nil {
		return nil, err
	}
	return map[string]any{"node_name": target.NodeName, "node_uid": target.NodeUID, "pod_consumers_absent": true}, nil
}

type identityConsumerOutput struct{ buffer bytes.Buffer }

func (out *identityConsumerOutput) Write(raw []byte) (int, error) {
	if len(raw) > identityConsumerObservationLimit-out.buffer.Len() {
		return 0, errIdentityConsumerObservation
	}
	return out.buffer.Write(raw)
}

func identityConsumerCommand(parent context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/local/bin/k3s", args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.WaitDelay = 250 * time.Millisecond
	output := &identityConsumerOutput{}
	command.Stdout, command.Stderr = output, io.Discard
	if command.Run() != nil || ctx.Err() != nil {
		return nil, errIdentityConsumerObservation
	}
	return output.buffer.Bytes(), nil
}

func validateIdentityConsumerAcknowledgement(raw []byte, target identityConsumerTarget) error {
	var response struct {
		OK     bool   `json:"ok"`
		Verb   string `json:"verb"`
		Result struct {
			identityConsumerTarget
			Absent bool `json:"pod_consumers_absent"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &response) != nil || !response.OK || response.Verb != "InspectIdentityConsumers" || response.Result.identityConsumerTarget != target || !response.Result.Absent {
		return errIdentityConsumerObservation
	}
	return nil
}
