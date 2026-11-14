package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Concrete matching observations, not an authorization bit. No source observer
// currently consumes this callable projection. Naming/resource policy, original
// authority lifetime, independent rereads and private broker validation must all
// be composed before completed rotations can enter a capacity observation.
type clusterSSHSiteWorkerCustody struct {
	PodName, PodUID, Node, Worker, ServiceName, ServiceUID, ServiceIP string
	SiteID, Generation, ProfileRank, OpsPort, DesktopPort             int64
	PodSHA256, ServiceSHA256, EndpointSHA256, RouteSHA256             string
}

// endpointSets contains a complete native EndpointSlice list for EACH Service
// in the independent operator inventory. Empty, missing, extra or duplicate
// joins fail. Global operator inventory must match the complete original source
// Pod set; foreign/unassigned objects never disappear through node filtering.
func joinClusterSSHSiteWorkerCustody(source clusterSSHSourceCohort, pods []map[string]any, inventory *operatorSiteWorkerInventory,
	endpointSets map[string][]map[string]any, routes []clusterSSHCurrentWorkerRoute) ([]clusterSSHSiteWorkerCustody, error) {
	fail := func() ([]clusterSSHSiteWorkerCustody, error) { return nil, clusterbootstrap.ErrPreparationConfig }
	if len(source.Members) < 1 || len(source.Members) > 2 || pods == nil || inventory == nil || !clusterSSHSiteWorkerInventoryValid(*inventory) ||
		endpointSets == nil || routes == nil || len(pods) != len(inventory.Pods) || len(pods) != len(inventory.Services) || len(pods) != len(routes) || len(endpointSets) != len(pods) {
		return fail()
	}
	nodes := map[string]bool{}
	for _, member := range source.Members {
		if !schedulerPlacementName(member.Name) || nodes[member.Name] {
			return fail()
		}
		nodes[member.Name] = true
	}
	operatorPods := map[string]operatorSiteWorkerInventoryPod{}
	services := map[string]operatorSiteWorkerInventoryService{}
	currentRoutes := map[string]clusterSSHCurrentWorkerRoute{}
	for _, pod := range inventory.Pods {
		operatorPods[pod.Metadata.Name] = pod
	}
	for _, service := range inventory.Services {
		services[service.Metadata.Name] = service
	}
	for _, route := range routes {
		derived, valid := clusterSSHCurrentWorkerRouteFromRow(route.observation)
		if !valid || !reflect.DeepEqual(derived, route) {
			return fail()
		}
		if _, duplicate := currentRoutes[route.Worker]; duplicate || route.Worker != schedulerK3sSiteWorkerGUID(route.SiteID) || route.Generation < 1 ||
			uint64(route.Generation) > clusterSSHCapacityLimit || route.ProfileRank < 0 || route.ProfileRank > 3 || !clusterSSHCustodyHash(route.SHA256) {
			return fail()
		}
		currentRoutes[route.Worker] = route
	}
	result := make([]clusterSSHSiteWorkerCustody, 0, len(pods))
	seenUID, seenWorker, seenService := map[string]bool{}, map[string]bool{}, map[string]bool{}
	seenSites := map[int64]bool{}
	for _, pod := range pods {
		projected, err := projectOperatorSiteWorkerInventory([]map[string]any{pod}, []map[string]any{}, "borealis")
		if err != nil || len(projected.Pods) != 1 {
			return fail()
		}
		native := projected.Pods[0]
		meta := native.Metadata
		observed, exists := operatorPods[meta.Name]
		if !exists || !reflect.DeepEqual(native, observed) || native.NodeName == nil || !nodes[*native.NodeName] || meta.DeletionTimestamp != nil || meta.HasOwnerReferences ||
			!clusterSSHCustodyWorkerLabels(meta.Labels) || seenUID[meta.UID] {
			return fail()
		}
		siteText, worker := meta.Labels["borealis.io/site-id"], meta.Labels["borealis.io/worker-guid"]
		site, err := strconv.ParseInt(siteText, 10, 64)
		if err != nil || site < 1 || uint64(site) > clusterSSHCapacityLimit || strconv.FormatInt(site, 10) != siteText || worker != schedulerK3sSiteWorkerGUID(site) || seenSites[site] || seenWorker[worker] {
			return fail()
		}
		serviceName := meta.Annotations["borealis.io/service-name"]
		service, exists := services[serviceName]
		if !exists || seenService[serviceName] || (serviceName != siteWorkerPodNameForSite(site) && serviceName != siteWorkerNameForSite(site, meta.Annotations["borealis.io/site-name"], worker)) ||
			service.Metadata.DeletionTimestamp != nil || service.Metadata.HasOwnerReferences || !clusterSSHCustodyWorkerLabels(service.Metadata.Labels) ||
			service.Metadata.Labels["borealis.io/site-id"] != siteText || service.Metadata.Labels["borealis.io/worker-guid"] != worker || service.ClusterIP == nil || seenUID[service.Metadata.UID] || service.Metadata.UID == meta.UID {
			return fail()
		}
		// Supported Service producer selects its seven identity labels and, after
		// rotation, exactly the current revision. No weakened/custom selectors.
		selector := map[string]string{}
		for _, key := range []string{"app.kubernetes.io/name", "app.kubernetes.io/part-of", "app.kubernetes.io/managed-by", "app.kubernetes.io/component", "borealis.io/workload", "borealis.io/site-id", "borealis.io/worker-guid"} {
			selector[key] = meta.Labels[key]
		}
		if revision, present := meta.Labels["borealis.io/redeploy-agent-revision"]; present {
			if revision == "" || !borealisOperatorKubernetesObjectNameAllowed(revision) {
				return fail()
			}
			selector["borealis.io/redeploy-agent-revision"] = revision
		}
		if !reflect.DeepEqual(selector, service.Selector) {
			return fail()
		}
		spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
		if status["phase"] != "Running" || !clusterSSHSiteWorkerPlacement(spec) || !clusterSSHCustodyPodReady(status) {
			return fail()
		}
		podIP, err := netip.ParseAddr(clusterSSHStorageText(status, "podIP"))
		if err != nil || podIP.String() != status["podIP"] || podIP.IsUnspecified() || podIP.IsMulticast() || podIP.IsLoopback() {
			return fail()
		}
		ports, ok := clusterSSHCustodyPorts(spec, service)
		if !ok {
			return fail()
		}
		route, exists := currentRoutes[worker]
		if !exists || route.SiteID != site || route.PodName != meta.Name || route.PodUID != meta.UID || route.Node != *native.NodeName ||
			route.Host != *service.ClusterIP || route.OpsPort != ports["remote-ops"] || route.DesktopPort != ports["remote-desktop"] {
			return fail()
		}
		endpoints, exists := endpointSets[serviceName]
		if !exists || len(endpoints) != 1 {
			return fail()
		}
		endpoint, endpointUID, ok := clusterSSHCustodyEndpoint(endpoints[0], service, native, podIP, ports)
		if !ok || seenUID[endpointUID] || endpointUID == meta.UID || endpointUID == service.Metadata.UID {
			return fail()
		}
		seenUID[meta.UID], seenUID[service.Metadata.UID], seenWorker[worker], seenSites[site], seenService[serviceName], seenUID[endpointUID] = true, true, true, true, true, true
		result = append(result, clusterSSHSiteWorkerCustody{PodName: meta.Name, PodUID: meta.UID, Node: *native.NodeName, Worker: worker,
			ServiceName: serviceName, ServiceUID: service.Metadata.UID, ServiceIP: *service.ClusterIP, SiteID: site, Generation: route.Generation, ProfileRank: route.ProfileRank,
			OpsPort: route.OpsPort, DesktopPort: route.DesktopPort, PodSHA256: native.SHA256, ServiceSHA256: service.SHA256, EndpointSHA256: endpoint, RouteSHA256: route.SHA256})
	}
	slices.SortFunc(result, func(a, b clusterSSHSiteWorkerCustody) int { return strings.Compare(a.PodName, b.PodName) })
	return result, nil
}

