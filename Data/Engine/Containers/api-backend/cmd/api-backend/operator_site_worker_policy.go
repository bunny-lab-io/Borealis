package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"unicode/utf8"
)

// These guards preserve legacy and uncertain worker custody. New launches also
// use an immutable per-site Service CREATE reservation for atomic exclusion.
type operatorSiteWorkerJSONObject map[string]any

// Effective deployment settings, not worker-supplied launch parameters. PIDs is
// retained for source/configuration parity; Kubernetes Pod resources cannot
// enforce it and this projection makes no PID-capacity claim.
type borealisOperatorSiteWorkerLimits struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
	PIDs   string `json:"pids"`
}

var operatorWorkerMilliCPU = regexp.MustCompile(`^[1-9][0-9]{0,8}m$`)
var operatorWorkerMemory = regexp.MustCompile(`^[1-9][0-9]{0,8}(Ki|Mi|Gi|Ti|T)$`)
var operatorWorkerPIDs = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)

func operatorSiteWorkerLimitsFromEnv() borealisOperatorSiteWorkerLimits {
	return borealisOperatorSiteWorkerLimits{
		CPU: os.Getenv("BOREALIS_SITE_WORKER_CPU_LIMIT"), Memory: os.Getenv("BOREALIS_SITE_WORKER_MEMORY_LIMIT"),
		PIDs: os.Getenv("BOREALIS_SITE_WORKER_PIDS_LIMIT"),
	}
}

func operatorSiteWorkerResources(name string, limits borealisOperatorSiteWorkerLimits) (borealisOperatorResourceProfile, bool) {
	profile, ok := borealisOperatorSiteWorkerResourceProfile(name)
	cpu, cpuOK := clusterSSHWorkloadMilliCPU(limits.CPU)
	memory, memoryOK := clusterSSHWorkloadMemory(limits.Memory)
	requestCPU, requestCPUOK := clusterSSHWorkloadMilliCPU(profile.RequestCPU)
	requestMemory, requestMemoryOK := clusterSSHWorkloadMemory(profile.RequestMemory)
	if !ok || !operatorWorkerMilliCPU.MatchString(limits.CPU) || !operatorWorkerMemory.MatchString(limits.Memory) || !operatorWorkerPIDs.MatchString(limits.PIDs) ||
		!cpuOK || !memoryOK || !requestCPUOK || !requestMemoryOK || requestCPU > cpu || requestMemory > memory {
		return borealisOperatorResourceProfile{}, false
	}
	profile.LimitCPU, profile.LimitMemory = limits.CPU, limits.Memory
	return profile, true
}

func decodeOperatorSiteWorkerObject(reader io.Reader) (map[string]any, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
	if err != nil || len(raw) == 0 || len(raw) > 4<<20 || !utf8.Valid(raw) {
		return nil, errors.New("invalid site-worker JSON response")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	value, err := clusterSSHPrivateJSONValue(d, 0)
	object, ok := value.(map[string]any)
	if err != nil || !ok || object == nil {
		return nil, errors.New("invalid site-worker JSON object")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("trailing site-worker JSON response")
	}
	return object, nil
}

func (o *borealisOperator) siteWorkerObjects(ctx context.Context, resource string) ([]map[string]any, error) {
	var payload operatorSiteWorkerJSONObject
	if err := o.kube.getJSON(ctx, o.kubePath("core", resource, ""), &payload); err != nil {
		return nil, err
	}
	items, ok := payload["items"].([]any)
	if !ok {
		return nil, errors.New("site-worker inventory must contain items array")
	}
	if raw, exists := payload["metadata"]; exists {
		metadata, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("invalid site-worker list metadata")
		}
		if continuation, exists := metadata["continue"]; exists && continuation != "" {
			return nil, errors.New("incomplete site-worker inventory")
		}
		if remaining, exists := metadata["remainingItemCount"]; exists && remaining != float64(0) {
			return nil, errors.New("incomplete site-worker inventory")
		}
	}
	objects := make([]map[string]any, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		object, ok := item.(map[string]any)
		metadata, metadataOK := object["metadata"].(map[string]any)
		name, nameOK := metadata["name"].(string)
		if !ok || !metadataOK || !nameOK || name == "" || seen[name] {
			return nil, errors.New("invalid or duplicate site-worker inventory object")
		}
		if raw, present := metadata["labels"]; present {
			labels, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("invalid site-worker inventory labels")
			}
			for _, value := range labels {
				if _, ok := value.(string); !ok {
					return nil, errors.New("invalid site-worker inventory label")
				}
			}
		}
		seen[name] = true
		objects = append(objects, object)
	}
	return objects, nil
}

