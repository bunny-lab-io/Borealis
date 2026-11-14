package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func sshPreparationTargetCPUFixture(snapshot clusterSSHPreparationApprovalSnapshot) []clusterSSHPreparationTargetCPU {
	plan, _ := clusterbootstrap.PreparationTargetCPUPlanDigest(clusterbootstrap.K3sPins().Version)
	var targets []clusterSSHPreparationTargetCPU
	for _, target := range snapshot.work.Proof.Cohort.Targets {
		targets = append(targets, clusterSSHPreparationTargetCPU{TargetID: target.Binding.TargetID, ObservationSHA256: strings.Repeat("d", 64),
			CPU: clusterbootstrap.PreparationTargetCPU{Version: 1, PlanSHA256: plan, OnlineCPUs: "0-15", AllocatableMilli: 16000, MemoryKiB: 33554432, ObservationSHA256: strings.Repeat("e", 64)}})
	}
	return targets
}

func sshPreparationCPUReadFixture(t *testing.T, snapshot clusterSSHPreparationApprovalSnapshot) clusterSSHPreparationTargetsRead {
	capacity := sshPreparationApprovalCapacityFixture(t, snapshot)
	return func(ctx context.Context, _ string) (clusterSSHPreparationCleanObservation, error) {
		targets := sshPreparationTargetCPUFixture(snapshot)
		digest, err := clusterSSHPreparationTargetCPUDigest(snapshot, targets)
		if err != nil || ctx.Err() != nil {
			return clusterSSHPreparationCleanObservation{}, errClusterConflict
		}
		owned := capacity.clone()
		return clusterSSHPreparationCleanObservation{Digest: digest, Targets: targets, Capacity: &owned}, nil
	}
}

func sshPreparationCPUGatesFixture(t *testing.T, snapshot clusterSSHPreparationApprovalSnapshot) clusterSSHPreparationGates {
	gates := sshPreparationGatesFixture()
	gates[1] = func(ctx context.Context, plan string) (string, error) {
		value, err := sshPreparationCPUReadFixture(t, snapshot)(ctx, plan)
		return value.Digest, err
	}
	capacity := sshPreparationApprovalCapacityFixture(t, snapshot)
	gates[2] = func(ctx context.Context, plan string) (string, error) {
		return capacity.digest(plan, sshPreparationTargetCPUFixture(snapshot))
	}
	gates[4] = func(context.Context, string) (string, error) { return capacity.ConfigurationSHA256, nil }
	return gates
}

// Approval/SQL tests borrow a complete existing workload fixture. Native gate
// tests separately prove its source transport and waiting-proposal lifetime.
func sshPreparationApprovalCapacityFixture(t *testing.T, snapshot clusterSSHPreparationApprovalSnapshot) clusterSSHPreparationCapacityProjection {
	t.Helper()
	a := clusterSSHPreparationAuthority{Cohort: snapshot.work.Proof.Cohort, Source: snapshot.work.Proof.Source, Baseline: snapshot.work.Baseline, K3sVersion: clusterbootstrap.K3sPins().Version}
	storage := sshStorageSnapshotFixture(t, a)
	// This approval seam uses stable mocked source receipts; native gate tests
	// exercise real complete-object receipts and drift through the transport.
	storage.Observation = strings.Repeat("c", 64)
	storage.Requirements.Applications, storage.Requirements.PostgresResources = clusterSSHSmallBusinessApplications(), clusterSSHSmallBusinessPostgres()
	storage.Requirements.WorkerPolicy.Limits.Memory = "384Mi"
	value := clusterSSHPreparationSnapshot{Storage: storage, DesiredWorkerSites: []int64{}, Observation: strings.Repeat("a", 64)}
	targets := sshPreparationTargetCPUFixture(snapshot)
	clean, _ := clusterSSHPreparationTargetCPUDigest(snapshot, targets)
	p := clusterSSHPreparationCapacityProjection{Version: 1, CleanSHA256: clean, ConfigurationSHA256: strings.Repeat("b", 64), SecretSHA256: value.Observation,
		StorageSHA256: storage.Observation, InstancePolicy: storage.Requirements.LonghornInstanceManagerResources}
	p.CensusSHA256, _ = clusterSSHPreparationDigest(value.DesiredWorkerSites)
	for i, target := range targets {
		hostname := snapshot.work.Proof.Cohort.Targets[i].Report.Hostname
		node := sshStorageObject("longhorn.io/v1beta2", "Node", "longhorn-system", hostname)
		clusterSSHStorageMap(node, "metadata")["uid"] = target.TargetID
		node["spec"] = map[string]any{"instanceManagerCPURequest": json.Number("1000")}
		capacity, err := calculateClusterSSHPreparationTargetCapacity(value, target.CPU, hostname, func(string) (map[string]any, error) { return node, nil })
		if err != nil {
			t.Fatal("complete approval capacity fixture", err)
		}
		p.Targets = append(p.Targets, clusterSSHPreparationCapacityTarget{target.TargetID, capacity})
	}
	return p
}

