package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// This fixture starts with waiting work and observes complete existing source
// objects. It does not borrow a claimed Expected, lease or fabricated subtotal.
type sshWaitingCapacityFixture struct {
	snapshot                 clusterSSHPreparationApprovalSnapshot
	storage                  *sshStorageFixture
	plan                     string
	targets                  []clusterSSHPreparationTargetCPU
	sites                    []int64
	lost                     bool
	targetReads, censusReads int
	networkReads             int
	beforeTargets            func(int)
	beforeCensus             func(int)
}

func newSSHWaitingCapacityFixture(t *testing.T, replacement bool, nodeMode string) *sshWaitingCapacityFixture {
	t.Helper()
	f := &sshWaitingCapacityFixture{snapshot: clusterSSHPreparationApprovalSnapshot{work: sshPreparationConfigurationFixture(t, replacement)}, sites: []int64{}}
	for i := range f.snapshot.work.Proof.Cohort.Targets {
		f.snapshot.work.Proof.Cohort.Targets[i].Report.CPUCount = 8
		if nodeMode == "override" {
			f.snapshot.work.Proof.Cohort.Targets[i].Report.CPUCount = 16
		}
	}
	f.plan, _ = clusterSSHPreparationPlanDigest(f.snapshot.work.Authorization)
	f.targets = sshPreparationTargetCPUFixture(f.snapshot)
	for i := range f.targets {
		if nodeMode != "override" {
			f.targets[i].CPU.OnlineCPUs, f.targets[i].CPU.AllocatableMilli = "0-7", 8000
		}
	}
	f.storage = newSSHStorageFixtureForAuthority(t, clusterSSHPreparationAuthority{Cohort: f.snapshot.work.Proof.Cohort, Source: f.snapshot.work.Proof.Source, Baseline: f.snapshot.work.Baseline, K3sVersion: clusterbootstrap.K3sPins().Version})
	resources := func(value clusterSSHApplicationResource) map[string]any {
		return map[string]any{"requests": map[string]any{"cpu": strconv.FormatUint(value.CPURequestMilli, 10) + "m", "memory": strconv.FormatUint(value.MemoryRequestBytes, 10)}, "limits": map[string]any{"cpu": strconv.FormatUint(value.CPULimitMilli, 10) + "m", "memory": strconv.FormatUint(value.MemoryLimitBytes, 10)}}
	}
	for _, value := range clusterSSHSmallBusinessApplications() {
		sshWaitingCapacityContainer(f.storage, value.Name)["resources"] = resources(value)
	}
	postgres := clusterSSHSmallBusinessPostgres()
	clusterSSHStorageMap(f.storage.objects[clusterSSHStoragePostgresPath], "spec")["resources"] = resources(postgres)
	for i := range f.snapshot.work.Proof.Source.Members {
		pod := f.storage.pod(i)
		for parent, keys := range map[string][]string{"spec": {"containers", "initContainers"}, "status": {"containerStatuses", "initContainerStatuses"}} {
			for _, key := range keys {
				for _, container := range clusterSSHStorageMap(pod, parent)[key].([]any) {
					container.(map[string]any)["resources"] = resources(postgres)
				}
			}
		}
	}
	for _, raw := range sshWaitingCapacityContainer(f.storage, "borealis-operator")["env"].([]any) {
		env := raw.(map[string]any)
		if env["name"] == "BOREALIS_SITE_WORKER_MEMORY_LIMIT" {
			env["value"] = "384Mi"
		}
	}
	secret := sshPreparationSecretFixture()
	data := map[string]string{}
	for key, value := range sshSmallBusinessSettings() {
		data[key] = base64.StdEncoding.EncodeToString([]byte(value))
	}
	secret["data"] = data
	f.storage.objects[clusterSSHRuntimeSecretPath] = secret
	if nodeMode != "absent" {
		for _, target := range f.snapshot.work.Proof.Cohort.Targets {
			node := sshStorageObject("longhorn.io/v1beta2", "Node", "longhorn-system", target.Report.Hostname)
			value := 0
			if nodeMode == "override" {
				value = 1000
			}
			node["spec"] = map[string]any{"instanceManagerCPURequest": value}
			f.storage.objects[clusterSSHInstanceNodePrefix+target.Report.Hostname] = node
		}
	}
	return f
}

