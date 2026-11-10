package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"errors"
	"io"
	"strings"
)

var errClusterSSHPreparationLonghornNodeAbsent = errors.New("planned target Longhorn Node absent")

func clusterSSHPreparationLonghornNodePath(path string) bool {
	name, ok := strings.CutPrefix(path, clusterSSHInstanceNodePrefix)
	return ok && clusterSSHStorageName(name)
}

// Only the existing authenticated named GET's exact Kubernetes NotFound Status
// proves absence. A missing object, denied request or proxy 404 never means zero.
func clusterSSHPreparationLonghornNotFound(path string, body io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(body, 4097))
	if err != nil || len(raw) > 4096 || !clusterSSHPreparationLonghornNodePath(path) {
		return clusterbootstrap.ErrPreparationConfig
	}
	value, _, err := clusterSSHStorageObject(raw)
	details := clusterSSHStorageMap(value, "details")
	code, valid := clusterSSHStorageInteger(value["code"])
	if err != nil || !valid || code != 404 || value["apiVersion"] != "v1" || value["kind"] != "Status" || value["status"] != "Failure" || value["reason"] != "NotFound" ||
		details["name"] != strings.TrimPrefix(path, clusterSSHInstanceNodePrefix) || details["kind"] != "nodes" || details["group"] != "longhorn.io" {
		return clusterbootstrap.ErrPreparationConfig
	}
	return errClusterSSHPreparationLonghornNodeAbsent
}

type clusterSSHPreparationTargetInstanceCPU struct {
	Present                     bool
	OverrideMilli, RequestMilli uint64
	ObservationSHA256           string
}

// The caller brackets this read with existing proposal/source authority and
// freezes complete observations across rereads. This calculation alone is not
// capacity fit or permission. The target's own override is never inferred from
// the source member, and the observed policy cap is enforced before summation.
func observeClusterSSHPreparationTargetInstanceCPU(read func(string) (map[string]any, error), hostname string,
	cpu clusterbootstrap.PreparationTargetCPU, policy clusterSSHInstanceManagerResources) (clusterSSHPreparationTargetInstanceCPU, error) {
	fail := func() (clusterSSHPreparationTargetInstanceCPU, error) {
		return clusterSSHPreparationTargetInstanceCPU{}, clusterbootstrap.ErrPreparationConfig
	}
	percentage, valid := clusterSSHInstancePercentage(policy.CPUPercentage)
	if read == nil || !clusterSSHStorageName(hostname) || cpu.Validate(clusterbootstrap.K3sPins().Version) != nil || !valid || policy.CPULimitMilli == 0 || policy.CPULimitMilli > clusterSSHCapacityLimit {
		return fail()
	}
	path := clusterSSHInstanceNodePrefix + hostname
	value, err := read(path)
	result := clusterSSHPreparationTargetInstanceCPU{}
	if errors.Is(err, errClusterSSHPreparationLonghornNodeAbsent) {
		if value != nil {
			return fail()
		}
		result.ObservationSHA256, err = clusterSSHPreparationDigest(struct{ Domain, Path string }{"longhorn-target-node-absent-v1", path})
	} else {
		id, ok := clusterSSHStorageMetadata(value, "longhorn.io/v1beta2", "Node", "longhorn-system")
		override, number := clusterSSHStorageInteger(clusterSSHStorageMap(value, "spec")["instanceManagerCPURequest"])
		if err != nil || !ok || id.Name != hostname || !number || uint64(override) > clusterSSHCapacityLimit {
			return fail()
		}
		result.Present, result.OverrideMilli = true, uint64(override)
		result.ObservationSHA256, err = clusterSSHPreparationDigest(value)
	}
	request, ok := clusterSSHInstanceCPURequest(cpu.AllocatableMilli, result.OverrideMilli, percentage)
	if err != nil || !ok || request > policy.CPULimitMilli {
		return fail()
	}
	result.RequestMilli = request
	return result, nil
}
