package clusterremote

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

const persistentNetworkFixture = `{"version":1,"machine_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","boot_id":"11111111-1111-4111-8111-111111111111","interfaces":[{"name":"ens18","addresses":["192.168.3.251/24"]}]}`
const persistentNetplanFixture = "network:\n  version: 2\n  ethernets:\n    ens18:\n      addresses: [192.168.3.251/24]\n"

func TestPersistentNetworkBindsCurrentHostAndAddress(t *testing.T) {
	value, err := parsePersistentNetwork([]byte(persistentNetworkFixture + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	facts, _ := parsePrivilegedFacts([]byte(privilegedHostFixture))
	if prefix, err := value.ManagementDeclaration(facts, "192.168.3.251", []string{"192.168.3.250"}); err != nil || prefix.String() != "192.168.3.0/24" {
		t.Fatal("matching persistent/current evidence rejected")
	}
	for name, change := range map[string]func(*PrivilegedFacts){
		"machine changed": func(f *PrivilegedFacts) { f.MachineID = strings.Repeat("b", 32) },
		"boot changed":    func(f *PrivilegedFacts) { f.BootID = "22222222-2222-4222-8222-222222222222" },
		"interface changed": func(f *PrivilegedFacts) {
			f.Addresses[0].Interface = "ens19"
			f.Routes[1].Interface = "ens19"
		},
		"DHCP": func(f *PrivilegedFacts) { f.Addresses[0].Dynamic = true },
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := parsePrivilegedFacts([]byte(privilegedHostFixture))
			change(&f)
			if _, err := value.ManagementDeclaration(f, "192.168.3.251", nil); err == nil {
				t.Fatal("changed host/current network accepted")
			}
		})
	}
	value.Interfaces[0].Addresses[0] = "192.168.3.251/25"
	if _, err := value.ManagementDeclaration(facts, "192.168.3.251", nil); err == nil {
		t.Fatal("different declared prefix accepted")
	}
	for _, bad := range []string{
		strings.Replace(persistentNetworkFixture, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(persistentNetworkFixture, `"version":1`, `"Version":1`, 1),
		strings.Replace(persistentNetworkFixture, `"version":1`, `"version":1,"password":"private-fixture"`, 1),
		strings.Replace(persistentNetworkFixture, `"version":1,`, "", 1),
		strings.Replace(persistentNetworkFixture, `"192.168.3.251/24"`, `"192.168.3.251/24","192.168.3.251/25"`, 1),
		strings.Replace(persistentNetworkFixture, "192.168.3.251/24", "192.168.3.255/24", 1),
		strings.Replace(persistentNetworkFixture, "192.168.3.251/24", "8.8.8.8/24", 1),
		strings.Replace(persistentNetworkFixture, "192.168.3.251/24", "192.168.3.251/32", 1),
		strings.Replace(persistentNetworkFixture, "ens18", "private-fixture-too-long", 1),
		persistentNetworkFixture + persistentNetworkFixture,
	} {
		if _, err := parsePersistentNetwork([]byte(bad)); err != ErrPersistentNetwork {
			t.Fatal("ambiguous or unsafe public projection accepted")
		}
	}
}

func TestPersistentNetworkPinnedSSHBoundaries(t *testing.T) {
	for _, mode := range []string{"password", "nopasswd", "malformed", "stdout overflow", "stderr overflow", "failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			server := newFakeSSH(t, "persistent network", func(command string, channel ssh.Channel) uint32 {
				if command != persistentNetworkCommand {
					return 1
				}
				if mode != "nopasswd" {
					input, err := io.ReadAll(channel)
					if err != nil || len(input) == 0 || input[len(input)-1] != '\n' {
						return 1
					}
				}
				switch mode {
				case "malformed":
					channel.Write([]byte(`{"private":"withheld"}`))
				case "stdout overflow":
					channel.Write(bytes.Repeat([]byte("x"), MaxOutputBytes+1))
				case "stderr overflow":
					channel.Stderr().Write(bytes.Repeat([]byte("x"), MaxOutputBytes+1))
				case "failure":
					channel.Stderr().Write([]byte("private network YAML"))
					return 1
				case "cancel":
					// Waiting for transport shutdown keeps fixture joined.
					_, _ = io.Copy(io.Discard, channel.Stderr())
				default:
					channel.Write([]byte(persistentNetworkFixture))
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
			password := server.password
			if mode == "nopasswd" {
				password = nil
			}
			value, err := client.InspectPersistentNetwork(ctx, password)
			if mode == "password" || mode == "nopasswd" {
				if err != nil || len(value.Interfaces) != 1 {
					t.Fatal("valid pinned observer rejected", err)
				}
			} else if err == nil || len(value.Interfaces) != 0 || strings.Contains(err.Error(), "private network YAML") {
				t.Fatal("unsafe observer result", err)
			}
		})
	}
}

