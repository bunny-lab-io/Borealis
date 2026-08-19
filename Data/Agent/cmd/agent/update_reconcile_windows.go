//go:build windows

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

func runPostUpdateReconciliation(options agentruntime.Options) error {
	configPath := strings.TrimSpace(options.ConfigPath)
	if configPath == "" {
		var err error
		configPath, err = agentconfig.PathFromBinary()
		if err != nil {
			return err
		}
	}
	current, err := agentconfig.Load(configPath)
	if err != nil {
		return fmt.Errorf("load Agent config: %w", err)
	}
	cfg := defaultBootstrapConfig()
	cfg.InstallDir = filepath.Dir(configPath)
	cfg.ServerURL = current.ServerURL
	cfg.ServerIPFallback = current.ServerIPFallback
	cfg.Verbose = options.Verbose
	logger, closeLog, err := openBootstrapLogger(cfg, false)
	if err != nil {
		return err
	}
	defer closeLog()
	return reconcileAgentUpdateHost(cfg, logger)
}

func reconcileAgentUpdateHost(cfg BootstrapConfig, logger *BootstrapLogger) error {
	logger.Tracef("Post-update host reconciliation start: install_dir=%s", cfg.InstallDir)
	dependencyErr := ensureAgentDependenciesForUpdate(cfg, logger)
	supportTaskErr := ensureAgentSupportTasks(cfg, logger)
	err := errors.Join(dependencyErr, supportTaskErr)
	logger.Tracef("Post-update host reconciliation complete: error=%v", err)
	return err
}

func reconcileAndStartAgentAfterUpdate(cfg BootstrapConfig, logger *BootstrapLogger) error {
	reconciliationErr := reconcileAgentUpdateHost(cfg, logger)
	serviceErr := startAgentRuntime(cfg, logger)
	return errors.Join(reconciliationErr, serviceErr)
}
