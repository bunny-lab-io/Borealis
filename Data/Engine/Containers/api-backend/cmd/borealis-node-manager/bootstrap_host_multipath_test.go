package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func bootstrapActiveMultipathFixture(unit string) []byte {
	raw := string(bootstrapHostDependencyFixture(unit))
	for old, next := range map[string]string{"LoadState=not-found\n": "LoadState=loaded\n", "ActiveState=inactive\n": "ActiveState=active\n",
		"UnitFileState=\n": "UnitFileState=enabled\n", "FragmentPath=\n": "FragmentPath=/usr/lib/systemd/system/" + unit + "\n"} {
		raw = strings.Replace(raw, old, next, 1)
	}
	if unit == "multipathd.service" {
		raw = strings.Replace(raw, "SubState=dead\n", "SubState=running\n", 1)
		raw = strings.Replace(raw, "MainPID=0\n", "MainPID=123\n", 1)
	} else {
		raw = strings.Replace(raw, "SubState=dead\n", "SubState=listening\n", 1)
	}
	return []byte(raw)
}

func TestBootstrapMultipathDecision(t *testing.T) {
	for _, unit := range []string{"multipathd.service", "multipathd.socket"} {
		for _, mode := range []string{"absent", "disabled", "enabled inactive", "active", "unknown", "masked", "override", "job", "reload", "failed", "foreign", "duplicate", "truncated", "state", "pid"} {
			t.Run(unit+"/"+mode, func(t *testing.T) {
				raw := string(bootstrapActiveMultipathFixture(unit))
				switch mode {
				case "absent":
					raw = string(bootstrapHostDependencyFixture(unit))
				case "disabled", "enabled inactive":
					raw = strings.Replace(raw, "ActiveState=active\n", "ActiveState=inactive\n", 1)
					raw = strings.Replace(raw, "SubState=running\n", "SubState=dead\n", 1)
					raw = strings.Replace(raw, "SubState=listening\n", "SubState=dead\n", 1)
					raw = strings.Replace(raw, "MainPID=123\n", "MainPID=0\n", 1)
					if mode == "disabled" {
						raw = strings.Replace(raw, "UnitFileState=enabled", "UnitFileState=disabled", 1)
					}
				case "unknown":
					raw += "Unknown=bad\n"
				case "masked":
					raw = strings.Replace(raw, "UnitFileState=enabled", "UnitFileState=masked", 1)
				case "override":
					raw = strings.Replace(raw, "DropInPaths=\n", "DropInPaths=/etc/override.conf\n", 1)
				case "job":
					raw = strings.Replace(raw, "Job=\n", "Job=23\n", 1)
				case "reload":
					raw = strings.Replace(raw, "NeedDaemonReload=no", "NeedDaemonReload=yes", 1)
				case "failed":
					raw = strings.Replace(raw, "Result=success", "Result=exit-code", 1)
				case "foreign":
					raw = strings.Replace(raw, "/usr/lib/systemd/system/", "/etc/systemd/system/", 1)
				case "duplicate":
					raw += "ActiveState=active\n"
				case "truncated":
					raw = strings.TrimSuffix(raw, "\n")
				case "state":
					raw = strings.Replace(raw, "ActiveState=active", "ActiveState=activating", 1)
				case "pid":
					if unit == "multipathd.service" {
						raw = strings.Replace(raw, "MainPID=123", "MainPID=1", 1)
					} else {
						raw += "MainPID=123\n"
					}
				}
				stop, err := bootstrapMultipathNeedsStop(unit, []byte(raw))
				valid := mode == "absent" || mode == "disabled" || mode == "enabled inactive" || mode == "active"
				if (err == nil) != valid || stop != (mode == "active" || mode == "enabled inactive") {
					t.Fatal("unsafe stop/skip", stop, err)
				}
			})
		}
	}
}

func TestBootstrapMultipathPreparation(t *testing.T) {
	for _, mode := range []string{"valid", "absent", "read failure", "maps", "drift", "stop failure", "unproven stop", "reactivation", "cancel", "authority"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var commands []bootstrapHostCommand
			stopped := map[string]bool{}
			calls, kernels, checks := 0, 0, 0
			check := func(context.Context) error {
				checks++
				if mode == "authority" && checks > 3 {
					return errors.New("private")
				}
				return nil
			}
			read := func(_ context.Context, unit string) ([]byte, error) {
				calls++
				if mode == "read failure" {
					return nil, errors.New("unknown")
				}
				if mode == "drift" && calls == 2 {
					return bootstrapHostDependencyFixture(unit), nil
				}
				if mode == "reactivation" && calls > 4 {
					return bootstrapActiveMultipathFixture(unit), nil
				}
				if mode == "absent" || stopped[unit] {
					return bootstrapHostDependencyFixture(unit), nil
				}
				return bootstrapActiveMultipathFixture(unit), nil
			}
			kernel := func(context.Context) error {
				kernels++
				if mode == "maps" {
					return errors.New("maps or unknown")
				}
				return nil
			}
			run := func(_ context.Context, step bootstrapHostCommand) error {
				commands = append(commands, step)
				if mode == "stop failure" {
					return errors.New("private command")
				}
				if mode == "cancel" {
					cancel()
				}
				if mode != "unproven stop" {
					unit := "multipathd.socket"
					if step == bootstrapHostDisableMultipathService {
						unit = "multipathd.service"
					}
					stopped[unit] = true
				}
				return nil
			}
			err := prepareBootstrapMultipathWith(ctx, check, read, kernel, run)
			if (err == nil) != (mode == "valid" || mode == "absent") || err != nil && err != errBootstrapSource {
				t.Fatal(err)
			}
			if mode == "valid" && (!reflect.DeepEqual(commands, []bootstrapHostCommand{bootstrapHostDisableMultipathSocket, bootstrapHostDisableMultipathService}) || kernels != 6) {
				t.Fatal(commands, kernels)
			}
			if (mode == "absent" || mode == "read failure" || mode == "maps" || mode == "drift" || mode == "authority") && len(commands) != 0 {
				t.Fatal("unproven stop", commands)
			}
			if (mode == "stop failure" || mode == "cancel") && len(commands) != 1 {
				t.Fatal("continued after failure", commands)
			}
		})
	}
}
