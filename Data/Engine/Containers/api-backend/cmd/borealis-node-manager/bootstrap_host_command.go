package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"io"
	"os"
	"os/exec"
	"time"
)

type bootstrapHostCommand uint8

const (
	bootstrapHostAPTUpdate bootstrapHostCommand = iota + 1
	bootstrapHostInstallBase
	bootstrapHostInstallDocker
	bootstrapHostLoadISCSI
	bootstrapHostDisableMultipathService
	bootstrapHostDisableMultipathSocket
	bootstrapHostReloadUnits
	bootstrapHostEnableDocker
	bootstrapHostEnableISCSI
	bootstrapHostEnableFirewall
	bootstrapHostStartPreparedK3s
)

type bootstrapHostCommandSpec struct {
	program string
	args    []string
	timeout time.Duration
}

// Fixed command vocabulary only. No public input can select a program, package,
// unit, apt option, shell body or timeout. Repository/key preparation and exact
// clean-host preflight must precede use under durable prepare_host intent.
func bootstrapHostCommandPlan(step bootstrapHostCommand) (bootstrapHostCommandSpec, error) {
	spec := bootstrapHostCommandSpec{timeout: 30 * time.Second}
	switch step {
	case bootstrapHostAPTUpdate, bootstrapHostInstallBase, bootstrapHostInstallDocker:
		// Package commands require the owned release archive plan. The old
		// unversioned repository resolver cannot execute through this runner.
		return bootstrapHostCommandSpec{}, errBootstrapSource
	case bootstrapHostLoadISCSI:
		spec.program, spec.args = "/usr/sbin/modprobe", []string{"iscsi_tcp"}
	case bootstrapHostDisableMultipathService, bootstrapHostDisableMultipathSocket:
		unit := "multipathd.service"
		if step == bootstrapHostDisableMultipathSocket {
			unit = "multipathd.socket"
		}
		spec.program, spec.args = "/usr/bin/systemctl", []string{"disable", "--now", "--", unit}
	case bootstrapHostReloadUnits:
		spec.program, spec.args = "/usr/bin/systemctl", []string{"daemon-reload"}
	case bootstrapHostStartPreparedK3s:
		// First start only, under a durable startup intent and both exclusions.
		// Timeout/transport loss is unknown; reconciliation never replays start.
		spec.program, spec.args, spec.timeout = "/usr/bin/systemctl", []string{"start", "--", "k3s.service"}, 10*time.Minute
	case bootstrapHostEnableDocker, bootstrapHostEnableISCSI, bootstrapHostEnableFirewall:
		unit := "docker.service"
		if step == bootstrapHostEnableISCSI {
			unit = "iscsid.service"
		} else if step == bootstrapHostEnableFirewall {
			unit = "borealis-k3s-api-firewall.service"
		}
		spec.program, spec.args = "/usr/bin/systemctl", []string{"enable", "--now", "--", unit}
	default:
		return bootstrapHostCommandSpec{}, errBootstrapSource
	}
	return spec, nil
}

// The fixed native host action calls this runner under both locks, immutable
// source/identity custody, durable host intent and exact host preflight.
// Production approval remains subject to all six preparation gates.
func runBootstrapHostCommand(ctx context.Context, step bootstrapHostCommand, hostLock, identityLock *os.File,
	guard *clusterbootstrap.ExecutorGuard, identity clusterbootstrap.ExecutorIdentity, check func(context.Context) error) error {
	spec, err := bootstrapHostCommandPlan(step)
	if err != nil {
		return errBootstrapSource
	}
	return runBootstrapHostSpec(ctx, spec, hostLock, identityLock, guard, identity, check, executeBootstrapHostCommand)
}

