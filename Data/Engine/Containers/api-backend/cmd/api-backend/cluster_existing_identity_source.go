package main

import (
	"borealis/api-backend/internal/engineidentity"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

const clusterExistingIdentitySourceRoot = "/opt/Borealis/Engine/Services/api-backend/secrets"

type clusterExistingIdentitySourceProcess struct {
	PodName, PodUID, NodeName string
}

func (p clusterExistingIdentitySourceProcess) valid(plan clusterExistingIdentityPlan) bool {
	if !plan.valid() || len(p.PodName) == 0 || len(p.PodName) > 253 || !clusterSSHStorageNameRE.MatchString(p.PodName) || !clusterUUIDRE.MatchString(p.PodUID) || p.PodUID == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for _, m := range plan.Members {
		if m.NodeID == plan.SourceID {
			return p.NodeName == m.Name
		}
	}
	return false
}

func clusterExistingIdentityLocalSource(plan clusterExistingIdentityPlan) (clusterExistingIdentitySourceProcess, error) {
	p := clusterExistingIdentitySourceProcess{PodName: os.Getenv("BOREALIS_ENGINE_POD_NAME"), PodUID: os.Getenv("BOREALIS_ENGINE_POD_UID"), NodeName: os.Getenv("BOREALIS_ENGINE_NODE_NAME")}
	if !p.valid(plan) || os.Getenv("BOREALIS_API_BACKGROUND_LOOPS") != "1" {
		return clusterExistingIdentitySourceProcess{}, errClusterConflict
	}
	return p, nil
}

type clusterExistingIdentitySourceCustody struct {
	Process            clusterExistingIdentitySourceProcess
	ContainerID, Image string
}

func (p clusterExistingIdentitySourceCustody) valid(plan clusterExistingIdentityPlan) bool {
	repo, digest, ok := strings.Cut(p.Image, "@sha256:")
	return p.Process.valid(plan) && ok && repo != "" && len(p.Image) <= 512 &&
		!strings.ContainsAny(repo, " \t\r\n") && validClusterSSHPreparationDigest(digest) &&
		strings.HasPrefix(p.ContainerID, "containerd://") && validClusterSSHPreparationDigest(strings.TrimPrefix(p.ContainerID, "containerd://"))
}

// Kubernetes reads run on original controller, not API replicas. Check actual
// Pod identity, placement, runtime and the host-backed directory consumed by
// this process. A source member UUID alone proves none of those facts.
func readClusterExistingIdentitySourceCustody(ctx context.Context, getJSON func(context.Context, string, any) error, plan clusterExistingIdentityPlan, process clusterExistingIdentitySourceProcess) (clusterExistingIdentitySourceCustody, error) {
	fail := func() (clusterExistingIdentitySourceCustody, error) {
		return clusterExistingIdentitySourceCustody{}, errClusterConflict
	}
	if getJSON == nil || !process.valid(plan) || ctx.Err() != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	snapshot := clusterExistingIdentitySnapshot{TargetID: plan.TargetID}
	for _, m := range plan.Members {
		state := "active"
		if m.NodeID == plan.TargetID {
			state = "drained"
		}
		snapshot.Members = append(snapshot.Members, clusterExistingIdentityMember{ID: m.NodeID, Name: m.Name, Address: m.Address, ApplicationState: state})
	}
	observeNodes := func() bool {
		ns, members, err := observeClusterExistingIdentityNodes(ctx, getJSON, snapshot)
		return err == nil && ns == plan.KubeSystemUID && reflect.DeepEqual(members, plan.Members)
	}
	if !observeNodes() {
		return fail()
	}
	readPod := func() (clusterExistingIdentitySourceCustody, error) {
		var pod map[string]any
		if getJSON(ctx, "/api/v1/namespaces/borealis/pods/"+process.PodName, &pod) != nil || ctx.Err() != nil {
			return fail()
		}
		return clusterExistingIdentityPodCustody(pod, plan, process)
	}
	before, err := readPod()
	if err != nil {
		return fail()
	}
	after, err := readPod()
	if err != nil || before != after || !observeNodes() || ctx.Err() != nil {
		return fail()
	}
	return before, nil
}

func clusterExistingIdentityPodCustody(pod map[string]any, plan clusterExistingIdentityPlan, process clusterExistingIdentitySourceProcess) (clusterExistingIdentitySourceCustody, error) {
	fail := func() (clusterExistingIdentitySourceCustody, error) {
		return clusterExistingIdentitySourceCustody{}, errClusterConflict
	}
	if !process.valid(plan) {
		return fail()
	}
	metadata := clusterSSHStorageMap(pod, "metadata")
	labels := clusterSSHStorageMap(metadata, "labels")
	annotations := clusterSSHStorageMap(metadata, "annotations")
	spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
	if metadata["name"] != process.PodName || metadata["uid"] != process.PodUID || metadata["namespace"] != "borealis" || metadata["deletionTimestamp"] != nil ||
		labels["app.kubernetes.io/name"] != "api-backend" || labels["borealis.io/engine-node"] != process.NodeName || labels["borealis.io/update-candidate"] != "false" || labels["borealis.io/traffic-state"] != "active" ||
		annotations["borealis.io/revision"] != plan.Baseline.SourceSHA || spec["nodeName"] != process.NodeName || status["phase"] != "Running" ||
		!clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
		return fail()
	}
	containers := anySlice(spec["containers"])
	statuses := anySlice(status["containerStatuses"])
	if len(containers) != 1 || len(statuses) != 1 {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "api-backend" {
		return fail()
	}
	image, ok := container["image"].(string)
	if !ok {
		return fail()
	}
	imageRepo, imageHash, found := strings.Cut(image, "@sha256:")
	if !found || imageRepo == "" || !validClusterSSHPreparationDigest(imageHash) || len(image) > 512 {
		return fail()
	}
	runtime, ok := statuses[0].(map[string]any)
	if !ok || runtime["name"] != "api-backend" || runtime["ready"] != true || runtime["started"] != true || runtime["imageID"] != image {
		return fail()
	}
	state := clusterSSHStorageMap(runtime, "state")
	if len(state) != 1 || len(clusterSSHStorageMap(state, "running")) == 0 {
		return fail()
	}
	containerID, ok := runtime["containerID"].(string)
	if !ok || !strings.HasPrefix(containerID, "containerd://") || !validClusterSSHPreparationDigest(strings.TrimPrefix(containerID, "containerd://")) {
		return fail()
	}
	fields := map[string]string{"BOREALIS_ENGINE_NODE_NAME": "spec.nodeName", "BOREALIS_ENGINE_POD_NAME": "metadata.name", "BOREALIS_ENGINE_POD_UID": "metadata.uid"}
	seen := map[string]bool{}
	for _, raw := range anySlice(container["env"]) {
		entry, ok := raw.(map[string]any)
		name, _ := entry["name"].(string)
		if !ok || name == "" || seen[name] {
			return fail()
		}
		seen[name] = true
		if field, ok := fields[name]; ok {
			from := clusterSSHStorageMap(entry, "valueFrom")
			ref := clusterSSHStorageMap(from, "fieldRef")
			if len(entry) != 2 || len(from) != 1 || (len(ref) != 1 && len(ref) != 2) || ref["fieldPath"] != field || (ref["apiVersion"] != nil && ref["apiVersion"] != "v1") {
				return fail()
			}
			delete(fields, name)
		}
		if name == "BOREALIS_API_BACKGROUND_LOOPS" && (len(entry) != 2 || entry["value"] != "1") {
			return fail()
		}
		if name == "BOREALIS_ENGINE_SECRET_PATH" && (len(entry) != 2 || entry["value"] != clusterExistingIdentitySourceRoot+"/engine_secret.txt") {
			return fail()
		}
	}
	if len(fields) != 0 || !seen["BOREALIS_API_BACKGROUND_LOOPS"] {
		return fail()
	}
	foundMount, foundVolume := false, false
	for _, raw := range anySlice(container["volumeMounts"]) {
		mount, ok := raw.(map[string]any)
		if !ok {
			return fail()
		}
		path, _ := mount["mountPath"].(string)
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fail()
		}
		if path == clusterExistingIdentitySourceRoot {
			if foundMount || mount["name"] != "api-secrets" || mount["subPath"] != nil || mount["subPathExpr"] != nil || mount["mountPropagation"] != nil {
				return fail()
			}
			foundMount = true
		} else if strings.HasPrefix(path, clusterExistingIdentitySourceRoot+"/") {
			return fail()
		}
	}
	for _, raw := range anySlice(spec["volumes"]) {
		volume, ok := raw.(map[string]any)
		if !ok {
			return fail()
		}
		if volume["name"] == "api-secrets" {
			host := clusterSSHStorageMap(volume, "hostPath")
			if foundVolume || len(volume) != 2 || len(host) != 2 || host["path"] != clusterExistingIdentitySourceRoot || host["type"] != "Directory" {
				return fail()
			}
			foundVolume = true
		}
	}
	if !foundMount || !foundVolume {
		return fail()
	}
	proof := clusterExistingIdentitySourceCustody{Process: process, ContainerID: containerID, Image: image}
	if !proof.valid(plan) {
		return fail()
	}
	return proof, nil
}

