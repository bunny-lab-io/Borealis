package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Only fixed read-only service metadata and procfs are consumed. No process
// arguments, environment or configuration contents are read. CSI connection
// reads kernel credentials only; no application payload is sent or received.
func sourceKubelet(ctx context.Context) (clusterbootstrap.SourceKubelet, error) {
	return observeSourceKubelet(ctx, "/proc", func(ctx context.Context) ([]byte, error) {
		return sourceLinkCommand(ctx, "/usr/bin/systemctl", "show", "k3s.service", "--property=Id,MainPID,InvocationID,ActiveState,SubState", "--no-pager")
	}, checkSourceKubeletExecutable, captureSourceCSIListener)
}

type sourceKubeletService struct {
	pid        uint64
	invocation string
}

func parseSourceKubeletService(raw []byte) (sourceKubeletService, error) {
	fail := func() (sourceKubeletService, error) {
		return sourceKubeletService{}, clusterbootstrap.ErrPreparationConfig
	}
	if len(raw) == 0 || len(raw) > 4096 {
		return fail()
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return fail()
		}
		if _, exists := values[k]; exists {
			return fail()
		}
		values[k] = v
	}
	pid, err := strconv.ParseUint(values["MainPID"], 10, 31)
	invocation := values["InvocationID"]
	if err != nil || pid < 2 || strconv.FormatUint(pid, 10) != values["MainPID"] || len(values) != 5 || values["Id"] != "k3s.service" || values["ActiveState"] != "active" || values["SubState"] != "running" || len(invocation) != 32 || invocation == strings.Repeat("0", 32) {
		return fail()
	}
	for _, b := range []byte(invocation) {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return fail()
		}
	}
	return sourceKubeletService{pid, invocation}, nil
}

func sourceKubeletRead(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > limit {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return raw, nil
}
func sourceKubeletStart(raw []byte, pid uint64) (uint64, error) {
	left, _, ok := strings.Cut(string(raw), " ")
	end := strings.LastIndexByte(string(raw), ')')
	if !ok || left != strconv.FormatUint(pid, 10) || end < len(left)+2 || !strings.HasPrefix(string(raw), left+" (") {
		return 0, clusterbootstrap.ErrPreparationConfig
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 || len(fields[0]) != 1 || !strings.Contains("RSDI", fields[0]) {
		return 0, clusterbootstrap.ErrPreparationConfig
	}
	start, err := strconv.ParseUint(fields[19], 10, 53)
	if err != nil || start == 0 || strconv.FormatUint(start, 10) != fields[19] {
		return 0, clusterbootstrap.ErrPreparationConfig
	}
	return start, nil
}
func sourceKubeletRootUID(raw []byte) bool {
	count := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			count++
			f := strings.Fields(line)
			if len(f) != 5 || strings.Join(f[1:], " ") != "0 0 0 0" {
				return false
			}
		}
	}
	return count == 1
}

// Linux unix_seq_show writes seven whitespace-delimited fields followed by the
// unescaped pathname. Preserve spaces inside path; never infer a default root.
func parseSourceKubeletListener(raw []byte, owned map[uint64]string) (string, uint64, error) {
	return parseSourceUnixListener(raw, owned, clusterbootstrap.KubeletPodResourcesSuffix)
}
func parseSourceUnixListener(raw []byte, owned map[uint64]string, suffix string) (string, uint64, error) {
	fail := func() (string, uint64, error) { return "", 0, clusterbootstrap.ErrPreparationConfig }
	if len(raw) == 0 || len(raw) > 1<<20 {
		return fail()
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) > 16385 || strings.Join(strings.Fields(lines[0]), " ") != "Num RefCount Protocol Flags Type St Inode Path" {
		return fail()
	}
	foundRoot := ""
	var foundInode uint64
	for _, line := range lines[1:] {
		fields := make([]string, 7)
		rest := line
		for i := range fields {
			rest = strings.TrimLeft(rest, " \t")
			j := strings.IndexAny(rest, " \t")
			if j < 0 {
				fields[i] = rest
				rest = ""
			} else {
				fields[i] = rest[:j]
				rest = rest[j:]
			}
			if fields[i] == "" {
				return fail()
			}
		}
		inode, err := strconv.ParseUint(fields[6], 10, 64)
		if err != nil {
			return fail()
		}
		if _, ok := owned[inode]; !ok {
			continue
		}
		socket := strings.TrimLeft(rest, " \t")
		root, ok := strings.CutSuffix(socket, suffix)
		if !ok {
			continue
		}
		if foundRoot != "" || fields[2] != "00000000" || fields[3] != "00010000" || fields[4] != "0001" || fields[5] != "01" {
			return fail()
		}
		// Validate all path rules through the same public contract as consumers.
		if !clusterbootstrap.ValidSourceKubeletRoot(root) {
			return fail()
		}
		foundRoot, foundInode = root, inode
	}
	if foundRoot == "" {
		return fail()
	}
	return foundRoot, foundInode, nil
}

