package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func bootstrapHostDependencyFixture(probe string) []byte {
	if probe == "packages" {
		var rows []string
		for _, name := range bootstrapHostPackages {
			rows = append(rows, name+"\tamd64\tinstall ok installed\t1:2.3-4ubuntu1\n")
		}
		return []byte(strings.Join(rows, ""))
	}
	if probe == "docker-empty" {
		return []byte("0 0 0 0\n")
	}
	values := map[string]string{
		"Id": probe, "LoadState": "loaded", "ActiveState": "active", "SubState": "running", "Type": "notify",
		"MainPID": "123", "ControlPID": "0", "NeedDaemonReload": "no", "Job": "", "UnitFileState": "enabled",
		"FragmentPath": "/usr/lib/systemd/system/" + probe, "DropInPaths": "", "Result": "success", "ExecMainStatus": "0",
	}
	if probe == "borealis-k3s-api-firewall.service" {
		values["SubState"], values["Type"], values["MainPID"] = "exited", "oneshot", "0"
		values["FragmentPath"] = "/etc/systemd/system/" + probe
	}
	keys := bootstrapHostUnitProperties
	if probe == "multipathd.service" || probe == "multipathd.socket" {
		values["LoadState"], values["ActiveState"], values["SubState"], values["MainPID"] = "not-found", "inactive", "dead", "0"
		values["UnitFileState"], values["FragmentPath"] = "", ""
		if probe == "multipathd.socket" {
			keys = bootstrapHostSocketProperties
		}
	}
	var rows []string
	for _, key := range strings.Split(keys, ",") {
		rows = append(rows, key+"="+values[key]+"\n")
	}
	return []byte(strings.Join(rows, ""))
}

func TestBootstrapHostDependencyPackages(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "truncated", "duplicate", "missing", "foreign", "wrong arch", "unconfigured", "broken", "version control", "version empty", "unknown probe"} {
		t.Run(mode, func(t *testing.T) {
			raw, probe := string(bootstrapHostDependencyFixture("packages")), "packages"
			switch mode {
			case "empty":
				raw = ""
			case "truncated":
				raw = strings.TrimSuffix(raw, "\n")
			case "duplicate":
				raw += strings.Split(raw, "\n")[0] + "\n"
			case "missing":
				_, raw, _ = strings.Cut(raw, "\n")
			case "foreign":
				raw = strings.Replace(raw, "docker-ce\t", "docker.io\t", 1)
			case "wrong arch":
				raw = strings.Replace(raw, "amd64", "arm64", 1)
			case "unconfigured":
				raw = strings.Replace(raw, "install ok installed", "install ok unpacked", 1)
			case "broken":
				raw = strings.Replace(raw, "install ok installed", "install reinstreq installed", 1)
			case "version control":
				raw = strings.Replace(raw, "1:2.3-4ubuntu1", "1.2\r", 1)
			case "version empty":
				raw = strings.Replace(raw, "1:2.3-4ubuntu1", "", 1)
			case "unknown probe":
				probe = "arbitrary"
			}
			if err := validateBootstrapHostDependency(probe, []byte(raw)); (err == nil) != (mode == "valid") {
				t.Fatal("invalid package evidence accepted", mode, err)
			}
		})
	}
}

