package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"os"
)

// Invoke only inside held pre-join source/account/identity custody. This proves
// the native host portion; it never releases a fence, runs preparation, joins
// K3s or grants controller settlement/admission authority on its own.
func proveBootstrapPreparedHost(ctx context.Context, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return errBootstrapSource
	}
	return proveBootstrapPreparedHostWith(ctx, expected, check, bootstrapPreparedHostObservers{
		repository:   func(ctx context.Context) error { return proveBootstrapDockerRepository(ctx, expected, check) },
		files:        func(ctx context.Context) error { return proveBootstrapPrejoinHostFiles(ctx, expected, check) },
		dependencies: func(ctx context.Context) error { return proveBootstrapPrejoinDependencies(ctx, check) },
		kernel:       func(ctx context.Context) error { return proveBootstrapPrejoinKernel(ctx, check) },
		firewall:     func(ctx context.Context) error { return proveBootstrapPrejoinFirewall(ctx, expected, check) },
	})
}

type bootstrapPreparedHostObservers struct {
	repository, files, dependencies, kernel, firewall func(context.Context) error
}

func proveBootstrapPreparedHostWith(ctx context.Context, expected clusterbootstrap.PreparationExpected, check func(context.Context) error, observers bootstrapPreparedHostObservers) error {
	if expected.Validate() != nil || check == nil || observers.repository == nil || observers.files == nil || observers.dependencies == nil || observers.kernel == nil || observers.firewall == nil {
		return errBootstrapSource
	}
	boundary := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	for pass := 0; pass < 2; pass++ {
		for _, observe := range []func(context.Context) error{observers.repository, observers.files, observers.dependencies, observers.kernel, observers.firewall} {
			if boundary() != nil || observe(ctx) != nil || boundary() != nil {
				return errBootstrapSource
			}
		}
	}
	// Files bracket final loaded-state proof; no successful command can hide
	// changes to baseline, mirrors, script or unit during native observation.
	if observers.repository(ctx) != nil || boundary() != nil || observers.files(ctx) != nil || boundary() != nil {
		return errBootstrapSource
	}
	return nil
}
