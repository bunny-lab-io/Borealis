package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Identities come from original trusted observations, never discovery of a
// replacement during recovery. This callable adapter grants no runtime action.
// Its current contract requires private IPv4, digest-pinned images, and original
// containerd identities. A restart/replacement cannot silently rebind evidence.
type clusterAutomaticRecoveryApplicationBinding struct {
	Node                            clusterAutomaticRecoveryNodeBinding
	NamespaceUID, ServiceUID        string
	EndpointSlice, EndpointSliceUID string
	PodIP, ServiceIP                string
	Port                            int
	API, Scheduler                  clusterAutomaticRecoveryWorkloadBinding
}

type clusterAutomaticRecoveryWorkloadBinding struct {
	Deployment, DeploymentUID, ReplicaSet, ReplicaSetUID, Pod, PodUID string
	Image, ImageID, ContainerID                                       string
}

func recoveryApplicationIP(value string) bool {
	ip := net.ParseIP(value)
	return ip != nil && ip.To4() != nil && ip.String() == value && ip.IsPrivate()
}

func (b clusterAutomaticRecoveryApplicationBinding) valid() bool {
	if !b.Node.valid() || !clusterSSHStorageName(b.EndpointSlice) || !recoveryApplicationIP(b.PodIP) ||
		!recoveryApplicationIP(b.ServiceIP) || b.PodIP == b.ServiceIP || b.Port < 1 || b.Port > 65535 {
		return false
	}
	uids := []string{b.Node.KubeSystemUID, b.Node.NodeUID, b.NamespaceUID, b.ServiceUID, b.EndpointSliceUID}
	for _, w := range []clusterAutomaticRecoveryWorkloadBinding{b.API, b.Scheduler} {
		for _, name := range []string{w.Deployment, w.ReplicaSet, w.Pod} {
			if !clusterSSHStorageName(name) || strings.Contains(name, "-candidate") {
				return false
			}
		}
		for _, image := range []string{w.Image, w.ImageID} {
			prefix, digest, ok := strings.Cut(image, "@")
			if !ok || prefix == "" || len(prefix) > 255 || strings.ContainsAny(prefix, " \t\r\n") || !clusterBootstrapDigestPattern.MatchString(digest) {
				return false
			}
		}
		if !strings.HasPrefix(w.ContainerID, "containerd://") || !validClusterSSHPreparationDigest(strings.TrimPrefix(w.ContainerID, "containerd://")) {
			return false
		}
		uids = append(uids, w.DeploymentUID, w.ReplicaSetUID, w.PodUID)
	}
	seen := map[string]bool{}
	for _, uid := range uids {
		if !vipScopeUUID(uid) || seen[uid] {
			return false
		}
		seen[uid] = true
	}
	return b.API.Pod != b.Scheduler.Pod && b.API.Deployment != b.Scheduler.Deployment && b.API.ContainerID != b.Scheduler.ContainerID
}

type clusterAutomaticRecoveryApplicationProbe func(context.Context, string, int, string) error

func (c *clusterAutomaticRecoveryCoordinator) applicationHealthObserver(b clusterAutomaticRecoveryApplicationBinding) clusterAutomaticRecoveryObserve {
	return c.applicationHealthObserverWithProbe(b, newRecoveryApplicationProbe())
}

func (c *clusterAutomaticRecoveryCoordinator) applicationHealthObserverWithProbe(b clusterAutomaticRecoveryApplicationBinding, probe clusterAutomaticRecoveryApplicationProbe) clusterAutomaticRecoveryObserve {
	return c.nativeHealthObserver(b.Node, func(ctx context.Context, kube *kubernetesAPIClient) (clusterAutomaticRecoveryHealth, error) {
		// The reader constructs every path from validated original identities.
		get := func(ctx context.Context, path string, out any) error {
			return kube.clusterSSHPrivateJSON(ctx, http.MethodGet, path, nil, out)
		}
		return readClusterAutomaticRecoveryApplication(ctx, get, probe, b)
	})
}

// Same credential-free probes used by native InspectHealth, with direct /ready
// additionally required: a healthy Service peer cannot mask an unready target.
// No ambient proxy, cookies, redirects, credentials or arbitrary URL capability.
func newRecoveryApplicationProbe() clusterAutomaticRecoveryApplicationProbe {
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
		DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: 3 * time.Second}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, address string, port int, path string) error {
		return recoveryApplicationHTTP(ctx, client, address, port, path)
	}
}

