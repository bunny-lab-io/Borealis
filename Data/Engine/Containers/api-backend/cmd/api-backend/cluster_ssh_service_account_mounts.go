package main

import "strings"

// Strip only Kubernetes' standard admitted service-account projection. Each
// fixed caller still validates its own complete remaining mount/volume contract.
func clusterSSHServiceAccountMounts(rawMounts, rawVolumes any, baseCount int, pod bool) ([]any, []any, bool) {
	if baseCount == 0 {
		if rawVolumes == nil {
			rawVolumes = []any{}
		}
		if rawMounts == nil {
			rawMounts = []any{}
		}
	}
	volumes, ok := rawVolumes.([]any)
	if !ok || len(volumes) < baseCount || len(volumes) > baseCount+1 || !pod && len(volumes) != baseCount {
		return nil, nil, false
	}
	mounts, ok := rawMounts.([]any)
	if !ok || len(mounts) != len(volumes) {
		return nil, nil, false
	}
	// Admission may add only the standard projected credentials, checked without
	// reading their contents. The caller checks every remaining declared mount.
	token := ""
	baseVolumes := []any{}
	baseMounts := []any{}
	for _, raw := range volumes {
		volume, ok := raw.(map[string]any)
		name := clusterSSHStorageText(volume, "name")
		if !ok {
			return nil, nil, false
		}
		if strings.HasPrefix(name, "kube-api-access-") {
			if !pod || token != "" || len(volume) != 2 || !clusterSSHStorageName(name) || !clusterSSHLonghornCSIServiceAccountProjection(volume["projected"]) {
				return nil, nil, false
			}
			token = name
		} else {
			baseVolumes = append(baseVolumes, raw)
		}
	}
	found := false
	for _, raw := range mounts {
		mount, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, false
		}
		if token != "" && mount["name"] == token {
			if found || mount["mountPath"] != "/var/run/secrets/kubernetes.io/serviceaccount" || mount["readOnly"] != true {
				return nil, nil, false
			}
			found = true
			for key, value := range mount {
				if key != "name" && key != "mountPath" && key != "readOnly" && (key != "mountPropagation" || value != "None") {
					return nil, nil, false
				}
			}
		} else {
			baseMounts = append(baseMounts, raw)
		}
	}
	return baseMounts, baseVolumes, (token != "") == found
}
