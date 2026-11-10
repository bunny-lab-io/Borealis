package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func sshPreparationTargetCapacityFixture(t *testing.T) (clusterSSHPreparationSnapshot, clusterbootstrap.PreparationTargetCPU, map[string]any) {
	t.Helper()
	_, _, snapshot := sshBrokerFixture(t)
	plan, err := clusterbootstrap.PreparationTargetCPUPlanDigest(snapshot.Expected.K3sVersion)
	if err != nil {
		t.Fatal(err)
	}
	cpu := clusterbootstrap.PreparationTargetCPU{Version: 1, PlanSHA256: plan, OnlineCPUs: "0-7", AllocatableMilli: 8000, MemoryKiB: 67108864, ObservationSHA256: strings.Repeat("e", 64)}
	node := sshStorageObject("longhorn.io/v1beta2", "Node", "longhorn-system", "engine-02")
	node["spec"] = map[string]any{"instanceManagerCPURequest": json.Number("0")}
	return snapshot, cpu, node
}

func sshPreparationTargetCapacityRead(t *testing.T, node map[string]any) func(string) (map[string]any, error) {
	t.Helper()
	return func(path string) (map[string]any, error) {
		if path != clusterSSHInstanceNodePrefix+"engine-02" {
			t.Fatal("capacity calculation escaped named target read", path)
		}
		return node, nil
	}
}

func TestClusterSSHPreparationTargetCapacitySubtotal(t *testing.T) {
	for _, mode := range []string{"present default", "absent default", "own override", "preclaim"} {
		t.Run(mode, func(t *testing.T) {
			snapshot, cpu, node := sshPreparationTargetCapacityFixture(t)
			read := sshPreparationTargetCapacityRead(t, node)
			wantLonghorn := uint64(960)
			if mode == "preclaim" {
				snapshot.Expected = clusterbootstrap.PreparationExpected{}
			}
			if mode == "absent default" {
				read = func(path string) (map[string]any, error) {
					if path != clusterSSHInstanceNodePrefix+"engine-02" {
						t.Fatal("absence read escaped target")
					}
					return nil, errClusterSSHPreparationLonghornNodeAbsent
				}
			} else if mode == "own override" {
				clusterSSHStorageMap(node, "spec")["instanceManagerCPURequest"] = json.Number("1000")
				wantLonghorn = 1000
			}
			got, err := calculateClusterSSHPreparationTargetCapacity(snapshot, cpu, "engine-02", read)
			// Existing complete subtotal fixture: application overlap, observed
			// worker plus its rotation, infrastructure and one instance memory cap.
			if err != nil || got.CPURequestMilli != 4680+wantLonghorn || got.MemoryLimitBytes != 10920<<20 ||
				got.HostMemoryReserveBytes != clusterSSHHostMemoryReserveBytes || got.LonghornCPU.RequestMilli != wantLonghorn ||
				got.LonghornCPU.Present != (mode != "absent default") || !validClusterSSHPreparationDigest(got.LonghornCPU.ObservationSHA256) {
				t.Fatal("target capacity subtotal changed or double-counted resources", got, err)
			}
		})
	}
}

