package agentruntime

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
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
	if err := os.WriteFile(filepath.Join(root, "agent.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := cleanupStartupTemp(filepath.Join(root, "agent.json"), log.New(os.Stdout, "", 0)); err != nil {
		t.Fatalf("cleanupStartupTemp returned error: %v", err)
	}
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Fatalf("Temp still exists or stat failed with unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agent.json")); err != nil {
		t.Fatalf("agent.json was touched: %v", err)
	}
}

func TestCleanupStartupTempNoopsWhenMissing(t *testing.T) {
	root := t.TempDir()
	if err := cleanupStartupTemp(filepath.Join(root, "agent.json"), nil); err != nil {
		t.Fatalf("cleanupStartupTemp returned error: %v", err)
	}
}

func TestHeartbeatIntervalUsesRapidHealthJitterWindow(t *testing.T) {
	first := heartbeatIntervalWithJitter(time.Unix(0, 0))
	second := heartbeatIntervalWithJitter(time.Unix(0, int64(4_999*time.Millisecond)))
	if first < 20*time.Second || first >= 25*time.Second {
		t.Fatalf("heartbeat interval outside jitter window: %s", first)
	}
	if second < 20*time.Second || second >= 25*time.Second {
		t.Fatalf("heartbeat interval outside jitter window: %s", second)
	}
	if first == second {
		t.Fatalf("heartbeat interval should jitter, both were %s", first)
	}
}

func TestAgentLivenessWriteUpdatesAgentJSON(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, agentconfig.FileName)
	cfg := agentconfig.Default()
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}
	agent := &Agent{configPath: configPath, bootID: "boot-test", logger: log.New(os.Stdout, "", 0)}
	agent.updateLiveness(func(l *agentconfig.AgentLivenessSection) {
		l.PID = 99
		l.BootID = agent.bootID
		l.LastSocketState = "connected"
	})
	loaded, err := agentconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Liveness.PID != 99 || loaded.Agent.Liveness.BootID != "boot-test" || loaded.Agent.Liveness.LastSocketState != "connected" {
		t.Fatalf("liveness not written: %#v", loaded.Agent.Liveness)
	}
}

func TestRecoveryLogWritesRoleRecoveryLog(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, agentconfig.FileName)
	cfg := agentconfig.Default()
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}
	agent := &Agent{configPath: configPath}
	agent.logRecovery("role_supervisor", "system:vnc", "restart", "success", "test", nil)
	raw, err := os.ReadFile(filepath.Join(root, "Logs", "Agent", "role_recovery.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "component=role_supervisor") || !strings.Contains(text, "role_id=system:vnc") || !strings.Contains(text, "action=restart") {
		t.Fatalf("role recovery log missing fields: %s", text)
	}
}

func TestRoleSupervisorAddsSourceOfTruthFields(t *testing.T) {
	events := []string{}
	supervisor := NewRoleSupervisor(func(component string, roleID string, action string, outcome string, reason string, err error) {
		events = append(events, component+"|"+roleID+"|"+action+"|"+outcome+"|"+reason)
	})
	first := supervisor.Update([]RoleSnapshot{
		{
			RoleID:     "system:vnc",
			RoleName:   "vnc",
			RoleLabel:  "UltraVNC Service",
			Context:    "system",
			Status:     "Recovering",
			StatusCode: "recovering",
			Detail:     "listener warming",
			Details:    map[string]any{"runtime": "go"},
			CheckedAt:  100,
		},
	})
	roles := first["roles"].([]map[string]any)
	if roles[0]["desired_state"] != "running" || roles[0]["observed_state"] != "recovering" || roles[0]["recovery_attempts"].(int) != 1 {
		t.Fatalf("supervised role fields missing: %#v", roles[0])
	}
	details := roles[0]["details"].(map[string]any)
	if details["desired_state"] != "running" || details["observed_state"] != "recovering" {
		t.Fatalf("supervised detail fields missing: %#v", details)
	}
	second := supervisor.Update([]RoleSnapshot{
		{
			RoleID:     "system:vnc",
			RoleName:   "vnc",
			RoleLabel:  "UltraVNC Service",
			Context:    "system",
			Status:     "Healthy",
			StatusCode: "healthy",
			Detail:     "listener ready",
			CheckedAt:  101,
		},
	})
	roles = second["roles"].([]map[string]any)
	if roles[0]["last_success_at"].(int64) <= 0 || roles[0]["recovery_attempts"].(int) != 0 {
		t.Fatalf("healthy role did not reset recovery metadata: %#v", roles[0])
	}
	if len(events) == 0 {
		t.Fatalf("expected recovery log events")
	}
}

