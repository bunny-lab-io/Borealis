package main

import (
	"strings"
	"time"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func markConfigUpdateOperation(configPath string, status string, detail string) {
	status = strings.ToLower(strings.TrimSpace(status))
	if strings.TrimSpace(configPath) == "" || status == "" {
		return
	}
	_ = agentconfig.UpdateWithWriter(configPath, "updater:operation", func(cfg *agentconfig.AgentConfig) {
		if strings.TrimSpace(cfg.Agent.Update.OperationID) == "" {
			return
		}
		if status != "success" && status != "failed" && status != "timed_out" && !updateOperationIsActive(cfg.Agent.Update) {
			return
		}
		now := time.Now().Unix()
		cfg.Agent.Update.Status = status
		cfg.Agent.Update.UpdatedAt = now
		cfg.Agent.Update.LastError = strings.TrimSpace(detail)
		if status == "success" || status == "failed" || status == "timed_out" {
			cfg.Agent.Update.CompletedAt = now
		}
	})
}

func markConfigUpdateOperationSuccess(configPath string) {
	if strings.TrimSpace(configPath) == "" {
		return
	}
	cfg, err := agentconfig.Load(configPath)
	if err == nil {
		switch strings.ToLower(strings.TrimSpace(cfg.Agent.Update.Status)) {
		case "staging", "restarting", "awaiting_reconnect", "awaiting_health":
			return
		}
	}
	markConfigUpdateOperation(configPath, "awaiting_health", "")
}
