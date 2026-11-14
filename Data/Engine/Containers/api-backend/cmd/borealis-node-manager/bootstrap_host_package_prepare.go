package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"io"
	"os"
)

// Package files remain inside the verified input owner while existing host
// journal code holds both fences. Only fixed APT cache metadata is added here;
// failed/foreign cleanup cannot become a successful preparation receipt.
func withBootstrapHostPackageCache(ctx context.Context, inputs *clusterbootstrap.PreparationInputs, expected clusterbootstrap.PreparationExpected,
	check func(context.Context) error, consume func(context.Context, string) error) error {
	if consume == nil {
		return errBootstrapSource
	}
	return inputs.WithHostPackages(ctx, expected, check, func(ctx context.Context, packages *clusterbootstrap.HostPackageSet) error {
		return packages.WithArchives(ctx, check, func(ctx context.Context, directory string, _ []clusterbootstrap.HostPackagePin) error {
			return withBootstrapAPTCacheMetadata(ctx, directory, check, consume)
		})
	})
}

func withBootstrapAPTCacheMetadata(ctx context.Context, directory string, check func(context.Context) error, consume func(context.Context, string) error) (result error) {
	if check == nil || consume == nil || ctx.Err() != nil || check(ctx) != nil {
		return errBootstrapSource
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return errBootstrapSource
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	owned := map[string]os.FileInfo{}
	defer func() {
		for _, name := range []string{"partial", "lock"} {
			before := owned[name]
			if before == nil {
				continue
			}
			after, err := root.Lstat(name)
			if err != nil || !os.SameFile(before, after) || root.Remove(name) != nil {
				result = errors.Join(result, errBootstrapSource)
			}
		}
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			result = errors.Join(result, errBootstrapSource)
		}
	}()
	lock, err := root.OpenFile("lock", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return errBootstrapSource
	}
	owned["lock"], err = lock.Stat()
	if closeErr := lock.Close(); err != nil || closeErr != nil {
		return errBootstrapSource
	}
	if root.Mkdir("partial", 0700) != nil {
		return errBootstrapSource
	}
	owned["partial"], err = root.Lstat("partial")
	if err != nil || check(ctx) != nil || ctx.Err() != nil {
		return errBootstrapSource
	}
	if consume(ctx, directory) != nil {
		return errBootstrapSource
	}
	return nil
}

func prepareBootstrapHostPackages(ctx context.Context, directory string, phase bootstrapHostCommand, preflight bool,
	hostLock, identityLock *os.File, guard *clusterbootstrap.ExecutorGuard, identity clusterbootstrap.ExecutorIdentity, check func(context.Context) error) error {
	return prepareBootstrapHostPackagesWith(ctx, directory, phase, preflight, check,
		func(ctx context.Context) ([]byte, error) { return readBootstrapDockerProbe(ctx, "packages") },
		func(ctx context.Context, spec bootstrapHostCommandSpec, simulate bool) ([]byte, error) {
			output := &bootstrapDockerOutput{}
			err := runBootstrapHostSpec(ctx, spec, hostLock, identityLock, guard, identity, check,
				func(ctx context.Context, spec bootstrapHostCommandSpec, host, identity *os.File) error {
					var sink io.Writer = io.Discard
					if simulate {
						sink = output
					}
					return executeBootstrapHostCommandOutput(ctx, spec, host, identity, sink)
				})
			return output.Bytes(), err
		})
}

func prepareBootstrapHostPackagesWith(ctx context.Context, directory string, phase bootstrapHostCommand, preflight bool,
	check func(context.Context) error, read func(context.Context) ([]byte, error), run func(context.Context, bootstrapHostCommandSpec, bool) ([]byte, error)) error {
	boundary := func() error {
		if check == nil || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	if read == nil || run == nil || boundary() != nil {
		return errBootstrapSource
	}
	before, err := read(ctx)
	if err != nil || boundary() != nil {
		return errBootstrapSource
	}
	var plan *bootstrapHostPackagePlan
	if preflight {
		plan, err = newBootstrapHostPackagePreflightPlan(directory, before)
	} else {
		plan, err = newBootstrapHostPackagePlan(directory, phase, before)
	}
	if err != nil {
		return errBootstrapSource
	}
	simulation, err := plan.simulationCommand()
	if err != nil {
		return errBootstrapSource
	}
	observed, err := run(ctx, simulation, true)
	if err != nil || boundary() != nil || plan.validateSimulation(observed) != nil {
		return errBootstrapSource
	}
	current, err := read(ctx)
	if err != nil || plan.validateBefore(current) != nil || boundary() != nil {
		return errBootstrapSource
	}
	if preflight || !plan.needsInstall() {
		return nil
	}
	install, err := plan.installCommand()
	if err != nil {
		return errBootstrapSource
	}
	if _, err := run(ctx, install, false); err != nil || boundary() != nil {
		return errBootstrapSource
	}
	current, err = read(ctx)
	if err != nil || plan.validateResult(current) != nil || boundary() != nil {
		return errBootstrapSource
	}
	return nil
}
