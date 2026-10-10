package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const targetManagementKernelFixture = `[{"ifindex":2,"ifname":"ens18","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UP","link_type":"ether","address":"02:00:00:00:00:01","broadcast":"ff:ff:ff:ff:ff:ff","addr_info":[{"family":"inet","local":"192.168.3.251","prefixlen":24,"scope":"global","valid_life_time":4294967295,"preferred_life_time":4294967295}]}]`

func targetManagementFixture(t *testing.T) managementLinkObservation {
	t.Helper()
	routing := routedNetworkFixture(t)
	return managementLinkObservation{Version: 1, Routing: routing, Link: clusterbootstrap.ManagementLink{Interface: "ens18", Index: 2, Address: "192.168.3.251/24", MAC: "02:00:00:00:00:01", NetworkNamespace: routing.Active.NetworkNamespace}}
}

func TestTargetManagementLinkTypedBinding(t *testing.T) {
	server := newFakeSSH(t, "target link")
	server.target.Address = "192.168.3.251"
	value := TargetManagementLink{target: server.target, approved: server.key, wire: targetManagementFixture(t)}
	peers := value.wire.Routing.Targets.Peers
	if link, err := value.ManagementLink(routedPrivilegedFixture(t), server.target, server.key, peers); err != nil || link != value.wire.Link {
		t.Fatal("valid binding", err)
	}
	for _, mode := range []string{"zero", "address", "port", "key", "machine", "boot", "index", "interface", "prefix", "missing peers", "wrong peer", "local peer"} {
		t.Run(mode, func(t *testing.T) {
			v, f, target, key, p := value, routedPrivilegedFixture(t), server.target, server.key, peers
			switch mode {
			case "zero":
				v = TargetManagementLink{}
			case "address":
				target.Address = "192.168.3.250"
			case "port":
				target.Port++
			case "key":
				key = newFakeSSH(t, "other").key
			case "machine":
				f.MachineID = strings.Repeat("c", 32)
			case "boot":
				f.BootID = "22222222-2222-4222-8222-222222222222"
			case "index":
				f.Addresses[0].Index++
			case "interface":
				f.Addresses[0].Interface = "ens19"
			case "prefix":
				f.Addresses[0].Prefix = f.Addresses[0].Prefix.Masked()
			case "missing peers":
				p = peers[:1]
			case "wrong peer":
				p = []string{"192.168.3.249", "192.168.3.252"}
			case "local peer":
				p = []string{"192.168.3.251", "192.168.3.252"}
			}
			if link, err := v.ManagementLink(f, target, key, p); err != ErrManagementLink || link != (clusterbootstrap.ManagementLink{}) {
				t.Fatal("changed binding accepted", err)
			}
		})
	}
	for name, edit := range map[string]func(*managementLinkObservation){
		"version":      func(v *managementLinkObservation) { v.Version = 2 },
		"missing link": func(v *managementLinkObservation) { v.Link = clusterbootstrap.ManagementLink{} },
		"MAC":          func(v *managementLinkObservation) { v.Link.MAC = "01:00:00:00:00:01" },
		"namespace":    func(v *managementLinkObservation) { v.Link.NetworkNamespace++ },
		"index":        func(v *managementLinkObservation) { v.Link.Index++ },
		"interface":    func(v *managementLinkObservation) { v.Link.Interface = "ens19" },
		"prefix":       func(v *managementLinkObservation) { v.Link.Address = "192.168.3.251/25" },
		"address":      func(v *managementLinkObservation) { v.Link.Address = "192.168.3.252/24" },
		"owner":        func(v *managementLinkObservation) { v.Routing.Active.Owner = "org.freedesktop.network1" },
		"no route":     func(v *managementLinkObservation) { v.Routing.Networks = []DirectNetwork{} },
		"no lookup":    func(v *managementLinkObservation) { v.Routing.Resolved = []string{} },
	} {
		t.Run(name, func(t *testing.T) {
			v := targetManagementFixture(t)
			edit(&v)
			raw, _ := json.Marshal(v)
			if _, err := parseManagementLink(raw); err != ErrManagementLink {
				t.Fatal("invalid link accepted")
			}
		})
	}
	raw, _ := json.Marshal(value.wire)
	for _, s := range []string{strings.Replace(string(raw), `"link":`, `"Link":`, 1), strings.Replace(string(raw), `"mac":`, `"private":"hidden","mac":`, 1), strings.Replace(string(raw), `"index":2`, `"index":2,"index":2`, 1), strings.Replace(string(raw), `"version":1,`, "", 1), string(raw) + string(raw), "null", "{}"} {
		if _, err := parseManagementLink([]byte(s)); err != ErrManagementLink {
			t.Fatal("ambiguous wire accepted")
		}
	}
}

