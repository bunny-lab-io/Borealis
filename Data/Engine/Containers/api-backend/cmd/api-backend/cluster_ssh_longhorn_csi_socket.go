package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"slices"
	"strings"
)

// Declared host path only, never proof of the effective kubelet root, inode,
// socket type, host ownership or contents. No host IO occurs in this parser.
func clusterSSHLonghornCSISocketPath(raw string) (string, bool) {
	socket, ok := clusterSSHStoragePath(raw)
	root, hasSuffix := strings.CutSuffix(socket, "/plugins/driver.longhorn.io")
	_, rootOK := clusterSSHStoragePath(root)
	return socket, ok && hasSuffix && rootOK
}
func clusterSSHLonghornCSISocketSpec(spec map[string]any, pod bool) (string, bool) {
	fail := func() (string, bool) { return "", false }
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || spec["serviceAccountName"] != "longhorn-service-account" || (spec["hostNetwork"] != nil && spec["hostNetwork"] != false) {
		return fail()
	}
	container, ok := containers[0].(map[string]any)
	if !ok || !clusterSSHStorageEmptyList(container["volumeDevices"]) {
		return fail()
	}
	volumes, ok := spec["volumes"].([]any)
	if !ok || len(volumes) < 1 || len(volumes) > 2 || (!pod && len(volumes) != 1) {
		return fail()
	}
	names := map[string]bool{}
	socket := ""
	for _, raw := range volumes {
		volume, ok := raw.(map[string]any)
		name := clusterSSHStorageText(volume, "name")
		if !ok || len(volume) != 2 || names[name] {
			return fail()
		}
		names[name] = true
		if name == "socket-dir" {
			host := clusterSSHStorageMap(volume, "hostPath")
			var valid bool
			socket, valid = clusterSSHLonghornCSISocketPath(clusterSSHStorageText(host, "path"))
			if len(host) != 2 || host["type"] != "DirectoryOrCreate" || !valid {
				return fail()
			}
		} else if !pod || !strings.HasPrefix(name, "kube-api-access-") || !clusterSSHStorageName(name) || !clusterSSHLonghornCSIServiceAccountProjection(volume["projected"]) {
			return fail()
		}
	}
	if socket == "" {
		return fail()
	}
	mounts, ok := container["volumeMounts"].([]any)
	if !ok || len(mounts) != len(volumes) {
		return fail()
	}
	for _, raw := range mounts {
		mount, ok := raw.(map[string]any)
		name := clusterSSHStorageText(mount, "name")
		if !ok || !names[name] {
			return fail()
		}
		target, valid := clusterSSHStoragePath(clusterSSHStorageText(mount, "mountPath"))
		if !valid {
			return fail()
		}
		readonly := name != "socket-dir"
		if (readonly && target != "/var/run/secrets/kubernetes.io/serviceaccount") || (!readonly && target != "/csi") {
			return fail()
		}
		if readonly && mount["readOnly"] != true {
			return fail()
		}
		for key, value := range mount {
			switch key {
			case "name", "mountPath":
			case "readOnly":
				if value != readonly {
					return fail()
				}
			case "mountPropagation":
				if value != "None" {
					return fail()
				}
			default:
				return fail()
			}
		}
		delete(names, name)
	}
	return socket, len(names) == 0
}
func clusterSSHLonghornCSISocketMatches(socket string) func(map[string]any, bool) bool {
	return func(spec map[string]any, pod bool) bool {
		observed, ok := clusterSSHLonghornCSISocketSpec(spec, pod)
		return ok && observed == socket
	}
}

// Permit only the standard Kubernetes v1.36 admission projection at its fixed
// read-only mount. This checks declared shape, not token/CA/namespace contents.
func clusterSSHLonghornCSIServiceAccountProjection(raw any) bool {
	projection, ok := raw.(map[string]any)
	mode, modeOK := clusterSSHStorageInteger(projection["defaultMode"])
	sources, sourcesOK := projection["sources"].([]any)
	if !ok || len(projection) != 2 || !modeOK || mode != 420 || !sourcesOK || len(sources) != 3 {
		return false
	}
	seen := map[string]bool{}
	for _, rawSource := range sources {
		source, ok := rawSource.(map[string]any)
		if !ok || len(source) != 1 {
			return false
		}
		for kind, rawValue := range source {
			value, ok := rawValue.(map[string]any)
			if !ok || seen[kind] {
				return false
			}
			seen[kind] = true
			switch kind {
			case "serviceAccountToken":
				expiration, ok := clusterSSHStorageInteger(value["expirationSeconds"])
				if len(value) != 2 || value["path"] != "token" || !ok || expiration != 3607 {
					return false
				}
			case "configMap":
				items, ok := value["items"].([]any)
				if len(value) != 2 || value["name"] != "kube-root-ca.crt" || !ok || len(items) != 1 {
					return false
				}
				item, ok := items[0].(map[string]any)
				if !ok || len(item) != 2 || item["key"] != "ca.crt" || item["path"] != "ca.crt" {
					return false
				}
			case "downwardAPI":
				items, ok := value["items"].([]any)
				if len(value) != 1 || !ok || len(items) != 1 {
					return false
				}
				item, ok := items[0].(map[string]any)
				field := clusterSSHStorageMap(item, "fieldRef")
				if !ok || len(item) != 2 || item["path"] != "namespace" || len(field) != 2 || field["apiVersion"] != "v1" || field["fieldPath"] != "metadata.namespace" {
					return false
				}
			default:
				return false
			}
		}
	}
	return len(seen) == 3
}

// Source members can have different process/inode identities, but every actual
// kubelet root must agree with the single cluster-wide CSI hostPath declaration.
func (r clusterSSHStorageRequirements) csiSocketMatchesNetworks(sources []clusterbootstrap.SourceNetwork) bool {
	if len(sources) < 1 || len(sources) > 2 || len(r.LonghornPlugin) != len(sources) {
		return false
	}
	seen := map[string]bool{}
	for _, source := range sources {
		peer := source.Kubelet.CSISocket.Listener
		if seen[source.NodeUID] || peer.UserID != 0 || !slices.ContainsFunc(r.LonghornPlugin, func(p clusterSSHLonghornPluginMember) bool {
			return p.Node == source.Hostname && p.NodeUID == source.NodeUID && p.PodUID == peer.PodUID && p.ContainerID == peer.ContainerID
		}) {
			return false
		}
		seen[source.NodeUID] = true
		if source.Validate() != nil || r.LonghornCSISocketPath != source.Kubelet.Root+clusterbootstrap.KubeletCSIDirectorySuffix {
			return false
		}
	}
	return true
}