func recoveryApplicationHTTP(ctx context.Context, client *http.Client, address string, port int, path string) error {
	if client == nil || !recoveryApplicationIP(address) || port < 1 || port > 65535 || !textInSet(path, "/startup", "/live", "/ready") || ctx.Err() != nil {
		return errClusterConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(address, strconv.Itoa(port))+path, nil)
	if err != nil {
		return errClusterConflict
	}
	response, err := client.Do(request)
	if err != nil {
		return errClusterConflict
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || ctx.Err() != nil {
		return errClusterConflict
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1025))
	var result clusterNativeObservationObject
	if err != nil || len(body) > 1024 || json.Unmarshal(body, &result) != nil || len(result) != 2 || result["ok"] != true ||
		result["phase"] != map[string]string{"/startup": "started", "/live": "live", "/ready": "ready"}[path] || ctx.Err() != nil {
		return errClusterConflict
	}
	return nil
}

func recoveryApplicationLabels(labels map[string]any, name, node string) bool {
	return labels["app.kubernetes.io/name"] == name && labels["borealis.io/engine-node"] == node &&
		labels["borealis.io/node-workload"] == "true" && labels["borealis.io/update-candidate"] == "false" && labels["borealis.io/traffic-state"] == "active"
}

func recoveryApplicationSelector(selector, labels map[string]any) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		if _, ok := v.(string); !ok || v == "" || labels[k] != v {
			return false
		}
	}
	return true
}

func recoveryApplicationWorkload(objects map[string]map[string]any, b clusterAutomaticRecoveryWorkloadBinding, name, node string) bool {
	deployment, rs, pod := objects["deployments/"+b.Deployment], objects["replicasets/"+b.ReplicaSet], objects["pods/"+b.Pod]
	for _, object := range []map[string]any{deployment, rs, pod} {
		if !recoveryApplicationLabels(clusterSSHStorageMap(clusterSSHStorageMap(object, "metadata"), "labels"), name, node) {
			return false
		}
	}
	owner, ok := clusterSSHLonghornUIOwner(pod, "ReplicaSet")
	parent, parentOK := clusterSSHLonghornUIOwner(rs, "Deployment")
	if !ok || !parentOK || owner.Name != b.ReplicaSet || owner.UID != b.ReplicaSetUID || parent.Name != b.Deployment || parent.UID != b.DeploymentUID {
		return false
	}
	for _, object := range []map[string]any{deployment, rs} {
		spec, status := clusterSSHStorageMap(object, "spec"), clusterSSHStorageMap(object, "status")
		generation, genOK := clusterSSHStorageInteger(clusterSSHStorageMap(object, "metadata")["generation"])
		observed, observedOK := clusterSSHStorageInteger(status["observedGeneration"])
		replicas, replicasOK := clusterSSHStorageInteger(spec["replicas"])
		ready, readyOK := clusterSSHStorageInteger(status["readyReplicas"])
		if !genOK || generation < 1 || !observedOK || generation != observed || !replicasOK || replicas != 1 || !readyOK || ready != 1 {
			return false
		}
		template := clusterSSHStorageMap(spec, "template")
		labels := clusterSSHStorageMap(clusterSSHStorageMap(template, "metadata"), "labels")
		if !recoveryApplicationLabels(labels, name, node) || !recoveryApplicationSelector(clusterSSHStorageMap(clusterSSHStorageMap(spec, "selector"), "matchLabels"), labels) ||
			!recoveryApplicationSelector(clusterSSHStorageMap(clusterSSHStorageMap(spec, "selector"), "matchLabels"), clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")) ||
			!recoveryApplicationContainer(clusterSSHStorageMap(template, "spec"), name, b.Image) {
			return false
		}
	}
	for _, key := range []string{"availableReplicas", "updatedReplicas"} {
		v, ok := clusterSSHStorageInteger(clusterSSHStorageMap(deployment, "status")[key])
		if !ok || v != 1 {
			return false
		}
	}
	spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
	if spec["nodeName"] != node || !recoveryApplicationContainer(spec, name, b.Image) || status["phase"] != "Running" || !validSourceConditions(status) || !sourceConditionTrue(status, "Ready") {
		return false
	}
	states, ok := status["containerStatuses"].([]any)
	if !ok || len(states) != 1 {
		return false
	}
	runtime, ok := states[0].(map[string]any)
	state := clusterSSHStorageMap(runtime, "state")
	_, startedErr := time.Parse(time.RFC3339Nano, clusterSSHStorageText(clusterSSHStorageMap(state, "running"), "startedAt"))
	return ok && runtime["name"] == name && runtime["image"] == b.Image && runtime["imageID"] == b.ImageID && runtime["containerID"] == b.ContainerID &&
		runtime["ready"] == true && runtime["started"] == true && len(state) == 1 && startedErr == nil
}

func recoveryApplicationContainer(spec map[string]any, name, image string) bool {
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || !clusterSSHStorageEmptyList(spec["initContainers"]) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) || spec["hostNetwork"] != nil && spec["hostNetwork"] != false {
		return false
	}
	container, ok := containers[0].(map[string]any)
	return ok && container["name"] == name && container["image"] == image
}

