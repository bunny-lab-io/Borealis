package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func sourceManagementLink(ctx context.Context, address string) (clusterbootstrap.ManagementLink, error) {
	return observeSourceManagementLink(ctx, address, sourceNetworkNamespace, func(ctx context.Context) ([]byte, error) {
		return sourceLinkCommand(ctx, "/usr/sbin/ip", "-j", "-d", "-4", "address", "show")
	})
}

func sourceNetworkNamespace() (uint64, error) {
	self, err := os.Stat("/proc/self/ns/net")
	init, other := os.Stat("/proc/1/ns/net")
	if err != nil || other != nil || !os.SameFile(self, init) {
		return 0, clusterbootstrap.ErrPreparationConfig
	}
	stat, ok := self.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 || stat.Ino > 1<<53-1 {
		return 0, clusterbootstrap.ErrPreparationConfig
	}
	return stat.Ino, nil
}

func observeSourceManagementLink(ctx context.Context, address string, namespace func() (uint64, error), read func(context.Context) ([]byte, error)) (clusterbootstrap.ManagementLink, error) {
	fail := func() (clusterbootstrap.ManagementLink, error) {
		return clusterbootstrap.ManagementLink{}, clusterbootstrap.ErrPreparationConfig
	}
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() || !ip.IsPrivate() || ip.String() != address || namespace == nil || read == nil || ctx.Err() != nil {
		return fail()
	}
	before, err := namespace()
	if err != nil || before == 0 || before > 1<<53-1 {
		return fail()
	}
	raw, err := read(ctx)
	if err != nil {
		return fail()
	}
	link, err := clusterbootstrap.ParseManagementLink(raw, before, address)
	after, other := namespace()
	if err != nil || other != nil || before != after || ctx.Err() != nil {
		return fail()
	}
	return link, nil
}

type sourceLinkOutput struct {
	buffer bytes.Buffer
	cancel context.CancelFunc
	large  bool
}

func (out *sourceLinkOutput) Write(p []byte) (int, error) {
	if len(p) > (128<<10)-out.buffer.Len() {
		out.large = true
		out.cancel()
		return 0, clusterbootstrap.ErrPreparationConfig
	}
	return out.buffer.Write(p)
}

// Only the fixed ip invocation above uses this runner in production. No
// request-controlled command, environment, stdin, path or diagnostic escapes.
func sourceLinkCommand(parent context.Context, binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 2500*time.Millisecond)
	defer cancel()
	if ctx.Err() != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.WaitDelay = 250 * time.Millisecond
	output := &sourceLinkOutput{cancel: cancel}
	command.Stdout, command.Stderr = output, io.Discard
	if command.Run() != nil || ctx.Err() != nil || output.large || output.buffer.Len() == 0 {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return output.buffer.Bytes(), nil
}
