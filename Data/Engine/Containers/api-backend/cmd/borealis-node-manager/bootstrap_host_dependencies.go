package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Same required Ubuntu dependency family as Engine.sh. Version strings are
// observed twice, not invented pins or evidence of package origin/integrity.
var bootstrapHostPackages = [...]string{"python3", "ca-certificates", "curl", "gnupg", "iptables", "open-iscsi", "nfs-common", "docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin"}

const bootstrapHostUnitProperties = "Id,LoadState,ActiveState,SubState,Type,MainPID,ControlPID,NeedDaemonReload,Job,UnitFileState,FragmentPath,DropInPaths,Result,ExecMainStatus"
const bootstrapHostSocketProperties = "Id,LoadState,ActiveState,SubState,NeedDaemonReload,Job,UnitFileState,FragmentPath,DropInPaths,Result"

var bootstrapHostPackageVersion = regexp.MustCompile(`^[0-9][A-Za-z0-9.+:~\-]{0,199}$`)

func proveBootstrapPrejoinDependencies(ctx context.Context, check func(context.Context) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return errBootstrapSource
	}
	return proveBootstrapPrejoinDependenciesWith(ctx, check, readBootstrapHostDependency)
}

// A successful package command is insufficient: inspect every expected row,
// loaded unit state and local daemon, bracket all reads with current authority,
// then repeat the full inventory. Failure returns no partial readiness result.
func proveBootstrapPrejoinDependenciesWith(ctx context.Context, check func(context.Context) error, read func(context.Context, string) ([]byte, error)) error {
	if check == nil || read == nil {
		return errBootstrapSource
	}
	boundary := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	previous := map[string][]byte{}
	for pass := 0; pass < 2; pass++ {
		for _, probe := range []string{"packages", "docker.service", "iscsid.service", "borealis-k3s-api-firewall.service", "multipathd.service", "multipathd.socket", "docker-empty"} {
			if boundary() != nil {
				return errBootstrapSource
			}
			raw, err := read(ctx, probe)
			if err != nil || boundary() != nil || validateBootstrapHostDependency(probe, raw) != nil {
				return errBootstrapSource
			}
			if pass == 0 {
				previous[probe] = bytes.Clone(raw)
			} else if !bytes.Equal(previous[probe], raw) {
				return errBootstrapSource
			}
		}
	}
	return boundary()
}

