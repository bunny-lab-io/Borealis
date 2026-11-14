package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func bootstrapPartialFirewallFixture(chainCount, jumps int) []byte {
	expected := bootstrapHostFilesFixture()
	chain := bootstrapHostFirewallChainRules(expected)
	input := bootstrapHostFirewallInputRules()
	rows := []string{"-P INPUT ACCEPT", "-P FORWARD DROP", "-P OUTPUT ACCEPT", "-N FOREIGN"}
	rows = append(rows, chain[:chainCount]...)
	rows = append(rows, input[len(input)-jumps:]...)
	rows = append(rows, "-A INPUT -p tcp -m tcp --dport 2222 -j FOREIGN", "-A FOREIGN -j RETURN")
	return []byte(strings.Join(rows, "\n") + "\n")
}

func TestBootstrapPartialFirewallConstruction(t *testing.T) {
	expected := bootstrapHostFilesFixture()
	count := len(bootstrapHostFirewallChainRules(expected))
	for chain := 0; chain <= count; chain++ {
		for jumps := 0; jumps <= 7; jumps++ {
			t.Run(fmt.Sprintf("%d/%d", chain, jumps), func(t *testing.T) {
				err := validateBootstrapPartialFirewall(bootstrapPartialFirewallFixture(chain, jumps), expected)
				valid := chain > 0 && (jumps == 0 || chain == count)
				if (err == nil) != valid {
					t.Fatal("unreachable firewall state accepted", err)
				}
			})
		}
	}
}

func TestBootstrapPartialFirewallRejectsForeign(t *testing.T) {
	expected := bootstrapHostFilesFixture()
	valid := string(bootstrapPartialFirewallFixture(len(bootstrapHostFirewallChainRules(expected)), 3))
	for _, mode := range []string{"unknown allow", "wrong peer", "missing drop", "wrong order", "earlier bypass", "late jump", "foreign reference", "foreign declaration", "duplicate", "wrong policy", "truncated", "control"} {
		t.Run(mode, func(t *testing.T) {
			raw := valid
			switch mode {
			case "unknown allow":
				raw = strings.Replace(raw, "-A BOREALIS-K3S-API -j DROP", "-A BOREALIS-K3S-API -j ACCEPT", 1)
			case "wrong peer":
				raw = strings.Replace(raw, "192.168.3.250/32", "0.0.0.0/0", 1)
			case "missing drop":
				raw = strings.Replace(raw, "-A BOREALIS-K3S-API -j DROP\n", "", 1)
			case "wrong order":
				raw = strings.Replace(raw, "--dport 10250", "--dport 6443", 1)
			case "earlier bypass":
				raw = strings.Replace(raw, "-A INPUT -p tcp -m tcp --dport 10250", "-A INPUT -j ACCEPT\n-A INPUT -p tcp -m tcp --dport 10250", 1)
			case "late jump":
				raw += "-A INPUT -p udp -m udp --dport 8472 -j BOREALIS-K3S-API\n"
			case "foreign reference":
				raw += "-A FORWARD -j BOREALIS-K3S-API\n"
			case "foreign declaration":
				raw += "-N BOREALIS-K3S-API-FOREIGN\n"
			case "duplicate":
				raw += "-N BOREALIS-K3S-API\n"
			case "wrong policy":
				raw = strings.Replace(raw, "-P FORWARD DROP", "-P FORWARD RETURN", 1)
			case "truncated":
				raw = strings.TrimSuffix(raw, "\n")
			case "control":
				raw = strings.Replace(raw, "FOREIGN", "FOREIGN\x00", 1)
			}
			if validateBootstrapPartialFirewall([]byte(raw), expected) == nil {
				t.Fatal("foreign state admitted")
			}
		})
	}
}

func TestBootstrapPartialFirewallObservation(t *testing.T) {
	for _, mode := range []string{"valid", "read error", "late rule", "late authority", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			expected := bootstrapHostFilesFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads := 0
			lost := false
			check := func(context.Context) error {
				if lost {
					return errBootstrapSource
				}
				return nil
			}
			read := func(_ context.Context, probe string) ([]byte, error) {
				if probe != "filter" {
					t.Fatal(probe)
				}
				reads++
				if mode == "read error" {
					return nil, errBootstrapSource
				}
				raw := string(bootstrapPartialFirewallFixture(3, 0))
				if mode == "late rule" && reads == 2 {
					raw += "-A FOREIGN -j DROP\n"
				}
				if mode == "late authority" && reads == 2 {
					lost = true
				}
				if mode == "cancel" {
					cancel()
				}
				return []byte(raw), nil
			}
			if err := proveBootstrapPartialFirewallWith(ctx, expected, check, read); (err == nil) != (mode == "valid") || mode == "valid" && reads != 2 {
				t.Fatal("unstable partial proof", reads, err)
			}
		})
	}
}

func bootstrapInterruptedFirewallUnitFixture() string {
	raw := string(bootstrapHostDependencyFixture("borealis-k3s-api-firewall.service"))
	return strings.NewReplacer("ActiveState=active", "ActiveState=failed", "SubState=exited", "SubState=failed", "Result=success", "Result=exit-code", "ExecMainStatus=0", "ExecMainStatus=1").Replace(raw)
}

