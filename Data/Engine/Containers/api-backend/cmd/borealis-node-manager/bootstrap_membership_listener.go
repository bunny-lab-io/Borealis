package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Private lifetime only: no listener identity is accepted from a request or
// exported as authority. Existing source proof authenticates the K3s executable.
type bootstrapMembershipListenerScope struct {
	check        func(context.Context) error
	close        func() error
	proofVersion uint8
}

type bootstrapMembershipTCPListener struct {
	family, address string
	inode, uid      uint64
}

// procfs prints addresses as native-endian uint32 words. Borealis's reviewed
// server is Linux/amd64. Include IPv6 wildcard/mapped IPv4 listeners because Go
// may use a dual-stack socket; ::1 cannot accept the fixed IPv4 destination.
// K3s ListenWithLoopback also permits a separate non-loopback bind. Ignore that
// endpoint, but reject every overlapping wildcard/loopback candidate, including
// unowned SO_REUSEPORT listeners, before selecting any process descriptor.
func parseBootstrapMembershipTCP(tcp4, tcp6 []byte, port uint16) (bootstrapMembershipTCPListener, error) {
	var result bootstrapMembershipTCPListener
	fail := func() (bootstrapMembershipTCPListener, error) {
		return bootstrapMembershipTCPListener{}, errBootstrapSource
	}
	for i, raw := range [][]byte{tcp4, tcp6} {
		if len(raw) == 0 || len(raw) > 1<<20 || raw[len(raw)-1] != '\n' || port == 0 {
			return fail()
		}
		lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
		remote, width, family := "rem_address", 8, "tcp"
		if i == 1 {
			remote, width, family = "remote_address", 32, "tcp6"
		}
		if len(lines) > 16385 || strings.Join(strings.Fields(lines[0]), " ") != "sl local_address "+remote+" st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode" {
			return fail()
		}
		endpoint := func(value string) (string, uint64, bool) {
			address, number, ok := strings.Cut(value, ":")
			if !ok || len(address) != width || len(number) != 4 {
				return "", 0, false
			}
			for _, c := range address + number {
				if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
					return "", 0, false
				}
			}
			p, err := strconv.ParseUint(number, 16, 16)
			return address, p, err == nil
		}
		for _, line := range lines[1:] {
			f := strings.Fields(line)
			if len(f) < 10 {
				return fail()
			}
			address, number, valid := endpoint(f[1])
			peer, peerPort, peerValid := endpoint(f[2])
			state, stateErr := strconv.ParseUint(f[3], 16, 8)
			if !valid || !peerValid || len(f[3]) != 2 || stateErr != nil {
				return fail()
			}
			if number != uint64(port) || state != 0x0a {
				continue
			}
			if address != strings.Repeat("0", width) && !(i == 0 && address == "0100007F") && !(i == 1 && (address == "0000000000000000FFFF00000100007F" || address == "0000000000000000FFFF000000000000")) {
				continue
			}
			inode, inodeErr := strconv.ParseUint(f[9], 10, 64)
			uid, uidErr := strconv.ParseUint(f[7], 10, 32)
			if result.inode != 0 || peer != strings.Repeat("0", width) || peerPort != 0 || inodeErr != nil || inode == 0 || uidErr != nil ||
				strconv.FormatUint(inode, 10) != f[9] || strconv.FormatUint(uid, 10) != f[7] {
				return fail()
			}
			result = bootstrapMembershipTCPListener{family, address, inode, uid}
		}
	}
	if result.inode == 0 {
		return fail()
	}
	return result, nil
}

