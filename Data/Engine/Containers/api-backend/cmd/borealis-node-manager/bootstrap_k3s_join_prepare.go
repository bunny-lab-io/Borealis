package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"os"
)

func bootstrapK3sConfigurationAction(action string) bool {
	return action == "configure_prejoin_k3s" || action == "reconcile_prejoin_k3s_configuration"
}

func withBootstrapK3sJoinRoot(ctx context.Context, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding, material *clusterbootstrap.K3sJoinMaterial,
	check func(context.Context) error, consume func(*os.Root, bootstrapK3sJoinFiles) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 || check == nil || consume == nil || ctx.Err() != nil || check(ctx) != nil {
		return errBootstrapSource
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return errBootstrapSource
	}
	defer root.Close()
	return withBootstrapK3sJoinFiles(ctx, expected, source, material, check, func(plan bootstrapK3sJoinFiles) error {
		if consume(root, plan) != nil || ctx.Err() != nil || check(ctx) != nil {
			return errBootstrapSource
		}
		return nil
	})
}

func bootstrapNativeK3sJoinActions(expected clusterbootstrap.PreparationExpected, source engineidentity.Binding, material *clusterbootstrap.K3sJoinMaterial,
	guard *clusterbootstrap.ExecutorGuard, executor clusterbootstrap.ExecutorIdentity, check func(context.Context) error) clusterbootstrap.PrejoinK3sJoinActions {
	return clusterbootstrap.PrejoinK3sJoinActions{
		Installed: bootstrapNativeK3sActions(expected, guard, executor, check).Prove,
		CheckFresh: func(ctx context.Context) error {
			return withBootstrapK3sJoinRoot(ctx, expected, source, material, check, func(root *os.Root, plan bootstrapK3sJoinFiles) error {
				return verifyBootstrapK3sJoinFilePrefix(root, expected, plan, false, true)
			})
		},
		Configure: func(ctx context.Context, hostLock, identityLock *os.File) error {
			if hostLock == nil || identityLock == nil {
				return errBootstrapSource
			}
			return withBootstrapK3sJoinRoot(ctx, expected, source, material, check, func(root *os.Root, plan bootstrapK3sJoinFiles) error {
				if observeBootstrapInertK3s(ctx, true) != nil || installBootstrapK3sJoinFiles(ctx, root, expected, plan, check) != nil || observeBootstrapInertK3s(ctx, true) != nil {
					return errBootstrapSource
				}
				return nil
			})
		},
		Prove: func(ctx context.Context) error {
			return withBootstrapK3sJoinRoot(ctx, expected, source, material, check, func(root *os.Root, plan bootstrapK3sJoinFiles) error {
				for pass := 0; pass < 2; pass++ {
					if check(ctx) != nil || verifyBootstrapK3sJoinFilePrefix(root, expected, plan, true, false) != nil || observeBootstrapInertK3s(ctx, true) != nil {
						return errBootstrapSource
					}
				}
				return nil
			})
		},
	}
}

func proveBootstrapPreparedK3sJoinHost(ctx context.Context, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding, material *clusterbootstrap.K3sJoinMaterial, check func(context.Context) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return errBootstrapSource
	}
	return proveBootstrapPreparedHostWith(ctx, expected, check, bootstrapPreparedHostObservers{
		repository: func(ctx context.Context) error { return proveBootstrapDockerRepository(ctx, expected, check) },
		files: func(ctx context.Context) error {
			return withBootstrapK3sJoinRoot(ctx, expected, source, material, check, func(root *os.Root, plan bootstrapK3sJoinFiles) error {
				return verifyBootstrapK3sJoinFilePrefix(root, expected, plan, false, false)
			})
		},
		dependencies: func(ctx context.Context) error { return proveBootstrapPrejoinDependencies(ctx, check) },
		kernel:       func(ctx context.Context) error { return proveBootstrapPrejoinKernel(ctx, check) },
		firewall:     func(ctx context.Context) error { return proveBootstrapPrejoinFirewall(ctx, expected, check) },
	})
}
