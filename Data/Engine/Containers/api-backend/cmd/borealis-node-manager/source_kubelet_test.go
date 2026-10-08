package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const sourceKubeletServiceFixture = "Id=k3s.service\nMainPID=42\nInvocationID=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nActiveState=active\nSubState=running\n"
const sourceKubeletUnixHeader = "Num       RefCount Protocol Flags    Type St Inode Path\n"

func sourceKubeletStatFixture(ticks string) string {
	return "42 (k3s (server)) S " + strings.Repeat("0 ", 18) + ticks + " 0\n"
}
func sourceKubeletUnixFixture(root string) string {
	return sourceKubeletUnixHeader + "0000000000000000: 00000002 00000000 00010000 0001 01 123 " + root + clusterbootstrap.KubeletPodResourcesSuffix + "\n"
}

func TestSourceKubeletServiceAndListenerBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "inactive", "dead", "alias", "zero pid", "pid syntax", "zero invocation", "invocation case", "duplicate", "unknown", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			raw := sourceKubeletServiceFixture
			switch mode {
			case "inactive":
				raw = strings.Replace(raw, "ActiveState=active", "ActiveState=activating", 1)
			case "dead":
				raw = strings.Replace(raw, "SubState=running", "SubState=dead", 1)
			case "alias":
				raw = strings.Replace(raw, "Id=k3s.service", "Id=other.service", 1)
			case "zero pid":
				raw = strings.Replace(raw, "MainPID=42", "MainPID=0", 1)
			case "pid syntax":
				raw = strings.Replace(raw, "MainPID=42", "MainPID=042", 1)
			case "zero invocation":
				raw = strings.Replace(raw, strings.Repeat("a", 32), strings.Repeat("0", 32), 1)
			case "invocation case":
				raw = strings.Replace(raw, strings.Repeat("a", 32), strings.Repeat("A", 32), 1)
			case "duplicate":
				raw += "Id=k3s.service\n"
			case "unknown":
				raw += "Environment=private\n"
			case "oversize":
				raw += strings.Repeat(" ", 4096)
			}
			got, err := parseSourceKubeletService([]byte(raw))
			if mode == "valid" {
				if err != nil || got.pid != 42 {
					t.Fatal(err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || got != (sourceKubeletService{}) {
				t.Fatal("unsafe service metadata")
			}
		})
	}
	for _, root := range []string{"/var/lib/kubelet", "/srv/custom kubelet", "/var/lib/rancher/k3s/agent/kubelet"} {
		got, inode, err := parseSourceKubeletListener([]byte(sourceKubeletUnixFixture(root)), map[uint64]string{123: "5"})
		if err != nil || got != root || inode != 123 {
			t.Fatalf("custom observed root: %q %v", root, err)
		}
	}
	for _, mode := range []string{"unowned", "connected", "datagram", "protocol", "not listening", "duplicate", "relative", "noncanonical", "root", "too long", "control", "missing", "bad header", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			raw := sourceKubeletUnixFixture("/var/lib/kubelet")
			owned := map[uint64]string{123: "5"}
			switch mode {
			case "unowned":
				owned = map[uint64]string{124: "5"}
			case "connected":
				raw = strings.Replace(raw, "0001 01", "0001 03", 1)
			case "datagram":
				raw = strings.Replace(raw, "0001 01", "0002 01", 1)
			case "protocol":
				raw = strings.Replace(raw, "00000000 00010000", "00000001 00010000", 1)
			case "not listening":
				raw = strings.Replace(raw, "00010000", "00000000", 1)
			case "duplicate":
				raw += strings.Split(raw, "\n")[1] + "\n"
			case "relative":
				raw = strings.Replace(raw, "/var/lib/kubelet", "relative", 1)
			case "noncanonical":
				raw = strings.Replace(raw, "/var/lib/kubelet", "/var/../lib/kubelet", 1)
			case "root":
				raw = strings.Replace(raw, "/var/lib/kubelet", "", 1)
			case "too long":
				raw = strings.Replace(raw, "/var/lib/kubelet", "/"+strings.Repeat("x", 100), 1)
			case "control":
				raw = strings.Replace(raw, "/var/lib/kubelet", "/var/\x00kubelet", 1)
			case "missing":
				raw = sourceKubeletUnixHeader
			case "bad header":
				raw = strings.Replace(raw, "Inode Path", "Wrong", 1)
			case "oversize":
				raw += strings.Repeat(" ", 1<<20)
			}
			root, inode, err := parseSourceKubeletListener([]byte(raw), owned)
			if err != clusterbootstrap.ErrPreparationConfig || root != "" || inode != 0 {
				t.Fatal("unsafe listener accepted")
			}
		})
	}
}

