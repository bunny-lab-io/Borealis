package clusterremote

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func activeNetworkFixture(t *testing.T) ActiveNetworkOwnership {
	t.Helper()
	declarations, err := parsePersistentNetwork([]byte(persistentNetworkFixture))
	if err != nil {
		t.Fatal(err)
	}
	return ActiveNetworkOwnership{Version: 1, Declarations: declarations, BusID: strings.Repeat("b", 32), Owner: ":1.42", NetworkNamespace: 4026531840,
		Interfaces: []ActiveNetworkInterface{{Name: "ens18", Index: 2, Addresses: []string{"192.168.3.251/24"}}}}
}

func TestActiveNetworkRequiresExactCurrentInterface(t *testing.T) {
	value := activeNetworkFixture(t)
	facts, _ := parsePrivilegedFacts([]byte(strings.Replace(privilegedHostFixture, `"ifname":`, `"ifindex":2,"ifname":`, 1)))
	if prefix, err := value.ManagementNetwork(facts, "192.168.3.251", []string{"192.168.3.250"}); err != nil || prefix.String() != "192.168.3.0/24" {
		t.Fatal("valid active management evidence rejected", err)
	}
	for _, index := range []int{0, 3} {
		facts.Addresses[0].Index = index
		if _, err := value.ManagementNetwork(facts, "192.168.3.251", nil); err != ErrActiveNetwork {
			t.Fatal("missing/replaced interface index accepted")
		}
	}
	facts.Addresses[0].Index = 2
	facts.BootID = "22222222-2222-4222-8222-222222222222"
	if _, err := value.ManagementNetwork(facts, "192.168.3.251", nil); err != ErrActiveNetwork {
		t.Fatal("changed host boot accepted")
	}
	for name, edit := range map[string]func(*ActiveNetworkOwnership){
		"owner":       func(v *ActiveNetworkOwnership) { v.Owner = "org.freedesktop.network1" },
		"bus":         func(v *ActiveNetworkOwnership) { v.BusID = strings.Repeat("0", 32) },
		"namespace":   func(v *ActiveNetworkOwnership) { v.NetworkNamespace = 0 },
		"index":       func(v *ActiveNetworkOwnership) { v.Interfaces[0].Index = 0 },
		"undeclared":  func(v *ActiveNetworkOwnership) { v.Interfaces[0].Addresses[0] = "192.168.3.252/24" },
		"wrong name":  func(v *ActiveNetworkOwnership) { v.Interfaces[0].Name = "ens19" },
		"duplicates":  func(v *ActiveNetworkOwnership) { v.Interfaces = append(v.Interfaces, v.Interfaces[0]) },
		"null result": func(v *ActiveNetworkOwnership) { v.Interfaces = nil },
	} {
		t.Run(name, func(t *testing.T) {
			v := activeNetworkFixture(t)
			edit(&v)
			raw, _ := json.Marshal(v)
			if _, err := parseActiveNetwork(raw); err != ErrActiveNetwork {
				t.Fatal("invalid ownership accepted")
			}
		})
	}
	raw, _ := json.Marshal(value)
	for _, changed := range []string{
		strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(raw), `"owner":`, `"Owner":`, 1),
		strings.Replace(string(raw), `"owner":`, `"private":"not-public","owner":`, 1),
		strings.Replace(string(raw), `"network_namespace":4026531840,`, "", 1),
		string(raw) + string(raw),
	} {
		if _, err := parseActiveNetwork([]byte(changed)); err != ErrActiveNetwork {
			t.Fatal("ambiguous public JSON accepted")
		}
	}
}

