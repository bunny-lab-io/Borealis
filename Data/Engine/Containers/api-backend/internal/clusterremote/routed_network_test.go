package clusterremote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

	"golang.org/x/crypto/ssh"
)

const routingStateFixture = `[[{"priority":0,"src":"all","table":"255","protocol":"2"},{"priority":32766,"src":"all","table":"254","protocol":"2"},{"priority":32767,"src":"all","table":"253","protocol":"2"}],[{"type":"1","dst":"default","gateway":"192.168.3.1","dev":"ens18","table":"254","protocol":"4","scope":"0","flags":[]},{"type":"1","dst":"192.168.3.0/24","dev":"ens18","table":"254","protocol":"2","scope":"253","prefsrc":"192.168.3.251","flags":[]},{"type":"2","dst":"192.168.3.251","dev":"ens18","table":"255","protocol":"2","scope":"254","prefsrc":"192.168.3.251","flags":[]},{"type":"3","dst":"192.168.3.255","dev":"ens18","table":"255","protocol":"2","scope":"253","prefsrc":"192.168.3.251","flags":[]}]]`

func routedNetworkFixture(t *testing.T) RoutedNetworkOwnership {
	t.Helper()
	return RoutedNetworkOwnership{Version: 1, Targets: RouteTargets{Management: "192.168.3.251", Peers: []string{"192.168.3.250", "192.168.3.252"}}, Resolved: []string{"192.168.3.250", "192.168.3.252"}, Active: activeNetworkFixture(t), Networks: []DirectNetwork{{
		Interface: "ens18", Index: 2, Address: "192.168.3.251/24", Excluded: []string{"192.168.3.251/32", "192.168.3.255/32"},
	}}}
}

