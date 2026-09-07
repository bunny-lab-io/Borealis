package clusterremote

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const privilegedAddressFixture = `[{"ifname":"ens18","flags":["UP","LOWER_UP"],"operstate":"UP","addr_info":[{"family":"inet","local":"192.168.3.251","prefixlen":24,"scope":"global","valid_life_time":4294967295,"preferred_life_time":4294967295}]}]`
const privilegedRouteFixture = `[{"dst":"default","gateway":"192.168.3.1","dev":"ens18","protocol":"static","flags":[]},{"dst":"192.168.3.0/24","dev":"ens18","scope":"link","protocol":"kernel","flags":[]}]`
const privilegedHostFixture = "protocol=1\nuid=0\nmachine_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nboot_id=11111111-1111-4111-8111-111111111111\nborealis_path=absent\nborealis_config=absent\nk3s_config=absent\nk3s_data=absent\nk3s_binary=absent\nk3s_load=not-found\nk3s_active=inactive\nk3s_agent_load=not-found\nk3s_agent_active=inactive\naddresses=" + privilegedAddressFixture + "\nroutes=" + privilegedRouteFixture + "\nkube_system_uid=not-present\nnodes=not-present\n"

func TestPinnedSSHPrivilegedInspectionKeepsSudoSecretOffCommand(t *testing.T) {
	for _, mode := range []string{"privileged password", "privileged nopasswd"} {
		t.Run(mode, func(t *testing.T) {
			server := newFakeSSH(t, mode)
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
			password := bytes.Clone(server.password)
			if mode == "privileged nopasswd" {
				password = nil
			}
			facts, err := client.InspectPrivileged(context.Background(), password)
			if err != nil || !facts.NoExistingInstallation() {
				t.Fatalf("privileged facts rejected: %v", err)
			}
			if mode != "privileged nopasswd" && !bytes.Equal(password, server.password) {
				t.Fatal("caller-owned sudo password mutated")
			}
			if network, err := facts.ConnectedManagementNetwork("192.168.3.251", []string{"192.168.3.252", "192.168.3.250", "192.168.3.248"}); err != nil || network.String() != "192.168.3.0/24" {
				t.Fatalf("direct peer network: %v %v", network, err)
			}
		})
	}
}

func TestPrivilegedSSHRejectsSecretsFailuresAndCancellation(t *testing.T) {
	for _, mode := range []string{"privileged stdout overflow", "privileged stderr overflow", "privileged secret error", "privileged malformed", "privileged hang", "privileged invalid password"} {
		t.Run(mode, func(t *testing.T) {
			server := newFakeSSH(t, mode)
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
			if mode == "privileged invalid password" {
				password = append(bytes.Clone(password), '\n')
			}
			facts, err := client.InspectPrivileged(ctx, password)
			if err == nil || facts.NoExistingInstallation() || strings.Contains(err.Error(), string(server.password)) {
				t.Fatalf("unsafe failure: %v", err)
			}
			if mode == "privileged hang" && !errors.Is(err, ErrCancelled) {
				t.Fatal("cancelled SSH did not close")
			}
			if mode == "privileged invalid password" && server.execCalls.Load() != 0 {
				t.Fatal("line-bearing sudo password reached command")
			}
		})
	}
}