func TestTargetManagementLinkParserParity(t *testing.T) {
	// Compare both actual host adapters against one adversarial byte corpus.
	type sample struct {
		name  string
		raw   []byte
		valid bool
	}
	cases := []sample{{"valid", []byte(targetManagementKernelFixture), true}}
	for _, tc := range []struct {
		name, key   string
		value       any
		row, remove bool
	}{
		{name: "missing hardware", key: "link_type", remove: true}, {name: "non Ethernet", key: "link_type", value: "infiniband"},
		{name: "down", key: "operstate", value: "DOWN"}, {name: "master", key: "master"}, {name: "composite", key: "linkinfo", value: map[string]any{}},
		{name: "parent", key: "link", value: "ens19"}, {name: "parent index", key: "link_index", value: 3}, {name: "peer namespace", key: "link_netnsid", value: 0}, {name: "point to point", key: "link_pointtopoint", value: false},
		{name: "NOARP", key: "flags", value: []string{"UP", "LOWER_UP", "BROADCAST", "NOARP"}},
		{name: "no carrier", key: "flags", value: []string{"UP", "BROADCAST", "MULTICAST"}},
		{name: "duplicate flag", key: "flags", value: []string{"UP", "LOWER_UP", "BROADCAST", "UP"}},
		{name: "null flags", key: "flags"}, {name: "numeric flag", key: "flags", value: []any{"UP", "LOWER_UP", "BROADCAST", 1}},
		{name: "broadcast", key: "broadcast", value: "00:00:00:00:00:00"},
		{name: "multicast MAC", key: "address", value: "01:00:00:00:00:01"}, {name: "zero MAC", key: "address", value: "00:00:00:00:00:00"},
		{name: "uppercase MAC", key: "address", value: "02:00:00:00:00:AB"}, {name: "short MAC", key: "address", value: "02:00:00:01"},
		{name: "bool index", key: "ifindex", value: true}, {name: "large index", key: "ifindex", value: 2147483648}, {name: "bad name", key: "ifname", value: "$(id)"},
		{name: "missing rows", key: "addr_info", remove: true}, {name: "null rows", key: "addr_info"},
		{name: "wrong family", key: "family", value: "inet6", row: true}, {name: "scope", key: "scope", value: "host", row: true},
		{name: "peer", key: "peer", value: "192.168.3.252", row: true}, {name: "DHCP", key: "dynamic", value: true, row: true},
		{name: "null DHCP", key: "dynamic", row: true}, {name: "integer DHCP", key: "dynamic", value: 0, row: true},
		{name: "deprecated", key: "deprecated", value: true, row: true}, {name: "tentative", key: "tentative", value: true, row: true}, {name: "DAD failure", key: "dadfailed", value: true, row: true},
		{name: "expires", key: "valid_life_time", value: 300, row: true}, {name: "null lifetime", key: "valid_life_time", row: true},
		{name: "string lifetime", key: "valid_life_time", value: "4294967295", row: true}, {name: "missing lifetime", key: "preferred_life_time", remove: true, row: true},
		{name: "bool prefix", key: "prefixlen", value: true, row: true}, {name: "public prefix", key: "prefixlen", value: 15, row: true}, {name: "31 prefix", key: "prefixlen", value: 31, row: true},
	} {
		var rows []map[string]any
		if json.Unmarshal([]byte(targetManagementKernelFixture), &rows) != nil {
			t.Fatal("fixture")
		}
		obj := rows[0]
		if tc.row {
			obj = obj["addr_info"].([]any)[0].(map[string]any)
		}
		if tc.remove {
			delete(obj, tc.key)
		} else {
			obj[tc.key] = tc.value
		}
		raw, _ := json.Marshal(rows)
		cases = append(cases, sample{tc.name, raw, false})
	}
	for name, raw := range map[string]string{
		"duplicate JSON":               strings.Replace(targetManagementKernelFixture, `"mtu":1500`, `"mtu":1500,"mtu":1500`, 1),
		"alias JSON":                   strings.Replace(targetManagementKernelFixture, `"mtu":1500`, `"mtu":1500,"MTU":1500`, 1),
		"nested aliases":               strings.Replace(targetManagementKernelFixture, `"mtu":1500`, `"private":{"x":1,"X":1}`, 1),
		"systemd aliases":              strings.Replace(targetManagementKernelFixture, `"mtu":1500`, `"private":{"ValidLifetimeUSec":1,"ValidLifetimeUsec":1}`, 1),
		"duplicate interface":          strings.TrimSuffix(targetManagementKernelFixture, "]") + "," + targetManagementKernelFixture[1:],
		"duplicate on other interface": strings.TrimSuffix(targetManagementKernelFixture, "]") + "," + strings.ReplaceAll(strings.ReplaceAll(targetManagementKernelFixture[1:], "ens18", "ens19"), `"ifindex":2`, `"ifindex":3`),
		"null":                         "null", "empty": "[]", "null interface": "[null]", "trailing": targetManagementKernelFixture + "{}",
	} {
		cases = append(cases, sample{name, []byte(raw), false})
	}
	for _, tc := range []struct {
		name                   string
		interfaces, per, total int
		valid                  bool
	}{
		{"interfaces limit", 128, 0, 0, true}, {"interfaces overflow", 129, 0, 0, false}, {"rows limit", 2, 256, 0, true}, {"rows overflow", 2, 257, 0, false}, {"total limit", 6, 0, 1023, true}, {"total overflow", 6, 0, 1024, false},
	} {
		var rows []map[string]any
		json.Unmarshal([]byte(targetManagementKernelFixture), &rows)
		remaining := tc.total
		for i := 1; i < tc.interfaces; i++ {
			count := tc.per
			if tc.total > 0 {
				count = min(remaining, 256)
				remaining -= count
			}
			addresses := make([]any, 0, count)
			for range count {
				addresses = append(addresses, map[string]any{"local": "10.0.0.1"})
			}
			rows = append(rows, map[string]any{"ifname": fmt.Sprintf("ens%d", i+18), "ifindex": i + 2, "addr_info": addresses})
		}
		raw, _ := json.Marshal(rows)
		cases = append(cases, sample{tc.name, raw, tc.valid})
	}
	cases = append(cases, sample{"current MAC only", []byte(strings.Replace(targetManagementKernelFixture, `"mtu":1500`, `"private":"withheld","permaddr":"02:00:00:00:00:02"`, 1)), true})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goLink, goErr := clusterbootstrap.ParseManagementLink(tc.raw, 1234, "192.168.3.251")
			script := persistentNetworkLibraryScript + activeNetworkLibraryScript + managementLinkLibraryScript + "\nmain(lambda: management_link(management_json(sys.stdin.buffer.read()),1234,'192.168.3.251'))\n"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script)
			cmd.Stdin = bytes.NewReader(tc.raw)
			raw, err := cmd.Output()
			if ctx.Err() != nil || (goErr == nil) != tc.valid || (err == nil) != tc.valid {
				t.Fatal("parser mismatch", goErr, err, ctx.Err())
			}
			if tc.valid {
				want, _ := json.Marshal(goLink)
				if !bytes.Equal(bytes.TrimSpace(raw), want) {
					t.Fatal("projection mismatch")
				}
			} else if len(raw) != 0 {
				t.Fatal("invalid link emitted evidence")
			}
		})
	}
}

