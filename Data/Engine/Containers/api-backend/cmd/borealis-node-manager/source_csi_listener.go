package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type sourceCSIListenerCapture func(context.Context, string, *sourceCSIPath) (*sourceCSIListenerObservation, error)
type sourceCSIListenerObservation struct {
	value   clusterbootstrap.SourceCSIListener
	recheck func(context.Context) error
	close   func()
}

// Only the supported unified hierarchy and Kubernetes/containerd cgroups are
// identities. An arbitrary ancestor containing a Pod UUID is not sufficient.
var sourceCSISystemdCgroup = regexp.MustCompile(`^0::/kubepods\.slice/(?:kubepods-(burstable|besteffort)\.slice/)?(kubepods(?:-(?:burstable|besteffort))?)-pod([0-9a-f_]{36})\.slice/cri-containerd-([0-9a-f]{64})\.scope\n$`)
var sourceCSICgroupfs = regexp.MustCompile(`^0::/kubepods/(?:(burstable|besteffort)/)?pod([0-9a-f-]{36})/([0-9a-f]{64})\n$`)

func parseSourceCSICgroup(raw []byte) (string, string, error) {
	pod, container := "", ""
	if m := sourceCSISystemdCgroup.FindSubmatch(raw); m != nil {
		prefix := "kubepods"
		if len(m[1]) != 0 {
			prefix += "-" + string(m[1])
		}
		if string(m[2]) != prefix {
			return "", "", clusterbootstrap.ErrPreparationConfig
		}
		pod, container = strings.ReplaceAll(string(m[3]), "_", "-"), string(m[4])
	} else if m := sourceCSICgroupfs.FindSubmatch(raw); m != nil {
		pod, container = string(m[2]), string(m[3])
	}
	v := clusterbootstrap.SourceCSIListener{PodUID: pod, ContainerID: container, IdentitySHA256: strings.Repeat("1", 64)}
	if v.Validate() != nil {
		return "", "", clusterbootstrap.ErrPreparationConfig
	}
	return pod, container, nil
}

type sourceCSIPeer struct {
	conn  net.Conn
	cred  unix.Ucred
	pidfd int
}

func (p *sourceCSIPeer) close() {
	if p.conn != nil {
		_ = p.conn.Close()
	}
	if p.pidfd >= 0 {
		_ = unix.Close(p.pidfd)
		p.pidfd = -1
	}
}
func (p *sourceCSIPeer) alive(ctx context.Context) bool {
	if ctx.Err() != nil || p.pidfd < 0 {
		return false
	}
	fds := []unix.PollFd{{Fd: int32(p.pidfd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, 0)
	return err == nil && n == 0 && fds[0].Revents == 0
}
func connectSourceCSIPeer(ctx context.Context, socket *sourceCSIPath) (*sourceCSIPeer, error) {
	p := &sourceCSIPeer{pidfd: -1}
	fail := func() (*sourceCSIPeer, error) { p.close(); return nil, clusterbootstrap.ErrPreparationConfig }
	if socket == nil || len(socket.fds) < 3 || ctx.Err() != nil {
		return fail()
	}
	// Resolve the held socket, never a newly looked-up host pathname. No CSI
	// request is sent. Linux supplies the listening process credentials and pidfd.
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", "/proc/self/fd/"+strconv.Itoa(socket.fds[len(socket.fds)-1]))
	if err != nil {
		return fail()
	}
	p.conn = conn
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return fail()
	}
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		var cred *unix.Ucred
		cred, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if socketErr != nil {
			return
		}
		p.cred = *cred
		p.pidfd, socketErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PEERPIDFD)
	})
	if err != nil || socketErr != nil || p.cred.Pid < 2 || !p.alive(ctx) {
		return fail()
	}
	return p, nil
}

type sourceCSIProcessState struct {
	PID      int32
	Start    uint64
	UID, GID uint32
	Cgroup   string
	// root, mount/network/PID/user namespaces and executable device/inode.
	Objects                          [6][2]uint64
	ExecutableSize                   int64
	ExecutableMode                   uint32
	ExecutableMtime, ExecutableCtime syscall.Timespec
	ListenerFD                       string
	ListenerInode                    uint64
}

