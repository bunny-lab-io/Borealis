package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Bind via a held directory's short procfs name, keeping tests independent of
// t.TempDir pathname length. No packets or RPCs are sent to this listener.
func makeSourceCSISocket(t *testing.T, hostRoot, root string) string {
	t.Helper()
	dir := filepath.Join(hostRoot, root+clusterbootstrap.KubeletCSIDirectorySuffix)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: "/proc/self/fd/" + strconv.Itoa(int(parent.Fd())) + "/csi.sock", Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(filepath.Join(dir, "csi.sock"), 0700); err != nil {
		listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return filepath.Join(dir, "csi.sock")
}
func TestSourceCSIPathFilesystemIdentity(t *testing.T) {
	for _, mode := range []string{"valid", "custom root", "longest root", "missing", "regular file", "fifo", "socket symlink", "directory symlink", "ancestor symlink", "ancestor file", "canceled", "socket replaced", "directory replaced", "ancestor replaced", "permissions changed", "host root changed", "cancel recheck", "held descriptor closed"} {
		t.Run(mode, func(t *testing.T) {
			host := t.TempDir()
			root := "/var/lib/kubelet"
			if mode == "custom root" {
				root = "/srv/custom root"
			}
			if mode == "longest root" {
				root = "/" + strings.Repeat("x", 106-len(clusterbootstrap.KubeletPodResourcesSuffix))
			}
			socket := makeSourceCSISocket(t, host, root)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			move := func(path string) { t.Helper(); must(os.Rename(path, path+"-old")) }
			switch mode {
			case "missing":
				must(os.Remove(socket))
			case "regular file":
				must(os.Remove(socket))
				must(os.WriteFile(socket, []byte("private"), 0600))
			case "fifo":
				must(os.Remove(socket))
				must(unix.Mkfifo(socket, 0600))
			case "socket symlink":
				move(socket)
				must(os.Symlink(socket+"-old", socket))
			case "directory symlink":
				dir := filepath.Dir(socket)
				move(dir)
				must(os.Symlink(dir+"-old", dir))
			case "ancestor symlink":
				dir := filepath.Join(host, "var")
				move(dir)
				must(os.Symlink(dir+"-old", dir))
			case "ancestor file":
				dir := filepath.Join(host, "var")
				move(dir)
				must(os.WriteFile(dir, nil, 0600))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			p, err := captureSourceCSIPath(ctx, host, root)
			switch mode {
			case "missing", "regular file", "fifo", "socket symlink", "directory symlink", "ancestor symlink", "ancestor file", "canceled":
				if err != clusterbootstrap.ErrPreparationConfig || p != nil {
					t.Fatal("unsafe path accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			first := p.projection(root)
			if first.Validate() != nil {
				t.Fatal("invalid projection")
			}
			switch mode {
			case "socket replaced":
				move(socket)
				makeSourceCSISocket(t, host, root)
			case "directory replaced":
				move(filepath.Dir(socket))
				makeSourceCSISocket(t, host, root)
			case "ancestor replaced":
				move(filepath.Join(host, "var"))
				makeSourceCSISocket(t, host, root)
			case "permissions changed":
				must(os.Chmod(socket, 0777))
			case "host root changed":
				host = t.TempDir()
				makeSourceCSISocket(t, host, root)
			case "cancel recheck":
				cancel()
			case "held descriptor closed":
				must(unix.Close(p.fds[len(p.fds)-1]))
				p.fds[len(p.fds)-1] = -1
			}
			err = p.recheck(ctx, host, root)
			if mode == "valid" || mode == "custom root" || mode == "longest root" {
				if err != nil {
					t.Fatal(err)
				}
				again, err := captureSourceCSIPath(ctx, host, root)
				if err != nil {
					t.Fatal(err)
				}
				defer again.close()
				if again.projection(root) != first {
					t.Fatal("unstable observation")
				}
				var st unix.Stat_t
				must(unix.Stat(socket, &st))
				if first.SocketInode != st.Ino || first.SocketDevice != uint64(st.Dev) {
					t.Fatal("wrong filesystem identity")
				}
			} else if err != clusterbootstrap.ErrPreparationConfig {
				t.Fatal("path drift accepted")
			}
		})
	}
}
func TestSourceCSIPathMountAndOwnershipDrift(t *testing.T) {
	for _, field := range []string{"mount", "inode", "device", "uid", "gid", "mode"} {
		t.Run(field, func(t *testing.T) {
			host := t.TempDir()
			root := "/var/lib/kubelet"
			makeSourceCSISocket(t, host, root)
			p, err := captureSourceCSIPath(context.Background(), host, root)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			before := p.projection(root)
			node := &p.nodes[len(p.nodes)-2]
			switch field {
			case "mount":
				node.Mount++
			case "inode":
				node.Inode++
			case "device":
				node.Device++
			case "uid":
				node.UID++
			case "gid":
				node.GID++
			case "mode":
				node.Mode ^= 1
			}
			if p.projection(root) == before {
				t.Fatal("identity not frozen")
			}
			if p.recheck(context.Background(), host, root) != clusterbootstrap.ErrPreparationConfig {
				t.Fatal("changed identity accepted")
			}
		})
	}
	// Ancestor replacement can retain the same final socket. Full ancestry still
	// must drift even when the directory/socket object identifiers do not.
	host := t.TempDir()
	root := "/var/lib/kubelet"
	makeSourceCSISocket(t, host, root)
	p, err := captureSourceCSIPath(context.Background(), host, root)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	old := p.projection(root)
	ancestor := filepath.Join(host, "var/lib")
	if err = os.Rename(ancestor, ancestor+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(ancestor, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(ancestor+"-old", "kubelet"), filepath.Join(ancestor, "kubelet")); err != nil {
		t.Fatal(err)
	}
	current, err := captureSourceCSIPath(context.Background(), host, root)
	if err != nil {
		t.Fatal(err)
	}
	defer current.close()
	fresh := current.projection(root)
	if fresh.SocketInode != old.SocketInode || fresh.DirectoryInode != old.DirectoryInode || fresh.PathSHA256 == old.PathSHA256 {
		t.Fatal("ancestor identity lost")
	}
	if p.recheck(context.Background(), host, root) != clusterbootstrap.ErrPreparationConfig {
		t.Fatal("ancestor replacement accepted")
	}
}
