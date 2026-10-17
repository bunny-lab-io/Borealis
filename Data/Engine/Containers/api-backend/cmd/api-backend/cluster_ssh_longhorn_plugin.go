package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"slices"
	"strings"
	"time"
)

const clusterSSHLonghornPluginPath = "/apis/apps/v1/namespaces/longhorn-system/daemonsets/longhorn-csi-plugin"
const clusterSSHLonghornPluginPodsPrefix = "/api/v1/namespaces/longhorn-system/pods?labelSelector=app%3Dlonghorn-csi-plugin&fieldSelector=spec.nodeName%3D"

func clusterSSHLonghornPluginPodsPath(node string) string {
	return clusterSSHLonghornPluginPodsPrefix + node + "&limit=2"
}
func clusterSSHLonghornPluginPodsPathValid(path string) bool {
	node, ok := strings.CutPrefix(path, clusterSSHLonghornPluginPodsPrefix)
	if !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, "&limit=2")
	return ok && clusterSSHStorageName(node)
}

type clusterSSHLonghornPluginMember struct {
	Node              string `json:"node"`
	NodeUID           string `json:"node_uid"`
	PodUID            string `json:"pod_uid"`
	ContainerID       string `json:"container_id"`
	ManagerResolved   string `json:"manager_resolved"`
	RegistrarResolved string `json:"registrar_resolved"`
	LivenessResolved  string `json:"liveness_resolved"`
}