func TestPrivilegedInventoryDoesNotConvertUnknownOrExistingStateToClean(t *testing.T) {
	for _, key := range []string{"borealis_path", "borealis_config", "k3s_config", "k3s_data", "k3s_binary"} {
		for _, state := range []string{"unknown", "directory", "regular", "symlink", "other"} {
			t.Run(key+"/"+state, func(t *testing.T) {
				facts, err := parsePrivilegedFacts([]byte(strings.Replace(privilegedHostFixture, key+"=absent", key+"="+state, 1)))
				if err != nil || facts.NoExistingInstallation() {
					t.Fatalf("unsafe existing path state: %v", err)
				}
			})
		}
	}
	for _, pair := range [][2]string{{"machine_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "machine_id=unknown"}, {"boot_id=11111111-1111-4111-8111-111111111111", "boot_id=unknown"}, {"k3s_load=not-found", "k3s_load=unknown"}, {"k3s_agent_load=not-found", "k3s_agent_load=loaded"}, {"k3s_active=inactive", "k3s_active=failed"}, {"nodes=not-present", "nodes=unknown"}, {"kube_system_uid=not-present", "kube_system_uid=unknown"}} {
		facts, err := parsePrivilegedFacts([]byte(strings.Replace(privilegedHostFixture, pair[0], pair[1], 1)))
		if err != nil || facts.NoExistingInstallation() {
			t.Fatalf("unknown state treated as clean: %v", err)
		}
	}
	existing := strings.Replace(privilegedHostFixture, "kube_system_uid=not-present", "kube_system_uid=22222222-2222-4222-8222-222222222222", 1)
	existing = strings.Replace(existing, "nodes=not-present", "nodes=engine-02,33333333-3333-4333-8333-333333333333;engine-03,44444444-4444-4444-8444-444444444444;", 1)
	facts, err := parsePrivilegedFacts([]byte(existing))
	if err != nil || facts.NoExistingInstallation() || len(facts.Nodes) != 2 || facts.NodesState != "observed" {
		t.Fatalf("existing cluster identity lost: %v", err)
	}
	for _, changed := range []string{
		privilegedHostFixture + "protocol=1\n",
		strings.Replace(privilegedHostFixture, "uid=0\n", "uid=1000\n", 1),
		strings.Replace(privilegedHostFixture, "protocol=1\n", "protocol=2\n", 1),
		strings.Replace(privilegedHostFixture, "k3s_binary=absent\n", "", 1),
		strings.Replace(privilegedHostFixture, "machine_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "machine_id=private-bad-data", 1),
		strings.Replace(privilegedHostFixture, "nodes=not-present", "nodes=engine,33333333-3333-4333-8333-333333333333;engine,44444444-4444-4444-8444-444444444444;", 1),
	} {
		if _, err := parsePrivilegedFacts([]byte(changed)); err == nil {
			t.Fatal("invalid inventory accepted")
		}
	}
}

func TestPrivilegedNetworkRequiresPermanentUniqueDirectAddress(t *testing.T) {
	for name, replace := range map[string][2]string{
		"DHCP":            {`"scope":"global"`, `"scope":"global","dynamic":true`},
		"expires":         {`"valid_life_time":4294967295`, `"valid_life_time":3600`},
		"deprecated":      {`"scope":"global"`, `"scope":"global","deprecated":true`},
		"no carrier":      {`"UP","LOWER_UP"`, `"UP"`},
		"interface down":  {`"operstate":"UP"`, `"operstate":"DOWN"`},
		"host route":      {`"prefixlen":24`, `"prefixlen":32`},
		"routed peers":    {`"scope":"link"`, `"scope":"link","gateway":"192.168.3.1"`},
		"link down":       {`"protocol":"kernel","flags":[]`, `"protocol":"kernel","flags":["linkdown"]`},
		"blackhole":       {`"scope":"link"`, `"scope":"link","type":"blackhole"`},
		"manual route":    {`"protocol":"kernel"`, `"protocol":"static"`},
		"wrong interface": {`"scope":"link"`, `"scope":"link"`},
	} {
		t.Run(name, func(t *testing.T) {
			raw := strings.Replace(privilegedHostFixture, replace[0], replace[1], 1)
			facts, err := parsePrivilegedFacts([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			if name == "wrong interface" {
				facts.Routes[1].Interface = "other"
			}
			if _, err := facts.ConnectedManagementNetwork("192.168.3.251", []string{"192.168.3.250"}); err == nil {
				t.Fatal("unsafe observed network accepted")
			}
		})
	}
	facts, _ := parsePrivilegedFacts([]byte(privilegedHostFixture))
	for _, peer := range []string{"192.168.4.1", "8.8.8.8", "::1", "invalid"} {
		if _, err := facts.ConnectedManagementNetwork("192.168.3.251", []string{peer}); err == nil {
			t.Fatal("unconnected peer accepted")
		}
	}
	facts.Addresses = append(facts.Addresses, facts.Addresses[0])
	if _, err := facts.ConnectedManagementNetwork("192.168.3.251", nil); err == nil {
		t.Fatal("ambiguous management address accepted")
	}
	for _, raw := range []string{
		`null`, privilegedAddressFixture + `{}`,
		strings.Replace(privilegedAddressFixture, `"prefixlen":24`, `"prefixlen":null`, 1),
		strings.Replace(privilegedAddressFixture, `"prefixlen":24`, `"prefixlen":24,"prefixlen":24`, 1),
		strings.Replace(privilegedAddressFixture, `"prefixlen":24`, `"Prefixlen":24`, 1),
		strings.Replace(privilegedAddressFixture, `"prefixlen":24`, `"prefixlen":33`, 1),
		strings.Replace(privilegedAddressFixture, `"valid_life_time":4294967295,`, ``, 1),
	} {
		if _, _, err := parseIPv4Inventory([]byte(raw), []byte(privilegedRouteFixture)); err == nil {
			t.Fatal("ambiguous network inventory accepted")
		}
	}
}

func TestPrivilegedShellStdinNeverBecomesCommands(t *testing.T) {
	for _, mode := range []string{"password", "nopasswd", "root"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "password-must-not-execute")
			write := func(name, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write("id", "#!/bin/sh\nif [ \"${FIXTURE_ROOT:-}\" = 1 ]; then printf 0; else printf 1000; fi\n")
			write("sudo", `#!/bin/sh
test "$1" = -k && test "$2" = -S && test "$3" = -p && test "$4" = '' && test "$5" = -- || exit 2
shift 5
if [ "$FIXTURE_MODE" = password ]; then IFS= read -r supplied || exit 3; fi
export FIXTURE_ROOT=1
exec "$@"
`)
			// Compile-time fixture script replaces inventory only. The real
			// wrapper's nested shell quoting, sudo args and stdin remain intact.
			command := buildPrivilegedInspectionCommand(root+":/usr/bin:/bin", `test "$(id -u)" = 0; printf 'inspection=ok\n'`)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c := exec.CommandContext(ctx, "/bin/sh", "-c", command)
			c.Env = append(os.Environ(), "FIXTURE_MODE="+mode)
			if mode == "root" {
				c.Env = append(c.Env, "FIXTURE_ROOT=1")
			}
			c.Stdin = strings.NewReader("touch " + shellConstant(marker) + "; $(touch " + shellConstant(marker) + ")\n")
			out, err := c.CombinedOutput()
			if err != nil || string(out) != "inspection=ok\n" {
				t.Fatalf("fixed wrapper failed: %v %q", err, out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("sudo input executed as shell source")
			}
		})
	}
	if out, err := exec.Command("/bin/sh", "-n", "-c", privilegedInspectionCommand).CombinedOutput(); err != nil {
		t.Fatalf("production script syntax: %v %s", err, out)
	}
}

func TestPrivilegedDirectoryProbePreservesAbsenceAndUnexpectedTypes(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]string{"missing": "absent", "missing/child": "absent", "directory": "directory", "directory/missing": "absent", "file": "regular", "file/child": "other", "link": "symlink", "link/child": "symlink"} {
		// Only fixture root changes. Real find/metadata branching runs intact.
		script := strings.Replace(inspectPathScript, "probe=/\n", "probe="+shellConstant(root)+"\n", 1) + `inspect_path "$1"`
		out, err := exec.Command("/bin/sh", "-c", script, "probe-fixture", name).CombinedOutput()
		if err != nil || string(out) != expected {
			t.Fatalf("path %s: %q %v", name, out, err)
		}
	}
}
