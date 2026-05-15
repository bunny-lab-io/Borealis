//go:build windows

package main

import (
	"path/filepath"
	"strings"

	agentconfig "github.com/bunny-lab-io/borealis/go-agent/internal/config"
	agentruntime "github.com/bunny-lab-io/borealis/go-agent/internal/runtime"
)

func runStandaloneUpdateCheck(options agentruntime.Options) error {
	cfg := defaultBootstrapConfig()
	if strings.TrimSpace(options.ConfigPath) != "" {
		cfg.InstallDir = filepath.Dir(options.ConfigPath)
	}
	current, err := agentconfig.LoadOrCreate(agentruntime.ConfigPathForExecutable(filepath.Join(cfg.InstallDir, "Agent.exe")))
	if err == nil {
		cfg.ServerURL = current.ServerURL
	}
	if strings.TrimSpace(options.ServerURL) != "" {
		cfg.ServerURL = options.ServerURL
	}
	cfg.Verbose = options.Verbose
	logger, closeLog, err := openBootstrapLogger(cfg, false)
	if err != nil {
		return err
	}
	defer closeLog()
	return runAgentUpdateCheck(cfg, logger)
}
