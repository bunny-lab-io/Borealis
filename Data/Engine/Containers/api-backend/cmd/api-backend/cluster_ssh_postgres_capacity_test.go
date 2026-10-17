package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestClusterSSHPostgresCapacityScope(t *testing.T) {
	for _, mode := range []string{"valid", "wrong image", "missing image", "authority before", "source changes", "measurement changes", "closed archive", "consumer failure", "cancelled", "overflow", "copy isolation"} {
		t.Run(mode, func(t *testing.T) {
			source := clusterSSHPostgresImage{Configured: sshPostgresConfiguredFixture, Resolved: sshPostgresResolvedFixture, Bootstrap: sshPostgresBootstrapFixture()}
			measured := clusterbootstrap.PostgresImageDemand{Reference: source.Resolved, IncomingBytes: 100, IncomingEntries: 1, RuntimeBytes: 1000, RuntimeEntries: 10, WorkerPeakBytes: 190, WorkerPeakEntries: 5}
			base := []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 500, Entries: 2}, {Path: "/var/lib/rancher/k3s", Bytes: 2000, Entries: 20}}
			original := append([]clusterSSHFilesystemDemand(nil), base...)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, reads, consumed := 0, 0, 0
			check := func(context.Context) error {
				calls++
				if mode == "authority before" || mode == "source changes" && consumed > 0 {
					return errors.New("changed original source")
				}
				return nil
			}
			read := func(ctx context.Context, check func(context.Context) error) (clusterbootstrap.PostgresImageDemand, error) {
				reads++
				if check(ctx) != nil {
					return clusterbootstrap.PostgresImageDemand{}, errors.New("lost")
				}
				if mode == "closed archive" && reads > 1 {
					return clusterbootstrap.PostgresImageDemand{}, errors.New("closed")
				}
				d := measured
				if mode == "measurement changes" && reads > 1 {
					d.RuntimeBytes++
				}
				return d, nil
			}
			switch mode {
			case "wrong image":
				measured.Reference = clusterSSHPostgresRepository + "@sha256:" + strings.Repeat("f", 64)
			case "missing image":
				source = clusterSSHPostgresImage{}
			case "cancelled":
				cancel()
			case "overflow":
				base[0].Bytes = clusterSSHCapacityLimit
			}
			err := withClusterSSHPostgresCapacityRead(ctx, source, read, check, base, func(ctx context.Context, c clusterSSHPreparedImageCapacity) error {
				consumed++
				if c.PostgresWorkerPeakBytes != 190 || c.PostgresWorkerPeakEntries != 5 || !reflect.DeepEqual(c.Target, []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 600, Entries: 3}, {Path: "/var/lib/rancher/k3s", Bytes: 3000, Entries: 30}}) {
					t.Fatal("worker/target budgets mixed")
				}
				if mode == "consumer failure" {
					return errors.New("failed")
				}
				if mode == "copy isolation" {
					c.Target[0].Bytes = 1
				}
				return nil
			})
			good := mode == "valid" || mode == "copy isolation"
			if (err == nil) != good {
				t.Fatalf("scope result: %v", err)
			}
			if good && (reads != 2 || consumed != 1 || calls < 4 || !reflect.DeepEqual(base, original)) {
				t.Fatal("missing boundaries or borrowed demands")
			}
			if (mode == "authority before" || mode == "missing image" || mode == "cancelled") && reads != 0 {
				t.Fatal("invalid authority reached measurement")
			}
			if (mode == "wrong image" || mode == "overflow") && consumed != 0 {
				t.Fatal("invalid demand reached consumer")
			}
		})
	}
}

func TestClusterSSHPostgresCapacitySharedFilesystem(t *testing.T) {
	f := newSSHStorageFixture(t, false)
	storage := sshStorageSnapshotFixture(t, f.a).Requirements
	d := clusterbootstrap.PostgresImageDemand{Reference: storage.PostgresImage.Resolved, IncomingBytes: 100, IncomingEntries: 1, RuntimeBytes: 1000, RuntimeEntries: 10, WorkerPeakBytes: 190, WorkerPeakEntries: 5}
	read := func(context.Context, func(context.Context) error) (clusterbootstrap.PostgresImageDemand, error) {
		return d, nil
	}
	err := withClusterSSHPostgresCapacityRead(context.Background(), storage.PostgresImage, read, func(context.Context) error { return nil }, nil, func(ctx context.Context, c clusterSSHPreparedImageCapacity) error {
		e := sshCapacityEvidence()
		e.Paths = append(e.Paths, clusterremote.FilesystemPath{Path: "/var/lib/rancher/k3s", Ancestor: "/var/lib", Filesystem: "root"})
		budgets, err := calculateClusterSSHStorageCapacity(storage, e, c.Target)
		if err != nil || len(budgets) != 1 || budgets[0].OtherBytes != 1100+11*4096 || budgets[0].ReplicaBytes != storage.ArtifactReplicaBytes+storage.PostgresInstanceBytes {
			t.Fatal("shared space, allocation reserve or PGDATA accounting changed", err)
		}
		// Worker scratch is not a target filesystem charge; PGDATA remains volume demand.
		e.Filesystems[0].AvailableBytes = budgets[0].OtherBytes + budgets[0].ReplicaBytes + budgets[0].RebuildBytes + budgets[0].MinimumFreeBytes
		budgets, err = calculateClusterSSHStorageCapacity(storage, e, c.Target)
		if err != nil || budgets[0].Fits {
			t.Fatal("exact physical boundary accepted", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
