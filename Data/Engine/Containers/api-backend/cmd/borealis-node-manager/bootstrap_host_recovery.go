package main

import (
	"bytes"
	"context"
	"os"

	"borealis/api-backend/internal/clusterbootstrap"
)

func bootstrapNativeHostRecovery(expected clusterbootstrap.PreparationExpected, guard *clusterbootstrap.ExecutorGuard,
	identity clusterbootstrap.ExecutorIdentity, caller func(context.Context) error) clusterbootstrap.PrejoinHostRecoveryActions {
	check := func(ctx context.Context) error {
		if guard == nil || caller == nil || ctx.Err() != nil || guard.CheckAuthority(ctx) != nil || caller(ctx) != nil || guard.CheckAuthority(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	open := func() (*os.Root, error) {
		if os.Geteuid() != 0 || os.Getegid() != 0 {
			return nil, errBootstrapSource
		}
		return os.OpenRoot("/")
	}
	return clusterbootstrap.PrejoinHostRecoveryActions{
		ProveConfigured: func(ctx context.Context) error {
			root, err := open()
			if err != nil {
				return errBootstrapSource
			}
			defer root.Close()
			return proveBootstrapConfiguredHost(ctx, root, expected, check)
		},
		Complete: func(ctx context.Context, host, identityLock *os.File) error {
			root, err := open()
			if err != nil {
				return errBootstrapSource
			}
			defer root.Close()
			if proveBootstrapConfiguredHost(ctx, root, expected, check) != nil {
				return errBootstrapSource
			}
			platform, err := observeBootstrapDockerPlatform(root)
			if err != nil {
				return errBootstrapSource
			}
			key, _, err := readBootstrapDockerFile(root, "etc/apt/keyrings/docker.asc", bootstrapDockerKeyLimit)
			if err != nil {
				return errBootstrapSource
			}
			published, err := bootstrapDockerRepositoryFiles(expected, platform.suite, key)
			if err != nil {
				return errBootstrapSource
			}
			pinned, err := pinBootstrapPublishedHostFiles(ctx, root, published, check)
			if err != nil {
				return errBootstrapSource
			}
			boundary := func(ctx context.Context) error { return pinned(ctx) }
			command := func(ctx context.Context, step bootstrapHostCommand) error {
				return runBootstrapHostCommand(ctx, step, host, identityLock, guard, identity, boundary)
			}
			return completeBootstrapConfiguredHostWith(ctx, host, identityLock, boundary, bootstrapConfiguredHostOperations{
				configured: func(ctx context.Context) error { return proveBootstrapConfiguredHost(ctx, root, expected, boundary) },
				files: func(ctx context.Context) error {
					if completeBootstrapHostFilesWith(ctx, root, expected, boundary) != nil {
						return errBootstrapSource
					}
					files, err := bootstrapPrejoinHostFiles(expected)
					if err != nil {
						return errBootstrapSource
					}
					published = append(published, files...)
					next, err := pinBootstrapPublishedHostFiles(ctx, root, published, check)
					if err != nil || boundary(ctx) != nil {
						return errBootstrapSource
					}
					pinned = next
					return boundary(ctx)
				},
				kernel: func(ctx context.Context) error { return proveBootstrapPrejoinKernel(ctx, boundary) },
				multipath: func(ctx context.Context) error {
					return prepareBootstrapMultipathWith(ctx, boundary, readBootstrapHostDependency, func(ctx context.Context) error { return proveBootstrapPrejoinKernel(ctx, boundary) }, command)
				},
				iscsi: func(ctx context.Context) error {
					return completeBootstrapISCSIWith(ctx, boundary, readBootstrapHostDependency, command)
				},
				firewall: func(ctx context.Context) error {
					if proveBootstrapRecoveryUnitWith(ctx, "borealis-k3s-api-firewall.service", boundary, readBootstrapHostDependency) != nil {
						return errBootstrapSource
					}
					return proveBootstrapFreshFirewall(ctx, boundary, readBootstrapHostFirewall)
				},
				prove: func(ctx context.Context) error { return proveBootstrapPreparedHost(ctx, expected, boundary) }, command: command,
			})
		},
		Prove: func(ctx context.Context) error { return proveBootstrapPreparedHost(ctx, expected, check) },
	}
}

func proveBootstrapConfiguredHost(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	read := func(ctx context.Context, probe string) ([]byte, error) {
		if probe == "inventory" {
			return readBootstrapDockerProbe(ctx, "packages")
		}
		if probe == "architecture" {
			return readBootstrapDockerProbe(ctx, probe)
		}
		return readBootstrapHostDependency(ctx, probe)
	}
	return proveBootstrapConfiguredHostWith(ctx, check, read,
		func(ctx context.Context) error { return proveBootstrapDockerRepositoryWith(ctx, root, expected, check) },
		func(context.Context) error { return proveBootstrapHostFilePrefix(root, expected, false) },
		func(ctx context.Context) error { return proveBootstrapKernel(ctx, check, true) },
		func(ctx context.Context) error {
			return proveBootstrapFreshFirewall(ctx, check, readBootstrapHostFirewall)
		})
}

type bootstrapConfiguredHostOperations struct {
	configured, files, kernel, multipath, iscsi, firewall, prove func(context.Context) error
	command                                                      func(context.Context, bootstrapHostCommand) error
}

func completeBootstrapConfiguredHostWith(ctx context.Context, host, identity *os.File, check func(context.Context) error, o bootstrapConfiguredHostOperations) error {
	if host == nil || identity == nil || check == nil || o.configured == nil || o.files == nil || o.kernel == nil || o.multipath == nil || o.iscsi == nil || o.firewall == nil || o.prove == nil || o.command == nil {
		return errBootstrapSource
	}
	h, he := host.Stat()
	i, ie := identity.Stat()
	if he != nil || ie != nil || !h.Mode().IsRegular() || !i.IsDir() {
		return errBootstrapSource
	}
	command := func(step bootstrapHostCommand) func(context.Context) error {
		return func(ctx context.Context) error { return o.command(ctx, step) }
	}
	// Configured packages and running empty Docker were independently proved.
	// Never invoke apt, replace repository files, or restart the Docker daemon.
	for _, step := range []func(context.Context) error{o.configured, o.files, command(bootstrapHostLoadISCSI), o.kernel, o.multipath, command(bootstrapHostReloadUnits), o.iscsi, o.firewall, command(bootstrapHostEnableFirewall), o.prove} {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil || step(ctx) != nil || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
	}
	return nil
}

func proveBootstrapRecoveryUnitWith(ctx context.Context, unit string, check func(context.Context) error, read func(context.Context, string) ([]byte, error)) error {
	if check == nil || read == nil {
		return errBootstrapSource
	}
	var previous []byte
	for pass := 0; pass < 2; pass++ {
		if ctx.Err() != nil || check(ctx) != nil {
			return errBootstrapSource
		}
		raw, err := read(ctx, unit)
		if err != nil || ctx.Err() != nil || check(ctx) != nil || validateBootstrapRecoverableUnit(unit, raw) != nil || pass > 0 && !bytes.Equal(previous, raw) {
			return errBootstrapSource
		}
		previous = bytes.Clone(raw)
	}
	return nil
}

func completeBootstrapISCSIWith(ctx context.Context, check func(context.Context) error, read func(context.Context, string) ([]byte, error), run func(context.Context, bootstrapHostCommand) error) error {
	if run == nil || proveBootstrapRecoveryUnitWith(ctx, "iscsid.service", check, read) != nil {
		return errBootstrapSource
	}
	raw, err := read(ctx, "iscsid.service")
	if err != nil || ctx.Err() != nil || check(ctx) != nil || validateBootstrapRecoverableUnit("iscsid.service", raw) != nil {
		return errBootstrapSource
	}
	if validateBootstrapHostDependency("iscsid.service", raw) != nil {
		if run(ctx, bootstrapHostEnableISCSI) != nil {
			return errBootstrapSource
		}
	}
	var previous []byte
	for pass := 0; pass < 2; pass++ {
		if ctx.Err() != nil || check(ctx) != nil {
			return errBootstrapSource
		}
		raw, err := read(ctx, "iscsid.service")
		if err != nil || ctx.Err() != nil || check(ctx) != nil || validateBootstrapHostDependency("iscsid.service", raw) != nil || pass > 0 && !bytes.Equal(previous, raw) {
			return errBootstrapSource
		}
		previous = bytes.Clone(raw)
	}
	return nil
}
