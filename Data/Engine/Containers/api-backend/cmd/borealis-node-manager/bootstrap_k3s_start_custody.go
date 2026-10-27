package main

import (
	"context"
	"strconv"
	"strings"
)

func bootstrapK3sStartupAction(action string) bool {
	return action == "start_prepared_k3s" || action == "reconcile_prepared_k3s_start"
}

// Phase custody only, never a completed-effect receipt. The start invocation
// crosses systemd's fixed pre-start and notify states. Auto-restart, stopping,
// failed units and unrelated jobs remain unknown; no replay is authorized.
func validateBootstrapStartingK3s(raw string, allowAbsent bool) error {
	if len(raw) > 8192 || !strings.HasSuffix(raw, "\n") {
		return errBootstrapSource
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if _, duplicate := values[key]; !ok || duplicate {
			return errBootstrapSource
		}
		values[key] = value
	}
	if len(values) != len(strings.Split(bootstrapStartedK3sProperties, ",")) {
		return errBootstrapSource
	}
	for _, key := range strings.Split(bootstrapStartedK3sProperties, ",") {
		if _, ok := values[key]; !ok {
			return errBootstrapSource
		}
	}
	positive := func(value string) bool {
		n, err := strconv.ParseUint(value, 10, 31)
		return err == nil && n > 1 && strconv.FormatUint(n, 10) == value
	}
	job := func(value string) bool {
		n, err := strconv.ParseUint(value, 10, 32)
		return err == nil && n > 0 && strconv.FormatUint(n, 10) == value
	}
	invocation := values["InvocationID"]
	validInvocation := len(invocation) == 32 && invocation != strings.Repeat("0", 32)
	for _, b := range invocation {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			validInvocation = false
		}
	}
	switch values["ActiveState"] + "/" + values["SubState"] {
	case "inactive/dead":
		if values["MainPID"] != "0" || values["ControlPID"] != "0" || values["Job"] != "" || invocation != "" && !validInvocation {
			return errBootstrapSource
		}
	case "activating/start-pre":
		if values["MainPID"] != "0" || !positive(values["ControlPID"]) || !job(values["Job"]) || !validInvocation {
			return errBootstrapSource
		}
	case "activating/start", "active/running":
		if !positive(values["MainPID"]) || values["ControlPID"] != "0" || !validInvocation || values["Job"] != "" && !job(values["Job"]) || values["ActiveState"] == "activating" && values["Job"] == "" {
			return errBootstrapSource
		}
	default:
		return errBootstrapSource
	}
	complete := !allowAbsent || values["ActiveState"] != "inactive"
	values["ActiveState"], values["SubState"], values["MainPID"], values["ControlPID"], values["Job"] = "inactive", "dead", "0", "0", ""
	var normalized strings.Builder
	for _, key := range strings.Split(bootstrapInertK3sProperties, ",") {
		normalized.WriteString(key + "=" + values[key] + "\n")
	}
	return validateBootstrapInertK3s(normalized.String(), complete)
}

func observeBootstrapStartingK3s(ctx context.Context, allowAbsent bool) error {
	raw, err := readBootstrapStartedK3s(ctx)
	if err != nil {
		return errBootstrapSource
	}
	return validateBootstrapStartingK3s(string(raw), allowAbsent)
}