func TestRoleSupervisorSchedulesRecoveryHandlerWithCooldown(t *testing.T) {
	recovered := make(chan RoleSnapshot, 2)
	supervisor := NewRoleSupervisor(nil)
	supervisor.RegisterRecoveryHandler("system:vnc", func(snapshot RoleSnapshot) {
		recovered <- snapshot
	})
	snapshot := RoleSnapshot{
		RoleID:     "system:vnc",
		RoleName:   "vnc",
		RoleLabel:  "UltraVNC Service",
		Context:    "system",
		Status:     "Recovering",
		StatusCode: "recovering",
		Detail:     "listener warming",
		CheckedAt:  time.Now().Unix(),
	}
	supervisor.Update([]RoleSnapshot{snapshot})
	select {
	case got := <-recovered:
		if got.RoleID != "system:vnc" {
			t.Fatalf("recovered role = %s, want system:vnc", got.RoleID)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for recovery handler")
	}
	supervisor.Update([]RoleSnapshot{snapshot})
	select {
	case <-recovered:
		t.Fatalf("recovery handler bypassed cooldown")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRoleRecoveryLogSuppressesRepeatedHealthChecks(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, agentconfig.FileName)
	cfg := agentconfig.Default()
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}
	agent := &Agent{configPath: configPath}
	role := map[string]any{
		"role_id":     "system:vnc",
		"status_code": "recovering",
		"detail":      "BorealisAgentUltraVNC is STOP_PENDING.",
	}
	agent.recordOneRoleHealth(role)
	agent.recordOneRoleHealth(role)

	raw, err := os.ReadFile(filepath.Join(root, "Logs", "Agent", "role_recovery.log"))
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(raw), "action=health_check"); count != 1 {
		t.Fatalf("health_check count = %d, want 1; log=%s", count, string(raw))
	}

	role["detail"] = "BorealisAgentUltraVNC is STOP_PENDING; listener is not_listening."
	agent.recordOneRoleHealth(role)
	raw, err = os.ReadFile(filepath.Join(root, "Logs", "Agent", "role_recovery.log"))
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(raw), "action=health_check"); count != 2 {
		t.Fatalf("health_check count after detail change = %d, want 2; log=%s", count, string(raw))
	}
}