func TestPersistentNetworkNetplanSnapshots(t *testing.T) {
	// Actual Ubuntu library, isolated fixture roots only. Missing bindings are
	// a test prerequisite failure; CI installs python3-netplan/python3-yaml.
	if err := exec.Command("/usr/bin/python3", "-I", "-B", "-c", "import netplan, yaml").Run(); err != nil {
		t.Fatal("Netplan fixtures require Ubuntu python3-netplan and python3-yaml:", err)
	}
	for _, mode := range []string{"static", "etc shadows lib", "lexical merge", "DHCP override", "runtime override", "symlink", "directory symlink", "FIFO", "hardlink", "writable", "oversized", "too many files", "changed snapshot", "changed boot", "malformed", "manual activation", "match", "empty match", "competing match", "merged usr", "bridge member", "NetworkManager", "excluded secret"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			write := func(path, value string) {
				path = filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("etc/machine-id", strings.Repeat("a", 32))
			write("proc/sys/kernel/random/boot_id", "11111111-1111-4111-8111-111111111111")
			write("etc/netplan/10-static.yaml", persistentNetplanFixture)
			prelude, want, success := "", 1, true
			switch mode {
			case "merged usr":
				write("usr/lib/netplan/05-vendor.yaml", "network: {version: 2}\n")
				prelude = "os.symlink('usr/lib', ROOT+'/lib')\n"
			case "competing match":
				want = 0
				write("etc/netplan/20-match.yaml", "network:\n  ethernets:\n    other:\n      match: {name: 'ens*'}\n      dhcp4: true\n")
			case "etc shadows lib":
				write("lib/netplan/10-static.yaml", "not valid: [")
			case "lexical merge":
				write("lib/netplan/20-static.yaml", "network:\n  ethernets:\n    ens18:\n      addresses: [192.168.3.252/24]\n")
			case "DHCP override":
				write("lib/netplan/20-dhcp.yaml", "network:\n  ethernets:\n    ens18:\n      dhcp4: true\n")
				want = 0
			case "runtime override":
				write("run/netplan/99-override.yaml", "")
				success = false
			case "symlink", "FIFO", "hardlink":
				success = false
				prelude = "os.unlink(ROOT+'/etc/netplan/10-static.yaml')\n"
				if mode == "symlink" {
					prelude += "os.symlink('/etc/passwd', ROOT+'/etc/netplan/10-static.yaml')\n"
				} else if mode == "FIFO" {
					prelude += "os.mkfifo(ROOT+'/etc/netplan/10-static.yaml', 0o600)\n"
				} else {
					prelude += "os.link(ROOT+'/etc/machine-id', ROOT+'/etc/netplan/10-static.yaml')\n"
				}
			case "directory symlink":
				success = false
				prelude = "os.rename(ROOT+'/etc/netplan', ROOT+'/saved')\nos.symlink('../saved', ROOT+'/etc/netplan')\n"
			case "writable":
				success = false
				prelude = "os.chmod(ROOT+'/etc/netplan/10-static.yaml', 0o666)\n"
			case "oversized":
				success = false
				write("etc/netplan/10-static.yaml", strings.Repeat("x", 65537))
			case "too many files":
				success = false
				for i := 0; i < 33; i++ {
					write("etc/netplan/extra"+strconv.Itoa(i)+".yaml", "network: {version: 2}")
				}
			case "changed snapshot", "changed boot":
				success = false
				path := "/etc/netplan/10-static.yaml"
				if mode == "changed boot" {
					path = "/proc/sys/kernel/random/boot_id"
				}
				prelude = "original_project = project\ndef changed_project(state):\n    result = original_project(state)\n    with open(ROOT+" + strconv.Quote(path) + ", 'a') as changed:\n        changed.write('changed')\n    return result\nproject = changed_project\n"
			case "malformed":
				success = false
				write("etc/netplan/10-static.yaml", "network: [private-fixture-secret")
			case "manual activation", "match", "empty match", "NetworkManager":
				want = 0
				extra := map[string]string{"manual activation": "activation-mode: manual", "match": "match: {name: ens18}", "empty match": "match: {}", "NetworkManager": "renderer: NetworkManager"}[mode]
				write("etc/netplan/10-static.yaml", persistentNetplanFixture+"      "+extra+"\n")
			case "bridge member":
				want = 0
				write("etc/netplan/10-static.yaml", persistentNetplanFixture+"  bridges:\n    br0:\n      interfaces: [ens18]\n")
			case "excluded secret":
				write("etc/netplan/10-static.yaml", persistentNetplanFixture+"  wifis:\n    wlan0:\n      dhcp4: true\n      access-points:\n        private-fixture-ssid:\n          password: private-fixture-secret\n")
			}
			script := strings.Replace(persistentNetworkScript, `ROOT = "/"`, "ROOT = "+strconv.Quote(root), 1)
			script = strings.Replace(script, "EXPECTED_UID = 0", "EXPECTED_UID = "+strconv.Itoa(os.Getuid()), 1)
			script = strings.Replace(script, `if __name__ == "__main__":`, "if False:", 1) + "\n" + prelude + "\nmain()\n"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script)
			// Production suppresses parser diagnostics at the fixed shell boundary.
			out, err := cmd.Output()
			if ctx.Err() != nil || (err == nil) != success || strings.Contains(string(out), "private-fixture") {
				t.Fatalf("observer outcome mismatch: %v, deadline=%v, public bytes=%d", err, ctx.Err(), len(out))
			}
			if success {
				value, err := parsePersistentNetwork(out)
				if err != nil || len(value.Interfaces) != want {
					t.Fatalf("projection mismatch: %v, interfaces=%d", err, len(value.Interfaces))
				}
				if mode == "lexical merge" && len(value.Interfaces[0].Addresses) != 2 {
					t.Fatal("upstream sequence merge lost")
				}
			} else if len(out) != 0 {
				t.Fatal("failed observer emitted partial evidence")
			}
			if _, err := os.Stat(filepath.Join(root, "run/systemd/network")); !os.IsNotExist(err) {
				t.Fatal("observer generated backend configuration")
			}
		})
	}
}

func TestPersistentNetworkCommandSyntax(t *testing.T) {
	if out, err := exec.Command("/bin/sh", "-n", "-c", persistentNetworkCommand).CombinedOutput(); err != nil {
		t.Fatalf("fixed command syntax: %v %s", err, out)
	}
}
