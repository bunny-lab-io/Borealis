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
}