func sshWaitingCapacityContainer(storage *sshStorageFixture, name string) map[string]any {
	return clusterSSHStorageMap(clusterSSHStorageMap(clusterSSHStorageMap(storage.objects[clusterSSHApplicationDeploymentPrefix+name], "spec"), "template"), "spec")["containers"].([]any)[0].(map[string]any)
}

func (f *sshWaitingCapacityFixture) check(ctx context.Context) error {
	if f.lost {
		return errClusterConflict
	}
	return ctx.Err()
}

func (f *sshWaitingCapacityFixture) get(ctx context.Context, path string, out any) error {
	if f.storage.objects[path] == nil {
		for _, target := range f.snapshot.work.Proof.Cohort.Targets {
			if path == clusterSSHInstanceNodePrefix+target.Report.Hostname {
				return errClusterSSHPreparationLonghornNodeAbsent
			}
		}
	}
	return f.storage.get(ctx, path, out)
}

func (f *sshWaitingCapacityFixture) readers() (func(context.Context, string) (string, error), clusterSSHPreparationTargetsRead) {
	_, configuration := clusterSSHPreparationConfigurationProjectionReader(f.snapshot.work, f.check, f.get,
		func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
			f.networkReads++
			return sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version), ctx.Err()
		})
	targets := func(ctx context.Context, plan string) (clusterSSHPreparationCleanObservation, error) {
		f.targetReads++
		if f.beforeTargets != nil {
			f.beforeTargets(f.targetReads)
		}
		digest, err := clusterSSHPreparationTargetCPUDigest(f.snapshot, f.targets)
		if ctx.Err() != nil || plan != f.plan {
			err = errClusterConflict
		}
		// Deliberately return shared storage: the gate must own retained values.
		return clusterSSHPreparationCleanObservation{Digest: digest, Targets: f.targets, Packages: sshPreparationTargetPackagesFixture(f.targets)}, err
	}
	census := func(ctx context.Context, window int64) ([]int64, error) {
		f.censusReads++
		if f.beforeCensus != nil {
			f.beforeCensus(f.censusReads)
		}
		if window <= 0 {
			return nil, errClusterConflict
		}
		return f.sites, ctx.Err()
	}
	return newClusterSSHPreparationCapacityRead(f.snapshot, f.check, f.get, configuration, targets, census)
}

func TestClusterSSHPreparationCapacityWaitingCompleteRounds(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		for _, mode := range []string{"absent", "default", "override"} {
			t.Run(strconv.FormatBool(replacement)+"/"+mode, func(t *testing.T) {
				f := newSSHWaitingCapacityFixture(t, replacement, mode)
				gate, approval := f.readers()
				digest, err := gate(context.Background(), f.plan)
				if err != nil {
					t.Fatal(err)
				}
				if f.targetReads != 2 || f.censusReads != 2 || f.storage.calls[clusterSSHRuntimeSecretPath] != 4 || f.storage.calls[clusterSSHStoragePostgresPath] != 2 {
					t.Fatalf("missing complete rounds: targets=%d census=%d secret=%d storage=%d", f.targetReads, f.censusReads, f.storage.calls[clusterSSHRuntimeSecretPath], f.storage.calls[clusterSSHStoragePostgresPath])
				}
				if f.networkReads != 2*len(f.snapshot.work.Proof.Source.Members) {
					t.Fatal("capacity duplicated native source observations", f.networkReads)
				}
				value, err := approval(context.Background(), f.plan)
				if err != nil || value.Capacity == nil {
					t.Fatalf("approval projection: %v", err)
				}
				actual, err := value.Capacity.digest(f.plan, value.Targets)
				if err != nil || actual != digest {
					t.Fatalf("capacity receipt mismatch: %v", err)
				}
				for _, target := range value.Capacity.Targets {
					want := uint64(960)
					if mode == "override" {
						want = 1000
					}
					if target.Capacity.LonghornCPU.RequestMilli != want || target.Capacity.LonghornCPU.Present != (mode != "absent") {
						t.Fatalf("own target policy mismatch: %+v", target.Capacity.LonghornCPU)
					}
				}
				r := clusterSSHPreparationReadiness(f.plan, [6]string{strings.Repeat("a", 64), value.Digest, digest, strings.Repeat("b", 64), value.Capacity.ConfigurationSHA256, strings.Repeat("c", 64)})
				r.Version, r.Targets, r.Capacity = 3, value.Targets, value.Capacity
				wire, _ := json.Marshal(r)
				if err := parseClusterSSHPreparationReadiness(wire, f.snapshot.work.Authorization); err != nil {
					t.Fatalf("real capacity readiness round trip: %v (%d bytes)", err, len(wire))
				}
				digest2, err := gate(context.Background(), f.plan)
				if err != nil || digest2 != digest || f.targetReads != 4 || f.censusReads != 4 {
					t.Fatalf("repeat did not reread complete evidence: %v", err)
				}
			})
		}
	}
}