func TestTargetManagementLinkObservationScope(t *testing.T) {
	for _, mode := range []string{"success", "MAC changes during first routes", "MAC changes between snapshots", "MAC changes during final routes", "index changes", "address changes", "host namespace changes", "init namespace differs", "init namespace changes", "link malformed", "link duplicate JSON", "link overflow", "link failed exit", "link hang", "link pipe closed hang", "owner changed", "bus changed", "namespace changed", "boot changed", "machine changed", "netplan changed", "runtime changed", "kernel changed", "daemon changed", "rules changed", "routes changed", "cached redirect", "lookup changed", "foreign", "wrong index"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			write := func(name, body string, mode os.FileMode) {
				p := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("etc/machine-id", strings.Repeat("a", 32), 0o600)
			write("proc/sys/kernel/random/boot_id", "11111111-1111-4111-8111-111111111111", 0o600)
			write("proc/self/ns/net", "namespace fixture", 0o600)
			if err := os.MkdirAll(filepath.Join(root, "proc/1/ns"), 0o700); err != nil {
				t.Fatal(err)
			}
			if mode == "init namespace differs" {
				write("proc/1/ns/net", "different namespace", 0o600)
			} else if err := os.Link(filepath.Join(root, "proc/self/ns/net"), filepath.Join(root, "proc/1/ns/net")); err != nil {
				t.Fatal(err)
			}
			write("etc/netplan/10-static.yaml", persistentNetplanFixture, 0o600)
			write("run/systemd/network/10-netplan-ens18.network", "[Match]\nName=ens18\n[Network]\nAddress=192.168.3.251/24\n", 0o600)
			write("daemon.json", networkdDescriptionFixture, 0o600)
			write("kernel.json", targetManagementKernelFixture, 0o600)
			write("routing.json", routingStateFixture, 0o600)
			fixture := strings.Replace(activeNetworkCommandFixture, "args = sys.argv[1:]\n", "args = sys.argv[1:]\n"+targetManagementCommandFixture+routedNetworkCommandFixture, 1)
			stub := "#!/usr/bin/python3\nROOT=" + strconv.Quote(root) + "\nMODE=" + strconv.Quote(mode) + "\n" + fixture
			write("bin/ip", stub, 0o700)
			write("bin/busctl", stub, 0o700)
			script := strings.Replace(managementLinkScript, `ROOT = "/"`, "ROOT = "+strconv.Quote(root), 1)
			script = strings.Replace(script, "EXPECTED_UID = 0", "EXPECTED_UID = "+strconv.Itoa(os.Getuid()), 1)
			script = strings.ReplaceAll(script, `"/usr/bin/busctl"`, strconv.Quote(filepath.Join(root, "bin/busctl")))
			script = strings.ReplaceAll(script, `"/usr/sbin/ip"`, strconv.Quote(filepath.Join(root, "bin/ip")))
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			targets, _ := json.Marshal(routedNetworkFixture(t).Targets)
			cmd := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script, base64.StdEncoding.EncodeToString(targets))
			cmd.Env = append(os.Environ(), "DBUS_SYSTEM_BUS_ADDRESS=invalid-fixture", "HTTP_PROXY=private-fixture")
			raw, err := cmd.Output()
			if ctx.Err() != nil || (err == nil) != (mode == "success") {
				t.Fatal("observation outcome", err, ctx.Err())
			}
			if err == nil {
				v, err := parseManagementLink(raw)
				if err != nil {
					t.Fatal(err)
				}
				if v.Link.MAC != "02:00:00:00:00:01" {
					t.Fatal("wrong MAC")
				}
				if _, err := v.Routing.ManagementRoutes(routedPrivilegedFixture(t), "192.168.3.251", v.Routing.Targets.Peers); err != nil {
					t.Fatal(err)
				}
				count, _ := os.ReadFile(filepath.Join(root, "count-detailed"))
				if string(count) != "4" {
					t.Fatal("link did not bracket routes")
				}
			} else if len(raw) != 0 {
				t.Fatal("failed observer emitted evidence")
			}
			if pidBytes, err := os.ReadFile(filepath.Join(root, "child-pid")); err == nil {
				pid, err := strconv.Atoi(string(pidBytes))
				if err != nil || syscall.Kill(pid, 0) != syscall.ESRCH {
					t.Fatal("child not killed and joined")
				}
			}
		})
	}
}

