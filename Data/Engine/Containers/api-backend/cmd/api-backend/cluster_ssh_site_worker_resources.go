package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"slices"
	"strconv"
	"strings"
)

const clusterSSHSiteWorkerPodsPrefix = "/api/v1/namespaces/borealis/pods?labelSelector=app.kubernetes.io%2Fname%3Dsite-worker&fieldSelector=spec.nodeName%3D"
const clusterSSHSiteWorkerPodsSuffix = "&limit=256"

// Collection/transport bound, not a sizing minimum or desired replica count.
const clusterSSHSiteWorkerLimit = 256

type clusterSSHSiteWorkerResource struct {
	Name      string                        `json:"name"`
	UID       string                        `json:"uid"`
	Node      string                        `json:"node"`
	Worker    string                        `json:"worker"`
	SiteID    int64                         `json:"site_id"`
	Resources clusterSSHApplicationResource `json:"resources"`
}

func clusterSSHSiteWorkerPodsPath(node string) string {
	return clusterSSHSiteWorkerPodsPrefix + node + clusterSSHSiteWorkerPodsSuffix
}

func clusterSSHSiteWorkerPodsPathValid(path string) bool {
	node, ok := strings.CutPrefix(path, clusterSSHSiteWorkerPodsPrefix)
	if !ok {
		return false
	}
	node, ok = strings.CutSuffix(node, clusterSSHSiteWorkerPodsSuffix)
	return ok && clusterSSHStorageName(node)
}

// Bare operator Pods can move to any active application node. Charge the whole
// observed active-source set to each target, without assuming even spreading or
// creating one worker replica per node. Missing-worker reconstruction and the
// complete desired worker census remain separate readiness evidence.
func clusterSSHSiteWorkerBudget(values []clusterSSHSiteWorkerResource) (cpu, memory uint64, ok bool) {
	if values == nil || len(values) > clusterSSHSiteWorkerLimit {
		return 0, 0, false
	}
	uids, workers := map[string]bool{}, map[string]bool{}
	previous := ""
	for _, v := range values {
		if !borealisOperatorKubernetesNameAllowed(v.Name) || !strings.HasPrefix(v.Name, siteWorkerNamePrefix) || v.Name <= previous ||
			!vipScopeUUID(v.UID) || uids[v.UID] || !clusterSSHStorageName(v.Node) ||
			!borealisOperatorKubernetesNameAllowed(v.Worker) || workers[v.Worker] || v.SiteID <= 0 || uint64(v.SiteID) > clusterSSHCapacityLimit ||
			v.Resources.Name != "site-worker" || !clusterSSHWorkloadResourceValid(v.Resources) {
			return 0, 0, false
		}
		previous, uids[v.UID], workers[v.Worker] = v.Name, true, true
		cpu, ok = clusterSSHCapacitySum(cpu, v.Resources.CPURequestMilli)
		if !ok {
			return 0, 0, false
		}
		memory, ok = clusterSSHCapacitySum(memory, v.Resources.MemoryLimitBytes)
		if !ok {
			return 0, 0, false
		}
	}
	return cpu, memory, true
}

func clusterSSHSiteWorkersMatchSource(values []clusterSSHSiteWorkerResource, source clusterSSHSourceCohort) bool {
	if _, _, ok := clusterSSHSiteWorkerBudget(values); !ok {
		return false
	}
	for _, v := range values {
		if !slices.ContainsFunc(source.Members, func(member clusterSSHSourceMember) bool { return member.Name == v.Node }) {
			return false
		}
	}
	return true
}

