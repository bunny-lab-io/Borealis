package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
)

// Socket first prevents activation while disabling the service. Every stop is
// bracketed by independent kernel proof: an existing/unknown multipath map is
// never flushed or interpreted as safe. Unit lookup failure never means absent.
func prepareBootstrapMultipathWith(ctx context.Context, check func(context.Context) error,
	read func(context.Context, string) ([]byte, error), kernel func(context.Context) error,
	run func(context.Context, bootstrapHostCommand) error) error {
	if check == nil || read == nil || kernel == nil || run == nil {
		return errBootstrapSource
	}
	boundary := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	for _, unit := range []string{"multipathd.socket", "multipathd.service"} {
		if boundary() != nil || kernel(ctx) != nil || boundary() != nil {
			return errBootstrapSource
		}
		first, err := read(ctx, unit)
		if err != nil || boundary() != nil {
			return errBootstrapSource
		}
		first = bytes.Clone(first)
		stop, err := bootstrapMultipathNeedsStop(unit, first)
		if err != nil {
			return errBootstrapSource
		}
		second, err := read(ctx, unit)
		if err != nil || !bytes.Equal(first, second) || boundary() != nil || kernel(ctx) != nil || boundary() != nil {
			return errBootstrapSource
		}
		if stop {
			step := bootstrapHostDisableMultipathSocket
			if unit == "multipathd.service" {
				step = bootstrapHostDisableMultipathService
			}
			if run(ctx, step) != nil || boundary() != nil {
				return errBootstrapSource
			}
		}
		var previous []byte
		for pass := 0; pass < 2; pass++ {
			if boundary() != nil {
				return errBootstrapSource
			}
			raw, err := read(ctx, unit)
			if err != nil || validateBootstrapHostDependency(unit, raw) != nil || boundary() != nil || pass > 0 && !bytes.Equal(previous, raw) {
				return errBootstrapSource
			}
			previous = bytes.Clone(raw)
		}
		if kernel(ctx) != nil || boundary() != nil {
			return errBootstrapSource
		}
	}
	// Do not trust disable exit status, or allow the second operation to undo
	// the first. Re-observe both exact final units twice.
	for pass := 0; pass < 2; pass++ {
		for _, unit := range []string{"multipathd.socket", "multipathd.service"} {
			if boundary() != nil {
				return errBootstrapSource
			}
			raw, err := read(ctx, unit)
			if err != nil || validateBootstrapHostDependency(unit, raw) != nil || boundary() != nil {
				return errBootstrapSource
			}
		}
	}
	return nil
}

func bootstrapMultipathNeedsStop(unit string, raw []byte) (bool, error) {
	if unit != "multipathd.service" && unit != "multipathd.socket" {
		return false, errBootstrapSource
	}
	if validateBootstrapHostDependency(unit, raw) == nil {
		return false, nil // Independently proven absent OR disabled/inactive.
	}
	if len(raw) == 0 || len(raw) > bootstrapHostFirewallLimit || raw[len(raw)-1] != '\n' {
		return false, errBootstrapSource
	}
	values := map[string]string{}
	for _, row := range strings.Split(string(raw[:len(raw)-1]), "\n") {
		key, value, ok := strings.Cut(row, "=")
		if _, exists := values[key]; !ok || exists {
			return false, errBootstrapSource
		}
		values[key] = value
	}
	if values["LoadState"] != "loaded" || values["UnitFileState"] != "enabled" && values["UnitFileState"] != "disabled" {
		return false, errBootstrapSource
	}
	if values["ActiveState"] == "active" {
		if unit == "multipathd.socket" {
			if values["SubState"] != "listening" {
				return false, errBootstrapSource
			}
		} else {
			pid, err := strconv.ParseUint(values["MainPID"], 10, 32)
			if err != nil || pid <= 1 || strconv.FormatUint(pid, 10) != values["MainPID"] || values["SubState"] != "running" ||
				(values["Type"] != "notify" && values["Type"] != "simple" && values["Type"] != "exec" && values["Type"] != "forking") {
				return false, errBootstrapSource
			}
		}
	} else if values["ActiveState"] != "inactive" || values["SubState"] != "dead" || unit == "multipathd.service" && values["MainPID"] != "0" {
		return false, errBootstrapSource
	}
	// Reuse the exact final-state validator for every other property, including
	// vendor fragment, overrides, reloads/jobs, failures and unexpected fields.
	values["UnitFileState"], values["ActiveState"], values["SubState"] = "disabled", "inactive", "dead"
	if unit == "multipathd.service" {
		values["MainPID"] = "0"
	}
	var normalized strings.Builder
	for key, value := range values {
		normalized.WriteString(key + "=" + value + "\n")
	}
	if validateBootstrapHostDependency(unit, []byte(normalized.String())) != nil {
		return false, errBootstrapSource
	}
	return true, nil
}