func sshPreparationApprovalCapacityProof(t *testing.T, snapshot clusterSSHPreparationApprovalSnapshot, proof *clusterSSHPreparationApprovalProof) {
	t.Helper()
	proof.targetCPU = sshPreparationTargetCPUFixture(snapshot)
	proof.receipts[1], _ = clusterSSHPreparationTargetCPUDigest(snapshot, proof.targetCPU)
	capacity := sshPreparationApprovalCapacityFixture(t, snapshot)
	proof.capacity = &capacity
	var err error
	proof.receipts[2], err = capacity.digest(proof.plan, proof.targetCPU)
	if err != nil {
		t.Fatal("capacity proof fixture", err)
	}
	proof.receipts[4] = capacity.ConfigurationSHA256
}

func TestClusterSSHPreparationTargetCPUProjectionSharesFreshRead(t *testing.T) {
	snapshot, _ := sshPreparationCleanTargetsFixture(t, false)
	plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	calls := 0
	base := sshPreparationCPUReadFixture(t, snapshot)
	clean, project := clusterSSHPreparationTargetCPUReaderPair(snapshot, func(ctx context.Context, plan string) (clusterSSHPreparationCleanObservation, error) {
		calls++
		value, err := base(ctx, plan)
		value.Packages = sshPreparationTargetPackagesFixture(value.Targets)
		return value, err
	})
	if _, err := project(context.Background(), plan); err == nil || calls != 0 {
		t.Fatal("unobserved projection became proof")
	}
	digest, err := clean(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		value, err := project(context.Background(), plan)
		if err != nil || value.Digest != digest || calls != 1 {
			t.Fatal("projection caused redundant native observation", err)
		}
		if !validClusterSSHPreparationPackages(value.Targets, value.Packages) {
			t.Fatal("package projection missing or borrowed")
		}
		value.Packages[0].Selection.Missing[0] = "foreign"
		value.Targets[0].CPU.MemoryKiB = 1
	}
	if _, err := clean(context.Background(), plan); err != nil || calls != 2 {
		t.Fatal("gate reused stale native proof")
	}
}