func TestClusterSSHPreparationCapacityWaitingPassiveDemand(t *testing.T) {
	f := newSSHWaitingCapacityFixture(t, false, "absent")
	gate, approval := f.readers()
	if _, err := gate(context.Background(), f.plan); err != nil {
		t.Fatal(err)
	}
	empty, _ := approval(context.Background(), f.plan)
	f.sites = []int64{11, 29}
	gate, approval = f.readers()
	if _, err := gate(context.Background(), f.plan); err != nil {
		t.Fatal(err)
	}
	passive, _ := approval(context.Background(), f.plan)
	if passive.Capacity.CensusSHA256 == empty.Capacity.CensusSHA256 {
		t.Fatal("passive desired workers omitted from census")
	}
	for i, target := range passive.Capacity.Targets {
		before := empty.Capacity.Targets[i].Capacity
		// Two passive workers plus complete rotation overlap: worker requests
		// stay 50m, distinct from task CPU limits, with existing 512Mi legacy
		// recreation envelope retained above the current 384Mi setting.
		if target.Capacity.CPURequestMilli-before.CPURequestMilli != 200 || target.Capacity.MemoryLimitBytes-before.MemoryLimitBytes != 4*512<<20 {
			t.Fatalf("passive worker/rotation budget omitted: before=%+v after=%+v", before, target.Capacity)
		}
	}
}

