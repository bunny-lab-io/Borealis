package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Real socket and pidfd, synthetic procfs, private fixture ownership. Never
// starts containerd or changes host paths/credentials.
func bootstrapContainerdSocketFixture(t *testing.T) (string, string) {
	t.Helper()
	host := t.TempDir()
	socket := filepath.Join(host, bootstrapContainerdSocket)
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(filepath.Dir(socket))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: "/proc/self/fd/" + strconv.Itoa(int(parent.Fd())) + "/containerd.sock", Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	t.Cleanup(func() { listener.Close() })
	if err := os.Chmod(socket, 0660); err != nil {
		t.Fatal(err)
	}
	return host, socket
}

func TestBootstrapContainerdEndpoint(t *testing.T) {
	for _, mode := range []string{"valid", "socket mode", "parent writable", "peer UID", "peer GID", "peer parent", "peer exited", "socket replaced", "parent replaced", "PID reused", "proc replaced", "UID changed", "GID changed", "namespace changed", "exe changed", "claim lost", "ignored failure", "canceled", "consumer failed"} {
		t.Run(mode, func(t *testing.T) {
			host, socketPath := bootstrapContainerdSocketFixture(t)
			proc := t.TempDir()
			pid := strconv.Itoa(os.Getpid())
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			write := func(name, value string) {
				t.Helper()
				p := filepath.Join(proc, name)
				must(os.MkdirAll(filepath.Dir(p), 0700))
				must(os.WriteFile(p, []byte(value), 0600))
			}
			link := func(name, target string) {
				t.Helper()
				p := filepath.Join(proc, name)
				must(os.MkdirAll(filepath.Dir(p), 0700))
				must(os.Symlink(target, p))
			}
			replace := func(name, target string) { t.Helper(); must(os.Remove(filepath.Join(proc, name))); link(name, target) }
			stat := strings.Replace(sourceKubeletStatFixture("100"), "42 (", pid+" (", 1)
			stat = strings.Replace(stat, ") S 0 ", ") S 42 ", 1)
			write(pid+"/stat", stat)
			write(pid+"/status", "Uid: 0 0 0 0\nGid: 0 0 0 0\n")
			write("host-net", "net")
			write("host-mnt", "mnt")
			write("other", "other")
			for _, id := range []string{"1", "42", pid} {
				link(id+"/root", proc)
				link(id+"/ns/net", filepath.Join(proc, "host-net"))
				link(id+"/ns/mnt", filepath.Join(proc, "host-mnt"))
			}
			link(pid+"/exe", "/usr/bin/true")
			executable, err := os.Open("/usr/bin/true")
			must(err)
			defer executable.Close()
			if mode == "socket mode" {
				must(os.Chmod(socketPath, 0666))
			}
			if mode == "parent writable" {
				must(os.Chmod(filepath.Dir(socketPath), 0770))
			}
			var peer *sourceCSIPeer
			connect := func(ctx context.Context, path *sourceCSIPath) (*sourceCSIPeer, error) {
				p, err := connectSourceCSIPeer(ctx, path)
				if err != nil {
					return nil, err
				}
				peer = p
				// Only injectable fixture substitutes root credentials; production
				// calls the kernel credential reader directly.
				p.cred.Uid, p.cred.Gid = 0, 0
				if mode == "peer UID" {
					p.cred.Uid = 1
				}
				if mode == "peer GID" {
					p.cred.Gid = 1
				}
				return p, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lost := false
			check := func(context.Context) error {
				if lost {
					return errBootstrapSource
				}
				return nil
			}
			var escaped func(context.Context) error
			var borrowed *os.File
			err = withBootstrapContainerdEndpoint(ctx, host, proc, 42, executable, check, connect, func(socket *os.File, refresh func(context.Context) error) error {
				escaped, borrowed = refresh, socket
				info, err := socket.Stat()
				if err != nil || info.Mode()&os.ModeSocket == 0 {
					t.Fatal("socket descriptor missing", err)
				}
				switch mode {
				case "peer parent":
					write(pid+"/stat", strings.Replace(stat, ") S 42 ", ") S 43 ", 1))
				case "peer exited":
					peer.close()
				case "socket replaced":
					must(os.Rename(socketPath, socketPath+".old"))
					must(os.WriteFile(socketPath, nil, 0660))
				case "parent replaced":
					must(os.Rename(filepath.Dir(socketPath), filepath.Dir(socketPath)+".old"))
					must(os.Mkdir(filepath.Dir(socketPath), 0700))
				case "PID reused":
					write(pid+"/stat", strings.Replace(stat, " 100 ", " 101 ", 1))
				case "proc replaced":
					must(os.Rename(filepath.Join(proc, pid), filepath.Join(proc, "old")))
					must(os.Mkdir(filepath.Join(proc, pid), 0700))
				case "UID changed":
					write(pid+"/status", "Uid: 1 1 1 1\nGid: 0 0 0 0\n")
				case "GID changed":
					write(pid+"/status", "Uid: 0 0 0 0\nGid: 0 1 0 0\n")
				case "namespace changed":
					replace(pid+"/ns/net", filepath.Join(proc, "other"))
				case "exe changed":
					replace(pid+"/exe", "/usr/bin/false")
				case "claim lost":
					lost = true
				case "ignored failure":
					lost = true
					_ = refresh(ctx)
					lost = false
					return nil
				case "canceled":
					cancel()
				case "consumer failed":
					return errBootstrapSource
				}
				return refresh(ctx)
			})
			if (err == nil) != (mode == "valid") {
				t.Fatalf("endpoint custody: %v", err)
			}
			if escaped != nil && escaped(context.Background()) == nil {
				t.Fatal("escaped refresh admitted")
			}
			if borrowed != nil {
				if _, err := borrowed.Stat(); err == nil {
					t.Fatal("escaped descriptor open")
				}
			}
		})
	}
}