func TestWatchdogDecisionMatrix(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		input watchdogDecisionInput
		want  watchdogDecision
	}{
		{
			name:  "missing service",
			input: watchdogDecisionInput{Now: now},
			want:  watchdogDecision{Action: "repair_service", Outcome: "missing", Reason: "service_missing"},
		},
		{
			name:  "stopped service",
			input: watchdogDecisionInput{ServiceExists: true, Now: now},
			want:  watchdogDecision{Action: "start_service", Outcome: "needed", Reason: "service_stopped"},
		},
		{
			name:  "unknown liveness",
			input: watchdogDecisionInput{ServiceExists: true, ServiceRunning: true, Now: now},
			want:  watchdogDecision{Action: "check_liveness", Outcome: "unknown", Reason: "no_local_tick"},
		},
		{
			name:  "healthy liveness",
			input: watchdogDecisionInput{ServiceExists: true, ServiceRunning: true, LastLocalTickAt: now.Add(-60 * time.Second).Unix(), Now: now},
			want:  watchdogDecision{Action: "check_liveness", Outcome: "healthy", Reason: "age=1m0s"},
		},
		{
			name:  "pid mismatch restart",
			input: watchdogDecisionInput{ServiceExists: true, ServiceRunning: true, ServicePID: 20, LivenessPID: 10, LastLocalTickAt: now.Add(-30 * time.Second).Unix(), Now: now},
			want:  watchdogDecision{Action: "restart_service", Outcome: "needed", Reason: "pid_mismatch_service=20_liveness=10"},
		},
		{
			name:  "socket disconnected restart",
			input: watchdogDecisionInput{ServiceExists: true, ServiceRunning: true, LastLocalTickAt: now.Add(-30 * time.Second).Unix(), LastSocketState: "disconnected", LastSocketStateAt: now.Add(-181 * time.Second).Unix(), Now: now},
			want:  watchdogDecision{Action: "restart_service", Outcome: "needed", Reason: "socket_disconnected_age=3m1s"},
		},
		{
			name:  "socket connecting restart",
			input: watchdogDecisionInput{ServiceExists: true, ServiceRunning: true, LastLocalTickAt: now.Add(-30 * time.Second).Unix(), LastSocketState: "connecting", LastSocketStateAt: now.Add(-181 * time.Second).Unix(), Now: now},
			want:  watchdogDecision{Action: "restart_service", Outcome: "needed", Reason: "socket_connecting_age=3m1s"},
		},
		{
			name:  "stale heartbeat restart",
			input: watchdogDecisionInput{ServiceExists: true, ServiceRunning: true, LastLocalTickAt: now.Add(-30 * time.Second).Unix(), LastHeartbeatSuccessAt: now.Add(-241 * time.Second).Unix(), Now: now},
			want:  watchdogDecision{Action: "restart_service", Outcome: "needed", Reason: "stale_heartbeat_success_age=4m1s"},
		},
		{
			name:  "stale liveness update active",
			input: watchdogDecisionInput{ServiceExists: true, ServiceRunning: true, LastLocalTickAt: now.Add(-181 * time.Second).Unix(), Now: now, UpdateActive: true},
			want:  watchdogDecision{Action: "check_liveness", Outcome: "skipped", Reason: "update_active"},
		},
		{
			name:  "stale liveness update expired",
			input: watchdogDecisionInput{ServiceExists: true, ServiceRunning: true, LastLocalTickAt: now.Add(-181 * time.Second).Unix(), Now: now, UpdateActive: true, UpdateExpired: true},
			want:  watchdogDecision{Action: "restart_service", Outcome: "needed", Reason: "stale_liveness_age=3m1s"},
		},
		{
			name:  "stale liveness restart",
			input: watchdogDecisionInput{ServiceExists: true, ServiceRunning: true, LastLocalTickAt: now.Add(-181 * time.Second).Unix(), Now: now},
			want:  watchdogDecision{Action: "restart_service", Outcome: "needed", Reason: "stale_liveness_age=3m1s"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := decideWatchdogRecovery(test.input)
			if got != test.want {
				t.Fatalf("decision = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestEngineSocketRoleSnapshot(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC).Unix()
	agent := &Agent{}
	agent.socketState = "connecting"
	agent.socketStateAt = now - int64((watchdogSocketStaleAfter + time.Second).Seconds())
	stale := agent.engineSocketRoleSnapshot(now)
	if stale.Status != "unhealthy" || stale.StatusCode != "unhealthy" {
		t.Fatalf("stale socket status = %s/%s, want unhealthy", stale.Status, stale.StatusCode)
	}
	if stale.Detail != "Engine Socket.IO control channel is stuck connecting." {
		t.Fatalf("stale socket detail = %q", stale.Detail)
	}

	agent.socketState = "connected"
	agent.socketStateAt = now
	healthy := agent.engineSocketRoleSnapshot(now)
	if healthy.Status != "healthy" || healthy.StatusCode != "healthy" {
		t.Fatalf("connected socket status = %s/%s, want healthy", healthy.Status, healthy.StatusCode)
	}
}