func sourceCSIProcess(ctx context.Context, proc string, cred unix.Ucred) (sourceCSIProcessState, error) {
	fail := func() (sourceCSIProcessState, error) {
		return sourceCSIProcessState{}, clusterbootstrap.ErrPreparationConfig
	}
	if ctx.Err() != nil {
		return fail()
	}
	pidPath := filepath.Join(proc, strconv.Itoa(int(cred.Pid)))
	process, err := os.OpenRoot(pidPath)
	if err != nil {
		return fail()
	}
	defer process.Close()
	stat, err := sourceKubeletRead(process, "stat", 4096)
	if err != nil {
		return fail()
	}
	start, err := sourceKubeletStart(stat, uint64(cred.Pid))
	if err != nil {
		return fail()
	}
	status, err := sourceKubeletRead(process, "status", 64<<10)
	if err != nil {
		return fail()
	}
	for name, want := range map[string]uint32{"Uid:": cred.Uid, "Gid:": cred.Gid} {
		count := 0
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, name) {
				count++
				f := strings.Fields(line)
				if len(f) != 5 {
					return fail()
				}
				for _, s := range f[1:] {
					if s != strconv.FormatUint(uint64(want), 10) {
						return fail()
					}
				}
			}
		}
		if count != 1 {
			return fail()
		}
	}
	cgroup, err := sourceKubeletRead(process, "cgroup", 4096)
	if err != nil {
		return fail()
	}
	if _, _, err = parseSourceCSICgroup(cgroup); err != nil {
		return fail()
	}
	result := sourceCSIProcessState{PID: cred.Pid, Start: start, UID: cred.Uid, GID: cred.Gid, Cgroup: string(cgroup)}
	for i, name := range []string{"root", "ns/mnt", "ns/net", "ns/pid", "ns/user", "exe"} {
		info, err := os.Stat(filepath.Join(pidPath, name))
		if err != nil {
			return fail()
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Ino == 0 || st.Dev == 0 {
			return fail()
		}
		result.Objects[i] = [2]uint64{uint64(st.Dev), st.Ino}
		if name == "exe" {
			if !info.Mode().IsRegular() || info.Size() <= 0 {
				return fail()
			}
			result.ExecutableSize = info.Size()
			result.ExecutableMode = st.Mode
			result.ExecutableMtime = st.Mtim
			result.ExecutableCtime = st.Ctim
		}
	}
	dir, err := process.Open("fd")
	if err != nil {
		return fail()
	}
	entries, err := dir.ReadDir(16385)
	dir.Close()
	if err != nil && err != io.EOF || len(entries) > 16384 {
		return fail()
	}
	owned := map[uint64]string{}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return fail()
		}
		name := entry.Name()
		if _, err := strconv.ParseUint(name, 10, 31); err != nil {
			return fail()
		}
		target, err := process.Readlink("fd/" + name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fail()
		}
		number, ok := strings.CutPrefix(target, "socket:[")
		if !ok {
			continue
		}
		number, ok = strings.CutSuffix(number, "]")
		if !ok {
			return fail()
		}
		inode, err := strconv.ParseUint(number, 10, 64)
		if err != nil || inode == 0 {
			return fail()
		}
		// Duplicate descriptors name the same listener; use the lowest numeric FD.
		if old, ok := owned[inode]; !ok || len(name) < len(old) || len(name) == len(old) && name < old {
			owned[inode] = name
		}
	}
	table, err := sourceKubeletRead(process, "net/unix", 1<<20)
	if err != nil {
		return fail()
	}
	root, inode, err := parseSourceUnixListener(table, owned, "/csi.sock")
	if err != nil || root != "/csi" {
		return fail()
	}
	result.ListenerInode = inode
	result.ListenerFD = owned[inode]
	return result, nil
}
func captureSourceCSIListener(ctx context.Context, proc string, socket *sourceCSIPath) (*sourceCSIListenerObservation, error) {
	peer, err := connectSourceCSIPeer(ctx, socket)
	if err != nil {
		return nil, err
	}
	var containerPath *sourceCSIPath
	closeAll := func() {
		if containerPath != nil {
			containerPath.close()
		}
		peer.close()
	}
	fail := func() (*sourceCSIListenerObservation, error) {
		closeAll()
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	before, err := sourceCSIProcess(ctx, proc, peer.cred)
	if err != nil {
		return fail()
	}
	root := filepath.Join(proc, strconv.Itoa(int(peer.cred.Pid)), "root")
	containerPath, err = captureSourceSocketPath(ctx, root, "/csi/csi.sock")
	if err != nil {
		return fail()
	}
	// Bind mount IDs differ across namespaces. Device/inode must agree for both
	// selected directory and socket; each namespace's full path stays frozen.
	for offset := 1; offset <= 2; offset++ {
		a, b := socket.nodes[len(socket.nodes)-offset], containerPath.nodes[len(containerPath.nodes)-offset]
		if a.Device != b.Device || a.Inode != b.Inode || a.Mode != b.Mode || a.UID != b.UID || a.GID != b.GID {
			return fail()
		}
	}
	if containerPath.nodes[0].Device != before.Objects[0][0] || containerPath.nodes[0].Inode != before.Objects[0][1] {
		return fail()
	}
	recheck := func(ctx context.Context) error {
		if !peer.alive(ctx) || containerPath.recheckSocket(ctx, root, "/csi/csi.sock") != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		after, err := sourceCSIProcess(ctx, proc, peer.cred)
		if err != nil || after != before || !peer.alive(ctx) {
			return clusterbootstrap.ErrPreparationConfig
		}
		return nil
	}
	if recheck(ctx) != nil {
		return fail()
	}
	pod, container, _ := parseSourceCSICgroup([]byte(before.Cgroup))
	raw, _ := json.Marshal(struct {
		Process sourceCSIProcessState
		Path    []sourceCSIPathNode
	}{before, containerPath.nodes})
	digest := sha256.Sum256(raw)
	value := clusterbootstrap.SourceCSIListener{PodUID: pod, ContainerID: container, UserID: peer.cred.Uid, IdentitySHA256: hex.EncodeToString(digest[:])}
	if value.Validate() != nil {
		return fail()
	}
	return &sourceCSIListenerObservation{value: value, recheck: recheck, close: closeAll}, nil
}
