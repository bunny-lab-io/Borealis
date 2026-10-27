package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const bootstrapStartedK3sProperties = bootstrapInertK3sProperties + ",InvocationID"

// Completion requires the exact still-disabled unit. Starting this unit does
// not authorize boot enablement, replacement, a restart, or another unit.
func parseBootstrapStartedK3s(raw string) (sourceKubeletService, error) {
	fail := func() (sourceKubeletService, error) { return sourceKubeletService{}, errBootstrapSource }
	if len(raw) > 8192 || !strings.HasSuffix(raw, "\n") {
		return fail()
	}
	values := map[string]string{}
	want := strings.Split(bootstrapStartedK3sProperties, ",")
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if _, duplicate := values[key]; !ok || duplicate {
			return fail()
		}
		values[key] = value
	}
	if len(values) != len(want) {
		return fail()
	}
	for _, key := range want {
		if _, ok := values[key]; !ok {
			return fail()
		}
	}
	service, err := parseSourceKubeletService([]byte("Id=" + values["Id"] + "\nMainPID=" + values["MainPID"] + "\nInvocationID=" + values["InvocationID"] + "\nActiveState=" + values["ActiveState"] + "\nSubState=" + values["SubState"] + "\n"))
	if err != nil {
		return fail()
	}
	// Reuse the exact loaded-file/disabled/no-job contract, changing only the
	// three fields that an acknowledged first start is allowed to change.
	values["ActiveState"], values["SubState"], values["MainPID"] = "inactive", "dead", "0"
	var inert strings.Builder
	for _, key := range strings.Split(bootstrapInertK3sProperties, ",") {
		inert.WriteString(key + "=" + values[key] + "\n")
	}
	if validateBootstrapInertK3s(inert.String(), true) != nil {
		return fail()
	}
	return service, nil
}

func readBootstrapStartedK3s(ctx context.Context) ([]byte, error) {
	return sourceLinkCommand(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property="+bootstrapStartedK3sProperties, "--", "k3s.service")
}

// Hold the observed executable and proc directory through the consumer. Unlike
// sourceKubeletProcess this requires no CSI socket: a drained joining member has
// not yet been admitted to storage. No process arguments or environment are read.
func withBootstrapStartedK3sProcess(ctx context.Context, proc string, read func(context.Context) ([]byte, error),
	executableCheck func(context.Context, *os.File) error, check func(context.Context) error, consume func(func(context.Context) error) error) error {
	if read == nil || executableCheck == nil || check == nil || consume == nil || ctx.Err() != nil || check(ctx) != nil {
		return errBootstrapSource
	}
	raw, err := read(ctx)
	service, parseErr := parseBootstrapStartedK3s(string(raw))
	if err != nil || parseErr != nil {
		return errBootstrapSource
	}
	pidPath := filepath.Join(proc, strconv.FormatUint(service.pid, 10))
	process, err := os.OpenRoot(pidPath)
	if err != nil {
		return errBootstrapSource
	}
	defer process.Close()
	processInfo, err := os.Stat(pidPath)
	if err != nil {
		return errBootstrapSource
	}
	stat, err := sourceKubeletRead(process, "stat", 4096)
	start, startErr := sourceKubeletStart(stat, service.pid)
	if err != nil || startErr != nil {
		return errBootstrapSource
	}
	host := map[string]os.FileInfo{}
	for _, name := range []string{"root", "ns/net", "ns/mnt"} {
		info, err := os.Stat(filepath.Join(proc, "1", name))
		if err != nil {
			return errBootstrapSource
		}
		host[name] = info
	}
	executable, err := os.Open(filepath.Join(pidPath, "exe"))
	if err != nil {
		return errBootstrapSource
	}
	defer executable.Close()
	exe, err := executable.Stat()
	if err != nil || !sourceKubeletExecutableUnchanged(exe, exe) || executableCheck(ctx, executable) != nil {
		return errBootstrapSource
	}
	active, failed := true, false
	defer func() { active = false }()
	refresh := func(ctx context.Context) (result error) {
		defer func() {
			if result != nil {
				failed = true
			}
		}()
		if !active || failed || ctx.Err() != nil || check(ctx) != nil {
			return errBootstrapSource
		}
		currentProcess, processErr := os.Stat(pidPath)
		if processErr != nil || !os.SameFile(processInfo, currentProcess) {
			return errBootstrapSource
		}
		raw, err := read(ctx)
		current, parseErr := parseBootstrapStartedK3s(string(raw))
		if err != nil || parseErr != nil || current != service {
			return errBootstrapSource
		}
		stat, err := sourceKubeletRead(process, "stat", 4096)
		ticks, tickErr := sourceKubeletStart(stat, service.pid)
		status, statusErr := sourceKubeletRead(process, "status", 64<<10)
		if err != nil || tickErr != nil || ticks != start || statusErr != nil || !sourceKubeletRootUID(status) {
			return errBootstrapSource
		}
		for name, original := range host {
			a, err := os.Stat(filepath.Join(pidPath, name))
			b, other := os.Stat(filepath.Join(proc, "1", name))
			if err != nil || other != nil || !os.SameFile(original, a) || !os.SameFile(original, b) {
				return errBootstrapSource
			}
		}
		currentExe, err := os.Stat(filepath.Join(pidPath, "exe"))
		heldExe, heldErr := executable.Stat()
		if err != nil || heldErr != nil || !sourceKubeletExecutableUnchanged(exe, currentExe) || !sourceKubeletExecutableUnchanged(exe, heldExe) || ctx.Err() != nil || check(ctx) != nil {
			return errBootstrapSource
		}
		return nil
	}
	if refresh(ctx) != nil || consume(refresh) != nil || refresh(ctx) != nil {
		return errBootstrapSource
	}
	return nil
}
