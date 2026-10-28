package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"math"
	"reflect"
	"testing"
)

func TestClusterSSHPreparationWorkerScratchBudget(t *testing.T) {
	f := imageReleaseFixture(t)
	addExternalReleaseFixture(t, f)
	addK3sReleaseFixture(t, f)
	images, err := resolveClusterSSHImageInventory(bootstrapContext(), f.expected)
	if err != nil {
		t.Fatal(err)
	}
	external, err := resolveClusterSSHExternalInventory(bootstrapContext(), f.expected)
	if err != nil {
		t.Fatal(err)
	}
	k3s, err := resolveClusterSSHK3sInventory(bootstrapContext(), f.expected, clusterbootstrap.K3sPins().Version)
	if err != nil {
		t.Fatal(err)
	}
	bundle := clusterbootstrap.PreparationScratchDemand{Bytes: 1200, Entries: 10}
	base := clusterSSHPreparedImageCapacity{PostgresWorkerPeakBytes: 190, PostgresWorkerPeakEntries: 5, Target: []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 999, Entries: 7}}}
	wantBytes := uint64(1390)
	for _, asset := range f.release.Assets {
		if asset.Name != clusterbootstrap.ImageInventoryName && asset.Name != clusterbootstrap.ExternalInventoryName && asset.Name != clusterbootstrap.K3sInventoryName {
			wantBytes += uint64(asset.Size)
		}
	}
	for _, mode := range []string{"valid", "missing bundle", "bundle overflow", "missing apps", "missing external", "missing k3s", "k3s source", "k3s version", "release mismatch", "worker overflow", "worker inode overflow"} {
		t.Run(mode, func(t *testing.T) {
			b, a, e, k, c := bundle, images, external, k3s, base
			switch mode {
			case "missing bundle":
				b.Bytes = 0
			case "bundle overflow":
				b.Bytes = math.MaxUint64
			case "missing apps":
				a.inventory = nil
			case "missing external":
				e.inventory = nil
			case "missing k3s":
				k.inventory = nil
			case "k3s source":
				k.expected.SourceSHA = "different"
			case "k3s version":
				k.version = "different"
			case "release mismatch":
				e.expected.SourceSHA = "different"
			case "worker overflow":
				c.PostgresWorkerPeakBytes = clusterSSHCapacityLimit
			case "worker inode overflow":
				c.PostgresWorkerPeakEntries = math.MaxUint64 - 1
			}
			got, err := clusterSSHPreparationWorkerCapacity(b, a, e, k, c)
			if (err == nil) != (mode == "valid") {
				t.Fatal("worker budget boundary", err)
			}
			if mode == "valid" && (got.WorkerScratchBytes != wantBytes || got.WorkerScratchEntries != 53 || got.PostgresWorkerPeakBytes != 190 || !reflect.DeepEqual(got.Target, base.Target)) {
				t.Fatal("worker contents mixed with target/generation budgets", got)
			}
		})
	}
}
