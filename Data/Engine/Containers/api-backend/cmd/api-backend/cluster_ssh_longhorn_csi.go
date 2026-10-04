package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
)

// Fixed Longhorn v1.12 CSI Deployment roles; not an operator-defined workload selector.
type clusterSSHLonghornCSIRole string

const (
	clusterSSHLonghornProvisioner clusterSSHLonghornCSIRole = "csi-provisioner"
	clusterSSHLonghornResizer     clusterSSHLonghornCSIRole = "csi-resizer"
	clusterSSHLonghornSnapshotter clusterSSHLonghornCSIRole = "csi-snapshotter"
)

func (r clusterSSHLonghornCSIRole) valid() bool {
	switch r {
	case clusterSSHLonghornProvisioner, clusterSSHLonghornResizer, clusterSSHLonghornSnapshotter:
		return true
	}
	return false
}
func (r clusterSSHLonghornCSIRole) repository() string { return "docker.io/longhornio/" + string(r) }
func (r clusterSSHLonghornCSIRole) deploymentPath() string {
	return "/apis/apps/v1/namespaces/longhorn-system/deployments/" + string(r)
}
func (r clusterSSHLonghornCSIRole) podsPath(node string) string {
	return "/api/v1/namespaces/longhorn-system/pods?labelSelector=app%3D" + string(r) + "&fieldSelector=spec.nodeName%3D" + node + "&limit=16"
}
func (r clusterSSHLonghornCSIRole) podsPathValid(path string) bool {
	prefix := strings.TrimSuffix(r.podsPath(""), "&limit=16")
	node, ok := strings.CutPrefix(path, prefix)
	if !r.valid() || !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, "&limit=16")
	return ok && clusterSSHStorageName(node)
}
func (r clusterSSHLonghornCSIRole) replicaSetPathValid(path string) bool {
	name, ok := strings.CutPrefix(path, clusterSSHLonghornAttacherReplicaSetPrefix)
	return r.valid() && ok && strings.HasPrefix(name, string(r)+"-") && clusterSSHStorageName(name)
}
func clusterSSHLonghornCSIPodsPathValid(path string) bool {
	return clusterSSHLonghornProvisioner.podsPathValid(path) || clusterSSHLonghornResizer.podsPathValid(path) || clusterSSHLonghornSnapshotter.podsPathValid(path)
}
func clusterSSHLonghornCSIPathValid(path string) bool {
	for _, r := range []clusterSSHLonghornCSIRole{clusterSSHLonghornProvisioner, clusterSSHLonghornResizer, clusterSSHLonghornSnapshotter} {
		if path == r.deploymentPath() || r.podsPathValid(path) || r.replicaSetPathValid(path) {
			return true
		}
	}
	return false
}

// Image identity only. Startup arguments/environment, socket contents and demand
// still require separate qualification; receipts detect changes, not correctness.
func (r clusterSSHLonghornCSIRole) templateImage(object map[string]any) (string, error) {
	fail := func() (string, error) { return "", clusterbootstrap.ErrPreparationConfig }
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
	containers, ok := spec["containers"].([]any)
	if !r.valid() || !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	image := clusterSSHStorageText(container, "image")
	if !ok || container["name"] != string(r) || !clusterSSHLonghornImageValid(image, string(r)) {
		return fail()
	}
	return image, nil
}

type clusterSSHLonghornCSIImages struct {
	Provisioner clusterSSHSourceExternalImage `json:"provisioner"`
	Resizer     clusterSSHSourceExternalImage `json:"resizer"`
	Snapshotter clusterSSHSourceExternalImage `json:"snapshotter"`
}

func (v clusterSSHLonghornCSIImages) valid(driver clusterSSHLonghornDriverImages) bool {
	return v.Provisioner.valid(clusterSSHLonghornProvisioner.repository()) && v.Provisioner.Configured == driver.Provisioner &&
		v.Resizer.valid(clusterSSHLonghornResizer.repository()) && v.Resizer.Configured == driver.Resizer &&
		v.Snapshotter.valid(clusterSSHLonghornSnapshotter.repository()) && v.Snapshotter.Configured == driver.Snapshotter
}
func observeClusterSSHLonghornCSIImages(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, driver clusterSSHLonghornDriverImages) (clusterSSHLonghornCSIImages, error) {
	result := clusterSSHLonghornCSIImages{}
	for _, input := range []struct {
		role       clusterSSHLonghornCSIRole
		configured string
		output     *clusterSSHSourceExternalImage
	}{
		{clusterSSHLonghornProvisioner, driver.Provisioner, &result.Provisioner},
		{clusterSSHLonghornResizer, driver.Resizer, &result.Resizer},
		{clusterSSHLonghornSnapshotter, driver.Snapshotter, &result.Snapshotter},
	} {
		role := input.role
		object, err := read(role.deploymentPath())
		owner, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "longhorn-system")
		image, imageErr := role.templateImage(object)
		if err != nil || !ok || owner.Name != string(role) || imageErr != nil || image != input.configured {
			return clusterSSHLonghornCSIImages{}, clusterbootstrap.ErrPreparationConfig
		}
		resolved, err := observeClusterSSHLonghornDeploymentRuntime(read, readList, source, owner, image, clusterSSHLonghornDeploymentWorkload{
			name: string(role), serviceAccount: "longhorn-service-account", repository: role.repository(), replicaSetPrefix: clusterSSHLonghornAttacherReplicaSetPrefix,
			podsPath: role.podsPath, replicaSetPathValid: role.replicaSetPathValid, templateImage: role.templateImage,
		})
		if err != nil {
			return clusterSSHLonghornCSIImages{}, clusterbootstrap.ErrPreparationConfig
		}
		*input.output = clusterSSHSourceExternalImage{Configured: image, Resolved: resolved}
	}
	return result, nil
}