// This callback-only scope requires both durable claim authority and fresh
// native custody proof before/after loading and consuming fixed material.
func withClusterExistingIdentitySourceMaterial(ctx context.Context, consumer *engineidentity.Consumer, root string, plan clusterExistingIdentityPlan, process clusterExistingIdentitySourceProcess,
	authority func(context.Context) (string, error), custody func(context.Context) (clusterExistingIdentitySourceCustody, error), consume func(context.Context, *engineidentity.Material) error) error {
	if consumer == nil || authority == nil || custody == nil || consume == nil || !process.valid(plan) || ctx.Err() != nil {
		return errClusterConflict
	}
	observe := func() (string, clusterExistingIdentitySourceCustody, error) {
		before, err := authority(ctx)
		if err != nil || !validClusterSSHPreparationDigest(before) {
			return "", clusterExistingIdentitySourceCustody{}, errClusterConflict
		}
		proof, err := custody(ctx)
		if err != nil || !proof.valid(plan) || proof.Process != process {
			return "", proof, errClusterConflict
		}
		after, err := authority(ctx)
		if err != nil || before != after || ctx.Err() != nil {
			return "", proof, errClusterConflict
		}
		return before, proof, nil
	}
	bound, proof, err := observe()
	if err != nil {
		return errClusterConflict
	}
	material, err := consumer.LoadFrom(root)
	if err != nil {
		return errClusterConflict
	}
	again, current, err := observe()
	if err != nil || bound != again || proof != current {
		return errClusterConflict
	}
	if consume(ctx, material) != nil || ctx.Err() != nil {
		return errClusterConflict
	}
	again, current, err = observe()
	if err != nil || bound != again || proof != current || ctx.Err() != nil {
		return errClusterConflict
	}
	return nil
}
