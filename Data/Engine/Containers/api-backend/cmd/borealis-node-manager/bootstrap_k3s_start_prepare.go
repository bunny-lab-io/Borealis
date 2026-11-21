package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"os"
)

// Configured accepts only the complete private inputs, with K3s's generated
// kubeconfig optionally present. It grants no startup or reconciliation itself.
func bootstrapNativeK3sStartActions(expected clusterbootstrap.PreparationExpected, source engineidentity.Binding, material *clusterbootstrap.K3sJoinMaterial,
	guard *clusterbootstrap.ExecutorGuard, executor clusterbootstrap.ExecutorIdentity, check func(context.Context) error) clusterbootstrap.PreparedK3sStartActions {
	configured := func(ctx context.Context) error {
		return withBootstrapK3sJoinRoot(ctx, expected, source, material, check, func(root *os.Root, plan bootstrapK3sJoinFiles) error {
			if verifyBootstrapK3sFilePrefix(root, expected, true, false) != nil || verifyBootstrapK3sJoinFilePhase(root, expected, plan, true, false, true) != nil {
				return errBootstrapSource
			}
			return nil
		})
	}
	return clusterbootstrap.PreparedK3sStartActions{
		Configured: configured,
		CheckFresh: func(ctx context.Context) error {
			if bootstrapNativeK3sJoinActions(expected, source, material, guard, executor, check).Prove(ctx) != nil {
				return errBootstrapSource
			}
			return withBootstrapK3sJoinRoot(ctx, expected, source, material, check, func(root *os.Root, _ bootstrapK3sJoinFiles) error {
				return verifyBootstrapArtifactPaths(root, expected, "start_prepared_k3s", true)
			})
		},
		Start: func(ctx context.Context, hostLock, identityLock *os.File) error {
			return withBootstrapK3sJoinRoot(ctx, expected, source, material, check, func(root *os.Root, plan bootstrapK3sJoinFiles) error {
				if verifyBootstrapArtifactPaths(root, expected, "start_prepared_k3s", true) != nil || verifyBootstrapK3sFilePrefix(root, expected, true, false) != nil || verifyBootstrapK3sJoinFilePrefix(root, expected, plan, true, false) != nil || observeBootstrapInertK3s(ctx, true) != nil {
					return errBootstrapSource
				}
				return runBootstrapHostCommand(ctx, bootstrapHostStartPreparedK3s, hostLock, identityLock, guard, executor, configured)
			})
		},
		Prove: func(ctx context.Context) (clusterbootstrap.K3sStartedObservation, error) {
			var observation clusterbootstrap.K3sStartedObservation
			err := withBootstrapK3sJoinRoot(ctx, expected, source, material, check, func(root *os.Root, plan bootstrapK3sJoinFiles) error {
				stable := bootstrapJoinedHostCheck(expected, func(ctx context.Context) error {
					if check(ctx) != nil || verifyBootstrapK3sFilePrefix(root, expected, true, false) != nil || verifyBootstrapK3sJoinFilePhase(root, expected, plan, true, false, true) != nil {
						return errBootstrapSource
					}
					return nil
				})
				return withBootstrapStartedK3sProcess(ctx, "/proc", readBootstrapStartedK3s, checkSourceKubeletExecutable, stable, func(refresh func(context.Context) error) error {
					if verifyBootstrapK3sGeneratedConfig(root, true) != nil {
						return errBootstrapSource
					}
					var err error
					observation, err = observeBootstrapK3sStartupAt(ctx, root, expected, source, material, refresh, newBootstrapJoinedNodeClient)
					if verifyBootstrapK3sGeneratedConfig(root, true) != nil {
						return errBootstrapSource
					}
					return err
				})
			})
			if err != nil {
				return clusterbootstrap.K3sStartedObservation{}, errBootstrapSource
			}
			return observation, nil
		},
	}
}

func proveBootstrapPreparedK3sStartHost(ctx context.Context, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding, material *clusterbootstrap.K3sJoinMaterial, check func(context.Context) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return errBootstrapSource
	}
	return proveBootstrapPreparedHostWith(ctx, expected, check, bootstrapPreparedHostObservers{
		repository: func(ctx context.Context) error { return proveBootstrapDockerRepository(ctx, expected, check) },
		files: func(ctx context.Context) error {
			return withBootstrapK3sJoinRoot(ctx, expected, source, material, check, func(root *os.Root, plan bootstrapK3sJoinFiles) error {
				return verifyBootstrapK3sJoinFilePhase(root, expected, plan, true, false, true)
			})
		},
		dependencies: func(ctx context.Context) error { return proveBootstrapPrejoinDependencies(ctx, check) },
		kernel:       func(ctx context.Context) error { return proveBootstrapPrejoinKernel(ctx, check) },
		firewall:     func(ctx context.Context) error { return proveBootstrapPrejoinFirewall(ctx, expected, check) },
	})
}
