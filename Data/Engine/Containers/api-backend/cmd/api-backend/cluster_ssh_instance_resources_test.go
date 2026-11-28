package main

import (
	"context"
	"testing"
)

func sshInstanceResourceValue() clusterSSHInstanceManagerResources {
	return clusterSSHInstanceManagerResources{MemoryRequestBytes: 64 << 20, MemoryLimitBytes: 128 << 20, CPULimitMilli: 1500, CPUPercentage: "12"}
}

func TestClusterSSHInstanceMemoryBudget(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		f := newSSHStorageFixture(t, replacement)
		observed, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
		if err != nil || observed.Requirements.LonghornInstanceManagerResources != sshInstanceResourceValue() {
			t.Fatalf("source protection projection missing: %v", err)
		}
		cpu, memory, ok := clusterSSHNodeWorkloadBudget(observed.Requirements)
		if !ok {
			t.Fatal("valid subtotal rejected")
		}
		// Source-only CPU overrides alter observed Pod requests, not target policy.
		for i, member := range f.a.Source.Members {
			clusterSSHStorageMap(f.objects[clusterSSHInstanceNodePrefix+member.Name], "spec")["instanceManagerCPURequest"] = 500
			container := clusterSSHStorageMap(sshInstancePod(f, i), "spec")["containers"].([]any)[0].(map[string]any)
			clusterSSHStorageMap(clusterSSHStorageMap(container, "resources"), "requests")["cpu"] = "500m"
		}
		changed, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
		if err != nil || changed.Requirements.LonghornInstanceManagerResources != observed.Requirements.LonghornInstanceManagerResources || changed.Requirements.observation == observed.Requirements.observation {
			t.Fatalf("source override copied or receipt lost: %v", err)
		}
		// Exactly one memory cap per target, independent of active-source count.
		changed.Requirements.LonghornInstanceManagerResources.MemoryLimitBytes++
		nextCPU, nextMemory, ok := clusterSSHNodeWorkloadBudget(changed.Requirements)
		if !ok || nextCPU != cpu || nextMemory != memory+1 {
			t.Fatal("memory cap omitted/multiplied or CPU invented")
		}
	}
}

func TestClusterSSHInstanceProjectionRejectsInvalidBudget(t *testing.T) {
	f := newSSHStorageFixture(t, false)
	observed, err := observeClusterSSHStorage(context.Background(), f.a.Source, f.get)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := clusterSSHNodeWorkloadBudget(observed.Requirements); !ok {
		t.Fatal("baseline rejected")
	}
	for _, mode := range []string{"missing", "request zero", "request above cap", "memory overflow", "aggregate overflow", "CPU zero", "CPU overflow", "percentage missing", "percentage overflow", "percentage negative", "percentage NaN", "percentage precision"} {
		t.Run(mode, func(t *testing.T) {
			r := observed.Requirements
			v := &r.LonghornInstanceManagerResources
			switch mode {
			case "missing":
				*v = clusterSSHInstanceManagerResources{}
			case "request zero":
				v.MemoryRequestBytes = 0
			case "request above cap":
				v.MemoryRequestBytes = v.MemoryLimitBytes + 1
			case "memory overflow":
				v.MemoryLimitBytes = clusterSSHCapacityLimit + 1
			case "aggregate overflow":
				v.MemoryLimitBytes = clusterSSHCapacityLimit
			case "CPU zero":
				v.CPULimitMilli = 0
			case "CPU overflow":
				v.CPULimitMilli = clusterSSHCapacityLimit + 1
			case "percentage missing":
				v.CPUPercentage = ""
			case "percentage overflow":
				v.CPUPercentage = "40.000001"
			case "percentage negative":
				v.CPUPercentage = "-1"
			case "percentage NaN":
				v.CPUPercentage = "NaN"
			case "percentage precision":
				v.CPUPercentage = "12.0000001"
			}
			if _, _, ok := clusterSSHNodeWorkloadBudget(r); ok {
				t.Fatal("invalid instance-manager projection accepted")
			}
		})
	}
}