func routedPrivilegedFixture(t *testing.T) PrivilegedFacts {
	t.Helper()
	facts, err := parsePrivilegedFacts([]byte(strings.Replace(privilegedHostFixture, `"ifname":`, `"ifindex":2,"ifname":`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestRoutedNetworkRequiresEveryPeer(t *testing.T) {
	value := routedNetworkFixture(t)
	facts := routedPrivilegedFixture(t)
	if prefix, err := value.ManagementRoutes(facts, "192.168.3.251", []string{"192.168.3.250", "192.168.3.252"}); err != nil || prefix.String() != "192.168.3.0/24" {
		t.Fatal("valid direct peer route rejected", err)
	}
	for _, peers := range [][]string{nil, {"192.168.3.251"}, {"192.168.3.250", "192.168.3.250"}, {"192.168.3.250", "192.168.3.255"}, {"192.168.4.250"}, {"invalid"}, {"192.168.3.0"}} {
		if _, err := value.ManagementRoutes(facts, "192.168.3.251", peers); err != ErrRoutedNetwork {
			t.Fatal("invalid peer cohort accepted", peers)
		}
	}
	value.Targets.Peers, value.Resolved = []string{"192.168.3.160", "192.168.3.250"}, []string{"192.168.3.160", "192.168.3.250"}
	value.Networks[0].Excluded = []string{"192.168.3.128/26", "192.168.3.251/32", "192.168.3.255/32"}
	if _, err := value.ManagementRoutes(facts, "192.168.3.251", []string{"192.168.3.250", "192.168.3.160"}); err != ErrRoutedNetwork {
		t.Fatal("one routed-away peer bypassed complete-group check")
	}
	value = routedNetworkFixture(t)
	facts.Addresses[0].Index = 3
	if _, err := value.ManagementRoutes(facts, "192.168.3.251", []string{"192.168.3.250", "192.168.3.252"}); err != ErrRoutedNetwork {
		t.Fatal("replaced current interface accepted")
	}
	for name, edit := range map[string]func(*RoutedNetworkOwnership){
		"version":          func(v *RoutedNetworkOwnership) { v.Version = 2 },
		"null resolved":    func(v *RoutedNetworkOwnership) { v.Resolved = nil },
		"unasked resolved": func(v *RoutedNetworkOwnership) { v.Resolved = []string{"192.168.3.253"} },
		"repeat resolved":  func(v *RoutedNetworkOwnership) { v.Resolved = []string{"192.168.3.250", "192.168.3.250"} },
		"invalid targets":  func(v *RoutedNetworkOwnership) { v.Targets.Management = "127.0.0.1" },
		"active identity":  func(v *RoutedNetworkOwnership) { v.Active.Owner = "invalid" },
		"null networks":    func(v *RoutedNetworkOwnership) { v.Networks = nil },
		"wrong interface":  func(v *RoutedNetworkOwnership) { v.Networks[0].Interface = "ens19" },
		"wrong index":      func(v *RoutedNetworkOwnership) { v.Networks[0].Index = 0 },
		"wrong address":    func(v *RoutedNetworkOwnership) { v.Networks[0].Address = "192.168.3.252/24" },
		"null exclusions":  func(v *RoutedNetworkOwnership) { v.Networks[0].Excluded = nil },
		"empty exclusions": func(v *RoutedNetworkOwnership) { v.Networks[0].Excluded = []string{} },
		"missing local":    func(v *RoutedNetworkOwnership) { v.Networks[0].Excluded = []string{"192.168.3.255/32"} },
		"outside subnet":   func(v *RoutedNetworkOwnership) { v.Networks[0].Excluded = []string{"192.168.4.0/24"} },
		"unmasked":         func(v *RoutedNetworkOwnership) { v.Networks[0].Excluded = []string{"192.168.3.251/25"} },
		"whole subnet":     func(v *RoutedNetworkOwnership) { v.Networks[0].Excluded = []string{"192.168.3.0/24"} },
		"overlap": func(v *RoutedNetworkOwnership) {
			v.Networks[0].Excluded = []string{"192.168.3.128/25", "192.168.3.251/32"}
		},
		"duplicate": func(v *RoutedNetworkOwnership) { v.Networks = append(v.Networks, v.Networks[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			v := routedNetworkFixture(t)
			edit(&v)
			raw, _ := json.Marshal(v)
			if _, err := parseRoutedNetwork(raw); err != ErrRoutedNetwork {
				t.Fatal("invalid direct route projection accepted")
			}
		})
	}
	raw, _ := json.Marshal(routedNetworkFixture(t))
	for _, malformed := range []string{
		strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(raw), `"networks":`, `"Networks":`, 1),
		strings.Replace(string(raw), `"networks":`, `"private":"hidden","networks":`, 1),
		string(raw) + string(raw),
	} {
		if _, err := parseRoutedNetwork([]byte(malformed)); err != ErrRoutedNetwork {
			t.Fatal("ambiguous public routing evidence accepted")
		}
	}
	// Literal interface names can share a prefix. Ordering must stay by name,
	// then address, rather than by a separator-concatenated artificial key.
	v := routedNetworkFixture(t)
	v.Active.Declarations.Interfaces = append(v.Active.Declarations.Interfaces, PersistentNetworkInterface{Name: "ens18.1", Addresses: []string{"192.168.4.251/24"}})
	v.Active.Interfaces = append(v.Active.Interfaces, ActiveNetworkInterface{Name: "ens18.1", Index: 3, Addresses: []string{"192.168.4.251/24"}})
	v.Networks = append(v.Networks, DirectNetwork{Interface: "ens18.1", Index: 3, Address: "192.168.4.251/24", Excluded: []string{"192.168.4.251/32"}})
	if v.validate() != nil {
		t.Fatal("ordered multiple interface evidence rejected")
	}
	v = routedNetworkFixture(t)
	v.Resolved = []string{"192.168.3.250"}
	if _, err := v.ManagementRoutes(routedPrivilegedFixture(t), "192.168.3.251", v.Targets.Peers); err != ErrRoutedNetwork {
		t.Fatal("missing kernel lookup accepted")
	}
}

func TestRoutedNetworkTargetDataBoundaries(t *testing.T) {
	for _, targets := range []RouteTargets{
		{Management: "192.168.3.251", Peers: nil},
		{Management: "192.168.3.251", Peers: []string{"192.168.3.250", "192.168.3.250"}},
		{Management: "192.168.3.251", Peers: []string{"192.168.3.251"}},
		{Management: "192.168.3.251;touch /tmp/invalid", Peers: []string{"192.168.3.250"}},
		{Management: "192.168.3.251", Peers: []string{"$(id)"}},
		{Management: "192.168.3.251", Peers: []string{"192.168.03.250"}},
		{Management: "192.168.3.251", Peers: []string{"192.168.3.250/24"}},
		{Management: "192.168.3.251", Peers: []string{"8.8.8.8"}},
		{Management: "::ffff:192.168.3.251", Peers: []string{"192.168.3.250"}},
		{Management: "192.168.3.251", Peers: make([]string, 33)},
	} {
		// A nil client proves rejection happens before any SSH session access.
		if _, err := (*Client)(nil).InspectRoutedNetwork(context.Background(), nil, targets); err != ErrRoutedNetwork {
			t.Fatal("invalid lookup input crossed transport boundary", err)
		}
	}
	valid, _ := json.Marshal(routedNetworkFixture(t).Targets)
	for _, raw := range [][]byte{
		valid,
		[]byte(`{"management":"192.168.3.251","peers":["192.168.3.250"],"path":"/tmp/private"}`),
		[]byte(`{"management":"192.168.3.251","peers":["192.168.3.252","192.168.3.250"]}`),
		[]byte(`{"management":"192.168.3.251","peers":["192.168.3.250"],"peers":[]}`),
		[]byte(`{"management":"192.168.3.251","peers":["--help"]}`),
		[]byte(`{"management":"192.168.3.251","peers":["127.0.0.1"]}`),
		[]byte(`{"management":"192.168.3.251","peers":[null]}`),
		bytes.Repeat([]byte("x"), 1025),
	} {
		script := persistentNetworkLibraryScript + activeNetworkLibraryScript + routedNetworkLibraryScript + "\nmain(route_targets)\n"
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		encoded := base64.StdEncoding.EncodeToString(raw)
		out, err := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script, encoded).Output()
		cancel()
		if bytes.Equal(raw, valid) {
			if err != nil || !bytes.Equal(bytes.TrimSpace(out), valid) {
				t.Fatal("valid public target argument rejected", err)
			}
		} else if err == nil || len(out) != 0 {
			t.Fatal("remote input validation accepted invalid target data")
		}
	}
}

func TestRoutedNetworkShellDataArgument(t *testing.T) {
	targets := routedNetworkFixture(t).Targets
	expected, _ := json.Marshal(targets)
	for _, mode := range []string{"password", "nopasswd", "root"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "stdin-must-not-execute")
			for name, body := range map[string]string{
				"id": "#!/bin/sh\nif [ \"${FIXTURE_ROOT:-}\" = 1 ]; then printf 0; else printf 1000; fi\n",
				"sudo": `#!/bin/sh
test "$1" = -k && test "$2" = -S && test "$3" = -p && test "$4" = '' && test "$5" = -- || exit 2
shift 5
if [ "$FIXTURE_MODE" = password ]; then IFS= read -r supplied || exit 3; fi
export FIXTURE_ROOT=1
exec "$@"
`,
			} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			command, err := routedNetworkCommand(targets)
			if err != nil {
				t.Fatal(err)
			}
			// Exercise the production command's complete nested quoting/data
			// argument. Replace only fixture tool search and final observation;
			// the real Python target parser runs without touching host state.
			command = strings.ReplaceAll(command, "/usr/sbin:/usr/bin:/sbin:/bin", root+":/usr/bin:/bin")
			command = strings.ReplaceAll(command, "main(observe_routing)", "main(route_targets)")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, "/bin/sh", "-c", command)
			process.Env = append(os.Environ(), "FIXTURE_MODE="+mode)
			if mode == "root" {
				process.Env = append(process.Env, "FIXTURE_ROOT=1")
			}
			process.Stdin = strings.NewReader("touch " + shellConstant(marker) + "; $(touch " + shellConstant(marker) + ")\n")
			raw, err := process.Output()
			if err != nil || !bytes.Equal(bytes.TrimSpace(raw), expected) {
				t.Fatal("public argv or sudo stdin framing failed", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("sudo stdin became executable source")
			}
		})
	}
}

