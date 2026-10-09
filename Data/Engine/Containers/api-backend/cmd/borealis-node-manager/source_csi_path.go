package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"golang.org/x/sys/unix"
)

// O_PATH never connects to, reads, or activates the selected socket/device.
// Only the fixed procfs root link is followed; every subsequent component is
// opened relative to a held directory with O_NOFOLLOW, including the socket.
type sourceCSIPathNode struct {
	Device, Inode, Mount uint64
	Mode, UID, GID       uint32
}
type sourceCSIPath struct {
	fds   []int
	nodes []sourceCSIPathNode
}

func (p *sourceCSIPath) close() {
	for _, fd := range p.fds {
		_ = unix.Close(fd)
	}
	p.fds = nil
}
func sourceCSIPathStat(fd int, socket bool) (sourceCSIPathNode, error) {
	var stat unix.Statx_t
	const mask = unix.STATX_TYPE | unix.STATX_MODE | unix.STATX_UID | unix.STATX_GID | unix.STATX_INO | unix.STATX_MNT_ID
	if unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_STATX_DONT_SYNC, mask, &stat) != nil || stat.Mask&mask != mask {
		return sourceCSIPathNode{}, clusterbootstrap.ErrPreparationConfig
	}
	kind := uint16(unix.S_IFDIR)
	if socket {
		kind = unix.S_IFSOCK
	}
	device := unix.Mkdev(stat.Dev_major, stat.Dev_minor)
	if stat.Mode&unix.S_IFMT != kind || device == 0 || device > 1<<53-1 || stat.Ino == 0 || stat.Ino > 1<<53-1 || stat.Mnt_id == 0 || stat.Mnt_id > 1<<53-1 {
		return sourceCSIPathNode{}, clusterbootstrap.ErrPreparationConfig
	}
	return sourceCSIPathNode{device, stat.Ino, stat.Mnt_id, uint32(stat.Mode), stat.Uid, stat.Gid}, nil
}
func captureSourceCSIPath(ctx context.Context, hostRoot, root string) (*sourceCSIPath, error) {
	p := &sourceCSIPath{}
	fail := func() (*sourceCSIPath, error) { p.close(); return nil, clusterbootstrap.ErrPreparationConfig }
	if ctx.Err() != nil || !clusterbootstrap.ValidSourceKubeletRoot(root) {
		return fail()
	}
	fd, err := unix.Open(hostRoot, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail()
	}
	p.fds = append(p.fds, fd)
	info, err := sourceCSIPathStat(fd, false)
	if err != nil {
		return fail()
	}
	p.nodes = append(p.nodes, info)
	parts := strings.Split(strings.TrimPrefix(root+clusterbootstrap.KubeletCSISocketSuffix, "/"), "/")
	for i, part := range parts {
		if ctx.Err() != nil {
			return fail()
		}
		flags := unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC
		socket := i == len(parts)-1
		if !socket {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(p.fds[len(p.fds)-1], part, flags, 0)
		if err != nil {
			return fail()
		}
		p.fds = append(p.fds, next)
		node, err := sourceCSIPathStat(next, socket)
		if err != nil {
			return fail()
		}
		p.nodes = append(p.nodes, node)
	}
	if ctx.Err() != nil {
		return fail()
	}
	return p, nil
}
func (p *sourceCSIPath) recheck(ctx context.Context, hostRoot, root string) error {
	fail := clusterbootstrap.ErrPreparationConfig
	if p == nil || len(p.nodes) < 4 || len(p.fds) != len(p.nodes) || ctx.Err() != nil {
		return fail
	}
	current, err := captureSourceCSIPath(ctx, hostRoot, root)
	if err != nil {
		return fail
	}
	defer current.close()
	if len(current.nodes) != len(p.nodes) {
		return fail
	}
	for i, fd := range p.fds {
		held, err := sourceCSIPathStat(fd, i == len(p.fds)-1)
		if err != nil || held != p.nodes[i] || current.nodes[i] != p.nodes[i] || ctx.Err() != nil {
			return fail
		}
	}
	return nil
}
func (p *sourceCSIPath) projection(root string) clusterbootstrap.SourceCSISocket {
	dir, socket := p.nodes[len(p.nodes)-2], p.nodes[len(p.nodes)-1]
	// Fixed typed encoding freezes full path ancestry without publishing host
	// metadata or confusing the filesystem inode with procfs's kernel socket inode.
	raw, _ := json.Marshal(struct {
		Path  string
		Nodes []sourceCSIPathNode
	}{root + clusterbootstrap.KubeletCSISocketSuffix, p.nodes})
	digest := sha256.Sum256(raw)
	return clusterbootstrap.SourceCSISocket{
		DirectoryDevice: dir.Device, DirectoryInode: dir.Inode, DirectoryMount: dir.Mount,
		SocketDevice: socket.Device, SocketInode: socket.Inode, SocketMount: socket.Mount,
		PathSHA256: hex.EncodeToString(digest[:]),
	}
}