func observeClusterSSHSiteWorkerResources(readList func(string, string, string, int) ([]map[string]any, error), source clusterSSHSourceCohort) ([]clusterSSHSiteWorkerResource, error) {
	fail := func() ([]clusterSSHSiteWorkerResource, error) { return nil, clusterbootstrap.ErrPreparationConfig }
	if readList == nil || len(source.Members) < 1 || len(source.Members) > 2 {
		return fail()
	}
	values := make([]clusterSSHSiteWorkerResource, 0)
	nodes := map[string]bool{}
	for _, member := range source.Members {
		if !clusterSSHStorageName(member.Name) || nodes[member.Name] {
			return fail()
		}
		nodes[member.Name] = true
		pods, err := readList(clusterSSHSiteWorkerPodsPath(member.Name), "Pod", "borealis", clusterSSHSiteWorkerLimit)
		if err != nil || len(pods) > clusterSSHSiteWorkerLimit-len(values) {
			return fail()
		}
		for _, pod := range pods {
			id, valid := clusterSSHStorageMetadata(pod, "v1", "Pod", "borealis")
			meta, spec, status := clusterSSHStorageMap(pod, "metadata"), clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
			labels, annotations := clusterSSHStorageMap(meta, "labels"), clusterSSHStorageMap(meta, "annotations")
			worker := clusterSSHStorageText(labels, "borealis.io/worker-guid")
			siteText := clusterSSHStorageText(labels, "borealis.io/site-id")
			site, siteErr := strconv.ParseInt(siteText, 10, 64)
			if !valid || siteErr != nil || strconv.FormatInt(site, 10) != siteText ||
				labels["app.kubernetes.io/name"] != "site-worker" || labels["app.kubernetes.io/part-of"] != "borealis" ||
				labels["app.kubernetes.io/managed-by"] != "borealis-operator" || labels["app.kubernetes.io/component"] != "site-worker" || labels["borealis.io/workload"] != "site-worker" ||
				id.Name != siteWorkerNameForSite(site, clusterSSHStorageText(annotations, "borealis.io/site-name"), worker) || annotations["borealis.io/service-name"] != id.Name ||
				!clusterSSHStorageEmptyList(meta["ownerReferences"]) || spec["nodeName"] != member.Name ||
				!clusterSSHSiteWorkerPlacement(spec) || status["phase"] != "Running" {
				return fail()
			}
			containers, ok := spec["containers"].([]any)
			if !ok || len(containers) != 1 {
				return fail()
			}
			container, ok := containers[0].(map[string]any)
			resources, resourcesOK := clusterSSHWorkloadResources("site-worker", container["resources"])
			if !ok || !resourcesOK || !clusterSSHBoundedPodResources(resources, pod, "site-worker", "prepare-site-worker-logs") {
				return fail()
			}
			values = append(values, clusterSSHSiteWorkerResource{Name: id.Name, UID: id.UID, Node: member.Name, Worker: worker, SiteID: site, Resources: resources})
		}
	}
	slices.SortFunc(values, func(a, b clusterSSHSiteWorkerResource) int { return strings.Compare(a.Name, b.Name) })
	if !clusterSSHSiteWorkersMatchSource(values, source) {
		return fail()
	}
	return values, nil
}

// The current producer has exactly one required active-node expression. Refuse
// unaccounted selectors/affinities/spread/schedulers instead of guessing where a
// source worker could be recreated. Current nodeName is separately source-bound.
func clusterSSHSiteWorkerPlacement(spec map[string]any) bool {
	affinity := clusterSSHStorageMap(spec, "affinity")
	node := clusterSSHStorageMap(affinity, "nodeAffinity")
	required := clusterSSHStorageMap(node, "requiredDuringSchedulingIgnoredDuringExecution")
	terms, ok := required["nodeSelectorTerms"].([]any)
	if len(affinity) != 1 || len(node) != 1 || len(required) != 1 || !ok || len(terms) != 1 ||
		!clusterSSHStorageEmptyMap(spec["nodeSelector"]) || !clusterSSHStorageEmptyList(spec["topologySpreadConstraints"]) ||
		!(clusterSSHStorageEmptyText(spec["schedulerName"]) || spec["schedulerName"] == "default-scheduler") {
		return false
	}
	term, ok := terms[0].(map[string]any)
	expressions, expressionsOK := term["matchExpressions"].([]any)
	if !ok || len(term) != 1 || !expressionsOK || len(expressions) != 1 {
		return false
	}
	expression, ok := expressions[0].(map[string]any)
	return ok && len(expression) == 3 && expression["key"] == "borealis.io/application-state" && expression["operator"] == "In" && clusterSSHStorageStrings(expression["values"], "active")
}