func readBootstrapHostDependency(parent context.Context, probe string) ([]byte, error) {
	program, args := bootstrapHostDependencyCommand(probe)
	if program == "" {
		return nil, errBootstrapSource
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, program, args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.Dir = "/"
	command.WaitDelay = 250 * time.Millisecond
	output := &bootstrapHostFirewallOutput{}
	command.Stdout, command.Stderr = output, io.Discard
	if command.Run() != nil || ctx.Err() != nil {
		return nil, errBootstrapSource
	}
	return output.buffer.Bytes(), nil
}

func bootstrapHostDependencyCommand(probe string) (string, []string) {
	switch probe {
	case "packages":
		args := []string{"--show", "--showformat=${Package}\t${Architecture}\t${Status}\t${Version}\n", "--"}
		return "/usr/bin/dpkg-query", append(args, bootstrapHostPackages[:]...)
	case "docker.service", "iscsid.service", "borealis-k3s-api-firewall.service", "multipathd.service", "multipathd.socket":
		properties := bootstrapHostUnitProperties
		if probe == "multipathd.socket" {
			properties = bootstrapHostSocketProperties
		}
		return "/usr/bin/systemctl", []string{"show", "--no-pager", "--property=" + properties, "--", probe}
	case "docker-empty":
		// Explicit local socket and fixed environment prevent ambient contexts
		// or DOCKER_HOST/DOCKER_CONTEXT from selecting another daemon.
		return "/usr/bin/docker", []string{"--host", "unix:///run/docker.sock", "info", "--format", "{{.Containers}} {{.ContainersRunning}} {{.ContainersPaused}} {{.ContainersStopped}}"}
	default:
		return "", nil
	}
}

func validateBootstrapHostDependency(probe string, raw []byte) error {
	if len(raw) == 0 || len(raw) > bootstrapHostFirewallLimit || raw[len(raw)-1] != '\n' {
		return errBootstrapSource
	}
	for _, value := range raw {
		if value != '\n' && value != '\t' && (value < ' ' || value > '~') {
			return errBootstrapSource
		}
	}
	if probe == "docker-empty" {
		if string(raw) != "0 0 0 0\n" {
			return errBootstrapSource
		}
		return nil
	}
	if probe == "packages" {
		want := map[string]bool{}
		for _, name := range bootstrapHostPackages {
			want[name] = true
		}
		for _, line := range strings.Split(string(raw[:len(raw)-1]), "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) != 4 || !want[fields[0]] || fields[1] != "amd64" && fields[1] != "all" || fields[2] != "install ok installed" || !bootstrapHostPackageVersion.MatchString(fields[3]) {
				return errBootstrapSource
			}
			delete(want, fields[0])
		}
		if len(want) != 0 {
			return errBootstrapSource
		}
		return nil
	}
	properties := map[string]string{}
	want := map[string]bool{}
	keys := bootstrapHostUnitProperties
	if probe == "multipathd.socket" {
		keys = bootstrapHostSocketProperties
	}
	for _, key := range strings.Split(keys, ",") {
		want[key] = true
	}
	for _, line := range strings.Split(string(raw[:len(raw)-1]), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || !want[key] || strings.ContainsRune(value, '\t') {
			return errBootstrapSource
		}
		delete(want, key)
		properties[key] = value
	}
	if len(want) != 0 || properties["Id"] != probe || properties["NeedDaemonReload"] != "no" || properties["Job"] != "" || properties["DropInPaths"] != "" || properties["Result"] != "success" {
		return errBootstrapSource
	}
	if probe != "multipathd.socket" && (properties["ControlPID"] != "0" || properties["ExecMainStatus"] != "0") {
		return errBootstrapSource
	}
	switch probe {
	case "multipathd.service", "multipathd.socket":
		// No service/socket activation or retained multipath configuration is
		// interpreted as safe. Kernel maps are a separate mandatory observation.
		if properties["ActiveState"] != "inactive" || properties["SubState"] != "dead" || probe != "multipathd.socket" && properties["MainPID"] != "0" {
			return errBootstrapSource
		}
		if properties["LoadState"] == "not-found" {
			if properties["UnitFileState"] != "" || properties["FragmentPath"] != "" {
				return errBootstrapSource
			}
		} else if properties["LoadState"] != "loaded" || properties["UnitFileState"] != "disabled" || !bootstrapHostVendorUnit(properties["FragmentPath"], probe) {
			return errBootstrapSource
		}
	case "docker.service", "iscsid.service", "borealis-k3s-api-firewall.service":
		if properties["LoadState"] != "loaded" || properties["ActiveState"] != "active" || properties["UnitFileState"] != "enabled" {
			return errBootstrapSource
		}
		if probe == "borealis-k3s-api-firewall.service" {
			if properties["SubState"] != "exited" || properties["Type"] != "oneshot" || properties["MainPID"] != "0" || properties["FragmentPath"] != "/etc/systemd/system/"+probe {
				return errBootstrapSource
			}
		} else {
			pid, err := strconv.ParseUint(properties["MainPID"], 10, 32)
			if err != nil || pid <= 1 || strconv.FormatUint(pid, 10) != properties["MainPID"] || properties["SubState"] != "running" || !bootstrapHostVendorUnit(properties["FragmentPath"], probe) {
				return errBootstrapSource
			}
			switch properties["Type"] {
			case "simple", "exec", "forking", "notify", "notify-reload":
			default:
				return errBootstrapSource
			}
		}
	default:
		return errBootstrapSource
	}
	return nil
}

func bootstrapHostVendorUnit(path, unit string) bool {
	return path == "/usr/lib/systemd/system/"+unit || path == "/lib/systemd/system/"+unit
}
