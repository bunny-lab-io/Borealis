package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"time"
)

// Read-only component, not a dispatcher or admission receipt. A future journal
// caller must retain both fences and bind this observation to its startup proof.
func observeBootstrapContainerdImport(ctx context.Context, artifacts *clusterbootstrap.PreparationArtifacts,
	hostLock, identityLock *os.File, guard *clusterbootstrap.ExecutorGuard, identity clusterbootstrap.ExecutorIdentity,
	check func(context.Context) error) ([]clusterbootstrap.ContainerdImageTarget, error) {
	if os.Geteuid() != 0 || os.Getegid() != 0 || guard == nil || check == nil || artifacts == nil {
		return nil, errBootstrapSource
	}
	authority := func(ctx context.Context) error {
		if guard.CheckAuthority(ctx) != nil || check(ctx) != nil || guard.CheckAuthority(ctx) != nil {
			return errBootstrapSource
		}
		return nil
	}
	targets, err := artifacts.ImportTargets(ctx, authority)
	if err != nil {
		return nil, errBootstrapSource
	}
	var observed []clusterbootstrap.ContainerdImageTarget
	err = withBootstrapStartedK3sExecutable(ctx, "/proc", readBootstrapStartedK3s, checkSourceKubeletExecutable, authority,
		func(serverCheck func(context.Context) error, executable *os.File, pid uint64) error {
			return withBootstrapContainerdEndpoint(ctx, "/", "/proc", pid, executable, serverCheck, connectSourceCSIPeer,
				func(socket *os.File, endpointCheck func(context.Context) error) error {
					var err error
					observed, err = runBootstrapContainerdCheckWith(ctx, targets, executable, socket, hostLock, identityLock,
						endpointCheck, func(ctx context.Context) error { return clusterbootstrap.ObserveExecutorIdle(ctx, identity) },
						executeBootstrapContainerdCheck, func() { guard.Close(); os.Exit(1) })
					return err
				})
		})
	if err != nil {
		return nil, errBootstrapSource
	}
	return observed, nil
}

func bootstrapContainerdCheckPlan() bootstrapHostCommandSpec {
	return bootstrapHostCommandSpec{program: "/proc/self/fd/5", timeout: 2 * time.Minute,
		args: []string{"--address=/proc/self/fd/6", "--namespace=k8s.io", "images", "check", "--snapshotter=overlayfs"}}
}

func runBootstrapContainerdCheckWith(ctx context.Context, targets []clusterbootstrap.ContainerdImageTarget,
	executable, socket, hostLock, identityLock *os.File, check, idle func(context.Context) error,
	run func(context.Context, bootstrapHostCommandSpec, *os.File, *os.File, *os.File, *os.File, io.Writer) error,
	abort func()) ([]clusterbootstrap.ContainerdImageTarget, error) {
	if executable == nil || socket == nil || run == nil || len(targets) == 0 {
		return nil, errBootstrapSource
	}
	var output bootstrapContainerdOutput
	err := runBootstrapCommandSpecWith(ctx, bootstrapContainerdCheckPlan(), hostLock, identityLock, check, idle,
		func(ctx context.Context, spec bootstrapHostCommandSpec, host, identity *os.File) error {
			return run(ctx, spec, host, identity, executable, socket, &output)
		}, abort)
	if err != nil || output.failed {
		return nil, errBootstrapSource
	}
	observed, err := clusterbootstrap.ParseContainerdImportCheck(output.buffer.Bytes(), targets)
	if err != nil {
		return nil, errBootstrapSource
	}
	return observed, nil
}

type bootstrapContainerdOutput struct {
	buffer bytes.Buffer
	failed bool
}

func (b *bootstrapContainerdOutput) Write(p []byte) (int, error) {
	if b.failed || len(p) > (256<<10)-b.buffer.Len() {
		b.failed = true
		return 0, errBootstrapSource
	}
	return b.buffer.Write(p)
}

// K3s's pinned embedded binary registers ctr with reexec before server startup.
// argv[0] must be ctr; executing the outer launcher would extract/write runtime.
func executeBootstrapContainerdCheck(ctx context.Context, spec bootstrapHostCommandSpec,
	host, identity, executable, socket *os.File, output io.Writer) error {
	command := exec.CommandContext(ctx, spec.program, spec.args...)
	command.Args[0] = "ctr"
	command.Dir = "/"
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.ExtraFiles = []*os.File{host, identity, executable, socket}
	command.Stdin, command.Stdout, command.Stderr = nil, output, io.Discard
	command.WaitDelay = 250 * time.Millisecond
	if command.Run() != nil || ctx.Err() != nil {
		return errBootstrapSource
	}
	return nil
}