func readClusterAutomaticRecoveryApplication(ctx context.Context, get func(context.Context, string, any) error, probe clusterAutomaticRecoveryApplicationProbe, b clusterAutomaticRecoveryApplicationBinding) (clusterAutomaticRecoveryHealth, error) {
	fail := func() (clusterAutomaticRecoveryHealth, error) {
		return clusterAutomaticRecoveryHealth{}, errClusterConflict
	}
	if !b.valid() || get == nil || probe == nil || ctx.Err() != nil {
		return fail()
	}
	read := func() (map[string]map[string]any, error) {
		if _, err := readClusterAutomaticRecoveryNodeHealth(ctx, get, b.Node); err != nil {
			return nil, err
		}
		objects := map[string]map[string]any{}
		type objectRef struct{ key, path, version, kind, name, uid, namespace string }
		items := []objectRef{
			{"namespace", "/api/v1/namespaces/borealis", "v1", "Namespace", "borealis", b.NamespaceUID, ""},
			{"service", "/api/v1/namespaces/borealis/services/api-backend", "v1", "Service", "api-backend", b.ServiceUID, "borealis"},
			{"slice", "/apis/discovery.k8s.io/v1/namespaces/borealis/endpointslices/" + b.EndpointSlice, "discovery.k8s.io/v1", "EndpointSlice", b.EndpointSlice, b.EndpointSliceUID, "borealis"},
		}
		for _, w := range []clusterAutomaticRecoveryWorkloadBinding{b.API, b.Scheduler} {
			items = append(items, objectRef{"deployments/" + w.Deployment, "/apis/apps/v1/namespaces/borealis/deployments/" + w.Deployment, "apps/v1", "Deployment", w.Deployment, w.DeploymentUID, "borealis"},
				objectRef{"replicasets/" + w.ReplicaSet, "/apis/apps/v1/namespaces/borealis/replicasets/" + w.ReplicaSet, "apps/v1", "ReplicaSet", w.ReplicaSet, w.ReplicaSetUID, "borealis"},
				objectRef{"pods/" + w.Pod, "/api/v1/namespaces/borealis/pods/" + w.Pod, "v1", "Pod", w.Pod, w.PodUID, "borealis"})
		}
		for _, item := range items {
			var object clusterNativeObservationObject
			if get(ctx, item.path, &object) != nil || ctx.Err() != nil || !recoveryApplicationExactFields(object) {
				return nil, errClusterConflict
			}
			id, ok := clusterSSHStorageMetadata(object, item.version, item.kind, item.namespace)
			if !ok || id.Name != item.name || id.UID != item.uid {
				return nil, errClusterConflict
			}
			objects[item.key] = object
		}
		if !recoveryApplicationWorkload(objects, b.API, "api-backend", b.Node.NodeName) || !recoveryApplicationWorkload(objects, b.Scheduler, "job-scheduler", b.Node.NodeName) || !recoveryApplicationEndpoint(objects, b) {
			return nil, errClusterConflict
		}
		return objects, nil
	}
	before, err := read()
	if err != nil {
		return fail()
	}
	for _, path := range []string{"/startup", "/live", "/ready"} {
		if probe(ctx, b.PodIP, b.Port, path) != nil || ctx.Err() != nil {
			return fail()
		}
	}
	if probe(ctx, b.ServiceIP, b.Port, "/ready") != nil || ctx.Err() != nil {
		return fail()
	}
	after, err := read()
	if err != nil || !reflect.DeepEqual(before, after) || ctx.Err() != nil {
		return fail()
	}
	return clusterAutomaticRecoveryHealth{NodeName: b.Node.NodeName, NodeUID: b.Node.NodeUID, Node: true, Application: true}, nil
}

