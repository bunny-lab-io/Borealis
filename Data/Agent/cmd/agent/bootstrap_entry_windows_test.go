//go:build windows

package main

import "testing"

func TestRuntimeFlagsUseServiceAndWatchdog(t *testing.T) {
	if !hasRuntimeFlag([]string{"--service"}) {
		t.Fatal("--service should bypass bootstrap")
	}
	if !hasRuntimeFlag([]string{"--watchdog-check"}) {
		t.Fatal("--watchdog-check should bypass bootstrap")
	}
	if !hasRuntimeFlag([]string{"--reconcile-update"}) {
		t.Fatal("--reconcile-update should bypass bootstrap")
	}
	if hasRuntimeFlag([]string{"--system-service"}) {
		t.Fatal("--system-service should not be accepted")
	}
}

func TestExplicitDeployIntentForcesRedeployInsteadOfHealthySkip(t *testing.T) {
	cfg := BootstrapConfig{
		ServerURL:          "https://borealis.example.com",
		SiteEnrollmentCode: "CODE",
		DeployIntent:       true,
	}
	health := InstallHealth{
		Exists:         true,
		AgentExeExists: true,
		ServiceExists:  true,
		ServiceRunning: true,
		EngineValid:    true,
	}
	if action := decideBootstrapAction(cfg, health, nil); action != actionDeploy {
		t.Fatalf("action = %s, want %s", action, actionDeploy)
	}
}
