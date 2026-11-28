package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"

	"borealis/api-backend/internal/clusterbootstrap"
)

// A canonical stopped oneshot may have exited/been interrupted during its fixed
// script. Running, queued, replaced, masked and rate-limited units are unknown.
func validateBootstrapInterruptedFirewallUnit(raw []byte) error {
	values, err := bootstrapHostUnitValues(raw)
	if err != nil {
		return errBootstrapSource
	}
	if values["MainPID"] != "0" || values["UnitFileState"] != "enabled" {
		return errBootstrapSource
	}
	switch values["ActiveState"] {
	case "inactive":
		if values["SubState"] != "dead" || values["Result"] != "success" || values["ExecMainStatus"] != "0" {
			return errBootstrapSource
		}
	case "failed":
		if values["SubState"] != "failed" {
			return errBootstrapSource
		}
		status, err := strconv.ParseUint(values["ExecMainStatus"], 10, 8)
		if err != nil || status == 0 || strconv.FormatUint(status, 10) != values["ExecMainStatus"] {
			return errBootstrapSource
		}
		switch values["Result"] {
		case "exit-code":
		case "signal":
			if status > 64 {
				return errBootstrapSource
			}
		case "timeout":
			if status != 9 && status != 15 {
				return errBootstrapSource
			}
		default:
			return errBootstrapSource
		}
	default:
		return errBootstrapSource
	}
	values["ActiveState"], values["SubState"], values["Result"], values["ExecMainStatus"] = "active", "exited", "success", "0"
	var normalized strings.Builder
	for key, value := range values {
		normalized.WriteString(key + "=" + value + "\n")
	}
	return validateBootstrapHostDependency("borealis-k3s-api-firewall.service", []byte(normalized.String()))
}

// Only exact states reachable by the existing fixed script: first an ordered
// chain prefix with no references, then the complete chain and zero-to-seven
// INPUT jumps inserted at position one in reverse construction order. Other
// table content is preserved, but cannot reference or redefine our chain.
func validateBootstrapPartialFirewall(raw []byte, expected clusterbootstrap.PreparationExpected) error {
	if expected.Validate() != nil || len(raw) == 0 || len(raw) > bootstrapHostFirewallLimit || raw[len(raw)-1] != '\n' {
		return errBootstrapSource
	}
	for _, ch := range raw {
		if ch != '\n' && (ch < ' ' || ch > '~') {
			return errBootstrapSource
		}
	}
	lines := strings.Split(string(raw[:len(raw)-1]), "\n")
	if len(lines) < 4 {
		return errBootstrapSource
	}
	for i, chain := range []string{"INPUT", "FORWARD", "OUTPUT"} {
		if lines[i] != "-P "+chain+" ACCEPT" && lines[i] != "-P "+chain+" DROP" {
			return errBootstrapSource
		}
	}
	var chain, input []string
	for _, line := range lines[3:] {
		switch {
		case line == "-N "+bootstrapHostFirewallChain || strings.HasPrefix(line, "-A "+bootstrapHostFirewallChain+" "):
			chain = append(chain, line)
		case strings.HasPrefix(line, "-A INPUT "):
			input = append(input, line)
		default:
			if strings.Contains(line, bootstrapHostFirewallChain) || !strings.HasPrefix(line, "-N ") && !strings.HasPrefix(line, "-A ") {
				return errBootstrapSource
			}
		}
	}
	wantChain, wantInput := bootstrapHostFirewallChainRules(expected), bootstrapHostFirewallInputRules()
	if len(chain) == 0 || len(chain) > len(wantChain) {
		return errBootstrapSource
	}
	for i, line := range chain {
		if line != wantChain[i] {
			return errBootstrapSource
		}
	}
	jumps := 0
	for jumps < len(input) && strings.Contains(input[jumps], bootstrapHostFirewallChain) {
		jumps++
	}
	if jumps > len(wantInput) || jumps > 0 && len(chain) != len(wantChain) {
		return errBootstrapSource
	}
	for i := 0; i < jumps; i++ {
		if input[i] != wantInput[len(wantInput)-jumps+i] {
			return errBootstrapSource
		}
	}
	for _, line := range input[jumps:] {
		if strings.Contains(line, bootstrapHostFirewallChain) {
			return errBootstrapSource
		}
	}
	return nil
}

func proveBootstrapPartialFirewallWith(ctx context.Context, expected clusterbootstrap.PreparationExpected, check func(context.Context) error, read func(context.Context, string) ([]byte, error)) error {
	if check == nil || read == nil {
		return errBootstrapSource
	}
	var previous []byte
	for pass := 0; pass < 2; pass++ {
		if ctx.Err() != nil || check(ctx) != nil {
			return errBootstrapSource
		}
		raw, err := read(ctx, "filter")
		if err != nil || ctx.Err() != nil || check(ctx) != nil || validateBootstrapPartialFirewall(raw, expected) != nil || pass > 0 && !bytes.Equal(previous, raw) {
			return errBootstrapSource
		}
		previous = bytes.Clone(raw)
	}
	return nil
}