func TestActiveNetworkPinnedSSHBoundaries(t *testing.T) {
	raw, _ := json.Marshal(activeNetworkFixture(t))
	for _, mode := range []string{"success", "malformed", "overflow", "failed exit", "cancel", "invalid sudo"} {
		t.Run(mode, func(t *testing.T) {
			received := make(chan []byte, 1)
			release := make(chan struct{})
			server := newFakeSSH(t, "active network", func(command string, channel ssh.Channel) uint32 {
				if command != activeNetworkCommand {
					return 1
				}
				password, err := io.ReadAll(channel)
				if err != nil {
					return 1
				}
				received <- password
				switch mode {
				case "malformed":
					channel.Write([]byte(`{"private":"fixture"}`))
				case "overflow":
					channel.Write(bytes.Repeat([]byte("x"), MaxOutputBytes+1))
				case "failed exit":
					channel.Write(raw)
					channel.Stderr().Write([]byte("private fixture diagnostics"))
					return 1
				case "cancel":
					<-release
				default:
					channel.Write(raw)
				}
				return 0
			})
			credential, err := PasswordCredential("operator", server.password)
			if err != nil {
				t.Fatal(err)
			}
			defer credential.Destroy()
			client, err := server.transport.Connect(context.Background(), server.target, server.key, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			password := bytes.Clone(server.password)
			if mode == "invalid sudo" {
				password = append(password, '\n')
			}
			value, err := client.InspectActiveNetwork(ctx, password)
			close(release)
			if mode == "success" {
				if err != nil || len(value.Interfaces) != 1 {
					t.Fatal("valid observer rejected", err)
				}
			} else if err == nil || len(value.Interfaces) != 0 || strings.Contains(err.Error(), "private fixture diagnostics") {
				t.Fatal("unsafe observer result", err)
			}
			if mode == "success" {
				select {
				case input := <-received:
					if !bytes.Equal(input, append(bytes.Clone(server.password), '\n')) {
						t.Fatal("sudo framing changed")
					}
				case <-ctx.Done():
					t.Fatal("missing sudo input")
				}
			}
			if mode == "invalid sudo" && server.execCalls.Load() != 0 {
				t.Fatal("invalid sudo input executed")
			}
			if mode == "cancel" && err != ErrCancelled {
				t.Fatal("cancellation lost", err)
			}
		})
	}
}

const networkdDescriptionFixture = `{"Interfaces":[{"Name":"ens18","Index":2,"Flags":69699,"AdministrativeState":"configured","OperationalState":"routable","CarrierState":"carrier","AddressState":"routable","KernelOperationalStateString":"up","ActivationPolicy":"up","NetworkFile":"/run/systemd/network/10-netplan-ens18.network","NetworkFileDropins":[],"Addresses":[{"Family":2,"Address":[192,168,3,251],"PrefixLength":24,"Scope":0,"Flags":128,"ConfigSource":"static","ConfigState":"configured"}]}],"private_fixture_diagnostics":"not-for-transport"}`

func TestActiveNetworkObservationScope(t *testing.T) {
	errors := map[string]bool{}
	for _, mode := range []string{"owner changed", "bus changed", "namespace changed", "wrong namespace", "bad namespace type", "daemon changed", "kernel changed", "netplan changed", "runtime changed", "boot changed", "runtime symlink", "runtime writable", "missing backend", "duplicate JSON", "case alias", "malformed JSON", "oversize", "hang", "pipe closed hang", "failed command"} {
		errors[mode] = true
	}
	inconclusive := map[string]bool{}
	for _, mode := range []string{"foreign", "DHCP", "finite lifetime", "finite aliases", "tentative", "wrong index", "wrong file", "dropin", "missing dropins", "manual", "down", "duplicate daemon address", "duplicate kernel address", "kernel DHCP", "wrong prefix", "unconfigured"} {
		inconclusive[mode] = true
	}
	modes := []string{"success", "empty dropins", "other DHCP"}
	for mode := range errors {
		modes = append(modes, mode)
	}
	for mode := range inconclusive {
		modes = append(modes, mode)
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			write := func(name, body string, permissions os.FileMode) {
				name = filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, []byte(body), permissions); err != nil {
					t.Fatal(err)
				}
			}
			write("etc/machine-id", strings.Repeat("a", 32), 0o600)
			write("proc/sys/kernel/random/boot_id", "11111111-1111-4111-8111-111111111111", 0o600)
			write("proc/self/ns/net", "namespace fixture metadata", 0o600)
			write("etc/netplan/10-static.yaml", persistentNetplanFixture, 0o600)
			write("run/systemd/network/10-netplan-ens18.network", "[Match]\nName=ens18\n[Network]\nAddress=192.168.3.251/24\n", 0o600)
			write("daemon.json", networkdDescriptionFixture, 0o600)
			write("kernel.json", strings.Replace(privilegedAddressFixture, `"ifname":`, `"ifindex":2,"ifname":`, 1), 0o600)
			stub := "#!/usr/bin/python3\nROOT=" + strconv.Quote(root) + "\nMODE=" + strconv.Quote(mode) + "\n" + activeNetworkCommandFixture
			write("bin/busctl", stub, 0o700)
			write("bin/ip", stub, 0o700)
			if mode == "runtime symlink" {
				name := filepath.Join(root, "run/systemd/network/10-netplan-ens18.network")
				if os.Remove(name) != nil || os.Symlink("/etc/passwd", name) != nil {
					t.Fatal("fixture symlink")
				}
			}
			if mode == "runtime writable" {
				if err := os.Chmod(filepath.Join(root, "run/systemd/network/10-netplan-ens18.network"), 0o666); err != nil {
					t.Fatal(err)
				}
			}
			script := strings.Replace(activeNetworkScript, `ROOT = "/"`, "ROOT = "+strconv.Quote(root), 1)
			script = strings.Replace(script, "EXPECTED_UID = 0", "EXPECTED_UID = "+strconv.Itoa(os.Getuid()), 1)
			script = strings.ReplaceAll(script, `"/usr/bin/busctl"`, strconv.Quote(filepath.Join(root, "bin/busctl")))
			script = strings.ReplaceAll(script, `"/usr/sbin/ip"`, strconv.Quote(filepath.Join(root, "bin/ip")))
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script)
			command.Env = append(os.Environ(), "DBUS_SYSTEM_BUS_ADDRESS=invalid-inherited-bus", "SYSTEMD_PAGER=invalid-inherited-pager", "HTTP_PROXY=private-fixture-proxy")
			out, err := command.Output()
			if ctx.Err() != nil || (err != nil) != errors[mode] || bytes.Contains(out, []byte("private_fixture")) || bytes.Contains(out, []byte("not-for-transport")) {
				t.Fatalf("observer scope outcome: error=%v deadline=%v public bytes=%d", err, ctx.Err(), len(out))
			}
			if !errors[mode] {
				value, parseErr := parseActiveNetwork(out)
				want := 1
				if inconclusive[mode] {
					want = 0
				}
				if parseErr != nil || len(value.Interfaces) != want {
					t.Fatalf("public ownership projection: %v interfaces=%d want=%d", parseErr, len(value.Interfaces), want)
				}
			} else if len(out) != 0 {
				t.Fatal("failed observation emitted partial evidence")
			}
			if pidBytes, err := os.ReadFile(filepath.Join(root, "child-pid")); err == nil {
				pid, err := strconv.Atoi(string(pidBytes))
				if err != nil || syscall.Kill(pid, 0) != syscall.ESRCH {
					t.Fatal("timed-out observer command was not killed and joined")
				}
			}
		})
	}
}