// A table row is network-namespace evidence, not process ownership. Bind its
// kernel inode to an actual FD of the retained process, then recheck both reads.
// The caller retains that descriptor number; no socket is duplicated or kept
// listening after K3s closes it.
func readBootstrapMembershipTCP(ctx context.Context, process *os.Root, port uint16) (bootstrapMembershipTCPListener, string, error) {
	fail := func() (bootstrapMembershipTCPListener, string, error) {
		return bootstrapMembershipTCPListener{}, "", errBootstrapSource
	}
	if ctx.Err() != nil || process == nil {
		return fail()
	}
	var tables [2][]byte
	for i, name := range []string{"net/tcp", "net/tcp6"} {
		var err error
		tables[i], err = sourceKubeletRead(process, name, 1<<20)
		if err != nil {
			return fail()
		}
	}
	listener, err := parseBootstrapMembershipTCP(tables[0], tables[1], port)
	if err != nil {
		return fail()
	}
	dir, err := process.Open("fd")
	if err != nil {
		return fail()
	}
	entries, readErr := dir.ReadDir(16385)
	closeErr := dir.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil || len(entries) > 16384 {
		return fail()
	}
	fd, lowest := "", uint64(1<<31)
	for _, entry := range entries {
		if ctx.Err() != nil {
			return fail()
		}
		name := entry.Name()
		n, err := strconv.ParseUint(name, 10, 31)
		if err != nil || strconv.FormatUint(n, 10) != name {
			return fail()
		}
		target, err := process.Readlink("fd/" + name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fail()
		}
		if target == "socket:["+strconv.FormatUint(listener.inode, 10)+"]" && n < lowest {
			fd, lowest = name, n
		}
	}
	if fd == "" || ctx.Err() != nil {
		return fail()
	}
	for i, name := range []string{"net/tcp", "net/tcp6"} {
		tables[i], err = sourceKubeletRead(process, name, 1<<20)
		if err != nil {
			return fail()
		}
	}
	after, err := parseBootstrapMembershipTCP(tables[0], tables[1], port)
	target, targetErr := process.Readlink("fd/" + fd)
	if err != nil || after != listener || targetErr != nil || target != "socket:["+strconv.FormatUint(listener.inode, 10)+"]" || ctx.Err() != nil {
		return fail()
	}
	return listener, fd, nil
}

func captureBootstrapMembershipListener(ctx context.Context, want clusterbootstrap.SourceKubelet, proofVersion uint8) (*bootstrapMembershipListenerScope, error) {
	return captureBootstrapMembershipListenersAt(ctx, "/proc", want, func(ctx context.Context) ([]byte, error) {
		return sourceLinkCommand(ctx, "/usr/bin/systemctl", "show", "k3s.service", "--property=Id,MainPID,InvocationID,ActiveState,SubState", "--no-pager")
	}, proofVersion)
}

func captureBootstrapMembershipListenerAt(ctx context.Context, proc string, want clusterbootstrap.SourceKubelet, serviceRead func(context.Context) ([]byte, error)) (*bootstrapMembershipListenerScope, error) {
	return captureBootstrapMembershipListenersAt(ctx, proc, want, serviceRead, clusterbootstrap.MembershipListenerProofVersion)
}