func TestBootstrapHostDependencyUnits(t *testing.T) {
	for _, probe := range []string{"docker.service", "iscsid.service", "borealis-k3s-api-firewall.service", "multipathd.service", "multipathd.socket"} {
		t.Run(probe, func(t *testing.T) {
			raw := string(bootstrapHostDependencyFixture(probe))
			if validateBootstrapHostDependency(probe, []byte(raw)) != nil {
				t.Fatal("valid unit rejected")
			}
			for _, mutation := range []struct{ key, value string }{
				{"Id", "foreign.service"}, {"LoadState", "error"}, {"ActiveState", "activating"}, {"SubState", "start"},
				{"NeedDaemonReload", "yes"}, {"Job", "4 /org/freedesktop/systemd1/job/4"}, {"DropInPaths", "/etc/systemd/system/override.conf"},
				{"FragmentPath", "/tmp/foreign.service"}, {"UnitFileState", "enabled-runtime"},
				{"Result", "exit-code"},
			} {
				t.Run(mutation.key, func(t *testing.T) {
					lines := strings.Split(raw, "\n")
					for i, line := range lines {
						if strings.HasPrefix(line, mutation.key+"=") {
							lines[i] = mutation.key + "=" + mutation.value
						}
					}
					if validateBootstrapHostDependency(probe, []byte(strings.Join(lines, "\n"))) == nil {
						t.Fatal("unit drift accepted", mutation.key)
					}
				})
			}
			for _, malformed := range []string{raw + "Id=" + probe + "\n", strings.Replace(raw, "Job=\n", "", 1), raw + "Unknown=yes\n"} {
				if validateBootstrapHostDependency(probe, []byte(malformed)) == nil {
					t.Fatal("incomplete/ambiguous unit accepted")
				}
			}
			if probe != "multipathd.socket" {
				for _, before := range []string{"ControlPID=0", "ExecMainStatus=0"} {
					if validateBootstrapHostDependency(probe, []byte(strings.Replace(raw, before, strings.TrimSuffix(before, "0")+"2", 1))) == nil {
						t.Fatal("active control process or failed execution accepted")
					}
				}
				for _, invalidPID := range []string{"-1", "01", "1", "4294967296"} {
					lines := strings.Split(raw, "\n")
					for i, line := range lines {
						if strings.HasPrefix(line, "MainPID=") {
							lines[i] = "MainPID=" + invalidPID
						}
					}
					if validateBootstrapHostDependency(probe, []byte(strings.Join(lines, "\n"))) == nil {
						t.Fatal("invalid process identity accepted", invalidPID)
					}
				}
			}
		})
	}
}

func TestBootstrapHostDependencyObservation(t *testing.T) {
	for _, mode := range []string{"valid", "read failure", "authority", "cancel", "late version", "daemon restart", "containers", "nil check", "nil read"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads, checks := 0, 0
			check := func(context.Context) error {
				checks++
				if mode == "authority" && checks == 29 {
					return errors.New("private error")
				}
				return nil
			}
			read := func(_ context.Context, probe string) ([]byte, error) {
				reads++
				if mode == "read failure" {
					return nil, errors.New("private error")
				}
				if mode == "cancel" {
					cancel()
				}
				raw := string(bootstrapHostDependencyFixture(probe))
				if mode == "late version" && reads == 8 {
					raw = strings.Replace(raw, "1:2.3-4ubuntu1", "1:2.3-5ubuntu1", 1)
				}
				if mode == "daemon restart" && reads == 9 {
					raw = strings.Replace(raw, "MainPID=123", "MainPID=456", 1)
				}
				if mode == "containers" && probe == "docker-empty" {
					raw = "1 0 0 1\n"
				}
				return []byte(raw), nil
			}
			if mode == "nil check" {
				check = nil
			}
			if mode == "nil read" {
				read = nil
			}
			err := proveBootstrapPrejoinDependenciesWith(ctx, check, read)
			if (err == nil) != (mode == "valid") || err != nil && err != errBootstrapSource {
				t.Fatal("dependency proof accepted drift or leaked diagnostics", err)
			}
			if mode == "valid" && (reads != 14 || checks != 29) {
				t.Fatal("full independent inventory not repeated", reads, checks)
			}
		})
	}
}

func TestBootstrapHostDependencyCommands(t *testing.T) {
	program, args := bootstrapHostDependencyCommand("packages")
	if program != "/usr/bin/dpkg-query" || len(args) != 3+len(bootstrapHostPackages) || args[2] != "--" {
		t.Fatal("package query contract changed")
	}
	program, args = bootstrapHostDependencyCommand("docker-empty")
	if program != "/usr/bin/docker" || strings.Join(args[:3], " ") != "--host unix:///run/docker.sock info" {
		t.Fatal("daemon not pinned to local socket")
	}
	for _, probe := range []string{"unknown", "docker.service; reboot", "--help", "../packages"} {
		if program, args := bootstrapHostDependencyCommand(probe); program != "" || args != nil {
			t.Fatal("non-fixed read command accepted")
		}
	}
}