func recoveryApplicationEndpoint(objects map[string]map[string]any, b clusterAutomaticRecoveryApplicationBinding) bool {
	pod, service, slice := objects["pods/"+b.API.Pod], objects["service"], objects["slice"]
	if clusterSSHStorageMap(pod, "status")["podIP"] != b.PodIP || clusterSSHStorageMap(service, "spec")["clusterIP"] != b.ServiceIP ||
		!recoveryApplicationSelector(clusterSSHStorageMap(clusterSSHStorageMap(service, "spec"), "selector"), clusterSSHStorageMap(clusterSSHStorageMap(pod, "metadata"), "labels")) ||
		clusterSSHStorageMap(clusterSSHStorageMap(slice, "metadata"), "labels")["kubernetes.io/service-name"] != "api-backend" || slice["addressType"] != "IPv4" {
		return false
	}
	refs, ok := clusterSSHStorageMap(slice, "metadata")["ownerReferences"].([]any)
	if !ok || len(refs) != 1 {
		return false
	}
	owner, ok := refs[0].(map[string]any)
	if !ok || owner["apiVersion"] != "v1" || owner["kind"] != "Service" || owner["name"] != "api-backend" || owner["uid"] != b.ServiceUID || owner["controller"] != true {
		return false
	}
	for _, ports := range []any{clusterSSHStorageMap(service, "spec")["ports"], slice["ports"]} {
		values, ok := ports.([]any)
		if !ok || len(values) != 1 {
			return false
		}
		port, ok := values[0].(map[string]any)
		n, integer := clusterSSHStorageInteger(port["port"])
		if !ok || !integer || n != int64(b.Port) || port["protocol"] != "TCP" {
			return false
		}
	}
	servicePort := clusterSSHStorageMap(service, "spec")["ports"].([]any)[0].(map[string]any)
	slicePort := slice["ports"].([]any)[0].(map[string]any)
	if servicePort["name"] != "http" || servicePort["targetPort"] != "http" || slicePort["name"] != "http" {
		return false
	}
	container := clusterSSHStorageMap(pod, "spec")["containers"].([]any)[0].(map[string]any)
	ports, ok := container["ports"].([]any)
	if !ok {
		return false
	}
	httpPorts := 0
	for _, raw := range ports {
		port, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		if port["name"] == "http" {
			n, valid := clusterSSHStorageInteger(port["containerPort"])
			if !valid || n != int64(b.Port) || port["protocol"] != "TCP" {
				return false
			}
			httpPorts++
		}
	}
	if httpPorts != 1 {
		return false
	}
	endpoints, ok := slice["endpoints"].([]any)
	if !ok || len(endpoints) == 0 || len(endpoints) > 100 {
		return false
	}
	found := 0
	for _, value := range endpoints {
		endpoint, ok := value.(map[string]any)
		if !ok {
			return false
		}
		target := clusterSSHStorageMap(endpoint, "targetRef")
		if endpoint["nodeName"] != b.Node.NodeName && target["uid"] != b.API.PodUID {
			continue
		}
		conditions := clusterSSHStorageMap(endpoint, "conditions")
		if endpoint["nodeName"] != b.Node.NodeName || target["kind"] != "Pod" || target["namespace"] != "borealis" || target["name"] != b.API.Pod || target["uid"] != b.API.PodUID ||
			conditions["ready"] != true || conditions["serving"] != true || conditions["terminating"] != false || !clusterSSHStorageStrings(endpoint["addresses"], b.PodIP) {
			return false
		}
		found++
	}
	return found == 1
}

func recoveryApplicationExactFields(value any) bool {
	switch object := value.(type) {
	case clusterNativeObservationObject:
		return recoveryApplicationExactFields(map[string]any(object))
	case map[string]any:
		if !clusterNativeObservationFieldNames(object, "apiVersion", "kind", "metadata", "name", "uid", "namespace", "resourceVersion", "deletionTimestamp", "generation", "labels", "spec", "status", "ownerReferences", "controller", "selector", "matchLabels", "template", "replicas", "observedGeneration", "readyReplicas", "availableReplicas", "updatedReplicas", "containers", "initContainers", "ephemeralContainers", "hostNetwork", "image", "imageID", "containerID", "nodeName", "phase", "conditions", "type", "ready", "started", "state", "running", "startedAt", "containerStatuses", "podIP", "clusterIP", "ports", "port", "targetPort", "containerPort", "protocol", "addressType", "endpoints", "targetRef", "addresses", "serving", "terminating") {
			return false
		}
		for _, child := range object {
			if !recoveryApplicationExactFields(child) {
				return false
			}
		}
	case []any:
		for _, child := range object {
			if !recoveryApplicationExactFields(child) {
				return false
			}
		}
	}
	return true
}
