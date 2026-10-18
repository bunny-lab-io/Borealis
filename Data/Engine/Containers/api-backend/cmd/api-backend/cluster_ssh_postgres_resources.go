package main

// The pinned CNPG controller gives postgres and its sequential bootstrap init
// the same Cluster resources. Require that exact effective contract on every
// owned active-member Pod; missing declarations cannot imply bounded demand.
func observeClusterSSHPostgresResources(clusterSpec, pod map[string]any) (clusterSSHApplicationResource, bool) {
	v, ok := clusterSSHWorkloadResources("postgres", clusterSpec["resources"])
	if !ok || !clusterSSHBoundedPodResources(v, pod, "postgres", "bootstrap-controller") {
		return clusterSSHApplicationResource{}, false
	}
	return v, true
}

// Supported workloads use one ordinary container and optionally one sequential
// init with the same envelope. Keep resize/status accounting identical.
func clusterSSHBoundedPodResources(v clusterSSHApplicationResource, pod map[string]any, mainName, initName string) bool {
	fail := func() bool { return false }
	spec, status := clusterSSHStorageMap(pod, "spec"), clusterSSHStorageMap(pod, "status")
	if !clusterSSHWorkloadResourceValid(v) || !clusterSSHStorageEmptyList(spec["ephemeralContainers"]) || !clusterSSHStorageEmptyMap(spec["resources"]) ||
		!clusterSSHStorageEmptyMap(spec["overhead"]) || !clusterSSHStorageEmptyText(spec["runtimeClassName"]) ||
		!clusterSSHStorageEmptyList(spec["resourceClaims"]) || !clusterSSHStorageEmptyText(status["resize"]) {
		return fail()
	}
	conditions, conditionsOK := status["conditions"].([]any)
	if !conditionsOK {
		return fail()
	}
	for _, raw := range conditions {
		condition, valid := raw.(map[string]any)
		if !valid || ((condition["type"] == "PodResizePending" || condition["type"] == "PodResizeInProgress") && condition["status"] != "False") {
			return fail()
		}
	}
	for _, kind := range []struct{ spec, status, name string }{
		{"containers", "containerStatuses", mainName},
		{"initContainers", "initContainerStatuses", initName},
	} {
		if kind.name == "" {
			if !clusterSSHStorageEmptyList(spec[kind.spec]) || !clusterSSHStorageEmptyList(status[kind.status]) {
				return fail()
			}
			continue
		}
		containers, containersOK := spec[kind.spec].([]any)
		statuses, statusesOK := status[kind.status].([]any)
		if !containersOK || len(containers) != 1 || !statusesOK || len(statuses) != 1 {
			return fail()
		}
		container, containerOK := containers[0].(map[string]any)
		runtime, runtimeOK := statuses[0].(map[string]any)
		observed, resourcesOK := clusterSSHWorkloadResources(v.Name, container["resources"])
		if !containerOK || !runtimeOK || container["name"] != kind.name || runtime["name"] != kind.name || !resourcesOK || observed != v ||
			!clusterSSHStorageEmptyText(container["restartPolicy"]) || !clusterSSHStorageEmptyList(container["resizePolicy"]) ||
			!clusterSSHStorageEmptyList(runtime["allocatedResourcesStatus"]) {
			return fail()
		}
		// In-place resize evidence must agree with the declared resources. Never
		// substitute a smaller request for resources still allocated by kubelet.
		if current, present := runtime["resources"]; present {
			actual, valid := clusterSSHWorkloadResources(v.Name, current)
			if !valid || actual != v {
				return fail()
			}
		}
		if allocated, present := runtime["allocatedResources"]; present {
			requests, valid := allocated.(map[string]any)
			cpu, cpuOK := clusterSSHWorkloadMilliCPU(requests["cpu"])
			memory, memoryOK := clusterSSHWorkloadMemory(requests["memory"])
			if !valid || len(requests) != 2 || !cpuOK || !memoryOK || cpu != v.CPURequestMilli || memory != v.MemoryRequestBytes {
				return fail()
			}
		}
	}
	return true
}

