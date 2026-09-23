package clusterremote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
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

	"golang.org/x/crypto/ssh"
)

func networkRenderFixture(t *testing.T) NetworkRenderRequest {
	t.Helper()
	v := targetManagementFixture(t)
	return NetworkRenderRequest{MachineID: v.Routing.Active.Declarations.MachineID, BootID: v.Routing.Active.Declarations.BootID, Link: v.Link, Targets: v.Routing.Targets}
}

// Run the real installed generator ONLY in generator mode with all four roots
// under the synthetic fixture. No netplan CLI, apply, reload or host output.
func installNetworkRenderFixture(t *testing.T, root string) {
	t.Helper()
	args := []string{"--root-dir", root}
	for _, name := range []string{"generator", "generator.early", "generator.late"} {
		path := filepath.Join(root, "run/systemd", name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		args = append(args, path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/lib/systemd/system-generators/netplan", args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	if err := cmd.Run(); err != nil {
		t.Fatal("isolated fixtures require Ubuntu native Netplan generator:", err)
	}
}

func TestNetworkRenderNativeCorrespondence(t *testing.T) {
	for _, mode := range []string{"success", "vendor shadow", "lexical merge", "earlier Ethernet", "later Ethernet", "merged usr", "later foreign", "changed DNS", "stale bytes", "empty mask", "symlink mask", "etc override", "earlier foreign", "vendor earlier", "legacy earlier", "local earlier", "dropin", "generic dropin", "malformed", "runtime Netplan", "unsupported auth", "unsupported match", "unsupported renderer", "unsupported device", "source drift", "runtime drift", "host drift", "link drift", "tool drift", "missing tool", "wrong generator", "generator writable", "generator failure", "generator hang", "generator overflow", "generator file overflow", "generator TERM", "generator incomplete", "generator writable output", "too many files", "oversize file", "writable directory", "symlink directory"} {
		t.Run(mode, func(t *testing.T) {
			testNetworkRenderCorrespondence(t, mode, false)
		})
	}
}

func testNetworkRenderCorrespondence(t *testing.T, mode string, boot bool) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, permissions os.FileMode) {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), permissions); err != nil {
			t.Fatal(err)
		}
	}
	write("etc/machine-id", strings.Repeat("a", 32), 0o600)
	write("proc/sys/kernel/random/boot_id", "11111111-1111-4111-8111-111111111111", 0o600)
	write("proc/self/ns/net", "namespace", 0o600)
	if err := os.MkdirAll(filepath.Join(root, "proc/1/ns"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "proc/self/ns/net"), filepath.Join(root, "proc/1/ns/net")); err != nil {
		t.Fatal(err)
	}
	write("etc/netplan/10-static.yaml", persistentNetplanFixture+"# private-source-comment\n", 0o600)
	if mode == "vendor shadow" {
		write("lib/netplan/10-static.yaml", strings.Replace(persistentNetplanFixture, ".251", ".245", 1), 0o600)
	}
	if mode == "lexical merge" || mode == "changed DNS" {
		write("etc/netplan/20-dns.yaml", "network:\n  ethernets:\n    ens18:\n      nameservers:\n        addresses: [192.168.3.1]\n", 0o600)
	}
	if mode == "earlier Ethernet" || mode == "later Ethernet" {
		name := "ens17"
		if mode == "later Ethernet" {
			name = "ens19"
		}
		write("etc/netplan/20-other.yaml", "network:\n  ethernets:\n    "+name+":\n      dhcp4: true\n", 0o600)
	}
	if mode == "merged usr" || boot {
		write("usr/lib/netplan/10-static.yaml", persistentNetplanFixture, 0o600)
		if err := os.Symlink("usr/lib", filepath.Join(root, "lib")); err != nil {
			t.Fatal(err)
		}
	}
	installNetworkRenderFixture(t, root)
	write("daemon.json", networkdDescriptionFixture, 0o600)
	write("kernel.json", targetManagementKernelFixture, 0o600)
	write("routing.json", routingStateFixture, 0o600)
	write("usr/libexec/netplan/generate", "trusted fixture tool identity", 0o700)
	write("usr/lib/x86_64-linux-gnu/libnetplan.so.1", "trusted fixture library identity", 0o600)
	write("usr/lib/systemd/system-generators/unused", "", 0o600)
	generator := filepath.Join(root, "usr/lib/systemd/system-generators/netplan")
	if err := os.Symlink("../../../libexec/netplan/generate", generator); err != nil {
		t.Fatal(err)
	}
	if boot {
		installNetworkBootFixture(t, root)
		if strings.HasPrefix(mode, "boot cloud ") {
			installNetworkCloudFixture(t, root)
			if mode == "boot cloud removed files" {
				paths, _ := filepath.Glob(filepath.Join(root, "usr/lib/systemd/system/cloud-*"))
				for _, path := range paths {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		if mode == "boot graphical" {
			path := filepath.Join(root, "etc/systemd/system/default.target")
			if os.Remove(path) != nil || os.Symlink("/lib/systemd/system/graphical.target", path) != nil {
				t.Fatal("graphical default fixture")
			}
		}
	}
	bootFixture := ""
	if boot {
		bootFixture = networkBootCommandFixture
	}
	fixture := strings.Replace(activeNetworkCommandFixture, "args = sys.argv[1:]\n", "args = sys.argv[1:]\n"+bootFixture+targetManagementCommandFixture+routedNetworkCommandFixture, 1)
	busMode := "success"
	if boot {
		busMode = mode
	}
	stub := "#!/usr/bin/python3\nROOT=" + strconv.Quote(root) + "\nMODE=" + strconv.Quote(busMode) + "\n" + fixture
	write("bin/busctl", stub, 0o700)
	write("bin/ip", stub, 0o700)
	write("bin/systemd/system-generators/netplan", "#!/usr/bin/python3\nROOT="+strconv.Quote(root)+"\nMODE="+strconv.Quote(mode)+networkRenderGeneratorFixture, 0o700)
	networkPath := "run/systemd/network/10-netplan-ens18.network"
	switch mode {
	case "changed DNS":
		write("etc/netplan/20-dns.yaml", "network:\n  ethernets:\n    ens18:\n      nameservers:\n        addresses: [192.168.3.2]\n", 0o600)
	case "stale bytes":
		write(networkPath, "[Match]\nName=ens18\n[Network]\nAddress=192.168.3.251/24\n", 0o600)
	case "empty mask":
		write(networkPath, "", 0o600)
	case "symlink mask":
		if os.Remove(filepath.Join(root, networkPath)) != nil || os.Symlink("/dev/null", filepath.Join(root, networkPath)) != nil {
			t.Fatal("mask fixture")
		}
	case "etc override":
		data, err := os.ReadFile(filepath.Join(root, networkPath))
		if err != nil {
			t.Fatal(err)
		}
		write("etc/systemd/network/10-netplan-ens18.network", string(data), 0o600)
	case "earlier foreign", "vendor earlier", "legacy earlier", "local earlier", "later foreign":
		area, name := "run", "05-foreign.network"
		if mode == "vendor earlier" {
			area = "usr/lib"
		} else if mode == "legacy earlier" {
			area = "lib"
		} else if mode == "local earlier" {
			area = "usr/local/lib"
		} else if mode == "later foreign" {
			name = "99-foreign.network"
		}
		write(area+"/systemd/network/"+name, "[Match]\nName=*\n[Network]\nDHCP=yes\n", 0o600)
	case "dropin", "generic dropin":
		name := "10-netplan-ens18.network.d"
		if mode == "generic dropin" {
			name = "network.d"
		}
		write("etc/systemd/network/"+name+"/50-custom.conf", "[Network]\nDNS=192.168.3.9\n", 0o600)
	case "malformed":
		write("etc/netplan/30-invalid.yaml", "invalid: [", 0o600)
	case "runtime Netplan":
		write("run/netplan/30-volatile.yaml", persistentNetplanFixture, 0o600)
	case "unsupported auth":
		write("etc/netplan/30-auth.yaml", "network:\n  ethernets:\n    ens19:\n      auth:\n        key-management: eap\n        method: peap\n        identity: private-fixture\n        password: private-fixture-password\n", 0o600)
	case "unsupported match":
		write("etc/netplan/30-match.yaml", "network:\n  ethernets:\n    ens19:\n      match:\n        name: ens19\n", 0o600)
	case "unsupported renderer":
		write("etc/netplan/30-nm.yaml", "network:\n  ethernets:\n    ens19:\n      renderer: NetworkManager\n      dhcp4: true\n", 0o600)
	case "unsupported device":
		write("etc/netplan/30-bridge.yaml", "network:\n  bridges:\n    br0:\n      dhcp4: true\n", 0o600)
	case "missing tool":
		if err := os.Remove(filepath.Join(root, "usr/libexec/netplan/generate")); err != nil {
			t.Fatal(err)
		}
	case "wrong generator":
		if os.Remove(generator) != nil || os.Symlink("/dev/null", generator) != nil {
			t.Fatal("generator fixture")
		}
	case "generator writable":
		if err := os.Chmod(filepath.Join(root, "usr/libexec/netplan/generate"), 0o777); err != nil {
			t.Fatal(err)
		}
	case "too many files":
		for i := 0; i < 257; i++ {
			write("etc/systemd/network/99-other"+strconv.Itoa(i)+".network", "[Match]\nName=other\n", 0o600)
		}
	case "oversize file":
		write("etc/systemd/network/99-other.network", strings.Repeat("x", 65537), 0o600)
	case "writable directory":
		if err := os.Chmod(filepath.Join(root, "run/systemd/network"), 0o777); err != nil {
			t.Fatal(err)
		}
	case "symlink directory":
		if os.Rename(filepath.Join(root, "run/systemd/network"), filepath.Join(root, "run/systemd/other")) != nil || os.Symlink("other", filepath.Join(root, "run/systemd/network")) != nil {
			t.Fatal("directory fixture")
		}
	}
	script := networkRenderScript
	if boot {
		script = networkBootScript
	}
	script = strings.Replace(script, `ROOT = "/"`, "ROOT = "+strconv.Quote(root), 1)
	script = strings.Replace(script, "EXPECTED_UID = 0", "EXPECTED_UID = "+strconv.Itoa(os.Getuid()), 1)
	script = strings.ReplaceAll(script, `"/usr/bin/busctl"`, strconv.Quote(filepath.Join(root, "bin/busctl")))
	script = strings.ReplaceAll(script, `"/usr/sbin/ip"`, strconv.Quote(filepath.Join(root, "bin/ip")))
	script = strings.ReplaceAll(script, `"/usr/lib/systemd/system-generators/netplan"`, strconv.Quote(filepath.Join(root, "bin/systemd/system-generators/netplan")))
	raw, _ := json.Marshal(routedNetworkFixture(t).Targets)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script, base64.StdEncoding.EncodeToString(raw))
	command.Env = append(os.Environ(), "NETPLAN_PARSER_IGNORE_ERRORS=1", "DBUS_SYSTEM_BUS_ADDRESS=invalid-inherited-bus", "SNAP=invalid-inherited-snap")
	out, err := command.Output()
	want := mode == "success" || mode == "boot graphical" || mode == "boot cloud disabled" || mode == "boot cloud unloaded" || mode == "vendor shadow" || mode == "lexical merge" || mode == "later Ethernet" || mode == "merged usr" || mode == "later foreign"
	if ctx.Err() != nil || (err == nil) != want || bytes.Contains(out, []byte("private-")) {
		t.Fatalf("native correspondence outcome: error=%v deadline=%v bytes=%d", err, ctx.Err(), len(out))
	}
	if want {
		var value networkRenderObservation
		if json.Unmarshal(out, &value) != nil || value.Version != map[bool]int{false: 1, true: 2}[boot] || value.Management.validate() != nil || value.Management.Link.Interface != "ens18" {
			t.Fatal("invalid successful projection")
		}
	} else if len(out) != 0 {
		t.Fatal("failure emitted partial evidence")
	}
	left, err := filepath.Glob(filepath.Join(root, "run/.borealis-network-render-*"))
	if err != nil || len(left) != 0 {
		t.Fatal("private render scratch not removed")
	}
	if mode == "generator file overflow" {
		if body, err := os.ReadFile(filepath.Join(root, "file-limit")); err != nil || string(body) != "bounded" {
			t.Fatal("kernel file-size bound missing")
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "generator-pid")); err == nil {
		pid, err := strconv.Atoi(string(raw))
		if err != nil || syscall.Kill(pid, 0) != syscall.ESRCH {
			t.Fatal("generator not killed and joined")
		}
	}
}

const networkRenderGeneratorFixture = `
import errno, os, pathlib, signal, sys, time
root = pathlib.Path(ROOT)
args = sys.argv[1:]
assert set(os.environ) == {"PATH", "LC_ALL"}
assert len(args) == 5 and args[0] == "--root-dir"
scratch = pathlib.Path(args[1])
assert scratch.parent == root/"run" and scratch.name.startswith(".borealis-network-render-")
assert args[2:] == [str(scratch/"run/systemd"/name) for name in ("generator", "generator.early", "generator.late")]
assert scratch.stat().st_mode & 0o777 == 0o700
payload = scratch/"etc/netplan/observation.yaml"
assert payload.stat().st_mode & 0o777 == 0o600
assert "private-" not in payload.read_text()
(root/"generator-pid").write_text(str(os.getpid()))
if MODE == "generator hang": time.sleep(20)
if MODE == "generator TERM":
    os.kill(os.getppid(), signal.SIGTERM)
    time.sleep(20)
if MODE == "generator failure":
    print("private-generator-diagnostic", file=sys.stderr)
    sys.exit(1)
if MODE == "generator overflow":
    os.write(1, b"x"*131073)
    sys.exit(0)
if MODE == "generator file overflow":
    try:
        (scratch/"oversized-output").write_bytes(b"x"*65537)
    except OSError as error:
        assert error.errno == errno.EFBIG
        (root/"file-limit").write_text("bounded")
    sys.exit(1)
if MODE == "generator incomplete": sys.exit(0)
if MODE == "generator writable output":
    directory = scratch/"run/systemd/network"
    directory.mkdir(parents=True)
    output = directory/"10-netplan-ens18.network"
    output.write_text("[Match]\nName=ens18\n")
    output.chmod(0o666)
    sys.exit(0)
if MODE == "source drift":
    with (root/"etc/netplan/10-static.yaml").open("a") as out: out.write("# changed\n")
if MODE == "runtime drift":
    with (root/"run/systemd/network/10-netplan-ens18.network").open("a") as out: out.write("# changed\n")
if MODE == "host drift": (root/"etc/machine-id").write_text("b"*32)
if MODE == "link drift":
    path = root/"kernel.json"
    path.write_text(path.read_text().replace("02:00:00:00:00:01", "02:00:00:00:00:09"))
if MODE == "tool drift":
    with (root/"usr/lib/x86_64-linux-gnu/libnetplan.so.1").open("a") as out: out.write("changed")
os.execve("/usr/lib/systemd/system-generators/netplan", ["/usr/lib/systemd/system-generators/netplan", *args], dict(os.environ))
`

func TestNetworkRenderNativePinnedScope(t *testing.T) {
	testNetworkRenderNativePinnedScope(t, false)
}

func testNetworkRenderNativePinnedScope(t *testing.T, boot bool) {
	t.Helper()
	version := map[bool]int{false: 1, true: 2}[boot]
	matches := func(v TargetNetworkRender, floor time.Time, target Target, key HostKey, request NetworkRenderRequest) error {
		if boot {
			return (TargetNetworkBoot{observation: v}).Matches(floor, target, key, request)
		}
		return v.Matches(floor, target, key, request)
	}
	for _, mode := range []string{"success", "wrong port", "wrong key", "unbound address", "failed authority", "lost during command", "callback deadline", "cancel", "private output", "changed result", "wrong version", "duplicate", "trailing", "empty", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			r := networkRenderFixture(t)
			command, _ := networkRenderCommand(r.Targets)
			if boot {
				command, _ = networkBootCommand(r.Targets)
			}
			var commands, checks atomic.Int32
			var joined atomic.Bool
			server := newFakeSSH(t, "network render", func(cmd string, channel ssh.Channel) uint32 {
				commands.Add(1)
				if cmd != command {
					return 1
				}
				input, err := io.ReadAll(channel)
				if err != nil || !bytes.Equal(input, []byte("private-sudo\n")) {
					return 1
				}
				if mode == "lost during command" || mode == "callback deadline" || mode == "cancel" {
					time.Sleep(1300 * time.Millisecond)
				}
				wire := networkRenderObservation{Version: version, Management: targetManagementFixture(t)}
				if mode == "private output" {
					channel.Stderr().Write([]byte("private-sudo"))
					return 1
				}
				if mode == "changed result" {
					wire.Management.Link.Index++
				}
				if mode == "wrong version" {
					wire.Version = 3 - version
				}
				out, _ := json.Marshal(wire)
				if mode == "duplicate" {
					out = bytes.Replace(out, []byte(`"version":`+strconv.Itoa(version)), []byte(`"version":1,"version":`+strconv.Itoa(version)), 1)
				}
				if mode == "trailing" {
					out = append(out, out...)
				}
				if mode == "empty" {
					out = nil
				}
				if mode == "overflow" {
					out = bytes.Repeat([]byte("x"), MaxOutputBytes+1)
				}
				channel.Write(out)
				return 0
			})
			server.target.Address = r.Targets.Management
			credential, _ := PasswordCredential("operator", server.password)
			defer credential.Destroy()
			client, err := server.transport.Connect(context.Background(), server.target, server.key, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			check := func(ctx context.Context) error {
				n := checks.Add(1)
				if mode == "failed authority" || mode == "lost during command" && n > 1 {
					return errors.New("private-authority")
				}
				if mode == "cancel" && n > 1 {
					cancel()
				}
				if mode == "callback deadline" && n > 1 {
					<-ctx.Done()
					joined.Store(true)
				}
				return nil
			}
			target, key := server.target, server.key
			if mode == "wrong port" {
				target.Port++
			}
			if mode == "wrong key" {
				key = newFakeSSH(t, "other").key
			}
			if mode == "unbound address" {
				r.Targets.Management = "192.168.3.248"
			}
			started := time.Now()
			var result TargetNetworkRender
			if boot {
				var value TargetNetworkBoot
				value, err = client.InspectNetworkBoot(ctx, []byte("private-sudo"), target, key, r, check)
				result = value.observation
			} else {
				result, err = client.InspectNetworkRender(ctx, []byte("private-sudo"), target, key, r, check)
			}
			if mode != "success" {
				if err != ErrNetworkRender || !reflect.DeepEqual(result, TargetNetworkRender{}) {
					t.Fatal("unsafe render result", err)
				}
				if mode == "callback deadline" && !joined.Load() {
					t.Fatal("callback not joined")
				}
				if (mode == "wrong port" || mode == "wrong key" || mode == "unbound address" || mode == "failed authority") && commands.Load() != 0 {
					t.Fatal("invalid request sent")
				}
				return
			}
			if err != nil || matches(result, started, target, key, r) != nil {
				t.Fatal("valid observation", err)
			}
			for _, change := range []string{"old", "future", "zero", "serialized", "machine", "boot", "MAC", "namespace", "peers", "port", "pin"} {
				v, request, floor, target, key := result, r, started, target, key
				request.Targets.Peers = append([]string(nil), r.Targets.Peers...)
				switch change {
				case "old":
					floor = time.Now()
				case "future":
					v.finished = time.Now().Add(time.Hour)
				case "zero":
					floor = time.Time{}
				case "serialized":
					raw, _ := json.Marshal(v)
					v = TargetNetworkRender{}
					_ = json.Unmarshal(raw, &v)
				case "machine":
					request.MachineID = strings.Repeat("b", 32)
				case "boot":
					request.BootID = "22222222-2222-4222-8222-222222222222"
				case "MAC":
					request.Link.MAC = "02:00:00:00:00:09"
				case "namespace":
					request.Link.NetworkNamespace++
				case "peers":
					request.Targets.Peers = request.Targets.Peers[:1]
				case "port":
					target.Port++
				case "pin":
					key.PublicKey = []byte("different")
				}
				if matches(v, floor, target, key, request) == nil {
					t.Fatal("stale/changed observation accepted", change)
				}
			}
		})
	}
}

