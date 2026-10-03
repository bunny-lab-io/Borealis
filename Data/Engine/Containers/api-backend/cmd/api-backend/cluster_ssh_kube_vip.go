package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"net/netip"
	"regexp"
	"strings"
)

const clusterSSHKubeVIPPath = "/apis/apps/v1/namespaces/kube-system/daemonsets/kube-vip-borealis-cluster"
const clusterSSHKubeVIPRepository = "ghcr.io/kube-vip/kube-vip"

var clusterSSHKubeVIPInterface = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`)

// Desired configuration and active-source running image identity. Lease
// ownership and network health remain separate; this never grants readiness.
type clusterSSHKubeVIPConfiguration struct {
	Image     string `json:"image"`
	Resolved  string `json:"resolved"`
	Address   string `json:"address"`
	Interface string `json:"interface"`
}

func (v clusterSSHKubeVIPConfiguration) valid(source clusterSSHSourceCohort) bool {
	return v.configuredValid(source) && (clusterSSHSourceExternalImage{Configured: v.Image, Resolved: v.Resolved}).valid(clusterSSHKubeVIPRepository)
}

func (v clusterSSHKubeVIPConfiguration) configuredValid(source clusterSSHSourceCohort) bool {
	address, err := netip.ParseAddr(v.Address)
	if err != nil || !address.Is4() || !address.IsPrivate() || v.Address != source.ControlPlaneVIP || v.Address != source.EdgeVIP || !clusterSSHKubeVIPInterface.MatchString(v.Interface) || len(v.Image) > 256 {
		return false
	}
	for _, pin := range clusterbootstrap.ExternalImagePins() {
		if strings.HasPrefix(pin.Reference, clusterSSHKubeVIPRepository+"@") && (v.Image == pin.Reference || v.Image == clusterSSHKubeVIPRepository+"@"+pin.IndexDigest || v.Image == clusterSSHKubeVIPRepository+"@"+pin.ManifestDigest) {
			return true
		}
	}
	return false
}

// Compare against fresh source-native management links, not a target interface
// or controller-local environment. The shared DaemonSet sets one interface.
func (v clusterSSHKubeVIPConfiguration) matchesNetworks(sources []clusterbootstrap.SourceNetwork) bool {
	if len(sources) < 1 || len(sources) > 2 {
		return false
	}
	for _, source := range sources {
		if source.Validate() != nil || source.ManagementLink.Interface != v.Interface {
			return false
		}
	}
	return true
}

func observeClusterSSHKubeVIP(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort) (clusterSSHKubeVIPConfiguration, error) {
	fail := func() (clusterSSHKubeVIPConfiguration, error) {
		return clusterSSHKubeVIPConfiguration{}, clusterbootstrap.ErrPreparationConfig
	}
	object, err := read(clusterSSHKubeVIPPath)
	metadata, ok := clusterSSHStorageMetadata(object, "apps/v1", "DaemonSet", "kube-system")
	if err != nil || !ok || metadata.Name != "kube-vip-borealis-cluster" {
		return fail()
	}
	spec := clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template"), "spec")
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || spec["hostNetwork"] != true || spec["serviceAccountName"] != "kube-vip-borealis" || spec["automountServiceAccountToken"] != true ||
		!clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) || !clusterSSHStorageEmptyList(spec["volumes"]) {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	if !ok || container["name"] != "kube-vip" || !clusterSSHStorageEmptyList(container["command"]) ||
		!clusterSSHStorageEmptyList(container["envFrom"]) || !clusterSSHStorageEmptyList(container["volumeMounts"]) || !clusterSSHStorageEmptyList(container["volumeDevices"]) ||
		!clusterSSHStorageEmptyText(container["workingDir"]) || !clusterSSHStorageEmptyText(container["restartPolicy"]) || !clusterSSHStorageEmptyMap(container["lifecycle"]) {
		return fail()
	}
	args, ok := container["args"].([]any)
	if !ok || len(args) != 1 || args[0] != "manager" {
		return fail()
	}
	env, ok := container["env"].([]any)
	if !ok || len(env) != 15 {
		return fail()
	}
	wanted := map[string]string{"vip_arp": "true", "port": "6443", "vip_subnet": "32", "cp_namespace": "kube-system", "vip_leaderelection": "true", "vip_leasename": "borealis-cluster-vip", "vip_leaseduration": "10", "vip_renewdeadline": "5", "vip_retryperiod": "2", "cp_enable": "true", "svc_enable": "false", "prometheus_server": ":2112"}
	result := clusterSSHKubeVIPConfiguration{Image: clusterSSHStorageText(container, "image")}
	seen := map[string]bool{}
	for _, raw := range env {
		entry, ok := raw.(map[string]any)
		name := clusterSSHStorageText(entry, "name")
		if !ok || len(entry) != 2 || seen[name] {
			return fail()
		}
		seen[name] = true
		if name == "vip_nodename" {
			from := clusterSSHStorageMap(entry, "valueFrom")
			field := clusterSSHStorageMap(from, "fieldRef")
			if len(from) != 1 || field["fieldPath"] != "spec.nodeName" || !(len(field) == 1 || len(field) == 2 && field["apiVersion"] == "v1") {
				return fail()
			}
			continue
		}
		value, ok := entry["value"].(string)
		if !ok {
			return fail()
		}
		switch name {
		case "address":
			result.Address = value
		case "vip_interface":
			result.Interface = value
		default:
			expected, known := wanted[name]
			if !known || value != expected {
				return fail()
			}
		}
	}
	if !result.configuredValid(source) {
		return fail()
	}
	result.Resolved, err = observeClusterSSHKubeVIPRuntime(readList, source, metadata, result.Image)
	if err != nil || !result.valid(source) {
		return fail()
	}
	return result, nil
}
