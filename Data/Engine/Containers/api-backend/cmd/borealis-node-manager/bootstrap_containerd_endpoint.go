package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const bootstrapContainerdSocket = "/run/k3s/containerd/containerd.sock"

// Hold O_PATH custody and a kernel peer pidfd while a synchronous reader uses
// the socket descriptor. No application request is sent by the peer probe.
func withBootstrapContainerdEndpoint(ctx context.Context, hostRoot, proc string, serverPID uint64, serverExecutable *os.File,
	check func(context.Context) error, connect func(context.Context, *sourceCSIPath) (*sourceCSIPeer, error), consume func(*os.File, func(context.Context) error) error) error {
	if check == nil || connect == nil || consume == nil || serverExecutable == nil || serverPID < 2 || ctx.Err() != nil || check(ctx) != nil {
		return errBootstrapSource
	}
	path, err := captureSourceSocketPath(ctx, hostRoot, bootstrapContainerdSocket)
	if err != nil {
		return errBootstrapSource
	}
	defer path.close()
	for i, node := range path.nodes {
		if node.UID != uint32(os.Geteuid()) || node.GID != uint32(os.Getegid()) || node.Mode&0022 != 0 && i != len(path.nodes)-1 {
			return errBootstrapSource
		}
		if i == len(path.nodes)-1 && node.Mode&07777 != 0660 {
			return errBootstrapSource
		}
	}
	peer, err := connect(ctx, path)
	if err != nil || peer == nil {
		return errBootstrapSource
	}
	defer peer.close()
	if peer.cred.Uid != 0 || peer.cred.Gid != 0 || peer.cred.Pid < 2 || uint64(peer.cred.Pid) == serverPID || !peer.alive(ctx) {
		return errBootstrapSource
	}
	pidPath := filepath.Join(proc, strconv.Itoa(int(peer.cred.Pid)))
	process, err := os.OpenRoot(pidPath)
	if err != nil {
		return errBootstrapSource
	}
	defer process.Close()
	processInfo, err := os.Stat(pidPath)
	if err != nil {
		return errBootstrapSource
	}
	executable, err := serverExecutable.Stat()
	if err != nil || !sourceKubeletExecutableUnchanged(executable, executable) {
		return errBootstrapSource
	}
	start := uint64(0)
	active, failed := true, false
	defer func() { active = false }()
	refresh := func(ctx context.Context) (result error) {
		defer func() {
			if result != nil {
				failed = true
			}
		}()
		if !active || failed || ctx.Err() != nil || check(ctx) != nil || !peer.alive(ctx) || path.recheckSocket(ctx, hostRoot, bootstrapContainerdSocket) != nil {
			return errBootstrapSource
		}
		currentProcess, processErr := os.Stat(pidPath)
		if processErr != nil || !os.SameFile(processInfo, currentProcess) {
			return errBootstrapSource
		}
		raw, err := sourceKubeletRead(process, "stat", 4096)
		ticks, tickErr := sourceKubeletStart(raw, uint64(peer.cred.Pid))
		end := strings.LastIndexByte(string(raw), ')')
		if err != nil || tickErr != nil || end < 0 {
			return errBootstrapSource
		}
		fields := strings.Fields(string(raw[end+1:]))
		if len(fields) < 2 || fields[1] != strconv.FormatUint(serverPID, 10) || start != 0 && ticks != start {
			return errBootstrapSource
		}
		start = ticks
		status, err := sourceKubeletRead(process, "status", 64<<10)
		if err != nil || !sourceKubeletRootUID(status) {
			return errBootstrapSource
		}
		groups := 0
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "Gid:") {
				groups++
				if strings.Join(strings.Fields(line), " ") != "Gid: 0 0 0 0" {
					return errBootstrapSource
				}
			}
		}
		if groups != 1 {
			return errBootstrapSource
		}
		for _, name := range []string{"root", "ns/net", "ns/mnt"} {
			a, err := os.Stat(filepath.Join(pidPath, name))
			b, other := os.Stat(filepath.Join(proc, "1", name))
			parent, parentErr := os.Stat(filepath.Join(proc, strconv.FormatUint(serverPID, 10), name))
			if err != nil || other != nil || parentErr != nil || !os.SameFile(a, b) || !os.SameFile(a, parent) {
				return errBootstrapSource
			}
		}
		child, err := os.Stat(filepath.Join(pidPath, "exe"))
		held, heldErr := serverExecutable.Stat()
		if err != nil || heldErr != nil || !sourceKubeletExecutableUnchanged(executable, child) || !sourceKubeletExecutableUnchanged(executable, held) || check(ctx) != nil {
			return errBootstrapSource
		}
		return nil
	}
	if refresh(ctx) != nil {
		return errBootstrapSource
	}
	fd, err := unix.FcntlInt(uintptr(path.fds[len(path.fds)-1]), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return errBootstrapSource
	}
	socket := os.NewFile(uintptr(fd), "held-containerd-socket")
	defer socket.Close()
	if consume(socket, refresh) != nil || refresh(ctx) != nil {
		return errBootstrapSource
	}
	return nil
}
