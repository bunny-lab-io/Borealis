package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sourceCSIListenerFixture() clusterbootstrap.SourceCSIListener {
	return clusterbootstrap.SourceCSIListener{PodUID: "11111111-1111-4111-8111-111111111111", ContainerID: strings.Repeat("d", 64), IdentitySHA256: strings.Repeat("e", 64)}
}
func TestSourceCSICgroup(t *testing.T) {
	pod := "11111111-1111-4111-8111-111111111111"
	cid := strings.Repeat("d", 64)
	for _, qos := range []string{"", "burstable", "besteffort"} {
		prefix := "kubepods"
		parent := ""
		fsParent := ""
		if qos != "" {
			prefix += "-" + qos
			parent = prefix + ".slice/"
			fsParent = qos + "/"
		}
		paths := []string{"0::/kubepods.slice/" + parent + prefix + "-pod" + strings.ReplaceAll(pod, "-", "_") + ".slice/cri-containerd-" + cid + ".scope\n", "0::/kubepods/" + fsParent + "pod" + pod + "/" + cid + "\n"}
		for _, path := range paths {
			a, b, err := parseSourceCSICgroup([]byte(path))
			if err != nil || a != pod || b != cid {
				t.Fatalf("valid path: %q %v", path, err)
			}
			for _, bad := range []string{path + path, strings.Replace(path, cid, strings.Repeat("0", 64), 1), strings.Replace(path, "0::", "1:cpu:", 1), strings.TrimSuffix(path, "\n"), strings.Replace(path, cid, "short", 1), strings.Replace(path, pod, "../"+pod, 1) + "child\n"} {
				if _, _, err := parseSourceCSICgroup([]byte(bad)); err == nil {
					t.Fatalf("accepted %q", bad)
				}
			}
		}
	}
	for _, path := range []string{"0::/system.slice/cri-containerd-" + cid + ".scope\n", "0::/kubepods.slice/kubepods-burstable.slice/kubepods-besteffort-pod" + strings.ReplaceAll(pod, "-", "_") + ".slice/cri-containerd-" + cid + ".scope\n"} {
		if _, _, err := parseSourceCSICgroup([]byte(path)); err == nil {
			t.Fatal("unowned group accepted")
		}
	}
}
func TestSourceCSIPeerRealSocket(t *testing.T) {
	host := t.TempDir()
	root := "/var/lib/kubelet"
	makeSourceCSISocket(t, host, root)
	socket, err := captureSourceCSIPath(context.Background(), host, root)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.close()
	peer, err := connectSourceCSIPeer(context.Background(), socket)
	if err != nil {
		t.Fatal(err)
	}
	if peer.cred.Pid != int32(os.Getpid()) || peer.cred.Uid != uint32(os.Getuid()) || !peer.alive(context.Background()) {
		t.Fatal("kernel credentials differ")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if peer.alive(ctx) {
		t.Fatal("cancellation ignored")
	}
	peer.close()
	if peer.alive(context.Background()) {
		t.Fatal("closed pidfd accepted")
	}
	if observed, err := captureSourceCSIListener(context.Background(), "/proc", socket); err == nil || observed != nil {
		t.Fatal("unrelated process accepted")
	}
	if observed, err := connectSourceCSIPeer(ctx, socket); err == nil || observed != nil {
		t.Fatal("canceled connect accepted")
	}
}

// Real kernel peer credentials/pidfd, synthetic container procfs metadata. No
// namespace entry or mount is needed to exercise the held-path composition.
func TestSourceCSIListenerProcessBinding(t *testing.T) {
	for _, mode := range []string{"valid", "container drift", "process restart", "listener closed", "namespace drift", "executable drift", "socket replaced", "UID drift", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			host := t.TempDir()
			if err := os.Mkdir(filepath.Join(host, "csi"), 0700); err != nil {
				t.Fatal(err)
			}
			parent, err := os.Open(filepath.Join(host, "csi"))
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: "/proc/self/fd/" + strconv.Itoa(int(parent.Fd())) + "/csi.sock", Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			listener.SetUnlinkOnClose(false)
			defer listener.Close()
			socket, err := captureSourceSocketPath(context.Background(), host, "/csi/csi.sock")
			if err != nil {
				t.Fatal(err)
			}
			defer socket.close()
			proc := t.TempDir()
			pid := strconv.Itoa(os.Getpid())
			dir := filepath.Join(proc, pid)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			write := func(name, value string) {
				t.Helper()
				p := filepath.Join(dir, name)
				must(os.MkdirAll(filepath.Dir(p), 0700))
				must(os.WriteFile(p, []byte(value), 0600))
			}
			link := func(name, target string) {
				t.Helper()
				p := filepath.Join(dir, name)
				must(os.MkdirAll(filepath.Dir(p), 0700))
				must(os.Symlink(target, p))
			}
			stat := strings.Replace(sourceKubeletStatFixture("100"), "42 (", pid+" (", 1)
			write("stat", stat)
			write("status", fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d\nGid:\t%d\t%d\t%d\t%d\n", os.Getuid(), os.Getuid(), os.Getuid(), os.Getuid(), os.Getgid(), os.Getgid(), os.Getgid(), os.Getgid()))
			group := "0::/kubepods/pod" + sourceCSIListenerFixture().PodUID + "/" + sourceCSIListenerFixture().ContainerID + "\n"
			write("cgroup", group)
			write("net/unix", sourceKubeletUnixHeader+"0000000000000000: 00000002 00000000 00010000 0001 01 123 /csi/csi.sock\n")
			link("fd/5", "socket:[123]")
			link("root", host)
			link("exe", "/usr/bin/true")
			for _, name := range []string{"mnt", "net", "pid", "user"} {
				write("ns/"+name, "namespace")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed, err := captureSourceCSIListener(ctx, proc, socket)
			if err != nil {
				t.Fatal(err)
			}
			defer observed.close()
			if observed.value.PodUID != sourceCSIListenerFixture().PodUID || observed.value.ContainerID != sourceCSIListenerFixture().ContainerID || observed.value.Validate() != nil {
				t.Fatal("wrong process projection")
			}
			switch mode {
			case "container drift":
				write("cgroup", strings.Replace(group, sourceCSIListenerFixture().ContainerID, strings.Repeat("f", 64), 1))
			case "process restart":
				write("stat", strings.Replace(stat, " 100 ", " 101 ", 1))
			case "listener closed":
				must(os.Remove(filepath.Join(dir, "fd/5")))
			case "namespace drift":
				must(os.Rename(filepath.Join(dir, "ns/mnt"), filepath.Join(dir, "ns/mnt-old")))
				write("ns/mnt", "new")
			case "executable drift":
				must(os.Remove(filepath.Join(dir, "exe")))
				link("exe", "/usr/bin/false")
			case "socket replaced":
				must(os.Rename(filepath.Join(host, "csi/csi.sock"), filepath.Join(host, "csi/old.sock")))
				must(os.WriteFile(filepath.Join(host, "csi/csi.sock"), nil, 0600))
			case "UID drift":
				write("status", "Uid:\t4294967295\t4294967295\t4294967295\t4294967295\n")
			case "canceled":
				cancel()
			}
			if err := observed.recheck(ctx); (err == nil) != (mode == "valid") {
				t.Fatalf("drift check: %v", err)
			}
		})
	}
}

func TestSourceCSIPeerProcessExit(t *testing.T) {
	const variable = "BOREALIS_TEST_CSI_PEER_ROOT"
	if host := os.Getenv(variable); host != "" {
		parent, err := os.Open(filepath.Join(host, "csi"))
		if err != nil {
			t.Fatal(err)
		}
		defer parent.Close()
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: "/proc/self/fd/" + strconv.Itoa(int(parent.Fd())) + "/csi.sock", Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		listener.SetUnlinkOnClose(false)
		defer listener.Close()
		_, _ = os.Stdout.Write([]byte("R"))
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	host := t.TempDir()
	if err := os.Mkdir(filepath.Join(host, "csi"), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSourceCSIPeerProcessExit$")
	cmd.Env = []string{variable + "=" + host}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = input.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	ready := make([]byte, 1)
	if _, err = io.ReadFull(output, ready); err != nil || string(ready) != "R" {
		t.Fatal("child did not listen", err)
	}
	socket, err := captureSourceSocketPath(ctx, host, "/csi/csi.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer socket.close()
	peer, err := connectSourceCSIPeer(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.close()
	if peer.cred.Pid != int32(cmd.Process.Pid) || !peer.alive(ctx) {
		t.Fatal("wrong peer")
	}
	_ = input.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if peer.alive(ctx) {
		t.Fatal("exited listener process accepted")
	}
	if value, err := connectSourceCSIPeer(ctx, socket); err == nil || value != nil {
		t.Fatal("stale socket connected")
	}
}
