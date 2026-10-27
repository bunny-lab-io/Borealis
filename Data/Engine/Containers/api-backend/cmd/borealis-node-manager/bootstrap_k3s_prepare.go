package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func bootstrapK3sAction(action string) bool {
	return action == "install_prejoin_k3s" || action == "reconcile_prejoin_k3s"
}

func bootstrapNativeK3sActions(expected clusterbootstrap.PreparationExpected, guard *clusterbootstrap.ExecutorGuard, executor clusterbootstrap.ExecutorIdentity, check func(context.Context) error) clusterbootstrap.PrejoinK3sActions {
	withRoot := func(ctx context.Context, run func(*os.Root) error) error {
		if os.Geteuid() != 0 || os.Getegid() != 0 || check == nil || ctx.Err() != nil || check(ctx) != nil {
			return errBootstrapSource
		}
		root, err := os.OpenRoot("/")
		if err != nil {
			return errBootstrapSource
		}
		defer root.Close()
		if run(root) != nil || ctx.Err() != nil || check(ctx) != nil {
			return errBootstrapSource
		}
		return nil
	}
	return clusterbootstrap.PrejoinK3sActions{
		CheckFresh: func(ctx context.Context) error {
			return withRoot(ctx, func(root *os.Root) error {
				if verifyBootstrapK3sFilePrefix(root, expected, false, true) != nil || observeBootstrapAbsentUnit(ctx, "k3s.service") != nil {
					return errBootstrapSource
				}
				return nil
			})
		},
		Install: func(ctx context.Context, hostLock, identityLock *os.File) error {
			return withRoot(ctx, func(root *os.Root) error {
				if observeBootstrapAbsentUnit(ctx, "k3s.service") != nil || installBootstrapPrejoinK3sFilesWith(ctx, root, expected, check) != nil {
					return errBootstrapSource
				}
				return runBootstrapHostCommand(ctx, bootstrapHostReloadUnits, hostLock, identityLock, guard, executor, check)
			})
		},
		Prove: func(ctx context.Context) error {
			return withRoot(ctx, func(root *os.Root) error {
				for pass := 0; pass < 2; pass++ {
					if check(ctx) != nil || verifyBootstrapK3sFilePrefix(root, expected, true, false) != nil || observeBootstrapInertK3s(ctx, true) != nil {
						return errBootstrapSource
					}
				}
				return verifyBootstrapK3sFilePrefix(root, expected, true, false)
			})
		},
	}
}

const bootstrapInertK3sProperties = "Id,Names,LoadState,ActiveState,SubState,MainPID,ControlPID,NeedDaemonReload,Job,UnitFileState,FragmentPath,SourcePath,DropInPaths,Transient,Type,Result,ExecMainStatus"

func observeBootstrapInertK3s(parent context.Context, complete bool) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property="+bootstrapInertK3sProperties, "--", "k3s.service")
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.WaitDelay = 250 * time.Millisecond
	output := &identityConsumerOutput{}
	command.Stdout, command.Stderr = output, io.Discard
	if command.Run() != nil || ctx.Err() != nil {
		return errBootstrapSource
	}
	return validateBootstrapInertK3s(output.buffer.String(), complete)
}

// Partial observations permit only inactive known prefixes during no-replace
// publication/reload. They never attest completion or authorize restart/replay.
func validateBootstrapInertK3s(raw string, complete bool) error {
	if len(raw) > 8192 || !strings.HasSuffix(raw, "\n") {
		return errBootstrapSource
	}
	values := map[string]string{}
	want := map[string]bool{}
	for _, key := range strings.Split(bootstrapInertK3sProperties, ",") {
		want[key] = true
	}
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || !want[key] {
			return errBootstrapSource
		}
		delete(want, key)
		values[key] = value
	}
	if len(want) != 0 {
		return errBootstrapSource
	}
	for key, value := range map[string]string{"Id": "k3s.service", "Names": "k3s.service", "ActiveState": "inactive", "SubState": "dead", "MainPID": "0", "ControlPID": "0", "Job": "", "SourcePath": "", "DropInPaths": "", "Transient": "no", "Result": "success", "ExecMainStatus": "0"} {
		if values[key] != value {
			return errBootstrapSource
		}
	}
	if values["NeedDaemonReload"] != "no" && (complete || values["NeedDaemonReload"] != "yes") {
		return errBootstrapSource
	}
	switch values["LoadState"] {
	case "loaded":
		if values["UnitFileState"] != "disabled" || values["FragmentPath"] != "/etc/systemd/system/k3s.service" || values["Type"] != "notify" {
			return errBootstrapSource
		}
	case "not-found":
		if complete || values["UnitFileState"] != "" && values["UnitFileState"] != "disabled" || values["FragmentPath"] != "" || values["Type"] != "simple" {
			return errBootstrapSource
		}
	default:
		return errBootstrapSource
	}
	return nil
}