func clusterSSHLonghornPluginValid(members []clusterSSHLonghornPluginMember, source clusterSSHSourceCohort, driver clusterSSHLonghornDriverImages) bool {
	if len(members) < 1 || len(members) > 2 || len(members) != len(source.Members) || !driver.valid() {
		return false
	}
	nodes, pods, containers := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, m := range members {
		identity := clusterbootstrap.SourceCSIListener{PodUID: m.PodUID, ContainerID: m.ContainerID, IdentitySHA256: strings.Repeat("1", 64)}
		if identity.Validate() != nil || nodes[m.Node] || pods[m.PodUID] || containers[m.ContainerID] || !slices.ContainsFunc(source.Members, func(s clusterSSHSourceMember) bool { return s.Name == m.Node && s.NodeUID == m.NodeUID }) {
			return false
		}
		nodes[m.Node], pods[m.PodUID], containers[m.ContainerID] = true, true, true
		for _, image := range []struct{ configured, resolved, role string }{{driver.Manager, m.ManagerResolved, "longhorn-manager"}, {driver.Registrar, m.RegistrarResolved, "csi-node-driver-registrar"}, {driver.Liveness, m.LivenessResolved, "livenessprobe"}} {
			if !(clusterSSHSourceExternalImage{Configured: image.configured, Resolved: image.resolved}).valid("docker.io/longhornio/" + image.role) {
				return false
			}
		}
	}
	return true
}
func observeClusterSSHLonghornPlugin(read func(string) (map[string]any, error), readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort, driver clusterSSHLonghornDriverImages, socket string) ([]clusterSSHLonghornPluginMember, error) {
	fail := func() ([]clusterSSHLonghornPluginMember, error) { return nil, clusterbootstrap.ErrPreparationConfig }
	if read == nil || readList == nil || !driver.valid() || len(source.Members) < 1 || len(source.Members) > 2 {
		return fail()
	}
	object, err := read(clusterSSHLonghornPluginPath)
	owner, ok := clusterSSHStorageMetadata(object, "apps/v1", "DaemonSet", "longhorn-system")
	template := clusterSSHStorageMap(clusterSSHStorageMap(object, "spec"), "template")
	if err != nil || !ok || owner.Name != "longhorn-csi-plugin" || clusterSSHStorageMap(clusterSSHStorageMap(template, "metadata"), "labels")["app"] != "longhorn-csi-plugin" || !clusterSSHLonghornPluginSpec(clusterSSHStorageMap(template, "spec"), false, driver, socket) {
		return fail()
	}
	result := []clusterSSHLonghornPluginMember{}
	for _, member := range source.Members {
		if !clusterSSHStorageName(member.Name) {
			return fail()
		}
		pods, err := readList(clusterSSHLonghornPluginPodsPath(member.Name), "Pod", "longhorn-system", 2)
		if err != nil || len(pods) != 1 {
			return fail()
		}
		pod := pods[0]
		id, ok := clusterSSHStorageMetadata(pod, "v1", "Pod", "longhorn-system")
		meta, spec, status := clusterSSHStorageMap(pod, "metadata"), clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
		if !ok || clusterSSHStorageMap(meta, "labels")["app"] != "longhorn-csi-plugin" || spec["nodeName"] != member.Name || !clusterSSHLonghornPluginSpec(spec, true, driver, socket) || status["phase"] != "Running" || !clusterSSHStorageEmptyList(status["initContainerStatuses"]) || !clusterSSHStorageEmptyList(status["ephemeralContainerStatuses"]) {
			return fail()
		}
		refs, ok := meta["ownerReferences"].([]any)
		if !ok || len(refs) != 1 {
			return fail()
		}
		ref, ok := refs[0].(map[string]any)
		if !ok || ref["apiVersion"] != "apps/v1" || ref["kind"] != "DaemonSet" || ref["name"] != owner.Name || ref["uid"] != owner.UID || ref["controller"] != true {
			return fail()
		}
		ready := 0
		conditions, ok := status["conditions"].([]any)
		if !ok {
			return fail()
		}
		for _, raw := range conditions {
			c, ok := raw.(map[string]any)
			if !ok {
				return fail()
			}
			if c["type"] == "Ready" {
				if c["status"] != "True" {
					return fail()
				}
				ready++
			}
		}
		if ready != 1 {
			return fail()
		}
		runtimes, ok := status["containerStatuses"].([]any)
		if !ok || len(runtimes) != 3 {
			return fail()
		}
		m := clusterSSHLonghornPluginMember{Node: member.Name, NodeUID: member.NodeUID, PodUID: id.UID}
		wanted := map[string]string{"longhorn-csi-plugin": driver.Manager, "node-driver-registrar": driver.Registrar, "longhorn-liveness-probe": driver.Liveness}
		ids := map[string]bool{}
		for _, raw := range runtimes {
			runtime, ok := raw.(map[string]any)
			name := clusterSSHStorageText(runtime, "name")
			image, known := wanted[name]
			if !ok || !known || runtime["image"] != image || runtime["ready"] != true || runtime["started"] != true {
				return fail()
			}
			delete(wanted, name)
			state := clusterSSHStorageMap(runtime, "state")
			started, err := time.Parse(time.RFC3339Nano, clusterSSHStorageText(clusterSSHStorageMap(state, "running"), "startedAt"))
			if err != nil || started.IsZero() || len(state) != 1 {
				return fail()
			}
			cid, ok := strings.CutPrefix(clusterSSHStorageText(runtime, "containerID"), "containerd://")
			if !ok || ids[cid] || !clusterSSHSourceObservationRE.MatchString(cid) || cid == strings.Repeat("0", 64) {
				return fail()
			}
			ids[cid] = true
			resolved := clusterSSHStorageText(runtime, "imageID")
			switch name {
			case "longhorn-csi-plugin":
				m.ContainerID = cid
				m.ManagerResolved = resolved
			case "node-driver-registrar":
				m.RegistrarResolved = resolved
			case "longhorn-liveness-probe":
				m.LivenessResolved = resolved
			}
		}
		result = append(result, m)
	}
	slices.SortFunc(result, func(a, b clusterSSHLonghornPluginMember) int { return strings.Compare(a.Node, b.Node) })
	if !clusterSSHLonghornPluginValid(result, source, driver) {
		return fail()
	}
	return result, nil
}

