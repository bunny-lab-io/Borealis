package main

import (
	"bytes"
	"context"
	"strings"
)

// Complete dpkg inventory must be configured, including packages outside our
// direct dependency set. Remove only the exact approved Docker family before
// applying the existing clean-inventory rules to everything else.
func validateBootstrapConfiguredPackages(raw []byte) error {
	if len(raw) == 0 || len(raw) > bootstrapDockerInventoryLimit || raw[len(raw)-1] != '\n' {
		return errBootstrapSource
	}
	want := map[string]bool{}
	for _, name := range bootstrapHostPackages {
		want[name] = true
	}
	var required, other strings.Builder
	for _, row := range strings.Split(string(raw[:len(raw)-1]), "\n") {
		fields := strings.Split(row, "\t")
		if len(fields) != 4 {
			return errBootstrapSource
		}
		if want[fields[0]] {
			required.WriteString(row + "\n")
		}
		docker := false
		for _, name := range bootstrapHostPackages[7:] {
			docker = docker || fields[0] == name
		}
		if !docker {
			other.WriteString(row + "\n")
		}
	}
	if validateBootstrapHostDependency("packages", []byte(required.String())) != nil || validateBootstrapDockerProbe("packages", []byte(other.String())) != nil {
		return errBootstrapSource
	}
	return nil
}

// Docker must already be running and independently empty. Only the fixed
// iscsid service may be stopped; firewall must never have run (fresh filter is
// separately proved). No failed/transitional/masked/overridden unit is adopted.
func validateBootstrapRecoverableUnit(unit string, raw []byte) error {
	if unit == "docker.service" {
		return validateBootstrapHostDependency(unit, raw)
	}
	if unit != "iscsid.service" && unit != "borealis-k3s-api-firewall.service" {
		return errBootstrapSource
	}
	values, err := bootstrapHostUnitValues(raw)
	if err != nil {
		return errBootstrapSource
	}
	if unit == "iscsid.service" {
		if values["UnitFileState"] != "enabled" && values["UnitFileState"] != "disabled" {
			return errBootstrapSource
		}
		if values["ActiveState"] == "inactive" {
			if values["SubState"] != "dead" || values["MainPID"] != "0" {
				return errBootstrapSource
			}
			values["ActiveState"], values["SubState"], values["MainPID"] = "active", "running", "2"
		}
		values["UnitFileState"] = "enabled"
	} else {
		if values["ActiveState"] != "inactive" || values["SubState"] != "dead" || values["MainPID"] != "0" || values["NeedDaemonReload"] != "yes" && values["NeedDaemonReload"] != "no" {
			return errBootstrapSource
		}
		if values["LoadState"] == "not-found" {
			if values["UnitFileState"] != "" || values["FragmentPath"] != "" || values["Type"] != "simple" {
				return errBootstrapSource
			}
			values["LoadState"], values["FragmentPath"], values["Type"] = "loaded", "/etc/systemd/system/"+unit, "oneshot"
		} else if values["UnitFileState"] != "enabled" && values["UnitFileState"] != "disabled" {
			return errBootstrapSource
		}
		values["ActiveState"], values["SubState"], values["UnitFileState"], values["NeedDaemonReload"] = "active", "exited", "enabled", "no"
	}
	var normalized strings.Builder
	for key, value := range values {
		normalized.WriteString(key + "=" + value + "\n")
	}
	return validateBootstrapHostDependency(unit, []byte(normalized.String()))
}

// Read-only proof for a bounded class, not a completed host receipt. Full
// snapshots repeat; errors never mean absent and package output is not evidence
// that Docker contains no workloads.
func proveBootstrapConfiguredHostWith(ctx context.Context, check func(context.Context) error,
	read func(context.Context, string) ([]byte, error), repository, files, kernel, firewall func(context.Context) error) error {
	return proveBootstrapHostRecoveryWith(ctx, check, read, repository, files, kernel, firewall, false)
}

func proveBootstrapHostRecoveryWith(ctx context.Context, check func(context.Context) error,
	read func(context.Context, string) ([]byte, error), repository, files, kernel, firewall func(context.Context) error, firewallOnly bool) error {
	if check == nil || read == nil || repository == nil || files == nil || kernel == nil || firewall == nil {
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
		for _, prove := range []func(context.Context) error{repository, files, kernel, firewall} {
			if boundary() != nil || prove(ctx) != nil || boundary() != nil {
				return errBootstrapSource
			}
		}
		for _, probe := range []string{"architecture", "inventory", "docker.service", "iscsid.service", "borealis-k3s-api-firewall.service", "multipathd.socket", "multipathd.service", "docker-empty"} {
			if boundary() != nil {
				return errBootstrapSource
			}
			raw, err := read(ctx, probe)
			if err != nil || boundary() != nil {
				return errBootstrapSource
			}
			switch probe {
			case "architecture":
				err = validateBootstrapDockerProbe(probe, raw)
			case "inventory":
				err = validateBootstrapConfiguredPackages(raw)
			case "docker.service", "iscsid.service", "borealis-k3s-api-firewall.service":
				if !firewallOnly {
					err = validateBootstrapRecoverableUnit(probe, raw)
				} else if probe == "borealis-k3s-api-firewall.service" {
					err = validateBootstrapInterruptedFirewallUnit(raw)
				} else {
					err = validateBootstrapHostDependency(probe, raw)
				}
			case "multipathd.socket", "multipathd.service":
				if firewallOnly {
					err = validateBootstrapHostDependency(probe, raw)
				} else {
					_, err = bootstrapMultipathNeedsStop(probe, raw)
				}
			default:
				err = validateBootstrapHostDependency(probe, raw)
			}
			if err != nil || pass > 0 && !bytes.Equal(previous[probe], raw) {
				return errBootstrapSource
			}
			previous[probe] = bytes.Clone(raw)
		}
	}
	return boundary()
}

func bootstrapHostUnitValues(raw []byte) (map[string]string, error) {
	if len(raw) == 0 || len(raw) > bootstrapHostFirewallLimit || raw[len(raw)-1] != '\n' {
		return nil, errBootstrapSource
	}
	want, values := map[string]bool{}, map[string]string{}
	for _, key := range strings.Split(bootstrapHostUnitProperties, ",") {
		want[key] = true
	}
	for _, ch := range raw {
		if ch != '\n' && (ch < ' ' || ch > '~') {
			return nil, errBootstrapSource
		}
	}
	for _, row := range strings.Split(string(raw[:len(raw)-1]), "\n") {
		key, value, ok := strings.Cut(row, "=")
		if !ok || !want[key] {
			return nil, errBootstrapSource
		}
		delete(want, key)
		values[key] = value
	}
	if len(want) != 0 {
		return nil, errBootstrapSource
	}
	return values, nil
}
