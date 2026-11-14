package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const membershipTCP4Header = "  sl local_address rem_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n"
const membershipTCP6Header = "  sl local_address remote_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n"

func membershipTCPRow(address string, port uint16, inode uint64) string {
	return fmt.Sprintf("0: %s:%04X %s:0000 0A 00000000:00000000 00:00000000 00000000 0 0 %d 1 0000000000000000 100 0 0 10 0\n", address, port, strings.Repeat("0", len(address)), inode)
}

func TestBootstrapMembershipTCPBoundaries(t *testing.T) {
	for _, mode := range []string{"loopback", "wildcard", "dual stack", "mapped loopback", "mapped wildcard", "separate bind", "unrelated connection", "IPv6 loopback ignored", "missing", "missing tcp6", "truncated", "oversize", "too many rows", "wrong header", "short row", "address syntax", "port syntax", "state syntax", "not listening", "wrong port", "zero port", "duplicate", "overlapping dual stack", "overlapping mapped wildcard", "peer address", "peer port", "zero inode", "inode syntax", "inode overflow", "uid syntax", "uid overflow"} {
		t.Run(mode, func(t *testing.T) {
			row := membershipTCPRow("0100007F", 6443, 123)
			tcp4, tcp6, port := membershipTCP4Header+row, membershipTCP6Header, uint16(6443)
			valid := false
			switch mode {
			case "loopback":
				valid = true
			case "wildcard":
				tcp4, valid = membershipTCP4Header+membershipTCPRow("00000000", port, 123), true
			case "dual stack", "mapped loopback", "mapped wildcard":
				address := "00000000000000000000000000000000"
				if mode == "mapped loopback" {
					address = "0000000000000000FFFF00000100007F"
				}
				if mode == "mapped wildcard" {
					address = "0000000000000000FFFF000000000000"
				}
				tcp4, tcp6, valid = membershipTCP4Header, membershipTCP6Header+membershipTCPRow(address, port, 123), true
			case "separate bind":
				tcp4 += membershipTCPRow("FC03A8C0", port, 456)
				valid = true
			case "unrelated connection":
				tcp4 += strings.Replace(row, " 0A ", " 01 ", 1)
				valid = true
			case "IPv6 loopback ignored":
				tcp6 += membershipTCPRow("00000000000000000000000001000000", port, 456)
				valid = true
			case "missing":
				tcp4 = membershipTCP4Header
			case "missing tcp6":
				tcp6 = ""
			case "truncated":
				tcp4 = strings.TrimSuffix(tcp4, "\n")
			case "oversize":
				tcp4 += strings.Repeat(" ", 1<<20) + "\n"
			case "too many rows":
				tcp4 += strings.Repeat(strings.Replace(row, " 0A ", " 01 ", 1), 16384)
			case "wrong header":
				tcp6 = strings.Replace(tcp6, "remote_address", "rem_address", 1)
			case "short row":
				tcp4 = membershipTCP4Header + "0: 0100007F:192B\n"
			case "address syntax":
				tcp4 = strings.Replace(tcp4, "0100007F", "0100007f", 1)
			case "port syntax":
				tcp4 = strings.Replace(tcp4, ":192B", ":192b", 1)
			case "state syntax":
				tcp4 = strings.Replace(tcp4, " 0A ", " GG ", 1)
			case "not listening":
				tcp4 = strings.Replace(tcp4, " 0A ", " 01 ", 1)
			case "wrong port":
				port = 6444
			case "zero port":
				port = 0
			case "duplicate":
				tcp4 += row
			case "overlapping dual stack":
				tcp6 += membershipTCPRow("00000000000000000000000000000000", port, 456)
			case "overlapping mapped wildcard":
				tcp6 += membershipTCPRow("0000000000000000FFFF000000000000", port, 456)
			case "peer address":
				tcp4 = strings.Replace(tcp4, "00000000:0000", "0100007F:0000", 1)
			case "peer port":
				tcp4 = strings.Replace(tcp4, "00000000:0000", "00000000:0001", 1)
			case "zero inode":
				tcp4 = strings.Replace(tcp4, " 123 ", " 0 ", 1)
			case "inode syntax":
				tcp4 = strings.Replace(tcp4, " 123 ", " 0123 ", 1)
			case "inode overflow":
				tcp4 = strings.Replace(tcp4, " 123 ", " 18446744073709551616 ", 1)
			case "uid syntax":
				tcp4 = strings.Replace(tcp4, " 0 0 123 ", " 00 0 123 ", 1)
			case "uid overflow":
				tcp4 = strings.Replace(tcp4, " 0 0 123 ", " 4294967296 0 123 ", 1)
			}
			got, err := parseBootstrapMembershipTCP([]byte(tcp4), []byte(tcp6), port)
			if valid {
				if err != nil || got.inode != 123 || got.uid != 0 {
					t.Fatal("valid listener rejected", got, err)
				}
			} else if err != errBootstrapSource || got != (bootstrapMembershipTCPListener{}) {
				t.Fatal("missing, ambiguous or malformed listener accepted", got, err)
			}
		})
	}
}