const targetManagementCommandFixture = `if pathlib.Path(sys.argv[0]).name == "ip" and args == ["-j", "-d", "-4", "address", "show"]:
    assert sys.stdin.buffer.read() == b''
    counter = root/'count-detailed'
    n = int(counter.read_text())+1 if counter.exists() else 1
    counter.write_text(str(n))
    value = json.loads((root/'kernel.json').read_text())
    if MODE == "MAC changes during first routes" and n >= 2 or MODE == "MAC changes between snapshots" and n >= 3 or MODE == "MAC changes during final routes" and n >= 4: value[0]['address'] = '02:00:00:00:00:02'
    if MODE == "index changes" and n > 1: value[0]['ifindex'] = 3
    if MODE == "address changes" and n > 1: value[0]['addr_info'][0]['local'] = '192.168.3.249'
    if MODE in ("host namespace changes", "init namespace changes") and n == 2:
        path = root/('proc/self/ns/net' if MODE == "host namespace changes" else 'proc/1/ns/net')
        path.unlink(); path.write_text('replaced namespace')
    if MODE == "machine changed" and n == 2: (root/'etc/machine-id').write_text('c'*32)
    if MODE in ("link hang", "link pipe closed hang"):
        (root/'child-pid').write_text(str(os.getpid()))
        if MODE == "link pipe closed hang": os.close(1)
        time.sleep(30)
    if MODE == "link overflow": sys.stdout.write('x'*131073); sys.exit(0)
    if MODE == "link malformed": print('private fixture diagnostics'); sys.exit(0)
    output = json.dumps(value)
    if MODE == "link duplicate JSON": output = output.replace('"ifindex":', '"ifindex":2,"ifindex":', 1)
    print(output)
    sys.exit(1 if MODE == "link failed exit" else 0)
`

