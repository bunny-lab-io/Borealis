package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
)

// Qualification may acquire inert local inputs under its existing joined read
// scope. This deliberately exposes no path, transfer, install or import method.
type clusterSSHQualificationPostgresArchive interface {
	StorageDemand(context.Context, func(context.Context) error) (clusterbootstrap.PostgresImageDemand, error)
	Close() error
}

type clusterSSHQualificationPostgresAcquire func(context.Context, string, func(context.Context) error) (clusterSSHQualificationPostgresArchive, error)

func acquireClusterSSHQualificationPostgres(ctx context.Context, reference string, check func(context.Context) error) (clusterSSHQualificationPostgresArchive, error) {
	image, err := clusterbootstrap.AcquirePostgresImage(ctx, "", reference, check)
	if err != nil {
		return nil, err
	}
	return image, nil
}

func withClusterSSHQualificationPostgresCapacity(ctx context.Context, source clusterSSHPostgresImage,
	base []clusterSSHFilesystemDemand, check func(context.Context) error, acquire clusterSSHQualificationPostgresAcquire,
	consume func(context.Context, clusterSSHPreparedImageCapacity) error) (result error) {
	if ctx.Err() != nil || !source.valid() || check == nil || acquire == nil || consume == nil || check(ctx) != nil {
		return clusterbootstrap.ErrImageArchive
	}
	image, err := acquire(ctx, source.Resolved, check)
	// Even a failed acquisition must release any returned owned resource.
	if image != nil {
		defer func() {
			if image.Close() != nil {
				result = clusterbootstrap.ErrImageArchive
			}
		}()
	}
	if err != nil || image == nil || ctx.Err() != nil || check(ctx) != nil {
		return clusterbootstrap.ErrImageArchive
	}
	// Native acquisition physically reserves PostgreSQL worker scratch first.
	// Target demand is independently remeasured while that archive stays owned;
	// worker peak is never charged to target filesystems or credited against PGDATA.
	return withClusterSSHPostgresCapacityRead(ctx, source, image.StorageDemand, check, base, consume)
}