// Real subprocesses exercise stdout limits, deadlines, exit status and command
// flags. Every path/mutation is inside a private synthetic fixture root.
const activeNetworkCommandFixture = `import copy, json, os, pathlib, sys, time
root = pathlib.Path(ROOT)
os.umask(0o077)
assert not any(key in os.environ for key in ("DBUS_SYSTEM_BUS_ADDRESS", "SYSTEMD_PAGER", "HTTP_PROXY", "PYTHONPATH"))
args = sys.argv[1:]
if pathlib.Path(sys.argv[0]).name == "ip":
    assert args == ["-j", "-4", "address", "show"]
    method = "ip"
else:
    assert args[:7] == ["--system", "--auto-start=no", "--allow-interactive-authorization=no", "--no-pager", "--timeout=2s", "--json=short", "call"]
    destination, path, interface, method = args[7:11]
    if method in ("GetId", "GetNameOwner"):
        assert (destination, path, interface) == ("org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus")
        assert args[11:] == ([] if method == "GetId" else ["s", "org.freedesktop.network1"])
    else:
        assert destination == ":1.42" and path == "/org/freedesktop/network1"
        if method == "Get":
            assert interface == "org.freedesktop.DBus.Properties" and args[11:] == ["ss", "org.freedesktop.network1.Manager", "NamespaceId"]
        else:
            assert method == "Describe" and interface == "org.freedesktop.network1.Manager" and args[11:] == []
counter = root/('count-'+method)
n = int(counter.read_text())+1 if counter.exists() else 1
counter.write_text(str(n))
if method == "GetId":
    value = "c"*32 if MODE == "bus changed" and n > 1 else "b"*32
elif method == "GetNameOwner":
    if MODE == "missing backend":
        sys.exit(1)
    value = ":1.43" if MODE == "owner changed" and n > 1 else ":1.42"
elif method == "Get":
    namespace = (root/'proc/self/ns/net').stat().st_ino
    if MODE == "wrong namespace" or MODE == "namespace changed" and n > 1:
        namespace += 1
    variant = {"type":"t", "data": str(namespace) if MODE == "bad namespace type" else namespace}
    print(json.dumps({"type":"v", "data":[variant]}))
    sys.exit(0)
elif method == "ip":
    value = json.loads((root/'kernel.json').read_text())
    if MODE == "kernel DHCP" or MODE == "kernel changed" and n > 1:
        value[0]['addr_info'][0]['dynamic'] = True
    if MODE == "duplicate kernel address":
        duplicate = copy.deepcopy(value[0]); duplicate['ifname'] = 'ens19'; duplicate['ifindex'] = 3; value.append(duplicate)
    print(json.dumps(value))
    sys.exit(0)
else:
    if MODE in ("hang", "pipe closed hang"):
        (root/'child-pid').write_text(str(os.getpid()))
        if MODE == "pipe closed hang":
            os.close(1)
        time.sleep(30)
    if MODE == "oversize":
        sys.stdout.write('x'*131073)
        sys.exit(0)
    if MODE == "malformed JSON":
        print('private_fixture invalid JSON')
        sys.exit(0)
    value = json.loads((root/'daemon.json').read_text())
    link = value['Interfaces'][0]; address = link['Addresses'][0]
    if MODE == "foreign" or MODE == "daemon changed" and n > 1: address['ConfigSource'] = 'foreign'
    if MODE == "DHCP": address['ConfigSource'] = 'DHCPv4'
    if MODE == "finite lifetime": address['ValidLifetimeUSec'] = 1234
    if MODE == "finite aliases": address.update({'ValidLifetimeUSec':1234,'ValidLifetimeUsec':1234})
    if MODE == "other DHCP":
        other = copy.deepcopy(address); other['Address'] = [192,168,3,253]; other.update({'ConfigSource':'DHCPv4','ValidLifetimeUSec':1234,'ValidLifetimeUsec':1234}); link['Addresses'].append(other)
    if MODE == "case alias": address['configsource'] = 'foreign'
    if MODE == "tentative": address['Flags'] |= 64
    if MODE == "wrong index": link['Index'] = 3
    if MODE == "wrong file": link['NetworkFile'] = '/etc/systemd/network/00-other.network'
    if MODE == "dropin": link['NetworkFileDropins'] = ['/run/systemd/network/10-netplan-ens18.network.d/override.conf']
    if MODE == "missing dropins": del link['NetworkFileDropins']
    if MODE == "empty dropins": link['NetworkFileDropins'] = None
    if MODE == "manual": link['ActivationPolicy'] = 'manual'
    if MODE == "down": link['Flags'] = 0
    if MODE == "unconfigured": address['ConfigState'] = 'requesting'
    if MODE == "wrong prefix": address['PrefixLength'] = 25
    if MODE == "duplicate daemon address":
        duplicate = copy.deepcopy(link); duplicate['Name'] = 'ens19'; duplicate['Index'] = 3; value['Interfaces'].append(duplicate)
    if n > 1:
        changes = {'netplan changed': 'etc/netplan/10-static.yaml', 'runtime changed': 'run/systemd/network/10-netplan-ens18.network', 'boot changed': 'proc/sys/kernel/random/boot_id'}
        if MODE in changes:
            with (root/changes[MODE]).open('a') as changed: changed.write('\n#changed')
    value = json.dumps(value)
    if MODE == "duplicate JSON": value = value.replace('"Interfaces":', '"Interfaces":[],"Interfaces":', 1)
print(json.dumps({"type":"s", "data":[value]}))
if MODE == "failed command" and method == "Describe": sys.exit(1)
`