func operatorSiteWorkerManaged(object map[string]any) bool {
	labels := nestedMap(nestedMap(object, "metadata"), "labels")
	return labels["app.kubernetes.io/component"] == "site-worker" && labels["app.kubernetes.io/managed-by"] == "borealis-operator"
}

func operatorSiteWorkerSiteID(object map[string]any) string {
	value, _ := nestedMap(nestedMap(object, "metadata"), "labels")["borealis.io/site-id"].(string)
	return value
}

func operatorSiteWorkerGUID(object map[string]any) string {
	value, _ := nestedMap(nestedMap(object, "metadata"), "labels")["borealis.io/worker-guid"].(string)
	return value
}

func (o *borealisOperator) siteWorkerLaunchPreflight(ctx context.Context, siteID int64, workerGUID string) error {
	for _, resource := range []string{"pods", "services"} {
		objects, err := o.siteWorkerObjects(ctx, resource)
		if err != nil {
			return err
		}
		for _, object := range objects {
			if !operatorSiteWorkerManaged(object) {
				continue
			}
			id := operatorSiteWorkerSiteID(object)
			parsed, err := strconv.ParseInt(id, 10, 64)
			if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != id || !borealisOperatorKubernetesNameAllowed(operatorSiteWorkerGUID(object)) {
				return errors.New("ambiguous existing site-worker identity")
			}
			if operatorSiteWorkerGUID(object) == workerGUID || id == strconv.FormatInt(siteID, 10) {
				return fmt.Errorf("site %d retains site-worker %s; explicit retirement required", siteID, resource)
			}
		}
	}
	return nil
}

func (o *borealisOperator) siteWorkerRetirementInventory(ctx context.Context, workerGUID string) ([]map[string]any, []map[string]any, error) {
	pods, err := o.siteWorkerObjects(ctx, "pods")
	if err != nil {
		return nil, nil, err
	}
	services, err := o.siteWorkerObjects(ctx, "services")
	if err != nil {
		return nil, nil, err
	}
	selected := [2][]map[string]any{}
	siteID := ""
	for kind, objects := range [][]map[string]any{pods, services} {
		for _, object := range objects {
			if !operatorSiteWorkerManaged(object) || operatorSiteWorkerGUID(object) != workerGUID {
				continue
			}
			metadata := nestedMap(object, "metadata")
			uid, uidOK := metadata["uid"].(string)
			id := operatorSiteWorkerSiteID(object)
			parsed, parseErr := strconv.ParseInt(id, 10, 64)
			_, candidate := nestedMap(metadata, "labels")["borealis.io/redeploy-agent-candidate"]
			if !uidOK || uid == "" || !borealisOperatorKubernetesObjectNameAllowed(cleanText(metadata["name"])) || metadata["namespace"] != o.namespace || parseErr != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != id || (siteID != "" && id != siteID) || candidate {
				return nil, nil, errors.New("ambiguous site-worker retirement identity or replacement candidate")
			}
			siteID = id
			selected[kind] = append(selected[kind], object)
		}
		if len(selected[kind]) > 1 {
			return nil, nil, errors.New("duplicate site-worker retirement inventory")
		}
	}
	if siteID != "" {
		for _, objects := range [][]map[string]any{pods, services} {
			for _, object := range objects {
				if operatorSiteWorkerManaged(object) && operatorSiteWorkerSiteID(object) == siteID && operatorSiteWorkerGUID(object) != workerGUID {
					return nil, nil, errors.New("multiple worker identities occupy site")
				}
			}
		}
	}
	return selected[0], selected[1], nil
}

func (o *borealisOperator) siteWorkerRetirementAbsent(ctx context.Context, workerGUID string, oldPods, services []map[string]any) error {
	pods, err := o.siteWorkerObjects(ctx, "pods")
	if err != nil {
		return err
	}
	names, sites := map[string]bool{}, map[string]bool{}
	for _, objects := range [][]map[string]any{oldPods, services} {
		for _, object := range objects {
			sites[operatorSiteWorkerSiteID(object)] = true
		}
	}
	for _, pod := range oldPods {
		names[cleanText(nestedMap(pod, "metadata")["name"])] = true
	}
	for _, pod := range pods {
		if names[cleanText(nestedMap(pod, "metadata")["name"])] || (operatorSiteWorkerManaged(pod) && (operatorSiteWorkerGUID(pod) == workerGUID || sites[operatorSiteWorkerSiteID(pod)])) {
			return errors.New("site-worker Pod absence is not confirmed")
		}
	}
	return nil
}
