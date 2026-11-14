package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func bootstrapConfiguredHostFixture(probe string) []byte {
	switch probe {
	case "architecture":
		return []byte("amd64\n")
	case "inventory":
		raw := string(bootstrapHostDependencyFixture("packages"))
		for _, name := range []string{"apt", "dpkg", "base-files"} {
			raw += name + "\tamd64\tinstall ok installed\t1.0\n"
		}
		return []byte(raw)
	case "borealis-k3s-api-firewall.service":
		raw := string(bootstrapHostDependencyFixture(probe))
		raw = strings.Replace(raw, "ActiveState=active", "ActiveState=inactive", 1)
		return []byte(strings.Replace(raw, "SubState=exited", "SubState=dead", 1))
	default:
		return bootstrapHostDependencyFixture(probe)
	}
}

func TestBootstrapConfiguredPackageInventory(t *testing.T) {
	raw := string(bootstrapConfiguredHostFixture("inventory"))
	for _, mode := range []string{"valid", "duplicate", "missing", "half configured unrelated", "foreign runtime", "wrong arch", "truncated", "bad version"} {
		t.Run(mode, func(t *testing.T) {
			value := raw
			switch mode {
			case "duplicate":
				value += strings.Split(raw, "\n")[0] + "\n"
			case "missing":
				value = strings.Replace(value, "docker-ce\tamd64\tinstall ok installed\t1:2.3-4ubuntu1\n", "", 1)
			case "half configured unrelated":
				value += "other\tamd64\tinstall ok unpacked\t1.0\n"
			case "foreign runtime":
				value += "containerd\tamd64\tinstall ok installed\t1.0\n"
			case "wrong arch":
				value = strings.Replace(value, "amd64", "arm64", 1)
			case "truncated":
				value = strings.TrimSuffix(value, "\n")
			case "bad version":
				value = strings.Replace(value, "1:2.3-4ubuntu1", "foreign\x00", 1)
			}
			if err := validateBootstrapConfiguredPackages([]byte(value)); (err == nil) != (mode == "valid") {
				t.Fatal("unsafe inventory", err)
			}
		})
	}
}

func TestBootstrapRecoverableUnit(t *testing.T) {
	for _, unit := range []string{"docker.service", "iscsid.service", "borealis-k3s-api-firewall.service"} {
		raw := string(bootstrapConfiguredHostFixture(unit))
		if validateBootstrapRecoverableUnit(unit, []byte(raw)) != nil {
			t.Fatal("valid unit", unit)
		}
		for _, mutation := range []struct{ key, value string }{{"Id", "foreign"}, {"LoadState", "error"}, {"ActiveState", "activating"}, {"SubState", "start"}, {"MainPID", "01"}, {"ControlPID", "9"}, {"Job", "10"}, {"DropInPaths", "/etc/foreign"}, {"FragmentPath", "/tmp/foreign"}, {"Result", "exit-code"}, {"ExecMainStatus", "1"}, {"UnitFileState", "masked"}, {"Type", "oneshot-foreign"}} {
			t.Run(unit+"/"+mutation.key, func(t *testing.T) {
				rows := strings.Split(raw, "\n")
				for i, row := range rows {
					if strings.HasPrefix(row, mutation.key+"=") {
						rows[i] = mutation.key + "=" + mutation.value
					}
				}
				if validateBootstrapRecoverableUnit(unit, []byte(strings.Join(rows, "\n"))) == nil {
					t.Fatal("unit drift accepted")
				}
			})
		}
		for _, value := range []string{raw + "Job=\n", strings.Replace(raw, "Job=\n", "", 1), raw + "Unknown=yes\n", strings.TrimSuffix(raw, "\n")} {
			if validateBootstrapRecoverableUnit(unit, []byte(value)) == nil {
				t.Fatal("malformed unit", unit)
			}
		}
	}
	iscsi := string(bootstrapConfiguredHostFixture("iscsid.service"))
	iscsi = strings.NewReplacer("ActiveState=active", "ActiveState=inactive", "SubState=running", "SubState=dead", "MainPID=123", "MainPID=0", "UnitFileState=enabled", "UnitFileState=disabled").Replace(iscsi)
	if validateBootstrapRecoverableUnit("iscsid.service", []byte(iscsi)) != nil {
		t.Fatal("known stopped iscsid rejected")
	}
	if validateBootstrapRecoverableUnit("docker.service", []byte(strings.ReplaceAll(iscsi, "iscsid.service", "docker.service"))) == nil {
		t.Fatal("stopped Docker adopted")
	}
	for _, reload := range []string{"yes", "no"} {
		raw := string(bootstrapConfiguredHostFixture("borealis-k3s-api-firewall.service"))
		raw = strings.NewReplacer("LoadState=loaded", "LoadState=not-found", "UnitFileState=enabled", "UnitFileState=", "FragmentPath=/etc/systemd/system/borealis-k3s-api-firewall.service", "FragmentPath=", "Type=oneshot", "Type=simple", "NeedDaemonReload=no", "NeedDaemonReload="+reload).Replace(raw)
		if validateBootstrapRecoverableUnit("borealis-k3s-api-firewall.service", []byte(raw)) != nil {
			t.Fatal("known absent firewall rejected")
		}
	}
}