func clusterSSHCustodyHash(hash string) bool {
	raw, err := hex.DecodeString(hash)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == hash
}

func clusterSSHCustodyWorkerLabels(labels map[string]string) bool {
	if _, candidate := labels["borealis.io/redeploy-agent-candidate"]; candidate {
		return false
	}
	return labels["app.kubernetes.io/name"] == "site-worker" && labels["app.kubernetes.io/part-of"] == "borealis" && labels["app.kubernetes.io/managed-by"] == "borealis-operator" &&
		labels["app.kubernetes.io/component"] == "site-worker" && labels["borealis.io/workload"] == "site-worker"
}

func clusterSSHCustodyPodReady(status map[string]any) bool {
	conditions, ok := status["conditions"].([]any)
	if !ok || len(conditions) > clusterSSHSiteWorkerLimit {
		return false
	}
	ready := false
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		if condition["type"] == "Ready" {
			if ready || condition["status"] != "True" {
				return false
			}
			ready = true
		}
	}
	return ready
}

func clusterSSHCustodyPorts(spec map[string]any, service operatorSiteWorkerInventoryService) (map[string]int64, bool) {
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) != 1 || len(service.Ports) != 2 {
		return nil, false
	}
	container, ok := containers[0].(map[string]any)
	ports, portsOK := container["ports"].([]any)
	if !ok || container["name"] != "site-worker" || !portsOK || len(ports) != 2 {
		return nil, false
	}
	result := map[string]int64{}
	for _, raw := range ports {
		port, ok := raw.(map[string]any)
		name := clusterSSHStorageText(port, "name")
		number, valid := clusterSSHStorageInteger(port["containerPort"])
		if !ok || !valid || number < 1 || number > 65535 || (name != "remote-ops" && name != "remote-desktop") || result[name] != 0 || port["protocol"] != "TCP" {
			return nil, false
		}
		if host, exists := port["hostPort"]; exists && host != nil {
			return nil, false
		}
		result[name] = number
	}
	seen := map[string]bool{}
	for _, port := range service.Ports {
		if seen[port.Name] || result[port.Name] != port.Port || port.TargetPort != port.Name || port.Protocol != "TCP" {
			return nil, false
		}
		seen[port.Name] = true
	}
	return result, result["remote-ops"] != result["remote-desktop"]
}

