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

func sshPreparationCPUReadFixture(snapshot clusterSSHPreparationApprovalSnapshot) clusterSSHPreparationTargetsRead {
	return func(ctx context.Context, _ string) (clusterSSHPreparationCleanObservation, error) {
		targets := sshPreparationTargetCPUFixture(snapshot)
		digest, err := clusterSSHPreparationTargetCPUDigest(snapshot, targets)
		if err != nil || ctx.Err() != nil {
			return clusterSSHPreparationCleanObservation{}, errClusterConflict
		}
		return clusterSSHPreparationCleanObservation{Digest: digest, Targets: targets}, nil
	}
}

func sshPreparationCPUGatesFixture(snapshot clusterSSHPreparationApprovalSnapshot) clusterSSHPreparationGates {
	gates := sshPreparationGatesFixture()
	gates[1] = func(ctx context.Context, plan string) (string, error) {
		value, err := sshPreparationCPUReadFixture(snapshot)(ctx, plan)
		return value.Digest, err
	}
	return gates
}

func TestClusterSSHPreparationTargetCPUProjectionSharesFreshRead(t *testing.T) {
	snapshot, _ := sshPreparationCleanTargetsFixture(t, false)
	plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
	calls := 0
	base := sshPreparationCPUReadFixture(snapshot)
	clean, project := clusterSSHPreparationTargetCPUReaderPair(snapshot, func(ctx context.Context, plan string) (clusterSSHPreparationCleanObservation, error) {
		calls++
		return base(ctx, plan)
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
	for _, mode := range []string{"fresh", "changed CPU", "changed clean digest", "consumer mutation", "read failure"} {
		t.Run(mode, func(t *testing.T) {
			snapshot, _ := sshPreparationCleanTargetsFixture(t, false)
			targets := sshPreparationTargetCPUFixture(snapshot)
			digest, _ := clusterSSHPreparationTargetCPUDigest(snapshot, targets)
			var gates clusterSSHPreparationGates
			for i := range gates {
				gates[i] = func(context.Context, string) (string, error) { return strings.Repeat("a", 64), nil }
			}
			gates[1] = func(context.Context, string) (string, error) { return digest, nil }
			calls := 0
			read := func(ctx context.Context, _ string) (clusterSSHPreparationCleanObservation, error) {
				calls++
				out := clusterSSHPreparationCleanObservation{Digest: digest, Targets: slices.Clone(targets)}
				if calls > 1 {
					switch mode {
					case "changed CPU":
						out.Targets[0].CPU.ObservationSHA256 = strings.Repeat("f", 64)
						out.Digest, _ = clusterSSHPreparationTargetCPUDigest(snapshot, out.Targets)
					case "changed clean digest":
						out.Digest = strings.Repeat("f", 64)
					case "read failure":
						return clusterSSHPreparationCleanObservation{}, errClusterConflict
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
				r.Version, r.Targets = 2, p.targetCPU
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