func TestClusterSSHPreparationTargetCPUReadinessBinding(t *testing.T) {
	snapshot, _ := sshPreparationCleanTargetsFixture(t, false)
	targets := sshPreparationTargetCPUFixture(snapshot)
	digest, err := clusterSSHPreparationTargetCPUDigest(snapshot, targets)
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	receipts := [6]string{strings.Repeat("a", 64), digest, strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64), strings.Repeat("e", 64)}
	readiness := clusterSSHPreparationReadiness(plan, receipts)
	readiness.Version, readiness.Targets = 2, targets
	raw, _ := json.Marshal(readiness)
	if parseClusterSSHPreparationReadiness(raw, snapshot.work.Authorization) != nil {
		t.Fatal("typed readiness rejected")
	}
	payload := marshalClusterJSON(map[string]any{"ssh_preparation_readiness": readiness, "ssh_preparation_authorization": snapshot.work.Authorization})
	got, err := clusterSSHPreparationExpectedCPU(payload, targets[0].TargetID)
	if err != nil || got == nil || *got != targets[0].CPU {
		t.Fatal("approved CPU not restored", err)
	}
	got.AllocatableMilli = 1
	again, _ := clusterSSHPreparationExpectedCPU(payload, targets[0].TargetID)
	if again.AllocatableMilli != 16000 {
		t.Fatal("retained caller changed binding")
	}
	for _, mode := range []string{"legacy relabel", "wrong target", "reordered", "CPU drift", "clean digest drift", "missing targets"} {
		t.Run(mode, func(t *testing.T) {
			changed := readiness
			changed.Targets = slices.Clone(readiness.Targets)
			changed.Checks = slices.Clone(readiness.Checks)
			switch mode {
			case "legacy relabel":
				changed.Version = 1
			case "wrong target":
				changed.Targets[0].TargetID = newClusterUUID()
			case "reordered":
				changed.Targets[0], changed.Targets[1] = changed.Targets[1], changed.Targets[0]
			case "CPU drift":
				changed.Targets[0].CPU.ObservationSHA256 = strings.Repeat("f", 64)
			case "clean digest drift":
				changed.Checks[1].SHA256 = strings.Repeat("f", 64)
			case "missing targets":
				changed.Targets = nil
			}
			raw, _ := json.Marshal(changed)
			if parseClusterSSHPreparationReadiness(raw, snapshot.work.Authorization) == nil {
				t.Fatal("changed CPU binding accepted")
			}
		})
	}
	legacy := clusterSSHPreparationReadiness(plan, receipts)
	payload = marshalClusterJSON(map[string]any{"ssh_preparation_readiness": legacy, "ssh_preparation_authorization": snapshot.work.Authorization})
	if cpu, err := clusterSSHPreparationExpectedCPU(payload, targets[0].TargetID); err != nil || cpu != nil {
		t.Fatal("legacy readiness acquired CPU authority")
	}
}

func TestClusterSSHPreparationTargetCPUApprovalRechecks(t *testing.T) {
	for _, mode := range []string{"fresh", "changed CPU", "changed clean digest", "consumer mutation", "read failure", "missing capacity", "changed capacity"} {
		t.Run(mode, func(t *testing.T) {
			snapshot, _ := sshPreparationCleanTargetsFixture(t, false)
			targets := sshPreparationTargetCPUFixture(snapshot)
			digest, _ := clusterSSHPreparationTargetCPUDigest(snapshot, targets)
			gates := sshPreparationCPUGatesFixture(t, snapshot)
			capacity := sshPreparationApprovalCapacityFixture(t, snapshot)
			calls := 0
			read := func(ctx context.Context, _ string) (clusterSSHPreparationCleanObservation, error) {
				calls++
				copy := capacity.clone()
				out := clusterSSHPreparationCleanObservation{Digest: digest, Targets: slices.Clone(targets), Capacity: &copy}
				if mode == "missing capacity" {
					out.Capacity = nil
				}
				if calls > 1 {
					switch mode {
					case "changed CPU":
						out.Targets[0].CPU.ObservationSHA256 = strings.Repeat("f", 64)
						out.Digest, _ = clusterSSHPreparationTargetCPUDigest(snapshot, out.Targets)
					case "changed clean digest":
						out.Digest = strings.Repeat("f", 64)
					case "read failure":
						return clusterSSHPreparationCleanObservation{}, errClusterConflict
					case "changed capacity":
						out.Capacity.Targets[0].Capacity.LonghornCPU.OverrideMilli++
					}
				}
				return out, ctx.Err()
			}
			err := withClusterSSHPreparationCPUApprovalProof(context.Background(), snapshot, gates, func(ctx context.Context) error { return ctx.Err() }, read, func(ctx context.Context, p clusterSSHPreparationApprovalProof) error {
				if !reflect.DeepEqual(p.targetCPU, targets) {
					t.Fatal("CPU missing from capability")
				}
				if mode == "consumer mutation" {
					p.targetCPU[0].CPU.OnlineCPUs = "0"
				}
				r := clusterSSHPreparationReadiness(p.plan, p.receipts)
				r.Version, r.Targets, r.Capacity = 3, p.targetCPU, p.capacity
				if validateClusterSSHPreparationReadinessCPU(r, snapshot.work.Authorization) != nil {
					return errClusterConflict
				}
				return p.recheck(ctx)
			})
			if (err == nil) != (mode == "fresh") {
				t.Fatal("unexpected capability result", err)
			}
		})
	}
}
