package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"reflect"
	"strings"
)

const clusterSSHCertManagerPodsPrefix = "/api/v1/namespaces/cert-manager/pods?labelSelector=app.kubernetes.io%2Fname%3D"
const clusterSSHCertManagerReplicaSetPrefix = "/apis/apps/v1/namespaces/cert-manager/replicasets/"

// These functions accept only the three fixed deployment names.
func clusterSSHCertManagerRole(name string) (role, label string) {
	switch name {
	case "cert-manager":
		return "controller", "cert-manager"
	case "cert-manager-cainjector":
		return "cainjector", "cainjector"
	case "cert-manager-webhook":
		return "webhook", "webhook"
	}
	return "", ""
}
func clusterSSHCertManagerPodsPath(name, node string) string {
	_, label := clusterSSHCertManagerRole(name)
	if label == "" {
		return ""
	}
	return clusterSSHCertManagerPodsPrefix + label + "&fieldSelector=spec.nodeName%3D" + node + "&limit=16"
}
func clusterSSHCertManagerPodsPathValid(path string) bool {
	for _, name := range clusterSSHCertManagerDeployments {
		prefix := strings.TrimSuffix(clusterSSHCertManagerPodsPath(name, ""), "&limit=16")
		node, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		node, ok = strings.CutSuffix(node, "&limit=16")
		if ok && clusterSSHStorageName(node) {
			return true
		}
	}
	return false
}
func clusterSSHCertManagerReplicaSetPathValid(path string) bool {
	name, ok := strings.CutPrefix(path, clusterSSHCertManagerReplicaSetPrefix)
	return ok && strings.HasPrefix(name, "cert-manager-") && clusterSSHStorageName(name)
}
func clusterSSHCertManagerTemplateImages(object map[string]any, name string) (string, string, error) {
	template := clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template")
	_, label := clusterSSHCertManagerRole(name)
	if label == "" || clusterSSHStorageMap(clusterSSHStorageMap(template, "metadata"), "labels")["app.kubernetes.io/name"] != label {
		return "", "", clusterbootstrap.ErrPreparationConfig
	}
	return clusterSSHCertManagerSpecImages(clusterSSHStorageMap(template, "spec"), name, false)
}
func clusterSSHCertManagerSpecImages(spec map[string]any, name string, pod bool) (string, string, error) {
	fail := func() (string, string, error) { return "", "", clusterbootstrap.ErrPreparationConfig }
	role, _ := clusterSSHCertManagerRole(name)
	if role == "" || spec["serviceAccountName"] != name || spec["hostNetwork"] != nil && spec["hostNetwork"] != false {
		return fail()
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
		return fail()
	}
	c, ok := containers[0].(map[string]any)
	image := clusterSSHStorageText(c, "image")
	if !ok || c["name"] != "cert-manager-"+role || !clusterSSHCertManagerImageValid(image, role) ||
		!clusterSSHStorageEmptyList(c["command"]) || !clusterSSHStorageEmptyList(c["envFrom"]) ||
		!clusterSSHStorageEmptyList(c["volumeDevices"]) || !clusterSSHCertManagerEnvironment(c["env"]) ||
		!clusterSSHStorageEmptyText(c["workingDir"]) || !clusterSSHStorageEmptyText(c["restartPolicy"]) || !clusterSSHStorageEmptyMap(c["lifecycle"]) {
		return fail()
	}
	mounts, volumes, ok := clusterSSHServiceAccountMounts(c["volumeMounts"], spec["volumes"], 0, pod)
	if !ok || len(mounts) != 0 || len(volumes) != 0 {
		return fail()
	}
	solver, ok := clusterSSHCertManagerArguments(c["args"], name)
	if !ok {
		return fail()
	}
	return image, solver, nil
}
func observeClusterSSHCertManagerRuntime(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, images clusterSSHCertManagerImages) (clusterSSHCertManagerImages, error) {
	fail := func() (clusterSSHCertManagerImages, error) {
		return clusterSSHCertManagerImages{}, clusterbootstrap.ErrPreparationConfig
	}
	if !images.configuredValid() {
		return fail()
	}
	for _, name := range clusterSSHCertManagerDeployments {
		role, label := clusterSSHCertManagerRole(name)
		object, err := read(clusterSSHCertManagerDeploymentPrefix + name)
		id, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "cert-manager")
		image, solver, configErr := clusterSSHCertManagerTemplateImages(object, name)
		if err != nil || !ok || id.Name != name || configErr != nil || name == "cert-manager" && solver != images.Solver {
			return fail()
		}
		configured := images.Controller
		if name == "cert-manager-cainjector" {
			configured = images.Cainjector
		}
		if name == "cert-manager-webhook" {
			configured = images.Webhook
		}
		if image != configured {
			return fail()
		}
		spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
		args := spec["containers"].([]any)[0].(map[string]any)["args"]
		specValid := func(spec map[string]any, pod bool) bool {
			gotImage, gotSolver, err := clusterSSHCertManagerSpecImages(spec, name, pod)
			if err != nil || gotImage != image || gotSolver != solver {
				return false
			}
			// Kubernetes preserves declared argument text in admitted Pod specs. A
			// still-reviewed but different startup configuration requires new evidence.
			return reflect.DeepEqual(spec["containers"].([]any)[0].(map[string]any)["args"], args)
		}
		resolved, err := observeClusterSSHLonghornDeploymentRuntime(read, readList, source, id, image, clusterSSHLonghornDeploymentWorkload{
			namespace: "cert-manager", labelKey: "app.kubernetes.io/name", labelValue: label, name: name, containerName: "cert-manager-" + role, serviceAccount: name, repository: "quay.io/jetstack/cert-manager-" + role,
			podsPath:         func(node string) string { return clusterSSHCertManagerPodsPath(name, node) },
			replicaSetPrefix: clusterSSHCertManagerReplicaSetPrefix,
			replicaSetPathValid: func(path string) bool {
				return clusterSSHCertManagerReplicaSetPathValid(path) && strings.HasPrefix(path, clusterSSHCertManagerReplicaSetPrefix+name+"-")
			},
			templateImage: func(object map[string]any) (string, error) {
				image, _, err := clusterSSHCertManagerTemplateImages(object, name)
				return image, err
			},
			specValid: specValid,
		})
		if err != nil {
			return fail()
		}
		switch name {
		case "cert-manager":
			images.ControllerResolved = resolved
		case "cert-manager-cainjector":
			images.CainjectorResolved = resolved
		case "cert-manager-webhook":
			images.WebhookResolved = resolved
		}
	}
	if !images.valid() {
		return fail()
	}
	return images, nil
}
