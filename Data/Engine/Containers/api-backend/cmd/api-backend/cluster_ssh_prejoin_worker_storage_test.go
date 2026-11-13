package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func sshPrejoinWorkerCapacityFixture() clusterSSHPreparedImageCapacity {
	return clusterSSHPreparedImageCapacity{WorkerScratchBytes: 1024, WorkerScratchEntries: 4,
		PostgresWorkerPeakBytes: 512, PostgresWorkerPeakEntries: 2,
		Target: []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 8 << 30, Entries: 10}}}
}

func sshPrejoinWorkerStorageFixture(t *testing.T) (*sshNetworkTargetsFixture, clusterSSHPrejoinSource, string) {
	t.Helper()
	f := newSSHNetworkTargetsFixture(t, false, "filesystem")
	_, process, _ := existingIdentitySourceFixture(t)
	m := f.a.Source.Members[0]
	process.NodeName = m.Name
	source := clusterSSHPrejoinSource{Binding: engineidentity.Binding{ClusterID: f.a.Source.ClusterID, SourceID: m.NodeID, SourceUID: m.NodeUID}, Process: process}
	f.a.IdentitySource = &source.Binding
	for i := range f.a.Cohort.Targets {
		f.a.Cohort.Targets[i].Report.CPUCount = 16
		f.a.Cohort.Targets[i].Report.MemoryKiB = 33554432
	}
	proof := clusterSSHParentProof{Version: 1, Cohort: f.a.Cohort, Source: f.a.Source}
	f.a.Queue = clusterSSHQueueSource{ClusterID: f.a.Source.ClusterID, ActiveOperationID: f.a.Cohort.OperationID, Release: f.a.Baseline.Release, SHA: f.a.Baseline.SourceSHA}
	report := newClusterSSHQualificationReport(f.a.Cohort.Attempt)
	for i := 0; i < len(report.Checks)-1; i++ {
		report.Checks[i].State = "passed"
	}
	authorization := sshPreparationAuthorizationFixture(t, proof, f.a.Queue, &report)
	confirmation := sshPreparationConfirmationFixture(t, &authorization, f.a.Source)
	plan, _ := clusterSSHPreparationPlanDigest(authorization)
	var receipts [6]string
	for i := range receipts {
		receipts[i] = strings.Repeat("a", 64)
	}
	snapshot := clusterSSHPreparationApprovalSnapshot{work: clusterSSHPreparationWork{Proof: proof, Authorization: authorization, Baseline: f.a.Baseline}}
	targets := sshPreparationTargetCPUFixture(snapshot)
	projection := sshPreparationApprovalCapacityFixture(t, snapshot)
	receipts[1], _ = clusterSSHPreparationTargetCPUDigest(snapshot, targets)
	receipts[2], _ = projection.digest(plan, targets)
	receipts[4] = projection.ConfigurationSHA256
	readiness := clusterSSHPreparationReadiness(plan, receipts)
	readiness.Version, readiness.Targets, readiness.Capacity = 3, targets, &projection
	digest, _ := clusterSSHPreparationDigest(proof)
	f.a.payload = marshalClusterJSON(map[string]any{"baseline_release": f.a.Baseline.Release, "baseline_sha": f.a.Baseline.SourceSHA, "source_k3s_version": f.a.K3sVersion, "target_count": len(f.a.Cohort.Targets),
		"ssh_inspection": map[string]any{"proof": proof, "sha256": digest, "qualification_required": true}, "ssh_preparation_authorization": authorization, "ssh_qualification": report, "ssh_preparation_confirmation": confirmation, "ssh_preparation_readiness": readiness})
	f.a.PayloadSHA256 = preparationReceiptHash(f.a.payload)
	return f, source, authorization.ConfirmationSHA256
}