func TestClusterSSHPreparationTargetCapacityExactFit(t *testing.T) {
	snapshot, cpu, node := sshPreparationTargetCapacityFixture(t)
	// Include passive/missing workers from the existing full desired census.
	for site := int64(1); site <= 20; site++ {
		snapshot.DesiredWorkerSites = append(snapshot.DesiredWorkerSites, site)
	}
	baseCPU, baseMemory, ok := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
	if !ok || baseCPU >= cpu.AllocatableMilli || cpu.AllocatableMilli-baseCPU > snapshot.Storage.Requirements.LonghornInstanceManagerResources.CPULimitMilli {
		t.Fatal("exact-fit fixture no longer uses an allowed target override", baseCPU, baseMemory, ok)
	}
	// Memory input has KiB precision. Adjust only a synthetic observed cap by
	// its sub-KiB remainder to exercise equality without changing production values.
	snapshot.Storage.Requirements.LonghornInstanceManagerResources.MemoryLimitBytes += (1024 - (baseMemory+clusterSSHHostMemoryReserveBytes)%1024) % 1024
	baseCPU, baseMemory, ok = clusterSSHDesiredWorkerWorkloadBudget(snapshot)
	if !ok {
		t.Fatal("aligned fixture budget invalid")
	}
	override := cpu.AllocatableMilli - baseCPU
	clusterSSHStorageMap(node, "spec")["instanceManagerCPURequest"] = json.Number(strconv.FormatUint(override, 10))
	cpu.MemoryKiB = (baseMemory + clusterSSHHostMemoryReserveBytes) / 1024
	read := sshPreparationTargetCapacityRead(t, node)
	got, err := calculateClusterSSHPreparationTargetCapacity(snapshot, cpu, "engine-02", read)
	if err != nil || got.CPURequestMilli != cpu.AllocatableMilli || got.MemoryLimitBytes+got.HostMemoryReserveBytes != cpu.MemoryKiB*1024 {
		t.Fatal("exact CPU and memory fit rejected", got, err)
	}
	clusterSSHStorageMap(node, "spec")["instanceManagerCPURequest"] = json.Number(strconv.FormatUint(override+1, 10))
	if got, err := calculateClusterSSHPreparationTargetCapacity(snapshot, cpu, "engine-02", read); !errors.Is(err, clusterbootstrap.ErrPreparationConfig) || got != (clusterSSHPreparationTargetCapacity{}) {
		t.Fatal("one milliCPU above native allocation accepted", got, err)
	}
	clusterSSHStorageMap(node, "spec")["instanceManagerCPURequest"] = json.Number(strconv.FormatUint(override, 10))
	cpu.MemoryKiB--
	if got, err := calculateClusterSSHPreparationTargetCapacity(snapshot, cpu, "engine-02", read); !errors.Is(err, clusterbootstrap.ErrPreparationConfig) || got != (clusterSSHPreparationTargetCapacity{}) {
		t.Fatal("one KiB below existing reserve plus workload caps accepted", got, err)
	}
	cpu.MemoryKiB++
	snapshot.DesiredWorkerSites = append(snapshot.DesiredWorkerSites, 21)
	if got, err := calculateClusterSSHPreparationTargetCapacity(snapshot, cpu, "engine-02", read); !errors.Is(err, clusterbootstrap.ErrPreparationConfig) || got != (clusterSSHPreparationTargetCapacity{}) {
		t.Fatal("larger passive-worker census reused previous fit", got, err)
	}
}

