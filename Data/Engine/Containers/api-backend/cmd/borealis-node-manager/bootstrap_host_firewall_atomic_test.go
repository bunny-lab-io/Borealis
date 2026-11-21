package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Execute the actual rendered script against isolated command stubs. Kernel
// transactions are separately qualified in disposable network namespaces.
func TestBootstrapFirewallAtomicRendering(t *testing.T) {
	for _, mode := range []string{"valid", "restore failed", "CIDR injection", "peer injection", "port injection"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			log := filepath.Join(root, "calls")
			stream := filepath.Join(root, "stream")
			write := func(name, raw string) {
				if err := os.WriteFile(filepath.Join(root, name), []byte(raw), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write("firewall.sh", bootstrapPrejoinFirewall)
			write("iptables-restore", `#!/bin/sh
set -eu
[ "$*" = '--noflush --wait 5' ] || exit 90
printf 'restore\n' >> "$TEST_CALLS"
/bin/cat > "$TEST_STREAM"
[ "$TEST_FAIL" = 0 ] || exit 1
`)
			write("iptables", `#!/bin/sh
set -eu
case "$1" in
 -C) exit 1 ;;
 -I) printf '%s\n' "$*" >> "$TEST_CALLS" ;;
 *) exit 91 ;;
esac
`)
			env := []string{"PATH=" + root, "LC_ALL=C", "TEST_CALLS=" + log, "TEST_STREAM=" + stream, "TEST_FAIL=0", "BOREALIS_K3S_PEER_CIDRS=192.168.3.250/32,192.168.3.251/32,192.168.3.252/32"}
			switch mode {
			case "restore failed":
				env[4] = "TEST_FAIL=1"
			case "CIDR injection":
				env = append(env, "BOREALIS_K3S_CLUSTER_CIDR=10.42.0.0/16\nCOMMIT\n*filter")
			case "peer injection":
				env[5] = "BOREALIS_K3S_PEER_CIDRS=192.168.3.250/32 -j ACCEPT"
			case "port injection":
				env = append(env, "BOREALIS_K3S_API_PORT=6443\n-A INPUT -j ACCEPT")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/bin/sh", filepath.Join(root, "firewall.sh"))
			command.Env = env
			if err := command.Run(); (err == nil) != (mode == "valid") {
				t.Fatal("unexpected script result", err)
			}
			calls, readErr := os.ReadFile(log)
			if strings.Contains(mode, "injection") {
				if !os.IsNotExist(readErr) {
					t.Fatal("injected stream reached native command")
				}
				return
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "restore failed" && string(calls) != "restore\n" {
				t.Fatal("jump added after failed restore", string(calls))
			}
			if mode == "valid" && (strings.Count(string(calls), "\n") != 8 || !strings.HasPrefix(string(calls), "restore\n")) {
				t.Fatal("non-atomic command ordering", string(calls))
			}
			raw, err := os.ReadFile(stream)
			if err != nil {
				t.Fatal(err)
			}
			want := bootstrapHostFirewallChainRules(bootstrapHostFilesFixture())
			want[0] = ":BOREALIS-K3S-API - [0:0]"
			for i, line := range want {
				line = strings.Replace(line, "RELATED,ESTABLISHED", "ESTABLISHED,RELATED", 1)
				for _, cidr := range []string{"10.42.0.0/16", "10.43.0.0/16"} {
					for _, iface := range []string{"cni+", "flannel+"} {
						line = strings.Replace(line, "-s "+cidr+" -i "+iface, "-i "+iface+" -s "+cidr, 1)
					}
				}
				want[i] = line
			}
			if string(raw) != "*filter\n"+strings.Join(want, "\n")+"\nCOMMIT\n" {
				t.Fatal("transaction differs from exact firewall contract")
			}
		})
	}
}
