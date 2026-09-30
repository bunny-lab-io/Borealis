package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"strings"
)

const clusterSSHCertManagerDeploymentPrefix = "/apis/apps/v1/namespaces/cert-manager/deployments/"

var clusterSSHCertManagerDeployments = []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"}

// Desired Deployment configuration only: this does not assert running Pod
// ownership, runtime image IDs, workload fit or readiness.
type clusterSSHCertManagerImages struct {
	Controller string `json:"controller"`
	Cainjector string `json:"cainjector"`
	Webhook    string `json:"webhook"`
	Solver     string `json:"solver"`
}

func clusterSSHCertManagerImageValid(reference, role string) bool {
	repository := "quay.io/jetstack/cert-manager-" + role
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		if strings.HasPrefix(pin.Reference, repository+":") &&
			(reference == pin.Reference || reference == repository+"@"+pin.IndexDigest || reference == repository+"@"+pin.ManifestDigest) {
			return true
		}
	}
	return false
}

func (images clusterSSHCertManagerImages) valid() bool {
	return clusterSSHCertManagerImageValid(images.Controller, "controller") &&
		clusterSSHCertManagerImageValid(images.Cainjector, "cainjector") &&
		clusterSSHCertManagerImageValid(images.Webhook, "webhook") &&
		clusterSSHCertManagerImageValid(images.Solver, "acmesolver")
}

func observeClusterSSHCertManagerImages(read func(string) (map[string]any, error)) (clusterSSHCertManagerImages, error) {
	fail := func() (clusterSSHCertManagerImages, error) {
		return clusterSSHCertManagerImages{}, clusterbootstrap.ErrPreparationConfig
	}
	var images clusterSSHCertManagerImages
	for _, name := range clusterSSHCertManagerDeployments {
		object, err := read(clusterSSHCertManagerDeploymentPrefix + name)
		metadata, ok := clusterSSHStorageMetadata(object, "apps/v1", "Deployment", "cert-manager")
		if err != nil || !ok || metadata.Name != name {
			return fail()
		}
		spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
		containers, ok := spec["containers"].([]any)
		if !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
			return fail()
		}
		container, ok := containers[0].(map[string]any)
		containerName := name
		if name == "cert-manager" {
			containerName += "-controller"
		}
		if !ok || container["name"] != containerName || !clusterSSHStorageEmptyList(container["command"]) ||
			!clusterSSHStorageEmptyList(container["envFrom"]) || !clusterSSHStorageEmptyList(container["volumeMounts"]) ||
			!clusterSSHStorageEmptyList(container["volumeDevices"]) || !clusterSSHCertManagerEnvironment(container["env"]) {
			return fail()
		}
		solver, ok := clusterSSHCertManagerArguments(container["args"], name)
		if !ok {
			return fail()
		}
		switch name {
		case "cert-manager":
			images.Controller, images.Solver = clusterSSHStorageText(container, "image"), solver
		case "cert-manager-cainjector":
			images.Cainjector = clusterSSHStorageText(container, "image")
		case "cert-manager-webhook":
			images.Webhook = clusterSSHStorageText(container, "image")
		}
	}
	if !images.valid() {
		return fail()
	}
	return images, nil
}

// The reviewed manifest supplies only this downward API variable. Reject
// env/config indirection instead of guessing which image inputs it overrides.
func clusterSSHCertManagerEnvironment(raw any) bool {
	env, ok := raw.([]any)
	if !ok || len(env) != 1 {
		return false
	}
	entry, ok := env[0].(map[string]any)
	from := clusterSSHStorageMap(entry, "valueFrom")
	field := clusterSSHStorageMap(from, "fieldRef")
	return ok && len(entry) == 2 && entry["name"] == "POD_NAMESPACE" && len(from) == 1 &&
		field["fieldPath"] == "metadata.namespace" && (len(field) == 1 || len(field) == 2 && field["apiVersion"] == "v1")
}

// Accept the pinned manifest's flag surface, including Kubernetes namespace
// expansion and repeated webhook DNS names. Other flags (notably --config and
// new image flags) need explicit inventory support before they can be used.
func clusterSSHCertManagerArguments(raw any, deployment string) (string, bool) {
	args, ok := raw.([]any)
	if !ok || len(args) == 0 || len(args) > 32 {
		return "", false
	}
	seen := map[string]bool{}
	solver := ""
	for i := 0; i < len(args); i++ {
		arg, ok := args[i].(string)
		if !ok || !strings.HasPrefix(arg, "--") {
			return "", false
		}
		flag, value, equals := strings.Cut(arg, "=")
		if !equals {
			i++
			if i >= len(args) {
				return "", false
			}
			value, ok = args[i].(string)
		}
		if !ok || value == "" || len(value) > 512 || strings.HasPrefix(value, "--") || strings.ContainsAny(value, "\x00\r\n\t") {
			return "", false
		}
		allowed := flag == "--v"
		switch deployment {
		case "cert-manager":
			allowed = allowed || flag == "--cluster-resource-namespace" || flag == "--leader-election-namespace" || flag == "--max-concurrent-challenges" || flag == "--acme-http01-solver-image"
		case "cert-manager-cainjector":
			allowed = allowed || flag == "--leader-election-namespace"
		case "cert-manager-webhook":
			allowed = allowed || flag == "--secure-port" || flag == "--dynamic-serving-ca-secret-namespace" || flag == "--dynamic-serving-ca-secret-name" || flag == "--dynamic-serving-dns-names"
		}
		if !allowed || seen[flag] && flag != "--dynamic-serving-dns-names" {
			return "", false
		}
		seen[flag] = true
		if flag == "--acme-http01-solver-image" {
			if !clusterSSHCertManagerImageValid(value, "acmesolver") {
				return "", false
			}
			solver = value
		}
	}
	return solver, deployment != "cert-manager" || solver != ""
}
