package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func reissuedSourceStageFixture(t *testing.T, replacement bool, rounds int, baseline clusterbootstrap.Expected) (
	clusterSSHPreparationRecoveryReissuedProofAuthority, clusterSSHPreparationRecoveryReissuedProofClaim, map[string]any,
	func(context.Context, string, any) error, clusterSSHSourceNetworkRead) {
	t.Helper()
	value, payload, sealed, secret, get, network := reissuedProgressFixture(t, replacement, rounds, baseline)
	claim := clusterSSHPreparationRecoveryReissuedProofClaim{Lineage: value.Lineage, Baseline: value.Baseline, payload: payload}
	_, _, tip := value.Lineage.tip()
	for i, lease := range value.Leases {
		claim.Claims = append(claim.Claims, clusterSSHPreparationRecoveryTargetClaim{Lease: lease, Previous: tip[i], Sealed: sealed[i]})
	}
	return value, claim, secret, get, network
}

func TestClusterSSHPreparationRecoveryReissuedSourceStageRejectsForeignCohort(t *testing.T) {
	baseline, _ := recoveryProgressManifestFixture()
	for _, mode := range []string{"Q2 lineage", "raw payload", "missing sibling", "old generation", "future generation", "holder", "previous tip", "credential", "baseline"} {
		t.Run(mode, func(t *testing.T) {
			value, claim, _, _, _ := reissuedSourceStageFixture(t, false, 2, baseline)
			switch mode {
			case "Q2 lineage":
				claim.Lineage.Requests = nil
			case "raw payload":
				claim.payload = `{}`
			case "missing sibling":
				claim.Claims = claim.Claims[:1]
			case "old generation":
				claim.Claims[1].Lease.Generation--
			case "future generation":
				claim.Claims[1].Lease.Generation++
			case "holder":
				claim.Claims[1].Lease.Holder = newClusterUUID()
			case "previous tip":
				claim.Claims[1].Previous.PreviousHolder = newClusterUUID()
			case "credential":
				claim.Claims[1].Sealed.ciphertext += "changed"
			case "baseline":
				claim.Baseline.SourceSHA = strings.Repeat("c", 40)
			}
			calls := 0
			deps := clusterSSHPreparationRecoveryReissuedSourceStageDependencies{
				source: func(clusterSSHPreparationRecoveryReissuedProofAuthority, string, func(context.Context) error) clusterSSHPreparationRecoverySourceRead {
					calls++
					return unavailableClusterSSHPreparationRecoverySourceRead
				}, download: func(context.Context, clusterbootstrap.Expected, string) (*clusterbootstrap.Bundle, error) {
					calls++
					return nil, errClusterConflict
				}, connect: func(context.Context, clusterremote.Target, clusterremote.HostKey, *clusterremote.Credential) (clusterSSHPreparationRecoverySourceStageClient, error) {
					calls++
					return nil, errClusterConflict
				},
			}
			got, err := reconcileClusterSSHPreparationRecoveryReissuedSourceCohort(context.Background(), value, claim, reissuedProgressAegis(), t.TempDir(), func(ctx context.Context) error { return ctx.Err() }, deps)
			if err == nil || len(got) != 0 || calls != 0 {
				t.Fatal("foreign cohort reached source or native work")
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedSourceStageCohortOrdering(t *testing.T) {
	baseline, manifest, members := recoveryProgressArchiveFixture(t)
	archive := recoveryProgressPackWithCompressionLevel(t, members, gzip.NoCompression)
	for _, mode := range []string{"Q3 pair applied", "Q3 replacement applied", "Q4 pair absent", "Q4 replacement retained absence", "last sibling empty", "last sibling later", "last sibling malformed", "inspection cleanup", "second target failure", "reconcile cleanup", "final input source drift", "final input authority drift", "cancel reconciliation"} {
		t.Run(mode, func(t *testing.T) {
			rounds := 1
			if strings.HasPrefix(mode, "Q4") {
				rounds = 2
			}
			value, claim, secret, get, network := reissuedSourceStageFixture(t, strings.Contains(mode, "replacement"), rounds, baseline)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			denied := false
			check := func(ctx context.Context) error {
				if denied {
					return errClusterConflict
				}
				return ctx.Err()
			}
			parent := t.TempDir()
			opened, closed, cleaned, reconciled, absent := 0, 0, 0, 0, 0
			inspected := make([]int, len(claim.Claims))
			cursors := make([]string, len(claim.Claims))
			for i := range cursors {
				cursors[i] = "stage_source"
				if strings.Contains(mode, "retained absence") {
					cursors[i] = "source_absent"
				}
			}
			deps := clusterSSHPreparationRecoveryReissuedSourceStageDependencies{
				source: func(authority clusterSSHPreparationRecoveryReissuedProofAuthority, payload string, current func(context.Context) error) clusterSSHPreparationRecoverySourceRead {
					return newClusterSSHPreparationRecoveryReissuedSourceRead(authority, payload, current, get, network)
				}, download: func(ctx context.Context, e clusterbootstrap.Expected, scratch string) (*clusterbootstrap.Bundle, error) {
					return recoveryProgressStageFixture(t, ctx, e, manifest, archive, scratch)
				}, closeInputs: func(inputs *clusterbootstrap.PreparationInputs) error {
					cleaned++
					err := inputs.Close()
					if mode == "inspection cleanup" && cleaned == len(claim.Claims) {
						return errors.New("private input cleanup")
					}
					if cleaned == 2*len(claim.Claims) {
						if mode == "final input source drift" {
							secret["metadata"].(map[string]any)["resourceVersion"] = "changed"
						}
						if mode == "final input authority drift" {
							denied = true
						}
					}
					return err
				},
			}
			deps.connect = func(ctx context.Context, target clusterremote.Target, key clusterremote.HostKey, credential *clusterremote.Credential) (clusterSSHPreparationRecoverySourceStageClient, error) {
				index := -1
				for i, item := range value.Cohort.Targets {
					if target.Address == item.Binding.Address && target.Port == item.Binding.Port {
						index = i
						if !reflect.DeepEqual(key, item.Key) || credential == nil {
							t.Fatal("original SSH binding changed")
						}
					}
				}
				if index < 0 {
					return nil, errClusterConflict
				}
				opened++
				last := ""
				record := func(action string) {
					if last != "" {
						t.Error("native actions shared executor")
					}
					last = action
					if action != "inspect" {
						for _, count := range inspected {
							if count == 0 || cleaned < len(claim.Claims) {
								t.Error("journal result preceded complete cohort inspection and cleanup")
							}
						}
					}
				}
				return &sourceStageTestClient{inspect: func() (clusterbootstrap.PreparationProgress, error) {
					record("inspect")
					inspected[index]++
					if index == len(inspected)-1 {
						switch mode {
						case "last sibling empty":
							return clusterbootstrap.PreparationProgress{Step: "empty"}, nil
						case "last sibling later":
							return clusterbootstrap.PreparationProgress{Step: "prepare_runtime_identity"}, nil
						case "last sibling malformed":
							return clusterbootstrap.PreparationProgress{Step: "completed"}, nil
						}
					}
					return clusterbootstrap.PreparationProgress{Step: cursors[index]}, nil
				}, reconcile: func() error {
					record("reconcile")
					reconciled++
					if mode == "cancel reconciliation" {
						cancel()
					}
					if strings.Contains(mode, "absent") || mode == "second target failure" && index == len(claim.Claims)-1 {
						return errors.New("native applied proof unavailable")
					}
					return nil
				}, absent: func() error {
					record("absent")
					absent++
					if mode == "second target failure" && index == len(claim.Claims)-1 {
						return errors.New("native absence unavailable")
					}
					cursors[index] = "source_absent"
					return nil
				}, close: func() error {
					closed++
					if mode == "reconcile cleanup" && last == "reconcile" {
						return errors.New("private SSH cleanup")
					}
					return nil
				}}, nil
			}
			got, err := reconcileClusterSSHPreparationRecoveryReissuedSourceCohort(ctx, value, claim, reissuedProgressAegis(), parent, check, deps)
			valid := strings.HasPrefix(mode, "Q")
			if (err == nil) != valid || valid && len(got) != len(claim.Claims) || !valid && len(got) != 0 || opened != closed {
				t.Fatal("partial or uncertain source result escaped", err, len(got), opened, closed)
			}
			if strings.HasPrefix(mode, "last sibling") || mode == "inspection cleanup" {
				if reconciled != 0 || absent != 0 {
					t.Fatal("rejected sibling allowed journal result")
				}
			}
			if mode == "reconcile cleanup" || mode == "cancel reconciliation" {
				if absent != 0 {
					t.Fatal("failed boundary became absence proof")
				}
			}
			if valid {
				want := "applied"
				if strings.Contains(mode, "absen") {
					want = "not_applied"
				}
				for i, item := range got {
					if item.TargetID != claim.Claims[i].Lease.TargetID || item.Resolution != want {
						t.Fatal("source result lost original cohort binding")
					}
				}
				if cleaned != 2*len(claim.Claims) {
					t.Fatal("missing input cleanup")
				}
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatal("owned scratch retained", err)
			}
		})
	}
}

func TestClusterSSHPreparationRecoveryReissuedSourceStageConsumesFailedHandle(t *testing.T) {
	claim := reissuedProofClaimFixture(t)
	copied := claim
	claim.Claims = append([]clusterSSHPreparationRecoveryTargetClaim(nil), claim.Claims...)
	claim.Claims[0].Lease.Generation++
	var controller *clusterController
	got, err := controller.recoverClusterSSHPreparationReissuedSourceStageWith(context.Background(), nil, claim, t.TempDir(), clusterSSHPreparationRecoveryReissuedSourceStageDependencies{})
	if err == nil || len(got) != 0 || !copied.custody.started.Load() || !copied.custody.failed.Load() {
		t.Fatal("failed source-reconciliation entry retained issued custody")
	}
	if got, err := controller.recoverClusterSSHPreparationReissuedSourceStageWith(context.Background(), nil, copied, t.TempDir(), clusterSSHPreparationRecoveryReissuedSourceStageDependencies{}); err == nil || len(got) != 0 {
		t.Fatal("copied failed reconciliation claim reused")
	}
}