func clusterSSHCustodyEndpoint(object map[string]any, service operatorSiteWorkerInventoryService, pod operatorSiteWorkerInventoryPod,
	ip netip.Addr, ports map[string]int64) (string, string, bool) {
	id, ok := clusterSSHStorageMetadata(object, "discovery.k8s.io/v1", "EndpointSlice", "borealis")
	meta := clusterSSHStorageMap(object, "metadata")
	labels := clusterSSHStorageMap(meta, "labels")
	refs, refsOK := meta["ownerReferences"].([]any)
	endpoints, endpointsOK := object["endpoints"].([]any)
	endpointPorts, portsOK := object["ports"].([]any)
	wantFamily := "IPv6"
	if ip.Is4() {
		wantFamily = "IPv4"
	}
	if !ok || labels["kubernetes.io/service-name"] != service.Metadata.Name || labels["endpointslice.kubernetes.io/managed-by"] != "endpointslice-controller.k8s.io" ||
		!refsOK || len(refs) != 1 || !endpointsOK || len(endpoints) != 1 || !portsOK || len(endpointPorts) != 2 || object["addressType"] != wantFamily {
		return "", "", false
	}
	owner, ok := refs[0].(map[string]any)
	if !ok || owner["apiVersion"] != "v1" || owner["kind"] != "Service" || owner["name"] != service.Metadata.Name || owner["uid"] != service.Metadata.UID || owner["controller"] != true {
		return "", "", false
	}
	endpoint, ok := endpoints[0].(map[string]any)
	conditions, target := clusterSSHStorageMap(endpoint, "conditions"), clusterSSHStorageMap(endpoint, "targetRef")
	if !ok || conditions["ready"] != true || !clusterSSHStorageStrings(endpoint["addresses"], ip.String()) || endpoint["nodeName"] != *pod.NodeName ||
		target["kind"] != "Pod" || target["namespace"] != "borealis" || target["name"] != pod.Metadata.Name || target["uid"] != pod.Metadata.UID {
		return "", "", false
	}
	if api, present := target["apiVersion"]; present && api != "v1" {
		return "", "", false
	}
	if serving, present := conditions["serving"]; present && serving != true {
		return "", "", false
	}
	if terminating, present := conditions["terminating"]; present && terminating != false {
		return "", "", false
	}
	seen := map[string]bool{}
	for _, raw := range endpointPorts {
		port, ok := raw.(map[string]any)
		name := clusterSSHStorageText(port, "name")
		number, valid := clusterSSHStorageInteger(port["port"])
		if !ok || !valid || number == 0 || ports[name] != number || seen[name] || port["protocol"] != "TCP" {
			return "", "", false
		}
		seen[name] = true
	}
	raw, err := json.Marshal(object)
	if err != nil || len(raw) > 2<<20 || !operatorSiteWorkerInventoryExactValues(object) {
		return "", "", false
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), id.UID, true
}