// The capability selects only reviewed fixed ports. Both listeners retain the
// same measured PID, executable and namespace scope; no caller supplies a port.
func captureBootstrapMembershipListenersAt(ctx context.Context, proc string, want clusterbootstrap.SourceKubelet, serviceRead func(context.Context) ([]byte, error), proofVersion uint8) (*bootstrapMembershipListenerScope, error) {
	if ctx.Err() != nil || want.Validate() != nil || serviceRead == nil ||
		proofVersion != clusterbootstrap.MembershipListenerProofVersion && proofVersion != clusterbootstrap.MembershipLocalEtcdProofVersion {
		return nil, errBootstrapSource
	}
	pidfd, err := unix.PidfdOpen(int(want.PID), 0)
	if err != nil {
		return nil, errBootstrapSource
	}
	var process *os.Root
	var executable *os.File
	var mu sync.Mutex
	failed, closed := false, false
	closeOwned := func() error {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			closed = true
			if executable != nil && executable.Close() != nil {
				failed = true
			}
			if process != nil && process.Close() != nil {
				failed = true
			}
			if unix.Close(pidfd) != nil {
				failed = true
			}
		}
		if failed {
			return errBootstrapSource
		}
		return nil
	}
	fail := func() (*bootstrapMembershipListenerScope, error) {
		_ = closeOwned()
		return nil, errBootstrapSource
	}
	pidPath := filepath.Join(proc, strconv.FormatUint(want.PID, 10))
	process, err = os.OpenRoot(pidPath)
	if err != nil {
		return fail()
	}
	executable, err = os.Open(filepath.Join(pidPath, "exe"))
	if err != nil {
		return fail()
	}
	exe, err := executable.Stat()
	if err != nil || !sourceKubeletExecutableUnchanged(exe, exe) {
		return fail()
	}
	exeStat := exe.Sys().(*syscall.Stat_t)
	if uint64(exeStat.Dev) != want.ExecutableDevice || exeStat.Ino != want.ExecutableInode {
		return fail()
	}
	var retained [2]bootstrapMembershipTCPListener
	var retainedFD [2]string
	check := func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		reject := func() error { failed = true; return errBootstrapSource }
		if closed || failed || ctx.Err() != nil {
			return reject()
		}
		alive := func() bool {
			fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
			n, err := unix.Poll(fds, 0)
			return err == nil && n == 0 && fds[0].Revents == 0
		}
		if !alive() {
			return reject()
		}
		raw, err := serviceRead(ctx)
		if err != nil {
			return reject()
		}
		service, err := parseSourceKubeletService(raw)
		if err != nil || service.pid != want.PID || service.invocation != want.Invocation {
			return reject()
		}
		stat, err := sourceKubeletRead(process, "stat", 4096)
		if err != nil {
			return reject()
		}
		start, err := sourceKubeletStart(stat, want.PID)
		if err != nil || start != want.StartTicks {
			return reject()
		}
		status, err := sourceKubeletRead(process, "status", 64<<10)
		if err != nil || !sourceKubeletRootUID(status) {
			return reject()
		}
		for _, object := range []struct {
			name          string
			inode, device uint64
		}{
			{"root", want.HostRootInode, want.HostRootDevice}, {"ns/mnt", want.MountNamespace, 0}, {"ns/net", want.NetworkNamespace, 0},
		} {
			actual, err := os.Stat(filepath.Join(pidPath, object.name))
			host, hostErr := os.Stat(filepath.Join(proc, "1", object.name))
			if err != nil || hostErr != nil || !os.SameFile(actual, host) {
				return reject()
			}
			st, ok := actual.Sys().(*syscall.Stat_t)
			if !ok || st.Ino != object.inode || object.device != 0 && uint64(st.Dev) != object.device {
				return reject()
			}
			if object.name == "ns/net" {
				self, err := os.Stat(filepath.Join(proc, "self/ns/net"))
				if err != nil || !os.SameFile(actual, self) {
					return reject()
				}
			}
		}
		now, err := os.Stat(filepath.Join(pidPath, "exe"))
		held, heldErr := executable.Stat()
		if err != nil || heldErr != nil || !sourceKubeletExecutableUnchanged(exe, now) || !sourceKubeletExecutableUnchanged(exe, held) {
			return reject()
		}
		for i, port := range [...]uint16{6443, 2379} {
			if i == 1 && proofVersion != clusterbootstrap.MembershipLocalEtcdProofVersion {
				break
			}
			listener, fd, err := readBootstrapMembershipTCP(ctx, process, port)
			if err != nil || listener.uid != 0 || retained[i].inode != 0 && (listener != retained[i] || fd != retainedFD[i]) ||
				i == 1 && (listener.inode == retained[0].inode || fd == retainedFD[0]) {
				return reject()
			}
			retained[i], retainedFD[i] = listener, fd
		}
		if !alive() || ctx.Err() != nil {
			return reject()
		}
		return nil
	}
	if check(ctx) != nil {
		return fail()
	}
	return &bootstrapMembershipListenerScope{check: check, close: closeOwned, proofVersion: proofVersion}, nil
}