type membershipProcFixture struct {
	proc, pid string
	want      clusterbootstrap.SourceKubelet
	service   []byte
}

func (f membershipProcFixture) write(t *testing.T, name, value string) {
	t.Helper()
	p := filepath.Join(f.proc, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}
func (f membershipProcFixture) link(t *testing.T, name, target string) {
	t.Helper()
	p := filepath.Join(f.proc, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

func newMembershipProcFixture(t *testing.T, pid int) membershipProcFixture {
	t.Helper()
	f := membershipProcFixture{proc: t.TempDir(), pid: strconv.Itoa(pid), want: membershipObservationFixture(t, true).Network.Kubelet}
	f.want.PID, f.want.StartTicks = uint64(pid), 100
	f.service = []byte(strings.Replace(sourceKubeletServiceFixture, "MainPID=42", "MainPID="+f.pid, 1))
	f.want.Invocation = strings.Repeat("a", 32)
	f.write(t, f.pid+"/stat", strings.Replace(sourceKubeletStatFixture("100"), "42 (", f.pid+" (", 1))
	f.write(t, f.pid+"/status", "Name:\tk3s\nUid:\t0\t0\t0\t0\n")
	f.write(t, f.pid+"/net/tcp", membershipTCP4Header+membershipTCPRow("0100007F", 6443, 123))
	f.write(t, f.pid+"/net/tcp6", membershipTCP6Header)
	f.link(t, f.pid+"/fd/5", "socket:[123]")
	f.write(t, "host-net", "namespace")
	f.write(t, "host-mnt", "namespace")
	f.write(t, "other", "other")
	for _, p := range []string{"1", f.pid} {
		f.link(t, p+"/root", f.proc)
		f.link(t, p+"/ns/net", filepath.Join(f.proc, "host-net"))
		f.link(t, p+"/ns/mnt", filepath.Join(f.proc, "host-mnt"))
	}
	f.link(t, "self/ns/net", filepath.Join(f.proc, "host-net"))
	f.link(t, f.pid+"/exe", "/usr/bin/true")
	for _, object := range []struct {
		name          string
		inode, device *uint64
	}{
		{"root", &f.want.HostRootInode, &f.want.HostRootDevice}, {"ns/net", &f.want.NetworkNamespace, nil}, {"ns/mnt", &f.want.MountNamespace, nil}, {"exe", &f.want.ExecutableInode, &f.want.ExecutableDevice},
	} {
		info, err := os.Stat(filepath.Join(f.proc, f.pid, object.name))
		if err != nil {
			t.Fatal(err)
		}
		st := info.Sys().(*syscall.Stat_t)
		*object.inode = st.Ino
		if object.device != nil {
			*object.device = uint64(st.Dev)
		}
	}
	if f.want.Validate() != nil {
		t.Fatal("invalid process fixture")
	}
	return f
}

func TestBootstrapMembershipListenerProcessCustody(t *testing.T) {
	for _, mode := range []string{"valid", "duplicate descriptor", "unowned", "descriptor replaced", "descriptor number changed", "descriptor missing", "descriptor malformed", "table missing", "ambiguous", "nonroot socket", "service failed", "service restart", "service stopped", "service PID changed", "process restarted", "process zombie", "process UID changed", "executable changed", "host root changed", "mount namespace changed", "network namespace changed", "manager namespace changed", "listener replaced", "listener address changed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			f := newMembershipProcFixture(t, os.Getpid())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			read := func(context.Context) ([]byte, error) { return f.service, nil }
			scope, err := captureBootstrapMembershipListenerAt(ctx, f.proc, f.want, read)
			if err != nil || scope == nil {
				t.Fatal("initial capture", err)
			}
			defer scope.close()
			switch mode {
			case "duplicate descriptor":
				f.link(t, f.pid+"/fd/9", "socket:[123]")
			case "unowned", "descriptor replaced":
				f.link(t, f.pid+"/fd/5", "socket:[456]")
			case "descriptor number changed":
				if err := os.Rename(filepath.Join(f.proc, f.pid, "fd/5"), filepath.Join(f.proc, f.pid, "fd/6")); err != nil {
					t.Fatal(err)
				}
			case "descriptor missing":
				if err := os.Remove(filepath.Join(f.proc, f.pid, "fd/5")); err != nil {
					t.Fatal(err)
				}
			case "descriptor malformed":
				f.link(t, f.pid+"/fd/05", "socket:[456]")
			case "table missing":
				f.write(t, f.pid+"/net/tcp", membershipTCP4Header)
			case "ambiguous":
				f.write(t, f.pid+"/net/tcp6", membershipTCP6Header+membershipTCPRow("00000000000000000000000000000000", 6443, 456))
			case "nonroot socket":
				f.write(t, f.pid+"/net/tcp", strings.Replace(membershipTCP4Header+membershipTCPRow("0100007F", 6443, 123), " 0 0 123 ", " 1000 0 123 ", 1))
			case "service failed":
				f.service = nil
			case "service restart":
				f.service = []byte(strings.Replace(string(f.service), strings.Repeat("a", 32), strings.Repeat("b", 32), 1))
			case "service stopped":
				f.service = []byte(strings.Replace(string(f.service), "active", "inactive", 1))
			case "service PID changed":
				f.service = []byte(strings.Replace(string(f.service), "MainPID="+f.pid, "MainPID=2", 1))
			case "process restarted":
				f.write(t, f.pid+"/stat", strings.Replace(sourceKubeletStatFixture("101"), "42 (", f.pid+" (", 1))
			case "process zombie":
				f.write(t, f.pid+"/stat", strings.Replace(strings.Replace(sourceKubeletStatFixture("100"), "42 (", f.pid+" (", 1), ") S ", ") Z ", 1))
			case "process UID changed":
				f.write(t, f.pid+"/status", "Uid:\t0\t1\t0\t0\n")
			case "executable changed":
				f.link(t, f.pid+"/exe", "/usr/bin/false")
			case "host root changed":
				f.link(t, f.pid+"/root", filepath.Join(f.proc, "other"))
			case "mount namespace changed":
				f.link(t, f.pid+"/ns/mnt", filepath.Join(f.proc, "other"))
			case "network namespace changed":
				f.link(t, f.pid+"/ns/net", filepath.Join(f.proc, "other"))
			case "manager namespace changed":
				f.link(t, "self/ns/net", filepath.Join(f.proc, "other"))
			case "listener replaced":
				f.write(t, f.pid+"/net/tcp", membershipTCP4Header+membershipTCPRow("0100007F", 6443, 456))
				f.link(t, f.pid+"/fd/5", "socket:[456]")
			case "listener address changed":
				f.write(t, f.pid+"/net/tcp", membershipTCP4Header+membershipTCPRow("00000000", 6443, 123))
			case "canceled":
				cancel()
			}
			err = scope.check(ctx)
			valid := mode == "valid" || mode == "duplicate descriptor"
			if valid {
				if err != nil || scope.close() != nil || scope.close() != nil {
					t.Fatal("stable custody or cleanup rejected", err)
				}
				if scope.check(context.Background()) != errBootstrapSource {
					t.Fatal("closed custody revived")
				}
			} else {
				if err != errBootstrapSource {
					t.Fatal("drifting listener custody accepted", err)
				}
				// Restoring one component cannot revive a failed observation.
				f.service = []byte(strings.Replace(sourceKubeletServiceFixture, "MainPID=42", "MainPID="+f.pid, 1))
				if scope.check(context.Background()) != errBootstrapSource || scope.close() != errBootstrapSource {
					t.Fatal("failed custody revived")
				}
			}
		})
	}
}

func TestBootstrapMembershipListenerRejectsDeadProcess(t *testing.T) {
	child := exec.Command("/usr/bin/sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	f := newMembershipProcFixture(t, child.Process.Pid)
	scope, err := captureBootstrapMembershipListenerAt(context.Background(), f.proc, f.want, func(context.Context) ([]byte, error) { return f.service, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer scope.close()
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if scope.check(context.Background()) != errBootstrapSource {
		t.Fatal("dead original process accepted with unchanged synthetic files")
	}
	if got, err := captureBootstrapMembershipListenerAt(context.Background(), f.proc, f.want, func(context.Context) ([]byte, error) { return f.service, nil }); err != errBootstrapSource || got != nil {
		t.Fatal("dead PID captured", err)
	}
}

func TestBootstrapMembershipListenerJoinedCleanup(t *testing.T) {
	f := newMembershipProcFixture(t, os.Getpid())
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	var block atomic.Bool
	read := func(ctx context.Context) ([]byte, error) {
		if block.Load() {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return f.service, nil
	}
	scope, err := captureBootstrapMembershipListenerAt(ctx, f.proc, f.want, read)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = scope.close() })
	block.Store(true)
	checked, closed := make(chan error, 1), make(chan error, 1)
	go func() { checked <- scope.check(ctx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("native check never entered service read")
	}
	go func() { closed <- scope.close() }()
	select {
	case <-closed:
		t.Fatal("cleanup escaped active native check")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	for _, done := range []chan error{checked, closed} {
		select {
		case err := <-done:
			if err != errBootstrapSource {
				t.Fatal("canceled native scope retained proof", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("native check or cleanup failed to join")
		}
	}
	if scope.check(context.Background()) != errBootstrapSource || scope.close() != errBootstrapSource {
		t.Fatal("canceled closed scope revived")
	}
}

func TestBootstrapMembershipListenerCaptureFailsClosed(t *testing.T) {
	for _, mode := range []string{"invalid expectation", "nil service reader", "canceled", "missing process", "executable mismatch", "private service failure", "missing owner", "different network namespace"} {
		t.Run(mode, func(t *testing.T) {
			f := newMembershipProcFixture(t, os.Getpid())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			read := func(context.Context) ([]byte, error) { return f.service, nil }
			switch mode {
			case "invalid expectation":
				f.want.PID = 0
			case "nil service reader":
				read = nil
			case "canceled":
				cancel()
			case "missing process":
				f.proc = t.TempDir()
			case "executable mismatch":
				f.want.ExecutableInode++
			case "private service failure":
				read = func(context.Context) ([]byte, error) { return nil, errors.New("private service diagnostic") }
			case "missing owner":
				f.link(t, f.pid+"/fd/5", "socket:[456]")
			case "different network namespace":
				f.link(t, "self/ns/net", filepath.Join(f.proc, "other"))
			}
			before, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				t.Fatal(err)
			}
			got, err := captureBootstrapMembershipListenerAt(ctx, f.proc, f.want, read)
			if err != errBootstrapSource || got != nil {
				t.Fatal("invalid capture returned custody or private error", err)
			}
			after, err := os.ReadDir("/proc/self/fd")
			if err != nil || len(after) != len(before) {
				t.Fatal("failed capture leaked descriptors", len(before), len(after), err)
			}
		})
	}
}

func TestBootstrapMembershipListenerRealKernelTable(t *testing.T) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	root, err := os.OpenRoot("/proc/self")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	got, fd, err := readBootstrapMembershipTCP(context.Background(), root, port)
	if err != nil || got.inode == 0 || fd == "" || got.address != "0100007F" {
		t.Fatal("real owned listener rejected", got, fd, err)
	}
	file, err := listener.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	child := exec.Command("/usr/bin/sleep", "30")
	child.ExtraFiles = []*os.File{file}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	if file.Close() != nil || listener.Close() != nil {
		t.Fatal("fixture close")
	}
	// The table still contains the socket, now held only by the child.
	if got, fd, err := readBootstrapMembershipTCP(context.Background(), root, port); err != errBootstrapSource || got != (bootstrapMembershipTCPListener{}) || fd != "" {
		t.Fatal("foreign-process listener accepted", got, fd, err)
	}
}

func TestBootstrapMembershipObservationListenerFences(t *testing.T) {
	exerciseBootstrapMembershipListenerFences(t, clusterbootstrap.MembershipListenerProofVersion)
}

func TestBootstrapMembershipObservationLocalEtcdFences(t *testing.T) {
	exerciseBootstrapMembershipListenerFences(t, clusterbootstrap.MembershipLocalEtcdProofVersion)
}

func exerciseBootstrapMembershipListenerFences(t *testing.T, proofVersion uint8) {
	modes := []string{"valid", "native stable", "native postread drift", "capture failed", "nil factory", "nil scope", "nil check", "nil close", "initial check", "before request", "after request", "final check", "close failed", "cancel during close", "wrong capability", "source config changed", "HTTP close error", "HTTP client expiry"}
	if proofVersion == clusterbootstrap.MembershipLocalEtcdProofVersion {
		modes = append(modes, "config etcd disabled initial", "config etcd override initial", "config reset initial", "config etcd disabled final")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			r := membershipObservationFixture(t, true)
			r.RequireListenerProof = true
			if proofVersion == clusterbootstrap.MembershipLocalEtcdProofVersion {
				r.RequiredListenerProofVersion = proofVersion
			}
			finalCheck := 14
			if proofVersion == clusterbootstrap.MembershipLocalEtcdProofVersion {
				finalCheck++
			}
			f := newMembershipProcFixture(t, os.Getpid())
			f.write(t, f.pid+"/net/tcp", membershipTCP4Header+membershipTCPRow("0100007F", 6443, 123)+membershipTCPRow("0100007F", 2379, 456))
			f.link(t, f.pid+"/fd/6", "socket:[456]")
			if strings.HasPrefix(mode, "native ") {
				r.Network.Kubelet = f.want
				r.Network.ManagementLink.NetworkNamespace = f.want.NetworkNamespace
			}
			root, _, _, serverCert, roots := bootstrapJoinedTLSFixture(t, r.Prepared[0].Expected, r.Network.Hostname)
			if root.MkdirAll("var/lib/rancher/k3s/server/db/etcd", 0700) != nil || root.WriteFile("var/lib/rancher/k3s/server/db/etcd/name", []byte(r.Network.Hostname+"-12345678"), 0600) != nil {
				t.Fatal("etcd-name fixture")
			}
			node, members := membershipNativeDocuments(r)
			var requests atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				if mode == "native postread drift" {
					f.link(t, f.pid+"/fd/5", "socket:[456]")
				}
				var body any
				switch req.URL.Path {
				case "/version":
					body = map[string]any{"gitVersion": r.Network.K3sVersion}
				case "/db/info":
					body = members
				case "/api/v1/nodes/" + r.Network.Hostname:
					body = node
				default:
					t.Error("unexpected membership read")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
			server.StartTLS()
			defer server.Close()
			var nativeScope context.Context
			factory := func(config *tls.Config) *http.Client {
				client := newBootstrapJoinedNodeClient(config)
				client.Transport.(*http.Transport).DialContext = func(ctx context.Context, kind, address string) (net.Conn, error) {
					if address != "127.0.0.1:6443" {
						t.Error("foreign dial")
					}
					return (&net.Dialer{}).DialContext(ctx, kind, server.Listener.Addr().String())
				}

				if strings.HasPrefix(mode, "HTTP ") {
					transport := client.Transport
					client.Transport = &sourceNetworkCleanupTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
						response, err := transport.RoundTrip(req)
						if response != nil && response.Body != nil {
							response.Body = &sourceNetworkCleanupBody{ReadCloser: response.Body, close: func() error {
								if requests.Load() == 6 {
									if mode == "HTTP close error" {
										return errors.New("native response close failed")
									}
									deadline, ok := req.Context().Deadline()
									scopeDeadline, scopeOK := nativeScope.Deadline()
									if !ok || !scopeOK || !deadline.Before(scopeDeadline) || req.Context().Err() != nil {
										t.Error("native fixture missed actual3s client deadline")
									}
									<-req.Context().Done()
									if nativeScope.Err() != nil {
										t.Error("native client expiry consumed outer15s scope")
									}
								}
								return nil
							}}
						}
						return response, err
					}}
				}
				return client
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checks, closes, sources := 0, 0, 0
			captured := false
			entered, release := make(chan struct{}), make(chan struct{})
			capture := func(ctx context.Context, want clusterbootstrap.SourceKubelet, proof uint8) (*bootstrapMembershipListenerScope, error) {
				captured = true
				if want != r.Network.Kubelet || proof != proofVersion {
					t.Error("listener did not receive measured process")
				}
				if mode == "nil scope" {
					return nil, nil
				}
				if strings.HasPrefix(mode, "native ") {
					return captureBootstrapMembershipListenersAt(ctx, f.proc, want, func(context.Context) ([]byte, error) { return f.service, nil }, proof)
				}
				scope := &bootstrapMembershipListenerScope{proofVersion: proof, check: func(ctx context.Context) error {
					checks++
					if ctx.Err() != nil || mode == "initial check" && checks == 1 || mode == "before request" && checks == finalCheck-12 || mode == "after request" && checks == finalCheck-11 || mode == "final check" && checks == finalCheck {
						return errors.New("private listener diagnostic")
					}
					return nil
				}, close: func() error {
					closes++
					if mode == "cancel during close" {
						close(entered)
						<-release
					}
					if mode == "close failed" {
						return errors.New("private close diagnostic")
					}
					return nil
				}}
				if mode == "wrong capability" {
					scope.proofVersion = proof - 1
				}
				if mode == "capture failed" {
					return scope, errors.New("private capture diagnostic")
				}
				if mode == "nil check" {
					scope.check = nil
				}
				if mode == "nil close" {
					scope.close = nil
				}
				return scope, nil
			}
			if mode == "nil factory" {
				capture = nil
			}
			type outcome struct {
				value bootstrapMembershipObservation
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				value, err := observeBootstrapMembershipAt(ctx, root, r, func(ctx context.Context) error { return ctx.Err() }, func(sourceCtx context.Context) (clusterbootstrap.SourceNetwork, error) {
					nativeScope = sourceCtx
					sources++
					if proofVersion == clusterbootstrap.MembershipLocalEtcdProofVersion && (!captured || closes != 0) {
						t.Error("source config escaped retained dual listener scope")
					}
					if mode == "source config changed" && sources == 2 {
						return clusterbootstrap.SourceNetwork{}, errBootstrapSource
					}
					raw, readErr := os.ReadFile("../../internal/clusterbootstrap/testdata/source-supervisor.json")
					var control map[string]any
					if readErr != nil || json.Unmarshal(raw, &control) != nil {
						t.Error("Control fixture unavailable")
						return clusterbootstrap.SourceNetwork{}, errBootstrapSource
					}
					control["ServerNodeName"] = r.Network.Hostname
					_, pods, podErr := net.ParseCIDR(r.Network.PodCIDR)
					_, services, serviceErr := net.ParseCIDR(r.Network.ServiceCIDR)
					if podErr != nil || serviceErr != nil {
						t.Error("Control fixture network invalid")
						return clusterbootstrap.SourceNetwork{}, errBootstrapSource
					}
					control["ClusterIPRange"], control["ClusterIPRanges"] = pods, []any{pods}
					control["ServiceIPRange"], control["ServiceIPRanges"] = services, []any{services}
					baseline, _ := json.Marshal(control)
					if value, err := clusterbootstrap.ParseSourceNetwork(baseline, r.Network); err != nil || value != r.Network {
						t.Error("stable actual Control fixture invalid", err)
						return clusterbootstrap.SourceNetwork{}, errBootstrapSource
					}
					if strings.HasPrefix(mode, "config ") && (strings.HasSuffix(mode, "initial") || sources == 2) {
						if strings.Contains(mode, "disabled") {
							control["DisableETCD"] = true
						}
						if strings.Contains(mode, "override") {
							control["ExtraEtcdArgs"] = []string{"listen-client-urls=https://192.0.2.1:2379"}
						}
						if strings.Contains(mode, "reset") {
							control["DisableAPIServer"] = true
						}
					}
					actual, _ := json.Marshal(control)
					return clusterbootstrap.ParseSourceNetwork(actual, r.Network)
				}, factory, capture)
				done <- outcome{value, err}
			}()
			if mode == "cancel during close" {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					close(release)
					<-done
					t.Fatal("cleanup never entered")
				}
				cancel()
				select {
				case <-done:
					close(release)
					t.Fatal("return escaped blocked cleanup")
				case <-time.After(20 * time.Millisecond):
				}
				close(release)
			}
			got := <-done
			if mode == "valid" || mode == "native stable" {
				if got.err != nil || got.value.ListenerProofVersion != proofVersion || !got.value.Valid(clusterbootstrap.MembershipObservationRequest(r)) || got.value.LocalNode != r.Nodes[0].Node || requests.Load() != 6 || mode == "valid" && checks != finalCheck {
					t.Fatal("stable provenance rejected", got.err, requests.Load(), checks)
				}
			} else if got.err != errBootstrapSource || !reflect.DeepEqual(got.value, bootstrapMembershipObservation{}) {
				t.Fatal("unproven listener escaped with observation or private diagnostic", got.err)
			}
			wantCloses := 1
			if mode == "nil scope" || mode == "nil close" || mode == "nil factory" || strings.HasPrefix(mode, "native ") {
				wantCloses = 0
			}
			if closes != wantCloses {
				t.Fatal("listener lifetime not joined exactly once", closes)
			}
			if (mode == "capture failed" || mode == "nil scope" || mode == "nil check" || mode == "nil close" || mode == "nil factory" || mode == "initial check" || mode == "before request") && requests.Load() != 0 {
				t.Fatal("request preceded listener proof")
			}
			if (mode == "after request" || mode == "native postread drift") && requests.Load() != 1 {
				t.Fatal("drift allowed additional requests")
			}
		})
	}
}

func TestBootstrapMembershipLocalEtcdProcessCustody(t *testing.T) {
	for _, mode := range []string{"valid", "missing2379", "foreign2379", "wrong port", "duplicate2379", "same inode", "nonroot2379", "late missing2379", "late fd replacement", "late inode replacement", "late PID start", "late service invocation", "late namespace", "late 6443", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			f := newMembershipProcFixture(t, os.Getpid())
			stable := membershipTCP4Header + membershipTCPRow("0100007F", 6443, 123) + membershipTCPRow("0100007F", 2379, 456)
			f.write(t, f.pid+"/net/tcp", stable)
			f.link(t, f.pid+"/fd/6", "socket:[456]")
			switch mode {
			case "missing2379":
				f.write(t, f.pid+"/net/tcp", membershipTCP4Header+membershipTCPRow("0100007F", 6443, 123))
			case "foreign2379":
				f.link(t, "99999/fd/6", "socket:[456]")
				if os.Remove(filepath.Join(f.proc, f.pid, "fd/6")) != nil {
					t.Fatal("fixture descriptor")
				}
			case "wrong port":
				f.write(t, f.pid+"/net/tcp", strings.Replace(stable, ":094B", ":094C", 1))
			case "duplicate2379":
				f.write(t, f.pid+"/net/tcp", stable+membershipTCPRow("00000000", 2379, 789))
			case "same inode":
				f.write(t, f.pid+"/net/tcp", membershipTCP4Header+membershipTCPRow("0100007F", 6443, 123)+membershipTCPRow("0100007F", 2379, 123))
			case "nonroot2379":
				f.write(t, f.pid+"/net/tcp", strings.Replace(stable, " 0 0 456 ", " 1 0 456 ", 1))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			before, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				t.Fatal(err)
			}
			scope, err := captureBootstrapMembershipListenersAt(ctx, f.proc, f.want, func(context.Context) ([]byte, error) { return f.service, nil }, clusterbootstrap.MembershipLocalEtcdProofVersion)
			initial := mode == "valid" || strings.HasPrefix(mode, "late ") || mode == "canceled"
			if !initial {
				if err != errBootstrapSource || scope != nil {
					t.Fatal("unowned local etcd listener captured", err)
				}
			} else {
				if err != nil || scope == nil || scope.proofVersion != clusterbootstrap.MembershipLocalEtcdProofVersion {
					t.Fatal("stable dual custody rejected", err)
				}
				defer scope.close()
				switch mode {
				case "late missing2379":
					f.write(t, f.pid+"/net/tcp", membershipTCP4Header+membershipTCPRow("0100007F", 6443, 123))
				case "late fd replacement":
					f.link(t, f.pid+"/fd/7", "socket:[456]")
					if os.Remove(filepath.Join(f.proc, f.pid, "fd/6")) != nil {
						t.Fatal("fixture descriptor")
					}
				case "late inode replacement":
					f.write(t, f.pid+"/net/tcp", strings.Replace(stable, " 456 ", " 789 ", 1))
					f.link(t, f.pid+"/fd/6", "socket:[789]")
				case "late PID start":
					f.write(t, f.pid+"/stat", strings.Replace(sourceKubeletStatFixture("101"), "42 (", f.pid+" (", 1))
				case "late service invocation":
					f.service = []byte(strings.Replace(string(f.service), strings.Repeat("a", 32), strings.Repeat("b", 32), 1))
				case "late namespace":
					f.link(t, f.pid+"/ns/net", filepath.Join(f.proc, "other"))
				case "late 6443":
					f.link(t, f.pid+"/fd/5", "socket:[789]")
				case "canceled":
					cancel()
				}
				checkErr := scope.check(ctx)
				closeErr := scope.close()
				if mode == "valid" {
					if checkErr != nil || closeErr != nil {
						t.Fatal("stable dual check failed", checkErr, closeErr)
					}
				} else if checkErr != errBootstrapSource || closeErr != errBootstrapSource {
					t.Fatal("dual custody drift retained proof", checkErr, closeErr)
				}
				if scope.check(context.Background()) != errBootstrapSource {
					t.Fatal("closed dual scope revived")
				}
			}
			after, err := os.ReadDir("/proc/self/fd")
			if err != nil || len(after) != len(before) {
				t.Fatal("dual capture leaked owned descriptors", len(before), len(after), err)
			}
		})
	}
}