func TestClusterSSHPreparationTargetCapacityRejectsIncompleteInputs(t *testing.T) {
	for name, mutate := range map[string]func(*clusterSSHPreparationSnapshot, *clusterbootstrap.PreparationTargetCPU, map[string]any){
		"unknown source receipt": func(s *clusterSSHPreparationSnapshot, _ *clusterbootstrap.PreparationTargetCPU, _ map[string]any) {
			s.Observation = strings.Repeat("0", 64)
		},
		"missing source receipt": func(s *clusterSSHPreparationSnapshot, _ *clusterbootstrap.PreparationTargetCPU, _ map[string]any) {
			s.Observation = ""
		},
		"missing workload receipt": func(s *clusterSSHPreparationSnapshot, _ *clusterbootstrap.PreparationTargetCPU, _ map[string]any) {
			s.Storage.Observation = ""
		},
		"unknown worker profile": func(s *clusterSSHPreparationSnapshot, _ *clusterbootstrap.PreparationTargetCPU, _ map[string]any) {
			s.Storage.Requirements.WorkerPolicy.Profile = "unknown"
		},
		"missing census": func(s *clusterSSHPreparationSnapshot, _ *clusterbootstrap.PreparationTargetCPU, _ map[string]any) {
			s.DesiredWorkerSites = nil
		},
		"duplicate census": func(s *clusterSSHPreparationSnapshot, _ *clusterbootstrap.PreparationTargetCPU, _ map[string]any) {
			s.DesiredWorkerSites = []int64{1, 1}
		},
		"unknown instance policy": func(s *clusterSSHPreparationSnapshot, _ *clusterbootstrap.PreparationTargetCPU, _ map[string]any) {
			s.Storage.Requirements.LonghornInstanceManagerResources = clusterSSHInstanceManagerResources{}
		},
		"memory sum overflow": func(s *clusterSSHPreparationSnapshot, _ *clusterbootstrap.PreparationTargetCPU, _ map[string]any) {
			s.Storage.Requirements.LonghornInstanceManagerResources.MemoryLimitBytes = clusterSSHCapacityLimit
		},
		"missing native memory": func(_ *clusterSSHPreparationSnapshot, c *clusterbootstrap.PreparationTargetCPU, _ map[string]any) {
			c.MemoryKiB = 0
		},
		"unbound native CPU": func(_ *clusterSSHPreparationSnapshot, c *clusterbootstrap.PreparationTargetCPU, _ map[string]any) {
			c.ObservationSHA256 = strings.Repeat("0", 64)
		},
		"own override above cap": func(s *clusterSSHPreparationSnapshot, _ *clusterbootstrap.PreparationTargetCPU, n map[string]any) {
			clusterSSHStorageMap(n, "spec")["instanceManagerCPURequest"] = json.Number(strconv.FormatUint(s.Storage.Requirements.LonghornInstanceManagerResources.CPULimitMilli+1, 10))
		},
		"source override substituted": func(_ *clusterSSHPreparationSnapshot, _ *clusterbootstrap.PreparationTargetCPU, n map[string]any) {
			clusterSSHStorageMap(n, "metadata")["name"] = "engine-01"
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot, cpu, node := sshPreparationTargetCapacityFixture(t)
			mutate(&snapshot, &cpu, node)
			got, err := calculateClusterSSHPreparationTargetCapacity(snapshot, cpu, "engine-02", sshPreparationTargetCapacityRead(t, node))
			if !errors.Is(err, clusterbootstrap.ErrPreparationConfig) || got != (clusterSSHPreparationTargetCapacity{}) {
				t.Fatal("incomplete target capacity returned a subtotal", got, err)
			}
		})
	}
	snapshot, cpu, _ := sshPreparationTargetCapacityFixture(t)
	for _, read := range []func(string) (map[string]any, error){nil, func(string) (map[string]any, error) { return nil, errors.New("denied") }} {
		if got, err := calculateClusterSSHPreparationTargetCapacity(snapshot, cpu, "engine-02", read); !errors.Is(err, clusterbootstrap.ErrPreparationConfig) || got != (clusterSSHPreparationTargetCapacity{}) {
			t.Fatal("unavailable own-target Longhorn observation accepted", got, err)
		}
	}
}

func TestClusterSSHPreparationTargetCapacityAllProfiles(t *testing.T) {
	for rank, profile := range []string{"homelab", "small-business", "msp", "enterprise"} {
		t.Run(profile, func(t *testing.T) {
			settings, applications, postgres, limits := sshRenderedProfileContract(t, profile, rank, [4]string{"12295", "24583", "49159", "81927"}[rank])
			snapshot, cpu, node := sshPreparationTargetCapacityFixture(t)
			snapshot.Settings = settings
			snapshot.Storage.Requirements.Applications = applications
			snapshot.Storage.Requirements.PostgresResources = postgres
			snapshot.Storage.Requirements.WorkerPolicy.Limits = limits
			snapshot.DesiredWorkerSites = []int64{1, 2}
			configuration, err := clusterbootstrap.NewPreparationConfiguration(snapshot.Expected, settings)
			if err != nil {
				t.Fatal("rendered profile configuration rejected", err)
			}
			if _, _, err := clusterSSHProfileApplications(configuration); err != nil {
				t.Fatal("unvalidated profile fixture", err)
			}
			cpu.OnlineCPUs, cpu.AllocatableMilli, cpu.MemoryKiB = "0-63", 64000, 268435456
			clusterSSHStorageMap(node, "spec")["instanceManagerCPURequest"] = json.Number("500")
			baseCPU, baseMemory, ok := clusterSSHDesiredWorkerWorkloadBudget(snapshot)
			got, err := calculateClusterSSHPreparationTargetCapacity(snapshot, cpu, "engine-02", sshPreparationTargetCapacityRead(t, node))
			if !ok || err != nil || got.CPURequestMilli != baseCPU+500 || got.MemoryLimitBytes != baseMemory || got.HostMemoryReserveBytes != clusterSSHHostMemoryReserveBytes {
				t.Fatal("existing supported profile narrowed or rebudgeted", profile, got, err)
			}
		})
	}
}
