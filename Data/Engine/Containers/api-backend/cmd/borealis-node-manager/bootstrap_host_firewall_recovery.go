package main

import (
	"context"
	"os"

	"borealis/api-backend/internal/clusterbootstrap"
)

func bootstrapNativeFirewallRecovery(expected clusterbootstrap.PreparationExpected, guard *clusterbootstrap.ExecutorGuard, identity clusterbootstrap.ExecutorIdentity, caller func(context.Context) error) clusterbootstrap.PrejoinFirewallRecoveryActions {
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
	return clusterbootstrap.PrejoinFirewallRecoveryActions{
		ProvePartial: func(ctx context.Context) error {
			root, err := open()
			if err != nil {
				return errBootstrapSource
			}
			defer root.Close()
			return proveBootstrapFirewallHost(ctx, root, expected, check)
		},
		Complete: func(ctx context.Context, host, identityLock *os.File) error {
			root, err := open()
			if err != nil {
				return errBootstrapSource
			}
			defer root.Close()
			if proveBootstrapFirewallHost(ctx, root, expected, check) != nil {
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
			files, err := bootstrapDockerRepositoryFiles(expected, platform.suite, key)
			if err != nil {
				return errBootstrapSource
			}
			hostFiles, err := bootstrapPrejoinHostFiles(expected)
			if err != nil {
				return errBootstrapSource
			}
			files = append(files, hostFiles...)
			pinned, err := pinBootstrapPublishedHostFiles(ctx, root, files, check)
			if err != nil {
				return errBootstrapSource
			}
			return completeBootstrapFirewallWith(ctx, pinned,
				func(ctx context.Context) error { return proveBootstrapFirewallHost(ctx, root, expected, pinned) },
				func(ctx context.Context) error {
					return runBootstrapHostCommand(ctx, bootstrapHostEnableFirewall, host, identityLock, guard, identity, pinned)
				},
				func(ctx context.Context) error { return proveBootstrapPreparedHost(ctx, expected, pinned) })
		},
		Prove: func(ctx context.Context) error { return proveBootstrapPreparedHost(ctx, expected, check) },
	}
}

func proveBootstrapFirewallHost(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	read := func(ctx context.Context, probe string) ([]byte, error) {
		if probe == "inventory" {
			return readBootstrapDockerProbe(ctx, "packages")
		}
		if probe == "architecture" {
			return readBootstrapDockerProbe(ctx, probe)
		}
		return readBootstrapHostDependency(ctx, probe)
	}
	return proveBootstrapHostRecoveryWith(ctx, check, read,
		func(ctx context.Context) error { return proveBootstrapDockerRepositoryWith(ctx, root, expected, check) },
		func(ctx context.Context) error { return proveBootstrapPrejoinHostFilesWith(ctx, root, expected, check) },
		func(ctx context.Context) error { return proveBootstrapPrejoinKernel(ctx, check) },
		func(ctx context.Context) error {
			return proveBootstrapPartialFirewallWith(ctx, expected, check, readBootstrapHostFirewall)
		}, true)
}

func completeBootstrapFirewallWith(ctx context.Context, check, partial, run, prove func(context.Context) error) error {
	if check == nil || partial == nil || run == nil || prove == nil {
		return errBootstrapSource
	}
	// The fixed script atomically replaces only its exact proven owned prefix;
	// other filter rules remain. Existing INPUT jumps must match construction
	// order, so appending missing jumps cannot preserve an earlier bypass.
	for _, step := range []func(context.Context) error{partial, run, prove} {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil || step(ctx) != nil || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
	}
	return nil
}