func TestRoutedNetworkPolicyProjection(t *testing.T) {
	active, _ := json.Marshal(activeNetworkFixture(t))
	targets, _ := json.Marshal(routedNetworkFixture(t).Targets)
	for _, tc := range []struct {
		name, edit string
		matched    bool
		excluded   []string
	}{
		{name: "default rules", matched: true},
		{name: "connected metric", edit: `routes[1]["metric"] = 100`, matched: true},
		{name: "ignored unused table", edit: `routes.append(dict(routes[1], table="100", private_fixture="not-for-transport"))`, matched: true},
		{name: "irrelevant main route", edit: `routes.append(dict(routes[1], dst="10.0.0.0/24", dev="other"))`, matched: true},
		{name: "gateway host route", edit: `routes.append(dict(routes[0], dst="192.168.3.250"))`, matched: true, excluded: []string{"192.168.3.250/31", "192.168.3.255/32"}},
		{name: "local VIP", edit: `routes.append(dict(routes[2], dst="192.168.3.250"))`, matched: true, excluded: []string{"192.168.3.250/31", "192.168.3.255/32"}},
		{name: "more specific blackhole", edit: `routes.append({"type":"6", "dst":"192.168.3.128/26", "table":"254"})`, matched: true, excluded: []string{"192.168.3.128/26", "192.168.3.251/32", "192.168.3.255/32"}},
		{name: "more specific multipath", edit: `routes.append(dict(routes[1], dst="192.168.3.128/26", nexthops=[{"gateway":"192.168.3.1","dev":"other","weight":1,"flags":[]}]))`, matched: true, excluded: []string{"192.168.3.128/26", "192.168.3.251/32", "192.168.3.255/32"}},
		{name: "local broader route", edit: `routes.append(dict(routes[2], dst="192.168.0.0/16"))`},
		{name: "whole subnet excluded", edit: `routes.extend([dict(routes[0], dst="192.168.3.0/25"),dict(routes[0], dst="192.168.3.128/25")])`},
		{name: "duplicate base", edit: `routes.append(dict(routes[1], metric=200))`},
		{name: "connected gateway", edit: `routes[1]["gateway"] = "192.168.3.1"`},
		{name: "connected next hop ID", edit: `routes[1]["nhid"] = 5`},
		{name: "connected multipath", edit: `routes[1]["nexthops"] = []`},
		{name: "connected encap", edit: `routes[1]["encap"] = {"type":"ip"}`},
		{name: "connected source selector", edit: `routes[1]["from"] = "192.168.3.251"`},
		{name: "connected TOS", edit: `routes[1]["tos"] = "16"`},
		{name: "connected unknown attribute", edit: `routes[1]["future"] = True`},
		{name: "connected wrong type", edit: `routes[1]["type"] = "6"`},
		{name: "connected wrong scope", edit: `routes[1]["scope"] = "0"`},
		{name: "connected wrong protocol", edit: `routes[1]["protocol"] = "4"`},
		{name: "connected down", edit: `routes[1]["flags"] = ["linkdown"]`},
		{name: "connected wrong source", edit: `routes[1]["prefsrc"] = "192.168.3.252"`},
		{name: "connected wrong interface", edit: `routes[1]["dev"] = "other"`},
		{name: "connected bool metric", edit: `routes[1]["metric"] = True`},
		{name: "missing local route", edit: `del routes[2]`},
		{name: "duplicate local route", edit: `routes.append(dict(routes[2]))`},
		{name: "missing rules", edit: `rules.clear()`},
		{name: "extra rule", edit: `rules.append(dict(rules[1],priority=100,table="100"))`},
		{name: "rule order", edit: `rules.reverse()`},
		{name: "rule priority", edit: `rules[1]["priority"] = 100`},
		{name: "bool priority", edit: `rules[0]["priority"] = False`},
		{name: "rule mark", edit: `rules[1]["fwmark"] = "1"`},
		{name: "rule inverted", edit: `rules[1]["not"] = None`},
		{name: "rule UID", edit: `rules[1]["uid_start"] = 0; rules[1]["uid_end"] = 100`},
		{name: "rule protocol selector", edit: `rules[1]["ipproto"] = "6"`},
		{name: "rule port", edit: `rules[1]["dport"] = 6443`},
		{name: "rule interface", edit: `rules[1]["oif"] = "ens18"`},
		{name: "rule suppress", edit: `rules[1]["suppress_prefixlen"] = 24`},
		{name: "rule action", edit: `rules[1]["action"] = "6"`},
		{name: "rule goto", edit: `rules[1]["goto"] = 32767`},
		{name: "rule source", edit: `rules[1]["src"] = "192.168.3.251"`},
		{name: "rule table alias", edit: `rules[1]["table"] = "main"`},
		{name: "rule future field", edit: `rules[1]["future"] = "unknown"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := persistentNetworkLibraryScript + activeNetworkLibraryScript + routedNetworkLibraryScript +
				"\ntargets = json.loads(" + strconv.Quote(string(targets)) + ")\nactive = json.loads(" + strconv.Quote(string(active)) + ")\nrules, routes = json.loads(" + strconv.Quote(routingStateFixture) + ")\n" + tc.edit +
				"\nmain(lambda: {\"version\":1, \"targets\":targets, \"active\":active, \"networks\":direct_networks(active, json.dumps([rules,routes])), \"resolved\":[]})\n"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			raw, err := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script).Output()
			if err != nil {
				t.Fatal("route projection failed", err)
			}
			value, err := parseRoutedNetwork(raw)
			if err != nil || (len(value.Networks) == 1) != tc.matched || len(value.Networks) > 1 || bytes.Contains(raw, []byte("private_fixture")) {
				t.Fatal("unexpected route projection", err, len(value.Networks))
			}
			if tc.matched {
				want := tc.excluded
				if want == nil {
					want = routedNetworkFixture(t).Networks[0].Excluded
				}
				if !reflect.DeepEqual(value.Networks[0].Excluded, want) {
					t.Fatal("competing destinations lost", value.Networks[0].Excluded, want)
				}
			}
		})
	}
}

func TestRoutedNetworkObservationScope(t *testing.T) {
	for _, mode := range []string{"success", "rules changed", "routes changed", "unused table changed", "scalar type changed", "route duplicate JSON", "route alias JSON", "route malformed", "route missing table", "route symbolic type", "route host bits", "route overflow", "route too many", "rule too many", "route hang", "route failed exit", "cached redirect", "lookup changed", "lookup cache flags", "lookup wrong interface", "lookup wrong source", "lookup wrong peer", "lookup wrong table", "lookup unknown", "lookup missing", "lookup bool uid", "lookup local", "lookup failure", "lookup hang", "lookup duplicate", "owner changed", "namespace changed", "boot changed", "netplan changed"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			write := func(name, body string, mode os.FileMode) {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("etc/machine-id", strings.Repeat("a", 32), 0o600)
			write("proc/sys/kernel/random/boot_id", "11111111-1111-4111-8111-111111111111", 0o600)
			write("proc/self/ns/net", "namespace fixture", 0o600)
			write("etc/netplan/10-static.yaml", persistentNetplanFixture, 0o600)
			write("run/systemd/network/10-netplan-ens18.network", "[Match]\nName=ens18\n[Network]\nAddress=192.168.3.251/24\n", 0o600)
			write("daemon.json", networkdDescriptionFixture, 0o600)
			write("kernel.json", strings.Replace(privilegedAddressFixture, `"ifname":`, `"ifindex":2,"ifname":`, 1), 0o600)
			write("routing.json", routingStateFixture, 0o600)
			fixture := strings.Replace(activeNetworkCommandFixture, "args = sys.argv[1:]\n", "args = sys.argv[1:]\n"+routedNetworkCommandFixture, 1)
			stub := "#!/usr/bin/python3\nROOT=" + strconv.Quote(root) + "\nMODE=" + strconv.Quote(mode) + "\n" + fixture
			write("bin/ip", stub, 0o700)
			write("bin/busctl", stub, 0o700)
			script := strings.Replace(routedNetworkScript, `ROOT = "/"`, "ROOT = "+strconv.Quote(root), 1)
			script = strings.Replace(script, "EXPECTED_UID = 0", "EXPECTED_UID = "+strconv.Itoa(os.Getuid()), 1)
			script = strings.ReplaceAll(script, `"/usr/bin/busctl"`, strconv.Quote(filepath.Join(root, "bin/busctl")))
			script = strings.ReplaceAll(script, `"/usr/sbin/ip"`, strconv.Quote(filepath.Join(root, "bin/ip")))
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			targets, _ := json.Marshal(routedNetworkFixture(t).Targets)
			command := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script, base64.StdEncoding.EncodeToString(targets))
			command.Env = append(os.Environ(), "DBUS_SYSTEM_BUS_ADDRESS=invalid-inherited-bus", "HTTP_PROXY=private-fixture-proxy")
			raw, err := command.Output()
			if ctx.Err() != nil || (err == nil) != (mode == "success") {
				t.Fatal("observation outcome", err, ctx.Err())
			}
			if err == nil {
				value, err := parseRoutedNetwork(raw)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := value.ManagementRoutes(routedPrivilegedFixture(t), "192.168.3.251", []string{"192.168.3.250", "192.168.3.252"}); err != nil {
					t.Fatal("complete route observation rejected", err)
				}
			} else if len(raw) != 0 {
				t.Fatal("failed observer emitted partial evidence")
			}
			if pidBytes, err := os.ReadFile(filepath.Join(root, "child-pid")); err == nil {
				pid, err := strconv.Atoi(string(pidBytes))
				if err != nil || syscall.Kill(pid, 0) != syscall.ESRCH {
					t.Fatal("timed-out route child not killed and joined")
				}
			}
		})
	}
}

const routedNetworkCommandFixture = `if pathlib.Path(sys.argv[0]).name == "ip" and args[:6] == ["-j", "-N", "-d", "-4", "route", "get"]:
    assert len(args) == 9 and args[7:] == ["from", "192.168.3.251"] and args[6] in ("192.168.3.250", "192.168.3.252")
    counter = root/('get-'+args[6])
    n = int(counter.read_text())+1 if counter.exists() else 1
    counter.write_text(str(n))
    value = {"type":"1", "dst":args[6], "from":args[8], "dev":"ens18", "table":"254", "flags":[], "uid":os.getuid(), "cache":[]}
    if MODE == "cached redirect" or MODE == "lookup changed" and n > 1: value["gateway"] = "192.168.3.1"
    if MODE == "lookup cache flags": value["cache"] = ["redirected"]
    if MODE == "lookup wrong interface": value["dev"] = "other"
    if MODE == "lookup wrong source": value["from"] = "192.168.3.253"
    if MODE == "lookup wrong peer": value["dst"] = "192.168.3.253"
    if MODE == "lookup wrong table": value["table"] = "100"
    if MODE == "lookup unknown": value["future"] = True
    if MODE == "lookup missing": del value["cache"]
    if MODE == "lookup bool uid": value["uid"] = False
    if MODE == "lookup local": value["type"] = "2"
    if MODE == "lookup failure": sys.exit(1)
    if MODE == "lookup hang":
        (root/'child-pid').write_text(str(os.getpid()))
        time.sleep(30)
    if MODE == "lookup duplicate": print(json.dumps([value,value]))
    else: print(json.dumps([value]))
    sys.exit(0)
if pathlib.Path(sys.argv[0]).name == "ip" and args[:4] == ["-j", "-N", "-d", "-4"]:
    assert args[4:] in (["rule", "show"], ["route", "show", "table", "all"])
    method = args[4]
    counter = root/('count-'+method)
    n = int(counter.read_text())+1 if counter.exists() else 1
    counter.write_text(str(n))
    rules, routes = json.loads((root/'routing.json').read_text())
    if MODE == "rules changed" and n > 1: rules[1]["fwmark"] = "1"
    if MODE == "routes changed" and n > 1: routes[1]["dev"] = "other"
    if MODE == "unused table changed" and n > 1: routes.append(dict(routes[1], table="100"))
    if MODE == "scalar type changed": routes.append(dict(routes[1], table="100", metric=1 if n == 1 else True))
    if MODE == "route missing table": del routes[1]["table"]
    if MODE == "route symbolic type": routes[1]["type"] = "unicast"
    if MODE == "route host bits": routes[1]["dst"] = "192.168.3.1/24"
    if MODE == "route too many": routes *= 129
    if MODE == "rule too many": rules *= 43
    if method == "route":
        if MODE == "route hang":
            (root/'child-pid').write_text(str(os.getpid()))
            time.sleep(30)
        if MODE == "route overflow": sys.stdout.write('x'*131073); sys.exit(0)
        if MODE == "route malformed": print('private_fixture'); sys.exit(0)
        if MODE == "route failed exit": sys.exit(1)
    output = json.dumps(rules if method == "rule" else routes)
    if method == "route" and MODE == "route duplicate JSON": output = output.replace('"table":', '"table":"254","table":', 1)
    if method == "route" and MODE == "route alias JSON": output = output.replace('"table":', '"Table":"254","table":', 1)
    print(output)
    sys.exit(0)
`

func TestRoutedNetworkPinnedSSHBoundaries(t *testing.T) {
	raw, _ := json.Marshal(routedNetworkFixture(t))
	targets := routedNetworkFixture(t).Targets
	expectedCommand, err := routedNetworkCommand(targets)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "malformed", "overflow", "failed exit", "cancel", "invalid sudo", "wrong targets"} {
		t.Run(mode, func(t *testing.T) {
			release := make(chan struct{})
			server := newFakeSSH(t, "routed network", func(command string, channel ssh.Channel) uint32 {
				if command != expectedCommand {
					return 1
				}
				password, err := io.ReadAll(channel)
				if err != nil || !bytes.Equal(password, []byte("routed network\n")) {
					return 1
				}
				switch mode {
				case "cancel":
					<-release
				case "overflow":
					channel.Write(bytes.Repeat([]byte("x"), MaxOutputBytes+1))
				case "malformed":
					channel.Write([]byte(`{"private":"fixture"}`))
				case "failed exit":
					channel.Write(raw)
					channel.Stderr().Write([]byte("private fixture diagnostics"))
					return 1
				case "wrong targets":
					channel.Write(bytes.Replace(raw, []byte(`"management":"192.168.3.251"`), []byte(`"management":"192.168.3.249"`), 1))
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
			password := []byte("routed network")
			if mode == "invalid sudo" {
				password = append(password, '\n')
			}
			value, err := client.InspectRoutedNetwork(ctx, password, targets)
			close(release)
			if mode == "success" {
				if err != nil || len(value.Networks) != 1 {
					t.Fatal("valid route observer rejected", err)
				}
			} else if err == nil || len(value.Networks) != 0 || strings.Contains(err.Error(), "private fixture") {
				t.Fatal("unsafe route observer result", err)
			}
			if mode == "cancel" && err != ErrCancelled {
				t.Fatal("cancellation lost", err)
			}
			want := map[string]error{"malformed": ErrRoutedNetwork, "overflow": ErrOutputLimit, "failed exit": ErrPrivilegeInspection, "invalid sudo": ErrInvalidAuth, "wrong targets": ErrRoutedNetwork}
			if expected, ok := want[mode]; ok && err != expected {
				t.Fatal("fixture did not exercise expected boundary", err, expected)
			}
			if mode == "invalid sudo" && server.execCalls.Load() != 0 {
				t.Fatal("invalid sudo input executed")
			}
		})
	}
}
