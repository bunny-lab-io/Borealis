package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"reflect"
)

// Reserved preparation and inert qualification acquisition share measured demand.
// This value is conditional demand, never a target-space or admission receipt.
type clusterSSHPreparedImageCapacity struct {
	Target                    []clusterSSHFilesystemDemand
	PostgresWorkerPeakBytes   uint64
	PostgresWorkerPeakEntries uint64
}

func withClusterSSHPostgresCapacity(ctx context.Context, source clusterSSHPostgresImage,
	image *clusterbootstrap.PostgresImage, check func(context.Context) error,
	base []clusterSSHFilesystemDemand, consume func(context.Context, clusterSSHPreparedImageCapacity) error) error {
	if image == nil {
		return clusterbootstrap.ErrImageArchive
	}
	return withClusterSSHPostgresCapacityRead(ctx, source, image.StorageDemand, check, base, consume)
}

// Private test seam: production accepts only the opaque acquired image above.
func withClusterSSHPostgresCapacityRead(ctx context.Context, source clusterSSHPostgresImage,
	read func(context.Context, func(context.Context) error) (clusterbootstrap.PostgresImageDemand, error), check func(context.Context) error,
	base []clusterSSHFilesystemDemand, consume func(context.Context, clusterSSHPreparedImageCapacity) error) error {
	if !source.valid() || read == nil || check == nil || consume == nil || ctx.Err() != nil || check(ctx) != nil {
		return clusterbootstrap.ErrImageArchive
	}
	measured, err := read(ctx, check)
	if err != nil || measured.Reference != source.Resolved || measured.IncomingBytes == 0 || measured.IncomingEntries != 1 ||
		measured.RuntimeBytes == 0 || measured.RuntimeEntries == 0 || measured.WorkerPeakBytes == 0 || measured.WorkerPeakEntries == 0 || check(ctx) != nil {
		return clusterbootstrap.ErrImageArchive
	}
	demands, err := mergeClusterSSHFilesystemDemands(base, []clusterSSHFilesystemDemand{
		{Path: "/opt/Borealis", Bytes: measured.IncomingBytes, Entries: measured.IncomingEntries},
		{Path: "/var/lib/rancher/k3s", Bytes: measured.RuntimeBytes, Entries: measured.RuntimeEntries},
	})
	if err != nil {
		return err
	}
	capacity := clusterSSHPreparedImageCapacity{Target: demands, PostgresWorkerPeakBytes: measured.WorkerPeakBytes, PostgresWorkerPeakEntries: measured.WorkerPeakEntries}
	if consume(ctx, capacity) != nil || ctx.Err() != nil || check(ctx) != nil {
		return clusterbootstrap.ErrImageArchive
	}
	// Detect closed/tampered archives and loss of the same original source/cohort
	// after consumption. A copied historical proof never enters this path.
	fresh, err := read(ctx, check)
	if err != nil || !reflect.DeepEqual(measured, fresh) || ctx.Err() != nil || check(ctx) != nil {
		return clusterbootstrap.ErrImageArchive
	}
	return nil
}
