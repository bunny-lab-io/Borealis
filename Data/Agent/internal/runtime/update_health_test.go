package agentruntime

import (
	"path/filepath"
	"testing"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func TestReconcileUpdateWithRoleHealthCompletesOnlyAfterHealthyRoles(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
	cfg := agentconfig.AgentConfig{}
	cfg.Agent.Update = agentconfig.AgentUpdateSection{
		OperationID: "op-health",
		Source:      "operator_initiated",
		Status:      "awaiting_health",
		DeadlineAt:  time.Now().Add(time.Minute).Unix(),
	}
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	reconcileUpdateWithRoleHealth(configPath, map[string]any{
		"roles": []map[string]any{
			{"role_id": "system:remote_shell", "role_label": "Remote Shell", "status_code": "healthy"},
			{"role_id": "system:rdp", "role_label": "Native RDP", "status_code": "unsupported"},
		},
	}, "build-42")

	loaded, err := agentconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Update.Status != "success" || loaded.Agent.Update.InstalledBuildAfter != "build-42" {
		t.Fatalf("expected healthy terminal update, got %+v", loaded.Agent.Update)
	}
	last := loaded.Agent.Update.Events[len(loaded.Agent.Update.Events)-1]
	if last.PhaseID != "update_completed" || last.TerminalStatus != "success" {
		t.Fatalf("expected terminal progress event, got %+v", last)
	}
}

func TestReconcileUpdateWithRoleHealthTimesOutUnhealthyRoles(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
	cfg := agentconfig.AgentConfig{}
	cfg.Agent.Update = agentconfig.AgentUpdateSection{
		OperationID: "op-timeout",
		Source:      "operator_initiated",
		Status:      "awaiting_health",
		DeadlineAt:  time.Now().Add(-time.Minute).Unix(),
	}
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	reconcileUpdateWithRoleHealth(configPath, map[string]any{
		"roles": []map[string]any{
			{"role_id": "system:wireguard", "role_label": "WireGuard", "status_code": "recovering", "recovery_attempts": 2},
		},
	}, "build-42")

	loaded, err := agentconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Update.Status != "timed_out" || loaded.Agent.Update.CompletedAt == 0 {
		t.Fatalf("expected timed-out update, got %+v", loaded.Agent.Update)
	}
	last := loaded.Agent.Update.Events[len(loaded.Agent.Update.Events)-1]
	if last.TerminalStatus != "timed_out" {
		t.Fatalf("expected timed-out terminal event, got %+v", last)
	}
}

func TestReconcileUpdateWithRoleHealthDoesNotCompleteBeforeUpdaterRestarts(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
	cfg := agentconfig.AgentConfig{}
	cfg.Agent.Update = agentconfig.AgentUpdateSection{
		OperationID: "op-requested",
		Source:      "operator_initiated",
		Status:      "requested",
		DeadlineAt:  time.Now().Add(time.Minute).Unix(),
	}
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	reconcileUpdateWithRoleHealth(configPath, map[string]any{
		"roles": []map[string]any{{"role_id": "system:remote_shell", "status_code": "healthy"}},
	}, "build-42")

	loaded, err := agentconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Update.Status != "requested" || len(loaded.Agent.Update.Events) != 0 {
		t.Fatalf("pre-update heartbeat completed operation: %+v", loaded.Agent.Update)
	}
}