func runBootstrapHostSpec(ctx context.Context, spec bootstrapHostCommandSpec, hostLock, identityLock *os.File,
	guard *clusterbootstrap.ExecutorGuard, identity clusterbootstrap.ExecutorIdentity, check func(context.Context) error,
	run func(context.Context, bootstrapHostCommandSpec, *os.File, *os.File) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 || guard == nil || check == nil {
		return errBootstrapSource
	}
	return runBootstrapCommandSpecWith(ctx, spec, hostLock, identityLock, func(ctx context.Context) error {
		if guard.CheckAuthority(ctx) != nil || check(ctx) != nil || guard.CheckAuthority(ctx) != nil {
			return errBootstrapSource
		}
		return nil
	}, func(ctx context.Context) error { return clusterbootstrap.ObserveExecutorIdle(ctx, identity) }, run,
		func() {
			// A child/unknown cgroup cannot unwind held fences into successful
			// control flow. Stop feeding watchdog and exit this verified helper;
			// systemd's independently verified KillMode=control-group terminates
			// descendants. Durable intent/marker stay; later executor must prove
			// the entire previous cgroup stopped before any reconciliation.
			guard.Close()
			os.Exit(1)
		})
}

func runBootstrapHostCommandWith(ctx context.Context, step bootstrapHostCommand, hostLock, identityLock *os.File,
	check, idle func(context.Context) error, run func(context.Context, bootstrapHostCommandSpec, *os.File, *os.File) error, abort func()) error {
	spec, err := bootstrapHostCommandPlan(step)
	if err != nil {
		return errBootstrapSource
	}
	return runBootstrapCommandSpecWith(ctx, spec, hostLock, identityLock, check, idle, run, abort)
}

// Share the fenced child/cleanup lifetime with the separately scoped read-only
// containerd command. Its executable/socket descriptors never enter the general
// host command vocabulary.
func runBootstrapCommandSpecWith(ctx context.Context, spec bootstrapHostCommandSpec, hostLock, identityLock *os.File,
	check, idle func(context.Context) error, run func(context.Context, bootstrapHostCommandSpec, *os.File, *os.File) error, abort func()) error {
	if spec.timeout <= 0 || hostLock == nil || identityLock == nil || check == nil || idle == nil || run == nil || abort == nil {
		return errBootstrapSource
	}
	host, hostErr := hostLock.Stat()
	identity, identityErr := identityLock.Stat()
	if hostErr != nil || identityErr != nil || !host.Mode().IsRegular() || !identity.IsDir() {
		return errBootstrapSource
	}
	boundary := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	if boundary() != nil || idle(ctx) != nil || boundary() != nil {
		return errBootstrapSource
	}
	commandCtx, cancel := context.WithTimeout(ctx, spec.timeout)
	runErr := run(commandCtx, spec, hostLock, identityLock)
	commandErr := commandCtx.Err()
	cancel()
	// Read-only cleanup proof is allowed after authority/caller cancellation;
	// it never renews authority or starts another command. Do not mistake direct
	// child Wait for completion of detached grandchildren/setsid descendants.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	idleErr := idle(cleanupCtx)
	cleanupCancel()
	if idleErr != nil {
		abort() // Production never returns. Test adapter can record this boundary.
		return errBootstrapSource
	}
	if runErr != nil || commandErr != nil || boundary() != nil {
		return errBootstrapSource
	}
	return nil
}

func executeBootstrapHostCommand(ctx context.Context, spec bootstrapHostCommandSpec, hostLock, identityLock *os.File) error {
	return executeBootstrapHostCommandOutput(ctx, spec, hostLock, identityLock, io.Discard)
}

func executeBootstrapHostCommandOutput(ctx context.Context, spec bootstrapHostCommandSpec, hostLock, identityLock *os.File, output io.Writer) error {
	command := exec.CommandContext(ctx, spec.program, spec.args...)
	command.Dir = "/"
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive"}
	command.ExtraFiles = []*os.File{hostLock, identityLock}
	command.Stdin, command.Stdout, command.Stderr = nil, output, io.Discard
	command.WaitDelay = 250 * time.Millisecond
	if command.Run() != nil || ctx.Err() != nil {
		return errBootstrapSource
	}
	return nil
}