func TestSourceKubeletObserverRechecksProcessAndService(t *testing.T) {
	for _, mode := range []string{"valid", "custom root", "service failure", "service restart", "service stopped", "process restarted", "socket replaced", "socket closed", "root changed", "mount namespace changed", "network namespace changed", "executable changed", "process UID changed", "host root changed", "zombie", "missing process", "executable proof rejected", "executable replaced during hash", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			proc := t.TempDir()
			root := "/var/lib/kubelet"
			if mode == "custom root" {
				root = "/srv/observed root"
			}
			write := func(name, value string) {
				t.Helper()
				p := filepath.Join(proc, name)
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			link := func(name, target string) {
				t.Helper()
				p := filepath.Join(proc, name)
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
			}
			replaceLink := func(name, target string) {
				t.Helper()
				if err := os.Remove(filepath.Join(proc, name)); err != nil {
					t.Fatal(err)
				}
				link(name, target)
			}
			write("42/stat", sourceKubeletStatFixture("100"))
			write("42/status", "Name:\tk3s\nUid:\t0\t0\t0\t0\n")
			write("42/net/unix", sourceKubeletUnixFixture(root))
			link("42/fd/5", "socket:[123]")
			write("host-net", "namespace")
			write("host-mnt", "namespace")
			write("other", "other")
			for _, pid := range []string{"1", "42"} {
				link(pid+"/root", proc)
				link(pid+"/ns/net", filepath.Join(proc, "host-net"))
				link(pid+"/ns/mnt", filepath.Join(proc, "host-mnt"))
			}
			link("42/exe", "/usr/bin/true")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			read := func(context.Context) ([]byte, error) {
				calls++
				raw := sourceKubeletServiceFixture
				if mode == "service failure" {
					return nil, errors.New("private service diagnostic")
				}
				if calls == 2 {
					switch mode {
					case "service restart":
						raw = strings.Replace(raw, strings.Repeat("a", 32), strings.Repeat("b", 32), 1)
					case "service stopped":
						raw = strings.Replace(raw, "ActiveState=active", "ActiveState=inactive", 1)
					case "process restarted":
						write("42/stat", sourceKubeletStatFixture("101"))
					case "socket replaced":
						write("42/net/unix", strings.Replace(sourceKubeletUnixFixture(root), " 123 ", " 124 ", 1))
						replaceLink("42/fd/5", "socket:[124]")
					case "socket closed":
						write("42/net/unix", sourceKubeletUnixHeader)
						if err := os.Remove(filepath.Join(proc, "42/fd/5")); err != nil {
							t.Fatal(err)
						}
					case "root changed":
						write("42/net/unix", sourceKubeletUnixFixture("/other/root"))
					case "mount namespace changed":
						replaceLink("42/ns/mnt", filepath.Join(proc, "other"))
					case "network namespace changed":
						replaceLink("42/ns/net", filepath.Join(proc, "other"))
					case "executable changed":
						replaceLink("42/exe", "/usr/bin/false")
					case "process UID changed":
						write("42/status", "Uid:\t1\t1\t1\t1\n")
					case "host root changed":
						if err := os.Mkdir(filepath.Join(proc, "new-root"), 0700); err != nil {
							t.Fatal(err)
						}
						replaceLink("42/root", filepath.Join(proc, "new-root"))
						replaceLink("1/root", filepath.Join(proc, "new-root"))
					case "zombie":
						write("42/stat", strings.Replace(sourceKubeletStatFixture("100"), ") S ", ") Z ", 1))
					case "missing process":
						if err := os.Remove(filepath.Join(proc, "42/stat")); err != nil {
							t.Fatal(err)
						}
					case "canceled":
						cancel()
					}
				}
				return []byte(raw), nil
			}
			// Small root-owned system binary models the reviewed executable. The
			// native hash verifier still reads the descriptor selected by the observer.
			fixture, fixtureErr := os.ReadFile("/usr/bin/true")
			if fixtureErr != nil {
				t.Fatal(fixtureErr)
			}
			digest := sha256.Sum256(fixture)
			pin := clusterbootstrap.K3sAssetPin{Name: "bin/k3s", Size: int64(len(fixture)), SHA256: hex.EncodeToString(digest[:])}
			checks := 0
			check := func(ctx context.Context, file *os.File) error {
				checks++
				if mode == "executable proof rejected" {
					return clusterbootstrap.ErrPreparationConfig
				}
				err := hashSourceKubeletExecutable(ctx, file, pin)
				if mode == "executable replaced during hash" && checks == 1 {
					replaceLink("42/exe", "/usr/bin/false")
				}
				return err
			}
			got, err := observeSourceKubelet(ctx, proc, read, check)
			if mode == "valid" || mode == "custom root" {
				if err != nil || got.Validate() != nil || got.Root != root || got.PID != 42 || got.StartTicks != 100 || got.ListenerInode != 123 || calls != 2 || checks != 2 {
					t.Fatalf("native projection %+v %v", got, err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || got != (clusterbootstrap.SourceKubelet{}) {
				t.Fatalf("unsafe observation: %+v %v", got, err)
			}
		})
	}
}

func TestSourceKubeletListenerRealKernelTable(t *testing.T) {
	root, err := os.MkdirTemp("", "borealis-kubelet-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	dir := filepath.Join(root, "pod-resources")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "kubelet.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	file, err := listener.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fd := strconv.FormatUint(uint64(file.Fd()), 10)
	link, err := os.Readlink("/proc/self/fd/" + fd)
	if err != nil {
		t.Fatal(err)
	}
	var inode uint64
	if _, err = fmt.Sscanf(link, "socket:[%d]", &inode); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("/proc/self/net/unix")
	if err != nil {
		t.Fatal(err)
	}
	got, observed, err := parseSourceKubeletListener(raw, map[uint64]string{inode: fd})
	if err != nil || got != root || observed != inode {
		t.Fatalf("real kernel listener %q %d %v", got, observed, err)
	}
}
