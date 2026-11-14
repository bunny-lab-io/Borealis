package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Component composition only: source objects use the real existing readers,
// while artifact bytes and persistent target filesystem proof are synthetic.
// The production constructor's persistent_storage blocker is tested unchanged.
func TestClusterSSHPreparationCapacityComposition(t *testing.T) {
	for _, mode := range []string{"component success", "production storage blocked", "supervised target drift", "configuration drift after capacity", "later clean drift"} {
		t.Run(mode, func(t *testing.T) {
			f := newSSHWaitingCapacityFixture(t, false, "absent")
			nativeCalls, networkCalls, capacityCalls, configurationCalls := 0, 0, 0, 0
			native := func(ctx context.Context, plan string) (clusterSSHPreparationCleanObservation, error) {
				nativeCalls++
				if mode == "supervised target drift" && nativeCalls == 2 || mode == "later clean drift" && nativeCalls == 3 {
					f.targets[0].CPU.ObservationSHA256 = strings.Repeat("f", 64)
				}
				digest, err := clusterSSHPreparationTargetCPUDigest(f.snapshot, f.targets)
				if ctx.Err() != nil || plan != f.plan {
					err = errClusterConflict
				}
				return clusterSSHPreparationCleanObservation{Digest: digest, Targets: f.targets}, err
			}
			// Exact production pairing: clean gate does fresh native observation;
			// capacity receives its adjacent typed projection, not another SSH read.
			clean, targetProjection := clusterSSHPreparationTargetCPUReaderPair(f.snapshot, native)
			configuration, profile := clusterSSHPreparationConfigurationProjectionReader(f.snapshot.work, f.check, f.get,
				func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
					networkCalls++
					return sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version), ctx.Err()
				})
			capacity, approvalProjection := newClusterSSHPreparationCapacityRead(f.snapshot, f.check, f.get, profile, targetProjection,
				func(ctx context.Context, window int64) ([]int64, error) {
					if window <= 0 {
						return nil, errClusterConflict
					}
					return f.sites, ctx.Err()
				})
			capacityGate := func(ctx context.Context, plan string) (string, error) {
				beforeNative, beforeNetwork, beforeSecret := nativeCalls, networkCalls, f.storage.calls[clusterSSHRuntimeSecretPath]
				digest, err := capacity(ctx, plan)
				capacityCalls++
				if err == nil {
					if nativeCalls != beforeNative {
						t.Fatal("capacity extraction started an extra native target read")
					}
					// One existing configuration read keeps its before/after source
					// Jobs. Both capacity rounds recheck the full Secret directly;
					// extracting limits creates no duplicate native source Jobs.
					if networkCalls-beforeNetwork != 2*len(f.snapshot.work.Proof.Source.Members) || f.storage.calls[clusterSSHRuntimeSecretPath]-beforeSecret != 4 {
						t.Fatalf("profile extraction duplicated or omitted existing source reads: network=%d secret=%d", networkCalls-beforeNetwork, f.storage.calls[clusterSSHRuntimeSecretPath]-beforeSecret)
					}
					if mode == "configuration drift after capacity" {
						f.storage.objects[clusterSSHRuntimeSecretPath]["data"].(map[string]string)["BOREALIS_API_BACKEND_MEMORY_LIMIT"] = base64.StdEncoding.EncodeToString([]byte("2g"))
					}
				}
				return digest, err
			}
			configurationGate := func(ctx context.Context, plan string) (string, error) {
				beforeNetwork, beforeSecret := networkCalls, f.storage.calls[clusterSSHRuntimeSecretPath]
				digest, err := configuration(ctx, plan)
				configurationCalls++
				if err == nil && (networkCalls-beforeNetwork != 2*len(f.snapshot.work.Proof.Source.Members) || f.storage.calls[clusterSSHRuntimeSecretPath]-beforeSecret != 2) {
					t.Fatal("configuration gate reused cached profile or duplicated its extraction")
				}
				return digest, err
			}
			artifact := func(ctx context.Context, plan string) (string, error) {
				if ctx.Err() != nil || plan != f.plan {
					return "", errClusterConflict
				}
				return strings.Repeat("a", 64), nil
			}
			gates := clusterSSHPreparationNativeGates(f.snapshot, artifact, clean, capacityGate, configurationGate)
			if digest, err := gates[3](context.Background(), f.plan); digest != "" || err == nil {
				t.Fatal("production constructor lost persistent storage blocker")
			}
			if mode != "production storage blocked" {
				// This substitution tests component ordering, never production
				// readiness or a claimed successful filesystem observation.
				gates[3] = func(context.Context, string) (string, error) { return strings.Repeat("b", 64), nil }
			}
			source := func(ctx context.Context) error {
				observed, err := observeClusterSSHSourceCohort(ctx, f.get, f.snapshot.work.Proof.Source)
				if err != nil || !reflect.DeepEqual(observed, f.snapshot.work.Proof.Source) {
					return errClusterConflict
				}
				return ctx.Err()
			}
			entered, approved := false, false
			var escaped func(context.Context, string) (string, error)
			_, err := consumeClusterSSHPreparationNativeGates(context.Background(), gates,
				func(ctx context.Context, scoped clusterSSHPreparationGates) (clusterSSHPreparationReadinessReceipt, error) {
					escaped = scoped[2]
					var receipt clusterSSHPreparationReadinessReceipt
					err := withClusterSSHPreparationCPUApprovalProof(ctx, f.snapshot, scoped, source, approvalProjection,
						func(ctx context.Context, proof clusterSSHPreparationApprovalProof) error {
							entered = true
							if nativeCalls != 2 || capacityCalls != 1 || configurationCalls != 1 || networkCalls != 4*len(f.snapshot.work.Proof.Source.Members) || proof.capacity == nil {
								t.Fatalf("wrong initial production ordering: native=%d capacity=%d configuration=%d source=%d", nativeCalls, capacityCalls, configurationCalls, networkCalls)
							}
							if mode == "later clean drift" {
								// Existing commit performs this recheck before SQL mutation.
								if err := proof.recheck(ctx); err != nil {
									return err
								}
							}
							approved = true
							receipt = clusterSSHPreparationReadiness(f.plan, proof.receipts)
							receipt.Version, receipt.Targets, receipt.Capacity = 3, proof.targetCPU, proof.capacity
							return nil
						})
					return receipt, err
				})
			if mode == "component success" {
				if err != nil || !approved || nativeCalls != 4 || capacityCalls != 2 || configurationCalls != 2 || networkCalls != 8*len(f.snapshot.work.Proof.Source.Members) {
					t.Fatalf("component composition/recheck failed: %v, native=%d capacity=%d configuration=%d source=%d", err, nativeCalls, capacityCalls, configurationCalls, networkCalls)
				}
			} else {
				if err == nil || !errors.Is(err, errClusterConflict) || approved || entered != (mode == "later clean drift") {
					t.Fatalf("changed/incomplete proof reached approval: entered=%v approved=%v err=%v", entered, approved, err)
				}
				if mode == "production storage blocked" {
					var blocked clusterSSHPreparationGateBlocked
					if !errors.As(err, &blocked) || blocked.Gate != "persistent_storage" {
						t.Fatal("lost exact production blocker", err)
					}
				}
			}
			before := nativeCalls
			if digest, err := escaped(context.Background(), f.plan); digest != "" || err == nil || nativeCalls != before {
				t.Fatal("closed composition retained usable capacity gate")
			}
		})
	}
}