func TestNetworkRenderShellData(t *testing.T) {
	testNetworkRenderShellData(t, false)
}

func testNetworkRenderShellData(t *testing.T, boot bool) {
	t.Helper()
	build := networkRenderCommand
	if boot {
		build = networkBootCommand
	}
	targets := routedNetworkFixture(t).Targets
	expected, _ := json.Marshal(targets)
	for _, mode := range []string{"password", "nopasswd", "root"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "stdin-must-not-execute")
			for name, body := range map[string]string{"id": "#!/bin/sh\nif [ \"${FIXTURE_ROOT:-}\" = 1 ]; then printf 0; else printf 1000; fi\n", "sudo": "#!/bin/sh\ntest \"$1\" = -k && test \"$2\" = -S && test \"$3\" = -p && test \"$4\" = '' && test \"$5\" = -- || exit 2\nshift 5\nif [ \"$FIXTURE_MODE\" = password ]; then IFS= read -r supplied || exit 3; fi\nexport FIXTURE_ROOT=1\nexec \"$@\"\n"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			command, err := build(targets)
			if err != nil {
				t.Fatal(err)
			}
			command = strings.ReplaceAll(command, "/usr/sbin:/usr/bin:/sbin:/bin", root+":/usr/bin:/bin")
			command = strings.ReplaceAll(command, "main(observe_network_render)", "main(route_targets)")
			command = strings.ReplaceAll(command, "main(lambda: observe_network_render(boot_snapshot))", "main(route_targets)")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
			cmd.Env = append(os.Environ(), "FIXTURE_MODE="+mode)
			if mode == "root" {
				cmd.Env = append(cmd.Env, "FIXTURE_ROOT=1")
			}
			cmd.Stdin = strings.NewReader("touch " + shellConstant(marker) + "; $(touch " + shellConstant(marker) + ")\n")
			raw, err := cmd.Output()
			if err != nil || !bytes.Equal(bytes.TrimSpace(raw), expected) {
				t.Fatal("data/sudo framing", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("password executed")
			}
		})
	}
	for _, targets := range []RouteTargets{{Management: "192.168.3.251", Peers: nil}, {Management: "$(id)", Peers: []string{"192.168.3.250"}}, {Management: "192.168.3.251", Peers: []string{"--help"}}, {Management: "192.168.3.251", Peers: []string{"192.168.3.250", "192.168.3.250"}}} {
		if _, err := build(targets); err != ErrNetworkRender {
			t.Fatal("input crossed transport")
		}
	}
}
