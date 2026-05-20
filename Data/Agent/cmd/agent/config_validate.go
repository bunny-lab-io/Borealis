package main

import (
	"fmt"
	"strings"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
)

func validateAgentConfig(configPath string) error {
	if strings.TrimSpace(configPath) == "" {
		return fmt.Errorf("config path missing")
	}
	cfg, err := agentconfig.Load(configPath)
	if err != nil {
		return err
	}
	if cfg.SchemaVersion > agentconfig.SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d; max supported %d", cfg.SchemaVersion, agentconfig.SchemaVersion)
	}
	return nil
}