func TestBootstrapInterruptedFirewallUnit(t *testing.T) {
	for _, mode := range []string{"exit", "signal", "timeout", "inactive", "running", "queued", "foreign", "dropin", "reload", "disabled", "status zero", "status padded", "rate limited", "unknown signal", "timeout status", "missing", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			raw := bootstrapInterruptedFirewallUnitFixture()
			switch mode {
			case "signal":
				raw = strings.NewReplacer("Result=exit-code", "Result=signal", "ExecMainStatus=1", "ExecMainStatus=9").Replace(raw)
			case "timeout":
				raw = strings.NewReplacer("Result=exit-code", "Result=timeout", "ExecMainStatus=1", "ExecMainStatus=15").Replace(raw)
			case "inactive":
				raw = strings.NewReplacer("ActiveState=failed", "ActiveState=inactive", "SubState=failed", "SubState=dead", "Result=exit-code", "Result=success", "ExecMainStatus=1", "ExecMainStatus=0").Replace(raw)
			case "running":
				raw = strings.Replace(raw, "MainPID=0", "MainPID=123", 1)
			case "queued":
				raw = strings.Replace(raw, "Job=\n", "Job=9\n", 1)
			case "foreign":
				raw = strings.Replace(raw, "FragmentPath=/etc/systemd/system/", "FragmentPath=/tmp/", 1)
			case "dropin":
				raw = strings.Replace(raw, "DropInPaths=\n", "DropInPaths=/tmp/override\n", 1)
			case "reload":
				raw = strings.Replace(raw, "NeedDaemonReload=no", "NeedDaemonReload=yes", 1)
			case "disabled":
				raw = strings.Replace(raw, "UnitFileState=enabled", "UnitFileState=disabled", 1)
			case "status zero":
				raw = strings.Replace(raw, "ExecMainStatus=1", "ExecMainStatus=0", 1)
			case "status padded":
				raw = strings.Replace(raw, "ExecMainStatus=1", "ExecMainStatus=01", 1)
			case "rate limited":
				raw = strings.Replace(raw, "Result=exit-code", "Result=start-limit-hit", 1)
			case "unknown signal":
				raw = strings.NewReplacer("Result=exit-code", "Result=signal", "ExecMainStatus=1", "ExecMainStatus=255").Replace(raw)
			case "timeout status":
				raw = strings.Replace(raw, "Result=exit-code", "Result=timeout", 1)
			case "missing":
				raw = strings.Replace(raw, "Job=\n", "", 1)
			case "duplicate":
				raw += "Job=\n"
			}
			valid := mode == "exit" || mode == "signal" || mode == "timeout" || mode == "inactive"
			if err := validateBootstrapInterruptedFirewallUnit([]byte(raw)); (err == nil) != valid {
				t.Fatal("unknown unit accepted", err)
			}
		})
	}
}

func TestBootstrapFirewallHostRequiresCompleteFoundation(t *testing.T) {
	for _, mode := range []string{"valid", "stopped iscsi", "active multipath", "unit running", "files", "repository", "kernel", "firewall"} {
		t.Run(mode, func(t *testing.T) {
			check := func(context.Context) error { return nil }
			component := func(name string) func(context.Context) error {
				return func(context.Context) error {
					if mode == name {
						return errBootstrapSource
					}
					return nil
				}
			}
			read := func(_ context.Context, probe string) ([]byte, error) {
				raw := string(bootstrapConfiguredHostFixture(probe))
				if probe == "borealis-k3s-api-firewall.service" {
					raw = bootstrapInterruptedFirewallUnitFixture()
					if mode == "unit running" {
						raw = string(bootstrapHostDependencyFixture(probe))
					}
				}
				if mode == "stopped iscsi" && probe == "iscsid.service" {
					raw = strings.NewReplacer("ActiveState=active", "ActiveState=inactive", "SubState=running", "SubState=dead", "MainPID=123", "MainPID=0").Replace(raw)
				}
				if mode == "active multipath" && probe == "multipathd.service" {
					raw = strings.Replace(raw, "ActiveState=inactive", "ActiveState=active", 1)
				}
				return []byte(raw), nil
			}
			err := proveBootstrapHostRecoveryWith(context.Background(), check, read, component("repository"), component("files"), component("kernel"), component("firewall"), true)
			if (err == nil) != (mode == "valid") {
				t.Fatal("incomplete foundation accepted", err)
			}
		})
	}
}

func TestBootstrapFirewallContinuationOrdering(t *testing.T) {
	want := []string{"partial", "run", "prove"}
	for failAt := -1; failAt < 3; failAt++ {
		for _, mode := range []string{"error", "cancel", "authority"} {
			t.Run(fmt.Sprintf("%d/%s", failAt, mode), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				lost := false
				var got []string
				check := func(context.Context) error {
					if lost {
						return errBootstrapSource
					}
					return nil
				}
				call := func(name string) func(context.Context) error {
					return func(context.Context) error {
						got = append(got, name)
						if len(got)-1 == failAt {
							switch mode {
							case "error":
								return errBootstrapSource
							case "cancel":
								cancel()
							case "authority":
								lost = true
							}
						}
						return nil
					}
				}
				err := completeBootstrapFirewallWith(ctx, check, call("partial"), call("run"), call("prove"))
				prefix := want
				if failAt >= 0 {
					prefix = want[:failAt+1]
				}
				if (err == nil) != (failAt < 0) || !reflect.DeepEqual(got, prefix) {
					t.Fatal("unsafe firewall continuation", got, err)
				}
			})
		}
	}
}
