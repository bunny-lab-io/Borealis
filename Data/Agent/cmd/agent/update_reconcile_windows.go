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
	reporter := newUpdateProgressReporter(configPath, logger)
	identityBefore := agentUpdateIdentityFingerprint(configPath)
	reporter.emit("staging_agent_binary", "", "success", "Agent Binary Staged After Recovery", "Deferred scoped replacement completed and installed binary was verified.", "")
	reporter.emit("reconciling_agent_host", "", "running", "Reconciling Agent Host", "Repairing dependencies, scheduled tasks, services, and runtime configuration.", "")
	if err := reconcileAgentUpdateHost(cfg, logger); err != nil {
		reporter.emit("reconciling_agent_host", "", "failed", "Agent Host Reconciliation Failed", err.Error(), "")
		return err
	}
	if identityBefore == "" || identityBefore != agentUpdateIdentityFingerprint(configPath) {
		return fmt.Errorf("Agent identity/trust verification failed after deferred replacement")
	}
	reporter.emit("protecting_agent_identity_trust", "verifying_agent_artifact", "success", "Identity/Trust Preserved", "Non-secret identity and trust fingerprint remained unchanged.", "")
	reporter.emit("reconciling_agent_host", "", "success", "Agent Host Reconciled", "Dependencies, tasks, and services reconciled.", "")
	markConfigUpdateOperation(configPath, "awaiting_reconnect", "")
	reporter.emit("waiting_agent_reconnection", "", "running", "Waiting for Agent Reconnection", "Waiting for matching heartbeat and required role health.", "")
	return nil
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
