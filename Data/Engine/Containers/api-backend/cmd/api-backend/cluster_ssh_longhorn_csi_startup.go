package main

import "strings"

// Reviewed Longhorn v1.12 CSI invocation, checked on desired/owner templates and
// observed Pod specs. This does not prove loaded process state or socket contents.
func clusterSSHLonghornCSIStartup(container map[string]any, role string) bool {
	return clusterSSHStorageEmptyList(container["command"]) &&
		clusterSSHStorageEmptyList(container["envFrom"]) &&
		clusterSSHStorageEmptyText(container["workingDir"]) &&
		clusterSSHStorageEmptyMap(container["lifecycle"]) &&
		clusterSSHLonghornCSIArguments(container["args"], role) &&
		clusterSSHLonghornCSIEnvironment(container["env"])
}

func clusterSSHLonghornCSIArguments(raw any, role string) bool {
	wanted := map[string]string{
		"--v": "2", "--csi-address": "$(ADDRESS)", "--timeout": "1m50s",
		"--leader-election": "true", "--leader-election-namespace": "$(POD_NAMESPACE)",
		"--kube-api-qps": "50", "--kube-api-burst": "100", "--http-endpoint": ":8000",
	}
	booleans := map[string]bool{"--leader-election": true}
	switch role {
	case "csi-attacher", "csi-snapshotter":
	case "csi-provisioner":
		wanted["--default-fstype"] = "ext4"
		wanted["--enable-capacity"] = "true"
		wanted["--capacity-ownerref-level"] = "2"
		wanted["--immediate-topology"] = "false"
		booleans["--enable-capacity"], booleans["--immediate-topology"] = true, true
	case "csi-resizer":
		wanted["--handle-volume-inuse-error"] = "false"
		wanted["--feature-gates"] = "RecoverVolumeExpansionFailure=false"
		booleans["--handle-volume-inuse-error"] = true
	default:
		return false
	}
	values, ok := raw.([]any)
	if !ok || len(values) < len(wanted) || len(values) > 2*len(wanted)+2 {
		return false
	}
	args := make([]string, len(values))
	for i, rawArg := range values {
		arg, ok := rawArg.(string)
		if !ok || len(arg) > 512 {
			return false
		}
		args[i] = arg
	}
	seen := map[string]int{}
	for i := 0; i < len(args); i++ {
		flag, value, equals := strings.Cut(args[i], "=")
		expected, known := wanted[flag]
		if !known {
			return false
		}
		if !equals {
			if booleans[flag] {
				value = "true"
			} else {
				i++
				if i >= len(args) {
					return false
				}
				value = args[i]
			}
		}
		if value != expected {
			return false
		}
		seen[flag]++
		limit := 1
		// The pinned resizer constructor emits this identical flag twice. Only that
		// reviewed repetition is allowed; conflicting values fail above.
		if role == "csi-resizer" && flag == "--leader-election-namespace" {
			limit = 2
		}
		if seen[flag] > limit {
			return false
		}
	}
	return len(seen) == len(wanted)
}

// Explicit baseline inputs from getCommonDeployment. Indirect/unknown settings,
// including an unqualified optional TZ override, remain fail-closed.
func clusterSSHLonghornCSIEnvironment(raw any) bool {
	env, ok := raw.([]any)
	if !ok || len(env) != 4 {
		return false
	}
	fields := map[string]string{"POD_NAMESPACE": "metadata.namespace", "NAMESPACE": "metadata.namespace", "POD_NAME": "metadata.name"}
	seen := map[string]bool{}
	for _, rawEntry := range env {
		entry, ok := rawEntry.(map[string]any)
		name := clusterSSHStorageText(entry, "name")
		if !ok || len(entry) != 2 || seen[name] {
			return false
		}
		seen[name] = true
		if name == "ADDRESS" {
			if entry["value"] != "/csi/csi.sock" {
				return false
			}
		} else {
			path, known := fields[name]
			from := clusterSSHStorageMap(entry, "valueFrom")
			field := clusterSSHStorageMap(from, "fieldRef")
			if !known || len(from) != 1 || field["fieldPath"] != path || !(len(field) == 1 || len(field) == 2 && field["apiVersion"] == "v1") {
				return false
			}
		}
	}
	return true
}