func observeSourceKubelet(ctx context.Context, proc string, serviceRead func(context.Context) ([]byte, error), executableCheck func(context.Context, *os.File) error, listenerCapture sourceCSIListenerCapture) (clusterbootstrap.SourceKubelet, error) {
	fail := func() (clusterbootstrap.SourceKubelet, error) {
		return clusterbootstrap.SourceKubelet{}, clusterbootstrap.ErrPreparationConfig
	}
	if serviceRead == nil || executableCheck == nil || listenerCapture == nil || ctx.Err() != nil {
		return fail()
	}
	raw, err := serviceRead(ctx)
	if err != nil {
		return fail()
	}
	service, err := parseSourceKubeletService(raw)
	if err != nil {
		return fail()
	}
	before, err := sourceKubeletProcess(ctx, proc, service, executableCheck, listenerCapture)
	if err != nil {
		return fail()
	}
	afterRaw, err := serviceRead(ctx)
	if err != nil {
		return fail()
	}
	afterService, err := parseSourceKubeletService(afterRaw)
	if err != nil || afterService != service {
		return fail()
	}
	after, err := sourceKubeletProcess(ctx, proc, service, executableCheck, listenerCapture)
	if err != nil || after != before || ctx.Err() != nil {
		return fail()
	}
	return before, nil
}

