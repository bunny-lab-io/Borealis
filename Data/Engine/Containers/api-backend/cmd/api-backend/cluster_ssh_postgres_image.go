package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"regexp"
	"strings"
)

// Only the supported CNPG runtime repository may become a future registry
// input. A configured tag is context, never an immutable artifact identity.
const clusterSSHPostgresRepository = "ghcr.io/cloudnative-pg/postgresql"

var clusterSSHPostgresTagRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

type clusterSSHPostgresImage struct {
	Configured string                        `json:"configured"`
	Resolved   string                        `json:"resolved"`
	Bootstrap  clusterSSHSourceExternalImage `json:"bootstrap"`
}

func (image clusterSSHPostgresImage) valid() bool {
	if !image.Bootstrap.valid(clusterSSHCNPGRepository) {
		return false
	}
	digest, ok := strings.CutPrefix(image.Resolved, clusterSSHPostgresRepository+"@sha256:")
	if !ok || !clusterSSHSourceObservationRE.MatchString(digest) || digest == strings.Repeat("0", 64) {
		return false
	}
	if image.Configured == image.Resolved {
		return true
	}
	tag, ok := strings.CutPrefix(image.Configured, clusterSSHPostgresRepository+":")
	return ok && clusterSSHPostgresTagRE.MatchString(tag)
}

// Caller already proves Pod ownership, active member placement and readiness.
// Require its one PostgreSQL container and running status to agree with the
// explicit Cluster imageName. Sidecars/catalogs need their own inventory;
// accepting them here would silently omit installed image demand.
func observeClusterSSHPostgresImage(clusterSpec, pod map[string]any) (clusterSSHPostgresImage, bool) {
	fail := func() (clusterSSHPostgresImage, bool) { return clusterSSHPostgresImage{}, false }
	if !clusterSSHStorageEmptyMap(clusterSpec["imageCatalogRef"]) {
		return fail()
	}
	spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "postgres" || container["image"] != clusterSpec["imageName"] {
		return fail()
	}
	statuses, ok := status["containerStatuses"].([]any)
	if !ok || len(statuses) != 1 {
		return fail()
	}
	runtime, ok := statuses[0].(map[string]any)
	if !ok || runtime["name"] != "postgres" || runtime["image"] != container["image"] || runtime["ready"] != true || runtime["started"] != true {
		return fail()
	}
	state := clusterSSHStorageMap(runtime, "state")
	if len(state) != 1 || len(clusterSSHStorageMap(state, "running")) == 0 {
		return fail()
	}
	bootstrap, ok := observeClusterSSHPostgresBootstrap(spec, status)
	if !ok {
		return fail()
	}
	image := clusterSSHPostgresImage{Bootstrap: bootstrap, Configured: clusterSSHStorageText(clusterSpec, "imageName"), Resolved: clusterSSHStorageText(runtime, "imageID")}
	if !image.valid() {
		return fail()
	}
	return image, true
}

// Source evidence accepts only a reviewed packaged image, never an arbitrary
// operator tag or an image ID from another repository. Both index and selected
// platform manifest are valid containerd identities for the same archive.
const clusterSSHCNPGRepository = "ghcr.io/cloudnative-pg/cloudnative-pg"

type clusterSSHSourceExternalImage struct {
	Configured string `json:"configured"`
	Resolved   string `json:"resolved"`
}

func (image clusterSSHSourceExternalImage) valid(repository string) bool {
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		repo := strings.Split(strings.Split(pin.Reference, "@")[0], ":")[0]
		if repo != repository {
			continue
		}
		index, manifest := repo+"@"+pin.IndexDigest, repo+"@"+pin.ManifestDigest
		return (image.Configured == pin.Reference || image.Configured == index || image.Configured == manifest) &&
			(image.Resolved == index || image.Resolved == manifest) &&
			(!strings.Contains(image.Configured, "@") || image.Configured == image.Resolved || image.Configured == index && image.Resolved == manifest)
	}
	return false
}

func observeClusterSSHPostgresBootstrap(spec, status map[string]any) (clusterSSHSourceExternalImage, bool) {
	fail := func() (clusterSSHSourceExternalImage, bool) { return clusterSSHSourceExternalImage{}, false }
	containers, ok := spec["initContainers"].([]any)
	if !ok || len(containers) != 1 {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "bootstrap-controller" || !clusterSSHStorageEmptyText(container["restartPolicy"]) {
		return fail()
	}
	statuses, ok := status["initContainerStatuses"].([]any)
	if !ok || len(statuses) != 1 {
		return fail()
	}
	runtime, ok := statuses[0].(map[string]any)
	if !ok || runtime["name"] != container["name"] || runtime["image"] != container["image"] {
		return fail()
	}
	state := clusterSSHStorageMap(runtime, "state")
	terminated := clusterSSHStorageMap(state, "terminated")
	exit, ok := clusterSSHStorageInteger(terminated["exitCode"])
	if len(state) != 1 || !ok || exit != 0 || terminated["reason"] != "Completed" {
		return fail()
	}
	image := clusterSSHSourceExternalImage{Configured: clusterSSHStorageText(container, "image"), Resolved: clusterSSHStorageText(runtime, "imageID")}
	if !image.valid(clusterSSHCNPGRepository) {
		return fail()
	}
	return image, true
}
