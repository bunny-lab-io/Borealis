package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// Only PreparationInputs.PreparePrejoinHost may consume this adapter: it must
// retain source/account/identity custody, durable prepare_host intent and BOTH
// locks. Only the fixed supervised host action selects it; no poller invokes
// preparation yet. The caller's custody check uses the separate host phase; earlier source
// and identity-prefix checks deliberately still reject prepared host files.
func bootstrapNativeHostActions(expected clusterbootstrap.PreparationExpected, guard *clusterbootstrap.ExecutorGuard,
	identity clusterbootstrap.ExecutorIdentity, check func(context.Context) error) clusterbootstrap.PrejoinHostActions {
	return bootstrapNativeHostActionsFor(expected, guard, identity, check, false)
}

func bootstrapNativeRepositoryHostActions(expected clusterbootstrap.PreparationExpected, guard *clusterbootstrap.ExecutorGuard,
	identity clusterbootstrap.ExecutorIdentity, check func(context.Context) error) clusterbootstrap.PrejoinHostActions {
	return bootstrapNativeHostActionsFor(expected, guard, identity, check, true)
}

func bootstrapNativeHostActionsFor(expected clusterbootstrap.PreparationExpected, guard *clusterbootstrap.ExecutorGuard,
	identity clusterbootstrap.ExecutorIdentity, check func(context.Context) error, repositoryContinuation bool) clusterbootstrap.PrejoinHostActions {
	callerCheck := check
	check = func(ctx context.Context) error {
		if guard == nil || callerCheck == nil || ctx.Err() != nil || guard.CheckAuthority(ctx) != nil ||
			callerCheck(ctx) != nil || guard.CheckAuthority(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	return clusterbootstrap.PrejoinHostActions{
		CheckFresh: func(ctx context.Context) error {
			if os.Geteuid() != 0 || os.Getegid() != 0 {
				return errBootstrapSource
			}
			root, err := os.OpenRoot("/")
			if err != nil {
				return errBootstrapSource
			}
			defer root.Close()
			_, err = observeBootstrapEarlyHost(ctx, root, expected, check, readBootstrapDockerProbe, observeBootstrapAbsentUnit, repositoryContinuation)
			if err == nil && repositoryContinuation {
				err = proveBootstrapFreshFirewall(ctx, check, readBootstrapHostFirewall)
			}
			return err
		},
		Prepare: func(ctx context.Context, hostLock, identityLock *os.File) error {
			if os.Geteuid() != 0 || os.Getegid() != 0 || guard == nil || check == nil {
				return errBootstrapSource
			}
			root, err := os.OpenRoot("/")
			if err != nil {
				return errBootstrapSource
			}
			defer root.Close()
			var clean bootstrapDockerHost
			var published []bootstrapHostFile
			publishedCheck := check
			boundary := func(ctx context.Context) error { return publishedCheck(ctx) }
			pin := func(ctx context.Context, files []bootstrapHostFile) error {
				if boundary(ctx) != nil {
					return errBootstrapSource
				}
				published = append(published, files...)
				next, err := pinBootstrapPublishedHostFiles(ctx, root, published, check)
				if err != nil || boundary(ctx) != nil {
					return errBootstrapSource
				}
				publishedCheck = next
				return boundary(ctx)
			}
			command := func(ctx context.Context, step bootstrapHostCommand, host, identityFile *os.File) error {
				return runBootstrapHostCommand(ctx, step, host, identityFile, guard, identity, boundary)
			}
			return prepareBootstrapNativeHostPhaseWith(ctx, expected, hostLock, identityLock, boundary, repositoryContinuation, bootstrapNativeHostOperations{
				preflight: func(ctx context.Context) error {
					clean, err = observeBootstrapEarlyHost(ctx, root, expected, check, readBootstrapDockerProbe, observeBootstrapAbsentUnit, repositoryContinuation)
					return err
				},
				base: func(ctx context.Context) error {
					after, err := observeBootstrapDockerHost(ctx, root, nil, check, readBootstrapDockerProbe)
					if err != nil || validateBootstrapHostBase(after.probes["packages"]) != nil {
						return errBootstrapSource
					}
					// Base installation may change package rows, never OS/APT
					// sources, ancestors or clean Docker unit observations.
					after.probes["packages"] = clean.probes["packages"]
					if !reflect.DeepEqual(clean, after) {
						return errBootstrapSource
					}
					return proveBootstrapFreshFirewall(ctx, check, readBootstrapHostFirewall)
				},
				repository: func(ctx context.Context) error {
					if repositoryContinuation {
						err = completeBootstrapDockerRepositoryWith(ctx, root, expected, check, readBootstrapDockerProbe)
					} else {
						err = installBootstrapDockerRepositoryWith(ctx, root, expected, check, readBootstrapDockerProbe, downloadBootstrapDockerKey)
					}
					if err != nil {
						return errBootstrapSource
					}
					key, _, err := readBootstrapDockerFile(root, "etc/apt/keyrings/docker.asc", bootstrapDockerKeyLimit)
					if err != nil {
						return errBootstrapSource
					}
					files, err := bootstrapDockerRepositoryFiles(expected, clean.suite, key)
					if err != nil || proveBootstrapDockerRepositoryWith(ctx, root, expected, check) != nil {
						return errBootstrapSource
					}
					return pin(ctx, files)
				},
				files: func(ctx context.Context) error {
					if installBootstrapPrejoinHostFilesWith(ctx, root, expected, boundary) != nil {
						return errBootstrapSource
					}
					files, err := bootstrapPrejoinHostFiles(expected)
					if err != nil {
						return errBootstrapSource
					}
					return pin(ctx, files)
				},
				multipath: func(ctx context.Context) error {
					return prepareBootstrapMultipathWith(ctx, boundary, readBootstrapHostDependency,
						func(ctx context.Context) error { return proveBootstrapPrejoinKernel(ctx, boundary) },
						func(ctx context.Context, step bootstrapHostCommand) error {
							return command(ctx, step, hostLock, identityLock)
						})
				},
				firewall: func(ctx context.Context) error {
					return proveBootstrapFreshFirewall(ctx, boundary, readBootstrapHostFirewall)
				},
				prove:   func(ctx context.Context) error { return proveBootstrapPreparedHost(ctx, expected, check) },
				command: command,
			})
		},
		Prove: func(ctx context.Context) error { return proveBootstrapPreparedHost(ctx, expected, check) },
	}
}

// Same fixed read-only preflight before durable intent and again before effects.
// The pre-intent call cannot install packages, publish files or start services.
func observeBootstrapFreshHost(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, check func(context.Context) error,
	read func(context.Context, string) ([]byte, error), absentUnit func(context.Context, string) error) (bootstrapDockerHost, error) {
	return observeBootstrapEarlyHost(ctx, root, expected, check, read, absentUnit, false)
}

func observeBootstrapEarlyHost(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, check func(context.Context) error,
	read func(context.Context, string) ([]byte, error), absentUnit func(context.Context, string) error, repositoryContinuation bool) (bootstrapDockerHost, error) {
	invalid := bootstrapDockerHost{}
	if expected.Validate() != nil || root == nil || check == nil || read == nil || absentUnit == nil || ctx.Err() != nil || check(ctx) != nil {
		return invalid, errBootstrapSource
	}
	var clean bootstrapDockerHost
	var err error
	if repositoryContinuation {
		var prefix bootstrapDockerRepositoryPrefix
		prefix, err = observeBootstrapDockerRepositoryPrefix(ctx, root, expected, check, read)
		clean = prefix.host
	} else {
		clean, err = observeBootstrapDockerHost(ctx, root, nil, check, read)
		if err == nil {
			err = proveBootstrapDockerScratchAbsent(ctx, root, check)
		}
	}
	if err != nil || preflightBootstrapHostFiles(ctx, root, expected, check) != nil || absentUnit(ctx, "borealis-k3s-api-firewall.service") != nil || ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
		return invalid, errBootstrapSource
	}
	return clean, nil
}

type bootstrapNativeHostOperations struct {
	preflight, base, repository, files, multipath, firewall, prove func(context.Context) error
	command                                                        func(context.Context, bootstrapHostCommand, *os.File, *os.File) error
}

func prepareBootstrapNativeHostWith(ctx context.Context, expected clusterbootstrap.PreparationExpected, hostLock, identityLock *os.File,
	check func(context.Context) error, o bootstrapNativeHostOperations) error {
	return prepareBootstrapNativeHostPhaseWith(ctx, expected, hostLock, identityLock, check, false, o)
}

func prepareBootstrapNativeHostPhaseWith(ctx context.Context, expected clusterbootstrap.PreparationExpected, hostLock, identityLock *os.File,
	check func(context.Context) error, repositoryContinuation bool, o bootstrapNativeHostOperations) error {
	if expected.Validate() != nil || hostLock == nil || identityLock == nil || check == nil || o.command == nil ||
		o.preflight == nil || o.base == nil || o.repository == nil || o.files == nil || o.multipath == nil || o.firewall == nil || o.prove == nil {
		return errBootstrapSource
	}
	host, hostErr := hostLock.Stat()
	identity, identityErr := identityLock.Stat()
	if hostErr != nil || identityErr != nil || !host.Mode().IsRegular() || !identity.IsDir() {
		return errBootstrapSource
	}
	command := func(step bootstrapHostCommand) func(context.Context) error {
		return func(ctx context.Context) error { return o.command(ctx, step, hostLock, identityLock) }
	}
	// Never replay a failed stage or acknowledge command success as host proof.
	// Package effects precede generated configuration; the firewall starts last,
	// after Docker so Docker's iptables changes cannot invalidate its prefix.
	steps := []func(context.Context) error{o.preflight}
	if !repositoryContinuation {
		steps = append(steps, command(bootstrapHostAPTUpdate), command(bootstrapHostInstallBase), o.base)
	} else {
		// Base packages already supply iptables. Preserve the pre-Docker
		// fresh-filter proof otherwise performed by the base-package phase.
		steps = append(steps, o.firewall)
	}
	steps = append(steps,
		o.repository, command(bootstrapHostAPTUpdate), command(bootstrapHostInstallDocker), o.files,
		command(bootstrapHostLoadISCSI), o.multipath, command(bootstrapHostReloadUnits),
		command(bootstrapHostEnableDocker), command(bootstrapHostEnableISCSI), o.firewall,
		command(bootstrapHostEnableFirewall), o.prove,
	)
	for _, step := range steps {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil || step(ctx) != nil ||
			ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
	}
	return nil
}

func preflightBootstrapHostFiles(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	files, err := bootstrapPrejoinHostFiles(expected)
	if err != nil || root == nil || check == nil {
		return errBootstrapSource
	}
	for _, file := range files {
		if ctx.Err() != nil || check(ctx) != nil || bootstrapHostParents(ctx, root, filepath.Dir(file.path), func() error { return check(ctx) }, false) != nil {
			return errBootstrapSource
		}
		for _, path := range []string{file.path, filepath.Join(filepath.Dir(file.path), file.pending)} {
			if !bootstrapDockerPathAbsent(root, path) {
				return errBootstrapSource
			}
		}
	}
	return nil
}

func validateBootstrapHostBase(raw string) error {
	if validateBootstrapDockerProbe("packages", []byte(raw)) != nil {
		return errBootstrapSource
	}
	want := map[string]bool{}
	for _, name := range bootstrapHostPackages[:7] {
		want[name] = true
	}
	for _, row := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		fields := strings.Split(row, "\t") // Complete inventory already validated.
		if want[fields[0]] && fields[2] == "install ok installed" && (fields[1] == "amd64" || fields[1] == "all") {
			delete(want, fields[0])
		}
	}
	if len(want) != 0 {
		return errBootstrapSource
	}
	return nil
}