// Pinned v1.12 plugin constructor. Only the existing baseline is accepted;
// declared command, environment and mounts cannot substitute another endpoint.
func clusterSSHLonghornPluginSpec(spec map[string]any, pod bool, driver clusterSSHLonghornDriverImages, socket string) bool {
	root, ok := strings.CutSuffix(socket, clusterbootstrap.KubeletCSIDirectorySuffix)
	if !ok || !clusterbootstrap.ValidSourceKubeletRoot(root) || spec["serviceAccountName"] != "longhorn-service-account" || spec["hostNetwork"] != nil && spec["hostNetwork"] != false || spec["hostPID"] != nil && spec["hostPID"] != false || spec["hostIPC"] != nil && spec["hostIPC"] != false || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) {
		return false
	}
	for _, security := range []map[string]any{clusterSSHStorageMap(spec, "securityContext")} {
		for _, key := range []string{"runAsUser", "runAsGroup"} {
			if v := security[key]; v != nil {
				n, ok := clusterSSHStorageInteger(v)
				if !ok || n != 0 {
					return false
				}
			}
		}
		if v := security["runAsNonRoot"]; v != nil && v != false {
			return false
		}
	}
	volumes, ok := spec["volumes"].([]any)
	if !ok || len(volumes) < 8 || len(volumes) > 9 || !pod && len(volumes) != 8 {
		return false
	}
	hosts := map[string]string{"socket-dir": socket, "kubernetes-csi-dir": root + "/plugins/kubernetes.io/csi", "registration-dir": root + "/plugins_registry", "pods-mount-dir": root + "/pods", "host-dev": "/dev", "host-proc": "/proc", "host-sys": "/sys", "lib-modules": "/lib/modules"}
	token := ""
	for _, raw := range volumes {
		v, ok := raw.(map[string]any)
		name := clusterSSHStorageText(v, "name")
		want, known := hosts[name]
		if !ok || len(v) != 2 {
			return false
		}
		if !known {
			if !pod || token != "" || !strings.HasPrefix(name, "kube-api-access-") || !clusterSSHStorageName(name) || !clusterSSHLonghornCSIServiceAccountProjection(v["projected"]) {
				return false
			}
			token = name
			continue
		}
		host := clusterSSHStorageMap(v, "hostPath")
		if host["path"] != want || len(host) < 1 || len(host) > 2 {
			return false
		}
		if strings.HasPrefix(want, root+"/") {
			if host["type"] != "DirectoryOrCreate" {
				return false
			}
		} else if !clusterSSHStorageEmptyText(host["type"]) {
			return false
		}
		delete(hosts, name)
	}
	if len(hosts) != 0 {
		return false
	}
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 3 {
		return false
	}
	wanted := map[string]string{"longhorn-csi-plugin": driver.Manager, "node-driver-registrar": driver.Registrar, "longhorn-liveness-probe": driver.Liveness}
	for _, raw := range containers {
		c, ok := raw.(map[string]any)
		name := clusterSSHStorageText(c, "name")
		image, known := wanted[name]
		if !ok || !known || c["image"] != image || !clusterSSHStorageEmptyList(c["command"]) || !clusterSSHStorageEmptyList(c["envFrom"]) || !clusterSSHStorageEmptyList(c["volumeDevices"]) || !clusterSSHStorageEmptyText(c["workingDir"]) || !clusterSSHStorageEmptyText(c["restartPolicy"]) {
			return false
		}
		delete(wanted, name)
		args := []string{"--v=4", "--csi-address=/csi/csi.sock"}
		envValues := map[string]string{}
		envFields := map[string]string{}
		mounts := map[string]string{"socket-dir": "/csi"}
		hook := ""
		switch name {
		case "node-driver-registrar":
			args = []string{"--v=2", "--csi-address=$(ADDRESS)", "--kubelet-registration-path=" + socket + "/csi.sock"}
			envValues["ADDRESS"] = "/csi/csi.sock"
			mounts["registration-dir"] = "/registration"
			hook = "rm -rf /registration/driver.longhorn.io /registration/driver.longhorn.io-reg.sock /csi//*"
		case "longhorn-csi-plugin":
			args = []string{"longhorn-manager", "-d", "csi", "--nodeid=$(NODE_ID)", "--endpoint=$(CSI_ENDPOINT)", "--drivername=driver.longhorn.io", "--manager-url=http://longhorn-backend:9500/v1"}
			envValues["CSI_ENDPOINT"] = "unix:///csi/csi.sock"
			envFields["NODE_ID"] = "spec.nodeName"
			envFields["POD_NAMESPACE"] = "metadata.namespace"
			mounts["kubernetes-csi-dir"] = root + "/plugins/kubernetes.io/csi"
			mounts["pods-mount-dir"] = root + "/pods"
			mounts["host-dev"] = "/dev"
			mounts["host-proc"] = "/host/proc"
			mounts["host-sys"] = "/sys"
			mounts["lib-modules"] = "/lib/modules"
			hook = "rm -f /csi//*"
		}
		actual, ok := clusterSSHLonghornDriverStrings(c["args"])
		if !ok || !slices.Equal(actual, args) {
			return false
		}
		if !clusterSSHLonghornPluginEnv(c["env"], envValues, envFields) {
			return false
		}
		lifecycle := clusterSSHStorageMap(c, "lifecycle")
		if hook == "" {
			if !clusterSSHStorageEmptyMap(c["lifecycle"]) {
				return false
			}
		} else {
			stop := clusterSSHStorageMap(lifecycle, "preStop")
			exec := clusterSSHStorageMap(stop, "exec")
			command, ok := clusterSSHLonghornDriverStrings(exec["command"])
			if len(lifecycle) != 1 || len(stop) != 1 || len(exec) != 1 || !ok || !slices.Equal(command, []string{"/bin/sh", "-c", hook}) {
				return false
			}
		}
		security := clusterSSHStorageMap(c, "securityContext")
		for _, key := range []string{"runAsUser", "runAsGroup"} {
			if v := security[key]; v != nil {
				n, ok := clusterSSHStorageInteger(v)
				if !ok || n != 0 {
					return false
				}
			}
		}
		if v := security["runAsNonRoot"]; v != nil && v != false {
			return false
		}
		if name != "longhorn-liveness-probe" && security["privileged"] != true {
			return false
		}
		if token != "" {
			mounts[token] = "/var/run/secrets/kubernetes.io/serviceaccount"
		}
		actualMounts, ok := c["volumeMounts"].([]any)
		if !ok || len(actualMounts) != len(mounts) {
			return false
		}
		for _, raw := range actualMounts {
			m, ok := raw.(map[string]any)
			n := clusterSSHStorageText(m, "name")
			target, known := mounts[n]
			observed, valid := clusterSSHStoragePath(clusterSSHStorageText(m, "mountPath"))
			if !ok || !known || !valid || observed != target {
				return false
			}
			ro := n == "lib-modules" || n == token
			if ro && m["readOnly"] != true || !ro && m["readOnly"] != nil && m["readOnly"] != false {
				return false
			}
			propagation := "None"
			if n == "kubernetes-csi-dir" || n == "pods-mount-dir" {
				propagation = "Bidirectional"
			}
			if propagation == "Bidirectional" && m["mountPropagation"] != propagation || propagation == "None" && !clusterSSHStorageEmptyText(m["mountPropagation"]) && m["mountPropagation"] != propagation {
				return false
			}
			for key := range m {
				if key != "name" && key != "mountPath" && key != "readOnly" && key != "mountPropagation" {
					return false
				}
			}
			delete(mounts, n)
		}
	}
	return len(wanted) == 0
}
func clusterSSHLonghornPluginEnv(raw any, values, fields map[string]string) bool {
	if len(values)+len(fields) == 0 {
		return clusterSSHStorageEmptyList(raw)
	}
	env, ok := raw.([]any)
	if !ok || len(env) != len(values)+len(fields) {
		return false
	}
	for _, raw := range env {
		e, ok := raw.(map[string]any)
		name := clusterSSHStorageText(e, "name")
		if !ok || len(e) != 2 {
			return false
		}
		if value, known := values[name]; known {
			if e["value"] != value {
				return false
			}
			delete(values, name)
		} else {
			path, known := fields[name]
			from := clusterSSHStorageMap(e, "valueFrom")
			field := clusterSSHStorageMap(from, "fieldRef")
			if !known || len(from) != 1 || field["fieldPath"] != path || !(len(field) == 1 || len(field) == 2 && field["apiVersion"] == "v1") {
				return false
			}
			delete(fields, name)
		}
	}
	return len(values)+len(fields) == 0
}
