package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"strings"
	"testing"
)

func TestBootstrapTargetCPUBeforeMutation(t *testing.T) {
	for _, mode := range []string{"fresh", "missing fresh proof", "legacy recovery", "CPU drift", "plan drift", "reader failure", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			expected := bootstrapHostFilesFixture()
			plan, _ := clusterbootstrap.PreparationTargetCPUPlanDigest(expected.K3sVersion)
			expected.TargetCPU = &clusterbootstrap.PreparationTargetCPU{Version: 1, PlanSHA256: plan, OnlineCPUs: "0-15", AllocatableMilli: 16000, MemoryKiB: 33554432, ObservationSHA256: strings.Repeat("d", 64)}
			action := "stage_source"
			if mode == "missing fresh proof" || mode == "legacy recovery" {
				expected.TargetCPU = nil
			}
			if mode == "legacy recovery" {
				action = "reconcile_source"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			reads := 0
			err := verifyBootstrapTargetCPU(ctx, expected, action, func(ctx context.Context, version string, check func(context.Context) error) (clusterbootstrap.PreparationTargetCPU, error) {
				reads++
				if version != expected.K3sVersion || check(ctx) != nil {
					t.Fatal("native read changed contract")
				}
				out := *expected.TargetCPU
				switch mode {
				case "CPU drift":
					out.ObservationSHA256 = strings.Repeat("e", 64)
				case "plan drift":
					out.PlanSHA256 = strings.Repeat("e", 64)
				case "reader failure":
					return clusterbootstrap.PreparationTargetCPU{}, clusterbootstrap.ErrPreparationConfig
				}
				return out, nil
			})
			if (err == nil) != (mode == "fresh" || mode == "legacy recovery") {
				t.Fatal("unsafe first effect admitted", err)
			}
			if (mode == "missing fresh proof" || mode == "legacy recovery" || mode == "cancelled") && reads != 0 {
				t.Fatal("invalid/legacy binding entered native read")
			}
		})
	}
}