func clusterSSHWorkloadResources(name string, raw any) (clusterSSHApplicationResource, bool) {
	resources, valid := raw.(map[string]any)
	requests, limits := clusterSSHStorageMap(resources, "requests"), clusterSSHStorageMap(resources, "limits")
	v := clusterSSHApplicationResource{Name: name}
	var cpuRequestOK, cpuLimitOK, memoryRequestOK, memoryLimitOK bool
	v.CPURequestMilli, cpuRequestOK = clusterSSHWorkloadMilliCPU(requests["cpu"])
	v.CPULimitMilli, cpuLimitOK = clusterSSHWorkloadMilliCPU(limits["cpu"])
	v.MemoryRequestBytes, memoryRequestOK = clusterSSHWorkloadMemory(requests["memory"])
	v.MemoryLimitBytes, memoryLimitOK = clusterSSHWorkloadMemory(limits["memory"])
	return v, valid && len(resources) == 2 && len(requests) == 2 && len(limits) == 2 &&
		cpuRequestOK && cpuLimitOK && memoryRequestOK && memoryLimitOK && clusterSSHWorkloadResourceValid(v)
}

func clusterSSHWorkloadResourceValid(v clusterSSHApplicationResource) bool {
	return v.CPURequestMilli > 0 && v.CPURequestMilli <= v.CPULimitMilli && v.CPULimitMilli <= clusterSSHCapacityLimit &&
		v.MemoryRequestBytes > 0 && v.MemoryRequestBytes <= v.MemoryLimitBytes && v.MemoryLimitBytes <= clusterSSHCapacityLimit
}

func clusterSSHNodeWorkloadBudget(r clusterSSHStorageRequirements) (cpu, memory uint64, ok bool) {
	cpu, memory, ok = clusterSSHApplicationBudget(r.Applications)
	if !ok || r.PostgresResources.Name != "postgres" || !clusterSSHWorkloadResourceValid(r.PostgresResources) {
		return 0, 0, false
	}
	// One planned instance per target. Bootstrap and postgres run sequentially
	// with identical resources, so max(init, postgres) equals one instance.
	cpu, ok = clusterSSHCapacitySum(cpu, r.PostgresResources.CPURequestMilli)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, r.PostgresResources.MemoryLimitBytes)
	if !ok {
		return 0, 0, false
	}
	operatorCPU, operatorMemory, valid := r.CNPGOperatorResources.budget()
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, operatorCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, operatorMemory)
	if !ok {
		return 0, 0, false
	}
	upgradeCPU, upgradeMemory, valid := r.SystemUpgradeResources.budget()
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, upgradeCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, upgradeMemory)
	if !ok {
		return 0, 0, false
	}
	snapshotCPU, snapshotMemory, valid := r.SnapshotResources.budget()
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, snapshotCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, snapshotMemory)
	if !ok {
		return 0, 0, false
	}
	certManagerCPU, certManagerMemory, valid := r.CertManagerResources.budget("cert-manager-controller")
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, certManagerCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, certManagerMemory)
	if !ok {
		return 0, 0, false
	}
	webhookCPU, webhookMemory, valid := r.CertManagerWebhookResources.budget("cert-manager-webhook")
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, webhookCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, webhookMemory)
	if !ok {
		return 0, 0, false
	}
	cainjectorCPU, cainjectorMemory, valid := r.CertManagerCainjectorResources.budget("cert-manager-cainjector")
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, cainjectorCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, cainjectorMemory)
	if !ok {
		return 0, 0, false
	}
	vipCPU, vipMemory, valid := r.KubeVIP.budget()
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, vipCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, vipMemory)
	if !ok {
		return 0, 0, false
	}
	managerCPU, managerMemory, valid := r.LonghornManagerResources.budget()
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, managerCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, managerMemory)
	if !ok {
		return 0, 0, false
	}
	uiCPU, uiMemory, valid := r.LonghornUIResources.budget()
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, uiCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, uiMemory)
	if !ok {
		return 0, 0, false
	}
	driverCPU, driverMemory, valid := r.LonghornDriverResources.budget()
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, driverCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, driverMemory)
	if !ok {
		return 0, 0, false
	}
	workerCPU, workerMemory, valid := clusterSSHSiteWorkerBudget(r.SiteWorkers)
	if !valid {
		return 0, 0, false
	}
	cpu, ok = clusterSSHCapacitySum(cpu, workerCPU)
	if !ok {
		return 0, 0, false
	}
	memory, ok = clusterSSHCapacitySum(memory, workerMemory)
	return cpu, memory, ok
}
