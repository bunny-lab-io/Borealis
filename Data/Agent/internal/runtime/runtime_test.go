package agentruntime

import (
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestStartupMilestonesSteadyStateComplete(t *testing.T) {
	milestones := startupMilestones("steady_state_online", "healthy", "ready", 123)
	if len(milestones) != len(startupMilestoneDefinitions) {
		t.Fatalf("milestone count = %d, want %d", len(milestones), len(startupMilestoneDefinitions))
	}
	for _, milestone := range milestones {
		if milestone["state"] != "complete" {
			t.Fatalf("milestone %v state = %v, want complete", milestone["key"], milestone["state"])
		}
		if milestone["completed_at"] != int64(123) {
			t.Fatalf("milestone %v missing completed_at: %#v", milestone["key"], milestone)
		}
	}
}

func TestStartupMilestonesSocketConnecting(t *testing.T) {
	milestones := startupMilestones("socket_connecting", "healthy", "connecting", 456)
	byKey := map[string]map[string]any{}
	for _, milestone := range milestones {
		byKey[milestone["key"].(string)] = milestone
	}
	if byKey["status_channel_online"]["state"] != "complete" {
		t.Fatalf("status_channel_online state = %v", byKey["status_channel_online"]["state"])
	}
	if byKey["socket_connecting"]["state"] != "active" {
		t.Fatalf("socket_connecting state = %v", byKey["socket_connecting"]["state"])
	}
	if byKey["socket_connected"]["state"] != "pending" {
		t.Fatalf("socket_connected state = %v", byKey["socket_connected"]["state"])
	}
}

func TestStartupMilestonesFailureMarksPhaseFailed(t *testing.T) {
	milestones := startupMilestones("authenticating", "unhealthy", "bad token", 789)
	byKey := map[string]map[string]any{}
	for _, milestone := range milestones {
		byKey[milestone["key"].(string)] = milestone
	}
	if byKey["authenticating"]["state"] != "failed" {
		t.Fatalf("authenticating state = %v", byKey["authenticating"]["state"])
	}
	if byKey["authenticating"]["detail"] != "bad token" {
		t.Fatalf("authenticating detail = %v", byKey["authenticating"]["detail"])
	}
	if byKey["authenticated"]["state"] != "pending" {
		t.Fatalf("authenticated state = %v", byKey["authenticated"]["state"])
	}
}

func TestCleanupStartupTempRemovesOnlyAgentTemp(t *testing.T) {
	root := t.TempDir()
	tempDir := filepath.Join(root, "Temp")
	if err := os.MkdirAll(filepath.Join(tempDir, "Onboarding"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "Onboarding", "state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := cleanupStartupTemp(filepath.Join(root, "config.json"), log.New(os.Stdout, "", 0)); err != nil {
		t.Fatalf("cleanupStartupTemp returned error: %v", err)
	}
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Fatalf("Temp still exists or stat failed with unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "config.json")); err != nil {
		t.Fatalf("config.json was touched: %v", err)
	}
}

func TestCleanupStartupTempNoopsWhenMissing(t *testing.T) {
	root := t.TempDir()
	if err := cleanupStartupTemp(filepath.Join(root, "config.json"), nil); err != nil {
		t.Fatalf("cleanupStartupTemp returned error: %v", err)
	}
}
