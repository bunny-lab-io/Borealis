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
		spec.program, spec.timeout = "/usr/bin/apt-get", 2*time.Minute
		// ExtraFiles fixes borrowed locks at FDs3/4. APT otherwise closes them
		// before dpkg/maintainer scripts. Preserve them without writing to them.
		spec.args = []string{"-o", "APT::Keep-Fds::=3", "-o", "APT::Keep-Fds::=4", "-o", "Dpkg::Use-Pty=0",
			"-o", "DPkg::Lock::Timeout=15", "-o", "Acquire::Retries=0", "-o", "Acquire::http::Timeout=30", "-o", "Acquire::https::Timeout=30"}
		if step == bootstrapHostAPTUpdate {
			spec.args = append(spec.args, "update", "--error-on=any")
		} else {
			spec.args = append(spec.args, "--yes", "--no-remove", "--no-upgrade", "--no-install-recommends", "install", "--")
			if step == bootstrapHostInstallBase {
				spec.args = append(spec.args, "python3", "ca-certificates", "curl", "gnupg", "iptables", "open-iscsi", "nfs-common")
			} else {
				spec.args = append(spec.args, "docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin")
			}
		}
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

// This is not a preparation dispatcher. Caller must own both locks, immutable
// source/identity custody, durable host intent and exact host preflight. No
// production caller enables this command vocabulary yet.
func runBootstrapHostCommand(ctx context.Context, step bootstrapHostCommand, hostLock, identityLock *os.File,
	guard *clusterbootstrap.ExecutorGuard, identity clusterbootstrap.ExecutorIdentity, check func(context.Context) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 || guard == nil || check == nil {
		return errBootstrapSource
	}
	return runBootstrapHostCommandWith(ctx, step, hostLock, identityLock, func(ctx context.Context) error {
		if guard.CheckAuthority(ctx) != nil || check(ctx) != nil || guard.CheckAuthority(ctx) != nil {
			return errBootstrapSource
		}
		return nil
	}, func(ctx context.Context) error { return clusterbootstrap.ObserveExecutorIdle(ctx, identity) }, executeBootstrapHostCommand,
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
	command := exec.CommandContext(ctx, spec.program, spec.args...)
	command.Dir = "/"
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive"}
	command.ExtraFiles = []*os.File{hostLock, identityLock}
	command.Stdin, command.Stdout, command.Stderr = nil, io.Discard, io.Discard
	command.WaitDelay = 250 * time.Millisecond
	if command.Run() != nil || ctx.Err() != nil {
		return errBootstrapSource
	}
	return nil
}
