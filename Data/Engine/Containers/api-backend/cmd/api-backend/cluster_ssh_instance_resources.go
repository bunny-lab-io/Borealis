package main

import "strconv"

// One V1/aio instance manager per joining node. CPU policy is retained without
// inventing a target CPU request: target allocatable/override evidence is still
// required. Only the verified memory cap contributes to this partial subtotal.
type clusterSSHInstanceManagerResources struct {
	MemoryRequestBytes uint64 `json:"memory_request_bytes"`
	MemoryLimitBytes   uint64 `json:"memory_limit_bytes"`
	CPULimitMilli      uint64 `json:"cpu_limit_milli"`
	CPUPercentage      string `json:"cpu_percentage"`
}

func clusterSSHInstancePercentage(value string) (float64, bool) {
	if !clusterSSHInstancePercentageRE.MatchString(value) {
		return 0, false
	}
	percentage, err := strconv.ParseFloat(value, 64)
	return percentage, err == nil && percentage >= 0 && percentage <= 40
}

func (v clusterSSHInstanceManagerResources) valid() bool {
	_, ok := clusterSSHInstancePercentage(v.CPUPercentage)
	return ok && v.MemoryRequestBytes > 0 && v.MemoryRequestBytes <= v.MemoryLimitBytes &&
		v.MemoryLimitBytes <= clusterSSHCapacityLimit && v.CPULimitMilli > 0 && v.CPULimitMilli <= clusterSSHCapacityLimit
}