func TestClusterSSHPreparationCapacityWaitingDrift(t *testing.T) {
	for _, mode := range []string{"source", "profile", "census", "missing census", "unordered census", "CPU", "target override", "own override exceeds cap", "source policy", "unknown source workload", "CPU insufficient", "memory insufficient", "authority", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHWaitingCapacityFixture(t, false, "default")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.beforeTargets = func(round int) {
				if round != 1 {
					return
				}
				switch mode {
				case "source":
					f.storage.objects["/api/v1/nodes"]["items"].([]any)[0].(map[string]any)["status"].(map[string]any)["nodeInfo"].(map[string]any)["bootID"] = newClusterUUID()
				case "profile":
					f.storage.objects[clusterSSHRuntimeSecretPath]["data"].(map[string]string)["BOREALIS_API_BACKEND_MEMORY_LIMIT"] = base64.StdEncoding.EncodeToString([]byte("2g"))
				case "census":
					f.sites = []int64{11}
				case "source policy":
					sshWaitingCapacityContainer(f.storage, "api-backend")["resources"].(map[string]any)["limits"].(map[string]any)["memory"] = "2048Mi"
				case "unknown source workload":
					delete(f.storage.objects, clusterSSHApplicationDeploymentPrefix+"webui-frontend")
				case "authority":
					f.lost = true
				case "cancel":
					cancel()
				}
			}
			if mode == "CPU" || mode == "target override" {
				f.beforeTargets = func(round int) {
					if round != 2 {
						return
					}
					if mode == "CPU" {
						f.targets[0].CPU.ObservationSHA256 = strings.Repeat("f", 64)
					} else {
						clusterSSHStorageMap(f.storage.objects[clusterSSHInstanceNodePrefix+f.snapshot.work.Proof.Cohort.Targets[0].Report.Hostname], "spec")["instanceManagerCPURequest"] = 1000
					}
				}
			}
			if mode == "CPU insufficient" {
				for id := int64(1); id <= 50; id++ {
					f.sites = append(f.sites, id)
				}
				for i := range f.targets {
					f.targets[i].CPU.MemoryKiB = 512 << 20
					f.snapshot.work.Proof.Cohort.Targets[i].Report.MemoryKiB = 512 << 20
				}
			}
			if mode == "missing census" {
				f.sites = nil
			}
			if mode == "unordered census" {
				f.sites = []int64{2, 1}
			}
			if mode == "own override exceeds cap" {
				clusterSSHStorageMap(f.storage.objects[clusterSSHInstanceNodePrefix+f.snapshot.work.Proof.Cohort.Targets[0].Report.Hostname], "spec")["instanceManagerCPURequest"] = 1501
			}
			if mode == "memory insufficient" {
				for i := range f.targets {
					f.targets[i].CPU.MemoryKiB = 16 << 20
					f.snapshot.work.Proof.Cohort.Targets[i].Report.MemoryKiB = 16 << 20
				}
			}
			gate, approval := f.readers()
			digest, err := gate(ctx, f.plan)
			var blocked clusterSSHPreparationGateBlocked
			if digest != "" || !errors.As(err, &blocked) || blocked.Gate != "complete_capacity" {
				t.Fatalf("drift accepted: digest=%q err=%v", digest, err)
			}
			if _, err := approval(context.Background(), f.plan); err == nil {
				t.Fatal("failed round exposed cached approval")
			}
			f.lost = false
			if _, err := gate(context.Background(), f.plan); err == nil {
				t.Fatal("failed scope reused")
			}
		})
	}
}

func TestClusterSSHPreparationCapacityWaitingProjectionLifetime(t *testing.T) {
	for _, mode := range []string{"copies", "authority", "changed later", "before observation", "wrong plan", "canceled projection"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHWaitingCapacityFixture(t, false, "override")
			gate, approval := f.readers()
			if mode == "before observation" {
				if _, err := approval(context.Background(), f.plan); err == nil {
					t.Fatal("unobserved projection accepted")
				}
				return
			}
			if _, err := gate(context.Background(), f.plan); err != nil {
				t.Fatal(err)
			}
			original, err := approval(context.Background(), f.plan)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "copies" {
				value, _ := approval(context.Background(), f.plan)
				if !validClusterSSHPreparationPackages(value.Targets, value.Packages) {
					t.Fatal("capacity discarded package projection")
				}
				value.Packages[0].Selection.Missing[0] = "foreign"
				value.Targets[0].CPU.MemoryKiB++
				value.Capacity.Targets[0].Capacity.CPURequestMilli++
				f.targets[0].CPU.ObservationSHA256 = strings.Repeat("f", 64)
				f.snapshot.work.Proof.Cohort.Targets[0].Report.CPUCount++
				again, err := approval(context.Background(), f.plan)
				if err != nil || !reflect.DeepEqual(original, again) {
					t.Fatalf("retained projection aliases caller: %v", err)
				}
				return
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			plan := f.plan
			switch mode {
			case "authority":
				f.lost = true
			case "changed later":
				f.sites = slices.Clone([]int64{11})
				if _, err := gate(context.Background(), f.plan); err == nil {
					t.Fatal("changed complete observation reused old receipt")
				}
			case "wrong plan":
				plan = strings.Repeat("f", 64)
			case "canceled projection":
				cancel()
			}
			if _, err := approval(ctx, plan); err == nil {
				t.Fatal("stale projection accepted")
			}
			f.lost = false
			if _, err := approval(context.Background(), f.plan); err == nil {
				t.Fatal("failed projection regained authority")
			}
		})
	}
}
