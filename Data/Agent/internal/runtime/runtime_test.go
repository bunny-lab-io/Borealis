package agentruntime

import "testing"

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