func sourceKubeletProcess(ctx context.Context, proc string, service sourceKubeletService, executableCheck func(context.Context, *os.File) error, listenerCapture sourceCSIListenerCapture) (clusterbootstrap.SourceKubelet, error) {
	fail := func() (clusterbootstrap.SourceKubelet, error) {
		return clusterbootstrap.SourceKubelet{}, clusterbootstrap.ErrPreparationConfig
	}
	if ctx.Err() != nil {
		return fail()
	}
	pidPath := filepath.Join(proc, strconv.FormatUint(service.pid, 10))
	process, err := os.OpenRoot(pidPath)
	if err != nil {
		return fail()
	}
	defer process.Close()
	stat, err := sourceKubeletRead(process, "stat", 4096)
	if err != nil {
		return fail()
	}
	start, err := sourceKubeletStart(stat, service.pid)
	if err != nil {
		return fail()
	}
	status, err := sourceKubeletRead(process, "status", 64<<10)
	if err != nil || !sourceKubeletRootUID(status) {
		return fail()
	}
	// The manager has its own protected mount namespace. Bind the observed K3s
	// process directly to PID1's root/mount/network, without entering a namespace.
	sameHost := func() ([4]uint64, error) {
		ns := map[string]uint64{}
		for _, name := range []string{"root", "ns/net", "ns/mnt"} {
			a, err := os.Stat(filepath.Join(pidPath, name))
			b, other := os.Stat(filepath.Join(proc, "1", name))
			if err != nil || other != nil || !os.SameFile(a, b) {
				return [4]uint64{}, clusterbootstrap.ErrPreparationConfig
			}
			info, ok := a.Sys().(*syscall.Stat_t)
			if !ok || info.Ino == 0 {
				return [4]uint64{}, clusterbootstrap.ErrPreparationConfig
			}
			ns[name] = info.Ino
			if name == "root" {
				ns["rootdev"] = uint64(info.Dev)
			}
		}
		return [4]uint64{ns["ns/net"], ns["ns/mnt"], ns["rootdev"], ns["root"]}, nil
	}
	host, err := sameHost()
	if err != nil {
		return fail()
	}
	// Follow only the fixed kernel procfs executable link; keep its descriptor
	// through all process/listener rechecks so pathname replacement cannot stand
	// in for the bytes that were authenticated.
	executable, err := os.Open(filepath.Join(pidPath, "exe"))
	if err != nil {
		return fail()
	}
	defer executable.Close()
	exe, err := executable.Stat()
	if err != nil || !sourceKubeletExecutableUnchanged(exe, exe) || executableCheck == nil || executableCheck(ctx, executable) != nil {
		return fail()
	}
	exestat := exe.Sys().(*syscall.Stat_t)
	fdDir, err := process.Open("fd")
	if err != nil {
		return fail()
	}
	entries, readErr := fdDir.ReadDir(16385)
	fdDir.Close()
	if readErr != nil && readErr != io.EOF || len(entries) > 16384 {
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
		owned[inode] = name
	}
	table, err := sourceKubeletRead(process, "net/unix", 1<<20)
	if err != nil {
		return fail()
	}
	root, inode, err := parseSourceKubeletListener(table, owned)
	if err != nil {
		return fail()
	}
	csi, err := captureSourceCSIPath(ctx, filepath.Join(pidPath, "root"), root)
	if err != nil {
		return fail()
	}
	defer csi.close()
	if csi.nodes[0].Device != host[2] || csi.nodes[0].Inode != host[3] {
		return fail()
	}
	listener, err := listenerCapture(ctx, proc, csi)
	if err != nil {
		return fail()
	}
	defer listener.close()
	// Recheck the owning descriptor and kernel socket inode, not filesystem inode.
	fdName := "fd/" + owned[inode]
	target, err := process.Readlink(fdName)
	if err != nil || target != "socket:["+strconv.FormatUint(inode, 10)+"]" {
		return fail()
	}
	afterTable, err := sourceKubeletRead(process, "net/unix", 1<<20)
	if err != nil {
		return fail()
	}
	afterRoot, afterInode, err := parseSourceKubeletListener(afterTable, owned)
	if err != nil || afterRoot != root || afterInode != inode {
		return fail()
	}
	afterStat, err := sourceKubeletRead(process, "stat", 4096)
	if err != nil {
		return fail()
	}
	afterStart, err := sourceKubeletStart(afterStat, service.pid)
	if err != nil || afterStart != start {
		return fail()
	}
	afterStatus, err := sourceKubeletRead(process, "status", 64<<10)
	if err != nil || !sourceKubeletRootUID(afterStatus) {
		return fail()
	}
	afterHost, err := sameHost()
	if err != nil || afterHost != host {
		return fail()
	}
	afterExe, err := os.Stat(filepath.Join(pidPath, "exe"))
	heldExe, heldErr := executable.Stat()
	if err != nil || heldErr != nil || !sourceKubeletExecutableUnchanged(exe, afterExe) || !sourceKubeletExecutableUnchanged(exe, heldExe) {
		return fail()
	}
	target, err = process.Readlink(fdName)
	if err != nil || target != "socket:["+strconv.FormatUint(inode, 10)+"]" || ctx.Err() != nil {
		return fail()
	}
	if csi.recheck(ctx, filepath.Join(pidPath, "root"), root) != nil {
		return fail()
	}
	if listener.recheck(ctx) != nil {
		return fail()
	}
	socket := csi.projection(root)
	socket.Listener = listener.value
	result := clusterbootstrap.SourceKubelet{CSISocket: socket, ExecutableSHA256: clusterbootstrap.K3sPins().ServerExecutable.SHA256, Root: root, PID: service.pid, StartTicks: start, Invocation: service.invocation, ListenerInode: inode, NetworkNamespace: host[0], MountNamespace: host[1], HostRootDevice: host[2], HostRootInode: host[3], ExecutableDevice: uint64(exestat.Dev), ExecutableInode: exestat.Ino}
	if result.Validate() != nil {
		return fail()
	}
	return result, nil
}