func TestTargetManagementLinkPinnedSSH(t *testing.T) {
	wire, _ := json.Marshal(targetManagementFixture(t))
	targets := routedNetworkFixture(t).Targets
	expected, _ := managementLinkCommand(targets)
	for _, mode := range []string{"success", "invalid sudo", "wrong endpoint", "wrong targets", "malformed", "overflow", "failed exit", "cancel", "wrong key"} {
		t.Run(mode, func(t *testing.T) {
			release := make(chan struct{})
			server := newFakeSSH(t, "target link", func(command string, channel ssh.Channel) uint32 {
				if command != expected {
					return 1
				}
				stdin, err := io.ReadAll(channel)
				if err != nil || !bytes.Equal(stdin, []byte("target link\n")) {
					return 1
				}
				switch mode {
				case "cancel":
					<-release
				case "malformed":
					channel.Write([]byte(`{"private":"fixture"}`))
				case "overflow":
					channel.Write(bytes.Repeat([]byte("x"), MaxOutputBytes+1))
				case "failed exit":
					channel.Write(wire)
					channel.Stderr().Write([]byte("private fixture diagnostics"))
					return 1
				case "wrong targets":
					channel.Write(bytes.ReplaceAll(wire, []byte("192.168.3.250"), []byte("192.168.3.249")))
				default:
					channel.Write(wire)
				}
				return 0
			})
			server.target.Address = "192.168.3.251"
			credential, err := PasswordCredential("operator", server.password)
			if err != nil {
				t.Fatal(err)
			}
			defer credential.Destroy()
			key := server.key
			if mode == "wrong key" {
				key = newFakeSSH(t, "other").key
			}
			client, err := server.transport.Connect(context.Background(), server.target, key, credential)
			if mode == "wrong key" {
				if err != ErrHostKeyChanged || server.authCalls.Load() != 0 || server.execCalls.Load() != 0 {
					t.Fatal("pin mismatch crossed authentication")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			password := []byte("target link")
			if mode == "invalid sudo" {
				password = append(password, '\n')
			}
			request := targets
			if mode == "wrong endpoint" {
				request.Management = "192.168.3.249"
			}
			value, err := client.InspectManagementLink(ctx, password, request)
			close(release)
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				link, err := value.ManagementLink(routedPrivilegedFixture(t), server.target, server.key, targets.Peers)
				if err != nil || link != targetManagementFixture(t).Link {
					t.Fatal("SSH binding", err)
				}
				clear(client.approved.PublicKey)
				if _, err := value.ManagementLink(routedPrivilegedFixture(t), server.target, server.key, targets.Peers); err != nil {
					t.Fatal("key bytes aliased")
				}
			} else {
				want := map[string]error{"invalid sudo": ErrInvalidAuth, "wrong endpoint": ErrManagementLink, "wrong targets": ErrManagementLink, "malformed": ErrManagementLink, "overflow": ErrOutputLimit, "failed exit": ErrPrivilegeInspection, "cancel": ErrCancelled}[mode]
				if err != want || !reflect.DeepEqual(value, TargetManagementLink{}) {
					t.Fatal("transport boundary", err, want)
				}
			}
			if (mode == "invalid sudo" || mode == "wrong endpoint") && server.execCalls.Load() != 0 {
				t.Fatal("invalid input executed")
			}
		})
	}
}

func TestTargetManagementLinkShellData(t *testing.T) {
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
			command, err := managementLinkCommand(targets)
			if err != nil {
				t.Fatal(err)
			}
			command = strings.ReplaceAll(command, "/usr/sbin:/usr/bin:/sbin:/bin", root+":/usr/bin:/bin")
			command = strings.ReplaceAll(command, "main(observe_management_link)", "main(route_targets)")
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
		if _, err := (*Client)(nil).InspectManagementLink(context.Background(), nil, targets); err != ErrManagementLink {
			t.Fatal("input crossed transport")
		}
	}
}
