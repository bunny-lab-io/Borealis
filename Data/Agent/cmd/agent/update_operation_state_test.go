package main

import (
	"path/filepath"
	"testing"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func TestTerminalUpdateOperationCannotBeResurrectedByHourlyCheck(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), agentconfig.FileName)
	cfg := agentconfig.AgentConfig{}
	cfg.Agent.Update = agentconfig.AgentUpdateSection{
		OperationID: "op-complete",
		Source:      updateSourceOperator,
		Status:      "success",
		CompletedAt: 1700000000,
	}
	if err := agentconfig.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	markConfigUpdateOperation(configPath, "running", "")
	loaded, err := agentconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Update.Status != "success" || loaded.Agent.Update.CompletedAt != 1700000000 {
		t.Fatalf("terminal update was resurrected: %+v", loaded.Agent.Update)
	}
}