func sshPrejoinWorkerStorageWire(t *testing.T, raw []byte, available uint64) []byte {
	t.Helper()
	var wire struct {
		Version   int                              `json:"version"`
		MachineID string                           `json:"machine_id"`
		BootID    string                           `json:"boot_id"`
		Evidence  clusterremote.FilesystemEvidence `json:"evidence"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		t.Fatal("invalid filesystem fixture")
	}
	wire.Version = 4
	wire.Evidence.Filesystems[0].TotalBytes = 120 << 30
	wire.Evidence.Filesystems[0].AvailableBytes = available
	out, _ := json.Marshal(wire)
	return out
}

// Runs the production worker input composition and claimed SSH filesystem
// reader. Only acquisition/native mutation and external SSH are test doubles.
func TestClusterSSHPrejoinWorkerStorageBeforeNative(t *testing.T) {
	for _, mode := range []string{"first target", "second target", "missing capacity", "missing storage", "missing check", "shortage", "stale native", "wrong cohort", "wrong selected", "CPU drift", "scope cleanup", "expiry", "source drift", "native writes", "source cleanup"} {
		t.Run(mode, func(t *testing.T) {
			f, source, confirmation := sshPrejoinWorkerStorageFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			selected := 0
			if mode == "second target" {
				selected = 1
			}
			claim := f.claims[selected]
			expected, err := buildClusterSSHPreparationExpected(f.a.Cohort, f.a.Source, claim.Lease, f.a.Baseline, f.a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16", f.a.payload)
			if err != nil {
				t.Fatal(err)
			}
			if expected.TargetCPU == nil {
				t.Fatal("supported approval CPU binding missing from worker fixture")
			}
			snapshot := sshStorageSnapshotFixture(t, f.a)
			capacity := sshPrejoinWorkerCapacityFixture()
			nativeCalled, acquisitionClosed, storageClosed := false, false, false
			f.filesystemWire = func(i int, raw []byte) []byte {
				available := uint64(90 << 30)
				if mode == "shortage" && i == selected || mode == "native writes" && nativeCalled {
					available = 1
				}
				return sshPrejoinWorkerStorageWire(t, raw, available)
			}
			storage := newClusterSSHPrejoinWorkerStorageCheck(f.a.Baseline, f.claims,
				func(ctx context.Context, consume func(context.Context, clusterSSHNetworkTargetReaders) error) error {
					defer func() { storageClosed = true }()
					err := runClusterSSHNetworkTargets(ctx, f.a.Baseline, f.claims, f.deps, func(ctx context.Context, readers clusterSSHNetworkTargetReaders) error {
						if mode == "stale native" {
							original := readers.Filesystem
							cached := map[string]clusterremote.TargetFilesystem{}
							readers.Filesystem = func(ctx context.Context, item clusterSSHInspectedTarget, request clusterremote.FilesystemRequest) (clusterremote.TargetFilesystem, error) {
								if value, ok := cached[item.Binding.TargetID]; ok {
									return value, nil
								}
								value, err := original(ctx, item, request)
								cached[item.Binding.TargetID] = value
								return value, err
							}
						}
						return consume(ctx, readers)
					})
					if mode == "scope cleanup" {
						return errors.New("private cleanup failure")
					}
					if mode == "expiry" {
						cancel()
					}
					return err
				})
			if mode == "missing check" {
				storage = nil
			}
			checks := clusterSSHPreparationChecks{
				Inputs: func(context.Context) error {
					if mode == "source drift" && storageClosed {
						return errors.New("changed source storage")
					}
					return nil
				},
				Authority: func(ctx context.Context) error { return ctx.Err() },
				K3sJoin:   func(context.Context, engineidentity.Binding, clusterSSHJoinMaterialConsumer) error { return nil },
			}
			checks.Storage = newClusterSSHPrejoinWorkerStorageRead(ctx, snapshot, checks.Authority)
			acquire := func(ctx context.Context, consume clusterSSHPrejoinWorkerSourceConsumer) error {
				defer func() { acquisitionClosed = true }()
				switch mode {
				case "missing capacity":
					capacity = clusterSSHPreparedImageCapacity{}
				case "missing storage":
					checks.Storage = nil
				case "wrong cohort":
					f.a.Cohort.Targets[1].Report.BootID = newClusterUUID()
				case "CPU drift":
					cpu := *expected.TargetCPU
					cpu.ObservationSHA256 = strings.Repeat("f", 64)
					expected.TargetCPU = &cpu
				case "wrong selected":
					expected.Target.TargetID = f.claims[1].Lease.TargetID
				}
				err := consume(ctx, &clusterbootstrap.PreparationInputs{}, &clusterbootstrap.PreparationArtifacts{},
					clusterbootstrap.PostgresImageRepository+"@sha256:"+strings.Repeat("a", 64), capacity, expected, checks)
				if mode == "source cleanup" {
					return errors.New("private acquired source cleanup")
				}
				return err
			}
			var escaped clusterSSHPreparationChecks
			native := func(ctx context.Context, _ *clusterbootstrap.PreparationInputs, _ *clusterbootstrap.PreparationArtifacts, _ string,
				e clusterbootstrap.PreparationExpected, supplied clusterSSHPreparationChecks) (clusterbootstrap.PrejoinPreparationReceipt, error) {
				if !storageClosed || acquisitionClosed || !reflect.DeepEqual(e, expected) {
					t.Fatal("native mutation preceded joined storage proof or escaped acquisition")
				}
				f.assertClosed(t)
				nativeCalled, escaped = true, supplied
				return sshPrejoinReceiptFixture(expected, source.Binding), nil
			}
			result, err := dispatchClusterSSHPrejoinWorker(ctx, claim, f.a.Baseline, confirmation, source,
				f.deps.authority(claim), acquire, storage, native, func(ctx context.Context) error { return ctx.Err() })
			success := mode == "first target" || mode == "second target" || mode == "native writes"
			if (err == nil) != success || nativeCalled != (success || mode == "source cleanup") {
				t.Fatalf("incorrect native/storage boundary: error=%v native=%v", err, nativeCalled)
			}
			if !success && (!reflect.DeepEqual(result, clusterSSHPrejoinDispatchResult{}) || err != clusterbootstrap.ErrSessionAuthority) {
				t.Fatal("failed storage scope released completion or private diagnostic")
			}
			if escaped.Storage != nil {
				if _, err := escaped.Storage(context.Background()); err == nil {
					t.Fatal("storage snapshot escaped worker lifetime")
				}
			}
			f.assertClosed(t)
		})
	}
}

func TestClusterSSHPrejoinWorkerStorageReopenedScopes(t *testing.T) {
	for _, mode := range []string{"same invocation", "demand drift", "source drift", "new invocation"} {
		t.Run(mode, func(t *testing.T) {
			f, _, _ := sshPrejoinWorkerStorageFixture(t)
			written := [2]bool{}
			f.filesystemWire = func(i int, raw []byte) []byte {
				available := uint64(90 << 30)
				if written[i] {
					available = 1
				}
				return sshPrejoinWorkerStorageWire(t, raw, available)
			}
			within := func(ctx context.Context, consume func(context.Context, clusterSSHNetworkTargetReaders) error) error {
				return runClusterSSHNetworkTargets(ctx, f.a.Baseline, f.claims, f.deps, consume)
			}
			check := newClusterSSHPrejoinWorkerStorageCheck(f.a.Baseline, f.claims, within)
			storage := sshStorageSnapshotFixture(t, f.a)
			checks := clusterSSHPreparationChecks{Authority: func(context.Context) error { return nil },
				Storage: newClusterSSHPrejoinWorkerStorageRead(context.Background(), storage, func(context.Context) error { return nil })}
			var expected []clusterbootstrap.PreparationExpected
			for i, claim := range f.claims {
				e, err := buildClusterSSHPreparationExpected(f.a.Cohort, f.a.Source, claim.Lease, f.a.Baseline, f.a.K3sVersion, "10.42.0.0/16", "10.43.0.0/16", f.a.payload)
				if err != nil || check(context.Background(), f.a.payload, e, sshPrejoinWorkerCapacityFixture(), checks) != nil {
					t.Fatalf("first selected target proof failed: index=%d error=%v", i, err)
				}
				expected = append(expected, e)
				written[i] = true
			}
			opens := f.opens[0].Load() + f.opens[1].Load()
			capacity := sshPrejoinWorkerCapacityFixture()
			switch mode {
			case "demand drift":
				capacity.Target[0].Bytes--
			case "source drift":
				storage.Observation = strings.Repeat("b", 64)
				checks.Storage = newClusterSSHPrejoinWorkerStorageRead(context.Background(), storage, func(context.Context) error { return nil })
			case "new invocation":
				check = newClusterSSHPrejoinWorkerStorageCheck(f.a.Baseline, f.claims, within)
			}
			err := check(context.Background(), f.a.payload, expected[0], capacity, checks)
			if (err == nil) != (mode == "same invocation") {
				t.Fatalf("reopened scope changed retained demand/authority: %v", err)
			}
			if mode != "new invocation" && f.opens[0].Load()+f.opens[1].Load() != opens {
				t.Fatal("reopened scope charged original demand after own writes")
			}
			f.assertClosed(t)
		})
	}
}

func TestClusterSSHPrejoinWorkerStorageSourceOwnership(t *testing.T) {
	a := sshPrejoinCompletionAuthorityFixture(t, false)
	original := sshStorageSnapshotFixture(t, a)
	owned := original.Requirements.Volumes[0]
	ctx, cancel := context.WithCancel(context.Background())
	read := newClusterSSHPrejoinWorkerStorageRead(ctx, original, func(context.Context) error { return nil })
	original.Requirements.Volumes[0].Claim = "changed original"
	first, err := read(ctx)
	if err != nil || !reflect.DeepEqual(first.Requirements.Volumes[0], owned) {
		t.Fatal("retained storage aliases original snapshot")
	}
	first.Requirements.Volumes = slices.Clone(first.Requirements.Volumes)
	first.Requirements.Volumes[0].Claim = "changed consumer"
	second, err := read(ctx)
	if err != nil || !reflect.DeepEqual(second.Requirements.Volumes[0], owned) {
		t.Fatal("borrowed storage aliases later reads")
	}
	cancel()
	if _, err := read(context.Background()); err == nil {
		t.Fatal("source storage outlived acquisition")
	}
}
