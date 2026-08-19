//go:build windows

package main

import (
	"strings"
	"testing"
)

func TestDeferredReplacementScriptRunsValidationThroughExeShim(t *testing.T) {
	script := deferredReplacementScript(
		BootstrapConfig{InstallDir: `C:\Borealis`},
		`C:\Borealis\Agent.exe.update`,
		`C:\Borealis\Agent.exe`,
		"build-id",
		"sha256",
	)
	if !strings.Contains(script, `$validateExe = $pending + ".validate.exe"`) {
		t.Fatalf("deferred script does not create executable validation shim")
	}
	if strings.Contains(script, `& $pending --validate-config`) {
		t.Fatalf("deferred script still executes .update file directly")
	}
	if !strings.Contains(script, "Mark-AgentUpdateFailed") || !strings.Contains(script, "Ensure-AgentServiceRunning") {
		t.Fatalf("deferred script does not mark failed update and restart service on failure")
	}
	if !strings.Contains(script, "--reconcile-update --config-path") || !strings.Contains(script, "reconcile-update:") {
		t.Fatalf("deferred script does not reconcile mutable host state after replacement")
	}
}

func TestDeferredRedeployReplacementScriptDoesNotRequireUpdateFinalize(t *testing.T) {
	script := deferredRedeployReplacementScript(
		BootstrapConfig{InstallDir: `C:\Borealis`},
		`C:\Borealis\Agent.exe.redeploy`,
		`C:\Borealis\Agent.exe`,
		"sha256",
	)
	for _, want := range []string{
		"Deferred redeploy replacement starting",
		"Invoke-PendingAgentValidation",
		"Ensure-AgentServiceRunning",
		"--install-service --config-path",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("deferred redeploy script missing %q", want)
		}
	}
	if strings.Contains(script, "--finalize-update") || strings.Contains(script, "Mark-AgentUpdateFailed") {
		t.Fatalf("redeploy script should not use update finalization state")
	}
}