func TestBootstrapConfiguredHostProof(t *testing.T) {
	for _, mode := range []string{"valid", "repository", "files", "kernel", "firewall", "read", "late packages", "containers", "stopped docker", "late authority", "cancel"} {
		t.Run(mode, func(t *testing.T) {
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
			component := func(name string) func(context.Context) error {
				return func(context.Context) error {
					if mode == name {
						return errBootstrapSource
					}
					return nil
				}
			}
			read := func(_ context.Context, probe string) ([]byte, error) {
				reads++
				if mode == "read" {
					return nil, errBootstrapSource
				}
				raw := string(bootstrapConfiguredHostFixture(probe))
				if mode == "late packages" && reads == 10 {
					raw = strings.ReplaceAll(raw, "1.0", "2.0")
				}
				if mode == "containers" && probe == "docker-empty" {
					raw = "1 0 0 1\n"
				}
				if mode == "stopped docker" && probe == "docker.service" {
					raw = strings.ReplaceAll(raw, "active", "inactive")
				}
				if mode == "late authority" && reads == 16 {
					lost = true
				}
				if mode == "cancel" {
					cancel()
				}
				return []byte(raw), nil
			}
			err := proveBootstrapConfiguredHostWith(ctx, check, read, component("repository"), component("files"), component("kernel"), component("firewall"))
			if (err == nil) != (mode == "valid") || mode == "valid" && reads != 16 {
				t.Fatal("partial state misclassified", reads, err)
			}
		})
	}
}

func TestBootstrapConfiguredHostOrdering(t *testing.T) {
	want := []string{"configured", "files", "command4", "kernel", "multipath", "command7", "iscsi", "firewall", "command10", "prove"}
	for failAt := -1; failAt < len(want); failAt++ {
		for _, failure := range []string{"error", "authority", "cancel"} {
			t.Run(fmt.Sprintf("%d/%s", failAt, failure), func(t *testing.T) {
				host, identity := bootstrapHostCommandLocks(t)
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
							switch failure {
							case "error":
								return errBootstrapSource
							case "authority":
								lost = true
							case "cancel":
								cancel()
							}
						}
						return nil
					}
				}
				o := bootstrapConfiguredHostOperations{configured: call("configured"), files: call("files"), kernel: call("kernel"), multipath: call("multipath"), iscsi: call("iscsi"), firewall: call("firewall"), prove: call("prove"), command: func(ctx context.Context, step bootstrapHostCommand) error {
					if step == bootstrapHostAPTUpdate || step == bootstrapHostInstallBase || step == bootstrapHostInstallDocker || step == bootstrapHostEnableDocker {
						t.Fatal("package/Docker replay")
					}
					return call(fmt.Sprintf("command%d", step))(ctx)
				}}
				err := completeBootstrapConfiguredHostWith(ctx, host, identity, check, o)
				prefix := want
				if failAt >= 0 {
					prefix = want[:failAt+1]
				}
				if !reflect.DeepEqual(got, prefix) || (err == nil) != (failAt < 0) {
					t.Fatal("unsafe continuation", got, err)
				}
			})
		}
	}
}

func TestBootstrapRecoveryKernelMissingModule(t *testing.T) {
	for _, mode := range []string{"absent", "live", "unloading", "unknown", "multipath"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			if err := os.MkdirAll(filepath.Join(base, "class/block"), 0700); err != nil {
				t.Fatal(err)
			}
			if mode != "absent" && mode != "multipath" {
				if err := os.MkdirAll(filepath.Join(base, "module/iscsi_tcp"), 0700); err != nil {
					t.Fatal(err)
				}
				if mode != "unknown" {
					raw := "live\n"
					if mode == "unloading" {
						raw = "going\n"
					}
					if err := os.WriteFile(filepath.Join(base, "module/iscsi_tcp/initstate"), []byte(raw), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "multipath" {
				if err := os.MkdirAll(filepath.Join(base, "class/block/dm-0/dm"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(base, "class/block/dm-0/dm/uuid"), []byte("mpath-foreign\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(base)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			check := func(context.Context) error { return nil }
			if err := proveBootstrapKernelWith(context.Background(), root, check, true); (err == nil) != (mode == "absent" || mode == "live") {
				t.Fatal("unsafe kernel state", err)
			}
			if mode == "absent" && proveBootstrapPrejoinKernelWith(context.Background(), root, check) == nil {
				t.Fatal("complete proof weakened")
			}
		})
	}
}

func TestBootstrapRecoveryISCSI(t *testing.T) {
	for _, mode := range []string{"running", "stopped", "write uncertainty", "late foreign", "final failure", "authority"} {
		t.Run(mode, func(t *testing.T) {
			active := mode == "running"
			reads, writes := 0, 0
			lost := false
			check := func(context.Context) error {
				if lost {
					return errBootstrapSource
				}
				return nil
			}
			read := func(_ context.Context, unit string) ([]byte, error) {
				if unit != "iscsid.service" {
					t.Fatal(unit)
				}
				reads++
				raw := string(bootstrapHostDependencyFixture(unit))
				if !active {
					raw = strings.NewReplacer("ActiveState=active", "ActiveState=inactive", "SubState=running", "SubState=dead", "MainPID=123", "MainPID=0", "UnitFileState=enabled", "UnitFileState=disabled").Replace(raw)
				}
				if mode == "late foreign" && reads == 3 {
					raw = strings.ReplaceAll(raw, "DropInPaths=", "DropInPaths=/foreign")
				}
				return []byte(raw), nil
			}
			run := func(_ context.Context, step bootstrapHostCommand) error {
				writes++
				if step != bootstrapHostEnableISCSI {
					t.Fatal("wrong command")
				}
				active = mode != "final failure"
				lost = mode == "authority"
				if mode == "write uncertainty" {
					return errBootstrapSource
				}
				return nil
			}
			err := completeBootstrapISCSIWith(context.Background(), check, read, run)
			if (err == nil) != (mode == "running" || mode == "stopped") || writes > 1 || (mode == "running" || mode == "late foreign") && writes != 0 {
				t.Fatal("unsafe service continuation", writes, reads, err)
			}
		})
	}
}
